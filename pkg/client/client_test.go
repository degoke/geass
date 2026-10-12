package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// serve starts a server that answers with handler and returns a client for it.
func serve(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return New(server.URL, "gsw_00000000_"+strings.Repeat("a", 40))
}

func respond(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func TestRequestsCarryTheTokenAndAJSONBody(t *testing.T) {
	var gotAuth, gotType, gotPath, gotMethod string
	var gotBody map[string]any
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotType, gotPath, gotMethod = r.Header.Get("Authorization"), r.Header.Get("Content-Type"), r.URL.Path, r.Method
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		respond(w, 200, map[string]any{"ok": true})
	})

	require.NoError(t, c.Scale(context.Background(), "api prod", 3))

	require.Equal(t, "Bearer "+c.Token, gotAuth)
	require.Equal(t, "application/json", gotType)
	require.Equal(t, "/api/v1/apps/api prod/scale", gotPath, "a name is escaped in the path")
	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, map[string]any{"replicas": float64(3)}, gotBody)
}

func TestErrorsKeepTheirStatusCodeAndMessage(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		respond(w, 404, map[string]string{"error": "app not found", "code": "not_found"})
	})

	_, err := c.AppStatus(context.Background(), "ghost")

	require.Error(t, err)
	require.Equal(t, "app not found", err.Error())
	require.True(t, IsNotFound(err))
	require.False(t, IsForbidden(err))
	require.Equal(t, "not_found", ErrorCode(err))
	require.Empty(t, ErrorCode(errors.New("something else")))
}

func TestAnErrorThatIsNotJSONIsStillAnError(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad gateway", http.StatusBadGateway)
	})

	err := c.Do(context.Background(), http.MethodGet, "/me", nil, nil, nil)

	var apiErr *Error
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, http.StatusBadGateway, apiErr.Status)
	require.Equal(t, "bad gateway", apiErr.Message)
}

func TestListsAreFiltered(t *testing.T) {
	var gotQuery string
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		respond(w, 200, map[string]any{"items": []map[string]any{{"kind": "app", "name": "web", "state": "ready"}}})
	})

	items, err := c.List(context.Background(), "apps", Filter{Project: "shop", Environment: "production"})

	require.NoError(t, err)
	require.Equal(t, "environment=production&project=shop", gotQuery)
	require.Len(t, items, 1)
	require.Equal(t, "ready", items[0].State)
}

func TestIdentityAndVersion(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/me":
			respond(w, 200, map[string]any{"username": "token:ci", "role": "observer", "canReadSensitive": true})
		default:
			respond(w, 200, map[string]string{"api": "v1"})
		}
	})

	me, err := c.Me(context.Background())
	require.NoError(t, err)
	require.Equal(t, "observer", me.Role)
	require.True(t, me.CanReadSensitive)
	version, err := c.Version(context.Background())
	require.NoError(t, err)
	require.Equal(t, "v1", version)
}

func TestDeleteConfirmsByName(t *testing.T) {
	var gotQuery, gotMethod string
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery, gotMethod = r.URL.RawQuery, r.Method
		respond(w, 200, map[string]any{"ok": true})
	})

	require.NoError(t, c.Delete(context.Background(), "apps", "web"))

	require.Equal(t, http.MethodDelete, gotMethod)
	require.Equal(t, "confirmName=web", gotQuery)
}

func TestVariablesAreSetAsConfigOrSecret(t *testing.T) {
	var paths []string
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		respond(w, 200, map[string]any{"ok": true})
	})

	require.NoError(t, c.SetVariable(context.Background(), "web", "LOG_LEVEL", "debug", false))
	require.NoError(t, c.SetVariable(context.Background(), "web", "API_KEY", "x", true))
	require.NoError(t, c.UnsetVariable(context.Background(), "web", "LOG_LEVEL", false))
	require.NoError(t, c.UnsetVariable(context.Background(), "web", "API_KEY", true))

	require.Equal(t, []string{
		"/api/v1/apps/web/config/set", "/api/v1/apps/web/secrets/set", "/api/v1/apps/web/config/delete", "/api/v1/apps/web/secrets/delete",
	}, paths)
}

