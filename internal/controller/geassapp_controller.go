/*
Copyright 2026 DEGOKE.

Licensed under the Elastic License 2.0 (the "License"); you may not use this
file except in compliance with the License. You may obtain a copy of the
License at LICENSE or https://www.elastic.co/licensing/elastic-license.
*/

package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
	"github.com/degoke/geass/pkg/webhooks"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

const appFinalizer = platform.FinalizerApp

const (
	appContainerName = "app"
	portNameHTTP     = "http"
	schemeHTTP       = "http"
	schemeHTTPS      = "https"
)

// GeassAppReconciler reconciles a GeassApp object.
type GeassAppReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// Webhooks receives deployment outcomes; nil disables webhook notifications.
	Webhooks webhooks.Emitter
}

// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassapps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassapps/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassapps/finalizers,verbs=update
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassdeployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassdeployments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods/log,verbs=get
// +kubebuilder:rbac:groups="",resources=pods/exec,verbs=create
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=servicemonitors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch

func (r *GeassAppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var app geassv1alpha1.GeassApp
	if err := r.Get(ctx, req.NamespacedName, &app); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if result, paused := beginProvisionReconcileUnlessDeleting(!app.DeletionTimestamp.IsZero(), app.Status.Conditions, app.Generation, &app.Status.ReconcileRetry); paused {
		return result, nil
	}

	if !controllerutil.ContainsFinalizer(&app, appFinalizer) {
		controllerutil.AddFinalizer(&app, appFinalizer)
		return ctrl.Result{}, r.Update(ctx, &app)
	}

	if !app.DeletionTimestamp.IsZero() {
		for _, wsNS := range appDeletionNamespaces(&app) {
			r.deleteTargetResources(ctx, &app, wsNS)
		}
		// Deployment history records are not owned by the app, so remove them here; otherwise a
		// new service that reuses the name would show the old service's history.
		if err := r.DeleteAllOf(ctx, &geassv1alpha1.GeassDeployment{}, client.InNamespace(app.Namespace), client.MatchingLabels{platform.LabelApp: app.Name}); err != nil {
			log.Error(err, "Could not delete deployment history", "app", app.Name)
		}
		// The service's own secret values (<app>-secrets) live in the system namespace with no owner
		// reference, so they have to be removed here or they outlive the service.
		_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: app.Name + "-secrets", Namespace: app.Namespace}}))
		controllerutil.RemoveFinalizer(&app, appFinalizer)
		return ctrl.Result{}, r.Update(ctx, &app)
	}

	if !applyAppDeployedSpec(&app) {
		return r.setNotReady(ctx, &app, "Service has pending changes and no last deployed snapshot")
	}

	if err := platform.ValidateProjectPlacement(ctx, r.Client, app.Spec.Project, app.Spec.Environment); err != nil {
		return r.setNotReady(ctx, &app, err.Error())
	}
	if err := validateAppSource(&app); err != nil {
		return r.setNotReady(ctx, &app, err.Error())
	}
	if err := r.ensureCloudflarePublicHost(ctx, &app); err != nil {
		return r.setNotReady(ctx, &app, err.Error())
	}

	wsNS, err := resourceNamespace(app.Spec.Project, string(app.Spec.Environment))
	if err != nil {
		return r.setNotReady(ctx, &app, err.Error())
	}

	if app.Spec.Source.Git != nil {
		if !app.Spec.Deploy.Enabled {
			if prevNS, moved := previousTargetNamespace(app.Status.TargetNamespace, wsNS); moved {
				r.deleteTargetResources(ctx, &app, prevNS)
			}
			r.deleteTargetResources(ctx, &app, wsNS)
			return r.setNotReady(ctx, &app, "Service is configured and waiting for deployment")
		}
		build, ready, err := r.ensureGitBuild(ctx, &app)
		if err != nil {
			return r.setNotReady(ctx, &app, err.Error())
		}
		if ready {
			app.Status.ResolvedImage = build.Status.ImageDigest
			app.Status.ActiveBuild = build.Name
		}
		if !ready {
			return r.setNotReady(ctx, &app, "Git source is waiting for a successful build")
		}
	}

	if !app.Spec.Deploy.Enabled {
		if prevNS, moved := previousTargetNamespace(app.Status.TargetNamespace, wsNS); moved {
			r.deleteTargetResources(ctx, &app, prevNS)
		}
		r.deleteTargetResources(ctx, &app, wsNS)
		return r.setNotReady(ctx, &app, "Service is configured and waiting for deployment")
	}

	if prevNS, moved := previousTargetNamespace(app.Status.TargetNamespace, wsNS); moved {
		r.deleteTargetResources(ctx, &app, prevNS)
	}
	if app.Status.RolloutPaused {
		return ctrl.Result{RequeueAfter: platform.RequeueAfterDefault}, nil
	}

	if err := r.reconcileConfigMap(ctx, &app, wsNS); err != nil {
		return r.setNotReady(ctx, &app, err.Error())
	}
	if err := r.reconcileFunctionCode(ctx, &app, wsNS); err != nil {
		return r.setNotReady(ctx, &app, err.Error())
	}
	if err := r.reconcileSecret(ctx, &app, wsNS); err != nil {
		return r.setNotReady(ctx, &app, err.Error())
	}
	plan, err := r.planScaleToZero(ctx, &app)
	if err != nil {
		return r.setNotReady(ctx, &app, err.Error())
	}
	est := platform.EstimateFromResources("service", appContainerResources(&app), deploymentReplicas(&app))
	placement, err := platform.ResolvePlacement(ctx, r.Client, app.Spec.Project, app.Spec.RunOn.Tags)
	if err != nil {
		return r.setNotReady(ctx, &app, err.Error())
	}
	if ok, msg := workloadFitsPlacementUnlessCreated(ctx, r.Client, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: app.Name, Namespace: wsNS}}, est, placement); !ok {
		return r.setNotReady(ctx, &app, msg)
	}
	if err := r.reconcileDeployment(ctx, &app, wsNS, withScaleToZero(plan)); err != nil {
		return r.setNotReady(ctx, &app, err.Error())
	}
	if err := r.reconcileAutoscaler(ctx, &app, wsNS); err != nil {
		return r.setNotReady(ctx, &app, err.Error())
	}
	if err := r.reconcileService(ctx, &app, wsNS); err != nil {
		return r.setNotReady(ctx, &app, err.Error())
	}
	if err := r.reconcileScaleToZero(ctx, &app, wsNS, &plan); err != nil {
		return r.setNotReady(ctx, &app, err.Error())
	}
	if err := r.reconcileIngress(ctx, &app, wsNS, withScaleToZero(plan)); err != nil {
		return r.setNotReady(ctx, &app, err.Error())
	}
	if err := r.reconcileFunctionTriggers(ctx, &app, wsNS, plan); err != nil {
		return r.setNotReady(ctx, &app, err.Error())
	}
	if err := r.reconcileServiceMonitor(ctx, &app, wsNS); err != nil {
		return r.setNotReady(ctx, &app, err.Error())
	}

	deploy := &appsv1.Deployment{}
	if err := r.Get(ctx, client.ObjectKey{Name: app.Name, Namespace: wsNS}, deploy); err != nil {
		return r.setNotReady(ctx, &app, "Deployment was not created")
	}
	// A service that sleeps when idle is at zero copies when nobody is using it, which is ready and not starting.
	replicas := effectiveReplicas(&app, deploy, plan)
	if err := r.reconcileOperationalDeployment(ctx, &app, deploy, replicas); err != nil {
		return r.setNotReady(ctx, &app, err.Error())
	}
	if settled, result, err := r.startGate(ctx, &app, wsNS, deploy, plan, replicas); settled || err != nil {
		return result, err
	}
	if !deploymentReady(deploy, replicas) {
		if app.Spec.Deploy.FailureThreshold > 0 {
			latest := &geassv1alpha1.GeassApp{}
			if err := r.Get(ctx, client.ObjectKeyFromObject(&app), latest); err == nil {
				failed := false
				if _, err := platform.PatchStatus(ctx, r.Client, latest, func() {
					latest.Status.HealthCheckFailures++
					if latest.Status.HealthCheckFailures >= app.Spec.Deploy.FailureThreshold {
						failed = true
						latest.Status.RolloutPaused = app.Spec.Deploy.PauseOnFailure
						latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, metav1.ConditionFalse, "RolloutFailed", "Deployment failed the configured health-check threshold")
						if app.Spec.Deploy.RollbackOnFailure {
							latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, "RollbackReady", metav1.ConditionTrue, "RollbackRequested", "Rollback was requested after rollout failure")
						}
					}
				}); err != nil {
					return ctrl.Result{}, err
				}
				if failed {
					return ctrl.Result{RequeueAfter: platform.RequeueAfterDefault}, nil
				}
			}
		}
		return r.setNotReady(ctx, &app, messageServiceStarting)
	}
	if app.Status.HealthCheckFailures != 0 {
		latest := &geassv1alpha1.GeassApp{}
		if err := r.Get(ctx, client.ObjectKeyFromObject(&app), latest); err == nil {
			_, _ = platform.PatchStatus(ctx, r.Client, latest, func() {
				latest.Status.HealthCheckFailures = 0
				latest.Status.RolloutPaused = false
			})
		}
	}

	url := ""
	if app.Spec.Ingress.Host != "" {
		scheme := schemeHTTP
		if app.Spec.Ingress.TLSEnabled {
			scheme = schemeHTTPS
		}
		path := app.Spec.Ingress.Path
		if path == "" {
			path = "/"
		}
		url = fmt.Sprintf("%s://%s%s", scheme, app.Spec.Ingress.Host, path)
	}

	latest := &geassv1alpha1.GeassApp{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(&app), latest); err != nil {
		return ctrl.Result{}, err
	}
	// Work out what the status should say first, so the DNS lookup and the ingress read happen once and
	// the write is skipped when it would say what it already says.
	traefikTarget := latest.Status.TraefikTarget
	if ingress := new(networkingv1.Ingress); r.Get(ctx, client.ObjectKey{Name: app.Name, Namespace: wsNS}, ingress) == nil && len(ingress.Status.LoadBalancer.Ingress) > 0 {
		traefikTarget = ingress.Status.LoadBalancer.Ingress[0].Hostname
		if traefikTarget == "" {
			traefikTarget = ingress.Status.LoadBalancer.Ingress[0].IP
		}
	}
	dnsVerified := true
	if app.Spec.Ingress.DNSVerification {
		dnsVerified = app.Spec.Ingress.Host != ""
		if dnsVerified {
			lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			_, lookupErr := net.DefaultResolver.LookupHost(lookupCtx, app.Spec.Ingress.Host)
			cancel()
			dnsVerified = lookupErr == nil
		}
	}
	if _, err := platform.PatchStatus(ctx, r.Client, latest, func() {
		latest.Status.TargetNamespace = wsNS
		latest.Status.URL = url
		latest.Status.DNSName = app.Spec.Ingress.Host
		latest.Status.TraefikTarget = traefikTarget
		latest.Status.DNSVerified = dnsVerified
		if app.Spec.Ingress.DNSVerification {
			if dnsVerified {
				latest.Status.DNSMessage = "DNS hostname is configured for verification"
			} else {
				latest.Status.DNSMessage = "DNS hostname is required"
			}
		}
		retryState := reconcileRetryFromAPI(&latest.Status.ReconcileRetry)
		platform.ClearReconcileRetry(&retryState)
		writeReconcileRetryToAPI(&latest.Status.ReconcileRetry, retryState)
		completeProvisionSuccess(&latest.Status.Provision)
		latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, metav1.ConditionTrue, "AppReady", "Application is ready")
		latest.Status.Conditions = scaleToZeroCondition(plan, latest.Status.Conditions)
	}); err != nil {
		return ctrl.Result{}, err
	}
	app.Status = latest.Status
	r.notifyDeployOutcome(ctx, &app, webhooks.EventDeploymentSucceeded, "")

	log.Info("Reconciled GeassApp", "environment", app.Spec.Environment, "namespace", wsNS)
	return ctrl.Result{}, nil
}

