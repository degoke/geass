/*
Copyright 2026 Adegoke Adewoye.

Licensed under the Elastic License 2.0 (the "License"); you may not use this
file except in compliance with the License. You may obtain a copy of the
License at LICENSE or https://www.elastic.co/licensing/elastic-license.
*/

package controller

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/compute"
	"github.com/degoke/geass/pkg/platform"
)

// autoscaling is the pool's autoscaling settings with the defaults filled in.
type autoscaling struct {
	scaleUpDelay   time.Duration
	maxScaleUp     int
	utilization    float64
	scaleDownAfter time.Duration
	delayAfterAdd  time.Duration
	minLifetime    time.Duration
	spareNodes     int
}

func autoscalingOf(pool *geassv1alpha1.GeassNodePool) autoscaling {
	a := autoscaling{
		scaleUpDelay: platform.DefaultScaleUpDelay, maxScaleUp: platform.DefaultMaxScaleUp,
		utilization:    float64(platform.DefaultScaleDownUtilization) / 100,
		scaleDownAfter: platform.DefaultScaleDownAfter, delayAfterAdd: platform.DefaultScaleDownDelayAfterAdd,
	}
	s := pool.Spec.Autoscaling
	if s == nil {
		return a
	}
	if s.ScaleUpDelaySeconds > 0 {
		a.scaleUpDelay = time.Duration(s.ScaleUpDelaySeconds) * time.Second
	}
	if s.MaxScaleUp > 0 {
		a.maxScaleUp = int(s.MaxScaleUp)
	}
	if s.ScaleDownUtilizationPercent > 0 {
		a.utilization = float64(s.ScaleDownUtilizationPercent) / 100
	}
	if s.ScaleDownAfterMinutes > 0 {
		a.scaleDownAfter = time.Duration(s.ScaleDownAfterMinutes) * time.Minute
	}
	if s.ScaleDownDelayAfterAddMinutes > 0 {
		a.delayAfterAdd = time.Duration(s.ScaleDownDelayAfterAddMinutes) * time.Minute
	}
	a.minLifetime = time.Duration(s.MinNodeLifetimeMinutes) * time.Minute
	a.spareNodes = int(s.SpareNodes)
	return a
}

// manage brings the pool's nodes to where they should be. mayScale is false while the pool is suspended, paused or
// without a working connection: it then only keeps what it has.
func (r *GeassNodePoolReconciler) manage(ctx context.Context, pool *geassv1alpha1.GeassNodePool, cluster *geassv1alpha1.GeassCluster, nodes *poolNodes, provider compute.Provider, template platform.NodeTemplate, report *poolReport, mayScale bool) error {
	liveCount := len(nodes.live) + nodes.justCreated
	report.desired = int32(liveCount)
	if err := r.clearFailedNodes(ctx, nodes); err != nil {
		return err
	}
	if !pool.IsInventory() && provider != nil {
		if err := r.advanceFallback(ctx, pool, nodes); err != nil {
			return err
		}
	}
	if !mayScale {
		return nil
	}
	min, max := int(pool.Spec.MinNodes), int(pool.Spec.MaxNodes)

	if !pool.AutoscalingEnabled() {
		report.desired = int32(min)
		if err := r.releaseDrains(ctx, pool, nodes); err != nil {
			return err
		}
		switch {
		case liveCount < min:
			made, limit, err := r.createNodes(ctx, pool, nodes, min-liveCount)
			if err != nil {
				return err
			}
			r.noteScaleUp(pool, made, limit, report)
		case liveCount > min:
			removed, err := r.removeNodes(ctx, nodes, liveCount-min)
			if err != nil {
				return err
			}
			r.noteScaleDown(pool, removed)
		}
		return nil
	}

	as := autoscalingOf(pool)
	usage, err := r.usage(ctx)
	if err != nil {
		return err
	}
	demand := platform.DemandOn(usage.Unplaced, template, r.now(), as.scaleUpDelay)
	report.pending = int32(len(demand.Fit) + len(demand.TooBig) + demand.Waiting)
	if note := platform.DescribeDemand(demand, template, pool.Name); note != "" {
		report.message = note
	}
	report.requeue = autoscaleInterval

	// Demand takes back the nodes that were being emptied: their room is the quickest to get.
	if len(demand.Fit)+demand.Waiting > 0 {
		if err := r.releaseDrains(ctx, pool, nodes); err != nil {
			return err
		}
	}

	comingUp := len(nodes.comingUp) + nodes.justCreated
	needed := 0
	if len(demand.Fit) > 0 {
		if template.CPUMillis == 0 && template.MemoryBytes == 0 {
			// Nothing is known about what a node of this pool offers, so one is added and the next look
			// will know.
			if comingUp == 0 {
				needed = 1
			}
		} else {
			needed = platform.NodesNeeded(demand.Fit, template, comingUp, r.recentRooms(ctx, nodes, usage))
		}
	}
	want := needed + r.spareDeficit(ctx, nodes, as, needed)
	if liveCount+want < min {
		want = min - liveCount
	}
	want = minInt(want, max-nodes.machines, as.maxScaleUp)
	if want < 0 {
		want = 0
	}
	report.desired = int32(minInt(max, liveCount+want))

	switch {
	case want > 0:
		made, limit, err := r.createNodes(ctx, pool, nodes, want)
		if err != nil {
			return err
		}
		logf.FromContext(ctx).Info("Adding nodes to a pool", "pool", pool.Name, "added", made, "wanted", want, "pods waiting", len(demand.Fit),
			"nodes needed", needed, "nodes coming up", comingUp, "live", liveCount, "limit", limit)
		r.noteScaleUp(pool, made, limit, report)
		return nil
	case needed > 0 && nodes.machines >= max:
		report.message = fmt.Sprintf("%d pods cannot start and the pool is at its maximum of %d nodes", len(demand.Fit), max)
		return nil
	}

	// Nothing to add. Look at taking something away, but not while pods are waiting for room or the pool has just grown.
	if len(demand.Fit)+demand.Waiting > 0 {
		pool.Status.UnneededSince = nil
		return nil
	}
	return r.scaleDown(ctx, pool, cluster, nodes, template, as, report)
}

