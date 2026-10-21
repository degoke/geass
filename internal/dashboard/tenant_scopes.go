package dashboard

import (
	"context"
	"slices"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/oidcauth"
)

// What a person from the identity provider may work in, once organizations are on. The provider says which organizations
// and workspaces they belong to and with what role; Geass keeps to the organizations it was told about (a
// GeassOrganization that names the provider's id), and to the workspaces those declare. What is left is a list of
// grants, which a session carries in its cookie, and the person works in one place at a time (the active scope).

// sessionGrant is what a person may do in one organization.
type sessionGrant struct {
	// Org is the name of the GeassOrganization.
	Org string
	// Admin is whether they administer the organization: they may work in all of it, or in any of its workspaces.
	Admin bool
	// All is whether they work in all of the organization without administering it: they read what it has, in any
	// workspace, with the viewer role. It is what a person who signs in through the organization's own provider, and maps to
	// viewer, is given: that provider says nothing of workspaces.
	All bool
	// Workspaces are the workspaces a person who is not an administrator works in, with the role they have in each ("" for
	// the one they have in the organization).
	Workspaces []workspaceGrant
}

type workspaceGrant struct {
	ID   string
	Role string
}

// sessionScope is a place a person may work in, as the dashboard lists them for the person to choose.
type sessionScope struct {
	Org           string `json:"org"`
	OrgName       string `json:"orgName,omitempty"`
	Workspace     string `json:"workspace,omitempty"`
	WorkspaceName string `json:"workspaceName,omitempty"`
	Role          string `json:"role"`
}

// grantsOf is what the token's memberships come to in Geass: the organizations that are set up (and not suspended), in
// the workspaces they declare. A person who does not administer an organization and is in none of its workspaces has no
// way in to it, so it is left out.
func grantsOf(memberships []oidcauth.Membership, orgs []geassv1alpha1.GeassOrganization) []sessionGrant {
	var grants []sessionGrant
	for i := range orgs {
		org := &orgs[i]
		if org.Spec.Suspended {
			continue
		}
		at := slices.IndexFunc(memberships, func(m oidcauth.Membership) bool { return m.Org == org.Spec.IdPID })
		if at < 0 {
			continue
		}
		member := memberships[at]
		grant := sessionGrant{Org: org.Name, Admin: member.Role == oidcauth.RoleAdmin}
		for _, mine := range member.Workspaces {
			for _, declared := range org.Spec.Workspaces {
				if declared.IdPID == mine.ID {
					grant.Workspaces = append(grant.Workspaces, workspaceGrant{ID: declared.ID, Role: normalizeScopeRole(mine.Role)})
				}
			}
		}
		if !grant.Admin && len(grant.Workspaces) == 0 {
			continue
		}
		slices.SortFunc(grant.Workspaces, func(a, b workspaceGrant) int { return strings.Compare(a.ID, b.ID) })
		grants = append(grants, grant)
	}
	slices.SortFunc(grants, func(a, b sessionGrant) int { return strings.Compare(a.Org, b.Org) })
	return grants
}

func normalizeScopeRole(role string) string {
	if role == oidcauth.RoleAdmin {
		return dashboardRoleAdmin
	}
	return ""
}

// roleIn is the role a person has working in an organization and workspace, and whether they may work there at all. An
// administrator of the organization may work in all of it and in any workspace, and has the admin role there. Anyone else
// works in a workspace they are in, with the higher of the role they have in the organization (viewer) and the one they
// have in the workspace.
func roleIn(grants []sessionGrant, org, workspace string) (string, bool) {
	for _, grant := range grants {
		if grant.Org != org {
			continue
		}
		if grant.Admin {
			return dashboardRoleAdmin, true
		}
		if grant.All {
			return dashboardRoleViewer, true
		}
		if workspace == "" {
			return "", false
		}
		for _, mine := range grant.Workspaces {
			if mine.ID == workspace {
				if mine.Role == dashboardRoleAdmin {
					return dashboardRoleAdmin, true
				}
				return dashboardRoleViewer, true
			}
		}
	}
	return "", false
}

// firstScope is where a person starts: the first organization, in all of it when they administer it and in its first
// workspace otherwise.
func firstScope(grants []sessionGrant) (org, workspace string, ok bool) {
	if len(grants) == 0 {
		return "", "", false
	}
	grant := grants[0]
	if grant.Admin || grant.All || len(grant.Workspaces) == 0 {
		return grant.Org, "", true
	}
	return grant.Org, grant.Workspaces[0].ID, true
}

