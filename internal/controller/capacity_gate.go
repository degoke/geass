package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// workloadFitsClusterUnlessCreated gates only the creation of a workload. Once its object exists, the
// pods it owns are already counted in the cluster's requested resources, so comparing its full
// estimate with what is left would reject a healthy running workload on a nearly full cluster.
func workloadFitsClusterUnlessCreated(ctx context.Context, c client.Client, existing client.Object, est platform.WorkloadEstimate) (bool, string) {
	return workloadFitsPlacementUnlessCreated(ctx, c, nil, existing, est, platform.Placement{})
}

// workloadFitsPlacementUnlessCreated is workloadFitsClusterUnlessCreated for a workload that may only
// run on some nodes: the room it needs must be on those nodes, not just somewhere in the cluster.
// The usage, when there is one, is what the pods ask for; without it the pods are read.
func workloadFitsPlacementUnlessCreated(ctx context.Context, c client.Client, usage *platform.PodUsageIndex, existing client.Object, est platform.WorkloadEstimate, placement platform.Placement) (bool, string) {
	if c != nil {
		if err := c.Get(ctx, client.ObjectKeyFromObject(existing), existing); err == nil {
			return true, ""
		}
	}
	return workloadFits(ctx, c, usage, est, placement)
}

// workloadFits asks whether the nodes have room for a workload. Reading every pod to add up what they ask for costs
// more the more pods there are, and the check runs for each workload that is created, so a cluster that is filled with
// services all at once spent more on each than on the one before. The index of pod usage is what avoids it.
func workloadFits(ctx context.Context, c client.Client, usage *platform.PodUsageIndex, est platform.WorkloadEstimate, placement platform.Placement) (bool, string) {
	if c == nil {
		return false, platform.UnknownCapacityMessage(est)
	}
	var nodes corev1.NodeList
	// The snapshot only reads the nodes, so the cache need not copy them for each workload that is created.
	if err := c.List(ctx, &nodes, client.UnsafeDisableDeepCopy); err != nil {
		return false, platform.UnknownCapacityMessage(est)
	}
	var used platform.PodUsage
	if usage.Ready() {
		used = usage.Usage()
	} else {
		var pods corev1.PodList
		if err := c.List(ctx, &pods); err != nil {
			return false, platform.UnknownCapacityMessage(est)
		}
		used = platform.UsageOfPods(pods.Items)
	}
	snapshot := platform.ComputeClusterSnapshotFromUsage(nodes.Items, used, placement)
	if !snapshot.Known && (placement.IsZero() || len(nodes.Items) == 0) {
		// Nodes that have not reported capacity yet are not a reason to refuse a workload that may run
		// anywhere. A workload pinned to a region or tag, though, needs a node there to exist at all.
		return true, ""
	}
	ok, message := snapshot.Fits(est)
	if !ok && poolsCanMakeRoom(ctx, c, nodes.Items, used, placement, est) {
		// The workload is let in, and its pods wait for the node that the pool will add.
		return true, ""
	}
	return ok, message
}

// poolsCanMakeRoom reports whether an autoscaling pool could add nodes that make the workload fit. The pods of a
// workload that is let in on that promise start out unable to be placed, which is what makes the pool grow; when the
// pool turns out to be at its maximum they stay that way, and the pool says so in its status.
func poolsCanMakeRoom(ctx context.Context, c client.Reader, nodes []corev1.Node, used platform.PodUsage, placement platform.Placement, est platform.WorkloadEstimate) bool {
	var pools geassv1alpha1.GeassNodePoolList
	if err := c.List(ctx, &pools, client.InNamespace(platform.SystemNamespace)); err != nil {
		return false
	}
	for i := range pools.Items {
		pool := &pools.Items[i]
		if !pool.AutoscalingEnabled() || pool.Spec.Suspend || len(pool.Status.NodeAllocatable) == 0 {
			continue
		}
		if pool.Status.Phase != geassv1alpha1.NodePoolReady && pool.Status.Phase != geassv1alpha1.NodePoolScaling {
			continue
		}
		// Nodes that must not hold data take services only: a database is not made room for there.
		if pool.EffectiveStorage() == geassv1alpha1.NodeStorageNone && est.Kind != platform.WorkloadService {
			continue
		}
		room := int(pool.Spec.MaxNodes) - int(pool.Status.Nodes)
		if room <= 0 {
			continue
		}
		labels, taints := platform.PoolNodeIdentity(pool)
		template := platform.TemplateFor(labels, taints, pool.Status.NodeAllocatable, 0, 0)
		grown := append([]corev1.Node(nil), nodes...)
		for k := range room {
			grown = append(grown, template.Node(fmt.Sprintf("%s-new-%d", pool.Name, k)))
		}
		snapshot := platform.ComputeClusterSnapshotFromUsage(grown, used, placement)
		if ok, _ := snapshot.Fits(est); ok {
			return true
		}
	}
	return false
}

// ephemeralNodesInUse reports whether a pool or a server that must not hold data exists, which is when services have
// to tolerate their taint. A cluster without one never changes what its services are made of. A list that fails is an
// error and not "none": answering none would take the toleration off every service and roll them all.
func ephemeralNodesInUse(ctx context.Context, c client.Reader) (bool, error) {
	// The lists are only read, so the cache's own objects are used as they are.
	var pools geassv1alpha1.GeassNodePoolList
	if err := c.List(ctx, &pools, client.InNamespace(platform.SystemNamespace), client.UnsafeDisableDeepCopy); err != nil {
		return false, fmt.Errorf("listing the node pools: %w", err)
	}
	for i := range pools.Items {
		if pools.Items[i].EffectiveStorage() == geassv1alpha1.NodeStorageNone {
			return true, nil
		}
	}
	var nodes geassv1alpha1.GeassNodeList
	if err := c.List(ctx, &nodes, client.InNamespace(platform.SystemNamespace), client.UnsafeDisableDeepCopy); err != nil {
		return false, fmt.Errorf("listing the nodes: %w", err)
	}
	for i := range nodes.Items {
		if nodes.Items[i].Spec.Storage == geassv1alpha1.NodeStorageNone && nodes.Items[i].DeletionTimestamp.IsZero() {
			return true, nil
		}
	}
	return false, nil
}
