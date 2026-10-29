package platform

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/resource"
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

// A copy stopped for memory and started again is a crash loop. A service in one says why, and goes on being looked at:
// ending in ProvisionFailed would pause it until someone edited it, when what stopped the copy may have passed.
func TestACrashLoopFromMemoryIsExplainedNotGivenUpOn(t *testing.T) {
	reserved, limit := resource.MustParse("128Mi"), resource.MustParse("256Mi")
	for name, kill := range map[string]OOMKill{
		"bursting":    {Reserved: reserved, Limit: limit},
		"reserved":    {Reserved: limit, Limit: limit},
		"no limit":    {},
		"no reserved": {Limit: limit},
	} {
		message := kill.Message()
		if ReconcileFailureShouldCount(message) {
			t.Fatalf("%s: counted as a failure: %s", name, message)
		}
		started := metav1.NewTime(time.Now().Add(-ReconcileFailureMaxDuration - time.Minute))
		state := ReconcileRetryState{Failures: ReconcileFailureThreshold, FailureStarted: &started}
		if outcome := ApplyNotReady(nil, state, 1, message); outcome.Terminal || outcome.RequeueAfter == 0 {
			t.Fatalf("%s: a service that runs out of memory must be retried, not given up on: %+v", name, outcome)
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

// The kubelet waits between pulls for minutes. Five passes thirty seconds apart, or three minutes, would end a
// database whose registry was away for two, so a pull that fails is counted for longer than other failures.
func TestAnImageThatCannotBePulledIsCountedForLongerThanOtherFailures(t *testing.T) {
	message := ImagePullMessage("redis:7.4.11-alpine", "ImagePullBackOff")
	if message != "Could not pull the database image redis:7.4.11-alpine (ImagePullBackOff)" {
		t.Fatalf("unexpected message: %s", message)
	}
	if !IsImagePullFailure(message) || IsImagePullFailure("Service is starting") {
		t.Fatalf("a pull failure must be told from a database that is only starting")
	}
	if !ReconcileFailureShouldCount(message) {
		t.Fatalf("an image that cannot be pulled has to be counted, or it would be starting for ever")
	}

	// Past what ends any other failure, in both tries and time.
	past := metav1.NewTime(time.Now().Add(-ReconcileFailureMaxDuration - time.Minute))
	state := ReconcileRetryState{Failures: ReconcileFailureThreshold, FailureStarted: &past}
	if outcome := ApplyNotReady(nil, state, 1, message); outcome.Terminal || outcome.RequeueAfter != RequeueAfterDefault {
		t.Fatalf("a registry that has been away for a few minutes must be retried: %+v", outcome)
	}
	if outcome := ApplyNotReady(nil, state, 1, "upstream request timed out"); !outcome.Terminal {
		t.Fatalf("the same history ends any other failure: %+v", outcome)
	}

	// And not for ever.
	long := metav1.NewTime(time.Now().Add(-ImagePullFailureMaxDuration - time.Minute))
	outcome := ApplyNotReady(nil, ReconcileRetryState{Failures: 2, FailureStarted: &long}, 4, message)
	if !outcome.Terminal || ReadyConditionReason(outcome.Conditions) != ConditionReasonProvisionFailed || outcome.Retry.FailureGeneration != 4 {
		t.Fatalf("an image that cannot be pulled for good must end as a failure: %+v", outcome)
	}
	if outcome.RequeueAfter != 0 {
		t.Fatalf("expected no requeue when terminal")
	}

	// The first failure starts the clock.
	first := ApplyNotReady(nil, ReconcileRetryState{}, 1, message)
	if first.Terminal || first.Retry.FailureStarted == nil || first.Retry.Failures != 1 {
		t.Fatalf("expected the first failure to be counted and timed: %+v", first)
	}
}

func TestADatabaseThatIsStartingIsNotTakenForAnImagePullFailure(t *testing.T) {
	for _, message := range []string{"Service is starting", "Postgres database is starting", "Replacing the Redis chart with Geass's own server", MessageCreatingDatabaseLogin, MessagePromotingReplica} {
		if IsImagePullFailure(message) {
			t.Fatalf("taken for a pull failure: %s", message)
		}
		past := metav1.NewTime(time.Now().Add(-ImagePullFailureMaxDuration - time.Minute))
		state := ReconcileRetryState{Failures: 9, FailureStarted: &past}
		if outcome := ApplyNotReady(nil, state, 1, message); outcome.Terminal || outcome.Retry.Failures != 0 {
			t.Fatalf("a database that is starting is waited for, and its failures forgotten: %+v", outcome)
		}
	}
}

func TestWhichImageCouldNotBePulledIsKeptInTheAttemptHistory(t *testing.T) {
	message := ImagePullMessage("mysql:8.4.11", "ErrImagePull")
	if got := SanitizeProvisionMessage(message); got != message {
		t.Fatalf("the image has to reach the person reading the history, got %q", got)
	}
}

// A service that is given up on because its copy cannot start used to say "Provisioning failed after repeated errors.
// Update settings or platform configuration", which sends a person looking for a fault of the platform when the image
// name has a typo in it.
func TestAServiceGivenUpOnSaysWhyItCannotStart(t *testing.T) {
	// An image that cannot be pulled is given longer than the other failures, because the kubelet retries on its own schedule.
	started := metav1.NewTime(time.Now().Add(-ImagePullFailureMaxDuration).Add(-time.Minute))
	for _, message := range []string{
		"The service cannot start: app is ImagePullBackOff (image nginx:nope)",
		MessageCouldNotPullImage + " redis:7 (ImagePullBackOff)",
	} {
		out := ApplyNotReady(nil, ReconcileRetryState{Failures: 2, FailureStarted: &started}, 2, message)

		require.True(t, out.Terminal, message)
		require.Equal(t, ConditionReasonProvisionFailed, ReadyConditionReason(out.Conditions), message)
		require.Equal(t, message, readyMessage(out.Conditions), "it is given up on, and still says what is wrong")
	}

	out := ApplyNotReady(nil, ReconcileRetryState{Failures: 2, FailureStarted: &started}, 2, "connection refused")
	require.Equal(t, ProvisionFailedUserMessage, readyMessage(out.Conditions), "any other failure is still the general one: the cause may be the platform's")
}

func readyMessage(conditions []metav1.Condition) string {
	for _, condition := range conditions {
		if condition.Type == ConditionReady {
			return condition.Message
		}
	}
	return ""
}
