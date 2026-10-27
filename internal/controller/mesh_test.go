package controller

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/mesh"
	"github.com/degoke/geass/pkg/platform"
	"github.com/degoke/geass/pkg/ssh"
)

const fixtureAuthKey = "fixture-auth-key-0001"

func wireGuardMesh() *geassv1alpha1.GeassClusterMeshSpec {
	return &geassv1alpha1.GeassClusterMeshSpec{Provider: geassv1alpha1.MeshProviderWireGuard}
}

func tailscaleMesh(url string) *geassv1alpha1.GeassClusterMeshSpec {
	return &geassv1alpha1.GeassClusterMeshSpec{
		Provider:  geassv1alpha1.MeshProviderTailscale,
		Tailscale: &geassv1alpha1.GeassMeshTailscale{AuthKeySecretRef: "geass-mesh-auth", ControlServerURL: url},
	}
}

func authKeySecret(data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "geass-mesh-auth", Namespace: platform.SystemNamespace}, Data: data}
}

func goodAuthKeySecret() *corev1.Secret {
	return authKeySecret(map[string][]byte{mesh.AuthKeySecretKey: []byte(fixtureAuthKey + "\n")})
}

func wireGuardControlPlane() *corev1.Node {
	node := controlPlaneNode("cp", true, internalIP("10.0.0.5"), externalIP("203.0.113.10"))
	node.Status.NodeInfo.KubeletVersion = "v1.36.5+k3s1"
	node.Annotations = map[string]string{mesh.AnnotationFlannelBackendType: "wireguard"}
	return node
}

type joinWorld struct {
	cluster     *geassv1alpha1.GeassCluster
	node        *geassv1alpha1.GeassNode
	objects     []client.Object
	provisioner *recordingProvisioner
	reconciler  *GeassNodeReconciler
	client      client.Client
}

// newJoinWorld is a cluster with a worker that is about to be joined, and the stand-in for SSH that records what the
// worker would be told.
func newJoinWorld(t *testing.T, network geassv1alpha1.GeassClusterNetworkSpec, eip string, extra ...client.Object) *joinWorld {
	t.Helper()
	scheme := storageTestScheme(t)
	cluster := clusterWithServerURL("https://203.0.113.10:6443", false)
	cluster.Spec.Network = network
	cluster.Status.Phase = geassv1alpha1.ClusterPhaseReady
	cluster.Spec.TokenSecretRef = corev1.SecretReference{Name: "geass-token", Namespace: platform.SystemNamespace}
	node := &geassv1alpha1.GeassNode{
		ObjectMeta: metav1.ObjectMeta{Name: "edge-1", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassNodeSpec{
			Role: geassv1alpha1.NodeRoleWorker, EIP: eip, SSHUser: "ubuntu",
			SSHKeySecretRef: &corev1.SecretReference{Name: "key", Namespace: platform.SystemNamespace},
		},
	}
	objects := append([]client.Object{
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "key", Namespace: platform.SystemNamespace}, Data: map[string][]byte{"ssh-privatekey": []byte("pem")}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "geass-token", Namespace: platform.SystemNamespace}, Data: map[string][]byte{platform.SecretKeyToken: []byte("tok")}},
		node, cluster,
	}, extra...)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithStatusSubresource(&geassv1alpha1.GeassNode{}, &geassv1alpha1.GeassCluster{}).Build()
	provisioner := &recordingProvisioner{}
	return &joinWorld{
		cluster: cluster, node: node, objects: objects, provisioner: provisioner, client: c,
		reconciler: &GeassNodeReconciler{Client: c, Scheme: scheme, SSH: provisioner},
	}
}

// join runs the reconciliation that joins the worker, and returns the node as it is afterwards.
func (w *joinWorld) join(t *testing.T) *geassv1alpha1.GeassNode {
	t.Helper()
	fresh := &geassv1alpha1.GeassNode{}
	require.NoError(t, w.client.Get(context.Background(), client.ObjectKeyFromObject(w.node), fresh))
	_, err := w.reconciler.reconcileWorker(context.Background(), fresh, w.cluster)
	require.NoError(t, err)
	after := &geassv1alpha1.GeassNode{}
	require.NoError(t, w.client.Get(context.Background(), client.ObjectKeyFromObject(w.node), after))
	return after
}

