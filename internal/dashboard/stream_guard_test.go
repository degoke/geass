package dashboard

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/apitokens"
	"github.com/degoke/geass/pkg/platform"
)

// onThisMachine is a session cookie as the browser sends it to a dashboard on 127.0.0.1 over http: there the cookie is not
// Secure, so it has the plain name. The helpers that make cookies talk to example.com, where it has the "__Host-" one.
func onThisMachine(c *http.Cookie) *http.Cookie {
	return &http.Cookie{Name: platform.DashboardSessionCookie, Value: c.Value}
}

// overrideVar sets a package-level setting for one test and puts back what it was, whatever that was.
func overrideVar[T any](t *testing.T, target *T, value T) {
	t.Helper()
	old := *target
	*target = value
	t.Cleanup(func() { *target = old })
}

func quickRecheck(t *testing.T) {
	t.Helper()
	overrideVar(t, &streamAuthRecheck, 40*time.Millisecond)
}

// liveStream is a stream of the dashboard's events that is open against a real server.
type liveStream struct {
	response *http.Response
	ended    chan struct{}
}

// openEvents opens /api/events the way the browser does, waits for the first event, and watches for the stream ending.
func openEvents(t *testing.T, server *httptest.Server, setup func(*http.Request)) *liveStream {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, server.URL+"/api/events", nil)
	require.NoError(t, err)
	setup(req)
	response, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode)
	t.Cleanup(func() { _ = response.Body.Close() })
	reader := bufio.NewReader(response.Body)
	line, err := reader.ReadString('\n')
	require.NoError(t, err)
	require.Contains(t, line, "connected")
	stream := &liveStream{response: response, ended: make(chan struct{})}
	go func() {
		_, _ = io.Copy(io.Discard, reader)
		close(stream.ended)
	}()
	return stream
}

func (s *liveStream) endsWithin(d time.Duration) bool {
	select {
	case <-s.ended:
		return true
	case <-time.After(d):
		return false
	}
}

func cookieOf(t *testing.T, srv *Server, username, role string) *http.Cookie {
	t.Helper()
	req := withDashboardSession(t, srv, httptest.NewRequest(http.MethodGet, "/", nil), username, role)
	cookie, err := req.Cookie(testSessionCookie)
	require.NoError(t, err)
	return cookie
}

func TestAStreamIsEndedWhenThePersonWhoOpenedItSignsOut(t *testing.T) {
	quickRecheck(t)
	f := newTokenFixture(t)
	server := httptest.NewServer(f.handler)
	t.Cleanup(server.Close)
	cookie := cookieOf(t, f.srv, "admin", dashboardRoleAdmin)
	stream := openEvents(t, server, func(r *http.Request) { r.AddCookie(onThisMachine(cookie)) })

	require.False(t, stream.endsWithin(300*time.Millisecond), "a stream whose credentials still stand was ended")

	out := postForm("/api/logout", nil)
	out.AddCookie(cookie)
	require.Equal(t, http.StatusOK, f.do(out).Code)

	require.True(t, stream.endsWithin(3*time.Second), "a stream stayed open after the person who opened it signed out")
}

func TestAStreamIsEndedWhenItsTokenIsRevoked(t *testing.T) {
	quickRecheck(t)
	f := newTokenFixture(t)
	server := httptest.NewServer(f.handler)
	t.Cleanup(server.Close)
	token := f.workspaceToken(t, apitokens.RoleViewer)
	stream := openEvents(t, server, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) })
	require.False(t, stream.endsWithin(300*time.Millisecond))

	records, err := f.srv.listAPITokens(context.Background())
	require.NoError(t, err)
	require.Len(t, records, 1)
	for id := range records {
		rec := f.asAdmin(t, postForm("/api/tokens/revoke", url.Values{"id": {id}}))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	}

	require.True(t, stream.endsWithin(3*time.Second), "a stream stayed open after its token was revoked")
}

func TestAStreamIsEndedWhenThePersonIsDemoted(t *testing.T) {
	quickRecheck(t)
	f := newTokenFixture(t)
	server := httptest.NewServer(f.handler)
	t.Cleanup(server.Close)
	cookie := cookieOf(t, f.srv, "admin", dashboardRoleAdmin)
	stream := openEvents(t, server, func(r *http.Request) { r.AddCookie(onThisMachine(cookie)) })
	require.False(t, stream.endsWithin(300*time.Millisecond))

	secret := &corev1.Secret{}
	key := client.ObjectKey{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace}
	require.NoError(t, f.srv.Client.Get(context.Background(), key, secret))
	var users []dashboardUser
	require.NoError(t, json.Unmarshal(secret.Data["users"], &users))
	for i := range users {
		if users[i].Username == "admin" {
			users[i].Role = dashboardRoleViewer
		}
	}
	secret.Data["users"], _ = json.Marshal(users)
	require.NoError(t, f.srv.Client.Update(context.Background(), secret))

	require.True(t, stream.endsWithin(3*time.Second), "a stream stayed open with the rights of a role the person no longer has")
}

