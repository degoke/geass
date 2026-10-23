package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/githubapp"
	"github.com/degoke/geass/pkg/platform"
)

const (
	testForwardingSecret = "fwd_0123456789abcdef0123456789abcdef"
	testInstallToken     = "ghs_providerminted0123456789abcdefABCD"
)

// externalFixture is a dashboard on a cluster with organizations off, driven through its real routes with an install-wide
// token, as rungeass calls a dedicated cluster. Everything it logs is kept, to show what never is.
type externalFixture struct {
	tokenFixture
	token  string
	logged *strings.Builder
}

func newExternalFixture(t *testing.T) externalFixture {
	t.Helper()
	f := newTokenFixture(t)
	logged := &strings.Builder{}
	return externalFixture{tokenFixture: f, token: f.workspaceToken(t, "admin"), logged: logged}
}

func (f externalFixture) call(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	logger := funcr.New(func(prefix, args string) { f.logged.WriteString(prefix + args + "\n") }, funcr.Options{Verbosity: 10})
	req := jsonRequest(f.token, method, path, body)
	rec := httptest.NewRecorder()
	logDashboardRequests(f.handler).ServeHTTP(rec, req.WithContext(ctrllog.IntoContext(req.Context(), logger)))
	return rec
}

func (f externalFixture) makeConnection(t *testing.T) {
	t.Helper()
	rec := f.call(t, http.MethodPut, "/api/v1/github/connections/rungeass", map[string]any{
		"provider": "rungeass", "appSlug": "rungeass", "installURL": "https://app.rungeass.test/github/install", "webhookSecret": testForwardingSecret,
	})
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
}

func (f externalFixture) bind(t *testing.T, id int64, account, org string) *httptest.ResponseRecorder {
	t.Helper()
	return f.call(t, http.MethodPut, "/api/v1/github/connections/rungeass/installations/"+itoa(id), map[string]any{"account": account, "accountType": "Organization", "org": org})
}

func (f externalFixture) giveToken(t *testing.T, id int64, token string, expires time.Time) *httptest.ResponseRecorder {
	t.Helper()
	return f.call(t, http.MethodPut, "/api/v1/github/connections/rungeass/installations/"+itoa(id)+"/token", map[string]any{"token": token, "expiresAt": expires.UTC().Format(time.RFC3339)})
}

func itoa(id int64) string { return strconv.FormatInt(id, 10) }

func decodeView(t *testing.T, rec *httptest.ResponseRecorder) externalGitHubView {
	t.Helper()
	var view externalGitHubView
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &view), rec.Body.String())
	return view
}

func storedSecret(t *testing.T, c client.Client, name string) *corev1.Secret {
	t.Helper()
	secret := &corev1.Secret{}
	if err := c.Get(asSystem(context.Background()), client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, secret); err != nil {
		return nil
	}
	return secret
}

