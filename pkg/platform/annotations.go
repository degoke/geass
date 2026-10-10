package platform

const (
	// AppPendingUpdatesAnnotation counts saved app changes that have not been deployed.
	AppPendingUpdatesAnnotation = "geass.dev/pending-updates"
	// AppPendingChangesAnnotation lists the kinds of saved app changes waiting to deploy.
	AppPendingChangesAnnotation = "geass.dev/pending-changes"
	// AppLastDeployedAnnotation stores the last deployed GeassApp spec as JSON.
	AppLastDeployedAnnotation = "geass.dev/last-deployed"
	// AppDeployRevisionAnnotation changes on every explicit deploy. It is copied to the pod template
	// so a deploy restarts the pods and they pick up new variables, secrets, and code.
	AppDeployRevisionAnnotation = "geass.dev/deploy-revision"
	// AppWebhookStateAnnotation records the last deployment event announced to webhooks, as
	// "<deploy revision>:<event>". Success and failure are only announced for deploys that were
	// announced as started, which also keeps an operator upgrade from announcing old deploys.
	AppWebhookStateAnnotation = "geass.dev/webhook-state"
	// AnnotationFunctionHash fingerprints a function's code on its pod template so edits roll the pods.
	AnnotationFunctionHash = "geass.dev/function-hash"
	// HelmStaleJobUIDAnnotation records the Helm install Job UID that existed
	// when the current HelmChart values generation was written. Ready requires
	// a later Job with a different UID.
	HelmStaleJobUIDAnnotation = "geass.dev/stale-helm-job-uid"
	// DashboardAuthSecretName holds dashboard users and the session key.
	DashboardAuthSecretName = "geass-dashboard-auth"
	// DashboardSessionCookie is the HttpOnly session cookie name.
	DashboardSessionCookie = "geass_session"
)
