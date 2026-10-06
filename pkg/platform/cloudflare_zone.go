package platform

import (
	"strings"

	corev1 "k8s.io/api/core/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// ResolveCloudflareZoneID picks the DNS zone to manage from platform config and the connect-time secret.
func ResolveCloudflareZoneID(config geassv1alpha1.GeassPlatformConfig, secret *corev1.Secret) string {
	if id := strings.TrimSpace(config.Spec.CloudflareZoneID); id != "" {
		return id
	}
	root := strings.TrimSpace(strings.ToLower(RootDomainFromConfig(config)))
	zones := CloudflareZonesFromSecret(secret)
	for _, zone := range zones {
		if root != "" && strings.EqualFold(zone.Name, root) {
			return zone.ID
		}
	}
	if len(zones) == 1 {
		return zones[0].ID
	}
	return ""
}
