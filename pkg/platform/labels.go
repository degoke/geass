package platform

const (
	LabelManagedBy       = "geass.dev/managed-by"
	LabelProject         = "geass.dev/project"
	LabelEnvironment     = "geass.dev/environment"
	LabelApp             = "geass.dev/app"
	LabelCluster         = "geass.dev/cluster"
	LabelLogicalDatabase = "geass.dev/logical-database"
	// LabelPullRequest is on every service and database made for a pull request environment. Its value is the
	// pull request's number.
	LabelPullRequest = "geass.dev/pull-request"
	// LabelUsageHour is on each ConfigMap the usage meter keeps an hour in, and names the hour (UTC, 2006010215).
	// LabelUsageDay names its day (20060102), which is how a range of hours is read. The operator's cache leaves these
	// ConfigMaps out: they are read from the API server when they are needed.
	LabelUsageHour       = "geass.dev/usage-hour"
	LabelUsageDay        = "geass.dev/usage-day"
	K8sLabelManagedBy    = "app.kubernetes.io/managed-by"
	K8sLabelAppName      = "app.kubernetes.io/name"
	K8sLabelInstance     = "app.kubernetes.io/instance"
	K8sLabelComponent    = "app.kubernetes.io/component"
	ManagedByValue       = "geass"
	ProjectSharedSecrets = "geass-shared-secrets"
	ProjectSharedVars    = "geass-shared-variables"
	HAReadinessName      = "platform"

	// CNPGLabelCluster and CNPGLabelInstance are on the pods and volumes of a Postgres database made by CloudNativePG: the
	// database they belong to, and the instance.
	CNPGLabelCluster  = "cnpg.io/cluster"
	CNPGLabelInstance = "cnpg.io/instanceName"
)
