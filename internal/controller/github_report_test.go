package controller

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	networkingv1 "k8s.io/api/networking/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/githubapp"
	"github.com/degoke/geass/pkg/platform"
)

const (
	reportSHA       = "9f3c2a1b8d7e6f504a3b2c1d0e9f8a7b6c5d4e3f"
	reportNextSHA   = "1234567890abcdef1234567890abcdef12345678"
	reportDashboard = "https://geass.example.com"
)

// seenGitHubRequest is one request that reached the fake GitHub.
type seenGitHubRequest struct {
	Method string
	Path   string
	Auth   string
	Body   map[string]any
}

// fakeGitHubAPI is a GitHub that records what it is asked and answers from a table. What is not in the table is
// a 404, which is how GitHub answers for what a token may not see.
type fakeGitHubAPI struct {
	server *httptest.Server

	mu       sync.Mutex
	requests []seenGitHubRequest
	answers  map[string]func(body map[string]any) (int, any)
}

func newFakeGitHubAPI(t *testing.T) *fakeGitHubAPI {
	t.Helper()
	g := &fakeGitHubAPI{answers: map[string]func(map[string]any) (int, any){}}
	g.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &body)
		}
		g.mu.Lock()
		g.requests = append(g.requests, seenGitHubRequest{Method: r.Method, Path: r.URL.Path, Auth: r.Header.Get("Authorization"), Body: body})
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

func (g *fakeGitHubAPI) answer(method, path string, code int, payload any) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.answers[method+" "+path] = func(map[string]any) (int, any) { return code, payload }
}

func (g *fakeGitHubAPI) forbid(method, path string) {
	g.answer(method, path, http.StatusForbidden, map[string]string{"message": "Resource not accessible by integration"})
}

func (g *fakeGitHubAPI) all() []seenGitHubRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]seenGitHubRequest(nil), g.requests...)
}

// to is every request made to a path, with its method.
func (g *fakeGitHubAPI) to(method, path string) []seenGitHubRequest {
	var out []seenGitHubRequest
	for _, request := range g.all() {
		if request.Method == method && request.Path == path {
			out = append(out, request)
		}
	}
	return out
}

func (g *fakeGitHubAPI) reset() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.requests = nil
}

// client is an HTTP client whose requests for api.github.com go to this fake.
func (g *fakeGitHubAPI) client(t *testing.T) *http.Client {
	t.Helper()
	target, err := url.Parse(g.server.URL)
	require.NoError(t, err)
	return &http.Client{Transport: toFakeGitHub{target: target}}
}

type toFakeGitHub struct{ target *url.URL }

func (f toFakeGitHub) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme, req.URL.Host = f.target.Scheme, f.target.Host
	return http.DefaultTransport.RoundTrip(req)
}

// reportClock is a time that moves when the test says so.
type reportClock struct{ now time.Time }

func (c *reportClock) Now() time.Time          { return c.now }
func (c *reportClock) advance(d time.Duration) { c.now = c.now.Add(d) }

func newReportGuard() (*githubapp.Guard, *reportClock) {
	clock := &reportClock{now: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)}
	return &githubapp.Guard{Now: clock.Now}, clock
}

// githubWorld is a cluster with a project that is connected to GitHub, a fake GitHub, and a client to the cluster. The
// tests add the builds and services they are about.
type githubWorld struct {
	gh     *fakeGitHubAPI
	client client.Client
	scheme *runtime.Scheme
	guard  *githubapp.Guard
	clock  *reportClock
}

type worldOptions struct {
	reportingOff bool
	objects      []client.Object
}

