package dashboard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kubefake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const (
	oldDigest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	newDigest = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
)

func gitApp(mutate ...func(*geassv1alpha1.GeassApp)) *geassv1alpha1.GeassApp {
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: testProjectName, Environment: geassv1alpha1.EnvironmentDev,
			Deploy: geassv1alpha1.GeassAppDeploySpec{Enabled: true},
			Source: geassv1alpha1.GeassAppSource{Git: &geassv1alpha1.GeassAppGitSource{
				Repository: "org/api", Branch: "main", ConnectionRef: corev1.LocalObjectReference{Name: "payments-github"},
			}},
		},
	}
	for _, m := range mutate {
		m(app)
	}
	return app
}

// operatorBuild is a build as the operator leaves it: labelled with platform.GitBuildLabels, the labels the
// operator's ensureGitBuild puts on every build it makes, and finished with a digest, a commit and a log.
func operatorBuild(app *geassv1alpha1.GeassApp, name string, age time.Duration, phase geassv1alpha1.GeassBuildPhase) *geassv1alpha1.GeassBuild {
	started := metav1.NewTime(time.Now().Add(-age))
	return &geassv1alpha1.GeassBuild{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace, Labels: platform.GitBuildLabels(app), CreationTimestamp: started},
		Spec:       geassv1alpha1.GeassBuildSpec{App: app.Name, Project: app.Spec.Project, Environment: app.Spec.Environment},
		Status: geassv1alpha1.GeassBuildStatus{
			Phase: phase, StartedAt: &started, CompletedAt: &started, ImageDigest: oldDigest, SourceRevision: "0123456789abcdef",
			CIState: "success", CIChecks: []string{"test"}, LogRef: "object://logs/builds/" + name + ".log",
		},
	}
}

func buildOf(t *testing.T, c client.Client, name string) geassv1alpha1.GeassBuild {
	t.Helper()
	var build geassv1alpha1.GeassBuild
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, &build))
	return build
}

func pushPayload(files ...[]string) map[string]any {
	payload := map[string]any{
		"installation": map[string]any{"id": 42},
		"repository":   map[string]string{"full_name": "org/api"},
		"ref":          "refs/heads/main",
	}
	if len(files) > 0 {
		commits := make([]map[string]any, 0, len(files))
		for _, modified := range files {
			commits = append(commits, map[string]any{"modified": modified})
		}
		payload["commits"] = commits
	}
	return payload
}

// pushServer is a dashboard with a service, its GitHub connection and the builds given, and the push webhook.
func pushServer(t *testing.T, app *geassv1alpha1.GeassApp, builds ...client.Object) (*Server, client.Client, string) {
	t.Helper()
	cfg := testGitHubAppConfig(t)
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: testProjectName, Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassProjectSpec{
			ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{"dev"},
			GitHubConnectionRef: &corev1.LocalObjectReference{Name: "payments-github"},
		},
	}
	connection := &geassv1alpha1.GeassGitHubConnection{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-github", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassGitHubConnectionSpec{SecretRef: corev1.LocalObjectReference{Name: "payments-github-token"}},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "payments-github-token", Namespace: platform.SystemNamespace},
		Data:       map[string][]byte{"installation_id": []byte("42"), "token": []byte("tok")},
	}
	objects := append([]client.Object{testPlatformConfig("https://geass.test"), testPlatformGitHubSecret(t, cfg), project, connection, secret, app}, builds...)
	c := newFakeClient(objects...)
	return &Server{Client: c}, c, cfg.WebhookSecret
}

func push(t *testing.T, srv *Server, secret string, payload any) string {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.handleGitHubWebhook(rec, signedGitHubWebhook(t, secret, payload))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	return rec.Body.String()
}

