package v1alpha1

import "k8s.io/apimachinery/pkg/api/resource"

// GeassTenantLimits keep one organization's workloads from crowding out another's on a cluster they share, beyond what its
// quota caps. Geass writes them into every namespace the organization's pods run in: each environment of each of its
// projects, and the namespace its builds run in. Kubernetes counts per namespace, so every one of those namespaces gets the
// whole of each limit; the organization's total over all of them is spec.quota.
//
// A field left out is not limited.
type GeassTenantLimits struct {
	// Pods is the most pods each namespace may have running or waiting at once. Finished pods are not counted.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100000
	// +optional
	Pods *int32 `json:"pods,omitempty"`
	// EphemeralStorage is the most local disk the pods of each namespace may use together, such as "20Gi": the total of
	// their containers' ephemeral-storage limits, and of their requests. A container's ephemeral storage is what it writes to
	// its own filesystem, its logs and its emptyDir volumes, on the node's disk. It needs a default limit for containers that
	// set none (container.defaultLimit.ephemeralStorage, or container.max.ephemeralStorage), or every such pod is refused.
	// +optional
	EphemeralStorage *resource.Quantity `json:"ephemeralStorage,omitempty"`
	// Container is what a container that asks for nothing is given, and the most one may ask for.
	// +optional
	Container *GeassContainerLimits `json:"container,omitempty"`
	// Bandwidth caps the network traffic of each pod. It works only where the cluster's network plugin runs the CNI bandwidth
	// plugin, and only on Kubernetes 1.36 or later, where Geass can add it to every pod as it is made.
	// +optional
	Bandwidth *GeassPodBandwidth `json:"bandwidth,omitempty"`
}

// IsZero reports whether nothing is limited.
func (l *GeassTenantLimits) IsZero() bool {
	return l == nil || (l.Pods == nil && l.EphemeralStorage == nil && l.Container.IsZero() && l.Bandwidth.IsZero())
}

// GeassContainerLimits are the defaults and the most of one container's requests and limits, for CPU, memory and ephemeral
// storage. They apply to every container made in the namespace, by Geass or anything else, when it is made: a container
// that is running is not changed. A request is what the scheduler sets aside for the container, and what the quota and the
// usage meter count; a limit is the most it may use, past which its CPU is slowed, or it is stopped for memory, or the pod
// is evicted for disk.
type GeassContainerLimits struct {
	// DefaultRequest is the request a container is given for a resource it asks for none of. Without it a container that
	// is given a limit asks for 50m CPU, 64Mi memory and 64Mi ephemeral storage, or its limit if that is lower.
	// +optional
	DefaultRequest *GeassContainerResources `json:"defaultRequest,omitempty"`
	// DefaultLimit is the limit a container is given for a resource it sets no limit on. Without it, that is Max.
	// +optional
	DefaultLimit *GeassContainerResources `json:"defaultLimit,omitempty"`
	// Max is the most a container may set as its limit. A pod with a container that sets more, or whose request is above
	// it, is refused.
	// +optional
	Max *GeassContainerResources `json:"max,omitempty"`
}

// IsZero reports whether nothing is set.
func (c *GeassContainerLimits) IsZero() bool {
	return c == nil || (c.DefaultRequest.IsZero() && c.DefaultLimit.IsZero() && c.Max.IsZero())
}

// GeassContainerResources is an amount of each resource a container may be given.
type GeassContainerResources struct {
	// CPU, such as "500m" or "2".
	// +optional
	CPU *resource.Quantity `json:"cpu,omitempty"`
	// Memory, such as "512Mi".
	// +optional
	Memory *resource.Quantity `json:"memory,omitempty"`
	// EphemeralStorage is local disk, such as "1Gi".
	// +optional
	EphemeralStorage *resource.Quantity `json:"ephemeralStorage,omitempty"`
}

// IsZero reports whether no amount is set.
func (r *GeassContainerResources) IsZero() bool {
	return r == nil || (r.CPU == nil && r.Memory == nil && r.EphemeralStorage == nil)
}

// GeassPodBandwidth is the most traffic one pod may send or receive, in bits per second, such as "100M" for 100 megabits.
// It counts all of the pod's traffic, to other pods of the cluster as well as to the internet. The network plugin holds it
// by queueing what goes past it; Kubernetes refuses a value below 1k or above 1P when it starts the pod.
type GeassPodBandwidth struct {
	// Ingress is the most a pod may receive.
	// +optional
	Ingress *resource.Quantity `json:"ingress,omitempty"`
	// Egress is the most a pod may send.
	// +optional
	Egress *resource.Quantity `json:"egress,omitempty"`
}

// IsZero reports whether neither direction is limited.
func (b *GeassPodBandwidth) IsZero() bool {
	return b == nil || (b.Ingress == nil && b.Egress == nil)
}

// GeassPlatformTenantLimitsSpec holds the limits every organization's workloads are held to unless the organization has its
// own (spec.limits on the GeassOrganization).
type GeassPlatformTenantLimitsSpec struct {
	// Defaults are the limits of every organization. An organization's own spec.limits replaces each of these it sets, field
	// by field at the top level: an organization that sets container replaces the whole of the default container limits.
	// Unset means no limits beyond each organization's own. A project that belongs to no organization is never limited by
	// them.
	// +optional
	Defaults *GeassTenantLimits `json:"defaults,omitempty"`
}
