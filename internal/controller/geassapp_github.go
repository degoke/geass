package controller

import (
	"context"
	"slices"
	"strconv"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/githubapp"
	"github.com/degoke/geass/pkg/platform"
)

func (r *GeassAppReconciler) githubResolver() *githubapp.Resolver {
	return &githubapp.Resolver{Client: r.Client, HTTP: r.HTTPClient, Namespace: platform.SystemNamespace}
}

func (r *GeassAppReconciler) guard() *githubapp.Guard {
	if r.GitHubGuard != nil {
		return r.GitHubGuard
	}
	return githubapp.DefaultGuard()
}

// ---- deploys ----

// The states of a deploy that Geass records on a service besides the ones it sends to GitHub.
const (
	// deployCreated is a GitHub Deployment that was made and has no status yet. It is recorded the moment it exists,
	// so that a report that fails after it was made adds its status to it when it is tried again, and does not make
	// a second deployment.
	deployCreated = "created"
)

// deployRecord is what the annotation on a service says of the last deploy it reported to GitHub: the commit,
// the GitHub Deployment's id (0 for a deploy that was noticed and never sent), the state last sent, and the
// service's deploy revision. A commit and a revision make a deploy: a push brings a new commit, a deploy from the
// dashboard, the API or the command line brings a new revision.
type deployRecord struct {
	sha      string
	id       int64
	state    string
	revision string
}

func (d deployRecord) String() string {
	return platform.FormatGitHubDeploymentRecord(d.sha, d.id, d.state, d.revision)
}

func parseDeployRecord(value string) (deployRecord, bool) {
	sha, id, state, revision, ok := platform.ParseGitHubDeploymentRecord(value)
	if !ok {
		return deployRecord{}, false
	}
	return deployRecord{sha: sha, id: id, state: state, revision: revision}, true
}

// sameDeploy reports whether the record is for this commit and revision, which is what makes a state a change to
// the same GitHub Deployment and not the start of another.
func (d deployRecord) sameDeploy(sha, revision string) bool {
	return d.sha == sha && d.revision == revision
}

// mayMoveTo reports whether a deploy that has been reported in one state may be reported in another. A deploy
// that succeeded is over: a service that restarts, scales or loses a node later is not deploying, and one that
// fails to reconcile months on has not failed to deploy. A failed deploy may start again or recover.
func mayMoveTo(from, to string) bool {
	if from == to {
		return false
	}
	switch from {
	case githubapp.DeploymentSuccess:
		return false
	case githubapp.DeploymentInProgress:
		return to == githubapp.DeploymentSuccess || to == githubapp.DeploymentFailure
	default:
		return true
	}
}

