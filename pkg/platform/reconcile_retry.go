package platform

import (
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ConditionReasonReconcileError  = "ReconcileError"
	ConditionReasonProvisionFailed = "ProvisionFailed"

	// ReconcileFailureThreshold is consecutive counted errors before stopping retries (ambiguous/transient cases).
	ReconcileFailureThreshold = 5
	// ReconcileFailureMaxDuration stops retries after this long in a counted failure streak.
	ReconcileFailureMaxDuration = 3 * time.Minute

	ProvisionFailedUserMessage = "Provisioning failed after repeated errors. Update settings or platform configuration, then save or redeploy to try again."
	RetryingProvisionMessage   = "Retrying provisioning"
)

// ReconcileRetryState mirrors GeassReconcileRetryStatus for platform helpers.
type ReconcileRetryState struct {
	Failures          int32
	FailureStarted    *metav1.Time
	FailureGeneration int64
}

// ReadyConditionReason returns the Ready condition reason, if any.
func ReadyConditionReason(conditions []metav1.Condition) string {
	for _, c := range conditions {
		if c.Type == ConditionReady {
			return c.Reason
		}
	}
	return ""
}

// SyncReconcileRetryForGeneration clears retry counters after the user changes spec.
func SyncReconcileRetryForGeneration(retry *ReconcileRetryState, generation int64) {
	if retry == nil {
		return
	}
	if retry.FailureGeneration != 0 && generation != retry.FailureGeneration {
		retry.Failures = 0
		retry.FailureStarted = nil
		retry.FailureGeneration = 0
	}
}

// ReconcileProvisionPaused reports whether reconciliation should idle until spec changes.
func ReconcileProvisionPaused(conditions []metav1.Condition, generation int64, retry ReconcileRetryState) bool {
	if ReadyConditionReason(conditions) != ConditionReasonProvisionFailed {
		return false
	}
	return retry.FailureGeneration == 0 || retry.FailureGeneration == generation
}

// ClearReconcileRetry resets retry tracking after a successful reconcile.
func ClearReconcileRetry(retry *ReconcileRetryState) {
	if retry == nil {
		return
	}
	retry.Failures = 0
	retry.FailureStarted = nil
	retry.FailureGeneration = 0
}

// ReconcileFailureShouldCount returns false for user-wait and in-progress messages.
func ReconcileFailureShouldCount(message string) bool {
	msg := strings.ToLower(strings.TrimSpace(message))
	if msg == "" {
		return false
	}
	if strings.Contains(msg, "waiting for deployment") || strings.Contains(msg, "configured and waiting") {
		return false
	}
	if strings.Contains(msg, "pending changes") {
		return false
	}
	if strings.Contains(msg, "waiting for a successful build") {
		return false
	}
	// A service with no registry to build into waits for one to be set, which is not a provisioning failure.
	if strings.Contains(msg, "no container registry is set") {
		return false
	}
	if strings.Contains(msg, "installing") && strings.Contains(msg, "chart") {
		return false
	}
	if strings.Contains(msg, "configure the bucket name") {
		return false
	}
	// A quota that is used up frees itself when something is stopped or the project raises it. Neither
	// changes the service's own spec, so counting this would pause the service until someone edited it.
	if strings.Contains(msg, strings.ToLower(MessageQuotaUsedUp)) || strings.Contains(msg, "exceeded quota") {
		return false
	}
	if strings.Contains(msg, strings.ToLower(MessageSetUpObjectStorage)) {
		return false
	}
	if strings.Contains(msg, strings.ToLower(MessageObjectStorageCredentials)) {
		return false
	}
	// A server and the databases that start slowly say "... is starting".
	if strings.Contains(msg, "is starting") {
		return false
	}
	if strings.Contains(msg, "postgres database is starting") {
		return false
	}
	if strings.Contains(msg, "restoring database from backup") {
		return false
	}
	if strings.Contains(msg, "creating logical database") {
		return false
	}
	if strings.Contains(msg, "administrator login") {
		return false
	}
	if strings.Contains(msg, "database server is not ready") {
		return false
	}
	if strings.Contains(msg, "database server") && strings.Contains(msg, "unavailable") {
		return false
	}
	// A project, or the cluster it is on, that is not ready yet, or is not there yet, is a wait: a project made a moment
	// ago is not ready for a second or two, and a service applied with it (one kubectl apply of both, in either order)
	// meets that. The retries come within milliseconds, so counting it used up the five tries before the project could be
	// ready and ended the service until someone edited it. ValidateProjectPlacement says: project "x" is not ready,
	// project "x" cluster "y" is not ready, project "x" does not exist. A name that is wrong for good is still said, on the
	// service, for as long as it is wrong.
	if strings.HasPrefix(msg, "project ") && (strings.HasSuffix(msg, " is not ready") || strings.HasSuffix(msg, " does not exist")) {
		return false
	}
	if strings.Contains(msg, "referenced geasscluster is not ready") {
		return false
	}
	if strings.Contains(msg, "geass cluster is not ready") {
		return false
	}
	if strings.Contains(msg, "retrying provisioning") {
		return false
	}
	if strings.Contains(msg, "high availability requires") {
		return false
	}
	if strings.Contains(msg, "clusterref is not supported") || strings.Contains(msg, "clusterrefimmutable") {
		return false
	}
	if strings.Contains(msg, "duplicateenvironment") || strings.Contains(msg, "must be unique") {
		return false
	}
	return true
}

