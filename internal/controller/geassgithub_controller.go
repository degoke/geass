package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

type GeassGitHubConnectionReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassgithubconnections,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassgithubconnections/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *GeassGitHubConnectionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	connection := &geassv1alpha1.GeassGitHubConnection{}
	if err := r.Get(ctx, req.NamespacedName, connection); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	secret := &corev1.Secret{}
	ready := r.Get(ctx, client.ObjectKey{Name: connection.Spec.SecretRef.Name, Namespace: connection.Namespace}, secret) == nil
	if ready && connection.Spec.WebhookSecretRef != nil {
		ready = r.Get(ctx, client.ObjectKey{Name: connection.Spec.WebhookSecretRef.Name, Namespace: connection.Namespace}, &corev1.Secret{}) == nil
	}
	latest := connection.DeepCopy()
	latest.Status.Ready = ready
	reason, message := "ConnectionReady", "GitHub connection Secret is available"
	if !ready {
		reason, message = "SecretUnavailable", "GitHub authentication or webhook Secret is unavailable"
	}
	latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, githubConditionStatus(ready), reason, message)
	return ctrl.Result{}, r.Status().Update(ctx, latest)
}

func githubConditionStatus(ready bool) metav1.ConditionStatus {
	if ready {
		return metav1.ConditionTrue
	}
	return metav1.ConditionFalse
}

func (r *GeassGitHubConnectionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).For(&geassv1alpha1.GeassGitHubConnection{}).Named("geassgithubconnection").Complete(r)
}
