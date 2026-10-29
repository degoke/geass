package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilptr "k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const egressOrg = "org-9c8d7e6f"

// egressMiningPorts is a part of the list of mining pools' ports the documentation suggests.
var egressMiningPorts = []geassv1alpha1.GeassEgressPortRange{
	{Port: 3333, EndPort: utilptr.To(int32(3336))}, {Port: 4444}, {Port: 5555}, {Port: 7777}, {Port: 14433}, {Port: 14444}, {Port: 45700},
}

func platformEgressConfig(egress *geassv1alpha1.GeassTenantEgress) *geassv1alpha1.GeassPlatformConfig {
	config := &geassv1alpha1.GeassPlatformConfig{ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}}
	if egress != nil {
		config.Spec.Tenancy = &geassv1alpha1.GeassPlatformTenantIsolationSpec{Egress: egress}
	}
	return config
}

func egressScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	require.NoError(t, geassv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, networkingv1.AddToScheme(scheme))
	require.NoError(t, batchv1.AddToScheme(scheme))
	return scheme
}

// egressNode is the one node of the fixture's cluster, a control plane node.
var egressNode = corev1.Node{
	ObjectMeta: metav1.ObjectMeta{Name: "server-1", Labels: map[string]string{"node-role.kubernetes.io/control-plane": "true"}},
	Spec:       corev1.NodeSpec{PodCIDR: "10.42.0.0/24"},
	Status:     corev1.NodeStatus{Addresses: []corev1.NodeAddress{{Type: corev1.NodeInternalIP, Address: "10.0.0.5"}}},
}

// egressFixture is a ready cluster that enforces network policies, with two projects in it: one of the organization, with
// two environments, which keeps away from the cluster's machines, and one of nobody's. config is the platform's
// configuration (nil for none) and orgEgress the organization's own limits.
func egressFixture(t *testing.T, config *geassv1alpha1.GeassPlatformConfig, orgEgress *geassv1alpha1.GeassTenantEgress, extra ...client.Object) (*GeassProjectReconciler, client.Client) {
	t.Helper()
	scheme := egressScheme(t)
	cluster := &geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: testClusterName, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassClusterSpec{Version: "v1", ServerURL: "https://cluster.example", TokenSecretRef: corev1.SecretReference{Name: "token"}},
		Status: geassv1alpha1.GeassClusterStatus{Conditions: []metav1.Condition{
			{Type: platform.ConditionReady, Status: metav1.ConditionTrue},
			{Type: platform.ConditionNetworkPolicyEnforced, Status: metav1.ConditionTrue, Reason: "Enforced"},
		}},
	}
	tenant := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: "shop", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassProjectSpec{
			ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{"production", "staging"}, Org: egressOrg,
			Network: geassv1alpha1.NewProjectNetwork(),
		},
	}
	nobodys := &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: "intranet", Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{ClusterRef: corev1.LocalObjectReference{Name: testClusterName}, Environments: []string{"production"}},
	}
	org := &geassv1alpha1.GeassOrganization{
		ObjectMeta: metav1.ObjectMeta{Name: egressOrg, Namespace: platform.SystemNamespace},
		Spec:       geassv1alpha1.GeassOrganizationSpec{DisplayName: "Acme", IdPID: "acme", Egress: orgEgress},
	}
	node := egressNode.DeepCopy()
	objects := []client.Object{cluster, tenant, nobodys, org, node, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: platform.SystemNamespace}}}
	if config != nil {
		objects = append(objects, config)
	}
	objects = append(objects, extra...)
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(tenant, nobodys, cluster).WithObjects(objects...).Build()
	return &GeassProjectReconciler{Client: c, Scheme: scheme}, c
}

func reconcileEgressProjects(t *testing.T, r *GeassProjectReconciler, names ...string) {
	t.Helper()
	for _, name := range names {
		_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}})
		require.NoError(t, err, name)
	}
}

