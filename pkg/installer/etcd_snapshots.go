package installer

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sigs.k8s.io/yaml"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
	"github.com/degoke/geass/pkg/release"
)

// Copies of the cluster's state off the control plane's disk (see pkg/platform/etcd_snapshots.go for how the pieces fit).
//
// This is the host's part, which is the one part that cannot be done from the cluster: K3s is told once, in a file that
// it reads when it starts, to send its snapshots to S3 and to read where from a Secret. It is opt-in, and it is a step of
// its own because it restarts K3s. Where the copies go is the cluster's part, and is set with the destination variables
// below, in the dashboard, or on the GeassCluster.

const (
	etcdSnapshotsDropInFile = "50-geass-etcd-snapshots.yaml"

	// Words of the `etcd-snapshots` command.
	etcdSnapshotsCommand = "etcd-snapshots"
	// EtcdSnapshotsEnable, Disable and Status are what the command can be asked to do.
	EtcdSnapshotsEnable  = "enable"
	EtcdSnapshotsDisable = "disable"
	EtcdSnapshotsStatus  = "status"

	// etcdSnapshotsRetention is how many snapshots K3s keeps, on this machine and in the bucket (the bucket's retention is
	// the same number unless it is set separately, which would make K3s ignore the Secret). K3s keeps five, which at two a
	// day is two and a half days: not enough to notice that something went wrong a week ago. Fourteen is a week, and a
	// snapshot of a small cluster is a few megabytes.
	etcdSnapshotsRetention = 14

	etcdSnapshotsAcknowledgement = "saved"

	// k3sEtcdDataDir exists when K3s's datastore is its embedded etcd, which is the only one that can be snapshotted.
	k3sEtcdDataDir = "/var/lib/rancher/k3s/server/db/etcd"
)

// Where the host's pieces are. Variables, so that tests can point them at a directory of their own.
var (
	k3sConfigDropInDir = "/etc/rancher/k3s/config.yaml.d"
	k3sServerTokenFile = k3sServerTokenPath
	k3sEtcdDataDirPath = k3sEtcdDataDir
)

func etcdSnapshotsDropInPath() string {
	return filepath.Join(k3sConfigDropInDir, etcdSnapshotsDropInFile)
}

// EtcdSnapshotsDropIn is the file that tells K3s to send its snapshots to S3 and to read the settings from the Secret
// that the cluster keeps. It sets no other S3 option: K3s ignores the Secret when it finds one.
func EtcdSnapshotsDropIn() string {
	raw, err := yaml.Marshal(map[string]any{
		"etcd-s3":                 true,
		"etcd-s3-config-secret":   platform.EtcdSnapshotsSecretName,
		"etcd-snapshot-retention": etcdSnapshotsRetention,
	})
	if err != nil {
		return ""
	}
	return "# Written by the Geass installer (etcd-snapshots enable). Remove it with: etcd-snapshots disable.\n" +
		"# K3s sends every snapshot to the S3 bucket named in the Secret kube-system/" + platform.EtcdSnapshotsSecretName + ".\n" +
		string(raw)
}

// EtcdSnapshotsDestination is where the copies go, as given to the installer. It is what the spec.etcdSnapshots of the
// cluster says, and the credentials of the Secret that it names.
type EtcdSnapshotsDestination struct {
	Bucket, Region, Endpoint, Folder string
	Insecure                         bool
	AccessKey, SecretKey             string
}

// EtcdSnapshotsOptions is what the installer was asked about snapshots.
type EtcdSnapshotsOptions struct {
	// Action is the word after `etcd-snapshots`: enable, disable or status. Empty when the command was not used.
	Action string
	// Enable is --etcd-snapshots: a cluster that is being built, or a server that is joining, sends copies from its
	// first start.
	Enable bool
	// Destination is where the copies go, from the environment. Nil when no bucket was given.
	Destination *EtcdSnapshotsDestination
}

