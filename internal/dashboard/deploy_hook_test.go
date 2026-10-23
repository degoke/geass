package dashboard

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr/funcr"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/apitokens"
	"github.com/degoke/geass/pkg/platform"
)

// hookFixture is the token fixture plus a service built from a repository, with a finished build, in production.
type hookFixture struct {
	tokenFixture
}

func newHookFixture(t *testing.T) hookFixture {
	t.Helper()
	f := newTokenFixture(t)
	app := gitApp(func(a *geassv1alpha1.GeassApp) {
		a.Name = "web-git"
		a.Spec.Environment = "production"
	})
	require.NoError(t, f.srv.Client.Create(context.Background(), app))
	require.NoError(t, f.srv.Client.Create(context.Background(), operatorBuild(app, "web-git-build", time.Hour, geassv1alpha1.GeassBuildSucceeded)))
	return hookFixture{tokenFixture: f}
}

// make creates a deploy hook the way the dashboard does and returns the secret shown once.
func (f hookFixture) make(t *testing.T, app string) string {
	t.Helper()
	rec := f.asAdmin(t, postForm("/api/tokens/create", url.Values{
		"name": {"hook for " + app}, "scope": {"service"}, "project": {testProjectName}, "environment": {"production"}, "app": {app},
	}))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var created struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &created))
	require.True(t, strings.HasPrefix(created.Token, "gsh_"), created.Token)
	return created.Token
}

// call is a request the way a registry or a scheduler makes it: a bare POST to the URL, from a public address.
func (f hookFixture) call(token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/hooks/deploy/"+token, nil)
	req.RemoteAddr = "203.0.113.20:5555"
	return f.do(req)
}

func (f hookFixture) app(t *testing.T, name string) geassv1alpha1.GeassApp {
	t.Helper()
	var app geassv1alpha1.GeassApp
	require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, &app))
	return app
}

func TestAServiceBuiltFromARepositoryIsBuiltAgainByItsHook(t *testing.T) {
	f := newHookFixture(t)
	token := f.make(t, "web-git")

	rec := f.call(token)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.JSONEq(t, `{"ok":true,"action":"build","service":"web-git"}`, rec.Body.String())
	build := buildOf(t, f.srv.Client, "web-git-build")
	require.Equal(t, geassv1alpha1.GeassBuildPending, build.Status.Phase, "the build starts over, from the head of the branch")
	require.Empty(t, build.Status.ImageDigest)
	require.Empty(t, build.Status.SourceRevision)
}

func TestAnImageServicesHookDeploysItsPendingChanges(t *testing.T) {
	f := newHookFixture(t)
	require.False(t, f.app(t, "draft-prod").Spec.Deploy.Enabled, "the service starts as a draft")
	token := f.make(t, "draft-prod")

	rec := f.call(token)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.JSONEq(t, `{"ok":true,"action":"deploy","service":"draft-prod"}`, rec.Body.String())
	deployed := f.app(t, "draft-prod")
	require.True(t, deployed.Spec.Deploy.Enabled)
	require.Empty(t, appPendingKinds(&deployed), "nothing is pending once it is deployed")
}

// The history of a service says what started each deploy. A hook's deploy said "Dashboard", twice, where nobody had been.
func TestADeployMadeByAHookIsRecordedAsTheHooksAndNotTheDashboards(t *testing.T) {
	f := newHookFixture(t)
	token := f.make(t, "draft-prod")

	require.Equal(t, http.StatusOK, f.call(token).Code)

	var history geassv1alpha1.GeassDeploymentList
	require.NoError(t, f.srv.Client.List(context.Background(), &history, client.InNamespace(platform.SystemNamespace), client.MatchingLabels{platform.LabelApp: "draft-prod"}))
	require.Len(t, history.Items, 1)
	require.Equal(t, "Deploy hook", history.Items[0].Spec.Source)
	require.Equal(t, "hook for draft-prod", history.Items[0].Spec.Actor, "the hook's name says which one")
}

func TestADeployWithNothingToSayOtherwiseIsTheDashboards(t *testing.T) {
	source, actor := deployOriginOf(context.Background())
	require.Equal(t, "Dashboard", source)
	require.Equal(t, "Dashboard", actor)

	ctx := context.WithValue(context.Background(), apiTokenContextKey{}, &apitokens.Record{Name: "ci"})
	source, actor = deployOriginOf(ctx)
	require.Equal(t, "API token", source, "a deploy made with a token says so")
	require.Equal(t, "ci", actor)
}

