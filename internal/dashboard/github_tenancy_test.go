package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/githubapp"
	"github.com/degoke/geass/pkg/platform"
)

// withOrganizationsOn turns organizations on in the platform configuration a push fixture holds, and scopes its client.
func withOrganizationsOn(t *testing.T, srv *Server, c client.Client) {
	t.Helper()
	ctx := asSystem(context.Background())
	var config geassv1alpha1.GeassPlatformConfig
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, &config))
	config.Spec.Auth = &geassv1alpha1.GeassPlatformAuthSpec{Mode: platform.AuthModeExternal, Tenancy: &geassv1alpha1.GeassPlatformTenancySpec{Enabled: true}}
	require.NoError(t, c.Update(ctx, &config))
	srv.Client = ScopeClient(c)
}

// pushAsSystem is push as it arrives through the dashboard's routes: a webhook is a public path, which the dashboard
// serves as its own work.
func pushAsSystem(t *testing.T, srv *Server, secret string, payload any) string {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.handleGitHubWebhook(rec, signedGitHubWebhook(t, secret, payload).WithContext(asSystem(context.Background())))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	return rec.Body.String()
}

func TestWithOrganizationsOnAPushMustNameTheInstallationOfTheProject(t *testing.T) {
	for name, tc := range map[string]struct {
		installation any
		want         string
	}{
		"the installation of the project": {map[string]any{"id": 42}, `"triggered":1`},
		"another installation":            {map[string]any{"id": 99}, `"triggered":0`},
		"no installation at all":          {nil, `"triggered":0`},
		"an installation id of zero":      {map[string]any{"id": 0}, `"triggered":0`},
	} {
		t.Run(name, func(t *testing.T) {
			app := gitApp()
			srv, c, secret := pushServer(t, app, operatorBuild(app, "api-build", time.Hour, geassv1alpha1.GeassBuildSucceeded))
			withOrganizationsOn(t, srv, c)
			payload := pushPayload()
			if tc.installation == nil {
				delete(payload, "installation")
			} else {
				payload["installation"] = tc.installation
			}

			require.Contains(t, pushAsSystem(t, srv, secret, payload), tc.want)
		})
	}
}

func TestWithOrganizationsOffAPushThatNamesNoInstallationStillBuilds(t *testing.T) {
	app := gitApp()
	srv, _, secret := pushServer(t, app, operatorBuild(app, "api-build", time.Hour, geassv1alpha1.GeassBuildSucceeded))
	payload := pushPayload()
	delete(payload, "installation")

	require.Contains(t, push(t, srv, secret, payload), `"triggered":1`, "as it did before organizations existed")
}

func TestAnInstallationAnotherOrganizationUsesIsNotForThisOneToTake(t *testing.T) {
	connectionOf := func(project string, installation string) []client.Object {
		return []client.Object{
			&geassv1alpha1.GeassGitHubConnection{
				ObjectMeta: metav1.ObjectMeta{Name: project + "-github", Namespace: platform.SystemNamespace},
				Spec:       geassv1alpha1.GeassGitHubConnectionSpec{SecretRef: corev1.LocalObjectReference{Name: project + "-github-token"}},
			},
			&corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: project + "-github-token", Namespace: platform.SystemNamespace},
				Data:       map[string][]byte{"installation_id": []byte(installation), "token": []byte("t")},
			},
		}
	}
	connected := func(p *geassv1alpha1.GeassProject) *geassv1alpha1.GeassProject {
		p.Spec.GitHubConnectionRef = &corev1.LocalObjectReference{Name: p.Name + "-github"}
		return p
	}
	objects := []client.Object{connected(tenantProject("pa1", orgA, wsA1)), connected(tenantProject("pb1", orgB, "")), tenantProject("pb2", orgB, ""), tenantProject("pu", "", "")}
	objects = slices.Concat(objects, connectionOf("pa1", "111"), connectionOf("pb1", "222"))
	srv := &Server{Client: newFakeClient(objects...)}
	ctx := context.Background()
	project := func(name string) *geassv1alpha1.GeassProject {
		var p geassv1alpha1.GeassProject
		require.NoError(t, srv.Client.Get(ctx, key(name), &p))
		return &p
	}

	held, err := srv.installationHeldByAnotherOrg(ctx, 111, project("pb2"))
	require.NoError(t, err)
	require.True(t, held, "organization B cannot take the installation organization A's project is connected to")
	held, err = srv.installationHeldByAnotherOrg(ctx, 111, project("pa1"))
	require.NoError(t, err)
	require.False(t, held, "a project's own installation is its own")
	held, err = srv.installationHeldByAnotherOrg(ctx, 222, project("pb2"))
	require.NoError(t, err)
	require.False(t, held, "and an organization's projects may share theirs")
	held, err = srv.installationHeldByAnotherOrg(ctx, 333, project("pb2"))
	require.NoError(t, err)
	require.False(t, held, "an installation nobody uses is free")
	held, err = srv.installationHeldByAnotherOrg(ctx, 111, project("pu"))
	require.NoError(t, err)
	require.True(t, held, "a project that belongs to no organization is not the organization's to share it with either")
}

