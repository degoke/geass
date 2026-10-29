package mesh

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

var overlayNow = time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

// overlayNode is a node with a pod range, Ready since the given time (the zero time means no time was recorded).
func overlayNode(name string, cidrs []string, ready corev1.ConditionStatus, since time.Time) corev1.Node {
	node := corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if len(cidrs) > 0 {
		node.Spec.PodCIDR = cidrs[0]
		node.Spec.PodCIDRs = cidrs
	}
	node.Status.Conditions = []corev1.NodeCondition{{Type: corev1.NodeReady, Status: ready, LastTransitionTime: metav1.NewTime(since)}}
	return node
}

func longReady(name, cidr string) corev1.Node {
	return overlayNode(name, []string{cidr}, corev1.ConditionTrue, overlayNow.Add(-time.Hour))
}

func addrs(values ...string) []netip.Addr {
	out := make([]netip.Addr, 0, len(values))
	for _, value := range values {
		out = append(out, netip.MustParseAddr(value))
	}
	return out
}

func targetNames(plan OverlayPlan) []string {
	names := make([]string, 0, len(plan.Targets))
	for _, target := range plan.Targets {
		names = append(names, target.Node)
	}
	return names
}

func TestTheProbeRunsFromTheNodeTheOperatorIsOnAndTargetsTheBridgeOfEveryOther(t *testing.T) {
	nodes := []corev1.Node{longReady("w2", "10.42.2.0/24"), longReady("cp", "10.42.0.0/24"), longReady("w1", "10.42.1.0/24")}

	plan := PlanOverlayProbe(nodes, addrs("127.0.0.1", "10.42.0.15"), overlayNow, 2*time.Minute)

	require.Empty(t, plan.NotRun)
	require.Equal(t, "cp", plan.From, "the node whose pod range holds the operator's address")
	require.Equal(t, []OverlayTarget{
		{Node: "w1", Address: netip.MustParseAddr("10.42.1.1")},
		{Node: "w2", Address: netip.MustParseAddr("10.42.2.1")},
	}, plan.Targets, "the first address after the network address of each other node's range, in name order")
}

func TestThePodRangeAddressIsTheBridgeWhateverTheRangeIsWrittenAs(t *testing.T) {
	for cidr, want := range map[string]string{
		"10.42.3.0/24":  "10.42.3.1",
		"10.42.3.77/24": "10.42.3.1", // not masked
		"10.42.8.0/29":  "10.42.8.1",
		"10.244.0.0/16": "10.244.0.1",
	} {
		node := longReady("w", cidr)
		address, ok := podBridgeAddress(&node)
		require.True(t, ok, cidr)
		require.Equal(t, want, address.String(), cidr)
	}
	for _, cidr := range []string{"10.42.3.0/30", "10.42.3.4/32", "fd00:42::/64", "not a range", "",
		"127.0.0.0/24", "0.0.0.0/24", "169.254.0.0/24", "224.0.0.0/24"} {
		node := longReady("w", cidr)
		_, ok := podBridgeAddress(&node)
		require.False(t, ok, "%q has no bridge address that is neither the network nor the broadcast one, is not IPv4, or is an address nothing is probed on", cidr)
	}
}

func TestADualStackNodeIsProbedOnItsIPv4Range(t *testing.T) {
	cp := longReady("cp", "10.42.0.0/24")
	worker := overlayNode("w1", []string{"fd00:42:1::/64", "10.42.1.0/24"}, corev1.ConditionTrue, overlayNow.Add(-time.Hour))

	plan := PlanOverlayProbe([]corev1.Node{cp, worker}, addrs("10.42.0.9"), overlayNow, time.Minute)

	require.Equal(t, []OverlayTarget{{Node: "w1", Address: netip.MustParseAddr("10.42.1.1")}}, plan.Targets)
}

func TestNodesThatCannotBeProbedYetAreLeftOutAndNamed(t *testing.T) {
	settle := 2 * time.Minute
	nodes := []corev1.Node{
		longReady("cp", "10.42.0.0/24"),
		longReady("ok", "10.42.1.0/24"),
		overlayNode("down", []string{"10.42.2.0/24"}, corev1.ConditionFalse, overlayNow.Add(-time.Hour)),
		overlayNode("new", []string{"10.42.3.0/24"}, corev1.ConditionTrue, overlayNow.Add(-settle+time.Second)),
		overlayNode("v6only", []string{"fd00:42:5::/64"}, corev1.ConditionTrue, overlayNow.Add(-time.Hour)),
		overlayNode("norange", nil, corev1.ConditionTrue, overlayNow.Add(-time.Hour)),
		{ObjectMeta: metav1.ObjectMeta{Name: "nocondition"}, Spec: corev1.NodeSpec{PodCIDR: "10.42.7.0/24"}},
		overlayNode("settled", []string{"10.42.4.0/24"}, corev1.ConditionTrue, overlayNow.Add(-settle)),
		overlayNode("untimed", []string{"10.42.6.0/24"}, corev1.ConditionTrue, time.Time{}),
	}

	plan := PlanOverlayProbe(nodes, addrs("10.42.0.9"), overlayNow, settle)

	require.Equal(t, []string{"ok", "settled", "untimed"}, targetNames(plan))
	require.Equal(t, []SkippedNode{
		{"down", SkipNotReady}, {"new", SkipSettling}, {"nocondition", SkipNotReady}, {"norange", SkipNoRange}, {"v6only", SkipNoRange},
	}, plan.Skipped, "each with why: a node that is not Ready is said to be, whatever else is true of it")
	require.Empty(t, plan.NotRun)
}