func newGitHubWorld(t *testing.T, opts worldOptions) *githubWorld {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	require.NoError(t, networkingv1.AddToScheme(scheme))
	require.NoError(t, autoscalingv2.AddToScheme(scheme))
	require.NoError(t, batchv1.AddToScheme(scheme))
	require.NoError(t, monitoringv1.AddToScheme(scheme))
	ready := []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue}}
	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: "shop", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassProjectSpec{
			ClusterRef: corev1.LocalObjectReference{Name: "default"}, Environments: []string{"production", "pr-12"},
			GitHubConnectionRef: &corev1.LocalObjectReference{Name: "shop-github"},
		},
		Status: geassv1alpha1.GeassProjectStatus{Conditions: ready},
	}
	if opts.reportingOff {
		off := false
		project.Spec.GitHubReporting = &off
	}
	objects := []client.Object{
		project,
		&geassv1alpha1.GeassCluster{ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: platform.SystemNamespace}, Status: geassv1alpha1.GeassClusterStatus{Conditions: ready}},
		&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "worker"},
			Status: corev1.NodeStatus{
				Allocatable: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("8"), corev1.ResourceMemory: resource.MustParse("16Gi")},
				Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}},
			},
		},
		&geassv1alpha1.GeassGitHubConnection{
			ObjectMeta: metav1.ObjectMeta{Name: "shop-github", Namespace: platform.SystemNamespace},
			Spec:       geassv1alpha1.GeassGitHubConnectionSpec{SecretRef: corev1.LocalObjectReference{Name: "shop-github-token"}},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "shop-github-token", Namespace: platform.SystemNamespace},
			Data:       map[string][]byte{"token": []byte("tok")},
		},
		&geassv1alpha1.GeassPlatformConfig{
			ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
			Spec:       geassv1alpha1.GeassPlatformConfigSpec{DashboardURL: reportDashboard},
		},
	}
	guard, clock := newReportGuard()
	return &githubWorld{
		gh: newFakeGitHubAPI(t), scheme: scheme, guard: guard, clock: clock,
		client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(slices.Concat(objects, opts.objects)...).
			WithStatusSubresource(&geassv1alpha1.GeassApp{}, &geassv1alpha1.GeassBuild{}, &geassv1alpha1.GeassGitHubConnection{}, &geassv1alpha1.GeassProject{}, &geassv1alpha1.GeassDeployment{}).Build(),
	}
}

func (w *githubWorld) buildReconciler(t *testing.T) *GeassBuildReconciler {
	t.Helper()
	return &GeassBuildReconciler{Client: w.client, Scheme: w.scheme, HTTPClient: w.gh.client(t), GitHubGuard: w.guard}
}

func (w *githubWorld) appReconciler(t *testing.T) *GeassAppReconciler {
	t.Helper()
	return &GeassAppReconciler{Client: w.client, Scheme: w.scheme, HTTPClient: w.gh.client(t), GitHubGuard: w.guard}
}

// connectionCondition is what the GitHub connection says about reporting.
func (w *githubWorld) connectionCondition(t *testing.T) *metav1.Condition {
	t.Helper()
	var connection geassv1alpha1.GeassGitHubConnection
	require.NoError(t, w.client.Get(context.Background(), client.ObjectKey{Name: "shop-github", Namespace: platform.SystemNamespace}, &connection))
	for i := range connection.Status.Conditions {
		if connection.Status.Conditions[i].Type == githubapp.ConditionReporting {
			return &connection.Status.Conditions[i]
		}
	}
	return nil
}

func reportBuildNamed(name string, mutate ...func(*geassv1alpha1.GeassBuild)) *geassv1alpha1.GeassBuild {
	build := &geassv1alpha1.GeassBuild{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace, Labels: map[string]string{platform.LabelApp: "web"}},
		Spec: geassv1alpha1.GeassBuildSpec{
			App: "web", Project: "shop", Environment: "production", Repository: "acme/web", Branch: "main", Revision: reportSHA,
			Registry: "registry.example.com/shop/web", ConnectionRef: &corev1.LocalObjectReference{Name: "shop-github"},
		},
	}
	for _, m := range mutate {
		m(build)
	}
	return build
}

func reconcileReportBuild(t *testing.T, r *GeassBuildReconciler, name string) ctrl.Result {
	t.Helper()
	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}})
	require.NoError(t, err)
	return result
}

func statusesTo(gh *fakeGitHubAPI) []seenGitHubRequest {
	return gh.to(http.MethodPost, "/repos/acme/web/statuses/"+reportSHA)
}

func okStatuses(gh *fakeGitHubAPI) {
	gh.answer(http.MethodPost, "/repos/acme/web/statuses/"+reportSHA, http.StatusCreated, map[string]any{"id": 1})
}

// finishJob plays the Job controller, which a fake client has none of: the build's Job succeeded and its pod says
// what it pushed.
func finishJob(t *testing.T, w *githubWorld, r *GeassBuildReconciler, build string) {
	t.Helper()
	ctx := context.Background()
	var job batchv1.Job
	require.NoError(t, w.client.Get(ctx, client.ObjectKey{Name: build + "-kaniko", Namespace: platform.SystemNamespace}, &job))
	job.UID = "job-uid"
	require.NoError(t, w.client.Update(ctx, &job))
	job.Status.Succeeded = 1
	require.NoError(t, w.client.Status().Update(ctx, &job))
	r.Kube = kubeServing(t, []corev1.Pod{kanikoPod("pod", build+"-kaniko", "job-uid", corev1.PodSucceeded, 0, digestOne)}, nil)
}

