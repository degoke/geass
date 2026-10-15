package installer

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/degoke/geass/pkg/migrations"
	"github.com/degoke/geass/pkg/release"
)

// UpgradeOptions are the choices a person has in an upgrade.
type UpgradeOptions struct {
	// Force goes on although a check says the cluster is not in a state to be updated. The checks are there to
	// protect data, so this is for a person who has looked at what they say.
	Force bool
	// SkipSnapshot goes on without taking an etcd snapshot first.
	SkipSnapshot bool
	// K3sVersion, when it is set, also upgrades the control plane's K3s to this release, and sets it as the version
	// that the workers are brought to.
	K3sVersion string
	// AllowUntestedK3s allows a K3s version outside the range this release's add-ons are known to run on.
	AllowUntestedK3s bool
	// NoRollback leaves a new operator that does not take over where it is, instead of rolling the operator back to
	// the one that ran before. For looking at what went wrong in the cluster it went wrong in.
	NoRollback bool
}

// UpgradePlan is what the steps of an upgrade share: what they found out, and what they decided.
type UpgradePlan struct {
	Options UpgradeOptions
	// Target is the release being installed.
	Target string
	// State is what the cluster looked like before anything was changed.
	State ClusterState
	// K3sTo is the K3s release the control plane is to be moved to, empty when K3s is left alone.
	K3sTo string
	// SnapshotDir and SnapshotName say where the snapshot taken before the upgrade is. Empty when none was taken.
	SnapshotDir, SnapshotName string
}

// NewUpgradePlan starts the plan for installing this binary's release over an existing cluster.
func NewUpgradePlan(opts UpgradeOptions) *UpgradePlan {
	return &UpgradePlan{Options: opts, Target: release.Version, K3sTo: opts.K3sVersion}
}

// SnapshotDescription says where the snapshot is, for a message that needs to point at it.
func (p *UpgradePlan) SnapshotDescription() string {
	if p.SnapshotName == "" {
		return "(none was taken)"
	}
	return filepath.Join(p.SnapshotDir, p.SnapshotName+"-*")
}

// UpgradeSteps is the installer's work on a machine that already runs Geass. Nothing in it removes K3s, replaces the
// cluster object or touches what the cluster stores: it replaces the custom resource definitions, the operator and
// its permissions, and (when asked) K3s, in an order that stops before the next change if the one before failed.
func UpgradeSteps(plan *UpgradePlan) []Step {
	return []Step{
		&UpgradePreflight{Plan: plan},
		&EtcdSnapshot{Plan: plan},
		&K3sServerUpgrade{Plan: plan},
		&ImageImport{},
		&UpgradeCRDs{Plan: plan},
		&UpgradeOperator{Plan: plan},
		&OperatorTakeover{Plan: plan},
		&SetK3sTarget{Plan: plan},
		&WaitForClusterReady{Plan: plan},
		&UpgradeSummary{Plan: plan},
	}
}

// ---- preflight ------------------------------------------------------------------------------------------------

// UpgradePreflight reads the cluster and refuses to go on when the upgrade is a downgrade, when the cluster is in a
// state that an upgrade could make worse, or when what was asked for cannot be done safely. It changes nothing.
type UpgradePreflight struct {
	Plan *UpgradePlan
}

func (s *UpgradePreflight) Name() string { return "check the cluster before updating" }