func appPendingUpdates(app *geassv1alpha1.GeassApp) int {
	count, err := strconv.Atoi(app.Annotations[platform.AppPendingUpdatesAnnotation])
	if err != nil || count < 0 {
		return 0
	}
	return count
}

func (r *GeassAppReconciler) appLabels(app *geassv1alpha1.GeassApp) map[string]string {
	return map[string]string{
		platform.K8sLabelAppName:   app.Name,
		platform.K8sLabelManagedBy: platform.ManagedByValue,
		platform.LabelProject:      app.Spec.Project,
		platform.LabelEnvironment:  string(app.Spec.Environment),
	}
}

func (r *GeassAppReconciler) reconcileOperationalDeployment(ctx context.Context, app *geassv1alpha1.GeassApp, deploy *appsv1.Deployment, replicas int32) error {
	name := app.Name + "-active"
	operation := &geassv1alpha1.GeassDeployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: app.Namespace}}
	phase := "RollingOut"
	if deploymentReady(deploy, replicas) {
		phase = "Available"
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, operation, func() error {
		applyGeassLabels(operation, app, "GeassDeployment")
		operation.Spec = geassv1alpha1.GeassDeploymentSpec{App: app.Name, Project: app.Spec.Project, Environment: app.Spec.Environment, Image: appImage(app), ImageDigest: app.Status.ResolvedImage, Replicas: app.Spec.Replicas, BuildRef: app.Status.ActiveBuild, Commit: appSourceCommit(app), Trigger: "Reconciliation", Source: "GeassApp controller"}
		return setSameNamespaceOwner(app, operation, r.Scheme)
	}); err != nil {
		return err
	}
	// The status has its own endpoint, so setting it inside the function above never saved it. It is
	// written here, and only when it changed.
	_, err := platform.PatchStatus(ctx, r.Client, operation, func() {
		operation.Status.Phase = phase
		operation.Status.AvailableReplicas = deploy.Status.AvailableReplicas
		operation.Status.HealthCheck = phase
	})
	return err
}

