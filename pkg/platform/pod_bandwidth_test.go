package platform

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	admissionv1 "k8s.io/api/admissionregistration/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apiserver/pkg/admission"
	plugincel "k8s.io/apiserver/pkg/admission/plugin/cel"
	"k8s.io/apiserver/pkg/admission/plugin/policy/mutating/patch"
	"k8s.io/apiserver/pkg/admission/plugin/policy/validating"
	celconfig "k8s.io/apiserver/pkg/apis/cel"
	"k8s.io/apiserver/pkg/cel/environment"
)

// The bandwidth policy is run here by the API server's own code for mutating admission policies: its CEL compiler, with the
// variables and the JSONPatch type a real policy has, and its JSON patcher, on a real pod and namespace. What this does not
// run is the API server's matching of the policy and its binding to a request; nothing here has a real API server.

// admitPod runs the policy's mutation on a pod made in a namespace, as the API server would on its creation.
func admitPod(t *testing.T, policy *admissionv1.MutatingAdmissionPolicy, namespace *corev1.Namespace, pod *corev1.Pod) *corev1.Pod {
	t.Helper()
	compiler, err := plugincel.NewCompositedCompiler(environment.MustBaseEnvSet(environment.DefaultCompatibilityVersion()))
	require.NoError(t, err)
	// As the API server compiles a policy: the variables without the patch types, the patch with them.
	options := plugincel.OptionalVariableDeclarations{HasAuthorizer: true}
	variables := make([]plugincel.NamedExpressionAccessor, 0, len(policy.Spec.Variables))
	for _, v := range policy.Spec.Variables {
		variables = append(variables, &validating.Variable{Name: v.Name, Expression: v.Expression})
	}
	compiler.CompileAndStoreVariables(variables, options, environment.StoredExpressions)
	require.Len(t, policy.Spec.Mutations, 1)
	options.HasPatchTypes = true
	evaluator := compiler.CompileMutatingEvaluator(&patch.JSONPatchCondition{Expression: policy.Spec.Mutations[0].JSONPatch.Expression}, options, environment.StoredExpressions)
	require.Empty(t, evaluator.CompilationErrors(), "the patch does not compile")

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	pods := schema.GroupVersionResource{Version: "v1", Resource: "pods"}
	kind := corev1.SchemeGroupVersion.WithKind("Pod")
	attributes := admission.NewAttributesRecord(pod, nil, kind, namespace.Name, pod.Name, pods, "", admission.Create, &metav1.CreateOptions{}, false, nil)
	request := patch.Request{
		MatchedResource: pods,
		VersionedAttributes: &admission.VersionedAttributes{
			Attributes: attributes, VersionedKind: kind,
			VersionedObject: admission.NewLazyObject(pod), VersionedOldObject: admission.NewLazyObject(nil),
		},
		ObjectInterfaces:  admission.NewObjectInterfacesFromScheme(scheme),
		OptionalVariables: plugincel.OptionalVariableBindings{},
		Namespace:         namespace,
	}
	// The variables are read through the context, as the API server's dispatcher makes it.
	out, err := patch.NewJSONPatcher(evaluator).Patch(compiler.CreateContext(context.Background()), request, celconfig.RuntimeCELCostBudget)
	require.NoError(t, err)
	admitted, ok := out.(*corev1.Pod)
	require.True(t, ok, "%T", out)
	return admitted
}

func bandwidthNamespace(ingress, egress string) *corev1.Namespace {
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "shop-production", Labels: map[string]string{LabelManagedBy: ManagedByValue}}}
	if ingress != "" || egress != "" {
		ns.Annotations = map[string]string{}
	}
	if ingress != "" {
		ns.Annotations[AnnotationPodIngressBandwidth] = ingress
	}
	if egress != "" {
		ns.Annotations[AnnotationPodEgressBandwidth] = egress
	}
	return ns
}

func TestAPodIsGivenTheBandwidthItsNamespaceNames(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "shop-production"}}

	admitted := admitPod(t, PodBandwidthMutationPolicy(), bandwidthNamespace("100M", "20M"), pod)

	require.Equal(t, "100M", admitted.Annotations[PodIngressBandwidthAnnotation])
	require.Equal(t, "20M", admitted.Annotations[PodEgressBandwidthAnnotation])
	require.Empty(t, pod.Annotations, "the pod that was sent is not changed in place")
}

func TestAPodKeepsItsOwnAnnotationsAndCannotGiveItselfMore(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "shop-production", Annotations: map[string]string{
		"example.com/note": "kept", PodEgressBandwidthAnnotation: "10G",
	}}}

	admitted := admitPod(t, PodBandwidthMutationPolicy(), bandwidthNamespace("", "20M"), pod)

	require.Equal(t, "20M", admitted.Annotations[PodEgressBandwidthAnnotation], "the namespace's limit replaces what the pod says")
	require.Equal(t, "kept", admitted.Annotations["example.com/note"])
	_, ingress := admitted.Annotations[PodIngressBandwidthAnnotation]
	require.False(t, ingress, "a direction the namespace does not limit is not added")
}

func TestAPodInANamespaceThatLimitsNothingIsLeftAsItIs(t *testing.T) {
	for _, pod := range []*corev1.Pod{
		{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "shop-production"}},
		{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "shop-production", Annotations: map[string]string{PodEgressBandwidthAnnotation: "5M"}}},
	} {
		admitted := admitPod(t, PodBandwidthMutationPolicy(), bandwidthNamespace("", ""), pod)
		require.Equal(t, pod.Annotations, admitted.Annotations)
	}
}

func TestTheBandwidthPolicyOnlyEverAddsToPodsAndFailsOpen(t *testing.T) {
	policy := PodBandwidthMutationPolicy()
	require.Equal(t, admissionv1.Ignore, *policy.Spec.FailurePolicy, "a policy that cannot run must not stop every pod of every namespace Geass manages")
	rules := policy.Spec.MatchConstraints.ResourceRules
	require.Len(t, rules, 1)
	require.Equal(t, []admissionv1.OperationType{admissionv1.Create}, rules[0].Operations, "a pod's network is set up once, when it starts")
	require.Equal(t, []string{"pods"}, rules[0].Resources)
	binding := PodBandwidthMutationBinding()
	require.Equal(t, policy.Name, binding.Spec.PolicyName)
	require.Equal(t, ManagedByValue, binding.Spec.MatchResources.NamespaceSelector.MatchLabels[LabelManagedBy])
	require.Equal(t, PodBandwidthAdmissionVersion, policy.Annotations[AnnotationAdmissionVersion])
	require.Equal(t, PodBandwidthAdmissionVersion, binding.Annotations[AnnotationAdmissionVersion])
}
