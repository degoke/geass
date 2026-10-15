package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/source"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/githubapp"
	"github.com/degoke/geass/pkg/platform"
)

// GeassBuildReconciler creates and observes the short-lived Kaniko build Job.
type GeassBuildReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	Kube       kubernetes.Interface
	HTTPClient *http.Client
	// GitHubGuard holds back reports to GitHub that it refused; nil uses the guard the whole operator shares.
	GitHubGuard *githubapp.Guard
	// AsyncReports runs reports to GitHub on their own instead of inside the reconcile, so that a slow GitHub does not
	// hold the controller's worker. The operator sets it; without it a report runs inside the reconcile.
	AsyncReports bool

	retries reportRetries
	reports reportDispatcher
}

// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassbuilds,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassbuilds/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods/log,verbs=get
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassobjectstores,verbs=get;list;watch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassplatformconfigs,verbs=get
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassgithubconnections,verbs=get
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassgithubconnections/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassprojects;geassapps,verbs=get;list;watch

func (r *GeassBuildReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	result, err := r.reconcileBuild(ctx, req)
	// A report to GitHub that failed for a reason that may pass is tried again soon, even when the build itself has
	// nothing more to do.
	return r.retries.apply(req.NamespacedName, result), err
}

func (r *GeassBuildReconciler) reconcileBuild(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	build := &geassv1alpha1.GeassBuild{}
	if err := r.Get(ctx, req.NamespacedName, build); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if err := r.ensureAppLabel(ctx, build); err != nil {
		// A build that was deleted since it was read (pruned, or its service removed) is not an error.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	// A build that Geass has been reporting to GitHub may owe it a state that did not get through.
	r.reportTrackedBuild(ctx, build)
	// A build that has finished and has nothing left to do is not looked at again. Every build ever made
	// would otherwise be reconciled on each operator restart and each event of its job, and each pass
	// asked GitHub about it and copied its log again.
	if buildSettled(build) {
		return ctrl.Result{}, nil
	}
	started, jobRan := buildProgress(build)
	if !jobRan && !build.Status.CancelRequested {
		// A build that cannot run as it is written fails before GitHub is asked anything about it.
		if _, _, _, err := buildTarget(build); err != nil {
			return ctrl.Result{}, r.updateStatus(ctx, build, geassv1alpha1.GeassBuildFailed, nil, err)
		}
	}
	if !started && !build.Status.CancelRequested {
		// A build that has not started yet (new, or put back to the start by a push, a rebuild or a reset) has no
		// Job of its own. One that exists is the last run's, and its result is not this run's, so it is removed
		// here and not left to whoever reset the build: that may have failed to, or have had no way to.
		stale := &batchv1.Job{}
		err := r.Get(ctx, client.ObjectKey{Name: build.Name + "-kaniko", Namespace: build.Namespace}, stale)
		if err == nil {
			if err := r.Delete(ctx, stale, client.PropagationPolicy(metav1.DeletePropagationBackground)); client.IgnoreNotFound(err) != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: 2 * time.Second}, nil
		} else if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
	}
	if build.Spec.Repository != "" && !build.Status.CancelRequested && !started {
		ready, err := r.refreshGitHubState(ctx, build)
		if err != nil {
			return ctrl.Result{RequeueAfter: 30 * time.Second}, r.updateStatus(ctx, build, geassv1alpha1.GeassBuildFailed, nil, err)
		}
		if !ready {
			return ctrl.Result{RequeueAfter: 15 * time.Second}, r.updateStatus(ctx, build, geassv1alpha1.GeassBuildWaitingForCI, nil, nil)
		}
	}
	if build.Spec.GitCredentialRef != nil && !jobRan {
		if err := r.refreshGitCredentialToken(ctx, build); err != nil {
			return ctrl.Result{RequeueAfter: 30 * time.Second}, r.updateStatus(ctx, build, geassv1alpha1.GeassBuildFailed, nil, err)
		}
	}
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: build.Name + "-kaniko", Namespace: build.Namespace}}
	if build.Status.CancelRequested {
		if err := r.Get(ctx, client.ObjectKeyFromObject(job), job); err == nil {
			if err := r.Delete(ctx, job); err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, r.updateStatus(ctx, build, geassv1alpha1.GeassBuildCancelled, nil, nil)
	}
	if err := r.Get(ctx, client.ObjectKeyFromObject(job), job); apierrors.IsNotFound(err) {
		if jobRan {
			// The job ran and has since been removed (its time to live ran out). A finished build is not
			// run again because its job is gone: building again takes a retrigger, which starts the build
			// over.
			return ctrl.Result{}, nil
		}
		if build.Spec.WaitForCI && !strings.EqualFold(build.Status.CIState, "success") {
			return ctrl.Result{RequeueAfter: 15 * time.Second}, r.updateStatus(ctx, build, geassv1alpha1.GeassBuildWaitingForCI, ptr(metav1.Now()), nil)
		}
		var jobErr error
		if job, jobErr = r.jobFor(build); jobErr != nil {
			// The build cannot run as it is written, and running it again would say the same.
			return ctrl.Result{}, r.updateStatus(ctx, build, geassv1alpha1.GeassBuildFailed, nil, jobErr)
		}
		// Builds run in the project's region so a project never spends another region's capacity, or
		// pulls its source and pushes its image across regions. They never need the tags a single
		// service asked for, but they check out its source and write its image layers to the node, so
		// they stay on the nodes the project requires.
		jobPlacement, err := platform.ProjectJobPlacement(ctx, r.Client, build.Spec.Project)
		if err != nil {
			return ctrl.Result{}, err
		}
		jobPlacement.ApplyToPodSpec(&job.Spec.Template.Spec)
		if err := controllerutil.SetControllerReference(build, job, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, job); err != nil {
			return ctrl.Result{}, err
		}
		now := metav1.Now()
		return ctrl.Result{}, r.updateStatus(ctx, build, geassv1alpha1.GeassBuildRunning, &now, nil)
	} else if err != nil {
		return ctrl.Result{}, err
	}

	phase := geassv1alpha1.GeassBuildRunning
	var completed *metav1.Time
	var failure error
	if job.Status.Succeeded > 0 {
		phase = geassv1alpha1.GeassBuildSucceeded
		completed = ptr(metav1.Now())
	} else if job.Status.Failed > 0 {
		phase = geassv1alpha1.GeassBuildFailed
		completed = ptr(metav1.Now())
		failure = errors.New(kanikoJobFailedReason)
	}
	if completed != nil && build.Spec.LogStoreRef != nil {
		if ref := r.archiveBuildLog(ctx, build); ref != "" {
			build.Status.LogRef = ref
		}
	}
	if err := r.updateStatus(ctx, build, phase, nil, failure); err != nil {
		return ctrl.Result{}, err
	}
	log.Info("Reconciled GeassBuild", "job", job.Name, "phase", phase)
	if completed != nil {
		if phase == geassv1alpha1.GeassBuildSucceeded {
			r.pruneSupersededBuilds(ctx, build)
		}
		return ctrl.Result{}, nil
	}
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

func ptr(t metav1.Time) *metav1.Time { return &t }

const (
	// kanikoJobTTL is how long a finished build Job (and its pod, and so its log) is kept. By then the
	// result is in the build's status and the log has been archived if the app asked for that.
	kanikoJobTTL int32 = 3600
	// keptBuildsPerApp is how many finished builds of one app are kept for its history. A build is one
	// object per change to the app's source settings, so without a limit they only ever accumulate.
	keptBuildsPerApp = 10
)

// buildProgress says how far a build got, from what it recorded. started means its job was created,
// so GitHub no longer has anything to decide; jobRan means it finished, one way or the other. A
// retrigger sets the phase back to Pending and clears the start time, which is what starts it over.
func buildProgress(build *geassv1alpha1.GeassBuild) (started, jobRan bool) {
	if build.Status.StartedAt == nil {
		return false, false
	}
	switch build.Status.Phase {
	case geassv1alpha1.GeassBuildRunning:
		return true, false
	case geassv1alpha1.GeassBuildSucceeded, geassv1alpha1.GeassBuildFailed:
		return true, true
	default:
		return false, false
	}
}

// buildSettled reports whether a build is finished and needs nothing more from the controller: it
// succeeded with an image digest, and its log is archived or was never going to be.
func buildSettled(build *geassv1alpha1.GeassBuild) bool {
	if build.Status.Phase != geassv1alpha1.GeassBuildSucceeded || build.Status.ImageDigest == "" || build.Status.CancelRequested {
		return false
	}
	return build.Spec.LogStoreRef == nil || !strings.HasPrefix(build.Status.LogRef, "pod-log://")
}

// pruneSupersededBuilds deletes the oldest finished builds of an app beyond keptBuildsPerApp. The build
// the app currently runs is never deleted, nor is one that is still going. A problem is logged and left
// for the next build to try again.
func (r *GeassBuildReconciler) pruneSupersededBuilds(ctx context.Context, build *geassv1alpha1.GeassBuild) {
	log := logf.FromContext(ctx)
	var builds geassv1alpha1.GeassBuildList
	if err := r.List(ctx, &builds, client.InNamespace(build.Namespace), client.MatchingLabels{platform.LabelApp: build.Spec.App}); err != nil {
		log.Error(err, "Failed to list builds to prune", "app", build.Spec.App)
		return
	}
	active := ""
	var app geassv1alpha1.GeassApp
	if err := r.Get(ctx, client.ObjectKey{Name: build.Spec.App, Namespace: build.Namespace}, &app); err == nil {
		active = app.Status.ActiveBuild
	}
	for _, name := range buildsToPrune(builds.Items, build.Name, active, keptBuildsPerApp) {
		if err := r.Delete(ctx, &geassv1alpha1.GeassBuild{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: build.Namespace}}); client.IgnoreNotFound(err) != nil {
			log.Error(err, "Failed to delete an old build", "build", name)
			continue
		}
		log.Info("Deleted an old GeassBuild", "build", name, "app", build.Spec.App)
	}
}

