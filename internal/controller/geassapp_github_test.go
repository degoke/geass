package controller

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/githubapp"
	"github.com/degoke/geass/pkg/platform"
)

const (
	deployRevisionOne = "2026-10-14T12:00:00.5Z"
	deployRevisionTwo = "2026-10-14T13:30:00.25Z"
)

// repoService is a service that builds acme/web and answers on web.example.com.
func repoService(mutate ...func(*geassv1alpha1.GeassApp)) *geassv1alpha1.GeassApp {
	app := &geassv1alpha1.GeassApp{
		ObjectMeta: metav1.ObjectMeta{
			Name: "web", Namespace: platform.SystemNamespace, Finalizers: []string{appFinalizer},
			Annotations: map[string]string{platform.AppDeployRevisionAnnotation: deployRevisionOne},
		},
		Spec: geassv1alpha1.GeassAppSpec{
			Project: "shop", Environment: "production", Port: 8080,
			Deploy:  geassv1alpha1.GeassAppDeploySpec{Enabled: true},
			Ingress: geassv1alpha1.GeassAppIngressSpec{Host: "web.example.com", TLSEnabled: true},
			Build:   geassv1alpha1.GeassAppBuildSpec{Registry: geassv1alpha1.GeassAppRegistrySpec{Repository: "registry.example.com/shop/web"}},
			Source: geassv1alpha1.GeassAppSource{Git: &geassv1alpha1.GeassAppGitSource{
				Repository: "acme/web", Branch: "main", ConnectionRef: corev1.LocalObjectReference{Name: "shop-github"},
			}},
		},
	}
	for _, m := range mutate {
		m(app)
	}
	return app
}

func reconcileService(t *testing.T, r *GeassAppReconciler, name string) {
	t.Helper()
	_, err := r.Reconcile(context.Background(), reconcileRequest(&geassv1alpha1.GeassApp{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: platform.SystemNamespace}}))
	require.NoError(t, err)
}

func deploymentNamed(t *testing.T, c client.Client, name, namespace string) *appsv1.Deployment {
	t.Helper()
	var deploy appsv1.Deployment
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: name, Namespace: namespace}, &deploy))
	return &deploy
}

// serviceIsUp plays the Deployment controller for any service, which markDeploymentAvailable does only for web.
func serviceIsUp(t *testing.T, c client.Client, name, namespace string) {
	t.Helper()
	deploy := deploymentNamed(t, c, name, namespace)
	deploy.Status.ObservedGeneration = deploy.Generation
	deploy.Status.Replicas, deploy.Status.ReadyReplicas, deploy.Status.AvailableReplicas, deploy.Status.UpdatedReplicas = 1, 1, 1, 1
	require.NoError(t, c.Status().Update(context.Background(), deploy))
}

// buildOf is the build the operator made for a service, found by the label it carries.
func buildOf(t *testing.T, c client.Client, app string) geassv1alpha1.GeassBuild {
	t.Helper()
	var builds geassv1alpha1.GeassBuildList
	require.NoError(t, c.List(context.Background(), &builds, client.InNamespace(platform.SystemNamespace), client.MatchingLabels{platform.LabelApp: app}))
	require.Len(t, builds.Items, 1)
	return builds.Items[0]
}

// buildSucceeds plays the build controller: the build of a service pushed its image, built from sha.
func buildSucceeds(t *testing.T, c client.Client, app, sha, digest string) {
	t.Helper()
	build := buildOf(t, c, app)
	build.Status.Phase, build.Status.SourceRevision, build.Status.ImageDigest = geassv1alpha1.GeassBuildSucceeded, sha, digest
	build.Status.Image = build.Spec.Registry
	require.NoError(t, c.Status().Update(context.Background(), &build))
}

func storedApp(t *testing.T, c client.Client, name string) geassv1alpha1.GeassApp {
	t.Helper()
	var app geassv1alpha1.GeassApp
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, &app))
	return app
}

func deploymentsOf(gh *fakeGitHubAPI) []seenGitHubRequest {
	return gh.to(http.MethodPost, "/repos/acme/web/deployments")
}

func deploymentStatuses(gh *fakeGitHubAPI, id int) []seenGitHubRequest {
	return gh.to(http.MethodPost, "/repos/acme/web/deployments/"+strconv.Itoa(id)+"/statuses")
}

