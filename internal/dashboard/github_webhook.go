package dashboard

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/githubapp"
	"github.com/degoke/geass/pkg/platform"
)

type githubWebhookPayload struct {
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Ref string `json:"ref"`
	// Deleted is a push that removed the branch, which has nothing to build.
	Deleted bool `json:"deleted"`
	// Commits are the commits the push brought, with the files each changed.
	Commits []struct {
		Added    []string `json:"added"`
		Modified []string `json:"modified"`
		Removed  []string `json:"removed"`
	} `json:"commits"`
}

// githubMaxPushCommits is how many commits GitHub puts in a push event. A push with that many has more it did
// not list, so the files it lists are not all of the files it changed.
const githubMaxPushCommits = 2048

// changedFiles is every file the push touched, or nil when the event does not say (a branch made at an
// existing commit has no commits of its own) or does not say all of it. Nil means "cannot tell", which a
// service that watches paths answers by building.
func (p githubWebhookPayload) changedFiles() []string {
	if len(p.Commits) == 0 || len(p.Commits) >= githubMaxPushCommits {
		return nil
	}
	var files []string
	for _, commit := range p.Commits {
		files = append(files, commit.Added...)
		files = append(files, commit.Modified...)
		files = append(files, commit.Removed...)
	}
	return files
}

func (s *Server) handleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	if !requirePost(w, r) {
		return
	}
	event := strings.TrimSpace(r.Header.Get("X-GitHub-Event"))
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		http.Error(w, "invalid webhook body", http.StatusBadRequest)
		return
	}
	webhookSecret, err := s.platformGitHubWebhookSecret(r.Context())
	if err != nil || webhookSecret == "" {
		http.Error(w, "GitHub webhook secret is not configured", http.StatusServiceUnavailable)
		return
	}
	digest := hmacSHA256(webhookSecret, body)
	if !hmac.Equal([]byte(digest), []byte(r.Header.Get("X-Hub-Signature-256"))) {
		http.Error(w, "invalid webhook signature", http.StatusUnauthorized)
		return
	}
	if event == "ping" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"accepted":true,"event":"ping"}`))
		return
	}
	if event == "pull_request" {
		var payload githubPullRequestPayload
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(w, "invalid webhook payload", http.StatusBadRequest)
			return
		}
		results, err := s.handlePullRequestEvent(r.Context(), payload)
		if err != nil {
			http.Error(w, "could not process webhook", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"accepted": true, "environments": results})
		return
	}
	if event != "" && event != "push" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"accepted":true,"ignored":true}`))
		return
	}
	var payload githubWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "invalid webhook payload", http.StatusBadRequest)
		return
	}
	if payload.Repository.FullName == "" {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"accepted":true,"triggered":0}`))
		return
	}
	triggered, err := s.triggerWebhookBuilds(r.Context(), payload)
	if err != nil {
		http.Error(w, "could not process webhook", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"accepted":true,"triggered":%d}`, triggered)
}

func (s *Server) triggerWebhookBuilds(ctx context.Context, payload githubWebhookPayload) (int, error) {
	if payload.Deleted {
		return 0, nil
	}
	var apps geassv1alpha1.GeassAppList
	if err := s.Client.List(ctx, &apps, client.InNamespace(systemNamespace)); err != nil {
		return 0, err
	}
	changed := payload.changedFiles()
	triggered := 0
	var failures []error
	for i := range apps.Items {
		app := &apps.Items[i]
		git := app.Spec.Source.Git
		if git == nil || git.Repository != payload.Repository.FullName {
			continue
		}
		if !app.Spec.Deploy.Enabled {
			continue
		}
		if payload.Ref != "" && payload.Ref != "refs/heads/"+git.Branch {
			continue
		}
		// A service that pins a commit builds that commit and no other, so a push has nothing for it.
		if git.Commit != "" {
			continue
		}
		if !platform.WatchPatternsTrigger(git.WatchPatterns, changed) {
			continue
		}
		if payload.Installation.ID > 0 {
			matches, err := s.appMatchesInstallation(ctx, app, payload.Installation.ID)
			if err != nil || !matches {
				continue
			}
		}
		build, err := s.currentBuild(ctx, app)
		if err != nil || build == nil {
			continue
		}
		if err := s.retriggerBuild(ctx, build); err != nil {
			// The other services the push matches are still built. GitHub is told afterwards that it failed.
			failures = append(failures, fmt.Errorf("%s: %w", app.Name, err))
			continue
		}
		triggered++
	}
	return triggered, errors.Join(failures...)
}

