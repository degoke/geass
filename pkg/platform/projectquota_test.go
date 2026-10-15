package platform

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

func quantity(value string) *resource.Quantity {
	q := resource.MustParse(value)
	return &q
}

func usageOf(cpu, pods string) corev1.ResourceList {
	list := corev1.ResourceList{}
	if cpu != "" {
		list[corev1.ResourceRequestsCPU] = resource.MustParse(cpu)
	}
	if pods != "" {
		list[corev1.ResourcePods] = resource.MustParse(pods)
	}
	return list
}

func requireQuantity(t *testing.T, list corev1.ResourceList, name corev1.ResourceName, want string, why ...string) {
	t.Helper()
	got, ok := list[name]
	require.True(t, ok, "%s is not capped", name)
	require.Zero(t, got.Cmp(resource.MustParse(want)), "%s should be %s, was %s %v", name, want, got.String(), why)
}

func TestAnEnvironmentCapCapsOnlyWhatTheProjectNamed(t *testing.T) {
	pods := int32(20)
	hard := EnvironmentHard("shop-production", &geassv1alpha1.GeassEnvironmentQuota{CPU: quantity("4"), Memory: quantity("8Gi"), Storage: quantity("50Gi"), Pods: &pods}, nil, nil)

	require.Len(t, hard, 4)
	requireQuantity(t, hard, corev1.ResourceRequestsCPU, "4")
	requireQuantity(t, hard, corev1.ResourceRequestsMemory, "8Gi")
	requireQuantity(t, hard, corev1.ResourceRequestsStorage, "50Gi")
	requireQuantity(t, hard, corev1.ResourcePods, "20")

	require.Len(t, EnvironmentHard("shop-production", &geassv1alpha1.GeassEnvironmentQuota{CPU: quantity("500m")}, nil, nil), 1, "a cap the project did not set is not a cap of zero")
	require.Empty(t, EnvironmentHard("shop-production", nil, nil, nil))
}

func TestTheHardLimitDoesNotShareStorageWithTheSpec(t *testing.T) {
	spec := &geassv1alpha1.GeassEnvironmentQuota{CPU: quantity("4")}
	hard := EnvironmentHard("shop-production", spec, nil, nil)
	cpu := hard[corev1.ResourceRequestsCPU]
	cpu.Add(resource.MustParse("1"))

	require.Zero(t, spec.CPU.Cmp(resource.MustParse("4")), "changing what was built from the spec must not change the spec, which is the cached project")
}

func TestAWholeProjectCapLeavesEachEnvironmentWhatTheOthersDoNotUse(t *testing.T) {
	total := &geassv1alpha1.GeassProjectQuota{CPU: quantity("8")}
	used := map[string]corev1.ResourceList{
		"shop-production": usageOf("3", ""),
		"shop-staging":    usageOf("1500m", ""),
		"shop-pr-7":       usageOf("500m", ""),
	}

	requireQuantity(t, EnvironmentHard("shop-production", nil, total, used), corev1.ResourceRequestsCPU, "6")
	requireQuantity(t, EnvironmentHard("shop-staging", nil, total, used), corev1.ResourceRequestsCPU, "4500m")
	requireQuantity(t, EnvironmentHard("shop-pr-7", nil, total, used), corev1.ResourceRequestsCPU, "3500m")
	requireQuantity(t, EnvironmentHard("shop-brand-new", nil, total, used), corev1.ResourceRequestsCPU, "3", "an environment with no quota yet may ask for what all the others leave")
}

func TestAnEnvironmentWithNothingUsedMayAskForTheWholeTotal(t *testing.T) {
	total := &geassv1alpha1.GeassProjectQuota{CPU: quantity("8")}
	requireQuantity(t, EnvironmentHard("shop-production", nil, total, nil), corev1.ResourceRequestsCPU, "8")
	requireQuantity(t, EnvironmentHard("shop-production", nil, total, map[string]corev1.ResourceList{"shop-production": usageOf("5", "")}), corev1.ResourceRequestsCPU, "8",
		"what the environment uses itself is not held against it: the total counts it once")
}

