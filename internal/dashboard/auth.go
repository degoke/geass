package dashboard

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
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
	loginFailureLimit   = 5
	loginFailureWindow  = time.Minute
	loginLockout        = time.Minute
)

var errDashboardAuthUnconfigured = errors.New("dashboard credentials are not configured; set GEASS_DASHBOARD_PASSWORD or create Secret geass-dashboard-auth")

type dashboardUser struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

type dashboardAuth struct {
	users      []dashboardUser
	sessionKey []byte
	epochs     map[string]int64
}

type dashboardSessionInfo struct {
	Username  string `json:"username"`
	Role      string `json:"role"`
	CanMutate bool   `json:"canMutate"`
}

type loginAttempt struct {
	count       int
	windowStart time.Time
	lockedUntil time.Time
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
		if !session.CanMutate && (isDashboardWriteMethod(r) || dashboardWriteCallback(r.URL.Path)) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "viewer role cannot change cluster state"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isDashboardWriteMethod(r *http.Request) bool {
	return r.Method != http.MethodGet && r.Method != http.MethodHead
}

func dashboardWriteCallback(path string) bool {
	switch path {
	case "/github/callback", "/settings/github/manifest/callback":
		return true
	default:
		return false
	}
}

func dashboardPublicPath(r *http.Request) bool {
	path := r.URL.Path
	switch {
	case path == "/geass-probe", path == "/webhooks/github":
		return true
	case path == "/api/login", path == "/api/logout", path == "/api/session":
		return true
	case strings.HasPrefix(path, "/assets/"):
		return true
	case r.Method == http.MethodGet && !strings.HasPrefix(path, "/api/") && !dashboardWriteCallback(path):
		return true
	default:
		return false
	}
}

func (s *Server) handleDashboardLogin(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, "/") || !parseFormOrRedirect(w, r, "/") {
		return
	}
	username := strings.TrimSpace(r.FormValue("username"))
	if s.loginLocked(r, username) {
		redirectFormError(w, r, "/", "invalid username or password")
		return
	}
	auth, err := s.dashboardAuth(r)
	if err != nil {
		redirectFormError(w, r, "/", "dashboard authentication is unavailable")
		return
	}
	user := auth.lookup(username)
	if user == nil || !passwordMatches(r.FormValue("password"), user.Password) {
		s.recordLoginFailure(r, username)
		redirectFormError(w, r, "/", "invalid username or password")
		return
	}
	s.clearLoginFailures(r, username)
	role := normalizeDashboardRole(user.Role)
	if role == "" {
		role = dashboardRoleViewer
	}
	token, err := issueDashboardSession(auth.sessionKey, user.Username, role, auth.epoch(user.Username))
	if err != nil {
		redirectFormError(w, r, "/", "could not create a session")
		return
	}
	http.SetCookie(w, dashboardSessionCookie(r, token, int(dashboardSessionTTL.Seconds())))
	if isJSONRequest(r) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "authenticated": true, "username": user.Username, "role": role, "canMutate": role == dashboardRoleAdmin})
		return
	}
	redirect(w, r, "/")
}