// buildsToPrune names the finished builds to delete: all but the newest keep, never the current build or
// the one the app runs, and never one that has not finished.
func buildsToPrune(builds []geassv1alpha1.GeassBuild, current, active string, keep int) []string {
	finished := make([]geassv1alpha1.GeassBuild, 0, len(builds))
	for i := range builds {
		switch builds[i].Status.Phase {
		case geassv1alpha1.GeassBuildSucceeded, geassv1alpha1.GeassBuildFailed, geassv1alpha1.GeassBuildCancelled:
			finished = append(finished, builds[i])
		}
	}
	slices.SortFunc(finished, func(a, b geassv1alpha1.GeassBuild) int {
		return b.CreationTimestamp.Compare(a.CreationTimestamp.Time)
	})
	var doomed []string
	for i, candidate := range finished {
		if i < keep || candidate.Name == current || candidate.Name == active {
			continue
		}
		doomed = append(doomed, candidate.Name)
	}
	return doomed
}

const (
	// kanikoContainerName is the container of a build's pod that runs Kaniko.
	kanikoContainerName = "kaniko"
	// noRegistryMessage is what a build says when there is nowhere to push its image. Geass has no registry
	// of its own to fall back on, so a build that is not told where to push does not run.
	noRegistryMessage = "no container registry is set for builds: set one under Settings > Registry, or set spec.build.registry.repository on the service"
	// kanikoWorkspace is where the git container checks the repository out and Kaniko reads it from.
	kanikoWorkspace = "/workspace"
	// kanikoDigestFile is where Kaniko writes the digest of the image it pushed. It is the container's
	// termination message, which Kubernetes keeps in the pod's status, so the digest is read from there and
	// not picked out of the log.
	kanikoDigestFile = "/dev/termination-log"
)

