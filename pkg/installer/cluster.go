package installer

import (
	"fmt"
	"os"
	"strings"
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
	serverURL := s.ServerURL
	if serverURL == "" {
		serverURL = s.Network.ServerURL()
	}
	if serverURL == "" {
		serverURL = "https://127.0.0.1:6443"
	}

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
		Warnf(s.Name(), "%s is not set, so no certificate issuer is created and HTTPS certificates will not be issued; "+
			"set spec.addons.certManager.acmeEmail on the GeassCluster to enable them", acmeEmailEnv)
	}
	manifest := geassClusterManifest(name, version, serverURL, s.Network, s.ACMEEmail)

	Logf(s.Name(), "Applying GeassCluster name=%s version=%s serverURL=%s", name, version, serverURL)
	opts := kubeOpts
	opts.stdin = strings.NewReader(manifest)
	return runCommandWithOptions(s.Name(), opts, "k3s", "kubectl", "apply", "-f", "-")
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
%s%s`, geassClusterAPIVersion, name, version, serverURL, certManagerManifest(acmeEmail), networkManifest(network))
}

// networkManifest is the cluster's network section: only a multi-site cluster says anything, and the
// operator reads it when it joins workers.
func networkManifest(network Network) string {
	if !network.MultiSite {
		return ""
	}
	return "  network:\n    multiSite: true\n"
}