func TestAPushRerunsTheNewestBuildOfTheServiceAndForgetsItsLastRun(t *testing.T) {
	app := gitApp()
	older := operatorBuild(app, "api-build-old", 2*time.Hour, geassv1alpha1.GeassBuildSucceeded)
	newest := operatorBuild(app, "api-build-new", time.Hour, geassv1alpha1.GeassBuildSucceeded)
	srv, c, secret := pushServer(t, app, older, newest)

	require.Contains(t, push(t, srv, secret, pushPayload()), `"triggered":1`, "a push to the branch of a deployed service builds it")

	rerun := buildOf(t, c, "api-build-new")
	require.Equal(t, geassv1alpha1.GeassBuildPending, rerun.Status.Phase)
	require.Empty(t, rerun.Status.ImageDigest, "the digest of the last run is not the result of this one")
	require.Empty(t, rerun.Status.SourceRevision, "the commit is resolved again, so the new push is what gets built")
	require.Empty(t, rerun.Status.CIState)
	require.Nil(t, rerun.Status.StartedAt)
	require.Nil(t, rerun.Status.CompletedAt)
	require.Empty(t, rerun.Status.LogRef)
	untouched := buildOf(t, c, "api-build-old")
	require.Equal(t, geassv1alpha1.GeassBuildSucceeded, untouched.Status.Phase, "an older build of the service is left as it was")
	require.Equal(t, oldDigest, untouched.Status.ImageDigest)
}

func TestAPushDeletesTheJobOfTheLastRunSoItsResultIsNotReadAgain(t *testing.T) {
	app := gitApp()
	build := operatorBuild(app, "api-build-1", time.Hour, geassv1alpha1.GeassBuildSucceeded)
	srv, _, secret := pushServer(t, app, build)
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: "api-build-1-kaniko", Namespace: platform.SystemNamespace}}
	srv.Kube = kubefake.NewClientset(job)

	push(t, srv, secret, pushPayload())

	_, err := srv.Kube.BatchV1().Jobs(platform.SystemNamespace).Get(context.Background(), "api-build-1-kaniko", metav1.GetOptions{})
	require.Error(t, err, "the Job is gone, so the build does not finish at once with what it said last time")
}

func TestAPushIsOnlyForTheServiceThatBuildsThatBranch(t *testing.T) {
	for name, tt := range map[string]struct {
		mutate  func(*geassv1alpha1.GeassApp)
		payload map[string]any
	}{
		"another branch":  {payload: func() map[string]any { p := pushPayload(); p["ref"] = "refs/heads/feature"; return p }()},
		"not deployed":    {mutate: func(a *geassv1alpha1.GeassApp) { a.Spec.Deploy.Enabled = false }, payload: pushPayload()},
		"a pinned commit": {mutate: func(a *geassv1alpha1.GeassApp) { a.Spec.Source.Git.Commit = "0123456789abcdef0123456789abcdef01234567" }, payload: pushPayload()},
		"the branch was deleted": {payload: func() map[string]any {
			p := pushPayload()
			p["deleted"] = true
			return p
		}()},
	} {
		t.Run(name, func(t *testing.T) {
			app := gitApp(func(a *geassv1alpha1.GeassApp) {
				if tt.mutate != nil {
					tt.mutate(a)
				}
			})
			srv, c, secret := pushServer(t, app, operatorBuild(app, "api-build-1", time.Hour, geassv1alpha1.GeassBuildSucceeded))

			require.Contains(t, push(t, srv, secret, tt.payload), `"triggered":0`)

			require.Equal(t, geassv1alpha1.GeassBuildSucceeded, buildOf(t, c, "api-build-1").Status.Phase)
		})
	}
}