func minInt(first int, rest ...int) int {
	for _, v := range rest {
		if v < first {
			first = v
		}
	}
	return first
}

func (r *GeassNodePoolReconciler) noteScaleUp(pool *geassv1alpha1.GeassNodePool, made int, limit string, report *poolReport) {
	if made > 0 {
		now := metav1.NewTime(r.now())
		pool.Status.LastScaleUp = &now
		report.phase = geassv1alpha1.NodePoolScaling
		report.message = fmt.Sprintf("adding %d nodes", made)
	}
	if limit != "" && made == 0 {
		report.message = limit
	}
}

func (r *GeassNodePoolReconciler) noteScaleDown(pool *geassv1alpha1.GeassNodePool, removed int) {
	if removed > 0 {
		now := metav1.NewTime(r.now())
		pool.Status.LastScaleDown = &now
	}
}

// spareDeficit is how many more nodes the pool needs so that SpareNodes of them stand empty: room for a burst that
// does not wait for a machine. Nodes that are coming up, and the ones about to be added for pods that wait, count as
// spare once those pods are placed, so only the rest is asked for.
func (r *GeassNodePoolReconciler) spareDeficit(ctx context.Context, nodes *poolNodes, as autoscaling, needed int) int {
	if as.spareNodes == 0 {
		return 0
	}
	empty := len(nodes.comingUp) + nodes.justCreated
	// One list for every node: asking per node would read all the cluster's pods once for each.
	var pods corev1.PodList
	if err := r.List(ctx, &pods, client.UnsafeDisableDeepCopy); err != nil {
		return 0 // not knowing which nodes are empty is no reason to buy machines
	}
	movable := map[string]int{}
	for i := range pods.Items {
		if platform.Movable(&pods.Items[i]) {
			movable[pods.Items[i].Spec.NodeName]++
		}
	}
	for _, node := range nodes.ready {
		if movable[nodeNameOf(node)] == 0 {
			empty++
		}
	}
	// Nodes added for waiting pods fill up, so they are not spare.
	empty -= needed
	return max(as.spareNodes-empty, 0)
}

func nodeNameOf(node *geassv1alpha1.GeassNode) string {
	if node.Status.NodeName != "" {
		return node.Status.NodeName
	}
	return node.Name
}

// usage is what the cluster's pods ask of its nodes.
func (r *GeassNodePoolReconciler) usage(ctx context.Context) (platform.PodUsage, error) {
	if r.PodUsage.Ready() {
		return r.PodUsage.Usage(), nil
	}
	var pods corev1.PodList
	if err := r.List(ctx, &pods, client.UnsafeDisableDeepCopy); err != nil {
		return platform.PodUsage{}, err
	}
	return platform.UsageOfPods(pods.Items), nil
}

