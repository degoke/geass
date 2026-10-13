package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
	"github.com/degoke/geass/pkg/ssh"
)

func controlPlaneNode(name string, ready bool, addresses ...corev1.NodeAddress) *corev1.Node {
	node := readyK8sNode(name, ready, time.Now())
	node.Labels = map[string]string{controlPlaneLabel: "true"}
	node.Status.Addresses = addresses
	return node
}

func internalIP(ip string) corev1.NodeAddress {
	return corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: ip}
}

func externalIP(ip string) corev1.NodeAddress {
	return corev1.NodeAddress{Type: corev1.NodeExternalIP, Address: ip}
}

func clusterWithServerURL(serverURL string, multiSite bool) *geassv1alpha1.GeassCluster {
	return &geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassClusterSpec{
			Version: "v1", ServerURL: serverURL,
			Network: geassv1alpha1.GeassClusterNetworkSpec{MultiSite: multiSite},
		},
	}
}

func joinURL(t *testing.T, cluster *geassv1alpha1.GeassCluster, nodes ...client.Object) string {
	t.Helper()
	got, err := workerServerURL(context.Background(), versionClient(t, nodes...), cluster)
	require.NoError(t, err)
	return got
}

// A cluster installed before the installer was fixed holds the control plane's own loopback address, which
// no worker can use. It must not need patching by hand.
func TestAWorkerJoinsAClusterWithALoopbackURLOnTheControlPlanesAddress(t *testing.T) {
	cluster := clusterWithServerURL("https://127.0.0.1:6443", false)

	got := joinURL(t, cluster, controlPlaneNode("cp", true, internalIP("10.0.0.5"), externalIP("203.0.113.10")))

	require.Equal(t, "https://10.0.0.5:6443", got, "the private address, which is the network the nodes share")
	require.Equal(t, "https://127.0.0.1:6443", cluster.Spec.ServerURL, "the cluster object itself is not rewritten")
}

func TestAMultiSiteClusterJoinsOnTheControlPlanesPublicAddress(t *testing.T) {
	cluster := clusterWithServerURL("https://127.0.0.1:6443", true)

	got := joinURL(t, cluster, controlPlaneNode("cp", true, internalIP("10.0.0.5"), externalIP("203.0.113.10")))

	require.Equal(t, "https://203.0.113.10:6443", got, "its workers are at other providers and cannot reach a private address")
}

func TestAnAddressThatCanBeUsedIsNeverReplaced(t *testing.T) {
	for _, serverURL := range []string{"https://10.0.0.5:6443", "https://203.0.113.10:6443", "https://cp.example.com:6443"} {
		cluster := clusterWithServerURL(serverURL, false)
		require.Equal(t, serverURL, joinURL(t, cluster, controlPlaneNode("cp", true, internalIP("10.9.9.9"))),
			"an address an operator chose on purpose is theirs to keep")
	}
}

func TestALoopbackURLWithNoControlPlaneAddressIsLeftForTheNodesOwnCheckToExplain(t *testing.T) {
	cluster := clusterWithServerURL("https://127.0.0.1:6443", false)

	require.Equal(t, "https://127.0.0.1:6443", joinURL(t, cluster))
	require.Equal(t, "https://127.0.0.1:6443", joinURL(t, cluster, controlPlaneNode("cp", true)))
	require.Equal(t, "https://127.0.0.1:6443", joinURL(t, cluster, &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "worker"}}),
		"a worker's address is not the control plane's")
}

func TestAControlPlaneThatIsUpIsNamedBeforeOneThatIsDown(t *testing.T) {
	cluster := clusterWithServerURL("https://127.0.0.1:6443", false)

	got := joinURL(t, cluster,
		controlPlaneNode("a-down", false, internalIP("10.0.0.1")),
		controlPlaneNode("c-up", true, internalIP("10.0.0.3")),
		controlPlaneNode("b-up", true, internalIP("10.0.0.2")),
	)

	require.Equal(t, "https://10.0.0.2:6443", got, "ready first, then by name, so the answer does not change between reconciles")
}

func TestAnIPv6ControlPlaneAddressGetsBrackets(t *testing.T) {
	cluster := clusterWithServerURL("https://localhost:6443", false)

	require.Equal(t, "https://[2001:db8::5]:6443", joinURL(t, cluster, controlPlaneNode("cp", true, internalIP("2001:db8::5"))))
}

// End to end through provisioning: what reaches the SSH provisioner is the address a worker can use.
func TestProvisioningPassesTheResolvedAddressAndTheHostKeysToTheProvisioner(t *testing.T) {
	scheme := storageTestScheme(t)
	node := &geassv1alpha1.GeassNode{
		ObjectMeta: metav1.ObjectMeta{Name: "w1", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassNodeSpec{
			Role: geassv1alpha1.NodeRoleWorker, EIP: "203.0.113.9", SSHUser: "ubuntu", SSHPort: 2222,
			SSHKeySecretRef: &corev1.SecretReference{Name: "key", Namespace: platform.SystemNamespace},
		},
	}
	cluster := clusterWithServerURL("https://127.0.0.1:6443", false)
	cluster.Status.Phase = geassv1alpha1.ClusterPhaseReady
	cpNode := controlPlaneNode("cp", true, internalIP("10.0.0.5"))
	cpNode.Status.NodeInfo.KubeletVersion = "v1.36.5+k3s1"
	sshSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "key", Namespace: platform.SystemNamespace},
		Data:       map[string][]byte{"ssh-privatekey": []byte("pem"), ssh.KnownHostsSecretKey: []byte("203.0.113.9 ssh-ed25519 AAAA\n")},
	}
	tokenSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "geass-token", Namespace: platform.SystemNamespace},
		Data:       map[string][]byte{platform.SecretKeyToken: []byte("tok")},
	}
	cluster.Spec.TokenSecretRef = corev1.SecretReference{Name: "geass-token", Namespace: platform.SystemNamespace}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sshSecret, tokenSecret, node, cluster, cpNode).
		WithStatusSubresource(&geassv1alpha1.GeassNode{}).Build()
	provisioner := &recordingProvisioner{}
	r := &GeassNodeReconciler{Client: c, Scheme: scheme, SSH: provisioner}
	fresh := &geassv1alpha1.GeassNode{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(node), fresh))

	_, err := r.reconcileWorker(context.Background(), fresh, cluster)

	require.NoError(t, err)
	require.Len(t, provisioner.provisioned, 1)
	in := provisioner.provisioned[0]
	require.Equal(t, "https://10.0.0.5:6443", in.ServerURL)
	require.Equal(t, 2222, in.Port)
	require.Equal(t, []byte("203.0.113.9 ssh-ed25519 AAAA\n"), in.KnownHosts)
	require.Equal(t, []byte("pem"), in.PrivateKey)
}
