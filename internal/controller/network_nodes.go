package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const (
	kubernetesServiceName      = "kubernetes"
	kubernetesServiceNamespace = "default"
)

// nodeNetworkFor describes the cluster's machines for a project that keeps its services away from them, and is
// nil for one that does not. It is built from the nodes as they are now: a policy is only as current as the
// last time it was written, so a change to the nodes has to reach the project (see projectsForNodeChange).
func (r *GeassProjectReconciler) nodeNetworkFor(ctx context.Context, project *geassv1alpha1.GeassProject) (*platform.NodeNetwork, error) {
	if !geassv1alpha1.ProjectBlocksNodeAddresses(project) {
		return nil, nil
	}
	var nodes corev1.NodeList
	// Nothing here changes a node, so the cache need not copy each one (with its image list and conditions) for every
	// project that is reconciled, which on a restart is every project at once.
	if err := r.List(ctx, &nodes, client.UnsafeDisableDeepCopy); err != nil {
		return nil, err
	}
	return platform.NodeNetworkOf(nodes.Items, r.apiServerPort(ctx)), nil
}

// apiServerPort is the port the control plane serves the API on at its own address, which is where the
// cluster's own "kubernetes" Service sends what pods ask of the API. A cluster that does not say uses the
// default.
func (r *GeassProjectReconciler) apiServerPort(ctx context.Context) int32 {
	var service corev1.Service
	if err := r.Get(ctx, client.ObjectKey{Name: kubernetesServiceName, Namespace: kubernetesServiceNamespace}, &service); err != nil {
		return platform.DefaultAPIServerPort
	}
	for _, port := range service.Spec.Ports {
		if port.TargetPort.IntValue() > 0 {
			return int32(port.TargetPort.IntValue())
		}
	}
	return platform.DefaultAPIServerPort
}

// projectsForNodeChange wakes the projects that block node addresses when a node joins, leaves or changes an
// address. The others have no rule that names a node, so they have nothing to update.
func (r *GeassProjectReconciler) projectsForNodeChange(ctx context.Context, _ client.Object) []ctrl.Request {
	var projects geassv1alpha1.GeassProjectList
	if err := r.List(ctx, &projects, client.InNamespace(platform.SystemNamespace)); err != nil {
		return nil
	}
	requests := make([]ctrl.Request, 0)
	for i := range projects.Items {
		if geassv1alpha1.ProjectBlocksNodeAddresses(&projects.Items[i]) {
			requests = append(requests, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&projects.Items[i])})
		}
	}
	return requests
}

// nodeNetworkChanged lets through the node events that change what a policy would say: a node appearing or
// going, and a change to its addresses, its pod range or whether it is a control plane node. A node reports
// its status every few seconds, and none of that is of interest here.
func nodeNetworkChanged() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc:  func(event.CreateEvent) bool { return true },
		DeleteFunc:  func(event.DeleteEvent) bool { return true },
		GenericFunc: func(event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			before, okBefore := e.ObjectOld.(*corev1.Node)
			after, okAfter := e.ObjectNew.(*corev1.Node)
			if !okBefore || !okAfter {
				return false
			}
			// The role is compared as well as the result, because a cluster whose nodes carry no role label
			// describes itself the same whichever node loses or gains one.
			return platform.IsControlPlaneNode(before) != platform.IsControlPlaneNode(after) ||
				!platform.NodeNetworkOf([]corev1.Node{*before}, 0).Equal(platform.NodeNetworkOf([]corev1.Node{*after}, 0))
		},
	}
}
