package platform

import (
	admissionv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// A pod's bandwidth is read by the CNI bandwidth plugin from two annotations of the pod, when the pod's network is set up on
// its node. Pods in a tenant namespace are made by Geass and by operators Geass runs (a database operator, the Job
// controller), so the annotations are not written into each of their templates: the namespace says what its pods are given,
// and a mutating admission policy puts it on every pod that is made there, whoever makes it. It overwrites what a pod says,
// so that nothing in the namespace can give itself more.
//
// Mutating admission policies are stable from Kubernetes 1.36. An older cluster has none, and its pods are not limited.

const (
	// PodBandwidthMutationPolicyName and PodBandwidthMutationBindingName are the policy that gives pods their bandwidth.
	PodBandwidthMutationPolicyName  = "geass-pod-bandwidth"
	PodBandwidthMutationBindingName = "geass-pod-bandwidth"
	// PodBandwidthAdmissionVersion changes whenever the policy or its binding does.
	PodBandwidthAdmissionVersion = "1"
)

// namespaceAnnotationExpression is the value of one annotation of the pod's namespace, or "" when it has none.
func namespaceAnnotationExpression(key string) string {
	return `has(namespaceObject.metadata.annotations) && '` + key + `' in namespaceObject.metadata.annotations ? ` +
		`namespaceObject.metadata.annotations['` + key + `'] : ''`
}

// podBandwidthMutationExpression is the JSON patch: the annotations a pod has not got are added first, because a JSON patch
// cannot add to what is not there, and then each limit the namespace names, which replaces the pod's own value. The "/" of
// an annotation's key is written "~1" in the path of a patch.
const podBandwidthMutationExpression = `((variables.ingress != '' || variables.egress != '') && !has(object.metadata.annotations) ? ` +
	`[JSONPatch{op: "add", path: "/metadata/annotations", value: {}}] : []) + ` +
	`(variables.ingress != '' ? [JSONPatch{op: "add", path: "/metadata/annotations/kubernetes.io~1ingress-bandwidth", value: variables.ingress}] : []) + ` +
	`(variables.egress != '' ? [JSONPatch{op: "add", path: "/metadata/annotations/kubernetes.io~1egress-bandwidth", value: variables.egress}] : [])`

// PodBandwidthMutationPolicy gives a new pod in a namespace that says so the bandwidth its namespace's annotations name. A
// namespace without them is not affected. A failure lets the pod through as it is, without the limit, rather than stopping
// every pod of every namespace Geass manages.
func PodBandwidthMutationPolicy() *admissionv1.MutatingAdmissionPolicy {
	return &admissionv1.MutatingAdmissionPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:        PodBandwidthMutationPolicyName,
			Labels:      map[string]string{LabelManagedBy: ManagedByValue},
			Annotations: map[string]string{AnnotationAdmissionVersion: PodBandwidthAdmissionVersion},
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
				{Name: "ingress", Expression: namespaceAnnotationExpression(AnnotationPodIngressBandwidth)},
				{Name: "egress", Expression: namespaceAnnotationExpression(AnnotationPodEgressBandwidth)},
			},
			Mutations: []admissionv1.Mutation{{
				PatchType: admissionv1.PatchTypeJSONPatch,
				JSONPatch: &admissionv1.JSONPatch{Expression: podBandwidthMutationExpression},
			}},
		},
	}
}

// PodBandwidthMutationBinding applies the policy to the namespaces Geass manages.
func PodBandwidthMutationBinding() *admissionv1.MutatingAdmissionPolicyBinding {
	return &admissionv1.MutatingAdmissionPolicyBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:        PodBandwidthMutationBindingName,
			Labels:      map[string]string{LabelManagedBy: ManagedByValue},
			Annotations: map[string]string{AnnotationAdmissionVersion: PodBandwidthAdmissionVersion},
		},
		Spec: admissionv1.MutatingAdmissionPolicyBindingSpec{
			PolicyName: PodBandwidthMutationPolicyName,
			MatchResources: &admissionv1.MatchResources{
				NamespaceSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{LabelManagedBy: ManagedByValue},
				},
			},
		},
	}
}
