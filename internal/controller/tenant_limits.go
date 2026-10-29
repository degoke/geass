package controller

import (
	"context"
	"fmt"
	"strings"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// An organization's noisy-neighbour limits (see platform.ContainerDefaults) are written into each namespace its pods run
// in: by the project controller into each environment, and by the organization controller (and the build that makes it)
// into its build namespace.

// reconcileTenantQuota puts the organization's per-namespace quota on a namespace, and takes it away when it limits neither
// pods nor ephemeral storage. extra are the labels that say whose the quota is.
func reconcileTenantQuota(ctx context.Context, c client.Client, namespace string, extra map[string]string, limits *geassv1alpha1.GeassTenantLimits) error {
	desired := platform.TenantQuota(namespace, extra, limits)
	if desired == nil {
		return deleteOwnedQuotaObject(ctx, c, &corev1.ResourceQuota{}, platform.TenantQuotaName, namespace)
	}
	current := &corev1.ResourceQuota{ObjectMeta: desired.ObjectMeta}
	_, err := controllerutil.CreateOrUpdate(ctx, c, current, func() error {
		current.Labels = desired.Labels
		current.Spec.Hard = desired.Spec.Hard
		return nil
	})
	return err
}

// reconcileLimitRange writes a namespace's LimitRange, or removes the one the operator made when there is nothing to hold.
func reconcileLimitRange(ctx context.Context, c client.Client, name, namespace string, desired *corev1.LimitRange) error {
	if desired == nil {
		return deleteOwnedQuotaObject(ctx, c, &corev1.LimitRange{}, name, namespace)
	}
	current := &corev1.LimitRange{ObjectMeta: desired.ObjectMeta}
	_, err := controllerutil.CreateOrUpdate(ctx, c, current, func() error {
		current.Labels = desired.Labels
		current.Spec = desired.Spec
		return nil
	})
	return err
}

// reconcileTenantBandwidth makes a namespace say what bandwidth its pods are given, and makes sure the policy that gives it
// to them is installed. It reports false when a bandwidth is asked for and the cluster has no mutating admission policies,
// so that nothing gives the pods their limit.
func reconcileTenantBandwidth(ctx context.Context, c client.Client, namespace string, limits *geassv1alpha1.GeassTenantLimits) (bool, error) {
	ingress, egress := platform.TenantBandwidth(limits)
	if err := ensureNamespaceAnnotation(ctx, c, namespace, platform.AnnotationPodIngressBandwidth, ingress); err != nil {
		return false, err
	}
	if err := ensureNamespaceAnnotation(ctx, c, namespace, platform.AnnotationPodEgressBandwidth, egress); err != nil {
		return false, err
	}
	if ingress == "" && egress == "" {
		return true, nil
	}
	return ensureMutationPolicy(ctx, c, platform.PodBandwidthMutationPolicy(), platform.PodBandwidthMutationBinding(), platform.PodBandwidthAdmissionVersion)
}

// ensureMutationPolicy installs a mutating admission policy and its binding, and brings them up to date when a newer version
// of the operator changes them (version is on both). It reports false, and installs nothing, on a cluster that has no
// mutating admission policies (before Kubernetes 1.36). It never removes them: a policy whose namespaces ask for nothing
// changes nothing.
func ensureMutationPolicy(ctx context.Context, c client.Client, desiredPolicy *admissionv1.MutatingAdmissionPolicy, desiredBinding *admissionv1.MutatingAdmissionPolicyBinding, version string) (bool, error) {
	policy := &admissionv1.MutatingAdmissionPolicy{}
	switch err := c.Get(ctx, client.ObjectKeyFromObject(desiredPolicy), policy); {
	case meta.IsNoMatchError(err):
		return false, nil
	case apierrors.IsNotFound(err):
		if err := c.Create(ctx, desiredPolicy); client.IgnoreAlreadyExists(err) != nil {
			return false, err
		}
	case err != nil:
		return false, err
	case policy.Annotations[platform.AnnotationAdmissionVersion] != version:
		policy.Labels, policy.Annotations, policy.Spec = desiredPolicy.Labels, desiredPolicy.Annotations, desiredPolicy.Spec
		if err := c.Update(ctx, policy); err != nil {
			return false, err
		}
	}

	binding := &admissionv1.MutatingAdmissionPolicyBinding{}
	switch err := c.Get(ctx, client.ObjectKeyFromObject(desiredBinding), binding); {
	case apierrors.IsNotFound(err):
		return true, client.IgnoreAlreadyExists(c.Create(ctx, desiredBinding))
	case err != nil:
		return false, err
	case binding.Annotations[platform.AnnotationAdmissionVersion] != version:
		binding.Labels, binding.Annotations, binding.Spec = desiredBinding.Labels, desiredBinding.Annotations, desiredBinding.Spec
		return true, c.Update(ctx, binding)
	}
	return true, nil
}

// tenantLimitsState is what came of writing an organization's limits into a project's environments, for the project's
// TenantLimitsApplied condition.
type tenantLimitsState struct {
	// problems are the environments whose limits could not be written, with why.
	problems []string
	// noBandwidthPolicy is set when a bandwidth is asked for and the cluster cannot give it to pods.
	noBandwidthPolicy bool
}

// condition is the project's TenantLimitsApplied condition, or ok false when the project has no limits to hold and should
// carry no such condition.
func (s tenantLimitsState) condition(org orgQuota) (status bool, reason, message string, ok bool) {
	switch {
	case org.limitsInvalid != nil:
		return false, "InvalidLimits", "The organization's limits cannot be applied, so each environment keeps what it had: " + org.limitsInvalid.Error(), true
	case org.limits == nil:
		return false, "", "", false
	case len(s.problems) > 0:
		return false, "NotApplied", "The organization's limits could not be applied: " + joinBounded(s.problems, 3), true
	case s.noBandwidthPolicy:
		return false, "BandwidthNotApplied", "The organization's limits are applied, except its bandwidth: the cluster has no mutating admission policies (Kubernetes 1.36 or later), which is how pods are given it", true
	}
	return true, "Applied", "The organization's limits are applied to every environment", true
}

// joinBounded joins the first few of a list, and says how many more there were, so that a condition stays short.
func joinBounded(items []string, most int) string {
	if len(items) <= most {
		return strings.Join(items, "; ")
	}
	return fmt.Sprintf("%s; and %d more", strings.Join(items[:most], "; "), len(items)-most)
}

// reconcileOrgBuildLimits holds an organization's build namespace to the organization's limits, and returns them for the
// build's own containers. Limits that cannot be applied leave the namespace as it was (the organization's projects say
// why), and nil is returned. The namespace must exist.
func reconcileOrgBuildLimits(ctx context.Context, c client.Client, org *geassv1alpha1.GeassOrganization) (*geassv1alpha1.GeassTenantLimits, error) {
	limits, err := platform.TenantLimitsOf(ctx, c, org)
	if err != nil {
		return nil, err
	}
	if err := platform.ValidateTenantLimits(limits); err != nil {
		logf.FromContext(ctx).Info("Left the limits of the build namespace as they were, because the organization's limits cannot be applied", "organization", org.Name, "problem", err.Error())
		return nil, nil
	}
	namespace := platform.OrgBuildNamespace(org.Name)
	if err := reconcileTenantQuota(ctx, c, namespace, map[string]string{platform.LabelOrgBuild: org.Name}, limits); err != nil {
		return nil, err
	}
	if err := reconcileLimitRange(ctx, c, platform.OrgBuildDefaultsName, namespace, platform.OrgBuildLimitRange(namespace, org.Name, limits)); err != nil {
		return nil, err
	}
	if available, err := reconcileTenantBandwidth(ctx, c, namespace, limits); err != nil {
		return nil, err
	} else if !available {
		logf.FromContext(ctx).Info("Mutating admission policies are not available, so builds are not given the organization's bandwidth", "organization", org.Name)
	}
	return limits, nil
}

// tenantLimitsChanged lets through a platform configuration whose default limits changed, and its creation and deletion.
func tenantLimitsChanged() predicate.Predicate {
	limitsOf := func(obj client.Object) *geassv1alpha1.GeassPlatformTenantLimitsSpec {
		config, ok := obj.(*geassv1alpha1.GeassPlatformConfig)
		if !ok || config.Name != platform.HAReadinessName || config.Namespace != platform.SystemNamespace {
			return nil
		}
		return config.Spec.TenantLimits
	}
	return predicate.Funcs{
		CreateFunc:  func(e event.CreateEvent) bool { return limitsOf(e.Object) != nil },
		DeleteFunc:  func(e event.DeleteEvent) bool { return limitsOf(e.Object) != nil },
		GenericFunc: func(event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			return !equality.Semantic.DeepEqual(limitsOf(e.ObjectOld), limitsOf(e.ObjectNew))
		},
	}
}

// projectsOfOrganizations maps the platform configuration to every project that belongs to an organization, which are the
// ones its default limits reach.
func projectsOfOrganizations(c client.Reader) func(context.Context, client.Object) []ctrl.Request {
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

// allOrganizations maps the platform configuration to every organization.
func allOrganizations(c client.Reader) func(context.Context, client.Object) []ctrl.Request {
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

// organizationOfBuildLimit maps a quota object the operator made in an organization's build namespace to the organization,
// so that one that is edited or deleted by hand is put back.
func organizationOfBuildLimit(_ context.Context, obj client.Object) []ctrl.Request {
	if obj.GetName() != platform.TenantQuotaName && obj.GetName() != platform.OrgBuildDefaultsName {
		return nil
	}
	org := obj.GetLabels()[platform.LabelOrgBuild]
	if org == "" || obj.GetNamespace() != platform.OrgBuildNamespace(org) {
		return nil
	}
	return []ctrl.Request{{NamespacedName: client.ObjectKey{Name: org, Namespace: platform.SystemNamespace}}}
}

// buildLimitEdited lets through what changes what a quota object of a build namespace holds, and its deletion: not what it
// says is used, which changes with every build.
func buildLimitEdited() predicate.Predicate {
	return predicate.Funcs{
		CreateFunc:  func(event.CreateEvent) bool { return false },
		DeleteFunc:  func(event.DeleteEvent) bool { return true },
		GenericFunc: func(event.GenericEvent) bool { return false },
		UpdateFunc: func(e event.UpdateEvent) bool {
			switch before := e.ObjectOld.(type) {
			case *corev1.ResourceQuota:
				after, ok := e.ObjectNew.(*corev1.ResourceQuota)
				return ok && !equality.Semantic.DeepEqual(before.Spec, after.Spec)
			case *corev1.LimitRange:
				after, ok := e.ObjectNew.(*corev1.LimitRange)
				return ok && !equality.Semantic.DeepEqual(before.Spec, after.Spec)
			}
			return false
		},
	}
}
