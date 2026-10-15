package alerts

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/degoke/geass/pkg/webhooks"
)

// smtpTimeout bounds one conversation with the mail server, from connecting to the last answer.
const smtpTimeout = 30 * time.Second

// ValidateSMTP checks what a person typed for the mail server, without connecting to it.
//
// Alertmanager is what sends the mail, and it has two limits that the form must not let a person walk into. It
// connects encrypted from the first byte only on port 465, and it will not sign in over a connection that is
// not encrypted.
func ValidateSMTP(host string, port int, security, username, from string) error {
	host = strings.TrimSpace(host)
	if host == "" || strings.ContainsAny(host, " /:@\\{}") {
		return errors.New("enter the mail server's name, such as smtp.example.com, without a port or a scheme")
	}
	if len(host) > 253 {
		return errors.New("the mail server's name is too long")
	}
	if port < 1 || port > 65535 {
		return errors.New("the port must be between 1 and 65535")
	}
	switch security {
	case SecuritySTARTTLS:
		if port == ImplicitTLSPort {
			return errors.New("port 465 is encrypted from the first byte: choose TLS from the start, or use port 587 for STARTTLS")
		}
	case SecurityTLS:
		if port != ImplicitTLSPort {
			return errors.New("TLS from the start is only used on port 465. For another port, choose STARTTLS")
		}
	case SecurityNone:
		if strings.TrimSpace(username) != "" {
			return errors.New("a password cannot be sent over a connection that is not encrypted. Choose STARTTLS or TLS, or remove the username")
		}
	default:
		return errors.New("security must be starttls, tls or none")
	}
	if len(username) > 256 || strings.ContainsAny(username, "\r\n{}") {
		return errors.New("the username is not valid")
	}
	if _, err := ParseAddress(from); err != nil {
		return errors.New("enter the address alerts come from, such as alerts@example.com")
	}
	return nil
}

// SMTPChecker connects to a mail server to see that the settings work: it connects, encrypts and signs in, and
// sends nothing. Alertmanager does the sending, from its own pod, so this shows that the server and the login are
// right and says nothing of whether the monitoring namespace can reach it.
type SMTPChecker struct {
	// DialContext connects to the mail server. Nil refuses unsafe addresses, as the webhooks do.
	DialContext func(ctx context.Context, network, address string) (net.Conn, error)
	// TLSConfig is what the connection's TLS settings start from; tests put their own roots in it.
	TLSConfig *tls.Config
}

func (c *SMTPChecker) dial(ctx context.Context, network, address string) (net.Conn, error) {
	if c != nil && c.DialContext != nil {
		return c.DialContext(ctx, network, address)
	}
	return webhooks.SafeDialer().DialContext(ctx, network, address)
}

func (c *SMTPChecker) tlsConfig(host string) *tls.Config {
	config := &tls.Config{}
	if c != nil && c.TLSConfig != nil {
		config = c.TLSConfig.Clone()
	}
	config.ServerName = host
	config.MinVersion = tls.VersionTLS12
	return config
}

// plainAuth signs in with AUTH PLAIN. The standard library's is the same once the connection is encrypted, and Verify
// refuses to sign in on a connection that is not before this is reached.
type plainAuth struct{ username, password string }

func (a plainAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "PLAIN", []byte("\x00" + a.username + "\x00" + a.password), nil
}

func (a plainAuth) Next(_ []byte, more bool) ([]byte, error) {
	if more {
		return nil, errors.New("unexpected challenge from the mail server")
	}
	return nil, nil
}

// loginAuth is AUTH LOGIN, which some servers (Exchange, Office 365) offer instead of PLAIN.
type loginAuth struct{ username, password string }

func (a loginAuth) Start(*smtp.ServerInfo) (string, []byte, error) { return "LOGIN", nil, nil }

func (a loginAuth) Next(challenge []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(challenge))) {
	case "username:":
		return []byte(a.username), nil
	case "password:":
		return []byte(a.password), nil
	}
	return nil, errors.New("unexpected challenge from the mail server")
}

// chooseAuth picks how to sign in from what the server says it accepts.
func chooseAuth(offered, username, password string) (smtp.Auth, error) {
	offered = strings.ToUpper(offered)
	switch {
	case strings.Contains(offered, "PLAIN"):
		return plainAuth{username, password}, nil
	case strings.Contains(offered, "LOGIN"):
		return loginAuth{username, password}, nil
	case strings.Contains(offered, "CRAM-MD5"):
		return smtp.CRAMMD5Auth(username, password), nil
	}
	return nil, errors.New("the mail server does not offer a way to sign in that Geass supports (PLAIN, LOGIN or CRAM-MD5)")
}

// Verify connects, encrypts and signs in as a message would, and stops there. A wrong password or a closed port is
// found out here, with the server's own words, before an alert depends on it.
func (c *SMTPChecker) Verify(ctx context.Context, server SMTP) error {
	// The form refuses this too, but the settings can also be written straight to the platform configuration, and the
	// password that this would send is the one saved for the server.
	if server.Security == SecurityNone && server.Username != "" {
		return errors.New("a password cannot be sent over a connection that is not encrypted: choose STARTTLS or TLS, or remove the username")
	}
	ctx, cancel := context.WithTimeout(ctx, smtpTimeout)
	defer cancel()
	address := net.JoinHostPort(server.Host, strconv.Itoa(server.Port))
	conn, err := c.dial(ctx, "tcp", address)
	if err != nil {
		return fmt.Errorf("could not connect to %s: %s", address, dialReason(err))
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	raw := conn
	// Cancelling the context must end a conversation that is waiting on the server.
	context.AfterFunc(ctx, func() { _ = raw.Close() })

	tlsConfig := c.tlsConfig(server.Host)
	if server.Security == SecurityTLS {
		secured := tls.Client(conn, tlsConfig)
		if err := secured.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return fmt.Errorf("could not set up TLS with %s: %w", address, err)
		}
		conn = secured
	}
	client, err := smtp.NewClient(conn, server.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("%s did not answer like a mail server: %w", address, err)
	}
	defer func() { _ = client.Close() }()
	if server.Security == SecuritySTARTTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("%s does not offer STARTTLS: choose TLS from the start (port 465), or none for a server that does not encrypt", address)
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("could not set up TLS with %s: %w", address, err)
		}
	}
	if server.Username != "" {
		ok, offered := client.Extension("AUTH")
		if !ok {
			return errors.New("the mail server does not offer a sign-in on this connection: remove the username")
		}
		auth, err := chooseAuth(offered, server.Username, server.Password)
		if err != nil {
			return err
		}
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("the mail server refused the username and password: %w", err)
		}
	}
	return client.Quit()
}

func dialReason(err error) string {
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Err != nil {
		return opErr.Err.Error()
	}
	return err.Error()
}
