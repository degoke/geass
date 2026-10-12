package ssh

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/degoke/geass/pkg/platform"
)

type Provisioner interface {
	Provision(ctx context.Context, in ProvisionInput) error
	Drain(ctx context.Context, host string, sshKey []byte, user string) error
}

type ProvisionInput struct {
	Host, User string
	Port       int
	PrivateKey []byte
	K3sVersion string
	ServerURL  string
	Token      string
	NodeName   string
	// Config is what the node registers with: its labels and taints, and its overlay and log settings.
	Config AgentConfig
	// MultiSite means the cluster's nodes reach each other over the internet: the node needs WireGuard
	// and its overlay port open before it joins.
	MultiSite bool
}

type sshProvisioner struct{}

func New() Provisioner {
	return &sshProvisioner{}
}

func (p *sshProvisioner) Provision(ctx context.Context, in ProvisionInput) error {
	client, err := dial(ctx, in)
	if err != nil {
		return fmt.Errorf("ssh dial %s: %w", in.Host, err)
	}
	defer func() { _ = client.Close() }()

	for _, step := range provisionSteps(in) {
		if err := runStep(ctx, client, step.name, step.cmd); err != nil && !step.optional {
			return err
		}
	}
	return nil
}

type provisionStep struct {
	name string
	cmd  string
	// optional steps may fail without stopping the node from joining.
	optional bool
}

// provisionSteps are the commands that turn a bare machine into a cluster node,
// in order. The iSCSI initiator goes in before k3s so the node can serve Longhorn
// replicas the moment the scheduler starts placing them there.
func provisionSteps(in ProvisionInput) []provisionStep {
	steps := rootSteps(in)
	for i := range steps {
		steps[i].cmd = asRoot(in.User, steps[i].cmd)
	}
	return steps
}

// asRoot makes a command run with root's privileges. Every step installs software, writes /etc or manages
// a service, so none of them works as an ordinary user, and the usual cloud image logs in as one (ubuntu,
// ec2-user) with passwordless sudo. Run as ordinary, the steps fail with errors that blame the wrong
// thing: the WireGuard check reported that the kernel could not create an interface.
func asRoot(user, cmd string) string {
	if user == "" || user == "root" {
		return cmd
	}
	return "sudo -n sh -c " + shellQuote(cmd)
}

// shellQuote makes s one argument to a shell, however many quotes, newlines and $ signs it holds.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func rootSteps(in ProvisionInput) []provisionStep {
	steps := []provisionStep{
		{name: "check-curl", cmd: "which curl || (apt-get update -qq && apt-get install -y curl)"},
		{name: "install-iscsi", cmd: platform.NodePrerequisitesScript, optional: true},
	}
	// Joining a cluster that cannot be reached is the commonest way a node fails to join, and k3s's own
	// failure is a service that restarts forever. Checking first turns it into an error that says what to open.
	steps = append(steps, provisionStep{name: "check-cluster-reachable", cmd: reachabilityCommand(in)})
	if in.MultiSite {
		steps = append(steps,
			provisionStep{name: "check-wireguard", cmd: wireGuardCommand},
			provisionStep{name: "open-overlay-port", cmd: openOverlayPortCommand, optional: true},
		)
	}
	if !in.Config.IsZero() {
		steps = append(steps, provisionStep{name: "write-k3s-config", cmd: in.Config.writeConfigCommand()})
	}
	return append(steps,
		provisionStep{name: "install-k3s", cmd: fmt.Sprintf(
			`curl -sfL https://get.k3s.io | \
 INSTALL_K3S_VERSION=%s \
 INSTALL_K3S_EXEC="agent" \
 K3S_URL=%s \
 K3S_TOKEN=%s \
 K3S_NODE_NAME=%s \
 sh -`,
			in.K3sVersion,
			in.ServerURL,
			in.Token,
			in.NodeName,
		)},
		provisionStep{name: "enable-k3s", cmd: "systemctl enable k3s-agent && systemctl start k3s-agent"},
	)
}

