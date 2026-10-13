package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ---- a saved token goes only to the address it was issued for ----

// spy is a second Geass: it records every request it is sent, and as whom.
type spy struct {
	server *httptest.Server
	mu     sync.Mutex
	seen   []string
}

func newSpy(t *testing.T) *spy {
	t.Helper()
	s := &spy{}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.seen = append(s.seen, r.Method+" "+r.URL.Path+" auth="+r.Header.Get("Authorization"))
		s.mu.Unlock()
		reply(w, 200, map[string]any{"items": []any{}, "api": "v1", "username": "token:other", "role": "admin"})
	}))
	t.Cleanup(s.server.Close)
	return s
}

func (s *spy) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seen...)
}

const savedToken = "gsw_00000000_" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestTheSavedTokenIsNotSentToAnotherAddress(t *testing.T) {
	var home []string
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		home = append(home, r.URL.Path)
		reply(w, 200, map[string]any{"items": []any{}})
	})
	h.signedIn(t, "", "")
	other := newSpy(t)

	for name, set := range map[string]func(){
		"--url": func() {},
		"$GEASS_URL": func() {
			h.app.Getenv = func(key string) string { return map[string]string{"GEASS_URL": other.server.URL}[key] }
		},
	} {
		args := []string{"apps", "list"}
		if name == "--url" {
			args = append([]string{"--url", other.server.URL}, args...)
		}
		set()

		code, _, stderr := h.run(args...)

		require.Equal(t, ExitDenied, code, "%s: %s", name, stderr)
		require.Contains(t, stderr, "geass login --url "+other.server.URL, "%s: it says how to sign in to that address", name)
		require.Empty(t, other.requests(), "%s: nothing is sent to an address the saved token was not issued for", name)
		require.Empty(t, home, "%s: and nothing is sent anywhere else", name)
	}
}

func TestAnotherAddressStillGetsATokenThePersonGivesForIt(t *testing.T) {
	h := newHarness(t, func(http.ResponseWriter, *http.Request) {})
	h.signedIn(t, "", "")
	other := newSpy(t)
	given := "gsw_11111111_" + strings.Repeat("b", 40)

	code, _, stderr := h.run("--url", other.server.URL, "--token", given, "apps", "list")

	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, []string{"GET /api/v1/apps auth=Bearer " + given}, other.requests(), "a token on the command line is the person's choice for this run")
}

func TestALoginSavedForAnAddressIsUsedWhenThatAddressIsAskedFor(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) { t.Error("the current login's address was not asked for") })
	other := newSpy(t)
	theirs := "gsw_22222222_" + strings.Repeat("c", 40)
	config := Config{Current: "stub", Contexts: map[string]Context{
		"stub":  {URL: h.server.URL, Token: savedToken},
		"other": {URL: other.server.URL, Token: theirs},
	}}
	require.NoError(t, config.Save(h.config))

	code, _, stderr := h.run("--url", other.server.URL, "apps", "list")

	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, []string{"GET /api/v1/apps auth=Bearer " + theirs}, other.requests(), "the token saved for that address, and not the current login's")
}

func TestVersionDoesNotSendTheSavedTokenToAnotherAddress(t *testing.T) {
	h := newHarness(t, func(http.ResponseWriter, *http.Request) {})
	h.signedIn(t, "", "")
	other := newSpy(t)

	code, stdout, stderr := h.run("--url", other.server.URL, "version")

	require.Equal(t, ExitOK, code, stderr)
	require.Contains(t, stdout, "api v1")
	require.Equal(t, []string{"GET /api/v1/version auth="}, other.requests())
}

