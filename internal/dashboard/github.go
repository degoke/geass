package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/githubapp"
	"github.com/degoke/geass/pkg/platform"
)

type githubRepository struct {
	FullName      string
	DefaultBranch string
	Private       bool
	HTMLURL       string
}

type githubAccount struct {
	Login string
	Type  string
}

func githubConnectionName(project string) string {
	return project + "-github"
}

func (s *Server) projectGitHubConnection(ctx context.Context, project string) (*geassv1alpha1.GeassGitHubConnection, error) {
	var p geassv1alpha1.GeassProject
	if err := s.Client.Get(ctx, client.ObjectKey{Name: project, Namespace: systemNamespace}, &p); err != nil {
		return nil, err
	}
	if p.Spec.GitHubConnectionRef == nil || p.Spec.GitHubConnectionRef.Name == "" {
		return nil, nil
	}
	connection := &geassv1alpha1.GeassGitHubConnection{}
	if err := s.Client.Get(ctx, client.ObjectKey{Name: p.Spec.GitHubConnectionRef.Name, Namespace: systemNamespace}, connection); err != nil {
		return nil, err
	}
	return connection, nil
}

func githubConnectionReady(connection *geassv1alpha1.GeassGitHubConnection) bool {
	if connection == nil {
		return false
	}
	return connection.Status.Ready || conditionStatus(connection.Status.Conditions, platform.ConditionReady) == "True"
}

func githubInstallationID(secret *corev1.Secret) int64 {
	return githubapp.InstallationIDFromSecret(secret)
}

func githubTokenFromSecret(secret *corev1.Secret) string {
	return githubapp.TokenFromSecret(secret)
}

func githubAccountFromSecret(secret *corev1.Secret) githubAccount {
	if secret == nil || secret.Data == nil {
		return githubAccount{}
	}
	return githubAccount{
		Login: string(secret.Data["account"]),
		Type:  string(secret.Data["account_type"]),
	}
}

func (s *Server) githubConnectionSecret(ctx context.Context, connection *geassv1alpha1.GeassGitHubConnection) (*corev1.Secret, error) {
	secret := &corev1.Secret{}
	if err := s.Client.Get(ctx, client.ObjectKey{Name: connection.Spec.SecretRef.Name, Namespace: systemNamespace}, secret); err != nil {
		return nil, err
	}
	return secret, nil
}

func (s *Server) githubTokenResolver() *githubapp.TokenResolver {
	return &githubapp.TokenResolver{Client: s.Client, HTTP: s.HTTPClient, Namespace: systemNamespace}
}

func (s *Server) githubConnectionToken(ctx context.Context, connection *geassv1alpha1.GeassGitHubConnection) (string, error) {
	secret, err := s.githubConnectionSecret(ctx, connection)
	if err != nil {
		return "", err
	}
	return s.githubTokenResolver().ConnectionToken(ctx, secret)
}

func (s *Server) listGitHubRepositories(ctx context.Context, connection *geassv1alpha1.GeassGitHubConnection, token string) ([]githubRepository, error) {
	secret, err := s.githubConnectionSecret(ctx, connection)
	if err != nil {
		return nil, err
	}
	if githubapp.InstallationIDFromSecret(secret) > 0 && s.githubAppConfigured(ctx) {
		repos, err := s.githubAppClient().ListInstallationRepositories(token)
		if err != nil {
			return nil, err
		}
		return mapGitHubRepositories(repos), nil
	}
	return s.listGitHubUserRepositories(ctx, token)
}

func mapGitHubRepositories(repos []githubapp.Repository) []githubRepository {
	out := make([]githubRepository, 0, len(repos))
	for _, repo := range repos {
		if repo.FullName == "" {
			continue
		}
		out = append(out, githubRepository{
			FullName:      repo.FullName,
			DefaultBranch: repo.DefaultBranch,
			Private:       repo.Private,
			HTMLURL:       repo.HTMLURL,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].FullName) < strings.ToLower(out[j].FullName)
	})
	return out
}

