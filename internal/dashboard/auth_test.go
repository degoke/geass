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
	require.Equal(t, http.SameSiteStrictMode, cookie.SameSite)

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
