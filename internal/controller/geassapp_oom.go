package controller

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// recentOOMKill is the latest time one of a service's copies was stopped for memory, when that was lately. Kubernetes
// says it on the pod, in the last state of the container, and does not say it anywhere that a user looks.
func (r *GeassAppReconciler) recentOOMKill(ctx context.Context, app *geassv1alpha1.GeassApp, wsNS string) (platform.OOMKill, bool) {
	var pods corev1.PodList
	// Only read, and a pod in a project's namespace is one of at most a few dozen, so the cache need not copy them.
	if err := r.List(ctx, &pods, client.InNamespace(wsNS), client.MatchingLabels(geassResourceLabels(app, "GeassApp")), client.UnsafeDisableDeepCopy); err != nil {
		return platform.OOMKill{}, false
	}
	return platform.RecentOOMKill(pods.Items, time.Now(), platform.OOMKillWindow)
}

// outOfMemoryCondition sets the condition that says a copy was stopped for memory, or takes it away when none was
// lately. A service that never ran out of memory never has the condition, so nothing is written for it.
func outOfMemoryCondition(conditions []metav1.Condition, kill platform.OOMKill, found bool) []metav1.Condition {
	if !found {
		meta.RemoveStatusCondition(&conditions, platform.ConditionOutOfMemory)
		return conditions
	}
	return platform.SetCondition(conditions, platform.ConditionOutOfMemory, metav1.ConditionTrue, platform.OOMKilledReason, kill.Message())
}

// oomKillExpiry is how long until the kill is no longer recent, which is when the condition has to be looked at again.
func oomKillExpiry(kill platform.OOMKill, now time.Time) time.Duration {
	return max(kill.At.Add(platform.OOMKillWindow).Sub(now), 0) + time.Second
}

// podOOMKilled lets through the change to a pod that is one of its containers being stopped for memory once more.
// A pod changes all the time, and a service need not hear of any of it but this.
func podOOMKilled() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			before, okBefore := e.ObjectOld.(*corev1.Pod)
			after, okAfter := e.ObjectNew.(*corev1.Pod)
			if !okBefore || !okAfter {
				return false
			}
			return platform.LastOOMKill(after).After(platform.LastOOMKill(before))
		},
		CreateFunc:  func(event.CreateEvent) bool { return false },
		DeleteFunc:  func(event.DeleteEvent) bool { return false },
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
}
