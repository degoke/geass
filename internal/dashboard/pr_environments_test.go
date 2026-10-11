package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const prRepo = "acme/web"

func prPayload(action string, number int32, headRef string) githubPullRequestPayload {
	payload := githubPullRequestPayload{Action: action, Number: number}
	payload.Repository.FullName = prRepo
	payload.PullRequest.HTMLURL = "https://github.com/acme/web/pull/7"
	payload.PullRequest.Head.Ref = headRef
	payload.PullRequest.Head.SHA = "abc123"
	payload.PullRequest.Head.Repo.FullName = prRepo
	payload.PullRequest.Base.Ref = "main"
	payload.PullRequest.Base.Repo.FullName = prRepo
	return payload
}

// prFixture is a project whose production environment builds acme/web.
func prFixture(t *testing.T, config *geassv1alpha1.GeassPREnvironments, extra ...client.Object) (*Server, client.Client) {
	t.Helper()
	three := int32(3)
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassProjectSpec{
			DisplayName: "Payments", Environments: []string{"production"}, PREnvironments: config,
			SharedVariables: []geassv1alpha1.GeassSharedVariable{
				{Name: "LOG_LEVEL", Environment: "production", Value: "info"},
				{Name: "API_KEY", Environment: "production", SecretRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: testProjectName + "-shared-secrets"}, Key: "production__API_KEY",
				}},
			},
		},
	}
	sharedSecrets := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName + "-shared-secrets", Namespace: platform.SystemNamespace},
		Data:       map[string][]byte{"production__API_KEY": []byte("shared-key-value")},
	}
	web := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "little-frost", Namespace: platform.SystemNamespace, Annotations: map[string]string{platform.DisplayNameAnnotation: "web"}},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: testProjectName, Environment: "production", Replicas: &three,
			Source: geassv1alpha1.GeassAppSource{Git: &geassv1alpha1.GeassAppGitSource{
				ConnectionRef: corev1.LocalObjectReference{Name: "payments-github"}, Repository: prRepo, Branch: "main", Commit: "pinned",
			}},
			Ingress:     geassv1alpha1.GeassAppIngressSpec{Host: "app.example.com", TLSEnabled: true},
			Autoscaling: &geassv1alpha1.GeassAppAutoscalingSpec{MaxReplicas: 6},
			ConfigData:  map[string]string{"FEATURE": "on"},
			SecretRef:   &corev1.LocalObjectReference{Name: "little-frost-secrets"},
			Env: []corev1.EnvVar{
				{Name: "DATABASE_URL", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: corev1.LocalObjectReference{Name: "scarlet-creek-connection"}, Key: "uri"}}},
				{Name: "REGION", Value: "eu"},
			},
			Deploy: geassv1alpha1.GeassAppDeploySpec{Enabled: true},
		},
	}
	appSecrets := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "little-frost-secrets", Namespace: platform.SystemNamespace},
		Data:       map[string][]byte{"API_TOKEN": []byte("app-token-value")},
	}
	worker := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "modest-lily", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassAppSpec{Project: testProjectName, Environment: "production", Source: imageAppSource("nginx:alpine")},
	}
	db := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "scarlet-creek", Namespace: platform.SystemNamespace, Annotations: map[string]string{platform.DisplayNameAnnotation: "main db"}},
		Spec: geassv1alpha1.GeassDatabaseSpec{
			Project: testProjectName, Environment: "production", Engine: geassv1alpha1.DatabaseEnginePostgres,
			Placement: geassv1alpha1.DatabasePlacementInCluster, HighAvailability: true, Version: "16",
		},
	}
	three2 := int32(3)
	db.Spec.Instances = &three2
	external := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "managed-db", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassDatabaseSpec{
			Project: testProjectName, Environment: "production", Engine: geassv1alpha1.DatabaseEnginePostgres,
			Placement: geassv1alpha1.DatabasePlacementExternal,
		},
	}
	bucket := &geassv1alpha1.GeassObjectStore{
		ObjectMeta: metav1.ObjectMeta{Name: "assets", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassObjectStoreSpec{Project: testProjectName, Environment: "production"},
	}
	objects := append([]client.Object{project, sharedSecrets, web, appSecrets, worker, db, external, bucket}, extra...)
	c := newFakeClient(objects...)
	return &Server{Client: c}, c
}

