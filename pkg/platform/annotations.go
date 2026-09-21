package platform

const (
	// AppPendingUpdatesAnnotation counts saved app changes that have not been deployed.
	AppPendingUpdatesAnnotation = "geass.dev/pending-updates"
	// AppPendingChangesAnnotation lists the kinds of saved app changes waiting to deploy.
	AppPendingChangesAnnotation = "geass.dev/pending-changes"
	// AppLastDeployedAnnotation stores the last deployed GeassApp spec as JSON.
	AppLastDeployedAnnotation = "geass.dev/last-deployed"
)
