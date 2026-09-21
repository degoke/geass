package dashboard

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/githubapp"
	"github.com/degoke/geass/pkg/platform"
)

func (s *Server) handleGeassProbe(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true,"service":"geass-dashboard"}`))
}

func (s *Server) verifyDashboardURL(ctx context.Context, dashboardURL string) (string, string) {
	parsed, err := url.Parse(dashboardURL)
	if err != nil || parsed.Host == "" {
		return "dashboard URL is invalid", "error"
	}
	host := parsed.Hostname()
	if _, err := net.LookupHost(host); err != nil {
		return fmt.Sprintf("DNS lookup failed for %s", host), "error"
	}
	if _, ok := s.probeURL(ctx, dashboardURL+"/geass-probe"); ok {
		return "", "success"
	}
	if _, ok := s.probeURL(ctx, s.internalProbeURL()+"/geass-probe"); ok {
		return "Dashboard responds locally, but the public URL could not be reached. Check ingress, TLS, and DNS routing.", "warning"
	}
	publicMsg, _ := s.probeURL(ctx, dashboardURL+"/geass-probe")
	if publicMsg != "" {
		return publicMsg, "error"
	}
	return fmt.Sprintf("%s did not return a successful Geass probe response", dashboardURL), "error"
}

func (s *Server) internalProbeURL() string {
	addr := strings.TrimSpace(s.Addr)
	if addr == "" || addr == "0" {
		return "http://127.0.0.1:8082"
	}
	if strings.HasPrefix(addr, ":") {
		return "http://127.0.0.1" + addr
	}
	if strings.Contains(addr, "://") {
		return strings.TrimRight(addr, "/")
	}
	return "http://" + addr
}

func (s *Server) probeURL(ctx context.Context, probeURL string) (string, bool) {
	httpClient := s.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	if err != nil {
		return "dashboard URL is invalid", false
	}
	response, err := httpClient.Do(request)
	if err != nil {
		return "could not reach the dashboard URL", false
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"ok":true`) {
		return fmt.Sprintf("%s did not return a successful Geass probe response", probeURL), false
	}
	return "", true
}

func (s *Server) handlePlatformGitHubTest(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, "/settings/github") {
		return
	}
	readiness, err := s.platformReadiness(r.Context())
	if err != nil {
		redirectProbe(w, r, "/settings/github", "error", "could not load GitHub settings")
		return
	}
	if !readiness.HasGitHubApp {
		redirectProbe(w, r, "/settings/github", "error", "GitHub App is not configured")
		return
	}
	gh := &githubapp.Client{Config: readiness.GitHubApp, HTTP: s.HTTPClient}
	hook, err := gh.GetAppHookConfig()
	if err != nil {
		redirectProbe(w, r, "/settings/github", "error", "could not reach GitHub App")
		return
	}
	expected := readiness.DashboardURL + "/webhooks/github"
	if hook.URL != expected {
		redirectProbe(w, r, "/settings/github", "error", fmt.Sprintf("GitHub webhook URL is %s, expected %s", hook.URL, expected))
		return
	}
	redirectProbe(w, r, "/settings/github", "success", "")
}

func (s *Server) handlePlatformGitHubClear(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, "/settings/github") || !parseFormOrRedirect(w, r, "/settings/github") {
		return
	}
	if strings.TrimSpace(r.FormValue("confirm")) != "remove-github" {
		redirectProbe(w, r, "/settings/github", "error", "type remove-github to confirm removal")
		return
	}
	config := &geassv1alpha1.GeassPlatformConfig{}
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: platform.HAReadinessName, Namespace: systemNamespace}, config); err == nil {
		config.Spec.GitHubAppRef = nil
		if err := s.Client.Update(r.Context(), config); err != nil {
			redirectProbe(w, r, "/settings/github", "error", "could not remove GitHub App")
			return
		}
	}
	secret := &corev1.Secret{}
	if err := s.Client.Get(r.Context(), client.ObjectKey{Name: platformGitHubAppSecretName, Namespace: systemNamespace}, secret); err == nil {
		if err := s.Client.Delete(r.Context(), secret); err != nil && !apierrors.IsNotFound(err) {
			redirectProbe(w, r, "/settings/github", "error", "could not remove GitHub App")
			return
		}
	}
	redirect(w, r, "/settings/github")
}
