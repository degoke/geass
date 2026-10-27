package mailrelay

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"
	"github.com/stretchr/testify/require"

	"github.com/degoke/geass/pkg/platform"
)

const (
	acmePassword  = "acme-password-0123456789abcdefghijklmnopqrstuvw"
	otherPassword = "other-password-0123456789abcdefghijklmnopqrstuv"
)

func TestTLSIsRequiredBeforeAnyoneSignsIn(t *testing.T) {
	h := newHarness(t, testOrg("org-acme", acmePassword, roomy()))
	raw := h.raw(t)

	greeting := raw.say("EHLO client.test")
	require.Contains(t, greeting, "STARTTLS")
	require.NotContains(t, greeting, "AUTH", "sign-in is not offered before TLS")
	plain := base64.StdEncoding.EncodeToString([]byte("\x00org-acme\x00" + acmePassword))
	require.True(t, strings.HasPrefix(raw.say("AUTH PLAIN "+plain), "523 5.7.10"), "a sign-in without TLS is refused, password and all")
	require.True(t, strings.HasPrefix(raw.say("MAIL FROM:<app@acme.example>"), "530 5.7.0"), "nothing is sent without signing in")
	require.True(t, strings.HasPrefix(raw.say("STARTTLS"), "220 "))

	tlsConn := tls.Client(raw.conn, &tls.Config{RootCAs: h.roots, ServerName: platform.MailRelayHost(), MinVersion: tls.VersionTLS12})
	require.NoError(t, tlsConn.Handshake(), "the relay's certificate is valid for its Service's name and signed by its authority")
	raw.conn, raw.buf = tlsConn, nil
	after := raw.say("EHLO client.test")
	require.Contains(t, after, "AUTH PLAIN LOGIN")
	require.True(t, strings.HasPrefix(raw.say("MAIL FROM:<app@acme.example>"), "530 5.7.0"), "TLS alone is not a sign-in")
	require.True(t, strings.HasPrefix(raw.say("AUTH PLAIN "+plain), "235 "))
	require.True(t, strings.HasPrefix(raw.say("MAIL FROM:<app@acme.example>"), "250 "))
}

func TestTheRelaysCertificateIsCheckedByAClient(t *testing.T) {
	h := newHarness(t, testOrg("org-acme", acmePassword, roomy()))
	conn, err := net.Dial("tcp", h.addr)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	// The handshake happens on the first command after STARTTLS.
	client, err := smtp.NewClientStartTLS(conn, &tls.Config{ServerName: platform.MailRelayHost(), MinVersion: tls.VersionTLS12})
	if err == nil {
		err = client.Noop()
	}
	require.ErrorContains(t, err, "certificate", "without the relay's authority the certificate is not trusted")
	conn2, err := net.Dial("tcp", h.addr)
	require.NoError(t, err)
	defer func() { _ = conn2.Close() }()
	client, err = smtp.NewClientStartTLS(conn2, &tls.Config{RootCAs: h.roots, ServerName: "relay.elsewhere.example", MinVersion: tls.VersionTLS12})
	if err == nil {
		err = client.Noop()
	}
	require.ErrorContains(t, err, "certificate", "nor for a name that is not the relay's")
}

func TestWrongOrMissingCredentialsAreRefused(t *testing.T) {
	h := newHarness(t, testOrg("org-acme", acmePassword, roomy()), testOrg("org-other", otherPassword, roomy()))

	for name, login := range map[string][2]string{
		"a wrong password":                  {"org-acme", acmePassword + "x"},
		"another organization's password":   {"org-acme", otherPassword},
		"a username nobody has":             {"org-nobody", acmePassword},
		"an empty password":                 {"org-acme", ""},
		"the password in place of the user": {acmePassword, acmePassword},
	} {
		client := h.connect(t)
		requireReply(t, client.Auth(sasl.NewPlainClient("", login[0], login[1])), 535, smtp.EnhancedCode{5, 7, 8})
		requireReply(t, sendOne(client), 530, smtp.EnhancedCode{5, 7, 0})
		require.Empty(t, h.upstream.received(), name)
	}

	client := h.connect(t)
	requireReply(t, client.Auth(sasl.NewPlainClient("org-other", "org-acme", acmePassword)), 535, smtp.EnhancedCode{5, 7, 8})

	// The LOGIN mechanism signs in as PLAIN does.
	login := h.connect(t)
	require.NoError(t, login.Auth(sasl.NewLoginClient("org-acme", acmePassword)))
	require.NoError(t, sendOne(login))
	require.Len(t, h.upstream.received(), 1)
}

