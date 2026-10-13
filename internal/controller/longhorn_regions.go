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
// Projects that require node tags (a compliance tag such as hipaa) get the same treatment for those
// tags: each node's Geass tags are mirrored as Longhorn tags too, and a project's volumes use a class
// whose nodeSelector names the tags it requires, so no copy of its data lands on another node.
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
	tagged, err := r.syncLonghornRegionTags(ctx, nodes.Items)
	if err != nil {
		log.Error(err, "Failed to tag Longhorn nodes with their regions")
		return volumeReplicaRetryAfter
	}
	general, err := r.generalOnly(ctx)
	if err != nil {
		log.Error(err, "Failed to check which Longhorn nodes are open to every project")
		return volumeReplicaRetryAfter
	}
	wait := time.Duration(0)
	if tagged && complianceMode(nodes.Items) {
		// The global class is made before the nodes are tagged, so it only switches to the general tag
		// on the next pass.
		wait = volumeReplicaPace
	}
	regions, err := r.regionalLonghornState(ctx, nodes.Items, general)
	if err != nil {
		log.Error(err, "Failed to read Longhorn managers")
		return volumeReplicaRetryAfter
	}

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
		params := withGeneralSelector(platform.LonghornRegionalStorageClassParameters(replicas, region), general)
		class := longhornStorageClass(platform.RegionalReplicatedClass(region), params, storagev1.VolumeBindingImmediate)
		if err := r.ensureStorageClass(ctx, class); err != nil {
			log.Error(err, "Failed to ensure the regional storage class", "region", region)
			wait = shortestRequeue(wait, volumeReplicaRetryAfter)
			continue
		}
		wait = shortestRequeue(wait, r.raiseClassVolumes(ctx, platform.RegionalReplicatedClass(region), replicas, volumeSelector(params["nodeSelector"], general)))
	}
	return shortestRequeue(wait, r.reconcileTaggedClasses(ctx, nodes.Items))
}

// syncLonghornRegionTags makes each Longhorn node carry the tags Geass owns, from its Kubernetes node: its
// region and each of its node tags, and no stale ones. Tags that are not Geass's are left as they are.
// A node Longhorn does not know yet is skipped: it is tagged on a later pass, and the classes wait for
// its manager anyway.
func (r *GeassClusterReconciler) syncLonghornRegionTags(ctx context.Context, nodes []corev1.Node) (changed bool, err error) {
	type scope struct {
		region  string
		tags    []string
		general bool
	}
	compliance := complianceMode(nodes)
	scopeOf := make(map[string]scope, len(nodes))
	for i := range nodes {
		var tags []string
		for key := range nodes[i].Labels {
			if tag, ok := strings.CutPrefix(key, platform.TagKeyPrefix); ok {
				tags = append(tags, tag)
			}
		}
		slices.Sort(tags)
		scopeOf[nodes[i].Name] = scope{
			region: nodes[i].Labels[platform.LabelRegion], tags: tags,
			general: compliance && !platform.IsComplianceNode(&nodes[i]),
		}
	}
	var list unstructured.UnstructuredList
	list.SetGroupVersionKind(longhornNodeListKind)
	if err := r.List(ctx, &list, client.InNamespace(platform.LonghornTargetNS)); err != nil {
		return false, err
	}
	for i := range list.Items {
		node := &list.Items[i]
		want, known := scopeOf[node.GetName()]
		if !known {
			continue
		}
		have, _, err := unstructured.NestedStringSlice(node.Object, "spec", "tags")
		if err != nil {
			return false, err
		}
		desired := withGeassTags(have, want.region, want.tags, want.general)
		if slices.Equal(have, desired) {
			continue
		}
		node.SetGroupVersionKind(longhornNodeKind)
		base := node.DeepCopy()
		if err := unstructured.SetNestedStringSlice(node.Object, desired, "spec", "tags"); err != nil {
			return false, err
		}
		// The tag list is replaced as a whole, so the patch carries the version it was computed from:
		// a tag an admin added meanwhile is kept for the next pass instead of being overwritten.
		if err := r.Patch(ctx, node, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{})); err != nil {
			return false, err
		}
		changed = true
	}
	return changed, nil
}

// withGeassTags returns the tags with every tag Geass owns replaced by the ones for the region and the
// node's tags (none for an empty region or no tags), and the general tag when the node is open to every
// project's volumes. Other tags keep their order.
func withGeassTags(tags []string, region string, nodeTags []string, general bool) []string {
	kept := make([]string, 0, len(tags)+2+len(nodeTags))
	for _, tag := range tags {
		if !strings.HasPrefix(tag, platform.LonghornRegionTagPrefix) && !strings.HasPrefix(tag, platform.LonghornTagPrefix) && tag != platform.LonghornGeneralTag {
			kept = append(kept, tag)
		}
	}
	if general {
		kept = append(kept, platform.LonghornGeneralTag)
	}
	if region != "" {
		kept = append(kept, platform.LonghornRegionTag(region))
	}
	for _, tag := range nodeTags {
		kept = append(kept, platform.LonghornTag(tag))
	}
	return kept
}

// readyLonghornManagerNodes is the set of nodes whose Longhorn manager is running and ready. A replica can
// only be placed on a node once its manager is.
func (r *GeassClusterReconciler) readyLonghornManagerNodes(ctx context.Context) (map[string]bool, error) {
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
	return ready, nil
}

