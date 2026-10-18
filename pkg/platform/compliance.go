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
//
// Some pods are made by operators Geass runs, such as a database operator's backup Jobs, and the
// operator's own resource has no field for a node selector on them. A second, mutating policy gives
// such a pod the selector and toleration it lacks, so it lands on the tagged nodes instead of being
// refused. It only ever narrows where a pod may go, and the policy that refuses stays as the check.

const (
	// AnnotationRequiredTags is on a project's namespaces and lists the node tags its pods must run
	// on, comma-separated and sorted. The admission policy reads it, so nothing else needs the project.
	AnnotationRequiredTags = "geass.dev/required-tags"

	ComplianceAdmissionPolicyName  = "geass-required-node-tags"
	ComplianceAdmissionBindingName = "geass-required-node-tags"

	// ComplianceMutationPolicyName and ComplianceMutationBindingName are the policy that adds what a pod lacks.
	// It needs Kubernetes 1.36, where mutating admission policies are stable; on an older cluster it is not
	// installed and the pods of such operators are refused by the policy above, which says why.
	ComplianceMutationPolicyName  = "geass-required-node-tags-defaults"
	ComplianceMutationBindingName = "geass-required-node-tags-defaults"

	// AnnotationAdmissionVersion records which version of the policy is installed, so the operator
	// writes it again only when the policy changes and not on every reconcile.
	AnnotationAdmissionVersion = "geass.dev/admission-version"
	// ComplianceAdmissionVersion changes whenever the policy or its binding does.
	ComplianceAdmissionVersion = "2"
)

// requiredTagsExpression is the tags the pod's namespace requires, as a list, read from its annotation.
const requiredTagsExpression = `has(namespaceObject.metadata.annotations) && '` + AnnotationRequiredTags + `' in namespaceObject.metadata.annotations ? ` +
	`namespaceObject.metadata.annotations['` + AnnotationRequiredTags + `'].split(',').filter(t, t != '') : []`

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
			Variables: []admissionv1.Variable{{Name: "required", Expression: requiredTagsExpression}},
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

// The variables of the mutating policy, which the expression that builds the patch is made from. They are
// kept apart so each can be run on its own in tests.
const (
	// missingSelectorsExpression is the required tags whose node selector the pod does not have.
	missingSelectorsExpression = `variables.required.filter(t, !(has(object.spec.nodeSelector) && ('` + TagKeyPrefix + `' + t) in object.spec.nodeSelector && ` +
		`object.spec.nodeSelector['` + TagKeyPrefix + `' + t] == 'true'))`
	// missingTolerationsExpression is the required tags whose taint the pod does not already tolerate. The taint is
	// NoSchedule, with the value "compliance", so a toleration that names the key counts only if it covers that effect
	// (none named is all of them) and either matches any value (Exists) or names the value.
	missingTolerationsExpression = `variables.required.filter(t, !(has(object.spec.tolerations) && ` +
		`object.spec.tolerations.exists(x, has(x.key) && x.key == '` + TagKeyPrefix + `' + t && ` +
		`(!has(x.effect) || x.effect == '' || x.effect == 'NoSchedule') && ` +
		`((has(x.operator) && x.operator == 'Exists') || (has(x.value) && x.value == '` + ComplianceTaintValue + `')))))`
)

// complianceMutationExpression is the JSON patch. A node selector or a list of tolerations that the pod has not got
// is added first, because a JSON patch cannot add to what is not there, and the pod's own entries are kept. The "/"
// of a key is written "~1" in the path of a patch.
var complianceMutationExpression = `(variables.missingSelectors.size() > 0 && !has(object.spec.nodeSelector) ? ` +
	`[JSONPatch{op: "add", path: "/spec/nodeSelector", value: {}}] : []) + ` +
	`variables.missingSelectors.map(t, JSONPatch{op: "add", path: "/spec/nodeSelector/` + strings.ReplaceAll(TagKeyPrefix, "/", "~1") + `" + t, value: "true"}) + ` +
	`(variables.missingTolerations.size() > 0 && !has(object.spec.tolerations) ? ` +
	`[JSONPatch{op: "add", path: "/spec/tolerations", value: []}] : []) + ` +
	`variables.missingTolerations.map(t, JSONPatch{op: "add", path: "/spec/tolerations/-", ` +
	`value: Object.spec.tolerations{key: '` + TagKeyPrefix + `' + t, operator: "Exists", effect: "NoSchedule"}})`

// ComplianceMutationPolicy gives a new pod in a namespace that requires node tags the node selector and
// toleration for each of them, when it has not got them. It does not fail a pod it cannot handle: the
// policy that refuses pods is the check, and this one only makes more of them pass it.
func ComplianceMutationPolicy() *admissionv1.MutatingAdmissionPolicy {
	return &admissionv1.MutatingAdmissionPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:        ComplianceMutationPolicyName,
			Labels:      map[string]string{LabelManagedBy: ManagedByValue},
			Annotations: map[string]string{AnnotationAdmissionVersion: ComplianceAdmissionVersion},
		},
		Spec: admissionv1.MutatingAdmissionPolicySpec{
			FailurePolicy:      ptr.To(admissionv1.Ignore),
			ReinvocationPolicy: admissionv1.IfNeededReinvocationPolicy,
			MatchConstraints: &admissionv1.MatchResources{
				ResourceRules: []admissionv1.NamedRuleWithOperations{{
					RuleWithOperations: admissionv1.RuleWithOperations{
						Operations: []admissionv1.OperationType{admissionv1.Create},
						Rule:       admissionv1.Rule{APIGroups: []string{""}, APIVersions: []string{"v1"}, Resources: []string{"pods"}},
					},
				}},
			},
			Variables: []admissionv1.Variable{
				{Name: "required", Expression: requiredTagsExpression},
				{Name: "missingSelectors", Expression: missingSelectorsExpression},
				{Name: "missingTolerations", Expression: missingTolerationsExpression},
			},
			Mutations: []admissionv1.Mutation{{
				PatchType: admissionv1.PatchTypeJSONPatch,
				JSONPatch: &admissionv1.JSONPatch{Expression: complianceMutationExpression},
			}},
		},
	}
}

// ComplianceMutationBinding applies the mutating policy to the namespaces Geass manages.
func ComplianceMutationBinding() *admissionv1.MutatingAdmissionPolicyBinding {
	return &admissionv1.MutatingAdmissionPolicyBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:        ComplianceMutationBindingName,
			Labels:      map[string]string{LabelManagedBy: ManagedByValue},
			Annotations: map[string]string{AnnotationAdmissionVersion: ComplianceAdmissionVersion},
		},
		Spec: admissionv1.MutatingAdmissionPolicyBindingSpec{
			PolicyName: ComplianceMutationPolicyName,
			MatchResources: &admissionv1.MatchResources{
				NamespaceSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{LabelManagedBy: ManagedByValue},
				},
			},
		},
	}
}