// A multi-site cluster that does not set the mesh field is joined as it was: the new refusals are for the mesh only.
func TestAMultiSiteClusterWithoutTheMeshFieldIsJoinedAsBefore(t *testing.T) {
	// The control plane runs VXLAN and advertises nothing, and the worker's address is a name: a mesh would refuse both.
	bare := controlPlaneNode("cp", true, internalIP("10.0.0.5"))
	bare.Status.NodeInfo.KubeletVersion = "v1.36.5+k3s1"
	bare.Annotations = map[string]string{mesh.AnnotationFlannelBackendType: "vxlan"}
	world := newJoinWorld(t, geassv1alpha1.GeassClusterNetworkSpec{MultiSite: true}, "worker.example.com", bare)

	after := world.join(t)

	require.Equal(t, geassv1alpha1.NodePhaseJoining, after.Status.Phase, after.Status.Message)
	require.Len(t, world.provisioner.provisioned, 1)
	in := world.provisioner.provisioned[0]
	require.True(t, in.MultiSite)
	require.Nil(t, in.Tailscale)
	require.Equal(t, "worker.example.com", in.Config.NodeExternalIP, "as it always was, a name included")
	require.Equal(t, ssh.NewAgentConfig(ssh.AgentOptions{MultiSite: true, ExternalIP: "worker.example.com"}), in.Config)
}

func TestAClusterWithNoMeshIsJoinedOnItsPrivateNetwork(t *testing.T) {
	cp := controlPlaneNode("cp", true, internalIP("10.0.0.5"))
	cp.Status.NodeInfo.KubeletVersion = "v1.36.5+k3s1"
	world := newJoinWorld(t, geassv1alpha1.GeassClusterNetworkSpec{}, "10.0.0.9", cp)

	after := world.join(t)

	require.Equal(t, geassv1alpha1.NodePhaseJoining, after.Status.Phase, after.Status.Message)
	in := world.provisioner.provisioned[0]
	require.False(t, in.MultiSite)
	require.Nil(t, in.Tailscale)
	require.Empty(t, in.Config.NodeExternalIP)
	require.Empty(t, in.Config.VPNAuthFile)
	require.False(t, in.Config.ExternalIPFromTailnet)
}

func TestAWireGuardMeshJoinsAWorkerOnItsAddress(t *testing.T) {
	world := newJoinWorld(t, geassv1alpha1.GeassClusterNetworkSpec{Mesh: wireGuardMesh()}, "198.51.100.7", wireGuardControlPlane())

	after := world.join(t)

	require.Equal(t, geassv1alpha1.NodePhaseJoining, after.Status.Phase, after.Status.Message)
	in := world.provisioner.provisioned[0]
	require.True(t, in.MultiSite, "WireGuard needs its kernel module and its port")
	require.Nil(t, in.Tailscale)
	require.Equal(t, "198.51.100.7", in.Config.NodeExternalIP)
	require.Equal(t, "https://203.0.113.10:6443", in.ServerURL)
}

// The mesh was added to the cluster object after the control plane was installed without it. Joining workers now would
// register them with public addresses to a control plane that runs VXLAN over private ones, and nobody could reach them.
func TestAMeshTurnedOnAfterTheControlPlaneWasBuiltRefusesWorkers(t *testing.T) {
	cp := controlPlaneNode("cp", true, internalIP("10.0.0.5"))
	cp.Status.NodeInfo.KubeletVersion = "v1.36.5+k3s1"
	cp.Annotations = map[string]string{mesh.AnnotationFlannelBackendType: "vxlan"}
	world := newJoinWorld(t, geassv1alpha1.GeassClusterNetworkSpec{Mesh: wireGuardMesh()}, "198.51.100.7", cp)

	after := world.join(t)

	require.Equal(t, geassv1alpha1.NodePhaseFailed, after.Status.Phase)
	require.Contains(t, after.Status.Message, "the mesh cannot take a worker")
	require.Contains(t, after.Status.Message, "cannot be switched on afterwards")
	require.Empty(t, world.provisioner.provisioned, "nothing was done to the machine")
}

