package client

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// projectServer plays a project's network and quota endpoints and keeps what was posted to them.
type projectServer struct {
	network map[string]any
	quota   map[string]any
	posted  []map[string]any
}

func (p *projectServer) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if r.Method == http.MethodPost {
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			p.posted = append(p.posted, body)
		}
		switch {
		case r.URL.Path == "/api/v1/projects/shop/network" && r.Method == http.MethodGet:
			respond(w, 200, p.network)
		case r.URL.Path == "/api/v1/projects/shop/network":
			maps.Copy(p.network, body)
			respond(w, 200, map[string]any{"ok": true})
		case r.URL.Path == "/api/v1/projects/shop/quota" && r.Method == http.MethodGet:
			respond(w, 200, p.quota)
		case r.URL.Path == "/api/v1/projects/shop/quota":
			respond(w, 200, map[string]any{"ok": true})
		default:
			respond(w, 404, map[string]string{"error": "not found", "code": "not_found"})
		}
	}
}

func TestChangingOnePartOfTheNetworkSendsBackTheRest(t *testing.T) {
	p := &projectServer{network: map[string]any{"isolation": "Project", "allowFromProjects": []string{"billing"}, "blockNodeAddresses": true}}
	c := serve(t, p.handler(t))

	off := false
	after, err := c.SetProjectNetwork(context.Background(), "shop", NetworkChange{BlockNodeAddresses: &off})

	require.NoError(t, err)
	require.Len(t, p.posted, 1)
	require.Equal(t, map[string]any{"isolation": "Project", "allowFromProjects": []any{"billing"}, "blockNodeAddresses": false}, p.posted[0],
		"the API takes who may reach the project every time, so what was not changed is sent as it was; and false is sent, not left out")
	require.False(t, after.BlockNodeAddresses)
	require.Equal(t, []string{"billing"}, after.AllowFromProjects)
}

func TestTheNetworkCanBeOpenedAndNarrowedAgain(t *testing.T) {
	p := &projectServer{network: map[string]any{"isolation": "Project", "allowFromProjects": []string{}, "blockNodeAddresses": false}}
	c := serve(t, p.handler(t))

	none, allowed := "None", []string{"a", "b"}
	_, err := c.SetProjectNetwork(context.Background(), "shop", NetworkChange{Isolation: &none, AllowFromProjects: &allowed})

	require.NoError(t, err)
	require.Equal(t, map[string]any{"isolation": "None", "allowFromProjects": []any{"a", "b"}}, p.posted[0], "blocking is not mentioned, so it is left as it is")
}

func TestAQuotaIsReadWithWhatIsUsed(t *testing.T) {
	p := &projectServer{quota: map[string]any{
		"environment": map[string]any{"cpu": "4"},
		"project":     map[string]any{"cpu": "10", "memory": "32Gi", "pods": 60},
		"used":        map[string]any{"cpu": "3500m", "pods": 12},
	}}
	c := serve(t, p.handler(t))

	quota, err := c.ProjectQuota(context.Background(), "shop")

	require.NoError(t, err)
	require.Equal(t, QuotaLimits{CPU: "4"}, quota.Environment)
	require.Equal(t, QuotaLimits{CPU: "10", Memory: "32Gi", Pods: 60}, quota.Project)
	require.Equal(t, QuotaLimits{CPU: "3500m", Pods: 12}, quota.Used)
	require.False(t, quota.Environment.IsZero())
	require.True(t, ProjectQuota{}.Used.IsZero(), "a project with no limits has an empty answer")
}

func TestOnlyTheLimitsNamedAreSent(t *testing.T) {
	p := &projectServer{quota: map[string]any{}}
	c := serve(t, p.handler(t))

	_, err := c.SetProjectQuota(context.Background(), "shop", map[string]string{"projectCpu": "8", "memory": ""})

	require.NoError(t, err)
	require.Equal(t, map[string]any{"projectCpu": "8", "memory": ""}, p.posted[0],
		"a limit that is not named is not sent, which keeps it, and one sent empty is removed")
}

func TestALimitThatDoesNotExistIsRefusedBeforeAnythingIsSent(t *testing.T) {
	p := &projectServer{quota: map[string]any{}}
	c := serve(t, p.handler(t))

	_, err := c.SetProjectQuota(context.Background(), "shop", map[string]string{"gpu": "1"})

	require.ErrorContains(t, err, `"gpu" is not a limit`)
	require.Empty(t, p.posted)
}
