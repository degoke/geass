package installer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/degoke/geass/pkg/release"
)

const (
	geassNamespace     = "geass-system"
	operatorDeployment = "geass-controller-manager"
	// releaseAnnotation is put on the operator's pod template by the installer, so that a cluster whose cluster
	// object has not yet reported a release (the operator is paused, or crash looping) still says which release
	// the manifests came from.
	releaseAnnotation = "geass.dev/release"
	// imageDigestAnnotation changes whenever the operator image does, even when its tag does not, which is what
	// makes the Deployment roll out and the new image run.
	imageDigestAnnotation = "geass.dev/operator-image-sha256"
)

// ErrNoGeass says that this machine runs a Kubernetes API server that has no Geass in it.
var ErrNoGeass = errors.New("this cluster has no Geass installed")

// errAPIUnreachable says that the machine's K3s did not answer.
var errAPIUnreachable = errors.New("the K3s API server did not answer")

// errNeedsRoot says that K3s could not be asked because this user may not read its kubeconfig. K3s may be running well:
// the installer has to run as root.
var errNeedsRoot = errors.New("this user cannot read K3s's kubeconfig (/etc/rancher/k3s/k3s.yaml), and the installer has to run as root")

// ClusterState is what the upgrade needs to know about the cluster it is about to change.
type ClusterState struct {
	// ClusterName is the GeassCluster object's name.
	ClusterName string
	// Release is the release the operator reported on the cluster object, and OperatorRelease the one the
	// operator's Deployment was last stamped with. Either may be empty: a cluster installed before releases were
	// recorded has neither.
	Release         string
	OperatorRelease string
	// OperatorImageDigest is the digest the Deployment's pod template was stamped with (imageDigestAnnotation), empty
	// when it was not stamped.
	OperatorImageDigest string
	// Phase is the cluster object's status phase ("Ready" when the cluster is ready).
	Phase string
	// SchemaVersion is the data migration level the cluster object reports.
	SchemaVersion int
	// OperatorImage is the image the operator Deployment runs.
	OperatorImage string
	// K3sVersion is the oldest K3s that a control plane node reports, and ControlPlanes how many there are.
	K3sVersion    string
	ControlPlanes int
	// Workers is how many nodes are not control plane nodes.
	Workers int
	// NodesNotReady names the nodes that are not ready.
	NodesNotReady []string
	// DegradedVolumes names Longhorn volumes that are not healthy: fewer good copies than they should have.
	DegradedVolumes []string
	// UnprotectedDatabases names databases that hold data in this cluster and are not backed up.
	UnprotectedDatabases []string
}

// RecordedRelease is the release this cluster is running as far as it says: what the operator reported, or else what
// the manifests were stamped with.
//
// When both are there the newer is taken. The operator's report can be the older of the two (a new operator that
// crash loops never reports), and the stamp on the Deployment can be the older (applied by hand): the guard against
// going back has to be measured from the newest thing the cluster has seen.
func (s ClusterState) RecordedRelease() string {
	return release.Newer(s.Release, s.OperatorRelease)
}

type kubeList struct {
	Items []json.RawMessage `json:"items"`
}

type kubeMeta struct {
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace"`
	DeletionTimestamp string            `json:"deletionTimestamp"`
	Annotations       map[string]string `json:"annotations"`
	Labels            map[string]string `json:"labels"`
}

// kubeGetList runs `kubectl get <resource> -o json` and returns the items. A resource that the cluster has no type
// for is no items, and ok is false so that a caller can tell it from an empty list.
func kubeGetList(step string, args ...string) (items []json.RawMessage, ok bool, err error) {
	out, err := kubectl(step, nil, append(append([]string{"get"}, args...), "-o", "json")...)
	if err != nil {
		switch {
		case isNoSuchResource(err):
			return nil, false, nil
		case isPermissionDenied(err):
			return nil, false, fmt.Errorf("%w: %v", errNeedsRoot, err)
		case isUnreachable(err):
			return nil, false, fmt.Errorf("%w: %v", errAPIUnreachable, err)
		}
		return nil, false, err
	}
	var list kubeList
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, false, fmt.Errorf("read the answer to `kubectl get %s`: %w", strings.Join(args, " "), err)
	}
	return list.Items, true, nil
}

// isNoSuchResource tells "the cluster has no such kind" from every other failure.
func isNoSuchResource(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "the server doesn't have a resource type") ||
		strings.Contains(msg, "no matches for kind") ||
		strings.Contains(msg, "could not find the requested resource")
}

