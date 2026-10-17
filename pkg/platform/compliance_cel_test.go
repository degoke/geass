package platform

import (
	"fmt"
	"maps"
	"testing"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types/ref"
	"github.com/google/cel-go/ext"
	"github.com/stretchr/testify/require"
)

// These tests run the policy's CEL expressions, which a fake client cannot do, so a typo in one would
// otherwise only show up as pods refused (or let through) on a real cluster.

func celEnv(t *testing.T) *cel.Env {
	t.Helper()
	env, err := cel.NewEnv(
		ext.Strings(),
		cel.Variable("namespaceObject", cel.DynType),
		cel.Variable("object", cel.DynType),
		cel.Variable("variables", cel.DynType),
	)
	require.NoError(t, err)
	return env
}

func celEval(t *testing.T, env *cel.Env, expression string, vars map[string]any) any {
	t.Helper()
	ast, issues := env.Compile(expression)
	require.NoError(t, issues.Err(), expression)
	program, err := env.Program(ast)
	require.NoError(t, err)
	out, _, err := program.Eval(vars)
	require.NoError(t, err, expression)
	return out.Value()
}

func namespaceWith(tags string) map[string]any {
	if tags == "-" {
		return map[string]any{"metadata": map[string]any{}}
	}
	return map[string]any{"metadata": map[string]any{"annotations": map[string]any{AnnotationRequiredTags: tags}}}
}

func podWith(selector map[string]any, nodeName string) map[string]any {
	spec := map[string]any{}
	if selector != nil {
		spec["nodeSelector"] = selector
	}
	if nodeName != "" {
		spec["nodeName"] = nodeName
	}
	return map[string]any{"spec": spec}
}

func TestComplianceAdmissionExpressionsDecideAsIntended(t *testing.T) {
	env := celEnv(t)
	policy := ComplianceAdmissionPolicy()
	required := policy.Spec.Variables[0].Expression
	selectorRule := policy.Spec.Validations[0].Expression
	nameRule := policy.Spec.Validations[1].Expression

	hipaa := map[string]any{TagKeyPrefix + "hipaa": "true"}
	both := map[string]any{TagKeyPrefix + "hipaa": "true", TagKeyPrefix + "pci": "true"}

	tests := []struct {
		name        string
		annotation  string
		pod         map[string]any
		wantAllowed bool
		wantNamed   bool
	}{
		{"namespace without the annotation allows anything", "-", podWith(nil, ""), true, true},
		{"an empty annotation allows anything", "", podWith(nil, ""), true, true},
		{"a pod with the selector is allowed", "hipaa", podWith(hipaa, ""), true, true},
		{"a pod without a node selector is refused", "hipaa", podWith(nil, ""), false, true},
		{"a pod on other tags is refused", "hipaa", podWith(map[string]any{"disk": "ssd"}, ""), false, true},
		{"the tag must be true", "hipaa", podWith(map[string]any{TagKeyPrefix + "hipaa": "false"}, ""), false, true},
		{"every required tag is needed", "hipaa,pci", podWith(hipaa, ""), false, true},
		{"all tags present is allowed", "hipaa,pci", podWith(both, ""), true, true},
		{"naming a node is refused", "hipaa", podWith(hipaa, "worker-1"), true, false},
		{"naming a node is fine where nothing is required", "-", podWith(nil, "worker-1"), true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vars := map[string]any{"namespaceObject": namespaceWith(tt.annotation), "object": tt.pod}
			list := celEval(t, env, required, vars)
			vars["variables"] = map[string]any{"required": list}

			require.Equal(t, tt.wantAllowed, celEval(t, env, selectorRule, vars), "selector rule")
			require.Equal(t, tt.wantNamed, celEval(t, env, nameRule, vars), "node name rule")
		})
	}
}

func TestComplianceAdmissionMessagesCompile(t *testing.T) {
	env := celEnv(t)
	message := ComplianceAdmissionPolicy().Spec.Validations[0].MessageExpression

	out := celEval(t, env, message, map[string]any{"variables": map[string]any{"required": []any{"hipaa", "pci"}}})

	require.Equal(t, `This project requires nodes tagged hipaa, pci, so every pod needs the node selector tag.geass.dev/<tag>: "true" for each of them`, out)
}

func TestComplianceMutationVariablesFindWhatAPodLacks(t *testing.T) {
	env := celEnv(t)
	policy := ComplianceMutationPolicy()
	require.Equal(t, []string{"required", "missingSelectors", "missingTolerations"}, []string{
		policy.Spec.Variables[0].Name, policy.Spec.Variables[1].Name, policy.Spec.Variables[2].Name,
	})
	missingSelectors := policy.Spec.Variables[1].Expression
	missingTolerations := policy.Spec.Variables[2].Expression

	hipaa := map[string]any{TagKeyPrefix + "hipaa": "true"}
	tolerates := func(tags ...string) map[string]any {
		tolerations := make([]any, 0, len(tags))
		for _, tag := range tags {
			tolerations = append(tolerations, map[string]any{"key": TagKeyPrefix + tag, "operator": "Exists", "effect": "NoSchedule"})
		}
		return map[string]any{"tolerations": tolerations}
	}
	with := func(selector map[string]any, extra map[string]any) map[string]any {
		pod := podWith(selector, "")
		maps.Copy(pod["spec"].(map[string]any), extra)
		return pod
	}

	tests := []struct {
		name           string
		annotation     string
		pod            map[string]any
		wantSelectors  []any
		wantTolerances []any
	}{
		{"nothing is required", "-", podWith(nil, ""), []any{}, []any{}},
		{"a pod with nothing lacks everything", "hipaa,pci", podWith(nil, ""), []any{"hipaa", "pci"}, []any{"hipaa", "pci"}},
		{"a pod that has the selector and the toleration lacks nothing", "hipaa", with(hipaa, tolerates("hipaa")), []any{}, []any{}},
		{"only what is missing is named", "hipaa,pci", with(hipaa, tolerates("pci")), []any{"pci"}, []any{"hipaa"}},
		{"a selector that is not true counts as missing", "hipaa", podWith(map[string]any{TagKeyPrefix + "hipaa": "false"}, ""), []any{"hipaa"}, []any{"hipaa"}},
		{"other selectors and tolerations are not the tag's", "hipaa", with(map[string]any{"disk": "ssd"}, tolerates("other")), []any{"hipaa"}, []any{"hipaa"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vars := map[string]any{"namespaceObject": namespaceWith(tt.annotation), "object": tt.pod}
			vars["variables"] = map[string]any{"required": celEval(t, env, policy.Spec.Variables[0].Expression, vars)}

			require.Equal(t, tt.wantSelectors, toAnyList(celEval(t, env, missingSelectors, vars)), "selectors")
			require.Equal(t, tt.wantTolerances, toAnyList(celEval(t, env, missingTolerations, vars)), "tolerations")
		})
	}
}

// toAnyList turns the list a CEL program returns into plain values to compare.
func toAnyList(value any) []any {
	out := []any{}
	switch list := value.(type) {
	case []any:
		out = append(out, list...)
	case []ref.Val:
		for _, item := range list {
			out = append(out, item.Value())
		}
	default:
		panic(fmt.Sprintf("not a list: %T", value))
	}
	return out
}

func TestComplianceMutationPatchPathsEscapeTheTagKey(t *testing.T) {
	require.Contains(t, ComplianceMutationPolicy().Spec.Mutations[0].JSONPatch.Expression, `/spec/nodeSelector/tag.geass.dev~1"`,
		"the \"/\" of a label key is \"~1\" in a JSON pointer, or the patch would add to a nested path")
}