func TestResolveBindsASavedLoginToItsOwnAddress(t *testing.T) {
	config := Config{Current: "staging", Contexts: map[string]Context{
		"staging": {URL: "https://staging.example.com", Token: "staging-token", Project: "proj-s", Environment: "staging"},
		"prod":    {URL: "https://prod.example.com/", Token: "prod-token", Project: "proj-p", Environment: "production"},
		"local":   {URL: "http://[::1]:8085", Token: "local-token"},
	}}
	env := func(values map[string]string) func(string) string {
		return func(key string) string { return values[key] }
	}

	tests := map[string]struct {
		flags       Resolved
		contextFlag string
		env         map[string]string
		config      *Config
		token       string
		project     string
		contextName string
	}{
		"nothing overridden": {token: "staging-token", project: "proj-s", contextName: "staging"},
		"the same address spelled differently": {
			flags: Resolved{URL: "HTTPS://Staging.Example.com:443/"}, token: "staging-token", project: "proj-s", contextName: "staging",
		},
		"another host": {flags: Resolved{URL: "https://other.example.com"}, token: "", project: "", contextName: ""},
		"another host from the environment": {
			env: map[string]string{"GEASS_URL": "https://other.example.com"}, token: "", project: "", contextName: "",
		},
		"the same host over plain http": {flags: Resolved{URL: "http://staging.example.com"}, token: "", contextName: ""},
		"another port":                  {flags: Resolved{URL: "https://staging.example.com:8443"}, token: ""},
		"another path":                  {flags: Resolved{URL: "https://staging.example.com/other"}, token: ""},
		"a token the person gave":       {flags: Resolved{URL: "https://other.example.com", Token: "given"}, token: "given"},
		"a token from the environment":  {flags: Resolved{URL: "https://other.example.com"}, env: map[string]string{"GEASS_TOKEN": "from-env"}, token: "from-env"},
		"another saved login's address": {
			flags: Resolved{URL: "https://prod.example.com"}, token: "prod-token", project: "proj-p", contextName: "prod",
		},
		"a login chosen by name whose address was overridden to another login's": {
			flags: Resolved{URL: "https://staging.example.com"}, contextFlag: "prod", token: "staging-token", project: "proj-s", contextName: "staging",
		},
		"a login chosen by name whose address was overridden elsewhere": {
			flags: Resolved{URL: "https://other.example.com"}, contextFlag: "prod", token: "",
		},
		"an address in brackets": {flags: Resolved{URL: "http://[::1]:8085"}, token: "local-token", contextName: "local"},
		"no login at all": {
			config: &Config{}, flags: Resolved{URL: "https://staging.example.com"}, token: "",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := config
			if tt.config != nil {
				c = *tt.config
			}

			got := c.Resolve(tt.flags, tt.contextFlag, env(tt.env))

			require.Equal(t, tt.token, got.Token)
			require.Equal(t, tt.project, got.Project, "a project from one Geass is not applied to another")
			require.Equal(t, tt.contextName, got.ContextName)
		})
	}
}

// ---- when a token expires ----

func TestWhoamiSaysWhenTheTokenExpires(t *testing.T) {
	expires := time.Now().Add(10*24*time.Hour + time.Hour).UTC()
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]any{"username": "token:ci", "role": "observer", "authentication": "token", "expiresAt": expires})
	})
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("whoami")

	require.Equal(t, ExitOK, code, stderr)
	require.Contains(t, stdout, "expires: "+expires.Format("2006-01-02 15:04")+" UTC (in 10 days)")

	_, stdout, _ = h.run("--json", "whoami")
	require.Contains(t, stdout, `"expiresAt"`)
}

func TestWhoamiSaysWhenATokenNeverExpires(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]any{"username": "token:ci", "role": "admin", "canMutate": true, "authentication": "token"})
	})
	h.signedIn(t, "", "")

	_, stdout, _ := h.run("whoami")

	require.Contains(t, stdout, "expires: never")
}

func TestDescribingWhenATokenExpires(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }

	require.Equal(t, "never", describeExpiry(nil, now))
	require.Equal(t, "2026-10-12 12:30 UTC (in 30 minutes)", describeExpiry(at(30*time.Minute), now))
	require.Equal(t, "2026-10-13 00:00 UTC (in 12 hours)", describeExpiry(at(12*time.Hour), now))
	require.Equal(t, "2026-11-11 12:00 UTC (in 30 days)", describeExpiry(at(30*24*time.Hour), now))
	require.Contains(t, describeExpiry(at(-time.Minute), now), "expired")
}

func TestLoginRemembersWhenTheTokenExpiresAndLogoutForgetsIt(t *testing.T) {
	expires := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]any{"username": "token:ci", "role": "observer", "authentication": "token", "expiresAt": expires})
	})

	code, _, stderr := h.run("login", "--url", h.server.URL, "--token", savedToken)
	require.Equal(t, ExitOK, code, stderr)
	config, err := LoadConfig(h.config)
	require.NoError(t, err)
	require.NotNil(t, config.Contexts[config.Current].ExpiresAt, "it is saved, which nothing did before")
	require.True(t, config.Contexts[config.Current].ExpiresAt.Equal(expires))

	_, stdout, _ := h.run("config", "get-contexts")
	require.Contains(t, stdout, "EXPIRES")
	require.Contains(t, stdout, "2026-11-11")

	code, _, _ = h.run("logout")
	require.Equal(t, ExitOK, code)
	config, _ = LoadConfig(h.config)
	require.Nil(t, config.Contexts[config.Current].ExpiresAt, "a token that is forgotten has no expiry to remember")
}

// ---- deployments, and the ids rollback takes ----