// parseEtcdSnapshotsDestination reads GEASS_ETCD_SNAPSHOTS_BUCKET and the variables that go with it. No bucket is not an
// error: the destination can be set later, in the dashboard.
func parseEtcdSnapshotsDestination(getenv func(string) string) (*EtcdSnapshotsDestination, error) {
	get := func(name string) string { return strings.TrimSpace(getenv("GEASS_ETCD_SNAPSHOTS_" + name)) }
	d := &EtcdSnapshotsDestination{
		Bucket: get("BUCKET"), Region: get("REGION"), Endpoint: get("ENDPOINT"), Folder: get("FOLDER"),
		Insecure:  truthy(get("INSECURE")),
		AccessKey: get("ACCESS_KEY"), SecretKey: get("SECRET_KEY"),
	}
	if d.Bucket == "" {
		for _, name := range []string{"REGION", "ENDPOINT", "FOLDER", "ACCESS_KEY", "SECRET_KEY"} {
			if get(name) != "" {
				return nil, fmt.Errorf("GEASS_ETCD_SNAPSHOTS_%s is set without GEASS_ETCD_SNAPSHOTS_BUCKET: say which bucket the copies go to", name)
			}
		}
		return nil, nil
	}
	if err := platform.ValidateEtcdSnapshots(&geassv1alpha1.GeassClusterEtcdSnapshotsSpec{
		Bucket: d.Bucket, Region: d.Region, Endpoint: d.Endpoint, Folder: d.Folder, Insecure: d.Insecure,
	}); err != nil {
		return nil, fmt.Errorf("GEASS_ETCD_SNAPSHOTS_*: %w", err)
	}
	if d.AccessKey == "" || d.SecretKey == "" {
		return nil, errors.New("GEASS_ETCD_SNAPSHOTS_BUCKET needs GEASS_ETCD_SNAPSHOTS_ACCESS_KEY and GEASS_ETCD_SNAPSHOTS_SECRET_KEY: an access key that can " +
			"use that bucket and nothing else. Without them, leave the bucket out and set where copies go in the dashboard")
	}
	return d, nil
}

// k3sSupportsEtcdS3Secret says whether a K3s release can read its S3 settings from a Secret. An unknown flag in a
// configuration file stops K3s from starting, so a release that lacks it must not be given the file. The change shipped
// in v1.31.0 and was backported to v1.30.3.
func k3sSupportsEtcdS3Secret(version string) bool {
	order, err := release.CompareK3s(version, "v1.30.3+k3s1")
	return err == nil && order >= 0
}

// ---- the choice made when a cluster is built --------------------------------------------------------------------------

// EtcdSnapshotsChoice is whether the cluster being built sends copies of its state off the machine. It is made before K3s
// starts, and read by the steps that follow.
type EtcdSnapshotsChoice struct {
	Enabled bool
}

// AskEtcdSnapshots decides whether to send copies, before K3s is installed. It never turns the feature on by itself: it
// is on when it was asked for (--etcd-snapshots), or when the person at the terminal says yes, and off otherwise.
type AskEtcdSnapshots struct {
	Choice *EtcdSnapshotsChoice
	// Flag is --etcd-snapshots.
	Flag bool
	// K3sVersion is the release being installed, empty for the current stable one.
	K3sVersion string
	// NoPrompt means the terminal is not asked: a server that joins a cluster does what the cluster's others do, and
	// that is for the person to say with --etcd-snapshots.
	NoPrompt bool
	// OpenTerminal opens the person's terminal; /dev/tty when nil.
	OpenTerminal func() (io.ReadWriteCloser, error)
}

func (s *AskEtcdSnapshots) Name() string {
	return "choose whether to keep copies of the cluster off this machine"
}

func (s *AskEtcdSnapshots) Run() error {
	if s.Flag {
		if s.K3sVersion != "" && !k3sSupportsEtcdS3Secret(s.K3sVersion) {
			return fmt.Errorf("K3s %s cannot send snapshots to a bucket that the cluster names: that needs v1.30.3+k3s1 or newer. "+
				"Pick a newer --k3s-version, or leave --etcd-snapshots out", s.K3sVersion)
		}
		s.Choice.Enabled = true
		Logf(s.Name(), "Copies of the cluster's state will be sent to S3 (--etcd-snapshots)")
		return nil
	}
	if s.NoPrompt {
		return nil
	}
	open := s.OpenTerminal
	if open == nil {
		open = func() (io.ReadWriteCloser, error) { return os.OpenFile("/dev/tty", os.O_RDWR, 0) }
	}
	tty, err := open()
	if err != nil {
		Logf(s.Name(), "No terminal to ask on, and --etcd-snapshots was not given: no copies of the cluster's state will be kept off this machine")
		return nil
	}
	defer func() { _ = tty.Close() }()
	if s.K3sVersion != "" && !k3sSupportsEtcdS3Secret(s.K3sVersion) {
		return nil
	}
	_, _ = fmt.Fprintf(tty, "\nK3s keeps snapshots of the cluster's database, with every project, secret and database record in it, on this\n"+
		"machine's own disk. If the disk is lost, so are they. Geass can also keep a copy in an S3 bucket of your own, which is\n"+
		"what lets you rebuild the cluster on a new machine. It needs a bucket and an access key that can use only that bucket;\n"+
		"you can say which after the install, in the dashboard. You can also turn this on later.\n\n"+
		"Keep copies of the cluster in your own S3 bucket? [y/N]: ")
	answer, _ := bufio.NewReader(tty).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		s.Choice.Enabled = true
		Logf(s.Name(), "Copies of the cluster's state will be sent to S3 (answered yes)")
	default:
		Logf(s.Name(), "Copies of the cluster's state stay on this machine (answered no)")
	}
	return nil
}

