package dashboard

import (
	"cmp"
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/apitokens"
	"github.com/degoke/geass/pkg/platform"
	"github.com/degoke/geass/pkg/release"
)

// What v1 offers beyond the dashboard's own routes: resources one at a time and as lists, in a shape meant for
// programs. The dashboard loads everything in one blob and works the rest out in the browser, which a command
// line or an agent should not have to do. Everything here is read from the same cache the blob is.

// v1Resource is the summary every kind of resource is listed as, so a program can treat them alike.
type v1Resource struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName,omitempty"`
	Project     string `json:"project,omitempty"`
	Environment string `json:"environment,omitempty"`
	// State is one of ready, pending (still being set up or rolled out), failed, draft (saved and not yet
	// deployed) or deleting.
	State          string         `json:"state"`
	Message        string         `json:"message,omitempty"`
	URL            string         `json:"url,omitempty"`
	PendingChanges int            `json:"pendingChanges,omitempty"`
	CreatedAt      time.Time      `json:"createdAt"`
	Details        map[string]any `json:"details,omitempty"`
}

// v1Caller is who is asking: a signed-in browser session, or an API token and the limits it carries.
type v1Caller struct {
	session *dashboardSessionInfo
	token   *apitokens.Record
}

func (s *Server) v1Caller(r *http.Request) v1Caller {
	return v1Caller{session: s.currentSession(r), token: tokenFromRequest(r)}
}

// scope is the project and environment a project token is limited to. ok is false for everyone else.
func (c v1Caller) scope() (project, environment string, ok bool) {
	if c.token != nil && c.token.Scope == apitokens.ScopeProject {
		return c.token.Project, c.token.Environment, true
	}
	return "", "", false
}

func (c v1Caller) canMutate() bool { return c.session != nil && c.session.CanMutate }

// canReadSensitive is whether the caller may see what is kept from viewers: logs, the names of variables.
func (c v1Caller) canReadSensitive() bool {
	return c.session != nil && (c.session.CanMutate || c.session.CanReadSensitive)
}

func (c v1Caller) workspaceAdmin() bool {
	if _, _, scoped := c.scope(); scoped {
		return false
	}
	return c.canMutate()
}

func writeV1Error(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{dashboardLiteralError: message, "code": v1ErrorCode(status)})
}

// v1Router serves the endpoints that exist only in v1. A GET it does not know is passed on to the dashboard's
// route of the same name (see v1ReadPath), so metrics, variables and the rest need not be written twice.
func (s *Server) v1Router() *http.ServeMux {
	s.v1Once.Do(func() {
		mux := http.NewServeMux()
		mux.HandleFunc("GET /api/v1/version", s.v1Version)
		mux.HandleFunc("GET /api/v1/me", s.v1Me)
		mux.HandleFunc("GET /api/v1/usage/hours", s.v1UsageHours)
		mux.HandleFunc("GET /api/v1/mail/usage", s.v1MailUsage)
		mux.HandleFunc("GET /api/v1/nodes", s.v1Nodes)
		mux.HandleFunc("GET /api/v1/compute/providers", s.v1ComputeProviders)
		mux.HandleFunc("GET /api/v1/compute/sizes", s.v1ComputeSizes)
		mux.HandleFunc("GET /api/v1/connections", s.v1Connections)
		mux.HandleFunc("GET /api/v1/connections/{name}", s.v1ConnectionGet)
		mux.HandleFunc("GET /api/v1/pools", s.v1Pools)
		mux.HandleFunc("GET /api/v1/pools/{name}", s.v1PoolGet)
		mux.HandleFunc("GET /api/v1/github/connections", s.v1GitHubConnections)
		mux.HandleFunc("GET /api/v1/github/connections/{name}", s.v1GitHubConnection)
		mux.HandleFunc("GET /api/v1/github/connections/{name}/installations", s.v1GitHubInstallations)
		mux.HandleFunc("GET /api/v1/openapi.json", s.v1OpenAPIHandler)
		mux.HandleFunc("GET /api/v1/auth/device/{code}", s.handleDeviceLookup)
		for _, kind := range []string{kindProjects, kindApps, kindDatabases, kindObjectStores, kindLogicalDatabases} {
			mux.HandleFunc("GET /api/v1/"+kind, s.v1List(kind))
			mux.HandleFunc("GET /api/v1/"+kind+"/{"+pathNameParam+"}", s.v1Get(kind))
		}
		mux.HandleFunc("GET /api/v1/apps/{name}/status", s.v1AppStatus)
		mux.HandleFunc("GET /api/v1/apps/{name}/deployments", s.v1AppDeployments)
		mux.HandleFunc("GET /api/v1/apps/{name}/logs", s.v1AppLogs)
		mux.HandleFunc("GET /api/v1/apps/{name}/logs/stream", s.v1AppLogStream)
		mux.HandleFunc("GET /api/v1/databases/{name}/connection", s.v1Connection(kindDatabases))
		mux.HandleFunc("GET /api/v1/logical-databases/{name}/connection", s.v1Connection(kindLogicalDatabases))
		s.v1Mux = mux
	})
	return s.v1Mux
}

