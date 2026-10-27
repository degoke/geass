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
	{http.MethodGet, "/usage/hours", "What each organization reserved, hour by hour, for billing (spec.usage.enabled). Send ?from= and ?to= (RFC 3339) and optionally ?org=. The platform's administrators only", accessAdmin, tagService, nil, false},

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
		{"deny", jsonBoolean, "Refuse the sign-in instead", false},
	}, false},
	{http.MethodGet, "/tokens", "API tokens (a signed-in admin only)", accessAdmin, tagAuth, nil, false},
	{http.MethodPost, "/tokens", "Create an API token. A signed-in admin only", accessAdmin, tagAuth, []v1Field{
		{pathNameParam, jsonString, "A label for the token", true},
		{"scope", jsonString, "workspace, project or service (a deploy hook)", true},
		{"role", jsonString, "For a workspace token: admin, observer or viewer", false},
		{formProject, jsonString, "For a project token or a deploy hook: the project", false},
		{formEnvironment, jsonString, "For a project token or a deploy hook: the environment", false},
		{"app", jsonString, "For a deploy hook (scope service): the service it deploys", false},
		{"expiresInDays", jsonInteger, "Days until it expires; 0 for never", false},
	}, false},
	{http.MethodDelete, "/tokens/{id}", "Revoke an API token. A signed-in admin only", accessAdmin, tagAuth, nil, false},

	// ---- projects ----
	{http.MethodGet, "/projects", "List projects", accessViewer, kindProjects, nil, false},
	{http.MethodPost, "/projects", "Create a project", accessAdmin, kindProjects, []v1Field{{formRegion, jsonString, "The region it runs in", false}}, false},
	{http.MethodGet, "/projects/{name}", "One project", accessViewer, kindProjects, nil, false},
	{http.MethodPatch, "/projects/{name}", "Change a project's settings", accessAdmin, kindProjects, []v1Field{
		{"displayName", jsonString, "The name shown in the dashboard", false},
		{"environments", jsonArray, "The environments the project has", false},
		{formRegion, jsonString, "The region it runs in", false},
	}, false},
	{http.MethodDelete, "/projects/{name}", "Delete a project and everything in it", accessAdmin, kindProjects, []v1Field{fieldConfirm}, false},
	{http.MethodPost, "/projects/{name}/deploy", "Deploy every pending change in an environment", accessAdmin, kindProjects, []v1Field{{formEnvironment, jsonString, "The environment", true}}, false},
	{http.MethodPost, "/projects/{name}/environments", "Add an environment", accessAdmin, kindProjects, []v1Field{{formEnvironment, jsonString, "The new environment", true}}, false},
	{http.MethodPost, "/projects/{name}/compliance", "Set the node tags every workload of the project must run on", accessAdmin, kindProjects, []v1Field{
		{"requiredTags", jsonArray, "Tags such as hipaa. Every one must be a compliance tag on the nodes that carry it", false},
	}, false},
	{http.MethodGet, "/projects/{name}/compliance", "The project's required tags and where it falls short of them", accessViewer, kindProjects, nil, false},
	{http.MethodGet, "/projects/{name}/network", "Who can reach the project's services, and whether they are kept away from the cluster's machines", accessViewer, kindProjects, nil, false},
	{http.MethodPost, "/projects/{name}/network", "Choose who can reach the project's services", accessAdmin, kindProjects, []v1Field{
		{"isolation", jsonString, "Project or None", true},
		{"allowFromProjects", jsonArray, "Other projects allowed to reach this one", false},
		{formBlockNodeAddresses, jsonBoolean, "Keep the project's services away from the machines the cluster runs on. Left as it is when not sent", false},
	}, false},
	{http.MethodGet, "/projects/{name}/quota", "What the project is capped at, and what its environments ask for together", accessViewer, kindProjects, nil, false},
	{http.MethodPost, "/projects/{name}/quota", "Cap what each environment, and the whole project, can use. A limit that is not sent stays as it is; one sent empty is removed", accessAdmin, kindProjects, []v1Field{
		{formQuotaCPU, jsonString, "The most CPU one environment's pods may ask for, such as 4 or 500m", false},
		{formQuotaMemory, jsonString, "The most memory one environment's pods may ask for, such as 8Gi", false},
		{formQuotaStorage, jsonString, "The most disk one environment's volumes may ask for, such as 50Gi", false},
		{formQuotaPods, jsonInteger, "The most pods one environment may have at once", false},
		{formProjectQuotaCPU, jsonString, "The most CPU the whole project's pods may ask for, added up over its environments", false},
		{formProjectQuotaMemory, jsonString, "The most memory the whole project's pods may ask for", false},
		{formProjectQuotaStorage, jsonString, "The most disk the whole project's volumes may ask for", false},
		{formProjectQuotaPods, jsonInteger, "The most pods the whole project may have at once", false},
	}, false},
	{http.MethodPost, "/projects/{name}/variables/save", "Set a shared variable", accessAdmin, kindProjects, []v1Field{
		{pathNameParam, jsonString, "The variable's name", true}, {formEnvironment, jsonString, "The environment", true},
		{"value", jsonString, "The value", true}, {"secret", jsonBoolean, "Keep the value secret", false},
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
		{"cpu", jsonString, "The CPU it reserves, one of 100m, 250m, 500m, 1 or 2 (default 100m)", false},
		{"memory", jsonString, "The memory it reserves, one of 128Mi, 256Mi, 512Mi, 1Gi, 2Gi or 4Gi (default 128Mi)", false},
		{"cpuMax", jsonString, "The most CPU it may use when the server has room. Default: the same as cpu", false},
		{"memoryMax", jsonString, "The most memory it may use when the server has room. Default: the same as memory", false},
		{"cpuRequest", jsonString, "With cpuLimit, instead of cpu and cpuMax: the exact CPU it reserves, of any size, such as 150m. A limit sent without a request is also the request, and the other way round", false},
		{"cpuLimit", jsonString, "With cpuRequest, instead of cpu and cpuMax: the exact most CPU it may use, of any size, not below cpuRequest", false},
		{"memoryRequest", jsonString, "With memoryLimit, instead of memory and memoryMax: the exact memory it reserves, of any size, such as 300Mi", false},
		{"memoryLimit", jsonString, "With memoryRequest, instead of memory and memoryMax: the exact most memory it may use, of any size, not below memoryRequest", false},
		{"draft", jsonBoolean, "Save it as a draft to deploy later. This is what happens when neither draft nor deploy is given", false},
		{formDeploy, jsonBoolean, "Deploy it as soon as it is saved, instead of leaving a draft. Cannot be combined with draft", false},
	}, false},
	{http.MethodGet, "/apps/{name}", "One service, with its settings and status", accessProject, kindApps, nil, false},
	{http.MethodPatch, "/apps/{name}", "Change a service's settings. They apply when it is next deployed, unless live", accessProject, kindApps, []v1Field{
		{"port", jsonInteger, "The port it serves on", false}, {"image", jsonString, "The image", false},
		{"cpu", jsonString, "The CPU it reserves, one of 100m, 250m, 500m, 1 or 2", false},
		{"memory", jsonString, "The memory it reserves, one of 128Mi, 256Mi, 512Mi, 1Gi, 2Gi or 4Gi", false},
		{"cpuMax", jsonString, "The most CPU it may use when the server has room, one of the sizes above and not below cpu. Empty means the same as cpu. Left out, it keeps the ceiling it has", false},
		{"memoryMax", jsonString, "The most memory it may use when the server has room, one of the sizes above and not below memory. Empty means the same as memory. Left out, it keeps the ceiling it has. A service over it is restarted", false},
		{"cpuRequest", jsonString, "With cpuLimit, instead of cpu and cpuMax: the exact CPU it reserves, of any size, such as 150m. A limit sent without a request is also the request, and the other way round", false},
		{"cpuLimit", jsonString, "With cpuRequest, instead of cpu and cpuMax: the exact most CPU it may use, of any size, not below cpuRequest", false},
		{"memoryRequest", jsonString, "With memoryLimit, instead of memory and memoryMax: the exact memory it reserves, of any size, such as 300Mi", false},
		{"memoryLimit", jsonString, "With memoryRequest, instead of memory and memoryMax: the exact most memory it may use, of any size, not below memoryRequest", false},
		{"replicas", jsonInteger, "How many copies run", false}, {formDeploy, jsonBoolean, "Deploy straight away", false},
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
	{http.MethodGet, "/apps/{name}/variables", "A service's variables: secrets and attached references by name, plain configuration with its values. Secret values are never returned", accessObserver, kindApps, nil, false},
	{http.MethodGet, "/apps/{name}/logs", "Recent logs of a service, as {\"lines\": [...]} with one entry per line. Narrow them with ?tail=, ?since=, ?level= and ?q=", accessProject, kindApps, nil, false},
	{http.MethodGet, "/apps/{name}/logs/stream", "Follow a service's logs", accessProject, kindApps, nil, true},
	{http.MethodGet, "/apps/{name}/runtime", "A service's pods", accessProject, kindApps, nil, false},
	{http.MethodGet, "/apps/{name}/metrics", "CPU, memory, network and requests over time", accessProject, kindApps, nil, false},

	// ---- databases ----
	{http.MethodGet, "/databases", "List databases", accessViewer, kindDatabases, nil, false},
	{http.MethodPost, "/databases", "Create a database", accessAdmin, kindDatabases, []v1Field{
		fieldProject, fieldEnv, {pathNameParam, jsonString, "The database's name", false},
		{"engine", jsonString, "postgres, mysql, mariadb, redis or sqlite", true}, {"highAvailability", jsonBoolean, "Run it replicated", false},
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
		{formRegion, jsonString, "The region", false}, {"zone", jsonString, "The site", false},
		{"tags", jsonArray, "The node's tags", false}, {"reserved", jsonArray, "Tags to reserve the node for", false},
		{"compliance", jsonArray, "Tags that are compliance tags, which are always reserved", false},
	}, false},
	// ---- machines: clouds, pools and single servers ----
	{http.MethodGet, "/compute/providers", "The clouds Geass can create machines at, and the credentials and settings each takes", accessViewer, tagCompute, nil, false},
	{http.MethodGet, "/compute/sizes", "The machine sizes a cloud offers in a region, cheapest first. Send ?connection= and ?region=", accessAdmin, tagCompute, nil, false},
	{http.MethodGet, "/connections", "Connections to clouds, with whether the cloud accepts them and what depends on them", accessViewer, tagCompute, nil, false},
	{http.MethodPost, "/connections", "Connect a cloud so that Geass can create machines there. The credentials are stored in a Secret and never shown again", accessAdmin, tagCompute, []v1Field{
		{pathNameParam, jsonString, "A name for the connection", true},
		{"provider", jsonString, "The cloud: see /compute/providers", true},
		{"purposes", jsonArray, "Compute (the default for a cloud that creates machines), and for AWS also Storage or Database", false},
		{formRegion, jsonString, "The connection's default region", false},
		{"credentials", jsonString, "Not a field: send each credential the provider names under its own key (token, accessKeyId, ...)", false},
	}, false},
	{http.MethodGet, "/connections/{name}", "One connection", accessViewer, tagCompute, nil, false},
	{http.MethodPatch, "/connections/{name}", "Change a connection's purposes or default region, or replace credentials (send only the ones that change)", accessAdmin, tagCompute, []v1Field{
		{"purposes", jsonArray, "Storage, Database and Compute", false},
		{formRegion, jsonString, "The default region", false},
	}, false},
	{http.MethodDelete, "/connections/{name}", "Remove a connection and its credentials. Refused while a pool or a machine depends on it", accessAdmin, tagCompute, []v1Field{fieldConfirm}, false},
	{http.MethodPost, "/connections/{name}/verify", "Ask the cloud again whether it accepts the connection's credentials", accessAdmin, tagCompute, nil, false},
	{http.MethodGet, "/pools", "Pools of nodes, with what each has and is doing", accessViewer, tagCompute, nil, false},
	{http.MethodPost, "/pools", "Make a pool of nodes: machines created at a cloud, or servers of yours joined when needed", accessAdmin, tagCompute, []v1Field{
		{pathNameParam, jsonString, "The pool's name", true},
		{"connection", jsonString, "The cloud connection that creates the machines (or send inventory instead)", false},
		{"inventory", jsonArray, "Instead of a connection, servers you own: each {name, address, sshUser, sshPort?, sshKeySecret?}", false},
		{"inventoryKey", jsonString, "The SSH private key of the inventory's servers, stored as a Secret", false},
		{formRegion, jsonString, "The cloud's own region or location", false},
		{"size", jsonString, "The cloud's name for a machine size, such as cx23 or t3.medium", false},
		{"fallbackSizes", jsonArray, "Sizes to try, in order, when the cloud has no room for size", false},
		{"fallbackRegions", jsonArray, "Regions to try, in order, when the cloud has no room in region", false},
		{"image", jsonString, "The operating system image. Default: the cloud's Ubuntu 24.04", false},
		{"spot", jsonBoolean, "Use the cloud's cheaper machines that it may take back", false},
		{"addressType", jsonString, "Public (the default) or Private", false},
		{"settings", jsonString, "Settings of the cloud (a subnet, a firewall), as a JSON object", false},
		{"placementRegion", jsonString, "The region the nodes carry, for placing projects", false},
		{"placementZone", jsonString, "The site the nodes carry", false},
		{"tags", jsonArray, "Node tags the nodes carry", false},
		{"reservedTags", jsonArray, "Of those, the tags that keep everything else off the nodes", false},
		{"storage", jsonString, "None (the default: no databases or volumes on these nodes) or Allow", false},
		{"minNodes", jsonInteger, "The fewest nodes the pool has. A pool that does not autoscale has exactly this many", false},
		{"maxNodes", jsonInteger, "The most nodes the pool ever has. Required", true},
		{"autoscaling", jsonBoolean, "Add nodes when pods cannot start, and remove idle ones", false},
		{"scaleUpDelaySeconds", jsonInteger, "How long pods must be unable to start before a node is added (default 30)", false},
		{"maxScaleUp", jsonInteger, "The most nodes added in one step (default 5)", false},
		{"scaleDownUtilizationPercent", jsonInteger, "A node used less than this is a candidate for removal (default 50)", false},
		{"scaleDownAfterMinutes", jsonInteger, "How long a node must be a candidate before it goes (default 10)", false},
		{"scaleDownDelayAfterAddMinutes", jsonInteger, "No removals this long after a node was added (default 10)", false},
		{"minNodeLifetimeMinutes", jsonInteger, "A node lives at least this long, for clouds that bill each started hour", false},
		{"spareNodes", jsonInteger, "Empty nodes kept ready for a burst", false},
		{"orphanPolicy", jsonString, "Report (the default) or Delete machines at the cloud that no node owns", false},
	}, false},
	{http.MethodGet, "/pools/{name}", "One pool, with its nodes", accessViewer, tagCompute, nil, false},
	{http.MethodPatch, "/pools/{name}", "Change a pool. Size, region and settings apply to nodes made from then on", accessAdmin, tagCompute, []v1Field{
		{"minNodes", jsonInteger, "The fewest nodes", false}, {"maxNodes", jsonInteger, "The most nodes", false},
		{"autoscaling", jsonBoolean, "Follow load between the minimum and the maximum", false},
		{"suspend", jsonBoolean, "Stop adding and removing nodes", false},
		{"size", jsonString, "The size of nodes made from now on", false},
	}, false},
	{http.MethodDelete, "/pools/{name}", "Delete a pool, its nodes and their machines", accessAdmin, tagCompute, []v1Field{fieldConfirm}, false},
	{http.MethodPost, "/pools/{name}/resume", "End the pause of a pool that stopped after repeated failures, and forget them", accessAdmin, tagCompute, nil, false},
	{http.MethodPost, "/servers", "Add a server: a machine created at a cloud (send connection and size), or one you have (send address and sshUser)", accessAdmin, tagCompute, []v1Field{
		{pathNameParam, jsonString, "The server's name. Made up for a machine at a cloud when left out", false},
		{"source", jsonString, "cloud or existing. Default: cloud, or existing when an address is sent", false},
		{"connection", jsonString, "For a cloud: the connection", false},
		{formRegion, jsonString, "For a cloud: its region", false},
		{"size", jsonString, "For a cloud: the machine size", false},
		{"image", jsonString, "For a cloud: the image", false},
		{"settings", jsonString, "For a cloud: its settings as a JSON object", false},
		{"address", jsonString, "For a server you have: where Geass reaches it over SSH", false},
		{"sshUser", jsonString, "For a server you have: a user that is root or can sudo without a password", false},
		{"sshPort", jsonInteger, "For a server you have: the SSH port (default 22)", false},
		{"sshPrivateKey", jsonString, "For a server you have: the SSH private key, stored as a Secret", false},
		{"sshKeySecret", jsonString, "For a server you have: a Secret that already holds the key", false},
		{"knownHosts", jsonString, "For a server you have: its host keys, to pin it", false},
		{"placementRegion", jsonString, "The region the node carries", false},
		{"placementZone", jsonString, "The site the node carries", false},
		{"tags", jsonArray, "Node tags", false},
		{"storage", jsonString, "None or Allow (the default for a single server)", false},
	}, false},
	{http.MethodDelete, "/servers/{name}", "Remove a server that was added on its own: its services move elsewhere and a machine made for it is deleted", accessAdmin, tagCompute, []v1Field{fieldConfirm}, false},
	{http.MethodGet, "/cluster/metrics", "What services use across the cluster, and what the machines are doing", accessViewer, tagNodes, nil, false},
	{http.MethodGet, "/operator/metrics", "How the operator is doing", accessViewer, tagNodes, nil, false},

	// ---- GitHub through an App a provider runs (external mode) ----
	{http.MethodGet, "/github/connections", "The connections of GitHub Apps that a provider outside the cluster runs, each with its installations and the state of their tokens. The platform's administrators only", accessViewer, tagGitHub, nil, false},
	{http.MethodPut, "/github/connections/{name}", "Make or change the connection of a GitHub App a provider runs: Geass then never mints a token or holds the App's key. The webhook secret is kept in a Secret and never shown again. The platform's administrators only", accessAdmin, tagGitHub, []v1Field{
		{formProvider, jsonString, "Who runs the App, as the dashboard names it. Required when the connection is made", false},
		{formAppSlug, jsonString, "The App's slug on GitHub (github.com/apps/<appSlug>)", false},
		{formInstallURL, jsonString, "An https address where a person installs the App; connecting a project with no installation sends them there", false},
		{formWebhookSecret, jsonString, "The secret the provider signs forwarded events with, 32 to 512 characters. Required when the connection is made; send it again to change it", false},
	}, false},
	{http.MethodGet, "/github/connections/{name}", "One connection of a GitHub App a provider runs. The platform's administrators only", accessViewer, tagGitHub, nil, false},
	{http.MethodDelete, "/github/connections/{name}", "Remove a connection of a GitHub App a provider runs, with its tokens. Refused while a project is connected through it", accessAdmin, tagGitHub, []v1Field{fieldConfirm}, false},
	{http.MethodGet, "/github/connections/{name}/installations", "The installations of the App that belong to this cluster, with the state and expiry of each one's token", accessViewer, tagGitHub, nil, false},
	{http.MethodPut, "/github/connections/{name}/installations/{id}", "Record an installation of the App as this cluster's, and the organization whose projects may connect to it", accessAdmin, tagGitHub, []v1Field{
		{formAccount, jsonString, "The login of the GitHub user or organization the App is installed on", true},
		{formAccountType, jsonString, "User, Organization or Enterprise", false},
		{formOrg, jsonString, "The organization whose projects may connect to it. Empty is the platform's: projects of no organization", false},
	}, false},
	{http.MethodDelete, "/github/connections/{name}/installations/{id}", "Remove an installation and its token. The projects connected through it are named in the answer, and stop building from GitHub until connected again", accessAdmin, tagGitHub, nil, false},
	{http.MethodPut, "/github/connections/{name}/installations/{id}/token", "Give Geass the installation's access token, which it uses for everything it does on GitHub for that installation. Stored in a Secret, never returned", accessAdmin, tagGitHub, []v1Field{
		{formToken, jsonString, "The installation access token GitHub issued", true},
		{formExpiresAt, jsonString, "When it expires (RFC 3339), as GitHub said with it", true},
	}, false},
	{http.MethodPost, "/projects/{name}/github/install", "Connect a project to GitHub. With the cluster's own App, the answer is where to install it; with a provider's, the project is connected to an installation of it (the only one its organization has, or the one sent)", accessAdmin, kindProjects, []v1Field{
		{formEnvironment, jsonString, "The environment the person came from", false},
		{formInstallation, jsonInteger, "With a provider's App: the installation to connect to", false},
	}, false},
	{http.MethodGet, "/projects/{name}/github/installations", "With a provider's App: the GitHub accounts a project can be connected to", accessAdmin, kindProjects, nil, false},

	// ---- abuse detection (spec.tenancy.abuse), the platform's alone ----
	{http.MethodGet, "/abuse/suspects", "The services suspected of abuse, with their evidence (a signal, not proof), those the CPU signal is counting, and those that were cleared. Send ?org= for one organization's. The platform's administrators only", accessAdmin, tagAbuse, nil, false},
	{http.MethodPost, "/abuse/suspects/{name}/clear", "Clear a service that was reviewed: the suspicion goes (a suspended service runs again), and the CPU signal leaves it alone for some days. A service that is not suspected can be cleared ahead of a heavy job. The platform's administrators only", accessAdmin, tagAbuse, []v1Field{
		{formAbuseDays, jsonInteger, "How many days the CPU signal leaves it alone, 1 to 365. Default: spec.tenancy.abuse.reviewExemptionDays, or 30", false},
		{formAbuseNote, jsonString, "Why, for the record", false},
	}, false},
	{http.MethodPost, "/abuse/suspects/{name}/release", "Let a suspended service run again. It stays suspected until it is cleared. The platform's administrators only", accessAdmin, tagAbuse, nil, false},
	{http.MethodPost, "/abuse/reports", "Report a workload from a detector outside Geass (Falco through falcosidekick's webhook output, or a provider's own tooling): the same suspicion, condition, event and action as the CPU signal's. A token of the platform's administrators only", accessAdmin, tagAbuse, []v1Field{
		{formAbuseService, jsonString, "The service, by name. Or send namespace and pod", false},
		{formAbuseNS, jsonString, "The namespace of the pod (Falco: output_fields.k8s.ns.name)", false},
		{formAbusePod, jsonString, "A pod that is a copy of the service (Falco: output_fields.k8s.pod.name)", false},
		{formAbuseReporter, jsonString, "Who reports it, such as falco. Default for a Falco event: falco", false},
		{formAbuseReason, jsonString, "What was seen, such as a rule's name. Default for a Falco event: its rule. It is shown to the organization on its service", false},
		{formAbusePriority, jsonString, "How urgent the reporter thinks it is", false},
		{formAbuseTime, jsonString, "When it was seen (RFC 3339)", false},
		{formFalcoFields, "object", "Falco's output_fields, as falcosidekick sends them: the namespace and pod are read from it", false},
	}, false},

	// ---- templates ----
	{http.MethodGet, "/templates", "Catalog and saved templates", accessObserver, tagTemplates, nil, false},
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
