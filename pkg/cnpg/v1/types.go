// Package v1 defines CloudNativePG API types used by Geass controllers.
// +kubebuilder:object:generate=false
package v1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	GroupName    = "postgresql.cnpg.io"
	GroupVersion = "v1"
)

var (
	SchemeGroupVersion = schema.GroupVersion{Group: GroupName, Version: GroupVersion}
	SchemeBuilder      = runtime.NewSchemeBuilder(addKnownTypes)
	AddToScheme        = SchemeBuilder.AddToScheme
)

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion,
		&Cluster{},
		&ClusterList{},
	)
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}

// Cluster is a CloudNativePG PostgreSQL cluster.
// +kubebuilder:object:root=true
type Cluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ClusterSpec   `json:"spec,omitempty"`
	Status            ClusterStatus `json:"status,omitempty"`
}

// ClusterList contains a list of Cluster resources.
// +kubebuilder:object:root=true
type ClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Cluster `json:"items"`
}

// ClusterSpec defines a CNPG cluster.
type ClusterSpec struct {
	Instances int32                  `json:"instances,omitempty"`
	ImageName string                 `json:"imageName,omitempty"`
	Storage   StorageConfiguration   `json:"storage,omitempty"`
	Bootstrap BootstrapConfiguration `json:"bootstrap,omitempty"`
}

// StorageConfiguration defines PVC settings.
type StorageConfiguration struct {
	Size string `json:"size,omitempty"`
}

// BootstrapConfiguration defines initial database bootstrap.
type BootstrapConfiguration struct {
	InitDB InitDBConfiguration `json:"initdb,omitempty"`
}

// InitDBConfiguration defines initdb settings.
type InitDBConfiguration struct {
	Database string                      `json:"database,omitempty"`
	Owner    string                      `json:"owner,omitempty"`
	Secret   corev1.LocalObjectReference `json:"secret,omitempty"`
}

// ClusterStatus defines observed CNPG cluster state.
type ClusterStatus struct {
	Phase          string `json:"phase,omitempty"`
	ReadyInstances int32  `json:"readyInstances,omitempty"`
	CurrentPrimary string `json:"currentPrimary,omitempty"`
}

// DefaultStorageSize returns a default 10Gi quantity string.
func DefaultStorageSize() resource.Quantity {
	return resource.MustParse("10Gi")
}