// reachabilityCommand checks that the cluster's API answers from this node. k3s's /ping needs no
// credentials.
func reachabilityCommand(in ProvisionInput) string {
	hint := "cannot reach the cluster API at " + in.ServerURL + " from this node: open TCP 6443 to it"
	if in.MultiSite {
		hint += " (and UDP " + platform.WireGuardPort + " between every pair of nodes), including in the provider's security group"
	}
	return fmt.Sprintf(`curl -sk --max-time 15 %s/ping | grep -q pong || { echo %q >&2; exit 1; }`, in.ServerURL, hint)
}

// wireGuardCommand makes sure the kernel can create a WireGuard interface, which the encrypted overlay
// needs. Kernels before 5.6 need the module installed; the attempt is made once.
const wireGuardCommand = `try() { ip link add geass-wgcheck type wireguard 2>/dev/null && ip link del geass-wgcheck; }
if ! try; then
  if command -v apt-get >/dev/null 2>&1; then
    DEBIAN_FRONTEND=noninteractive apt-get install -y wireguard >/dev/null 2>&1 || true
  elif command -v dnf >/dev/null 2>&1; then
    dnf install -y wireguard-tools >/dev/null 2>&1 || true
  fi
  modprobe wireguard 2>/dev/null || true
fi
try || { echo "this node's kernel cannot create a WireGuard interface, which a multi-site cluster needs (built in from Linux 5.6)" >&2; exit 1; }`

// openOverlayPortCommand opens the WireGuard port on a host firewall that is active. It is best effort:
// a cloud provider's own security group cannot be changed from here, which is why reachability is checked.
const openOverlayPortCommand = `if command -v ufw >/dev/null 2>&1 && ufw status | grep -q "Status: active"; then
  ufw allow ` + platform.WireGuardPort + `/udp && ufw allow 10250/tcp
elif command -v firewall-cmd >/dev/null 2>&1 && [ "$(firewall-cmd --state 2>/dev/null)" = "running" ]; then
  firewall-cmd --permanent --add-port=` + platform.WireGuardPort + `/udp --add-port=10250/tcp && firewall-cmd --reload
fi`

func (p *sshProvisioner) Drain(ctx context.Context, host string, key []byte, user string) error {
	client, err := dial(ctx, ProvisionInput{
		Host: host, User: user, PrivateKey: key, Port: 22,
	})
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	return runStep(ctx, client, "uninstall-k3s", asRoot(user, "/usr/local/bin/k3s-agent-uninstall.sh || true"))
}

func dial(_ context.Context, in ProvisionInput) (*gossh.Client, error) {
	signer, err := gossh.ParsePrivateKey(in.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("parse key: %w", err)
	}

	port := in.Port
	if port == 0 {
		port = 22
	}

	cfg := &gossh.ClientConfig{
		User:            in.User,
		Auth:            []gossh.AuthMethod{gossh.PublicKeys(signer)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         30 * time.Second,
	}
	addr := fmt.Sprintf("%s:%d", in.Host, port)
	return gossh.Dial("tcp", addr, cfg)
}

func runStep(ctx context.Context, client *gossh.Client, name, cmd string) error {
	sess, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("new session for %s: %w", name, err)
	}
	defer func() { _ = sess.Close() }()

	var out, errBuf bytes.Buffer
	sess.Stdout = &out
	sess.Stderr = &errBuf

	done := make(chan error, 1)
	go func() { done <- sess.Run(cmd) }()

	select {
	case <-ctx.Done():
		_ = sess.Signal(gossh.SIGTERM)
		return fmt.Errorf("step %s cancelled: %w", name, ctx.Err())
	case err := <-done:
		if err != nil {
			return fmt.Errorf("step %s failed: %w\nstdout: %s\nstderr: %s",
				name, err, out.String(), errBuf.String())
		}
	}
	return nil
}
