package v1alpha1

// GeassPlatformManagedSpec says that a platform runs this Geass for the people who use it, and keeps for itself the
// settings that a person who runs their own Geass would change: the installer and how Geass is updated, the K3s release,
// how people sign in, and the dashboard's domain and DNS.
//
// With it on, the dashboard's API refuses a write to any of those (403, code "managed", with a message that says the platform
// manages it), and the dashboard shows them as they are, read-only, with an explanation. Reads still work, and everything else
// (projects, services, databases, organizations, pools, servers, connectors for GitHub and clouds) is as it was. Unset or off,
// nothing changes.
//
// It is a guard rail for the dashboard and its API, not a boundary: whoever can write this object, or the cluster's other
// objects, can still change what it covers. The platform sets it, and no route of the dashboard's API writes it.
type GeassPlatformManagedSpec struct {
	// Enabled turns managed mode on. Unset or false means off.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// Provider names the platform that manages the cluster, such as rungeass. It is put in the messages that say who
	// manages a setting: "managed by rungeass". Left empty the messages say "the platform that runs this cluster".
	// +kubebuilder:validation:MaxLength=60
	// +kubebuilder:validation:Pattern=`^([A-Za-z0-9][A-Za-z0-9 ._-]*)?$`
	// +optional
	Provider string `json:"provider,omitempty"`
}
