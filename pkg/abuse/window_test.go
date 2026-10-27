package abuse

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var start = time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)

func defaults() Params {
	return Params{ThresholdPercent: DefaultThresholdPercent, Window: DefaultWindow}
}

// busy is a measured sample of the copies "a" at a share of what they are allowed, in percent.
func busy(at time.Time, percent int64) Sample {
	return Sample{At: at, Measured: true, Pods: "a", UsedMilli: percent * 10, AllowedMilli: 1000}
}

// feed gives the run a sample a minute from start, for minutes minutes and one more (the samples at both ends), each made
// by sample, and says at which minute the run first raised a suspicion, or -1.
func feed(run *Run, p Params, minutes int, sample func(minute int, at time.Time) Sample) (*Run, int) {
	first := -1
	for minute := 0; minute <= minutes; minute++ {
		var suspected bool
		run, suspected = Observe(run, sample(minute, start.Add(time.Duration(minute)*time.Minute)), p)
		if suspected && first < 0 {
			first = minute
		}
	}
	return run, first
}

func TestASustainedServiceIsSuspectedWhenItsRunLastsTheWindow(t *testing.T) {
	run, first := feed(nil, defaults(), 40, func(_ int, at time.Time) Sample { return busy(at, 97) })
	require.Equal(t, 30, first, "the 31st sample is 30 minutes after the first")
	require.Equal(t, start, run.Since)
	require.Equal(t, int32(41), run.Samples)
	require.InDelta(t, 97.0, run.AveragePercent(), 0.001)
	require.InDelta(t, 97.0, run.MinPercent(), 0.001)

	_, first = feed(nil, defaults(), 29, func(_ int, at time.Time) Sample { return busy(at, 100) })
	require.Equal(t, -1, first, "29 minutes at all the CPU is not 30")
}

func TestASampleExactlyAtTheThresholdCountsAndOneBelowBreaksTheRun(t *testing.T) {
	_, first := feed(nil, defaults(), 30, func(_ int, at time.Time) Sample { return busy(at, 90) })
	require.Equal(t, 30, first, "90%% of a 90%% threshold counts")

	at := func(_ int, at time.Time) Sample {
		return Sample{At: at, Measured: true, Pods: "a", UsedMilli: 899, AllowedMilli: 1000}
	}
	run, first := feed(nil, defaults(), 60, at)
	require.Equal(t, -1, first, "89.9%% is below")
	require.Nil(t, run)
}

func TestOneQuietMinuteStartsTheCountAgain(t *testing.T) {
	run, first := feed(nil, defaults(), 45, func(minute int, at time.Time) Sample {
		if minute == 20 {
			return busy(at, 50)
		}
		return busy(at, 99)
	})
	require.Equal(t, -1, first, "a run from minute 21 has lasted 24 minutes at minute 45")
	require.Equal(t, start.Add(21*time.Minute), run.Since)
}

func TestARestartOrOtherCopiesStartANewRun(t *testing.T) {
	run, first := feed(nil, defaults(), 40, func(minute int, at time.Time) Sample {
		sample := busy(at, 99)
		if minute >= 25 {
			sample.Pods = "a-restarted"
		}
		return sample
	})
	require.Equal(t, -1, first, "the restart at minute 25 began a new run, 15 minutes long at minute 40")
	require.Equal(t, start.Add(25*time.Minute), run.Since)
	require.Equal(t, "a-restarted", run.Pods)
}

func TestAnUnmeasuredSampleEndsTheRun(t *testing.T) {
	for why, unmeasured := range map[string]Sample{
		"no copies":   {Why: whyNoCopies},
		"a rollout":   {Why: whyChanging},
		"settling":    {Why: whySettling},
		"no use":      {Why: whyNoUse},
		"no limit":    {Why: whyUnbounded},
		"allowed one": {Measured: true, Pods: "a", UsedMilli: 5, AllowedMilli: 0},
	} {
		run, first := feed(nil, defaults(), 40, func(minute int, at time.Time) Sample {
			if minute == 10 {
				unmeasured.At = at
				return unmeasured
			}
			return busy(at, 99)
		})
		require.Equal(t, -1, first, why)
		require.Equal(t, start.Add(11*time.Minute), run.Since, why)
	}
}