func TestAWireGuardMeshRefusesAWorkerWhoseAddressIsNotAnAddress(t *testing.T) {
	for address, want := range map[string]string{
		"worker.example.com": "not an IP address",
		"127.0.0.1":          "cannot be reached from another machine",
		"":                   "no address",
	} {
		t.Run(address, func(t *testing.T) {
			world := newJoinWorld(t, geassv1alpha1.GeassClusterNetworkSpec{Mesh: wireGuardMesh()}, address, wireGuardControlPlane())

			after := world.join(t)

			require.Equal(t, geassv1alpha1.NodePhaseFailed, after.Status.Phase)
			require.Contains(t, after.Status.Message, want)
			require.Empty(t, world.provisioner.provisioned)
		})
	}
}

func TestAMeshThatCannotBeUsedRefusesWorkersAndSaysWhy(t *testing.T) {
	world := newJoinWorld(t, geassv1alpha1.GeassClusterNetworkSpec{Mesh: &geassv1alpha1.GeassClusterMeshSpec{Provider: geassv1alpha1.MeshProviderTailscale}}, "198.51.100.7", wireGuardControlPlane())

	after := world.join(t)

	require.Equal(t, geassv1alpha1.NodePhaseFailed, after.Status.Phase)
	require.Contains(t, after.Status.Message, "provider Tailscale needs spec.network.mesh.tailscale")
	require.Empty(t, world.provisioner.provisioned)
}

func tailnetControlPlane() *corev1.Node {
	node := controlPlaneNode("cp", true, internalIP("10.0.0.5"), externalIP("100.64.0.7"))
	node.Status.NodeInfo.KubeletVersion = "v1.36.5+k3s1"
	return node
}

func tailnetWorld(t *testing.T, url string, extra ...client.Object) *joinWorld {
	t.Helper()
	world := newJoinWorld(t, geassv1alpha1.GeassClusterNetworkSpec{Mesh: tailscaleMesh(url)}, "198.51.100.7", extra...)
	world.cluster.Spec.ServerURL = "https://100.64.0.7:6443"
	require.NoError(t, world.client.Update(context.Background(), world.cluster))
	return world
}

func TestATailscaleMeshJoinsAWorkerOntoTheTailnetWithTheKeyFromTheSecret(t *testing.T) {
	world := tailnetWorld(t, "https://headscale.example.com", tailnetControlPlane(), goodAuthKeySecret())

	after := world.join(t)

	require.Equal(t, geassv1alpha1.NodePhaseJoining, after.Status.Phase, after.Status.Message)
	require.Len(t, world.provisioner.provisioned, 1)
	in := world.provisioner.provisioned[0]
	require.Equal(t, &ssh.TailscaleJoin{AuthKey: fixtureAuthKey, ControlServerURL: "https://headscale.example.com"}, in.Tailscale,
		"the newline at the end of the Secret's value is not part of the key")
	require.False(t, in.MultiSite, "nothing needs the WireGuard module or its port")
	require.Equal(t, "/etc/rancher/k3s/vpn-auth", in.Config.VPNAuthFile)
	require.True(t, in.Config.ExternalIPFromTailnet)
	require.Empty(t, in.Config.NodeExternalIP, "the node advertises its tailnet address, not the one Geass reaches it on")
	require.Equal(t, "https://100.64.0.7:6443", in.ServerURL)
	require.NotContains(t, in.Config.YAML(), fixtureAuthKey)
}

