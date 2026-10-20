/*
Copyright 2026 Adegoke Adewoye.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// NodeStorage says whether a node may hold data that has to outlive it.
// +kubebuilder:validation:Enum=None;Allow
type NodeStorage string

const (
	// NodeStorageNone keeps databases, object stores and volume replicas off the node. It is the default for
	// nodes of a pool, because a pool's nodes come and go. The node is tainted, and only services, which keep
	// nothing on the node, are placed on it.
	NodeStorageNone NodeStorage = "None"
	// NodeStorageAllow makes the node an ordinary worker that may hold data. A node that does is never removed
	// by the autoscaler while it holds a volume.
	NodeStorageAllow NodeStorage = "Allow"
)

// OrphanPolicy says what the pool does with a machine at the provider that carries its labels and belongs to no
// node.
// +kubebuilder:validation:Enum=Report;Delete
type OrphanPolicy string

const (
	// OrphanReport only lists the machine in the pool's status and raises a problem. It is the default, because
	// deleting a machine is not undoable.
	OrphanReport OrphanPolicy = "Report"
	// OrphanDelete deletes a machine that has had no node for twenty minutes.
	OrphanDelete OrphanPolicy = "Delete"
)

// AddressType is the address of a machine that Geass reaches it, and the node advertises, on.
// +kubebuilder:validation:Enum=Public;Private
type AddressType string

const (
	AddressPublic  AddressType = "Public"
	AddressPrivate AddressType = "Private"
)

// GeassNodePoolAutoscaling says when a pool adds nodes and when it removes them.
type GeassNodePoolAutoscaling struct {
	// Enabled lets the pool grow and shrink between minNodes and maxNodes. Off, the pool keeps exactly minNodes.
	// +kubebuilder:default=false
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// ScaleUpDelaySeconds is how long a pod must have been unable to start before the pool adds a node for it. It
	// keeps a pod that is merely slow to be scheduled from buying a machine.
	// +kubebuilder:default=30
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=3600
	// +optional
	ScaleUpDelaySeconds int32 `json:"scaleUpDelaySeconds,omitempty"`

	// MaxScaleUp is the most nodes added in one step.
	// +kubebuilder:default=5
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=50
	// +optional
	MaxScaleUp int32 `json:"maxScaleUp,omitempty"`

	// ScaleDownUtilizationPercent is the share of a node's CPU or memory, whichever is higher, that its pods
	// reserve, below which the node is a candidate for removal.
	// +kubebuilder:default=50
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	// +optional
	ScaleDownUtilizationPercent int32 `json:"scaleDownUtilizationPercent,omitempty"`

	// ScaleDownAfterMinutes is how long a node must have been a candidate before it is removed.
	// +kubebuilder:default=10
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=1440
	// +optional
	ScaleDownAfterMinutes int32 `json:"scaleDownAfterMinutes,omitempty"`

	// ScaleDownDelayAfterAddMinutes is how long after a node was added the pool removes none, so that it does not
	// remove capacity that the load which asked for it has not used yet.
	// +kubebuilder:default=10
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=1440
	// +optional
	ScaleDownDelayAfterAddMinutes int32 `json:"scaleDownDelayAfterAddMinutes,omitempty"`

	// MinNodeLifetimeMinutes is how long a node lives before it may be removed. Most providers bill each started
	// hour, so a node removed after ten minutes costs a full hour; setting this to 55 keeps a node for the hour
	// that is paid for anyway, ready for the next load.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=10080
	// +optional
	MinNodeLifetimeMinutes int32 `json:"minNodeLifetimeMinutes,omitempty"`

	// SpareNodes is how many nodes beyond what pods ask for the pool keeps empty, so that a burst finds room at
	// once instead of waiting minutes for a machine. They count against maxNodes and cost money while idle.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=20
	// +optional
	SpareNodes int32 `json:"spareNodes,omitempty"`
}

// GeassInventoryHost is a server you already own that a pool may join to the cluster when it needs room and give
// back when it does not.
type GeassInventoryHost struct {
	// Name is the node's name once it joins.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=63
	Name string `json:"name"`

	// Address is where Geass reaches the server over SSH, and the address the node advertises in a multi-site
	// cluster.
	// +kubebuilder:validation:MinLength=1
	Address string `json:"address"`

	// SSHPort is the SSH port. It defaults to 22.
	// +kubebuilder:default=22
	// +optional
	SSHPort int32 `json:"sshPort,omitempty"`

	// SSHUser is a user that is root or can use sudo without a password.
	// +kubebuilder:validation:MinLength=1
	SSHUser string `json:"sshUser"`

	// SSHKeySecretRef is the Secret that holds the user's private key (and, optionally, known_hosts).
	SSHKeySecretRef corev1.SecretReference `json:"sshKeySecretRef"`
}

// GeassNodePoolInventory is a fixed list of servers a pool draws from instead of a cloud.
type GeassNodePoolInventory struct {
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=100
	// +listType=map
	// +listMapKey=name
	Hosts []GeassInventoryHost `json:"hosts"`
}

// GeassNodePoolSpec defines the desired state of GeassNodePool.
// +kubebuilder:validation:XValidation:rule="self.maxNodes >= self.minNodes",message="maxNodes must not be below minNodes"
// +kubebuilder:validation:XValidation:rule="has(self.connectionRef) != has(self.inventory)",message="a pool uses either a cloud connection or an inventory of servers, not both and not neither"
// +kubebuilder:validation:XValidation:rule="!has(self.connectionRef) || has(self.size)",message="a pool that creates machines at a cloud needs a size"
type GeassNodePoolSpec struct {
	// Cluster is the GeassCluster the nodes join. It defaults to default.
	// +kubebuilder:default=default
	// +optional
	Cluster string `json:"cluster,omitempty"`

	// ConnectionRef names the GeassCloudConnection that creates the machines. It must be a connection for
	// compute that is not scoped to a project.
	// +optional
	ConnectionRef string `json:"connectionRef,omitempty"`

	// Inventory lists servers you own, instead of a cloud connection.
	// +optional
	Inventory *GeassNodePoolInventory `json:"inventory,omitempty"`

	// Region is the provider's own place: a location, zone or region, as its connection documents.
	// +optional
	Region string `json:"region,omitempty"`

	// Size is the provider's name for a machine size, such as cx23 or t3.medium.
	// +optional
	Size string `json:"size,omitempty"`

	// FallbackSizes are tried, in order, when the provider has no room for size or the account is at a limit for it.
	// +kubebuilder:validation:MaxItems=5
	// +optional
	FallbackSizes []string `json:"fallbackSizes,omitempty"`

	// FallbackRegions are tried, in order, when the provider has no room in region.
	// +kubebuilder:validation:MaxItems=5
	// +optional
	FallbackRegions []string `json:"fallbackRegions,omitempty"`

	// Image is the operating system image. It defaults to the provider's Ubuntu 24.04.
	// +optional
	Image string `json:"image,omitempty"`

	// Spot asks for the provider's cheaper machines that it may take back, where it has them. A node that is
	// taken back is replaced.
	// +optional
	Spot bool `json:"spot,omitempty"`

	// AddressType says which address of a machine Geass reaches it on. Private suits a cluster whose nodes share
	// one private network with the machines of the pool; Public is for a multi-site cluster.
	// +kubebuilder:default=Public
	// +optional
	AddressType AddressType `json:"addressType,omitempty"`

	// Settings are values specific to the provider, such as a subnet or a security group. The provider's
	// documentation lists them.
	// +optional
	Settings map[string]string `json:"settings,omitempty"`

	// Placement is the region, zone and tags the nodes carry.
	// +optional
	Placement *GeassNodePlacement `json:"placement,omitempty"`

	// Storage says whether the nodes may hold data. It defaults to None.
	// +kubebuilder:default=None
	// +optional
	Storage NodeStorage `json:"storage,omitempty"`

	// MinNodes is how many nodes the pool always has. A pool that does not autoscale has exactly this many.
	// +kubebuilder:default=0
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=200
	MinNodes int32 `json:"minNodes"`

	// MaxNodes is the most nodes the pool ever has. It is required, so that no pool can grow without a bound.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=200
	MaxNodes int32 `json:"maxNodes"`

	// Autoscaling makes the pool follow load between minNodes and maxNodes.
	// +optional
	Autoscaling *GeassNodePoolAutoscaling `json:"autoscaling,omitempty"`

	// OrphanPolicy says what to do with a machine of this pool at the provider that no node owns.
	// +kubebuilder:default=Report
	// +optional
	OrphanPolicy OrphanPolicy `json:"orphanPolicy,omitempty"`

	// Suspend stops the pool from adding or removing nodes. Nodes it has stay.
	// +optional
	Suspend bool `json:"suspend,omitempty"`
}

// NodePoolPhase is how a pool is doing.
type NodePoolPhase string

const (
	NodePoolReady     NodePoolPhase = "Ready"
	NodePoolScaling   NodePoolPhase = "Scaling"
	NodePoolDegraded  NodePoolPhase = "Degraded"
	NodePoolPaused    NodePoolPhase = "Paused"
	NodePoolSuspended NodePoolPhase = "Suspended"
	NodePoolPending   NodePoolPhase = "Pending"
)

// GeassNodePoolSize is the machine size the pool creates, as the provider describes it.
type GeassNodePoolSize struct {
	// Name is the size in use. It differs from spec.size while a fallback size is in use.
	Name        string `json:"name,omitempty"`
	VCPUs       int32  `json:"vcpus,omitempty"`
	MemoryMiB   int64  `json:"memoryMiB,omitempty"`
	HourlyPrice string `json:"hourlyPrice,omitempty"`
	Currency    string `json:"currency,omitempty"`
}

// GeassNodePoolStatus defines the observed state of GeassNodePool.
type GeassNodePoolStatus struct {
	// conditions represent the current state of the GeassNodePool resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Phase is how the pool is doing.
	// +optional
	Phase NodePoolPhase `json:"phase,omitempty"`

	// Message says what the pool is doing, or why it is not doing something.
	// +optional
	Message string `json:"message,omitempty"`

	// ObservedGeneration is the generation of the spec the status describes.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// DesiredNodes is how many nodes the pool is working towards.
	// +optional
	DesiredNodes int32 `json:"desiredNodes,omitempty"`

	// Nodes is how many nodes the pool has, whatever state they are in.
	// +optional
	Nodes int32 `json:"nodes,omitempty"`

	// ReadyNodes is how many of them are ready.
	// +optional
	ReadyNodes int32 `json:"readyNodes,omitempty"`

	// PendingPods is how many pods were unable to start, and could start on a node of this pool, at the last look.
	// +optional
	PendingPods int32 `json:"pendingPods,omitempty"`

	// Size is the machine size nodes are created with, as the provider describes it.
	// +optional
	Size *GeassNodePoolSize `json:"size,omitempty"`

	// NodeAllocatable is what a new node of the pool offers to pods, after the system's own use. The capacity check
	// uses it to tell whether the pool can make room for a workload.
	// +optional
	NodeAllocatable corev1.ResourceList `json:"nodeAllocatable,omitempty"`

	// EstimatedHourlyCost is what the pool's nodes cost for an hour at the provider's list price, when the provider
	// states one. Taxes and traffic are not in it.
	// +optional
	EstimatedHourlyCost string `json:"estimatedHourlyCost,omitempty"`

	// ConsecutiveFailures counts machines that failed to become nodes since the last one that did. Three of them
	// pause the pool.
	// +optional
	ConsecutiveFailures int32 `json:"consecutiveFailures,omitempty"`

	// CountedFailures are the nodes whose failure is already in ConsecutiveFailures.
	// +kubebuilder:validation:MaxItems=20
	// +optional
	CountedFailures []string `json:"countedFailures,omitempty"`

	// PausedUntil is when a pool that was paused after repeated failures tries again.
	// +optional
	PausedUntil *metav1.Time `json:"pausedUntil,omitempty"`

	// LastScaleUp and LastScaleDown are when the pool last added and last removed a node.
	// +optional
	LastScaleUp *metav1.Time `json:"lastScaleUp,omitempty"`
	// +optional
	LastScaleDown *metav1.Time `json:"lastScaleDown,omitempty"`

	// ScaleDownBackoffUntil is when a pool whose last attempt to remove a node failed (a pod would not leave) tries
	// again.
	// +optional
	ScaleDownBackoffUntil *metav1.Time `json:"scaleDownBackoffUntil,omitempty"`

	// LastFailure is why the most recent machine failed to become a node, and LastFailureAt is when.
	// +optional
	LastFailure string `json:"lastFailure,omitempty"`
	// +optional
	LastFailureAt *metav1.Time `json:"lastFailureAt,omitempty"`

	// FallbackStep says which size and region of the pool's lists new nodes are made with: 0 is the first of each,
	// and each step after it is the next combination, taken when the provider has no room or the account is at a
	// limit. It goes back to 0 an hour later.
	// +optional
	FallbackStep int32 `json:"fallbackStep,omitempty"`
	// +optional
	FallbackSince *metav1.Time `json:"fallbackSince,omitempty"`

	// UnneededSince records, for each node that is a candidate for removal, since when.
	// +optional
	UnneededSince map[string]metav1.Time `json:"unneededSince,omitempty"`

	// Orphans are machines at the provider that carry the pool's labels and belong to no node.
	// +kubebuilder:validation:MaxItems=50
	// +optional
	Orphans []string `json:"orphans,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Min",type=integer,JSONPath=`.spec.minNodes`
// +kubebuilder:printcolumn:name="Max",type=integer,JSONPath=`.spec.maxNodes`
// +kubebuilder:printcolumn:name="Nodes",type=integer,JSONPath=`.status.nodes`
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.readyNodes`
// +kubebuilder:printcolumn:name="Size",type=string,JSONPath=`.spec.size`

// GeassNodePool is the Schema for the geassnodepools API
type GeassNodePool struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of GeassNodePool
	// +required
	Spec GeassNodePoolSpec `json:"spec"`

	// status defines the observed state of GeassNodePool
	// +optional
	Status GeassNodePoolStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// GeassNodePoolList contains a list of GeassNodePool
type GeassNodePoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GeassNodePool `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &GeassNodePool{}, &GeassNodePoolList{})
		return nil
	})
}

// AutoscalingEnabled reports whether the pool follows load.
func (p *GeassNodePool) AutoscalingEnabled() bool {
	return p.Spec.Autoscaling != nil && p.Spec.Autoscaling.Enabled
}

// IsInventory reports whether the pool draws on servers listed in it instead of creating machines.
func (p *GeassNodePool) IsInventory() bool {
	return p.Spec.Inventory != nil
}

// EffectiveStorage is the pool's storage mode: None unless it says otherwise.
func (p *GeassNodePool) EffectiveStorage() NodeStorage {
	if p.Spec.Storage == "" {
		return NodeStorageNone
	}
	return p.Spec.Storage
}
