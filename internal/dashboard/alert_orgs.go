package dashboard

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/alerts"
)

// An organization's alerts. The platform keeps its own channels (and the mail server) under Settings; an organization has
// channels of its own, which hear only about its projects, are made and seen by its own administrators, and post to Slack.
// The same handlers serve both: what changes is whose channels a request may see and change, which alertScope says.

const orgAlertsWhat = "Alerts"

// alertScope is whose channels a request works on.
type alertScope struct {
	// org is the organization a channel must belong to, or "" for the platform, which may work on every channel.
	org string
	// workspace is the workspace the person works in, when they work in one and not in all of the organization.
	workspace string
}

func (s *Server) alertScopeOf(ctx context.Context) alertScope {
	tenant, ok := tenantOf(ctx)
	if !ok || tenant.Platform {
		return alertScope{}
	}
	return alertScope{org: tenant.Org, workspace: tenant.Workspace}
}

// owns is whether a request may see and change a channel. The platform may any channel. An organization's person may
// that organization's, and one who works in a workspace only those that name projects, all of which they can see: a channel
// of all the organization's projects is for its administrators.
func (a alertScope) owns(ctx context.Context, s *Server, org string, projects []string) bool {
	if a.org == "" {
		return true
	}
	if org != a.org {
		return false
	}
	if a.workspace == "" {
		return true
	}
	if len(projects) == 0 {
		return false
	}
	var visible geassv1alpha1.GeassProjectList
	if err := s.Client.List(ctx, &visible, client.InNamespace(systemNamespace)); err != nil {
		return false
	}
	seen := make(map[string]bool, len(visible.Items))
	for i := range visible.Items {
		seen[visible.Items[i].Name] = true
	}
	return !slices.ContainsFunc(projects, func(name string) bool { return !seen[name] })
}

// channelLimit says why one more channel for the owner cannot be made, or "" when it can. The platform has its own
// allowance, each organization has its own, and there is one for all of them, so that one cannot use up the others'.
func channelLimit(channels []geassv1alpha1.GeassAlertChannel, org string) string {
	held := 0
	for _, channel := range channels {
		if channel.Org == org {
			held++
		}
	}
	switch {
	case org == "" && held >= alerts.MaxChannels:
		return fmt.Sprintf("There can be at most %d channels.", alerts.MaxChannels)
	case org != "" && held >= alerts.MaxChannelsPerOrg:
		return fmt.Sprintf("An organization can have at most %d channels.", alerts.MaxChannelsPerOrg)
	case len(channels) >= alerts.MaxChannelsTotal:
		return "There are too many channels already."
	}
	return ""
}

// handleOrgAlertChannels serves an organization's changes to its channels. They are the platform's handlers, which know
// from the request whose channels it is for, so a person who is the platform's goes into an organization first.
func (s *Server) handleOrgAlertChannels(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, "/alerts") || !parseFormOrRedirect(w, r, "/alerts") || !s.requireSessionAdmin(w, r, orgAlertsWhat) || !requireOrgContext(w, r) {
		return
	}
	switch strings.TrimPrefix(r.URL.Path, "/alerts/channels/") {
	case verbSave:
		s.alertChannelSave(w, r)
	case dashboardLiteralDelete:
		s.alertChannelDelete(w, r)
	case "test":
		s.alertChannelTest(w, r)
	default:
		http.NotFound(w, r)
	}
}

// requireOrgContext refuses a request that is not for an organization: the platform's own settings are elsewhere.
func requireOrgContext(w http.ResponseWriter, r *http.Request) bool {
	if isPlatformContext(r.Context()) {
		writeJSON(w, http.StatusForbidden, map[string]string{dashboardLiteralError: "These are an organization's alerts. The platform's are in Settings."})
		return false
	}
	return true
}

type orgAlertsView struct {
	Kinds        []alerts.Kind      `json:"kinds"`
	Channels     []alertChannelView `json:"channels"`
	Listening    bool               `json:"listening"`
	Alertmanager alertmanagerView   `json:"alertmanager"`
	Firing       []firingView       `json:"firing"`
}

// handleAPIOrgAlerts serves GET /api/alerts: the channels of the caller's organization they may see, and what is firing for
// the projects they may see. It says nothing of the mail server, of other channels, or of the cluster.
func (s *Server) handleAPIOrgAlerts(w http.ResponseWriter, r *http.Request) {
	if !s.requireSessionAdmin(w, r, orgAlertsWhat) || !requireOrgContext(w, r) {
		return
	}
	ctx := r.Context()
	scope := s.alertScopeOf(ctx)
	config, err := alerts.Load(asSystem(ctx), s.Client, systemNamespace)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not load the alert settings"})
		return
	}
	view := orgAlertsView{Kinds: alerts.OrgKinds(), Channels: []alertChannelView{}, Firing: []firingView{}}
	for _, channel := range config.Channels {
		if channel.Org != scope.org || !scope.owns(ctx, s, channel.Org, channel.Projects) {
			continue
		}
		item := alertChannelView{
			ID: channel.ID, Name: channel.Name, Type: channel.Type, Recipients: []string{}, Events: nonNil(channel.Events),
			Projects: nonNil(channel.Projects), Environments: nonNil(channel.Environments),
			NotifyResolved: channel.NotifyResolved, Disabled: channel.Disabled,
		}
		if channel.SlackURL != "" {
			item.HasSecret = true
			item.Target = alerts.SlackHost(channel.SlackURL)
		}
		view.Channels = append(view.Channels, item)
		view.Listening = view.Listening || !channel.Disabled
	}

	// What is firing is shown for the projects the person may see, and for no other: an alert is about a project by its
	// label, and one that names none is the cluster's.
	var projects geassv1alpha1.GeassProjectList
	if err := s.Client.List(ctx, &projects, client.InNamespace(systemNamespace)); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not read the projects"})
		return
	}
	visible := make(map[string]bool, len(projects.Items))
	for i := range projects.Items {
		visible[projects.Items[i].Name] = true
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	manager := s.alertmanager()
	if err := manager.Ready(ctx); err != nil {
		view.Alertmanager = alertmanagerView{Error: "Alerts cannot be sent right now."}
	} else {
		view.Alertmanager.Ready = true
		if firing, err := manager.Firing(ctx); err == nil {
			for _, alert := range firing {
				if project := alert.Labels[alerts.LabelProject]; project != "" && visible[project] {
					view.Firing = append(view.Firing, firingOf(alert))
				}
			}
			sortFiring(view.Firing)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, view)
}

// removeOrganizationAlerts deletes the channels of an organization that is gone, and their secrets once the configuration
// no longer refers to them. It is best effort: a channel that is left routes on projects the organization no longer has,
// which is none, so it hears nothing.
func (s *Server) removeOrganizationAlerts(ctx context.Context, org string) {
	var gone []string
	err := s.mutateAlerting(ctx, func(spec *geassv1alpha1.GeassAlertingSpec) error {
		gone = gone[:0]
		spec.Channels = slices.DeleteFunc(spec.Channels, func(channel geassv1alpha1.GeassAlertChannel) bool {
			if channel.Org == org {
				gone = append(gone, channel.ID)
				return true
			}
			return false
		})
		return nil
	})
	if err != nil || len(gone) == 0 {
		return
	}
	if applyErr := s.applyAlerting(ctx); secretsMayGo(applyErr) {
		for _, id := range gone {
			s.deleteSecretKey(ctx, client.ObjectKey{Name: alerts.ChannelsSecretName, Namespace: systemNamespace}, id)
		}
	}
}