func enabledPR() *geassv1alpha1.GeassPREnvironments {
	return &geassv1alpha1.GeassPREnvironments{Enabled: true, BaseEnvironment: "production", MaxOpen: 5}
}

func getApp(t *testing.T, c client.Client, name string) (geassv1alpha1.GeassApp, bool) {
	t.Helper()
	var app geassv1alpha1.GeassApp
	err := c.Get(context.Background(), client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, &app)
	return app, err == nil
}

func getDatabase(t *testing.T, c client.Client, name string) (geassv1alpha1.GeassDatabase, bool) {
	t.Helper()
	var db geassv1alpha1.GeassDatabase
	err := c.Get(context.Background(), client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, &db)
	return db, err == nil
}

func TestOpeningAPullRequestClonesTheBaseEnvironment(t *testing.T) {
	ctx := context.Background()
	srv, c := prFixture(t, enabledPR())

	results, err := srv.handlePullRequestEvent(ctx, prPayload("opened", 7, "feature-x"))
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "pr-7", results[0].Env)
	require.Equal(t, 3, results[0].Created, "two services and one database")

	project := projectOf(t, c)
	require.Equal(t, []string{"production", "pr-7"}, project.Spec.Environments)
	require.Len(t, project.Status.PullRequests, 1)
	entry := project.Status.PullRequests[0]
	require.Equal(t, int32(7), entry.Number)
	require.Equal(t, "feature-x", entry.Branch)
	require.Equal(t, "https://github.com/acme/web/pull/7", entry.URL)

	web, ok := getApp(t, c, "little-frost-pr7")
	require.True(t, ok, "the git service is cloned")
	require.Equal(t, "pr-7", string(web.Spec.Environment))
	require.Equal(t, "feature-x", web.Spec.Source.Git.Branch, "a service that builds this repository follows the pull request branch")
	require.Empty(t, web.Spec.Source.Git.Commit, "the pinned commit is dropped so new pushes are built")
	require.True(t, web.Spec.Deploy.Enabled, "pull request environments deploy on their own")
	require.NotEmpty(t, web.Annotations[platform.AppLastDeployedAnnotation])
	require.Empty(t, web.Annotations[platform.AppPendingChangesAnnotation], "a clone is not a draft")
	require.Equal(t, "web", web.Annotations[platform.DisplayNameAnnotation], "the canvas shows the same names as the base environment")
	require.Equal(t, int32(1), *web.Spec.Replicas, "a review environment runs one replica")
	require.Nil(t, web.Spec.Autoscaling)
	require.Empty(t, web.Spec.Ingress.Host, "the base environment keeps its own domain")
	require.False(t, web.Spec.Ingress.TLSEnabled)
	require.Equal(t, "on", web.Spec.ConfigData["FEATURE"])

	require.Len(t, web.Spec.Env, 2)
	require.Equal(t, "scarlet-creek-pr7-connection", web.Spec.Env[0].ValueFrom.SecretKeyRef.Name,
		"the clone talks to its own database, not the base environment's")
	require.Equal(t, "uri", web.Spec.Env[0].ValueFrom.SecretKeyRef.Key)
	require.Equal(t, "eu", web.Spec.Env[1].Value)

	require.NotNil(t, web.Spec.SecretRef)
	require.Equal(t, "little-frost-pr7-secrets", web.Spec.SecretRef.Name)
	var copied corev1.Secret
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "little-frost-pr7-secrets", Namespace: platform.SystemNamespace}, &copied))
	require.Equal(t, "app-token-value", string(copied.Data["API_TOKEN"]))

	worker, ok := getApp(t, c, "modest-lily-pr7")
	require.True(t, ok)
	require.Equal(t, "nginx:alpine", worker.Spec.Source.Image.Image)

	clone, ok := getDatabase(t, c, "scarlet-creek-pr7")
	require.True(t, ok, "the in-cluster database is cloned as a fresh instance")
	require.Equal(t, "pr-7", string(clone.Spec.Environment))
	require.Equal(t, geassv1alpha1.DatabaseEnginePostgres, clone.Spec.Engine)
	require.Equal(t, "16", clone.Spec.Version)
	require.False(t, clone.Spec.HighAvailability)
	require.Nil(t, clone.Spec.Instances)
	require.True(t, clone.Spec.Deploy.Enabled)
	require.Equal(t, "main db", clone.Annotations[platform.DisplayNameAnnotation])

	_, copiedExternal := getDatabase(t, c, "managed-db-pr7")
	require.False(t, copiedExternal, "external databases are not copied")
	var stores geassv1alpha1.GeassObjectStoreList
	require.NoError(t, c.List(ctx, &stores))
	require.Len(t, stores.Items, 1, "buckets are not copied")

	var shared = map[string]geassv1alpha1.GeassSharedVariable{}
	for _, variable := range project.Spec.SharedVariables {
		if variable.Environment == "pr-7" {
			shared[variable.Name] = variable
		}
	}
	require.Equal(t, "info", shared["LOG_LEVEL"].Value)
	require.NotNil(t, shared["API_KEY"].SecretRef)
	require.Equal(t, "pr-7__API_KEY", shared["API_KEY"].SecretRef.Key)
	var sharedSecret corev1.Secret
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: testProjectName + "-shared-secrets", Namespace: platform.SystemNamespace}, &sharedSecret))
	require.Equal(t, "shared-key-value", string(sharedSecret.Data["pr-7__API_KEY"]), "shared secrets are copied for the new environment")
	require.Equal(t, "shared-key-value", string(sharedSecret.Data["production__API_KEY"]), "and the base environment's stay")

	base, _ := getApp(t, c, "little-frost")
	require.Equal(t, "main", base.Spec.Source.Git.Branch, "the base environment is untouched")
	require.Equal(t, int32(3), *base.Spec.Replicas)
	require.Equal(t, "app.example.com", base.Spec.Ingress.Host)
}

