// Package mailrelay is the mail relay that Geass runs in the cluster for organizations' services (`manager mailrelay`, made
// and kept by the operator; see pkg/platform/mail_relay.go). It is an SMTP submission server: a client must start TLS
// before it may sign in, signs in with its organization's own login, and sends within its organization's limits; the relay
// hands each message to the platform's mail service (the upstream) while the client waits, and answers with what the
// upstream said.
//
// It runs from the operator's image because that is the one image every cluster that installed Geass has, as the network
// policy check does (internal/netcheck).
package mailrelay

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/mail"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
)

const (
	// maxAuthFailures is how many failed sign-ins a connection may make before it is closed.
	maxAuthFailures = 3
	// maxHeaderBytes is the most a message's header may be. The header is read before the message is sent on, to check its
	// sender; the rest is passed through as it comes.
	maxHeaderBytes = 256 << 10
	// sendTimeout bounds handing one message to the upstream.
	sendTimeout = 3 * time.Minute
)

// The replies the relay gives that go-smtp does not.
var (
	errAuthRequired = &smtp.SMTPError{Code: 530, EnhancedCode: smtp.EnhancedCode{5, 7, 0}, Message: "Authentication required: start TLS, then sign in with your organization's login"}
	errRemoved      = &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 7, 1}, Message: "This login may no longer send mail"}
	errTooManyConns = &smtp.SMTPError{Code: 454, EnhancedCode: smtp.EnhancedCode{4, 7, 0}, Message: "Too many connections of this organization at once; try again later"}
	errNoUpstream   = &smtp.SMTPError{Code: 451, EnhancedCode: smtp.EnhancedCode{4, 4, 1}, Message: "The relay could not reach its mail service; try again later"}
	errUpstreamAuth = &smtp.SMTPError{Code: 451, EnhancedCode: smtp.EnhancedCode{4, 7, 0}, Message: "The relay could not sign in to its mail service; try again later"}
)

// Relay is the relay's state: the configuration it was last given, what it counts for each organization, and its metrics.
type Relay struct {
	log     *slog.Logger
	now     func() time.Time
	metrics *metrics
	sender  sender

	current     atomic.Pointer[loaded]
	certificate atomic.Pointer[tls.Certificate]
	credentials atomic.Pointer[UpstreamCredentials]

	mu   sync.Mutex
	orgs map[string]*orgCounters

	startedAt time.Time
}

// Options are what a relay is made with besides its configuration.
type Options struct {
	// Logger is where the relay writes; nil discards. Nothing secret is ever written: no password, no message, no
	// credential of the upstream.
	Logger *slog.Logger
	// Now is the clock; nil is time.Now.
	Now func() time.Time
	// HeloName is what the relay calls itself to the upstream.
	HeloName string
	// sender replaces the upstream client, in tests.
	sender sender
}

// New makes a relay. It takes no message until it has a configuration and a certificate (SetConfig, SetCertificate).
func New(o Options) *Relay {
	r := &Relay{log: o.Logger, now: o.Now, metrics: newMetrics(), sender: o.sender, orgs: map[string]*orgCounters{}}
	if r.log == nil {
		r.log = slog.New(slog.DiscardHandler)
	}
	if r.now == nil {
		r.now = time.Now
	}
	if r.sender == nil {
		r.sender = &smtpSender{heloName: o.HeloName}
	}
	r.startedAt = r.now()
	return r
}

// SetConfig makes a configuration the one the relay works with. What it counted for each organization is kept.
func (r *Relay) SetConfig(config *Config) {
	r.current.Store(load(config))
}

// SetCertificate makes a certificate the one the relay serves.
func (r *Relay) SetCertificate(certificate *tls.Certificate) {
	r.certificate.Store(certificate)
}

// SetUpstreamCredentials makes credentials the ones the relay signs in to the upstream with; nil signs in with none.
func (r *Relay) SetUpstreamCredentials(credentials *UpstreamCredentials) {
	r.credentials.Store(credentials)
}

// Ready reports whether the relay has a configuration and a certificate.
func (r *Relay) Ready() bool { return r.current.Load() != nil && r.certificate.Load() != nil }

