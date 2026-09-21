package platform

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

func TestEstimateWorkloadHAMultipliesInstances(t *testing.T) {
	single := EstimateWorkload(WorkloadPostgres, false, 0)
	require.Equal(t, int32(1), single.Replicas)
	require.Equal(t, int64(250), single.CPUMillis)

	ha := EstimateWorkload(WorkloadPostgres, true, 0)
	require.Equal(t, int32(3), ha.Replicas)
	require.Equal(t, int64(750), ha.CPUMillis)
	require.Equal(t, single.MemoryBytes*3, ha.MemoryBytes)
}

func TestEstimateWorkloadExternalUsesNoClusterCPU(t *testing.T) {
	est := EstimateWorkload(WorkloadExternal, false, 1)
	require.Equal(t, int64(0), est.CPUMillis)
	require.Equal(t, int64(0), est.MemoryBytes)
}

func TestDefaultAppResourcesSetsRequests(t *testing.T) {
	res := DefaultAppResources()
	cpu := res.Requests[corev1.ResourceCPU]
	memory := res.Requests[corev1.ResourceMemory]
	require.Equal(t, "100m", cpu.String())
	require.Equal(t, "128Mi", memory.String())
	limitCPU := res.Limits[corev1.ResourceCPU]
	limitMemory := res.Limits[corev1.ResourceMemory]
	require.Equal(t, cpu.String(), limitCPU.String())
	require.Equal(t, memory.String(), limitMemory.String())
}

func TestNodeTooSmallMessageAsksToScaleUp(t *testing.T) {
	message := NodeTooSmallMessage(EstimateWorkload(WorkloadService, false, 1))
	require.Contains(t, message, "Scale up")
	require.Contains(t, message, "100m")
}
