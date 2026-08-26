package controller

import (
	"context"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// GeassNetworkLogReconciler bounds access-log retention. Traefik or an
// adapter creates the structured records; this controller removes expired
// records without retaining request bodies.
type GeassNetworkLogReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Retention time.Duration
}

// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassnetworklogs,verbs=get;list;watch;create;delete

func (r *GeassNetworkLogReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := &geassv1alpha1.GeassNetworkLog{}
	if err := r.Get(ctx, req.NamespacedName, log); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	retention := r.Retention
	if retention <= 0 {
		retention = 7 * 24 * time.Hour
	}
	if !log.Spec.Timestamp.Time.IsZero() && time.Since(log.Spec.Timestamp.Time) > retention {
		return ctrl.Result{}, r.Delete(ctx, log)
	}
	return ctrl.Result{RequeueAfter: retention / 2}, nil
}

func (r *GeassNetworkLogReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).For(&geassv1alpha1.GeassNetworkLog{}).Named("geassnetworklog").Complete(r)
}
