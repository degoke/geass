package dashboard

import (
	"net/http"
)

// handlePlatformGitHubRoutes serves the settings of the cluster's own GitHub App: /settings/github/save, test and clear.
func (s *Server) handlePlatformGitHubRoutes(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/settings/github/save":
		s.handlePlatformGitHubSettingsSave(w, r)
	case "/settings/github/test":
		s.handlePlatformGitHubTest(w, r)
	case "/settings/github/clear":
		s.handlePlatformGitHubClear(w, r)
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{dashboardLiteralError: "not found"})
	}
}

func (s *Server) handlePlatformGitHubSettingsSave(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, "/settings/github") || !parseFormOrRedirect(w, r, "/settings/github") {
		return
	}
	if s.refuseInExternalMode(w, r, "/settings/github") {
		return
	}
	readiness, err := s.platformReadiness(r.Context())
	if err != nil {
		redirectProbe(w, r, "/settings/github", dashboardLiteralError, "could not load GitHub settings")
		return
	}
	if !readiness.HasDashboardURL {
		redirectProbe(w, r, "/settings/github", dashboardLiteralError, "dashboard URL must be configured first")
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
		redirectProbe(w, r, "/settings/github", dashboardLiteralError, githubCredentialError(err))
		return
	}
	redirect(w, r, "/settings/github")
}

func githubCredentialError(err error) string {
	if err == nil {
		return "could not save GitHub App credentials"
	}
	switch err.Error() {
	case "app ID, client ID, and slug are required",
		"client secret, webhook secret, and private key are required",
		"GitHub App credentials are incomplete",
		"GitHub webhook secret is not configured",
		"GitHub App public URL must use HTTPS",
		"GitHub App private key is not valid PEM",
		"GitHub App private key is not RSA":
		return err.Error()
	default:
		return "could not save GitHub App credentials"
	}
}