func TestTheProviderMakesAnExternalConnectionWhoseSecretIsNeverShown(t *testing.T) {
	f := newExternalFixture(t)

	created := f.call(t, http.MethodPut, "/api/v1/github/connections/rungeass", map[string]any{
		"provider": "rungeass", "appSlug": "rungeass", "installURL": "https://app.rungeass.test/github/install", "webhookSecret": testForwardingSecret,
	})

	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	view := decodeView(t, created)
	require.Equal(t, "rungeass", view.Provider)
	require.Equal(t, "/webhooks/github/forwarded/rungeass", view.WebhookPath)
	require.True(t, view.Ready, "the forwarding secret is there")
	require.Equal(t, "True", view.TokensReady)
	require.Equal(t, "NoInstallations", view.TokensReason)
	connection := &geassv1alpha1.GeassGitHubConnection{}
	require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: "rungeass", Namespace: platform.SystemNamespace}, connection))
	require.NotNil(t, connection.Spec.External)
	require.Equal(t, githubapp.ExternalWebhookSecretName("rungeass"), connection.Spec.WebhookSecretRef.Name)
	stored := storedSecret(t, f.srv.Client, githubapp.ExternalWebhookSecretName("rungeass"))
	require.Equal(t, testForwardingSecret, string(stored.Data[githubapp.SecretKeyWebhookSecret]))
	require.Equal(t, "rungeass", stored.Labels[githubapp.LabelGitHubConnection])

	// Changing it without the secret keeps the secret; a field sent empty is cleared.
	changed := f.call(t, http.MethodPut, "/api/v1/github/connections/rungeass", map[string]any{"appSlug": "rungeass-two", "installURL": ""})
	require.Equal(t, http.StatusOK, changed.Code, changed.Body.String())
	require.Equal(t, "rungeass-two", decodeView(t, changed).AppSlug)
	require.Empty(t, decodeView(t, changed).InstallURL)
	require.Equal(t, testForwardingSecret, string(storedSecret(t, f.srv.Client, githubapp.ExternalWebhookSecretName("rungeass")).Data[githubapp.SecretKeyWebhookSecret]))

	// Sent again, it is changed.
	rotated := f.call(t, http.MethodPut, "/api/v1/github/connections/rungeass", map[string]any{"webhookSecret": strings.Repeat("r", 40)})
	require.Equal(t, http.StatusOK, rotated.Code, rotated.Body.String())
	require.Equal(t, strings.Repeat("r", 40), string(storedSecret(t, f.srv.Client, githubapp.ExternalWebhookSecretName("rungeass")).Data[githubapp.SecretKeyWebhookSecret]))

	for _, rec := range []*httptest.ResponseRecorder{created, changed, rotated,
		f.call(t, http.MethodGet, "/api/v1/github/connections", nil), f.call(t, http.MethodGet, "/api/v1/github/connections/rungeass", nil),
		f.asAdmin(t, httptest.NewRequest(http.MethodGet, "/api/settings/github", nil)), f.asAdmin(t, httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)),
	} {
		require.NotContains(t, rec.Body.String(), testForwardingSecret)
		require.NotContains(t, rec.Body.String(), strings.Repeat("r", 40))
	}
	require.NotContains(t, f.logged.String(), testForwardingSecret, "nor is it logged")
	require.NotContains(t, f.logged.String(), strings.Repeat("r", 40))
}

func TestAnExternalConnectionIsRefusedWhatItCannotBe(t *testing.T) {
	f := newExternalFixture(t)
	require.NoError(t, f.srv.Client.Create(context.Background(), &geassv1alpha1.GeassGitHubConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "shared", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassGitHubConnectionSpec{SecretRef: corev1.LocalObjectReference{Name: "shared-token"}},
	}))
	good := map[string]any{"provider": "rungeass", "webhookSecret": testForwardingSecret}
	for name, tc := range map[string]struct {
		path   string
		body   map[string]any
		status int
		says   string
	}{
		"no provider":                 {"rungeass", map[string]any{"webhookSecret": testForwardingSecret}, http.StatusBadRequest, "provider is required"},
		"no webhook secret":           {"rungeass", map[string]any{"provider": "rungeass"}, http.StatusBadRequest, "webhookSecret is required"},
		"a short webhook secret":      {"rungeass", map[string]any{"provider": "rungeass", "webhookSecret": "short"}, http.StatusBadRequest, "between 32 and 512"},
		"a name of a project's":       {"payments-github", good, http.StatusBadRequest, "does not end in -github"},
		"a name that is not a label":  {"Run_Geass", good, http.StatusBadRequest, "lowercase letters"},
		"a connection that is not":    {"shared", good, http.StatusConflict, "not one of an external App"},
		"an install address not TLS":  {"rungeass", map[string]any{"provider": "rungeass", "webhookSecret": testForwardingSecret, "installURL": "http://app.test"}, http.StatusBadRequest, "https"},
		"a slug that is not a slug":   {"rungeass", map[string]any{"provider": "rungeass", "webhookSecret": testForwardingSecret, "appSlug": "Not A Slug"}, http.StatusBadRequest, "appSlug"},
		"a provider with a line feed": {"rungeass", map[string]any{"provider": "a\nb", "webhookSecret": testForwardingSecret}, http.StatusBadRequest, "provider"},
	} {
		rec := f.call(t, http.MethodPut, "/api/v1/github/connections/"+tc.path, tc.body)
		require.Equal(t, tc.status, rec.Code, "%s: %s", name, rec.Body.String())
		message, _ := decodeError(t, rec)
		require.Contains(t, message, tc.says, name)
		require.NotContains(t, rec.Body.String(), testForwardingSecret, name)
	}
}

