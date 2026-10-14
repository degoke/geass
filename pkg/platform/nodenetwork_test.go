package platform

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func testNode(name string, labels map[string]string, podCIDRs []string, addresses ...corev1.NodeAddress) corev1.Node {
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec:       corev1.NodeSpec{PodCIDRs: podCIDRs},
		Status:     corev1.NodeStatus{Addresses: addresses},
	}
}

func internalIP(ip string) corev1.NodeAddress {
	return corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: ip}
}

func TestEveryNodeAddressAndItsPlaceOnThePodNetworkIsBlocked(t *testing.T) {
	network := NodeNetworkOf([]corev1.Node{
		testNode("cp", map[string]string{labelNodeControlPlane: "true"}, []string{"10.42.0.0/24"},
			internalIP("10.0.0.5"), corev1.NodeAddress{Type: corev1.NodeExternalIP, Address: "203.0.113.7"}, corev1.NodeAddress{Type: corev1.NodeHostName, Address: "cp"}),
		testNode("worker", nil, []string{"10.42.1.0/24"}, internalIP("10.0.0.6")),
	}, 0)

	require.Equal(t, []string{
		"10.0.0.5/32", "10.0.0.6/32", "10.42.0.0/32", "10.42.0.1/32", "10.42.1.0/32", "10.42.1.1/32", "203.0.113.7/32",
	}, network.Blocked, "both of a node's addresses, and the two addresses of its pod range that no pod can hold: the overlay's and the bridge every pod leaves by")
	require.Equal(t, []string{"10.0.0.5/32", "203.0.113.7/32"}, network.APIAddresses, "only the control plane serves the API")
	require.Equal(t, DefaultAPIServerPort, network.APIPort)
}

func TestTheOldPodCIDRFieldIsReadWhenThereIsNoList(t *testing.T) {
	node := testNode("cp", nil, nil, internalIP("10.0.0.5"))
	node.Spec.PodCIDR = "10.42.0.0/24"

	require.Equal(t, []string{"10.0.0.5/32", "10.42.0.0/32", "10.42.0.1/32"}, NodeNetworkOf([]corev1.Node{node}, 6443).Blocked)
}

func TestAClusterWithNoControlPlaneLabelKeepsTheAPIOpenWhereverItCouldBe(t *testing.T) {
	network := NodeNetworkOf([]corev1.Node{
		testNode("a", nil, nil, internalIP("10.0.0.5")),
		testNode("b", nil, nil, internalIP("10.0.0.6")),
	}, 8443)

	require.Equal(t, []string{"10.0.0.5/32", "10.0.0.6/32"}, network.APIAddresses,
		"better open on every address, as it was before, than closed on all of them and the Postgres instances cut off")
	require.Equal(t, int32(8443), network.APIPort)
}

func TestTheOldMasterLabelStillMarksAControlPlane(t *testing.T) {
	network := NodeNetworkOf([]corev1.Node{
		testNode("old", map[string]string{labelNodeMaster: ""}, nil, internalIP("10.0.0.5")),
		testNode("worker", nil, nil, internalIP("10.0.0.6")),
	}, 0)

	require.Equal(t, []string{"10.0.0.5/32"}, network.APIAddresses)
}

func TestAddressesThatCannotOrNeedNotBeBlockedAreLeftOut(t *testing.T) {
	network := NodeNetworkOf([]corev1.Node{testNode("n", nil, []string{"fd00::/64", "10.42.0.0/31", "garbage"},
		internalIP("10.0.0.5"), internalIP("10.0.0.5"), internalIP("fd00::5"), internalIP("::ffff:10.0.0.7"),
		internalIP("127.0.0.1"), internalIP("0.0.0.0"), internalIP("169.254.1.1"), internalIP("not an address"))}, 0)

	require.Equal(t, []string{"10.0.0.5/32", "10.0.0.7/32"}, network.Blocked,
		"IPv6 cannot be listed in an IPv4 range, loopback and link-local are not worth listing (link-local is blocked already), a range too small to hold a pod and a gateway is skipped, and repeats collapse")
}

func TestNodeNetworkIsTheSameWhateverOrderTheNodesComeIn(t *testing.T) {
	a := testNode("a", nil, []string{"10.42.0.0/24"}, internalIP("10.0.0.5"))
	b := testNode("b", nil, []string{"10.42.1.0/24"}, internalIP("10.0.0.6"))

	require.True(t, NodeNetworkOf([]corev1.Node{a, b}, 0).Equal(NodeNetworkOf([]corev1.Node{b, a}, 0)),
		"a policy rewritten for a different order would be a write for nothing")
	require.False(t, NodeNetworkOf([]corev1.Node{a}, 0).Equal(NodeNetworkOf([]corev1.Node{a, b}, 0)))
	require.False(t, NodeNetworkOf([]corev1.Node{a}, 0).Equal(NodeNetworkOf([]corev1.Node{a}, 8443)))
	var none *NodeNetwork
	require.True(t, none.Equal(nil))
	require.False(t, none.Equal(NodeNetworkOf(nil, 0)))
}