func (s *Server) projectDeployedRepositories(ctx context.Context, project string) []string {
	var apps geassv1alpha1.GeassAppList
	if err := s.Client.List(ctx, &apps, client.InNamespace(systemNamespace)); err != nil {
		return nil
	}
	seen := map[string]bool{}
	var repositories []string
	for _, app := range apps.Items {
		if app.Spec.Project != project || app.Spec.Source.Git == nil {
			continue
		}
		repo := strings.TrimSpace(app.Spec.Source.Git.Repository)
		if repo == "" || seen[repo] {
			continue
		}
		seen[repo] = true
		repositories = append(repositories, repo)
	}
	sort.Strings(repositories)
	return repositories
}

func workspaceGitCreateURL(project, environment, repository, repoQuery string) string {
	q := url.Values{"create": {"app-git"}}
	if repository != "" {
		q.Set("repository", repository)
	}
	if repoQuery != "" {
		q.Set("repoq", repoQuery)
	}
	return workspaceURL(project, environment, q)
}

func (s *Server) githubConnectionUsable(ctx context.Context, connection *geassv1alpha1.GeassGitHubConnection) bool {
	if githubConnectionReady(connection) {
		return true
	}
	if connection == nil {
		return false
	}
	secret, err := s.githubConnectionSecret(ctx, connection)
	if err != nil {
		return false
	}
	return githubInstallationID(secret) > 0 || githubTokenFromSecret(secret) != ""
}

func (s *Server) createAppGitFormBody(r *http.Request, project, environment string) string {
	if prerequisite := s.platformGitHubPrerequisiteHTML(r.Context()); prerequisite != "" {
		return prerequisite
	}
	connection, err := s.projectGitHubConnection(r.Context(), project)
	if err != nil {
		return Alert("error", "Could not load the GitHub connection for this project.")
	}
	if connection == nil {
		return s.githubConnectPanel(r.Context(), project, environment, "")
	}
	if !s.githubConnectionUsable(r.Context(), connection) {
		return s.githubConnectPanel(r.Context(), project, environment, "GitHub is not connected yet. Install your GitHub App to continue.")
	}
	token, err := s.githubConnectionToken(r.Context(), connection)
	if err != nil {
		return s.githubConnectPanel(r.Context(), project, environment, err.Error())
	}
	secret, _ := s.githubConnectionSecret(r.Context(), connection)
	account := githubAccountFromSecret(secret)
	repos, repoErr := s.listGitHubRepositories(r.Context(), connection, token)
	selectedRepo := strings.TrimSpace(r.URL.Query().Get("repository"))
	repoQuery := strings.TrimSpace(r.URL.Query().Get("repoq"))
	filtered := filterGitHubRepositories(repos, repoQuery)
	return s.githubDeployPanel(r, project, environment, connection, secret, account, filtered, repoErr, s.projectDeployedRepositories(r.Context(), project), selectedRepo, repoQuery)
}

func filterGitHubRepositories(repos []githubRepository, query string) []githubRepository {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return repos
	}
	filtered := make([]githubRepository, 0, len(repos))
	for _, repo := range repos {
		if strings.Contains(strings.ToLower(repo.FullName), query) {
			filtered = append(filtered, repo)
		}
	}
	return filtered
}