func TestAHookDeploysItsServiceAndNothingElse(t *testing.T) {
	f := newHookFixture(t)
	token := f.make(t, "api-prod")

	require.Equal(t, http.StatusOK, f.call(token).Code)

	for _, other := range []string{"draft-prod", "draft-staging"} {
		require.False(t, f.app(t, other).Spec.Deploy.Enabled, other+" is not what the hook is for")
	}
	require.Equal(t, geassv1alpha1.GeassBuildSucceeded, buildOf(t, f.srv.Client, "web-git-build").Status.Phase)
}

func TestAHookRecordsWhenItWasLastUsed(t *testing.T) {
	f := newHookFixture(t)
	token := f.make(t, "api-prod")

	require.Equal(t, http.StatusOK, f.call(token).Code)

	records, err := f.srv.listAPITokens(context.Background())
	require.NoError(t, err)
	var hook apitokens.Record
	for _, record := range records {
		if record.Scope == apitokens.ScopeService {
			hook = record
		}
	}
	require.NotNil(t, hook.LastUsedAt, "the token list can say whether a hook is still in use")
}

// A hook's secret is in a URL, where logs and proxies see it. It must not be a key to anything but the one deploy.
func TestAHookIsNoCredentialForTheAPI(t *testing.T) {
	f := newHookFixture(t)
	token := f.make(t, "api-prod")

	for _, call := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/me"},
		{http.MethodGet, "/api/token"},
		{http.MethodPost, "/api/token/deploy"},
		{http.MethodPost, "/api/apps/api-prod/deploy"},
		{http.MethodGet, "/api/bootstrap"},
	} {
		rec := f.do(bearer(token, call.method, call.path, nil))
		require.Equal(t, http.StatusUnauthorized, rec.Code, call.path)
	}
	require.False(t, f.app(t, "draft-prod").Spec.Deploy.Enabled)
}

func TestOnlyADeployHookWorksOnAHookURL(t *testing.T) {
	f := newHookFixture(t)
	workspace := f.workspaceToken(t, "admin")
	project := f.projectToken(t, "production")

	for name, token := range map[string]string{"a workspace admin token": workspace, "a project token": project} {
		rec := f.call(token)

		require.Equal(t, http.StatusNotFound, rec.Code, name)
		require.Contains(t, rec.Body.String(), deployHookGone, name)
	}
	require.False(t, f.app(t, "draft-prod").Spec.Deploy.Enabled)
}

func TestEveryWayAHookCanBeBadLooksTheSame(t *testing.T) {
	f := newHookFixture(t)
	good := f.make(t, "draft-prod")
	parts := strings.Split(good, "_")
	wrongSecret := strings.Join([]string{parts[0], parts[1], strings.Repeat("0", len(parts[2]))}, "_")
	unknownID := strings.Join([]string{parts[0], "ffffffff", parts[2]}, "_")

	expired := f.make(t, "api-prod")
	expiredID := strings.Split(expired, "_")[1]
	require.NoError(t, f.srv.mutateAPITokens(context.Background(), func(records map[string]apitokens.Record) (bool, error) {
		record := records[expiredID]
		past := time.Now().Add(-time.Hour)
		record.ExpiresAt = &past
		records[expiredID] = record
		return true, nil
	}))

	revoked := f.make(t, "web-git")
	require.Equal(t, http.StatusOK, f.asAdmin(t, postForm("/api/tokens/revoke", url.Values{"id": {strings.Split(revoked, "_")[1]}})).Code)

	var want string
	for name, token := range map[string]string{
		"garbage": "nonsense", "empty": "", "wrong secret": wrongSecret, "unknown id": unknownID, "expired": expired, "revoked": revoked,
	} {
		rec := f.call(token)

		require.Equal(t, http.StatusNotFound, rec.Code, name)
		if want == "" {
			want = rec.Body.String()
		}
		require.Equal(t, want, rec.Body.String(), name+": a caller cannot tell which of these it was")
	}
	require.False(t, f.app(t, "draft-prod").Spec.Deploy.Enabled, "and none of them deployed anything")
	require.Equal(t, geassv1alpha1.GeassBuildSucceeded, buildOf(t, f.srv.Client, "web-git-build").Status.Phase)
}