// The first probe is made when the node has been Ready for the settle time, which nothing tells the operator: the plan says
// when that is, so that it can look again then.
func TestThePlanSaysWhenTheFirstNodeThatIsStillSettlingWillHaveSettled(t *testing.T) {
	settle := 2 * time.Minute
	nodes := []corev1.Node{
		longReady("cp", "10.42.0.0/24"),
		longReady("old", "10.42.1.0/24"),
		overlayNode("later", []string{"10.42.2.0/24"}, corev1.ConditionTrue, overlayNow.Add(-30*time.Second)),
		overlayNode("sooner", []string{"10.42.3.0/24"}, corev1.ConditionTrue, overlayNow.Add(-90*time.Second)),
		overlayNode("down", []string{"10.42.4.0/24"}, corev1.ConditionFalse, overlayNow.Add(-time.Second)),
	}

	plan := PlanOverlayProbe(nodes, addrs("10.42.0.9"), overlayNow, settle)

	require.Equal(t, overlayNow.Add(30*time.Second), plan.SettledAt, "the earliest of the nodes that were left out for being Ready only a short while")
	require.Equal(t, []string{"old"}, targetNames(plan))

	settled := PlanOverlayProbe(nodes[:2], addrs("10.42.0.9"), overlayNow, settle)
	require.True(t, settled.SettledAt.IsZero(), "nothing is waiting to settle: a node that is not Ready is not waited for, nothing says when it will be")
}

// The EC2 run: a worker with no pod on the pod network has no bridge address, and the probe of it was silent with a tunnel
// that was up.
func TestANodeWithNoPodOnThePodNetworkIsLeftOutAndSaidTo(t *testing.T) {
	nodes := []corev1.Node{longReady("cp", "10.42.0.0/24"), longReady("empty", "10.42.1.0/24"), longReady("busy", "10.42.2.0/24")}
	plan := PlanOverlayProbe(nodes, addrs("10.42.0.9"), overlayNow, 2*time.Minute)
	require.Equal(t, []string{"busy", "empty"}, targetNames(plan), "the plan alone does not know what runs where")

	plan = plan.WithPodNetwork(map[string]bool{"busy": true})

	require.Equal(t, []string{"busy"}, targetNames(plan))
	require.Equal(t, []SkippedNode{{"empty", SkipNoPod}}, plan.Skipped)
	require.True(t, plan.Has(SkipNoPod))
	require.False(t, plan.Has(SkipNotReady))
	require.Empty(t, plan.NotRun)
	message := OverlayFinding{Plan: plan, Answered: 1}.ReadyMessage(geassv1alpha1.MeshProviderWireGuard)
	require.Contains(t, message, "1 of 1 other nodes")
	require.Contains(t, message, "not probed: empty (no pod is running on the pod network yet, so there is no bridge address to probe; it is probed once one runs)")
}

func TestWhenNoNodeRunsAPodThereIsNothingToProbeAndTheMessageSaysSo(t *testing.T) {
	nodes := []corev1.Node{longReady("cp", "10.42.0.0/24"), longReady("empty-1", "10.42.1.0/24"), longReady("empty-2", "10.42.2.0/24")}

	plan := PlanOverlayProbe(nodes, addrs("10.42.0.9"), overlayNow, 2*time.Minute).WithPodNetwork(nil)

	require.Empty(t, plan.Targets)
	require.Contains(t, plan.NotRun, "there is no other node to probe from cp")
	require.Contains(t, plan.NotRun, "empty-1, empty-2 (no pod is running on the pod network yet")
	require.False(t, OverlayFinding{Plan: plan}.Probed())

	// A plan that never had a probe point is left as it was: it has no targets to take anything from.
	outside := PlanOverlayProbe(nodes, addrs("192.0.2.9"), overlayNow, time.Minute)
	require.Equal(t, outside, outside.WithPodNetwork(map[string]bool{}))
}

