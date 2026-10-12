package mcpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/degoke/geass/pkg/client"
)

// An agent's view of a Geass: a stub API that answers who the token is, and whatever else a test gives it.
type stub struct {
	me      map[string]any
	handler func(w http.ResponseWriter, r *http.Request) bool
	calls   atomic.Int32
}

func (s *stub) serve(t *testing.T) *client.Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.calls.Add(1)
		if r.URL.Path == "/api/v1/me" {
			respond(w, 200, s.me)
			return
		}
		if s.handler != nil && s.handler(w, r) {
			return
		}
		respond(w, 404, map[string]string{"error": "not found", "code": "not_found"})
	}))
	t.Cleanup(server.Close)
	return client.New(server.URL, "gsw_00000000_"+strings.Repeat("a", 40))
}

func respond(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func admin() map[string]any {
	return map[string]any{"username": "token:ci", "role": "admin", "canMutate": true, "canReadSensitive": true, "authentication": "token", "scope": "workspace"}
}

func observer() map[string]any {
	return map[string]any{"username": "token:ci", "role": "observer", "canMutate": false, "canReadSensitive": true, "authentication": "token", "scope": "workspace"}
}

func viewer() map[string]any {
	return map[string]any{"username": "token:ci", "role": "viewer", "canMutate": false, "canReadSensitive": false, "authentication": "token", "scope": "workspace"}
}

func projectToken() map[string]any {
	return map[string]any{"username": "token:deploy", "role": "admin", "canMutate": true, "canReadSensitive": true, "authentication": "token", "scope": "project", "project": "shop", "environment": "production"}
}

// connect runs the server in memory and returns an agent's session with it.
func connect(t *testing.T, opts Options) (*Server, *mcp.ClientSession) {
	t.Helper()
	ctx := context.Background()
	server, err := New(ctx, opts)
	require.NoError(t, err)
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.MCP().Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })
	agent := mcp.NewClient(&mcp.Implementation{Name: "test-agent", Version: "1"}, nil)
	session, err := agent.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return server, session
}

// call runs a tool and returns what the agent would read, and whether it was an error.
func call(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) (text string, isError bool) {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	var b strings.Builder
	for _, content := range result.Content {
		if part, ok := content.(*mcp.TextContent); ok {
			b.WriteString(part.Text)
		}
	}
	return b.String(), result.IsError
}

func toolNames(t *testing.T, session *mcp.ClientSession) []string {
	t.Helper()
	listed, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

func TestAnAdminAgentSeesEveryToolExceptDeletion(t *testing.T) {
	api := (&stub{me: admin()}).serve(t)
	_, session := connect(t, Options{Client: api, PollInterval: time.Millisecond})

	names := toolNames(t, session)

	for _, want := range []string{"whoami", "list_apps", "app_status", "get_logs", "deploy_app", "scale_app", "rollback_app", "set_variable", "create_app", "database_connection", "list_nodes"} {
		require.Contains(t, names, want)
	}
	require.NotContains(t, names, "delete_resource", "deleting is a choice, not a default")
}

func TestDeletionIsOnlyOfferedWhenAllowed(t *testing.T) {
	api := (&stub{me: admin()}).serve(t)

	_, session := connect(t, Options{Client: api, AllowDelete: true})

	require.Contains(t, toolNames(t, session), "delete_resource")
}

func TestAnObserverIsNotEvenShownATooThatChangesThings(t *testing.T) {
	api := (&stub{me: observer()}).serve(t)
	_, session := connect(t, Options{Client: api, AllowDelete: true})

	names := toolNames(t, session)

	require.Contains(t, names, "get_logs", "an observer reads logs")
	require.Contains(t, names, "app_status")
	for _, forbidden := range []string{"deploy_app", "scale_app", "rollback_app", "set_variable", "unset_variable", "create_app", "delete_resource", "database_connection", "set_function_trigger", "remove_function_trigger", "invoke_function", "set_sleep_when_idle"} {
		require.NotContains(t, names, forbidden, "the token cannot do it, so the agent never sees it")
	}
}

func TestAViewerCannotReadLogs(t *testing.T) {
	api := (&stub{me: viewer()}).serve(t)
	_, session := connect(t, Options{Client: api, PollInterval: time.Millisecond})

	names := toolNames(t, session)

	require.Contains(t, names, "list_apps")
	for _, hidden := range []string{"get_logs", "list_variables", "deployment_logs", "deploy_app"} {
		require.NotContains(t, names, hidden)
	}
}

func TestAProjectTokenAgentSeesOnlyTheToolsForItsEnvironment(t *testing.T) {
	api := (&stub{me: projectToken()}).serve(t)
	_, session := connect(t, Options{Client: api, AllowDelete: true})

	names := toolNames(t, session)

	for _, want := range []string{"list_apps", "app_status", "deploy_app", "get_logs", "set_variable", "scale_app"} {
		require.Contains(t, names, want)
	}
	for _, hidden := range []string{"list_projects", "list_nodes", "list_databases", "list_buckets", "create_app", "delete_resource", "database_connection", "project_compliance"} {
		require.NotContains(t, names, hidden, "outside a project token's environment")
	}
}

func TestReadOnlyModeHidesWritesEvenFromAnAdmin(t *testing.T) {
	api := (&stub{me: admin()}).serve(t)
	_, session := connect(t, Options{Client: api, ReadOnly: true, AllowDelete: true})

	names := toolNames(t, session)

	require.Contains(t, names, "get_logs")
	for _, hidden := range []string{"deploy_app", "scale_app", "set_variable", "create_app", "delete_resource", "database_connection", "set_function_trigger", "invoke_function"} {
		require.NotContains(t, names, hidden)
	}
}

func TestToolsSayWhetherTheyAreReadOnlyOrDestructive(t *testing.T) {
	api := (&stub{me: admin()}).serve(t)
	_, session := connect(t, Options{Client: api, AllowDelete: true})

	listed, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	byName := map[string]*mcp.Tool{}
	for _, tool := range listed.Tools {
		byName[tool.Name] = tool
	}

	require.True(t, byName["app_status"].Annotations.ReadOnlyHint)
	require.False(t, byName["deploy_app"].Annotations.ReadOnlyHint)
	require.True(t, *byName["delete_resource"].Annotations.DestructiveHint, "an agent host can ask before running it")
	require.False(t, *byName["deploy_app"].Annotations.DestructiveHint)
	require.NotNil(t, byName["deploy_app"].InputSchema, "the agent is told what to send")
	require.NotEmpty(t, byName["get_logs"].Description)
}

func TestAServerCannotBeMadeWithAnUnusableToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respond(w, 401, map[string]string{"error": "invalid or expired API token", "code": "unauthenticated"})
	}))
	t.Cleanup(server.Close)

	_, err := New(context.Background(), Options{Client: client.New(server.URL, "gsw_00000000_"+strings.Repeat("0", 40))})

	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid or expired")
}

