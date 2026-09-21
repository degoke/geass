package dashboard

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/githubapp"
	"github.com/degoke/geass/pkg/platform"
	"github.com/stretchr/testify/require"
)

type rewriteHostTransport struct {
	target *url.URL
}

func (t rewriteHostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = t.target.Scheme
	req.URL.Host = t.target.Host
	return http.DefaultTransport.RoundTrip(req)
}

func githubTestHTTPClient(serverURL string) *http.Client {
	target, err := url.Parse(serverURL)
	if err != nil {
		panic(err)
	}
	return &http.Client{Transport: rewriteHostTransport{target: target}}
}

func testGitHubAppConfig(t *testing.T) githubapp.Config {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
	return githubapp.Config{
		AppID:         12345,
		ClientID:      "test-client-id",
		ClientSecret:  "test-client-secret",
		Slug:          "geass",
		PrivateKeyPEM: string(pemBytes),
		WebhookSecret: "whsec_test",
		PublicBaseURL: "https://geass.test",
	}
}

func testPlatformGitHubSecret(t *testing.T, cfg githubapp.Config) *corev1.Secret {
	t.Helper()
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: platformGitHubAppSecretName, Namespace: platform.SystemNamespace},
		Data: map[string][]byte{
			githubapp.SecretKeyAppID:         []byte("12345"),
			githubapp.SecretKeyClientID:      []byte(cfg.ClientID),
			githubapp.SecretKeyClientSecret:  []byte(cfg.ClientSecret),
			githubapp.SecretKeySlug:          []byte(cfg.Slug),
			githubapp.SecretKeyPrivateKey:    []byte(cfg.PrivateKeyPEM),
			githubapp.SecretKeyWebhookSecret: []byte(cfg.WebhookSecret),
		},
	}
}

func testPlatformConfig(dashboardURL string) *geassv1alpha1.GeassPlatformConfig {
	cfg := &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassPlatformConfigSpec{
			DashboardURL: dashboardURL,
			GitHubAppRef: &corev1.LocalObjectReference{Name: platformGitHubAppSecretName},
		},
	}
	if dashboardURL != "" {
		cfg.Status.Conditions = platform.SetCondition(nil, platform.ConditionDashboardDomainReady, metav1.ConditionTrue, "Verified", "Dashboard domain is configured")
	}
	return cfg
}

func TestGitHubDeployShowsDashboardURLPrerequisite(t *testing.T) {
	ctx := context.Background()
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{"dev"}},
	}
	srv := &Server{Client: newFakeClient(project)}
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil).WithContext(ctx))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"hasDashboardURL":false`)
	require.Contains(t, rec.Body.String(), testProjectName)
}

func TestGitHubDeployShowsGitHubAppPrerequisite(t *testing.T) {
	ctx := context.Background()
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{"dev"}},
	}
	platformConfig := testPlatformConfig("https://geass.test")
	srv := &Server{Client: newFakeClient(project, platformConfig)}
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil).WithContext(ctx))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"hasDashboardURL":true`)
	require.Contains(t, rec.Body.String(), `"hasGitHubApp":false`)
}

