package ssh

import (
	"fmt"
	"strings"

	"github.com/degoke/geass/pkg/mesh"
)

// TailscaleJoin is what a node needs to join a tailnet (or a Headscale server's network) before K3s starts. K3s is then
// given the same credentials in a file, as its documentation shows, and told the node's tailnet address.
type TailscaleJoin struct {
	// AuthKey is the key the node joins with. It is a secret: it is written to a file only root can read, and is
	// taken out of every error that leaves this package.
	AuthKey string
	// ControlServerURL is the address of a Headscale server. Empty means Tailscale's own service.
	ControlServerURL string
}

// installTailscaleCommand installs Tailscale with its own script, as its documentation says to, when the machine does
// not have it, and makes sure the daemon is running. K3s does not install Tailscale: it is expected to be there.
const installTailscaleCommand = `command -v tailscale >/dev/null 2>&1 || curl -fsSL https://tailscale.com/install.sh | sh
systemctl enable --now tailscaled >/dev/null 2>&1 || true`

// joinTailnetCommand writes the file K3s reads its tailnet credentials from, with a mode that lets only root read it,
// and brings the node onto the tailnet unless it is already on it. It stops when the node has no tailnet address,
// because K3s is told to advertise that address and would register without one.
func (t *TailscaleJoin) joinTailnetCommand(hostname string) string {
	var script strings.Builder
	fmt.Fprintf(&script, "mkdir -p /etc/rancher/k3s\n")
	fmt.Fprintf(&script, "( umask 077 && printf '%%s' %s > %s )\n", shellQuote(mesh.VPNAuth(t.AuthKey, t.ControlServerURL)), mesh.AuthFile)
	fmt.Fprintf(&script, "chmod 600 %s\n", mesh.AuthFile)
	up := "tailscale up --auth-key=" + shellQuote(t.AuthKey) + " --hostname=" + shellQuote(hostname) + " --timeout=60s"
	if t.ControlServerURL != "" {
		up += " --login-server=" + shellQuote(t.ControlServerURL)
	}
	fmt.Fprintf(&script, "tailscale ip -4 >/dev/null 2>&1 || %s\n", up)
	script.WriteString(`tailscale ip -4 >/dev/null 2>&1 || { echo "this node did not get an IPv4 address on the tailnet: check the auth key, that it has not expired or been used up, and that the node is approved" >&2; exit 1; }`)
	return script.String()
}

// redact takes the auth key out of an error, whatever step produced it: a tool's complaint may repeat what it was given.
func (in ProvisionInput) redact(err error) error {
	if err == nil || in.Tailscale == nil || in.Tailscale.AuthKey == "" {
		return err
	}
	message := err.Error()
	if !strings.Contains(message, in.Tailscale.AuthKey) {
		return err
	}
	return &redactedError{message: strings.ReplaceAll(message, in.Tailscale.AuthKey, "[redacted]"), err: err}
}

// redactedError is an error whose text has been cleaned. The original stays reachable for errors.As and errors.Is,
// but its text is not what Error says.
type redactedError struct {
	message string
	err     error
}

func (e *redactedError) Error() string { return e.message }
func (e *redactedError) Unwrap() error { return e.err }

// tailnetSteps are the steps that bring a node onto a tailnet, for a node that joins a Tailscale mesh and none for any
// other. They are not optional: a node that is not on the tailnet cannot reach the control plane or the other nodes.
func tailnetSteps(in ProvisionInput) []provisionStep {
	if in.Tailscale == nil {
		return nil
	}
	return []provisionStep{
		{name: "install-tailscale", cmd: installTailscaleCommand},
		{name: "join-tailnet", cmd: in.Tailscale.joinTailnetCommand(in.NodeName)},
	}
}
