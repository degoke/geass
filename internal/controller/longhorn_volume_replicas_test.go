package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// Spelled out on purpose: if the implementation's group, version or kind were
// mistyped, a fixture built from the same constants would hide it.
var testVolumeGVK = schema.GroupVersionKind{Group: "longhorn.io", Version: "v1beta2", Kind: "Volume"}

// longhornVolume is a Longhorn Volume custom resource. It carries more than the
// replica count so tests can prove that nothing else about it is rewritten.
func longhornVolume(name string, replicas int64) *unstructured.Unstructured {
	vol := &unstructured.Unstructured{Object: map[string]any{
		"spec": map[string]any{
			"numberOfReplicas": replicas,
			"size":             "1073741824",
			"dataLocality":     "disabled",
		},
		"status": map[string]any{"robustness": "healthy"},
	}}
	vol.SetGroupVersionKind(testVolumeGVK)
	vol.SetName(name)
	vol.SetNamespace(platform.LonghornTargetNS)
	return vol
}

// longhornPV is the PersistentVolume Kubernetes keeps for a Longhorn-provisioned
// claim. The Longhorn volume has the same name as the PV's CSI volume handle.
func longhornPV(name, class string) *corev1.PersistentVolume {
	return &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: corev1.PersistentVolumeSpec{
			StorageClassName: class,
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{Driver: platform.LonghornProvisioner, VolumeHandle: name},
			},
		},
		Status: corev1.PersistentVolumeStatus{Phase: corev1.VolumeBound},
	}
}

// volumePair is what the CSI driver leaves behind for one claim: the PV and the
// Longhorn volume behind it.
func volumePair(name, class string, replicas int64) []client.Object {
	return []client.Object{longhornPV(name, class), longhornVolume(name, replicas)}
}

// volumeCalls records what the replica step does to the API and can make it fail.
type volumeCalls struct {
	listed    int
	attempted int
	patched   []string
	updated   []string
	bodies    map[string]string
	// hangPatch makes every patch wait until its context ends, as a webhook that
	// never answers would.
	hangPatch bool
	failPatch func(volume string) error
	// beforePatch runs just before a patch is sent, to model someone else changing
	// the volume after it was listed.
	beforePatch func(cl client.Client, volume string)
	failList    func(ctx context.Context, list client.ObjectList) error
	failGet     func(obj client.Object) error
}

func (v *volumeCalls) funcs() interceptor.Funcs {
	return interceptor.Funcs{
		Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			if v.failGet != nil {
				if err := v.failGet(obj); err != nil {
					return err
				}
			}
			return cl.Get(ctx, key, obj, opts...)
		},
		List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			switch list.(type) {
			case *corev1.PersistentVolumeList, *unstructured.UnstructuredList:
				v.listed++
			}
			if v.failList != nil {
				if err := v.failList(ctx, list); err != nil {
					return err
				}
			}
			return cl.List(ctx, list, opts...)
		},
		Patch: func(ctx context.Context, cl client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			if _, ok := obj.(*unstructured.Unstructured); !ok || obj.GetObjectKind().GroupVersionKind().Group != "longhorn.io" {
				return cl.Patch(ctx, obj, patch, opts...)
			}
			v.attempted++
			if v.hangPatch {
				<-ctx.Done()
				return ctx.Err()
			}
			if v.failPatch != nil {
				if err := v.failPatch(obj.GetName()); err != nil {
					return err
				}
			}
			if v.beforePatch != nil {
				v.beforePatch(cl, obj.GetName())
			}
			// Read the body before sending: the patched object is overwritten by the response.
			body, err := patch.Data(obj)
			if err != nil {
				return err
			}
			if err := cl.Patch(ctx, obj, patch, opts...); err != nil {
				return err
			}
			if v.bodies == nil {
				v.bodies = map[string]string{}
			}
			v.bodies[obj.GetName()] = string(body)
			v.patched = append(v.patched, obj.GetName())
			return nil
		},
		Update: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if _, ok := obj.(*unstructured.Unstructured); ok {
				v.updated = append(v.updated, obj.GetName())
			}
			return cl.Update(ctx, obj, opts...)
		},
	}
}

func newReplicaFixture(t *testing.T, nodes int, calls *volumeCalls, withDriver bool, objects ...client.Object) (*GeassClusterReconciler, client.Client) {
	t.Helper()
	scheme := longhornTestScheme(t)
	all := make([]client.Object, 0, nodes+1+len(objects))
	if withDriver {
		all = append(all, &storagev1.CSIDriver{ObjectMeta: metav1.ObjectMeta{Name: platform.LonghornProvisioner}})
	}
	for i := 1; i <= nodes; i++ {
		all = append(all, testNode(fmt.Sprintf("n%d", i)))
	}
	all = append(all, objects...)
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(all...).WithStatusSubresource(&geassv1alpha1.GeassCluster{}).Build()
	c := interceptor.NewClient(base, calls.funcs())
	if withDriver {
		// Longhorn runs a manager on every node, and nothing may be raised until all of them are up.
		for i := 1; i <= nodes; i++ {
			addReadyManager(t, c, fmt.Sprintf("n%d", i))
		}
	}
	return &GeassClusterReconciler{Client: c, Scheme: scheme}, c
}

// replicaReconciler is a cluster with Longhorn's CSI driver registered and a ready
// manager on every node.
func replicaReconciler(t *testing.T, nodes int, calls *volumeCalls, objects ...client.Object) (*GeassClusterReconciler, client.Client) {
	t.Helper()
	return newReplicaFixture(t, nodes, calls, true, objects...)
}

