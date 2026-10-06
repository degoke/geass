package platform

import (
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/stretchr/testify/require"
)

func TestCloudflareZonesSecretRoundTrip(t *testing.T) {
	secret := &corev1.Secret{Data: map[string][]byte{}}
	SetCloudflareZonesOnSecret(secret, []CloudflareZoneRecord{
		{ID: "z2", Name: "b.com"},
		{ID: "z1", Name: "a.com"},
		{ID: "z1", Name: "duplicate"},
	})
	zones := CloudflareZonesFromSecret(secret)
	require.Len(t, zones, 2)
	require.Equal(t, "a.com", zones[0].Name)
	require.Equal(t, "b.com", zones[1].Name)
}

func TestEnsureCloudflareZoneInList(t *testing.T) {
	zones := EnsureCloudflareZoneInList([]CloudflareZoneRecord{{ID: "z1", Name: "a.com"}}, "z2", "b.com")
	require.Len(t, zones, 2)
	zones = EnsureCloudflareZoneInList(zones, "z1", "a.com")
	require.Len(t, zones, 2)
}
