package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// GeassEnvironment identifies an opt-in isolated environment within a project.
type GeassEnvironment string

const (
	EnvironmentDev        GeassEnvironment = "dev"
	EnvironmentStaging    GeassEnvironment = "staging"
	EnvironmentProduction GeassEnvironment = "production"
)

type GeassProjectSpec struct {
	// DisplayName is the human-readable project name shown by the dashboard.
	DisplayName string `json:"displayName,omitempty"`
	// ClusterRef identifies the GeassCluster that owns this project.
	// +kubebuilder:validation:Required
	ClusterRef corev1.LocalObjectReference `json:"clusterRef"`
	// Environments are the isolated environments enabled for this project.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Environments []string `json:"environments"`
}

type GeassProjectEnvironmentStatus struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

type GeassProjectStatus struct {
	// ClusterRef is the cluster observed by the controller. It prevents silent project migration.
	ClusterRef   string                          `json:"clusterRef,omitempty"`
	Environments []GeassProjectEnvironmentStatus `json:"environments,omitempty"`
	Conditions   []metav1.Condition              `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
type GeassProject struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitzero"`
	Spec              GeassProjectSpec `json:"spec"`
	// +optional
	Status GeassProjectStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type GeassProjectList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GeassProject `json:"items"`
}

type GeassDeploymentSpec struct {
	App string `json:"app"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Project string `json:"project"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=dev;staging;production
	Environment GeassEnvironment `json:"environment"`
	Image       string           `json:"image"`
	Replicas    *int32           `json:"replicas,omitempty"`
}

type GeassDeploymentStatus struct {
	Phase       string             `json:"phase,omitempty"`
	StartedAt   *metav1.Time       `json:"startedAt,omitempty"`
	CompletedAt *metav1.Time       `json:"completedAt,omitempty"`
	Conditions  []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type GeassDeployment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitzero"`
	Spec              GeassDeploymentSpec `json:"spec"`
	// +optional
	Status GeassDeploymentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type GeassDeploymentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GeassDeployment `json:"items"`
}

type GeassLogicalDatabaseSpec struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Project string `json:"project"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=dev;staging;production
	Environment GeassEnvironment `json:"environment"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ServerRef    string `json:"serverRef"`
	DatabaseName string `json:"databaseName"`
}

type GeassLogicalDatabaseStatus struct {
	ConnectionSecret string             `json:"connectionSecret,omitempty"`
	JobName          string             `json:"jobName,omitempty"`
	Conditions       []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type GeassLogicalDatabase struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitzero"`
	Spec              GeassLogicalDatabaseSpec `json:"spec"`
	// +optional
	Status GeassLogicalDatabaseStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type GeassLogicalDatabaseList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GeassLogicalDatabase `json:"items"`
}

type GeassHAReadinessSpec struct {
	ClusterRef string `json:"clusterRef,omitempty"`
}
type GeassHAReadinessStatus struct {
	HealthyNodes      int32              `json:"healthyNodes,omitempty"`
	StorageClassReady bool               `json:"storageClassReady"`
	ClusterReady      bool               `json:"clusterReady"`
	AddonsReady       bool               `json:"addonsReady"`
	Conditions        []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type GeassHAReadiness struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitzero"`
	Spec              GeassHAReadinessSpec `json:"spec"`
	// +optional
	Status GeassHAReadinessStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type GeassHAReadinessList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GeassHAReadiness `json:"items"`
}

type GeassPlatformConfigSpec struct {
	DefaultDomain string `json:"defaultDomain,omitempty"`
	PrometheusURL string `json:"prometheusURL,omitempty"`
}

type GeassPlatformConfigStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type GeassPlatformConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitzero"`
	Spec              GeassPlatformConfigSpec `json:"spec"`
	// +optional
	Status GeassPlatformConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type GeassPlatformConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GeassPlatformConfig `json:"items"`
}

type GeassCloudProvider string

const CloudProviderAWS GeassCloudProvider = "AWS"

type GeassCloudConnectionSpec struct {
	Provider GeassCloudProvider `json:"provider"`
}
type GeassCloudConnectionStatus struct {
	Available  bool               `json:"available"`
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type GeassCloudConnection struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitzero"`
	Spec              GeassCloudConnectionSpec `json:"spec"`
	// +optional
	Status GeassCloudConnectionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type GeassCloudConnectionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GeassCloudConnection `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &GeassProject{}, &GeassProjectList{}, &GeassDeployment{}, &GeassDeploymentList{}, &GeassLogicalDatabase{}, &GeassLogicalDatabaseList{}, &GeassHAReadiness{}, &GeassHAReadinessList{}, &GeassPlatformConfig{}, &GeassPlatformConfigList{}, &GeassCloudConnection{}, &GeassCloudConnectionList{})
		return nil
	})
}
