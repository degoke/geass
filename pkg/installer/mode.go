package installer

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/degoke/geass/pkg/release"
)

// Mode is what the installer does on this machine.
type Mode string

const (
	// ModeInstall builds a cluster on a machine that has none.
	ModeInstall Mode = "install"
	// ModeUpgrade updates the Geass that is already here, keeping its data.
	ModeUpgrade Mode = "upgrade"
	// ModeReinstall removes K3s and builds a new cluster in its place, deleting what the old one held.
	ModeReinstall Mode = "reinstall"
	// ModeJoinServer makes a machine that has no K3s an additional control plane server of an existing cluster.
	ModeJoinServer Mode = "join-server"
	// ModeRefreshServer is what a server that joined a cluster is asked to do when the installer is run on it again:
	// keep the operator's image current.
	ModeRefreshServer Mode = "refresh-server"
)

// Options are what the installer was asked to do.
type Options struct {
	// Mode is what was asked for explicitly. Empty means: do whatever the machine needs.
	Mode Mode
	// Yes answers the reinstall confirmation in advance, for a script.
	Yes bool
	// Upgrade holds the choices that only an upgrade has.
	Upgrade UpgradeOptions
	// K3sVersion pins the K3s release of a new cluster, or (upgrade) the release the control plane is moved to.
	K3sVersion string
	// JoinServer and JoinToken make this machine an additional control plane server of the cluster at that
	// address, with that server token.
	JoinServer, JoinToken string
	// Version and Help ask the installer to print something and stop.
	Version, Help bool
}

const usage = `Usage: geass [options]

With no options the installer does what this machine needs: it builds a cluster on a machine that has none, and
updates the Geass that is already here, keeping everything it holds. It never removes a cluster unless it is told to.

  --upgrade              update the existing cluster (the default when there is one)
  --k3s-version <name>   a new cluster: install this K3s release instead of the current stable one
                         an existing cluster: also upgrade the control plane's K3s to it, then the workers (the
                         full name, such as v1.36.5+k3s1, one minor version at a time)
  --allow-untested-k3s   allow a K3s version outside the range this release's add-ons are known to run on
  --skip-snapshot        update without taking an etcd snapshot first (there is then no way back)
  --no-rollback          if the new operator does not take over, leave it as it is instead of rolling the operator
                         back to the one that ran before
  --force                update although the cluster is not healthy (not ready, degraded volumes)
  --reinstall            DELETE the cluster on this machine and build a new one
  --join-server <url>    do not build a cluster: make this machine an additional control plane server of the one at
                         this address (https://host:6443), so that the cluster survives losing one. The server token
                         is in /var/lib/rancher/k3s/server/token on the first server; give it in GEASS_JOIN_TOKEN
                         (or --join-token, which other users of this machine can read in the process list). A server
                         has to run the cluster's K3s, which a machine that has not joined cannot ask it: say which with
                         --k3s-version (or GEASS_K3S_VERSION), as "k3s --version" on the first server prints it
  --yes                  do not ask for confirmation of --reinstall
  --version              print the release this installer is
  --help                 print this

Run on a server that joined a cluster, with no options, the installer only puts the operator's image on it, which is what
lets the operator run there when the first server is gone. Run it on each of them after the first one is updated.

Environment: GEASS_REINSTALL=1, GEASS_YES=1, GEASS_NO_ROLLBACK=1 and GEASS_JOIN_SERVER=<url> are the same as the options. GEASS_K3S_VERSION is the same as
--k3s-version when a cluster is being built, and is ignored when one is being updated: only the option upgrades its K3s. The settings
of a new cluster (GEASS_ACME_EMAIL, GEASS_MULTI_SITE, GEASS_NODE_EXTERNAL_IP) apply only when one is being built.
`

