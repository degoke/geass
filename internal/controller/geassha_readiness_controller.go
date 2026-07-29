package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

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
	latest := readiness.DeepCopy()
	if err := r.Get(ctx, client.ObjectKeyFromObject(&readiness), latest); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	latest.Status.HealthyNodes = healthyNodes
	latest.Status.StorageClassReady = storageReady
	latest.Status.ClusterReady = clusterReady
	latest.Status.AddonsReady = addonsReady
	latest.Status.Conditions = setReadinessCondition(latest.Status.Conditions, conditionNodesReady, healthyNodes >= 3, "HealthyNodes", "At least three healthy schedulable nodes are required")
	latest.Status.Conditions = setReadinessCondition(latest.Status.Conditions, conditionStorageReady, storageReady, "StorageClass", "A usable persistent storage class is required")
	latest.Status.Conditions = setReadinessCondition(latest.Status.Conditions, "ClusterReady", clusterReady, "ClusterReady", "The referenced GeassCluster must be ready")
	latest.Status.Conditions = setReadinessCondition(latest.Status.Conditions, conditionAddonsReady, addonsReady, "PlatformAddons", "GeassCluster add-ons must be ready")
	latest.Status.Conditions = setReadinessCondition(latest.Status.Conditions, platform.ConditionReady, ready, "HAReadiness", "HA prerequisites are satisfied")
	if err := r.Status().Update(ctx, latest); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func setReadinessCondition(conditions []metav1.Condition, conditionType string, ready bool, reason, message string) []metav1.Condition {
	status := metav1.ConditionFalse
	if ready {
		status = metav1.ConditionTrue
	}
	return platform.SetCondition(conditions, conditionType, status, reason, message)
}

func (r *GeassHAReadinessReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).For(&geassv1alpha1.GeassHAReadiness{}).Named("geasshareadiness").Complete(r)
}