// githubTakesDeploys answers like a GitHub that has the permissions: deployments are numbered from 4801.
func githubTakesDeploys(gh *fakeGitHubAPI) {
	next := 4800
	gh.mu.Lock()
	defer gh.mu.Unlock()
	gh.answers["POST /repos/acme/web/deployments"] = func(map[string]any) (int, any) {
		next++
		return http.StatusCreated, map[string]any{"id": next}
	}
	for id := 4801; id < 4810; id++ {
		gh.answers["POST /repos/acme/web/deployments/"+strconv.Itoa(id)+"/statuses"] = func(map[string]any) (int, any) {
			return http.StatusCreated, map[string]any{"id": 1}
		}
	}
}

func TestADeployOfARepositoryServiceIsReportedFromTheBuildToTheRollout(t *testing.T) {
	w := newGitHubWorld(t, worldOptions{objects: []client.Object{repoService()}})
	githubTakesDeploys(w.gh)
	r := w.appReconciler(t)

	reconcileService(t, r, "web") // makes the build, which has to run before anything is deployed
	require.Empty(t, w.gh.all(), "nothing is deployed while the build runs: that is the commit status's to say")

	buildSucceeds(t, w.client, "web", reportSHA, digestOne)
	reconcileService(t, r, "web") // the Deployment is made with the new image, and is not ready yet
	deployments := deploymentsOf(w.gh)
	require.Len(t, deployments, 1)
	require.Equal(t, map[string]any{
		"ref": reportSHA, "environment": "production", "description": "Geass: web",
		"auto_merge": false, "required_contexts": []any{}, "transient_environment": false,
	}, deployments[0].Body)
	started := deploymentStatuses(w.gh, 4801)
	require.Len(t, started, 1)
	require.Equal(t, "in_progress", started[0].Body["state"])
	require.Equal(t, "https://web.example.com/", started[0].Body["environment_url"])
	require.Equal(t, reportDashboard+"/projects/shop/apps/web?environment=production&view=deployments", started[0].Body["log_url"])

	for range 3 {
		reconcileService(t, r, "web")
	}
	require.Len(t, w.gh.all(), 2, "a deploy that is still going is not reported again on every pass")

	serviceIsUp(t, w.client, "web", "shop-production")
	reconcileService(t, r, "web")
	statuses := deploymentStatuses(w.gh, 4801)
	require.Len(t, statuses, 2)
	require.Equal(t, "success", statuses[1].Body["state"])
	require.Equal(t, "https://web.example.com/", statuses[1].Body["environment_url"], "the service's public address is the environment URL")
	require.Len(t, deploymentsOf(w.gh), 1, "the status went to the deployment that was made, and not to another")

	w.gh.reset()
	for range 4 {
		reconcileService(t, r, "web")
	}
	require.Empty(t, w.gh.all(), "a service that is up and unchanged tells GitHub nothing")
	require.Equal(t, reportSHA+":4801:success:"+deployRevisionOne, storedApp(t, w.client, "web").Annotations[platform.AppGitHubDeploymentAnnotation])
}

func TestAPushIsANewDeploymentAndAnExplicitDeployOfTheSameCommitIsToo(t *testing.T) {
	w := newGitHubWorld(t, worldOptions{objects: []client.Object{repoService()}})
	githubTakesDeploys(w.gh)
	r := w.appReconciler(t)
	reconcileService(t, r, "web")
	buildSucceeds(t, w.client, "web", reportSHA, digestOne)
	reconcileService(t, r, "web")
	serviceIsUp(t, w.client, "web", "shop-production")
	reconcileService(t, r, "web")
	require.Len(t, deploymentsOf(w.gh), 1)

	// A push: the same build runs again and is built from the next commit.
	build := buildOf(t, w.client, "web")
	platform.ResetBuildStatus(&build.Status)
	require.NoError(t, w.client.Status().Update(context.Background(), &build))
	reconcileService(t, r, "web") // waiting for the build
	buildSucceeds(t, w.client, "web", reportNextSHA, digestTwo)
	reconcileService(t, r, "web")
	serviceIsUp(t, w.client, "web", "shop-production")
	reconcileService(t, r, "web")

	deployments := deploymentsOf(w.gh)
	require.Len(t, deployments, 2, "a new commit is a new GitHub Deployment")
	require.Equal(t, reportNextSHA, deployments[1].Body["ref"])
	require.Equal(t, "success", deploymentStatuses(w.gh, 4802)[len(deploymentStatuses(w.gh, 4802))-1].Body["state"])

	// The same commit deployed again from the dashboard: a new revision.
	app := storedApp(t, w.client, "web")
	app.Annotations[platform.AppDeployRevisionAnnotation] = deployRevisionTwo
	require.NoError(t, w.client.Update(context.Background(), &app))
	reconcileService(t, r, "web")
	serviceIsUp(t, w.client, "web", "shop-production")
	reconcileService(t, r, "web")

	deployments = deploymentsOf(w.gh)
	require.Len(t, deployments, 3, "so is a deploy of the commit that is already running: it restarts the pods and applies new variables")
	require.Equal(t, reportNextSHA, deployments[2].Body["ref"])
}

