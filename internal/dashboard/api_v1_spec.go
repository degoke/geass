package dashboard

import (
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strings"
)

// v1Field is a field of a request body.
type v1Field struct {
	Name        string
	Type        string // string, integer, boolean, array, object
	Description string
	Required    bool
}

// v1Operation is one thing the v1 API does. The list below is the API's description: the OpenAPI document is
// generated from it, so what is documented and what is served cannot drift apart without a test noticing
// (api_v1_spec_test.go checks every operation here is answered, and every read the API serves is here).
type v1Operation struct {
	Method  string
	Path    string
	Summary string
	// Access is who may call it: public, viewer (anyone signed in), observer (also reads what is kept from
	// viewers), project (a project token for its own environment may too) or admin.
	Access string
	Tag    string
	Fields []v1Field
	// Stream says the response is server-sent events.
	Stream bool
}

var (
	fieldConfirm = v1Field{"confirmName", jsonString, "The name of the resource, to confirm deleting it", true}
	fieldProject = v1Field{formProject, jsonString, "The project", true}
	fieldEnv     = v1Field{formEnvironment, jsonString, "The environment, such as production", true}
)

// v1Operations is the whole API. Reads that only v1 has are native; the others are the dashboard's own
// routes under the version prefix; every write is a dashboard handler fed a form made from the JSON body.
var v1Operations = []v1Operation{
	// ---- the service itself ----
	{http.MethodGet, "/version", "The API version", accessViewer, tagService, nil, false},
	{http.MethodGet, "/me", "Who the caller is and what they may do", accessViewer, tagService, nil, false},
	{http.MethodGet, "/openapi.json", "This description of the API", accessViewer, tagService, nil, false},
	{http.MethodGet, "/events", "A stream that says when anything changes, so a caller need not poll", accessViewer, tagService, nil, true},
	{http.MethodGet, "/usage", "Resource use across the cluster", accessViewer, tagService, nil, false},

	// ---- signing in ----
	{http.MethodPost, "/auth/device", "Start a sign-in from a program. Returns a code for the person to approve", accessPublic, tagAuth,
		[]v1Field{{"clientName", jsonString, "What is asking, shown to the person who approves", false}}, false},
	{http.MethodPost, "/auth/token", "Ask whether a sign-in has been approved. Returns the token once it has", accessPublic, tagAuth,
		[]v1Field{{"deviceCode", jsonString, "The device code from the start of the sign-in", true}}, false},
	{http.MethodGet, "/auth/device/{code}", "What a sign-in request is, before approving it (the dashboard page uses this)", accessAdmin, tagAuth, nil, false},
	{http.MethodPost, "/auth/device/approve", "Approve or refuse a sign-in. A signed-in admin only", accessAdmin, tagAuth, []v1Field{
		{"userCode", jsonString, "The code the program is showing", true},
		{"role", jsonString, "admin, observer or viewer. Defaults to observer", false},
		{"expiresInDays", jsonInteger, "How long the token lasts. Defaults to 30", false},
		{"deny", "boolean", "Refuse the sign-in instead", false},
	}, false},
	{http.MethodGet, "/tokens", "API tokens (a signed-in admin only)", accessAdmin, tagAuth, nil, false},
	{http.MethodPost, "/tokens", "Create an API token. A signed-in admin only", accessAdmin, tagAuth, []v1Field{
		{pathNameParam, jsonString, "A label for the token", true},
		{"scope", jsonString, "workspace or project", true},
		{"role", jsonString, "For a workspace token: admin, observer or viewer", false},
		{formProject, jsonString, "For a project token: the project", false},
		{formEnvironment, jsonString, "For a project token: the environment", false},
		{"expiresInDays", jsonInteger, "Days until it expires; 0 for never", false},
	}, false},
	{http.MethodDelete, "/tokens/{id}", "Revoke an API token. A signed-in admin only", accessAdmin, tagAuth, nil, false},

	// ---- projects ----
	{http.MethodGet, "/projects", "List projects", accessViewer, kindProjects, nil, false},
	{http.MethodPost, "/projects", "Create a project", accessAdmin, kindProjects, []v1Field{{"region", jsonString, "The region it runs in", false}}, false},
	{http.MethodGet, "/projects/{name}", "One project", accessViewer, kindProjects, nil, false},
	{http.MethodPatch, "/projects/{name}", "Change a project's settings", accessAdmin, kindProjects, []v1Field{
		{"displayName", jsonString, "The name shown in the dashboard", false},
		{"environments", jsonArray, "The environments the project has", false},
		{"region", jsonString, "The region it runs in", false},
	}, false},
	{http.MethodDelete, "/projects/{name}", "Delete a project and everything in it", accessAdmin, kindProjects, []v1Field{fieldConfirm}, false},
	{http.MethodPost, "/projects/{name}/deploy", "Deploy every pending change in an environment", accessAdmin, kindProjects, []v1Field{{formEnvironment, jsonString, "The environment", true}}, false},
	{http.MethodPost, "/projects/{name}/environments", "Add an environment", accessAdmin, kindProjects, []v1Field{{formEnvironment, jsonString, "The new environment", true}}, false},
	{http.MethodPost, "/projects/{name}/compliance", "Set the node tags every workload of the project must run on", accessAdmin, kindProjects, []v1Field{
		{"requiredTags", jsonArray, "Tags such as hipaa. Every one must be a compliance tag on the nodes that carry it", false},
	}, false},
	{http.MethodGet, "/projects/{name}/compliance", "The project's required tags and where it falls short of them", accessViewer, kindProjects, nil, false},
	{http.MethodPost, "/projects/{name}/network", "Choose who can reach the project's services", accessAdmin, kindProjects, []v1Field{
		{"isolation", jsonString, "Project or None", true},
		{"allowFromProjects", jsonArray, "Other projects allowed to reach this one", false},
	}, false},
	{http.MethodPost, "/projects/{name}/variables/save", "Set a shared variable", accessAdmin, kindProjects, []v1Field{
		{pathNameParam, jsonString, "The variable's name", true}, {formEnvironment, jsonString, "The environment", true},
		{"value", jsonString, "The value", true}, {"secret", "boolean", "Keep the value secret", false},
	}, false},
	{http.MethodGet, "/projects/{name}/metrics", "CPU, memory and network over time for a project", accessViewer, kindProjects, nil, false},
	{http.MethodGet, "/projects/{name}/logs", "Recent logs of the project's services", accessObserver, kindProjects, nil, false},
	{http.MethodGet, "/projects/{name}/logs/stream", "Follow the project's logs", accessObserver, kindProjects, nil, true},
	{http.MethodGet, "/projects/{name}/logs/history", "Older logs, from the logging add-on", accessObserver, kindProjects, nil, false},

	// ---- apps ----
	{http.MethodGet, "/apps", "List services. Filter with ?project= and ?environment=", accessProject, kindApps, nil, false},
	{http.MethodPost, "/apps", "Create a service", accessAdmin, kindApps, []v1Field{
		fieldProject, fieldEnv,
		{pathNameParam, jsonString, "The service's name", false},
		{"source", jsonString, "image, git or function", true},
		{"image", jsonString, "For an image source: the image", false},
		{"repository", jsonString, "For a git source: owner/repo", false},
		{"branch", jsonString, "For a git source: the branch", false},
		{"runtime", jsonString, "For a function: bun, node or python", false},
		{"code", jsonString, "For a function: its source", false},
		{"mode", jsonString, "For a function: http (a program that serves HTTP, the default) or event (a handler that is given an event)", false},
		{"functionTriggers", jsonArray, "For a function: its schedules, each {name, schedule, path?, payload?}", false},
		{"port", jsonInteger, "The port it serves on", false},
		{"draft", "boolean", "Save it without deploying", false},
	}, false},
	{http.MethodGet, "/apps/{name}", "One service, with its settings and status", accessProject, kindApps, nil, false},
	{http.MethodPatch, "/apps/{name}", "Change a service's settings. They apply when it is next deployed, unless live", accessProject, kindApps, []v1Field{
		{"port", jsonInteger, "The port it serves on", false}, {"image", jsonString, "The image", false},
		{"cpu", jsonString, "CPU request, such as 250m", false}, {"memory", jsonString, "Memory request, such as 512Mi", false},
		{"replicas", jsonInteger, "How many copies run", false}, {formDeploy, "boolean", "Deploy straight away", false},
		{"functionCode", jsonString, "For a function: its source", false}, {"functionRuntime", jsonString, "For a function: bun, node or python", false},
		{"functionMode", jsonString, "For a function: http or event", false},
		{"functionTriggers", jsonArray, "For a function: its schedules, each {name, schedule, path?, payload?}. They apply when it is next deployed. An empty string removes them all", false},
		{"scaleToZero", jsonString, "on to stop the service when idle and start it on the next request, off to keep it running. Takes effect at once", false},
		{"idleSeconds", jsonInteger, "How long without requests before it stops, 30 to 86400 (default 300)", false},
		{"scaleToZeroMax", jsonInteger, "At most how many copies run when awake, 1 to 100 (default 3)", false},
	}, false},
	{http.MethodDelete, "/apps/{name}", "Delete a service", accessAdmin, kindApps, []v1Field{fieldConfirm}, false},
	{http.MethodGet, "/apps/{name}/status", "Everything a deploy leaves behind, in one read: state, replicas, build, last attempt", accessProject, kindApps, nil, false},
	{http.MethodGet, "/apps/{name}/deployments", "A service's deployment history, newest first", accessProject, kindApps, nil, false},
	{http.MethodGet, "/apps/{name}/deployments/{id}/logs", "The build and deploy logs of one deployment", accessProject, kindApps, nil, false},
	{http.MethodPost, "/apps/{name}/deploy", "Deploy a service's pending changes", accessProject, kindApps, nil, false},
	{http.MethodPost, "/apps/{name}/scale", "Set how many copies run. Takes effect at once", accessProject, kindApps, []v1Field{{"replicas", jsonInteger, "How many", true}}, false},
	{http.MethodPost, "/apps/{name}/rollback", "Go back to an earlier deployment", accessProject, kindApps, []v1Field{{"revision", jsonString, "The deployment to return to", true}}, false},
	{http.MethodPost, "/apps/{name}/build", "Start the build again", accessProject, kindApps, nil, false},
	{http.MethodPost, "/apps/{name}/build/cancel", "Cancel a running build", accessProject, kindApps, nil, false},
	{http.MethodPost, "/apps/{name}/retry-provision", "Try again after a failed deploy", accessProject, kindApps, nil, false},
	{http.MethodPost, "/apps/{name}/config/set", "Set a configuration value", accessProject, kindApps, []v1Field{{"key", jsonString, "The name", true}, {"value", jsonString, "The value", true}}, false},
	{http.MethodPost, "/apps/{name}/config/delete", "Remove a configuration value", accessProject, kindApps, []v1Field{{"key", jsonString, "The name", true}}, false},
	{http.MethodPost, "/apps/{name}/secrets/set", "Set a secret", accessProject, kindApps, []v1Field{{"key", jsonString, "The name", true}, {"value", jsonString, "The value", true}}, false},
	{http.MethodPost, "/apps/{name}/secrets/delete", "Remove a secret", accessProject, kindApps, []v1Field{{"key", jsonString, "The name", true}}, false},
	{http.MethodGet, "/apps/{name}/variables", "The names of a service's variables (never their values)", accessObserver, kindApps, nil, false},
	{http.MethodGet, "/apps/{name}/logs", "Recent logs of a service", accessProject, kindApps, nil, false},
	{http.MethodGet, "/apps/{name}/logs/stream", "Follow a service's logs", accessProject, kindApps, nil, true},
	{http.MethodGet, "/apps/{name}/runtime", "A service's pods", accessProject, kindApps, nil, false},
	{http.MethodGet, "/apps/{name}/metrics", "CPU, memory, network and requests over time", accessProject, kindApps, nil, false},

	// ---- databases ----
	{http.MethodGet, "/databases", "List databases", accessViewer, kindDatabases, nil, false},
	{http.MethodPost, "/databases", "Create a database", accessAdmin, kindDatabases, []v1Field{
		fieldProject, fieldEnv, {pathNameParam, jsonString, "The database's name", false},
		{"engine", jsonString, "postgres, mysql, redis or sqlite", true}, {"highAvailability", "boolean", "Run it replicated", false},
	}, false},
	{http.MethodGet, "/databases/{name}", "One database", accessViewer, kindDatabases, nil, false},
	{http.MethodPatch, "/databases/{name}", "Change a database's settings", accessAdmin, kindDatabases, nil, false},
	{http.MethodDelete, "/databases/{name}", "Delete a database", accessAdmin, kindDatabases, []v1Field{fieldConfirm}, false},
	{http.MethodPost, "/databases/{name}/deploy", "Deploy a database's pending changes", accessAdmin, kindDatabases, nil, false},
	{http.MethodGet, "/databases/{name}/connection", "How to connect. The password and URI only with ?reveal=true, to a workspace admin", accessAdmin, kindDatabases, nil, false},
	{http.MethodGet, "/databases/{name}/metrics", "CPU, memory, disk and network over time", accessViewer, kindDatabases, nil, false},
	{http.MethodPost, "/databases/restore", "Restore a backup into a new database", accessAdmin, kindDatabases, []v1Field{
		{"archive", jsonString, "The backup archive", true}, {"restorePoint", jsonString, "The restore point", false},
	}, false},

	// ---- logical databases, buckets ----
	{http.MethodGet, "/logical-databases", "List logical databases", accessViewer, kindDatabases, nil, false},
	{http.MethodPost, "/logical-databases", "Create a database on a shared server", accessAdmin, kindDatabases, nil, false},
	{http.MethodGet, "/logical-databases/{name}", "One logical database", accessViewer, kindDatabases, nil, false},
	{http.MethodDelete, "/logical-databases/{name}", "Delete a logical database", accessAdmin, kindDatabases, []v1Field{fieldConfirm}, false},
	{http.MethodGet, "/logical-databases/{name}/connection", "How to connect, as for a database", accessAdmin, kindDatabases, nil, false},
	{http.MethodGet, "/object-stores", "List buckets", accessViewer, kindObjectStores, nil, false},
	{http.MethodPost, "/object-stores", "Create a bucket", accessAdmin, kindObjectStores, nil, false},
	{http.MethodGet, "/object-stores/{name}", "One bucket", accessViewer, kindObjectStores, nil, false},
	{http.MethodDelete, "/object-stores/{name}", "Delete a bucket", accessAdmin, kindObjectStores, []v1Field{fieldConfirm}, false},

	// ---- nodes ----
	{http.MethodGet, "/nodes", "The cluster's nodes, with their region, zone and tags", accessViewer, tagNodes, nil, false},
	{http.MethodPost, "/nodes/{name}/placement", "Set a node's region, zone and tags", accessAdmin, tagNodes, []v1Field{
		{"region", jsonString, "The region", false}, {"zone", jsonString, "The site", false},
		{"tags", jsonArray, "The node's tags", false}, {"reserved", jsonArray, "Tags to reserve the node for", false},
		{"compliance", jsonArray, "Tags that are compliance tags, which are always reserved", false},
	}, false},
	{http.MethodGet, "/cluster/metrics", "What services use across the cluster, and what the machines are doing", accessViewer, tagNodes, nil, false},
	{http.MethodGet, "/operator/metrics", "How the operator is doing", accessViewer, tagNodes, nil, false},

	// ---- templates ----
	{http.MethodGet, "/templates", "Saved and built-in templates", accessObserver, tagTemplates, nil, false},
	{http.MethodPost, "/templates/{id}/deploy", "Deploy a template into an environment", accessAdmin, tagTemplates, []v1Field{fieldProject, fieldEnv}, false},

	// ---- for project tokens ----
	{http.MethodGet, "/token/services", "The services a project token can deploy, and whether they are ready", accessProject, tagTokens, nil, false},
	{http.MethodPost, "/token/deploy", "Deploy every pending change in the token's environment", accessProject, tagTokens, nil, false},
}

