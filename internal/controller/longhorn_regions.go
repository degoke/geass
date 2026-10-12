package controller

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// Longhorn spreads a replicated volume's copies over every node, which would put a project's data in
// another region. A region therefore gets a replicated class of its own, restricted to its nodes.
// Longhorn restricts by its own node tags, not by Kubernetes labels, so each node's region is mirrored
// onto its Longhorn node as a tag, and the class's nodeSelector names that tag.
//
// This is additive: the global classes and the pass that raises their volumes are untouched, and a
// cluster whose nodes carry no region labels never reaches any of it.

var longhornNodeKind = schema.GroupVersionKind{
	Group:   platform.LonghornAPIGroup,
	Version: platform.LonghornAPIVersion,
	Kind:    "Node",
}

var longhornNodeListKind = longhornNodeKind.GroupVersion().WithKind("NodeList")

// Longhorn's Node objects are read as unstructured, like its volumes, so that needs list and patch.
// +kubebuilder:rbac:groups=longhorn.io,resources=nodes,verbs=get;list;patch

// regionalLonghorn describes one region as far as Longhorn is concerned.
type regionalLonghorn struct {
	// usable is how many of the region's nodes can host replicas.
	usable int
	// hosted is whether every one of them has a ready Longhorn manager, without which a replica cannot
	// be placed there. It is the regional form of what longhornReplicaTarget waits for.
	hosted bool
}

// reconcileLonghornRegions keeps Longhorn's view of the regions in step with the cluster's: node tags,
// one replicated class per region, and the replica count of the volumes in those classes. The result is
// how long to wait before running again, zero meaning nothing is left to do. Like the volume pass it is
// background work and never returns an error.
func (r *GeassClusterReconciler) reconcileLonghornRegions(ctx context.Context, cluster *geassv1alpha1.GeassCluster) time.Duration {
	if !geassv1alpha1.LonghornEnabled(cluster.Spec.Addons) || !cluster.DeletionTimestamp.IsZero() {
		return 0
	}
	log := logf.FromContext(ctx)
	ctx, cancel := context.WithTimeout(ctx, volumeReplicaPassTimeout)
	defer cancel()

	installed, err := r.longhornDriverInstalled(ctx)
	if err != nil {
		log.Error(err, "Failed to check for the Longhorn CSI driver")
		return volumeReplicaRetryAfter
	}
	if !installed {
		return 0
	}
	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes); err != nil {
		log.Error(err, "Failed to list nodes")
		return volumeReplicaRetryAfter
	}
	if err := r.syncLonghornRegionTags(ctx, nodes.Items); err != nil {
		log.Error(err, "Failed to tag Longhorn nodes with their regions")
		return volumeReplicaRetryAfter
	}
	regions, err := r.regionalLonghornState(ctx, nodes.Items)
	if err != nil {
		log.Error(err, "Failed to read Longhorn managers")
		return volumeReplicaRetryAfter
	}

	wait := time.Duration(0)
	for _, region := range sortedKeys(regions) {
		state := regions[region]
		if state.usable == 0 {
			continue
		}
		if !state.hosted {
			// Same wait as for the global classes: a replica can only be placed on a node once its
			// manager is running.
			wait = shortestRequeue(wait, volumeReplicaPace)
			continue
		}
		replicas := platform.LonghornReplicaCount(state.usable)
		class := longhornStorageClass(platform.RegionalReplicatedClass(region), platform.LonghornRegionalStorageClassParameters(replicas, region), storagev1.VolumeBindingImmediate)
		if err := r.ensureStorageClass(ctx, class); err != nil {
			log.Error(err, "Failed to ensure the regional storage class", "region", region)
			wait = shortestRequeue(wait, volumeReplicaRetryAfter)
			continue
		}
		wait = shortestRequeue(wait, r.raiseRegionalVolumes(ctx, region, replicas))
	}
	return wait
}

