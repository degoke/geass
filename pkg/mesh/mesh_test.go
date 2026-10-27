package mesh

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

func tailscaleMesh(url string) *geassv1alpha1.GeassClusterMeshSpec {
	return &geassv1alpha1.GeassClusterMeshSpec{
		Provider:  geassv1alpha1.MeshProviderTailscale,
		Tailscale: &geassv1alpha1.GeassMeshTailscale{AuthKeySecretRef: "geass-mesh-auth", ControlServerURL: url},
	}
}

func TestAClusterThatSetsNothingHasNoMesh(t *testing.T) {
	require.Equal(t, Settings{}, From(geassv1alpha1.GeassClusterNetworkSpec{}))
	require.False(t, From(geassv1alpha1.GeassClusterNetworkSpec{}).UsesOverlayPort())
}

// A cluster that was multi-site before the mesh field existed behaves as it did: WireGuard, and none of the new checks.
func TestMultiSiteAloneIsTheWireGuardMeshWithoutTheChecks(t *testing.T) {
	settings := From(geassv1alpha1.GeassClusterNetworkSpec{MultiSite: true})

	require.True(t, settings.Enabled)
	require.Equal(t, geassv1alpha1.MeshProviderWireGuard, settings.Provider)
	require.False(t, settings.Checked, "only a cluster that sets mesh is checked, so a running multi-site cluster is not refused anything new")
	require.True(t, settings.UsesOverlayPort())
}

func TestAMeshImpliesMultiSiteAndDefaultsToWireGuard(t *testing.T) {
	settings := From(geassv1alpha1.GeassClusterNetworkSpec{Mesh: &geassv1alpha1.GeassClusterMeshSpec{}})

	require.True(t, settings.Enabled, "a mesh is for nodes at different sites, so multiSite is not also needed")
	require.Equal(t, geassv1alpha1.MeshProviderWireGuard, settings.Provider)
	require.True(t, settings.Checked)
	require.Empty(t, settings.Invalid)
}

func TestATailscaleMeshOpensNoOverlayPort(t *testing.T) {
	settings := From(geassv1alpha1.GeassClusterNetworkSpec{Mesh: tailscaleMesh("")})

	require.True(t, settings.IsTailscale())
	require.False(t, settings.UsesOverlayPort(), "Tailscale carries the traffic: nothing opens 51820 for the cluster")
	require.Empty(t, settings.Invalid)
}

func TestMeshSettingsThatCannotBeUsedSayWhy(t *testing.T) {
	for name, tc := range map[string]struct {
		spec *geassv1alpha1.GeassClusterMeshSpec
		want string
	}{
		"unknown provider":         {&geassv1alpha1.GeassClusterMeshSpec{Provider: "ZeroTier"}, "is not one of WireGuard, Tailscale"},
		"tailscale without keys":   {&geassv1alpha1.GeassClusterMeshSpec{Provider: geassv1alpha1.MeshProviderTailscale}, "needs spec.network.mesh.tailscale"},
		"tailscale on a wireguard": {&geassv1alpha1.GeassClusterMeshSpec{Tailscale: &geassv1alpha1.GeassMeshTailscale{AuthKeySecretRef: "k"}}, "is for provider Tailscale"},
		"empty secret name": {&geassv1alpha1.GeassClusterMeshSpec{
			Provider: geassv1alpha1.MeshProviderTailscale, Tailscale: &geassv1alpha1.GeassMeshTailscale{AuthKeySecretRef: " "}}, "must name the Secret"},
		"plain http control server":   {tailscaleMesh("http://headscale.example.com"), "https address"},
		"control server with a user":  {tailscaleMesh("https://user@headscale.example.com"), "plain address"},
		"control server with a comma": {tailscaleMesh("https://headscale.example.com/a,extraArgs=x"), "plain address"},
		"control server with a quote": {tailscaleMesh("https://headscale.example.com/a'b"), "plain address"},
	} {
		t.Run(name, func(t *testing.T) {
			err := Validate(tc.spec)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)

			settings := From(geassv1alpha1.GeassClusterNetworkSpec{Mesh: tc.spec})
			require.Contains(t, settings.Invalid, tc.want, "the operator reads the reason from the settings")
		})
	}
	require.NoError(t, Validate(nil))
	require.NoError(t, Validate(tailscaleMesh("")))
	require.NoError(t, Validate(tailscaleMesh("https://headscale.example.com:8443/path")))
}

func TestAuthKeysAreCheckedWithoutBeingRepeated(t *testing.T) {
	require.NoError(t, ValidateAuthKey("fixture-auth-key-0001"))
	require.NoError(t, ValidateAuthKey("0123456789abcdef0123456789abcdef"))
	for _, bad := range []string{"", "short", "has space in it", "comma,separated,value", "quote'd-value-here", "new\nline-value-here", strings.Repeat("a", 257)} {
		err := ValidateAuthKey(bad)
		require.Error(t, err, bad)
		if bad != "" {
			require.NotContains(t, err.Error(), bad, "a refused key may be a real one, so it is not in the message")
		}
	}
}

