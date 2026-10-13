package installer

import (
	"errors"
	"net"
	"strings"
	"testing"
)

func TestGeassClusterManifestUsesSystemNamespace(t *testing.T) {
	t.Parallel()

	manifest := geassClusterManifest("default", "v1", "https://example.test", Network{}, "")
	if !strings.Contains(manifest, "namespace: geass-system") {
		t.Fatalf("manifest should place GeassCluster in geass-system:\n%s", manifest)
	}
}

func TestGeassClusterManifestTurnsOnLonghorn(t *testing.T) {
	t.Parallel()

	// Longhorn is opt-in in the CRD (kind clusters cannot run it), so the
	// installer, which only ever builds real clusters, has to ask for it.
	manifest := geassClusterManifest("default", "v1", "https://example.test", Network{}, "")
	if !strings.Contains(manifest, "addons:\n    longhorn:\n      enabled: true\n") {
		t.Fatalf("manifest should enable the Longhorn add-on:\n%s", manifest)
	}
}

func addressIs(address string) func() (string, error) {
	return func() (string, error) { return address, nil }
}

// A default install used to store https://127.0.0.1:6443, the control plane's own loopback address, so
// no worker could ever join it.
func TestADefaultInstallTellsWorkersToJoinOnTheControlPlanesOwnAddress(t *testing.T) {
	t.Parallel()

	step := &ClusterCRApply{detectAddress: addressIs("10.0.0.5")}
	if got := step.resolveServerURL(); got != "https://10.0.0.5:6443" {
		t.Fatalf("serverURL = %q, want the machine's address on the default route", got)
	}
	step = &ClusterCRApply{detectAddress: addressIs("2001:db8::5")}
	if got := step.resolveServerURL(); got != "https://[2001:db8::5]:6443" {
		t.Fatalf("an IPv6 address needs brackets in a URL: %q", got)
	}
}

func TestAMultiSiteClusterStillJoinsOnThePublicAddress(t *testing.T) {
	t.Parallel()

	step := &ClusterCRApply{
		Network:       Network{MultiSite: true, ExternalIP: "203.0.113.10"},
		detectAddress: addressIs("10.0.0.5"),
	}
	if got := step.resolveServerURL(); got != "https://203.0.113.10:6443" {
		t.Fatalf("workers at other providers cannot reach a private address: %q", got)
	}
}

func TestAnAddressSetOnTheStepBeatsEveryDetection(t *testing.T) {
	t.Parallel()

	step := &ClusterCRApply{
		ServerURL:     "https://cp.example.com:6443",
		Network:       Network{MultiSite: true, ExternalIP: "203.0.113.10"},
		detectAddress: addressIs("10.0.0.5"),
	}
	if got := step.resolveServerURL(); got != "https://cp.example.com:6443" {
		t.Fatalf("serverURL = %q", got)
	}
}

// With no default route there is no address to give. The operator then joins workers on the control plane
// node's own address, so the install must not fail over it.
func TestWithoutADetectableAddressTheInstallFallsBackToLoopbackAndDoesNotFail(t *testing.T) {
	t.Parallel()

	step := &ClusterCRApply{detectAddress: func() (string, error) { return "", errors.New("network is unreachable") }}
	if got := step.resolveServerURL(); got != "https://127.0.0.1:6443" {
		t.Fatalf("serverURL = %q", got)
	}
}

func TestTheDetectedAddressIsWhatTheClusterObjectCarries(t *testing.T) {
	t.Parallel()

	step := &ClusterCRApply{detectAddress: addressIs("10.0.0.5")}
	manifest := geassClusterManifest("default", "v1", step.resolveServerURL(), Network{}, "")
	if !strings.Contains(manifest, "  serverURL: https://10.0.0.5:6443\n") {
		t.Fatalf("manifest should carry the address workers join on:\n%s", manifest)
	}
	if strings.Contains(manifest, "127.0.0.1") {
		t.Fatalf("manifest must not carry the loopback address:\n%s", manifest)
	}
}

// The real lookup talks to the kernel's routing table. Where there is no route (a sandbox with no
// network), it must say so rather than invent an address.
func TestTheRealAddressLookupNeverReturnsALoopbackAddress(t *testing.T) {
	t.Parallel()

	address, err := controlPlaneAddress()
	if err != nil {
		t.Skipf("no default route here: %v", err)
	}
	ip := net.ParseIP(address)
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
		t.Fatalf("controlPlaneAddress() = %q, want an address other machines can reach", address)
	}
}
