package dashboard

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// Organizations are made and changed by the platform's administrators, and only by them: an organization cannot make
// itself, change what the provider's ids it is known by, or move a project in or out. What is in an organization is what
// its own administrators then do (see the scoped client).

const (
	maxOrgDisplayName = 100
	maxOrgIdPID       = 253
	maxOrgWorkspaces  = 100
)

type orgView struct {
	Name        string          `json:"name"`
	DisplayName string          `json:"displayName"`
	IdPID       string          `json:"idpID"`
	Suspended   bool            `json:"suspended"`
	ClusterRef  string          `json:"clusterRef,omitempty"`
	Workspaces  []workspaceView `json:"workspaces"`
	Projects    int             `json:"projects"`
	// What the platform sets for all its projects.
	MaxProjects    *int32        `json:"maxProjects,omitempty"`
	Quota          *orgQuotaView `json:"quota,omitempty"`
	Region         string        `json:"region,omitempty"`
	RequiredTags   []string      `json:"requiredTags,omitempty"`
	AllowedDomains []string      `json:"allowedDomains,omitempty"`
	// AutoCreated says a sign-in made it, and not a platform administrator; one that is also Suspended is waiting for approval.
	AutoCreated bool `json:"autoCreated,omitempty"`
	// IdP is the organization's own identity provider, when it has one.
	IdP *orgIdPView `json:"idp,omitempty"`
}

type workspaceView struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	IdPID       string `json:"idpID"`
	Projects    int    `json:"projects"`
}

// requirePlatformContext refuses anyone who is not acting for the platform. The routes that call it are not all in the
// platform's class (assigning a project is a project route), so they ask again.
func requirePlatformContext(w http.ResponseWriter, r *http.Request) bool {
	if !isPlatformContext(r.Context()) {
		writeJSON(w, http.StatusForbidden, map[string]string{dashboardLiteralError: "only the platform's administrators can do this"})
		return false
	}
	return true
}

func randomID(prefix string, bytes int) (string, error) {
	buffer := make([]byte, bytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(buffer), nil
}

// handleAPIOrgs serves GET /api/orgs: the organizations, with how many projects each has.
func (s *Server) handleAPIOrgs(w http.ResponseWriter, r *http.Request) {
	if !requirePlatformContext(w, r) {
		return
	}
	orgs, err := s.listOrganizations(r.Context())
	if err != nil {
		writeDashboardUnavailable(w)
		return
	}
	var projects geassv1alpha1.GeassProjectList
	if err := s.Client.List(r.Context(), &projects, client.InNamespace(systemNamespace)); err != nil {
		writeDashboardUnavailable(w)
		return
	}
	perOrg, perWorkspace := map[string]int{}, map[string]int{}
	for i := range projects.Items {
		if org := projects.Items[i].Spec.Org; org != "" {
			perOrg[org]++
			if workspace := projects.Items[i].Spec.Workspace; workspace != "" {
				perWorkspace[org+"/"+workspace]++
			}
		}
	}
	items := make([]orgView, 0, len(orgs))
	for i := range orgs {
		org := &orgs[i]
		view := orgView{Name: org.Name, DisplayName: org.Spec.DisplayName, IdPID: org.Spec.IdPID, Suspended: org.Spec.Suspended, Projects: perOrg[org.Name], Workspaces: []workspaceView{}}
		if org.Spec.ClusterRef != nil {
			view.ClusterRef = org.Spec.ClusterRef.Name
		}
		view.MaxProjects, view.Quota, view.Region = org.Spec.MaxProjects, orgQuotaViewOf(org.Spec.Quota), org.Spec.Region
		view.RequiredTags, view.AllowedDomains = org.Spec.RequiredTags, org.Spec.AllowedDomains
		view.AutoCreated = org.Annotations[annotationAutoCreated] == boolTrue
		view.IdP = s.orgIdPViewOf(org)
		for _, workspace := range org.Spec.Workspaces {
			view.Workspaces = append(view.Workspaces, workspaceView{ID: workspace.ID, DisplayName: workspace.DisplayName, IdPID: workspace.IdPID, Projects: perWorkspace[org.Name+"/"+workspace.ID]})
		}
		items = append(items, view)
	}
	slices.SortFunc(items, func(a, b orgView) int {
		return strings.Compare(strings.ToLower(a.DisplayName), strings.ToLower(b.DisplayName))
	})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{jsonItems: items})
}

