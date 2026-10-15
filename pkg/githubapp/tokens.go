package githubapp

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const (
	connectionTokenKey        = "token"
	connectionInstallationKey = "installation_id"
	// connectionTokenAtKey is when the token in the Secret was minted, as RFC 3339.
	connectionTokenAtKey = "token_minted_at"
	// connectionTokenReuseFor is how long a minted installation token is used again before a new one is made. GitHub's
	// last an hour, so what is reused still has at least half of that to run, which is enough for a clone or a report.
	connectionTokenReuseFor = 30 * time.Minute
)

// TokenResolver refreshes GitHub installation tokens using platform app credentials.
type TokenResolver struct {
	Client    client.Client
	HTTP      *http.Client
	Namespace string
}

func (r *TokenResolver) namespace() string {
	if r.Namespace != "" {
		return r.Namespace
	}
	return platform.SystemNamespace
}

func (r *TokenResolver) httpClient() *http.Client {
	if r.HTTP != nil {
		return r.HTTP
	}
	return &http.Client{}
}

// LoadPlatformConfig reads the platform GitHub App configuration from cluster state.
func (r *TokenResolver) LoadPlatformConfig(ctx context.Context) (Config, error) {
	config := &geassv1alpha1.GeassPlatformConfig{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: r.namespace()}, config); err != nil {
		return Config{}, err
	}
	dashboardURL := normalizePublicURL(config.Spec.DashboardURL)
	if config.Spec.GitHubAppRef == nil || config.Spec.GitHubAppRef.Name == "" {
		return Config{PublicBaseURL: dashboardURL}, nil
	}
	secret := &corev1.Secret{}
	if err := r.Client.Get(ctx, client.ObjectKey{Name: config.Spec.GitHubAppRef.Name, Namespace: r.namespace()}, secret); err != nil {
		return Config{}, err
	}
	return ConfigFromSecret(secret, dashboardURL), nil
}

// ConnectionToken returns a valid GitHub token for a connection Secret, refreshing installation tokens when needed.
func (r *TokenResolver) ConnectionToken(ctx context.Context, connectionSecret *corev1.Secret) (string, error) {
	if connectionSecret == nil {
		return "", fmt.Errorf("GitHub connection Secret is required")
	}
	installationID := InstallationIDFromSecret(connectionSecret)
	if installationID > 0 {
		// Every report asks for a token. Minting one is a request to GitHub and a write of the Secret, so the one
		// that was minted a short while ago is used again.
		if token := TokenFromSecret(connectionSecret); token != "" && tokenIsFresh(connectionSecret, time.Now()) {
			return token, nil
		}
		cfg, err := r.LoadPlatformConfig(ctx)
		if err != nil && !apierrors.IsNotFound(err) {
			return "", err
		}
		if cfg.Configured() {
			gh := &Client{Config: cfg, HTTP: r.httpClient()}
			token, err := gh.InstallationToken(installationID)
			if err != nil {
				return "", err
			}
			if err := r.persistToken(ctx, connectionSecret, token); err != nil {
				return "", err
			}
			return token, nil
		}
	}
	token := TokenFromSecret(connectionSecret)
	if token == "" {
		return "", fmt.Errorf("GitHub connection Secret does not contain a token")
	}
	return token, nil
}

func (r *TokenResolver) persistToken(ctx context.Context, secret *corev1.Secret, token string) error {
	latest := &corev1.Secret{}
	if err := r.Client.Get(ctx, client.ObjectKeyFromObject(secret), latest); err != nil {
		return err
	}
	if latest.Data == nil {
		latest.Data = map[string][]byte{}
	}
	latest.Data[connectionTokenKey] = []byte(token)
	latest.Data[connectionTokenAtKey] = []byte(time.Now().UTC().Format(time.RFC3339))
	return r.Client.Update(ctx, latest)
}

// tokenIsFresh reports whether the installation token in the Secret was minted recently enough to use again. A
// Secret that does not say when (one from before this was recorded) is never fresh.
func tokenIsFresh(secret *corev1.Secret, now time.Time) bool {
	minted, err := time.Parse(time.RFC3339, strings.TrimSpace(string(secret.Data[connectionTokenAtKey])))
	if err != nil {
		return false
	}
	age := now.Sub(minted)
	return age >= 0 && age < connectionTokenReuseFor
}

func InstallationIDFromSecret(secret *corev1.Secret) int64 {
	if secret == nil || secret.Data == nil {
		return 0
	}
	raw := strings.TrimSpace(string(secret.Data[connectionInstallationKey]))
	if raw == "" {
		return 0
	}
	id, _ := strconv.ParseInt(raw, 10, 64)
	return id
}

func TokenFromSecret(secret *corev1.Secret) string {
	if secret == nil || secret.Data == nil {
		return ""
	}
	for _, key := range []string{connectionTokenKey, "githubToken", "github-token"} {
		if value := string(secret.Data[key]); value != "" {
			return value
		}
	}
	return ""
}
