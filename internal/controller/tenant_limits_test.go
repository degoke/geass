package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilptr "k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/event"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const limitedOrg = "org-5e6f7a8b"

func limitAmounts(cpu, memory, disk string) *geassv1alpha1.GeassContainerResources {
	r := &geassv1alpha1.GeassContainerResources{}
	for value, field := range map[string]**resource.Quantity{cpu: &r.CPU, memory: &r.Memory, disk: &r.EphemeralStorage} {
		if value != "" {
			q := resource.MustParse(value)
			*field = &q
		}
	}
	return r
}

// platformDefaults are the limits a shared cluster's platform gives every organization.
func platformDefaults() *geassv1alpha1.GeassTenantLimits {
	disk := resource.MustParse("20Gi")
	ingress, egress := resource.MustParse("100M"), resource.MustParse("50M")
	return &geassv1alpha1.GeassTenantLimits{
		Pods:             utilptr.To(int32(40)),
		EphemeralStorage: &disk,
		Container: &geassv1alpha1.GeassContainerLimits{
			DefaultRequest: limitAmounts("100m", "128Mi", "100Mi"),
			DefaultLimit:   limitAmounts("1", "512Mi", "1Gi"),
			Max:            limitAmounts("4", "8Gi", "10Gi"),
		},
		Bandwidth: &geassv1alpha1.GeassPodBandwidth{Ingress: &ingress, Egress: &egress},
	}
}

func platformConfigWith(defaults *geassv1alpha1.GeassTenantLimits) *geassv1alpha1.GeassPlatformConfig {
	config := &geassv1alpha1.GeassPlatformConfig{ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}}
	if defaults != nil {
		config.Spec.TenantLimits = &geassv1alpha1.GeassPlatformTenantLimitsSpec{Defaults: defaults}
	}
	return config
}

func limitsScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, networkingv1.AddToScheme(scheme))
	require.NoError(t, admissionv1.AddToScheme(scheme))
	require.NoError(t, batchv1.AddToScheme(scheme))
	return scheme
}

// limitsFixture is a ready cluster with two projects in it, one of the organization and one of nobody's, each with one
// environment, and the platform's configuration.
func limitsFixture(t *testing.T, config *geassv1alpha1.GeassPlatformConfig, orgLimits *geassv1alpha1.GeassTenantLimits) (*GeassProjectReconciler, client.Client) {
	t.Helper()
	scheme := limitsScheme(t)
	cluster := &geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: testClusterName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassClusterSpec{Version: "v1", ServerURL: "https://cluster.example", TokenSecretRef: corev1.SecretReference{Name: "token"}},
		Status:     geassv1alpha1.GeassClusterStatus{Conditions: []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue}}},
	}
	tenant := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: "shop", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{"production"}, Org: limitedOrg},
	}
	nobodys := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: "intranet", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{"production"}},
	}
	org := &geassv1alpha1.GeassOrganization{
		ObjectMeta: metav1.ObjectMeta{Name: limitedOrg, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassOrganizationSpec{DisplayName: "Acme", IdPID: "acme", Limits: orgLimits},
	}
	objects := []client.Object{cluster, tenant, nobodys, org, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: platform.SystemNamespace}}}
	if config != nil {
		objects = append(objects, config)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(tenant, nobodys, cluster).WithObjects(objects...).Build()
	return &GeassProjectReconciler{Client: c, Scheme: scheme}, c
}

func reconcileProjects(t *testing.T, r *GeassProjectReconciler, names ...string) {
	t.Helper()
	for _, name := range names {
		_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}})
		require.NoError(t, err, name)
	}
}

func tenantQuotaIn(t *testing.T, c client.Client, namespace string) (*corev1.ResourceQuota, bool) {
	t.Helper()
	var quota corev1.ResourceQuota
	err := c.Get(context.Background(), client.ObjectKey{Name: platform.TenantQuotaName, Namespace: namespace}, &quota)
	if apierrors.IsNotFound(err) {
		return nil, false
	}
	require.NoError(t, err)
	return &quota, true
}