func (s *UpgradePreflight) Run() error {
	plan := s.Plan
	if plan.Options.K3sVersion != "" && !release.ValidK3s(plan.Options.K3sVersion) {
		return fmt.Errorf("%q is not a K3s release: use the full name, such as v1.36.5+k3s1", plan.Options.K3sVersion)
	}
	state, err := InspectCluster(s.Name())
	if err != nil {
		return err
	}
	plan.State = state

	recorded := state.RecordedRelease()
	fmt.Printf("  cluster %q runs %s on K3s %s\n", state.ClusterName, release.Describe(recorded), orUnknown(state.K3sVersion))
	fmt.Printf("  this installer is %s\n", release.Describe(plan.Target))
	// What follows is refused whatever --force says: it is what keeps the data readable, not a state that passes.
	if err := release.CheckUpgrade(recorded, plan.Target); err != nil {
		return err
	}
	if state.SchemaVersion > migrations.Latest() {
		// The release's own number cannot always be ordered (a build from an untagged checkout is a bare commit), the
		// data's level always can be.
		return fmt.Errorf("the data in this cluster is at schema level %d, and this installer's release only knows up to level %d: "+
			"it is older than the release that last ran here. Geass does not downgrade a cluster; install the same or a newer release, "+
			"or restore a snapshot taken before the update", state.SchemaVersion, migrations.Latest())
	}
	if err := k3sPlanProblem(state, plan.Options); err != nil {
		return err
	}

	incoming, err := incomingCRDVersions()
	if err != nil {
		return err
	}
	live, err := liveGeassCRDs(s.Name())
	if err != nil {
		return err
	}
	if problems := crdReplacementProblems(live, incoming); len(problems) > 0 {
		return fmt.Errorf("this release cannot replace the custom resource definitions this cluster has:\n%s"+
			"Updating would leave the API server unable to read those objects. Update to the release in between first, which migrates them",
			describeProblems(problems))
	}

	blockers, warnings := upgradeFindings(state, plan.Options)
	for _, w := range warnings {
		Warnf(s.Name(), "%s", w)
	}
	if len(blockers) > 0 {
		if !plan.Options.Force {
			return fmt.Errorf("the cluster is not in a state to be updated:\n%s\nFix these and run the installer again. "+
				"If you have looked at them and want to go on anyway, add --force", describeProblems(blockers))
		}
		for _, b := range blockers {
			Warnf(s.Name(), "going on although: %s", b)
		}
	}
	return nil
}

func incomingCRDVersions() (map[string]map[string]bool, error) {
	if len(embeddedOperatorManifests) == 0 {
		return nil, fmt.Errorf("embedded operator manifests are empty; rebuild geass with make build-geass")
	}
	crds, _, _, err := splitOperatorManifests(embeddedOperatorManifests)
	if err != nil {
		return nil, err
	}
	return servedVersions(crds)
}

// k3sPlanProblem says why the K3s part of the plan cannot be carried out. It is not something --force overrides: the
// rules are the ones that protect the datastore (no downgrade, one minor version at a time), and --allow-untested-k3s
// is the one way past the range the add-ons are known to run on.
func k3sPlanProblem(state ClusterState, opts UpgradeOptions) error {
	if opts.K3sVersion != "" {
		return release.CheckK3sUpgrade(state.K3sVersion, opts.K3sVersion, opts.AllowUntestedK3s)
	}
	if minor, err := release.K3sMinor(state.K3sVersion); err == nil && minor < release.K3sMinMinor {
		return fmt.Errorf("this release's add-ons need Kubernetes 1.%d or newer, and this cluster runs K3s %s. "+
			"Upgrade K3s one minor version at a time with --k3s-version first", release.K3sMinMinor, state.K3sVersion)
	}
	return nil
}

