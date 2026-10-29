// Package abuse looks for the services of organizations' projects that may be abusing the machines of a cluster that
// organizations share, such as by mining a cryptocurrency on them, and keeps what it found.
//
// Everything it finds is a signal and never proof. What Geass can see of a container from outside it is how much CPU it
// uses (from the cluster's metrics server, metrics.k8s.io, which K3s runs and the CPU autoscaler already needs), how many
// requests the ingress passes it (from Prometheus, only while the monitoring add-on is on), and what a detector outside
// Geass reports. A miner looks like a service that uses all the CPU it is allowed, for a long time, while nobody calls
// it; so do a batch job, a render, a load test and a queue worker that is behind. So a platform administrator reviews each
// suspect, and the default action changes nothing.
//
// What it cannot see: a miner that keeps below the threshold (a throttled or "low and slow" one), that pauses within each
// window, or that restarts itself more often than the window; mining on a GPU; what a program is, what it connects to or
// what it says (a miner on port 443 looks like any other client); builds, which are not services. A tool that watches
// system calls (Falco, Tetragon) sees more, but not inside a pod that runs under gVisor, whose system calls never reach the
// node's kernel. CPU accounting works there all the same.
//
// The sustained-CPU signal keeps, for each service whose CPU use is at the threshold now, an unbroken run of samples (see
// Observe). Runs are in one ConfigMap of the system namespace, written once a minute by the operator that holds the lease,
// so that a restart or a new leader carries on where the last one stopped (within MaxGap) instead of starting every run
// again. A service at rest has no run, so the ConfigMap holds only the few that are busy, and at most MaxRuns of them.
// What Geass decided about a service (it is suspected, and why; it was reviewed) is on the service itself, in two
// annotations, which only Geass and the platform's administrators write: the dashboard gives organizations no way to set
// a service's annotations.
package abuse

