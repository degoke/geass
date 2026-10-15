package platform

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
