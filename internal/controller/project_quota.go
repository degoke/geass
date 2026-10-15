package controller

import (
	"context"
	"slices"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// reconcileProjectQuota puts the cap a project asked for on one of its environments, and takes it off again
// when the project stops asking. A project that never asked has none of these objects, so nothing is capped
// until someone chooses it. usage is what every environment of the project uses now (see environmentUsage), which
// a project-wide cap is divided by.
func (r *GeassProjectReconciler) reconcileProjectQuota(ctx context.Context, project *geassv1alpha1.GeassProject, namespace string, usage map[string]corev1.ResourceList) error {
	hard := platform.EnvironmentHard(namespace, project.Spec.EnvironmentQuota, project.Spec.ProjectQuota, usage)
	if len(hard) == 0 {
		if err := deleteOwnedQuotaObject(ctx, r.Client, &corev1.ResourceQuota{}, platform.ProjectQuotaName, namespace); err != nil {
			return err
		}
		return deleteOwnedQuotaObject(ctx, r.Client, &corev1.LimitRange{}, platform.ProjectDefaultsName, namespace)
	}

	desired := platform.ProjectResourceQuota(project.Name, namespace, hard)
	current := &corev1.ResourceQuota{ObjectMeta: desired.ObjectMeta}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, current, func() error {
		current.Labels = desired.Labels
		current.Spec.Hard = desired.Spec.Hard
		return nil
	}); err != nil {
		return err
	}

	defaults := platform.ProjectLimitRange(project.Name, namespace, hard)
	if defaults == nil {
		return deleteOwnedQuotaObject(ctx, r.Client, &corev1.LimitRange{}, platform.ProjectDefaultsName, namespace)
	}
	existing := &corev1.LimitRange{ObjectMeta: defaults.ObjectMeta}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, existing, func() error {
		existing.Labels = defaults.Labels
		existing.Spec = defaults.Spec
		return nil
	})
	return err
}

// environmentUsage reads what each environment of a project uses from the quota in its namespace, which
// Kubernetes keeps up to date. It returns nil for a project with no project-wide cap, which has no use for it. An
// environment with no quota yet is left out, and counts as using nothing until its quota has been counted.
func (r *GeassProjectReconciler) environmentUsage(ctx context.Context, project *geassv1alpha1.GeassProject) (map[string]corev1.ResourceList, error) {
	if project.Spec.ProjectQuota.IsZero() {
		return nil, nil
	}
	usage := make(map[string]corev1.ResourceList, len(project.Spec.Environments))
	for _, env := range project.Spec.Environments {
		namespace, err := platform.ProjectNamespace(project.Name, env)
		if err != nil {
			continue
		}
		var quota corev1.ResourceQuota
		if err := r.Get(ctx, client.ObjectKey{Name: platform.ProjectQuotaName, Namespace: namespace}, &quota); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return nil, err
		}
		usage[namespace] = quota.Status.Used
	}
	return usage, nil
}

// deleteOwnedQuotaObject removes an object the operator made, looking first so that a project that has no quota
// costs no request to the API server, and leaving alone one a person made under the same name.
func deleteOwnedQuotaObject(ctx context.Context, c client.Client, obj client.Object, name, namespace string) error {
	if err := c.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, obj); err != nil {
		return client.IgnoreNotFound(err)
	}
	if obj.GetLabels()[platform.LabelManagedBy] != platform.ManagedByValue {
		return nil
	}
	if err := c.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	return nil
}

// projectsForQuotaObject maps a quota object the operator owns back to its project, so that one that is changed
// or deleted by hand is put back.
func (r *GeassProjectReconciler) projectsForQuotaObject(_ context.Context, obj client.Object) []ctrl.Request {
	if !slices.Contains(platform.ManagedQuotaNames(), obj.GetName()) {
		return nil
	}
	project := obj.GetLabels()[platform.LabelProject]
	if project == "" {
		return nil
	}
	return []ctrl.Request{{NamespacedName: client.ObjectKey{Name: project, Namespace: platform.SystemNamespace}}}
}

// quotaObjectEdited lets through a deletion and an edit of what a quota object says. What it says is used is let
// through too, but only for a project with a project-wide cap, whose environments' caps follow it: for any other
// project a quota's usage changes with every pod that starts or stops, and none of it is of interest.
func (r *GeassProjectReconciler) quotaObjectEdited() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc:  func(event.CreateEvent) bool { return false },
		DeleteFunc:  func(event.DeleteEvent) bool { return true },
		GenericFunc: func(event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			switch before := e.ObjectOld.(type) {
			case *corev1.ResourceQuota:
				after, ok := e.ObjectNew.(*corev1.ResourceQuota)
				if !ok {
					return false
				}
				if !equality.Semantic.DeepEqual(before.Spec, after.Spec) {
					return true
				}
				return !equality.Semantic.DeepEqual(before.Status.Used, after.Status.Used) && r.projectHasWideQuota(e.ObjectNew)
			case *corev1.LimitRange:
				after, ok := e.ObjectNew.(*corev1.LimitRange)
				return ok && !equality.Semantic.DeepEqual(before.Spec, after.Spec)
			}
			return false
		},
	}
}

// projectHasWideQuota reports whether the project a quota object belongs to caps what all its environments use.
func (r *GeassProjectReconciler) projectHasWideQuota(obj client.Object) bool {
	name := obj.GetLabels()[platform.LabelProject]
	if name == "" {
		return false
	}
	var project geassv1alpha1.GeassProject
	if err := r.Get(context.Background(), client.ObjectKey{Name: name, Namespace: platform.SystemNamespace}, &project); err != nil {
		return false
	}
	return !project.Spec.ProjectQuota.IsZero()
}

// quotaProblem says when a Deployment cannot make a pod because its environment's quota is used up, which the
// Deployment reports only on a condition of its own. It returns "" for any other reason.
func quotaProblem(deploy *appsv1.Deployment) string {
	for _, cond := range deploy.Status.Conditions {
		if cond.Type != appsv1.DeploymentReplicaFailure || cond.Status != corev1.ConditionTrue {
			continue
		}
		if _, detail, found := strings.Cut(cond.Message, "exceeded quota: "); found {
			return platform.MessageQuotaUsedUp + ": " + detail
		}
	}
	return ""
}
