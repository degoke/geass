package abuse

import (
	"math"
	"time"
)

// Sample is one look at a service: how much CPU its copies used, of what they are allowed, at a moment.
type Sample struct {
	At time.Time
	// Measured is false when the service could not be measured at that moment. Why says what kept it: no copies, a copy
	// that is starting or was restarted, a rollout or a change in the number of copies, a use the metrics server has not
	// reported, or a container that is allowed any CPU.
	Measured bool
	Why      string
	// Pods identifies the copies that were measured and how often each has restarted. A sample of other copies, or of the
	// same copies after a restart, starts a new run.
	Pods string
	// UsedMilli is what the copies used together, and AllowedMilli what they are allowed together, in thousandths of a core.
	UsedMilli    int64
	AllowedMilli int64
	// TrafficKnown says whether RequestsPerMinute was read. It is only asked for when the settings count quiet services
	// alone.
	TrafficKnown      bool
	RequestsPerMinute float64
}

// Permille is the share of what it is allowed that the service used, in thousandths.
func (s Sample) Permille() int64 {
	if s.AllowedMilli <= 0 {
		return 0
	}
	return s.UsedMilli * 1000 / s.AllowedMilli
}

// Run is an unbroken string of samples of one service at or above the threshold.
type Run struct {
	// Since is the first sample of the run, and Last the latest.
	Since   time.Time `json:"since"`
	Last    time.Time `json:"last"`
	Samples int32     `json:"samples"`
	// SumPermille adds up each sample's share, and MinPermille is the least of them, for the evidence.
	SumPermille int64 `json:"sumPermille"`
	MinPermille int64 `json:"minPermille"`
	// Pods is the copies the run measured (see Sample.Pods).
	Pods string `json:"pods"`
	// Blind is set once a sample of the run was taken without the requests it was to be judged with, which holds the
	// whole run to the stricter window.
	Blind bool `json:"blind,omitempty"`
}

// AveragePercent is the average share of the run's samples, in percent, to a tenth.
func (r Run) AveragePercent() float64 {
	if r.Samples == 0 {
		return 0
	}
	return math.Round(float64(r.SumPermille)/float64(r.Samples)) / 10
}

// MinPercent is the least share of the run's samples, in percent, to a tenth.
func (r Run) MinPercent() float64 { return float64(r.MinPermille) / 10 }

// Lasted is how long the run has gone on.
func (r Run) Lasted() time.Duration { return r.Last.Sub(r.Since) }

// windowFor is how long the run has to last to raise a suspicion.
func (p Params) windowFor(run Run) time.Duration {
	if run.Blind && p.Quiet != nil {
		return p.Quiet.BlindWindow
	}
	return p.Window
}

// thresholdFor is the share a sample has to reach, in thousandths: the stricter one when the settings count quiet
// services alone and the requests were not read.
func (p Params) thresholdFor(sample Sample) int64 {
	if p.blind(sample) {
		return int64(p.Quiet.BlindThresholdPercent) * 10
	}
	return int64(p.ThresholdPercent) * 10
}

func (p Params) blind(sample Sample) bool { return p.Quiet != nil && !sample.TrafficKnown }

// Observe adds a sample to the run of its service, which is nil when it has none, and says whether the run has now lasted
// the window. It returns the run as it is after the sample, nil when the sample ends it.
//
// A run is broken, and the service has none, by a sample that was not measured, that is below the threshold, or (when the
// settings count quiet services alone) of a service sent more requests than they allow. A sample of other copies than the
// run's, or that comes more than MaxGap after the last, or not after it, starts a new run from itself. A run raises a
// suspicion once its first and latest samples are the window apart, or more: with a sample a minute and a window of 30
// minutes, on the 31st sample.
func Observe(run *Run, sample Sample, p Params) (*Run, bool) {
	if !sample.Measured {
		return nil, false
	}
	share := sample.Permille()
	if share < p.thresholdFor(sample) {
		return nil, false
	}
	if p.Quiet != nil && sample.TrafficKnown && sample.RequestsPerMinute > p.Quiet.MaxRequestsPerMinute {
		return nil, false
	}
	var next Run
	if run == nil || run.Pods != sample.Pods || !sample.At.After(run.Last) || sample.At.Sub(run.Last) > MaxGap {
		next = Run{Since: sample.At, Pods: sample.Pods, MinPermille: share}
	} else {
		next = *run
	}
	next.Last = sample.At
	next.Samples++
	next.SumPermille += share
	next.MinPermille = min(next.MinPermille, share)
	next.Blind = next.Blind || p.blind(sample)
	return &next, next.Lasted() >= p.windowFor(next)
}
