/*
Copyright 2026 DEGOKE.

Licensed under the Elastic License 2.0 (the "License"); you may not use this
file except in compliance with the License. You may obtain a copy of the
License at LICENSE or https://www.elastic.co/licensing/elastic-license.
*/

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// GeassProvisionAttemptPhase is the outcome of a single deploy/provision run.
// +kubebuilder:validation:Enum=Running;Succeeded;Failed
type GeassProvisionAttemptPhase string

const (
	ProvisionAttemptRunning   GeassProvisionAttemptPhase = "Running"
	ProvisionAttemptSucceeded GeassProvisionAttemptPhase = "Succeeded"
	ProvisionAttemptFailed    GeassProvisionAttemptPhase = "Failed"
)

// GeassProvisionAttemptError is one user-safe error recorded during a provision attempt.
type GeassProvisionAttemptError struct {
	// RecordedAt is when this error was observed.
	RecordedAt metav1.Time `json:"recordedAt"`
	// Message is product-facing text (no operator internals).
	// +kubebuilder:validation:MaxLength=500
	Message string `json:"message"`
}

// GeassProvisionAttempt tracks one deploy/provision run for a resource.
type GeassProvisionAttempt struct {
	// ID uniquely identifies this attempt within the resource.
	// +kubebuilder:validation:MaxLength=64
	ID string `json:"id"`
	// StartedAt is when provisioning began.
	StartedAt metav1.Time `json:"startedAt"`
	// CompletedAt is set when the attempt reaches a terminal phase.
	// +optional
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`
	// Phase is Running until the attempt succeeds or fails.
	Phase GeassProvisionAttemptPhase `json:"phase"`
	// Summary is a short user-facing outcome line when the attempt completes.
	// +optional
	Summary string `json:"summary,omitempty"`
	// Errors lists distinct errors seen during this attempt (newest last).
	// +optional
	// +kubebuilder:validation:MaxItems=12
	Errors []GeassProvisionAttemptError `json:"errors,omitempty"`
	// BuildRef links to a GeassBuild for image build logs (services with Git source).
	// +optional
	BuildRef string `json:"buildRef,omitempty"`
	// DeploymentRef links to a GeassDeployment row for this deploy (services).
	// +optional
	DeploymentRef string `json:"deploymentRef,omitempty"`
}

// GeassProvisionStatus holds deploy/provision history on workload resources.
type GeassProvisionStatus struct {
	// ActiveProvisionAttempt is the ID of the in-progress attempt, if any.
	// +optional
	ActiveProvisionAttempt string `json:"activeProvisionAttempt,omitempty"`
	// ProvisionAttempts is recent deploy/provision history (newest first, capped in controller).
	// +optional
	// +kubebuilder:validation:MaxItems=8
	ProvisionAttempts []GeassProvisionAttempt `json:"provisionAttempts,omitempty"`
}

// GeassReconcileRetryStatus tracks consecutive provision errors before reconciliation stops.
type GeassReconcileRetryStatus struct {
	// ReconcileFailures counts consecutive infrastructure errors while not ready.
	// +optional
	ReconcileFailures int32 `json:"reconcileFailures,omitempty"`
	// ReconcileFailureStarted is set when ReconcileFailures first increments.
	// +optional
	ReconcileFailureStarted *metav1.Time `json:"reconcileFailureStarted,omitempty"`
	// ReconcileFailureGeneration is spec generation when provisioning was marked failed.
	// +optional
	ReconcileFailureGeneration int64 `json:"reconcileFailureGeneration,omitempty"`
}

// GeassDeploySpec gates whether Geass controllers provision or update infrastructure.
type GeassDeploySpec struct {
	// Enabled is true after the user deploys. False keeps the resource as an editable draft.
	// +optional
	Enabled bool `json:"enabled,omitempty"`
}