func TestAServiceThatRestartsOrScalesIsNotDeployingAgain(t *testing.T) {
	w := newGitHubWorld(t, worldOptions{objects: []client.Object{repoService()}})
	githubTakesDeploys(w.gh)
	r := w.appReconciler(t)
	reconcileService(t, r, "web")
	buildSucceeds(t, w.client, "web", reportSHA, digestOne)
	reconcileService(t, r, "web")
	serviceIsUp(t, w.client, "web", "shop-production")
	reconcileService(t, r, "web")
	w.gh.reset()

	// A node goes down: the service has fewer ready copies than it wants, for a while.
	deploy := deploymentNamed(t, w.client, "web", "shop-production")
	deploy.Status.ReadyReplicas, deploy.Status.AvailableReplicas = 0, 0
	require.NoError(t, w.client.Status().Update(context.Background(), deploy))
	reconcileService(t, r, "web")
	serviceIsUp(t, w.client, "web", "shop-production")
	reconcileService(t, r, "web")

	require.Empty(t, w.gh.all(), "the deploy that succeeded is over: what happens to the pods afterwards is not another deploy")
}

func TestServicesThatWereRunningBeforeReportingExistedAreNotAnnouncedWhenTheOperatorStarts(t *testing.T) {
	// The service is deployed while the project does not report, which is how a service the operator has been running
	// for a long time looks to the first operator that does: ready, built from a commit, and with nothing recorded.
	w := newGitHubWorld(t, worldOptions{reportingOff: true, objects: []client.Object{repoService()}})
	githubTakesDeploys(w.gh)
	r := w.appReconciler(t)
	reconcileService(t, r, "web")
	buildSucceeds(t, w.client, "web", reportSHA, digestOne)
	reconcileService(t, r, "web")
	serviceIsUp(t, w.client, "web", "shop-production")
	reconcileService(t, r, "web")
	require.Empty(t, w.gh.all(), "reporting is off")
	var project geassv1alpha1.GeassProject
	require.NoError(t, w.client.Get(context.Background(), client.ObjectKey{Name: "shop", Namespace: platform.SystemNamespace}, &project))
	project.Spec.GitHubReporting = nil // on, as it is for a project that never set it
	require.NoError(t, w.client.Update(context.Background(), &project))

	for range 3 {
		reconcileService(t, r, "web")
	}

	require.Empty(t, w.gh.all(), "no deployment is made for a deploy that happened before")
	require.Equal(t, reportSHA+":0:success:"+deployRevisionOne, storedApp(t, w.client, "web").Annotations[platform.AppGitHubDeploymentAnnotation],
		"it is recorded, so the next deploy is known to be a new one")

	// The next push is reported.
	build := buildOf(t, w.client, "web")
	platform.ResetBuildStatus(&build.Status)
	require.NoError(t, w.client.Status().Update(context.Background(), &build))
	reconcileService(t, r, "web")
	buildSucceeds(t, w.client, "web", reportNextSHA, digestTwo)
	reconcileService(t, r, "web")
	serviceIsUp(t, w.client, "web", "shop-production")
	reconcileService(t, r, "web")
	require.Len(t, deploymentsOf(w.gh), 1)
	require.Equal(t, reportNextSHA, deploymentsOf(w.gh)[0].Body["ref"])
}