func limitRangeIn(t *testing.T, c client.Client, name, namespace string) (*corev1.LimitRange, bool) {
	t.Helper()
	var limits corev1.LimitRange
	err := c.Get(context.Background(), client.ObjectKey{Name: name, Namespace: namespace}, &limits)
	if apierrors.IsNotFound(err) {
		return nil, false
	}
	require.NoError(t, err)
	return &limits, true
}

func namespaceAnnotations(t *testing.T, c client.Client, name string) map[string]string {
	t.Helper()
	var ns corev1.Namespace
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: name}, &ns))
	return ns.Annotations
}

func limitsCondition(t *testing.T, c client.Client, project string) *metav1.Condition {
	t.Helper()
	var stored geassv1alpha1.GeassProject
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: project, Namespace: platform.SystemNamespace}, &stored))
	return meta.FindStatusCondition(stored.Status.Conditions, platform.ConditionTenantLimitsApplied)
}

func requireHard(t *testing.T, list corev1.ResourceList, name corev1.ResourceName, want string) {
	t.Helper()
	got, ok := list[name]
	require.True(t, ok, "%s is missing from %v", name, list)
	require.Zero(t, got.Cmp(resource.MustParse(want)), "%s is %s, not %s", name, got.String(), want)
}

func TestAnOrganizationsEnvironmentsAreHeldToThePlatformsLimitsAndNothingElseIs(t *testing.T) {
	r, c := limitsFixture(t, platformConfigWith(platformDefaults()), nil)

	reconcileProjects(t, r, "shop", "intranet")

	quota, ok := tenantQuotaIn(t, c, "shop-production")
	require.True(t, ok)
	requireHard(t, quota.Spec.Hard, corev1.ResourcePods, "40")
	requireHard(t, quota.Spec.Hard, corev1.ResourceRequestsEphemeralStorage, "20Gi")
	requireHard(t, quota.Spec.Hard, corev1.ResourceLimitsEphemeralStorage, "20Gi")
	require.Equal(t, "shop", quota.Labels[platform.LabelProject], "an edit by hand is traced back to the project")

	defaults, ok := limitRangeIn(t, c, platform.ProjectDefaultsName, "shop-production")
	require.True(t, ok)
	require.Len(t, defaults.Spec.Limits, 1)
	item := defaults.Spec.Limits[0]
	// What a container that declares nothing ends up with: these are what the API server's LimitRanger copies into it.
	requireHard(t, item.DefaultRequest, corev1.ResourceCPU, "100m")
	requireHard(t, item.DefaultRequest, corev1.ResourceMemory, "128Mi")
	requireHard(t, item.DefaultRequest, corev1.ResourceEphemeralStorage, "100Mi")
	requireHard(t, item.Default, corev1.ResourceCPU, "1")
	requireHard(t, item.Default, corev1.ResourceMemory, "512Mi")
	requireHard(t, item.Default, corev1.ResourceEphemeralStorage, "1Gi")
	requireHard(t, item.Max, corev1.ResourceCPU, "4")
	requireHard(t, item.Max, corev1.ResourceEphemeralStorage, "10Gi")

	annotations := namespaceAnnotations(t, c, "shop-production")
	require.Equal(t, "100M", annotations[platform.AnnotationPodIngressBandwidth])
	require.Equal(t, "50M", annotations[platform.AnnotationPodEgressBandwidth])
	var policy admissionv1.MutatingAdmissionPolicy
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: platform.PodBandwidthMutationPolicyName}, &policy), "the policy that gives pods their bandwidth is installed")
	var binding admissionv1.MutatingAdmissionPolicyBinding
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: platform.PodBandwidthMutationBindingName}, &binding))

	condition := limitsCondition(t, c, "shop")
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionTrue, condition.Status)
	require.Equal(t, "Applied", condition.Reason)

	// The project of no organization, and the platform's own namespace, are not a tenant's.
	for _, namespace := range []string{"intranet-production", platform.SystemNamespace} {
		_, ok := tenantQuotaIn(t, c, namespace)
		require.False(t, ok, namespace)
		_, ok = limitRangeIn(t, c, platform.ProjectDefaultsName, namespace)
		require.False(t, ok, namespace)
		require.NotContains(t, namespaceAnnotations(t, c, namespace), platform.AnnotationPodEgressBandwidth, namespace)
	}
	require.Nil(t, limitsCondition(t, c, "intranet"), "a project of no organization carries no such condition")
}

