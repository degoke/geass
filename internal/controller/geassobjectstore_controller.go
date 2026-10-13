/*
Copyright 2026 DEGOKE.

Licensed under the Elastic License 2.0 (the "License"); you may not use this
file except in compliance with the License. You may obtain a copy of the
License at LICENSE or https://www.elastic.co/licensing/elastic-license.
*/

package controller

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/cloud"
	"github.com/degoke/geass/pkg/helmchart"
	helmv1 "github.com/degoke/geass/pkg/helmchart/v1"
	"github.com/degoke/geass/pkg/platform"
)

const objectStoreFinalizer = platform.FinalizerObjectStore

var errClusterMinIOMissing = errors.New("set up the MinIO server in cluster settings first")

// GeassObjectStoreReconciler reconciles a GeassObjectStore object.
type GeassObjectStoreReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	HTTP   *http.Client
}

// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassobjectstores,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassobjectstores/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassobjectstores/finalizers,verbs=update
// +kubebuilder:rbac:groups=helm.cattle.io,resources=helmcharts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geasscloudconnections,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch

func (r *GeassObjectStoreReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var store geassv1alpha1.GeassObjectStore
	if err := r.Get(ctx, req.NamespacedName, &store); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if result, paused := beginProvisionReconcileUnlessDeleting(!store.DeletionTimestamp.IsZero(), store.Status.Conditions, store.Generation, &store.Status.ReconcileRetry); paused {
		return result, nil
	}

	clusterServer := isClusterObjectStore(&store)
	wsNS := platform.SystemNamespace
	if !clusterServer {
		if ns, err := resourceNamespace(store.Spec.Project, string(store.Spec.Environment)); err == nil {
			wsNS = ns
		}
	}

	if !controllerutil.ContainsFinalizer(&store, objectStoreFinalizer) {
		controllerutil.AddFinalizer(&store, objectStoreFinalizer)
		return ctrl.Result{}, r.Update(ctx, &store)
	}

	if !store.DeletionTimestamp.IsZero() {
		// A store that never became ready was never usable, so failed cleanup must not hold its deletion: the
		// credentials that could not create anything (no S3 or IAM permission, say) cannot clean up either.
		// Once a store has been ready, cleanup has to finish before the finalizer goes.
		tolerate := func(err error) error {
			if err == nil || strings.TrimSpace(store.Status.ConnectionSecret) != "" {
				return err
			}
			log.Info("Could not clean up a bucket that never became ready", "name", store.Name, "error", err.Error())
			return nil
		}
		if clusterServer {
			attached, attachErr := r.attachedProjectMinIONames(ctx)
			if attachErr != nil {
				return ctrl.Result{}, attachErr
			}
			if len(attached) > 0 {
				return ctrl.Result{}, fmt.Errorf("delete project buckets before removing the MinIO server: %s", strings.Join(attached, ", "))
			}
			if err := helmchart.Delete(ctx, r.Client, platform.ClusterMinIOChartName); err != nil {
				return ctrl.Result{}, err
			}
			if err := r.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: minioRootSecretName(store.Name), Namespace: platform.SystemNamespace}}); client.IgnoreNotFound(err) != nil {
				return ctrl.Result{}, err
			}
		} else if objectStorePlacement(&store) == geassv1alpha1.ObjectStorePlacementExternal {
			if platform.ObjectStoreInfrastructureActive(&store) {
				if err := tolerate(r.deleteExternalStore(ctx, deployedObjectStore(&store))); err != nil {
					return ctrl.Result{}, err
				}
			}
		} else {
			if err := tolerate(r.deleteInClusterBuckets(ctx, deployedObjectStore(&store))); err != nil {
				return ctrl.Result{}, err
			}
			if err := client.IgnoreNotFound(r.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: minioUserSecretName(store.Name), Namespace: platform.SystemNamespace}})); err != nil {
				return ctrl.Result{}, err
			}
			if platform.ObjectStoreInfrastructureActive(&store) {
				if err := r.ensureClusterMinIOHelm(ctx); err != nil && !errors.Is(err, errClusterMinIOMissing) {
					if err := tolerate(err); err != nil {
						return ctrl.Result{}, err
					}
				}
			}
		}
		_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: store.Name + "-connection", Namespace: wsNS}}))
		controllerutil.RemoveFinalizer(&store, objectStoreFinalizer)
		return ctrl.Result{}, r.Update(ctx, &store)
	}

	if !clusterServer {
		if err := r.validateObjectStoreCloudConnection(ctx, &store); err != nil {
			return r.setNotReady(ctx, &store, err.Error())
		}
		if err := platform.ValidateProjectPlacement(ctx, r.Client, store.Spec.Project, store.Spec.Environment); err != nil {
			return r.setNotReady(ctx, &store, err.Error())
		}
		ns, err := resourceNamespace(store.Spec.Project, string(store.Spec.Environment))
		if err != nil {
			return r.setNotReady(ctx, &store, err.Error())
		}
		wsNS = ns
	}

	if !clusterServer {
		if err := applyObjectStoreDeployedSpec(&store); err != nil {
			if errors.Is(err, ErrAwaitingDeploy) {
				return r.setNotReady(ctx, &store, waitingForDeployMessage)
			}
			return r.setNotReady(ctx, &store, err.Error())
		}
	}
	if objectStorePlacement(&store) == geassv1alpha1.ObjectStorePlacementExternal || store.Spec.Engine == geassv1alpha1.ObjectStoreEngineS3 {
		return r.reconcileExternal(ctx, &store, wsNS)
	}
	if store.Spec.Engine != geassv1alpha1.ObjectStoreEngineMinIO && store.Spec.Engine != "" {
		return r.setNotReady(ctx, &store, fmt.Sprintf("unsupported engine %q", store.Spec.Engine))
	}
	if clusterServer {
		return r.reconcileClusterMinIO(ctx, &store, log)
	}
	return r.reconcileProjectBucket(ctx, &store, wsNS)
}

