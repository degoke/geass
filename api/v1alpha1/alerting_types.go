package v1alpha1

import corev1 "k8s.io/api/core/v1"

// What Geass can alert about. A channel subscribes to some of these, or to all of them.
const (
	// AlertBuildFailed is a build of a repository service that failed.
	AlertBuildFailed = "build.failed"
	// AlertDeployFailed is a service, function, database or bucket that could not be deployed and that Geass gave up on.
	AlertDeployFailed = "deploy.failed"
	// AlertServiceDown is a service that should be running and has no replica that is ready.
	AlertServiceDown = "service.down"
	// AlertBackupFailed is a database whose backups are failing, so recent changes may not be recoverable.
	AlertBackupFailed = "backup.failed"
	// AlertNodeUnhealthy is a node of the cluster that is not ready.
	AlertNodeUnhealthy = "node.unhealthy"
	// AlertClusterIssue is a problem of the whole cluster: an upgrade that stopped, certificates that cannot be
	// issued, network policies that are not enforced, or the operator itself not doing its work.
	AlertClusterIssue = "cluster.issue"
	// AlertWorkloadCrashLooping is a pod that keeps crashing and being restarted.
	AlertWorkloadCrashLooping = "workload.crashlooping"
	// AlertStorageFilling is a volume or a node's disk that is nearly full.
	AlertStorageFilling = "storage.filling"
	// AlertStorageFailing is a node's disk that is failing (it has gone read-only), or a volume that has lost a copy of its
	// data or cannot be read at all.
	AlertStorageFailing = "storage.failing"
)

// GeassAlertingSpec says where Geass tells people that something went wrong. Geass turns it into an
// AlertmanagerConfig, and the cluster's Alertmanager (part of the monitoring add-on) does the sending, so alerting
// needs the monitoring add-on to be on.
type GeassAlertingSpec struct {
	// SMTP is the mail server that email channels send through. Without it email channels cannot send.
	// +optional
	SMTP *GeassAlertSMTP `json:"smtp,omitempty"`
	// Channels are the places alerts are sent to. The URL of a Slack channel is a secret: it is kept in the
	// Secret geass-alert-channels under the channel's id, never in this spec.
	// +kubebuilder:validation:MaxItems=200
	// +optional
	Channels []GeassAlertChannel `json:"channels,omitempty"`
}

// GeassAlertSMTP is the mail server alerts are sent through. The password is never in this spec: it is the
// "password" key of the Secret CredentialRef names.
type GeassAlertSMTP struct {
	// Host is the name of the mail server, such as smtp.example.com.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Host string `json:"host"`
	// Port is the port the mail server listens on. Unset means 587, which is where STARTTLS is offered.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +kubebuilder:default=587
	// +optional
	Port int32 `json:"port,omitempty"`
	// Security is how the connection is protected: starttls upgrades a plain connection (usually port 587), tls
	// is encrypted from the first byte (usually port 465), and none sends in the clear, which is for a mail
	// server inside the cluster. Unset means starttls.
	// +kubebuilder:validation:Enum=starttls;tls;none
	// +kubebuilder:default=starttls
	// +optional
	Security string `json:"security,omitempty"`
	// Username signs in to the mail server. Leave it empty for one that does not ask.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	Username string `json:"username,omitempty"`
	// CredentialRef names the Secret in the system namespace whose "password" key is the password.
	// +optional
	CredentialRef *corev1.LocalObjectReference `json:"credentialRef,omitempty"`
	// From is the address the mail comes from: alerts@example.com, or "Geass <alerts@example.com>".
	// +kubebuilder:validation:MinLength=3
	// +kubebuilder:validation:MaxLength=320
	From string `json:"from"`
}

// GeassAlertChannel is one place alerts are sent to, and which alerts it hears about.
type GeassAlertChannel struct {
	// ID identifies the channel in the dashboard and names its secret.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]{8}$`
	ID string `json:"id"`
	// Name is what the dashboard calls the channel.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=60
	Name string `json:"name"`
	// Type is slack, for a Slack incoming webhook (or any receiver that takes the same JSON), or email.
	// +kubebuilder:validation:Enum=slack;email
	Type string `json:"type"`
	// Recipients are the addresses an email channel writes to.
	// +kubebuilder:validation:MaxItems=20
	// +kubebuilder:validation:items:MaxLength=320
	// +optional
	Recipients []string `json:"recipients,omitempty"`
	// Events limits which kinds of alert the channel hears about. Empty means every kind, including ones added later.
	// +kubebuilder:validation:items:Enum=build.failed;deploy.failed;service.down;backup.failed;node.unhealthy;cluster.issue;workload.crashlooping;storage.filling
	// +optional
	Events []string `json:"events,omitempty"`
	// Projects limits the channel to these projects. Empty means every project, and it is also what lets a
	// channel hear about the cluster itself (nodes, certificates, upgrades), which belongs to no project.
	// +kubebuilder:validation:MaxItems=100
	// +optional
	Projects []string `json:"projects,omitempty"`
	// Environments limits the channel to these environments, such as production. Empty means every environment.
	// +kubebuilder:validation:MaxItems=100
	// +optional
	Environments []string `json:"environments,omitempty"`
	// NotifyResolved sends a message when a problem clears, not only when it starts. Unset means it does.
	// +kubebuilder:default=true
	// +optional
	NotifyResolved *bool `json:"notifyResolved,omitempty"`
	// Disabled pauses the channel without deleting it.
	// +optional
	Disabled bool `json:"disabled,omitempty"`
	// Org makes the channel an organization's: the GeassOrganization whose people set it up and can see it. It hears
	// only about that organization's projects (the ones in Projects, when it names some), never about the cluster, and
	// never about another organization's. Without it the channel is the platform's.
	// +kubebuilder:validation:Pattern=`^org-[a-f0-9]{8}$`
	// +optional
	Org string `json:"org,omitempty"`
}
