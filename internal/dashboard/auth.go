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
	"fmt"
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
	dashboardRoleAdmin              = "admin"
	dashboardRoleViewer             = "viewer"
	loginFailureLimit               = 5
	loginFailureWindow              = time.Minute
	loginLockout                    = time.Minute
	dashboardSessionKeyPrefix       = "hex:"
	dashboardPlaceholderPassword    = "CHANGE_ME"
	dashboardLoginLockoutsSecretKey = "login-lockouts"
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
	Count       int       `json:"count"`
	WindowStart time.Time `json:"windowStart"`
	LockedUntil time.Time `json:"lockedUntil"`
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
		if !session.CanMutate && (isDashboardWriteMethod(r) || dashboardWriteCallback(r.URL.Path) || dashboardSensitiveRead(r.URL.Path)) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "viewer role cannot access this"})
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

func dashboardSensitiveRead(path string) bool {
	switch {
	case strings.HasPrefix(path, "/api/apps/") && strings.HasSuffix(path, "/logs"):
		return true
	case strings.HasPrefix(path, "/api/apps/") && strings.HasSuffix(path, "/runtime"):
		return true
	case strings.HasPrefix(path, "/api/apps/") && strings.HasSuffix(path, "/variables"):
		return true
	case strings.HasPrefix(path, "/api/projects/") && strings.HasSuffix(path, "/github/repos"):
		return true
	case path == "/api/settings/github":
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
		if err := s.revokeDashboardSession(r, session.Username); err != nil {
			redirectFormError(w, r, "/", "could not sign out")
			return
		}
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
	users, sessionKey, epochs, secret, getErr, err := s.loadDashboardAuth(r)
	s.authMu.Unlock()
	if err != nil {
		return nil, err
	}
	upgraded, hashErr := hashDashboardUsers(users)
	if hashErr != nil {
		return nil, hashErr
	}
	if upgraded {
		s.authMu.Lock()
		persistErr := s.persistHashedDashboardUsers(r, secret, getErr, users, sessionKey, epochs)
		s.authMu.Unlock()
		if persistErr != nil {
			return nil, persistErr
		}
	}
	auth := &dashboardAuth{users: users, sessionKey: sessionKey, epochs: epochs}
	s.authMu.Lock()
	s.auth = auth
	s.authMu.Unlock()
	return auth, nil
}

func (s *Server) loadDashboardAuth(r *http.Request) ([]dashboardUser, []byte, map[string]int64, *corev1.Secret, error, error) {
	sessionKey := []byte(strings.TrimSpace(os.Getenv("GEASS_DASHBOARD_SESSION_KEY")))
	envUsers := parseDashboardUsersEnv(os.Getenv("GEASS_DASHBOARD_USERS"))
	if len(envUsers) == 0 {
		if password := strings.TrimSpace(os.Getenv("GEASS_DASHBOARD_PASSWORD")); password != "" {
			username := strings.TrimSpace(os.Getenv("GEASS_DASHBOARD_USERNAME"))
			if username == "" {
				username = dashboardRoleAdmin
			}
			envUsers = []dashboardUser{{Username: username, Password: password, Role: dashboardRoleAdmin}}
		}
	}
	secret := &corev1.Secret{}
	err := s.Client.Get(r.Context(), client.ObjectKey{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace}, secret)
	epochs := map[string]int64{}
	secretUsers := []dashboardUser{}
	if err == nil {
		secretUsers = parseDashboardUsersSecret(secret)
		if len(sessionKey) == 0 {
			sessionKey = secret.Data["session-key"]
		}
		epochs = parseDashboardSessionEpochs(secret)
	} else if !apierrors.IsNotFound(err) {
		return nil, nil, nil, nil, err, err
	}
	users := secretUsers
	if len(users) == 0 {
		users = envUsers
	}
	if len(users) == 0 {
		return nil, nil, nil, nil, err, errDashboardAuthUnconfigured
	}
	if len(sessionKey) == 0 {
		generated, genErr := randomDashboardBytes(32)
		if genErr != nil {
			return nil, nil, nil, nil, err, genErr
		}
		persisted, persistErr := s.persistDashboardSessionKey(r, secret, err, generated, epochs)
		if persistErr != nil {
			return nil, nil, nil, nil, err, persistErr
		}
		sessionKey = persisted
	}
	decoded, decodeErr := decodeDashboardSessionKey(sessionKey)
	if decodeErr != nil {
		return nil, nil, nil, nil, err, decodeErr
	}
	return users, decoded, epochs, secret, err, nil
}

func encodeDashboardSessionKey(sessionKey []byte) string {
	return dashboardSessionKeyPrefix + hex.EncodeToString(sessionKey)
}

func decodeDashboardSessionKey(raw []byte) ([]byte, error) {
	value := strings.TrimSpace(string(raw))
	if value == "" {
		return nil, fmt.Errorf("session-key is missing")
	}
	if encoded, ok := strings.CutPrefix(value, dashboardSessionKeyPrefix); ok {
		decoded, err := hex.DecodeString(encoded)
		if err != nil || len(decoded) < 16 {
			return nil, fmt.Errorf("session-key is invalid")
		}
		return decoded, nil
	}
	if len(raw) < 16 {
		return nil, fmt.Errorf("session-key is invalid")
	}
	return raw, nil
}

func (s *Server) persistDashboardSessionKey(r *http.Request, existing *corev1.Secret, getErr error, sessionKey []byte, epochs map[string]int64) ([]byte, error) {
	encodedKey := encodeDashboardSessionKey(sessionKey)
	payload, err := json.Marshal(epochs)
	if err != nil {
		return nil, err
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
		if createErr := s.Client.Create(r.Context(), created); createErr != nil {
			if !apierrors.IsAlreadyExists(createErr) {
				return nil, createErr
			}
			latest := &corev1.Secret{}
			if getLatest := s.Client.Get(r.Context(), client.ObjectKey{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace}, latest); getLatest != nil {
				return nil, getLatest
			}
			decoded, decodeErr := decodeDashboardSessionKey(latest.Data["session-key"])
			if decodeErr != nil {
				return nil, decodeErr
			}
			return decoded, nil
		}
		return sessionKey, nil
	}
	latest := existing.DeepCopy()
	if latest.Data == nil {
		latest.Data = map[string][]byte{}
	}
	latest.Data["session-key"] = []byte(encodedKey)
	latest.Data["session-epochs"] = payload
	if err := s.Client.Update(r.Context(), latest); err != nil {
		return nil, err
	}
	return sessionKey, nil
}

func (s *Server) persistHashedDashboardUsers(r *http.Request, existing *corev1.Secret, getErr error, users []dashboardUser, sessionKey []byte, epochs map[string]int64) error {
	if apierrors.IsNotFound(getErr) || existing == nil || existing.Name == "" {
		payload, err := json.Marshal(users)
		if err != nil {
			return err
		}
		epochPayload, err := json.Marshal(epochs)
		if err != nil {
			return err
		}
		created := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace},
			Type:       corev1.SecretTypeOpaque,
			StringData: map[string]string{
				"users":          string(payload),
				"session-key":    encodeDashboardSessionKey(sessionKey),
				"session-epochs": string(epochPayload),
			},
		}
		if createErr := s.Client.Create(r.Context(), created); createErr != nil {
			if !apierrors.IsAlreadyExists(createErr) {
				return createErr
			}
			latest := &corev1.Secret{}
			if getLatest := s.Client.Get(r.Context(), client.ObjectKey{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace}, latest); getLatest != nil {
				return getLatest
			}
			return s.updateDashboardUsersSecret(r.Context(), latest, users, sessionKey, epochs)
		}
		return nil
	}
	return s.updateDashboardUsersSecret(r.Context(), existing, users, sessionKey, epochs)
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
		latest.Data["session-key"] = []byte(encodeDashboardSessionKey(sessionKey))
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
		if len(fields) != 3 || fields[0] == "" || fields[1] == "" {
			continue
		}
		normalized, ok := parseDashboardRole(fields[2], false)
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
		if strings.TrimSpace(user.Username) == "" || user.Password == "" || isPlaceholderPassword(user.Password) {
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
	payload := expiry + ":" + hex.EncodeToString(nonce) + ":" + encodeSessionField(username) + ":" + encodeSessionField(role) + ":" + strconv.FormatInt(epoch, 10)
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
	username, ok = decodeSessionField(parts[2])
	if !ok || username == "" {
		return "", "", 0, false
	}
	roleValue, ok := decodeSessionField(parts[3])
	if !ok {
		return "", "", 0, false
	}
	role = normalizeDashboardRole(roleValue)
	if role == "" {
		role = dashboardRoleViewer
	}
	epoch, err = strconv.ParseInt(parts[4], 10, 64)
	if err != nil {
		return "", "", 0, false
	}
	return username, role, epoch, true
}

