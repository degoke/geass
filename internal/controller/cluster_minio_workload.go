package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/degoke/geass/pkg/platform"
)

func isClusterMinIOWorkloadPod(pod *corev1.Pod) bool {
	return isHelmReleasePod(pod, platform.SystemNamespace, platform.ClusterMinIOChartName)
}

func clusterMinIOWorkloadReady(ctx context.Context, c client.Client) (bool, string, error) {
	return helmReleaseWorkloadReady(ctx, c, platform.SystemNamespace, platform.ClusterMinIOChartName, 1)
}