// upgradeFindings turns what was found out about the cluster into what stops an upgrade (blockers, which --force goes
// on past) and what should be said about it and nothing more (warnings).
func upgradeFindings(state ClusterState, opts UpgradeOptions) (blockers, warnings []string) {
	if state.Phase != "Ready" {
		blockers = append(blockers, fmt.Sprintf("the cluster is not ready (its phase is %q): an add-on may still be installing or failing, "+
			"and updating the operator on top of that makes it harder to tell which change broke what", state.Phase))
	}
	if len(state.DegradedVolumes) > 0 {
		blockers = append(blockers, "these Longhorn volumes do not have all their copies, and anything that restarts the cluster's components "+
			"risks the copy that is left: "+strings.Join(state.DegradedVolumes, ", "))
	}
	if len(state.NodesNotReady) > 0 {
		if opts.K3sVersion != "" {
			blockers = append(blockers, "these nodes are not ready, so upgrading K3s now could not tell its own problems from theirs: "+
				strings.Join(state.NodesNotReady, ", "))
		} else {
			warnings = append(warnings, "these nodes are not ready: "+strings.Join(state.NodesNotReady, ", "))
		}
	}
	if state.ControlPlanes > 1 {
		warnings = append(warnings, fmt.Sprintf("this cluster has %d control plane nodes; the installer upgrades K3s only on the one it runs on, "+
			"and the others have to be upgraded by hand, one at a time", state.ControlPlanes))
	}
	if len(state.ControlPlanesWithoutNewImage) > 0 {
		warnings = append(warnings, fmt.Sprintf("the new operator can start on any control plane node, and these do not report its image (%s): %s. "+
			"The image is in no registry, only inside this installer, so an operator that starts on one of them cannot pull it, and the update "+
			"rolls back. Run this installer with no options on each of them first (that only puts the image "+
			"there), then run the update here. If you have done that already, this may be the report not having caught up (a node can take "+
			"some seconds to report an image it was just given) or leaving the image out (a node reports at most 50)", state.NewOperatorImage, strings.Join(state.ControlPlanesWithoutNewImage, ", ")))
	}
	if len(state.UnprotectedDatabases) > 0 {
		warnings = append(warnings, "these databases are not backed up, so the etcd snapshot does not cover their data (it holds the cluster's "+
			"objects, not the databases' contents): "+strings.Join(state.UnprotectedDatabases, ", "))
	}
	return blockers, warnings
}

// ---- snapshot -------------------------------------------------------------------------------------------------

// EtcdSnapshot takes a snapshot of the cluster's etcd before anything is changed. A snapshot is what makes an
// upgrade reversible: K3s does not downgrade and the operator refuses to run over data newer than it knows, so the
// way back from a bad upgrade is to restore what the cluster held before.
type EtcdSnapshot struct {
	Plan *UpgradePlan
	// Dir, when it is set, is where the snapshot is written instead of K3s's own snapshot directory. A reinstall uses
	// it because K3s's own is deleted with the rest of its data.
	Dir string
	// Label says what the snapshot is for, and names it.
	Label string
}

func (s *EtcdSnapshot) Name() string { return "snapshot the cluster's etcd" }

// k3sSnapshotDir is where K3s keeps its snapshots unless told otherwise, and where the snapshot of an update goes.
// It is a variable so that tests can point it at a directory of their own.
var k3sSnapshotDir = "/var/lib/rancher/k3s/server/db/snapshots"

// mkdirAll makes a directory; tests replace it.
var mkdirAll = os.MkdirAll

var unsafeNameChars = regexp.MustCompile(`[^a-zA-Z0-9.-]+`)

func snapshotName(label, from, to string) string {
	name := fmt.Sprintf("%s-%s-to-%s", label, orWord(from, "unrecorded"), orWord(to, "unknown"))
	return strings.Trim(unsafeNameChars.ReplaceAllString(name, "-"), "-")
}

func orWord(v, word string) string {
	if strings.TrimSpace(v) == "" {
		return word
	}
	return v
}

func (s *EtcdSnapshot) Run() error {
	plan := s.Plan
	if plan.Options.SkipSnapshot {
		Warnf(s.Name(), "Going on without a snapshot (--skip-snapshot): there is no way back to the cluster as it is now")
		return nil
	}
	label := s.Label
	if label == "" {
		label = "geass-pre-upgrade"
	}
	name := snapshotName(label, plan.State.RecordedRelease(), plan.Target)
	dir := s.Dir
	if dir == "" {
		dir = k3sSnapshotDir
	}
	// The directory is always named, so that a server whose configuration sets etcd-snapshot-dir elsewhere still writes
	// where this step looks for the file.
	if err := mkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	args := []string{"etcd-snapshot", "save", "--name", name, "--dir", dir}
	if _, err := sh.run(s.Name(), commandOptions{echoStdout: false, echoStderr: true}, "k3s", args...); err != nil {
		return fmt.Errorf("could not take an etcd snapshot: %w. K3s only snapshots an embedded etcd datastore, which "+
			"is what Geass installs; if this server was set up some other way and you accept that there is no way back, add --skip-snapshot", err)
	}
	plan.SnapshotDir, plan.SnapshotName = dir, name

	found := latestSnapshot(dir, name)
	if found == "" {
		// A snapshot nobody can find is no way back, and the whole point of taking it was to have one.
		plan.SnapshotDir, plan.SnapshotName = "", ""
		return fmt.Errorf("K3s said it took the snapshot, but no file called %s-* is in %s, so there is no way back to rely on. "+
			"Look at `k3s etcd-snapshot ls`; if you accept going on without one, add --skip-snapshot", name, dir)
	}
	Logf(s.Name(), "Snapshot written to %s", found)
	fmt.Printf("  snapshot: %s\n", found)
	return nil
}

