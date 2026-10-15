package platform

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

func networkProject(name string, network *geassv1alpha1.GeassProjectNetwork) *geassv1alpha1.GeassProject {
	return &geassv1alpha1.GeassProject{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: SystemNamespace},
		Spec:       geassv1alpha1.GeassProjectSpec{Network: network},
	}
}

func TestAProjectIsIsolatedUnlessItOptedOut(t *testing.T) {
	require.Equal(t, geassv1alpha1.NetworkIsolationProject, geassv1alpha1.ProjectNetworkIsolation(networkProject("api", nil)),
		"a project that never chose is isolated: the safe setting must be the default")
	require.Equal(t, geassv1alpha1.NetworkIsolationProject, geassv1alpha1.ProjectNetworkIsolation(networkProject("api", &geassv1alpha1.GeassProjectNetwork{})))
	require.Equal(t, geassv1alpha1.NetworkIsolationNone, geassv1alpha1.ProjectNetworkIsolation(
		networkProject("api", &geassv1alpha1.GeassProjectNetwork{Isolation: geassv1alpha1.NetworkIsolationNone})))
}

func TestIsolationAdmitsTheProjectItsOptedInProjectsAndThePlatform(t *testing.T) {
	policy := ProjectIsolationPolicy(networkProject("shop", &geassv1alpha1.GeassProjectNetwork{
		AllowFromProjects: []string{"web", "admin", "web", "  ", "shop"},
	}), "shop-production")

	require.Equal(t, "shop-production", policy.Namespace)
	require.Equal(t, []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}, policy.Spec.PolicyTypes,
		"isolation limits who can come in; it must not touch what the project's pods can reach")
	require.Empty(t, policy.Spec.PodSelector.MatchLabels, "it covers every pod in the namespace, including ones added later")
	require.Len(t, policy.Spec.Ingress, 1)

	from := policy.Spec.Ingress[0].From
	projects := from[0].NamespaceSelector.MatchExpressions[0]
	require.Equal(t, LabelProject, projects.Key)
	require.Equal(t, metav1.LabelSelectorOpIn, projects.Operator)
	require.Equal(t, []string{"shop", "admin", "web"}, projects.Values,
		"itself first, then the others sorted, with repeats, blanks and itself listed again dropped")

	platformNamespaces := make([]string, 0, len(from)-1)
	for _, peer := range from[1:] {
		platformNamespaces = append(platformNamespaces, peer.NamespaceSelector.MatchLabels[LabelNamespaceName])
	}
	require.ElementsMatch(t, []string{"kube-system", SystemNamespace, MonitoringTargetNS, CNPGTargetNS, ServerlessTargetNS}, platformNamespaces,
		"the ingress controller, the operator and dashboard, Prometheus, the Postgres operator and the scale-to-zero proxy all call into projects")
}

func TestIsolationNeverNamesAnotherProjectUnlessOptedIn(t *testing.T) {
	policy := ProjectIsolationPolicy(networkProject("shop", nil), "shop-production")
	for _, peer := range policy.Spec.Ingress[0].From {
		if peer.NamespaceSelector == nil {
			continue
		}
		require.Empty(t, peer.NamespaceSelector.MatchLabels[LabelProject], "no peer may select by project through a plain label")
		for _, expression := range peer.NamespaceSelector.MatchExpressions {
			require.Equal(t, []string{"shop"}, expression.Values)
		}
	}
	for _, peer := range policy.Spec.Ingress[0].From {
		require.Nil(t, peer.IPBlock, "no address range is let in: that would admit other projects' pods again")
		require.Nil(t, peer.PodSelector, "a pod selector alone means this namespace only, and a pod selector inside a peer with a namespace selector would narrow it")
	}
}

func TestCloudMetadataIsBlockedButNothingElseIsCutOff(t *testing.T) {
	policy := CloudMetadataPolicy("shop", "shop-production", nil)

	require.Equal(t, []networkingv1.PolicyType{networkingv1.PolicyTypeEgress}, policy.Spec.PolicyTypes)
	require.Len(t, policy.Spec.Egress, 1)
	to := policy.Spec.Egress[0].To
	require.Len(t, to, 2)
	require.NotNil(t, to[0].NamespaceSelector, "every pod stays reachable, so DNS and other services keep working whatever the network plugin does with pod addresses")
	require.Empty(t, to[0].NamespaceSelector.MatchLabels)
	require.Equal(t, "0.0.0.0/0", to[1].IPBlock.CIDR, "the internet and private networks stay reachable: services use outside databases")
	require.Equal(t, []string{"169.254.0.0/16"}, to[1].IPBlock.Except, "the instance credentials service is the one thing that is not")
	require.Equal(t, "shop", policy.Labels[LabelProject], "labelled so a change to it is traced back to its project")
}

