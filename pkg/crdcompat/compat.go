// Package crdcompat decides whether a custom resource definition can replace the one an earlier release shipped
// without losing or invalidating what is already stored.
//
// A cluster that is updated keeps its objects in etcd as the old schema let them be written. The API server
// applies the new schema to them when it reads them back, and that has consequences that nothing else warns
// about: a field the new schema does not have is dropped from the object (and gone for good the next time the
// object is written), a field that is newly required makes every stored object that lacks it fail validation
// on its next update, and a tightened bound does the same to values that were fine yesterday. This package
// finds those changes by comparing the two schemas, so that a release that would do them is caught when it is
// proposed, not when a cluster is updated.
//
// The rules are deliberately stricter than "would this fail today": an additive-only CRD is something that can
// be reasoned about, and a change that is safe only because of what happens to be stored is not.
package crdcompat

import (
	"bytes"
	"cmp"
	"fmt"
	"slices"
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/equality"
)

// The rules a change can break. Each is also what an exception names.
const (
	RuleCRDRemoved             = "crd-removed"
	RuleScopeChanged           = "scope-changed"
	RuleNamesChanged           = "names-changed"
	RuleVersionRemoved         = "version-removed"
	RuleVersionUnserved        = "version-unserved"
	RuleStorageVersionChanged  = "storage-version-changed"
	RuleSubresourceRemoved     = "subresource-removed"
	RuleConversionChanged      = "conversion-changed"
	RuleFieldRemoved           = "field-removed"
	RuleTypeChanged            = "type-changed"
	RuleRequiredAdded          = "required-added"
	RuleEnumRestricted         = "enum-restricted"
	RuleValidationTightened    = "validation-tightened"
	RulePreserveUnknownRemoved = "preserve-unknown-removed"
	RuleListSemanticsChanged   = "list-semantics-changed"
)

// A Violation is one change that is not safe for stored objects.
type Violation struct {
	// CRD is the definition's name, such as geassapps.geass.geass.dev.
	CRD string
	// Version is the API version the change is in, empty for a change to the definition as a whole.
	Version string
	// Path is the field, written as it is in the object: spec.addons.longhorn.enabled. A list's elements are
	// path[] and a map's values path{}. Empty for a change to the definition or to a version as a whole.
	Path string
	// Rule is which rule the change breaks, one of the Rule constants.
	Rule string
	// Message says what changed and what to do instead.
	Message string
}

func (v Violation) String() string {
	where := v.CRD
	if v.Version != "" {
		where += " " + v.Version
	}
	if v.Path != "" {
		where += " " + v.Path
	}
	return fmt.Sprintf("%s [%s]: %s", where, v.Rule, v.Message)
}

// Compare checks every definition in old against its replacement in updated. A definition that is only in
// updated is new and cannot break anything; one that is only in old has been removed, which does.
func Compare(old, updated []apiextensionsv1.CustomResourceDefinition) []Violation {
	byName := map[string]*apiextensionsv1.CustomResourceDefinition{}
	for i := range updated {
		byName[updated[i].Name] = &updated[i]
	}
	var out []Violation
	for i := range old {
		next, ok := byName[old[i].Name]
		if !ok {
			out = append(out, Violation{
				CRD:  old[i].Name,
				Rule: RuleCRDRemoved,
				Message: "the definition is gone, so the API server stops serving every object of this kind that a cluster stores. " +
					"A kind is retired in two releases: first stop using it, and remove it only in a release that cluster owners have been told to update to",
			})
			continue
		}
		out = append(out, CompareCRD(&old[i], next)...)
	}
	sortViolations(out)
	return out
}

func sortViolations(vs []Violation) {
	slices.SortStableFunc(vs, func(a, b Violation) int {
		return cmp.Or(
			strings.Compare(a.CRD, b.CRD),
			strings.Compare(a.Version, b.Version),
			strings.Compare(a.Path, b.Path),
			strings.Compare(a.Rule, b.Rule),
		)
	})
}