func isClusterObjectStore(store *geassv1alpha1.GeassObjectStore) bool {
	return strings.TrimSpace(store.Spec.Project) == ""
}

func objectStorePlacement(store *geassv1alpha1.GeassObjectStore) geassv1alpha1.GeassObjectStorePlacement {
	if store.Spec.Placement == geassv1alpha1.ObjectStorePlacementExternal || store.Spec.Engine == geassv1alpha1.ObjectStoreEngineS3 {
		return geassv1alpha1.ObjectStorePlacementExternal
	}
	return geassv1alpha1.ObjectStorePlacementInCluster
}

func minioRootSecretName(storeName string) string {
	return storeName + "-root"
}

func minioUserSecretName(storeName string) string {
	return storeName + "-minio-user"
}

func (r *GeassObjectStoreReconciler) reconcileClusterMinIO(ctx context.Context, store *geassv1alpha1.GeassObjectStore, log interface{ Info(string, ...any) }) (ctrl.Result, error) {
	if patched, err := r.ensureClusterMinIOResourceSpec(ctx, store); err != nil {
		return r.setNotReady(ctx, store, err.Error())
	} else if patched {
		return ctrl.Result{Requeue: true}, nil
	}
	accessKey, secretKey, err := r.ensureMinIORootSecret(ctx, store)
	if err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	placement, pinnedNow, err := r.clusterMinIOPlacement(ctx, store)
	if err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	if pinnedNow {
		// The chart is rendered from the stored server, so let it see the pin first.
		return ctrl.Result{Requeue: true}, nil
	}
	res := clusterMinIOResources(store)
	est := platform.EstimateFromResources("MinIO server", res, 1)
	if ok, msg := workloadFitsPlacementUnlessCreated(ctx, r.Client, &helmv1.HelmChart{ObjectMeta: metav1.ObjectMeta{Name: platform.ClusterMinIOChartName, Namespace: platform.HelmChartNamespace}}, est, placement); !ok {
		return r.setNotReady(ctx, store, msg)
	}
	if err := r.ensureClusterMinIOHelm(ctx); err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	chart, err := helmchart.Get(ctx, r.Client, platform.ClusterMinIOChartName)
	if err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	ready, err := helmChartReady(ctx, r.Client, chart)
	if err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	if !ready {
		log.Info("Waiting for cluster MinIO HelmChart", "chart", platform.ClusterMinIOChartName)
		return r.setNotReady(ctx, store, installingChartMessage("MinIO"))
	}
	workloadReady, waitMsg, err := clusterMinIOWorkloadReady(ctx, r.Client)
	if err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	if !workloadReady {
		log.Info("Waiting for cluster MinIO workload", "message", waitMsg)
		return r.setNotReady(ctx, store, waitMsg)
	}
	endpoint := fmt.Sprintf("http://%s.%s.svc:9000", platform.ClusterMinIOChartName, platform.SystemNamespace)
	if err := r.reconcileConnectionSecret(ctx, store, platform.SystemNamespace, endpoint, store.Name, accessKey, secretKey); err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	return r.setReady(ctx, store, platform.SystemNamespace, endpoint, "Cluster MinIO server is ready")
}

func (r *GeassObjectStoreReconciler) ensureMinIORootSecret(ctx context.Context, store *geassv1alpha1.GeassObjectStore) (string, string, error) {
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: minioRootSecretName(store.Name), Namespace: platform.SystemNamespace}}
	accessKey, secretKey := "", ""
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		applyGeassLabels(secret, store, "GeassObjectStore")
		accessKey = firstNonEmpty(secretValue(secret, "rootUser"), secretValue(secret, platform.ConnectionKeyAccessKey))
		secretKey = firstNonEmpty(secretValue(secret, "rootPassword"), secretValue(secret, platform.ConnectionKeySecretKey))
		if accessKey == "" || secretKey == "" {
			generatedAccess, err := randomCredential()
			if err != nil {
				return err
			}
			generatedSecret, err := randomCredential()
			if err != nil {
				return err
			}
			accessKey, secretKey = generatedAccess, generatedSecret
		}
		setSecretStrings(secret, map[string]string{
			"rootUser":                      accessKey,
			"rootPassword":                  secretKey,
			platform.ConnectionKeyAccessKey: accessKey,
			platform.ConnectionKeySecretKey: secretKey,
		})
		return setSameNamespaceOwner(store, secret, r.Scheme)
	})
	if err != nil {
		return "", "", err
	}
	return accessKey, secretKey, nil
}

func (r *GeassObjectStoreReconciler) ensureClusterMinIOHelm(ctx context.Context) error {
	server, err := r.clusterMinIO(ctx)
	if err != nil {
		return err
	}
	values, err := r.clusterMinIOValues(ctx, server)
	if err != nil {
		return err
	}
	spec := helmv1.HelmChartSpec{
		Chart:           platform.MinIOReleaseChart,
		Repo:            platform.MinIOChartRepo,
		Version:         platform.MinIOChartVersion,
		TargetNamespace: platform.SystemNamespace,
		CreateNamespace: false,
		ValuesContent:   values,
	}
	return helmchart.Ensure(ctx, r.Client, platform.ClusterMinIOChartName, spec)
}

