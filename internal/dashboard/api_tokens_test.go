package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/apitokens"
	"github.com/degoke/geass/pkg/platform"
)

// tokenFixture is a project with a production and a staging environment, one service in each, and a
// service in an unrelated project.
type tokenFixture struct {
	srv     *Server
	handler http.Handler
}

func newTokenFixture(t *testing.T) tokenFixture {
	t.Helper()
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{DisplayName: "Payments", Environments: []string{"production", "staging"}},
	}
	app := func(name, projectName, environment string, enabled bool) *geassv1alpha1.GeassApp {
		return &geassv1alpha1.GeassApp{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace},
			Spec: geassv1alpha1.GeassAppSpec{
				Project: projectName, Environment: geassv1alpha1.GeassEnvironment(environment),
				Source: imageAppSource("nginx:alpine"), Deploy: geassv1alpha1.GeassAppDeploySpec{Enabled: enabled},
			},
		}
	}
	srv := &Server{Client: newFakeClient(
		project,
		app("api-prod", testProjectName, "production", true),
		app("api-staging", testProjectName, "staging", true),
		app("draft-prod", testProjectName, "production", false),
		app("draft-staging", testProjectName, "staging", false),
		app("other-app", "other-project", "production", true),
		roomyTestNode(),
	)}
	users := dashboardUsersSecret(
		dashboardUser{Username: "admin", Password: "test-password", Role: dashboardRoleAdmin},
		dashboardUser{Username: "viewer", Password: "test-password", Role: dashboardRoleViewer},
	)
	require.NoError(t, srv.Client.Create(context.Background(), users))
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	return tokenFixture{srv: srv, handler: mux}
}

