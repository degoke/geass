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
	"time"

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

func tokenFixture(t *testing.T, minted string, withApp bool) (*TokenResolver, *corev1.Secret, *int) {
	t.Helper()
	mints := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/app/installations/42/access_tokens" {
			mints++
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "new-token"})
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(api.Close)
	target, err := url.Parse(api.URL)
	require.NoError(t, err)
	data := map[string][]byte{"installation_id": []byte("42"), "token": []byte("minted-earlier")}
	if minted != "" {
		data[connectionTokenAtKey] = []byte(minted)
	}
	connection := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "payments-github-token", Namespace: platform.SystemNamespace}, Data: data}
	objects := []client.Object{connection}
	if withApp {
		objects = append(objects,
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "platform-github-app", Namespace: platform.SystemNamespace}, Data: map[string][]byte{
				SecretKeyAppID: []byte("12345"), SecretKeyClientID: []byte("client"), SecretKeyClientSecret: []byte("secret"),
				SecretKeySlug: []byte("geass"), SecretKeyPrivateKey: []byte(testPrivateKeyPEM(t)), SecretKeyWebhookSecret: []byte("whsec"),
			}},
			&geassv1alpha1.GeassPlatformConfig{ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
				Spec: geassv1alpha1.GeassPlatformConfigSpec{DashboardURL: "https://geass.test", GitHubAppRef: &corev1.LocalObjectReference{Name: "platform-github-app"}}},
		)
	}
	scheme := runtime.NewScheme()
	_ = geassv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	return &TokenResolver{Client: c, HTTP: &http.Client{Transport: roundTripHostRewrite{target: target}}}, connection, &mints
}

// Each report asks for a token. Minting is a request to GitHub and a write of the Secret, so a token minted a short while
// ago is used again.
func TestAnInstallationTokenMintedRecentlyIsUsedAgainNotMintedAgain(t *testing.T) {
	resolver, connection, mints := tokenFixture(t, time.Now().Add(-5*time.Minute).UTC().Format(time.RFC3339), true)

	for range 3 {
		token, err := resolver.ConnectionToken(context.Background(), connection)
		require.NoError(t, err)
		require.Equal(t, "minted-earlier", token)
	}

	require.Zero(t, *mints, "GitHub was not asked for another")
}

func TestAnOldInstallationTokenIsReplacedAndTheNewOneIsRemembered(t *testing.T) {
	resolver, connection, mints := tokenFixture(t, time.Now().Add(-connectionTokenReuseFor-time.Minute).UTC().Format(time.RFC3339), true)

	token, err := resolver.ConnectionToken(context.Background(), connection)
	require.NoError(t, err)
	require.Equal(t, "new-token", token)
	require.Equal(t, 1, *mints)

	stored := &corev1.Secret{}
	require.NoError(t, resolver.Client.Get(context.Background(), client.ObjectKeyFromObject(connection), stored))
	require.Equal(t, "new-token", string(stored.Data["token"]))
	require.True(t, tokenIsFresh(stored, time.Now()), "so the next report reuses it")
	again, err := resolver.ConnectionToken(context.Background(), stored)
	require.NoError(t, err)
	require.Equal(t, "new-token", again)
	require.Equal(t, 1, *mints)
}

func TestATokenWithNoRecordOfWhenItWasMintedIsNotTrusted(t *testing.T) {
	resolver, connection, mints := tokenFixture(t, "", true)

	token, err := resolver.ConnectionToken(context.Background(), connection)

	require.NoError(t, err)
	require.Equal(t, "new-token", token, "a Secret from before this was recorded is minted anew once")
	require.Equal(t, 1, *mints)
}

func TestTokenFreshness(t *testing.T) {
	now := time.Now()
	at := func(minted time.Time) *corev1.Secret {
		return &corev1.Secret{Data: map[string][]byte{connectionTokenAtKey: []byte(minted.UTC().Format(time.RFC3339))}}
	}
	require.True(t, tokenIsFresh(at(now.Add(-time.Minute)), now))
	require.False(t, tokenIsFresh(at(now.Add(-connectionTokenReuseFor)), now))
	require.False(t, tokenIsFresh(at(now.Add(time.Hour)), now), "a time in the future is not to be believed")
	require.False(t, tokenIsFresh(&corev1.Secret{}, now))
	require.False(t, tokenIsFresh(&corev1.Secret{Data: map[string][]byte{connectionTokenAtKey: []byte("yesterday")}}, now))
}