// clusterMinIOPlacement decides, once, which nodes the cluster MinIO server runs on: its volume lives
// there. The choice is the server's region (or the cluster default) and the tags it asks for, recorded
// on the server like a database's. A server that was installed before placement existed stays where
// it is. pinnedNow says the decision was just recorded.
func (r *GeassObjectStoreReconciler) clusterMinIOPlacement(ctx context.Context, store *geassv1alpha1.GeassObjectStore) (placement platform.Placement, pinnedNow bool, err error) {
	if pinned, ok := store.Annotations[platform.AnnotationNodePlacement]; ok {
		return platform.ParsePlacement(pinned), false, nil
	}
	if store.Status.TargetNamespace == "" {
		region := store.Spec.ServerRegion
		if region == "" {
			if region, err = platform.DefaultRegion(ctx, r.Client, ""); err != nil {
				return platform.Placement{}, false, err
			}
		}
		placement = platform.NewPlacement(region, store.Spec.RunOn.Tags)
	}
	latest := &geassv1alpha1.GeassObjectStore{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(store), latest); err != nil {
		return platform.Placement{}, false, err
	}
	base := latest.DeepCopy()
	if latest.Annotations == nil {
		latest.Annotations = map[string]string{}
	}
	latest.Annotations[platform.AnnotationNodePlacement] = placement.Encode()
	if err := r.Patch(ctx, latest, client.MergeFrom(base)); err != nil {
		return platform.Placement{}, false, err
	}
	return placement, true, nil
}

func pinnedStorePlacement(store *geassv1alpha1.GeassObjectStore) platform.Placement {
	if store == nil {
		return platform.Placement{}
	}
	return platform.ParsePlacement(store.Annotations[platform.AnnotationNodePlacement])
}

// minioPlacementValues are the chart values that keep the MinIO server, and the chart's own setup
// job, on the placement's nodes. The setup job only needs the region.
func minioPlacementValues(p platform.Placement) string {
	if p.IsZero() {
		return ""
	}
	values := p.HelmValues(0)
	if job := jobPlacement(p); !job.IsZero() {
		values += "postJob:\n" + job.HelmValues(2)
	}
	return values
}

func clusterMinIOResources(server *geassv1alpha1.GeassObjectStore) corev1.ResourceRequirements {
	if server != nil && len(server.Spec.Resources.Requests) > 0 {
		return server.Spec.Resources
	}
	return platform.DefaultDatabaseResources()
}

func (r *GeassObjectStoreReconciler) ensureClusterMinIOResourceSpec(ctx context.Context, store *geassv1alpha1.GeassObjectStore) (bool, error) {
	if store == nil || len(store.Spec.Resources.Requests) > 0 {
		return false, nil
	}
	original := store.DeepCopy()
	store.Spec.Resources = platform.DefaultDatabaseResources()
	if err := r.Patch(ctx, store, client.MergeFrom(original)); err != nil {
		return false, err
	}
	return true, nil
}

func (r *GeassObjectStoreReconciler) clusterMinIOValues(ctx context.Context, server *geassv1alpha1.GeassObjectStore) (string, error) {
	res := clusterMinIOResources(server)
	var b strings.Builder
	fmt.Fprintf(&b, `mode: standalone
commonLabels:
  %s: %s
existingSecret: "%s"
image:
  repository: %s
  tag: %s
  pullPolicy: IfNotPresent
mcImage:
  repository: %s
  tag: %s
  pullPolicy: IfNotPresent
resources:
  requests:
    cpu: %s
    memory: %s
  limits:
    cpu: %s
    memory: %s
persistence:
  size: 10Gi
%s`, platform.LabelManagedBy, platform.ManagedByValue, minioRootSecretName(server.Name),
		platform.MinIOImageRepository, platform.MinIOImageTag,
		platform.MinIOMCImageRepository, platform.MinIOMCImageTag,
		resourceQuantity(res.Requests, corev1.ResourceCPU), resourceQuantity(res.Requests, corev1.ResourceMemory),
		resourceQuantity(res.Limits, corev1.ResourceCPU), resourceQuantity(res.Limits, corev1.ResourceMemory),
		minioPlacementValues(pinnedStorePlacement(server)))
	users, policies, err := r.minioBucketIdentities(ctx)
	if err != nil {
		return "", err
	}
	if len(users) == 0 {
		b.WriteString("users: []\npolicies: []\n")
		return b.String(), nil
	}
	b.WriteString("users:\n")
	for _, user := range users {
		fmt.Fprintf(&b, "  - accessKey: '{{ required \"MinIO user accessKey is missing\" (index (lookup \"v1\" \"Secret\" \"%s\" \"%s\").data \"%s\" | b64dec) }}'\n    existingSecret: \"%s\"\n    existingSecretKey: secretKey\n    policy: \"%s\"\n", platform.SystemNamespace, user.existingSecret, platform.ConnectionKeyAccessKey, user.existingSecret, user.policy)
	}
	b.WriteString("policies:\n")
	for _, policy := range policies {
		fmt.Fprintf(&b, "  - name: \"%s\"\n    statements:\n", policy.name)
		for _, bucket := range policy.buckets {
			fmt.Fprintf(&b, `      - effect: Allow
        resources:
          - "arn:aws:s3:::%s"
        actions:
          - "s3:GetBucketLocation"
          - "s3:ListBucket"
          - "s3:ListBucketMultipartUploads"
      - effect: Allow
        resources:
          - "arn:aws:s3:::%s/*"
        actions:
          - "s3:GetObject"
          - "s3:PutObject"
          - "s3:DeleteObject"
          - "s3:AbortMultipartUpload"
          - "s3:ListMultipartUploadParts"
`, bucket, bucket)
		}
	}
	return b.String(), nil
}

