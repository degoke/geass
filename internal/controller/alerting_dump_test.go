package controller

import (
	"os"
	"testing"

	"sigs.k8s.io/yaml"
)

// TestDumpAlertRules writes the Prometheus rules that Geass makes to the file named by GEASS_DUMP_RULES, in the form
// Prometheus reads them, for `make test-alert-rules` to check with promtool. It does nothing otherwise.
func TestDumpAlertRules(t *testing.T) {
	out := os.Getenv("GEASS_DUMP_RULES")
	if out == "" {
		t.Skip("set GEASS_DUMP_RULES to a file to write the rules to")
	}
	groups := make([]any, 0, 8)
	for _, group := range alertRules().Spec.Groups {
		groups = append(groups, group)
	}
	for _, group := range operatorRules().Spec.Groups {
		groups = append(groups, group)
	}
	raw, err := yaml.Marshal(map[string]any{"groups": groups})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}