func TestABuildIsReportedToGitHubAsACommitStatusAsItGoes(t *testing.T) {
	w := newGitHubWorld(t, worldOptions{objects: []client.Object{reportBuildNamed("web-build-1")}})
	okStatuses(w.gh)
	r := w.buildReconciler(t)

	reconcileReportBuild(t, r, "web-build-1") // no CI to wait for: the Job is made and the build is running
	running := statusesTo(w.gh)
	require.Len(t, running, 1)
	require.Equal(t, "Bearer tok", running[0].Auth)
	require.Equal(t, map[string]any{
		"state": "pending", "context": "geass/build/web", "description": "Building the image",
		"target_url": reportDashboard + "/projects/shop/apps/web?environment=production&view=deployments",
	}, running[0].Body)

	finishJob(t, w, r, "web-build-1")
	reconcileReportBuild(t, r, "web-build-1")
	statuses := statusesTo(w.gh)
	require.Len(t, statuses, 2)
	require.Equal(t, "success", statuses[1].Body["state"])
	require.Equal(t, "geass/build/web", statuses[1].Body["context"])

	// Looked at again and again, the build says nothing more.
	for range 4 {
		reconcileReportBuild(t, r, "web-build-1")
	}
	require.Len(t, w.gh.all(), 2, "a state is reported once")
	var stored geassv1alpha1.GeassBuild
	require.NoError(t, w.client.Get(context.Background(), client.ObjectKey{Name: "web-build-1", Namespace: platform.SystemNamespace}, &stored))
	require.Equal(t, reportSHA+":Succeeded", stored.Annotations[platform.BuildGitHubStatusAnnotation])
	require.Equal(t, geassv1alpha1.GeassBuildSucceeded, stored.Status.Phase)
}

func TestABuildThatJobFailedIsAFailureOnTheCommit(t *testing.T) {
	w := newGitHubWorld(t, worldOptions{objects: []client.Object{reportBuildNamed("web-build-1")}})
	okStatuses(w.gh)
	r := w.buildReconciler(t)
	reconcileReportBuild(t, r, "web-build-1")

	var job batchv1.Job
	require.NoError(t, w.client.Get(context.Background(), client.ObjectKey{Name: "web-build-1-kaniko", Namespace: platform.SystemNamespace}, &job))
	job.Status.Failed = 3
	require.NoError(t, w.client.Status().Update(context.Background(), &job))
	reconcileReportBuild(t, r, "web-build-1")

	statuses := statusesTo(w.gh)
	require.Len(t, statuses, 2)
	require.Equal(t, "failure", statuses[1].Body["state"], "the code did not build")
	require.Equal(t, "The build failed", statuses[1].Body["description"])
}