type minioBucketUser struct {
	existingSecret string
	policy         string
}

type minioBucketPolicy struct {
	name    string
	buckets []string
}

func (r *GeassObjectStoreReconciler) minioBucketIdentities(ctx context.Context) ([]minioBucketUser, []minioBucketPolicy, error) {
	var list geassv1alpha1.GeassObjectStoreList
	if err := r.List(ctx, &list, client.InNamespace(platform.SystemNamespace)); err != nil {
		return nil, nil, err
	}
	var users []minioBucketUser
	var policies []minioBucketPolicy
	for i := range list.Items {
		store := &list.Items[i]
		if isClusterObjectStore(store) || !store.DeletionTimestamp.IsZero() {
			continue
		}
		if objectStorePlacement(store) == geassv1alpha1.ObjectStorePlacementExternal {
			continue
		}
		if store.Spec.Engine != "" && store.Spec.Engine != geassv1alpha1.ObjectStoreEngineMinIO {
			continue
		}
		secret := &corev1.Secret{}
		if err := r.Get(ctx, client.ObjectKey{Name: minioUserSecretName(store.Name), Namespace: platform.SystemNamespace}, secret); err != nil {
			continue
		}
		secretKey := secretValue(secret, platform.ConnectionKeySecretKey)
		if secretKey == "" {
			continue
		}
		buckets, err := inClusterBucketNames(deployedObjectStore(store))
		if err != nil {
			// The store's own reconcile reports the name. It must not stop the chart values being rendered for
			// every other store, and for the MinIO server itself.
			continue
		}
		policyName := minioPolicyName(store.Name)
		users = append(users, minioBucketUser{existingSecret: minioUserSecretName(store.Name), policy: policyName})
		policies = append(policies, minioBucketPolicy{name: policyName, buckets: buckets})
	}
	return users, policies, nil
}

func minioPolicyName(storeName string) string {
	return "geass-" + storeName
}

func isClusterMinIOHelmJob(name string) bool {
	return strings.HasPrefix(name, "helm-install-"+platform.ClusterMinIOChartName)
}

// objectStoreBucketNames returns buckets explicitly configured on the spec (draft stores have none).
func objectStoreBucketNames(store *geassv1alpha1.GeassObjectStore) ([]string, error) {
	buckets := store.Spec.Buckets
	for _, bucket := range buckets {
		if err := platform.ValidBucketName(bucket); err != nil {
			return nil, err
		}
	}
	return buckets, nil
}

// inClusterBucketNames are the names the store's buckets have on the cluster MinIO server: the names in the spec,
// each behind the project environment, so that projects never share a bucket.
func inClusterBucketNames(store *geassv1alpha1.GeassObjectStore) ([]string, error) {
	buckets, err := objectStoreBucketNames(store)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(buckets))
	for i, bucket := range buckets {
		if names[i], err = platform.InClusterBucketName(store.Spec.Project, string(store.Spec.Environment), bucket); err != nil {
			return nil, err
		}
	}
	return names, nil
}

// deployedObjectStore is the store carrying the spec that was last deployed. A saved edit stays pending until it
// is deployed, so what exists on MinIO or in AWS follows the deployed spec: that is what to grant access to and
// what to remove. A store with nothing deployed keeps its own spec. With no edit pending it is the store itself,
// so the result must be treated as read-only.
func deployedObjectStore(store *geassv1alpha1.GeassObjectStore) *geassv1alpha1.GeassObjectStore {
	if platform.PendingUpdates(store.Annotations) == 0 {
		return store
	}
	deployed := store.DeepCopy()
	// An error leaves the spec as it was, which is the best there is when no snapshot was recorded.
	_ = applyObjectStoreDeployedSpec(deployed)
	return deployed
}

// markProvisioningStarted records, before the first bucket or user is created, that the store may own resources
// outside the cluster. Deleting a store that never became ready would otherwise leave them behind, because a
// store is only known to own anything once its connection secret is written, which happens last. The annotation
// is patched onto a fresh copy so that the deployed spec the reconcile is running is never written back.
func (r *GeassObjectStoreReconciler) markProvisioningStarted(ctx context.Context, store *geassv1alpha1.GeassObjectStore) error {
	if store.Annotations[platform.AnnotationProvisioningStarted] == "true" {
		return nil
	}
	latest := &geassv1alpha1.GeassObjectStore{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(store), latest); err != nil {
		return err
	}
	if latest.Annotations[platform.AnnotationProvisioningStarted] != "true" {
		base := latest.DeepCopy()
		if latest.Annotations == nil {
			latest.Annotations = map[string]string{}
		}
		latest.Annotations[platform.AnnotationProvisioningStarted] = "true"
		if err := r.Patch(ctx, latest, client.MergeFrom(base)); err != nil {
			return err
		}
	}
	if store.Annotations == nil {
		store.Annotations = map[string]string{}
	}
	store.Annotations[platform.AnnotationProvisioningStarted] = "true"
	return nil
}

