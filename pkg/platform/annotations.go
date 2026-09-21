package platform

const (
	// AppPendingUpdatesAnnotation counts saved app changes that have not been deployed.
	AppPendingUpdatesAnnotation = "geass.dev/pending-updates"
	// AppPendingChangesAnnotation lists the kinds of saved app changes waiting to deploy.
	AppPendingChangesAnnotation = "geass.dev/pending-changes"
	// AppLastDeployedAnnotation stores the last deployed GeassApp spec as JSON.
	AppLastDeployedAnnotation = "geass.dev/last-deployed"
	// HelmStaleJobUIDAnnotation records the Helm install Job UID that existed
	// when the current HelmChart values generation was written. Ready requires
	// a later Job with a different UID.
	HelmStaleJobUIDAnnotation = "geass.dev/stale-helm-job-uid"
	// DashboardAuthSecretName holds the dashboard password and session key.
	DashboardAuthSecretName = "geass-dashboard-auth"
	// DashboardSessionCookie is the HttpOnly session cookie name.
	DashboardSessionCookie = "geass_session"
)
