package controller

import (
	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func recordProvisionOnNotReady(provision *geassv1alpha1.GeassProvisionStatus, message string, outcome platform.NotReadyOutcome) {
	if provision == nil {
		return
	}
	counted := platform.ReconcileFailureShouldCount(message)
	platform.RecordProvisionAttemptError(provision, message, counted)
	if outcome.Terminal {
		platform.EnsureActiveProvisionAttempt(provision)
		if !counted {
			platform.RecordProvisionAttemptError(provision, message, true)
		}
		platform.CompleteActiveProvisionAttempt(provision, geassv1alpha1.ProvisionAttemptFailed, platform.TerminalMessage(message))
	}
}

func completeProvisionSuccess(provision *geassv1alpha1.GeassProvisionStatus) {
	if provision == nil {
		return
	}
	if provision.ActiveProvisionAttempt == "" {
		return
	}
	platform.CompleteActiveProvisionAttempt(provision, geassv1alpha1.ProvisionAttemptSucceeded, "Provisioned successfully")
}
