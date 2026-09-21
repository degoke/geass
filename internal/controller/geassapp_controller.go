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
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
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
}

// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassapps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassapps/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassapps/finalizers,verbs=update
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

func (r *GeassAppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var app geassv1alpha1.GeassApp
	if err := r.Get(ctx, req.NamespacedName, &app); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !controllerutil.ContainsFinalizer(&app, appFinalizer) {
		controllerutil.AddFinalizer(&app, appFinalizer)
		return ctrl.Result{}, r.Update(ctx, &app)
	}

	if !app.DeletionTimestamp.IsZero() {
		if wsNS, err := resourceNamespace(app.Spec.Project, string(app.Spec.Environment)); err == nil {
			r.deleteTargetResources(ctx, &app, wsNS)
		}
		controllerutil.RemoveFinalizer(&app, appFinalizer)
		return ctrl.Result{}, r.Update(ctx, &app)
	}

	if app.Spec.Deploy.Enabled && appPendingUpdates(&app) > 0 {
		snapshot, ok := platform.LastDeployedSpec(&app)
		if !ok {
			return r.setNotReady(ctx, &app, "Service has pending changes and no last deployed snapshot")
		}
		app.Spec = snapshot
		app.Spec.Deploy.Enabled = true
	}

	if err := platform.ValidateProjectPlacement(ctx, r.Client, app.Spec.Project, app.Spec.Environment); err != nil {
		return r.setNotReady(ctx, &app, err.Error())
	}
	if err := validateAppSource(&app); err != nil {
		return r.setNotReady(ctx, &app, err.Error())
	}

	wsNS, err := resourceNamespace(app.Spec.Project, string(app.Spec.Environment))
	if err != nil {
		return r.setNotReady(ctx, &app, err.Error())
	}

	if app.Spec.Source.Git != nil {
		build, ready, err := r.ensureGitBuild(ctx, &app)
		if err != nil {
			return r.setNotReady(ctx, &app, err.Error())
		}
		if ready {
			app.Status.ResolvedImage = build.Status.ImageDigest
			app.Status.ActiveBuild = build.Name
		}
		if !app.Spec.Deploy.Enabled {
			if prevNS, moved := previousTargetNamespace(app.Status.TargetNamespace, wsNS); moved {
				r.deleteTargetResources(ctx, &app, prevNS)
			}
			r.deleteTargetResources(ctx, &app, wsNS)
			if !ready {
				return r.setNotReady(ctx, &app, "Git source is waiting for a successful build")
			}
			return r.setNotReady(ctx, &app, "Service is configured and waiting for deployment")
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
	if err := r.reconcileSecret(ctx, &app, wsNS); err != nil {
		return r.setNotReady(ctx, &app, err.Error())
	}
	if err := r.reconcileDeployment(ctx, &app, wsNS); err != nil {
		return r.setNotReady(ctx, &app, err.Error())
	}
	if err := r.reconcileAutoscaler(ctx, &app, wsNS); err != nil {
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
	if err := r.reconcileOperationalDeployment(ctx, &app, deploy); err != nil {
		return r.setNotReady(ctx, &app, err.Error())
	}
	if !deploymentReady(deploy, replicas) {
		if app.Spec.Deploy.FailureThreshold > 0 {
			latest := app.DeepCopy()
			if err := r.Get(ctx, client.ObjectKeyFromObject(&app), latest); err == nil {
				latest.Status.HealthCheckFailures++
				if latest.Status.HealthCheckFailures >= app.Spec.Deploy.FailureThreshold {
					latest.Status.RolloutPaused = app.Spec.Deploy.PauseOnFailure
					latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, metav1.ConditionFalse, "RolloutFailed", "Deployment failed the configured health-check threshold")
					if app.Spec.Deploy.RollbackOnFailure {
						latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, "RollbackReady", metav1.ConditionTrue, "RollbackRequested", "Rollback was requested after rollout failure")
					}
					if err := r.Status().Update(ctx, latest); err != nil {
						return ctrl.Result{}, err
					}
					return ctrl.Result{RequeueAfter: platform.RequeueAfterDefault}, nil
				}
				if err := r.Status().Update(ctx, latest); err != nil {
					return ctrl.Result{}, err
				}
			}
		}
		return r.setNotReady(ctx, &app, "Deployment is not available yet")
	}
	if app.Status.HealthCheckFailures != 0 {
		latest := app.DeepCopy()
		if err := r.Get(ctx, client.ObjectKeyFromObject(&app), latest); err == nil {
			latest.Status.HealthCheckFailures = 0
			latest.Status.RolloutPaused = false
			_ = r.Status().Update(ctx, latest)
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

	latest := app.DeepCopy()
	if err := r.Get(ctx, client.ObjectKeyFromObject(&app), latest); err != nil {
		return ctrl.Result{}, err
	}
	latest.Status.TargetNamespace = wsNS
	latest.Status.URL = url
	latest.Status.DNSName = app.Spec.Ingress.Host
	if ingress := new(networkingv1.Ingress); r.Get(ctx, client.ObjectKey{Name: app.Name, Namespace: wsNS}, ingress) == nil && len(ingress.Status.LoadBalancer.Ingress) > 0 {
		latest.Status.TraefikTarget = ingress.Status.LoadBalancer.Ingress[0].Hostname
		if latest.Status.TraefikTarget == "" {
			latest.Status.TraefikTarget = ingress.Status.LoadBalancer.Ingress[0].IP
		}
	}
	latest.Status.DNSVerified = true
	if app.Spec.Ingress.DNSVerification {
		latest.Status.DNSVerified = app.Spec.Ingress.Host != ""
		if latest.Status.DNSVerified {
			lookupCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			_, lookupErr := net.DefaultResolver.LookupHost(lookupCtx, app.Spec.Ingress.Host)
			cancel()
			latest.Status.DNSVerified = lookupErr == nil
		}
	}
	if app.Spec.Ingress.DNSVerification {
		if latest.Status.DNSVerified {
			latest.Status.DNSMessage = "DNS hostname is configured for verification"
		} else {
			latest.Status.DNSMessage = "DNS hostname is required"
		}
	}
	latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, metav1.ConditionTrue, "AppReady", "Application is ready")
	if err := r.Status().Update(ctx, latest); err != nil {
		return ctrl.Result{}, err
	}

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
		"app.kubernetes.io/name":   app.Name,
		platform.K8sLabelManagedBy: platform.ManagedByValue,
		platform.LabelProject:      app.Spec.Project,
		platform.LabelEnvironment:  string(app.Spec.Environment),
	}
}

func (r *GeassAppReconciler) reconcileOperationalDeployment(ctx context.Context, app *geassv1alpha1.GeassApp, deploy *appsv1.Deployment) error {
	name := app.Name + "-active"
	operation := &geassv1alpha1.GeassDeployment{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: app.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, operation, func() error {
		applyGeassLabels(operation, app, "GeassDeployment")
		phase := "RollingOut"
		if deploymentReady(deploy, deploymentReplicas(app)) {
			phase = "Available"
		}
		operation.Spec = geassv1alpha1.GeassDeploymentSpec{App: app.Name, Project: app.Spec.Project, Environment: app.Spec.Environment, Image: appImage(app), ImageDigest: app.Status.ResolvedImage, Replicas: app.Spec.Replicas, BuildRef: app.Status.ActiveBuild, Commit: appSourceCommit(app), Trigger: "Reconciliation", Source: "GeassApp controller"}
		operation.Status.Phase = phase
		operation.Status.AvailableReplicas = deploy.Status.AvailableReplicas
		operation.Status.HealthCheck = phase
		return setSameNamespaceOwner(app, operation, r.Scheme)
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
		secret.StringData = secretData
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
						sharedEnv = append(sharedEnv, corev1.EnvVar{Name: variable.Name, ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "geass-shared-secrets"}, Key: variable.Name}}})
					} else {
						sharedEnv = append(sharedEnv, corev1.EnvVar{Name: variable.Name, ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "geass-shared-variables"}, Key: variable.Name}}})
					}
					continue
				}
				if variable.SecretRef != nil {
					envFrom = append(envFrom, corev1.EnvFromSource{SecretRef: &corev1.SecretEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "geass-shared-secrets"}}})
					break
				}
			}
			for _, variable := range project.Spec.SharedVariables {
				if variable.Environment == string(app.Spec.Environment) && variable.SecretRef == nil {
					envFrom = append(envFrom, corev1.EnvFromSource{ConfigMapRef: &corev1.ConfigMapEnvSource{LocalObjectReference: corev1.LocalObjectReference{Name: "geass-shared-variables"}}})
					break
				}
			}
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
		deploy.Spec.Template = corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: podLabels},
			Spec: corev1.PodSpec{
				RestartPolicy: geassRestartPolicy(app.Spec.Deploy.RestartPolicy),
				Containers: []corev1.Container{{
					Name:           appContainerName,
					Image:          appImage(app),
					Ports:          []corev1.ContainerPort{{Name: portNameHTTP, ContainerPort: port}},
					Command:        app.Spec.Build.Command,
					Args:           app.Spec.Build.Args,
					WorkingDir:     app.Spec.Build.WorkingDir,
					Env:            append(append([]corev1.EnvVar(nil), app.Spec.Env...), sharedEnv...),
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
		return setSameNamespaceOwner(app, deploy, r.Scheme)
	})
	return err
}

