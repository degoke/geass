package main

import (
	"fmt"
	"os"
	"time"

	"github.com/degoke/geass/pkg/installer"
	"github.com/degoke/geass/pkg/release"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	opts, err := installer.ParseOptions(args, os.Getenv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 2
	}
	if opts.Help {
		fmt.Print(installer.Usage())
		return 0
	}
	if opts.Version {
		fmt.Println(release.Version)
		return 0
	}

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

	// What this machine needs depends on what is on it: a bare machine gets a cluster, a machine that runs Geass is
	// updated with its data kept, and a cluster is only ever deleted when that was asked for.
	mode, err := installer.ChooseMode(opts.Mode, installer.DetectExisting())
	if err != nil {
		fmt.Fprintf(os.Stderr, "✗ %v\n", err)
		return 1
	}
	installer.Logf("installer", "Geass %s, mode %s", release.Version, mode)
	fmt.Printf("%s: %s\n", release.Describe(release.Version), mode)

	var steps []installer.Step
	switch mode {
	case installer.ModeUpgrade:
		steps = upgradeSteps(opts)
	case installer.ModeJoinServer:
		steps = installer.JoinServerSteps(opts)
	case installer.ModeRefreshServer:
		steps = installer.RefreshServerSteps()
	case installer.ModeEtcdSnapshots:
		steps = installer.EtcdSnapshotsSteps(opts)
	case installer.ModeReinstall:
		steps = append([]installer.Step{
			&installer.ConfirmReinstall{Yes: opts.Yes, SkipSnapshot: opts.Upgrade.SkipSnapshot},
			installer.ReinstallSnapshot(installer.NewUpgradePlan(opts.Upgrade)),
		}, installSteps(opts)...)
	default:
		steps = installSteps(opts)
	}

	installer.Logf("installer", "Starting %s with %d steps", mode, len(steps))
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
			return 1
		}

		duration := time.Since(start).Round(time.Millisecond)
		installer.Logf(step.Name(), "Step completed successfully in %s", duration)
		fmt.Printf("✓ %s\n", step.Name())
	}
	return 0
}

// upgradeSteps updates a cluster that is already here. The settings of a new cluster are not applied to it: its
// address, region and certificate contact are on the GeassCluster object, which the update leaves alone.
func upgradeSteps(opts installer.Options) []installer.Step {
	for _, name := range []string{"GEASS_ACME_EMAIL", "GEASS_MULTI_SITE", "GEASS_NODE_EXTERNAL_IP"} {
		if os.Getenv(name) != "" {
			installer.Warnf("installer", "%s is ignored: it sets up a new cluster, and this one is being updated. "+
				"Change the cluster's settings on its GeassCluster object", name)
		}
	}
	if opts.EtcdSnapshots.Enable || opts.EtcdSnapshots.Destination != nil {
		installer.Warnf("installer", "--etcd-snapshots and GEASS_ETCD_SNAPSHOTS_* are ignored: they set up a new cluster, "+
			"and this one is being updated. To keep copies of this cluster in S3, run the installer with "+
			"`etcd-snapshots enable`")
	}
	if os.Getenv("GEASS_K3S_VERSION") != "" && opts.Upgrade.K3sVersion == "" {
		installer.Warnf("installer", "GEASS_K3S_VERSION is ignored: it picks the K3s of a new cluster, "+
			"and this one is being updated. To upgrade its K3s as well, pass --k3s-version")
	}
	return installer.UpgradeSteps(installer.NewUpgradePlan(opts.Upgrade))
}

// installSteps builds a cluster. It is also what a reinstall does after it has removed the old one.
func installSteps(opts installer.Options) []installer.Step {
	// GEASS_MULTI_SITE=1 GEASS_NODE_EXTERNAL_IP=<public ip> builds a cluster whose nodes are at
	// different providers: an encrypted WireGuard overlay instead of VXLAN, and the public IP advertised.
	network := installer.NetworkFromEnv()

	// GEASS_ACME_EMAIL is the contact address for the cluster's Let's Encrypt account. Without it the
	// cluster installs with no certificate issuer, so no ingress gets an HTTPS certificate.
	acmeEmail := installer.ACMEEmailFromEnv()
	checks := &installer.SystemChecks{
		Network: network, ACMEEmail: acmeEmail, K3sVersion: opts.K3sVersion, AllowUntestedK3s: opts.Upgrade.AllowUntestedK3s,
	}
	applyCluster := &installer.ClusterCRApply{Network: network, ACMEEmail: acmeEmail}
	// Whether K3s sends copies of the cluster's state to an S3 bucket of the person's own. It is never on unless it was
	// asked for or the person at the terminal says yes, and it is decided before K3s starts, which reads it at start.
	snapshots := &installer.EtcdSnapshotsChoice{}

	return []installer.Step{
		// OS, memory, disk, arch, the ACME email, and WireGuard for multi-site.
		checks,
		// Uninstall k3s if it is already present (only a reinstall gets here with one), before the firewall step
		// saves its rules.
		&installer.RemoveExistingInstall{},
		// Open the cluster, overlay and web ports when a host firewall is active.
		&installer.FirewallSetup{Network: network},
		// The iSCSI initiator, which Longhorn needs on every node.
		&installer.NodePrerequisites{},
		// Ask whether the cluster's state is kept off this machine, and if so tell K3s so before it starts.
		&installer.AskEtcdSnapshots{Choice: snapshots, Flag: opts.EtcdSnapshots.Enable, K3sVersion: opts.K3sVersion},
		&installer.WriteEtcdSnapshotsDropIn{Choice: snapshots},
		// Download the get.k3s.io installer and run it in server mode.
		&installer.K3sInstall{Network: network, Version: opts.K3sVersion},
		// Poll until /etc/rancher/k3s/k3s.yaml exists.
		&installer.WaitForK3s{},
		// A copy of the cluster is only readable with this machine's server token, which the disk that the copies are
		// meant to outlive holds.
		&installer.EtcdSnapshotsToken{Choice: snapshots},
		// Load the embedded operator image into k3s's containerd.
		&installer.ImageImport{},
		// Apply the embedded operator manifests.
		&installer.OperatorDeploy{},
		// Poll until the operator pod is running.
		&installer.WaitForOperator{},
		// Store the join token the operator needs to add workers.
		&installer.JoinTokenSecret{},
		// Apply the initial GeassCluster object.
		applyCluster,
		// Say where the copies go, when that was given.
		&installer.SetEtcdSnapshotsDestination{Destination: opts.EtcdSnapshots.Destination, Choice: snapshots},
		// And take the first copy at once, so that it is known whether they arrive before noon.
		&installer.TakeFirstEtcdSnapshot{Destination: opts.EtcdSnapshots.Destination, Choice: snapshots},
	}
}