// buildTarget is where a build pushes its image and where Kaniko finds what to build in the checkout, or why
// the build cannot run: no registry to push to, or a path that leaves the repository.
func buildTarget(build *geassv1alpha1.GeassBuild) (image, contextDir, dockerfile string, err error) {
	if build.Spec.Registry == "" {
		return "", "", "", errors.New(noRegistryMessage)
	}
	contextDir, dockerfile, err = platform.BuildPaths(kanikoWorkspace, build.Spec.Context, build.Spec.Dockerfile)
	return build.Spec.Registry, contextDir, dockerfile, err
}

func (r *GeassBuildReconciler) jobFor(build *geassv1alpha1.GeassBuild) (*batchv1.Job, error) {
	image, contextDir, dockerfile, err := buildTarget(build)
	if err != nil {
		return nil, err
	}
	args := []string{"--dockerfile=" + dockerfile, "--context=dir://" + contextDir, "--destination=" + image, "--digest-file=" + kanikoDigestFile}
	if build.Spec.Cache {
		args = append(args, "--cache=true")
	}
	cloneCommand := "git clone --branch \"$BRANCH\" https://github.com/" + strings.TrimPrefix(build.Spec.Repository, "https://github.com/") + " " + kanikoWorkspace
	if build.Spec.GitCredentialRef != nil {
		cloneCommand = "git -c http.extraheader=\"Authorization: Bearer $GITHUB_TOKEN\" clone --branch \"$BRANCH\" https://github.com/" + strings.TrimPrefix(build.Spec.Repository, "https://github.com/") + " " + kanikoWorkspace
	}
	cloneCommand += " && git -C " + kanikoWorkspace + " checkout --detach \"$REVISION\""
	podSpec := corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, InitContainers: []corev1.Container{{Name: "git", Image: "alpine/git:latest", Command: []string{"sh", "-c", cloneCommand}, Env: []corev1.EnvVar{{Name: "BRANCH", Value: build.Spec.Branch}, {Name: "REVISION", Value: buildRevision(build)}}, VolumeMounts: []corev1.VolumeMount{{Name: "workspace", MountPath: kanikoWorkspace}}}}, Containers: []corev1.Container{{Name: kanikoContainerName, Image: "gcr.io/kaniko-project/executor:v1.23.2", Args: args, TerminationMessagePath: kanikoDigestFile, VolumeMounts: []corev1.VolumeMount{{Name: "workspace", MountPath: kanikoWorkspace}}}}, Volumes: []corev1.Volume{{Name: "workspace", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}}}
	if build.Spec.GitCredentialRef != nil {
		podSpec.InitContainers[0].Env = append(podSpec.InitContainers[0].Env, corev1.EnvVar{Name: "GITHUB_TOKEN", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: *build.Spec.GitCredentialRef, Key: "token"}}})
	}
	if build.Spec.CredentialRef != nil {
		podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{Name: "registry-auth", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: build.Spec.CredentialRef.Name, Items: []corev1.KeyToPath{{Key: ".dockerconfigjson", Path: "config.json"}}}}})
		podSpec.Containers[0].VolumeMounts = append(podSpec.Containers[0].VolumeMounts, corev1.VolumeMount{Name: "registry-auth", MountPath: "/kaniko/.docker", ReadOnly: true})
	}
	return &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: build.Name + "-kaniko", Namespace: build.Namespace, Labels: map[string]string{labelGeassName: build.Spec.App}}, Spec: batchv1.JobSpec{BackoffLimit: ptrInt32(2), TTLSecondsAfterFinished: ptrInt32(kanikoJobTTL), Template: corev1.PodTemplateSpec{Spec: podSpec}}}, nil
}

