package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/helmchart"
	"github.com/degoke/geass/pkg/platform"
)

// What a person sees of an add-on is in status.addons: whether it is on, whether it is up, and why it is not. It is worked out
// from the Helm chart each add-on installs and the Job that installs it, which nobody has to look at.

// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;delete

const (
	// addonFailedAttempts is how many times an install job may fail before the add-on is called Failed. K3s's Helm
	// controller lets a job try again for a long time, so one failure (a chart repository that was not reachable for a
	// moment) is not yet a failure to report.
	addonFailedAttempts = 3

	// addonProgressRetry is how soon to look again at an add-on that is on and not up. It is the cluster's own wait while it is
	// not ready, so that looking at the add-ons never makes the cluster look sooner than it would.
	addonProgressRetry = platform.RequeueAfterDefault
	// addonFailedRetry is how soon to look again at one that is failing, which a person may be putting right.
	addonFailedRetry = time.Minute
)

// chartProgress is how far one chart of an add-on has got.
type chartProgress struct {
	state     geassv1alpha1.AddonState
	reason    string
	message   string
	installed bool
}

// reconcileAddonRetry deletes the install jobs of the add-on that the cluster was asked to try again, and removes the request.
// K3s's Helm controller makes a new job for a chart whose job is gone, which is what a person did by hand with kubectl.
func (r *GeassClusterReconciler) reconcileAddonRetry(ctx context.Context, cluster *geassv1alpha1.GeassCluster) {
	name, asked := cluster.Annotations[platform.AnnotationRetryAddon]
	if !asked || !cluster.DeletionTimestamp.IsZero() {
		return
	}
	log := logf.FromContext(ctx)
	if addon, known := platform.FindAddon(name); known && addon.Enabled(cluster.Spec.Addons) {
		for _, ch := range addon.Charts {
			if err := r.deleteInstallJob(ctx, ch); err != nil {
				log.Error(err, "Failed to delete the install job of an add-on to try it again", "addon", name, "chart", ch.Name)
				return // the request stays, and the next pass tries again
			}
		}
		log.Info("Deleted the install jobs of an add-on to try it again", "addon", name)
	}
	latest := &geassv1alpha1.GeassCluster{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(cluster), latest); err != nil {
		return
	}
	base := latest.DeepCopy()
	delete(latest.Annotations, platform.AnnotationRetryAddon)
	if err := r.Patch(ctx, latest, client.MergeFrom(base)); err != nil && !apierrors.IsNotFound(err) {
		log.Error(err, "Failed to remove the request to try an add-on again", "addon", name)
	}
}

