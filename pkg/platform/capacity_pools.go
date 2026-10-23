package platform

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// PoolsCanMakeRoom reports whether an autoscaling pool could add nodes that make the workload fit. The pods of a
// workload that is let in on that promise start out unable to be placed, which is what makes the pool grow; when the
// pool turns out to be at its maximum they stay that way, and the pool says so in its status.
//
// It is the one answer for every place that refuses a workload for lack of room: the operator, when it makes the
// workload, and the dashboard, the command line and the API, when someone asks for it. A check that did not ask
// the pools would refuse the very workload the pool is there to make room for.
func PoolsCanMakeRoom(pools []geassv1alpha1.GeassNodePool, nodes []corev1.Node, used PodUsage, placement Placement, est WorkloadEstimate) bool {
	for i := range pools {
		pool := &pools[i]
		if !pool.AutoscalingEnabled() || pool.Spec.Suspend || len(pool.Status.NodeAllocatable) == 0 {
			continue
		}
		if pool.Status.Phase != geassv1alpha1.NodePoolReady && pool.Status.Phase != geassv1alpha1.NodePoolScaling {
			continue
		}
		// Nodes that must not hold data take services only: a database is not made room for there.
		if pool.EffectiveStorage() == geassv1alpha1.NodeStorageNone && est.Kind != WorkloadService {
			continue
		}
		room := int(pool.Spec.MaxNodes) - int(pool.Status.Nodes)
		if room <= 0 {
			continue
		}
		labels, taints := PoolNodeIdentity(pool)
		template := TemplateFor(labels, taints, pool.Status.NodeAllocatable, 0, 0)
		grown := append([]corev1.Node(nil), nodes...)
		for k := range room {
			grown = append(grown, template.Node(fmt.Sprintf("%s-new-%d", pool.Name, k)))
		}
		snapshot := ComputeClusterSnapshotFromUsage(grown, used, placement)
		if ok, _ := snapshot.Fits(est); ok {
			return true
		}
	}
	return false
}

// EphemeralNodesInUse reports whether a pool or a server that must not hold data exists, which is when services have
// to tolerate their taint. A cluster without one never changes what its services are made of. A list that fails is an
// error and not "none": answering none would take the toleration off every service and roll them all.
func EphemeralNodesInUse(ctx context.Context, c client.Reader) (bool, error) {
	// The lists are only read, so the cache's own objects are used as they are.
	var pools geassv1alpha1.GeassNodePoolList
	if err := c.List(ctx, &pools, client.InNamespace(SystemNamespace), client.UnsafeDisableDeepCopy); err != nil {
		return false, fmt.Errorf("listing the node pools: %w", err)
	}
	for i := range pools.Items {
		if pools.Items[i].EffectiveStorage() == geassv1alpha1.NodeStorageNone {
			return true, nil
		}
	}
	var nodes geassv1alpha1.GeassNodeList
	if err := c.List(ctx, &nodes, client.InNamespace(SystemNamespace), client.UnsafeDisableDeepCopy); err != nil {
		return false, fmt.Errorf("listing the nodes: %w", err)
	}
	for i := range nodes.Items {
		if nodes.Items[i].Spec.Storage == geassv1alpha1.NodeStorageNone && nodes.Items[i].DeletionTimestamp.IsZero() {
			return true, nil
		}
	}
	return false, nil
}
