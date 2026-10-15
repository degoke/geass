package dashboard

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/githubapp"
	"github.com/degoke/geass/pkg/platform"
)

// recordedRequest is one request that reached the fake GitHub.
type recordedRequest struct {
	Method string
	Path   string
	Body   map[string]any
}

// githubRecorder is a GitHub that records what it is asked and answers from a table; what is not in it is a 404.
type githubRecorder struct {
	server *httptest.Server

	mu       sync.Mutex
	requests []recordedRequest
	answers  map[string]func(body map[string]any) (int, any)
}

func newGitHubRecorder(t *testing.T) *githubRecorder {
	t.Helper()
	g := &githubRecorder{answers: map[string]func(map[string]any) (int, any){}}
	g.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &body)
		}
		g.mu.Lock()
		g.requests = append(g.requests, recordedRequest{Method: r.Method, Path: r.URL.Path, Body: body})
		answer, ok := g.answers[r.Method+" "+r.URL.Path]
		g.mu.Unlock()
		code, payload := http.StatusNotFound, any(map[string]string{"message": "Not Found"})
		if ok {
			code, payload = answer(body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(g.server.Close)
	return g
}

func (g *githubRecorder) answer(method, path string, code int, payload any) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.answers[method+" "+path] = func(map[string]any) (int, any) { return code, payload }
}

func (g *githubRecorder) all() []recordedRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]recordedRequest(nil), g.requests...)
}

func (g *githubRecorder) to(method, path string) []recordedRequest {
	var out []recordedRequest
	for _, request := range g.all() {
		if request.Method == method && request.Path == path {
			out = append(out, request)
		}
	}
	return out
}

func (g *githubRecorder) client() *http.Client {
	return &http.Client{Transport: roundTripRewrite{target: g.server.URL}}
}

const (
	reportedDeploymentID = 4801
	existingCommentID    = 900
)

// connectedPRFixture is prFixture with the project connected to GitHub, and a GitHub that has the permissions: it
// takes deployment statuses, and has a Geass comment on pull request 7.
func connectedPRFixture(t *testing.T, mutate ...func(*geassv1alpha1.GeassProject)) (*Server, client.Client, *githubRecorder) {
	t.Helper()
	connection := &geassv1alpha1.GeassGitHubConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-github", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassGitHubConnectionSpec{SecretRef: corev1.LocalObjectReference{Name: "payments-github-token"}},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-github-token", Namespace: platform.SystemNamespace},
		Data:       map[string][]byte{"token": []byte("tok")},
	}
	srv, c := prFixture(t, enabledPR(), connection, secret)
	require.NoError(t, srv.mutateProject(context.Background(), testProjectName, func(p *geassv1alpha1.GeassProject) error {
		p.Spec.GitHubConnectionRef = &corev1.LocalObjectReference{Name: "payments-github"}
		for _, m := range mutate {
			m(p)
		}
		return nil
	}))
	gh := newGitHubRecorder(t)
	srv.HTTPClient = gh.client()
	srv.GitHubReports = githubapp.NewGuard()
	gh.answer(http.MethodGet, "/repos/acme/web/issues/7/comments", http.StatusOK, []map[string]any{
		{"id": 5, "body": "looks good to me", "user": map[string]string{"login": "reviewer", "type": "User"}},
		{"id": existingCommentID, "body": githubapp.PullRequestMarker(testProjectName) + "\n### Geass preview environment `pr-7`\n\nold", "user": map[string]string{"login": "geass[bot]", "type": "Bot"}},
	})
	gh.answer(http.MethodPatch, "/repos/acme/web/issues/comments/"+strconv.Itoa(existingCommentID), http.StatusOK, map[string]any{"id": existingCommentID})
	gh.answer(http.MethodPost, "/repos/acme/web/deployments/"+strconv.Itoa(reportedDeploymentID)+"/statuses", http.StatusCreated, map[string]any{"id": 1})
	return srv, c, gh
}

// openPR7WithADeployment opens pull request 7 and gives its copy of web the record the operator leaves when it has
// reported a deploy.
func openPR7WithADeployment(t *testing.T, srv *Server, c client.Client) {
	t.Helper()
	ctx := context.Background()
	_, err := srv.handlePullRequestEvent(ctx, prPayload("opened", 7, "feature-x"))
	require.NoError(t, err)
	app, ok := getApp(t, c, "little-frost-pr7")
	require.True(t, ok)
	app.Annotations[platform.AppGitHubDeploymentAnnotation] = "abc123:" + strconv.Itoa(reportedDeploymentID) + ":success:2026-10-14T12:00:00.5Z"
	require.NoError(t, c.Update(ctx, &app))
}

