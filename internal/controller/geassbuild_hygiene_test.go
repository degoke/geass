package controller

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// requestCounter is an HTTP transport that counts requests and fails them, so a test can show a pass
// never reached GitHub.
type requestCounter struct{ count atomic.Int32 }

func (c *requestCounter) RoundTrip(req *http.Request) (*http.Response, error) {
	c.count.Add(1)
	return nil, http.ErrHandlerTimeout
}

func buildTestReconciler(t *testing.T, objects ...client.Object) (*GeassBuildReconciler, client.Client, *requestCounter) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, batchv1.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).
		WithStatusSubresource(&geassv1alpha1.GeassBuild{}).Build()
	counter := &requestCounter{}
	return &GeassBuildReconciler{Client: c, Scheme: scheme, HTTPClient: &http.Client{Transport: counter}}, c, counter
}

func finishedBuild(name string, phase geassv1alpha1.GeassBuildPhase, mutate ...func(*geassv1alpha1.GeassBuild)) *geassv1alpha1.GeassBuild {
	started := metav1.NewTime(time.Now().Add(-time.Hour))
	build := &geassv1alpha1.GeassBuild{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace, Labels: map[string]string{platform.LabelApp: "api"}},
		Spec:       geassv1alpha1.GeassBuildSpec{App: "api", Project: "shop", Repository: "acme/api", Branch: "main", WaitForCI: true},
		Status: geassv1alpha1.GeassBuildStatus{
			Phase: phase, StartedAt: &started, JobRef: name + "-kaniko", Image: "registry/api:1",
			ImageDigest: "sha256:abc", LogRef: "pod-log://" + name + "-kaniko/kaniko",
		},
	}
	for _, m := range mutate {
		m(build)
	}
	return build
}

func reconcileBuild(t *testing.T, r *GeassBuildReconciler, name string) ctrl.Result {
	t.Helper()
	result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}})
	require.NoError(t, err)
	return result
}

func jobExists(t *testing.T, c client.Client, name string) bool {
	t.Helper()
	err := c.Get(context.Background(), client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, &batchv1.Job{})
	if apierrors.IsNotFound(err) {
		return false
	}
	require.NoError(t, err)
	return true
}

func TestASettledBuildIsNeverLookedAtAgain(t *testing.T) {
	r, c, requests := buildTestReconciler(t, finishedBuild("api-build-1", geassv1alpha1.GeassBuildSucceeded))

	for range 3 {
		require.Equal(t, ctrl.Result{}, reconcileBuild(t, r, "api-build-1"))
	}

	require.Zero(t, requests.count.Load(), "nothing is asked of GitHub about a build that is done")
	require.False(t, jobExists(t, c, "api-build-1-kaniko"), "and no job is made for it")
}

func TestABuildWhoseJobHasBeenCleanedUpIsNotBuiltAgain(t *testing.T) {
	for _, phase := range []geassv1alpha1.GeassBuildPhase{geassv1alpha1.GeassBuildSucceeded, geassv1alpha1.GeassBuildFailed} {
		t.Run(string(phase), func(t *testing.T) {
			// Its log is still in the pod log reference, so a succeeded build is not settled either.
			build := finishedBuild("api-build-1", phase, func(b *geassv1alpha1.GeassBuild) {
				b.Spec.LogStoreRef = &corev1.LocalObjectReference{Name: "logs"}
			})
			r, c, requests := buildTestReconciler(t, build)

			reconcileBuild(t, r, "api-build-1")

			require.Zero(t, requests.count.Load())
			require.False(t, jobExists(t, c, "api-build-1-kaniko"), "a finished build is not run again because the time to live removed its job")
		})
	}
}

func TestARetriggeredBuildStartsOverAndMakesAJob(t *testing.T) {
	build := finishedBuild("api-build-1", geassv1alpha1.GeassBuildPending, func(b *geassv1alpha1.GeassBuild) {
		b.Spec.Repository = "" // no GitHub round trip: this is about the job
		b.Spec.WaitForCI = false
		b.Status.StartedAt, b.Status.CompletedAt = nil, nil
		b.Status.ImageDigest = ""
	})
	r, c, _ := buildTestReconciler(t, build)

	reconcileBuild(t, r, "api-build-1")

	require.True(t, jobExists(t, c, "api-build-1-kaniko"), "a retrigger clears the start time, so the build runs")
	var stored geassv1alpha1.GeassBuild
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(build), &stored))
	require.Equal(t, geassv1alpha1.GeassBuildRunning, stored.Status.Phase)
	require.NotNil(t, stored.Status.StartedAt)
}

func TestKanikoJobsAreCleanedUpAfterAWhile(t *testing.T) {
	job := (&GeassBuildReconciler{}).jobFor(finishedBuild("api-build-1", geassv1alpha1.GeassBuildPending))

	require.NotNil(t, job.Spec.TTLSecondsAfterFinished)
	require.Equal(t, kanikoJobTTL, *job.Spec.TTLSecondsAfterFinished)
}

