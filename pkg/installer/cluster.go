package installer

import (
	"fmt"
	"os"
	"strings"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const (
	geassClusterAPIVersion = "geass.geass.dev/v1alpha1"
	geassClusterCRD        = "geassclusters.geass.geass.dev"
)

type ClusterCRApply struct {
	ClusterName string
	Version     string
	ServerURL   string
	Network     Network
	// ACMEEmail is the Let's Encrypt contact address. Without one the operator creates no certificate
	// issuer, so no ingress gets an HTTPS certificate until it is set.
	ACMEEmail string

	// detectAddress replaces the machine's real address lookup in tests.
	detectAddress func() (string, error)
}

func (s *ClusterCRApply) Name() string { return "apply GeassCluster CR" }

func (s *ClusterCRApply) Run() error {
	name := s.ClusterName
	if name == "" {
		name = "default"
	}
	version := s.Version
	if version == "" {
		version = "v1"
	}
	serverURL := s.resolveServerURL()

	kubeEnv := append(os.Environ(), k3sKubeconfigEnv)
	kubeOpts := commandOptions{
		env:        kubeEnv,
		envLog:     []string{k3sKubeconfigEnv},
		echoStdout: true,
		echoStderr: true,
	}

	Logf(s.Name(), "Waiting for CRD %s to become established", geassClusterCRD)
	if err := runCommandWithOptions(s.Name(), kubeOpts, "k3s", "kubectl", "wait",
		"--for=condition=established", "crd/"+geassClusterCRD, "--timeout=60s",
	); err != nil {
		return fmt.Errorf("wait for GeassCluster CRD: %w", err)
	}

	if s.ACMEEmail == "" {
		Warnf(s.Name(), "%s is not set. The dashboard at this server's IP address needs none, but certificates for your "+
			"domains do: enter an email address in the dashboard under Settings > Domain before you add one", acmeEmailEnv)
	}
	manifest := geassClusterManifest(name, version, serverURL, s.Network, s.ACMEEmail)

	Logf(s.Name(), "Applying GeassCluster name=%s version=%s serverURL=%s", name, version, serverURL)
	opts := kubeOpts
	opts.stdin = strings.NewReader(manifest)
	return runCommandWithOptions(s.Name(), opts, "k3s", "kubectl", "apply", "-f", "-")
}

// loopbackServerURL is what the cluster object holds when this machine's address cannot be worked out.
// No worker can use it as it is: the operator swaps in the control plane node's own address when it
// joins one.
const loopbackServerURL = "https://127.0.0.1:" + platform.ClusterAPIPort

// resolveServerURL is the address workers are told to join the cluster on, which has to be one they can
// reach from another machine. In order: an address set on the step, the control plane's public address
// for a multi-site cluster, and otherwise this machine's own address on the network its workers share
// with it, as the default route sees it.
func (s *ClusterCRApply) resolveServerURL() string {
	if s.ServerURL != "" {
		return s.ServerURL
	}
	if serverURL := s.Network.ServerURL(); serverURL != "" {
		return serverURL
	}
	detect := s.detectAddress
	if detect == nil {
		detect = controlPlaneAddress
	}
	address, err := detect()
	if err != nil {
		Warnf(s.Name(), "Could not work out this machine's address (%v), so spec.serverURL is %s. "+
			"The operator will join workers on the control plane node's own address instead; "+
			"set spec.serverURL on the GeassCluster to override it", err, loopbackServerURL)
		return loopbackServerURL
	}
	return serverURLFor(address)
}

func geassClusterManifest(name, version, serverURL string, network Network, acmeEmail string) string {
	return fmt.Sprintf(`apiVersion: %s
kind: GeassCluster
metadata:
  name: %s
  namespace: geass-system
spec:
  version: %s
  serverURL: %s
  tokenSecretRef:
    name: geass-token
    namespace: geass-system
  addons:
    longhorn:
      enabled: true
    logging:
      enabled: true
%s%s%s`, geassClusterAPIVersion, name, version, serverURL, certManagerManifest(acmeEmail), dashboardManifestSection, networkManifest(network))
}

// dashboardManifestSection serves the dashboard of a new cluster at its public IP address until it has a domain.
const dashboardManifestSection = "  dashboard:\n    publicIP: true\n"

// meshAuthSecretName is the Secret that holds the auth key of a Tailscale mesh, which the cluster object names and the
// operator reads whenever it adds a worker.
const meshAuthSecretName = "geass-mesh-auth"

// networkManifest is the cluster's network section: only a multi-site cluster says anything, and the
// operator reads it when it joins workers.
func networkManifest(network Network) string {
	switch {
	case network.IsTailscale():
		manifest := "  network:\n    mesh:\n      provider: Tailscale\n      tailscale:\n        authKeySecretRef: " + meshAuthSecretName + "\n"
		if network.Tailscale.ControlServerURL != "" {
			manifest += "        controlServerURL: \"" + network.Tailscale.ControlServerURL + "\"\n"
		}
		return manifest
	case network.Mesh == geassv1alpha1.MeshProviderWireGuard:
		// multiSite stays, so that what an operator that does not know the mesh field reads is what it always was.
		return "  network:\n    multiSite: true\n    mesh:\n      provider: WireGuard\n"
	case network.MultiSite:
		return "  network:\n    multiSite: true\n"
	}
	return ""
}
