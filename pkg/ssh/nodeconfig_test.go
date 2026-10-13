package ssh

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

func storagePlacement() *geassv1alpha1.GeassNodePlacement {
	return &geassv1alpha1.GeassNodePlacement{
		Region: "us", Zone: "aws-us-east-1",
		Tags: []geassv1alpha1.GeassNodeTag{{Name: "storage", Reserved: true}, {Name: "compute"}},
	}
}

func TestAgentConfigRegistersTheNodeWithItsPlacement(t *testing.T) {
	config := AgentConfigFor(storagePlacement())

	require.Equal(t, []string{
		"tag.geass.dev/compute=true",
		"tag.geass.dev/storage=true",
		"topology.kubernetes.io/region=us",
		"topology.kubernetes.io/zone=aws-us-east-1",
	}, config.NodeLabels)
	require.Equal(t, []string{"tag.geass.dev/storage=reserved:NoSchedule"}, config.NodeTaints, "only the reserved tag is a taint")
}

func TestAgentConfigWithoutAPlacementWritesNothing(t *testing.T) {
	require.True(t, AgentConfigFor(nil).IsZero())
	require.True(t, AgentConfigFor(&geassv1alpha1.GeassNodePlacement{}).IsZero())
}

func TestAgentConfigYAMLIsWhatK3sReads(t *testing.T) {
	var parsed map[string][]string
	require.NoError(t, yaml.Unmarshal([]byte(AgentConfigFor(storagePlacement()).YAML()), &parsed))
	require.Contains(t, parsed["node-label"], "topology.kubernetes.io/region=us")
	require.Equal(t, []string{"tag.geass.dev/storage=reserved:NoSchedule"}, parsed["node-taint"])
}

func TestProvisionWritesTheConfigBeforeK3sStarts(t *testing.T) {
	steps := provisionSteps(ProvisionInput{K3sVersion: "v1.30.0", ServerURL: "https://server:6443", Token: "t", NodeName: "n", Config: AgentConfigFor(storagePlacement())})

	index := map[string]int{}
	for i, step := range steps {
		index[step.name] = i
	}
	configStep, ok := index["write-k3s-config"]
	require.True(t, ok, "a node with a placement needs its config written")
	require.Less(t, configStep, index["install-k3s"], "the labels must be there when the node first registers, or it can be scheduled on before it is labelled")
	require.False(t, steps[configStep].optional, "a node that cannot be labelled must not join")
	require.Contains(t, steps[configStep].cmd, "/etc/rancher/k3s/config.yaml")
	require.True(t, strings.Contains(steps[configStep].cmd, "<<'GEASS_K3S_CONFIG'"), "the content must not be interpreted by the shell")
}

func TestProvisionWithoutAPlacementWritesNoConfig(t *testing.T) {
	for _, step := range provisionSteps(ProvisionInput{K3sVersion: "v1.30.0", ServerURL: "https://server:6443", Token: "t", NodeName: "n"}) {
		require.NotEqual(t, "write-k3s-config", step.name)
	}
}

func TestEveryWorkerKeepsMoreContainerLogs(t *testing.T) {
	config := NewAgentConfig(AgentOptions{})
	require.Equal(t, []string{"container-log-max-size=50Mi", "container-log-max-files=5"}, config.KubeletArgs, "the node's own log history is what the dashboard reads")
	require.False(t, config.IsZero())

	var parsed map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(config.YAML()), &parsed))
	require.Contains(t, parsed["kubelet-arg"], "container-log-max-size=50Mi")
	require.NotContains(t, parsed, "node-external-ip", "a single-site cluster advertises nothing extra")
	require.NotContains(t, parsed, "flannel-external-ip")
}