func TestAConnectionThatFailsToSignInThreeTimesIsClosed(t *testing.T) {
	h := newHarness(t, testOrg("org-acme", acmePassword, roomy()))
	client := h.connect(t)
	for range maxAuthFailures {
		requireReply(t, client.Auth(sasl.NewPlainClient("", "org-acme", "guess")), 535, smtp.EnhancedCode{5, 7, 8})
	}
	err := client.Auth(sasl.NewPlainClient("", "org-acme", acmePassword))
	require.Error(t, err, "even the right password is not taken once a connection has failed three times")
	var reply *smtp.SMTPError
	require.False(t, errorsAs(err, &reply) && reply.Code == 235)
	require.Contains(t, h.logs.String(), `"msg":"sign-in refused","org":"org-acme"`)
}

func TestARotatedPasswordIsTakenUntilItsTimeAndTheNewOneAlways(t *testing.T) {
	until := testStart.Add(24 * time.Hour)
	org := testOrg("org-acme", "new-password", roomy())
	org.Credentials = append(org.Credentials, Credential{SHA256: PasswordDigest("old-password"), NotAfter: &until})
	h := newHarness(t, org)

	h.signedIn(t, "org-acme", "old-password")
	h.signedIn(t, "org-acme", "new-password")
	h.clock.set(until)
	requireReply(t, h.connect(t).Auth(sasl.NewPlainClient("", "org-acme", "old-password")), 535, smtp.EnhancedCode{5, 7, 8})
	h.signedIn(t, "org-acme", "new-password")

	// A login taken out of the configuration (its Secret deleted, the organization removed) stops at once, for the
	// connections already signed in too.
	client := h.signedIn(t, "org-acme", "new-password")
	h.reconfigure(func(c *Config) { c.Organizations = nil })
	requireReply(t, sendOne(client), 550, smtp.EnhancedCode{5, 7, 1})
	requireReply(t, h.connect(t).Auth(sasl.NewPlainClient("", "org-acme", "new-password")), 535, smtp.EnhancedCode{5, 7, 8})
}

func TestEachRateLimitRefusesAtItsLimitAndRefillsAfterItsWindow(t *testing.T) {
	for _, tc := range []struct {
		window window
		limits func(*Limits)
		limit  int
	}{
		{minuteWindow, func(l *Limits) { l.MessagesPerMinute = 3 }, 3},
		{hourWindow, func(l *Limits) { l.MessagesPerHour = 2 }, 2},
		{dayWindow, func(l *Limits) { l.MessagesPerDay = 4 }, 4},
	} {
		t.Run(tc.window.name, func(t *testing.T) {
			limits := roomy()
			tc.limits(&limits)
			h := newHarness(t, testOrg("org-acme", acmePassword, limits))
			client := h.signedIn(t, "org-acme", acmePassword)
			for i := range tc.limit {
				require.NoError(t, sendOne(client), "message %d of %d", i+1, tc.limit)
			}
			err := sendOne(client)
			requireReply(t, err, 450, smtp.EnhancedCode{4, 7, 1})
			require.Contains(t, err.Error(), fmt.Sprintf("%d messages a %s", tc.limit, tc.window.name))
			require.Len(t, h.upstream.received(), tc.limit, "the refused message never reached the upstream")

			// Each message counts for the whole window after the bucket it was sent in, and not a moment longer.
			refill := testStart.Add(tc.window.length + tc.window.bucket)
			h.clock.set(refill.Add(-time.Nanosecond))
			requireReply(t, sendOne(client), 450, smtp.EnhancedCode{4, 7, 1})
			h.clock.set(refill)
			for i := range tc.limit {
				require.NoError(t, sendOne(client), "after the window, message %d of %d", i+1, tc.limit)
			}
			requireReply(t, sendOne(client), 450, smtp.EnhancedCode{4, 7, 1})
		})
	}
}

