package installer

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

const fixtureAuthKey = "fixture-auth-key-0001"

func writeAuthKey(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "authkey")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func parsedServerConfig(t *testing.T, network Network) map[string]any {
	t.Helper()
	var config map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(network.ServerConfig()), &config))
	return config
}

// What the installer wrote, and what it said, before the mesh field existed: captured from that code. A cluster that
// does not use GEASS_MESH must get exactly this.
func TestAClusterThatDoesNotUseTheMeshIsInstalledExactlyAsBefore(t *testing.T) {
	raw, err := os.ReadFile("testdata/legacy_network.golden.json")
	require.NoError(t, err)
	var golden map[string]string
	require.NoError(t, json.Unmarshal(raw, &golden))

	single := Network{}
	multi := Network{MultiSite: true, ExternalIP: "203.0.113.1"}
	require.Equal(t, golden["single-server-config"], single.ServerConfig())
	require.Equal(t, golden["multi-server-config"], multi.ServerConfig())
	require.Equal(t, golden["single-manifest"], geassClusterManifest("default", "v1", "https://10.0.0.5:6443", single, "ops@example.com"))
	require.Equal(t, golden["multi-manifest"], geassClusterManifest("default", "v1", "https://203.0.113.1:6443", multi, "ops@example.com"))
	require.Equal(t, golden["multi-server-url"], multi.ServerURL())
}

func TestTheEnvironmentOfAClusterThatAsksForNothingBuildsNoMesh(t *testing.T) {
	for _, name := range []string{"GEASS_MESH", "GEASS_MULTI_SITE", "GEASS_NODE_EXTERNAL_IP", "GEASS_TAILSCALE_AUTH_KEY_FILE", "GEASS_TAILSCALE_CONTROL_URL"} {
		t.Setenv(name, "")
	}
	network := NetworkFromEnv()

	require.Equal(t, Network{}, network)
	require.False(t, network.Enabled())
	require.NoError(t, network.Validate())
}

// GEASS_MULTI_SITE on its own is not the mesh field: it is not checked in the new ways and the cluster object does not
// say mesh, so a cluster installed that way is the one that was installed before.
func TestMultiSiteAloneStaysTheLegacyInstallEvenWithAnOddAddress(t *testing.T) {
	t.Setenv("GEASS_MESH", "")
	t.Setenv("GEASS_MULTI_SITE", "1")
	t.Setenv("GEASS_NODE_EXTERNAL_IP", "10.0.0.5")

	network := NetworkFromEnv()

	require.True(t, network.MultiSite)
	require.Empty(t, network.Mesh)
	require.NoError(t, network.Validate(), "a private address was never refused here, and a cluster installed that way must not start to be")
	require.NotContains(t, networkManifest(network), "mesh", "the cluster object says what it always said")
}

func TestAWireGuardMeshIsMultiSiteThatAlsoSaysMesh(t *testing.T) {
	t.Setenv("GEASS_MESH", "WireGuard")
	t.Setenv("GEASS_MULTI_SITE", "")
	t.Setenv("GEASS_NODE_EXTERNAL_IP", "203.0.113.1")

	network := NetworkFromEnv()

	require.True(t, network.MultiSite)
	require.Equal(t, geassv1alpha1.MeshProviderWireGuard, network.Mesh)
	require.NoError(t, network.Validate())
	require.Equal(t, parsedServerConfig(t, Network{MultiSite: true, ExternalIP: "203.0.113.1"}), parsedServerConfig(t, network),
		"the server is configured exactly as a multi-site server")
	require.Equal(t, "  network:\n    multiSite: true\n    mesh:\n      provider: WireGuard\n", networkManifest(network))

	manifest := geassClusterManifest("default", "v1", network.ServerURL(), network, "")
	var cluster geassv1alpha1.GeassCluster
	require.NoError(t, yaml.UnmarshalStrict([]byte(manifest), &cluster), manifest)
	require.True(t, cluster.Spec.Network.MultiSite)
	require.Equal(t, geassv1alpha1.MeshProviderWireGuard, cluster.Spec.Network.Mesh.Provider)
}

