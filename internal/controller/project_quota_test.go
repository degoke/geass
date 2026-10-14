package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func quotaTestClient(objects ...client.Object) client.Client {
	scheme := runtime.NewScheme()
	_ = geassv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func quotaOf(cpu, memory string) *geassv1alpha1.GeassEnvironmentQuota {
	quota := &geassv1alpha1.GeassEnvironmentQuota{}
	if cpu != "" {
		q := resource.MustParse(cpu)
		quota.CPU = &q
	}
	if memory != "" {
		q := resource.MustParse(memory)
		quota.Memory = &q
	}
	return quota
}

func quotaProject(quota *geassv1alpha1.GeassEnvironmentQuota) *geassv1alpha1.GeassProject {
	return &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: "shop", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{EnvironmentQuota: quota},
	}
}

func hasQuotaObjects(t *testing.T, c client.Client) (quota, defaults bool) {
	t.Helper()
	err := c.Get(context.Background(), client.ObjectKey{Name: platform.ProjectQuotaName, Namespace: "shop-production"}, &corev1.ResourceQuota{})
	require.True(t, err == nil || apierrors.IsNotFound(err), "%v", err)
	quota = err == nil
	err = c.Get(context.Background(), client.ObjectKey{Name: platform.ProjectDefaultsName, Namespace: "shop-production"}, &corev1.LimitRange{})
	require.True(t, err == nil || apierrors.IsNotFound(err), "%v", err)
	return quota, err == nil
}

func TestAProjectWithNoQuotaHasNoQuotaObjects(t *testing.T) {
	c := quotaTestClient()
	r := &GeassProjectReconciler{Client: c}

	for _, project := range []*geassv1alpha1.GeassProject{quotaProject(nil), quotaProject(&geassv1alpha1.GeassEnvironmentQuota{})} {
		require.NoError(t, r.reconcileProjectQuota(context.Background(), project, "shop-production", nil))
		quota, defaults := hasQuotaObjects(t, c)
		require.False(t, quota, "nothing is capped until someone chooses it")
		require.False(t, defaults)
	}
}

func TestAnEnvironmentGetsTheQuotaAndTheDefaultsItNeeds(t *testing.T) {
	c := quotaTestClient()
	r := &GeassProjectReconciler{Client: c}

	require.NoError(t, r.reconcileProjectQuota(context.Background(), quotaProject(quotaOf("4", "8Gi")), "shop-production", nil))

	var quota corev1.ResourceQuota
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: platform.ProjectQuotaName, Namespace: "shop-production"}, &quota))
	cpu := quota.Spec.Hard[corev1.ResourceRequestsCPU]
	require.Zero(t, cpu.Cmp(resource.MustParse("4")))
	_, defaults := hasQuotaObjects(t, c)
	require.True(t, defaults, "a quota on CPU or memory refuses a pod that asks for neither, so containers get a small default request")
}

func TestChangingTheQuotaChangesTheObjectsAndRemovingItRemovesThem(t *testing.T) {
	c := quotaTestClient()
	r := &GeassProjectReconciler{Client: c}
	ctx := context.Background()

	require.NoError(t, r.reconcileProjectQuota(ctx, quotaProject(quotaOf("4", "8Gi")), "shop-production", nil))
	require.NoError(t, r.reconcileProjectQuota(ctx, quotaProject(quotaOf("2", "")), "shop-production", nil))

	var quota corev1.ResourceQuota
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.ProjectQuotaName, Namespace: "shop-production"}, &quota))
	require.Len(t, quota.Spec.Hard, 1, "a cap that was taken away is gone, not left at its old value")
	var defaults corev1.LimitRange
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.ProjectDefaultsName, Namespace: "shop-production"}, &defaults))
	require.Len(t, defaults.Spec.Limits[0].DefaultRequest, 1)

	pods := int32(10)
	require.NoError(t, r.reconcileProjectQuota(ctx, quotaProject(&geassv1alpha1.GeassEnvironmentQuota{Pods: &pods}), "shop-production", nil))
	quotaPresent, defaultsPresent := hasQuotaObjects(t, c)
	require.True(t, quotaPresent)
	require.False(t, defaultsPresent, "a cap on pods alone refuses no pod for lacking a request")

	require.NoError(t, r.reconcileProjectQuota(ctx, quotaProject(nil), "shop-production", nil))
	quotaPresent, defaultsPresent = hasQuotaObjects(t, c)
	require.False(t, quotaPresent)
	require.False(t, defaultsPresent)
}

func TestAQuotaSomeoneElseMadeIsNotRemoved(t *testing.T) {
	theirs := &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: platform.ProjectQuotaName, Namespace: "shop-production"}}
	c := quotaTestClient(theirs)
	r := &GeassProjectReconciler{Client: c}

	require.NoError(t, r.reconcileProjectQuota(context.Background(), quotaProject(nil), "shop-production", nil))

	quota, _ := hasQuotaObjects(t, c)
	require.True(t, quota, "only what the operator labelled as its own is deleted")
}