func (r *GeassObjectStoreReconciler) reconcileProjectBucket(ctx context.Context, store *geassv1alpha1.GeassObjectStore, wsNS string) (ctrl.Result, error) {
	serverBuckets, err := inClusterBucketNames(store)
	if err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	if len(serverBuckets) == 0 {
		return r.setNotReady(ctx, store, "Configure the bucket name in settings, then deploy")
	}
	server, err := r.clusterMinIO(ctx)
	if err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	if conditionStatus(server.Status.Conditions, platform.ConditionReady) != string(metav1.ConditionTrue) || server.Status.Endpoint == "" {
		return r.setNotReady(ctx, store, "Set up the MinIO server in cluster settings first")
	}
	// A project that requires node tags must not keep its objects on a server that runs elsewhere.
	if required, err := platform.ProjectRequiredTags(ctx, r.Client, store.Spec.Project); err == nil {
		if message := platform.ComplianceMismatchMessage("MinIO server", pinnedStorePlacement(server), required); message != "" {
			return r.setNotReady(ctx, store, message)
		}
	}
	rootSecret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Name: minioRootSecretName(server.Name), Namespace: platform.SystemNamespace}, rootSecret); err != nil {
		return r.setNotReady(ctx, store, "cluster MinIO credentials are unavailable")
	}
	accessKey := firstNonEmpty(secretValue(rootSecret, "rootUser"), secretValue(rootSecret, platform.ConnectionKeyAccessKey))
	secretKey := firstNonEmpty(secretValue(rootSecret, "rootPassword"), secretValue(rootSecret, platform.ConnectionKeySecretKey))
	if accessKey == "" || secretKey == "" {
		return r.setNotReady(ctx, store, "cluster MinIO credentials are unavailable")
	}
	s3 := &cloud.AWSClient{HTTP: r.HTTP, AccessKey: accessKey, SecretKey: secretKey, Endpoint: server.Status.Endpoint}
	if err := r.markProvisioningStarted(ctx, store); err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	for _, bucket := range serverBuckets {
		if err := s3.EnsureBucket(bucket); err != nil {
			return r.setNotReady(ctx, store, err.Error())
		}
	}
	userAccess, userSecret, err := r.ensureProjectBucketKeys(ctx, store, wsNS, server.Status.Endpoint, serverBuckets[0])
	if err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	if err := r.ensureMinIOUserSecret(ctx, store, userAccess, userSecret); err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	previousGeneration, previousJobUID := r.clusterMinIOHelmSnapshot(ctx)
	if err := r.ensureClusterMinIOHelm(ctx); err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	if err := r.persistStaleMinIOHelmJob(ctx, previousGeneration, previousJobUID); err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	ready, err := r.clusterMinIOHelmReady(ctx)
	if err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	if !ready {
		return r.setNotReady(ctx, store, installingChartMessage("MinIO"))
	}
	workloadReady, waitMsg, err := clusterMinIOWorkloadReady(ctx, r.Client)
	if err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	if !workloadReady {
		return r.setNotReady(ctx, store, waitMsg)
	}
	if err := r.clearStaleMinIOHelmJob(ctx); err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	if err := r.reconcileConnectionSecret(ctx, store, wsNS, server.Status.Endpoint, serverBuckets[0], userAccess, userSecret); err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	return r.setReady(ctx, store, wsNS, server.Status.Endpoint, "Bucket is ready on the cluster MinIO server")
}

func (r *GeassObjectStoreReconciler) ensureProjectBucketKeys(ctx context.Context, store *geassv1alpha1.GeassObjectStore, wsNS, endpoint, bucket string) (string, string, error) {
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: store.Name + "-connection", Namespace: wsNS}}
	accessKey, secretKey := "", ""
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		applyGeassLabels(secret, store, "GeassObjectStore")
		accessKey = store.Name
		secretKey = secretValue(secret, platform.ConnectionKeySecretKey)
		if secretKey == "" {
			generatedSecret, err := randomCredential()
			if err != nil {
				return err
			}
			secretKey = generatedSecret
		}
		setSecretStrings(secret, map[string]string{
			platform.ConnectionKeyEndpoint:  endpoint,
			platform.ConnectionKeyAccessKey: accessKey,
			platform.ConnectionKeySecretKey: secretKey,
			platform.ConnectionKeyBucket:    bucket,
		})
		return setSameNamespaceOwner(store, secret, r.Scheme)
	})
	return accessKey, secretKey, err
}

func (r *GeassObjectStoreReconciler) ensureMinIOUserSecret(ctx context.Context, store *geassv1alpha1.GeassObjectStore, accessKey, secretKey string) error {
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: minioUserSecretName(store.Name), Namespace: platform.SystemNamespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		applyGeassLabels(secret, store, "GeassObjectStore")
		setSecretStrings(secret, map[string]string{
			platform.ConnectionKeyAccessKey: accessKey,
			platform.ConnectionKeySecretKey: secretKey,
		})
		return setSameNamespaceOwner(store, secret, r.Scheme)
	})
	return err
}

func (r *GeassObjectStoreReconciler) clusterMinIOHelmSnapshot(ctx context.Context) (int64, types.UID) {
	chart, err := helmchart.Get(ctx, r.Client, platform.ClusterMinIOChartName)
	if err != nil {
		return 0, ""
	}
	if chart.Status.JobName == "" {
		return chart.Generation, ""
	}
	job := &batchv1.Job{}
	if err := r.Get(ctx, client.ObjectKey{Name: chart.Status.JobName, Namespace: chart.Namespace}, job); err != nil {
		return chart.Generation, ""
	}
	return chart.Generation, job.UID
}

