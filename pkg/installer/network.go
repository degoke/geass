package installer

import (
	"fmt"
	"net"
	"os"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/degoke/geass/pkg/platform"
)

const k3sConfigPath = "/etc/rancher/k3s/config.yaml"

// Network is how the cluster's nodes reach each other.
//
// By default they share a private network and k3s's VXLAN overlay carries the traffic. A multi-site
// cluster has nodes at different providers that only reach each other over the internet, so the overlay
// has to be encrypted: k3s's WireGuard backend, with every node advertising its public IP. Switching an
// existing cluster means restarting every node, so it is chosen at install time.
type Network struct {
	MultiSite bool
	// ExternalIP is this node's public address, which other sites reach it on. Required for multi-site.
	ExternalIP string
}

// NetworkFromEnv reads the choice from the environment, as the installer's other switches are:
// GEASS_MULTI_SITE turns it on and GEASS_NODE_EXTERNAL_IP names this node's public address.
func NetworkFromEnv() Network {
	return Network{
		MultiSite:  envEnabled("GEASS_MULTI_SITE"),
		ExternalIP: strings.TrimSpace(os.Getenv("GEASS_NODE_EXTERNAL_IP")),
	}
}

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
	if !n.MultiSite {
		return nil
	}
	if n.ExternalIP == "" {
		return fmt.Errorf("a multi-site cluster needs this node's public address: set GEASS_NODE_EXTERNAL_IP")
	}
	if net.ParseIP(n.ExternalIP) == nil {
		return fmt.Errorf("GEASS_NODE_EXTERNAL_IP %q is not an IP address", n.ExternalIP)
	}
	return nil
}

// ServerURL is the address workers join the cluster on when the network dictates it. A multi-site
// cluster's workers are elsewhere, so it is the control plane's public address. Otherwise it is empty and
// the caller uses this machine's own address on the network the workers share with it: see
// controlPlaneAddress.
func (n Network) ServerURL() string {
	if n.MultiSite && n.ExternalIP != "" {
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
	if n.MultiSite {
		config["flannel-backend"] = platform.WireGuardBackend
		config["node-external-ip"] = n.ExternalIP
		// Without this flannel builds the tunnel on the node's private address, which the other sites
		// cannot reach.
		config["flannel-external-ip"] = true
		// Workers join on the public address, so the API server's certificate has to be valid for it.
		config["tls-san"] = []string{n.ExternalIP}
	}
	return config
}