func TestAPushOnlyBuildsWhatTheWatchPathsWatch(t *testing.T) {
	watching := func(patterns ...string) *geassv1alpha1.GeassApp {
		return gitApp(func(a *geassv1alpha1.GeassApp) { a.Spec.Source.Git.WatchPatterns = patterns })
	}
	for name, tt := range map[string]struct {
		app     *geassv1alpha1.GeassApp
		payload map[string]any
		want    string
	}{
		"nothing watched, any change":                  {watching(), pushPayload([]string{"README.md"}), `"triggered":1`},
		"a change under a watched path":                {watching("/src/**"), pushPayload([]string{"README.md"}, []string{"src/main.go"}), `"triggered":1`},
		"only changes outside it":                      {watching("/src/**"), pushPayload([]string{"README.md"}, []string{"docs/a.md"}), `"triggered":0`},
		"only docs, which are taken out":               {watching("**", "!/docs/**"), pushPayload([]string{"docs/a.md"}), `"triggered":0`},
		"docs and code":                                {watching("**", "!/docs/**"), pushPayload([]string{"docs/a.md", "main.go"}), `"triggered":1`},
		"a push that does not say what changed builds": {watching("/src/**"), pushPayload(), `"triggered":1`},
	} {
		t.Run(name, func(t *testing.T) {
			srv, _, secret := pushServer(t, tt.app, operatorBuild(tt.app, "api-build-1", time.Hour, geassv1alpha1.GeassBuildSucceeded))

			require.Contains(t, push(t, srv, secret, tt.payload), tt.want)
		})
	}
}

func TestAPushForAServiceWithNoBuildYetTriggersNothing(t *testing.T) {
	srv, _, secret := pushServer(t, gitApp())

	require.Contains(t, push(t, srv, secret, pushPayload()), `"triggered":0`, "its build is made when it is deployed")
}

func buildActionRequest(t *testing.T, srv *Server, action string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := withAdminSession(t, srv, postForm("/apps/api/"+action, url.Values{}))
	srv.handleAppBuildAction(rec, req, "api", action == "build/cancel")
	return rec
}

func TestTheBuildActionsActOnTheNewestBuildNotTheFirstListed(t *testing.T) {
	app := gitApp()
	older := operatorBuild(app, "a-build", 2*time.Hour, geassv1alpha1.GeassBuildSucceeded) // sorts first, and is older
	newest := operatorBuild(app, "z-build", time.Hour, geassv1alpha1.GeassBuildFailed)
	srv, c, _ := pushServer(t, app, older, newest)

	rec := buildActionRequest(t, srv, "build")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	rerun := buildOf(t, c, "z-build")
	require.Equal(t, geassv1alpha1.GeassBuildPending, rerun.Status.Phase)
	require.Empty(t, rerun.Status.ImageDigest, "a rebuild does not keep the digest of the run before it")
	require.Empty(t, rerun.Status.SourceRevision, "and builds the head of the branch as it is now")
	require.Nil(t, rerun.Status.StartedAt)
	require.Equal(t, geassv1alpha1.GeassBuildSucceeded, buildOf(t, c, "a-build").Status.Phase)

	rec = buildActionRequest(t, srv, "build/cancel")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	cancelled := buildOf(t, c, "z-build")
	require.True(t, cancelled.Status.CancelRequested)
	require.Equal(t, geassv1alpha1.GeassBuildCancelled, cancelled.Status.Phase)
	require.Equal(t, geassv1alpha1.GeassBuildSucceeded, buildOf(t, c, "a-build").Status.Phase, "the older build is not the one that is cancelled")
}

func TestTheBuildActionDoesNotMakeABuildNothingWouldDeploy(t *testing.T) {
	app := gitApp()
	srv, c, _ := pushServer(t, app)

	rec := buildActionRequest(t, srv, "build")

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "not been built yet")
	var builds geassv1alpha1.GeassBuildList
	require.NoError(t, c.List(context.Background(), &builds))
	require.Empty(t, builds.Items, "a build with a random name is not the one the service uses, and has no credential to clone with")
}

func TestBuildLogsAreReadFromThePodsOfTheBuildsJob(t *testing.T) {
	app := gitApp()
	build := operatorBuild(app, "api-build-1", time.Hour, geassv1alpha1.GeassBuildRunning)
	build.Status.LogRef = ""
	srv, _, _ := pushServer(t, app, build)
	// The pod of a Job is named <job>-<random suffix>, which is why asking for <job> never found it.
	srv.Kube = kubefake.NewClientset(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Name: "api-build-1-kaniko-x7k2p", Namespace: platform.SystemNamespace, Labels: map[string]string{"job-name": "api-build-1-kaniko"},
	}})

	section := srv.buildLogSection(context.Background(), "api-build-1")

	require.Equal(t, "fake logs", section.Lines, "the output of the pod, not the build's phase")
}

