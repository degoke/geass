package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/degoke/geass/internal/testshapes"
	"github.com/degoke/geass/pkg/client"
)

// harness is the command line pointed at a stub API, with its output captured and its configuration in a
// temporary directory, so no test touches the real world.
type harness struct {
	app    *App
	out    *bytes.Buffer
	errOut *bytes.Buffer
	server *httptest.Server
	config string
}

func newHarness(t *testing.T, handler http.HandlerFunc) *harness {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	h := &harness{out: &bytes.Buffer{}, errOut: &bytes.Buffer{}, server: server, config: filepath.Join(t.TempDir(), "geass", "config.json")}
	h.app = &App{
		In: strings.NewReader(""), Out: h.out, Err: h.errOut,
		Getenv:      func(string) string { return "" },
		ConfigPath:  h.config,
		NewClient:   client.New,
		Interactive: func() bool { return false },
		Sleep:       func(time.Duration) {},
		Version:     "test",

		PollInterval: time.Millisecond,
	}
	return h
}

// signedIn saves a login for the stub server.
func (h *harness) signedIn(t *testing.T, project, environment string) {
	t.Helper()
	config := Config{Current: "stub", Contexts: map[string]Context{"stub": {URL: h.server.URL, Token: "gsw_00000000_" + strings.Repeat("a", 40), Project: project, Environment: environment}}}
	require.NoError(t, config.Save(h.config))
}

func (h *harness) run(args ...string) (code int, stdout, stderr string) {
	h.out.Reset()
	h.errOut.Reset()
	h.flagsReset()
	code = h.app.Run(context.Background(), args)
	return code, h.out.String(), h.errOut.String()
}

// flagsReset clears the flags a previous run set: the harness reuses one App for several runs.
func (h *harness) flagsReset() {
	h.app.flags = struct {
		context, url, token, project, environment string
		json, yes                                 bool
	}{}
}

func reply(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func TestNotBeingSignedInSaysHowToSignIn(t *testing.T) {
	h := newHarness(t, func(http.ResponseWriter, *http.Request) { t.Error("nothing should be asked of the API") })

	code, _, stderr := h.run("apps", "list")

	require.Equal(t, ExitDenied, code)
	require.Contains(t, stderr, "geass login")
}

func TestListingAppsPrintsATable(t *testing.T) {
	var gotQuery string
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		reply(w, 200, map[string]any{"items": []map[string]any{
			{"kind": "app", "name": "web", "project": "shop", "environment": "production", "state": "ready", "url": "https://web.example.com", "createdAt": time.Now().Add(-3 * time.Hour)},
			{"kind": "app", "name": "api", "project": "shop", "environment": "production", "state": "draft", "details": map[string]any{"image": "nginx:alpine"}, "createdAt": time.Now().Add(-72 * time.Hour)},
		}})
	})
	h.signedIn(t, "", "")

	code, stdout, _ := h.run("apps", "list", "-p", "shop", "-e", "production")

	require.Equal(t, ExitOK, code)
	require.Equal(t, "environment=production&project=shop", gotQuery)
	require.Contains(t, stdout, "NAME")
	require.Regexp(t, `web\s+shop\s+production\s+ready\s+https://web.example.com\s+3h`, stdout)
	require.Regexp(t, `api\s+shop\s+production\s+draft\s+nginx:alpine\s+3d`, stdout)
}