// v1ReadPath is the dashboard route that serves a v1 read that v1 itself does not: the same path without the
// version, so GET /api/v1/apps/web/metrics is GET /api/apps/web/metrics.
func v1ReadPath(path string) string { return "/api/" + strings.TrimPrefix(path, v1Prefix) }

// v1Version says which API this is and which Geass release serves it. The release is what tells a person (and the command
// line) that the two are out of step; api is the version of the API itself and changes far less often.
func (s *Server) v1Version(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"api": "v1", pathNameParam: "geass", "release": release.Version})
}

func (s *Server) v1Me(w http.ResponseWriter, r *http.Request) {
	caller := s.v1Caller(r)
	if caller.session == nil {
		writeV1Error(w, http.StatusUnauthorized, "authentication required")
		return
	}
	me := map[string]any{
		"username": caller.session.Username, "role": caller.session.Role,
		"canMutate": caller.canMutate(), "canReadSensitive": caller.canReadSensitive(),
		"authentication": "session",
	}
	// Where the caller works, once organizations are on: the organization and workspace, by name, or the platform.
	if s.tenancyOn(r.Context()) {
		if caller.session.PlatformAdmin && caller.session.Org == "" {
			me["platformAdmin"] = true
		}
		if caller.session.Org != "" {
			me["org"] = caller.session.Org
			if org, err := s.organization(r.Context(), caller.session.Org); err == nil && org != nil {
				me["orgName"] = org.Spec.DisplayName
				for _, workspace := range org.Spec.Workspaces {
					if workspace.ID == caller.session.Workspace {
						me["workspaceName"] = workspace.DisplayName
					}
				}
			}
			if caller.session.Workspace != "" {
				me["workspace"] = caller.session.Workspace
			}
		}
	}
	if caller.token != nil {
		me["authentication"], me["scope"] = "token", caller.token.Scope
		if project, environment, ok := caller.scope(); ok {
			me["project"], me["environment"] = project, environment
		}
		if caller.token.ExpiresAt != nil {
			me["expiresAt"] = caller.token.ExpiresAt
		}
	}
	if bearer := oidcBearerFromRequest(r); bearer != nil {
		me["authentication"], me["expiresAt"] = "oidc", bearer.Expires
	}
	writeJSON(w, http.StatusOK, me)
}

// v1Data loads what the caller may see, redacted the way the dashboard redacts it for them.
func (s *Server) v1Data(r *http.Request) (dashboardBootstrap, error) {
	caller := s.v1Caller(r)
	viewer := !caller.canMutate()
	data, err := s.loadDashboardBootstrap(r.Context(), viewer)
	if err != nil {
		return data, err
	}
	if viewer {
		sanitizeDashboardForViewer(&data)
	}
	// Variable values never leave in a read, whoever asks: the keys say what is set.
	for i := range data.Apps.Items {
		spec := &data.Apps.Items[i].Spec
		for key := range spec.SecretData {
			spec.SecretData[key] = ""
		}
	}
	return data, nil
}

// inScope reports whether a resource is one the caller may see: anything for most callers, and for a project
// token only what is in its project and environment.
func (c v1Caller) inScope(project, environment string) bool {
	scopeProject, scopeEnvironment, scoped := c.scope()
	if !scoped {
		return true
	}
	return project == scopeProject && (environment == "" || environment == scopeEnvironment)
}

