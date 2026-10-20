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

	"github.com/degoke/geass/internal/testshapes"
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

	for _, want := range []string{"whoami", "list_apps", "app_status", "get_logs", "deploy_app", "rebuild_app", "scale_app", "rollback_app", "set_variable", "create_app", "database_connection", "list_nodes"} {
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
	for _, forbidden := range []string{"deploy_app", "rebuild_app", "scale_app", "rollback_app", "set_variable", "unset_variable", "create_app", "delete_resource", "database_connection", "set_function_trigger", "remove_function_trigger", "invoke_function", "set_sleep_when_idle"} {
		require.NotContains(t, names, forbidden, "the token cannot do it, so the agent never sees it")
	}
}

func TestAViewerCannotReadLogs(t *testing.T) {
	api := (&stub{me: viewer()}).serve(t)
	_, session := connect(t, Options{Client: api, PollInterval: time.Millisecond})

	names := toolNames(t, session)

	require.Contains(t, names, "list_apps")
	for _, hidden := range []string{"get_logs", "list_variables", "deployment_logs", "deploy_app", "rebuild_app"} {
		require.NotContains(t, names, hidden)
	}
}

func TestAProjectTokenAgentSeesOnlyTheToolsForItsEnvironment(t *testing.T) {
	api := (&stub{me: projectToken()}).serve(t)
	_, session := connect(t, Options{Client: api, AllowDelete: true})

	names := toolNames(t, session)

	for _, want := range []string{"list_apps", "app_status", "deploy_app", "rebuild_app", "get_logs", "set_variable", "scale_app"} {
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
	for _, hidden := range []string{"deploy_app", "rebuild_app", "scale_app", "set_variable", "create_app", "delete_resource", "set_function_trigger", "invoke_function"} {
		require.NotContains(t, names, hidden)
	}
	require.Contains(t, names, "database_connection", "it changes nothing, and read-only mode hides what changes things")
}

func TestOnlyAnAdminSeesDatabaseConnectionEvenWhenItIsReadOnly(t *testing.T) {
	for name, tt := range map[string]struct {
		me       map[string]any
		readOnly bool
		want     bool
	}{
		"an admin":                   {admin(), false, true},
		"an admin in read-only mode": {admin(), true, true},
		"an observer":                {observer(), false, false},
		"a viewer":                   {viewer(), false, false},
		"a project token":            {projectToken(), false, false},
		"a project token read-only":  {projectToken(), true, false},
	} {
		api := (&stub{me: tt.me}).serve(t)
		_, session := connect(t, Options{Client: api, ReadOnly: tt.readOnly})

		require.Equal(t, tt.want, slices.Contains(toolNames(t, session), "database_connection"), name)
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
	require.False(t, byName["rebuild_app"].Annotations.ReadOnlyHint)
	require.False(t, *byName["rebuild_app"].Annotations.DestructiveHint, "a build again replaces nothing that cannot be built again")
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
		case "/api/v1/apps/web/logs":
			logsQuery = r.URL.RawQuery
			respond(w, 200, testshapes.LogsAnswer("web",
				map[string]any{"t": "2026-01-01T10:00:00Z", "service": "web", "pod": "web-1", "container": "app", "line": "listening on 8080", "level": "info"},
				map[string]any{"t": "2026-01-01T10:00:01Z", "service": "web", "pod": "web-2", "container": "app", "line": "Error: boom", "level": "error"},
			))
		default:
			return false
		}
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api, PollInterval: time.Millisecond})

	text, isError := call(t, session, "get_logs", map[string]any{"app": "web", "level": "error", "tail": 9999})

	require.False(t, isError, text)
	require.Equal(t, "2026-01-01T10:00:00Z web-1 listening on 8080\n2026-01-01T10:00:01Z web-2 Error: boom\n", text)
	require.Contains(t, logsQuery, "tail=500", "an agent cannot ask for an unbounded amount")
	require.Contains(t, logsQuery, "level=error")
	require.NotContains(t, logsQuery, "service=", "the route is the service's own: it needs no project or environment")

	empty, _ := call(t, session, "get_logs", map[string]any{"app": "ghost"})
	require.NotEmpty(t, empty)
}

