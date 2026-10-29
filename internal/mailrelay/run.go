package mailrelay

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Run is what `manager mailrelay ...` does: it serves SMTP and the admin port until it is told to stop, and reads its
// files again every few seconds, so that what the kubelet writes into its volumes (a new organization, a rotated login,
// a renewed certificate, the upstream's new password) is used without a restart, and nothing it counted is lost. It
// returns the process's exit status.
func Run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("mailrelay", flag.ContinueOnError)
	flags.SetOutput(stderr)
	listen := flags.String("listen", ":1587", "Where the SMTP submission server listens.")
	adminListen := flags.String("admin-listen", ":9187", "Where health, metrics and usage are served.")
	configPath := flags.String("config", "", "The configuration the operator writes (config.json).")
	tokenPath := flags.String("admin-token", "", "The file holding the token metrics and usage are read with.")
	certPath := flags.String("tls-cert", "", "The relay's certificate.")
	keyPath := flags.String("tls-key", "", "The relay's private key.")
	upstreamDir := flags.String("upstream-credentials", "", "A directory with the files username and password to sign in to the upstream with; empty signs in with none.")
	hostname := flags.String("hostname", "geass-mail-relay", "What the relay calls itself, to clients and to the upstream.")
	maxConnections := flags.Int("max-connections", 256, "The most connections at once, of every organization and of nobody signed in yet.")
	reloadEvery := flags.Duration("reload-interval", 5*time.Second, "How often the files are read again.")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *configPath == "" || *tokenPath == "" || *certPath == "" || *keyPath == "" {
		_, _ = fmt.Fprintln(stderr, "mailrelay: --config, --admin-token, --tls-cert and --tls-key are required")
		return 2
	}

	log := slog.New(slog.NewJSONHandler(stdout, nil))
	relay := New(Options{Logger: log, HeloName: *hostname})
	token := &tokenHolder{}
	files := &files{config: *configPath, token: *tokenPath, cert: *certPath, key: *keyPath, upstream: *upstreamDir}
	files.reload(relay, token, log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()
	go func() {
		ticker := time.NewTicker(*reloadEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				files.reload(relay, token, log)
			}
		}
	}()

	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Error("could not listen", "address", *listen, "error", err.Error())
		return 1
	}
	server := relay.NewServer(*hostname)
	admin := &http.Server{Addr: *adminListen, Handler: relay.AdminHandler(token.get), ReadHeaderTimeout: 10 * time.Second}

	failed := make(chan error, 2)
	go func() { failed <- server.Serve(LimitConnections(listener, *maxConnections)) }()
	go func() {
		if err := admin.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			failed <- err
		}
	}()
	log.Info("mail relay started", "listen", *listen, "admin", *adminListen, "maxConnections", *maxConnections)

	status := 0
	select {
	case <-ctx.Done():
	case err := <-failed:
		log.Error("the relay stopped", "error", err.Error())
		status = 1
	}
	// A message being handed to the upstream is let finish, within the pod's grace period.
	shutdown, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdown); err != nil {
		_ = server.Close()
	}
	_ = admin.Shutdown(shutdown)
	log.Info("mail relay stopped")
	return status
}

// files are where the relay reads what the operator gives it, and what it read last.
type files struct {
	config, token, cert, key, upstream string

	mu                           sync.Mutex
	lastConfig, lastCert, lastUp []byte
}

// reload reads every file again and uses what changed. A file that cannot be read or is wrong leaves what the relay had,
// and is logged without its content.
func (f *files) reload(relay *Relay, token *tokenHolder, log *slog.Logger) {
	f.mu.Lock()
	defer f.mu.Unlock()
	changed, failed := false, false
	fail := func(what string, err error) {
		failed = true
		log.Error("could not read the relay's "+what, "error", err.Error())
	}

	if data, err := os.ReadFile(f.config); err != nil {
		fail("configuration", err)
	} else if !bytes.Equal(data, f.lastConfig) {
		changed = true
		if config, err := ParseConfig(data); err != nil {
			fail("configuration", err)
		} else {
			relay.SetConfig(config)
			f.lastConfig = data
			log.Info("configuration loaded", "organizations", len(config.Organizations))
		}
	}
	if data, err := os.ReadFile(f.token); err != nil {
		fail("admin token", err)
	} else if value := strings.TrimSpace(string(data)); value != token.get() {
		token.set(value)
	}

	certPEM, certErr := os.ReadFile(f.cert)
	keyPEM, keyErr := os.ReadFile(f.key)
	switch {
	case certErr != nil || keyErr != nil:
		fail("certificate", errors.Join(certErr, keyErr))
	case !bytes.Equal(append(append([]byte{}, certPEM...), keyPEM...), f.lastCert):
		changed = true
		if certificate, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
			fail("certificate", err)
		} else {
			relay.SetCertificate(&certificate)
			f.lastCert = append(append([]byte{}, certPEM...), keyPEM...)
			log.Info("certificate loaded")
		}
	}

	if f.upstream != "" {
		username, userErr := os.ReadFile(filepath.Join(f.upstream, "username"))
		password, passErr := os.ReadFile(filepath.Join(f.upstream, "password"))
		combined := append(append(append([]byte{}, username...), 0), password...)
		switch {
		case userErr != nil || passErr != nil:
			fail("upstream credentials", errors.Join(userErr, passErr))
		case !bytes.Equal(combined, f.lastUp):
			changed = true
			relay.SetUpstreamCredentials(&UpstreamCredentials{
				Username: strings.TrimSpace(string(username)), Password: strings.TrimRight(string(password), "\r\n"),
			})
			f.lastUp = combined
			log.Info("upstream credentials loaded")
		}
	}

	switch {
	case failed:
		relay.metrics.reloads.WithLabelValues("failed").Inc()
	case changed:
		relay.metrics.reloads.WithLabelValues("ok").Inc()
	}
}

// LimitConnections keeps at most max connections open at once. One more is told to try again later and closed at once,
// so that a flood of connections, signed in or not, cannot hold the relay's memory or every organization's place.
func LimitConnections(listener net.Listener, max int) net.Listener {
	return &limitListener{Listener: listener, max: int64(max)}
}

type limitListener struct {
	net.Listener
	max    int64
	active atomic.Int64
}

func (l *limitListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if l.active.Add(1) > l.max {
			l.active.Add(-1)
			_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
			_, _ = conn.Write([]byte("421 4.7.0 Too many connections; try again later\r\n"))
			_ = conn.Close()
			continue
		}
		return &countedConn{Conn: conn, listener: l}, nil
	}
}

type countedConn struct {
	net.Conn
	listener *limitListener
	once     sync.Once
}

func (c *countedConn) Close() error {
	c.once.Do(func() { c.listener.active.Add(-1) })
	return c.Conn.Close()
}
