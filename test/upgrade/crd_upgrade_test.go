// Package upgrade tests what happens to the objects a cluster has stored when its custom resource definitions are
// replaced by the ones of a newer release: the thing that a cluster update does to its data, run against a real API
// server (envtest). The definitions of the baseline release (hack/crd-baseline-ref, read from git) are installed,
// objects with every field set are stored under them, the current definitions replace them, and nothing that was
// stored may be gone, invalid or unwritable.
//
// pkg/crdcompat finds the same problems by comparing schemas; this finds them by doing it, so the two checks guard
// each other. The tests at the end change the current definitions on purpose, and fail unless both checks notice.
package upgrade

import (
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/degoke/geass/pkg/crdcompat"
)

const namespace = "geass-system"

// stored is an object that was written under the baseline definitions, and what the API server then held.
type stored struct {
	gvk          schema.GroupVersionKind
	name         string
	spec, status any
}

func (s stored) String() string { return s.gvk.Kind + "/" + s.name }

func newObject(gvk schema.GroupVersionKind, name string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{}}
	u.SetGroupVersionKind(gvk)
	u.SetNamespace(namespace)
	u.SetName(name)
	return u
}

// storeObjects writes one object of every kind, with every field the baseline schema has set, and returns what
// the API server holds for each. A kind whose schema cannot be filled in automatically (a validation rule that
// needs particular values) is reported and left out; the test insists that most kinds were covered.
func storeObjects(t *testing.T, c client.Client, crds []apiextensionsv1.CustomResourceDefinition) []stored {
	t.Helper()
	require.NoError(t, c.Create(background(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}))

	var out []stored
	for i := range crds {
		crd := &crds[i]
		var version *apiextensionsv1.CustomResourceDefinitionVersion
		for j := range crd.Spec.Versions {
			if crd.Spec.Versions[j].Storage {
				version = &crd.Spec.Versions[j]
			}
		}
		require.NotNil(t, version, crd.Name)
		gvk := schema.GroupVersionKind{Group: crd.Spec.Group, Version: version.Name, Kind: crd.Spec.Names.Kind}
		root := version.Schema.OpenAPIV3Schema

		obj := newObject(gvk, "upgrade-"+crd.Spec.Names.Singular)
		specSchema, hasSpec := root.Properties["spec"]
		statusSchema, hasStatus := root.Properties["status"]
		if hasSpec {
			spec, ok := populate(&specSchema, 0)
			if !ok {
				t.Logf("not stored: %s has a spec that cannot be filled in from its schema", gvk.Kind)
				continue
			}
			obj.Object["spec"] = spec
		}
		var status any
		if hasStatus {
			status, _ = populate(&statusSchema, 0)
		}
		separateStatus := version.Subresources != nil && version.Subresources.Status != nil
		if status != nil && !separateStatus {
			obj.Object["status"] = status
		}
		if err := createDroppingWhatIsRefused(c, obj); err != nil {
			t.Logf("not stored: %s was refused (%v)", gvk.Kind, firstLine(err))
			continue
		}
		if status != nil && separateStatus {
			obj.Object["status"] = status
			if err := c.Status().Update(background(), obj); err != nil {
				t.Logf("status not stored: %s was refused (%v)", gvk.Kind, firstLine(err))
				delete(obj.Object, "status")
			}
		}
		// What counts is what the API server holds, defaults and all, not what was sent.
		held := newObject(gvk, obj.GetName())
		require.NoError(t, c.Get(background(), client.ObjectKeyFromObject(obj), held))
		out = append(out, stored{gvk: gvk, name: obj.GetName(), spec: held.Object["spec"], status: held.Object["status"]})
	}
	return out
}

// createDroppingWhatIsRefused creates the object, and when the API server refuses a field (a validation rule that
// needs particular values, such as "set one of these two, not both") leaves that field out and tries again. The
// object is then a little less full than it could be, which is better than not testing the kind at all.
func createDroppingWhatIsRefused(c client.Client, obj *unstructured.Unstructured) error {
	var err error
	for range 4 {
		if err = c.Create(background(), obj); err == nil {
			return nil
		}
		var status apierrors.APIStatus
		if !errors.As(err, &status) || status.Status().Details == nil || len(status.Status().Details.Causes) == 0 {
			return err
		}
		dropped := false
		for _, cause := range status.Status().Details.Causes {
			if path := strings.Split(cause.Field, "."); len(path) > 1 {
				if removed, _ := removeField(obj.Object, path); removed {
					dropped = true
				}
			}
		}
		if !dropped {
			return err
		}
		obj.SetResourceVersion("")
	}
	return err
}

