package dashboard

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
	"github.com/degoke/geass/pkg/projectbundle"
	"github.com/degoke/geass/pkg/release"
)

// Moving a project to another cluster: POST /api/v1/projects/{p}/export makes a bundle of its definition, and
// POST /api/v1/projects/import makes a new project from one (pkg/projectbundle says what is in it and what is not).
//
// Export only reads. Import creates a new project, and its databases, buckets and services as drafts: nothing in it runs and
// no secret exists until a person has put what the services need in place and deployed them. Both are for an administrator
// and not for a project's own token, and both run for the caller's tenant: what is read or made is what the organization
// the caller works in may see and make.

const (
	// importQueryDryRun and the others are the options of an import sent in the address, when the body is the archive itself.
	importQueryDryRun  = "dryRun"
	importQueryName    = "name"
	importQueryRegion  = "region"
	importQueryKeepID  = "keepProjectId"
	exportQuerySummary = "summary"

	bundleContentType = "application/gzip"
	// importJSONSlack is what a JSON body may hold beyond the base64 of an archive: the options.
	importJSONSlack = 4 << 10
	// maxProjectFolderLength keeps the file name of an export short.
	maxExportFileName = 80
)

// handleProjectExport answers POST /api/v1/projects/{p}/export with the project's bundle, or with its manifest alone when
// ?summary=true.
func (s *Server) handleProjectExport(w http.ResponseWriter, r *http.Request, name string) {
	if !s.mayMoveProjects(w, r) {
		return
	}
	ctx := r.Context()
	var project geassv1alpha1.GeassProject
	if err := s.Client.Get(ctx, client.ObjectKey{Name: name, Namespace: systemNamespace}, &project); err != nil || !project.DeletionTimestamp.IsZero() {
		if err == nil || apierrors.IsNotFound(err) {
			writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: "project was not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: dashboardActionFailed})
		return
	}
	source, err := s.projectBundleSource(ctx, &project)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: dashboardActionFailed})
		return
	}
	bundle, err := projectbundle.Build(source)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: dashboardActionFailed})
		return
	}
	summary := r.URL.Query().Get(exportQuerySummary) == boolTrue
	logger := ctrllog.FromContext(ctx)
	logger.Info("Exported project", "project", project.Name, "by", s.currentSession(r).Username, "summary", summary,
		"services", len(bundle.Services), "databases", len(bundle.Databases), "buckets", len(bundle.Buckets),
		"toReenter", len(bundle.Manifest.ToReenter), "notIncluded", len(bundle.Manifest.NotIncluded))
	if summary {
		bundle.Manifest.Format, bundle.Manifest.Version = projectbundle.Format, projectbundle.Version
		writeJSON(w, http.StatusOK, bundle.Manifest)
		return
	}
	archive, err := bundle.Encode()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: dashboardActionFailed})
		return
	}
	file := bundle.Manifest.Project.DisplayName
	if len(file) > maxExportFileName {
		file = file[:maxExportFileName]
	}
	w.Header().Set("Content-Type", bundleContentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", file+"-bundle.tar.gz"))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", fmt.Sprint(len(archive)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(archive)
}

// mayMoveProjects is whether the caller may export or import: an administrator, signed in or with a workspace token, and not a
// token that belongs to one project.
func (s *Server) mayMoveProjects(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{dashboardLiteralError: "method not allowed"})
		return false
	}
	if !requireMutation(w, r, "/projects") {
		return false
	}
	if s.refuseScopedToken(w, r) {
		return false
	}
	if !s.v1Caller(r).canMutate() {
		writeJSON(w, http.StatusForbidden, map[string]string{dashboardLiteralError: "only an administrator can move a project"})
		return false
	}
	return true
}

