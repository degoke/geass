package installer

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

const geassAPIGroup = "geass.geass.dev"

// liveCRD is a definition as the cluster has it: which versions objects are stored in.
type liveCRD struct {
	Name           string
	StoredVersions []string
}

func liveGeassCRDs(step string) ([]liveCRD, error) {
	items, ok, err := kubeGetList(step, "crd")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	var out []liveCRD
	for _, raw := range items {
		var crd struct {
			Metadata kubeMeta `json:"metadata"`
			Spec     struct {
				Group string `json:"group"`
			} `json:"spec"`
			Status struct {
				StoredVersions []string `json:"storedVersions"`
			} `json:"status"`
		}
		if json.Unmarshal(raw, &crd) != nil || crd.Spec.Group != geassAPIGroup {
			continue
		}
		out = append(out, liveCRD{Name: crd.Metadata.Name, StoredVersions: crd.Status.StoredVersions})
	}
	return out, nil
}

// servedVersions reads which versions each definition in the manifests serves.
func servedVersions(crdManifests []byte) (map[string]map[string]bool, error) {
	docs, err := manifestDocs(crdManifests)
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]bool{}
	for _, doc := range docs {
		var obj unstructured.Unstructured
		if err := yaml.Unmarshal(doc, &obj.Object); err != nil {
			return nil, err
		}
		versions, _, _ := unstructured.NestedSlice(obj.Object, "spec", "versions")
		served := map[string]bool{}
		for _, v := range versions {
			m, ok := v.(map[string]any)
			if !ok {
				continue
			}
			name, _ := m["name"].(string)
			isServed, _ := m["served"].(bool)
			served[name] = isServed
		}
		out[obj.GetName()] = served
	}
	return out, nil
}

// crdReplacementProblems says which of the cluster's definitions the ones that are about to be applied cannot safely
// replace. The one rule that no release can be allowed to break: every version that a cluster has stored objects in
// stays served. A cluster that has been through several releases may hold objects in a version that a newer release
// has stopped serving, and the API server then cannot read them. This is checked against the cluster itself, because
// only the cluster knows what it stored, and not only the release before this one.
func crdReplacementProblems(live []liveCRD, incoming map[string]map[string]bool) []string {
	var out []string
	for _, crd := range live {
		served, ok := incoming[crd.Name]
		if !ok {
			// The release drops a kind that this cluster has. Applying the new manifests leaves the old definition
			// where it is (nothing is deleted), so nothing is lost, but it is worth knowing.
			continue
		}
		for _, v := range crd.StoredVersions {
			if !served[v] {
				out = append(out, fmt.Sprintf("%s has objects stored as %s, which this release does not serve", crd.Name, v))
			}
		}
	}
	slices.Sort(out)
	return out
}

func describeProblems(problems []string) string {
	var b strings.Builder
	for _, p := range problems {
		b.WriteString("  - " + p + "\n")
	}
	return b.String()
}
