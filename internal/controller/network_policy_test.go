package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

func networkTestClient(objects ...client.Object) client.Client {
	scheme := runtime.NewScheme()
	_ = geassv1alpha1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = networkingv1.AddToScheme(scheme)
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func networkNamespace(name string, labels map[string]string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels}}
}

func networkTestProject(network *geassv1alpha1.GeassProjectNetwork) *geassv1alpha1.GeassProject {
	return &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: "shop", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{Network: network},
	}
}

func getPolicy(t *testing.T, c client.Client, namespace, name string) (*networkingv1.NetworkPolicy, bool) {
	t.Helper()
	policy := &networkingv1.NetworkPolicy{}
	err := c.Get(context.Background(), client.ObjectKey{Name: name, Namespace: namespace}, policy)
	if apierrors.IsNotFound(err) {
		return nil, false
	}
	require.NoError(t, err)
	return policy, true
}

func TestAProjectEnvironmentGetsIsolationAndTheMetadataBlock(t *testing.T) {
	c := networkTestClient(networkNamespace("shop-production", map[string]string{platform.LabelProject: "shop"}))
	r := &GeassProjectReconciler{Client: c}

	require.NoError(t, r.reconcileProjectNetwork(context.Background(), networkTestProject(nil), "shop-production"))

	_, isolated := getPolicy(t, c, "shop-production", platform.ProjectIsolationPolicyName)
	_, blocked := getPolicy(t, c, "shop-production", platform.CloudMetadataPolicyName)
	require.True(t, isolated, "a project that never chose is isolated")
	require.True(t, blocked)
}

func TestNetworkPoliciesFollowTheAllowList(t *testing.T) {
	c := networkTestClient(networkNamespace("shop-production", map[string]string{platform.LabelProject: "shop"}))
	r := &GeassProjectReconciler{Client: c}
	ctx := context.Background()

	require.NoError(t, r.reconcileProjectNetwork(ctx, networkTestProject(nil), "shop-production"))
	require.NoError(t, r.reconcileProjectNetwork(ctx, networkTestProject(&geassv1alpha1.GeassProjectNetwork{AllowFromProjects: []string{"web"}}), "shop-production"))

	policy, _ := getPolicy(t, c, "shop-production", platform.ProjectIsolationPolicyName)
	require.Equal(t, []string{"shop", "web"}, policy.Spec.Ingress[0].From[0].NamespaceSelector.MatchExpressions[0].Values)

	require.NoError(t, r.reconcileProjectNetwork(ctx, networkTestProject(nil), "shop-production"))
	policy, _ = getPolicy(t, c, "shop-production", platform.ProjectIsolationPolicyName)
	require.Equal(t, []string{"shop"}, policy.Spec.Ingress[0].From[0].NamespaceSelector.MatchExpressions[0].Values,
		"taking a project off the list closes the door again")
}

func TestOpeningAProjectRemovesIsolationButKeepsTheMetadataBlock(t *testing.T) {
	c := networkTestClient(networkNamespace("shop-production", map[string]string{platform.LabelProject: "shop"}))
	r := &GeassProjectReconciler{Client: c}
	ctx := context.Background()

	require.NoError(t, r.reconcileProjectNetwork(ctx, networkTestProject(nil), "shop-production"))
	open := networkTestProject(&geassv1alpha1.GeassProjectNetwork{Isolation: geassv1alpha1.NetworkIsolationNone})
	require.NoError(t, r.reconcileProjectNetwork(ctx, open, "shop-production"))

	_, isolated := getPolicy(t, c, "shop-production", platform.ProjectIsolationPolicyName)
	_, blocked := getPolicy(t, c, "shop-production", platform.CloudMetadataPolicyName)
	require.False(t, isolated)
	require.True(t, blocked, "opting out of isolation must not hand cloud credentials to the project's pods")

	require.NoError(t, r.reconcileProjectNetwork(ctx, open, "shop-production"), "reconciling an already-open project is not an error")
}