func TestInstallationsAreRecordedListedAndRemoved(t *testing.T) {
	f := newExternalFixture(t)
	f.makeConnection(t)

	bound := f.bind(t, 42, "acme", "")
	require.Equal(t, http.StatusOK, bound.Code, bound.Body.String())
	var installation externalInstallationView
	require.NoError(t, json.Unmarshal(bound.Body.Bytes(), &installation))
	require.Equal(t, externalInstallationView{ID: 42, Account: "acme", AccountType: "Organization", TokenState: githubapp.TokenMissing}, installation)
	require.Equal(t, http.StatusOK, f.bind(t, 7, "beta", "").Code)
	require.Equal(t, http.StatusOK, f.bind(t, 42, "acme-renamed", "").Code, "recording it again changes it")

	listed := f.call(t, http.MethodGet, "/api/v1/github/connections/rungeass/installations", nil)
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	var items struct {
		Items []externalInstallationView `json:"items"`
	}
	require.NoError(t, json.Unmarshal(listed.Body.Bytes(), &items))
	require.Len(t, items.Items, 2)
	require.Equal(t, int64(7), items.Items[0].ID, "in the order of their ids")
	require.Equal(t, "acme-renamed", items.Items[1].Account)
	view := decodeView(t, f.call(t, http.MethodGet, "/api/v1/github/connections/rungeass", nil))
	require.Equal(t, "False", view.TokensReady)
	require.Equal(t, "TokenMissing", view.TokensReason)

	removed := f.call(t, http.MethodDelete, "/api/v1/github/connections/rungeass/installations/7", nil)
	require.Equal(t, http.StatusOK, removed.Code, removed.Body.String())
	require.Len(t, decodeView(t, f.call(t, http.MethodGet, "/api/v1/github/connections/rungeass", nil)).Installations, 1)
	require.Equal(t, http.StatusNotFound, f.call(t, http.MethodDelete, "/api/v1/github/connections/rungeass/installations/7", nil).Code)
}

func TestAnInstallationIsRefusedWhatWouldShareIt(t *testing.T) {
	f := newExternalFixture(t)
	f.makeConnection(t)
	require.Equal(t, http.StatusCreated, f.call(t, http.MethodPut, "/api/v1/github/connections/other", map[string]any{"provider": "other", "webhookSecret": testForwardingSecret}).Code)
	require.Equal(t, http.StatusOK, f.bind(t, 42, "acme", "").Code)

	twice := f.call(t, http.MethodPut, "/api/v1/github/connections/other/installations/42", map[string]any{"account": "acme"})
	require.Equal(t, http.StatusConflict, twice.Code, "an installation is one connection's: %s", twice.Body.String())
	noOrg := f.bind(t, 43, "beta", "org-nobody")
	require.Equal(t, http.StatusBadRequest, noOrg.Code, noOrg.Body.String())
	for name, body := range map[string]map[string]any{
		"no account":         {},
		"an account of junk": {"account": "a b"},
		"an unknown type":    {"account": "acme", "accountType": "Robot"},
	} {
		require.Equal(t, http.StatusBadRequest, f.call(t, http.MethodPut, "/api/v1/github/connections/rungeass/installations/44", body).Code, name)
	}
	require.Equal(t, http.StatusBadRequest, f.call(t, http.MethodPut, "/api/v1/github/connections/rungeass/installations/abc", map[string]any{"account": "acme"}).Code)
	require.Equal(t, http.StatusNotFound, f.call(t, http.MethodPut, "/api/v1/github/connections/nobody/installations/42", map[string]any{"account": "acme"}).Code)
}