// TLSConfig is the server's TLS configuration, which always serves the latest certificate.
func (r *Relay) TLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			if certificate := r.certificate.Load(); certificate != nil {
				return certificate, nil
			}
			return nil, errors.New("the relay has no certificate yet")
		},
	}
}

// NewServer is the SMTP server for the relay. It requires TLS before a client may sign in (go-smtp neither offers nor
// takes AUTH on a connection without it) and never writes what is said on a connection anywhere.
func (r *Relay) NewServer(hostname string) *smtp.Server {
	server := smtp.NewServer(r)
	server.Domain = hostname
	server.TLSConfig = r.TLSConfig()
	server.AllowInsecureAuth = false
	server.MaxMessageBytes = maxRelayMessageBytes
	server.ReadTimeout = 2 * time.Minute
	server.WriteTimeout = 2 * time.Minute
	server.ErrorLog = slogPrinter{log: r.log}
	return server
}

// maxRelayMessageBytes is the most any organization may be allowed, which the server advertises (SIZE); each organization's
// own limit is held as its message comes in.
const maxRelayMessageBytes = 50 << 20

// slogPrinter takes go-smtp's own error lines, which name a connection's address and what went wrong with it.
type slogPrinter struct{ log *slog.Logger }

func (p slogPrinter) Printf(format string, v ...any) {
	p.log.Warn("smtp server", "detail", strings.TrimSpace(fmt.Sprintf(format, v...)))
}

func (p slogPrinter) Println(v ...any) {
	p.log.Warn("smtp server", "detail", strings.TrimSpace(fmt.Sprintln(v...)))
}

// counters are what the relay counts for an organization, made the first time it is needed.
func (r *Relay) counters(org string) *orgCounters {
	r.mu.Lock()
	defer r.mu.Unlock()
	counters := r.orgs[org]
	if counters == nil {
		counters = newOrgCounters()
		r.orgs[org] = counters
	}
	return counters
}

// NewSession is go-smtp's backend: one session per connection.
func (r *Relay) NewSession(c *smtp.Conn) (smtp.Session, error) {
	remote := ""
	if addr := c.Conn().RemoteAddr(); addr != nil {
		remote = addr.String()
	}
	return &session{relay: r, conn: c, remote: remote}, nil
}

// session is one connection, and the message it is sending.
type session struct {
	relay  *Relay
	conn   *smtp.Conn
	remote string

	// org is the organization signed in, "" before.
	org      string
	failures int

	from       string
	recipients []string
	held       *reservation
}

var _ smtp.AuthSession = (*session)(nil)

// AuthMechanisms are PLAIN and LOGIN, which every mail library speaks. go-smtp offers them only once TLS is on.
func (s *session) AuthMechanisms() []string { return []string{sasl.Plain, sasl.Login} }

// Auth starts a sign-in. A connection that failed too often is closed instead.
func (s *session) Auth(mechanism string) (sasl.Server, error) {
	if s.failures >= maxAuthFailures {
		_ = s.conn.Close()
		return nil, smtp.ErrAuthFailed
	}
	switch mechanism {
	case sasl.Plain:
		return sasl.NewPlainServer(func(identity, username, password string) error {
			if identity != "" && identity != username {
				return s.refuseSignIn(username)
			}
			return s.signIn(username, password)
		}), nil
	case sasl.Login:
		return &loginServer{authenticate: s.signIn}, nil
	}
	return nil, smtp.ErrAuthUnknownMechanism
}

// signIn checks a login and takes one of its organization's connections.
func (s *session) signIn(username, password string) error {
	current := s.relay.current.Load()
	if current == nil {
		return errNoUpstream
	}
	org, ok := current.authenticate(username, password, s.relay.now())
	if !ok {
		return s.refuseSignIn(username)
	}
	counters := s.relay.counters(org.Name)
	if !counters.acquire(org.Limits.Connections) {
		s.reject(org.Name, reasonConnections, "a connection over the organization's limit was refused", "limit", org.Limits.Connections)
		return errTooManyConns
	}
	s.org = org.Name
	s.relay.metrics.connections.WithLabelValues(org.Name).Inc()
	s.relay.log.Info("signed in", orgLabel, org.Name, "remote", s.remote)
	return nil
}