func TestRepeatedPullRequestEventsDoNotDuplicateAnything(t *testing.T) {
	ctx := context.Background()
	srv, c := prFixture(t, enabledPR())
	_, err := srv.handlePullRequestEvent(ctx, prPayload("opened", 7, "feature-x"))
	require.NoError(t, err)

	results, err := srv.handlePullRequestEvent(ctx, prPayload("synchronize", 7, "feature-x"))
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Zero(t, results[0].Created, "nothing new is created for a push to the branch")
	results, err = srv.handlePullRequestEvent(ctx, prPayload("reopened", 7, "feature-x"))
	require.NoError(t, err)
	require.Zero(t, results[0].Created)

	project := projectOf(t, c)
	require.Equal(t, []string{"production", "pr-7"}, project.Spec.Environments)
	require.Len(t, project.Status.PullRequests, 1)
	sharedForPR := 0
	for _, variable := range project.Spec.SharedVariables {
		if variable.Environment == "pr-7" {
			sharedForPR++
		}
	}
	require.Equal(t, 2, sharedForPR)
}

func TestSynchronizeRepairsAnEnvironmentThatIsMissingPieces(t *testing.T) {
	ctx := context.Background()
	srv, c := prFixture(t, enabledPR())
	_, err := srv.handlePullRequestEvent(ctx, prPayload("opened", 7, "feature-x"))
	require.NoError(t, err)
	web, _ := getApp(t, c, "little-frost-pr7")
	require.NoError(t, c.Delete(ctx, &web))

	results, err := srv.handlePullRequestEvent(ctx, prPayload("synchronize", 7, "feature-x"))
	require.NoError(t, err)
	require.Equal(t, 1, results[0].Created)
	_, ok := getApp(t, c, "little-frost-pr7")
	require.True(t, ok, "a half-created environment heals on the next event")
}