func validateAppSource(app *geassv1alpha1.GeassApp) error {
	imageSource := app.Spec.Source.Image != nil
	gitSource := app.Spec.Source.Git != nil
	if imageSource && gitSource {
		return fmt.Errorf("source.image and source.git are mutually exclusive")
	}
	if !imageSource && !gitSource {
		return fmt.Errorf("one of spec.source.image or spec.source.git is required")
	}
	if imageSource && app.Spec.Source.Image.Image == "" {
		return fmt.Errorf("source.image.image is required")
	}
	if gitSource {
		if app.Spec.Source.Git.ConnectionRef.Name == "" || app.Spec.Source.Git.Repository == "" || app.Spec.Source.Git.Branch == "" {
			return fmt.Errorf("source.git.connectionRef, repository, and branch are required")
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
	return ""
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
		return nil, false, fmt.Errorf("Git source is required")
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
			ing.Annotations = map[string]string{"cert-manager.io/cluster-issuer": "letsencrypt-prod", "traefik.ingress.kubernetes.io/router.entrypoints": "websecure"}
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
							Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: app.Name, Port: networkingv1.ServiceBackendPort{Number: port}}},
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
		metricsPort = portNameHTTP
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

func (r *GeassAppReconciler) deleteTargetResources(ctx context.Context, app *geassv1alpha1.GeassApp, wsNS string) {
	names := []string{app.Name, app.Name + "-config", app.Name + "-secret", app.Name + "-metrics"}
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
	latest := app.DeepCopy()
	if err := r.Get(ctx, client.ObjectKeyFromObject(app), latest); err != nil {
		return ctrl.Result{}, err
	}
	latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, metav1.ConditionFalse, "ReconcileError", message)
	if app.Status.ResolvedImage != "" {
		latest.Status.ResolvedImage = app.Status.ResolvedImage
		latest.Status.ActiveBuild = app.Status.ActiveBuild
	}
	if err := r.Status().Update(ctx, latest); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: platform.RequeueAfterDefault}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GeassAppReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&geassv1alpha1.GeassApp{}).
		Owns(&geassv1alpha1.GeassBuild{}).
		Owns(&geassv1alpha1.GeassDeployment{}).
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
		})).
		Named("geassapp").
		Complete(r)
}