func (s *Server) v1List(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, err := s.v1Data(r)
		if err != nil {
			writeV1Error(w, http.StatusInternalServerError, "could not load the resources")
			return
		}
		caller := s.v1Caller(r)
		project, environment := r.URL.Query().Get(formProject), r.URL.Query().Get(formEnvironment)
		items := []v1Resource{}
		for _, view := range v1Views(kind, &data) {
			if caller.inScope(view.Project, view.Environment) && (project == "" || view.Project == project) &&
				(environment == "" || view.Environment == environment) {
				items = append(items, view)
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{jsonItems: items})
	}
}

func (s *Server) v1Get(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue(pathNameParam)
		data, err := s.v1Data(r)
		if err != nil {
			writeV1Error(w, http.StatusInternalServerError, "could not load the resource")
			return
		}
		caller := s.v1Caller(r)
		views := v1Views(kind, &data)
		index := slices.IndexFunc(views, func(view v1Resource) bool { return view.Name == name })
		// The same answer for a resource that is missing and one the caller may not see, so names do not leak.
		if index < 0 || !caller.inScope(views[index].Project, views[index].Environment) {
			writeV1Error(w, http.StatusNotFound, strings.TrimSuffix(strings.ReplaceAll(kind, "-", " "), "s")+" not found")
			return
		}
		spec, status := v1SpecAndStatus(kind, name, &data)
		writeJSON(w, http.StatusOK, map[string]any{"resource": views[index], "spec": spec, "status": status})
	}
}

// v1Views summarises every resource of a kind.
func v1Views(kind string, data *dashboardBootstrap) []v1Resource {
	var views []v1Resource
	switch kind {
	case kindProjects:
		for i := range data.Projects.Items {
			views = append(views, projectView(&data.Projects.Items[i]))
		}
	case kindApps:
		for i := range data.Apps.Items {
			views = append(views, appView(&data.Apps.Items[i]))
		}
	case kindDatabases:
		for i := range data.Databases.Items {
			views = append(views, databaseView(&data.Databases.Items[i]))
		}
	case kindObjectStores:
		for i := range data.ObjectStores.Items {
			views = append(views, objectStoreView(&data.ObjectStores.Items[i]))
		}
	case kindLogicalDatabases:
		for i := range data.LogicalDatabases.Items {
			views = append(views, logicalDatabaseView(&data.LogicalDatabases.Items[i]))
		}
	}
	return views
}

func v1SpecAndStatus(kind, name string, data *dashboardBootstrap) (spec, status any) {
	switch kind {
	case kindProjects:
		for _, item := range data.Projects.Items {
			if item.Name == name {
				return item.Spec, item.Status
			}
		}
	case kindApps:
		for _, item := range data.Apps.Items {
			if item.Name == name {
				return item.Spec, item.Status
			}
		}
	case kindDatabases:
		for _, item := range data.Databases.Items {
			if item.Name == name {
				return item.Spec, item.Status
			}
		}
	case kindObjectStores:
		for _, item := range data.ObjectStores.Items {
			if item.Name == name {
				return item.Spec, item.Status
			}
		}
	case kindLogicalDatabases:
		for _, item := range data.LogicalDatabases.Items {
			if item.Name == name {
				return item.Spec, item.Status
			}
		}
	}
	return nil, nil
}

// v1State is how a resource is doing, in a word, and the message that goes with it when something is wrong.
func v1State(conditions []metav1.Condition, deleting, draft bool) (state, message string) {
	if deleting {
		return stateDeleting, ""
	}
	if draft {
		return stateDraft, "Saved and not deployed yet"
	}
	for _, condition := range conditions {
		if condition.Type != platform.ConditionReady {
			continue
		}
		switch {
		case condition.Status == metav1.ConditionTrue:
			return stateReady, ""
		case condition.Reason == platform.ConditionReasonProvisionFailed || condition.Reason == "RolloutFailed":
			return stateFailed, condition.Message
		default:
			return statePending, condition.Message
		}
	}
	return statePending, ""
}

func projectView(project *geassv1alpha1.GeassProject) v1Resource {
	state, message := v1State(project.Status.Conditions, !project.DeletionTimestamp.IsZero(), false)
	details := map[string]any{"environments": project.Spec.Environments}
	if project.Spec.Region != "" {
		details["region"] = project.Spec.Region
	}
	if len(project.Spec.RequiredTags) > 0 {
		details["requiredTags"] = project.Spec.RequiredTags
	}
	return v1Resource{
		Kind: "project", Name: project.Name, DisplayName: firstNonEmptyString(project.Spec.DisplayName, project.Name),
		State: state, Message: message, CreatedAt: project.CreationTimestamp.Time, Details: details,
	}
}

