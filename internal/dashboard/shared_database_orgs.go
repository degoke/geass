package dashboard

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// A shared database server holds the databases of one organization (platform.SharedServerAllows). The dashboard refuses
// a database that would break that (validateLogicalDatabaseServer), a project moved away from its servers'
// organization (databasesOnForeignServers) and a server given to an organization whose its databases are not
// (handleSharedDatabaseOrg). What was there before, or was written past the dashboard, is listed here for the platform's
// administrators, and the operator reports it on each database; neither moves or deletes anything.

// maxNamedInRefusal is how many databases a refusal names before it says how many more there are.
const maxNamedInRefusal = 5

// handleSharedDatabaseRoutes serves the platform's changes to a shared server other than making one.
func (s *Server) handleSharedDatabaseRoutes(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/shared-databases/"), "/"), "/")
	if len(parts) == 2 && parts[1] == "org" {
		s.handleSharedDatabaseOrg(w, r, parts[0])
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: "not found"})
}

// handleSharedDatabaseOrg serves POST /api/shared-databases/{name}/org: it gives a shared server to an organization (org),
// or to the platform (org empty). It is how a server the platform made for everyone becomes the one organization's that
// is the only one using it. It is refused while the server holds a database of a project that is not that organization's
// (for the platform, while organizations are on, one that is any organization's): giving it would put two owners' data on
// one server. It is the platform's alone.
func (s *Server) handleSharedDatabaseOrg(w http.ResponseWriter, r *http.Request, name string) {
	if !requireMutation(w, r, "/") || !parseFormOrRedirect(w, r, "/") || !requirePlatformContext(w, r) {
		return
	}
	ctx := r.Context()
	org := strings.TrimSpace(r.FormValue("org"))
	if org != "" {
		found, err := s.organization(ctx, org)
		if err != nil {
			writeDashboardUnavailable(w)
			return
		}
		if found == nil {
			orgFail(w, http.StatusNotFound, "no such organization")
			return
		}
	}
	var server geassv1alpha1.GeassDatabase
	if err := s.Client.Get(ctx, client.ObjectKey{Name: name, Namespace: systemNamespace}, &server); err != nil {
		if apierrors.IsNotFound(err) {
			orgFail(w, http.StatusNotFound, "no such database server")
			return
		}
		writeDashboardUnavailable(w)
		return
	}
	if !platform.IsSharedDatabase(&server) {
		orgFail(w, http.StatusBadRequest, "the database belongs to a project, and only a shared server is given to an organization")
		return
	}
	if server.Spec.Org == org {
		// Nothing changes, so nothing is mixed that was not before.
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "database": name, "org": org})
		return
	}
	message, err := s.serverWouldMix(ctx, server.Name, org)
	if err != nil {
		writeDashboardUnavailable(w)
		return
	}
	if message != "" {
		orgFail(w, http.StatusConflict, message)
		return
	}
	before := server.Spec.Org
	err = retry.RetryOnConflict(writeRetry, func() error {
		var latest geassv1alpha1.GeassDatabase
		if err := s.Client.Get(ctx, client.ObjectKeyFromObject(&server), &latest); err != nil {
			return err
		}
		if latest.Spec.Org == org {
			return nil
		}
		latest.Spec.Org = org
		return s.Client.Update(ctx, &latest)
	})
	if err != nil {
		orgFail(w, http.StatusInternalServerError, "could not change whose the server is")
		return
	}
	if before != org {
		ctrllog.FromContext(ctx).Info("Changed organization of database server", "database", name, "from", before, "to", org, "by", s.currentSession(r).Username)
		s.notifyResourceMutation(&server, "update")
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "database": name, "org": org})
}

// serverWouldMix says why the server cannot be the organization's (empty for the platform's): the databases on it whose
// projects are not. It is empty when there are none.
func (s *Server) serverWouldMix(ctx context.Context, server, org string) (string, error) {
	on, err := s.logicalDatabasesOn(ctx, server)
	if err != nil {
		return "", err
	}
	owners, err := s.projectOrganizations(ctx)
	if err != nil {
		return "", err
	}
	tenancy := s.tenancyOn(ctx)
	var named []string
	for i := range on {
		projectOrg := owners[on[i].Spec.Project]
		if platform.SharedServerAllows(tenancy, org, projectOrg) {
			continue
		}
		named = append(named, fmt.Sprintf("%s (project %s, %s)", on[i].Name, on[i].Spec.Project, ownerLabel(projectOrg, "no organization")))
	}
	if len(named) == 0 {
		return "", nil
	}
	slices.Sort(named)
	more := ""
	if len(named) > maxNamedInRefusal {
		more = fmt.Sprintf(" and %d more", len(named)-maxNamedInRefusal)
		named = named[:maxNamedInRefusal]
	}
	return fmt.Sprintf("%s, and the server holds databases that are not %s's: %s%s. Move their data to another server, or delete them, first",
		capitalizeFirst(platform.MessageOneOrganizationPerServer), ownerLabel(org, "the platform"), strings.Join(named, ", "), more), nil
}

