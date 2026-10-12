package platform

import (
	"testing"

	"github.com/google/cel-go/cel"
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