var pathParameter = regexp.MustCompile(`\{([^}]+)\}`)

// v1OpenAPI is the API described in OpenAPI 3.
func v1OpenAPI() map[string]any {
	paths := map[string]map[string]any{}
	for _, operation := range v1Operations {
		item := paths[operation.Path]
		if item == nil {
			item = map[string]any{}
			paths[operation.Path] = item
		}
		item[strings.ToLower(operation.Method)] = operationObject(operation)
	}
	out := map[string]any{}
	for path, item := range paths {
		out[path] = item
	}
	return map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":       "Geass API",
			"version":     "1",
			"description": "Deploy and run services, databases and buckets. Send `Authorization: Bearer <token>`, with a body of JSON. Every error is {\"error\": \"...\", \"code\": \"...\"}.",
		},
		"servers":    []map[string]string{{"url": "/api/v1"}},
		"security":   []map[string][]string{{"bearer": {}}},
		"components": map[string]any{"securitySchemes": map[string]any{"bearer": map[string]any{"type": "http", "scheme": "bearer"}}},
		"paths":      out,
	}
}

func operationObject(operation v1Operation) map[string]any {
	object := map[string]any{
		"summary":     operation.Summary,
		"tags":        []string{operation.Tag},
		"operationId": operationID(operation),
		"x-access":    operation.Access,
		"responses":   responses(operation),
	}
	matches := pathParameter.FindAllStringSubmatch(operation.Path, -1)
	parameters := make([]map[string]any, 0, len(matches))
	for _, match := range matches {
		parameters = append(parameters, map[string]any{pathNameParam: match[1], "in": "path", "required": true, "schema": map[string]string{"type": "string"}})
	}
	if len(parameters) > 0 {
		object["parameters"] = parameters
	}
	if operation.Access == "public" {
		object["security"] = []map[string][]string{}
	}
	if len(operation.Fields) > 0 {
		properties := map[string]any{}
		var required []string
		for _, field := range operation.Fields {
			properties[field.Name] = fieldSchema(field)
			if field.Required {
				required = append(required, field.Name)
			}
		}
		schema := map[string]any{"type": "object", "properties": properties}
		if len(required) > 0 {
			slices.Sort(required)
			schema["required"] = required
		}
		object["requestBody"] = map[string]any{"required": len(required) > 0, "content": map[string]any{"application/json": map[string]any{"schema": schema}}}
	}
	return object
}