func TestGitHubDeployShowsConnectPanelWhenPlatformReady(t *testing.T) {
	ctx := context.Background()
	cfg := testGitHubAppConfig(t)
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{"dev"}},
	}
	srv := &Server{Client: newFakeClient(project, testPlatformConfig("https://geass.test"), testPlatformGitHubSecret(t, cfg))}
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil).WithContext(ctx))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"hasDashboardURL":true`)
	require.Contains(t, rec.Body.String(), `"hasGitHubApp":true`)
}

func TestGitHubDeployListsRepositoriesWhenConnected(t *testing.T) {
	ctx := context.Background()
	cfg := testGitHubAppConfig(t)
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassProjectSpec{
			ClusterRef:          corev1.LocalObjectReference{Name: testClusterName},
			Environments:        []string{"dev"},
			GitHubConnectionRef: &corev1.LocalObjectReference{Name: "payments-github"},
		},
	}
	tokenSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-github-token", Namespace: platform.SystemNamespace},
		Data: map[string][]byte{
			"token":           []byte("test-token"),
			"installation_id": []byte("42"),
			"account":         []byte("geass-dev"),
			"account_type":    []byte("User"),
		},
	}
	connection := &geassv1alpha1.GeassGitHubConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-github", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassGitHubConnectionSpec{
			SecretRef:        corev1.LocalObjectReference{Name: "payments-github-token"},
			WebhookSecretRef: &corev1.LocalObjectReference{Name: platformGitHubAppSecretName},
		},
		Status: geassv1alpha1.GeassGitHubConnectionStatus{Ready: true},
	}

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/installations/42/access_tokens":
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "fresh-token"})
		case "/installation/repositories":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"repositories": []map[string]any{
					{"full_name": "geass-dev/api", "default_branch": "main", "private": true, "html_url": "https://github.com/geass-dev/api"},
					{"full_name": "geass-dev/web", "default_branch": "main", "private": false, "html_url": "https://github.com/geass-dev/web"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()

	srv := &Server{
		Client:     newFakeClient(project, testPlatformConfig("https://geass.test"), testPlatformGitHubSecret(t, cfg), tokenSecret, connection),
		HTTPClient: githubTestHTTPClient(api.URL),
	}

	rec := httptest.NewRecorder()
	srv.handleAPI(rec, httptest.NewRequest(http.MethodGet, "/api/projects/payments/github/repos", nil).WithContext(ctx))
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, "geass-dev/api")
	require.Contains(t, body, "geass-dev/web")
}

func TestGitHubCallbackCreatesConnectionAndRedirects(t *testing.T) {
	ctx := context.Background()
	cfg := testGitHubAppConfig(t)
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{"dev"}},
	}
	c := newFakeClient(project, testPlatformConfig("https://geass.test"), testPlatformGitHubSecret(t, cfg), dashboardUsersSecret(dashboardUser{Username: "admin", Password: "test-password", Role: dashboardRoleAdmin}))

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/app/installations/99":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 99,
				"account": map[string]string{
					"login": "geass-dev",
					"type":  "User",
				},
			})
		case "/app/installations/99/access_tokens":
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "installation-token"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer api.Close()

	srv := &Server{
		Client:     c,
		HTTPClient: githubTestHTTPClient(api.URL),
	}
	gh := &githubapp.Client{Config: cfg, HTTP: srv.HTTPClient}
	state, err := gh.SignState(testProjectName, "dev")
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	req := withOrigin(httptest.NewRequest(http.MethodGet, "/github/callback?installation_id=99&state="+url.QueryEscape(state), nil).WithContext(ctx))
	req = withDashboardSession(t, srv, req, "admin", dashboardRoleAdmin)
	srv.handleGitHubCallback(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Contains(t, rec.Header().Get("Location"), "create=app-git")

	var updated geassv1alpha1.GeassProject
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: testProjectName, Namespace: platform.SystemNamespace}, &updated))
	require.NotNil(t, updated.Spec.GitHubConnectionRef)
	require.Equal(t, "payments-github", updated.Spec.GitHubConnectionRef.Name)

	var connection geassv1alpha1.GeassGitHubConnection
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "payments-github", Namespace: platform.SystemNamespace}, &connection))
	require.Equal(t, "payments-github-token", connection.Spec.SecretRef.Name)
	require.Equal(t, platformGitHubAppSecretName, connection.Spec.WebhookSecretRef.Name)

	var tokenSecret corev1.Secret
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "payments-github-token", Namespace: platform.SystemNamespace}, &tokenSecret))
	require.Equal(t, "99", string(tokenSecret.Data["installation_id"]))
}

func TestListGitHubRepositoriesPaginates(t *testing.T) {
	ctx := context.Background()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/repos" {
			http.NotFound(w, r)
			return
		}
		page := r.URL.Query().Get("page")
		if page == "" || page == "1" {
			repos := make([]map[string]string, 100)
			for i := range repos {
				repos[i] = map[string]string{"full_name": "org/repo-" + string(rune('a'+i%26)), "default_branch": "main"}
			}
			_ = json.NewEncoder(w).Encode(repos)
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]string{{"full_name": "org/extra", "default_branch": "main"}})
	}))
	defer api.Close()

	connection := &geassv1alpha1.GeassGitHubConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-github", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassGitHubConnectionSpec{SecretRef: corev1.LocalObjectReference{Name: "payments-github-token"}},
	}
	tokenSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-github-token", Namespace: platform.SystemNamespace},
		Data:       map[string][]byte{"token": []byte("legacy-token")},
	}

	srv := &Server{
		Client:     newFakeClient(connection, tokenSecret),
		HTTPClient: githubTestHTTPClient(api.URL),
	}
	repos, err := srv.listGitHubRepositories(ctx, connection, "legacy-token")
	require.NoError(t, err)
	require.Len(t, repos, 101)
}

func TestGitHubAPIRequestReadsBody(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"login":"geass"}`)
	}))
	defer api.Close()

	srv := &Server{HTTPClient: githubTestHTTPClient(api.URL)}
	data, err := srv.githubAPIRequestLegacy(context.Background(), "token", "/user")
	require.NoError(t, err)
	require.Contains(t, string(data), "geass")
}