func (s *Server) handleDashboardLogout(w http.ResponseWriter, r *http.Request) {
	if !requireMutation(w, r, "/") {
		return
	}
	if session := s.currentSession(r); session != nil {
		_ = s.revokeDashboardSession(r, session.Username)
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

func (s *Server) sessionCanMutate(r *http.Request) bool {
	session := s.currentSession(r)
	return session != nil && session.CanMutate
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
	username, _, epoch, ok := dashboardSessionClaims(auth.sessionKey, cookie.Value)
	if !ok {
		return nil
	}
	user := auth.lookup(username)
	if user == nil {
		return nil
	}
	if epoch != auth.epoch(username) {
		return nil
	}
	liveRole := normalizeDashboardRole(user.Role)
	if liveRole == "" {
		liveRole = dashboardRoleViewer
	}
	return &dashboardSessionInfo{Username: user.Username, Role: liveRole, CanMutate: liveRole == dashboardRoleAdmin}
}

func (s *Server) dashboardAuth(r *http.Request) (*dashboardAuth, error) {
	s.authMu.Lock()
	defer s.authMu.Unlock()

	users, sessionKey, epochs, err := s.loadDashboardAuth(r)
	if err != nil {
		return nil, err
	}
	auth := &dashboardAuth{users: users, sessionKey: sessionKey, epochs: epochs}
	s.auth = auth
	return auth, nil
}

func (s *Server) loadDashboardAuth(r *http.Request) ([]dashboardUser, []byte, map[string]int64, error) {
	sessionKey := []byte(strings.TrimSpace(os.Getenv("GEASS_DASHBOARD_SESSION_KEY")))
	users := parseDashboardUsersEnv(os.Getenv("GEASS_DASHBOARD_USERS"))
	fromEnv := len(users) > 0
	if len(users) == 0 {
		if password := strings.TrimSpace(os.Getenv("GEASS_DASHBOARD_PASSWORD")); password != "" {
			username := strings.TrimSpace(os.Getenv("GEASS_DASHBOARD_USERNAME"))
			if username == "" {
				username = dashboardRoleAdmin
			}
			users = []dashboardUser{{Username: username, Password: password, Role: dashboardRoleAdmin}}
			fromEnv = true
		}
	}
	secret := &corev1.Secret{}
	err := s.Client.Get(r.Context(), client.ObjectKey{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace}, secret)
	epochs := map[string]int64{}
	if err == nil {
		if len(users) == 0 {
			users = parseDashboardUsersSecret(secret)
		}
		if len(sessionKey) == 0 {
			sessionKey = secret.Data["session-key"]
		}
		epochs = parseDashboardSessionEpochs(secret)
	} else if !apierrors.IsNotFound(err) {
		return nil, nil, nil, err
	}
	if len(users) == 0 {
		return nil, nil, nil, errDashboardAuthUnconfigured
	}
	if len(sessionKey) == 0 {
		generated, genErr := randomDashboardBytes(32)
		if genErr != nil {
			return nil, nil, nil, genErr
		}
		sessionKey = generated
		if persistErr := s.persistDashboardSessionKey(r, secret, err, sessionKey, epochs); persistErr != nil {
			return nil, nil, nil, persistErr
		}
	}
	if decoded, decodeErr := hex.DecodeString(string(sessionKey)); decodeErr == nil && len(decoded) >= 16 {
		sessionKey = decoded
	}
	if !fromEnv {
		if upgraded, hashErr := hashDashboardUsers(users); hashErr != nil {
			return nil, nil, nil, hashErr
		} else if upgraded && err == nil {
			_ = s.updateDashboardUsersSecret(r.Context(), secret, users, sessionKey, epochs)
		}
	}
	return users, sessionKey, epochs, nil
}

func (s *Server) persistDashboardSessionKey(r *http.Request, existing *corev1.Secret, getErr error, sessionKey []byte, epochs map[string]int64) error {
	encodedKey := hex.EncodeToString(sessionKey)
	payload, err := json.Marshal(epochs)
	if err != nil {
		return err
	}
	if apierrors.IsNotFound(getErr) || existing == nil || existing.Name == "" {
		created := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace},
			Type:       corev1.SecretTypeOpaque,
			StringData: map[string]string{
				"session-key":    encodedKey,
				"session-epochs": string(payload),
			},
		}
		if createErr := s.Client.Create(r.Context(), created); createErr != nil && !apierrors.IsAlreadyExists(createErr) {
			return createErr
		}
		return nil
	}
	latest := existing.DeepCopy()
	if latest.Data == nil {
		latest.Data = map[string][]byte{}
	}
	latest.Data["session-key"] = []byte(encodedKey)
	latest.Data["session-epochs"] = payload
	return s.Client.Update(r.Context(), latest)
}