// ReconcileFailureIsPermanent reports errors that will not self-heal with periodic retries.
func ReconcileFailureIsPermanent(message string) bool {
	msg := strings.ToLower(strings.TrimSpace(message))
	if msg == "" {
		return false
	}
	if strings.Contains(msg, "unsupported engine") {
		return true
	}
	if strings.Contains(msg, "clusterref") && strings.Contains(msg, "not supported") {
		return true
	}
	// Network errors ("dial tcp", "connection refused", "no such host") and RBAC "forbidden" errors are
	// deliberately not listed: they are what a restarting API server or service looks like, so they
	// count toward the retry threshold instead of ending retries on the first failure.
	if strings.Contains(msg, "access denied") {
		return true
	}
	if strings.HasPrefix(msg, "put \"http") || strings.Contains(msg, "ensurebucket") {
		return true
	}
	if strings.Contains(msg, "invalid") && (strings.Contains(msg, "credential") || strings.Contains(msg, "configuration")) {
		return true
	}
	if strings.Contains(msg, "required") && (strings.Contains(msg, "aws") || strings.Contains(msg, "connection")) {
		return true
	}
	if strings.Contains(msg, "database creation job failed") {
		return true
	}
	return false
}

// ResetProvisionRetry clears a terminal failure so the controller can reconcile again.
func ResetProvisionRetry(retry *ReconcileRetryState, conditions []metav1.Condition) []metav1.Condition {
	ClearReconcileRetry(retry)
	return SetCondition(conditions, ConditionReady, metav1.ConditionFalse, ConditionReasonReconcileError, RetryingProvisionMessage)
}

// NotReadyOutcome is the result of recording a not-ready reconcile pass.
type NotReadyOutcome struct {
	Conditions   []metav1.Condition
	Retry        ReconcileRetryState
	RequeueAfter time.Duration
	Terminal     bool
}

// ApplyNotReady updates Ready and retry counters for a failed reconcile pass.
func ApplyNotReady(conditions []metav1.Condition, retry ReconcileRetryState, generation int64, message string) NotReadyOutcome {
	if !ReconcileFailureShouldCount(message) {
		ClearReconcileRetry(&retry)
		return NotReadyOutcome{
			Conditions:   SetCondition(conditions, ConditionReady, metav1.ConditionFalse, ConditionReasonReconcileError, message),
			Retry:        retry,
			RequeueAfter: RequeueAfterDefault,
		}
	}

	retry.Failures++
	if retry.FailureStarted == nil {
		now := metav1.Now()
		retry.FailureStarted = &now
	}

	terminal := ReconcileFailureIsPermanent(message) || retry.Failures >= ReconcileFailureThreshold
	if !terminal && retry.FailureStarted != nil {
		if time.Since(retry.FailureStarted.Time) >= ReconcileFailureMaxDuration {
			terminal = true
		}
	}

	if terminal {
		retry.FailureGeneration = generation
		return NotReadyOutcome{
			Conditions: SetCondition(conditions, ConditionReady, metav1.ConditionFalse, ConditionReasonProvisionFailed, ProvisionFailedUserMessage),
			Retry:      retry,
			Terminal:   true,
		}
	}

	return NotReadyOutcome{
		Conditions:   SetCondition(conditions, ConditionReady, metav1.ConditionFalse, ConditionReasonReconcileError, message),
		Retry:        retry,
		RequeueAfter: RequeueAfterDefault,
	}
}
