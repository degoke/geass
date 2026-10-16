package dashboard

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	"github.com/degoke/geass/pkg/platform"
)

func serviceForm(t *testing.T, values url.Values) *http.Request {
	t.Helper()
	r, err := http.NewRequest(http.MethodPost, "/apps", strings.NewReader(values.Encode()))
	require.NoError(t, err)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	require.NoError(t, r.ParseForm())
	return r
}

func amounts(res corev1.ResourceRequirements) map[string]string {
	out := map[string]string{}
	for name, list := range map[string]corev1.ResourceList{"request": res.Requests, "limit": res.Limits} {
		cpu, memory := list[corev1.ResourceCPU], list[corev1.ResourceMemory]
		if !cpu.IsZero() {
			out[name+"CPU"] = cpu.String()
		}
		if !memory.IsZero() {
			out[name+"Memory"] = memory.String()
		}
	}
	return out
}

func burstingService(t *testing.T) corev1.ResourceRequirements {
	t.Helper()
	res, err := platform.ResourcesFromSizes("250m", "1Gi", "1", "2Gi")
	require.NoError(t, err)
	return res
}

func TestServiceResourcesReserveAndCeilingAreBothSet(t *testing.T) {
	res, err := serviceResourcesFromForm(serviceForm(t, url.Values{
		"cpu": {"250m"}, "memory": {"1Gi"}, "cpuMax": {"1"}, "memoryMax": {"2Gi"},
	}), platform.DefaultAppResources())
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"requestCPU": "250m", "requestMemory": "1Gi", "limitCPU": "1", "limitMemory": "2Gi",
	}, amounts(res))
}

func TestServiceResourcesWithoutCeilingFieldsAreAsBefore(t *testing.T) {
	res, err := serviceResourcesFromForm(serviceForm(t, url.Values{"cpu": {"500m"}, "memory": {"512Mi"}}), platform.DefaultAppResources())
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"requestCPU": "500m", "requestMemory": "512Mi", "limitCPU": "500m", "limitMemory": "512Mi",
	}, amounts(res))

	// Nothing about resources was sent: nothing changes.
	res, err = serviceResourcesFromForm(serviceForm(t, url.Values{"port": {"8080"}}), burstingService(t))
	require.NoError(t, err)
	require.Equal(t, burstingService(t), res)
}

func TestServiceResourcesCeilingSentEmptyMeansTheReservation(t *testing.T) {
	res, err := serviceResourcesFromForm(serviceForm(t, url.Values{
		"cpu": {"250m"}, "memory": {"1Gi"}, "cpuMax": {""}, "memoryMax": {""},
	}), burstingService(t))
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"requestCPU": "250m", "requestMemory": "1Gi", "limitCPU": "250m", "limitMemory": "1Gi",
	}, amounts(res))
	require.False(t, bursts(res))
}

func TestServiceResourcesChangingTheReservationKeepsAnExistingCeiling(t *testing.T) {
	res, err := serviceResourcesFromForm(serviceForm(t, url.Values{"cpu": {"500m"}, "memory": {"512Mi"}}), burstingService(t))
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"requestCPU": "500m", "requestMemory": "512Mi", "limitCPU": "1", "limitMemory": "2Gi",
	}, amounts(res))

	// A ceiling that the new reservation has passed is raised to it.
	res, err = serviceResourcesFromForm(serviceForm(t, url.Values{"cpu": {"2"}, "memory": {"4Gi"}}), burstingService(t))
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"requestCPU": "2", "requestMemory": "4Gi", "limitCPU": "2", "limitMemory": "4Gi",
	}, amounts(res))
}

func TestServiceResourcesLoweringAFixedSizeDoesNotLeaveAHigherCeiling(t *testing.T) {
	// A service that reserved all of what it used must not become one that may burst because someone made it smaller.
	fixed, err := platform.ResourcesFromSize("500m", "2Gi")
	require.NoError(t, err)
	res, err := serviceResourcesFromForm(serviceForm(t, url.Values{"cpu": {"250m"}, "memory": {"1Gi"}}), fixed)
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"requestCPU": "250m", "requestMemory": "1Gi", "limitCPU": "250m", "limitMemory": "1Gi",
	}, amounts(res))
}

func TestServiceResourcesOnlyACeilingKeepsTheReservation(t *testing.T) {
	res, err := serviceResourcesFromForm(serviceForm(t, url.Values{"memoryMax": {"4Gi"}}), burstingService(t))
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"requestCPU": "250m", "requestMemory": "1Gi", "limitCPU": "1", "limitMemory": "4Gi",
	}, amounts(res))

	// The service is not changed by a form that is refused.
	before := burstingService(t)
	_, err = serviceResourcesFromForm(serviceForm(t, url.Values{"memoryMax": {"512Mi"}}), before)
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot be below")
	require.Equal(t, burstingService(t), before)
}

func TestServiceResourcesOnlyACeilingOnAServiceWithNoRequestsUsesTheDefaults(t *testing.T) {
	res, err := serviceResourcesFromForm(serviceForm(t, url.Values{"memoryMax": {"512Mi"}}), corev1.ResourceRequirements{})
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"requestCPU": "100m", "requestMemory": "128Mi", "limitCPU": "100m", "limitMemory": "512Mi",
	}, amounts(res))
}

func TestServiceResourcesCeilingsThatAreNotSizesAreRefused(t *testing.T) {
	_, err := serviceResourcesFromForm(serviceForm(t, url.Values{"cpu": {"250m"}, "memory": {"1Gi"}, "memoryMax": {"3Gi"}}), platform.DefaultAppResources())
	require.Error(t, err)
	require.Contains(t, err.Error(), "the most memory a service can use must be one of the available sizes")
}

func TestServiceResourcesTheOlderLimitFieldsAreLeftToResourcesFromForm(t *testing.T) {
	values := url.Values{"cpuLimit": {"600m"}, "memoryLimit": {"900Mi"}, "memoryMax": {"2Gi"}}
	want, wantErr := resourcesFromForm(serviceForm(t, values), platform.DefaultAppResources())
	got, err := serviceResourcesFromForm(serviceForm(t, values), platform.DefaultAppResources())
	require.Equal(t, wantErr, err)
	require.Equal(t, want, got)
}

func TestDatabasesIgnoreACeiling(t *testing.T) {
	// Databases and object stores go through resourcesFromForm, which has no ceiling: a kill of one costs more
	// than a kill of a service.
	res, err := resourcesFromForm(serviceForm(t, url.Values{"cpu": {"250m"}, "memory": {"512Mi"}, "memoryMax": {"2Gi"}}), platform.DefaultDatabaseResources())
	require.NoError(t, err)
	require.False(t, bursts(res))
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
