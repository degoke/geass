package installer

import (
	"fmt"
	"maps"
	"net"
	"os"
	"os/exec"
	"strings"

	"sigs.k8s.io/yaml"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/mesh"
	"github.com/degoke/geass/pkg/platform"
)

const k3sConfigPath = "/etc/rancher/k3s/config.yaml"

// Network is how the cluster's nodes reach each other.
//
// By default they share a private network and k3s's VXLAN overlay carries the traffic. A multi-site
// cluster has nodes at different providers that only reach each other over the internet, so the overlay
// has to be encrypted: k3s's WireGuard backend, with every node advertising its public IP. Switching an
// existing cluster means restarting every node, so it is chosen at install time.
//
// GEASS_MESH is the newer way to ask for it, and the only way to ask for a Tailscale mesh. GEASS_MESH=wireguard is
// GEASS_MULTI_SITE=1 and also records spec.network.mesh on the cluster, which turns on the operator's checks of the mesh.
// GEASS_MESH=tailscale needs this machine to be on the tailnet already (the installer reads its address from tailscale)
// and an auth key in a file, which is what the other nodes join with.
type Network struct {
	// MultiSite is a WireGuard mesh: GEASS_MULTI_SITE, or GEASS_MESH=wireguard. It is false for a Tailscale mesh, which
	// has no use for the WireGuard module or its port.
	MultiSite bool
	// ExternalIP is this node's public address, which other sites reach it on, or its address on the tailnet. Required
	// for multi-site.
	ExternalIP string
	// Mesh is set when the cluster is built with GEASS_MESH: it is then recorded on the cluster object. It is empty for
	// a cluster that asked for nothing, or only for GEASS_MULTI_SITE.
	Mesh geassv1alpha1.MeshProvider
	// Tailscale is what a Tailscale mesh is joined with.
	Tailscale TailscaleNetwork

	// problem is why the environment could not be turned into a network, reported by Validate: the installer's
	// steps are built before any of them runs.
	problem error
}

// TailscaleNetwork is what the control plane and every node join a tailnet with.
type TailscaleNetwork struct {
	// AuthKey is the key, read from GEASS_TAILSCALE_AUTH_KEY_FILE. It is a secret: it is never logged.
	AuthKey string
	// ControlServerURL is a Headscale server's address, from GEASS_TAILSCALE_CONTROL_URL. Empty means Tailscale's own.
	ControlServerURL string
}

