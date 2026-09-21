package dashboard

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/degoke/geass/pkg/platform"
)

func dashboardUsersSecret(users ...dashboardUser) *corev1.Secret {
	payload, err := json.Marshal(users)
	if err != nil {
		panic(err)
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace},
		Data: map[string][]byte{
			"users":       payload,
			"session-key": []byte("0123456789abcdef0123456789abcdef"),
		},
	}
}

func TestDashboardAPIRequiresAuthentication(t *testing.T) {
	srv := &Server{Client: newFakeClient()}
	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil))
	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Contains(t, rec.Body.String(), "authentication required")
}

func TestDashboardLoginIssuesSessionCookie(t *testing.T) {
	ctx := t.Context()
	secret := dashboardUsersSecret(dashboardUser{Username: "admin", Password: "test-password", Role: dashboardRoleAdmin})
	srv := &Server{Client: newFakeClient(secret)}
	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	form := url.Values{"username": {"admin"}, "password": {"test-password"}}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(form.Encode())).WithContext(ctx))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"role":"admin"`)

	var cookie *http.Cookie
	for _, item := range rec.Result().Cookies() {
		if item.Name == platform.DashboardSessionCookie {
			cookie = item
		}
	}
	require.NotNil(t, cookie)
	require.True(t, cookie.HttpOnly)
	require.Equal(t, http.SameSiteLaxMode, cookie.SameSite)

	bootstrap := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil).WithContext(ctx)
	bootstrap.AddCookie(cookie)
	bootRec := httptest.NewRecorder()
	mux.ServeHTTP(bootRec, bootstrap)
	require.Equal(t, http.StatusOK, bootRec.Code)
	require.Contains(t, bootRec.Body.String(), `"projects"`)
	require.Contains(t, bootRec.Body.String(), `"canMutate":true`)
	require.Contains(t, bootRec.Body.String(), `"username":"admin"`)
}

func TestDashboardLoginRejectsWrongPassword(t *testing.T) {
	secret := dashboardUsersSecret(dashboardUser{Username: "admin", Password: "test-password", Role: dashboardRoleAdmin})
	srv := &Server{Client: newFakeClient(secret)}
	form := url.Values{"username": {"admin"}, "password": {"wrong"}}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(form.Encode())))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid username or password")
}

func TestDashboardViewerCannotMutate(t *testing.T) {
	ctx := t.Context()
	secret := dashboardUsersSecret(
		dashboardUser{Username: "admin", Password: "admin-pass", Role: dashboardRoleAdmin},
		dashboardUser{Username: "reports", Password: "view-pass", Role: dashboardRoleViewer},
	)
	srv := &Server{Client: newFakeClient(secret)}
	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	form := url.Values{"username": {"reports"}, "password": {"view-pass"}}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(form.Encode())).WithContext(ctx))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var cookie *http.Cookie
	for _, item := range rec.Result().Cookies() {
		if item.Name == platform.DashboardSessionCookie {
			cookie = item
		}
	}
	require.NotNil(t, cookie)

	bootstrap := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil).WithContext(ctx)
	bootstrap.AddCookie(cookie)
	bootRec := httptest.NewRecorder()
	mux.ServeHTTP(bootRec, bootstrap)
	require.Equal(t, http.StatusOK, bootRec.Code)
	require.Contains(t, bootRec.Body.String(), `"canMutate":false`)
	require.Contains(t, bootRec.Body.String(), `"role":"viewer"`)

	create := withOrigin(httptest.NewRequest(http.MethodPost, "/api/projects/create", nil).WithContext(ctx))
	create.AddCookie(cookie)
	createRec := httptest.NewRecorder()
	mux.ServeHTTP(createRec, create)
	require.Equal(t, http.StatusForbidden, createRec.Code)
	require.Contains(t, createRec.Body.String(), "viewer role")
}

func TestDashboardAuthDoesNotGeneratePassword(t *testing.T) {
	srv := &Server{Client: newFakeClient()}
	form := url.Values{"username": {"admin"}, "password": {"anything"}}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(form.Encode())))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "unavailable")
}

func TestDashboardUnknownRoleIsNotAdmin(t *testing.T) {
	secret := dashboardUsersSecret(
		dashboardUser{Username: "ops", Password: "test-unknown-role", Role: "operator"},
		dashboardUser{Username: "admin", Password: "test-password", Role: dashboardRoleAdmin},
	)
	srv := &Server{Client: newFakeClient(secret)}
	form := url.Values{"username": {"ops"}, "password": {"test-unknown-role"}}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(form.Encode())))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid username or password")
}

func TestDashboardLogoutRevokesSession(t *testing.T) {
	ctx := t.Context()
	secret := dashboardUsersSecret(dashboardUser{Username: "admin", Password: "test-password", Role: dashboardRoleAdmin})
	srv := &Server{Client: newFakeClient(secret)}
	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	form := url.Values{"username": {"admin"}, "password": {"test-password"}}
	login := withOrigin(httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(form.Encode())).WithContext(ctx))
	login.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	login.Header.Set("Accept", "application/json")
	loginRec := httptest.NewRecorder()
	mux.ServeHTTP(loginRec, login)
	require.Equal(t, http.StatusOK, loginRec.Code)

	var cookie *http.Cookie
	for _, item := range loginRec.Result().Cookies() {
		if item.Name == platform.DashboardSessionCookie {
			cookie = item
		}
	}
	require.NotNil(t, cookie)

	logout := withOrigin(httptest.NewRequest(http.MethodPost, "/api/logout", nil).WithContext(ctx))
	logout.Header.Set("Accept", "application/json")
	logout.AddCookie(cookie)
	logoutRec := httptest.NewRecorder()
	mux.ServeHTTP(logoutRec, logout)
	require.Equal(t, http.StatusOK, logoutRec.Code)

	bootstrap := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil).WithContext(ctx)
	bootstrap.AddCookie(cookie)
	bootRec := httptest.NewRecorder()
	mux.ServeHTTP(bootRec, bootstrap)
	require.Equal(t, http.StatusUnauthorized, bootRec.Code)
}

func TestDashboardLoginRateLimit(t *testing.T) {
	secret := dashboardUsersSecret(dashboardUser{Username: "admin", Password: "test-password", Role: dashboardRoleAdmin})
	srv := &Server{Client: newFakeClient(secret)}
	for i := 0; i < loginFailureLimit; i++ {
		form := url.Values{"username": {"admin"}, "password": {"wrong"}}
		req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(form.Encode())))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		req.RemoteAddr = "10.0.0.8:1234"
		rec := httptest.NewRecorder()
		srv.handleAPI(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	}
	form := url.Values{"username": {"admin"}, "password": {"test-password"}}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(form.Encode())))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.RemoteAddr = "10.0.0.8:1234"
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid username or password")
}

func TestDashboardCookieSecureOnPublicHost(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "geass.example.com"
	cookie := dashboardSessionCookie(req, "token", 60)
	require.True(t, cookie.Secure)
	require.Equal(t, http.SameSiteLaxMode, cookie.SameSite)

	loopback := httptest.NewRequest(http.MethodGet, "/", nil)
	loopback.Host = "127.0.0.1:8082"
	loopbackCookie := dashboardSessionCookie(loopback, "token", 60)
	require.False(t, loopbackCookie.Secure)
}

func TestDashboardAuthReloadsExistingSecretOnAlreadyExists(t *testing.T) {
	ctx := t.Context()
	t.Setenv("GEASS_DASHBOARD_USERNAME", "admin")
	t.Setenv("GEASS_DASHBOARD_PASSWORD", "stored-pass")
	secret := dashboardUsersSecret(dashboardUser{Username: "admin", Password: "stored-pass", Role: dashboardRoleAdmin})
	secret.Data["session-key"] = []byte(dashboardSessionKeyPrefix + hex.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	inner := newFakeClient(secret)
	srv := &Server{Client: &alreadyExistsAuthClient{Client: inner}}
	form := url.Values{"username": {"admin"}, "password": {"stored-pass"}}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(form.Encode())).WithContext(ctx))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestDashboardPlaceholderPasswordIsRejected(t *testing.T) {
	secret := dashboardUsersSecret(dashboardUser{Username: "admin", Password: dashboardPlaceholderPassword, Role: dashboardRoleAdmin})
	srv := &Server{Client: newFakeClient(secret)}
	form := url.Values{"username": {"admin"}, "password": {dashboardPlaceholderPassword}}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(form.Encode())))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "unavailable")
}

func TestDashboardSessionAllowsColonInUsername(t *testing.T) {
	ctx := t.Context()
	secret := dashboardUsersSecret(dashboardUser{Username: "ops:admin", Password: "test-password", Role: dashboardRoleAdmin})
	srv := &Server{Client: newFakeClient(secret)}
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	form := url.Values{"username": {"ops:admin"}, "password": {"test-password"}}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(form.Encode())).WithContext(ctx))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"username":"ops:admin"`)

	var cookie *http.Cookie
	for _, item := range rec.Result().Cookies() {
		if item.Name == platform.DashboardSessionCookie {
			cookie = item
		}
	}
	require.NotNil(t, cookie)
	bootstrap := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil).WithContext(ctx)
	bootstrap.AddCookie(cookie)
	bootRec := httptest.NewRecorder()
	mux.ServeHTTP(bootRec, bootstrap)
	require.Equal(t, http.StatusOK, bootRec.Code)
	require.Contains(t, bootRec.Body.String(), `"username":"ops:admin"`)
}

