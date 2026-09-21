package dashboard

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/degoke/geass/pkg/platform"
)

const dashboardSessionTTL = 12 * time.Hour

const (
	dashboardRoleAdmin  = "admin"
	dashboardRoleViewer = "viewer"
)

type dashboardUser struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

type dashboardAuth struct {
	users      []dashboardUser
	sessionKey []byte
}

type dashboardSessionInfo struct {
	Username  string `json:"username"`
	Role      string `json:"role"`
	CanMutate bool   `json:"canMutate"`
}

func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if dashboardPublicPath(r) {
			next.ServeHTTP(w, r)
			return
		}
		session := s.currentSession(r)
		if session == nil {
			if isJSONRequest(r) || strings.HasPrefix(r.URL.Path, "/api/") {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
				return
			}
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !session.CanMutate {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "viewer role cannot change cluster state"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func dashboardPublicPath(r *http.Request) bool {
	path := r.URL.Path
	switch {
	case path == "/geass-probe", path == "/webhooks/github", path == "/github/callback", path == "/settings/github/manifest/callback":
		return true
	case path == "/api/login", path == "/api/logout", path == "/api/session":
		return true
	case strings.HasPrefix(path, "/assets/"):
		return true
	case r.Method == http.MethodGet && !strings.HasPrefix(path, "/api/"):
		return true
	default:
		return false
	}
}

func (s *Server) handleDashboardLogin(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, "/") || !parseFormOrRedirect(w, r, "/") {
		return
	}
	auth, err := s.dashboardAuth(r)
	if err != nil {
		redirectFormError(w, r, "/", "dashboard authentication is unavailable")
		return
	}
	user := auth.lookup(r.FormValue("username"))
	if user == nil || !passwordEqual(r.FormValue("password"), user.Password) {
		redirectFormError(w, r, "/", "invalid username or password")
		return
	}
	token, err := issueDashboardSession(auth.sessionKey, user.Username, normalizeDashboardRole(user.Role))
	if err != nil {
		redirectFormError(w, r, "/", "could not create a session")
		return
	}
	http.SetCookie(w, dashboardSessionCookie(r, token, int(dashboardSessionTTL.Seconds())))
	if isJSONRequest(r) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "authenticated": true, "username": user.Username, "role": normalizeDashboardRole(user.Role), "canMutate": normalizeDashboardRole(user.Role) == dashboardRoleAdmin})
		return
	}
	redirect(w, r, "/")
}

func (s *Server) handleDashboardLogout(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, "/") {
		return
	}
	http.SetCookie(w, dashboardSessionCookie(r, "", -1))
	if isJSONRequest(r) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "authenticated": false})
		return
	}
	redirect(w, r, "/")
}

func (s *Server) handleDashboardSession(w http.ResponseWriter, r *http.Request) {
	session := s.currentSession(r)
	if session == nil {
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "username": session.Username, "role": session.Role, "canMutate": session.CanMutate})
}

func (s *Server) sessionValid(r *http.Request) bool {
	return s.currentSession(r) != nil
}

func (s *Server) currentSession(r *http.Request) *dashboardSessionInfo {
	cookie, err := r.Cookie(platform.DashboardSessionCookie)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return nil
	}
	auth, err := s.dashboardAuth(r)
	if err != nil {
		return nil
	}
	username, role, ok := dashboardSessionClaims(auth.sessionKey, cookie.Value)
	if !ok {
		return nil
	}
	user := auth.lookup(username)
	if user == nil {
		return nil
	}
	liveRole := normalizeDashboardRole(user.Role)
	if liveRole != role && role != "" {
		liveRole = normalizeDashboardRole(user.Role)
	}
	return &dashboardSessionInfo{Username: user.Username, Role: liveRole, CanMutate: liveRole == dashboardRoleAdmin}
}

func (s *Server) dashboardAuth(r *http.Request) (*dashboardAuth, error) {
	s.authMu.Lock()
	defer s.authMu.Unlock()

	users, sessionKey, err := s.loadDashboardAuth(r)
	if err != nil {
		return nil, err
	}
	auth := &dashboardAuth{users: users, sessionKey: sessionKey}
	s.auth = auth
	return auth, nil
}

