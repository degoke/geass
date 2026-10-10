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
		"dial tcp: lookup minio: no such host",
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
