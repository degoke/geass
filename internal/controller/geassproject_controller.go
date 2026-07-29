package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// GeassProjectReconciler creates only the namespaces explicitly enabled by a project.
type GeassProjectReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassprojects,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassprojects/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassprojects/finalizers,verbs=update
// +kubebuilder:rbac:groups=geass.geass.dev,resources=geassclusters,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch;delete
func (r *GeassProjectReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var project geassv1alpha1.GeassProject
	if err := r.Get(ctx, req.NamespacedName, &project); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !project.DeletionTimestamp.IsZero() {
		environments := make(map[string]struct{}, len(project.Spec.Environments)+len(project.Status.Environments))
		for _, env := range project.Spec.Environments {
			environments[env] = struct{}{}
		}
		for _, env := range project.Status.Environments {
			environments[env.Name] = struct{}{}
		}
		for env := range environments {
			if namespace, err := platform.ProjectNamespace(project.Name, env); err == nil {
				_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace, Labels: map[string]string{platform.LabelProject: project.Name}}}))
			}
		}
		controllerutil.RemoveFinalizer(&project, platform.FinalizerProject)
		return ctrl.Result{}, r.Update(ctx, &project)
	}

	if !controllerutil.ContainsFinalizer(&project, platform.FinalizerProject) {
		controllerutil.AddFinalizer(&project, platform.FinalizerProject)
		if err := r.Update(ctx, &project); err != nil {
			return ctrl.Result{}, err
		}
	}

	var cluster geassv1alpha1.GeassCluster
	if err := r.Get(ctx, client.ObjectKey{Name: project.Spec.ClusterRef.Name, Namespace: platform.SystemNamespace}, &cluster); err != nil {
		return r.setProjectNotReady(ctx, &project, "ClusterNotFound", "Referenced GeassCluster is not available")
	}
	if conditionStatus(cluster.Status.Conditions, platform.ConditionReady) != string(metav1.ConditionTrue) {
		return r.setProjectNotReady(ctx, &project, "ClusterNotReady", "Referenced GeassCluster is not ready")
	}
	if project.Status.ClusterRef != "" && project.Status.ClusterRef != cluster.Name {
		return r.setProjectNotReady(ctx, &project, "ClusterRefImmutable", "Changing a project's clusterRef is not supported")
	}

	desired := make(map[string]string, len(project.Spec.Environments))
	for _, env := range project.Spec.Environments {
		if _, exists := desired[env]; exists {
			return r.setProjectNotReady(ctx, &project, "DuplicateEnvironment", "Project environments must be unique")
		}
		namespace, err := platform.ProjectNamespace(project.Name, env)
		if err != nil {
			return r.setProjectNotReady(ctx, &project, "InvalidEnvironment", err.Error())
		}
		desired[env] = namespace
		if err := ensureNamespace(ctx, r.Client, namespace, map[string]string{
			platform.LabelManagedBy:   platform.ManagedByValue,
			platform.LabelProject:     project.Name,
			platform.LabelEnvironment: env,
			platform.LabelCluster:     cluster.Name,
		}); err != nil {
			return ctrl.Result{}, err
		}
	}

	for _, current := range project.Status.Environments {
		if _, ok := desired[current.Name]; ok {
			continue
		}
		namespace, err := platform.ProjectNamespace(project.Name, current.Name)
		if err == nil {
			_ = client.IgnoreNotFound(r.Delete(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace, Labels: map[string]string{platform.LabelProject: project.Name}}}))
		}
	}

	latest := &geassv1alpha1.GeassProject{}
	if err := r.Get(ctx, client.ObjectKeyFromObject(&project), latest); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	latest.Status.Environments = make([]geassv1alpha1.GeassProjectEnvironmentStatus, 0, len(project.Spec.Environments))
	latest.Status.ClusterRef = cluster.Name
	for _, env := range project.Spec.Environments {
		latest.Status.Environments = append(latest.Status.Environments, geassv1alpha1.GeassProjectEnvironmentStatus{Name: env, Namespace: desired[env]})
	}
	latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, metav1.ConditionTrue, "ProjectReady", "Project environments are ready")
	return ctrl.Result{}, r.Status().Update(ctx, latest)
}

func (r *GeassProjectReconciler) setProjectNotReady(ctx context.Context, project *geassv1alpha1.GeassProject, reason, message string) (ctrl.Result, error) {
	latest := project.DeepCopy()
	if err := r.Get(ctx, client.ObjectKeyFromObject(project), latest); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	latest.Status.Conditions = platform.SetCondition(latest.Status.Conditions, platform.ConditionReady, metav1.ConditionFalse, reason, message)
	return ctrl.Result{RequeueAfter: platform.RequeueAfterDefault}, r.Status().Update(ctx, latest)
}

func (r *GeassProjectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&geassv1alpha1.GeassProject{}).
		Watches(&geassv1alpha1.GeassCluster{}, handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
			var projects geassv1alpha1.GeassProjectList
			if err := r.List(ctx, &projects, client.InNamespace(platform.SystemNamespace)); err != nil {
				return nil
			}
			requests := make([]ctrl.Request, 0)
			for _, project := range projects.Items {
				if project.Spec.ClusterRef.Name == obj.GetName() {
					requests = append(requests, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&project)})
				}
			}
			return requests
		})).
		Complete(r)
}