// syncLonghornRegionTags makes each Longhorn node carry the region tag of its Kubernetes node, and no
// stale one. Tags that are not Geass's are left as they are. A node Longhorn does not know yet is
// skipped: it is tagged on a later pass, and the classes wait for its manager anyway.
func (r *GeassClusterReconciler) syncLonghornRegionTags(ctx context.Context, nodes []corev1.Node) error {
	regionOf := make(map[string]string, len(nodes))
	for i := range nodes {
		regionOf[nodes[i].Name] = nodes[i].Labels[platform.LabelRegion]
	}
	var list unstructured.UnstructuredList
	list.SetGroupVersionKind(longhornNodeListKind)
	if err := r.List(ctx, &list, client.InNamespace(platform.LonghornTargetNS)); err != nil {
		return err
	}
	for i := range list.Items {
		node := &list.Items[i]
		region, known := regionOf[node.GetName()]
		if !known {
			continue
		}
		have, _, err := unstructured.NestedStringSlice(node.Object, "spec", "tags")
		if err != nil {
			return err
		}
		want := withRegionTag(have, region)
		if slices.Equal(have, want) {
			continue
		}
		node.SetGroupVersionKind(longhornNodeKind)
		base := node.DeepCopy()
		if err := unstructured.SetNestedStringSlice(node.Object, want, "spec", "tags"); err != nil {
			return err
		}
		// The tag list is replaced as a whole, so the patch carries the version it was computed from:
		// a tag an admin added meanwhile is kept for the next pass instead of being overwritten.
		if err := r.Patch(ctx, node, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
			return err
		}
	}
	return nil
}

// withRegionTag returns the tags with Geass's region tags replaced by the one for region (none when
// the region is empty). Other tags keep their order.
func withRegionTag(tags []string, region string) []string {
	kept := make([]string, 0, len(tags)+1)
	for _, tag := range tags {
		if !strings.HasPrefix(tag, platform.LonghornRegionTagPrefix) {
			kept = append(kept, tag)
		}
	}
	if region != "" {
		kept = append(kept, platform.LonghornRegionTag(region))
	}
	return kept
}

// regionalLonghornState counts, for every region, the nodes that can host replicas and whether each
// has a ready manager.
func (r *GeassClusterReconciler) regionalLonghornState(ctx context.Context, nodes []corev1.Node) (map[string]regionalLonghorn, error) {
	var pods corev1.PodList
	if err := r.List(ctx, &pods, client.InNamespace(platform.LonghornTargetNS), client.MatchingLabels{"app": "longhorn-manager"}); err != nil {
		return nil, err
	}
	ready := map[string]bool{}
	for i := range pods.Items {
		if pods.Items[i].Spec.NodeName != "" && podConditionsReady(pods.Items[i]) {
			ready[pods.Items[i].Spec.NodeName] = true
		}
	}
	regions := map[string]regionalLonghorn{}
	managed := map[string]int{}
	for i := range nodes {
		region := nodes[i].Labels[platform.LabelRegion]
		if region == "" || !nodeSchedulable(&nodes[i]) {
			continue
		}
		state := regions[region]
		state.usable++
		if ready[nodes[i].Name] {
			managed[region]++
		}
		regions[region] = state
	}
	for region, state := range regions {
		state.hosted = managed[region] >= state.usable
		regions[region] = state
	}
	return regions, nil
}

// raiseRegionalVolumes raises the volumes of a region's class up to the region's replica count, with
// the same pacing and refusal handling as the global pass.
func (r *GeassClusterReconciler) raiseRegionalVolumes(ctx context.Context, region string, replicas int) time.Duration {
	log := logf.FromContext(ctx)
	if replicas <= 1 {
		return 0
	}
	names, err := r.volumeNamesOfClass(ctx, platform.RegionalReplicatedClass(region))
	if err != nil {
		log.Error(err, "Failed to list PersistentVolumes")
		return volumeReplicaRetryAfter
	}
	if len(names) == 0 {
		return 0
	}
	volumes, err := r.longhornVolumes(ctx)
	if err != nil {
		log.Error(err, "Failed to list Longhorn volumes")
		return volumeReplicaRetryAfter
	}
	failed, more := r.raiseVolumesTo(ctx, names, volumes, int64(replicas))
	switch {
	case more:
		return volumeReplicaPace
	case failed:
		return volumeReplicaRetryAfter
	default:
		return 0
	}
}

// volumeNamesOfClass are the Longhorn volumes behind bound claims of one class, in name order.
func (r *GeassClusterReconciler) volumeNamesOfClass(ctx context.Context, class string) ([]string, error) {
	var pvs corev1.PersistentVolumeList
	if err := r.List(ctx, &pvs); err != nil {
		return nil, err
	}
	var names []string
	for i := range pvs.Items {
		pv := &pvs.Items[i]
		if pv.Spec.StorageClassName != class {
			continue
		}
		csi := pv.Spec.CSI
		if csi == nil || csi.Driver != platform.LonghornProvisioner || csi.VolumeHandle == "" ||
			pv.Status.Phase != corev1.VolumeBound || !pv.DeletionTimestamp.IsZero() {
			continue
		}
		names = append(names, csi.VolumeHandle)
	}
	slices.Sort(names)
	return names, nil
}

func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}