// recentNodeWindow is how long after a node became ready its free room still counts as room for pods that cannot
// start. The scheduler takes a few seconds to notice a new node, and in that time the pods that waited for it are
// still marked unable to start; without this a pool would buy a second machine for pods the first has room for.
const recentNodeWindow = 3 * time.Minute

// recentRooms is the free room of the pool's nodes that became ready within the last few minutes.
func (r *GeassNodePoolReconciler) recentRooms(ctx context.Context, nodes *poolNodes, usage platform.PodUsage) []platform.Room {
	var rooms []platform.Room
	for _, geassNode := range nodes.ready {
		var node corev1.Node
		if err := r.Get(ctx, client.ObjectKey{Name: nodeNameOf(geassNode)}, &node); err != nil {
			continue
		}
		recent := false
		for _, cond := range node.Status.Conditions {
			if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
				recent = r.now().Sub(cond.LastTransitionTime.Time) < recentNodeWindow
			}
		}
		if !recent {
			continue
		}
		used := usage.PerNode[node.Name]
		cpu := platform.QuantityMillis(node.Status.Allocatable[corev1.ResourceCPU]) - used.CPUMillis
		memory := platform.QuantityBytes(node.Status.Allocatable[corev1.ResourceMemory]) - used.MemoryBytes
		if cpu > 0 && memory > 0 {
			rooms = append(rooms, platform.Room{CPUMillis: cpu, MemoryBytes: memory})
		}
	}
	return rooms
}

// releaseDrains puts back into service the nodes of the pool that the autoscaler was emptying. A node is released
// when load came back, or when the pool stopped autoscaling; only a node that Geass itself cordoned is touched.
func (r *GeassNodePoolReconciler) releaseDrains(ctx context.Context, pool *geassv1alpha1.GeassNodePool, nodes *poolNodes) error {
	for _, node := range nodes.live {
		name := nodeNameOf(node)
		var k8sNode corev1.Node
		if err := r.Get(ctx, client.ObjectKey{Name: name}, &k8sNode); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return err
		}
		if k8sNode.Annotations[platform.AnnotationPoolCordon] != valueTrue {
			continue
		}
		if err := uncordonNode(ctx, r.Client, name); err != nil {
			return err
		}
	}
	pool.Status.UnneededSince = nil
	return nil
}

// nodeView is what the scale-down check knows about one node of the pool.
type nodeView struct {
	geass       *geassv1alpha1.GeassNode
	k8s         *corev1.Node
	movable     []*corev1.Pod
	utilization float64
}

