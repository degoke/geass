package installer

import (
	"fmt"
	"os"
	"strings"
	"time"
)

type WaitForK3s struct{}

func (s *WaitForK3s) Name() string { return "wait for k3s ready" }

func (s *WaitForK3s) Run() error {
	deadline := time.Now().Add(5 * time.Minute)
	Logf(s.Name(), "Waiting for /etc/rancher/k3s/k3s.yaml until %s", deadline.UTC().Format(time.RFC3339))
	for time.Now().Before(deadline) {
		if _, err := os.Stat("/etc/rancher/k3s/k3s.yaml"); err == nil {
			Logf(s.Name(), "k3s kubeconfig detected")
			return runCommand(s.Name(), "k3s", "kubectl", "get", "node")
		}
		fmt.Println("  waiting for k3s...")
		Logf(s.Name(), "k3s kubeconfig not present yet")
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("k3s did not become ready within 5 minutes")
}

type WaitForOperator struct {
	// DeploymentName is the operator Deployment to wait for in geass-system.
	DeploymentName string
}

func (s *WaitForOperator) Name() string { return "wait for operator ready" }

func (s *WaitForOperator) deploymentName() string {
	if name := strings.TrimSpace(s.DeploymentName); name != "" {
		return name
	}
	return "geass-controller-manager"
}

func (s *WaitForOperator) Run() error {
	deployment := s.deploymentName()
	deadline := time.Now().Add(5 * time.Minute)
	kubeEnv := append(os.Environ(), "KUBECONFIG=/etc/rancher/k3s/k3s.yaml")
	Logf(s.Name(), "Waiting for deployment/%s rollout until %s", deployment, deadline.UTC().Format(time.RFC3339))
	for time.Now().Before(deadline) {
		out, err := combinedOutputCommandWithOptions(s.Name(), commandOptions{
			env:    kubeEnv,
			envLog: []string{"KUBECONFIG=/etc/rancher/k3s/k3s.yaml"},
		}, "k3s", "kubectl",
			"rollout", "status", "deployment/"+deployment,
			"-n", "geass-system", "--timeout=60s",
		)
		if err == nil {
			Logf(s.Name(), "Operator rollout completed")
			return nil
		}
		fmt.Printf("  waiting... %s\n", strings.TrimSpace(string(out)))
		Logf(s.Name(), "Operator rollout still pending")
		time.Sleep(5 * time.Second)
	}
	return fmt.Errorf("operator did not become ready within 5 minutes")
}