func deploymentReplicas(app *geassv1alpha1.GeassApp) int32 {
	if app.Spec.Replicas == nil {
		return 1
	}
	return *app.Spec.Replicas
}

func appContainerResources(app *geassv1alpha1.GeassApp) corev1.ResourceRequirements {
	if app == nil || len(app.Spec.Resources.Requests) == 0 {
		return platform.DefaultAppResources()
	}
	return app.Spec.Resources
}

func appSourceCommit(app *geassv1alpha1.GeassApp) string {
	if app.Spec.Source.Git != nil {
		return app.Spec.Source.Git.Commit
	}
	return ""
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

// functionConfigMapName is the ConfigMap that holds a function's source file.
func functionConfigMapName(app *geassv1alpha1.GeassApp) string {
	return app.Name + "-function"
}

// deleteIfPresent deletes an object that exists. The read is served from the cache, so a service that never had
// the object costs no request to the API server on each reconcile.
func (r *GeassAppReconciler) deleteIfPresent(ctx context.Context, obj client.Object) error {
	if err := r.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
		return client.IgnoreNotFound(err)
	}
	return client.IgnoreNotFound(r.Delete(ctx, obj))
}

// reconcileFunctionCode publishes a function's code as a ConfigMap, or removes the ConfigMap
// when the service is not a function.
func (r *GeassAppReconciler) reconcileFunctionCode(ctx context.Context, app *geassv1alpha1.GeassApp, wsNS string) error {
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: functionConfigMapName(app), Namespace: wsNS}}
	fn := app.Spec.Source.Function
	if fn == nil {
		if err := r.reconcileTriggerSecret(ctx, app, wsNS); err != nil {
			return err
		}
		return r.deleteIfPresent(ctx, cm)
	}
	files, err := platform.FunctionFiles(fn)
	if err != nil {
		return err
	}
	if err := r.reconcileTriggerSecret(ctx, app, wsNS); err != nil {
		return err
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		applyGeassLabels(cm, app, "GeassApp")
		cm.Data = files
		return setSameNamespaceOwner(app, cm, r.Scheme)
	})
	return err
}