func TestPullRequestsThatShouldNotGetAnEnvironmentAreIgnored(t *testing.T) {
	ctx := context.Background()
	cases := map[string]struct {
		config *geassv1alpha1.GeassPREnvironments
		edit   func(*githubPullRequestPayload)
	}{
		"feature disabled":         {config: &geassv1alpha1.GeassPREnvironments{Enabled: false, BaseEnvironment: "production"}},
		"feature never configured": {config: nil},
		"pull request from a fork": {config: enabledPR(), edit: func(p *githubPullRequestPayload) { p.PullRequest.Head.Repo.FullName = "mallory/web" }},
		"targets another branch":   {config: enabledPR(), edit: func(p *githubPullRequestPayload) { p.PullRequest.Base.Ref = "release" }},
		"repository not built": {config: enabledPR(), edit: func(p *githubPullRequestPayload) {
			p.Repository.FullName = "acme/other"
			p.PullRequest.Head.Repo.FullName = "acme/other"
			p.PullRequest.Base.Repo.FullName = "acme/other"
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			srv, c := prFixture(t, tc.config)
			payload := prPayload("opened", 7, "feature-x")
			if tc.edit != nil {
				tc.edit(&payload)
			}
			results, err := srv.handlePullRequestEvent(ctx, payload)
			require.NoError(t, err)
			require.Empty(t, results)
			require.Equal(t, []string{"production"}, projectOf(t, c).Spec.Environments)
			_, cloned := getApp(t, c, "little-frost-pr7")
			require.False(t, cloned)
		})
	}
}

func TestThePullRequestEnvironmentLimitIsEnforced(t *testing.T) {
	ctx := context.Background()
	config := enabledPR()
	config.MaxOpen = 1
	srv, c := prFixture(t, config)

	_, err := srv.handlePullRequestEvent(ctx, prPayload("opened", 7, "feature-x"))
	require.NoError(t, err)
	results, err := srv.handlePullRequestEvent(ctx, prPayload("opened", 8, "feature-y"))
	require.NoError(t, err)

	require.Len(t, results, 1)
	require.Empty(t, results[0].Env)
	require.Contains(t, results[0].Note, "limit of 1")
	require.Equal(t, []string{"production", "pr-7"}, projectOf(t, c).Spec.Environments, "the second pull request got no environment")
	_, ok := getApp(t, c, "little-frost-pr8")
	require.False(t, ok)

	_, err = srv.handlePullRequestEvent(ctx, prPayload("closed", 7, "feature-x"))
	require.NoError(t, err)
	results, err = srv.handlePullRequestEvent(ctx, prPayload("opened", 8, "feature-y"))
	require.NoError(t, err)
	require.Equal(t, "pr-8", results[0].Env, "closing a pull request frees a slot")
}