func TestAMeshMakesTheInstallerRefuseAddressesNoSiteCanUse(t *testing.T) {
	for _, address := range []string{"127.0.0.1", "0.0.0.0", "169.254.1.1", "::1", "224.0.0.1"} {
		err := Network{MultiSite: true, Mesh: geassv1alpha1.MeshProviderWireGuard, ExternalIP: address}.Validate()
		require.Error(t, err, address)
		require.Contains(t, err.Error(), "cannot be reached from another machine")
	}
	// Private and shared addresses are warned about, not refused: sites that route to each other are legitimate.
	for _, address := range []string{"10.0.0.5", "192.168.1.20", "100.64.0.7"} {
		require.NoError(t, Network{MultiSite: true, Mesh: geassv1alpha1.MeshProviderWireGuard, ExternalIP: address}.Validate(), address)
	}
}

func TestAnUnknownMeshIsRefusedBeforeAnythingIsChanged(t *testing.T) {
	t.Setenv("GEASS_MESH", "zerotier")
	err := NetworkFromEnv().Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "is not one of wireguard, tailscale")
}

func fakeTailnet(t *testing.T, ip string, err error) {
	t.Helper()
	previous := tailnetIPv4
	tailnetIPv4 = func() (string, error) { return ip, err }
	t.Cleanup(func() { tailnetIPv4 = previous })
}

func tailscaleEnvironment(t *testing.T, keyFile string) {
	t.Helper()
	t.Setenv("GEASS_MESH", "tailscale")
	t.Setenv("GEASS_MULTI_SITE", "")
	t.Setenv("GEASS_NODE_EXTERNAL_IP", "")
	t.Setenv("GEASS_TAILSCALE_AUTH_KEY_FILE", keyFile)
	t.Setenv("GEASS_TAILSCALE_CONTROL_URL", "")
}

func TestATailscaleMeshReadsItsKeyFromAFileAndItsAddressFromTailscale(t *testing.T) {
	tailscaleEnvironment(t, writeAuthKey(t, fixtureAuthKey+"\n"))
	fakeTailnet(t, "100.64.0.7", nil)

	network := NetworkFromEnv()

	require.NoError(t, network.Validate())
	require.True(t, network.IsTailscale())
	require.False(t, network.MultiSite, "no WireGuard module or port is needed")
	require.Equal(t, "100.64.0.7", network.ExternalIP)
	require.Equal(t, fixtureAuthKey, network.Tailscale.AuthKey, "the newline at the end of the file is not part of the key")
	require.Equal(t, "https://100.64.0.7:6443", network.ServerURL(), "workers join the control plane on its tailnet address")
}

func TestATailscaleServerIsConfiguredAsK3sDocumentsIt(t *testing.T) {
	network := Network{Mesh: geassv1alpha1.MeshProviderTailscale, ExternalIP: "100.64.0.7", Tailscale: TailscaleNetwork{AuthKey: fixtureAuthKey}}

	config := parsedServerConfig(t, network)

	require.Equal(t, "/etc/rancher/k3s/vpn-auth", config["vpn-auth-file"])
	require.Equal(t, "100.64.0.7", config["node-external-ip"])
	require.Equal(t, []any{"100.64.0.7"}, config["tls-san"])
	require.NotContains(t, config, "flannel-backend", "K3s's integration decides what runs over the tailnet")
	require.NotContains(t, config, "flannel-external-ip")
	require.Equal(t, true, config["secrets-encryption"], "every server encrypts Secrets")
	require.NotContains(t, network.ServerConfig(), fixtureAuthKey, "the key is in its own file")
}

