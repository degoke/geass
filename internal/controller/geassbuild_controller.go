package controller

import (
	"bytes"
	"context"
	"encoding/json"
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
	logf "sigs.k8s.io/controller-runtime/pkg/log"

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
}

// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassbuilds,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassbuilds/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods/log,verbs=get
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassobjectstores,verbs=get;list;watch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassplatformconfigs,verbs=get
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassgithubconnections,verbs=get

func (r *GeassBuildReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	build := &geassv1alpha1.GeassBuild{}
	if err := r.Get(ctx, req.NamespacedName, build); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	// A build that has finished and has nothing left to do is not looked at again. Every build ever made
	// would otherwise be reconciled on each operator restart and each event of its job, and each pass
	// asked GitHub about it and copied its log again.
	if buildSettled(build) {
		return ctrl.Result{}, nil
	}
	started, jobRan := buildProgress(build)
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
		job = r.jobFor(build)
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
		failure = fmt.Errorf("kaniko Job failed")
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

func (r *GeassBuildReconciler) jobFor(build *geassv1alpha1.GeassBuild) *batchv1.Job {
	image := build.Status.Image
	if image == "" {
		image = build.Spec.Registry
	}
	if image == "" {
		image = "registry.geass.dev/" + build.Spec.Project + "/" + build.Spec.App + ":" + buildRevision(build)
	}
	dockerfile := build.Spec.Dockerfile
	if dockerfile == "" {
		dockerfile = "Dockerfile"
	}
	args := []string{"--dockerfile=" + dockerfile, "--context=dir:///workspace", "--destination=" + image}
	if build.Spec.Cache {
		args = append(args, "--cache=true")
	}
	cloneCommand := "git clone --branch \"$BRANCH\" https://github.com/" + strings.TrimPrefix(build.Spec.Repository, "https://github.com/") + " /workspace"
	if build.Spec.GitCredentialRef != nil {
		cloneCommand = "git -c http.extraheader=\"Authorization: Bearer $GITHUB_TOKEN\" clone --branch \"$BRANCH\" https://github.com/" + strings.TrimPrefix(build.Spec.Repository, "https://github.com/") + " /workspace"
	}
	cloneCommand += " && git -C /workspace checkout --detach \"$REVISION\""
	podSpec := corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever, InitContainers: []corev1.Container{{Name: "git", Image: "alpine/git:latest", Command: []string{"sh", "-c", cloneCommand}, Env: []corev1.EnvVar{{Name: "BRANCH", Value: build.Spec.Branch}, {Name: "REVISION", Value: buildRevision(build)}}, VolumeMounts: []corev1.VolumeMount{{Name: "workspace", MountPath: "/workspace"}}}}, Containers: []corev1.Container{{Name: "kaniko", Image: "gcr.io/kaniko-project/executor:v1.23.2", Args: args, VolumeMounts: []corev1.VolumeMount{{Name: "workspace", MountPath: "/workspace"}}}}, Volumes: []corev1.Volume{{Name: "workspace", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}}}
	if build.Spec.GitCredentialRef != nil {
		podSpec.InitContainers[0].Env = append(podSpec.InitContainers[0].Env, corev1.EnvVar{Name: "GITHUB_TOKEN", ValueFrom: &corev1.EnvVarSource{SecretKeyRef: &corev1.SecretKeySelector{LocalObjectReference: *build.Spec.GitCredentialRef, Key: "token"}}})
	}
	if build.Spec.CredentialRef != nil {
		podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{Name: "registry-auth", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: build.Spec.CredentialRef.Name, Items: []corev1.KeyToPath{{Key: ".dockerconfigjson", Path: "config.json"}}}}})
		podSpec.Containers[0].VolumeMounts = append(podSpec.Containers[0].VolumeMounts, corev1.VolumeMount{Name: "registry-auth", MountPath: "/kaniko/.docker", ReadOnly: true})
	}
	return &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: build.Name + "-kaniko", Namespace: build.Namespace, Labels: map[string]string{labelGeassName: build.Spec.App}}, Spec: batchv1.JobSpec{BackoffLimit: ptrInt32(2), TTLSecondsAfterFinished: ptrInt32(kanikoJobTTL), Template: corev1.PodTemplateSpec{Spec: podSpec}}}
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
	if err := r.Get(ctx, client.ObjectKey{Name: build.Name + "-kaniko", Namespace: build.Namespace}, job); err == nil {
		retryCount = job.Status.Failed
		imageDigest = job.Annotations["geass.dev/image-digest"]
	}
	if phase == geassv1alpha1.GeassBuildSucceeded && imageDigest == "" && latest.Status.ImageDigest == "" {
		imageDigest = r.digestFromBuildPod(ctx, build)
	}
	_, err := platform.PatchStatus(ctx, r.Client, latest, func() {
		latest.Status.Phase = phase
		latest.Status.JobRef = build.Name + "-kaniko"
		latest.Status.LogRef = build.Status.LogRef
		if latest.Status.LogRef == "" {
			latest.Status.LogRef = "pod-log://" + build.Name + "-kaniko/kaniko"
		}
		latest.Status.SourceRevision = build.Status.SourceRevision
		latest.Status.CIState = build.Status.CIState
		latest.Status.CIChecks = append([]string(nil), build.Status.CIChecks[:min(len(build.Status.CIChecks), geassv1alpha1.MaxCIChecks)]...)
		if latest.Status.Image == "" {
			latest.Status.Image = build.Spec.Registry
			if latest.Status.Image == "" {
				latest.Status.Image = "registry.geass.dev/" + build.Spec.Project + "/" + build.Spec.App + ":" + buildRevision(build)
			}
		}
		if started != nil && latest.Status.StartedAt == nil {
			latest.Status.StartedAt = started
		}
		if failure != nil {
			latest.Status.FailureReason = failure.Error()
		}
		if retryCount >= 0 {
			latest.Status.RetryCount = retryCount
		}
		if phase == geassv1alpha1.GeassBuildSucceeded {
			if imageDigest != "" {
				latest.Status.ImageDigest = imageDigest
			}
			if latest.Status.ImageDigest == "" {
				latest.Status.Phase = geassv1alpha1.GeassBuildFailed
				latest.Status.FailureReason = "Kaniko completed without an immutable image digest"
			}
			// Once: moving it on every pass made a finished build look different each time it was looked at.
			if latest.Status.CompletedAt == nil {
				latest.Status.CompletedAt = ptr(metav1.Now())
			}
		}
	})
	return err
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
		logData, err = r.Kube.CoreV1().Pods(build.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: "kaniko"}).Do(ctx).Raw()
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
			sha, err := r.githubRequest(ctx, token, build, "/commits/"+url.PathEscape(build.Spec.Branch), nil)
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
	checks, err := r.githubRequest(ctx, token, build, "/commits/"+url.PathEscape(build.Status.SourceRevision)+"/check-runs", nil)
	if err != nil {
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

func (r *GeassBuildReconciler) githubRequest(ctx context.Context, token string, build *geassv1alpha1.GeassBuild, path string, body io.Reader) ([]byte, error) {
	repo := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(build.Spec.Repository, "https://github.com/"), "http://github.com/"), ".git")
	httpClient := r.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/"+repo+path, body)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/vnd.github+json")
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
		return nil, fmt.Errorf("GitHub API returned %s", response.Status)
	}
	return data, nil
}

func (r *GeassBuildReconciler) digestFromBuildPod(ctx context.Context, build *geassv1alpha1.GeassBuild) string {
	if r.Kube == nil {
		return ""
	}
	pods, err := r.Kube.CoreV1().Pods(build.Namespace).List(ctx, metav1.ListOptions{LabelSelector: "job-name=" + build.Name + "-kaniko"})
	if err != nil {
		return ""
	}
	for _, pod := range pods.Items {
		data, err := r.Kube.CoreV1().Pods(build.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: "kaniko"}).Do(ctx).Raw()
		if err != nil {
			continue
		}
		for field := range strings.FieldsSeq(string(data)) {
			if strings.HasPrefix(field, "sha256:") {
				return strings.TrimRight(field, ",")
			}
		}
	}
	return ""
}

func (r *GeassBuildReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).For(&geassv1alpha1.GeassBuild{}).Owns(&batchv1.Job{}).Named("geassbuild").Complete(r)
}