// removeField deletes the field at path from a nested object, and says whether it was there.
func removeField(obj map[string]any, path []string) (bool, error) {
	cur := obj
	for _, name := range path[:len(path)-1] {
		next, ok := cur[name].(map[string]any)
		if !ok {
			return false, nil
		}
		cur = next
	}
	last := path[len(path)-1]
	if _, ok := cur[last]; !ok {
		return false, nil
	}
	delete(cur, last)
	return true, nil
}

// toggleABoolean flips the first boolean in the object's spec, a change to the spec that no schema should
// refuse, and says whether there was one to flip. Writing an object unchanged is not enough to find what an
// upgrade breaks: the API server lets an update through when the part that fails validation is not touched.
func toggleABoolean(obj *unstructured.Unstructured) bool {
	var toggle func(v any) bool
	toggle = func(v any) bool {
		switch node := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(node))
			for k := range node {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			for _, k := range keys {
				if b, ok := node[k].(bool); ok {
					node[k] = !b
					return true
				}
				if toggle(node[k]) {
					return true
				}
			}
		case []any:
			if slices.ContainsFunc(node, toggle) {
				return true
			}
		}
		return false
	}
	return toggle(obj.Object["spec"])
}

func firstLine(err error) string {
	return strings.SplitN(err.Error(), "\n", 2)[0]
}

// problems lists what an upgrade did to the stored objects: a field that is gone, an object that can no longer be
// updated, and an object whose content the API server would no longer accept if it were written again.
//
// The last is a check of its own because of validation ratcheting (Kubernetes 1.30 and later): an update that does not
// change a value that fails the new schema is let through, so updating an object in place only finds what is wrong
// with the part of it that the update touches. Writing the same content as a new object (a dry run, so nothing is
// stored) is checked in full, which is what a controller that rewrites the object, or a person who applies it again
// to a new cluster, would meet.
func problems(t *testing.T, c client.Client, objects []stored) []string {
	t.Helper()
	var out []string
	for _, s := range objects {
		got := newObject(s.gvk, s.name)
		if err := c.Get(background(), client.ObjectKey{Namespace: namespace, Name: s.name}, got); err != nil {
			out = append(out, fmt.Sprintf("%s can no longer be read: %v", s, err))
			continue
		}
		if path := lost(s.spec, got.Object["spec"], "spec"); path != "" {
			out = append(out, fmt.Sprintf("%s lost %s", s, path))
		}
		if s.status != nil {
			if path := lost(s.status, got.Object["status"], "status"); path != "" {
				out = append(out, fmt.Sprintf("%s lost %s", s, path))
			}
		}
		// Writing the object back unchanged is the smallest thing any controller or user does to it.
		if err := c.Update(background(), got); err != nil {
			out = append(out, fmt.Sprintf("%s can no longer be updated: %s", s, firstLine(err)))
		}
		if s.status != nil {
			if err := c.Status().Update(background(), got); err != nil && !apierrors.IsNotFound(err) {
				out = append(out, fmt.Sprintf("%s status can no longer be updated: %s", s, firstLine(err)))
			}
		}
		// The same content as a new object is validated whole.
		fresh := newObject(s.gvk, s.name+"-dry-run")
		fresh.Object["spec"] = runtime.DeepCopyJSON(map[string]any{"spec": got.Object["spec"]})["spec"]
		if err := c.Create(background(), fresh, client.DryRunAll); err != nil {
			out = append(out, fmt.Sprintf("%s as it is stored would be refused if it were written again: %s", s, firstLine(err)))
		}
		// And changing it, which is what makes the API server look at the part it touches again.
		// The change is put back, so that the object is what it was when it is looked at again.
		changed := got.DeepCopy()
		if toggleABoolean(changed) {
			if err := c.Update(background(), changed); err != nil {
				out = append(out, fmt.Sprintf("%s can no longer be updated: %s", s, firstLine(err)))
			} else {
				toggleABoolean(changed)
				if err := c.Update(background(), changed); err != nil {
					out = append(out, fmt.Sprintf("%s cannot be put back after a change: %s", s, firstLine(err)))
				}
			}
		}
	}
	slices.Sort(out)
	return out
}

