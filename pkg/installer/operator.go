package installer

import (
	"bytes"
	"fmt"
	"os"
	"strings"
)

type OperatorDeploy struct {
	// KustomizePath optionally applies manifests from disk instead of the embedded bundle.
	KustomizePath string
}

func (s *OperatorDeploy) Name() string { return "deploy geass-operator" }

func (s *OperatorDeploy) Run() error {
	if path := strings.TrimSpace(s.KustomizePath); path != "" {
		return s.applyFromKustomize(path)
	}
	return s.applyEmbedded()
}

func (s *OperatorDeploy) applyFromKustomize(path string) error {
	resolved, err := ResolvePath(path)
	if err != nil {
		return fmt.Errorf("resolve kustomize path: %w", err)
	}
	Logf(s.Name(), "Deploying manifests from %s", resolved)

	return runCommandWithOptions(s.Name(), commandOptions{
		env:        append(os.Environ(), "KUBECONFIG=/etc/rancher/k3s/k3s.yaml"),
		envLog:     []string{"KUBECONFIG=/etc/rancher/k3s/k3s.yaml"},
		echoStdout: true,
		echoStderr: true,
	}, "k3s", "kubectl", "apply", "-k", resolved)
}

func (s *OperatorDeploy) applyEmbedded() error {
	if len(embeddedOperatorManifests) == 0 {
		return fmt.Errorf("embedded operator manifests are empty; rebuild geass with make build-geass")
	}

	Logf(s.Name(), "Deploying embedded operator manifests (%d bytes)", len(embeddedOperatorManifests))
	return runCommandWithOptions(s.Name(), commandOptions{
		env:        append(os.Environ(), "KUBECONFIG=/etc/rancher/k3s/k3s.yaml"),
		envLog:     []string{"KUBECONFIG=/etc/rancher/k3s/k3s.yaml"},
		stdin:      bytes.NewReader(embeddedOperatorManifests),
		echoStdout: true,
		echoStderr: true,
	}, "k3s", "kubectl", "apply", "-f", "-")
}
