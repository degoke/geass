package v1alpha1

import (
	"slices"

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
	// Region is where this project's workloads run: only nodes in the region are used. Empty means the
	// cluster's default region. It is chosen when the project is created and cannot change while the
	// project has databases or storage, because their data lives on the nodes.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=40
	// +optional
	Region string `json:"region,omitempty"`
	// RequiredTags are node tags every workload of this project must run on, such as "hipaa". A node
	// needs all of them. Unlike a service's own runOn tags, these apply to everything the project
	// runs: services, databases, buckets, builds, and the jobs that back up and restore them.
	// +kubebuilder:validation:MaxItems=8
	// +kubebuilder:validation:items:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:items:MaxLength=40
	// +listType=set
	// +optional
	RequiredTags []string `json:"requiredTags,omitempty"`
	// Environments are the isolated environments enabled for this project.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:items:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:items:MaxLength=63
	// +kubebuilder:validation:MaxItems=100
	Environments []string `json:"environments"`
	// SharedVariables are environment-scoped values available to project services.
	// +optional
	SharedVariables []GeassSharedVariable `json:"sharedVariables,omitempty"`
	// GitHubConnectionRef references a Secret containing GitHub credentials and webhook data.
	// +optional
	GitHubConnectionRef *corev1.LocalObjectReference `json:"githubConnectionRef,omitempty"`
	// Webhooks receive signed notifications about deployments in this project. The signing secret of
	// each webhook is kept in the Secret <project>-webhooks, never in this spec.
	// +kubebuilder:validation:MaxItems=20
	// +optional
	Webhooks []GeassWebhook `json:"webhooks,omitempty"`
	// PREnvironments creates a temporary copy of an environment for every pull request.
	// +optional
	PREnvironments *GeassPREnvironments `json:"prEnvironments,omitempty"`
	// GitHubReporting turns on what Geass reports back to GitHub for this project's repository services: a
	// commit status for each build, a GitHub Deployment for each deploy, and one comment on each pull request
	// that has an environment. Unset means on. It only does anything once the project is connected to GitHub,
	// and a GitHub App registered before Geass reported needs the new permissions approved first.
	// +kubebuilder:default=true
	// +optional
	GitHubReporting *bool `json:"githubReporting,omitempty"`
	// BackupStorage is where this project's databases keep their backups. Without it, databases
	// that have backups turned on report that they need storage.
	// +optional
	BackupStorage *GeassBackupStorage `json:"backupStorage,omitempty"`
	// Network sets who can reach this project's services. Without it the project is isolated.
	// +optional
	Network *GeassProjectNetwork `json:"network,omitempty"`
	// EnvironmentQuota caps what each environment of this project can use. Without it nothing is capped
	// and one project can use all the room the cluster has.
	// +optional
	EnvironmentQuota *GeassEnvironmentQuota `json:"environmentQuota,omitempty"`
	// ProjectQuota caps what the whole project can ask for, added up over its environments. It can be set
	// with an environment quota, and an environment is held to whichever of the two is lower.
	// +optional
	ProjectQuota *GeassProjectQuota `json:"projectQuota,omitempty"`
}

// GeassWebhook is an outbound webhook for project events.
type GeassWebhook struct {
	// ID identifies the webhook in the dashboard and in delivery records.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]{8}$`
	ID string `json:"id"`
	// URL receives a POST for each event.
	// +kubebuilder:validation:MinLength=8
	// +kubebuilder:validation:MaxLength=2048
	URL string `json:"url"`
	// Events limits which events are sent. Empty means every event.
	// +kubebuilder:validation:items:Enum=deployment.started;deployment.succeeded;deployment.failed
	// +optional
	Events []string `json:"events,omitempty"`
	// Disabled pauses deliveries without deleting the webhook.
	// +optional
	Disabled bool `json:"disabled,omitempty"`
}

// GeassWebhookDelivery records the outcome of one webhook delivery.
type GeassWebhookDelivery struct {
	WebhookID      string      `json:"webhookId"`
	Event          string      `json:"event"`
	DeliveryID     string      `json:"deliveryId"`
	StatusCode     int32       `json:"statusCode,omitempty"`
	Error          string      `json:"error,omitempty"`
	Attempts       int32       `json:"attempts"`
	DurationMillis int64       `json:"durationMillis"`
	Time           metav1.Time `json:"time"`
}

