package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// FinalizerOrganization holds an organization until the namespace its builds run in is gone.
const FinalizerOrganization = "geass.geass.dev/organization"

// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassorganizations,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassorganizations/finalizers,verbs=update

// GeassOrganizationReconciler cleans up after an organization. What an organization has is mostly its projects', and a
// project cleans up after itself; the one thing that is the organization's own is the namespace its builds run in (see
// platform.OrgBuildPolicy), which is made when its first build starts and has to go when the organization does.
type GeassOrganizationReconciler struct {
	client.Client
}

// Reconcile keeps the finalizer on a live organization, and on a deleted one removes its build namespace and lets it go.
func (r *GeassOrganizationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var org geassv1alpha1.GeassOrganization
	if err := r.Get(ctx, req.NamespacedName, &org); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if org.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&org, FinalizerOrganization) {
			return ctrl.Result{}, nil
		}
		controllerutil.AddFinalizer(&org, FinalizerOrganization)
		return ctrl.Result{}, r.Update(ctx, &org)
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

// SetupWithManager sets up the controller with the Manager.
func (r *GeassOrganizationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).For(&geassv1alpha1.GeassOrganization{}).Named("geassorganization").Complete(r)
}