// handleOrgRoutes serves the platform's changes to organizations: create, update, delete, and their workspaces.
func (s *Server) handleOrgRoutes(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, "/") || !parseFormOrRedirect(w, r, "/") || !requirePlatformContext(w, r) {
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/orgs/"), "/"), "/")
	switch {
	case len(parts) == 1 && parts[0] == verbCreate:
		s.handleOrgCreate(w, r)
	case len(parts) == 2 && parts[1] == "update":
		s.handleOrgUpdate(w, r, parts[0])
	case len(parts) == 2 && parts[1] == dashboardLiteralDelete:
		s.handleOrgDelete(w, r, parts[0])
	case len(parts) == 3 && parts[1] == "idp":
		s.handleOrgIdP(w, r, parts[0], parts[2])
	case len(parts) == 3 && parts[1] == "workspaces" && parts[2] == verbCreate:
		s.handleWorkspaceCreate(w, r, parts[0])
	case len(parts) == 4 && parts[1] == "workspaces" && parts[3] == dashboardLiteralDelete:
		s.handleWorkspaceDelete(w, r, parts[0], parts[2])
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: "not found"})
	}
}

func orgFail(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{dashboardLiteralError: message})
}

// checkOrgText refuses text that is empty or longer than it may be.
func checkOrgText(label, value string, limit int) error {
	switch {
	case value == "":
		return fmt.Errorf("%s is required", label)
	case len(value) > limit:
		return fmt.Errorf("%s can be at most %d characters", label, limit)
	}
	return nil
}

// idpIDTaken is whether another organization is already known by the provider's id: two would make a person's token
// name both, and decide which they sign in to by the order they happen to be listed in.
func idpIDTaken(orgs []geassv1alpha1.GeassOrganization, idpID, except string) bool {
	return slices.ContainsFunc(orgs, func(o geassv1alpha1.GeassOrganization) bool { return o.Name != except && o.Spec.IdPID == idpID })
}

// clusterRefFrom reads and checks the cluster a form names: nothing for the platform's default, or one that exists.
func (s *Server) clusterRefFrom(r *http.Request, value string) (*corev1.LocalObjectReference, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	var cluster geassv1alpha1.GeassCluster
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: value, Namespace: systemNamespace}, &cluster); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("the cluster %q does not exist", value)
		}
		return nil, err
	}
	return &corev1.LocalObjectReference{Name: value}, nil
}

func (s *Server) handleOrgCreate(w http.ResponseWriter, r *http.Request) {
	displayName, idpID := strings.TrimSpace(r.FormValue("displayName")), strings.TrimSpace(r.FormValue("idpID"))
	if err := checkOrgText("The name", displayName, maxOrgDisplayName); err != nil {
		orgFail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := checkOrgText("The provider's id of the organization", idpID, maxOrgIdPID); err != nil {
		orgFail(w, http.StatusBadRequest, err.Error())
		return
	}
	orgs, err := s.listOrganizations(r.Context())
	if err != nil {
		writeDashboardUnavailable(w)
		return
	}
	if idpIDTaken(orgs, idpID, "") {
		orgFail(w, http.StatusConflict, "another organization is already known by that id")
		return
	}
	clusterRef, err := s.clusterRefFrom(r, r.FormValue("clusterRef"))
	if err != nil {
		orgFail(w, http.StatusBadRequest, err.Error())
		return
	}
	name, err := randomID("org-", 4)
	if err != nil {
		writeDashboardUnavailable(w)
		return
	}
	org := &geassv1alpha1.GeassOrganization{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: systemNamespace},
		Spec:       geassv1alpha1.GeassOrganizationSpec{DisplayName: displayName, IdPID: idpID, ClusterRef: clusterRef},
	}
	if err := s.applyOrgPolicyForm(r.Context(), r, org, orgs); err != nil {
		var refused userError
		if errors.As(err, &refused) {
			orgFail(w, refused.status, refused.text)
			return
		}
		writeDashboardUnavailable(w)
		return
	}
	if err := s.Client.Create(r.Context(), org); err != nil {
		orgFail(w, http.StatusInternalServerError, "could not create the organization")
		return
	}
	ctrllog.FromContext(r.Context()).Info("Created organization", "organization", name, "displayName", displayName, "by", s.currentSession(r).Username)
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, pathNameParam: name})
}