// serverIntercepting is a dashboard whose client runs the given interceptors, over the same objects and scheme as
// newFakeClient.
func serverIntercepting(t *testing.T, funcs interceptor.Funcs, objects ...client.Object) (*Server, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).
		WithStatusSubresource(&geassv1alpha1.GeassBuild{}, &geassv1alpha1.GeassApp{}, &geassv1alpha1.GeassPlatformConfig{}).
		WithInterceptorFuncs(funcs).Build()
	return &Server{Client: c}, c
}

func TestAPushRerunsTheBuildTheServiceUsesNotTheNewestOne(t *testing.T) {
	// Cache turned on (build 2) and off again: the service went back to build 1, the older one.
	app := gitApp(func(a *geassv1alpha1.GeassApp) { a.Status.CurrentBuild = "api-build-1" })
	one := operatorBuild(app, "api-build-1", 2*time.Hour, geassv1alpha1.GeassBuildSucceeded)
	two := operatorBuild(app, "api-build-2", time.Hour, geassv1alpha1.GeassBuildSucceeded)
	srv, c, secret := pushServer(t, app, one, two)

	require.Contains(t, push(t, srv, secret, pushPayload()), `"triggered":1`)

	require.Equal(t, geassv1alpha1.GeassBuildPending, buildOf(t, c, "api-build-1").Status.Phase, "the build the service runs is the one rebuilt")
	require.Equal(t, geassv1alpha1.GeassBuildSucceeded, buildOf(t, c, "api-build-2").Status.Phase, "the newer build that nothing uses is left alone")
}

func TestTheBuildActionsAndStatusFollowTheBuildTheServiceUses(t *testing.T) {
	app := gitApp(func(a *geassv1alpha1.GeassApp) { a.Status.CurrentBuild = "z-build" })
	older := operatorBuild(app, "z-build", 2*time.Hour, geassv1alpha1.GeassBuildRunning) // used by the service, but not the newest
	newest := operatorBuild(app, "a-build", time.Hour, geassv1alpha1.GeassBuildSucceeded)
	srv, c, _ := pushServer(t, app, older, newest)

	current, err := srv.currentBuild(context.Background(), app)
	require.NoError(t, err)
	require.Equal(t, "z-build", current.Name)

	rec := buildActionRequest(t, srv, "build/cancel")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, geassv1alpha1.GeassBuildCancelled, buildOf(t, c, "z-build").Status.Phase)
	require.Equal(t, geassv1alpha1.GeassBuildSucceeded, buildOf(t, c, "a-build").Status.Phase)
}

func TestWithNoRecordOfTheCurrentBuildTheNewestIsUsed(t *testing.T) {
	app := gitApp() // not reconciled yet, or by an operator from before the record
	srv, _, _ := pushServer(t, app, operatorBuild(app, "a-build", 2*time.Hour, geassv1alpha1.GeassBuildSucceeded), operatorBuild(app, "z-build", time.Hour, geassv1alpha1.GeassBuildSucceeded))

	current, err := srv.currentBuild(context.Background(), app)

	require.NoError(t, err)
	require.Equal(t, "z-build", current.Name)

	app.Status.CurrentBuild = "a-build-that-was-pruned"
	current, err = srv.currentBuild(context.Background(), app)
	require.NoError(t, err)
	require.Equal(t, "z-build", current.Name, "a record of a build that is gone is not followed")
}

