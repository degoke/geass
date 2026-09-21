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

func (r *GeassObjectStoreReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var store geassv1alpha1.GeassObjectStore
	if err := r.Get(ctx, req.NamespacedName, &store); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	clusterServer := isClusterObjectStore(&store)
	wsNS := platform.SystemNamespace
	if !clusterServer {
		if err := platform.ValidateProjectPlacement(ctx, r.Client, store.Spec.Project, store.Spec.Environment); err != nil {
			return r.setNotReady(ctx, &store, err.Error())
		}
		ns, err := resourceNamespace(store.Spec.Project, string(store.Spec.Environment))
		if err != nil {
			return r.setNotReady(ctx, &store, err.Error())
		}
		wsNS = ns
	}

	if !controllerutil.ContainsFinalizer(&store, objectStoreFinalizer) {
		controllerutil.AddFinalizer(&store, objectStoreFinalizer)
		return ctrl.Result{}, r.Update(ctx, &store)
	}

	if !store.DeletionTimestamp.IsZero() {
		if clusterServer {
			_ = helmchart.Delete(ctx, r.Client, platform.ClusterMinIOChartName)
			_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: minioRootSecretName(store.Name), Namespace: platform.SystemNamespace}}))
		} else if objectStorePlacement(&store) == geassv1alpha1.ObjectStorePlacementExternal {
			if err := r.deleteExternalStore(ctx, &store); err != nil {
				return ctrl.Result{}, err
			}
		} else {
			_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: minioUserSecretName(store.Name), Namespace: platform.SystemNamespace}}))
			_ = r.ensureClusterMinIOHelm(ctx)
		}
		_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: store.Name + "-connection", Namespace: wsNS}}))
		controllerutil.RemoveFinalizer(&store, objectStoreFinalizer)
		return ctrl.Result{}, r.Update(ctx, &store)
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
	accessKey, secretKey, err := r.ensureMinIORootSecret(ctx, store)
	if err != nil {
		return r.setNotReady(ctx, store, err.Error())
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
		return r.setNotReady(ctx, store, "MinIO HelmChart is not ready")
	}
	endpoint := fmt.Sprintf("http://%s.%s.svc:9000", platform.ClusterMinIOChartName, platform.SystemNamespace)
	if err := r.reconcileConnectionSecret(ctx, store, platform.SystemNamespace, endpoint, accessKey, secretKey); err != nil {
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
		secret.StringData = map[string]string{
			"rootUser":                      accessKey,
			"rootPassword":                  secretKey,
			platform.ConnectionKeyAccessKey: accessKey,
			platform.ConnectionKeySecretKey: secretKey,
		}
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

func (r *GeassObjectStoreReconciler) clusterMinIOValues(ctx context.Context, server *geassv1alpha1.GeassObjectStore) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, `mode: standalone
commonLabels:
  %s: %s
existingSecret: "%s"
`, platform.LabelManagedBy, platform.ManagedByValue, minioRootSecretName(server.Name))
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
		fmt.Fprintf(&b, "  - accessKey: \"%s\"\n    existingSecret: \"%s\"\n    existingSecretKey: secretKey\n    policy: \"%s\"\n", user.accessKey, user.existingSecret, user.policy)
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
	accessKey      string
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
		buckets, err := objectStoreBuckets(store)
		if err != nil {
			return nil, nil, err
		}
		policyName := minioPolicyName(store.Name)
		users = append(users, minioBucketUser{accessKey: store.Name, existingSecret: minioUserSecretName(store.Name), policy: policyName})
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

func objectStoreBuckets(store *geassv1alpha1.GeassObjectStore) ([]string, error) {
	buckets := store.Spec.Buckets
	if len(buckets) == 0 {
		buckets = []string{store.Name}
	}
	for _, bucket := range buckets {
		if err := platform.ValidBucketName(bucket); err != nil {
			return nil, err
		}
	}
	return buckets, nil
}

func (r *GeassObjectStoreReconciler) reconcileProjectBucket(ctx context.Context, store *geassv1alpha1.GeassObjectStore, wsNS string) (ctrl.Result, error) {
	server, err := r.clusterMinIO(ctx)
	if err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	if conditionStatus(server.Status.Conditions, platform.ConditionReady) != string(metav1.ConditionTrue) || server.Status.Endpoint == "" {
		return r.setNotReady(ctx, store, "Set up the MinIO server in cluster settings first")
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
	buckets, err := objectStoreBuckets(store)
	if err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	s3 := &cloud.AWSClient{HTTP: r.HTTP, AccessKey: accessKey, SecretKey: secretKey, Endpoint: server.Status.Endpoint}
	for _, bucket := range buckets {
		if err := s3.EnsureBucket(bucket); err != nil {
			return r.setNotReady(ctx, store, err.Error())
		}
	}
	userAccess, userSecret, err := r.ensureProjectBucketKeys(ctx, store, wsNS, server.Status.Endpoint)
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
		return r.setNotReady(ctx, store, "MinIO HelmChart is not ready")
	}
	if err := r.reconcileConnectionSecret(ctx, store, wsNS, server.Status.Endpoint, userAccess, userSecret); err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	return r.setReady(ctx, store, wsNS, server.Status.Endpoint, "Bucket is ready on the cluster MinIO server")
}

func (r *GeassObjectStoreReconciler) ensureProjectBucketKeys(ctx context.Context, store *geassv1alpha1.GeassObjectStore, wsNS, endpoint string) (string, string, error) {
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
		buckets, err := objectStoreBuckets(store)
		if err != nil {
			return err
		}
		secret.StringData = map[string]string{
			platform.ConnectionKeyEndpoint:  endpoint,
			platform.ConnectionKeyAccessKey: accessKey,
			platform.ConnectionKeySecretKey: secretKey,
			platform.ConnectionKeyBucket:    buckets[0],
		}
		return setSameNamespaceOwner(store, secret, r.Scheme)
	})
	return accessKey, secretKey, err
}