func TestTheLowerOfTheEnvironmentCapAndWhatIsLeftWins(t *testing.T) {
	total := &geassv1alpha1.GeassProjectQuota{CPU: quantity("8")}
	environment := &geassv1alpha1.GeassEnvironmentQuota{CPU: quantity("4")}
	used := map[string]corev1.ResourceList{"shop-staging": usageOf("2", "")}

	requireQuantity(t, EnvironmentHard("shop-production", environment, total, used), corev1.ResourceRequestsCPU, "4", "plenty is left, so the environment's own cap holds")
	used["shop-staging"] = usageOf("6", "")
	requireQuantity(t, EnvironmentHard("shop-production", environment, total, used), corev1.ResourceRequestsCPU, "2", "little is left, so that holds")
}

func TestAnEnvironmentCapOnAnotherResourceIsKeptAlongsideTheTotal(t *testing.T) {
	pods := int32(10)
	hard := EnvironmentHard("shop-production",
		&geassv1alpha1.GeassEnvironmentQuota{Pods: &pods},
		&geassv1alpha1.GeassProjectQuota{CPU: quantity("8")}, nil)

	require.Len(t, hard, 2)
	requireQuantity(t, hard, corev1.ResourcePods, "10")
	requireQuantity(t, hard, corev1.ResourceRequestsCPU, "8")
}

func TestAProjectOverItsTotalLeavesNoRoomAnywhereAndNeverGoesNegative(t *testing.T) {
	total := &geassv1alpha1.GeassProjectQuota{CPU: quantity("4")}

	// Two environments that use 3 each against a total of 4: each is told it has 1 left, which is below what it
	// already uses, so neither can add a pod. The totals add up: the 1 each is told is the same 1.
	both := map[string]corev1.ResourceList{"shop-production": usageOf("3", ""), "shop-staging": usageOf("3", "")}
	requireQuantity(t, EnvironmentHard("shop-production", nil, total, both), corev1.ResourceRequestsCPU, "1")
	requireQuantity(t, EnvironmentHard("shop-staging", nil, total, both), corev1.ResourceRequestsCPU, "1")

	// One environment that already uses more than the whole total leaves the other with less than nothing: zero.
	over := map[string]corev1.ResourceList{"shop-production": usageOf("5", ""), "shop-staging": usageOf("", "")}
	requireQuantity(t, EnvironmentHard("shop-staging", nil, total, over), corev1.ResourceRequestsCPU, "0")
	requireQuantity(t, EnvironmentHard("shop-production", nil, total, over), corev1.ResourceRequestsCPU, "4")
}

func TestTheEnvironmentCapsNeverAddUpToMoreThanTheTotalOnceTheyAreCounted(t *testing.T) {
	total := &geassv1alpha1.GeassProjectQuota{CPU: quantity("8")}
	used := map[string]corev1.ResourceList{"a": usageOf("2", ""), "b": usageOf("1", ""), "c": usageOf("0", "")}

	// Each environment's cap, less what it uses, is the room it has. The room anyone has is the total less what the
	// others use less what it uses itself, which is the same room for all of them: that is what keeps the sum honest.
	rooms := make([]resource.Quantity, 0, len(used))
	for namespace, usage := range used {
		hard := EnvironmentHard(namespace, nil, total, used)
		cap := hard[corev1.ResourceRequestsCPU]
		cap.Sub(usage[corev1.ResourceRequestsCPU])
		rooms = append(rooms, cap)
	}
	for _, room := range rooms {
		require.Zero(t, room.Cmp(resource.MustParse("5")), "every environment is told it has the 5 CPUs that are left, which it can have until another takes some")
	}
}

func TestThePodTotalIsDividedLikeTheRest(t *testing.T) {
	pods := int32(10)
	total := &geassv1alpha1.GeassProjectQuota{Pods: &pods}
	hard := EnvironmentHard("shop-staging", nil, total, map[string]corev1.ResourceList{"shop-production": usageOf("", "7")})

	require.Len(t, hard, 1)
	requireQuantity(t, hard, corev1.ResourcePods, "3")
}

