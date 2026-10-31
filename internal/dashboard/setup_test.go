package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/degoke/geass/pkg/platform"
)

// setupFixture is a dashboard whose auth Secret holds a setup link and the given users.
func setupFixture(t *testing.T, users ...dashboardUser) (tokenFixture, string) {
	t.Helper()
	srv := &Server{Client: newFakeClient()}
	token, value, err := platform.NewDashboardSetup(time.Now())
	require.NoError(t, err)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace},
		Data:       map[string][]byte{platform.DashboardSetupKey: value},
	}
	if len(users) > 0 {
		secret = dashboardUsersSecret(users...)
		secret.Data[platform.DashboardSetupKey] = value
	}
	require.NoError(t, srv.Client.Create(context.Background(), secret))
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	return tokenFixture{srv: srv, handler: mux}, token
}

func (f tokenFixture) authSecret(t *testing.T) *corev1.Secret {
	t.Helper()
	secret := &corev1.Secret{}
	require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace}, secret))
	return secret
}

func setupForm(token, username, password string) *http.Request {
	return postForm("/api/setup", url.Values{"token": {token}, "username": {username}, "password": {password}})
}

func sessionCookieOf(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == testSessionCookie && cookie.Value != "" {
			return cookie
		}
	}
	return nil
}

func TestASetupLinkMakesTheFirstAdminAndSignsThemIn(t *testing.T) {
	f, token := setupFixture(t)
	password := strings.Repeat("c", 14)

	rec := f.do(setupForm(token, "ada", password))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	cookie := sessionCookieOf(rec)
	require.NotNil(t, cookie)
	session := f.as(cookie, httptest.NewRequest(http.MethodGet, "/api/session", nil))
	require.Contains(t, session.Body.String(), `"username":"ada"`)
	users := f.storedUsers(t)
	require.Len(t, users, 1)
	require.Equal(t, dashboardRoleAdmin, users[0].Role)
	require.NoError(t, bcrypt.CompareHashAndPassword([]byte(users[0].Password), []byte(password)))
	require.NotContains(t, f.authSecret(t).Data, platform.DashboardSetupKey)
}

func TestASetupLinkWorksOnce(t *testing.T) {
	f, token := setupFixture(t)
	require.Equal(t, http.StatusOK, f.do(setupForm(token, "ada", strings.Repeat("c", 14))).Code)

	rec := f.do(setupForm(token, "eve", strings.Repeat("d", 14)))

	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Contains(t, rec.Body.String(), errSetupLinkInvalid.Error())
	require.Len(t, f.storedUsers(t), 1)
}

func TestAWrongOrExpiredSetupLinkIsRefused(t *testing.T) {
	f, token := setupFixture(t)
	require.Equal(t, http.StatusForbidden, f.do(setupForm(token+"x", "ada", strings.Repeat("c", 14))).Code)
	require.Contains(t, f.authSecret(t).Data, platform.DashboardSetupKey, "a wrong token leaves the link")

	_, expired, err := platform.NewDashboardSetup(time.Now().Add(-platform.DashboardSetupTTL - time.Minute))
	require.NoError(t, err)
	secret := f.authSecret(t)
	secret.Data[platform.DashboardSetupKey] = expired
	require.NoError(t, f.srv.Client.Update(context.Background(), secret))
	require.Equal(t, http.StatusForbidden, f.do(setupForm(token, "ada", strings.Repeat("c", 14))).Code)
}

func TestASetupLinkIsKeptWhenThePasswordIsRefused(t *testing.T) {
	f, token := setupFixture(t)

	rec := f.do(setupForm(token, "ada", "short"))

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, f.authSecret(t).Data, platform.DashboardSetupKey)
	require.Equal(t, http.StatusOK, f.do(setupForm(token, "ada", strings.Repeat("c", 14))).Code)
}

func TestASetupLinkResetsAnExistingUserAndEndsTheirSessions(t *testing.T) {
	old := strings.Repeat("e", 14)
	f, token := setupFixture(t, dashboardUser{Username: "admin", Password: old, Role: dashboardRoleViewer})
	before := f.signIn(t, "admin", old)
	require.NotNil(t, before)

	rec := f.do(setupForm(token, "Admin", strings.Repeat("f", 14)))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Nil(t, f.signIn(t, "admin", old))
	require.NotNil(t, f.signIn(t, "admin", strings.Repeat("f", 14)))
	require.Equal(t, http.StatusUnauthorized, f.as(before, httptest.NewRequest(http.MethodGet, "/api/users", nil)).Code)
	users := f.storedUsers(t)
	require.Len(t, users, 1)
	require.Equal(t, dashboardRoleAdmin, users[0].Role)
}

func TestASetupLinkNeedsTheDashboardOrigin(t *testing.T) {
	f, token := setupFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/api/setup", strings.NewReader(url.Values{
		"token": {token}, "username": {"ada"}, "password": {strings.Repeat("c", 14)},
	}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	rec := f.do(req)

	require.NotEqual(t, http.StatusOK, rec.Code)
	require.Contains(t, f.authSecret(t).Data, platform.DashboardSetupKey)
}
