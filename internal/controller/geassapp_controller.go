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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

const appFinalizer = platform.FinalizerApp

// GeassAppReconciler reconciles a GeassApp object.
type GeassAppReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassapps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassapps/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassapps/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=servicemonitors,verbs=get;list;watch;create;update;patch;delete

func (r *GeassAppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var app geassv1alpha1.GeassApp
	if err := r.Get(ctx, req.NamespacedName, &app); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	wsNS, err := platform.WorkspaceNamespace(string(app.Spec.Workspace))
	if err != nil {
		return r.setNotReady(ctx, &app, err.Error())
	}

	if !controllerutil.ContainsFinalizer(&app, appFinalizer) {
		controllerutil.AddFinalizer(&app, appFinalizer)
		return ctrl.Result{}, r.Update(ctx, &app)
	}

	if !app.DeletionTimestamp.IsZero() {
		if err := r.deleteWorkspaceResources(ctx, &app, wsNS); err != nil {
			return r.setNotReady(ctx, &app, err.Error())
		}
		controllerutil.RemoveFinalizer(&app, appFinalizer)
		return ctrl.Result{}, r.Update(ctx, &app)
	}

	if prevNS, moved := previousWorkspaceNamespace(app.Status.WorkspaceNamespace, wsNS); moved {
		if err := r.deleteWorkspaceResources(ctx, &app, prevNS); err != nil {
			return r.setNotReady(ctx, &app, err.Error())
		}
	}

	if err := r.reconcileConfigMap(ctx, &app, wsNS); err != nil {
		return r.setNotReady(ctx, &app, err.Error())
	}
	if err := r.reconcileSecret(ctx, &app, wsNS); err != nil {
		return r.setNotReady(ctx, &app, err.Error())
	}
	if err := r.reconcileDeployment(ctx, &app, wsNS); err != nil {
		return r.setNotReady(ctx, &app, err.Error())
	}
	if err := r.reconcileService(ctx, &app, wsNS); err != nil {
		return r.setNotReady(ctx, &app, err.Error())
	}
	if err := r.reconcileIngress(ctx, &app, wsNS); err != nil {
		return r.setNotReady(ctx, &app, err.Error())
	}
	if err := r.reconcileServiceMonitor(ctx, &app, wsNS); err != nil {
		return r.setNotReady(ctx, &app, err.Error())
	}

	replicas := int32(1)
	if app.Spec.Replicas != nil {
		replicas = *app.Spec.Replicas
	}
	deploy := &appsv1.Deployment{}
	if err := r.Get(ctx, client.ObjectKey{Name: app.Name, Namespace: wsNS}, deploy); err != nil {
		return r.setNotReady(ctx, &app, "Deployment was not created")
	}
	if !deploymentReady(deploy, replicas) {
		return r.setNotReady(ctx, &app, "Deployment is not available yet")
	}

	url := ""
	if app.Spec.Ingress.Host != "" {
		scheme := "http"
		if app.Spec.Ingress.TLSEnabled {
			scheme = "https"
		}
		path := app.Spec.Ingress.Path
		if path == "" {
			path = "/"
		}
		url = fmt.Sprintf("%s://%s%s", scheme, app.Spec.Ingress.Host, path)
	}

	latest := app.DeepCopy()
	if err := r.Get(ctx, client.ObjectKeyFromObject(&app), latest); err != nil {
		return ctrl.Result{}, err
	}
	latest.Status.WorkspaceNamespace = wsNS
	latest.Status.URL = url
	latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, metav1.ConditionTrue, "AppReady", "Application is ready")
	if err := r.Status().Update(ctx, latest); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Reconciled GeassApp", "workspace", app.Spec.Workspace, "namespace", wsNS)
	return ctrl.Result{}, nil
}

func (r *GeassAppReconciler) appLabels(app *geassv1alpha1.GeassApp) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       app.Name,
		"app.kubernetes.io/managed-by": "geass",
		"geass.dev/workspace":          string(app.Spec.Workspace),
	}
}

func (r *GeassAppReconciler) reconcileConfigMap(ctx context.Context, app *geassv1alpha1.GeassApp, wsNS string) error {
	if len(app.Spec.ConfigData) == 0 {
		return nil
	}
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: app.Name + "-config", Namespace: wsNS},
	}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		applyGeassLabels(cm, app, "GeassApp")
		cm.Data = app.Spec.ConfigData
		return setSameNamespaceOwner(app, cm, r.Scheme)
	})
	_ = op
	return err
}

