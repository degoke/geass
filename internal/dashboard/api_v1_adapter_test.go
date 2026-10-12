package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// jsonRequest builds a request the way a program would: a JSON body and a bearer token.
func jsonRequest(token, method, path string, body any) *http.Request {
	var reader *strings.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = strings.NewReader(string(raw))
	} else {
		reader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) (message, code string) {
	t.Helper()
	require.Contains(t, rec.Header().Get("Content-Type"), "application/json")
	var payload struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload), rec.Body.String())
	return payload.Error, payload.Code
}

func TestAJSONBodyReachesTheHandlerAsAForm(t *testing.T) {
	f := newTokenFixture(t)
	token := f.workspaceToken(t, "admin")

	rec := f.do(jsonRequest(token, http.MethodPost, "/api/v1/apps/draft-prod/scale", map[string]any{"replicas": 3}))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var app geassv1alpha1.GeassApp
	require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: "draft-prod", Namespace: platform.SystemNamespace}, &app))
	require.NotNil(t, app.Spec.Replicas)
	require.EqualValues(t, 3, *app.Spec.Replicas, "a JSON number is the form value the handler reads")
}

func TestV1CreatesWithPostOnTheCollection(t *testing.T) {
	f := newTokenFixture(t)
	token := f.workspaceToken(t, "admin")

	rec := f.do(jsonRequest(token, http.MethodPost, "/api/v1/apps", map[string]any{
		"project": testProjectName, "environment": "production", "name": "fresh", "source": "image", "image": "nginx:alpine", "port": 8080, "draft": true,
	}))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var app geassv1alpha1.GeassApp
	require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: "fresh", Namespace: platform.SystemNamespace}, &app))
	require.Equal(t, "nginx:alpine", app.Spec.Source.Image.Image)
	require.EqualValues(t, 8080, app.Spec.Port)
}

func TestV1DeletesWithDeleteAndAConfirmation(t *testing.T) {
	f := newTokenFixture(t)
	token := f.workspaceToken(t, "admin")

	refused := f.do(jsonRequest(token, http.MethodDelete, "/api/v1/apps/draft-prod", nil))
	require.Equal(t, http.StatusBadRequest, refused.Code, "the handler's confirmation rule still applies")
	message, code := decodeError(t, refused)
	require.NotEmpty(t, message)
	require.Equal(t, "invalid_request", code)

	deleted := f.do(jsonRequest(token, http.MethodDelete, "/api/v1/apps/draft-prod?confirmName=draft-prod", nil))
	require.Equal(t, http.StatusOK, deleted.Code, deleted.Body.String())
}

func TestV1UpdatesWithPatch(t *testing.T) {
	f := newTokenFixture(t)
	token := f.workspaceToken(t, "admin")

	rec := f.do(jsonRequest(token, http.MethodPatch, "/api/v1/apps/draft-prod", map[string]any{"port": 9090}))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var app geassv1alpha1.GeassApp
	require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: "draft-prod", Namespace: platform.SystemNamespace}, &app))
	require.EqualValues(t, 9090, app.Spec.Port)
}

