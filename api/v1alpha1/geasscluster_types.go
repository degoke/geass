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

// GeassWorkspace identifies a fixed Geass deployment environment.
// +kubebuilder:validation:Enum=dev;staging;production
type GeassWorkspace string

const (
	WorkspaceDev        GeassWorkspace = "dev"
	WorkspaceStaging    GeassWorkspace = "staging"
	WorkspaceProduction GeassWorkspace = "production"
)

// GeassClusterCertManagerAddon controls cert-manager installation.
type GeassClusterCertManagerAddon struct {
	// Enabled installs cert-manager when true.
	// +kubebuilder:default=true
	// +optional
	Enabled *bool `json:"enabled,omitempty"`
}

// GeassClusterMonitoringAddon controls kube-prometheus-stack installation.
type GeassClusterMonitoringAddon struct {
	// Enabled installs monitoring when true.
	// +kubebuilder:default=true
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// Profile selects the monitoring footprint.
	// +kubebuilder:default=lite
	// +kubebuilder:validation:Enum=lite;full
	// +optional
	Profile string `json:"profile,omitempty"`
}

// GeassClusterAddonsSpec groups platform add-on toggles.
type GeassClusterAddonsSpec struct {
	// CertManager controls cert-manager installation.
	// +optional
	CertManager GeassClusterCertManagerAddon `json:"certManager,omitempty"`

	// Monitoring controls kube-prometheus-stack installation.
	// +optional
	Monitoring GeassClusterMonitoringAddon `json:"monitoring,omitempty"`
}

// GeassClusterSpec defines the desired state of GeassCluster.
type GeassClusterSpec struct {
	// Version is the version of the GeassCluster.
	Version string `json:"version"`

	// ServerURL is the URL of the GeassCluster server.
	ServerURL string `json:"serverURL"`

	// TokenSecretRef is the reference to the secret containing the token for the GeassCluster.
	TokenSecretRef corev1.SecretReference `json:"tokenSecretRef"`

	// Addons controls default platform add-ons reconciled by the cluster controller.
	// +optional
	Addons GeassClusterAddonsSpec `json:"addons,omitempty"`
}

type ClusterPhase string

const (
	ClusterPhaseReady ClusterPhase = "Ready"
)

// GeassClusterStatus defines the observed state of GeassCluster.
type GeassClusterStatus struct {
	// conditions represent the current state of the GeassCluster resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Phase is the current phase of the GeassCluster.
	Phase ClusterPhase `json:"phase,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`

// GeassCluster is the Schema for the geassclusters API.
type GeassCluster struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec GeassClusterSpec `json:"spec"`

	// +optional
	Status GeassClusterStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// GeassClusterList contains a list of GeassCluster.
type GeassClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GeassCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &GeassCluster{}, &GeassClusterList{})
		return nil
	})
}

// AddonEnabled returns whether an addon pointer is enabled (default true).
func AddonEnabled(enabled *bool) bool {
	return enabled == nil || *enabled
}
