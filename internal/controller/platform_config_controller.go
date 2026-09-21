package controller

import (
	"context"
	"fmt"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

type GeassPlatformConfigReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	HTTPClient *http.Client
}

// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassplatformconfigs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassplatformconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=nodes;services,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch

func (r *GeassPlatformConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := log.FromContext(ctx)

	var config geassv1alpha1.GeassPlatformConfig
	if err := r.Get(ctx, req.NamespacedName, &config); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	result, requeueAfter := r.reconcileDashboardDomain(ctx, &config)
	if err := r.setDashboardDomainReady(ctx, req.NamespacedName, result); err != nil {
		return ctrl.Result{}, err
	}
	if requeueAfter > 0 && result.Status != metav1.ConditionTrue {
		log.Info("Waiting for dashboard domain verification", "after", requeueAfter)
		return ctrl.Result{RequeueAfter: requeueAfter}, nil
	}
	return ctrl.Result{}, nil
}

// setDashboardDomainReady writes only the DashboardDomainReady condition.
func (r *GeassPlatformConfigReconciler) setDashboardDomainReady(ctx context.Context, key client.ObjectKey, result platform.DashboardDomainReconcileResult) error {
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		latest := &geassv1alpha1.GeassPlatformConfig{}
		if err := r.Get(ctx, key, latest); err != nil {
			return client.IgnoreNotFound(err)
		}
		before := platform.DashboardDomainCondition(latest.Status.Conditions)
		latest.Status.Conditions = platform.SetConditionForGeneration(
			latest.Status.Conditions,
			platform.ConditionDashboardDomainReady,
			result.Status,
			result.Reason,
			result.Message,
			latest.Generation,
		)
		after := platform.DashboardDomainCondition(latest.Status.Conditions)
		if domainConditionUnchanged(before, after) {
			return nil
		}
		if err := r.Status().Update(ctx, latest); err != nil {
			if apierrors.IsConflict(err) {
				lastErr = err
				continue
			}
			return err
		}
		return nil
	}
	return lastErr
}

func domainConditionUnchanged(before, after *metav1.Condition) bool {
	if before == nil && after == nil {
		return true
	}
	if before == nil || after == nil {
		return false
	}
	return before.Status == after.Status &&
		before.Reason == after.Reason &&
		before.Message == after.Message &&
		before.ObservedGeneration == after.ObservedGeneration
}

func (r *GeassPlatformConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).For(&geassv1alpha1.GeassPlatformConfig{}).Named("geassplatformconfig").Complete(r)
}

type GeassCloudConnectionReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=geass.geass.dev,resources=geasscloudconnections,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geasscloudconnections/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *GeassCloudConnectionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var connection geassv1alpha1.GeassCloudConnection
	if err := r.Get(ctx, req.NamespacedName, &connection); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	latest := connection.DeepCopy()
	if err := r.Get(ctx, client.ObjectKeyFromObject(&connection), latest); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if connection.Spec.SecretRef.Name == "" {
		latest.Status.Available = false
		latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, metav1.ConditionFalse, "CredentialsMissing", "A credentials secret is required")
		return ctrl.Result{}, r.Status().Update(ctx, latest)
	}
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Name: connection.Spec.SecretRef.Name, Namespace: connection.Namespace}, secret); err != nil {
		latest.Status.Available = false
		latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, metav1.ConditionFalse, "CredentialsMissing", "Connection credentials are unavailable")
		return ctrl.Result{}, r.Status().Update(ctx, latest)
	}
	ready := false
	reason, message := "CredentialsReady", "Cloud connection is ready"
	switch connection.Spec.Provider {
	case geassv1alpha1.CloudProviderAWS:
		ready = secretValue(secret, platform.SecretKeyAccessKeyID) != "" && secretValue(secret, platform.SecretKeySecretAccessKey) != ""
		if !ready {
			reason, message = "CredentialsMissing", "AWS access key and secret key are required"
		}
	case geassv1alpha1.CloudProviderPlanetScale:
		org := firstNonEmpty(connection.Spec.Organization, secretValue(secret, platform.SecretKeyOrganization))
		ready = secretValue(secret, platform.SecretKeyToken) != "" && org != ""
		if !ready {
			reason, message = "CredentialsMissing", "PlanetScale token and organization are required"
		}
	default:
		reason, message = "UnsupportedProvider", fmt.Sprintf("unsupported provider %q", connection.Spec.Provider)
	}
	status := metav1.ConditionFalse
	if ready {
		status = metav1.ConditionTrue
	}
	latest.Status.Available = ready
	latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, status, reason, message)
	return ctrl.Result{}, r.Status().Update(ctx, latest)
}

func (r *GeassCloudConnectionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).For(&geassv1alpha1.GeassCloudConnection{}).Named("geasscloudconnection").Complete(r)
}
