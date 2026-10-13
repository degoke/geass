package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const (
	digestOne = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	digestTwo = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
)

// kubeServing is a Kubernetes API that knows some pods and their logs, enough for what a build reads
// from the pods of its Job: the list of them by label, and a container's log.
func kubeServing(t *testing.T, pods []corev1.Pod, logs map[string]string) kubernetes.Interface {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/log"):
			parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
			text, ok := logs[parts[len(parts)-2]]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(text))
		case strings.HasSuffix(r.URL.Path, "/pods"):
			selector := r.URL.Query().Get("labelSelector")
			list := corev1.PodList{TypeMeta: metav1.TypeMeta{Kind: "PodList", APIVersion: "v1"}}
			for _, pod := range pods {
				if key, value, ok := strings.Cut(selector, "="); !ok || pod.Labels[key] == value {
					list.Items = append(list.Items, pod)
				}
			}
			w.Header().Set("Content-Type", "application/json")
			// assert, not require: a handler runs on the server's goroutine, where a test cannot be stopped.
			assert.NoError(t, json.NewEncoder(w).Encode(list))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	kube, err := kubernetes.NewForConfig(&rest.Config{Host: server.URL})
	require.NoError(t, err)
	return kube
}

// kanikoPod is a pod of a build's Job after Kaniko ran: the phase it ended in, and what the kaniko container
// left as its termination message.
func kanikoPod(name, job string, owner types.UID, phase corev1.PodPhase, exitCode int32, message string) corev1.Pod {
	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace, Labels: map[string]string{"job-name": job}},
		Status: corev1.PodStatus{Phase: phase, ContainerStatuses: []corev1.ContainerStatus{{
			Name: "kaniko", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: exitCode, Message: message}},
		}}},
	}
	if owner != "" {
		pod.OwnerReferences = []metav1.OwnerReference{{APIVersion: "batch/v1", Kind: "Job", Name: job, UID: owner, Controller: new(true)}}
	}
	return pod
}

func deploymentPodOf(t *testing.T, c client.Client, name, namespace string) corev1.PodSpec {
	t.Helper()
	var deploy appsv1.Deployment
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: name, Namespace: namespace}, &deploy))
	return deploy.Spec.Template.Spec
}

func buildJobNamed(name string, uid types.UID, mutate ...func(*batchv1.Job)) *batchv1.Job {
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace, UID: uid}}
	for _, m := range mutate {
		m(job)
	}
	return job
}

func succeeded(job *batchv1.Job) { job.Status.Succeeded = 1 }

func storedBuild(t *testing.T, c client.Client, name string) geassv1alpha1.GeassBuild {
	t.Helper()
	var build geassv1alpha1.GeassBuild
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, &build))
	return build
}

func TestTheDigestComesFromThePodThatBuiltTheImage(t *testing.T) {
	const job = "api-build-1-kaniko"
	build := finishedBuild("api-build-1", geassv1alpha1.GeassBuildRunning, func(b *geassv1alpha1.GeassBuild) { b.Status.ImageDigest = "" })
	for name, tt := range map[string]struct {
		pods []corev1.Pod
		logs map[string]string
		want string
	}{
		"the termination message Kaniko writes": {
			pods: []corev1.Pod{kanikoPod("p1", job, "job-uid", corev1.PodSucceeded, 0, digestOne)},
			want: digestOne,
		},
		"a newline after it is not part of it": {
			pods: []corev1.Pod{kanikoPod("p1", job, "job-uid", corev1.PodSucceeded, 0, digestOne+"\n")},
			want: digestOne,
		},
		"a pod that failed and pushed nothing is not read": {
			pods: []corev1.Pod{
				kanikoPod("p1", job, "job-uid", corev1.PodFailed, 1, digestTwo),
				kanikoPod("p2", job, "job-uid", corev1.PodSucceeded, 0, digestOne),
			},
			want: digestOne,
		},
		"a pod of an earlier Job of the same name is not read": {
			pods: []corev1.Pod{
				kanikoPod("old", job, "earlier-job-uid", corev1.PodSucceeded, 0, digestTwo),
				kanikoPod("new", job, "job-uid", corev1.PodSucceeded, 0, digestOne),
			},
			want: digestOne,
		},
		"the log of a Job made before Kaniko was asked for the file": {
			pods: []corev1.Pod{kanikoPod("p1", job, "job-uid", corev1.PodSucceeded, 0, "")},
			logs: map[string]string{"p1": "INFO[0003] Using cached layer " + digestTwo + "\nINFO[0010] Pushing image to registry/api:1\nINFO[0012] Pushed registry/api@" + digestOne + "\n"},
			want: digestOne,
		},
		"a layer in the log is not the image": {
			pods: []corev1.Pod{kanikoPod("p1", job, "job-uid", corev1.PodSucceeded, 0, "")},
			logs: map[string]string{"p1": "INFO[0003] Using cached layer " + digestTwo + "\n"},
			want: "",
		},
		"a termination message that is not a digest": {
			pods: []corev1.Pod{kanikoPod("p1", job, "job-uid", corev1.PodSucceeded, 0, "error: boom")},
			logs: map[string]string{"p1": ""},
			want: "",
		},
		"no pods": {want: ""},
	} {
		t.Run(name, func(t *testing.T) {
			r := &GeassBuildReconciler{Kube: kubeServing(t, tt.pods, tt.logs)}

			require.Equal(t, tt.want, r.digestFromBuildPod(context.Background(), build, buildJobNamed(job, "job-uid")))
		})
	}
}