func TestARateOfZeroSendsNothing(t *testing.T) {
	limits := roomy()
	limits.MessagesPerMinute = 0
	h := newHarness(t, testOrg("org-acme", acmePassword, limits))
	requireReply(t, sendOne(h.signedIn(t, "org-acme", acmePassword)), 450, smtp.EnhancedCode{4, 7, 1})
}

func TestOneOrganizationAtItsLimitDoesNotSlowAnother(t *testing.T) {
	limits := roomy()
	limits.MessagesPerMinute = 2
	limits.Connections = 1
	h := newHarness(t, testOrg("org-acme", acmePassword, limits), testOrg("org-other", otherPassword, limits))
	acme := h.signedIn(t, "org-acme", acmePassword)
	require.NoError(t, sendOne(acme))
	require.NoError(t, sendOne(acme))
	requireReply(t, sendOne(acme), 450, smtp.EnhancedCode{4, 7, 1})
	requireReply(t, h.connect(t).Auth(sasl.NewPlainClient("", "org-acme", acmePassword)), 454, smtp.EnhancedCode{4, 7, 0})

	other := h.signedIn(t, "org-other", otherPassword)
	require.NoError(t, sendOne(other))
	require.NoError(t, sendOne(other))
	requireReply(t, sendOne(other), 450, smtp.EnhancedCode{4, 7, 1})

	usage := h.relay.Usage()
	require.Len(t, usage.Organizations, 2)
	for _, org := range usage.Organizations {
		require.Equal(t, int64(2), org.Accepted, org.Org)
		require.Equal(t, int64(2), org.LastMinute, org.Org)
		require.Equal(t, int64(1), org.Rejected[reasonRateMinute], org.Org)
		require.Equal(t, int64(1), org.Connections, org.Org)
	}
	require.Equal(t, int64(1), usage.Organizations[0].Rejected[reasonConnections], "only org-acme went over its connections")
}

func TestRecipientsPerMessageAreCappedExactlyAtTheLimit(t *testing.T) {
	limits := roomy()
	limits.RecipientsPerMessage = 2
	h := newHarness(t, testOrg("org-acme", acmePassword, limits))
	client := h.signedIn(t, "org-acme", acmePassword)
	require.NoError(t, client.Mail("app@acme.example", nil))
	require.NoError(t, client.Rcpt("one@example.net", nil))
	require.NoError(t, client.Rcpt("two@example.net", nil))
	requireReply(t, client.Rcpt("three@example.net", nil), 452, smtp.EnhancedCode{4, 5, 3})
	writer, err := client.Data()
	require.NoError(t, err)
	_, err = io.WriteString(writer, message("app@acme.example", "two of three"))
	require.NoError(t, err)
	require.NoError(t, writer.Close(), "the recipients that were taken are sent to")
	received := h.upstream.received()
	require.Len(t, received, 1)
	require.Equal(t, []string{"one@example.net", "two@example.net"}, received[0].to)
	require.NoError(t, send(client, "app@acme.example", []string{"three@example.net"}, message("app@acme.example", "the rest")))
}

