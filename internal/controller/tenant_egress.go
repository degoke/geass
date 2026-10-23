package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// An organization's outbound limits (see platform.PlanEgress) are written into the egress policy of each namespace its
// pods run in: by the project controller into each environment's geass-block-cloud-metadata, and by the organization
// controller (and the build that makes the namespace) into its build namespace's geass-build-isolation. They are part of
// those policies and not objects of their own, so taking them away is writing the policies without them.

// projectEgress is what the organization of a project limits its pods' outbound traffic to. The zero value limits nothing.
type projectEgress struct {
	// plan is what the limits leave open from an environment, nil when nothing is limited.
	plan *platform.EgressPlan
	// invalid says why the limits cannot be applied as they are written. Each environment then keeps its policy as it is.
	invalid error
}

// organizationEgress reads the outbound limits of the project's organization, for its environments.
func (r *GeassProjectReconciler) organizationEgress(ctx context.Context, project *geassv1alpha1.GeassProject) (projectEgress, error) {
	org, err := platform.OrganizationOf(ctx, r.Client, project)
	if err != nil || org == nil {
		return projectEgress{}, err
	}
	limits, err := platform.TenantEgressOf(ctx, r.Client, org)
	if err != nil || limits == nil {
		return projectEgress{}, err
	}
	plan, err := platform.PlanEgress(limits)
	if err != nil {
		return projectEgress{invalid: err}, nil
	}
	return projectEgress{plan: plan.ForEnvironment(r.apiServerPort(ctx))}, nil
}

// condition is the project's EgressLimitsApplied condition, or ok false for a project with no limits, which carries none.
// Written into the policies is not in force: that takes a network plugin that enforces them, which the cluster's
// NetworkPolicyEnforced condition says, so the condition is True only when that is True too.
func (e projectEgress) condition(cluster *geassv1alpha1.GeassCluster) (status metav1.ConditionStatus, reason, message string, ok bool) {
	switch {
	case e.invalid != nil:
		return metav1.ConditionFalse, "InvalidLimits", "The organization's outbound limits cannot be applied, so each environment keeps the network policy it had: " + e.invalid.Error(), true
	case e.plan == nil:
		return "", "", "", false
	}
	written := "The organization's outbound limits are written into the network policy of every environment (" + e.plan.Summary() + ")"
	enforced := meta.FindStatusCondition(cluster.Status.Conditions, platform.ConditionNetworkPolicyEnforced)
	switch {
	case enforced == nil:
		return metav1.ConditionUnknown, "EnforcementUnknown", written + ", but the cluster has not yet been checked for enforcing network policies (its NetworkPolicyEnforced condition), so they may not be in force", true
	case enforced.Status == metav1.ConditionFalse:
		return metav1.ConditionFalse, "NotEnforced", written + ", but the cluster's network plugin does not enforce network policies (its NetworkPolicyEnforced condition is False), so nothing holds them", true
	case enforced.Status != metav1.ConditionTrue:
		return metav1.ConditionUnknown, "EnforcementUnknown", written + ", but the last check could not tell whether the cluster enforces network policies (its NetworkPolicyEnforced condition is Unknown), so they may not be in force", true
	}
	return metav1.ConditionTrue, "Applied", "The organization's outbound limits are in force in every environment: " + e.plan.Summary(), true
}

// applyOrgBuildPolicy writes the policy of an organization's build namespace, with the organization's outbound limits.
// Limits that cannot be applied leave a policy that is there as it is (the organization's projects say why); one that is
// not there yet is written without them, since the namespace must never be open. The namespace must exist.
func applyOrgBuildPolicy(ctx context.Context, c client.Client, org *geassv1alpha1.GeassOrganization) error {
	namespace := platform.OrgBuildNamespace(org.Name)
	limits, err := platform.TenantEgressOf(ctx, c, org)
	if err != nil {
		return err
	}
	plan, err := platform.PlanEgress(limits)
	if err != nil {
		exists, getErr := networkPolicyExists(ctx, c, platform.OrgBuildPolicyName, namespace)
		if getErr != nil {
			return getErr
		}
		log := logf.FromContext(ctx).WithValues("organization", org.Name, "namespace", namespace, "problem", err.Error())
		if exists {
			log.Info("Left the network policy of the build namespace as it was, because the organization's outbound limits cannot be applied")
			return nil
		}
		log.Info("Wrote the network policy of the build namespace without outbound limits, because the organization's cannot be applied")
		plan = nil
	}
	return applyNetworkPolicy(ctx, c, platform.TenantOrgBuildPolicy(namespace, org.Name, nil, plan))
}

