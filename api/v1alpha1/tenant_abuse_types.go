package v1alpha1

// GeassAbuseAction is what Geass does to a service of an organization that it suspects of abuse.
// +kubebuilder:validation:Enum=None;Suspend
type GeassAbuseAction string

const (
	// AbuseActionNone says so, on the service, in an event and to the platform's administrators, and changes nothing.
	AbuseActionNone GeassAbuseAction = "None"
	// AbuseActionSuspend also scales the service to zero copies until a platform administrator releases it. Nothing is
	// deleted: the service, its settings, its volumes and its databases stay as they are.
	AbuseActionSuspend GeassAbuseAction = "Suspend"
)

// GeassTenantAbuse looks for the services of organizations' projects that may be abusing the platform's machines, such as by
// mining a cryptocurrency, and says so. What it finds is a signal, never proof: legitimate work (a batch job, a load test, a
// build of a large project) can look the same, which is why a platform administrator reviews each suspect, and why the
// default action changes nothing.
//
// It has two inputs. The sustained-CPU signal: a service whose copies use nearly all the CPU they are allowed, without a
// break, for a window of time. And reports from a detector outside Geass (such as Falco), which a platform administrator's
// token sends to the API. Unset means Geass looks for nothing, and every service is as before.
type GeassTenantAbuse struct {
	// CPU is the sustained-CPU signal. Unset means its defaults: 90 percent of what the copies are allowed, for 30 minutes.
	// +optional
	CPU *GeassAbuseCPUSignal `json:"cpu,omitempty"`
	// Action is what is done to a suspect: None, the default, or Suspend. An organization's own spec.abuse.action replaces it.
	// +optional
	Action GeassAbuseAction `json:"action,omitempty"`
	// ReviewExemptionDays is how long a service that a platform administrator has cleared is left out of the CPU signal.
	// Unset means 30.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=365
	// +optional
	ReviewExemptionDays int32 `json:"reviewExemptionDays,omitempty"`
}

// GeassAbuseCPUSignal is when a service's CPU use counts as sustained. The use is measured once a minute from the cluster's
// metrics server (metrics.k8s.io, which K3s runs), as the CPU all the service's copies use together, over the CPU they are
// allowed together: each container's limit, or its request when it has no limit. A service counts once every sample for the
// whole window was at or above the threshold. A copy that starts, restarts or is replaced, a change in the number of copies,
// a rollout, and a service with no copies (stopped, or asleep under scale to zero) end the count, which starts again from
// zero once the copies have settled.
type GeassAbuseCPUSignal struct {
	// ThresholdPercent is how much of what its copies are allowed a service has to use. Unset means 90.
	// +kubebuilder:validation:Minimum=50
	// +kubebuilder:validation:Maximum=100
	// +optional
	ThresholdPercent int32 `json:"thresholdPercent,omitempty"`
	// WindowMinutes is how long the use has to last without a break. Unset means 30.
	// +kubebuilder:validation:Minimum=10
	// +kubebuilder:validation:Maximum=1440
	// +optional
	WindowMinutes int32 `json:"windowMinutes,omitempty"`
	// QuietTraffic, when set, counts the CPU use only while the service is sent few requests: a busy web service is
	// expected to use its CPU, and a miner is sent nothing. The requests are those the ingress (Traefik) counts, which
	// Prometheus has only while the monitoring add-on is on. Without them a busy service cannot be told from a quiet one,
	// so the stricter withoutTrafficData applies in place of the threshold and window.
	// +optional
	QuietTraffic *GeassAbuseQuietTraffic `json:"quietTraffic,omitempty"`
}

// GeassAbuseQuietTraffic is how few requests a service has to be sent for its CPU use to count.
type GeassAbuseQuietTraffic struct {
	// MaxRequestsPerMinute is the most requests a minute, averaged over the last five minutes, that the ingress may pass to
	// the service while its CPU use counts. Zero means none at all. Requests that reach the service from inside the
	// cluster, not through the ingress, are not seen.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1000000
	MaxRequestsPerMinute int32 `json:"maxRequestsPerMinute"`
	// WithoutTrafficData is the threshold and window that apply when the requests cannot be read. Unset means 95 percent
	// for twice the window.
	// +optional
	WithoutTrafficData *GeassAbuseCPUFallback `json:"withoutTrafficData,omitempty"`
}

// GeassAbuseCPUFallback is a stricter threshold and window, for when Geass cannot see what would explain high CPU use.
type GeassAbuseCPUFallback struct {
	// ThresholdPercent replaces the signal's. Unset means the larger of 95 and the signal's own.
	// +kubebuilder:validation:Minimum=50
	// +kubebuilder:validation:Maximum=100
	// +optional
	ThresholdPercent int32 `json:"thresholdPercent,omitempty"`
	// WindowMinutes replaces the signal's. Unset means twice the signal's own, at most 1440.
	// +kubebuilder:validation:Minimum=10
	// +kubebuilder:validation:Maximum=1440
	// +optional
	WindowMinutes int32 `json:"windowMinutes,omitempty"`
}

// GeassOrganizationAbuse is how the platform's abuse detection (spec.tenancy.abuse on the GeassPlatformConfig) treats one
// organization. Legitimate work can keep a CPU busy for hours (batch jobs, rendering, training a model, a load test), and
// this is where the platform says which organizations or projects do that. Like the rest of the organization's spec, it is
// the platform's to write: no route of the dashboard changes it.
type GeassOrganizationAbuse struct {
	// Exempt leaves every service of the organization out of the sustained-CPU signal. A report from an outside detector
	// still applies.
	// +optional
	Exempt bool `json:"exempt,omitempty"`
	// ExemptProjects leaves the services of these projects of the organization out of the sustained-CPU signal. A report
	// from an outside detector still applies.
	// +kubebuilder:validation:MaxItems=100
	// +kubebuilder:validation:items:MaxLength=63
	// +listType=set
	// +optional
	ExemptProjects []string `json:"exemptProjects,omitempty"`
	// Action replaces the platform's action for the organization's services.
	// +optional
	Action GeassAbuseAction `json:"action,omitempty"`
}

// GeassEgressMiningPools says whether the pods of an organization may connect to the ports public mining pools listen on by
// default.
// +kubebuilder:validation:Enum=Blocked;Open
type GeassEgressMiningPools string

const (
	// EgressMiningPoolsBlocked closes a curated list of the TCP ports that public mining pools, and the example
	// configurations of mining programs, use (3333 to 3336, 4444, 5555, 7777, 14433, 14444 and 45700) to every address
	// outside the cluster that allow does not open them to. It is a heuristic: a miner that uses port 443, any other port
	// or a proxy is not stopped, and nothing is detected.
	EgressMiningPoolsBlocked GeassEgressMiningPools = "Blocked"
	// EgressMiningPoolsOpen leaves them open, like any other port.
	EgressMiningPoolsOpen GeassEgressMiningPools = "Open"
)

// MiningPoolsBlocked reports whether the mining pools' ports are closed, which they are only when MiningPools says Blocked.
func (e *GeassTenantEgress) MiningPoolsBlocked() bool {
	return e != nil && e.MiningPools == EgressMiningPoolsBlocked
}
