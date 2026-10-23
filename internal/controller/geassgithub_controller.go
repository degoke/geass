package controller

import (
	"context"
	"fmt"
	"strings"
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

// externalTokenReport is what the tokens of an external App's installations come to: the status of each, the
// TokensReady condition, and the next moment one of them changes state by itself.
type externalTokenReport struct {
	installations []geassv1alpha1.GeassGitHubInstallationStatus
	status        metav1.ConditionStatus
	reason        string
	message       string
	next          time.Time
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
	held := map[int64]*githubapp.InstallationToken{}
	for _, installation := range connection.Spec.External.Installations {
		secret := &corev1.Secret{}
		err := r.Get(ctx, client.ObjectKey{Name: githubapp.ExternalTokenSecretName(connection.Name, installation.ID), Namespace: connection.Namespace}, secret)
		if err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		if err == nil {
			if token, ok := githubapp.TokenFromExternalSecret(secret); ok {
				held[installation.ID] = &token
			}
		}
	}
	report := externalTokens(connection.Spec.External, held, now)

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
		latest.Status.Installations = report.installations
		latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, githubConditionStatus(ready), reason, message)
		latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, githubapp.ConditionTokensReady, report.status, report.reason, report.message)
	}); err != nil {
		return ctrl.Result{}, err
	}
	if report.next.IsZero() {
		return ctrl.Result{}, nil
	}
	// A second past the moment, so the pass that runs then sees the new state.
	return ctrl.Result{RequeueAfter: report.next.Sub(now) + time.Second}, nil
}

// externalTokens judges the tokens of an App's installations at a moment.
func externalTokens(app *geassv1alpha1.GeassGitHubExternalApp, held map[int64]*githubapp.InstallationToken, now time.Time) externalTokenReport {
	report := externalTokenReport{installations: make([]geassv1alpha1.GeassGitHubInstallationStatus, 0, len(app.Installations))}
	var missing, expired, soon []string
	for _, installation := range app.Installations {
		token := held[installation.ID]
		state := githubapp.TokenState(token, now)
		entry := geassv1alpha1.GeassGitHubInstallationStatus{ID: installation.ID, TokenState: state}
		name := fmt.Sprintf("%d (%s)", installation.ID, installation.Account)
		if token != nil {
			entry.TokenExpiresAt = &metav1.Time{Time: token.ExpiresAt}
			for _, at := range []time.Time{token.ExpiresAt.Add(-githubapp.TokenRenewalWindow), token.ExpiresAt.Add(-githubapp.TokenUsableMargin)} {
				if at.After(now) && (report.next.IsZero() || at.Before(report.next)) {
					report.next = at
				}
			}
		}
		switch state {
		case githubapp.TokenMissing:
			missing = append(missing, name)
		case githubapp.TokenExpired:
			expired = append(expired, name)
		case githubapp.TokenExpiringSoon:
			soon = append(soon, name+" expires at "+token.ExpiresAt.UTC().Format(time.RFC3339))
		}
		report.installations = append(report.installations, entry)
	}
	provider := app.Provider
	switch {
	case len(app.Installations) == 0:
		report.status, report.reason = metav1.ConditionTrue, "NoInstallations"
		report.message = fmt.Sprintf("No installation of the GitHub App belongs to this cluster yet; %s lists them", provider)
	case len(missing) > 0 || len(expired) > 0:
		report.status, report.reason = metav1.ConditionFalse, "TokenExpired"
		if len(expired) == 0 {
			report.reason = "TokenMissing"
		}
		var parts []string
		if len(missing) > 0 {
			parts = append(parts, "no token for installation "+listOf(missing))
		}
		if len(expired) > 0 {
			parts = append(parts, "an expired token for installation "+listOf(expired))
		}
		report.message = fmt.Sprintf("Geass has %s. Builds, repository lists and reports for them wait until %s gives a new one", strings.Join(parts, ", and "), provider)
	case len(soon) > 0:
		report.status, report.reason = metav1.ConditionFalse, "TokenExpiringSoon"
		report.message = fmt.Sprintf("The token for installation %s and is due to be renewed by %s. It is still used, so nothing waits yet", listOf(soon), provider)
	default:
		report.status, report.reason = metav1.ConditionTrue, "TokensValid"
		report.message = fmt.Sprintf("Geass holds a token for each of the %d installations, good for at least %s more", len(app.Installations), githubapp.TokenRenewalWindow)
	}
	return report
}

// listOf names a few of many, so a condition's message stays short however many installations there are.
func listOf(names []string) string {
	const shown = 5
	if len(names) <= shown {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:shown], ", "), len(names)-shown)
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