// mutateOrganization applies change to the latest copy of an organization, trying again when it was written meanwhile.
func (s *Server) mutateOrganization(r *http.Request, name string, change func(*geassv1alpha1.GeassOrganization) error) error {
	return retry.RetryOnConflict(writeRetry, func() error {
		// The organizations are the platform's; an organization's own change to what is its own (its registry) is made by
		// a handler that has checked who is asking.
		ctx := asSystem(r.Context())
		var org geassv1alpha1.GeassOrganization
		if err := s.Client.Get(ctx, client.ObjectKey{Name: name, Namespace: systemNamespace}, &org); err != nil {
			return err
		}
		if err := change(&org); err != nil {
			return err
		}
		return s.Client.Update(ctx, &org)
	})
}

// userError is a refusal that is told to whoever asked, as opposed to a failure of the dashboard's own.
type userError struct {
	status int
	text   string
}

func (e userError) Error() string { return e.text }

func (s *Server) finishOrgChange(w http.ResponseWriter, err error, ok map[string]any) {
	var refused userError
	switch {
	case err == nil:
		ok["ok"] = true
		writeJSON(w, http.StatusOK, ok)
	case apierrors.IsNotFound(err):
		orgFail(w, http.StatusNotFound, "no such organization")
	case errors.As(err, &refused):
		orgFail(w, refused.status, refused.text)
	default:
		orgFail(w, http.StatusInternalServerError, "could not change the organization")
	}
}

func (s *Server) handleOrgUpdate(w http.ResponseWriter, r *http.Request, name string) {
	orgs, err := s.listOrganizations(r.Context())
	if err != nil {
		writeDashboardUnavailable(w)
		return
	}
	err = s.mutateOrganization(r, name, func(org *geassv1alpha1.GeassOrganization) error {
		if formHasValue(r, "displayName") {
			value := strings.TrimSpace(r.FormValue("displayName"))
			if err := checkOrgText("The name", value, maxOrgDisplayName); err != nil {
				return userError{http.StatusBadRequest, err.Error()}
			}
			org.Spec.DisplayName = value
		}
		if formHasValue(r, "idpID") {
			value := strings.TrimSpace(r.FormValue("idpID"))
			if err := checkOrgText("The provider's id of the organization", value, maxOrgIdPID); err != nil {
				return userError{http.StatusBadRequest, err.Error()}
			}
			if idpIDTaken(orgs, value, org.Name) {
				return userError{http.StatusConflict, "another organization is already known by that id"}
			}
			org.Spec.IdPID = value
		}
		if formHasValue(r, "suspended") {
			org.Spec.Suspended = r.FormValue("suspended") == "on" || r.FormValue("suspended") == boolTrue
		}
		if formHasValue(r, "clusterRef") {
			ref, err := s.clusterRefFrom(r, r.FormValue("clusterRef"))
			if err != nil {
				return userError{http.StatusBadRequest, err.Error()}
			}
			org.Spec.ClusterRef = ref
		}
		return s.applyOrgPolicyForm(r.Context(), r, org, orgs)
	})
	if err == nil {
		ctrllog.FromContext(r.Context()).Info("Changed organization", "organization", name, "by", s.currentSession(r).Username)
	}
	s.finishOrgChange(w, err, map[string]any{pathNameParam: name})
}

