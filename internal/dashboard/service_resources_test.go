package dashboard

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
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

func TestServiceResourcesExactRequestsAndLimitsAreSetAsGiven(t *testing.T) {
	// Any size, and not only the ones the dashboard offers.
	res, err := serviceResourcesFromForm(serviceForm(t, url.Values{
		"cpuRequest": {"150m"}, "cpuLimit": {"600m"}, "memoryRequest": {"300Mi"}, "memoryLimit": {"900Mi"},
	}), platform.DefaultAppResources())
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"requestCPU": "150m", "requestMemory": "300Mi", "limitCPU": "600m", "limitMemory": "900Mi",
	}, amounts(res))
	require.True(t, bursts(res))
}

func TestServiceResourcesExactAMissingRequestOrLimitIsTheOtherOne(t *testing.T) {
	// A limit alone is also what it reserves, as Kubernetes fills in a missing request. Without this the service would
	// have limits and no requests, and the operator ignores the limits of a service that has no requests.
	res, err := serviceResourcesFromForm(serviceForm(t, url.Values{"memoryLimit": {"900Mi"}}), burstingService(t))
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"requestCPU": "250m", "requestMemory": "900Mi", "limitCPU": "1", "limitMemory": "900Mi",
	}, amounts(res), "the other resource keeps what it had")

	// A request alone is also what it may use.
	res, err = serviceResourcesFromForm(serviceForm(t, url.Values{"cpuRequest": {"300m"}, "memoryLimit": {"2Gi"}}), burstingService(t))
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"requestCPU": "300m", "requestMemory": "2Gi", "limitCPU": "300m", "limitMemory": "2Gi",
	}, amounts(res))
}

func TestServiceResourcesExactFieldsThatAreEmptyWereNotSent(t *testing.T) {
	res, err := serviceResourcesFromForm(serviceForm(t, url.Values{
		"cpuRequest": {""}, "cpuLimit": {""}, "memoryRequest": {"300Mi"}, "memoryLimit": {"900Mi"},
	}), burstingService(t))
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"requestCPU": "250m", "requestMemory": "300Mi", "limitCPU": "1", "limitMemory": "900Mi",
	}, amounts(res))
}

func TestServiceResourcesExactDoesNotChangeTheServiceThatIsRefused(t *testing.T) {
	before := burstingService(t)
	for name, values := range map[string]url.Values{
		"a limit below its request": {"memoryRequest": {"900Mi"}, "memoryLimit": {"300Mi"}},
		"not an amount":             {"memoryRequest": {"lots"}, "memoryLimit": {"900Mi"}},
		"zero":                      {"memoryRequest": {"0"}, "memoryLimit": {"900Mi"}},
		"a negative amount":         {"cpuRequest": {"-1"}, "cpuLimit": {"1"}},
	} {
		_, err := serviceResourcesFromForm(serviceForm(t, values), before)
		require.Error(t, err, name)
	}
	require.Equal(t, burstingService(t), before)

	_, err := serviceResourcesFromForm(serviceForm(t, url.Values{"memoryRequest": {"900Mi"}, "memoryLimit": {"300Mi"}}), before)
	require.ErrorContains(t, err, "memoryLimit cannot be below memoryRequest")
}

func TestServiceResourcesExactAndSizesCannotBeMixed(t *testing.T) {
	for name, values := range map[string]url.Values{
		"sizes":    {"memory": {"1Gi"}, "memoryLimit": {"2Gi"}},
		"ceilings": {"memoryMax": {"2Gi"}, "memoryLimit": {"2Gi"}},
	} {
		_, err := serviceResourcesFromForm(serviceForm(t, values), platform.DefaultAppResources())
		require.ErrorContains(t, err, "not both", name)
	}
	// cpuRequest and memoryRequest on their own are still other names for cpu and memory.
	res, err := serviceResourcesFromForm(serviceForm(t, url.Values{"cpuRequest": {"500m"}, "memoryRequest": {"512Mi"}}), platform.DefaultAppResources())
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"requestCPU": "500m", "requestMemory": "512Mi", "limitCPU": "500m", "limitMemory": "512Mi",
	}, amounts(res))
}

func TestDatabasesIgnoreACeilingAndLimits(t *testing.T) {
	// Databases and object stores go through resourcesFromForm, which has no ceiling and reads no limits: a kill of one of
	// them costs more than a kill of a service.
	res, err := resourcesFromForm(serviceForm(t, url.Values{"cpu": {"250m"}, "memory": {"512Mi"}, "memoryMax": {"2Gi"}}), platform.DefaultDatabaseResources())
	require.NoError(t, err)
	require.False(t, bursts(res))

	res, err = resourcesFromForm(serviceForm(t, url.Values{"cpuLimit": {"2"}, "memoryLimit": {"4Gi"}}), platform.DefaultDatabaseResources())
	require.NoError(t, err)
	require.Equal(t, platform.DefaultDatabaseResources(), res, "limits alone change nothing")

	res, err = resourcesFromForm(serviceForm(t, url.Values{"cpu": {"500m"}, "memory": {"1Gi"}, "cpuLimit": {"2"}, "memoryLimit": {"4Gi"}}), platform.DefaultDatabaseResources())
	require.NoError(t, err)
	require.Equal(t, map[string]string{
		"requestCPU": "500m", "requestMemory": "1Gi", "limitCPU": "500m", "limitMemory": "1Gi",
	}, amounts(res), "the size is taken, the limits are not")
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

func TestV1PatchSetsExactRequestsAndLimitsAndTheCeilingFields(t *testing.T) {
	f := newTokenFixture(t)
	token := f.workspaceToken(t, "admin")
	read := func() corev1.ResourceRequirements {
		t.Helper()
		var app geassv1alpha1.GeassApp
		require.NoError(t, f.srv.Client.Get(context.Background(), client.ObjectKey{Name: "draft-prod", Namespace: platform.SystemNamespace}, &app))
		return app.Spec.Resources
	}

	// The exact fields: any size, as sent.
	rec := f.do(jsonRequest(token, http.MethodPatch, "/api/v1/apps/draft-prod", map[string]any{
		"cpuRequest": "150m", "cpuLimit": "600m", "memoryRequest": "300Mi", "memoryLimit": "900Mi",
	}))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, map[string]string{
		"requestCPU": "150m", "requestMemory": "300Mi", "limitCPU": "600m", "limitMemory": "900Mi",
	}, amounts(read()))

	// A reservation and a ceiling, from the dashboard's sizes.
	rec = f.do(jsonRequest(token, http.MethodPatch, "/api/v1/apps/draft-prod", map[string]any{
		"cpu": "250m", "memory": "512Mi", "cpuMax": "1", "memoryMax": "2Gi",
	}))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, map[string]string{
		"requestCPU": "250m", "requestMemory": "512Mi", "limitCPU": "1", "limitMemory": "2Gi",
	}, amounts(read()))

	// A refusal says why, and leaves the service as it was.
	rec = f.do(jsonRequest(token, http.MethodPatch, "/api/v1/apps/draft-prod", map[string]any{"memoryRequest": "2Gi", "memoryLimit": "1Gi"}))
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	message, code := decodeError(t, rec)
	require.Equal(t, "invalid_request", code)
	require.Contains(t, message, "memoryLimit cannot be below memoryRequest")
	require.Equal(t, map[string]string{
		"requestCPU": "250m", "requestMemory": "512Mi", "limitCPU": "1", "limitMemory": "2Gi",
	}, amounts(read()))
}