func projectConditionOf(t *testing.T, c client.Client, project, conditionType string) *metav1.Condition {
	t.Helper()
	var stored geassv1alpha1.GeassProject
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: project, Namespace: platform.SystemNamespace}, &stored))
	return meta.FindStatusCondition(stored.Status.Conditions, conditionType)
}

func planOf(t *testing.T, egress *geassv1alpha1.GeassTenantEgress) *platform.EgressPlan {
	t.Helper()
	plan, err := platform.PlanEgress(egress)
	require.NoError(t, err)
	return plan
}

// wantShopPolicy is what the environment's egress policy of the organization's project should be under the limits.
func wantShopPolicy(t *testing.T, namespace string, egress *geassv1alpha1.GeassTenantEgress) *networkingv1.NetworkPolicy {
	t.Helper()
	nodes := platform.NodeNetworkOf([]corev1.Node{egressNode}, platform.DefaultAPIServerPort)
	return platform.TenantCloudMetadataPolicy("shop", namespace, nodes, planOf(t, egress).ForEnvironment(platform.DefaultAPIServerPort))
}

func requirePolicy(t *testing.T, c client.Client, want *networkingv1.NetworkPolicy, why string) *networkingv1.NetworkPolicy {
	t.Helper()
	stored, ok := getPolicy(t, c, want.Namespace, want.Name)
	require.True(t, ok, "%s: %s/%s is missing", why, want.Namespace, want.Name)
	require.Equal(t, want.Labels, stored.Labels, why)
	require.Equal(t, want.Spec, stored.Spec, why)
	return stored
}

// limitedPolicies are the policies anywhere in the cluster with a rule that lists ports to 0.0.0.0/0, which is what the
// outbound limits write and nothing else of Geass's does.
func limitedPolicies(t *testing.T, c client.Client) []string {
	t.Helper()
	var policies networkingv1.NetworkPolicyList
	require.NoError(t, c.List(context.Background(), &policies))
	var out []string
	for _, policy := range policies.Items {
		for _, rule := range policy.Spec.Egress {
			if len(rule.Ports) > 0 && len(rule.To) == 1 && rule.To[0].IPBlock != nil && rule.To[0].IPBlock.CIDR == "0.0.0.0/0" {
				out = append(out, policy.Namespace+"/"+policy.Name)
				break
			}
		}
	}
	return out
}

func TestAnOrganizationsEnvironmentsGetItsLimitsAndNothingElseDoes(t *testing.T) {
	limits := &geassv1alpha1.GeassTenantEgress{BlockedPorts: egressMiningPorts}
	r, c := egressFixture(t, platformEgressConfig(limits), nil)
	reconcileEgressProjects(t, r, "shop", "intranet")

	for _, namespace := range []string{"shop-production", "shop-staging"} {
		requirePolicy(t, c, wantShopPolicy(t, namespace, limits), namespace+" holds the limits, the node block and the API rule together")
		var shop geassv1alpha1.GeassProject
		require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "shop", Namespace: platform.SystemNamespace}, &shop))
		requirePolicy(t, c, platform.ProjectIsolationPolicy(&shop, namespace), "the isolation policy is not touched")
	}
	requirePolicy(t, c, platform.CloudMetadataPolicy("intranet", "intranet-production", nil), "a project of no organization is never limited")
	require.ElementsMatch(t, []string{"shop-production/" + platform.CloudMetadataPolicyName, "shop-staging/" + platform.CloudMetadataPolicyName}, limitedPolicies(t, c),
		"the limits are written into the organization's environments and nowhere else: not geass-system, not kube-system, not another project")

	condition := projectConditionOf(t, c, "shop", platform.ConditionEgressLimitsApplied)
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionTrue, condition.Status)
	require.Equal(t, "Applied", condition.Reason)
	require.Contains(t, condition.Message, "TCP 25, 465, 587, 2465, 2525, 2587, 3333-3336, 4444, 5555, 7777")
	require.Nil(t, projectConditionOf(t, c, "intranet", platform.ConditionEgressLimitsApplied))
	require.Equal(t, metav1.ConditionTrue, projectConditionOf(t, c, "shop", platform.ConditionReady).Status, "the limits never hold a project back")
}

