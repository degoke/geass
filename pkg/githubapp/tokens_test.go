package githubapp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
	"github.com/stretchr/testify/require"
)

func testPrivateKeyPEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return string(pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	}))
}

func TestTokenResolverRefreshesInstallationToken(t *testing.T) {
	ctx := context.Background()
	privateKey := testPrivateKeyPEM(t)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/app/installations/42/access_tokens" {
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "fresh-token"})
			return
		}
		http.NotFound(w, r)
	}))
	defer api.Close()

	target, err := url.Parse(api.URL)
	require.NoError(t, err)
	httpClient := &http.Client{Transport: roundTripHostRewrite{target: target}}

	platformSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "platform-github-app", Namespace: platform.SystemNamespace},
		Data: map[string][]byte{
			SecretKeyAppID:         []byte("12345"),
			SecretKeyClientID:      []byte("client"),
			SecretKeyClientSecret:  []byte("secret"),
			SecretKeySlug:          []byte("geass"),
			SecretKeyPrivateKey:    []byte(privateKey),
			SecretKeyWebhookSecret: []byte("whsec"),
		},
	}
	platformConfig := &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassPlatformConfigSpec{
			DashboardURL: "https://geass.test",
			GitHubAppRef: &corev1.LocalObjectReference{Name: "platform-github-app"},
		},
	}
	connectionSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-github-token", Namespace: platform.SystemNamespace},
		Data: map[string][]byte{
			"installation_id": []byte("42"),
			"token":           []byte("stale-token"),
		},
	}

	scheme := runtime.NewScheme()
	_ = geassv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(platformConfig, platformSecret, connectionSecret).Build()

	resolver := &TokenResolver{
		Client: c,
		HTTP:   httpClient,
	}
	token, err := resolver.ConnectionToken(ctx, connectionSecret)
	require.NoError(t, err)
	require.Equal(t, "fresh-token", token)

	updated := &corev1.Secret{}
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(connectionSecret), updated))
	require.Equal(t, "fresh-token", string(updated.Data["token"]))
}

type roundTripHostRewrite struct {
	target *url.URL
}

func (t roundTripHostRewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = t.target.Scheme
	req.URL.Host = t.target.Host
	return http.DefaultTransport.RoundTrip(req)
}
