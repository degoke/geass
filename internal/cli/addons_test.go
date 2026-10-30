package cli

import (
	"encoding/json"
	"maps"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// addonsAPI plays the add-ons endpoints and keeps what was sent. A write changes the add-on it names, so what is read back is
// what a real cluster would say.
type addonsAPI struct {
	addons []map[string]any
	posted []string
	bodies []map[string]any
	// readyAfter is how many reads of serverless say Installing before it says Ready.
	readyAfter int
	reads      int
}

func addonEntry(name, title, state string, fields map[string]any) map[string]any {
	entry := map[string]any{"name": name, "title": title, "enabled": state != "Off", "state": state, "installed": state != "Off", "canRetry": false, "canDisable": true}
	maps.Copy(entry, fields)
	return entry
}

func (a *addonsAPI) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			a.posted = append(a.posted, r.URL.Path)
			a.bodies = append(a.bodies, body)
			if r.URL.Path == "/api/v1/settings/addons/serverless/save" && body["enabled"] == true {
				a.addons[0] = addonEntry("serverless", "Sleep when idle", "Installing", map[string]any{"message": "Waiting to start the install."})
			}
			reply(w, 200, map[string]any{"ok": true})
			return
		}
		require.Equal(t, "/api/v1/settings/addons", r.URL.Path)
		a.reads++
		if a.addons[0]["enabled"] == true && a.reads > a.readyAfter {
			a.addons[0] = addonEntry("serverless", "Sleep when idle", "Ready", nil)
		}
		reply(w, 200, map[string]any{"addons": a.addons})
	}
}

func newAddonsAPI() *addonsAPI {
	return &addonsAPI{addons: []map[string]any{
		addonEntry("serverless", "Sleep when idle", "Off", nil),
		addonEntry("longhorn", "Replicated storage", "Ready", map[string]any{"canDisable": false}),
		addonEntry("monitoring", "Metrics", "Failed", map[string]any{"profile": "lite", "reason": "InstallSuspended", "message": "The install job is suspended.", "canRetry": true}),
		addonEntry("logging", "Logs", "Ready", map[string]any{"retentionDays": 7}),
	}}
}

func TestAddonsListShowsEachStateAndWhatToDoAboutAFailure(t *testing.T) {
	h := newHarness(t, newAddonsAPI().handler(t))
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("addons")

	require.Equal(t, ExitOK, code, stderr)
	require.Regexp(t, `serverless\s+Sleep when idle\s+off\s+Off`, stdout)
	require.Regexp(t, `longhorn\s+Replicated storage\s+on\s+Ready`, stdout)
	require.Regexp(t, `monitoring\s+Metrics\s+on\s+Failed\s+The install job is suspended\.`, stdout)
	require.Contains(t, stdout, "Run `geass addons retry monitoring`.", "a stuck add-on says how to get it going")
	require.Contains(t, stdout, "Logs kept 7 days.")
}

func TestAddonsEnableSendsOnlyEnabledAndWaitCanWatchItComeUp(t *testing.T) {
	api := newAddonsAPI()
	api.readyAfter = 2
	h := newHarness(t, api.handler(t))
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("addons", "enable", "serverless", "--wait", "--timeout", "30s")

	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, []string{"/api/v1/settings/addons/serverless/save"}, api.posted)
	require.Equal(t, map[string]any{"enabled": true}, api.bodies[0])
	require.Contains(t, stderr, "Installing: Waiting to start the install.")
	require.Contains(t, stdout, "Sleep when idle is ready.")
}

func TestAddonsDisableMetricsAsksFirstAndAScriptMustSayYes(t *testing.T) {
	api := newAddonsAPI()
	h := newHarness(t, api.handler(t))
	h.signedIn(t, "", "")

	code, _, stderr := h.run("addons", "disable", "monitoring")
	require.Equal(t, ExitUsage, code, stderr)
	require.Empty(t, api.posted, "nothing was sent before it was confirmed")

	code, _, stderr = h.run("addons", "disable", "monitoring", "--yes")
	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, map[string]any{"enabled": false}, api.bodies[0])
}

func TestAddonsDisableServerlessNeedsNoConfirmation(t *testing.T) {
	api := newAddonsAPI()
	h := newHarness(t, api.handler(t))
	h.signedIn(t, "", "")

	code, _, stderr := h.run("addons", "disable", "serverless")

	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, map[string]any{"enabled": false}, api.bodies[0])
}

func TestAddonsSetChangesAnOptionAndNothingElse(t *testing.T) {
	api := newAddonsAPI()
	h := newHarness(t, api.handler(t))
	h.signedIn(t, "", "")

	code, _, stderr := h.run("addons", "set", "logging", "--retention-days", "30")
	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, map[string]any{"retentionDays": float64(30)}, api.bodies[0], "enabled is not sent, so it stays as it is")

	code, _, stderr = h.run("addons", "set", "logging")
	require.Equal(t, ExitUsage, code)
	require.Contains(t, stderr, "name an option to change")
}

func TestAddonsRetrySendsTheRequest(t *testing.T) {
	api := newAddonsAPI()
	h := newHarness(t, api.handler(t))
	h.signedIn(t, "", "")

	code, _, stderr := h.run("addons", "retry", "monitoring")

	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, []string{"/api/v1/settings/addons/monitoring/retry"}, api.posted)
}

func TestAddonsCanBeReadByAProgram(t *testing.T) {
	h := newHarness(t, newAddonsAPI().handler(t))
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("addons", "list", "--json")

	require.Equal(t, ExitOK, code, stderr)
	var decoded struct {
		Addons []struct {
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"addons"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &decoded), stdout)
	require.Len(t, decoded.Addons, 4)
	require.Equal(t, "Failed", decoded.Addons[2].State)
}