func (s *Server) appMatchesInstallation(ctx context.Context, app *geassv1alpha1.GeassApp, installationID int64) (bool, error) {
	var project geassv1alpha1.GeassProject
	if err := s.Client.Get(ctx, client.ObjectKey{Name: app.Spec.Project, Namespace: systemNamespace}, &project); err != nil {
		return false, err
	}
	if project.Spec.GitHubConnectionRef == nil {
		return false, nil
	}
	connection := &geassv1alpha1.GeassGitHubConnection{}
	if err := s.Client.Get(ctx, client.ObjectKey{Name: project.Spec.GitHubConnectionRef.Name, Namespace: systemNamespace}, connection); err != nil {
		return false, err
	}
	secret := &corev1.Secret{}
	if err := s.Client.Get(ctx, client.ObjectKey{Name: connection.Spec.SecretRef.Name, Namespace: systemNamespace}, secret); err != nil {
		return false, err
	}
	return githubapp.InstallationIDFromSecret(secret) == installationID, nil
}

func (s *Server) validateGitConnectionForProject(ctx context.Context, project, connectionRef string) error {
	if connectionRef == "" {
		return fmt.Errorf("GitHub connection is required")
	}
	expected := githubConnectionName(project)
	if connectionRef != expected {
		return fmt.Errorf("GitHub connection does not belong to this project")
	}
	var projectCR geassv1alpha1.GeassProject
	if err := s.Client.Get(ctx, client.ObjectKey{Name: project, Namespace: systemNamespace}, &projectCR); err != nil {
		return fmt.Errorf("project was not found")
	}
	if projectCR.Spec.GitHubConnectionRef == nil || projectCR.Spec.GitHubConnectionRef.Name != connectionRef {
		return fmt.Errorf("project is not connected to GitHub")
	}
	connection := &geassv1alpha1.GeassGitHubConnection{}
	if err := s.Client.Get(ctx, client.ObjectKey{Name: connectionRef, Namespace: systemNamespace}, connection); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("GitHub connection was not found")
		}
		return fmt.Errorf("GitHub connection is unavailable")
	}
	secret := &corev1.Secret{}
	if err := s.Client.Get(ctx, client.ObjectKey{Name: connection.Spec.SecretRef.Name, Namespace: systemNamespace}, secret); err != nil {
		return fmt.Errorf("GitHub connection Secret is unavailable")
	}
	if githubapp.InstallationIDFromSecret(secret) == 0 && githubapp.TokenFromSecret(secret) == "" {
		return fmt.Errorf("GitHub connection is not ready")
	}
	return nil
}

func (s *Server) validateCloudConnectionForProject(ctx context.Context, project, connectionRef string) error {
	if connectionRef == "" {
		return nil
	}
	connection := &geassv1alpha1.GeassCloudConnection{}
	if err := s.Client.Get(ctx, client.ObjectKey{Name: connectionRef, Namespace: systemNamespace}, connection); err != nil {
		if apierrors.IsNotFound(err) {
			return fmt.Errorf("cloud connection was not found")
		}
		return fmt.Errorf("cloud connection is unavailable")
	}
	if strings.TrimSpace(connection.Spec.Project) == "" {
		return fmt.Errorf("use a project-scoped cloud connection")
	}
	return platform.ValidateCloudConnectionForProject(*connection, project)
}

func hmacSHA256(secret string, body []byte) string {
	digest := hmac.New(sha256.New, []byte(secret))
	_, _ = digest.Write(body)
	return "sha256=" + hex.EncodeToString(digest.Sum(nil))
}
