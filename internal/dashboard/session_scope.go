package dashboard

import (
	"net/http"
	"strings"
	"time"

	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/degoke/geass/pkg/platform"
)

// handleSessionScope serves POST /api/session/scope: a person who works in more than one place (organizations, and the
// workspaces of each) says which they work in now. It checks the choice against what the session was given when the
// person signed in, and issues the cookie again with the same end and the same start, so that changing place neither
// lengthens the session nor lets it outlast a sign-out that came after it began.
func (s *Server) handleSessionScope(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, "/") || !parseFormOrRedirect(w, r, "/") {
		return
	}
	fail := func(status int, message string) {
		writeJSON(w, status, map[string]string{dashboardLiteralError: message})
	}
	if credentialIsBearer(r) {
		fail(http.StatusBadRequest, "a token works in the place it was made for")
		return
	}
	if !s.tenancyOn(r.Context()) {
		fail(http.StatusBadRequest, "organizations are not turned on")
		return
	}
	cookie, err := r.Cookie(cookieName(r, platform.DashboardSessionCookie))
	auth, authErr := s.dashboardAuth(r)
	if err != nil || authErr != nil {
		fail(http.StatusUnauthorized, "authentication required")
		return
	}
	now := time.Now()
	claims, ok := parseSSOSession(auth.sessionKey, cookie.Value, now)
	if !ok {
		fail(http.StatusBadRequest, "only a person who signed in through the identity provider can change where they work")
		return
	}
	// The session has to still stand (it may have been revoked since): the same check as for any request.
	if session := s.currentSession(r); session == nil {
		fail(http.StatusUnauthorized, "authentication required")
		return
	}

	org := strings.TrimSpace(r.FormValue("org"))
	workspace := strings.TrimSpace(r.FormValue("workspace"))
	role := ""
	switch {
	case org == "" && workspace == "":
		// The platform's own place, for a platform administrator.
		if !claims.Platform {
			fail(http.StatusNotFound, "that is not a place you can work in")
			return
		}
		role = dashboardRoleAdmin
	case org == "":
		fail(http.StatusBadRequest, "name the organization as well as the workspace")
		return
	case claims.Platform:
		// A platform administrator may go into any organization that is open, as its administrator.
		if !s.scopeStillStands(r.Context(), org, workspace) {
			fail(http.StatusNotFound, "that is not a place you can work in")
			return
		}
		role = dashboardRoleAdmin
	default:
		granted, allowed := roleIn(claims.Grants, org, workspace)
		if !allowed || !s.scopeStillStands(r.Context(), org, workspace) {
			fail(http.StatusNotFound, "that is not a place you can work in")
			return
		}
		role = granted
	}

	claims.Org, claims.Workspace, claims.Role = org, workspace, role
	ttl := time.Unix(claims.Expires, 0).Sub(now)
	token, err := issueSSOSession(auth.sessionKey, claims, ttl, now)
	if err != nil {
		fail(http.StatusInternalServerError, "could not change where you work")
		return
	}
	http.SetCookie(w, dashboardSessionCookie(r, token, int(ttl.Seconds())))
	ctrllog.FromContext(r.Context()).Info("Changed where a person works", "user", claims.Email, "org", org, "workspace", workspace)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "org": org, "workspace": workspace, "role": role})
}