func TestDeploymentsAreListedNewestFirstWithTheIDsRollbackTakes(t *testing.T) {
	var path string
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		// What the dashboard sends: a phase of Recorded, no trigger, and the actor the dashboard names itself.
		reply(w, 200, map[string]any{"items": []map[string]any{
			{"id": "web-7x2kq", "phase": "Recorded", "image": "nginx:1.27", "commit": "", "trigger": "", "actor": "Dashboard", "changeTitle": "Deploy nginx:1.27", "createdAt": time.Now().Add(-3 * time.Hour)},
			{"id": "web-4m8zp", "phase": "Recorded", "image": "nginx:1.26", "actor": "GitHub", "trigger": "push", "changeTitle": "Deploy acme/web@main", "createdAt": time.Now().Add(-72 * time.Hour)},
		}})
	})
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("apps", "deployments", "web")

	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, "/api/v1/apps/web/deployments", path)
	require.Regexp(t, `ID\s+PHASE\s+AGE\s+TRIGGER\s+ACTOR\s+CHANGE`, stdout)
	require.Regexp(t, `web-7x2kq\s+Recorded\s+3h\s+-\s+Dashboard\s+Deploy nginx:1.27`, stdout)
	require.Regexp(t, `web-4m8zp\s+Recorded\s+3d\s+push\s+GitHub\s+Deploy acme/web@main`, stdout)
	require.Less(t, strings.Index(stdout, "web-7x2kq"), strings.Index(stdout, "web-4m8zp"), "in the order the API gives, which is newest first")
}

