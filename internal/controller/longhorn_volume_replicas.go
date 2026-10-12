package controller

import (
	"context"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const (
	// volumeReplicaBatchSize is how many volumes one pass raises. Longhorn builds a
	// new replica by copying the whole volume to another node, so raising every
	// volume the moment a cluster grows would request all of those copies at once.
	// They are spread out instead, this many per volumeReplicaPace. That limits how
	// fast rebuilds are requested, not how many run at the same time: concurrency is
	// bounded by Longhorn's own per-node rebuild limit, and Geass neither waits for a
	// rebuild to finish nor checks that the new replicas found room. Gating on
	// rebuilds in progress was considered and rejected, because a volume that is
	// degraded for an unrelated reason would then stall growth for all the others.
	volumeReplicaBatchSize = 5
	// volumeReplicaPace is the wait before the next batch.
	volumeReplicaPace = platform.RequeueAfterDefault
	// volumeReplicaRetryAfter is the wait after Longhorn refused a volume or could
	// not be read. A refusal is usually a volume in a state that cannot grow yet, so
	// asking again soon only adds load.
	volumeReplicaRetryAfter = 5 * time.Minute
	// volumeReplicaMaxConsecutiveFailures ends a pass early. When this many volumes
	// in a row are refused, Longhorn is the problem rather than the volumes, and
	// trying the rest only adds load. A success resets the count, so a few refused
	// volumes do not hold up the healthy ones behind them.
	volumeReplicaMaxConsecutiveFailures = 5
)

// volumeReplicaPassTimeout bounds one whole pass. The cluster reconciler has a
// single worker, so a pass that waits indefinitely (a webhook that never answers, or
// a cached list that never syncs because the operator's role cannot read
// PersistentVolumes) would stall every cluster reconcile behind it. A variable so
// tests can shorten it.
var volumeReplicaPassTimeout = 30 * time.Second

var longhornVolumeKind = schema.GroupVersionKind{
	Group:   platform.LonghornAPIGroup,
	Version: platform.LonghornAPIVersion,
	Kind:    "Volume",
}

var longhornVolumeListKind = longhornVolumeKind.GroupVersion().WithKind("VolumeList")

// Longhorn's Volume objects are read as unstructured and so bypass the manager's
// cache (it only caches typed objects unless told otherwise): that needs list and
// patch, not watch. Nothing watches longhorn.io on purpose: the CRD does not exist
// on clusters without Longhorn, and a watch on a missing kind fails manager startup.
// The PersistentVolumes in front of them are a core type, so those are watched (see
// replicatedVolumeBound), which is what notices a volume provisioned after a pass.

// +kubebuilder:rbac:groups="",resources=persistentvolumes,verbs=get;list;watch
// +kubebuilder:rbac:groups=longhorn.io,resources=volumes,verbs=get;list;patch

// reconcileLonghornVolumeReplicas brings volumes that already exist up to the
// replica count the replicated class gives new ones. A StorageClass only applies
// to volumes created after it, so without this a cluster that grows from one node
// to three keeps every older volume as a single copy.
//
// It only ever raises, and only volumes whose PersistentVolume is in the
// replicated class: geass-single volumes (HA Postgres, which replicates itself) are
// one copy on purpose, and volumes on local-path or on someone else's class are not
// Geass's to change. It never lowers a count, so a node going away cannot quietly
// drop copies and a node count that flaps cannot make volumes flap with it.
//
// The result is how long to wait before running again, zero meaning there is
// nothing left to do. Nothing here returns an error. This is background work: an
// error from the cluster reconcile would mark the cluster not ready and, if it
// persisted, count toward the failure threshold that pauses the reconciler.
func (r *GeassClusterReconciler) reconcileLonghornVolumeReplicas(ctx context.Context, cluster *geassv1alpha1.GeassCluster) time.Duration {
	// Reconciles run while a cluster is being deleted so its finalizers can finish.
	// Starting rebuilds is not part of that.
	if !geassv1alpha1.LonghornEnabled(cluster.Spec.Addons) || !cluster.DeletionTimestamp.IsZero() {
		return 0
	}
	log := logf.FromContext(ctx)
	ctx, cancel := context.WithTimeout(ctx, volumeReplicaPassTimeout)
	defer cancel()

	installed, err := r.longhornDriverInstalled(ctx)
	if err != nil {
		log.Error(err, "Failed to check for the Longhorn CSI driver")
		return volumeReplicaRetryAfter
	}
	if !installed {
		return 0
	}
	replicas, hosted, err := r.longhornReplicaTarget(ctx)
	if err != nil {
		log.Error(err, "Failed to work out the Longhorn replica count")
		return volumeReplicaRetryAfter
	}
	if replicas <= 1 {
		// Every volume has at least one replica, so none can be below this.
		return 0
	}
	if !hosted {
		// The same wait that holds back the storage classes: Longhorn can only place a
		// replica on a new node once its manager is running. Raising earlier asks for
		// copies that have nowhere to go and leaves the volume degraded.
		return volumeReplicaPace
	}
	desired := int64(replicas)

	names, err := r.replicatedVolumeNames(ctx)
	if err != nil {
		log.Error(err, "Failed to list PersistentVolumes")
		return volumeReplicaRetryAfter
	}
	if len(names) == 0 {
		return 0
	}
	volumes, err := r.longhornVolumes(ctx)
	if err != nil {
		log.Error(err, "Failed to list Longhorn volumes")
		return volumeReplicaRetryAfter
	}

	failed, more := r.raiseVolumesTo(ctx, names, volumes, desired)

	switch {
	case more:
		// A refused volume is retried on every pass anyway, so it must not set the pace
		// for healthy volumes still waiting: at the retry interval they would take ten
		// times as long.
		return volumeReplicaPace
	case failed:
		return volumeReplicaRetryAfter
	default:
		return 0
	}
}

// raiseVolumesTo raises the named volumes that are below desired, at most volumeReplicaBatchSize of
// them. failed says Longhorn refused at least one, and more that volumes are still waiting behind the
// batch, which is what sets the pace of the next pass.
func (r *GeassClusterReconciler) raiseVolumesTo(ctx context.Context, names []string, volumes map[string]*unstructured.Unstructured, desired int64) (failed, more bool) {
	log := logf.FromContext(ctx)
	raised, failures := 0, 0
	for _, name := range names {
		volume, ok := volumes[name]
		if !ok {
			continue
		}
		have, ok := volumeReplicaCount(volume)
		if !ok || have >= desired {
			continue
		}
		if raised == volumeReplicaBatchSize {
			more = true
			break
		}
		// A refusal does not count toward the batch. If it did, a volume Longhorn keeps
		// refusing would sort first on every pass and starve the ones behind it.
		if err := r.raiseVolumeReplicas(ctx, volume, desired); err != nil {
			failed = true
			failures++
			log.Error(err, "Failed to raise Longhorn volume replica count", "volume", name, "replicas", desired)
			if failures == volumeReplicaMaxConsecutiveFailures {
				break
			}
			continue
		}
		failures = 0
		raised++
		log.Info("Raised Longhorn volume replica count", "volume", name, "from", have, "to", desired)
	}

	return failed, more
}

// replicatedVolumeNames are the Longhorn volumes behind bound claims of the
// replicated class, in name order. The class is read from the PersistentVolume
// because that is the only place it survives: a Longhorn Volume does not record
// which class it came from, and the class object itself is deleted and recreated
// whenever the node count changes, while each PV keeps the class name it was
// provisioned with.
func (r *GeassClusterReconciler) replicatedVolumeNames(ctx context.Context) ([]string, error) {
	var pvs corev1.PersistentVolumeList
	if err := r.List(ctx, &pvs); err != nil {
		return nil, err
	}
	var names []string
	for i := range pvs.Items {
		if name, ok := replicatedLonghornVolume(&pvs.Items[i]); ok {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names, nil
}

// replicatedLonghornVolume reports the Longhorn volume name behind a PV, if the PV
// is a live claim of the replicated class on Longhorn. The CSI volume handle is the
// Longhorn volume's name.
func replicatedLonghornVolume(pv *corev1.PersistentVolume) (string, bool) {
	csi := pv.Spec.CSI
	if pv.Spec.StorageClassName != platform.StorageClassReplicated ||
		csi == nil || csi.Driver != platform.LonghornProvisioner || csi.VolumeHandle == "" {
		return "", false
	}
	// A volume that is not bound, or is being deleted, is on its way out; growing it
	// would only start a rebuild nobody needs.
	if pv.Status.Phase != corev1.VolumeBound || !pv.DeletionTimestamp.IsZero() {
		return "", false
	}
	return csi.VolumeHandle, true
}

// replicatedVolumeBound lets through the PersistentVolume events that can mean a
// volume has turned up for the replica pass: one created already eligible, or one
// that has just become so as its claim bound. Everything else a PV does
// (annotations, status churn, deletion) cannot, and would only run the whole
// cluster reconcile for nothing.
func replicatedVolumeBound() predicate.Predicate {
	eligible := func(obj client.Object) bool {
		pv, ok := obj.(*corev1.PersistentVolume)
		if !ok {
			return false
		}
		_, ok = replicatedLonghornVolume(pv)
		return ok
	}
	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return eligible(e.Object) },
		UpdateFunc:  func(e event.UpdateEvent) bool { return eligible(e.ObjectNew) && !eligible(e.ObjectOld) },
		DeleteFunc:  func(event.DeleteEvent) bool { return false },
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
}

// longhornVolumes lists Longhorn's volumes with one call, by name, rather than
// fetching each one.
func (r *GeassClusterReconciler) longhornVolumes(ctx context.Context) (map[string]*unstructured.Unstructured, error) {
	var list unstructured.UnstructuredList
	list.SetGroupVersionKind(longhornVolumeListKind)
	if err := r.List(ctx, &list, client.InNamespace(platform.LonghornTargetNS)); err != nil {
		return nil, err
	}
	volumes := make(map[string]*unstructured.Unstructured, len(list.Items))
	for i := range list.Items {
		volumes[list.Items[i].GetName()] = &list.Items[i]
	}
	return volumes, nil
}

// volumeReplicaCount reads spec.numberOfReplicas. A volume without a readable count
// is left alone rather than guessed at.
func volumeReplicaCount(volume *unstructured.Unstructured) (int64, bool) {
	count, found, err := unstructured.NestedInt64(volume.Object, "spec", "numberOfReplicas")
	return count, found && err == nil
}

// raiseVolumeReplicas sets spec.numberOfReplicas with a merge patch of that one
// field. Longhorn then rebuilds replicas onto the new nodes. A patch, not an
// update, so that fields a newer Longhorn adds to a volume are never written back
// from a stale copy. The patch carries the resourceVersion the volume was listed
// at, so one that has changed since (an admin raising it further, say) is retried
// on a later pass instead of being overwritten with a lower count.
func (r *GeassClusterReconciler) raiseVolumeReplicas(ctx context.Context, volume *unstructured.Unstructured, replicas int64) error {
	volume.SetGroupVersionKind(longhornVolumeKind)
	base := volume.DeepCopy()
	if err := unstructured.SetNestedField(volume.Object, replicas, "spec", "numberOfReplicas"); err != nil {
		return err
	}
	return r.Patch(ctx, volume, client.MergeFromWithOptions(base, client.MergeFromWithOptimisticLock{}))
}

// shortestRequeue combines two requests to run again, where zero means "no request".
func shortestRequeue(a, b time.Duration) time.Duration {
	switch {
	case a == 0:
		return b
	case b == 0:
		return a
	default:
		return min(a, b)
	}
}
