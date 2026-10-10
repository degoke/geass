package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/remotecommand"
	utilexec "k8s.io/client-go/util/exec"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// The terminal protocol between the dashboard and the browser:
//
//	browser -> server  binary frame: keystrokes (stdin)
//	browser -> server  text frame:   {"type":"resize","cols":120,"rows":32}
//	server  -> browser binary frame: terminal output (stdout, stderr merged by the TTY)
//	server  -> browser text frame:   {"type":"exit","error":"..."} just before the socket closes
const (
	consoleWSPingInterval = 25 * time.Second
	consoleWSPongWait     = 70 * time.Second
	consoleWSWriteWait    = 10 * time.Second
	consoleWSMaxMessage   = 1 << 20
	consoleDefaultCols    = 100
	consoleDefaultRows    = 30
)

// consoleTarget is the container a terminal attaches to.
type consoleTarget struct {
	Namespace string
	Pod       string
	Container string
	Command   []string
}

// consoleStreams are the TTY streams of one attached terminal.
type consoleStreams struct {
	Stdin  io.Reader
	Stdout io.Writer
	Sizes  remotecommand.TerminalSizeQueue
}

// consoleExecFunc runs an interactive TTY command until it exits or ctx ends.
type consoleExecFunc func(ctx context.Context, target consoleTarget, streams consoleStreams) error

var consoleUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	// Browsers always send Origin on WebSocket upgrades. Reuse the dashboard's same-origin rule so
	// another site cannot open a shell with the visitor's session cookie.
	CheckOrigin: sameOriginMutation,
}

// activeConsoleSessions allows one attached terminal per audited console session.
var activeConsoleSessions sync.Map

// execInPod attaches an interactive TTY to a pod container with the Kubernetes exec API.
func (s *Server) execInPod(ctx context.Context, target consoleTarget, streams consoleStreams) error {
	request := s.Kube.CoreV1().RESTClient().Post().Resource("pods").Name(target.Pod).Namespace(target.Namespace).SubResource("exec")
	for _, part := range target.Command {
		request.Param("command", part)
	}
	request.Param("container", target.Container).Param("stdin", "true").Param("stdout", "true").Param("stderr", "false").Param("tty", "true")
	executor, err := remotecommand.NewSPDYExecutor(s.Config, http.MethodPost, request.URL())
	if err != nil {
		return err
	}
	return executor.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:             streams.Stdin,
		Stdout:            streams.Stdout,
		Tty:               true,
		TerminalSizeQueue: streams.Sizes,
	})
}

// consoleShellCommand prefers bash when the image has it and falls back to sh.
func consoleShellCommand(command []string) []string {
	if len(command) == 1 && command[0] == "sh" {
		return []string{"sh", "-c", "command -v bash >/dev/null 2>&1 && exec bash -l || exec sh"}
	}
	return command
}

// sizeQueue delivers terminal resize events to the exec stream, newest last.
type sizeQueue struct {
	events chan remotecommand.TerminalSize
	done   <-chan struct{}
}

func newSizeQueue(done <-chan struct{}) *sizeQueue {
	return &sizeQueue{events: make(chan remotecommand.TerminalSize, 8), done: done}
}

func (q *sizeQueue) push(size remotecommand.TerminalSize) {
	for {
		select {
		case q.events <- size:
			return
		default:
			select {
			case <-q.events: // drop the oldest resize; only the latest matters
			default:
			}
		}
	}
}

// Next implements remotecommand.TerminalSizeQueue.
func (q *sizeQueue) Next() *remotecommand.TerminalSize {
	select {
	case size := <-q.events:
		return &size
	case <-q.done:
		return nil
	}
}