func encodeSessionField(value string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(value))
}

func decodeSessionField(value string) (string, bool) {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", false
	}
	return string(decoded), true
}

func passwordMatches(got, stored string) bool {
	if stored == "" || isPlaceholderPassword(stored) || isPlaceholderPassword(got) {
		return false
	}
	if isBcryptHash(stored) {
		return bcrypt.CompareHashAndPassword([]byte(stored), []byte(got)) == nil
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(stored)) == 1
}

func isPlaceholderPassword(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), dashboardPlaceholderPassword)
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
	username = strings.ToLower(strings.TrimSpace(username))
	s.loginMu.Lock()
	if s.loginFailures == nil {
		s.loginFailures = map[string]loginAttempt{}
	}
	attempt := s.loginFailures[username]
	s.loginMu.Unlock()
	if loginAttemptLocked(attempt) {
		return true
	}
	stored := s.loadLoginLockout(r, username)
	return loginAttemptLocked(stored)
}

func (s *Server) recordLoginFailure(r *http.Request, username string) {
	username = strings.ToLower(strings.TrimSpace(username))
	s.loginMu.Lock()
	if s.loginFailures == nil {
		s.loginFailures = map[string]loginAttempt{}
	}
	now := time.Now()
	attempt := s.loginFailures[username]
	if now.Sub(attempt.WindowStart) > loginFailureWindow {
		attempt = loginAttempt{WindowStart: now}
	}
	attempt.Count++
	if attempt.Count >= loginFailureLimit {
		attempt.LockedUntil = now.Add(loginLockout)
	}
	s.loginFailures[username] = attempt
	s.loginMu.Unlock()
	_ = s.persistLoginLockout(r, username, attempt)
}

