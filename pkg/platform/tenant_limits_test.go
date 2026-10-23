package platform

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

func amounts(cpu, memory, disk string) *geassv1alpha1.GeassContainerResources {
	r := &geassv1alpha1.GeassContainerResources{}
	if cpu != "" {
		r.CPU = quantity(cpu)
	}
	if memory != "" {
		r.Memory = quantity(memory)
	}
	if disk != "" {
		r.EphemeralStorage = quantity(disk)
	}
	return r
}

// sharedClusterLimits are limits a platform that organizations share might set.
func sharedClusterLimits() *geassv1alpha1.GeassTenantLimits {
	return &geassv1alpha1.GeassTenantLimits{
		Pods:             ptr.To(int32(40)),
		EphemeralStorage: quantity("20Gi"),
		Container: &geassv1alpha1.GeassContainerLimits{
			DefaultRequest: amounts("100m", "128Mi", "100Mi"),
			DefaultLimit:   amounts("1", "512Mi", "1Gi"),
			Max:            amounts("4", "8Gi", "10Gi"),
		},
		Bandwidth: &geassv1alpha1.GeassPodBandwidth{Ingress: quantity("100M"), Egress: quantity("50M")},
	}
}

func requireAmount(t *testing.T, list corev1.ResourceList, name corev1.ResourceName, want string) {
	t.Helper()
	got, ok := list[name]
	require.True(t, ok, "%s is missing from %v", name, list)
	require.Zero(t, got.Cmp(resource.MustParse(want)), "%s is %s, not %s", name, got.String(), want)
}

func TestNoLimitsAnywhereIsNoLimits(t *testing.T) {
	require.Nil(t, EffectiveTenantLimits(nil, nil))
	require.Nil(t, EffectiveTenantLimits(&geassv1alpha1.GeassTenantLimits{}, &geassv1alpha1.GeassTenantLimits{Container: &geassv1alpha1.GeassContainerLimits{}}),
		"limits that set nothing are the same as none")
	require.NoError(t, ValidateTenantLimits(nil))
	require.Empty(t, TenantHard(nil))
	require.Nil(t, TenantQuota("shop-production", nil, nil))
	require.Nil(t, ContainerDefaults(nil, nil))
	require.Nil(t, OrgBuildLimitRange("geass-build-acme", "acme", nil))
	require.Nil(t, BuildResources(nil))
	ingress, egress := TenantBandwidth(nil)
	require.Empty(t, ingress)
	require.Empty(t, egress)
}

func TestAnOrganizationsOwnLimitsReplaceThePlatformsFieldByField(t *testing.T) {
	defaults := sharedClusterLimits()
	own := &geassv1alpha1.GeassTenantLimits{
		Pods:      ptr.To(int32(200)),
		Container: &geassv1alpha1.GeassContainerLimits{Max: amounts("16", "", "")},
	}

	limits := EffectiveTenantLimits(defaults, own)

	require.EqualValues(t, 200, *limits.Pods)
	require.Zero(t, limits.EphemeralStorage.Cmp(resource.MustParse("20Gi")), "what the organization leaves out is the platform's")
	require.Equal(t, "100M", limits.Bandwidth.Ingress.String())
	require.Nil(t, limits.Container.DefaultRequest, "an organization's container limits replace the platform's whole, not one amount at a time")
	require.Zero(t, limits.Container.Max.CPU.Cmp(resource.MustParse("16")))

	*limits.Pods = 1
	require.EqualValues(t, 40, *defaults.Pods, "the platform's defaults are not changed by working out an organization's")
	require.EqualValues(t, 200, *own.Pods)

	require.Equal(t, own, EffectiveTenantLimits(nil, own.DeepCopy()), "with no defaults an organization's own are all there is")
}

func TestTheLimitsOfAnOrganizationComeFromThePlatformConfiguration(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	config := &geassv1alpha1.GeassPlatformConfig{
		ObjectMeta: metav1.ObjectMeta{Name: HAReadinessName, Namespace: SystemNamespace},
		Spec:       geassv1alpha1.GeassPlatformConfigSpec{TenantLimits: &geassv1alpha1.GeassPlatformTenantLimitsSpec{Defaults: sharedClusterLimits()}},
	}
	org := &geassv1alpha1.GeassOrganization{ObjectMeta: metav1.ObjectMeta{Name: "acme", Namespace: SystemNamespace}}
	ctx := context.Background()

	limits, err := TenantLimitsOf(ctx, fake.NewClientBuilder().WithScheme(scheme).WithObjects(config).Build(), org)
	require.NoError(t, err)
	require.EqualValues(t, 40, *limits.Pods)

	limits, err = TenantLimitsOf(ctx, fake.NewClientBuilder().WithScheme(scheme).Build(), org)
	require.NoError(t, err)
	require.Nil(t, limits, "a platform with no configuration limits nobody")

	limits, err = TenantLimitsOf(ctx, fake.NewClientBuilder().WithScheme(scheme).WithObjects(config).Build(), nil)
	require.NoError(t, err)
	require.Nil(t, limits, "a project of no organization is never limited by them")
}

