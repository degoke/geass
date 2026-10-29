// Package mesh is what Geass knows about a cluster whose nodes are at different sites, some of them behind a NAT: which
// mesh carries the traffic, what K3s is told for it, and what is wrong when a cluster, a node or an address does not fit.
//
// It is pure: it reads types and values and renders settings, and never reaches a cluster or a machine. The installer
// uses it to write the control plane's K3s configuration, the SSH provisioner to write a worker's, and the operator to
// refuse a join that cannot work and to say, in a condition, what is wrong with a mesh that is already running.
package mesh

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strings"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const (
	// AuthFile is where a node keeps what K3s needs to join the tailnet: name=tailscale,joinKey=...  K3s reads it
	// through --vpn-auth-file, so the key is in a file only root can read and not in the configuration file.
	AuthFile = "/etc/rancher/k3s/vpn-auth"

	// AuthKeySecretKey is the key, in the Secret that spec.network.mesh.tailscale.authKeySecretRef names, that holds the
	// auth key.
	AuthKeySecretKey = "authKey"

	// ConditionMeshReady is the GeassCluster condition that says whether the mesh is in place on every node. It exists
	// only on a cluster that sets spec.network.mesh.
	ConditionMeshReady = "MeshReady"

	// AnnotationFlannelBackendType is what flannel records on a node: the overlay it runs there.
	AnnotationFlannelBackendType = "flannel.alpha.coreos.com/backend-type"
	flannelBackendWireGuard      = "wireguard"
)

// Settings is the mesh a cluster has, once the two ways of asking for one are read as one.
type Settings struct {
	// Enabled is whether the nodes are at different sites: spec.network.mesh is set, or spec.network.multiSite is true.
	Enabled bool
	// Provider is what carries the traffic. WireGuard when Enabled and nothing else is said. Empty when not Enabled.
	Provider geassv1alpha1.MeshProvider
	// Checked is whether spec.network.mesh is set. Only then does Geass refuse a join that cannot work and report the
	// state of the mesh: a cluster that only sets multiSite behaves exactly as it did before the mesh field existed.
	Checked bool
	// Tailscale is the tailnet settings of a Tailscale mesh.
	Tailscale *geassv1alpha1.GeassMeshTailscale
	// Invalid is why spec.network.mesh cannot be used, empty when it can.
	Invalid string
}

// From reads the mesh a cluster's network section describes.
func From(network geassv1alpha1.GeassClusterNetworkSpec) Settings {
	if network.Mesh == nil {
		if !network.MultiSite {
			return Settings{}
		}
		return Settings{Enabled: true, Provider: geassv1alpha1.MeshProviderWireGuard}
	}
	provider := network.Mesh.Provider
	if provider == "" {
		provider = geassv1alpha1.MeshProviderWireGuard
	}
	settings := Settings{Enabled: true, Provider: provider, Checked: true, Tailscale: network.Mesh.Tailscale}
	if err := Validate(network.Mesh); err != nil {
		settings.Invalid = err.Error()
	}
	return settings
}

// UsesOverlayPort is whether nodes send each other UDP 51820: the WireGuard flannel backend listens there. Tailscale
// opens no port of its own that the cluster has to allow.
func (s Settings) UsesOverlayPort() bool {
	return s.Enabled && s.Provider == geassv1alpha1.MeshProviderWireGuard
}

// IsTailscale is whether nodes join a tailnet.
func (s Settings) IsTailscale() bool {
	return s.Enabled && s.Provider == geassv1alpha1.MeshProviderTailscale
}

