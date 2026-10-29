package mailrelay

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
	"github.com/stretchr/testify/require"

	"github.com/degoke/geass/pkg/platform"
)

// These tests run the relay as it runs in the cluster, on a loopback port, and talk to it with go-smtp's client, the way
// an organization's service would; behind it is a fake upstream, another go-smtp server in the test, that records what it
// is given and can be told to defer, refuse, hold a message or be down. No real mail service and no real mail program is
// involved.

// testStart is where the tests' clock starts: on a whole minute, so that each window's buckets start with it.
var testStart = time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// syncBuffer is a buffer the relay's log can be written to from every connection at once.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// upstreamMessage is what the fake upstream was given.
type upstreamMessage struct {
	from string
	to   []string
	data []byte
}

// fakeUpstream is the mail service behind the relay.
type fakeUpstream struct {
	listener net.Listener
	server   *smtp.Server

	mu       sync.Mutex
	messages []upstreamMessage
	// rcptErr, when it returns an error, refuses a recipient; dataErr refuses a message once it has been given.
	rcptErr func(to string) error
	dataErr error
	// hold, when set, keeps every message waiting until it is closed.
	hold chan struct{}
	// login, when set, is the login the upstream requires.
	login *UpstreamCredentials
}

func newFakeUpstream(t *testing.T, tlsConfig *tls.Config) *fakeUpstream {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	up := &fakeUpstream{listener: listener}
	up.server = smtp.NewServer(smtp.BackendFunc(func(*smtp.Conn) (smtp.Session, error) { return &upstreamSession{up: up}, nil }))
	up.server.Domain = "upstream.test"
	up.server.TLSConfig = tlsConfig
	up.server.AllowInsecureAuth = tlsConfig == nil
	up.server.ErrorLog = slogPrinter{log: slog.New(slog.DiscardHandler)}
	go func() { _ = up.server.Serve(listener) }()
	t.Cleanup(func() { _ = up.server.Close() })
	return up
}

func (u *fakeUpstream) port() int { return u.listener.Addr().(*net.TCPAddr).Port }

func (u *fakeUpstream) received() []upstreamMessage {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]upstreamMessage(nil), u.messages...)
}

func (u *fakeUpstream) set(change func(*fakeUpstream)) {
	u.mu.Lock()
	defer u.mu.Unlock()
	change(u)
}

// stop takes the upstream down: nothing answers on its port any more.
func (u *fakeUpstream) stop() { _ = u.server.Close() }

type upstreamSession struct {
	up     *fakeUpstream
	signed bool
	msg    upstreamMessage
}

func (s *upstreamSession) AuthMechanisms() []string { return []string{sasl.Plain} }

func (s *upstreamSession) Auth(string) (sasl.Server, error) {
	return sasl.NewPlainServer(func(_, username, password string) error {
		s.up.mu.Lock()
		defer s.up.mu.Unlock()
		if s.up.login == nil || username != s.up.login.Username || password != s.up.login.Password {
			return smtp.ErrAuthFailed
		}
		s.signed = true
		return nil
	}), nil
}

func (s *upstreamSession) Mail(from string, _ *smtp.MailOptions) error {
	s.up.mu.Lock()
	login := s.up.login
	s.up.mu.Unlock()
	if login != nil && !s.signed {
		return smtp.ErrAuthRequired
	}
	s.msg = upstreamMessage{from: from}
	return nil
}

func (s *upstreamSession) Rcpt(to string, _ *smtp.RcptOptions) error {
	s.up.mu.Lock()
	refuse := s.up.rcptErr
	s.up.mu.Unlock()
	if refuse != nil {
		if err := refuse(to); err != nil {
			return err
		}
	}
	s.msg.to = append(s.msg.to, to)
	return nil
}

func (s *upstreamSession) Data(r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.up.mu.Lock()
	hold, refuse := s.up.hold, s.up.dataErr
	s.up.mu.Unlock()
	if hold != nil {
		<-hold
	}
	if refuse != nil {
		return refuse
	}
	s.msg.data = data
	s.up.mu.Lock()
	s.up.messages = append(s.up.messages, s.msg)
	s.up.mu.Unlock()
	return nil
}

func (s *upstreamSession) Reset()        { s.msg = upstreamMessage{} }
func (s *upstreamSession) Logout() error { return nil }

// harness is a relay serving on a loopback port, with its fake upstream and its log.
type harness struct {
	relay    *Relay
	addr     string
	clock    *fakeClock
	logs     *syncBuffer
	upstream *fakeUpstream
	roots    *x509.CertPool
	config   *Config
}

// testCertificate is the relay's certificate, signed by an authority made for the test, and a pool of that authority.
func testCertificate(t *testing.T) (tls.Certificate, *x509.CertPool, []byte) {
	t.Helper()
	caCert, caKey, err := platform.NewMailRelayCA(time.Now())
	require.NoError(t, err)
	certPEM, keyPEM, err := platform.IssueMailRelayCertificate(caCert, caKey, time.Now())
	require.NoError(t, err)
	certificate, err := tls.X509KeyPair(certPEM, keyPEM)
	require.NoError(t, err)
	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(caCert))
	return certificate, roots, append(certPEM, keyPEM...)
}

// roomy are limits nothing in a test reaches unless it means to.
func roomy() Limits {
	return Limits{MessagesPerMinute: 1000, MessagesPerHour: 1000, MessagesPerDay: 1000, RecipientsPerMessage: 100, MaxMessageBytes: 1 << 20, Connections: 50}
}

func testOrg(name, password string, limits Limits) Organization {
	return Organization{Name: name, Username: name, Credentials: []Credential{{SHA256: PasswordDigest(password)}}, Limits: limits}
}

