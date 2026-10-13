package dashboard

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const (
	authFixtureAdminCred      = "geass-fixture-admin-cred"
	authFixtureViewerCred     = "geass-fixture-viewer-cred"
	authFixtureEnvCred        = "geass-fixture-env-cred"
	authFixtureStoredCred     = "geass-fixture-stored-cred"
	authFixtureOtherAdminCred = "geass-fixture-alt-admin-cred"
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
	secret := dashboardUsersSecret(dashboardUser{Username: "admin", Password: authFixtureAdminCred, Role: dashboardRoleAdmin})
	srv := &Server{Client: newFakeClient(secret)}
	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	form := url.Values{"username": {"admin"}, "password": {authFixtureAdminCred}}
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
	secret := dashboardUsersSecret(dashboardUser{Username: "admin", Password: authFixtureAdminCred, Role: dashboardRoleAdmin})
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
		dashboardUser{Username: "reports", Password: authFixtureViewerCred, Role: dashboardRoleViewer},
	)
	srv := &Server{Client: newFakeClient(secret)}
	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	form := url.Values{"username": {"reports"}, "password": {authFixtureViewerCred}}
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
		dashboardUser{Username: "admin", Password: authFixtureAdminCred, Role: dashboardRoleAdmin},
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
	secret := dashboardUsersSecret(dashboardUser{Username: "admin", Password: authFixtureAdminCred, Role: dashboardRoleAdmin})
	srv := &Server{Client: newFakeClient(secret)}
	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	form := url.Values{"username": {"admin"}, "password": {authFixtureAdminCred}}
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
	secret := dashboardUsersSecret(dashboardUser{Username: "admin", Password: authFixtureAdminCred, Role: dashboardRoleAdmin})
	srv := &Server{Client: newFakeClient(secret)}
	for range loginFailureLimit {
		form := url.Values{"username": {"admin"}, "password": {"wrong"}}
		req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(form.Encode())))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		req.RemoteAddr = "10.0.0.8:1234"
		rec := httptest.NewRecorder()
		srv.handleAPI(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	}
	form := url.Values{"username": {"admin"}, "password": {authFixtureAdminCred}}
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
	loopback.Host = "127.0.0.1:8085"
	loopbackCookie := dashboardSessionCookie(loopback, "token", 60)
	require.False(t, loopbackCookie.Secure)
}

func TestDashboardCookieSecureIgnoresForwardedHTTPOnPublicHost(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "geass.example.com"
	req.Header.Set("X-Forwarded-Proto", "http")
	req.Header.Set("Forwarded", "for=1.1.1.1;proto=http")
	cookie := dashboardSessionCookie(req, "token", 60)
	require.True(t, cookie.Secure)
}

func TestDashboardCookieSecureOnlyExemptsPlainHTTPToLoopback(t *testing.T) {
	cookieFor := func(host string, mutate func(*http.Request)) *http.Cookie {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Host = host
		if mutate != nil {
			mutate(req)
		}
		return dashboardSessionCookie(req, "token", 60)
	}
	for _, host := range []string{"localhost:8085", "LOCALHOST:8085", "127.0.0.1:8085", "[::1]:8085"} {
		require.False(t, cookieFor(host, nil).Secure, host)
	}
	// Anything that only looks like loopback, and every address that is not, gets the flag.
	for _, host := range []string{"geass.example.com", "localhost.evil.example.com", "127.0.0.1.evil.example.com", "10.0.0.5:8085", "0.0.0.0:8085", ""} {
		require.True(t, cookieFor(host, nil).Secure, host)
	}
	// HTTPS to loopback keeps the flag, whether this process terminated TLS or a proxy says it did.
	require.True(t, cookieFor("127.0.0.1:8085", func(r *http.Request) { r.TLS = &tls.ConnectionState{} }).Secure)
	require.True(t, cookieFor("127.0.0.1:8085", func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "https") }).Secure)
	require.True(t, cookieFor("127.0.0.1:8085", func(r *http.Request) { r.Header.Set("Forwarded", "proto=https") }).Secure)
	// Signing out clears the cookie with the same attributes it was set with.
	logout := httptest.NewRequest(http.MethodGet, "/", nil)
	logout.Host = "geass.example.com"
	require.True(t, dashboardSessionCookie(logout, "", -1).Secure)
}

