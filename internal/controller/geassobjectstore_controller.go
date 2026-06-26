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
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

func (r *GeassObjectStoreReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var store geassv1alpha1.GeassObjectStore
	if err := r.Get(ctx, req.NamespacedName, &store); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if store.Spec.Engine != geassv1alpha1.ObjectStoreEngineMinIO {
		return r.setNotReady(ctx, &store, fmt.Sprintf("unsupported engine %q", store.Spec.Engine))
	}

	wsNS, err := platform.WorkspaceNamespace(string(store.Spec.Workspace))
	if err != nil {
		return r.setNotReady(ctx, &store, err.Error())
	}

	if !controllerutil.ContainsFinalizer(&store, objectStoreFinalizer) {
		controllerutil.AddFinalizer(&store, objectStoreFinalizer)
		return ctrl.Result{}, r.Update(ctx, &store)
	}

	chartName := objectStoreChartName(store.Name)
	if !store.DeletionTimestamp.IsZero() {
		if err := r.deleteWorkspaceResources(ctx, chartName, &store, wsNS); err != nil {
			return r.setNotReady(ctx, &store, err.Error())
		}
		controllerutil.RemoveFinalizer(&store, objectStoreFinalizer)
		return ctrl.Result{}, r.Update(ctx, &store)
	}

	if prevNS, moved := previousWorkspaceNamespace(store.Status.WorkspaceNamespace, wsNS); moved {
		if err := r.cleanupPreviousWorkspace(ctx, chartName, &store, prevNS); err != nil {
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
		bucketLines.WriteString(fmt.Sprintf("  - name: %s\n    policy: none\n    purge: false\n", b))
	}
	values := fmt.Sprintf(`mode: standalone
rootUser: "%s"
rootPassword: "%s"
buckets:
%s`, accessKey, secretKey, bucketLines.String())
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
	if !helmChartReady(chart) {
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
	latest.Status.WorkspaceNamespace = wsNS
	latest.Status.ConnectionSecret = store.Name + "-connection"
	latest.Status.Endpoint = endpoint
	latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, metav1.ConditionTrue, "ObjectStoreReady", "MinIO object store is ready")
	return ctrl.Result{}, r.Status().Update(ctx, latest)
}

func objectStoreChartName(name string) string {
	return "geass-minio-" + name
}

func (r *GeassObjectStoreReconciler) cleanupPreviousWorkspace(ctx context.Context, chartName string, store *geassv1alpha1.GeassObjectStore, previousNS string) error {
	_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: store.Name + "-connection", Namespace: previousNS}}))
	return helmchart.Delete(ctx, r.Client, chartName)
}

func (r *GeassObjectStoreReconciler) deleteWorkspaceResources(ctx context.Context, chartName string, store *geassv1alpha1.GeassObjectStore, wsNS string) error {
	_ = helmchart.Delete(ctx, r.Client, chartName)
	_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: store.Name + "-connection", Namespace: wsNS}}))
	return nil
}

func (r *GeassObjectStoreReconciler) reconcileConnectionSecret(ctx context.Context, store *geassv1alpha1.GeassObjectStore, wsNS, endpoint, accessKey, secretKey string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: store.Name + "-connection", Namespace: wsNS},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		applyGeassLabels(secret, store, "GeassObjectStore")
		secret.StringData = map[string]string{
			"endpoint":  endpoint,
			"accessKey": accessKey,
			"secretKey": secretKey,
			"bucket":    store.Name,
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
