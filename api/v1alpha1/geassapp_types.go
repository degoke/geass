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

// GeassAppIngressSpec defines external access for an app.
type GeassAppIngressSpec struct {
	// Host is the ingress hostname.
	// +optional
	Host string `json:"host,omitempty"`

	// Path is the HTTP path prefix.
	// +kubebuilder:default=/
	// +optional
	Path string `json:"path,omitempty"`

	// TLSEnabled requests TLS termination via cert-manager when true.
	// +optional
	TLSEnabled bool `json:"tlsEnabled,omitempty"`
}

// GeassAppMetricsSpec configures Prometheus scraping.
type GeassAppMetricsSpec struct {
	// Enabled creates a ServiceMonitor when true.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// Path is the metrics HTTP path.
	// +kubebuilder:default=/metrics
	// +optional
	Path string `json:"path,omitempty"`

	// Port is the metrics port name or number on the Service.
	// +kubebuilder:default=http
	// +optional
	Port string `json:"port,omitempty"`
}

// GeassAppSpec defines the desired state of GeassApp.
type GeassAppSpec struct {
	// Workspace is the target Geass workspace.
	// +kubebuilder:validation:Enum=dev;staging;production
	Workspace GeassWorkspace `json:"workspace"`

	// Image is the container image to run.
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`

	// Replicas is the desired Deployment replica count.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=0
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	// Port is the primary container and Service port.
	// +kubebuilder:default=8080
	// +kubebuilder:validation:Minimum=1
	// +optional
	Port int32 `json:"port,omitempty"`

	// Ingress configures Traefik ingress exposure.
	// +optional
	Ingress GeassAppIngressSpec `json:"ingress,omitempty"`

	// Env is a list of environment variables injected into the container.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// EnvFrom loads environment variables from referenced ConfigMaps and Secrets in the workspace namespace.
	// +optional
	EnvFrom []corev1.EnvFromSource `json:"envFrom,omitempty"`

	// ConfigMapRefs lists existing ConfigMaps in the workspace namespace to mount under /config/refs/<name>.
	// +optional
	ConfigMapRefs []corev1.LocalObjectReference `json:"configMapRefs,omitempty"`

	// SecretRefs lists existing Secrets in the workspace namespace to mount under /secrets/refs/<name>.
	// +optional
	SecretRefs []corev1.LocalObjectReference `json:"secretRefs,omitempty"`

	// ConfigData is key/value data stored in an owned ConfigMap.
	// +optional
	ConfigData map[string]string `json:"configData,omitempty"`

	// SecretData is key/value data stored in an owned Secret.
	// +optional
	SecretData map[string]string `json:"secretData,omitempty"`

	// Metrics configures optional Prometheus scraping.
	// +optional
	Metrics GeassAppMetricsSpec `json:"metrics,omitempty"`
}

// GeassAppStatus defines the observed state of GeassApp.
type GeassAppStatus struct {
	// WorkspaceNamespace is the namespace workloads were created in.
	// +optional
	WorkspaceNamespace string `json:"workspaceNamespace,omitempty"`

	// URL is the primary ingress URL when configured.
	// +optional
	URL string `json:"url,omitempty"`

	// conditions represent the current state of the GeassApp resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Workspace",type=string,JSONPath=`.spec.workspace`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// GeassApp is the Schema for the geassapps API.
type GeassApp struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec GeassAppSpec `json:"spec"`

	// +optional
	Status GeassAppStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// GeassAppList contains a list of GeassApp.
type GeassAppList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GeassApp `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &GeassApp{}, &GeassAppList{})
		return nil
	})
}