func (s *Server) updateDashboardUsersSecret(ctx context.Context, secret *corev1.Secret, users []dashboardUser, sessionKey []byte, epochs map[string]int64) error {
	payload, err := json.Marshal(users)
	if err != nil {
		return err
	}
	epochPayload, err := json.Marshal(epochs)
	if err != nil {
		return err
	}
	latest := secret.DeepCopy()
	if latest.Data == nil {
		latest.Data = map[string][]byte{}
	}
	latest.Data["users"] = payload
	latest.Data["session-epochs"] = epochPayload
	if len(sessionKey) > 0 {
		latest.Data["session-key"] = []byte(hex.EncodeToString(sessionKey))
	}
	return s.Client.Update(ctx, latest)
}

func (s *Server) revokeDashboardSession(r *http.Request, username string) error {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	secret := &corev1.Secret{}
	err := s.Client.Get(r.Context(), client.ObjectKey{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace}, secret)
	epochs := map[string]int64{}
	if err == nil {
		epochs = parseDashboardSessionEpochs(secret)
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	epochs[username] = epochs[username] + 1
	if apierrors.IsNotFound(err) {
		payload, marshalErr := json.Marshal(epochs)
		if marshalErr != nil {
			return marshalErr
		}
		return s.Client.Create(r.Context(), &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace},
			Type:       corev1.SecretTypeOpaque,
			StringData: map[string]string{"session-epochs": string(payload)},
		})
	}
	latest := secret.DeepCopy()
	payload, marshalErr := json.Marshal(epochs)
	if marshalErr != nil {
		return marshalErr
	}
	if latest.Data == nil {
		latest.Data = map[string][]byte{}
	}
	latest.Data["session-epochs"] = payload
	return s.Client.Update(r.Context(), latest)
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

func (a *dashboardAuth) epoch(username string) int64 {
	if a == nil || a.epochs == nil {
		return 0
	}
	return a.epochs[username]
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
	return filterDashboardUsers(users, false)
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
		return filterDashboardUsers(users, false)
	}
	var users []dashboardUser
	for _, part := range strings.Split(value, ",") {
		fields := strings.SplitN(strings.TrimSpace(part), ":", 3)
		if len(fields) < 2 || fields[0] == "" || fields[1] == "" {
			continue
		}
		role := ""
		defaultAdmin := len(fields) == 2
		if len(fields) == 3 {
			role = fields[2]
		}
		normalized, ok := parseDashboardRole(role, defaultAdmin)
		if !ok {
			continue
		}
		users = append(users, dashboardUser{Username: fields[0], Password: fields[1], Role: normalized})
	}
	return users
}

func filterDashboardUsers(users []dashboardUser, defaultAdmin bool) []dashboardUser {
	filtered := make([]dashboardUser, 0, len(users))
	for _, user := range users {
		if strings.TrimSpace(user.Username) == "" || user.Password == "" {
			continue
		}
		role, ok := parseDashboardRole(user.Role, defaultAdmin)
		if !ok {
			continue
		}
		user.Role = role
		filtered = append(filtered, user)
	}
	return filtered
}

func parseDashboardRole(role string, defaultAdmin bool) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "":
		if defaultAdmin {
			return dashboardRoleAdmin, true
		}
		return dashboardRoleViewer, true
	case dashboardRoleAdmin:
		return dashboardRoleAdmin, true
	case dashboardRoleViewer:
		return dashboardRoleViewer, true
	default:
		return "", false
	}
}

func normalizeDashboardRole(role string) string {
	parsed, ok := parseDashboardRole(role, false)
	if !ok {
		return ""
	}
	return parsed
}

func parseDashboardSessionEpochs(secret *corev1.Secret) map[string]int64 {
	epochs := map[string]int64{}
	if secret == nil || len(secret.Data["session-epochs"]) == 0 {
		return epochs
	}
	if err := json.Unmarshal(secret.Data["session-epochs"], &epochs); err != nil {
		return map[string]int64{}
	}
	return epochs
}