// reportDeploy tells GitHub how the deploy of a repository service is going: a GitHub Deployment for the commit it
// runs, in the environment it runs in, with a status that follows it from in progress to success or failure and
// the service's address as the environment URL. It is called wherever the controller learns something about a
// deploy and does nothing unless that is news: what was last reported is recorded on the service.
//
// wasReady says the service was already serving when the pass began. A service that has no record and is serving
// when it is first looked at was deployed before Geass reported anything, and is given a record without a word to
// GitHub, so that upgrading does not announce every service in the cluster as freshly deployed.
func (r *GeassAppReconciler) reportDeploy(ctx context.Context, app *geassv1alpha1.GeassApp, state, message string, wasReady bool) {
	if app.Spec.Source.Git == nil || !app.Spec.Deploy.Enabled || app.Status.ActiveBuild == "" || !app.DeletionTimestamp.IsZero() {
		return
	}
	log := logf.FromContext(ctx)
	record, hasRecord := parseDeployRecord(app.Annotations[platform.AppGitHubDeploymentAnnotation])
	revision := app.Annotations[platform.AppDeployRevisionAnnotation]

	// What the service runs is what its active build built, but only while that build is not being run again: a
	// build that has started over has the next commit (or none yet), and no say in what is running now.
	var build geassv1alpha1.GeassBuild
	sha, repository, connection := "", "", ""
	if err := r.Get(ctx, client.ObjectKey{Name: app.Status.ActiveBuild, Namespace: app.Namespace}, &build); err == nil &&
		build.Status.Phase == geassv1alpha1.GeassBuildSucceeded && build.Status.SourceRevision != "" {
		sha, repository = build.Status.SourceRevision, build.Spec.Repository
		if build.Spec.ConnectionRef != nil {
			connection = build.Spec.ConnectionRef.Name
		}
	} else if err != nil && !apierrors.IsNotFound(err) {
		log.Error(err, "Could not read the build a service runs to report its deploy", "app", app.Name, "build", app.Status.ActiveBuild)
		return
	} else if hasRecord && state == githubapp.DeploymentFailure {
		// The deploy that failed is the one that was reported as under way, whatever the build is doing now.
		sha, repository, connection = record.sha, app.Spec.Source.Git.Repository, app.Spec.Source.Git.ConnectionRef.Name
	}
	if sha == "" {
		return
	}

	if !hasRecord && state == githubapp.DeploymentSuccess && wasReady {
		// Deployed before this operator reported deploys.
		if err := recordAnnotation(ctx, r.Client, app, platform.AppGitHubDeploymentAnnotation, deployRecord{sha: sha, state: githubapp.DeploymentSuccess, revision: revision}.String()); err != nil {
			log.Error(err, "Could not record the deploy of a service", "app", app.Name)
		}
		return
	}
	var id int64
	if hasRecord && record.sameDeploy(sha, revision) {
		if !mayMoveTo(record.state, state) {
			return
		}
		id = record.id
	}
	// The deployment this process already made for this deploy, when the record on the service has not reached
	// the cache yet: asking GitHub for another would make a second deployment of the same deploy.
	deployKey := app.Namespace + "/" + app.Name + "/" + sha + "/" + revision
	if made, ok := r.guard().Deployment(deployKey); ok && id == 0 {
		id = made
	}

	key := "deploy/" + deployKey + "/" + state
	result := r.githubResolver().Do(ctx, r.guard(), githubapp.Report{
		Project:    app.Spec.Project,
		Connection: connection,
		Repository: repository,
		Capability: githubapp.CapabilityDeployments,
		Key:        key,
		Send: func(ctx context.Context, target *githubapp.Target) error {
			name := platform.ResourceDisplayName(app.ObjectMeta)
			if id == 0 {
				transient := app.Labels[platform.LabelPullRequest] != ""
				deployment := githubapp.Deployment{
					Ref:         sha,
					Environment: string(app.Spec.Environment),
					Description: "Geass: " + name,
					Transient:   transient,
				}
				if transient {
					notProduction := false
					deployment.Production = &notProduction
				}
				created, err := target.CreateDeployment(ctx, deployment)
				if err != nil {
					return err
				}
				id = created
				r.guard().RememberDeployment(deployKey, created)
				if err := recordAnnotation(ctx, r.Client, app, platform.AppGitHubDeploymentAnnotation, deployRecord{sha: sha, id: id, state: deployCreated, revision: revision}.String()); err != nil {
					log.Error(err, "Could not record the GitHub deployment made for a service", "app", app.Name)
				}
			}
			status := githubapp.DeploymentStatus{
				State:          state,
				EnvironmentURL: appPublicURL(app),
				LogURL:         githubapp.DashboardServiceURL(target.DashboardURL, app.Spec.Project, string(app.Spec.Environment), app.Name),
			}
			switch state {
			case githubapp.DeploymentInProgress:
				status.Description = "Deploying " + name
			case githubapp.DeploymentSuccess:
				status.Description = "Deployed " + name
			default:
				status.Description = firstNonEmpty(message, "The deploy of "+name+" failed")
			}
			return target.CreateDeploymentStatus(ctx, id, status)
		},
	})
	if result.Outcome.Done() {
		if err := recordAnnotation(ctx, r.Client, app, platform.AppGitHubDeploymentAnnotation, deployRecord{sha: sha, id: id, state: state, revision: revision}.String()); err != nil {
			log.Error(err, "Could not record the deploy reported to GitHub", "app", app.Name)
		}
	}
	switch result.Outcome {
	case githubapp.Sent:
		log.V(1).Info("Reported the deploy to GitHub", "app", app.Name, "state", state, "commit", sha, "deployment", id)
	default:
		// Failed, refused, held back or given up: the report is still owed, and nothing was recorded as reported, so
		// it is sent when GitHub can take it. RetryAfter says when to try.
		r.retries.want(client.ObjectKeyFromObject(app), result.RetryAfter)
	}
}