func replicaTestCluster(longhorn bool) *geassv1alpha1.GeassCluster {
	off := false
	return &geassv1alpha1.GeassCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "geass", Namespace: platform.SystemNamespace},
		Spec: geassv1alpha1.GeassClusterSpec{Addons: geassv1alpha1.GeassClusterAddonsSpec{
			CertManager: geassv1alpha1.GeassClusterCertManagerAddon{Enabled: &off},
			Monitoring:  geassv1alpha1.GeassClusterMonitoringAddon{Enabled: &off},
			Longhorn:    geassv1alpha1.GeassClusterLonghornAddon{Enabled: &longhorn},
		}},
	}
}

func volumeReplicas(t *testing.T, c client.Client, name string) int64 {
	t.Helper()
	vol := &unstructured.Unstructured{}
	vol.SetGroupVersionKind(testVolumeGVK)
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: platform.LonghornTargetNS, Name: name}, vol))
	value, found, err := unstructured.NestedFieldNoCopy(vol.Object, "spec", "numberOfReplicas")
	require.NoError(t, err)
	require.True(t, found, "volume %s lost its replica count", name)
	switch n := value.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	default:
		t.Fatalf("volume %s has a replica count of unexpected type %T", name, value)
		return 0
	}
}

func many(prefix, class string, count int, replicas int64) []client.Object {
	objects := make([]client.Object, 0, count*2)
	for i := range count {
		objects = append(objects, volumePair(fmt.Sprintf("%s-%02d", prefix, i), class, replicas)...)
	}
	return objects
}

func TestRaiseVolumeReplicasWhenNodesAreAdded(t *testing.T) {
	calls := &volumeCalls{}
	r, c := replicaReconciler(t, 3, calls, slices.Concat(
		volumePair("pvc-a", platform.StorageClassReplicated, 1),
		volumePair("pvc-b", platform.StorageClassReplicated, 1),
	)...)

	requeue := r.reconcileLonghornVolumeReplicas(context.Background(), replicaTestCluster(true))

	require.Zero(t, requeue, "there is nothing left to do, so no follow-up is needed")
	require.EqualValues(t, 3, volumeReplicas(t, c, "pvc-a"))
	require.EqualValues(t, 3, volumeReplicas(t, c, "pvc-b"))
}

func TestRaiseVolumeReplicasFollowsTheNodeCount(t *testing.T) {
	for nodes, want := range map[int]int64{1: 1, 2: 2, 3: 3, 5: 3} {
		calls := &volumeCalls{}
		r, c := replicaReconciler(t, nodes, calls, volumePair("pvc-a", platform.StorageClassReplicated, 1)...)

		r.reconcileLonghornVolumeReplicas(context.Background(), replicaTestCluster(true))

		require.Equal(t, want, volumeReplicas(t, c, "pvc-a"), "%d nodes", nodes)
	}
}

func TestRaiseVolumeReplicasDoesNotLookAtVolumesOnASingleNodeCluster(t *testing.T) {
	calls := &volumeCalls{}
	r, _ := replicaReconciler(t, 1, calls, volumePair("pvc-a", platform.StorageClassReplicated, 1)...)

	requeue := r.reconcileLonghornVolumeReplicas(context.Background(), replicaTestCluster(true))

	require.Zero(t, requeue)
	// One replica is already the most a single node can hold, so nothing can be below it.
	require.Zero(t, calls.listed, "no volume can be below one replica, so there is nothing to read")
	require.Empty(t, calls.patched)
}

func TestRaiseVolumeReplicasLeavesTheSingleReplicaClassAlone(t *testing.T) {
	calls := &volumeCalls{}
	r, c := replicaReconciler(t, 3, calls, slices.Concat(
		volumePair("pvc-data", platform.StorageClassReplicated, 1),
		volumePair("pvc-ha", platform.StorageClassSingle, 1),
	)...)

	r.reconcileLonghornVolumeReplicas(context.Background(), replicaTestCluster(true))

	require.EqualValues(t, 3, volumeReplicas(t, c, "pvc-data"))
	require.EqualValues(t, 1, volumeReplicas(t, c, "pvc-ha"), "HA Postgres replicates itself, so its volumes are one copy on purpose")
	require.Equal(t, []string{"pvc-data"}, calls.patched)
}

func TestRaiseVolumeReplicasOnlyTouchesGeassVolumesOnLonghorn(t *testing.T) {
	otherDriver := longhornPV("pvc-other-driver", platform.StorageClassReplicated)
	otherDriver.Spec.CSI.Driver = "other.csi.example.com"
	localPath := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "pvc-local"},
		Spec: corev1.PersistentVolumeSpec{
			StorageClassName:       "local-path",
			PersistentVolumeSource: corev1.PersistentVolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/var/lib/rancher/k3s/storage/pvc-local"}},
		},
		Status: corev1.PersistentVolumeStatus{Phase: corev1.VolumeBound},
	}
	// Someone's own Longhorn class, and a volume with no PV at all: neither is ours.
	adminClass := longhornPV("pvc-admin", "longhorn")

	calls := &volumeCalls{}
	r, c := replicaReconciler(t, 3, calls,
		otherDriver, localPath, adminClass,
		longhornVolume("pvc-other-driver", 1), longhornVolume("pvc-admin", 1), longhornVolume("manual-volume", 1),
	)

	requeue := r.reconcileLonghornVolumeReplicas(context.Background(), replicaTestCluster(true))

	require.Zero(t, requeue)
	require.Empty(t, calls.patched)
	require.EqualValues(t, 1, volumeReplicas(t, c, "pvc-admin"))
	require.EqualValues(t, 1, volumeReplicas(t, c, "manual-volume"))
}

