package controller

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func reconcileRetryFromAPI(in *geassv1alpha1.GeassReconcileRetryStatus) platform.ReconcileRetryState {
	if in == nil {
		return platform.ReconcileRetryState{}
	}
	return platform.ReconcileRetryState{
		Failures:          in.ReconcileFailures,
		FailureStarted:    in.ReconcileFailureStarted,
		FailureGeneration: in.ReconcileFailureGeneration,
	}
}

func writeReconcileRetryToAPI(out *geassv1alpha1.GeassReconcileRetryStatus, state platform.ReconcileRetryState) {
	if out == nil {
		return
	}
	out.ReconcileFailures = state.Failures
	out.ReconcileFailureStarted = state.FailureStarted
	out.ReconcileFailureGeneration = state.FailureGeneration
}

func beginProvisionReconcile(conditions []metav1.Condition, generation int64, retry *geassv1alpha1.GeassReconcileRetryStatus) (ctrl.Result, bool) {
	state := reconcileRetryFromAPI(retry)
	platform.SyncReconcileRetryForGeneration(&state, generation)
	writeReconcileRetryToAPI(retry, state)
	if platform.ReconcileProvisionPaused(conditions, generation, state) {
		return ctrl.Result{}, true
	}
	return ctrl.Result{}, false
}

// Provision retry pause must not block finalizer cleanup while the object is terminating.
func beginProvisionReconcileUnlessDeleting(deleting bool, conditions []metav1.Condition, generation int64, retry *geassv1alpha1.GeassReconcileRetryStatus) (ctrl.Result, bool) {
	if deleting {
		return ctrl.Result{}, false
	}
	return beginProvisionReconcile(conditions, generation, retry)
}

func finishNotReady(
	conditions []metav1.Condition,
	retry *geassv1alpha1.GeassReconcileRetryStatus,
	provision *geassv1alpha1.GeassProvisionStatus,
	generation int64,
	message string,
) (ctrl.Result, []metav1.Condition) {
	state := reconcileRetryFromAPI(retry)
	outcome := platform.ApplyNotReady(conditions, state, generation, message)
	writeReconcileRetryToAPI(retry, outcome.Retry)
	recordProvisionOnNotReady(provision, message, outcome)
	platform.CountNotReady(platform.ReadyConditionReason(outcome.Conditions))
	result := ctrl.Result{}
	if outcome.RequeueAfter > 0 {
		result.RequeueAfter = outcome.RequeueAfter
	}
	return result, outcome.Conditions
}
