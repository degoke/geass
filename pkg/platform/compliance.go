package platform

import (
	"strings"

	admissionv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// A project can require node tags (a compliance tag such as hipaa). The operator puts the node
// selector on everything it creates, and this admission policy is the backstop for everything it does
// not: a chart installed by hand, a pod made with kubectl, or a controller that forgot. Without it
// the guarantee would only be as strong as every code path that builds a pod.

const (
	// AnnotationRequiredTags is on a project's namespaces and lists the node tags its pods must run
	// on, comma-separated and sorted. The admission policy reads it, so nothing else needs the project.
	AnnotationRequiredTags = "geass.dev/required-tags"

	ComplianceAdmissionPolicyName  = "geass-required-node-tags"
	ComplianceAdmissionBindingName = "geass-required-node-tags"

	// AnnotationAdmissionVersion records which version of the policy is installed, so the operator
	// writes it again only when the policy changes and not on every reconcile.
	AnnotationAdmissionVersion = "geass.dev/admission-version"
	// ComplianceAdmissionVersion changes whenever the policy or its binding does.
	ComplianceAdmissionVersion = "1"
)

// RequiredTagsAnnotation is the annotation value for the tags, or empty when there are none.
func RequiredTagsAnnotation(tags []string) string {
	return strings.Join(NewPlacement("", tags).Tags, ",")
}

// ComplianceAdmissionPolicy refuses a new pod in a namespace that requires node tags unless the pod
// is limited to nodes that carry all of them, and refuses a pod that names its node, which would skip
// the scheduler's checks. A namespace without the annotation is not affected.
func ComplianceAdmissionPolicy() *admissionv1.ValidatingAdmissionPolicy {
	return &admissionv1.ValidatingAdmissionPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:        ComplianceAdmissionPolicyName,
			Labels:      map[string]string{LabelManagedBy: ManagedByValue},
			Annotations: map[string]string{AnnotationAdmissionVersion: ComplianceAdmissionVersion},
		},
		Spec: admissionv1.ValidatingAdmissionPolicySpec{
			FailurePolicy: ptr.To(admissionv1.Fail),
			MatchConstraints: &admissionv1.MatchResources{
				ResourceRules: []admissionv1.NamedRuleWithOperations{{
					RuleWithOperations: admissionv1.RuleWithOperations{
						Operations: []admissionv1.OperationType{admissionv1.Create},
						Rule:       admissionv1.Rule{APIGroups: []string{""}, APIVersions: []string{"v1"}, Resources: []string{"pods"}},
					},
				}},
			},
			Variables: []admissionv1.Variable{{
				Name: "required",
				Expression: `has(namespaceObject.metadata.annotations) && '` + AnnotationRequiredTags + `' in namespaceObject.metadata.annotations ? ` +
					`namespaceObject.metadata.annotations['` + AnnotationRequiredTags + `'].split(',').filter(t, t != '') : []`,
			}},
			Validations: []admissionv1.Validation{
				{
					Expression: `variables.required.all(t, has(object.spec.nodeSelector) && ('` + TagKeyPrefix + `' + t) in object.spec.nodeSelector && object.spec.nodeSelector['` + TagKeyPrefix + `' + t] == 'true')`,
					MessageExpression: `'This project requires nodes tagged ' + variables.required.join(', ') + ', so every pod needs the node selector ` +
						TagKeyPrefix + `<tag>: "true" for each of them'`,
					Reason: ptr.To(metav1.StatusReasonForbidden),
				},
				{
					Expression: `variables.required.size() == 0 || !has(object.spec.nodeName) || object.spec.nodeName == ''`,
					Message:    "This project requires tagged nodes, so a pod cannot choose its own node",
					Reason:     ptr.To(metav1.StatusReasonForbidden),
				},
			},
		},
	}
}

// ComplianceAdmissionBinding applies the policy to the namespaces Geass manages.
func ComplianceAdmissionBinding() *admissionv1.ValidatingAdmissionPolicyBinding {
	return &admissionv1.ValidatingAdmissionPolicyBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:        ComplianceAdmissionBindingName,
			Labels:      map[string]string{LabelManagedBy: ManagedByValue},
			Annotations: map[string]string{AnnotationAdmissionVersion: ComplianceAdmissionVersion},
		},
		Spec: admissionv1.ValidatingAdmissionPolicyBindingSpec{
			PolicyName:        ComplianceAdmissionPolicyName,
			ValidationActions: []admissionv1.ValidationAction{admissionv1.Deny},
			MatchResources: &admissionv1.MatchResources{
				NamespaceSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{LabelManagedBy: ManagedByValue},
				},
			},
		},
	}
}