func (r *GeassObjectStoreReconciler) persistStaleMinIOHelmJob(ctx context.Context, previousGeneration int64, previousJobUID types.UID) error {
	if previousJobUID == "" {
		return nil
	}
	chart, err := helmchart.Get(ctx, r.Client, platform.ClusterMinIOChartName)
	if err != nil {
		return err
	}
	if chart.Generation <= previousGeneration {
		return nil
	}
	if chart.Annotations[platform.HelmStaleJobUIDAnnotation] == string(previousJobUID) {
		return nil
	}
	latest := chart.DeepCopy()
	if err := r.Get(ctx, client.ObjectKeyFromObject(chart), latest); err != nil {
		return err
	}
	if latest.Annotations == nil {
		latest.Annotations = map[string]string{}
	}
	latest.Annotations[platform.HelmStaleJobUIDAnnotation] = string(previousJobUID)
	return r.Update(ctx, latest)
}

func (r *GeassObjectStoreReconciler) clusterMinIOHelmReady(ctx context.Context) (bool, error) {
	chart, err := helmchart.Get(ctx, r.Client, platform.ClusterMinIOChartName)
	if err != nil {
		return false, err
	}
	ready, err := helmChartReady(ctx, r.Client, chart)
	if err != nil || !ready {
		return false, err
	}
	staleUID := ""
	if chart.Annotations != nil {
		staleUID = chart.Annotations[platform.HelmStaleJobUIDAnnotation]
	}
	if staleUID == "" {
		return true, nil
	}
	job := &batchv1.Job{}
	if err := r.Get(ctx, client.ObjectKey{Name: chart.Status.JobName, Namespace: chart.Namespace}, job); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	if string(job.UID) == staleUID {
		return false, nil
	}
	return job.Status.Succeeded > 0, nil
}

func (r *GeassObjectStoreReconciler) clearStaleMinIOHelmJob(ctx context.Context) error {
	chart, err := helmchart.Get(ctx, r.Client, platform.ClusterMinIOChartName)
	if err != nil {
		return err
	}
	if chart.Annotations == nil || chart.Annotations[platform.HelmStaleJobUIDAnnotation] == "" {
		return nil
	}
	latest := chart.DeepCopy()
	if err := r.Get(ctx, client.ObjectKeyFromObject(chart), latest); err != nil {
		return err
	}
	delete(latest.Annotations, platform.HelmStaleJobUIDAnnotation)
	return r.Update(ctx, latest)
}

func (r *GeassObjectStoreReconciler) validateObjectStoreCloudConnection(ctx context.Context, store *geassv1alpha1.GeassObjectStore) error {
	if store.Spec.ConnectionRef == nil || strings.TrimSpace(store.Spec.ConnectionRef.Name) == "" {
		return nil
	}
	connection := &geassv1alpha1.GeassCloudConnection{}
	if err := r.Get(ctx, client.ObjectKey{Name: store.Spec.ConnectionRef.Name, Namespace: platform.SystemNamespace}, connection); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("cloud connection %q was not found", store.Spec.ConnectionRef.Name)
		}
		return fmt.Errorf("cloud connection %q is unavailable", store.Spec.ConnectionRef.Name)
	}
	return platform.ValidateCloudConnectionForProject(*connection, store.Spec.Project)
}

func (r *GeassObjectStoreReconciler) deleteExternalStore(ctx context.Context, store *geassv1alpha1.GeassObjectStore) error {
	if store.Spec.ConnectionRef == nil || store.Spec.ConnectionRef.Name == "" {
		return fmt.Errorf("AWS connection is required to delete this store")
	}
	connection := &geassv1alpha1.GeassCloudConnection{}
	if err := r.Get(ctx, client.ObjectKey{Name: store.Spec.ConnectionRef.Name, Namespace: platform.SystemNamespace}, connection); err != nil {
		return fmt.Errorf("AWS connection is unavailable: %w", err)
	}
	if err := platform.ValidateCloudConnectionForProject(*connection, store.Spec.Project); err != nil {
		return err
	}
	if connection.Spec.Provider != geassv1alpha1.CloudProviderAWS || connection.Spec.SecretRef.Name == "" {
		return fmt.Errorf("AWS connection is unavailable")
	}
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Name: connection.Spec.SecretRef.Name, Namespace: platform.SystemNamespace}, secret); err != nil {
		return fmt.Errorf("AWS credentials are unavailable: %w", err)
	}
	accessKey := secretValue(secret, platform.SecretKeyAccessKeyID)
	secretKey := secretValue(secret, platform.SecretKeySecretAccessKey)
	if accessKey == "" || secretKey == "" {
		return fmt.Errorf("AWS credentials are unavailable")
	}
	aws := &cloud.AWSClient{
		HTTP:      r.HTTP,
		AccessKey: accessKey,
		SecretKey: secretKey,
		Region:    firstNonEmpty(store.Spec.Region, connection.Spec.Region, secretValue(secret, platform.SecretKeyRegion), "us-east-1"),
	}
	return r.deleteExternalAWS(store, aws)
}

func (r *GeassObjectStoreReconciler) deleteExternalAWS(store *geassv1alpha1.GeassObjectStore, aws *cloud.AWSClient) error {
	if store.Spec.CreateBucket {
		// Names that are missing or not valid mean no bucket was created, so none is removed: never one that
		// merely shares the resource's name.
		buckets, _ := objectStoreBucketNames(store)
		for _, bucket := range buckets {
			if err := aws.DeleteBucket(bucket); err != nil {
				return err
			}
		}
	}
	return aws.DeleteBucketUser(store.Name)
}

