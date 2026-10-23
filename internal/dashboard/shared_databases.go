package dashboard

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// The cluster-wide database server is a Postgres or MySQL that projects share: a GeassDatabase with no
// project, living in the system namespace as the cluster's object storage does. Projects take logical databases on it
// (see GeassLogicalDatabase), each with a login that reaches that database and nothing else. Setting it up
// and removing it is the platform's call, so these handlers are for dashboard admins; project tokens are
// refused by default like every route they were not given.

// handleSharedDatabaseCreate serves POST /api/shared-databases/create.
func (s *Server) handleSharedDatabaseCreate(w http.ResponseWriter, r *http.Request) {
	fallback := "/databases"
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	engine := parseDatabaseEngine(r.FormValue("engine"))
	if engine != geassv1alpha1.DatabaseEnginePostgres && engine != geassv1alpha1.DatabaseEngineMySQL {
		redirectFormError(w, r, fallback, "A shared database server can be Postgres or MySQL")
		return
	}
	if formFlag(r, "highAvailability") && platform.EngineIsSingleInstance(engine) {
		redirectFormUserError(w, r, fallback, platform.UserError(platform.SingleInstanceMessage(engine)))
		return
	}
	name := strings.ToLower(strings.TrimSpace(r.FormValue("name")))
	if name == "" {
		name = "cluster-" + strings.ToLower(string(engine))
	}
	if errs := validation.IsDNS1123Label(name); len(errs) > 0 {
		redirectFormError(w, r, fallback, "The name may only contain lowercase letters, numbers and hyphens")
		return
	}
	var existing geassv1alpha1.GeassDatabase
	if err := s.Client.Get(asSystem(r.Context()), client.ObjectKey{Name: name, Namespace: systemNamespace}, &existing); err == nil {
		redirectFormError(w, r, fallback, "A database with that name already exists")
		return
	} else if !apierrors.IsNotFound(err) {
		redirectFormInternalError(w, r, fallback)
		return
	}
	// A server can be made for an organization: only its projects may take databases on it, and its people see it. It runs
	// where the organization requires its work to.
	org, err := s.organization(r.Context(), strings.TrimSpace(r.FormValue("org")))
	if err != nil {
		redirectFormInternalError(w, r, fallback)
		return
	}
	if orgName := strings.TrimSpace(r.FormValue("org")); orgName != "" && org == nil {
		redirectFormError(w, r, fallback, "There is no such organization")
		return
	}
	region := strings.ToLower(strings.TrimSpace(r.FormValue("region")))
	if err := s.validateRegion(r.Context(), region); err != nil {
		redirectFormUserError(w, r, fallback, err)
		return
	}
	effectiveRegion := region
	if effectiveRegion == "" && org != nil {
		effectiveRegion = org.Spec.Region
	}
	if effectiveRegion == "" {
		effectiveRegion, _ = platform.DefaultRegion(r.Context(), s.system(), "")
	}
	tags := splitNames(r.Form["runOnTags"])
	if err := s.validateTags(r.Context(), effectiveRegion, tags); err != nil {
		redirectFormUserError(w, r, fallback, err)
		return
	}
	res, err := resourcesFromForm(r, defaultResourcesForEngine(engine))
	if err != nil {
		redirectFormUserError(w, r, fallback, err)
		return
	}
	db := &geassv1alpha1.GeassDatabase{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: systemNamespace},
		Spec: geassv1alpha1.GeassDatabaseSpec{
			Engine:           engine,
			Placement:        geassv1alpha1.DatabasePlacementInCluster,
			HighAvailability: formFlag(r, "highAvailability"),
			Version:          strings.TrimSpace(r.FormValue("version")),
			Resources:        res,
			ServerRegion:     region,
			RunOn:            geassv1alpha1.GeassWorkloadPlacement{Tags: tags},
			Org:              orgNameOf(org),
			// There is no project to deploy from, so it starts running at once, as the cluster's object storage does.
			Deploy: geassv1alpha1.GeassDeploySpec{Enabled: true},
		},
	}
	if db.Spec.HighAvailability {
		instances := int32(3)
		db.Spec.Instances = &instances
	}
	storageSize, err := databaseStorageForCreate(r.FormValue("storageSize"), db)
	if err != nil {
		redirectFormUserError(w, r, fallback, err)
		return
	}
	db.Spec.StorageSize = storageSize
	est := platform.EstimateWorkload(databaseCreateWorkloadKind(engine), db.Spec.HighAvailability, 0)
	est = platform.EstimateFromResources(est.Label, databaseResources(db), est.Replicas)
	placement := platform.NewPlacement(effectiveRegion, tags)
	if org != nil {
		placement = placement.WithRequired(org.Spec.RequiredTags)
	}
	if s.rejectIfNoCapacityForPlacement(w, r, fallback, est, placement) {
		return
	}
	if err := s.Client.Create(r.Context(), db); err != nil {
		redirectFormInternalError(w, r, fallback)
		return
	}
	s.notifyResourceMutation(db, "create")
	if isJSONRequest(r) {
		writeJSON(w, http.StatusCreated, map[string]string{"database": name})
		return
	}
	redirect(w, r, fallback)
}

