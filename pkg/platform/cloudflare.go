package platform

import (
	"strings"

	corev1 "k8s.io/api/core/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

const (
	ConditionCloudflareReady     = "CloudflareReady"
	PlatformCloudflareSecretName = "platform-cloudflare"
	CloudflaredDeploymentName    = "geass-cloudflared"
	CloudflaredTokenSecretName   = "geass-cloudflared-token"
	CloudflareTunnelName         = "geass"

	SecretKeyCloudflareAPIToken  = "apiToken"
	SecretKeyCloudflareAccountID = "accountId"
	SecretKeyCloudflareZoneName  = "zoneName"
	SecretKeyCloudflareZones     = "zones"
)

type CloudflareCredentials struct {
	APIToken  string
	AccountID string
	ZoneID    string
}

func CloudflareCredentialsFromSecret(secret *corev1.Secret, zoneID string) CloudflareCredentials {
	if secret == nil {
		return CloudflareCredentials{}
	}
	return CloudflareCredentials{
		APIToken:  secretValue(secret, SecretKeyCloudflareAPIToken),
		AccountID: secretValue(secret, SecretKeyCloudflareAccountID),
		ZoneID:    strings.TrimSpace(zoneID),
	}
}

func (c CloudflareCredentials) Valid() bool {
	return c.APIToken != "" && c.AccountID != "" && c.ZoneID != ""
}

func CloudflareConfigured(config geassv1alpha1.GeassPlatformConfig) bool {
	return config.Spec.CloudflareConnectionRef != nil && config.Spec.CloudflareConnectionRef.Name != ""
}

func CloudflareTunnelMode(config geassv1alpha1.GeassPlatformConfig) bool {
	return DashboardExposureFromConfig(config) == geassv1alpha1.DashboardExposureCloudflareTunnel
}

// AppPublicHostname builds a default public hostname for a service under the platform root domain.
func AppPublicHostname(appName, project, environment, rootDomain string) string {
	rootDomain = strings.TrimSpace(strings.ToLower(rootDomain))
	if rootDomain == "" {
		return ""
	}
	label := dnsLabel(appName)
	project = dnsLabel(project)
	env := dnsLabel(environment)
	if label == "" || project == "" {
		return ""
	}
	if env != "" && env != "production" {
		return label + "-" + project + "-" + env + "." + rootDomain
	}
	return label + "-" + project + "." + rootDomain
}

func dnsLabel(raw string) string {
	raw = strings.TrimSpace(strings.ToLower(raw))
	raw = strings.ReplaceAll(raw, "_", "-")
	var b strings.Builder
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 63 {
		out = out[:63]
		out = strings.TrimRight(out, "-")
	}
	return out
}

func secretValue(secret *corev1.Secret, key string) string {
	if secret == nil || secret.Data == nil {
		return ""
	}
	return strings.TrimSpace(string(secret.Data[key]))
}
