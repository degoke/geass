package controller

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func nextWake(t *testing.T, d *reportDispatcher, within time.Duration) (types.NamespacedName, bool) {
	t.Helper()
	select {
	case event := <-d.channel():
		return types.NamespacedName{Name: event.Object.GetName(), Namespace: event.Object.GetNamespace()}, true
	case <-time.After(within):
		return types.NamespacedName{}, false
	}
}

func TestAReportThatRunsOnItsOwnLeavesTheReconcileAtOnce(t *testing.T) {
	var dispatcher reportDispatcher
	var retries reportRetries
	release, done := make(chan struct{}), make(chan struct{})
	key := types.NamespacedName{Name: "web", Namespace: "geass-system"}

	started := time.Now()
	dispatcher.dispatch(context.Background(), true, key, &retries, func(context.Context) {
		<-release
		close(done)
	})

	require.Less(t, time.Since(started), time.Second, "the caller did not wait for the report")
	select {
	case <-done:
		t.Fatal("the report should still be waiting")
	default:
	}
	close(release)
	<-done
}

func TestWithoutAsyncAReportRunsInsideTheReconcile(t *testing.T) {
	var dispatcher reportDispatcher
	var retries reportRetries
	key := types.NamespacedName{Name: "web", Namespace: "geass-system"}
	ran := false

	dispatcher.dispatch(context.Background(), false, key, &retries, func(context.Context) {
		ran = true
		retries.want(key, time.Minute)
	})

	require.True(t, ran, "it has run by the time dispatch returns")
	after, wanted := retries.take(key)
	require.True(t, wanted, "and its retry is left for the reconcile to apply, as it always was")
	require.Equal(t, time.Minute, after)
}

func TestOnlyOneReportPassRunsForAnObjectAndTheObjectIsReconciledAgainAfterIt(t *testing.T) {
	var dispatcher reportDispatcher
	var retries reportRetries
	key := types.NamespacedName{Name: "web", Namespace: "geass-system"}
	release := make(chan struct{})
	var running, runs atomic.Int32
	run := func(context.Context) {
		runs.Add(1)
		require.Equal(t, int32(1), running.Add(1), "two passes for one object at once")
		<-release
		running.Add(-1)
	}

	dispatcher.dispatch(context.Background(), true, key, &retries, run)
	require.Eventually(t, func() bool { return runs.Load() == 1 }, time.Second, 5*time.Millisecond)
	dispatcher.dispatch(context.Background(), true, key, &retries, run) // arrives while the first is running
	dispatcher.dispatch(context.Background(), true, key, &retries, run)
	close(release)

	woken, ok := nextWake(t, &dispatcher, 2*time.Second)
	require.True(t, ok, "the object is reconciled again, to say what is true by now")
	require.Equal(t, key, woken)
	require.Equal(t, int32(1), runs.Load(), "the passes that arrived meanwhile were folded into one reconcile, not run on a stale copy")
}

func TestAReportThatWantsATryLaterWakesTheObjectAfterThePause(t *testing.T) {
	var dispatcher reportDispatcher
	var retries reportRetries
	key := types.NamespacedName{Name: "web", Namespace: "geass-system"}

	dispatcher.dispatch(context.Background(), true, key, &retries, func(context.Context) {
		retries.want(key, 60*time.Millisecond)
	})

	woken, ok := nextWake(t, &dispatcher, 2*time.Second)
	require.True(t, ok)
	require.Equal(t, key, woken)

	quiet := types.NamespacedName{Name: "quiet", Namespace: "geass-system"}
	dispatcher.dispatch(context.Background(), true, quiet, &retries, func(context.Context) {})
	_, woke := nextWake(t, &dispatcher, 200*time.Millisecond)
	require.False(t, woke, "a report with nothing to try again does not wake anything")
}

// The wiring, not only the dispatcher: with GitHub hanging, the reconcile of a build returns, and the report still
// goes out when GitHub answers.
func TestABuildReconcileDoesNotWaitForGitHub(t *testing.T) {
	w := newGitHubWorld(t, worldOptions{objects: []client.Object{reportBuildNamed("web-build-1")}})
	release := make(chan struct{})
	w.gh.mu.Lock()
	w.gh.answers["POST /repos/acme/web/statuses/"+reportSHA] = func(map[string]any) (int, any) {
		<-release
		return http.StatusCreated, map[string]any{}
	}
	w.gh.mu.Unlock()
	r := w.buildReconciler(t)
	r.AsyncReports = true

	started := time.Now()
	reconcileReportBuild(t, r, "web-build-1")

	require.Less(t, time.Since(started), 2*time.Second, "the reconcile came back while GitHub was still holding the request")
	require.Eventually(t, func() bool { return len(statusesTo(w.gh)) == 1 }, 3*time.Second, 10*time.Millisecond, "the report was started")
	var stored geassv1alpha1.GeassBuild
	require.NoError(t, w.client.Get(context.Background(), client.ObjectKey{Name: "web-build-1", Namespace: platform.SystemNamespace}, &stored))
	require.Empty(t, stored.Annotations[platform.BuildGitHubStatusAnnotation], "and it has not been recorded as sent, because it has not been")

	close(release)
	require.Eventually(t, func() bool {
		var latest geassv1alpha1.GeassBuild
		if err := w.client.Get(context.Background(), client.ObjectKey{Name: "web-build-1", Namespace: platform.SystemNamespace}, &latest); err != nil {
			return false
		}
		return latest.Annotations[platform.BuildGitHubStatusAnnotation] == reportSHA+":Running"
	}, 3*time.Second, 10*time.Millisecond, "once GitHub answered, the state is recorded")
}