// latestSnapshot is the newest file in dir that starts with name: K3s appends a timestamp to the name it is given.
func latestSnapshot(dir, name string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var best string
	var bestTime time.Time
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), name) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if best == "" || info.ModTime().After(bestTime) {
			best, bestTime = filepath.Join(dir, e.Name()), info.ModTime()
		}
	}
	return best
}

// ---- custom resource definitions and operator -------------------------------------------------------------------

// UpgradeCRDs replaces the custom resource definitions. It is its own step, and waits for the API server to serve
// them, because everything after it (the operator's permissions, the operator) is about the kinds they define.
type UpgradeCRDs struct {
	Plan *UpgradePlan
}

func (s *UpgradeCRDs) Name() string { return "update custom resource definitions" }

func (s *UpgradeCRDs) Run() error {
	crds, _, names, err := splitOperatorManifests(embeddedOperatorManifests)
	if err != nil {
		return err
	}
	Logf(s.Name(), "Applying %d custom resource definitions", len(names))
	// Server-side apply because a definition is large, and the annotation that client-side apply keeps of what it
	// last applied is capped at 256 KiB (the largest of ours is a third of that today). Conflicts with the field
	// manager that applied them at install are forced: they are our own.
	if err := kubectlEcho(s.Name(), strings.NewReader(string(crds)), "apply", "--server-side", "--force-conflicts", "-f", "-"); err != nil {
		return err
	}
	args := []string{"wait", "--for=condition=established", "--timeout=120s"}
	for _, name := range names {
		args = append(args, "crd/"+name)
	}
	if err := kubectlEcho(s.Name(), nil, args...); err != nil {
		return fmt.Errorf("the API server did not start serving the updated definitions: %w", err)
	}
	return nil
}

// UpgradeOperator replaces the operator and what it needs: its namespace, permissions and Deployment, stamped with
// the release and the image so that the Deployment rolls out when either changes.
type UpgradeOperator struct {
	Plan *UpgradePlan
}

func (s *UpgradeOperator) Name() string { return "update geass-operator" }

func (s *UpgradeOperator) Run() error {
	_, rest, _, err := splitOperatorManifests(embeddedOperatorManifests)
	if err != nil {
		return err
	}
	stamped, err := stampOperator(rest, s.Plan.Target, operatorImageDigest())
	if err != nil {
		return err
	}
	Logf(s.Name(), "Applying the operator manifests for %s (%d bytes)", s.Plan.Target, len(stamped))
	return kubectlEcho(s.Name(), strings.NewReader(string(stamped)), "apply", "--server-side", "--force-conflicts", "-f", "-")
}

// ---- waiting for the new operator (takeover.go) ----------------------------------------------------------------------

// SetK3sTarget records the K3s release that the workers are to be brought to, which the operator then does one node at
// a time. It runs after the control plane is on that release: a worker must never be ahead of its server.
type SetK3sTarget struct {
	Plan *UpgradePlan
}

func (s *SetK3sTarget) Name() string { return "set the K3s version for the workers" }

func (s *SetK3sTarget) Run() error {
	if s.Plan.K3sTo == "" {
		return nil
	}
	patch := fmt.Sprintf(`{"spec":{"k3s":{"version":%q}}}`, s.Plan.K3sTo)
	return kubectlEcho(s.Name(), nil, "-n", geassNamespace, "patch", "geasscluster", s.Plan.State.ClusterName, "--type", "merge", "-p", patch)
}

