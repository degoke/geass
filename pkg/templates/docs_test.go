package templates

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"

	"github.com/degoke/geass/pkg/platform"
)

const templatesDoc = "../../website/src/content/docs/docs/guides/templates.md"

// exampleBlock matches a fenced block the docs title as a template ConfigMap, such as template-acme-stack.yaml.
var exampleBlock = regexp.MustCompile("(?s)```yaml title=\"(template-[a-z0-9-]+\\.yaml)\"\n(.*?)\n```")

// The docs tell people to copy these ConfigMaps as they are, so each one has to be a template Geass accepts.
func TestEveryTemplateExampleInTheDocsIsValid(t *testing.T) {
	doc, err := os.ReadFile(templatesDoc)
	if os.IsNotExist(err) {
		t.Skip("the documentation is not part of this checkout")
	}
	require.NoError(t, err)

	blocks := exampleBlock.FindAllStringSubmatch(string(doc), -1)
	require.GreaterOrEqual(t, len(blocks), 6, "the five examples and the hand-written one")
	for _, block := range blocks {
		file, manifest := block[1], block[2]
		t.Run(file, func(t *testing.T) {
			var configMap corev1.ConfigMap
			require.NoError(t, yaml.UnmarshalStrict([]byte(manifest), &configMap))
			require.Equal(t, "true", configMap.Labels["geass.dev/template"])
			require.Equal(t, "geass-system", configMap.Namespace)

			var template Template
			require.NoError(t, json.Unmarshal([]byte(configMap.Data["template.json"]), &template))
			require.NoError(t, template.Validate())
			require.Equal(t, "template-"+template.ID, configMap.Name, "the name must be template- plus the id")
			require.Equal(t, file, configMap.Name+".yaml")
			_, inCatalog := Find(template.ID)
			require.False(t, inCatalog, "an example cannot reuse a catalog id: Geass would ignore it")
		})
	}
}

// tableRows returns the cells of every table row whose first cell is a bold name, keyed by that name and the
// number of cells. The catalog table has three cells a row and the sizing table four, so one name can be in both.
func tableRows(doc string) map[string][]string {
	rows := map[string][]string{}
	for line := range strings.SplitSeq(doc, "\n") {
		if !strings.HasPrefix(line, "| **") {
			continue
		}
		cells := strings.Split(strings.TrimSuffix(strings.TrimPrefix(line, "| "), " |"), " | ")
		rows[fmt.Sprintf("%s/%d", strings.Trim(cells[0], "*"), len(cells))] = cells
	}
	return rows
}

// sizeLabel is how the docs, and the dashboard's size picker, write the CPU and memory of a resource list.
func sizeLabel(t *testing.T, list corev1.ResourceList) string {
	t.Helper()
	label := func(options []platform.SizeOption, value string) string {
		for _, option := range options {
			if option.Value == value {
				return option.Label
			}
		}
		require.Failf(t, "size is not on the picker's lists", "%s", value)
		return ""
	}
	return label(platform.CPUSizes(), list.Cpu().String()) + ", " + label(platform.MemorySizes(), list.Memory().String())
}

// The docs repeat each catalog entry's image, reserve and ceiling by hand. They have to say what Builtin() does,
// or a version bump that misses one place publishes the wrong numbers with every test passing.
func TestTheCatalogTablesInTheDocsMatchTheCatalog(t *testing.T) {
	doc, err := os.ReadFile(templatesDoc)
	if os.IsNotExist(err) {
		t.Skip("the documentation is not part of this checkout")
	}
	require.NoError(t, err)
	rows := tableRows(string(doc))

	for _, template := range Builtin() {
		t.Run(template.ID, func(t *testing.T) {
			catalog, ok := rows[template.Name+"/3"]
			require.True(t, ok, "%s has no row in the catalog table", template.Name)

			var sized *Resource
			for i, resource := range template.Resources {
				if resource.Image != "" {
					require.Contains(t, catalog[1], "`"+resource.Image+"`", "the catalog table names another image for %s", resource.Key)
				}
				if resource.Size != nil {
					sized = &template.Resources[i]
				}
			}

			sizing, ok := rows[template.Name+"/4"]
			if sized == nil {
				require.False(t, ok, "%s has a sizing row but no size", template.Name)
				return
			}
			require.True(t, ok, "%s has a size and no row in the sizing table", template.Name)
			resources := sized.AppResources()
			require.Equal(t, sizeLabel(t, resources.Requests), sizing[1], "reserved")
			require.Equal(t, sizeLabel(t, resources.Limits), sizing[2], "may use up to")
		})
	}
}