func (r *GeassAppReconciler) reconcileSecret(ctx context.Context, app *geassv1alpha1.GeassApp, wsNS string) error {
	if len(app.Spec.SecretData) == 0 && app.Spec.SecretRef == nil {
		return client.IgnoreNotFound(r.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: app.Name + "-secret", Namespace: wsNS}}))
	}
	secretData := app.Spec.SecretData
	if app.Spec.SecretRef != nil {
		source := &corev1.Secret{}
		if err := r.Get(ctx, client.ObjectKey{Name: app.Spec.SecretRef.Name, Namespace: platform.SystemNamespace}, source); err != nil {
			return err
		}
		secretData = make(map[string]string, len(source.Data))
		for key, value := range source.Data {
			secretData[key] = string(value)
		}
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: app.Name + "-secret", Namespace: wsNS},
	}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		applyGeassLabels(secret, app, "GeassApp")
		setSecretStrings(secret, secretData)
		return setSameNamespaceOwner(app, secret, r.Scheme)
	})
	_ = op
	return err
}

// workloadOption changes how a service's Deployment and Ingress are made.
type workloadOption func(*workloadOptions)

type workloadOptions struct{ scaleToZero scaleToZeroPlan }

// withScaleToZero carries a service's scale-to-zero plan to the pieces that depend on it.
func withScaleToZero(plan scaleToZeroPlan) workloadOption {
	return func(o *workloadOptions) { o.scaleToZero = plan }
}

func newWorkloadOptions(opts []workloadOption) workloadOptions {
	var o workloadOptions
	for _, apply := range opts {
		apply(&o)
	}
	return o
}

