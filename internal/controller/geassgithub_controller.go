package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/githubapp"
	"github.com/degoke/geass/pkg/platform"
)

type GeassGitHubConnectionReconciler struct {
	client.Client
	Scheme *runtime.Scheme
	// Now is the clock the state of an external App's tokens is judged by; nil is the system's.
	Now func() time.Time
}

// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassgithubconnections,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassgithubconnections/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *GeassGitHubConnectionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	connection := &geassv1alpha1.GeassGitHubConnection{}
	if err := r.Get(ctx, req.NamespacedName, connection); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if connection.Spec.External != nil {
		return r.reconcileExternal(ctx, connection)
	}
	secret := &corev1.Secret{}
	ready := r.Get(ctx, client.ObjectKey{Name: connection.Spec.SecretRef.Name, Namespace: connection.Namespace}, secret) == nil
	if ready && connection.Spec.WebhookSecretRef != nil {
		ready = r.Get(ctx, client.ObjectKey{Name: connection.Spec.WebhookSecretRef.Name, Namespace: connection.Namespace}, &corev1.Secret{}) == nil
	}
	latest := &geassv1alpha1.GeassGitHubConnection{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(connection), latest); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	reason, message := "ConnectionReady", "GitHub connection Secret is available"
	if !ready {
		reason, message = "SecretUnavailable", "GitHub authentication or webhook Secret is unavailable"
	}
	return ctrl.Result{}, r.writeStatus(ctx, latest, func() {
		latest.Status.Ready = ready
		latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, githubConditionStatus(ready), reason, message)
	})
}

// writeStatus records a connection's status. The CRD requires status.ready, and a merge patch leaves out what did not
// change from the empty status of an object that never had one, ready: false among it, which the API server then refuses
// as missing. So the first status a connection gets is written whole.
func (r *GeassGitHubConnectionReconciler) writeStatus(ctx context.Context, latest *geassv1alpha1.GeassGitHubConnection, mutate func()) error {
	if len(latest.Status.Conditions) == 0 && !latest.Status.Ready {
		mutate()
		return client.IgnoreNotFound(r.Status().Update(ctx, latest))
	}
	_, err := platform.PatchStatus(ctx, r.Client, latest, mutate)
	return client.IgnoreNotFound(err)
}

func (r *GeassGitHubConnectionReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// reconcileExternal records what Geass holds for an external App: whether the secret its provider signs forwarded events
// with is there (Ready), and for each installation whether there is a token and until when (TokensReady). A token runs
// out on its own, so the connection is looked at again when the next one is due for renewal or lapses, besides when a
// token Secret changes.
func (r *GeassGitHubConnectionReconciler) reconcileExternal(ctx context.Context, connection *geassv1alpha1.GeassGitHubConnection) (ctrl.Result, error) {
	now := r.now()
	ready := false
	if ref := connection.Spec.WebhookSecretRef; ref != nil {
		webhook := &corev1.Secret{}
		if err := r.Get(ctx, client.ObjectKey{Name: ref.Name, Namespace: connection.Namespace}, webhook); err == nil {
			ready = len(webhook.Data[githubapp.SecretKeyWebhookSecret]) > 0
		} else if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
	}
	held, err := githubapp.HeldTokens(ctx, r.Client, connection)
	if err != nil {
		return ctrl.Result{}, err
	}
	report := githubapp.JudgeTokens(connection.Spec.External, held, now)

	latest := &geassv1alpha1.GeassGitHubConnection{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(connection), latest); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	reason, message := "ConnectionReady", fmt.Sprintf("Geass takes the GitHub events %s forwards", connection.Spec.External.Provider)
	if !ready {
		reason, message = "SecretUnavailable", fmt.Sprintf("The secret %s signs forwarded GitHub events with is missing, so Geass refuses them", connection.Spec.External.Provider)
	}
	if err := r.writeStatus(ctx, latest, func() {
		latest.Status.Ready = ready
		latest.Status.Installations = report.Installations
		latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, githubConditionStatus(ready), reason, message)
		latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, githubapp.ConditionTokensReady, report.Status, report.Reason, report.Message)
	}); err != nil {
		return ctrl.Result{}, err
	}
	if report.Next.IsZero() {
		return ctrl.Result{}, nil
	}
	// A second past the moment, so the pass that runs then sees the new state.
	return ctrl.Result{RequeueAfter: report.Next.Sub(now) + time.Second}, nil
}

func githubConditionStatus(ready bool) metav1.ConditionStatus {
	if ready {
		return metav1.ConditionTrue
	}
	return metav1.ConditionFalse
}

// connectionOfTokenSecret is the connection a Secret holds a token or the forwarding secret of, by its label.
func connectionOfTokenSecret(_ context.Context, obj client.Object) []reconcile.Request {
	name := obj.GetLabels()[githubapp.LabelGitHubConnection]
	if name == "" {
		return nil
	}
	return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: name, Namespace: obj.GetNamespace()}}}
}

func (r *GeassGitHubConnectionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	labelled := predicate.NewPredicateFuncs(func(obj client.Object) bool { return obj.GetLabels()[githubapp.LabelGitHubConnection] != "" })
	return ctrl.NewControllerManagedBy(mgr).For(&geassv1alpha1.GeassGitHubConnection{}).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(connectionOfTokenSecret), builder.WithPredicates(labelled)).
		Named("geassgithubconnection").Complete(r)
}
