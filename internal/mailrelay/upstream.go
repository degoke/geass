package mailrelay

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
)

// The relay hands each message to the upstream while its client waits, on a connection of its own, and answers the client
// with what the upstream answered: 250 only once the upstream has taken the message. So nothing is kept in the relay, there
// is no queue to fill or lose, and a client whose message could not be sent is told so and keeps it. One recipient the
// upstream refuses fails the whole message.

const (
	upstreamDialTimeout    = 10 * time.Second
	upstreamCommandTimeout = 30 * time.Second
	upstreamDataTimeout    = 2 * time.Minute
)

// UpstreamCredentials are what the relay signs in to the upstream with.
type UpstreamCredentials struct {
	Username string
	Password string
}

// envelope is one message to send.
type envelope struct {
	from       string
	recipients []string
}

// upstreamError is why a message was not sent, of one of the kinds the metrics name.
type upstreamError struct {
	kind string
	// reply is what the upstream answered, when it answered.
	reply *smtp.SMTPError
	err   error
}

func (e *upstreamError) Error() string {
	if e.reply != nil {
		return fmt.Sprintf("upstream %s: %d %d.%d.%d", e.kind, e.reply.Code, e.reply.EnhancedCode[0], e.reply.EnhancedCode[1], e.reply.EnhancedCode[2])
	}
	return fmt.Sprintf("upstream %s: %v", e.kind, e.err)
}

func (e *upstreamError) Unwrap() error { return e.err }

// errMessageAborted is what sending reports when reading the client's message failed, such as for being too large.
var errMessageAborted = errors.New("the message could not be read")

// sender sends one message to the upstream. Tests give the relay their own.
type sender interface {
	send(ctx context.Context, upstream Upstream, credentials *UpstreamCredentials, env envelope, message io.Reader) error
}

// smtpSender sends with go-smtp's client.
type smtpSender struct {
	// rootCAs are the authorities the upstream's certificate is checked against; nil is the system's.
	rootCAs *x509.CertPool
	// heloName is what the relay calls itself to the upstream.
	heloName string
	// dial connects to the upstream; nil dials its host. Tests send a host name to the loopback address with it.
	dial func(ctx context.Context, address string) (net.Conn, error)
}

func (s *smtpSender) connect(ctx context.Context, address string) (net.Conn, error) {
	if s.dial != nil {
		return s.dial(ctx, address)
	}
	return (&net.Dialer{Timeout: upstreamDialTimeout}).DialContext(ctx, "tcp", address)
}

func (s *smtpSender) send(ctx context.Context, upstream Upstream, credentials *UpstreamCredentials, env envelope, message io.Reader) error {
	address := net.JoinHostPort(upstream.Host, strconv.Itoa(upstream.Port))
	tlsConfig := &tls.Config{ServerName: upstream.Host, RootCAs: s.rootCAs, MinVersion: tls.VersionTLS12}
	dialCtx, cancel := context.WithTimeout(ctx, upstreamDialTimeout)
	defer cancel()

	conn, err := s.connect(dialCtx, address)
	if err == nil && upstream.TLS == TLSImplicit {
		secure := tls.Client(conn, tlsConfig)
		if err = secure.HandshakeContext(dialCtx); err != nil {
			_ = conn.Close()
		}
		conn = secure
	}
	if err != nil {
		return &upstreamError{kind: upstreamUnreachable, err: err}
	}
	// A context that ends (the relay stopping) ends the conversation.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	var client *smtp.Client
	if upstream.TLS == TLSStartTLS {
		// It fails, and sends nothing, unless the upstream offers STARTTLS and its certificate is valid for the host.
		client, err = smtp.NewClientStartTLS(conn, tlsConfig)
		if err != nil {
			_ = conn.Close()
			return classify(err, upstreamUnreachable)
		}
	} else {
		client = smtp.NewClient(conn)
	}
	defer func() { _ = client.Close() }()
	client.CommandTimeout = upstreamCommandTimeout
	client.SubmissionTimeout = upstreamDataTimeout
	// After STARTTLS the client greets again, so the name is given there too.
	if s.heloName != "" {
		if err := client.Hello(s.heloName); err != nil {
			return classify(err, upstreamUnreachable)
		}
	}

	if credentials != nil {
		if err := client.Auth(sasl.NewPlainClient("", credentials.Username, credentials.Password)); err != nil {
			return classify(err, upstreamAuth)
		}
	}
	if err := client.Mail(env.from, nil); err != nil {
		// An upstream that wants a sign-in the relay did not make says so at MAIL: that is the platform's to fix too.
		if reply := (*smtp.SMTPError)(nil); errors.As(err, &reply) && (reply.Code == 530 || (credentials == nil && reply.EnhancedCode == smtp.EnhancedCode{5, 7, 0})) {
			return classify(err, upstreamAuth)
		}
		return classify(err, "")
	}
	for _, recipient := range env.recipients {
		if err := client.Rcpt(recipient, nil); err != nil {
			return classify(err, "")
		}
	}
	data, err := client.Data()
	if err != nil {
		return classify(err, "")
	}
	if _, err := io.Copy(data, message); err != nil {
		// The final dot is never sent: closing the connection in the middle of the data makes the upstream drop what it
		// has of the message.
		_ = client.Close()
		return fmt.Errorf("%w: %w", errMessageAborted, err)
	}
	if err := data.Close(); err != nil {
		return classify(err, "")
	}
	_ = client.Quit()
	return nil
}

// classify says what kind of failure an error of the client is: a reply of the upstream is deferred (4xx) or refused (5xx)
// by its code, unless kind names it, and anything else is the upstream being unreachable.
func classify(err error, kind string) error {
	var reply *smtp.SMTPError
	if !errors.As(err, &reply) {
		return &upstreamError{kind: upstreamUnreachable, err: err}
	}
	if kind == "" || kind == upstreamUnreachable {
		kind = upstreamRefused
		if reply.Temporary() {
			kind = upstreamDeferred
		}
	}
	return &upstreamError{kind: kind, reply: reply, err: err}
}