// tailnetIPv4 is how the installer learns this machine's address on the tailnet. A variable so that a test can say.
var tailnetIPv4 = func() (string, error) {
	out, err := exec.Command("tailscale", "ip", "-4").Output()
	if err != nil {
		return "", err
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(line), nil
}

// NetworkFromEnv reads the choice from the environment, as the installer's other switches are:
// GEASS_MULTI_SITE turns the WireGuard mesh on and GEASS_NODE_EXTERNAL_IP names this node's public address. GEASS_MESH
// is wireguard or tailscale, with GEASS_TAILSCALE_AUTH_KEY_FILE and GEASS_TAILSCALE_CONTROL_URL for the second.
func NetworkFromEnv() Network {
	network := Network{
		MultiSite:  envEnabled("GEASS_MULTI_SITE"),
		ExternalIP: strings.TrimSpace(os.Getenv("GEASS_NODE_EXTERNAL_IP")),
	}
	switch choice := strings.ToLower(strings.TrimSpace(os.Getenv("GEASS_MESH"))); choice {
	case "":
	case "wireguard":
		network.Mesh = geassv1alpha1.MeshProviderWireGuard
		network.MultiSite = true
	case "tailscale":
		network.Mesh = geassv1alpha1.MeshProviderTailscale
		if network.MultiSite {
			network.problem = fmt.Errorf("GEASS_MULTI_SITE is the WireGuard mesh: leave it unset with GEASS_MESH=tailscale")
			return network
		}
		network.readTailscale()
	default:
		network.problem = fmt.Errorf("GEASS_MESH %q is not one of wireguard, tailscale", os.Getenv("GEASS_MESH"))
	}
	return network
}

// readTailscale reads what a Tailscale mesh needs from the environment and this machine: the auth key from its file and
// the address this machine has on the tailnet, unless GEASS_NODE_EXTERNAL_IP names one.
func (n *Network) readTailscale() {
	n.Tailscale.ControlServerURL = strings.TrimSpace(os.Getenv("GEASS_TAILSCALE_CONTROL_URL"))
	if err := mesh.ValidateControlServerURL(n.Tailscale.ControlServerURL); err != nil {
		n.problem = fmt.Errorf("GEASS_TAILSCALE_CONTROL_URL: %w", err)
		return
	}
	file := strings.TrimSpace(os.Getenv("GEASS_TAILSCALE_AUTH_KEY_FILE"))
	if file == "" {
		n.problem = fmt.Errorf("a Tailscale mesh needs an auth key: put one in a file that only root can read and set GEASS_TAILSCALE_AUTH_KEY_FILE to its path")
		return
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		n.problem = fmt.Errorf("read GEASS_TAILSCALE_AUTH_KEY_FILE: %w", err)
		return
	}
	n.Tailscale.AuthKey = strings.TrimSpace(string(raw))
	if err := mesh.ValidateAuthKey(n.Tailscale.AuthKey); err != nil {
		n.problem = fmt.Errorf("GEASS_TAILSCALE_AUTH_KEY_FILE %s: %w", file, err)
		return
	}
	if n.ExternalIP != "" {
		return
	}
	ip, err := tailnetIPv4()
	if err != nil || ip == "" {
		n.problem = fmt.Errorf("this machine has no address on the tailnet. Install Tailscale on it and bring it up with `tailscale up` first (the cluster's " +
			"address is the one `tailscale ip -4` prints), or set GEASS_NODE_EXTERNAL_IP to it")
		return
	}
	n.ExternalIP = ip
}

// Enabled is whether the nodes are at different sites.
func (n Network) Enabled() bool { return n.MultiSite || n.Mesh == geassv1alpha1.MeshProviderTailscale }

// IsTailscale is whether the nodes join a tailnet.
func (n Network) IsTailscale() bool { return n.Mesh == geassv1alpha1.MeshProviderTailscale }

func envEnabled(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// Validate checks that a multi-site install has what it needs.
func (n Network) Validate() error {
	if n.problem != nil {
		return n.problem
	}
	if !n.Enabled() {
		return nil
	}
	if n.ExternalIP == "" {
		return fmt.Errorf("a multi-site cluster needs this node's public address: set GEASS_NODE_EXTERNAL_IP")
	}
	if net.ParseIP(n.ExternalIP) == nil {
		return fmt.Errorf("GEASS_NODE_EXTERNAL_IP %q is not an IP address", n.ExternalIP)
	}
	if n.Mesh == "" {
		// A cluster that asks only for GEASS_MULTI_SITE is installed as it always was.
		return nil
	}
	return n.checkAddress()
}

// checkAddress refuses an address no other site can use, and says so when it is one that only some can. A loopback
// or link-local address never works. A private one works only between sites that route to each other, which is not
// what a mesh is for, so it is allowed with a warning; so is one on 100.64.0.0/10 outside a tailnet.
func (n Network) checkAddress() error {
	kind, _ := mesh.Kind(n.ExternalIP)
	switch kind {
	case mesh.AddressUnusable:
		return fmt.Errorf("%s cannot be reached from another machine (it is loopback, unspecified, link-local or multicast): "+
			"set GEASS_NODE_EXTERNAL_IP to the address other sites reach this machine on", n.ExternalIP)
	case mesh.AddressPrivate:
		Warnf("system checks", "%s is a private address. It means one thing at each site, so nodes at other sites reach this machine on it only "+
			"if their networks route to each other. A mesh is for sites that do not: use the address the other sites can send traffic to "+
			"(a public one, or the one on the tailnet)", n.ExternalIP)
	case mesh.AddressShared:
		if !n.IsTailscale() {
			Warnf("system checks", "%s is in 100.64.0.0/10, which the internet does not route: only a node on the same carrier or tailnet network "+
				"can reach it. Use this machine's public address, or GEASS_MESH=tailscale", n.ExternalIP)
		}
	case mesh.AddressPublic:
		if warning := externalAddressNotOnThisMachine(n.ExternalIP); warning != "" && !n.IsTailscale() {
			Warnf("system checks", "%s", warning)
		}
	}
	return nil
}

// interfaceAddresses is the addresses on this machine's network interfaces. It is a variable so that a test can say what
// the machine has.
var interfaceAddresses = net.InterfaceAddrs

// externalAddressNotOnThisMachine is the warning for an external address that no network interface of this machine
// has, and empty when it has it or the machine cannot say. A cloud that maps a public address to a private one (an AWS
// Elastic IP) puts it on no interface and, seen on EC2, does not send a machine's own traffic to it back to the machine.
// K3s advertises the API server on the external address, so the pods on this machine reach the API through an address
// they cannot reach: the operator restarts in a loop and the installer stops waiting for it. With the address on the
// loopback interface the traffic stays on the machine.
func externalAddressNotOnThisMachine(address string) string {
	want := net.ParseIP(address)
	if want == nil {
		return ""
	}
	addrs, err := interfaceAddresses()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		switch a := addr.(type) {
		case *net.IPNet:
			if a.IP.Equal(want) {
				return ""
			}
		case *net.IPAddr:
			if a.IP.Equal(want) {
				return ""
			}
		}
	}
	return fmt.Sprintf("%[1]s is not an address of any network interface of this machine (a cloud that maps public addresses to private ones, such as an "+
		"AWS Elastic IP, does not put it there). The API server is advertised on it, and on EC2 a machine's own traffic to its public address does "+
		"not come back, so the pods on this machine cannot reach the API and the operator restarts in a loop. Add the address to the loopback "+
		"interface before installing, so that the traffic stays here: `ip addr add %[1]s/32 dev lo`, and make that persistent, or it is lost at "+
		"the next reboot. Ignore this if your network does send the traffic back", address)
}

// ServerURL is the address workers join the cluster on when the network dictates it. A multi-site
// cluster's workers are elsewhere, so it is the control plane's public address. Otherwise it is empty and
// the caller uses this machine's own address on the network the workers share with it: see
// controlPlaneAddress.
func (n Network) ServerURL() string {
	if n.Enabled() && n.ExternalIP != "" {
		return serverURLFor(n.ExternalIP)
	}
	return ""
}

func serverURLFor(host string) string {
	return "https://" + net.JoinHostPort(host, platform.ClusterAPIPort)
}

// controlPlaneAddress is this machine's address on the network workers will reach it over: the source
// address the kernel picks for traffic leaving by the default route. That is also the address k3s
// registers the node under, so it is in the API server's certificate. It is a private address on a cloud
// VM, and the public one on a server that has its public address on the interface.
//
// Connecting a UDP socket sends nothing: it only makes the kernel choose a route and a source address.
func controlPlaneAddress() (string, error) {
	var lastErr error
	for _, probe := range []struct{ network, target string }{
		{"udp4", "192.0.2.1:9"},     // TEST-NET-1: never routed anywhere, so nothing real is contacted
		{"udp6", "[2001:db8::1]:9"}, // documentation prefix, for a host with only IPv6
	} {
		conn, err := net.Dial(probe.network, probe.target)
		if err != nil {
			lastErr = err
			continue
		}
		udp, ok := conn.LocalAddr().(*net.UDPAddr)
		_ = conn.Close()
		if !ok || udp.IP.IsLoopback() || udp.IP.IsUnspecified() || udp.IP.IsLinkLocalUnicast() {
			continue
		}
		return udp.IP.String(), nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no usable address on the default route")
	}
	return "", lastErr
}

// ServerConfig is the k3s server's configuration file.
func (n Network) ServerConfig() string {
	raw, err := yaml.Marshal(n.ServerSettings())
	if err != nil {
		return ""
	}
	return string(raw)
}

// ServerSettings are the settings in the k3s server's configuration file. A server that joins a cluster has to have
// the same ones as the first, because K3s refuses one whose critical settings differ, and adds to them where it is.
func (n Network) ServerSettings() map[string]any {
	config := map[string]any{
		"kubelet-arg": platform.KubeletLogArgs,
		// Every Secret in the cluster is stored encrypted in K3s's datastore, and so is in the etcd snapshots the
		// installer takes before an upgrade. Without this they hold the dashboard users, cloud keys and database
		// passwords in the clear.
		"secrets-encryption": true,
	}
	switch {
	case n.IsTailscale():
		maps.Copy(config, mesh.TailscaleServerSettings(n.ExternalIP))
	case n.MultiSite:
		maps.Copy(config, mesh.WireGuardServerSettings(n.ExternalIP))
	}
	return config
}
