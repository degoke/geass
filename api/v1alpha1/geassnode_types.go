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

type NodeRole string

const (
	NodeRoleControlPlane NodeRole = "ControlPlane"
	NodeRoleWorker       NodeRole = "Worker"
)

type NodePhase string

const (
	NodePhasePending  NodePhase = "Pending"
	NodePhaseCreating NodePhase = "Creating"
	NodePhaseJoining  NodePhase = "Joining"
	NodePhaseReady    NodePhase = "Ready"
	NodePhaseFailed   NodePhase = "Failed"
	NodePhaseDeleting NodePhase = "Deleting"
	NodePhaseDeleted  NodePhase = "Deleted"
	NodePhaseError    NodePhase = "Error"
	NodePhaseUnknown  NodePhase = "Unknown"
)

// GeassNodeSpec defines the desired state of GeassNode
type GeassNodeSpec struct {
	// Role is the role of the node in the cluster.
	// +kubebuilder:validation:Enum=ControlPlane;Worker
	Role NodeRole `json:"role"`

	// Default marks a pre-existing cluster control plane node that the operator should not provision.
	// +optional
	Default bool `json:"default,omitempty"`

	// EIP is the Elastic IP address of the node.
	// +optional
	EIP string `json:"eip,omitempty"`

	// SSHPort is the port number for the SSH server.
	// +optional
	// +kubebuilder:default=22
	SSHPort int `json:"sshPort,omitempty"`

	// SSHUser is the username for the SSH server.
	// +optional
	SSHUser string `json:"sshUser,omitempty"`

	// SSHKeySecretRef is the reference to the secret containing the SSH private key.
	// Required for worker nodes; optional for control plane nodes.
	// +optional
	SSHKeySecretRef *corev1.SecretReference `json:"sshKeySecretRef,omitempty"`

	// ClusterRef is the reference to the GeassCluster resource.
	ClusterRef corev1.ObjectReference `json:"clusterRef"`

	// Placement is the node's region, zone and tags. Leave it out to manage the node's labels by hand.
	// +optional
	Placement *GeassNodePlacement `json:"placement,omitempty"`

	// Pool is the GeassNodePool the node belongs to. A node of a pool is labelled with it, and the pool is what
	// adds and removes it.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="a node cannot move to another pool: make a new node"
	// +optional
	Pool string `json:"pool,omitempty"`

	// Storage says whether the node may hold data. For a node of a pool it comes from the pool; for any other it
	// is left out, which means Allow.
	// +optional
	Storage NodeStorage `json:"storage,omitempty"`

	// Cloud makes Geass create the node's machine at a cloud provider, instead of joining one that already exists.
	// The machine's address is then in status.cloud, and eip, sshUser and sshKeySecretRef are left out: Geass
	// logs in with a key of its own. The connection and the region cannot be changed once the machine exists: the machine
	// is deleted through the connection that made it.
	// +kubebuilder:validation:XValidation:rule="self.connectionRef == oldSelf.connectionRef && (has(self.region) ? self.region : '') == (has(oldSelf.region) ? oldSelf.region : '')",message="a node's cloud connection and region cannot be changed: make a new node"
	// +optional
	Cloud *GeassNodeCloud `json:"cloud,omitempty"`
}

// GeassNodeCloud says what machine to create for a node.
type GeassNodeCloud struct {
	// ConnectionRef names the GeassCloudConnection to create the machine with.
	// +kubebuilder:validation:MinLength=1
	ConnectionRef string `json:"connectionRef"`

	// Region is the provider's own place: a location, zone or region.
	// +optional
	Region string `json:"region,omitempty"`

	// Size is the provider's name for a machine size.
	// +kubebuilder:validation:MinLength=1
	Size string `json:"size"`

	// Image is the operating system image. It defaults to the provider's Ubuntu 24.04.
	// +optional
	Image string `json:"image,omitempty"`

	// Spot asks for a cheaper machine that the provider may take back.
	// +optional
	Spot bool `json:"spot,omitempty"`

	// AddressType says which address of the machine Geass reaches it on. It defaults to Public.
	// +optional
	AddressType AddressType `json:"addressType,omitempty"`

	// Settings are values specific to the provider.
	// +optional
	Settings map[string]string `json:"settings,omitempty"`
}