func TestAHookIsCalledWithPostOnly(t *testing.T) {
	f := newHookFixture(t)
	token := f.make(t, "draft-prod")

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, "/hooks/deploy/"+token, nil)
		req.RemoteAddr = "203.0.113.20:5555"
		rec := f.do(req)

		require.Equal(t, http.StatusMethodNotAllowed, rec.Code, method)
		require.Equal(t, http.MethodPost, rec.Header().Get("Allow"), method)
	}
	require.False(t, f.app(t, "draft-prod").Spec.Deploy.Enabled, "a link preview or a crawler fetching the URL deploys nothing")
}

func TestAClientThatKeepsGuessingHooksIsSlowedDown(t *testing.T) {
	f := newHookFixture(t)
	good := f.make(t, "draft-prod")
	for range loginSourceFailureLimit {
		require.Equal(t, http.StatusNotFound, f.call("gsh_00000000_"+strings.Repeat("a", 40)).Code)
	}

	rec := f.call(good)

	require.Equal(t, http.StatusTooManyRequests, rec.Code, "even the right URL waits, so guessing cannot go on at full speed")
	require.False(t, f.app(t, "draft-prod").Spec.Deploy.Enabled)
}

func TestAHookStopsWorkingWhenItsServiceIsGone(t *testing.T) {
	f := newHookFixture(t)
	token := f.make(t, "draft-prod")
	app := f.app(t, "draft-prod")
	require.NoError(t, f.srv.Client.Delete(context.Background(), &app))

	rec := f.call(token)

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Contains(t, rec.Body.String(), deployHookGone)
}

func TestDeletingAServiceRevokesItsHook(t *testing.T) {
	f := newHookFixture(t)
	token := f.make(t, "draft-prod")
	other := f.make(t, "api-prod")

	rec := f.asAdmin(t, postForm("/api/apps/draft-prod/delete", url.Values{"confirmName": {"draft-prod"}}))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	records, err := f.srv.listAPITokens(context.Background())
	require.NoError(t, err)
	apps := map[string]bool{}
	for _, record := range records {
		apps[record.App] = true
	}
	require.False(t, apps["draft-prod"], "the hook of a deleted service is gone, not left to match a service made later with the same name")
	require.True(t, apps["api-prod"], "and the hook of another service is not touched")
	require.Equal(t, http.StatusNotFound, f.call(token).Code)
	require.Equal(t, http.StatusOK, f.call(other).Code)
}

func TestDeletingAnEnvironmentRevokesItsHooks(t *testing.T) {
	f := newHookFixture(t)
	token := f.make(t, "api-prod")

	f.srv.deleteAPITokensFor(context.Background(), testProjectName, "production")

	require.Equal(t, http.StatusNotFound, f.call(token).Code)
}

func TestOnlyASignedInAdminCanMakeAHook(t *testing.T) {
	f := newHookFixture(t)
	form := url.Values{"name": {"hook"}, "scope": {"service"}, "project": {testProjectName}, "environment": {"production"}, "app": {"api-prod"}}

	admin := f.workspaceToken(t, "admin")
	require.Equal(t, http.StatusForbidden, f.do(bearer(admin, http.MethodPost, "/api/tokens/create", form)).Code, "not even an admin token")
	viewer := f.do(withDashboardSession(t, f.srv, postForm("/api/tokens/create", form), "viewer", dashboardRoleViewer))
	require.Equal(t, http.StatusForbidden, viewer.Code)

	records, err := f.srv.listAPITokens(context.Background())
	require.NoError(t, err)
	for _, record := range records {
		require.NotEqual(t, apitokens.ScopeService, record.Scope, "no hook was made")
	}
}

func TestAHookIsMadeForAServiceThatExistsInTheEnvironment(t *testing.T) {
	f := newHookFixture(t)
	make := func(extra url.Values) *httptest.ResponseRecorder {
		form := url.Values{"name": {"hook"}, "scope": {"service"}, "project": {testProjectName}, "environment": {"production"}, "app": {"api-prod"}}
		maps.Copy(form, extra)
		return f.asAdmin(t, postForm("/api/tokens/create", form))
	}

	require.Equal(t, http.StatusBadRequest, make(url.Values{"app": {"no-such-service"}}).Code, "no service by that name")
	require.Equal(t, http.StatusBadRequest, make(url.Values{"app": {"api-staging"}}).Code, "a service of another environment")
	require.Equal(t, http.StatusBadRequest, make(url.Values{"app": {"other-app"}}).Code, "a service of another project")
	require.Equal(t, http.StatusBadRequest, make(url.Values{"app": {""}}).Code, "a hook needs a service")
	require.Equal(t, http.StatusBadRequest, make(url.Values{"environment": {"nowhere"}}).Code)

	require.Equal(t, http.StatusOK, make(nil).Code)
	again := make(nil)
	require.Equal(t, http.StatusBadRequest, again.Code, "a service has one hook")
	require.Contains(t, again.Body.String(), "already has a deploy hook")
}

