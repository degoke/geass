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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	if err := platform.ValidateProjectPlacement(ctx, r.Client, cache.Spec.Project, cache.Spec.Environment); err != nil {
		return r.setNotReady(ctx, &cache, err.Error())
	}

	if cache.Spec.Engine != geassv1alpha1.CacheEngineRedis {
		return r.setNotReady(ctx, &cache, fmt.Sprintf("unsupported engine %q", cache.Spec.Engine))
	}

	wsNS, err := resourceNamespace(cache.Spec.Project, string(cache.Spec.Environment))
	if err != nil {
		return r.setNotReady(ctx, &cache, err.Error())
	}

	if !controllerutil.ContainsFinalizer(&cache, cacheFinalizer) {
		controllerutil.AddFinalizer(&cache, cacheFinalizer)
		return ctrl.Result{}, r.Update(ctx, &cache)
	}

	chartName := cacheChartName(cache.Name)
	if !cache.DeletionTimestamp.IsZero() {
		r.deleteTargetResources(ctx, chartName, &cache, wsNS)
		controllerutil.RemoveFinalizer(&cache, cacheFinalizer)
		return ctrl.Result{}, r.Update(ctx, &cache)
	}
	password, err := r.ensureRedisPassword(ctx, &cache, wsNS)
	if err != nil {
		return r.setNotReady(ctx, &cache, err.Error())
	}

	if prevNS, moved := previousTargetNamespace(cache.Status.TargetNamespace, wsNS); moved {
		if err := r.cleanupPreviousTarget(ctx, chartName, &cache, prevNS); err != nil {
			return r.setNotReady(ctx, &cache, err.Error())
		}
	}

	values := fmt.Sprintf(`architecture: standalone
commonLabels:
  %s: %s
  %s: %s
  %s: %s
auth:
  enabled: true
  password: "%s"
master:
  persistence:
    enabled: false
`, platform.LabelManagedBy, platform.ManagedByValue, platform.LabelProject, cache.Spec.Project, platform.LabelEnvironment, cache.Spec.Environment, password)
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
	ready, err := helmChartReady(ctx, r.Client, chart)
	if err != nil {
		return r.setNotReady(ctx, &cache, err.Error())
	}
	if !ready {
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
	latest.Status.TargetNamespace = wsNS
	latest.Status.ConnectionSecret = cache.Name + "-connection"
	latest.Status.Host = host
	latest.Status.Port = 6379
	latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, metav1.ConditionTrue, "CacheReady", "Redis cache is ready")
	return ctrl.Result{}, r.Status().Update(ctx, latest)
}

func cacheChartName(name string) string {
	return "geass-redis-" + name
}

func (r *GeassCacheReconciler) cleanupPreviousTarget(ctx context.Context, chartName string, cache *geassv1alpha1.GeassCache, previousNS string) error {
	_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: cache.Name + "-connection", Namespace: previousNS}}))
	// Uninstall the release from the old namespace before redeploying elsewhere.
	return helmchart.Delete(ctx, r.Client, chartName)
}

func (r *GeassCacheReconciler) deleteTargetResources(ctx context.Context, chartName string, cache *geassv1alpha1.GeassCache, wsNS string) {
	_ = helmchart.Delete(ctx, r.Client, chartName)
	_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: cache.Name + "-connection", Namespace: wsNS}}))
}

func (r *GeassCacheReconciler) reconcileConnectionSecret(ctx context.Context, cache *geassv1alpha1.GeassCache, wsNS, host string) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: cache.Name + "-connection", Namespace: wsNS},
	}
	password, err := r.ensureRedisPassword(ctx, cache, wsNS)
	if err != nil {
		return err
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		applyGeassLabels(secret, cache, "GeassCache")
		secret.Data = map[string][]byte{
			platform.ConnectionKeyHost:     []byte(host),
			platform.ConnectionKeyPort:     []byte(platform.RedisDefaultPort),
			platform.ConnectionKeyPassword: []byte(password),
			platform.ConnectionKeyURI:      fmt.Appendf(nil, "redis://:%s@%s:%s", password, host, platform.RedisDefaultPort),
		}
		return setSameNamespaceOwner(cache, secret, r.Scheme)
	})
	return err
}

func (r *GeassCacheReconciler) ensureRedisPassword(ctx context.Context, cache *geassv1alpha1.GeassCache, namespace string) (string, error) {
	secret := &corev1.Secret{}
	err := r.Get(ctx, client.ObjectKey{Name: cache.Name + "-connection", Namespace: namespace}, secret)
	if err != nil && !apierrors.IsNotFound(err) {
		return "", err
	}
	if err == nil {
		if password := string(secret.Data["password"]); password != "" {
			return password, nil
		}
		if password := secret.StringData["password"]; password != "" {
			return password, nil
		}
	}
	bytes := make([]byte, 24)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	password := hex.EncodeToString(bytes)
	if apierrors.IsNotFound(err) {
		secret = &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: cache.Name + "-connection", Namespace: namespace}}
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		applyGeassLabels(secret, cache, "GeassCache")
		if secret.Data == nil {
			secret.Data = map[string][]byte{}
		}
		secret.Data["password"] = []byte(password)
		return setSameNamespaceOwner(cache, secret, r.Scheme)
	})
	return password, err
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