// authKeyPattern is what an auth key may be made of. Tailscale's keys (a fixed prefix, then letters, digits and
// dashes) and Headscale's (hexadecimal) are all letters, digits and dashes. Anything else is refused because the key is
// written into a comma separated line K3s parses and into a shell command, and it is nobody's key if it holds a
// comma, a quote or a space.
var authKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{8,256}$`)

// ValidateAuthKey says whether key can be used as an auth key, without repeating it: a refused key may be a real one.
func ValidateAuthKey(key string) error {
	if key == "" {
		return fmt.Errorf("the auth key is empty")
	}
	if !authKeyPattern.MatchString(key) {
		return fmt.Errorf("the auth key is not 8 to 256 letters, digits, dashes and underscores, which is what Tailscale and Headscale keys are made of")
	}
	return nil
}

// Validate checks the mesh section the way the CRD's rules do, for a cluster object that did not pass through them (a
// test, an object made by an older tool), and adds what a rule cannot see: the address of the control server.
func Validate(spec *geassv1alpha1.GeassClusterMeshSpec) error {
	if spec == nil {
		return nil
	}
	switch spec.Provider {
	case "", geassv1alpha1.MeshProviderWireGuard:
		if spec.Tailscale != nil {
			return fmt.Errorf("spec.network.mesh.tailscale is for provider Tailscale, and the provider is WireGuard")
		}
	case geassv1alpha1.MeshProviderTailscale:
		if spec.Tailscale == nil {
			return fmt.Errorf("provider Tailscale needs spec.network.mesh.tailscale, with the Secret that holds the auth key")
		}
		if strings.TrimSpace(spec.Tailscale.AuthKeySecretRef) == "" {
			return fmt.Errorf("spec.network.mesh.tailscale.authKeySecretRef must name the Secret that holds the auth key")
		}
		if err := ValidateControlServerURL(spec.Tailscale.ControlServerURL); err != nil {
			return err
		}
	default:
		return fmt.Errorf("spec.network.mesh.provider %q is not one of WireGuard, Tailscale", spec.Provider)
	}
	return nil
}

// ValidateControlServerURL checks the address of a Headscale server: https, a host, an optional port and path, and
// nothing that could change what the comma separated line K3s reads means.
func ValidateControlServerURL(raw string) error {
	if raw == "" {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return fmt.Errorf("spec.network.mesh.tailscale.controlServerURL must be an https address such as https://headscale.example.com")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.ContainsAny(raw, ", \t\r\n'\"\\$`") {
		return fmt.Errorf("spec.network.mesh.tailscale.controlServerURL must be a plain address: no user name, query, fragment or punctuation")
	}
	return nil
}

// VPNAuth is the line K3s reads from the auth file: the provider, the key, and the control server when there is one.
func VPNAuth(key, controlServerURL string) string {
	line := "name=tailscale,joinKey=" + key
	if controlServerURL != "" {
		line += ",controlServerURL=" + controlServerURL
	}
	return line
}

// WireGuardServerSettings are what a K3s server is told for the WireGuard mesh. They are the settings Geass wrote for a
// multi-site cluster before the mesh field existed, unchanged.
func WireGuardServerSettings(externalIP string) map[string]any {
	return map[string]any{
		"flannel-backend":  platform.WireGuardBackend,
		"node-external-ip": externalIP,
		// Without this flannel builds the tunnel on the node's private address, which the other sites
		// cannot reach.
		"flannel-external-ip": true,
		// Workers join on the public address, so the API server's certificate has to be valid for it.
		"tls-san": []string{externalIP},
	}
}

// TailscaleServerSettings are what a K3s server is told to join a tailnet, as K3s's documentation gives them: the
// auth file, and the tailnet address as the address the node advertises. The flannel backend is left as it is: K3s's
// own integration decides what to run over the tailnet.
func TailscaleServerSettings(tailnetIP string) map[string]any {
	return map[string]any{
		"vpn-auth-file":    AuthFile,
		"node-external-ip": tailnetIP,
		// Workers join on the tailnet address, so the API server's certificate has to be valid for it.
		"tls-san": []string{tailnetIP},
	}
}

// AddressKind says what an address is good for between sites.
type AddressKind int

const (
	// AddressPublic is routed on the internet.
	AddressPublic AddressKind = iota
	// AddressPrivate is a private range (RFC 1918, or an IPv6 unique local address): it means one thing at each site.
	AddressPrivate
	// AddressShared is 100.64.0.0/10, shared address space that is not routed on the internet. It is also where
	// Tailscale, and Headscale by default, put the nodes of a tailnet.
	AddressShared
	// AddressTailnet is a Tailscale IPv6 address (fd7a:115c:a1e0::/48).
	AddressTailnet
	// AddressUnusable is one no other node can use: loopback, unspecified, link-local or multicast.
	AddressUnusable
)

