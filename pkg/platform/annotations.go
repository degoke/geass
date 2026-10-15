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
	// AppGitHubDeploymentAnnotation records where Geass last reported a repository service's deploy to GitHub, as
	// "<commit>:<GitHub deployment id>:<state>". It is what keeps a deploy from being reported twice: a state is
	// only sent when it differs from the one recorded. A service with no record that is already running when
	// the record is first looked for is given one without anything being sent, so upgrading never reports the
	// deploys that were made before.
	AppGitHubDeploymentAnnotation = "geass.dev/github-deployment"
	// BuildGitHubStatusAnnotation records the build state last reported to GitHub as a commit status, as
	// "<commit>:<build phase>". A build without it that is not changing state is left alone, so builds made
	// before Geass reported to GitHub are not reported when the operator starts.
	BuildGitHubStatusAnnotation = "geass.dev/github-status"
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