func TestDashboardViewerCannotReadLogs(t *testing.T) {
	ctx := t.Context()
	secret := dashboardUsersSecret(dashboardUser{Username: "reports", Password: "view-pass", Role: dashboardRoleViewer})
	srv := &Server{Client: newFakeClient(secret)}
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	form := url.Values{"username": {"reports"}, "password": {"view-pass"}}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(form.Encode())).WithContext(ctx))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	var cookie *http.Cookie
	for _, item := range rec.Result().Cookies() {
		if item.Name == platform.DashboardSessionCookie {
			cookie = item
		}
	}
	require.NotNil(t, cookie)

	logs := httptest.NewRequest(http.MethodGet, "/api/apps/demo/logs", nil).WithContext(ctx)
	logs.AddCookie(cookie)
	logsRec := httptest.NewRecorder()
	mux.ServeHTTP(logsRec, logs)
	require.Equal(t, http.StatusForbidden, logsRec.Code)

	repos := httptest.NewRequest(http.MethodGet, "/api/projects/payments/github/repos", nil).WithContext(ctx)
	repos.AddCookie(cookie)
	reposRec := httptest.NewRecorder()
	mux.ServeHTTP(reposRec, repos)
	require.Equal(t, http.StatusForbidden, reposRec.Code)
}

