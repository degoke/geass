package platform

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// MaxProvisionAttempts is how many finished/running deploy rows we keep on each resource CR.
	MaxProvisionAttempts = 8
	// MaxProvisionAttemptErrors is distinct error lines stored per attempt while it is running.
	MaxProvisionAttemptErrors = 12
	// MaxProvisionAttemptErrorsRetained after an attempt completes (terminal trim).
	MaxProvisionAttemptErrorsRetained = 5

	maxProvisionAttempts      = MaxProvisionAttempts
	maxProvisionAttemptErrors = MaxProvisionAttemptErrors
)

// StartFailureMarker is in the message of a service whose copy cannot start. Such a message says what is wrong
// with the service itself, so it is kept as it is and not taken for a fault of the platform.
const StartFailureMarker = "cannot start: "

// SanitizeProvisionMessage returns product-facing text for attempt history.
func SanitizeProvisionMessage(message string) string {
	msg := strings.TrimSpace(message)
	if msg == "" {
		return "Provisioning issue"
	}
	if msg == ProvisionFailedUserMessage || msg == RetryingProvisionMessage {
		return msg
	}
	if strings.Contains(msg, StartFailureMarker) {
		return msg
	}
	if ReconcileFailureIsPermanent(msg) {
		return "Could not reach required platform services"
	}
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "controller") ||
		strings.Contains(lower, ".svc") || strings.Contains(lower, "dial tcp") ||
		strings.Contains(lower, "namespace") || strings.Contains(lower, "helm") {
		return "Object storage or platform services are not available"
	}
	if strings.Contains(lower, "configure the bucket name") {
		return "Bucket name is not configured"
	}
	if strings.Contains(lower, "waiting for deployment") || strings.Contains(lower, "configured and waiting") {
		return "Waiting for deploy"
	}
	if len(msg) > 220 {
		return "Provisioning error"
	}
	return msg
}

// BeginProvisionAttempt starts a new running attempt (newest first).
func BeginProvisionAttempt(status *geassv1alpha1.GeassProvisionStatus) string {
	if status == nil {
		return ""
	}
	now := metav1.Now()
	id := newProvisionAttemptID()
	attempt := geassv1alpha1.GeassProvisionAttempt{
		ID:        id,
		StartedAt: now,
		Phase:     geassv1alpha1.ProvisionAttemptRunning,
	}
	status.ProvisionAttempts = prependAPIAttempt(status.ProvisionAttempts, attempt)
	status.ActiveProvisionAttempt = id
	trimAPIProvisionAttempts(status)
	return id
}

// EnsureActiveProvisionAttempt starts an attempt when errors occur without an explicit deploy.
func EnsureActiveProvisionAttempt(status *geassv1alpha1.GeassProvisionStatus) {
	if status == nil {
		return
	}
	if status.ActiveProvisionAttempt != "" {
		if attempt := findAPIAttempt(status.ProvisionAttempts, status.ActiveProvisionAttempt); attempt != nil && attempt.Phase == geassv1alpha1.ProvisionAttemptRunning {
			return
		}
	}
	BeginProvisionAttempt(status)
}

// RecordProvisionAttemptError appends a user-safe error to the active attempt.
func RecordProvisionAttemptError(status *geassv1alpha1.GeassProvisionStatus, rawMessage string, counted bool) {
	if status == nil || !counted {
		return
	}
	EnsureActiveProvisionAttempt(status)
	if status.ActiveProvisionAttempt == "" {
		return
	}
	message := SanitizeProvisionMessage(rawMessage)
	now := metav1.Now()
	for i := range status.ProvisionAttempts {
		if status.ProvisionAttempts[i].ID != status.ActiveProvisionAttempt {
			continue
		}
		if status.ProvisionAttempts[i].Phase != geassv1alpha1.ProvisionAttemptRunning {
			return
		}
		errors := status.ProvisionAttempts[i].Errors
		if len(errors) > 0 && errors[len(errors)-1].Message == message {
			return
		}
		errors = append(errors, geassv1alpha1.GeassProvisionAttemptError{RecordedAt: now, Message: message})
		if len(errors) > maxProvisionAttemptErrors {
			errors = errors[len(errors)-maxProvisionAttemptErrors:]
		}
		status.ProvisionAttempts[i].Errors = errors
		return
	}
}

// CompleteActiveProvisionAttempt marks the active attempt succeeded or failed.
func CompleteActiveProvisionAttempt(status *geassv1alpha1.GeassProvisionStatus, phase geassv1alpha1.GeassProvisionAttemptPhase, summary string) {
	if status == nil || status.ActiveProvisionAttempt == "" {
		return
	}
	now := metav1.Now()
	for i := range status.ProvisionAttempts {
		if status.ProvisionAttempts[i].ID != status.ActiveProvisionAttempt {
			continue
		}
		status.ProvisionAttempts[i].Phase = phase
		status.ProvisionAttempts[i].Summary = summary
		status.ProvisionAttempts[i].CompletedAt = &now
		provisionDuration.WithLabelValues(string(phase)).Observe(now.Sub(status.ProvisionAttempts[i].StartedAt.Time).Seconds())
		if len(status.ProvisionAttempts[i].Errors) > MaxProvisionAttemptErrorsRetained {
			errs := status.ProvisionAttempts[i].Errors
			status.ProvisionAttempts[i].Errors = errs[len(errs)-MaxProvisionAttemptErrorsRetained:]
		}
		status.ActiveProvisionAttempt = ""
		return
	}
}

// AttachProvisionAttemptRefs sets optional links on the active running attempt.
func AttachProvisionAttemptRefs(status *geassv1alpha1.GeassProvisionStatus, buildRef, deploymentRef string) {
	if status == nil || status.ActiveProvisionAttempt == "" {
		return
	}
	for i := range status.ProvisionAttempts {
		if status.ProvisionAttempts[i].ID != status.ActiveProvisionAttempt {
			continue
		}
		if status.ProvisionAttempts[i].Phase != geassv1alpha1.ProvisionAttemptRunning {
			return
		}
		if buildRef != "" {
			status.ProvisionAttempts[i].BuildRef = buildRef
		}
		if deploymentRef != "" {
			status.ProvisionAttempts[i].DeploymentRef = deploymentRef
		}
		return
	}
}

func prependAPIAttempt(attempts []geassv1alpha1.GeassProvisionAttempt, attempt geassv1alpha1.GeassProvisionAttempt) []geassv1alpha1.GeassProvisionAttempt {
	out := make([]geassv1alpha1.GeassProvisionAttempt, 0, len(attempts)+1)
	out = append(out, attempt)
	out = append(out, attempts...)
	return out
}

func trimAPIProvisionAttempts(status *geassv1alpha1.GeassProvisionStatus) {
	if len(status.ProvisionAttempts) <= maxProvisionAttempts {
		return
	}
	status.ProvisionAttempts = status.ProvisionAttempts[:maxProvisionAttempts]
}

func findAPIAttempt(attempts []geassv1alpha1.GeassProvisionAttempt, id string) *geassv1alpha1.GeassProvisionAttempt {
	for i := range attempts {
		if attempts[i].ID == id {
			return &attempts[i]
		}
	}
	return nil
}

func newProvisionAttemptID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format("20060102150405")))
	}
	return hex.EncodeToString(buf)
}