// installationCallback is a person of an organization coming back from GitHub with an installation id and, when the App
// asked for their authorization, a code. githubSays is what GitHub answers: the installations that person can use, and
// whether the code is good.
type githubSays struct {
	listed       []int64
	badCode      bool
	tokenCalls   int
	userAPICalls int
}

func (g *githubSays) server(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login/oauth/access_token":
			g.tokenCalls++
			if g.badCode || r.FormValue("code") != "good-code" || r.FormValue("client_secret") != "test-client-secret" {
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "bad_verification_code"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "ghu_person"})
		case "/user/installations":
			g.userAPICalls++
			if r.Header.Get("Authorization") != "Bearer ghu_person" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			installations := []map[string]any{}
			for _, id := range g.listed {
				installations = append(installations, map[string]any{"id": id})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"total_count": len(installations), "installations": installations})
		case "/app/installations/99":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 99, "account": map[string]string{"login": "acme", "type": "Organization"}})
		case "/app/installations/99/access_tokens":
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "installation-token"})
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestWithOrganizationsOnTheInstallerMustShowGitHubListsTheInstallationAsTheirs(t *testing.T) {
	cfg := testGitHubAppConfig(t)
	for name, tc := range map[string]struct {
		query   string
		github  githubSays
		tenant  Tenant
		allowed bool
	}{
		"an installation GitHub lists for them":                  {"&code=good-code", githubSays{listed: []int64{7, 99}}, Tenant{Org: orgA}, true},
		"an installation GitHub does not list for them":          {"&code=good-code", githubSays{listed: []int64{7}}, Tenant{Org: orgA}, false},
		"an address with no authorization code":                  {"", githubSays{listed: []int64{99}}, Tenant{Org: orgA}, false},
		"a code GitHub does not accept":                          {"&code=stolen", githubSays{listed: []int64{99}}, Tenant{Org: orgA}, false},
		"a code that is good but a token GitHub no longer takes": {"&code=good-code", githubSays{badCode: true, listed: []int64{99}}, Tenant{Org: orgA}, false},
		"the platform's administrator needs none":                {"", githubSays{}, Tenant{Platform: true}, true},
	} {
		t.Run(name, func(t *testing.T) {
			says := tc.github
			api := says.server(t)
			defer api.Close()
			base := newFakeClient(tenantProject("pa1", orgA, ""), testPlatformConfig("https://geass.test"), testPlatformGitHubSecret(t, cfg))
			srv := &Server{HTTPClient: githubTestHTTPClient(api.URL), Client: base}
			withOrganizationsOn(t, srv, base)
			gh := &githubapp.Client{Config: cfg, HTTP: srv.HTTPClient}
			state, err := gh.SignState("pa1", "production")
			require.NoError(t, err)

			rec := httptest.NewRecorder()
			req := withOrigin(httptest.NewRequest(http.MethodGet, "/github/callback?installation_id=99&state="+url.QueryEscape(state)+tc.query, nil))
			req = req.WithContext(context.WithValue(withTenant(req.Context(), tc.tenant), sessionContextKey{}, &dashboardSessionInfo{Username: "person", Role: dashboardRoleAdmin, CanMutate: true, Org: tc.tenant.Org}))
			srv.handleGitHubCallback(rec, req)
			require.Equal(t, http.StatusSeeOther, rec.Code)

			var connection geassv1alpha1.GeassGitHubConnection
			err = base.Get(asSystem(context.Background()), key("pa1-github"), &connection)
			if tc.allowed {
				require.NoError(t, err, "the installation is connected")
				return
			}
			require.True(t, apierrors.IsNotFound(err), "nothing was connected")
			var token corev1.Secret
			require.True(t, apierrors.IsNotFound(base.Get(asSystem(context.Background()), key("pa1-github-token"), &token)), "and no installation token was made for an installation that was not theirs")
			require.Contains(t, rec.Header().Get("Location"), "error=", "they are told why")
		})
	}
}

func TestWithOrganizationsOffTheInstallationNeedsNoProofAsBefore(t *testing.T) {
	cfg := testGitHubAppConfig(t)
	says := githubSays{}
	api := says.server(t)
	defer api.Close()
	base := newFakeClient(tenantProject("pa1", "", ""), testPlatformConfig("https://geass.test"), testPlatformGitHubSecret(t, cfg))
	srv := &Server{HTTPClient: githubTestHTTPClient(api.URL), Client: base}
	gh := &githubapp.Client{Config: cfg, HTTP: srv.HTTPClient}
	state, err := gh.SignState("pa1", "production")
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	req := withOrigin(httptest.NewRequest(http.MethodGet, "/github/callback?installation_id=99&state="+url.QueryEscape(state), nil))
	req = req.WithContext(context.WithValue(req.Context(), sessionContextKey{}, &dashboardSessionInfo{Username: "root", Role: dashboardRoleAdmin, CanMutate: true}))
	srv.handleGitHubCallback(rec, req)
	require.Equal(t, http.StatusSeeOther, rec.Code)
	require.NoError(t, base.Get(context.Background(), key("pa1-github"), &geassv1alpha1.GeassGitHubConnection{}))
	require.Zero(t, says.tokenCalls+says.userAPICalls, "GitHub was not asked who they are")
}