func TestJSONOutputIsOnlyJSON(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]any{"items": []map[string]any{{"kind": "app", "name": "web", "state": "ready"}}})
	})
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("--json", "apps", "list")

	require.Equal(t, ExitOK, code)
	var decoded struct {
		Items []client.Resource `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &decoded), stdout)
	require.Equal(t, "web", decoded.Items[0].Name)
	require.Empty(t, stderr, "progress and notes go to the error stream, never into what a program parses")
}

func TestASavedDefaultNarrowsStatusButNotAnExplicitList(t *testing.T) {
	var queries []string
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.Path+"?"+r.URL.RawQuery)
		if strings.HasSuffix(r.URL.Path, "/status") {
			reply(w, 200, map[string]any{"name": "web", "state": "ready", "ready": true, "replicas": map[string]any{"desired": 2, "ready": 2, "available": 2}, "url": "https://web.example.com"})
			return
		}
		reply(w, 200, map[string]any{"items": []map[string]any{{"name": "web", "state": "ready"}}})
	})
	h.signedIn(t, "shop", "production")

	code, stdout, _ := h.run("status")

	require.Equal(t, ExitOK, code)
	require.Equal(t, []string{"/api/v1/apps?environment=production&project=shop", "/api/v1/apps/web/status?"}, queries)
	require.Regexp(t, `web\s+ready\s+2/2`, stdout)
}

func TestNotFoundHasItsOwnExitCode(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 404, map[string]string{"error": "app not found", "code": "not_found"})
	})
	h.signedIn(t, "", "")

	code, _, stderr := h.run("apps", "get", "ghost")

	require.Equal(t, ExitNotFound, code)
	require.Contains(t, stderr, "app not found")
}

func TestAnExpiredTokenSaysToSignInAgain(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 401, map[string]string{"error": "invalid or expired API token", "code": "unauthenticated"})
	})
	h.signedIn(t, "", "")

	code, _, stderr := h.run("whoami")

	require.Equal(t, ExitDenied, code)
	require.Contains(t, stderr, "geass login")
}

func TestUsageMistakesExitWithTwo(t *testing.T) {
	h := newHarness(t, func(http.ResponseWriter, *http.Request) {})
	h.signedIn(t, "", "")

	for _, args := range [][]string{{"scale", "web"}, {"scale", "web", "lots"}, {"no-such-command"}, {"apps", "list", "--no-such-flag"}, {"env", "set", "web", "NOEQUALS"}} {
		code, _, stderr := h.run(args...)
		require.Equal(t, ExitUsage, code, "%v: %s", args, stderr)
	}
}

func TestScaleCanPutAServiceToSleep(t *testing.T) {
	var requests []string
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if r.Method == http.MethodGet {
			reply(w, 200, testshapes.SleepStatus("web", true, "Sleeps when idle"))
			return
		}
		requests = append(requests, r.Method+" "+r.URL.Path+" "+strings.TrimSpace(string(raw)))
		_, _ = w.Write([]byte(`{}`))
	})
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("scale", "web", "--sleep-after", "10m")
	require.Equal(t, ExitOK, code, stderr)
	require.Contains(t, stdout, "sleeps after 10m")
	code, _, stderr = h.run("scale", "web", "--always-on")
	require.Equal(t, ExitOK, code, stderr)

	require.Equal(t, []string{
		`PATCH /api/v1/apps/web {"idleSeconds":600,"scaleToZero":"on"}`,
		`PATCH /api/v1/apps/web {"scaleToZero":"off"}`,
	}, requests)

	for _, args := range [][]string{{"scale", "web", "--sleep-after", "1s"}, {"scale", "web", "--sleep-after", "5m", "--always-on"}} {
		code, _, _ = h.run(args...)
		require.Equal(t, ExitUsage, code, "%v", args)
	}
}

func TestDeployingWaitsAndReportsTheOutcome(t *testing.T) {
	var polls atomic.Int32
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/deploy"):
			reply(w, 200, map[string]any{"ok": true, "deployed": true, "title": "Deploying web", "pending": []string{}})
		case strings.HasSuffix(r.URL.Path, "/status"):
			if polls.Add(1) < 3 {
				reply(w, 200, map[string]any{"name": "web", "state": "pending", "ready": false, "build": map[string]any{"phase": "Running"}})
				return
			}
			reply(w, 200, map[string]any{"name": "web", "state": "ready", "ready": true, "url": "https://web.example.com", "replicas": map[string]any{"desired": 1, "ready": 1, "available": 1}})
		}
	})
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("deploy", "web", "--wait")

	require.Equal(t, ExitOK, code, stderr)
	require.Contains(t, stderr, "web: Deploying web")
	require.Contains(t, stderr, "web: build running", "progress shows on the error stream")
	require.Contains(t, stderr, "web: ready at https://web.example.com")
	require.Regexp(t, `web\s+ready\s+1/1`, stdout)
}

func TestAServiceThatSleepsIsShownAsAsleepNotDown(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]any{"name": "web", "state": "ready", "ready": true, "sleeping": true, "replicas": map[string]any{"desired": 0, "ready": 0, "available": 0}})
	})
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("status", "web")

	require.Equal(t, ExitOK, code, stderr)
	require.Regexp(t, `web\s+ready\s+0/0 \(asleep\)`, stdout)
}

func TestADeployThatFailsExitsWithFiveAndSaysWhy(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			reply(w, 200, map[string]any{"ok": true, "deployed": true})
			return
		}
		reply(w, 200, map[string]any{"name": "web", "state": "pending", "ready": false,
			"build": map[string]any{"phase": "Failed", "failureReason": "Kaniko completed without an immutable image digest"}})
	})
	h.signedIn(t, "", "")

	code, _, stderr := h.run("deploy", "web", "--wait")

	require.Equal(t, ExitDeployFailed, code)
	require.Contains(t, stderr, "immutable image digest")
}

func TestDeployWithoutNamesDeploysTheEnvironmentAndWaitsForWhatIsThere(t *testing.T) {
	var deployBody map[string]any
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/projects/shop/deploy":
			_ = json.NewDecoder(r.Body).Decode(&deployBody)
			reply(w, 200, map[string]any{"ok": true, "deployed": 2})
		case r.URL.Path == "/api/v1/apps":
			reply(w, 200, map[string]any{"items": []map[string]any{{"name": "web", "state": "pending"}, {"name": "draft", "state": "draft"}}})
		case strings.HasSuffix(r.URL.Path, "/status"):
			reply(w, 200, map[string]any{"name": "web", "state": "ready", "ready": true})
		}
	})
	h.signedIn(t, "shop", "production")

	code, _, stderr := h.run("deploy", "--wait")

	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, map[string]any{"environment": "production"}, deployBody)
	require.Contains(t, stderr, "Deployed 2 pending change(s)")
	require.NotContains(t, stderr, "draft:", "a draft is never waited for")
}

func TestDeployWithNothingToGoOnIsAUsageError(t *testing.T) {
	h := newHarness(t, func(http.ResponseWriter, *http.Request) {})
	h.signedIn(t, "", "")

	code, _, stderr := h.run("deploy")

	require.Equal(t, ExitUsage, code)
	require.Contains(t, stderr, "--project")
}

func TestDeletingNeedsYesWhenNobodyCanBeAsked(t *testing.T) {
	var deleted atomic.Bool
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleted.Store(true)
		}
		reply(w, 200, map[string]any{"ok": true})
	})
	h.signedIn(t, "", "")

	code, _, stderr := h.run("apps", "delete", "web")
	require.Equal(t, ExitUsage, code)
	require.Contains(t, stderr, "--yes")
	require.False(t, deleted.Load(), "nothing was deleted")

	code, stdout, _ := h.run("apps", "delete", "web", "--yes")
	require.Equal(t, ExitOK, code)
	require.True(t, deleted.Load())
	require.Contains(t, stdout, "Deleted web")
}

func TestDeletingAsksAPersonWhoIsThere(t *testing.T) {
	var deleted atomic.Bool
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deleted.Store(true)
		}
		reply(w, 200, map[string]any{"ok": true})
	})
	h.signedIn(t, "", "")
	h.app.Interactive = func() bool { return true }

	h.app.In = strings.NewReader("n\n")
	code, _, _ := h.run("apps", "delete", "web")
	require.Equal(t, ExitError, code)
	require.False(t, deleted.Load(), "no means no")

	h.app.In = strings.NewReader("yes\n")
	code, _, _ = h.run("apps", "delete", "web")
	require.Equal(t, ExitOK, code)
	require.True(t, deleted.Load())
}

func TestSettingVariablesSaysTheyArePending(t *testing.T) {
	var calls []string
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		calls = append(calls, r.URL.Path+" "+body["key"].(string)+"="+body["value"].(string))
		reply(w, 200, map[string]any{"ok": true})
	})
	h.signedIn(t, "", "")

	code, _, stderr := h.run("env", "set", "web", "LOG_LEVEL=debug", "URL=http://x?a=b")
	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, []string{"/api/v1/apps/web/config/set LOG_LEVEL=debug", "/api/v1/apps/web/config/set URL=http://x?a=b"}, calls, "only the first = splits, so a value can contain one")
	require.Contains(t, stderr, "geass deploy web")

	code, _, _ = h.run("env", "set", "web", "--secret", "KEY=v")
	require.Equal(t, ExitOK, code)
	require.Equal(t, "/api/v1/apps/web/secrets/set KEY=v", calls[2])
}

func TestListingVariablesShowsNamesOnly(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]any{"secrets": []string{"STRIPE_KEY"}, "config": map[string]any{"LOG_LEVEL": "debug"}, "env": []map[string]any{{"name": "PORT", "kind": "plain"}}})
	})
	h.signedIn(t, "", "")

	code, stdout, _ := h.run("env", "list", "web")

	require.Equal(t, ExitOK, code)
	require.Regexp(t, `STRIPE_KEY\s+secret`, stdout)
	require.Regexp(t, `LOG_LEVEL\s+config`, stdout)
	require.NotContains(t, stdout, "debug", "values are not shown")
}

func TestRollbackGoesToThePreviousDeploymentByDefault(t *testing.T) {
	var rolledTo string
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			rolledTo, _ = body["revision"].(string)
			reply(w, 200, map[string]any{"ok": true})
			return
		}
		reply(w, 200, map[string]any{"items": []map[string]any{{"id": "web-3"}, {"id": "web-2"}, {"id": "web-1"}}})
	})
	h.signedIn(t, "", "")

	code, _, _ := h.run("rollback", "web")
	require.Equal(t, ExitOK, code)
	require.Equal(t, "web-2", rolledTo, "newest is the one running, so the one before it")

	code, _, _ = h.run("rollback", "web", "web-1")
	require.Equal(t, ExitOK, code)
	require.Equal(t, "web-1", rolledTo)
}

func TestLogsPrintLinesAndFollowStreams(t *testing.T) {
	var requests []string
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path+"?"+r.URL.RawQuery)
		switch {
		case strings.HasSuffix(r.URL.Path, "/logs/stream"):
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, "data: {\"t\":\"2026-01-01T10:00:00Z\",\"service\":\"web\",\"pod\":\"web-5d8f-abcde\",\"container\":\"app\",\"line\":\"streamed line\",\"level\":\"info\"}\n\n")
		case strings.HasSuffix(r.URL.Path, "/logs"):
			reply(w, 200, testshapes.LogsAnswer("web",
				map[string]any{"t": "2026-01-01T09:00:00Z", "service": "web", "pod": "web-5d8f-abcde", "container": "app", "line": "hello", "level": "info"},
				map[string]any{"t": "2026-01-01T09:00:05Z", "service": "web", "pod": "web-5d8f-fghij", "container": "app", "line": "Error: boom", "level": "error"},
			))
		default:
			t.Errorf("unexpected request %s", r.URL)
		}
	})
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("logs", "web", "--tail", "10")
	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, "09:00:00 abcde hello\n09:00:05 fghij Error: boom\n", stdout, "the lines the API sent, which an earlier version printed none of")
	require.Equal(t, []string{"/api/v1/apps/web/logs?tail=10"}, requests, "one request: the service's own route")

	code, stdout, _ = h.run("logs", "web", "-f")
	require.Equal(t, ExitOK, code)
	require.Contains(t, stdout, "streamed line")
}

func TestLogsPassTheirFiltersOn(t *testing.T) {
	var query string
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		reply(w, 200, testshapes.LogsAnswer("web", map[string]any{"t": "2026-01-01T09:00:00Z", "pod": "web-5d8f-abcde", "line": "timeout", "level": "error"}))
	})
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("logs", "web", "--tail", "10", "--since", "15m", "--level", "error", "--search", "timeout")

	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, "level=error&q=timeout&since=15m&tail=10", query)
	require.Contains(t, stdout, "timeout")
}

func TestLogsAsJSONAreTheLinesTheAPISent(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, testshapes.LogsAnswer("web", map[string]any{"t": "2026-01-01T09:00:00Z", "service": "web", "pod": "web-5d8f-abcde", "container": "app", "line": "hello", "level": "info"}))
	})
	h.signedIn(t, "", "")

	_, stdout, stderr := h.run("--json", "logs", "web")

	var line client.LogLine
	require.NoError(t, json.Unmarshal([]byte(stdout), &line), stdout)
	require.Equal(t, "hello", line.Text)
	require.Equal(t, "web", line.Service)
	require.Empty(t, stderr)
}

func TestNoLogsSaysSoOnTheErrorStream(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) { reply(w, 200, testshapes.LogsAnswer("web")) })
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("logs", "web")

	require.Equal(t, ExitOK, code)
	require.Empty(t, stdout, "nothing is mixed into what a program reads")
	require.Contains(t, stderr, "No log lines matched")
}

func TestLoginWithATokenSavesIt(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer gsw_00000000_"+strings.Repeat("b", 40), r.Header.Get("Authorization"))
		reply(w, 200, map[string]any{"username": "token:ci", "role": "observer"})
	})
	token := "gsw_00000000_" + strings.Repeat("b", 40)

	code, stdout, stderr := h.run("login", "--url", h.server.URL, "--token", token)

	require.Equal(t, ExitOK, code, stderr)
	require.Contains(t, stdout, "Signed in")
	config, err := LoadConfig(h.config)
	require.NoError(t, err)
	require.Equal(t, token, config.Contexts[config.Current].Token)
	info, err := os.Stat(h.config)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "a file with a token in it is for its owner only")

	// The saved login is used with no flags.
	code, stdout, _ = h.run("whoami")
	require.Equal(t, ExitOK, code)
	require.Contains(t, stdout, "token:ci")
}

func TestLoginWithATokenFromStandardInput(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]any{"username": "token:ci", "role": "admin"})
	})
	h.app.In = strings.NewReader("gsw_00000000_" + strings.Repeat("c", 40) + "\n")

	code, _, stderr := h.run("login", "--url", h.server.URL, "--token-stdin")

	require.Equal(t, ExitOK, code, stderr)
	config, _ := LoadConfig(h.config)
	require.Equal(t, "gsw_00000000_"+strings.Repeat("c", 40), config.Contexts[config.Current].Token)
}

func TestLoginWithABadTokenSavesNothing(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 401, map[string]string{"error": "invalid or expired API token", "code": "unauthenticated"})
	})

	code, _, _ := h.run("login", "--url", h.server.URL, "--token", "gsw_00000000_"+strings.Repeat("d", 40))

	require.Equal(t, ExitDenied, code)
	_, err := os.Stat(h.config)
	require.True(t, os.IsNotExist(err), "a login that did not work is not remembered")
}

func TestLoginByApprovingACodeInTheDashboard(t *testing.T) {
	var polls atomic.Int32
	var opened string
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/device":
			reply(w, 200, map[string]any{"deviceCode": "dc", "userCode": "BCDF-GHJK", "verificationUri": "https://geass.example/device",
				"verificationUriComplete": "https://geass.example/device?code=BCDF-GHJK", "interval": 1, "expiresIn": 600})
		case "/api/v1/auth/token":
			switch polls.Add(1) {
			case 1:
				reply(w, 400, map[string]string{"error": "waiting", "code": "authorization_pending"})
			case 2:
				reply(w, 429, map[string]string{"error": "slow down", "code": "slow_down"})
			default:
				reply(w, 200, map[string]any{"token": "gsw_00000000_" + strings.Repeat("e", 40), "role": "observer"})
			}
		case "/api/v1/me":
			reply(w, 200, map[string]any{"username": "token:CLI", "role": "observer"})
		}
	})
	h.app.Interactive = func() bool { return true }
	h.app.OpenBrowser = func(address string) error { opened = address; return nil }

	code, stdout, stderr := h.run("login", "--url", h.server.URL)

	require.Equal(t, ExitOK, code, stderr)
	require.Contains(t, stderr, "BCDF-GHJK", "the code is shown so it can be checked against the dashboard")
	require.Equal(t, "https://geass.example/device?code=BCDF-GHJK", opened)
	require.Contains(t, stdout, "Signed in")
	require.EqualValues(t, 3, polls.Load())
}

func TestLoginRefusedInTheDashboardFails(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/device":
			reply(w, 200, map[string]any{"deviceCode": "dc", "userCode": "BCDF-GHJK", "verificationUri": "x", "interval": 1, "expiresIn": 600})
		default:
			reply(w, 400, map[string]string{"error": "refused", "code": "access_denied"})
		}
	})

	code, _, stderr := h.run("login", "--url", h.server.URL, "--no-browser")

	require.Equal(t, ExitDenied, code)
	require.Contains(t, stderr, "refused")
}

func TestLogoutKeepsTheAddressAndForgetsTheToken(t *testing.T) {
	h := newHarness(t, func(http.ResponseWriter, *http.Request) {})
	h.signedIn(t, "shop", "production")

	code, _, _ := h.run("logout")

	require.Equal(t, ExitOK, code)
	config, _ := LoadConfig(h.config)
	require.Empty(t, config.Contexts["stub"].Token)
	require.Equal(t, h.server.URL, config.Contexts["stub"].URL)
	require.Equal(t, "shop", config.Contexts["stub"].Project, "defaults survive")
}

func TestEnvironmentVariablesCanProvideTheLoginForScripts(t *testing.T) {
	var gotAuth string
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		reply(w, 200, map[string]any{"username": "token:ci", "role": "admin", "canMutate": true})
	})
	h.app.Getenv = func(key string) string {
		return map[string]string{"GEASS_URL": h.server.URL, "GEASS_TOKEN": "gsw_00000000_" + strings.Repeat("f", 40)}[key]
	}

	code, _, stderr := h.run("whoami")

	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, "Bearer gsw_00000000_"+strings.Repeat("f", 40), gotAuth, "no config file was needed")
}

func TestConfigContextsCanBeListedAndSwitched(t *testing.T) {
	h := newHarness(t, func(http.ResponseWriter, *http.Request) {})
	config := Config{Current: "staging", Contexts: map[string]Context{
		"staging": {URL: "https://staging.example", Token: "secret-1", User: "me"},
		"prod":    {URL: "https://prod.example", Token: "secret-2"},
	}}
	require.NoError(t, config.Save(h.config))

	code, stdout, _ := h.run("config", "get-contexts")
	require.Equal(t, ExitOK, code)
	require.Regexp(t, `\*\s+staging`, stdout)

	code, stdout, _ = h.run("--json", "config", "get-contexts")
	require.Equal(t, ExitOK, code)
	require.NotContains(t, stdout, "secret-1", "tokens are never printed")

	code, _, _ = h.run("config", "use-context", "prod")
	require.Equal(t, ExitOK, code)
	saved, _ := LoadConfig(h.config)
	require.Equal(t, "prod", saved.Current)

	code, _, stderr := h.run("config", "use-context", "nowhere")
	require.Equal(t, ExitNotFound, code)
	require.Contains(t, stderr, "staging")

	code, _, _ = h.run("-p", "shop", "-e", "staging", "config", "set-context")
	require.Equal(t, ExitOK, code)
	saved, _ = LoadConfig(h.config)
	require.Equal(t, "shop", saved.Contexts["prod"].Project)
}

func TestNodeTaggingSendsComplianceAndImpliesTheTag(t *testing.T) {
	var body map[string]any
	var path string
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&body)
		reply(w, 200, map[string]any{"ok": true})
	})
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("nodes", "tag", "worker-2", "--region", "eu", "--tags", "gpu", "--compliance", "hipaa")

	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, "/api/v1/nodes/worker-2/placement", path)
	require.Equal(t, "eu", body["region"])
	require.Equal(t, "gpu,hipaa", body["tags"], "a compliance tag is a tag the node carries")
	require.Equal(t, "hipaa", body["compliance"])
	require.Contains(t, stdout, "not moved")
}

func TestComplianceShowSetAndClear(t *testing.T) {
	var setBody map[string]any
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			reply(w, 200, map[string]any{"requiredTags": []string{"hipaa"}, "databases": []map[string]any{{"name": "orders", "compliant": false, "missing": []string{"hipaa"}}}, "openNodes": []string{"n3"}})
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&setBody)
		reply(w, 200, map[string]any{"ok": true})
	})
	h.signedIn(t, "", "")

	code, stdout, _ := h.run("compliance", "show", "clinic")
	require.Equal(t, ExitOK, code)
	require.Contains(t, stdout, "requires nodes tagged: hipaa")
	require.Regexp(t, `orders\s+missing hipaa`, stdout)
	require.Contains(t, stdout, "n3")

	code, _, _ = h.run("compliance", "set", "clinic", "hipaa", "pci")
	require.Equal(t, ExitOK, code)
	require.Equal(t, "hipaa,pci", setBody["requiredTags"])

	code, _, _ = h.run("compliance", "clear", "clinic")
	require.Equal(t, ExitOK, code)
	require.Equal(t, "", setBody["requiredTags"])
}

func TestTheRawAPICommandSendsWhatItIsGiven(t *testing.T) {
	var gotMethod, gotQuery string
	var gotBody map[string]any
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotQuery = r.Method, r.URL.RawQuery
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		reply(w, 200, map[string]any{"ok": true})
	})
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("api", "post", "/apps/web/scale?x=1", "-q", "y=2", "-d", `{"replicas": 3}`)

	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, http.MethodPost, gotMethod)
	require.Equal(t, "x=1&y=2", gotQuery)
	require.Equal(t, map[string]any{"replicas": float64(3)}, gotBody)
	require.Contains(t, stdout, `"ok": true`)

	code, _, _ = h.run("api", "POST", "/apps/web/scale", "-d", "{not json")
	require.NotEqual(t, ExitOK, code, "a body that is not JSON is refused before it is sent")
}

func TestCreatingAnAppFromAnImage(t *testing.T) {
	var body map[string]any
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		reply(w, 200, map[string]any{"ok": true})
	})
	h.signedIn(t, "shop", "production")

	code, stdout, stderr := h.run("apps", "create", "web", "--image", "nginx:alpine", "--port", "8080")

	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, map[string]any{"project": "shop", "environment": "production", "name": "web", "draft": true, "source": "image", "image": "nginx:alpine", "port": float64(8080)}, body)
	require.Contains(t, stdout, "draft")

	code, _, _ = h.run("apps", "create", "web")
	require.Equal(t, ExitUsage, code, "with nothing to run")
}

func TestConnectionHidesThePasswordUnlessAsked(t *testing.T) {
	var query string
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		out := map[string]any{"host": "db.svc", "port": "5432", "username": "app", "keys": []string{"password"}}
		if r.URL.Query().Get("reveal") == "true" {
			out["password"], out["revealed"] = "hunter2", true
		}
		reply(w, 200, out)
	})
	h.signedIn(t, "", "")

	_, stdout, stderr := h.run("dbs", "connection", "orders")
	require.NotContains(t, stdout, "hunter2")
	require.Contains(t, stdout, "db.svc")
	require.Contains(t, stderr, "--reveal")
	require.Empty(t, query)

	_, stdout, _ = h.run("dbs", "connection", "orders", "--reveal")
	require.Contains(t, stdout, "hunter2")
	require.Equal(t, "reveal=true", query)
}

func TestNodesAreListedWithTheirTags(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]any{"items": []map[string]any{
			{"name": "h1", "ready": true, "region": "eu", "tags": []map[string]any{{"name": "hipaa", "reserved": true, "compliance": true}}, "cpu": "4", "memory": "8Gi"},
			{"name": "g1", "ready": false},
		}})
	})
	h.signedIn(t, "", "")

	_, stdout, _ := h.run("nodes", "list")

	require.Regexp(t, `h1\s+Ready\s+eu\s+-\s+hipaa \(compliance\)`, stdout)
	require.Regexp(t, `g1\s+NotReady`, stdout)
}

func TestVersionWorksSignedOut(t *testing.T) {
	h := newHarness(t, func(http.ResponseWriter, *http.Request) {})

	code, stdout, _ := h.run("version")

	require.Equal(t, ExitOK, code)
	require.Contains(t, stdout, "geass test")
}

func TestVersionSaysWhichReleaseTheClusterRuns(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"api":"v1","release":"v0.3.0","name":"geass"}`))
	})
	h.signedIn(t, "", "")

	code, stdout, _ := h.run("version")
	require.Equal(t, ExitOK, code)
	require.Contains(t, stdout, "api v1\n")
	require.Contains(t, stdout, "release v0.3.0\n")
	require.NotContains(t, stdout, "note:", "a command line that is not a release (the tests' is \"test\") cannot be ordered against the cluster's")

	code, stdout, _ = h.run("--json", "version")
	require.Equal(t, ExitOK, code)
	require.JSONEq(t, `{"cli":"test","api":"v1","release":"v0.3.0"}`, stdout)
}

