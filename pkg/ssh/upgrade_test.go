package ssh

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/degoke/geass/pkg/platform"
)

func upgradeInput() ProvisionInput {
	return ProvisionInput{K3sVersion: "v1.35.1+k3s1", ServerURL: "https://10.0.0.5:6443", Token: "t", NodeName: "w1", User: "root"}
}

func names(steps []provisionStep) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.name
	}
	return out
}

func TestAnUpgradeChecksTheClusterIsReachableBeforeReplacingK3sAndTheVersionAfter(t *testing.T) {
	steps := upgradeSteps(upgradeInput())
	require.Equal(t, []string{"check-curl", "check-cluster-reachable", "install-k3s", "check-k3s-version"}, names(steps))
}

func TestAnUpgradeDoesNotRepeatWhatAJoinedNodeAlreadyHas(t *testing.T) {
	for _, step := range upgradeSteps(upgradeInput()) {
		require.NotEqual(t, "install-iscsi", step.name, "a node that serves Longhorn replicas has it")
		require.NotContains(t, step.cmd, platform.NodePrerequisitesScript)
		require.NotEqual(t, "enable-k3s", step.name, "the install script restarts the service itself")
		require.NotContains(t, step.cmd, "uninstall", "an upgrade never removes K3s from the node")
	}
}

func TestAnUpgradeRewritesTheNodeConfigOnlyWhenThereIsOne(t *testing.T) {
	require.NotContains(t, names(upgradeSteps(upgradeInput())), "write-k3s-config")

	in := upgradeInput()
	in.Config = AgentConfigFor(storagePlacement())
	steps := names(upgradeSteps(in))
	require.Equal(t, []string{"check-curl", "check-cluster-reachable", "write-k3s-config", "install-k3s", "check-k3s-version"}, steps)
}

func TestAnUpgradeInstallsTheSameAgentTheNodeJoinedAs(t *testing.T) {
	in := upgradeInput()
	var upgrade, join string
	for _, s := range upgradeSteps(in) {
		if s.name == "install-k3s" {
			upgrade = s.cmd
		}
	}
	for _, s := range provisionSteps(in) {
		if s.name == "install-k3s" {
			join = s.cmd
		}
	}
	require.Equal(t, join, upgrade, "same name, same server, same token: the node is the node it was, on a newer K3s")
	require.Contains(t, upgrade, "INSTALL_K3S_VERSION='v1.35.1+k3s1'")
	require.Contains(t, upgrade, `INSTALL_K3S_EXEC="agent"`)
	require.Contains(t, upgrade, "K3S_NODE_NAME='w1'")
}

func TestTheVersionCheckQuotesTheVersionAndNamesWhatItFound(t *testing.T) {
	check := agentVersionCheck("v1.35.1+k3s1;id")
	require.Contains(t, check, "grep -qF 'v1.35.1+k3s1;id'")
	require.Contains(t, check, "systemctl is-active --quiet k3s-agent")
	require.Contains(t, check, "journalctl -u k3s-agent", "a failure points at where to look")
	require.NotContains(t, check, "K3s v1.35.1+k3s1;id:", "the version is only ever passed as a quoted argument")
}

func TestAnUpgradeAsAnOrdinaryUserUsesSudoOnEveryStep(t *testing.T) {
	in := upgradeInput()
	in.User = "ubuntu"
	for _, s := range upgradeSteps(in) {
		require.True(t, strings.HasPrefix(s.cmd, "sudo -n sh -c "), "%s: %s", s.name, s.cmd)
	}
}

func TestAnUpgradeRunsEveryStepOnTheNodeOverSSH(t *testing.T) {
	server := startSSHServer(t)
	in := server.input(t)
	in.K3sVersion, in.ServerURL, in.Token, in.NodeName = "v1.35.1+k3s1", "https://10.0.0.5:6443", "secret", "w1"

	require.NoError(t, New().Upgrade(shortContext(t), in))

	ran := server.ran()
	require.Len(t, ran, 4)
	require.Contains(t, ran[1], "/ping", "the cluster's reachability is checked first")
	require.Contains(t, ran[2], "INSTALL_K3S_VERSION='v1.35.1+k3s1'")
	require.Contains(t, ran[3], "grep -qF 'v1.35.1+k3s1'")
}

func TestAnUpgradeRefusesAMachineWhoseHostKeyIsNotTheRecordedOne(t *testing.T) {
	server := startSSHServer(t)
	in := server.input(t)
	in.KnownHosts = knownHostsFor("[127.0.0.1]:1", newHostKey(t))
	in.K3sVersion, in.Token = "v1.35.1+k3s1", "secret-token"

	require.Error(t, New().Upgrade(shortContext(t), in))
	require.Empty(t, server.ran(), "the join token rides in the install command: it goes nowhere a key does not match")
}
