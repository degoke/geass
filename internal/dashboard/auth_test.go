package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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

func TestDashboardAuthReloadsExistingSecretOnAlreadyExists(t *testing.T) {
	ctx := t.Context()
	secret := dashboardUsersSecret(dashboardUser{Username: "admin", Password: "stored-pass", Role: dashboardRoleAdmin})
	srv := &Server{Client: newFakeClient(secret)}
	form := url.Values{"username": {"admin"}, "password": {"stored-pass"}}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(form.Encode())).WithContext(ctx))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
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
