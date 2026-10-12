package platform

import (
	"context"
	"reflect"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/runtime"
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

// PatchStatus applies mutate to obj's status and sends it to the API server only if the status ended up
// different from what it was. It reports whether it wrote.
//
// A reconcile that finds the world as it left it would otherwise rewrite the same status every time it
// runs: a request to the API server for nothing, and for any field that carries a timestamp, a real
// write that wakes every watcher of the object. Comparing first makes the steady state free.
//
// The write is a merge patch of the status, with no resourceVersion check, so two writers that change
// different fields do not make each other fail, and a stale copy cannot overwrite a field it did not change.
// obj must be a fresh read: mutate edits it in place.
func PatchStatus(ctx context.Context, c client.Client, obj client.Object, mutate func()) (bool, error) {
	kind := reflect.TypeOf(obj).Elem().Name()
	base, ok := obj.DeepCopyObject().(client.Object)
	if !ok {
		return false, nil
	}
	mutate()

	changed, err := statusChanged(base, obj)
	if err != nil {
		statusWrites.WithLabelValues(kind, StatusFailed).Inc()
		return false, err
	}
	if !changed {
		statusWrites.WithLabelValues(kind, StatusSkipped).Inc()
		return false, nil
	}
	if err := c.Status().Patch(ctx, obj, client.MergeFrom(base)); err != nil {
		statusWrites.WithLabelValues(kind, StatusFailed).Inc()
		return false, err
	}
	statusWrites.WithLabelValues(kind, StatusWritten).Inc()
	return true, nil
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
