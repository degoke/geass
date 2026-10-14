package platform

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestApplyNotReady_resetsOnProgressMessages(t *testing.T) {
	retry := ReconcileRetryState{Failures: 5}
	out := ApplyNotReady(nil, retry, 1, "Resource is configured and waiting for deployment")
	if out.Retry.Failures != 0 {
		t.Fatalf("expected failures reset, got %d", out.Retry.Failures)
	}
	if out.RequeueAfter != RequeueAfterDefault {
		t.Fatalf("expected requeue, got %v", out.RequeueAfter)
	}
}

func TestApplyNotReady_failFastOnPermanentError(t *testing.T) {
	out := ApplyNotReady(nil, ReconcileRetryState{}, 3, `unsupported engine "oracle"`)
	if ReadyConditionReason(out.Conditions) != ConditionReasonProvisionFailed {
		t.Fatalf("expected ProvisionFailed, got %q", ReadyConditionReason(out.Conditions))
	}
	if out.Retry.FailureGeneration != 3 {
		t.Fatalf("expected failure generation 3, got %d", out.Retry.FailureGeneration)
	}
	if out.RequeueAfter != 0 {
		t.Fatalf("expected no requeue when terminal")
	}
}

func TestApplyNotReady_transientNetworkErrorsKeepRetrying(t *testing.T) {
	for _, message := range []string{
		`Get "https://10.96.0.1:443/api/v1/namespaces": dial tcp 10.96.0.1:443: connect: connection refused`,
		"dial tcp: lookup geass-s3: no such host",
		"dial tcp 10.0.0.4:9000: connect: no route to host",
		`secrets is forbidden: User "system:serviceaccount:geass-system:manager" cannot get resource`,
	} {
		if ReconcileFailureIsPermanent(message) {
			t.Fatalf("%q must not end retries on its own", message)
		}
		out := ApplyNotReady(nil, ReconcileRetryState{}, 3, message)
		if ReadyConditionReason(out.Conditions) != ConditionReasonReconcileError {
			t.Fatalf("expected a retryable ReconcileError for %q, got %q", message, ReadyConditionReason(out.Conditions))
		}
		if out.Terminal || out.RequeueAfter != RequeueAfterDefault {
			t.Fatalf("expected a requeue for %q, got terminal=%v requeue=%v", message, out.Terminal, out.RequeueAfter)
		}
	}
}

func TestApplyNotReady_terminalAfterThreshold(t *testing.T) {
	retry := ReconcileRetryState{Failures: ReconcileFailureThreshold - 1}
	out := ApplyNotReady(nil, retry, 3, "upstream request timed out")
	if ReadyConditionReason(out.Conditions) != ConditionReasonProvisionFailed {
		t.Fatalf("expected ProvisionFailed, got %q", ReadyConditionReason(out.Conditions))
	}
	if out.RequeueAfter != 0 {
		t.Fatalf("expected no requeue when terminal")
	}
}

func TestApplyNotReady_terminalAfterMaxDuration(t *testing.T) {
	started := metav1.NewTime(time.Now().Add(-ReconcileFailureMaxDuration).Add(-time.Minute))
	retry := ReconcileRetryState{Failures: 2, FailureStarted: &started}
	out := ApplyNotReady(nil, retry, 2, "connection refused")
	if ReadyConditionReason(out.Conditions) != ConditionReasonProvisionFailed {
		t.Fatalf("expected ProvisionFailed, got %q", ReadyConditionReason(out.Conditions))
	}
}

func TestReconcileProvisionPaused(t *testing.T) {
	conds := SetCondition(nil, ConditionReady, metav1.ConditionFalse, ConditionReasonProvisionFailed, ProvisionFailedUserMessage)
	retry := ReconcileRetryState{FailureGeneration: 4}
	if !ReconcileProvisionPaused(conds, 4, retry) {
		t.Fatal("expected paused at same generation")
	}
	if ReconcileProvisionPaused(conds, 5, retry) {
		t.Fatal("expected not paused after spec change")
	}
}

