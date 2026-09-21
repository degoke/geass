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
	if err := platform.ValidateProjectPlacement(ctx, r.Client, store.Spec.Project, store.Spec.Environment); err != nil {
		return r.setNotReady(ctx, &store, err.Error())
	}

	wsNS, err := resourceNamespace(store.Spec.Project, string(store.Spec.Environment))
	if err != nil {
		return r.setNotReady(ctx, &store, err.Error())
	}

	if !controllerutil.ContainsFinalizer(&store, objectStoreFinalizer) {
		controllerutil.AddFinalizer(&store, objectStoreFinalizer)
		return ctrl.Result{}, r.Update(ctx, &store)
	}

	if !store.DeletionTimestamp.IsZero() {
		r.deleteTargetResources(ctx, objectStoreChartName(store.Name), &store, wsNS)
		controllerutil.RemoveFinalizer(&store, objectStoreFinalizer)
		return ctrl.Result{}, r.Update(ctx, &store)
	}

	if objectStorePlacement(&store) == geassv1alpha1.ObjectStorePlacementExternal || store.Spec.Engine == geassv1alpha1.ObjectStoreEngineS3 {
		return r.reconcileExternal(ctx, &store, wsNS)
	}
	if store.Spec.Engine != geassv1alpha1.ObjectStoreEngineMinIO {
		return r.setNotReady(ctx, &store, fmt.Sprintf("unsupported engine %q", store.Spec.Engine))
	}

	chartName := objectStoreChartName(store.Name)
	if prevNS, moved := previousTargetNamespace(store.Status.TargetNamespace, wsNS); moved {
		if err := r.cleanupPreviousTarget(ctx, chartName, &store, prevNS); err != nil {
			return r.setNotReady(ctx, &store, err.Error())
		}
	}

	accessKey := store.Name + "-access"
	secretKey := store.Name + "-secret"
	buckets := store.Spec.Buckets
	if len(buckets) == 0 {
		buckets = []string{store.Name}
	}

	var bucketLines strings.Builder
	for _, b := range buckets {
		fmt.Fprintf(&bucketLines, "  - name: %s\n    policy: none\n    purge: false\n", b)
	}
	values := fmt.Sprintf(`mode: standalone
commonLabels:
  %s: %s
  %s: %s
  %s: %s
rootUser: "%s"
rootPassword: "%s"
buckets:
%s`, platform.LabelManagedBy, platform.ManagedByValue, platform.LabelProject, store.Spec.Project, platform.LabelEnvironment, store.Spec.Environment, accessKey, secretKey, bucketLines.String())
	spec := helmv1.HelmChartSpec{
		Chart:           platform.MinIOReleaseChart,
		Repo:            platform.MinIOChartRepo,
		Version:         platform.MinIOChartVersion,
		TargetNamespace: wsNS,
		CreateNamespace: false,
		ValuesContent:   values,
	}
	if err := helmchart.Ensure(ctx, r.Client, chartName, spec); err != nil {
		return r.setNotReady(ctx, &store, err.Error())
	}
	chart, err := helmchart.Get(ctx, r.Client, chartName)
	if err != nil {
		return r.setNotReady(ctx, &store, err.Error())
	}
	ready, err := helmChartReady(ctx, r.Client, chart)
	if err != nil {
		return r.setNotReady(ctx, &store, err.Error())
	}
	if !ready {
		log.Info("Waiting for MinIO HelmChart", "chart", chartName)
		return r.setNotReady(ctx, &store, "MinIO HelmChart is not ready")
	}

	endpoint := fmt.Sprintf("http://%s.%s.svc:9000", chartName, wsNS)
	if err := r.reconcileConnectionSecret(ctx, &store, wsNS, endpoint, accessKey, secretKey); err != nil {
		return r.setNotReady(ctx, &store, err.Error())
	}

	latest := store.DeepCopy()
	if err := r.Get(ctx, client.ObjectKeyFromObject(&store), latest); err != nil {
		return ctrl.Result{}, err
	}
	latest.Status.TargetNamespace = wsNS
	latest.Status.ConnectionSecret = store.Name + "-connection"
	latest.Status.Endpoint = endpoint
	latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, metav1.ConditionTrue, "ObjectStoreReady", "MinIO object store is ready")
	return ctrl.Result{}, r.Status().Update(ctx, latest)
}

func objectStoreChartName(name string) string {
	return "geass-minio-" + name
}

func objectStorePlacement(store *geassv1alpha1.GeassObjectStore) geassv1alpha1.GeassObjectStorePlacement {
	if store.Spec.Placement == geassv1alpha1.ObjectStorePlacementExternal || store.Spec.Engine == geassv1alpha1.ObjectStoreEngineS3 {
		return geassv1alpha1.ObjectStorePlacementExternal
	}
	return geassv1alpha1.ObjectStorePlacementInCluster
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
		client := &cloud.AWSClient{AccessKey: accessKey, SecretKey: secretKey, Region: region}
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
	latest := store.DeepCopy()
	if err := r.Get(ctx, client.ObjectKeyFromObject(store), latest); err != nil {
		return ctrl.Result{}, err
	}
	latest.Status.TargetNamespace = wsNS
	latest.Status.ConnectionSecret = store.Name + "-connection"
	latest.Status.Endpoint = endpoint
	latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, metav1.ConditionTrue, "ObjectStoreReady", "AWS S3 bucket is ready")
	return ctrl.Result{}, r.Status().Update(ctx, latest)
}

func (r *GeassObjectStoreReconciler) cleanupPreviousTarget(ctx context.Context, chartName string, store *geassv1alpha1.GeassObjectStore, previousNS string) error {
	_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: store.Name + "-connection", Namespace: previousNS}}))
	return helmchart.Delete(ctx, r.Client, chartName)
}

func (r *GeassObjectStoreReconciler) deleteTargetResources(ctx context.Context, chartName string, store *geassv1alpha1.GeassObjectStore, wsNS string) {
	_ = helmchart.Delete(ctx, r.Client, chartName)
	_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: store.Name + "-connection", Namespace: wsNS}}))
}

func (r *GeassObjectStoreReconciler) reconcileConnectionSecret(ctx context.Context, store *geassv1alpha1.GeassObjectStore, wsNS, endpoint, accessKey, secretKey string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: store.Name + "-connection", Namespace: wsNS},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		applyGeassLabels(secret, store, "GeassObjectStore")
		secret.StringData = map[string]string{
			"endpoint":                      endpoint,
			"accessKey":                     accessKey,
			"secretKey":                     secretKey,
			platform.ConnectionKeyEndpoint:  endpoint,
			platform.ConnectionKeyAccessKey: accessKey,
			platform.ConnectionKeySecretKey: secretKey,
			"bucket":                        store.Name,
		}
		if len(store.Spec.Buckets) > 0 {
			secret.StringData["bucket"] = store.Spec.Buckets[0]
		}
		return setSameNamespaceOwner(store, secret, r.Scheme)
	})
	return err
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