func TestABuildThatHasFinishedCannotBeCancelled(t *testing.T) {
	for _, phase := range []geassv1alpha1.GeassBuildPhase{geassv1alpha1.GeassBuildSucceeded, geassv1alpha1.GeassBuildFailed, geassv1alpha1.GeassBuildCancelled} {
		t.Run(string(phase), func(t *testing.T) {
			app := gitApp()
			srv, c, _ := pushServer(t, app, operatorBuild(app, "api-build-1", time.Hour, phase))

			rec := buildActionRequest(t, srv, "build/cancel")

			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Contains(t, rec.Body.String(), "already finished")
			got := buildOf(t, c, "api-build-1")
			require.Equal(t, phase, got.Status.Phase, "a finished build is left as it was")
			require.False(t, got.Status.CancelRequested)
		})
	}
	for _, phase := range []geassv1alpha1.GeassBuildPhase{"", geassv1alpha1.GeassBuildPending, geassv1alpha1.GeassBuildWaitingForCI, geassv1alpha1.GeassBuildRunning} {
		t.Run("in progress "+string(phase), func(t *testing.T) {
			app := gitApp()
			srv, c, _ := pushServer(t, app, operatorBuild(app, "api-build-1", time.Hour, phase))

			rec := buildActionRequest(t, srv, "build/cancel")

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.Equal(t, geassv1alpha1.GeassBuildCancelled, buildOf(t, c, "api-build-1").Status.Phase)
		})
	}
}

func TestAPushStillBuildsTheOtherServicesWhenOneFails(t *testing.T) {
	prod := gitApp()
	staging := gitApp(func(a *geassv1alpha1.GeassApp) { a.Name = "api-staging" })
	srv, c, _ := pushServer(t, prod, operatorBuild(prod, "api-build-1", time.Hour, geassv1alpha1.GeassBuildSucceeded))
	require.NoError(t, c.Create(context.Background(), staging))
	require.NoError(t, c.Create(context.Background(), operatorBuild(staging, "api-staging-build-1", time.Hour, geassv1alpha1.GeassBuildSucceeded)))
	kube := kubefake.NewClientset()
	kube.PrependReactor("delete", "jobs", func(action k8stesting.Action) (bool, runtime.Object, error) {
		if action.(k8stesting.DeleteAction).GetName() == "api-build-1-kaniko" {
			return true, nil, errors.New("the API server is busy")
		}
		return false, nil, nil
	})
	srv.Kube = kube

	triggered, err := srv.triggerWebhookBuilds(context.Background(), githubWebhookPayload{
		Repository: struct {
			FullName string `json:"full_name"`
		}{FullName: "org/api"},
		Ref: "refs/heads/main",
	})

	require.ErrorContains(t, err, "api: the API server is busy", "GitHub is told it failed")
	require.Equal(t, 1, triggered, "but the other service was rebuilt")
	require.Equal(t, geassv1alpha1.GeassBuildPending, buildOf(t, c, "api-staging-build-1").Status.Phase)
	require.Equal(t, geassv1alpha1.GeassBuildSucceeded, buildOf(t, c, "api-build-1").Status.Phase, "and the one whose Job could not be removed is left as it was")
}

func TestARetriggerLosingARaceWithTheControllerTriesAgain(t *testing.T) {
	app := gitApp()
	var conflicts atomic.Int32
	srv, c := serverIntercepting(t, interceptor.Funcs{
		SubResourceUpdate: func(ctx context.Context, c client.Client, sub string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			if _, isBuild := obj.(*geassv1alpha1.GeassBuild); isBuild && conflicts.Add(1) == 1 {
				return apierrors.NewConflict(schema.GroupResource{Group: "geass.geass.dev", Resource: "geassbuilds"}, obj.GetName(), nil)
			}
			return c.SubResource(sub).Update(ctx, obj, opts...)
		},
	}, app, operatorBuild(app, "api-build-1", time.Hour, geassv1alpha1.GeassBuildRunning))
	build := buildOf(t, c, "api-build-1")

	require.NoError(t, srv.retriggerBuild(context.Background(), &build))

	require.EqualValues(t, 2, conflicts.Load(), "it was refused once and made the second time")
	require.Equal(t, geassv1alpha1.GeassBuildPending, buildOf(t, c, "api-build-1").Status.Phase)
}
