package platform

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ConditionReady           = "Ready"
	ConditionAddonsReady     = "AddonsReady"
	ConditionWorkspacesReady = "WorkspacesReady"
)

// SetCondition upserts a condition on the provided slice and returns the updated slice.
func SetCondition(conditions []metav1.Condition, conditionType string, status metav1.ConditionStatus, reason, message string) []metav1.Condition {
	now := metav1.Now()
	for i := range conditions {
		if conditions[i].Type == conditionType {
			if conditions[i].Status == status && conditions[i].Reason == reason && conditions[i].Message == message {
				return conditions
			}
			conditions[i].Status = status
			conditions[i].Reason = reason
			conditions[i].Message = message
			conditions[i].LastTransitionTime = now
			return conditions
		}
	}
	return append(conditions, metav1.Condition{
		Type:               conditionType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: now,
		ObservedGeneration: 0,
	})
}

// IsConditionTrue reports whether the named condition is True.
func IsConditionTrue(conditions []metav1.Condition, conditionType string) bool {
	for _, c := range conditions {
		if c.Type == conditionType {
			return c.Status == metav1.ConditionTrue
		}
	}
	return false
}

// ConditionMessage returns a short human-readable message for a condition type.
func ConditionMessage(conditionType string, ready bool) string {
	if ready {
		return conditionType + " is ready"
	}
	return conditionType + " is not ready"
}

// RequeueAfterDefault is the default requeue interval for progressing reconciles.
const RequeueAfterDefault = 30 * time.Second