// orgNameOf is the name of an organization, or empty for none.
func orgNameOf(org *geassv1alpha1.GeassOrganization) string {
	if org == nil {
		return ""
	}
	return org.Name
}

// formFlag reads a checkbox or switch: "on" from a form, or anything strconv reads as true from the API.
func formFlag(r *http.Request, key string) bool {
	value := strings.ToLower(strings.TrimSpace(r.FormValue(key)))
	if value == "on" {
		return true
	}
	on, err := strconv.ParseBool(value)
	return err == nil && on
}

// logicalDatabasesOn lists the logical databases that live on a server.
func (s *Server) logicalDatabasesOn(ctx context.Context, server string) ([]geassv1alpha1.GeassLogicalDatabase, error) {
	// Every logical database on the server is counted, whoever's it is: a server is not removed from under data that is
	// not the remover's to see.
	var logical geassv1alpha1.GeassLogicalDatabaseList
	if err := s.Client.List(asSystem(ctx), &logical, client.InNamespace(systemNamespace)); err != nil {
		return nil, err
	}
	var on []geassv1alpha1.GeassLogicalDatabase
	for i := range logical.Items {
		if logical.Items[i].Spec.ServerRef == server {
			on = append(on, logical.Items[i])
		}
	}
	return on, nil
}

// sharedServerInUse is the reason a shared server cannot be removed yet: projects still have databases on
// it, and removing it would destroy them all. Empty when it can go.
func (s *Server) sharedServerInUse(ctx context.Context, db *geassv1alpha1.GeassDatabase) (string, error) {
	if !platform.IsSharedDatabase(db) {
		return "", nil
	}
	on, err := s.logicalDatabasesOn(ctx, db.Name)
	if err != nil || len(on) == 0 {
		return "", err
	}
	projects := map[string]bool{}
	for i := range on {
		projects[on[i].Spec.Project] = true
	}
	return fmt.Sprintf("%d %s in %d %s still use this server. Delete them first, because removing the server would destroy their data",
		len(on), plural(len(on), "database", "databases"), len(projects), plural(len(projects), "project", "projects")), nil
}

func plural(count int, one, many string) string {
	if count == 1 {
		return one
	}
	return many
}

// validateLogicalDatabaseServer checks a project's request for a database on a server before it is created,
// so a mistake is an error in the dialog and not a resource that never becomes ready. It reports the user
// message, or empty when the request is fine.
func (s *Server) validateLogicalDatabaseServer(ctx context.Context, project, environment, serverName string) (string, error) {
	var server geassv1alpha1.GeassDatabase
	if err := s.Client.Get(ctx, client.ObjectKey{Name: serverName, Namespace: systemNamespace}, &server); err != nil {
		if apierrors.IsNotFound(err) {
			return "That database server does not exist", nil
		}
		return "", err
	}
	if server.Spec.Engine != geassv1alpha1.DatabaseEnginePostgres && server.Spec.Engine != geassv1alpha1.DatabaseEngineMySQL && server.Spec.Engine != "" {
		return "A database can only be created on a Postgres or MySQL server", nil
	}
	if !platform.IsSharedDatabase(&server) {
		if server.Spec.Project != project || string(server.Spec.Environment) != environment {
			return "That server belongs to another project or environment", nil
		}
		return "", nil
	}
	// A server an organization owns is for that organization's projects, and while organizations are on the platform's is
	// for the projects of none (platform.SharedServerAllows).
	if tenancy := s.tenancyOn(ctx); server.Spec.Org != "" || tenancy {
		var owner geassv1alpha1.GeassProject
		if err := s.Client.Get(ctx, client.ObjectKey{Name: project, Namespace: systemNamespace}, &owner); err != nil {
			if apierrors.IsNotFound(err) {
				return "That project does not exist", nil
			}
			return "", err
		}
		switch {
		case platform.SharedServerAllows(tenancy, server.Spec.Org, owner.Spec.Org):
		case !isPlatformContext(ctx):
			return "That database server does not exist", nil
		case server.Spec.Org != "":
			return "That server belongs to an organization the project is not in", nil
		default:
			// Only the platform sees its own server, so only the platform is told this. No server is made for the
			// organization here: setting one up is the platform's decision, and what it costs is the platform's to weigh.
			return fmt.Sprintf("That server is the platform's and the project is in the organization %s, but with organizations on %s. "+
				"Choose a server of %s, or set one up for it with POST /api/shared-databases/create and org=%s",
				owner.Spec.Org, platform.MessageOneOrganizationPerServer, owner.Spec.Org, owner.Spec.Org), nil
		}
	}
	serverRegion := s.databasePlacement(ctx, &server).Region
	if serverRegion == "" {
		serverRegion = server.Spec.ServerRegion
	}
	projectRegion, err := platform.ProjectRegion(ctx, s.system(), project)
	if err != nil {
		return "", err
	}
	if serverRegion != "" && projectRegion != "" && serverRegion != projectRegion {
		return fmt.Sprintf("This server runs in the %s region, but the project runs in the %s region. Every query would cross regions", serverRegion, projectRegion), nil
	}
	return "", nil
}
