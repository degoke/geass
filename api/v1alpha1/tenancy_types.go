package v1alpha1

// GeassPlatformTenantIsolationSpec hardens a cluster that organizations who do not trust each other share. Who belongs to
// which organization is spec.auth.tenancy; this is how far apart their work is kept.
type GeassPlatformTenantIsolationSpec struct {
	// Egress limits what the pods of organizations' projects may reach outside the cluster: the ports mail is sent on
	// are closed, and optionally further ports (such as those mining pools listen on), or all but a list of them. An
	// organization's own spec.egress replaces each field of it that it sets. Unset means the pods reach the internet on
	// every port, as before.
	// +optional
	Egress *GeassTenantEgress `json:"egress,omitempty"`
}
