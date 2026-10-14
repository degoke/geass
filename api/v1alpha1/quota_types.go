package v1alpha1

import "k8s.io/apimachinery/pkg/api/resource"

// GeassEnvironmentQuota caps what one environment of a project can use. Every environment of the project
// gets its own copy of the cap, pull request environments included, because Kubernetes counts usage per
// namespace and an environment is a namespace. A project with three environments and a 4 CPU cap can ask for
// up to 12 CPUs in all.
//
// The caps count what pods ask for (their requests), which is also what the scheduler and the capacity
// gate count. A field left out is not capped.
type GeassEnvironmentQuota struct {
	// CPU is the most CPU the pods of one environment may ask for in total, such as "4" or "500m".
	// +optional
	CPU *resource.Quantity `json:"cpu,omitempty"`
	// Memory is the most memory the pods of one environment may ask for in total, such as "8Gi".
	// +optional
	Memory *resource.Quantity `json:"memory,omitempty"`
	// Storage is the most disk the volumes of one environment may ask for in total, such as "50Gi".
	// +optional
	Storage *resource.Quantity `json:"storage,omitempty"`
	// Pods is the most pods one environment may have running or waiting at once. Finished pods are not counted.
	// +kubebuilder:validation:Minimum=1
	// +optional
	Pods *int32 `json:"pods,omitempty"`
}

// IsZero reports whether no cap is set, which is the same as having no quota.
func (q *GeassEnvironmentQuota) IsZero() bool {
	return q == nil || (q.CPU == nil && q.Memory == nil && q.Storage == nil && q.Pods == nil)
}

// GeassProjectQuota caps what a whole project can ask for, added up over all its environments, pull request
// environments included. Kubernetes counts usage per namespace and has no cap that spans several, so Geass keeps
// the environments' own caps in step with the total: each environment may ask for what the others leave. That
// follows within a moment of a pod starting or stopping, which means that two environments starting a great deal
// at the same instant can briefly go past the total. Nothing that is running is stopped when it is passed.
//
// A field left out is not capped. It can be set with an environment quota, and an environment is held to
// whichever of the two is lower.
type GeassProjectQuota struct {
	// CPU is the most CPU the pods of the project may ask for in total, such as "8".
	// +optional
	CPU *resource.Quantity `json:"cpu,omitempty"`
	// Memory is the most memory the pods of the project may ask for in total, such as "16Gi".
	// +optional
	Memory *resource.Quantity `json:"memory,omitempty"`
	// Storage is the most disk the volumes of the project may ask for in total, such as "200Gi".
	// +optional
	Storage *resource.Quantity `json:"storage,omitempty"`
	// Pods is the most pods the project may have running or waiting at once. Finished pods are not counted.
	// +kubebuilder:validation:Minimum=1
	// +optional
	Pods *int32 `json:"pods,omitempty"`
}

// IsZero reports whether no cap is set, which is the same as having no quota.
func (q *GeassProjectQuota) IsZero() bool {
	return q == nil || (q.CPU == nil && q.Memory == nil && q.Storage == nil && q.Pods == nil)
}

// GeassQuotaUsage is what a project's environments ask for together, for the resources its project quota caps.
type GeassQuotaUsage struct {
	// CPU is the CPU the project's pods ask for.
	// +optional
	CPU *resource.Quantity `json:"cpu,omitempty"`
	// Memory is the memory the project's pods ask for.
	// +optional
	Memory *resource.Quantity `json:"memory,omitempty"`
	// Storage is the disk the project's volumes ask for.
	// +optional
	Storage *resource.Quantity `json:"storage,omitempty"`
	// Pods is how many pods the project has running or waiting.
	// +optional
	Pods *int32 `json:"pods,omitempty"`
}
