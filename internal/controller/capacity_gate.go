package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/degoke/geass/pkg/platform"
)

// workloadFitsClusterUnlessCreated gates only the creation of a workload. Once its object exists, the
// pods it owns are already counted in the cluster's requested resources, so comparing its full
// estimate with what is left would reject a healthy running workload on a nearly full cluster.
func workloadFitsClusterUnlessCreated(ctx context.Context, c client.Client, existing client.Object, est platform.WorkloadEstimate) (bool, string) {
	return workloadFitsPlacementUnlessCreated(ctx, c, existing, est, platform.Placement{})
}

// workloadFitsPlacementUnlessCreated is workloadFitsClusterUnlessCreated for a workload that may only
// run on some nodes: the room it needs must be on those nodes, not just somewhere in the cluster.
func workloadFitsPlacementUnlessCreated(ctx context.Context, c client.Client, existing client.Object, est platform.WorkloadEstimate, placement platform.Placement) (bool, string) {
	if c != nil {
		if err := c.Get(ctx, client.ObjectKeyFromObject(existing), existing); err == nil {
			return true, ""
		}
	}
	return workloadFits(ctx, c, est, placement)
}

func workloadFits(ctx context.Context, c client.Client, est platform.WorkloadEstimate, placement platform.Placement) (bool, string) {
	if c == nil {
		return false, platform.UnknownCapacityMessage(est)
	}
	var nodes corev1.NodeList
	if err := c.List(ctx, &nodes); err != nil {
		return false, platform.UnknownCapacityMessage(est)
	}
	var pods corev1.PodList
	if err := c.List(ctx, &pods); err != nil {
		return false, platform.UnknownCapacityMessage(est)
	}
	snapshot := platform.ComputeClusterSnapshotFor(nodes.Items, pods.Items, placement)
	if !snapshot.Known && (placement.IsZero() || len(nodes.Items) == 0) {
		// Nodes that have not reported capacity yet are not a reason to refuse a workload that may run
		// anywhere. A workload pinned to a region or tag, though, needs a node there to exist at all.
		return true, ""
	}
	return snapshot.Fits(est)
}