func TestAGapLongerThanMaxGapStartsANewRunAndAShortOneDoesNot(t *testing.T) {
	p := defaults()
	run, _ := Observe(nil, busy(start, 99), p)
	run, _ = Observe(run, busy(start.Add(MaxGap), 99), p)
	require.Equal(t, start, run.Since, "a gap of exactly MaxGap carries on")
	run, _ = Observe(run, busy(start.Add(2*MaxGap+time.Second), 99), p)
	require.Equal(t, start.Add(2*MaxGap+time.Second), run.Since, "a longer one is time nobody saw")

	again, _ := Observe(run, busy(run.Last, 99), p)
	require.Equal(t, run.Last, again.Since, "a sample that is not after the last (a clock that went back) starts again")
}

func TestAsLongAsTheRunGoesOnTheSuspicionIsSaidOnEverySample(t *testing.T) {
	run, _ := feed(nil, defaults(), 30, func(_ int, at time.Time) Sample { return busy(at, 95) })
	_, suspected := Observe(run, busy(start.Add(31*time.Minute), 95), defaults())
	require.True(t, suspected, "the caller records it once; Observe only says how long the run is")
}

func TestTheWindowAndThresholdAreTheSettings(t *testing.T) {
	p := Params{ThresholdPercent: 75, Window: 10 * time.Minute}
	_, first := feed(nil, p, 20, func(_ int, at time.Time) Sample { return busy(at, 80) })
	require.Equal(t, 10, first)
	_, first = feed(nil, defaults(), 60, func(_ int, at time.Time) Sample { return busy(at, 80) })
	require.Equal(t, -1, first, "80%% is below the default threshold")
}

func TestQuietTrafficCountsOnlyServicesThatAreSentFewRequests(t *testing.T) {
	p := defaults()
	p.Quiet = &Quiet{MaxRequestsPerMinute: 6, BlindThresholdPercent: 95, BlindWindow: time.Hour}
	withTraffic := func(rate float64) func(int, time.Time) Sample {
		return func(_ int, at time.Time) Sample {
			sample := busy(at, 92)
			sample.TrafficKnown, sample.RequestsPerMinute = true, rate
			return sample
		}
	}
	run, first := feed(nil, p, 40, withTraffic(6))
	require.Equal(t, 30, first, "at most the allowed requests is quiet")
	require.False(t, run.Blind)

	run, first = feed(nil, p, 40, withTraffic(6.5))
	require.Equal(t, -1, first, "a service that is being called is expected to work")
	require.Nil(t, run)
}

func TestWithoutTheRequestsTheStricterThresholdAndLongerWindowApply(t *testing.T) {
	p := defaults()
	p.Quiet = &Quiet{MaxRequestsPerMinute: 0, BlindThresholdPercent: 95, BlindWindow: time.Hour}

	run, first := feed(nil, p, 90, func(_ int, at time.Time) Sample { return busy(at, 92) })
	require.Equal(t, -1, first, "92%% is enough with the requests known, not without them")
	require.Nil(t, run)

	run, first = feed(nil, p, 90, func(_ int, at time.Time) Sample { return busy(at, 96) })
	require.Equal(t, 60, first, "without the requests, the whole hour")
	require.True(t, run.Blind)

	_, first = feed(nil, p, 90, func(minute int, at time.Time) Sample {
		sample := busy(at, 96)
		sample.TrafficKnown = minute != 5 // Prometheus missed one sample
		return sample
	})
	require.Equal(t, 60, first, "one sample without the requests holds the run to the longer window")
}
