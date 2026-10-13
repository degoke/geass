package controller

import (
	"cmp"
	"net"
	"slices"
	"strings"

	corev1 "k8s.io/api/core/v1"

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
func workerServerURL(cluster *geassv1alpha1.GeassCluster, controlPlane []corev1.Node) string {
	serverURL := cluster.Spec.ServerURL
	if !platform.IsLoopbackURL(serverURL) {
		return serverURL
	}
	// A node that is up is the safer one to name, and sorting keeps the answer the same between reconciles.
	// The nodes are sorted through pointers: a Node is a large struct.
	nodes := make([]*corev1.Node, len(controlPlane))
	for i := range controlPlane {
		nodes[i] = &controlPlane[i]
	}
	slices.SortStableFunc(nodes, func(a, b *corev1.Node) int {
		if readyA, readyB := nodeReady(a), nodeReady(b); readyA != readyB {
			if readyA {
				return -1
			}
			return 1
		}
		return cmp.Compare(a.Name, b.Name)
	})
	for _, node := range nodes {
		host := controlPlaneHost(node, cluster.Spec.Network.MultiSite)
		if host == "" {
			continue
		}
		if resolved, ok := platform.WithURLHost(serverURL, host); ok {
			return resolved
		}
	}
	return serverURL
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
			// The address becomes part of a command that runs as root on the worker, and a node reports it
			// itself, so only an IP address is taken: anything else is skipped.
			if host := strings.TrimSpace(addr.Address); addr.Type == want && net.ParseIP(host) != nil {
				return host
			}
		}
	}
	return ""
}