// What K3s's documentation shows for the two modes, and what Geass wrote for multi-site before this change. The
// WireGuard settings are a pin: a cluster that does not use the mesh field must get exactly these.
func TestServerSettings(t *testing.T) {
	var wireguard map[string]any
	raw, err := yaml.Marshal(WireGuardServerSettings("203.0.113.1"))
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(raw, &wireguard))
	require.Equal(t, map[string]any{
		"flannel-backend":     "wireguard-native",
		"node-external-ip":    "203.0.113.1",
		"flannel-external-ip": true,
		"tls-san":             []any{"203.0.113.1"},
	}, wireguard)

	raw, err = yaml.Marshal(TailscaleServerSettings("100.64.0.7"))
	require.NoError(t, err)
	var tailscale map[string]any
	require.NoError(t, yaml.Unmarshal(raw, &tailscale))
	require.Equal(t, map[string]any{
		"vpn-auth-file":    "/etc/rancher/k3s/vpn-auth",
		"node-external-ip": "100.64.0.7",
		"tls-san":          []any{"100.64.0.7"},
	}, tailscale, "K3s's documentation gives vpn-auth and node-external-ip, and no flannel setting")
}

func TestTheAuthFileHoldsWhatK3sReads(t *testing.T) {
	require.Equal(t, "name=tailscale,joinKey=fixture-auth-key-0001", VPNAuth("fixture-auth-key-0001", ""))
	require.Equal(t, "name=tailscale,joinKey=fixture-auth-key-0001,controlServerURL=https://headscale.example.com",
		VPNAuth("fixture-auth-key-0001", "https://headscale.example.com"))
}

func TestKind(t *testing.T) {
	for address, want := range map[string]AddressKind{
		"203.0.113.10":      AddressPublic,
		"198.51.100.7":      AddressPublic,
		"2001:db8::1":       AddressPublic,
		"10.0.0.5":          AddressPrivate,
		"172.16.4.4":        AddressPrivate,
		"192.168.1.20":      AddressPrivate,
		"fd00::1":           AddressPrivate,
		"100.64.0.7":        AddressShared,
		"100.127.255.254":   AddressShared,
		"100.128.0.1":       AddressPublic,
		"fd7a:115c:a1e0::1": AddressTailnet,
		"127.0.0.1":         AddressUnusable,
		"::1":               AddressUnusable,
		"0.0.0.0":           AddressUnusable,
		"169.254.10.10":     AddressUnusable,
		"224.0.0.1":         AddressUnusable,
		"::ffff:10.0.0.5":   AddressPrivate,
		"  203.0.113.10 ":   AddressPublic,
	} {
		got, ok := Kind(address)
		require.True(t, ok, address)
		require.Equal(t, want, got, address)
	}
	_, ok := Kind("node.example.com")
	require.False(t, ok, "a name is not an address")
	_, ok = Kind("")
	require.False(t, ok)
}

func TestCheckServerURL(t *testing.T) {
	wireguard := From(geassv1alpha1.GeassClusterNetworkSpec{Mesh: &geassv1alpha1.GeassClusterMeshSpec{}})
	tailscale := From(geassv1alpha1.GeassClusterNetworkSpec{Mesh: tailscaleMesh("")})

	require.Empty(t, CheckServerURL(Settings{}, "https://10.0.0.5:6443"), "no mesh, nothing to say")
	require.Empty(t, CheckServerURL(wireguard, "https://203.0.113.10:6443"))
	require.Empty(t, CheckServerURL(wireguard, "https://cp.example.com:6443"), "a name may resolve to anything")
	require.Empty(t, CheckServerURL(wireguard, "https://127.0.0.1:6443"), "the operator joins on the control plane's own address instead of a loopback one")
	require.Empty(t, CheckServerURL(tailscale, "https://100.64.0.7:6443"), "this is what a Tailscale mesh's address looks like")
	require.Empty(t, CheckServerURL(tailscale, "https://[fd7a:115c:a1e0::7]:6443"))

	private := CheckServerURL(wireguard, "https://10.0.0.5:6443")
	require.Contains(t, private, "private address")
	require.Contains(t, private, "public address", "it says what to set instead")
	require.Contains(t, CheckServerURL(tailscale, "https://10.0.0.5:6443"), "tailnet address")
	require.Contains(t, CheckServerURL(wireguard, "https://100.64.0.7:6443"), "100.64.0.0/10")
}