// newHarness starts a relay with the organizations and a fake upstream in plain text.
func newHarness(t *testing.T, orgs ...Organization) *harness {
	t.Helper()
	return newHarnessSending(t, nil, orgs...)
}

// newHarnessSending is newHarness with a sender of its own; nil is the relay's.
func newHarnessSending(t *testing.T, s sender, orgs ...Organization) *harness {
	t.Helper()
	upstream := newFakeUpstream(t, nil)
	h := &harness{clock: &fakeClock{now: testStart}, logs: &syncBuffer{}, upstream: upstream}
	h.relay = New(Options{Logger: slog.New(slog.NewJSONHandler(h.logs, nil)), Now: h.clock.Now, HeloName: "relay.test", sender: s})
	h.config = &Config{Upstream: Upstream{Host: "127.0.0.1", Port: upstream.port(), TLS: TLSNone}, Organizations: orgs}
	h.relay.SetConfig(h.config)
	certificate, roots, _ := testCertificate(t)
	h.relay.SetCertificate(&certificate)
	h.roots = roots
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := h.relay.NewServer(platform.MailRelayHost())
	go func() { _ = server.Serve(LimitConnections(listener, 64)) }()
	t.Cleanup(func() { _ = server.Close() })
	h.addr = listener.Addr().String()
	return h
}

// reconfigure gives the relay a changed configuration, as a reload would.
func (h *harness) reconfigure(change func(*Config)) {
	config := *h.config
	config.Organizations = append([]Organization(nil), h.config.Organizations...)
	change(&config)
	h.config = &config
	h.relay.SetConfig(&config)
}

// connect opens a connection and starts TLS on it, trusting the relay's authority, as a client given geass-mail does.
func (h *harness) connect(t *testing.T) *smtp.Client {
	t.Helper()
	conn, err := net.Dial("tcp", h.addr)
	require.NoError(t, err)
	client, err := smtp.NewClientStartTLS(conn, &tls.Config{RootCAs: h.roots, ServerName: platform.MailRelayHost(), MinVersion: tls.VersionTLS12})
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// signedIn is a connection signed in with a login.
func (h *harness) signedIn(t *testing.T, username, password string) *smtp.Client {
	t.Helper()
	client := h.connect(t)
	require.NoError(t, client.Auth(sasl.NewPlainClient("", username, password)))
	return client
}

// message is a message with a From header, with the lines ended as SMTP ends them.
func message(from, body string) string {
	return "From: " + from + "\r\nTo: someone@example.net\r\nSubject: test\r\n\r\n" + body + "\r\n"
}

// send sends one message on a connection, and resets the connection when it was refused, so that it can send another.
func send(client *smtp.Client, from string, to []string, data string) error {
	err := func() error {
		if err := client.Mail(from, nil); err != nil {
			return err
		}
		for _, recipient := range to {
			if err := client.Rcpt(recipient, nil); err != nil {
				return err
			}
		}
		writer, err := client.Data()
		if err != nil {
			return err
		}
		if _, err := io.WriteString(writer, data); err != nil {
			return err
		}
		return writer.Close()
	}()
	if err != nil {
		_ = client.Reset()
	}
	return err
}

// sendOne sends a plain message from an address at the organization's domain.
func sendOne(client *smtp.Client) error {
	return send(client, "app@acme.example", []string{"someone@example.net"}, message("app@acme.example", "hello"))
}

// requireReply checks that an error is the relay's reply with the code and enhanced code.
func requireReply(t *testing.T, err error, code int, enhanced smtp.EnhancedCode) {
	t.Helper()
	var reply *smtp.SMTPError
	require.True(t, errors.As(err, &reply), "a reply of the relay, not %v", err)
	require.Equal(t, code, reply.Code, "reply %d %v: %s", reply.Code, reply.EnhancedCode, reply.Message)
	require.Equal(t, enhanced, reply.EnhancedCode, "reply %d %v: %s", reply.Code, reply.EnhancedCode, reply.Message)
}

// rawSession speaks to the relay line by line, for what go-smtp's client will not do (sign in without TLS).
type rawSession struct {
	t    *testing.T
	conn net.Conn
	buf  []byte
}

func (h *harness) raw(t *testing.T) *rawSession {
	t.Helper()
	conn, err := net.Dial("tcp", h.addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	s := &rawSession{t: t, conn: conn}
	require.True(t, strings.HasPrefix(s.reply(), "220 "))
	return s
}

// reply reads one reply, every line of it.
func (s *rawSession) reply() string {
	s.t.Helper()
	_ = s.conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var lines []string
	for {
		line := s.line()
		lines = append(lines, line)
		if len(line) < 4 || line[3] != '-' {
			return strings.Join(lines, "\n")
		}
	}
}

func (s *rawSession) line() string {
	for {
		if i := bytes.IndexByte(s.buf, '\n'); i >= 0 {
			line := strings.TrimRight(string(s.buf[:i]), "\r")
			s.buf = s.buf[i+1:]
			return line
		}
		chunk := make([]byte, 4096)
		n, err := s.conn.Read(chunk)
		if n == 0 && err != nil {
			s.t.Fatalf("the relay closed the connection: %v", err)
		}
		s.buf = append(s.buf, chunk[:n]...)
	}
}

func (s *rawSession) say(line string) string {
	s.t.Helper()
	_, err := s.conn.Write([]byte(line + "\r\n"))
	require.NoError(s.t, err)
	return s.reply()
}

// plainLogin is a PLAIN sign-in.
func plainLogin(username, password string) sasl.Client {
	return sasl.NewPlainClient("", username, password)
}
