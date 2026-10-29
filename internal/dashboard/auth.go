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
	dashboardRoleAdmin  = "admin"
	dashboardRoleViewer = "viewer"
	loginFailureLimit   = 5
	// loginUserFailureLimit is how many failed sign-ins for one username, from every client together, lock it for
	// everyone, when the clients can be told apart (see loginSourceKey). Each client has its own lock after
	// loginFailureLimit of its own, so one that keeps guessing is shut out without shutting out the person who owns the
	// account; this is the backstop for many clients guessing at once.
	loginUserFailureLimit           = 25
	loginFailureWindow              = time.Minute
	loginLockout                    = time.Minute
	dashboardSessionKeyPrefix       = "hex:"
	dashboardPlaceholderPassword    = "CHANGE_ME"
	dashboardLoginLockoutsSecretKey = "login-lockouts"
)

const (
	// loginSourceFailureLimit is how many failed sign-ins one client address may cause within loginFailureWindow
	// before it is refused for loginLockout. It sits well above loginFailureLimit: the per-username lock guards
	// an account, this only slows a flood. It counts only clients Geass can tell apart (see loginSourceKey).
	loginSourceFailureLimit = 20
	// loginSourceMaxEntries bounds how many client addresses are remembered, in memory.
	loginSourceMaxEntries = 4096
	// loginLockoutMaxEntries bounds the records kept in the Secret. Only configured usernames are ever stored,
	// so this is a backstop, not something a sign-in form can reach.
	loginLockoutMaxEntries = 256
)

// dashboardDummyPasswordHash is a bcrypt hash, at the cost of the hashes Geass writes, of a password nobody
// holds. A sign-in for a name that is not configured is compared against it, so it takes as long as one for a
// name that is, and the time of a reply does not say which usernames exist.
const dashboardDummyPasswordHash = "$2a$10$Q8UjcLp2ioi1VYVVp6qFZOkQUvAva4O.gEuTuXG/sUMWQMep1M//W"

// loginSharedPeers is the shared address space (RFC 6598) that clusters and VPNs hand to pods and nodes.
var loginSharedPeers = netip.MustParsePrefix("100.64.0.0/10")

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
	// ssoRevoked is, for each person who signed in through an identity provider, the moment (Unix milliseconds)
	// before which their sessions no longer count.
	ssoRevoked map[string]int64
	// ssoNotBefore is the moment (Unix milliseconds) before which no session of a person from the provider counts.
	ssoNotBefore int64
}

type dashboardSessionInfo struct {
	Username  string `json:"username"`
	Role      string `json:"role"`
	CanMutate bool   `json:"canMutate"`
	// CanReadSensitive is whether the session may read what is kept from viewers: logs, the names of a
	// service's variables, and the like. Admins can, and so can an observer token, which changes nothing.
	CanReadSensitive bool `json:"-"`
	// Identity names a person who signed in through an identity provider (their stable key there), and is empty for
	// a local user. It is what the tokens they make are tied to.
	Identity string `json:"-"`
	// PlatformAdmin is whether the session acts for the platform as a whole and not for an organization: its
	// password administrators, install-wide tokens, and anyone the provider names as one. While organizations are off
	// everyone acts for the platform, whatever this says.
	PlatformAdmin bool `json:"platformAdmin,omitempty"`
	// Org and Workspace are what an organization's session acts for: the GeassOrganization and the workspace in it
	// ("" for all of the organization's, which only its administrators are given).
	Org       string `json:"org,omitempty"`
	Workspace string `json:"workspace,omitempty"`
	// Grants are where a person from the provider may work, which they choose between. They are in the session's cookie;
	// the dashboard lists them with their names (see Scopes).
	Grants []sessionGrant `json:"-"`
	// Scopes is where the person may work, for the page to offer; it is filled in where a session is told to the page.
	Scopes []sessionScope `json:"scopes,omitempty"`
}

type loginAttempt struct {
	Count       int       `json:"count"`
	WindowStart time.Time `json:"windowStart"`
	LockedUntil time.Time `json:"lockedUntil"`
}