// WriteEtcdSnapshotsDropIn tells K3s to send snapshots, before it starts, when the choice was yes.
type WriteEtcdSnapshotsDropIn struct {
	Choice *EtcdSnapshotsChoice
}

func (s *WriteEtcdSnapshotsDropIn) Name() string { return "tell k3s to send snapshots to S3" }

func (s *WriteEtcdSnapshotsDropIn) Run() error {
	if s.Choice == nil || !s.Choice.Enabled {
		return nil
	}
	return writeEtcdSnapshotsDropIn(s.Name())
}

func writeEtcdSnapshotsDropIn(step string) error {
	path := etcdSnapshotsDropInPath()
	if err := mkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(EtcdSnapshotsDropIn()), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	Logf(step, "Wrote %s", path)
	return nil
}

// ---- the server token --------------------------------------------------------------------------------------------------

// EtcdSnapshotsToken shows the server token, once, on the person's terminal. A snapshot is only readable with the token
// of the cluster it came from: K3s encrypts the cluster's keys inside it with it. The token is on the machine's disk, so
// it is lost with the disk that the copies are for, and the copies are worth nothing without it.
//
// It is written to the terminal and not to the installer's output or its log, which a pipeline may keep. With no terminal
// it is not shown at all, and the step says where it is.
type EtcdSnapshotsToken struct {
	Choice *EtcdSnapshotsChoice
	// OpenTerminal opens the person's terminal; /dev/tty when nil.
	OpenTerminal func() (io.ReadWriteCloser, error)
}

func (s *EtcdSnapshotsToken) Name() string { return "keep the cluster's server token" }

func (s *EtcdSnapshotsToken) Run() error {
	if s.Choice != nil && !s.Choice.Enabled {
		return nil
	}
	return showServerToken(s.Name(), s.OpenTerminal)
}

