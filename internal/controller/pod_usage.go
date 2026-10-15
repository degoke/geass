package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	toolscache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/cache"

	"github.com/degoke/geass/pkg/platform"
)

// PodUsageTracker keeps a platform.PodUsageIndex current from the operator's cache of pods. It adds no watch of its
// own: the cache already holds every pod for the controllers that read them, and this listens to what it is told.
type PodUsageTracker struct {
	Cache cache.Cache
	Index *platform.PodUsageIndex
}

// NeedLeaderElection is false: the index is read by whichever controller needs it, and the operator that is not the
// leader has no controller running, but costs nothing to keep it current.
func (t *PodUsageTracker) NeedLeaderElection() bool { return false }

// Start listens to pod events until the context ends. The index is marked ready once it has been given every pod that
// existed when it began to listen; until then the capacity check reads the pods itself.
func (t *PodUsageTracker) Start(ctx context.Context) error {
	informer, err := t.Cache.GetInformer(ctx, &corev1.Pod{})
	if err != nil {
		return fmt.Errorf("could not watch pods for the capacity check: %w", err)
	}
	registration, err := informer.AddEventHandler(toolscache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			if pod, ok := obj.(*corev1.Pod); ok {
				t.Index.Set(pod)
			}
		},
		UpdateFunc: func(_, obj any) {
			if pod, ok := obj.(*corev1.Pod); ok {
				t.Index.Set(pod)
			}
		},
		DeleteFunc: func(obj any) {
			if tombstone, ok := obj.(toolscache.DeletedFinalStateUnknown); ok {
				obj = tombstone.Obj
			}
			if pod, ok := obj.(*corev1.Pod); ok {
				t.Index.Remove(pod)
			}
		},
	})
	if err != nil {
		return fmt.Errorf("could not listen to pods for the capacity check: %w", err)
	}
	if !toolscache.WaitForCacheSync(ctx.Done(), registration.HasSynced) {
		return nil
	}
	t.Index.SetReady()
	<-ctx.Done()
	return informer.RemoveEventHandler(registration)
}