func TestBlockingTheNodesNamesThemInTheSameExceptionListAndKeepsTheAPIOpen(t *testing.T) {
	nodes := &NodeNetwork{
		Blocked:      []string{"10.0.0.5/32", "10.0.0.6/32", "10.42.0.0/32", "10.42.0.1/32"},
		APIAddresses: []string{"10.0.0.5/32"},
		APIPort:      6443,
	}
	policy := CloudMetadataPolicy("shop", "shop-production", nodes)

	require.Len(t, policy.Spec.Egress, 2, "everything but the machines, and the API on the control plane")
	pods, outside := policy.Spec.Egress[0].To[0], policy.Spec.Egress[0].To[1]
	require.NotNil(t, pods.NamespaceSelector, "pods stay reachable by selector, so blocking the machines never cuts off DNS or another service")
	require.Equal(t, "0.0.0.0/0", outside.IPBlock.CIDR)
	require.Equal(t, []string{"169.254.0.0/16", "10.0.0.5/32", "10.0.0.6/32", "10.42.0.0/32", "10.42.0.1/32"}, outside.IPBlock.Except,
		"the metadata range and the machines are one exception list: policies add up, so a second policy excepting the machines would be cancelled by this one allowing them")

	api := policy.Spec.Egress[1]
	require.Len(t, api.To, 1)
	require.Equal(t, "10.0.0.5/32", api.To[0].IPBlock.CIDR)
	require.Empty(t, api.To[0].IPBlock.Except)
	require.Len(t, api.Ports, 1)
	require.Equal(t, int32(6443), api.Ports[0].Port.IntVal, "a Postgres instance reaches the API through the control plane's address, and only on its API port")
	require.Equal(t, corev1.ProtocolTCP, *api.Ports[0].Protocol)
}

func TestBlockingTheNodesBeforeAnyIsKnownChangesNothingElse(t *testing.T) {
	policy := CloudMetadataPolicy("shop", "shop-production", &NodeNetwork{APIPort: 6443})

	require.Len(t, policy.Spec.Egress, 1, "no control plane address, so no rule for the API")
	require.Equal(t, []string{"169.254.0.0/16"}, policy.Spec.Egress[0].To[1].IPBlock.Except)
}

func TestNotBlockingTheNodesIsExactlyWhatItWasBefore(t *testing.T) {
	require.Equal(t, CloudMetadataPolicy("shop", "shop-production", nil), CloudMetadataPolicy("shop", "shop-production", &NodeNetwork{}),
		"a project that does not opt in must see no change when it is updated")
}

func TestTheMetricsAndLogStoresAcceptOnlyThePlatform(t *testing.T) {
	logs := LogStorePolicy()
	require.Equal(t, LoggingTargetNS, logs.Namespace)
	require.Empty(t, logs.Spec.PodSelector.MatchLabels, "everything in the logging namespace is the store or its shipper")

	metrics := MetricsStorePolicy()
	require.Equal(t, MonitoringTargetNS, metrics.Namespace)
	require.Equal(t, map[string]string{"app.kubernetes.io/name": "prometheus"}, metrics.Spec.PodSelector.MatchLabels,
		"only Prometheus: the namespace's admission webhook is called by the API server from outside the pod network")

	for _, policy := range []*networkingv1.NetworkPolicy{logs, metrics} {
		var namespaces []string
		for _, peer := range policy.Spec.Ingress[0].From {
			if peer.NamespaceSelector != nil {
				namespaces = append(namespaces, peer.NamespaceSelector.MatchLabels[LabelNamespaceName])
			}
		}
		require.Contains(t, namespaces, SystemNamespace, "the dashboard reads both")
		require.NotContains(t, namespaces, "kube-system")
		require.Equal(t, []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}, policy.Spec.PolicyTypes)
	}
}

func TestEveryPolicyIsOneTheOperatorWatches(t *testing.T) {
	for _, policy := range []*networkingv1.NetworkPolicy{
		ProjectIsolationPolicy(networkProject("shop", nil), "shop-production"),
		CloudMetadataPolicy("shop", "shop-production", nil),
		LogStorePolicy(),
		MetricsStorePolicy(),
	} {
		require.Contains(t, ManagedNetworkPolicyNames(), policy.Name)
		require.Equal(t, ManagedByValue, policy.Labels[LabelManagedBy])
	}
}
