package ssh

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

const fixtureAuthKey = "fixture-auth-key-0001"

func tailnetInput(controlServerURL string) ProvisionInput {
	join := &TailscaleJoin{AuthKey: fixtureAuthKey, ControlServerURL: controlServerURL}
	return ProvisionInput{
		User: "ubuntu", K3sVersion: "v1.36.5+k3s1", ServerURL: "https://100.64.0.7:6443", Token: "fixture-token", NodeName: "edge-1",
		Tailscale: join,
		Config:    NewAgentConfig(AgentOptions{Tailscale: join, Placement: storagePlacement()}),
	}
}

func stepByName(t *testing.T, steps []provisionStep, name string) provisionStep {
	t.Helper()
	index := indexOf(stepNames(steps), name)
	require.GreaterOrEqual(t, index, 0, "no step %s in %v", name, stepNames(steps))
	return steps[index]
}

type goldenStep struct {
	Name     string
	Cmd      string
	Optional bool
}

func goldenSteps(steps []provisionStep) []goldenStep {
	out := make([]goldenStep, 0, len(steps))
	for _, step := range steps {
		out = append(out, goldenStep{step.name, step.cmd, step.optional})
	}
	return out
}

// The join commands and the configuration written before the mesh field existed, captured from the code as it was.
// A cluster that does not use a Tailscale mesh must get exactly these, byte for byte.
func TestAClusterWithoutATailnetIsJoinedExactlyAsBefore(t *testing.T) {
	raw, err := os.ReadFile("testdata/legacy_join.golden.json")
	require.NoError(t, err)
	var golden map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &golden))

	placement := storagePlacement()
	base := ProvisionInput{User: "ubuntu", K3sVersion: "v1.36.5+k3s1", ServerURL: "https://203.0.113.1:6443", Token: "fixture-token", NodeName: "worker-1"}
	single := base
	single.Config = NewAgentConfig(AgentOptions{Placement: placement})
	multi := base
	multi.MultiSite = true
	multi.Config = NewAgentConfig(AgentOptions{Placement: placement, MultiSite: true, ExternalIP: "198.51.100.7"})
	root := multi
	root.User = "root"

	for name, got := range map[string][]goldenStep{
		"single-provision":     goldenSteps(provisionSteps(single)),
		"multi-provision":      goldenSteps(provisionSteps(multi)),
		"root-multi-provision": goldenSteps(provisionSteps(root)),
		"single-upgrade":       goldenSteps(upgradeSteps(single)),
		"multi-upgrade":        goldenSteps(upgradeSteps(multi)),
	} {
		var want []goldenStep
		require.NoError(t, json.Unmarshal(golden[name], &want), name)
		require.Equal(t, want, got, "%s changed: a cluster that does not use a Tailscale mesh must be joined as it was", name)
	}
	var multiConfig, singleConfig string
	require.NoError(t, json.Unmarshal(golden["multi-config"], &multiConfig))
	require.NoError(t, json.Unmarshal(golden["single-config"], &singleConfig))
	require.Equal(t, multiConfig, multi.Config.YAML())
	require.Equal(t, singleConfig, single.Config.YAML())
}

func TestATailnetWorkerIsToldWhatK3sDocumentsAndNothingItRefuses(t *testing.T) {
	config := tailnetInput("").Config

	var parsed map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(config.YAML()), &parsed))
	require.Equal(t, "/etc/rancher/k3s/vpn-auth", parsed["vpn-auth-file"])
	require.NotContains(t, parsed, "node-external-ip", "the address is the tailnet's, which only the machine knows once it has joined")
	require.NotContains(t, parsed, "flannel-external-ip", "an agent refuses it")
	require.NotContains(t, parsed, "flannel-backend", "an agent does not take it; the server decides")
	require.NotContains(t, config.YAML(), fixtureAuthKey, "the key is in its own file, never in the configuration")
	require.Contains(t, parsed["node-label"], "topology.kubernetes.io/region=us", "placement still shares the file")

	agentKeys := []string{"node-label", "node-taint", "node-external-ip", "kubelet-arg", "vpn-auth-file"}
	for key := range parsed {
		require.Contains(t, agentKeys, key, "k3s agents do not accept %q", key)
	}
}

func TestATailnetWorkerAdvertisesTheAddressItWasGivenOnTheTailnet(t *testing.T) {
	command := tailnetInput("").Config.writeConfigCommand()

	require.Contains(t, command, "<<'GEASS_K3S_CONFIG'\n")
	configEnd := strings.Index(command, "GEASS_K3S_CONFIG\n")
	last := command[configEnd:]
	require.Contains(t, last, `echo "node-external-ip: $(tailscale ip -4 | head -n 1)" >> /etc/rancher/k3s/config.yaml`,
		"appended after the heredoc ends, so the shell expands it and not the quoted document")
	require.False(t, tailnetInput("").Config.IsZero())
}