func (r *GeassObjectStoreReconciler) deleteInClusterBuckets(ctx context.Context, store *geassv1alpha1.GeassObjectStore) error {
	if !platform.ObjectStoreInfrastructureActive(store) {
		return nil
	}
	buckets, err := inClusterBucketNames(store)
	if err != nil || len(buckets) == 0 {
		// Names that cannot be stored, or none, mean no bucket was created, so none is removed: never one that
		// merely shares the resource's name.
		return nil
	}
	server, err := r.clusterMinIO(ctx)
	if err != nil {
		if errors.Is(err, errClusterMinIOMissing) {
			return nil
		}
		return err
	}
	if server.Status.Endpoint == "" {
		return fmt.Errorf("cluster MinIO endpoint is unavailable")
	}
	rootSecret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Name: minioRootSecretName(server.Name), Namespace: platform.SystemNamespace}, rootSecret); err != nil {
		return fmt.Errorf("cluster MinIO credentials are unavailable: %w", err)
	}
	accessKey := firstNonEmpty(secretValue(rootSecret, "rootUser"), secretValue(rootSecret, platform.ConnectionKeyAccessKey))
	secretKey := firstNonEmpty(secretValue(rootSecret, "rootPassword"), secretValue(rootSecret, platform.ConnectionKeySecretKey))
	if accessKey == "" || secretKey == "" {
		return fmt.Errorf("cluster MinIO credentials are unavailable")
	}
	s3 := &cloud.AWSClient{HTTP: r.HTTP, AccessKey: accessKey, SecretKey: secretKey, Endpoint: server.Status.Endpoint}
	for _, bucket := range buckets {
		if err := s3.DeleteBucket(bucket); err != nil {
			return err
		}
	}
	return nil
}

func (r *GeassObjectStoreReconciler) clusterMinIO(ctx context.Context) (*geassv1alpha1.GeassObjectStore, error) {
	var list geassv1alpha1.GeassObjectStoreList
	if err := r.List(ctx, &list, client.InNamespace(platform.SystemNamespace)); err != nil {
		return nil, err
	}
	for i := range list.Items {
		item := &list.Items[i]
		if !isClusterObjectStore(item) {
			continue
		}
		if objectStorePlacement(item) == geassv1alpha1.ObjectStorePlacementExternal {
			continue
		}
		if item.Spec.Engine != "" && item.Spec.Engine != geassv1alpha1.ObjectStoreEngineMinIO {
			continue
		}
		return item, nil
	}
	return nil, errClusterMinIOMissing
}

func (r *GeassObjectStoreReconciler) attachedProjectMinIONames(ctx context.Context) ([]string, error) {
	var list geassv1alpha1.GeassObjectStoreList
	if err := r.List(ctx, &list, client.InNamespace(platform.SystemNamespace)); err != nil {
		return nil, err
	}
	var names []string
	for i := range list.Items {
		item := &list.Items[i]
		if isClusterObjectStore(item) || !item.DeletionTimestamp.IsZero() {
			continue
		}
		if objectStorePlacement(item) == geassv1alpha1.ObjectStorePlacementExternal {
			continue
		}
		if item.Spec.Engine != "" && item.Spec.Engine != geassv1alpha1.ObjectStoreEngineMinIO {
			continue
		}
		names = append(names, item.Name)
	}
	return names, nil
}

func (r *GeassObjectStoreReconciler) reconcileExternal(ctx context.Context, store *geassv1alpha1.GeassObjectStore, wsNS string) (ctrl.Result, error) {
	buckets, err := objectStoreBucketNames(store)
	if err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	if len(buckets) == 0 {
		return r.setNotReady(ctx, store, "Configure the bucket name in settings, then deploy")
	}
	if store.Spec.ConnectionRef == nil || store.Spec.ConnectionRef.Name == "" {
		return r.setNotReady(ctx, store, "an AWS connection is required for external buckets")
	}
	connection := &geassv1alpha1.GeassCloudConnection{}
	if err := r.Get(ctx, client.ObjectKey{Name: store.Spec.ConnectionRef.Name, Namespace: platform.SystemNamespace}, connection); err != nil {
		return r.setNotReady(ctx, store, "AWS connection is unavailable")
	}
	if err := platform.ValidateCloudConnectionForProject(*connection, store.Spec.Project); err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	if connection.Spec.Provider != geassv1alpha1.CloudProviderAWS {
		return r.setNotReady(ctx, store, "external buckets require an AWS connection")
	}
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Name: connection.Spec.SecretRef.Name, Namespace: platform.SystemNamespace}, secret); err != nil {
		return r.setNotReady(ctx, store, "AWS credentials are unavailable")
	}
	accessKey := secretValue(secret, platform.SecretKeyAccessKeyID)
	secretKey := secretValue(secret, platform.SecretKeySecretAccessKey)
	region := firstNonEmpty(store.Spec.Region, connection.Spec.Region, secretValue(secret, platform.SecretKeyRegion), "us-east-1")
	if accessKey == "" || secretKey == "" {
		return r.setNotReady(ctx, store, "AWS access key and secret key are required")
	}
	aws := &cloud.AWSClient{HTTP: r.HTTP, AccessKey: accessKey, SecretKey: secretKey, Region: region}
	if err := r.markProvisioningStarted(ctx, store); err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	if store.Spec.CreateBucket {
		for _, bucket := range buckets {
			if err := aws.EnsureBucket(bucket); err != nil {
				return r.setNotReady(ctx, store, err.Error())
			}
		}
	}
	existing := &corev1.Secret{}
	existingAccess, existingSecret := "", ""
	if err := r.Get(ctx, client.ObjectKey{Name: store.Name + "-connection", Namespace: wsNS}, existing); err == nil {
		existingAccess = secretValue(existing, platform.ConnectionKeyAccessKey)
		existingSecret = secretValue(existing, platform.ConnectionKeySecretKey)
	} else if !apierrors.IsNotFound(err) {
		return r.setNotReady(ctx, store, err.Error())
	}
	userAccess, userSecret, err := aws.EnsureBucketUser(buckets, store.Name, existingAccess, existingSecret)
	if err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	endpoint := fmt.Sprintf("https://s3.%s.amazonaws.com", region)
	if err := r.reconcileConnectionSecret(ctx, store, wsNS, endpoint, buckets[0], userAccess, userSecret); err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	return r.setReady(ctx, store, wsNS, endpoint, "AWS S3 bucket is ready")
}