func TestARerunBuildRecordsTheDigestOfItsOwnRun(t *testing.T) {
	ctx := context.Background()
	const jobName = "api-build-1-kaniko"
	// The build ran before and recorded digestOne. A push resets it, as the webhook does.
	build := finishedBuild("api-build-1", geassv1alpha1.GeassBuildSucceeded, func(b *geassv1alpha1.GeassBuild) {
		b.Spec.Repository, b.Spec.WaitForCI = "", false
		b.Status.ImageDigest = digestOne
	})
	r, c, _ := buildTestReconciler(t, build)
	var stored geassv1alpha1.GeassBuild
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(build), &stored))
	platform.ResetBuildStatus(&stored.Status)
	require.NoError(t, c.Status().Update(ctx, &stored))
	require.Empty(t, storedBuild(t, c, "api-build-1").Status.ImageDigest, "the reset forgets the digest of the last run")

	reconcileBuild(t, r, "api-build-1")
	require.True(t, jobExists(t, c, jobName), "the build runs again")
	require.Equal(t, geassv1alpha1.GeassBuildRunning, storedBuild(t, c, "api-build-1").Status.Phase)

	// The new Job finishes, and its pod says what it pushed this time.
	var job batchv1.Job
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: jobName, Namespace: platform.SystemNamespace}, &job))
	job.UID = "second-job"
	require.NoError(t, c.Update(ctx, &job))
	job.Status.Succeeded = 1
	require.NoError(t, c.Status().Update(ctx, &job))
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(&job), &job))
	require.EqualValues(t, "second-job", job.UID)
	require.EqualValues(t, 1, job.Status.Succeeded)
	r.Kube = kubeServing(t, []corev1.Pod{
		kanikoPod("first-run", jobName, "first-job", corev1.PodSucceeded, 0, digestOne),
		kanikoPod("second-run", jobName, "second-job", corev1.PodSucceeded, 0, digestTwo),
	}, nil)
	reconcileBuild(t, r, "api-build-1")

	done := storedBuild(t, c, "api-build-1")
	require.Equal(t, geassv1alpha1.GeassBuildSucceeded, done.Status.Phase)
	require.Equal(t, digestTwo, done.Status.ImageDigest, "the new digest is recorded, and the old one is not kept in its place")
}

// Whoever reset the build may have left the old digest in place (a reset by hand, or by an older dashboard).
// The controller does not rely on it being cleared: a run that has not ended is not Succeeded, so the digest
// recorded then is not trusted, and the one its pod reports replaces it.
func TestARerunBuildThatKeptTheOldDigestStillRecordsTheNewOne(t *testing.T) {
	build := finishedBuild("api-build-1", geassv1alpha1.GeassBuildRunning, func(b *geassv1alpha1.GeassBuild) {
		b.Spec.Repository, b.Spec.WaitForCI = "", false
		b.Status.ImageDigest = digestOne
	})
	r, c, _ := buildTestReconciler(t, build, buildJobNamed("api-build-1-kaniko", "second-job", succeeded))
	r.Kube = kubeServing(t, []corev1.Pod{kanikoPod("second-run", "api-build-1-kaniko", "second-job", corev1.PodSucceeded, 0, digestTwo)}, nil)

	reconcileBuild(t, r, "api-build-1")

	got := storedBuild(t, c, "api-build-1")
	require.Equal(t, geassv1alpha1.GeassBuildSucceeded, got.Status.Phase)
	require.Equal(t, digestTwo, got.Status.ImageDigest)
}

func TestABuildThatEndedWithoutADigestFailsAndDoesNotKeepAnOldOne(t *testing.T) {
	// Reset, then run again: the Job succeeded but there is nothing to read the digest from.
	build := finishedBuild("api-build-1", geassv1alpha1.GeassBuildRunning, func(b *geassv1alpha1.GeassBuild) {
		b.Spec.Repository, b.Spec.WaitForCI = "", false
		b.Status.ImageDigest = digestOne // what a reset by hand that forgot the digest leaves behind
	})
	r, c, _ := buildTestReconciler(t, build, buildJobNamed("api-build-1-kaniko", "job", succeeded))
	r.Kube = kubeServing(t, nil, nil)

	reconcileBuild(t, r, "api-build-1")

	got := storedBuild(t, c, "api-build-1")
	require.Equal(t, geassv1alpha1.GeassBuildFailed, got.Status.Phase, "a run that did not say what it pushed is not a success because an earlier run did")
	require.Empty(t, got.Status.ImageDigest)
	require.Equal(t, "Kaniko completed without an immutable image digest", got.Status.FailureReason)
}

func TestASucceededBuildKeepsItsDigestWhileItsLogIsStillBeingArchived(t *testing.T) {
	// The build has a log store and its log is not archived yet, so it is looked at again while its Job lives.
	// If the pod cannot be read on that pass, what was recorded when the build succeeded stays.
	build := finishedBuild("api-build-1", geassv1alpha1.GeassBuildSucceeded, func(b *geassv1alpha1.GeassBuild) {
		b.Spec.Repository, b.Spec.WaitForCI = "", false
		b.Spec.LogStoreRef = &corev1.LocalObjectReference{Name: "logs"}
		b.Status.ImageDigest = digestOne
	})
	r, c, _ := buildTestReconciler(t, build, buildJobNamed("api-build-1-kaniko", "job", succeeded))
	r.Kube = kubeServing(t, nil, nil)

	reconcileBuild(t, r, "api-build-1")

	got := storedBuild(t, c, "api-build-1")
	require.Equal(t, geassv1alpha1.GeassBuildSucceeded, got.Status.Phase)
	require.Equal(t, digestOne, got.Status.ImageDigest)
}

