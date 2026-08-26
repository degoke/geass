package dashboard

import (
	"context"
	"fmt"
	"html/template"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/githubapp"
	"github.com/degoke/geass/pkg/platform"
)

const platformGitHubAppSecretName = "platform-github-app"

type platformReadiness struct {
	Config          geassv1alpha1.GeassPlatformConfig
	DashboardURL    string
	GitHubApp       githubapp.Config
	GitHubAppSecret *corev1.Secret
	HasDashboardURL bool
	HasGitHubApp    bool
}

func (s *Server) platformConfig(ctx context.Context) (geassv1alpha1.GeassPlatformConfig, error) {
	config := geassv1alpha1.GeassPlatformConfig{}
	err := s.Client.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: systemNamespace}, &config)
	if apierrors.IsNotFound(err) {
		return geassv1alpha1.GeassPlatformConfig{}, nil
	}
	return config, err
}

func normalizeDashboardURL(raw string) string {
	return platform.NormalizeDashboardURL(raw)
}

func (s *Server) platformReadiness(ctx context.Context) (platformReadiness, error) {
	config, err := s.platformConfig(ctx)
	if err != nil {
		return platformReadiness{}, err
	}
	readiness := platformReadiness{Config: config}
	rootDomain := platform.RootDomainFromConfig(config)
	if rootDomain != "" {
		readiness.DashboardURL = platform.DashboardURLFromRoot(rootDomain)
	} else {
		readiness.DashboardURL = platform.NormalizeDashboardURL(config.Spec.DashboardURL)
	}
	readiness.HasDashboardURL = platform.DashboardDomainReady(config)

	var secret *corev1.Secret
	if config.Spec.GitHubAppRef != nil && config.Spec.GitHubAppRef.Name != "" {
		secret = &corev1.Secret{}
		if err := s.Client.Get(ctx, client.ObjectKey{Name: config.Spec.GitHubAppRef.Name, Namespace: systemNamespace}, secret); err != nil && !apierrors.IsNotFound(err) {
			return platformReadiness{}, err
		}
		if apierrors.IsNotFound(err) {
			secret = nil
		}
	}
	readiness.GitHubAppSecret = secret
	readiness.GitHubApp = s.resolveGitHubAppConfig(readiness.DashboardURL, secret, config.Spec.GitHubAppRef != nil && config.Spec.GitHubAppRef.Name != "")
	readiness.HasGitHubApp = readiness.GitHubApp.Configured()
	return readiness, nil
}

func (s *Server) resolveGitHubAppConfig(dashboardURL string, secret *corev1.Secret, githubAppRefSet bool) githubapp.Config {
	if githubAppRefSet {
		if secret != nil {
			return githubapp.ConfigFromSecret(secret, dashboardURL)
		}
		return githubapp.Config{PublicBaseURL: dashboardURL}
	}
	cfg := s.GitHubApp
	if dashboardURL != "" {
		cfg.PublicBaseURL = dashboardURL
	}
	if cfg.Configured() {
		return cfg
	}
	cfg = githubapp.LoadConfigFromEnv()
	if dashboardURL != "" {
		cfg.PublicBaseURL = dashboardURL
	}
	return cfg
}

func (s *Server) githubAppClientFromContext(ctx context.Context) (*githubapp.Client, platformReadiness, error) {
	readiness, err := s.platformReadiness(ctx)
	if err != nil {
		return nil, platformReadiness{}, err
	}
	return &githubapp.Client{Config: readiness.GitHubApp, HTTP: s.HTTPClient}, readiness, nil
}

func (s *Server) githubAppClient() *githubapp.Client {
	client, _, err := s.githubAppClientFromContext(context.Background())
	if err != nil || client == nil {
		cfg := s.GitHubApp
		if !cfg.Configured() {
			cfg = githubapp.LoadConfigFromEnv()
		}
		return &githubapp.Client{Config: cfg, HTTP: s.HTTPClient}
	}
	return client
}

func (s *Server) githubAppConfigured(ctx context.Context) bool {
	readiness, err := s.platformReadiness(ctx)
	if err != nil {
		return false
	}
	return readiness.HasGitHubApp
}

func (s *Server) platformGitHubPrerequisiteHTML(ctx context.Context) string {
	readiness, err := s.platformReadiness(ctx)
	if err != nil {
		return Alert("error", "Could not load platform settings.")
	}
	if !readiness.HasDashboardURL {
		return s.githubPrerequisitePanel(
			"Set your dashboard domain before connecting GitHub.",
			"Geass needs a stable public HTTPS domain for GitHub App callbacks and webhooks.",
			"/settings/domain",
			"Configure domain",
		)
	}
	if !readiness.HasGitHubApp {
		return s.githubPrerequisitePanel(
			"Configure your GitHub App before connecting repositories.",
			"Create a GitHub App on github.com using the callback and webhook URLs shown in platform settings, then paste the app credentials into Geass.",
			"/settings/github",
			"Configure GitHub App",
		)
	}
	return ""
}

func (s *Server) platformGitHubReadyError(ctx context.Context) error {
	readiness, err := s.platformReadiness(ctx)
	if err != nil {
		return fmt.Errorf("could not load platform settings")
	}
	if !readiness.HasDashboardURL {
		return fmt.Errorf("dashboard URL is not configured")
	}
	if !readiness.HasGitHubApp {
		return fmt.Errorf("GitHub App is not configured")
	}
	return nil
}

func (s *Server) platformGitHubWebhookSecret(ctx context.Context) (string, error) {
	readiness, err := s.platformReadiness(ctx)
	if err != nil {
		return "", err
	}
	if readiness.GitHubApp.WebhookSecret != "" {
		return readiness.GitHubApp.WebhookSecret, nil
	}
	return "", nil
}

func (s *Server) githubPrerequisitePanel(title, description, href, buttonLabel string) string {
	return Card(`<h3 class="card-title">` + template.HTMLEscapeString(title) + `</h3><p class="text-secondary">` + template.HTMLEscapeString(description) + `</p><div class="row-wrap mt-2">` + Button(buttonLabel, ButtonOpts{Href: href, Variant: "primary"}) + `</div>`)
}

func githubAppSetupInstructions(dashboardURL string) string {
	callbackURL := dashboardURL + "/github/callback"
	webhookURL := dashboardURL + "/webhooks/github"
	copyButton := func(value string) string {
		return `<button type="button" class="btn btn-ghost btn-sm" data-copy-value="` + template.HTMLEscapeString(value) + `">Copy</button>`
	}
	return fmt.Sprintf(`<div class="settings-copy-grid"><div><span class="field-label">Homepage URL</span><div class="copy-row"><code class="copy-value">%s</code>%s</div></div><div><span class="field-label">Callback URL</span><div class="copy-row"><code class="copy-value">%s</code>%s</div></div><div><span class="field-label">Webhook URL</span><div class="copy-row"><code class="copy-value">%s</code>%s</div></div><div><span class="field-label">Permissions</span><span class="text-secondary text-sm">Contents (Read), Metadata (Read), Push events</span></div></div>`,
		template.HTMLEscapeString(dashboardURL), copyButton(dashboardURL),
		template.HTMLEscapeString(callbackURL), copyButton(callbackURL),
		template.HTMLEscapeString(webhookURL), copyButton(webhookURL),
	)
}