func TestWhatEachBuildStateLooksLikeOnTheCommit(t *testing.T) {
	status := func(phase geassv1alpha1.GeassBuildPhase, mutate ...func(*geassv1alpha1.GeassBuild)) *geassv1alpha1.GeassBuild {
		return reportBuildNamed("b", append([]func(*geassv1alpha1.GeassBuild){func(b *geassv1alpha1.GeassBuild) {
			b.Status.Phase, b.Status.SourceRevision = phase, reportSHA
		}}, mutate...)...)
	}
	for name, tt := range map[string]struct {
		build       *geassv1alpha1.GeassBuild
		state       string
		description string
		ok          bool
	}{
		"waiting for CI":         {status(geassv1alpha1.GeassBuildWaitingForCI), "pending", "Waiting for CI checks to pass before building", true},
		"running":                {status(geassv1alpha1.GeassBuildRunning), "pending", "Building the image", true},
		"built":                  {status(geassv1alpha1.GeassBuildSucceeded, func(b *geassv1alpha1.GeassBuild) { b.Status.ImageDigest = digestOne }), "success", "Built the image", true},
		"built without a digest": {status(geassv1alpha1.GeassBuildSucceeded), "", "", false},
		"the Job failed":         {status(geassv1alpha1.GeassBuildFailed, func(b *geassv1alpha1.GeassBuild) { b.Status.FailureReason = kanikoJobFailedReason }), "failure", "The build failed", true},
		"CI failed": {status(geassv1alpha1.GeassBuildFailed, func(b *geassv1alpha1.GeassBuild) {
			b.Status.CIState, b.Status.FailureReason = "failure", `GitHub check "tests" concluded failure`
		}), "failure", `GitHub check "tests" concluded failure`, true},
		"no registry to push to": {status(geassv1alpha1.GeassBuildFailed, func(b *geassv1alpha1.GeassBuild) { b.Status.FailureReason = noRegistryMessage }), "error", noRegistryMessage, true},
		"failed with no reason":  {status(geassv1alpha1.GeassBuildFailed), "error", "The build could not run", true},
		"cancelled":              {status(geassv1alpha1.GeassBuildCancelled), "error", "The build was cancelled", true},
		"not started":            {status(geassv1alpha1.GeassBuildPending), "", "", false},
		"commit not known yet":   {reportBuildNamed("b", func(b *geassv1alpha1.GeassBuild) { b.Status.Phase = geassv1alpha1.GeassBuildRunning }), "", "", false},
		"no repository":          {status(geassv1alpha1.GeassBuildRunning, func(b *geassv1alpha1.GeassBuild) { b.Spec.Repository = "" }), "", "", false},
	} {
		t.Run(name, func(t *testing.T) {
			state, description, ok := buildCommitStatus(tt.build)
			require.Equal(t, tt.ok, ok)
			require.Equal(t, tt.state, state)
			require.Equal(t, tt.description, description)
		})
	}
}

func TestAMissingPermissionNeverFailsTheBuild(t *testing.T) {
	w := newGitHubWorld(t, worldOptions{objects: []client.Object{reportBuildNamed("web-build-1")}})
	// An app registered before Geass reported: it has no Commit statuses permission.
	w.gh.forbid(http.MethodPost, "/repos/acme/web/statuses/"+reportSHA)
	r := w.buildReconciler(t)

	reconcileReportBuild(t, r, "web-build-1")
	finishJob(t, w, r, "web-build-1")
	reconcileReportBuild(t, r, "web-build-1")
	for range 3 {
		reconcileReportBuild(t, r, "web-build-1")
	}

	var stored geassv1alpha1.GeassBuild
	require.NoError(t, w.client.Get(context.Background(), client.ObjectKey{Name: "web-build-1", Namespace: platform.SystemNamespace}, &stored))
	require.Equal(t, geassv1alpha1.GeassBuildSucceeded, stored.Status.Phase, "the build went on as if there were nobody to tell")
	require.Empty(t, stored.Status.FailureReason)
	require.Len(t, statusesTo(w.gh), 1, "GitHub was asked once, and not again for the next state or the next reconcile")
	condition := w.connectionCondition(t)
	require.NotNil(t, condition, "the owner can see it on the connection")
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, githubapp.ReasonPermissionMissing, condition.Reason)
	require.Contains(t, condition.Message, "Commit statuses")
	require.Contains(t, condition.Message, "Accept new permissions")
}

func TestAProjectThatTurnedReportingOffIsNeverReportedTo(t *testing.T) {
	w := newGitHubWorld(t, worldOptions{reportingOff: true, objects: []client.Object{reportBuildNamed("web-build-1")}})
	okStatuses(w.gh)
	r := w.buildReconciler(t)

	reconcileReportBuild(t, r, "web-build-1")
	finishJob(t, w, r, "web-build-1")
	reconcileReportBuild(t, r, "web-build-1")

	var stored geassv1alpha1.GeassBuild
	require.NoError(t, w.client.Get(context.Background(), client.ObjectKey{Name: "web-build-1", Namespace: platform.SystemNamespace}, &stored))
	require.Equal(t, geassv1alpha1.GeassBuildSucceeded, stored.Status.Phase)
	require.Empty(t, w.gh.all(), "not a request")
	require.Empty(t, stored.Annotations[platform.BuildGitHubStatusAnnotation])
}