func TestDashboardAuthReloadsExistingSecretOnAlreadyExists(t *testing.T) {
	ctx := t.Context()
	t.Setenv("GEASS_DASHBOARD_USERNAME", "admin")
	t.Setenv("GEASS_DASHBOARD_PASSWORD", authFixtureStoredCred)
	secret := dashboardUsersSecret(dashboardUser{Username: "admin", Password: authFixtureStoredCred, Role: dashboardRoleAdmin})
	secret.Data["session-key"] = []byte(dashboardSessionKeyPrefix + hex.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	inner := newFakeClient(secret)
	srv := &Server{Client: &alreadyExistsAuthClient{Client: inner}}
	form := url.Values{"username": {"admin"}, "password": {authFixtureStoredCred}}
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
	secret := dashboardUsersSecret(dashboardUser{Username: "ops:admin", Password: authFixtureAdminCred, Role: dashboardRoleAdmin})
	srv := &Server{Client: newFakeClient(secret)}
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	form := url.Values{"username": {"ops:admin"}, "password": {authFixtureAdminCred}}
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
	secret := dashboardUsersSecret(dashboardUser{Username: "reports", Password: authFixtureViewerCred, Role: dashboardRoleViewer})
	srv := &Server{Client: newFakeClient(secret)}
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	form := url.Values{"username": {"reports"}, "password": {authFixtureViewerCred}}
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

	// Listing PlanetScale sizes calls PlanetScale with the stored token, which is not for viewers.
	sizes := httptest.NewRequest(http.MethodGet, "/api/planetscale/cluster-sizes?project=payments&connection=ps&engine=MySQL", nil).WithContext(ctx)
	sizes.AddCookie(cookie)
	sizesRec := httptest.NewRecorder()
	mux.ServeHTTP(sizesRec, sizes)
	require.Equal(t, http.StatusForbidden, sizesRec.Code)

	runtime := httptest.NewRequest(http.MethodGet, "/api/apps/demo/runtime", nil).WithContext(ctx)
	runtime.AddCookie(cookie)
	runtimeRec := httptest.NewRecorder()
	mux.ServeHTTP(runtimeRec, runtime)
	require.Equal(t, http.StatusForbidden, runtimeRec.Code)

	variables := httptest.NewRequest(http.MethodGet, "/api/apps/demo/variables", nil).WithContext(ctx)
	variables.AddCookie(cookie)
	variablesRec := httptest.NewRecorder()
	mux.ServeHTTP(variablesRec, variables)
	require.Equal(t, http.StatusForbidden, variablesRec.Code)

	settings := httptest.NewRequest(http.MethodGet, "/api/settings/github", nil).WithContext(ctx)
	settings.AddCookie(cookie)
	settingsRec := httptest.NewRecorder()
	mux.ServeHTTP(settingsRec, settings)
	require.Equal(t, http.StatusForbidden, settingsRec.Code)
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
	secret := dashboardUsersSecret(dashboardUser{Username: "admin", Password: authFixtureAdminCred, Role: dashboardRoleAdmin})
	srv := &Server{Client: newFakeClient(secret)}
	for i := range loginFailureLimit {
		form := url.Values{"username": {"admin"}, "password": {"wrong"}}
		req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(form.Encode())))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Accept", "application/json")
		req.RemoteAddr = fmt.Sprintf("10.0.0.%d:1234", i+2)
		rec := httptest.NewRecorder()
		srv.handleAPI(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	}
	form := url.Values{"username": {"admin"}, "password": {authFixtureAdminCred}}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(form.Encode())))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.RemoteAddr = "10.8.0.1:1234"
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid username or password")
}

func dashboardLoginAttempt(srv *Server, username, password, remoteAddr string) *httptest.ResponseRecorder {
	form := url.Values{"username": {username}, "password": {password}}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(form.Encode())))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.RemoteAddr = remoteAddr
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	return rec
}

// storedLoginLockouts is what the Secret holds for lockouts, and how many bytes that is.
func storedLoginLockouts(t *testing.T, c client.Client) (map[string]loginAttempt, int) {
	t.Helper()
	secret := &corev1.Secret{}
	require.NoError(t, c.Get(t.Context(), client.ObjectKey{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace}, secret))
	lockouts, err := parseDashboardLoginLockouts(secret)
	require.NoError(t, err)
	return lockouts, len(secret.Data[dashboardLoginLockoutsSecretKey])
}

func seedLoginLockouts(t *testing.T, secret *corev1.Secret, lockouts map[string]loginAttempt) {
	t.Helper()
	payload, err := json.Marshal(lockouts)
	require.NoError(t, err)
	secret.Data[dashboardLoginLockoutsSecretKey] = payload
}