func TestRaiseVolumeReplicasFindsTheLonghornVolumeByHandleNotPVName(t *testing.T) {
	pv := longhornPV("pv-with-another-name", platform.StorageClassReplicated)
	pv.Spec.CSI.VolumeHandle = "pvc-the-real-name"
	calls := &volumeCalls{}
	r, c := replicaReconciler(t, 3, calls, pv, longhornVolume("pvc-the-real-name", 1))

	r.reconcileLonghornVolumeReplicas(context.Background(), replicaTestCluster(true))

	require.EqualValues(t, 3, volumeReplicas(t, c, "pvc-the-real-name"))
}

func TestRaiseVolumeReplicasNeverLowersTheCount(t *testing.T) {
	calls := &volumeCalls{}
	// The cluster shrank to two nodes, but this volume already holds three copies.
	r, c := replicaReconciler(t, 2, calls, volumePair("pvc-a", platform.StorageClassReplicated, 3)...)

	requeue := r.reconcileLonghornVolumeReplicas(context.Background(), replicaTestCluster(true))

	require.Zero(t, requeue)
	require.EqualValues(t, 3, volumeReplicas(t, c, "pvc-a"))
	require.Empty(t, calls.patched)
}

func TestRaiseVolumeReplicasWritesNothingOnceVolumesAreAtTheDesiredCount(t *testing.T) {
	calls := &volumeCalls{}
	r, _ := replicaReconciler(t, 3, calls, slices.Concat(
		volumePair("pvc-a", platform.StorageClassReplicated, 1),
		volumePair("pvc-b", platform.StorageClassReplicated, 3),
	)...)

	r.reconcileLonghornVolumeReplicas(context.Background(), replicaTestCluster(true))
	require.Equal(t, []string{"pvc-a"}, calls.patched)

	// Every later pass is a no-op: the cluster reconciles on node events, so this runs a lot.
	for range 3 {
		require.Zero(t, r.reconcileLonghornVolumeReplicas(context.Background(), replicaTestCluster(true)))
	}
	require.Equal(t, []string{"pvc-a"}, calls.patched, "an unchanged volume must never be written again")
}

func TestRaiseVolumeReplicasSendsAMergePatchOfOnlyTheReplicaCount(t *testing.T) {
	calls := &volumeCalls{}
	r, c := replicaReconciler(t, 3, calls, volumePair("pvc-a", platform.StorageClassReplicated, 1)...)

	r.reconcileLonghornVolumeReplicas(context.Background(), replicaTestCluster(true))

	// A patch of one field cannot overwrite anything a newer Longhorn adds to a volume.
	// The resourceVersion rides along as a precondition (see
	// TestRaiseVolumeReplicasDoesNotOverwriteAVolumeChangedAfterItWasListed); nothing else may.
	var patch map[string]any
	require.NoError(t, json.Unmarshal([]byte(calls.bodies["pvc-a"]), &patch))
	require.Equal(t, []string{"metadata", "spec"}, slices.Sorted(maps.Keys(patch)))
	require.Equal(t, map[string]any{"numberOfReplicas": float64(3)}, patch["spec"])
	patchMeta, _ := patch["metadata"].(map[string]any)
	require.Equal(t, []string{"resourceVersion"}, slices.Sorted(maps.Keys(patchMeta)))
	require.Empty(t, calls.updated, "a full update would send every field back to Longhorn")

	vol := &unstructured.Unstructured{}
	vol.SetGroupVersionKind(testVolumeGVK)
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: platform.LonghornTargetNS, Name: "pvc-a"}, vol))
	size, _, _ := unstructured.NestedString(vol.Object, "spec", "size")
	locality, _, _ := unstructured.NestedString(vol.Object, "spec", "dataLocality")
	robustness, _, _ := unstructured.NestedString(vol.Object, "status", "robustness")
	require.Equal(t, "1073741824", size)
	require.Equal(t, "disabled", locality)
	require.Equal(t, "healthy", robustness)
}

func TestRaiseVolumeReplicasIsPacedInBatches(t *testing.T) {
	total := volumeReplicaBatchSize*2 + 2
	calls := &volumeCalls{}
	r, c := replicaReconciler(t, 3, calls, many("pvc", platform.StorageClassReplicated, total, 1)...)
	cluster := replicaTestCluster(true)

	// Rebuilding a replica copies the whole volume, so a cluster that grows must not
	// start every volume at once.
	require.Equal(t, volumeReplicaPace, r.reconcileLonghornVolumeReplicas(context.Background(), cluster))
	require.Len(t, calls.patched, volumeReplicaBatchSize)
	require.Equal(t, volumeReplicaPace, r.reconcileLonghornVolumeReplicas(context.Background(), cluster))
	require.Len(t, calls.patched, volumeReplicaBatchSize*2)
	require.Zero(t, r.reconcileLonghornVolumeReplicas(context.Background(), cluster))
	require.Len(t, calls.patched, total)

	// The order is deterministic, so a slow rollout is not reshuffled on every pass.
	require.True(t, slices.IsSorted(calls.patched), "volumes are raised in name order: %v", calls.patched)
	for i := range total {
		require.EqualValues(t, 3, volumeReplicas(t, c, fmt.Sprintf("pvc-%02d", i)))
	}

	// And once it is all done it stays quiet.
	require.Zero(t, r.reconcileLonghornVolumeReplicas(context.Background(), cluster))
	require.Len(t, calls.patched, total)
}