func TestOnlyARunningPodThatIsNotOnTheHostNetworkMakesTheBridge(t *testing.T) {
	pod := func(node string, phase corev1.PodPhase, hostNetwork bool) corev1.Pod {
		return corev1.Pod{Spec: corev1.PodSpec{NodeName: node, HostNetwork: hostNetwork}, Status: corev1.PodStatus{Phase: phase}}
	}

	nodes := PodNetworkNodes([]corev1.Pod{
		pod("running", corev1.PodRunning, false),
		pod("host-network-only", corev1.PodRunning, true),
		pod("pending", corev1.PodPending, false),
		pod("failed", corev1.PodFailed, false),
		pod("unknown", corev1.PodUnknown, false),
		pod("", corev1.PodRunning, false),
		pod("mixed", corev1.PodRunning, true),
		pod("mixed", corev1.PodRunning, false),
	})

	require.Equal(t, map[string]bool{"running": true, "mixed": true}, nodes)
}

// A stopped tailscaled turned a node NotReady on EC2, and the message did not say a node was not Ready.
func TestTheMessageOfAClusterWhoseOnlyOtherNodeIsNotReadySaysSo(t *testing.T) {
	nodes := []corev1.Node{
		longReady("cp", "10.42.0.0/24"),
		overlayNode("pub-t", []string{"10.42.1.0/24"}, corev1.ConditionFalse, overlayNow.Add(-time.Hour)),
		overlayNode("unknown", []string{"10.42.2.0/24"}, corev1.ConditionUnknown, overlayNow.Add(-time.Hour)),
		overlayNode("new", []string{"10.42.3.0/24"}, corev1.ConditionTrue, overlayNow.Add(-time.Second)),
	}

	plan := PlanOverlayProbe(nodes, addrs("10.42.0.9"), overlayNow, 2*time.Minute)

	require.Equal(t, "there is no other node to probe from cp. Left out: pub-t, unknown (not Ready); new (Ready for less than 2m0s)", plan.NotRun)
	message := OverlayFinding{Plan: plan}.ReadyMessage(geassv1alpha1.MeshProviderTailscale)
	require.Contains(t, message, "The overlay probe did not run: there is no other node to probe from cp. Left out: pub-t, unknown (not Ready)")
}

func TestNothingIsProbedFromAPointThatIsNotOnThePodNetwork(t *testing.T) {
	nodes := []corev1.Node{longReady("cp", "10.42.0.0/24"), longReady("w1", "10.42.1.0/24")}

	for name, local := range map[string][]netip.Addr{
		"outside the cluster":            addrs("192.0.2.9", "127.0.0.1"),
		"on a host network":              addrs("10.0.0.5"), // the node's own address is in no pod range
		"no addresses at all":            nil,
		"an address of the wrong family": addrs("fd00::1"),
	} {
		plan := PlanOverlayProbe(nodes, local, overlayNow, time.Minute)

		require.Empty(t, plan.Targets, name)
		require.Contains(t, plan.NotRun, "not on the cluster's pod network", name)
	}
}

func TestNothingIsProbedWhenTheOperatorsNodeIsAlone(t *testing.T) {
	alone := PlanOverlayProbe([]corev1.Node{longReady("cp", "10.42.0.0/24")}, addrs("10.42.0.9"), overlayNow, 2*time.Minute)
	require.Empty(t, alone.Targets)

	allNew := PlanOverlayProbe([]corev1.Node{longReady("cp", "10.42.0.0/24"), overlayNode("w1", []string{"10.42.1.0/24"}, corev1.ConditionTrue, overlayNow)},
		addrs("10.42.0.9"), overlayNow, 2*time.Minute)
	require.Empty(t, allNew.Targets)
	require.Equal(t, []SkippedNode{{"w1", SkipSettling}}, allNew.Skipped)
	require.Equal(t, "there is no other node to probe from cp. Left out: w1 (Ready for less than 2m0s)", allNew.NotRun)
	require.Equal(t, overlayNow.Add(2*time.Minute), allNew.SettledAt)
	require.Equal(t, "there is no other node to probe from cp: it is the only node of the cluster", alone.NotRun)
}

func TestAnIPv4MappedLocalAddressFindsItsNode(t *testing.T) {
	plan := PlanOverlayProbe([]corev1.Node{longReady("cp", "10.42.0.0/24"), longReady("w1", "10.42.1.0/24")},
		[]netip.Addr{netip.MustParseAddr("::ffff:10.42.0.9")}, overlayNow, time.Minute)

	require.Equal(t, "cp", plan.From)
}

func TestOnlySilenceIsUnreachable(t *testing.T) {
	require.False(t, ProbeAnswered.Unreachable())
	require.False(t, ProbeNotRun.Unreachable(), "a probe that could not be made says nothing about the node")
	require.True(t, ProbeNoAnswer.Unreachable())
	require.True(t, ProbeNoRoute.Unreachable())
}