// projectBundleSource reads what a project is made of. It reads the names of the keys of a service's Secret and never a value.
func (s *Server) projectBundleSource(ctx context.Context, project *geassv1alpha1.GeassProject) (projectbundle.Source, error) {
	source := projectbundle.Source{Project: *project, GeassRelease: release.Version, Now: time.Now()}
	config, err := s.platformConfig(ctx)
	if err != nil {
		return source, err
	}
	source.RootDomain = platform.RootDomainFromConfig(config)

	var apps geassv1alpha1.GeassAppList
	var databases geassv1alpha1.GeassDatabaseList
	var logical geassv1alpha1.GeassLogicalDatabaseList
	var stores geassv1alpha1.GeassObjectStoreList
	var connections geassv1alpha1.GeassCloudConnectionList
	for _, list := range []client.ObjectList{&apps, &databases, &logical, &stores, &connections} {
		if err := s.Client.List(ctx, list, client.InNamespace(systemNamespace)); err != nil {
			return source, err
		}
	}
	for i := range apps.Items {
		app := &apps.Items[i]
		if app.Spec.Project == project.Name {
			source.Apps = append(source.Apps, projectbundle.SourceApp{App: *app, SecretKeys: sortedKeys(s.appSecretKeys(ctx, app))})
		}
	}
	for i := range databases.Items {
		if databases.Items[i].Spec.Project == project.Name {
			source.Databases = append(source.Databases, databases.Items[i])
		}
	}
	for i := range logical.Items {
		if logical.Items[i].Spec.Project == project.Name {
			source.LogicalDatabases = append(source.LogicalDatabases, logical.Items[i])
		}
	}
	for i := range stores.Items {
		if stores.Items[i].Spec.Project == project.Name {
			source.Buckets = append(source.Buckets, stores.Items[i])
		}
	}
	for i := range connections.Items {
		if connections.Items[i].Spec.Project == project.Name {
			source.Connections = append(source.Connections, projectbundle.SourceConnection{Name: connections.Items[i].Name, Provider: string(connections.Items[i].Spec.Provider)})
		}
	}
	tokens, err := s.listAPITokens(ctx)
	if err != nil {
		return source, err
	}
	for _, record := range tokens {
		if record.Project == project.Name {
			source.Tokens++
		}
	}
	return source, nil
}

// importRequest is what an import is asked to do.
type importRequest struct {
	Bundle        string `json:"bundle"`
	DryRun        bool   `json:"dryRun"`
	Name          string `json:"name"`
	Region        string `json:"region"`
	KeepProjectID bool   `json:"keepProjectId"`
}

func truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case boolTrue, "on", "1", "yes":
		return true
	}
	return false
}

// readImportRequest reads the archive and the options. The archive is the body when the body is not JSON, with the options in
// the address; or it is the base64 of an archive in a JSON body that carries the options.
func readImportRequest(w http.ResponseWriter, r *http.Request) (archive []byte, request importRequest, status int, err error) {
	limits := projectbundle.DefaultLimits
	contentType := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.Contains(contentType, "json") {
		raw, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, limits.MaxArchiveBytes*4/3+importJSONSlack))
		if readErr != nil {
			return nil, request, bodyStatus(readErr), bodyError(readErr)
		}
		if len(bytes.TrimSpace(raw)) > 0 {
			if jsonErr := json.Unmarshal(raw, &request); jsonErr != nil {
				return nil, request, http.StatusBadRequest, errors.New("the request body is not the JSON object it should be")
			}
		}
		if request.Bundle == "" {
			return nil, request, http.StatusBadRequest, errors.New("send the bundle: its bytes as the body (Content-Type: application/gzip), or its base64 as \"bundle\" in a JSON body")
		}
		archive, decodeErr := base64.StdEncoding.DecodeString(request.Bundle)
		if decodeErr != nil {
			return nil, request, http.StatusBadRequest, errors.New("\"bundle\" is not base64")
		}
		return archive, request, http.StatusOK, nil
	}
	query := r.URL.Query()
	request = importRequest{
		DryRun: truthy(query.Get(importQueryDryRun)), Name: query.Get(importQueryName), Region: query.Get(importQueryRegion),
		KeepProjectID: truthy(query.Get(importQueryKeepID)),
	}
	archive, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, limits.MaxArchiveBytes+1))
	if readErr != nil {
		return nil, request, bodyStatus(readErr), bodyError(readErr)
	}
	if len(archive) == 0 {
		return nil, request, http.StatusBadRequest, errors.New("send the bundle: its bytes as the body (Content-Type: application/gzip), or its base64 as \"bundle\" in a JSON body")
	}
	return archive, request, http.StatusOK, nil
}

