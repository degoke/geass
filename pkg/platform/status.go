package platform

import (
	"context"
	"reflect"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// statusWrites counts what PatchStatus did, per kind: the share of "skipped" is how many writes the
// controllers no longer send because nothing changed.
var statusWrites = prometheus.NewCounterVec(prometheus.CounterOpts{
	Name: "geass_status_writes_total",
	Help: "Status updates the operator considered, by resource kind and what happened: written, skipped because nothing changed, or failed.",
}, []string{"kind", "result"})

func init() {
	metrics.Registry.MustRegister(statusWrites)
}

// Results of PatchStatus, as they appear on the geass_status_writes_total metric.
const (
	StatusWritten = "written"
	StatusSkipped = "skipped"
	StatusFailed  = "failed"
)

// statusRetry is how PatchStatus waits between tries after a conflict. The copy it reads again comes from the
// informer cache, which can be a moment behind the write that caused the conflict.
var statusRetry = wait.Backoff{Steps: 5, Duration: 25 * time.Millisecond, Factor: 2, Jitter: 0.1}

// PatchStatus applies mutate to obj's status and sends it to the API server only if the status ended up
// different from what it was. It reports whether it wrote.
//
// A reconcile that finds the world as it left it would otherwise rewrite the same status every time it
// runs: a request to the API server for nothing, and for any field that carries a timestamp, a real
// write that wakes every watcher of the object. Comparing first makes the steady state free.
//
// The write is a merge patch of the status, which sends a list such as the conditions as a whole. So that
// two writers that change different conditions cannot overwrite each other, the patch carries the
// resourceVersion it was made from: if the object has moved on, the write is refused, the object is read
// again, mutate is applied to that, and the write is tried again. A mutate function is therefore run more
// than once on a conflict, and has to do the same thing whenever it is run. obj must be a fresh read:
// mutate edits it in place, and on a conflict it is replaced by a newer one.
func PatchStatus(ctx context.Context, c client.Client, obj client.Object, mutate func()) (bool, error) {
	kind := reflect.TypeOf(obj).Elem().Name()
	key := client.ObjectKeyFromObject(obj)
	var wrote bool
	err := retry.OnError(statusRetry, apierrors.IsConflict, func() error {
		base, ok := obj.DeepCopyObject().(client.Object)
		if !ok {
			return nil
		}
		mutate()

		changed, err := statusChanged(base, obj)
		if err != nil || !changed {
			wrote = false
			return err
		}
		err = c.Status().Patch(ctx, obj, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
		if apierrors.IsConflict(err) {
			if getErr := c.Get(ctx, key, obj); getErr != nil {
				return getErr
			}
			return err
		}
		wrote = err == nil
		return err
	})
	switch {
	case err != nil:
		statusWrites.WithLabelValues(kind, StatusFailed).Inc()
		return false, err
	case wrote:
		statusWrites.WithLabelValues(kind, StatusWritten).Inc()
	default:
		statusWrites.WithLabelValues(kind, StatusSkipped).Inc()
	}
	return wrote, nil
}

// statusChanged compares only the status of two copies of an object. It works on the unstructured form so
// that it needs to know nothing about the type, and uses semantic equality so that equivalent values (an
// empty list and a missing one, a time with a different location) are not taken for a change.
func statusChanged(before, after client.Object) (bool, error) {
	beforeStatus, err := statusOf(before)
	if err != nil {
		return false, err
	}
	afterStatus, err := statusOf(after)
	if err != nil {
		return false, err
	}
	return !equality.Semantic.DeepEqual(beforeStatus, afterStatus), nil
}

func statusOf(obj client.Object) (any, error) {
	content, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		return nil, err
	}
	return content["status"], nil
}
