package v1alpha1

import corev1 "k8s.io/api/core/v1"

// GeassEgressMail says whether the pods of an organization may send mail straight to addresses outside the cluster.
// +kubebuilder:validation:Enum=Blocked;Open
type GeassEgressMail string

const (
	// EgressMailBlocked closes the ports mail is sent and submitted on (TCP 25, 465, 587, 2465, 2525 and 2587) to every
	// address outside the cluster that allow does not open them to.
	EgressMailBlocked GeassEgressMail = "Blocked"
	// EgressMailOpen leaves them open, like any other port.
	EgressMailOpen GeassEgressMail = "Open"
)

// GeassTenantEgress limits the connections the pods of an organization's projects open to addresses outside the cluster.
// Geass writes it into the network policies that already decide where those pods may go: geass-block-cloud-metadata in
// each environment of the organization's projects, and geass-build-isolation in its build namespace. Connections to the
// cluster's pods, the cluster's DNS among them, are not limited by it, and what those policies already close (the cloud's
// metadata service, the cluster's machines, a build's private addresses) stays closed whatever it says. It is only as good
// as the network plugin: one that enforces network policies, and their port ranges (endPort).
//
// Setting it at all closes mail. Each field an organization sets in its own spec.egress replaces the platform's.
type GeassTenantEgress struct {
	// Mail is Blocked, which closes the ports mail is sent on, or Open. Unset means Blocked.
	// +optional
	Mail GeassEgressMail `json:"mail,omitempty"`
	// AllowedPorts, when set, are the only destination ports open to addresses outside the cluster: every other port is
	// closed. List UDP ports too (such as 443, for HTTP/3), or no UDP leaves the cluster. Unset means every port is open
	// but the closed ones. A port that is allowed and also closed, by mail or by blockedPorts, is closed. The cluster's API
	// port stays open from project environments, whose Postgres databases use it.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// +optional
	AllowedPorts []GeassEgressPortRange `json:"allowedPorts,omitempty"`
	// BlockedPorts are closed to addresses outside the cluster, besides mail's. Closing the ports public mining pools
	// listen on (see the documentation for a list) is a heuristic: it makes mining with a pool's defaults fail, and does
	// nothing against a miner who uses another port, a proxy or port 443. It detects nothing. An organization's empty list
	// replaces the platform's with none.
	// +kubebuilder:validation:MaxItems=64
	// +optional
	BlockedPorts []GeassEgressPortRange `json:"blockedPorts,omitempty"`
	// Allow opens ports to ranges of addresses whatever else closes them, such as the mail ports to the platform's mail
	// relay. It cannot open what the policies close anyway: the cloud's metadata service, the cluster's machines for a
	// project that keeps away from them, and a build's private addresses. An organization's empty list replaces the
	// platform's with none.
	// +kubebuilder:validation:MaxItems=32
	// +optional
	Allow []GeassEgressAllowance `json:"allow,omitempty"`
	// MiningPools is Blocked, which closes a curated list of the TCP ports public mining pools listen on by default (3333 to
	// 3336, 4444, 5555, 7777, 14433, 14444 and 45700), or Open. Unset means Open. It is a heuristic: a miner that uses port
	// 443, any other port or a proxy is not stopped, and it detects nothing.
	// +optional
	MiningPools GeassEgressMiningPools `json:"miningPools,omitempty"`
}

// MailBlocked reports whether the mail ports are closed, which they are unless Mail says Open.
func (e *GeassTenantEgress) MailBlocked() bool {
	return e != nil && e.Mail != EgressMailOpen
}

// GeassEgressPortRange is one destination port, or a range of them, of one protocol.
// +kubebuilder:validation:XValidation:rule="!has(self.endPort) || self.endPort >= self.port",message="endPort must not be below port"
type GeassEgressPortRange struct {
	// Port is the port, or the first port of the range.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`
	// EndPort is the last port of the range, which it includes. Unset means Port alone.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +optional
	EndPort *int32 `json:"endPort,omitempty"`
	// Protocol is TCP or UDP. Unset means TCP.
	// +kubebuilder:validation:Enum=TCP;UDP
	// +optional
	Protocol corev1.Protocol `json:"protocol,omitempty"`
}

// GeassEgressAllowance opens ports to one range of addresses.
type GeassEgressAllowance struct {
	// CIDR is the range of IPv4 addresses, as its first address and the length of its prefix, such as "203.0.113.25/32"
	// for one address.
	// +kubebuilder:validation:Pattern=`^([0-9]{1,3}\.){3}[0-9]{1,3}/[0-9]{1,2}$`
	// +kubebuilder:validation:MaxLength=18
	CIDR string `json:"cidr"`
	// Ports are the ports opened to it.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	Ports []GeassEgressPortRange `json:"ports"`
	// Description says what the addresses are, for whoever reads the configuration.
	// +kubebuilder:validation:MaxLength=200
	// +optional
	Description string `json:"description,omitempty"`
}