func TestATailnetJoinBringsTheNodeOntoTheTailnetBeforeItChecksTheControlPlane(t *testing.T) {
	steps := provisionSteps(tailnetInput(""))
	names := stepNames(steps)

	require.Equal(t, []string{
		"check-curl", "install-iscsi", "install-tailscale", "join-tailnet", "check-cluster-reachable", "write-k3s-config", "install-k3s", "enable-k3s",
	}, names, "the control plane is on the tailnet, so it can only be reached once the node is")
	require.NotContains(t, names, "check-wireguard", "Tailscale does not use the WireGuard kernel module")
	require.NotContains(t, names, "open-overlay-port", "a tailnet needs no port opened for the cluster")
	require.False(t, stepByName(t, steps, "install-tailscale").optional)
	require.False(t, stepByName(t, steps, "join-tailnet").optional, "a node that is not on the tailnet cannot join the cluster")
}

func TestAnUpgradeOfATailnetWorkerRewritesTheSameThings(t *testing.T) {
	names := stepNames(upgradeSteps(tailnetInput("")))

	require.Equal(t, []string{"check-curl", "install-tailscale", "join-tailnet", "check-cluster-reachable", "write-k3s-config", "install-k3s", "check-k3s-version"}, names)
}

func TestTheTailnetJoinWritesTheKeyWhereOnlyRootCanReadIt(t *testing.T) {
	command := stepByName(t, rootSteps(tailnetInput("")), "join-tailnet").cmd

	require.Contains(t, command, "( umask 077 && printf '%s' 'name=tailscale,joinKey="+fixtureAuthKey+"' > /etc/rancher/k3s/vpn-auth )")
	require.Contains(t, command, "chmod 600 /etc/rancher/k3s/vpn-auth")
	require.NotContains(t, command, "controlServerURL", "Tailscale's own service needs none")
	require.NotContains(t, command, "--login-server")
	require.Contains(t, command, "tailscale ip -4 >/dev/null 2>&1 || tailscale up --auth-key="+shellQuote(fixtureAuthKey))
	require.Contains(t, command, "--hostname='edge-1'")
	require.Contains(t, command, "did not get an IPv4 address on the tailnet")
}

func TestAHeadscaleServerIsGivenToK3sAndToTailscale(t *testing.T) {
	command := stepByName(t, rootSteps(tailnetInput("https://headscale.example.com")), "join-tailnet").cmd

	require.Contains(t, command, "name=tailscale,joinKey="+fixtureAuthKey+",controlServerURL=https://headscale.example.com")
	require.Contains(t, command, "--login-server='https://headscale.example.com'")
}

func TestTheTailnetCommandsAreRunAsRootThroughSudo(t *testing.T) {
	for _, step := range provisionSteps(tailnetInput("")) {
		require.True(t, strings.HasPrefix(step.cmd, "sudo -n sh -c "), "%s must run as root: %s", step.name, step.cmd)
	}
}

func TestTheHintForAnUnreachableControlPlaneNamesTheTailnetNotThePort(t *testing.T) {
	command := reachabilityCommand(tailnetInput(""))

	require.Contains(t, command, "address on the tailnet")
	require.Contains(t, command, "access rules")
	require.NotContains(t, command, "UDP 51820", "a tailnet has no overlay port to open")
	require.NotContains(t, command, "provider's security group")
}

func TestAnErrorNeverRepeatsTheAuthKey(t *testing.T) {
	in := tailnetInput("")
	original := &DialError{Host: "h", Err: errors.New("failed with " + fixtureAuthKey + " in it"), Retryable: true}

	err := in.redact(original)

	require.NotContains(t, err.Error(), fixtureAuthKey)
	require.Contains(t, err.Error(), "[redacted]")
	require.True(t, IsDialError(err), "what the caller decides on is still there")
	require.Same(t, err, in.redact(err), "an error that holds no key is returned as it is")
	require.NoError(t, in.redact(nil))

	other := errors.New("nothing secret")
	require.Same(t, other, in.redact(other))
	require.Same(t, other, ProvisionInput{}.redact(other), "a node without a tailnet has nothing to take out")
}

func TestADefaultClusterNodeHasNoTailnetSteps(t *testing.T) {
	require.Empty(t, tailnetSteps(ProvisionInput{}))
	_ = geassv1alpha1.MeshProviderTailscale
}