func TestClosingAPullRequestEndsItsDeploymentsAndSaysSoOnItsComment(t *testing.T) {
	srv, c, gh := connectedPRFixture(t)
	openPR7WithADeployment(t, srv, c)

	_, err := srv.handlePullRequestEvent(context.Background(), prPayload("closed", 7, "feature-x"))
	require.NoError(t, err)
	srv.reports.Wait()

	require.Equal(t, []string{"production"}, projectOf(t, c).Spec.Environments, "the environment is removed first, as it always was")
	statuses := gh.to(http.MethodPost, "/repos/acme/web/deployments/"+strconv.Itoa(reportedDeploymentID)+"/statuses")
	require.Len(t, statuses, 1)
	require.Equal(t, "inactive", statuses[0].Body["state"], "the deployments of an environment that is gone are inactive")
	patched := gh.to(http.MethodPatch, "/repos/acme/web/issues/comments/"+strconv.Itoa(existingCommentID))
	require.Len(t, patched, 1, "the comment that is there is edited in place")
	body, _ := patched[0].Body["body"].(string)
	require.Contains(t, body, githubapp.PullRequestMarker(testProjectName)+"\n", "the marker stays, so the comment is found again if the pull request is opened again")
	require.Contains(t, body, "was removed")
	require.Empty(t, gh.to(http.MethodPost, "/repos/acme/web/issues/7/comments"), "no second comment")
}

func TestAPullRequestThatNeverHadACommentDoesNotGetOneWhenItsEnvironmentIsRemoved(t *testing.T) {
	srv, c, gh := connectedPRFixture(t)
	gh.answer(http.MethodGet, "/repos/acme/web/issues/7/comments", http.StatusOK, []map[string]any{})
	openPR7WithADeployment(t, srv, c)

	_, err := srv.handlePullRequestEvent(context.Background(), prPayload("closed", 7, "feature-x"))
	require.NoError(t, err)
	srv.reports.Wait()

	require.Empty(t, gh.to(http.MethodPost, "/repos/acme/web/issues/7/comments"))
	require.Empty(t, gh.to(http.MethodPatch, "/repos/acme/web/issues/comments/"+strconv.Itoa(existingCommentID)))
}

func TestAProjectThatTurnedReportingOffTellsGitHubNothingWhenAPullRequestCloses(t *testing.T) {
	off := false
	srv, c, gh := connectedPRFixture(t, func(p *geassv1alpha1.GeassProject) { p.Spec.GitHubReporting = &off })
	openPR7WithADeployment(t, srv, c)

	_, err := srv.handlePullRequestEvent(context.Background(), prPayload("closed", 7, "feature-x"))
	require.NoError(t, err)
	srv.reports.Wait()

	require.Equal(t, []string{"production"}, projectOf(t, c).Spec.Environments)
	require.Empty(t, gh.all(), "not a request")
}

func TestAMissingPermissionNeverStopsAPullRequestEnvironmentFromBeingRemoved(t *testing.T) {
	srv, c, gh := connectedPRFixture(t)
	gh.answer(http.MethodGet, "/repos/acme/web/issues/7/comments", http.StatusForbidden, map[string]string{"message": "Resource not accessible by integration"})
	gh.answer(http.MethodPost, "/repos/acme/web/deployments/"+strconv.Itoa(reportedDeploymentID)+"/statuses", http.StatusForbidden, map[string]string{"message": "Resource not accessible by integration"})
	openPR7WithADeployment(t, srv, c)

	results, err := srv.handlePullRequestEvent(context.Background(), prPayload("closed", 7, "feature-x"))
	require.NoError(t, err)
	srv.reports.Wait()

	require.Len(t, results, 1)
	require.Equal(t, 3, results[0].Removed)
	require.Equal(t, []string{"production"}, projectOf(t, c).Spec.Environments)
	var connection geassv1alpha1.GeassGitHubConnection
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "payments-github", Namespace: platform.SystemNamespace}, &connection))
	var reporting *metav1.Condition
	for i := range connection.Status.Conditions {
		if connection.Status.Conditions[i].Type == githubapp.ConditionReporting {
			reporting = &connection.Status.Conditions[i]
		}
	}
	require.NotNil(t, reporting, "the owner can see on the connection that GitHub refused")
	require.Equal(t, metav1.ConditionFalse, reporting.Status)
}

