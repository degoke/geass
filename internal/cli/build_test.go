package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// buildAPI is a Geass whose service web is built from a repository: it keeps what was asked of it, and answers the
// status of web from the steps a test gives it, one per read, the last repeating.
type buildAPI struct {
	mu       sync.Mutex
	requests []string
	statuses []map[string]any
	reads    int
	// onBuild answers the request to run the build again; nil answers that it has.
	onBuild func(w http.ResponseWriter, r *http.Request)
	// apps is what the list of services answers.
	apps []map[string]any
}

func (b *buildAPI) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b.mu.Lock()
		b.requests = append(b.requests, r.Method+" "+r.URL.Path)
		b.mu.Unlock()
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/build"):
			if b.onBuild != nil {
				b.onBuild(w, r)
				return
			}
			reply(w, 200, map[string]any{"ok": true})
		case strings.HasSuffix(r.URL.Path, "/status"):
			b.mu.Lock()
			step := b.statuses[min(b.reads, len(b.statuses)-1)]
			b.reads++
			b.mu.Unlock()
			reply(w, 200, step)
		case r.URL.Path == "/api/v1/apps":
			reply(w, 200, map[string]any{"items": b.apps})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}
}

func (b *buildAPI) seen() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.requests...)
}

func status(state string, ready bool, build map[string]any) map[string]any {
	step := map[string]any{"name": "web", "state": state, "ready": ready, "replicas": map[string]any{"desired": 1, "ready": 1, "available": 1}}
	if ready {
		step["url"] = "https://web.example.com"
	}
	if build != nil {
		step["build"] = build
	}
	return step
}

func finished(completed time.Time) map[string]any {
	return map[string]any{"name": "web-build", "phase": "Succeeded", "sourceRevision": "abc1234",
		"startedAt": completed.Add(-time.Minute), "completedAt": completed}
}

func TestBuildRunsTheBuildAndFollowsItThroughToTheRollout(t *testing.T) {
	before := finished(time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC))
	api := &buildAPI{statuses: []map[string]any{
		status("ready", true, before), // read before the build is asked for
		status("ready", true, before), // the restart has not shown yet: this is not the answer
		status("pending", false, map[string]any{"name": "web-build", "phase": "Running"}),
		status("pending", false, finished(time.Date(2026, 10, 14, 12, 5, 0, 0, time.UTC))),
		status("ready", true, finished(time.Date(2026, 10, 14, 12, 5, 0, 0, time.UTC))),
	}}
	h := newHarness(t, api.handler(t))
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("build", "web", "--wait")

	require.Equal(t, ExitOK, code, stderr)
	require.Contains(t, stderr, "web: build started")
	require.Contains(t, stderr, "web: build running", "progress is on the error stream, as it is for deploy")
	require.Contains(t, stderr, "web: ready at https://web.example.com")
	require.Regexp(t, `web\s+ready\s+1/1`, stdout)
	seen := api.seen()
	require.Equal(t, "GET /api/v1/apps/web/status", seen[0], "the build as it was is read first")
	require.Equal(t, "POST /api/v1/apps/web/build", seen[1])
	require.Greater(t, api.reads, 3, "the status that still showed the old build was not taken for the result")
}

func TestBuildWithoutWaitStartsTheBuildAndReturns(t *testing.T) {
	api := &buildAPI{}
	h := newHarness(t, api.handler(t))
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("build", "web")

	require.Equal(t, ExitOK, code, stderr)
	require.Empty(t, stdout, "a command that changes something prints on the error stream")
	require.Contains(t, stderr, "web: build started")
	require.Equal(t, []string{"POST /api/v1/apps/web/build"}, api.seen(), "no status is read when nothing is waited for")
}

func TestBuildAsJSONSaysWhichServicesAreBuilding(t *testing.T) {
	api := &buildAPI{}
	h := newHarness(t, api.handler(t))
	h.signedIn(t, "", "")

	code, stdout, _ := h.run("build", "web", "api", "--json")

	require.Equal(t, ExitOK, code)
	var out struct {
		Building []string `json:"building"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &out), stdout)
	require.Equal(t, []string{"web", "api"}, out.Building)
}

func TestBuildWaitAsJSONIsTheStatusesAsDeployPrintsThem(t *testing.T) {
	api := &buildAPI{statuses: []map[string]any{status("ready", true, nil)}}
	h := newHarness(t, api.handler(t))
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("build", "web", "--wait", "--json")

	require.Equal(t, ExitOK, code, stderr)
	var out struct {
		Items []struct {
			Name  string `json:"name"`
			Ready bool   `json:"ready"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &out), stdout)
	require.Len(t, out.Items, 1)
	require.True(t, out.Items[0].Ready)
	require.Contains(t, stderr, "web: build started", "progress stays out of what a program parses")
}