func TestMessageSizeIsCappedExactlyAtTheLimit(t *testing.T) {
	limits := roomy()
	body := strings.Repeat(strings.Repeat("x", 76)+"\r\n", 30)
	at := message("app@acme.example", body)
	limits.MaxMessageBytes = int64(len(at))
	h := newHarness(t, testOrg("org-acme", acmePassword, limits))
	client := h.signedIn(t, "org-acme", acmePassword)

	require.NoError(t, send(client, "app@acme.example", []string{"a@example.net"}, at), "a message of exactly the limit is sent")
	require.Equal(t, []byte(at), h.upstream.received()[0].data, "and arrives as it was written")

	over := message("app@acme.example", body+"x")
	requireReply(t, send(client, "app@acme.example", []string{"a@example.net"}, over), 552, smtp.EnhancedCode{5, 3, 4})
	require.Len(t, h.upstream.received(), 1, "the upstream was cut off before the end of the message, so it kept none of it")

	err := client.Mail("app@acme.example", &smtp.MailOptions{Size: int64(len(at)) + 1})
	requireReply(t, err, 552, smtp.EnhancedCode{5, 3, 4})
	require.NoError(t, client.Reset())

	bigHeader := strings.Repeat("X-Filler: "+strings.Repeat("y", 70)+"\r\n", 40) + at
	requireReply(t, send(client, "app@acme.example", []string{"a@example.net"}, bigHeader), 552, smtp.EnhancedCode{5, 3, 4})

	// None of the refused messages was counted against the organization's rates.
	usage := h.relay.Usage().Organizations[0]
	require.Equal(t, int64(1), usage.LastMinute)
	require.Equal(t, int64(3), usage.Rejected[reasonSize])
}

func TestConnectionsPerOrganizationAreCappedAndGivenBack(t *testing.T) {
	limits := roomy()
	limits.Connections = 2
	h := newHarness(t, testOrg("org-acme", acmePassword, limits))
	first := h.signedIn(t, "org-acme", acmePassword)
	h.signedIn(t, "org-acme", acmePassword)
	third := h.connect(t)
	requireReply(t, third.Auth(sasl.NewPlainClient("", "org-acme", acmePassword)), 454, smtp.EnhancedCode{4, 7, 0})

	require.NoError(t, first.Quit())
	require.Eventually(t, func() bool { return h.relay.Usage().Organizations[0].Connections == 1 }, 5*time.Second, 10*time.Millisecond)
	require.NoError(t, third.Auth(sasl.NewPlainClient("", "org-acme", acmePassword)), "a connection that closed gives its place back")
}

func TestTooManyConnectionsAltogetherAreToldToComeBack(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	limited := LimitConnections(listener, 1)
	defer func() { _ = limited.Close() }()
	go func() {
		for {
			conn, err := limited.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(io.Discard, conn) }()
		}
	}()
	first, err := net.Dial("tcp", listener.Addr().String())
	require.NoError(t, err)
	defer func() { _ = first.Close() }()
	time.Sleep(50 * time.Millisecond)
	second, err := net.Dial("tcp", listener.Addr().String())
	require.NoError(t, err)
	defer func() { _ = second.Close() }()
	reply, err := io.ReadAll(second)
	require.NoError(t, err)
	require.Equal(t, "421 4.7.0 Too many connections; try again later\r\n", string(reply))
}

