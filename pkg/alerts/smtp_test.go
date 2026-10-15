package alerts

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSignsInOverSTARTTLSAndSendsNothing(t *testing.T) {
	server := newFakeSMTP(t, func(f *fakeSMTP) {
		f.offerSTARTTLS, f.auth, f.user, f.pass, f.requireTLSForAuth = true, "PLAIN LOGIN", "geass", "s3cret", true
	})

	require.NoError(t, server.checker().Verify(context.Background(), server.smtp(SecuritySTARTTLS)))
	require.Empty(t, server.received(), "a check is not a message")
}

func TestSignsInOverImplicitTLS(t *testing.T) {
	server := newFakeSMTP(t, func(f *fakeSMTP) {
		f.implicitTLS, f.auth, f.user, f.pass = true, "PLAIN", "geass", "s3cret"
	})

	require.NoError(t, server.checker().Verify(context.Background(), server.smtp(SecurityTLS)))
}

func TestEveryWayOfSigningInTheServerOffersIsTried(t *testing.T) {
	for _, mechanisms := range []string{"PLAIN", "LOGIN"} {
		t.Run(mechanisms, func(t *testing.T) {
			server := newFakeSMTP(t, func(f *fakeSMTP) {
				f.offerSTARTTLS, f.auth, f.user, f.pass = true, mechanisms, "geass", "s3cret"
			})
			require.NoError(t, server.checker().Verify(context.Background(), server.smtp(SecuritySTARTTLS)))
		})
	}
}

// The form refuses a login without encryption, but the settings can be written to the platform configuration by hand.
func TestAPasswordIsNeverSentWithoutEncryption(t *testing.T) {
	server := newFakeSMTP(t, func(f *fakeSMTP) {
		f.auth, f.user, f.pass = "PLAIN", "geass", "s3cret"
	})

	err := server.checker().Verify(context.Background(), server.smtp(SecurityNone))

	require.ErrorContains(t, err, "not encrypted")
}

func TestAServerWithoutALoginNeedsNone(t *testing.T) {
	server := newFakeSMTP(t, nil)
	settings := server.smtp(SecurityNone)
	settings.Username = ""

	require.NoError(t, server.checker().Verify(context.Background(), settings))
}

func TestAWrongPasswordIsSaidSoAndTheSecretIsNotInTheError(t *testing.T) {
	server := newFakeSMTP(t, func(f *fakeSMTP) {
		f.offerSTARTTLS, f.auth, f.user, f.pass = true, "PLAIN", "geass", "right"
	})
	settings := server.smtp(SecuritySTARTTLS)
	settings.Password = "wrong"

	err := server.checker().Verify(context.Background(), settings)

	require.ErrorContains(t, err, "refused the username and password")
	require.NotContains(t, err.Error(), "wrong")
}

func TestSTARTTLSAskedForOfAServerThatDoesNotOfferItIsExplained(t *testing.T) {
	server := newFakeSMTP(t, nil)

	err := server.checker().Verify(context.Background(), server.smtp(SecuritySTARTTLS))

	require.ErrorContains(t, err, "does not offer STARTTLS")
	require.ErrorContains(t, err, "port 465")
}

func TestACertificateThatIsNotTrustedIsRefused(t *testing.T) {
	server := newFakeSMTP(t, func(f *fakeSMTP) { f.offerSTARTTLS = true })
	checker := server.checker()
	checker.TLSConfig = nil // the system's roots: the fake's certificate is not among them

	err := checker.Verify(context.Background(), server.smtp(SecuritySTARTTLS))

	require.ErrorContains(t, err, "could not set up TLS")
}

func TestAnUnreachableServerIsReported(t *testing.T) {
	server := newFakeSMTP(t, nil)
	checker := server.checker()
	require.NoError(t, server.listener.Close())

	err := checker.Verify(context.Background(), server.smtp(SecurityNone))

	require.ErrorContains(t, err, "could not connect to mail.test:2525")
}

func TestACheckEndsWhenItsContextDoes(t *testing.T) {
	server := newFakeSMTP(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	require.Error(t, server.checker().Verify(ctx, server.smtp(SecurityNone)))
}

func TestSMTPSettingsAreCheckedAgainstWhatAlertmanagerCanDo(t *testing.T) {
	require.NoError(t, ValidateSMTP("smtp.example.com", 587, SecuritySTARTTLS, "user", "alerts@example.com"))
	require.NoError(t, ValidateSMTP("smtp.example.com", 465, SecurityTLS, "user", "Geass <alerts@example.com>"))
	require.NoError(t, ValidateSMTP("mail.internal", 25, SecurityNone, "", "alerts@example.com"))

	for name, call := range map[string]func() error{
		"empty host":       func() error { return ValidateSMTP("", 587, SecuritySTARTTLS, "", "a@example.com") },
		"host with a port": func() error { return ValidateSMTP("smtp.example.com:587", 587, SecuritySTARTTLS, "", "a@example.com") },
		"host with a scheme": func() error {
			return ValidateSMTP("smtp://smtp.example.com", 587, SecuritySTARTTLS, "", "a@example.com")
		},
		"host with a template":        func() error { return ValidateSMTP("{{.x}}", 587, SecuritySTARTTLS, "", "a@example.com") },
		"port zero":                   func() error { return ValidateSMTP("smtp.example.com", 0, SecuritySTARTTLS, "", "a@example.com") },
		"unknown security":            func() error { return ValidateSMTP("smtp.example.com", 587, "ssl", "", "a@example.com") },
		"starttls on the tls port":    func() error { return ValidateSMTP("smtp.example.com", 465, SecuritySTARTTLS, "", "a@example.com") },
		"tls on another port":         func() error { return ValidateSMTP("smtp.example.com", 587, SecurityTLS, "", "a@example.com") },
		"a password with no security": func() error { return ValidateSMTP("smtp.example.com", 25, SecurityNone, "user", "a@example.com") },
		"a sender that is no address": func() error { return ValidateSMTP("smtp.example.com", 587, SecuritySTARTTLS, "", "not an address") },
		"a template in a username":    func() error { return ValidateSMTP("smtp.example.com", 587, SecuritySTARTTLS, "{{x}}", "a@example.com") },
	} {
		require.Error(t, call(), name)
	}
}