func TestRaiseVolumeReplicasStopsExactlyAtTheBatchWithoutAFollowUpWhenNothingRemains(t *testing.T) {
	calls := &volumeCalls{}
	r, _ := replicaReconciler(t, 3, calls, many("pvc", platform.StorageClassReplicated, volumeReplicaBatchSize, 1)...)

	require.Zero(t, r.reconcileLonghornVolumeReplicas(context.Background(), replicaTestCluster(true)),
		"a batch that finishes the job must not ask to be run again")
	require.Len(t, calls.patched, volumeReplicaBatchSize)
}

func TestRaiseVolumeReplicasSkipsClaimsThatAreNotInUse(t *testing.T) {
	released := longhornPV("pvc-released", platform.StorageClassReplicated)
	released.Status.Phase = corev1.VolumeReleased
	available := longhornPV("pvc-available", platform.StorageClassReplicated)
	available.Status.Phase = corev1.VolumeAvailable
	now := metav1.Now()
	deleting := longhornPV("pvc-deleting", platform.StorageClassReplicated)
	deleting.DeletionTimestamp = &now
	deleting.Finalizers = []string{"kubernetes.io/pv-protection"}

	calls := &volumeCalls{}
	r, _ := replicaReconciler(t, 3, calls, released, available, deleting,
		longhornVolume("pvc-released", 1), longhornVolume("pvc-available", 1), longhornVolume("pvc-deleting", 1))

	require.Zero(t, r.reconcileLonghornVolumeReplicas(context.Background(), replicaTestCluster(true)))
	require.Empty(t, calls.patched, "growing a volume that is going away only starts a pointless rebuild")
}

func TestRaiseVolumeReplicasSkipsAVolumeLonghornDoesNotHave(t *testing.T) {
	calls := &volumeCalls{}
	r, c := replicaReconciler(t, 3, calls, slices.Concat(
		[]client.Object{longhornPV("pvc-gone", platform.StorageClassReplicated)},
		volumePair("pvc-here", platform.StorageClassReplicated, 1),
	)...)

	require.Zero(t, r.reconcileLonghornVolumeReplicas(context.Background(), replicaTestCluster(true)))
	require.EqualValues(t, 3, volumeReplicas(t, c, "pvc-here"), "one missing volume must not hold up the others")
}

func TestRaiseVolumeReplicasKeepsGoingWhenOneVolumeRefusesAndBacksOff(t *testing.T) {
	calls := &volumeCalls{failPatch: func(volume string) error {
		if volume == "pvc-bad" {
			return errors.New(`admission webhook "validator.longhorn.io" denied the request`)
		}
		return nil
	}}
	// pvc-bad sorts first. It must not use up the batch, or the volumes behind it would
	// never be reached.
	r, c := replicaReconciler(t, 3, calls, slices.Concat(
		volumePair("pvc-bad", platform.StorageClassReplicated, 1),
		many("pvc-ok", platform.StorageClassReplicated, volumeReplicaBatchSize, 1),
	)...)

	requeue := r.reconcileLonghornVolumeReplicas(context.Background(), replicaTestCluster(true))

	require.Equal(t, volumeReplicaRetryAfter, requeue, "after a refusal, try again later and slowly")
	require.EqualValues(t, 1, volumeReplicas(t, c, "pvc-bad"))
	for i := range volumeReplicaBatchSize {
		require.EqualValues(t, 3, volumeReplicas(t, c, fmt.Sprintf("pvc-ok-%02d", i)))
	}
}

func TestRaiseVolumeReplicasBacksOffWhenVolumesCannotBeRead(t *testing.T) {
	for name, failing := range map[string]func(context.Context, client.ObjectList) error{
		"persistent volumes": func(_ context.Context, list client.ObjectList) error {
			if _, ok := list.(*corev1.PersistentVolumeList); ok {
				return errors.New("apiserver unavailable")
			}
			return nil
		},
		"longhorn volumes": func(_ context.Context, list client.ObjectList) error {
			if _, ok := list.(*unstructured.UnstructuredList); ok {
				return errors.New("no matches for kind Volume in version longhorn.io/v1beta2")
			}
			return nil
		},
	} {
		calls := &volumeCalls{failList: failing}
		r, _ := replicaReconciler(t, 3, calls, volumePair("pvc-a", platform.StorageClassReplicated, 1)...)

		requeue := r.reconcileLonghornVolumeReplicas(context.Background(), replicaTestCluster(true))

		require.Equal(t, volumeReplicaRetryAfter, requeue, name)
		require.Empty(t, calls.patched, name)
	}
}

func TestRaiseVolumeReplicasDoesNothingUnlessLonghornIsOnAndInstalled(t *testing.T) {
	t.Run("switched off", func(t *testing.T) {
		calls := &volumeCalls{}
		r, _ := replicaReconciler(t, 3, calls, volumePair("pvc-a", platform.StorageClassReplicated, 1)...)

		require.Zero(t, r.reconcileLonghornVolumeReplicas(context.Background(), replicaTestCluster(false)))
		require.Zero(t, calls.listed)
		require.Empty(t, calls.patched)
	})
	t.Run("driver not registered yet", func(t *testing.T) {
		calls := &volumeCalls{}
		r, _ := newReplicaFixture(t, 3, calls, false, volumePair("pvc-a", platform.StorageClassReplicated, 1)...)

		require.Zero(t, r.reconcileLonghornVolumeReplicas(context.Background(), replicaTestCluster(true)))
		require.Zero(t, calls.listed)
		require.Empty(t, calls.patched)
	})
}