func TestSendersAreCheckedInTheEnvelopeAndTheHeader(t *testing.T) {
	listed := testOrg("org-acme", acmePassword, roomy())
	listed.AllowedSenderDomains = []string{"acme.example"}
	required := testOrg("org-strict", otherPassword, roomy())
	required.RequireSenderDomain = true
	open := testOrg("org-open", "open-password", roomy())
	h := newHarness(t, listed, required, open)

	acme := h.signedIn(t, "org-acme", acmePassword)
	to := []string{"someone@example.net"}
	require.NoError(t, send(acme, "app@acme.example", to, message("app@acme.example", "ok")))
	require.NoError(t, send(acme, "app@mail.acme.example", to, message("App <app@mail.acme.example>", "a domain below")))
	requireReply(t, send(acme, "app@evil.example", to, message("app@acme.example", "")), 550, smtp.EnhancedCode{5, 7, 1})
	requireReply(t, send(acme, "app@notacme.example", to, message("app@acme.example", "")), 550, smtp.EnhancedCode{5, 7, 1})
	requireReply(t, send(acme, "", to, message("app@acme.example", "")), 550, smtp.EnhancedCode{5, 7, 1})
	requireReply(t, send(acme, "app@acme.example", to, message("ceo@bank.example", "")), 550, smtp.EnhancedCode{5, 7, 1})
	requireReply(t, send(acme, "app@acme.example", to, message("app@acme.example, ceo@bank.example", "")), 550, smtp.EnhancedCode{5, 7, 1})
	withSender := "Sender: ceo@bank.example\r\n" + message("app@acme.example", "")
	requireReply(t, send(acme, "app@acme.example", to, withSender), 550, smtp.EnhancedCode{5, 7, 1})
	requireReply(t, send(acme, "app@acme.example", to, "Subject: no From\r\n\r\nbody\r\n"), 550, smtp.EnhancedCode{5, 7, 1})
	require.Len(t, h.upstream.received(), 2)

	strict := h.signedIn(t, "org-strict", otherPassword)
	requireReply(t, send(strict, "app@strict.example", to, message("app@strict.example", "")), 550, smtp.EnhancedCode{5, 7, 1})

	anyone := h.signedIn(t, "org-open", "open-password")
	require.NoError(t, send(anyone, "", to, message("whoever@anywhere.example", "no list, not required")))
	require.Len(t, h.upstream.received(), 3)
	require.Equal(t, int64(7), h.relay.Usage().Organizations[0].Rejected[reasonSenderDomain])
}

func TestRecipientsAtDeniedDomainsAreRefused(t *testing.T) {
	h := newHarness(t, testOrg("org-acme", acmePassword, roomy()))
	h.reconfigure(func(c *Config) { c.DeniedRecipientDomains = []string{"blocked.example"} })
	client := h.signedIn(t, "org-acme", acmePassword)
	require.NoError(t, client.Mail("app@acme.example", nil))
	requireReply(t, client.Rcpt("someone@blocked.example", nil), 550, smtp.EnhancedCode{5, 7, 1})
	requireReply(t, client.Rcpt("someone@mx.BLOCKED.example", nil), 550, smtp.EnhancedCode{5, 7, 1})
	require.NoError(t, client.Rcpt("someone@notblocked.example", nil))
	var reply *smtp.SMTPError
	require.True(t, errorsAs(client.Rcpt("no-domain", nil), &reply))
	require.Equal(t, 501, reply.Code, "an address without a domain is refused")
	writer, err := client.Data()
	require.NoError(t, err)
	_, _ = io.WriteString(writer, message("app@acme.example", "x"))
	require.NoError(t, writer.Close())
	require.Equal(t, []string{"someone@notblocked.example"}, h.upstream.received()[0].to)
}

func TestNothingIsAnsweredBeforeTheUpstreamTookTheMessage(t *testing.T) {
	h := newHarness(t, testOrg("org-acme", acmePassword, roomy()))
	release := make(chan struct{})
	h.upstream.set(func(u *fakeUpstream) { u.hold = release })
	client := h.signedIn(t, "org-acme", acmePassword)
	done := make(chan error, 1)
	go func() { done <- sendOne(client) }()
	select {
	case err := <-done:
		t.Fatalf("the relay answered (%v) before its upstream had the message", err)
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-done)
	require.Len(t, h.upstream.received(), 1)
}

func TestAnUpstreamThatIsDownIsATemporaryFailureAndGivesTheCountBack(t *testing.T) {
	limits := roomy()
	limits.MessagesPerMinute = 1
	h := newHarness(t, testOrg("org-acme", acmePassword, limits))
	client := h.signedIn(t, "org-acme", acmePassword)
	h.upstream.stop()
	requireReply(t, sendOne(client), 451, smtp.EnhancedCode{4, 4, 1})

	// The message was not sent, so it does not count: once the upstream is back the organization's one message goes.
	up := newFakeUpstream(t, nil)
	h.reconfigure(func(c *Config) { c.Upstream.Port = up.port() })
	require.NoError(t, sendOne(client))
	require.Len(t, up.received(), 1)
	requireReply(t, sendOne(client), 450, smtp.EnhancedCode{4, 7, 1})
	usage := h.relay.Usage().Organizations[0]
	require.Equal(t, int64(1), usage.UpstreamFailures[upstreamUnreachable])
	require.Equal(t, int64(1), usage.Accepted)
}