func TestAMissingDeploymentsPermissionNeverFailsTheDeploy(t *testing.T) {
	w := newGitHubWorld(t, worldOptions{objects: []client.Object{repoService()}})
	w.gh.forbid(http.MethodPost, "/repos/acme/web/deployments")
	r := w.appReconciler(t)
	reconcileService(t, r, "web")
	buildSucceeds(t, w.client, "web", reportSHA, digestOne)
	reconcileService(t, r, "web")
	serviceIsUp(t, w.client, "web", "shop-production")
	for range 4 {
		reconcileService(t, r, "web")
	}

	app := storedApp(t, w.client, "web")
	require.Equal(t, metav1.ConditionTrue, conditionOf(app.Status.Conditions, platform.ConditionReady), "the service is up, whatever GitHub said: %v", app.Status.Conditions)
	require.Len(t, deploymentsOf(w.gh), 1, "GitHub was asked once, not on every pass")
	condition := w.connectionCondition(t)
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Contains(t, condition.Message, "Deployments")
	require.Contains(t, condition.Message, "Accept new permissions")
}

func TestAProjectThatTurnedReportingOffGetsNoDeployments(t *testing.T) {
	w := newGitHubWorld(t, worldOptions{reportingOff: true, objects: []client.Object{repoService()}})
	githubTakesDeploys(w.gh)
	r := w.appReconciler(t)
	reconcileService(t, r, "web")
	buildSucceeds(t, w.client, "web", reportSHA, digestOne)
	reconcileService(t, r, "web")
	serviceIsUp(t, w.client, "web", "shop-production")
	reconcileService(t, r, "web")

	require.Empty(t, w.gh.all())
}

func TestOnlyServicesBuiltFromARepositoryAreReported(t *testing.T) {
	w := newGitHubWorld(t, worldOptions{objects: []client.Object{repoService(func(app *geassv1alpha1.GeassApp) {
		app.Spec.Source = geassv1alpha1.GeassAppSource{Image: &geassv1alpha1.GeassAppImageSource{Image: "nginx:alpine"}}
	})}})
	githubTakesDeploys(w.gh)
	r := w.appReconciler(t)

	reconcileService(t, r, "web")
	serviceIsUp(t, w.client, "web", "shop-production")
	reconcileService(t, r, "web")

	require.Empty(t, w.gh.all(), "an image has no commit to say was deployed")
}

func TestADeployThatFailsIsReportedAsAFailureOnTheDeploymentThatWasStarted(t *testing.T) {
	w := newGitHubWorld(t, worldOptions{objects: []client.Object{repoService()}})
	githubTakesDeploys(w.gh)
	r := w.appReconciler(t)
	reconcileService(t, r, "web")
	buildSucceeds(t, w.client, "web", reportSHA, digestOne)
	reconcileService(t, r, "web")
	require.Len(t, deploymentStatuses(w.gh, 4801), 1)

	// The service keeps failing to come up until the operator gives up on it.
	latest := storedApp(t, w.client, "web")
	for range platform.ReconcileFailureThreshold + 1 {
		_, err := r.setNotReady(context.Background(), &latest, "pods keep crashing")
		require.NoError(t, err)
		latest = storedApp(t, w.client, "web")
	}
	require.Equal(t, platform.ConditionReasonProvisionFailed, platform.ReadyConditionReason(latest.Status.Conditions), "the operator gave up")

	statuses := deploymentStatuses(w.gh, 4801)
	require.Len(t, statuses, 2, "in progress, then failed, on the same deployment")
	require.Equal(t, "failure", statuses[1].Body["state"])
	require.Equal(t, platform.ProvisionFailedUserMessage[:100], statuses[1].Body["description"].(string)[:100])
	require.Len(t, deploymentsOf(w.gh), 1)
}

// ---- pull requests ----

// pullRequestService is a copy of web made for pull request 12: its own environment, a branch, and a label.
func pullRequestService(name string, mutate ...func(*geassv1alpha1.GeassApp)) *geassv1alpha1.GeassApp {
	app := repoService(func(app *geassv1alpha1.GeassApp) {
		app.Name = name
		app.Labels = map[string]string{platform.LabelPullRequest: "12"}
		app.Annotations[platform.DisplayNameAnnotation] = strings.TrimSuffix(name, "-pr12")
		app.Spec.Environment = "pr-12"
		app.Spec.Ingress = geassv1alpha1.GeassAppIngressSpec{Host: name + ".example.com", TLSEnabled: true}
		app.Spec.Source.Git.Branch = "feature"
	})
	for _, m := range mutate {
		m(app)
	}
	return app
}

