package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/remotecommand"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func activeConsoleSession(name string) *geassv1alpha1.GeassConsoleSession {
	started := metav1.NewTime(time.Now())
	return &geassv1alpha1.GeassConsoleSession{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassConsoleSessionSpec{
			App: "web", Project: testProjectName, Environment: "production",
			Pod: "web-abc-def", Container: "app", Actor: "admin", Command: []string{"sh"}, TimeoutSeconds: 300,
		},
		Status: geassv1alpha1.GeassConsoleSessionStatus{Phase: geassv1alpha1.GeassConsoleActive, StartedAt: &started},
	}
}

// consoleWSServer serves the terminal endpoint with an admin session and a fake TTY.
func consoleWSServer(t *testing.T, exec consoleExecFunc, session *geassv1alpha1.GeassConsoleSession) (*Server, *httptest.Server) {
	t.Helper()
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassAppSpec{Project: testProjectName, Environment: "production", Source: imageAppSource("nginx:alpine")},
	}
	objects := []client.Object{app}
	if session != nil {
		objects = append(objects, session)
	}
	srv := &Server{Client: newFakeClient(objects...), consoleExec: exec}
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv.handleAppConsoleWS(w, withAdminSession(t, srv, r), "web")
	}))
	t.Cleanup(httpServer.Close)
	return srv, httpServer
}

func dialConsole(t *testing.T, httpServer *httptest.Server, query, origin string) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	endpoint := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/api/apps/web/console/ws?" + query
	header := http.Header{}
	if origin != "" {
		header.Set("Origin", origin)
	}
	return websocket.DefaultDialer.Dial(endpoint, header)
}

// echoShell upper-cases whatever is typed and records the sizes it was given.
type echoShell struct {
	mu    sync.Mutex
	sizes []remotecommand.TerminalSize
	exit  error
}

func (e *echoShell) run(ctx context.Context, target consoleTarget, streams consoleStreams) error {
	go func() {
		for {
			size := streams.Sizes.Next()
			if size == nil {
				return
			}
			e.mu.Lock()
			e.sizes = append(e.sizes, *size)
			e.mu.Unlock()
		}
	}()
	buf := make([]byte, 256)
	for {
		n, err := streams.Stdin.Read(buf)
		if n > 0 {
			line := string(buf[:n])
			if strings.TrimSpace(line) == "exit" {
				return e.exit
			}
			_, _ = streams.Stdout.Write([]byte(strings.ToUpper(line)))
		}
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
}

func (e *echoShell) seenSizes() []remotecommand.TerminalSize {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]remotecommand.TerminalSize(nil), e.sizes...)
}

func readFrame(t *testing.T, conn *websocket.Conn) (int, []byte) {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
	messageType, data, err := conn.ReadMessage()
	require.NoError(t, err)
	return messageType, data
}