func TestWithoutLimitsNothingIsWrittenAsBefore(t *testing.T) {
	for name, config := range map[string]*geassv1alpha1.GeassPlatformConfig{
		"no configuration":          nil,
		"a configuration without":   platformConfigWith(nil),
		"defaults that set nothing": platformConfigWith(&geassv1alpha1.GeassTenantLimits{}),
	} {
		t.Run(name, func(t *testing.T) {
			r, c := limitsFixture(t, config, nil)

			reconcileProjects(t, r, "shop")

			_, ok := tenantQuotaIn(t, c, "shop-production")
			require.False(t, ok)
			_, ok = limitRangeIn(t, c, platform.ProjectDefaultsName, "shop-production")
			require.False(t, ok)
			require.Empty(t, namespaceAnnotations(t, c, "shop-production"))
			require.True(t, apierrors.IsNotFound(c.Get(context.Background(), client.ObjectKey{Name: platform.PodBandwidthMutationPolicyName}, &admissionv1.MutatingAdmissionPolicy{})),
				"no policy is installed until a bandwidth is asked for")
			require.Nil(t, limitsCondition(t, c, "shop"))
		})
	}
}

func TestTheLimitsFollowTheOrganizationsAndGoWhenTheSettingDoes(t *testing.T) {
	ctx := context.Background()
	r, c := limitsFixture(t, platformConfigWith(platformDefaults()), nil)
	reconcileProjects(t, r, "shop")

	// The organization is given more pods and a container limit of its own.
	var org geassv1alpha1.GeassOrganization
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: limitedOrg, Namespace: platform.SystemNamespace}, &org))
	org.Spec.Limits = &geassv1alpha1.GeassTenantLimits{
		Pods:      utilptr.To(int32(200)),
		Container: &geassv1alpha1.GeassContainerLimits{Max: limitAmounts("16", "32Gi", "50Gi")},
	}
	require.NoError(t, c.Update(ctx, &org))
	reconcileProjects(t, r, "shop")

	quota, _ := tenantQuotaIn(t, c, "shop-production")
	requireHard(t, quota.Spec.Hard, corev1.ResourcePods, "200")
	requireHard(t, quota.Spec.Hard, corev1.ResourceLimitsEphemeralStorage, "20Gi")
	defaults, _ := limitRangeIn(t, c, platform.ProjectDefaultsName, "shop-production")
	item := defaults.Spec.Limits[0]
	requireHard(t, item.Max, corev1.ResourceCPU, "16")
	requireHard(t, item.Default, corev1.ResourceCPU, "16")
	requireHard(t, item.DefaultRequest, corev1.ResourceCPU, "50m")

	// The platform stops giving limits: what is the organization's own stays, the rest goes.
	var config geassv1alpha1.GeassPlatformConfig
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, &config))
	config.Spec.TenantLimits = nil
	require.NoError(t, c.Update(ctx, &config))
	reconcileProjects(t, r, "shop")

	quota, _ = tenantQuotaIn(t, c, "shop-production")
	require.Len(t, quota.Spec.Hard, 1, "the ephemeral storage cap was the platform's")
	requireHard(t, quota.Spec.Hard, corev1.ResourcePods, "200")
	require.NotContains(t, namespaceAnnotations(t, c, "shop-production"), platform.AnnotationPodIngressBandwidth, "so was the bandwidth")

	// And the organization's own go too.
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: limitedOrg, Namespace: platform.SystemNamespace}, &org))
	org.Spec.Limits = nil
	require.NoError(t, c.Update(ctx, &org))
	reconcileProjects(t, r, "shop")

	_, ok := tenantQuotaIn(t, c, "shop-production")
	require.False(t, ok)
	_, ok = limitRangeIn(t, c, platform.ProjectDefaultsName, "shop-production")
	require.False(t, ok)
	require.Empty(t, namespaceAnnotations(t, c, "shop-production"))
	require.Nil(t, limitsCondition(t, c, "shop"), "nothing to hold, nothing to say")
}