func TestLimitsThatKubernetesWouldRefuseAreCaughtFirst(t *testing.T) {
	require.NoError(t, ValidateTenantLimits(sharedClusterLimits()))

	tests := []struct {
		name   string
		mutate func(*geassv1alpha1.GeassTenantLimits)
		want   string
	}{
		{"a default limit above the most", func(l *geassv1alpha1.GeassTenantLimits) { l.Container.DefaultLimit.CPU = quantity("8") }, "container.defaultLimit.cpu (8) is above container.max.cpu (4)"},
		{"a default request above the default limit", func(l *geassv1alpha1.GeassTenantLimits) { l.Container.DefaultRequest.Memory = quantity("1Gi") }, "container.defaultRequest.memory (1Gi) is above the limit a container is given (512Mi)"},
		{"a default request above the most, with no default limit", func(l *geassv1alpha1.GeassTenantLimits) {
			l.Container.DefaultLimit = nil
			l.Container.DefaultRequest.CPU = quantity("5")
		}, "container.defaultRequest.cpu (5) is above the limit a container is given (4)"},
		{"an amount of zero", func(l *geassv1alpha1.GeassTenantLimits) { l.Container.Max.EphemeralStorage = quantity("0") }, "container.max.ephemeralStorage must be above zero"},
		{"a namespace with no room", func(l *geassv1alpha1.GeassTenantLimits) { l.EphemeralStorage = quantity("0") }, "ephemeralStorage must be above zero"},
		{"a cap on disk with nothing to give a container that sets no limit", func(l *geassv1alpha1.GeassTenantLimits) {
			l.Container.DefaultLimit.EphemeralStorage, l.Container.Max.EphemeralStorage = nil, nil
			l.Container.DefaultRequest.EphemeralStorage = nil
		}, "ephemeralStorage caps the total of the containers' ephemeral storage limits"},
		{"a bandwidth below what Kubernetes takes", func(l *geassv1alpha1.GeassTenantLimits) { l.Bandwidth.Egress = quantity("500") }, "bandwidth.egress must be from 1k to 1P"},
		{"a bandwidth above it", func(l *geassv1alpha1.GeassTenantLimits) { l.Bandwidth.Ingress = quantity("2P") }, "bandwidth.ingress must be from 1k to 1P"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limits := sharedClusterLimits()
			tt.mutate(limits)
			err := ValidateTenantLimits(limits)
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.want)
		})
	}

	onlyDisk := &geassv1alpha1.GeassTenantLimits{EphemeralStorage: quantity("20Gi")}
	require.Error(t, ValidateTenantLimits(onlyDisk), "every pod would be refused for having no ephemeral storage limit")
	onlyDisk.Container = &geassv1alpha1.GeassContainerLimits{Max: amounts("", "", "5Gi")}
	require.NoError(t, ValidateTenantLimits(onlyDisk), "the most is the default limit, as Kubernetes makes it")
}

func TestTheTenantQuotaCountsPodsAndEphemeralStorage(t *testing.T) {
	quota := TenantQuota("shop-production", map[string]string{LabelProject: "shop"}, sharedClusterLimits())

	require.Equal(t, TenantQuotaName, quota.Name)
	require.Equal(t, "shop-production", quota.Namespace)
	require.Equal(t, ManagedByValue, quota.Labels[LabelManagedBy], "only what the operator labelled as its own is ever removed")
	require.Equal(t, "shop", quota.Labels[LabelProject])
	require.Len(t, quota.Spec.Hard, 3)
	requireAmount(t, quota.Spec.Hard, corev1.ResourcePods, "40")
	requireAmount(t, quota.Spec.Hard, corev1.ResourceRequestsEphemeralStorage, "20Gi")
	requireAmount(t, quota.Spec.Hard, corev1.ResourceLimitsEphemeralStorage, "20Gi")

	require.Nil(t, TenantQuota("shop-production", nil, &geassv1alpha1.GeassTenantLimits{Container: sharedClusterLimits().Container}),
		"limits on containers alone need no quota")
}

func TestAContainerThatAsksForNothingIsGivenTheDefaultsAndHeldToTheMost(t *testing.T) {
	limits := sharedClusterLimits()

	item := ContainerDefaults(limits, TenantHard(limits))

	require.Equal(t, corev1.LimitTypeContainer, item.Type)
	requireAmount(t, item.DefaultRequest, corev1.ResourceCPU, "100m")
	requireAmount(t, item.DefaultRequest, corev1.ResourceMemory, "128Mi")
	requireAmount(t, item.DefaultRequest, corev1.ResourceEphemeralStorage, "100Mi")
	requireAmount(t, item.Default, corev1.ResourceCPU, "1")
	requireAmount(t, item.Default, corev1.ResourceMemory, "512Mi")
	requireAmount(t, item.Default, corev1.ResourceEphemeralStorage, "1Gi")
	requireAmount(t, item.Max, corev1.ResourceCPU, "4")
	requireAmount(t, item.Max, corev1.ResourceMemory, "8Gi")
	requireAmount(t, item.Max, corev1.ResourceEphemeralStorage, "10Gi")
}

