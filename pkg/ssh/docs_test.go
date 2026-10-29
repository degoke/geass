package ssh

import (
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/mesh"
)

const meshDoc = "../../website/src/content/docs/docs/operations/mesh-networking.md"

var (
	meshClusterExample = regexp.MustCompile("(?s)```yaml\n(spec:\n.*?)\n```")
	// A worker's configuration file as the documentation tells a person to write it by hand.
	handWrittenConfig = regexp.MustCompile("(?s)/etc/rancher/k3s/config\\.yaml[^\n]*<<'EOF'\n(.*?)\nEOF")
)

func readMeshDoc(t *testing.T) string {
	t.Helper()
	doc, err := os.ReadFile(meshDoc)
	if os.IsNotExist(err) {
		t.Skip("the documentation is not part of this checkout")
	}
	require.NoError(t, err)
	return string(doc)
}

// The page tells people to copy the cluster object, so it has to be one the API types take.
func TestTheClusterObjectInTheMeshDocIsOneGeassAccepts(t *testing.T) {
	blocks := meshClusterExample.FindAllStringSubmatch(readMeshDoc(t), -1)
	require.Len(t, blocks, 1)

	var cluster struct {
		Spec geassv1alpha1.GeassClusterSpec `json:"spec"`
	}
	require.NoError(t, yaml.UnmarshalStrict([]byte(blocks[0][1]), &cluster), "an unknown or misspelled field")
	require.NotNil(t, cluster.Spec.Network.Mesh)
	require.NoError(t, mesh.Validate(cluster.Spec.Network.Mesh))
	settings := mesh.From(cluster.Spec.Network)
	require.True(t, settings.IsTailscale())
	require.Empty(t, mesh.CheckServerURL(settings, cluster.Spec.ServerURL), "the example's own address passes the checks the page describes")
}

// ... and the files it tells people to write by hand have to be what the operator writes to a worker.
func TestTheWorkerFilesInTheMeshDocAreWhatGeassWrites(t *testing.T) {
	blocks := handWrittenConfig.FindAllStringSubmatch(readMeshDoc(t), -1)
	require.Len(t, blocks, 2, "one for WireGuard and one for Tailscale")

	parse := func(text string) map[string]any {
		var parsed map[string]any
		require.NoError(t, yaml.Unmarshal([]byte(text), &parsed))
		return parsed
	}
	region := &geassv1alpha1.GeassNodePlacement{Region: "eu"}

	wireguard := NewAgentConfig(AgentOptions{Placement: region, MultiSite: true, ExternalIP: "198.51.100.7"})
	require.Equal(t, parse(wireguard.YAML()), parse(blocks[0][1]), "the WireGuard file in the page")

	tailnet := NewAgentConfig(AgentOptions{Tailscale: &TailscaleJoin{AuthKey: fixtureAuthKey}})
	require.Equal(t, parse(tailnet.YAML()), parse(blocks[1][1]), "the Tailscale file in the page, before the node's address is added")
}

func TestTheTailscaleCommandsInTheMeshDocAreWhatGeassRuns(t *testing.T) {
	doc := readMeshDoc(t)
	join := &TailscaleJoin{AuthKey: "<key>"}

	require.Contains(t, doc, "sudo tailscale up --auth-key=<key> --hostname=edge-1 --timeout=60s", "the flags the operator passes")
	require.Contains(t, doc, "printf '%s' '"+mesh.VPNAuth("<key>", "")+"'", "the line the operator writes")
	require.Contains(t, doc, `echo "node-external-ip: $(tailscale ip -4 | head -n 1)"`, "how the node's address is added")
	require.Contains(t, join.joinTailnetCommand("edge-1"), "tailscale up --auth-key="+shellQuote("<key>")+" --hostname="+shellQuote("edge-1")+" --timeout=60s")
	require.Contains(t, doc, "INSTALL_K3S_EXEC=agent")
}
