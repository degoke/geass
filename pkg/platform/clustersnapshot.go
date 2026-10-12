package platform

import (
	corev1 "k8s.io/api/core/v1"
)

// ClusterSnapshot summarizes schedulable node capacity and current pod requests.
type ClusterSnapshot struct {
	Known                  bool
	CPUAllocatableMillis   int64
	CPURequestedMillis     int64
	CPUAvailableMillis     int64
	MemoryAllocatableBytes int64
	MemoryRequestedBytes   int64
	MemoryAvailableBytes   int64
	LargestNodeCPUMillis   int64
	LargestNodeMemoryBytes int64
	SchedulableNodes       int
	// ReservedNodes counts nodes that would take the workload but are kept for other work.
	ReservedNodes   int
	CPUAvailable    string
	MemoryAvailable string
	// Where names the part of the cluster the snapshot covers ("the us region"). It is empty when
	// the snapshot covers every node a general workload can use.
	Where string
}

// ComputeClusterSnapshot derives the capacity available to a workload that may run anywhere.
func ComputeClusterSnapshot(nodes []corev1.Node, pods []corev1.Pod) ClusterSnapshot {
	return ComputeClusterSnapshotFor(nodes, pods, Placement{})
}

// ComputeClusterSnapshotFor derives the capacity available to a workload with the given placement. Only
// nodes the workload can be scheduled on count, and only the requests of pods that are, or could be,
// running on those nodes, so a reserved node's capacity is not offered to general work and its
// reservations are not charged against it.
func ComputeClusterSnapshotFor(nodes []corev1.Node, pods []corev1.Pod, placement Placement) ClusterSnapshot {
	snapshot := ClusterSnapshot{Where: placement.Describe()}
	var eligible []*corev1.Node
	for i := range nodes {
		node := &nodes[i]
		if !placement.Eligible(node) {
			if placement.ReservedAgainst(node) {
				snapshot.ReservedNodes++
			}
			continue
		}
		cpuMillis := QuantityMillis(node.Status.Allocatable[corev1.ResourceCPU])
		memBytes := QuantityBytes(node.Status.Allocatable[corev1.ResourceMemory])
		if cpuMillis == 0 && memBytes == 0 {
			continue
		}
		eligible = append(eligible, node)
		snapshot.SchedulableNodes++
		snapshot.CPUAllocatableMillis += cpuMillis
		snapshot.MemoryAllocatableBytes += memBytes
		if cpuMillis > snapshot.LargestNodeCPUMillis {
			snapshot.LargestNodeCPUMillis = cpuMillis
		}
		if memBytes > snapshot.LargestNodeMemoryBytes {
			snapshot.LargestNodeMemoryBytes = memBytes
		}
	}
	requestedCPU, requestedMem := podRequestedResources(podsOnOrFor(pods, eligible))
	snapshot.CPURequestedMillis = requestedCPU
	snapshot.MemoryRequestedBytes = requestedMem
	snapshot.CPUAvailableMillis = max(snapshot.CPUAllocatableMillis-requestedCPU, 0)
	snapshot.MemoryAvailableBytes = max(snapshot.MemoryAllocatableBytes-requestedMem, 0)
	snapshot.CPUAvailable = FormatCPUMillis(snapshot.CPUAvailableMillis)
	snapshot.MemoryAvailable = FormatMemoryBytes(snapshot.MemoryAvailableBytes)
	if snapshot.SchedulableNodes > 0 {
		snapshot.Known = true
	}
	return snapshot
}

// podsOnOrFor keeps the pods that use the given nodes: those running on one, and those not yet placed
// that could be.
func podsOnOrFor(pods []corev1.Pod, nodes []*corev1.Node) []corev1.Pod {
	byName := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		byName[node.Name] = true
	}
	kept := make([]corev1.Pod, 0, len(pods))
	for i := range pods {
		pod := &pods[i]
		if pod.Spec.NodeName != "" {
			if byName[pod.Spec.NodeName] {
				kept = append(kept, *pod)
			}
			continue
		}
		if podCouldRunOnAny(pod, nodes) {
			kept = append(kept, *pod)
		}
	}
	return kept
}