func (r *GeassAppReconciler) reconcileDeployment(ctx context.Context, app *geassv1alpha1.GeassApp, wsNS string, opts ...workloadOption) error {
	options := newWorkloadOptions(opts)
	placement, err := platform.ResolvePlacement(ctx, r.Client, app.Spec.Project, app.Spec.RunOn.Tags)
	if err != nil {
		return err
	}
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
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, deploy, func() error {
		applyGeassLabels(deploy, app, "GeassApp")
		selector := geassResourceLabels(app, "GeassApp")
		// A service that sleeps when idle has its copies counted by the scale-to-zero add-on, which stops it and
		// starts it again. Setting them here too would wake it on every reconcile.
		if !options.scaleToZero.active {
			deploy.Spec.Replicas = &replicas
		}
		deploy.Spec.Selector = &metav1.LabelSelector{MatchLabels: selector}
		podLabels := r.appLabels(app)
		maps.Copy(podLabels, selector)
		volumes := []corev1.Volume{}
		volumeMounts := []corev1.VolumeMount{}
		envFrom := append([]corev1.EnvFromSource(nil), app.Spec.EnvFrom...)
		sharedEnv := []corev1.EnvVar{}
		var project geassv1alpha1.GeassProject
		if err := r.Get(ctx, client.ObjectKey{Name: app.Spec.Project, Namespace: platform.SystemNamespace}, &project); err == nil {
			selected := make(map[string]struct{}, len(app.Spec.SharedVariableRefs))
			for _, ref := range app.Spec.SharedVariableRefs {
				selected[ref] = struct{}{}
			}
			selectAll := len(selected) == 0
			for _, variable := range project.Spec.SharedVariables {
				if variable.Environment != string(app.Spec.Environment) {
					continue
				}
				if !selectAll {
					if _, ok := selected[variable.Name]; !ok {
						continue
					}
					if variable.SecretRef != nil {
						sharedEnv = append(sharedEnv, corev1.EnvVar{Name: variable.Name, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: platform.ProjectSharedSecrets}, Key: variable.Name}}})
					} else {
						sharedEnv = append(sharedEnv, corev1.EnvVar{Name: variable.Name, ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: platform.ProjectSharedVars}, Key: variable.Name}}})
					}
					continue
				}
				if variable.SecretRef != nil {
					envFrom = append(envFrom, corev1.EnvFromSource{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: platform.ProjectSharedSecrets}}})
					break
				}
			}
			for _, variable := range project.Spec.SharedVariables {
				if variable.Environment == string(app.Spec.Environment) && variable.SecretRef == nil {
					envFrom = append(envFrom, corev1.EnvFromSource{ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: platform.ProjectSharedVars}}})
					break
				}
			}
		}
		// Service variables are environment variables. They come after the shared project variables so
		// a service can override a shared value.
		if len(app.Spec.ConfigData) > 0 {
			envFrom = append(envFrom, corev1.EnvFromSource{ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: app.Name + "-config"}}})
		}
		if len(app.Spec.SecretData) > 0 || app.Spec.SecretRef != nil {
			envFrom = append(envFrom, corev1.EnvFromSource{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: app.Name + "-secret"}}})
		}
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
		if len(app.Spec.SecretData) > 0 || app.Spec.SecretRef != nil {
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
		command := app.Spec.Build.Command
		env := append(append([]corev1.EnvVar(nil), app.Spec.Env...), sharedEnv...)
		podAnnotations := map[string]string{}
		if revision := app.Annotations[platform.AppDeployRevisionAnnotation]; revision != "" {
			podAnnotations[platform.AppDeployRevisionAnnotation] = revision
		}
		var automountToken *bool
		if fn := app.Spec.Source.Function; fn != nil {
			if files, err := platform.FunctionFiles(fn); err == nil {
				if len(command) == 0 {
					command = platform.FunctionCommand(fn)
				}
				var items []corev1.KeyToPath
				for _, name := range slices.Sorted(maps.Keys(files)) {
					items = append(items, corev1.KeyToPath{Key: name, Path: name})
				}
				volumes = append(volumes, corev1.Volume{
					Name: "function",
					VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{Name: functionConfigMapName(app)},
						Items:                items,
					}},
				})
				volumeMounts = append(volumeMounts, corev1.VolumeMount{Name: "function", MountPath: platform.FunctionMountPath, ReadOnly: true})
				if !hasEnvVar(env, "PORT") {
					env = append(env, corev1.EnvVar{Name: "PORT", Value: strconv.Itoa(int(port))})
				}
				if functionHasTriggerToken(fn) && !hasEnvVar(env, platform.TriggerTokenEnv) {
					env = append(env, triggerTokenEnv(app))
					// The pods read the token once, when they start. Its fingerprint is in the pod template so a
					// token that is made again rolls them, and they and the schedules go on agreeing on it.
					if fingerprint := r.triggerTokenFingerprint(ctx, app, wsNS); fingerprint != "" {
						podAnnotations[annotationTriggerTokenFingerprint] = fingerprint
					}
				}
				// The pod template changes with the code, so a code edit rolls the pods.
				podAnnotations[platform.AnnotationFunctionHash] = platform.FunctionPodHash(fn)
				// User code does not need Kubernetes API credentials.
				noToken := false
				automountToken = &noToken
			}
		}
		deploy.Spec.Template = corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: podLabels, Annotations: podAnnotations},
			Spec: corev1.PodSpec{
				RestartPolicy:                geassRestartPolicy(app.Spec.Deploy.RestartPolicy),
				AutomountServiceAccountToken: automountToken,
				Containers: []corev1.Container{{
					Name:           appContainerName,
					Image:          appImage(app),
					Ports:          []corev1.ContainerPort{{Name: portNameHTTP, ContainerPort: port}},
					Command:        command,
					Args:           app.Spec.Build.Args,
					WorkingDir:     app.Spec.Build.WorkingDir,
					Env:            env,
					EnvFrom:        envFrom,
					ReadinessProbe: app.Spec.Deploy.ReadinessProbe,
					LivenessProbe:  app.Spec.Deploy.LivenessProbe,
					StartupProbe:   app.Spec.Deploy.StartupProbe,
					Resources:      appContainerResources(app),
					VolumeMounts:   volumeMounts,
				}},
				Volumes: volumes,
			},
		}
		if pullSecret := appImagePullSecret(app); pullSecret != nil {
			deploy.Spec.Template.Spec.ImagePullSecrets = []corev1.LocalObjectReference{*pullSecret}
		}
		placement.ApplyToPodSpec(&deploy.Spec.Template.Spec)
		if options.scaleToZero.active {
			// What the pods are made from, so a service that sleeps is known to need checking again when it changes.
			if deploy.Annotations == nil {
				deploy.Annotations = map[string]string{}
			}
			deploy.Annotations[annotationTemplateFingerprint] = fingerprintOf(&deploy.Spec.Template)
		}
		return setSameNamespaceOwner(app, deploy, r.Scheme)
	})
	return err
}

func validateAppSource(app *geassv1alpha1.GeassApp) error {
	if !app.Spec.Deploy.Enabled {
		return nil
	}
	imageSource := app.Spec.Source.Image != nil
	gitSource := app.Spec.Source.Git != nil
	functionSource := app.Spec.Source.Function != nil
	configured := 0
	for _, set := range []bool{imageSource, gitSource, functionSource} {
		if set {
			configured++
		}
	}
	if configured > 1 {
		return fmt.Errorf("source.image, source.git, and source.function are mutually exclusive")
	}
	if configured == 0 {
		return fmt.Errorf("one of spec.source.image, spec.source.git, or spec.source.function is required")
	}
	if imageSource && app.Spec.Source.Image.Image == "" {
		return fmt.Errorf("source.image.image is required")
	}
	if gitSource {
		if app.Spec.Source.Git.ConnectionRef.Name == "" || app.Spec.Source.Git.Repository == "" || app.Spec.Source.Git.Branch == "" {
			return fmt.Errorf("source.git.connectionRef, repository, and branch are required")
		}
	}
	if functionSource {
		if err := platform.ValidateFunctionSource(app.Spec.Source.Function); err != nil {
			return fmt.Errorf("source.function: %w", err)
		}
	}
	return nil
}