// WaitForClusterReady waits for the cluster to say it is ready again. The upgrade is done whether or not it does: an
// add-on whose chart version moved takes its time, so this reports and does not fail.
type WaitForClusterReady struct {
	Plan    *UpgradePlan
	Timeout time.Duration
}

func (s *WaitForClusterReady) Name() string { return "wait for the cluster to be ready" }

func (s *WaitForClusterReady) Run() error {
	timeout := s.Timeout
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	deadline := time.Now().Add(timeout)
	for {
		out, err := kubectl(s.Name(), nil, "-n", geassNamespace, "get", "geasscluster", s.Plan.State.ClusterName, "-o", "jsonpath={.status.phase}")
		if err == nil && strings.TrimSpace(string(out)) == "Ready" {
			return nil
		}
		if !time.Now().Before(deadline) {
			Warnf(s.Name(), "The cluster is not ready after %s. The update itself is done; an add-on may still be upgrading. "+
				"See: kubectl get helmcharts -n kube-system, and kubectl -n geass-system get geasscluster", timeout)
			return nil
		}
		fmt.Println("  waiting for the cluster to be ready (add-ons may be upgrading)...")
		time.Sleep(pollInterval)
	}
}

// UpgradeSummary says what was done and what is left to do.
type UpgradeSummary struct {
	Plan *UpgradePlan
}

func (s *UpgradeSummary) Name() string { return "summary" }

// controlPlaneMove says what K3s this server ran before the update, and which of the other control plane servers run
// something other than the release it is being moved to, as "name (release)". The cluster is read before anything
// changes, so each server's release is what it was then. Without a release for this server it is the oldest one.
func (p *UpgradePlan) controlPlaneMove() (moved string, others []string) {
	self := thisNode()
	moved = p.State.K3sVersion
	if version := p.State.ControlPlaneVersions[self]; version != "" {
		moved = version
	}
	for name, version := range p.State.ControlPlaneVersions {
		if name != self && version != p.K3sTo {
			others = append(others, name+" ("+orUnknown(version)+")")
		}
	}
	slices.Sort(others)
	return moved, others
}

func (s *UpgradeSummary) Run() error {
	p := s.Plan
	from := release.Describe(p.State.RecordedRelease())
	fmt.Printf("  %s -> %s\n", from, release.Describe(p.Target))
	if p.K3sTo != "" {
		moved, others := p.controlPlaneMove()
		if moved != p.K3sTo {
			fmt.Printf("  K3s (control plane) %s -> %s\n", orUnknown(moved), p.K3sTo)
		}
		if len(others) > 0 {
			fmt.Printf("  The other %d control plane server(s) still run an older K3s: %s. Upgrade each of them by hand, one at a time, and wait for its node to be ready:\n",
				len(others), strings.Join(others, ", "))
			fmt.Printf("    curl -sfL https://get.k3s.io | sudo env INSTALL_K3S_VERSION=%s INSTALL_K3S_EXEC=server sh -\n", p.K3sTo)
		}
		if p.State.Workers > 0 && (len(others) > 0 || moved != p.K3sTo) {
			if len(others) > 0 {
				fmt.Printf("  The operator upgrades the %d worker node(s) to K3s %s, one at a time, once every control plane server runs it. Watch it with:\n", p.State.Workers, p.K3sTo)
			} else {
				fmt.Printf("  The operator now upgrades the %d worker node(s) to K3s %s, one at a time. Watch it with:\n", p.State.Workers, p.K3sTo)
			}
			fmt.Printf("    kubectl -n geass-system get geasscluster %s -o jsonpath='{.status.k3s}'\n", p.State.ClusterName)
		}
	}
	if p.SnapshotName != "" {
		fmt.Printf("  etcd snapshot from before the update: %s\n", p.SnapshotDescription())
	}
	fmt.Println("  Update the command line to the same release on the machines that use it: install-cli.sh.")
	return nil
}