func appView(app *geassv1alpha1.GeassApp) v1Resource {
	state, message := v1State(app.Status.Conditions, !app.DeletionTimestamp.IsZero(), !app.Spec.Deploy.Enabled)
	details := map[string]any{"port": app.Spec.Port}
	switch {
	case app.Spec.Source.Git != nil:
		details["source"] = sourceGit
		details["repository"] = app.Spec.Source.Git.Repository
		details["branch"] = app.Spec.Source.Git.Branch
	case app.Spec.Source.Function != nil:
		details["source"] = "function"
		details["runtime"] = app.Spec.Source.Function.Runtime
		details["mode"] = platform.FunctionMode(app.Spec.Source.Function)
		if triggers := app.Spec.Source.Function.Triggers; len(triggers) > 0 {
			details["triggers"] = triggers
		}
	case app.Spec.Source.Image != nil:
		details["source"] = "image"
		details["image"] = app.Spec.Source.Image.Image
	}
	if app.Spec.Replicas != nil {
		details["replicas"] = *app.Spec.Replicas
	}
	if sleep := app.Spec.ScaleToZero; sleep != nil && sleep.Enabled {
		details["scaleToZero"] = map[string]any{"idleSeconds": cmp.Or(sleep.IdleSeconds, 300), "maxReplicas": cmp.Or(sleep.MaxReplicas, 3)}
		// Whether it can: the operator says why not when it cannot, such as the add-on not being installed.
		if cond := apimeta.FindStatusCondition(app.Status.Conditions, "ScaleToZero"); cond != nil {
			details["scaleToZero"].(map[string]any)["active"] = cond.Status == metav1.ConditionTrue
			details["scaleToZero"].(map[string]any)["message"] = cond.Message
		}
	}
	return v1Resource{
		Kind: "app", Name: app.Name, DisplayName: platform.ResourceDisplayName(app.ObjectMeta),
		Project: app.Spec.Project, Environment: string(app.Spec.Environment),
		State: state, Message: message, URL: app.Status.URL, PendingChanges: appPendingCount(app),
		CreatedAt: app.CreationTimestamp.Time, Details: details,
	}
}

func appPendingCount(app *geassv1alpha1.GeassApp) int {
	count, err := strconv.Atoi(app.Annotations[platform.AppPendingUpdatesAnnotation])
	if err != nil || count < 0 {
		return 0
	}
	return count
}

func databaseView(db *geassv1alpha1.GeassDatabase) v1Resource {
	state, message := v1State(db.Status.Conditions, !db.DeletionTimestamp.IsZero(), !db.Spec.Deploy.Enabled)
	details := map[string]any{"engine": string(db.Spec.Engine), "highAvailability": platform.DatabaseWantsHA(db)}
	if db.Spec.Placement != "" {
		details["placement"] = string(db.Spec.Placement)
	}
	if version := platform.DatabaseVersion(db); version != "" {
		details["version"] = version
	}
	return v1Resource{
		Kind: "database", Name: db.Name, DisplayName: platform.ResourceDisplayName(db.ObjectMeta),
		Project: db.Spec.Project, Environment: string(db.Spec.Environment),
		State: state, Message: message, CreatedAt: db.CreationTimestamp.Time, Details: details,
	}
}

func objectStoreView(store *geassv1alpha1.GeassObjectStore) v1Resource {
	state, message := v1State(store.Status.Conditions, !store.DeletionTimestamp.IsZero(), !store.Spec.Deploy.Enabled)
	details := map[string]any{}
	if store.Spec.Placement != "" {
		details["placement"] = string(store.Spec.Placement)
	}
	if len(store.Spec.Buckets) > 0 {
		details["buckets"] = store.Spec.Buckets
	}
	return v1Resource{
		Kind: "object-store", Name: store.Name, DisplayName: platform.ResourceDisplayName(store.ObjectMeta),
		Project: store.Spec.Project, Environment: string(store.Spec.Environment),
		State: state, Message: message, CreatedAt: store.CreationTimestamp.Time, Details: details,
	}
}

