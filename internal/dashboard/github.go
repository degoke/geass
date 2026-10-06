package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/githubapp"
)

type githubRepository struct {
	FullName      string
	DefaultBranch string
	Private       bool
	HTMLURL       string
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
	slices.SortFunc(out, func(a, b githubRepository) int {
		return strings.Compare(strings.ToLower(a.FullName), strings.ToLower(b.FullName))
	})
	return out
}

func (s *Server) handleProjectGitHubInstall(w http.ResponseWriter, r *http.Request, project string) {
	fallback := workspaceCreateURL(project, "", "app-git")
	if !requireMutation(w, r, fallback) || !parseFormOrRedirect(w, r, fallback) {
		return
	}
	environment := strings.TrimSpace(r.FormValue("environment"))
	if environment == "" {
		environment = strings.TrimSpace(r.URL.Query().Get("environment"))
	}
	fallback = workspaceCreateURL(project, environment, "app-git")
	if err := s.platformGitHubReadyError(r.Context()); err != nil {
		redirectFormUserError(w, r, fallback, err)
		return
	}
	gh, _, err := s.githubAppClientFromContext(r.Context())
	if err != nil || !gh.Config.Configured() {
		redirectFormError(w, r, fallback, "GitHub App is not configured")
		return
	}
	state, err := gh.SignState(project, environment)
	if err != nil {
		redirectFormError(w, r, fallback, "could not start GitHub installation")
		return
	}
	installURL := gh.Config.InstallURL(state)
	if isJSONRequest(r) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "url": installURL})
		return
	}
	redirect(w, r, installURL)
}

func (s *Server) handleGitHubCallback(w http.ResponseWriter, r *http.Request) {
	fallback := "/settings/github"
	if !s.sessionCanMutate(r) {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
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
		redirectFormError(w, r, fallback, "GitHub installation state is invalid")
		return
	}
	fallback = workspaceCreateURL(project, environment, "app-git")
	if err := s.saveGitHubInstallation(r.Context(), project, environment, installationID); err != nil {
		redirectFormInternalError(w, r, fallback)
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
		redirectFormInternalError(w, r, fallback)
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
	slices.SortFunc(repositories, func(a, b githubRepository) int {
		return strings.Compare(strings.ToLower(a.FullName), strings.ToLower(b.FullName))
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
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode >= 300 {
		return nil, fmt.Errorf("GitHub API returned %s", response.Status)
	}
	return data, nil
}