// isPermissionDenied says that `k3s kubectl` could not read the kubeconfig. It is not that K3s did not answer: without
// the file kubectl may fall back to an address nothing listens on, and the error then also reads "connection refused",
// so this is asked first.
func isPermissionDenied(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "Unable to read /etc/rancher/k3s/k3s.yaml") || strings.Contains(msg, "permission denied")
}

func isUnreachable(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "connection refused") || strings.Contains(msg, "couldn't get current server API group list") ||
		strings.Contains(msg, "Unable to connect to the server") || strings.Contains(msg, "no such file or directory") ||
		strings.Contains(msg, "i/o timeout")
}

// GeassInstalled says whether this machine's K3s has Geass in it. A machine whose K3s does not answer is an error
// and not a no: nothing can be said about what is in a cluster that cannot be asked.
func GeassInstalled() (bool, error) {
	_, err := kubectl("detect geass", nil, "get", "crd", geassClusterCRD, "-o", "name")
	switch {
	case err == nil:
		return true, nil
	case isPermissionDenied(err):
		return false, fmt.Errorf("%w: %v", errNeedsRoot, err)
	case isUnreachable(err):
		return false, fmt.Errorf("%w: %v", errAPIUnreachable, err)
	case strings.Contains(err.Error(), "(NotFound)"):
		// The API server answered, and says that the definition is not there. Anything else (the k3s binary not on
		// the PATH of a sudo that drops /usr/local/bin, a permission error) is not an answer about Geass.
		return false, nil
	}
	return false, err
}

// InspectCluster reads what an upgrade needs to know. It changes nothing.
func InspectCluster(step string) (ClusterState, error) {
	state := ClusterState{}

	clusters, ok, err := kubeGetList(step, "geassclusters.geass.geass.dev", "-n", geassNamespace)
	if err != nil {
		return state, err
	}
	if !ok {
		return state, ErrNoGeass
	}
	if len(clusters) == 0 {
		return state, fmt.Errorf("this machine has Geass installed but no GeassCluster object in %s, so the installer "+
			"cannot tell what it would be upgrading: create the cluster object, or reinstall", geassNamespace)
	}
	var cluster struct {
		Metadata kubeMeta `json:"metadata"`
		Status   struct {
			Phase         string `json:"phase"`
			Release       string `json:"release"`
			SchemaVersion int    `json:"schemaVersion"`
		} `json:"status"`
	}
	// The installer names its cluster "default". When there is another, the first by name is used, and says so.
	picked := pickCluster(clusters)
	if err := json.Unmarshal(picked, &cluster); err != nil {
		return state, fmt.Errorf("read the GeassCluster: %w", err)
	}
	state.ClusterName = cluster.Metadata.Name
	state.Release = cluster.Status.Release
	state.Phase = cluster.Status.Phase
	state.SchemaVersion = cluster.Status.SchemaVersion
	if len(clusters) > 1 {
		Warnf(step, "There are %d GeassCluster objects; using %q", len(clusters), state.ClusterName)
	}

	if err := inspectOperator(step, &state); err != nil {
		return state, err
	}
	if err := inspectNodes(step, &state); err != nil {
		return state, err
	}
	if err := inspectVolumes(step, &state); err != nil {
		return state, err
	}
	if err := inspectDatabases(step, &state); err != nil {
		return state, err
	}
	return state, nil
}

func pickCluster(items []json.RawMessage) json.RawMessage {
	best := items[0]
	bestName := ""
	for _, item := range items {
		var meta struct {
			Metadata kubeMeta `json:"metadata"`
		}
		if json.Unmarshal(item, &meta) != nil {
			continue
		}
		name := meta.Metadata.Name
		if name == "default" {
			return item
		}
		if bestName == "" || name < bestName {
			best, bestName = item, name
		}
	}
	return best
}

func inspectOperator(step string, state *ClusterState) error {
	deployments, _, err := kubeGetList(step, "deployments", "-n", geassNamespace)
	if err != nil {
		return err
	}
	for _, raw := range deployments {
		var d struct {
			Metadata kubeMeta `json:"metadata"`
			Spec     struct {
				Template struct {
					Metadata kubeMeta `json:"metadata"`
					Spec     struct {
						Containers []struct {
							Name  string `json:"name"`
							Image string `json:"image"`
						} `json:"containers"`
					} `json:"spec"`
				} `json:"template"`
			} `json:"spec"`
		}
		if json.Unmarshal(raw, &d) != nil || d.Metadata.Name != operatorDeployment {
			continue
		}
		state.OperatorRelease = d.Spec.Template.Metadata.Annotations[releaseAnnotation]
		state.OperatorImageDigest = d.Spec.Template.Metadata.Annotations[imageDigestAnnotation]
		for _, c := range d.Spec.Template.Spec.Containers {
			if c.Name == "manager" {
				state.OperatorImage = c.Image
			}
		}
	}
	return nil
}