func TestTheProjectsOwnQuotaKeepsItsDefaultsWhenTheOrganizationsLimitsGo(t *testing.T) {
	ctx := context.Background()
	r, c := limitsFixture(t, platformConfigWith(platformDefaults()), nil)
	var project geassv1alpha1.GeassProject
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "shop", Namespace: platform.SystemNamespace}, &project))
	project.Spec.EnvironmentQuota = quotaOf("4", "")
	require.NoError(t, c.Update(ctx, &project))
	reconcileProjects(t, r, "shop")

	var config geassv1alpha1.GeassPlatformConfig
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, &config))
	config.Spec.TenantLimits = nil
	require.NoError(t, c.Update(ctx, &config))
	reconcileProjects(t, r, "shop")

	defaults, ok := limitRangeIn(t, c, platform.ProjectDefaultsName, "shop-production")
	require.True(t, ok, "a quota on CPU still needs containers to ask for some")
	require.Len(t, defaults.Spec.Limits[0].DefaultRequest, 1)
	requireHard(t, defaults.Spec.Limits[0].DefaultRequest, corev1.ResourceCPU, "50m")
	require.Nil(t, defaults.Spec.Limits[0].Max)
	require.Nil(t, defaults.Spec.Limits[0].Default)
}

func TestLimitsThatCannotBeAppliedLeaveTheEnvironmentAsItWasAndSaySo(t *testing.T) {
	ctx := context.Background()
	r, c := limitsFixture(t, platformConfigWith(platformDefaults()), nil)
	reconcileProjects(t, r, "shop")

	// The organization's own container limits give a default above the most.
	var org geassv1alpha1.GeassOrganization
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: limitedOrg, Namespace: platform.SystemNamespace}, &org))
	org.Spec.Limits = &geassv1alpha1.GeassTenantLimits{
		Pods:      utilptr.To(int32(1)),
		Container: &geassv1alpha1.GeassContainerLimits{DefaultLimit: limitAmounts("8", "", ""), Max: limitAmounts("2", "", "10Gi")},
	}
	require.NoError(t, c.Update(ctx, &org))
	reconcileProjects(t, r, "shop")

	condition := limitsCondition(t, c, "shop")
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, "InvalidLimits", condition.Reason)
	require.Contains(t, condition.Message, "container.defaultLimit.cpu (8) is above container.max.cpu (2)")
	quota, _ := tenantQuotaIn(t, c, "shop-production")
	requireHard(t, quota.Spec.Hard, corev1.ResourcePods, "40")
	defaults, _ := limitRangeIn(t, c, platform.ProjectDefaultsName, "shop-production")
	requireHard(t, defaults.Spec.Limits[0].Max, corev1.ResourceCPU, "4")
	require.Equal(t, "100M", namespaceAnnotations(t, c, "shop-production")[platform.AnnotationPodIngressBandwidth], "nothing that held the namespace is taken away")
}

func TestAProjectSaysWhenTheClusterCannotGivePodsTheirBandwidth(t *testing.T) {
	r, base := limitsFixture(t, platformConfigWith(platformDefaults()), nil)
	watching, ok := base.(client.WithWatch)
	require.True(t, ok)
	// Kubernetes before 1.36 does not serve mutating admission policies.
	r.Client = interceptor.NewClient(watching, interceptor.Funcs{
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if _, mutating := obj.(*admissionv1.MutatingAdmissionPolicy); mutating {
				return &meta.NoKindMatchError{GroupKind: obj.GetObjectKind().GroupVersionKind().GroupKind()}
			}
			return c.Get(ctx, key, obj, opts...)
		},
	})

	reconcileProjects(t, r, "shop")

	condition := limitsCondition(t, base, "shop")
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, "BandwidthNotApplied", condition.Reason)
	_, ok = tenantQuotaIn(t, base, "shop-production")
	require.True(t, ok, "the other limits do not wait for it")
}