func TestAStreamOfAPersonFromTheProviderEndsWhenTheProviderSignsThemOut(t *testing.T) {
	quickRecheck(t)
	f := newSSOFixture(t, platform.AuthModeExternal)
	server := httptest.NewServer(f.handler)
	t.Cleanup(server.Close)
	cookie := f.signedInAs(t, testPerson("u-alice", "alice@example.com", "owner"))
	stream := openEvents(t, server, func(r *http.Request) { r.AddCookie(onThisMachine(cookie)) })
	require.False(t, stream.endsWithin(300*time.Millisecond))

	require.Equal(t, http.StatusOK, f.backchannel(f.idp.logoutToken("u-alice", nil), "198.51.100.7:4000").Code)

	require.True(t, stream.endsWithin(3*time.Second))
}

func TestAStreamIsNotEndedByAReadThatFails(t *testing.T) {
	quickRecheck(t)
	f := newSSOFixture(t, platform.AuthModeLocal)
	secrets := &unreadableSecrets{Client: f.srv.Client}
	f.srv.Client = secrets
	server := httptest.NewServer(f.handler)
	t.Cleanup(server.Close)
	cookie := cookieOf(t, f.srv, "admin", dashboardRoleAdmin)
	stream := openEvents(t, server, func(r *http.Request) { r.AddCookie(onThisMachine(cookie)) })

	// The users Secret cannot be read for a while: nothing can be told, so nothing is ended.
	secrets.fail.Store(true)
	require.False(t, stream.endsWithin(500*time.Millisecond), "a stream was ended because the API server did not answer")
}

// unreadableSecrets fails every read of a Secret once told to.
type unreadableSecrets struct {
	client.Client
	fail atomic.Bool
}

