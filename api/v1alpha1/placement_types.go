/*
Copyright 2026 Adegoke Adewoye.

Licensed under the Functional Source License, Version 1.1, ALv2 Future License
(FSL-1.1-ALv2, the "License"); you may not use this file except in compliance
with the License. You may obtain a copy of the License at LICENSE or
https://fsl.software/FSL-1.1-ALv2.template.md.
*/

package v1alpha1

// GeassNodeTag is a free-form label on a node, such as "storage" or "gpu".
type GeassNodeTag struct {
	// Name is the tag, a lowercase DNS label.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=40
	Name string `json:"name"`

	// Reserved keeps every workload that does not ask for this tag off the node, so the node is
	// dedicated to the work that does.
	// +optional
	Reserved bool `json:"reserved,omitempty"`

	// Compliance marks the tag as a compliance boundary, such as "hipaa". A compliance tag is always
	// reserved, so nothing but the projects that require it can land on the node.
	// +optional
	Compliance bool `json:"compliance,omitempty"`
}

// GeassNodePlacement says where a node sits and what it is for. The operator writes it onto the
// Kubernetes node as labels (and, for reserved tags, taints).
type GeassNodePlacement struct {
	// Region groups nodes that are close together, such as "us" or "eu". A project pinned to a
	// region only runs on nodes in it.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=40
	// +optional
	Region string `json:"region,omitempty"`

	// Zone is the provider or site inside the region, such as "aws-us-east-1". Replicas of a highly
	// available database are spread across zones.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=40
	// +optional
	Zone string `json:"zone,omitempty"`

	// Tags describe what the node is for.
	// +kubebuilder:validation:MaxItems=16
	// +listType=map
	// +listMapKey=name
	// +optional
	Tags []GeassNodeTag `json:"tags,omitempty"`
}

// GeassWorkloadPlacement lets a resource ask for particular nodes. The region always comes from the
// project; this adds to it.
type GeassWorkloadPlacement struct {
	// Tags are node tags the workload must run on. A node needs all of them.
	// +kubebuilder:validation:MaxItems=8
	// +kubebuilder:validation:items:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:items:MaxLength=40
	// +listType=set
	// +optional
	Tags []string `json:"tags,omitempty"`
}
