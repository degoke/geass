package githubapp

import (
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

const (
	SecretKeyAppID         = "app_id"
	SecretKeyClientID      = "client_id"
	SecretKeyClientSecret  = "client_secret"
	SecretKeySlug          = "slug"
	SecretKeyPrivateKey    = "private_key"
	SecretKeyWebhookSecret = "webhook_secret"
)

// ConfigFromSecret builds GitHub App configuration from a Kubernetes Secret and dashboard URL.
func ConfigFromSecret(secret *corev1.Secret, dashboardURL string) Config {
	if secret == nil {
		return Config{PublicBaseURL: normalizePublicURL(dashboardURL)}
	}
	data := secret.Data
	if len(data) == 0 && len(secret.StringData) > 0 {
		data = make(map[string][]byte, len(secret.StringData))
		for key, value := range secret.StringData {
			data[key] = []byte(value)
		}
	}
	appID, _ := strconv.ParseInt(strings.TrimSpace(string(data[SecretKeyAppID])), 10, 64)
	return Config{
		AppID:         appID,
		ClientID:      string(data[SecretKeyClientID]),
		ClientSecret:  string(data[SecretKeyClientSecret]),
		Slug:          string(data[SecretKeySlug]),
		PrivateKeyPEM: string(data[SecretKeyPrivateKey]),
		WebhookSecret: string(data[SecretKeyWebhookSecret]),
		PublicBaseURL: normalizePublicURL(dashboardURL),
	}
}

func normalizePublicURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}
