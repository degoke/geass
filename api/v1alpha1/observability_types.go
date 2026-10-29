package v1alpha1

import corev1 "k8s.io/api/core/v1"

// GeassObservabilitySpec says where the cluster's metrics and logs go besides the stores the monitoring and logging
// add-ons run in the cluster.
type GeassObservabilitySpec struct {
	// External sends the cluster's metrics and logs to stores outside the cluster as well, and has the dashboard read
	// them from there. It is what lets a dashboard that runs outside the cluster (--controllers=false) show metrics and
	// log history. Unset, everything stays in the cluster, as before.
	// +optional
	External *GeassExternalObservabilitySpec `json:"external,omitempty"`
}

// GeassExternalObservabilitySpec names a metrics store and a log store outside the cluster. Either can be left out, and
// each can be written to, read from, or both.
type GeassExternalObservabilitySpec struct {
	// Metrics is a store that speaks Prometheus remote write and the Prometheus query API, such as Mimir, Cortex,
	// Thanos or VictoriaMetrics.
	// +optional
	Metrics *GeassExternalMetricsSpec `json:"metrics,omitempty"`
	// Logs is a store that speaks VictoriaLogs' JSON lines and LogsQL APIs: VictoriaLogs itself, or something in front
	// of it.
	// +optional
	Logs *GeassExternalLogsSpec `json:"logs,omitempty"`
}

// GeassExternalMetricsSpec is a metrics store outside the cluster. Its credentials are never in this spec: they are the
// keys of the Secret CredentialRef names.
type GeassExternalMetricsSpec struct {
	// RemoteWriteURL receives every sample the cluster's Prometheus keeps, by Prometheus remote write: Mimir's or
	// Cortex's /api/v1/push, VictoriaMetrics' /api/v1/write (with the tenant in the path for its cluster version), or
	// anything else that speaks the protocol. Prometheus keeps its own copy as before. Empty sends nothing. It needs the
	// monitoring add-on.
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:Pattern=`^https?://[^\s]+$`
	// +optional
	RemoteWriteURL string `json:"remoteWriteURL,omitempty"`
	// QueryURL is a Prometheus-compatible query API: the address that /api/v1/query and /api/v1/query_range are added to
	// (for Mimir that is its /prometheus prefix). When it is set, the dashboard reads every metric it shows from it and
	// never from the Prometheus in the cluster. Empty leaves the dashboard reading the cluster's Prometheus.
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:Pattern=`^https?://[^\s]+$`
	// +optional
	QueryURL string `json:"queryURL,omitempty"`
	// Tenant is sent in TenantHeader with every write and every query, for a store that keeps tenants apart by a
	// header. Empty sends no tenant header, for a store that takes the tenant from the URL or from the credentials.
	// +kubebuilder:validation:MaxLength=150
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9!._*'()-]+$`
	// +optional
	Tenant string `json:"tenant,omitempty"`
	// TenantHeader is the header that carries Tenant. Unset means X-Scope-OrgID, which Mimir, Cortex and Loki read;
	// Thanos reads THANOS-TENANT. A header Prometheus sets itself, such as Authorization, cannot be used.
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9-]+$`
	// +optional
	TenantHeader string `json:"tenantHeader,omitempty"`
	// CredentialRef names a Secret in the system namespace that holds how to sign in to the store: a "token" key for a
	// bearer token, or "username" and "password" keys for basic authentication. Empty sends no credentials. Geass
	// copies the keys it uses into a Secret beside Prometheus, which reads them from there.
	// +optional
	CredentialRef *corev1.LocalObjectReference `json:"credentialRef,omitempty"`
}

// GeassExternalLogsSpec is a log store outside the cluster. Its credentials are never in this spec: they are the keys of
// the Secret CredentialRef names.
type GeassExternalLogsSpec struct {
	// PushURL is the address of the store that the log shipper sends every line to, besides the store in the cluster:
	// /insert/jsonline is added to it. Lines of projects that require node tags are not sent, as they are not sent to the
	// store in the cluster. Empty sends nothing. It needs the logging add-on.
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:Pattern=`^https?://[^\s]+$`
	// +optional
	PushURL string `json:"pushURL,omitempty"`
	// QueryURL is the address of the store that answers LogsQL: /select/logsql/query is added to it. When it is set,
	// the dashboard reads log history from it and never from the store in the cluster. Empty leaves the dashboard
	// reading the cluster's store.
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:Pattern=`^https?://[^\s]+$`
	// +optional
	QueryURL string `json:"queryURL,omitempty"`
	// AccountID and ProjectID are the VictoriaLogs tenant the lines are written to and read from, sent as the AccountID
	// and ProjectID headers with every line and every query. Unset means 0, VictoriaLogs' default tenant.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=4294967295
	// +optional
	AccountID int64 `json:"accountID,omitempty"`
	// ProjectID is the second half of the tenant. See AccountID.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=4294967295
	// +optional
	ProjectID int64 `json:"projectID,omitempty"`
	// CredentialRef names a Secret in the system namespace that holds how to sign in to the store: a "token" key for a
	// bearer token, or "username" and "password" keys for basic authentication. Empty sends no credentials. Geass
	// copies the keys it uses into a Secret beside the log shipper, which reads them from there.
	// +optional
	CredentialRef *corev1.LocalObjectReference `json:"credentialRef,omitempty"`
	// RetentionDays is how long the store keeps logs, which the dashboard reports with the history it reads from it.
	// Unset means the logging add-on's retention.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=3650
	// +optional
	RetentionDays int32 `json:"retentionDays,omitempty"`
}