func TestDashboardLoginFloodOfUnknownUsernamesDoesNotGrowSecret(t *testing.T) {
	secret := dashboardUsersSecret(dashboardUser{Username: "admin", Password: authFixtureAdminCred, Role: dashboardRoleAdmin})
	srv := &Server{Client: newFakeClient(secret)}
	// A first sign-in hashes the fixture password into the Secret, so the size below is a steady baseline.
	require.Equal(t, http.StatusOK, dashboardLoginAttempt(srv, "admin", authFixtureAdminCred, "198.51.100.1:1000").Code)
	_, baseline := storedLoginLockouts(t, srv.Client)

	for i := range 2000 {
		// Spread over many addresses so it is the Secret logic, not the per-address limiter, that is tested.
		remote := fmt.Sprintf("198.51.%d.%d:1000", i/250, i%250+1)
		rec := dashboardLoginAttempt(srv, fmt.Sprintf("ghost-%d", i), "guess", remote)
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.Contains(t, rec.Body.String(), "invalid username or password")
	}

	lockouts, size := storedLoginLockouts(t, srv.Client)
	require.Empty(t, lockouts)
	require.Equal(t, baseline, size)
	srv.loginMu.Lock()
	defer srv.loginMu.Unlock()
	require.Empty(t, srv.loginFailures)
	require.LessOrEqual(t, len(srv.loginSources), loginSourceMaxEntries)
}

func TestDashboardLoginLockoutStoresOnlyConfiguredUsers(t *testing.T) {
	secret := dashboardUsersSecret(dashboardUser{Username: "admin", Password: authFixtureAdminCred, Role: dashboardRoleAdmin})
	srv := &Server{Client: newFakeClient(secret)}
	require.Equal(t, http.StatusBadRequest, dashboardLoginAttempt(srv, "ghost", "guess", "198.51.100.1:1000").Code)
	require.Equal(t, http.StatusBadRequest, dashboardLoginAttempt(srv, " ADMIN ", "guess", "198.51.100.1:1000").Code)

	lockouts, _ := storedLoginLockouts(t, srv.Client)
	require.Len(t, lockouts, 1)
	require.Equal(t, 1, lockouts["admin"].Count)
}

func TestDashboardLoginLockoutSurvivesRestart(t *testing.T) {
	secret := dashboardUsersSecret(dashboardUser{Username: "admin", Password: authFixtureAdminCred, Role: dashboardRoleAdmin})
	c := newFakeClient(secret)
	first := &Server{Client: c}
	for range loginFailureLimit {
		require.Equal(t, http.StatusBadRequest, dashboardLoginAttempt(first, "admin", "guess", "198.51.100.1:1000").Code)
	}

	// A new Server has none of the in-memory state, only what the Secret kept.
	second := &Server{Client: c}
	rec := dashboardLoginAttempt(second, "admin", authFixtureAdminCred, "198.51.100.2:1000")
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid username or password")
	require.Empty(t, rec.Result().Cookies())
}

func TestDashboardLoginLockoutsDropExpiredRecords(t *testing.T) {
	secret := dashboardUsersSecret(
		dashboardUser{Username: "admin", Password: authFixtureAdminCred, Role: dashboardRoleAdmin},
		dashboardUser{Username: "viewer", Password: authFixtureViewerCred, Role: dashboardRoleViewer},
	)
	now := time.Now()
	stale := now.Add(-time.Hour)
	seed := map[string]loginAttempt{"viewer": {Count: loginFailureLimit, WindowStart: now, LockedUntil: now.Add(loginLockout)}}
	// What the Secret of an operator that met the old code can hold: a record per name an attacker tried.
	for i := range 100 {
		seed[fmt.Sprintf("ghost-%d", i)] = loginAttempt{Count: loginFailureLimit, WindowStart: stale, LockedUntil: stale.Add(loginLockout)}
	}
	seedLoginLockouts(t, secret, seed)
	srv := &Server{Client: newFakeClient(secret)}

	require.Equal(t, http.StatusBadRequest, dashboardLoginAttempt(srv, "admin", "guess", "198.51.100.1:1000").Code)

	lockouts, _ := storedLoginLockouts(t, srv.Client)
	require.Len(t, lockouts, 2)
	require.Contains(t, lockouts, "admin")
	require.Contains(t, lockouts, "viewer")
	// A lock that is still running is not the kind of record that gets dropped.
	locked, err := srv.loginLocked(httptest.NewRequest(http.MethodPost, "/api/login", nil), "viewer")
	require.NoError(t, err)
	require.True(t, locked)
}

