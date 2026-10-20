package installer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/degoke/geass/pkg/migrations"
	"github.com/degoke/geass/pkg/release"
)

// fakeHost stands in for the machine: it records every command the steps run and answers them from a script.
type fakeHost struct {
	mu    sync.Mutex
	calls []fakeCall
	// answer returns what a command prints, or an error. Unanswered commands print nothing and succeed.
	answer func(c fakeCall) ([]byte, error)
}

type fakeCall struct {
	Name  string
	Args  []string
	Stdin string
	// Env is the label the step gave its environment overrides, and FullEnv the environment the command really gets.
	Env     []string
	FullEnv []string
}

func (c fakeCall) String() string { return c.Name + " " + strings.Join(c.Args, " ") }

// text is the command and what was fed to it, for looking for something in either.
func (c fakeCall) text() string { return c.String() + "\n" + c.Stdin }

func (f *fakeHost) run(_ string, opts commandOptions, name string, args ...string) ([]byte, error) {
	call := fakeCall{Name: name, Args: args}
	if opts.stdin != nil {
		data, _ := io.ReadAll(opts.stdin)
		call.Stdin = string(data)
	}
	call.Env = opts.envLog
	call.FullEnv = opts.env
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
	if f.answer == nil {
		return nil, nil
	}
	return f.answer(call)
}

// commands lists what was run, one line each.
func (f *fakeHost) commands() []string {
	out := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		out = append(out, c.String())
	}
	return out
}

// ran reports whether any command contained all of the words.
func (f *fakeHost) ran(words ...string) bool { return f.index(words...) >= 0 }

