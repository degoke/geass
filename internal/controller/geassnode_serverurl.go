package controller

import (
	"cmp"
	"context"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// workerServerURL is the address a new worker joins the cluster on: the cluster's spec.serverURL.
//
// A cluster installed before the installer stopped writing https://127.0.0.1:6443 there still holds it, and
// so does one whose installer could not work out its own address. That is the control plane's address
// from the control plane's own point of view, which no worker can use, and it used to surface as a
// firewall error. It is replaced by the address the control plane node registered under: its public
// address in a multi-site cluster, whose workers are at other providers, and its private one otherwise.
// The cluster object is not edited. If no control plane node has an address, the URL is returned as it
// is and the worker's own reachability check explains what is wrong.
func workerServerURL(ctx context.Context, c client.Client, cluster *geassv1alpha1.GeassCluster) (string, error) {
	serverURL := cluster.Spec.ServerURL
	if !platform.IsLoopbackURL(serverURL) {
		return serverURL, nil
	}
	var nodes corev1.NodeList
	if err := c.List(ctx, &nodes, client.HasLabels{controlPlaneLabel}); err != nil {
		return "", err
	}
	// A node that is up is the safer one to name, and sorting keeps the answer the same between reconciles.
	slices.SortStableFunc(nodes.Items, func(a, b corev1.Node) int {
		if readyA, readyB := nodeReady(&a), nodeReady(&b); readyA != readyB {
			if readyA {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.Name, b.Name)
	})
	for i := range nodes.Items {
		host := controlPlaneHost(&nodes.Items[i], cluster.Spec.Network.MultiSite)
		if host == "" {
			continue
		}
		if resolved, ok := platform.WithURLHost(serverURL, host); ok {
			return resolved, nil
		}
	}
	return serverURL, nil
}

// controlPlaneHost is the address of a control plane node that workers should join on. A multi-site
// cluster prefers the external address, since its workers are at other providers; every other cluster
// prefers the internal one, which is the network its nodes share.
func controlPlaneHost(node *corev1.Node, multiSite bool) string {
	first, second := corev1.NodeInternalIP, corev1.NodeExternalIP
	if multiSite {
		first, second = second, first
	}
	for _, want := range []corev1.NodeAddressType{first, second} {
		for _, addr := range node.Status.Addresses {
			if host := strings.TrimSpace(addr.Address); addr.Type == want && host != "" {
				return host
			}
		}
	}
	return ""
}