func TestFormBodiesStillWorkOnV1(t *testing.T) {
	f := newTokenFixture(t)
	token := f.workspaceToken(t, "admin")

	rec := f.do(bearer(token, http.MethodPost, "/api/v1/apps/draft-prod/scale", url.Values{"replicas": {"2"}}))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func TestErrorsFromEveryLayerHaveOneShape(t *testing.T) {
	f := newTokenFixture(t)
	token := f.workspaceToken(t, "admin")
	viewer := f.workspaceToken(t, "viewer")

	for name, tt := range map[string]struct {
		req          *http.Request
		status       int
		wantCode     string
		wantContains string
	}{
		"no credentials":     {httptest.NewRequest(http.MethodPost, "/api/v1/apps/draft-prod/scale", nil), http.StatusUnauthorized, "unauthenticated", "authentication"},
		"a bad token":        {jsonRequest("gsw_00000000_"+strings.Repeat("0", 40), http.MethodPost, "/api/v1/apps/draft-prod/scale", map[string]any{}), http.StatusUnauthorized, "unauthenticated", "token"},
		"a viewer writing":   {jsonRequest(viewer, http.MethodPost, "/api/v1/apps/draft-prod/scale", map[string]any{"replicas": 2}), http.StatusForbidden, "forbidden", "viewer"},
		"an unknown action":  {jsonRequest(token, http.MethodPost, "/api/v1/apps/draft-prod/explode", map[string]any{}), http.StatusNotFound, "not_found", ""},
		"an unmapped verb":   {jsonRequest(token, http.MethodPut, "/api/v1/nodes/worker-1", map[string]any{}), http.StatusBadRequest, "invalid_request", "no PUT"},
		"a body that is not": {jsonRequest(token, http.MethodPost, "/api/v1/apps/draft-prod/scale", nil), http.StatusBadRequest, "invalid_request", ""},
		"an unknown read":    {jsonRequest(token, http.MethodGet, "/api/v1/nonsense", nil), http.StatusNotFound, "not_found", ""},
	} {
		t.Run(name, func(t *testing.T) {
			if name == "a body that is not" {
				tt.req = httptest.NewRequest(http.MethodPost, "/api/v1/apps/draft-prod/scale", strings.NewReader("{broken"))
				tt.req.Header.Set("Content-Type", "application/json")
				tt.req.Header.Set("Authorization", "Bearer "+token)
			}
			rec := f.do(tt.req)

			require.Equal(t, tt.status, rec.Code, rec.Body.String())
			message, code := decodeError(t, rec)
			require.Equal(t, tt.wantCode, code)
			require.NotEmpty(t, message)
			require.Contains(t, strings.ToLower(message), strings.ToLower(tt.wantContains))
		})
	}
}

func TestAProjectTokenKeepsItsLimitsOnV1(t *testing.T) {
	f := newTokenFixture(t)
	token := f.projectToken(t, "production")

	allowed := f.do(jsonRequest(token, http.MethodPost, "/api/v1/apps/draft-prod/scale", map[string]any{"replicas": 2}))
	require.Equal(t, http.StatusOK, allowed.Code, allowed.Body.String())

	otherEnvironment := f.do(jsonRequest(token, http.MethodPost, "/api/v1/apps/draft-staging/scale", map[string]any{"replicas": 2}))
	require.Equal(t, http.StatusNotFound, otherEnvironment.Code, "the same answer as a service that does not exist")

	create := f.do(jsonRequest(token, http.MethodPost, "/api/v1/apps", map[string]any{"project": testProjectName, "name": "sneaky"}))
	require.Equal(t, http.StatusForbidden, create.Code, "a deploy token cannot create resources, however it asks")

	crossProject := f.do(jsonRequest(token, http.MethodPost, "/api/v1/apps/draft-prod/update", map[string]any{"environment": "staging"}))
	require.Equal(t, http.StatusForbidden, crossProject.Code, "the environment check reads JSON fields too")
}

func TestBodyConversionFollowsTheFormConventions(t *testing.T) {
	form, err := v1BodyToForm("application/json", []byte(`{
		"name": "api", "port": 8080, "ratio": 0.5, "metrics": true, "waitForCI": false, "missing": null,
		"environments": ["production", "staging"], "configJSON": {"A": "1"}
	}`))

	require.NoError(t, err)
	require.Equal(t, url.Values{
		"name": {"api"}, "port": {"8080"}, "ratio": {"0.5"}, "metrics": {"on"},
		"environments": {"production", "staging"}, "configJSON": {`{"A":"1"}`},
	}, form, "true is a checked box, false and null are absent, a list repeats, and an object travels as its text")

	_, err = v1BodyToForm("application/json", []byte(`{"x": [[1]]}`))
	require.Error(t, err)
	_, err = v1BodyToForm("application/json", []byte(`[1,2]`))
	require.Error(t, err, "only an object is a request")

	form, err = v1BodyToForm("application/x-www-form-urlencoded", []byte("a=1&b=2"))
	require.NoError(t, err)
	require.Equal(t, url.Values{"a": {"1"}, "b": {"2"}}, form)
}

func TestV1PathMapping(t *testing.T) {
	for name, tt := range map[string]struct {
		method, path, want string
		extra              map[string]string
		ok                 bool
	}{
		"create an app":       {"POST", "apps", "/api/apps/create", nil, true},
		"create a database":   {"POST", "databases", "/api/databases/create", nil, true},
		"create a project":    {"POST", "projects", "/api/projects/create", nil, true},
		"delete an app":       {"DELETE", "apps/web", "/api/apps/web/delete", nil, true},
		"delete a project":    {"DELETE", "projects/shop", "/api/projects/shop/settings/delete", nil, true},
		"update a database":   {"PATCH", "databases/db", "/api/databases/db/update", nil, true},
		"save a project":      {"PUT", "projects/shop", "/api/projects/shop/settings/save", nil, true},
		"an action":           {"POST", "apps/web/deploy", "/api/apps/web/deploy", nil, true},
		"a nested action":     {"POST", "apps/web/config/set", "/api/apps/web/config/set", nil, true},
		"project compliance":  {"POST", "projects/shop/compliance", "/api/projects/shop/compliance", nil, true},
		"create a token":      {"POST", "tokens", "/api/tokens/create", nil, true},
		"revoke a token":      {"DELETE", "tokens/abc12345", "/api/tokens/revoke", map[string]string{"id": "abc12345"}, true},
		"unknown verb":        {"PUT", "nodes/n1", "", nil, false},
		"a collection delete": {"DELETE", "apps", "", nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			got, extra, ok := v1LegacyPath(tt.method, strings.Split(tt.path, "/"))
			require.Equal(t, tt.ok, ok)
			require.Equal(t, tt.want, got)
			require.Equal(t, tt.extra, extra)
		})
	}
}