func TestBuildProgress(t *testing.T) {
	now := metav1.Now()
	for name, tt := range map[string]struct {
		build              geassv1alpha1.GeassBuildStatus
		wantStarted, wantR bool
	}{
		"never started":       {geassv1alpha1.GeassBuildStatus{Phase: geassv1alpha1.GeassBuildPending}, false, false},
		"failed before a job": {geassv1alpha1.GeassBuildStatus{Phase: geassv1alpha1.GeassBuildFailed}, false, false},
		"running":             {geassv1alpha1.GeassBuildStatus{Phase: geassv1alpha1.GeassBuildRunning, StartedAt: &now}, true, false},
		"succeeded":           {geassv1alpha1.GeassBuildStatus{Phase: geassv1alpha1.GeassBuildSucceeded, StartedAt: &now}, true, true},
		"failed after a job":  {geassv1alpha1.GeassBuildStatus{Phase: geassv1alpha1.GeassBuildFailed, StartedAt: &now}, true, true},
		"retriggered":         {geassv1alpha1.GeassBuildStatus{Phase: geassv1alpha1.GeassBuildPending, StartedAt: &now}, false, false},
	} {
		t.Run(name, func(t *testing.T) {
			started, ran := buildProgress(&geassv1alpha1.GeassBuild{Status: tt.build})
			require.Equal(t, tt.wantStarted, started)
			require.Equal(t, tt.wantR, ran)
		})
	}
}

func TestABuildIsSettledOnlyOnceItsLogIsSafe(t *testing.T) {
	require.True(t, buildSettled(finishedBuild("b", geassv1alpha1.GeassBuildSucceeded)), "no log store, so nothing left to archive")
	archived := finishedBuild("b", geassv1alpha1.GeassBuildSucceeded, func(b *geassv1alpha1.GeassBuild) {
		b.Spec.LogStoreRef = &corev1.LocalObjectReference{Name: "logs"}
		b.Status.LogRef = "s3://logs/b.log"
	})
	require.True(t, buildSettled(archived))
	waiting := finishedBuild("b", geassv1alpha1.GeassBuildSucceeded, func(b *geassv1alpha1.GeassBuild) {
		b.Spec.LogStoreRef = &corev1.LocalObjectReference{Name: "logs"}
	})
	require.False(t, buildSettled(waiting), "the log still has to be copied out of the pod")
	require.False(t, buildSettled(finishedBuild("b", geassv1alpha1.GeassBuildFailed)))
	require.False(t, buildSettled(finishedBuild("b", geassv1alpha1.GeassBuildSucceeded, func(b *geassv1alpha1.GeassBuild) { b.Status.ImageDigest = "" })))
}

func TestOldBuildsArePrunedButNeverTheOnesInUse(t *testing.T) {
	builds := make([]geassv1alpha1.GeassBuild, 0, 14)
	for i := range 14 {
		build := finishedBuild("api-build-"+string(rune('a'+i)), geassv1alpha1.GeassBuildSucceeded)
		build.CreationTimestamp = metav1.NewTime(time.Date(2026, 1, 1, i, 0, 0, 0, time.UTC))
		builds = append(builds, *build)
	}
	running := finishedBuild("api-build-running", geassv1alpha1.GeassBuildRunning)
	running.CreationTimestamp = metav1.NewTime(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	builds = append(builds, *running)

	// "a" is the oldest. "b" is the one the app runs, and "c" is the one that just finished.
	doomed := buildsToPrune(builds, "api-build-c", "api-build-b", 10)

	require.ElementsMatch(t, []string{"api-build-a", "api-build-d"}, doomed, "the ten newest are kept; of the four older, b and c are in use, so a and d go, and the running build is never touched")
}

func TestPruningDeletesWhatIsSelected(t *testing.T) {
	objects := make([]client.Object, 0, 13)
	for i := range 12 {
		build := finishedBuild("api-build-"+string(rune('a'+i)), geassv1alpha1.GeassBuildSucceeded)
		build.CreationTimestamp = metav1.NewTime(time.Date(2026, 1, 1, i, 0, 0, 0, time.UTC))
		objects = append(objects, build)
	}
	objects = append(objects, &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: platform.SystemNamespace},
		Status:     geassv1alpha1.GeassAppStatus{ActiveBuild: "api-build-a"},
	})
	r, c, _ := buildTestReconciler(t, objects...)
	newest := finishedBuild("api-build-l", geassv1alpha1.GeassBuildSucceeded)

	r.pruneSupersededBuilds(context.Background(), newest)

	var left geassv1alpha1.GeassBuildList
	require.NoError(t, c.List(context.Background(), &left, client.InNamespace(platform.SystemNamespace)))
	names := make([]string, 0, len(left.Items))
	for _, build := range left.Items {
		names = append(names, build.Name)
	}
	require.NotContains(t, names, "api-build-b", "the second oldest goes")
	require.Contains(t, names, "api-build-a", "the oldest stays because the app runs it")
	require.Len(t, names, 11)
}
