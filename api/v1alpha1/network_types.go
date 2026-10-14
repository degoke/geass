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
	// BlockNodeAddresses stops the project's services from opening connections to the machines the cluster
	// runs on: every node's own addresses, including the one it has on the pod network. What is left open on
	// them is the Kubernetes API port of the control plane, which some workloads (Postgres instances) need.
	// A service that reaches another through a node's address, such as its own public address when DNS points
	// straight at a node, stops working, so a project that does not say is not blocked: this field has no
	// default, because a default would reach the projects that existed before it did. Projects created by the
	// dashboard or the API say true. Geass keeps the rule in step with the cluster's nodes
	// as they join and leave.
	// +optional
	BlockNodeAddresses bool `json:"blockNodeAddresses,omitempty"`
}

// NewProjectNetwork is the network a project is created with by the dashboard and the API:
// isolated from other projects, and kept away from the cluster's machines. A project made any other way, with a
// manifest, says what it wants, and a project that existed before the block did stays as it was.
func NewProjectNetwork() *GeassProjectNetwork {
	return &GeassProjectNetwork{Isolation: NetworkIsolationProject, BlockNodeAddresses: true}
}

// ProjectBlocksNodeAddresses reports whether a project's services are cut off from the cluster's machines.
func ProjectBlocksNodeAddresses(project *GeassProject) bool {
	return project != nil && project.Spec.Network != nil && project.Spec.Network.BlockNodeAddresses
}

// ProjectNetworkIsolation is the isolation a project runs with. A project that never chose one is
// isolated: the safe setting is the default, and opening a project up is a deliberate step.
func ProjectNetworkIsolation(project *GeassProject) GeassNetworkIsolation {
	if project == nil || project.Spec.Network == nil || project.Spec.Network.Isolation == "" {
		return NetworkIsolationProject
	}
	return project.Spec.Network.Isolation
}
