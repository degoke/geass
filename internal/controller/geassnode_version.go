package controller

import (
	"context"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/version"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
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
func workerK3sVersion(nodes []corev1.Node, cluster *geassv1alpha1.GeassCluster) string {
	lowest := ""
	for i := range nodes {
		kubelet := nodes[i].Status.NodeInfo.KubeletVersion
		if !isK3sVersion(kubelet) {
			continue
		}
		if lowest == "" || olderK3s(kubelet, lowest) {
			lowest = kubelet
		}
	}
	if lowest != "" {
		return lowest
	}
	if isK3sVersion(cluster.Spec.Version) {
		return cluster.Spec.Version
	}
	return ""
}

// controlPlaneNodes lists the control plane nodes, which a worker's join settings are worked out from: the
// k3s version it installs and the address it joins on.
func controlPlaneNodes(ctx context.Context, c client.Client) ([]corev1.Node, error) {
	var nodes corev1.NodeList
	if err := c.List(ctx, &nodes, client.HasLabels{controlPlaneLabel}); err != nil {
		return nil, err
	}
	return nodes.Items, nil
}

// isK3sVersion reports whether v looks like a k3s release, such as v1.36.5+k3s1.
func isK3sVersion(v string) bool {
	return strings.Contains(v, "+k3s") && strings.HasPrefix(v, "v")
}

// olderK3s reports whether a is an older k3s release than b.
func olderK3s(a, b string) bool {
	av, aErr := version.ParseGeneric(strings.SplitN(a, "+", 2)[0])
	bv, bErr := version.ParseGeneric(strings.SplitN(b, "+", 2)[0])
	if aErr != nil || bErr != nil {
		return a < b
	}
	if av.LessThan(bv) {
		return true
	}
	if bv.LessThan(av) {
		return false
	}
	return a < b
}
