package ssh

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/degoke/geass/pkg/platform"
)

type Provisioner interface {
	Provision(ctx context.Context, in ProvisionInput) error
	// Upgrade replaces K3s on a worker that is already part of the cluster with in.K3sVersion, keeping the node's name,
	// its place in the cluster and everything on its disks, and restarts the agent. It needs the same fields as
	// Provision. It is safe to run again on a node that is already on the version: it then only restarts the agent.
	Upgrade(ctx context.Context, in ProvisionInput) error
	// Drain uninstalls the k3s agent from the machine. It connects the way Provision does, so only the
	// connection fields of in are used: Host, Port, User, PrivateKey and KnownHosts.
	Drain(ctx context.Context, in ProvisionInput) error
}

type ProvisionInput struct {
	Host, User string
	Port       int
	PrivateKey []byte
	// KnownHosts holds the host keys the machine may present, in the format `ssh-keyscan` prints. When it
	// is set a machine with any other key is refused; when it is empty the host key is not checked.
	KnownHosts []byte
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

// installK3sCommand installs the K3s agent at in.K3sVersion with the install script. Run over a node that already has
// the agent it replaces the binary and restarts the service, which is how K3s is upgraded; the node's data, its
// registration and its running containers are not touched.
func installK3sCommand(in ProvisionInput) string {
	return fmt.Sprintf(
		`curl -sfL https://get.k3s.io | \
 INSTALL_K3S_VERSION=%s \
 INSTALL_K3S_EXEC="agent" \
 K3S_URL=%s \
 K3S_TOKEN=%s \
 K3S_NODE_NAME=%s \
 sh -`,
		shellQuote(in.K3sVersion),
		shellQuote(in.ServerURL),
		shellQuote(in.Token),
		shellQuote(in.NodeName),
	)
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
		provisionStep{name: "install-k3s", cmd: installK3sCommand(in)},
		provisionStep{name: "enable-k3s", cmd: "systemctl enable k3s-agent && systemctl start k3s-agent"},
	)
}

// upgradeSteps are the commands that move a worker that is already in the cluster to a newer K3s. It does what
// joining does, minus what a node that has joined already has (the iSCSI initiator, the firewall), and ends by checking
// that the agent is running the version that was asked for: the install script exits as soon as it has restarted the
// service, which is not the same as the node being back.
func upgradeSteps(in ProvisionInput) []provisionStep {
	steps := []provisionStep{
		{name: "check-curl", cmd: "which curl || (apt-get update -qq && apt-get install -y curl)"},
		// An unreachable cluster is the one thing that turns an upgrade into an outage: the new agent could not rejoin.
		{name: "check-cluster-reachable", cmd: reachabilityCommand(in)},
	}
	if !in.Config.IsZero() {
		steps = append(steps, provisionStep{name: "write-k3s-config", cmd: in.Config.writeConfigCommand()})
	}
	steps = append(steps,
		provisionStep{name: "install-k3s", cmd: installK3sCommand(in)},
		provisionStep{name: "check-k3s-version", cmd: agentVersionCheck(in.K3sVersion)},
	)
	for i := range steps {
		steps[i].cmd = asRoot(in.User, steps[i].cmd)
	}
	return steps
}

// agentVersionCheck waits for the agent service to be running K3s at the version, up to two minutes.
func agentVersionCheck(version string) string {
	q := shellQuote(version)
	return fmt.Sprintf(`for i in $(seq 1 60); do
  if systemctl is-active --quiet k3s-agent && /usr/local/bin/k3s --version 2>/dev/null | grep -qF %[1]s; then exit 0; fi
  sleep 2
done
printf 'the k3s agent is not running K3s %%s: %%s (see journalctl -u k3s-agent)\n' %[1]s "$(/usr/local/bin/k3s --version 2>&1 | head -1)" >&2
exit 1`, q)
}

func (p *sshProvisioner) Upgrade(ctx context.Context, in ProvisionInput) error {
	client, err := dial(ctx, in)
	if err != nil {
		return fmt.Errorf("ssh dial %s: %w", in.Host, err)
	}
	defer func() { _ = client.Close() }()

	for _, step := range upgradeSteps(in) {
		if err := runStep(ctx, client, step.name, step.cmd); err != nil {
			return err
		}
	}
	return nil
}