func TestDashboardSecurityHeaders(t *testing.T) {
	srv := &Server{Client: newFakeClient()}
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil))
	require.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	require.Equal(t, "DENY", rec.Header().Get("X-Frame-Options"))
	require.Contains(t, rec.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'")
}

func TestDashboardLoginRateLimitIsPerUsername(t *testing.T) {
	secret := dashboardUsersSecret(dashboardUser{Username: "admin", Password: "test-password", Role: dashboardRoleAdmin})
	srv := &Server{Client: newFakeClient(secret)}
	for i := 0; i < loginFailureLimit; i++ {
		form := url.Values{"username": {"admin"}, "password": {"wrong"}}
		req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(form.Encode())))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		req.RemoteAddr = fmt.Sprintf("10.0.0.%d:1234", i+2)
		rec := httptest.NewRecorder()
		srv.handleAPI(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	}
	form := url.Values{"username": {"admin"}, "password": {"test-password"}}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(form.Encode())))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.RemoteAddr = "10.8.0.1:1234"
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid username or password")
}

func TestDashboardHashesEnvUsersIntoSecret(t *testing.T) {
	ctx := t.Context()
	t.Setenv("GEASS_DASHBOARD_USERNAME", "admin")
	t.Setenv("GEASS_DASHBOARD_PASSWORD", "env-password")
	srv := &Server{Client: newFakeClient()}
	form := url.Values{"username": {"admin"}, "password": {"env-password"}}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(form.Encode())).WithContext(ctx))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	secret := &corev1.Secret{}
	require.NoError(t, srv.Client.Get(ctx, client.ObjectKey{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace}, secret))
	require.True(t, isBcryptHash(string(secret.Data["users"])) || strings.Contains(string(secret.Data["users"]), "$2"))
	require.True(t, strings.HasPrefix(string(secret.Data["session-key"]), dashboardSessionKeyPrefix))
}

func withDashboardSession(t *testing.T, srv *Server, r *http.Request, username, role string) *http.Request {
	t.Helper()
	if r.Host == "" {
		r.Host = "example.com"
	}
	auth, err := srv.dashboardAuth(r)
	require.NoError(t, err)
	token, err := issueDashboardSession(auth.sessionKey, username, role, auth.epoch(username))
	require.NoError(t, err)
	r.AddCookie(dashboardSessionCookie(r, token, 3600))
	return r
}

type alreadyExistsAuthClient struct {
	client.Client
	gets int
}

func (c *alreadyExistsAuthClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if key.Name == platform.DashboardAuthSecretName {
		c.gets++
		if c.gets == 1 {
			return apierrors.NewNotFound(schema.GroupResource{Resource: "secrets"}, key.Name)
		}
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func (c *alreadyExistsAuthClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if secret, ok := obj.(*corev1.Secret); ok && secret.Name == platform.DashboardAuthSecretName {
		return apierrors.NewAlreadyExists(schema.GroupResource{Resource: "secrets"}, secret.Name)
	}
	return c.Client.Create(ctx, obj, opts...)
}
