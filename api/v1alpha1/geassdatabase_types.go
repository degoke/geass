/*
Copyright 2026 DEGOKE.

Licensed under the Elastic License 2.0 (the "License"); you may not use this
file except in compliance with the License. You may obtain a copy of the
License at LICENSE or https://www.elastic.co/licensing/elastic-license.
*/

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// GeassDatabaseEngine identifies the database engine type.
// +kubebuilder:validation:Enum=Postgres
type GeassDatabaseEngine string

const (
	DatabaseEnginePostgres GeassDatabaseEngine = "Postgres"
)

// GeassDatabaseSpec defines the desired state of GeassDatabase.
type GeassDatabaseSpec struct {
	// Workspace is the target Geass workspace.
	// +kubebuilder:validation:Enum=dev;staging;production
	Workspace GeassWorkspace `json:"workspace"`

	// Engine is the database engine to provision.
	// +kubebuilder:validation:Enum=Postgres
	Engine GeassDatabaseEngine `json:"engine"`

	// Version is the Postgres major version.
	// +kubebuilder:default="16"
	// +optional
	Version string `json:"version,omitempty"`

	// Instances is the number of Postgres instances in the CNPG cluster.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +optional
	Instances *int32 `json:"instances,omitempty"`

	// StorageSize is the requested PVC size per instance.
	// +kubebuilder:default="10Gi"
	// +optional
	StorageSize *resource.Quantity `json:"storageSize,omitempty"`
}

// GeassDatabaseStatus defines the observed state of GeassDatabase.
type GeassDatabaseStatus struct {
	// WorkspaceNamespace is the namespace workloads were created in.
	// +optional
	WorkspaceNamespace string `json:"workspaceNamespace,omitempty"`

	// ConnectionSecret is the name of the Secret containing connection credentials.
	// +optional
	ConnectionSecret string `json:"connectionSecret,omitempty"`

	// Host is the in-cluster database hostname.
	// +optional
	Host string `json:"host,omitempty"`

	// conditions represent the current state of the GeassDatabase resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Workspace",type=string,JSONPath=`.spec.workspace`
// +kubebuilder:printcolumn:name="Engine",type=string,JSONPath=`.spec.engine`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// GeassDatabase is the Schema for the geassdatabases API.
type GeassDatabase struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec GeassDatabaseSpec `json:"spec"`

	// +optional
	Status GeassDatabaseStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// GeassDatabaseList contains a list of GeassDatabase.
type GeassDatabaseList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GeassDatabase `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &GeassDatabase{}, &GeassDatabaseList{})
		return nil
	})
}