func bodyStatus(err error) int {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

func bodyError(err error) error {
	if bodyStatus(err) == http.StatusRequestEntityTooLarge {
		return projectbundle.ErrTooLarge
	}
	return errors.New("the request body could not be read")
}

// handleProjectImport answers POST /api/v1/projects/import.
func (s *Server) handleProjectImport(w http.ResponseWriter, r *http.Request) {
	if !s.mayMoveProjects(w, r) {
		return
	}
	ctx := r.Context()
	archive, request, status, err := readImportRequest(w, r)
	if err != nil {
		writeJSON(w, status, map[string]string{dashboardLiteralError: err.Error()})
		return
	}
	bundle, err := projectbundle.Decode(bytes.NewReader(archive), projectbundle.DefaultLimits)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, projectbundle.ErrTooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		writeJSON(w, status, map[string]string{dashboardLiteralError: err.Error()})
		return
	}
	options := projectbundle.Options{Name: request.Name, Region: request.Region, KeepProjectID: request.KeepProjectID}
	target, err := s.importTarget(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: dashboardActionFailed})
		return
	}
	plan := projectbundle.PlanImport(bundle, target, options)
	actor := s.currentSession(r).Username
	logger := ctrllog.FromContext(ctx)

	if request.DryRun {
		logger.Info("Planned import of project", "from", bundle.Manifest.Project.ID, "by", actor, "blockers", len(plan.Blockers), "warnings", len(plan.Warnings))
		writeJSON(w, http.StatusOK, map[string]any{"dryRun": true, "ok": plan.OK(), "plan": plan})
		return
	}
	if !plan.OK() {
		logger.Info("Refused import of project", "from", bundle.Manifest.Project.ID, "by", actor, "blockers", len(plan.Blockers))
		writeJSON(w, http.StatusConflict, map[string]any{
			dashboardLiteralError: "the project cannot be imported: " + plan.Blockers[0].Message, "code": "conflict", "plan": plan,
		})
		return
	}
	result, err := s.importProject(ctx, bundle, &plan, target, actor)
	if err != nil {
		if platform.IsUserError(err) {
			logger.Info("Import of project failed and was rolled back", "from", bundle.Manifest.Project.ID, "by", actor, "reason", err.Error())
			writeJSON(w, http.StatusConflict, map[string]string{dashboardLiteralError: err.Error(), "code": "conflict"})
			return
		}
		logger.Error(err, "Import of project failed and was rolled back", "from", bundle.Manifest.Project.ID, "by", actor)
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: dashboardActionFailed})
		return
	}
	logger.Info("Imported project", "project", result.Project.ID, "from", bundle.Manifest.Project.ID, "by", actor,
		"services", len(bundle.Services), "databases", len(bundle.Databases), "buckets", len(bundle.Buckets), "toReenter", len(result.ToReenter))
	s.notifyChangeIn(result.Project.ID, "projects", result.Project.ID, "import")
	writeJSON(w, http.StatusCreated, result)
}