func TestListingAppsReturnsJSONTheAgentCanRead(t *testing.T) {
	var query string
	api := (&stub{me: admin(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/v1/apps" {
			return false
		}
		query = r.URL.RawQuery
		respond(w, 200, map[string]any{"items": []map[string]any{{"kind": "app", "name": "web", "state": "ready", "project": "shop"}}})
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api, PollInterval: time.Millisecond})

	text, isError := call(t, session, "list_apps", map[string]any{"project": "shop", "environment": "production"})

	require.False(t, isError)
	require.Equal(t, "environment=production&project=shop", query)
	var out struct {
		Items []client.Resource `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &out), text)
	require.Equal(t, "web", out.Items[0].Name)
}

func TestAnErrorFromGeassIsAToolErrorTheAgentCanReadAndActOn(t *testing.T) {
	api := (&stub{me: admin(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		respond(w, 404, map[string]string{"error": "app not found", "code": "not_found"})
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api, PollInterval: time.Millisecond})

	text, isError := call(t, session, "app_status", map[string]any{"app": "ghost"})

	require.True(t, isError, "reported inside the result, where the agent sees it, not as a protocol failure")
	require.Contains(t, text, "app not found")
	require.Contains(t, text, "not visible to this token")
}

func TestARefusalTellsTheAgentNotToRetry(t *testing.T) {
	api := (&stub{me: admin(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		respond(w, 403, map[string]string{"error": "viewer role cannot access this", "code": "forbidden"})
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api, PollInterval: time.Millisecond})

	text, isError := call(t, session, "scale_app", map[string]any{"app": "web", "replicas": 2})

	require.True(t, isError)
	require.Contains(t, text, "do not retry")
}

func TestLogsAreReturnedAsPlainLines(t *testing.T) {
	var logsQuery string
	api := (&stub{me: observer(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		switch r.URL.Path {
		case "/api/v1/apps/web/status":
			respond(w, 200, map[string]any{"name": "web", "project": "shop", "environment": "production"})
		case "/api/v1/projects/shop/logs":
			logsQuery = r.URL.RawQuery
			respond(w, 200, map[string]any{"logs": []map[string]any{{"t": "2026-01-01T10:00:00Z", "pod": "web-1", "line": "listening on 8080"}}})
		default:
			return false
		}
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api, PollInterval: time.Millisecond})

	text, isError := call(t, session, "get_logs", map[string]any{"app": "web", "level": "error", "tail": 9999})

	require.False(t, isError)
	require.Equal(t, "2026-01-01T10:00:00Z web-1 listening on 8080\n", text)
	require.Contains(t, logsQuery, "tail=500", "an agent cannot ask for an unbounded amount")
	require.Contains(t, logsQuery, "level=error")

	empty, _ := call(t, session, "get_logs", map[string]any{"app": "ghost"})
	require.NotEmpty(t, empty)
}

func TestDeployingWithWaitReturnsTheOutcome(t *testing.T) {
	var polls atomic.Int32
	api := (&stub{me: admin(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/apps/web/deploy":
			respond(w, 200, map[string]any{"ok": true, "deployed": true})
		case r.URL.Path == "/api/v1/apps/web/status":
			if polls.Add(1) < 2 {
				respond(w, 200, map[string]any{"name": "web", "state": "pending", "ready": false})
				return true
			}
			respond(w, 200, map[string]any{"name": "web", "state": "ready", "ready": true, "url": "https://web.example.com"})
		default:
			return false
		}
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api, PollInterval: time.Millisecond})

	text, isError := call(t, session, "deploy_app", map[string]any{"app": "web", "wait": true, "timeout_seconds": 30})

	require.False(t, isError, text)
	var out struct {
		Ready  bool `json:"ready"`
		Status struct {
			URL string `json:"url"`
		} `json:"status"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &out), text)
	require.True(t, out.Ready)
	require.Equal(t, "https://web.example.com", out.Status.URL)
}