// ParseOptions reads the installer's arguments and the environment variables that stand for them.
func ParseOptions(args []string, getenv func(string) string) (Options, error) {
	var opts Options
	var upgrade, reinstall bool

	fs := flag.NewFlagSet("geass", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.BoolVar(&upgrade, "upgrade", false, "")
	fs.BoolVar(&reinstall, "reinstall", false, "")
	fs.BoolVar(&opts.Yes, "yes", false, "")
	fs.BoolVar(&opts.Yes, "y", false, "")
	fs.BoolVar(&opts.Upgrade.Force, "force", false, "")
	fs.BoolVar(&opts.Upgrade.SkipSnapshot, "skip-snapshot", false, "")
	fs.BoolVar(&opts.Upgrade.NoRollback, "no-rollback", false, "")
	fs.BoolVar(&opts.Upgrade.AllowUntestedK3s, "allow-untested-k3s", false, "")
	fs.StringVar(&opts.K3sVersion, "k3s-version", "", "")
	fs.StringVar(&opts.JoinServer, "join-server", "", "")
	fs.StringVar(&opts.JoinToken, "join-token", "", "")
	fs.BoolVar(&opts.Version, "version", false, "")
	fs.BoolVar(&opts.Help, "help", false, "")
	fs.BoolVar(&opts.Help, "h", false, "")

	// "geass upgrade" reads better than "geass --upgrade", and means the same.
	var rest []string
	for _, a := range args {
		if a == "upgrade" || a == "install" {
			upgrade = upgrade || a == "upgrade"
			if a == "install" {
				opts.Mode = ModeInstall
			}
			continue
		}
		rest = append(rest, a)
	}
	if err := fs.Parse(rest); err != nil {
		return Options{}, fmt.Errorf("%w\n\n%s", err, usage)
	}
	if fs.NArg() > 0 {
		return Options{}, fmt.Errorf("unexpected argument %q\n\n%s", fs.Arg(0), usage)
	}

	// Only the option upgrades the K3s of a cluster that exists. The variable picks the release a new cluster is built on,
	// and is often left set in a shell profile or a pipeline from the day of the install: read as the option, it would
	// move the control plane (and then every worker) to a new K3s on an update that asked for a Geass release only.
	opts.Upgrade.K3sVersion = opts.K3sVersion
	if opts.K3sVersion == "" {
		opts.K3sVersion = strings.TrimSpace(getenv("GEASS_K3S_VERSION"))
	}
	if opts.JoinServer == "" {
		opts.JoinServer = strings.TrimSpace(getenv("GEASS_JOIN_SERVER"))
	}
	if opts.JoinToken == "" {
		opts.JoinToken = strings.TrimSpace(getenv("GEASS_JOIN_TOKEN"))
	}
	reinstall = reinstall || truthy(getenv("GEASS_REINSTALL"))
	opts.Upgrade.NoRollback = opts.Upgrade.NoRollback || truthy(getenv("GEASS_NO_ROLLBACK"))
	opts.Yes = opts.Yes || truthy(getenv("GEASS_YES"))

	switch {
	case reinstall && upgrade:
		return Options{}, errors.New("--reinstall and --upgrade contradict each other: one deletes the cluster and the other keeps it")
	case opts.JoinServer != "" && (reinstall || upgrade):
		return Options{}, errors.New("--join-server adds this machine to a cluster that exists, and cannot be combined with --reinstall or --upgrade")
	case opts.JoinServer != "":
		if err := (JoinOptions{ServerURL: opts.JoinServer, Token: opts.JoinToken}).Validate(); err != nil {
			return Options{}, err
		}
		opts.Mode = ModeJoinServer
	case reinstall:
		opts.Mode = ModeReinstall
	case upgrade:
		opts.Mode = ModeUpgrade
	}
	if opts.K3sVersion != "" && !release.ValidK3s(opts.K3sVersion) {
		return Options{}, fmt.Errorf("%q is not a K3s release: use the full name, such as v1.36.5+k3s1 (a channel such as stable is not accepted)", opts.K3sVersion)
	}
	return opts, nil
}

// Usage is what --help prints.
func Usage() string { return usage }

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// Existing is what is on this machine already.
type Existing struct {
	// K3sPresent says that something of K3s is installed here.
	K3sPresent bool
	// Agent says that it is an agent (a worker) and not a server.
	Agent bool
	// JoinedServer says that it is a server that joined a cluster another server started, so what is here is one copy
	// of a cluster and not a cluster of its own.
	JoinedServer bool
	// GeassPresent says that the K3s has Geass in it.
	GeassPresent bool
	// Unreachable is why the K3s server could not be asked, when it could not be.
	Unreachable error
}

// ChooseMode decides what to do from what was asked for and what is here. It is where the installer stops being
// willing to delete a cluster on its own account: the only way to Reinstall is to ask for it.
func ChooseMode(want Mode, here Existing) (Mode, error) {
	if want == ModeJoinServer {
		if here.K3sPresent {
			return "", errors.New("this machine already has K3s, and a machine can only join a cluster before it has one. The installer will not replace a K3s that is here: " +
				"remove it yourself (and, if it was a server of that cluster, delete its node from the cluster first) and run the installer again")
		}
		return ModeJoinServer, nil
	}
	if want == ModeReinstall {
		if !here.K3sPresent {
			return ModeInstall, nil
		}
		if here.JoinedServer {
			return "", errors.New("--reinstall would delete this server's copy of a cluster that other servers hold too, and leave a member in the cluster's database " +
				"that no longer exists. Delete this node from the cluster first (kubectl delete node <name>, from another server), then run the installer again")
		}
		return ModeReinstall, nil
	}
	if !here.K3sPresent {
		if want == ModeUpgrade {
			return "", errors.New("there is nothing to upgrade: this machine has no K3s, and so no Geass. Run the installer without --upgrade to build a cluster")
		}
		return ModeInstall, nil
	}

	const wipe = "To delete what is here and build a new cluster, run the installer with --reinstall."
	switch {
	case here.Agent:
		return "", fmt.Errorf("this machine is a worker (it runs the K3s agent), and the installer sets up and updates a control plane. "+
			"Workers are updated by Geass from the control plane: set spec.k3s.version on the GeassCluster. %s", wipe)
	case errors.Is(here.Unreachable, errNeedsRoot):
		// Not a reason to offer --reinstall: nothing is wrong with the cluster.
		return "", fmt.Errorf("K3s is installed here, but it could not be asked what it holds: %w. Run the installer with sudo", here.Unreachable)
	case here.Unreachable != nil:
		return "", fmt.Errorf("K3s is installed here but its API server did not answer (%v), so the installer cannot tell what it would be updating. "+
			"Check `systemctl status k3s` and run it again. %s", here.Unreachable, wipe)
	case here.JoinedServer && want == ModeUpgrade:
		return "", errors.New("this machine is a server that joined a cluster, and the installer updates a cluster from its first server. Run it there: " +
			"it updates Geass and the cluster's K3s. Then run the installer here with no options, to put the new operator image on this server")
	case here.JoinedServer && want == ModeInstall:
		return "", errors.New("this machine already is a server of a cluster. Run the installer here with no options to refresh its operator image")
	case here.JoinedServer:
		return ModeRefreshServer, nil
	case !here.GeassPresent:
		return "", fmt.Errorf("K3s is installed here but Geass is not in it. The installer will not remove a K3s that it did not set up for Geass. %s", wipe)
	case want == ModeInstall:
		return "", errors.New("this machine already has Geass installed: run the installer without options to update it, or with --reinstall to delete it and start again")
	}
	return ModeUpgrade, nil
}

// DetectExisting looks at this machine.
func DetectExisting() Existing {
	here := Existing{K3sPresent: K3sInstalled()}
	if !here.K3sPresent {
		return here
	}
	_, server := os.Stat(k3sServiceUnit)
	_, agent := os.Stat("/etc/systemd/system/k3s-agent.service")
	if server != nil && agent == nil {
		here.Agent = true
		return here
	}
	if config, err := os.ReadFile(k3sConfigPath); err == nil && joinedServerConfig(string(config)) {
		here.JoinedServer = true
		return here
	}
	installed, err := GeassInstalled()
	here.GeassPresent, here.Unreachable = installed, err
	return here
}

// ConfirmReinstall is the question that stands between --reinstall and the deletion of a cluster. A script answers it
// in advance with --yes; a person is asked, on the terminal, because the installer is usually run as `curl | sh`,
// where its standard input is the script and not the person.
type ConfirmReinstall struct {
	Yes bool
	// SkipSnapshot says that no snapshot will be taken (--skip-snapshot), which the question has to say.
	SkipSnapshot bool
	// OpenTerminal opens the person's terminal; /dev/tty when nil.
	OpenTerminal func() (io.ReadWriteCloser, error)
}

func (s *ConfirmReinstall) Name() string { return "confirm reinstall" }

const confirmWord = "reinstall"

func (s *ConfirmReinstall) Run() error {
	if s.Yes {
		Warnf(s.Name(), "Reinstalling without asking (--yes)")
		return nil
	}
	open := s.OpenTerminal
	if open == nil {
		open = func() (io.ReadWriteCloser, error) { return os.OpenFile("/dev/tty", os.O_RDWR, 0) }
	}
	tty, err := open()
	if err != nil {
		return errors.New("--reinstall deletes this machine's cluster and its data, and there is no terminal to ask you on. Add --yes if you mean it")
	}
	defer func() { _ = tty.Close() }()

	snapshot := fmt.Sprintf("The installer will try to keep a last etcd snapshot in %s first (nothing restores it for you, and a\n"+
		"cluster that is broken may not be able to give one).", reinstallSnapshotDir)
	if s.SkipSnapshot {
		snapshot = "No snapshot will be kept (--skip-snapshot)."
	}
	_, _ = fmt.Fprintf(tty, "\nThis DELETES the cluster on this machine: every project, service, secret, API token and\n"+
		"database that Geass and Kubernetes keep here, and the data on the local volumes.\n"+
		"%s\n\n"+
		"Type %q to go on, anything else to stop: ", snapshot, confirmWord)
	answer, err := bufio.NewReader(tty).ReadString('\n')
	if err != nil && answer == "" {
		return errors.New("no answer was given, so nothing was deleted")
	}
	if strings.TrimSpace(answer) != confirmWord {
		return errors.New("not confirmed, so nothing was deleted")
	}
	return nil
}

// reinstallSnapshotDir is where a reinstall leaves the last snapshot: outside K3s's data directory, which the
// uninstall deletes.
const reinstallSnapshotDir = "/var/backups/geass"

// BestEffort runs a step whose failure must not stop what comes next: a snapshot of a cluster that is being thrown
// away because it is broken may well be one that cannot be taken.
type BestEffort struct{ Step }

func (b BestEffort) Run() error {
	if err := b.Step.Run(); err != nil {
		Warnf(b.Name(), "Continuing without it: %v", err)
	}
	return nil
}

// ReinstallSnapshot keeps the cluster's last state where the uninstall cannot reach it.
func ReinstallSnapshot(plan *UpgradePlan) Step {
	plan.Target = release.Version
	return BestEffort{&EtcdSnapshot{Plan: plan, Dir: reinstallSnapshotDir, Label: "geass-pre-reinstall"}}
}
