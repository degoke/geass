package v1alpha1

// GeassPlatformTenantIsolationSpec hardens a cluster that organizations who do not trust each other share. Who belongs to
// which organization is spec.auth.tenancy; this is how far apart their work is kept.
type GeassPlatformTenantIsolationSpec struct {
	// Sandbox runs the pods Geass makes for organizations' projects under a sandboxed container runtime, such as gVisor or
	// Kata Containers, so that code one organization wrote does not share the node's kernel directly with another's. Unset
	// means every pod runs with the node's own runtime, as before.
	// +optional
	Sandbox *GeassTenantSandboxSpec `json:"sandbox,omitempty"`
	// Egress limits what the pods of organizations' projects may reach outside the cluster: the ports mail is sent on
	// are closed, and optionally further ports (such as those mining pools listen on), or all but a list of them. An
	// organization's own spec.egress replaces each field of it that it sets. Unset means the pods reach the internet on
	// every port, as before.
	// +optional
	Egress *GeassTenantEgress `json:"egress,omitempty"`
	// Registry keeps the platform's registry login out of organizations' builds and namespaces, and each organization's
	// images apart from the platform's and from every other organization's. Unset means builds use the registry they did
	// before: an organization without a registry of its own builds with the platform's login.
	// +optional
	Registry *GeassTenantRegistrySpec `json:"registry,omitempty"`
	// Abuse looks for the services of organizations' projects that may be abusing the platform's machines, such as by
	// mining: a service whose CPU use stays near what it is allowed, and reports from a detector outside Geass. It says so
	// and, if asked, scales a suspect to zero. What it finds is a signal, not proof. Unset means Geass looks for nothing.
	// +optional
	Abuse *GeassTenantAbuse `json:"abuse,omitempty"`
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

const (
	// RegistryPolicyRequireOwnRegistry builds an organization's images only into a registry of the organization's own.
	RegistryPolicyRequireOwnRegistry = "requireOwnRegistry"
)

// GeassTenantRegistrySpec is how the projects of organizations get a registry. It applies to the projects that belong to an
// organization (spec.org set); the projects of no organization build and pull as they did.
type GeassTenantRegistrySpec struct {
	// Policy is what an organization's builds may push with. requireOwnRegistry: an organization's build is never given the
	// platform's registry login, its namespaces never hold a copy of it, and its builds push to the organization's own
	// registry (spec.registry on its GeassOrganization) under <repository>/<project>/<service>; a build of an organization
	// that has none does not start. An organization's services may not run an image that is in the platform's repository
	// or in another organization's registry. A registry that authorizes each organization for a path of its own (Harbor,
	// zot) is how one registry is shared: each organization is given a login for its own path as its registry.
	// +kubebuilder:validation:Enum=requireOwnRegistry
	// +kubebuilder:default=requireOwnRegistry
	// +optional
	Policy string `json:"policy,omitempty"`
}