func TestTheProjectsUsageIsTheSumOfItsEnvironments(t *testing.T) {
	pods := int32(10)
	total := &geassv1alpha1.GeassProjectQuota{CPU: quantity("8"), Pods: &pods}
	used := map[string]corev1.ResourceList{"a": usageOf("1500m", "4"), "b": usageOf("500m", "3"), "c": {}}

	usage := QuotaUsageOf(total, used)

	require.NotNil(t, usage)
	require.Zero(t, usage.CPU.Cmp(resource.MustParse("2")))
	require.Equal(t, int32(7), *usage.Pods)
	require.Nil(t, usage.Memory, "only what the project capped is reported")
	require.Nil(t, QuotaUsageOf(nil, used))
	require.Nil(t, QuotaUsageOf(&geassv1alpha1.GeassProjectQuota{}, used))

	empty := QuotaUsageOf(total, nil)
	require.Zero(t, empty.CPU.Cmp(resource.MustParse("0")), "a project that uses nothing says so")
}

func TestTheQuotaObjectCarriesTheHardLimits(t *testing.T) {
	quota := ProjectResourceQuota("shop", "shop-production", corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("2")})

	require.Equal(t, ProjectQuotaName, quota.Name)
	require.Equal(t, "shop-production", quota.Namespace)
	require.Equal(t, "shop", quota.Labels[LabelProject])
	require.Equal(t, ManagedByValue, quota.Labels[LabelManagedBy])
	requireQuantity(t, quota.Spec.Hard, corev1.ResourceRequestsCPU, "2")
}

func TestContainersGetARequestOnlyForWhatTheQuotaCounts(t *testing.T) {
	both := ProjectLimitRange("shop", "shop-production", corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("4"), corev1.ResourceRequestsMemory: resource.MustParse("8Gi")})
	require.NotNil(t, both)
	require.Len(t, both.Spec.Limits, 1)
	require.Equal(t, corev1.LimitTypeContainer, both.Spec.Limits[0].Type)
	require.Len(t, both.Spec.Limits[0].DefaultRequest, 2)
	require.Empty(t, both.Spec.Limits[0].Default, "a default request only: a default limit would start killing containers that never had one")

	cpuOnly := ProjectLimitRange("shop", "shop-production", corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("4")})
	require.Len(t, cpuOnly.Spec.Limits[0].DefaultRequest, 1)
	require.Contains(t, cpuOnly.Spec.Limits[0].DefaultRequest, corev1.ResourceCPU)

	require.Nil(t, ProjectLimitRange("shop", "shop-production", corev1.ResourceList{corev1.ResourceRequestsStorage: resource.MustParse("1Gi"), corev1.ResourcePods: resource.MustParse("5")}),
		"a quota that does not count CPU or memory refuses no pod for lacking a request, so there is nothing to fill in")
}

func TestEveryQuotaObjectIsOneTheOperatorWatches(t *testing.T) {
	hard := corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("1"), corev1.ResourceRequestsMemory: resource.MustParse("1Gi")}
	require.Contains(t, ManagedQuotaNames(), ProjectResourceQuota("shop", "ns", hard).Name)
	require.Contains(t, ManagedQuotaNames(), ProjectLimitRange("shop", "ns", hard).Name)
}

func TestAQuotaWithNoCapsIsNoQuota(t *testing.T) {
	var none *geassv1alpha1.GeassEnvironmentQuota
	require.True(t, none.IsZero())
	require.True(t, (&geassv1alpha1.GeassEnvironmentQuota{}).IsZero())
	require.False(t, (&geassv1alpha1.GeassEnvironmentQuota{CPU: quantity("1")}).IsZero())

	var noTotal *geassv1alpha1.GeassProjectQuota
	require.True(t, noTotal.IsZero())
	require.True(t, (&geassv1alpha1.GeassProjectQuota{}).IsZero())
	require.False(t, (&geassv1alpha1.GeassProjectQuota{CPU: quantity("1")}).IsZero())
}
