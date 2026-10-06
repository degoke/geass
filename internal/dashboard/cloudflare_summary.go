package dashboard

import (
	"context"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

type cloudflareConnectionSummary struct {
	ZoneID    string
	ZoneName  string
	AccountID string
}

// cloudflareConnectionSummary reads the zone chosen at connect time from the platform secret
// and GeassPlatformConfig. It does not call the Cloudflare API.
func (s *Server) cloudflareConnectionSummary(ctx context.Context, config geassv1alpha1.GeassPlatformConfig) cloudflareConnectionSummary {
	out := cloudflareConnectionSummary{}
	if !platform.CloudflareConfigured(config) {
		return out
	}
	out.ZoneID = strings.TrimSpace(config.Spec.CloudflareZoneID)
	secret := &corev1.Secret{}
	if err := s.Client.Get(ctx, client.ObjectKey{Name: platform.PlatformCloudflareSecretName, Namespace: systemNamespace}, secret); err != nil {
		if !apierrors.IsNotFound(err) {
			return out
		}
		out.ZoneName = platform.RootDomainFromConfig(config)
		return out
	}
	out.AccountID = strings.TrimSpace(string(secret.Data[platform.SecretKeyCloudflareAccountID]))
	out.ZoneName = strings.TrimSpace(string(secret.Data[platform.SecretKeyCloudflareZoneName]))
	if out.ZoneName == "" {
		out.ZoneName = platform.RootDomainFromConfig(config)
	}
	return out
}
