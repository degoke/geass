package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// Every ingress Geass creates asks cert-manager for its certificate from the ClusterIssuer named
// letsencrypt-prod, and cert-manager is installed with no issuers at all. Without one, certificates are
// never issued: the dashboard's domain stays pending and so does every service's HTTPS.
//
// The operator therefore keeps that issuer in place once cert-manager is ready: ACME over HTTP-01,
// solved through the Traefik ingress class. Let's Encrypt wants a contact address for its account, which
// only the operator of the cluster can give (spec.addons.certManager.acmeEmail, set by the installer
// from GEASS_ACME_EMAIL). Without one the issuer is not created, and the ClusterIssuerReady condition
// says so, instead of the reconciler failing or inventing an address.

// +kubebuilder:rbac:groups=cert-manager.io,resources=clusterissuers,verbs=get;list;watch;create;update;patch

const (
	issuerReasonReady        = "IssuerReady"
	issuerReasonExternal     = "ExternallyManaged"
	issuerReasonOtherCluster = "ManagedByAnotherCluster"
	issuerReasonRegistering  = "IssuerRegistering"
	issuerReasonNotReady     = "IssuerNotReady"
	issuerReasonEmailMissing = "ACMEEmailMissing"
	issuerReasonCertManager  = "WaitingForCertManager"
	issuerReasonApplyFailed  = "IssuerApplyFailed"
	issuerReasonAccessDenied = "IssuerAccessDenied"

	// issuerReadyMessage is what is said of an issuer that this cluster keeps and that works.
	issuerReadyMessage = "The " + platform.ClusterIssuerName + " ClusterIssuer is ready to issue certificates"

	// clusterIssuerRetry is how soon to look again while the issuer is not yet usable: cert-manager is
	// installing, or has not finished registering the ACME account. A settled issuer asks for nothing:
	// once the kind exists, changes to the issuer are watched.
	clusterIssuerRetry = 30 * time.Second
)

// clusterIssuerOutcome is what the issuer's condition should say, and when to look again.
type clusterIssuerOutcome struct {
	status  metav1.ConditionStatus
	reason  string
	message string
	requeue time.Duration
	// unknown means the state could not be read, so the condition is left as it was.
	unknown bool
}

// reconcileClusterIssuer keeps the letsencrypt-prod ClusterIssuer in place and reports on it. Like the
// other add-on extras it is background work: a problem is logged, retried and written to the
// ClusterIssuerReady condition, and never held against the cluster's Ready. It returns how soon to look
// again, or zero when there is nothing left to do. certManagerReady is the add-on's readiness as
// reconcileAddons just worked it out.
//
// An issuer that exists but was not created by Geass is left exactly as it is: it may be somebody's own
// (a wildcard DNS-01 issuer, say) and replacing it would break the certificates that depend on it.
func (r *GeassClusterReconciler) reconcileClusterIssuer(ctx context.Context, cluster *geassv1alpha1.GeassCluster, certManagerReady bool) time.Duration {
	log := logf.FromContext(ctx)
	key := client.ObjectKeyFromObject(cluster)

	if !geassv1alpha1.AddonEnabled(cluster.Spec.Addons.CertManager.Enabled) {
		// With cert-manager switched off it is somebody else's to run, and so are its issuers.
		if err := r.setClusterIssuerCondition(ctx, key, nil); err != nil {
			log.Error(err, "Failed to clear ClusterIssuer condition", "cluster", key.Name)
			return clusterIssuerRetry
		}
		return 0
	}

	var watchRetry time.Duration
	if certManagerReady && !r.startClusterIssuerWatch(ctx) {
		watchRetry = clusterIssuerRetry
	}
	outcome := r.ensureClusterIssuer(ctx, cluster, certManagerReady)
	if outcome.unknown {
		return shortestRequeue(outcome.requeue, watchRetry)
	}
	if err := r.setClusterIssuerCondition(ctx, key, &outcome); err != nil {
		log.Error(err, "Failed to record ClusterIssuer condition", "cluster", key.Name)
		return shortestRequeue(outcome.requeue, clusterIssuerRetry)
	}
	return shortestRequeue(outcome.requeue, watchRetry)
}

// startClusterIssuerWatch has changes to the issuer reconcile the clusters, so a deleted or edited issuer is
// put back at once instead of at some later pass. The watch cannot be set up with the controller: the kind
// does not exist until cert-manager is installed, and a watch on a kind that is missing at start-up stops the
// manager. It is started the first time cert-manager is ready, and once. It reports whether nothing is left
// to retry.
func (r *GeassClusterReconciler) startClusterIssuerWatch(ctx context.Context) bool {
	if r.watchClusterIssuers == nil {
		return true
	}
	r.issuerWatchMu.Lock()
	defer r.issuerWatchMu.Unlock()
	if r.issuerWatchStarted {
		return true
	}
	if err := r.watchClusterIssuers(); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to start watching ClusterIssuers")
		return false
	}
	r.issuerWatchStarted = true
	return true
}