func (c *unreadableSecrets) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*corev1.Secret); ok && c.fail.Load() {
		return context.DeadlineExceeded
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func TestOnlyLongLivedPathsAreWatched(t *testing.T) {
	for path, watched := range map[string]bool{
		"/api/events": true, "/api/apps/web/console/ws": true, "/api/apps/web/console/stream": true,
		"/api/projects/p/logs/stream": true, "/api/v1/apps/web/logs/stream": true,
		"/api/apps": false, "/api/tokens": false, "/api/events/other": false, "/": false,
	} {
		require.Equal(t, watched, isStreamPath(path), path)
	}
}

func TestATerminalIsEndedWhenThePersonWhoOpenedItSignsOut(t *testing.T) {
	quickRecheck(t)
	// A shell that stops when its context does, as the real exec does (the shared fake only looks after a read returns).
	shell := func(ctx context.Context, _ consoleTarget, streams consoleStreams) error {
		done := make(chan struct{})
		go func() {
			defer close(done)
			buf := make([]byte, 256)
			for {
				n, err := streams.Stdin.Read(buf)
				if n > 0 {
					_, _ = streams.Stdout.Write([]byte(strings.ToUpper(string(buf[:n]))))
				}
				if err != nil {
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
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassAppSpec{Project: testProjectName, Environment: "production", Source: imageAppSource("nginx:alpine")},
	}
	srv := &Server{Client: newFakeClient(app, activeConsoleSession("sess")), consoleExec: shell}
	require.NoError(t, srv.Client.Create(context.Background(), dashboardUsersSecret(dashboardUser{Username: "admin", Password: "test-password", Role: dashboardRoleAdmin})))
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	cookie := cookieOf(t, srv, "admin", dashboardRoleAdmin)

	header := http.Header{}
	header.Set("Cookie", onThisMachine(cookie).String())
	header.Set("Origin", server.URL)
	conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/apps/web/console/ws?session=sess", header)
	require.NoError(t, err, "the terminal did not open")
	_ = response.Body.Close()
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, []byte("hi\n")))
	_, data := readFrame(t, conn)
	require.Equal(t, "HI\n", string(data), "the terminal should answer while the person is signed in")

	out := postForm("/api/logout", nil)
	out.AddCookie(cookie)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, out)
	require.Equal(t, http.StatusOK, rec.Code)

	deadline := time.Now().Add(3 * time.Second)
	var exit map[string]any
	for time.Now().Before(deadline) {
		require.NoError(t, conn.SetReadDeadline(deadline))
		messageType, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		if messageType == websocket.TextMessage {
			var frame map[string]any
			if json.Unmarshal(data, &frame) == nil && frame["type"] == "exit" {
				exit = frame
				break
			}
		}
	}
	require.NotNil(t, exit, "the terminal stayed open after the person who opened it signed out")
	require.Equal(t, "Your session ended", exit["error"])
}

// secretReads counts the reads of the users Secret.
type secretReads struct {
	client.Client
	reads atomic.Int32
}

func (c *secretReads) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*corev1.Secret); ok && key.Name == platform.DashboardAuthSecretName {
		c.reads.Add(1)
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func TestTheChecksOfOpenStreamsShareWhatTheyReadAndStillSeeChanges(t *testing.T) {
	overrideVar(t, &streamAuthRecheck, 400*time.Millisecond) // a snapshot lives for a quarter of it: 100ms
	f := newTokenFixture(t)
	reads := &secretReads{Client: f.srv.Client}
	f.srv.Client = reads
	cookie := cookieOf(t, f.srv, "admin", dashboardRoleAdmin)
	open := httptest.NewRequest(http.MethodGet, "/api/events", nil)
	open.AddCookie(cookie)
	was := streamCredentials{role: dashboardRoleAdmin}
	reads.reads.Store(0)

	for range 50 {
		require.True(t, f.srv.streamStillAllowed(open, was), "a stream whose person is signed in was ended")
	}
	require.Equal(t, int32(1), reads.reads.Load(), "50 checks of open streams read the users Secret more than once")

	time.Sleep(150 * time.Millisecond)
	require.True(t, f.srv.streamStillAllowed(open, was))
	require.Equal(t, int32(2), reads.reads.Load(), "a snapshot that was old was not read again")

	// A sign-out is seen by the next check once the snapshot has aged out.
	out := postForm("/api/logout", nil)
	out.AddCookie(cookie)
	require.Equal(t, http.StatusOK, f.do(out).Code)
	time.Sleep(150 * time.Millisecond)
	require.False(t, f.srv.streamStillAllowed(open, was), "a sign-out was hidden by the shared snapshot")
}

func TestATerminalIsRecordedAsEndedByTheSessionEndingAndNotAsClosed(t *testing.T) {
	quickRecheck(t)
	endedBy := func(signOut bool) string {
		shell := func(ctx context.Context, _ consoleTarget, streams consoleStreams) error {
			done := make(chan struct{})
			go func() {
				defer close(done)
				buf := make([]byte, 64)
				for {
					n, err := streams.Stdin.Read(buf)
					if n > 0 && strings.TrimSpace(string(buf[:n])) == "exit" {
						return
					}
					if err != nil {
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
		app := &geassv1alpha1.GeassApp{
			ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: platform.SystemNamespace},
			Spec:       geassv1alpha1.GeassAppSpec{Project: testProjectName, Environment: "production", Source: imageAppSource("nginx:alpine")},
		}
		srv := &Server{Client: newFakeClient(app, activeConsoleSession("sess")), consoleExec: shell}
		require.NoError(t, srv.Client.Create(context.Background(), dashboardUsersSecret(dashboardUser{Username: "admin", Password: "test-password", Role: dashboardRoleAdmin})))
		mux := http.NewServeMux()
		srv.registerRoutes(mux)
		server := httptest.NewServer(mux)
		defer server.Close()
		cookie := cookieOf(t, srv, "admin", dashboardRoleAdmin)
		header := http.Header{}
		header.Set("Cookie", onThisMachine(cookie).String())
		header.Set("Origin", server.URL)
		conn, response, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/api/apps/web/console/ws?session=sess", header)
		require.NoError(t, err)
		_ = response.Body.Close()
		defer func() { _ = conn.Close() }()

		if signOut {
			out := postForm("/api/logout", nil)
			out.AddCookie(cookie)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, out)
			require.Equal(t, http.StatusOK, rec.Code)
		} else {
			require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, []byte("exit\n")))
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			session := &geassv1alpha1.GeassConsoleSession{}
			require.NoError(t, srv.Client.Get(context.Background(), client.ObjectKey{Name: "sess", Namespace: platform.SystemNamespace}, session))
			if session.Status.Phase == geassv1alpha1.GeassConsoleClosed {
				return session.Status.Reason
			}
			time.Sleep(20 * time.Millisecond)
		}
		require.FailNow(t, "the console session was never recorded as closed")
		return ""
	}

	require.Equal(t, "Terminal closed", endedBy(false), "a terminal the person closed")
	require.Equal(t, "Session ended", endedBy(true), "a terminal that a revoked session ended is recorded as an ordinary close")
}