// scopesOf lists where a person may work, with the names the dashboard shows for them.
func scopesOf(grants []sessionGrant, orgs []geassv1alpha1.GeassOrganization) []sessionScope {
	var scopes []sessionScope
	for _, grant := range grants {
		at := slices.IndexFunc(orgs, func(o geassv1alpha1.GeassOrganization) bool { return o.Name == grant.Org })
		if at < 0 {
			continue
		}
		org := orgs[at]
		names := map[string]string{}
		for _, workspace := range org.Spec.Workspaces {
			names[workspace.ID] = workspace.DisplayName
		}
		if grant.Admin || grant.All {
			role := dashboardRoleAdmin
			if !grant.Admin {
				role = dashboardRoleViewer
			}
			scopes = append(scopes, sessionScope{Org: org.Name, OrgName: org.Spec.DisplayName, Role: role})
			for _, workspace := range org.Spec.Workspaces {
				scopes = append(scopes, sessionScope{Org: org.Name, OrgName: org.Spec.DisplayName, Workspace: workspace.ID, WorkspaceName: workspace.DisplayName, Role: role})
			}
			continue
		}
		for _, mine := range grant.Workspaces {
			role := dashboardRoleViewer
			if mine.Role == dashboardRoleAdmin {
				role = dashboardRoleAdmin
			}
			scopes = append(scopes, sessionScope{Org: org.Name, OrgName: org.Spec.DisplayName, Workspace: mine.ID, WorkspaceName: names[mine.ID], Role: role})
		}
	}
	return scopes
}

// ---- what a cookie holds ----

// encodeGrants writes grants in the one field of a session cookie that holds them: organizations separated by ";", each
// "name~role~workspace:role,workspace:role" with the role as a (admin) or v (viewer).
func encodeGrants(grants []sessionGrant) string {
	parts := make([]string, 0, len(grants))
	for _, grant := range grants {
		role := "v"
		switch {
		case grant.Admin:
			role = "a"
		case grant.All:
			role = "w"
		}
		workspaces := make([]string, 0, len(grant.Workspaces))
		for _, mine := range grant.Workspaces {
			workspaceRole := "v"
			if mine.Role == dashboardRoleAdmin {
				workspaceRole = "a"
			}
			workspaces = append(workspaces, mine.ID+":"+workspaceRole)
		}
		parts = append(parts, grant.Org+"~"+role+"~"+strings.Join(workspaces, ","))
	}
	return strings.Join(parts, ";")
}

// decodeGrants is the other way round. It is false for text that is not what encodeGrants writes.
func decodeGrants(text string) ([]sessionGrant, bool) {
	if text == "" {
		return nil, true
	}
	var grants []sessionGrant
	for entry := range strings.SplitSeq(text, ";") {
		fields := strings.Split(entry, "~")
		if len(fields) != 3 || fields[0] == "" || (fields[1] != "a" && fields[1] != "v" && fields[1] != "w") {
			return nil, false
		}
		grant := sessionGrant{Org: fields[0], Admin: fields[1] == "a", All: fields[1] == "w"}
		if fields[2] != "" {
			for item := range strings.SplitSeq(fields[2], ",") {
				id, role, found := strings.Cut(item, ":")
				if !found || id == "" || (role != "a" && role != "v") {
					return nil, false
				}
				workspaceRole := ""
				if role == "a" {
					workspaceRole = dashboardRoleAdmin
				}
				grant.Workspaces = append(grant.Workspaces, workspaceGrant{ID: id, Role: workspaceRole})
			}
		}
		grants = append(grants, grant)
	}
	return grants, true
}

// ---- the organizations ----

// listOrganizations reads the organizations that are set up. It is the dashboard's own work: what a person is shown of
// them is decided by the caller.
func (s *Server) listOrganizations(ctx context.Context) ([]geassv1alpha1.GeassOrganization, error) {
	var orgs geassv1alpha1.GeassOrganizationList
	if err := s.Client.List(asSystem(ctx), &orgs, client.InNamespace(systemNamespace)); err != nil {
		return nil, err
	}
	// An organization that is being deleted (the operator is clearing up after it) is already gone to everyone.
	return slices.DeleteFunc(orgs.Items, func(org geassv1alpha1.GeassOrganization) bool { return !org.DeletionTimestamp.IsZero() }), nil
}

// organization reads one, or nil when there is none by that name.
func (s *Server) organization(ctx context.Context, name string) (*geassv1alpha1.GeassOrganization, error) {
	var org geassv1alpha1.GeassOrganization
	if err := s.Client.Get(asSystem(ctx), client.ObjectKey{Name: name, Namespace: systemNamespace}, &org); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	if !org.DeletionTimestamp.IsZero() {
		return nil, nil
	}
	return &org, nil
}

