/*
Copyright 2026 DEGOKE.

Licensed under the Elastic License 2.0 (the "License"); you may not use this
file except in compliance with the License. You may obtain a copy of the
License at LICENSE or https://www.elastic.co/licensing/elastic-license.
*/

package controller

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

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

func (r *GeassObjectStoreReconciler) reconcileClusterMinIO(ctx context.Context, store *geassv1alpha1.GeassObjectStore, log interface{ Info(string, ...any) }) (ctrl.Result, error) {
	accessKey := store.Name + "-access"
	secretKey := store.Name + "-secret"
	values := fmt.Sprintf(`mode: standalone
commonLabels:
  %s: %s
rootUser: "%s"
rootPassword: "%s"
`, platform.LabelManagedBy, platform.ManagedByValue, accessKey, secretKey)
	spec := helmv1.HelmChartSpec{
		Chart:           platform.MinIOReleaseChart,
		Repo:            platform.MinIOChartRepo,
		Version:         platform.MinIOChartVersion,
		TargetNamespace: platform.SystemNamespace,
		CreateNamespace: false,
		ValuesContent:   values,
	}
	if err := helmchart.Ensure(ctx, r.Client, platform.ClusterMinIOChartName, spec); err != nil {
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

func (r *GeassObjectStoreReconciler) reconcileProjectBucket(ctx context.Context, store *geassv1alpha1.GeassObjectStore, wsNS string) (ctrl.Result, error) {
	server, err := r.clusterMinIO(ctx)
	if err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	if conditionStatus(server.Status.Conditions, platform.ConditionReady) != string(metav1.ConditionTrue) || server.Status.Endpoint == "" || server.Status.ConnectionSecret == "" {
		return r.setNotReady(ctx, store, "Set up the MinIO server in cluster settings first")
	}
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Name: server.Status.ConnectionSecret, Namespace: server.Status.TargetNamespace}, secret); err != nil {
		return r.setNotReady(ctx, store, "cluster MinIO credentials are unavailable")
	}
	accessKey := firstNonEmpty(secretValue(secret, platform.ConnectionKeyAccessKey), server.Name+"-access")
	secretKey := firstNonEmpty(secretValue(secret, platform.ConnectionKeySecretKey), server.Name+"-secret")
	if accessKey == "" || secretKey == "" {
		return r.setNotReady(ctx, store, "cluster MinIO credentials are unavailable")
	}
	buckets := store.Spec.Buckets
	if len(buckets) == 0 {
		buckets = []string{store.Name}
	}
	s3 := &cloud.AWSClient{HTTP: r.HTTP, AccessKey: accessKey, SecretKey: secretKey, Endpoint: server.Status.Endpoint}
	for _, bucket := range buckets {
		if err := s3.EnsureBucket(bucket); err != nil {
			return r.setNotReady(ctx, store, err.Error())
		}
	}
	if err := r.reconcileConnectionSecret(ctx, store, wsNS, server.Status.Endpoint, accessKey, secretKey); err != nil {
		return r.setNotReady(ctx, store, err.Error())
	}
	return r.setReady(ctx, store, wsNS, server.Status.Endpoint, "Bucket is ready on the cluster MinIO server")
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
	buckets := store.Spec.Buckets
	if len(buckets) == 0 {
		buckets = []string{store.Name}
	}
	if store.Spec.CreateBucket {
		client := &cloud.AWSClient{HTTP: r.HTTP, AccessKey: accessKey, SecretKey: secretKey, Region: region}
		for _, bucket := range buckets {
			if err := client.EnsureBucket(bucket); err != nil {
				return r.setNotReady(ctx, store, err.Error())
			}
		}
	}
	endpoint := fmt.Sprintf("https://s3.%s.amazonaws.com", region)
	if err := r.reconcileConnectionSecret(ctx, store, wsNS, endpoint, accessKey, secretKey); err != nil {
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

// SetupWithManager sets up the controller with the Manager.
func (r *GeassObjectStoreReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&geassv1alpha1.GeassObjectStore{}).
		Named("geassobjectstore").
		Complete(r)
}