func TestBuildsMadeBeforeReportingExistedAreNotReportedWhenTheOperatorStarts(t *testing.T) {
	old := []client.Object{
		reportBuildNamed("old-ok", func(b *geassv1alpha1.GeassBuild) {
			b.Status = geassv1alpha1.GeassBuildStatus{Phase: geassv1alpha1.GeassBuildSucceeded, SourceRevision: reportSHA, ImageDigest: digestOne, StartedAt: ptr(metav1.Now())}
		}),
		reportBuildNamed("old-failed", func(b *geassv1alpha1.GeassBuild) {
			b.Status = geassv1alpha1.GeassBuildStatus{Phase: geassv1alpha1.GeassBuildFailed, SourceRevision: reportSHA, StartedAt: ptr(metav1.Now()), FailureReason: kanikoJobFailedReason}
		}),
		reportBuildNamed("old-archiving", func(b *geassv1alpha1.GeassBuild) {
			b.Spec.LogStoreRef = &corev1.LocalObjectReference{Name: "logs"}
			b.Status = geassv1alpha1.GeassBuildStatus{Phase: geassv1alpha1.GeassBuildSucceeded, SourceRevision: reportSHA, ImageDigest: digestOne, StartedAt: ptr(metav1.Now()), LogRef: "pod-log://x/kaniko"}
		}),
	}
	w := newGitHubWorld(t, worldOptions{objects: old})
	okStatuses(w.gh)
	r := w.buildReconciler(t)

	for _, name := range []string{"old-ok", "old-failed", "old-archiving"} {
		for range 2 {
			reconcileReportBuild(t, r, name)
		}
	}

	require.Empty(t, w.gh.all(), "starting the operator does not announce every build there ever was")
}

func TestABuildThatGitHubCouldNotBeToldAboutIsToldAgainLater(t *testing.T) {
	w := newGitHubWorld(t, worldOptions{objects: []client.Object{reportBuildNamed("web-build-1")}})
	w.gh.answer(http.MethodPost, "/repos/acme/web/statuses/"+reportSHA, http.StatusBadGateway, map[string]string{"message": "Bad Gateway"})
	r := w.buildReconciler(t)

	first := reconcileReportBuild(t, r, "web-build-1")
	require.Equal(t, githubapp.RetrySpacing, first.RequeueAfter, "the pass asks to be run again for the report, which it would not for the build")
	require.Len(t, statusesTo(w.gh), 1)

	// GitHub is back. The build is looked at again after the gap and the state that did not get through is sent.
	okStatuses(w.gh)
	w.clock.advance(githubapp.RetrySpacing)
	reconcileReportBuild(t, r, "web-build-1")

	statuses := statusesTo(w.gh)
	require.Len(t, statuses, 2)
	require.Equal(t, "pending", statuses[1].Body["state"])
	var stored geassv1alpha1.GeassBuild
	require.NoError(t, w.client.Get(context.Background(), client.ObjectKey{Name: "web-build-1", Namespace: platform.SystemNamespace}, &stored))
	require.Equal(t, reportSHA+":Running", stored.Annotations[platform.BuildGitHubStatusAnnotation])
	condition := w.connectionCondition(t)
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionTrue, condition.Status, "an outage is not a missing permission, and once GitHub took a report the connection says so")
}

// A team that requires the status under branch protection must not find, the day a second service is added to the
// repository, that the name it requires is never sent again.
func TestTheNameOfAStatusDoesNotChangeWhenAnotherServiceIsAdded(t *testing.T) {
	service := func(name string) *geassv1alpha1.GeassApp {
		return &geassv1alpha1.GeassApp{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace},
			Spec: geassv1alpha1.GeassAppSpec{
				Project: "shop", Environment: "production",
				Source: geassv1alpha1.GeassAppSource{Git: &geassv1alpha1.GeassAppGitSource{Repository: "acme/web", Branch: "main", ConnectionRef: corev1.LocalObjectReference{Name: "shop-github"}}},
			},
		}
	}
	alone := newGitHubWorld(t, worldOptions{objects: []client.Object{service("web")}})
	before := (&GeassBuildReconciler{Client: alone.client}).buildStatusContext(context.Background(), reportBuildNamed("b"))

	shared := newGitHubWorld(t, worldOptions{objects: []client.Object{service("web"), service("worker")}})
	r := &GeassBuildReconciler{Client: shared.client}
	after := r.buildStatusContext(context.Background(), reportBuildNamed("b"))

	require.Equal(t, "geass/build/web", before)
	require.Equal(t, before, after, "adding a second service to the repository does not rename the first one's status")
	worker := reportBuildNamed("b", func(b *geassv1alpha1.GeassBuild) { b.Spec.App = "worker" })
	require.Equal(t, "geass/build/worker", r.buildStatusContext(context.Background(), worker),
		"and each service has its own, so a failure of one is not hidden behind the success of the other")
}

