package dashboard

import (
	"net/http"

	corev1 "k8s.io/api/core/v1"

	"github.com/degoke/geass/pkg/githubapp"
)

func (s *Server) handlePlatformSettings(w http.ResponseWriter, r *http.Request) {
	clusterOverview := s.clusterOverviewHTML(r.Context())

	body := `<p class="overline">Platform</p>` + PageHeader("General settings", "") +
		`<h2 class="section-title">Cluster overview</h2>` +
		`<div hx-get="/settings" hx-trigger="every 30s" hx-select="#cluster-overview" hx-target="#cluster-overview" hx-swap="outerHTML">` + clusterOverview + `</div>`

	s.renderPage(w, r, "Platform Settings", body)
}

func (s *Server) handlePlatformGitHubSettings(w http.ResponseWriter, r *http.Request) {
	readiness, err := s.platformReadiness(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !readiness.HasDashboardURL && !s.ensureDashboardDomainVerified(r.Context(), readiness) {
		body := `<p class="overline">Platform</p>` + PageHeader("GitHub App", "Connect Geass to GitHub for repository deploys.") +
			Alert("warning", "Configure your dashboard domain before setting up a GitHub App.") +
			Card(`<p class="text-secondary">Geass needs a stable public HTTPS domain to register GitHub App callback and webhook endpoints.</p><div class="row-wrap mt-2">`+Button("Configure domain", ButtonOpts{Href: "/settings/domain", Variant: "primary"})+`</div>`)
		s.renderPage(w, r, "GitHub App Settings", body)
		return
	}

	secret := readiness.GitHubAppSecret
	appID := secretValue(secret, githubapp.SecretKeyAppID)
	clientID := secretValue(secret, githubapp.SecretKeyClientID)
	slug := secretValue(secret, githubapp.SecretKeySlug)
	hasPrivateKey := secret != nil && len(secret.Data[githubapp.SecretKeyPrivateKey]) > 0
	hasClientSecret := secret != nil && len(secret.Data[githubapp.SecretKeyClientSecret]) > 0
	hasWebhookSecret := secret != nil && len(secret.Data[githubapp.SecretKeyWebhookSecret]) > 0

	state := s.beginGitHubManifestState(w, r)
	body := `<p class="overline">Platform</p>` + PageHeader("GitHub App", "Create a GitHub App for repository deploys.") +
		githubAppManifestForm(readiness.DashboardURL, state) +
		Card(`<h3 class="card-title">GitHub App URLs</h3><p class="text-secondary">These values are included automatically when you create the app with Geass. Use them if you create or edit the app manually.</p>`+githubAppSetupInstructions(readiness.DashboardURL)) +
		FormOpen("/settings/github/save", "POST", "") +
		Card(
			`<h3 class="card-title">Or paste credentials manually</h3>`+
				`<p class="text-secondary text-sm">Use this for an existing app. Project connections still authenticate through the GitHub App installation flow, so users must sign in to GitHub and authorize repository access.</p>`+
				Field("App ID", Input("appID", appID, map[string]string{"placeholder": "123456", "required": "required"}))+
				Field("Client ID", Input("clientID", clientID, map[string]string{"placeholder": "Iv1.abcdef", "required": "required"}))+
				Field("App slug", Input("slug", slug, map[string]string{"placeholder": "geass", "required": "required"}))+
				Field("Client secret", Input("clientSecret", "", map[string]string{"placeholder": placeholderWhenSet(hasClientSecret, "Leave blank to keep current"), "autocomplete": "off"}))+
				Field("Webhook secret", Input("webhookSecret", "", map[string]string{"placeholder": placeholderWhenSet(hasWebhookSecret, "Leave blank to keep current"), "autocomplete": "off"}))+
				Field("Private key (PEM)", Textarea("privateKey", "", map[string]string{"placeholder": placeholderWhenSet(hasPrivateKey, "Leave blank to keep current private key"), "rows": "8"}))+
				Button("Save GitHub App", ButtonOpts{Type: "submit", Variant: "primary"}),
		) + `</form>`

	if readiness.HasGitHubApp {
		body += Alert("success", "GitHub App is configured. Projects can now connect GitHub accounts and deploy repositories.")
		body += `<div class="row-wrap mt-2">` + FormOpen("/settings/github/test", "POST", "") + Button("Test GitHub webhook", ButtonOpts{Type: "submit", Variant: "ghost"}) + `</form>` +
			FormOpen("/settings/github/clear", "POST", "") +
			Field("Type remove-github to confirm", Input("confirm", "", map[string]string{"placeholder": "remove-github", "autocomplete": "off", "required": "required"})) +
			Button("Remove GitHub App", ButtonOpts{Type: "submit", Variant: "danger"}) + `</form></div>`
	}

	s.renderPage(w, r, "GitHub App Settings", body)
}

func placeholderWhenSet(set bool, keepMessage string) string {
	if set {
		return keepMessage
	}
	return ""
}

func secretValue(secret *corev1.Secret, key string) string {
	if secret == nil || secret.Data == nil {
		return ""
	}
	return string(secret.Data[key])
}

func (s *Server) handlePlatformGitHubSettingsSave(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, "/settings/github") || !parseFormOrRedirect(w, r, "/settings/github") {
		return
	}
	readiness, err := s.platformReadiness(r.Context())
	if err != nil {
		redirectProbe(w, r, "/settings/github", "error", err.Error())
		return
	}
	if !readiness.HasDashboardURL {
		redirectProbe(w, r, "/settings/github", "error", "dashboard URL must be configured first")
		return
	}

	if err := s.persistGitHubAppCredentials(r.Context(), readiness.DashboardURL, githubAppCredentialInput{
		AppID:         r.FormValue("appID"),
		ClientID:      r.FormValue("clientID"),
		Slug:          r.FormValue("slug"),
		ClientSecret:  r.FormValue("clientSecret"),
		WebhookSecret: r.FormValue("webhookSecret"),
		PrivateKey:    r.FormValue("privateKey"),
		KeepExisting:  true,
	}); err != nil {
		redirectProbe(w, r, "/settings/github", "error", err.Error())
		return
	}
	redirect(w, r, "/settings/github")
}