// nodeInfo is what the installer needs to know about one node.
type nodeInfo struct {
	Name         string
	ControlPlane bool
	Ready        bool
	// Version is the kubelet's version, which for K3s is the K3s release.
	Version string
}

func readNodes(step string) ([]nodeInfo, error) {
	nodes, _, err := kubeGetList(step, "nodes")
	if err != nil {
		return nil, err
	}
	var out []nodeInfo
	for _, raw := range nodes {
		var n struct {
			Metadata kubeMeta `json:"metadata"`
			Status   struct {
				Conditions []struct {
					Type   string `json:"type"`
					Status string `json:"status"`
				} `json:"conditions"`
				NodeInfo struct {
					KubeletVersion string `json:"kubeletVersion"`
				} `json:"nodeInfo"`
			} `json:"status"`
		}
		if json.Unmarshal(raw, &n) != nil {
			continue
		}
		info := nodeInfo{Name: n.Metadata.Name, Version: n.Status.NodeInfo.KubeletVersion}
		_, info.ControlPlane = n.Metadata.Labels["node-role.kubernetes.io/control-plane"]
		for _, c := range n.Status.Conditions {
			if c.Type == "Ready" && c.Status == "True" {
				info.Ready = true
			}
		}
		out = append(out, info)
	}
	return out, nil
}

func inspectNodes(step string, state *ClusterState) error {
	nodes, err := readNodes(step)
	if err != nil {
		return err
	}
	for _, n := range nodes {
		if !n.Ready {
			state.NodesNotReady = append(state.NodesNotReady, n.Name)
		}
		if !n.ControlPlane {
			state.Workers++
			continue
		}
		state.ControlPlanes++
		if v := n.Version; release.ValidK3s(v) {
			if state.K3sVersion == "" {
				state.K3sVersion = v
			} else if order, err := release.CompareK3s(v, state.K3sVersion); err == nil && order < 0 {
				state.K3sVersion = v
			}
		}
	}
	slices.Sort(state.NodesNotReady)
	return nil
}

// inspectVolumes finds Longhorn volumes that are not healthy. Longhorn may not be installed, which is no volumes.
func inspectVolumes(step string, state *ClusterState) error {
	volumes, ok, err := kubeGetList(step, "volumes.longhorn.io", "-n", "longhorn-system")
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	for _, raw := range volumes {
		var v struct {
			Metadata kubeMeta `json:"metadata"`
			Status   struct {
				State      string `json:"state"`
				Robustness string `json:"robustness"`
			} `json:"status"`
		}
		if json.Unmarshal(raw, &v) != nil {
			continue
		}
		// An unattached volume has no robustness to speak of ("unknown"): nothing runs on it, so nothing is lost
		// by upgrading around it. A volume that is attached and not healthy has fewer good copies than it should.
		if v.Status.State == "attached" && (v.Status.Robustness == "degraded" || v.Status.Robustness == "faulted") {
			state.DegradedVolumes = append(state.DegradedVolumes, v.Metadata.Name+" ("+v.Status.Robustness+")")
		}
	}
	slices.Sort(state.DegradedVolumes)
	return nil
}

// inspectDatabases finds databases that keep their data in this cluster and are not backed up.
func inspectDatabases(step string, state *ClusterState) error {
	databases, ok, err := kubeGetList(step, "geassdatabases.geass.geass.dev", "-A")
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	for _, raw := range databases {
		var d struct {
			Metadata kubeMeta `json:"metadata"`
			Spec     struct {
				Placement string `json:"placement"`
			} `json:"spec"`
			Status struct {
				Backup *struct {
					State string `json:"state"`
				} `json:"backup"`
			} `json:"status"`
		}
		// A database that is hosted elsewhere has no data here to lose, and one that is being deleted is on its way
		// out. (An external database says so in spec.placement, whatever its provider.)
		if json.Unmarshal(raw, &d) != nil || d.Spec.Placement == "External" || d.Metadata.DeletionTimestamp != "" {
			continue
		}
		if d.Status.Backup == nil || d.Status.Backup.State != "Protected" {
			state.UnprotectedDatabases = append(state.UnprotectedDatabases, d.Metadata.Name)
		}
	}
	slices.Sort(state.UnprotectedDatabases)
	return nil
}

// operatorImageDigest identifies the operator image this installer carries. It is the hash of the image archive, so
// it changes with the image and not with its name.
func operatorImageDigest() string {
	return digestOf(embeddedOperatorImageTar)
}

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
