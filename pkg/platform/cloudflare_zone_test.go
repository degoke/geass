package platform

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/stretchr/testify/require"
)

func TestResolveCloudflareZoneID(t *testing.T) {
	config := geassv1alpha1.GeassPlatformConfig{
		Spec: geassv1alpha1.GeassPlatformConfigSpec{
			RootDomain:       "example.com",
			CloudflareZoneID: "explicit",
		},
	}
	require.Equal(t, "explicit", ResolveCloudflareZoneID(config, nil))

	config.Spec.CloudflareZoneID = ""
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: PlatformCloudflareSecretName},
		Data: map[string][]byte{
			SecretKeyCloudflareZones: []byte(`[{"id":"z1","name":"example.com"},{"id":"z2","name":"other.test"}]`),
		},
	}
	require.Equal(t, "z1", ResolveCloudflareZoneID(config, secret))

	config.Spec.RootDomain = "other.test"
	require.Equal(t, "z2", ResolveCloudflareZoneID(config, secret))

	config.Spec.RootDomain = ""
	require.Equal(t, "", ResolveCloudflareZoneID(config, secret))

	secret.Data[SecretKeyCloudflareZones] = []byte(`[{"id":"only","name":"solo.dev"}]`)
	require.Equal(t, "only", ResolveCloudflareZoneID(config, secret))
}