func TestWithoutTheSettingNothingIsWrittenThatWasNotBefore(t *testing.T) {
	for name, config := range map[string]*geassv1alpha1.GeassPlatformConfig{
		"no platform configuration":       nil,
		"a configuration without tenancy": platformEgressConfig(nil),
		"tenancy without egress":          {ObjectMeta: metav1.ObjectMeta{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, Spec: geassv1alpha1.GeassPlatformConfigSpec{Tenancy: &geassv1alpha1.GeassPlatformTenantIsolationSpec{}}},
	} {
		t.Run(name, func(t *testing.T) {
			r, c := egressFixture(t, config, nil)
			reconcileEgressProjects(t, r, "shop", "intranet")
			nodes := platform.NodeNetworkOf([]corev1.Node{egressNode}, platform.DefaultAPIServerPort)
			requirePolicy(t, c, platform.CloudMetadataPolicy("shop", "shop-production", nodes), "the policy is what it always was")
			require.Empty(t, limitedPolicies(t, c))
			require.Nil(t, projectConditionOf(t, c, "shop", platform.ConditionEgressLimitsApplied), "no limits, no condition")
		})
	}
}

func TestAnOrganizationsOwnLimitsHoldWithoutThePlatforms(t *testing.T) {
	own := &geassv1alpha1.GeassTenantEgress{}
	r, c := egressFixture(t, nil, own)
	reconcileEgressProjects(t, r, "shop")
	requirePolicy(t, c, wantShopPolicy(t, "shop-production", own), "an organization the platform set limits on directly: mail is closed")
}

func TestTheLimitsFollowChangesAndGoWithTheSetting(t *testing.T) {
	limits := &geassv1alpha1.GeassTenantEgress{}
	theirs := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "allow-monitoring-agent", Namespace: "shop-production"},
		Spec:       networkingv1.NetworkPolicySpec{PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}},
	}
	r, c := egressFixture(t, platformEgressConfig(limits), nil, theirs)
	ctx := context.Background()
	reconcileEgressProjects(t, r, "shop")
	requirePolicy(t, c, wantShopPolicy(t, "shop-production", limits), "mail closed")

	// The platform closes more.
	var config geassv1alpha1.GeassPlatformConfig
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, &config))
	config.Spec.Tenancy.Egress.BlockedPorts = egressMiningPorts
	require.NoError(t, c.Update(ctx, &config))
	reconcileEgressProjects(t, r, "shop")
	requirePolicy(t, c, wantShopPolicy(t, "shop-production", &geassv1alpha1.GeassTenantEgress{BlockedPorts: egressMiningPorts}), "the mining ports are closed too")

	// The organization is let send mail, through a relay only.
	var org geassv1alpha1.GeassOrganization
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: egressOrg, Namespace: platform.SystemNamespace}, &org))
	org.Spec.Egress = &geassv1alpha1.GeassTenantEgress{Allow: []geassv1alpha1.GeassEgressAllowance{{CIDR: "203.0.113.25/32", Ports: []geassv1alpha1.GeassEgressPortRange{{Port: 587}}}}}
	require.NoError(t, c.Update(ctx, &org))
	reconcileEgressProjects(t, r, "shop")
	requirePolicy(t, c, wantShopPolicy(t, "shop-production", &geassv1alpha1.GeassTenantEgress{BlockedPorts: egressMiningPorts, Allow: org.Spec.Egress.Allow}),
		"the organization's allowance is added and the platform's blocked ports kept")

	// Both take theirs away.
	org.Spec.Egress = nil
	require.NoError(t, c.Update(ctx, &org))
	config.Spec.Tenancy = nil
	require.NoError(t, c.Update(ctx, &config))
	reconcileEgressProjects(t, r, "shop")
	nodes := platform.NodeNetworkOf([]corev1.Node{egressNode}, platform.DefaultAPIServerPort)
	requirePolicy(t, c, platform.CloudMetadataPolicy("shop", "shop-production", nodes), "without the setting the policy is exactly what it was before")
	require.Empty(t, limitedPolicies(t, c))
	require.Nil(t, projectConditionOf(t, c, "shop", platform.ConditionEgressLimitsApplied), "and the condition goes")

	stored, ok := getPolicy(t, c, "shop-production", theirs.Name)
	require.True(t, ok, "a policy Geass did not write is never deleted")
	require.Equal(t, theirs.Spec, stored.Spec, "or changed")
}

