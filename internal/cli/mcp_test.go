package cli

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/degoke/geass/internal/mcpserver"
)

// bearerTransport adds the agent's Geass token to every request, as an agent host would.
type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(req)
}

func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())
	return address
}

func TestTheMCPServerOverHTTPServesEachAgentTheToolsItsTokenAllows(t *testing.T) {
	h := newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/me" {
			reply(w, 404, map[string]string{"error": "not found", "code": "not_found"})
			return
		}
		// The token decides who the agent is: one token is an admin, the other only observes.
		if strings.Contains(r.Header.Get("Authorization"), strings.Repeat("a", 40)) {
			reply(w, 200, map[string]any{"username": "token:admin", "role": "admin", "canMutate": true, "canReadSensitive": true})
			return
		}
		reply(w, 200, map[string]any{"username": "token:watcher", "role": "observer", "canReadSensitive": true})
	})
	address := freeAddress(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- h.app.serveMCPOverHTTP(ctx, h.server.URL, address, mcpserver.Options{Version: "test"}) }()
	require.Eventually(t, func() bool {
		conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
		}
		return err == nil
	}, 3*time.Second, 20*time.Millisecond)

	// No token, no entry.
	response, err := http.Post("http://"+address, "application/json", strings.NewReader("{}"))
	require.NoError(t, err)
	_ = response.Body.Close()
	require.Equal(t, http.StatusUnauthorized, response.StatusCode)

	toolsFor := func(token string) []string {
		transport := &mcp.StreamableClientTransport{Endpoint: "http://" + address, HTTPClient: &http.Client{Transport: bearerTransport{token}}}
		agent := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "1"}, nil)
		session, err := agent.Connect(context.Background(), transport, nil)
		require.NoError(t, err)
		defer func() { _ = session.Close() }()
		listed, err := session.ListTools(context.Background(), nil)
		require.NoError(t, err)
		names := make([]string, 0, len(listed.Tools))
		for _, tool := range listed.Tools {
			names = append(names, tool.Name)
		}
		return names
	}

	adminTools := toolsFor("gsw_00000000_" + strings.Repeat("a", 40))
	watcherTools := toolsFor("gsw_00000000_" + strings.Repeat("b", 40))
	require.Contains(t, adminTools, "deploy_app")
	require.Contains(t, watcherTools, "get_logs")
	require.NotContains(t, watcherTools, "deploy_app", "the same server, but this agent's token cannot deploy")

	cancel()
	require.NoError(t, <-done)
}

func TestTheMCPCommandNeedsALogin(t *testing.T) {
	h := newHarness(t, func(http.ResponseWriter, *http.Request) {})

	code, _, stderr := h.run("mcp")

	require.Equal(t, ExitDenied, code)
	require.Contains(t, stderr, "geass login")
}
