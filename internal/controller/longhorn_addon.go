package controller

import (
	"context"
	"maps"
	"slices"
	"strings"

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

// longhornReservationValues let Longhorn run on nodes that are reserved for a tag. Its system components
// are told to tolerate every taint, and the chart's own manager and driver are given the same toleration:
// a storage node cannot hold replicas without a manager on it. They are added only once a node is
// reserved, because changing the chart's values restarts Longhorn's managers, which clusters that never
// reserve a node have no reason to go through. Once added they stay (see longhornChartValues).
const longhornReservationValues = `defaultSettings:
  taintToleration: ":"
longhornManager:
  tolerations:
    - operator: Exists
longhornDriver:
  tolerations:
    - operator: Exists
`

// +kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=storage.k8s.io,resources=csidrivers,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch

// reconcileLonghorn installs Longhorn and, once it is running, the storage classes
// Geass selects between. It reports whether storage is ready to use.
func (r *GeassClusterReconciler) reconcileLonghorn(ctx context.Context) (bool, error) {
	values, err := r.longhornChartValues(ctx)
	if err != nil {
		return false, err
	}
	spec := helmv1.HelmChartSpec{
		Chart:           platform.LonghornReleaseChart,
		Repo:            platform.LonghornChartRepo,
		Version:         platform.LonghornChartVersion,
		TargetNamespace: platform.LonghornTargetNS,
		CreateNamespace: true,
		ValuesContent:   values,
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
	replicas, hosted, general, err := r.longhornTarget(ctx)
	if err != nil || !hosted {
		return false, err
	}
	if err := r.ensureLonghornStorageClasses(ctx, replicas, general); err != nil {
		return false, err
	}
	return true, nil
}

// longhornReplicaTarget is how many replicas a replicated volume should have, and
// whether Longhorn can host that many yet. It is the one definition both the
// storage class and the step that raises existing volumes work from, so the two
// cannot disagree about either.
//
// One manager pod being ready only proves Longhorn is up on one node. A replica
// per node is asked for, so hosted is false until every node has a ready manager;
// otherwise the first volumes start out degraded.
func (r *GeassClusterReconciler) longhornReplicaTarget(ctx context.Context) (replicas int, hosted bool, err error) {
	replicas, hosted, _, err = r.longhornTarget(ctx)
	return replicas, hosted, err
}

// longhornTarget is longhornReplicaTarget, and also says whether general volumes are to stay off compliance
// nodes (see generalOnly), in which case those nodes are not counted: no replica of a general volume will
// be placed there.
func (r *GeassClusterReconciler) longhornTarget(ctx context.Context) (replicas int, hosted, general bool, err error) {
	general, err = r.generalOnly(ctx)
	if err != nil {
		return 0, false, false, err
	}
	if general {
		replicas, hosted, err = r.generalReplicaTarget(ctx)
		return replicas, hosted, general, err
	}
	nodes, err := r.schedulableNodeCount(ctx)
	if err != nil {
		return 0, false, false, err
	}
	managers, err := r.longhornManagersReady(ctx)
	if err != nil {
		return 0, false, false, err
	}
	return platform.LonghornReplicaCount(nodes), managers >= max(nodes, 1), false, nil
}

// generalReplicaTarget is the replica count for general volumes when compliance nodes are left out: one
// per general node, and hosted once each of them has a ready manager.
func (r *GeassClusterReconciler) generalReplicaTarget(ctx context.Context) (int, bool, error) {
	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes); err != nil {
		return 0, false, err
	}
	ready, err := r.readyLonghornManagerNodes(ctx)
	if err != nil {
		return 0, false, err
	}
	count, managed := 0, 0
	for i := range nodes.Items {
		if !nodeSchedulable(&nodes.Items[i]) || platform.IsComplianceNode(&nodes.Items[i]) {
			continue
		}
		count++
		if ready[nodes.Items[i].Name] {
			managed++
		}
	}
	return platform.LonghornReplicaCount(count), managed >= max(count, 1), nil
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

// longhornChartValues are the values Longhorn is installed with: the base values, plus the toleration of
// reserved nodes once any node is reserved. It never takes the toleration back out, so a reservation
// that comes and goes does not restart Longhorn each time.
func (r *GeassClusterReconciler) longhornChartValues(ctx context.Context) (string, error) {
	if chart, err := helmchart.Get(ctx, r.Client, platform.LonghornChartName); err == nil && strings.Contains(chart.Spec.ValuesContent, longhornReservationValues) {
		return longhornValues + longhornReservationValues, nil
	} else if err != nil && !apierrors.IsNotFound(err) {
		return "", err
	}
	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes); err != nil {
		return "", err
	}
	for i := range nodes.Items {
		for _, taint := range nodes.Items[i].Spec.Taints {
			if strings.HasPrefix(taint.Key, platform.TagKeyPrefix) {
				return longhornValues + longhornReservationValues, nil
			}
		}
	}
	return longhornValues, nil
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
		// A node reserved for a tag still hosts replicas: Longhorn tolerates Geass reservations (see
		// longhornReservationValues), and storage nodes are the ones most likely to be reserved.
		if strings.HasPrefix(taint.Key, platform.TagKeyPrefix) {
			continue
		}
		if taint.Effect == corev1.TaintEffectNoSchedule || taint.Effect == corev1.TaintEffectNoExecute {
			return false
		}
	}
	return true
}

func (r *GeassClusterReconciler) ensureLonghornStorageClasses(ctx context.Context, replicas int, general bool) error {
	for _, desired := range []*storagev1.StorageClass{
		longhornStorageClass(platform.StorageClassReplicated, withGeneralSelector(platform.LonghornStorageClassParameters(replicas), general), storagev1.VolumeBindingImmediate),
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

// geassTagLabels are a node's Geass tags, sorted. A change to them changes which nodes a project's
// storage class covers.
func geassTagLabels(node *corev1.Node) []string {
	var tags []string
	for key := range node.Labels {
		if tag, ok := strings.CutPrefix(key, platform.TagKeyPrefix); ok {
			tags = append(tags, tag)
		}
	}
	slices.Sort(tags)
	return tags
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
			return okOld && okNew && (nodeSchedulable(oldNode) != nodeSchedulable(newNode) ||
				oldNode.Labels[platform.LabelRegion] != newNode.Labels[platform.LabelRegion] ||
				!slices.Equal(geassTagLabels(oldNode), geassTagLabels(newNode)))
		},
	}
}

// objectStoreServerPlaced lets through the moment the cluster's object storage server is placed, which is when its
// volume's storage class is known and may need making. It ignores every bucket, and everything else the server does.
func objectStoreServerPlaced() predicate.Predicate {
	placed := func(obj client.Object) bool {
		store, ok := obj.(*geassv1alpha1.GeassObjectStore)
		if !ok || !platform.IsClusterObjectStoreServer(store) {
			return false
		}
		_, pinned := store.Annotations[platform.AnnotationNodePlacement]
		return pinned
	}
	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return placed(e.Object) },
		DeleteFunc:  func(event.DeleteEvent) bool { return false },
		GenericFunc: func(event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			return placed(e.ObjectNew) && e.ObjectOld.GetAnnotations()[platform.AnnotationNodePlacement] != e.ObjectNew.GetAnnotations()[platform.AnnotationNodePlacement]
		},
	}
}
