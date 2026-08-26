package controller

import (
	"context"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// GeassConsoleSessionReconciler enforces the short-lived, auditable session
// lifecycle. It deliberately does not persist terminal output or secret data.
type GeassConsoleSessionReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassconsolesessions,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassconsolesessions/status,verbs=get;update;patch

func (r *GeassConsoleSessionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	session := &geassv1alpha1.GeassConsoleSession{}
	if err := r.Get(ctx, req.NamespacedName, session); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if session.Status.Phase == geassv1alpha1.GeassConsoleClosed || session.Status.Phase == geassv1alpha1.GeassConsoleExpired {
		return ctrl.Result{}, nil
	}
	latest := session.DeepCopy()
	if latest.Status.StartedAt == nil {
		now := metav1.Now()
		latest.Status.StartedAt = &now
		latest.Status.Phase = geassv1alpha1.GeassConsoleActive
		if latest.Spec.TimeoutSeconds <= 0 || latest.Spec.TimeoutSeconds > 900 {
			latest.Spec.TimeoutSeconds = 300
		}
		if err := r.Update(ctx, latest); err != nil {
			return ctrl.Result{}, err
		}
	}
	deadline := latest.Status.StartedAt.Add(time.Duration(latest.Spec.TimeoutSeconds) * time.Second)
	if time.Now().After(deadline) {
		now := metav1.Now()
		latest.Status.Phase = geassv1alpha1.GeassConsoleExpired
		latest.Status.EndedAt = &now
		latest.Status.Reason = "Session timeout"
		return ctrl.Result{}, r.Status().Update(ctx, latest)
	}
	return ctrl.Result{RequeueAfter: time.Until(deadline)}, nil
}

func (r *GeassConsoleSessionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).For(&geassv1alpha1.GeassConsoleSession{}).Complete(r)
}

func newConsoleSessionOwner(app *geassv1alpha1.GeassApp, session *geassv1alpha1.GeassConsoleSession, scheme *runtime.Scheme) error {
	return controllerutil.SetControllerReference(app, session, scheme)
}