func (r *GeassObjectStoreReconciler) ensureMinIOUserSecret(ctx context.Context, store *geassv1alpha1.GeassObjectStore, accessKey, secretKey string) error {
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: minioUserSecretName(store.Name), Namespace: platform.SystemNamespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		applyGeassLabels(secret, store, "GeassObjectStore")
		secret.StringData = map[string]string{
			platform.ConnectionKeyAccessKey: accessKey,
			platform.ConnectionKeySecretKey: secretKey,
		}
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

func (r *GeassObjectStoreReconciler) deleteExternalStore(ctx context.Context, store *geassv1alpha1.GeassObjectStore) error {
	if store.Spec.ConnectionRef == nil || store.Spec.ConnectionRef.Name == "" {
		return nil
	}
	connection := &geassv1alpha1.GeassCloudConnection{}
	if err := r.Get(ctx, client.ObjectKey{Name: store.Spec.ConnectionRef.Name, Namespace: platform.SystemNamespace}, connection); err != nil {
		return client.IgnoreNotFound(err)
	}
	if connection.Spec.Provider != geassv1alpha1.CloudProviderAWS || connection.Spec.SecretRef.Name == "" {
		return nil
	}
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Name: connection.Spec.SecretRef.Name, Namespace: platform.SystemNamespace}, secret); err != nil {
		return client.IgnoreNotFound(err)
	}
	accessKey := secretValue(secret, platform.SecretKeyAccessKeyID)
	secretKey := secretValue(secret, platform.SecretKeySecretAccessKey)
	if accessKey == "" || secretKey == "" {
		return nil
	}
	aws := &cloud.AWSClient{
		HTTP:      r.HTTP,
		AccessKey: accessKey,
		SecretKey: secretKey,
		Region:    firstNonEmpty(store.Spec.Region, connection.Spec.Region, secretValue(secret, platform.SecretKeyRegion), "us-east-1"),
	}
	buckets, err := objectStoreBuckets(store)
	if err != nil {
		buckets = []string{store.Name}
	}
	for _, bucket := range buckets {
		if err := aws.DeleteBucket(bucket); err != nil {
			return err
		}
	}
	return aws.DeleteBucketUser(store.Name)
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
	return nil, fmt.Errorf("set up the MinIO server in cluster settings first")
}

func (r *GeassObjectStoreReconciler) reconcileExternal(ctx context.Context, store *geassv1alpha1.GeassObjectStore, wsNS string) (ctrl.Result, error) {
	if store.Spec.ConnectionRef == nil || store.Spec.ConnectionRef.Name == "" {
		return r.setNotReady(ctx, store, "an AWS connection is required for external buckets")
	}
	connection := &geassv1alpha1.GeassCloudConnection{}
	if err := r.Get(ctx, client.ObjectKey{Name: store.Spec.ConnectionRef.Name, Namespace: platform.SystemNamespace}, connection); err != nil {
		return r.setNotReady(ctx, store, "AWS connection is unavailable")
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
	buckets, err := objectStoreBuckets(store)
	if err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	aws := &cloud.AWSClient{HTTP: r.HTTP, AccessKey: accessKey, SecretKey: secretKey, Region: region}
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
	if err := r.reconcileConnectionSecret(ctx, store, wsNS, endpoint, userAccess, userSecret); err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	return r.setReady(ctx, store, wsNS, endpoint, "AWS S3 bucket is ready")
}

func (r *GeassObjectStoreReconciler) reconcileConnectionSecret(ctx context.Context, store *geassv1alpha1.GeassObjectStore, wsNS, endpoint, accessKey, secretKey string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: store.Name + "-connection", Namespace: wsNS},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		applyGeassLabels(secret, store, "GeassObjectStore")
		secret.StringData = map[string]string{
			platform.ConnectionKeyEndpoint:  endpoint,
			platform.ConnectionKeyAccessKey: accessKey,
			platform.ConnectionKeySecretKey: secretKey,
			platform.ConnectionKeyBucket:    store.Name,
		}
		if len(store.Spec.Buckets) > 0 {
			secret.StringData[platform.ConnectionKeyBucket] = store.Spec.Buckets[0]
		}
		return setSameNamespaceOwner(store, secret, r.Scheme)
	})
	return err
}

func (r *GeassObjectStoreReconciler) setReady(ctx context.Context, store *geassv1alpha1.GeassObjectStore, wsNS, endpoint, message string) (ctrl.Result, error) {
	latest := store.DeepCopy()
	if err := r.Get(ctx, client.ObjectKeyFromObject(store), latest); err != nil {
		return ctrl.Result{}, err
	}
	latest.Status.TargetNamespace = wsNS
	latest.Status.ConnectionSecret = store.Name + "-connection"
	latest.Status.Endpoint = endpoint
	latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, metav1.ConditionTrue, "ObjectStoreReady", message)
	return ctrl.Result{}, r.Status().Update(ctx, latest)
}

func (r *GeassObjectStoreReconciler) setNotReady(ctx context.Context, store *geassv1alpha1.GeassObjectStore, message string) (ctrl.Result, error) {
	latest := store.DeepCopy()
	if err := r.Get(ctx, client.ObjectKeyFromObject(store), latest); err != nil {
		return ctrl.Result{}, err
	}
	latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, metav1.ConditionFalse, "ReconcileError", message)
	if err := r.Status().Update(ctx, latest); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: platform.RequeueAfterDefault}, nil
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
		Named("geassobjectstore").
		Complete(r)
}
