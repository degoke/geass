package githubapp

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// clock is a time that moves when the test says so.
type clock struct{ now time.Time }

func (c *clock) Now() time.Time          { return c.now }
func (c *clock) advance(d time.Duration) { c.now = c.now.Add(d) }

func newTestGuard() (*Guard, *clock) {
	c := &clock{now: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)}
	return &Guard{Now: c.Now}, c
}

func TestAReportGitHubRefusedIsHeldBackUntilTheBackoffPasses(t *testing.T) {
	guard, clock := newTestGuard()
	require.False(t, guard.Blocked("shop-github", CapabilityStatuses))

	guard.Block("shop-github", CapabilityStatuses, 0)

	require.True(t, guard.Blocked("shop-github", CapabilityStatuses))
	require.False(t, guard.Blocked("shop-github", CapabilityDeployments), "each kind of report has its own permission")
	require.False(t, guard.Blocked("blog-github", CapabilityStatuses), "and each connection is its own app")
	clock.advance(RefusedBackoff - time.Second)
	require.True(t, guard.Blocked("shop-github", CapabilityStatuses))
	clock.advance(2 * time.Second)
	require.False(t, guard.Blocked("shop-github", CapabilityStatuses), "after the backoff the next report finds out whether the permission was approved")
}

func TestUnblockingLetsReportsThroughAgain(t *testing.T) {
	guard, _ := newTestGuard()
	guard.Block("shop-github", CapabilityComments, time.Hour)
	require.True(t, guard.anyBlocked("shop-github"))

	guard.Unblock("shop-github", CapabilityComments)

	require.False(t, guard.Blocked("shop-github", CapabilityComments))
	require.False(t, guard.anyBlocked("shop-github"))
}

func TestAProblemIsLoggedOnceUntilItIsForgotten(t *testing.T) {
	guard := NewGuard()
	require.True(t, guard.Once("shop-github|refused|statuses"))
	require.False(t, guard.Once("shop-github|refused|statuses"))
	require.True(t, guard.Once("shop-github|refused|deployments"), "another reason is another problem")

	guard.Forget("shop-github|refused|statuses")

	require.True(t, guard.Once("shop-github|refused|statuses"))
}

func TestAFailingReportIsTriedAFewTimesWithAGapAndThenDropped(t *testing.T) {
	guard, clock := newTestGuard()

	allowed, _ := guard.TryAgain("build/a")
	require.True(t, allowed, "the first try is always allowed")

	allowed, wait := guard.TryAgain("build/a")
	require.False(t, allowed, "not again straight away")
	require.Greater(t, wait, time.Duration(0))
	require.LessOrEqual(t, wait, RetrySpacing)

	clock.advance(RetrySpacing)
	allowed, _ = guard.TryAgain("build/a")
	require.True(t, allowed)
	clock.advance(RetrySpacing)
	allowed, _ = guard.TryAgain("build/a")
	require.True(t, allowed, "the third and last try")

	clock.advance(time.Hour)
	allowed, wait = guard.TryAgain("build/a")
	require.False(t, allowed)
	require.Zero(t, wait, "no wait means it is dropped, not delayed")

	allowed, _ = guard.TryAgain("build/b")
	require.True(t, allowed, "another report is counted on its own")
}

func TestADeliveredReportStartsFromNothingNextTime(t *testing.T) {
	guard, clock := newTestGuard()
	for range MaxAttempts {
		_, _ = guard.TryAgain("build/a")
		clock.advance(RetrySpacing)
	}
	guard.Delivered("build/a")

	allowed, _ := guard.TryAgain("build/a")

	require.True(t, allowed)
}

func TestTheTablesAreBounded(t *testing.T) {
	guard := NewGuard()
	for i := range guardLimit + 10 {
		guard.Once(string(rune('a'+i%26)) + time.Duration(i).String())
		guard.Block(time.Duration(i).String(), CapabilityStatuses, time.Hour)
		guard.RememberComment(time.Duration(i).String(), "x")
	}
	guard.mu.Lock()
	defer guard.mu.Unlock()
	require.LessOrEqual(t, len(guard.logged), guardLimit)
	require.LessOrEqual(t, len(guard.blocked), guardLimit)
	require.LessOrEqual(t, len(guard.comments), guardLimit)
}

func TestACommentIsOnlySentWhenItChanged(t *testing.T) {
	guard := NewGuard()
	require.False(t, guard.CommentIs("acme/web#12|shop", "v1"))

	guard.RememberComment("acme/web#12|shop", "v1")
	require.True(t, guard.CommentIs("acme/web#12|shop", "v1"))
	require.False(t, guard.CommentIs("acme/web#12|shop", "v2"))

	guard.ForgetComment("acme/web#12|shop")
	require.False(t, guard.CommentIs("acme/web#12|shop", "v1"), "a pull request that is opened again gets its comment written again")
}

func TestPermissionForMapsEachReportToTheAppPermissionItNeeds(t *testing.T) {
	require.Equal(t, "statuses", PermissionFor(CapabilityStatuses).Key)
	require.Equal(t, "deployments", PermissionFor(CapabilityDeployments).Key)
	require.Equal(t, "pull_requests", PermissionFor(CapabilityComments).Key)
	require.Equal(t, "write", PermissionFor(CapabilityComments).Level)
	require.Empty(t, PermissionFor("nonsense").Key)
}