func TestDashboardLoginLockoutsAreCapped(t *testing.T) {
	secret := dashboardUsersSecret(dashboardUser{Username: "admin", Password: authFixtureAdminCred, Role: dashboardRoleAdmin})
	// More records than the cap, all inside their window so none of them has expired. A higher number began later.
	base := time.Now().Add(-30 * time.Second)
	total := loginLockoutMaxEntries + 50
	seed := map[string]loginAttempt{}
	for i := range total {
		seed[fmt.Sprintf("user-%03d", i)] = loginAttempt{Count: 1, WindowStart: base.Add(time.Duration(i) * time.Millisecond)}
	}
	seedLoginLockouts(t, secret, seed)
	srv := &Server{Client: newFakeClient(secret)}

	require.Equal(t, http.StatusBadRequest, dashboardLoginAttempt(srv, "admin", "guess", "198.51.100.1:1000").Code)

	lockouts, _ := storedLoginLockouts(t, srv.Client)
	require.Len(t, lockouts, loginLockoutMaxEntries)
	require.Contains(t, lockouts, "admin")
	// The record being written stays; the oldest of the rest are what goes.
	require.NotContains(t, lockouts, "user-000")
	require.NotContains(t, lockouts, "user-050")
	require.Contains(t, lockouts, "user-051")
	require.Contains(t, lockouts, fmt.Sprintf("user-%03d", total-1))
}

func TestDashboardLoginSourceLimit(t *testing.T) {
	secret := dashboardUsersSecret(dashboardUser{Username: "admin", Password: authFixtureAdminCred, Role: dashboardRoleAdmin})
	srv := &Server{Client: newFakeClient(secret)}
	const flooder = "203.0.113.9:4444"
	for i := range loginSourceFailureLimit {
		rec := dashboardLoginAttempt(srv, fmt.Sprintf("ghost-%d", i), "guess", flooder)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	}

	// The address is refused now, even with the right password and whatever the username.
	rec := dashboardLoginAttempt(srv, "admin", authFixtureAdminCred, flooder)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "invalid username or password")
	require.Empty(t, rec.Result().Cookies())

	// Another address is not affected.
	require.Equal(t, http.StatusOK, dashboardLoginAttempt(srv, "admin", authFixtureAdminCred, "203.0.113.10:4444").Code)

	// The refusal ends when the lock does.
	key := loginSourceKey(&http.Request{RemoteAddr: flooder})
	past := time.Now().Add(-time.Hour)
	srv.loginMu.Lock()
	srv.loginSources[key] = loginAttempt{Count: loginSourceFailureLimit, WindowStart: past, LockedUntil: past.Add(loginLockout)}
	srv.loginMu.Unlock()
	require.Equal(t, http.StatusOK, dashboardLoginAttempt(srv, "admin", authFixtureAdminCred, flooder).Code)
}

func TestDashboardLoginSourceLimitSparesOrdinaryMistakes(t *testing.T) {
	secret := dashboardUsersSecret(dashboardUser{Username: "admin", Password: authFixtureAdminCred, Role: dashboardRoleAdmin})
	srv := &Server{Client: newFakeClient(secret)}
	const office = "203.0.113.20:4444"
	// A few mistyped names or passwords, then the right one: nobody is turned away for that.
	for i := range loginFailureLimit - 1 {
		require.Equal(t, http.StatusBadRequest, dashboardLoginAttempt(srv, fmt.Sprintf("typo-%d", i), "guess", office).Code)
	}
	require.Equal(t, http.StatusOK, dashboardLoginAttempt(srv, "admin", authFixtureAdminCred, office).Code)
}

func TestDashboardLoginSourceTableIsBounded(t *testing.T) {
	remote := func(i int) *http.Request {
		return &http.Request{RemoteAddr: fmt.Sprintf("10.%d.%d.%d:1000", i>>16&255, i>>8&255, i&255)}
	}
	srv := &Server{}
	for i := range loginSourceMaxEntries + 500 {
		srv.noteLoginSourceFailure(remote(i))
		require.LessOrEqual(t, len(srv.loginSources), loginSourceMaxEntries)
	}

	// A full table of expired entries is swept, not wiped: what is still live survives.
	swept := &Server{loginSources: map[string]loginAttempt{}}
	stale := time.Now().Add(-time.Hour)
	for i := range loginSourceMaxEntries {
		swept.loginSources[loginSourceKey(remote(i))] = loginAttempt{Count: 1, WindowStart: stale}
	}
	swept.noteLoginSourceFailure(remote(loginSourceMaxEntries + 1))
	require.Len(t, swept.loginSources, 1)
}