func (s *Server) githubConnectPanel(ctx context.Context, project, environment, notice string) string {
	var b strings.Builder
	if notice != "" {
		b.WriteString(Alert("warning", template.HTMLEscapeString(notice)))
	}
	b.WriteString(`<div class="github-connect"><div class="github-connect-copy"><h3 class="card-title">Connect GitHub</h3><p class="text-secondary">Install your GitHub App on your account or organization to grant repository access. Geass never stores your GitHub password.</p><ul class="github-permissions"><li>Read repository contents and metadata</li><li>Receive push events for automatic deploys</li><li>Choose which repositories Geass can access</li></ul></div>`)
	if !s.githubAppConfigured(ctx) {
		b.WriteString(s.platformGitHubPrerequisiteHTML(ctx))
	} else {
		installURL := "/projects/" + url.PathEscape(project) + "/github/install?environment=" + url.QueryEscape(environment)
		b.WriteString(`<div class="github-connect-actions">` + Button("Connect GitHub", ButtonOpts{Href: installURL, Variant: "primary"}) + `</div>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}

func (s *Server) githubDeployPanel(r *http.Request, project, environment string, connection *geassv1alpha1.GeassGitHubConnection, secret *corev1.Secret, account githubAccount, repos []githubRepository, repoErr error, deployed []string, selectedRepo, repoQuery string) string {
	cfg := s.githubAppClient().Config
	installationID := strconv.FormatInt(githubInstallationID(secret), 10)
	var b strings.Builder
	b.WriteString(`<div class="github-deploy github-picker">`)

	b.WriteString(`<section class="github-repo-section"><form class="github-repo-search" method="GET" action="/projects/` + url.PathEscape(project) + `"><input type="hidden" name="environment" value="` + template.HTMLEscapeString(environment) + `"><input type="hidden" name="create" value="app-git">`)
	b.WriteString(`<div class="github-repo-search-row"><a class="workspace-back" href="` + template.HTMLEscapeString(workspaceURL(project, environment, nil)) + `" aria-label="Back to workspace">←</a><input class="input" name="repoq" value="` + template.HTMLEscapeString(repoQuery) + `" placeholder="Search repositories, or paste a URL..." autofocus></div><div class="github-repo-toolbar"><a class="link" href="` + template.HTMLEscapeString(cfg.InstallationSettingsURL(installationID)) + `">⚙ Configure GitHub App</a>` + Button("Refresh", ButtonOpts{Href: workspaceGitCreateURL(project, environment, "", repoQuery), Variant: "ghost"}) + `</div></form>`)

	if repoErr != nil {
		b.WriteString(Alert("error", "Could not list repositories: "+template.HTMLEscapeString(repoErr.Error())))
	} else if len(repos) == 0 {
		b.WriteString(Alert("warning", "No repositories match this search. Grant repository access in GitHub App settings, then refresh."))
	} else {
		b.WriteString(`<div class="github-repo-list">`)
		for _, repo := range repos {
			branch := repo.DefaultBranch
			if branch == "" {
				branch = "main"
			}
			fmt.Fprintf(&b, `<form method="POST" action="/apps/create" class="github-repo-form"><input type="hidden" name="source" value="git"><input type="hidden" name="name" value="%s"><input type="hidden" name="project" value="%s"><input type="hidden" name="environment" value="%s"><input type="hidden" name="connectionRef" value="%s"><input type="hidden" name="repository" value="%s"><input type="hidden" name="branch" value="%s"><input type="hidden" name="dockerfile" value="Dockerfile"><input type="hidden" name="context" value="."><button class="github-repo-row" type="submit"><span class="github-repo-icon" aria-hidden="true">◉</span><span><strong>%s</strong></span><span aria-hidden="true">›</span></button></form>`, template.HTMLEscapeString(s.nextGitHubAppName(r.Context(), repo.FullName)), template.HTMLEscapeString(project), template.HTMLEscapeString(environment), template.HTMLEscapeString(connection.Name), template.HTMLEscapeString(repo.FullName), template.HTMLEscapeString(branch), template.HTMLEscapeString(repo.FullName))
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(`</section>`)
	b.WriteString(`</div>`)
	return b.String()
}

func (s *Server) nextGitHubAppName(ctx context.Context, repository string) string {
	base := strings.TrimSpace(repository)
	if slash := strings.LastIndex(base, "/"); slash >= 0 {
		base = base[slash+1:]
	}
	var sanitized strings.Builder
	for _, r := range strings.ToLower(base) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			sanitized.WriteRune(r)
		} else if sanitized.Len() > 0 && !strings.HasSuffix(sanitized.String(), "-") {
			sanitized.WriteByte('-')
		}
	}
	base = strings.Trim(sanitized.String(), "-")
	if base == "" {
		base = "service"
	}
	if len(base) > 50 {
		base = strings.TrimRight(base[:50], "-")
	}
	used := map[string]bool{}
	var apps geassv1alpha1.GeassAppList
	if err := s.Client.List(ctx, &apps, client.InNamespace(systemNamespace)); err == nil {
		for _, app := range apps.Items {
			used[app.Name] = true
		}
	}
	name := base
	for suffix := 2; used[name]; suffix++ {
		name = fmt.Sprintf("%s-%d", base, suffix)
	}
	return name
}

func (s *Server) handleProjectGitHubInstall(w http.ResponseWriter, r *http.Request, project string) {
	environment := strings.TrimSpace(r.URL.Query().Get("environment"))
	fallback := workspaceCreateURL(project, environment, "app-git")
	if prerequisite := s.platformGitHubPrerequisiteHTML(r.Context()); prerequisite != "" {
		redirectFormError(w, r, fallback, "GitHub App is not configured")
		return
	}
	gh, _, err := s.githubAppClientFromContext(r.Context())
	if err != nil || !gh.Config.Configured() {
		redirectFormError(w, r, fallback, "GitHub App is not configured")
		return
	}
	state, err := gh.SignState(project, environment)
	if err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	redirect(w, r, gh.Config.InstallURL(state))
}

func (s *Server) handleGitHubCallback(w http.ResponseWriter, r *http.Request) {
	fallback := "/settings/github"
	installationRaw := strings.TrimSpace(r.URL.Query().Get("installation_id"))
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if installationRaw == "" || state == "" {
		redirectFormError(w, r, fallback, "installation_id and state are required")
		return
	}
	installationID, err := strconv.ParseInt(installationRaw, 10, 64)
	if err != nil || installationID <= 0 {
		redirectFormError(w, r, fallback, "installation_id is invalid")
		return
	}
	gh, readiness, err := s.githubAppClientFromContext(r.Context())
	if err != nil || !readiness.HasGitHubApp {
		redirectFormError(w, r, fallback, "GitHub App is not configured")
		return
	}
	project, environment, err := gh.VerifyState(state)
	if err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	fallback = workspaceCreateURL(project, environment, "app-git")
	if err := s.saveGitHubInstallation(r.Context(), project, environment, installationID); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	redirect(w, r, fallback)
}

func (s *Server) saveGitHubInstallation(ctx context.Context, project, environment string, installationID int64) error {
	gh, readiness, err := s.githubAppClientFromContext(ctx)
	if err != nil {
		return err
	}
	installation, err := gh.GetInstallation(installationID)
	if err != nil {
		return err
	}
	token, err := gh.InstallationToken(installationID)
	if err != nil {
		return err
	}

	var p geassv1alpha1.GeassProject
	if err := s.Client.Get(ctx, client.ObjectKey{Name: project, Namespace: systemNamespace}, &p); err != nil {
		return err
	}

	connectionName := githubConnectionName(project)
	tokenSecretName := connectionName + "-token"

	tokenSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: tokenSecretName, Namespace: systemNamespace},
		Type:       corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"token":           []byte(token),
			"installation_id": []byte(strconv.FormatInt(installationID, 10)),
			"account":         []byte(installation.Account.Login),
			"account_type":    []byte(installation.Account.Type),
		},
	}
	if err := s.Client.Create(ctx, tokenSecret); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return err
		}
		existing := &corev1.Secret{}
		if err := s.Client.Get(ctx, client.ObjectKey{Name: tokenSecretName, Namespace: systemNamespace}, existing); err != nil {
			return err
		}
		if existing.Data == nil {
			existing.Data = map[string][]byte{}
		}
		existing.Data["token"] = []byte(token)
		existing.Data["installation_id"] = []byte(strconv.FormatInt(installationID, 10))
		existing.Data["account"] = []byte(installation.Account.Login)
		existing.Data["account_type"] = []byte(installation.Account.Type)
		if err := s.Client.Update(ctx, existing); err != nil {
			return err
		}
	}

	connection := &geassv1alpha1.GeassGitHubConnection{}
	err = s.Client.Get(ctx, client.ObjectKey{Name: connectionName, Namespace: systemNamespace}, connection)
	if apierrors.IsNotFound(err) {
		spec := geassv1alpha1.GeassGitHubConnectionSpec{SecretRef: corev1.LocalObjectReference{Name: tokenSecretName}}
		if readiness.Config.Spec.GitHubAppRef != nil && readiness.Config.Spec.GitHubAppRef.Name != "" {
			spec.WebhookSecretRef = &corev1.LocalObjectReference{Name: readiness.Config.Spec.GitHubAppRef.Name}
		}
		connection = &geassv1alpha1.GeassGitHubConnection{
			ObjectMeta: metav1.ObjectMeta{Name: connectionName, Namespace: systemNamespace},
			Spec:       spec,
		}
		if err := s.Client.Create(ctx, connection); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		connection.Spec.SecretRef = corev1.LocalObjectReference{Name: tokenSecretName}
		if readiness.Config.Spec.GitHubAppRef != nil && readiness.Config.Spec.GitHubAppRef.Name != "" {
			connection.Spec.WebhookSecretRef = &corev1.LocalObjectReference{Name: readiness.Config.Spec.GitHubAppRef.Name}
		}
		if err := s.Client.Update(ctx, connection); err != nil {
			return err
		}
	}

	p.Spec.GitHubConnectionRef = &corev1.LocalObjectReference{Name: connectionName}
	return s.Client.Update(ctx, &p)
}

func (s *Server) handleProjectGitHubDisconnect(w http.ResponseWriter, r *http.Request, project string) {
	fallback := workspaceCreateURL(project, "", "app-git")
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	environment := strings.TrimSpace(r.FormValue("environment"))
	fallback = workspaceCreateURL(project, environment, "app-git")
	var p geassv1alpha1.GeassProject
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: project, Namespace: systemNamespace}, &p); err != nil {
		http.NotFound(w, r)
		return
	}
	p.Spec.GitHubConnectionRef = nil
	if err := s.Client.Update(r.Context(), &p); err != nil {
		redirectFormError(w, r, fallback, err.Error())
		return
	}
	redirect(w, r, fallback)
}

// listGitHubUserRepositories remains for tests and legacy token-based connections.
func (s *Server) listGitHubUserRepositories(ctx context.Context, token string) ([]githubRepository, error) {
	return s.listGitHubRepositoriesLegacy(ctx, token)
}

func (s *Server) listGitHubRepositoriesLegacy(ctx context.Context, token string) ([]githubRepository, error) {
	var repositories []githubRepository
	page := 1
	for page <= 5 {
		data, err := s.githubAPIRequestLegacy(ctx, token, fmt.Sprintf("/user/repos?per_page=100&sort=updated&page=%d", page))
		if err != nil {
			return nil, err
		}
		var batch []struct {
			FullName      string `json:"full_name"`
			DefaultBranch string `json:"default_branch"`
			Private       bool   `json:"private"`
			HTMLURL       string `json:"html_url"`
		}
		if err := json.Unmarshal(data, &batch); err != nil {
			return nil, err
		}
		if len(batch) == 0 {
			break
		}
		for _, repo := range batch {
			if repo.FullName == "" {
				continue
			}
			repositories = append(repositories, githubRepository{
				FullName:      repo.FullName,
				DefaultBranch: repo.DefaultBranch,
				Private:       repo.Private,
				HTMLURL:       repo.HTMLURL,
			})
		}
		if len(batch) < 100 {
			break
		}
		page++
	}
	sort.Slice(repositories, func(i, j int) bool {
		return strings.ToLower(repositories[i].FullName) < strings.ToLower(repositories[j].FullName)
	})
	return repositories, nil
}

func (s *Server) githubAPIRequestLegacy(ctx context.Context, token, path string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com"+path, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/vnd.github+json")
	httpClient := s.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= 300 {
		return nil, fmt.Errorf("GitHub API returned %s", response.Status)
	}
	return data, nil
}