// podCouldRunOnAny reports whether a pod that has no node yet could be placed on one of the nodes,
// judging by its node selector and tolerations only.
func podCouldRunOnAny(pod *corev1.Pod, nodes []*corev1.Node) bool {
	for _, node := range nodes {
		if !selectorMatches(pod.Spec.NodeSelector, node.Labels) {
			continue
		}
		if podToleratesTaints(pod, node.Spec.Taints) {
			return true
		}
	}
	return false
}

func selectorMatches(selector, labels map[string]string) bool {
	for key, value := range selector {
		if labels[key] != value {
			return false
		}
	}
	return true
}

func podToleratesTaints(pod *corev1.Pod, taints []corev1.Taint) bool {
	for _, taint := range taints {
		if taint.Effect != corev1.TaintEffectNoSchedule && taint.Effect != corev1.TaintEffectNoExecute {
			continue
		}
		tolerated := false
		for _, toleration := range pod.Spec.Tolerations {
			if tolerationMatches(toleration, taint) {
				tolerated = true
				break
			}
		}
		if !tolerated {
			return false
		}
	}
	return true
}

func tolerationMatches(toleration corev1.Toleration, taint corev1.Taint) bool {
	if toleration.Effect != "" && toleration.Effect != taint.Effect {
		return false
	}
	if toleration.Key != "" && toleration.Key != taint.Key {
		return false
	}
	if toleration.Operator == corev1.TolerationOpExists {
		return true
	}
	return toleration.Value == taint.Value
}

func (c ClusterSnapshot) Fits(est WorkloadEstimate) (bool, string) {
	if est.CPUMillis == 0 && est.MemoryBytes == 0 {
		return true, ""
	}
	if !c.Known {
		if c.ReservedNodes > 0 {
			return false, ReservedNodesMessage(est, c.Where, c.ReservedNodes)
		}
		if c.Where != "" {
			return false, NoNodesInPlacementMessage(est, c.Where)
		}
		return false, UnknownCapacityMessage(est)
	}
	if est.PerCPUMillis > c.LargestNodeCPUMillis || est.PerMemBytes > c.LargestNodeMemoryBytes {
		if c.Where != "" {
			return false, NodeTooSmallInPlacementMessage(est, c.Where)
		}
		return false, NodeTooSmallMessage(est)
	}
	if est.CPUMillis > c.CPUAvailableMillis || est.MemoryBytes > c.MemoryAvailableBytes {
		if c.Where != "" {
			return false, InsufficientCapacityInPlacementMessage(est, c.Where, c.CPUAvailable, c.MemoryAvailable)
		}
		return false, InsufficientCapacityMessage(est, c.CPUAvailable, c.MemoryAvailable)
	}
	return true, ""
}

func podRequestedResources(pods []corev1.Pod) (cpuMillis, memoryBytes int64) {
	for i := range pods {
		pod := &pods[i]
		if !pod.DeletionTimestamp.IsZero() || pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}
		cpu, memory := PodRequests(pod)
		cpuMillis += cpu
		memoryBytes += memory
	}
	return cpuMillis, memoryBytes
}

// PodRequests is the CPU and memory a pod asks the scheduler for: its containers' requests plus the
// runtime's overhead. Init containers count in full, which slightly overstates a pod whose init
// containers finish, but it is the same figure the capacity gate uses.
func PodRequests(pod *corev1.Pod) (cpuMillis, memoryBytes int64) {
	cpuMillis = containerRequestedCPU(pod.Spec.InitContainers) + containerRequestedCPU(pod.Spec.Containers)
	memoryBytes = containerRequestedMemory(pod.Spec.InitContainers) + containerRequestedMemory(pod.Spec.Containers)
	if pod.Spec.Overhead != nil {
		cpuMillis += QuantityMillis(pod.Spec.Overhead[corev1.ResourceCPU])
		memoryBytes += QuantityBytes(pod.Spec.Overhead[corev1.ResourceMemory])
	}
	return cpuMillis, memoryBytes
}

func containerRequestedCPU(containers []corev1.Container) int64 {
	var total int64
	for _, container := range containers {
		total += QuantityMillis(container.Resources.Requests[corev1.ResourceCPU])
	}
	return total
}

func containerRequestedMemory(containers []corev1.Container) int64 {
	var total int64
	for _, container := range containers {
		total += QuantityBytes(container.Resources.Requests[corev1.ResourceMemory])
	}
	return total
}