// scopeStillStands is whether the organization (and the workspace in it) a session works in is still there and open: it
// is checked on each request, so that an organization that is suspended or removed, or a workspace that is, stops at once and
// not when the cookie runs out.
func (s *Server) scopeStillStands(ctx context.Context, org, workspace string) bool {
	found, err := s.organization(ctx, org)
	if err != nil || found == nil || found.Spec.Suspended {
		return false
	}
	if workspace == "" {
		return true
	}
	return slices.ContainsFunc(found.Spec.Workspaces, func(w geassv1alpha1.GeassOrganizationWorkspace) bool { return w.ID == workspace })
}

// tenantGrantFor is the tenant of an organization session: the organization and workspace it works in, once they are shown
// to still stand.
func (s *Server) tenantGrantFor(ctx context.Context, session *dashboardSessionInfo) Tenant {
	if session.Org == "" || !s.scopeStillStands(ctx, session.Org, session.Workspace) {
		return Tenant{}
	}
	return Tenant{Org: session.Org, Workspace: session.Workspace}
}

// deniedOrgPending is the sign-in error of a person whose organization was made for them and is waiting to be approved.
const deniedOrgPending = "org_pending"

// placeInOrganization decides where a person starts working once organizations are on, and puts it in the claims of their
// session. It returns "" when they have somewhere to work, and the code of a sign-in error when they have not: a person the
// provider put in no organization Geass knows, and who is no platform administrator.
func (s *Server) placeInOrganization(ctx context.Context, claims *ssoSessionClaims, identity *oidcauth.Identity, settings ssoSettings) string {
	orgs, err := s.listOrganizations(ctx)
	if err != nil {
		return ssoUnavailable
	}
	// An organization the platform lets a sign-in make is made now, and read again with the others.
	if settings.AutoProvision != nil {
		if err := s.autoProvisionOrganizations(ctx, settings, identity, orgs); err != nil {
			return ssoUnavailable
		}
		if orgs, err = s.listOrganizations(ctx); err != nil {
			return ssoUnavailable
		}
	}
	claims.Grants = grantsOf(identity.Memberships, orgs)
	claims.Platform = identity.Platform
	if identity.Platform {
		// A platform administrator starts as the platform, and may go into an organization from there.
		claims.Org, claims.Workspace, claims.Role = "", "", dashboardRoleAdmin
		return ""
	}
	org, workspace, ok := firstScope(claims.Grants)
	if !ok {
		if waitingForApproval(identity, orgs) {
			return deniedOrgPending
		}
		return oidcauth.DeniedNoOrg
	}
	role, _ := roleIn(claims.Grants, org, workspace)
	claims.Org, claims.Workspace, claims.Role = org, workspace, role
	return ""
}

// testOrganizations says which of the organizations a test sign-in's token names are set up in Geass, so that the person
// who is testing can see that the claim is read as they meant it.
func (s *Server) testOrganizations(ctx context.Context, identity *oidcauth.Identity) []ssoTestOrg {
	orgs, _ := s.listOrganizations(ctx)
	out := make([]ssoTestOrg, 0, len(identity.Memberships))
	for _, member := range identity.Memberships {
		entry := ssoTestOrg{IdPID: member.Org, Role: member.Role, Workspaces: len(member.Workspaces)}
		if at := slices.IndexFunc(orgs, func(o geassv1alpha1.GeassOrganization) bool { return o.Spec.IdPID == member.Org }); at >= 0 {
			entry.Name, entry.Suspended = orgs[at].Name, orgs[at].Spec.Suspended
		}
		out = append(out, entry)
	}
	return out
}

// sessionScopes is where a session may work, for the page to offer: nothing while organizations are off. A platform
// administrator is offered the platform, and every organization, in which they are administrators.
func (s *Server) sessionScopes(ctx context.Context, session *dashboardSessionInfo) []sessionScope {
	if session == nil || !s.tenancyOn(ctx) {
		return nil
	}
	orgs, err := s.listOrganizations(ctx)
	if err != nil {
		return nil
	}
	if !session.PlatformAdmin {
		return scopesOf(session.Grants, orgs)
	}
	grants := make([]sessionGrant, 0, len(orgs))
	for i := range orgs {
		if !orgs[i].Spec.Suspended {
			grants = append(grants, sessionGrant{Org: orgs[i].Name, Admin: true})
		}
	}
	slices.SortFunc(grants, func(a, b sessionGrant) int { return strings.Compare(a.Org, b.Org) })
	return append([]sessionScope{{Role: dashboardRoleAdmin}}, scopesOf(grants, orgs)...)
}