// wsOutput writes terminal output to the socket as binary frames.
type wsOutput struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (o *wsOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	_ = o.conn.SetWriteDeadline(time.Now().Add(consoleWSWriteWait))
	if err := o.conn.WriteMessage(websocket.BinaryMessage, append([]byte(nil), p...)); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (o *wsOutput) writeText(payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	_ = o.conn.SetWriteDeadline(time.Now().Add(consoleWSWriteWait))
	_ = o.conn.WriteMessage(websocket.TextMessage, data)
}

func clampTerminalSize(cols, rows int) (uint16, uint16) {
	if cols < 2 || cols > 1000 {
		cols = consoleDefaultCols
	}
	if rows < 2 || rows > 500 {
		rows = consoleDefaultRows
	}
	return uint16(cols), uint16(rows)
}

// handleAppConsoleWS serves GET /api/apps/{name}/console/ws?session=<console session>.
func (s *Server) handleAppConsoleWS(w http.ResponseWriter, r *http.Request, name string) {
	exec := s.consoleExec
	if exec == nil {
		if s.Kube == nil || s.Config == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{dashboardLiteralError: "Kubernetes exec is not configured"})
			return
		}
		exec = s.execInPod
	}
	if !sameOriginMutation(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{dashboardLiteralError: "request origin could not be verified"})
		return
	}
	sessionName := strings.TrimSpace(r.URL.Query().Get("session"))
	session := &geassv1alpha1.GeassConsoleSession{}
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: sessionName, Namespace: systemNamespace}, session); err != nil || session.Spec.App != name {
		writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: "console session not found"})
		return
	}
	if session.Status.Phase != geassv1alpha1.GeassConsoleActive {
		writeJSON(w, http.StatusGone, map[string]string{dashboardLiteralError: "console session has ended"})
		return
	}
	timeout := time.Duration(session.Spec.TimeoutSeconds) * time.Second
	if session.Status.StartedAt != nil {
		timeout = time.Until(session.Status.StartedAt.Add(timeout))
	}
	if timeout <= 0 {
		writeJSON(w, http.StatusGone, map[string]string{dashboardLiteralError: "console session has ended"})
		return
	}
	app, err := s.getApp(r, name)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: "app not found"})
		return
	}
	namespace, err := resourceNamespaceForApp(*app)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: "could not start console"})
		return
	}
	if _, busy := activeConsoleSessions.LoadOrStore(sessionName, struct{}{}); busy {
		writeJSON(w, http.StatusConflict, map[string]string{dashboardLiteralError: "this console session already has an open terminal"})
		return
	}
	defer activeConsoleSessions.Delete(sessionName)

	conn, err := consoleUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade already replied with an HTTP error.
	}
	defer func() { _ = conn.Close() }()

	logger := ctrllog.FromContext(r.Context())
	actor := session.Spec.Actor
	logger.Info("Console terminal opened", "app", name, "pod", session.Spec.Pod, "container", session.Spec.Container, "actor", actor, "session", sessionName)
	defer logger.Info("Console terminal closed", "app", name, "pod", session.Spec.Pod, "actor", actor, "session", sessionName)

	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), timeout)
	defer cancel()

	conn.SetReadLimit(consoleWSMaxMessage)
	_ = conn.SetReadDeadline(time.Now().Add(consoleWSPongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(consoleWSPongWait))
	})

	sizes := newSizeQueue(ctx.Done())
	cols, _ := strconv.Atoi(r.URL.Query().Get("cols"))
	rows, _ := strconv.Atoi(r.URL.Query().Get("rows"))
	initialCols, initialRows := clampTerminalSize(cols, rows)
	sizes.push(remotecommand.TerminalSize{Width: initialCols, Height: initialRows})

	stdinReader, stdinWriter := io.Pipe()
	output := &wsOutput{conn: conn}

	// Browser -> pod: keystrokes and resize events.
	go func() {
		defer cancel()
		defer func() { _ = stdinWriter.Close() }()
		for {
			messageType, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			_ = conn.SetReadDeadline(time.Now().Add(consoleWSPongWait))
			switch messageType {
			case websocket.BinaryMessage:
				if _, err := stdinWriter.Write(data); err != nil {
					return
				}
			case websocket.TextMessage:
				var control struct {
					Type string `json:"type"`
					Cols int    `json:"cols"`
					Rows int    `json:"rows"`
				}
				if json.Unmarshal(data, &control) == nil && control.Type == "resize" {
					c, rr := clampTerminalSize(control.Cols, control.Rows)
					sizes.push(remotecommand.TerminalSize{Width: c, Height: rr})
				}
			}
		}
	}()

	// Keep idle terminals alive through proxies and detect dead browsers.
	go func() {
		ticker := time.NewTicker(consoleWSPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(consoleWSWriteWait)); err != nil {
					cancel()
					return
				}
			}
		}
	}()

	target := consoleTarget{
		Namespace: namespace,
		Pod:       session.Spec.Pod,
		Container: session.Spec.Container,
		Command:   consoleShellCommand(session.Spec.Command),
	}
	execErr := exec(ctx, target, consoleStreams{Stdin: stdinReader, Stdout: output, Sizes: sizes})
	timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
	cancel()
	// Nothing reads stdin any more. Closing it wakes the goroutine that may be blocked writing keystrokes.
	_ = stdinReader.Close()

	exit := map[string]any{"type": "exit"}
	var exitStatus utilexec.ExitError
	switch {
	case timedOut:
		exit["error"] = "Session time limit reached"
	case errors.As(execErr, &exitStatus):
		exit["code"] = exitStatus.ExitStatus()
	case execErr != nil:
		exit["error"] = execErr.Error()
	}
	output.writeText(exit)
	_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(consoleWSWriteWait))

	s.closeConsoleSession(context.WithoutCancel(r.Context()), sessionName, "Terminal closed")
}

// closeConsoleSession records the end of a terminal on the audited session object.
func (s *Server) closeConsoleSession(ctx context.Context, name, reason string) {
	_ = retry.RetryOnConflict(writeRetry, func() error {
		session := &geassv1alpha1.GeassConsoleSession{}
		if err := s.Client.Get(ctx, client.ObjectKey{Name: name, Namespace: systemNamespace}, session); err != nil {
			return client.IgnoreNotFound(err)
		}
		if session.Status.Phase == geassv1alpha1.GeassConsoleClosed || session.Status.Phase == geassv1alpha1.GeassConsoleExpired {
			return nil
		}
		now := metav1.Now()
		session.Status.Phase = geassv1alpha1.GeassConsoleClosed
		session.Status.EndedAt = &now
		session.Status.Reason = reason
		return s.Client.Status().Update(ctx, session)
	})
}