// scaleDown removes a node of an autoscaling pool when it is not needed: it is under-used, everything on it fits
// elsewhere, it has been so for long enough, and nothing about it says to leave it. Empty nodes go together; a
// node with pods goes alone, after its pods have been moved.
func (r *GeassNodePoolReconciler) scaleDown(ctx context.Context, pool *geassv1alpha1.GeassNodePool, cluster *geassv1alpha1.GeassCluster, nodes *poolNodes, template platform.NodeTemplate, as autoscaling, report *poolReport) error {
	log := logf.FromContext(ctx)
	now := r.now()

	// First finish what is already being emptied.
	draining, err := r.continueDrains(ctx, pool, nodes)
	if err != nil {
		return err
	}
	if draining > 0 {
		report.message = fmt.Sprintf("removing %d nodes", draining)
		return nil
	}

	if pool.Status.ScaleDownBackoffUntil != nil && pool.Status.ScaleDownBackoffUntil.After(now) {
		return nil
	}
	if pool.Status.LastScaleUp != nil && now.Sub(pool.Status.LastScaleUp.Time) < as.delayAfterAdd {
		return nil
	}
	if u := cluster.Status.K3s.Upgrade; u != nil && (u.Phase == geassv1alpha1.K3sUpgradeUpgrading || u.Phase == geassv1alpha1.K3sUpgradeWaiting) {
		report.message = "not removing nodes while K3s is being upgraded"
		return nil
	}
	if len(nodes.comingUp) > 0 || nodes.justCreated > 0 {
		return nil
	}
	removable := len(nodes.ready) - int(pool.Spec.MinNodes)
	if removable <= 0 {
		pool.Status.UnneededSince = nil
		return nil
	}

	usage, err := r.usage(ctx)
	if err != nil {
		return err
	}
	var all corev1.NodeList
	if err := r.List(ctx, &all, client.UnsafeDisableDeepCopy); err != nil {
		return err
	}
	byName := map[string]*corev1.Node{}
	for i := range all.Items {
		byName[all.Items[i].Name] = &all.Items[i]
	}
	podList := corev1.PodList{}
	if err := r.List(ctx, &podList, client.UnsafeDisableDeepCopy); err != nil {
		return err
	}
	// Grouped once, so that looking at each node does not walk every pod in the cluster.
	podsByNode := map[string][]*corev1.Pod{}
	for i := range podList.Items {
		pod := &podList.Items[i]
		podsByNode[pod.Spec.NodeName] = append(podsByNode[pod.Spec.NodeName], pod)
	}

	unneeded := map[string]metav1.Time{}
	var eligible []nodeView
	blockedReasons := []string{}
	for _, geassNode := range nodes.ready {
		name := nodeNameOf(geassNode)
		k8sNode := byName[name]
		if k8sNode == nil {
			continue
		}
		view := nodeView{geass: geassNode, k8s: k8sNode}
		reason := r.removalBlocker(ctx, pool, as, now, &view, podsByNode[k8sNode.Name])
		if reason == "" {
			reason = r.notNeeded(&view, as, usage, all.Items)
		}
		if reason != "" {
			blockedReasons = append(blockedReasons, name+": "+reason)
			continue
		}
		since := now
		if prior, ok := pool.Status.UnneededSince[name]; ok {
			since = prior.Time
		}
		unneeded[name] = metav1.NewTime(since)
		if now.Sub(since) >= as.scaleDownAfter {
			eligible = append(eligible, view)
		}
	}
	pool.Status.UnneededSince = unneeded
	if len(eligible) == 0 {
		if len(blockedReasons) > 0 {
			log.V(1).Info("Nodes stay", "pool", pool.Name, "reasons", strings.Join(blockedReasons, "; "))
		}
		return nil
	}

	// Empty nodes go together, keeping the spare nodes and the minimum; otherwise the least used node alone.
	slices.SortStableFunc(eligible, func(a, b nodeView) int {
		if c := cmp.Compare(a.utilization, b.utilization); c != 0 {
			return c
		}
		// Among equals the newest goes first.
		return b.geass.CreationTimestamp.Compare(a.geass.CreationTimestamp.Time)
	})
	var chosen []nodeView
	empties := 0
	for _, v := range eligible {
		if len(v.movable) == 0 {
			empties++
		}
	}
	keepEmpty := as.spareNodes
	for _, v := range eligible {
		if len(chosen) >= removable {
			break
		}
		if len(v.movable) == 0 {
			if len(chosen) < platform.MaxEmptyNodesRemoved && empties-len(chosen) > keepEmpty {
				chosen = append(chosen, v)
			}
			continue
		}
		if len(chosen) == 0 {
			chosen = append(chosen, v)
		}
		break
	}
	for _, v := range chosen {
		if err := cordonNode(ctx, r.Client, v.k8s.Name, now); err != nil {
			return err
		}
		log.Info("Removing a node that is not needed", "pool", pool.Name, "node", v.k8s.Name, "utilization", fmt.Sprintf("%.0f%%", v.utilization*100), "pods", len(v.movable))
	}
	if len(chosen) > 0 {
		report.message = fmt.Sprintf("removing %d nodes", len(chosen))
	}
	return nil
}

// removalBlocker says why a node must stay, or returns "". It looks at the node, then at what runs on it.
func (r *GeassNodePoolReconciler) removalBlocker(ctx context.Context, pool *geassv1alpha1.GeassNodePool, as autoscaling, now time.Time, view *nodeView, pods []*corev1.Pod) string {
	node := view.k8s
	if node.Annotations[platform.AnnotationScaleDownDisabled] == valueTrue {
		return platform.AnnotationScaleDownDisabled + " is set on the node"
	}
	if node.Spec.Unschedulable && node.Annotations[platform.AnnotationPoolCordon] != valueTrue {
		return "the node was cordoned by someone else"
	}
	if as.minLifetime > 0 && now.Sub(view.geass.CreationTimestamp.Time) < as.minLifetime {
		return fmt.Sprintf("the node is younger than %s", as.minLifetime)
	}
	for _, pod := range pods {
		if !platform.Movable(pod) {
			continue
		}
		if blocker := platform.RemovalBlocker(pod); blocker != "" {
			return blocker
		}
		view.movable = append(view.movable, pod)
	}
	if pool.EffectiveStorage() == geassv1alpha1.NodeStorageAllow {
		if holds, err := r.holdsVolumeReplicas(ctx, node.Name); err != nil || holds {
			if err != nil {
				return "could not tell whether the node holds volume replicas: " + err.Error()
			}
			return "the node holds replicas of a volume"
		}
	}
	return ""
}