func (r *GeassObjectStoreReconciler) reconcileConnectionSecret(ctx context.Context, store *geassv1alpha1.GeassObjectStore, wsNS, endpoint, bucket, accessKey, secretKey string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: store.Name + "-connection", Namespace: wsNS},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		applyGeassLabels(secret, store, "GeassObjectStore")
		setSecretStrings(secret, map[string]string{
			platform.ConnectionKeyEndpoint:  endpoint,
			platform.ConnectionKeyAccessKey: accessKey,
			platform.ConnectionKeySecretKey: secretKey,
			platform.ConnectionKeyBucket:    bucket,
		})
		return setSameNamespaceOwner(store, secret, r.Scheme)
	})
	return err
}

func (r *GeassObjectStoreReconciler) setReady(ctx context.Context, store *geassv1alpha1.GeassObjectStore, wsNS, endpoint, message string) (ctrl.Result, error) {
	latest := &geassv1alpha1.GeassObjectStore{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(store), latest); err != nil {
		return ctrl.Result{}, err
	}
	_, err := platform.PatchStatus(ctx, r.Client, latest, func() {
		latest.Status.TargetNamespace = wsNS
		latest.Status.ConnectionSecret = store.Name + "-connection"
		latest.Status.Endpoint = endpoint
		retryState := reconcileRetryFromAPI(&latest.Status.ReconcileRetry)
		platform.ClearReconcileRetry(&retryState)
		writeReconcileRetryToAPI(&latest.Status.ReconcileRetry, retryState)
		completeProvisionSuccess(&latest.Status.Provision)
		latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, metav1.ConditionTrue, "ObjectStoreReady", message)
	})
	return ctrl.Result{}, err
}

func (r *GeassObjectStoreReconciler) setNotReady(ctx context.Context, store *geassv1alpha1.GeassObjectStore, message string) (ctrl.Result, error) {
	latest := &geassv1alpha1.GeassObjectStore{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(store), latest); err != nil {
		return ctrl.Result{}, err
	}
	var result ctrl.Result
	if _, err := platform.PatchStatus(ctx, r.Client, latest, func() {
		var conditions []metav1.Condition
		result, conditions = finishNotReady(latest.Status.Conditions, &latest.Status.ReconcileRetry, &latest.Status.Provision, latest.Generation, message)
		latest.Status.Conditions = conditions
	}); err != nil {
		return ctrl.Result{}, err
	}
	return result, nil
}

func randomCredential() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GeassObjectStoreReconciler) SetupWithManager(mgr ctrl.Manager) error {
	enqueueStores := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, _ client.Object) []ctrl.Request {
		var list geassv1alpha1.GeassObjectStoreList
		if err := r.List(ctx, &list, client.InNamespace(platform.SystemNamespace)); err != nil {
			return nil
		}
		requests := make([]ctrl.Request, 0, len(list.Items))
		for i := range list.Items {
			store := &list.Items[i]
			requests = append(requests, ctrl.Request{NamespacedName: types.NamespacedName{Name: store.Name, Namespace: store.Namespace}})
		}
		return requests
	})
	return ctrl.NewControllerManagedBy(mgr).
		For(&geassv1alpha1.GeassObjectStore{}).
		Watches(&helmv1.HelmChart{}, enqueueStores, builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			return obj.GetName() == platform.ClusterMinIOChartName
		}))).
		Watches(&batchv1.Job{}, enqueueStores, builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			return obj.GetNamespace() == platform.HelmChartNamespace && isClusterMinIOHelmJob(obj.GetName())
		}))).
		Watches(&corev1.Pod{}, enqueueStores, builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
			pod, ok := obj.(*corev1.Pod)
			return ok && isClusterMinIOWorkloadPod(pod)
		}))).
		// A project changing its region or required tags changes which MinIO server it may use.
		Watches(&geassv1alpha1.GeassProject{}, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, project client.Object) []ctrl.Request {
			var list geassv1alpha1.GeassObjectStoreList
			if err := r.List(ctx, &list, client.InNamespace(platform.SystemNamespace)); err != nil {
				return nil
			}
			var requests []ctrl.Request
			for i := range list.Items {
				if list.Items[i].Spec.Project == project.GetName() {
					requests = append(requests, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
				}
			}
			return requests
		}), builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Named("geassobjectstore").
		Complete(r)
}
