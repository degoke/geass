package dashboard

import (
	"context"
	"fmt"
	"net/http"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/degoke/geass/pkg/platform"
)

type clusterCapacity struct {
	Known                  bool                                 `json:"known"`
	CPUAllocatable         string                               `json:"cpuAllocatable"`
	CPURequested           string                               `json:"cpuRequested"`
	CPUAvailable           string                               `json:"cpuAvailable"`
	MemoryAllocatable      string                               `json:"memoryAllocatable"`
	MemoryRequested        string                               `json:"memoryRequested"`
	MemoryAvailable        string                               `json:"memoryAvailable"`
	CPUAllocatableMillis   int64                                `json:"cpuAllocatableMillis"`
	CPURequestedMillis     int64                                `json:"cpuRequestedMillis"`
	CPUAvailableMillis     int64                                `json:"cpuAvailableMillis"`
	MemoryAllocatableBytes int64                                `json:"memoryAllocatableBytes"`
	MemoryRequestedBytes   int64                                `json:"memoryRequestedBytes"`
	MemoryAvailableBytes   int64                                `json:"memoryAvailableBytes"`
	LargestNodeCPUMillis   int64                                `json:"largestNodeCpuMillis"`
	LargestNodeMemoryBytes int64                                `json:"largestNodeMemoryBytes"`
	HealthyNodes           int                                  `json:"healthyNodes"`
	SchedulableNodes       int                                  `json:"schedulableNodes"`
	Nodes                  []capacityNode                       `json:"nodes"`
	Issues                 []capacityIssue                      `json:"issues"`
	Estimates              map[string]platform.WorkloadEstimate `json:"estimates"`
	CPUSizes               []platform.SizeOption                `json:"cpuSizes"`
	MemorySizes            []platform.SizeOption                `json:"memorySizes"`
	Message                string                               `json:"message,omitempty"`
}

type capacityNode struct {
	Name              string   `json:"name"`
	Role              string   `json:"role"`
	Ready             bool     `json:"ready"`
	Schedulable       bool     `json:"schedulable"`
	CPUAllocatable    string   `json:"cpuAllocatable"`
	MemoryAllocatable string   `json:"memoryAllocatable"`
	Pressure          []string `json:"pressure"`
}

