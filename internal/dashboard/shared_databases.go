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
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: name, Namespace: systemNamespace}, &existing); err == nil {
		redirectFormError(w, r, fallback, "A database with that name already exists")
		return
	} else if !apierrors.IsNotFound(err) {
		redirectFormInternalError(w, r, fallback)
		return
	}
	region := strings.ToLower(strings.TrimSpace(r.FormValue("region")))
	if err := s.validateRegion(r.Context(), region); err != nil {
		redirectFormUserError(w, r, fallback, err)
		return
	}
	effectiveRegion := region
	if effectiveRegion == "" {
		effectiveRegion, _ = platform.DefaultRegion(r.Context(), s.Client, "")
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
	if s.rejectIfNoCapacityForPlacement(w, r, fallback, est, platform.NewPlacement(effectiveRegion, tags)) {
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
	var logical geassv1alpha1.GeassLogicalDatabaseList
	if err := s.Client.List(ctx, &logical, client.InNamespace(systemNamespace)); err != nil {
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
	serverRegion := s.databasePlacement(ctx, &server).Region
	if serverRegion == "" {
		serverRegion = server.Spec.ServerRegion
	}
	projectRegion, err := platform.ProjectRegion(ctx, s.Client, project)
	if err != nil {
		return "", err
	}
	if serverRegion != "" && projectRegion != "" && serverRegion != projectRegion {
		return fmt.Sprintf("This server runs in the %s region, but the project runs in the %s region. Every query would cross regions", serverRegion, projectRegion), nil
	}
	return "", nil
}