// projectOrganizations is the organization of every project, by name ("" for none). A project that is not there has
// none, so its databases count as the platform's.
func (s *Server) projectOrganizations(ctx context.Context) (map[string]string, error) {
	var projects geassv1alpha1.GeassProjectList
	if err := s.Client.List(asSystem(ctx), &projects, client.InNamespace(systemNamespace)); err != nil {
		return nil, err
	}
	owners := make(map[string]string, len(projects.Items))
	for i := range projects.Items {
		owners[projects.Items[i].Name] = projects.Items[i].Spec.Org
	}
	return owners, nil
}

// ownerLabel names an organization, or says none for no name.
func ownerLabel(org, none string) string {
	if org == "" {
		return none
	}
	return "the organization " + org
}

// mixedDatabaseView is a logical database on a shared server of another owner than its project.
type mixedDatabaseView struct {
	Name        string `json:"name"`
	Project     string `json:"project"`
	Environment string `json:"environment"`
	// ProjectOrg and ServerOrg are whose the project and the server are; empty is no organization, the platform's.
	ProjectOrg string `json:"projectOrg"`
	Server     string `json:"server"`
	ServerOrg  string `json:"serverOrg"`
	// Made says the database is on the server already (it has a connection secret): it is kept and served. One that is
	// not is not made while organizations are on.
	Made    bool   `json:"made"`
	Message string `json:"message"`
}

// mixedServerView is a shared server that holds a mixed database, with whose all its databases are, which says what can
// be done: a server whose databases are all one organization's can be given to it (POST /api/shared-databases/{name}/org).
type mixedServerView struct {
	Name   string             `json:"name"`
	Org    string             `json:"org"`
	Owners []serverOwnerCount `json:"owners"`
}

type serverOwnerCount struct {
	// Org is the organization, or empty for the projects of none.
	Org       string `json:"org"`
	Databases int    `json:"databases"`
}

// handleAPISharedDatabaseMixing serves GET /api/shared-databases/mixed: every logical database whose shared server is not
// its project's organization's, as it is while organizations are on (the platform's server is for the projects of none).
// It says so whether organizations are on or not, so that what turning them on will report can be seen and moved first;
// enforced says whether they are on, and so whether the operator refuses and reports them. The platform's alone.
func (s *Server) handleAPISharedDatabaseMixing(w http.ResponseWriter, r *http.Request) {
	if !requirePlatformContext(w, r) {
		return
	}
	ctx := r.Context()
	var servers geassv1alpha1.GeassDatabaseList
	var logical geassv1alpha1.GeassLogicalDatabaseList
	for _, list := range []client.ObjectList{&servers, &logical} {
		if err := s.Client.List(asSystem(ctx), list, client.InNamespace(systemNamespace)); err != nil {
			writeDashboardUnavailable(w)
			return
		}
	}
	owners, err := s.projectOrganizations(ctx)
	if err != nil {
		writeDashboardUnavailable(w)
		return
	}
	shared := map[string]*geassv1alpha1.GeassDatabase{}
	for i := range servers.Items {
		if platform.IsSharedDatabase(&servers.Items[i]) {
			shared[servers.Items[i].Name] = &servers.Items[i]
		}
	}
	items := []mixedDatabaseView{}
	held := map[string]map[string]int{}
	for i := range logical.Items {
		db := &logical.Items[i]
		server, ok := shared[db.Spec.ServerRef]
		if !ok {
			continue
		}
		projectOrg := owners[db.Spec.Project]
		if held[server.Name] == nil {
			held[server.Name] = map[string]int{}
		}
		held[server.Name][projectOrg]++
		message := platform.SharedServerMixing(server.Name, server.Spec.Org, db.Spec.Project, projectOrg)
		if message == "" {
			continue
		}
		items = append(items, mixedDatabaseView{
			Name: db.Name, Project: db.Spec.Project, Environment: string(db.Spec.Environment), ProjectOrg: projectOrg,
			Server: server.Name, ServerOrg: server.Spec.Org, Made: db.Status.ConnectionSecret != "", Message: message,
		})
	}
	slices.SortFunc(items, func(a, b mixedDatabaseView) int {
		if c := strings.Compare(a.Server, b.Server); c != 0 {
			return c
		}
		return strings.Compare(a.Name, b.Name)
	})
	mixed := []mixedServerView{}
	for _, item := range items {
		if slices.ContainsFunc(mixed, func(v mixedServerView) bool { return v.Name == item.Server }) {
			continue
		}
		view := mixedServerView{Name: item.Server, Org: item.ServerOrg, Owners: []serverOwnerCount{}}
		for org, count := range held[item.Server] {
			view.Owners = append(view.Owners, serverOwnerCount{Org: org, Databases: count})
		}
		slices.SortFunc(view.Owners, func(a, b serverOwnerCount) int { return strings.Compare(a.Org, b.Org) })
		mixed = append(mixed, view)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"enforced": s.tenancyOn(ctx), jsonItems: items, "servers": mixed})
}