func TestAFailedDeployIsAnOutcomeThatSaysWhy(t *testing.T) {
	api := (&stub{me: admin(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodPost {
			respond(w, 200, map[string]any{"ok": true})
			return true
		}
		respond(w, 200, map[string]any{"name": "web", "state": "pending", "ready": false,
			"build": map[string]any{"phase": "Failed", "failureReason": "Kaniko completed without an immutable image digest"}})
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api, PollInterval: time.Millisecond})

	text, isError := call(t, session, "deploy_app", map[string]any{"app": "web", "wait": true})

	require.False(t, isError, "the agent asked what happened, and this is the answer")
	require.Contains(t, text, `"failed": true`)
	require.Contains(t, text, "immutable image digest", "the reason, which is what the agent needs to fix it")
}

func TestDeployingWithoutWaitReturnsAtOnceAndSaysHowToCheck(t *testing.T) {
	api := (&stub{me: admin(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		respond(w, 200, map[string]any{"ok": true, "deployed": true})
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api, PollInterval: time.Millisecond})

	text, isError := call(t, session, "deploy_app", map[string]any{"app": "web"})

	require.False(t, isError)
	require.Contains(t, text, "app_status")
}

func TestSettingAVariableIsPendingUntilDeployed(t *testing.T) {
	var calls []string
	api := (&stub{me: admin(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		calls = append(calls, r.Method+" "+r.URL.Path)
		respond(w, 200, map[string]any{"ok": true})
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api, PollInterval: time.Millisecond})

	text, isError := call(t, session, "set_variable", map[string]any{"app": "web", "key": "LOG_LEVEL", "value": "debug"})

	require.False(t, isError)
	require.Equal(t, []string{"POST /api/v1/apps/web/config/set"}, calls, "set, and not deployed")
	require.Contains(t, text, `"pending": true`)
	require.NotContains(t, text, "debug", "the value is not echoed back")
}

func TestSleepWhenIdleIsASettingNotAScale(t *testing.T) {
	var method, path string
	var body map[string]any
	api := (&stub{me: admin(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		method, path = r.Method, r.URL.Path
		body = nil
		_ = json.NewDecoder(r.Body).Decode(&body)
		respond(w, 200, map[string]any{"ok": true})
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api, PollInterval: time.Millisecond})

	_, isError := call(t, session, "set_sleep_when_idle", map[string]any{"app": "web", "enabled": true, "idle_seconds": 600})
	require.False(t, isError)
	require.Equal(t, "PATCH /api/v1/apps/web", method+" "+path)
	require.Equal(t, map[string]any{"scaleToZero": "on", "idleSeconds": float64(600)}, body)

	_, isError = call(t, session, "set_sleep_when_idle", map[string]any{"app": "web", "enabled": false})
	require.False(t, isError)
	require.Equal(t, map[string]any{"scaleToZero": "off"}, body, "switching it off is sent as off, which a JSON false would not carry")

	_, isError = call(t, session, "set_sleep_when_idle", map[string]any{"app": "web", "enabled": true, "idle_seconds": 5})
	require.True(t, isError)
}

func TestAgentsCanScheduleAndCallAFunction(t *testing.T) {
	var patched map[string]any
	var called string
	var api *client.Client
	api = (&stub{me: admin(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/apps/hello":
			respond(w, 200, map[string]any{
				"resource": map[string]any{"name": "hello", "url": api.BaseURL + "/fn"},
				"spec":     map[string]any{"source": map[string]any{"function": map[string]any{"runtime": "node", "mode": "event", "triggers": []any{}}}},
			})
		case r.Method == http.MethodPatch:
			patched = nil
			_ = json.NewDecoder(r.Body).Decode(&patched)
			respond(w, 200, map[string]any{"ok": true})
		case strings.HasPrefix(r.URL.Path, "/fn"):
			called = r.Method + " " + r.URL.Path
			respond(w, 200, map[string]any{"hello": "world"})
		default:
			return false
		}
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api, PollInterval: time.Millisecond})

	text, isError := call(t, session, "set_function_trigger", map[string]any{"app": "hello", "name": "nightly", "schedule": "0 3 * * *"})
	require.False(t, isError, text)
	require.Contains(t, text, `"pending": true`, "a schedule starts with the next deploy, and the agent is told")
	require.Equal(t, []any{map[string]any{"name": "nightly", "schedule": "0 3 * * *"}}, patched["functionTriggers"])

	_, isError = call(t, session, "set_function_trigger", map[string]any{"app": "hello", "name": "nightly"})
	require.True(t, isError, "a schedule needs a schedule")

	text, isError = call(t, session, "invoke_function", map[string]any{"app": "hello", "path": "/run"})
	require.False(t, isError, text)
	require.Equal(t, "GET /fn/run", called)
	require.Contains(t, text, "world")
}

func TestASecretIsStoredAsASecretAndNeverEchoed(t *testing.T) {
	var path string
	api := (&stub{me: admin(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		path = r.URL.Path
		respond(w, 200, map[string]any{"ok": true})
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api, PollInterval: time.Millisecond})

	text, _ := call(t, session, "set_variable", map[string]any{"app": "web", "key": "STRIPE_KEY", "value": "sk_live_secret", "secret": true})

	require.Equal(t, "/api/v1/apps/web/secrets/set", path)
	require.NotContains(t, text, "sk_live_secret")
}

func TestDatabaseConnectionNeverAsksForThePassword(t *testing.T) {
	var query string
	api := (&stub{me: admin(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		query = r.URL.RawQuery
		out := map[string]any{"host": "db.svc", "port": "5432", "database": "orders", "username": "app", "keys": []string{"password", "uri"}}
		if r.URL.Query().Get("reveal") == "true" {
			out["password"] = "hunter2"
		}
		respond(w, 200, out)
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api, PollInterval: time.Millisecond})

	text, isError := call(t, session, "database_connection", map[string]any{"name": "orders"})

	require.False(t, isError)
	require.Empty(t, query, "reveal is never requested")
	require.Contains(t, text, "db.svc")
	require.NotContains(t, text, "hunter2")
	require.NotContains(t, text, "keys")
}

func TestDeletingNeedsTheNameRepeated(t *testing.T) {
	var deletes atomic.Int32
	api := (&stub{me: admin(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodDelete {
			deletes.Add(1)
		}
		respond(w, 200, map[string]any{"ok": true})
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api, AllowDelete: true})

	text, isError := call(t, session, "delete_resource", map[string]any{"kind": "databases", "name": "orders", "confirm": "orderz"})
	require.True(t, isError)
	require.Contains(t, text, "not deleted")
	require.Zero(t, deletes.Load())

	_, isError = call(t, session, "delete_resource", map[string]any{"kind": "pods", "name": "x", "confirm": "x"})
	require.True(t, isError, "only the kinds geass manages")
	require.Zero(t, deletes.Load())

	_, isError = call(t, session, "delete_resource", map[string]any{"kind": "databases", "name": "orders", "confirm": "orders"})
	require.False(t, isError)
	require.EqualValues(t, 1, deletes.Load())
}

func TestAHugeResultIsCutShortWithAnExplanation(t *testing.T) {
	huge := strings.Repeat("x", maxResultSize+1000)

	result := jsonResult(huge)

	text := result.Content[0].(*mcp.TextContent).Text
	require.LessOrEqual(t, len(text), maxResultSize+200)
	require.Contains(t, text, "too long")
}

func TestWaitTimeoutIsBounded(t *testing.T) {
	require.EqualValues(t, defaultWait, waitTimeout(0).Seconds())
	require.EqualValues(t, 45, waitTimeout(45).Seconds())
	require.EqualValues(t, maxWait, waitTimeout(100000).Seconds())
}

func TestInstructionsTellTheAgentWhatItMayDo(t *testing.T) {
	require.Contains(t, instructions(client.Identity{CanMutate: false}, Options{}), "only read")
	require.Contains(t, instructions(client.Identity{CanMutate: true}, Options{}), "deploy_app")
	require.Contains(t, instructions(client.Identity{CanMutate: true, Scope: "project", Project: "shop", Environment: "production"}, Options{}), "shop/production")
	require.Contains(t, instructions(client.Identity{CanMutate: true}, Options{ReadOnly: true}), "only read")
}