func TestLogsAreReadFromTheProjectNarrowedToTheService(t *testing.T) {
	var logsQuery string
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/apps/web/status":
			respond(w, 200, map[string]any{"name": "web", "project": "shop", "environment": "production"})
		case "/api/v1/projects/shop/logs":
			logsQuery = r.URL.RawQuery
			respond(w, 200, map[string]any{"logs": []map[string]any{{"t": "2026-01-01T00:00:00Z", "service": "web", "line": "listening", "level": "info"}}})
		default:
			t.Errorf("unexpected request %s", r.URL)
		}
	})

	lines, err := c.Logs(context.Background(), "web", LogOptions{Tail: 50, Level: "error", Search: "timeout", Since: "15m"})

	require.NoError(t, err)
	require.Equal(t, "environment=production&level=error&q=timeout&service=web&since=15m&tail=50", logsQuery)
	require.Len(t, lines, 1)
	require.Equal(t, "listening", lines[0].Text)
}

func TestFollowingLogsReadsServerSentEvents(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "text/event-stream", r.Header.Get("Accept"))
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = io.WriteString(w, ": keep-alive\n\n")
		_, _ = io.WriteString(w, "data: {\"t\":\"2026-01-01T00:00:00Z\",\"service\":\"web\",\"line\":\"first\"}\n\n")
		flusher.Flush()
		_, _ = io.WriteString(w, "data: not json at all\n\n")
		_, _ = io.WriteString(w, "data: {\"t\":\"2026-01-01T00:00:01Z\",\"service\":\"web\",\"line\":\"second\"}\n\n")
		flusher.Flush()
	})

	var seen []string
	err := c.FollowLogs(context.Background(), "web", LogOptions{}, func(line LogLine) error {
		seen = append(seen, line.Text)
		return nil
	})

	require.NoError(t, err)
	require.Equal(t, []string{"first", "second"}, seen, "comments and lines that are not log lines are skipped")
}

func TestAStreamStopsWhenTheHandlerSaysSo(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for i := range 10 {
			_, _ = fmt.Fprintf(w, "data: {\"line\":\"%d\"}\n\n", i)
		}
	})
	enough := errors.New("enough")

	count := 0
	err := c.FollowLogs(context.Background(), "web", LogOptions{}, func(LogLine) error {
		count++
		if count == 3 {
			return enough
		}
		return nil
	})

	require.ErrorIs(t, err, enough)
	require.Equal(t, 3, count)
}

func TestAStreamErrorBeforeItStartsIsAnAPIError(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		respond(w, 403, map[string]string{"error": "viewer role cannot access this", "code": "forbidden"})
	})

	err := c.FollowLogs(context.Background(), "web", LogOptions{}, func(LogLine) error { return nil })

	require.True(t, IsForbidden(err))
}

func TestWaitingForAServiceReturnsOnceItIsReady(t *testing.T) {
	var polls atomic.Int32
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		state := map[string]any{"name": "web", "state": "pending", "ready": false}
		if polls.Add(1) >= 3 {
			state = map[string]any{"name": "web", "state": "ready", "ready": true, "url": "https://web.example.com"}
		}
		respond(w, 200, state)
	})

	var progress []string
	status, err := c.WaitForApp(context.Background(), "web", WaitOptions{Interval: time.Millisecond, OnStatus: func(s AppStatus) { progress = append(progress, s.State) }})

	require.NoError(t, err)
	require.True(t, status.Ready)
	require.Equal(t, "https://web.example.com", status.URL)
	require.Equal(t, []string{"pending", "pending", "ready"}, progress)
}

func TestWaitingForAServiceThatFailsReturnsTheReason(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, map[string]any{
			"name": "web", "state": "pending", "ready": false,
			"build": map[string]any{"name": "b", "phase": "Failed", "failureReason": "Kaniko completed without an immutable image digest"},
		})
	})

	status, err := c.WaitForApp(context.Background(), "web", WaitOptions{Interval: time.Millisecond})

	require.ErrorIs(t, err, ErrDeployFailed)
	require.Contains(t, err.Error(), "immutable image digest", "the reason is in the error, where a person or agent reads it")
	require.Equal(t, "Failed", status.Build.Phase)
}

func TestWaitingForAServiceThatIsMarkedFailed(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, map[string]any{"name": "web", "state": "failed", "message": "Provisioning failed after repeated errors", "ready": false})
	})

	_, err := c.WaitForApp(context.Background(), "web", WaitOptions{Interval: time.Millisecond})

	require.ErrorIs(t, err, ErrDeployFailed)
	require.Contains(t, err.Error(), "Provisioning failed")
}