// reachabilityCommand checks that the cluster's API answers from this node. k3s's /ping needs no
// credentials. A failure says why, from what curl reports: the commonest causes (a stored address no
// other machine can use, a closed port, a name that does not resolve) look the same from k3s's own
// failure, which is a service that restarts forever.
func reachabilityCommand(in ProvisionInput) string {
	var script strings.Builder
	fmt.Fprintf(&script, "out=$(curl -sk --max-time 15 %s 2>/dev/null); rc=$?\n", shellQuote(in.ServerURL+"/ping"))
	script.WriteString("case \"$out\" in *pong*) exit 0 ;; esac\ncase $rc in\n")
	for _, failure := range reachabilityFailures(in) {
		fmt.Fprintf(&script, "%s) why=%s ;;\n", failure.curlExit, shellQuote(failure.message))
	}
	script.WriteString("esac\necho \"$why (curl exit code $rc)\" >&2\nexit 1")
	return script.String()
}

type reachabilityFailure struct {
	// curlExit is a curl exit status, or * for any other.
	curlExit string
	message  string
}

func reachabilityFailures(in ProvisionInput) []reachabilityFailure {
	url := in.ServerURL
	lead := "cannot reach the cluster API at " + url + " from this node: "
	check := "Check that spec.serverURL on the GeassCluster is the control plane's address on a network this node can reach"
	ports := "and that TCP 6443 is open to this node, in the host firewall and in the provider's security group."
	if in.MultiSite {
		ports = strings.TrimSuffix(ports, ".") + ", with UDP " + platform.WireGuardPort + " open between every pair of nodes."
	}

	if platform.IsLoopbackURL(url) {
		// Whatever curl said, this is the cause: no machine but the control plane itself can use this address.
		message := lead + "spec.serverURL on the GeassCluster is a loopback address, which means this node itself to anything " +
			"that connects to it. Set it to the control plane's private address, or its public address when the nodes " +
			"share no private network, then delete this node and create it again."
		return []reachabilityFailure{{"*", message}}
	}
	return []reachabilityFailure{
		{"0", lead + "something answered, but it is not a K3s API server (no pong from /ping). " + check + "."},
		{"6", lead + "the host name does not resolve from here. " + check + "."},
		{"7", lead + "the connection was refused: nothing listens at that address and port, or a firewall rejects it. " + check + ", " + ports},
		{"28", lead + "the connection timed out: a firewall or security group is probably dropping the traffic, or this node has no route to that address. " + check + ", " + ports},
		{"*", lead + check + ", " + ports},
	}
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

func (p *sshProvisioner) Drain(ctx context.Context, in ProvisionInput) error {
	client, err := dial(ctx, in)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	return runStep(ctx, client, "uninstall-k3s", asRoot(in.User, "/usr/local/bin/k3s-agent-uninstall.sh || true"))
}

func dial(_ context.Context, in ProvisionInput) (*gossh.Client, error) {
	signer, err := gossh.ParsePrivateKey(in.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("parse key: %w", err)
	}
	hostKey, err := hostKeyCallback(in.KnownHosts)
	if err != nil {
		return nil, err
	}

	address := in.address()
	cfg := &gossh.ClientConfig{
		User:              in.User,
		Auth:              []gossh.AuthMethod{gossh.PublicKeys(signer)},
		HostKeyCallback:   hostKey,
		HostKeyAlgorithms: hostKeyAlgorithms(in.KnownHosts, address),
		Timeout:           30 * time.Second,
	}
	return gossh.Dial("tcp", address, cfg)
}

// address is where the SSH server is: the host and the port, which is 22 when none is set. An IPv6 host
// may be written with or without the brackets a URL needs.
func (in ProvisionInput) address() string {
	port := in.Port
	if port == 0 {
		port = 22
	}
	host := strings.TrimSuffix(strings.TrimPrefix(in.Host, "["), "]")
	return net.JoinHostPort(host, strconv.Itoa(port))
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