func probedPlan() OverlayPlan {
	return OverlayPlan{From: "cp", Targets: []OverlayTarget{{Node: "w1"}, {Node: "w2"}}}
}

func TestTheReadyMessageSaysWhatWasProbedAndNothingMore(t *testing.T) {
	finding := OverlayFinding{Plan: probedPlan(), Answered: 2}

	message := finding.ReadyMessage(geassv1alpha1.MeshProviderWireGuard)

	require.Contains(t, message, "Every node advertises an address on the WireGuard mesh")
	require.Contains(t, message, "A probe from the operator's pod on node cp got an answer from the pod network of 2 of 2 other nodes")
	require.Contains(t, message, "one path from one point")
	require.Contains(t, message, "port 9")
	require.Contains(t, message, "does not test traffic between two other nodes, large packets or a service")
	require.NotContains(t, message, "not answered yet")
	require.NotContains(t, message, "not probed")
}

func TestTheReadyMessageSaysWhichNodesWereNotAnsweredOrLeftOut(t *testing.T) {
	plan := probedPlan()
	plan.Skipped = []SkippedNode{{"new-1", SkipSettling}, {"down-1", SkipNotReady}}
	plan.Settle = 2 * time.Minute
	finding := OverlayFinding{Plan: plan, Answered: 1, Pending: 1}

	message := finding.ReadyMessage(geassv1alpha1.MeshProviderTailscale)

	require.Contains(t, message, "on the Tailscale mesh")
	require.Contains(t, message, "1 of 2 other nodes (1 not answered yet")
	require.Contains(t, message, "not probed: down-1 (not Ready); new-1 (Ready for less than 2m0s)")
}

// With no probe the message is the one the condition had before the check existed, with the reason added.
func TestTheReadyMessageOfAProbeThatDidNotRunKeepsTheOldWordsAndSaysWhy(t *testing.T) {
	finding := OverlayFinding{Plan: OverlayPlan{NotRun: "it is turned off"}}

	message := finding.ReadyMessage(geassv1alpha1.MeshProviderWireGuard)

	require.Equal(t, "Every node advertises an address on the WireGuard mesh. This is read from the nodes and does not test that traffic flows "+
		"between sites: check pods on two sites reach each other (see the documentation). The overlay probe did not run: it is turned off", message)
	require.False(t, finding.Probed())
}

func TestTheUnreachableMessageSaysWhereFromWhatWasTestedAndWhatToCheck(t *testing.T) {
	finding := OverlayFinding{Plan: probedPlan(), Unreachable: []string{"w2 (timed out)", "w1 (no route to host)"}}

	wireGuard := finding.UnreachableMessage(geassv1alpha1.MeshProviderWireGuard)

	require.Contains(t, wireGuard, "A probe from the operator's pod on node cp got no answer from the pod network of: w1 (no route to host), w2 (timed out)")
	require.Contains(t, wireGuard, "TCP connection to port 9 on the first address of each node's pod range")
	require.Contains(t, wireGuard, "2 probes in a row")
	require.Contains(t, wireGuard, "UDP 51820 is open in both directions")
	require.Contains(t, wireGuard, "a NAT keeps the tunnel's mapping")
	require.Contains(t, wireGuard, "no firewall on the node drops traffic from the pod range to the node itself")
	require.Contains(t, wireGuard, "ufw allow from 10.42.0.0/16", "the remedy that made a node answer on EC2, with the range K3s uses unless told another")
	require.Contains(t, wireGuard, "one probe from one point, so it can be wrong")
	require.Contains(t, wireGuard, "Nothing is held back because of it")
	require.NotContains(t, wireGuard, "tailnet")

	tailscale := finding.UnreachableMessage(geassv1alpha1.MeshProviderTailscale)
	require.Contains(t, tailscale, "tailnet's access rules and the approved routes")
	require.NotContains(t, tailscale, "UDP 51820", "a tailnet opens no port of the cluster's")
	require.Contains(t, tailscale, "ufw allow from 10.42.0.0/16", "a firewall on the node is the same whichever mesh it is")
}

func TestTheUnreachableMessageCutsALongList(t *testing.T) {
	names := make([]string, 0, 8)
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		names = append(names, name+" (timed out)")
	}

	message := OverlayFinding{Plan: probedPlan(), Unreachable: names}.UnreachableMessage(geassv1alpha1.MeshProviderWireGuard)

	require.Contains(t, message, "a (timed out), b (timed out), c (timed out), d (timed out), e (timed out) and 3 more")
	require.False(t, strings.Contains(message, "h (timed out)"))
}
