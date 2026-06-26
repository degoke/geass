/*
Copyright 2026 DEGOKE.

Licensed under the Elastic License 2.0 (the "License"); you may not use this
file except in compliance with the License. You may obtain a copy of the
License at LICENSE or https://www.elastic.co/licensing/elastic-license.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// GeassObjectStoreEngine identifies the object store engine type.
// +kubebuilder:validation:Enum=MinIO
type GeassObjectStoreEngine string

const (
	ObjectStoreEngineMinIO GeassObjectStoreEngine = "MinIO"
)

// GeassObjectStoreSpec defines the desired state of GeassObjectStore.
type GeassObjectStoreSpec struct {
	// Workspace is the target Geass workspace.
	// +kubebuilder:validation:Enum=dev;staging;production
	Workspace GeassWorkspace `json:"workspace"`

	// Engine is the object store engine to provision.
	// +kubebuilder:validation:Enum=MinIO
	Engine GeassObjectStoreEngine `json:"engine"`

	// Buckets is an optional list of buckets to create on provision.
	// +optional
	Buckets []string `json:"buckets,omitempty"`
}

// GeassObjectStoreStatus defines the observed state of GeassObjectStore.
type GeassObjectStoreStatus struct {
	// WorkspaceNamespace is the namespace workloads were created in.
	// +optional
	WorkspaceNamespace string `json:"workspaceNamespace,omitempty"`

	// ConnectionSecret is the name of the Secret containing credentials.
	// +optional
	ConnectionSecret string `json:"connectionSecret,omitempty"`

	// Endpoint is the in-cluster S3-compatible endpoint URL.
	// +optional
	Endpoint string `json:"endpoint,omitempty"`

	// conditions represent the current state of the GeassObjectStore resource.
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

// GeassObjectStore is the Schema for the geassobjectstores API.
type GeassObjectStore struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec GeassObjectStoreSpec `json:"spec"`

	// +optional
	Status GeassObjectStoreStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// GeassObjectStoreList contains a list of GeassObjectStore.
type GeassObjectStoreList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GeassObjectStore `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &GeassObjectStore{}, &GeassObjectStoreList{})
		return nil
	})
}