// Kubernetes makes a missing default limit the most, and a missing default request the default limit. Left to it, a
// container that asks for nothing would ask for the most it may have: reserved on a node, counted by the quota and billed.
func TestOnlyAMostStillLeavesAContainerThatAsksForNothingASmallRequest(t *testing.T) {
	limits := &geassv1alpha1.GeassTenantLimits{Container: &geassv1alpha1.GeassContainerLimits{Max: amounts("4", "8Gi", "10Mi")}}

	item := ContainerDefaults(limits, nil)

	requireAmount(t, item.Default, corev1.ResourceCPU, "4")
	requireAmount(t, item.Default, corev1.ResourceMemory, "8Gi")
	requireAmount(t, item.DefaultRequest, corev1.ResourceCPU, "50m")
	requireAmount(t, item.DefaultRequest, corev1.ResourceMemory, "64Mi")
	requireAmount(t, item.DefaultRequest, corev1.ResourceEphemeralStorage, "10Mi")
	require.Len(t, item.DefaultRequest, 3, "a most below the small request is the request, which may not be above the limit")
}

func TestOnlyADefaultRequestGivesNoLimit(t *testing.T) {
	limits := &geassv1alpha1.GeassTenantLimits{Container: &geassv1alpha1.GeassContainerLimits{DefaultRequest: amounts("250m", "256Mi", "")}}

	item := ContainerDefaults(limits, nil)

	require.Len(t, item.DefaultRequest, 2)
	require.Nil(t, item.Default, "a limit is only given where one is asked for")
	require.Nil(t, item.Max)
}

func TestTheProjectsQuotaAndTheOrganizationsLimitsShareOneLimitRange(t *testing.T) {
	hard := corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("4"), corev1.ResourceRequestsMemory: resource.MustParse("8Gi")}

	before := ProjectLimitRange("shop", "shop-production", hard, nil)
	require.Len(t, before.Spec.Limits, 1)
	require.Len(t, before.Spec.Limits[0].DefaultRequest, 2)
	requireAmount(t, before.Spec.Limits[0].DefaultRequest, corev1.ResourceCPU, "50m")
	require.Nil(t, before.Spec.Limits[0].Default, "without the organization's limits nothing has changed")

	limits := &geassv1alpha1.GeassTenantLimits{
		EphemeralStorage: quantity("20Gi"),
		Container:        &geassv1alpha1.GeassContainerLimits{DefaultRequest: amounts("100m", "", ""), Max: amounts("", "", "10Gi")},
	}
	after := ProjectLimitRange("shop", "shop-production", hard, limits)
	require.Len(t, after.Spec.Limits, 1, "Kubernetes applies the defaults of several LimitRanges in no set order, so there is one")
	item := after.Spec.Limits[0]
	requireAmount(t, item.DefaultRequest, corev1.ResourceCPU, "100m")
	requireAmount(t, item.DefaultRequest, corev1.ResourceMemory, "64Mi")
	requireAmount(t, item.DefaultRequest, corev1.ResourceEphemeralStorage, "64Mi")
	requireAmount(t, item.Default, corev1.ResourceEphemeralStorage, "10Gi")
	require.Equal(t, ProjectDefaultsName, after.Name)
	require.Equal(t, "shop", after.Labels[LabelProject])

	require.Contains(t, ManagedQuotaNames(), TenantQuotaName, "a tenant quota edited by hand is put back")
}

func TestABuildIsGivenTheMostAContainerMayHaveAndASmallRequest(t *testing.T) {
	resources := BuildResources(sharedClusterLimits())

	requireAmount(t, resources.Limits, corev1.ResourceCPU, "4")
	requireAmount(t, resources.Limits, corev1.ResourceMemory, "8Gi")
	requireAmount(t, resources.Limits, corev1.ResourceEphemeralStorage, "10Gi")
	requireAmount(t, resources.Requests, corev1.ResourceCPU, "100m")
	requireAmount(t, resources.Requests, corev1.ResourceMemory, "128Mi")
	requireAmount(t, resources.Requests, corev1.ResourceEphemeralStorage, "100Mi")

	require.Nil(t, BuildResources(&geassv1alpha1.GeassTenantLimits{Container: &geassv1alpha1.GeassContainerLimits{DefaultLimit: amounts("1", "", "")}}),
		"with no most a build is given the defaults like anything else")

	build := OrgBuildLimitRange("geass-build-acme", "acme", sharedClusterLimits())
	require.Equal(t, OrgBuildDefaultsName, build.Name)
	require.Equal(t, "acme", build.Labels[LabelOrgBuild])
	require.Equal(t, ManagedByValue, build.Labels[LabelManagedBy])
}

func TestTheBandwidthAnnotationsAreKubernetesQuantities(t *testing.T) {
	ingress, egress := TenantBandwidth(sharedClusterLimits())
	require.Equal(t, "100M", ingress)
	require.Equal(t, "50M", egress)

	ingress, egress = TenantBandwidth(&geassv1alpha1.GeassTenantLimits{Bandwidth: &geassv1alpha1.GeassPodBandwidth{Egress: quantity("1G")}})
	require.Empty(t, ingress)
	require.Equal(t, "1G", egress)
}