// openPullRequest tells the project that pull request 12 has an environment, as the dashboard does when it makes one.
func openPullRequest(t *testing.T, w *githubWorld) {
	t.Helper()
	var project geassv1alpha1.GeassProject
	require.NoError(t, w.client.Get(context.Background(), client.ObjectKey{Name: "shop", Namespace: platform.SystemNamespace}, &project))
	project.Status.PullRequests = []geassv1alpha1.GeassPullRequestEnvironment{{Number: 12, Repository: "acme/web", Branch: "feature", Environment: "pr-12", OpenedAt: metav1.Now()}}
	require.NoError(t, w.client.Status().Update(context.Background(), &project))
}

// pullRequestComments is the comments of pull request 12 on the fake GitHub: it keeps what is written to it.
type pullRequestComments struct {
	gh   *fakeGitHubAPI
	body string
	made bool
}

func serveComments(gh *fakeGitHubAPI) *pullRequestComments {
	c := &pullRequestComments{gh: gh}
	gh.mu.Lock()
	defer gh.mu.Unlock()
	gh.answers["GET /repos/acme/web/issues/12/comments"] = func(map[string]any) (int, any) {
		if !c.made {
			return http.StatusOK, []map[string]any{}
		}
		return http.StatusOK, []map[string]any{{"id": 900, "body": c.body, "user": map[string]string{"login": "geass[bot]", "type": "Bot"}}}
	}
	gh.answers["POST /repos/acme/web/issues/12/comments"] = func(body map[string]any) (int, any) {
		c.made, c.body = true, body["body"].(string)
		return http.StatusCreated, map[string]any{"id": 900}
	}
	gh.answers["PATCH /repos/acme/web/issues/comments/900"] = func(body map[string]any) (int, any) {
		c.body = body["body"].(string)
		return http.StatusOK, map[string]any{"id": 900}
	}
	return c
}

func TestAPullRequestEnvironmentIsADeploymentOfATransientEnvironment(t *testing.T) {
	w := newGitHubWorld(t, worldOptions{objects: []client.Object{pullRequestService("web-pr12")}})
	openPullRequest(t, w)
	githubTakesDeploys(w.gh)
	serveComments(w.gh)
	r := w.appReconciler(t)
	reconcileService(t, r, "web-pr12")
	buildSucceeds(t, w.client, "web-pr12", reportSHA, digestOne)
	reconcileService(t, r, "web-pr12")

	deployments := deploymentsOf(w.gh)
	require.Len(t, deployments, 1)
	require.Equal(t, "pr-12", deployments[0].Body["environment"], "the Geass environment's name")
	require.Equal(t, true, deployments[0].Body["transient_environment"])
	require.Equal(t, false, deployments[0].Body["production_environment"], "a preview is not production")
	require.Equal(t, "Geass: web", deployments[0].Body["description"], "named as the service is in the dashboard, without the suffix a pull request adds")
}

func TestAPullRequestHasOneCommentAndItIsUpdatedInPlaceAsTheServicesComeUp(t *testing.T) {
	w := newGitHubWorld(t, worldOptions{objects: []client.Object{pullRequestService("web-pr12"), pullRequestService("worker-pr12", func(app *geassv1alpha1.GeassApp) {
		app.Spec.Ingress = geassv1alpha1.GeassAppIngressSpec{}
	})}})
	openPullRequest(t, w)
	githubTakesDeploys(w.gh)
	comments := serveComments(w.gh)
	r := w.appReconciler(t)
	marker := githubapp.PullRequestMarker("shop")

	// Both services are building: the comment is written once, listing both.
	reconcileService(t, r, "web-pr12")
	reconcileService(t, r, "worker-pr12")
	require.True(t, comments.made, "the comment appears as soon as the environment has services")
	require.True(t, strings.HasPrefix(comments.body, marker+"\n"), "it starts with the hidden marker: %s", comments.body)
	require.Contains(t, comments.body, "| web | Building | - |")
	require.Contains(t, comments.body, "| worker | Building | - |")
	posts := len(w.gh.to(http.MethodPost, "/repos/acme/web/issues/12/comments"))
	require.Equal(t, 1, posts)

	// Nothing has changed, so nothing is sent however often the services are looked at.
	w.gh.reset()
	for range 3 {
		reconcileService(t, r, "web-pr12")
		reconcileService(t, r, "worker-pr12")
	}
	require.Empty(t, w.gh.all(), "a comment that says what it said is not written again, nor looked for")

	// web comes up: its row says so and has its address. The same comment is edited, and no second one is made.
	buildSucceeds(t, w.client, "web-pr12", reportSHA, digestOne)
	reconcileService(t, r, "web-pr12")
	serviceIsUp(t, w.client, "web-pr12", "shop-pr-12")
	reconcileService(t, r, "web-pr12")
	require.Contains(t, comments.body, "| web | Ready | <https://web-pr12.example.com/> |")
	require.Contains(t, comments.body, "| worker | Building | - |")
	require.Contains(t, comments.body, "Built from commit `9f3c2a1`.")
	require.Contains(t, comments.body, "[Open it in Geass]("+reportDashboard+"/projects/shop?environment=pr-12)")
	require.Len(t, w.gh.to(http.MethodPost, "/repos/acme/web/issues/12/comments"), 0, "never a second comment")
	require.NotEmpty(t, w.gh.to(http.MethodPatch, "/repos/acme/web/issues/comments/900"))
}