func TestRaiseVolumeReplicasWaitsUntilEveryNodeHasAReadyManager(t *testing.T) {
	calls := &volumeCalls{}
	r, c := replicaReconciler(t, 3, calls, volumePair("pvc-a", platform.StorageClassReplicated, 1)...)
	// A third node has joined, but Longhorn has not started its manager there yet.
	require.NoError(t, c.Delete(context.Background(), &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "longhorn-manager-n3", Namespace: platform.LonghornTargetNS},
	}))

	// Raising now would ask for replicas that cannot be placed, leaving the volume
	// degraded. This is the same condition that holds back the storage classes.
	require.Equal(t, volumeReplicaPace, r.reconcileLonghornVolumeReplicas(context.Background(), replicaTestCluster(true)))
	require.Empty(t, calls.patched)
	require.EqualValues(t, 1, volumeReplicas(t, c, "pvc-a"))
	require.Zero(t, calls.listed, "there is no point reading volumes that cannot be raised yet")

	addReadyManager(t, c, "n3")
	require.Zero(t, r.reconcileLonghornVolumeReplicas(context.Background(), replicaTestCluster(true)))
	require.EqualValues(t, 3, volumeReplicas(t, c, "pvc-a"))
}

func TestRaiseVolumeReplicasIgnoresManagersThatAreNotReady(t *testing.T) {
	calls := &volumeCalls{}
	r, c := replicaReconciler(t, 2, calls, volumePair("pvc-a", platform.StorageClassReplicated, 1)...)
	require.NoError(t, c.Delete(context.Background(), &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "longhorn-manager-n2", Namespace: platform.LonghornTargetNS},
	}))
	// Present but not ready, as a manager is while it starts or crash-loops.
	require.NoError(t, c.Create(context.Background(), &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: "longhorn-manager-n2", Namespace: platform.LonghornTargetNS,
			Labels: map[string]string{"app": "longhorn-manager"},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}))

	require.Equal(t, volumeReplicaPace, r.reconcileLonghornVolumeReplicas(context.Background(), replicaTestCluster(true)))
	require.Empty(t, calls.patched)
}

// refusing makes Longhorn's admission webhook deny patches to the named volumes.
func refusing(names ...string) func(string) error {
	return func(volume string) error {
		if slices.Contains(names, volume) {
			return errors.New(`admission webhook "validator.longhorn.io" denied the request`)
		}
		return nil
	}
}

func shortenPassTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	previous := volumeReplicaPassTimeout
	volumeReplicaPassTimeout = d
	t.Cleanup(func() { volumeReplicaPassTimeout = previous })
}

// passWithin runs the pass and fails the test, instead of hanging it, if the pass
// does not come back in time.
func passWithin(t *testing.T, limit time.Duration, r *GeassClusterReconciler) time.Duration {
	t.Helper()
	done := make(chan time.Duration, 1)
	go func() { done <- r.reconcileLonghornVolumeReplicas(context.Background(), replicaTestCluster(true)) }()
	select {
	case requeue := <-done:
		return requeue
	case <-time.After(limit):
		t.Fatalf("the pass was still running after %s", limit)
		return 0
	}
}

func TestRaiseVolumeReplicasKeepsTheBatchPaceWhileMoreVolumesWaitBehindARefusal(t *testing.T) {
	calls := &volumeCalls{failPatch: refusing("pvc-bad")}
	r, c := replicaReconciler(t, 3, calls, slices.Concat(
		volumePair("pvc-bad", platform.StorageClassReplicated, 1),
		many("pvc-ok", platform.StorageClassReplicated, volumeReplicaBatchSize+1, 1),
	)...)
	cluster := replicaTestCluster(true)

	// pvc-bad is retried on every pass anyway, so it must not set the pace for the
	// healthy volumes still waiting: at the retry interval they would take ten times as long.
	require.Equal(t, volumeReplicaPace, r.reconcileLonghornVolumeReplicas(context.Background(), cluster))
	require.Len(t, calls.patched, volumeReplicaBatchSize)

	// With only the refused volume left to try, slow down.
	require.Equal(t, volumeReplicaRetryAfter, r.reconcileLonghornVolumeReplicas(context.Background(), cluster))
	require.Len(t, calls.patched, volumeReplicaBatchSize+1)
	require.EqualValues(t, 1, volumeReplicas(t, c, "pvc-bad"))
}

func TestRaiseVolumeReplicasStopsAfterRepeatedRefusals(t *testing.T) {
	calls := &volumeCalls{failPatch: func(string) error { return errors.New("webhook unavailable") }}
	r, c := replicaReconciler(t, 3, calls, many("pvc", platform.StorageClassReplicated, 20, 1)...)

	requeue := r.reconcileLonghornVolumeReplicas(context.Background(), replicaTestCluster(true))

	// If every volume is refused, Longhorn is the problem. Trying all twenty only adds load.
	require.Equal(t, volumeReplicaRetryAfter, requeue)
	require.Equal(t, volumeReplicaMaxConsecutiveFailures, calls.attempted)
	require.EqualValues(t, 1, volumeReplicas(t, c, "pvc-00"))
}

