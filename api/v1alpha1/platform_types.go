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

// DashboardExposure selects how the Geass dashboard is published on the network.
type DashboardExposure string

const (
	DashboardExposureIngress          DashboardExposure = "ingress"
	DashboardExposureCloudflareTunnel DashboardExposure = "cloudflare-tunnel"
)

// GeassSharedVariable is a project-scoped value materialized into each selected
// environment. Secret values are written to a Kubernetes Secret and are never
// copied into application status.
type GeassSharedVariable struct {
	// Name is the environment variable name.
	// +kubebuilder:validation:Pattern=`^[A-Z_][A-Z0-9_]*$`
	Name string `json:"name"`
	// Environment scopes the variable to one project environment.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=63
	Environment string `json:"environment"`
	// Value is the non-secret value to materialize.
	// +optional
	Value string `json:"value,omitempty"`
	// SecretRef points to a Kubernetes Secret containing the value when it is sensitive.
	// +optional
	SecretRef *corev1.SecretKeySelector `json:"secretRef,omitempty"`
}

type GeassProjectSpec struct {
	// DisplayName is the user-facing project name shown by the dashboard (lowercase slug, e.g. morning-beach).
	DisplayName string `json:"displayName,omitempty"`
	// ClusterRef identifies the GeassCluster that owns this project.
	// +kubebuilder:validation:Required
	ClusterRef corev1.LocalObjectReference `json:"clusterRef"`
	// Environments are the isolated environments enabled for this project.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:items:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:items:MaxLength=63
	Environments []string `json:"environments"`
	// SharedVariables are environment-scoped values available to project services.
	// +optional
	SharedVariables []GeassSharedVariable `json:"sharedVariables,omitempty"`
	// GitHubConnectionRef references a Secret containing GitHub credentials and webhook data.
	// +optional
	GitHubConnectionRef *corev1.LocalObjectReference `json:"githubConnectionRef,omitempty"`
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

// GeassGitHubConnection stores only references to authentication material. The
// token and webhook secret remain in Kubernetes Secrets and are never exposed
// through application resources or status.
type GeassGitHubConnectionSpec struct {
	SecretRef        corev1.LocalObjectReference  `json:"secretRef"`
	WebhookSecretRef *corev1.LocalObjectReference `json:"webhookSecretRef,omitempty"`
}

type GeassGitHubConnectionStatus struct {
	Ready      bool               `json:"ready"`
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type GeassGitHubConnection struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitzero"`
	Spec              GeassGitHubConnectionSpec `json:"spec"`
	// +optional
	Status GeassGitHubConnectionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type GeassGitHubConnectionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GeassGitHubConnection `json:"items"`
}

type GeassDeploymentSpec struct {
	App string `json:"app"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Project string `json:"project"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=63
	Environment GeassEnvironment `json:"environment"`
	Image       string           `json:"image"`
	Replicas    *int32           `json:"replicas,omitempty"`
	// ChangeTitle is the human-readable reason for this deployment.
	// +optional
	ChangeTitle string `json:"changeTitle,omitempty"`
	// Source identifies the system that initiated the deployment.
	// +optional
	Source string `json:"source,omitempty"`
	// Actor identifies the user or automation actor when known.
	// +optional
	Actor string `json:"actor,omitempty"`
	// Commit is the source revision when a repository-backed deployment provides one.
	// +optional
	Commit      string `json:"commit,omitempty"`
	BuildRef    string `json:"buildRef,omitempty"`
	ImageDigest string `json:"imageDigest,omitempty"`
	Trigger     string `json:"trigger,omitempty"`
	EventLogRef string `json:"eventLogRef,omitempty"`
	RollbackRef string `json:"rollbackRef,omitempty"`
}

type GeassDeploymentStatus struct {
	Phase             string             `json:"phase,omitempty"`
	StartedAt         *metav1.Time       `json:"startedAt,omitempty"`
	CompletedAt       *metav1.Time       `json:"completedAt,omitempty"`
	Conditions        []metav1.Condition `json:"conditions,omitempty"`
	AvailableReplicas int32              `json:"availableReplicas,omitempty"`
	HealthCheck       string             `json:"healthCheck,omitempty"`
}

type GeassBuildPhase string

const (
	GeassBuildPending      GeassBuildPhase = "Pending"
	GeassBuildWaitingForCI GeassBuildPhase = "WaitingForCI"
	GeassBuildRunning      GeassBuildPhase = "Running"
	GeassBuildSucceeded    GeassBuildPhase = "Succeeded"
	GeassBuildFailed       GeassBuildPhase = "Failed"
	GeassBuildCancelled    GeassBuildPhase = "Cancelled"
)

type GeassBuildSpec struct {
	App              string                       `json:"app"`
	Project          string                       `json:"project"`
	Environment      GeassEnvironment             `json:"environment"`
	Repository       string                       `json:"repository,omitempty"`
	Revision         string                       `json:"revision,omitempty"`
	Branch           string                       `json:"branch,omitempty"`
	Dockerfile       string                       `json:"dockerfile,omitempty"`
	Context          string                       `json:"context,omitempty"`
	WaitForCI        bool                         `json:"waitForCI,omitempty"`
	ConnectionRef    *corev1.LocalObjectReference `json:"connectionRef,omitempty"`
	GitCredentialRef *corev1.LocalObjectReference `json:"gitCredentialRef,omitempty"`
	Registry         string                       `json:"registry,omitempty"`
	CredentialRef    *corev1.LocalObjectReference `json:"credentialRef,omitempty"`
	Cache            bool                         `json:"cache,omitempty"`
	LogStoreRef      *corev1.LocalObjectReference `json:"logStoreRef,omitempty"`
}

type GeassBuildStatus struct {
	Phase           GeassBuildPhase    `json:"phase,omitempty"`
	JobRef          string             `json:"jobRef,omitempty"`
	Image           string             `json:"image,omitempty"`
	ImageDigest     string             `json:"imageDigest,omitempty"`
	LogRef          string             `json:"logRef,omitempty"`
	FailureReason   string             `json:"failureReason,omitempty"`
	RetryCount      int32              `json:"retryCount,omitempty"`
	StartedAt       *metav1.Time       `json:"startedAt,omitempty"`
	CompletedAt     *metav1.Time       `json:"completedAt,omitempty"`
	CIState         string             `json:"ciState,omitempty"`
	CIChecks        []string           `json:"ciChecks,omitempty"`
	SourceRevision  string             `json:"sourceRevision,omitempty"`
	CancelRequested bool               `json:"cancelRequested,omitempty"`
	LastLogLine     string             `json:"lastLogLine,omitempty"`
	Conditions      []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type GeassBuild struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitzero"`
	Spec              GeassBuildSpec `json:"spec"`
	// +optional
	Status GeassBuildStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type GeassBuildList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GeassBuild `json:"items"`
}

type GeassConsoleSessionPhase string

const (
	GeassConsolePending GeassConsoleSessionPhase = "Pending"
	GeassConsoleActive  GeassConsoleSessionPhase = "Active"
	GeassConsoleExpired GeassConsoleSessionPhase = "Expired"
	GeassConsoleClosed  GeassConsoleSessionPhase = "Closed"
)

type GeassConsoleSessionSpec struct {
	App            string           `json:"app"`
	Project        string           `json:"project"`
	Environment    GeassEnvironment `json:"environment"`
	Pod            string           `json:"pod"`
	Container      string           `json:"container"`
	Actor          string           `json:"actor,omitempty"`
	Command        []string         `json:"command,omitempty"`
	TimeoutSeconds int32            `json:"timeoutSeconds,omitempty"`
}

type GeassConsoleSessionStatus struct {
	Phase     GeassConsoleSessionPhase `json:"phase,omitempty"`
	StartedAt *metav1.Time             `json:"startedAt,omitempty"`
	EndedAt   *metav1.Time             `json:"endedAt,omitempty"`
	Reason    string                   `json:"reason,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type GeassConsoleSession struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitzero"`
	Spec              GeassConsoleSessionSpec `json:"spec"`
	// +optional
	Status GeassConsoleSessionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type GeassConsoleSessionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GeassConsoleSession `json:"items"`
}

// GeassNetworkLog is a structured Traefik HTTP access-log record. It contains
// request metadata only; request and response bodies are never stored.
type GeassNetworkLogSpec struct {
	Project         string           `json:"project"`
	Environment     GeassEnvironment `json:"environment"`
	App             string           `json:"app"`
	Timestamp       metav1.Time      `json:"timestamp"`
	Host            string           `json:"host,omitempty"`
	Method          string           `json:"method,omitempty"`
	Path            string           `json:"path,omitempty"`
	Status          int32            `json:"status,omitempty"`
	LatencyMillis   int64            `json:"latencyMillis,omitempty"`
	UpstreamOutcome string           `json:"upstreamOutcome,omitempty"`
}

// +kubebuilder:object:root=true
type GeassNetworkLog struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitzero"`
	Spec              GeassNetworkLogSpec `json:"spec"`
}

