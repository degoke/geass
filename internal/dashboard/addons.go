package dashboard

import (
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const (
	addonsSettingsPath = "/settings/addons"
	addonsWhat         = "Add-ons"

	formAddonEnabled   = "enabled"
	formAddonProfile   = "profile"
	formAddonRetention = "retentionDays"

	minLogRetentionDays = 1
	maxLogRetentionDays = 90
)

// monitoringProfiles are the footprints of the metrics add-on (spec.addons.monitoring.profile).
var monitoringProfiles = []string{"lite", "full"}

// addonView is one add-on as the dashboard, the command line and the API show it: whether it is asked for, how it is doing, and
// what can be done to it. The state is what the operator wrote in status.addons.
type addonView struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	// Enabled says the cluster asks for it.
	Enabled bool `json:"enabled"`
	// State is Off, Installing, Ready or Failed. Reason and Message say why when it is not Ready.
	State   string `json:"state"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
	// Installed says something of it is in the cluster, which can be so while it is Off.
	Installed bool `json:"installed"`
	// CanRetry says its install is stuck in a way that deleting the install job puts right.
	CanRetry bool `json:"canRetry"`
	// CanDisable is false for an add-on that Geass never removes and that holds data: once it is on, it stays on.
	CanDisable bool `json:"canDisable"`
	// KeptWhenOff says that turning it off leaves what is installed in place.
	KeptWhenOff bool `json:"keptWhenOff"`

	// Profile and Profiles are for the metrics add-on, and RetentionDays for the logs.
	Profile       string   `json:"profile,omitempty"`
	Profiles      []string `json:"profiles,omitempty"`
	RetentionDays int32    `json:"retentionDays,omitempty"`
}

type addonSettings struct {
	Addons []addonView `json:"addons"`
	// RetryPending names the add-on whose retry the operator has not carried out yet.
	RetryPending string `json:"retryPending,omitempty"`
}

// addonSettingsOf is what the cluster says about its add-ons.
func addonSettingsOf(cluster *geassv1alpha1.GeassCluster) addonSettings {
	out := addonSettings{Addons: make([]addonView, 0, len(platform.Addons)), RetryPending: cluster.Annotations[platform.AnnotationRetryAddon]}
	spec := cluster.Spec.Addons
	for _, addon := range platform.Addons {
		view := addonView{Name: addon.Name, Title: addon.Title, Enabled: addon.Enabled(spec), KeptWhenOff: addon.KeptWhenOff}
		view.State = string(geassv1alpha1.AddonStateOff)
		if view.Enabled {
			// The operator has not reported yet: it was turned on a moment ago, or the operator is older than this field.
			view.State, view.Message = string(geassv1alpha1.AddonStateInstalling), "Waiting for the operator to report."
		}
		for _, status := range cluster.Status.Addons {
			if status.Name != addon.Name {
				continue
			}
			view.State, view.Reason, view.Message, view.Installed = string(status.State), status.Reason, status.Message, status.Installed
			view.CanRetry = platform.AddonRetryable(status)
			// What the operator wrote describes the cluster as it was when it last looked, which is not after the change that was
			// just made. The request says what the add-on is to be, so until the operator has looked again, that is what shows.
			switch {
			case view.Enabled && status.State == geassv1alpha1.AddonStateOff:
				view.State, view.Reason, view.Message = string(geassv1alpha1.AddonStateInstalling), "", "Waiting for the operator to report."
			case !view.Enabled && status.State != geassv1alpha1.AddonStateOff:
				view.State, view.Reason, view.Message, view.CanRetry = string(geassv1alpha1.AddonStateOff), "", "Turning it off.", false
			}
		}
		view.CanDisable = addon.Name != platform.AddonLonghorn || !view.Enabled
		switch addon.Name {
		case platform.AddonMonitoring:
			view.Profile = spec.Monitoring.Profile
			if view.Profile == "" {
				view.Profile = monitoringProfiles[0]
			}
			view.Profiles = monitoringProfiles
		case platform.AddonLogging:
			view.RetentionDays = geassv1alpha1.LogRetentionDays(spec)
		}
		out.Addons = append(out.Addons, view)
	}
	return out
}

// requireAddonAdmin refuses anyone who may not change the cluster. The add-ons name what the cluster runs and how it is
// doing, which is for the people who run it.
func (s *Server) requireAddonAdmin(w http.ResponseWriter, r *http.Request) bool {
	if !s.sessionCanMutate(r) {
		writeJSON(w, http.StatusForbidden, map[string]string{dashboardLiteralError: addonsWhat + " " + sessionAdminOnly})
		return false
	}
	return true
}

// handleAPIAddonSettings says which add-ons are on and how each is doing.
func (s *Server) handleAPIAddonSettings(w http.ResponseWriter, r *http.Request) {
	if !s.requireAddonAdmin(w, r) {
		return
	}
	cluster, err := s.defaultGeassCluster(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not load the cluster"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, addonSettingsOf(cluster))
}

// handleAddonRoutes serves /settings/addons/<add-on>/save and /settings/addons/<add-on>/retry.
func (s *Server) handleAddonRoutes(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, addonsSettingsPath), "/"), "/")
	if len(parts) != 2 {
		addonRouteUnknown(w)
		return
	}
	if !requireMutation(w, r, addonsSettingsPath) || !parseFormOrRedirect(w, r, addonsSettingsPath) || !s.requireAddonAdmin(w, r) {
		return
	}
	addon, ok := platform.FindAddon(parts[0])
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: fmt.Sprintf("%q is not an add-on: use one of %s", parts[0], addonNames())})
		return
	}
	switch parts[1] {
	case verbSave:
		s.handleAddonSave(w, r, addon)
	case "retry":
		s.handleAddonRetry(w, r, addon)
	default:
		addonRouteUnknown(w)
	}
}

// addonRouteUnknown answers a path under /settings/addons that is not one of the two routes.
func addonRouteUnknown(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: "unknown add-on route: use save or retry"})
}

func addonNames() string {
	names := make([]string, 0, len(platform.Addons))
	for _, addon := range platform.Addons {
		names = append(names, addon.Name)
	}
	return strings.Join(names, ", ")
}

// addonChange is what a request asks of an add-on. A field that is nil is left as it is.
type addonChange struct {
	enabled       *bool
	profile       *string
	retentionDays *int32
}

// parseAddonChange reads and checks what a request asks of an add-on. It says nothing of whether the change is allowed in the
// state the cluster is in: that is for the caller to decide, with the cluster in hand.
func parseAddonChange(addon platform.Addon, form map[string][]string) (addonChange, error) {
	var change addonChange
	value := func(key string) (string, bool) {
		values, ok := form[key]
		if !ok || len(values) == 0 {
			return "", false
		}
		return strings.TrimSpace(values[0]), true
	}
	if text, ok := value(formAddonEnabled); ok {
		var enabled bool
		switch strings.ToLower(text) {
		case "on", boolTrue, "1", "yes":
			enabled = true
		case "off", "false", "0", "no":
		default:
			return change, fmt.Errorf("enabled is true or false, not %q", text)
		}
		change.enabled = &enabled
	}
	if text, ok := value(formAddonProfile); ok {
		if addon.Name != platform.AddonMonitoring {
			return change, fmt.Errorf("only the metrics add-on (%s) has a profile", platform.AddonMonitoring)
		}
		if !slices.Contains(monitoringProfiles, text) {
			return change, fmt.Errorf("the profile is %s, not %q", strings.Join(monitoringProfiles, " or "), text)
		}
		change.profile = &text
	}
	if text, ok := value(formAddonRetention); ok {
		if addon.Name != platform.AddonLogging {
			return change, fmt.Errorf("only the logs add-on (%s) has a retention", platform.AddonLogging)
		}
		days, err := strconv.Atoi(text)
		if err != nil || days < minLogRetentionDays || days > maxLogRetentionDays {
			return change, fmt.Errorf("retentionDays is a number of days from %d to %d", minLogRetentionDays, maxLogRetentionDays)
		}
		days32 := int32(days)
		change.retentionDays = &days32
	}
	if change.enabled == nil && change.profile == nil && change.retentionDays == nil {
		return change, fmt.Errorf("name something to change: enabled, and for %s a profile or for %s retentionDays", platform.AddonMonitoring, platform.AddonLogging)
	}
	return change, nil
}

// handleAddonSave turns an add-on on or off, or changes its options. It writes only the fields that were sent, so that it
// cannot undo what somebody else changed in the meantime.
func (s *Server) handleAddonSave(w http.ResponseWriter, r *http.Request, addon platform.Addon) {
	fail := func(message string) { redirectProbe(w, r, addonsSettingsPath, dashboardLiteralError, message) }
	change, err := parseAddonChange(addon, r.Form)
	if err != nil {
		fail(err.Error())
		return
	}
	cluster, err := s.defaultGeassCluster(r.Context())
	if err != nil {
		fail("could not load the cluster")
		return
	}
	if change.enabled != nil && !*change.enabled && addon.Name == platform.AddonLonghorn && addon.Enabled(cluster.Spec.Addons) {
		// Switching it off does not remove it. It only stops Geass from looking after the volumes and storage classes it made,
		// so the one thing it would do is leave databases with nothing keeping their volumes right.
		fail("Replicated storage cannot be turned off here. Geass never removes it, because databases keep their data on it, and turning it off would stop Geass from looking after their volumes.")
		return
	}
	base := cluster.DeepCopy()
	applyAddonChange(&cluster.Spec.Addons, addon, change)
	if err := s.Client.Patch(r.Context(), cluster, client.MergeFrom(base)); err != nil {
		fail("could not save the add-on")
		return
	}
	redirect(w, r, addonsSettingsPath)
}

// applyAddonChange sets what the change names on the cluster's add-ons.
func applyAddonChange(spec *geassv1alpha1.GeassClusterAddonsSpec, addon platform.Addon, change addonChange) {
	if change.enabled != nil {
		enabled := *change.enabled
		switch addon.Name {
		case platform.AddonServerless:
			spec.Serverless.Enabled = &enabled
		case platform.AddonLonghorn:
			spec.Longhorn.Enabled = &enabled
		case platform.AddonMonitoring:
			spec.Monitoring.Enabled = &enabled
		case platform.AddonLogging:
			spec.Logging.Enabled = &enabled
		}
	}
	if change.profile != nil {
		spec.Monitoring.Profile = *change.profile
	}
	if change.retentionDays != nil {
		spec.Logging.RetentionDays = *change.retentionDays
	}
}

// handleAddonRetry asks the operator to start the install of an add-on over, when it is stuck. It does not do it itself: the
// install jobs are in kube-system, which the operator's role covers and a dashboard that runs apart from it may not.
func (s *Server) handleAddonRetry(w http.ResponseWriter, r *http.Request, addon platform.Addon) {
	fail := func(status int, message string) {
		writeJSON(w, status, map[string]string{dashboardLiteralError: message})
	}
	cluster, err := s.defaultGeassCluster(r.Context())
	if err != nil {
		fail(http.StatusInternalServerError, "could not load the cluster")
		return
	}
	view := addonSettingsOf(cluster)
	var current *addonView
	for i := range view.Addons {
		if view.Addons[i].Name == addon.Name {
			current = &view.Addons[i]
		}
	}
	if current == nil || !current.CanRetry {
		state := "not known"
		if current != nil {
			state = strings.ToLower(current.State)
		}
		fail(http.StatusConflict, fmt.Sprintf("%s is not stuck, so there is nothing to retry. It is %s.", addon.Title, state))
		return
	}
	if view.RetryPending != "" {
		fail(http.StatusConflict, "A retry is already waiting for the operator. Wait a moment and look again.")
		return
	}
	base := cluster.DeepCopy()
	if cluster.Annotations == nil {
		cluster.Annotations = map[string]string{}
	}
	cluster.Annotations[platform.AnnotationRetryAddon] = addon.Name
	if err := s.Client.Patch(r.Context(), cluster, client.MergeFrom(base)); err != nil {
		fail(http.StatusInternalServerError, "could not ask the operator to retry")
		return
	}
	redirect(w, r, addonsSettingsPath)
}
