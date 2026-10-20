package controller

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// Every status write wakes every watcher of an object, the controller that wrote it included. These
// predicates let a controller, or a watch on another kind, ignore the events that cannot change what it
// does. They are used only where that is known: the dashboard sometimes writes status to wake a
// controller (retrying a failed provision, cancelling a build), so a controller that can be woken that
// way has to keep seeing status changes.

// specOrMetadataChanged lets through what a controller of a kind nobody wakes through its status acts on:
// creation, deletion, a change to the spec (which advances the generation), to annotations, or to
// finalizers and the deletion timestamp.
func specOrMetadataChanged() predicate.Predicate {
	return predicate.Or(
		predicate.GenerationChangedPredicate{},
		predicate.AnnotationChangedPredicate{},
		deletionOrFinalizersChanged(),
	)
}

func deletionOrFinalizersChanged() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			if e.ObjectOld == nil || e.ObjectNew == nil {
				return true
			}
			return e.ObjectOld.GetDeletionTimestamp().IsZero() != e.ObjectNew.GetDeletionTimestamp().IsZero() ||
				len(e.ObjectOld.GetFinalizers()) != len(e.ObjectNew.GetFinalizers())
		},
		CreateFunc:  func(event.CreateEvent) bool { return false },
		DeleteFunc:  func(event.DeleteEvent) bool { return false },
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
}

// readyOrSpecChanged is for a watch on a project or a cluster that other resources depend on: they care
// what it asks for, and whether it is ready, and not about the rest of its status (a project's webhook
// deliveries and pull requests, which change all the time).
func readyOrSpecChanged() predicate.Predicate {
	return predicate.Or(
		predicate.GenerationChangedPredicate{},
		predicate.Funcs{
			UpdateFunc: func(e event.UpdateEvent) bool {
				return readyStatus(e.ObjectOld) != readyStatus(e.ObjectNew)
			},
			CreateFunc:  func(event.CreateEvent) bool { return false },
			DeleteFunc:  func(event.DeleteEvent) bool { return false },
			GenericFunc: func(event.GenericEvent) bool { return false },
		},
	)
}

// readyStatus summarises what dependents read of a project or a cluster: whether it is ready and, for a
// cluster, whether its add-ons are.
func readyStatus(obj client.Object) string {
	var conditions []metav1.Condition
	switch typed := obj.(type) {
	case *geassv1alpha1.GeassProject:
		conditions = typed.Status.Conditions
	case *geassv1alpha1.GeassCluster:
		conditions = typed.Status.Conditions
	default:
		return ""
	}
	return conditionStatus(conditions, platform.ConditionReady) + "/" + conditionStatus(conditions, platform.ConditionAddonsReady)
}

// databaseGoneOrDeleting is for a watch that only needs to know a database is on its way out.
func databaseGoneOrDeleting() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc:  func(event.CreateEvent) bool { return false },
		DeleteFunc:  func(event.DeleteEvent) bool { return true },
		GenericFunc: func(event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			return e.ObjectOld.GetDeletionTimestamp().IsZero() && !e.ObjectNew.GetDeletionTimestamp().IsZero()
		},
	}
}

// appForDeployment maps a service's Deployment to the service, by the labels the operator puts on it. A
// Deployment that is not a service's is not mapped.
func appForDeployment(_ context.Context, obj client.Object) []ctrl.Request {
	labels := obj.GetLabels()
	if labels[labelGeassKind] != "GeassApp" || labels[labelGeassName] == "" || labels[labelGeassSystemNS] == "" {
		return nil
	}
	return []ctrl.Request{{NamespacedName: client.ObjectKey{Name: labels[labelGeassName], Namespace: labels[labelGeassSystemNS]}}}
}

// deploymentScaleChanged lets through the changes to a Deployment that a service's state follows: how many
// copies are wanted and how many are up. Everything else about a Deployment changing is the operator's own
// doing and is already being reconciled.
func deploymentScaleChanged() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			before, okBefore := e.ObjectOld.(*appsv1.Deployment)
			after, okAfter := e.ObjectNew.(*appsv1.Deployment)
			if !okBefore || !okAfter {
				return false
			}
			return deploymentReplicasOf(before) != deploymentReplicasOf(after) ||
				before.Status.AvailableReplicas != after.Status.AvailableReplicas ||
				before.Status.UpdatedReplicas != after.Status.UpdatedReplicas ||
				before.Status.Replicas != after.Status.Replicas
		},
		CreateFunc:  func(event.CreateEvent) bool { return false },
		DeleteFunc:  func(event.DeleteEvent) bool { return false },
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
}

// poolCreatedOrDeleted lets through a node pool appearing or going away, and one that changes whether its nodes may
// hold data. Those are what decide whether services tolerate the taint of such nodes.
func poolCreatedOrDeleted() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc:  func(event.CreateEvent) bool { return true },
		DeleteFunc:  func(event.DeleteEvent) bool { return true },
		GenericFunc: func(event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			before, okBefore := e.ObjectOld.(*geassv1alpha1.GeassNodePool)
			after, okAfter := e.ObjectNew.(*geassv1alpha1.GeassNodePool)
			return okBefore && okAfter && before.EffectiveStorage() != after.EffectiveStorage()
		},
	}
}

// ephemeralNodeChanged lets through a server added on its own that must not hold data, appearing, going away or
// changing that. The servers of a pool are left out: the pool's own watch covers them, and a pool that scales would
// otherwise have every service reconciled each time it adds or removes one.
func ephemeralNodeChanged() predicate.Predicate {
	ephemeral := func(o client.Object) bool {
		node, ok := o.(*geassv1alpha1.GeassNode)
		return ok && node.Spec.Pool == "" && node.Spec.Storage == geassv1alpha1.NodeStorageNone
	}
	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return ephemeral(e.Object) },
		DeleteFunc:  func(e event.DeleteEvent) bool { return ephemeral(e.Object) },
		GenericFunc: func(event.GenericEvent) bool { return false },
		UpdateFunc:  func(e event.UpdateEvent) bool { return ephemeral(e.ObjectOld) != ephemeral(e.ObjectNew) },
	}
}