func TestOnceAPullRequestIsClosedTheOperatorDoesNotWriteItsCommentAgain(t *testing.T) {
	w := newGitHubWorld(t, worldOptions{objects: []client.Object{pullRequestService("web-pr12")}})
	githubTakesDeploys(w.gh)
	serveComments(w.gh)
	r := w.appReconciler(t)
	// The project does not list the pull request: it was closed, and the dashboard has said so on the comment.
	reconcileService(t, r, "web-pr12")
	buildSucceeds(t, w.client, "web-pr12", reportSHA, digestOne)
	reconcileService(t, r, "web-pr12")

	require.Empty(t, w.gh.to(http.MethodPost, "/repos/acme/web/issues/12/comments"))
	require.Empty(t, w.gh.to(http.MethodPatch, "/repos/acme/web/issues/comments/900"))
}

func TestAMissingPullRequestPermissionLeavesTheDeployAlone(t *testing.T) {
	w := newGitHubWorld(t, worldOptions{objects: []client.Object{pullRequestService("web-pr12")}})
	openPullRequest(t, w)
	githubTakesDeploys(w.gh)
	w.gh.forbid(http.MethodGet, "/repos/acme/web/issues/12/comments")
	r := w.appReconciler(t)
	reconcileService(t, r, "web-pr12")
	buildSucceeds(t, w.client, "web-pr12", reportSHA, digestOne)
	reconcileService(t, r, "web-pr12")
	serviceIsUp(t, w.client, "web-pr12", "shop-pr-12")
	for range 3 {
		reconcileService(t, r, "web-pr12")
	}

	app := storedApp(t, w.client, "web-pr12")
	require.Equal(t, metav1.ConditionTrue, conditionOf(app.Status.Conditions, platform.ConditionReady))
	require.Len(t, w.gh.to(http.MethodGet, "/repos/acme/web/issues/12/comments"), 1, "asked once, and not again while the permission is missing")
	require.Len(t, deploymentsOf(w.gh), 1, "deployments are a different permission, and still work")
	condition := w.connectionCondition(t)
	require.NotNil(t, condition)
	require.Contains(t, condition.Message, "Pull requests")
}

func TestPreviewStateSaysWhatAServiceIsDoing(t *testing.T) {
	ready := []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue}}
	failed := []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionFalse, Reason: platform.ConditionReasonProvisionFailed}}
	waiting := []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionFalse, Reason: "Waiting"}}
	service := func(conditions []metav1.Condition, deploy bool) *geassv1alpha1.GeassApp {
		app := repoService()
		app.Status.Conditions, app.Spec.Deploy.Enabled = conditions, deploy
		return app
	}
	build := func(phase geassv1alpha1.GeassBuildPhase) *geassv1alpha1.GeassBuild {
		return &geassv1alpha1.GeassBuild{Status: geassv1alpha1.GeassBuildStatus{Phase: phase}}
	}
	for name, tt := range map[string]struct {
		app   *geassv1alpha1.GeassApp
		build *geassv1alpha1.GeassBuild
		want  string
	}{
		"ready":               {service(ready, true), build(geassv1alpha1.GeassBuildSucceeded), githubapp.PreviewReady},
		"gave up":             {service(failed, true), build(geassv1alpha1.GeassBuildSucceeded), githubapp.PreviewFailed},
		"building":            {service(waiting, true), build(geassv1alpha1.GeassBuildRunning), githubapp.PreviewBuilding},
		"waiting for CI":      {service(waiting, true), build(geassv1alpha1.GeassBuildWaitingForCI), githubapp.PreviewBuilding},
		"build not started":   {service(waiting, true), build(geassv1alpha1.GeassBuildPending), githubapp.PreviewBuilding},
		"build failed":        {service(waiting, true), build(geassv1alpha1.GeassBuildFailed), githubapp.PreviewBuildFailed},
		"build cancelled":     {service(waiting, true), build(geassv1alpha1.GeassBuildCancelled), githubapp.PreviewBuildFailed},
		"built, now rolling":  {service(waiting, true), build(geassv1alpha1.GeassBuildSucceeded), githubapp.PreviewDeploying},
		"no build, deploying": {service(waiting, true), nil, githubapp.PreviewDeploying},
		"a draft":             {service(nil, false), nil, githubapp.PreviewQueued},
	} {
		t.Run(name, func(t *testing.T) { require.Equal(t, tt.want, previewState(tt.app, tt.build)) })
	}
}

