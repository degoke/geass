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
	return operatorDeployment
}

// Run waits for the operator of a cluster that was just built. It is the same wait as the one that an update makes
// (waitForOperatorRollout), so a pod that is crash looping or cannot pull its image is reported at once, with why.
func (s *WaitForOperator) Run() error {
	return waitForOperatorRollout(s.Name(), s.deploymentName(), 5*time.Minute)
}
