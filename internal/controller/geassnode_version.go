package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/release"
)

// controlPlaneLabel is set on every control plane node, with or without a value.
const controlPlaneLabel = "node-role.kubernetes.io/control-plane"

// workerK3sVersion is the k3s version a new worker installs. It has to be the version the control plane
// runs: an agent must not be newer than its server, and a different one is a cluster nobody tested.
//
// The cluster object's spec.version cannot be used for this. It is the Geass cluster's own version, and
// the installer fills it with "v1", which is not a k3s release, so every worker failed to install k3s.
// It is only used when it is itself a k3s version (an operator who pinned one on purpose); otherwise the
// answer is empty, and k3s's installer then picks its current stable release.
//
// hosted is the release a control plane that is not one of the cluster's nodes reports (see hostedServerVersion),
// and empty for a cluster with a control plane node.
func workerK3sVersion(nodes []corev1.Node, hosted string, cluster *geassv1alpha1.GeassCluster) string {
	if lowest := oldestK3sVersion(nodes); lowest != "" {
		return lowest
	}
	if hosted != "" {
		return hosted
	}
	if release.ValidK3s(cluster.Spec.Version) {
		return cluster.Spec.Version
	}
	return ""
}

// controlPlaneNodes lists the control plane nodes, which a worker's join settings are worked out from: the
// k3s version it installs and the address it joins on.
func controlPlaneNodes(ctx context.Context, c client.Reader) ([]corev1.Node, error) {
	var nodes corev1.NodeList
	if err := c.List(ctx, &nodes, client.HasLabels{controlPlaneLabel}); err != nil {
		return nil, err
	}
	return nodes.Items, nil
}