// refuseSignIn counts a failed sign-in. The username is logged and counted only when it is an organization's: anything
// else may be a password typed in the wrong place.
func (s *session) refuseSignIn(username string) error {
	s.failures++
	org := unknownOrg
	if current := s.relay.current.Load(); current != nil {
		if known := current.byUsername[username]; known != nil {
			org = known.Name
			s.relay.counters(org).countAuthFailure()
		}
	}
	s.relay.metrics.authFailures.WithLabelValues(org).Inc()
	s.relay.log.Warn("sign-in refused", orgLabel, org, "remote", s.remote, "failures", s.failures)
	return smtp.ErrAuthFailed
}

// organization is the signed-in organization as the configuration has it now, nil when it has been removed since.
func (s *session) organization() (*loaded, *loadedOrg) {
	current := s.relay.current.Load()
	if current == nil || s.org == "" {
		return current, nil
	}
	return current, current.byName[s.org]
}

// reject counts and logs a refusal.
func (s *session) reject(org, reason, message string, args ...any) {
	s.relay.counters(org).countRejected(reason)
	s.relay.metrics.rejections.WithLabelValues(org, reason).Inc()
	s.relay.log.Info(message, append([]any{orgLabel, org, "reason", reason, "remote", s.remote}, args...)...)
}

// Mail starts a message: the sender must be one the organization may send from, the size it announces within its limit,
// and the organization must have room for one more message in each of its windows.
func (s *session) Mail(from string, opts *smtp.MailOptions) error {
	if s.org == "" {
		return errAuthRequired
	}
	_, org := s.organization()
	if org == nil {
		s.reject(s.org, reasonRemoved, "a message of an organization the relay no longer has was refused")
		return errRemoved
	}
	s.held.release()
	s.held = nil
	domain := domainOf(from)
	if !org.senderAllowed(domain) {
		s.reject(org.Name, reasonSenderDomain, "a sender the organization may not send from was refused", "domain", domain)
		return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 7, 1},
			Message: "Sender address rejected: not a domain this organization may send from"}
	}
	if opts != nil && opts.Size > org.Limits.MaxMessageBytes {
		s.reject(org.Name, reasonSize, "a message announced over the size limit was refused", "size", opts.Size, "limit", org.Limits.MaxMessageBytes)
		return sizeError(org.Limits.MaxMessageBytes)
	}
	held, full := s.relay.counters(org.Name).reserve(s.relay.now(), org.Limits)
	if full != nil {
		limit := full.limit(org.Limits)
		s.reject(org.Name, "rate_"+full.name, "a message over a rate limit was refused", "window", full.name, "limit", limit)
		return &smtp.SMTPError{Code: 450, EnhancedCode: smtp.EnhancedCode{4, 7, 1},
			Message: fmt.Sprintf("Rate limit reached: this organization may send %d messages a %s; try again later", limit, full.name)}
	}
	s.held = held
	s.from = from
	s.recipients = nil
	return nil
}

func sizeError(limit int64) error {
	return &smtp.SMTPError{Code: 552, EnhancedCode: smtp.EnhancedCode{5, 3, 4},
		Message: fmt.Sprintf("Message too big: this organization may send messages of at most %d bytes", limit)}
}

// Rcpt adds a recipient: within the organization's limit of recipients, and not at a domain the relay does not send to.
func (s *session) Rcpt(to string, _ *smtp.RcptOptions) error {
	current, org := s.organization()
	switch {
	case s.org == "":
		return errAuthRequired
	case org == nil:
		s.reject(s.org, reasonRemoved, "a message of an organization the relay no longer has was refused")
		return errRemoved
	case int64(len(s.recipients)) >= org.Limits.RecipientsPerMessage:
		s.reject(org.Name, reasonRecipients, "a recipient over the limit per message was refused", "limit", org.Limits.RecipientsPerMessage)
		return &smtp.SMTPError{Code: 452, EnhancedCode: smtp.EnhancedCode{4, 5, 3},
			Message: fmt.Sprintf("Too many recipients: at most %d a message; send the rest in another message", org.Limits.RecipientsPerMessage)}
	}
	domain := domainOf(to)
	if domain == "" {
		return &smtp.SMTPError{Code: 501, EnhancedCode: smtp.EnhancedCode{5, 1, 3}, Message: "Recipient address has no domain"}
	}
	if current.recipientDenied(domain) {
		s.reject(org.Name, reasonRecipientDomain, "a recipient at a denied domain was refused", "domain", domain)
		return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 7, 1}, Message: "Recipient address rejected: the relay does not send to this domain"}
	}
	s.recipients = append(s.recipients, to)
	return nil
}