func showServerToken(step string, openTerminal func() (io.ReadWriteCloser, error)) error {
	raw, err := os.ReadFile(k3sServerTokenFile)
	if err != nil {
		return fmt.Errorf("read the server token: %w", err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return fmt.Errorf("the server token file %s is empty", k3sServerTokenFile)
	}
	open := openTerminal
	if open == nil {
		open = func() (io.ReadWriteCloser, error) { return os.OpenFile("/dev/tty", os.O_RDWR, 0) }
	}
	tty, err := open()
	if err != nil {
		Warnf(step, "There is no terminal to show the cluster's server token on. Copy it from %s to somewhere that is not this machine "+
			"(a password manager): without it a copy of the cluster cannot be restored", k3sServerTokenFile)
		return nil
	}
	defer func() { _ = tty.Close() }()
	_, _ = fmt.Fprintf(tty, "\nThe cluster's server token is:\n\n    %s\n\n"+
		"Store it in a password manager, not on this machine. K3s encrypts the cluster's keys inside every snapshot with it,\n"+
		"so a copy of the cluster cannot be restored without it, and it is on the disk that the copies are meant to outlive.\n"+
		"It is also in %s. Press Enter once it is stored (type %q to say so): ", token, k3sServerTokenFile, etcdSnapshotsAcknowledgement)
	answer, _ := bufio.NewReader(tty).ReadString('\n')
	if strings.EqualFold(strings.TrimSpace(answer), etcdSnapshotsAcknowledgement) || strings.TrimSpace(answer) == "" {
		Logf(step, "The server token was shown on the terminal")
		return nil
	}
	Warnf(step, "The server token was shown on the terminal, and not acknowledged: store it from %s before you rely on the copies", k3sServerTokenFile)
	return nil
}

// ---- turning it on, off and looking at it, on a machine that has K3s --------------------------------------------------

// EtcdSnapshotsSteps is what `etcd-snapshots <action>` does on a control plane server.
func EtcdSnapshotsSteps(opts Options) []Step {
	switch opts.EtcdSnapshots.Action {
	case EtcdSnapshotsDisable:
		return []Step{&EtcdSnapshotsHostCheck{}, &DisableEtcdSnapshots{}}
	case EtcdSnapshotsStatus:
		return []Step{&EtcdSnapshotsHostCheck{}, &ShowEtcdSnapshots{}}
	default:
		return []Step{
			&EtcdSnapshotsHostCheck{Enabling: true},
			&EnableEtcdSnapshots{},
			&EtcdSnapshotsToken{},
			&SetEtcdSnapshotsDestination{Destination: opts.EtcdSnapshots.Destination},
			&TakeFirstEtcdSnapshot{Destination: opts.EtcdSnapshots.Destination},
		}
	}
}

// EtcdSnapshotsHostCheck refuses a machine that cannot send snapshots: a worker, a K3s that does not run its own etcd, a
// K3s that does not know the setting.
type EtcdSnapshotsHostCheck struct {
	// Enabling says that the machine is about to be changed, so that its K3s has to know the setting.
	Enabling bool
}

func (s *EtcdSnapshotsHostCheck) Name() string { return "check that this server can send snapshots" }

func (s *EtcdSnapshotsHostCheck) Run() error {
	if _, err := os.Stat("/etc/systemd/system/k3s-agent.service"); err == nil {
		return errors.New("this machine is a worker: it runs the K3s agent, which has no database to snapshot. Run this on a control plane server")
	}
	if _, err := os.Stat(k3sEtcdDataDirPath); err != nil {
		return fmt.Errorf("this K3s does not keep its data in its own embedded etcd (%s is not here), which is the only datastore K3s can send "+
			"snapshots of. Geass installs it that way, so a K3s that was set up another way has to be backed up by whatever set it up", k3sEtcdDataDirPath)
	}
	if !s.Enabling {
		return nil
	}
	out, err := sh.run(s.Name(), commandOptions{}, "k3s", "server", "--help")
	if err != nil {
		return fmt.Errorf("could not ask this K3s what it supports: %w", err)
	}
	if !strings.Contains(string(out), "--etcd-s3-config-secret") {
		return errors.New("this K3s cannot read where to send snapshots from a Secret (--etcd-s3-config-secret, from v1.30.3+k3s1 and v1.31.0), " +
			"and a configuration file that names a setting it does not know stops it from starting. Upgrade K3s first (--k3s-version)")
	}
	return nil
}

// EnableEtcdSnapshots writes the drop-in and restarts K3s, which reads it only when it starts. The API server is away for
// the length of the restart, and running containers are not stopped.
type EnableEtcdSnapshots struct{}

func (s *EnableEtcdSnapshots) Name() string { return "send k3s snapshots to S3" }

func (s *EnableEtcdSnapshots) Run() error {
	path := etcdSnapshotsDropInPath()
	if existing, err := os.ReadFile(path); err == nil && string(existing) == EtcdSnapshotsDropIn() {
		Logf(s.Name(), "%s is already in place", path)
		fmt.Println("  K3s on this server already sends snapshots to S3")
		return nil
	}
	if err := writeEtcdSnapshotsDropIn(s.Name()); err != nil {
		return err
	}
	return restartK3sServer(s.Name())
}

// DisableEtcdSnapshots takes the drop-in away and restarts K3s. Copies that are already in the bucket stay there.
type DisableEtcdSnapshots struct{}

func (s *DisableEtcdSnapshots) Name() string { return "stop sending k3s snapshots to S3" }

func (s *DisableEtcdSnapshots) Run() error {
	path := etcdSnapshotsDropInPath()
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Println("  K3s on this server was not sending snapshots to S3")
			return nil
		}
		return fmt.Errorf("remove %s: %w", path, err)
	}
	Logf(s.Name(), "Removed %s", path)
	if err := restartK3sServer(s.Name()); err != nil {
		return err
	}
	fmt.Println("  Copies already in the bucket are left there. To stop the operator looking for them, clear spec.etcdSnapshots on the GeassCluster.")
	return nil
}

// restartK3sServer restarts K3s and waits for its API server.
func restartK3sServer(step string) error {
	fmt.Println("  restarting K3s: the API server is away for a minute, and running workloads are not stopped")
	if _, err := sh.run(step, commandOptions{echoStderr: true}, "systemctl", "restart", "k3s"); err != nil {
		return fmt.Errorf("restart K3s: %w. The drop-in is in place; K3s starts with it the next time it is restarted", err)
	}
	deadline := time.Now().Add(5 * time.Minute)
	var lastErr error
	for {
		if _, err := kubectl(step, nil, "get", "--raw=/readyz"); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("K3s did not answer within 5 minutes of its restart: %v. See `journalctl -u k3s`", lastErr)
		}
		fmt.Println("  waiting for the K3s API server...")
		time.Sleep(pollInterval)
	}
}

