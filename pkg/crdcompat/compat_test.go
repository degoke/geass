package crdcompat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

// spec parses the schema of one version's `spec` from YAML, so that a test reads like the CRD it describes.
func spec(t *testing.T, doc string) *apiextensionsv1.JSONSchemaProps {
	t.Helper()
	var props apiextensionsv1.JSONSchemaProps
	require.NoError(t, yaml.UnmarshalStrict([]byte(doc), &props), doc)
	return &props
}

// widget is a definition whose only version has this `spec`.
func widget(t *testing.T, specYAML string) apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	return apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{Name: "widgets.example.dev"},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "example.dev",
			Scope: apiextensionsv1.NamespaceScoped,
			Names: apiextensionsv1.CustomResourceDefinitionNames{Kind: "Widget", Plural: "widgets", Singular: "widget"},
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
				Name: "v1", Served: true, Storage: true,
				Subresources: &apiextensionsv1.CustomResourceSubresources{Status: &apiextensionsv1.CustomResourceSubresourceStatus{}},
				Schema: &apiextensionsv1.CustomResourceValidation{OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
					Type:       "object",
					Properties: map[string]apiextensionsv1.JSONSchemaProps{"spec": *spec(t, specYAML)},
				}},
			}},
		},
	}
}

func compare(t *testing.T, before, after string) []Violation {
	t.Helper()
	return CompareCRD(ptr(widget(t, before)), ptr(widget(t, after)))
}

func ptr[T any](v T) *T { return &v }

// rules lists which rules were broken, at which paths.
func rules(vs []Violation) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.Rule+" "+v.Path)
	}
	return out
}

const base = `
type: object
required: [name]
properties:
  name: {type: string, maxLength: 63}
  replicas: {type: integer, minimum: 0, maximum: 10}
  mode: {type: string, enum: [fast, safe]}
  addons:
    type: object
    properties:
      longhorn:
        type: object
        properties:
          enabled: {type: boolean}
  ports:
    type: array
    items:
      type: object
      required: [port]
      properties:
        port: {type: integer}
  labels:
    type: object
    additionalProperties: {type: string}
`

func TestAdditiveChangesAreSafe(t *testing.T) {
	added := strings.Replace(base, "  labels:", `  region: {type: string, pattern: "^[a-z]+$"}
  retention: {type: integer, default: 7, minimum: 1}
  labels:`, 1)
	added = strings.Replace(added, "port: {type: integer}", "port: {type: integer}\n        protocol: {type: string, enum: [tcp, udp]}", 1)
	added = strings.Replace(added, "enabled: {type: boolean}", "enabled: {type: boolean}\n          replicas: {type: integer}", 1)
	added = strings.Replace(added, "enum: [fast, safe]", "enum: [fast, safe, slow]", 1)
	added = strings.Replace(added, "maxLength: 63", "maxLength: 253", 1)
	added = strings.Replace(added, "maximum: 10", "maximum: 100", 1)

	require.Empty(t, compare(t, base, added), "new optional fields, a wider enum and looser bounds break nothing")
}

func TestDescriptionsAndDefaultsDoNotMatter(t *testing.T) {
	changed := strings.Replace(base, "name: {type: string, maxLength: 63}", "name: {type: string, maxLength: 63, description: a new description, default: x}", 1)
	require.Empty(t, compare(t, base, changed))
}

func TestARemovedFieldIsLoss(t *testing.T) {
	removed := strings.Replace(base, "  mode: {type: string, enum: [fast, safe]}\n", "", 1)
	vs := compare(t, base, removed)
	require.Equal(t, []string{"field-removed spec.mode"}, rules(vs))
	require.Contains(t, vs[0].Message, "pruned")
	require.Contains(t, vs[0].Message, "migrate", "the message points at the way out")
}

func TestARemovedNestedFieldIsFoundWhereItIs(t *testing.T) {
	removed := strings.Replace(base, "          enabled: {type: boolean}", "          other: {type: boolean}", 1)
	require.ElementsMatch(t, []string{"field-removed spec.addons.longhorn.enabled"}, rules(compare(t, base, removed)))
}

func TestAFieldInsideAListOrMapIsFound(t *testing.T) {
	removed := strings.Replace(base, "        port: {type: integer}", "        number: {type: integer}", 1)
	vs := compare(t, base, removed)
	require.Equal(t, []string{"field-removed spec.ports[].port"}, rules(vs))
}

func TestARenamedFieldIsARemovalAndAnAddition(t *testing.T) {
	renamed := strings.Replace(base, "  mode:", "  strategy:", 1)
	require.Equal(t, []string{"field-removed spec.mode"}, rules(compare(t, base, renamed)))
}