func TestAnEnvironmentTheLimitsCouldNotBeWrittenIntoIsNamedAndTriedAgain(t *testing.T) {
	r, base := limitsFixture(t, platformConfigWith(platformDefaults()), nil)
	watching, ok := base.(client.WithWatch)
	require.True(t, ok)
	r.Client = interceptor.NewClient(watching, interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if _, limits := obj.(*corev1.LimitRange); limits {
				return apierrors.NewInvalid(corev1.SchemeGroupVersion.WithKind("LimitRange").GroupKind(), obj.GetName(), nil)
			}
			return c.Create(ctx, obj, opts...)
		},
	})

	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKey{Name: "shop", Namespace: platform.SystemNamespace}})

	require.Error(t, err, "the reconcile is tried again")
	condition := limitsCondition(t, base, "shop")
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, "NotApplied", condition.Reason)
	require.Contains(t, condition.Message, "shop-production: ")
	var stored geassv1alpha1.GeassProject
	require.NoError(t, base.Get(context.Background(), client.ObjectKey{Name: "shop", Namespace: platform.SystemNamespace}, &stored))
	require.True(t, platform.IsConditionTrue(stored.Status.Conditions, platform.ConditionReady), "the environments are there; only their limits are not")
}

func TestATenantQuotaEditedByHandIsPutBackAndItsUsageWakesNothing(t *testing.T) {
	r := &GeassProjectReconciler{Client: quotaTestClient(quotaProject(nil))}
	quota := platform.TenantQuota("shop-production", map[string]string{platform.LabelProject: "shop"}, platformDefaults())

	requests := r.projectsForQuotaObject(context.Background(), quota)
	require.Len(t, requests, 1)
	require.Equal(t, "shop", requests[0].Name)

	edited := quota.DeepCopy()
	edited.Spec.Hard[corev1.ResourcePods] = resource.MustParse("1000")
	require.True(t, r.quotaObjectEdited().Update(event.UpdateEvent{ObjectOld: quota, ObjectNew: edited}))
	used := quota.DeepCopy()
	used.Status.Used = corev1.ResourceList{corev1.ResourcePods: resource.MustParse("3")}
	require.False(t, r.quotaObjectEdited().Update(event.UpdateEvent{ObjectOld: quota, ObjectNew: used}), "nothing is divided by what it says is used")
}

func TestOnlyAChangeOfTheDefaultLimitsReachesEveryOrganizationsProjects(t *testing.T) {
	changed := tenantLimitsChanged()
	before := platformConfigWith(platformDefaults())

	other := before.DeepCopy()
	other.Spec.DashboardURL = "https://geass.example.com"
	require.False(t, changed.Update(event.UpdateEvent{ObjectOld: before, ObjectNew: other}), "the rest of the configuration is none of the limits' business")
	fewer := before.DeepCopy()
	fewer.Spec.TenantLimits.Defaults.Pods = utilptr.To(int32(10))
	require.True(t, changed.Update(event.UpdateEvent{ObjectOld: before, ObjectNew: fewer}))
	require.True(t, changed.Create(event.CreateEvent{Object: before}))
	require.False(t, changed.Create(event.CreateEvent{Object: platformConfigWith(nil)}))
	elsewhere := before.DeepCopy()
	elsewhere.Name = "not-the-platform"
	require.False(t, changed.Create(event.CreateEvent{Object: elsewhere}))

	_, c := limitsFixture(t, before, nil)
	requests := projectsOfOrganizations(c)(context.Background(), before)
	require.Len(t, requests, 1, "a project of no organization is not one of them")
	require.Equal(t, "shop", requests[0].Name)
	requests = allOrganizations(c)(context.Background(), before)
	require.Len(t, requests, 1)
	require.Equal(t, limitedOrg, requests[0].Name)
}

// ---- the build namespace ----