// The limiter and the per-username record share loginMu; this is for `go test -race`, which can only find a
// missed lock when goroutines really do run at once.
func TestDashboardLoginStateIsSafeForConcurrentUse(t *testing.T) {
	secret := dashboardUsersSecret(dashboardUser{Username: "admin", Password: authFixtureAdminCred, Role: dashboardRoleAdmin})
	srv := &Server{Client: newFakeClient(secret)}
	// Hash the fixture password once, so the goroutines below do not all race to upgrade it.
	require.Equal(t, http.StatusOK, dashboardLoginAttempt(srv, "admin", authFixtureAdminCred, "198.51.100.250:1000").Code)

	var wg sync.WaitGroup
	for worker := range 16 {
		wg.Go(func() {
			for i := range 40 {
				remote := fmt.Sprintf("192.0.%d.%d:1000", worker, i)
				srv.noteLoginSourceFailure(&http.Request{RemoteAddr: remote})
				_ = srv.loginSourceBlocked(&http.Request{RemoteAddr: remote})
				if i%10 == 0 {
					dashboardLoginAttempt(srv, fmt.Sprintf("ghost-%d-%d", worker, i), "guess", remote)
				}
			}
		})
	}
	wg.Wait()

	lockouts, _ := storedLoginLockouts(t, srv.Client)
	require.Empty(t, lockouts)
	srv.loginMu.Lock()
	defer srv.loginMu.Unlock()
	require.LessOrEqual(t, len(srv.loginSources), loginSourceMaxEntries)
}

func TestLoginSourceKey(t *testing.T) {
	key := func(remote string) string { return loginSourceKey(&http.Request{RemoteAddr: remote}) }
	require.Equal(t, key("192.0.2.7:80"), key("192.0.2.7:9999"))
	require.NotEqual(t, key("192.0.2.7:80"), key("192.0.2.8:80"))
	require.Equal(t, key("192.0.2.7:80"), key("[::ffff:192.0.2.7]:80"))
	// One IPv6 subscriber holds a /64, so the addresses inside it are one source.
	require.Equal(t, key("[2001:db8:1:2::1]:443"), key("[2001:db8:1:2:ffff:ffff:ffff:ffff]:443"))
	require.NotEqual(t, key("[2001:db8:1:2::1]:443"), key("[2001:db8:1:3::1]:443"))
}

func TestDashboardHashesEnvUsersIntoSecret(t *testing.T) {
	ctx := t.Context()
	t.Setenv("GEASS_DASHBOARD_USERNAME", "admin")
	t.Setenv("GEASS_DASHBOARD_PASSWORD", authFixtureEnvCred)
	srv := &Server{Client: newFakeClient()}
	form := url.Values{"username": {"admin"}, "password": {authFixtureEnvCred}}
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

func TestDashboardCompactEnvUsersRequireRole(t *testing.T) {
	t.Setenv("GEASS_DASHBOARD_USERS", "admin:"+authFixtureEnvCred)
	srv := &Server{Client: newFakeClient()}
	form := url.Values{"username": {"admin"}, "password": {authFixtureEnvCred}}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(form.Encode())))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "unavailable")
}