// ShowEtcdSnapshots says what this server does about snapshots, and what the cluster says about the bucket.
type ShowEtcdSnapshots struct{}

func (s *ShowEtcdSnapshots) Name() string { return "look at the cluster's snapshots" }

func (s *ShowEtcdSnapshots) Run() error {
	if _, err := os.Stat(etcdSnapshotsDropInPath()); err == nil {
		fmt.Println("  this server: sends its snapshots to S3 (" + etcdSnapshotsDropInPath() + ")")
	} else {
		fmt.Println("  this server: does not send snapshots to S3. Turn it on with `etcd-snapshots enable`")
	}
	out, err := kubectl(s.Name(), nil, "get", "geassclusters.geass.geass.dev", "-n", geassNamespace, "-o", "json")
	if err != nil {
		fmt.Printf("  the cluster: could not be read (%v)\n", err)
		return nil
	}
	var list struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Spec struct {
				EtcdSnapshots *struct {
					Bucket, Region, Endpoint, Folder string
				} `json:"etcdSnapshots"`
			} `json:"spec"`
			Status struct {
				Conditions []struct {
					Type, Status, Reason, Message string
				} `json:"conditions"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &list); err != nil || len(list.Items) == 0 {
		fmt.Println("  the cluster: has no Geass cluster object")
		return nil
	}
	cluster := list.Items[0]
	if cluster.Spec.EtcdSnapshots == nil {
		fmt.Println("  the cluster: has no bucket for copies yet. Set one in the dashboard, or run `etcd-snapshots enable` with GEASS_ETCD_SNAPSHOTS_BUCKET, " +
			"GEASS_ETCD_SNAPSHOTS_ACCESS_KEY and GEASS_ETCD_SNAPSHOTS_SECRET_KEY")
		return nil
	}
	e := cluster.Spec.EtcdSnapshots
	fmt.Printf("  the cluster: copies go to bucket %s, folder %s\n", e.Bucket, orWord(e.Folder, platform.DefaultEtcdSnapshotsFolder))
	for _, c := range cluster.Status.Conditions {
		if c.Type == platform.ConditionEtcdSnapshotsCurrent {
			fmt.Printf("  the bucket:  %s (%s)\n", c.Message, c.Reason)
		}
	}
	return nil
}

// SetEtcdSnapshotsDestination tells the cluster where the copies go, from the GEASS_ETCD_SNAPSHOTS_* variables. With no
// bucket given it only says how to, because the destination can as well be set from the dashboard.
type SetEtcdSnapshotsDestination struct {
	// Choice is whether the cluster being built sends copies: nil on a cluster that is here, where the command is the choice.
	Choice      *EtcdSnapshotsChoice
	Destination *EtcdSnapshotsDestination
	// ClusterName is the GeassCluster to set it on; the one the installer made when empty.
	ClusterName string
}

func (s *SetEtcdSnapshotsDestination) Name() string { return "tell the cluster where snapshots go" }

func (s *SetEtcdSnapshotsDestination) Run() error {
	if s.Choice != nil && !s.Choice.Enabled {
		return nil
	}
	d := s.Destination
	if d == nil {
		// A cluster that was rebuilt from a copy of itself has the bucket already: it was in the copy.
		if bucket, ok := clusterEtcdSnapshotsBucket(s.Name()); ok {
			fmt.Printf("  The cluster already says where copies go: bucket %s\n", bucket)
			return nil
		}
		fmt.Println("  Where the copies go is not set yet. Set the bucket and its access key in the dashboard (Settings > Cluster backup), " +
			"or run `etcd-snapshots enable` again with GEASS_ETCD_SNAPSHOTS_BUCKET, _ACCESS_KEY and _SECRET_KEY")
		return nil
	}
	name := s.ClusterName
	if name == "" {
		name = "default"
	}
	secret, err := json.Marshal(map[string]any{
		"apiVersion": "v1", "kind": "Secret",
		"metadata": map[string]any{"name": platform.EtcdSnapshotCredentialsSecretName, "namespace": geassNamespace},
		"data":     encodedData(platform.EtcdSnapshotsCredentialsData(d.AccessKey, d.SecretKey)),
	})
	if err != nil {
		return fmt.Errorf("render the credentials: %w", err)
	}
	// The key goes in on stdin, which the installer's log does not record.
	if err := kubectlEcho(s.Name(), strings.NewReader(string(secret)), "apply", "-f", "-"); err != nil {
		return err
	}
	spec := map[string]any{"bucket": d.Bucket}
	for key, value := range map[string]string{"region": d.Region, "endpoint": d.Endpoint, "folder": d.Folder} {
		if value != "" {
			spec[key] = value
		}
	}
	if d.Insecure {
		spec["insecure"] = true
	}
	patch, err := json.Marshal(map[string]any{"spec": map[string]any{"etcdSnapshots": spec}})
	if err != nil {
		return fmt.Errorf("render the destination: %w", err)
	}
	return kubectlEcho(s.Name(), nil, "patch", "geassclusters.geass.geass.dev", name, "-n", geassNamespace, "--type", "merge", "-p", string(patch))
}

// clusterEtcdSnapshotsBucket is the bucket the cluster already sends copies to, if it says one.
func clusterEtcdSnapshotsBucket(step string) (string, bool) {
	out, err := kubectl(step, nil, "get", "geassclusters.geass.geass.dev", "-n", geassNamespace, "-o", "json")
	if err != nil {
		return "", false
	}
	var list struct {
		Items []struct {
			Spec struct {
				EtcdSnapshots *struct {
					Bucket string `json:"bucket"`
				} `json:"etcdSnapshots"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out, &list); err != nil || len(list.Items) == 0 || list.Items[0].Spec.EtcdSnapshots == nil || list.Items[0].Spec.EtcdSnapshots.Bucket == "" {
		return "", false
	}
	return list.Items[0].Spec.EtcdSnapshots.Bucket, true
}

