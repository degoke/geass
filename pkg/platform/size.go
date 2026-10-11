package platform

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

const DefaultAutoscalingTargetCPU int32 = 50

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

// DefaultDatabaseStorage is the persistent volume size an in-cluster database gets
// when nobody chose one. SQLite is a single embedded file, so it starts small; the
// server engines start larger because their volumes also hold logs and indexes.
func DefaultDatabaseStorage(engine geassv1alpha1.GeassDatabaseEngine) string {
	if engine == geassv1alpha1.DatabaseEngineSQLite {
		return "1Gi"
	}
	return "10Gi"
}

// DatabaseStorageDefaults is the default volume size for each engine that has one,
// keyed by lower-case engine name. The dashboard serves it so the UI never has to
// carry its own copy of these numbers.
func DatabaseStorageDefaults() map[string]string {
	defaults := map[string]string{}
	for _, engine := range []geassv1alpha1.GeassDatabaseEngine{
		geassv1alpha1.DatabaseEnginePostgres,
		geassv1alpha1.DatabaseEngineMySQL,
		geassv1alpha1.DatabaseEngineSQLite,
	} {
		defaults[strings.ToLower(string(engine))] = DefaultDatabaseStorage(engine)
	}
	return defaults
}

// DefaultDatabaseResources is the CPU and memory assigned to Postgres, MySQL, Redis, and MinIO.
func DefaultDatabaseResources() corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("250m"),
			corev1.ResourceMemory: resource.MustParse("512Mi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("250m"),
			corev1.ResourceMemory: resource.MustParse("512Mi"),
		},
	}
}

// ResourcesFromSize maps a user-facing CPU and memory assignment to Kubernetes
// requests and limits. Limits match the assigned amount so burst cannot exceed
// the capacity reserved for the workload.
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
	if !allowedSize(cpu, CPUSizes()) {
		return corev1.ResourceRequirements{}, fmt.Errorf("CPU must be one of the available sizes")
	}
	if !allowedSize(memory, MemorySizes()) {
		return corev1.ResourceRequirements{}, fmt.Errorf("memory must be one of the available sizes")
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
		Limits:   corev1.ResourceList{corev1.ResourceCPU: cpuQty, corev1.ResourceMemory: memQty},
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

// DefaultAutoscalingMax is the upper copy count when autoscaling is enabled
// without an explicit maximum. It never doubles the assigned replica count
// and always leaves headroom above three or more copies.
func DefaultAutoscalingMax(replicas int32) int32 {
	if replicas < 1 {
		replicas = 1
	}
	if replicas < 3 {
		return 3
	}
	return replicas + 1
}

func allowedSize(value string, options []SizeOption) bool {
	for _, option := range options {
		if option.Value == value {
			return true
		}
	}
	return false
}
