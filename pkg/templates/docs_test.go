package templates

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/yaml"
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
