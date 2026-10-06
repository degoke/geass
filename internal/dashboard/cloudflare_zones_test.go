package dashboard

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStoredCloudflareZonesUsesConnectTimeValues(t *testing.T) {
	require.Nil(t, storedCloudflareZones("", "example.com"))
	require.Nil(t, storedCloudflareZones("zone-1", ""))

	zones := storedCloudflareZones("zone-1", "example.com")
	require.Len(t, zones, 1)
	require.Equal(t, "zone-1", zones[0].ID)
	require.Equal(t, "example.com", zones[0].Name)
}