func TestAHandEditedOrDeletedPolicyIsPutBackWithItsLimits(t *testing.T) {
	limits := &geassv1alpha1.GeassTenantEgress{BlockedPorts: egressMiningPorts}
	r, c := egressFixture(t, platformEgressConfig(limits), nil)
	ctx := context.Background()
	reconcileEgressProjects(t, r, "shop")
	want := wantShopPolicy(t, "shop-production", limits)

	stored := requirePolicy(t, c, want, "written")
	stored.Spec.Egress[1].Ports = nil
	require.NoError(t, c.Update(ctx, stored))
	require.NotEmpty(t, r.projectsForNetworkPolicy(ctx, stored), "an edit wakes the project")
	reconcileEgressProjects(t, r, "shop")
	requirePolicy(t, c, want, "a rule without ports would open every port; it is put back")

	require.NoError(t, c.Delete(ctx, stored))
	reconcileEgressProjects(t, r, "shop")
	requirePolicy(t, c, want, "a deleted policy is made again, with the limits")
}

func TestLimitsThatCannotBeAppliedLeaveEachEnvironmentAsItWas(t *testing.T) {
	limits := &geassv1alpha1.GeassTenantEgress{BlockedPorts: egressMiningPorts}
	r, c := egressFixture(t, platformEgressConfig(limits), nil)
	ctx := context.Background()
	reconcileEgressProjects(t, r, "shop")
	before, _ := getPolicy(t, c, "shop-production", platform.CloudMetadataPolicyName)

	// The CRD takes this range, but it is the metadata service, which is never opened.
	var org geassv1alpha1.GeassOrganization
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: egressOrg, Namespace: platform.SystemNamespace}, &org))
	org.Spec.Egress = &geassv1alpha1.GeassTenantEgress{Allow: []geassv1alpha1.GeassEgressAllowance{{CIDR: "169.254.169.254/32", Ports: []geassv1alpha1.GeassEgressPortRange{{Port: 80}}}}}
	require.NoError(t, c.Update(ctx, &org))
	// And a new environment is added while they are wrong.
	var shop geassv1alpha1.GeassProject
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: "shop", Namespace: platform.SystemNamespace}, &shop))
	shop.Spec.Environments = append(shop.Spec.Environments, "preview")
	require.NoError(t, c.Update(ctx, &shop))
	reconcileEgressProjects(t, r, "shop")

	after, _ := getPolicy(t, c, "shop-production", platform.CloudMetadataPolicyName)
	require.Equal(t, before.ResourceVersion, after.ResourceVersion, "the environment's policy is not written at all")
	require.Equal(t, before.Spec, after.Spec, "and keeps the limits it had")
	nodes := platform.NodeNetworkOf([]corev1.Node{egressNode}, platform.DefaultAPIServerPort)
	requirePolicy(t, c, platform.CloudMetadataPolicy("shop", "shop-preview", nodes), "a new environment still gets the metadata block, without limits")

	condition := projectConditionOf(t, c, "shop", platform.ConditionEgressLimitsApplied)
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, "InvalidLimits", condition.Reason)
	require.Contains(t, condition.Message, `allow[0].cidr: "169.254.169.254/32" is inside 169.254.0.0/16, which is never opened`)
	require.Equal(t, metav1.ConditionTrue, projectConditionOf(t, c, "shop", platform.ConditionReady).Status)

	// Fixed, they apply everywhere again.
	org.Spec.Egress = nil
	require.NoError(t, c.Update(ctx, &org))
	reconcileEgressProjects(t, r, "shop")
	for _, namespace := range []string{"shop-production", "shop-staging", "shop-preview"} {
		requirePolicy(t, c, wantShopPolicy(t, namespace, limits), namespace)
	}
	require.Equal(t, "Applied", projectConditionOf(t, c, "shop", platform.ConditionEgressLimitsApplied).Reason)
}