// tenantEgressChanged lets through a platform configuration whose outbound limits changed, and its creation and deletion
// when it has them.
func tenantEgressChanged() predicate.Predicate {
	egressOf := func(obj client.Object) *geassv1alpha1.GeassTenantEgress {
		config, ok := obj.(*geassv1alpha1.GeassPlatformConfig)
		if !ok || config.Name != platform.HAReadinessName || config.Namespace != platform.SystemNamespace || config.Spec.Tenancy == nil {
			return nil
		}
		return config.Spec.Tenancy.Egress
	}
	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return egressOf(e.Object) != nil },
		DeleteFunc:  func(e event.DeleteEvent) bool { return egressOf(e.Object) != nil },
		GenericFunc: func(event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			return !equality.Semantic.DeepEqual(egressOf(e.ObjectOld), egressOf(e.ObjectNew))
		},
	}
}

// networkPolicyEnforcementChanged lets through a cluster whose NetworkPolicyEnforced condition changed its status, which
// the EgressLimitsApplied condition of its projects follows.
func networkPolicyEnforcementChanged() predicate.Predicate {
	enforcement := func(obj client.Object) string {
		cluster, ok := obj.(*geassv1alpha1.GeassCluster)
		if !ok {
			return ""
		}
		return conditionStatus(cluster.Status.Conditions, platform.ConditionNetworkPolicyEnforced)
	}
	return predicate.Funcs{
		CreateFunc:  func(event.CreateEvent) bool { return false },
		DeleteFunc:  func(event.DeleteEvent) bool { return false },
		GenericFunc: func(event.GenericEvent) bool { return false },
		UpdateFunc:  func(e event.UpdateEvent) bool { return enforcement(e.ObjectOld) != enforcement(e.ObjectNew) },
	}
}

// projectsWithOrganizations maps the platform configuration to every project that belongs to an organization, which are
// the ones its outbound limits reach.
func projectsWithOrganizations(c client.Reader) func(context.Context, client.Object) []ctrl.Request {
	return func(ctx context.Context, _ client.Object) []ctrl.Request {
		var projects geassv1alpha1.GeassProjectList
		if err := c.List(ctx, &projects, client.InNamespace(platform.SystemNamespace)); err != nil {
			return nil
		}
		var requests []ctrl.Request
		for i := range projects.Items {
			if projects.Items[i].Spec.Org != "" {
				requests = append(requests, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&projects.Items[i])})
			}
		}
		return requests
	}
}

// everyOrganization maps the platform configuration to every organization, whose build namespaces its limits reach.
func everyOrganization(c client.Reader) func(context.Context, client.Object) []ctrl.Request {
	return func(ctx context.Context, _ client.Object) []ctrl.Request {
		var orgs geassv1alpha1.GeassOrganizationList
		if err := c.List(ctx, &orgs, client.InNamespace(platform.SystemNamespace)); err != nil {
			return nil
		}
		requests := make([]ctrl.Request, 0, len(orgs.Items))
		for i := range orgs.Items {
			requests = append(requests, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(&orgs.Items[i])})
		}
		return requests
	}
}

// organizationOfBuildPolicy maps the policy of an organization's build namespace to the organization, so that one that
// is edited or deleted by hand is put back. Any other policy maps to nothing.
func organizationOfBuildPolicy(_ context.Context, obj client.Object) []ctrl.Request {
	if obj.GetName() != platform.OrgBuildPolicyName {
		return nil
	}
	org := obj.GetLabels()[platform.LabelOrgBuild]
	if org == "" || obj.GetNamespace() != platform.OrgBuildNamespace(org) {
		return nil
	}
	return []ctrl.Request{{NamespacedName: client.ObjectKey{Name: org, Namespace: platform.SystemNamespace}}}
}
