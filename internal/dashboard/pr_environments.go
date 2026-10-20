package dashboard

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const (
	defaultMaxPREnvironments = 5
	labelPullRequest         = platform.LabelPullRequest
)

// githubPullRequestPayload is the part of a GitHub pull_request event Geass reads.
type githubPullRequestPayload struct {
	Action      string          `json:"action"`
	Number      int32           `json:"number"`
	PullRequest pullRequestBody `json:"pull_request"`
	Repository  struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
}

type pullRequestBody struct {
	HTMLURL string `json:"html_url"`
	Head    struct {
		Ref  string `json:"ref"`
		SHA  string `json:"sha"`
		Repo struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"head"`
	Base struct {
		Ref  string `json:"ref"`
		Repo struct {
			FullName string `json:"full_name"`
		} `json:"repo"`
	} `json:"base"`
}

// pullRequestResult tells the webhook sender what happened, for the response body and the tests.
type pullRequestResult struct {
	Project string `json:"project"`
	Action  string `json:"action"`
	Env     string `json:"environment,omitempty"`
	Created int    `json:"created,omitempty"`
	Removed int    `json:"removed,omitempty"`
	Note    string `json:"note,omitempty"`
}

// handlePullRequestEvent creates, keeps, or removes pull request environments for every project that
// has them enabled and builds this repository in its base environment.
func (s *Server) handlePullRequestEvent(ctx context.Context, payload githubPullRequestPayload) ([]pullRequestResult, error) {
	repo := payload.Repository.FullName
	pr := payload.PullRequest
	if repo == "" || payload.Number <= 0 {
		return nil, nil
	}
	logger := ctrllog.FromContext(ctx)

	var projects geassv1alpha1.GeassProjectList
	if err := s.Client.List(ctx, &projects, client.InNamespace(systemNamespace)); err != nil {
		return nil, err
	}
	var results []pullRequestResult
	requireInstallation := s.tenancyOn(ctx)
	for i := range projects.Items {
		project := &projects.Items[i]
		config := project.Spec.PREnvironments
		if config == nil || !config.Enabled {
			// Turning the feature off must not strand environments that already exist: their
			// pull requests still have to clean them up when they close.
			if _, open := findPullRequest(project, repo, payload.Number); payload.Action != "closed" || !open {
				continue
			}
		}
		if requireInstallation || payload.Installation.ID > 0 {
			// As for a push: with organizations on, only the installation says whose a repository is.
			matches, err := s.projectMatchesInstallation(ctx, project, payload.Installation.ID)
			if err != nil || !matches || payload.Installation.ID <= 0 {
				continue
			}
		}
		switch payload.Action {
		case "closed":
			result, err := s.closePullRequestEnvironment(ctx, project.Name, repo, payload.Number)
			if err != nil {
				return results, err
			}
			if result.Env != "" {
				results = append(results, result)
			}
		case "opened", "reopened", "ready_for_review", "synchronize":
			// Code from a fork must never run inside the cluster with this project's secrets.
			if pr.Head.Repo.FullName != repo || pr.Base.Repo.FullName != repo {
				logger.Info("Ignored pull request from a fork", "project", project.Name, "repository", repo, "number", payload.Number)
				continue
			}
			result, err := s.ensurePullRequestEnvironment(ctx, project.Name, repo, payload.Number, pr)
			if err != nil {
				return results, err
			}
			if result.Env != "" || result.Note != "" {
				results = append(results, result)
			}
		}
	}
	return results, nil
}

func (s *Server) projectMatchesInstallation(ctx context.Context, project *geassv1alpha1.GeassProject, installationID int64) (bool, error) {
	probe := &geassv1alpha1.GeassApp{Spec: geassv1alpha1.GeassAppSpec{Project: project.Name}}
	return s.appMatchesInstallation(ctx, probe, installationID)
}

func baseEnvironmentOf(project *geassv1alpha1.GeassProject) string {
	if config := project.Spec.PREnvironments; config != nil && config.BaseEnvironment != "" {
		return config.BaseEnvironment
	}
	if len(project.Spec.Environments) > 0 {
		return project.Spec.Environments[0]
	}
	return ""
}

func maxOpenPREnvironments(project *geassv1alpha1.GeassProject) int {
	if config := project.Spec.PREnvironments; config != nil && config.MaxOpen > 0 {
		return int(config.MaxOpen)
	}
	return defaultMaxPREnvironments
}

func findPullRequest(project *geassv1alpha1.GeassProject, repo string, number int32) (geassv1alpha1.GeassPullRequestEnvironment, bool) {
	for _, entry := range project.Status.PullRequests {
		if entry.Repository == repo && entry.Number == number {
			return entry, true
		}
	}
	return geassv1alpha1.GeassPullRequestEnvironment{}, false
}

// pullRequestEnvironmentName is pr-<number>, with the repository name added when two repositories
// in one project have a pull request with the same number.
func pullRequestEnvironmentName(project *geassv1alpha1.GeassProject, repo string, number int32) string {
	name := fmt.Sprintf("pr-%d", number)
	for _, entry := range project.Status.PullRequests {
		if entry.Environment == name && entry.Repository != repo {
			short := slugify(repo[strings.LastIndex(repo, "/")+1:])
			name = fmt.Sprintf("pr-%d-%s", number, short)
			if len(name) > 63 {
				name = strings.TrimRight(name[:63], "-")
			}
			break
		}
	}
	return name
}

// cloneName keeps a resource's name recognisable and unique per pull request, within 63 characters.
func cloneName(name string, number int32) string {
	suffix := fmt.Sprintf("-pr%d", number)
	if len(name)+len(suffix) > 63 {
		name = strings.TrimRight(name[:63-len(suffix)], "-")
	}
	return name + suffix
}

func (s *Server) updateProjectStatus(ctx context.Context, name string, mutate func(status *geassv1alpha1.GeassProjectStatus)) error {
	return retry.RetryOnConflict(writeRetry, func() error {
		var project geassv1alpha1.GeassProject
		if err := s.Client.Get(ctx, client.ObjectKey{Name: name, Namespace: systemNamespace}, &project); err != nil {
			return err
		}
		mutate(&project.Status)
		return s.Client.Status().Update(ctx, &project)
	})
}

// ensurePullRequestEnvironment makes sure a pull request has an environment that mirrors the base
// environment. It is safe to repeat: existing pieces are kept and missing ones are created.
func (s *Server) ensurePullRequestEnvironment(ctx context.Context, projectName, repo string, number int32, pr pullRequestBody) (pullRequestResult, error) {
	result := pullRequestResult{Project: projectName, Action: "ensure"}
	var project geassv1alpha1.GeassProject
	if err := s.Client.Get(ctx, client.ObjectKey{Name: projectName, Namespace: systemNamespace}, &project); err != nil {
		return result, err
	}
	base := baseEnvironmentOf(&project)
	if base == "" || !slices.Contains(project.Spec.Environments, base) {
		result.Note = "the base environment does not exist"
		return result, nil
	}
	apps, databases, err := s.baseEnvironmentContents(ctx, projectName, base)
	if err != nil {
		return result, err
	}
	// Only pull requests for a repository this environment builds, into the branch it builds.
	if !buildsBranch(apps, repo, pr.Base.Ref) {
		return result, nil
	}

	env := ""
	if entry, ok := findPullRequest(&project, repo, number); ok {
		env = entry.Environment
	} else {
		if len(project.Status.PullRequests) >= maxOpenPREnvironments(&project) {
			result.Note = fmt.Sprintf("the limit of %d pull request environments is reached", maxOpenPREnvironments(&project))
			return result, nil
		}
		env = pullRequestEnvironmentName(&project, repo, number)
		if slices.Contains(project.Spec.Environments, env) {
			result.Note = fmt.Sprintf("an environment named %s already exists", env)
			return result, nil
		}
		if err := s.createPullRequestEnvironment(ctx, &project, base, env, repo, number, pr); err != nil {
			return result, err
		}
	}
	result.Env = env

	created, err := s.cloneEnvironmentResources(ctx, &project, base, env, repo, number, pr, apps, databases)
	result.Created = created
	return result, err
}

func (s *Server) baseEnvironmentContents(ctx context.Context, projectName, base string) ([]geassv1alpha1.GeassApp, []geassv1alpha1.GeassDatabase, error) {
	var apps geassv1alpha1.GeassAppList
	if err := s.Client.List(ctx, &apps, client.InNamespace(systemNamespace)); err != nil {
		return nil, nil, err
	}
	var databases geassv1alpha1.GeassDatabaseList
	if err := s.Client.List(ctx, &databases, client.InNamespace(systemNamespace)); err != nil {
		return nil, nil, err
	}
	var baseApps []geassv1alpha1.GeassApp
	for _, app := range apps.Items {
		if app.Spec.Project == projectName && string(app.Spec.Environment) == base && app.DeletionTimestamp.IsZero() {
			baseApps = append(baseApps, app)
		}
	}
	var baseDatabases []geassv1alpha1.GeassDatabase
	for _, db := range databases.Items {
		if db.Spec.Project == projectName && string(db.Spec.Environment) == base && db.DeletionTimestamp.IsZero() &&
			db.Spec.Placement != geassv1alpha1.DatabasePlacementExternal {
			baseDatabases = append(baseDatabases, db)
		}
	}
	return baseApps, baseDatabases, nil
}

func buildsBranch(apps []geassv1alpha1.GeassApp, repo, branch string) bool {
	for _, app := range apps {
		if git := app.Spec.Source.Git; git != nil && git.Repository == repo && (branch == "" || git.Branch == branch) {
			return true
		}
	}
	return false
}

// createPullRequestEnvironment adds the environment to the project, copies the base environment's
// shared variables into it, and records the pull request in the project status.
func (s *Server) createPullRequestEnvironment(ctx context.Context, project *geassv1alpha1.GeassProject, base, env, repo string, number int32, pr pullRequestBody) error {
	secretName := project.Name + "-shared-secrets"
	// Copy secret values first so the variables never point at a missing key.
	var source corev1.Secret
	haveSource := s.Client.Get(asSystem(ctx), client.ObjectKey{Name: secretName, Namespace: systemNamespace}, &source) == nil
	for _, variable := range project.Spec.SharedVariables {
		if variable.Environment != base || variable.SecretRef == nil || !haveSource {
			continue
		}
		if value, ok := source.Data[variable.SecretRef.Key]; ok {
			if err := s.upsertSecretKey(ctx, client.ObjectKey{Name: secretName, Namespace: systemNamespace}, env+"__"+variable.Name, string(value)); err != nil {
				return err
			}
		}
	}
	err := s.mutateProject(ctx, project.Name, func(latest *geassv1alpha1.GeassProject) error {
		if slices.Contains(latest.Spec.Environments, env) {
			return nil
		}
		latest.Spec.Environments = append(latest.Spec.Environments, env)
		for _, variable := range latest.Spec.SharedVariables {
			if variable.Environment != base {
				continue
			}
			copied := variable
			copied.Environment = env
			if variable.SecretRef != nil {
				copied.SecretRef = &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
					Key:                  env + "__" + variable.Name,
				}
			}
			latest.Spec.SharedVariables = append(latest.Spec.SharedVariables, copied)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return s.updateProjectStatus(ctx, project.Name, func(status *geassv1alpha1.GeassProjectStatus) {
		if _, exists := findPullRequestIn(status, repo, number); exists {
			return
		}
		status.PullRequests = append(status.PullRequests, geassv1alpha1.GeassPullRequestEnvironment{
			Number: number, Repository: repo, Branch: pr.Head.Ref, Environment: env, URL: pr.HTMLURL, OpenedAt: metav1.Now(),
		})
	})
}

func findPullRequestIn(status *geassv1alpha1.GeassProjectStatus, repo string, number int32) (int, bool) {
	for i, entry := range status.PullRequests {
		if entry.Repository == repo && entry.Number == number {
			return i, true
		}
	}
	return 0, false
}

// cloneEnvironmentResources copies the base environment's databases and services into the pull
// request environment and deploys them. Services that build this repository follow the pull
// request's branch. Buckets, logical databases, and external databases are not copied.
func (s *Server) cloneEnvironmentResources(ctx context.Context, project *geassv1alpha1.GeassProject, base, env, repo string, number int32, pr pullRequestBody, apps []geassv1alpha1.GeassApp, databases []geassv1alpha1.GeassDatabase) (int, error) {
	created := 0
	connectionNames := map[string]string{} // base connection secret -> cloned connection secret

	for _, original := range databases {
		name := cloneName(original.Name, number)
		connectionNames[original.Name+"-connection"] = name + "-connection"
		var existing geassv1alpha1.GeassDatabase
		if err := s.Client.Get(ctx, client.ObjectKey{Name: name, Namespace: systemNamespace}, &existing); err == nil {
			continue
		} else if !apierrors.IsNotFound(err) {
			return created, err
		}
		clone := &geassv1alpha1.GeassDatabase{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: systemNamespace,
				Annotations: map[string]string{platform.DisplayNameAnnotation: platform.ResourceDisplayName(original.ObjectMeta)},
				Labels:      map[string]string{labelPullRequest: strconv.Itoa(int(number))},
			},
			Spec: *original.Spec.DeepCopy(),
		}
		clone.Spec.Environment = geassv1alpha1.GeassEnvironment(env)
		// A pull request environment is for review, so one instance is enough.
		clone.Spec.HighAvailability = false
		clone.Spec.Instances = nil
		// Review databases are throwaway: they are neither backed up nor restored from backups.
		clone.Spec.Backup = nil
		clone.Spec.RestoreFrom = nil
		clone.Spec.Deploy.Enabled = true
		if err := platform.SetLastDeployedJSON(&clone.ObjectMeta, clone.Spec); err != nil {
			return created, err
		}
		if err := s.Client.Create(ctx, clone); err != nil && !apierrors.IsAlreadyExists(err) {
			return created, err
		}
		created++
	}

	for _, original := range apps {
		name := cloneName(original.Name, number)
		var existing geassv1alpha1.GeassApp
		if err := s.Client.Get(ctx, client.ObjectKey{Name: name, Namespace: systemNamespace}, &existing); err == nil {
			continue
		} else if !apierrors.IsNotFound(err) {
			return created, err
		}
		clone := &geassv1alpha1.GeassApp{
			ObjectMeta: metav1.ObjectMeta{
				Name: name, Namespace: systemNamespace,
				Annotations: map[string]string{platform.DisplayNameAnnotation: platform.ResourceDisplayName(original.ObjectMeta)},
				Labels:      map[string]string{labelPullRequest: strconv.Itoa(int(number))},
			},
			Spec: *original.Spec.DeepCopy(),
		}
		clone.Spec.Environment = geassv1alpha1.GeassEnvironment(env)
		// Each environment gets its own address; the base environment's domain stays with it.
		clone.Spec.Ingress = geassv1alpha1.GeassAppIngressSpec{Path: original.Spec.Ingress.Path}
		one := int32(1)
		clone.Spec.Replicas = &one
		clone.Spec.Autoscaling = nil
		// A schedule belongs to the environment it was made for: a copy in every pull request would repeat
		// whatever it does (mail, writes to a shared store) once per open pull request.
		if clone.Spec.Source.Function != nil {
			clone.Spec.Source.Function.Triggers = nil
		}
		// Most open pull requests are idle most of the time, so they sleep unless the service said otherwise.
		// A service without a public host, or a cluster without the add-on, runs awake as before.
		if clone.Spec.ScaleToZero == nil {
			clone.Spec.ScaleToZero = &geassv1alpha1.GeassAppScaleToZeroSpec{Enabled: true}
		}
		if git := clone.Spec.Source.Git; git != nil {
			git.Commit = ""
			if git.Repository == repo {
				git.Branch = pr.Head.Ref
			}
		}
		for i := range clone.Spec.Env {
			if selector := clone.Spec.Env[i].ValueFrom; selector != nil && selector.SecretKeyRef != nil {
				if renamed, ok := connectionNames[selector.SecretKeyRef.Name]; ok {
					selector.SecretKeyRef.Name = renamed
				}
			}
		}
		clone.Spec.SecretRef = nil
		if original.Spec.SecretRef != nil {
			secretName := name + "-secrets"
			if copied, err := s.copySecret(ctx, original.Spec.SecretRef.Name, secretName); err != nil {
				return created, err
			} else if copied {
				clone.Spec.SecretRef = &corev1.LocalObjectReference{Name: secretName}
			}
		}
		clone.Spec.Deploy.Enabled = true
		if err := platform.SetLastDeployedSpec(clone); err != nil {
			return created, err
		}
		if err := s.Client.Create(ctx, clone); err != nil && !apierrors.IsAlreadyExists(err) {
			return created, err
		}
		created++
	}
	return created, nil
}

// copySecret duplicates a secret in the system namespace. It reports false when the source is gone.
func (s *Server) copySecret(ctx context.Context, from, to string) (bool, error) {
	var source corev1.Secret
	if err := s.Client.Get(asSystem(ctx), client.ObjectKey{Name: from, Namespace: systemNamespace}, &source); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	target := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: to, Namespace: systemNamespace},
		Type:       source.Type,
		Data:       source.DeepCopy().Data,
	}
	if err := s.Client.Create(asSystem(ctx), target); err != nil && !apierrors.IsAlreadyExists(err) {
		return false, err
	}
	return true, nil
}

// closePullRequestEnvironment removes the environment of a closed or merged pull request.
func (s *Server) closePullRequestEnvironment(ctx context.Context, projectName, repo string, number int32) (pullRequestResult, error) {
	result := pullRequestResult{Project: projectName, Action: "close"}
	var project geassv1alpha1.GeassProject
	if err := s.Client.Get(ctx, client.ObjectKey{Name: projectName, Namespace: systemNamespace}, &project); err != nil {
		return result, client.IgnoreNotFound(err)
	}
	entry, ok := findPullRequest(&project, repo, number)
	if !ok {
		return result, nil
	}
	result.Env = entry.Environment
	// The GitHub Deployments of the environment are read first: they are named on the services that go with it.
	deployments := s.pullRequestDeployments(ctx, projectName, entry.Environment, number)
	deleted, err := s.removeEnvironment(ctx, projectName, entry.Environment)
	switch {
	case err == nil:
	case errors.Is(err, errEnvironmentMissing):
		// The environment list no longer has it; still delete anything left behind.
		if deleted, err = s.deleteEnvironmentResources(ctx, projectName, entry.Environment); err != nil {
			return result, err
		}
	case errors.Is(err, errLastEnvironment):
		return result, fmt.Errorf("cannot remove the only environment of project %s", projectName)
	default:
		return result, err
	}
	result.Removed = deleted
	if err := s.updateProjectStatus(ctx, projectName, func(status *geassv1alpha1.GeassProjectStatus) {
		if index, found := findPullRequestIn(status, repo, number); found {
			status.PullRequests = slices.Delete(status.PullRequests, index, index+1)
		}
	}); err != nil {
		return result, err
	}
	// Told after the project stops listing the pull request, which is what stops the operator from writing the
	// comment again. In the background: GitHub gives a webhook ten seconds, and this is several requests.
	s.reportInBackground(ctx, func(ctx context.Context) {
		s.reportPullRequestRemoved(ctx, projectName, repo, number, entry.Environment, deployments)
	})
	return result, nil
}

// ---- settings ----

func (s *Server) handleProjectPREnvironments(w http.ResponseWriter, r *http.Request, project, operation string) {
	fallback := workspacePanelURL(project, "", "environments", "")
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	switch operation {
	case "save":
		enabled := r.FormValue("enabled") == "on"
		base := strings.TrimSpace(r.FormValue("baseEnvironment"))
		maxOpen := int32(defaultMaxPREnvironments)
		if value := strings.TrimSpace(r.FormValue("maxOpen")); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 1 || parsed > 25 {
				redirectFormError(w, r, fallback, "the limit must be between 1 and 25")
				return
			}
			maxOpen = int32(parsed)
		}
		errBase := errors.New("the base environment was not found in this project")
		err := s.mutateProject(r.Context(), project, func(p *geassv1alpha1.GeassProject) error {
			chosen := base
			if chosen == "" && len(p.Spec.Environments) > 0 {
				chosen = p.Spec.Environments[0]
			}
			if !slices.Contains(p.Spec.Environments, chosen) {
				return errBase
			}
			p.Spec.PREnvironments = &geassv1alpha1.GeassPREnvironments{Enabled: enabled, BaseEnvironment: chosen, MaxOpen: maxOpen}
			return nil
		})
		s.replyWebhookMutation(w, r, fallback, err, errBase, map[string]any{"ok": true})
	case "close":
		number, err := strconv.Atoi(strings.TrimSpace(r.FormValue("number")))
		repo := strings.TrimSpace(r.FormValue("repository"))
		if err != nil || number <= 0 || repo == "" {
			redirectFormError(w, r, fallback, "pull request number and repository are required")
			return
		}
		result, err := s.closePullRequestEnvironment(r.Context(), project, repo, int32(number))
		if err != nil {
			redirectFormInternalError(w, r, fallback)
			return
		}
		if result.Env == "" {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "deleted": result.Removed})
	default:
		http.NotFound(w, r)
	}
}
