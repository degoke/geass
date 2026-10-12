package v1alpha1

// GeassNetworkIsolation says how far a project's services are open to the rest of the cluster.
// +kubebuilder:validation:Enum=Project;None
type GeassNetworkIsolation string

const (
	// NetworkIsolationProject lets a project's services be reached only by the project itself (every
	// one of its environments), by the platform, and from the public internet through its domains.
	// Other projects cannot reach them.
	NetworkIsolationProject GeassNetworkIsolation = "Project"
	// NetworkIsolationNone leaves the project's services open to every service in the cluster.
	NetworkIsolationNone GeassNetworkIsolation = "None"
)

// GeassProjectNetwork controls which traffic may reach a project's services from inside the cluster.
// The project's services can always reach other services and the internet; what is limited is who can
// reach them.
type GeassProjectNetwork struct {
	// Isolation defaults to Project.
	// +kubebuilder:default=Project
	// +optional
	Isolation GeassNetworkIsolation `json:"isolation,omitempty"`
	// AllowFromProjects names other projects whose services may reach this project's services, such as
	// a front-end project calling an internal API. It has no effect when isolation is None.
	// +kubebuilder:validation:MaxItems=20
	// +kubebuilder:validation:items:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:items:MaxLength=63
	// +listType=set
	// +optional
	AllowFromProjects []string `json:"allowFromProjects,omitempty"`
}

// ProjectNetworkIsolation is the isolation a project runs with. A project that never chose one is
// isolated: the safe setting is the default, and opening a project up is a deliberate step.
func ProjectNetworkIsolation(project *GeassProject) GeassNetworkIsolation {
	if project == nil || project.Spec.Network == nil || project.Spec.Network.Isolation == "" {
		return NetworkIsolationProject
	}
	return project.Spec.Network.Isolation
}