func TestTheConditionSaysWhetherTheClusterEnforcesPolicies(t *testing.T) {
	for name, tc := range map[string]struct {
		enforced *metav1.Condition
		status   metav1.ConditionStatus
		reason   string
		says     string
	}{
		"enforced":     {&metav1.Condition{Type: platform.ConditionNetworkPolicyEnforced, Status: metav1.ConditionTrue}, metav1.ConditionTrue, "Applied", "in force in every environment"},
		"not enforced": {&metav1.Condition{Type: platform.ConditionNetworkPolicyEnforced, Status: metav1.ConditionFalse}, metav1.ConditionFalse, "NotEnforced", "does not enforce network policies"},
		"unknown":      {&metav1.Condition{Type: platform.ConditionNetworkPolicyEnforced, Status: metav1.ConditionUnknown}, metav1.ConditionUnknown, "EnforcementUnknown", "could not tell"},
		"not checked":  {nil, metav1.ConditionUnknown, "EnforcementUnknown", "has not yet been checked"},
	} {
		t.Run(name, func(t *testing.T) {
			r, c := egressFixture(t, platformEgressConfig(&geassv1alpha1.GeassTenantEgress{}), nil)
			ctx := context.Background()
			var cluster geassv1alpha1.GeassCluster
			require.NoError(t, c.Get(ctx, client.ObjectKey{Name: testClusterName, Namespace: platform.SystemNamespace}, &cluster))
			meta.RemoveStatusCondition(&cluster.Status.Conditions, platform.ConditionNetworkPolicyEnforced)
			if tc.enforced != nil {
				meta.SetStatusCondition(&cluster.Status.Conditions, *tc.enforced)
			}
			require.NoError(t, c.Status().Update(ctx, &cluster))
			reconcileEgressProjects(t, r, "shop")

			condition := projectConditionOf(t, c, "shop", platform.ConditionEgressLimitsApplied)
			require.NotNil(t, condition)
			require.Equal(t, tc.status, condition.Status)
			require.Equal(t, tc.reason, condition.Reason)
			require.Contains(t, condition.Message, tc.says)
			requirePolicy(t, c, wantShopPolicy(t, "shop-production", &geassv1alpha1.GeassTenantEgress{}), "the policy is written whatever the plugin does")
		})
	}
}

func TestTheClusterWakesItsProjectsWhenEnforcementChanges(t *testing.T) {
	enforced := func(status metav1.ConditionStatus) *geassv1alpha1.GeassCluster {
		return &geassv1alpha1.GeassCluster{Status: geassv1alpha1.GeassClusterStatus{Conditions: []metav1.Condition{
			{Type: platform.ConditionReady, Status: metav1.ConditionTrue},
			{Type: platform.ConditionNetworkPolicyEnforced, Status: status},
		}}}
	}
	changed := networkPolicyEnforcementChanged()
	require.True(t, changed.Update(event.UpdateEvent{ObjectOld: enforced(metav1.ConditionTrue), ObjectNew: enforced(metav1.ConditionFalse)}))
	require.False(t, changed.Update(event.UpdateEvent{ObjectOld: enforced(metav1.ConditionTrue), ObjectNew: enforced(metav1.ConditionTrue)}),
		"a check that finds the same is not a change")
}