// projectsIn counts the projects that belong to an organization, or to one workspace of it when workspace is set. A
// project that is being deleted is not counted. It reads every project, whoever asks: the answer is the count.
func (s *Server) projectsIn(ctx context.Context, org, workspace string) (int, error) {
	var projects geassv1alpha1.GeassProjectList
	if err := s.Client.List(asSystem(ctx), &projects, client.InNamespace(systemNamespace)); err != nil {
		return 0, err
	}
	count := 0
	for i := range projects.Items {
		spec := projects.Items[i].Spec
		if spec.Org == org && (workspace == "" || spec.Workspace == workspace) && projects.Items[i].DeletionTimestamp.IsZero() {
			count++
		}
	}
	return count, nil
}

// errProjectLimit is what someone is told whose organization has all the projects it may have.
func errProjectLimit(limit int32) error {
	return platform.Userf("Your organization has reached its limit of %d projects. Ask a platform administrator to raise it, or delete a project you no longer use", limit)
}

// checkProjectLimit refuses another project for the organization once it has as many as it may. The project named
// except is not counted, which is how a project that is being moved in, or has just been made, is let through. An
// organization that is not there, or has no limit, has none.
func (s *Server) checkProjectLimit(ctx context.Context, orgName, except string) error {
	org, err := s.organization(ctx, orgName)
	if err != nil {
		return err
	}
	if org == nil || org.Spec.MaxProjects == nil {
		return nil
	}
	held, err := s.projectsIn(ctx, orgName, "")
	if err != nil {
		return err
	}
	if except != "" {
		var project geassv1alpha1.GeassProject
		if err := s.Client.Get(asSystem(ctx), client.ObjectKey{Name: except, Namespace: systemNamespace}, &project); err == nil && project.Spec.Org == orgName && project.DeletionTimestamp.IsZero() {
			held--
		}
	}
	// Counting the project asked about: it is the one more there would be.
	if int32(held)+1 > *org.Spec.MaxProjects {
		return errProjectLimit(*org.Spec.MaxProjects)
	}
	return nil
}