func TestANamespaceMissingItsProjectLabelIsRepairedSoTheProjectIsNotCutOffFromItself(t *testing.T) {
	c := networkTestClient(networkNamespace("shop-production", map[string]string{"keep": "me"}))
	r := &GeassProjectReconciler{Client: c}

	require.NoError(t, r.reconcileProjectNetwork(context.Background(), networkTestProject(nil), "shop-production"))

	ns := &corev1.Namespace{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "shop-production"}, ns))
	require.Equal(t, "shop", ns.Labels[platform.LabelProject])
	require.Equal(t, "me", ns.Labels["keep"], "labels that are already there are left alone")
}

func TestAPolicyChangedByHandIsPutBack(t *testing.T) {
	c := networkTestClient(networkNamespace("shop-production", map[string]string{platform.LabelProject: "shop"}))
	r := &GeassProjectReconciler{Client: c}
	ctx := context.Background()
	require.NoError(t, r.reconcileProjectNetwork(ctx, networkTestProject(nil), "shop-production"))

	policy, _ := getPolicy(t, c, "shop-production", platform.ProjectIsolationPolicyName)
	policy.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{{}}
	require.NoError(t, c.Update(ctx, policy))

	require.NoError(t, r.reconcileProjectNetwork(ctx, networkTestProject(nil), "shop-production"))
	policy, _ = getPolicy(t, c, "shop-production", platform.ProjectIsolationPolicyName)
	require.NotEmpty(t, policy.Spec.Ingress[0].From, "an empty ingress rule would admit everyone")
}

func TestOnlyOurPoliciesWakeTheProjectController(t *testing.T) {
	r := &GeassProjectReconciler{Client: networkTestClient()}
	ours := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{
		Name: platform.ProjectIsolationPolicyName, Namespace: "shop-production", Labels: map[string]string{platform.LabelProject: "shop"},
	}}
	requests := r.projectsForNetworkPolicy(context.Background(), ours)
	require.Len(t, requests, 1)
	require.Equal(t, "shop", requests[0].Name)
	require.Equal(t, platform.SystemNamespace, requests[0].Namespace)

	someoneElses := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: "mine", Namespace: "shop-production", Labels: map[string]string{platform.LabelProject: "shop"}}}
	require.Empty(t, r.projectsForNetworkPolicy(context.Background(), someoneElses))
	require.Empty(t, r.projectsForNetworkPolicy(context.Background(), &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: platform.LogStorePolicyName}}),
		"a store policy has no project to wake")
}

func telemetryCluster(monitoring, logging bool) *geassv1alpha1.GeassCluster {
	cluster := replicaTestCluster(false)
	cluster.Spec.Addons.Monitoring.Enabled = &monitoring
	cluster.Spec.Addons.Logging = geassv1alpha1.GeassClusterLoggingAddon{Enabled: &logging}
	return cluster
}

func TestTheStoresAreClosedOnceTheirNamespacesExist(t *testing.T) {
	c := networkTestClient(networkNamespace(platform.MonitoringTargetNS, nil), networkNamespace(platform.LoggingTargetNS, nil))
	r := &GeassClusterReconciler{Client: c}

	require.Zero(t, r.reconcileTelemetryIsolation(context.Background(), telemetryCluster(true, true)))

	_, metrics := getPolicy(t, c, platform.MonitoringTargetNS, platform.MetricsStorePolicyName)
	_, logs := getPolicy(t, c, platform.LoggingTargetNS, platform.LogStorePolicyName)
	require.True(t, metrics)
	require.True(t, logs)
}

func TestTheStoresAreRetriedWhileTheirChartsAreStillCreatingTheNamespace(t *testing.T) {
	c := networkTestClient(networkNamespace(platform.MonitoringTargetNS, nil))
	r := &GeassClusterReconciler{Client: c}

	require.Equal(t, telemetryPolicyRetry, r.reconcileTelemetryIsolation(context.Background(), telemetryCluster(true, true)),
		"the log store's namespace is not there yet, so look again soon")
	_, metrics := getPolicy(t, c, platform.MonitoringTargetNS, platform.MetricsStorePolicyName)
	require.True(t, metrics, "the namespace that exists is not held up by the one that does not")
}

func TestAStoreThatIsNotInstalledIsNotWaitedFor(t *testing.T) {
	c := networkTestClient()
	r := &GeassClusterReconciler{Client: c}

	require.Zero(t, r.reconcileTelemetryIsolation(context.Background(), telemetryCluster(false, false)),
		"no monitoring and no logging means nothing to close and nothing to retry")
}