func TestATailscaleMeshRefusesAWorkerWhileItsKeyIsMissingOrWrong(t *testing.T) {
	bad := "has a space, and a comma"
	for name, tc := range map[string]struct {
		secret *corev1.Secret
		want   string
	}{
		"no Secret":       {nil, "does not exist"},
		"no key in it":    {authKeySecret(map[string][]byte{"other": []byte("x")}), `has no "authKey" key`},
		"an empty key":    {authKeySecret(map[string][]byte{mesh.AuthKeySecretKey: []byte("  \n")}), `has no "authKey" key`},
		"an unusable key": {authKeySecret(map[string][]byte{mesh.AuthKeySecretKey: []byte(bad)}), "cannot be used"},
	} {
		t.Run(name, func(t *testing.T) {
			objects := []client.Object{tailnetControlPlane()}
			if tc.secret != nil {
				objects = append(objects, tc.secret)
			}
			world := tailnetWorld(t, "", objects...)

			after := world.join(t)

			require.Equal(t, geassv1alpha1.NodePhaseFailed, after.Status.Phase)
			require.Contains(t, after.Status.Message, tc.want)
			require.NotContains(t, after.Status.Message, bad, "a key that is refused may be a real one")
			require.Empty(t, world.provisioner.provisioned)
		})
	}
}

func TestATailscaleMeshNeedsAControlPlaneThatAdvertisesATailnetAddress(t *testing.T) {
	bare := controlPlaneNode("cp", true, internalIP("10.0.0.5"))
	bare.Status.NodeInfo.KubeletVersion = "v1.36.5+k3s1"
	world := tailnetWorld(t, "", bare, goodAuthKeySecret())

	after := world.join(t)

	require.Equal(t, geassv1alpha1.NodePhaseFailed, after.Status.Phase)
	require.Contains(t, after.Status.Message, "advertises no external address")
}

func TestAnUpgradeOfATailnetWorkerCarriesTheSameKey(t *testing.T) {
	world := tailnetWorld(t, "", tailnetControlPlane(), goodAuthKeySecret())
	creds := sshCredentials{privateKey: []byte("pem")}

	in, err := workerJoinInput(context.Background(), world.client, nil, world.node, world.cluster, creds, "tok")

	require.NoError(t, err)
	require.Equal(t, fixtureAuthKey, in.Tailscale.AuthKey)
	require.True(t, in.Config.ExternalIPFromTailnet, "the configuration is written the same way when K3s is replaced")
}

func TestARefusedJoinKeepsTheOldMessageForOtherFailures(t *testing.T) {
	require.Equal(t, "could not determine the cluster's k3s version: boom", joinInputFailure(errors.New("boom")),
		"a failure that is not a refusal says what it always said")
	require.Equal(t, "the reason", joinInputFailure(&joinRefusedError{"the reason"}))
}

// Cloud machines: the firewall Geass makes, and the rule it adds to the control plane's, follow the mesh.
func TestOnlyAWireGuardMeshOpensTheOverlayPort(t *testing.T) {
	for name, tc := range map[string]struct {
		network geassv1alpha1.GeassClusterNetworkSpec
		udp     bool
	}{
		"no mesh":              {geassv1alpha1.GeassClusterNetworkSpec{}, false},
		"multi-site as before": {geassv1alpha1.GeassClusterNetworkSpec{MultiSite: true}, true},
		"a WireGuard mesh":     {geassv1alpha1.GeassClusterNetworkSpec{Mesh: wireGuardMesh()}, true},
		"a Tailscale mesh":     {geassv1alpha1.GeassClusterNetworkSpec{Mesh: tailscaleMesh("")}, false},
	} {
		t.Run(name, func(t *testing.T) {
			rules := controlPlaneRules(&geassv1alpha1.GeassCluster{Spec: geassv1alpha1.GeassClusterSpec{Network: tc.network}}, "198.51.100.7/32")

			require.Equal(t, "tcp", rules[0].Protocol)
			require.EqualValues(t, 6443, rules[0].Port)
			if tc.udp {
				require.Len(t, rules, 2)
				require.Equal(t, "udp", rules[1].Protocol)
			} else {
				require.Len(t, rules, 1, "a tailnet opens no port on the control plane")
			}
		})
	}
}