func (r *GeassAppReconciler) reconcileSecret(ctx context.Context, app *geassv1alpha1.GeassApp, wsNS string) error {
	if len(app.Spec.SecretData) == 0 {
		return nil
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: app.Name + "-secret", Namespace: wsNS},
	}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		applyGeassLabels(secret, app, "GeassApp")
		secret.StringData = app.Spec.SecretData
		return setSameNamespaceOwner(app, secret, r.Scheme)
	})
	_ = op
	return err
}

func (r *GeassAppReconciler) reconcileDeployment(ctx context.Context, app *geassv1alpha1.GeassApp, wsNS string) error {
	replicas := int32(1)
	if app.Spec.Replicas != nil {
		replicas = *app.Spec.Replicas
	}
	port := app.Spec.Port
	if port == 0 {
		port = 8080
	}

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: app.Name, Namespace: wsNS},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, deploy, func() error {
		applyGeassLabels(deploy, app, "GeassApp")
		selector := geassResourceLabels(app, "GeassApp")
		deploy.Spec.Replicas = &replicas
		deploy.Spec.Selector = &metav1.LabelSelector{MatchLabels: selector}
		podLabels := r.appLabels(app)
		for k, v := range selector {
			podLabels[k] = v
		}
		volumes := []corev1.Volume{}
		volumeMounts := []corev1.VolumeMount{}
		if len(app.Spec.ConfigData) > 0 {
			volumes = append(volumes, corev1.Volume{
				Name: "config",
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{Name: app.Name + "-config"},
					},
				},
			})
			volumeMounts = append(volumeMounts, corev1.VolumeMount{Name: "config", MountPath: "/config"})
		}
		if len(app.Spec.SecretData) > 0 {
			volumes = append(volumes, corev1.Volume{
				Name: "secret",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: app.Name + "-secret",
					},
				},
			})
			volumeMounts = append(volumeMounts, corev1.VolumeMount{Name: "secret", MountPath: "/secrets", ReadOnly: true})
		}
		for _, ref := range app.Spec.ConfigMapRefs {
			volName := "cm-" + ref.Name
			volumes = append(volumes, corev1.Volume{
				Name: volName,
				VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{Name: ref.Name},
					},
				},
			})
			volumeMounts = append(volumeMounts, corev1.VolumeMount{
				Name:      volName,
				MountPath: "/config/refs/" + ref.Name,
				ReadOnly:  true,
			})
		}
		for _, ref := range app.Spec.SecretRefs {
			volName := "sec-" + ref.Name
			volumes = append(volumes, corev1.Volume{
				Name: volName,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{SecretName: ref.Name},
				},
			})
			volumeMounts = append(volumeMounts, corev1.VolumeMount{
				Name:      volName,
				MountPath: "/secrets/refs/" + ref.Name,
				ReadOnly:  true,
			})
		}
		deploy.Spec.Template = corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: podLabels},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name:         "app",
					Image:        app.Spec.Image,
					Ports:        []corev1.ContainerPort{{Name: "http", ContainerPort: port}},
					Env:          app.Spec.Env,
					EnvFrom:      app.Spec.EnvFrom,
					VolumeMounts: volumeMounts,
				}},
				Volumes: volumes,
			},
		}
		return setSameNamespaceOwner(app, deploy, r.Scheme)
	})
	return err
}

func (r *GeassAppReconciler) reconcileService(ctx context.Context, app *geassv1alpha1.GeassApp, wsNS string) error {
	port := app.Spec.Port
	if port == 0 {
		port = 8080
	}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: app.Name, Namespace: wsNS},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		applyGeassLabels(svc, app, "GeassApp")
		svc.Spec.Selector = r.appLabels(app)
		svc.Spec.Ports = []corev1.ServicePort{{
			Name:       "http",
			Port:       port,
			TargetPort: intstr.FromString("http"),
		}}
		return setSameNamespaceOwner(app, svc, r.Scheme)
	})
	return err
}

