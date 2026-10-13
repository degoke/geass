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
	"maps"
	"net/http"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/degoke/geass/pkg/apitokens"
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

const (
	// loginSourceFailureLimit is how many failed sign-ins one client address may cause within loginFailureWindow
	// before it is refused for loginLockout. It sits well above loginFailureLimit: the per-username lock guards
	// an account, this only slows a flood, and behind a proxy every client shares one address.
	loginSourceFailureLimit = 20
	// loginSourceMaxEntries bounds how many client addresses are remembered, in memory.
	loginSourceMaxEntries = 4096
	// loginLockoutMaxEntries bounds the records kept in the Secret. Only configured usernames are ever stored,
	// so this is a backstop, not something a sign-in form can reach.
	loginLockoutMaxEntries = 256
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
	// CanReadSensitive is whether the session may read what is kept from viewers: logs, the names of a
	// service's variables, and the like. Admins can, and so can an observer token, which changes nothing.
	CanReadSensitive bool `json:"-"`
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
		if presented, ok := bearerToken(r); ok {
			record, err := s.authenticateAPIToken(r.Context(), presented)
			if err != nil {
				if errors.Is(err, errInvalidToken) {
					writeJSON(w, http.StatusUnauthorized, map[string]string{dashboardLiteralError: errInvalidToken.Error()})
				} else {
					writeJSON(w, http.StatusInternalServerError, map[string]string{dashboardLiteralError: "could not verify the API token"})
				}
				return
			}
			r = r.WithContext(context.WithValue(r.Context(), apiTokenContextKey{}, record))
			if record.Scope == apitokens.ScopeProject && !s.allowProjectToken(w, r, record) {
				return
			}
		}
		session := s.currentSession(r)
		if session == nil {
			if isJSONRequest(r) || strings.HasPrefix(r.URL.Path, "/api/") {
				writeJSON(w, http.StatusUnauthorized, map[string]string{dashboardLiteralError: "authentication required"})
				return
			}
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		if !session.CanMutate && (isDashboardWriteMethod(r) || dashboardWriteCallback(r.URL.Path) ||
			(dashboardSensitiveRead(r.URL.Path) && !session.CanReadSensitive)) {
			writeJSON(w, http.StatusForbidden, map[string]string{dashboardLiteralError: "viewer role cannot access this"})
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
	case strings.Contains(path, "/deployments/") && strings.HasSuffix(path, "/logs"):
		return true
	case strings.HasPrefix(path, "/api/apps/") && strings.HasSuffix(path, "/logs"):
		return true
	case strings.HasPrefix(path, "/api/apps/") && strings.HasSuffix(path, "/runtime"):
		return true
	case strings.HasPrefix(path, "/api/apps/") && strings.HasSuffix(path, "/variables"):
		return true
	case strings.HasPrefix(path, "/api/apps/") && strings.HasSuffix(path, "/console/ws"):
		return true
	case strings.HasPrefix(path, "/api/object-stores/") && (strings.HasSuffix(path, "/runtime") || strings.HasSuffix(path, "/logs")):
		return true
	case strings.HasPrefix(path, "/api/projects/") && strings.HasSuffix(path, "/github/repos"):
		return true
	case strings.HasPrefix(path, "/api/projects/") && (strings.HasSuffix(path, "/logs") || strings.HasSuffix(path, "/logs/stream") || strings.HasSuffix(path, "/logs/history")):
		return true
	case path == "/api/settings/github", path == "/api/templates", path == "/api/tokens", path == "/api/planetscale/cluster-sizes":
		return true
	default:
		return false
	}
}

func dashboardPublicPath(r *http.Request) bool {
	path := r.URL.Path
	switch {
	case path == platform.GeassDashboardProbePath, path == "/webhooks/github":
		return true
	case path == "/api/login", path == "/api/logout", path == "/api/session":
		return true
	// Signing in from a program: it starts and collects a request, and approving one takes a signed-in admin.
	case path == "/api/auth/device" && r.Method == http.MethodPost, path == "/api/auth/token" && r.Method == http.MethodPost:
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
	if s.loginSourceBlocked(r) {
		redirectFormError(w, r, "/", "invalid username or password")
		return
	}
	locked, err := s.loginLocked(r, username)
	if err != nil {
		redirectFormError(w, r, "/", dashboardAuthUnavailable)
		return
	}
	if locked {
		redirectFormError(w, r, "/", "invalid username or password")
		return
	}
	auth, err := s.dashboardAuth(r)
	if err != nil {
		redirectFormError(w, r, "/", dashboardAuthUnavailable)
		return
	}
	user := auth.lookup(username)
	if user == nil || !passwordMatches(r.FormValue("password"), user.Password) {
		if err := s.recordLoginFailure(r, user); err != nil {
			redirectFormError(w, r, "/", dashboardAuthUnavailable)
			return
		}
		redirectFormError(w, r, "/", "invalid username or password")
		return
	}
	if err := s.clearLoginFailures(r, username); err != nil {
		redirectFormError(w, r, "/", "could not create a session")
		return
	}
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

func (s *Server) sessionCanMutate(r *http.Request) bool {
	session := s.currentSession(r)
	return session != nil && session.CanMutate
}

func (s *Server) currentSession(r *http.Request) *dashboardSessionInfo {
	if record := tokenFromRequest(r); record != nil {
		return tokenSession(record)
	}
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
	return &dashboardSessionInfo{Username: user.Username, Role: liveRole, CanMutate: liveRole == dashboardRoleAdmin, CanReadSensitive: liveRole == dashboardRoleAdmin}
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
		parsedUsers, parseUsersErr := parseDashboardUsersSecret(secret)
		if parseUsersErr != nil {
			return nil, nil, nil, nil, err, parseUsersErr
		}
		secretUsers = parsedUsers
		if len(sessionKey) == 0 {
			sessionKey = secret.Data["session-key"]
		}
		parsedEpochs, parseErr := parseDashboardSessionEpochs(secret)
		if parseErr != nil {
			return nil, nil, nil, nil, err, parseErr
		}
		epochs = parsedEpochs
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
	if dashboardUsersHaveDuplicates(users) {
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
		parsedEpochs, parseErr := parseDashboardSessionEpochs(secret)
		if parseErr != nil {
			return parseErr
		}
		epochs = parsedEpochs
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	normalized := normalizeDashboardUsername(username)
	current := int64(0)
	for key, epoch := range epochs {
		if normalizeDashboardUsername(key) == normalized {
			current = epoch
			delete(epochs, key)
		}
	}
	epochs[normalized] = current + 1
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

func normalizeDashboardUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

func (a *dashboardAuth) lookup(username string) *dashboardUser {
	username = normalizeDashboardUsername(username)
	if username == "" || a == nil {
		return nil
	}
	for i := range a.users {
		if normalizeDashboardUsername(a.users[i].Username) == username && a.users[i].Password != "" {
			return &a.users[i]
		}
	}
	return nil
}

func (a *dashboardAuth) epoch(username string) int64 {
	if a == nil || a.epochs == nil {
		return 0
	}
	username = normalizeDashboardUsername(username)
	for key, epoch := range a.epochs {
		if normalizeDashboardUsername(key) == username {
			return epoch
		}
	}
	return 0
}

func parseDashboardUsersSecret(secret *corev1.Secret) ([]dashboardUser, error) {
	if secret == nil {
		return nil, nil
	}
	raw := secret.Data["users"]
	if len(raw) == 0 {
		return nil, nil
	}
	var users []dashboardUser
	if err := json.Unmarshal(raw, &users); err != nil {
		return nil, err
	}
	filtered := filterDashboardUsers(users, false)
	if len(filtered) == 0 {
		return nil, errDashboardAuthUnconfigured
	}
	return filtered, nil
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
	for part := range strings.SplitSeq(value, ",") {
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

func dashboardUsersHaveDuplicates(users []dashboardUser) bool {
	seen := map[string]struct{}{}
	for _, user := range users {
		key := normalizeDashboardUsername(user.Username)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			return true
		}
		seen[key] = struct{}{}
	}
	return false
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

func parseDashboardSessionEpochs(secret *corev1.Secret) (map[string]int64, error) {
	epochs := map[string]int64{}
	if secret == nil || len(secret.Data["session-epochs"]) == 0 {
		return epochs, nil
	}
	if err := json.Unmarshal(secret.Data["session-epochs"], &epochs); err != nil {
		return nil, err
	}
	if epochs == nil {
		epochs = map[string]int64{}
	}
	return epochs, nil
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

func (s *Server) loginLocked(r *http.Request, username string) (bool, error) {
	username = normalizeDashboardUsername(username)
	s.loginMu.Lock()
	if s.loginFailures == nil {
		s.loginFailures = map[string]loginAttempt{}
	}
	attempt := s.loginFailures[username]
	s.loginMu.Unlock()
	if loginAttemptLocked(attempt) {
		return true, nil
	}
	stored, err := s.loadLoginLockout(r, username)
	if err != nil {
		return false, err
	}
	return loginAttemptLocked(stored), nil
}

// recordLoginFailure counts a failed sign-in against the client address and, when the name given belongs to a
// configured user, against that user too. user is nil for any other name, and nothing is kept for it: a name
// typed into the form must never decide what the Secret, or the operator's memory, holds.
func (s *Server) recordLoginFailure(r *http.Request, user *dashboardUser) error {
	s.noteLoginSourceFailure(r)
	if user == nil {
		return nil
	}
	username := normalizeDashboardUsername(user.Username)
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
	s.loginMu.Unlock()
	if err := s.persistLoginLockout(r, username, attempt); err != nil {
		return err
	}
	s.loginMu.Lock()
	if s.loginFailures == nil {
		s.loginFailures = map[string]loginAttempt{}
	}
	s.loginFailures[username] = attempt
	s.loginMu.Unlock()
	return nil
}

func (s *Server) clearLoginFailures(r *http.Request, username string) error {
	username = normalizeDashboardUsername(username)
	s.loginMu.Lock()
	delete(s.loginFailures, username)
	s.loginMu.Unlock()
	return s.persistLoginLockout(r, username, loginAttempt{})
}

func loginAttemptLocked(attempt loginAttempt) bool {
	return !attempt.LockedUntil.IsZero() && time.Now().Before(attempt.LockedUntil)
}

// loginAttemptExpired is whether a record can no longer change an outcome: its window has ended and any lock
// has lifted, so the next failure would start a fresh window anyway.
func loginAttemptExpired(attempt loginAttempt, now time.Time) bool {
	return now.Sub(attempt.WindowStart) > loginFailureWindow && !now.Before(attempt.LockedUntil)
}

// loginSourceKey names the client address a request came from. An IPv4-mapped IPv6 address counts as the IPv4
// one, and an IPv6 address counts as its /64, which is what a single subscriber is handed, so rotating through
// a block does not shake the limiter off.
func loginSourceKey(r *http.Request) string {
	host := clientAddress(r)
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	addr = addr.Unmap().WithZone("")
	if addr.Is6() {
		if prefix, err := addr.Prefix(64); err == nil {
			return prefix.String()
		}
	}
	return addr.String()
}

// loginSourceBlocked is whether the address of this request has failed too many sign-ins lately.
func (s *Server) loginSourceBlocked(r *http.Request) bool {
	key := loginSourceKey(r)
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	return loginAttemptLocked(s.loginSources[key])
}

// noteLoginSourceFailure counts one failed sign-in against the address of the request. The table lives in
// memory and is bounded: when it is full it drops what has expired and, if that frees nothing, starts over.
// Starting over only helps someone who controls thousands of addresses at once, and the per-username lock
// does not depend on it.
func (s *Server) noteLoginSourceFailure(r *http.Request) {
	key := loginSourceKey(r)
	now := time.Now()
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	if s.loginSources == nil {
		s.loginSources = map[string]loginAttempt{}
	}
	attempt, tracked := s.loginSources[key]
	if !tracked && len(s.loginSources) >= loginSourceMaxEntries {
		for source, other := range s.loginSources {
			if loginAttemptExpired(other, now) {
				delete(s.loginSources, source)
			}
		}
		if len(s.loginSources) >= loginSourceMaxEntries {
			clear(s.loginSources)
		}
	}
	if now.Sub(attempt.WindowStart) > loginFailureWindow {
		attempt = loginAttempt{WindowStart: now}
	}
	attempt.Count++
	if attempt.Count >= loginSourceFailureLimit {
		attempt.LockedUntil = now.Add(loginLockout)
	}
	s.loginSources[key] = attempt
}

// pruneLoginLockouts drops the stored records that no longer matter and then, if more than
// loginLockoutMaxEntries remain, the ones whose window began longest ago. The record under keep is the one
// being written and always stays.
func pruneLoginLockouts(lockouts map[string]loginAttempt, keep string, now time.Time) {
	for name, attempt := range lockouts {
		if name != keep && loginAttemptExpired(attempt, now) {
			delete(lockouts, name)
		}
	}
	if len(lockouts) <= loginLockoutMaxEntries {
		return
	}
	names := slices.Collect(maps.Keys(lockouts))
	slices.SortFunc(names, func(a, b string) int { return lockouts[a].WindowStart.Compare(lockouts[b].WindowStart) })
	for _, name := range names {
		if len(lockouts) <= loginLockoutMaxEntries {
			return
		}
		if name != keep {
			delete(lockouts, name)
		}
	}
}

func (s *Server) loadLoginLockout(r *http.Request, username string) (loginAttempt, error) {
	secret := &corev1.Secret{}
	err := s.Client.Get(r.Context(), client.ObjectKey{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace}, secret)
	if apierrors.IsNotFound(err) {
		return loginAttempt{}, nil
	}
	if err != nil {
		return loginAttempt{}, err
	}
	lockouts, err := parseDashboardLoginLockouts(secret)
	if err != nil {
		return loginAttempt{}, err
	}
	return lockouts[username], nil
}

func (s *Server) persistLoginLockout(r *http.Request, username string, attempt loginAttempt) error {
	var lastErr error
	for range 8 {
		secret := &corev1.Secret{}
		err := s.Client.Get(r.Context(), client.ObjectKey{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace}, secret)
		if apierrors.IsNotFound(err) {
			if attempt.Count == 0 && attempt.LockedUntil.IsZero() {
				return nil
			}
			lockouts := map[string]loginAttempt{username: attempt}
			payload, marshalErr := json.Marshal(lockouts)
			if marshalErr != nil {
				return marshalErr
			}
			createErr := s.Client.Create(r.Context(), &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace},
				Type:       corev1.SecretTypeOpaque,
				StringData: map[string]string{dashboardLoginLockoutsSecretKey: string(payload)},
			})
			if createErr == nil {
				return nil
			}
			if !apierrors.IsAlreadyExists(createErr) {
				return createErr
			}
			continue
		}
		if err != nil {
			return err
		}
		lockouts, parseErr := parseDashboardLoginLockouts(secret)
		if parseErr != nil {
			return parseErr
		}
		if attempt.Count == 0 && attempt.LockedUntil.IsZero() {
			delete(lockouts, username)
		} else {
			lockouts[username] = attempt
		}
		pruneLoginLockouts(lockouts, username, time.Now())
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

func parseDashboardLoginLockouts(secret *corev1.Secret) (map[string]loginAttempt, error) {
	lockouts := map[string]loginAttempt{}
	if secret == nil || len(secret.Data[dashboardLoginLockoutsSecretKey]) == 0 {
		return lockouts, nil
	}
	if err := json.Unmarshal(secret.Data[dashboardLoginLockoutsSecretKey], &lockouts); err != nil {
		return nil, err
	}
	if lockouts == nil {
		lockouts = map[string]loginAttempt{}
	}
	return lockouts, nil
}