func TestCheckAdvertisedAddress(t *testing.T) {
	wireguard := From(geassv1alpha1.GeassClusterNetworkSpec{Mesh: &geassv1alpha1.GeassClusterMeshSpec{}})
	legacy := From(geassv1alpha1.GeassClusterNetworkSpec{MultiSite: true})
	tailscale := From(geassv1alpha1.GeassClusterNetworkSpec{Mesh: tailscaleMesh("")})

	require.Empty(t, CheckAdvertisedAddress(wireguard, "203.0.113.20"))
	require.Empty(t, CheckAdvertisedAddress(wireguard, "10.1.2.3"), "a node behind a NAT may be reached on its private address by the nodes of its own site")
	require.Contains(t, CheckAdvertisedAddress(wireguard, "worker.example.com"), "not an IP address")
	require.Contains(t, CheckAdvertisedAddress(wireguard, "127.0.0.1"), "cannot be reached")
	require.Contains(t, CheckAdvertisedAddress(wireguard, ""), "no address")
	require.Empty(t, CheckAdvertisedAddress(legacy, "worker.example.com"), "a multi-site cluster without the mesh field is joined as it always was")
	require.Empty(t, CheckAdvertisedAddress(tailscale, "worker.example.com"), "a Tailscale node finds its own address")
}

func node(name string, annotations map[string]string, addresses ...corev1.NodeAddress) corev1.Node {
	return corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name, Annotations: annotations}, Status: corev1.NodeStatus{Addresses: addresses}}
}

func external(ip string) corev1.NodeAddress {
	return corev1.NodeAddress{Type: corev1.NodeExternalIP, Address: ip}
}

func internal(ip string) corev1.NodeAddress {
	return corev1.NodeAddress{Type: corev1.NodeInternalIP, Address: ip}
}

func TestAMeshWhereEveryNodeAdvertisesAnAddressHasNothingToReport(t *testing.T) {
	wireguard := From(geassv1alpha1.GeassClusterNetworkSpec{Mesh: &geassv1alpha1.GeassClusterMeshSpec{}})
	control := node("cp", map[string]string{AnnotationFlannelBackendType: "wireguard"}, internal("10.0.0.5"), external("203.0.113.10"))
	worker := node("w1", nil, internal("10.1.0.5"), external("198.51.100.7"))

	report := Check(wireguard, "https://203.0.113.10:6443", []corev1.Node{control}, []corev1.Node{control, worker})

	require.True(t, report.OK(), report.Message())
}

func TestAClusterWithoutAMeshHasNothingToReport(t *testing.T) {
	bare := node("cp", map[string]string{AnnotationFlannelBackendType: "vxlan"}, internal("10.0.0.5"))
	require.True(t, Check(Settings{}, "https://10.0.0.5:6443", []corev1.Node{bare}, []corev1.Node{bare}).OK())
}

// The mesh was switched on in the cluster object after the control plane was installed without it: workers joined now
// would advertise public addresses to a control plane that runs VXLAN over private ones.
func TestAControlPlaneInstalledWithoutTheMeshBlocksJoins(t *testing.T) {
	wireguard := From(geassv1alpha1.GeassClusterNetworkSpec{Mesh: &geassv1alpha1.GeassClusterMeshSpec{}})
	control := node("cp", map[string]string{AnnotationFlannelBackendType: "vxlan"}, internal("10.0.0.5"))

	report := Check(wireguard, "https://10.0.0.5:6443", []corev1.Node{control}, []corev1.Node{control})

	require.Len(t, report.Blocking, 2, "it runs VXLAN and advertises no external address")
	require.Contains(t, report.Blocking[0], "cannot be switched on afterwards")
	require.Contains(t, report.Blocking[1], "advertises no external address")
}

func TestAControlPlaneWithoutAnExternalAddressBlocksJoinsInEitherProvider(t *testing.T) {
	control := node("cp", nil, internal("10.0.0.5"))
	for name, settings := range map[string]Settings{
		"wireguard": From(geassv1alpha1.GeassClusterNetworkSpec{Mesh: &geassv1alpha1.GeassClusterMeshSpec{}}),
		"tailscale": From(geassv1alpha1.GeassClusterNetworkSpec{Mesh: tailscaleMesh("")}),
	} {
		t.Run(name, func(t *testing.T) {
			report := Check(settings, "https://100.64.0.7:6443", []corev1.Node{control}, []corev1.Node{control})
			require.Len(t, report.Blocking, 1)
			require.Contains(t, report.Blocking[0], "Reinstall the control plane with GEASS_MESH set")
		})
	}
}