func TestABuildThatMovedOnAfterAFailureIsNoLongerFailed(t *testing.T) {
	build := finishedBuild("api-build-1", geassv1alpha1.GeassBuildFailed, func(b *geassv1alpha1.GeassBuild) {
		b.Spec.Repository, b.Spec.WaitForCI = "", false
		b.Status.StartedAt, b.Status.CompletedAt, b.Status.ImageDigest = nil, nil, ""
		b.Status.FailureReason = "GitHub API returned 502 Bad Gateway"
	})
	r, c, _ := buildTestReconciler(t, build)

	reconcileBuild(t, r, "api-build-1")

	got := storedBuild(t, c, "api-build-1")
	require.Equal(t, geassv1alpha1.GeassBuildRunning, got.Status.Phase)
	require.Empty(t, got.Status.FailureReason, "the reason belonged to the attempt that failed")
}

func TestABuildWithNowhereToPushFailsBeforeAskingGitHubAnything(t *testing.T) {
	build := finishedBuild("api-build-1", geassv1alpha1.GeassBuildPending, func(b *geassv1alpha1.GeassBuild) {
		b.Spec.Registry = ""
		b.Status.StartedAt, b.Status.CompletedAt, b.Status.ImageDigest = nil, nil, ""
	})
	r, c, requests := buildTestReconciler(t, build)

	reconcileBuild(t, r, "api-build-1")

	got := storedBuild(t, c, "api-build-1")
	require.Equal(t, geassv1alpha1.GeassBuildFailed, got.Status.Phase)
	require.Equal(t, noRegistryMessage, got.Status.FailureReason)
	require.Zero(t, requests.count.Load(), "there is no point resolving the commit of a build that cannot run")
	require.False(t, jobExists(t, c, "api-build-1-kaniko"))
}

func TestABuildFromAFolderBuildsThatFolder(t *testing.T) {
	build := finishedBuild("api-build-1", geassv1alpha1.GeassBuildPending, func(b *geassv1alpha1.GeassBuild) {
		b.Spec.Context, b.Spec.Dockerfile = "/apps/api", "docker/Dockerfile"
	})

	job, err := (&GeassBuildReconciler{}).jobFor(build)

	require.NoError(t, err)
	args := job.Spec.Template.Spec.Containers[0].Args
	require.Contains(t, args, "--context=dir:///workspace/apps/api")
	require.Contains(t, args, "--dockerfile=/workspace/apps/api/docker/Dockerfile", "the Dockerfile is relative to the root directory, and absolute so a Dockerfile at the repository root is not found first")
	require.Equal(t, kanikoDigestFile, job.Spec.Template.Spec.Containers[0].TerminationMessagePath, "Kaniko is told to write the digest where Kubernetes reads the termination message from")
}

func TestABuildThatLeavesTheRepositoryFailsInsteadOfRunning(t *testing.T) {
	build := finishedBuild("api-build-1", geassv1alpha1.GeassBuildPending, func(b *geassv1alpha1.GeassBuild) {
		b.Spec.Repository, b.Spec.WaitForCI = "", false
		b.Spec.Context = "../elsewhere"
		b.Status.StartedAt, b.Status.CompletedAt, b.Status.ImageDigest = nil, nil, ""
	})
	r, c, _ := buildTestReconciler(t, build)

	reconcileBuild(t, r, "api-build-1")

	got := storedBuild(t, c, "api-build-1")
	require.Equal(t, geassv1alpha1.GeassBuildFailed, got.Status.Phase)
	require.Contains(t, got.Status.FailureReason, "leaves the repository")
	require.False(t, jobExists(t, c, "api-build-1-kaniko"))
}

func TestBuildsMadeBeforeTheAppLabelExistedGetIt(t *testing.T) {
	// The operator used to label its builds geass.dev/name only.
	old := finishedBuild("api-build-old", geassv1alpha1.GeassBuildSucceeded, func(b *geassv1alpha1.GeassBuild) {
		b.Labels = map[string]string{labelGeassName: "api", labelGeassKind: "GeassBuild"}
	})
	r, c, _ := buildTestReconciler(t, old)

	reconcileBuild(t, r, "api-build-old")

	got := storedBuild(t, c, "api-build-old")
	require.Equal(t, "api", got.Labels[platform.LabelApp], "so the webhook, the build actions, the status read and the clean-up find it")
	require.Equal(t, "api", got.Labels[labelGeassName], "and what it had stays")
}

func gitApp() *geassv1alpha1.GeassApp {
	return &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: "payments", Environment: "production", Port: 8080,
			Source: geassv1alpha1.GeassAppSource{Git: &geassv1alpha1.GeassAppGitSource{
				ConnectionRef: corev1.LocalObjectReference{Name: "payments-github"}, Repository: "acme/api", Branch: "main",
			}},
			Build:  geassv1alpha1.GeassAppBuildSpec{Registry: geassv1alpha1.GeassAppRegistrySpec{Repository: "registry.example.com/acme/api"}},
			Deploy: geassv1alpha1.GeassAppDeploySpec{Enabled: true},
		},
	}
}

