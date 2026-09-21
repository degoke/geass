package dashboard

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRequireMutationGETRedirectsAwayFromSave(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/settings/github/save", nil)
	ok := requireMutation(rec, req, "/settings/github")
	require.False(t, ok)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Equal(t, "/settings/github", rec.Header().Get("Location"))
}

func TestRedirectProbeEncodesMessage(t *testing.T) {
	rec := httptest.NewRecorder()
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/settings/github/save", nil))
	redirectProbe(rec, req, "/settings/github", "error", "type remove-github to confirm")
	require.Equal(t, http.StatusSeeOther, rec.Code)
	loc := rec.Header().Get("Location")
	require.True(t, strings.HasPrefix(loc, "/settings/github?"))
	u, err := url.Parse(loc)
	require.NoError(t, err)
	require.Equal(t, "error", u.Query().Get("probe"))
	require.Equal(t, "type remove-github to confirm", u.Query().Get("message"))
}

func TestRedirectProbeHXUsesHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/settings/domain/save", nil))
	req.Header.Set("HX-Request", "true")
	redirectProbe(rec, req, "/settings/domain", "error", "enter your main domain")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("HX-Redirect"), "/settings/domain?")
	require.Contains(t, rec.Header().Get("HX-Redirect"), "probe=error")
}

func TestRedirectFormErrorUsesCurrentPage(t *testing.T) {
	rec := httptest.NewRecorder()
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/apps/create", nil))
	req.Header.Set("HX-Current-URL", "http://localhost:8082/projects/payments?panel=apps")
	req.Host = "localhost:8082"
	redirectFormError(rec, req, "/apps", "name is required")
	require.Equal(t, http.StatusSeeOther, rec.Code)
	loc := rec.Header().Get("Location")
	u, err := url.Parse(loc)
	require.NoError(t, err)
	require.Equal(t, "/projects/payments", u.Path)
	require.Equal(t, "apps", u.Query().Get("panel"))
	require.Equal(t, "name is required", u.Query().Get("error"))
}

func TestJSONMutationResponsesDoNotRedirect(t *testing.T) {
	rec := httptest.NewRecorder()
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/apps/create", nil))
	req.Header.Set("Accept", "application/json")
	redirect(rec, req, "/projects/demo")

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.JSONEq(t, `{"ok":true}`, rec.Body.String())
}

func TestAPIMutationValidationReturnsJSON(t *testing.T) {
	srv := &Server{Client: newFakeClient()}
	rec := httptest.NewRecorder()
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/api/apps/create", nil))

	srv.handleAPI(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.JSONEq(t, `{"error":"name, project, and a source are required"}`, rec.Body.String())
}

func TestRedirectFormErrorHXSetsRedirectHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/apps/create", nil))
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-Current-URL", "http://geass.test/projects/demo")
	req.Host = "geass.test"
	redirectFormError(rec, req, "/apps", "image is required")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Header().Get("HX-Redirect"), "error=image+is+required")
	require.Contains(t, rec.Header().Get("HX-Redirect"), "/projects/demo")
}

func TestFlashAlertRendersErrorAndProbe(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/apps?error=boom", nil)
	require.Contains(t, flashAlert(req), "boom")
	require.Contains(t, flashAlert(req), `role="alert"`)

	probe := httptest.NewRequest(http.MethodGet, "/settings/domain?probe=success", nil)
	require.Contains(t, flashAlert(probe), "Verification succeeded")
}

func TestFormReturnPathIgnoresMutationReferer(t *testing.T) {
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/apps/create", nil))
	req.Header.Set("Referer", "http://localhost/apps/create")
	req.Host = "localhost"
	require.Equal(t, "/apps", formReturnPath(req, "/apps"))
}

func TestGitHubSaveGETRedirectsToSettingsPage(t *testing.T) {
	srv := &Server{Client: newFakeClient()}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/settings/github/save", nil)
	srv.handlePlatformGitHubSettingsSave(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Equal(t, "/settings/github", rec.Header().Get("Location"))
}

func TestDomainSaveGETRedirectsToSettingsPage(t *testing.T) {
	srv := &Server{Client: newFakeClient()}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/settings/domain/save", nil)
	srv.handlePlatformDomainSave(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Equal(t, "/settings/domain", rec.Header().Get("Location"))
}

func TestRequireMutationRejectsMissingOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/settings/github/save", nil)
	req.Host = "geass.example.com"
	rec := httptest.NewRecorder()
	require.False(t, requireMutation(rec, req, "/settings/github"))
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.Contains(t, rec.Header().Get("Location"), "request+origin")
}

func TestRequireMutationAcceptsSameOrigin(t *testing.T) {
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/settings/github/save", nil))
	req.Host = "example.com"
	rec := httptest.NewRecorder()
	require.True(t, requireMutation(rec, req, "/settings/github"))
}

func TestRequireMutationAcceptsForwardedHost(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/settings/github/save", nil)
	req.Host = "geass-dashboard.geass-system.svc:8082"
	req.Header.Set("Origin", "https://geass.example.com")
	req.Header.Set("X-Forwarded-Host", "geass.example.com")
	rec := httptest.NewRecorder()
	require.True(t, requireMutation(rec, req, "/settings/github"))
}

func TestRequireMutationAcceptsForwardedHeaderHost(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/settings/github/save", nil)
	req.Host = "10.0.0.12:8082"
	req.Header.Set("Origin", "https://geass.example.com")
	req.Header.Set("Forwarded", `for=10.1.1.1;host=geass.example.com;proto=https`)
	rec := httptest.NewRecorder()
	require.True(t, requireMutation(rec, req, "/settings/github"))
}

func TestRequireMutationRejectsCrossOriginWithUnrelatedForwardedHost(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/settings/github/save", nil)
	req.Host = "geass.example.com"
	req.Header.Set("Origin", "https://attacker.example")
	req.Header.Set("X-Forwarded-Host", "geass.example.com")
	rec := httptest.NewRecorder()
	require.False(t, requireMutation(rec, req, "/settings/github"))
}

func TestDeleteFormDoesNotPushURL(t *testing.T) {
	html := deleteForm("/apps/x/delete", "x")
	require.Contains(t, html, `hx-push-url="false"`)
	require.Contains(t, html, `hx-swap="none"`)
	require.Contains(t, html, `name="confirmName"`)
	require.Contains(t, html, "Type x to confirm")
	require.NotContains(t, html, `name="confirmName" value="x"`)
	require.NotContains(t, html, `hx-push-url="true"`)
}

func TestAppConfigFormErrorReturnsPanelAlertForHX(t *testing.T) {
	srv := &Server{Client: newFakeClient()}
	rec := httptest.NewRecorder()
	req := withOrigin(httptest.NewRequest(http.MethodPost, "/apps/demo/config/set", nil))
	req.Header.Set("HX-Request", "true")
	srv.appConfigFormError(rec, req, "demo", "key is required")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "key is required")
	require.Contains(t, rec.Body.String(), `role="alert"`)
}