func TestRaiseVolumeReplicasCountsOnlyUnbrokenRunsOfRefusals(t *testing.T) {
	// Six refusals in all, more than the limit, but never more than two in a row.
	refused := []bool{true, true, false, true, false, true, false, true, false, true, false}
	objects := make([]client.Object, 0, len(refused)*2)
	for i, bad := range refused {
		kind := "ok"
		if bad {
			kind = "bad"
		}
		objects = append(objects, volumePair(fmt.Sprintf("pvc-%02d-%s", i, kind), platform.StorageClassReplicated, 1)...)
	}
	calls := &volumeCalls{failPatch: func(volume string) error {
		if strings.HasSuffix(volume, "-bad") {
			return errors.New("refused")
		}
		return nil
	}}
	r, _ := replicaReconciler(t, 3, calls, objects...)

	r.reconcileLonghornVolumeReplicas(context.Background(), replicaTestCluster(true))

	// A success resets the count, so healthy volumes behind a scattering of refused
	// ones are still reached.
	require.Len(t, calls.patched, volumeReplicaBatchSize)
}

func TestRaiseVolumeReplicasGivesUpWhenLonghornHangs(t *testing.T) {
	shortenPassTimeout(t, 50*time.Millisecond)
	calls := &volumeCalls{hangPatch: true}
	r, c := replicaReconciler(t, 3, calls, many("pvc", platform.StorageClassReplicated, 20, 1)...)

	// The cluster reconciler has one worker. A webhook that never answers must cost it
	// at most the deadline, not a timeout per volume.
	requeue := passWithin(t, 5*time.Second, r)

	require.Equal(t, volumeReplicaRetryAfter, requeue)
	require.LessOrEqual(t, calls.attempted, volumeReplicaMaxConsecutiveFailures)
	require.EqualValues(t, 1, volumeReplicas(t, c, "pvc-00"))
}

func TestRaiseVolumeReplicasGivesUpWhenPersistentVolumesCannotBeRead(t *testing.T) {
	shortenPassTimeout(t, 50*time.Millisecond)
	calls := &volumeCalls{failList: func(ctx context.Context, list client.ObjectList) error {
		if _, ok := list.(*corev1.PersistentVolumeList); ok {
			// What a cached list does when the operator's role cannot list or watch
			// PersistentVolumes: it waits for an informer that never syncs.
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}}
	r, _ := replicaReconciler(t, 3, calls, volumePair("pvc-a", platform.StorageClassReplicated, 1)...)

	require.Equal(t, volumeReplicaRetryAfter, passWithin(t, 5*time.Second, r))
	require.Empty(t, calls.patched)
}

func TestRaiseVolumeReplicasDoesNotOverwriteAVolumeChangedAfterItWasListed(t *testing.T) {
	calls := &volumeCalls{beforePatch: func(cl client.Client, volume string) {
		// An admin raises the volume to five replicas after the pass listed it at one.
		vol := &unstructured.Unstructured{}
		vol.SetGroupVersionKind(testVolumeGVK)
		require.NoError(t, cl.Get(context.Background(), client.ObjectKey{Namespace: platform.LonghornTargetNS, Name: volume}, vol))
		require.NoError(t, unstructured.SetNestedField(vol.Object, int64(5), "spec", "numberOfReplicas"))
		require.NoError(t, cl.Update(context.Background(), vol))
	}}
	r, c := replicaReconciler(t, 3, calls, volumePair("pvc-a", platform.StorageClassReplicated, 1)...)

	requeue := r.reconcileLonghornVolumeReplicas(context.Background(), replicaTestCluster(true))

	// The count only ever goes up. Writing three over five would delete two replicas
	// somebody asked for, so a volume that moved is retried, not overwritten.
	require.EqualValues(t, 5, volumeReplicas(t, c, "pvc-a"))
	require.Empty(t, calls.patched)
	require.Equal(t, volumeReplicaRetryAfter, requeue)
}

func TestRaiseVolumeReplicasSkipsAClusterThatIsBeingDeleted(t *testing.T) {
	calls := &volumeCalls{}
	r, c := replicaReconciler(t, 3, calls, volumePair("pvc-a", platform.StorageClassReplicated, 1)...)
	cluster := replicaTestCluster(true)
	now := metav1.Now()
	cluster.DeletionTimestamp = &now

	// Reconciles are let through during deletion so finalizers can run. Starting
	// rebuilds is not part of that.
	require.Zero(t, r.reconcileLonghornVolumeReplicas(context.Background(), cluster))
	require.Zero(t, calls.listed)
	require.Empty(t, calls.patched)
	require.EqualValues(t, 1, volumeReplicas(t, c, "pvc-a"))
}

func TestRaiseVolumeReplicasLeavesVolumesWithoutAReadableReplicaCountAlone(t *testing.T) {
	asText := longhornVolume("pvc-text", 1)
	require.NoError(t, unstructured.SetNestedField(asText.Object, "1", "spec", "numberOfReplicas"))
	missing := longhornVolume("pvc-missing", 1)
	unstructured.RemoveNestedField(missing.Object, "spec", "numberOfReplicas")

	calls := &volumeCalls{}
	r, c := replicaReconciler(t, 3, calls, slices.Concat(
		[]client.Object{longhornPV("pvc-text", platform.StorageClassReplicated), asText},
		[]client.Object{longhornPV("pvc-missing", platform.StorageClassReplicated), missing},
		volumePair("pvc-ok", platform.StorageClassReplicated, 1),
	)...)

	require.Zero(t, r.reconcileLonghornVolumeReplicas(context.Background(), replicaTestCluster(true)))

	// An unreadable count is not zero. Guessing would patch volumes that should be left alone.
	require.Equal(t, []string{"pvc-ok"}, calls.patched)
	got := &unstructured.Unstructured{}
	got.SetGroupVersionKind(testVolumeGVK)
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: platform.LonghornTargetNS, Name: "pvc-text"}, got))
	text, _, _ := unstructured.NestedString(got.Object, "spec", "numberOfReplicas")
	require.Equal(t, "1", text)
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Namespace: platform.LonghornTargetNS, Name: "pvc-missing"}, got))
	_, found, _ := unstructured.NestedFieldNoCopy(got.Object, "spec", "numberOfReplicas")
	require.False(t, found)
}