func TestAHookIsListedWithoutItsSecret(t *testing.T) {
	f := newHookFixture(t)
	token := f.make(t, "api-prod")

	rec := f.asAdmin(t, httptest.NewRequest(http.MethodGet, "/api/tokens", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	var list struct {
		Items []apiTokenView `json:"items"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &list))
	require.Len(t, list.Items, 1)
	require.Equal(t, apitokens.ScopeService, list.Items[0].Scope)
	require.Equal(t, "api-prod", list.Items[0].App)
	require.NotContains(t, rec.Body.String(), strings.Split(token, "_")[2], "the secret is shown once, when the hook is made")
}

func TestTheOtherKindsOfTokenCannotBeTiedToOneService(t *testing.T) {
	f := newHookFixture(t)

	for name, form := range map[string]url.Values{
		"workspace": {"name": {"x"}, "scope": {"workspace"}, "app": {"api-prod"}},
		"project":   {"name": {"x"}, "scope": {"project"}, "project": {testProjectName}, "environment": {"production"}, "app": {"api-prod"}},
	} {
		rec := f.asAdmin(t, postForm("/api/tokens/create", form))

		require.Equal(t, http.StatusBadRequest, rec.Code, name+": "+rec.Body.String())
	}
}

func TestAHookCannotDeployAServiceTheClusterHasNoRoomFor(t *testing.T) {
	f := newHookFixture(t)
	token := f.make(t, "draft-prod")
	require.NoError(t, f.srv.Client.Delete(context.Background(), roomyTestNode()), "the cluster has no node left")

	rec := f.call(token)

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	require.False(t, f.app(t, "draft-prod").Spec.Deploy.Enabled, "a hook is held to the same capacity check as the Deploy button")
}

// The hook's secret is its path, and the operator logs the path of every request. Whoever can read that log must
// not be handed a deploy credential for every service.
func TestTheOperatorsRequestLogNeverCarriesAHooksSecret(t *testing.T) {
	f := newHookFixture(t)
	token := f.make(t, "draft-prod")
	var logged strings.Builder
	logger := funcr.New(func(prefix, args string) { logged.WriteString(prefix + args + "\n") }, funcr.Options{Verbosity: 10})
	handler := logDashboardRequests(f.handler)

	for _, secret := range []string{token, "gsh_00000000_" + strings.Repeat("a", 40)} {
		req := httptest.NewRequest(http.MethodPost, "/hooks/deploy/"+secret, nil)
		req.RemoteAddr = "203.0.113.20:5555"
		handler.ServeHTTP(httptest.NewRecorder(), req.WithContext(ctrllog.IntoContext(req.Context(), logger)))
	}

	require.Contains(t, logged.String(), "/hooks/deploy/<redacted>", "the request is still logged, so there is something to check")
	require.NotContains(t, logged.String(), strings.Split(token, "_")[2], "not the secret of a real hook")
	require.NotContains(t, logged.String(), strings.Repeat("a", 40), "and not the secret anyone typed")
	require.Contains(t, loggedPath("/api/apps"), "/api/apps", "other paths are logged as they are")
}

// ageHook makes the last call of a hook old enough that it may be called again.
func (f hookFixture) ageHook(token string) {
	f.srv.hookMu.Lock()
	defer f.srv.hookMu.Unlock()
	if f.srv.hookLast == nil {
		f.srv.hookLast = map[string]time.Time{} // a call that was refused never claimed the cooldown
	}
	f.srv.hookLast[strings.Split(token, "_")[1]] = time.Now().Add(-2 * deployHookCooldown)
}

// A URL that leaked into a loop must not be able to keep a service's builds from ever finishing.
func TestAHookCalledAgainTooSoonDoesNothingTheSecondTime(t *testing.T) {
	f := newHookFixture(t)
	token := f.make(t, "web-git")
	require.Equal(t, http.StatusOK, f.call(token).Code)
	build := buildOf(t, f.srv.Client, "web-git-build")
	build.Status.Phase = geassv1alpha1.GeassBuildRunning
	require.NoError(t, f.srv.Client.Status().Update(context.Background(), &build), "the build the first call started is under way")

	rec := f.call(token)

	require.Equal(t, http.StatusTooManyRequests, rec.Code, rec.Body.String())
	require.NotEmpty(t, rec.Header().Get("Retry-After"), "the caller is told when to come back")
	require.Equal(t, geassv1alpha1.GeassBuildRunning, buildOf(t, f.srv.Client, "web-git-build").Status.Phase, "and the build that is running was not started over")

	f.ageHook(token)
	require.Equal(t, http.StatusOK, f.call(token).Code, "once the cooldown is over it works again")
	require.Equal(t, geassv1alpha1.GeassBuildPending, buildOf(t, f.srv.Client, "web-git-build").Status.Phase)
}

func TestTheCooldownBelongsToTheHookNotTheClient(t *testing.T) {
	f := newHookFixture(t)
	first := f.make(t, "web-git")
	second := f.make(t, "draft-prod")

	require.Equal(t, http.StatusOK, f.call(first).Code)

	require.Equal(t, http.StatusOK, f.call(second).Code, "another hook is not made to wait for this one")
}

func TestAHookThatCouldNotRunDoesNotMakeTheCallerWait(t *testing.T) {
	f := newHookFixture(t)
	token := f.make(t, "draft-prod")
	require.NoError(t, f.srv.Client.Delete(context.Background(), roomyTestNode()))
	require.Equal(t, http.StatusBadRequest, f.call(token).Code, "no room")
	require.NoError(t, f.srv.Client.Create(context.Background(), roomyTestNode()))

	rec := f.call(token)

	require.Equal(t, http.StatusOK, rec.Code, "the failed call used up nothing: "+rec.Body.String())
}

func TestAHookDoesNotBuildWhatAPushWouldNotBuild(t *testing.T) {
	f := newHookFixture(t)
	for name, mutate := range map[string]func(*geassv1alpha1.GeassApp){
		"never-deployed": func(a *geassv1alpha1.GeassApp) { a.Spec.Deploy.Enabled = false },
		"pinned":         func(a *geassv1alpha1.GeassApp) { a.Spec.Source.Git.Commit = "0123456789abcdef" },
	} {
		app := gitApp(mutate, func(a *geassv1alpha1.GeassApp) {
			a.Name = name
			a.Spec.Environment = "production"
		})
		require.NoError(t, f.srv.Client.Create(context.Background(), app))
		require.NoError(t, f.srv.Client.Create(context.Background(), operatorBuild(app, name+"-build", time.Hour, geassv1alpha1.GeassBuildSucceeded)))
		token := f.make(t, name)

		rec := f.call(token)

		require.Equal(t, http.StatusConflict, rec.Code, name+": "+rec.Body.String())
		require.Equal(t, geassv1alpha1.GeassBuildSucceeded, buildOf(t, f.srv.Client, name+"-build").Status.Phase, name+" was left as it was")
		f.ageHook(token)
		require.Equal(t, http.StatusConflict, f.call(token).Code, name+": a refusal used up no cooldown, so asking again is answered the same way")
	}
}

// tokenUsed is dropped when a token goes; the record of when a hook last ran must go with it, or every hook that was ever
// called stays in the operator's memory.
func TestRevokingOrDeletingAHookForgetsWhenItLastRan(t *testing.T) {
	remembered := func(f hookFixture, token string) bool {
		f.srv.hookMu.Lock()
		defer f.srv.hookMu.Unlock()
		_, ok := f.srv.hookLast[strings.Split(token, "_")[1]]
		return ok
	}
	f := newHookFixture(t)
	revoked := f.make(t, "api-prod")
	deleted := f.make(t, "draft-prod")
	byProject := f.make(t, "web-git")
	require.Equal(t, http.StatusOK, f.call(revoked).Code)
	require.Equal(t, http.StatusOK, f.call(deleted).Code)
	require.Equal(t, http.StatusOK, f.call(byProject).Code)
	require.True(t, remembered(f, revoked) && remembered(f, deleted) && remembered(f, byProject))

	require.Equal(t, http.StatusOK, f.asAdmin(t, postForm("/api/tokens/revoke", url.Values{"id": {strings.Split(revoked, "_")[1]}})).Code)
	f.srv.deleteAPITokensForApp(context.Background(), "draft-prod")
	f.srv.deleteAPITokensFor(context.Background(), testProjectName, "production")

	for name, token := range map[string]string{"revoked": revoked, "its service deleted": deleted, "its environment deleted": byProject} {
		require.False(t, remembered(f, token), name+": nothing is kept for a hook that is gone")
	}
}