func logicalDatabaseView(logical *geassv1alpha1.GeassLogicalDatabase) v1Resource {
	state, message := v1State(logical.Status.Conditions, !logical.DeletionTimestamp.IsZero(), false)
	return v1Resource{
		Kind: "logical-database", Name: logical.Name, DisplayName: platform.ResourceDisplayName(logical.ObjectMeta),
		Project: logical.Spec.Project, Environment: string(logical.Spec.Environment),
		State: state, Message: message, CreatedAt: logical.CreationTimestamp.Time,
		Details: map[string]any{"server": logical.Spec.ServerRef, "database": logical.Spec.DatabaseName},
	}
}

// ---- a service's status, in one read ----

// v1AppStatus is what a deploy leaves behind, gathered so a caller can wait on it without reading five
// resources: whether the service is up, what is rolling out, how the build went, and why it failed if it did.
func (s *Server) v1AppStatus(w http.ResponseWriter, r *http.Request) {
	app, ok := s.v1AppForCaller(w, r)
	if !ok {
		return
	}
	view := appView(app)
	status := map[string]any{
		pathNameParam: app.Name, formProject: app.Spec.Project, formEnvironment: string(app.Spec.Environment),
		"state": view.State, "message": view.Message, "url": app.Status.URL, "pendingChanges": view.PendingChanges,
		"ready": view.State == stateReady,
	}
	// Whether a service that asked to sleep when idle can is the operator's to say, and a caller that has just asked
	// reads it here, in the one read it already makes, instead of loading every resource for it.
	if sleep, ok := view.Details["scaleToZero"]; ok {
		status["scaleToZero"] = sleep
	}
	if namespace, err := resourceNamespaceForApp(*app); err == nil {
		var deploy appsv1.Deployment
		if err := s.Client.Get(asSystem(r.Context()), client.ObjectKey{Name: app.Name, Namespace: namespace}, &deploy); err == nil {
			desired := int32(1)
			if deploy.Spec.Replicas != nil {
				desired = *deploy.Spec.Replicas
			}
			status["replicas"] = map[string]int32{"desired": desired, "ready": deploy.Status.ReadyReplicas, jsonKeyAvailable: deploy.Status.AvailableReplicas}
			// A service that sleeps when idle and has no copies is waiting for its next request, not down.
			if _, sleeps := view.Details["scaleToZero"]; sleeps && desired == 0 {
				status["sleeping"] = true
			}
		}
	}
	if build, err := s.currentBuild(r.Context(), app); err == nil && build != nil {
		status["build"] = map[string]any{
			pathNameParam: build.Name, "phase": string(build.Status.Phase), "failureReason": build.Status.FailureReason,
			"sourceRevision": build.Status.SourceRevision, "ciState": build.Status.CIState,
			"startedAt": build.Status.StartedAt, "completedAt": build.Status.CompletedAt,
		}
	}
	if attempt := latestProvisionAttempt(app.Status.Provision); attempt != nil {
		status["lastAttempt"] = map[string]any{
			"id": attempt.ID, "phase": string(attempt.Phase), "startedAt": attempt.StartedAt,
			"completedAt": attempt.CompletedAt, "summary": attempt.Summary, "errors": attempt.Errors,
		}
	}
	writeJSON(w, http.StatusOK, status)
}

func latestProvisionAttempt(provision geassv1alpha1.GeassProvisionStatus) *geassv1alpha1.GeassProvisionAttempt {
	if len(provision.ProvisionAttempts) == 0 {
		return nil
	}
	return &provision.ProvisionAttempts[0]
}

