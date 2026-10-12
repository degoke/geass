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

// ServerURL is the address workers join the cluster on. A multi-site cluster's workers are elsewhere,
// so it is the control plane's public address; otherwise the loopback default applies.
func (n Network) ServerURL() string {
	if n.MultiSite && n.ExternalIP != "" {
		return "https://" + net.JoinHostPort(n.ExternalIP, "6443")
	}
	return ""
}

// ServerConfig is the k3s server's configuration file.
func (n Network) ServerConfig() string {
	config := map[string]any{
		"kubelet-arg": platform.KubeletLogArgs,
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
	raw, err := yaml.Marshal(config)
	if err != nil {
		return ""
	}
	return string(raw)
}
