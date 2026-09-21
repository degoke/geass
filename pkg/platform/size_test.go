package platform

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

func TestResourcesFromSizeSetsAssignedCPUAndMemory(t *testing.T) {
	res, err := ResourcesFromSize("250m", "512Mi")
	require.NoError(t, err)
	cpu := res.Requests[corev1.ResourceCPU]
	memory := res.Requests[corev1.ResourceMemory]
	require.Equal(t, "250m", cpu.String())
	require.Equal(t, "512Mi", memory.String())
	limitCPU := res.Limits[corev1.ResourceCPU]
	require.Equal(t, "500m", limitCPU.String())
}

func TestEstimateFromResourcesMultipliesCopies(t *testing.T) {
	res, err := ResourcesFromSize("100m", "128Mi")
	require.NoError(t, err)
	est := EstimateFromResources("service", res, 3)
	require.Equal(t, int32(3), est.Replicas)
	require.Equal(t, int64(300), est.CPUMillis)
	require.False(t, est.Approximate)
}

func TestResourcesFromSizeRejectsUnknownAmounts(t *testing.T) {
	_, err := ResourcesFromSize("3", "128Mi")
	require.Error(t, err)
	require.Contains(t, err.Error(), "CPU")
	_, err = ResourcesFromSize("100m", "10Gi")
	require.Error(t, err)
	require.Contains(t, err.Error(), "memory")
}