func (s *Server) clearLoginFailures(r *http.Request, username string) {
	username = strings.ToLower(strings.TrimSpace(username))
	s.loginMu.Lock()
	delete(s.loginFailures, username)
	s.loginMu.Unlock()
	_ = s.persistLoginLockout(r, username, loginAttempt{})
}

func loginAttemptLocked(attempt loginAttempt) bool {
	return !attempt.LockedUntil.IsZero() && time.Now().Before(attempt.LockedUntil)
}

func (s *Server) loadLoginLockout(r *http.Request, username string) loginAttempt {
	secret := &corev1.Secret{}
	err := s.Client.Get(r.Context(), client.ObjectKey{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace}, secret)
	if err != nil {
		return loginAttempt{}
	}
	lockouts := parseDashboardLoginLockouts(secret)
	return lockouts[username]
}

func (s *Server) persistLoginLockout(r *http.Request, username string, attempt loginAttempt) error {
	var lastErr error
	for i := 0; i < 8; i++ {
		secret := &corev1.Secret{}
		err := s.Client.Get(r.Context(), client.ObjectKey{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace}, secret)
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		lockouts := parseDashboardLoginLockouts(secret)
		if attempt.Count == 0 && attempt.LockedUntil.IsZero() {
			delete(lockouts, username)
		} else {
			lockouts[username] = attempt
		}
		payload, marshalErr := json.Marshal(lockouts)
		if marshalErr != nil {
			return marshalErr
		}
		latest := secret.DeepCopy()
		if latest.Data == nil {
			latest.Data = map[string][]byte{}
		}
		latest.Data[dashboardLoginLockoutsSecretKey] = payload
		lastErr = s.Client.Update(r.Context(), latest)
		if lastErr == nil || !apierrors.IsConflict(lastErr) {
			return lastErr
		}
	}
	return lastErr
}

func parseDashboardLoginLockouts(secret *corev1.Secret) map[string]loginAttempt {
	lockouts := map[string]loginAttempt{}
	if secret == nil || len(secret.Data[dashboardLoginLockoutsSecretKey]) == 0 {
		return lockouts
	}
	if err := json.Unmarshal(secret.Data[dashboardLoginLockoutsSecretKey], &lockouts); err != nil {
		return map[string]loginAttempt{}
	}
	return lockouts
}
