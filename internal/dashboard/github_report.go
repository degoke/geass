package dashboard

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/githubapp"
	"github.com/degoke/geass/pkg/platform"
)

// What the operator reports to GitHub is decided in its controllers (builds, deploys, the pull request comment).
// The dashboard handles the one thing only it sees happen: a pull request's environment being removed, which it
// tells GitHub about, and the switch that turns reporting off.

func (s *Server) githubReportResolver() *githubapp.Resolver {
	return &githubapp.Resolver{Client: s.Client, HTTP: s.HTTPClient, Namespace: systemNamespace}
}

func (s *Server) githubReportGuard() *githubapp.Guard {
	if s.GitHubReports != nil {
		return s.GitHubReports
	}
	return githubapp.DefaultGuard()
}

// reportInBackground runs a report on its own, with the request's logger and values but not its deadline: the
// request is a webhook that GitHub waits ten seconds for, and the report is several requests to GitHub.
func (s *Server) reportInBackground(ctx context.Context, report func(context.Context)) {
	detached := context.WithoutCancel(ctx)
	s.reports.Go(func() {
		reportCtx, cancel := context.WithTimeout(detached, time.Minute)
		defer cancel()
		report(reportCtx)
	})
}

// pullRequestDeployment is a GitHub Deployment Geass made for a service of a pull request's environment.
type pullRequestDeployment struct {
	service    string
	repository string
	connection string
	id         int64
}

// pullRequestDeployments finds the GitHub Deployments of a pull request's environment, which have to be read
// before the environment is removed: the services carry the ids.
func (s *Server) pullRequestDeployments(ctx context.Context, project, environment string, number int32) []pullRequestDeployment {
	var apps geassv1alpha1.GeassAppList
	if err := s.Client.List(ctx, &apps, client.InNamespace(systemNamespace), client.MatchingLabels{labelPullRequest: strconv.Itoa(int(number))}); err != nil {
		return nil
	}
	var found []pullRequestDeployment
	for i := range apps.Items {
		app := &apps.Items[i]
		git := app.Spec.Source.Git
		if git == nil || app.Spec.Project != project || string(app.Spec.Environment) != environment {
			continue
		}
		_, id, _, _, ok := platform.ParseGitHubDeploymentRecord(app.Annotations[platform.AppGitHubDeploymentAnnotation])
		if !ok || id <= 0 {
			continue
		}
		found = append(found, pullRequestDeployment{service: platform.ResourceDisplayName(app.ObjectMeta), repository: git.Repository, connection: git.ConnectionRef.Name, id: id})
	}
	return found
}

// reportPullRequestRemoved tells GitHub that a pull request's environment is gone: its GitHub Deployments are
// marked inactive, and the comment Geass keeps on the pull request says the environment was removed. It is
// called after the environment has been removed, and nothing it meets fails the removal.
func (s *Server) reportPullRequestRemoved(ctx context.Context, project, repo string, number int32, environment string, deployments []pullRequestDeployment) {
	resolver := s.githubReportResolver()
	guard := s.githubReportGuard()
	for _, deployment := range deployments {
		resolver.Do(ctx, guard, githubapp.Report{
			Project:    project,
			Connection: deployment.connection,
			Repository: deployment.repository,
			Capability: githubapp.CapabilityDeployments,
			Key:        "inactive/" + project + "/" + environment + "/" + strconv.FormatInt(deployment.id, 10),
			Send: func(ctx context.Context, target *githubapp.Target) error {
				return target.CreateDeploymentStatus(ctx, deployment.id, githubapp.DeploymentStatus{
					State:       githubapp.DeploymentInactive,
					Description: "The pull request environment was removed",
				})
			},
		})
	}
	// What the operator last wrote is forgotten, so that a pull request that is opened again gets its comment
	// written again and not skipped for saying what it said before.
	commentKey := repo + "#" + strconv.Itoa(int(number)) + "|" + project
	guard.ForgetComment(commentKey)
	body := githubapp.RenderPreviewComment(githubapp.Preview{Project: project, Environment: environment, Removed: true})
	result := resolver.Do(ctx, guard, githubapp.Report{
		Project:    project,
		Repository: repo,
		Capability: githubapp.CapabilityComments,
		Key:        "comment/" + commentKey + "/removed",
		Send: func(ctx context.Context, target *githubapp.Target) error {
			// A pull request that never had a comment (reporting was off, or nothing had been deployed) does not get one
			// that says its environment is gone.
			_, err := target.UpsertComment(ctx, int(number), githubapp.PullRequestMarker(project), body, false)
			return err
		},
	})
	if result.Err != nil && result.Outcome != githubapp.Skipped {
		ctrllog.FromContext(ctx).V(1).Info("Could not update the pull request comment", "project", project, "repository", repo, "number", number, "outcome", string(result.Outcome))
	}
}

// handleProjectGitHubReporting turns reporting to GitHub on or off for a project.
func (s *Server) handleProjectGitHubReporting(w http.ResponseWriter, r *http.Request, project string) {
	fallback := workspacePanelURL(project, "", "settings", "integrations")
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	enabled := r.FormValue("enabled") == "on" || r.FormValue("enabled") == boolTrue
	err := s.mutateProject(r.Context(), project, func(p *geassv1alpha1.GeassProject) error {
		p.Spec.GitHubReporting = &enabled
		return nil
	})
	s.replyWebhookMutation(w, r, fallback, err, nil, map[string]any{"ok": true, "enabled": enabled})
}
