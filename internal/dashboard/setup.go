package dashboard

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/degoke/geass/pkg/platform"
)

// POST /api/setup uses up a setup link (see pkg/platform/dashboard_setup.go): it makes the admin it names, or gives an
// existing user that name's new password and the admin role, and signs that person in. The token is the credential, so the
// route is public. A wrong token counts against the address as a failed sign-in does.

var errSetupLinkInvalid = errors.New("this setup link was used or has expired")

func (s *Server) handleDashboardSetup(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, "/") || !parseFormOrRedirect(w, r, "/") {
		return
	}
	source := loginSourceKey(r)
	if s.loginSourceBlocked(source) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{dashboardLiteralError: "too many attempts: wait one minute"})
		return
	}
	token := strings.TrimSpace(r.FormValue("token"))
	username := strings.TrimSpace(r.FormValue(formUsername))
	password := r.FormValue("password")
	if password == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: "enter a password"})
		return
	}
	if _, _, err := passwordForRequest(password); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: err.Error()})
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		redirectFormInternalError(w, r, "/")
		return
	}

	err = s.mutateDashboardUsersData(r, func(users []dashboardUser, epochs map[string]int64, data map[string][]byte) ([]dashboardUser, error) {
		if !platform.DashboardSetupMatches(data[platform.DashboardSetupKey], token, time.Now()) {
			return nil, errSetupLinkInvalid
		}
		delete(data, platform.DashboardSetupKey)
		if i := indexOfDashboardUser(users, username); i >= 0 {
			users[i].Password, users[i].Role = string(hash), dashboardRoleAdmin
			bumpDashboardEpoch(epochs, users[i].Username)
			return users, nil
		}
		if err := validateNewUsername(username); err != nil {
			return nil, err
		}
		return append(users, dashboardUser{Username: username, Password: string(hash), Role: dashboardRoleAdmin}), nil
	})
	switch {
	case errors.Is(err, errSetupLinkInvalid):
		s.noteLoginSourceFailure(source)
		writeJSON(w, http.StatusForbidden, map[string]string{dashboardLiteralError: err.Error()})
		return
	case err != nil:
		// An answer from the API server names cluster internals, so only the rules of a name or a password are shown.
		var status apierrors.APIStatus
		if errors.As(err, &status) {
			writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: dashboardAuthUnavailable})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{dashboardLiteralError: err.Error()})
		return
	}
	ctrllog.FromContext(r.Context()).Info("Used dashboard setup link", "username", username)

	auth, err := s.dashboardAuth(r)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: dashboardAuthUnavailable})
		return
	}
	user := auth.lookup(username)
	if user == nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: dashboardAuthUnavailable})
		return
	}
	session, err := issueDashboardSession(auth.sessionKey, user.Username, dashboardRoleAdmin, auth.epoch(user.Username))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not create a session"})
		return
	}
	http.SetCookie(w, dashboardSessionCookie(r, session, int(dashboardSessionTTL.Seconds())))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "authenticated": true, "username": user.Username, formRole: dashboardRoleAdmin})
}