func TestClosingAPullRequestRemovesItsEnvironmentAndNothingElse(t *testing.T) {
	ctx := context.Background()
	srv, c := prFixture(t, enabledPR())
	_, err := srv.handlePullRequestEvent(ctx, prPayload("opened", 7, "feature-x"))
	require.NoError(t, err)
	_, err = srv.handlePullRequestEvent(ctx, prPayload("opened", 8, "feature-y"))
	require.NoError(t, err)

	results, err := srv.handlePullRequestEvent(ctx, prPayload("closed", 7, "feature-x"))
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "pr-7", results[0].Env)
	require.Equal(t, 3, results[0].Removed, "three clones deleted")

	project := projectOf(t, c)
	require.Equal(t, []string{"production", "pr-8"}, project.Spec.Environments)
	require.Len(t, project.Status.PullRequests, 1)
	require.Equal(t, int32(8), project.Status.PullRequests[0].Number)
	for _, variable := range project.Spec.SharedVariables {
		require.NotEqual(t, "pr-7", variable.Environment, "the environment's shared variables go with it")
	}
	var sharedSecret corev1.Secret
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: testProjectName + "-shared-secrets", Namespace: platform.SystemNamespace}, &sharedSecret))
	require.NotContains(t, sharedSecret.Data, "pr-7__API_KEY")
	require.Contains(t, sharedSecret.Data, "pr-8__API_KEY", "other pull requests keep theirs")
	require.Contains(t, sharedSecret.Data, "production__API_KEY")

	for _, name := range []string{"little-frost-pr7", "modest-lily-pr7"} {
		_, ok := getApp(t, c, name)
		require.False(t, ok, name)
	}
	_, ok := getDatabase(t, c, "scarlet-creek-pr7")
	require.False(t, ok)
	for _, name := range []string{"little-frost", "little-frost-pr8", "modest-lily"} {
		_, ok := getApp(t, c, name)
		require.True(t, ok, "%s must survive", name)
	}

	results, err = srv.handlePullRequestEvent(ctx, prPayload("closed", 99, "nothing"))
	require.NoError(t, err)
	require.Empty(t, results, "closing a pull request that never had an environment is a no-op")
}

func TestTheSamePullRequestNumberInTwoRepositoriesGetsDistinctEnvironments(t *testing.T) {
	ctx := context.Background()
	api := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "grand-owl", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: testProjectName, Environment: "production",
			Source: geassv1alpha1.GeassAppSource{Git: &geassv1alpha1.GeassAppGitSource{Repository: "acme/api", Branch: "main"}},
		},
	}
	srv, c := prFixture(t, enabledPR(), api)

	_, err := srv.handlePullRequestEvent(ctx, prPayload("opened", 7, "feature-x"))
	require.NoError(t, err)
	other := prPayload("opened", 7, "feature-z")
	other.Repository.FullName = "acme/api"
	other.PullRequest.Head.Repo.FullName = "acme/api"
	other.PullRequest.Base.Repo.FullName = "acme/api"
	results, err := srv.handlePullRequestEvent(ctx, other)
	require.NoError(t, err)

	require.Equal(t, "pr-7-api", results[0].Env)
	require.Equal(t, []string{"production", "pr-7", "pr-7-api"}, projectOf(t, c).Spec.Environments)

	_, err = srv.handlePullRequestEvent(ctx, prPayload("closed", 7, "feature-x"))
	require.NoError(t, err)
	require.Equal(t, []string{"production", "pr-7-api"}, projectOf(t, c).Spec.Environments, "closing one repository's pull request leaves the other's")
}

func TestCloneNamesStayWithinKubernetesLimits(t *testing.T) {
	require.Equal(t, "web-pr7", cloneName("web", 7))
	long := strings.Repeat("a", 63)
	name := cloneName(long, 1234)
	require.LessOrEqual(t, len(name), 63)
	require.True(t, strings.HasSuffix(name, "-pr1234"), "the pull request number must survive truncation")
	require.NotContains(t, cloneName(strings.Repeat("a", 58)+"-b", 12), "--")
}