var (
	sharedAddressSpace = netip.MustParsePrefix("100.64.0.0/10")
	tailnetIPv6        = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
)

// Kind classifies an address. It returns false for a string that is not an IP address.
func Kind(address string) (AddressKind, bool) {
	ip, err := netip.ParseAddr(strings.TrimSpace(address))
	if err != nil {
		return AddressUnusable, false
	}
	ip = ip.Unmap()
	switch {
	case ip.IsLoopback(), ip.IsUnspecified(), ip.IsLinkLocalUnicast(), ip.IsLinkLocalMulticast(), ip.IsMulticast():
		return AddressUnusable, true
	case sharedAddressSpace.Contains(ip):
		return AddressShared, true
	case tailnetIPv6.Contains(ip):
		return AddressTailnet, true
	case ip.IsPrivate():
		return AddressPrivate, true
	default:
		return AddressPublic, true
	}
}

// ServerURLHost is the IP address a server URL names, and false when it names a host name (or nothing): a name may
// resolve to anything, so only an address is judged.
func ServerURLHost(serverURL string) (string, bool) {
	parsed, err := url.Parse(serverURL)
	if err != nil {
		return "", false
	}
	host := parsed.Hostname()
	if net.ParseIP(host) == nil {
		return "", false
	}
	return host, true
}

// CheckServerURL says what is wrong with the address workers join the cluster on, in a mesh: an address that is private
// to one site cannot be reached from the others. It returns "" when there is nothing to say, which includes a host name
// and a loopback address (the operator joins a worker on the control plane's own address instead of a loopback one).
func CheckServerURL(settings Settings, serverURL string) string {
	if !settings.Enabled {
		return ""
	}
	host, ok := ServerURLHost(serverURL)
	if !ok {
		return ""
	}
	kind, _ := Kind(host)
	switch kind {
	case AddressPrivate:
		return fmt.Sprintf("spec.serverURL is %s, a private address: it means one thing at each site, so nodes at other sites cannot reach "+
			"the control plane on it. Set it to the control plane's %s", serverURL, reachableAddress(settings))
	case AddressShared:
		if settings.Provider != geassv1alpha1.MeshProviderTailscale {
			return fmt.Sprintf("spec.serverURL is %s, in 100.64.0.0/10, which the internet does not route: only a node on the same carrier "+
				"or tailnet network can reach it. Set it to the control plane's public address", serverURL)
		}
	}
	return ""
}

func reachableAddress(settings Settings) string {
	if settings.Provider == geassv1alpha1.MeshProviderTailscale {
		return "tailnet address (the one `tailscale ip -4` prints on it)"
	}
	return "public address (the one other sites send UDP 51820 to)"
}

// CheckAdvertisedAddress says whether the address a worker would advertise can be used in this mesh. It returns "" when
// it can, and why not when it cannot: the join must not go on, because the node would register and be isolated.
// A Tailscale node advertises its tailnet address, which it finds out for itself, so only a WireGuard mesh checks.
func CheckAdvertisedAddress(settings Settings, address string) string {
	if !settings.Checked || settings.Provider != geassv1alpha1.MeshProviderWireGuard {
		return ""
	}
	if strings.TrimSpace(address) == "" {
		return "the node has no address to advertise: set spec.eip to the address other sites send UDP 51820 to"
	}
	kind, ok := Kind(address)
	switch {
	case !ok:
		return fmt.Sprintf("%q is not an IP address, and the node advertises its address as an IP address in a WireGuard mesh: set spec.eip to an IP address", address)
	case kind == AddressUnusable:
		return fmt.Sprintf("%s cannot be reached from another machine (it is loopback, unspecified, link-local or multicast): set spec.eip to the address other sites send UDP 51820 to", address)
	}
	return ""
}
