package controller

import (
	"context"
	"maps"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilptr "k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/helmchart"
	helmv1 "github.com/degoke/geass/pkg/helmchart/v1"
	"github.com/degoke/geass/pkg/platform"
)

// Geass creates its own storage classes, so the chart's is switched off, and the
// Longhorn UI is an unauthenticated surface that users are never meant to reach.
const longhornValues = `persistence:
  createStorageClass: false
longhornUI:
  replicas: 0
`

// +kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=storage.k8s.io,resources=csidrivers,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch

// reconcileLonghorn installs Longhorn and, once it is running, the storage classes
// Geass selects between. It reports whether storage is ready to use.
func (r *GeassClusterReconciler) reconcileLonghorn(ctx context.Context) (bool, error) {
	spec := helmv1.HelmChartSpec{
		Chart:           platform.LonghornReleaseChart,
		Repo:            platform.LonghornChartRepo,
		Version:         platform.LonghornChartVersion,
		TargetNamespace: platform.LonghornTargetNS,
		CreateNamespace: true,
		ValuesContent:   longhornValues,
	}
	if err := helmchart.Ensure(ctx, r.Client, platform.LonghornChartName, spec); err != nil {
		return false, err
	}
	ready, err := r.addonReady(ctx, platform.LonghornChartName, platform.LonghornTargetNS)
	if err != nil || !ready {
		return false, err
	}
	// The manager pods can be up before the CSI driver is registered. A class that
	// names a driver which is not there yet would strand every volume claim.
	installed, err := r.longhornDriverInstalled(ctx)
	if err != nil || !installed {
		return false, err
	}
	nodes, err := r.schedulableNodeCount(ctx)
	if err != nil {
		return false, err
	}
	// One manager pod being ready only proves Longhorn is up on one node. The
	// replicated class asks for a replica per node, so wait until every node can
	// host one; otherwise the first volumes start out degraded.
	managers, err := r.longhornManagersReady(ctx)
	if err != nil || managers < max(nodes, 1) {
		return false, err
	}
	if err := r.ensureLonghornStorageClasses(ctx, platform.LonghornReplicaCount(nodes)); err != nil {
		return false, err
	}
	return true, nil
}

// longhornManagersReady counts ready longhorn-manager pods, one per usable node.
func (r *GeassClusterReconciler) longhornManagersReady(ctx context.Context) (int, error) {
	var pods corev1.PodList
	if err := r.List(ctx, &pods, client.InNamespace(platform.LonghornTargetNS), client.MatchingLabels{"app": "longhorn-manager"}); err != nil {
		return 0, err
	}
	ready := 0
	for i := range pods.Items {
		if podConditionsReady(pods.Items[i]) {
			ready++
		}
	}
	return ready, nil
}

func (r *GeassClusterReconciler) longhornDriverInstalled(ctx context.Context) (bool, error) {
	err := r.Get(ctx, client.ObjectKey{Name: platform.LonghornProvisioner}, &storagev1.CSIDriver{})
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return err == nil, err
}

// schedulableNodeCount is how many nodes can run Longhorn replicas. It counts
// nodes that exist rather than nodes that are Ready, so a node that is briefly
// unreachable does not make the replica count (and the class) flap.
func (r *GeassClusterReconciler) schedulableNodeCount(ctx context.Context) (int, error) {
	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes); err != nil {
		return 0, err
	}
	count := 0
	for i := range nodes.Items {
		if nodeSchedulable(&nodes.Items[i]) {
			count++
		}
	}
	return count, nil
}

func nodeSchedulable(node *corev1.Node) bool {
	if node.Spec.Unschedulable {
		return false
	}
	for _, taint := range node.Spec.Taints {
		if taint.Effect == corev1.TaintEffectNoSchedule || taint.Effect == corev1.TaintEffectNoExecute {
			return false
		}
	}
	return true
}

func (r *GeassClusterReconciler) ensureLonghornStorageClasses(ctx context.Context, replicas int) error {
	for _, desired := range []*storagev1.StorageClass{
		longhornStorageClass(platform.StorageClassReplicated, platform.LonghornStorageClassParameters(replicas), storagev1.VolumeBindingImmediate),
		// WaitForFirstConsumer so the volume is created after its pod is scheduled:
		// strict-local keeps the replica on that pod's node.
		longhornStorageClass(platform.StorageClassSingle, platform.LonghornSingleStorageClassParameters(), storagev1.VolumeBindingWaitForFirstConsumer),
	} {
		if err := r.ensureStorageClass(ctx, desired); err != nil {
			return err
		}
	}
	return nil
}

func longhornStorageClass(name string, parameters map[string]string, binding storagev1.VolumeBindingMode) *storagev1.StorageClass {
	reclaim := corev1.PersistentVolumeReclaimDelete
	expand := platform.LonghornAllowsExpansion
	return &storagev1.StorageClass{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{platform.LabelManagedBy: platform.ManagedByValue},
		},
		Provisioner:          platform.LonghornProvisioner,
		Parameters:           parameters,
		ReclaimPolicy:        &reclaim,
		VolumeBindingMode:    &binding,
		AllowVolumeExpansion: &expand,
	}
}

// ensureStorageClass creates the class, or replaces it when it differs. A
// StorageClass's parameters are immutable, so a changed replica count can only
// be applied by deleting and recreating it. That is safe for existing volumes:
// each PersistentVolume carries its own parameters and only references the class
// by name. An unchanged class is never touched, since deleting it would briefly
// make new volume claims fail.
func (r *GeassClusterReconciler) ensureStorageClass(ctx context.Context, desired *storagev1.StorageClass) error {
	existing := &storagev1.StorageClass{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if apierrors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}
	if storageClassMatches(existing, desired) {
		return nil
	}
	if err := r.Delete(ctx, existing); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return r.Create(ctx, desired)
}

func storageClassMatches(existing, desired *storagev1.StorageClass) bool {
	return existing.Provisioner == desired.Provisioner &&
		maps.Equal(existing.Parameters, desired.Parameters) &&
		utilptr.Equal(existing.ReclaimPolicy, desired.ReclaimPolicy) &&
		utilptr.Equal(existing.VolumeBindingMode, desired.VolumeBindingMode) &&
		utilptr.Equal(existing.AllowVolumeExpansion, desired.AllowVolumeExpansion)
}

// clustersForNodeChange maps any node event to every GeassCluster, so the replica
// count follows nodes being added or removed without waiting for a periodic resync.
func (r *GeassClusterReconciler) clustersForNodeChange(ctx context.Context, _ client.Object) []reconcile.Request {
	var clusters geassv1alpha1.GeassClusterList
	if err := r.List(ctx, &clusters); err != nil {
		return nil
	}
	requests := make([]reconcile.Request, 0, len(clusters.Items))
	for i := range clusters.Items {
		requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&clusters.Items[i])})
	}
	return requests
}

// nodeSchedulingChanged lets through node creation and deletion and updates that
// change whether the node can run workloads. Everything else a node does,
// chiefly status heartbeats, is noise that would otherwise trigger a reconcile
// every few seconds.
func nodeSchedulingChanged() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc:  func(event.CreateEvent) bool { return true },
		DeleteFunc:  func(event.DeleteEvent) bool { return true },
		GenericFunc: func(event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldNode, okOld := e.ObjectOld.(*corev1.Node)
			newNode, okNew := e.ObjectNew.(*corev1.Node)
			return okOld && okNew && nodeSchedulable(oldNode) != nodeSchedulable(newNode)
		},
	}
}
