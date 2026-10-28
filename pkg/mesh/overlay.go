package mesh

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// The overlay check asks, from one place, whether the pod network of each other node answers. Everything Check reads
// comes from the nodes themselves, and a node is Ready and advertises the right address when its kubelet can reach the
// API server, which it does over a connection that never touches the overlay. So a mesh whose tunnel is closed (UDP
// 51820 blocked, a NAT that does not keep its mapping, tailnet rules that do not allow the pod range) looks healthy
// there. This file decides what to probe and says what an answer, or none, means. It does no network I/O: the operator
// makes the connections (internal/controller/mesh_overlay.go) and hands the results back.
//
// The probe is a TCP connection from the operator's pod to the first address of another node's pod range. On K3s that
// address is the node's cni0 bridge (10.42.3.1 for 10.42.3.0/24), which is the node answering on the pod network. The
// connection leaves the probe's node by the route to that pod range, which is the flannel WireGuard interface in a
// WireGuard mesh and tailscale0 in a Tailscale one, so it follows whatever the mesh put in the routing table. The port
// is one nothing listens on: a refusal is as good an answer as an accepted connection, because either one means a
// packet crossed the overlay and another came back. Only silence is no answer.

const (
	// AnnotationOverlayCheck, set to OverlayCheckOff on a GeassCluster, turns the overlay check off for it. A cluster
	// that sets no mesh never has the check.
	AnnotationOverlayCheck = "geass.dev/mesh-overlay-check"
	// OverlayCheckOff is the value of AnnotationOverlayCheck that turns the check off.
	OverlayCheckOff = "off"

	// OverlayProbePort is the TCP port the probe connects to. Nothing is expected to listen on it, so the node's kernel
	// answers a connection with a refusal and nothing on the node is started, logged or shown a request.
	OverlayProbePort = 9

	// OverlayConfirm is how many probes in a row must get no answer before a node is reported: one lost probe, or one
	// made before the node's bridge exists, is not a finding.
	OverlayConfirm = 2

	// maxOverlayRangeBits is the longest prefix of a pod range that has an address for the bridge besides the network
	// address and the broadcast one.
	maxOverlayRangeBits = 29
)

// ProbeStatus is what came of one probe of a node.
type ProbeStatus int

const (
	// ProbeAnswered is a connection that was accepted or refused: a packet came back from the node's pod network.
	ProbeAnswered ProbeStatus = iota
	// ProbeNoAnswer is a connection that got no reply in time.
	ProbeNoAnswer
	// ProbeNoRoute is a connection that the probe's own machine, or a router on the way, said cannot be made: no
	// route to the pod range, or the host unreachable.
	ProbeNoRoute
	// ProbeNotRun is a probe that could not be made, or was cut short: it says nothing about the node.
	ProbeNotRun
)

// Unreachable is whether the probe is evidence that the node's pod network does not answer.
func (s ProbeStatus) Unreachable() bool { return s == ProbeNoAnswer || s == ProbeNoRoute }

// ProbeResult is the outcome of one probe: what happened, and in a few words, for a message.
type ProbeResult struct {
	Status ProbeStatus
	Detail string
}

// OverlayTarget is one node to probe, and the address to probe it on.
type OverlayTarget struct {
	Node    string
	Address netip.Addr
}

// OverlayPlan is what to probe this time, or why nothing is probed.
type OverlayPlan struct {
	// From is the node the operator's pod is on, which is where every probe is made from.
	From string
	// Targets are the other nodes to probe, in name order.
	Targets []OverlayTarget
	// Skipped are the nodes left out: not Ready, Ready only a moment ago, or with no IPv4 pod range.
	Skipped []string
	// NotRun is why there is nothing to probe, in words for a condition. Empty when there is.
	NotRun string
}

// PlanOverlayProbe chooses the nodes to probe and says which node the probe runs on. local is the addresses of the
// machine the probe runs on, which is how it finds its own node: the node whose pod range holds one of them. A probe
// that runs outside the cluster's pod network (an operator that is not on a node, or on the host network of one) would
// find nothing wrong and prove nothing, so then nothing is probed.
//
// A node is left out until it has been Ready for settle: its bridge is made when its first pod starts, so a node that
// has only just joined may not have the address yet.
func PlanOverlayProbe(nodes []corev1.Node, local []netip.Addr, now time.Time, settle time.Duration) OverlayPlan {
	from, ok := probeNode(nodes, local)
	if !ok {
		return OverlayPlan{NotRun: "the operator has an address in no node's pod range, so it is not on the cluster's pod network " +
			"(it runs outside the cluster, or on a node's host network), and a probe from it would say nothing about the overlay"}
	}
	plan := OverlayPlan{From: from}
	for i := range nodes {
		node := &nodes[i]
		if node.Name == from {
			continue
		}
		address, hasRange := podBridgeAddress(node)
		if !hasRange || !readyFor(node, now, settle) {
			plan.Skipped = append(plan.Skipped, node.Name)
			continue
		}
		plan.Targets = append(plan.Targets, OverlayTarget{Node: node.Name, Address: address})
	}
	slices.SortFunc(plan.Targets, func(a, b OverlayTarget) int { return strings.Compare(a.Node, b.Node) })
	slices.Sort(plan.Skipped)
	if len(plan.Targets) == 0 {
		plan.NotRun = fmt.Sprintf("there is no other node to probe from %s: none is Ready for more than %s with an IPv4 pod range", from, settle)
	}
	return plan
}