func appImage(app *geassv1alpha1.GeassApp) string {
	if app.Status.ResolvedImage != "" {
		return app.Status.ResolvedImage
	}
	if app.Spec.Source.Image != nil {
		return app.Spec.Source.Image.Image
	}
	if fn := app.Spec.Source.Function; fn != nil {
		if fnRuntime, ok := platform.FunctionRuntimeFor(fn.Runtime); ok {
			return fnRuntime.Image
		}
	}
	return ""
}

func hasEnvVar(env []corev1.EnvVar, name string) bool {
	for _, variable := range env {
		if variable.Name == name {
			return true
		}
	}
	return false
}

func appImagePullSecret(app *geassv1alpha1.GeassApp) *corev1.LocalObjectReference {
	if app.Spec.Source.Image == nil {
		return nil
	}
	return app.Spec.Source.Image.PullSecret
}

func (r *GeassAppReconciler) ensureGitBuild(ctx context.Context, app *geassv1alpha1.GeassApp) (*geassv1alpha1.GeassBuild, bool, error) {
	git := app.Spec.Source.Git
	if git == nil {
		return nil, false, fmt.Errorf("git source is required")
	}
	registry := app.Spec.Build.Registry.Repository
	credentialRef := app.Spec.Build.Registry.CredentialRef
	if registry == "" {
		config := &geassv1alpha1.GeassPlatformConfig{}
		if err := r.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, config); err == nil {
			registry = config.Spec.Registry.Repository
			if credentialRef == nil {
				credentialRef = config.Spec.Registry.CredentialRef
			}
		}
	}
	gitCredentialRef := &git.ConnectionRef
	connection := &geassv1alpha1.GeassGitHubConnection{}
	if err := r.Get(ctx, client.ObjectKey{Name: git.ConnectionRef.Name, Namespace: app.Namespace}, connection); err == nil {
		gitCredentialRef = &connection.Spec.SecretRef
	}
	desired := geassv1alpha1.GeassBuildSpec{App: app.Name, Project: app.Spec.Project, Environment: app.Spec.Environment, Repository: git.Repository, Branch: git.Branch, Revision: git.Commit, ConnectionRef: &git.ConnectionRef, GitCredentialRef: gitCredentialRef, Dockerfile: git.Dockerfile, Context: git.Context, WaitForCI: git.WaitForCI, Cache: app.Spec.Build.Cache, CredentialRef: credentialRef, LogStoreRef: app.Spec.Logs.ArchiveStoreRef, Registry: registry}
	name := gitBuildName(app.Name, desired)
	build := &geassv1alpha1.GeassBuild{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: app.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, build, func() error {
		applyGeassLabels(build, app, "GeassBuild")
		build.Spec = desired
		return setSameNamespaceOwner(app, build, r.Scheme)
	})
	return build, err == nil && build.Status.Phase == geassv1alpha1.GeassBuildSucceeded && build.Status.ImageDigest != "", err
}

func gitBuildName(appName string, spec geassv1alpha1.GeassBuildSpec) string {
	encoded, _ := json.Marshal(spec)
	digest := sha256.Sum256(encoded)
	suffix := hex.EncodeToString(digest[:])[:12]
	base := strings.Trim(appName, "-")
	maxBase := 63 - len("-build-") - len(suffix)
	if len(base) > maxBase {
		base = strings.Trim(base[:maxBase], "-")
	}
	return base + "-build-" + suffix
}

func geassRestartPolicy(policy corev1.RestartPolicy) corev1.RestartPolicy {
	if policy == "" {
		return corev1.RestartPolicyAlways
	}
	return policy
}

func (r *GeassAppReconciler) reconcileAutoscaler(ctx context.Context, app *geassv1alpha1.GeassApp, wsNS string) error {
	hpa := &autoscalingv2.HorizontalPodAutoscaler{ObjectMeta: metav1.ObjectMeta{Name: app.Name, Namespace: wsNS}}
	spec := app.Spec.Autoscaling
	if spec == nil || spec.MaxReplicas <= 1 || spec.TargetCPUUtilization == nil {
		if err := r.Get(ctx, client.ObjectKeyFromObject(hpa), hpa); apierrors.IsNotFound(err) {
			return nil
		} else if err != nil {
			return err
		}
		return client.IgnoreNotFound(r.Delete(ctx, hpa))
	}
	min := int32(1)
	if spec.MinReplicas != nil {
		min = *spec.MinReplicas
	}
	if min < 1 || min > spec.MaxReplicas {
		return fmt.Errorf("autoscaling minReplicas must be between 1 and maxReplicas")
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, hpa, func() error {
		applyGeassLabels(hpa, app, "GeassApp")
		hpa.Spec = autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{APIVersion: "apps/v1", Kind: "Deployment", Name: app.Name},
			MinReplicas:    &min,
			MaxReplicas:    spec.MaxReplicas,
			Metrics:        []autoscalingv2.MetricSpec{{Type: autoscalingv2.ResourceMetricSourceType, Resource: &autoscalingv2.ResourceMetricSource{Name: corev1.ResourceCPU, Target: autoscalingv2.MetricTarget{Type: autoscalingv2.UtilizationMetricType, AverageUtilization: spec.TargetCPUUtilization}}}},
		}
		return setSameNamespaceOwner(app, hpa, r.Scheme)
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
			Name:       portNameHTTP,
			Port:       port,
			TargetPort: intstr.FromString(portNameHTTP),
		}}
		return setSameNamespaceOwner(app, svc, r.Scheme)
	})
	return err
}