func TestAQuotaEditedByHandIsPutBack(t *testing.T) {
	c := quotaTestClient()
	r := &GeassProjectReconciler{Client: c}
	ctx := context.Background()
	require.NoError(t, r.reconcileProjectQuota(ctx, quotaProject(quotaOf("4", "")), "shop-production", nil))

	var quota corev1.ResourceQuota
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.ProjectQuotaName, Namespace: "shop-production"}, &quota))
	quota.Spec.Hard[corev1.ResourceRequestsCPU] = resource.MustParse("400")
	require.NoError(t, c.Update(ctx, &quota))
	require.NoError(t, r.reconcileProjectQuota(ctx, quotaProject(quotaOf("4", "")), "shop-production", nil))

	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.ProjectQuotaName, Namespace: "shop-production"}, &quota))
	cpu := quota.Spec.Hard[corev1.ResourceRequestsCPU]
	require.Zero(t, cpu.Cmp(resource.MustParse("4")))
}

func TestQuotaObjectsAreTracedBackToTheirProject(t *testing.T) {
	r := &GeassProjectReconciler{Client: quotaTestClient()}
	owned := &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: platform.ProjectQuotaName, Labels: map[string]string{platform.LabelProject: "shop"}}}

	requests := r.projectsForQuotaObject(context.Background(), owned)
	require.Len(t, requests, 1)
	require.Equal(t, "shop", requests[0].Name)
	require.Equal(t, platform.SystemNamespace, requests[0].Namespace)

	other := &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: "their-quota", Labels: map[string]string{platform.LabelProject: "shop"}}}
	require.Empty(t, r.projectsForQuotaObject(context.Background(), other), "a quota the operator does not own is none of its business")
	require.Empty(t, r.projectsForQuotaObject(context.Background(), &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: platform.ProjectQuotaName}}))
}

func TestAQuotasUsageDoesNotWakeTheProjectButAnEditDoes(t *testing.T) {
	predicate := (&GeassProjectReconciler{Client: quotaTestClient(quotaProject(quotaOf("4", "")))}).quotaObjectEdited()
	before := platform.ProjectResourceQuota("shop", "shop-production", corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("4")})

	used := before.DeepCopy()
	used.Status.Used = corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("3")}
	require.False(t, predicate.Update(event.UpdateEvent{ObjectOld: before, ObjectNew: used}), "usage changes with every pod that starts or stops")

	edited := before.DeepCopy()
	edited.Spec.Hard[corev1.ResourceRequestsCPU] = resource.MustParse("400")
	require.True(t, predicate.Update(event.UpdateEvent{ObjectOld: before, ObjectNew: edited}))
	require.True(t, predicate.Delete(event.DeleteEvent{Object: before}))
	require.False(t, predicate.Create(event.CreateEvent{Object: before}))

	defaultsBefore := platform.ProjectLimitRange("shop", "shop-production", corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("4")})
	defaultsEdited := defaultsBefore.DeepCopy()
	defaultsEdited.Spec.Limits[0].DefaultRequest[corev1.ResourceCPU] = resource.MustParse("4")
	require.True(t, predicate.Update(event.UpdateEvent{ObjectOld: defaultsBefore, ObjectNew: defaultsEdited}))
}

func TestAServiceBlockedByTheQuotaSaysSoInsteadOfStarting(t *testing.T) {
	deploy := &appsv1.Deployment{Status: appsv1.DeploymentStatus{Conditions: []appsv1.DeploymentCondition{{
		Type: appsv1.DeploymentReplicaFailure, Status: corev1.ConditionTrue, Reason: "FailedCreate",
		Message: `pods "web-7d9-x" is forbidden: exceeded quota: geass-project-quota, requested: requests.cpu=500m, used: requests.cpu=3500m, limited: requests.cpu=4`,
	}}}}

	problem := quotaProblem(deploy)

	require.Equal(t, platform.MessageQuotaUsedUp+": geass-project-quota, requested: requests.cpu=500m, used: requests.cpu=3500m, limited: requests.cpu=4", problem)
	require.False(t, platform.ReconcileFailureShouldCount(problem))
}

func TestOtherReplicaFailuresAreNotBlamedOnTheQuota(t *testing.T) {
	require.Empty(t, quotaProblem(&appsv1.Deployment{}))
	require.Empty(t, quotaProblem(&appsv1.Deployment{Status: appsv1.DeploymentStatus{Conditions: []appsv1.DeploymentCondition{{
		Type: appsv1.DeploymentReplicaFailure, Status: corev1.ConditionTrue, Message: `pods "web" is forbidden: violates PodSecurity "restricted"`,
	}}}}))
	require.Empty(t, quotaProblem(&appsv1.Deployment{Status: appsv1.DeploymentStatus{Conditions: []appsv1.DeploymentCondition{{
		Type: appsv1.DeploymentReplicaFailure, Status: corev1.ConditionFalse, Message: "exceeded quota: x",
	}}}}), "a failure that has cleared is not a failure")
}