func TestTheDeployRecordRoundTripsWithATimestampThatHasColons(t *testing.T) {
	record := deployRecord{sha: reportSHA, id: 4801, state: "in_progress", revision: "2026-10-14T12:00:00.5Z"}

	parsed, ok := parseDeployRecord(record.String())

	require.True(t, ok)
	require.Equal(t, record, parsed)
	for _, bad := range []string{"", "x", "a:b:c:d", ":1:in_progress:r", reportSHA + ":-1:success:r", reportSHA + ":1::r"} {
		_, ok := parseDeployRecord(bad)
		require.False(t, ok, bad)
	}
	empty, ok := parseDeployRecord(reportSHA + ":0:success:")
	require.True(t, ok, "a service deployed before revisions were recorded has none")
	require.Equal(t, "", empty.revision)
}

func TestWhichStatesOfADeployMayFollowWhich(t *testing.T) {
	for from, tos := range map[string]map[string]bool{
		"created":     {"in_progress": true, "success": true, "failure": true},
		"in_progress": {"in_progress": false, "success": true, "failure": true},
		"failure":     {"in_progress": true, "success": true, "failure": false},
		"success":     {"in_progress": false, "success": false, "failure": false},
	} {
		for to, want := range tos {
			require.Equal(t, want, mayMoveTo(from, to), "%s to %s", from, to)
		}
	}
}

// A deploy state that GitHub could not take is still owed. Recording it as sent would leave the GitHub Deployment
// showing "in progress" for ever, since a success is never reported twice.
func TestADeployStateGitHubCouldNotTakeIsNotRecordedAsSentAndIsSentLater(t *testing.T) {
	w := newGitHubWorld(t, worldOptions{objects: []client.Object{repoService()}})
	githubTakesDeploys(w.gh)
	r := w.appReconciler(t)
	reconcileService(t, r, "web")
	buildSucceeds(t, w.client, "web", reportSHA, digestOne)
	reconcileService(t, r, "web") // the deployment is made and reported as in progress
	require.Len(t, deploymentStatuses(w.gh, 4801), 1)

	// GitHub has an outage as the service comes up.
	w.gh.answer(http.MethodPost, "/repos/acme/web/deployments/4801/statuses", http.StatusBadGateway, map[string]string{"message": "Bad Gateway"})
	serviceIsUp(t, w.client, "web", "shop-production")
	var last ctrl.Result
	for range githubapp.MaxAttempts + 1 {
		var err error
		last, err = r.Reconcile(context.Background(), reconcileRequest(&geassv1alpha1.GeassApp{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: platform.SystemNamespace}}))
		require.NoError(t, err)
		w.clock.advance(githubapp.RetrySpacing)
	}

	require.Contains(t, storedApp(t, w.client, "web").Annotations[platform.AppGitHubDeploymentAnnotation], ":in_progress:",
		"the success never got through, so it is not on record as sent")
	require.GreaterOrEqual(t, last.RequeueAfter, githubapp.RefusedBackoff, "and the service is looked at again after a long pause")

	// GitHub is back.
	githubTakesDeploys(w.gh)
	reconcileService(t, r, "web")

	statuses := deploymentStatuses(w.gh, 4801)
	require.Equal(t, "success", statuses[len(statuses)-1].Body["state"], "the state that was owed is sent")
	require.Contains(t, storedApp(t, w.client, "web").Annotations[platform.AppGitHubDeploymentAnnotation], ":success:")
}