type capacityIssue struct {
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

func (s *Server) clusterCapacity(ctx context.Context) clusterCapacity {
	snapshot := clusterCapacity{Estimates: platform.WorkloadEstimates(), CPUSizes: platform.CPUSizes(), MemorySizes: platform.MemorySizes(), Message: "Node capacity is unavailable until the cluster reports schedulable nodes"}
	if s == nil || s.Client == nil {
		return snapshot
	}
	var nodes corev1.NodeList
	if err := s.Client.List(ctx, &nodes); err != nil {
		snapshot.Message = "Could not read cluster nodes"
		return snapshot
	}
	var pods corev1.PodList
	if err := s.Client.List(ctx, &pods); err != nil {
		snapshot.Message = "Could not read cluster pods"
		return snapshot
	}
	requestedCPU, requestedMem := podRequestedResources(pods.Items)
	for _, node := range nodes.Items {
		entry := describeCapacityNode(node)
		snapshot.Nodes = append(snapshot.Nodes, entry)
		if entry.Ready {
			snapshot.HealthyNodes++
		}
		if !entry.Ready || !entry.Schedulable {
			continue
		}
		cpuQty := node.Status.Allocatable[corev1.ResourceCPU]
		memQty := node.Status.Allocatable[corev1.ResourceMemory]
		cpuMillis := platform.QuantityMillis(cpuQty)
		memBytes := platform.QuantityBytes(memQty)
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
		for _, pressure := range entry.Pressure {
			snapshot.Issues = append(snapshot.Issues, capacityIssue{Severity: "warning", Message: fmt.Sprintf("Node %s reports %s", entry.Name, pressure)})
		}
	}
	snapshot.CPURequestedMillis = requestedCPU
	snapshot.MemoryRequestedBytes = requestedMem
	snapshot.CPUAvailableMillis = snapshot.CPUAllocatableMillis - requestedCPU
	if snapshot.CPUAvailableMillis < 0 {
		snapshot.CPUAvailableMillis = 0
	}
	snapshot.MemoryAvailableBytes = snapshot.MemoryAllocatableBytes - requestedMem
	if snapshot.MemoryAvailableBytes < 0 {
		snapshot.MemoryAvailableBytes = 0
	}
	snapshot.CPUAllocatable = platform.FormatCPUMillis(snapshot.CPUAllocatableMillis)
	snapshot.CPURequested = platform.FormatCPUMillis(snapshot.CPURequestedMillis)
	snapshot.CPUAvailable = platform.FormatCPUMillis(snapshot.CPUAvailableMillis)
	snapshot.MemoryAllocatable = platform.FormatMemoryBytes(snapshot.MemoryAllocatableBytes)
	snapshot.MemoryRequested = platform.FormatMemoryBytes(snapshot.MemoryRequestedBytes)
	snapshot.MemoryAvailable = platform.FormatMemoryBytes(snapshot.MemoryAvailableBytes)
	if snapshot.SchedulableNodes == 0 {
		return snapshot
	}
	snapshot.Known = true
	snapshot.Message = ""
	if snapshot.CPUAvailableMillis < 100 {
		snapshot.Issues = append(snapshot.Issues, capacityIssue{Severity: "danger", Message: "Available CPU is too low for a new service. Scale up the cluster before creating in-cluster resources"})
	}
	if snapshot.MemoryAvailableBytes < 128*1024*1024 {
		snapshot.Issues = append(snapshot.Issues, capacityIssue{Severity: "danger", Message: "Available memory is too low for a new service. Scale up the cluster before creating in-cluster resources"})
	}
	return snapshot
}

func describeCapacityNode(node corev1.Node) capacityNode {
	entry := capacityNode{
		Name:              node.Name,
		Role:              "worker",
		Schedulable:       !node.Spec.Unschedulable,
		CPUAllocatable:    quantityString(node.Status.Allocatable[corev1.ResourceCPU]),
		MemoryAllocatable: quantityString(node.Status.Allocatable[corev1.ResourceMemory]),
	}
	if _, ok := node.Labels["node-role.kubernetes.io/control-plane"]; ok {
		entry.Role = "control-plane"
	}
	if _, ok := node.Labels["node-role.kubernetes.io/master"]; ok {
		entry.Role = "control-plane"
	}
	for _, condition := range node.Status.Conditions {
		switch condition.Type {
		case corev1.NodeReady:
			entry.Ready = condition.Status == corev1.ConditionTrue
		case corev1.NodeMemoryPressure, corev1.NodeDiskPressure, corev1.NodePIDPressure, corev1.NodeNetworkUnavailable:
			if condition.Status == corev1.ConditionTrue {
				entry.Pressure = append(entry.Pressure, string(condition.Type))
			}
		}
	}
	if !entry.Schedulable {
		entry.Pressure = append(entry.Pressure, "Unschedulable")
	}
	return entry
}

func quantityString(q resource.Quantity) string {
	if q.IsZero() {
		return "0"
	}
	return q.String()
}

func podRequestedResources(pods []corev1.Pod) (cpuMillis, memoryBytes int64) {
	for _, pod := range pods {
		if !pod.DeletionTimestamp.IsZero() || pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}
		cpuMillis += containerRequestedCPU(pod.Spec.InitContainers) + containerRequestedCPU(pod.Spec.Containers)
		memoryBytes += containerRequestedMemory(pod.Spec.InitContainers) + containerRequestedMemory(pod.Spec.Containers)
		if pod.Spec.Overhead != nil {
			cpuMillis += platform.QuantityMillis(pod.Spec.Overhead[corev1.ResourceCPU])
			memoryBytes += platform.QuantityBytes(pod.Spec.Overhead[corev1.ResourceMemory])
		}
	}
	return cpuMillis, memoryBytes
}

func containerRequestedCPU(containers []corev1.Container) int64 {
	var total int64
	for _, container := range containers {
		total += platform.QuantityMillis(container.Resources.Requests[corev1.ResourceCPU])
	}
	return total
}

func containerRequestedMemory(containers []corev1.Container) int64 {
	var total int64
	for _, container := range containers {
		total += platform.QuantityBytes(container.Resources.Requests[corev1.ResourceMemory])
	}
	return total
}

func (c clusterCapacity) Fits(est platform.WorkloadEstimate) (bool, string) {
	if !c.Known || (est.CPUMillis == 0 && est.MemoryBytes == 0) {
		return true, ""
	}
	if est.PerCPUMillis > c.LargestNodeCPUMillis || est.PerMemBytes > c.LargestNodeMemoryBytes {
		return false, platform.NodeTooSmallMessage(est)
	}
	if est.CPUMillis > c.CPUAvailableMillis || est.MemoryBytes > c.MemoryAvailableBytes {
		return false, platform.InsufficientCapacityMessage(est, c.CPUAvailable, c.MemoryAvailable)
	}
	return true, ""
}

func (s *Server) rejectIfNoCapacity(w http.ResponseWriter, r *http.Request, fallback, kind string, ha bool, replicas int32) bool {
	return s.rejectIfNoCapacityFor(w, r, fallback, platform.EstimateWorkload(kind, ha, replicas))
}

func (s *Server) rejectIfNoCapacityFor(w http.ResponseWriter, r *http.Request, fallback string, est platform.WorkloadEstimate) bool {
	snapshot := s.clusterCapacity(r.Context())
	ok, message := snapshot.Fits(est)
	if ok {
		return false
	}
	redirectFormError(w, r, fallback, message)
	return true
}
