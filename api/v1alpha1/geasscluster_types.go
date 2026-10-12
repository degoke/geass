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

// GeassClusterCertManagerAddon controls cert-manager installation.
type GeassClusterCertManagerAddon struct {
	// Enabled installs cert-manager when true.
	// +kubebuilder:default=true
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// ACMEEmail is the contact address Let's Encrypt knows the cluster's ACME account by, and where it
	// sends expiry notices. When it is set, Geass keeps the letsencrypt-prod ClusterIssuer that every
	// ingress asks for in place; when it is empty, no issuer is created and no HTTPS certificate can be
	// issued, which the ClusterIssuerReady condition reports. The installer sets it from GEASS_ACME_EMAIL.
	// Let's Encrypt refuses placeholder domains such as example.com.
	// +kubebuilder:validation:MaxLength=254
	// +kubebuilder:validation:Pattern=`^([^@\s]+@[^@\s]+\.[^@\s]+)?$`
	// +optional
	ACMEEmail string `json:"acmeEmail,omitempty"`
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

// GeassClusterLonghornAddon controls the Longhorn block storage engine that backs
// database volumes. Geass creates and selects the storage classes itself.
type GeassClusterLonghornAddon struct {
	// Enabled installs Longhorn and the Geass-managed storage classes. It defaults to
	// off because Longhorn needs iSCSI on every node, which clusters such as kind do
	// not provide; the installer turns it on for the clusters it builds.
	// +kubebuilder:default=false
	// +optional
	Enabled *bool `json:"enabled,omitempty"`
}

// GeassClusterLoggingAddon controls log collection and search: a Fluent Bit shipper on every node and a
// VictoriaLogs server that keeps what it ships, so logs survive the pods that wrote them.
type GeassClusterLoggingAddon struct {
	// Enabled installs log collection. It is opt-in: it adds a pod per node and a server with a volume, which a
	// small cluster may not want, and it is not installed unless asked for. The installer turns it on.
	// +kubebuilder:default=false
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// RetentionDays is how long logs are kept before they are deleted.
	// +kubebuilder:default=7
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=90
	// +optional
	RetentionDays int32 `json:"retentionDays,omitempty"`
}

// GeassClusterServerlessAddon controls scale to zero: KEDA and its HTTP add-on, which hold a request while a
// sleeping service starts, and put it to sleep again when it has been idle.
type GeassClusterServerlessAddon struct {
	// Enabled installs KEDA and the HTTP add-on. It is opt-in: it adds a few small pods, and changes the
	// ingress controller's settings so it can route to the add-on. Services can then ask to scale to zero.
	// Turning it off stops services from scaling to zero but never uninstalls KEDA: services that were asleep are
	// woken and run normally.
	// +kubebuilder:default=false
	// +optional
	Enabled *bool `json:"enabled,omitempty"`
}

// GeassClusterAddonsSpec groups platform add-on toggles.
type GeassClusterAddonsSpec struct {
	// CertManager controls cert-manager installation.
	// +optional
	CertManager GeassClusterCertManagerAddon `json:"certManager,omitempty"`

	// Monitoring controls kube-prometheus-stack installation.
	// +optional
	Monitoring GeassClusterMonitoringAddon `json:"monitoring,omitempty"`

	// Longhorn controls the Longhorn storage engine installation.
	// +optional
	Longhorn GeassClusterLonghornAddon `json:"longhorn,omitempty"`

	// Logging controls log collection and search.
	// +optional
	Logging GeassClusterLoggingAddon `json:"logging,omitempty"`

	// Serverless controls scale to zero.
	// +optional
	Serverless GeassClusterServerlessAddon `json:"serverless,omitempty"`
}

// GeassClusterNetworkSpec says how the cluster's nodes reach each other.
type GeassClusterNetworkSpec struct {
	// MultiSite means the nodes are at different providers and reach each other over the internet, so
	// the overlay network is encrypted (k3s's WireGuard backend) and every node advertises its public
	// IP. It is chosen when the cluster is installed, because switching means restarting every node,
	// and workers are joined to match it.
	// +optional
	MultiSite bool `json:"multiSite,omitempty"`
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

	// Network says how the nodes reach each other.
	// +optional
	Network GeassClusterNetworkSpec `json:"network,omitempty"`

	// DefaultRegion is where projects that do not choose a region run. Left empty, such projects
	// may run on any node.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=40
	// +optional
	DefaultRegion string `json:"defaultRegion,omitempty"`
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

	// ReconcileRetry tracks provision retries before reconciliation gives up.
	// +optional
	ReconcileRetry GeassReconcileRetryStatus `json:"reconcileRetry,omitempty"`
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
	// +optional
	Status GeassClusterStatus `json:"status,omitempty"`
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

// LoggingEnabled reports whether log collection was explicitly turned on. Like Longhorn it is opt-in, so
// existing clusters and test fixtures never start collecting logs by accident.
func LoggingEnabled(spec GeassClusterAddonsSpec) bool {
	return spec.Logging.Enabled != nil && *spec.Logging.Enabled
}

// DefaultLogRetentionDays is how long logs are kept when no retention is chosen.
const DefaultLogRetentionDays = 7

// LogRetentionDays is the retention to apply: the chosen one, or the default.
func LogRetentionDays(spec GeassClusterAddonsSpec) int32 {
	if spec.Logging.RetentionDays > 0 {
		return spec.Logging.RetentionDays
	}
	return DefaultLogRetentionDays
}

// ServerlessEnabled reports whether scale to zero was explicitly turned on. Like Longhorn and logging it is
// opt-in: an unset value means "not installed".
func ServerlessEnabled(spec GeassClusterAddonsSpec) bool {
	return spec.Serverless.Enabled != nil && *spec.Serverless.Enabled
}

// LonghornEnabled reports whether Longhorn was explicitly turned on. Unlike the
// other add-ons it is opt-in: an unset value means "not installed", so existing
// clusters and test fixtures never start installing a storage engine by accident.
func LonghornEnabled(spec GeassClusterAddonsSpec) bool {
	return spec.Longhorn.Enabled != nil && *spec.Longhorn.Enabled
}