// requireStoredVersionsServed checks that every version that objects are stored in is still served by the
// definition that replaced the old one: a stored version that is not served makes its objects unreadable.
func requireStoredVersionsServed(t *testing.T, c client.Client, crds []apiextensionsv1.CustomResourceDefinition) {
	t.Helper()
	for i := range crds {
		var live apiextensionsv1.CustomResourceDefinition
		require.NoError(t, c.Get(background(), client.ObjectKey{Name: crds[i].Name}, &live))
		served := map[string]bool{}
		for _, v := range live.Spec.Versions {
			served[v.Name] = v.Served
		}
		for _, name := range live.Status.StoredVersions {
			require.True(t, served[name], "%s still has objects stored as %s, which is no longer served", live.Name, name)
		}
	}
}

// withoutAcceptedLosses leaves out the objects of kinds that hack/crd-compat-exceptions.yaml accepts a loss for. The
// exception says a cluster owner may lose something there, so those objects are not expected to come through; they
// are carried again as soon as the exception is removed, which the check that reads the same file insists on once the
// baseline has moved past the change.
func withoutAcceptedLosses(t *testing.T, objects []stored, crds []apiextensionsv1.CustomResourceDefinition) []stored {
	t.Helper()
	ex, err := crdcompat.LoadExceptions(filepath.Join(repoRoot(t), "hack", "crd-compat-exceptions.yaml"))
	require.NoError(t, err)
	excepted := map[string]bool{}
	for _, e := range ex.Exceptions {
		for i := range crds {
			if crds[i].Name == e.CRD {
				excepted[crds[i].Spec.Names.Kind] = true
			}
		}
	}
	kept := objects[:0:0]
	for _, s := range objects {
		if excepted[s.gvk.Kind] {
			t.Logf("not carried: %s, because its definition has a deliberate exception", s)
			continue
		}
		kept = append(kept, s)
	}
	return kept
}

func TestStoredObjectsSurviveTheUpgradeToTheCurrentDefinitions(t *testing.T) {
	baseline, current := baselineCRDs(t), currentCRDs(t)
	cfg, c := startAPIServer(t)

	install(t, cfg, baseline)
	objects := storeObjects(t, c, baseline)
	require.GreaterOrEqual(t, len(objects), len(baseline)*2/3,
		"most kinds must be covered, or the test proves little: the generator needs teaching about a new kind of schema")
	require.Empty(t, problems(t, c, objects), "nothing may go wrong before the upgrade, or the check below is not about the upgrade")
	objects = withoutAcceptedLosses(t, objects, baseline)

	install(t, cfg, current)

	require.Empty(t, problems(t, c, objects),
		"objects stored under the baseline release's definitions lost data, or can no longer be written, under the current ones")
	requireStoredVersionsServed(t, c, current)
	t.Logf("%d of %d kinds stored and carried across", len(objects), len(baseline))
}