func (s *Server) loadDashboardAuth(r *http.Request) ([]dashboardUser, []byte, error) {
	sessionKey := []byte(strings.TrimSpace(os.Getenv("GEASS_DASHBOARD_SESSION_KEY")))
	users := parseDashboardUsersEnv(os.Getenv("GEASS_DASHBOARD_USERS"))
	if len(users) == 0 {
		if password := strings.TrimSpace(os.Getenv("GEASS_DASHBOARD_PASSWORD")); password != "" {
			username := strings.TrimSpace(os.Getenv("GEASS_DASHBOARD_USERNAME"))
			if username == "" {
				username = dashboardRoleAdmin
			}
			users = []dashboardUser{{Username: username, Password: password, Role: dashboardRoleAdmin}}
		}
	}
	secret := &corev1.Secret{}
	err := s.Client.Get(r.Context(), client.ObjectKey{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace}, secret)
	if err == nil {
		if len(users) == 0 {
			users = parseDashboardUsersSecret(secret)
		}
		if len(sessionKey) == 0 {
			sessionKey = secret.Data["session-key"]
		}
	} else if !apierrors.IsNotFound(err) {
		return nil, nil, err
	}
	if len(users) == 0 || len(sessionKey) == 0 {
		if len(users) == 0 {
			generated, genErr := randomDashboardSecret(24)
			if genErr != nil {
				return nil, nil, genErr
			}
			users = []dashboardUser{{Username: dashboardRoleAdmin, Password: generated, Role: dashboardRoleAdmin}}
		}
		if len(sessionKey) == 0 {
			generated, genErr := randomDashboardBytes(32)
			if genErr != nil {
				return nil, nil, genErr
			}
			sessionKey = generated
		}
		payload, marshalErr := json.Marshal(users)
		if marshalErr != nil {
			return nil, nil, marshalErr
		}
		created := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace},
			Type:       corev1.SecretTypeOpaque,
			StringData: map[string]string{
				"users":       string(payload),
				"session-key": hex.EncodeToString(sessionKey),
			},
		}
		if createErr := s.Client.Create(r.Context(), created); createErr != nil {
			if !apierrors.IsAlreadyExists(createErr) {
				return nil, nil, createErr
			}
			existing := &corev1.Secret{}
			if getErr := s.Client.Get(r.Context(), client.ObjectKey{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace}, existing); getErr != nil {
				return nil, nil, getErr
			}
			if len(parseDashboardUsersSecret(existing)) > 0 {
				users = parseDashboardUsersSecret(existing)
			}
			if len(existing.Data["session-key"]) > 0 {
				sessionKey = existing.Data["session-key"]
			}
		}
	}
	if decoded, err := hex.DecodeString(string(sessionKey)); err == nil && len(decoded) >= 16 {
		sessionKey = decoded
	}
	return users, sessionKey, nil
}

func (a *dashboardAuth) lookup(username string) *dashboardUser {
	username = strings.TrimSpace(username)
	if username == "" || a == nil {
		return nil
	}
	for i := range a.users {
		if a.users[i].Username == username && a.users[i].Password != "" {
			return &a.users[i]
		}
	}
	return nil
}

func parseDashboardUsersSecret(secret *corev1.Secret) []dashboardUser {
	if secret == nil {
		return nil
	}
	raw := secret.Data["users"]
	if len(raw) == 0 {
		return nil
	}
	var users []dashboardUser
	if err := json.Unmarshal(raw, &users); err != nil {
		return nil
	}
	return users
}

func parseDashboardUsersEnv(value string) []dashboardUser {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if strings.HasPrefix(value, "[") {
		var users []dashboardUser
		if err := json.Unmarshal([]byte(value), &users); err != nil {
			return nil
		}
		return users
	}
	var users []dashboardUser
	for _, part := range strings.Split(value, ",") {
		fields := strings.SplitN(strings.TrimSpace(part), ":", 3)
		if len(fields) < 2 || fields[0] == "" || fields[1] == "" {
			continue
		}
		role := dashboardRoleAdmin
		if len(fields) == 3 {
			role = fields[2]
		}
		users = append(users, dashboardUser{Username: fields[0], Password: fields[1], Role: normalizeDashboardRole(role)})
	}
	return users
}

func normalizeDashboardRole(role string) string {
	if strings.EqualFold(strings.TrimSpace(role), dashboardRoleViewer) {
		return dashboardRoleViewer
	}
	return dashboardRoleAdmin
}

func dashboardSessionCookie(r *http.Request, value string, maxAge int) *http.Cookie {
	secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	return &http.Cookie{
		Name:     platform.DashboardSessionCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   secure,
	}
}

func issueDashboardSession(sessionKey []byte, username, role string) (string, error) {
	nonce, err := randomDashboardBytes(16)
	if err != nil {
		return "", err
	}
	expiry := strconv.FormatInt(time.Now().Add(dashboardSessionTTL).Unix(), 10)
	payload := expiry + ":" + hex.EncodeToString(nonce) + ":" + username + ":" + role
	mac := hmacSHA256Bytes(sessionKey, payload)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + hex.EncodeToString(mac), nil
}

func dashboardSessionClaims(sessionKey []byte, token string) (username, role string, ok bool) {
	encoded, macHex, ok := strings.Cut(token, ".")
	if !ok {
		return "", "", false
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", "", false
	}
	expected := hmacSHA256Bytes(sessionKey, string(payload))
	if !hmac.Equal([]byte(hex.EncodeToString(expected)), []byte(macHex)) {
		return "", "", false
	}
	parts := strings.Split(string(payload), ":")
	if len(parts) != 4 {
		return "", "", false
	}
	expiry, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Now().Unix() >= expiry {
		return "", "", false
	}
	if parts[2] == "" {
		return "", "", false
	}
	return parts[2], normalizeDashboardRole(parts[3]), true
}

func dashboardSessionValid(sessionKey []byte, token string) bool {
	_, _, ok := dashboardSessionClaims(sessionKey, token)
	return ok
}

func passwordEqual(got, want string) bool {
	gotSum := sha256.Sum256([]byte(got))
	wantSum := sha256.Sum256([]byte(want))
	return hmac.Equal(gotSum[:], wantSum[:]) && want != ""
}

func hmacSHA256Bytes(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}

func randomDashboardSecret(size int) (string, error) {
	bytes, err := randomDashboardBytes(size)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func randomDashboardBytes(size int) ([]byte, error) {
	bytes := make([]byte, size)
	if _, err := rand.Read(bytes); err != nil {
		return nil, err
	}
	return bytes, nil
}
