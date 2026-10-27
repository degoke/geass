package mailrelay

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAWindowHoldsAMessageForItsLengthAndAtMostOneBucketMore(t *testing.T) {
	for _, w := range windows {
		t.Run(w.name, func(t *testing.T) {
			r := newRing(w)
			// Sent in the middle of a bucket: it counts until the window has passed since the end of that bucket.
			sent := testStart.Add(w.bucket / 2)
			r.add(sent)
			require.Equal(t, int64(1), r.count(sent))
			require.Equal(t, int64(1), r.count(sent.Add(w.length)), "it still counts a whole window after it was sent")
			end := testStart.Add(w.bucket + w.length)
			require.Equal(t, int64(1), r.count(end.Add(-time.Nanosecond)))
			require.Equal(t, int64(0), r.count(end), "and not after the window has passed since its bucket ended")
			require.LessOrEqual(t, end.Sub(sent), w.length+w.bucket)
		})
	}
}

func TestAMessageGivenBackNoLongerCounts(t *testing.T) {
	counters := newOrgCounters()
	limits := Limits{MessagesPerMinute: 1, MessagesPerHour: 10, MessagesPerDay: 10}
	held, full := counters.reserve(testStart, limits)
	require.Nil(t, full)
	_, full = counters.reserve(testStart, limits)
	require.Equal(t, minuteWindow.name, full.name, "the place is held while the message is being sent")
	held.release()
	held.release()
	again, full := counters.reserve(testStart, limits)
	require.Nil(t, full, "a message that was not sent gave its place back, once")
	again.keep(testStart, 2, 100)
	again.release()
	_, full = counters.reserve(testStart, limits)
	require.NotNil(t, full, "a message that was sent is not given back")
	require.Equal(t, int64(1), counters.accepted)
}

func TestAGivenBackMessageFromAnOldBucketTakesNothingFromANewOne(t *testing.T) {
	r := newRing(minuteWindow)
	old := r.add(testStart)
	// Long enough later the same slot of the ring holds another second.
	later := testStart.Add(time.Duration(len(r.buckets)) * time.Second)
	r.add(later)
	r.remove(old)
	require.Equal(t, int64(1), r.count(later))
}

func TestReservingAtOnceNeverPassesTheLimit(t *testing.T) {
	counters := newOrgCounters()
	limits := Limits{MessagesPerMinute: 50, MessagesPerHour: 1000, MessagesPerDay: 1000}
	var wg sync.WaitGroup
	var mu sync.Mutex
	taken := 0
	for range 200 {
		wg.Go(func() {
			if held, full := counters.reserve(testStart, limits); full == nil {
				held.keep(testStart, 1, 1)
				mu.Lock()
				taken++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	require.Equal(t, 50, taken)
}

func TestConnectionsAreTakenAndGivenBack(t *testing.T) {
	counters := newOrgCounters()
	require.True(t, counters.acquire(2))
	require.True(t, counters.acquire(2))
	require.False(t, counters.acquire(2))
	counters.releaseConnection()
	require.True(t, counters.acquire(2))
	counters.releaseConnection()
	counters.releaseConnection()
	counters.releaseConnection()
	require.Equal(t, int64(0), counters.connections, "never below none")
}
