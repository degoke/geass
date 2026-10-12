package installer

import (
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestSingleSiteClusterKeepsTheDefaultOverlay(t *testing.T) {
	var config map[string]any
	if err := yaml.Unmarshal([]byte(Network{}.ServerConfig()), &config); err != nil {
		t.Fatal(err)
	}
	if _, ok := config["flannel-backend"]; ok {
		t.Fatalf("a single-site cluster must keep k3s's default VXLAN overlay: %v", config)
	}
	if _, ok := config["node-external-ip"]; ok {
		t.Fatalf("nothing extra is advertised: %v", config)
	}
	if got := config["kubelet-arg"]; got == nil {
		t.Fatal("every cluster keeps more container logs")
	}
}

func TestMultiSiteServerUsesAnEncryptedOverlayOnItsPublicAddress(t *testing.T) {
	network := Network{MultiSite: true, ExternalIP: "203.0.113.1"}
	var config map[string]any
	if err := yaml.Unmarshal([]byte(network.ServerConfig()), &config); err != nil {
		t.Fatal(err)
	}
	if config["flannel-backend"] != "wireguard-native" {
		t.Fatalf("flannel-backend = %v, want wireguard-native: VXLAN is not encrypted and would cross the internet in plaintext", config["flannel-backend"])
	}
	if config["node-external-ip"] != "203.0.113.1" || config["flannel-external-ip"] != true {
		t.Fatalf("the public address must be advertised and used for the tunnel: %v", config)
	}
	sans, _ := config["tls-san"].([]any)
	if len(sans) != 1 || sans[0] != "203.0.113.1" {
		t.Fatalf("workers join on the public address, so the API certificate must cover it: %v", config["tls-san"])
	}
}

func TestMultiSiteNeedsAValidPublicAddress(t *testing.T) {
	if err := (Network{}).Validate(); err != nil {
		t.Fatalf("single-site needs nothing: %v", err)
	}
	if err := (Network{MultiSite: true}).Validate(); err == nil || !strings.Contains(err.Error(), "GEASS_NODE_EXTERNAL_IP") {
		t.Fatalf("a missing address must say how to set it: %v", err)
	}
	if err := (Network{MultiSite: true, ExternalIP: "not-an-ip"}).Validate(); err == nil {
		t.Fatal("a hostname or typo must be refused: it ends up in the API certificate and the join address")
	}
	if err := (Network{MultiSite: true, ExternalIP: "203.0.113.1"}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Network{MultiSite: true, ExternalIP: "2001:db8::1"}).Validate(); err != nil {
		t.Fatalf("IPv6 is a valid address: %v", err)
	}
}

func TestWorkersJoinAMultiSiteClusterOnItsPublicAddress(t *testing.T) {
	if got := (Network{MultiSite: true, ExternalIP: "203.0.113.1"}).ServerURL(); got != "https://203.0.113.1:6443" {
		t.Fatalf("ServerURL() = %q", got)
	}
	if got := (Network{MultiSite: true, ExternalIP: "2001:db8::1"}).ServerURL(); got != "https://[2001:db8::1]:6443" {
		t.Fatalf("an IPv6 address needs brackets in a URL: %q", got)
	}
	if got := (Network{}).ServerURL(); got != "" {
		t.Fatalf("single-site keeps the default: %q", got)
	}
}

func TestMultiSiteOpensTheWireGuardPortInsteadOfVXLAN(t *testing.T) {
	ports := func(network Network) map[string]bool {
		found := map[string]bool{}
		for _, p := range k3sPorts(network) {
			found[p.port+"/"+p.protocol] = true
		}
		return found
	}
	single := ports(Network{})
	if !single["8472/udp"] || single["51820/udp"] {
		t.Fatalf("single-site ports: %v", single)
	}
	multi := ports(Network{MultiSite: true})
	if !multi["51820/udp"] || multi["8472/udp"] {
		t.Fatalf("multi-site must not leave the unencrypted VXLAN port open to the internet: %v", multi)
	}
	if !multi["6443/tcp"] || !multi["10250/tcp"] {
		t.Fatalf("the API and kubelet ports stay open: %v", multi)
	}
}

func TestMultiSiteClusterIsRecordedForTheOperator(t *testing.T) {
	multi := geassClusterManifest("default", "v1", "https://203.0.113.1:6443", Network{MultiSite: true, ExternalIP: "203.0.113.1"})
	if !strings.Contains(multi, "  network:\n    multiSite: true\n") {
		t.Fatalf("the operator joins workers to match, so it has to be told:\n%s", multi)
	}
	var parsed map[string]any
	if err := yaml.Unmarshal([]byte(multi), &parsed); err != nil {
		t.Fatalf("the manifest must stay valid YAML: %v\n%s", err, multi)
	}
	single := geassClusterManifest("default", "v1", "https://127.0.0.1:6443", Network{})
	if strings.Contains(single, "network:") {
		t.Fatalf("a single-site cluster says nothing about its network:\n%s", single)
	}
}

func TestNetworkIsReadFromTheEnvironment(t *testing.T) {
	t.Setenv("GEASS_MULTI_SITE", "yes")
	t.Setenv("GEASS_NODE_EXTERNAL_IP", " 203.0.113.9 ")
	network := NetworkFromEnv()
	if !network.MultiSite || network.ExternalIP != "203.0.113.9" {
		t.Fatalf("NetworkFromEnv() = %+v", network)
	}
	t.Setenv("GEASS_MULTI_SITE", "")
	if NetworkFromEnv().MultiSite {
		t.Fatal("unset means single-site")
	}
}