func TestDashboardCompactEnvUsersWithRole(t *testing.T) {
	t.Setenv("GEASS_DASHBOARD_USERS", "admin:"+authFixtureEnvCred+":admin")
	srv := &Server{Client: newFakeClient()}
	form := url.Values{"username": {"admin"}, "password": {authFixtureEnvCred}}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(form.Encode())))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"role":"admin"`)
}

func TestDashboardLoginLockoutRetriesSecretConflict(t *testing.T) {
	ctx := t.Context()
	secret := dashboardUsersSecret(dashboardUser{Username: "admin", Password: authFixtureAdminCred, Role: dashboardRoleAdmin})
	inner := newFakeClient(secret)
	wrapper := &conflictAuthClient{Client: inner}
	srv := &Server{Client: wrapper}
	form := url.Values{"username": {"admin"}, "password": {"wrong"}}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(form.Encode())).WithContext(ctx))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.GreaterOrEqual(t, wrapper.updates, 2)
	stored := &corev1.Secret{}
	require.NoError(t, inner.Get(ctx, client.ObjectKey{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace}, stored))
	require.Contains(t, string(stored.Data[dashboardLoginLockoutsSecretKey]), "admin")
}

func TestDashboardLoginLockoutFailsClosedWhenPersistErrors(t *testing.T) {
	ctx := t.Context()
	secret := dashboardUsersSecret(dashboardUser{Username: "admin", Password: authFixtureAdminCred, Role: dashboardRoleAdmin})
	failing := &persistFailAuthClient{Client: newFakeClient(secret)}
	srv := &Server{Client: failing}
	form := url.Values{"username": {"admin"}, "password": {"wrong"}}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(form.Encode())).WithContext(ctx))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "unavailable")
	locked, err := srv.loginLocked(req, "admin")
	require.NoError(t, err)
	require.False(t, locked)

	clearing := &Server{Client: &persistFailAuthClient{Client: newFakeClient(secret)}}
	success := url.Values{"username": {"admin"}, "password": {authFixtureAdminCred}}
	okReq := withOrigin(httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(success.Encode())).WithContext(ctx))
	okReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	okReq.Header.Set("Accept", "application/json")
	okRec := httptest.NewRecorder()
	clearing.handleAPI(okRec, okReq)
	require.Equal(t, http.StatusBadRequest, okRec.Code)
	require.Contains(t, okRec.Body.String(), "could not create a session")
	require.NotContains(t, okRec.Body.String(), `"authenticated":true`)
	require.Empty(t, okRec.Result().Cookies())
}

func TestDashboardLoginLockoutFailsClosedWhenSecretGetErrors(t *testing.T) {
	secret := dashboardUsersSecret(dashboardUser{Username: "admin", Password: authFixtureAdminCred, Role: dashboardRoleAdmin})
	srv := &Server{Client: &lockoutGetFailClient{Client: newFakeClient(secret)}}
	form := url.Values{"username": {"admin"}, "password": {authFixtureAdminCred}}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(form.Encode())))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "unavailable")
	require.NotContains(t, rec.Body.String(), `"authenticated":true`)
}

func TestDashboardLoginRejectsCaseInsensitiveDuplicateUsers(t *testing.T) {
	secret := dashboardUsersSecret(
		dashboardUser{Username: "Admin", Password: authFixtureAdminCred, Role: dashboardRoleAdmin},
		dashboardUser{Username: "admin", Password: authFixtureOtherAdminCred, Role: dashboardRoleViewer},
	)
	srv := &Server{Client: newFakeClient(secret)}
	form := url.Values{"username": {"admin"}, "password": {authFixtureAdminCred}}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(form.Encode())))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "unavailable")
}

func TestDashboardLoginLockoutFailsClosedOnCorruptJSON(t *testing.T) {
	secret := dashboardUsersSecret(dashboardUser{Username: "admin", Password: authFixtureAdminCred, Role: dashboardRoleAdmin})
	secret.Data[dashboardLoginLockoutsSecretKey] = []byte("{not-json")
	srv := &Server{Client: newFakeClient(secret)}
	form := url.Values{"username": {"admin"}, "password": {authFixtureAdminCred}}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(form.Encode())))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "unavailable")
	require.NotContains(t, rec.Body.String(), `"authenticated":true`)
}

func TestDashboardAuthFailsClosedOnCorruptSessionEpochs(t *testing.T) {
	secret := dashboardUsersSecret(dashboardUser{Username: "admin", Password: authFixtureAdminCred, Role: dashboardRoleAdmin})
	secret.Data["session-epochs"] = []byte("{not-json")
	srv := &Server{Client: newFakeClient(secret)}
	form := url.Values{"username": {"admin"}, "password": {authFixtureAdminCred}}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(form.Encode())))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "unavailable")
	require.NotContains(t, rec.Body.String(), `"authenticated":true`)

	token, err := issueDashboardSession([]byte("0123456789abcdef0123456789abcdef"), "admin", dashboardRoleAdmin, 0)
	require.NoError(t, err)
	sessionReq := httptest.NewRequest(http.MethodGet, "/api/session", nil)
	sessionReq.AddCookie(&http.Cookie{Name: platform.DashboardSessionCookie, Value: token})
	require.Nil(t, srv.currentSession(sessionReq))
}

func TestDashboardAuthFailsClosedOnCorruptUsersJSON(t *testing.T) {
	ctx := t.Context()
	t.Setenv("GEASS_DASHBOARD_USERNAME", "admin")
	t.Setenv("GEASS_DASHBOARD_PASSWORD", authFixtureEnvCred)
	secret := dashboardUsersSecret(dashboardUser{Username: "admin", Password: authFixtureAdminCred, Role: dashboardRoleAdmin})
	secret.Data["users"] = []byte("{not-json")
	srv := &Server{Client: newFakeClient(secret)}
	form := url.Values{"username": {"admin"}, "password": {authFixtureEnvCred}}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(form.Encode())).WithContext(ctx))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "unavailable")
	require.NotContains(t, rec.Body.String(), `"authenticated":true`)

	stored := &corev1.Secret{}
	require.NoError(t, srv.Client.Get(ctx, client.ObjectKey{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace}, stored))
	require.Equal(t, "{not-json", string(stored.Data["users"]))
}

func TestDashboardAuthFailsClosedWhenSecretUsersFilterEmpty(t *testing.T) {
	ctx := t.Context()
	t.Setenv("GEASS_DASHBOARD_USERNAME", "admin")
	t.Setenv("GEASS_DASHBOARD_PASSWORD", authFixtureEnvCred)
	cases := []struct {
		name string
		raw  string
	}{
		{name: "empty array", raw: "[]"},
		{name: "null", raw: "null"},
		{name: "placeholder", raw: `[{"username":"admin","password":"CHANGE_ME","role":"admin"}]`},
		{name: "unknown role", raw: fmt.Sprintf(`[{"username":"admin","password":"%s","role":"superuser"}]`, authFixtureEnvCred)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			secret := dashboardUsersSecret(dashboardUser{Username: "admin", Password: authFixtureAdminCred, Role: dashboardRoleAdmin})
			secret.Data["users"] = []byte(tc.raw)
			srv := &Server{Client: newFakeClient(secret)}
			form := url.Values{"username": {"admin"}, "password": {authFixtureEnvCred}}
			req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(form.Encode())).WithContext(ctx))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Accept", "application/json")
			rec := httptest.NewRecorder()
			srv.handleAPI(rec, req)
			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Contains(t, rec.Body.String(), "unavailable")
			require.NotContains(t, rec.Body.String(), `"authenticated":true`)

			stored := &corev1.Secret{}
			require.NoError(t, srv.Client.Get(ctx, client.ObjectKey{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace}, stored))
			require.Equal(t, tc.raw, string(stored.Data["users"]))
		})
	}
}

func TestDashboardLoginIsCaseInsensitive(t *testing.T) {
	secret := dashboardUsersSecret(dashboardUser{Username: "Admin", Password: authFixtureAdminCred, Role: dashboardRoleAdmin})
	srv := &Server{Client: newFakeClient(secret)}
	form := url.Values{"username": {"ADMIN"}, "password": {authFixtureAdminCred}}
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(form.Encode())))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	rec := httptest.NewRecorder()
	srv.handleAPI(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"authenticated":true`)
}