func TestOnlyAChangeToTheOutboundLimitsWakesTheirNamespaces(t *testing.T) {
	changed := tenantEgressChanged()
	with := func(egress *geassv1alpha1.GeassTenantEgress, rootDomain string) *geassv1alpha1.GeassPlatformConfig {
		config := platformEgressConfig(egress)
		config.Spec.RootDomain = rootDomain
		return config
	}
	require.True(t, changed.Update(event.UpdateEvent{ObjectOld: with(nil, "a.example"), ObjectNew: with(&geassv1alpha1.GeassTenantEgress{}, "a.example")}))
	require.True(t, changed.Update(event.UpdateEvent{ObjectOld: with(&geassv1alpha1.GeassTenantEgress{}, ""), ObjectNew: with(&geassv1alpha1.GeassTenantEgress{Mail: geassv1alpha1.EgressMailOpen}, "")}))
	require.False(t, changed.Update(event.UpdateEvent{ObjectOld: with(&geassv1alpha1.GeassTenantEgress{}, "a.example"), ObjectNew: with(&geassv1alpha1.GeassTenantEgress{}, "b.example")}),
		"a change to anything else is not")
	require.True(t, changed.Create(event.CreateEvent{Object: with(&geassv1alpha1.GeassTenantEgress{}, "")}))
	require.False(t, changed.Create(event.CreateEvent{Object: with(nil, "")}))
	other := with(&geassv1alpha1.GeassTenantEgress{}, "")
	other.Name = "not-the-platform"
	require.False(t, changed.Create(event.CreateEvent{Object: other}), "only the platform's configuration counts")

	r, _ := egressFixture(t, nil, nil)
	requests := projectsOfOrganizations(r.Client)(context.Background(), nil)
	require.Equal(t, []ctrl.Request{{NamespacedName: client.ObjectKey{Name: "shop", Namespace: platform.SystemNamespace}}}, requests, "the projects of organizations, and only those")
}

// buildEgressFixture is an organization's build that is about to start, with the platform's outbound limits.
func buildEgressFixture(t *testing.T, limits *geassv1alpha1.GeassTenantEgress) (*GeassBuildReconciler, *GeassOrganizationReconciler, client.Client) {
	t.Helper()
	r, c := isolationFixture(t, isolatedOrg, false)
	if limits != nil {
		require.NoError(t, c.Create(context.Background(), platformEgressConfig(limits)))
	}
	return r, &GeassOrganizationReconciler{Client: c}, c
}

func reconcileOrganization(t *testing.T, r *GeassOrganizationReconciler, name string) {
	t.Helper()
	for range 2 { // the first adds the finalizer
		_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}})
		require.NoError(t, err)
	}
}