// GeassNodeCloudStatus is what Geass knows about a node's machine.
type GeassNodeCloudStatus struct {
	// Attempt counts the machines made for the node before this one. It is part of the machine's name after the
	// first, so that a machine still being removed never shares a name with its replacement.
	// +optional
	Attempt int32 `json:"attempt,omitempty"`

	// InstanceName is the name the machine is created with. It is written before the provider is called, so that
	// a machine made just before a crash is found again by its name and not made twice.
	// +optional
	InstanceName string `json:"instanceName,omitempty"`

	// InstanceID is the provider's id for the machine. The machine is deleted by this id and no other.
	// +optional
	InstanceID string `json:"instanceID,omitempty"`

	// Size and Region are what the machine was actually created with, which differs from the spec when a fallback
	// was used.
	// +optional
	Size string `json:"size,omitempty"`
	// +optional
	Region string `json:"region,omitempty"`

	// Problem names what keeps the machine from being created: Capacity (the provider has no room for it) or Quota
	// (the account is at a limit). The node's pool reads it to try another size or place.
	// +kubebuilder:validation:Enum=Capacity;Quota
	// +optional
	Problem string `json:"problem,omitempty"`

	// State is the machine's state as the provider last reported it.
	// +optional
	State string `json:"state,omitempty"`

	// PublicIP and PrivateIP are the machine's addresses.
	// +optional
	PublicIP string `json:"publicIP,omitempty"`
	// +optional
	PrivateIP string `json:"privateIP,omitempty"`

	// CreatedAt is when the provider accepted the machine.
	// +optional
	CreatedAt *metav1.Time `json:"createdAt,omitempty"`

	// ControlPlaneAccess is the address opened for the machine in the control plane's firewall, which is closed again
	// when the machine is removed.
	// +optional
	ControlPlaneAccess string `json:"controlPlaneAccess,omitempty"`

	// ControlPlaneAccessFirewall is the firewall that ControlPlaneAccess was opened on. The rule is closed on this one,
	// even if the cluster's controlPlaneFirewall has since been changed or removed.
	// +optional
	ControlPlaneAccessFirewall *GeassControlPlaneFirewall `json:"controlPlaneAccessFirewall,omitempty"`

	// JoinAttempts counts the times Geass set about installing K3s on the machine. Each one is written before the
	// install starts, with a write that fails if another pass got there first, which is what keeps one node from being
	// installed twice by two passes that read an old copy of it.
	// +optional
	JoinAttempts int32 `json:"joinAttempts,omitempty"`

	// JoinStartedAt is when Geass began to install K3s on the machine. A node that is not ready a quarter of an
	// hour later is given up on.
	// +optional
	JoinStartedAt *metav1.Time `json:"joinStartedAt,omitempty"`
}

// GeassNodeStatus defines the observed state of GeassNode.
type GeassNodeStatus struct {
	// conditions represent the current state of the GeassNode resource.
	// Each condition has a unique type and reflects the status of a specific aspect of the resource.
	//
	// Standard condition types include:
	// - "Available": the resource is fully functional
	// - "Progressing": the resource is being created or updated
	// - "Degraded": the resource failed to reach or maintain its desired state
	//
	// The status of each condition is one of True, False, or Unknown.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Phase is the current phase of the GeassNode.
	Phase NodePhase `json:"phase,omitempty"`

	// Message is a human-readable status message.
	Message string `json:"message,omitempty"`

	// LastHeartbeatTime is no longer updated. Kubelet keeps a heartbeat on the Kubernetes node (its Ready
	// condition and node lease), and rewriting one here once a minute for every node cost a write and an
	// event for nothing that read it.
	// +optional
	LastHeartbeatTime metav1.Time `json:"lastHeartbeatTime,omitempty"`

	// NodeName is the name of the Kubernetes node.
	NodeName string `json:"nodeName,omitempty"`

	// Cloud is the machine Geass created for the node, when spec.cloud asks for one.
	// +optional
	Cloud *GeassNodeCloudStatus `json:"cloud,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// GeassNode is the Schema for the geassnodes API
type GeassNode struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of GeassNode
	// +required
	Spec GeassNodeSpec `json:"spec"`

	// status defines the observed state of GeassNode
	// +optional
	// +optional
	Status GeassNodeStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// GeassNodeList contains a list of GeassNode
type GeassNodeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GeassNode `json:"items"`
}

// Address is where Geass reaches the node's machine: spec.eip, or, for a machine Geass created, the address of it.
func (n *GeassNode) Address() string {
	if n.Spec.EIP != "" {
		return n.Spec.EIP
	}
	if n.Spec.Cloud != nil && n.Status.Cloud != nil {
		if n.Spec.Cloud.AddressType == AddressPrivate {
			return n.Status.Cloud.PrivateIP
		}
		return n.Status.Cloud.PublicIP
	}
	return ""
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &GeassNode{}, &GeassNodeList{})
		return nil
	})
}
