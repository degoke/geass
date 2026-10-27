package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// FinalizerOrganization holds an organization until the namespace its builds run in is gone.
const FinalizerOrganization = "geass.geass.dev/organization"

// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassorganizations,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassorganizations/finalizers,verbs=update

// GeassOrganizationReconciler looks after what is the organization's own and not one of its projects': the namespace its
// builds run in (see platform.OrgBuildPolicy), which is made when its first build starts, is held to the organization's
// limits and keeps its outbound limits in its network policy while the organization lives, and has to go when the
// organization does. A project cleans up after itself.
type GeassOrganizationReconciler struct {
	client.Client
}

// +kubebuilder:rbac:groups="",resources=resourcequotas;limitranges,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassplatformconfigs,verbs=get;list;watch

// Reconcile keeps the finalizer on a live organization, and its build namespace's policy and limits up to date, and on a
// deleted one removes its build namespace and lets it go.
func (r *GeassOrganizationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var org geassv1alpha1.GeassOrganization
	if err := r.Get(ctx, req.NamespacedName, &org); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if org.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&org, FinalizerOrganization) {
			controllerutil.AddFinalizer(&org, FinalizerOrganization)
			return ctrl.Result{}, r.Update(ctx, &org)
		}
		return ctrl.Result{}, r.reconcileBuildNamespace(ctx, &org)
	}
	if !controllerutil.ContainsFinalizer(&org, FinalizerOrganization) {
		return ctrl.Result{}, nil
	}
	if err := r.deleteBuildNamespace(ctx, org.Name); err != nil {
		return ctrl.Result{}, err
	}
	controllerutil.RemoveFinalizer(&org, FinalizerOrganization)
	return ctrl.Result{}, r.Update(ctx, &org)
}

// deleteBuildNamespace removes the organization's build namespace, if there is one and Geass made it for the organization.
// A namespace of that name that is not the organization's is left alone.
func (r *GeassOrganizationReconciler) deleteBuildNamespace(ctx context.Context, org string) error {
	name := platform.OrgBuildNamespace(org)
	var ns corev1.Namespace
	if err := r.Get(ctx, client.ObjectKey{Name: name}, &ns); err != nil {
		return client.IgnoreNotFound(err)
	}
	if ns.Labels[platform.LabelOrgBuild] != org {
		logf.FromContext(ctx).Info("Left a namespace that is not the organization's build namespace", "namespace", name, "organization", org)
		return nil
	}
	if err := r.Delete(ctx, &ns); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

// reconcileBuildNamespace writes the policy of the organization's build namespace, with its outbound limits, and holds the
// namespace to the organization's limits, when it has one: one that has not been made yet gets both when its first build
// makes it.
func (r *GeassOrganizationReconciler) reconcileBuildNamespace(ctx context.Context, org *geassv1alpha1.GeassOrganization) error {
	var ns corev1.Namespace
	if err := r.Get(ctx, client.ObjectKey{Name: platform.OrgBuildNamespace(org.Name)}, &ns); err != nil {
		return client.IgnoreNotFound(err)
	}
	if ns.Labels[platform.LabelOrgBuild] != org.Name || !ns.DeletionTimestamp.IsZero() {
		return nil
	}
	if err := applyOrgBuildPolicy(ctx, r.Client, org); err != nil {
		return err
	}
	_, err := reconcileOrgBuildLimits(ctx, r.Client, org)
	return err
}

// SetupWithManager sets up the controller with the Manager.
func (r *GeassOrganizationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).For(&geassv1alpha1.GeassOrganization{}).
		// The limits and outbound limits the platform gives every organization are held in each build namespace.
		Watches(&geassv1alpha1.GeassPlatformConfig{}, handler.EnqueueRequestsFromMapFunc(allOrganizations(r.Client)),
			builder.WithPredicates(predicate.Or(tenantLimitsChanged(), tenantEgressChanged()))).
		// A quota or LimitRange of a build namespace that is edited or deleted by hand is put back.
		Watches(&corev1.ResourceQuota{}, handler.EnqueueRequestsFromMapFunc(organizationOfBuildLimit), builder.WithPredicates(buildLimitEdited())).
		Watches(&corev1.LimitRange{}, handler.EnqueueRequestsFromMapFunc(organizationOfBuildLimit), builder.WithPredicates(buildLimitEdited())).
		// So is the policy of a build namespace.
		Watches(&networkingv1.NetworkPolicy{}, handler.EnqueueRequestsFromMapFunc(organizationOfBuildPolicy)).
		Named("geassorganization").Complete(r)
}