// ensureClusterIssuer creates or corrects the issuer when it should, and works out what to report.
func (r *GeassClusterReconciler) ensureClusterIssuer(ctx context.Context, cluster *geassv1alpha1.GeassCluster, certManagerReady bool) clusterIssuerOutcome {
	log := logf.FromContext(ctx)
	waiting := clusterIssuerOutcome{
		status:  metav1.ConditionFalse,
		reason:  issuerReasonCertManager,
		message: "Waiting for cert-manager to be ready before creating the " + platform.ClusterIssuerName + " ClusterIssuer",
		requeue: clusterIssuerRetry,
	}
	if !certManagerReady {
		return waiting
	}

	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(platform.ClusterIssuerGVK)
	err := r.Get(ctx, client.ObjectKey{Name: platform.ClusterIssuerName}, existing)
	switch {
	case meta.IsNoMatchError(err):
		// The chart is up but the API server does not serve cert-manager's kinds yet.
		return waiting
	case apierrors.IsNotFound(err):
		existing = nil
	case apierrors.IsForbidden(err):
		// Not a passing condition, and not one a retry fixes: say what is missing.
		log.Error(err, "Failed to get ClusterIssuer", "name", platform.ClusterIssuerName)
		return clusterIssuerAccessDenied()
	case err != nil:
		log.Error(err, "Failed to get ClusterIssuer", "name", platform.ClusterIssuerName)
		return clusterIssuerOutcome{unknown: true, requeue: clusterIssuerRetry}
	}

	email := strings.TrimSpace(cluster.Spec.Addons.CertManager.ACMEEmail)
	otherOwner := r.otherOwnerOf(ctx, cluster, existing)
	switch {
	case existing != nil && !platform.ClusterIssuerManaged(existing):
		return clusterIssuerState(existing, issuerReasonExternal, "The "+platform.ClusterIssuerName+" ClusterIssuer was not created by Geass and is left unchanged")
	case otherOwner != "":
		// The issuer is cluster-wide, so two GeassClusters that disagree would undo each other forever.
		return clusterIssuerState(existing, issuerReasonOtherCluster,
			"The "+platform.ClusterIssuerName+" ClusterIssuer is managed by GeassCluster "+otherOwner+" and is left unchanged")
	case existing == nil && email == "":
		return clusterIssuerOutcome{
			status: metav1.ConditionFalse,
			reason: issuerReasonEmailMissing,
			message: "No ACME contact email is configured, so the " + platform.ClusterIssuerName + " ClusterIssuer was not created " +
				"and HTTPS certificates cannot be issued; set spec.addons.certManager.acmeEmail on the GeassCluster (the installer reads GEASS_ACME_EMAIL)",
		}
	case email == "":
		// The email was taken away from an issuer Geass made. Leave it: certificates issued through it
		// renew through it, and deleting it would end that.
		return clusterIssuerState(existing, issuerReasonReady, issuerReadyMessage)
	}

	desired := platform.ClusterIssuer(email, cluster.Name)
	if existing == nil {
		if err := r.Create(ctx, desired); err != nil {
			return clusterIssuerApplyFailed(ctx, "create", err)
		}
		log.Info("Created ClusterIssuer", "name", platform.ClusterIssuerName)
		return clusterIssuerState(desired, issuerReasonReady, issuerReadyMessage)
	}
	// Besides a changed spec, an issuer that records no owner, or whose owner is gone, is taken over.
	if !platform.ClusterIssuerSpecCurrent(existing, desired) || platform.ClusterIssuerOwner(existing) != cluster.Name {
		if err := platform.MergeClusterIssuerSpec(existing, desired); err != nil {
			return clusterIssuerApplyFailed(ctx, "update", err)
		}
		existing.SetLabels(desired.GetLabels())
		if err := r.Update(ctx, existing); err != nil {
			return clusterIssuerApplyFailed(ctx, "update", err)
		}
		log.Info("Updated ClusterIssuer", "name", platform.ClusterIssuerName)
		// What cert-manager reports still describes the old spec, and a new email is registered again.
		outcome := clusterIssuerState(existing, issuerReasonReady, issuerReadyMessage)
		outcome.requeue = clusterIssuerRetry
		return outcome
	}
	return clusterIssuerState(existing, issuerReasonReady, issuerReadyMessage)
}