// notNeeded returns "" when the node is a candidate for removal: under the utilisation limit and with everything on
// it fitting elsewhere. Otherwise it says why not.
func (r *GeassNodePoolReconciler) notNeeded(view *nodeView, as autoscaling, usage platform.PodUsage, nodes []corev1.Node) string {
	cpuAllocatable := platform.QuantityMillis(view.k8s.Status.Allocatable[corev1.ResourceCPU])
	memoryAllocatable := platform.QuantityBytes(view.k8s.Status.Allocatable[corev1.ResourceMemory])
	cpu, memory := platform.PodsRequests(view.movable)
	view.utilization = platform.Utilization(cpu, memory, cpuAllocatable, memoryAllocatable)
	if len(view.movable) > 0 && view.utilization >= as.utilization {
		return fmt.Sprintf("%.0f%% used, the limit for removal is %.0f%%", view.utilization*100, as.utilization*100)
	}
	var others []platform.NodeCapacity
	for i := range nodes {
		other := &nodes[i]
		if other.Name == view.k8s.Name {
			continue
		}
		requested := usage.PerNode[other.Name]
		others = append(others, platform.NodeCapacity{
			Node:        other,
			CPUFree:     platform.QuantityMillis(other.Status.Allocatable[corev1.ResourceCPU]) - requested.CPUMillis,
			MemoryFree:  platform.QuantityBytes(other.Status.Allocatable[corev1.ResourceMemory]) - requested.MemoryBytes,
			Schedulable: !other.Spec.Unschedulable && nodeReady(other),
		})
	}
	if !platform.FitsElsewhere(view.movable, others) {
		return "its pods would not fit on the other nodes"
	}
	return ""
}

// continueDrains carries on emptying the nodes that were cordoned for removal: it moves their pods, and deletes a node
// once nothing is left on it. A node that will not empty is put back, and the pool waits before it tries again.
func (r *GeassNodePoolReconciler) continueDrains(ctx context.Context, pool *geassv1alpha1.GeassNodePool, nodes *poolNodes) (int, error) {
	draining := 0
	for _, geassNode := range nodes.live {
		name := nodeNameOf(geassNode)
		var k8sNode corev1.Node
		if err := r.Get(ctx, client.ObjectKey{Name: name}, &k8sNode); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return draining, err
		}
		if k8sNode.Annotations[platform.AnnotationPoolCordon] != valueTrue {
			continue
		}
		progress, err := evictFrom(ctx, r.Client, name)
		if err != nil {
			return draining, err
		}
		if progress.Done() {
			if err := r.Delete(ctx, geassNode); err != nil && !apierrors.IsNotFound(err) {
				return draining, err
			}
			r.noteScaleDown(pool, 1)
			continue
		}
		since := r.now()
		if raw, ok := k8sNode.Annotations[platform.AnnotationPoolCordonAt]; ok {
			if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
				since = parsed
			}
		}
		if r.now().Sub(since) > platform.ScaleDownDrainTimeout {
			logf.FromContext(ctx).Info("A node would not empty, putting it back", "pool", pool.Name, "node", name, "remaining", progress.Remaining, "blocked", progress.Blocked)
			if err := uncordonNode(ctx, r.Client, name); err != nil {
				return draining, err
			}
			until := metav1.NewTime(r.now().Add(platform.ScaleDownBackoff))
			pool.Status.ScaleDownBackoffUntil = &until
			pool.Status.UnneededSince = nil
			continue
		}
		draining++
	}
	return draining, nil
}

// Longhorn's replicas are read as unstructured, like its nodes and volumes, to tell whether a node holds one.
// +kubebuilder:rbac:groups=longhorn.io,resources=replicas,verbs=get;list;watch

var longhornReplica = schema.GroupVersionKind{Group: "longhorn.io", Version: "v1beta2", Kind: "ReplicaList"}

// holdsVolumeReplicas reports whether Longhorn keeps a replica of a volume on the node. A node that does is not
// removed: the volume would run on one copy less until another is rebuilt, and may lose its last one.
func (r *GeassNodePoolReconciler) holdsVolumeReplicas(ctx context.Context, node string) (bool, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(longhornReplica)
	if err := r.List(ctx, list, client.InNamespace("longhorn-system")); err != nil {
		if meta.IsNoMatchError(err) || apierrors.IsNotFound(err) {
			return false, nil // Longhorn is not installed
		}
		return false, err
	}
	for _, item := range list.Items {
		if id, _, _ := unstructured.NestedString(item.Object, "spec", "nodeID"); id == node {
			return true, nil
		}
	}
	return false, nil
}