func TestATailscaleMeshNeedsAKeyAnAddressAndANodeThatIsOnTheTailnet(t *testing.T) {
	good := writeAuthKey(t, fixtureAuthKey)

	t.Run("no key file", func(t *testing.T) {
		tailscaleEnvironment(t, "")
		err := NetworkFromEnv().Validate()
		require.ErrorContains(t, err, "GEASS_TAILSCALE_AUTH_KEY_FILE")
	})
	t.Run("a key file that is not there", func(t *testing.T) {
		tailscaleEnvironment(t, filepath.Join(t.TempDir(), "missing"))
		require.ErrorContains(t, NetworkFromEnv().Validate(), "read GEASS_TAILSCALE_AUTH_KEY_FILE")
	})
	t.Run("a key that cannot be one", func(t *testing.T) {
		bad := "has,a comma and a space"
		tailscaleEnvironment(t, writeAuthKey(t, bad))
		err := NetworkFromEnv().Validate()
		require.ErrorContains(t, err, "letters, digits")
		require.NotContains(t, err.Error(), bad, "a refused key may be a real one")
	})
	t.Run("a machine that is not on the tailnet", func(t *testing.T) {
		tailscaleEnvironment(t, good)
		fakeTailnet(t, "", errors.New("exit status 1"))
		err := NetworkFromEnv().Validate()
		require.ErrorContains(t, err, "no address on the tailnet")
		require.ErrorContains(t, err, "tailscale up")
	})
	t.Run("an address given by hand", func(t *testing.T) {
		tailscaleEnvironment(t, good)
		t.Setenv("GEASS_NODE_EXTERNAL_IP", "100.64.0.9")
		fakeTailnet(t, "", errors.New("must not be asked"))
		network := NetworkFromEnv()
		require.NoError(t, network.Validate())
		require.Equal(t, "100.64.0.9", network.ExternalIP)
	})
	t.Run("an address that is not one", func(t *testing.T) {
		tailscaleEnvironment(t, good)
		t.Setenv("GEASS_NODE_EXTERNAL_IP", "not-an-ip")
		require.ErrorContains(t, NetworkFromEnv().Validate(), "is not an IP address")
	})
	t.Run("multi-site as well", func(t *testing.T) {
		tailscaleEnvironment(t, good)
		t.Setenv("GEASS_MULTI_SITE", "1")
		require.ErrorContains(t, NetworkFromEnv().Validate(), "WireGuard mesh")
	})
	t.Run("a control server that is not https", func(t *testing.T) {
		tailscaleEnvironment(t, good)
		t.Setenv("GEASS_TAILSCALE_CONTROL_URL", "http://headscale.example.com")
		require.ErrorContains(t, NetworkFromEnv().Validate(), "GEASS_TAILSCALE_CONTROL_URL")
	})
}

func TestATailscaleClusterObjectNamesTheSecretAndTheControlServer(t *testing.T) {
	network := Network{
		Mesh: geassv1alpha1.MeshProviderTailscale, ExternalIP: "100.64.0.7",
		Tailscale: TailscaleNetwork{AuthKey: fixtureAuthKey, ControlServerURL: "https://headscale.example.com"},
	}

	manifest := geassClusterManifest("default", "v1", network.ServerURL(), network, "")

	var cluster geassv1alpha1.GeassCluster
	require.NoError(t, yaml.UnmarshalStrict([]byte(manifest), &cluster), manifest)
	require.Equal(t, "https://100.64.0.7:6443", cluster.Spec.ServerURL)
	require.False(t, cluster.Spec.Network.MultiSite, "an operator that does not know the mesh field must not take this for WireGuard")
	require.Equal(t, geassv1alpha1.MeshProviderTailscale, cluster.Spec.Network.Mesh.Provider)
	require.Equal(t, "geass-mesh-auth", cluster.Spec.Network.Mesh.Tailscale.AuthKeySecretRef)
	require.Equal(t, "https://headscale.example.com", cluster.Spec.Network.Mesh.Tailscale.ControlServerURL)
	require.NotContains(t, manifest, fixtureAuthKey, "the key is in a Secret, not in the cluster object")

	network.Tailscale.ControlServerURL = ""
	require.NotContains(t, geassClusterManifest("default", "v1", network.ServerURL(), network, ""), "controlServerURL")
}

