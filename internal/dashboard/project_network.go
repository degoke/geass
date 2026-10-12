package dashboard

import (
	"context"
	"net/http"
	"slices"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// maxAllowedProjects matches the limit on the project's spec.
const maxAllowedProjects = 20

// handleProjectNetwork sets who can reach a project's services: only the project itself (and the
// platform and public domains), or also the other projects it names. Like every setting that widens
// access it is for signed-in admins; a project token cannot reach it.
func (s *Server) handleProjectNetwork(w http.ResponseWriter, r *http.Request, project string) {
	fallback := workspacePanelURL(project, "", "settings", "")
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	ctx := r.Context()
	network, err := s.projectNetworkFromForm(ctx, r, project)
	if err != nil {
		if platform.IsUserError(err) {
			redirectFormUserError(w, r, fallback, err)
			return
		}
		redirectFormInternalError(w, r, fallback)
		return
	}
	err = s.mutateProject(ctx, project, func(p *geassv1alpha1.GeassProject) error {
		p.Spec.Network = network
		return nil
	})
	s.replyWebhookMutation(w, r, fallback, err, nil, map[string]any{"ok": true})
}

// projectNetworkFromForm reads and checks the submitted network settings. Naming a project that does
// not exist is refused, so a typo cannot quietly leave a door open for whoever later creates it.
func (s *Server) projectNetworkFromForm(ctx context.Context, r *http.Request, project string) (*geassv1alpha1.GeassProjectNetwork, error) {
	isolation := geassv1alpha1.GeassNetworkIsolation(strings.TrimSpace(r.FormValue("isolation")))
	if isolation != geassv1alpha1.NetworkIsolationProject && isolation != geassv1alpha1.NetworkIsolationNone {
		return nil, platform.Userf("choose whether this project's services are private or open")
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
		return nil, platform.Userf("at most %d other projects can be allowed", maxAllowedProjects)
	}
	for _, name := range allowed {
		err := s.Client.Get(ctx, client.ObjectKey{Name: name, Namespace: systemNamespace}, &geassv1alpha1.GeassProject{})
		if apierrors.IsNotFound(err) {
			return nil, platform.Userf("project %q does not exist", name)
		}
		if err != nil {
			return nil, err
		}
	}
	return &geassv1alpha1.GeassProjectNetwork{Isolation: isolation, AllowFromProjects: allowed}, nil
}