func TestTheProvidersTokenIsKeptInASecretAndOnlyItsStateIsSaid(t *testing.T) {
	f := newExternalFixture(t)
	f.makeConnection(t)
	require.Equal(t, http.StatusOK, f.bind(t, 42, "acme", "").Code)
	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Second)

	given := f.giveToken(t, 42, testInstallToken, expires)

	require.Equal(t, http.StatusOK, given.Code, given.Body.String())
	var answer struct {
		TokenState     string    `json:"tokenState"`
		TokenExpiresAt time.Time `json:"tokenExpiresAt"`
	}
	require.NoError(t, json.Unmarshal(given.Body.Bytes(), &answer))
	require.Equal(t, githubapp.TokenValid, answer.TokenState)
	require.True(t, expires.Equal(answer.TokenExpiresAt))
	stored := storedSecret(t, f.srv.Client, githubapp.ExternalTokenSecretName("rungeass", 42))
	require.Equal(t, testInstallToken, string(stored.Data["token"]))
	require.Equal(t, "42", stored.Labels[githubapp.LabelGitHubInstallation])
	view := decodeView(t, f.call(t, http.MethodGet, "/api/v1/github/connections/rungeass", nil))
	require.Equal(t, "True", view.TokensReady)
	require.Equal(t, githubapp.TokenValid, view.Installations[0].TokenState)
	require.True(t, expires.Equal(*view.Installations[0].TokenExpiresAt))

	soon := f.giveToken(t, 42, testInstallToken+"x", time.Now().Add(5*time.Minute))
	require.Equal(t, http.StatusOK, soon.Code, soon.Body.String())
	require.Contains(t, soon.Body.String(), githubapp.TokenExpiringSoon, "a token due for renewal is taken and said to be")
	require.Equal(t, "TokenExpiringSoon", decodeView(t, f.call(t, http.MethodGet, "/api/v1/github/connections/rungeass", nil)).TokensReason)

	for name, tc := range map[string]struct {
		id      int64
		body    map[string]any
		status  int
		message string
	}{
		"no token":                     {42, map[string]any{"expiresAt": expires.Format(time.RFC3339)}, http.StatusBadRequest, "token is required"},
		"a token with a header in it":  {42, map[string]any{"token": "ghs_abc\r\nX-Evil: 1", "expiresAt": expires.Format(time.RFC3339)}, http.StatusBadRequest, "characters"},
		"no expiry":                    {42, map[string]any{"token": testInstallToken}, http.StatusBadRequest, "expiresAt is required"},
		"an expiry that is not a time": {42, map[string]any{"token": testInstallToken, "expiresAt": "in an hour"}, http.StatusBadRequest, "RFC 3339"},
		"an expiry that has passed":    {42, map[string]any{"token": testInstallToken, "expiresAt": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)}, http.StatusBadRequest, "has passed"},
		"an installation not bound":    {99, map[string]any{"token": testInstallToken, "expiresAt": expires.Format(time.RFC3339)}, http.StatusNotFound, "add it first"},
	} {
		rec := f.call(t, http.MethodPut, "/api/v1/github/connections/rungeass/installations/"+itoa(tc.id)+"/token", tc.body)
		require.Equal(t, tc.status, rec.Code, "%s: %s", name, rec.Body.String())
		message, _ := decodeError(t, rec)
		require.Contains(t, message, tc.message, name)
		require.NotContains(t, rec.Body.String(), testInstallToken, name)
		require.NotContains(t, rec.Body.String(), "X-Evil", name)
	}
	require.Equal(t, testInstallToken+"x", string(storedSecret(t, f.srv.Client, githubapp.ExternalTokenSecretName("rungeass", 42)).Data["token"]),
		"a refused token changes nothing")

	// The token is in no answer and no line of the log.
	for _, rec := range []*httptest.ResponseRecorder{
		f.call(t, http.MethodGet, "/api/v1/github/connections", nil), f.call(t, http.MethodGet, "/api/v1/github/connections/rungeass/installations", nil),
		f.asAdmin(t, httptest.NewRequest(http.MethodGet, "/api/settings/github", nil)), f.asAdmin(t, httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil)),
		f.call(t, http.MethodGet, "/api/v1/openapi.json", nil),
	} {
		require.NotContains(t, rec.Body.String(), testInstallToken)
	}
	require.Contains(t, f.logged.String(), "Stored installation token", "the store is logged, so there is something to check")
	require.NotContains(t, f.logged.String(), testInstallToken)
}

