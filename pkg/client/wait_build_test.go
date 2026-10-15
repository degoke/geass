package client

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func finishedBuild(completed time.Time) *BuildStatus {
	started := completed.Add(-time.Minute)
	return &BuildStatus{Name: "web-build", Phase: "Succeeded", SourceRevision: "abc1234", StartedAt: &started, CompletedAt: &completed}
}

func buildBody(build *BuildStatus, ready bool) map[string]any {
	state := "ready"
	if !ready {
		state = "pending"
	}
	return map[string]any{"name": "web", "state": state, "ready": ready, "build": build}
}

func TestRestartingABuildPostsToTheBuildEndpoint(t *testing.T) {
	var method, path string
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.Path
		respond(w, 200, map[string]any{"ok": true})
	})

	require.NoError(t, c.RestartBuild(context.Background(), "web"))

	require.Equal(t, http.MethodPost, method)
	require.Equal(t, "/api/v1/apps/web/build", path)
}

func TestRestartingABuildReportsWhatTheAPIRefusesWith(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		respond(w, 400, map[string]string{"error": "the app has no repository build", "code": "bad_request"})
	})

	err := c.RestartBuild(context.Background(), "web")

	require.Error(t, err)
	require.Contains(t, err.Error(), "no repository build")
}

func TestTheBuildThatRanBeforeIsNotTakenForTheAnswer(t *testing.T) {
	before := finishedBuild(time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC))
	next := finishedBuild(time.Date(2026, 10, 14, 12, 5, 0, 0, time.UTC))
	var polls atomic.Int32
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch polls.Add(1) {
		case 1, 2:
			// The restart has not shown yet: the service is ready, built by the run that ended before.
			respond(w, 200, buildBody(before, true))
		case 3:
			respond(w, 200, buildBody(&BuildStatus{Name: "web-build", Phase: "Running"}, false))
		case 4:
			respond(w, 200, buildBody(next, false))
		default:
			respond(w, 200, buildBody(next, true))
		}
	})

	var progress []string
	status, err := c.WaitForApp(context.Background(), "web", WaitOptions{
		Interval: time.Millisecond, AfterBuild: before,
		OnStatus: func(s AppStatus) { progress = append(progress, s.State+"/"+s.Build.Phase) },
	})

	require.NoError(t, err)
	require.EqualValues(t, 5, polls.Load(), "a ready service with the old build is not the result of the build that was asked for")
	require.True(t, status.Ready)
	require.Equal(t, next.CompletedAt.UTC(), status.Build.CompletedAt.UTC(), "the answer is the new run")
	require.Equal(t, []string{"ready/Succeeded", "ready/Succeeded", "pending/Running", "pending/Succeeded", "ready/Succeeded"}, progress)
}

func TestAFailureThatIsOnlyTheOldBuildIsNotReported(t *testing.T) {
	before := &BuildStatus{Name: "web-build", Phase: "Failed", FailureReason: "the Dockerfile has a typo"}
	var polls atomic.Int32
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if polls.Add(1) < 3 {
			respond(w, 200, buildBody(before, false))
			return
		}
		respond(w, 200, buildBody(finishedBuild(time.Now()), true))
	})

	_, err := c.WaitForApp(context.Background(), "web", WaitOptions{Interval: time.Millisecond, AfterBuild: before})

	require.NoError(t, err, "the run that failed before is not this one: the service built, and is ready")
}

func TestARunThatEndsExactlyAsTheLastOneDidIsAcceptedOnceTheRestartHadTimeToShow(t *testing.T) {
	// A build that fails at once, the same way, looks the same as the one before. It cannot be told apart, so after
	// the grace period it is taken for what it is.
	before := &BuildStatus{Name: "web-build", Phase: "Failed", FailureReason: "no registry is set for builds"}
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, buildBody(before, false))
	})

	_, err := c.WaitForApp(context.Background(), "web", WaitOptions{Interval: time.Millisecond, AfterBuild: before, StaleFor: 20 * time.Millisecond, Timeout: 5 * time.Second})

	require.ErrorIs(t, err, ErrDeployFailed)
	require.Contains(t, err.Error(), "no registry")
}

func TestWithoutABuildToTellApartTheFirstReadyStatusIsTheAnswer(t *testing.T) {
	var polls atomic.Int32
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		polls.Add(1)
		respond(w, 200, buildBody(finishedBuild(time.Now()), true))
	})

	_, err := c.WaitForApp(context.Background(), "web", WaitOptions{Interval: time.Millisecond})

	require.NoError(t, err)
	require.EqualValues(t, 1, polls.Load(), "waiting for a deploy is unchanged")
}

func TestOnlyAFinishedBuildCanBeTheSameFinishedBuild(t *testing.T) {
	done := finishedBuild(time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC))
	other := finishedBuild(time.Date(2026, 10, 14, 12, 0, 1, 0, time.UTC))
	rebuilt := *done
	rebuilt.SourceRevision = "def5678"

	require.True(t, sameFinishedBuild(done, done))
	require.False(t, sameFinishedBuild(done, other), "ending at another time is another run")
	require.False(t, sameFinishedBuild(&rebuilt, done), "so is building another commit")
	require.False(t, sameFinishedBuild(&BuildStatus{Name: "web-build", Phase: "Running"}, &BuildStatus{Name: "web-build", Phase: "Running"}), "one that is going is not finished")
	require.False(t, sameFinishedBuild(nil, done))
	require.False(t, sameFinishedBuild(done, nil))
}
