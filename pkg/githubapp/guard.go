package githubapp

import (
	"sync"
	"time"
)

// What Geass reports to GitHub, each of which needs its own permission on the GitHub App.
const (
	CapabilityStatuses    = "statuses"
	CapabilityDeployments = "deployments"
	CapabilityComments    = "comments"
)

// PermissionFor is the GitHub App permission a kind of report needs.
func PermissionFor(capability string) Permission {
	key := ""
	switch capability {
	case CapabilityStatuses:
		key = "statuses"
	case CapabilityDeployments:
		key = "deployments"
	case CapabilityComments:
		key = "pull_requests"
	}
	for _, need := range NeededPermissions {
		if need.Key == key {
			return need
		}
	}
	return Permission{}
}

const (
	// RefusedBackoff is how long reports of one kind to one connection stop after GitHub refused one. A
	// refusal is a permission the app does not have, which only its owner can change, so asking again at
	// once is asking for the same answer; after this long the next report finds out whether it was approved.
	RefusedBackoff = 10 * time.Minute
	// MaxAttempts is how many times a report that failed for a reason that may pass is tried, in all.
	MaxAttempts = 3
	// RetrySpacing is the least time between two tries of the same report.
	RetrySpacing = 10 * time.Second
	// guardLimit bounds each table: they are keyed by connection and by report, and a long-running operator
	// must not collect them for ever.
	guardLimit = 4096
)

// Guard is what keeps reporting to GitHub from hammering it. The controllers reconcile often, and a report
// that is refused (or that GitHub cannot take right now) would otherwise be sent again on every pass. It
// keeps its state in memory: an operator that restarts asks again once, which is what finds out whether a
// permission has been approved since.
type Guard struct {
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	mu       sync.Mutex
	blocked  map[string]time.Time
	logged   map[string]struct{}
	attempts map[string]attempt
	comments map[string]string
	deploys  map[string]int64
}

type attempt struct {
	count int
	last  time.Time
}

// NewGuard returns an empty guard.
func NewGuard() *Guard { return &Guard{} }

var (
	defaultGuard     *Guard
	defaultGuardOnce sync.Once
)

// DefaultGuard is the guard the operator and the dashboard share, so a refusal one of them met is known to the
// other.
func DefaultGuard() *Guard {
	defaultGuardOnce.Do(func() { defaultGuard = NewGuard() })
	return defaultGuard
}

func (g *Guard) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

func blockKey(connection, capability string) string { return connection + "|" + capability }

// Blocked reports whether reports of this kind to this connection are held back because GitHub refused one.
func (g *Guard) Blocked(connection, capability string) bool {
	return g.blockedFor(connection, capability) > 0
}

// blockedFor is how much longer reports of this kind to this connection are held back, or zero when they are not.
// It is what a held-back report asks to be tried again after.
func (g *Guard) blockedFor(connection, capability string) time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	until, ok := g.blocked[blockKey(connection, capability)]
	if !ok {
		return 0
	}
	remaining := until.Sub(g.now())
	if remaining <= 0 {
		delete(g.blocked, blockKey(connection, capability))
		return 0
	}
	return remaining
}

// Block holds back reports of this kind to this connection for d (RefusedBackoff when d is not positive).
func (g *Guard) Block(connection, capability string, d time.Duration) {
	if d <= 0 {
		d = RefusedBackoff
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.blocked == nil || len(g.blocked) >= guardLimit {
		g.blocked = map[string]time.Time{}
	}
	g.blocked[blockKey(connection, capability)] = g.now().Add(d)
}

// anyBlocked reports whether any kind of report to the connection is being held back.
func (g *Guard) anyBlocked(connection string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	for _, capability := range []string{CapabilityStatuses, CapabilityDeployments, CapabilityComments} {
		if until, ok := g.blocked[blockKey(connection, capability)]; ok && !now.After(until) {
			return true
		}
	}
	return false
}

// Unblock lets reports of this kind through again, which a report that went through shows is right.
func (g *Guard) Unblock(connection, capability string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.blocked, blockKey(connection, capability))
}

// Once is true the first time it is asked about a key and false after, so that a problem is logged once and
// not on every reconcile. The key is what makes the problem the same problem: the connection and the reason.
func (g *Guard) Once(key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, seen := g.logged[key]; seen {
		return false
	}
	if g.logged == nil || len(g.logged) >= guardLimit {
		g.logged = map[string]struct{}{}
	}
	g.logged[key] = struct{}{}
	return true
}

// Forget lets Once answer true for a key again, for a problem that was fixed and may come back.
func (g *Guard) Forget(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.logged, key)
}

// TryAgain says whether a report that failed for a reason that may pass should be sent again now: no more than
// MaxAttempts times, and not sooner than RetrySpacing after the last try. The first try of a report is always
// allowed. retryAfter is how long until it should be asked again, which is what a caller requeues for.
func (g *Guard) TryAgain(report string) (allowed bool, retryAfter time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	tried := g.attempts[report]
	if tried.count >= MaxAttempts {
		return false, 0
	}
	if tried.count > 0 && now.Sub(tried.last) < RetrySpacing {
		return false, RetrySpacing - now.Sub(tried.last)
	}
	if g.attempts == nil || len(g.attempts) >= guardLimit {
		g.attempts = map[string]attempt{}
	}
	g.attempts[report] = attempt{count: tried.count + 1, last: now}
	return true, RetrySpacing
}

// Delivered forgets the tries of a report, because it went through or is not going to be tried again.
func (g *Guard) Delivered(report string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.attempts, report)
}

// CommentIs reports whether the last body sent for a pull request comment is this one, so a pass that has
// nothing new to say asks GitHub nothing.
func (g *Guard) CommentIs(comment, fingerprint string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.comments[comment] == fingerprint
}

// RememberComment records what was last sent for a pull request comment.
func (g *Guard) RememberComment(comment, fingerprint string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.comments == nil || len(g.comments) >= guardLimit {
		g.comments = map[string]string{}
	}
	g.comments[comment] = fingerprint
}

// RememberDeployment records that a GitHub Deployment was made for a deploy, by the deploy's key. The id is also
// recorded on the service, but a pass that reads the service from a cache that has not caught up with that write
// would not find it and make a second deployment of the same deploy: this is what it asks first.
func (g *Guard) RememberDeployment(deploy string, id int64) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.deploys == nil || len(g.deploys) >= guardLimit {
		g.deploys = map[string]int64{}
	}
	g.deploys[deploy] = id
}

// Deployment is the id of the GitHub Deployment made for a deploy, when this process made one.
func (g *Guard) Deployment(deploy string) (int64, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	id, ok := g.deploys[deploy]
	return id, ok
}

// ForgetComment drops what was recorded for a pull request comment, for one that is finished.
func (g *Guard) ForgetComment(comment string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.comments, comment)
}