func (r *GeassAppReconciler) reconcileIngress(ctx context.Context, app *geassv1alpha1.GeassApp, wsNS string) error {
	if app.Spec.Ingress.Host == "" {
		ing := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: app.Name, Namespace: wsNS}}
		if err := r.Get(ctx, client.ObjectKeyFromObject(ing), ing); apierrors.IsNotFound(err) {
			return nil
		} else if err != nil {
			return err
		}
		return client.IgnoreNotFound(r.Delete(ctx, ing))
	}

	path := app.Spec.Ingress.Path
	if path == "" {
		path = "/"
	}
	pathType := networkingv1.PathTypePrefix
	port := app.Spec.Port
	if port == 0 {
		port = 8080
	}

	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: app.Name, Namespace: wsNS},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, ing, func() error {
		applyGeassLabels(ing, app, "GeassApp")
		if app.Spec.Ingress.TLSEnabled {
			ing.Annotations = map[string]string{
				"cert-manager.io/cluster-issuer": "letsencrypt-prod",
			}
		} else {
			ing.Annotations = nil
		}
		rules := []networkingv1.IngressRule{{
			Host: app.Spec.Ingress.Host,
			IngressRuleValue: networkingv1.IngressRuleValue{
				HTTP: &networkingv1.HTTPIngressRuleValue{
					Paths: []networkingv1.HTTPIngressPath{{
						Path:     path,
						PathType: &pathType,
						Backend: networkingv1.IngressBackend{
							Service: &networkingv1.IngressServiceBackend{
								Name: app.Name,
								Port: networkingv1.ServiceBackendPort{Number: port},
							},
						},
					}},
				},
			},
		}}
		ing.Spec.Rules = rules
		if app.Spec.Ingress.TLSEnabled {
			ing.Spec.TLS = []networkingv1.IngressTLS{{
				Hosts:      []string{app.Spec.Ingress.Host},
				SecretName: app.Name + "-tls",
			}}
		} else {
			ing.Spec.TLS = nil
		}
		return setSameNamespaceOwner(app, ing, r.Scheme)
	})
	return err
}

func (r *GeassAppReconciler) reconcileServiceMonitor(ctx context.Context, app *geassv1alpha1.GeassApp, wsNS string) error {
	name := app.Name + "-metrics"
	if !app.Spec.Metrics.Enabled {
		sm := &monitoringv1.ServiceMonitor{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: wsNS}}
		if err := r.Get(ctx, client.ObjectKeyFromObject(sm), sm); apierrors.IsNotFound(err) {
			return nil
		} else if err != nil {
			return err
		}
		return client.IgnoreNotFound(r.Delete(ctx, sm))
	}

	metricsPath := app.Spec.Metrics.Path
	if metricsPath == "" {
		metricsPath = "/metrics"
	}
	metricsPort := app.Spec.Metrics.Port
	if metricsPort == "" {
		metricsPort = "http"
	}

	sm := &monitoringv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: wsNS},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, sm, func() error {
		applyGeassLabels(sm, app, "GeassApp")
		sm.Spec.Selector = metav1.LabelSelector{MatchLabels: r.appLabels(app)}
		sm.Spec.Endpoints = []monitoringv1.Endpoint{{
			Port: metricsPort,
			Path: metricsPath,
		}}
		return setSameNamespaceOwner(app, sm, r.Scheme)
	})
	return err
}

func (r *GeassAppReconciler) deleteWorkspaceResources(ctx context.Context, app *geassv1alpha1.GeassApp, wsNS string) error {
	names := []string{app.Name, app.Name + "-config", app.Name + "-secret", app.Name + "-metrics"}
	for _, name := range names {
		_ = r.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: wsNS}})
		_ = r.Delete(ctx, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: wsNS}})
		_ = r.Delete(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: wsNS}})
		_ = r.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: wsNS}})
		_ = r.Delete(ctx, &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: wsNS}})
		_ = r.Delete(ctx, &monitoringv1.ServiceMonitor{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: wsNS}})
	}
	_ = r.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: app.Name, Namespace: wsNS}})
	_ = r.Delete(ctx, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: app.Name, Namespace: wsNS}})
	_ = r.Delete(ctx, &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: app.Name, Namespace: wsNS}})
	return nil
}

func (r *GeassAppReconciler) setNotReady(ctx context.Context, app *geassv1alpha1.GeassApp, message string) (ctrl.Result, error) {
	latest := app.DeepCopy()
	if err := r.Get(ctx, client.ObjectKeyFromObject(app), latest); err != nil {
		return ctrl.Result{}, err
	}
	latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, metav1.ConditionFalse, "ReconcileError", message)
	if err := r.Status().Update(ctx, latest); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: platform.RequeueAfterDefault}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GeassAppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&geassv1alpha1.GeassApp{}).
		Named("geassapp").
		Complete(r)
}