func TestConsoleTerminalRelaysInputOutputAndResizes(t *testing.T) {
	shell := &echoShell{}
	srv, httpServer := consoleWSServer(t, shell.run, activeConsoleSession("web-console-1"))

	conn, _, err := dialConsole(t, httpServer, "session=web-console-1&cols=132&rows=40", httpServer.URL)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, []byte("hello\n")))
	messageType, data := readFrame(t, conn)
	require.Equal(t, websocket.BinaryMessage, messageType)
	require.Equal(t, "HELLO\n", string(data))

	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"resize","cols":90,"rows":25}`)))
	require.Eventually(t, func() bool { return len(shell.seenSizes()) >= 2 }, 3*time.Second, 20*time.Millisecond)
	sizes := shell.seenSizes()
	require.Equal(t, remotecommand.TerminalSize{Width: 132, Height: 40}, sizes[0], "the size in the URL is the initial TTY size")
	require.Equal(t, remotecommand.TerminalSize{Width: 90, Height: 25}, sizes[1])

	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, []byte("exit\n")))
	messageType, data = readFrame(t, conn)
	require.Equal(t, websocket.TextMessage, messageType)
	var exit map[string]any
	require.NoError(t, json.Unmarshal(data, &exit))
	require.Equal(t, "exit", exit["type"])
	require.NotContains(t, exit, "error")

	require.Eventually(t, func() bool {
		var session geassv1alpha1.GeassConsoleSession
		if err := srv.Client.Get(context.Background(), client.ObjectKey{Name: "web-console-1", Namespace: platform.SystemNamespace}, &session); err != nil {
			return false
		}
		return session.Status.Phase == geassv1alpha1.GeassConsoleClosed && session.Status.EndedAt != nil
	}, 3*time.Second, 20*time.Millisecond, "the audited session records that the terminal closed")
}

func TestConsoleTerminalReportsShellExitStatusAndErrors(t *testing.T) {
	cases := map[string]struct {
		exit    error
		wantErr string
	}{
		"transport failure": {exit: errors.New("container not found"), wantErr: "container not found"},
		"clean exit":        {exit: nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			shell := &echoShell{exit: tc.exit}
			_, httpServer := consoleWSServer(t, shell.run, activeConsoleSession("web-console-2"))
			conn, _, err := dialConsole(t, httpServer, "session=web-console-2", httpServer.URL)
			require.NoError(t, err)
			defer func() { _ = conn.Close() }()
			require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, []byte("exit\n")))
			_, data := readFrame(t, conn)
			var exit map[string]any
			require.NoError(t, json.Unmarshal(data, &exit))
			if tc.wantErr != "" {
				require.Equal(t, tc.wantErr, exit["error"])
			} else {
				require.NotContains(t, exit, "error")
			}
		})
	}
}

func TestConsoleTerminalRejectsBadRequestsBeforeUpgrading(t *testing.T) {
	shell := &echoShell{}
	expired := activeConsoleSession("web-console-expired")
	expired.Status.Phase = geassv1alpha1.GeassConsoleExpired
	_, httpServer := consoleWSServer(t, shell.run, activeConsoleSession("web-console-3"))
	_, expiredServer := consoleWSServer(t, shell.run, expired)

	_, resp, err := dialConsole(t, httpServer, "session=web-console-3", "https://evil.example")
	require.Error(t, err)
	require.Equal(t, http.StatusForbidden, resp.StatusCode, "a page on another origin must not be able to open a shell")

	_, resp, err = dialConsole(t, httpServer, "session=web-console-3", "")
	require.Error(t, err)
	require.Equal(t, http.StatusForbidden, resp.StatusCode, "an upgrade without an Origin header is refused")

	_, resp, err = dialConsole(t, httpServer, "session=nope", httpServer.URL)
	require.Error(t, err)
	require.Equal(t, http.StatusNotFound, resp.StatusCode)

	_, resp, err = dialConsole(t, expiredServer, "session=web-console-expired", expiredServer.URL)
	require.Error(t, err)
	require.Equal(t, http.StatusGone, resp.StatusCode)
}

func TestConsoleSessionAllowsOneTerminalAtATime(t *testing.T) {
	shell := &echoShell{}
	_, httpServer := consoleWSServer(t, shell.run, activeConsoleSession("web-console-4"))

	first, _, err := dialConsole(t, httpServer, "session=web-console-4", httpServer.URL)
	require.NoError(t, err)
	defer func() { _ = first.Close() }()
	require.NoError(t, first.WriteMessage(websocket.BinaryMessage, []byte("ping\n")))
	_, data := readFrame(t, first)
	require.Equal(t, "PING\n", string(data))

	_, resp, err := dialConsole(t, httpServer, "session=web-console-4", httpServer.URL)
	require.Error(t, err)
	require.Equal(t, http.StatusConflict, resp.StatusCode)

	require.NoError(t, first.Close())
	require.Eventually(t, func() bool {
		_, resp, err := dialConsole(t, httpServer, "session=web-console-4", httpServer.URL)
		return err != nil && resp != nil && resp.StatusCode == http.StatusGone
	}, 3*time.Second, 50*time.Millisecond, "closing the terminal ends the audited session, so it cannot be attached again")
}

func TestConsoleIsUnavailableWithoutKubernetesExec(t *testing.T) {
	srv := &Server{Client: newFakeClient()}
	rec := httptest.NewRecorder()
	req := withAdminSession(t, srv, httptest.NewRequest(http.MethodGet, "/api/apps/web/console/ws?session=x", nil))
	srv.handleAppConsoleWS(rec, req, "web")
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
}

func TestViewersCannotOpenTerminals(t *testing.T) {
	require.True(t, dashboardSensitiveRead("/api/apps/web/console/ws"), "the terminal route is restricted to roles that can mutate")
}

func TestConsoleShellPrefersBashAndKeepsExplicitCommands(t *testing.T) {
	require.Equal(t, []string{"sh", "-c", "command -v bash >/dev/null 2>&1 && exec bash -l || exec sh"}, consoleShellCommand([]string{"sh"}))
	require.Equal(t, []string{"python3"}, consoleShellCommand([]string{"python3"}))
}

func TestSizeQueueKeepsTheLatestResize(t *testing.T) {
	done := make(chan struct{})
	queue := newSizeQueue(done)
	for i := 1; i <= 20; i++ {
		queue.push(remotecommand.TerminalSize{Width: uint16(i), Height: 10})
	}
	var last remotecommand.TerminalSize
	for len(queue.events) > 0 {
		last = *queue.Next()
	}
	require.Equal(t, uint16(20), last.Width, "a burst of resizes must end on the newest size")
	close(done)
	require.Nil(t, queue.Next(), "the queue ends when the terminal context ends")
}

// When the shell has exited, keystrokes still in flight must not leave the browser-to-pod goroutine
// blocked on a pipe nobody reads.
func TestConsoleClosesStdinWhenTheShellExits(t *testing.T) {
	stdinSeen := make(chan io.Reader, 1)
	release := make(chan struct{})
	exec := func(ctx context.Context, target consoleTarget, streams consoleStreams) error {
		stdinSeen <- streams.Stdin
		<-release // the shell is running but not reading stdin
		return nil
	}
	_, httpServer := consoleWSServer(t, exec, activeConsoleSession("web-console-1"))
	conn, _, err := dialConsole(t, httpServer, "session=web-console-1", httpServer.URL)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	stdin := <-stdinSeen

	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, []byte("x")))
	time.Sleep(100 * time.Millisecond) // let the server's reader block writing "x" into the unread pipe
	close(release)
	// The exit message is written after the server has finished with the shell's streams.
	messageType, _ := readFrame(t, conn)
	require.Equal(t, websocket.TextMessage, messageType)

	result := make(chan error, 1)
	go func() {
		_, readErr := stdin.Read(make([]byte, 8))
		result <- readErr
	}()
	select {
	case readErr := <-result:
		require.ErrorIs(t, readErr, io.ErrClosedPipe, "stdin is closed once the shell is gone, not left holding a pending write")
	case <-time.After(5 * time.Second):
		t.Fatal("stdin was never closed")
	}
}