// regionalLonghornState counts, for every region, the nodes that can host replicas and whether each
// has a ready manager. With general set, compliance nodes are left out: the region's class no longer
// places replicas there.
func (r *GeassClusterReconciler) regionalLonghornState(ctx context.Context, nodes []corev1.Node, general bool) (map[string]regionalLonghorn, error) {
	ready, err := r.readyLonghornManagerNodes(ctx)
	if err != nil {
		return nil, err
	}
	regions := map[string]regionalLonghorn{}
	managed := map[string]int{}
	for i := range nodes {
		region := nodes[i].Labels[platform.LabelRegion]
		if region == "" || !nodeSchedulable(&nodes[i]) || (general && platform.IsComplianceNode(&nodes[i])) {
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

// taggedScope is a region and the node tags a project requires. Volumes of projects with the same
// scope share a class.
type taggedScope struct {
	region string
	tags   []string
}

// requiredTagScopes are the scopes volumes need a class for: those of the projects that require node
// tags, those databases were pinned to, which stay served after a project drops its tags, and the one the
// cluster's object storage server was pinned to.
func (r *GeassClusterReconciler) requiredTagScopes(ctx context.Context) ([]taggedScope, error) {
	scopes := map[string]taggedScope{}
	add := func(region string, tags []string) {
		if len(tags) > 0 {
			scopes[platform.TaggedReplicatedClass(region, tags)] = taggedScope{region: region, tags: tags}
		}
	}
	var projects geassv1alpha1.GeassProjectList
	if err := r.List(ctx, &projects, client.InNamespace(platform.SystemNamespace)); err != nil {
		return nil, err
	}
	for i := range projects.Items {
		tags := platform.NewPlacement("", projects.Items[i].Spec.RequiredTags).Tags
		if len(tags) == 0 {
			continue
		}
		region, err := platform.ProjectRegion(ctx, r.Client, projects.Items[i].Name)
		if err != nil {
			return nil, err
		}
		add(region, tags)
	}
	var databases geassv1alpha1.GeassDatabaseList
	if err := r.List(ctx, &databases, client.InNamespace(platform.SystemNamespace)); err != nil {
		return nil, err
	}
	for i := range databases.Items {
		if pinned, ok := databases.Items[i].Annotations[platform.AnnotationNodePlacement]; ok {
			placement := platform.ParsePlacement(pinned)
			add(placement.Region, placement.Required)
		}
	}
	// The cluster's object storage server is pinned to nodes like a database, and its volume stays on them too.
	var stores geassv1alpha1.GeassObjectStoreList
	if err := r.List(ctx, &stores, client.InNamespace(platform.SystemNamespace)); err != nil {
		return nil, err
	}
	for i := range stores.Items {
		if platform.IsClusterObjectStoreServer(&stores.Items[i]) {
			if pinned, ok := stores.Items[i].Annotations[platform.AnnotationNodePlacement]; ok {
				placement := platform.ParsePlacement(pinned)
				add(placement.Region, platform.ObjectStoreServerStorageTags(placement))
			}
		}
	}
	return slices.SortedFunc(maps.Values(scopes), func(a, b taggedScope) int {
		return strings.Compare(platform.TaggedReplicatedClass(a.region, a.tags), platform.TaggedReplicatedClass(b.region, b.tags))
	}), nil
}

// reconcileTaggedClasses makes the replicated class of every scope that needs one: it keeps a volume's
// copies on the nodes a project requires, so a compliance project's data is never replicated onto a
// node outside it. It waits, like the regional pass, until every node in the scope has a manager.
func (r *GeassClusterReconciler) reconcileTaggedClasses(ctx context.Context, nodes []corev1.Node) time.Duration {
	log := logf.FromContext(ctx)
	scopes, err := r.requiredTagScopes(ctx)
	if err != nil {
		log.Error(err, "Failed to read the node tags projects require")
		return volumeReplicaRetryAfter
	}
	if len(scopes) == 0 {
		return 0
	}
	ready, err := r.readyLonghornManagerNodes(ctx)
	if err != nil {
		log.Error(err, "Failed to read Longhorn managers")
		return volumeReplicaRetryAfter
	}
	wait := time.Duration(0)
	for _, scope := range scopes {
		usable, managed := 0, 0
		for i := range nodes {
			if !nodeSchedulable(&nodes[i]) || !platform.NewPlacement(scope.region, scope.tags).Matches(&nodes[i]) {
				continue
			}
			usable++
			if ready[nodes[i].Name] {
				managed++
			}
		}
		if usable == 0 {
			continue
		}
		if managed < usable {
			wait = shortestRequeue(wait, volumeReplicaPace)
			continue
		}
		replicas := platform.LonghornReplicaCount(usable)
		name := platform.TaggedReplicatedClass(scope.region, scope.tags)
		class := longhornStorageClass(name, platform.LonghornTaggedStorageClassParameters(replicas, scope.region, scope.tags), storagev1.VolumeBindingImmediate)
		if err := r.ensureStorageClass(ctx, class); err != nil {
			log.Error(err, "Failed to ensure the storage class for required node tags", "class", name)
			wait = shortestRequeue(wait, volumeReplicaRetryAfter)
			continue
		}
		wait = shortestRequeue(wait, r.raiseClassVolumes(ctx, name, replicas, nil))
	}
	return wait
}

// raiseClassVolumes raises the volumes of one class up to its replica count, with the same pacing and
// refusal handling as the global pass. A volume that is raised also takes the class's node selector when one
// is given, so the copies it gains are not placed on compliance nodes.
func (r *GeassClusterReconciler) raiseClassVolumes(ctx context.Context, class string, replicas int, selector []string) time.Duration {
	log := logf.FromContext(ctx)
	if replicas <= 1 {
		return 0
	}
	names, err := r.volumeNamesOfClass(ctx, class)
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
	failed, more := r.raiseVolumesTo(ctx, names, volumes, int64(replicas), selector)
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