// importTarget tells the planner what this cluster has, for the caller's tenant.
func (s *Server) importTarget(ctx context.Context) (projectbundle.Target, error) {
	// The id the project would get is also what a hostname is checked against: a project that does not exist, so that the check
	// asks only about the hostname and the organization's domains.
	id, err := s.newProjectID(ctx)
	if err != nil {
		return projectbundle.Target{}, err
	}
	target := projectbundle.Target{CanKeepProjectID: isPlatformContext(ctx), NewProjectID: id, HostProblem: s.importHostProblem(ctx, id)}
	var orgName string
	if tenant, ok := tenantOf(ctx); ok && !tenant.Platform {
		orgName = tenant.Org
	}

	// Names are compared among the projects the caller can see: a project of another organization is not a clash, and is not
	// revealed by one.
	var visible geassv1alpha1.GeassProjectList
	if err := s.Client.List(ctx, &visible, client.InNamespace(systemNamespace)); err != nil {
		return target, err
	}
	names := map[string]bool{}
	for i := range visible.Items {
		if !visible.Items[i].DeletionTimestamp.IsZero() {
			continue
		}
		names[platform.NormalizeProjectName(cmpOr(visible.Items[i].Spec.DisplayName, visible.Items[i].Name))] = true
	}
	target.ProjectNameTaken = func(name string) bool { return names[name] }
	target.ProjectIDTaken = func(id string) bool {
		err := s.Client.Get(asSystem(ctx), client.ObjectKey{Name: id, Namespace: systemNamespace}, &geassv1alpha1.GeassProject{})
		return err == nil || !apierrors.IsNotFound(err)
	}
	if orgName != "" {
		if limit := s.checkProjectLimit(ctx, orgName, ""); limit != nil {
			if !platform.IsUserError(limit) {
				return target, limit
			}
			target.ProjectLimit = limit.Error()
		}
	}

	nodes, err := s.listNodes(ctx)
	if err != nil {
		return target, err
	}
	for i := range nodes {
		if !platform.NodeUsable(&nodes[i]) {
			continue
		}
		target.HealthyNodes++
		placement := platform.NodePlacementOf(&nodes[i])
		node := projectbundle.Node{Region: placement.Region}
		for _, tag := range placement.Tags {
			node.Tags = append(node.Tags, tag.Name)
			if tag.Reserved {
				node.Reserved = append(node.Reserved, tag.Name)
			}
		}
		target.Nodes = append(target.Nodes, node)
	}

	if server, err := s.clusterObjectStore(ctx); err != nil {
		return target, err
	} else if server != nil {
		_, supported := platform.ObjectStoreServerFor(server.Spec.Engine)
		target.ObjectStorage = supported
	}
	readiness, err := s.platformReadiness(ctx)
	if err != nil {
		return target, err
	}
	target.GitHubApp = readiness.HasGitHubApp || readiness.GitHubProvider != ""
	target.Registry = readiness.Config.Spec.Registry.Repository != ""
	if orgName != "" {
		if org, err := s.organization(ctx, orgName); err != nil {
			return target, err
		} else if org != nil && org.Spec.Registry != nil && org.Spec.Registry.Repository != "" {
			target.Registry = true
		}
	}
	if clusterName, err := s.clusterForNewProject(ctx); err == nil {
		var cluster geassv1alpha1.GeassCluster
		if err := s.Client.Get(asSystem(ctx), client.ObjectKey{Name: clusterName, Namespace: systemNamespace}, &cluster); err == nil {
			enabled := cluster.Spec.Addons.Serverless.Enabled
			target.ServerlessAddon = enabled != nil && *enabled
		}
	}
	return target, nil
}