func TestAnExternalConnectionIsRemovedOnlyOnceNoProjectUsesIt(t *testing.T) {
	f := newExternalFixture(t)
	f.makeConnection(t)
	require.Equal(t, http.StatusOK, f.bind(t, 42, "acme", "").Code)
	require.Equal(t, http.StatusOK, f.giveToken(t, 42, testInstallToken, time.Now().Add(time.Hour)).Code)
	connected := f.call(t, http.MethodPost, "/api/v1/projects/payments/github/install", map[string]any{})
	require.Equal(t, http.StatusOK, connected.Code, connected.Body.String())

	require.Equal(t, http.StatusBadRequest, f.call(t, http.MethodDelete, "/api/v1/github/connections/rungeass", nil).Code, "the name is confirmed")
	inUse := f.call(t, http.MethodDelete, "/api/v1/github/connections/rungeass?confirmName=rungeass", nil)
	require.Equal(t, http.StatusConflict, inUse.Code, inUse.Body.String())
	require.Contains(t, inUse.Body.String(), "payments")

	require.Equal(t, http.StatusOK, f.call(t, http.MethodPost, "/api/v1/projects/payments/github/disconnect", map[string]any{}).Code)
	deleted := f.call(t, http.MethodDelete, "/api/v1/github/connections/rungeass?confirmName=rungeass", nil)
	require.Equal(t, http.StatusOK, deleted.Code, deleted.Body.String())
	require.Nil(t, storedSecret(t, f.srv.Client, githubapp.ExternalTokenSecretName("rungeass", 42)), "its tokens go with it")
	require.Nil(t, storedSecret(t, f.srv.Client, githubapp.ExternalWebhookSecretName("rungeass")))
	require.Equal(t, http.StatusNotFound, f.call(t, http.MethodGet, "/api/v1/github/connections/rungeass", nil).Code)
}

func TestAProjectConnectsToTheOnlyInstallationItsOrganizationHas(t *testing.T) {
	f := newExternalFixture(t)
	f.makeConnection(t)

	none := f.call(t, http.MethodPost, "/api/v1/projects/payments/github/install", map[string]any{"environment": "production"})
	require.Equal(t, http.StatusOK, none.Code, none.Body.String())
	var sent struct {
		URL string `json:"url"`
	}
	require.NoError(t, json.Unmarshal(none.Body.Bytes(), &sent))
	target, err := url.Parse(sent.URL)
	require.NoError(t, err)
	require.Equal(t, "app.rungeass.test", target.Host, "with no installation, the person is sent to install the App at the provider's")
	require.Equal(t, "payments", target.Query().Get("project"))
	require.Equal(t, "production", target.Query().Get("environment"))

	require.Equal(t, http.StatusOK, f.bind(t, 42, "acme", "").Code)
	require.Equal(t, http.StatusOK, f.giveToken(t, 42, testInstallToken, time.Now().Add(time.Hour)).Code)
	connected := f.call(t, http.MethodPost, "/api/v1/projects/payments/github/install", map[string]any{})
	require.Equal(t, http.StatusOK, connected.Code, connected.Body.String())
	require.Contains(t, connected.Body.String(), `"connected":true`)

	project := projectOf(t, f.srv.Client)
	require.Equal(t, "payments-github", project.Spec.GitHubConnectionRef.Name)
	connection := &geassv1alpha1.GeassGitHubConnection{}
	require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: "payments-github", Namespace: platform.SystemNamespace}, connection))
	require.Equal(t, githubapp.ExternalWebhookSecretName("rungeass"), connection.Spec.WebhookSecretRef.Name)
	secret := storedSecret(t, f.srv.Client, "payments-github-token")
	require.Equal(t, "42", string(secret.Data["installation_id"]))
	require.Equal(t, "rungeass", string(secret.Data[githubapp.SecretKeyExternalConnection]))
	require.Equal(t, "acme", string(secret.Data["account"]))
	require.NotContains(t, connected.Body.String(), testInstallToken)

	// The platform's view says which projects use the installation.
	view := decodeView(t, f.call(t, http.MethodGet, "/api/v1/github/connections/rungeass", nil))
	require.Equal(t, []string{"payments"}, view.Installations[0].Projects)
	// A Git service can now be made with the project's connection, as with the cluster's own App.
	require.NoError(t, f.srv.validateGitConnectionForProject(asSystem(context.Background()), "payments", "payments-github"))
}

