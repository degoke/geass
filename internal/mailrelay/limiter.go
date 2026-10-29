package mailrelay

import (
	"sync"
	"time"
)

// Rates are counted in sliding windows of a minute, an hour and a day, each kept as a ring of small fixed buckets (a second
// for the minute, a minute for the hour and the day), so that the memory an organization takes does not grow with what it
// sends. A message is counted in the bucket of the moment it was taken and drops out of a window once a whole window has
// passed since its bucket ended: it counts for at least the window's length and at most one bucket longer. So no window of
// that length ever holds more messages than the limit, and a message sent at the limit is refused exactly there.
//
// A message is counted when the relay takes its MAIL command, before it is sent, so that connections sending at once
// cannot all pass the last free place; one that is then not sent (refused, given up, not taken by the upstream) is given
// back.
//
// Where the counts live: in the memory of the relay's process, one replica. A restart starts every count from nothing, so
// an organization that sent its day's limit can send it again after one, and a second replica would count apart and let
// each organization send twice its limits. The Deployment is kept at one replica, replaced by stopping the old pod first.

// window is one of the limits' periods.
type window struct {
	name   string
	length time.Duration
	bucket time.Duration
}

var (
	minuteWindow = window{name: "minute", length: time.Minute, bucket: time.Second}
	hourWindow   = window{name: "hour", length: time.Hour, bucket: time.Minute}
	dayWindow    = window{name: "day", length: 24 * time.Hour, bucket: time.Minute}
	windows      = []window{minuteWindow, hourWindow, dayWindow}
)

func (w window) limit(l Limits) int64 {
	switch w.name {
	case minuteWindow.name:
		return l.MessagesPerMinute
	case hourWindow.name:
		return l.MessagesPerHour
	}
	return l.MessagesPerDay
}

// ring counts messages in one window.
type ring struct {
	window window
	// buckets hold one more bucket than the window, so that a message counts until the window has passed since its
	// bucket ended.
	buckets []bucket
}

type bucket struct {
	index int64
	count int64
}

func newRing(w window) *ring {
	return &ring{window: w, buckets: make([]bucket, int(w.length/w.bucket)+1)}
}

func (r *ring) indexOf(t time.Time) int64 { return t.UnixNano() / int64(r.window.bucket) }

func (r *ring) slot(index int64) *bucket {
	n := int64(len(r.buckets))
	return &r.buckets[((index%n)+n)%n]
}

// count is how many messages the window holds at a moment.
func (r *ring) count(now time.Time) int64 {
	current := r.indexOf(now)
	oldest := current - int64(len(r.buckets)) + 1
	var total int64
	for _, b := range r.buckets {
		if b.index >= oldest && b.index <= current {
			total += b.count
		}
	}
	return total
}

// add counts a message at a moment and says in which bucket.
func (r *ring) add(now time.Time) int64 {
	index := r.indexOf(now)
	b := r.slot(index)
	if b.index != index {
		*b = bucket{index: index}
	}
	b.count++
	return index
}

// remove gives back a message counted in a bucket, unless the bucket has been reused since.
func (r *ring) remove(index int64) {
	if b := r.slot(index); b.index == index && b.count > 0 {
		b.count--
	}
}

// orgCounters is what the relay counts for one organization. It is kept across reloads of the configuration, by name.
type orgCounters struct {
	mu          sync.Mutex
	rings       []*ring
	connections int64

	// What the usage API reports, since the relay started.
	accepted       int64
	recipients     int64
	bytes          int64
	rejected       map[string]int64
	upstreamFailed map[string]int64
	authFailures   int64
	lastAccepted   time.Time
}

func newOrgCounters() *orgCounters {
	counters := &orgCounters{rejected: map[string]int64{}, upstreamFailed: map[string]int64{}}
	for _, w := range windows {
		counters.rings = append(counters.rings, newRing(w))
	}
	return counters
}

// reservation is a message counted before it is sent, which is given back if it is not.
type reservation struct {
	counters *orgCounters
	indexes  []int64
	done     bool
}

// reserve counts a message when every window has room for it, and otherwise says which window is full.
func (c *orgCounters) reserve(now time.Time, limits Limits) (*reservation, *window) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.rings {
		if r.count(now) >= r.window.limit(limits) {
			full := r.window
			return nil, &full
		}
	}
	res := &reservation{counters: c}
	for _, r := range c.rings {
		res.indexes = append(res.indexes, r.add(now))
	}
	return res, nil
}

// release gives a message back. It does nothing to one already released or kept.
func (res *reservation) release() {
	if res == nil {
		return
	}
	c := res.counters
	c.mu.Lock()
	defer c.mu.Unlock()
	if res.done {
		return
	}
	res.done = true
	for i, r := range c.rings {
		r.remove(res.indexes[i])
	}
}

// keep makes a message's count final: it was sent.
func (res *reservation) keep(now time.Time, recipients, size int64) {
	if res == nil {
		return
	}
	c := res.counters
	c.mu.Lock()
	defer c.mu.Unlock()
	res.done = true
	c.accepted++
	c.recipients += recipients
	c.bytes += size
	c.lastAccepted = now
}

// acquire takes one of the organization's connections, when it has one left.
func (c *orgCounters) acquire(limit int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connections >= limit {
		return false
	}
	c.connections++
	return true
}

func (c *orgCounters) releaseConnection() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.connections > 0 {
		c.connections--
	}
}

func (c *orgCounters) countRejected(reason string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rejected[reason]++
}

func (c *orgCounters) countUpstreamFailure(kind string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.upstreamFailed[kind]++
}

func (c *orgCounters) countAuthFailure() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.authFailures++
}