func TestABuildThatFailsExitsWithFiveAndSaysWhy(t *testing.T) {
	api := &buildAPI{statuses: []map[string]any{
		status("ready", true, finished(time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC))),
		status("pending", false, map[string]any{"name": "web-build", "phase": "Running"}),
		status("pending", false, map[string]any{"name": "web-build", "phase": "Failed", "failureReason": "kaniko Job failed"}),
	}}
	h := newHarness(t, api.handler(t))
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("build", "web", "--wait")

	require.Equal(t, ExitDeployFailed, code)
	require.Contains(t, stderr, "kaniko Job failed")
	require.Contains(t, stdout, "web", "the status is printed even when the build failed")
}

func TestABuildThatNeverFinishesExitsWithSix(t *testing.T) {
	api := &buildAPI{statuses: []map[string]any{
		status("pending", false, map[string]any{"name": "web-build", "phase": "Running"}),
	}}
	h := newHarness(t, api.handler(t))
	h.signedIn(t, "", "")

	code, _, stderr := h.run("build", "web", "--wait", "--timeout", "30ms")

	require.Equal(t, ExitTimeout, code, stderr)
	require.Contains(t, stderr, "gave up waiting")
}

func TestBuildWithoutNamesBuildsEveryServiceBuiltFromARepository(t *testing.T) {
	api := &buildAPI{apps: []map[string]any{
		{"name": "web", "state": "ready", "details": map[string]any{"source": "git"}},
		{"name": "api", "state": "pending", "details": map[string]any{"source": "git"}},
		{"name": "draft", "state": "draft", "details": map[string]any{"source": "git"}},
		{"name": "going", "state": "deleting", "details": map[string]any{"source": "git"}},
		{"name": "cache", "state": "ready", "details": map[string]any{"source": "image"}},
		{"name": "fn", "state": "ready", "details": map[string]any{"source": "function"}},
	}}
	h := newHarness(t, api.handler(t))
	h.signedIn(t, "shop", "production")

	code, _, stderr := h.run("build")

	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, []string{"GET /api/v1/apps", "POST /api/v1/apps/web/build", "POST /api/v1/apps/api/build"}, api.seen(),
		"a draft has no build, an image or a function has none to run, and a service being deleted is left alone")
}

func TestBuildWithNoRepositoryServicesSaysSoAndSucceeds(t *testing.T) {
	api := &buildAPI{apps: []map[string]any{{"name": "cache", "state": "ready", "details": map[string]any{"source": "image"}}}}
	h := newHarness(t, api.handler(t))
	h.signedIn(t, "shop", "production")

	code, stdout, stderr := h.run("build", "--json")

	require.Equal(t, ExitOK, code, stderr)
	require.Contains(t, stderr, "No services built from a repository")
	require.JSONEq(t, `{"building":[]}`, stdout)
}

func TestBuildWithNothingToGoOnIsAUsageError(t *testing.T) {
	h := newHarness(t, func(http.ResponseWriter, *http.Request) { t.Error("nothing should be asked of the API") })
	h.signedIn(t, "", "")

	code, _, stderr := h.run("build")

	require.Equal(t, ExitUsage, code)
	require.Contains(t, stderr, "--project")
}

func TestBuildOfAServiceThatIsNotBuiltFromARepositoryIsAnErrorThatNamesIt(t *testing.T) {
	api := &buildAPI{onBuild: func(w http.ResponseWriter, r *http.Request) {
		reply(w, 400, map[string]string{"error": "the app has no repository build", "code": "bad_request"})
	}}
	h := newHarness(t, api.handler(t))
	h.signedIn(t, "", "")

	code, _, stderr := h.run("build", "cache")

	require.Equal(t, ExitError, code)
	require.Contains(t, stderr, "cache: the app has no repository build")
}

func TestBuildOfAServiceThatDoesNotExistIsNotFound(t *testing.T) {
	api := &buildAPI{onBuild: func(w http.ResponseWriter, r *http.Request) {
		reply(w, 404, map[string]string{"error": "not found", "code": "not_found"})
	}}
	h := newHarness(t, api.handler(t))
	h.signedIn(t, "", "")

	code, _, _ := h.run("build", "ghost")

	require.Equal(t, ExitNotFound, code)
}

func TestATokenThatMayNotBuildIsDenied(t *testing.T) {
	api := &buildAPI{onBuild: func(w http.ResponseWriter, r *http.Request) {
		reply(w, 403, map[string]string{"error": "viewer role cannot access this", "code": "forbidden"})
	}}
	h := newHarness(t, api.handler(t))
	h.signedIn(t, "", "")

	code, _, stderr := h.run("build", "web")

	require.Equal(t, ExitDenied, code)
	require.Contains(t, stderr, "your token may not do this")
}

func TestBuildIsAlsoUnderApps(t *testing.T) {
	api := &buildAPI{}
	h := newHarness(t, api.handler(t))
	h.signedIn(t, "", "")

	code, _, stderr := h.run("apps", "build", "web")

	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, []string{"POST /api/v1/apps/web/build"}, api.seen())
}