func (s *Server) handleOrgDelete(w http.ResponseWriter, r *http.Request, name string) {
	if strings.TrimSpace(r.FormValue("confirmName")) != name {
		orgFail(w, http.StatusBadRequest, "type the organization's name to confirm deleting it")
		return
	}
	org, err := s.organization(r.Context(), name)
	if err != nil {
		writeDashboardUnavailable(w)
		return
	}
	if org == nil {
		orgFail(w, http.StatusNotFound, "no such organization")
		return
	}
	held, err := s.projectsIn(r.Context(), name, "")
	if err != nil {
		writeDashboardUnavailable(w)
		return
	}
	if held > 0 {
		orgFail(w, http.StatusConflict, fmt.Sprintf("the organization still has %d projects: move or delete them first", held))
		return
	}
	if owned, err := s.organizationOwns(r.Context(), name); err != nil {
		writeDashboardUnavailable(w)
		return
	} else if owned != "" {
		orgFail(w, http.StatusConflict, "the organization still owns "+owned+": delete it first")
		return
	}
	if err := s.Client.Delete(r.Context(), org); err != nil && !apierrors.IsNotFound(err) {
		orgFail(w, http.StatusInternalServerError, "could not delete the organization")
		return
	}
	// What the organization set up outside its projects goes with it: its alert channels, and the Slack addresses that
	// are their secrets.
	s.removeOrganizationAlerts(r.Context(), name)
	s.removeOrganizationRegistry(r.Context(), name)
	ctrllog.FromContext(r.Context()).Info("Deleted organization", "organization", name, "by", s.currentSession(r).Username)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleWorkspaceCreate(w http.ResponseWriter, r *http.Request, orgName string) {
	displayName, idpID := strings.TrimSpace(r.FormValue("displayName")), strings.TrimSpace(r.FormValue("idpID"))
	if err := checkOrgText("The workspace's name", displayName, maxOrgDisplayName); err != nil {
		orgFail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := checkOrgText("The provider's id of the workspace", idpID, maxOrgIdPID); err != nil {
		orgFail(w, http.StatusBadRequest, err.Error())
		return
	}
	id, err := randomID("ws-", 3)
	if err != nil {
		writeDashboardUnavailable(w)
		return
	}
	err = s.mutateOrganization(r, orgName, func(org *geassv1alpha1.GeassOrganization) error {
		if len(org.Spec.Workspaces) >= maxOrgWorkspaces {
			return userError{http.StatusConflict, fmt.Sprintf("an organization can have at most %d workspaces", maxOrgWorkspaces)}
		}
		for _, existing := range org.Spec.Workspaces {
			if existing.IdPID == idpID || existing.ID == id {
				return userError{http.StatusConflict, "the organization already has a workspace known by that id"}
			}
		}
		org.Spec.Workspaces = append(org.Spec.Workspaces, geassv1alpha1.GeassOrganizationWorkspace{ID: id, DisplayName: displayName, IdPID: idpID})
		return nil
	})
	s.finishOrgChange(w, err, map[string]any{pathNameParam: orgName, "workspace": id})
}

func (s *Server) handleWorkspaceDelete(w http.ResponseWriter, r *http.Request, orgName, id string) {
	held, err := s.projectsIn(r.Context(), orgName, id)
	if err != nil {
		writeDashboardUnavailable(w)
		return
	}
	if held > 0 {
		orgFail(w, http.StatusConflict, fmt.Sprintf("the workspace still has %d projects: move or delete them first", held))
		return
	}
	err = s.mutateOrganization(r, orgName, func(org *geassv1alpha1.GeassOrganization) error {
		at := slices.IndexFunc(org.Spec.Workspaces, func(w geassv1alpha1.GeassOrganizationWorkspace) bool { return w.ID == id })
		if at < 0 {
			return userError{http.StatusNotFound, "no such workspace"}
		}
		org.Spec.Workspaces = slices.Delete(org.Spec.Workspaces, at, at+1)
		return nil
	})
	s.finishOrgChange(w, err, map[string]any{pathNameParam: orgName})
}

// handleProjectAssign serves POST /api/projects/{p}/assign: it puts a project in an organization, and in a workspace of it,
// or takes it out of its organization when none is named. It is the platform's alone: it is how a project that was made
// before organizations existed is given to one, and how one is moved between them.
func (s *Server) handleProjectAssign(w http.ResponseWriter, r *http.Request, name string) {
	if !requireMutation(w, r, "/") || !parseFormOrRedirect(w, r, "/") || !requirePlatformContext(w, r) {
		return
	}
	org, workspace := strings.TrimSpace(r.FormValue("org")), strings.TrimSpace(r.FormValue("workspace"))
	switch {
	case org == "" && workspace != "":
		orgFail(w, http.StatusBadRequest, "name the organization as well as the workspace")
		return
	case org != "":
		found, err := s.organization(r.Context(), org)
		if err != nil {
			writeDashboardUnavailable(w)
			return
		}
		if found == nil {
			orgFail(w, http.StatusNotFound, "no such organization")
			return
		}
		if workspace != "" && !slices.ContainsFunc(found.Spec.Workspaces, func(w geassv1alpha1.GeassOrganizationWorkspace) bool { return w.ID == workspace }) {
			orgFail(w, http.StatusNotFound, "the organization has no such workspace")
			return
		}
		// A project the organization has already is not one more; one that comes from outside is.
		if err := s.checkProjectLimit(r.Context(), org, name); err != nil {
			if platform.IsUserError(err) {
				orgFail(w, http.StatusConflict, err.Error())
				return
			}
			writeDashboardUnavailable(w)
			return
		}
	}
	// A project that has databases on a server another organization owns keeps its login to it: the server's data is not
	// the new organization's to hold, and the project would be taking the old one's with it.
	if message, err := s.databasesOnForeignServers(r.Context(), name, org); err != nil {
		writeDashboardUnavailable(w)
		return
	} else if message != "" {
		orgFail(w, http.StatusConflict, message)
		return
	}
	err := s.mutateProject(r.Context(), name, func(project *geassv1alpha1.GeassProject) error {
		project.Spec.Org, project.Spec.Workspace = org, workspace
		return nil
	})
	switch {
	case apierrors.IsNotFound(err):
		orgFail(w, http.StatusNotFound, "project not found")
	case err != nil:
		orgFail(w, http.StatusInternalServerError, "could not assign the project")
	default:
		ctrllog.FromContext(r.Context()).Info("Assigned project", "project", name, "organization", org, "workspace", workspace, "by", s.currentSession(r).Username)
		s.notifyChangeIn(name, "projects", name, "assign")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "project": name, "org": org, "workspace": workspace})
	}
}