// index is the position of the first command that contains all of the words, or -1.
func (f *fakeHost) index(words ...string) int {
	for i, c := range f.calls {
		line := c.text()
		ok := true
		for _, w := range words {
			if !strings.Contains(line, w) {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

// useHost makes the steps talk to the fake, and makes everything that waits quick.
func useHost(t *testing.T, f *fakeHost) {
	t.Helper()
	oldHost, oldPoll := sh, pollInterval
	sh, pollInterval = f, time.Millisecond
	t.Cleanup(func() { sh, pollInterval = oldHost, oldPoll })
}

// useManifests makes the installer carry these manifests and this image.
func useManifests(t *testing.T, manifests, image string) {
	t.Helper()
	oldManifests, oldImage := embeddedOperatorManifests, embeddedOperatorImageTar
	embeddedOperatorManifests, embeddedOperatorImageTar = []byte(manifests), []byte(image)
	t.Cleanup(func() { embeddedOperatorManifests, embeddedOperatorImageTar = oldManifests, oldImage })
}

// useRelease makes this installer a release.
func useRelease(t *testing.T, version string) {
	t.Helper()
	old := release.Version
	release.Version = version
	t.Cleanup(func() { release.Version = old })
}

const testManifests = `apiVersion: v1
kind: Namespace
metadata:
  name: geass-system
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: geassclusters.geass.geass.dev
spec:
  group: geass.geass.dev
  versions:
  - name: v1alpha1
    served: true
    storage: true
---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: geassapps.geass.geass.dev
spec:
  group: geass.geass.dev
  versions:
  - name: v1alpha1
    served: true
    storage: true
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: geass-manager-role
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: geass-controller-manager
  namespace: geass-system
spec:
  template:
    metadata:
      annotations:
        kubectl.kubernetes.io/default-container: manager
    spec:
      containers:
      - name: manager
        image: geass-operator:v0.3.0
`

// ---- a cluster to upgrade --------------------------------------------------------------------------------------------

// testCluster is what the fake API server says is in the cluster.
type testCluster struct {
	release        string // status.release of the cluster object
	phase          string
	k3s            string
	notReady       []string
	volumes        []string // "name:state:robustness"
	databases      []string // "name:engine:backupState"
	storedVersions string
	schemaVersion  int
	// after the operator is replaced, the cluster reports this release.
	reportsAfterApply string

	// A new operator that goes wrong: its rollout never finishes, or its pod crash loops. schemaRecord is what the
	// ConfigMap that holds the data's schema level says ("" is no ConfigMap).
	rolloutBroken, crashLoop bool
	// slowRollout is how many times the rollout is still not finished when asked, for a new operator that is healthy and
	// busy with its data migrations.
	slowRollout  int
	schemaRecord string
	// What a rollback finds: it fails, or the previous operator cannot start (its image is gone).
	undoErr        error
	previousBroken bool

	applied, undone bool
}

// newOperatorRuns says that the Deployment is the one that this update applied: after the apply, and until it is undone.
func (c *testCluster) newOperatorRuns() bool { return c.applied && !c.undone }

func (c *testCluster) answer(call fakeCall) ([]byte, error) {
	line := call.String()
	switch {
	case strings.Contains(line, "get crd geassclusters"):
		return []byte("customresourcedefinition/geassclusters.geass.geass.dev"), nil
	case strings.Contains(line, "get geassclusters.geass.geass.dev") && strings.Contains(line, "-o json"):
		return json.Marshal(map[string]any{"items": []any{map[string]any{
			"metadata": map[string]any{"name": "default"},
			"status":   map[string]any{"phase": c.phase, "release": c.release, "schemaVersion": max(c.schemaVersion, 1)},
		}}})
	case strings.Contains(line, "get deployments"):
		image, annotations := "geass-operator:v0.0.1", map[string]any{}
		if c.newOperatorRuns() {
			image = "geass-operator:v0.3.0"
			annotations = map[string]any{"geass.dev/release": "v0.3.0", "geass.dev/operator-image-sha256": "abc"}
		}
		return json.Marshal(map[string]any{"items": []any{map[string]any{
			"metadata": map[string]any{"name": "geass-controller-manager"},
			"spec": map[string]any{"template": map[string]any{
				"metadata": map[string]any{"annotations": annotations},
				"spec":     map[string]any{"containers": []any{map[string]any{"name": "manager", "image": image}}},
			}},
		}}})
	case strings.Contains(line, "rollout status"):
		if c.slowRollout > 0 && c.newOperatorRuns() {
			c.slowRollout--
			return nil, errors.New("error: timed out waiting for the condition")
		}
		if c.rolloutBroken && c.newOperatorRuns() || c.previousBroken && c.undone {
			return nil, errors.New("error: timed out waiting for the condition")
		}
	case strings.Contains(line, "rollout undo"):
		if c.undoErr != nil {
			return nil, c.undoErr
		}
		c.undone = true
	case strings.Contains(line, "get pods") && strings.Contains(line, "-o json"):
		waiting := func(reason string) []byte {
			out, _ := json.Marshal(map[string]any{"items": []any{map[string]any{
				"metadata": map[string]any{"name": "geass-controller-manager-new"},
				"status": map[string]any{"containerStatuses": []any{map[string]any{
					"name": "manager", "restartCount": 3, "state": map[string]any{"waiting": map[string]any{"reason": reason, "message": "boom"}},
				}}},
			}}})
			return out
		}
		switch {
		case c.crashLoop && c.newOperatorRuns():
			return waiting("CrashLoopBackOff"), nil
		case c.previousBroken && c.undone:
			return waiting("ImagePullBackOff"), nil
		}
		return []byte(`{"items": []}`), nil
	case strings.Contains(line, "get pods"):
		return []byte("NAME   READY   STATUS\ngeass-controller-manager-new   0/1   CrashLoopBackOff\n"), nil
	case strings.Contains(line, "logs deployment/geass-controller-manager"):
		return []byte("level=error msg=\"could not prepare the cluster's data\"\n"), nil
	case strings.Contains(line, "get configmap geass-schema"):
		if c.schemaRecord == "" {
			return nil, errors.New(`Error from server (NotFound): configmaps "geass-schema" not found`)
		}
		return []byte(c.schemaRecord), nil
	case strings.Contains(line, "get nodes"):
		ready := func(name string) string {
			if slices.Contains(c.notReady, name) {
				return "False"
			}
			return "True"
		}
		return json.Marshal(map[string]any{"items": []any{
			map[string]any{
				"metadata": map[string]any{"name": "cp", "labels": map[string]any{"node-role.kubernetes.io/control-plane": "true"}},
				"status": map[string]any{
					"conditions": []any{map[string]any{"type": "Ready", "status": ready("cp")}},
					"nodeInfo":   map[string]any{"kubeletVersion": c.k3s},
				},
			},
			map[string]any{
				"metadata": map[string]any{"name": "w1"},
				"status": map[string]any{
					"conditions": []any{map[string]any{"type": "Ready", "status": ready("w1")}},
					"nodeInfo":   map[string]any{"kubeletVersion": c.k3s},
				},
			},
		}})
	case strings.Contains(line, "get volumes.longhorn.io"):
		var items []any
		for _, v := range c.volumes {
			parts := strings.Split(v, ":")
			items = append(items, map[string]any{
				"metadata": map[string]any{"name": parts[0]},
				"status":   map[string]any{"state": parts[1], "robustness": parts[2]},
			})
		}
		return json.Marshal(map[string]any{"items": items})
	case strings.Contains(line, "get geassdatabases.geass.geass.dev"):
		var items []any
		for _, d := range c.databases {
			parts := strings.Split(d, ":")
			meta := map[string]any{"name": parts[0]}
			if len(parts) > 3 && parts[3] == "deleting" {
				meta["deletionTimestamp"] = "2026-10-13T00:00:00Z"
			}
			item := map[string]any{"metadata": meta, "spec": map[string]any{"engine": "Postgres", "placement": parts[1]}}
			if parts[2] != "" {
				item["status"] = map[string]any{"backup": map[string]any{"state": parts[2]}}
			}
			items = append(items, item)
		}
		return json.Marshal(map[string]any{"items": items})
	case line == "k3s kubectl get crd -o json":
		stored := c.storedVersions
		if stored == "" {
			stored = "v1alpha1"
		}
		return json.Marshal(map[string]any{"items": []any{
			map[string]any{
				"metadata": map[string]any{"name": "geassclusters.geass.geass.dev"},
				"spec":     map[string]any{"group": "geass.geass.dev"},
				"status":   map[string]any{"storedVersions": strings.Split(stored, ",")},
			},
			map[string]any{
				"metadata": map[string]any{"name": "helmcharts.helm.cattle.io"},
				"spec":     map[string]any{"group": "helm.cattle.io"},
				"status":   map[string]any{"storedVersions": []string{"v1"}},
			},
		}})
	case strings.Contains(line, "apply --server-side") && strings.Contains(call.Stdin, "kind: Deployment"):
		c.applied = true
	case strings.Contains(line, "jsonpath={.status.release}"):
		if c.newOperatorRuns() && c.reportsAfterApply != "" {
			return []byte(c.reportsAfterApply), nil
		}
		return []byte(c.release), nil
	case strings.Contains(line, "jsonpath={.status.phase}"):
		return []byte(c.phase), nil
	}
	return nil, nil
}

func healthyCluster() *testCluster {
	return &testCluster{release: "v0.2.0", phase: "Ready", k3s: "v1.34.2+k3s1", reportsAfterApply: "v0.3.0"}
}

func setUpUpgrade(t *testing.T, cluster *testCluster) *fakeHost {
	t.Helper()
	host := &fakeHost{answer: cluster.answer}
	useHost(t, host)
	useManifests(t, testManifests, "image-bytes")
	useRelease(t, "v0.3.0")
	useSnapshotDir(t, host)
	return host
}

// useSnapshotDir points the update's snapshots at a directory of the test's own, and makes the fake K3s write the
// file that `k3s etcd-snapshot save` writes there: the name it was given and a timestamp.
func useSnapshotDir(t *testing.T, host *fakeHost) {
	t.Helper()
	dir := t.TempDir()
	old := k3sSnapshotDir
	k3sSnapshotDir = dir
	t.Cleanup(func() { k3sSnapshotDir = old })
	inner := host.answer
	host.answer = func(c fakeCall) ([]byte, error) {
		if c.Name == "k3s" && len(c.Args) > 1 && c.Args[0] == "etcd-snapshot" && c.Args[1] == "save" {
			var name, to string
			for i, a := range c.Args {
				if a == "--name" {
					name = c.Args[i+1]
				}
				if a == "--dir" {
					to = c.Args[i+1]
				}
			}
			if to == dir {
				if err := os.WriteFile(filepath.Join(dir, name+"-node-1791879317"), []byte("etcd"), 0o600); err != nil {
					return nil, err
				}
			}
			return nil, nil
		}
		if inner == nil {
			return nil, nil
		}
		return inner(c)
	}
}

// ---- the order of an upgrade -------------------------------------------------------------------------------------------

func runSteps(plan *UpgradePlan) error {
	for _, step := range UpgradeSteps(plan) {
		// What waits does not wait long.
		switch w := step.(type) {
		case *OperatorTakeover:
			w.RolloutTimeout, w.ReleaseTimeout, w.RollbackTimeout = 100*time.Millisecond, 100*time.Millisecond, 100*time.Millisecond
		case *WaitForClusterReady:
			w.Timeout = 50 * time.Millisecond
		}
		if err := step.Run(); err != nil {
			return fmt.Errorf("%s: %w", step.Name(), err)
		}
	}
	return nil
}

func TestAnUpgradeSnapshotsFirstAndReplacesDefinitionsBeforeTheOperator(t *testing.T) {
	host := setUpUpgrade(t, healthyCluster())

	require.NoError(t, runSteps(NewUpgradePlan(UpgradeOptions{})))

	snapshot := host.index("etcd-snapshot save")
	importImage := host.index("ctr image import")
	crds := host.index("apply --server-side", "kind: CustomResourceDefinition")
	established := host.index("wait --for=condition=established")
	operator := host.index("apply --server-side", "kind: Deployment")
	rollout := host.index("rollout status")

	for name, i := range map[string]int{"snapshot": snapshot, "image import": importImage, "definitions": crds, "establish wait": established, "operator": operator, "rollout": rollout} {
		require.GreaterOrEqual(t, i, 0, "%s never ran; ran: %v", name, host.commands())
	}
	require.Less(t, snapshot, importImage, "the snapshot comes before anything is changed")
	require.Less(t, snapshot, crds)
	require.Less(t, crds, established, "definitions are waited for")
	require.Less(t, established, operator, "the operator is replaced only once its kinds are served")
	require.Less(t, operator, rollout)
	require.False(t, host.ran("ClusterRole", "CustomResourceDefinition"), "definitions and the operator's manifests go in separate applies")
}

func TestAnUpgradeNeverRemovesK3sOrRewritesTheClusterObject(t *testing.T) {
	host := setUpUpgrade(t, healthyCluster())
	require.NoError(t, runSteps(NewUpgradePlan(UpgradeOptions{})))

	for _, c := range host.calls {
		require.NotContains(t, c.String(), "uninstall", "an upgrade has no business uninstalling K3s")
		require.NotContains(t, c.String(), "get.k3s.io", "and no business running K3s's installer when K3s is left alone")
		require.False(t, strings.Contains(c.Stdin, "kind: GeassCluster"), "the cluster object holds the cluster's settings, which the installer must not overwrite")
	}
	require.False(t, host.ran("delete"), "nothing is deleted")
}

func TestTheOperatorIsStampedSoItRollsOutEvenWhenTheImageNameDoesNot(t *testing.T) {
	host := setUpUpgrade(t, healthyCluster())
	require.NoError(t, runSteps(NewUpgradePlan(UpgradeOptions{})))

	i := host.index("apply --server-side", "kind: Deployment")
	require.GreaterOrEqual(t, i, 0)
	manifest := host.calls[i].Stdin
	require.Contains(t, manifest, "geass.dev/release: v0.3.0")
	require.Contains(t, manifest, "geass.dev/operator-image-sha256: "+digestOf([]byte("image-bytes")))
	require.Contains(t, manifest, "kubectl.kubernetes.io/default-container", "what was already there is kept")
}

func TestAnUpgradeWaitsForTheNewOperatorToReportItself(t *testing.T) {
	cluster := healthyCluster()
	cluster.reportsAfterApply = "v0.2.0" // the old release keeps being what the cluster says: the new one never takes over
	host := setUpUpgrade(t, cluster)

	err := runSteps(NewUpgradePlan(UpgradeOptions{}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "wait for the new operator")
	require.Contains(t, err.Error(), "did not report release v0.3.0")
	require.True(t, host.ran("logs", "deployment/geass-controller-manager"), "and the operator's log is read to say why")
}

// ---- when the new operator does not take over ---------------------------------------------------------------------------------

func TestANewOperatorThatCannotStartIsRolledBackAndTheUpdateGoesNoFurther(t *testing.T) {
	cluster := healthyCluster()
	cluster.crashLoop, cluster.rolloutBroken = true, true
	cluster.reportsAfterApply = "" // it never reports
	cluster.schemaRecord = "1"
	host := setUpUpgrade(t, cluster)

	err := runSteps(NewUpgradePlan(UpgradeOptions{}))

	require.Error(t, err, "an update whose operator did not take over has not succeeded")
	require.Contains(t, err.Error(), "rolled the operator back")
	require.Contains(t, err.Error(), "geass-operator:v0.0.1", "and says to what")
	require.Contains(t, err.Error(), "could not prepare the cluster's data", "and why the new one failed")
	require.True(t, cluster.undone)

	undo := host.index("rollout undo")
	require.GreaterOrEqual(t, undo, 0, "%v", host.commands())
	require.Less(t, host.index("logs deployment/geass-controller-manager"), undo, "the log is read before the pod that holds it is replaced")
	require.Less(t, host.index("--previous"), undo, "the crashed container's too")
	require.False(t, host.ran("patch geasscluster"), "nothing after the takeover runs: not the K3s target for the workers")
	require.False(t, host.ran("jsonpath={.status.phase}"), "and the installer does not wait for a cluster it is leaving as it found it")
}

func TestACrashLoopIsNoticedWithoutWaitingOutTheWholeTimeout(t *testing.T) {
	cluster := healthyCluster()
	cluster.crashLoop, cluster.rolloutBroken = true, true
	setUpUpgrade(t, cluster)

	start := time.Now()
	err := (&OperatorTakeover{Plan: planAfterPreflight(t), RolloutTimeout: time.Hour, ReleaseTimeout: time.Hour}).Run()
	require.Error(t, err)
	require.Contains(t, err.Error(), "has crashed 3 times")
	require.Less(t, time.Since(start), 10*time.Second, "it did not wait an hour for something that will not change")
}

func TestANewOperatorThatIsOnlySlowToStartIsWaitedForAndNotRolledBack(t *testing.T) {
	// Its migrations are running: the pod is not ready, it is not crash looping, and it has not reported its release.
	cluster := healthyCluster()
	cluster.slowRollout = 200
	host := setUpUpgrade(t, cluster)
	plan := planAfterPreflight(t)

	err := (&OperatorTakeover{Plan: plan, RolloutTimeout: time.Minute, ReleaseTimeout: time.Minute}).Run()

	require.NoError(t, err)
	require.False(t, host.ran("rollout undo"), "an operator that is busy is not a failed one")
}

func TestTheOperatorOfAFreshInstallThatCrashLoopsIsReportedAtOnceWithWhy(t *testing.T) {
	cluster := healthyCluster()
	cluster.applied, cluster.crashLoop, cluster.rolloutBroken = true, true, true
	useHost(t, &fakeHost{answer: cluster.answer})

	start := time.Now()
	err := (&WaitForOperator{}).Run()

	require.ErrorContains(t, err, "has crashed 3 times")
	require.Less(t, time.Since(start), 10*time.Second, "it did not wait out its five minutes")
}

func TestTheNewOperatorIsGivenAsLongAsItsStartupProbeAllows(t *testing.T) {
	// The probe is what keeps a slow migration from being killed; the installer must not be the one that gives up first.
	manifest, err := os.ReadFile("../../config/manager/manager.yaml")
	require.NoError(t, err)
	probe := regexp.MustCompile(`startupProbe:[^\n]*\n(?:\s+[^\n]*\n)*?\s+periodSeconds: (\d+)\n\s+failureThreshold: (\d+)`).FindStringSubmatch(string(manifest))
	require.NotNil(t, probe, "the startupProbe of the manager is not where this test looks for it")
	period, _ := strconv.Atoi(probe[1])
	threshold, _ := strconv.Atoi(probe[2])

	require.Greater(t, defaultRolloutTimeout, time.Duration(period*threshold)*time.Second)
}

// planAfterPreflight is a plan that has been through the first step, and the apply of the new operator.
func planAfterPreflight(t *testing.T) *UpgradePlan {
	t.Helper()
	plan := NewUpgradePlan(UpgradeOptions{})
	require.NoError(t, (&UpgradePreflight{Plan: plan}).Run())
	require.NoError(t, (&UpgradeOperator{Plan: plan}).Run())
	return plan
}

// useMigrations makes this installer's release know of this many migrations: it is a release with a longer history
// than the one that is in the repository today.
func useMigrations(t *testing.T, n int) {
	t.Helper()
	old := migrations.All
	all := make([]migrations.Migration, 0, n)
	for i := 1; i <= n; i++ {
		all = append(all, migrations.Migration{Version: i, Name: "test", Run: func(context.Context, client.Client) error { return nil }})
	}
	migrations.All = all
	t.Cleanup(func() { migrations.All = old })
}

func TestANewOperatorThatAlreadyMigratedTheDataPastWhatThePreviousOneKnowsIsNotRolledBack(t *testing.T) {
	useMigrations(t, 4) // this release knows up to level 4; the cluster's previous operator, up to 2
	cluster := healthyCluster()
	cluster.schemaVersion = 2
	cluster.crashLoop, cluster.rolloutBroken, cluster.reportsAfterApply = true, true, ""
	cluster.schemaRecord = "3" // the new operator migrated to level 3 and then failed; the previous one knows up to 2
	host := setUpUpgrade(t, cluster)

	err := runSteps(NewUpgradePlan(UpgradeOptions{}))

	require.Error(t, err)
	require.Contains(t, err.Error(), "schema level 3")
	require.Contains(t, err.Error(), "up to level 2")
	require.Contains(t, err.Error(), "was not rolled back")
	require.False(t, host.ran("rollout undo"), "the previous operator would refuse that data, or misread it")
	require.Contains(t, err.Error(), "restore", "and the way out is said")
}

func TestARollbackIsAllowedAtTheLevelThePreviousOperatorReported(t *testing.T) {
	useMigrations(t, 4)
	cluster := healthyCluster()
	cluster.schemaVersion = 2
	cluster.crashLoop, cluster.rolloutBroken, cluster.reportsAfterApply = true, true, ""
	cluster.schemaRecord = "2"
	setUpUpgrade(t, cluster)

	require.ErrorContains(t, runSteps(NewUpgradePlan(UpgradeOptions{})), "rolled the operator back")
	require.True(t, cluster.undone)
}

func TestAClusterFromBeforeTheRecordIsRolledBackOverTheBaselineLevelOnly(t *testing.T) {
	// A cluster that predates the record has no level. The new operator's first migration is the baseline, which stores
	// nothing differently, so level 1 is still readable by an operator from before there were levels.
	for level, rolledBack := range map[string]bool{"": true, "1": true, "2": false} {
		cluster := healthyCluster()
		cluster.release = "" // installed before releases were recorded
		cluster.crashLoop, cluster.rolloutBroken, cluster.reportsAfterApply = true, true, ""
		cluster.schemaRecord = level
		host := setUpUpgrade(t, cluster)

		err := runSteps(NewUpgradePlan(UpgradeOptions{}))

		require.Error(t, err, "level %q", level)
		require.Equal(t, rolledBack, host.ran("rollout undo"), "level %q", level)
	}
}

func TestANewOperatorThatDidTakeOverIsNotRolledBackWhateverHappensAfter(t *testing.T) {
	// It reported its release, so it ran: it may have written fields that the previous operator does not know.
	cluster := healthyCluster()
	cluster.rolloutBroken = true // never ready, but its controllers ran and said which release it is
	cluster.reportsAfterApply = "v0.3.0"
	host := setUpUpgrade(t, cluster)

	err := runSteps(NewUpgradePlan(UpgradeOptions{}))

	require.Error(t, err)
	require.Contains(t, err.Error(), "may already have written fields")
	require.False(t, host.ran("rollout undo"))
}

func TestARunThatDidNotReplaceTheOperatorHasNothingToRollBackTo(t *testing.T) {
	// The same release again, with the operator unhealthy for some other reason. `rollout undo` would go to a revision
	// older than the one that was running, which is a downgrade nobody asked for.
	cluster := healthyCluster()
	host := setUpUpgrade(t, cluster)
	plan := planAfterPreflight(t)
	// What the Deployment was before this run is what it is now: the apply changed nothing.
	plan.State.OperatorImage = "geass-operator:v0.3.0"
	plan.State.OperatorImageDigest = "abc"
	plan.State.OperatorRelease = "v0.3.0"
	cluster.rolloutBroken, cluster.reportsAfterApply = true, ""

	err := (&OperatorTakeover{Plan: plan, RolloutTimeout: 50 * time.Millisecond, ReleaseTimeout: 50 * time.Millisecond}).Run()

	require.ErrorContains(t, err, "did not replace the operator")
	require.False(t, host.ran("rollout undo"))
}

func TestNoRollbackLeavesTheNewOperatorWhereItIs(t *testing.T) {
	cluster := healthyCluster()
	cluster.crashLoop, cluster.rolloutBroken, cluster.reportsAfterApply = true, true, ""
	host := setUpUpgrade(t, cluster)

	err := runSteps(NewUpgradePlan(UpgradeOptions{NoRollback: true}))

	require.ErrorContains(t, err, "--no-rollback")
	require.False(t, host.ran("rollout undo"))
	require.False(t, cluster.undone)
}

func TestARollbackThatCannotBeDoneOrThatTheOldOperatorDoesNotSurviveIsReportedAsThat(t *testing.T) {
	cluster := healthyCluster()
	cluster.crashLoop, cluster.rolloutBroken, cluster.reportsAfterApply = true, true, ""
	cluster.undoErr = errors.New(`error: no rollout history found for deployment "geass-controller-manager"`)
	setUpUpgrade(t, cluster)
	err := runSteps(NewUpgradePlan(UpgradeOptions{}))
	require.ErrorContains(t, err, "tried to roll the operator back and could not")
	require.ErrorContains(t, err, "no rollout history")

	// The previous operator's image was removed from containerd by K3s's cleanup of unused images.
	gone := healthyCluster()
	gone.crashLoop, gone.rolloutBroken, gone.reportsAfterApply = true, true, ""
	gone.previousBroken = true
	setUpUpgrade(t, gone)
	err = runSteps(NewUpgradePlan(UpgradeOptions{}))
	require.ErrorContains(t, err, "previous one did not start either")
	require.ErrorContains(t, err, "ImagePullBackOff")
	require.ErrorContains(t, err, "removed from K3s's containerd")
}

func TestARollbackSaysWhatItLeftChangedAndWhereTheSnapshotIs(t *testing.T) {
	cluster := healthyCluster()
	cluster.crashLoop, cluster.rolloutBroken, cluster.reportsAfterApply = true, true, ""
	setUpUpgrade(t, cluster)

	err := runSteps(NewUpgradePlan(UpgradeOptions{}))
	require.ErrorContains(t, err, "custom resource definitions")
	require.ErrorContains(t, err, "geass-pre-upgrade-v0.2.0-to-v0.3.0")
	require.ErrorContains(t, err, "were not touched")
}

func TestARollbackAfterAK3sUpgradeSaysThatK3sDoesNotGoBack(t *testing.T) {
	cluster := healthyCluster()
	host := setUpUpgrade(t, cluster)
	oldRead := readFile
	readFile = func(string) ([]byte, error) { return []byte(k3sUnit), nil }
	t.Cleanup(func() { readFile = oldRead })
	inner := host.answer
	host.answer = func(c fakeCall) ([]byte, error) {
		if c.Name == "sh" {
			cluster.k3s = "v1.35.1+k3s1"
			return nil, nil
		}
		return inner(c)
	}
	cluster.crashLoop, cluster.rolloutBroken, cluster.reportsAfterApply = true, true, ""

	err := runSteps(NewUpgradePlan(UpgradeOptions{K3sVersion: "v1.35.1+k3s1"}))

	require.ErrorContains(t, err, "K3s on the control plane (v1.35.1+k3s1, which does not go back)")
	require.False(t, host.ran("patch geasscluster"), "the workers are not told to follow a control plane whose operator is not running")
}

// ---- what an upgrade refuses -------------------------------------------------------------------------------------------

// mutating lists the commands that change the cluster or the machine.
func mutating(host *fakeHost) []string {
	var out []string
	for _, c := range host.calls {
		line := c.String()
		if strings.Contains(line, "apply") || strings.Contains(line, "etcd-snapshot") || strings.Contains(line, "ctr image") ||
			strings.Contains(line, "patch") || strings.Contains(line, "get.k3s.io") || strings.Contains(line, "delete") {
			out = append(out, line)
		}
	}
	return out
}

func TestAnUpgradeRefusesADowngradeBeforeChangingAnything(t *testing.T) {
	cluster := healthyCluster()
	cluster.release = "v0.9.0"
	host := setUpUpgrade(t, cluster)

	err := runSteps(NewUpgradePlan(UpgradeOptions{}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not downgrade")
	require.Contains(t, err.Error(), "v0.9.0")
	require.Empty(t, mutating(host), "the refusal comes before every change")
}

func TestAnUpgradeFromAnUnrecordedReleaseIsAllowed(t *testing.T) {
	cluster := healthyCluster()
	cluster.release = "" // installed before releases were recorded: every cluster that exists today
	setUpUpgrade(t, cluster)
	require.NoError(t, runSteps(NewUpgradePlan(UpgradeOptions{})))
}

func TestAnUpgradeRefusesAClusterThatIsNotReadyUnlessForced(t *testing.T) {
	cluster := healthyCluster()
	cluster.phase = ""
	host := setUpUpgrade(t, cluster)

	err := runSteps(NewUpgradePlan(UpgradeOptions{}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "not ready")
	require.Contains(t, err.Error(), "--force")
	require.Empty(t, mutating(host))

	forced := setUpUpgrade(t, cluster)
	require.NoError(t, runSteps(NewUpgradePlan(UpgradeOptions{Force: true})))
	require.True(t, forced.ran("etcd-snapshot save"), "forced, it goes on, and still takes the snapshot")
}

func TestAnUpgradeRefusesWhileALonghornVolumeIsDegraded(t *testing.T) {
	cluster := healthyCluster()
	cluster.volumes = []string{"pvc-a:attached:healthy", "pvc-b:attached:degraded", "pvc-c:detached:unknown"}
	host := setUpUpgrade(t, cluster)

	err := runSteps(NewUpgradePlan(UpgradeOptions{}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "pvc-b (degraded)")
	require.NotContains(t, err.Error(), "pvc-a")
	require.NotContains(t, err.Error(), "pvc-c", "a detached volume has nothing running on it")
	require.Empty(t, mutating(host))
}

func TestAnUpgradeRefusesDefinitionsThatDropAVersionTheClusterStoresObjectsIn(t *testing.T) {
	cluster := healthyCluster()
	cluster.storedVersions = "v1alpha1,v1alpha0" // an old version whose objects were never migrated
	host := setUpUpgrade(t, cluster)

	err := runSteps(NewUpgradePlan(UpgradeOptions{}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "geassclusters.geass.geass.dev has objects stored as v1alpha0")
	require.Empty(t, mutating(host), "found out before the definitions are replaced, when it can still be stopped")
}

func TestAnUpgradeWarnsAboutUnprotectedDatabasesWithoutStoppingForThem(t *testing.T) {
	cluster := healthyCluster()
	cluster.databases = []string{
		"orders:InCluster:Protected", "cache:InCluster:", "mirror:External:", "old:InCluster::deleting",
	}
	setUpUpgrade(t, cluster)
	state, err := InspectCluster("test")
	require.NoError(t, err)
	require.Equal(t, []string{"cache"}, state.UnprotectedDatabases,
		"a protected database, one that lives elsewhere and one that is being deleted are not at risk here")

	blockers, warnings := upgradeFindings(state, UpgradeOptions{})
	require.Empty(t, blockers)
	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], "cache")
}

func TestAnUpgradeCanSkipTheSnapshotOnlyWhenToldTo(t *testing.T) {
	host := setUpUpgrade(t, healthyCluster())
	require.NoError(t, runSteps(NewUpgradePlan(UpgradeOptions{SkipSnapshot: true})))
	require.False(t, host.ran("etcd-snapshot"))
}

func TestAFailedSnapshotStopsTheUpgradeBeforeAnythingIsChanged(t *testing.T) {
	cluster := healthyCluster()
	host := setUpUpgrade(t, cluster)
	inner := host.answer
	host.answer = func(c fakeCall) ([]byte, error) {
		if strings.Contains(c.String(), "etcd-snapshot") {
			return nil, errors.New("etcd datastore disabled")
		}
		return inner(c)
	}

	err := runSteps(NewUpgradePlan(UpgradeOptions{}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "--skip-snapshot", "it says how to go on without one, if that is the intent")
	for _, line := range mutating(host) {
		require.Contains(t, line, "etcd-snapshot", "nothing else ran: %s", line)
	}
}

// ---- K3s -------------------------------------------------------------------------------------------------------------

// The unit that K3s's install script writes for `INSTALL_K3S_EXEC="server --cluster-init"`, as it came out of the script
// (v1.31.5+k3s1): the first argument bare, the others in single quotes, a tab before them.
const k3sUnit = "[Unit]\nDescription=Lightweight Kubernetes\nWants=network-online.target\n\n[Install]\nWantedBy=multi-user.target\n\n" +
	"[Service]\nType=notify\nEnvironmentFile=-/etc/default/%N\nKillMode=process\nExecStartPre=-/sbin/modprobe br_netfilter\n" +
	"ExecStart=/usr/local/bin/k3s \\\n    server \\\n\t'--cluster-init' \\\n\n"

func TestTheServersArgumentsAreReadBackFromItsUnit(t *testing.T) {
	args, err := serverArgsFromUnit(k3sUnit)
	require.NoError(t, err)
	require.Equal(t, []string{"server", "--cluster-init"}, args, "re-running the install script without them would change what the server runs as")

	args, err = serverArgsFromUnit("[Service]\nExecStart=/usr/local/bin/k3s server --cluster-init --tls-san 10.0.0.5\n")
	require.NoError(t, err)
	require.Equal(t, []string{"server", "--cluster-init", "--tls-san", "10.0.0.5"}, args, "on one line, too")
}

func TestTheServersArgumentsKeepTheirQuoting(t *testing.T) {
	// The install script quotes an argument with a quote in it as 'it'\''s', and systemd reads that back as it's.
	unit := "[Service]\nExecStart=/usr/local/bin/k3s \\\n    server \\\n\t'--node-label' \\\n\t'zone=a b' \\\n\t'--node-name=it'\\''s' \\\n\t\"--tls-san=x.example\"\n"
	args, err := serverArgsFromUnit(unit)
	require.NoError(t, err)
	require.Equal(t, []string{"server", "--node-label", "zone=a b", "--node-name=it's", "--tls-san=x.example"}, args,
		"an argument with a space in it is one argument: that is why they go to the script as arguments and not in INSTALL_K3S_EXEC")
}

func TestTheServersArgumentsAreNotGuessedWhenTheyCannotBePassedOnSafely(t *testing.T) {
	_, err := serverArgsFromUnit("[Service]\nExecStart=/usr/local/bin/k3s server --node-name=$HOSTNAME\n")
	require.ErrorContains(t, err, "expands")

	_, err = serverArgsFromUnit("[Service]\nExecStart=/usr/local/bin/k3s server --node-label 'unclosed\n")
	require.ErrorContains(t, err, "unclosed quote")

	_, err = serverArgsFromUnit("[Service]\nExecStart=/usr/local/bin/k3s agent\n")
	require.ErrorContains(t, err, "not a server")

	_, err = serverArgsFromUnit("[Service]\nExecStart=/usr/local/bin/k3s\n")
	require.ErrorContains(t, err, "no arguments")

	_, err = serverArgsFromUnit("[Service]\nType=notify\n")
	require.ErrorContains(t, err, "no ExecStart")
}

func TestK3sIsUpgradedWithTheServersOwnArgumentsAndAfterTheSnapshot(t *testing.T) {
	cluster := healthyCluster()
	host := setUpUpgrade(t, cluster)
	oldRead := readFile
	readFile = func(path string) ([]byte, error) {
		require.Equal(t, k3sServiceUnit, path)
		return []byte(k3sUnit), nil
	}
	t.Cleanup(func() { readFile = oldRead })
	// The install script is "downloaded" and then run; afterwards the control plane reports the new version.
	inner := host.answer
	host.answer = func(c fakeCall) ([]byte, error) {
		if c.Name == "curl" {
			return []byte("#!/bin/sh\n"), nil
		}
		if c.Name == "sh" {
			cluster.k3s = "v1.35.1+k3s1"
			return nil, nil
		}
		return inner(c)
	}

	plan := NewUpgradePlan(UpgradeOptions{K3sVersion: "v1.35.1+k3s1"})
	require.NoError(t, runSteps(plan))

	install := host.index("sh -s - server --cluster-init")
	require.GreaterOrEqual(t, install, 0, "K3s's installer ran, with the server's own arguments: %v", host.commands())
	require.Less(t, host.index("etcd-snapshot save"), install, "never before the snapshot")
	require.Less(t, install, host.index("ctr image import"), "and before the operator is replaced, which must come up on the K3s it was built for")
	require.Equal(t, []string{"INSTALL_K3S_VERSION=v1.35.1+k3s1"}, host.calls[install].Env, "the arguments are not also passed as INSTALL_K3S_EXEC, which the script would split on whitespace")
	require.True(t, host.ran("patch geasscluster default", `"version":"v1.35.1+k3s1"`), "and the workers are told which version to follow")
	require.Less(t, host.index("rollout status"), host.index("patch geasscluster"), "once the new operator is there to act on it")
}

func TestK3sUpgradeIsRefusedForAnythingButTheNextMinor(t *testing.T) {
	for name, target := range map[string]string{
		"two minors":  "v1.36.1+k3s1",
		"a downgrade": "v1.33.9+k3s1",
		"a channel":   "stable",
	} {
		t.Run(name, func(t *testing.T) {
			host := setUpUpgrade(t, healthyCluster())
			err := runSteps(NewUpgradePlan(UpgradeOptions{K3sVersion: target}))
			require.Error(t, err)
			require.Empty(t, mutating(host), "refused before anything is changed")
		})
	}
}

func TestK3sUpgradeIsRefusedWhileANodeIsNotReady(t *testing.T) {
	cluster := healthyCluster()
	cluster.notReady = []string{"w1"}
	host := setUpUpgrade(t, cluster)

	err := runSteps(NewUpgradePlan(UpgradeOptions{K3sVersion: "v1.35.1+k3s1"}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "w1")
	require.Empty(t, mutating(host))

	// Updating Geass alone is not held up by it: it is only a warning there.
	only := setUpUpgrade(t, cluster)
	require.NoError(t, runSteps(NewUpgradePlan(UpgradeOptions{})))
	require.True(t, only.ran("etcd-snapshot save"))
}

func TestAReleaseThatNeedsANewerK3sSaysSoInsteadOfBreakingTheAddOns(t *testing.T) {
	cluster := healthyCluster()
	cluster.k3s = "v1.24.9+k3s1" // below what the add-ons need
	host := setUpUpgrade(t, cluster)

	err := runSteps(NewUpgradePlan(UpgradeOptions{}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "--k3s-version")
	require.Empty(t, mutating(host))
}

// ---- stamping and splitting manifests --------------------------------------------------------------------------------

func TestOperatorManifestsAreSplitIntoDefinitionsAndTheRest(t *testing.T) {
	crds, rest, names, err := splitOperatorManifests([]byte(testManifests))
	require.NoError(t, err)
	require.Equal(t, []string{"geassclusters.geass.geass.dev", "geassapps.geass.geass.dev"}, names)
	require.Contains(t, string(crds), "geassclusters.geass.geass.dev")
	require.NotContains(t, string(crds), "kind: Deployment")
	require.Contains(t, string(rest), "kind: Deployment")
	require.Contains(t, string(rest), "kind: Namespace")
	require.NotContains(t, string(rest), "CustomResourceDefinition")

	_, _, _, err = splitOperatorManifests([]byte("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: x\n"))
	require.Error(t, err, "a bundle with no definitions is not an operator release")
}

func TestStampingNeedsADeploymentToStamp(t *testing.T) {
	_, err := stampOperator([]byte("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: geass-system\n"), "v1", "abc")
	require.ErrorContains(t, err, "no operator Deployment")
}

func TestTheImageDigestChangesWithTheImage(t *testing.T) {
	_, rest, _, err := splitOperatorManifests([]byte(testManifests))
	require.NoError(t, err)
	a, err := stampOperator(rest, "v1", digestOf([]byte("one")))
	require.NoError(t, err)
	b, err := stampOperator(rest, "v1", digestOf([]byte("two")))
	require.NoError(t, err)
	require.NotEqual(t, string(a), string(b), "a new image must change the pod template, or the old operator keeps running")
	again, err := stampOperator(rest, "v1", digestOf([]byte("one")))
	require.NoError(t, err)
	require.Equal(t, string(a), string(again), "and the same image changes nothing, so applying it twice restarts nothing")
}

// ---- inspecting ----------------------------------------------------------------------------------------------------------

func TestInspectingTheClusterReadsWhatTheUpgradeNeeds(t *testing.T) {
	cluster := healthyCluster()
	cluster.k3s = "v1.34.2+k3s1"
	cluster.notReady = []string{"w1"}
	setUpUpgrade(t, cluster)

	state, err := InspectCluster("test")
	require.NoError(t, err)
	require.Equal(t, "default", state.ClusterName)
	require.Equal(t, "v0.2.0", state.Release)
	require.Equal(t, "Ready", state.Phase)
	require.Equal(t, "v1.34.2+k3s1", state.K3sVersion)
	require.Equal(t, 1, state.ControlPlanes)
	require.Equal(t, 1, state.Workers)
	require.Equal(t, []string{"w1"}, state.NodesNotReady)
	require.Equal(t, "geass-operator:v0.0.1", state.OperatorImage)
}

func TestAMachineWithoutGeassIsSaidToHaveNone(t *testing.T) {
	host := &fakeHost{answer: func(c fakeCall) ([]byte, error) {
		return nil, errors.New(`Error from server (NotFound): customresourcedefinitions.apiextensions.k8s.io "geassclusters.geass.geass.dev" not found`)
	}}
	useHost(t, host)
	installed, err := GeassInstalled()
	require.NoError(t, err)
	require.False(t, installed)
}

func TestAK3sThatDoesNotAnswerIsNotTakenForOneWithoutGeass(t *testing.T) {
	host := &fakeHost{answer: func(c fakeCall) ([]byte, error) {
		return nil, errors.New("The connection to the server 127.0.0.1:6443 was refused - did you specify the right host or port?\nconnection refused")
	}}
	useHost(t, host)
	installed, err := GeassInstalled()
	require.Error(t, err, "a cluster that cannot be asked cannot be said to be empty")
	require.False(t, installed)
	require.ErrorIs(t, err, errAPIUnreachable)
}

func TestARunWithoutRootIsNotTakenForAK3sThatDoesNotAnswer(t *testing.T) {
	// What `k3s kubectl` says to a user who cannot read the kubeconfig; without the file it may also fall back to an
	// address nothing listens on, so "connection refused" is in it too.
	host := &fakeHost{answer: func(c fakeCall) ([]byte, error) {
		return nil, errors.New("WARN[0000] Unable to read /etc/rancher/k3s/k3s.yaml, please start server with --write-kubeconfig-mode\n" +
			"The connection to the server localhost:8080 was refused - did you specify the right host or port?\nconnection refused")
	}}
	useHost(t, host)

	installed, err := GeassInstalled()

	require.False(t, installed)
	require.ErrorIs(t, err, errNeedsRoot)
	require.NotErrorIs(t, err, errAPIUnreachable, "K3s may be running well")

	_, err = ChooseMode("", Existing{K3sPresent: true, Unreachable: err})
	require.ErrorContains(t, err, "run as root")
	require.ErrorContains(t, err, "sudo")
	require.NotContains(t, err.Error(), "systemctl status", "it does not send the user to debug a K3s that is not broken")
	require.NotContains(t, err.Error(), "--reinstall", "and never offers to delete the cluster for it")
}

func TestSnapshotNamesAreSafeToPutOnACommandLine(t *testing.T) {
	require.Equal(t, "geass-pre-upgrade-v0.2.0-to-v0.3.0", snapshotName("geass-pre-upgrade", "v0.2.0", "v0.3.0"))
	require.Equal(t, "geass-pre-upgrade-unrecorded-to-dev", snapshotName("geass-pre-upgrade", "", "dev"))
	require.Equal(t, "geass-pre-upgrade-v1.0.0-x-to-v2", snapshotName("geass-pre-upgrade", "v1.0.0;x", "v2"), "a character a shell would read is replaced")
}

func TestTheLatestSnapshotIsTheNewestFileWithTheName(t *testing.T) {
	dir := t.TempDir()
	older, newer := filepath.Join(dir, "geass-pre-upgrade-a-1"), filepath.Join(dir, "geass-pre-upgrade-a-2")
	require.NoError(t, os.WriteFile(older, []byte("1"), 0o600))
	require.NoError(t, os.WriteFile(newer, []byte("2"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "on-demand-1"), []byte("x"), 0o600))
	now := time.Now()
	require.NoError(t, os.Chtimes(older, now.Add(-time.Hour), now.Add(-time.Hour)))
	require.NoError(t, os.Chtimes(newer, now, now))

	require.Equal(t, newer, latestSnapshot(dir, "geass-pre-upgrade-a"))
	require.Empty(t, latestSnapshot(dir, "no-such-name"))
}

// ---- what the review of this code found -----------------------------------------------------------------------------------

func TestForceDoesNotGoPastTheRulesThatProtectTheDatastore(t *testing.T) {
	for name, tc := range map[string]struct {
		k3s, target string
		extra       UpgradeOptions
	}{
		"a downgrade":                 {k3s: "v1.34.2+k3s1", target: "v1.33.9+k3s1"},
		"two minors at once":          {k3s: "v1.34.2+k3s1", target: "v1.36.1+k3s1"},
		"a channel":                   {k3s: "v1.34.2+k3s1", target: "stable"},
		"a running version not known": {k3s: "", target: "v1.35.1+k3s1"},
		"past the tested range":       {k3s: "v1.36.5+k3s1", target: "v1.37.1+k3s1"},
	} {
		t.Run(name, func(t *testing.T) {
			cluster := healthyCluster()
			cluster.k3s = tc.k3s
			host := setUpUpgrade(t, cluster)

			err := runSteps(NewUpgradePlan(UpgradeOptions{K3sVersion: tc.target, Force: true}))

			require.Error(t, err, "--force is for a cluster that is not ready, not for the K3s rules")
			require.Empty(t, mutating(host), "and nothing was changed")
		})
	}
}

func TestForceDoesNotGoPastAK3sTooOldForTheAddOnsEither(t *testing.T) {
	cluster := healthyCluster()
	cluster.k3s = "v1.24.9+k3s1"
	host := setUpUpgrade(t, cluster)

	require.Error(t, runSteps(NewUpgradePlan(UpgradeOptions{Force: true})))
	require.Empty(t, mutating(host))
}

func TestForceStillGoesPastAClusterThatIsNotReady(t *testing.T) {
	cluster := healthyCluster()
	cluster.phase = ""
	setUpUpgrade(t, cluster)
	require.NoError(t, runSteps(NewUpgradePlan(UpgradeOptions{Force: true})))
}

func TestAClusterWhoseDataIsNewerThanThisInstallerIsRefusedEvenWhenTheReleasesCannotBeOrdered(t *testing.T) {
	cluster := healthyCluster()
	cluster.release = "v0.9.0"
	cluster.schemaVersion = 99
	host := setUpUpgrade(t, cluster)
	// An installer built from an untagged checkout is a bare commit, which cannot be ordered against a tag.
	useRelease(t, "110fa20")

	err := runSteps(NewUpgradePlan(UpgradeOptions{Force: true}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "schema level 99")
	require.Empty(t, mutating(host), "the data's level can always be compared, and --force does not get past it")
}

func TestTheNewerOfTheReportedAndStampedReleasesIsWhatTheClusterRuns(t *testing.T) {
	state := ClusterState{Release: "v0.2.0", OperatorRelease: "v0.3.0"}
	require.Equal(t, "v0.3.0", state.RecordedRelease(), "an operator that crash loops never reports, so its report is the old one")
	state = ClusterState{Release: "v0.3.0", OperatorRelease: "v0.2.0"}
	require.Equal(t, "v0.3.0", state.RecordedRelease())
	require.Equal(t, "v0.2.0", ClusterState{OperatorRelease: "v0.2.0"}.RecordedRelease())
	require.Empty(t, ClusterState{}.RecordedRelease())
}

func TestASnapshotThatCannotBeFoundIsNotAWayBackAndStopsTheUpdate(t *testing.T) {
	host := setUpUpgrade(t, healthyCluster())
	// K3s says it worked and writes nothing where it was told to.
	inner := host.answer
	host.answer = func(c fakeCall) ([]byte, error) {
		if len(c.Args) > 1 && c.Args[0] == "etcd-snapshot" {
			return nil, nil
		}
		return inner(c)
	}

	err := runSteps(NewUpgradePlan(UpgradeOptions{}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "no way back")
	require.Contains(t, err.Error(), "--skip-snapshot")
	for _, line := range mutating(host) {
		require.Contains(t, line, "etcd-snapshot", "nothing was changed after it: %s", line)
	}
}

// A server that sends its snapshots to a bucket sends the one before an update too. A bucket that was cleared, deleted or whose key
// was revoked must not stop the update: the file on this machine is the way back.
func TestASnapshotThatCouldNotBeSentToTheBucketButWasWrittenHereDoesNotStopTheUpdate(t *testing.T) {
	host := setUpUpgrade(t, healthyCluster())
	inner := host.answer
	host.answer = func(c fakeCall) ([]byte, error) {
		out, err := inner(c) // writes the file, as K3s does before it sends it
		if len(c.Args) > 1 && c.Args[0] == "etcd-snapshot" && c.Args[1] == "save" {
			return out, errors.New("failed to upload the snapshot to S3: the config secret was not found")
		}
		return out, err
	}

	stderr := captureStderr(t, func() { require.NoError(t, runSteps(NewUpgradePlan(UpgradeOptions{}))) })

	require.Contains(t, stderr, "sending it to its S3 bucket")
}

func TestASnapshotThatFailedAndWroteNothingStillStopsTheUpdate(t *testing.T) {
	host := setUpUpgrade(t, healthyCluster())
	inner := host.answer
	host.answer = func(c fakeCall) ([]byte, error) {
		if len(c.Args) > 1 && c.Args[0] == "etcd-snapshot" && c.Args[1] == "save" {
			return nil, errors.New("etcd is not running")
		}
		return inner(c)
	}

	err := runSteps(NewUpgradePlan(UpgradeOptions{}))

	require.Error(t, err)
	require.Contains(t, err.Error(), "could not take an etcd snapshot")
}

func TestTheSnapshotAlwaysNamesItsDirectorySoAConfiguredOneCannotHideIt(t *testing.T) {
	host := setUpUpgrade(t, healthyCluster())
	require.NoError(t, runSteps(NewUpgradePlan(UpgradeOptions{})))
	require.True(t, host.ran("etcd-snapshot save", "--dir "+k3sSnapshotDir), "%v", host.commands())
}

func TestAnUpgradeLeavesOutThePartsOfTheBundleThatBelongToK3s(t *testing.T) {
	foreign := testManifests + `---
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: helmcharts.helm.cattle.io
spec:
  group: helm.cattle.io
  versions:
  - name: v1
    served: true
    storage: true
`
	host := setUpUpgrade(t, healthyCluster())
	useManifests(t, foreign, "image-bytes")

	crds, rest, names, err := splitOperatorManifests([]byte(foreign))
	require.NoError(t, err)
	require.Equal(t, []string{"geassclusters.geass.geass.dev", "geassapps.geass.geass.dev"}, names)
	require.NotContains(t, string(crds), "helmcharts")
	require.NotContains(t, string(rest), "helmcharts")

	require.NoError(t, runSteps(NewUpgradePlan(UpgradeOptions{})))
	for _, c := range host.calls {
		require.NotContains(t, c.Stdin, "helmcharts.helm.cattle.io", "K3s's own definition is not replaced by a stand-in for it")
	}
}

func TestTheControlPlaneThatWasUpgradedIsWhatTheWaitLooksFor(t *testing.T) {
	// A second control plane node that is still on the old version, and a worker that is not ready: neither is what the
	// upgrade of this machine is waiting for.
	nodes := func(c fakeCall) ([]byte, error) {
		return json.Marshal(map[string]any{"items": []any{
			map[string]any{
				"metadata": map[string]any{"name": "cp1", "labels": map[string]any{"node-role.kubernetes.io/control-plane": "true"}},
				"status": map[string]any{
					"conditions": []any{map[string]any{"type": "Ready", "status": "True"}},
					"nodeInfo":   map[string]any{"kubeletVersion": "v1.35.1+k3s1"},
				},
			},
			map[string]any{
				"metadata": map[string]any{"name": "cp2", "labels": map[string]any{"node-role.kubernetes.io/control-plane": "true"}},
				"status": map[string]any{
					"conditions": []any{map[string]any{"type": "Ready", "status": "True"}},
					"nodeInfo":   map[string]any{"kubeletVersion": "v1.34.2+k3s1"},
				},
			},
			map[string]any{
				"metadata": map[string]any{"name": "w1"},
				"status": map[string]any{
					"conditions": []any{map[string]any{"type": "Ready", "status": "False"}},
					"nodeInfo":   map[string]any{"kubeletVersion": "v1.34.2+k3s1"},
				},
			},
		}})
	}
	useHost(t, &fakeHost{answer: nodes})

	require.NoError(t, waitForControlPlaneVersion("test", "v1.35.1+k3s1", time.Second))
	require.Error(t, waitForControlPlaneVersion("test", "v1.36.1+k3s1", 10*time.Millisecond), "and a version nobody reports is still waited for")
}

func TestAnInheritedInstallExecDoesNotReachK3sInstallScript(t *testing.T) {
	t.Setenv("INSTALL_K3S_EXEC", "agent")
	t.Setenv("INSTALL_K3S_VERSION", "v1.20.0+k3s1")
	cluster := healthyCluster()
	host := setUpUpgrade(t, cluster)
	oldRead := readFile
	readFile = func(string) ([]byte, error) { return []byte(k3sUnit), nil }
	t.Cleanup(func() { readFile = oldRead })
	inner := host.answer
	host.answer = func(c fakeCall) ([]byte, error) {
		if c.Name == "sh" {
			cluster.k3s = "v1.35.1+k3s1"
			return nil, nil
		}
		return inner(c)
	}

	require.NoError(t, runSteps(NewUpgradePlan(UpgradeOptions{K3sVersion: "v1.35.1+k3s1"})))

	for _, c := range host.calls {
		if c.Name != "sh" {
			continue
		}
		var execs, versions []string
		for _, kv := range c.FullEnv {
			if strings.HasPrefix(kv, "INSTALL_K3S_EXEC=") {
				execs = append(execs, kv)
			}
			if strings.HasPrefix(kv, "INSTALL_K3S_VERSION=") {
				versions = append(versions, kv)
			}
		}
		require.Empty(t, execs, "what the person running the installer had set would be added to the server's arguments")
		require.Equal(t, []string{"INSTALL_K3S_VERSION=v1.35.1+k3s1"}, versions, "and the version is the one that was asked for")
	}
}

func TestGeassIsNotThoughtAbsentJustBecauseKubectlCouldNotBeRun(t *testing.T) {
	host := &fakeHost{answer: func(fakeCall) ([]byte, error) {
		return nil, errors.New(`exec: "k3s": executable file not found in $PATH`)
	}}
	useHost(t, host)
	installed, err := GeassInstalled()
	require.Error(t, err, "that is not the API server saying there is no Geass")
	require.False(t, installed)
}

// ---- control plane servers that do not have the new operator image -----------------------------------------------------

func TestTheOperatorImageIsReadFromTheManifestsTheInstallerCarries(t *testing.T) {
	useManifests(t, testManifests, "image")
	require.Equal(t, "geass-operator:v0.3.0", embeddedOperatorImage())

	useManifests(t, "kind: Namespace\n", "image")
	require.Empty(t, embeddedOperatorImage(), "an installer without an operator names no image")
}

func TestAControlPlaneThatLacksTheNewImageIsNamedBeforeAnythingChanges(t *testing.T) {
	const image = "geass-operator:v0.3.0"
	nodes := []nodeInfo{
		{Name: "cp1", ControlPlane: true, Images: []string{"docker.io/library/geass-operator:v0.2.0"}},
		{Name: "cp2", ControlPlane: true, Images: []string{"docker.io/library/geass-operator:v0.2.0"}},
		{Name: "cp3", ControlPlane: true, Images: []string{"docker.io/library/" + image}},
		{Name: "cp4", ControlPlane: true, Images: []string{image}},
		{Name: "w1", Images: nil},
	}
	require.Equal(t, []string{"cp2"}, controlPlanesWithoutImage(nodes, "cp1", image),
		"the machine the installer runs on gets the image as part of the update, workers never run the operator, and both spellings of the name count")
	require.Equal(t, []string{"cp1", "cp2"}, controlPlanesWithoutImage(nodes, "elsewhere", image))
	require.Empty(t, controlPlanesWithoutImage(nodes, "cp1", ""), "no image to look for, nothing to say")
	require.Empty(t, controlPlanesWithoutImage([]nodeInfo{{Name: "cp1", ControlPlane: true}}, "another-name", image),
		"a cluster of one has nobody else to give it to, whatever its node is called")
}

func TestAnUpgradeWarnsWhenAnotherControlPlaneHasNotBeenGivenTheImage(t *testing.T) {
	state := ClusterState{
		Phase: "Ready", ControlPlanes: 3, NewOperatorImage: "geass-operator:v0.3.0",
		ControlPlanesWithoutNewImage: []string{"cp2", "cp3"},
	}
	blockers, warnings := upgradeFindings(state, UpgradeOptions{})
	require.Empty(t, blockers, "a node that does not report an image may still have it, so this only warns")
	require.Len(t, warnings, 2)
	require.Contains(t, warnings[1], "cp2, cp3")
	require.Contains(t, warnings[1], "geass-operator:v0.3.0")
	require.Contains(t, warnings[1], "no options")

	state.ControlPlanesWithoutNewImage = nil
	_, warnings = upgradeFindings(state, UpgradeOptions{})
	require.Len(t, warnings, 1, "and once they all have it there is only the note about K3s")
}

// ---- what the summary says about the other control plane servers ------------------------------------------------------------

// summaryOf runs the summary step and returns what it printed.
func summaryOf(t *testing.T, plan *UpgradePlan) string {
	t.Helper()
	old := os.Stdout
	read, write, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = write
	t.Cleanup(func() { os.Stdout = old })
	runErr := (&UpgradeSummary{Plan: plan}).Run()
	require.NoError(t, write.Close())
	os.Stdout = old
	out, err := io.ReadAll(read)
	require.NoError(t, err)
	require.NoError(t, runErr)
	return string(out)
}

func TestTheSummaryDoesNotPromiseTheWorkersMoveWhileOtherControlPlanesAreBehind(t *testing.T) {
	plan := &UpgradePlan{
		Target: "v0.3.0", K3sTo: "v1.36.5+k3s1",
		State: ClusterState{ClusterName: "default", K3sVersion: "v1.35.9+k3s1", ControlPlanes: 3, Workers: 1,
			ControlPlaneVersions: map[string]string{"this": "v1.35.9+k3s1", "cp2": "v1.35.9+k3s1", "cp3": "v1.35.9+k3s1"}},
	}
	useThisNode(t, "this")

	out := summaryOf(t, plan)

	require.Contains(t, out, "The other 2 control plane server(s) still run an older K3s: cp2 (v1.35.9+k3s1), cp3 (v1.35.9+k3s1)")
	require.Contains(t, out, "INSTALL_K3S_VERSION=v1.36.5+k3s1 INSTALL_K3S_EXEC=server")
	require.Contains(t, out, "once every control plane server runs it")
	require.NotContains(t, out, "The operator now upgrades", "the worker waits for the others")
}

func TestTheSummaryOfASingleControlPlaneIsAsItWas(t *testing.T) {
	plan := &UpgradePlan{
		Target: "v0.3.0", K3sTo: "v1.36.5+k3s1",
		State: ClusterState{ClusterName: "default", K3sVersion: "v1.35.9+k3s1", ControlPlanes: 1, Workers: 2,
			ControlPlaneVersions: map[string]string{"this": "v1.35.9+k3s1"}},
	}
	useThisNode(t, "this")

	out := summaryOf(t, plan)

	require.Contains(t, out, "The operator now upgrades the 2 worker node(s) to K3s v1.36.5+k3s1")
	require.NotContains(t, out, "control plane server(s) still run")
}

// The operator's image is found by reading the manifests as YAML, so the way the renderer lays them out does not matter.
func TestTheOperatorsImageIsReadFromItsDeploymentWhateverTheLayout(t *testing.T) {
	useManifests(t, testManifests, "image")
	require.Equal(t, "geass-operator:v0.3.0", embeddedOperatorImage())

	// A list item that starts with image (the renderer sorts the keys, and another version of it may not put command
	// first), and another container whose image also names the operator.
	useManifests(t, `apiVersion: apps/v1
kind: Deployment
metadata:
  name: geass-controller-manager
spec:
  template:
    spec:
      containers:
      - image: "registry.example.com/geass-operator:v9"
        name: manager
      - image: registry.example.com/geass-operator-sidecar:v9
        name: sidecar
`, "image")
	require.Equal(t, "registry.example.com/geass-operator:v9", embeddedOperatorImage())

	useManifests(t, "kind: Namespace\nmetadata:\n  name: geass-system\n", "image")
	require.Empty(t, embeddedOperatorImage(), "no Deployment, no image")
}

// useThisNode makes this machine's node be called name, for a test.
func useThisNode(t *testing.T, name string) {
	t.Helper()
	old := thisNode
	thisNode = func() string { return name }
	t.Cleanup(func() { thisNode = old })
}

// The other servers may already have been upgraded by hand, or this one may be the one that is already current. The
// summary says what is true of each, and not that the oldest release is what everyone runs.
func TestTheSummaryNamesOnlyTheControlPlanesThatAreStillBehind(t *testing.T) {
	useThisNode(t, "this")
	plan := &UpgradePlan{
		Target: "v0.3.0", K3sTo: "v1.36.5+k3s1",
		State: ClusterState{ClusterName: "default", K3sVersion: "v1.35.9+k3s1", ControlPlanes: 3, Workers: 1,
			ControlPlaneVersions: map[string]string{"this": "v1.35.9+k3s1", "cp2": "v1.36.5+k3s1", "cp3": "v1.35.9+k3s1"}},
	}

	out := summaryOf(t, plan)

	require.Contains(t, out, "K3s (control plane) v1.35.9+k3s1 -> v1.36.5+k3s1")
	require.Contains(t, out, "The other 1 control plane server(s) still run an older K3s: cp3 (v1.35.9+k3s1)")
	require.NotContains(t, out, "cp2", "a server that is already current is not told to upgrade")

	// Every other server is current: this one was the last, and the operator moves the workers now.
	plan.State.ControlPlaneVersions["cp3"] = "v1.36.5+k3s1"
	out = summaryOf(t, plan)
	require.NotContains(t, out, "still run an older K3s")
	require.Contains(t, out, "The operator now upgrades the 1 worker node(s)")
}

func TestTheSummaryDoesNotSayThisRunMovedAServerThatWasAlreadyCurrent(t *testing.T) {
	useThisNode(t, "this")
	plan := &UpgradePlan{
		Target: "v0.3.0", K3sTo: "v1.36.5+k3s1",
		State: ClusterState{ClusterName: "default", K3sVersion: "v1.35.9+k3s1", ControlPlanes: 2, Workers: 1,
			ControlPlaneVersions: map[string]string{"this": "v1.36.5+k3s1", "cp2": "v1.35.9+k3s1"}},
	}

	out := summaryOf(t, plan)

	require.NotContains(t, out, "K3s (control plane)", "this server was already on the release, so this run moved no K3s")
	require.Contains(t, out, "The other 1 control plane server(s) still run an older K3s: cp2 (v1.35.9+k3s1)")
	require.Contains(t, out, "once every control plane server runs it")
}
