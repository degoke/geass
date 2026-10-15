package dashboard

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A report to GitHub that was started in the background (the removal of a pull request environment) is given a moment to
// finish when the dashboard shuts down, and shutting down never waits for one that hangs.
func TestShuttingDownWaitsForBackgroundReportsButNotForEver(t *testing.T) {
	srv := &Server{}
	release := make(chan struct{})
	finished := make(chan struct{})
	srv.reports.Go(func() {
		<-release
		close(finished)
	})

	started := time.Now()
	srv.drainReports(150 * time.Millisecond)
	require.GreaterOrEqual(t, time.Since(started), 100*time.Millisecond, "it waited")
	require.Less(t, time.Since(started), 2*time.Second, "but only as long as it was told to")
	select {
	case <-finished:
		t.Fatal("the report was still held")
	default:
	}

	close(release)
	started = time.Now()
	srv.drainReports(5 * time.Second)
	require.Less(t, time.Since(started), time.Second, "once the report ends there is nothing to wait for")
	<-finished
}

func TestShuttingDownWithNoReportsDoesNotWait(t *testing.T) {
	started := time.Now()

	(&Server{}).drainReports(5 * time.Second)

	require.Less(t, time.Since(started), time.Second)
}