func TestAProjectWithSeveralInstallationsIsConnectedToTheOneItNames(t *testing.T) {
	f := newExternalFixture(t)
	f.makeConnection(t)
	require.Equal(t, http.StatusOK, f.bind(t, 42, "acme", "").Code)
	require.Equal(t, http.StatusOK, f.bind(t, 43, "beta", "").Code)

	ambiguous := f.call(t, http.MethodPost, "/api/v1/projects/payments/github/install", map[string]any{})
	require.Equal(t, http.StatusBadRequest, ambiguous.Code)
	message, _ := decodeError(t, ambiguous)
	require.Contains(t, message, "acme (42)")
	require.Contains(t, message, "beta (43)")

	listed := f.call(t, http.MethodGet, "/api/v1/projects/payments/github/installations", nil)
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	require.Contains(t, listed.Body.String(), `"account":"beta"`)

	named := f.call(t, http.MethodPost, "/api/v1/projects/payments/github/install", map[string]any{"installation": 43})
	require.Equal(t, http.StatusOK, named.Code, named.Body.String())
	require.Equal(t, "43", string(storedSecret(t, f.srv.Client, "payments-github-token").Data["installation_id"]))
	require.Equal(t, http.StatusBadRequest, f.call(t, http.MethodPost, "/api/v1/projects/payments/github/install", map[string]any{"installation": 99}).Code)
}

func TestAProjectsRepositoriesAreListedWithTheProvidersToken(t *testing.T) {
	var auth string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/installation/repositories":
			auth = r.Header.Get("Authorization")
			_ = json.NewEncoder(w).Encode(map[string]any{"repositories": []map[string]any{{"full_name": "acme/api", "default_branch": "main"}}})
		default:
			t.Errorf("Geass asked GitHub for %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	f := newExternalFixture(t)
	f.srv.HTTPClient = githubTestHTTPClient(api.URL)
	f.makeConnection(t)
	require.Equal(t, http.StatusOK, f.bind(t, 42, "acme", "").Code)
	require.Equal(t, http.StatusOK, f.call(t, http.MethodPost, "/api/v1/projects/payments/github/install", map[string]any{}).Code)

	waiting := f.asAdmin(t, httptest.NewRequest(http.MethodGet, "/api/projects/payments/github/repos", nil))
	require.Equal(t, http.StatusServiceUnavailable, waiting.Code, "with no token yet the list says why: %s", waiting.Body.String())
	require.Contains(t, waiting.Body.String(), "no token for GitHub installation 42 (acme) yet")

	require.Equal(t, http.StatusOK, f.giveToken(t, 42, testInstallToken, time.Now().Add(time.Hour)).Code)
	listed := f.asAdmin(t, httptest.NewRequest(http.MethodGet, "/api/projects/payments/github/repos", nil))
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	require.Contains(t, listed.Body.String(), "acme/api")
	require.Equal(t, "Bearer "+testInstallToken, auth, "the installation's repositories, asked with the provider's token")
	require.NotContains(t, listed.Body.String(), testInstallToken)
}

func TestInExternalModeTheClustersOwnAppIsNotSetUp(t *testing.T) {
	f := newExternalFixture(t)
	f.makeConnection(t)

	settings := f.asAdmin(t, httptest.NewRequest(http.MethodGet, "/api/settings/github", nil))
	require.Equal(t, http.StatusOK, settings.Code)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(settings.Body.Bytes(), &payload))
	require.Equal(t, true, payload["hasGitHubApp"])
	require.NotContains(t, payload, "manifest", "no App is registered from here")
	require.NotContains(t, payload, "manifestAction")
	external, ok := payload["external"].(map[string]any)
	require.True(t, ok, settings.Body.String())
	require.Equal(t, "rungeass", external["provider"])

	saved := f.asAdmin(t, postForm("/api/settings/github/save", url.Values{"appID": {"1"}, "clientID": {"c"}, "slug": {"s"}, "privateKey": {"k"}}))
	require.Equal(t, http.StatusBadRequest, saved.Code)
	require.Contains(t, saved.Body.String(), "provided to this cluster by rungeass")
	tested := f.asAdmin(t, postForm("/api/settings/github/test", url.Values{}))
	require.Contains(t, tested.Body.String(), "provided to this cluster by rungeass")
	callback := httptest.NewRequest(http.MethodGet, "/settings/github/manifest/callback?code=abc&state="+strings.Repeat("a", 32), nil)
	callback.Header.Set("Accept", "application/json")
	manifest := f.asAdmin(t, callback)
	require.Contains(t, manifest.Body.String(), "provided to this cluster by rungeass", "the manifest flow is not run")
	var secrets corev1.SecretList
	require.NoError(t, f.srv.Client.List(asSystem(context.Background()), &secrets))
	for _, secret := range secrets.Items {
		require.NotEqual(t, platformGitHubAppSecretName, secret.Name, "no App credentials are stored in the cluster")
		require.NotContains(t, secret.Data, githubapp.SecretKeyPrivateKey)
	}

	bootstrap := f.asAdmin(t, httptest.NewRequest(http.MethodGet, "/api/bootstrap", nil))
	require.Contains(t, bootstrap.Body.String(), `"hasGitHubApp":true`)
	require.Contains(t, bootstrap.Body.String(), `"githubProvider":"rungeass"`)
}

