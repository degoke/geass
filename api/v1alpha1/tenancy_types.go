package v1alpha1

// GeassPlatformTenantIsolationSpec hardens a cluster that organizations who do not trust each other share. Who belongs to
// which organization is spec.auth.tenancy; this is how far apart their work is kept.
type GeassPlatformTenantIsolationSpec struct {
	// Registry keeps the platform's registry login out of organizations' builds and namespaces, and each organization's
	// images apart from the platform's and from every other organization's. Unset means builds use the registry they did
	// before: an organization without a registry of its own builds with the platform's login.
	// +optional
	Registry *GeassTenantRegistrySpec `json:"registry,omitempty"`
}

// The policies of spec.tenancy.registry.
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