func encodedData(data map[string][]byte) map[string]string {
	out := make(map[string]string, len(data))
	for key, value := range data {
		out[key] = base64.StdEncoding.EncodeToString(value)
	}
	return out
}

// TakeFirstEtcdSnapshot takes a copy at once, once the cluster has told K3s where copies go. K3s takes its snapshots at 00:00 and
// 12:00, and the one before the destination was set stays on the disk, so without this the first copy in the bucket is up to
// half a day away and nobody knows until then whether it works. It is only an attempt: a failure is said, and is not the
// install's.
type TakeFirstEtcdSnapshot struct {
	Choice      *EtcdSnapshotsChoice
	Destination *EtcdSnapshotsDestination
	// Wait is how long to wait for the operator to write the settings that K3s reads; three minutes when zero.
	Wait time.Duration
}

func (s *TakeFirstEtcdSnapshot) Name() string { return "take the first copy of the cluster" }

func (s *TakeFirstEtcdSnapshot) Run() error {
	if s.Choice != nil && !s.Choice.Enabled {
		return nil
	}
	var bucket string
	if s.Destination != nil {
		bucket = s.Destination.Bucket
	} else {
		// With no destination given, the copy is taken when the cluster already has one (see SetEtcdSnapshotsDestination).
		var ok bool
		if bucket, ok = clusterEtcdSnapshotsBucket(s.Name()); !ok {
			return nil
		}
	}
	wait := s.Wait
	if wait == 0 {
		wait = 3 * time.Minute
	}
	deadline := time.Now().Add(wait)
	for {
		if _, err := kubectl(s.Name(), nil, "-n", platform.EtcdSnapshotsSecretNamespace, "get", "secret", platform.EtcdSnapshotsSecretName, "-o", "name"); err == nil {
			break
		}
		if !time.Now().Before(deadline) {
			Warnf(s.Name(), "The operator has not written the settings that K3s reads within %s, so no copy was taken now. K3s takes one at "+
				"00:00 or 12:00, server time; to take one yourself, run `sudo k3s etcd-snapshot save`", wait)
			return nil
		}
		fmt.Println("  waiting for the operator to say where copies go...")
		time.Sleep(pollInterval)
	}
	if _, err := sh.run(s.Name(), commandOptions{echoStderr: true}, "k3s", "etcd-snapshot", "save", "--name", "geass-first-copy"); err != nil {
		Warnf(s.Name(), "The first copy could not be taken: %v. Look at `sudo journalctl -u k3s`; K3s tries again at 00:00 or 12:00, server time", err)
		return nil
	}
	fmt.Printf("  a copy of the cluster is in bucket %s\n", bucket)
	return nil
}
