package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

const (
	conditionNodesReady   = "NodesReady"
	conditionStorageReady = "StorageReady"
	conditionAddonsReady  = "AddonsReady"
)

// GeassHAReadinessReconciler evaluates the concrete prerequisites required by
// highly available PostgreSQL provisioning.
type GeassHAReadinessReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=geass.geass.dev,resources=geasshareadinesses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geasshareadinesses/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=get;list;watch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassclusters,verbs=get;list;watch

func (r *GeassHAReadinessReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var readiness geassv1alpha1.GeassHAReadiness
	if err := r.Get(ctx, req.NamespacedName, &readiness); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	var nodes corev1.NodeList
	if err := r.List(ctx, &nodes); err != nil {
		return ctrl.Result{}, err
	}
	healthyNodes := int32(0)
	for _, node := range nodes.Items {
		if node.Spec.Unschedulable {
			continue
		}
		for _, condition := range node.Status.Conditions {
			if condition.Type == corev1.NodeReady && condition.Status == corev1.ConditionTrue {
				healthyNodes++
				break
			}
		}
	}
	var classes storagev1.StorageClassList
	if err := r.List(ctx, &classes); err != nil {
		return ctrl.Result{}, err
	}
	storageReady := len(classes.Items) > 0
	addonsReady := false
	clusterReady := false
	var clusters geassv1alpha1.GeassClusterList
	if err := r.List(ctx, &clusters, client.InNamespace(platform.SystemNamespace)); err != nil {
		return ctrl.Result{}, err
	}
	for _, cluster := range clusters.Items {
		if readiness.Spec.ClusterRef != "" && cluster.Name != readiness.Spec.ClusterRef {
			continue
		}
		clusterReady = conditionStatus(cluster.Status.Conditions, platform.ConditionReady) == string(metav1.ConditionTrue)
		addonsReady = conditionStatus(cluster.Status.Conditions, platform.ConditionAddonsReady) == string(metav1.ConditionTrue)
		break
	}
	ready := healthyNodes >= 3 && storageReady && clusterReady && addonsReady
	latest := &geassv1alpha1.GeassHAReadiness{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(&readiness), latest); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	_, err := platform.PatchStatus(ctx, r.Client, latest, func() {
		latest.Status.HealthyNodes = healthyNodes
		latest.Status.StorageClassReady = storageReady
		latest.Status.ClusterReady = clusterReady
		latest.Status.AddonsReady = addonsReady
		latest.Status.Conditions = setReadinessCondition(latest.Status.Conditions, conditionNodesReady, healthyNodes >= 3, "HealthyNodes", "At least three healthy schedulable nodes are required")
		latest.Status.Conditions = setReadinessCondition(latest.Status.Conditions, conditionStorageReady, storageReady, "StorageClass", "A usable persistent storage class is required")
		latest.Status.Conditions = setReadinessCondition(latest.Status.Conditions, "ClusterReady", clusterReady, "ClusterReady", "The referenced GeassCluster must be ready")
		latest.Status.Conditions = setReadinessCondition(latest.Status.Conditions, conditionAddonsReady, addonsReady, "PlatformAddons", "GeassCluster add-ons must be ready")
		latest.Status.Conditions = setReadinessCondition(latest.Status.Conditions, platform.ConditionReady, ready, "HAReadiness", "HA prerequisites are satisfied")
	})
	return ctrl.Result{}, err
}

func setReadinessCondition(conditions []metav1.Condition, conditionType string, ready bool, reason, message string) []metav1.Condition {
	status := metav1.ConditionFalse
	if ready {
		status = metav1.ConditionTrue
	}
	return platform.SetCondition(conditions, conditionType, status, reason, message)
}

func (r *GeassHAReadinessReconciler) SetupWithManager(mgr ctrl.Manager) error {
	// The verdict is made of the nodes, the storage classes and the cluster's readiness, so a change to any
	// of them re-evaluates it. With no watches it was only ever worked out when the resource itself changed,
	// and went stale after that.
	allReadiness := handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, _ client.Object) []ctrl.Request {
		var list geassv1alpha1.GeassHAReadinessList
		if err := r.List(ctx, &list, client.InNamespace(platform.SystemNamespace)); err != nil {
			return nil
		}
		requests := make([]ctrl.Request, 0, len(list.Items))
		for i := range list.Items {
			requests = append(requests, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&list.Items[i])})
		}
		return requests
	})
	return ctrl.NewControllerManagedBy(mgr).
		For(&geassv1alpha1.GeassHAReadiness{}, builder.WithPredicates(specOrMetadataChanged())).
		Watches(&corev1.Node{}, allReadiness, builder.WithPredicates(predicate.Or(nodeSchedulingChanged(), nodeHealthOrPlacementChanged()))).
		Watches(&storagev1.StorageClass{}, allReadiness).
		Watches(&geassv1alpha1.GeassCluster{}, allReadiness, builder.WithPredicates(readyOrSpecChanged())).
		Named("geasshareadiness").
		Complete(r)
}
