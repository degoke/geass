package dashboard

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

type clusterCapacity struct {
	Known                  bool   `json:"known"`
	CPUAllocatable         string `json:"cpuAllocatable"`
	CPURequested           string `json:"cpuRequested"`
	CPUAvailable           string `json:"cpuAvailable"`
	MemoryAllocatable      string `json:"memoryAllocatable"`
	MemoryRequested        string `json:"memoryRequested"`
	MemoryAvailable        string `json:"memoryAvailable"`
	CPUAllocatableMillis   int64  `json:"cpuAllocatableMillis"`
	CPURequestedMillis     int64  `json:"cpuRequestedMillis"`
	CPUAvailableMillis     int64  `json:"cpuAvailableMillis"`
	MemoryAllocatableBytes int64  `json:"memoryAllocatableBytes"`
	MemoryRequestedBytes   int64  `json:"memoryRequestedBytes"`
	MemoryAvailableBytes   int64  `json:"memoryAvailableBytes"`
	// MemoryCeiling is the most memory the running services could use between them, which is more than they
	// reserve when some of them may use more than that. It is not worked out for a part of the cluster.
	MemoryCeiling          string          `json:"memoryCeiling,omitempty"`
	MemoryCeilingBytes     int64           `json:"memoryCeilingBytes,omitempty"`
	LargestNodeCPUMillis   int64           `json:"largestNodeCpuMillis"`
	LargestNodeMemoryBytes int64           `json:"largestNodeMemoryBytes"`
	HealthyNodes           int             `json:"healthyNodes"`
	SchedulableNodes       int             `json:"schedulableNodes"`
	Nodes                  []capacityNode  `json:"nodes"`
	Issues                 []capacityIssue `json:"issues"`
	// Where names the part of the cluster these numbers cover ("the us region"); empty for the whole
	// cluster as a general workload sees it.
	Where       string                               `json:"where,omitempty"`
	Regions     []regionInfo                         `json:"regions,omitempty"`
	Tags        []tagInfo                            `json:"tags,omitempty"`
	Estimates   map[string]platform.WorkloadEstimate `json:"estimates"`
	CPUSizes    []platform.SizeOption                `json:"cpuSizes"`
	MemorySizes []platform.SizeOption                `json:"memorySizes"`
	Message     string                               `json:"message,omitempty"`
}

type capacityNode struct {
	Name              string   `json:"name"`
	Role              string   `json:"role"`
	Ready             bool     `json:"ready"`
	Schedulable       bool     `json:"schedulable"`
	CPUAllocatable    string   `json:"cpuAllocatable"`
	MemoryAllocatable string   `json:"memoryAllocatable"`
	Pressure          []string `json:"pressure"`
	// K3sVersion is the version the node's kubelet reports, which for K3s is the K3s release.
	K3sVersion string `json:"k3sVersion,omitempty"`
	// Region, Zone and Tags are where the node sits and what it is for.
	Region string                       `json:"region,omitempty"`
	Zone   string                       `json:"zone,omitempty"`
	Tags   []geassv1alpha1.GeassNodeTag `json:"tags,omitempty"`
	// Pool is the pool the node belongs to; Ephemeral says it must not hold data.
	Pool      string `json:"pool,omitempty"`
	Ephemeral bool   `json:"ephemeral,omitempty"`
}

type capacityIssue struct {
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

func (s *Server) clusterCapacity(ctx context.Context) clusterCapacity {
	return s.clusterCapacityFor(ctx, platform.Placement{})
}

// clusterCapacityFor is the capacity available to a workload that may only run on some nodes.
func (s *Server) clusterCapacityFor(ctx context.Context, placement platform.Placement) clusterCapacity {
	snapshot := clusterCapacity{Where: placement.Describe(), Estimates: platform.WorkloadEstimates(), CPUSizes: platform.CPUSizes(), MemorySizes: platform.MemorySizes(), Message: "Node capacity is unavailable until the cluster reports schedulable nodes"}
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
	capacity := platform.ComputeClusterSnapshotFor(nodes.Items, pods.Items, placement)
	snapshot.Regions, snapshot.Tags = regionsAndTags(nodes.Items)
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
		if snapshot.Where != "" {
			snapshot.Message = "There is no ready node in " + snapshot.Where + " yet"
		}
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
	if placement.IsZero() {
		snapshot.MemoryCeilingBytes = platform.MemoryCeilingOfPods(pods.Items)
		snapshot.MemoryCeiling = platform.FormatMemoryBytes(snapshot.MemoryCeilingBytes)
		if snapshot.MemoryCeilingBytes > snapshot.MemoryAllocatableBytes {
			// Nothing is refused for it: a service reserves what it needs, and may use more only while there is room.
			// It is said so that the servers running short, and the services that use more than they reserve being the
			// first to restart, is not a surprise.
			snapshot.Issues = append(snapshot.Issues, capacityIssue{Severity: "warning", Message: fmt.Sprintf(
				"Services may use up to %s of memory between them and the servers have %s. If they all do at once, those using more than they reserved restart first",
				snapshot.MemoryCeiling, snapshot.MemoryAllocatable)})
		}
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
		K3sVersion:        node.Status.NodeInfo.KubeletVersion,
	}
	if _, ok := node.Labels["node-role.kubernetes.io/control-plane"]; ok {
		entry.Role = "control-plane"
	}
	if _, ok := node.Labels["node-role.kubernetes.io/master"]; ok {
		entry.Role = "control-plane"
	}
	placement := platform.NodePlacementOf(&node)
	entry.Region, entry.Zone, entry.Tags = placement.Region, placement.Zone, placement.Tags
	entry.Pool, entry.Ephemeral = node.Labels[platform.LabelPool], platform.IsEphemeralNode(&node)
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
		Where:                  c.Where,
	}.Fits(est)
}
