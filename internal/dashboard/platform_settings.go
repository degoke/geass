package dashboard

import (
	"net/http"
)

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
