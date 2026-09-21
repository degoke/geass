package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/stretchr/testify/require"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/githubapp"
	"github.com/degoke/geass/pkg/platform"
)

func TestHandleGitHubManifestCallbackPersistsCredentials(t *testing.T) {
	ctx := context.Background()
	appConfig := testGitHubAppConfig(t)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/app-manifests/code-1/conversions", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":             4513186,
			"slug":           "geass",
			"client_id":      "Iv1.abc",
			"client_secret":  "client-secret",
			"webhook_secret": "webhook-secret",
			"pem":            appConfig.PrivateKeyPEM,
		})
	}))
	defer api.Close()

	config := testPlatformConfig("https://geass.example.com")
	config.Spec.RootDomain = "example.com"
	config.ObjectMeta.Generation = 1
	config.Status.Conditions = platform.SetConditionForGeneration(nil, platform.ConditionDashboardDomainReady, metav1.ConditionTrue, "Verified", "ok", 1)
	c := newFakeClient(config, dashboardUsersSecret(dashboardUser{Username: "admin", Password: "test-password", Role: dashboardRoleAdmin}))
	srv := &Server{
		Client:     c,
		HTTPClient: &http.Client{Transport: roundTripRewrite{target: api.URL}},
	}

	state := "0123456789abcdef0123456789abcdef"
	req := httptest.NewRequest(http.MethodGet, "/settings/github/manifest/callback?code=code-1&state="+state, nil).WithContext(ctx)
	req.AddCookie(&http.Cookie{Name: githubManifestStateCookieName(state), Value: state})
	req = withDashboardSession(t, srv, req, "admin", dashboardRoleAdmin)
	rec := httptest.NewRecorder()
	srv.handleGitHubManifestCallback(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Contains(t, rec.Header().Get("Location"), "probe=success")

	var secret corev1.Secret
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platformGitHubAppSecretName, Namespace: platform.SystemNamespace}, &secret))
	require.Equal(t, "4513186", string(secret.Data[githubapp.SecretKeyAppID]))
	require.Equal(t, "Iv1.abc", string(secret.Data[githubapp.SecretKeyClientID]))
	require.Equal(t, "geass", string(secret.Data[githubapp.SecretKeySlug]))
	require.Equal(t, "client-secret", string(secret.Data[githubapp.SecretKeyClientSecret]))

	var updated geassv1alpha1.GeassPlatformConfig
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, &updated))
	require.NotNil(t, updated.Spec.GitHubAppRef)
	require.Equal(t, platformGitHubAppSecretName, updated.Spec.GitHubAppRef.Name)
}

func TestHandleGitHubManifestCallbackRejectsStateMismatch(t *testing.T) {
	srv := &Server{Client: newFakeClient(testPlatformConfig("https://geass.example.com"), dashboardUsersSecret(dashboardUser{Username: "admin", Password: "test-password", Role: dashboardRoleAdmin}))}
	state := "0123456789abcdef0123456789abcdef"
	req := httptest.NewRequest(http.MethodGet, "/settings/github/manifest/callback?code=code-1&state="+state, nil)
	req.AddCookie(&http.Cookie{Name: githubManifestStateCookieName(state), Value: "other"})
	req = withDashboardSession(t, srv, req, "admin", dashboardRoleAdmin)
	rec := httptest.NewRecorder()
	srv.handleGitHubManifestCallback(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Contains(t, rec.Header().Get("Location"), "state+mismatch")
}

type roundTripRewrite struct {
	target string
}

func (t roundTripRewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	target, err := http.NewRequest(req.Method, t.target+req.URL.Path, req.Body)
	if err != nil {
		return nil, err
	}
	target.Header = req.Header.Clone()
	return http.DefaultTransport.RoundTrip(target)
}

func TestGitHubCredentialErrorHidesKubernetesAndCryptoText(t *testing.T) {
	require.Equal(t, "app ID, client ID, and slug are required", githubCredentialError(fmt.Errorf("app ID, client ID, and slug are required")))
	require.Equal(t, "client secret, webhook secret, and private key are required", githubCredentialError(fmt.Errorf("client secret, webhook secret, and private key are required")))
	require.Equal(t, "could not save GitHub App credentials", githubCredentialError(fmt.Errorf(`Secret "platform-github-app" is invalid: spec.data: Required value`)))
	require.Equal(t, "could not save GitHub App credentials", githubCredentialError(fmt.Errorf("x509: failed to parse private key (use ParsePKCS8PrivateKey instead for this key format)")))
}

func TestGitHubManifestStateCookieSecureOnPublicHost(t *testing.T) {
	srv := &Server{}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/settings/github", nil)
	req.Host = "geass.example.com"
	req.Header.Set("X-Forwarded-Proto", "http")
	state := srv.beginGitHubManifestState(rec, req)
	require.NotEmpty(t, state)
	var cookie *http.Cookie
	for _, item := range rec.Result().Cookies() {
		if item.Name == githubManifestStateCookieName(state) {
			cookie = item
		}
	}
	require.NotNil(t, cookie)
	require.True(t, cookie.Secure)
	require.True(t, cookie.HttpOnly)
	require.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
}