// ---- a cap on the whole project ----

func wideProject(total *geassv1alpha1.GeassProjectQuota, environment *geassv1alpha1.GeassEnvironmentQuota, environments ...string) *geassv1alpha1.GeassProject {
	return &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: "shop", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{Environments: environments, ProjectQuota: total, EnvironmentQuota: environment},
	}
}

func cpuTotal(value string) *geassv1alpha1.GeassProjectQuota {
	q := resource.MustParse(value)
	return &geassv1alpha1.GeassProjectQuota{CPU: &q}
}

// usedQuota is the quota Geass made in an environment, with what Kubernetes says it uses.
func usedQuota(namespace, cpu string) *corev1.ResourceQuota {
	quota := platform.ProjectResourceQuota("shop", namespace, corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("8")})
	quota.Status.Used = corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse(cpu)}
	return quota
}

func hardCPU(t *testing.T, c client.Client, namespace string) string {
	t.Helper()
	var quota corev1.ResourceQuota
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: platform.ProjectQuotaName, Namespace: namespace}, &quota))
	cpu := quota.Spec.Hard[corev1.ResourceRequestsCPU]
	return cpu.String()
}

func TestEachEnvironmentMayAskForWhatTheOthersLeaveOfTheProjectTotal(t *testing.T) {
	c := quotaTestClient(usedQuota("shop-production", "3"), usedQuota("shop-staging", "1500m"))
	r := &GeassProjectReconciler{Client: c}
	ctx := context.Background()
	project := wideProject(cpuTotal("8"), nil, "production", "staging", "pr-7")

	usage, err := r.environmentUsage(ctx, project)
	require.NoError(t, err)
	for _, namespace := range []string{"shop-production", "shop-staging", "shop-pr-7"} {
		require.NoError(t, r.reconcileProjectQuota(ctx, project, namespace, usage))
	}

	require.Equal(t, "5", hardCPU(t, c, "shop-staging"), "production uses 3 of the 8, and what staging uses itself is not held against it")
	require.Equal(t, "6500m", hardCPU(t, c, "shop-production"), "staging uses 1500m, so production may ask for the 6500m that is left")
	require.Equal(t, "3500m", hardCPU(t, c, "shop-pr-7"), "an environment that was only just made may ask for what both leave")
}

func TestTheProjectWideCapMovesWhenAnotherEnvironmentsUsageDoes(t *testing.T) {
	c := quotaTestClient(usedQuota("shop-production", "6"), usedQuota("shop-staging", "0"))
	r := &GeassProjectReconciler{Client: c}
	ctx := context.Background()
	project := wideProject(cpuTotal("8"), nil, "production", "staging")
	reconcileAll := func() {
		t.Helper()
		usage, err := r.environmentUsage(ctx, project)
		require.NoError(t, err)
		for _, namespace := range []string{"shop-production", "shop-staging"} {
			require.NoError(t, r.reconcileProjectQuota(ctx, project, namespace, usage))
		}
	}

	reconcileAll()
	require.Equal(t, "2", hardCPU(t, c, "shop-staging"), "production uses 6 of 8, so staging may ask for 2")
	require.Equal(t, "8", hardCPU(t, c, "shop-production"), "and production, which staging uses none of, may still have the whole 8")

	var production corev1.ResourceQuota
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.ProjectQuotaName, Namespace: "shop-production"}, &production))
	production.Status.Used = corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("1")}
	require.NoError(t, c.Update(ctx, &production))

	reconcileAll()
	require.Equal(t, "7", hardCPU(t, c, "shop-staging"), "production gave room back, so staging may ask for it")
}

func TestAnEnvironmentCapStillAppliesUnderAProjectWideCap(t *testing.T) {
	c := quotaTestClient()
	r := &GeassProjectReconciler{Client: c}
	ctx := context.Background()
	project := wideProject(cpuTotal("8"), quotaOf("3", ""), "production")

	require.NoError(t, r.reconcileProjectQuota(ctx, project, "shop-production", nil))

	require.Equal(t, "3", hardCPU(t, c, "shop-production"), "the lower of the two is what holds")
}

