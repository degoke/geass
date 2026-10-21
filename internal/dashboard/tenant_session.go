package dashboard

import (
	"context"
	"net/http"

	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/apitokens"
)

// tenancyOn is whether organizations are on. It is the scoped client that knows: a dashboard whose client is not
// scoped (a test's, or a build without organizations) has them off, and reads nothing to find out.
func (s *Server) tenancyOn(ctx context.Context) bool {
	scoped, ok := s.Client.(*tenantClient)
	return ok && scoped.enabled != nil && scoped.enabled(ctx)
}

// tenantOfSession is whose request this is. While organizations are off, everyone who may sign in acts for the platform,
// as they always did. Once they are on, only the platform's administrators do; everyone else acts for the organization
// and workspace their session or token is bound to, and a project's token for that one project.
func (s *Server) tenantOfSession(r *http.Request, session *dashboardSessionInfo) Tenant {
	ctx := r.Context()
	if !s.tenancyOn(ctx) {
		return Tenant{Platform: true}
	}
	// A platform administrator acts for the platform until they go into an organization, which they may do to see what it
	// sees; in one, they are that organization's.
	if session.PlatformAdmin && session.Org == "" {
		return Tenant{Platform: true}
	}
	if record := tokenFromRequest(r); record != nil && record.Scope == apitokens.ScopeProject {
		// A project's token takes the organization of the project it was made for. A project that belongs to nobody
		// has no organization, so its token reaches nothing until a platform administrator assigns the project.
		var project geassv1alpha1.GeassProject
		if err := s.Client.Get(asSystem(ctx), client.ObjectKey{Name: record.Project, Namespace: systemNamespace}, &project); err != nil {
			return Tenant{}
		}
		return Tenant{Org: project.Spec.Org, Workspace: project.Spec.Workspace, Project: project.Name}
	}
	return s.tenantGrantFor(ctx, session)
}