func appTestReconciler(t *testing.T, objects ...client.Object) (*GeassAppReconciler, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, batchv1.AddToScheme(scheme))
	require.NoError(t, appsv1.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).
		WithStatusSubresource(&geassv1alpha1.GeassBuild{}, &geassv1alpha1.GeassApp{}).Build()
	return &GeassAppReconciler{Client: c, Scheme: scheme}, c
}

// TestBuildsTheOperatorMakesAreFoundByTheLabelEverythingSelectsOn is the contract between the operator and
// what looks builds up. It does not label builds itself: the builds come out of ensureGitBuild, as they do
// in the cluster, and are listed the way the push webhook, the build actions and the status read list them.
func TestBuildsTheOperatorMakesAreFoundByTheLabelEverythingSelectsOn(t *testing.T) {
	ctx := context.Background()
	app := gitApp()
	r, c := appTestReconciler(t, app)

	build, ready, err := r.ensureGitBuild(ctx, app)

	require.NoError(t, err)
	require.False(t, ready, "a build that has not run is not ready")
	var found geassv1alpha1.GeassBuildList
	require.NoError(t, c.List(ctx, &found, client.InNamespace(platform.SystemNamespace), client.MatchingLabels{platform.LabelApp: app.Name}))
	require.Len(t, found.Items, 1, "listing by geass.dev/app finds the build the operator made")
	require.Equal(t, build.Name, found.Items[0].Name)
	require.Equal(t, app.Name, found.Items[0].Labels[labelGeassName], "geass.dev/name stays, for kubectl -l geass.dev/name=<app>")
}

func TestOldBuildsAreActuallyPrunedWhenTheOperatorMadeThem(t *testing.T) {
	ctx := context.Background()
	app := gitApp()
	r, c := appTestReconciler(t, app)
	// Twelve builds, each for different settings so each has its own name, all made by the operator.
	names := make([]string, 0, 12)
	for i := range 12 {
		app.Spec.Source.Git.Dockerfile = "Dockerfile." + string(rune('a'+i))
		build, _, err := r.ensureGitBuild(ctx, app)
		require.NoError(t, err)
		names = append(names, build.Name)
		// What the operator made, with an age: the fake client sets none, and the clean-up keeps the newest.
		stored := storedBuild(t, c, build.Name)
		require.NoError(t, c.Delete(ctx, &stored))
		stored.ResourceVersion = ""
		stored.CreationTimestamp = metav1.NewTime(time.Date(2026, 1, 1, i, 0, 0, 0, time.UTC))
		stored.Status.Phase, stored.Status.ImageDigest = geassv1alpha1.GeassBuildSucceeded, digestOne
		require.NoError(t, c.Create(ctx, &stored))
	}
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(app), app))
	app.Status.ActiveBuild = names[0]
	require.NoError(t, c.Status().Update(ctx, app))
	buildReconciler := &GeassBuildReconciler{Client: c, Scheme: r.Scheme}

	last := storedBuild(t, c, names[11])
	buildReconciler.pruneSupersededBuilds(ctx, &last)

	var left geassv1alpha1.GeassBuildList
	require.NoError(t, c.List(ctx, &left, client.InNamespace(platform.SystemNamespace)))
	leftNames := make([]string, 0, len(left.Items))
	for _, b := range left.Items {
		leftNames = append(leftNames, b.Name)
	}
	// Built in order, so names[0] is the oldest and names[11] the newest. The oldest is the service's active build, so
	// of the two outside the newest ten (names[0] and names[1]) only names[1] goes.
	require.NotContains(t, leftNames, names[1], "the older of the builds beyond the ten newest goes; before the label was fixed none did")
	require.Contains(t, leftNames, names[0], "the build the service runs is never deleted")
	require.Contains(t, leftNames, names[11], "nor the one that just finished")
	require.Len(t, left.Items, keptBuildsPerApp+1, "the newest ten, and the one the service runs")
}

func TestTheServiceRunsAPullableReferenceNotABareDigest(t *testing.T) {
	build := &geassv1alpha1.GeassBuild{
		ObjectMeta: metav1.ObjectMeta{Name: "api-build-1"},
		Spec:       geassv1alpha1.GeassBuildSpec{Registry: "registry.example.com/acme/api:main"},
		Status:     geassv1alpha1.GeassBuildStatus{Image: "registry.example.com/acme/api:main", ImageDigest: digestOne},
	}

	image, err := builtImageReference(build)

	require.NoError(t, err)
	require.Equal(t, "registry.example.com/acme/api@"+digestOne, image)

	build.Status.Image = "" // an older build that never recorded it
	image, err = builtImageReference(build)
	require.NoError(t, err)
	require.Equal(t, "registry.example.com/acme/api@"+digestOne, image, "the registry it was told to push to says where it is")

	build.Status.ImageDigest = "sha256:short"
	_, err = builtImageReference(build)
	require.Error(t, err, "a value that is not a digest is not deployed")
}