func ptrInt32(v int32) *int32 { return &v }

func (r *GeassBuildReconciler) updateStatus(ctx context.Context, build *geassv1alpha1.GeassBuild, phase geassv1alpha1.GeassBuildPhase, started *metav1.Time, failure error) error {
	latest := &geassv1alpha1.GeassBuild{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(build), latest); err != nil {
		return err
	}
	// Read what the job says before the status is built, so the write is a single patch of the result.
	retryCount := int32(-1)
	imageDigest := ""
	job := &batchv1.Job{}
	jobFound := r.Get(ctx, client.ObjectKey{Name: build.Name + "-kaniko", Namespace: build.Namespace}, job) == nil
	if jobFound {
		retryCount = job.Status.Failed
		if phase == geassv1alpha1.GeassBuildSucceeded {
			imageDigest = r.digestFromBuildPod(ctx, build, job)
		}
	}
	// This pass read a Job that the build has since been put back to the start under: what it found is the old
	// run's, and writing it would undo the reset (or fail the build for a digest that run no longer has).
	if jobFound && started == nil && latest.Status.StartedAt == nil {
		return nil
	}
	// The digest a run ends with is the one its own pod reports. One recorded earlier is kept only when the
	// build had already succeeded: a build that was reset and has run again is not Succeeded until it ends, so
	// a digest left from its last run is never taken for the result of this one.
	if phase == geassv1alpha1.GeassBuildSucceeded && imageDigest == "" && latest.Status.Phase == geassv1alpha1.GeassBuildSucceeded {
		imageDigest = latest.Status.ImageDigest
	}
	before := latest.Status.Phase
	wrote, err := platform.PatchStatus(ctx, r.Client, latest, func() {
		latest.Status.Phase = phase
		latest.Status.JobRef = build.Name + "-kaniko"
		latest.Status.LogRef = build.Status.LogRef
		if latest.Status.LogRef == "" {
			latest.Status.LogRef = "pod-log://" + build.Name + "-kaniko/" + kanikoContainerName
		}
		latest.Status.SourceRevision = build.Status.SourceRevision
		latest.Status.CIState = build.Status.CIState
		latest.Status.CIChecks = append([]string(nil), build.Status.CIChecks[:min(len(build.Status.CIChecks), geassv1alpha1.MaxCIChecks)]...)
		// What the build pushes to is what it was told to, so it is the same on every run.
		latest.Status.Image = build.Spec.Registry
		if started != nil && latest.Status.StartedAt == nil {
			latest.Status.StartedAt = started
		}
		if failure != nil {
			latest.Status.FailureReason = failure.Error()
		} else if phase != geassv1alpha1.GeassBuildFailed {
			// A build that went on after a failure (GitHub answered the next time) is not failed any more.
			latest.Status.FailureReason = ""
		}
		if retryCount >= 0 {
			latest.Status.RetryCount = retryCount
		}
		if phase == geassv1alpha1.GeassBuildSucceeded {
			latest.Status.ImageDigest = imageDigest
			if imageDigest == "" {
				latest.Status.Phase = geassv1alpha1.GeassBuildFailed
				latest.Status.FailureReason = "Kaniko completed without an immutable image digest"
			}
			// Once: moving it on every pass made a finished build look different each time it was looked at.
			if latest.Status.CompletedAt == nil {
				latest.Status.CompletedAt = ptr(metav1.Now())
			}
		}
	})
	if err != nil {
		return err
	}
	// Told to GitHub after the status is written, so GitHub is only told what the build recorded. A build whose
	// phase did not change has nothing new to say, which is also what keeps builds made before this existed quiet.
	if wrote {
		changed := latest.Status.Phase != before
		r.dispatchReport(ctx, latest, func(ctx context.Context, build *geassv1alpha1.GeassBuild) { r.reportBuild(ctx, build, changed) })
	}
	return nil
}

