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

const cacheFinalizer = platform.FinalizerCache

// GeassCacheReconciler reconciles a GeassCache object.
type GeassCacheReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=geass.geass.dev,resources=geasscaches,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geasscaches/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geasscaches/finalizers,verbs=update
// +kubebuilder:rbac:groups=helm.cattle.io,resources=helmcharts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

func (r *GeassCacheReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var cache geassv1alpha1.GeassCache
	if err := r.Get(ctx, req.NamespacedName, &cache); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if cache.Spec.Engine != geassv1alpha1.CacheEngineRedis {
		return r.setNotReady(ctx, &cache, fmt.Sprintf("unsupported engine %q", cache.Spec.Engine))
	}

	wsNS, err := platform.WorkspaceNamespace(string(cache.Spec.Workspace))
	if err != nil {
		return r.setNotReady(ctx, &cache, err.Error())
	}

	if !controllerutil.ContainsFinalizer(&cache, cacheFinalizer) {
		controllerutil.AddFinalizer(&cache, cacheFinalizer)
		return ctrl.Result{}, r.Update(ctx, &cache)
	}

	chartName := cacheChartName(cache.Name)
	if !cache.DeletionTimestamp.IsZero() {
		if err := r.deleteWorkspaceResources(ctx, chartName, &cache, wsNS); err != nil {
			return r.setNotReady(ctx, &cache, err.Error())
		}
		controllerutil.RemoveFinalizer(&cache, cacheFinalizer)
		return ctrl.Result{}, r.Update(ctx, &cache)
	}

	if prevNS, moved := previousWorkspaceNamespace(cache.Status.WorkspaceNamespace, wsNS); moved {
		if err := r.cleanupPreviousWorkspace(ctx, chartName, &cache, prevNS); err != nil {
			return r.setNotReady(ctx, &cache, err.Error())
		}
	}

	values := fmt.Sprintf(`architecture: standalone
auth:
  enabled: true
  password: "%s"
master:
  persistence:
    enabled: false
`, cache.Name+"-redis")
	spec := helmv1.HelmChartSpec{
		Chart:           platform.RedisReleaseChart,
		Repo:            platform.RedisChartRepo,
		Version:         platform.RedisChartVersion,
		TargetNamespace: wsNS,
		CreateNamespace: false,
		ValuesContent:   values,
	}
	if err := helmchart.Ensure(ctx, r.Client, chartName, spec); err != nil {
		return r.setNotReady(ctx, &cache, err.Error())
	}
	chart, err := helmchart.Get(ctx, r.Client, chartName)
	if err != nil {
		return r.setNotReady(ctx, &cache, err.Error())
	}
	if !helmChartReady(chart) {
		log.Info("Waiting for Redis HelmChart", "chart", chartName)
		return r.setNotReady(ctx, &cache, "Redis HelmChart is not ready")
	}

	host := fmt.Sprintf("%s-master.%s.svc", chartName, wsNS)
	if err := r.reconcileConnectionSecret(ctx, &cache, wsNS, host); err != nil {
		return r.setNotReady(ctx, &cache, err.Error())
	}

	latest := cache.DeepCopy()
	if err := r.Get(ctx, client.ObjectKeyFromObject(&cache), latest); err != nil {
		return ctrl.Result{}, err
	}
	latest.Status.WorkspaceNamespace = wsNS
	latest.Status.ConnectionSecret = cache.Name + "-connection"
	latest.Status.Host = host
	latest.Status.Port = 6379
	latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, metav1.ConditionTrue, "CacheReady", "Redis cache is ready")
	return ctrl.Result{}, r.Status().Update(ctx, latest)
}

func cacheChartName(name string) string {
	return "geass-redis-" + name
}

func (r *GeassCacheReconciler) cleanupPreviousWorkspace(ctx context.Context, chartName string, cache *geassv1alpha1.GeassCache, previousNS string) error {
	_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: cache.Name + "-connection", Namespace: previousNS}}))
	// Uninstall the release from the old namespace before redeploying elsewhere.
	return helmchart.Delete(ctx, r.Client, chartName)
}

func (r *GeassCacheReconciler) deleteWorkspaceResources(ctx context.Context, chartName string, cache *geassv1alpha1.GeassCache, wsNS string) error {
	_ = helmchart.Delete(ctx, r.Client, chartName)
	_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: cache.Name + "-connection", Namespace: wsNS}}))
	return nil
}

func (r *GeassCacheReconciler) reconcileConnectionSecret(ctx context.Context, cache *geassv1alpha1.GeassCache, wsNS, host string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: cache.Name + "-connection", Namespace: wsNS},
	}
	password := cache.Name + "-redis"
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		applyGeassLabels(secret, cache, "GeassCache")
		secret.StringData = map[string]string{
			"host":     host,
			"port":     "6379",
			"password": password,
			"uri":      fmt.Sprintf("redis://:%s@%s:6379", password, host),
		}
		return setSameNamespaceOwner(cache, secret, r.Scheme)
	})
	return err
}

func (r *GeassCacheReconciler) setNotReady(ctx context.Context, cache *geassv1alpha1.GeassCache, message string) (ctrl.Result, error) {
	latest := cache.DeepCopy()
	if err := r.Get(ctx, client.ObjectKeyFromObject(cache), latest); err != nil {
		return ctrl.Result{}, err
	}
	latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, metav1.ConditionFalse, "ReconcileError", message)
	if err := r.Status().Update(ctx, latest); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: platform.RequeueAfterDefault}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GeassCacheReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&geassv1alpha1.GeassCache{}).
		Named("geasscache").
		Complete(r)
}