func TestAServiceWithNoRegistryAsksForOneInsteadOfBuildingToANameThatDoesNotExist(t *testing.T) {
	app := gitApp()
	app.Spec.Build.Registry.Repository = ""
	r, c := appTestReconciler(t, app)

	_, ready, err := r.ensureGitBuild(context.Background(), app)

	require.EqualError(t, err, noRegistryMessage)
	require.False(t, ready)
	var builds geassv1alpha1.GeassBuildList
	require.NoError(t, c.List(context.Background(), &builds))
	require.Empty(t, builds.Items, "no build is made that could only fail on registry.geass.dev")
}

func platformRegistry(repository string, credential string) *geassv1alpha1.GeassPlatformConfig {
	config := &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassPlatformConfigSpec{Registry: geassv1alpha1.GeassPlatformRegistrySpec{Repository: repository}},
	}
	if credential != "" {
		config.Spec.Registry.CredentialRef = &corev1.LocalObjectReference{Name: credential}
	}
	return config
}

func registrySecret(name string, data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace}, Type: corev1.SecretTypeDockerConfigJson, Data: data}
}

func TestTheServiceUsesThePlatformRegistryWhenItNamesNone(t *testing.T) {
	app := gitApp()
	app.Spec.Build.Registry.Repository = ""
	r, c := appTestReconciler(t, app, platformRegistry("ghcr.io/acme/geass-builds", "registry-creds"))

	build, _, err := r.ensureGitBuild(context.Background(), app)

	require.NoError(t, err)
	stored := storedBuild(t, c, build.Name)
	require.Equal(t, "ghcr.io/acme/geass-builds", stored.Spec.Registry)
	require.Equal(t, &corev1.LocalObjectReference{Name: "registry-creds"}, stored.Spec.CredentialRef)
}

func TestAServiceWithItsOwnRegistryDoesNotBorrowThePlatformsCredential(t *testing.T) {
	app := gitApp() // its own repository, and no credential of its own
	r, c := appTestReconciler(t, app, platformRegistry("ghcr.io/acme/geass-builds", "registry-creds"))

	build, _, err := r.ensureGitBuild(context.Background(), app)

	require.NoError(t, err)
	require.Nil(t, storedBuild(t, c, build.Name).Spec.CredentialRef, "the platform's credential is for the platform's registry")
}

func TestThePodsPullABuiltImageWithTheRegistryCredential(t *testing.T) {
	ctx := context.Background()
	app := gitApp()
	app.Spec.Build.Registry.CredentialRef = &corev1.LocalObjectReference{Name: "registry-creds"}
	config := []byte(`{"auths":{"registry.example.com":{"auth":"dTpw"}}}`)
	r, c := appTestReconciler(t, app, registrySecret("registry-creds", map[string][]byte{corev1.DockerConfigJsonKey: config}))
	const ns = "payments-production"

	pullSecret, err := r.reconcileRegistryPullSecret(ctx, app, ns)

	require.NoError(t, err)
	require.Equal(t, &corev1.LocalObjectReference{Name: "api-registry-pull"}, pullSecret)
	var copied corev1.Secret
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "api-registry-pull", Namespace: ns}, &copied))
	require.Equal(t, corev1.SecretTypeDockerConfigJson, copied.Type, "a pull secret has to be of this type")
	require.Equal(t, config, copied.Data[corev1.DockerConfigJsonKey])

	app.Status.ResolvedImage = "registry.example.com/acme/api@" + digestOne
	require.NoError(t, r.reconcileDeployment(ctx, app, ns, withRegistryPullSecret(pullSecret)))
	pod := deploymentPodOf(t, c, "api", ns)
	require.Equal(t, "registry.example.com/acme/api@"+digestOne, pod.Containers[0].Image, "the pods run the repository at the digest, which a node can pull")
	require.Equal(t, []corev1.LocalObjectReference{{Name: "api-registry-pull"}}, pod.ImagePullSecrets)

	// The credential is taken off the service: the copy goes, and the next rollout does not ask for it.
	app.Spec.Build.Registry.CredentialRef = nil
	pullSecret, err = r.reconcileRegistryPullSecret(ctx, app, ns)
	require.NoError(t, err)
	require.Nil(t, pullSecret)
	err = c.Get(ctx, client.ObjectKey{Name: "api-registry-pull", Namespace: ns}, &corev1.Secret{})
	require.Error(t, err, "a credential the service no longer uses is not left in its namespace")
	require.NoError(t, r.reconcileDeployment(ctx, app, ns, withRegistryPullSecret(pullSecret)))
	require.Empty(t, deploymentPodOf(t, c, "api", ns).ImagePullSecrets)
}

func TestARegistryCredentialWithoutDockerConfigIsReportedNotCopied(t *testing.T) {
	app := gitApp()
	app.Spec.Build.Registry.CredentialRef = &corev1.LocalObjectReference{Name: "registry-creds"}
	r, _ := appTestReconciler(t, app, registrySecret("registry-creds", map[string][]byte{"token": []byte("x")}))

	_, err := r.reconcileRegistryPullSecret(context.Background(), app, "payments-production")

	require.ErrorContains(t, err, ".dockerconfigjson")
}

func TestAServiceThatDoesNotBuildHasNoRegistryPullSecret(t *testing.T) {
	app := functionApp("bun", "x")
	r := functionReconciler(t)

	pullSecret, err := r.reconcileRegistryPullSecret(context.Background(), app, "payments-production")

	require.NoError(t, err)
	require.Nil(t, pullSecret)
}

