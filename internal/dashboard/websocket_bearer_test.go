package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/apitokens"
	"github.com/degoke/geass/pkg/platform"
)

// terminalServer serves the whole dashboard (its authentication included) over a real socket, with a fake TTY behind
// the terminal of the service "web".
func terminalServer(t *testing.T) (tokenFixture, *httptest.Server) {
	t.Helper()
	f := newTokenFixture(t)
	f.srv.consoleExec = (&echoShell{}).run
	require.NoError(t, f.srv.Client.Create(context.Background(), &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassAppSpec{Project: testProjectName, Environment: "production", Source: imageAppSource("nginx:alpine")},
	}))
	require.NoError(t, f.srv.Client.Create(context.Background(), activeConsoleSession("sess")))
	server := httptest.NewServer(f.handler)
	t.Cleanup(server.Close)
	return f, server
}

// dialTerminal opens the terminal as a browser can for a page that works with a token: the only thing it controls on a
// WebSocket besides the URL is the list of subprotocols it offers.
func dialTerminal(server *httptest.Server, protocols ...string) (*websocket.Conn, *http.Response, error) {
	dialer := websocket.Dialer{Subprotocols: protocols}
	return dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/apps/web/console/ws?session=sess", nil)
}

func TestATerminalOpensWithATokenOfferedAsASubprotocol(t *testing.T) {
	f, server := terminalServer(t)
	token := f.workspaceToken(t, apitokens.RoleAdmin)

	conn, response, err := dialTerminal(server, websocketProtocol, websocketBearerPrefix+token)
	require.NoError(t, err, "an admin token offered as a subprotocol should open the terminal")
	t.Cleanup(func() { _ = conn.Close() })
	require.Equal(t, websocketProtocol, conn.Subprotocol(), "the answer must not repeat the token, only the protocol the terminal speaks")
	require.NotContains(t, response.Header.Get("Sec-WebSocket-Protocol"), token)

	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, []byte("hi\n")))
	_, data := readFrame(t, conn)
	require.Equal(t, "HI\n", string(data))
}

func TestATerminalIsRefusedToATokenThatMayNotHaveOne(t *testing.T) {
	f, server := terminalServer(t)
	for name, token := range map[string]string{
		"a viewer":         f.workspaceToken(t, apitokens.RoleViewer),
		"a project token":  f.projectToken(t, "production"),
		"a made-up token":  "gsw_nothing_here",
		"an empty one":     "",
		"another's secret": "gsh_deploy_hook_secret",
	} {
		t.Run(name, func(t *testing.T) {
			_, response, err := dialTerminal(server, websocketProtocol, websocketBearerPrefix+token)
			require.Error(t, err, "the terminal must stay shut: it is a shell in the service's container")
			require.NotNil(t, response)
			require.Contains(t, []int{http.StatusUnauthorized, http.StatusForbidden}, response.StatusCode)
		})
	}
}

func TestATokenInASubprotocolOpensNothingButATerminal(t *testing.T) {
	f, _ := terminalServer(t)
	token := f.workspaceToken(t, apitokens.RoleAdmin)

	// The credential is read only from the upgrade of a terminal. A request that merely carries the header, or that is
	// not for a terminal, finds no one signed in.
	for name, req := range map[string]*http.Request{
		"a plain request": httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil),
		"an upgrade to another path": func() *http.Request {
			r := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)
			r.Header.Set("Connection", "Upgrade")
			r.Header.Set("Upgrade", "websocket")
			return r
		}(),
		"a terminal's path without an upgrade": httptest.NewRequest(http.MethodGet, "/api/apps/web/console/ws?session=sess", nil),
	} {
		t.Run(name, func(t *testing.T) {
			req.Header.Set("Sec-WebSocket-Protocol", websocketProtocol+", "+websocketBearerPrefix+token)
			rec := f.do(req)
			require.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
		})
	}
}

func TestAnAuthorizationHeaderStillWinsOverASubprotocol(t *testing.T) {
	f, _ := terminalServer(t)
	viewer := f.workspaceToken(t, apitokens.RoleViewer)
	admin := f.workspaceToken(t, apitokens.RoleAdmin)

	req := bearer(viewer, http.MethodGet, "/api/apps/web/console/ws?session=sess", nil)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Protocol", websocketProtocol+", "+websocketBearerPrefix+admin)
	require.Equal(t, http.StatusForbidden, f.do(req).Code, "the token of the header is the one that counts, so a viewer cannot borrow an admin's")
}

func TestWebsocketBearerParsing(t *testing.T) {
	upgrade := func(protocols string) *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/api/apps/web/console/ws", nil)
		req.Header.Set("Connection", "Upgrade")
		req.Header.Set("Upgrade", "websocket")
		if protocols != "" {
			req.Header.Set("Sec-WebSocket-Protocol", protocols)
		}
		return req
	}
	for protocols, want := range map[string]struct {
		token     string
		presented bool
	}{
		"geass.v1, geass.bearer.gsw_abc":  {"gsw_abc", true},
		"geass.bearer.gsw_abc,geass.v1":   {"gsw_abc", true},
		"geass.bearer.":                   {"", true},
		"geass.v1":                        {"", false},
		"graphql-ws":                      {"", false},
		"":                                {"", false},
		"xgeass.bearer.gsw_abc, geass.v1": {"", false},
	} {
		token, presented := bearerToken(upgrade(protocols))
		require.Equal(t, want.presented, presented, "%q", protocols)
		require.Equal(t, want.token, token, "%q", protocols)
	}
}

func TestATerminalOpenedWithATokenEndsWhenTheTokenIsRevoked(t *testing.T) {
	quickRecheck(t)
	f := newTokenFixture(t)
	// A shell that stops when its context does, as the real exec does.
	f.srv.consoleExec = func(ctx context.Context, _ consoleTarget, streams consoleStreams) error {
		done := make(chan struct{})
		go func() {
			defer close(done)
			buf := make([]byte, 256)
			for {
				if _, err := streams.Stdin.Read(buf); err != nil {
					return
				}
			}
		}()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
			return nil
		}
	}
	require.NoError(t, f.srv.Client.Create(context.Background(), &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassAppSpec{Project: testProjectName, Environment: "production", Source: imageAppSource("nginx:alpine")},
	}))
	require.NoError(t, f.srv.Client.Create(context.Background(), activeConsoleSession("sess")))
	server := httptest.NewServer(f.handler)
	t.Cleanup(server.Close)
	token := f.workspaceToken(t, apitokens.RoleAdmin)

	conn, response, err := dialTerminal(server, websocketProtocol, websocketBearerPrefix+token)
	require.NoError(t, err)
	_ = response.Body.Close()
	t.Cleanup(func() { _ = conn.Close() })

	records, err := f.srv.listAPITokens(context.Background())
	require.NoError(t, err)
	require.Len(t, records, 1)
	for id := range records {
		rec := f.asAdmin(t, postForm("/api/tokens/revoke", url.Values{"id": {id}}))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	}

	deadline := time.Now().Add(3 * time.Second)
	var exit map[string]any
	for time.Now().Before(deadline) {
		require.NoError(t, conn.SetReadDeadline(deadline))
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var frame map[string]any
		if messageType == websocket.TextMessage && json.Unmarshal(data, &frame) == nil && frame["type"] == "exit" {
			exit = frame
			break
		}
	}
	require.NotNil(t, exit, "the terminal stayed open after the token that opened it was revoked")
}