func TestTheMeshAuthKeyIsStoredInASecretTheClusterObjectNames(t *testing.T) {
	manifest, err := meshAuthSecretManifest(fixtureAuthKey)
	require.NoError(t, err)

	var secret struct {
		Metadata struct{ Name, Namespace string }
		Data     map[string]string
	}
	require.NoError(t, json.Unmarshal([]byte(manifest), &secret))
	require.Equal(t, "geass-mesh-auth", secret.Metadata.Name)
	require.Equal(t, "geass-system", secret.Metadata.Namespace)
	decoded, err := base64.StdEncoding.DecodeString(secret.Data["authKey"])
	require.NoError(t, err)
	require.Equal(t, fixtureAuthKey, string(decoded))
}

func TestTheMeshAuthSecretStepDoesNothingWithoutATailnet(t *testing.T) {
	require.NoError(t, (&MeshAuthSecret{Network: Network{}}).Run(), "it must not run kubectl for a cluster that has no tailnet")
	require.NoError(t, (&MeshAuthSecret{Network: Network{MultiSite: true, ExternalIP: "203.0.113.1"}}).Run())
}

func TestAServerThatJoinsAClusterIsNotGivenAMesh(t *testing.T) {
	t.Setenv("GEASS_MESH", "tailscale")
	t.Setenv("GEASS_MULTI_SITE", "")
	err := (&JoinChecks{Plan: &JoinPlan{Join: JoinOptions{ServerURL: "https://203.0.113.1:6443", Token: "fixture-token"}}}).Run()
	require.ErrorContains(t, err, "GEASS_MESH", "a server that joins is not the first, so it takes no mesh of its own")
}

// Found on EC2 (spike mesh-nat): with GEASS_NODE_EXTERNAL_IP set to an Elastic IP, which is on no interface, the pods on the
// control plane cannot reach the API server (it is advertised on that address and EC2 does not send a machine's own traffic to
// its public address back), so the operator crash-looped and the installer gave up after five minutes with no hint why.
func TestAWarningSaysWhenTheExternalAddressIsOnNoInterfaceOfThisMachine(t *testing.T) {
	machine := func(addresses ...string) func() ([]net.Addr, error) {
		return func() ([]net.Addr, error) {
			out := make([]net.Addr, 0, len(addresses))
			for _, address := range addresses {
				ip := net.ParseIP(address)
				out = append(out, &net.IPNet{IP: ip, Mask: net.CIDRMask(32, 32)})
			}
			return out, nil
		}
	}
	original := interfaceAddresses
	t.Cleanup(func() { interfaceAddresses = original })

	interfaceAddresses = machine("127.0.0.1", "10.61.1.170")
	warning := externalAddressNotOnThisMachine("13.51.184.132")
	require.Contains(t, warning, "13.51.184.132 is not an address of any network interface of this machine")
	require.Contains(t, warning, "ip addr add 13.51.184.132/32 dev lo")

	// The workaround itself, a public address on a server that has it on the interface, and an address that is not one.
	interfaceAddresses = machine("127.0.0.1", "10.61.1.170", "13.51.184.132")
	require.Empty(t, externalAddressNotOnThisMachine("13.51.184.132"))
	interfaceAddresses = machine("203.0.113.9")
	require.Empty(t, externalAddressNotOnThisMachine("203.0.113.9"))
	require.Empty(t, externalAddressNotOnThisMachine("not-an-ip"))

	// A machine that cannot list its interfaces is not told it is wrong.
	interfaceAddresses = func() ([]net.Addr, error) { return nil, errors.New("no netlink") }
	require.Empty(t, externalAddressNotOnThisMachine("13.51.184.132"))
}
