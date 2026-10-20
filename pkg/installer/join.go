package installer

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/degoke/geass/pkg/platform"
	"github.com/degoke/geass/pkg/release"
)

// An extra control plane server is a machine that joins the cluster's K3s as a server, so that the cluster has three
// copies of its database (K3s's embedded etcd) and three API servers, and survives the loss of one. The first server's
// install already runs etcd (--cluster-init), which is what makes this possible: nothing about it has to change.
//
// The installer does it, on the machine that is to join, and not the operator over SSH like a worker, because only the
// installer has the operator's image: it ships inside it. A server that took over from the first one without the
// image could not run the operator, and the operator is what runs the cluster.

const (
	// k3sServerTokenPath is where the first server keeps the token that lets another server join.
	k3sServerTokenPath = "/var/lib/rancher/k3s/server/token"

	// etcdClientPort and etcdPeerPort are what the servers use to reach each other's database. They are never
	// opened on the first server by its own install: they are not needed until a second server joins.
	etcdClientPort = "2379"
	etcdPeerPort   = "2380"
)

// JoinOptions says which cluster this machine joins as an additional control plane server.
type JoinOptions struct {
	// ServerURL is an existing server's address, https://host:6443.
	ServerURL string
	// Token is that cluster's server token (/var/lib/rancher/k3s/server/token on a server).
	Token string
}

// Validate checks that the options say where to join and with what.
func (o JoinOptions) Validate() error {
	address, err := normalizeJoinURL(o.ServerURL)
	if err != nil {
		return err
	}
	if parsed, _ := url.Parse(address); parsed != nil && isOwnAddress(parsed.Hostname()) {
		return fmt.Errorf("%q is this machine's own address: give the address of the cluster's first server", o.ServerURL)
	}
	if strings.TrimSpace(o.Token) == "" {
		return fmt.Errorf("joining a cluster needs its server token: set GEASS_JOIN_TOKEN, or pass --join-token. " +
			"It is in " + k3sServerTokenPath + " on the cluster's first server")
	}
	return nil
}

// isOwnAddress reports whether a host can only mean the machine that is asking, which is never where a cluster is.
func isOwnAddress(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsUnspecified())
}

// normalizeJoinURL returns the address of a server as https://host:port, with the port K3s serves on when none is
// given, and refuses what is not an address a machine can join.
func normalizeJoinURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("joining a cluster needs the address of one of its servers, such as https://10.0.0.5:6443")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" {
		return "", fmt.Errorf("%q is not a server address: use https://host:6443", raw)
	}
	if parsed.Scheme != "https" {
		return "", fmt.Errorf("%q is not an https address: K3s servers are joined over https", raw)
	}
	if parsed.Path != "" && parsed.Path != "/" || parsed.RawQuery != "" || parsed.User != nil {
		return "", fmt.Errorf("%q has more than a host and a port: use https://host:6443", raw)
	}
	port := parsed.Port()
	if port == "" {
		port = platform.ClusterAPIPort
	}
	return "https://" + net.JoinHostPort(parsed.Hostname(), port), nil
}

// JoinPlan carries what the steps of a join share: what to join, and the K3s version the new server installs, which
// has to be the one the cluster runs.
type JoinPlan struct {
	Join JoinOptions
	// K3sVersion is the release the cluster runs, given with --k3s-version or GEASS_K3S_VERSION. A machine that has not
	// joined has no way to ask the cluster: K3s answers /version only to a client it knows, and the join token is not
	// one. So it is asked for, and checked afterwards, when the server has joined and the cluster can say.
	K3sVersion string
	// Version is the release that is installed: K3sVersion, once the checks have accepted it.
	Version string
}

// k3sVersionHelp says where the version is, for the message of a join that was not told it.
const k3sVersionHelp = "run `k3s --version` on the cluster's first server, or `sudo k3s kubectl get nodes`, " +
	"and pass the release that it says, such as v1.36.5+k3s1: --k3s-version, or GEASS_K3S_VERSION"

// JoinChecks checks the machine and the cluster before anything is changed.
type JoinChecks struct {
	Plan *JoinPlan
	// HTTP is how the cluster is asked its version; a client that does not verify its certificate when nil.
	HTTP *http.Client
	// Dial is how the ports between servers are tried; net.DialTimeout when nil.
	Dial func(network, address string, timeout time.Duration) (net.Conn, error)
}

