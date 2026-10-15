package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// limitsAPI plays a project's quota and network endpoints and keeps what was sent.
type limitsAPI struct {
	quota, network map[string]any
	posted         []string
	bodies         []map[string]any
}

func (l *limitsAPI) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if r.Method == http.MethodPost {
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			l.posted = append(l.posted, r.URL.Path)
			l.bodies = append(l.bodies, body)
			reply(w, 200, map[string]any{"ok": true})
			return
		}
		switch r.URL.Path {
		case "/api/v1/projects/shop/quota":
			reply(w, 200, l.quota)
		case "/api/v1/projects/shop/network":
			reply(w, 200, l.network)
		default:
			reply(w, 404, map[string]string{"error": "project not found", "code": "not_found"})
		}
	}
}

func TestQuotaShowListsTheLimitsAndWhatIsUsed(t *testing.T) {
	api := &limitsAPI{quota: map[string]any{
		"environment": map[string]any{"cpu": "4", "pods": 20},
		"project":     map[string]any{"cpu": "10", "memory": "32Gi"},
		"used":        map[string]any{"cpu": "3500m", "memory": "9Gi"},
	}}
	h := newHarness(t, api.handler(t))
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("projects", "quota", "show", "shop")

	require.Equal(t, ExitOK, code, stderr)
	require.Regexp(t, `cpu\s+4\s+10\s+3500m`, stdout)
	require.Regexp(t, `memory\s+-\s+32Gi\s+9Gi`, stdout)
	require.Regexp(t, `pods\s+20\s+-\s+-`, stdout)
}

func TestQuotaShowSaysSoWhenThereAreNoLimits(t *testing.T) {
	h := newHarness(t, (&limitsAPI{quota: map[string]any{}}).handler(t))
	h.signedIn(t, "", "")

	code, stdout, _ := h.run("project", "quota", "show", "shop")

	require.Equal(t, ExitOK, code)
	require.Contains(t, stdout, "shop has no limits")
}

func TestQuotaShowCanBeReadByAProgram(t *testing.T) {
	h := newHarness(t, (&limitsAPI{quota: map[string]any{"project": map[string]any{"cpu": "10"}}}).handler(t))
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("projects", "quota", "show", "shop", "--json")

	require.Equal(t, ExitOK, code, stderr)
	var decoded struct {
		Project struct {
			CPU string `json:"cpu"`
		} `json:"project"`
	}
	require.NoError(t, json.Unmarshal([]byte(stdout), &decoded), stdout)
	require.Equal(t, "10", decoded.Project.CPU)
}

func TestQuotaSetSendsOnlyTheFlagsGiven(t *testing.T) {
	api := &limitsAPI{quota: map[string]any{"project": map[string]any{"cpu": "8", "memory": "16Gi"}}}
	h := newHarness(t, api.handler(t))
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("projects", "quota", "set", "shop", "--project-cpu", "8", "--project-memory", "16Gi", "--memory", "")

	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, []string{"/api/v1/projects/shop/quota"}, api.posted)
	require.Equal(t, map[string]any{"projectCpu": "8", "projectMemory": "16Gi", "memory": ""}, api.bodies[0],
		"a limit not named is not sent, so it keeps its value, and one given empty is sent empty, which removes it")
	require.Contains(t, stdout, "16Gi", "it shows the limits as they are afterwards")
}

func TestQuotaSetWithNothingToSetIsAUsageMistake(t *testing.T) {
	api := &limitsAPI{quota: map[string]any{}}
	h := newHarness(t, api.handler(t))
	h.signedIn(t, "", "")

	code, _, stderr := h.run("projects", "quota", "set", "shop")

	require.Equal(t, ExitUsage, code)
	require.Contains(t, stderr, "name a limit")
	require.Empty(t, api.posted)
}

func TestQuotaClearSendsEveryLimitEmpty(t *testing.T) {
	api := &limitsAPI{quota: map[string]any{}}
	h := newHarness(t, api.handler(t))
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("projects", "quota", "clear", "shop")

	require.Equal(t, ExitOK, code, stderr)
	require.Len(t, api.bodies[0], 8)
	for name, value := range api.bodies[0] {
		require.Equal(t, "", value, name)
	}
	require.Contains(t, stdout, "shop has no limits")
}

func TestNetworkShowSaysWhoCanReachTheProject(t *testing.T) {
	h := newHarness(t, (&limitsAPI{network: map[string]any{"isolation": "Project", "allowFromProjects": []string{"billing"}, "blockNodeAddresses": true}}).handler(t))
	h.signedIn(t, "", "")

	code, stdout, stderr := h.run("projects", "network", "show", "shop")

	require.Equal(t, ExitOK, code, stderr)
	require.Contains(t, stdout, "only the project itself")
	require.Contains(t, stdout, "Also allowed in: billing")
	require.Contains(t, stdout, "Kept away from the cluster's machines: yes")
}

func TestNetworkSetChangesOnlyWhatWasNamed(t *testing.T) {
	api := &limitsAPI{network: map[string]any{"isolation": "Project", "allowFromProjects": []string{"billing"}, "blockNodeAddresses": true}}
	h := newHarness(t, api.handler(t))
	h.signedIn(t, "", "")

	code, _, stderr := h.run("projects", "network", "set", "shop", "--block-nodes=false")

	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, map[string]any{"isolation": "Project", "allowFromProjects": []any{"billing"}, "blockNodeAddresses": false}, api.bodies[0],
		"the rest of who may reach the project is read and sent back, and false is sent as false")
}

func TestNetworkSetCanOpenAndNarrowAProject(t *testing.T) {
	api := &limitsAPI{network: map[string]any{"isolation": "Project", "allowFromProjects": []string{}, "blockNodeAddresses": false}}
	h := newHarness(t, api.handler(t))
	h.signedIn(t, "", "")

	code, _, stderr := h.run("projects", "network", "set", "shop", "--isolation", "NONE", "--allow-from", "a,b")
	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, "None", api.bodies[0]["isolation"])
	require.Equal(t, []any{"a", "b"}, api.bodies[0]["allowFromProjects"])
	require.NotContains(t, api.bodies[0], "blockNodeAddresses", "a flag that was not given is not sent")

	code, _, _ = h.run("projects", "network", "set", "shop", "--isolation", "sideways")
	require.Equal(t, ExitUsage, code)
	code, stderr2 := func() (int, string) { c, _, e := h.run("projects", "network", "set", "shop"); return c, e }()
	require.Equal(t, ExitUsage, code)
	require.True(t, strings.Contains(stderr2, "name something to change"), stderr2)
}

func TestAMissingProjectHasTheNotFoundExitCode(t *testing.T) {
	h := newHarness(t, (&limitsAPI{}).handler(t))
	h.signedIn(t, "", "")
	code, _, stderr := h.run("projects", "quota", "show", "ghost")
	require.Equal(t, ExitNotFound, code)
	require.Contains(t, stderr, "project not found")
}
