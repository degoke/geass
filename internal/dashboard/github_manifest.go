package dashboard

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/githubapp"
	"github.com/degoke/geass/pkg/platform"
)

const githubManifestStateCookie = "geass_github_manifest_state_"

func githubManifestStateCookieName(state string) string {
	return githubManifestStateCookie + state
}

func (s *Server) beginGitHubManifestState(w http.ResponseWriter, r *http.Request) string {
	state, err := randomHex(16)
	if err != nil {
		return ""
	}
	http.SetCookie(w, &http.Cookie{
		Name:     githubManifestStateCookieName(state),
		Value:    state,
		Path:     "/settings/github/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   requestIsHTTPS(r),
		MaxAge:   3600,
	})
	return state
}

func (s *Server) handleGitHubManifestCallback(w http.ResponseWriter, r *http.Request) {
	if !s.sessionCanMutate(r) {
		http.Error(w, "authentication required", http.StatusUnauthorized)
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	state := strings.TrimSpace(r.URL.Query().Get("state"))
	if code == "" {
		redirectProbe(w, r, "/settings/github", "error", "GitHub did not return a manifest creation code")
		return
	}
	if !validManifestState(state) {
		redirectProbe(w, r, "/settings/github", "error", "GitHub App creation state is invalid; try again from Geass")
		return
	}
	cookie, err := r.Cookie(githubManifestStateCookieName(state))
	if err != nil || cookie.Value == "" || state == "" || cookie.Value != state {
		redirectProbe(w, r, "/settings/github", "error", "GitHub App creation state mismatch; try again from Geass")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     githubManifestStateCookieName(state),
		Value:    "",
		Path:     "/settings/github/",
		HttpOnly: true,
		MaxAge:   -1,
	})

	converted, err := githubapp.ConvertManifestCode(r.Context(), s.HTTPClient, code)
	if err != nil {
		redirectProbe(w, r, "/settings/github", "error", "could not convert GitHub App manifest")
		return
	}
	readiness, err := s.platformReadiness(r.Context())
	if err != nil {
		redirectProbe(w, r, "/settings/github", "error", "could not load GitHub settings")
		return
	}
	if !readiness.HasDashboardURL && !s.ensureDashboardDomainVerified(r.Context(), readiness) {
		message := "dashboard domain is not reachable yet"
		if readiness.DashboardURL == "" {
			message = "dashboard URL must be configured first"
		}
		redirectProbe(w, r, "/settings/github", "error", message)
		return
	}
	if err := s.persistGitHubAppCredentials(r.Context(), readiness.DashboardURL, githubAppCredentialInput{
		AppID:         strconv.FormatInt(converted.ID, 10),
		ClientID:      converted.ClientID,
		Slug:          converted.Slug,
		ClientSecret:  converted.ClientSecret,
		WebhookSecret: converted.WebhookSecret,
		PrivateKey:    converted.PEM,
	}); err != nil {
		redirectProbe(w, r, "/settings/github", "error", githubCredentialError(err))
		return
	}
	redirectProbe(w, r, "/settings/github", "success", "")
}

type githubAppCredentialInput struct {
	AppID         string
	ClientID      string
	Slug          string
	ClientSecret  string
	WebhookSecret string
	PrivateKey    string
	KeepExisting  bool
}

