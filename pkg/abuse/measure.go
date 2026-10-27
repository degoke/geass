package abuse

import (
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

// PodUse is what the metrics server says one pod uses.
type PodUse struct {
	// CPUMilli is the CPU of all its containers together, in thousandths of a core, averaged over the metrics server's
	// own window (15 seconds on K3s by default).
	CPUMilli int64
}

// Usage is what each pod uses, by namespace and name.
type Usage map[types.NamespacedName]PodUse

// Why a service could not be measured.
const (
	whyNoDeployment = "the service has no Deployment"
	whyNoCopies     = "the service has no copies running"
	whyChanging     = "the service is rolling out or changing its number of copies"
	whySettling     = "a copy of the service is starting, or was restarted, less than three minutes ago"
	whyNoUse        = "the metrics server has not reported what a copy uses"
	whyUnbounded    = "a container of the service has neither a CPU limit nor a request, so its use is a share of nothing"
)

// Measure takes a sample of a service from its Deployment, the pods it has now and what the metrics server says they use.
// It measures only a service that is settled: every copy it asks for is there, up to date, ready, and has been running for
// SettleTime, so that a rollout, a restart, a change in the number of copies or a wake from zero is never counted against
// it. The share is what all the copies use over what they are allowed together: each container's CPU limit, or its request
// when it has no limit.
func Measure(now time.Time, deploy *appsv1.Deployment, pods []corev1.Pod, usage Usage) Sample {
	sample := Sample{At: now}
	if deploy == nil || !deploy.DeletionTimestamp.IsZero() {
		sample.Why = whyNoDeployment
		return sample
	}
	wanted := int32(1)
	if deploy.Spec.Replicas != nil {
		wanted = *deploy.Spec.Replicas
	}
	if wanted == 0 {
		sample.Why = whyNoCopies
		return sample
	}
	status := deploy.Status
	if status.ObservedGeneration < deploy.Generation || status.Replicas != wanted || status.UpdatedReplicas != wanted || status.ReadyReplicas != wanted {
		sample.Why = whyChanging
		return sample
	}
	var live []corev1.Pod
	for i := range pods {
		if pods[i].DeletionTimestamp.IsZero() && pods[i].Status.Phase == corev1.PodRunning {
			live = append(live, pods[i])
		}
	}
	if len(live) == 0 {
		sample.Why = whyNoCopies
		return sample
	}
	if int32(len(live)) != wanted {
		sample.Why = whyChanging
		return sample
	}
	fingerprints := make([]string, 0, len(live))
	for i := range live {
		pod := &live[i]
		if !settled(pod, now) {
			sample.Why = whySettling
			return sample
		}
		use, ok := usage[types.NamespacedName{Namespace: pod.Namespace, Name: pod.Name}]
		if !ok {
			sample.Why = whyNoUse
			return sample
		}
		allowed, bounded := allowedMilli(pod)
		if !bounded {
			sample.Why = whyUnbounded
			return sample
		}
		sample.UsedMilli += use.CPUMilli
		sample.AllowedMilli += allowed
		fingerprints = append(fingerprints, string(pod.UID)+":"+strconv.Itoa(int(restarts(pod))))
	}
	slices.Sort(fingerprints)
	sum := sha256.Sum256([]byte(strings.Join(fingerprints, ",")))
	sample.Pods = hex.EncodeToString(sum[:8])
	sample.Measured = true
	return sample
}

// settled reports whether a pod is ready and has been running, and each of its containers since it last started, for
// SettleTime.
func settled(pod *corev1.Pod, now time.Time) bool {
	ready := false
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			ready = condition.Status == corev1.ConditionTrue
		}
	}
	if !ready || pod.Status.StartTime == nil || now.Sub(pod.Status.StartTime.Time) < SettleTime {
		return false
	}
	if len(pod.Status.ContainerStatuses) == 0 {
		return false
	}
	for _, container := range pod.Status.ContainerStatuses {
		running := container.State.Running
		if running == nil || now.Sub(running.StartedAt.Time) < SettleTime {
			return false
		}
	}
	return true
}

func restarts(pod *corev1.Pod) int32 {
	var total int32
	for _, container := range pod.Status.ContainerStatuses {
		total += container.RestartCount
	}
	return total
}

// allowedMilli is the CPU a pod's containers are allowed together: each one's limit, or its request without one. The
// containers that run beside the others for the pod's life (init containers that restart always, as sidecars do) count as
// well, because the metrics server counts what they use. bounded is false when a container has neither.
func allowedMilli(pod *corev1.Pod) (allowed int64, bounded bool) {
	containers := slices.Clone(pod.Spec.Containers)
	for _, init := range pod.Spec.InitContainers {
		if init.RestartPolicy != nil && *init.RestartPolicy == corev1.ContainerRestartPolicyAlways {
			containers = append(containers, init)
		}
	}
	for _, container := range containers {
		limit := container.Resources.Limits.Cpu()
		if limit.IsZero() {
			limit = container.Resources.Requests.Cpu()
		}
		if limit.IsZero() {
			return 0, false
		}
		allowed += limit.MilliValue()
	}
	return allowed, len(containers) > 0
}