func fieldSchema(field v1Field) map[string]any {
	schema := map[string]any{"type": field.Type, "description": field.Description}
	if field.Type == jsonArray {
		schema["items"] = map[string]string{"type": "string"}
	}
	return schema
}

func responses(operation v1Operation) map[string]any {
	ok := map[string]any{"description": "Success"}
	if operation.Stream {
		ok = map[string]any{"description": "A stream of server-sent events", "content": map[string]any{"text/event-stream": map[string]any{"schema": map[string]string{"type": "string"}}}}
	}
	out := map[string]any{"200": ok}
	for _, status := range []string{"400", "401", "403", "404"} {
		out[status] = map[string]any{"description": "An error: {\"error\": \"...\", \"code\": \"...\"}"}
	}
	return out
}

func operationID(operation v1Operation) string {
	segments := strings.Split(strings.Trim(operation.Path, "/"), "/")
	parts := make([]string, 0, 1+len(segments))
	parts = append(parts, strings.ToLower(operation.Method))
	for _, segment := range segments {
		segment = strings.NewReplacer("{", "", "}", "").Replace(segment)
		parts = append(parts, strings.ToUpper(segment[:1])+segment[1:])
	}
	return strings.Join(parts, "")
}

func (s *Server) v1OpenAPIHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(v1OpenAPI())
}
