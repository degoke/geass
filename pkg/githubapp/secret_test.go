package githubapp

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestConfigFromSecret(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	privateKey := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "platform-github-app"},
		Data: map[string][]byte{
			SecretKeyAppID:         []byte("42"),
			SecretKeyClientID:      []byte("Iv1.test"),
			SecretKeyClientSecret:  []byte("secret"),
			SecretKeySlug:          []byte("geass"),
			SecretKeyPrivateKey:    privateKey,
			SecretKeyWebhookSecret: []byte("whsec"),
		},
	}
	cfg := ConfigFromSecret(secret, "https://geass.example.com")
	require.Equal(t, int64(42), cfg.AppID)
	require.Equal(t, "Iv1.test", cfg.ClientID)
	require.Equal(t, "geass", cfg.Slug)
	require.Equal(t, "https://geass.example.com", cfg.PublicBaseURL)
	require.True(t, cfg.Configured())
}

func TestConfigValidateRejectsInvalidPrivateKeyAndNonHTTPSURL(t *testing.T) {
	cfg := Config{
		AppID:         42,
		ClientID:      "Iv1.test",
		ClientSecret:  "secret",
		Slug:          "geass",
		PrivateKeyPEM: "not-a-private-key",
		WebhookSecret: "whsec",
		PublicBaseURL: "http://geass.example.com",
	}
	require.Error(t, cfg.Validate())

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	cfg.PrivateKeyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	require.Error(t, cfg.Validate())
}