func TestTheStatusNameUsesTheNameTheDashboardShowsAndFallsBackToTheServiceID(t *testing.T) {
	named := &geassv1alpha1.GeassApp{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: platform.SystemNamespace,
		Annotations: map[string]string{"geass.dev/display-name": "storefront"}}}
	world := newGitHubWorld(t, worldOptions{objects: []client.Object{named}})

	require.Equal(t, "geass/build/storefront", (&GeassBuildReconciler{Client: world.client}).buildStatusContext(context.Background(), reportBuildNamed("b")))

	empty := newGitHubWorld(t, worldOptions{})
	require.Equal(t, "geass/build/web", (&GeassBuildReconciler{Client: empty.client}).buildStatusContext(context.Background(), reportBuildNamed("b")),
		"a service that cannot be read is still named, by its ID")
}

func TestGithubRequestCanSendABodyAndKeepsReadingAsBefore(t *testing.T) {
	gh := newFakeGitHubAPI(t)
	gh.answer(http.MethodGet, "/repos/acme/web/commits/main", http.StatusOK, map[string]string{"sha": reportSHA})
	gh.answer(http.MethodPost, "/repos/acme/web/statuses/"+reportSHA, http.StatusCreated, map[string]string{"id": "1"})
	gh.forbid(http.MethodGet, "/repos/acme/web/commits/"+reportSHA+"/check-runs")
	r := &GeassBuildReconciler{HTTPClient: gh.client(t)}
	build := reportBuildNamed("b")

	data, err := r.githubRequest(context.Background(), "tok", build, http.MethodGet, "/commits/main", nil)
	require.NoError(t, err)
	require.Contains(t, string(data), reportSHA)

	_, err = r.githubRequest(context.Background(), "tok", build, http.MethodPost, "/statuses/"+reportSHA, map[string]string{"state": "success"})
	require.NoError(t, err)
	posted := gh.to(http.MethodPost, "/repos/acme/web/statuses/"+reportSHA)
	require.Len(t, posted, 1)
	require.Equal(t, map[string]any{"state": "success"}, posted[0].Body, "a body is sent as JSON")

	_, err = r.githubRequest(context.Background(), "tok", build, http.MethodGet, "/commits/"+reportSHA+"/check-runs", nil)
	var refused *githubAPIError
	require.ErrorAs(t, err, &refused)
	require.Equal(t, http.StatusForbidden, refused.code, "the code of a refusal is still there to read")
	require.True(t, strings.HasPrefix(err.Error(), "GitHub API returned 403"), "and what a failed build says is unchanged: %s", err)
}

// A report GitHub refused is owed, not done: once the owner approves the permission the build's state is sent.
func TestABuildStateGitHubRefusedIsSentOnceThePermissionIsApproved(t *testing.T) {
	w := newGitHubWorld(t, worldOptions{objects: []client.Object{reportBuildNamed("web-build-1")}})
	w.gh.forbid(http.MethodPost, "/repos/acme/web/statuses/"+reportSHA)
	r := w.buildReconciler(t)
	reconcileReportBuild(t, r, "web-build-1")
	finishJob(t, w, r, "web-build-1")
	reconcileReportBuild(t, r, "web-build-1")
	require.Len(t, statusesTo(w.gh), 1, "refused once, then held back")
	var stored geassv1alpha1.GeassBuild
	require.NoError(t, w.client.Get(context.Background(), client.ObjectKey{Name: "web-build-1", Namespace: platform.SystemNamespace}, &stored))
	require.Equal(t, "owed", stored.Annotations[platform.BuildGitHubStatusAnnotation], "it is on record as owing a status, not as having sent one")

	okStatuses(w.gh)
	w.clock.advance(githubapp.RefusedBackoff + time.Second)
	reconcileReportBuild(t, r, "web-build-1")

	statuses := statusesTo(w.gh)
	require.Len(t, statuses, 2)
	require.Equal(t, "success", statuses[1].Body["state"], "the state the build is in now")
	require.NoError(t, w.client.Get(context.Background(), client.ObjectKey{Name: "web-build-1", Namespace: platform.SystemNamespace}, &stored))
	require.Equal(t, reportSHA+":Succeeded", stored.Annotations[platform.BuildGitHubStatusAnnotation])
}
