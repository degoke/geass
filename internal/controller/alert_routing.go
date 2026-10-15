package controller

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/alerts"
	"github.com/degoke/geass/pkg/platform"
)

// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=alertmanagerconfigs,verbs=get;list;watch;create;update;patch;delete

const (
	// alertRoutingResync is how often the AlertmanagerConfig is checked against the settings when nothing has changed.
	// A Secret that a person edits with kubectl (a Slack URL, the mail password) changes nothing the controller watches,
	// and a channel whose secret was missing is left out of the config until the next look.
	alertRoutingResync = 10 * time.Minute
	// alertRoutingWaiting is how soon to look again when the monitoring add-on, which brings the kind, is not there yet.
	alertRoutingWaiting = time.Minute
)

// AlertRoutingReconciler keeps the cluster's AlertmanagerConfig saying what the platform configuration says: a receiver
// and a route for each channel an admin has set up. Alertmanager, which the monitoring add-on installs, does the sending.
//
// The dashboard makes the same change itself when a person saves a setting, so that it is in place at once. This is what
// puts it right when something else changed the settings, when the object was edited or deleted, and when an operator
// that knows more arrives.
type AlertRoutingReconciler struct {
	client.Client
}

// Reconcile makes the AlertmanagerConfig match the settings.
func (r *AlertRoutingReconciler) Reconcile(ctx context.Context, _ ctrl.Request) (ctrl.Result, error) {
	err := alerts.Sync(ctx, r.Client, platform.SystemNamespace)
	switch {
	case err == nil:
		return ctrl.Result{RequeueAfter: alertRoutingResync}, nil
	case meta.IsNoMatchError(err):
		// Nothing to route to until the monitoring add-on has been installed.
		return ctrl.Result{RequeueAfter: alertRoutingWaiting}, nil
	default:
		logf.FromContext(ctx).Error(err, "Could not set up Alertmanager for the alert channels")
		return ctrl.Result{}, err
	}
}

// SetupWithManager sets up the controller with the Manager. It does not watch the AlertmanagerConfig it makes: the kind
// exists only once the monitoring chart is installed, and a watch on one that is not there fails the manager.
func (r *AlertRoutingReconciler) SetupWithManager(mgr ctrl.Manager) error {
	onlyThePlatform := predicate.NewPredicateFuncs(func(object client.Object) bool {
		return object.GetName() == platform.HAReadinessName && object.GetNamespace() == platform.SystemNamespace
	})
	return ctrl.NewControllerManagedBy(mgr).
		Named("alertrouting").
		For(&geassv1alpha1.GeassPlatformConfig{}, builder.WithPredicates(onlyThePlatform, predicate.GenerationChangedPredicate{})).
		Complete(r)
}