func TestAnUpstreamThatDefersOrRefusesIsPassedOn(t *testing.T) {
	h := newHarness(t, testOrg("org-acme", acmePassword, roomy()))
	client := h.signedIn(t, "org-acme", acmePassword)

	h.upstream.set(func(u *fakeUpstream) {
		u.dataErr = &smtp.SMTPError{Code: 421, EnhancedCode: smtp.EnhancedCode{4, 3, 2}, Message: "Account 123456789012 is throttled"}
	})
	err := sendOne(client)
	requireReply(t, err, 451, smtp.EnhancedCode{4, 3, 2})
	require.NotContains(t, err.Error(), "123456789012", "what the upstream says is not passed on: it can name the platform's account")

	h.upstream.set(func(u *fakeUpstream) {
		u.dataErr = &smtp.SMTPError{Code: 554, EnhancedCode: smtp.EnhancedCode{5, 7, 1}, Message: "Email address is not verified"}
	})
	requireReply(t, sendOne(client), 554, smtp.EnhancedCode{5, 7, 1})

	h.upstream.set(func(u *fakeUpstream) {
		u.dataErr = nil
		u.rcptErr = func(to string) error {
			if strings.HasPrefix(to, "gone@") {
				return &smtp.SMTPError{Code: 550, EnhancedCode: smtp.EnhancedCode{5, 1, 1}, Message: "No such user"}
			}
			return nil
		}
	})
	requireReply(t, send(client, "app@acme.example", []string{"ok@example.net", "gone@example.net"}, message("app@acme.example", "")),
		550, smtp.EnhancedCode{5, 1, 1})
	require.Empty(t, h.upstream.received(), "one recipient refused fails the whole message")

	h.upstream.set(func(u *fakeUpstream) {
		u.rcptErr = nil
		u.login = &UpstreamCredentials{Username: "relay", Password: "upstream-password"}
	})
	requireReply(t, sendOne(client), 451, smtp.EnhancedCode{4, 7, 0})
	h.relay.SetUpstreamCredentials(&UpstreamCredentials{Username: "relay", Password: "wrong"})
	requireReply(t, sendOne(client), 451, smtp.EnhancedCode{4, 7, 0})
	h.relay.SetUpstreamCredentials(&UpstreamCredentials{Username: "relay", Password: "upstream-password"})
	require.NoError(t, sendOne(client), "with the upstream's login the relay signs in and sends")

	usage := h.relay.Usage().Organizations[0]
	require.Equal(t, map[string]int64{upstreamDeferred: 1, upstreamRefused: 2, upstreamAuth: 2}, usage.UpstreamFailures)
	require.Equal(t, int64(1), usage.LastMinute, "only the message that was sent counts")
}

