/*
Copyright 2026 Adegoke Adewoye.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
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
	// Turning it off never uninstalls KEDA, so services that sleep keep sleeping.
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

	// Mesh says how nodes at different sites, some of them behind a NAT, are joined into one cluster network. Left
	// out, nothing changes: multiSite on its own is the same as a mesh with the WireGuard provider, as it always was,
	// and a cluster that sets neither keeps one private network. Setting it also turns on the checks the operator
	// makes of a mesh (the MeshReady condition, and a worker is refused while the control plane is not on it), and
	// makes multiSite redundant. Like multiSite it is chosen when the cluster is installed, because it describes how
	// the control plane was built and Geass does not rebuild that.
	// +optional
	Mesh *GeassClusterMeshSpec `json:"mesh,omitempty"`

	// ControlPlaneFirewall names the cloud firewall in front of the control plane. When it is set, the address of each
	// machine a pool creates is let through it (the API port and, in a WireGuard mesh, the overlay port) before the
	// machine joins, and taken out again when the machine is removed, so the control plane need not be open to the
	// internet. Without it the control plane's TCP 6443 (and UDP 51820 in a WireGuard mesh) must already accept the
	// new machines.
	// +optional
	ControlPlaneFirewall *GeassControlPlaneFirewall `json:"controlPlaneFirewall,omitempty"`
}

// MeshProvider is what carries traffic between the sites of a mesh.
// +kubebuilder:validation:Enum=WireGuard;Tailscale
type MeshProvider string

const (
	// MeshProviderWireGuard is K3s's own WireGuard flannel backend (wireguard-native): every node advertises an address
	// the others can send UDP 51820 to, and a node behind a NAT can join when it is the one that opens the tunnel.
	// It needs nothing outside the cluster.
	MeshProviderWireGuard MeshProvider = "WireGuard"
	// MeshProviderTailscale is K3s's Tailscale integration (vpn-auth), for a Tailscale tailnet or a Headscale server.
	// Nodes advertise their tailnet address, and Tailscale finds a path between them, through a relay when it must,
	// so two nodes that are both behind a NAT can reach each other. It needs a tailnet and an auth key.
	MeshProviderTailscale MeshProvider = "Tailscale"
)

// GeassClusterMeshSpec chooses the mesh the nodes of a cluster talk over.
// +kubebuilder:validation:XValidation:rule="self.provider != 'Tailscale' || has(self.tailscale)",message="provider Tailscale needs the tailscale settings (an auth key Secret)"
// +kubebuilder:validation:XValidation:rule="self.provider == 'Tailscale' || !has(self.tailscale)",message="the tailscale settings are only for provider Tailscale"
type GeassClusterMeshSpec struct {
	// Provider is what carries traffic between the sites. WireGuard is the default and needs nothing but UDP 51820
	// between nodes; Tailscale needs a tailnet (or a Headscale server) and an auth key, and opens no port.
	// +kubebuilder:default=WireGuard
	// +optional
	Provider MeshProvider `json:"provider,omitempty"`

	// Tailscale holds what a node needs to join the tailnet. It is required with provider Tailscale and refused
	// with any other.
	// +optional
	Tailscale *GeassMeshTailscale `json:"tailscale,omitempty"`
}

// GeassMeshTailscale is how nodes join a tailnet, or a Headscale server's network.
type GeassMeshTailscale struct {
	// AuthKeySecretRef names a Secret in geass-system that holds the key nodes join the tailnet with, under the key
	// authKey. Use a reusable, pre-approved key that is tagged (or that belongs to a user who may have the nodes),
	// and that expires: it is copied to the disk of every node, where only root can read it. The key is never shown
	// in a status, an event or the operator's log.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`
	AuthKeySecretRef string `json:"authKeySecretRef"`

	// ControlServerURL is the address of a Headscale server (or another Tailscale-compatible control server), such as
	// https://headscale.example.com. Left empty, nodes join Tailscale's own service. It must be https, and has
	// no user name, query or fragment.
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^(https://[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?(:[0-9]{1,5})?(/[A-Za-z0-9._~/-]*)?)?$`
	// +optional
	ControlServerURL string `json:"controlServerURL,omitempty"`
}

// GeassControlPlaneFirewall says which firewall guards the control plane, and the connection that may change it.
type GeassControlPlaneFirewall struct {
	// ConnectionRef is the cloud connection (for Compute) at the provider where the control plane runs. It may be
	// another provider than the one that creates the machines.
	// +kubebuilder:validation:MinLength=1
	ConnectionRef string `json:"connectionRef"`

	// Target is the provider's id for the firewall or security group, such as sg-0abc123 or a Hetzner firewall id.
	// +kubebuilder:validation:MinLength=1
	Target string `json:"target"`

	// Region is where the firewall is, for a provider that answers per region.
	// +optional
	Region string `json:"region,omitempty"`
}

// GeassClusterK3sSpec says which K3s release the cluster's nodes should run.
type GeassClusterK3sSpec struct {
	// Version is the K3s release the worker nodes are brought to, such as v1.36.5+k3s1. When it is set and a
	// worker runs something else, the operator upgrades the workers one at a time: it cordons the node, replaces
	// K3s on it over SSH, waits for it to be ready on the new version and uncordons it, and stops at the first
	// that does not come back. Workers are only moved to the version the control plane already runs, because an
	// agent must not be newer than its server, and the operator never touches the control plane itself: it is
	// upgraded by running the installer on that machine with --k3s-version, which also sets this field.
	// Left empty, the operator leaves K3s on every node alone.
	// +kubebuilder:validation:MaxLength=40
	// +kubebuilder:validation:Pattern=`^(v1\.[0-9]+\.[0-9]+\+k3s[0-9]+)?$`
	// +optional
	Version string `json:"version,omitempty"`
}

// GeassClusterEtcdSnapshotsSpec says where copies of the cluster's own state are kept off the control plane's disk. K3s
// takes a snapshot of its database (etcd) twice a day and keeps five on the same disk as the database, so a disk that is
// lost takes the cluster and every snapshot with it. With this set, K3s also sends each snapshot to this bucket.
//
// A snapshot holds everything the cluster stores, every Secret included, in plain text: give it a bucket of its own, that
// nothing else can read, with encryption on, and credentials that can only use that bucket. Setting this does nothing
// until K3s on the control plane has been told to send copies (the installer's etcd-snapshots command); it only says
// where they go, and it can be changed without restarting K3s.
type GeassClusterEtcdSnapshotsSpec struct {
	// Bucket is the S3 bucket the copies are sent to.
	// +kubebuilder:validation:MinLength=3
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9][a-z0-9.-]*[a-z0-9]$`
	Bucket string `json:"bucket"`

	// Region is the bucket's region. Left empty it is us-east-1, which is what K3s assumes, and is right for any
	// S3-compatible server that has no regions.
	// +kubebuilder:validation:MaxLength=40
	// +kubebuilder:validation:Pattern=`^[a-z0-9-]*$`
	// +optional
	Region string `json:"region,omitempty"`

	// Endpoint is the host (and port, if it is not the usual one) of an S3-compatible server, such as
	// s3.eu-north-1.amazonaws.com or minio.example.com:9000, without a scheme. Left empty it is AWS S3.
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^([A-Za-z0-9.-]+(:[0-9]+)?)?$`
	// +optional
	Endpoint string `json:"endpoint,omitempty"`

	// Insecure sends the copies over plain HTTP, for a server on a network that is closed to everyone else. It is never
	// right for AWS.
	// +optional
	Insecure bool `json:"insecure,omitempty"`

	// Folder is the prefix the copies are stored under in the bucket. It is geass when left empty.
	// +kubebuilder:validation:MaxLength=200
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9._/-]*$`
	// +optional
	Folder string `json:"folder,omitempty"`

	// CredentialsSecretRef names a Secret in geass-system with the keys ACCESS_KEY_ID and ACCESS_SECRET_KEY: an access
	// key that can list the bucket and read, write and delete objects in it, and can do nothing else. It is
	// geass-etcd-snapshot-credentials when left empty.
	// +optional
	CredentialsSecretRef *corev1.LocalObjectReference `json:"credentialsSecretRef,omitempty"`
}

// GeassClusterEtcdSnapshotsStatus is what the operator finds in the bucket.
type GeassClusterEtcdSnapshotsStatus struct {
	// LastCopyTime is when the newest snapshot in the bucket was written.
	// +optional
	LastCopyTime *metav1.Time `json:"lastCopyTime,omitempty"`

	// LastCopyName is the name of that snapshot, which is what k3s server --cluster-reset-restore-path is given.
	// +optional
	LastCopyName string `json:"lastCopyName,omitempty"`

	// Copies is how many snapshots the bucket holds under the folder.
	// +optional
	Copies int32 `json:"copies,omitempty"`

	// CheckedAt is when the bucket was last looked at.
	// +optional
	CheckedAt *metav1.Time `json:"checkedAt,omitempty"`
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

	// K3s says which K3s release the nodes should run.
	// +optional
	K3s GeassClusterK3sSpec `json:"k3s,omitempty"`

	// EtcdSnapshots says where copies of the cluster's own state are kept off the control plane's disk. Left out, there
	// are none: the snapshots K3s takes stay on the disk they protect. It is opt-in.
	// +optional
	EtcdSnapshots *GeassClusterEtcdSnapshotsSpec `json:"etcdSnapshots,omitempty"`

	// LostNodeAfter is how long a node may be not ready before it is taken to be lost, and the databases on it are made to start
	// on another node or, for a highly available Postgres database whose volume was on it alone, replaced (which removes that
	// instance's volume). It is 15 minutes when left out. 0s turns it off, which is what to set before a planned shutdown of
	// longer than that.
	// +optional
	LostNodeAfter *metav1.Duration `json:"lostNodeAfter,omitempty"`

	// Dashboard says how the dashboard is reached before it has a domain.
	// +optional
	Dashboard GeassClusterDashboardSpec `json:"dashboard,omitempty"`
}

// GeassClusterDashboardSpec says how the dashboard is reached before it has a domain.
type GeassClusterDashboardSpec struct {
	// PublicIP serves the dashboard over HTTPS at the control plane's public IPv4 address, with a Let's Encrypt
	// certificate for that address, until a dashboard domain is verified. The installer turns it on for a new cluster.
	// +optional
	PublicIP *bool `json:"publicIP,omitempty"`
}

type ClusterPhase string

const (
	ClusterPhaseReady ClusterPhase = "Ready"
)

// K3sUpgradePhase is how far an upgrade of the workers' K3s has got.
type K3sUpgradePhase string

const (
	// K3sUpgradeWaitingForControlPlane: workers are behind spec.k3s.version, but the control plane is not on
	// it yet, and a worker must not run a newer K3s than its server.
	K3sUpgradeWaitingForControlPlane K3sUpgradePhase = "WaitingForControlPlane"
	// K3sUpgradeWaiting: the next worker is not started because the cluster is not in a state that it is safe
	// to take a node out of: a node is not ready or a replicated volume is not healthy. Message says which.
	K3sUpgradeWaiting K3sUpgradePhase = "Waiting"
	// K3sUpgradeUpgrading: a worker is being upgraded, CurrentNode.
	K3sUpgradeUpgrading K3sUpgradePhase = "Upgrading"
	// K3sUpgradeComplete: every worker runs the target version.
	K3sUpgradeComplete K3sUpgradePhase = "Complete"
	// K3sUpgradeFailed: a worker did not come back on the target version. The operator stops there, leaving that
	// node cordoned, and goes no further until the target changes or the node is fixed.
	K3sUpgradeFailed K3sUpgradePhase = "Failed"
)

// K3sUpgradeNodeStep is where the worker that is being upgraded has got to, so that an operator that restarts
// part-way carries on from there.
type K3sUpgradeNodeStep string

const (
	K3sUpgradeStepCordoned  K3sUpgradeNodeStep = "Cordoned"
	K3sUpgradeStepInstalled K3sUpgradeNodeStep = "Installed"
)

// GeassK3sUpgradeStatus is the progress of bringing the workers to spec.k3s.version.
type GeassK3sUpgradeStatus struct {
	// Phase says how the upgrade is going.
	// +optional
	Phase K3sUpgradePhase `json:"phase,omitempty"`

	// TargetVersion is the version the workers are being brought to.
	// +optional
	TargetVersion string `json:"targetVersion,omitempty"`

	// CurrentNode is the worker being upgraded, while one is.
	// +optional
	CurrentNode string `json:"currentNode,omitempty"`

	// Step is how far the current node has got.
	// +optional
	Step K3sUpgradeNodeStep `json:"step,omitempty"`

	// StartedAt is when the current node's upgrade began, which is what its time limit counts from.
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`

	// Message says what the upgrade is doing, or why it is not.
	// +optional
	Message string `json:"message,omitempty"`
}

// GeassClusterK3sStatus is what the cluster runs, and the state of an upgrade of it.
type GeassClusterK3sStatus struct {
	// ControlPlaneVersion is the oldest K3s version that a control plane node reports.
	// +optional
	ControlPlaneVersion string `json:"controlPlaneVersion,omitempty"`

	// Upgrade is the progress of an upgrade of the workers to spec.k3s.version.
	// +optional
	Upgrade *GeassK3sUpgradeStatus `json:"upgrade,omitempty"`
}

// AddonState is where an add-on is in being installed.
// +kubebuilder:validation:Enum=Off;Installing;Ready;Failed
type AddonState string

const (
	// AddonStateOff: the add-on is not asked for. Installed says whether something of it is still in the cluster.
	AddonStateOff AddonState = "Off"
	// AddonStateInstalling: the add-on is asked for and is not up yet. Message says what it is waiting for.
	AddonStateInstalling AddonState = "Installing"
	// AddonStateReady: the add-on is installed and its pods are running.
	AddonStateReady AddonState = "Ready"
	// AddonStateFailed: the add-on is asked for and cannot come up without help. Reason and Message say why.
	AddonStateFailed AddonState = "Failed"
)

// GeassClusterAddonStatus is how one add-on of spec.addons is doing. The operator writes it, so that a person can see why an
// add-on is not up without looking at the Helm jobs that install it.
type GeassClusterAddonStatus struct {
	// Name is the add-on, as it is named under spec.addons: monitoring, logging, serverless or longhorn.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// State is Off, Installing, Ready or Failed.
	State AddonState `json:"state"`

	// Reason is a short word for why the add-on is Failed, such as InstallFailed or InstallSuspended.
	// +optional
	Reason string `json:"reason,omitempty"`

	// Message says in a sentence what the add-on is waiting for, or what went wrong.
	// +optional
	Message string `json:"message,omitempty"`

	// Installed says that something of the add-on is in the cluster. It is true for an add-on that is Off when turning it
	// off does not remove it (scale to zero and replicated storage never are).
	// +optional
	Installed bool `json:"installed,omitempty"`
}

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

	// Release is the Geass release of the operator that last reconciled this cluster, such as v0.2.0. The
	// installer reads it before an update, to refuse a downgrade and to wait until the new operator reports
	// itself. A cluster installed before releases were recorded has none until its operator is updated.
	// +optional
	Release string `json:"release,omitempty"`

	// SchemaVersion is the level of data migrations that have been applied to what this cluster stores.
	// An operator that knows of fewer migrations than this refuses to start, so that an older release can
	// never reconcile data that a newer one has changed.
	// +optional
	SchemaVersion int32 `json:"schemaVersion,omitempty"`

	// K3s is what the cluster's nodes run and how an upgrade of the workers is going.
	// +optional
	K3s GeassClusterK3sStatus `json:"k3s,omitempty"`

	// EtcdSnapshots is what the operator found in the bucket that spec.etcdSnapshots names.
	// +optional
	EtcdSnapshots *GeassClusterEtcdSnapshotsStatus `json:"etcdSnapshots,omitempty"`

	// Addons is how each of the add-ons that can be switched on and off is doing: monitoring, logging, serverless and
	// longhorn.
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=16
	// +optional
	Addons []GeassClusterAddonStatus `json:"addons,omitempty"`

	// DashboardPublicURL is the HTTPS address of the dashboard at the public IP address, once its certificate is issued.
	// +optional
	DashboardPublicURL string `json:"dashboardPublicURL,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Release",type=string,JSONPath=`.status.release`
// +kubebuilder:printcolumn:name="K3s",type=string,JSONPath=`.status.k3s.controlPlaneVersion`

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