func TestAProjectWideCapAloneCreatesTheQuotaAndTheDefaults(t *testing.T) {
	c := quotaTestClient()
	r := &GeassProjectReconciler{Client: c}

	require.NoError(t, r.reconcileProjectQuota(context.Background(), wideProject(cpuTotal("8"), nil, "production"), "shop-production", nil))

	quota, defaults := hasQuotaObjects(t, c)
	require.True(t, quota)
	require.True(t, defaults, "a cap on CPU is a cap on what pods ask for, so a container that asks for nothing is counted as asking for a little")
}

func TestRemovingBothCapsRemovesTheQuota(t *testing.T) {
	c := quotaTestClient()
	r := &GeassProjectReconciler{Client: c}
	ctx := context.Background()
	require.NoError(t, r.reconcileProjectQuota(ctx, wideProject(cpuTotal("8"), nil, "production"), "shop-production", nil))

	require.NoError(t, r.reconcileProjectQuota(ctx, wideProject(nil, nil, "production"), "shop-production", nil))

	quota, defaults := hasQuotaObjects(t, c)
	require.False(t, quota)
	require.False(t, defaults)
}

func TestOnlyAProjectWithAWholeProjectCapLooksAtUsage(t *testing.T) {
	c := quotaTestClient(usedQuota("shop-production", "3"))
	r := &GeassProjectReconciler{Client: c}

	none, err := r.environmentUsage(context.Background(), wideProject(nil, quotaOf("4", ""), "production"))
	require.NoError(t, err)
	require.Nil(t, none, "an environment cap needs no usage: Kubernetes enforces it by itself")

	some, err := r.environmentUsage(context.Background(), wideProject(cpuTotal("8"), nil, "production", "staging"))
	require.NoError(t, err)
	require.Len(t, some, 1, "an environment with no quota yet is left out")
	require.Contains(t, some, "shop-production")
}

func TestAWholeProjectCapIsWokenByUsageButAnEnvironmentCapIsNot(t *testing.T) {
	wide := wideProject(cpuTotal("8"), nil, "production")
	narrow := wideProject(nil, quotaOf("4", ""), "production")
	narrow.Name = "narrow"
	r := &GeassProjectReconciler{Client: quotaTestClient(wide, narrow)}
	predicate := r.quotaObjectEdited()

	before := usedQuota("shop-production", "1")
	after := usedQuota("shop-production", "2")
	require.True(t, predicate.Update(event.UpdateEvent{ObjectOld: before, ObjectNew: after}), "the other environments' caps follow what this one uses")

	beforeNarrow := usedQuota("narrow-production", "1")
	beforeNarrow.Labels[platform.LabelProject] = "narrow"
	afterNarrow := beforeNarrow.DeepCopy()
	afterNarrow.Status.Used = corev1.ResourceList{corev1.ResourceRequestsCPU: resource.MustParse("2")}
	require.False(t, predicate.Update(event.UpdateEvent{ObjectOld: beforeNarrow, ObjectNew: afterNarrow}), "nothing follows it, so waking for it would be reconciling a project for no reason")
}

func TestTheProjectWideUsageIsReportedOnTheProject(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, networkingv1.AddToScheme(scheme))
	cluster := &geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: testClusterName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassClusterSpec{Version: "v1", ServerURL: "https://cluster.example", TokenSecretRef: corev1.SecretReference{Name: "token"}},
		Status:     geassv1alpha1.GeassClusterStatus{Conditions: []metav1.Condition{{Type: platform.ConditionReady, Status: metav1.ConditionTrue}}},
	}
	project := wideProject(cpuTotal("8"), nil, testEnvDev, testEnvStaging)
	project.Name = testProjectName
	project.Spec.ClusterRef = corev1.LocalObjectReference{Name: testClusterName}
	devQuota := usedQuota(testProjectName+"-"+testEnvDev, "2")
	devQuota.Labels[platform.LabelProject] = testProjectName
	stagingQuota := usedQuota(testProjectName+"-"+testEnvStaging, "500m")
	stagingQuota.Labels[platform.LabelProject] = testProjectName
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(project, cluster).WithObjects(project, cluster, devQuota, stagingQuota).Build()
	r := &GeassProjectReconciler{Client: c, Scheme: scheme}

	_, err := r.Reconcile(context.Background(), requestFor(project))
	require.NoError(t, err)

	var updated geassv1alpha1.GeassProject
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(project), &updated))
	require.NotNil(t, updated.Status.QuotaUsage)
	require.Zero(t, updated.Status.QuotaUsage.CPU.Cmp(resource.MustParse("2500m")), "what the two environments use together")
	require.Nil(t, updated.Status.QuotaUsage.Memory, "only what the project capped")

	updated.Spec.ProjectQuota = nil
	require.NoError(t, c.Update(context.Background(), &updated))
	_, err = r.Reconcile(context.Background(), requestFor(project))
	require.NoError(t, err)
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(project), &updated))
	require.Nil(t, updated.Status.QuotaUsage, "a project that no longer has a cap has no usage to report against it")
}
