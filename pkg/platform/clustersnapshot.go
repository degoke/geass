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
	CPUAvailable           string
	MemoryAvailable        string
}

// ComputeClusterSnapshot derives capacity from node allocatable totals and pod requests.
func ComputeClusterSnapshot(nodes []corev1.Node, pods []corev1.Pod) ClusterSnapshot {
	snapshot := ClusterSnapshot{}
	requestedCPU, requestedMem := podRequestedResources(pods)
	snapshot.CPURequestedMillis = requestedCPU
	snapshot.MemoryRequestedBytes = requestedMem

	for _, node := range nodes {
		if !nodeSchedulable(node) {
			continue
		}
		cpuQty := node.Status.Allocatable[corev1.ResourceCPU]
		memQty := node.Status.Allocatable[corev1.ResourceMemory]
		cpuMillis := QuantityMillis(cpuQty)
		memBytes := QuantityBytes(memQty)
		if cpuMillis == 0 && memBytes == 0 {
			continue
		}
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
	snapshot.CPUAvailableMillis = max(snapshot.CPUAllocatableMillis-requestedCPU, 0)
	snapshot.MemoryAvailableBytes = max(snapshot.MemoryAllocatableBytes-requestedMem, 0)
	snapshot.CPUAvailable = FormatCPUMillis(snapshot.CPUAvailableMillis)
	snapshot.MemoryAvailable = FormatMemoryBytes(snapshot.MemoryAvailableBytes)
	if snapshot.SchedulableNodes > 0 {
		snapshot.Known = true
	}
	return snapshot
}

func (c ClusterSnapshot) Fits(est WorkloadEstimate) (bool, string) {
	if est.CPUMillis == 0 && est.MemoryBytes == 0 {
		return true, ""
	}
	if !c.Known {
		return false, UnknownCapacityMessage(est)
	}
	if est.PerCPUMillis > c.LargestNodeCPUMillis || est.PerMemBytes > c.LargestNodeMemoryBytes {
		return false, NodeTooSmallMessage(est)
	}
	if est.CPUMillis > c.CPUAvailableMillis || est.MemoryBytes > c.MemoryAvailableBytes {
		return false, InsufficientCapacityMessage(est, c.CPUAvailable, c.MemoryAvailable)
	}
	return true, ""
}

func nodeSchedulable(node corev1.Node) bool {
	if node.Spec.Unschedulable {
		return false
	}
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady && condition.Status != corev1.ConditionTrue {
			return false
		}
	}
	return true
}

func podRequestedResources(pods []corev1.Pod) (cpuMillis, memoryBytes int64) {
	for _, pod := range pods {
		if !pod.DeletionTimestamp.IsZero() || pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}
		cpuMillis += containerRequestedCPU(pod.Spec.InitContainers) + containerRequestedCPU(pod.Spec.Containers)
		memoryBytes += containerRequestedMemory(pod.Spec.InitContainers) + containerRequestedMemory(pod.Spec.Containers)
		if pod.Spec.Overhead != nil {
			cpuMillis += QuantityMillis(pod.Spec.Overhead[corev1.ResourceCPU])
			memoryBytes += QuantityBytes(pod.Spec.Overhead[corev1.ResourceMemory])
		}
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