func TestAMultiSiteWorkerAdvertisesItsPublicAddress(t *testing.T) {
	config := NewAgentConfig(AgentOptions{MultiSite: true, ExternalIP: "203.0.113.7", Placement: storagePlacement()})

	var parsed map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(config.YAML()), &parsed))
	require.Equal(t, "203.0.113.7", parsed["node-external-ip"])
	require.NotContains(t, parsed, "flannel-external-ip", "a k3s agent refuses this flag and will not start; the server passes the setting on")
	require.Contains(t, parsed["node-label"], "topology.kubernetes.io/region=us", "placement and overlay settings share one file")
}

func TestAMultiSiteWorkerWithoutAPublicAddressAdvertisesNothing(t *testing.T) {
	config := NewAgentConfig(AgentOptions{MultiSite: true})
	require.Empty(t, config.NodeExternalIP, "there is no address to advertise, so none is invented")
}

// Found by running a real k3s agent with the file this code wrote: it exited with "flag provided but not
// defined: -flannel-external-ip". Every key written here must be one an agent accepts.
func TestAWorkerConfigOnlyUsesSettingsAK3sAgentAccepts(t *testing.T) {
	agentKeys := []string{"node-label", "node-taint", "node-external-ip", "kubelet-arg"}
	config := NewAgentConfig(AgentOptions{MultiSite: true, ExternalIP: "203.0.113.7", Placement: storagePlacement()})

	var parsed map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(config.YAML()), &parsed))
	for key := range parsed {
		require.Contains(t, agentKeys, key, "k3s agents do not accept %q", key)
	}
}

func stepNames(steps []provisionStep) []string {
	names := make([]string, len(steps))
	for i, step := range steps {
		names[i] = step.name
	}
	return names
}

func TestEveryJoinChecksTheClusterIsReachableFirst(t *testing.T) {
	steps := provisionSteps(ProvisionInput{K3sVersion: "v1.30.0", ServerURL: "https://203.0.113.1:6443", Token: "t", NodeName: "n"})
	names := stepNames(steps)

	require.Less(t, indexOf(names, "check-cluster-reachable"), indexOf(names, "install-k3s"))
	check := steps[indexOf(names, "check-cluster-reachable")]
	require.False(t, check.optional, "a node that cannot reach the cluster must not be left restarting k3s forever")
	require.Contains(t, check.cmd, "https://203.0.113.1:6443/ping")
	require.Contains(t, check.cmd, "TCP 6443 is open to this node")
	require.NotContains(t, check.cmd, "UDP 51820", "the single-site hint does not mention the overlay port")
}

func TestAMultiSiteJoinChecksWireGuardAndOpensTheOverlayPort(t *testing.T) {
	steps := provisionSteps(ProvisionInput{K3sVersion: "v1.30.0", ServerURL: "https://203.0.113.1:6443", Token: "t", NodeName: "n", MultiSite: true})
	names := stepNames(steps)

	require.Less(t, indexOf(names, "check-wireguard"), indexOf(names, "install-k3s"), "WireGuard must work before k3s tries to use it")
	require.False(t, steps[indexOf(names, "check-wireguard")].optional)
	require.Contains(t, steps[indexOf(names, "check-wireguard")].cmd, "type wireguard")
	port := steps[indexOf(names, "open-overlay-port")]
	require.True(t, port.optional, "a cloud security group cannot be changed from here, so this is best effort")
	require.Contains(t, port.cmd, "51820/udp")
	require.Contains(t, steps[indexOf(names, "check-cluster-reachable")].cmd, "UDP 51820", "the failure hint names the overlay port")
}

func TestASingleSiteJoinDoesNotTouchWireGuardOrFirewalls(t *testing.T) {
	names := stepNames(provisionSteps(ProvisionInput{K3sVersion: "v1.30.0", ServerURL: "https://server:6443", Token: "t", NodeName: "n"}))
	require.NotContains(t, names, "check-wireguard")
	require.NotContains(t, names, "open-overlay-port")
}

func indexOf(names []string, name string) int {
	for i, candidate := range names {
		if candidate == name {
			return i
		}
	}
	return -1
}