// probeNode is the node whose pod range holds one of the addresses of the machine the probe runs on.
func probeNode(nodes []corev1.Node, local []netip.Addr) (string, bool) {
	var found []string
	for i := range nodes {
		for _, prefix := range podRanges(&nodes[i]) {
			if slices.ContainsFunc(local, func(a netip.Addr) bool { return prefix.Contains(a.Unmap()) }) {
				found = append(found, nodes[i].Name)
				break
			}
		}
	}
	if len(found) == 0 {
		return "", false
	}
	return slices.Min(found), true
}

// podRanges are the ranges of addresses a node hands to its pods.
func podRanges(node *corev1.Node) []netip.Prefix {
	cidrs := node.Spec.PodCIDRs
	if len(cidrs) == 0 && node.Spec.PodCIDR != "" {
		cidrs = []string{node.Spec.PodCIDR}
	}
	var out []netip.Prefix
	for _, cidr := range cidrs {
		if prefix, err := netip.ParsePrefix(cidr); err == nil {
			out = append(out, prefix.Masked())
		}
	}
	return out
}

// podBridgeAddress is the address after the network address of the node's first IPv4 pod range: where the node answers
// on the pod network. It returns false for a node with no such range.
func podBridgeAddress(node *corev1.Node) (netip.Addr, bool) {
	for _, prefix := range podRanges(node) {
		if prefix.Addr().Is4() && prefix.Bits() <= maxOverlayRangeBits {
			return prefix.Addr().Next(), true
		}
	}
	return netip.Addr{}, false
}

// readyFor is whether the node is Ready and has been for at least d. A node with no recorded time counts as settled.
func readyFor(node *corev1.Node, now time.Time, d time.Duration) bool {
	for _, condition := range node.Status.Conditions {
		if condition.Type == corev1.NodeReady {
			return condition.Status == corev1.ConditionTrue && now.Sub(condition.LastTransitionTime.Time) >= d
		}
	}
	return false
}

// OverlayFinding is what the probes have found about the nodes of a plan so far.
type OverlayFinding struct {
	Plan OverlayPlan
	// Answered is how many of the plan's nodes answered the last probe.
	Answered int
	// Unreachable are the nodes that did not answer twice in a row, each with what happened ("edge-1 (timed out)").
	Unreachable []string
	// Pending is how many have not been probed yet, or have been probed once without an answer and are being tried
	// again, or could not be probed.
	Pending int
}

// Probed is whether any probe was made: there was a node to probe from and something to probe.
func (f OverlayFinding) Probed() bool { return f.Plan.NotRun == "" }

// ReadyMessage is the MeshReady message of a mesh whose nodes all advertise an address: what that says, and what the
// overlay check added to it. It says what was probed and nothing more.
func (f OverlayFinding) ReadyMessage(provider geassv1alpha1.MeshProvider) string {
	if !f.Probed() {
		return fmt.Sprintf("Every node advertises an address on the %s mesh. This is read from the nodes and does not test that traffic flows "+
			"between sites: check pods on two sites reach each other (see the documentation). The overlay probe did not run: %s", provider, f.Plan.NotRun)
	}
	total := len(f.Plan.Targets)
	message := fmt.Sprintf("Every node advertises an address on the %s mesh. A probe from the operator's pod on node %s got an answer from "+
		"the pod network of %d of %d other nodes", provider, f.Plan.From, f.Answered, total)
	if f.Pending > 0 {
		message += fmt.Sprintf(" (%d not answered yet: not probed yet, or being tried again)", f.Pending)
	}
	message += fmt.Sprintf(". That is one path from one point, a TCP connection to port %d on the first address of each node's pod range: it does not "+
		"test traffic between two other nodes, large packets or a service (see the documentation)", OverlayProbePort)
	if len(f.Plan.Skipped) > 0 {
		message += "; not probed: " + named(f.Plan.Skipped) + " (not Ready, Ready only a moment ago, or no IPv4 pod range)"
	}
	return message
}

// UnreachableMessage is the MeshReady message when nodes do not answer: which, from where, how it was tested, and what
// to check. It says the probe is from one place, and that nothing is held back because of it.
func (f OverlayFinding) UnreachableMessage(provider geassv1alpha1.MeshProvider) string {
	check := "that UDP 51820 is open in both directions between the sites, and that a NAT keeps the tunnel's mapping"
	if provider == geassv1alpha1.MeshProviderTailscale {
		check = "the tailnet's access rules and the approved routes for the pod range (K3s advertises each node's pod range)"
	}
	return fmt.Sprintf("A probe from the operator's pod on node %s got no answer from the pod network of: %s. It makes a TCP connection to port %d "+
		"on the first address of each node's pod range, and nothing came back on %d probes in a row. Nodes show Ready all the same, because a node's "+
		"connection to the API server does not use the pod network. Check %s; and that no firewall on the node drops traffic from the pod range. "+
		"This is one probe from one point, so it can be wrong; confirm between two pods as the documentation's Verify the mesh says. "+
		"Nothing is held back because of it: no join is refused and the cluster's Ready is not changed",
		f.Plan.From, named(f.Unreachable), OverlayProbePort, OverlayConfirm, check)
}