func TestRaiseVolumeReplicasBacksOffWhenLonghornStateCannotBeRead(t *testing.T) {
	failList := func(kind client.ObjectList) func(context.Context, client.ObjectList) error {
		return func(_ context.Context, list client.ObjectList) error {
			if fmt.Sprintf("%T", list) == fmt.Sprintf("%T", kind) {
				return errors.New("apiserver unavailable")
			}
			return nil
		}
	}
	for name, calls := range map[string]*volumeCalls{
		"csi driver": {failGet: func(obj client.Object) error {
			if _, ok := obj.(*storagev1.CSIDriver); ok {
				return errors.New("apiserver unavailable")
			}
			return nil
		}},
		"nodes":             {failList: failList(&corev1.NodeList{})},
		"longhorn managers": {failList: failList(&corev1.PodList{})},
	} {
		r, _ := replicaReconciler(t, 3, calls, volumePair("pvc-a", platform.StorageClassReplicated, 1)...)

		require.Equal(t, volumeReplicaRetryAfter, r.reconcileLonghornVolumeReplicas(context.Background(), replicaTestCluster(true)), name)
		require.Zero(t, calls.listed, name)
		require.Empty(t, calls.patched, name)
	}
}

func TestLonghornReplicaTarget(t *testing.T) {
	for name, tc := range map[string]struct {
		nodes, managers int
		replicas        int
		hosted          bool
	}{
		"one node":                     {nodes: 1, managers: 1, replicas: 1, hosted: true},
		"two nodes, both managers up":  {nodes: 2, managers: 2, replicas: 2, hosted: true},
		"three nodes, one manager yet": {nodes: 3, managers: 1, replicas: 3, hosted: false},
		"five nodes still means three": {nodes: 5, managers: 5, replicas: 3, hosted: true},
		"no manager at all":            {nodes: 1, managers: 0, replicas: 1, hosted: false},
	} {
		calls := &volumeCalls{}
		r, c := newReplicaFixture(t, tc.nodes, calls, false)
		for i := 1; i <= tc.managers; i++ {
			addReadyManager(t, c, fmt.Sprintf("n%d", i))
		}

		replicas, hosted, err := r.longhornReplicaTarget(context.Background())

		require.NoError(t, err, name)
		require.Equal(t, tc.replicas, replicas, name)
		require.Equal(t, tc.hosted, hosted, name)
	}
}

func TestReplicatedVolumeWatchFiresOnlyWhenAVolumeBecomesEligible(t *testing.T) {
	pred := replicatedVolumeBound()
	bound := longhornPV("pvc-a", platform.StorageClassReplicated)
	pending := bound.DeepCopy()
	pending.Status.Phase = corev1.VolumePending
	single := longhornPV("pvc-ha", platform.StorageClassSingle)
	localPath := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "pvc-local"},
		Spec:       corev1.PersistentVolumeSpec{StorageClassName: "local-path"},
		Status:     corev1.PersistentVolumeStatus{Phase: corev1.VolumeBound},
	}

	require.True(t, pred.Create(event.CreateEvent{Object: bound}))
	require.False(t, pred.Create(event.CreateEvent{Object: single}), "geass-single volumes are never raised")
	require.False(t, pred.Create(event.CreateEvent{Object: localPath}))
	require.False(t, pred.Create(event.CreateEvent{Object: pending}), "a claim that is not bound yet cannot be raised")

	// Becoming Bound is the moment a freshly provisioned volume turns up.
	require.True(t, pred.Update(event.UpdateEvent{ObjectOld: pending, ObjectNew: bound}))
	// Later churn on a volume that was already eligible is noise.
	touched := bound.DeepCopy()
	touched.Annotations = map[string]string{"example.com/touched": "yes"}
	require.False(t, pred.Update(event.UpdateEvent{ObjectOld: bound, ObjectNew: touched}))
	require.False(t, pred.Delete(event.DeleteEvent{Object: bound}))
	require.False(t, pred.Generic(event.GenericEvent{Object: bound}))
}

func TestShortestRequeue(t *testing.T) {
	for name, tc := range map[string]struct{ a, b, want time.Duration }{
		"neither wants one":     {0, 0, 0},
		"only the first":        {30 * time.Second, 0, 30 * time.Second},
		"only the second":       {0, 5 * time.Minute, 5 * time.Minute},
		"the sooner one wins":   {5 * time.Minute, 30 * time.Second, 30 * time.Second},
		"the sooner one, again": {30 * time.Second, 5 * time.Minute, 30 * time.Second},
	} {
		require.Equal(t, tc.want, shortestRequeue(tc.a, tc.b), name)
	}
}

func reconcileOnce(t *testing.T, r *GeassClusterReconciler) ctrl.Result {
	t.Helper()
	result, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: types.NamespacedName{Name: "geass", Namespace: platform.SystemNamespace}})
	require.NoError(t, err)
	return result
}

