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

// GeassCacheEngine identifies the cache engine type.
// +kubebuilder:validation:Enum=Redis
type GeassCacheEngine string

const (
	CacheEngineRedis GeassCacheEngine = "Redis"
)

// GeassCacheSpec defines the desired state of GeassCache.
type GeassCacheSpec struct {
	// Workspace is the target Geass workspace.
	// +kubebuilder:validation:Enum=dev;staging;production
	Workspace GeassWorkspace `json:"workspace"`

	// Engine is the cache engine to provision.
	// +kubebuilder:validation:Enum=Redis
	Engine GeassCacheEngine `json:"engine"`

	// Version is the Redis chart/app version hint.
	// +kubebuilder:default="7.4"
	// +optional
	Version string `json:"version,omitempty"`
}

// GeassCacheStatus defines the observed state of GeassCache.
type GeassCacheStatus struct {
	// WorkspaceNamespace is the namespace workloads were created in.
	// +optional
	WorkspaceNamespace string `json:"workspaceNamespace,omitempty"`

	// ConnectionSecret is the name of the Secret containing Redis credentials.
	// +optional
	ConnectionSecret string `json:"connectionSecret,omitempty"`

	// Host is the in-cluster Redis hostname.
	// +optional
	Host string `json:"host,omitempty"`

	// Port is the Redis port.
	// +optional
	Port int32 `json:"port,omitempty"`

	// conditions represent the current state of the GeassCache resource.
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

// GeassCache is the Schema for the geasscaches API.
type GeassCache struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec GeassCacheSpec `json:"spec"`

	// +optional
	Status GeassCacheStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// GeassCacheList contains a list of GeassCache.
type GeassCacheList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GeassCache `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &GeassCache{}, &GeassCacheList{})
		return nil
	})
}