// deleteInstallJob removes the job that installs a chart, if there is one. Its pods go with it.
func (r *GeassClusterReconciler) deleteInstallJob(ctx context.Context, ch platform.AddonChart) error {
	chart, err := helmchart.Get(ctx, r.Client, ch.Name)
	if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if chart.Status.JobName == "" {
		return nil
	}
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: chart.Status.JobName, Namespace: chart.Namespace}}
	if err := r.Delete(ctx, job, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

// reconcileAddonStatus records how each add-on is doing. It is background work and returns when to look again: soon while an
// add-on that is on is not up, and never when every one is settled.
func (r *GeassClusterReconciler) reconcileAddonStatus(ctx context.Context, cluster *geassv1alpha1.GeassCluster) time.Duration {
	if !cluster.DeletionTimestamp.IsZero() {
		return 0
	}
	log := logf.FromContext(ctx)
	statuses := make([]geassv1alpha1.GeassClusterAddonStatus, 0, len(platform.Addons))
	var retry time.Duration
	for _, addon := range platform.Addons {
		status, err := r.addonProgress(ctx, addon, cluster.Spec.Addons)
		if err != nil {
			log.Error(err, "Failed to work out how an add-on is doing", "addon", addon.Name)
			return addonProgressRetry
		}
		statuses = append(statuses, status)
		switch status.State {
		case geassv1alpha1.AddonStateInstalling:
			retry = shortestRequeue(retry, addonProgressRetry)
		case geassv1alpha1.AddonStateFailed:
			retry = shortestRequeue(retry, addonFailedRetry)
		}
	}
	latest := &geassv1alpha1.GeassCluster{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(cluster), latest); err != nil {
		return retry
	}
	if _, err := platform.PatchStatus(ctx, r.Client, latest, func() { latest.Status.Addons = statuses }); err != nil {
		log.Error(err, "Failed to record how the add-ons are doing")
		return addonProgressRetry
	}
	return retry
}

// addonProgress is how one add-on is doing: the worst of what its charts are doing.
func (r *GeassClusterReconciler) addonProgress(ctx context.Context, addon platform.Addon, spec geassv1alpha1.GeassClusterAddonsSpec) (geassv1alpha1.GeassClusterAddonStatus, error) {
	status := geassv1alpha1.GeassClusterAddonStatus{Name: addon.Name, State: geassv1alpha1.AddonStateReady}
	enabled := addon.Enabled(spec)
	var worst *chartProgress
	var message []string
	for _, ch := range addon.Charts {
		progress, err := r.chartProgress(ctx, ch)
		if err != nil {
			return status, err
		}
		status.Installed = status.Installed || progress.installed
		if progress.state == geassv1alpha1.AddonStateReady {
			continue
		}
		if worst == nil || rank(progress.state) > rank(worst.state) {
			worst = &progress
		}
		message = append(message, ch.Label+": "+progress.message)
	}
	switch {
	case !enabled && status.Installed && addon.KeptWhenOff:
		status.State = geassv1alpha1.AddonStateOff
		status.Message = "Turned off. Still installed."
	case !enabled && status.Installed:
		status.State = geassv1alpha1.AddonStateOff
		status.Message = "Turned off. Removing it."
	case !enabled:
		status.State = geassv1alpha1.AddonStateOff
	case worst != nil:
		status.State, status.Reason = worst.state, worst.reason
		status.Message = worst.message
		if len(addon.Charts) > 1 {
			status.Message = strings.Join(message, " ")
		}
	}
	if enabled && status.State == geassv1alpha1.AddonStateReady && addon.Name == platform.AddonLonghorn {
		// The chart is up, but Longhorn is not usable until its driver is registered and it runs on every node.
		return r.longhornProgress(ctx, status)
	}
	return status, nil
}

// rank orders the states that a chart that is not Ready can be in, worst last.
func rank(state geassv1alpha1.AddonState) int {
	if state == geassv1alpha1.AddonStateFailed {
		return 2
	}
	return 1
}

// longhornProgress holds Longhorn at Installing until the CSI driver is registered and every node has a ready manager. These
// are the conditions under which the storage classes are made, so a database waits on the same thing.
func (r *GeassClusterReconciler) longhornProgress(ctx context.Context, status geassv1alpha1.GeassClusterAddonStatus) (geassv1alpha1.GeassClusterAddonStatus, error) {
	installed, err := r.longhornDriverInstalled(ctx)
	if err != nil {
		return status, err
	}
	if !installed {
		status.State, status.Message = geassv1alpha1.AddonStateInstalling, "Waiting for the storage driver."
		return status, nil
	}
	_, hosted, err := r.longhornReplicaTarget(ctx)
	if err != nil {
		return status, err
	}
	if !hosted {
		status.State = geassv1alpha1.AddonStateInstalling
		status.Message = "Waiting for Longhorn on every node. A node without the iSCSI initiator never gets it."
	}
	return status, nil
}

// chartProgress is how far the install of one chart has got.
func (r *GeassClusterReconciler) chartProgress(ctx context.Context, ch platform.AddonChart) (chartProgress, error) {
	chart, err := helmchart.Get(ctx, r.Client, ch.Name)
	switch {
	case meta.IsNoMatchError(err):
		return chartProgress{state: geassv1alpha1.AddonStateFailed, reason: platform.AddonReasonNoHelmController,
			message: "This cluster has no Helm controller."}, nil
	case apierrors.IsNotFound(err):
		return chartProgress{state: geassv1alpha1.AddonStateInstalling, message: "Waiting to start the install."}, nil
	case err != nil:
		return chartProgress{}, err
	}
	progress := chartProgress{installed: true, state: geassv1alpha1.AddonStateInstalling, message: "Waiting for the install job to start."}
	if chart.Status.JobName == "" {
		return progress, nil
	}
	job := &batchv1.Job{}
	if err := r.Get(ctx, types.NamespacedName{Name: chart.Status.JobName, Namespace: chart.Namespace}, job); apierrors.IsNotFound(err) {
		return progress, nil
	} else if err != nil {
		return chartProgress{}, err
	}
	switch {
	case job.Spec.Suspend != nil && *job.Spec.Suspend:
		progress.state, progress.reason = geassv1alpha1.AddonStateFailed, platform.AddonReasonInstallSuspended
		progress.message = "The install job is suspended."
	case jobFailed(job) != "":
		progress.state, progress.reason = geassv1alpha1.AddonStateFailed, platform.AddonReasonInstallFailed
		progress.message = fmt.Sprintf("The install job failed: %s. Check that the cluster can reach %s.", jobFailed(job), chart.Spec.Repo)
	case job.Status.Succeeded > 0:
		ready, message, err := helmReleaseWorkloadReady(ctx, r.Client, ch.Namespace, ch.Name, 1)
		if err != nil {
			return chartProgress{}, err
		}
		if ready {
			progress.state, progress.message = geassv1alpha1.AddonStateReady, ""
		} else {
			progress.message = "Waiting for its pods to start."
			if message == messageServiceFailed {
				progress.message = "A pod failed to start."
			}
		}
	case job.Status.Failed >= addonFailedAttempts:
		progress.state, progress.reason = geassv1alpha1.AddonStateFailed, platform.AddonReasonInstallFailed
		progress.message = fmt.Sprintf("The install job failed %d times. Check that the cluster can reach %s.", job.Status.Failed, chart.Spec.Repo)
	case job.Status.Failed > 0:
		progress.message = fmt.Sprintf("Install failed %d time. Trying again.", job.Status.Failed)
		if job.Status.Failed > 1 {
			progress.message = fmt.Sprintf("Install failed %d times. Trying again.", job.Status.Failed)
		}
	default:
		progress.message = "Installing."
	}
	return progress, nil
}

// jobFailed is why a job gave up, or "" when it has not.
func jobFailed(job *batchv1.Job) string {
	for _, condition := range job.Status.Conditions {
		if condition.Type == batchv1.JobFailed && condition.Status == corev1.ConditionTrue {
			if condition.Message != "" {
				return strings.TrimSuffix(condition.Message, ".")
			}
			return "it gave up"
		}
	}
	return ""
}