func TestPullRequestEventsOnlyReachProjectsOnTheSameGitHubInstallation(t *testing.T) {
	ctx := context.Background()
	cfg := testGitHubAppConfig(t)
	srv, c := prFixture(t, enabledPR(),
		testPlatformConfig("https://geass.test"), testPlatformGitHubSecret(t, cfg),
		&geassv1alpha1.GeassGitHubConnection{
			ObjectMeta: metav1.ObjectMeta{Name: "payments-github", Namespace: platform.SystemNamespace},
			Spec:       geassv1alpha1.GeassGitHubConnectionSpec{SecretRef: corev1.LocalObjectReference{Name: "payments-github-token"}},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "payments-github-token", Namespace: platform.SystemNamespace},
			Data:       map[string][]byte{"installation_id": []byte("42"), "token": []byte("tok")},
		},
	)
	var project geassv1alpha1.GeassProject
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: testProjectName, Namespace: platform.SystemNamespace}, &project))
	project.Spec.GitHubConnectionRef = &corev1.LocalObjectReference{Name: "payments-github"}
	require.NoError(t, c.Update(ctx, &project))

	send := func(installation int64) *httptest.ResponseRecorder {
		payload := map[string]any{
			"action": "opened", "number": 7,
			"installation": map[string]any{"id": installation},
			"repository":   map[string]string{"full_name": prRepo},
			"pull_request": map[string]any{
				"html_url": "https://github.com/acme/web/pull/7",
				"head":     map[string]any{"ref": "feature-x", "sha": "abc", "repo": map[string]string{"full_name": prRepo}},
				"base":     map[string]any{"ref": "main", "repo": map[string]string{"full_name": prRepo}},
			},
		}
		req := signedGitHubWebhook(t, cfg.WebhookSecret, payload).WithContext(ctx)
		req.Header.Set("X-GitHub-Event", "pull_request")
		rec := httptest.NewRecorder()
		srv.handleGitHubWebhook(rec, req)
		return rec
	}

	rec := send(999)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, []string{"production"}, projectOf(t, c).Spec.Environments, "another installation's event must not create environments here")

	rec = send(42)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), `"environment":"pr-7"`)
	require.Equal(t, []string{"production", "pr-7"}, projectOf(t, c).Spec.Environments)

	// An unsigned request is refused before anything is read.
	unsigned := withOrigin(httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader(`{"action":"opened"}`)))
	unsigned.Header.Set("X-GitHub-Event", "pull_request")
	bad := httptest.NewRecorder()
	srv.handleGitHubWebhook(bad, unsigned)
	require.Equal(t, http.StatusUnauthorized, bad.Code)
}

func postPR(t *testing.T, srv *Server, operation string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.handleProjectRoutes(rec, withAdminSession(t, srv, postForm("/projects/"+testProjectName+"/pr-environments/"+operation, form)))
	return rec
}