func TestVersionSaysWhenTheCommandLineAndTheClusterAreOutOfStep(t *testing.T) {
	require.Empty(t, releaseMismatch("v0.3.0", "v0.3.0"))
	require.Empty(t, releaseMismatch("dev", "v0.3.0"), "nothing is known about the order of a development build")
	require.Empty(t, releaseMismatch("v0.3.0", ""), "a cluster that predates the field says no release")

	older := releaseMismatch("v0.2.0", "v0.3.0")
	require.Contains(t, older, "older than the cluster")
	require.Contains(t, older, "install-cli.sh")
	require.Contains(t, older, "GEASS_VERSION=v0.3.0")

	newer := releaseMismatch("v0.4.0", "v0.3.0")
	require.Contains(t, newer, "newer than the cluster")
	require.Contains(t, newer, "installer on the control plane")

	// A build past a tag, or with changes, is a developer's own: there is no such release to install, so neither side
	// is told to update to it.
	for _, dev := range []string{"v0.3.0-4-g1a2b3c4", "v0.3.0-4-g1a2b3c4-dirty", "v0.3.0-dirty"} {
		require.Empty(t, releaseMismatch(dev, "v0.4.0"), "cli %s", dev)
		require.Empty(t, releaseMismatch("v0.3.0", dev), "cluster %s", dev)
		require.Empty(t, releaseMismatch(dev, "v0.3.0"), "cli %s", dev)
	}
}

func TestTheExitCodeFollowsTheKindOfFailure(t *testing.T) {
	require.Equal(t, ExitOK, exitCodeFor(nil))
	require.Equal(t, ExitNotFound, exitCodeFor(&client.Error{Status: 404}))
	require.Equal(t, ExitDenied, exitCodeFor(&client.Error{Status: 401}))
	require.Equal(t, ExitDenied, exitCodeFor(&client.Error{Status: 403}))
	require.Equal(t, ExitDeployFailed, exitCodeFor(client.ErrDeployFailed))
	require.Equal(t, ExitTimeout, exitCodeFor(context.DeadlineExceeded))
	require.Equal(t, ExitError, exitCodeFor(io.EOF))
	require.Equal(t, ExitUsage, exitCodeFor(usageError("bad")))
}
