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

	// GEASS_MULTI_SITE=1 GEASS_NODE_EXTERNAL_IP=<public ip> builds a cluster whose nodes are at
	// different providers: an encrypted WireGuard overlay instead of VXLAN, and the public IP advertised.
	network := installer.NetworkFromEnv()

	// GEASS_ACME_EMAIL is the contact address for the cluster's Let's Encrypt account. Without it the
	// cluster installs with no certificate issuer, so no ingress gets an HTTPS certificate.
	acmeEmail := installer.ACMEEmailFromEnv()
	checks := &installer.SystemChecks{Network: network, ACMEEmail: acmeEmail}
	applyCluster := &installer.ClusterCRApply{Network: network, ACMEEmail: acmeEmail}

	steps := []installer.Step{
		checks, // OS, memory, disk, arch, the ACME email, and WireGuard for multi-site
		&installer.FirewallSetup{Network: network}, // open 6443, 10250 and the overlay port when a host firewall is active
		&installer.RemoveExistingInstall{},         // uninstall k3s if already present
		&installer.NodePrerequisites{},             // iSCSI initiator, which Longhorn needs on every node
		&installer.K3sInstall{Network: network},    // download get.k3s.io installer and run server mode
		&installer.WaitForK3s{},                    // poll until /etc/rancher/k3s/k3s.yaml exists
		&installer.ImageImport{},                   // load embedded operator image into k3s containerd
		&installer.OperatorDeploy{},                // apply embedded operator manifests
		&installer.WaitForOperator{},               // poll until operator pod Running
		&installer.JoinTokenSecret{},               // store the join token the operator needs to add workers
		applyCluster,                               // apply initial GeassCluster CR
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
