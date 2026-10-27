package v1alpha1

// GeassPlatformTenantIsolationSpec hardens a cluster that organizations who do not trust each other share. Who belongs to
// which organization is spec.auth.tenancy; this is how far apart their work is kept on the nodes.
type GeassPlatformTenantIsolationSpec struct {
	// Sandbox runs the pods Geass makes for organizations' projects under a sandboxed container runtime, such as gVisor or
	// Kata Containers, so that code one organization wrote does not share the node's kernel directly with another's. Unset
	// means every pod runs with the node's own runtime, as before.
	// +optional
	Sandbox *GeassTenantSandboxSpec `json:"sandbox,omitempty"`
}

// GeassTenantSandboxSpec names the RuntimeClass the pods of organizations' projects run with: the pods of their services,
// of their functions' schedules and of their builds. The projects of no organization and Geass's own pods keep the node's
// own runtime. The nodes need the runtime installed and the cluster a RuntimeClass of this name; while there is none, an
// organization's service or build waits, and says so, and is never started without the sandbox.
type GeassTenantSandboxSpec struct {
	// RuntimeClassName is the RuntimeClass the pods run with, such as gvisor or kata.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	RuntimeClassName string `json:"runtimeClassName"`
	// SkipOnReservedNodes runs without the sandbox the pods that can only be scheduled on nodes reserved for their own
	// organization: their node selector asks for a tag the organization requires of all its projects, no other
	// organization requires it, and every node that carries it is reserved for it, of which there is at least one.
	// Anything less certain is sandboxed.
	// +optional
	SkipOnReservedNodes bool `json:"skipOnReservedNodes,omitempty"`
}