func TestAnEnvironmentClosedByHandIsReportedToo(t *testing.T) {
	srv, c, gh := connectedPRFixture(t)
	openPR7WithADeployment(t, srv, c)

	rec := postPR(t, srv, "close", url.Values{"number": {"7"}, "repository": {prRepo}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	srv.reports.Wait()

	require.Len(t, gh.to(http.MethodPatch, "/repos/acme/web/issues/comments/"+strconv.Itoa(existingCommentID)), 1)
}

func TestTheOperatorsMemoryOfAPullRequestCommentIsClearedWhenItsEnvironmentIsRemoved(t *testing.T) {
	srv, c, _ := connectedPRFixture(t)
	openPR7WithADeployment(t, srv, c)
	srv.GitHubReports.RememberComment("acme/web#7|"+testProjectName, "what the operator last wrote")

	_, err := srv.handlePullRequestEvent(context.Background(), prPayload("closed", 7, "feature-x"))
	require.NoError(t, err)
	srv.reports.Wait()

	require.False(t, srv.GitHubReports.CommentIs("acme/web#7|"+testProjectName, "what the operator last wrote"),
		"a pull request that is opened again must get its comment written again, not skipped for saying what it said before")
}

func TestOnlyTheServicesOfThePullRequestsEnvironmentAreEnded(t *testing.T) {
	srv, c, _ := connectedPRFixture(t)
	openPR7WithADeployment(t, srv, c)
	_, err := srv.handlePullRequestEvent(context.Background(), prPayload("opened", 8, "feature-y"))
	require.NoError(t, err)
	other, ok := getApp(t, c, "little-frost-pr8")
	require.True(t, ok)
	other.Annotations[platform.AppGitHubDeploymentAnnotation] = "def456:4802:success:rev"
	require.NoError(t, c.Update(context.Background(), &other))

	found := srv.pullRequestDeployments(context.Background(), testProjectName, "pr-7", 7)

	require.Len(t, found, 1)
	require.EqualValues(t, reportedDeploymentID, found[0].id)
	require.Equal(t, prRepo, found[0].repository)
}

// ---- the setting ----

func postReporting(t *testing.T, srv *Server, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.handleProjectRoutes(rec, withAdminSession(t, srv, postForm("/projects/"+testProjectName+"/github-reporting/save", form)))
	return rec
}

func TestTheReportingSwitchIsSavedOnTheProject(t *testing.T) {
	srv, c := prFixture(t, nil)
	require.Nil(t, projectOf(t, c).Spec.GitHubReporting, "unset: on")
	require.True(t, platform.GitHubReportingEnabled(&geassv1alpha1.GeassProject{}))

	rec := postReporting(t, srv, url.Values{})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	saved := projectOf(t, c).Spec.GitHubReporting
	require.NotNil(t, saved)
	require.False(t, *saved, "leaving it off the form turns it off, as the other switches on the settings page do")
	require.False(t, platform.GitHubReportingEnabled(&geassv1alpha1.GeassProject{Spec: geassv1alpha1.GeassProjectSpec{GitHubReporting: saved}}))

	rec = postReporting(t, srv, url.Values{"enabled": {"on"}})
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"ok":true,"enabled":true}`, rec.Body.String())
	require.True(t, *projectOf(t, c).Spec.GitHubReporting)
}

func TestTheReportingSwitchForAProjectThatDoesNotExistIsNotFound(t *testing.T) {
	srv, _ := prFixture(t, nil)
	rec := httptest.NewRecorder()
	srv.handleProjectRoutes(rec, withAdminSession(t, srv, postForm("/projects/ghost/github-reporting/save", url.Values{"enabled": {"on"}})))
	require.Equal(t, http.StatusNotFound, rec.Code)
}

func TestSavingProjectSettingsKeepsTheReportingSwitch(t *testing.T) {
	srv, c := prFixture(t, nil)
	require.Equal(t, http.StatusOK, postReporting(t, srv, url.Values{}).Code)

	rec := httptest.NewRecorder()
	srv.handleProjectRoutes(rec, withAdminSession(t, srv, postForm("/projects/"+testProjectName+"/settings/save", url.Values{
		"displayName": {"payments"}, "environments": {"production"},
	})))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	saved := projectOf(t, c).Spec.GitHubReporting
	require.NotNil(t, saved, "saving other settings does not turn reporting back on")
	require.False(t, *saved)
}

func TestViewersDoNotSeeTheReportingSwitchBecauseItIsAGitHubSetting(t *testing.T) {
	off := false
	data := &dashboardBootstrap{}
	data.Projects.Items = []geassv1alpha1.GeassProject{{
		Spec: geassv1alpha1.GeassProjectSpec{
			GitHubConnectionRef: &corev1.LocalObjectReference{Name: "payments-github"},
			GitHubReporting:     &off,
			PREnvironments:      &geassv1alpha1.GeassPREnvironments{Enabled: true},
		},
	}}

	sanitizeDashboardForViewer(data)

	require.Nil(t, data.Projects.Items[0].Spec.GitHubConnectionRef)
	require.Nil(t, data.Projects.Items[0].Spec.GitHubReporting, "the GitHub settings are not for viewers")
	require.NotNil(t, data.Projects.Items[0].Spec.PREnvironments, "nothing else is hidden by this")
}

func TestTheReportingSwitchIsNotOpenToProjectTokens(t *testing.T) {
	_, ok := projectTokenRoute(http.MethodPost, "/api/projects/"+testProjectName+"/github-reporting/save")
	require.False(t, ok, "a token that may deploy a service may not change what Geass reports to GitHub")
}

// ---- the Test GitHub App check ----

func gitHubAppTestServer(t *testing.T, gh *githubRecorder) *Server {
	t.Helper()
	cfg := testGitHubAppConfig(t)
	srv := &Server{
		Client:     newFakeClient(testPlatformConfig("https://geass.test"), testPlatformGitHubSecret(t, cfg)),
		HTTPClient: gh.client(),
	}
	return srv
}

func runGitHubAppTest(t *testing.T, srv *Server) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.handlePlatformGitHubTest(rec, withAdminSession(t, srv, postForm("/settings/github/test", url.Values{})))
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	return rec.Code, body
}

func TestTheGitHubAppCheckPassesWhenTheAppHasEveryPermission(t *testing.T) {
	gh := newGitHubRecorder(t)
	gh.answer(http.MethodGet, "/app/hook/config", http.StatusOK, map[string]any{"url": "https://geass.test/webhooks/github", "content_type": "json", "active": true})
	gh.answer(http.MethodGet, "/app", http.StatusOK, map[string]any{"slug": "geass", "permissions": map[string]string{
		"checks": "read", "contents": "read", "metadata": "read", "pull_requests": "write", "statuses": "write", "deployments": "write",
	}})

	code, body := runGitHubAppTest(t, gitHubAppTestServer(t, gh))

	require.Equal(t, http.StatusOK, code)
	require.Equal(t, true, body["ok"])
	require.NotContains(t, body, "warning")
}

func TestTheGitHubAppCheckSaysWhichPermissionsAnOlderAppLacksAndHowToApproveThem(t *testing.T) {
	gh := newGitHubRecorder(t)
	gh.answer(http.MethodGet, "/app/hook/config", http.StatusOK, map[string]any{"url": "https://geass.test/webhooks/github", "content_type": "json", "active": true})
	// An app registered before Geass reported to GitHub.
	gh.answer(http.MethodGet, "/app", http.StatusOK, map[string]any{"slug": "geass-old", "permissions": map[string]string{
		"checks": "read", "contents": "read", "metadata": "read", "pull_requests": "read",
	}})

	code, body := runGitHubAppTest(t, gitHubAppTestServer(t, gh))

	require.Equal(t, http.StatusOK, code, "the webhook is right, so this is a warning and not a failure")
	warning, _ := body["warning"].(string)
	require.NotEmpty(t, warning)
	for _, want := range []string{
		"Commit statuses (a status on each commit Geass builds)",
		"Deployments (a GitHub Deployment for each deploy)",
		"Pull requests (pull request environments read pull requests, and Geass comments on them)",
		"https://github.com/settings/apps/geass-old/permissions",
		"Commit statuses: Read and write",
		"Accept new permissions",
		"builds and deploys are not affected",
	} {
		require.Contains(t, warning, want)
	}
	require.NotContains(t, warning, "Checks", "the permissions the app has are not listed as missing")
	require.NotContains(t, warning, "Contents")
}

func TestTheGitHubAppCheckStillFailsWhenTheWebhookIsWrong(t *testing.T) {
	gh := newGitHubRecorder(t)
	gh.answer(http.MethodGet, "/app/hook/config", http.StatusOK, map[string]any{"url": "https://elsewhere.example.com/webhooks/github", "content_type": "json", "active": true})
	gh.answer(http.MethodGet, "/app", http.StatusOK, map[string]any{"slug": "geass", "permissions": map[string]string{}})

	code, body := runGitHubAppTest(t, gitHubAppTestServer(t, gh))

	require.Equal(t, http.StatusBadRequest, code)
	require.Contains(t, body["error"], "webhook URL is https://elsewhere.example.com/webhooks/github")
	require.Empty(t, gh.to(http.MethodGet, "/app"), "the permissions are not looked at when the webhook already fails")
}

func TestTheGitHubAppCheckSaysSoWhenThePermissionsCannotBeRead(t *testing.T) {
	gh := newGitHubRecorder(t)
	gh.answer(http.MethodGet, "/app/hook/config", http.StatusOK, map[string]any{"url": "https://geass.test/webhooks/github", "content_type": "json", "active": true})
	gh.answer(http.MethodGet, "/app", http.StatusInternalServerError, map[string]string{"message": "boom"})

	code, body := runGitHubAppTest(t, gitHubAppTestServer(t, gh))

	require.Equal(t, http.StatusOK, code)
	require.Contains(t, body["warning"], "permissions could not be read")
}