func hashDashboardUsers(users []dashboardUser) (bool, error) {
	upgraded := false
	for i := range users {
		if isBcryptHash(users[i].Password) {
			continue
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(users[i].Password), bcrypt.DefaultCost)
		if err != nil {
			return false, err
		}
		users[i].Password = string(hash)
		upgraded = true
	}
	return upgraded, nil
}

func dashboardSessionCookie(r *http.Request, value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     platform.DashboardSessionCookie,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   dashboardCookieSecure(r),
	}
}

func issueDashboardSession(sessionKey []byte, username, role string, epoch int64) (string, error) {
	nonce, err := randomDashboardBytes(16)
	if err != nil {
		return "", err
	}
	expiry := strconv.FormatInt(time.Now().Add(dashboardSessionTTL).Unix(), 10)
	payload := expiry + ":" + hex.EncodeToString(nonce) + ":" + username + ":" + role + ":" + strconv.FormatInt(epoch, 10)
	mac := hmacSHA256Bytes(sessionKey, payload)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + hex.EncodeToString(mac), nil
}

func dashboardSessionClaims(sessionKey []byte, token string) (username, role string, epoch int64, ok bool) {
	encoded, macHex, ok := strings.Cut(token, ".")
	if !ok {
		return "", "", 0, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return "", "", 0, false
	}
	expected := hmacSHA256Bytes(sessionKey, string(payload))
	if !hmac.Equal([]byte(hex.EncodeToString(expected)), []byte(macHex)) {
		return "", "", 0, false
	}
	parts := strings.Split(string(payload), ":")
	if len(parts) != 5 {
		return "", "", 0, false
	}
	expiry, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Now().Unix() >= expiry {
		return "", "", 0, false
	}
	if parts[2] == "" {
		return "", "", 0, false
	}
	role = normalizeDashboardRole(parts[3])
	if role == "" {
		role = dashboardRoleViewer
	}
	epoch, err = strconv.ParseInt(parts[4], 10, 64)
	if err != nil {
		return "", "", 0, false
	}
	return parts[2], role, epoch, true
}

func passwordMatches(got, stored string) bool {
	if stored == "" {
		return false
	}
	if isBcryptHash(stored) {
		return bcrypt.CompareHashAndPassword([]byte(stored), []byte(got)) == nil
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(stored)) == 1
}

func isBcryptHash(value string) bool {
	return strings.HasPrefix(value, "$2a$") || strings.HasPrefix(value, "$2b$") || strings.HasPrefix(value, "$2y$")
}

func hmacSHA256Bytes(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}

func randomDashboardBytes(size int) ([]byte, error) {
	bytes := make([]byte, size)
	if _, err := rand.Read(bytes); err != nil {
		return nil, err
	}
	return bytes, nil
}

func (s *Server) loginLocked(r *http.Request, username string) bool {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	if s.loginFailures == nil {
		s.loginFailures = map[string]loginAttempt{}
	}
	attempt, ok := s.loginFailures[loginKey(r, username)]
	return ok && time.Now().Before(attempt.lockedUntil)
}

func (s *Server) recordLoginFailure(r *http.Request, username string) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	if s.loginFailures == nil {
		s.loginFailures = map[string]loginAttempt{}
	}
	key := loginKey(r, username)
	now := time.Now()
	attempt := s.loginFailures[key]
	if now.Sub(attempt.windowStart) > loginFailureWindow {
		attempt = loginAttempt{windowStart: now}
	}
	attempt.count++
	if attempt.count >= loginFailureLimit {
		attempt.lockedUntil = now.Add(loginLockout)
	}
	s.loginFailures[key] = attempt
}

func (s *Server) clearLoginFailures(r *http.Request, username string) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	delete(s.loginFailures, loginKey(r, username))
}

func loginKey(r *http.Request, username string) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return strings.ToLower(strings.TrimSpace(username)) + "|" + host
}