// Prometheus reaches each kubelet on its external address in either mesh; the values of a cluster that is not in one
// are what they were.
func TestMonitoringFollowsTheMeshAndTheValuesOfOtherClustersAreWhatTheyWere(t *testing.T) {
	raw, err := os.ReadFile("testdata/legacy_monitoring_values.golden.json")
	require.NoError(t, err)
	var golden map[string]string
	require.NoError(t, json.Unmarshal(raw, &golden))

	for _, profile := range []string{"", "full"} {
		single, err := monitoringChartValues(monitoringCluster(false, profile))
		require.NoError(t, err)
		require.Equal(t, golden["single-site-values-profile-"+profile], single)
		multi, err := monitoringChartValues(monitoringCluster(true, profile))
		require.NoError(t, err)
		require.Equal(t, golden["multi-site-values-profile-"+profile], multi)

		for name, spec := range map[string]*geassv1alpha1.GeassClusterMeshSpec{"WireGuard": wireGuardMesh(), "Tailscale": tailscaleMesh("")} {
			cluster := monitoringCluster(false, profile)
			cluster.Spec.Network.Mesh = spec
			values, err := monitoringChartValues(cluster)
			require.NoError(t, err)
			require.Equal(t, golden["multi-site-values-profile-"+profile], values, "a %s mesh scrapes kubelets as a multi-site cluster does", name)
		}
	}
}

func TestAMeshJoinsWorkersOnTheControlPlanesExternalAddress(t *testing.T) {
	for name, spec := range map[string]*geassv1alpha1.GeassClusterMeshSpec{"WireGuard": wireGuardMesh(), "Tailscale": tailscaleMesh("")} {
		cluster := clusterWithServerURL("https://127.0.0.1:6443", false)
		cluster.Spec.Network.Mesh = spec
		got := joinURL(t, cluster, controlPlaneNode("cp", true, internalIP("10.0.0.5"), externalIP("203.0.113.10")))
		require.Equal(t, "https://203.0.113.10:6443", got, name)
	}
}

// The condition.

func meshCondition(t *testing.T, c client.Client, cluster *geassv1alpha1.GeassCluster) *metav1.Condition {
	t.Helper()
	latest := &geassv1alpha1.GeassCluster{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(cluster), latest))
	return meta.FindStatusCondition(latest.Status.Conditions, mesh.ConditionMeshReady)
}

func conditionWorld(t *testing.T, network geassv1alpha1.GeassClusterNetworkSpec, serverURL string, objects ...client.Object) (*GeassClusterReconciler, *geassv1alpha1.GeassCluster, client.Client) {
	t.Helper()
	scheme := storageTestScheme(t)
	cluster := clusterWithServerURL(serverURL, false)
	cluster.Spec.Network = network
	all := append([]client.Object{cluster}, objects...)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(all...).WithStatusSubresource(&geassv1alpha1.GeassCluster{}).Build()
	return &GeassClusterReconciler{Client: c, Scheme: scheme}, cluster, c
}

func TestAClusterWithoutAMeshGetsNoMeshCondition(t *testing.T) {
	for name, network := range map[string]geassv1alpha1.GeassClusterNetworkSpec{"none": {}, "multi-site only": {MultiSite: true}} {
		t.Run(name, func(t *testing.T) {
			r, cluster, c := conditionWorld(t, network, "https://203.0.113.10:6443", controlPlaneNode("cp", true, internalIP("10.0.0.5")))

			require.Zero(t, r.reconcileMesh(context.Background(), cluster))

			require.Nil(t, meshCondition(t, c, cluster), "nothing is added for a cluster that does not use the mesh field")
		})
	}
}

func TestAMeshWhereEveryNodeAdvertisesAnAddressIsReady(t *testing.T) {
	worker := readyK8sNode("w1", true, time.Now())
	worker.Status.Addresses = []corev1.NodeAddress{externalIP("198.51.100.7")}
	r, cluster, c := conditionWorld(t, geassv1alpha1.GeassClusterNetworkSpec{Mesh: wireGuardMesh()}, "https://203.0.113.10:6443", wireGuardControlPlane(), worker)

	require.Equal(t, meshRecheck, r.reconcileMesh(context.Background(), cluster))

	condition := meshCondition(t, c, cluster)
	require.NotNil(t, condition)
	require.Equal(t, metav1.ConditionTrue, condition.Status)
	require.Equal(t, meshReasonReady, condition.Reason)
	require.Contains(t, condition.Message, "does not test that traffic flows")
}