func TestSavingThePullRequestSettings(t *testing.T) {
	srv, c := prFixture(t, nil)

	rec := postPR(t, srv, "save", url.Values{"enabled": {"on"}, "baseEnvironment": {"production"}, "maxOpen": {"3"}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	config := projectOf(t, c).Spec.PREnvironments
	require.NotNil(t, config)
	require.True(t, config.Enabled)
	require.Equal(t, "production", config.BaseEnvironment)
	require.Equal(t, int32(3), config.MaxOpen)

	require.Equal(t, http.StatusBadRequest, postPR(t, srv, "save", url.Values{"enabled": {"on"}, "baseEnvironment": {"nope"}}).Code, "the base environment must exist")
	require.Equal(t, http.StatusBadRequest, postPR(t, srv, "save", url.Values{"enabled": {"on"}, "maxOpen": {"0"}}).Code)
	require.Equal(t, http.StatusBadRequest, postPR(t, srv, "save", url.Values{"enabled": {"on"}, "maxOpen": {"99"}}).Code)

	rec = postPR(t, srv, "save", url.Values{"baseEnvironment": {"production"}})
	require.Equal(t, http.StatusOK, rec.Code)
	require.False(t, projectOf(t, c).Spec.PREnvironments.Enabled, "leaving enabled off turns the feature off")
}

func TestAPullRequestEnvironmentCanBeClosedByHand(t *testing.T) {
	ctx := context.Background()
	srv, c := prFixture(t, enabledPR())
	_, err := srv.handlePullRequestEvent(ctx, prPayload("opened", 7, "feature-x"))
	require.NoError(t, err)

	require.Equal(t, http.StatusBadRequest, postPR(t, srv, "close", url.Values{"number": {"x"}, "repository": {prRepo}}).Code)
	require.Equal(t, http.StatusNotFound, postPR(t, srv, "close", url.Values{"number": {"99"}, "repository": {prRepo}}).Code)

	rec := postPR(t, srv, "close", url.Values{"number": {"7"}, "repository": {prRepo}})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, []string{"production"}, projectOf(t, c).Spec.Environments)
	require.Empty(t, projectOf(t, c).Status.PullRequests)
}

func TestClosingAPullRequestStillCleansUpAfterTheFeatureIsTurnedOff(t *testing.T) {
	ctx := context.Background()
	srv, c := prFixture(t, enabledPR())
	_, err := srv.handlePullRequestEvent(ctx, prPayload("opened", 7, "feature-x"))
	require.NoError(t, err)

	require.NoError(t, srv.mutateProject(ctx, testProjectName, func(p *geassv1alpha1.GeassProject) error {
		p.Spec.PREnvironments.Enabled = false
		return nil
	}))

	results, err := srv.handlePullRequestEvent(ctx, prPayload("opened", 8, "feature-y"))
	require.NoError(t, err)
	require.Empty(t, results, "no new environments while the feature is off")

	results, err = srv.handlePullRequestEvent(ctx, prPayload("closed", 7, "feature-x"))
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "pr-7", results[0].Env)
	project := projectOf(t, c)
	require.Equal(t, []string{"production"}, project.Spec.Environments)
	require.Empty(t, project.Status.PullRequests)
	_, ok := getApp(t, c, "little-frost-pr7")
	require.False(t, ok, "the environment's services are removed even though the feature is off")
}

// A review environment is throwaway. Copying the base database's backups would give every pull
// request its own continuously archived folder in the project's bucket, and copying a restore
// source would try to rebuild the base database's data inside each review environment.
func TestPullRequestDatabasesDoNotInheritBackupsOrRestores(t *testing.T) {
	ctx := context.Background()
	protected := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: "protected-db", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassDatabaseSpec{
			Project: testProjectName, Environment: "production", Engine: geassv1alpha1.DatabaseEnginePostgres,
			Placement: geassv1alpha1.DatabasePlacementInCluster, Version: "16",
			Backup:      &geassv1alpha1.GeassDatabaseBackupSpec{Enabled: true, RetentionDays: 30},
			RestoreFrom: &geassv1alpha1.GeassDatabaseRestoreSpec{Archive: "old-archive-1a2b3c4d", RestorePoint: "20260301T020000"},
		},
	}
	srv, c := prFixture(t, enabledPR(), protected)

	_, err := srv.handlePullRequestEvent(ctx, prPayload("opened", 7, "feature-x"))
	require.NoError(t, err)

	clone, ok := getDatabase(t, c, "protected-db-pr7")
	require.True(t, ok, "the database itself is still cloned")
	require.Nil(t, clone.Spec.Backup, "review environments are not backed up")
	require.Nil(t, clone.Spec.RestoreFrom, "a review database starts empty rather than restoring the base database's data")
	require.Equal(t, "16", clone.Spec.Version)

	snapshot, ok := platform.UnmarshalLastDeployed[geassv1alpha1.GeassDatabaseSpec](clone.Annotations)
	require.True(t, ok)
	require.Nil(t, snapshot.Backup, "the deployed snapshot is what the controller actually runs, so it must be clean too")
	require.Nil(t, snapshot.RestoreFrom)

	base, _ := getDatabase(t, c, "protected-db")
	require.NotNil(t, base.Spec.Backup, "the base database keeps its backups")
	require.Equal(t, int32(30), base.Spec.Backup.RetentionDays)
	require.NotNil(t, base.Spec.RestoreFrom)
}