func cmpOr(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// newProjectID makes an id that no project of any organization has. The scoped client would show only the caller's projects, so
// the whole cluster is asked.
func (s *Server) newProjectID(ctx context.Context) (string, error) {
	for range 20 {
		id, err := platform.GenerateProjectID()
		if err != nil {
			return "", err
		}
		err = s.Client.Get(asSystem(ctx), client.ObjectKey{Name: id, Namespace: systemNamespace}, &geassv1alpha1.GeassProject{})
		if apierrors.IsNotFound(err) {
			return id, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", errors.New("could not make an unused project id")
}

// importHostProblem says why a hostname cannot be used by a project the caller makes: it is not a hostname, it is the
// dashboard's, another service has it, or it is outside the domains the caller's organization may use. The same check that
// saving a service makes, before there is a project to save it in.
func (s *Server) importHostProblem(ctx context.Context, project string) func(host string) string {
	var allowed []string
	if tenant, ok := tenantOf(ctx); ok && !tenant.Platform && tenant.Org != "" {
		if org, err := s.organization(ctx, tenant.Org); err == nil && org != nil {
			allowed = org.Spec.AllowedDomains
		}
	}
	return func(host string) string {
		probe := &geassv1alpha1.GeassApp{ObjectMeta: metav1.ObjectMeta{Name: "import-probe"}, Spec: geassv1alpha1.GeassAppSpec{Project: project, Ingress: geassv1alpha1.GeassAppIngressSpec{Host: host}}}
		if err := s.checkAppHosts(ctx, probe, nil); err != nil {
			return err.Error()
		}
		if len(allowed) > 0 && !slices.ContainsFunc(allowed, func(domain string) bool { return hostWithin(host, domain) }) {
			return fmt.Sprintf("is not under a domain your organization may use (%s)", strings.Join(allowed, ", "))
		}
		return ""
	}
}

// importedResource is one object an import made.
type importedResource struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Environment string `json:"environment,omitempty"`
	// From is the object's name in the bundle.
	From string `json:"from,omitempty"`
}

type importedProject struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// importResult is what an import answers with. Its items name the new objects where the plan named the old ones.
type importResult struct {
	DryRun    bool                     `json:"dryRun"`
	Project   importedProject          `json:"project"`
	Created   []importedResource       `json:"created"`
	Warnings  []projectbundle.Finding  `json:"warnings"`
	ToReenter []projectbundle.Item     `json:"toReenter"`
	NotMoved  []projectbundle.Item     `json:"notMoved"`
	Data      []projectbundle.DataNote `json:"data"`
	Next      []string                 `json:"next"`
}

// importProject makes the project, then its databases, its buckets and its services, as drafts. When anything fails what was
// made is removed, last first, so that a refused import leaves nothing behind.
func (s *Server) importProject(ctx context.Context, bundle *projectbundle.Bundle, plan *projectbundle.Plan, target projectbundle.Target, actor string) (result *importResult, err error) {
	cluster, err := s.clusterForNewProject(ctx)
	if err != nil {
		return nil, platform.Userf("%v", err)
	}
	if err := s.Client.Get(asSystem(ctx), client.ObjectKey{Name: cluster, Namespace: systemNamespace}, &geassv1alpha1.GeassCluster{}); err != nil {
		return nil, platform.Userf("the platform default cluster does not exist")
	}
	id := target.NewProjectID
	if plan.Project.ID != "" {
		id = plan.Project.ID
	}
	var server *geassv1alpha1.GeassObjectStore
	if len(bundle.Buckets) > 0 {
		if server, err = s.clusterObjectStore(ctx); err != nil || server == nil {
			return nil, platform.UserError(platform.MessageSetUpObjectStorage)
		}
	}

	project := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: id, Namespace: systemNamespace, Annotations: map[string]string{
			"geass.dev/imported-at": time.Now().UTC().Format(time.RFC3339), "geass.dev/imported-by": actor, "geass.dev/imported-from": bundle.Manifest.Project.ID,
		}},
		Spec: importedProjectSpec(bundle, plan, cluster),
	}
	tenant, scoped := tenantOf(ctx)
	scoped = scoped && !tenant.Platform && tenant.Org != ""
	if scoped {
		if err := s.checkProjectLimit(ctx, tenant.Org, ""); err != nil {
			return nil, err
		}
	}
	var made []client.Object
	defer func() {
		if err == nil {
			return
		}
		// Nothing that was made stays: the services, then the buckets and databases, then the project.
		for _, object := range slices.Backward(made) {
			if deleteErr := s.Client.Delete(context.WithoutCancel(ctx), object); deleteErr != nil && !apierrors.IsNotFound(deleteErr) {
				ctrllog.FromContext(ctx).Error(deleteErr, "Could not remove what a failed import had made", "kind", fmt.Sprintf("%T", object), "name", object.GetName())
			}
		}
		result = nil
	}()
	if err := s.Client.Create(ctx, project); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return nil, platform.Userf("a project with the id %s already exists", id)
		}
		return nil, err
	}
	made = append(made, project)
	if scoped {
		if err := s.checkProjectLimit(ctx, tenant.Org, id); err != nil {
			return nil, err
		}
	}
	if clash, err := s.projectNameRaced(ctx, project); err != nil {
		return nil, err
	} else if clash {
		return nil, platform.Userf("A project called %s already exists. Choose another name", plan.Project.Name)
	}
	if _, err := s.getProjectAllowingCacheLag(ctx, id); err != nil {
		return nil, err
	}

	result = &importResult{Project: importedProject{ID: id, Name: plan.Project.Name}, Created: []importedResource{{Kind: "projects", Name: id, DisplayName: plan.Project.Name}},
		Warnings: plan.Warnings, ToReenter: nil, NotMoved: plan.NotMoved, Data: nil, Next: plan.Next}
	ids := map[string]string{} // kind and name in the bundle -> name here
	connections := map[string]string{}

	for i := range bundle.Databases {
		doc := &bundle.Databases[i]
		display := cmpOr(doc.Metadata.DisplayName, doc.Metadata.Name)
		db := &geassv1alpha1.GeassDatabase{ObjectMeta: metav1.ObjectMeta{Namespace: systemNamespace}, Spec: *doc.Spec.DeepCopy()}
		db.Spec.Project, db.Spec.Placement = id, geassv1alpha1.DatabasePlacementInCluster
		db.Spec.Deploy.Enabled = false
		platform.InitializePendingCreated(&db.ObjectMeta)
		if gap, gapErr := platform.DatabaseSandboxGap(ctx, s.system(), db, s.databasePlacement(ctx, db).NodeSelector()); gapErr != nil {
			return nil, gapErr
		} else if gap != "" {
			return nil, platform.UserError(platform.EngineCannotBeSandboxedMessage(gap, false))
		}
		if err := s.createNamed(ctx, db, display); err != nil {
			return nil, err
		}
		made = append(made, db)
		ids[projectbundle.KindDatabase+"/"+doc.Metadata.Name] = db.Name
		connections[projectbundle.ConnectionSecretName(doc.Metadata.Name)] = projectbundle.ConnectionSecretName(db.Name)
		result.Created = append(result.Created, importedResource{Kind: "databases", Name: db.Name, DisplayName: display, Environment: string(db.Spec.Environment), From: doc.Metadata.Name})
	}
	for i := range bundle.Buckets {
		doc := &bundle.Buckets[i]
		display := cmpOr(doc.Metadata.DisplayName, doc.Metadata.Name)
		store := &geassv1alpha1.GeassObjectStore{ObjectMeta: metav1.ObjectMeta{Namespace: systemNamespace}, Spec: *doc.Spec.DeepCopy()}
		// A bucket in the cluster is on whichever server the cluster has.
		store.Spec.Project, store.Spec.Placement, store.Spec.Engine = id, geassv1alpha1.ObjectStorePlacementInCluster, server.Spec.Engine
		for _, bucket := range store.Spec.Buckets {
			if err := validateBucketNameForStore(store, bucket); err != nil {
				return nil, platform.Userf("bucket %s: %v", display, err)
			}
		}
		store.Spec.Deploy.Enabled = false
		platform.InitializePendingCreated(&store.ObjectMeta)
		if err := s.createNamed(ctx, store, display); err != nil {
			return nil, err
		}
		made = append(made, store)
		ids[projectbundle.KindBucket+"/"+doc.Metadata.Name] = store.Name
		connections[projectbundle.ConnectionSecretName(doc.Metadata.Name)] = projectbundle.ConnectionSecretName(store.Name)
		result.Created = append(result.Created, importedResource{Kind: "object-stores", Name: store.Name, DisplayName: display, Environment: string(store.Spec.Environment), From: doc.Metadata.Name})
	}
	for i := range bundle.Services {
		doc := &bundle.Services[i]
		display := cmpOr(doc.Metadata.DisplayName, doc.Metadata.Name)
		app := &geassv1alpha1.GeassApp{ObjectMeta: metav1.ObjectMeta{Namespace: systemNamespace}, Spec: *doc.Spec.DeepCopy()}
		app.Spec.Project = id
		app.Spec.Deploy.Enabled = false
		for j := range app.Spec.Env {
			if ref := app.Spec.Env[j].ValueFrom; ref != nil && ref.SecretKeyRef != nil {
				ref.SecretKeyRef = ref.SecretKeyRef.DeepCopy()
				renamed, ok := connections[ref.SecretKeyRef.Name]
				if !ok {
					return nil, platform.Userf("service %s: the variable %s refers to a database or bucket that the bundle does not hold", display, app.Spec.Env[j].Name)
				}
				ref.SecretKeyRef.Name = renamed
			}
		}
		if ref := app.Spec.Logs.ArchiveStoreRef; ref != nil {
			renamed, ok := ids[projectbundle.KindBucket+"/"+ref.Name]
			if !ok {
				return nil, platform.Userf("service %s archives its logs to a bucket that the bundle does not hold", display)
			}
			app.Spec.Logs.ArchiveStoreRef = &corev1.LocalObjectReference{Name: renamed}
		}
		// The project's own GitHub connection is made when the person connects the new project.
		if git := app.Spec.Source.Git; git != nil {
			git.ConnectionRef = corev1.LocalObjectReference{}
		}
		app.Spec.Ingress.Host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(app.Spec.Ingress.Host), "."))
		if err := s.checkAppHosts(ctx, app, nil); err != nil {
			return nil, platform.Userf("service %s: %v", display, err)
		}
		initializeAppPendingChanges(app)
		if err := s.createNamed(ctx, app, display); err != nil {
			return nil, err
		}
		made = append(made, app)
		ids[projectbundle.KindService+"/"+doc.Metadata.Name] = app.Name
		result.Created = append(result.Created, importedResource{Kind: "apps", Name: app.Name, DisplayName: display, Environment: string(app.Spec.Environment), From: doc.Metadata.Name})
	}

	result.ToReenter = renameItems(plan.ToReenter, ids, bundle.Manifest.Project.ID, id)
	result.NotMoved = renameItems(plan.NotMoved, ids, bundle.Manifest.Project.ID, id)
	result.Warnings = renameFindings(plan.Warnings, ids)
	result.Data = renameData(plan.Data, ids)
	return result, nil
}