func TestAnOrganizationsBuildsAreHeldToItsLimitsAndGivenTheMostAContainerMayHave(t *testing.T) {
	r, limitsClient := isolationFixture(t, isolatedOrg, false)
	require.NoError(t, limitsClient.Create(context.Background(), platformConfigWith(platformDefaults())))

	reconcileBuild(t, r, "api-build-1")
	namespace := platform.OrgBuildNamespace(isolatedOrg)

	quota, ok := tenantQuotaIn(t, limitsClient, namespace)
	require.True(t, ok)
	requireHard(t, quota.Spec.Hard, corev1.ResourcePods, "40")
	require.Equal(t, isolatedOrg, quota.Labels[platform.LabelOrgBuild])
	var buildQuota corev1.ResourceQuota
	require.NoError(t, limitsClient.Get(context.Background(), client.ObjectKey{Name: platform.OrgBuildQuotaName, Namespace: namespace}, &buildQuota), "the build quota of its own stays beside it")
	defaults, ok := limitRangeIn(t, limitsClient, platform.OrgBuildDefaultsName, namespace)
	require.True(t, ok)
	requireHard(t, defaults.Spec.Limits[0].Max, corev1.ResourceMemory, "8Gi")
	require.Equal(t, "50M", namespaceAnnotations(t, limitsClient, namespace)[platform.AnnotationPodEgressBandwidth])

	jobs := jobsIn(t, limitsClient, namespace)
	require.Len(t, jobs, 1)
	pod := jobs[0].Spec.Template.Spec
	for _, container := range append(append([]corev1.Container{}, pod.InitContainers...), pod.Containers...) {
		requireHard(t, container.Resources.Limits, corev1.ResourceMemory, "8Gi")
		requireHard(t, container.Resources.Limits, corev1.ResourceEphemeralStorage, "10Gi")
		requireHard(t, container.Resources.Requests, corev1.ResourceMemory, "128Mi")
		requireHard(t, container.Resources.Requests, corev1.ResourceCPU, "100m")
	}
}

func TestABuildOfAnOrganizationWithoutLimitsAsksForNothingAsBefore(t *testing.T) {
	r, c := isolationFixture(t, isolatedOrg, false)

	reconcileBuild(t, r, "api-build-1")
	namespace := platform.OrgBuildNamespace(isolatedOrg)

	_, ok := tenantQuotaIn(t, c, namespace)
	require.False(t, ok)
	_, ok = limitRangeIn(t, c, platform.OrgBuildDefaultsName, namespace)
	require.False(t, ok)
	jobs := jobsIn(t, c, namespace)
	require.Len(t, jobs, 1)
	require.Empty(t, jobs[0].Spec.Template.Spec.Containers[0].Resources.Limits)
	require.Empty(t, jobs[0].Spec.Template.Spec.Containers[0].Resources.Requests)
}

func TestTheOrganizationControllerKeepsAnExistingBuildNamespaceHeldToTheLimits(t *testing.T) {
	ctx := context.Background()
	org := &geassv1alpha1.GeassOrganization{
		ObjectMeta: metav1.ObjectMeta{Name: limitedOrg, Namespace: platform.SystemNamespace, Finalizers: []string{FinalizerOrganization}},
		Spec:       geassv1alpha1.GeassOrganizationSpec{DisplayName: "Acme", IdPID: "acme", Limits: &geassv1alpha1.GeassTenantLimits{Pods: utilptr.To(int32(5))}},
	}
	namespace := platform.OrgBuildNamespace(limitedOrg)
	c := fake.NewClientBuilder().WithScheme(limitsScheme(t)).WithObjects(org,
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace, Labels: platform.OrgBuildNamespaceLabels(limitedOrg)}}).Build()
	r := &GeassOrganizationReconciler{Client: c}
	reconcile := func() {
		t.Helper()
		_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(org)})
		require.NoError(t, err)
	}

	reconcile()
	quota, ok := tenantQuotaIn(t, c, namespace)
	require.True(t, ok)
	requireHard(t, quota.Spec.Hard, corev1.ResourcePods, "5")

	require.NoError(t, c.Get(ctx, client.ObjectKeyFromObject(org), org))
	org.Spec.Limits = nil
	require.NoError(t, c.Update(ctx, org))
	reconcile()
	_, ok = tenantQuotaIn(t, c, namespace)
	require.False(t, ok, "the limits go with the setting")

	// The edits that matter are traced back to the organization; what it says is used is not.
	edited := platform.TenantQuota(namespace, map[string]string{platform.LabelOrgBuild: limitedOrg}, &geassv1alpha1.GeassTenantLimits{Pods: utilptr.To(int32(5))})
	requests := organizationOfBuildLimit(ctx, edited)
	require.Len(t, requests, 1)
	require.Equal(t, limitedOrg, requests[0].Name)
	require.Empty(t, organizationOfBuildLimit(ctx, &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: platform.OrgBuildQuotaName, Namespace: namespace, Labels: map[string]string{platform.LabelOrgBuild: limitedOrg}}}))
	elsewhere := edited.DeepCopy()
	elsewhere.Namespace = "shop-production"
	require.Empty(t, organizationOfBuildLimit(ctx, elsewhere), "only in the organization's own build namespace")
	used := edited.DeepCopy()
	used.Status.Used = corev1.ResourceList{corev1.ResourcePods: resource.MustParse("2")}
	require.False(t, buildLimitEdited().Update(event.UpdateEvent{ObjectOld: edited, ObjectNew: used}))
}

