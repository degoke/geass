package platform

import (
	"net/netip"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilptr "k8s.io/utils/ptr"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// destination is where a pod opens a connection to: a pod of the cluster in a namespace, or an address outside it.
type destination struct {
	podNamespace string
	address      netip.Addr
	port         int32
	protocol     corev1.Protocol
}

func outside(address string, port int32, protocol corev1.Protocol) destination {
	return destination{address: netip.MustParseAddr(address), port: port, protocol: protocol}
}

func podIn(namespace string, port int32, protocol corev1.Protocol) destination {
	return destination{podNamespace: namespace, port: port, protocol: protocol}
}

// egressAllowed says, from nothing but the policy as it would be stored, whether it lets a connection out, the way the
// NetworkPolicy API defines it: a connection is allowed when one rule allows it, and a rule allows it when one of its peers
// holds the destination (or it names none) and one of its ports does (or it names none). An address block is taken to hold
// addresses outside the cluster only, which is what the policies assume of the plugins that count least as inside it. It
// is written apart from the code that builds the policies, so that the two check each other.
func egressAllowed(policy *networkingv1.NetworkPolicy, to destination) bool {
	for _, rule := range policy.Spec.Egress {
		if peersHold(rule.To, to) && portsHold(rule.Ports, to) {
			return true
		}
	}
	return false
}

func peersHold(peers []networkingv1.NetworkPolicyPeer, to destination) bool {
	if len(peers) == 0 {
		return true
	}
	for _, peer := range peers {
		switch {
		case peer.IPBlock != nil:
			if to.podNamespace != "" {
				continue
			}
			if netip.MustParsePrefix(peer.IPBlock.CIDR).Contains(to.address) && !slices.ContainsFunc(peer.IPBlock.Except, func(except string) bool {
				return netip.MustParsePrefix(except).Contains(to.address)
			}) {
				return true
			}
		case peer.NamespaceSelector != nil:
			if to.podNamespace == "" {
				continue
			}
			name, named := peer.NamespaceSelector.MatchLabels[LabelNamespaceName]
			if !named || name == to.podNamespace {
				return true
			}
		}
	}
	return false
}

func portsHold(ports []networkingv1.NetworkPolicyPort, to destination) bool {
	if len(ports) == 0 {
		return true
	}
	for _, port := range ports {
		protocol := corev1.ProtocolTCP
		if port.Protocol != nil {
			protocol = *port.Protocol
		}
		if protocol != to.protocol {
			continue
		}
		if port.Port == nil {
			return true
		}
		if port.Port.Type != intstr.Int {
			panic("a named port: Geass never writes one")
		}
		first, last := port.Port.IntVal, port.Port.IntVal
		if port.EndPort != nil {
			last = *port.EndPort
		}
		if to.port >= first && to.port <= last {
			return true
		}
	}
	return false
}

// openPorts are the ports of a protocol the policy lets out to an address, port by port.
func openPorts(policy *networkingv1.NetworkPolicy, address string, protocol corev1.Protocol) map[int32]bool {
	open := map[int32]bool{}
	for port := int32(1); port <= 65535; port++ {
		if egressAllowed(policy, outside(address, port, protocol)) {
			open[port] = true
		}
	}
	return open
}

// allBut is every port from 1 to 65535 except the ones named, and except the ranges given as pairs.
func allBut(single []int32, ranges ...[2]int32) map[int32]bool {
	out := map[int32]bool{}
	for port := int32(1); port <= 65535; port++ {
		out[port] = true
	}
	for _, port := range single {
		delete(out, port)
	}
	for _, r := range ranges {
		for port := r[0]; port <= r[1]; port++ {
			delete(out, port)
		}
	}
	return out
}

func only(single []int32, ranges ...[2]int32) map[int32]bool {
	out := map[int32]bool{}
	for _, port := range single {
		out[port] = true
	}
	for _, r := range ranges {
		for port := r[0]; port <= r[1]; port++ {
			out[port] = true
		}
	}
	return out
}

func requireOpen(t *testing.T, want, got map[int32]bool, why string) {
	t.Helper()
	var extra, missing []int32
	for port := range got {
		if !want[port] {
			extra = append(extra, port)
		}
	}
	for port := range want {
		if !got[port] {
			missing = append(missing, port)
		}
	}
	slices.Sort(extra)
	slices.Sort(missing)
	if len(extra) > 10 {
		extra = extra[:10]
	}
	if len(missing) > 10 {
		missing = missing[:10]
	}
	require.True(t, len(extra) == 0 && len(missing) == 0, "%s: open that should be closed %v, closed that should be open %v", why, extra, missing)
}

func tcp(port int32) geassv1alpha1.GeassEgressPortRange {
	return geassv1alpha1.GeassEgressPortRange{Port: port}
}

func udp(port int32) geassv1alpha1.GeassEgressPortRange {
	return geassv1alpha1.GeassEgressPortRange{Port: port, Protocol: corev1.ProtocolUDP}
}

func tcpRange(first, last int32) geassv1alpha1.GeassEgressPortRange {
	return geassv1alpha1.GeassEgressPortRange{Port: first, EndPort: utilptr.To(last)}
}

func mustPlan(t *testing.T, egress *geassv1alpha1.GeassTenantEgress) *EgressPlan {
	t.Helper()
	plan, err := PlanEgress(egress)
	require.NoError(t, err)
	require.NotNil(t, plan)
	return plan
}

// miningPoolPorts is the list of mining pools' ports the documentation suggests for blockedPorts.
var miningPoolPorts = []geassv1alpha1.GeassEgressPortRange{
	tcpRange(3333, 3336), tcp(4444), tcp(5555), tcp(6666), tcp(7777), tcp(9999), tcp(13333), tcp(14433), tcp(14444), tcp(15555),
	tcp(17777), tcp(19999), tcp(45700),
}

func TestWithoutLimitsThePoliciesAreExactlyWhatTheyWere(t *testing.T) {
	nodes := &NodeNetwork{Blocked: []string{"10.0.0.5/32"}, APIAddresses: []string{"10.0.0.5/32"}, APIPort: 6443}
	require.Equal(t, CloudMetadataPolicy("shop", "shop-production", nodes), TenantCloudMetadataPolicy("shop", "shop-production", nodes, nil))
	require.Equal(t, OrgBuildPolicy("geass-build-org-1", "org-1", nil), TenantOrgBuildPolicy("geass-build-org-1", "org-1", nil, nil))

	plan, err := PlanEgress(nil)
	require.NoError(t, err)
	require.Nil(t, plan, "no setting is no limit")

	nothingClosed := mustPlan(t, &geassv1alpha1.GeassTenantEgress{Mail: geassv1alpha1.EgressMailOpen})
	require.Equal(t, CloudMetadataPolicy("shop", "shop-production", nodes), TenantCloudMetadataPolicy("shop", "shop-production", nodes, nothingClosed.ForEnvironment(6443)),
		"limits that close nothing leave the policy as it was, so that ping and SCTP are not lost for nothing")
	require.Equal(t, OrgBuildPolicy("geass-build-org-1", "org-1", nil), TenantOrgBuildPolicy("geass-build-org-1", "org-1", nil, nothingClosed))
}

func TestTheDefaultsCloseTheMailPortsAndNothingElse(t *testing.T) {
	plan := mustPlan(t, &geassv1alpha1.GeassTenantEgress{})
	policy := TenantCloudMetadataPolicy("shop", "shop-production", nil, plan.ForEnvironment(6443))

	tcpOf := func(port int32) networkingv1.NetworkPolicyPort {
		protocol := corev1.ProtocolTCP
		value := intstr.FromInt32(port)
		return networkingv1.NetworkPolicyPort{Protocol: &protocol, Port: &value}
	}
	tcpRangeOf := func(first, last int32) networkingv1.NetworkPolicyPort {
		port := tcpOf(first)
		port.EndPort = utilptr.To(last)
		return port
	}
	udpAll := corev1.ProtocolUDP
	require.Equal(t, []networkingv1.NetworkPolicyEgressRule{
		{To: []networkingv1.NetworkPolicyPeer{{NamespaceSelector: policy.Spec.Egress[0].To[0].NamespaceSelector}}},
		{
			To: []networkingv1.NetworkPolicyPeer{{IPBlock: &networkingv1.IPBlock{CIDR: "0.0.0.0/0", Except: []string{"169.254.0.0/16"}}}},
			Ports: []networkingv1.NetworkPolicyPort{
				tcpRangeOf(1, 24), tcpRangeOf(26, 464), tcpRangeOf(466, 586), tcpRangeOf(588, 2464), tcpRangeOf(2466, 2524),
				tcpRangeOf(2526, 2586), tcpRangeOf(2588, 65535), {Protocol: &udpAll},
			},
		},
	}, policy.Spec.Egress, "the pods of the cluster on every port, and the outside on every port but mail's, as the fewest ranges")
	require.Empty(t, policy.Spec.Egress[0].To[0].NamespaceSelector.MatchLabels, "every namespace's pods")
	require.Empty(t, policy.Spec.Egress[0].To[0].NamespaceSelector.MatchExpressions)

	requireOpen(t, allBut(MailPorts()), openPorts(policy, "198.51.100.7", corev1.ProtocolTCP), "TCP to the internet")
	requireOpen(t, allBut(nil), openPorts(policy, "198.51.100.7", corev1.ProtocolUDP), "UDP to the internet")
	requireOpen(t, allBut(MailPorts()), openPorts(policy, "10.20.30.40", corev1.ProtocolTCP), "TCP to a private network, such as an outside database")
	for _, port := range MailPorts() {
		require.True(t, egressAllowed(policy, podIn("mail-relay", port, corev1.ProtocolTCP)), "the cluster's own pods are reached on any port, %d among them", port)
	}
}

func TestBlockedPortsAreClosedBesidesMailAndTheRestIsTheirExactComplement(t *testing.T) {
	plan := mustPlan(t, &geassv1alpha1.GeassTenantEgress{BlockedPorts: append(slices.Clone(miningPoolPorts), udp(3478))})
	for name, policy := range map[string]*networkingv1.NetworkPolicy{
		"environment":     TenantCloudMetadataPolicy("shop", "shop-production", nil, plan.ForEnvironment(6443)),
		"build namespace": TenantOrgBuildPolicy("geass-build-org-1", "org-1", nil, plan),
	} {
		closed := slices.Clone(MailPorts())
		for _, r := range miningPoolPorts {
			last := r.Port
			if r.EndPort != nil {
				last = *r.EndPort
			}
			for port := r.Port; port <= last; port++ {
				closed = append(closed, port)
			}
		}
		requireOpen(t, allBut(closed), openPorts(policy, "198.51.100.7", corev1.ProtocolTCP), name+": TCP")
		requireOpen(t, allBut([]int32{3478}), openPorts(policy, "198.51.100.7", corev1.ProtocolUDP), name+": UDP")
	}
}

func TestOnlyListedPortsOpenAndClosedWinsOverAllowed(t *testing.T) {
	plan := mustPlan(t, &geassv1alpha1.GeassTenantEgress{
		AllowedPorts: []geassv1alpha1.GeassEgressPortRange{tcp(80), tcp(443), udp(443), tcpRange(8000, 8100), tcp(587)},
		BlockedPorts: []geassv1alpha1.GeassEgressPortRange{tcp(8080)},
	})
	build := TenantOrgBuildPolicy("geass-build-org-1", "org-1", nil, plan)
	requireOpen(t, only([]int32{80, 443}, [2]int32{8000, 8079}, [2]int32{8081, 8100}), openPorts(build, "198.51.100.7", corev1.ProtocolTCP),
		"a build: what is listed, less mail (587) and the blocked 8080")
	requireOpen(t, only([]int32{443}), openPorts(build, "198.51.100.7", corev1.ProtocolUDP), "UDP only where listed")

	environment := TenantCloudMetadataPolicy("shop", "shop-production", nil, plan.ForEnvironment(6443))
	requireOpen(t, only([]int32{80, 443, 6443}, [2]int32{8000, 8079}, [2]int32{8081, 8100}), openPorts(environment, "198.51.100.7", corev1.ProtocolTCP),
		"an environment also keeps the cluster's API port, which its Postgres databases use")
	require.True(t, egressAllowed(environment, podIn("kube-system", 53, corev1.ProtocolUDP)), "the cluster's DNS is a pod, and pods are reached on every port")
	require.True(t, egressAllowed(environment, podIn("kube-system", 53, corev1.ProtocolTCP)))
	require.True(t, egressAllowed(build, podIn("kube-system", 53, corev1.ProtocolUDP)), "a build keeps its DNS rule")
	require.True(t, egressAllowed(build, podIn("kube-system", 53, corev1.ProtocolTCP)))
	require.False(t, egressAllowed(build, podIn("shop-production", 80, corev1.ProtocolTCP)), "and is still kept from the cluster's other pods")
}

func TestTheAPIPortStaysOpenFromEnvironmentsEvenWhenItIsBlocked(t *testing.T) {
	plan := mustPlan(t, &geassv1alpha1.GeassTenantEgress{BlockedPorts: []geassv1alpha1.GeassEgressPortRange{tcpRange(6000, 7000)}})
	environment := TenantCloudMetadataPolicy("shop", "shop-production", nil, plan.ForEnvironment(6443))
	requireOpen(t, allBut(MailPorts(), [2]int32{6000, 6442}, [2]int32{6444, 7000}), openPorts(environment, "203.0.113.9", corev1.ProtocolTCP), "the API port is carved out")
	build := TenantOrgBuildPolicy("geass-build-org-1", "org-1", nil, plan)
	requireOpen(t, allBut(MailPorts(), [2]int32{6000, 7000}), openPorts(build, "203.0.113.9", corev1.ProtocolTCP), "a build has no use for the API")

	other := TenantCloudMetadataPolicy("shop", "shop-production", nil, plan.ForEnvironment(0))
	require.True(t, egressAllowed(other, outside("203.0.113.9", DefaultAPIServerPort, corev1.ProtocolTCP)), "no port known is the default one")
}

func TestWhatThePoliciesCloseStaysClosedWhateverTheLimitsAllow(t *testing.T) {
	nodes := &NodeNetwork{
		Blocked:      []string{"10.0.0.5/32", "10.0.0.6/32", "10.42.0.0/32", "10.42.0.1/32"},
		APIAddresses: []string{"10.0.0.5/32"},
		APIPort:      6443,
	}
	plan := mustPlan(t, &geassv1alpha1.GeassTenantEgress{Allow: []geassv1alpha1.GeassEgressAllowance{
		{CIDR: "0.0.0.0/0", Ports: []geassv1alpha1.GeassEgressPortRange{tcp(25), tcp(80)}},
		{CIDR: "10.0.0.0/8", Ports: []geassv1alpha1.GeassEgressPortRange{tcp(587)}},
		{CIDR: "10.0.0.6/32", Ports: []geassv1alpha1.GeassEgressPortRange{tcp(25)}},
	}})
	environment := TenantCloudMetadataPolicy("shop", "shop-production", nodes, plan.ForEnvironment(6443))
	unlimited := CloudMetadataPolicy("shop", "shop-production", nodes)

	for _, to := range []destination{
		outside("169.254.169.254", 80, corev1.ProtocolTCP),
		outside("169.254.169.254", 25, corev1.ProtocolTCP),
		outside("10.0.0.6", 25, corev1.ProtocolTCP),
		outside("10.0.0.6", 22, corev1.ProtocolTCP),
		outside("10.0.0.5", 25, corev1.ProtocolTCP),
		outside("10.42.0.1", 587, corev1.ProtocolTCP),
	} {
		require.False(t, egressAllowed(unlimited, to), "%v is closed before the limits", to)
		require.False(t, egressAllowed(environment, to), "%v is closed with them, whatever an allowance says", to)
	}
	require.True(t, egressAllowed(environment, outside("10.0.0.5", 6443, corev1.ProtocolTCP)), "the API on the control plane stays open")
	require.True(t, egressAllowed(environment, outside("198.51.100.7", 25, corev1.ProtocolTCP)), "the allowance opens mail to the internet")
	require.True(t, egressAllowed(environment, outside("10.9.9.9", 587, corev1.ProtocolTCP)), "and the private one opens 587 to a relay in the private network")
	require.False(t, egressAllowed(environment, outside("198.51.100.7", 587, corev1.ProtocolTCP)), "an allowance opens only its own ports to its own addresses")

	var outsideRule *networkingv1.NetworkPolicyEgressRule
	for i := range environment.Spec.Egress {
		if to := environment.Spec.Egress[i].To; len(to) == 1 && to[0].IPBlock != nil && to[0].IPBlock.CIDR == "0.0.0.0/0" && len(environment.Spec.Egress[i].Ports) > 2 {
			outsideRule = &environment.Spec.Egress[i]
		}
	}
	require.NotNil(t, outsideRule)
	require.Equal(t, unlimited.Spec.Egress[0].To[1].IPBlock.Except, outsideRule.To[0].IPBlock.Except, "the same exception list as before")
	require.Equal(t, unlimited.Spec.Egress[len(unlimited.Spec.Egress)-1], environment.Spec.Egress[len(environment.Spec.Egress)-1], "the API rule is untouched")
	require.Len(t, environment.Spec.Egress, 5, "pods, the outside, two allowances (the one inside a node opens nothing and is left out), the API")

	build := TenantOrgBuildPolicy("geass-build-org-1", "org-1", nil, plan)
	for _, to := range []destination{
		outside("10.9.9.9", 587, corev1.ProtocolTCP),
		outside("192.168.1.1", 25, corev1.ProtocolTCP),
		outside("127.0.0.1", 25, corev1.ProtocolTCP),
		outside("169.254.169.254", 80, corev1.ProtocolTCP),
		outside("100.64.0.1", 80, corev1.ProtocolTCP),
	} {
		require.False(t, egressAllowed(build, to), "a build never reaches a private address: %v", to)
	}
	require.True(t, egressAllowed(build, outside("198.51.100.7", 25, corev1.ProtocolTCP)))
	require.Len(t, build.Spec.Egress, 3, "DNS, the internet and the one allowance that is not wholly private")
}

func TestPortRangesAreTheFewestInOrder(t *testing.T) {
	plan := mustPlan(t, &geassv1alpha1.GeassTenantEgress{
		Mail:         geassv1alpha1.EgressMailOpen,
		BlockedPorts: []geassv1alpha1.GeassEgressPortRange{tcpRange(100, 200), tcpRange(150, 300), tcp(301), tcp(65535), tcp(1)},
	})
	policy := TenantOrgBuildPolicy("geass-build-org-1", "org-1", nil, plan)
	ports := policy.Spec.Egress[1].Ports
	require.Len(t, ports, 3, "overlapping and touching ranges are one: TCP 2-99, 302-65534, and every UDP port")
	require.Equal(t, int32(2), ports[0].Port.IntVal)
	require.Equal(t, int32(99), *ports[0].EndPort)
	require.Equal(t, int32(302), ports[1].Port.IntVal)
	require.Equal(t, int32(65534), *ports[1].EndPort)
	require.Nil(t, ports[2].Port, "a protocol with every port open is one entry that needs no range support")
	require.Equal(t, corev1.ProtocolUDP, *ports[2].Protocol)

	single := mustPlan(t, &geassv1alpha1.GeassTenantEgress{AllowedPorts: []geassv1alpha1.GeassEgressPortRange{tcp(443)}})
	only443 := TenantOrgBuildPolicy("geass-build-org-1", "org-1", nil, single).Spec.Egress[1].Ports
	require.Len(t, only443, 1)
	require.Nil(t, only443[0].EndPort, "a single port has no endPort")
	require.NotSame(t, ports[0].Protocol, ports[1].Protocol, "no two entries share a pointer")

	again := TenantOrgBuildPolicy("geass-build-org-1", "org-1", nil, mustPlan(t, &geassv1alpha1.GeassTenantEgress{
		Mail:         geassv1alpha1.EgressMailOpen,
		BlockedPorts: []geassv1alpha1.GeassEgressPortRange{tcp(1), tcp(65535), tcp(301), tcpRange(150, 300), tcpRange(100, 200)},
	}))
	require.Equal(t, policy, again, "the same limits in another order write the same policy, so it is not written again for nothing")
}

func TestLimitsThatCannotBeAppliedAreRefusedWithEveryReason(t *testing.T) {
	for name, tc := range map[string]struct {
		egress geassv1alpha1.GeassTenantEgress
		want   []string
	}{
		"port zero":    {geassv1alpha1.GeassTenantEgress{BlockedPorts: []geassv1alpha1.GeassEgressPortRange{tcp(0)}}, []string{"blockedPorts[0]: port 0 is not between 1 and 65535"}},
		"port too big": {geassv1alpha1.GeassTenantEgress{AllowedPorts: []geassv1alpha1.GeassEgressPortRange{tcp(70000)}}, []string{"allowedPorts[0]: port 70000"}},
		"range backwards": {geassv1alpha1.GeassTenantEgress{BlockedPorts: []geassv1alpha1.GeassEgressPortRange{tcp(1), tcpRange(4000, 3000)}},
			[]string{"blockedPorts[1]: endPort 3000 is not between port 4000 and 65535"}},
		"range past the end": {geassv1alpha1.GeassTenantEgress{BlockedPorts: []geassv1alpha1.GeassEgressPortRange{tcpRange(4000, 65536)}}, []string{"endPort 65536"}},
		"sctp":               {geassv1alpha1.GeassTenantEgress{BlockedPorts: []geassv1alpha1.GeassEgressPortRange{{Port: 9, Protocol: corev1.ProtocolSCTP}}}, []string{`protocol "SCTP" is not TCP or UDP`}},
		"mail":               {geassv1alpha1.GeassTenantEgress{Mail: "Relayed"}, []string{`mail is "Relayed"`}},
		"empty allow-list": {geassv1alpha1.GeassTenantEgress{AllowedPorts: []geassv1alpha1.GeassEgressPortRange{}},
			[]string{"allowedPorts is an empty list, which would close every port"}},
		"nothing left open": {geassv1alpha1.GeassTenantEgress{AllowedPorts: []geassv1alpha1.GeassEgressPortRange{tcp(25), tcp(587)}},
			[]string{"leave no port open"}},
		"everything blocked": {geassv1alpha1.GeassTenantEgress{BlockedPorts: []geassv1alpha1.GeassEgressPortRange{tcpRange(1, 65535), {Port: 1, EndPort: utilptr.To(int32(65535)), Protocol: corev1.ProtocolUDP}}},
			[]string{"leave no port open"}},
		"not a range": {geassv1alpha1.GeassTenantEgress{Allow: []geassv1alpha1.GeassEgressAllowance{{CIDR: "relay.example.com", Ports: []geassv1alpha1.GeassEgressPortRange{tcp(25)}}}},
			[]string{`allow[0].cidr: "relay.example.com" is not a range of IPv4 addresses`}},
		"IPv6": {geassv1alpha1.GeassTenantEgress{Allow: []geassv1alpha1.GeassEgressAllowance{{CIDR: "2001:db8::/32", Ports: []geassv1alpha1.GeassEgressPortRange{tcp(25)}}}},
			[]string{"is not a range of IPv4 addresses"}},
		"not the first address": {geassv1alpha1.GeassTenantEgress{Allow: []geassv1alpha1.GeassEgressAllowance{{CIDR: "203.0.113.25/24", Ports: []geassv1alpha1.GeassEgressPortRange{tcp(25)}}}},
			[]string{"which is 203.0.113.0/24"}},
		"the metadata service": {geassv1alpha1.GeassTenantEgress{Allow: []geassv1alpha1.GeassEgressAllowance{{CIDR: "169.254.169.254/32", Ports: []geassv1alpha1.GeassEgressPortRange{tcp(80)}}}},
			[]string{"is inside 169.254.0.0/16, which is never opened"}},
		"loopback": {geassv1alpha1.GeassTenantEgress{Allow: []geassv1alpha1.GeassEgressAllowance{{CIDR: "127.0.0.1/32", Ports: []geassv1alpha1.GeassEgressPortRange{tcp(25)}}}},
			[]string{"127.0.0.0/8"}},
		"allowance without ports": {geassv1alpha1.GeassTenantEgress{Allow: []geassv1alpha1.GeassEgressAllowance{{CIDR: "203.0.113.25/32"}}}, []string{"allow[0].ports is empty"}},
		"every problem at once": {geassv1alpha1.GeassTenantEgress{
			BlockedPorts: []geassv1alpha1.GeassEgressPortRange{tcp(0)},
			Allow:        []geassv1alpha1.GeassEgressAllowance{{CIDR: "x", Ports: []geassv1alpha1.GeassEgressPortRange{tcp(0)}}},
		}, []string{"blockedPorts[0]", "allow[0].cidr", "allow[0].ports[0]"}},
	} {
		t.Run(name, func(t *testing.T) {
			plan, err := PlanEgress(&tc.egress)
			require.Error(t, err)
			require.Nil(t, plan)
			for _, want := range tc.want {
				require.Contains(t, err.Error(), want)
			}
		})
	}
}

func TestAnOrganizationReplacesEachFieldItSets(t *testing.T) {
	platformLimits := &geassv1alpha1.GeassTenantEgress{
		BlockedPorts: miningPoolPorts,
		Allow:        []geassv1alpha1.GeassEgressAllowance{{CIDR: "203.0.113.25/32", Ports: []geassv1alpha1.GeassEgressPortRange{tcp(587)}}},
	}
	require.Nil(t, EffectiveTenantEgress(nil, nil))
	require.Equal(t, platformLimits, EffectiveTenantEgress(platformLimits, nil))

	own := &geassv1alpha1.GeassTenantEgress{Mail: geassv1alpha1.EgressMailOpen, BlockedPorts: []geassv1alpha1.GeassEgressPortRange{}}
	merged := EffectiveTenantEgress(platformLimits, own)
	require.Equal(t, geassv1alpha1.EgressMailOpen, merged.Mail, "the platform's approval for this organization's mail")
	require.Empty(t, merged.BlockedPorts, "an empty list replaces the platform's with none")
	require.NotNil(t, merged.BlockedPorts)
	require.Equal(t, platformLimits.Allow, merged.Allow, "what it leaves out is the platform's")
	require.Len(t, platformLimits.BlockedPorts, len(miningPoolPorts), "the platform's own copy is not touched")

	alone := EffectiveTenantEgress(nil, &geassv1alpha1.GeassTenantEgress{})
	require.NotNil(t, alone, "an organization's own limits hold without the platform's")
	require.True(t, alone.MailBlocked(), "setting limits at all closes mail")
	require.False(t, (*geassv1alpha1.GeassTenantEgress)(nil).MailBlocked(), "no limits close nothing")
}

func TestTheSummarySaysWhatIsClosedOrOpen(t *testing.T) {
	require.Equal(t, "TCP 25, 465, 587, 2465, 2525, 2587 closed to addresses outside the cluster", mustPlan(t, &geassv1alpha1.GeassTenantEgress{}).Summary())
	require.Equal(t, "TCP 25, 465, 587, 2465, 2525, 2587, 3333-3336, 4444, 5555, 6666 and 9 more closed to addresses outside the cluster, and opened on some ports to 203.0.113.25/32",
		mustPlan(t, &geassv1alpha1.GeassTenantEgress{BlockedPorts: miningPoolPorts, Allow: []geassv1alpha1.GeassEgressAllowance{
			{CIDR: "203.0.113.25/32", Ports: []geassv1alpha1.GeassEgressPortRange{tcp(587)}},
		}}).Summary())
	listed := mustPlan(t, &geassv1alpha1.GeassTenantEgress{AllowedPorts: []geassv1alpha1.GeassEgressPortRange{tcp(80), tcp(443), udp(443)}})
	require.Equal(t, "only TCP 80, 443 and UDP 443 open to addresses outside the cluster", listed.Summary())
	require.Equal(t, "only TCP 80, 443, 6443 and UDP 443 open to addresses outside the cluster", listed.ForEnvironment(6443).Summary(),
		"an environment says that the API port is open too")
	require.Equal(t, "no port closed to addresses outside the cluster", mustPlan(t, &geassv1alpha1.GeassTenantEgress{Mail: geassv1alpha1.EgressMailOpen}).Summary())
	require.Empty(t, (*EgressPlan)(nil).Summary())
}

func TestALimitedPolicyIsStillOneTheOperatorWatches(t *testing.T) {
	plan := mustPlan(t, &geassv1alpha1.GeassTenantEgress{})
	policy := TenantCloudMetadataPolicy("shop", "shop-production", nil, plan.ForEnvironment(6443))
	require.Contains(t, ManagedNetworkPolicyNames(), policy.Name, "its name is unchanged, so a hand edit is put back and an update replaces nothing")
	require.Equal(t, ManagedByValue, policy.Labels[LabelManagedBy])
	require.Equal(t, "shop", policy.Labels[LabelProject])
	require.Equal(t, []networkingv1.PolicyType{networkingv1.PolicyTypeEgress}, policy.Spec.PolicyTypes, "still egress only: ingress is the isolation policy's")

	build := TenantOrgBuildPolicy("geass-build-org-1", "org-1", nil, plan)
	require.Equal(t, OrgBuildPolicyName, build.Name)
	require.Equal(t, "org-1", build.Labels[LabelOrgBuild])
	require.Equal(t, []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress}, build.Spec.PolicyTypes, "nothing may connect to a build, as before")
	require.Empty(t, build.Spec.Ingress)
}