// GeassPREnvironments configures pull request environments.
type GeassPREnvironments struct {
	// Enabled turns pull request environments on.
	Enabled bool `json:"enabled"`
	// BaseEnvironment is copied for every pull request.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=63
	// +optional
	BaseEnvironment string `json:"baseEnvironment,omitempty"`
	// MaxOpen limits how many pull request environments can exist at once.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=25
	// +kubebuilder:default=5
	// +optional
	MaxOpen int32 `json:"maxOpen,omitempty"`
}

// GeassPullRequestEnvironment records an environment created for a pull request.
type GeassPullRequestEnvironment struct {
	Number      int32       `json:"number"`
	Repository  string      `json:"repository"`
	Branch      string      `json:"branch"`
	Environment string      `json:"environment"`
	URL         string      `json:"url,omitempty"`
	OpenedAt    metav1.Time `json:"openedAt"`
}

type GeassProjectEnvironmentStatus struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

type GeassProjectStatus struct {
	// ClusterRef is the cluster observed by the controller. It prevents silent project migration.
	ClusterRef string `json:"clusterRef,omitempty"`
	// +kubebuilder:validation:MaxItems=100
	Environments []GeassProjectEnvironmentStatus `json:"environments,omitempty"`
	// ReconcileRetry tracks provision retries before reconciliation gives up.
	// +optional
	ReconcileRetry GeassReconcileRetryStatus `json:"reconcileRetry,omitempty"`
	Conditions     []metav1.Condition        `json:"conditions,omitempty"`
	// WebhookDeliveries are the most recent webhook deliveries, newest first.
	// +kubebuilder:validation:MaxItems=20
	// +optional
	WebhookDeliveries []GeassWebhookDelivery `json:"webhookDeliveries,omitempty"`
	// PullRequests are the open pull request environments.
	// +kubebuilder:validation:MaxItems=50
	// +optional
	PullRequests []GeassPullRequestEnvironment `json:"pullRequests,omitempty"`
	// QuotaUsage is what the project's environments ask for together. Present while the project has a
	// project quota, and as current as the last time an environment's usage changed.
	// +optional
	QuotaUsage *GeassQuotaUsage `json:"quotaUsage,omitempty"`
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

// MaxCIChecks is how many CI check names a build keeps in its status.
const MaxCIChecks = 50

type GeassBuildStatus struct {
	Phase  GeassBuildPhase `json:"phase,omitempty"`
	JobRef string          `json:"jobRef,omitempty"`
	// Image is the destination the build pushes to, as it was told to (spec.registry).
	Image string `json:"image,omitempty"`
	// ImageDigest is the sha256 digest of the image the last run pushed. A run that is started again clears
	// it, and the digest recorded when that run ends is the one its own pod reports.
	ImageDigest   string       `json:"imageDigest,omitempty"`
	LogRef        string       `json:"logRef,omitempty"`
	FailureReason string       `json:"failureReason,omitempty"`
	RetryCount    int32        `json:"retryCount,omitempty"`
	StartedAt     *metav1.Time `json:"startedAt,omitempty"`
	CompletedAt   *metav1.Time `json:"completedAt,omitempty"`
	CIState       string       `json:"ciState,omitempty"`
	// CIChecks are the names of the checks that ran. At most MaxCIChecks are kept: a monorepo can run
	// hundreds, and a status that grows with the repository is not one to store in etcd.
	// +kubebuilder:validation:MaxItems=50
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
	ServerRef string `json:"serverRef"`
	// DatabaseName is what the database is called in the app's own terms. On a project's own server it is
	// the name of the database there. On the cluster-wide server, which many projects share, it is
	// prefixed with the project and environment so names never collide, and the database gets a login of
	// its own that can reach nothing else.
	DatabaseName string `json:"databaseName"`
	// ConnectionLimit caps how many connections the database's own login may hold on a shared server, so
	// one project cannot use up all of them. It does not apply on a project's own server.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=1000
	// +optional
	ConnectionLimit int32 `json:"connectionLimit,omitempty"`
}

type GeassLogicalDatabaseStatus struct {
	ConnectionSecret string `json:"connectionSecret,omitempty"`
	JobName          string `json:"jobName,omitempty"`
	// Database is the database's real name on the server; on a shared server it is not DatabaseName.
	// +optional
	Database string `json:"database,omitempty"`
	// Username is the login that owns the database on a shared server.
	// +optional
	Username string `json:"username,omitempty"`
	// ReconcileRetry tracks provision retries before reconciliation gives up.
	// +optional
	ReconcileRetry GeassReconcileRetryStatus `json:"reconcileRetry,omitempty"`
	// Provision tracks deploy attempts and errors for this resource.
	// +optional
	Provision  GeassProvisionStatus `json:"provision,omitempty"`
	Conditions []metav1.Condition   `json:"conditions,omitempty"`
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
	GitHubAppRef *corev1.LocalObjectReference `json:"githubAppRef,omitempty"`
	// CloudflareConnectionRef references a Secret with Cloudflare API credentials (apiToken, accountId).
	// +optional
	CloudflareConnectionRef *corev1.LocalObjectReference `json:"cloudflareConnectionRef,omitempty"`
	// CloudflareZoneID is the DNS zone used for automatic records when Cloudflare is connected.
	// +optional
	CloudflareZoneID  string                     `json:"cloudflareZoneID,omitempty"`
	Registry          GeassPlatformRegistrySpec  `json:"registry,omitempty"`
	TraefikAccessLogs GeassTraefikAccessLogsSpec `json:"traefikAccessLogs,omitempty"`
	// Auth says how people sign in to the dashboard: with the users in the Secret geass-dashboard-auth (the
	// default), or through an identity provider.
	// +optional
	Auth *GeassPlatformAuthSpec `json:"auth,omitempty"`
	// Alerting says where Geass tells people that something went wrong: a build or deploy that failed, a service
	// that is down, backups that are failing, a node that is not ready.
	// +optional
	Alerting *GeassAlertingSpec `json:"alerting,omitempty"`
}

// GeassPlatformAuthSpec says whose word Geass takes for who a person is.
type GeassPlatformAuthSpec struct {
	// Mode is local, where the users are the ones in the Secret geass-dashboard-auth, or external, where an
	// identity provider that speaks OpenID Connect decides who may sign in and with which role. Unset means
	// local. A user of the Secret who has a password and the admin role can still sign in while the mode is
	// external, so that a provider that is down or set up wrong does not lock everyone out.
	// +kubebuilder:validation:Enum=local;external
	// +kubebuilder:default=local
	// +optional
	Mode string `json:"mode,omitempty"`
	// OIDC is the identity provider. It is used only while Mode is external, but it can be filled in and tested
	// before that.
	// +optional
	OIDC *GeassPlatformOIDCSpec `json:"oidc,omitempty"`
}

// GeassPlatformOIDCSpec says how to reach an OpenID Connect identity provider and how its claims become a Geass
// role. The client secret is never in this spec: it is the "clientSecret" key of the Secret CredentialRef names.
type GeassPlatformOIDCSpec struct {
	// DisplayName is what the sign-in button calls the provider ("Sign in with Acme").
	// +kubebuilder:validation:MaxLength=60
	// +optional
	DisplayName string `json:"displayName,omitempty"`
	// Issuer is the provider's issuer URL, exactly as its discovery document names it. It must be https, and
	// cannot be this machine or a link-local address.
	// +kubebuilder:validation:MaxLength=2048
	// +optional
	Issuer string `json:"issuer,omitempty"`
	// ClientID is the id Geass was registered with at the provider.
	// +kubebuilder:validation:MaxLength=512
	// +optional
	ClientID string `json:"clientID,omitempty"`
	// CredentialRef names the Secret in the system namespace whose "clientSecret" key is the client secret.
	// Leave it empty for a public client that relies on PKCE alone.
	// +optional
	CredentialRef *corev1.LocalObjectReference `json:"credentialRef,omitempty"`
	// Scopes are asked for besides openid. Empty means email and profile.
	// +kubebuilder:validation:MaxItems=20
	// +optional
	Scopes []string `json:"scopes,omitempty"`
	// RoleClaim is the claim of the ID token that holds a person's role at the provider: a name, or a dotted
	// path into nested claims. Its value may be a string, a list, or a comma-separated string.
	// +kubebuilder:validation:MaxLength=253
	// +optional
	RoleClaim string `json:"roleClaim,omitempty"`
	// RoleMap says which Geass role (admin or viewer) each role at the provider is. A person whose roles map to
	// none cannot sign in.
	// +kubebuilder:validation:MaxProperties=50
	// +optional
	RoleMap map[string]string `json:"roleMap,omitempty"`
	// RequireClaim limits sign-in to people whose ID token has each of these claims with the value given, for
	// example {"organization_id": "org_123"} to let only the members of one organization in.
	// +kubebuilder:validation:MaxProperties=10
	// +optional
	RequireClaim map[string]string `json:"requireClaim,omitempty"`
	// SessionMinutes is how long a sign-in lasts before the person is sent back to the provider, which is how a
	// role that changed or a person who was removed there is noticed here.
	// +kubebuilder:validation:Minimum=5
	// +kubebuilder:validation:Maximum=720
	// +kubebuilder:default=60
	// +optional
	SessionMinutes int32 `json:"sessionMinutes,omitempty"`
	// RevokeTokensOnLogout is whether a back-channel logout from the provider (someone signed out there, or was removed)
	// also revokes the API tokens and deploy hooks that person made. It always ends their sessions here. Unset means true,
	// which is the safe choice for offboarding: a provider cannot say whether someone signed out or was removed. Turn it off
	// when tokens that CI depends on are made from the accounts of people who sign out; their tokens then last until they
	// expire or are revoked by hand.
	// +kubebuilder:default=true
	// +optional
	RevokeTokensOnLogout *bool `json:"revokeTokensOnLogout,omitempty"`
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
	// CloudflareTunnelID is the managed Cloudflare tunnel UUID when the connector is enabled.
	// +optional
	CloudflareTunnelID string             `json:"cloudflareTunnelID,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
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
	// The providers below create machines. They are the compute providers of pkg/compute.
	CloudProviderGCP          GeassCloudProvider = "GCP"
	CloudProviderDigitalOcean GeassCloudProvider = "DigitalOcean"
	CloudProviderHetzner      GeassCloudProvider = "Hetzner"
	CloudProviderOVHcloud     GeassCloudProvider = "OVHcloud"
	CloudProviderVultr        GeassCloudProvider = "Vultr"
	CloudProviderLinode       GeassCloudProvider = "Linode"
	CloudProviderScaleway     GeassCloudProvider = "Scaleway"
	CloudProviderAzure        GeassCloudProvider = "Azure"
	CloudProviderOCI          GeassCloudProvider = "OCI"
	CloudProviderUpCloud      GeassCloudProvider = "UpCloud"
	// CloudProviderWebhook creates machines through HTTP endpoints the user runs, for a provider Geass has no
	// driver for.
	CloudProviderWebhook GeassCloudProvider = "Webhook"
)

// GeassCloudPurpose is what a connection is used for.
// +kubebuilder:validation:Enum=Storage;Database;Compute
type GeassCloudPurpose string

const (
	CloudPurposeStorage  GeassCloudPurpose = "Storage"
	CloudPurposeDatabase GeassCloudPurpose = "Database"
	// CloudPurposeCompute lets node pools create machines with the connection. A connection for compute belongs to
	// the cluster, not to a project, and only an admin can use it.
	CloudPurposeCompute GeassCloudPurpose = "Compute"
)

type GeassCloudConnectionSpec struct {
	// Provider is the external cloud or database vendor.
	// +kubebuilder:validation:Enum=AWS;PlanetScale;GCP;DigitalOcean;Hetzner;OVHcloud;Vultr;Linode;Scaleway;Azure;OCI;UpCloud;Webhook
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
	// Purposes say what the connection is for. Left out, a connection is for what its provider did before
	// purposes existed: storage and databases for AWS, databases for PlanetScale. A connection for Compute must
	// not be scoped to a project.
	// +kubebuilder:validation:MaxItems=3
	// +listType=set
	// +optional
	Purposes []GeassCloudPurpose `json:"purposes,omitempty"`
}
type GeassCloudConnectionStatus struct {
	Available  bool               `json:"available"`
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// Account names the account, project or tenant the credentials act on, as the provider reports it.
	// +optional
	Account string `json:"account,omitempty"`
	// Message says what the last check found, or why it could not tell.
	// +optional
	Message string `json:"message,omitempty"`
	// VerifiedAt is when the credentials were last checked with the provider.
	// +optional
	VerifiedAt *metav1.Time `json:"verifiedAt,omitempty"`
	// ExpiresAt is when the credentials stop working, when the provider says so.
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`
	// Warnings are things that work now and are likely to fail later.
	// +kubebuilder:validation:MaxItems=10
	// +optional
	Warnings []string `json:"warnings,omitempty"`
}

// HasPurpose reports whether the connection is for the purpose. A connection that lists none is for what its
// provider was for before purposes existed.
func (c *GeassCloudConnection) HasPurpose(purpose GeassCloudPurpose) bool {
	if len(c.Spec.Purposes) == 0 {
		switch c.Spec.Provider {
		case CloudProviderAWS:
			return purpose == CloudPurposeStorage || purpose == CloudPurposeDatabase
		case CloudProviderPlanetScale:
			return purpose == CloudPurposeDatabase
		default:
			return purpose == CloudPurposeCompute
		}
	}
	return slices.Contains(c.Spec.Purposes, purpose)
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