// firstStoredField picks a field of a stored spec to damage: the first, so the choice does not depend on map order.
func firstStoredField(t *testing.T, objects []stored) (stored, string) {
	t.Helper()
	for _, s := range objects {
		spec, ok := s.spec.(map[string]any)
		if !ok || len(spec) == 0 {
			continue
		}
		keys := make([]string, 0, len(spec))
		for k := range spec {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		return s, keys[0]
	}
	t.Fatal("no stored object has a spec")
	return stored{}, ""
}

// firstWithABoolean picks a stored object whose spec has a boolean to change.
func firstWithABoolean(t *testing.T, objects []stored) stored {
	t.Helper()
	for _, s := range objects {
		probe := newObject(s.gvk, s.name)
		probe.Object["spec"] = s.spec
		if toggleABoolean(probe) {
			return s
		}
	}
	t.Fatal("no stored object has a boolean in its spec")
	return stored{}
}

func crdFor(t *testing.T, crds []apiextensionsv1.CustomResourceDefinition, kind string) *apiextensionsv1.CustomResourceDefinition {
	t.Helper()
	for i := range crds {
		if crds[i].Spec.Names.Kind == kind {
			return &crds[i]
		}
	}
	t.Fatalf("no definition of %s", kind)
	return nil
}

func specSchema(crd *apiextensionsv1.CustomResourceDefinition) *apiextensionsv1.JSONSchemaProps {
	root := crd.Spec.Versions[0].Schema.OpenAPIV3Schema
	spec := root.Properties["spec"]
	return &spec
}

func setSpecSchema(crd *apiextensionsv1.CustomResourceDefinition, spec *apiextensionsv1.JSONSchemaProps) {
	crd.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["spec"] = *spec
}

// firstNestedObject picks a stored object whose spec has an object inside it, and the name of that field: somewhere below
// the top of the spec, where an update that touches only a boolean elsewhere does not look.
func firstNestedObject(t *testing.T, crds []apiextensionsv1.CustomResourceDefinition, objects []stored) (stored, string) {
	t.Helper()
	for _, s := range objects {
		spec, ok := s.spec.(map[string]any)
		if !ok {
			continue
		}
		crd := crdFor(t, crds, s.gvk.Kind)
		specProps := specSchema(crd)
		keys := make([]string, 0, len(spec))
		for k := range spec {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			prop, has := specProps.Properties[k]
			if _, isMap := spec[k].(map[string]any); has && isMap && prop.Type == "object" && len(prop.Properties) > 0 {
				return s, k
			}
		}
	}
	t.Fatal("no stored object has an object inside its spec")
	return stored{}, ""
}

// The tests below are the check's own check. They break the definitions on purpose, the two ways that matter, and
// the test above passing would mean nothing if these did not fail.

func TestAnUpgradeThatDropsAFieldIsCaughtByBothChecks(t *testing.T) {
	baseline := baselineCRDs(t)
	cfg, c := startAPIServer(t)
	install(t, cfg, baseline)
	objects := storeObjects(t, c, baseline)
	victim, field := firstStoredField(t, objects)

	broken := deepCopyAll(baseline)
	crd := crdFor(t, broken, victim.gvk.Kind)
	spec := specSchema(crd)
	delete(spec.Properties, field)
	setSpecSchema(crd, spec)
	install(t, cfg, broken)

	found := problems(t, c, objects)
	require.Contains(t, strings.Join(found, "\n"), fmt.Sprintf("%s lost spec.%s", victim, field),
		"the live check must see the field pruned from the stored object")

	violations := crdcompat.Compare(baseline, broken)
	require.NotEmpty(t, violations)
	require.Equal(t, crdcompat.RuleFieldRemoved, violations[0].Rule)
	require.Equal(t, "spec."+field, violations[0].Path, "and the schema check must see the same field")
}

func TestAnUpgradeThatRequiresANewFieldIsCaughtByBothChecks(t *testing.T) {
	baseline := baselineCRDs(t)
	cfg, c := startAPIServer(t)
	install(t, cfg, baseline)
	objects := storeObjects(t, c, baseline)
	victim := firstWithABoolean(t, objects)

	broken := deepCopyAll(baseline)
	crd := crdFor(t, broken, victim.gvk.Kind)
	spec := specSchema(crd)
	spec.Properties["mustHave"] = apiextensionsv1.JSONSchemaProps{Type: "string"}
	spec.Required = append(spec.Required, "mustHave")
	setSpecSchema(crd, spec)
	install(t, cfg, broken)

	found := problems(t, c, objects)
	require.Contains(t, strings.Join(found, "\n"), fmt.Sprintf("%s can no longer be updated", victim),
		"the live check must see the stored object refused for lacking the new field")

	violations := crdcompat.Compare(baseline, broken)
	require.NotEmpty(t, violations)
	require.Equal(t, crdcompat.RuleRequiredAdded, violations[0].Rule)
}

func TestAnUpgradeThatRequiresANewFieldDeepInsideASpecIsCaughtByBothChecks(t *testing.T) {
	baseline := baselineCRDs(t)
	cfg, c := startAPIServer(t)
	install(t, cfg, baseline)
	objects := storeObjects(t, c, baseline)
	victim, nested := firstNestedObject(t, baseline, objects)

	broken := deepCopyAll(baseline)
	crd := crdFor(t, broken, victim.gvk.Kind)
	spec := specSchema(crd)
	inner := spec.Properties[nested]
	inner.Properties = map[string]apiextensionsv1.JSONSchemaProps{}
	maps.Copy(inner.Properties, spec.Properties[nested].Properties)
	inner.Properties["mustHaveDeep"] = apiextensionsv1.JSONSchemaProps{Type: "string"}
	inner.Required = append(slices.Clone(inner.Required), "mustHaveDeep")
	spec.Properties[nested] = inner
	setSpecSchema(crd, spec)
	install(t, cfg, broken)

	found := problems(t, c, objects)
	require.Contains(t, strings.Join(found, "\n"), fmt.Sprintf("%s as it is stored would be refused", victim),
		"an update that changes some other field is let through by ratcheting: only writing the whole object again finds this")

	violations := crdcompat.Compare(baseline, broken)
	require.NotEmpty(t, violations)
	require.Equal(t, crdcompat.RuleRequiredAdded, violations[0].Rule)
	require.Equal(t, "spec."+nested+".mustHaveDeep", violations[0].Path)
}
