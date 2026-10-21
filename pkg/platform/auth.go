package platform

import geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"

// PlatformAuthSecretName is the Secret in geass-system that holds the client secret the dashboard saved for the
// identity provider people sign in through.
const PlatformAuthSecretName = "platform-auth"

// PlatformAuthClientSecretKey is the key of that Secret that holds the client secret.
const PlatformAuthClientSecretKey = "clientSecret"

// The ways people sign in to the dashboard (GeassPlatformConfig spec.auth.mode).
const (
	AuthModeLocal    = "local"
	AuthModeExternal = "external"
)

// TenancyEnabled is whether people belong to organizations, each seeing only its own projects. It needs the identity
// provider to be how people sign in, because the provider is what says who is in which organization.
func TenancyEnabled(config geassv1alpha1.GeassPlatformConfig) bool {
	auth := config.Spec.Auth
	return auth != nil && auth.Mode == AuthModeExternal && auth.Tenancy != nil && auth.Tenancy.Enabled
}