func TestAMeshWithANodeThatIsNotOnItSaysSo(t *testing.T) {
	handJoined := readyK8sNode("hand-joined", true, time.Now())
	handJoined.Status.Addresses = []corev1.NodeAddress{internalIP("10.5.0.5")}
	r, cluster, c := conditionWorld(t, geassv1alpha1.GeassClusterNetworkSpec{Mesh: wireGuardMesh()}, "https://203.0.113.10:6443", wireGuardControlPlane(), handJoined)

	require.Equal(t, meshRetry, r.reconcileMesh(context.Background(), cluster), "looked at again soon while something is wrong")

	condition := meshCondition(t, c, cluster)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, meshReasonAttended, condition.Reason)
	require.Contains(t, condition.Message, "hand-joined")
}

func TestAMeshWhoseControlPlaneIsNotOnItIsBlocked(t *testing.T) {
	cp := controlPlaneNode("cp", true, internalIP("10.0.0.5"))
	cp.Annotations = map[string]string{mesh.AnnotationFlannelBackendType: "vxlan"}
	r, cluster, c := conditionWorld(t, geassv1alpha1.GeassClusterNetworkSpec{Mesh: wireGuardMesh()}, "https://10.0.0.5:6443", cp)

	r.reconcileMesh(context.Background(), cluster)

	condition := meshCondition(t, c, cluster)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, meshReasonBlocked, condition.Reason)
	require.Contains(t, condition.Message, "cannot be switched on afterwards")
	require.Contains(t, condition.Message, "private address", "the server URL is one too")
}

func TestATailscaleMeshWithoutItsKeyIsReportedWithoutTheKey(t *testing.T) {
	r, cluster, c := conditionWorld(t, geassv1alpha1.GeassClusterNetworkSpec{Mesh: tailscaleMesh("")}, "https://100.64.0.7:6443", tailnetControlPlane())

	r.reconcileMesh(context.Background(), cluster)

	condition := meshCondition(t, c, cluster)
	require.Equal(t, metav1.ConditionFalse, condition.Status)
	require.Equal(t, meshReasonAuthKey, condition.Reason)
	require.Contains(t, condition.Message, "does not exist")
}

func TestATailscaleMeshWithItsKeyIsReady(t *testing.T) {
	r, cluster, c := conditionWorld(t, geassv1alpha1.GeassClusterNetworkSpec{Mesh: tailscaleMesh("")}, "https://100.64.0.7:6443", tailnetControlPlane(), goodAuthKeySecret())

	r.reconcileMesh(context.Background(), cluster)

	condition := meshCondition(t, c, cluster)
	require.Equal(t, metav1.ConditionTrue, condition.Status, condition.Message)
	require.NotContains(t, condition.Message, fixtureAuthKey)
}

func TestTheMeshConditionGoesWhenTheMeshDoes(t *testing.T) {
	r, cluster, c := conditionWorld(t, geassv1alpha1.GeassClusterNetworkSpec{Mesh: wireGuardMesh()}, "https://203.0.113.10:6443", wireGuardControlPlane())
	r.reconcileMesh(context.Background(), cluster)
	require.NotNil(t, meshCondition(t, c, cluster))

	latest := &geassv1alpha1.GeassCluster{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(cluster), latest))
	latest.Spec.Network.Mesh = nil
	require.NoError(t, c.Update(context.Background(), latest))
	require.NoError(t, c.Get(context.Background(), client.ObjectKeyFromObject(cluster), latest))

	require.Zero(t, r.reconcileMesh(context.Background(), latest))
	require.Nil(t, meshCondition(t, c, cluster))
}

func TestAMeshSpecThatCannotBeUsedIsReported(t *testing.T) {
	r, cluster, c := conditionWorld(t, geassv1alpha1.GeassClusterNetworkSpec{Mesh: &geassv1alpha1.GeassClusterMeshSpec{Provider: "ZeroTier"}}, "https://203.0.113.10:6443")

	r.reconcileMesh(context.Background(), cluster)

	condition := meshCondition(t, c, cluster)
	require.Equal(t, meshReasonInvalid, condition.Reason)
	require.Contains(t, condition.Message, "ZeroTier")
}