// reportToGitHub is everything the controller tells GitHub when it learns how a deploy is going: the deploy
// itself, and the pull request comment that lists it when the service is in a pull request's environment.
func (r *GeassAppReconciler) reportToGitHub(ctx context.Context, app *geassv1alpha1.GeassApp, state, message string, wasReady bool) {
	r.dispatchReport(ctx, app, func(ctx context.Context, app *geassv1alpha1.GeassApp) {
		r.reportDeploy(ctx, app, state, message, wasReady)
		r.syncPullRequestComment(ctx, app)
	})
}

// dispatchReport runs a report for a service off the reconcile, on a copy of the service as it is now: what a pass
// has to say to GitHub is not worth holding the controller's worker for, whatever GitHub is doing.
func (r *GeassAppReconciler) dispatchReport(ctx context.Context, app *geassv1alpha1.GeassApp, run func(context.Context, *geassv1alpha1.GeassApp)) {
	snapshot := app.DeepCopy()
	r.reports.dispatch(ctx, r.AsyncReports, client.ObjectKeyFromObject(app), &r.retries, func(ctx context.Context) { run(ctx, snapshot) })
}

// ---- pull requests ----

// previewState is how a service of a pull request's environment looks in the comment.
func previewState(app *geassv1alpha1.GeassApp, build *geassv1alpha1.GeassBuild) string {
	switch {
	case platform.IsConditionTrue(app.Status.Conditions, platform.ConditionReady):
		return githubapp.PreviewReady
	case platform.ReadyConditionReason(app.Status.Conditions) == platform.ConditionReasonProvisionFailed:
		return githubapp.PreviewFailed
	case build != nil:
		switch build.Status.Phase {
		case geassv1alpha1.GeassBuildFailed, geassv1alpha1.GeassBuildCancelled:
			return githubapp.PreviewBuildFailed
		case geassv1alpha1.GeassBuildSucceeded:
			return githubapp.PreviewDeploying
		default:
			return githubapp.PreviewBuilding
		}
	case app.Spec.Deploy.Enabled:
		return githubapp.PreviewDeploying
	default:
		return githubapp.PreviewQueued
	}
}

