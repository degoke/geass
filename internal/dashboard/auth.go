package dashboard

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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

type dashboardAuth struct {
	password   string
	sessionKey []byte
}

func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if dashboardPublicPath(r) {
			next.ServeHTTP(w, r)
			return
		}
		if s.sessionValid(r) {
			next.ServeHTTP(w, r)
			return
		}
		if isJSONRequest(r) || strings.HasPrefix(r.URL.Path, "/api/") {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}
		http.Error(w, "authentication required", http.StatusUnauthorized)
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
	if !passwordEqual(r.FormValue("password"), auth.password) {
		redirectFormError(w, r, "/", "invalid password")
		return
	}
	token, err := issueDashboardSession(auth.sessionKey)
	if err != nil {
		redirectFormError(w, r, "/", "could not create a session")
		return
	}
	http.SetCookie(w, dashboardSessionCookie(r, token, int(dashboardSessionTTL.Seconds())))
	if isJSONRequest(r) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "authenticated": true})
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
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": s.sessionValid(r)})
}

func (s *Server) sessionValid(r *http.Request) bool {
	cookie, err := r.Cookie(platform.DashboardSessionCookie)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		return false
	}
	auth, err := s.dashboardAuth(r)
	if err != nil {
		return false
	}
	return dashboardSessionValid(auth.sessionKey, cookie.Value)
}

func (s *Server) dashboardAuth(r *http.Request) (*dashboardAuth, error) {
	s.authMu.Lock()
	defer s.authMu.Unlock()
	if s.auth != nil && s.auth.password != "" && len(s.auth.sessionKey) > 0 {
		return s.auth, nil
	}
	password := strings.TrimSpace(os.Getenv("GEASS_DASHBOARD_PASSWORD"))
	sessionKey := []byte(strings.TrimSpace(os.Getenv("GEASS_DASHBOARD_SESSION_KEY")))
	secret := &corev1.Secret{}
	err := s.Client.Get(r.Context(), client.ObjectKey{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace}, secret)
	if err == nil {
		if password == "" {
			password = string(secret.Data["password"])
		}
		if len(sessionKey) == 0 {
			sessionKey = secret.Data["session-key"]
		}
	} else if !apierrors.IsNotFound(err) {
		return nil, err
	}
	if password == "" || len(sessionKey) == 0 {
		if password == "" {
			generated, genErr := randomDashboardSecret(24)
			if genErr != nil {
				return nil, genErr
			}
			password = generated
		}
		if len(sessionKey) == 0 {
			generated, genErr := randomDashboardBytes(32)
			if genErr != nil {
				return nil, genErr
			}
			sessionKey = generated
		}
		created := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: platform.DashboardAuthSecretName, Namespace: platform.SystemNamespace},
			Type:       corev1.SecretTypeOpaque,
			StringData: map[string]string{
				"password":    password,
				"session-key": hex.EncodeToString(sessionKey),
			},
		}
		if err := s.Client.Create(r.Context(), created); err != nil && !apierrors.IsAlreadyExists(err) {
			return nil, err
		}
	}
	if decoded, err := hex.DecodeString(string(sessionKey)); err == nil && len(decoded) >= 16 {
		sessionKey = decoded
	}
	s.auth = &dashboardAuth{password: password, sessionKey: sessionKey}
	return s.auth, nil
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

func issueDashboardSession(sessionKey []byte) (string, error) {
	nonce, err := randomDashboardBytes(16)
	if err != nil {
		return "", err
	}
	expiry := strconv.FormatInt(time.Now().Add(dashboardSessionTTL).Unix(), 10)
	payload := expiry + ":" + hex.EncodeToString(nonce)
	mac := hmacSHA256Bytes(sessionKey, payload)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + hex.EncodeToString(mac), nil
}

func dashboardSessionValid(sessionKey []byte, token string) bool {
	encoded, macHex, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return false
	}
	expected := hmacSHA256Bytes(sessionKey, string(payload))
	if !hmac.Equal([]byte(hex.EncodeToString(expected)), []byte(macHex)) {
		return false
	}
	expiryText, _, ok := strings.Cut(string(payload), ":")
	if !ok {
		return false
	}
	expiry, err := strconv.ParseInt(expiryText, 10, 64)
	if err != nil {
		return false
	}
	return time.Now().Unix() < expiry
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
