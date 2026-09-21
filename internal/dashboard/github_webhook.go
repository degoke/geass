package dashboard

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	var apps geassv1alpha1.GeassAppList
	if err := s.Client.List(ctx, &apps, client.InNamespace(systemNamespace)); err != nil {
		return 0, err
	}
	triggered := 0
	for i := range apps.Items {
		app := &apps.Items[i]
		if app.Spec.Source.Git == nil || app.Spec.Source.Git.Repository != payload.Repository.FullName {
			continue
		}
		if !app.Spec.Deploy.Enabled {
			continue
		}
		if payload.Ref != "" && payload.Ref != "refs/heads/"+app.Spec.Source.Git.Branch {
			continue
		}
		if payload.Installation.ID > 0 {
			matches, err := s.appMatchesInstallation(ctx, app, payload.Installation.ID)
			if err != nil || !matches {
				continue
			}
		}
		var builds geassv1alpha1.GeassBuildList
		if err := s.Client.List(ctx, &builds, client.InNamespace(systemNamespace), client.MatchingLabels{platform.LabelApp: app.Name}); err != nil || len(builds.Items) == 0 {
			continue
		}
		sort.SliceStable(builds.Items, func(i, j int) bool {
			return builds.Items[i].CreationTimestamp.After(builds.Items[j].CreationTimestamp.Time)
		})
		if err := s.retriggerBuild(ctx, &builds.Items[0]); err != nil {
			return triggered, err
		}
		triggered++
	}
	return triggered, nil
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

func (s *Server) retriggerBuild(ctx context.Context, build *geassv1alpha1.GeassBuild) error {
	latest := &geassv1alpha1.GeassBuild{}
	if err := s.Client.Get(ctx, client.ObjectKeyFromObject(build), latest); err != nil {
		return err
	}
	if s.Kube != nil {
		jobName := latest.Name + "-kaniko"
		policy := metav1.DeletePropagationBackground
		_ = s.Kube.BatchV1().Jobs(latest.Namespace).Delete(ctx, jobName, metav1.DeleteOptions{PropagationPolicy: &policy})
	}
	latest.Status.CancelRequested = false
	latest.Status.Phase = geassv1alpha1.GeassBuildPending
	latest.Status.FailureReason = ""
	latest.Status.SourceRevision = ""
	latest.Status.CIState = ""
	latest.Status.CIChecks = nil
	return s.Client.Status().Update(ctx, latest)
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

func hmacSHA256(secret string, body []byte) string {
	digest := hmac.New(sha256.New, []byte(secret))
	_, _ = digest.Write(body)
	return "sha256=" + hex.EncodeToString(digest.Sum(nil))
}
