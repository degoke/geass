package platform

import (
	"net/netip"
	"slices"

	corev1 "k8s.io/api/core/v1"
)

// DefaultAPIServerPort is the port the Kubernetes API listens on when the cluster does not say otherwise,
// which is what K3s and kubeadm both use.
const DefaultAPIServerPort int32 = 6443

const (
	labelNodeControlPlane = "node-role.kubernetes.io/control-plane"
	// labelNodeMaster is what clusters older than Kubernetes 1.24 call a control plane node.
	labelNodeMaster = "node-role.kubernetes.io/master"
)

// NodeNetwork is what the egress policy of a project that blocks the cluster's machines needs to know about
// them. It is built from the nodes themselves, so it is only as current as the list it was built from, and
// has to be built again whenever a node joins, leaves or changes an address.
type NodeNetwork struct {
	// Blocked are the addresses a pod must not open connections to, as /32 ranges, sorted and without
	// repeats. They are each node's own addresses, and the two lowest addresses of its pod range, which is
	// where the node itself sits on the pod network: the pod's default gateway, and the overlay's address.
	// Only IPv4 is listed, because the policy only allows IPv4 to leave the cluster.
	Blocked []string
	// APIAddresses are the addresses of the control plane nodes. The API port stays open on them.
	APIAddresses []string
	// APIPort is the port the Kubernetes API listens on at those addresses.
	APIPort int32
}

// NodeNetworkOf works out what to block from the cluster's nodes. apiPort is the port the API listens on at
// the control plane's own address, or zero for the default.
//
// Control plane nodes are told apart by their role label. A cluster whose nodes carry none leaves the API
// port open on every node's address instead, which opens no more than it was before the block, while a
// policy that left it open on none would cut off the workloads that need the API.
func NodeNetworkOf(nodes []corev1.Node, apiPort int32) *NodeNetwork {
	if apiPort <= 0 {
		apiPort = DefaultAPIServerPort
	}
	var blocked, hosts, controlPlane []netip.Addr
	for i := range nodes {
		node := &nodes[i]
		own := nodeHostAddresses(node)
		hosts = append(hosts, own...)
		blocked = append(blocked, own...)
		blocked = append(blocked, nodePodNetworkAddresses(node)...)
		if IsControlPlaneNode(node) {
			controlPlane = append(controlPlane, own...)
		}
	}
	if len(controlPlane) == 0 {
		controlPlane = hosts
	}
	return &NodeNetwork{Blocked: hostRanges(blocked), APIAddresses: hostRanges(controlPlane), APIPort: apiPort}
}

// IsControlPlaneNode reports whether a node carries a control plane role label.
func IsControlPlaneNode(node *corev1.Node) bool {
	_, controlPlane := node.Labels[labelNodeControlPlane]
	_, master := node.Labels[labelNodeMaster]
	return controlPlane || master
}

// nodeHostAddresses are the addresses a node says it can be reached on.
func nodeHostAddresses(node *corev1.Node) []netip.Addr {
	var out []netip.Addr
	for _, address := range node.Status.Addresses {
		if address.Type != corev1.NodeInternalIP && address.Type != corev1.NodeExternalIP {
			continue
		}
		if addr, ok := blockableAddress(address.Address); ok {
			out = append(out, addr)
		}
	}
	return out
}

// nodePodNetworkAddresses are the two lowest addresses of each IPv4 range a node hands to its pods. Pods never
// get them: the lowest is the overlay interface's own and the next is the bridge every pod's traffic leaves
// through, which is the node answering on the pod network.
func nodePodNetworkAddresses(node *corev1.Node) []netip.Addr {
	cidrs := node.Spec.PodCIDRs
	if len(cidrs) == 0 && node.Spec.PodCIDR != "" {
		cidrs = []string{node.Spec.PodCIDR}
	}
	var out []netip.Addr
	for _, cidr := range cidrs {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil || !prefix.Addr().Is4() || prefix.Bits() > 29 {
			continue
		}
		first := prefix.Masked().Addr()
		out = append(out, first, first.Next())
	}
	return out
}

// blockableAddress parses an address and says whether it is one a policy can list: an IPv4 address that is not
// the unspecified, loopback or link-local one. The link-local range is blocked on its own already.
func blockableAddress(value string) (netip.Addr, bool) {
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Addr{}, false
	}
	addr = addr.Unmap()
	if !addr.Is4() || addr.IsUnspecified() || addr.IsLoopback() || addr.IsLinkLocalUnicast() {
		return netip.Addr{}, false
	}
	return addr, true
}

// hostRanges turns addresses into sorted /32 ranges without repeats.
func hostRanges(addrs []netip.Addr) []string {
	slices.SortFunc(addrs, netip.Addr.Compare)
	addrs = slices.Compact(addrs)
	out := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		out = append(out, netip.PrefixFrom(addr, addr.BitLen()).String())
	}
	return out
}

// Equal reports whether two descriptions would produce the same policy.
func (n *NodeNetwork) Equal(other *NodeNetwork) bool {
	if n == nil || other == nil {
		return n == other
	}
	return n.APIPort == other.APIPort && slices.Equal(n.Blocked, other.Blocked) && slices.Equal(n.APIAddresses, other.APIAddresses)
}
