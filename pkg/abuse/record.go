package abuse

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Suspect is why Geass suspects a service, and what it did, kept on the service in AnnotationSuspect. It stays until a
// platform administrator clears it: a miner that stops when it is seen, or is scaled to zero, must not be let off by that.
type Suspect struct {
	// Source is SourceCPU or SourceReport.
	Source string `json:"source"`
	// Since is when the evidence began: the run's first sample, or the time the report gives.
	Since time.Time `json:"since"`
	// DetectedAt is when Geass recorded it.
	DetectedAt time.Time `json:"detectedAt"`

	// The sustained-CPU signal's evidence: the run's average and least share of what the copies are allowed, in percent,
	// the threshold and window it was held to, how many samples it had, and what was known of the requests it was sent
	// ("quiet": at most the allowed rate in every sample; "unknown": not read, so the stricter threshold and window
	// applied; empty: not considered).
	AveragePercent   float64 `json:"averagePercent,omitempty"`
	MinPercent       float64 `json:"minPercent,omitempty"`
	ThresholdPercent int32   `json:"thresholdPercent,omitempty"`
	WindowMinutes    int32   `json:"windowMinutes,omitempty"`
	Samples          int32   `json:"samples,omitempty"`
	Traffic          string  `json:"traffic,omitempty"`

	// A report's evidence: who reported it, what they said (a rule's name, say), the pod it named and how urgent the
	// reporter thought it.
	Reporter string `json:"reporter,omitempty"`
	Detail   string `json:"detail,omitempty"`
	Pod      string `json:"pod,omitempty"`
	Priority string `json:"priority,omitempty"`

	// Suspended says the service is scaled to zero for it. A release keeps the suspicion and lets the service run.
	Suspended  bool       `json:"suspended"`
	ReleasedAt *time.Time `json:"releasedAt,omitempty"`
	ReleasedBy string     `json:"releasedBy,omitempty"`
}

// Traffic, as a suspect records it.
const (
	TrafficQuiet   = "quiet"
	TrafficUnknown = "unknown"
)

// Review is a platform administrator's clearing of a service, kept on it in AnnotationReview until it ends.
type Review struct {
	At    time.Time `json:"at"`
	Until time.Time `json:"until"`
	By    string    `json:"by,omitempty"`
	Note  string    `json:"note,omitempty"`
	// Cleared is the source of the suspicion that was cleared, or empty for a service that was not suspected.
	Cleared string `json:"cleared,omitempty"`
}

// SuspectOf reads the suspicion a service carries. ok is false for a service that carries none. One that cannot be read
// is a suspended suspect of an unknown source (see sourceUnreadable).
func SuspectOf(object metav1.Object) (suspect *Suspect, ok bool) {
	raw, present := object.GetAnnotations()[AnnotationSuspect]
	if !present {
		return nil, false
	}
	suspect = &Suspect{}
	if err := json.Unmarshal([]byte(raw), suspect); err != nil || (suspect.Source != SourceCPU && suspect.Source != SourceReport) {
		return &Suspect{Source: sourceUnreadable, Suspended: true}, true
	}
	return suspect, true
}

// Suspended reports whether a service is to be scaled to zero for a suspicion.
func Suspended(object metav1.Object) bool {
	suspect, ok := SuspectOf(object)
	return ok && suspect.Suspended
}

// ReviewOf reads the review a service carries, or nil. One that cannot be read is no review.
func ReviewOf(object metav1.Object) *Review {
	raw, present := object.GetAnnotations()[AnnotationReview]
	if !present {
		return nil
	}
	review := &Review{}
	if err := json.Unmarshal([]byte(raw), review); err != nil || review.Until.IsZero() {
		return nil
	}
	return review
}

// Encode writes a suspicion for its annotation.
func (s Suspect) Encode() string {
	out, _ := json.Marshal(s)
	return string(out)
}

// Encode writes a review for its annotation.
func (r Review) Encode() string {
	out, _ := json.Marshal(r)
	return string(out)
}

// Unreadable reports whether the suspicion is one whose annotation could not be read.
func (s Suspect) Unreadable() bool { return s.Source == sourceUnreadable }

// Reason is the reason of the service's AbuseSuspected condition for this suspicion.
func (s Suspect) Reason() string {
	switch {
	case s.Suspended:
		return ReasonSuspended
	case s.Source == SourceReport:
		return ReasonReported
	default:
		return ReasonSustainedCPU
	}
}

// Message says, for the condition and the event, what the suspicion rests on and what was done. It is a signal, and says so.
func (s Suspect) Message() string {
	var evidence string
	switch s.Source {
	case SourceCPU:
		evidence = fmt.Sprintf("Its copies used %.1f%% on average (never below %.1f%%) of the CPU they are allowed, at or above %d%%, for %d minutes from %s",
			s.AveragePercent, s.MinPercent, s.ThresholdPercent, s.WindowMinutes, s.Since.UTC().Format(time.RFC3339))
		switch s.Traffic {
		case TrafficQuiet:
			evidence += ", while it was sent few requests"
		case TrafficUnknown:
			evidence += ", without knowing what requests it was sent"
		}
		evidence += ". That is a signal, not proof: batch work and load tests look the same"
	case SourceReport:
		evidence = "A detector outside Geass"
		if s.Reporter != "" {
			evidence += " (" + s.Reporter + ")"
		}
		evidence += " reported it"
		if s.Detail != "" {
			evidence += ": " + s.Detail
		}
	default:
		evidence = "Its record of the suspicion (the " + AnnotationSuspect + " annotation) cannot be read, so it is held to be suspended"
	}
	evidence = strings.TrimSuffix(evidence, ".") + "."
	switch {
	case s.Suspended:
		return "Abuse is suspected. " + evidence + " It is scaled to zero until a platform administrator releases or clears it; nothing was deleted."
	case s.ReleasedAt != nil:
		return "Abuse is suspected. " + evidence + " A platform administrator let it run again on " + s.ReleasedAt.UTC().Format(time.RFC3339) + "."
	default:
		return "Abuse is suspected. " + evidence + " It is left running until a platform administrator reviews it."
	}
}

// Message says what a review is, for the condition.
func (r Review) Message() string {
	what := "A platform administrator cleared the service"
	if r.Cleared == "" {
		what = "A platform administrator exempted the service"
	}
	return what + " on " + r.At.UTC().Format(time.RFC3339) + "; the sustained-CPU signal leaves it alone until " + r.Until.UTC().Format(time.RFC3339) + "."
}