// Organizations on: the platform's routes are the platform's, and an organization connects only its own projects, and
// only to installations listed for it.
func TestAnOrganizationCannotReachTheExternalAppOrAnotherOrganizationsInstallation(t *testing.T) {
	f := newMatrixFixture(t)
	ctx := context.Background()
	webhook := githubapp.ExternalWebhookSecretName("rungeass")
	require.NoError(t, f.base.Create(ctx, &geassv1alpha1.GeassGitHubConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "rungeass", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassGitHubConnectionSpec{SecretRef: corev1.LocalObjectReference{Name: webhook}, WebhookSecretRef: &corev1.LocalObjectReference{Name: webhook},
			External: &geassv1alpha1.GeassGitHubExternalApp{Provider: "rungeass", Installations: []geassv1alpha1.GeassGitHubInstallation{
				{ID: 42, Account: "acme", Org: orgA}, {ID: 43, Account: "beta", Org: orgB},
			}}},
	}))
	require.NoError(t, f.base.Create(ctx, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: webhook, Namespace: platform.SystemNamespace},
		Data: map[string][]byte{githubapp.SecretKeyWebhookSecret: []byte(testForwardingSecret)}}))
	aAdmin, aViewer, bAdmin := sessionIn(orgA, "", dashboardRoleAdmin), sessionIn(orgA, "", dashboardRoleViewer), sessionIn(orgB, "", dashboardRoleAdmin)

	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/github/connections"}, {http.MethodGet, "/api/v1/github/connections/rungeass"},
		{http.MethodGet, "/api/v1/github/connections/rungeass/installations"},
		{http.MethodPost, "/api/github/connections/rungeass/save"}, {http.MethodPost, "/api/github/connections/rungeass/delete"},
		{http.MethodPost, "/api/github/connections/rungeass/installations/42/save"}, {http.MethodPost, "/api/github/connections/rungeass/installations/42/delete"},
		{http.MethodPost, "/api/github/connections/rungeass/installations/42/token"},
	} {
		before := f.snapshot(t)
		rec := f.do(aAdmin, route.method, route.path, form("token", testInstallToken, "expiresAt", time.Now().Add(time.Hour).UTC().Format(time.RFC3339), "account", "acme", "confirmName", "rungeass"))
		require.Equal(t, http.StatusForbidden, rec.Code, "%s %s: %s", route.method, route.path, rec.Body.String())
		require.Equal(t, before, f.snapshot(t))
		require.Equal(t, http.StatusForbidden, f.do(aViewer, route.method, route.path, nil).Code, "%s %s", route.method, route.path)
	}
	require.Nil(t, storedSecret(t, f.base, githubapp.ExternalTokenSecretName("rungeass", 42)), "no token was stored by an organization")

	// Another organization's installation is not one to connect to, and is answered as one that does not exist.
	theirs := f.do(aAdmin, http.MethodPost, "/api/projects/pa1/github/install", form("installation", "43"))
	require.Equal(t, http.StatusBadRequest, theirs.Code, theirs.Body.String())
	missing := f.do(aAdmin, http.MethodPost, "/api/projects/pa1/github/install", form("installation", "777"))
	require.Equal(t, theirs.Body.String(), missing.Body.String())
	// Nor is another organization's project.
	require.NotEqual(t, http.StatusOK, f.do(aAdmin, http.MethodPost, "/api/projects/pb1/github/install", form("installation", "42")).Code)
	require.Equal(t, http.StatusNotFound, f.do(aAdmin, http.MethodGet, "/api/projects/pb1/github/installations", nil).Code)
	// A viewer connects nothing.
	require.Equal(t, http.StatusForbidden, f.do(aViewer, http.MethodPost, "/api/projects/pa1/github/install", nil).Code)
	require.Equal(t, http.StatusForbidden, f.do(aViewer, http.MethodGet, "/api/projects/pa1/github/installations", nil).Code)

	// What an organization is shown of the App is its own installations, and nothing of the other's.
	listed := f.do(aAdmin, http.MethodGet, "/api/projects/pa1/github/installations", nil)
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	require.Contains(t, listed.Body.String(), `"account":"acme"`)
	require.NotContains(t, listed.Body.String(), "beta")

	// Its own project, to its own installation, it connects.
	connected := f.do(aAdmin, http.MethodPost, "/api/projects/pa1/github/install", nil)
	require.Equal(t, http.StatusOK, connected.Code, connected.Body.String())
	require.Equal(t, "42", string(storedSecret(t, f.base, "pa1-github-token").Data["installation_id"]))
	require.Equal(t, http.StatusOK, f.do(bAdmin, http.MethodPost, "/api/projects/pb1/github/install", nil).Code)
	require.Equal(t, "43", string(storedSecret(t, f.base, "pb1-github-token").Data["installation_id"]))

	// The platform moves nothing from one organization to another while a project of the first is connected.
	moved := f.do(platformSession, http.MethodPost, "/api/github/connections/rungeass/installations/42/save", form("account", "acme", "org", orgB))
	require.Equal(t, http.StatusConflict, moved.Code, moved.Body.String())
	// And the platform's own routes are its own.
	require.Equal(t, http.StatusOK, f.do(platformSession, http.MethodPost, "/api/github/connections/rungeass/installations/42/token",
		form("token", testInstallToken, "expiresAt", time.Now().Add(time.Hour).UTC().Format(time.RFC3339))).Code)
	require.Equal(t, http.StatusOK, f.do(platformSession, http.MethodGet, "/api/v1/github/connections/rungeass", nil).Code)
}