func TestGitHubCallbackRequiresAdminSession(t *testing.T) {
	srv := &Server{Client: newFakeClient()}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/github/callback?installation_id=99&state=nope", nil)
	srv.handleGitHubCallback(rec, req)
	require.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestGitHubInstallRedirectsToGitHubApp(t *testing.T) {
	ctx := context.Background()
	cfg := testGitHubAppConfig(t)
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{"dev"}},
	}
	srv := &Server{
		Client: newFakeClient(project, testPlatformConfig("https://geass.test"), testPlatformGitHubSecret(t, cfg)),
	}
	form := url.Values{"environment": {"dev"}}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/projects/payments/github/install", strings.NewReader(form.Encode())).WithContext(ctx))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handleProjectRoutes(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	location := rec.Header().Get("Location")
	require.True(t, strings.HasPrefix(location, "https://github.com/apps/geass/installations/new"))
	require.Contains(t, location, "state=")
}

func TestGitHubInstallJSONReturnsURL(t *testing.T) {
	ctx := context.Background()
	cfg := testGitHubAppConfig(t)
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{"dev"}},
	}
	srv := &Server{
		Client: newFakeClient(project, testPlatformConfig("https://geass.test"), testPlatformGitHubSecret(t, cfg)),
	}
	form := url.Values{"environment": {"dev"}}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/projects/payments/github/install", strings.NewReader(form.Encode())).WithContext(ctx))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"url"`)
	require.Contains(t, rec.Body.String(), "https://github.com/apps/geass/installations/new")
}

func TestPlatformGitHubSettingsRequiresDashboardURL(t *testing.T) {
	srv := &Server{Client: newFakeClient()}
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, httptest.NewRequest(http.MethodGet, "/api/settings/github", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"hasDashboardURL":false`)
}

func TestPlatformGitHubSettingsShowsCallbackURLs(t *testing.T) {
	srv := &Server{Client: newFakeClient(testPlatformConfig("https://geass.test"))}
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, httptest.NewRequest(http.MethodGet, "/api/settings/github", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, "https://geass.test/github/callback")
	require.Contains(t, body, "https://geass.test/webhooks/github")
}

func TestPlatformGitHubSettingsSaveStoresSecret(t *testing.T) {
	ctx := context.Background()
	cfg := testGitHubAppConfig(t)
	c := newFakeClient(testPlatformConfig("https://geass.test"))
	srv := &Server{Client: c}

	form := url.Values{
		"appID":         {"12345"},
		"clientID":      {cfg.ClientID},
		"slug":          {cfg.Slug},
		"clientSecret":  {cfg.ClientSecret},
		"webhookSecret": {cfg.WebhookSecret},
		"privateKey":    {cfg.PrivateKeyPEM},
	}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/settings/github/save", strings.NewReader(form.Encode())).WithContext(ctx))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handlePlatformGitHubSettingsSave(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)

	var secret corev1.Secret
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platformGitHubAppSecretName, Namespace: platform.SystemNamespace}, &secret))
	require.Equal(t, cfg.ClientID, string(secret.Data[githubapp.SecretKeyClientID]))

	var config geassv1alpha1.GeassPlatformConfig
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, &config))
	require.Equal(t, platformGitHubAppSecretName, config.Spec.GitHubAppRef.Name)
}

func TestPlatformDomainSaveStoresDomain(t *testing.T) {
	ctx := context.Background()
	c := newFakeClient()
	srv := &Server{Client: c}
	form := url.Values{"domain": {"geass.example.com"}}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/settings/domain/save", strings.NewReader(form.Encode())).WithContext(ctx))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handlePlatformDomainSave(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)

	var config geassv1alpha1.GeassPlatformConfig
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, &config))
	require.Equal(t, "example.com", config.Spec.RootDomain)
	require.Equal(t, "https://geass.example.com", config.Spec.DashboardURL)
	require.False(t, platform.IsConditionTrue(config.Status.Conditions, platform.ConditionDashboardDomainReady))
}

func TestPlatformDomainSaveRejectsInvalidDomain(t *testing.T) {
	ctx := context.Background()
	srv := &Server{Client: newFakeClient()}
	form := url.Values{"domain": {"not a domain"}}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/settings/domain/save", strings.NewReader(form.Encode())).WithContext(ctx))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handlePlatformDomainSave(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Contains(t, rec.Header().Get("Location"), "example.com")
}