// toLoopback dials every upstream at the loopback address, whatever its host is called, so that a certificate can be
// checked against a name.
func toLoopback(ctx context.Context, address string) (net.Conn, error) {
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	return (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", port))
}

func TestTLSToTheUpstreamIsRequiredAndItsCertificateChecked(t *testing.T) {
	// The test's authority signs certificates for the relay's own names, so the upstreams here are called by one of them.
	certificate, roots, _ := testCertificate(t)
	serverTLS := &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
	h := newHarnessSending(t, &smtpSender{rootCAs: roots, heloName: "relay.test", dial: toLoopback}, testOrg("org-acme", acmePassword, roomy()))
	client := h.signedIn(t, "org-acme", acmePassword)

	starttls := newFakeUpstream(t, serverTLS)
	starttls.set(func(u *fakeUpstream) {
		u.login = &UpstreamCredentials{Username: "relay", Password: "upstream-password"}
	})
	h.relay.SetUpstreamCredentials(&UpstreamCredentials{Username: "relay", Password: "upstream-password"})
	h.reconfigure(func(c *Config) {
		c.Upstream = Upstream{Host: platform.MailRelayHost(), Port: starttls.port(), TLS: TLSStartTLS}
	})
	require.NoError(t, sendOne(client), "STARTTLS with a certificate for the upstream's name, then the relay's own sign-in")
	require.Len(t, starttls.received(), 1)

	h.reconfigure(func(c *Config) { c.Upstream.Host = "smtp.elsewhere.example" })
	requireReply(t, sendOne(client), 451, smtp.EnhancedCode{4, 4, 1})
	require.Len(t, starttls.received(), 1, "a certificate that is not for the upstream's name sends nothing")

	plain := newFakeUpstream(t, nil)
	h.reconfigure(func(c *Config) {
		c.Upstream = Upstream{Host: platform.MailRelayHost(), Port: plain.port(), TLS: TLSStartTLS}
	})
	requireReply(t, sendOne(client), 451, smtp.EnhancedCode{4, 4, 1})
	require.Empty(t, plain.received(), "an upstream that does not offer STARTTLS is sent nothing")

	listener, err := tls.Listen("tcp", "127.0.0.1:0", serverTLS)
	require.NoError(t, err)
	implicit := &fakeUpstream{listener: listener, login: &UpstreamCredentials{Username: "relay", Password: "upstream-password"}}
	implicit.server = smtp.NewServer(smtp.BackendFunc(func(*smtp.Conn) (smtp.Session, error) { return &upstreamSession{up: implicit}, nil }))
	implicit.server.AllowInsecureAuth = true
	go func() { _ = implicit.server.Serve(listener) }()
	t.Cleanup(func() { _ = implicit.server.Close() })
	h.reconfigure(func(c *Config) {
		c.Upstream = Upstream{Host: platform.MailRelayHost(), Port: implicit.port(), TLS: TLSImplicit}
	})
	require.NoError(t, sendOne(client), "TLS from the first byte")
	require.Len(t, implicit.received(), 1)
}

func TestOrganizationsSendingAtOnceGetExactlyTheirLimits(t *testing.T) {
	limits := roomy()
	limits.MessagesPerMinute = 7
	orgs := []Organization{testOrg("org-a", "password-a", limits), testOrg("org-b", "password-b", limits), testOrg("org-c", "password-c", limits)}
	h := newHarness(t, orgs...)
	var accepted, refused [3]atomic.Int64
	var wg sync.WaitGroup
	for i, org := range orgs {
		for range 4 {
			wg.Go(func() {
				conn, err := net.Dial("tcp", h.addr)
				if err != nil {
					t.Error(err)
					return
				}
				client, err := smtp.NewClientStartTLS(conn, &tls.Config{RootCAs: h.roots, ServerName: platform.MailRelayHost(), MinVersion: tls.VersionTLS12})
				if err != nil {
					t.Error(err)
					return
				}
				defer func() { _ = client.Close() }()
				if err := client.Auth(sasl.NewPlainClient("", org.Name, "password-"+org.Name[len(org.Name)-1:])); err != nil {
					t.Error(err)
					return
				}
				for range 5 {
					if err := sendOne(client); err != nil {
						refused[i].Add(1)
					} else {
						accepted[i].Add(1)
					}
				}
			})
		}
	}
	wg.Wait()
	for i := range orgs {
		require.Equal(t, int64(7), accepted[i].Load(), orgs[i].Name)
		require.Equal(t, int64(13), refused[i].Load(), orgs[i].Name)
	}
	require.Len(t, h.upstream.received(), 21)
}

func TestSecretsAreNeverWrittenToTheLog(t *testing.T) {
	upstreamPassword := "upstream-secret-password-0123456789"
	h := newHarness(t, testOrg("org-acme", acmePassword, roomy()))
	h.upstream.set(func(u *fakeUpstream) { u.login = &UpstreamCredentials{Username: "relay", Password: upstreamPassword} })
	h.relay.SetUpstreamCredentials(&UpstreamCredentials{Username: "relay", Password: upstreamPassword})

	require.NoError(t, sendOne(h.signedIn(t, "org-acme", acmePassword)))
	requireReply(t, h.connect(t).Auth(sasl.NewPlainClient("", "org-acme", acmePassword+"typo")), 535, smtp.EnhancedCode{5, 7, 8})
	// A password typed where the username goes is not logged either.
	requireReply(t, h.connect(t).Auth(sasl.NewPlainClient("", otherPassword, "x")), 535, smtp.EnhancedCode{5, 7, 8})
	login := h.connect(t)
	requireReply(t, login.Auth(sasl.NewLoginClient("org-acme", acmePassword+"login")), 535, smtp.EnhancedCode{5, 7, 8})
	h.relay.SetUpstreamCredentials(&UpstreamCredentials{Username: "relay", Password: upstreamPassword + "-wrong"})
	requireReply(t, sendOne(h.signedIn(t, "org-acme", acmePassword)), 451, smtp.EnhancedCode{4, 7, 0})
	raw := h.raw(t)
	raw.say("EHLO client.test")
	raw.say("AUTH PLAIN " + base64.StdEncoding.EncodeToString([]byte("\x00org-acme\x00"+acmePassword)))

	logs := h.logs.String()
	require.Contains(t, logs, `"msg":"message relayed"`, "the log is being captured")
	require.Contains(t, logs, `"msg":"sign-in refused"`)
	for _, secret := range []string{
		acmePassword, otherPassword, upstreamPassword, "typo", "login\"",
		base64.StdEncoding.EncodeToString([]byte("\x00org-acme\x00" + acmePassword)),
		base64.StdEncoding.EncodeToString([]byte(acmePassword)),
		PasswordDigest(acmePassword), "hello\r\n",
	} {
		require.NotContains(t, logs, secret)
	}
}

func TestAReloadKeepsWhatWasCounted(t *testing.T) {
	limits := roomy()
	limits.MessagesPerMinute = 3
	h := newHarness(t, testOrg("org-acme", acmePassword, limits))
	client := h.signedIn(t, "org-acme", acmePassword)
	require.NoError(t, sendOne(client))
	require.NoError(t, sendOne(client))
	h.reconfigure(func(c *Config) {
		org := testOrg("org-acme", acmePassword, limits)
		org.Limits.MessagesPerMinute = 2
		c.Organizations = []Organization{org, testOrg("org-new", otherPassword, limits)}
	})
	requireReply(t, sendOne(client), 450, smtp.EnhancedCode{4, 7, 1})
	require.NoError(t, sendOne(h.signedIn(t, "org-new", otherPassword)), "a new organization signs in once the configuration has it")
}

// errorsAs is errors.As, for a test that only needs to know.
func errorsAs(err error, target **smtp.SMTPError) bool {
	reply, ok := err.(*smtp.SMTPError)
	if ok {
		*target = reply
	}
	return ok
}

func TestAMessageThatCannotBeReadIsNotTheUpstreamsFailure(t *testing.T) {
	h := newHarness(t, testOrg("org-acme", acmePassword, roomy()))
	client := h.signedIn(t, "org-acme", acmePassword)
	long := message("app@acme.example", strings.Repeat("y", 3000))
	err := send(client, "app@acme.example", []string{"a@example.net"}, long)
	require.Error(t, err, "a line over 2000 characters is not taken")
	require.Empty(t, h.upstream.received())
	usage := h.relay.Usage().Organizations[0]
	require.Empty(t, usage.UpstreamFailures, "the upstream is not blamed for it")
	require.Equal(t, int64(0), usage.LastMinute, "and it is not counted")
}