// Data sends the message on to the upstream and answers with what it said. The header is read first, to check the
// addresses of From and Sender; the rest goes through as it arrives, and a message that grows past the organization's
// size limit is dropped at the upstream (its connection is closed before the end of the data).
func (s *session) Data(r io.Reader) error {
	current, org := s.organization()
	if s.org == "" {
		return errAuthRequired
	}
	if org == nil {
		s.reject(s.org, reasonRemoved, "a message of an organization the relay no longer has was refused")
		return errRemoved
	}
	held := s.held
	s.held = nil
	limit := org.Limits.MaxMessageBytes
	limited := &limitedReader{r: r, left: limit}
	buffered := bufio.NewReaderSize(limited, 32<<10)
	header, err := readHeader(buffered)
	if err != nil {
		held.release()
		if limited.over {
			s.reject(org.Name, reasonSize, "a message over the size limit was refused", "limit", limit)
			return sizeError(limit)
		}
		return &smtp.SMTPError{Code: 554, EnhancedCode: smtp.EnhancedCode{5, 6, 0}, Message: "The message header could not be read"}
	}
	if org.checksSender() {
		if domain, ok := headerSendersAllowed(header, org); !ok {
			held.release()
			s.reject(org.Name, reasonSenderDomain, "a message whose From or Sender the organization may not use was refused", "domain", domain)
			return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 7, 1},
				Message: "From or Sender header rejected: not a domain this organization may send from"}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	started := s.relay.now()
	message := io.MultiReader(bytes.NewReader(header), buffered)
	err = s.relay.sender.send(ctx, current.config.Upstream, s.relay.credentials.Load(), envelope{from: s.from, recipients: s.recipients}, message)
	elapsed := s.relay.now().Sub(started)
	if limited.over {
		held.release()
		s.reject(org.Name, reasonSize, "a message over the size limit was refused", "limit", limit)
		return sizeError(limit)
	}
	if err != nil {
		held.release()
		return s.upstreamFailed(org.Name, err, elapsed)
	}
	size := limit - limited.left
	held.keep(s.relay.now(), int64(len(s.recipients)), size)
	s.relay.metrics.accepted.WithLabelValues(org.Name).Inc()
	s.relay.metrics.recipients.WithLabelValues(org.Name).Add(float64(len(s.recipients)))
	s.relay.metrics.bytes.WithLabelValues(org.Name).Add(float64(size))
	s.relay.metrics.upstreamDuration.WithLabelValues("accepted").Observe(elapsed.Seconds())
	s.relay.log.Info("message relayed", orgLabel, org.Name, "fromDomain", domainOf(s.from), "recipients", len(s.recipients),
		"bytes", size, "durationMs", elapsed.Milliseconds())
	return nil
}

// upstreamFailed answers a client whose message the upstream did not take: a temporary failure for anything the client can
// try again (the upstream unreachable, deferring, or refusing the relay's own sign-in), and the upstream's own permanent
// code when it refused the message for good. What the upstream said is not passed on: it can name the platform's account.
func (s *session) upstreamFailed(org string, err error, elapsed time.Duration) error {
	var failure *upstreamError
	if !errors.As(err, &failure) {
		failure = &upstreamError{kind: upstreamUnreachable, err: err}
	}
	s.relay.counters(org).countUpstreamFailure(failure.kind)
	s.relay.metrics.upstreamFailures.WithLabelValues(org, failure.kind).Inc()
	s.relay.metrics.upstreamDuration.WithLabelValues(failure.kind).Observe(elapsed.Seconds())
	code := 0
	if failure.reply != nil {
		code = failure.reply.Code
	}
	s.relay.log.Warn("the upstream did not take a message", orgLabel, org, "kind", failure.kind, "upstreamCode", code,
		"recipients", len(s.recipients), "durationMs", elapsed.Milliseconds())
	switch failure.kind {
	case upstreamAuth:
		return errUpstreamAuth
	case upstreamDeferred:
		enhanced := failure.reply.EnhancedCode
		if enhanced[0] != 4 {
			enhanced = smtp.EnhancedCode{4, 0, 0}
		}
		return &smtp.SMTPError{Code: 451, EnhancedCode: enhanced, Message: fmt.Sprintf("The mail service deferred the message (%d); try again later", code)}
	case upstreamRefused:
		enhanced := failure.reply.EnhancedCode
		if enhanced[0] != 5 {
			enhanced = smtp.EnhancedCode{5, 0, 0}
		}
		replyCode := code
		if replyCode/100 != 5 {
			replyCode = 554
		}
		return &smtp.SMTPError{Code: replyCode, EnhancedCode: enhanced, Message: fmt.Sprintf("The mail service refused the message (%d)", code)}
	}
	return errNoUpstream
}

