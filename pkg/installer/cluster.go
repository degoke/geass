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
		serverURL = "https://127.0.0.1:6443"
	}

	kubeEnv := append(os.Environ(), "KUBECONFIG=/etc/rancher/k3s/k3s.yaml")
	kubeOpts := commandOptions{
		env:        kubeEnv,
		envLog:     []string{"KUBECONFIG=/etc/rancher/k3s/k3s.yaml"},
		echoStdout: true,
		echoStderr: true,
	}

	Logf(s.Name(), "Waiting for CRD %s to become established", geassClusterCRD)
	if err := runCommandWithOptions(s.Name(), kubeOpts, "k3s", "kubectl", "wait",
		"--for=condition=established", "crd/"+geassClusterCRD, "--timeout=60s",
	); err != nil {
		return fmt.Errorf("wait for GeassCluster CRD: %w", err)
	}

	manifest := fmt.Sprintf(`apiVersion: %s
kind: GeassCluster
metadata:
  name: %s
spec:
  version: %s
  serverURL: %s
  tokenSecretRef:
    name: geass-token
    namespace: geass-system
`, geassClusterAPIVersion, name, version, serverURL)

	Logf(s.Name(), "Applying GeassCluster name=%s version=%s serverURL=%s", name, version, serverURL)
	opts := kubeOpts
	opts.stdin = strings.NewReader(manifest)
	return runCommandWithOptions(s.Name(), opts, "k3s", "kubectl", "apply", "-f", "-")
}
