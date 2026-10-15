package mcpserver

import (
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func finishedAt(minute int) map[string]any {
	completed := time.Date(2026, 10, 14, 12, minute, 0, 0, time.UTC)
	return map[string]any{"name": "web-build", "phase": "Succeeded", "sourceRevision": "abc1234", "startedAt": completed.Add(-time.Minute), "completedAt": completed}
}

func TestRebuildingWithWaitFollowsTheNewBuildAndReturnsTheOutcome(t *testing.T) {
	var reads atomic.Int32
	var mu sync.Mutex
	var calls []string
	api := (&stub{me: admin(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		mu.Lock()
		calls = append(calls, r.Method+" "+r.URL.Path)
		mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/apps/web/build":
			respond(w, 200, map[string]any{"ok": true})
		case r.URL.Path == "/api/v1/apps/web/status":
			switch reads.Add(1) {
			case 1, 2: // before the restart, and before it shows: both are the run that ended earlier
				respond(w, 200, map[string]any{"name": "web", "state": "ready", "ready": true, "build": finishedAt(0)})
			case 3:
				respond(w, 200, map[string]any{"name": "web", "state": "pending", "ready": false, "build": map[string]any{"name": "web-build", "phase": "Running"}})
			default:
				respond(w, 200, map[string]any{"name": "web", "state": "ready", "ready": true, "url": "https://web.example.com", "build": finishedAt(5)})
			}
		default:
			return false
		}
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api, PollInterval: time.Millisecond})

	text, isError := call(t, session, "rebuild_app", map[string]any{"app": "web", "wait": true, "timeout_seconds": 30})

	require.False(t, isError, text)
	var out struct {
		Ready  bool `json:"ready"`
		Status struct {
			URL   string `json:"url"`
			Build struct {
				CompletedAt time.Time `json:"completedAt"`
			} `json:"build"`
		} `json:"status"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &out), text)
	require.True(t, out.Ready)
	require.Equal(t, "https://web.example.com", out.Status.URL)
	require.Equal(t, 5, out.Status.Build.CompletedAt.Minute(), "the outcome is the new build, not the one that was there before the restart")
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, "GET /api/v1/apps/web/status", calls[0])
	require.Equal(t, "POST /api/v1/apps/web/build", calls[1])
}

func TestAFailedRebuildIsAnOutcomeThatSaysWhy(t *testing.T) {
	var reads atomic.Int32
	api := (&stub{me: admin(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		switch r.Method {
		case http.MethodPost:
			respond(w, 200, map[string]any{"ok": true})
		default:
			build := finishedAt(0)
			if reads.Add(1) > 1 {
				build = map[string]any{"name": "web-build", "phase": "Failed", "failureReason": "GitHub check \"tests\" concluded failure"}
			}
			respond(w, 200, map[string]any{"name": "web", "state": "pending", "ready": false, "build": build})
		}
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api, PollInterval: time.Millisecond})

	text, isError := call(t, session, "rebuild_app", map[string]any{"app": "web", "wait": true})

	require.False(t, isError, "the agent asked what happened, and this is the answer")
	require.Contains(t, text, `"failed": true`)
	require.Contains(t, text, "concluded failure", "the reason, which is what the agent needs to fix it")
}

func TestRebuildingWithoutWaitReturnsAtOnceAndSaysHowToCheck(t *testing.T) {
	var calls []string
	api := (&stub{me: admin(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		calls = append(calls, r.Method+" "+r.URL.Path)
		respond(w, 200, map[string]any{"ok": true})
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api, PollInterval: time.Millisecond})

	text, isError := call(t, session, "rebuild_app", map[string]any{"app": "web"})

	require.False(t, isError, text)
	require.Contains(t, text, `"building": true`)
	require.Contains(t, text, "app_status")
	require.Equal(t, []string{"POST /api/v1/apps/web/build"}, calls, "nothing is read when nothing is waited for")
}

func TestRebuildingAServiceThatIsNotBuiltFromARepositoryIsAnError(t *testing.T) {
	api := (&stub{me: admin(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		respond(w, 400, map[string]string{"error": "the app has no repository build", "code": "bad_request"})
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api, PollInterval: time.Millisecond})

	text, isError := call(t, session, "rebuild_app", map[string]any{"app": "cache"})

	require.True(t, isError)
	require.Contains(t, text, "no repository build")
}

func TestAProjectTokenAgentMayRebuildItsOwnServices(t *testing.T) {
	api := (&stub{me: projectToken()}).serve(t)
	_, session := connect(t, Options{Client: api})

	require.Contains(t, toolNames(t, session), "rebuild_app", "the build endpoint is open to project tokens, as deploy is")
}