func (s *JoinChecks) Name() string { return "system checks (joining a cluster)" }

func (s *JoinChecks) Run() error {
	if err := s.Plan.Join.Validate(); err != nil {
		return err
	}
	if envEnabled("GEASS_MULTI_SITE") {
		return fmt.Errorf("GEASS_MULTI_SITE is for the first server of a cluster. A multi-site cluster keeps its control plane at one site, " +
			"and servers at other sites would carry etcd's traffic across the internet")
	}
	if err := (&SystemChecks{Network: Network{}}).Run(); err != nil {
		return err
	}
	if err := s.resolveVersion(); err != nil {
		return err
	}
	return s.checkPeerPorts()
}

// checkPeerPorts tries the two ports a server joins through, on the server it was pointed at. They are closed unless
// someone opened them, because nothing needed them before this, and a join that cannot reach them does not fail:
// K3s waits for etcd for a long time, and what it logs does not say that a firewall is what is in the way.
func (s *JoinChecks) checkPeerPorts() error {
	address, err := normalizeJoinURL(s.Plan.Join.ServerURL)
	if err != nil {
		return err
	}
	parsed, _ := url.Parse(address)
	dial := s.Dial
	if dial == nil {
		dial = net.DialTimeout
	}
	var closed []string
	for _, port := range []string{etcdClientPort, etcdPeerPort} {
		conn, err := dial("tcp", net.JoinHostPort(parsed.Hostname(), port), 5*time.Second)
		if err != nil {
			closed = append(closed, port)
			continue
		}
		_ = conn.Close()
	}
	if len(closed) > 0 {
		return fmt.Errorf("this machine cannot reach TCP %s on %s, which servers use to reach each other's database. "+
			"Open TCP %s and %s between the servers, in the host firewall (ufw allow %s/tcp) and in the provider's security group, "+
			"on the server being joined and on every other server. Only a cluster that was built with the installer runs etcd, "+
			"which is what these ports are for: a K3s that was not started with --cluster-init cannot take a second server",
			strings.Join(closed, " and "), parsed.Hostname(), etcdClientPort, etcdPeerPort, etcdClientPort)
	}
	Logf(s.Name(), "Can reach the database ports (%s, %s) on %s", etcdClientPort, etcdPeerPort, parsed.Hostname())
	return nil
}

// resolveVersion takes the K3s version the join was told, and checks that it is a release and that the cluster is
// there to join.
func (s *JoinChecks) resolveVersion() error {
	address, err := normalizeJoinURL(s.Plan.Join.ServerURL)
	if err != nil {
		return err
	}
	version := strings.TrimSpace(s.Plan.K3sVersion)
	if version == "" {
		return fmt.Errorf("a server has to run the same K3s as the others, and a machine that has not joined cannot ask the "+
			"cluster which that is. To find it, %s", k3sVersionHelp)
	}
	if !release.ValidK3s(version) {
		return fmt.Errorf("%q is not a K3s release: use the full name, such as v1.36.5+k3s1 (a channel name is not accepted)", version)
	}
	if err := clusterReachable(s.HTTP, address); err != nil {
		return err
	}
	Logf(s.Name(), "The cluster at %s answers, and this server will run K3s %s", address, version)
	s.Plan.Version = version
	return nil
}

// clusterReachable asks a server whether it is a K3s server. /ping answers without credentials, which is why the check
// is the same one a worker makes before it joins, and why nothing more than reachability can be learnt this way. The
// certificate it presents is signed by a CA this machine has never heard of, and nothing secret is sent: K3s checks it
// against the token when the join is made.
func clusterReachable(client *http.Client, address string) error {
	if client == nil {
		client = &http.Client{
			Timeout:   15 * time.Second,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // see above
		}
	}
	response, err := client.Get(address + "/ping")
	if err != nil {
		return fmt.Errorf("cannot reach the cluster at %s from this machine: %w. "+
			"Check the address, and that TCP %s is open to this machine, in the host firewall and in the provider's security group",
			address, err, platform.ClusterAPIPort)
	}
	defer func() { _ = response.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 64))
	if !strings.Contains(string(body), "pong") {
		return fmt.Errorf("something answered at %s, but it is not a K3s server (no pong from /ping)", address)
	}
	return nil
}