// importedProjectSpec is the new project's spec: what the bundle says of the project, on the cluster the caller's organization
// is bound to. A project made by an import is protected from other projects like any other the dashboard makes.
func importedProjectSpec(bundle *projectbundle.Bundle, plan *projectbundle.Plan, cluster string) geassv1alpha1.GeassProjectSpec {
	source := bundle.Project.Spec
	spec := geassv1alpha1.GeassProjectSpec{
		DisplayName: plan.Project.Name, ClusterRef: corev1.LocalObjectReference{Name: cluster}, Region: plan.Project.Region,
		RequiredTags: slices.Clone(source.RequiredTags), Environments: slices.Clone(source.Environments),
		PREnvironments: source.PREnvironments.DeepCopy(), GitHubReporting: source.GitHubReporting,
		EnvironmentQuota: source.EnvironmentQuota.DeepCopy(), ProjectQuota: source.ProjectQuota.DeepCopy(),
		Network: geassv1alpha1.NewProjectNetwork(),
	}
	if source.Network != nil {
		spec.Network = &geassv1alpha1.GeassProjectNetwork{Isolation: source.Network.Isolation, BlockNodeAddresses: source.Network.BlockNodeAddresses}
	}
	for _, variable := range source.SharedVariables {
		spec.SharedVariables = append(spec.SharedVariables, geassv1alpha1.GeassSharedVariable{Name: variable.Name, Environment: variable.Environment, Value: variable.Value})
	}
	return spec
}

