package platform

import (
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	// ConditionOutOfMemory is on a service when one of its copies was stopped for using more memory than it could
	// have a short while ago. It is not there otherwise.
	ConditionOutOfMemory = "OutOfMemory"

	// OOMKilledReason is what Kubernetes calls the end of a container that the kernel stopped for memory.
	OOMKilledReason = "OOMKilled"

	// MessageOutOfMemory starts what a service says when a copy of it was stopped for memory. It is not a failure to
	// provision: a copy that is stopped and started again is what a crash loop is, and a service that said "failed"
	// for it would be paused until someone edited it, when the cause may pass of itself.
	MessageOutOfMemory = "Ran out of memory"

	// OOMKillWindow is how long after a container was stopped for memory that its service goes on saying so. A
	// container remembers its last end for as long as it lives, so without a limit a single kill would be in the
	// status for days.
	OOMKillWindow = time.Hour
)

// OOMKill is a container that the kernel stopped for using more memory than it could have. The kernel does that
// for two reasons that Kubernetes does not tell apart: the container went over its own limit, or the server ran
// out of memory while the container was using more than it had reserved.
type OOMKill struct {
	Pod       string
	Container string
	// At is when the container was stopped.
	At time.Time
	// Reserved and Limit are the memory the container asked the scheduler for, and the most it may use.
	Reserved resource.Quantity
	Limit    resource.Quantity
}

// Bursting reports whether the container may use more memory than it reserved, which is what makes it the first to
// be stopped when its server runs short.
func (k OOMKill) Bursting() bool {
	return !k.Reserved.IsZero() && k.Limit.Cmp(k.Reserved) > 0
}

// Message says what happened, and what to do about it, in the words of the dashboard.
func (k OOMKill) Message() string {
	if k.Bursting() {
		return fmt.Sprintf(MessageOutOfMemory+": a copy was stopped for using more than it could have. It reserves %s and may use up to %s, and when a server runs short the first copies it stops are those using more than they reserve. Reserve more memory, or let it use more",
			MemoryLabel(k.Reserved), MemoryLabel(k.Limit))
	}
	if k.Limit.IsZero() {
		return MessageOutOfMemory + ": a copy was stopped for using more than the server could give it. Give it a memory limit it can keep to, or reserve more"
	}
	return fmt.Sprintf(MessageOutOfMemory+": a copy was stopped for going over its %s. Give it more memory", MemoryLabel(k.Limit))
}

// MemoryLabel is an amount of memory as the dashboard writes it: the label of the size it is, or the amount.
func MemoryLabel(quantity resource.Quantity) string {
	for _, option := range MemorySizes() {
		if size := resource.MustParse(option.Value); size.Cmp(quantity) == 0 {
			return option.Label
		}
	}
	return FormatMemoryBytes(quantity.Value())
}

// PodOOMKills lists the times a pod's containers were stopped for memory that the pod still remembers: the one that
// ended a running container a moment ago, and the one before it. A container that has been stopped for memory more
// than once is remembered by its latest.
func PodOOMKills(pod *corev1.Pod) []OOMKill {
	var kills []OOMKill
	for i := range pod.Status.ContainerStatuses {
		status := &pod.Status.ContainerStatuses[i]
		var latest *corev1.ContainerStateTerminated
		for _, terminated := range []*corev1.ContainerStateTerminated{status.State.Terminated, status.LastTerminationState.Terminated} {
			if terminated == nil || terminated.Reason != OOMKilledReason {
				continue
			}
			if latest == nil || terminationTime(terminated).After(terminationTime(latest)) {
				latest = terminated
			}
		}
		if latest == nil {
			continue
		}
		kill := OOMKill{Pod: pod.Name, Container: status.Name, At: terminationTime(latest)}
		for _, container := range pod.Spec.Containers {
			if container.Name == status.Name {
				kill.Reserved = container.Resources.Requests[corev1.ResourceMemory]
				kill.Limit = container.Resources.Limits[corev1.ResourceMemory]
			}
		}
		kills = append(kills, kill)
	}
	return kills
}

// LastOOMKill is the time of the latest time one of the pod's containers was stopped for memory, and zero when none was.
func LastOOMKill(pod *corev1.Pod) time.Time {
	var latest time.Time
	for _, kill := range PodOOMKills(pod) {
		if kill.At.After(latest) {
			latest = kill.At
		}
	}
	return latest
}

// RecentOOMKill is the latest time a container of the pods was stopped for memory, if that was within the window before
// now. Pods that are going away are not asked: a copy that a rollout is replacing has said what it had to.
func RecentOOMKill(pods []corev1.Pod, now time.Time, window time.Duration) (OOMKill, bool) {
	var found OOMKill
	var ok bool
	for i := range pods {
		pod := &pods[i]
		if !pod.DeletionTimestamp.IsZero() {
			continue
		}
		for _, kill := range PodOOMKills(pod) {
			if kill.At.IsZero() || now.Sub(kill.At) > window {
				continue
			}
			if !ok || kill.At.After(found.At) {
				found, ok = kill, true
			}
		}
	}
	return found, ok
}

// terminationTime is when a container ended. A container that was stopped without a recorded end counts as ended when
// it started.
func terminationTime(state *corev1.ContainerStateTerminated) time.Time {
	if !state.FinishedAt.IsZero() {
		return state.FinishedAt.Time
	}
	return state.StartedAt.Time
}