func TestChangingATypeBreaksStoredValues(t *testing.T) {
	changed := strings.Replace(base, "replicas: {type: integer, minimum: 0, maximum: 10}", "replicas: {type: string}", 1)
	vs := compare(t, base, changed)
	require.Equal(t, []string{"type-changed spec.replicas"}, rules(vs), "and nothing under it is compared")
}

func TestARequiredFieldBreaksObjectsThatLackIt(t *testing.T) {
	required := strings.Replace(base, "required: [name]", "required: [name, replicas]", 1)
	require.Equal(t, []string{"required-added spec.replicas"}, rules(compare(t, base, required)))
}

func TestARequiredFieldWithADefaultIsFilledInOnRead(t *testing.T) {
	required := strings.Replace(base, "required: [name]", "required: [name, replicas]", 1)
	required = strings.Replace(required, "replicas: {type: integer, minimum: 0, maximum: 10}", "replicas: {type: integer, minimum: 0, maximum: 10, default: 1}", 1)
	require.Empty(t, compare(t, base, required))
}

func TestANewFieldThatIsRequiredBreaksExistingObjects(t *testing.T) {
	required := strings.Replace(base, "required: [name]", "required: [name, brandNew]", 1)
	required = strings.Replace(required, "  mode:", "  brandNew: {type: string}\n  mode:", 1)
	require.Equal(t, []string{"required-added spec.brandNew"}, rules(compare(t, base, required)),
		"existing objects have no value for a field that did not exist")
}

func TestARequiredFieldInANewObjectIsFine(t *testing.T) {
	added := strings.Replace(base, "  labels:", `  backup:
    type: object
    required: [bucket]
    properties:
      bucket: {type: string}
  labels:`, 1)
	require.Empty(t, compare(t, base, added), "no stored object has a backup, so none can lack its bucket")
}

func TestNarrowingAnEnumBreaksStoredValues(t *testing.T) {
	narrowed := strings.Replace(base, "enum: [fast, safe]", "enum: [fast]", 1)
	vs := compare(t, base, narrowed)
	require.Equal(t, []string{"enum-restricted spec.mode"}, rules(vs))
	require.Contains(t, vs[0].Message, `"safe"`)

	restricted := strings.Replace(base, "name: {type: string, maxLength: 63}", "name: {type: string, maxLength: 63, enum: [a, b]}", 1)
	require.Equal(t, []string{"enum-restricted spec.name"}, rules(compare(t, base, restricted)), "a free value that becomes a list")
}

func TestTighteningABoundBreaksStoredValues(t *testing.T) {
	for name, edit := range map[string][2]string{
		"lower maxLength":   {"maxLength: 63", "maxLength: 32"},
		"new maxLength":     {"name: {type: string, maxLength: 63}", "name: {type: string, maxLength: 63}\n  other: {type: string, maxLength: 5}"},
		"higher minimum":    {"minimum: 0", "minimum: 1"},
		"lower maximum":     {"maximum: 10", "maximum: 5"},
		"a pattern":         {"name: {type: string, maxLength: 63}", "name: {type: string, maxLength: 63, pattern: '^[a-z]+$'}"},
		"a format":          {"name: {type: string, maxLength: 63}", "name: {type: string, maxLength: 63, format: date-time}"},
		"a minimum length":  {"name: {type: string, maxLength: 63}", "name: {type: string, maxLength: 63, minLength: 3}"},
		"unique items":      {"    type: array\n    items:", "    type: array\n    uniqueItems: true\n    items:"},
		"fewer items":       {"    type: array\n    items:", "    type: array\n    maxItems: 3\n    items:"},
		"exclusive maximum": {"maximum: 10", "maximum: 10, exclusiveMaximum: true"},
	} {
		after := strings.Replace(base, edit[0], edit[1], 1)
		if name == "new maxLength" {
			// A brand new field with a bound is fine: nothing stored has it.
			require.Empty(t, compare(t, base, after), name)
			continue
		}
		vs := compare(t, base, after)
		require.NotEmpty(t, vs, name)
		require.Equal(t, RuleValidationTightened, vs[0].Rule, name)
	}
}

func TestAPatternThatChangesIsTighterEvenIfItLooksLooser(t *testing.T) {
	// Whether one regular expression accepts everything another does is not something to guess at.
	before := strings.Replace(base, "name: {type: string, maxLength: 63}", "name: {type: string, pattern: '^[a-z]+$'}", 1)
	after := strings.Replace(base, "name: {type: string, maxLength: 63}", "name: {type: string, pattern: '^[a-z0-9]+$'}", 1)
	require.Equal(t, []string{"validation-tightened spec.name"}, rules(compare(t, before, after)))
}