// +kubebuilder:object:root=true
type GeassNetworkLogList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []GeassNetworkLog `json:"items"`
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
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=63
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
	// DashboardURL is the public HTTPS URL where the Geass dashboard is reachable.
	// It is used for GitHub App callback and webhook URLs.
	// +kubebuilder:validation:Pattern=`^https://[a-zA-Z0-9.-]+(?::[0-9]+)?$`
	// +optional
	DashboardURL string `json:"dashboardURL,omitempty"`
	// RootDomain is the operator's apex domain (e.g. example.com). Geass serves the
	// dashboard at geass.<rootDomain> after DNS verification.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`
	// +optional
	RootDomain string `json:"rootDomain,omitempty"`
	// DashboardExposure selects how the dashboard is published (ingress on a server or Cloudflare Tunnel for local dev).
	// +kubebuilder:validation:Enum=ingress;cloudflare-tunnel
	// +kubebuilder:default=ingress
	// +optional
	DashboardExposure DashboardExposure `json:"dashboardExposure,omitempty"`
	// TunnelCNAMETarget is the Cloudflare tunnel hostname (uuid.cfargotunnel.com) for dashboardExposure=cloudflare-tunnel.
	// +kubebuilder:validation:Pattern=`^[a-z0-9-]+(\.[a-z0-9-]+)+$`
	// +optional
	TunnelCNAMETarget string `json:"tunnelCNAMETarget,omitempty"`
	DefaultDomain     string `json:"defaultDomain,omitempty"`
	PrometheusURL     string `json:"prometheusURL,omitempty"`
	// DefaultClusterRef identifies the cluster new projects should use.
	// +optional
	DefaultClusterRef corev1.LocalObjectReference `json:"defaultClusterRef,omitempty"`
	// GitHubAppRef references a Secret containing the platform GitHub App credentials.
	// +optional
	GitHubAppRef      *corev1.LocalObjectReference `json:"githubAppRef,omitempty"`
	Registry          GeassPlatformRegistrySpec    `json:"registry,omitempty"`
	TraefikAccessLogs GeassTraefikAccessLogsSpec   `json:"traefikAccessLogs,omitempty"`
}

