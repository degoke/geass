package ssh

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRootRunsCommandsAsTheyAreAndEveryoneElseThroughSudo(t *testing.T) {
	require.Equal(t, "systemctl start k3s-agent", asRoot("root", "systemctl start k3s-agent"))
	require.Equal(t, "systemctl start k3s-agent", asRoot("", "systemctl start k3s-agent"), "no user is no SSH login; nothing to escalate")
	require.Equal(t, "sudo -n sh -c 'systemctl start k3s-agent'", asRoot("ubuntu", "systemctl start k3s-agent"),
		"-n so a machine that wants a password fails at once instead of hanging a join")
}

// The command reaches the node as one shell string, so quoting has to survive whatever the steps contain:
// here-documents, pipes, single quotes and variables.
func TestQuotingKeepsEveryCommandIntact(t *testing.T) {
	for name, command := range map[string]string{
		"single quotes": `echo 'it'"'"'s'`,
		"here-document": "cat > /etc/rancher/k3s/config.yaml <<'EOF'\nnode-label:\n- a=b\nEOF",
		"pipe and env":  `curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC="agent" K3S_TOKEN=$TOKEN sh -`,
		"dollar signs":  `echo "$HOME" '$HOME' $(id -u)`,
	} {
		t.Run(name, func(t *testing.T) {
			out, err := exec.Command("sh", "-c", "printf %s "+shellQuote(command)).Output()
			require.NoError(t, err)
			require.Equal(t, command, string(out), "the shell must hand the command over exactly as written")
		})
	}
}

func TestEveryProvisioningStepRunsAsRootForAnOrdinaryUser(t *testing.T) {
	in := ProvisionInput{User: "ubuntu", K3sVersion: "v1.30.0", ServerURL: "https://server:6443", Token: "t", NodeName: "n", MultiSite: true, Config: NewAgentConfig(AgentOptions{MultiSite: true, ExternalIP: "203.0.113.7"})}

	steps := provisionSteps(in)
	require.NotEmpty(t, steps)
	for _, step := range steps {
		require.True(t, strings.HasPrefix(step.cmd, "sudo -n sh -c '"), "step %s would run without root", step.name)
	}

	in.User = "root"
	for _, step := range provisionSteps(in) {
		require.False(t, strings.HasPrefix(step.cmd, "sudo"), "root does not need sudo (step %s)", step.name)
	}
}