func TestDeletingAServiceRemovesItsRegistryPullSecret(t *testing.T) {
	ctx := context.Background()
	app := gitApp()
	const ns = "payments-production"
	r, c := appTestReconciler(t, app, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "api-registry-pull", Namespace: ns}})

	r.deleteTargetResources(ctx, app, ns)

	require.Error(t, c.Get(ctx, client.ObjectKey{Name: "api-registry-pull", Namespace: ns}, &corev1.Secret{}))
}

func TestAGitSourceWithAPathOutsideTheRepositoryIsRejected(t *testing.T) {
	app := gitApp()
	app.Spec.Source.Git.Context = "../../etc"

	require.ErrorContains(t, validateAppSource(app), "leaves the repository")
	app.Spec.Source.Git.Context = "apps/api"
	require.NoError(t, validateAppSource(app))
}

// A finished build is reset by whoever asks for a rebuild. The reset is the same function the dashboard calls.
func TestAResetBuildIsPendingWithNothingLeftOfItsLastRun(t *testing.T) {
	now := metav1.NewTime(time.Now())
	build := finishedBuild("api-build-1", geassv1alpha1.GeassBuildSucceeded, func(b *geassv1alpha1.GeassBuild) {
		b.Status.CompletedAt, b.Status.SourceRevision = &now, "0123"
	})
	platform.ResetBuildStatus(&build.Status)
	started, jobRan := buildProgress(build)

	require.False(t, started)
	require.False(t, jobRan)
	require.False(t, buildSettled(build))
}

func TestResolveGitImageStopsUntilTheBuildHasSucceeded(t *testing.T) {
	ctx := context.Background()
	app := gitApp()
	r, c := appTestReconciler(t, app)

	_, stop, err := r.resolveGitImage(ctx, app, "payments-production")
	require.NoError(t, err)
	require.True(t, stop, "the first pass makes the build and waits for it")
	require.Empty(t, app.Status.ResolvedImage)

	var builds geassv1alpha1.GeassBuildList
	require.NoError(t, c.List(ctx, &builds))
	require.Len(t, builds.Items, 1)
	build := builds.Items[0]
	build.Status.Phase, build.Status.Image, build.Status.ImageDigest = geassv1alpha1.GeassBuildSucceeded, "registry.example.com/acme/api", digestOne
	require.NoError(t, c.Status().Update(ctx, &build))

	_, stop, err = r.resolveGitImage(ctx, app, "payments-production")
	require.NoError(t, err)
	require.False(t, stop)
	require.Equal(t, "registry.example.com/acme/api@"+digestOne, app.Status.ResolvedImage)
	require.Equal(t, build.Name, app.Status.ActiveBuild)
}

// TestAGitServiceRunsItsBuiltImageEndToEnd drives the service's own Reconcile, with the build controller's
// part played by the test: the build finishes with a digest, and the Deployment must run the repository at that
// digest, with the registry credential to pull it, and settle without writing to the API server again.
func TestAGitServiceRunsItsBuiltImageEndToEnd(t *testing.T) {
	ctx := context.Background()
	r, app, log, c := steadyStateReconciler(t)
	var stored geassv1alpha1.GeassApp
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(app), &stored))
	stored.Spec.Source = geassv1alpha1.GeassAppSource{Git: &geassv1alpha1.GeassAppGitSource{
		ConnectionRef: corev1.LocalObjectReference{Name: "shop-github"}, Repository: "acme/web", Branch: "main",
	}}
	stored.Spec.Build.Registry = geassv1alpha1.GeassAppRegistrySpec{
		Repository: "registry.example.com/acme/web", CredentialRef: &corev1.LocalObjectReference{Name: "registry-creds"},
	}
	require.NoError(t, c.Update(ctx, &stored))
	login := []byte(`{"auths":{"registry.example.com":{"auth":"dTpw"}}}`)
	require.NoError(t, c.Create(ctx, registrySecret("registry-creds", map[string][]byte{corev1.DockerConfigJsonKey: login})))

	reconcileApp(t, r, app) // makes the build, and waits for it
	var builds geassv1alpha1.GeassBuildList
	require.NoError(t, c.List(ctx, &builds, client.MatchingLabels{platform.LabelApp: "web"}), "the build is found by the label everything else looks it up by")
	require.Len(t, builds.Items, 1)
	build := builds.Items[0]
	require.Equal(t, "registry.example.com/acme/web", build.Spec.Registry)

	build.Status.Phase, build.Status.Image, build.Status.ImageDigest = geassv1alpha1.GeassBuildSucceeded, build.Spec.Registry, digestOne
	require.NoError(t, c.Update(ctx, &build)) // this client has no status subresource for builds
	reconcileApp(t, r, app)
	markDeploymentAvailable(t, c)
	reconcileApp(t, r, app)

	var deploy appsv1.Deployment
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "web", Namespace: "shop-production"}, &deploy))
	pod := deploy.Spec.Template.Spec
	require.Equal(t, "registry.example.com/acme/web@"+digestOne, pod.Containers[0].Image, "a reference a node can pull, not the bare digest")
	require.Equal(t, []corev1.LocalObjectReference{{Name: "web-registry-pull"}}, pod.ImagePullSecrets)
	var pull corev1.Secret
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "web-registry-pull", Namespace: "shop-production"}, &pull))
	require.Equal(t, login, pull.Data[corev1.DockerConfigJsonKey])

	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(app), &stored))
	require.Equal(t, "registry.example.com/acme/web@"+digestOne, stored.Status.ResolvedImage, "recorded on the service, where the dashboard and the CLI read it")
	require.Equal(t, build.Name, stored.Status.ActiveBuild)
	require.Equal(t, build.Name, stored.Status.CurrentBuild, "and which build its settings call for")
	require.Equal(t, metav1.ConditionTrue, conditionOf(stored.Status.Conditions, platform.ConditionReady), "%v", stored.Status.Conditions)

	log.reset()
	for range 3 {
		reconcileApp(t, r, app)
	}
	require.Empty(t, log.writes, "a built service that is up costs the API server nothing: %v", log.writes)

	// A ready service is not only "not written to": the image it runs is put back on its status when that is lost,
	// as the pass that finds the Deployment ready is the only pass a service may ever make.
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(app), &stored))
	stored.Status.ResolvedImage, stored.Status.ActiveBuild = "", ""
	require.NoError(t, c.Status().Update(ctx, &stored))
	reconcileApp(t, r, app)
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(app), &stored))
	require.Equal(t, "registry.example.com/acme/web@"+digestOne, stored.Status.ResolvedImage)
	require.Equal(t, build.Name, stored.Status.ActiveBuild)
}