func TestWaitingGivesUpAtTheTimeout(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, map[string]any{"name": "web", "state": "pending", "ready": false})
	})

	status, err := c.WaitForApp(context.Background(), "web", WaitOptions{Interval: time.Millisecond, Timeout: 30 * time.Millisecond})

	require.Error(t, err)
	require.NotErrorIs(t, err, ErrDeployFailed)
	require.Contains(t, err.Error(), "gave up waiting")
	require.Equal(t, "pending", status.State, "and says how far it got")
}

func TestAServiceWithPendingChangesIsNotYetDone(t *testing.T) {
	var polls atomic.Int32
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		pending := 1
		if polls.Add(1) > 1 {
			pending = 0
		}
		respond(w, 200, map[string]any{"name": "web", "state": "ready", "ready": true, "pendingChanges": pending})
	})

	_, err := c.WaitForApp(context.Background(), "web", WaitOptions{Interval: time.Millisecond})

	require.NoError(t, err)
	require.EqualValues(t, 2, polls.Load(), "ready with a change still waiting to be deployed is not done")
}

func TestDeviceLoginPollsUntilApproved(t *testing.T) {
	var polls atomic.Int32
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/device":
			respond(w, 200, map[string]any{"deviceCode": "dc", "userCode": "BCDF-GHJK", "verificationUri": "http://x/device", "interval": 1, "expiresIn": 600})
		case "/api/v1/auth/token":
			if polls.Add(1) < 3 {
				respond(w, 400, map[string]string{"error": "waiting", "code": "authorization_pending"})
				return
			}
			respond(w, 200, map[string]any{"token": "gsw_x", "role": "observer", "approvedBy": "admin"})
		}
	})
	c.Token = ""

	login, err := c.StartDeviceLogin(context.Background(), "geass CLI")
	require.NoError(t, err)
	require.Equal(t, "BCDF-GHJK", login.UserCode)

	var token DeviceToken
	for {
		token, err = c.PollDeviceLogin(context.Background(), login.DeviceCode)
		if ErrorCode(err) == "authorization_pending" {
			continue
		}
		break
	}
	require.NoError(t, err)
	require.Equal(t, "gsw_x", token.Token)
	require.EqualValues(t, 3, polls.Load())
}

func TestNodesAndCompliance(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/nodes":
			respond(w, 200, map[string]any{"items": []map[string]any{{"name": "h1", "ready": true, "region": "eu", "tags": []map[string]any{{"name": "hipaa", "reserved": true, "compliance": true}}}}})
		case strings.HasSuffix(r.URL.Path, "/compliance") && r.Method == http.MethodGet:
			respond(w, 200, map[string]any{"requiredTags": []string{"hipaa"}, "databases": []map[string]any{{"name": "orders", "compliant": false, "missing": []string{"hipaa"}}}})
		default:
			respond(w, 200, map[string]any{"ok": true})
		}
	})

	nodes, err := c.Nodes(context.Background())
	require.NoError(t, err)
	require.True(t, nodes[0].Tags[0].Compliance)

	report, err := c.Compliance(context.Background(), "clinic")
	require.NoError(t, err)
	require.False(t, report.Databases[0].Compliant)
	require.Equal(t, []string{"hipaa"}, report.Databases[0].Missing)

	require.NoError(t, c.SetRequiredTags(context.Background(), "clinic", []string{"hipaa", "pci"}))
	require.NoError(t, c.SetNodePlacement(context.Background(), "h1", "eu", "", []string{"hipaa"}, nil, []string{"hipaa"}))
}

// The answer is what a real dashboard sent when a service was deployed: pending is a list of kinds of change,
// which an earlier version of the client expected to be a number.
func TestDeployingReadsWhatTheDashboardReallyAnswers(t *testing.T) {
	c := serve(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"button":"","deployed":true,"detail":"","ok":true,"pending":["variables","functions"],"title":""}`))
	})
	result, err := c.Deploy(context.Background(), "web")
	require.NoError(t, err)
	require.True(t, result.Deployed)
	require.Equal(t, []string{"variables", "functions"}, result.Pending)
}
