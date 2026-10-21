package controller

import (
	"context"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
	"github.com/degoke/geass/pkg/platform"
)

// +kubebuilder:rbac:groups=admissionregistration.k8s.io,resources=validatingadmissionpolicies;validatingadmissionpolicybindings;mutatingadmissionpolicies;mutatingadmissionpolicybindings,verbs=get;list;watch;create;update;patch;delete

// reconcileProjectCompliance makes a project environment's namespace say which node tags its pods must
// run on, and makes sure the admission policy that enforces it is installed. The controllers put the
// node selector on what they create; the policy catches everything else.
func (r *GeassProjectReconciler) reconcileProjectCompliance(ctx context.Context, project *geassv1alpha1.GeassProject, namespace string) error {
	tags, err := platform.ProjectTags(ctx, r.Client, project)
	if err != nil {
		return err
	}
	required := platform.RequiredTagsAnnotation(tags)
	if err := ensureNamespaceAnnotation(ctx, r.Client, namespace, platform.AnnotationRequiredTags, required); err != nil {
		return err
	}
	if required == "" {
		return nil
	}
	if err := ensureComplianceAdmission(ctx, r.Client); err != nil {
		// A cluster too old for admission policies still gets the node selectors from the controllers,
		// so this is reported and not allowed to hold the project back.
		if meta.IsNoMatchError(err) {
			logf.FromContext(ctx).Info("Admission policies are not available, so pods created outside Geass are not checked for required node tags", "project", project.Name)
			return nil
		}
		return err
	}
	return nil
}

// ensureNamespaceAnnotation sets an annotation on a namespace, or removes it when value is empty.
func ensureNamespaceAnnotation(ctx context.Context, c client.Client, name, key, value string) error {
	ns := &corev1.Namespace{}
	if err := c.Get(ctx, client.ObjectKey{Name: name}, ns); err != nil {
		return err
	}
	current, present := ns.Annotations[key]
	if (value == "" && !present) || (value != "" && current == value) {
		return nil
	}
	patch := client.MergeFrom(ns.DeepCopy())
	if value == "" {
		delete(ns.Annotations, key)
	} else {
		if ns.Annotations == nil {
			ns.Annotations = map[string]string{}
		}
		ns.Annotations[key] = value
	}
	return c.Patch(ctx, ns, patch)
}

// ensureComplianceAdmission installs the admission policy and its binding, and brings them up to date
// when a newer version of the operator changes them. It never removes them: with no tags required
// anywhere they allow everything, and removing a policy that something else still relies on is worse
// than leaving an idle one.
func ensureComplianceAdmission(ctx context.Context, c client.Client) error {
	desiredPolicy := platform.ComplianceAdmissionPolicy()
	policy := &admissionv1.ValidatingAdmissionPolicy{}
	switch err := c.Get(ctx, client.ObjectKeyFromObject(desiredPolicy), policy); {
	case apierrors.IsNotFound(err):
		if err := c.Create(ctx, desiredPolicy); client.IgnoreAlreadyExists(err) != nil {
			return err
		}
	case err != nil:
		return err
	case policy.Annotations[platform.AnnotationAdmissionVersion] != platform.ComplianceAdmissionVersion:
		policy.Labels, policy.Annotations, policy.Spec = desiredPolicy.Labels, desiredPolicy.Annotations, desiredPolicy.Spec
		if err := c.Update(ctx, policy); err != nil {
			return err
		}
	}

	desiredBinding := platform.ComplianceAdmissionBinding()
	binding := &admissionv1.ValidatingAdmissionPolicyBinding{}
	switch err := c.Get(ctx, client.ObjectKeyFromObject(desiredBinding), binding); {
	case apierrors.IsNotFound(err):
		if err := c.Create(ctx, desiredBinding); client.IgnoreAlreadyExists(err) != nil {
			return err
		}
	case err != nil:
		return err
	case binding.Annotations[platform.AnnotationAdmissionVersion] != platform.ComplianceAdmissionVersion:
		binding.Labels, binding.Annotations, binding.Spec = desiredBinding.Labels, desiredBinding.Annotations, desiredBinding.Spec
		if err := c.Update(ctx, binding); err != nil {
			return err
		}
	}
	return ensureComplianceMutation(ctx, c)
}

// ensureComplianceMutation installs the mutating policy that gives pods made by other operators the node selector
// and toleration their namespace requires. A cluster older than Kubernetes 1.36 has no such policy, and is left
// without it: the pods are then refused by the policy that checks, rather than let through unprotected.
func ensureComplianceMutation(ctx context.Context, c client.Client) error {
	desiredPolicy := platform.ComplianceMutationPolicy()
	policy := &admissionv1.MutatingAdmissionPolicy{}
	switch err := c.Get(ctx, client.ObjectKeyFromObject(desiredPolicy), policy); {
	case meta.IsNoMatchError(err):
		logf.FromContext(ctx).Info("Mutating admission policies are not available, so pods that other operators create in a project that requires node tags are refused instead of being given its node selector")
		return nil
	case apierrors.IsNotFound(err):
		if err := c.Create(ctx, desiredPolicy); client.IgnoreAlreadyExists(err) != nil {
			return err
		}
	case err != nil:
		return err
	case policy.Annotations[platform.AnnotationAdmissionVersion] != platform.ComplianceAdmissionVersion:
		policy.Labels, policy.Annotations, policy.Spec = desiredPolicy.Labels, desiredPolicy.Annotations, desiredPolicy.Spec
		if err := c.Update(ctx, policy); err != nil {
			return err
		}
	}

	desiredBinding := platform.ComplianceMutationBinding()
	binding := &admissionv1.MutatingAdmissionPolicyBinding{}
	switch err := c.Get(ctx, client.ObjectKeyFromObject(desiredBinding), binding); {
	case apierrors.IsNotFound(err):
		return client.IgnoreAlreadyExists(c.Create(ctx, desiredBinding))
	case err != nil:
		return err
	case binding.Annotations[platform.AnnotationAdmissionVersion] != platform.ComplianceAdmissionVersion:
		binding.Labels, binding.Annotations, binding.Spec = desiredBinding.Labels, desiredBinding.Annotations, desiredBinding.Spec
		return c.Update(ctx, binding)
	}
	return nil
}