func TestNoLogLinesSaysSo(t *testing.T) {
	api := (&stub{me: observer(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		respond(w, 200, testshapes.LogsAnswer("web"))
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api})

	text, isError := call(t, session, "get_logs", map[string]any{"app": "web"})

	require.False(t, isError)
	require.Equal(t, "No log lines matched.", text)
}

func TestAProjectTokenCanReadLogs(t *testing.T) {
	api := (&stub{me: projectToken(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != "/api/v1/apps/web/logs" {
			return false
		}
		respond(w, 200, testshapes.LogsAnswer("web", map[string]any{"t": "2026-01-01T10:00:00Z", "pod": "web-1", "line": "hello"}))
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api})

	text, isError := call(t, session, "get_logs", map[string]any{"app": "web"})

	require.False(t, isError, "the tool is offered to a project token, so it has to work for one: %s", text)
	require.Contains(t, text, "hello")
}

func TestDeploymentLogsAreReadAsTheDashboardSendsThem(t *testing.T) {
	var path string
	api := (&stub{me: observer(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		path = r.URL.Path
		// Each section's lines are one block of text. The tool once read them as a list and failed whenever a
		// section had any.
		_, _ = w.Write([]byte(`{"id":"web-7x2kq","sections":[` +
			`{"id":"provision","title":"Provision","lines":"Provisioning failed\nimage pull failed"},` +
			`{"id":"build","title":"Build","lines":"Kaniko completed without an immutable image digest"}]}`))
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api})

	text, isError := call(t, session, "deployment_logs", map[string]any{"app": "web", "deployment": "web-7x2kq"})

	require.False(t, isError, text)
	require.Equal(t, "/api/v1/apps/web/deployments/web-7x2kq/logs", path)
	var out struct {
		ID       string `json:"id"`
		Sections []struct {
			ID    string `json:"id"`
			Lines string `json:"lines"`
		} `json:"sections"`
	}
	require.NoError(t, json.Unmarshal([]byte(text), &out), text)
	require.Equal(t, "web-7x2kq", out.ID)
	require.Len(t, out.Sections, 2)
	require.Equal(t, "Provisioning failed\nimage pull failed", out.Sections[0].Lines)
	require.Contains(t, out.Sections[1].Lines, "immutable image digest")
}

func TestLongDeploymentLogsKeepTheirEndWhereAFailureIs(t *testing.T) {
	var build strings.Builder
	for i := 1; i <= 1000; i++ {
		build.WriteString("step " + strings.Repeat("x", 10) + " " + string(rune('a'+i%26)) + "\n")
	}
	build.WriteString("ERROR: the build failed here")
	api := (&stub{me: observer(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		respond(w, 200, map[string]any{"id": "d", "sections": []map[string]any{{"id": "build", "title": "Build", "lines": build.String()}}})
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api})

	text, isError := call(t, session, "deployment_logs", map[string]any{"app": "web", "deployment": "d"})

	require.False(t, isError)
	require.Contains(t, text, "ERROR: the build failed here", "a result that is cut would otherwise lose it")
	require.Contains(t, text, "earlier lines left out")
	require.Less(t, len(text), maxResultSize)
}

func TestLastLinesKeepsTheEndAndSaysWhatItLeftOut(t *testing.T) {
	require.Equal(t, "a\nb", lastLines("a\nb", 5), "short text is as it was")
	require.Equal(t, "a\nb\n", lastLines("a\nb\n", 2), "and so is text that fits exactly")
	require.Equal(t, "... (2 earlier lines left out)\nc\nd", lastLines("a\nb\nc\nd", 2))
}

func TestMetricsAreForAnHourWhenTheAgentSaysNothingElse(t *testing.T) {
	var queries []string
	api := (&stub{me: admin(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		queries = append(queries, r.URL.Path+"?"+r.URL.RawQuery)
		respond(w, 200, map[string]any{"window": "1h", "step": 60, "available": true, "series": map[string]any{}})
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api})

	_, isError := call(t, session, "app_metrics", map[string]any{"name": "web"})
	require.False(t, isError)
	_, isError = call(t, session, "app_metrics", map[string]any{"name": "web", "window": "6h"})
	require.False(t, isError)
	_, isError = call(t, session, "app_metrics", map[string]any{"name": "shop", "kind": "projects"})
	require.False(t, isError)

	require.Equal(t, []string{
		"/api/v1/apps/web/metrics?window=1h", "/api/v1/apps/web/metrics?window=6h", "/api/v1/projects/shop/metrics?window=1h",
	}, queries, "the tool says 1h is the default, and the API's own default is 15m")
}

func TestTheMetricsToolSaysWhatItDoes(t *testing.T) {
	api := (&stub{me: admin()}).serve(t)
	_, session := connect(t, Options{Client: api})
	listed, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)

	for _, tool := range listed.Tools {
		if tool.Name != "app_metrics" {
			continue
		}
		schema, err := json.Marshal(tool.InputSchema)
		require.NoError(t, err)
		require.Contains(t, string(schema), "1h (default)", "and the tool now does")
	}
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
		if r.Method == http.MethodGet {
			respond(w, 200, testshapes.SleepStatus("web", true, "Sleeps when idle"))
			return true
		}
		method, path = r.Method, r.URL.Path
		body = nil
		_ = json.NewDecoder(r.Body).Decode(&body)
		respond(w, 200, map[string]any{"ok": true})
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api, PollInterval: time.Millisecond})

	text, isError := call(t, session, "set_sleep_when_idle", map[string]any{"app": "web", "enabled": true, "idle_seconds": 600})
	require.False(t, isError)
	require.Contains(t, text, `"active": true`)
	require.NotContains(t, text, "cannot sleep")
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
	require.Contains(t, instructions(client.Identity{CanMutate: true}, Options{}), "rebuild_app")
	require.Contains(t, instructions(client.Identity{CanMutate: true, Scope: "project", Project: "shop", Environment: "production"}, Options{}), "shop/production")
	require.Contains(t, instructions(client.Identity{CanMutate: true}, Options{ReadOnly: true}), "only read")
}

func TestAServiceThatCannotSleepIsReportedAsSuch(t *testing.T) {
	var patched bool
	api := (&stub{me: admin(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodGet {
			respond(w, 200, testshapes.SleepStatus("web", false, "The serverless add-on is not installed"))
			return true
		}
		patched = true
		respond(w, 200, map[string]any{"ok": true})
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api, PollInterval: time.Millisecond, SleepCheckTimeout: 30 * time.Millisecond})

	text, isError := call(t, session, "set_sleep_when_idle", map[string]any{"app": "web", "enabled": true})

	require.False(t, isError, "the setting was saved, so this is an outcome to read")
	require.True(t, patched)
	require.Contains(t, text, `"active": false`)
	require.Contains(t, text, "The serverless add-on is not installed", "and why, which is what the agent has to act on")
	require.Contains(t, text, "stays running")
}

func TestAnUnconfirmedSleepIsNotReportedAsActive(t *testing.T) {
	api := (&stub{me: admin(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		respond(w, 200, map[string]any{"ok": true})
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api, PollInterval: time.Millisecond, SleepCheckTimeout: 30 * time.Millisecond})

	text, isError := call(t, session, "set_sleep_when_idle", map[string]any{"app": "web", "enabled": true})

	require.False(t, isError)
	require.Contains(t, text, `"active": false`)
	require.Contains(t, text, "scaleToZero", "it says where to look")
}

func TestKeepingAServiceRunningNeedsNoCheck(t *testing.T) {
	var gets atomic.Int32
	api := (&stub{me: admin(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodGet {
			gets.Add(1)
		}
		respond(w, 200, map[string]any{"ok": true})
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api})

	text, isError := call(t, session, "set_sleep_when_idle", map[string]any{"app": "web", "enabled": false})

	require.False(t, isError)
	require.Contains(t, text, `"sleepWhenIdle": false`)
	require.Zero(t, gets.Load())
}

func TestCreatingAnAppWithDeployAsksTheAPIToDeployIt(t *testing.T) {
	var body map[string]any
	api := (&stub{me: admin(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		_ = json.NewDecoder(r.Body).Decode(&body)
		respond(w, 200, map[string]any{"ok": true, "name": "web", "kind": "apps", "deployed": true})
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api})

	text, isError := call(t, session, "create_app", map[string]any{"project": "shop", "environment": "production", "name": "web", "image": "nginx:alpine", "deploy": true})

	require.False(t, isError, text)
	require.Equal(t, true, body["deploy"])
	require.NotContains(t, body, "draft", "the API takes a draft and a deploy as different asks")
	require.Contains(t, text, `"draft": false`, "which is now true")
	require.Contains(t, text, `"deploying": true`)
	require.Contains(t, text, "app_status")
}

func TestCreatingAnAppWithoutDeployAsksForADraft(t *testing.T) {
	var body map[string]any
	api := (&stub{me: admin(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		_ = json.NewDecoder(r.Body).Decode(&body)
		respond(w, 200, map[string]any{"ok": true})
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api})

	text, isError := call(t, session, "create_app", map[string]any{"project": "shop", "environment": "production", "name": "web", "repository": "acme/web"})

	require.False(t, isError, text)
	require.Equal(t, true, body["draft"])
	require.NotContains(t, body, "deploy")
	require.Contains(t, text, `"draft": true`)
	require.NotContains(t, text, "deploying")
}

func TestVariableToolsSayPlainConfigurationIsNotHidden(t *testing.T) {
	api := (&stub{me: admin()}).serve(t)
	_, session := connect(t, Options{Client: api})
	listed, err := session.ListTools(context.Background(), nil)
	require.NoError(t, err)
	descriptions := map[string]string{}
	for _, tool := range listed.Tools {
		descriptions[tool.Name] = tool.Description
	}

	require.NotContains(t, descriptions["list_variables"], "Values are never returned")
	require.Contains(t, descriptions["list_variables"], "Secret values are never returned")
	require.Contains(t, descriptions["list_variables"], "plain configuration")
	require.NotContains(t, descriptions["get_app"], "Variable values are never included")
	require.Contains(t, descriptions["get_app"], "Secret values are never included")

	text := instructions(client.Identity{CanMutate: true}, Options{})
	require.Contains(t, text, "Secret values are never returned by any tool")
	require.Contains(t, text, "Plain configuration is not secret")
}

// The documentation gives how many tools each kind of token sees, so the numbers are pinned here: a tool added
// or a rule changed has to change them, and the docs with them.
func TestHowManyToolsEachKindOfTokenSees(t *testing.T) {
	for name, tt := range map[string]struct {
		me      map[string]any
		options Options
		count   int
	}{
		"a viewer":                          {me: viewer(), count: 18},
		"an observer":                       {me: observer(), count: 21},
		"a project token":                   {me: projectToken(), count: 19},
		"an admin":                          {me: admin(), count: 38},
		"an admin who allows deleting":      {me: admin(), options: Options{AllowDelete: true}, count: 40},
		"an admin in read-only mode":        {me: admin(), options: Options{ReadOnly: true}, count: 23},
		"a project token in read-only mode": {me: projectToken(), options: Options{ReadOnly: true}, count: 9},
	} {
		tt.options.Client = (&stub{me: tt.me}).serve(t)
		server, err := New(context.Background(), tt.options)
		require.NoError(t, err)

		require.Len(t, server.Tools(), tt.count, name)
	}
}

func TestAnAdminInReadOnlyModeSeesWhatAnObserverSeesAndWhatOnlyAnAdminMayReadButChangesNothing(t *testing.T) {
	observed, err := New(context.Background(), Options{Client: (&stub{me: observer()}).serve(t)})
	require.NoError(t, err)
	readOnly, err := New(context.Background(), Options{Client: (&stub{me: admin()}).serve(t), ReadOnly: true})
	require.NoError(t, err)

	want := append(observed.Tools(), "database_connection", "cloud_sizes")
	slices.Sort(want)
	got := readOnly.Tools()
	slices.Sort(got)

	require.Equal(t, want, got)
}

func TestASleepSettingThatIsSavedIsNotAnErrorWhenTheCheckFails(t *testing.T) {
	var patched bool
	api := (&stub{me: admin(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == http.MethodGet {
			respond(w, 500, map[string]string{"error": "could not load the service", "code": "internal_error"})
			return true
		}
		patched = true
		respond(w, 200, map[string]any{"ok": true})
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api, PollInterval: time.Millisecond, SleepCheckTimeout: 30 * time.Millisecond})

	text, isError := call(t, session, "set_sleep_when_idle", map[string]any{"app": "web", "enabled": true})

	require.True(t, patched)
	require.False(t, isError, "the setting is saved: an error would read as if it were not")
	require.Contains(t, text, `"sleepWhenIdle": true`)
	require.Contains(t, text, "saved, but whether the service can sleep when idle could not be checked")
	require.Contains(t, text, "could not load the service", "and why the check failed")
}

func TestCreateAppWithDeployOnAGeassThatSavesADraftSaysSo(t *testing.T) {
	api := (&stub{me: admin(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		// A Geass older than this server ignores deploy, saves a draft and has no `deployed` to report.
		respond(w, 200, map[string]any{"ok": true, "name": "web", "kind": "apps"})
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api})

	text, isError := call(t, session, "create_app", map[string]any{"project": "shop", "environment": "production", "name": "web", "image": "nginx:alpine", "deploy": true})

	require.False(t, isError, text)
	require.Contains(t, text, `"draft": true`, "it is a draft, whatever was asked")
	require.NotContains(t, text, "deploying")
	require.Contains(t, text, "did not deploy it")
	require.Contains(t, text, "deploy_app")
}

// ---- project limits ----

func TestEveryTokenWithAWorkspaceSeesTheLimitsButOnlyAnAdminCanChangeQuota(t *testing.T) {
	for name, tt := range map[string]struct {
		me        map[string]any
		wantWrite bool
	}{
		"admin":    {admin(), true},
		"observer": {observer(), false},
		"viewer":   {viewer(), false},
	} {
		t.Run(name, func(t *testing.T) {
			_, session := connect(t, Options{Client: (&stub{me: tt.me}).serve(t)})
			names := toolNames(t, session)
			require.Contains(t, names, "project_quota")
			require.Contains(t, names, "project_network")
			require.Equal(t, tt.wantWrite, slices.Contains(names, "set_project_quota"))
			require.NotContains(t, names, "set_project_network", "opening a project to the cluster, or letting it reach the machines, is for a person to choose")
		})
	}
}

func TestAProjectTokenAndReadOnlyModeNeverSeeTheQuotaTool(t *testing.T) {
	_, session := connect(t, Options{Client: (&stub{me: projectToken()}).serve(t)})
	for _, hidden := range []string{"project_quota", "project_network", "set_project_quota"} {
		require.NotContains(t, toolNames(t, session), hidden, "outside a project token's environment")
	}

	_, session = connect(t, Options{Client: (&stub{me: admin()}).serve(t), ReadOnly: true})
	names := toolNames(t, session)
	require.Contains(t, names, "project_quota")
	require.NotContains(t, names, "set_project_quota")
}

func TestTheQuotaToolSendsOnlyTheLimitsTheAgentNamed(t *testing.T) {
	var posted map[string]any
	api := (&stub{me: admin(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		switch {
		case r.URL.Path == "/api/v1/projects/shop/quota" && r.Method == http.MethodPost:
			_ = json.NewDecoder(r.Body).Decode(&posted)
			respond(w, 200, map[string]any{"ok": true})
		case r.URL.Path == "/api/v1/projects/shop/quota":
			respond(w, 200, map[string]any{"project": map[string]any{"cpu": "8"}, "used": map[string]any{"cpu": "2"}})
		default:
			return false
		}
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api})

	text, isError := call(t, session, "set_project_quota", map[string]any{"project": "shop", "project_cpu": "8", "memory": ""})

	require.False(t, isError, text)
	require.Equal(t, map[string]any{"projectCpu": "8", "memory": ""}, posted, "a limit that was left out is not sent, and an empty one is, which removes it")
	require.Contains(t, text, `"cpu": "8"`, "the agent is shown the limits as they are now")

	text, isError = call(t, session, "set_project_quota", map[string]any{"project": "shop"})
	require.True(t, isError)
	require.Contains(t, text, "name at least one limit")

	text, isError = call(t, session, "project_quota", map[string]any{"project": "shop"})
	require.False(t, isError, text)
	require.Contains(t, text, `"used"`)
}

// Agents send a count as a number, and a CPU of 4 as one too. A field that accepted only strings would refuse them
// before the tool ran, with a type error.
func TestTheQuotaToolTakesNumbersAsWellAsStrings(t *testing.T) {
	var posted map[string]any
	api := (&stub{me: admin(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		switch {
		case r.URL.Path == "/api/v1/projects/shop/quota" && r.Method == http.MethodPost:
			_ = json.NewDecoder(r.Body).Decode(&posted)
			respond(w, 200, map[string]any{"ok": true})
		case r.URL.Path == "/api/v1/projects/shop/quota":
			respond(w, 200, map[string]any{})
		default:
			return false
		}
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api})

	text, isError := call(t, session, "set_project_quota", map[string]any{"project": "shop", "pods": 20, "project_cpu": 4, "memory": "8Gi", "project_pods": 0.5e2})

	require.False(t, isError, text)
	require.Equal(t, map[string]any{"pods": "20", "projectCpu": "4", "memory": "8Gi", "projectPods": "50"}, posted)
}

func TestTheNetworkToolReadsAndNothingElse(t *testing.T) {
	var methods []string
	api := (&stub{me: admin(), handler: func(w http.ResponseWriter, r *http.Request) bool {
		methods = append(methods, r.Method)
		respond(w, 200, map[string]any{"isolation": "Project", "allowFromProjects": []string{}, "blockNodeAddresses": true})
		return true
	}}).serve(t)
	_, session := connect(t, Options{Client: api})

	text, isError := call(t, session, "project_network", map[string]any{"project": "shop"})

	require.False(t, isError, text)
	require.Contains(t, text, `"blockNodeAddresses": true`)
	require.NotContains(t, methods, http.MethodPost)
}