// Restoring a database from backups takes as long as the data is big. It is progress, not a failure,
// and must not use up the retry budget that gives up on a database after three minutes of errors.
func TestRestoringADatabaseIsNotCountedAsAFailure(t *testing.T) {
	if ReconcileFailureShouldCount("Restoring database from backup") {
		t.Fatal("a restore in progress was counted as a failure")
	}
	started := metav1.NewTime(time.Now().Add(-ReconcileFailureMaxDuration - time.Minute))
	state := ReconcileRetryState{Failures: 3, FailureStarted: &started, FailureGeneration: 1}
	outcome := ApplyNotReady(nil, state, 1, "Restoring database from backup")
	if reason := ReadyConditionReason(outcome.Conditions); reason == ConditionReasonProvisionFailed {
		t.Fatalf("a restore running longer than the retry window was marked failed (reason %q)", reason)
	}
}

func TestApplyNotReady_aMissingRegistryIsAWaitNotAFailure(t *testing.T) {
	message := "no container registry is set for builds: set one under Settings > Registry, or set spec.build.registry.repository on the service"
	retry := ReconcileRetryState{Failures: ReconcileFailureThreshold}
	started := metav1.NewTime(time.Now().Add(-2 * ReconcileFailureMaxDuration))
	retry.FailureStarted = &started

	out := ApplyNotReady(nil, retry, 1, message)

	if ReadyConditionReason(out.Conditions) == ConditionReasonProvisionFailed || out.Terminal {
		t.Fatalf("a service waiting for a registry went terminal: %q", ReadyConditionReason(out.Conditions))
	}
	if out.Retry.Failures != 0 || out.RequeueAfter != RequeueAfterDefault {
		t.Fatalf("expected retry state cleared and a requeue so setting the registry is noticed, got %+v / %v", out.Retry, out.RequeueAfter)
	}
}

// A quota that is used up frees itself when something stops or the project raises it, and neither touches the
// service's own spec. Counting it would end in ProvisionFailed, which pauses the service until that spec is edited.
func TestAQuotaThatIsUsedUpIsWaitedOutNotCountedAsAFailure(t *testing.T) {
	for _, message := range []string{
		MessageQuotaUsedUp + ": geass-project-quota, requested: requests.cpu=500m, used: requests.cpu=3500m, limited: requests.cpu=4",
		`persistentvolumeclaims "data" is forbidden: exceeded quota: geass-project-quota, requested: requests.storage=10Gi`,
	} {
		if ReconcileFailureShouldCount(message) {
			t.Fatalf("counted as a failure: %s", message)
		}
		started := metav1.NewTime(time.Now().Add(-ReconcileFailureMaxDuration - time.Minute))
		state := ReconcileRetryState{Failures: ReconcileFailureThreshold, FailureStarted: &started}
		outcome := ApplyNotReady(nil, state, 1, message)
		if outcome.Terminal || outcome.RequeueAfter == 0 {
			t.Fatalf("a quota that is used up must be retried, not given up on: %+v", outcome)
		}
	}
}

// A service that is applied together with its project meets a project that is not ready yet. The retries come
// milliseconds apart, so counting that used the five tries up at once and the service stayed paused after the project
// was ready. It was found on a real cluster with one kubectl apply of both.
func TestAProjectThatIsNotReadyYetIsWaitedForNotCountedAsAFailure(t *testing.T) {
	for _, message := range []string{
		`project "shop" is not ready`,
		`project "shop" cluster "default" is not ready`,
		`project "shop" does not exist`,
	} {
		if ReconcileFailureShouldCount(message) {
			t.Fatalf("counted as a failure: %s", message)
		}
		state := ReconcileRetryState{Failures: ReconcileFailureThreshold - 1}
		outcome := ApplyNotReady(nil, state, 1, message)
		if outcome.Terminal || outcome.RequeueAfter == 0 {
			t.Fatalf("a project that is not ready yet must be waited for: %+v", outcome)
		}
	}
	// What is wrong for good still counts: a project with no such environment will not fix itself.
	for _, message := range []string{`project "shop" has no staging environment`, `project is required`} {
		if !ReconcileFailureShouldCount(message) {
			t.Fatalf("should still count: %s", message)
		}
	}
}
