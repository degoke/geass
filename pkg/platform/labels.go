package platform

const (
	LabelManagedBy       = "geass.dev/managed-by"
	LabelProject         = "geass.dev/project"
	LabelEnvironment     = "geass.dev/environment"
	LabelApp             = "geass.dev/app"
	LabelCluster         = "geass.dev/cluster"
	LabelLogicalDatabase = "geass.dev/logical-database"
	K8sLabelManagedBy    = "app.kubernetes.io/managed-by"
	K8sLabelAppName      = "app.kubernetes.io/name"
	ManagedByValue       = "geass"
	ProjectSharedSecrets = "geass-shared-secrets"
	ProjectSharedVars    = "geass-shared-variables"
	HAReadinessName      = "platform"
)