func TestDashboardViewerBootstrapStripsSensitiveFields(t *testing.T) {
	ctx := t.Context()
	secret := dashboardUsersSecret(dashboardUser{Username: "reports", Password: authFixtureViewerCred, Role: dashboardRoleViewer})
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "demo", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassAppSpec{
			Project:     "payments",
			Environment: geassv1alpha1.EnvironmentDev,
			Source:      geassv1alpha1.GeassAppSource{Git: &geassv1alpha1.GeassAppGitSource{Repository: "geass-dev/api", Branch: "main"}},
			ConfigData:  map[string]string{"LOG_LEVEL": "debug"},
			SecretRef:   &corev1.LocalObjectReference{Name: "demo-secrets"},
			Ingress:     geassv1alpha1.GeassAppIngressSpec{Host: "demo.example"},
		},
	}
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: "payments", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassProjectSpec{
			DisplayName:         "Payments",
			GitHubConnectionRef: &corev1.LocalObjectReference{Name: "payments-github"},
			SharedVariables:     []geassv1alpha1.GeassSharedVariable{{Name: "DATABASE_URL", Environment: "dev", Value: "postgres://secret", SecretRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "payments-shared-secrets"}, Key: "DATABASE_URL"}}},
		},
	}
	database := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassDatabaseSpec{
			Project:           "payments",
			Environment:       geassv1alpha1.EnvironmentDev,
			Engine:            geassv1alpha1.DatabaseEnginePostgres,
			Placement:         geassv1alpha1.DatabasePlacementExternal,
			Provider:          geassv1alpha1.DatabaseProviderPlanetScale,
			Mode:              geassv1alpha1.DatabaseModeCreate,
			ExternalHost:      "db.example.internal",
			ExternalPort:      3306,
			Username:          "orders-user",
			DatabaseName:      "orders_prod",
			ClusterSize:       "PS_10",
			ConnectionRef:     &corev1.LocalObjectReference{Name: "payments-ps"},
			PasswordSecretRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "orders-external"}, Key: "password"},
		},
		Status: geassv1alpha1.GeassDatabaseStatus{Host: "orders-rw", ConnectionSecret: "orders-connection"},
	}
	store := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{Name: "assets", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassObjectStoreSpec{Project: "payments", ConnectionRef: &corev1.LocalObjectReference{Name: "aws"}, Buckets: []string{"uploads"}},
		Status:     geassv1alpha1.GeassObjectStoreStatus{ConnectionSecret: "assets-connection"},
	}
	connection := &geassv1alpha1.GeassCloudConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "aws", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassCloudConnectionSpec{Provider: geassv1alpha1.CloudProviderAWS, Region: "us-east-1", SecretRef: corev1.LocalObjectReference{Name: "aws-credentials"}},
	}
	config := &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassPlatformConfigSpec{GitHubAppRef: &corev1.LocalObjectReference{Name: "github-app"}, TunnelCNAMETarget: "abc.cfargotunnel.com"},
	}
	build := &geassv1alpha1.GeassBuild{
		ObjectMeta: metav1.ObjectMeta{Name: "demo-1", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassBuildSpec{App: "demo", Repository: "geass-dev/api", Revision: "abc123"},
		Status:     geassv1alpha1.GeassBuildStatus{SourceRevision: "abc123", ImageDigest: "sha256:deadbeef"},
	}
	srv := &Server{Client: newFakeClient(secret, app, project, database, store, connection, config, build)}
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	form := url.Values{"username": {"reports"}, "password": {authFixtureViewerCred}}
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
	body := bootRec.Body.String()
	require.Contains(t, body, `"role":"viewer"`)
	require.Contains(t, body, `"hasGitHubApp":false`)
	require.Contains(t, body, `"hasDashboardURL":false`)
	require.Contains(t, body, `"dashboardURL":""`)
	require.Contains(t, body, `"name":"demo"`)
	require.Contains(t, body, `"name":"payments"`)
	require.Contains(t, body, `"name":"orders"`)
	require.Contains(t, body, `"name":"assets"`)
	require.NotContains(t, body, `"engine":"Postgres"`)
	require.NotContains(t, body, "geass-dev/api")
	require.NotContains(t, body, "demo.example")
	require.NotContains(t, body, "demo-secrets")
	require.NotContains(t, body, "LOG_LEVEL")
	require.NotContains(t, body, "postgres://secret")
	require.NotContains(t, body, "payments-github")
	require.NotContains(t, body, "orders-rw")
	require.NotContains(t, body, "orders-connection")
	require.NotContains(t, body, "db.example.internal")
	require.NotContains(t, body, "PS_10")
	require.NotContains(t, body, "orders-user")
	require.NotContains(t, body, "orders_prod")
	require.NotContains(t, body, "payments-ps")
	require.NotContains(t, body, "orders-external")
	require.NotContains(t, body, `"externalPort":3306`)
	require.NotContains(t, body, "PlanetScale")
	require.NotContains(t, body, `"placement":"External"`)
	require.NotContains(t, body, "uploads")
	require.NotContains(t, body, "assets-connection")
	require.NotContains(t, body, `"name":"aws"`)
	require.NotContains(t, body, "us-east-1")
	require.NotContains(t, body, "aws-credentials")
	require.NotContains(t, body, "github-app")
	require.NotContains(t, body, "abc.cfargotunnel.com")
	require.NotContains(t, body, "abc123")
	require.NotContains(t, body, "sha256:deadbeef")
	require.NotContains(t, body, `"name":"demo-1"`)
}

