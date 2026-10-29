package platform

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// curatedMiningPorts is the list the documentation gives for miningPools, written out port by port and apart from
// MiningPoolPorts, so that a change to either is a change to both.
var curatedMiningPorts = []int32{3333, 3334, 3335, 3336, 4444, 5555, 7777, 14433, 14444, 45700}

func TestMiningPoolsBlockedClosesExactlyTheCuratedPortsBesidesMail(t *testing.T) {
	plan := mustPlan(t, &geassv1alpha1.GeassTenantEgress{MiningPools: geassv1alpha1.EgressMiningPoolsBlocked})
	for name, policy := range map[string]*networkingv1.NetworkPolicy{
		"environment":     TenantCloudMetadataPolicy("shop", "shop-production", nil, plan.ForEnvironment(6443)),
		"build namespace": TenantOrgBuildPolicy("geass-build-org-1", "org-1", nil, plan),
	} {
		closed := append(slices.Clone(MailPorts()), curatedMiningPorts...)
		requireOpen(t, allBut(closed), openPorts(policy, "198.51.100.7", corev1.ProtocolTCP), name+": TCP to the internet")
		requireOpen(t, allBut(nil), openPorts(policy, "198.51.100.7", corev1.ProtocolUDP), name+": UDP is not touched: stratum is TCP")
		if name == "environment" {
			// A build reaches no private address and no pod but the cluster's DNS, limits or not.
			requireOpen(t, allBut(closed), openPorts(policy, "10.20.30.40", corev1.ProtocolTCP), name+": TCP to a private network")
			for _, port := range curatedMiningPorts {
				require.True(t, egressAllowed(policy, podIn("pool-proxy", port, corev1.ProtocolTCP)), "the cluster's own pods are reached on %d", port)
			}
		}
		for _, neighbour := range []int32{3332, 3337, 4443, 4445, 5554, 5556, 6666, 7776, 7778, 9999, 14432, 14434, 14443, 14445, 45699, 45701, 443} {
			require.True(t, egressAllowed(policy, outside("198.51.100.7", neighbour, corev1.ProtocolTCP)), "%s: %d, beside a pool's port, stays open", name, neighbour)
		}
	}
	require.Equal(t, "TCP 25, 465, 587, 2465, 2525, 2587, 3333-3336, 4444, 5555, 7777 and 3 more closed to addresses outside the cluster", plan.Summary())
}

func TestMiningPoolsAloneWithMailOpenCloseOnlyThePoolsPorts(t *testing.T) {
	plan := mustPlan(t, &geassv1alpha1.GeassTenantEgress{Mail: geassv1alpha1.EgressMailOpen, MiningPools: geassv1alpha1.EgressMiningPoolsBlocked})
	policy := TenantCloudMetadataPolicy("shop", "shop-production", nil, plan.ForEnvironment(6443))
	requireOpen(t, allBut(curatedMiningPorts), openPorts(policy, "198.51.100.7", corev1.ProtocolTCP), "only the pools' ports")
	require.Equal(t, "TCP 3333-3336, 4444, 5555, 7777, 14433, 14444, 45700 closed to addresses outside the cluster", plan.Summary())
}

func TestMiningPoolsOpenOrUnsetCloseNoneOfThem(t *testing.T) {
	nodes := &NodeNetwork{Blocked: []string{"10.0.0.5/32"}, APIAddresses: []string{"10.0.0.5/32"}, APIPort: 6443}
	for _, pools := range []geassv1alpha1.GeassEgressMiningPools{"", geassv1alpha1.EgressMiningPoolsOpen} {
		plan := mustPlan(t, &geassv1alpha1.GeassTenantEgress{MiningPools: pools})
		policy := TenantCloudMetadataPolicy("shop", "shop-production", nil, plan.ForEnvironment(6443))
		requireOpen(t, allBut(MailPorts()), openPorts(policy, "198.51.100.7", corev1.ProtocolTCP), "mail only, with miningPools "+string(pools))

		nothing := mustPlan(t, &geassv1alpha1.GeassTenantEgress{Mail: geassv1alpha1.EgressMailOpen, MiningPools: pools})
		require.Equal(t, CloudMetadataPolicy("shop", "shop-production", nodes), TenantCloudMetadataPolicy("shop", "shop-production", nodes, nothing.ForEnvironment(6443)),
			"miningPools %q with mail open changes nothing a policy says", pools)
	}
}

func TestClosedPoolsPortsWinOverAllowedPortsAndAnAllowanceOpensThem(t *testing.T) {
	plan := mustPlan(t, &geassv1alpha1.GeassTenantEgress{
		MiningPools:  geassv1alpha1.EgressMiningPoolsBlocked,
		AllowedPorts: []geassv1alpha1.GeassEgressPortRange{tcp(443), tcp(3333), tcp(4444)},
		Allow: []geassv1alpha1.GeassEgressAllowance{{
			CIDR: "203.0.113.9/32", Ports: []geassv1alpha1.GeassEgressPortRange{tcp(4444)}, Description: "a pool the platform runs on purpose",
		}},
	})
	policy := TenantCloudMetadataPolicy("shop", "shop-production", nil, plan.ForEnvironment(6443))
	requireOpen(t, only([]int32{443, 6443}), openPorts(policy, "198.51.100.7", corev1.ProtocolTCP), "an allowed port that is a pool's is closed")
	require.True(t, egressAllowed(policy, outside("203.0.113.9", 4444, corev1.ProtocolTCP)), "an allowance opens a pool's port to its addresses")
	require.False(t, egressAllowed(policy, outside("203.0.113.9", 3333, corev1.ProtocolTCP)), "and only the ports it names")
}

func TestAnOrganizationReplacesMiningPoolsAndAnUnknownValueIsRefused(t *testing.T) {
	blocked := &geassv1alpha1.GeassTenantEgress{MiningPools: geassv1alpha1.EgressMiningPoolsBlocked}
	open := &geassv1alpha1.GeassTenantEgress{MiningPools: geassv1alpha1.EgressMiningPoolsOpen}
	require.False(t, EffectiveTenantEgress(blocked, open).MiningPoolsBlocked(), "the organization's Open replaces the platform's Blocked")
	require.True(t, EffectiveTenantEgress(nil, blocked).MiningPoolsBlocked(), "an organization can close them when the platform does not")
	require.True(t, EffectiveTenantEgress(blocked, &geassv1alpha1.GeassTenantEgress{Mail: geassv1alpha1.EgressMailOpen}).MiningPoolsBlocked(),
		"an organization that leaves it out keeps the platform's")
	require.False(t, (*geassv1alpha1.GeassTenantEgress)(nil).MiningPoolsBlocked())

	_, err := PlanEgress(&geassv1alpha1.GeassTenantEgress{MiningPools: "Sometimes"})
	require.ErrorContains(t, err, `miningPools is "Sometimes", not Blocked or Open`)
}

func TestTheCuratedListIsWhatMiningPoolPortsReturns(t *testing.T) {
	var listed []int32
	for _, r := range MiningPoolPorts() {
		require.NoError(t, checkPortRange(r))
		require.Equal(t, corev1.ProtocolTCP, protocolOf(r))
		last := r.Port
		if r.EndPort != nil {
			last = *r.EndPort
		}
		for port := r.Port; port <= last; port++ {
			listed = append(listed, port)
		}
	}
	require.Equal(t, curatedMiningPorts, listed)
	require.NotSame(t, MiningPoolPorts()[0].EndPort, MiningPoolPorts()[0].EndPort, "each call is a copy nobody can change for the next")
}