// projectNameRaced says another project the caller sees, made no later than this one, has the same name: two imports at the
// same moment can both pass the check that came first, so whichever finds an earlier one withdraws.
func (s *Server) projectNameRaced(ctx context.Context, mine *geassv1alpha1.GeassProject) (bool, error) {
	var visible geassv1alpha1.GeassProjectList
	if err := s.Client.List(ctx, &visible, client.InNamespace(systemNamespace)); err != nil {
		return false, err
	}
	name := platform.NormalizeProjectName(mine.Spec.DisplayName)
	for i := range visible.Items {
		other := &visible.Items[i]
		if other.Name == mine.Name || !other.DeletionTimestamp.IsZero() || platform.NormalizeProjectName(cmpOr(other.Spec.DisplayName, other.Name)) != name {
			continue
		}
		earlier := other.CreationTimestamp.Before(&mine.CreationTimestamp) || (other.CreationTimestamp.Equal(&mine.CreationTimestamp) && other.Name < mine.Name)
		if earlier {
			return true, nil
		}
	}
	return false, nil
}

func renameItems(items []projectbundle.Item, ids map[string]string, sourceProject, project string) []projectbundle.Item {
	out := make([]projectbundle.Item, 0, len(items))
	for _, item := range items {
		switch item.Kind {
		case projectbundle.KindService, projectbundle.KindDatabase, projectbundle.KindBucket:
			if renamed, ok := ids[item.Kind+"/"+item.ID]; ok {
				item.ID = renamed
			}
		case projectbundle.KindProject:
			if item.ID == sourceProject {
				item.ID = project
			}
		}
		out = append(out, item)
	}
	return out
}

func renameFindings(findings []projectbundle.Finding, ids map[string]string) []projectbundle.Finding {
	out := make([]projectbundle.Finding, 0, len(findings))
	for _, finding := range findings {
		if renamed, ok := ids[finding.Kind+"/"+finding.ID]; ok {
			finding.ID = renamed
		}
		out = append(out, finding)
	}
	return out
}

func renameData(notes []projectbundle.DataNote, ids map[string]string) []projectbundle.DataNote {
	out := make([]projectbundle.DataNote, 0, len(notes))
	for _, note := range notes {
		if renamed, ok := ids[note.Kind+"/"+note.ID]; ok {
			note.ID = renamed
		}
		out = append(out, note)
	}
	return out
}