type GeassTraefikAccessLogsSpec struct {
	Enabled            bool   `json:"enabled,omitempty"`
	Format             string `json:"format,omitempty"`
	NetworkLogEndpoint string `json:"networkLogEndpoint,omitempty"`
}

type GeassPlatformRegistrySpec struct {
	Repository    string                       `json:"repository,omitempty"`
	CredentialRef *corev1.LocalObjectReference `json:"credentialRef,omitempty"`
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

const (
	CloudProviderAWS         GeassCloudProvider = "AWS"
	CloudProviderPlanetScale GeassCloudProvider = "PlanetScale"
)

type GeassCloudConnectionSpec struct {
	// Provider is the external cloud or database vendor.
	// +kubebuilder:validation:Enum=AWS;PlanetScale
	Provider GeassCloudProvider `json:"provider"`
	// SecretRef stores provider credentials. Secret values are never copied into status.
	SecretRef corev1.LocalObjectReference `json:"secretRef"`
	// Project optionally scopes this connection to a single Geass project.
	// +optional
	Project string `json:"project,omitempty"`
	// Region is the default AWS region for this connection.
	// +optional
	Region string `json:"region,omitempty"`
	// Organization is the PlanetScale organization slug.
	// +optional
	Organization string `json:"organization,omitempty"`
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
		s.AddKnownTypes(SchemeGroupVersion, &GeassProject{}, &GeassProjectList{}, &GeassGitHubConnection{}, &GeassGitHubConnectionList{}, &GeassDeployment{}, &GeassDeploymentList{}, &GeassBuild{}, &GeassBuildList{}, &GeassConsoleSession{}, &GeassConsoleSessionList{}, &GeassNetworkLog{}, &GeassNetworkLogList{}, &GeassLogicalDatabase{}, &GeassLogicalDatabaseList{}, &GeassHAReadiness{}, &GeassHAReadinessList{}, &GeassPlatformConfig{}, &GeassPlatformConfigList{}, &GeassCloudConnection{}, &GeassCloudConnectionList{})
		return nil
	})
}