// otherOwnerOf is the name of another GeassCluster that owns the issuer and still exists, or "". An owner
// that is gone leaves the issuer free to be taken over, so deleting the first cluster does not strand it; an
// owner that cannot be looked up is assumed to exist, because leaving the issuer alone is the safe mistake.
func (r *GeassClusterReconciler) otherOwnerOf(ctx context.Context, cluster *geassv1alpha1.GeassCluster, issuer *unstructured.Unstructured) string {
	if issuer == nil || !platform.ClusterIssuerManaged(issuer) {
		return ""
	}
	owner := platform.ClusterIssuerOwner(issuer)
	if owner == "" || owner == cluster.Name {
		return ""
	}
	err := r.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: owner}, &geassv1alpha1.GeassCluster{})
	if apierrors.IsNotFound(err) {
		return ""
	}
	return owner
}

// clusterIssuerAccessDenied says that the operator may not manage ClusterIssuers, which no retry will change.
func clusterIssuerAccessDenied() clusterIssuerOutcome {
	return clusterIssuerOutcome{
		status: metav1.ConditionFalse,
		reason: issuerReasonAccessDenied,
		message: "The operator is not allowed to manage ClusterIssuers; its ClusterRole needs get, list, watch, create, update and patch " +
			"on clusterissuers.cert-manager.io",
		requeue: clusterIssuerRetry,
	}
}

// clusterIssuerApplyFailed reports a failed write. cert-manager validates issuers through its webhook,
// which is not serving for a short while after the chart is installed, so a failure is retried soon and
// shown, not treated as final. The error itself stays in the log: it names in-cluster addresses, which are
// not for a message the dashboard repeats, and it changes from one attempt to the next.
func clusterIssuerApplyFailed(ctx context.Context, verb string, err error) clusterIssuerOutcome {
	// Somebody else changed or created the issuer between the read and the write; the next pass sees it.
	if apierrors.IsConflict(err) || apierrors.IsAlreadyExists(err) {
		return clusterIssuerOutcome{unknown: true, requeue: clusterIssuerRetry}
	}
	logf.FromContext(ctx).Error(err, "Failed to apply ClusterIssuer", "verb", verb, "name", platform.ClusterIssuerName)
	if apierrors.IsForbidden(err) {
		return clusterIssuerAccessDenied()
	}
	return clusterIssuerOutcome{
		status:  metav1.ConditionFalse,
		reason:  issuerReasonApplyFailed,
		message: fmt.Sprintf("Could not %s the %s ClusterIssuer; the operator will retry, and its log says why", verb, platform.ClusterIssuerName),
		requeue: clusterIssuerRetry,
	}
}

// clusterIssuerState reports on an issuer that is in place, from the Ready condition cert-manager keeps on
// it. A new issuer has none for a moment, and one whose ACME registration was refused carries the reason,
// which is the likeliest thing to go wrong (an email at a domain Let's Encrypt rejects). readyReason and
// readyMessage say what a working issuer is, which depends on whose it is.
func clusterIssuerState(issuer *unstructured.Unstructured, readyReason, readyMessage string) clusterIssuerOutcome {
	status, reported, found := platform.ClusterIssuerReadyCondition(issuer)
	switch {
	case !found:
		return clusterIssuerOutcome{
			status:  metav1.ConditionFalse,
			reason:  issuerReasonRegistering,
			message: "Waiting for cert-manager to register the ACME account for the " + platform.ClusterIssuerName + " ClusterIssuer",
			requeue: clusterIssuerRetry,
		}
	case status != metav1.ConditionTrue:
		// Ready can be Unknown, or False with no message, while cert-manager is still working.
		message := fmt.Sprintf("cert-manager reports the %s ClusterIssuer is not ready", platform.ClusterIssuerName)
		if reported != "" {
			message += ": " + reported
		}
		return clusterIssuerOutcome{status: metav1.ConditionFalse, reason: issuerReasonNotReady, message: message, requeue: clusterIssuerRetry}
	default:
		return clusterIssuerOutcome{status: metav1.ConditionTrue, reason: readyReason, message: readyMessage}
	}
}

// setClusterIssuerCondition writes only the ClusterIssuerReady condition, and only when it changed: it
// runs after the cluster's own status was written, and must neither overwrite that nor make the cluster
// reconcile itself again for nothing. A nil outcome removes the condition. LastTransitionTime moves only when
// the status does, as it means "since", so the reason changing under a status that stays False does not
// restart the clock.
func (r *GeassClusterReconciler) setClusterIssuerCondition(ctx context.Context, key client.ObjectKey, outcome *clusterIssuerOutcome) error {
	latest := &geassv1alpha1.GeassCluster{}
	if err := r.Get(ctx, key, latest); err != nil {
		return client.IgnoreNotFound(err)
	}
	_, err := platform.PatchStatus(ctx, r.Client, latest, func() {
		if outcome == nil {
			meta.RemoveStatusCondition(&latest.Status.Conditions, platform.ConditionClusterIssuerReady)
			return
		}
		meta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
			Type:    platform.ConditionClusterIssuerReady,
			Status:  outcome.status,
			Reason:  outcome.reason,
			Message: outcome.message,
		})
	})
	return client.IgnoreNotFound(err)
}