func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Who is asking is worked out by the dashboard for itself: it reads the users and the tokens, which no
		// organization may. The handler then runs for the tenant that comes out of it, or, for a path that is public,
		// as the dashboard's own work, which is what it has always been.
		r = r.WithContext(asSystem(r.Context()))
		if dashboardPublicPath(r) {
			next.ServeHTTP(w, r)
			return
		}
		if presented, ok := bearerToken(r); ok {
			if isOIDCBearerCandidate(presented) {
				// Not one of Geass's own tokens: the identity provider's access token, when that is turned on. The person it
				// stands for is the session of this request, which is what the handlers below read.
				session, bearer, err := s.authenticateOIDCAccessToken(r, presented)
				if err != nil {
					writeOIDCBearerError(w, err)
					return
				}
				r = r.WithContext(context.WithValue(context.WithValue(r.Context(), oidcBearerKey{}, bearer), sessionContextKey{}, session))
			} else {
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
		}
		session := s.currentSession(r)
		if session != nil {
			// What the handlers below ask again is answered from here, not by reading the users Secret once more.
			r = r.WithContext(context.WithValue(r.Context(), sessionContextKey{}, session))
		}
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
		tenant := s.tenantOfSession(r, session)
		if routeScopeOf(r.Method, r.URL.Path) == scopePlatform && !tenant.Platform {
			writeJSON(w, http.StatusForbidden, map[string]string{dashboardLiteralError: "only the platform's administrators can do this"})
			return
		}
		r = r.WithContext(withTenant(r.Context(), tenant))
		// A stream or a terminal that stays open is checked against its credentials again from time to time.
		r, stop := s.guardStream(r)
		defer stop()
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
	case path == "/api/settings/github", path == "/api/settings/auth", path == "/api/settings/alerts", path == "/api/templates", path == "/api/tokens", path == "/api/planetscale/cluster-sizes":
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
	// The provider of an external GitHub App forwards GitHub's events: its signature with the secret it shares with the
	// cluster is the credential.
	case strings.HasPrefix(path, githubForwardedPrefix):
		return true
	case path == "/api/login", path == "/api/logout", path == "/api/session":
		return true
	// A deploy hook is called by a tool that cannot sign in: the secret in its URL is the credential. Every method
	// gets through to the handler, which answers anything but POST with 405.
	case strings.HasPrefix(path, deployHookPrefix):
		return true
	// The identity provider tells Geass that a person signed out there. Its signature on the token is the credential.
	case path == oidcLogoutPath && r.Method == http.MethodPost:
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
	source := loginSourceKey(r)
	if s.loginSourceBlocked(source) {
		redirectFormError(w, r, "/", "invalid username or password")
		return
	}
	auth, err := s.dashboardAuth(r)
	if err != nil {
		redirectFormError(w, r, "/", dashboardAuthUnavailable)
		return
	}
	user := auth.lookup(username)
	// While an identity provider decides who signs in, a password works only for an admin. A name it does not allow is
	// treated as one that does not exist, so the answer does not say which names are users. Whether the provider decides
	// is read for every attempt, whatever the name, so that the work does not depend on whether the name exists. If it cannot
	// be told, it is assumed that the provider does: a password that should be refused must not be accepted because a read
	// failed.
	config, configErr := s.platformConfig(r.Context())
	if user != nil && normalizeDashboardRole(user.Role) != dashboardRoleAdmin && (configErr != nil || ssoSettingsOf(config).External()) {
		user = nil
	}
	// Every name costs one bcrypt comparison, a configured user's against its own hash and any other name's
	// against a dummy, before anything else is decided: neither the time of the reply nor the lock tells a
	// caller whether a name exists.
	stored := dashboardDummyPasswordHash
	if user != nil {
		stored = user.Password
	}
	passwordOK := passwordMatches(r.FormValue("password"), stored)
	// Only a configured user can have a lock, so any other name skips reading the stored lockouts.
	if user != nil {
		locked, err := s.loginLocked(r, user.Username, source)
		if err != nil {
			redirectFormError(w, r, "/", dashboardAuthUnavailable)
			return
		}
		if locked {
			redirectFormError(w, r, "/", "invalid username or password")
			return
		}
	}
	if user == nil || !passwordOK {
		s.noteLoginSourceFailure(source)
		if err := s.recordLoginFailure(r, user, source); err != nil {
			redirectFormError(w, r, "/", dashboardAuthUnavailable)
			return
		}
		redirectFormError(w, r, "/", "invalid username or password")
		return
	}
	if err := s.clearLoginFailures(r, username, source); err != nil {
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
		var err error
		if session.Identity != "" {
			err = s.revokeSSOIdentity(r.Context(), session.Identity)
		} else {
			err = s.revokeDashboardSession(r, session.Username)
		}
		if err != nil {
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
		// sso tells the sign-in page to offer the identity provider, and what to call it.
		answer := map[string]any{"authenticated": false}
		// Only the configuration is read, not the client secret: this answer is public.
		if config, err := s.platformConfig(r.Context()); err == nil {
			if settings := ssoSettingsOf(config); settings.External() {
				answer["sso"] = map[string]any{pathNameParam: settings.ssoName()}
			}
		}
		writeJSON(w, http.StatusOK, answer)
		return
	}
	answer := map[string]any{"authenticated": true, "username": session.Username, "role": session.Role, "canMutate": session.CanMutate}
	// Where the person works, and where else they may, once organizations are on.
	if scopes := s.sessionScopes(r.Context(), session); len(scopes) > 0 {
		answer["org"], answer["workspace"], answer["platformAdmin"], answer["scopes"] = session.Org, session.Workspace, session.PlatformAdmin, scopes
	}
	writeJSON(w, http.StatusOK, answer)
}

func (s *Server) sessionCanMutate(r *http.Request) bool {
	session := s.currentSession(r)
	return session != nil && session.CanMutate
}

// sessionContextKey carries the session that withAuth worked out for a request, so that the handlers that ask who is
// signed in again (several do, for the name and for the role) do not each read and parse the users Secret.
type sessionContextKey struct{}

func (s *Server) currentSession(r *http.Request) *dashboardSessionInfo {
	if cached, ok := r.Context().Value(sessionContextKey{}).(*dashboardSessionInfo); ok {
		return cached
	}
	if record := tokenFromRequest(r); record != nil {
		return tokenSession(record)
	}
	cookie, err := r.Cookie(cookieName(r, platform.DashboardSessionCookie))
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return nil
	}
	auth, err := s.authForRequest(r)
	if err != nil {
		return nil
	}
	if claims, ok := parseSSOSession(auth.sessionKey, cookie.Value, time.Now()); ok {
		return s.ssoSession(r, auth, claims)
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
	return &dashboardSessionInfo{Username: user.Username, Role: liveRole, CanMutate: liveRole == dashboardRoleAdmin, CanReadSensitive: liveRole == dashboardRoleAdmin, PlatformAdmin: true}
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
	auth := &dashboardAuth{users: users, sessionKey: sessionKey, epochs: epochs, ssoRevoked: parseSSORevocations(secret), ssoNotBefore: parseSSONotBefore(secret)}
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
	err := s.Client.Get(asSystem(r.Context()), client.ObjectKey{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace}, secret)
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
		if createErr := s.Client.Create(asSystem(r.Context()), created); createErr != nil {
			if !apierrors.IsAlreadyExists(createErr) {
				return nil, createErr
			}
			latest := &corev1.Secret{}
			if getLatest := s.Client.Get(asSystem(r.Context()), client.ObjectKey{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace}, latest); getLatest != nil {
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
	if err := s.Client.Update(asSystem(r.Context()), latest); err != nil {
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
		if createErr := s.Client.Create(asSystem(r.Context()), created); createErr != nil {
			if !apierrors.IsAlreadyExists(createErr) {
				return createErr
			}
			latest := &corev1.Secret{}
			if getLatest := s.Client.Get(asSystem(r.Context()), client.ObjectKey{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace}, latest); getLatest != nil {
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
	return s.Client.Update(asSystem(ctx), latest)
}

func (s *Server) revokeDashboardSession(r *http.Request, username string) error {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	secret := &corev1.Secret{}
	err := s.Client.Get(asSystem(r.Context()), client.ObjectKey{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace}, secret)
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
		return s.Client.Create(asSystem(r.Context()), &corev1.Secret{
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
	return s.Client.Update(asSystem(r.Context()), latest)
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

// cookieName is the name a cookie is set and read under for this request. Where the cookie is Secure it carries the
// "__Host-" prefix, which a browser accepts only for a Secure cookie with Path=/ and no Domain, and which a page of another
// host cannot set. That matters because tenants' services run on sibling subdomains of the dashboard: without the prefix, such
// a page could set a cookie of the same name for the whole domain, and the dashboard would read the planted one (a session of
// the planter's choosing, or a state that lets a forged sign-in through). Over plain http on this machine, where a Secure cookie
// is not wanted, the plain name is used.
func cookieName(r *http.Request, name string) string {
	if dashboardCookieSecure(r) {
		return "__Host-" + name
	}
	return name
}

func dashboardSessionCookie(r *http.Request, value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     cookieName(r, platform.DashboardSessionCookie),
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

func (s *Server) loginLocked(r *http.Request, username, source string) (bool, error) {
	username = normalizeDashboardUsername(username)
	s.loginMu.Lock()
	if s.loginFailures == nil {
		s.loginFailures = map[string]loginAttempt{}
	}
	attempt := s.loginFailures[username]
	pair := s.loginPairs[loginPairKey(username, source)]
	s.loginMu.Unlock()
	// A client that was locked out of this username by its own failures stays locked out; the others are not.
	if source != "" && loginAttemptLocked(pair) {
		return true, nil
	}
	if loginAttemptLocked(attempt) {
		return true, nil
	}
	stored, err := s.loadLoginLockout(r, username)
	if err != nil {
		return false, err
	}
	return loginAttemptLocked(stored), nil
}

// recordLoginFailure counts a failed sign-in against the configured user it was for. user is nil for any other
// name, and nothing is kept for it: a name typed into the form must never decide what the Secret, or the
// operator's memory, holds.
func (s *Server) recordLoginFailure(r *http.Request, user *dashboardUser, source string) error {
	if user == nil {
		return nil
	}
	username := normalizeDashboardUsername(user.Username)
	// Where the client can be told apart from the others, its failures lock it (alone) after loginFailureLimit, and the
	// username is locked for everyone only after loginUserFailureLimit of them from all clients. Where it cannot (every
	// visitor arrives from one private address), the username is all there is to lock, after loginFailureLimit.
	limit := loginFailureLimit
	if source != "" {
		limit = loginUserFailureLimit
	}
	s.loginMu.Lock()
	if s.loginFailures == nil {
		s.loginFailures = map[string]loginAttempt{}
	}
	now := time.Now()
	if source != "" {
		s.noteLoginPairFailureLocked(loginPairKey(username, source), now)
	}
	attempt := s.loginFailures[username]
	if now.Sub(attempt.WindowStart) > loginFailureWindow {
		attempt = loginAttempt{WindowStart: now}
	}
	attempt.Count++
	if attempt.Count >= limit {
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

func (s *Server) clearLoginFailures(r *http.Request, username, source string) error {
	username = normalizeDashboardUsername(username)
	s.loginMu.Lock()
	delete(s.loginFailures, username)
	delete(s.loginPairs, loginPairKey(username, source))
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

// loginSourceKey names the client a request came from, or is empty when the address says nothing about the
// client. A private, loopback, link-local or shared-space peer is a proxy, a tunnel or the cluster itself, where
// one address stands for every visitor, so limiting it would let anyone lock everybody out; those are left to
// the per-username lock. An IPv4-mapped IPv6 address counts as the IPv4 one, and an IPv6 address as its /64.
func loginSourceKey(r *http.Request) string {
	addr, err := netip.ParseAddr(clientIP(r))
	if err != nil {
		return ""
	}
	addr = addr.Unmap().WithZone("")
	if privateAddress(addr) {
		return ""
	}
	if addr.Is6() {
		if prefix, err := addr.Prefix(64); err == nil {
			return prefix.String()
		}
	}
	return addr.String()
}

// loginSourceBlocked is whether the client with this key has failed too many sign-ins lately.
func (s *Server) loginSourceBlocked(key string) bool {
	if key == "" {
		return false
	}
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	return loginAttemptLocked(s.loginSources[key])
}

// noteLoginSourceFailure counts one failed sign-in against the client with this key. The table lives in memory
// and is bounded, and a flood of fresh addresses cannot push a block out of it (see evictLoginSources). A client
// that rotates through IPv6 /64 prefixes gets a fresh allowance for each, so this slows a flood without stopping
// one: it is the per-username lock that protects an account.
func (s *Server) noteLoginSourceFailure(key string) {
	if key == "" {
		return
	}
	now := time.Now()
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	if s.loginSources == nil {
		s.loginSources = map[string]loginAttempt{}
	}
	attempt, tracked := s.loginSources[key]
	if !tracked && len(s.loginSources) >= loginSourceMaxEntries {
		evictLoginSources(s.loginSources, now)
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

// evictLoginSources makes room in the full address table for one more client. It drops what has expired and, if
// that is not enough, every client that is not blocked right now (their counts start over with their next
// failure), and only then the block that ends soonest. Blocks are what the table is for, so a flood of fresh
// addresses, each failing once, never costs a block.
func evictLoginSources(sources map[string]loginAttempt, now time.Time) {
	for key, attempt := range sources {
		if loginAttemptExpired(attempt, now) {
			delete(sources, key)
		}
	}
	if len(sources) < loginSourceMaxEntries {
		return
	}
	for key, attempt := range sources {
		if !now.Before(attempt.LockedUntil) {
			delete(sources, key)
		}
	}
	if len(sources) < loginSourceMaxEntries {
		return
	}
	soonest := ""
	for key, attempt := range sources {
		if soonest == "" || attempt.LockedUntil.Before(sources[soonest].LockedUntil) {
			soonest = key
		}
	}
	delete(sources, soonest)
}

// pruneLoginLockouts drops the stored records that no longer matter and then, if more than
// loginLockoutMaxEntries remain, the ones whose window began longest ago, quiet records before locked ones so
// that no lock is lost while one that is not locked can go instead. The record under keep is the one being
// written and always stays.
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
	slices.SortFunc(names, func(a, b string) int {
		lockedA, lockedB := now.Before(lockouts[a].LockedUntil), now.Before(lockouts[b].LockedUntil)
		if lockedA != lockedB {
			if lockedA {
				return 1
			}
			return -1
		}
		return lockouts[a].WindowStart.Compare(lockouts[b].WindowStart)
	})
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
	err := s.Client.Get(asSystem(r.Context()), client.ObjectKey{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace}, secret)
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
		err := s.Client.Get(asSystem(r.Context()), client.ObjectKey{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace}, secret)
		if apierrors.IsNotFound(err) {
			if attempt.Count == 0 && attempt.LockedUntil.IsZero() {
				return nil
			}
			lockouts := map[string]loginAttempt{username: attempt}
			payload, marshalErr := json.Marshal(lockouts)
			if marshalErr != nil {
				return marshalErr
			}
			createErr := s.Client.Create(asSystem(r.Context()), &corev1.Secret{
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
		lastErr = s.Client.Update(asSystem(r.Context()), latest)
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

// privateAddress is whether an address is one a visitor from the internet cannot have: private, this machine, link-local
// or in the shared space that clusters and VPNs hand out. A connection from one is a proxy's, a tunnel's or a neighbour's.
func privateAddress(addr netip.Addr) bool {
	addr = addr.Unmap().WithZone("")
	return addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsUnspecified() || loginSharedPeers.Contains(addr)
}

// clientIP is the address of whoever is really making a request. One that comes from a public address is from that
// address, whatever its headers say. One that comes from a private address, as everything does through the cluster's
// ingress or a tunnel, says who it is for in X-Forwarded-For, which is read from the right: each proxy adds the address it
// was spoken to from, so the right-most address that is not itself private is the one the nearest proxy saw, and what a
// client wrote before it is not believed. When the header is missing or cannot be read the answer is the peer's address.
//
// A caller inside the cluster can write that header too, and choose its address. What that buys it is another client's
// allowance of failed sign-ins, no more than loginUserFailureLimit of them for a username in all, so it is not a way past the
// lock on an account.
func clientIP(r *http.Request) string {
	peer := clientAddress(r)
	addr, err := netip.ParseAddr(peer)
	if err != nil || !privateAddress(addr) {
		return peer
	}
	if forwarded := forwardedClient(r); forwarded != "" {
		return forwarded
	}
	return peer
}

// forwardedClient is the right-most public address in X-Forwarded-For, or "" when there is none or an entry cannot be read
// (then nothing in the header is trusted).
func forwardedClient(r *http.Request) string {
	lines := r.Header.Values("X-Forwarded-For")
	entries := make([]string, 0, len(lines))
	for _, line := range lines {
		entries = append(entries, strings.Split(line, ",")...)
	}
	for _, entry := range slices.Backward(entries) {
		entry = strings.TrimSpace(entry)
		addr, err := netip.ParseAddr(entry)
		if err != nil {
			// Some proxies write the port too.
			withPort, portErr := netip.ParseAddrPort(entry)
			if portErr != nil {
				return ""
			}
			addr = withPort.Addr()
		}
		if !privateAddress(addr) {
			return addr.Unmap().WithZone("").String()
		}
	}
	return ""
}

func loginPairKey(username, source string) string { return username + "|" + source }

// noteLoginPairFailureLocked counts one failed sign-in for a username from a client address, with loginMu held.
func (s *Server) noteLoginPairFailureLocked(key string, now time.Time) {
	if s.loginPairs == nil {
		s.loginPairs = map[string]loginAttempt{}
	}
	attempt, tracked := s.loginPairs[key]
	if !tracked && len(s.loginPairs) >= loginSourceMaxEntries {
		evictLoginSources(s.loginPairs, now)
	}
	if now.Sub(attempt.WindowStart) > loginFailureWindow {
		attempt = loginAttempt{WindowStart: now}
	}
	attempt.Count++
	if attempt.Count >= loginFailureLimit {
		attempt.LockedUntil = now.Add(loginLockout)
	}
	s.loginPairs[key] = attempt
}