func (r *GeassBuildReconciler) archiveBuildLog(ctx context.Context, build *geassv1alpha1.GeassBuild) string {
	if r.Kube == nil || build.Spec.LogStoreRef == nil {
		return ""
	}
	store := &geassv1alpha1.GeassObjectStore{}
	if err := r.Get(ctx, client.ObjectKey{Name: build.Spec.LogStoreRef.Name, Namespace: build.Namespace}, store); err != nil || store.Status.Endpoint == "" || store.Status.ConnectionSecret == "" {
		return ""
	}
	ns, err := resourceNamespace(build.Spec.Project, string(build.Spec.Environment))
	if err != nil {
		return ""
	}
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Name: store.Status.ConnectionSecret, Namespace: ns}, secret); err != nil {
		return ""
	}
	pods, err := r.Kube.CoreV1().Pods(build.Namespace).List(ctx, metav1.ListOptions{LabelSelector: "job-name=" + build.Name + "-kaniko"})
	if err != nil {
		return ""
	}
	var logData []byte
	for _, pod := range pods.Items {
		logData, err = r.Kube.CoreV1().Pods(build.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: kanikoContainerName}).Do(ctx).Raw()
		if err == nil && len(logData) > 0 {
			break
		}
	}
	if len(logData) == 0 {
		return ""
	}
	bucket := string(secret.Data["bucket"])
	if bucket == "" {
		bucket = store.Name
	}
	objectURL := strings.TrimRight(store.Status.Endpoint, "/") + "/" + url.PathEscape(bucket) + "/builds/" + url.PathEscape(build.Name) + ".log"
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, objectURL, bytes.NewReader(logData))
	if err != nil {
		return ""
	}
	request.Header.Set("Content-Type", "text/plain; charset=utf-8")
	request.SetBasicAuth(string(secret.Data["accessKey"]), string(secret.Data["secretKey"]))
	httpClient := r.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return ""
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ""
	}
	return "object://" + store.Name + "/builds/" + build.Name + ".log"
}

