package dashboard

// Names the v1 API repeats. They are constants so the same word is never spelled two ways.
const (
	kindProjects         = "projects"
	kindApps             = "apps"
	kindDatabases        = "databases"
	kindObjectStores     = "object-stores"
	kindLogicalDatabases = "logical-databases"
	kindPools            = "pools"
	kindConnections      = "connections"
	kindServers          = "servers"

	// pathNameParam is the {name} of a v1 route, and pathEnvironments the segment that holds a project's environments.
	pathNameParam    = "name"
	pathEnvironments = "environments"

	// verbCreate is the action word of a route that makes something.
	verbCreate = "create"
	verbSave   = "save"
	// ssoUnavailable is the sign-in error of something the dashboard could not read, and the answer a person is given for it.
	ssoUnavailable = "unavailable"

	formProject     = "project"
	formRegion      = "region"
	formEnvironment = "environment"
	formUsername    = "username"
	formRole        = "role"

	stateReady    = "ready"
	statePending  = "pending"
	stateFailed   = "failed"
	stateDraft    = "draft"
	stateDeleting = "deleting"

	sourceGit   = "git"
	boolTrue    = "true"
	jsonString  = "string"
	jsonBoolean = "boolean"
	jsonInteger = "integer"
	jsonArray   = "array"
	// jsonItems is the key a list is answered under.
	jsonItems = "items"

	// formDeploy is the form field that asks for a deploy straight away, and jsonKeyAvailable the key several
	// responses share.
	formDeploy       = "deploy"
	jsonKeyAvailable = "available"
)

// Who may call an operation (see v1Operation.Access), and the groups the operations are listed under.
const (
	accessPublic   = "public"
	accessViewer   = "viewer"
	accessObserver = "observer"
	accessProject  = "project"
	accessAdmin    = "admin"

	tagService   = "service"
	tagAuth      = "auth"
	tagNodes     = "nodes"
	tagCompute   = "compute"
	tagTemplates = "templates"
	tagTokens    = "tokens"
	tagGitHub    = "github"
)
