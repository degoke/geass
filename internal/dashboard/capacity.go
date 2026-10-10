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
	capacity := platform.ComputeClusterSnapshot(nodes.Items, pods.Items)
	for _, node := range nodes.Items {
		entry := describeCapacityNode(node)
		snapshot.Nodes = append(snapshot.Nodes, entry)
		if entry.Ready {
			snapshot.HealthyNodes++
		}
		for _, pressure := range entry.Pressure {
			snapshot.Issues = append(snapshot.Issues, capacityIssue{Severity: "warning", Message: fmt.Sprintf("Node %s reports %s", entry.Name, pressure)})
		}
	}
	snapshot.SchedulableNodes = capacity.SchedulableNodes
	snapshot.CPUAllocatableMillis = capacity.CPUAllocatableMillis
	snapshot.CPURequestedMillis = capacity.CPURequestedMillis
	snapshot.CPUAvailableMillis = capacity.CPUAvailableMillis
	snapshot.MemoryAllocatableBytes = capacity.MemoryAllocatableBytes
	snapshot.MemoryRequestedBytes = capacity.MemoryRequestedBytes
	snapshot.MemoryAvailableBytes = capacity.MemoryAvailableBytes
	snapshot.LargestNodeCPUMillis = capacity.LargestNodeCPUMillis
	snapshot.LargestNodeMemoryBytes = capacity.LargestNodeMemoryBytes
	snapshot.CPUAllocatable = platform.FormatCPUMillis(snapshot.CPUAllocatableMillis)
	snapshot.CPURequested = platform.FormatCPUMillis(snapshot.CPURequestedMillis)
	snapshot.CPUAvailable = capacity.CPUAvailable
	snapshot.MemoryAllocatable = platform.FormatMemoryBytes(snapshot.MemoryAllocatableBytes)
	snapshot.MemoryRequested = platform.FormatMemoryBytes(snapshot.MemoryRequestedBytes)
	snapshot.MemoryAvailable = capacity.MemoryAvailable
	if snapshot.SchedulableNodes == 0 {
		return snapshot
	}
	snapshot.Known = capacity.Known
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

func (c clusterCapacity) Fits(est platform.WorkloadEstimate) (bool, string) {
	return platform.ClusterSnapshot{
		Known:                  c.Known,
		CPUAvailableMillis:     c.CPUAvailableMillis,
		MemoryAvailableBytes:   c.MemoryAvailableBytes,
		LargestNodeCPUMillis:   c.LargestNodeCPUMillis,
		LargestNodeMemoryBytes: c.LargestNodeMemoryBytes,
		CPUAvailable:           c.CPUAvailable,
		MemoryAvailable:        c.MemoryAvailable,
	}.Fits(est)
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