func (s *Server) persistGitHubAppCredentials(ctx context.Context, dashboardURL string, input githubAppCredentialInput) error {
	appID, err := strconv.ParseInt(strings.TrimSpace(input.AppID), 10, 64)
	if err != nil || appID <= 0 || strings.TrimSpace(input.ClientID) == "" || strings.TrimSpace(input.Slug) == "" {
		return fmt.Errorf("app ID, client ID, and slug are required")
	}

	existing := &corev1.Secret{}
	err = s.Client.Get(ctx, client.ObjectKey{Name: platformGitHubAppSecretName, Namespace: systemNamespace}, existing)
	creatingSecret := apierrors.IsNotFound(err)
	if creatingSecret {
		existing = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: platformGitHubAppSecretName, Namespace: systemNamespace},
			Type:       corev1.SecretTypeOpaque,
			Data:       map[string][]byte{},
		}
	} else if err != nil {
		return err
	}
	if existing.Data == nil {
		existing.Data = map[string][]byte{}
	}

	clientSecret := strings.TrimSpace(input.ClientSecret)
	webhookSecret := strings.TrimSpace(input.WebhookSecret)
	privateKey := strings.TrimSpace(input.PrivateKey)
	if input.KeepExisting {
		if clientSecret == "" {
			clientSecret = string(existing.Data[githubapp.SecretKeyClientSecret])
		}
		if webhookSecret == "" {
			webhookSecret = string(existing.Data[githubapp.SecretKeyWebhookSecret])
		}
		if privateKey == "" {
			privateKey = string(existing.Data[githubapp.SecretKeyPrivateKey])
		}
	}
	if clientSecret == "" || webhookSecret == "" || privateKey == "" {
		return fmt.Errorf("client secret, webhook secret, and private key are required")
	}
	validated := githubapp.Config{
		AppID:         appID,
		ClientID:      strings.TrimSpace(input.ClientID),
		ClientSecret:  clientSecret,
		Slug:          strings.TrimSpace(input.Slug),
		PrivateKeyPEM: privateKey,
		WebhookSecret: webhookSecret,
		PublicBaseURL: dashboardURL,
	}
	if err := validated.Validate(); err != nil {
		return err
	}

	existing.Data[githubapp.SecretKeyAppID] = []byte(strings.TrimSpace(input.AppID))
	existing.Data[githubapp.SecretKeyClientID] = []byte(strings.TrimSpace(input.ClientID))
	existing.Data[githubapp.SecretKeyClientSecret] = []byte(clientSecret)
	existing.Data[githubapp.SecretKeySlug] = []byte(strings.TrimSpace(input.Slug))
	existing.Data[githubapp.SecretKeyPrivateKey] = []byte(privateKey)
	existing.Data[githubapp.SecretKeyWebhookSecret] = []byte(webhookSecret)

	if creatingSecret {
		err = s.Client.Create(ctx, existing)
	} else {
		err = s.Client.Update(ctx, existing)
	}
	if err != nil {
		return err
	}
	config := &geassv1alpha1.GeassPlatformConfig{}
	err = s.Client.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: systemNamespace}, config)
	if apierrors.IsNotFound(err) {
		config = &geassv1alpha1.GeassPlatformConfig{
			ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: systemNamespace},
			Spec: geassv1alpha1.GeassPlatformConfigSpec{
				DashboardURL: dashboardURL,
				GitHubAppRef: &corev1.LocalObjectReference{Name: platformGitHubAppSecretName},
			},
		}
		return s.Client.Create(ctx, config)
	}
	if err != nil {
		return err
	}
	config.Spec.GitHubAppRef = &corev1.LocalObjectReference{Name: platformGitHubAppSecretName}
	return s.Client.Update(ctx, config)
}

func requestIsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return true
	}
	return forwardedProto(r) == "https"
}

func forwardedProto(r *http.Request) string {
	forwarded := strings.TrimSpace(r.Header.Get("Forwarded"))
	if forwarded == "" {
		return ""
	}
	for _, element := range strings.Split(forwarded, ",") {
		for _, field := range strings.Split(element, ";") {
			key, value, ok := strings.Cut(strings.TrimSpace(field), "=")
			if !ok || !strings.EqualFold(key, "proto") {
				continue
			}
			return strings.ToLower(strings.Trim(value, `"'`))
		}
	}
	return ""
}

func requestIsLoopback(r *http.Request) bool {
	host := normalizeRequestHost(r.Host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	switch host {
	case "localhost", "127.0.0.1", "::1":
		return true
	default:
		return false
	}
}

func dashboardCookieSecure(r *http.Request) bool {
	if requestIsHTTPS(r) {
		return true
	}
	if strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "http") || forwardedProto(r) == "http" {
		return false
	}
	return !requestIsLoopback(r)
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func validManifestState(state string) bool {
	if len(state) != 32 {
		return false
	}
	_, err := hex.DecodeString(state)
	return err == nil
}
