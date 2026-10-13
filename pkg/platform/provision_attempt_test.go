package platform

import (
	"testing"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

func TestBeginProvisionAttempt_prependsRunning(t *testing.T) {
	status := &geassv1alpha1.GeassProvisionStatus{}
	id := BeginProvisionAttempt(status)
	if id == "" || status.ActiveProvisionAttempt != id {
		t.Fatalf("expected active attempt id")
	}
	if len(status.ProvisionAttempts) != 1 || status.ProvisionAttempts[0].Phase != geassv1alpha1.ProvisionAttemptRunning {
		t.Fatalf("expected one running attempt")
	}
}

func TestRecordProvisionAttemptError_dedupes(t *testing.T) {
	status := &geassv1alpha1.GeassProvisionStatus{}
	BeginProvisionAttempt(status)
	RecordProvisionAttemptError(status, "dial tcp: lookup geass-s3", true)
	RecordProvisionAttemptError(status, "dial tcp: lookup geass-s3", true)
	if len(status.ProvisionAttempts[0].Errors) != 1 {
		t.Fatalf("expected deduped errors, got %d", len(status.ProvisionAttempts[0].Errors))
	}
}