func TestATokenOrASecretInTheAddressIsRefused(t *testing.T) {
	f := newExternalFixture(t)
	f.makeConnection(t)
	require.Equal(t, http.StatusOK, f.bind(t, 42, "acme", "").Code)
	expires := url.QueryEscape(time.Now().Add(time.Hour).UTC().Format(time.RFC3339))

	inQuery := f.call(t, http.MethodPut, "/api/v1/github/connections/rungeass/installations/42/token?token="+testInstallToken+"&expiresAt="+expires, map[string]any{})
	require.Equal(t, http.StatusBadRequest, inQuery.Code, inQuery.Body.String())
	require.Contains(t, inQuery.Body.String(), "never in the address")
	secretInQuery := f.call(t, http.MethodPut, "/api/v1/github/connections/rungeass?webhookSecret="+testForwardingSecret, map[string]any{})
	require.Equal(t, http.StatusBadRequest, secretInQuery.Code)
	legacy := f.asAdmin(t, postForm("/api/github/connections/rungeass/installations/42/token?token="+testInstallToken, url.Values{"expiresAt": {expires}}))
	require.Equal(t, http.StatusBadRequest, legacy.Code, legacy.Body.String())
	require.Nil(t, storedSecret(t, f.srv.Client, githubapp.ExternalTokenSecretName("rungeass", 42)))
}