func (r *GeassAppReconciler) reconcileIngress(ctx context.Context, app *geassv1alpha1.GeassApp, wsNS string, opts ...workloadOption) error {
	options := newWorkloadOptions(opts)
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

	// Traffic to a service that sleeps when idle goes through the add-on's proxy, which is what holds a request
	// while the service wakes.
	backendName, backendPort := app.Name, port
	if options.scaleToZero.active {
		backendName, backendPort = wakeServiceName(app), platform.ServerlessInterceptorPort
	}
	ing := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: app.Name, Namespace: wsNS},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, ing, func() error {
		applyGeassLabels(ing, app, "GeassApp")
		if app.Spec.Ingress.TLSEnabled {
			ing.Annotations = map[string]string{"cert-manager.io/cluster-issuer": platform.ClusterIssuerName, "traefik.ingress.kubernetes.io/router.entrypoints": "websecure"}
		} else {
			ing.Annotations = map[string]string{"traefik.ingress.kubernetes.io/router.entrypoints": "web"}
		}
		ing.Annotations["geass.dev/access-log-app"] = app.Name
		ing.Spec.IngressClassName = stringPtr("traefik")
		ingressRules := app.Spec.Ingress.Rules
		if len(ingressRules) == 0 {
			ingressRules = []geassv1alpha1.GeassAppIngressRule{{Host: app.Spec.Ingress.Host, Path: path}}
		}
		rules := make([]networkingv1.IngressRule, 0, len(ingressRules))
		for _, ingressRule := range ingressRules {
			rulePath := ingressRule.Path
			if rulePath == "" {
				rulePath = "/"
			}
			rules = append(rules, networkingv1.IngressRule{
				Host: ingressRule.Host,
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Path: rulePath, PathType: &pathType,
							Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: backendName, Port: networkingv1.ServiceBackendPort{Number: backendPort}}},
						}},
					},
				},
			})
		}
		ing.Spec.Rules = rules
		if app.Spec.Ingress.TLSEnabled {
			tlsHosts := make([]string, 0, len(ingressRules))
			for _, ingressRule := range ingressRules {
				if ingressRule.Host != "" {
					tlsHosts = append(tlsHosts, ingressRule.Host)
				}
			}
			ing.Spec.TLS = []networkingv1.IngressTLS{{
				Hosts:      tlsHosts,
				SecretName: app.Name + "-tls",
			}}
		} else {
			ing.Spec.TLS = nil
		}
		return setSameNamespaceOwner(app, ing, r.Scheme)
	})
	return err
}

func stringPtr(value string) *string { return &value }

// serviceMonitorSelector picks out the app's Service. A ServiceMonitor matches Services by the labels
// on the Service itself, which are the Geass ownership labels. appLabels are the labels on the pods
// (they are the Service's selector, not its labels), so selecting by them matches no Service at all and
// the app is never scraped.
func serviceMonitorSelector(app *geassv1alpha1.GeassApp) map[string]string {
	return map[string]string{
		labelGeassKind: "GeassApp",
		labelGeassName: app.Name,
	}
}

func (r *GeassAppReconciler) reconcileServiceMonitor(ctx context.Context, app *geassv1alpha1.GeassApp, wsNS string) error {
	name := app.Name + "-metrics"
	if !app.Spec.Metrics.Enabled {
		sm := &monitoringv1.ServiceMonitor{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: wsNS}}
		// Clusters without the Prometheus Operator have no ServiceMonitor kind at all.
		if err := r.Get(ctx, client.ObjectKeyFromObject(sm), sm); apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
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
		metricsPort = portNameHTTP
	}

	sm := &monitoringv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: wsNS},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, sm, func() error {
		applyGeassLabels(sm, app, "GeassApp")
		sm.Spec.Selector = metav1.LabelSelector{MatchLabels: serviceMonitorSelector(app)}
		sm.Spec.Endpoints = []monitoringv1.Endpoint{{
			Port: metricsPort,
			Path: metricsPath,
		}}
		return setSameNamespaceOwner(app, sm, r.Scheme)
	})
	if meta.IsNoMatchError(err) {
		return fmt.Errorf("metrics are enabled but Prometheus Operator is not installed in this cluster")
	}
	return err
}

// appDeletionNamespaces returns every project-environment namespace that may still
// hold workloads for this app. Spec placement can be invalid while status still
// records a previous target namespace after moves or edits.
func appDeletionNamespaces(app *geassv1alpha1.GeassApp) []string {
	seen := make(map[string]struct{})
	var namespaces []string
	add := func(ns string) {
		ns = strings.TrimSpace(ns)
		if ns == "" {
			return
		}
		if _, ok := seen[ns]; ok {
			return
		}
		seen[ns] = struct{}{}
		namespaces = append(namespaces, ns)
	}
	if wsNS, err := resourceNamespace(app.Spec.Project, string(app.Spec.Environment)); err == nil {
		add(wsNS)
	}
	add(app.Status.TargetNamespace)
	return namespaces
}

