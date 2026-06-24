/*
Copyright 2026 DEGOKE.

Licensed under the Elastic License 2.0 (the "License"); you may not use this
file except in compliance with the License. You may obtain a copy of the
License at LICENSE or https://www.elastic.co/licensing/elastic-license.
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

	// LastHeartbeatTime is the last time the node was observed healthy.
	LastHeartbeatTime metav1.Time `json:"lastHeartbeatTime,omitempty"`

	// NodeName is the name of the Kubernetes node.
	NodeName string `json:"nodeName,omitempty"`
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
	Status GeassNodeStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// GeassNodeList contains a list of GeassNode
type GeassNodeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GeassNode `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &GeassNode{}, &GeassNodeList{})
		return nil
	})
}