// CompareCRD checks one definition against its replacement.
func CompareCRD(old, updated *apiextensionsv1.CustomResourceDefinition) []Violation {
	c := &comparison{crd: old.Name}

	if old.Spec.Scope != updated.Spec.Scope {
		c.add("", "", RuleScopeChanged, fmt.Sprintf("the scope changed from %s to %s; a stored object cannot move between namespaced and cluster scope", old.Spec.Scope, updated.Spec.Scope))
	}
	on, nn := old.Spec.Names, updated.Spec.Names
	if on.Kind != nn.Kind || on.Plural != nn.Plural || on.Singular != nn.Singular || old.Spec.Group != updated.Spec.Group {
		c.add("", "", RuleNamesChanged, fmt.Sprintf("the kind, plural, singular or group changed (%s/%s -> %s/%s); that is a different resource, and what was stored under the old one is not served under the new",
			old.Spec.Group, on.Plural, updated.Spec.Group, nn.Plural))
	}

	newVersions := map[string]*apiextensionsv1.CustomResourceDefinitionVersion{}
	for i := range updated.Spec.Versions {
		newVersions[updated.Spec.Versions[i].Name] = &updated.Spec.Versions[i]
	}
	oldStorage, newStorage := storageVersion(old), storageVersion(updated)
	for i := range old.Spec.Versions {
		ov := &old.Spec.Versions[i]
		nv, ok := newVersions[ov.Name]
		if !ok {
			if ov.Served || ov.Storage {
				c.add(ov.Name, "", RuleVersionRemoved, "the version is gone, but clients and stored objects still use it. Keep serving it until a release that cluster owners have updated to has migrated their objects off it")
			}
			continue
		}
		c.version = ov.Name
		if ov.Served && !nv.Served {
			c.add(ov.Name, "", RuleVersionUnserved, "the version is no longer served, which breaks every client and manifest that still uses it")
		}
		c.compareVersion(ov, nv)
	}
	c.version = ""
	if oldStorage != "" && newStorage != "" && oldStorage != newStorage {
		c.add("", "", RuleStorageVersionChanged, fmt.Sprintf("the version that objects are stored in changed from %s to %s. Existing objects stay in %s until they are rewritten, "+
			"so the old version must stay served, the change needs a conversion if the schemas differ, and a storage migration has to run before the old version can be dropped", oldStorage, newStorage, oldStorage))
	}
	oc, nc := conversionStrategy(old), conversionStrategy(updated)
	if oc != nc && oc == apiextensionsv1.WebhookConverter {
		c.add("", "", RuleConversionChanged, "the conversion webhook was removed, so objects stored in one version can no longer be read as another")
	}
	sortViolations(c.violations)
	return c.violations
}

func storageVersion(crd *apiextensionsv1.CustomResourceDefinition) string {
	for i := range crd.Spec.Versions {
		if crd.Spec.Versions[i].Storage {
			return crd.Spec.Versions[i].Name
		}
	}
	return ""
}

func conversionStrategy(crd *apiextensionsv1.CustomResourceDefinition) apiextensionsv1.ConversionStrategyType {
	if crd.Spec.Conversion == nil || crd.Spec.Conversion.Strategy == "" {
		return apiextensionsv1.NoneConverter
	}
	return crd.Spec.Conversion.Strategy
}

type comparison struct {
	crd        string
	version    string
	violations []Violation
}

func (c *comparison) add(version, path, rule, message string) {
	if version == "" {
		version = c.version
	}
	c.violations = append(c.violations, Violation{CRD: c.crd, Version: version, Path: path, Rule: rule, Message: message})
}