func (f tokenFixture) do(req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func (f tokenFixture) asAdmin(t *testing.T, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	return f.do(withAdminSession(t, f.srv, req))
}

// create makes a token through the same endpoint the dashboard uses and returns the secret shown once.
func (f tokenFixture) create(t *testing.T, form url.Values) string {
	t.Helper()
	rec := f.asAdmin(t, postForm("/api/tokens/create", form))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var created struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	require.NotEmpty(t, created.Token)
	return created.Token
}

func (f tokenFixture) workspaceToken(t *testing.T, role string) string {
	t.Helper()
	return f.create(t, url.Values{"name": {"ci"}, "scope": {"workspace"}, "role": {role}})
}

func (f tokenFixture) projectToken(t *testing.T, environment string) string {
	t.Helper()
	return f.create(t, url.Values{"name": {"deploy"}, "scope": {"project"}, "project": {testProjectName}, "environment": {environment}})
}

// bearer builds a request the way a CLI would: no cookie, no Origin header, just the token.
func bearer(token, method, path string, form url.Values) *http.Request {
	var req *http.Request
	if form != nil {
		req = httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func TestAdminCreatesATokenAndOnlyItsHashIsStored(t *testing.T) {
	f := newTokenFixture(t)
	token := f.workspaceToken(t, "admin")
	require.True(t, strings.HasPrefix(token, "gsw_"))

	var secret corev1.Secret
	require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: apiTokensSecretName, Namespace: platform.SystemNamespace}, &secret))
	require.Len(t, secret.Data, 1)
	for _, raw := range secret.Data {
		require.NotContains(t, string(raw), token, "the token itself must never be stored")
		_, tokenSecret, _, _ := apitokens.Parse(token)
		require.NotContains(t, string(raw), tokenSecret)
	}

	rec := f.asAdmin(t, httptest.NewRequest(http.MethodGet, "/api/tokens", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotContains(t, rec.Body.String(), "hash", "listing must not expose stored hashes")
	require.NotContains(t, rec.Body.String(), token)
	var listed struct {
		Items []apiTokenView `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &listed))
	require.Len(t, listed.Items, 1)
	require.Equal(t, "ci", listed.Items[0].Name)
	require.Equal(t, "admin", listed.Items[0].CreatedBy)
}

func TestTokenIdentifiesItself(t *testing.T) {
	f := newTokenFixture(t)
	token := f.projectToken(t, "staging")

	rec := f.do(bearer(token, http.MethodGet, "/api/token", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var view apiTokenView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &view))
	require.Equal(t, apitokens.ScopeProject, view.Scope)
	require.Equal(t, testProjectName, view.Project)
	require.Equal(t, "staging", view.Environment)
}

func TestInvalidTokensAreRejectedEvenWithAValidSession(t *testing.T) {
	f := newTokenFixture(t)
	token := f.workspaceToken(t, "admin")
	_, record, err := apitokens.New(apitokens.Request{Name: "never stored", Scope: apitokens.ScopeWorkspace}, time.Now())
	require.NoError(t, err)
	forged := "gsw_" + record.ID + "_" + strings.Repeat("a", 40)
	_, goodSecret, _, _ := apitokens.Parse(token)
	goodID, _, _, _ := apitokens.Parse(token)

	for name, presented := range map[string]string{
		"garbage":       "not-a-token",
		"unknown id":    forged,
		"wrong secret":  "gsw_" + goodID + "_" + strings.Repeat("0", 40),
		"wrong scope":   "gsp_" + goodID + "_" + goodSecret,
		"empty bearer":  "",
		"other service": "ghp_" + goodID + "_" + goodSecret,
	} {
		t.Run(name, func(t *testing.T) {
			// A valid admin cookie must not rescue a request that presents a bad token.
			req := withAdminSession(t, f.srv, httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil))
			req.Header.Set("Authorization", "Bearer "+presented)
			rec := f.do(req)
			require.Equal(t, http.StatusUnauthorized, rec.Code, rec.Body.String())
		})
	}
}

func TestExpiredAndRevokedTokensStopWorking(t *testing.T) {
	f := newTokenFixture(t)
	token := f.workspaceToken(t, "viewer")
	require.Equal(t, http.StatusOK, f.do(bearer(token, http.MethodGet, "/api/token", nil)).Code)

	// Expire it by rewriting the stored deadline.
	id, _, _, _ := apitokens.Parse(token)
	require.NoError(t, f.srv.mutateAPITokens(context.Background(), func(records map[string]apitokens.Record) (bool, error) {
		record := records[id]
		past := time.Now().Add(-time.Minute)
		record.ExpiresAt = &past
		records[id] = record
		return true, nil
	}))
	require.Equal(t, http.StatusUnauthorized, f.do(bearer(token, http.MethodGet, "/api/token", nil)).Code)

	fresh := f.workspaceToken(t, "viewer")
	freshID, _, _, _ := apitokens.Parse(fresh)
	require.Equal(t, http.StatusOK, f.do(bearer(fresh, http.MethodGet, "/api/token", nil)).Code)
	rec := f.asAdmin(t, postForm("/api/tokens/revoke", url.Values{"id": {freshID}}))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, http.StatusUnauthorized, f.do(bearer(fresh, http.MethodGet, "/api/token", nil)).Code, "a revoked token fails immediately")

	rec = f.asAdmin(t, postForm("/api/tokens/revoke", url.Values{"id": {freshID}}))
	require.Equal(t, http.StatusNotFound, rec.Code, "revoking twice reports the token is gone")
}

func TestWorkspaceTokenFollowsItsRole(t *testing.T) {
	f := newTokenFixture(t)
	viewer := f.workspaceToken(t, "viewer")
	admin := f.workspaceToken(t, "admin")

	require.Equal(t, http.StatusOK, f.do(bearer(viewer, http.MethodGet, "/api/bootstrap", nil)).Code)
	rec := f.do(bearer(viewer, http.MethodPost, "/api/apps/api-prod/scale", url.Values{"replicas": {"2"}}))
	require.Equal(t, http.StatusForbidden, rec.Code, "a read-only token cannot change anything")
	require.Equal(t, http.StatusForbidden, f.do(bearer(viewer, http.MethodGet, "/api/apps/api-prod/variables", nil)).Code, "nor read variables")

	require.Equal(t, http.StatusOK, f.do(bearer(admin, http.MethodGet, "/api/bootstrap", nil)).Code)
	rec = f.do(bearer(admin, http.MethodPost, "/api/apps/api-prod/scale", url.Values{"replicas": {"2"}}))
	require.NotEqual(t, http.StatusForbidden, rec.Code, "an admin token may mutate without an Origin header: %s", rec.Body.String())
	require.NotEqual(t, http.StatusUnauthorized, rec.Code)
}

func TestTokensCannotManageTokens(t *testing.T) {
	f := newTokenFixture(t)
	admin := f.workspaceToken(t, "admin")
	project := f.projectToken(t, "production")

	for name, token := range map[string]string{"admin workspace token": admin, "project token": project} {
		t.Run(name, func(t *testing.T) {
			rec := f.do(bearer(token, http.MethodGet, "/api/tokens", nil))
			require.Equal(t, http.StatusForbidden, rec.Code)
			rec = f.do(bearer(token, http.MethodPost, "/api/tokens/create", url.Values{"name": {"escalate"}, "scope": {"workspace"}, "role": {"admin"}}))
			require.Equal(t, http.StatusForbidden, rec.Code, "a token must not be able to mint a more powerful token")
			rec = f.do(bearer(token, http.MethodPost, "/api/tokens/revoke", url.Values{"id": {"00000000"}}))
			require.Equal(t, http.StatusForbidden, rec.Code)
		})
	}

	// A signed-in viewer cannot list or create tokens either.
	viewerReq := withDashboardSession(t, f.srv, httptest.NewRequest(http.MethodGet, "/api/tokens", nil), "viewer", dashboardRoleViewer)
	require.Equal(t, http.StatusForbidden, f.do(viewerReq).Code)
}

func TestProjectTokenIsLimitedToItsEnvironment(t *testing.T) {
	f := newTokenFixture(t)
	token := f.projectToken(t, "production")
	get := func(path string) int { return f.do(bearer(token, http.MethodGet, path, nil)).Code }
	post := func(path string, form url.Values) int { return f.do(bearer(token, http.MethodPost, path, form)).Code }

	require.Equal(t, http.StatusOK, get("/api/apps/api-prod/logs"))
	require.Equal(t, http.StatusOK, get("/api/apps/api-prod/variables"))
	require.Equal(t, http.StatusOK, get("/api/apps/api-prod/deployments/abc/logs"))

	for _, other := range []string{"api-staging", "other-app", "does-not-exist"} {
		require.Equal(t, http.StatusNotFound, get("/api/apps/"+other+"/logs"), other)
		require.Equal(t, http.StatusNotFound, post("/api/apps/"+other+"/deploy", url.Values{}), other)
		require.Equal(t, http.StatusNotFound, post("/api/apps/"+other+"/config/set", url.Values{"name": {"A"}, "value": {"1"}}), other)
	}
	// Moving the service out of its environment or project through the settings form is refused.
	require.Equal(t, http.StatusForbidden, post("/api/apps/api-prod/update", url.Values{"environment": {"staging"}}))
	require.Equal(t, http.StatusForbidden, post("/api/apps/api-prod/update", url.Values{"project": {"other-project"}}))
	require.Equal(t, http.StatusForbidden, post("/api/apps/api-prod/update?environment=staging", url.Values{}))
}

func TestProjectTokenCannotReachAnythingOutsideItsAllowList(t *testing.T) {
	f := newTokenFixture(t)
	token := f.projectToken(t, "production")

	for _, request := range []struct{ method, path string }{
		{http.MethodGet, "/api/bootstrap"},
		{http.MethodGet, "/api/events"},
		{http.MethodGet, "/api/templates"},
		{http.MethodGet, "/api/settings/github"},
		{http.MethodGet, "/api/apps/api-prod/console/ws"},
		{http.MethodGet, "/api/projects/" + testProjectName + "/metrics"},
		{http.MethodPost, "/api/apps/create"},
		{http.MethodPost, "/api/apps/api-prod/delete"},
		{http.MethodPost, "/api/apps/api-prod/attach"},
		{http.MethodPost, "/api/apps/api-prod/console/create"},
		{http.MethodPost, "/api/apps/api-prod/networking/delete"},
		{http.MethodPost, "/api/databases/create"},
		{http.MethodPost, "/api/projects/create"},
		{http.MethodPost, "/api/projects/" + testProjectName + "/settings/delete"},
		{http.MethodPost, "/api/projects/" + testProjectName + "/environments/create"},
		{http.MethodPost, "/api/projects/" + testProjectName + "/webhooks/save"},
		{http.MethodPost, "/api/projects/" + testProjectName + "/variables/save"},
		{http.MethodPost, "/api/projects/" + testProjectName + "/deploy"},
		{http.MethodPost, "/api/settings/github/clear"},
		{http.MethodPost, "/api/cloud-connections/create"},
		{http.MethodDelete, "/api/apps/api-prod"},
		{http.MethodPut, "/api/apps/api-prod/deploy"},
	} {
		var form url.Values
		if request.method != http.MethodGet {
			form = url.Values{}
		}
		rec := f.do(bearer(token, request.method, request.path, form))
		require.Equal(t, http.StatusForbidden, rec.Code, "%s %s: %s", request.method, request.path, rec.Body.String())
	}

	// JSON bodies are refused instead of being half understood.
	req := httptest.NewRequest(http.MethodPost, "/api/apps/api-prod/deploy", strings.NewReader(`{"environment":"staging"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	require.Equal(t, http.StatusUnsupportedMediaType, f.do(req).Code)
}

func TestProjectTokenRouteTable(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		app          string
		ok           bool
	}{
		{"GET", "/api/token", "", true},
		{"GET", "/api/token/services", "", true},
		{"POST", "/api/token/deploy", "", true},
		{"POST", "/api/token", "", false},
		{"GET", "/api/apps/a/logs", "a", true},
		{"GET", "/api/apps/a/logs/", "a", true},
		{"GET", "/api/apps/a/deployments/d1/logs", "a", true},
		{"GET", "/api/apps/a/deployments/logs", "", false},
		{"GET", "/api/apps/a/deployments/d1/x/logs", "", false},
		{"POST", "/api/apps/a/deploy", "a", true},
		{"POST", "/api/apps/a/config/set", "a", true},
		{"POST", "/api/apps/a/deploy ", "", false},
		{"POST", "/api/apps//deploy", "", false},
		{"POST", "/api/apps/a", "", false},
		{"POST", "/api/apps/a/delete", "", false},
		{"GET", "/api/apps/a/deploy", "", false},
		{"POST", "/api/apps/a/logs", "", false},
		{"GET", "/api/appsx/a/logs", "", false},
		{"GET", "/apps/a/logs", "", false},
	} {
		route, ok := projectTokenRoute(tc.method, tc.path)
		require.Equal(t, tc.ok, ok, "%s %q", tc.method, tc.path)
		if ok {
			require.Equal(t, tc.app, route.app, "%s %q", tc.method, tc.path)
		}
	}
}

func TestProjectTokenDeploysOnlyItsOwnEnvironment(t *testing.T) {
	f := newTokenFixture(t)
	token := f.projectToken(t, "production")

	rec := f.do(bearer(token, http.MethodPost, "/api/token/deploy", url.Values{"environment": {"staging"}}))
	require.Equal(t, http.StatusForbidden, rec.Code, "naming another environment is refused")

	rec = f.do(bearer(token, http.MethodPost, "/api/token/deploy", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var result struct {
		Deployed int `json:"deployed"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	require.Equal(t, 1, result.Deployed)

	enabled := func(name string) bool {
		var app geassv1alpha1.GeassApp
		require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, &app))
		return app.Spec.Deploy.Enabled
	}
	require.True(t, enabled("draft-prod"), "the draft in the token's environment is deployed")
	require.False(t, enabled("draft-staging"), "drafts in other environments are untouched")

	workspace := f.workspaceToken(t, "admin")
	require.Equal(t, http.StatusForbidden, f.do(bearer(workspace, http.MethodPost, "/api/token/deploy", nil)).Code,
		"workspace tokens deploy through the project route instead")
}

func TestTokenServicesListsOnlyWhatTheTokenCanSee(t *testing.T) {
	f := newTokenFixture(t)
	token := f.projectToken(t, "production")

	rec := f.do(bearer(token, http.MethodGet, "/api/token/services", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var listed struct {
		Environment string         `json:"environment"`
		Services    []tokenService `json:"services"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &listed))
	require.Equal(t, "production", listed.Environment)
	var ids []string
	for _, service := range listed.Services {
		ids = append(ids, service.ID)
	}
	require.ElementsMatch(t, []string{"api-prod", "draft-prod"}, ids)

	workspace := f.workspaceToken(t, "viewer")
	require.Equal(t, http.StatusBadRequest, f.do(bearer(workspace, http.MethodGet, "/api/token/services", nil)).Code, "workspace tokens must name a project")
	rec = f.do(bearer(workspace, http.MethodGet, "/api/token/services?project="+testProjectName+"&environment=staging", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "api-staging")
	require.NotContains(t, rec.Body.String(), "api-prod")
}

func TestCreatingTokensValidatesTheRequest(t *testing.T) {
	f := newTokenFixture(t)
	for name, form := range map[string]url.Values{
		"missing name":        {"scope": {"workspace"}},
		"unknown scope":       {"name": {"x"}, "scope": {"team"}},
		"unknown project":     {"name": {"x"}, "scope": {"project"}, "project": {"nope"}, "environment": {"production"}},
		"unknown environment": {"name": {"x"}, "scope": {"project"}, "project": {testProjectName}, "environment": {"qa"}},
		"project no env":      {"name": {"x"}, "scope": {"project"}, "project": {testProjectName}},
		"bad expiry":          {"name": {"x"}, "scope": {"workspace"}, "expiresInDays": {"soon"}},
		"negative expiry":     {"name": {"x"}, "scope": {"workspace"}, "expiresInDays": {"-3"}},
	} {
		t.Run(name, func(t *testing.T) {
			rec := f.asAdmin(t, postForm("/api/tokens/create", form))
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
		})
	}
	listed, err := f.srv.listAPITokens(context.Background())
	require.NoError(t, err)
	require.Empty(t, listed, "no token is stored for a rejected request")
}

func TestCookieSessionsStillNeedASameOriginRequest(t *testing.T) {
	f := newTokenFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/api/tokens/create", strings.NewReader(url.Values{"name": {"x"}, "scope": {"workspace"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "https://evil.example")
	req = withAdminSession(t, f.srv, req)
	rec := f.do(req)
	require.NotEqual(t, http.StatusOK, rec.Code, "the token endpoints keep the dashboard's cross-site protection for cookies")
	listed, err := f.srv.listAPITokens(context.Background())
	require.NoError(t, err)
	require.Empty(t, listed)
}

func TestTokenUseIsRecordedAtMostOncePerInterval(t *testing.T) {
	f := newTokenFixture(t)
	token := f.workspaceToken(t, "viewer")
	id, _, _, _ := apitokens.Parse(token)
	lastUsed := func() *time.Time {
		records, err := f.srv.listAPITokens(context.Background())
		require.NoError(t, err)
		return records[id].LastUsedAt
	}
	require.Nil(t, lastUsed())

	require.Equal(t, http.StatusOK, f.do(bearer(token, http.MethodGet, "/api/token", nil)).Code)
	first := lastUsed()
	require.NotNil(t, first)

	require.Equal(t, http.StatusOK, f.do(bearer(token, http.MethodGet, "/api/token", nil)).Code)
	require.Equal(t, first, lastUsed(), "a second request inside the interval does not rewrite the Secret")
}

func TestRemovingAnEnvironmentOrProjectRevokesItsProjectTokens(t *testing.T) {
	f := newTokenFixture(t)
	production := f.projectToken(t, "production")
	staging := f.projectToken(t, "staging")
	workspace := f.workspaceToken(t, "viewer")
	status := func(token string) int { return f.do(bearer(token, http.MethodGet, "/api/token", nil)).Code }

	f.srv.deleteAPITokensFor(context.Background(), testProjectName, "staging")
	require.Equal(t, http.StatusOK, status(production))
	require.Equal(t, http.StatusUnauthorized, status(staging))
	require.Equal(t, http.StatusOK, status(workspace), "workspace tokens are not tied to a project")

	f.srv.deleteAPITokensFor(context.Background(), testProjectName, "")
	require.Equal(t, http.StatusUnauthorized, status(production))
	require.Equal(t, http.StatusOK, status(workspace))
}

func TestTokenCountIsCapped(t *testing.T) {
	f := newTokenFixture(t)
	require.NoError(t, f.srv.mutateAPITokens(context.Background(), func(records map[string]apitokens.Record) (bool, error) {
		for i := 0; i < apitokens.MaxTokens; i++ {
			id := strings.Repeat("0", 6) + string(rune('a'+i/26)) + string(rune('a'+i%26))
			records[id] = apitokens.Record{ID: id, Name: id, Scope: apitokens.ScopeWorkspace, Role: apitokens.RoleViewer, Hash: "00"}
		}
		return true, nil
	}))
	rec := f.asAdmin(t, postForm("/api/tokens/create", url.Values{"name": {"one too many"}, "scope": {"workspace"}}))
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
}

func TestBearerTokenParsing(t *testing.T) {
	for header, want := range map[string]struct {
		token     string
		presented bool
	}{
		"Bearer abc":   {"abc", true},
		"bearer abc":   {"abc", true},
		"BEARER  abc ": {"abc", true},
		"Bearer":       {"", true},
		"Bearer ":      {"", true},
		"Bearerabc":    {"", false},
		"Basic abc":    {"", false},
		"":             {"", false},
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		token, presented := bearerToken(req)
		require.Equal(t, want.presented, presented, "%q", header)
		require.Equal(t, want.token, token, "%q", header)
	}
}