func TestViewerBootstrapDoesNotReadPlatformConfig(t *testing.T) {
	ctx := t.Context()
	secret := dashboardUsersSecret(dashboardUser{Username: "reports", Password: authFixtureViewerCred, Role: dashboardRoleViewer})
	config := &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassPlatformConfigSpec{GitHubAppRef: &corev1.LocalObjectReference{Name: "github-app"}},
	}
	counting := &getNameClient{Client: newFakeClient(secret, config)}
	srv := &Server{Client: counting}
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	form := url.Values{"username": {"reports"}, "password": {authFixtureViewerCred}}
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
	counting.names = nil
	bootstrap := httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil).WithContext(ctx)
	bootstrap.AddCookie(cookie)
	bootRec := httptest.NewRecorder()
	mux.ServeHTTP(bootRec, bootstrap)
	require.Equal(t, http.StatusOK, bootRec.Code)
	require.NotContains(t, counting.names, platform.HAReadinessName)
	require.NotContains(t, counting.names, "github-app")
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

type conflictAuthClient struct {
	client.Client
	updates int
}

func (c *conflictAuthClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if secret, ok := obj.(*corev1.Secret); ok && secret.Name == platform.DashboardAuthSecretName {
		if _, ok := secret.Data[dashboardLoginLockoutsSecretKey]; ok {
			c.updates++
			if c.updates == 1 {
				return apierrors.NewConflict(schema.GroupResource{Resource: "secrets"}, secret.Name, fmt.Errorf("conflict"))
			}
		}
	}
	return c.Client.Update(ctx, obj, opts...)
}

type persistFailAuthClient struct {
	client.Client
}

func (c *persistFailAuthClient) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if secret, ok := obj.(*corev1.Secret); ok && secret.Name == platform.DashboardAuthSecretName {
		if _, ok := secret.Data[dashboardLoginLockoutsSecretKey]; ok {
			return fmt.Errorf("persist failed")
		}
	}
	return c.Client.Update(ctx, obj, opts...)
}

type lockoutGetFailClient struct {
	client.Client
}

func (c *lockoutGetFailClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if key.Name == platform.DashboardAuthSecretName {
		return fmt.Errorf("secret get failed")
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

type getNameClient struct {
	client.Client
	names []string
}

func (c *getNameClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	c.names = append(c.names, key.Name)
	return c.Client.Get(ctx, key, obj, opts...)
}