func TestDroppingNullableMakesStoredNullsInvalid(t *testing.T) {
	before := strings.Replace(base, "name: {type: string, maxLength: 63}", "name: {type: string, nullable: true}", 1)
	after := strings.Replace(base, "name: {type: string, maxLength: 63}", "name: {type: string}", 1)
	require.Equal(t, []string{"validation-tightened spec.name"}, rules(compare(t, before, after)))
}

func TestAnObjectThatStopsPreservingUnknownFieldsLosesThem(t *testing.T) {
	before := strings.Replace(base, "  labels:", "  extra: {type: object, x-kubernetes-preserve-unknown-fields: true}\n  labels:", 1)
	after := strings.Replace(base, "  labels:", "  extra: {type: object, properties: {a: {type: string}}}\n  labels:", 1)
	require.Equal(t, []string{"preserve-unknown-removed spec.extra"}, rules(compare(t, before, after)))
}

func TestFieldsRemovedBeneathAnObjectThatPreservesUnknownFieldsAreKept(t *testing.T) {
	before := strings.Replace(base, "  labels:", "  extra: {type: object, properties: {a: {type: string}}, x-kubernetes-preserve-unknown-fields: true}\n  labels:", 1)
	after := strings.Replace(base, "  labels:", "  extra: {type: object, x-kubernetes-preserve-unknown-fields: true}\n  labels:", 1)
	require.Empty(t, compare(t, before, after))
}

func TestChangingWhatIdentifiesAListElementChangesWhatIsValid(t *testing.T) {
	before := strings.Replace(base, "    type: array\n    items:", "    type: array\n    x-kubernetes-list-type: map\n    x-kubernetes-list-map-keys: [port]\n    items:", 1)
	after := strings.Replace(before, "x-kubernetes-list-map-keys: [port]", "x-kubernetes-list-map-keys: [port, protocol]", 1)
	require.Equal(t, []string{"list-semantics-changed spec.ports"}, rules(compare(t, before, after)))
	require.Equal(t, []string{"list-semantics-changed spec.ports"}, rules(compare(t, base, before)), "a list that becomes a map must have unique keys")
}

func TestANewValidationRuleBreaksObjectsThatDoNotMeetIt(t *testing.T) {
	after := strings.Replace(base, "properties:\n  name:", "x-kubernetes-validations:\n- rule: self.replicas > 1\nproperties:\n  name:", 1)
	vs := compare(t, base, after)
	require.Equal(t, []string{"validation-tightened spec"}, rules(vs))
	require.Contains(t, vs[0].Message, "self.replicas > 1")

	require.Empty(t, compare(t, after, base), "removing a rule loosens")
}

func TestMapValuesAreComparedToo(t *testing.T) {
	after := strings.Replace(base, "additionalProperties: {type: string}", "additionalProperties: {type: string, maxLength: 8}", 1)
	require.Equal(t, []string{"validation-tightened spec.labels{}"}, rules(compare(t, base, after)))
}

func TestARemovedDefinitionIsLoss(t *testing.T) {
	old := []apiextensionsv1.CustomResourceDefinition{widget(t, base)}
	vs := Compare(old, nil)
	require.Len(t, vs, 1)
	require.Equal(t, RuleCRDRemoved, vs[0].Rule)

	require.Empty(t, Compare(nil, old), "a new definition breaks nothing")
}

func TestDefinitionLevelChanges(t *testing.T) {
	for name, tc := range map[string]struct {
		edit func(*apiextensionsv1.CustomResourceDefinition)
		rule string
	}{
		"scope":                 {func(c *apiextensionsv1.CustomResourceDefinition) { c.Spec.Scope = apiextensionsv1.ClusterScoped }, RuleScopeChanged},
		"plural":                {func(c *apiextensionsv1.CustomResourceDefinition) { c.Spec.Names.Plural = "gadgets" }, RuleNamesChanged},
		"kind":                  {func(c *apiextensionsv1.CustomResourceDefinition) { c.Spec.Names.Kind = "Gadget" }, RuleNamesChanged},
		"group":                 {func(c *apiextensionsv1.CustomResourceDefinition) { c.Spec.Group = "other.dev" }, RuleNamesChanged},
		"status subresource":    {func(c *apiextensionsv1.CustomResourceDefinition) { c.Spec.Versions[0].Subresources = nil }, RuleSubresourceRemoved},
		"unserving the version": {func(c *apiextensionsv1.CustomResourceDefinition) { c.Spec.Versions[0].Served = false }, RuleVersionUnserved},
		"removing the version":  {func(c *apiextensionsv1.CustomResourceDefinition) { c.Spec.Versions = nil }, RuleVersionRemoved},
	} {
		before := widget(t, base)
		after := widget(t, base)
		tc.edit(&after)
		vs := CompareCRD(&before, &after)
		require.NotEmpty(t, vs, name)
		require.Equal(t, tc.rule, vs[0].Rule, name)
	}
}