func TestAnOrganizationsBuildNamespaceHoldsItsLimits(t *testing.T) {
	limits := &geassv1alpha1.GeassTenantEgress{BlockedPorts: egressMiningPorts}
	builds, orgs, c := buildEgressFixture(t, limits)
	ctx := context.Background()
	namespace := platform.OrgBuildNamespace(isolatedOrg)
	reconcileBuild(t, builds, "api-build-1")
	want := platform.TenantOrgBuildPolicy(namespace, isolatedOrg, nil, planOf(t, limits))
	requirePolicy(t, c, want, "the build that makes the namespace writes the limits")
	require.Equal(t, []string{namespace + "/" + platform.OrgBuildPolicyName}, limitedPolicies(t, c), "and only there: the project has no environment here")

	// A change reaches the namespace through the organization, without a build.
	var config geassv1alpha1.GeassPlatformConfig
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, &config))
	config.Spec.Tenancy.Egress.AllowedPorts = []geassv1alpha1.GeassEgressPortRange{{Port: 443}, {Port: 443, Protocol: corev1.ProtocolUDP}, {Port: 80}}
	require.NoError(t, c.Update(ctx, &config))
	require.Len(t, allOrganizations(c)(ctx, &config), 1)
	reconcileOrganization(t, orgs, isolatedOrg)
	want = platform.TenantOrgBuildPolicy(namespace, isolatedOrg, nil, planOf(t, config.Spec.Tenancy.Egress))
	requirePolicy(t, c, want, "only the listed ports, and no API port: a build has no use for it")

	// A hand edit is put back.
	stored, _ := getPolicy(t, c, namespace, platform.OrgBuildPolicyName)
	stored.Spec.Egress = append(stored.Spec.Egress, networkingv1.NetworkPolicyEgressRule{})
	require.NoError(t, c.Update(ctx, stored))
	require.Equal(t, []ctrl.Request{{NamespacedName: client.ObjectKey{Name: isolatedOrg, Namespace: platform.SystemNamespace}}}, organizationOfBuildPolicy(ctx, stored))
	reconcileOrganization(t, orgs, isolatedOrg)
	requirePolicy(t, c, want, "an empty rule would let the build reach everything; it is put back")

	// The next build to start keeps them.
	var org geassv1alpha1.GeassOrganization
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: isolatedOrg, Namespace: platform.SystemNamespace}, &org))
	_, err := builds.ensureOrgBuildNamespace(ctx, &org)
	require.NoError(t, err)
	requirePolicy(t, c, want, "a build writes the same")

	// The setting goes, and so do the limits.
	config.Spec.Tenancy = nil
	require.NoError(t, c.Update(ctx, &config))
	reconcileOrganization(t, orgs, isolatedOrg)
	requirePolicy(t, c, platform.OrgBuildPolicy(namespace, isolatedOrg, nil), "exactly the build policy there was before")
	require.Empty(t, limitedPolicies(t, c))
}

func TestInvalidLimitsLeaveTheBuildNamespaceAsItWas(t *testing.T) {
	limits := &geassv1alpha1.GeassTenantEgress{}
	builds, orgs, c := buildEgressFixture(t, limits)
	ctx := context.Background()
	namespace := platform.OrgBuildNamespace(isolatedOrg)
	reconcileBuild(t, builds, "api-build-1")
	before, _ := getPolicy(t, c, namespace, platform.OrgBuildPolicyName)

	var config geassv1alpha1.GeassPlatformConfig
	require.NoError(t, c.Get(ctx, client.ObjectKey{Name: platform.HAReadinessName, Namespace: platform.SystemNamespace}, &config))
	config.Spec.Tenancy.Egress.AllowedPorts = []geassv1alpha1.GeassEgressPortRange{{Port: 25}}
	require.NoError(t, c.Update(ctx, &config))
	reconcileOrganization(t, orgs, isolatedOrg)
	after, _ := getPolicy(t, c, namespace, platform.OrgBuildPolicyName)
	require.Equal(t, before.ResourceVersion, after.ResourceVersion, "limits that leave nothing open are not written")
	require.Equal(t, before.Spec, after.Spec)
}

func TestABuildNamespaceTheOrganizationDoesNotOwnIsLeftAlone(t *testing.T) {
	_, orgs, c := buildEgressFixture(t, &geassv1alpha1.GeassTenantEgress{})
	ctx := context.Background()
	namespace := platform.OrgBuildNamespace(isolatedOrg)
	require.NoError(t, c.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace, Labels: map[string]string{"owner": "someone-else"}}}))
	reconcileOrganization(t, orgs, isolatedOrg)
	_, written := getPolicy(t, c, namespace, platform.OrgBuildPolicyName)
	require.False(t, written, "Geass writes nothing into a namespace of that name that it did not make")

	reconcileOrganization(t, orgs, "org-that-has-no-build-namespace")
	require.Empty(t, organizationOfBuildPolicy(ctx, &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{
		Name: platform.OrgBuildPolicyName, Namespace: "shop-production", Labels: map[string]string{platform.LabelOrgBuild: isolatedOrg},
	}}), "a policy of that name outside the organization's build namespace is not the organization's")
	require.Empty(t, organizationOfBuildPolicy(ctx, &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: "mine", Namespace: namespace}}))
}