// A stand-in for the tailscale command, so the commands that run on a node can be run here for real: it keeps its state
// in a directory, says what it was asked to do, and gives the node an address once it has been brought up.
func fakeTailscale(t *testing.T, comesUp bool) (path, state string) {
	t.Helper()
	dir := t.TempDir()
	state = t.TempDir()
	script := `#!/bin/sh
state="` + state + `"
case "$1" in
  ip) [ -f "$state/up" ] && { echo 100.64.0.9; exit 0; }; exit 1 ;;
  up) echo "$@" >> "$state/up-called"; ` + map[bool]string{true: `touch "$state/up"`, false: `true`}[comesUp] + `; exit 0 ;;
esac
exit 2
`
	require.NoError(t, os.WriteFile(dir+"/tailscale", []byte(script), 0o755))
	return dir, state
}

func runHere(t *testing.T, tailscaleDir, k3sDir, command string) (string, error) {
	t.Helper()
	command = strings.ReplaceAll(command, "/etc/rancher/k3s", k3sDir)
	cmd := exec.Command("sh", "-c", command)
	cmd.Env = []string{"PATH=" + tailscaleDir + ":/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestTheTailnetJoinRunsAsWrittenAndLeavesTheKeyReadableByRootOnly(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	tailscaleDir, state := fakeTailscale(t, true)
	k3sDir := t.TempDir()
	command := stepByName(t, rootSteps(tailnetInput("https://headscale.example.com")), "join-tailnet").cmd

	out, err := runHere(t, tailscaleDir, k3sDir, command)
	require.NoError(t, err, out)

	file := k3sDir + "/vpn-auth"
	info, err := os.Stat(file)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "the key is readable by its owner only")
	content, err := os.ReadFile(file)
	require.NoError(t, err)
	require.Equal(t, "name=tailscale,joinKey="+fixtureAuthKey+",controlServerURL=https://headscale.example.com", string(content), "no trailing newline")

	called, err := os.ReadFile(state + "/up-called")
	require.NoError(t, err)
	require.Equal(t, "up --auth-key="+fixtureAuthKey+" --hostname=edge-1 --timeout=60s --login-server=https://headscale.example.com\n", string(called))

	// A second run finds the node on the tailnet and does not bring it up again.
	require.NoError(t, os.Remove(state+"/up-called"))
	out, err = runHere(t, tailscaleDir, k3sDir, command)
	require.NoError(t, err, out)
	_, err = os.Stat(state + "/up-called")
	require.True(t, os.IsNotExist(err), "a node that is on the tailnet is left alone")
}

func TestTheTailnetJoinStopsWhenTheNodeGetsNoAddress(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	tailscaleDir, _ := fakeTailscale(t, false)
	command := stepByName(t, rootSteps(tailnetInput("")), "join-tailnet").cmd

	out, err := runHere(t, tailscaleDir, t.TempDir(), command)

	require.Error(t, err, "K3s would be told to advertise an address that does not exist")
	require.Contains(t, out, "did not get an IPv4 address on the tailnet")
	require.NotContains(t, out, fixtureAuthKey)
}

func TestTheTailnetConfigurationGetsTheNodesTailnetAddressAppended(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	tailscaleDir, state := fakeTailscale(t, true)
	require.NoError(t, os.WriteFile(state+"/up", nil, 0o644))
	k3sDir := t.TempDir()
	command := stepByName(t, rootSteps(tailnetInput("")), "write-k3s-config").cmd

	out, err := runHere(t, tailscaleDir, k3sDir, command)
	require.NoError(t, err, out)

	content, err := os.ReadFile(k3sDir + "/config.yaml")
	require.NoError(t, err)
	var parsed map[string]any
	require.NoError(t, yaml.Unmarshal(content, &parsed), "the file is still one YAML document: %s", content)
	require.Equal(t, "100.64.0.9", parsed["node-external-ip"])
	require.Equal(t, "/etc/rancher/k3s/vpn-auth", strings.ReplaceAll(parsed["vpn-auth-file"].(string), k3sDir, "/etc/rancher/k3s"))
	require.Contains(t, parsed["node-label"], "topology.kubernetes.io/region=us")
}

func TestEveryTailnetStepIsValidShell(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	for _, in := range []ProvisionInput{tailnetInput(""), tailnetInput("https://headscale.example.com")} {
		for _, step := range append(provisionSteps(in), upgradeSteps(in)...) {
			cmd := exec.Command("sh", "-n")
			cmd.Stdin = strings.NewReader(step.cmd)
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, "%s: %s", step.name, out)
		}
	}
}