// K3sServerJoin installs K3s on this machine as a server of the cluster it was pointed at.
type K3sServerJoin struct {
	Plan *JoinPlan
	// run is how the install script is run; the real one when nil. For tests.
	run func(env, envLog []string, script []byte) error
}

func (s *K3sServerJoin) Name() string { return "install k3s (joining as a server)" }

// joinServerConfig is the configuration file of a server that joins: the first server's settings, and where the
// cluster is and the token to join it with. K3s reads them from the file, which is why running the install script
// again later (to upgrade K3s) needs neither, and why the token is not in an environment variable or on a command
// line, where it would be on show. The file is readable by root only.
func joinServerConfig(plan *JoinPlan) (string, error) {
	address, err := normalizeJoinURL(plan.Join.ServerURL)
	if err != nil {
		return "", err
	}
	settings := Network{}.ServerSettings()
	settings["server"] = address
	settings["token"] = strings.TrimSpace(plan.Join.Token)
	raw, err := yaml.Marshal(settings)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// joinEnvironment is what the K3s install script is run with: a server, at the version the cluster runs.
func joinEnvironment(base []string, plan *JoinPlan) (env, envLog []string, err error) {
	if plan.Version == "" {
		return nil, nil, fmt.Errorf("the cluster's K3s version is not known yet: the checks have not run")
	}
	// A variable the person running the installer happens to have set must not change what is installed: a K3S_URL would
	// make the script install an agent.
	env = append(environWithout(base, "INSTALL_K3S_EXEC", "INSTALL_K3S_VERSION", "K3S_URL", "K3S_TOKEN", "K3S_TOKEN_FILE"),
		"INSTALL_K3S_EXEC=server", "INSTALL_K3S_VERSION="+plan.Version)
	return env, []string{"INSTALL_K3S_EXEC=server", "INSTALL_K3S_VERSION=" + plan.Version}, nil
}

func (s *K3sServerJoin) Run() error {
	env, envLog, err := joinEnvironment(os.Environ(), s.Plan)
	if err != nil {
		return err
	}
	config, err := joinServerConfig(s.Plan)
	if err != nil {
		return err
	}
	if err := writeK3sConfig(s.Name(), config); err != nil {
		return err
	}
	script, err := outputCommand(s.Name(), "curl", "-sfL", "https://get.k3s.io")
	if err != nil {
		return fmt.Errorf("download k3s installer: %w", err)
	}
	Logf(s.Name(), "Downloaded k3s installer script (%d bytes)", len(script))
	if s.run != nil {
		return s.run(env, envLog, script)
	}
	return runCommandWithOptions(s.Name(), commandOptions{
		env: env, envLog: envLog, stdin: bytes.NewReader(script), echoStdout: true, echoStderr: true,
	}, "sh", "-s", "-")
}

// WaitForServerJoin waits until this machine's node is in the cluster, ready, and a control plane node.
type WaitForServerJoin struct {
	Timeout time.Duration
	// Version is the K3s release this server was installed at, which the cluster has to agree it runs. Empty skips the check.
	Version string
	// hostname and nodes are how it looks, for tests.
	hostname func() (string, error)
	nodes    func(step string) ([]nodeInfo, error)
}

func (s *WaitForServerJoin) Name() string { return "wait for this server to join" }

func (s *WaitForServerJoin) Run() error {
	hostname := s.hostname
	if hostname == nil {
		hostname = os.Hostname
	}
	nodes := s.nodes
	if nodes == nil {
		nodes = readNodes
	}
	timeout := s.Timeout
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	name, err := hostname()
	if err != nil {
		return fmt.Errorf("cannot tell this machine's name: %w", err)
	}
	// K3s names a node after its host, in lower case.
	name = strings.ToLower(name)
	deadline := time.Now().Add(timeout)
	var last error
	for {
		list, err := nodes(s.Name())
		if err != nil {
			last = err
		} else {
			last = fmt.Errorf("node %q is not in the cluster yet", name)
			for _, node := range list {
				if node.Name != name {
					continue
				}
				if node.ControlPlane && node.Ready {
					Logf(s.Name(), "Node %s is a ready control plane node (%s)", node.Name, node.Version)
					if want := strings.TrimSpace(s.Version); want != "" && node.Version != "" && node.Version != want {
						return fmt.Errorf("this server was installed at K3s %s, and its node reports %s. The release that is installed is the one asked for: "+
							"look at `journalctl -u k3s` and at what INSTALL_K3S_VERSION was in the environment of the install script", want, node.Version)
					}
					return s.checkAgainstTheOthers(node, list)
				}
				last = fmt.Errorf("node %q is in the cluster (control plane: %v, ready: %v)", name, node.ControlPlane, node.Ready)
			}
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("this server did not join within %s: %v. Look at `journalctl -u k3s`: a server whose settings or K3s "+
				"version differ from the others, a closed port (TCP %s, %s and %s between the servers) and a wrong token are the usual causes",
				timeout, last, platform.ClusterAPIPort, etcdClientPort, etcdPeerPort)
		}
		fmt.Println("  waiting for this server to join the cluster...")
		time.Sleep(pollInterval)
	}
}

// checkAgainstTheOthers says what the cluster knows now and the join could not: whether this server runs the K3s that
// the other control plane servers do. It is the first moment the cluster can say, and a server that differs is a real
// problem, reported here with what to do about it and not left to show up later as an upgrade that will not go.
func (s *WaitForServerJoin) checkAgainstTheOthers(this nodeInfo, all []nodeInfo) error {
	var others []string
	for _, node := range all {
		if node.Name != this.Name && node.ControlPlane && node.Version != "" && node.Version != this.Version {
			others = append(others, node.Name+" runs "+node.Version)
		}
	}
	if len(others) == 0 {
		return nil
	}
	return fmt.Errorf("this server runs K3s %s, and the cluster's other control plane servers do not: %s. A server has to run the same "+
		"K3s as the others. Install the release they run on this machine (curl -sfL https://get.k3s.io | sudo env "+
		"INSTALL_K3S_VERSION=<release> INSTALL_K3S_EXEC=server sh -), or upgrade them to this one one at a time",
		this.Version, strings.Join(others, ", "))
}

// JoinServerSteps makes this machine an additional control plane server of an existing cluster.
func JoinServerSteps(opts Options) []Step {
	// The version is asked for, from the option or from GEASS_K3S_VERSION: unlike a first server, which takes the current stable
	// K3s when it is not told, a joining one has to run what the cluster runs, and cannot find out before it has joined.
	plan := &JoinPlan{Join: JoinOptions{ServerURL: opts.JoinServer, Token: opts.JoinToken}, K3sVersion: opts.K3sVersion}
	// The servers of a cluster each take snapshots, and each has to be told where to send them: a server that joins
	// does what the others do when it is asked to, with --etcd-snapshots.
	snapshots := &EtcdSnapshotsChoice{}
	return []Step{
		// The machine, the address and token, and the K3s version the cluster runs.
		&JoinChecks{Plan: plan},
		// The cluster's ports, and the two that servers use to reach each other's database.
		&FirewallSetup{Network: Network{}, ServerPeer: true},
		// The iSCSI initiator, which Longhorn needs on every node.
		&NodePrerequisites{},
		&AskEtcdSnapshots{Choice: snapshots, Flag: opts.EtcdSnapshots.Enable, K3sVersion: opts.K3sVersion, NoPrompt: true},
		&WriteEtcdSnapshotsDropIn{Choice: snapshots},
		&K3sServerJoin{Plan: plan},
		&WaitForK3s{},
		&WaitForServerJoin{Version: strings.TrimSpace(plan.K3sVersion)},
		// The point of joining from here: the operator's image on this server, so that the operator can run on it
		// when the first one is gone.
		&ImageImport{},
	}
}

// RefreshServerSteps is what a server that joined an existing cluster is asked to do when the installer is run on it
// again: keep the operator's image current. An update is made on the first server. It leaves the operator's
// Deployment naming a new image, and this is how that image gets onto the other control plane servers.
func RefreshServerSteps() []Step {
	return []Step{&ImageImport{}}
}

// joinedServerConfig reports whether a K3s configuration file is that of a server that joined a cluster, and did not
// start one: it says where the cluster is with "server". The first server's file never does.
func joinedServerConfig(content string) bool {
	var settings map[string]any
	if err := yaml.Unmarshal([]byte(content), &settings); err != nil {
		return false
	}
	server, _ := settings["server"].(string)
	return strings.TrimSpace(server) != ""
}
