package dashboard

import (
	"context"
	"net/http"
	"strings"

	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// Managed mode (GeassPlatformConfig spec.managed) is for a Geass that a platform runs for the people who use it. The platform
// keeps five things for itself, which a person who runs their own Geass would change from the dashboard or with the installer:
//
//   - the installer: the command people are told to run on the control plane, and what only it can finish (the backup of the
//     cluster's own state, which K3s is told to send once, by the installer)
//   - updates: how Geass itself is brought to a new release
//   - the K3s release the nodes run, and the update of the workers to it
//   - how people sign in: the identity provider and its roles, how the token names organizations, an organization's own
//     provider
//   - the dashboard's domain and the DNS records Geass keeps for it
//
// With it on, the dashboard's API refuses a write to any of them and says who manages it, and the dashboard is told so in its
// bootstrap (platform.managed) to show them as they are, read-only, with that explanation. Reads are untouched, and so is
// everything that is not in the list. Off, which is what it is unless the platform's configuration says otherwise, none of
// this runs: the only cost is that a write to one of these routes first reads the platform configuration.
//
// The dashboard has no route that updates Geass or its K3s release: those are the installer's (on the machine) and the
// cluster's own objects'. So of the five, the API has routes to refuse for sign-in, the dashboard's domain and the backup of the
// cluster's state only; the other two are in the list so that the dashboard can say they are managed, and so that a route added
// for them later has one place to be listed.
const (
	managedInstaller       = "installer"
	managedUpgrades        = "upgrades"
	managedK3sVersion      = "k3sVersion"
	managedSignIn          = "signIn"
	managedDashboardDomain = "dashboardDomain"

	// managedCode is the code of the error a refused write answers with, for a program to switch on.
	managedCode = "managed"
	// managedByDefault is how a refusal names the platform when the configuration does not.
	managedByDefault = "the platform that runs this cluster"
)

// managedAreas is what managed mode keeps for the platform, in the order the dashboard is told.
var managedAreas = []string{managedInstaller, managedUpgrades, managedK3sVersion, managedSignIn, managedDashboardDomain}

// managedSubjects says what each area is, as the start of the sentence that refuses a write to it: "<subject> managed by
// <platform> and cannot be changed here."
var managedSubjects = map[string]string{
	managedInstaller:       "The backup of the cluster's state, which the installer sets up, is",
	managedUpgrades:        "Updating Geass is",
	managedK3sVersion:      "The K3s version is",
	managedSignIn:          "Sign-in settings are",
	managedDashboardDomain: "The dashboard's domain and DNS are",
}

// managedState is what the platform configuration says about managed mode.
type managedState struct {
	Enabled  bool
	Provider string
}

func managedStateOf(config geassv1alpha1.GeassPlatformConfig) managedState {
	spec := config.Spec.Managed
	if spec == nil || !spec.Enabled {
		return managedState{}
	}
	return managedState{Enabled: true, Provider: strings.TrimSpace(spec.Provider)}
}

// by is who the messages say manages the cluster.
func (m managedState) by() string {
	if m.Provider != "" {
		return m.Provider
	}
	return managedByDefault
}

// managedState reads the platform configuration, which the dashboard's own cache serves. It is the platform's, whoever asks.
func (s *Server) managedState(ctx context.Context) (managedState, error) {
	config, err := s.platformConfig(ctx)
	if err != nil {
		return managedState{}, err
	}
	return managedStateOf(config), nil
}

// dashboardManaged is what the dashboard is told: that the platform manages the cluster, who it is, and which of the five
// things it keeps. It is in the bootstrap only while managed mode is on, so a cluster that is not managed answers exactly as
// before.
type dashboardManaged struct {
	Enabled  bool     `json:"enabled"`
	Provider string   `json:"provider,omitempty"`
	Locked   []string `json:"locked"`
}

// dashboardManagedOf is nil when managed mode is off. A dashboard that is not told shows its controls, and the server still
// refuses what they do.
func dashboardManagedOf(state managedState) *dashboardManaged {
	if !state.Enabled {
		return nil
	}
	return &dashboardManaged{Enabled: true, Provider: state.Provider, Locked: append([]string(nil), managedAreas...)}
}

// managedAreaOf says which of the five a write belongs to. It is given the dashboard's own path, which is what the v1 API's
// writes are turned into before they get here (POST /api/v1/settings/auth/save is /settings/auth/save), so one list serves both.
func managedAreaOf(path string) (string, bool) {
	switch {
	case underPath(path, "/settings/auth"), underPath(path, "/users"), path == "/setup":
		// The people who sign in with a password are part of sign-in.
		return managedSignIn, true
	case underPath(path, "/settings/domain"), underPath(path, "/settings/cloudflare"):
		// Cloudflare is how Geass writes the dashboard's DNS records, and the tunnel it is published through.
		return managedDashboardDomain, true
	case underPath(path, "/settings/etcd-snapshots"):
		return managedInstaller, true
	case isOrganizationIdPPath(path):
		return managedSignIn, true
	}
	return "", false
}

func underPath(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+"/")
}

// isOrganizationIdPPath is /orgs/<organization>/idp/...: an organization's own identity provider. The rest of /orgs (making,
// renaming and removing organizations and their workspaces) is what a platform does through the API, and is not kept back.
func isOrganizationIdPPath(path string) bool {
	rest, ok := strings.CutPrefix(path, "/orgs/")
	if !ok {
		return false
	}
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	return len(parts) >= 2 && parts[1] == "idp"
}

// refuseWhenManaged answers a write that the platform keeps for itself, when managed mode is on, and says whether it did. It
// is given the request after authentication and the v1 translation, so who may call a route is decided before it, and the
// dashboard's routes and the v1 API's are refused alike. A write that is not one of the five is not looked at, and not read
// for. When it cannot tell whether managed mode is on, it refuses: that is the safe way to be wrong.
func (s *Server) refuseWhenManaged(w http.ResponseWriter, r *http.Request) bool {
	area, ok := managedAreaOf(r.URL.Path)
	if !ok {
		return false
	}
	state, err := s.managedState(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not check whether the platform manages this setting"})
		return true
	}
	if !state.Enabled {
		return false
	}
	ctrllog.FromContext(r.Context()).Info("Refused a change that the platform manages", "area", area, "path", r.URL.Path)
	body := map[string]string{
		dashboardLiteralError: managedSubjects[area] + " managed by " + state.by() + " and cannot be changed here.",
		"code":                managedCode,
		"area":                area,
	}
	if state.Provider != "" {
		body["managedBy"] = state.Provider
	}
	writeJSON(w, http.StatusForbidden, body)
	return true
}
