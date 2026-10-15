package alerts

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// receivedMail is one message the fake server accepted.
type receivedMail struct {
	from string
	to   []string
	data string
}

// fakeSMTP is a mail server that speaks enough SMTP for the client: EHLO, STARTTLS, AUTH PLAIN and LOGIN, MAIL, RCPT,
// DATA and QUIT. It is a real listener on the loopback, so the client's own protocol code is what is tested.
type fakeSMTP struct {
	t        *testing.T
	listener net.Listener
	cert     tls.Certificate
	roots    *x509.CertPool

	// offerSTARTTLS makes the server advertise STARTTLS. implicitTLS makes it encrypted from the first byte.
	offerSTARTTLS bool
	implicitTLS   bool
	// auth is what the server advertises after AUTH: "PLAIN", "LOGIN", or "" for none.
	auth       string
	user, pass string
	// requireTLSForAuth makes it refuse AUTH before the connection is encrypted.
	requireTLSForAuth bool
	rejectRcpt        map[string]bool
	// busyConnections is how many connections it greets with 421 before it behaves.
	busyConnections int

	mu          sync.Mutex
	connections int
	mails       []receivedMail
}

func newFakeSMTP(t *testing.T, configure func(*fakeSMTP)) *fakeSMTP {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	cert, roots := selfSignedCert(t)
	server := &fakeSMTP{t: t, listener: listener, cert: cert, roots: roots, rejectRcpt: map[string]bool{}}
	if configure != nil {
		configure(server)
	}
	go server.serve()
	t.Cleanup(func() { _ = listener.Close() })
	return server
}

func selfSignedCert(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "mail.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: []string{"mail.test"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	require.NoError(t, err)
	parsed, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, roots
}

// checker returns a checker whose connections go to this server, whatever host they ask for, and that trusts its
// certificate.
func (f *fakeSMTP) checker() *SMTPChecker {
	return &SMTPChecker{
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, network, f.listener.Addr().String())
		},
		TLSConfig: &tls.Config{RootCAs: f.roots},
	}
}

func (f *fakeSMTP) smtp(security string) SMTP {
	return SMTP{Host: "mail.test", Port: 2525, Security: security, Username: f.user, Password: f.pass, From: "Geass <alerts@example.com>"}
}

func (f *fakeSMTP) received() []receivedMail {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]receivedMail(nil), f.mails...)
}

func (f *fakeSMTP) serve() {
	for {
		conn, err := f.listener.Accept()
		if err != nil {
			return
		}
		go f.handle(conn)
	}
}

func (f *fakeSMTP) handle(raw net.Conn) {
	defer func() { _ = raw.Close() }()
	f.mu.Lock()
	f.connections++
	number := f.connections
	f.mu.Unlock()
	_ = raw.SetDeadline(time.Now().Add(10 * time.Second))

	conn := raw
	encrypted := false
	if f.implicitTLS {
		conn = tls.Server(raw, &tls.Config{Certificates: []tls.Certificate{f.cert}})
		encrypted = true
	}
	reader := bufio.NewReader(conn)
	say := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }
	line := func() (string, bool) {
		text, err := reader.ReadString('\n')
		return strings.TrimRight(text, "\r\n"), err == nil
	}
	if number <= f.busyConnections {
		say("421 4.3.2 try again later")
		return
	}
	say("220 mail.test ESMTP ready")

	var from string
	to := make([]string, 0, 4)
	authenticated := f.auth == ""
	for {
		text, ok := line()
		if !ok {
			return
		}
		upper := strings.ToUpper(text)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			lines := []string{"250-mail.test"}
			if f.offerSTARTTLS && !encrypted {
				lines = append(lines, "250-STARTTLS")
			}
			if f.auth != "" && (!f.requireTLSForAuth || encrypted) {
				lines = append(lines, "250-AUTH "+f.auth)
			}
			lines = append(lines, "250 8BITMIME")
			for _, l := range lines {
				say(l)
			}
		case upper == "STARTTLS":
			say("220 ready to start TLS")
			conn = tls.Server(raw, &tls.Config{Certificates: []tls.Certificate{f.cert}})
			reader = bufio.NewReader(conn)
			encrypted = true
		case strings.HasPrefix(upper, "AUTH PLAIN"):
			payload := strings.TrimSpace(text[len("AUTH PLAIN"):])
			if payload == "" {
				say("334 ")
				payload, _ = line()
			}
			decoded, _ := base64.StdEncoding.DecodeString(payload)
			parts := strings.Split(string(decoded), "\x00")
			if len(parts) == 3 && parts[1] == f.user && parts[2] == f.pass {
				authenticated = true
				say("235 2.7.0 authenticated")
			} else {
				say("535 5.7.8 username and password not accepted")
			}
		case upper == "AUTH LOGIN":
			say("334 " + base64.StdEncoding.EncodeToString([]byte("Username:")))
			user, _ := line()
			say("334 " + base64.StdEncoding.EncodeToString([]byte("Password:")))
			pass, _ := line()
			u, _ := base64.StdEncoding.DecodeString(user)
			p, _ := base64.StdEncoding.DecodeString(pass)
			if string(u) == f.user && string(p) == f.pass {
				authenticated = true
				say("235 2.7.0 authenticated")
			} else {
				say("535 5.7.8 username and password not accepted")
			}
		case strings.HasPrefix(upper, "MAIL FROM:"):
			if !authenticated {
				say("530 5.7.0 authentication required")
				continue
			}
			from = angleAddress(text[len("MAIL FROM:"):])
			to = to[:0]
			say("250 2.1.0 ok")
		case strings.HasPrefix(upper, "RCPT TO:"):
			address := angleAddress(text[len("RCPT TO:"):])
			if f.rejectRcpt[address] {
				say("550 5.1.1 no such user")
				continue
			}
			to = append(to, address)
			say("250 2.1.5 ok")
		case upper == "DATA":
			say("354 end data with <CR><LF>.<CR><LF>")
			var data strings.Builder
			for {
				l, ok := line()
				if !ok {
					return
				}
				if l == "." {
					break
				}
				data.WriteString(strings.TrimPrefix(l, ".") + "\n")
			}
			f.mu.Lock()
			f.mails = append(f.mails, receivedMail{from: from, to: append([]string(nil), to...), data: data.String()})
			f.mu.Unlock()
			say("250 2.0.0 queued")
		case upper == "QUIT":
			say("221 2.0.0 bye")
			return
		case upper == "RSET", upper == "NOOP":
			say("250 ok")
		default:
			say("502 5.5.2 command not recognized")
		}
	}
}

// angleAddress reads the address out of "<a@example.com> BODY=8BITMIME".
func angleAddress(argument string) string {
	argument = strings.TrimSpace(argument)
	if start := strings.Index(argument, "<"); start >= 0 {
		if end := strings.Index(argument[start:], ">"); end > 0 {
			return argument[start+1 : start+end]
		}
	}
	return argument
}