func buildRevision(build *geassv1alpha1.GeassBuild) string {
	if build.Status.SourceRevision != "" {
		return build.Status.SourceRevision
	}
	if build.Spec.Revision != "" {
		return build.Spec.Revision
	}
	return build.Spec.Branch
}

func (r *GeassBuildReconciler) refreshGitHubState(ctx context.Context, build *geassv1alpha1.GeassBuild) (bool, error) {
	if build.Status.SourceRevision == "" {
		if build.Spec.Revision != "" {
			build.Status.SourceRevision = build.Spec.Revision
		} else {
			connection := &geassv1alpha1.GeassGitHubConnection{}
			if build.Spec.ConnectionRef == nil {
				return false, fmt.Errorf("GitHub connection reference is required to resolve a branch")
			}
			if err := r.Get(ctx, client.ObjectKey{Name: build.Spec.ConnectionRef.Name, Namespace: build.Namespace}, connection); err != nil {
				return false, err
			}
			secret := &corev1.Secret{}
			if err := r.Get(ctx, client.ObjectKey{Name: connection.Spec.SecretRef.Name, Namespace: connection.Namespace}, secret); err != nil {
				return false, err
			}
			token, err := r.githubTokenResolver().ConnectionToken(ctx, secret)
			if err != nil {
				return false, err
			}
			sha, err := r.githubRequest(ctx, token, build, http.MethodGet, "/commits/"+url.PathEscape(build.Spec.Branch), nil)
			if err != nil {
				return false, err
			}
			var commit struct {
				SHA string `json:"sha"`
			}
			if err := json.Unmarshal(sha, &commit); err != nil || commit.SHA == "" {
				return false, fmt.Errorf("GitHub did not return a commit for branch %q", build.Spec.Branch)
			}
			build.Status.SourceRevision = commit.SHA
		}
	}
	if !build.Spec.WaitForCI {
		return true, nil
	}
	connection := &geassv1alpha1.GeassGitHubConnection{}
	if build.Spec.ConnectionRef == nil {
		return false, fmt.Errorf("GitHub connection reference is required for CI gating")
	}
	if err := r.Get(ctx, client.ObjectKey{Name: build.Spec.ConnectionRef.Name, Namespace: build.Namespace}, connection); err != nil {
		return false, err
	}
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Name: connection.Spec.SecretRef.Name, Namespace: connection.Namespace}, secret); err != nil {
		return false, err
	}
	token, err := r.githubTokenResolver().ConnectionToken(ctx, secret)
	if err != nil {
		return false, err
	}
	checks, err := r.githubRequest(ctx, token, build, http.MethodGet, "/commits/"+url.PathEscape(build.Status.SourceRevision)+"/check-runs", nil)
	if err != nil {
		var refused *githubAPIError
		if errors.As(err, &refused) && refused.code == http.StatusForbidden {
			return false, fmt.Errorf("%w: reading check runs needs the Checks: Read permission on the Geass GitHub App, which an app registered before it was requested does not have", err)
		}
		return false, err
	}
	var result struct {
		CheckRuns []struct {
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"check_runs"`
	}
	if err := json.Unmarshal(checks, &result); err != nil {
		return false, err
	}
	build.Status.CIChecks = build.Status.CIChecks[:0]
	if len(result.CheckRuns) == 0 {
		build.Status.CIState = "pending"
		return false, nil
	}
	for _, check := range result.CheckRuns {
		build.Status.CIChecks = append(build.Status.CIChecks, check.Name)
		if check.Status != "completed" {
			build.Status.CIState = "pending"
			return false, nil
		}
		if check.Conclusion != "success" {
			build.Status.CIState = "failure"
			return false, fmt.Errorf("GitHub check %q concluded %s", check.Name, check.Conclusion)
		}
	}
	build.Status.CIState = "success"
	return true, nil
}

func (r *GeassBuildReconciler) githubTokenResolver() *githubapp.TokenResolver {
	return &githubapp.TokenResolver{Client: r.Client, HTTP: r.HTTPClient, Namespace: platform.SystemNamespace}
}

func (r *GeassBuildReconciler) refreshGitCredentialToken(ctx context.Context, build *geassv1alpha1.GeassBuild) error {
	if build.Spec.GitCredentialRef == nil {
		return nil
	}
	secret := &corev1.Secret{}
	if err := r.Get(ctx, client.ObjectKey{Name: build.Spec.GitCredentialRef.Name, Namespace: build.Namespace}, secret); err != nil {
		return err
	}
	_, err := r.githubTokenResolver().ConnectionToken(ctx, secret)
	return err
}

// githubRequest makes a request to the build's repository on GitHub's API and returns the answer. body, when it
// is not nil, is sent as JSON, which is what the calls that change something take.
func (r *GeassBuildReconciler) githubRequest(ctx context.Context, token string, build *geassv1alpha1.GeassBuild, method, path string, body any) ([]byte, error) {
	repo := githubapp.NormalizeRepository(build.Spec.Repository)
	if repo == "" {
		return nil, fmt.Errorf("%q is not a GitHub repository", build.Spec.Repository)
	}
	httpClient := r.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, "https://api.github.com/repos/"+repo+path, reader)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/vnd.github+json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= 300 {
		return nil, &githubAPIError{code: response.StatusCode, status: response.Status}
	}
	return data, nil
}

// githubAPIError is GitHub refusing a request, with the code kept so a caller can say what the refusal means.
type githubAPIError struct {
	code   int
	status string
}

func (e *githubAPIError) Error() string { return "GitHub API returned " + e.status }

// digestFromBuildPod reads the digest of the image a build pushed from the pods of its Job. Kaniko writes it
// to the container's termination message (kanikoDigestFile), which Kubernetes keeps in the pod's status. The
// log is the fallback, for a build whose Job was made before Kaniko was asked to write it: Kaniko ends a push
// with "Pushed <repository>@sha256:<digest>". Only pods that finished successfully are read, and only the
// ones this Job owns: a Job that was deleted and made again has the same name, and its old pods can outlive it.
func (r *GeassBuildReconciler) digestFromBuildPod(ctx context.Context, build *geassv1alpha1.GeassBuild, job *batchv1.Job) string {
	if r.Kube == nil {
		return ""
	}
	pods, err := r.Kube.CoreV1().Pods(build.Namespace).List(ctx, metav1.ListOptions{LabelSelector: "job-name=" + job.Name})
	if err != nil {
		return ""
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Status.Phase != corev1.PodSucceeded || (job.UID != "" && !metav1.IsControlledBy(pod, job)) {
			continue
		}
		for _, status := range pod.Status.ContainerStatuses {
			if status.Name != kanikoContainerName || status.State.Terminated == nil || status.State.Terminated.ExitCode != 0 {
				continue
			}
			if digest := strings.TrimSpace(status.State.Terminated.Message); platform.ValidImageDigest(digest) {
				return digest
			}
		}
		data, err := r.Kube.CoreV1().Pods(build.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: kanikoContainerName}).Do(ctx).Raw()
		if err != nil {
			continue
		}
		if digest := platform.DigestFromKanikoLog(string(data)); digest != "" {
			return digest
		}
	}
	return ""
}

// ensureAppLabel puts the label a service's builds are found by on a build made before every build carried
// it. The operator labelled its builds geass.dev/name only, and everything that looks builds up (the push
// webhook, the build actions, the status read and the clean-up of old builds) selects on geass.dev/app, so
// those builds were never found. Every build is reconciled when the operator starts, so this reaches them all.
func (r *GeassBuildReconciler) ensureAppLabel(ctx context.Context, build *geassv1alpha1.GeassBuild) error {
	if build.Spec.App == "" || build.Labels[platform.LabelApp] == build.Spec.App {
		return nil
	}
	patch := client.MergeFrom(build.DeepCopy())
	if build.Labels == nil {
		build.Labels = map[string]string{}
	}
	build.Labels[platform.LabelApp] = build.Spec.App
	return r.Patch(ctx, build, patch)
}

func (r *GeassBuildReconciler) SetupWithManager(mgr ctrl.Manager) error {
	controllerBuilder := ctrl.NewControllerManagedBy(mgr)
	if r.AsyncReports {
		// What a report that runs on its own wants tried again arrives here, as the pause it asked for ends.
		controllerBuilder = controllerBuilder.WatchesRawSource(source.Channel(r.reports.channel(), &handler.EnqueueRequestForObject{}))
	}
	return controllerBuilder.For(&geassv1alpha1.GeassBuild{}).Owns(&batchv1.Job{}).Named("geassbuild").Complete(r)
}