func (c *comparison) compareVersion(old, updated *apiextensionsv1.CustomResourceDefinitionVersion) {
	if old.Subresources != nil {
		if old.Subresources.Status != nil && (updated.Subresources == nil || updated.Subresources.Status == nil) {
			c.add("", "", RuleSubresourceRemoved, "the status subresource was removed: status would be written through the main resource, and writers that update it separately would start overwriting spec")
		}
		if old.Subresources.Scale != nil && (updated.Subresources == nil || updated.Subresources.Scale == nil) {
			c.add("", "", RuleSubresourceRemoved, "the scale subresource was removed, which breaks kubectl scale and every autoscaler that uses it")
		}
	}
	if old.Schema == nil || old.Schema.OpenAPIV3Schema == nil {
		return
	}
	if updated.Schema == nil || updated.Schema.OpenAPIV3Schema == nil {
		c.add("", "", RuleFieldRemoved, "the schema was removed: every field would be pruned from stored objects")
		return
	}
	c.walk("", old.Schema.OpenAPIV3Schema, updated.Schema.OpenAPIV3Schema)
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

// walk compares a schema with the one that replaces it, and the schemas of everything beneath them.
func (c *comparison) walk(path string, old, updated *apiextensionsv1.JSONSchemaProps) {
	if old.Type != updated.Type {
		c.add("", path, RuleTypeChanged, fmt.Sprintf("the type changed from %s to %s; stored values of the old type no longer validate", orNone(old.Type), orNone(updated.Type)))
		// What is under a field whose type changed is not comparable.
		return
	}
	if old.XIntOrString != updated.XIntOrString {
		c.add("", path, RuleTypeChanged, "the int-or-string marker changed, which changes the values that are accepted")
	}
	if isTrue(old.XPreserveUnknownFields) && !isTrue(updated.XPreserveUnknownFields) {
		c.add("", path, RulePreserveUnknownRemoved, "unknown fields used to be kept here and now are pruned, so whatever stored objects hold beyond the schema is lost on their next write")
	}
	if old.Nullable && !updated.Nullable {
		c.add("", path, RuleValidationTightened, "null used to be accepted and no longer is, so a stored null fails validation")
	}
	c.compareEnum(path, old, updated)
	c.compareBounds(path, old, updated)
	c.compareList(path, old, updated)
	c.compareRequired(path, old, updated)
	c.compareCEL(path, old, updated)
	c.compareComposition(path, old, updated)

	// Properties.
	names := make([]string, 0, len(old.Properties))
	for name := range old.Properties {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		oldProp := old.Properties[name]
		newProp, ok := updated.Properties[name]
		p := join(path, name)
		if !ok {
			if isTrue(updated.XPreserveUnknownFields) {
				continue
			}
			c.add("", p, RuleFieldRemoved, "the field is gone, so its value is pruned from every stored object the next time one is written. "+
				"Keep it in the schema (mark it deprecated in its description) and stop using it; to rename or reshape it, add the new field and migrate the data (pkg/migrations)")
			continue
		}
		c.walk(p, &oldProp, &newProp)
	}

	// List elements and map values.
	if old.Items != nil && old.Items.Schema != nil {
		if updated.Items == nil || updated.Items.Schema == nil {
			c.add("", path+"[]", RuleTypeChanged, "the schema of the list's elements was removed")
		} else {
			c.walk(path+"[]", old.Items.Schema, updated.Items.Schema)
		}
	}
	if old.AdditionalProperties != nil {
		switch {
		case old.AdditionalProperties.Schema != nil:
			switch {
			case updated.AdditionalProperties != nil && updated.AdditionalProperties.Schema != nil:
				c.walk(path+"{}", old.AdditionalProperties.Schema, updated.AdditionalProperties.Schema)
			case updated.AdditionalProperties != nil && updated.AdditionalProperties.Allows:
				// Typed values that become free-form accept everything they did.
			default:
				c.add("", path+"{}", RuleFieldRemoved, "values under any key used to be accepted here and no longer are, so any that are stored are pruned")
			}
		case old.AdditionalProperties.Allows:
			switch {
			case updated.AdditionalProperties == nil || !updated.AdditionalProperties.Allows && updated.AdditionalProperties.Schema == nil:
				c.add("", path, RuleFieldRemoved, "arbitrary keys used to be accepted here and no longer are, so any that are stored are pruned")
			case updated.AdditionalProperties.Schema != nil:
				c.add("", path, RuleTypeChanged, "values of any type used to be accepted under any key here and are now typed, "+
					"so a stored value of another type fails validation")
			}
		}
	}
}

func orNone(s string) string {
	if s == "" {
		return "unspecified"
	}
	return s
}

func isTrue(b *bool) bool { return b != nil && *b }

func (c *comparison) compareEnum(path string, old, updated *apiextensionsv1.JSONSchemaProps) {
	if len(updated.Enum) == 0 {
		return
	}
	if len(old.Enum) == 0 {
		c.add("", path, RuleEnumRestricted, "the value used to be free and is now limited to a list, so stored values that are not on it fail validation")
		return
	}
	allowed := map[string]bool{}
	for _, v := range updated.Enum {
		allowed[string(bytes.TrimSpace(v.Raw))] = true
	}
	var dropped []string
	for _, v := range old.Enum {
		if raw := string(bytes.TrimSpace(v.Raw)); !allowed[raw] {
			dropped = append(dropped, raw)
		}
	}
	if len(dropped) > 0 {
		c.add("", path, RuleEnumRestricted, fmt.Sprintf("these values are no longer accepted: %s. Stored objects that hold them fail validation on their next update", strings.Join(dropped, ", ")))
	}
}

func (c *comparison) compareBounds(path string, old, updated *apiextensionsv1.JSONSchemaProps) {
	tighter := func(what string, was, now string) {
		c.add("", path, RuleValidationTightened, fmt.Sprintf("%s was %s and is now %s, which rejects values that were valid", what, was, now))
	}
	// An upper bound is tighter when it appears or is lowered; a lower bound when it appears or is raised.
	upperF := func(what string, was, now *float64) {
		if now != nil && (was == nil || *now < *was) {
			tighter(what, fmtF(was), fmtF(now))
		}
	}
	lowerF := func(what string, was, now *float64) {
		if now != nil && (was == nil || *now > *was) {
			tighter(what, fmtF(was), fmtF(now))
		}
	}
	upperI := func(what string, was, now *int64) {
		if now != nil && (was == nil || *now < *was) {
			tighter(what, fmtI(was), fmtI(now))
		}
	}
	lowerI := func(what string, was, now *int64) {
		if now != nil && (was == nil || *now > *was) {
			tighter(what, fmtI(was), fmtI(now))
		}
	}
	upperF("maximum", old.Maximum, updated.Maximum)
	lowerF("minimum", old.Minimum, updated.Minimum)
	upperI("maxLength", old.MaxLength, updated.MaxLength)
	lowerI("minLength", old.MinLength, updated.MinLength)
	upperI("maxItems", old.MaxItems, updated.MaxItems)
	lowerI("minItems", old.MinItems, updated.MinItems)
	upperI("maxProperties", old.MaxProperties, updated.MaxProperties)
	lowerI("minProperties", old.MinProperties, updated.MinProperties)
	if updated.ExclusiveMaximum && !old.ExclusiveMaximum || updated.ExclusiveMinimum && !old.ExclusiveMinimum {
		tighter("an exclusive bound", "inclusive", "exclusive")
	}
	if updated.UniqueItems && !old.UniqueItems {
		tighter("uniqueItems", "false", "true")
	}
	if updated.MultipleOf != nil && (old.MultipleOf == nil || *old.MultipleOf != *updated.MultipleOf) {
		tighter("multipleOf", fmtF(old.MultipleOf), fmtF(updated.MultipleOf))
	}
	if updated.Pattern != "" && updated.Pattern != old.Pattern {
		tighter("the pattern", fmt.Sprintf("%q", old.Pattern), fmt.Sprintf("%q", updated.Pattern))
	}
	if updated.Format != "" && updated.Format != old.Format {
		tighter("the format", fmt.Sprintf("%q", old.Format), fmt.Sprintf("%q", updated.Format))
	}
}

func fmtF(f *float64) string {
	if f == nil {
		return "unbounded"
	}
	return fmt.Sprintf("%g", *f)
}

func fmtI(i *int64) string {
	if i == nil {
		return "unbounded"
	}
	return fmt.Sprintf("%d", *i)
}

func (c *comparison) compareList(path string, old, updated *apiextensionsv1.JSONSchemaProps) {
	changed := func(a, b *string) bool {
		if a == nil && b == nil {
			return false
		}
		if a == nil || b == nil {
			return true
		}
		return *a != *b
	}
	if changed(old.XListType, updated.XListType) || !equality.Semantic.DeepEqual(old.XListMapKeys, updated.XListMapKeys) ||
		changed(old.XMapType, updated.XMapType) {
		c.add("", path, RuleListSemanticsChanged, "the list or map type, or the keys that identify an element, changed. "+
			"A stored list that has duplicates under the new keys fails validation, and merges (server-side apply) treat elements differently")
	}
}

func (c *comparison) compareRequired(path string, old, updated *apiextensionsv1.JSONSchemaProps) {
	was := map[string]bool{}
	for _, name := range old.Required {
		was[name] = true
	}
	for _, name := range updated.Required {
		if was[name] {
			continue
		}
		// A field with a default is filled in on every read, so a stored object that lacks it still validates.
		if prop, ok := updated.Properties[name]; ok && prop.Default != nil {
			continue
		}
		c.add("", join(path, name), RuleRequiredAdded, "the field is newly required, so every stored object that does not have it fails validation the next time it is updated. "+
			"Make it optional (+optional) and treat the empty value as the default in code, or give it a +kubebuilder:default")
	}
}

func (c *comparison) compareCEL(path string, old, updated *apiextensionsv1.JSONSchemaProps) {
	had := map[string]bool{}
	for _, rule := range old.XValidations {
		had[rule.Rule] = true
	}
	for _, rule := range updated.XValidations {
		if !had[rule.Rule] {
			c.add("", path, RuleValidationTightened, fmt.Sprintf("a validation rule was added or changed: %s. Stored objects that break it can no longer be updated", rule.Rule))
		}
	}
}

func (c *comparison) compareComposition(path string, old, updated *apiextensionsv1.JSONSchemaProps) {
	if !equality.Semantic.DeepEqual(old.AllOf, updated.AllOf) || !equality.Semantic.DeepEqual(old.AnyOf, updated.AnyOf) ||
		!equality.Semantic.DeepEqual(old.OneOf, updated.OneOf) || !equality.Semantic.DeepEqual(old.Not, updated.Not) {
		c.add("", path, RuleValidationTightened, "allOf, anyOf, oneOf or not changed; the compatibility of a composed schema is not checked, so review it by hand")
	}
}
