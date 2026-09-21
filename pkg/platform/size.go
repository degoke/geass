package platform

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const DefaultAutoscalingTargetCPU int32 = 70

// SizeOption is a CPU or memory amount shown in the dashboard without
// Kubernetes request/limit wording.
type SizeOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// CPUSizes is the CPU amounts a user can assign to a resource.
func CPUSizes() []SizeOption {
	return []SizeOption{
		{Value: "100m", Label: "0.1 CPU"},
		{Value: "250m", Label: "0.25 CPU"},
		{Value: "500m", Label: "0.5 CPU"},
		{Value: "1", Label: "1 CPU"},
		{Value: "2", Label: "2 CPU"},
	}
}

// MemorySizes is the memory amounts a user can assign to a resource.
func MemorySizes() []SizeOption {
	return []SizeOption{
		{Value: "128Mi", Label: "128 MB"},
		{Value: "256Mi", Label: "256 MB"},
		{Value: "512Mi", Label: "512 MB"},
		{Value: "1Gi", Label: "1 GB"},
		{Value: "2Gi", Label: "2 GB"},
		{Value: "4Gi", Label: "4 GB"},
	}
}

// DefaultDatabaseResources is the CPU and memory assigned to Postgres, MySQL, Redis, and MinIO.
func DefaultDatabaseResources() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("250m"),
			corev1.ResourceMemory: resource.MustParse("512Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("500m"),
			corev1.ResourceMemory: resource.MustParse("1Gi"),
		},
	}
}

// ResourcesFromSize maps a user-facing CPU and memory assignment to Kubernetes
// requests and limits. Limits are twice the assigned amount.
func ResourcesFromSize(cpu, memory string) (corev1.ResourceRequirements, error) {
	cpu = strings.TrimSpace(cpu)
	memory = strings.TrimSpace(memory)
	if cpu == "" && memory == "" {
		return DefaultAppResources(), nil
	}
	if cpu == "" {
		cpu = "100m"
	}
	if memory == "" {
		memory = "128Mi"
	}
	cpuQty, err := resource.ParseQuantity(cpu)
	if err != nil {
		return corev1.ResourceRequirements{}, err
	}
	memQty, err := resource.ParseQuantity(memory)
	if err != nil {
		return corev1.ResourceRequirements{}, err
	}
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{corev1.ResourceCPU: cpuQty, corev1.ResourceMemory: memQty},
		Limits:   corev1.ResourceList{corev1.ResourceCPU: doubledQuantity(cpuQty), corev1.ResourceMemory: doubledQuantity(memQty)},
	}, nil
}

// EstimateFromResources builds a capacity estimate from assigned CPU, memory, and copies.
func EstimateFromResources(label string, res corev1.ResourceRequirements, replicas int32) WorkloadEstimate {
	if replicas < 1 {
		replicas = 1
	}
	perCPU := QuantityMillis(res.Requests[corev1.ResourceCPU])
	perMem := QuantityBytes(res.Requests[corev1.ResourceMemory])
	return WorkloadEstimate{
		Kind:         WorkloadService,
		Label:        label,
		CPU:          FormatCPUMillis(perCPU * int64(replicas)),
		Memory:       FormatMemoryBytes(perMem * int64(replicas)),
		CPUMillis:    perCPU * int64(replicas),
		MemoryBytes:  perMem * int64(replicas),
		Replicas:     replicas,
		Approximate:  false,
		PerCPU:       FormatCPUMillis(perCPU),
		PerMemory:    FormatMemoryBytes(perMem),
		PerCPUMillis: perCPU,
		PerMemBytes:  perMem,
	}
}

// DefaultAutoscalingMax is the upper copy count when autoscaling is enabled.
func DefaultAutoscalingMax(replicas int32) int32 {
	if replicas < 1 {
		replicas = 1
	}
	if replicas >= 3 {
		return replicas * 2
	}
	return 3
}

func doubledQuantity(q resource.Quantity) resource.Quantity {
	copy := q.DeepCopy()
	copy.Add(q)
	return copy
}