func clusterReady(t *testing.T, c client.Client) bool {
	t.Helper()
	cluster := &geassv1alpha1.GeassCluster{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "geass", Namespace: platform.SystemNamespace}, cluster))
	return conditionIsTrue(cluster.Status.Conditions, platform.ConditionReady)
}

func TestReconcileRaisesExistingVolumesWhenTheClusterGrows(t *testing.T) {
	calls := &volumeCalls{}
	r, c := newReplicaFixture(t, 1, calls, false, slices.Concat(
		[]client.Object{replicaTestCluster(true)},
		volumePair("pvc-db", platform.StorageClassReplicated, 1),
		volumePair("pvc-ha", platform.StorageClassSingle, 1),
	)...)

	// Longhorn installs; the cluster has one node, so one copy is all there can be.
	reconcileOnce(t, r)
	markLonghornInstalled(t, c)
	result := reconcileOnce(t, r)
	require.Zero(t, result.RequeueAfter)
	require.True(t, clusterReady(t, c))
	require.Empty(t, calls.patched)
	require.EqualValues(t, 1, volumeReplicas(t, c, "pvc-db"))

	// Two nodes join. Longhorn needs a moment to start a manager on each, and until
	// it has, neither the class nor the existing volumes may move on: replicas
	// asked for on a node with no manager cannot be placed.
	require.NoError(t, c.Create(context.Background(), testNode("n2")))
	require.NoError(t, c.Create(context.Background(), testNode("n3")))
	result = reconcileOnce(t, r)
	require.Equal(t, platform.RequeueAfterDefault, result.RequeueAfter)
	require.False(t, clusterReady(t, c))
	require.Empty(t, calls.patched, "volumes must not be raised before every node can host a replica")
	require.EqualValues(t, 1, volumeReplicas(t, c, "pvc-db"))

	// The managers come up. New volumes get three replicas from the recreated class;
	// this is the existing volume catching up.
	addReadyManager(t, c, "n2")
	addReadyManager(t, c, "n3")
	result = reconcileOnce(t, r)

	require.Zero(t, result.RequeueAfter)
	require.True(t, clusterReady(t, c))
	require.Equal(t, "3", getStorageClass(t, c, platform.StorageClassReplicated).Parameters["numberOfReplicas"])
	require.EqualValues(t, 3, volumeReplicas(t, c, "pvc-db"))
	require.EqualValues(t, 1, volumeReplicas(t, c, "pvc-ha"))
}

func TestReconcileComesBackWhileVolumesAreStillBeingRaised(t *testing.T) {
	calls := &volumeCalls{}
	r, c := newReplicaFixture(t, 3, calls, false, slices.Concat(
		[]client.Object{replicaTestCluster(true)},
		many("pvc", platform.StorageClassReplicated, volumeReplicaBatchSize+1, 1),
	)...)
	reconcileOnce(t, r)
	markLonghornInstalled(t, c)

	result := reconcileOnce(t, r)

	// A ready cluster normally asks for no follow-up, so without this the rest of the
	// volumes would wait for the next node event.
	require.Equal(t, volumeReplicaPace, result.RequeueAfter)
	require.True(t, clusterReady(t, c), "rolling replicas out is not a reason to report the cluster as not ready")
	require.Len(t, calls.patched, volumeReplicaBatchSize)

	result = reconcileOnce(t, r)
	require.Zero(t, result.RequeueAfter)
	require.Len(t, calls.patched, volumeReplicaBatchSize+1)
}

func TestReconcileTreatsALonghornVolumeFailureAsBackgroundNoise(t *testing.T) {
	calls := &volumeCalls{failPatch: func(string) error { return errors.New("volume is being expanded") }}
	r, c := newReplicaFixture(t, 3, calls, false, slices.Concat(
		[]client.Object{replicaTestCluster(true)},
		volumePair("pvc-db", platform.StorageClassReplicated, 1),
	)...)
	reconcileOnce(t, r)
	markLonghornInstalled(t, c)

	// Returning this as an error would count toward the reconcile failure threshold
	// and eventually pause the whole cluster reconciler.
	for range 5 {
		result := reconcileOnce(t, r)
		require.Equal(t, volumeReplicaRetryAfter, result.RequeueAfter)
	}

	cluster := &geassv1alpha1.GeassCluster{}
	require.NoError(t, c.Get(context.Background(), client.ObjectKey{Name: "geass", Namespace: platform.SystemNamespace}, cluster))
	require.True(t, conditionIsTrue(cluster.Status.Conditions, platform.ConditionReady))
	require.Zero(t, cluster.Status.ReconcileRetry.ReconcileFailures)
	require.EqualValues(t, 1, volumeReplicas(t, c, "pvc-db"))
}

func TestReconcileKeepsTheSoonerRequeueWhenTheClusterIsNotReady(t *testing.T) {
	calls := &volumeCalls{failPatch: func(string) error { return errors.New("refused") }}
	r, _ := newReplicaFixture(t, 3, calls, true, slices.Concat(
		[]client.Object{replicaTestCluster(true)},
		volumePair("pvc-db", platform.StorageClassReplicated, 1),
	)...)

	// Longhorn's chart is not marked installed, so the cluster is still pending and
	// asks to be retried quickly. A slow volume retry must not stretch that.
	result := reconcileOnce(t, r)
	require.Equal(t, platform.RequeueAfterDefault, result.RequeueAfter)
}