import (
	"context"
	"slices"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const (
	// ConditionAbuseSuspected is on a service that is suspected (True) or that a platform administrator has reviewed and
	// exempted for a while (False, Reviewed), and on a project that has suspected services (True). A service that has never
	// been suspected carries none.
	ConditionAbuseSuspected = "AbuseSuspected"

	// ReasonSustainedCPU is a service suspected by the sustained-CPU signal and left running.
	ReasonSustainedCPU = "SustainedCPU"
	// ReasonReported is a service an outside detector reported and that is left running.
	ReasonReported = "Reported"
	// ReasonSuspended is a suspected service that was scaled to zero, whichever signal suspected it.
	ReasonSuspended = "Suspended"
	// ReasonReviewed is a service a platform administrator cleared, which the CPU signal leaves alone until the review ends.
	ReasonReviewed = "Reviewed"
	// ReasonServicesSuspected is a project with at least one suspected service.
	ReasonServicesSuspected = "ServicesSuspected"

	// EventSuspected is the warning event a service gets when it becomes suspected.
	EventSuspected = "AbuseSuspected"
	// EventReleased is the event a suspended service gets when a platform administrator lets it run again.
	EventReleased = "AbuseReleased"
	// EventReviewed is the event a suspected service gets when a platform administrator clears it.
	EventReviewed = "AbuseReviewed"

	// AnnotationSuspect is on a suspected service: what made Geass suspect it, and what was done (a Suspect, as JSON).
	AnnotationSuspect = "geass.dev/abuse-suspect"
	// AnnotationReview is on a service a platform administrator cleared: until when, by whom (a Review, as JSON).
	AnnotationReview = "geass.dev/abuse-review"

	// SourceCPU is a suspicion that the sustained-CPU signal raised.
	SourceCPU = "cpu"
	// SourceReport is a suspicion that a detector outside Geass reported.
	SourceReport = "report"
	// sourceUnreadable is a suspect annotation that could not be read. It is held to be a suspended suspect: only Geass
	// and the platform's administrators write it, and a service is better stopped than let run on a record nobody can read.
	sourceUnreadable = "unreadable"

	// DefaultThresholdPercent is the share of what its copies are allowed that a service has to use.
	DefaultThresholdPercent = 90
	// DefaultWindow is how long it has to use it without a break.
	DefaultWindow = 30 * time.Minute
	// DefaultBlindThresholdPercent is the least threshold when the requests a service is sent cannot be read.
	DefaultBlindThresholdPercent = 95
	// DefaultReviewExemption is how long a service a platform administrator cleared is left out.
	DefaultReviewExemption = 30 * 24 * time.Hour
	// MaxWindow is the longest window.
	MaxWindow = 24 * time.Hour

	// Interval is how often the signal samples.
	Interval = time.Minute
	// MaxGap is the longest time between two samples of one run. A longer one (the operator restarted for long, or the
	// metrics server did not answer) is time nobody saw, and the run starts again.
	MaxGap = 3 * time.Minute
	// SettleTime is how long a copy has to have been running, and each of its containers since it last started, before its
	// use is counted: a program that starts (a JIT, a cache that warms, a migration) is busy for a while.
	SettleTime = 3 * time.Minute
	// MaxRuns is the most runs kept. More services than this at the threshold at once keeps those that have been there
	// longest.
	MaxRuns = 2000
)

// Settings are spec.tenancy.abuse with their defaults filled in.
type Settings struct {
	Params Params
	// Action is the platform's: an organization may replace it (see For).
	Action geassv1alpha1.GeassAbuseAction
	// ReviewExemption is how long a cleared service is left out of the CPU signal.
	ReviewExemption time.Duration
}

// Params are what a sample is judged by.
type Params struct {
	ThresholdPercent int32
	Window           time.Duration
	// Quiet is set when the CPU counts only while the service is sent few requests.
	Quiet *Quiet
}

// Quiet is how few requests a service has to be sent for its CPU use to count, and what applies instead when the requests
// cannot be read.
type Quiet struct {
	MaxRequestsPerMinute  float64
	BlindThresholdPercent int32
	BlindWindow           time.Duration
}

// SettingsOf fills in the defaults of spec.tenancy.abuse. It returns nil when it is unset: Geass then looks for nothing.
func SettingsOf(spec *geassv1alpha1.GeassTenantAbuse) *Settings {
	if spec == nil {
		return nil
	}
	settings := &Settings{
		Params:          Params{ThresholdPercent: DefaultThresholdPercent, Window: DefaultWindow},
		Action:          geassv1alpha1.AbuseActionNone,
		ReviewExemption: DefaultReviewExemption,
	}
	if spec.Action != "" {
		settings.Action = spec.Action
	}
	if spec.ReviewExemptionDays > 0 {
		settings.ReviewExemption = time.Duration(spec.ReviewExemptionDays) * 24 * time.Hour
	}
	cpu := spec.CPU
	if cpu == nil {
		return settings
	}
	if cpu.ThresholdPercent > 0 {
		settings.Params.ThresholdPercent = min(cpu.ThresholdPercent, 100)
	}
	if cpu.WindowMinutes > 0 {
		settings.Params.Window = min(time.Duration(cpu.WindowMinutes)*time.Minute, MaxWindow)
	}
	if quiet := cpu.QuietTraffic; quiet != nil {
		settings.Params.Quiet = &Quiet{
			MaxRequestsPerMinute:  float64(max(quiet.MaxRequestsPerMinute, 0)),
			BlindThresholdPercent: max(settings.Params.ThresholdPercent, DefaultBlindThresholdPercent),
			BlindWindow:           min(2*settings.Params.Window, MaxWindow),
		}
		if fallback := quiet.WithoutTrafficData; fallback != nil {
			if fallback.ThresholdPercent > 0 {
				settings.Params.Quiet.BlindThresholdPercent = min(fallback.ThresholdPercent, 100)
			}
			if fallback.WindowMinutes > 0 {
				settings.Params.Quiet.BlindWindow = min(time.Duration(fallback.WindowMinutes)*time.Minute, MaxWindow)
			}
		}
	}
	return settings
}

// Load reads the settings from the platform configuration. It returns nil when there is none, or it sets none.
func Load(ctx context.Context, reader client.Reader) (*Settings, error) {
	var config geassv1alpha1.GeassPlatformConfig
	err := reader.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, &config)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return SettingsOf(SpecOf(&config)), nil
}

// SpecOf is the platform configuration's spec.tenancy.abuse, or nil.
func SpecOf(config *geassv1alpha1.GeassPlatformConfig) *geassv1alpha1.GeassTenantAbuse {
	if config == nil || config.Spec.Tenancy == nil {
		return nil
	}
	return config.Spec.Tenancy.Abuse
}

// Policy is how the settings treat one organization: what is done to its suspects, and what the CPU signal leaves out.
type Policy struct {
	Action         geassv1alpha1.GeassAbuseAction
	Exempt         bool
	ExemptProjects []string
}

// For is the policy of an organization: the platform's action, or the organization's own in its place, and the
// organization's exemptions.
func (s *Settings) For(org *geassv1alpha1.GeassOrganization) Policy {
	policy := Policy{Action: s.Action}
	if org == nil || org.Spec.Abuse == nil {
		return policy
	}
	own := org.Spec.Abuse
	if own.Action != "" {
		policy.Action = own.Action
	}
	policy.Exempt = own.Exempt
	policy.ExemptProjects = own.ExemptProjects
	return policy
}

// LeavesOut reports whether the CPU signal leaves a project of the organization out.
func (p Policy) LeavesOut(project string) bool {
	return p.Exempt || slices.Contains(p.ExemptProjects, project)
}

// Suspends reports whether a suspect is scaled to zero.
func (p Policy) Suspends() bool {
	return p.Action == geassv1alpha1.AbuseActionSuspend
}
