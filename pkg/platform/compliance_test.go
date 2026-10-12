package platform

import (
	"testing"

	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admissionregistration/v1"
)

func TestRequiredTagsAnnotationIsSortedAndEmptyWithoutTags(t *testing.T) {
	require.Equal(t, "hipaa,pci", RequiredTagsAnnotation([]string{"pci", " hipaa ", "pci"}))
	require.Empty(t, RequiredTagsAnnotation(nil))
}

func TestComplianceAdmissionPolicyReadsTheNamespaceAnnotation(t *testing.T) {
	policy := ComplianceAdmissionPolicy()
	require.Equal(t, ComplianceAdmissionPolicyName, policy.Name)
	require.Equal(t, ComplianceAdmissionVersion, policy.Annotations[AnnotationAdmissionVersion])
	require.Equal(t, admissionv1.Fail, *policy.Spec.FailurePolicy)

	rules := policy.Spec.MatchConstraints.ResourceRules
	require.Len(t, rules, 1)
	require.Equal(t, []string{"pods"}, rules[0].Resources)
	require.Equal(t, []admissionv1.OperationType{admissionv1.Create}, rules[0].Operations, "scheduling binds an existing pod, so only creation is checked")

	require.Len(t, policy.Spec.Variables, 1)
	require.Contains(t, policy.Spec.Variables[0].Expression, AnnotationRequiredTags)
	require.Len(t, policy.Spec.Validations, 2)
	require.Contains(t, policy.Spec.Validations[0].Expression, TagKeyPrefix)
	require.Contains(t, policy.Spec.Validations[1].Expression, "nodeName")
}

func TestComplianceAdmissionBindingDeniesInManagedNamespaces(t *testing.T) {
	binding := ComplianceAdmissionBinding()
	require.Equal(t, ComplianceAdmissionPolicyName, binding.Spec.PolicyName)
	require.Equal(t, []admissionv1.ValidationAction{admissionv1.Deny}, binding.Spec.ValidationActions)
	require.Equal(t, map[string]string{LabelManagedBy: ManagedByValue}, binding.Spec.MatchResources.NamespaceSelector.MatchLabels)
}
