package platform

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	WorkloadService  = "service"
	WorkloadPostgres = "postgres"
	WorkloadMySQL    = "mysql"
	WorkloadSQLite   = "sqlite"
	WorkloadRedis    = "redis"
	WorkloadMinIO    = "minio"
	WorkloadBucket   = "bucket"
	WorkloadLogical  = "logical"
	WorkloadExternal = "external"
)

// WorkloadEstimate is an approximate CPU and memory request for a Geass resource.
type WorkloadEstimate struct {
	Kind         string `json:"kind"`
	Label        string `json:"label"`
	CPU          string `json:"cpu"`
	Memory       string `json:"memory"`
	CPUMillis    int64  `json:"cpuMillis"`
	MemoryBytes  int64  `json:"memoryBytes"`
	Replicas     int32  `json:"replicas"`
	Approximate  bool   `json:"approximate"`
	PerCPU       string `json:"perCpu"`
	PerMemory    string `json:"perMemory"`
	PerCPUMillis int64  `json:"perCpuMillis"`
	PerMemBytes  int64  `json:"perMemoryBytes"`
}

type workloadDefaults struct {
	label    string
	cpuMilli int64
	memBytes int64
	haCount  int32
}

var workloadCatalog = map[string]workloadDefaults{
	WorkloadService:  {label: "service", cpuMilli: 100, memBytes: 128 * 1024 * 1024, haCount: 1},
	WorkloadPostgres: {label: "PostgreSQL database", cpuMilli: 250, memBytes: 512 * 1024 * 1024, haCount: 3},
	WorkloadMySQL:    {label: "MySQL database", cpuMilli: 250, memBytes: 512 * 1024 * 1024, haCount: 3},
	WorkloadSQLite:   {label: "SQLite database", cpuMilli: 100, memBytes: 128 * 1024 * 1024, haCount: 1},
	WorkloadRedis:    {label: "Redis database", cpuMilli: 100, memBytes: 128 * 1024 * 1024, haCount: 3},
	WorkloadMinIO:    {label: "MinIO server", cpuMilli: 250, memBytes: 512 * 1024 * 1024, haCount: 1},
	WorkloadBucket:   {label: "bucket", cpuMilli: 0, memBytes: 0, haCount: 1},
	WorkloadLogical:  {label: "logical database", cpuMilli: 0, memBytes: 0, haCount: 1},
	WorkloadExternal: {label: "external resource", cpuMilli: 0, memBytes: 0, haCount: 1},
}

// DefaultAppResources is the request/limit set applied to services that omit one.
func DefaultAppResources() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("128Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("500m"),
			corev1.ResourceMemory: resource.MustParse("256Mi"),
		},
	}
}

// DefaultSQLiteResources is the request set applied to in-cluster SQLite.
func DefaultSQLiteResources() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("100m"),
			corev1.ResourceMemory: resource.MustParse("128Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("250m"),
			corev1.ResourceMemory: resource.MustParse("256Mi"),
		},
	}
}

// EstimateWorkload returns an approximate CPU and memory request for a create action.
func EstimateWorkload(kind string, ha bool, replicas int32) WorkloadEstimate {
	defaults, ok := workloadCatalog[kind]
	if !ok {
		defaults = workloadCatalog[WorkloadService]
		kind = WorkloadService
	}
	count := int32(1)
	if replicas > 0 {
		count = replicas
	} else if ha && defaults.haCount > 1 {
		count = defaults.haCount
	}
	totalCPU := defaults.cpuMilli * int64(count)
	totalMem := defaults.memBytes * int64(count)
	return WorkloadEstimate{
		Kind:         kind,
		Label:        defaults.label,
		CPU:          FormatCPUMillis(totalCPU),
		Memory:       FormatMemoryBytes(totalMem),
		CPUMillis:    totalCPU,
		MemoryBytes:  totalMem,
		Replicas:     count,
		Approximate:  true,
		PerCPU:       FormatCPUMillis(defaults.cpuMilli),
		PerMemory:    FormatMemoryBytes(defaults.memBytes),
		PerCPUMillis: defaults.cpuMilli,
		PerMemBytes:  defaults.memBytes,
	}
}

// WorkloadEstimates returns the catalog shown in the dashboard create flow.
func WorkloadEstimates() map[string]WorkloadEstimate {
	out := map[string]WorkloadEstimate{}
	for kind := range workloadCatalog {
		out[kind] = EstimateWorkload(kind, false, 1)
	}
	out[WorkloadPostgres+"-ha"] = EstimateWorkload(WorkloadPostgres, true, 0)
	out[WorkloadMySQL+"-ha"] = EstimateWorkload(WorkloadMySQL, true, 0)
	out[WorkloadRedis+"-ha"] = EstimateWorkload(WorkloadRedis, true, 0)
	return out
}

func FormatCPUMillis(millis int64) string {
	if millis <= 0 {
		return "0"
	}
	return resource.NewMilliQuantity(millis, resource.DecimalSI).String()
}

func FormatMemoryBytes(bytes int64) string {
	if bytes <= 0 {
		return "0"
	}
	return resource.NewQuantity(bytes, resource.BinarySI).String()
}

func QuantityMillis(q resource.Quantity) int64 {
	return q.MilliValue()
}

func QuantityBytes(q resource.Quantity) int64 {
	return q.Value()
}

func InsufficientCapacityMessage(est WorkloadEstimate, availableCPU, availableMemory string) string {
	return fmt.Sprintf("This %s needs about %s CPU and %s memory. The cluster currently has %s CPU and %s memory available. Scale up the cluster before creating it", est.Label, est.CPU, est.Memory, availableCPU, availableMemory)
}

func UnknownCapacityMessage(est WorkloadEstimate) string {
	return fmt.Sprintf("Cluster capacity is unknown, so this %s cannot be scheduled yet. Scale up until schedulable nodes report CPU and memory", est.Label)
}

func NodeTooSmallMessage(est WorkloadEstimate) string {
	return fmt.Sprintf("This %s needs about %s CPU and %s memory per instance, but no schedulable node has enough allocatable capacity. Scale up with larger nodes", est.Label, est.PerCPU, est.PerMemory)
}