func TestTheOrganizationControllerDoesNotMakeABuildNamespace(t *testing.T) {
	ctx := context.Background()
	org := &geassv1alpha1.GeassOrganization{
		ObjectMeta: metav1.ObjectMeta{Name: limitedOrg, Namespace: platform.SystemNamespace, Finalizers: []string{FinalizerOrganization}},
		Spec:       geassv1alpha1.GeassOrganizationSpec{DisplayName: "Acme", IdPID: "acme", Limits: &geassv1alpha1.GeassTenantLimits{Pods: utilptr.To(int32(5))}},
	}
	c := fake.NewClientBuilder().WithScheme(limitsScheme(t)).WithObjects(org).Build()

	_, err := (&GeassOrganizationReconciler{Client: c}).Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(org)})

	require.NoError(t, err)
	require.True(t, apierrors.IsNotFound(c.Get(ctx, client.ObjectKey{Name: platform.OrgBuildNamespace(limitedOrg)}, &corev1.Namespace{})), "it is made by the first build")
}

// ---- a service the limits refuse ----

func TestAServiceTheLimitsRefuseSaysWhy(t *testing.T) {
	refused := func(message string) *appsv1.Deployment {
		return &appsv1.Deployment{Status: appsv1.DeploymentStatus{Conditions: []appsv1.DeploymentCondition{{
			Type: appsv1.DeploymentReplicaFailure, Status: corev1.ConditionTrue, Reason: "FailedCreate", Message: message,
		}}}}
	}

	problem := quotaProblem(refused(`pods "web-7d9-x" is forbidden: maximum cpu usage per Container is 2, but limit is 4`))
	require.Equal(t, platform.MessageLimitsRefused+": maximum cpu usage per Container is 2, but limit is 4", problem)
	require.False(t, platform.ReconcileFailureShouldCount(problem), "a change to the limits lets it in, so it waits rather than being given up on")

	problem = quotaProblem(refused(`pods "web-7d9-x" is forbidden: [maximum cpu usage per Container is 2, but limit is 4, maximum memory usage per Container is 1Gi, but limit is 2Gi]`))
	require.Contains(t, problem, "maximum memory usage per Container is 1Gi")

	problem = quotaProblem(refused(`Pod "web-7d9-x" is invalid: spec.containers[0].resources.requests: Invalid value: "2": must be less than or equal to cpu limit of 1`))
	require.Equal(t, platform.MessagePodsInvalid+`: spec.containers[0].resources.requests: Invalid value: "2": must be less than or equal to cpu limit of 1`, problem)
	require.False(t, platform.ReconcileFailureShouldCount(problem))

	require.Empty(t, quotaProblem(refused(`pods "web" is forbidden: violates PodSecurity "restricted"`)))
	require.Empty(t, quotaProblem(refused(`Pod "web" is invalid: spec.containers[0].image: Required value`)))
}
