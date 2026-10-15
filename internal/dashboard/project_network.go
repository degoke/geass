package dashboard

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// maxAllowedProjects matches the limit on the project's spec.
const maxAllowedProjects = 20

// formBlockNodeAddresses is the field that turns the block on the cluster's machines on or off.
const formBlockNodeAddresses = "blockNodeAddresses"

// handleProjectNetwork sets who can reach a project's services: only the project itself (and the
// platform and public domains), or also the other projects it names, and whether its services are kept
// away from the cluster's machines. Like every setting that widens access it is for signed-in admins; a
// project token cannot reach it.
func (s *Server) handleProjectNetwork(w http.ResponseWriter, r *http.Request, project string) {
	fallback := workspacePanelURL(project, "", "settings", "")
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	ctx := r.Context()
	network, blockNodes, err := s.projectNetworkFromForm(ctx, r, project)
	if err != nil {
		if platform.IsUserError(err) {
			redirectFormUserError(w, r, fallback, err)
			return
		}
		redirectFormInternalError(w, r, fallback)
		return
	}
	err = s.mutateProject(ctx, project, func(p *geassv1alpha1.GeassProject) error {
		// A request that does not mention the setting leaves it as it was, so a client written before it
		// existed, or one that only changes who may call in, cannot switch it off by accident.
		network.BlockNodeAddresses = geassv1alpha1.ProjectBlocksNodeAddresses(p)
		if blockNodes != nil {
			network.BlockNodeAddresses = *blockNodes
		}
		p.Spec.Network = network
		return nil
	})
	s.replyWebhookMutation(w, r, fallback, err, nil, map[string]any{"ok": true})
}

// projectNetworkFromForm reads and checks the submitted network settings. Naming a project that does
// not exist is refused, so a typo cannot quietly leave a door open for whoever later creates it. The second
// result is what the request said about blocking the cluster's machines, or nil when it said nothing.
func (s *Server) projectNetworkFromForm(ctx context.Context, r *http.Request, project string) (*geassv1alpha1.GeassProjectNetwork, *bool, error) {
	isolation := geassv1alpha1.GeassNetworkIsolation(strings.TrimSpace(r.FormValue("isolation")))
	if isolation != geassv1alpha1.NetworkIsolationProject && isolation != geassv1alpha1.NetworkIsolationNone {
		return nil, nil, platform.Userf("choose whether this project's services are private or open")
	}
	var blockNodes *bool
	if value := strings.TrimSpace(r.FormValue(formBlockNodeAddresses)); value != "" {
		// "on" is what a checkbox sends, and what a JSON true reaches this handler as.
		parsed, err := strconv.ParseBool(strings.NewReplacer("on", "true", "off", "false").Replace(strings.ToLower(value)))
		if err != nil {
			return nil, nil, platform.Userf("%s must be true or false", formBlockNodeAddresses)
		}
		blockNodes = &parsed
	}
	allowed := make([]string, 0, len(r.Form["allowFromProjects"]))
	for _, name := range r.Form["allowFromProjects"] {
		name = strings.TrimSpace(name)
		if name == "" || name == project || slices.Contains(allowed, name) {
			continue
		}
		allowed = append(allowed, name)
	}
	slices.Sort(allowed)
	if len(allowed) > maxAllowedProjects {
		return nil, nil, platform.Userf("at most %d other projects can be allowed", maxAllowedProjects)
	}
	for _, name := range allowed {
		err := s.Client.Get(ctx, client.ObjectKey{Name: name, Namespace: systemNamespace}, &geassv1alpha1.GeassProject{})
		if apierrors.IsNotFound(err) {
			return nil, nil, platform.Userf("project %q does not exist", name)
		}
		if err != nil {
			return nil, nil, err
		}
	}
	return &geassv1alpha1.GeassProjectNetwork{Isolation: isolation, AllowFromProjects: allowed}, blockNodes, nil
}

// handleAPIProjectNetwork says who can reach a project's services now: what a program needs to read before it
// changes one part of the setting and leaves the rest as it is.
func (s *Server) handleAPIProjectNetwork(w http.ResponseWriter, r *http.Request, name string) {
	var project geassv1alpha1.GeassProject
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &project); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: "project not found"})
		return
	}
	isolation, allowed := geassv1alpha1.NetworkIsolationProject, []string{}
	if project.Spec.Network != nil {
		if project.Spec.Network.Isolation != "" {
			isolation = project.Spec.Network.Isolation
		}
		allowed = append(allowed, project.Spec.Network.AllowFromProjects...)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"isolation": isolation, "allowFromProjects": allowed, formBlockNodeAddresses: geassv1alpha1.ProjectBlocksNodeAddresses(&project),
	})
}
