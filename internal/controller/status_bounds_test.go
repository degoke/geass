package controller

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

// Status is stored in etcd with the object and rewritten whenever it changes, so a list in it that grows
// with use is a list that eventually makes every write of the object heavier, until the object no longer
// fits. Every list in a status must therefore say how long it can get, with one exception: conditions,
// which hold at most one entry per type. This reads the generated CRDs, so a new list added without a bound
// fails here instead of surfacing years later as a slow cluster.
func TestEveryListInAStatusHasABound(t *testing.T) {
	files, err := filepath.Glob("../../config/crd/bases/geass.geass.dev_*.yaml")
	require.NoError(t, err)
	require.NotEmpty(t, files, "the generated CRDs are where the schemas are")

	var unbounded []string
	for _, file := range files {
		raw, err := os.ReadFile(file)
		require.NoError(t, err)
		var crd struct {
			Spec struct {
				Versions []struct {
					Schema struct {
						OpenAPIV3Schema struct {
							Properties map[string]map[string]any `json:"properties"`
						} `json:"openAPIV3Schema"`
					} `json:"schema"`
				} `json:"versions"`
			} `json:"spec"`
		}
		require.NoError(t, yaml.Unmarshal(raw, &crd))
		for _, version := range crd.Spec.Versions {
			status, ok := version.Schema.OpenAPIV3Schema.Properties["status"]
			if !ok {
				continue
			}
			for _, path := range unboundedLists(status, "status") {
				if path != "status.conditions" {
					unbounded = append(unbounded, filepath.Base(file)+" "+path)
				}
			}
		}
	}
	slices.Sort(unbounded)
	require.Empty(t, unbounded, "give each list a +kubebuilder:validation:MaxItems marker, and keep the list within it when writing")
}

// unboundedLists returns the paths of the arrays under a schema that have no maxItems.
func unboundedLists(schema map[string]any, path string) []string {
	var found []string
	if schema["type"] == "array" {
		if _, bounded := schema["maxItems"]; !bounded {
			found = append(found, path)
		}
		if items, ok := schema["items"].(map[string]any); ok {
			found = append(found, unboundedLists(items, path+"[]")...)
		}
	}
	if properties, ok := schema["properties"].(map[string]any); ok {
		names := make([]string, 0, len(properties))
		for name := range properties {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			if child, ok := properties[name].(map[string]any); ok {
				found = append(found, unboundedLists(child, path+"."+name)...)
			}
		}
	}
	return found
}

func TestTheBoundCheckFindsAnUnboundedList(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"history": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"capped":  map[string]any{"type": "array", "maxItems": 5, "items": map[string]any{"type": "string"}},
			"nested": map[string]any{"type": "object", "properties": map[string]any{
				"rows": map[string]any{"type": "array", "maxItems": 3, "items": map[string]any{
					"type": "object", "properties": map[string]any{"tags": map[string]any{"type": "array"}},
				}},
			}},
		},
	}

	require.Equal(t, []string{"status.history", "status.nested.rows[].tags"}, unboundedLists(schema, "status"))
}