func TestAddingAVersionAndMovingStorageNeedsAConversionPlan(t *testing.T) {
	before := widget(t, base)
	after := widget(t, base)
	v2 := *after.Spec.Versions[0].DeepCopy()
	v2.Name = "v2"
	after.Spec.Versions = append(after.Spec.Versions, v2)
	require.Empty(t, CompareCRD(&before, &after), "a second version that is not stored yet is safe")

	after.Spec.Versions[0].Storage = false
	vs := CompareCRD(&before, &after)
	require.Equal(t, []string{"storage-version-changed "}, rules(vs))
	require.Contains(t, vs[0].Message, "v1")
}

func TestEachVersionIsComparedAgainstItself(t *testing.T) {
	before := widget(t, base)
	before.Spec.Versions = append(before.Spec.Versions, *before.Spec.Versions[0].DeepCopy())
	before.Spec.Versions[1].Name, before.Spec.Versions[1].Storage = "v2", false
	after := before.DeepCopy()
	delete(after.Spec.Versions[1].Schema.OpenAPIV3Schema.Properties["spec"].Properties, "mode")

	vs := CompareCRD(&before, after)
	require.Len(t, vs, 1)
	require.Equal(t, "v2", vs[0].Version)
	require.Equal(t, "spec.mode", vs[0].Path)
}

func TestExceptionsLetADeliberateChangeThroughAndExpireWhenUnused(t *testing.T) {
	removed := strings.Replace(base, "  mode: {type: string, enum: [fast, safe]}\n", "", 1)
	vs := compare(t, base, removed)
	vs[0].CRD = "widgets.example.dev"

	ex := Exceptions{Exceptions: []Exception{
		{CRD: "widgets.example.dev", Path: "spec.mode", Rule: RuleFieldRemoved, Reason: "never read by anything"},
		{CRD: "widgets.example.dev", Path: "spec.gone", Rule: RuleFieldRemoved, Reason: "since reverted"},
	}}
	remaining, unused := ex.Apply(vs)
	require.Empty(t, remaining)
	require.Len(t, unused, 1)
	require.Equal(t, "spec.gone", unused[0].Path, "an exception for a change that no longer exists is reported, so it does not wait around to excuse a later one")

	remaining, _ = Exceptions{}.Apply(vs)
	require.Len(t, remaining, 1)
}

func TestAnExceptionMustSayWhy(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "exceptions.yaml")
	require.NoError(t, writeFile(path, "exceptions:\n- crd: widgets.example.dev\n  path: spec.mode\n  rule: field-removed\n"))
	_, err := LoadExceptions(path)
	require.ErrorContains(t, err, "no reason")

	require.NoError(t, writeFile(path, "exceptions:\n- crd: widgets.example.dev\n  path: spec.mode\n  rule: field-removed\n  reason: never read\n"))
	ex, err := LoadExceptions(path)
	require.NoError(t, err)
	require.Len(t, ex.Exceptions, 1)

	ex, err = LoadExceptions(filepath.Join(dir, "missing.yaml"))
	require.NoError(t, err, "no file is no exceptions")
	require.Empty(t, ex.Exceptions)
}

func TestTheShippedDefinitionsAreCompatibleWithThemselves(t *testing.T) {
	crds, err := LoadDir(filepath.Join("..", "..", "config", "crd", "bases"), "geass.geass.dev")
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(crds), 15, "the Geass definitions were found")
	require.Empty(t, Compare(crds, crds))
	for _, crd := range crds {
		require.Equal(t, "geass.geass.dev", crd.Spec.Group, "other projects' definitions kept beside them are not checked")
	}
}

func writeFile(path, content string) error { return os.WriteFile(path, []byte(content), 0o600) }

func TestFreeFormMapValuesThatBecomeTypedBreakStoredValuesOfOtherTypes(t *testing.T) {
	free := strings.Replace(base, "additionalProperties: {type: string}", "additionalProperties: true", 1)
	typed := strings.Replace(base, "additionalProperties: {type: string}", "additionalProperties: {type: string}", 1)
	require.Equal(t, []string{"type-changed spec.labels"}, rules(compare(t, free, typed)))

	require.Empty(t, compare(t, free, free))
	require.Empty(t, compare(t, typed, free), "typed values that become free-form break nothing")
	removed := strings.Replace(base, "  labels:\n    type: object\n    additionalProperties: {type: string}\n", "", 1)
	require.Equal(t, []string{"field-removed spec.labels"}, rules(compare(t, base, removed)))
}