// syncPullRequestComment keeps the one comment a pull request has from Geass up to date: the preview addresses of
// the services of its environment, and how each is doing. It does nothing for a service that is not in a pull
// request's environment. Every service of the environment calls it as it changes, and each writes the whole
// comment from what the cluster says, so that the comment ends up right whichever of them wrote last. What was
// last written is kept in memory, so a pass with nothing new to say asks GitHub nothing.
func (r *GeassAppReconciler) syncPullRequestComment(ctx context.Context, app *geassv1alpha1.GeassApp) {
	number, err := strconv.Atoi(app.Labels[platform.LabelPullRequest])
	if err != nil || number <= 0 || !app.DeletionTimestamp.IsZero() {
		return
	}
	log := logf.FromContext(ctx)
	var project geassv1alpha1.GeassProject
	if err := r.Get(ctx, client.ObjectKey{Name: app.Spec.Project, Namespace: app.Namespace}, &project); err != nil {
		return
	}
	// The environment belongs to a pull request while the project lists it as open. Once the pull request closes,
	// the dashboard says so in the comment, and nothing here writes over that.
	var entry *geassv1alpha1.GeassPullRequestEnvironment
	for i := range project.Status.PullRequests {
		candidate := &project.Status.PullRequests[i]
		if candidate.Environment == string(app.Spec.Environment) && int(candidate.Number) == number {
			entry = candidate
			break
		}
	}
	if entry == nil || !platform.GitHubReportingEnabled(&project) {
		return
	}

	var apps geassv1alpha1.GeassAppList
	if err := r.List(ctx, &apps, client.InNamespace(app.Namespace), client.MatchingLabels{platform.LabelPullRequest: strconv.Itoa(number)}); err != nil {
		log.Error(err, "Could not list the services of a pull request environment", "environment", app.Spec.Environment)
		return
	}
	preview := githubapp.Preview{Project: app.Spec.Project, Environment: string(app.Spec.Environment)}
	for i := range apps.Items {
		member := &apps.Items[i]
		if member.Name == app.Name {
			// What this pass knows of the service is newer than the cache's copy.
			member = app
		}
		if member.Spec.Project != app.Spec.Project || member.Spec.Environment != app.Spec.Environment || !member.DeletionTimestamp.IsZero() {
			continue
		}
		var build *geassv1alpha1.GeassBuild
		if member.Spec.Source.Git != nil && member.Status.CurrentBuild != "" {
			found := &geassv1alpha1.GeassBuild{}
			if r.Get(ctx, client.ObjectKey{Name: member.Status.CurrentBuild, Namespace: member.Namespace}, found) == nil {
				build = found
			}
		}
		service := githubapp.PreviewService{
			Name:  platform.ResourceDisplayName(member.ObjectMeta),
			State: previewState(member, build),
			URL:   appPublicURL(member),
		}
		if build != nil && build.Status.Phase == geassv1alpha1.GeassBuildSucceeded {
			service.Commit = build.Status.SourceRevision
		}
		if service.State != githubapp.PreviewReady {
			// A service that is not up yet has an address that does not answer yet.
			service.URL = ""
		}
		preview.Services = append(preview.Services, service)
	}
	slices.SortFunc(preview.Services, func(a, b githubapp.PreviewService) int { return strings.Compare(a.Name, b.Name) })

	var connection string
	if git := app.Spec.Source.Git; git != nil {
		connection = git.ConnectionRef.Name
	}
	// The dashboard's address is read when the comment is rendered, so a link is there from the first write.
	resolver := r.githubResolver()
	if cfg, err := (&githubapp.TokenResolver{Client: r.Client, HTTP: r.HTTPClient, Namespace: platform.SystemNamespace}).LoadPlatformConfig(ctx); err == nil {
		preview.DashboardURL = cfg.PublicBaseURL
	}
	body := githubapp.RenderPreviewComment(preview)
	commentKey := entry.Repository + "#" + strconv.Itoa(number) + "|" + app.Spec.Project
	fingerprint := githubapp.Fingerprint(body)
	guard := r.guard()
	if guard.CommentIs(commentKey, fingerprint) {
		return
	}
	marker := githubapp.PullRequestMarker(app.Spec.Project)
	result := resolver.Do(ctx, guard, githubapp.Report{
		Project:    app.Spec.Project,
		Connection: connection,
		Repository: entry.Repository,
		Capability: githubapp.CapabilityComments,
		Key:        "comment/" + commentKey + "/" + fingerprint,
		Send: func(ctx context.Context, target *githubapp.Target) error {
			_, err := target.UpsertComment(ctx, number, marker, body, true)
			return err
		},
	})
	if result.Outcome.Done() {
		guard.RememberComment(commentKey, fingerprint)
		return
	}
	r.retries.want(client.ObjectKeyFromObject(app), result.RetryAfter)
}