func (r *GeassAppReconciler) deleteTargetResources(ctx context.Context, app *geassv1alpha1.GeassApp, wsNS string) {
	_ = r.deleteScaleToZero(ctx, app, wsNS)
	_ = r.deleteFunctionTriggers(ctx, app, wsNS, nil)
	names := []string{app.Name, app.Name + "-config", app.Name + "-secret", app.Name + "-metrics", functionConfigMapName(app), triggerSecretName(app)}
	for _, name := range names {
		_ = r.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: wsNS}})
		_ = r.Delete(ctx, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: wsNS}})
		_ = r.Delete(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: wsNS}})
		_ = r.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: wsNS}})
		_ = r.Delete(ctx, &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: wsNS}})
		_ = r.Delete(ctx, &monitoringv1.ServiceMonitor{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: wsNS}})
		_ = r.Delete(ctx, &autoscalingv2.HorizontalPodAutoscaler{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: wsNS}})
	}
	_ = r.Delete(ctx, &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: app.Name, Namespace: wsNS}})
	_ = r.Delete(ctx, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: app.Name, Namespace: wsNS}})
	_ = r.Delete(ctx, &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: app.Name, Namespace: wsNS}})
	if app.Spec.SecretRef != nil {
		_ = r.Delete(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: app.Spec.SecretRef.Name, Namespace: platform.SystemNamespace}})
	}
}

func (r *GeassAppReconciler) setNotReady(ctx context.Context, app *geassv1alpha1.GeassApp, message string) (ctrl.Result, error) {
	latest := &geassv1alpha1.GeassApp{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(app), latest); err != nil {
		return ctrl.Result{}, err
	}
	var result ctrl.Result
	if _, err := platform.PatchStatus(ctx, r.Client, latest, func() {
		var conditions []metav1.Condition
		result, conditions = finishNotReady(latest.Status.Conditions, &latest.Status.ReconcileRetry, &latest.Status.Provision, latest.Generation, message)
		latest.Status.Conditions = conditions
		if app.Status.ResolvedImage != "" {
			latest.Status.ResolvedImage = app.Status.ResolvedImage
			latest.Status.ActiveBuild = app.Status.ActiveBuild
		}
	}); err != nil {
		return ctrl.Result{}, err
	}
	if platform.ReadyConditionReason(latest.Status.Conditions) == platform.ConditionReasonProvisionFailed {
		r.notifyDeployOutcome(ctx, latest, webhooks.EventDeploymentFailed, platform.ProvisionFailedUserMessage)
	}
	return result, nil
}

func (r *GeassAppReconciler) ensureCloudflarePublicHost(ctx context.Context, app *geassv1alpha1.GeassApp) error {
	if strings.TrimSpace(app.Spec.Ingress.Host) != "" {
		return nil
	}
	config := &geassv1alpha1.GeassPlatformConfig{}
	if err := r.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, config); err != nil {
		return client.IgnoreNotFound(err)
	}
	if !platform.IsConditionTrue(config.Status.Conditions, platform.ConditionCloudflareReady) {
		return nil
	}
	rootDomain := platform.RootDomainFromConfig(*config)
	host := platform.AppPublicHostname(app.Name, app.Spec.Project, string(app.Spec.Environment), rootDomain)
	if host == "" {
		return nil
	}
	// Reconcile may have replaced app.Spec with the last-deployed snapshot, so saving a copy of it
	// would overwrite the user's pending edits. Change only the live object.
	live := &geassv1alpha1.GeassApp{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(app), live); err != nil {
		return client.IgnoreNotFound(err)
	}
	if strings.TrimSpace(live.Spec.Ingress.Host) != "" {
		return nil
	}
	live.Spec.Ingress.Host = host
	live.Spec.Ingress.TLSEnabled = false
	return r.Update(ctx, live)
}

// SetupWithManager sets up the controller with the Manager.
func (r *GeassAppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&geassv1alpha1.GeassApp{}).
		Owns(&geassv1alpha1.GeassBuild{}).
		Owns(&geassv1alpha1.GeassDeployment{}).
		// A service's Deployment is in the project's namespace, so it cannot be owned by the service. Its copies
		// are started and stopped by the scale-to-zero add-on, and the service's state follows them.
		Watches(&appsv1.Deployment{}, handler.EnqueueRequestsFromMapFunc(appForDeployment), builder.WithPredicates(deploymentScaleChanged())).
		Watches(&geassv1alpha1.GeassProject{}, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
			var apps geassv1alpha1.GeassAppList
			if err := r.List(ctx, &apps, client.InNamespace(platform.SystemNamespace)); err != nil {
				return nil
			}
			requests := make([]ctrl.Request, 0)
			for _, app := range apps.Items {
				if app.Spec.Project == obj.GetName() {
					requests = append(requests, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&app)})
				}
			}
			return requests
		}), builder.WithPredicates(readyOrSpecChanged())).
		Named("geassapp").
		Complete(r)
}
