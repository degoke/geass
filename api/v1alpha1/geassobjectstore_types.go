/*
Copyright 2026 Adegoke Adewoye.

Licensed under the Functional Source License, Version 1.1, ALv2 Future License
(FSL-1.1-ALv2, the "License"); you may not use this file except in compliance
with the License. You may obtain a copy of the License at LICENSE or
https://fsl.software/FSL-1.1-ALv2.template.md.
*/

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// GeassObjectStoreEngine identifies the object store engine type.
// +kubebuilder:validation:Enum=SeaweedFS;Garage;S3
type GeassObjectStoreEngine string

const (
	// ObjectStoreEngineSeaweedFS and ObjectStoreEngineGarage are the servers Geass runs in the cluster.
	// Which one the cluster uses is chosen once, when the cluster's object storage is set up.
	ObjectStoreEngineSeaweedFS GeassObjectStoreEngine = "SeaweedFS"
	ObjectStoreEngineGarage    GeassObjectStoreEngine = "Garage"
	// ObjectStoreEngineS3 is a bucket in the customer's AWS account.
	ObjectStoreEngineS3 GeassObjectStoreEngine = "S3"
)

// GeassObjectStorePlacement selects in-cluster or external object storage.
// +kubebuilder:validation:Enum=InCluster;External
type GeassObjectStorePlacement string

const (
	ObjectStorePlacementInCluster GeassObjectStorePlacement = "InCluster"
	ObjectStorePlacementExternal  GeassObjectStorePlacement = "External"
)

// GeassObjectStoreSpec defines the desired state of GeassObjectStore.
type GeassObjectStoreSpec struct {
	// Project is the owning project. Leave empty for the cluster object storage server.
	// +optional
	Project string `json:"project,omitempty"`
	// Environment is the project environment. Required when Project is set.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=63
	// +optional
	Environment GeassEnvironment `json:"environment,omitempty"`

	// Engine is the object store engine to provision. For a bucket in the cluster it is the engine of the
	// cluster's object storage server. It is chosen once: the server's volume, logins and config belong to the engine
	// it was set up with, so changing it means deleting the object storage and setting it up again.
	// +kubebuilder:validation:Enum=SeaweedFS;Garage;S3
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="engine cannot be changed; delete the object storage and set it up again"
	Engine GeassObjectStoreEngine `json:"engine"`

	// Placement selects the cluster's own object storage server or an external S3 provider.
	// +kubebuilder:validation:Enum=InCluster;External
	// +kubebuilder:default=InCluster
	// +optional
	Placement GeassObjectStorePlacement `json:"placement,omitempty"`

	// ConnectionRef identifies a GeassCloudConnection used for AWS S3.
	// +optional
	ConnectionRef *corev1.LocalObjectReference `json:"connectionRef,omitempty"`

	// Region is the AWS region for external buckets.
	// +optional
	Region string `json:"region,omitempty"`

	// CreateBucket requests that Geass create the named buckets when possible.
	// +optional
	CreateBucket bool `json:"createBucket,omitempty"`

	// Buckets is an optional list of buckets to create on provision.
	// +optional
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:items:MinLength=3
	// +kubebuilder:validation:items:MaxLength=63
	// +kubebuilder:validation:items:Pattern=`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`
	// +kubebuilder:validation:XValidation:rule="self.all(b, !b.matches('^[0-9]+[.][0-9]+[.][0-9]+[.][0-9]+$') && !b.contains('..'))",message="bucket names must not be formatted as an IP address or contain consecutive periods"
	Buckets []string `json:"buckets,omitempty"`

	// Resources is the CPU and memory assigned to the in-cluster object storage server.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// StorageSize is the size of the cluster object storage server's volume. Left unset it is 10Gi. It can
	// be raised later where the cluster's storage can be resized, and never lowered. Only the server has one.
	// +optional
	StorageSize *resource.Quantity `json:"storageSize,omitempty"`

	// Deploy gates bucket provisioning until the user deploys from the dashboard.
	// +optional
	Deploy GeassDeploySpec `json:"deploy,omitempty"`

	// RunOn asks for nodes with particular tags. The cluster object storage server has no project, so
	// this is the only way to choose where it runs.
	// +optional
	RunOn GeassWorkloadPlacement `json:"runOn,omitempty"`

	// ServerRegion is where the cluster object storage server runs. Project buckets follow their project instead.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=40
	// +optional
	ServerRegion string `json:"serverRegion,omitempty"`
}

// GeassObjectStoreStatus defines the observed state of GeassObjectStore.
type GeassObjectStoreStatus struct {
	// TargetNamespace is the namespace workloads were created in.
	// +optional
	TargetNamespace string `json:"targetNamespace,omitempty"`

	// ConnectionSecret is the name of the Secret containing credentials.
	// +optional
	ConnectionSecret string `json:"connectionSecret,omitempty"`

	// Endpoint is the in-cluster S3-compatible endpoint URL.
	// +optional
	Endpoint string `json:"endpoint,omitempty"`

	// ReconcileRetry tracks provision retries before reconciliation gives up.
	// +optional
	ReconcileRetry GeassReconcileRetryStatus `json:"reconcileRetry,omitempty"`

	// Provision tracks deploy attempts and errors for this resource.
	// +optional
	Provision GeassProvisionStatus `json:"provision,omitempty"`

	// conditions represent the current state of the GeassObjectStore resource.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Environment",type=string,JSONPath=`.spec.environment`
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
	Status GeassObjectStoreStatus `json:"status,omitempty"`
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
