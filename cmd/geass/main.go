package main

import (
	"fmt"
	"os"
	"time"

	"github.com/degoke/geass/pkg/installer"
)

func main() {
	logPath, logNote, err := installer.InitLogging()
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to initialize install logging: %v\n", err)
	} else {
		defer func() {
			if closeErr := installer.CloseLogging(); closeErr != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to close install log: %v\n", closeErr)
			}
		}()
		fmt.Printf("Installation log: %s\n", logPath)
		if logNote != "" {
			fmt.Printf("%s\n", logNote)
		}
	}

	steps := []installer.Step{
		&installer.SystemChecks{},          // OS, memory, disk, arch
		&installer.FirewallSetup{},         // open 6443, 10250, 8472/udp
		&installer.RemoveExistingInstall{}, // uninstall k3s if already present
		&installer.K3sInstall{},            // download get.k3s.io installer and run server mode
		&installer.WaitForK3s{},            // poll until /etc/rancher/k3s/k3s.yaml exists
		&installer.ImageImport{},           // load embedded operator image into k3s containerd
		&installer.OperatorDeploy{},        // apply embedded operator manifests
		&installer.WaitForOperator{},       // poll until operator pod Running
		&installer.ClusterCRApply{},        // apply initial GeassCluster CR
	}

	installer.Logf("installer", "Starting installation with %d steps", len(steps))
	for _, step := range steps {
		start := time.Now()
		fmt.Printf("→ %s\n", step.Name())
		installer.Logf(step.Name(), "Step started")

		if err := step.Run(); err != nil {
			installer.Logf(step.Name(), "Step failed after %s: %v", time.Since(start).Round(time.Millisecond), err)
			fmt.Fprintf(os.Stderr, "✗ %s failed: %v\n", step.Name(), err)
			if logPath != "" {
				fmt.Fprintf(os.Stderr, "Detailed log: %s\n", logPath)
				_ = installer.CloseLogging()
			}
			os.Exit(1)
		}

		duration := time.Since(start).Round(time.Millisecond)
		installer.Logf(step.Name(), "Step completed successfully in %s", duration)
		fmt.Printf("✓ %s\n", step.Name())
	}
}