func TestDeploymentsAsJSONKeepEveryField(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]any{"items": []map[string]any{{"id": "web-7x2kq", "phase": "Recorded", "image": "nginx:1.27", "createdAt": time.Now()}}})
	})
	h.signedIn(t, "", "")

	_, stdout, stderr := h.run("--json", "apps", "deployments", "web")

	var out struct {
		Items []struct {
			ID    string `json:"id"`
			Image string `json:"image"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &out), stdout)
	require.Equal(t, "web-7x2kq", out.Items[0].ID)
	require.Equal(t, "nginx:1.27", out.Items[0].Image)
	require.Empty(t, stderr)
}

func TestAServiceWithNoDeploymentsListsNone(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) { reply(w, 200, map[string]any{"items": []any{}}) })
	h.signedIn(t, "", "")

	_, stdout, _ := h.run("--json", "apps", "deployments", "web")
	require.JSONEq(t, `{"items": []}`, stdout, "an empty list, not null")

	code, _, _ := h.run("apps", "deployments")
	require.Equal(t, ExitUsage, code, "it needs the service")
}

func TestRollbackPointsAtTheCommandThatListsDeployments(t *testing.T) {
	h := newHarness(t, func(http.ResponseWriter, *http.Request) {})

	_, stdout, _ := h.run("rollback", "--help")

	require.Contains(t, stdout, "geass apps deployments APP")
	require.NotContains(t, stdout, "geass apps get APP", "which does not list them")
}

// ---- sleeping when idle is only reported when it is so ----

func TestScaleSaysSoWhenTheServiceCannotSleep(t *testing.T) {
	var requests []string
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			reply(w, 200, sleepDetail(false, "The serverless add-on is not installed"))
			return
		}
		raw, _ := io.ReadAll(r.Body)
		requests = append(requests, r.Method+" "+r.URL.Path+" "+strings.TrimSpace(string(raw)))
		reply(w, 200, map[string]any{"ok": true})
	})
	h.signedIn(t, "", "")
	h.app.SleepCheckTimeout = 30 * time.Millisecond

	code, stdout, stderr := h.run("scale", "web", "--sleep-after", "10m")

	require.Equal(t, ExitError, code, "the service stays running, so this did not do what was asked")
	require.Contains(t, stderr, "cannot sleep")
	require.Contains(t, stderr, "The serverless add-on is not installed", "and says why")
	require.NotContains(t, stdout, "sleeps after")
	require.Equal(t, []string{`PATCH /api/v1/apps/web {"idleSeconds":600,"scaleToZero":"on"}`}, requests, "the setting was saved")
}

func TestScaleStillScalesWhenTheServiceCannotSleep(t *testing.T) {
	var scaled bool
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet:
			reply(w, 200, sleepDetail(false, "The service has no public host"))
		case strings.HasSuffix(r.URL.Path, "/scale"):
			scaled = true
			reply(w, 200, map[string]any{"ok": true})
		default:
			reply(w, 200, map[string]any{"ok": true})
		}
	})
	h.signedIn(t, "", "")
	h.app.SleepCheckTimeout = 30 * time.Millisecond

	code, stdout, stderr := h.run("scale", "web", "3", "--sleep-after", "10m")

	require.Equal(t, ExitError, code)
	require.Contains(t, stderr, "no public host")
	require.True(t, scaled, "the rest of what was asked is still done")
	require.Contains(t, stdout, "web scaled to 3")
}

func TestScaleDoesNotClaimWhatTheClusterHasNotConfirmed(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// The operator has not looked yet: no active, no message.
			reply(w, 200, map[string]any{"resource": map[string]any{"name": "web", "details": map[string]any{"scaleToZero": map[string]any{"idleSeconds": 600}}}})
			return
		}
		reply(w, 200, map[string]any{"ok": true})
	})
	h.signedIn(t, "", "")
	h.app.SleepCheckTimeout = 30 * time.Millisecond

	code, stdout, stderr := h.run("scale", "web", "--sleep-after", "10m")

	require.Equal(t, ExitError, code)
	require.Contains(t, stderr, "has not said yet")
	require.NotContains(t, stdout, "sleeps after")
}

func TestKeepingAServiceRunningNeedsNoConfirmation(t *testing.T) {
	var gets int
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			gets++
		}
		reply(w, 200, map[string]any{"ok": true})
	})
	h.signedIn(t, "", "")

	code, stdout, _ := h.run("scale", "web", "--always-on")

	require.Equal(t, ExitOK, code)
	require.Contains(t, stdout, "stays running")
	require.Zero(t, gets)
}

// ---- nodes tag shows the warnings ----

func TestNodeTaggingShowsTheWarningsTheAPISends(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]any{"ok": true, "warnings": []string{
			"hipaa is now a compliance tag on this node. Workloads already running here keep running until they restart",
			"Clinic requires hipaa, and no other node carries it, so its workloads cannot restart until one does",
		}})
	})
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("nodes", "tag", "worker-2", "--tags", "hipaa", "--compliance", "hipaa")

	require.Equal(t, ExitOK, code, stderr)
	require.Contains(t, stdout, "Updated worker-2")
	require.Contains(t, stderr, "warning: hipaa is now a compliance tag")
	require.Contains(t, stderr, "warning: Clinic requires hipaa")
	require.NotContains(t, stdout, "warning", "they are notes, so a program reading the output never sees them")
}

func TestNodeTaggingWithNoWarningsSaysNothingMore(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]any{"ok": true, "warnings": nil})
	})
	h.signedIn(t, "", "")

	_, _, stderr := h.run("nodes", "tag", "worker-2", "--region", "eu")

	require.Empty(t, stderr)
}

// ---- creating a service and deploying it ----

func TestCreatingAnAppWithDeployAsksTheAPIToDeployIt(t *testing.T) {
	var body map[string]any
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		reply(w, 200, map[string]any{"ok": true, "name": "web", "kind": "apps", "deployed": true})
	})
	h.signedIn(t, "shop", "production")

	code, stdout, stderr := h.run("apps", "create", "web", "--image", "nginx:alpine", "--deploy")

	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, map[string]any{"project": "shop", "environment": "production", "name": "web", "deploy": true, "source": "image", "image": "nginx:alpine"}, body,
		"deploy, and not draft: the API takes them as different asks")
	require.Contains(t, stdout, "started deploying it")
}

func TestCreatingAnAppWithoutDeployAsksForADraft(t *testing.T) {
	var body map[string]any
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		reply(w, 200, map[string]any{"ok": true})
	})
	h.signedIn(t, "shop", "production")

	code, stdout, _ := h.run("apps", "create", "web", "--image", "nginx:alpine")

	require.Equal(t, ExitOK, code)
	require.Equal(t, true, body["draft"])
	require.NotContains(t, body, "deploy")
	require.Contains(t, stdout, "draft")
}

// ---- what the help says ----

func TestTheHelpDoesNotPromiseJSONFromEveryCommand(t *testing.T) {
	h := newHarness(t, func(http.ResponseWriter, *http.Request) {})

	_, stdout, _ := h.run("--help")

	require.NotContains(t, stdout, "Every command takes --json")
	require.Contains(t, stdout, "Commands that print data")
	require.Contains(t, stdout, "report on standard error")
}

func TestTheVariablesHelpSaysPlainConfigurationIsNotHidden(t *testing.T) {
	h := newHarness(t, func(http.ResponseWriter, *http.Request) {})

	_, stdout, _ := h.run("env", "--help")

	require.NotContains(t, stdout, "Values are never shown")
	require.Contains(t, stdout, "Secret values are never returned")
	require.Contains(t, stdout, "Plain configuration is not secret")
}

func TestListingVariablesAsJSONCarriesPlainConfigurationValues(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		reply(w, 200, map[string]any{"secrets": []string{"STRIPE_KEY"}, "config": map[string]any{"LOG_LEVEL": "debug"}, "env": []map[string]any{}})
	})
	h.signedIn(t, "", "")

	_, stdout, _ := h.run("--json", "env", "list", "web")

	require.Contains(t, stdout, `"LOG_LEVEL": "debug"`, "plain configuration is not secret, and the help says so")
	require.NotContains(t, stdout, "sk_")
}
