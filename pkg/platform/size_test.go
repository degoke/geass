package platform

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

func TestDefaultDatabaseStorageIsSmallForSQLite(t *testing.T) {
	require.Equal(t, "1Gi", DefaultDatabaseStorage(geassv1alpha1.DatabaseEngineSQLite))
	for _, engine := range []geassv1alpha1.GeassDatabaseEngine{
		geassv1alpha1.DatabaseEnginePostgres,
		geassv1alpha1.DatabaseEngineMySQL,
		geassv1alpha1.DatabaseEngineRedis,
	} {
		require.Equal(t, "10Gi", DefaultDatabaseStorage(engine), string(engine))
	}
}

func TestResourcesFromSizeSetsAssignedCPUAndMemory(t *testing.T) {
	res, err := ResourcesFromSize("250m", "512Mi")
	require.NoError(t, err)
	cpu := res.Requests[corev1.ResourceCPU]
	memory := res.Requests[corev1.ResourceMemory]
	require.Equal(t, "250m", cpu.String())
	require.Equal(t, "512Mi", memory.String())
	limitCPU := res.Limits[corev1.ResourceCPU]
	require.Equal(t, "250m", limitCPU.String())
}

func TestEstimateFromResourcesMultipliesCopies(t *testing.T) {
	res, err := ResourcesFromSize("100m", "128Mi")
	require.NoError(t, err)
	est := EstimateFromResources("service", res, 3)
	require.Equal(t, int32(3), est.Replicas)
	require.Equal(t, int64(300), est.CPUMillis)
	require.False(t, est.Approximate)
}

func TestDefaultAutoscalingMaxDoesNotDoubleAssignedCopies(t *testing.T) {
	require.Equal(t, int32(3), DefaultAutoscalingMax(1))
	require.Equal(t, int32(3), DefaultAutoscalingMax(2))
	require.Equal(t, int32(4), DefaultAutoscalingMax(3))
	require.Equal(t, int32(6), DefaultAutoscalingMax(5))
}

func TestResourcesFromSizesReservesLessThanItMayUse(t *testing.T) {
	res, err := ResourcesFromSizes("250m", "512Mi", "1", "2Gi")
	require.NoError(t, err)
	requestCPU, requestMemory := res.Requests[corev1.ResourceCPU], res.Requests[corev1.ResourceMemory]
	limitCPU, limitMemory := res.Limits[corev1.ResourceCPU], res.Limits[corev1.ResourceMemory]
	require.Equal(t, "250m", requestCPU.String())
	require.Equal(t, "512Mi", requestMemory.String())
	require.Equal(t, "1", limitCPU.String())
	require.Equal(t, "2Gi", limitMemory.String())
	require.True(t, bursts(res))

	// What is reserved is what the capacity check counts, so room is not taken for the part it may only borrow.
	est := EstimateFromResources("service", res, 2)
	require.Equal(t, int64(500), est.CPUMillis)
	require.Equal(t, int64(1024*1024*1024), est.MemoryBytes)
}

func TestResourcesFromSizesWithoutACeilingMatchesResourcesFromSize(t *testing.T) {
	same, err := ResourcesFromSize("500m", "1Gi")
	require.NoError(t, err)
	for _, ceilings := range [][2]string{{"", ""}, {"500m", "1Gi"}} {
		res, err := ResourcesFromSizes("500m", "1Gi", ceilings[0], ceilings[1])
		require.NoError(t, err)
		require.Equal(t, same, res)
		require.False(t, bursts(res))
	}

	// A ceiling for one of the two leaves the other at what it reserves.
	res, err := ResourcesFromSizes("500m", "1Gi", "", "4Gi")
	require.NoError(t, err)
	limitCPU, limitMemory := res.Limits[corev1.ResourceCPU], res.Limits[corev1.ResourceMemory]
	require.Equal(t, "500m", limitCPU.String())
	require.Equal(t, "4Gi", limitMemory.String())
	require.True(t, bursts(res))

	none, err := ResourcesFromSizes("", "", "", "")
	require.NoError(t, err)
	require.Equal(t, DefaultAppResources(), none)
}

func TestResourcesFromSizesRejectsACeilingBelowTheReservation(t *testing.T) {
	_, err := ResourcesFromSizes("500m", "1Gi", "250m", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "CPU")
	_, err = ResourcesFromSizes("500m", "1Gi", "", "512Mi")
	require.Error(t, err)
	require.Contains(t, err.Error(), "memory")
	// Only a ceiling given: the reservation falls back to the smallest size, which it cannot be asked to exceed.
	res, err := ResourcesFromSizes("", "", "", "256Mi")
	require.NoError(t, err)
	limitMemory, requestMemory := res.Limits[corev1.ResourceMemory], res.Requests[corev1.ResourceMemory]
	require.Equal(t, "256Mi", limitMemory.String())
	require.Equal(t, "128Mi", requestMemory.String())
}

func TestResourcesFromSizesRejectsACeilingThatIsNotASize(t *testing.T) {
	_, err := ResourcesFromSizes("100m", "128Mi", "3", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "the most CPU a service can use must be one of the available sizes")
	_, err = ResourcesFromSizes("100m", "128Mi", "", "10Gi")
	require.Error(t, err)
	require.Contains(t, err.Error(), "the most memory a service can use must be one of the available sizes")
}

func TestResourcesFromSizeRejectsUnknownAmounts(t *testing.T) {
	_, err := ResourcesFromSize("3", "128Mi")
	require.Error(t, err)
	require.Contains(t, err.Error(), "CPU")
	_, err = ResourcesFromSize("100m", "10Gi")
	require.Error(t, err)
	require.Contains(t, err.Error(), "memory")
}

// bursts reports whether a workload may use more CPU or memory than it reserves.
func bursts(res corev1.ResourceRequirements) bool {
	for _, name := range []corev1.ResourceName{corev1.ResourceCPU, corev1.ResourceMemory} {
		request, limit := res.Requests[name], res.Limits[name]
		if limit.Cmp(request) > 0 {
			return true
		}
	}
	return false
}