func TestOneControlPlaneOfSeveralOnTheMeshIsEnough(t *testing.T) {
	wireguard := From(geassv1alpha1.GeassClusterNetworkSpec{Mesh: &geassv1alpha1.GeassClusterMeshSpec{}})
	on := node("cp-a", map[string]string{AnnotationFlannelBackendType: "wireguard"}, external("203.0.113.10"))
	bare := node("cp-b", map[string]string{AnnotationFlannelBackendType: "wireguard"}, internal("10.0.0.6"))

	report := Check(wireguard, "https://203.0.113.10:6443", []corev1.Node{on, bare}, []corev1.Node{on, bare})

	require.Empty(t, report.Blocking, "joining is possible: the server URL names the node that is on the mesh")
	require.Len(t, report.Warnings, 1)
	require.Contains(t, report.Warnings[0], "cp-b")
}

func TestAClusterWithNoControlPlaneNodesIsNotBlocked(t *testing.T) {
	wireguard := From(geassv1alpha1.GeassClusterNetworkSpec{Mesh: &geassv1alpha1.GeassClusterMeshSpec{}})
	worker := node("w1", nil, external("198.51.100.7"))
	require.True(t, Check(wireguard, "https://cp.example.com:6443", nil, []corev1.Node{worker}).OK(),
		"a control plane hosted outside the cluster has no node to look at")
}

func TestNodesThatAreNotOnTheMeshAreNamedAndTheListIsCut(t *testing.T) {
	wireguard := From(geassv1alpha1.GeassClusterNetworkSpec{Mesh: &geassv1alpha1.GeassClusterMeshSpec{}})
	control := node("cp", nil, external("203.0.113.10"))
	nodes := make([]corev1.Node, 0, 8)
	nodes = append(nodes, control)
	for _, name := range []string{"w7", "w1", "w2", "w3", "w4", "w5", "w6"} {
		nodes = append(nodes, node(name, nil, internal("10.1.0.5")))
	}

	report := Check(wireguard, "https://203.0.113.10:6443", []corev1.Node{control}, nodes)

	require.Empty(t, report.Blocking)
	require.Len(t, report.Warnings, 1)
	require.Contains(t, report.Warnings[0], "w1, w2, w3, w4, w5 and 2 more", "sorted, so the message does not change between reconciles")
	require.Contains(t, report.Warnings[0], "node-external-ip")
}

func TestAPrivateServerURLIsAWarningNotARefusal(t *testing.T) {
	wireguard := From(geassv1alpha1.GeassClusterNetworkSpec{Mesh: &geassv1alpha1.GeassClusterMeshSpec{}})
	control := node("cp", map[string]string{AnnotationFlannelBackendType: "wireguard"}, external("203.0.113.10"))

	report := Check(wireguard, "https://10.0.0.5:6443", []corev1.Node{control}, []corev1.Node{control})

	require.Empty(t, report.Blocking, "sites joined by a private link are legitimate, and the worker's own reachability check says the truth")
	require.Len(t, report.Warnings, 1)
	require.Contains(t, report.Warnings[0], "private address")
}

func TestWireGuardNodesWithPrivateAddressesAreWarnedAbout(t *testing.T) {
	wireguard := From(geassv1alpha1.GeassClusterNetworkSpec{Mesh: &geassv1alpha1.GeassClusterMeshSpec{}})
	tailscale := From(geassv1alpha1.GeassClusterNetworkSpec{Mesh: tailscaleMesh("")})
	control := node("cp", map[string]string{AnnotationFlannelBackendType: "wireguard"}, external("203.0.113.10"))
	behindNAT := node("edge", nil, external("192.168.1.20"))

	report := Check(wireguard, "https://203.0.113.10:6443", []corev1.Node{control}, []corev1.Node{control, behindNAT})
	require.Len(t, report.Warnings, 1)
	require.Contains(t, report.Warnings[0], "edge")
	require.Contains(t, report.Warnings[0], "opened the tunnel itself")

	require.True(t, Check(tailscale, "https://100.64.0.7:6443", nil, []corev1.Node{behindNAT}).OK(),
		"a tailnet node's address is the tailnet's, whatever the network around it")
}

func TestAnUnusableAdvertisedAddressIsReported(t *testing.T) {
	wireguard := From(geassv1alpha1.GeassClusterNetworkSpec{Mesh: &geassv1alpha1.GeassClusterMeshSpec{}})
	control := node("cp", map[string]string{AnnotationFlannelBackendType: "wireguard"}, external("203.0.113.10"))
	odd := node("odd", nil, external("127.0.0.1"))

	report := Check(wireguard, "https://203.0.113.10:6443", []corev1.Node{control}, []corev1.Node{control, odd})
	require.Len(t, report.Warnings, 1)
	require.Contains(t, report.Warnings[0], "no other machine can reach: odd")
}

func TestAReportsMessageNeverNamesAKey(t *testing.T) {
	tailscale := From(geassv1alpha1.GeassClusterNetworkSpec{Mesh: tailscaleMesh("https://headscale.example.com")})
	require.NotContains(t, Check(tailscale, "https://10.0.0.5:6443", nil, nil).Message(), "authKey")
}
