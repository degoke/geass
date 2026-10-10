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
	if c != nil {
		if err := c.Get(ctx, client.ObjectKeyFromObject(existing), existing); err == nil {
			return true, ""
		}
	}
	return workloadFitsCluster(ctx, c, est)
}

func workloadFitsCluster(ctx context.Context, c client.Client, est platform.WorkloadEstimate) (bool, string) {
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
	snapshot := platform.ComputeClusterSnapshot(nodes.Items, pods.Items)
	if !snapshot.Known {
		return true, ""
	}
	return snapshot.Fits(est)
}
