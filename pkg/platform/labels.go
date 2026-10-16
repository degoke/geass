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
	LabelPullRequest     = "geass.dev/pull-request"
	K8sLabelManagedBy    = "app.kubernetes.io/managed-by"
	K8sLabelAppName      = "app.kubernetes.io/name"
	K8sLabelInstance     = "app.kubernetes.io/instance"
	K8sLabelComponent    = "app.kubernetes.io/component"
	ManagedByValue       = "geass"
	ProjectSharedSecrets = "geass-shared-secrets"
	ProjectSharedVars    = "geass-shared-variables"
	HAReadinessName      = "platform"
)
