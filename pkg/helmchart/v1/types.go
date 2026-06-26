// Package v1 defines K3s HelmChart API types.
// +kubebuilder:object:generate=false
package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	GroupName    = "helm.cattle.io"
	GroupVersion = "v1"
)

var (
	SchemeGroupVersion = schema.GroupVersion{Group: GroupName, Version: GroupVersion}
	SchemeBuilder      = runtime.NewSchemeBuilder(addKnownTypes)
	AddToScheme        = SchemeBuilder.AddToScheme
)

func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion,
		&HelmChart{},
		&HelmChartList{},
	)
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}

// HelmChart is the K3s Helm controller chart resource.
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced
type HelmChart struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              HelmChartSpec   `json:"spec,omitempty"`
	Status            HelmChartStatus `json:"status,omitempty"`
}

// HelmChartList contains a list of HelmChart resources.
// +kubebuilder:object:root=true
type HelmChartList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HelmChart `json:"items"`
}

// HelmChartSpec defines the desired Helm chart installation.
type HelmChartSpec struct {
	Chart           string            `json:"chart,omitempty"`
	Repo            string            `json:"repo,omitempty"`
	Version         string            `json:"version,omitempty"`
	TargetNamespace string            `json:"targetNamespace,omitempty"`
	CreateNamespace bool              `json:"createNamespace,omitempty"`
	ValuesContent   string            `json:"valuesContent,omitempty"`
	JobImage        string            `json:"jobImage,omitempty"`
	Bootstrap       bool              `json:"bootstrap,omitempty"`
	ChartContent    string            `json:"chartContent,omitempty"`
	HelmVersion     string            `json:"helmVersion,omitempty"`
	FailurePolicy   string            `json:"failurePolicy,omitempty"`
	Timeout         string            `json:"timeout,omitempty"`
	Set             map[string]string `json:"set,omitempty"`
}

// HelmChartStatus defines the observed state of a HelmChart.
type HelmChartStatus struct {
	JobName        string               `json:"jobName,omitempty"`
	URL            string               `json:"url,omitempty"`
	Conditions     []HelmChartCondition `json:"conditions,omitempty"`
	Version        int                  `json:"version,omitempty"`
	FailureMessage string               `json:"failureMessage,omitempty"`
}

// HelmChartCondition describes HelmChart readiness.
type HelmChartCondition struct {
	Type               string                 `json:"type,omitempty"`
	Status             metav1.ConditionStatus `json:"status,omitempty"`
	LastTransitionTime metav1.Time            `json:"lastTransitionTime,omitempty"`
	Reason             string                 `json:"reason,omitempty"`
	Message            string                 `json:"message,omitempty"`
}