// databasesOnForeignServers says why a project cannot be given to the organization (empty for the platform): it has
// logical databases on a shared server that belongs to another organization or, while organizations are on, on the
// platform's server, which is for the projects of none (platform.SharedServerAllows). A project that stays in the
// organization it is in changes nothing about whose its servers are, so it is never held back. It is empty when there are
// none.
func (s *Server) databasesOnForeignServers(ctx context.Context, project, org string) (string, error) {
	var current geassv1alpha1.GeassProject
	if err := s.Client.Get(asSystem(ctx), client.ObjectKey{Name: project, Namespace: systemNamespace}, &current); err == nil && current.Spec.Org == org {
		return "", nil
	} else if err != nil && !apierrors.IsNotFound(err) {
		return "", err
	}
	tenancy := s.tenancyOn(ctx)
	var logical geassv1alpha1.GeassLogicalDatabaseList
	if err := s.Client.List(asSystem(ctx), &logical, client.InNamespace(systemNamespace)); err != nil {
		return "", err
	}
	for i := range logical.Items {
		if logical.Items[i].Spec.Project != project {
			continue
		}
		var server geassv1alpha1.GeassDatabase
		if err := s.Client.Get(asSystem(ctx), client.ObjectKey{Name: logical.Items[i].Spec.ServerRef, Namespace: systemNamespace}, &server); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return "", err
		}
		if !platform.IsSharedDatabase(&server) || platform.SharedServerAllows(tenancy, server.Spec.Org, org) {
			continue
		}
		if server.Spec.Org != "" {
			return "the project has databases on a server that belongs to another organization: delete them first", nil
		}
		return fmt.Sprintf("the project has databases on the platform's database server %s, and with organizations on %s: "+
			"move their data to a server of the organization, or delete them, first", server.Name, platform.MessageOneOrganizationPerServer), nil
	}
	return "", nil
}

// organizationOwns says what an organization still owns that is not a project, which has to be deleted before it is: its
// shared database servers and its cloud connections. It is empty when there are none.
func (s *Server) organizationOwns(ctx context.Context, org string) (string, error) {
	var servers geassv1alpha1.GeassDatabaseList
	if err := s.Client.List(asSystem(ctx), &servers, client.InNamespace(systemNamespace)); err != nil {
		return "", err
	}
	var connections geassv1alpha1.GeassCloudConnectionList
	if err := s.Client.List(asSystem(ctx), &connections, client.InNamespace(systemNamespace)); err != nil {
		return "", err
	}
	var owned []string
	for i := range servers.Items {
		if servers.Items[i].Spec.Org == org && servers.Items[i].Spec.Project == "" {
			owned = append(owned, "the database server "+servers.Items[i].Name)
		}
	}
	for i := range connections.Items {
		if connections.Items[i].Spec.Org == org && connections.Items[i].Spec.Project == "" {
			owned = append(owned, "the cloud connection "+connections.Items[i].Name)
		}
	}
	if len(owned) == 0 {
		return "", nil
	}
	return strings.Join(owned, ", "), nil
}