func TestAGitServiceWithoutARegistryTellsYouSo(t *testing.T) {
	ctx := context.Background()
	r, app, _, c := steadyStateReconciler(t)
	var stored geassv1alpha1.GeassApp
	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(app), &stored))
	stored.Spec.Source = geassv1alpha1.GeassAppSource{Git: &geassv1alpha1.GeassAppGitSource{
		ConnectionRef: corev1.LocalObjectReference{Name: "shop-github"}, Repository: "acme/web", Branch: "main",
	}}
	require.NoError(t, c.Update(ctx, &stored))

	reconcileApp(t, r, app)

	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(app), &stored))
	var ready *metav1.Condition
	for i := range stored.Status.Conditions {
		if stored.Status.Conditions[i].Type == platform.ConditionReady {
			ready = &stored.Status.Conditions[i]
		}
	}
	require.NotNil(t, ready)
	require.Contains(t, ready.Message, "no container registry is set for builds")
	var builds geassv1alpha1.GeassBuildList
	require.NoError(t, c.List(ctx, &builds))
	require.Empty(t, builds.Items)
}

func TestABuildPutBackToTheStartDoesNotAcceptTheLastRunsJob(t *testing.T) {
	ctx := context.Background()
	// Reset by hand, or by a dashboard whose delete of the Job failed: the last run's Job is still there, finished.
	build := finishedBuild("api-build-1", geassv1alpha1.GeassBuildPending, func(b *geassv1alpha1.GeassBuild) {
		b.Spec.Repository, b.Spec.WaitForCI = "", false
		b.Status.StartedAt, b.Status.CompletedAt, b.Status.ImageDigest = nil, nil, ""
	})
	r, c, _ := buildTestReconciler(t, build, buildJobNamed("api-build-1-kaniko", "last-run", succeeded))
	r.Kube = kubeServing(t, []corev1.Pod{kanikoPod("last-run-pod", "api-build-1-kaniko", "last-run", corev1.PodSucceeded, 0, digestOne)}, nil)

	reconcileBuild(t, r, "api-build-1")

	require.False(t, jobExists(t, c, "api-build-1-kaniko"), "the last run's Job is removed, not read")
	got := storedBuild(t, c, "api-build-1")
	require.Equal(t, geassv1alpha1.GeassBuildPending, got.Status.Phase, "and its result is not this run's")
	require.Empty(t, got.Status.ImageDigest)

	reconcileBuild(t, r, "api-build-1")

	require.True(t, jobExists(t, c, "api-build-1-kaniko"), "the next pass starts the build")
	require.Equal(t, geassv1alpha1.GeassBuildRunning, storedBuild(t, c, "api-build-1").Status.Phase)
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "api-build-1-kaniko", Namespace: platform.SystemNamespace}, &batchv1.Job{}))
}

func TestAPassThatReadTheOldJobDoesNotWriteOverAReset(t *testing.T) {
	ctx := context.Background()
	// What the pass read before the build was reset: started and running. What is stored now: put back to the start.
	stale := finishedBuild("api-build-1", geassv1alpha1.GeassBuildRunning, func(b *geassv1alpha1.GeassBuild) {
		b.Spec.Repository, b.Spec.WaitForCI = "", false
	})
	reset := stale.DeepCopy()
	platform.ResetBuildStatus(&reset.Status)
	r, c, _ := buildTestReconciler(t, reset, buildJobNamed("api-build-1-kaniko", "last-run", succeeded))
	r.Kube = kubeServing(t, nil, nil)

	require.NoError(t, r.updateStatus(ctx, stale, geassv1alpha1.GeassBuildSucceeded, nil, nil))

	got := storedBuild(t, c, "api-build-1")
	require.Equal(t, geassv1alpha1.GeassBuildPending, got.Status.Phase, "the reset stands: it is not failed for a digest the old run no longer has")
	require.Empty(t, got.Status.FailureReason)
	require.Nil(t, got.Status.CompletedAt)
}