func (s *Server) v1AppDeployments(w http.ResponseWriter, r *http.Request) {
	app, ok := s.v1AppForCaller(w, r)
	if !ok {
		return
	}
	var history geassv1alpha1.GeassDeploymentList
	if err := s.Client.List(r.Context(), &history, client.InNamespace(systemNamespace), client.MatchingLabels{platform.LabelApp: app.Name}); err != nil {
		writeV1Error(w, http.StatusInternalServerError, "could not load the deployments")
		return
	}
	slices.SortFunc(history.Items, func(a, b geassv1alpha1.GeassDeployment) int {
		return b.CreationTimestamp.Compare(a.CreationTimestamp.Time)
	})
	items := make([]map[string]any, 0, len(history.Items))
	for _, deployment := range history.Items {
		items = append(items, map[string]any{
			"id": deployment.Name, "phase": deployment.Status.Phase, "image": deployment.Spec.Image,
			"commit": deployment.Spec.Commit, "trigger": deployment.Spec.Trigger, "actor": deployment.Spec.Actor,
			"changeTitle": deployment.Spec.ChangeTitle, "createdAt": deployment.CreationTimestamp.Time,
			"startedAt": deployment.Status.StartedAt, "completedAt": deployment.Status.CompletedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{jsonItems: items})
}

// v1AppForCaller finds the service a request is about and answers the request itself when the caller may
// not have it. A project token reaches only services in its own project and environment, and gets the same
// answer for one outside them as for one that does not exist.
func (s *Server) v1AppForCaller(w http.ResponseWriter, r *http.Request) (*geassv1alpha1.GeassApp, bool) {
	app, err := s.getApp(r, r.PathValue(pathNameParam))
	if err != nil || !s.v1Caller(r).inScope(app.Spec.Project, string(app.Spec.Environment)) {
		writeV1Error(w, http.StatusNotFound, "app not found")
		return nil, false
	}
	return app, true
}

// v1AppLogRequest checks that the caller may read a service's logs and returns the project's log request
// narrowed to that service. The environment and the service are set here, over anything the caller sent, so
// a project token cannot read past its own service.
func (s *Server) v1AppLogRequest(w http.ResponseWriter, r *http.Request) (*geassv1alpha1.GeassApp, *http.Request, bool) {
	if !s.v1Caller(r).canReadSensitive() {
		writeV1Error(w, http.StatusForbidden, "viewer role cannot access this")
		return nil, nil, false
	}
	app, ok := s.v1AppForCaller(w, r)
	if !ok {
		return nil, nil, false
	}
	narrowed := r.Clone(r.Context())
	query := narrowed.URL.Query()
	query.Set(formEnvironment, string(app.Spec.Environment))
	query.Set("service", app.Name)
	narrowedURL := *r.URL
	narrowedURL.RawQuery = query.Encode()
	narrowed.URL = &narrowedURL
	return app, narrowed, true
}

// v1AppLogs reads a service's recent logs. It is the project's logs narrowed to one service, so it answers the
// way the project does (and the stream below does): {"lines": [...]} with one entry per line, carrying its
// time, pod, container, text and level, for the tail, since, q and level filters to act on. The tail is the
// newest lines across the service's copies, and the answer names no service but this one. The dashboard's
// own route of the same name answers a block of text, which a program should not have to take apart.
func (s *Server) v1AppLogs(w http.ResponseWriter, r *http.Request) {
	app, narrowed, ok := s.v1AppLogRequest(w, r)
	if !ok {
		return
	}
	s.writeProjectLogs(w, narrowed, app.Spec.Project, app.Name)
}

// v1AppLogStream follows a service's logs as server-sent events: recent lines first, then new ones as they
// are written, across restarts and new pods. It is the project's stream narrowed to one service.
func (s *Server) v1AppLogStream(w http.ResponseWriter, r *http.Request) {
	app, narrowed, ok := s.v1AppLogRequest(w, r)
	if !ok {
		return
	}
	s.handleAPIProjectLogStream(w, narrowed, app.Spec.Project)
}

// ---- connection details ----

// v1Connection says how to reach a database. The host, port, database and user are shown to anyone who may
// manage resources; the password and the connection string only when asked for with ?reveal=true, only to a
// workspace admin, and the request is logged. A project token and an observer never see them.
func (s *Server) v1Connection(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		caller := s.v1Caller(r)
		if !caller.workspaceAdmin() {
			writeV1Error(w, http.StatusForbidden, "only a workspace admin can read connection details")
			return
		}
		namespace, secretName, found := s.v1ConnectionSecret(r.Context(), kind, r.PathValue(pathNameParam))
		if !found {
			writeV1Error(w, http.StatusNotFound, "no connection details yet: the resource has not finished setting up")
			return
		}
		var secret corev1.Secret
		if err := s.Client.Get(asSystem(r.Context()), client.ObjectKey{Name: secretName, Namespace: namespace}, &secret); err != nil {
			if apierrors.IsNotFound(err) {
				writeV1Error(w, http.StatusNotFound, "no connection details yet: the resource has not finished setting up")
				return
			}
			writeV1Error(w, http.StatusInternalServerError, "could not read the connection details")
			return
		}
		out := map[string]any{"keys": sortedSecretKeys(&secret)}
		for _, key := range []string{platform.ConnectionKeyHost, platform.ConnectionKeyPort, platform.ConnectionKeyDatabase, platform.ConnectionKeyUsername} {
			if value, ok := secret.Data[key]; ok {
				out[key] = string(value)
			}
		}
		if reveal, _ := strconv.ParseBool(r.URL.Query().Get("reveal")); reveal {
			ctrllog.FromContext(r.Context()).Info("Connection details revealed", "kind", kind, "name", r.PathValue(pathNameParam), "by", caller.session.Username)
			for _, key := range []string{platform.ConnectionKeyPassword, platform.ConnectionKeyURI} {
				if value, ok := secret.Data[key]; ok {
					out[key] = string(value)
				}
			}
			out["revealed"] = true
		}
		writeJSON(w, http.StatusOK, out)
	}
}

func (s *Server) v1ConnectionSecret(ctx context.Context, kind, name string) (namespace, secret string, found bool) {
	switch kind {
	case kindDatabases:
		var db geassv1alpha1.GeassDatabase
		if err := s.Client.Get(ctx, client.ObjectKey{Name: name, Namespace: systemNamespace}, &db); err != nil {
			return "", "", false
		}
		return db.Status.TargetNamespace, db.Status.ConnectionSecret, db.Status.TargetNamespace != "" && db.Status.ConnectionSecret != ""
	default:
		var logical geassv1alpha1.GeassLogicalDatabase
		if err := s.Client.Get(ctx, client.ObjectKey{Name: name, Namespace: systemNamespace}, &logical); err != nil {
			return "", "", false
		}
		ns, err := platform.ProjectNamespace(logical.Spec.Project, string(logical.Spec.Environment))
		return ns, logical.Status.ConnectionSecret, err == nil && logical.Status.ConnectionSecret != ""
	}
}

func sortedSecretKeys(secret *corev1.Secret) []string {
	keys := make([]string, 0, len(secret.Data))
	for key := range secret.Data {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}

// ---- nodes ----

type v1Node struct {
	Name         string      `json:"name"`
	Ready        bool        `json:"ready"`
	Schedulable  bool        `json:"schedulable"`
	ControlPlane bool        `json:"controlPlane"`
	Region       string      `json:"region,omitempty"`
	Zone         string      `json:"zone,omitempty"`
	Tags         []v1NodeTag `json:"tags,omitempty"`
	CPU          string      `json:"cpu,omitempty"`
	Memory       string      `json:"memory,omitempty"`
	// Pool is the pool the node belongs to, and Ephemeral says it must not hold data (databases and volumes stay off
	// it, and it may be removed when the load goes).
	Pool      string `json:"pool,omitempty"`
	Ephemeral bool   `json:"ephemeral,omitempty"`
}

type v1NodeTag struct {
	Name       string `json:"name"`
	Reserved   bool   `json:"reserved,omitempty"`
	Compliance bool   `json:"compliance,omitempty"`
}

func (s *Server) v1Nodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := s.listNodes(r.Context())
	if err != nil {
		writeV1Error(w, http.StatusInternalServerError, "could not load the nodes")
		return
	}
	if _, _, scoped := s.v1Caller(r).scope(); scoped {
		writeV1Error(w, http.StatusForbidden, "this token cannot access that endpoint")
		return
	}
	items := make([]v1Node, 0, len(nodes))
	for i := range nodes {
		node := &nodes[i]
		placement := platform.NodePlacementOf(node)
		view := v1Node{
			Name: node.Name, Ready: nodeReady(node), Schedulable: platform.NodeUsable(node),
			Region: placement.Region, Zone: placement.Zone,
			CPU: node.Status.Allocatable.Cpu().String(), Memory: node.Status.Allocatable.Memory().String(),
		}
		view.Pool, view.Ephemeral = node.Labels[platform.LabelPool], platform.IsEphemeralNode(node)
		_, view.ControlPlane = node.Labels["node-role.kubernetes.io/control-plane"]
		for _, tag := range placement.Tags {
			view.Tags = append(view.Tags, v1NodeTag{Name: tag.Name, Reserved: tag.Reserved, Compliance: tag.Compliance})
		}
		items = append(items, view)
	}
	slices.SortFunc(items, func(a, b v1Node) int { return strings.Compare(a.Name, b.Name) })
	writeJSON(w, http.StatusOK, map[string]any{jsonItems: items})
}