// Reset ends a message that was not sent, and gives back its count.
func (s *session) Reset() {
	s.held.release()
	s.held = nil
	s.from = ""
	s.recipients = nil
}

// Logout ends the connection, and gives back its organization's connection.
func (s *session) Logout() error {
	s.Reset()
	if s.org != "" {
		s.relay.counters(s.org).releaseConnection()
		s.relay.metrics.connections.WithLabelValues(s.org).Dec()
		s.org = ""
	}
	return nil
}

// limitedReader reads at most left bytes, and fails, saying so, on the byte past them.
type limitedReader struct {
	r    io.Reader
	left int64
	over bool
}

var errTooBig = errors.New("the message is over the size limit")

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.over {
		return 0, errTooBig
	}
	if int64(len(p)) > l.left+1 {
		p = p[:l.left+1]
	}
	n, err := l.r.Read(p)
	if int64(n) > l.left {
		l.over = true
		n = int(l.left)
		l.left = 0
		return n, errTooBig
	}
	l.left -= int64(n)
	return n, err
}

// readHeader reads a message's header, up to and with the empty line that ends it.
func readHeader(r *bufio.Reader) ([]byte, error) {
	var header bytes.Buffer
	for {
		line, err := r.ReadSlice('\n')
		header.Write(line)
		if header.Len() > maxHeaderBytes {
			return nil, errors.New("the header is too long")
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return header.Bytes(), nil
			}
			return nil, err
		}
		if len(bytes.TrimRight(line, "\r\n")) == 0 {
			return header.Bytes(), nil
		}
	}
}

// headerSendersAllowed checks every address of the From and Sender headers against the organization's sender domains. It
// returns the first domain it refuses. A message without a From header, or with one that cannot be read, is refused.
func headerSendersAllowed(header []byte, org *loadedOrg) (string, bool) {
	message, err := mail.ReadMessage(bytes.NewReader(header))
	if err != nil {
		return "", false
	}
	from := message.Header["From"]
	if len(from) == 0 {
		return "", false
	}
	for _, field := range append(from, message.Header["Sender"]...) {
		addresses, err := mail.ParseAddressList(field)
		if err != nil {
			return "", false
		}
		for _, address := range addresses {
			if domain := domainOf(address.Address); !org.senderAllowed(domain) {
				return domain, false
			}
		}
	}
	return "", true
}

// loginServer is the LOGIN mechanism, which go-sasl has only a client of: the server asks for the username and then the
// password, each base64 encoded by go-smtp.
type loginServer struct {
	authenticate func(username, password string) error
	username     string
	step         int
}

func (l *loginServer) Next(response []byte) (challenge []byte, done bool, err error) {
	switch l.step {
	case 0:
		l.step = 1
		if response == nil {
			return []byte("Username:"), false, nil
		}
		// The username came with AUTH LOGIN itself.
		l.username = string(response)
		l.step = 2
		return []byte("Password:"), false, nil
	case 1:
		l.username = string(response)
		l.step = 2
		return []byte("Password:"), false, nil
	case 2:
		l.step = 3
		return nil, true, l.authenticate(l.username, string(response))
	}
	return nil, false, sasl.ErrUnexpectedClientResponse
}