func TestABuildDeletedWhileItIsLabelledIsNotAnError(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	old := finishedBuild("api-build-old", geassv1alpha1.GeassBuildSucceeded, func(b *geassv1alpha1.GeassBuild) {
		b.Labels = map[string]string{labelGeassName: "api"}
	})
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(old).WithInterceptorFuncs(interceptor.Funcs{
		Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			return apierrors.NewNotFound(schema.GroupResource{Group: "geass.geass.dev", Resource: "geassbuilds"}, obj.GetName())
		},
	}).Build()
	r := &GeassBuildReconciler{Client: c, Scheme: scheme}

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKey{Name: "api-build-old", Namespace: platform.SystemNamespace}})

	require.NoError(t, err, "a build pruned between the read and the label is gone, which is nothing to retry")
}

// pullSecretReconciler is an app reconciler whose client counts the Secrets it is asked to delete.
func pullSecretReconciler(t *testing.T, deletes *int, objects ...client.Object) (*GeassAppReconciler, client.Client) {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithInterceptorFuncs(interceptor.Funcs{
		Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			if _, isSecret := obj.(*corev1.Secret); isSecret {
				*deletes++
			}
			return c.Delete(ctx, obj, opts...)
		},
	}).Build()
	return &GeassAppReconciler{Client: c, Scheme: scheme}, c
}

func TestAServiceWithNoPullSecretCostsNoDelete(t *testing.T) {
	ctx := context.Background()
	const ns = "payments-production"
	var deletes int
	r, c := pullSecretReconciler(t, &deletes)

	for range 3 {
		pull, err := r.reconcileRegistryPullSecret(ctx, functionApp("bun", "x"), ns)
		require.NoError(t, err)
		require.Nil(t, pull)
		pull, err = r.reconcileRegistryPullSecret(ctx, gitApp(), ns)
		require.NoError(t, err)
		require.Nil(t, pull)
	}
	require.Zero(t, deletes, "nothing is there to delete, so nothing is sent to the API server")

	// One that is left over (the service stopped using a credential) is deleted, once.
	leftover := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "api-registry-pull", Namespace: ns}}
	require.NoError(t, c.Create(ctx, leftover))
	_, err := r.reconcileRegistryPullSecret(ctx, gitApp(), ns)
	require.NoError(t, err)
	require.Equal(t, 1, deletes)
	require.Error(t, c.Get(ctx, client.ObjectKeyFromObject(leftover), &corev1.Secret{}))
}

func TestAPullSecretOfAnotherTypeIsReplacedWhenGeassMadeIt(t *testing.T) {
	ctx := context.Background()
	const ns = "payments-production"
	app := gitApp()
	app.Spec.Build.Registry.CredentialRef = &corev1.LocalObjectReference{Name: "registry-creds"}
	config := []byte(`{"auths":{"registry.example.com":{"auth":"dTpw"}}}`)
	creds := registrySecret("registry-creds", map[string][]byte{corev1.DockerConfigJsonKey: config})

	var deletes int
	ours := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "api-registry-pull", Namespace: ns, Labels: map[string]string{platform.LabelManagedBy: platform.ManagedByValue}}, Type: corev1.SecretTypeOpaque}
	r, c := pullSecretReconciler(t, &deletes, creds, ours)
	pull, err := r.reconcileRegistryPullSecret(ctx, app, ns)
	require.NoError(t, err, "a type cannot be changed in place, so it is replaced")
	require.NotNil(t, pull)
	var got corev1.Secret
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "api-registry-pull", Namespace: ns}, &got))
	require.Equal(t, corev1.SecretTypeDockerConfigJson, got.Type)

	theirs := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "api-registry-pull", Namespace: ns}, Type: corev1.SecretTypeOpaque, Data: map[string][]byte{"keep": []byte("me")}}
	r, c = pullSecretReconciler(t, &deletes, creds, theirs)
	_, err = r.reconcileRegistryPullSecret(ctx, app, ns)
	require.ErrorContains(t, err, "not Geass's")
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "api-registry-pull", Namespace: ns}, &got), "somebody else's Secret is not deleted")
	require.Equal(t, []byte("me"), got.Data["keep"])
}

// A build is named by a hash of its settings, so settings turned on and off again return to the first build, which
// is older than the second. The service records which one its settings call for, for a push or a rebuild to run.
func TestTheServiceRecordsTheBuildItsCurrentSettingsCallFor(t *testing.T) {
	ctx := context.Background()
	app := gitApp()
	r, c := appTestReconciler(t, app)

	_, _, err := r.resolveGitImage(ctx, app, "payments-production")
	require.NoError(t, err)
	first := app.Status.CurrentBuild
	require.NotEmpty(t, first)

	app.Spec.Build.Cache = true // another build, made after the first
	_, _, err = r.resolveGitImage(ctx, app, "payments-production")
	require.NoError(t, err)
	second := app.Status.CurrentBuild
	require.NotEqual(t, first, second)

	app.Spec.Build.Cache = false // back to the first, which is the older of the two
	_, _, err = r.resolveGitImage(ctx, app, "payments-production")
	require.NoError(t, err)
	require.Equal(t, first, app.Status.CurrentBuild, "the build that is used again, not the newest")

	var builds geassv1alpha1.GeassBuildList
	require.NoError(t, c.List(ctx, &builds))
	require.Len(t, builds.Items, 2, "and no third build was made for it")
}
