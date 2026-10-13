package upgrade

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

// populate makes a value that satisfies a schema and has every field in it set, so that an object built from it
// notices when an upgrade drops any one of them. It does not try to be clever about what a field means: the
// schema's own enum, bounds and pattern are the only guide, and a schema that none of its guesses satisfies
// returns ok false, which the caller treats as "this object cannot be generated".
func populate(s *apiextensionsv1.JSONSchemaProps, depth int) (value any, ok bool) {
	if depth > 12 {
		return nil, false
	}
	if len(s.Enum) > 0 {
		var v any
		if err := jsonUnmarshal(s.Enum[0].Raw, &v); err != nil {
			return nil, false
		}
		return v, true
	}
	if s.XIntOrString {
		return int64(1), true
	}
	switch s.Type {
	case "string":
		return populateString(s)
	case "integer":
		return populateNumber(s, true), true
	case "number":
		return populateNumber(s, false), true
	case "boolean":
		return true, true
	case "array":
		if s.Items == nil || s.Items.Schema == nil {
			return []any{}, true
		}
		item, ok := populate(s.Items.Schema, depth+1)
		if !ok {
			return nil, false
		}
		return []any{item}, true
	case "object", "":
		return populateObject(s, depth)
	}
	return nil, false
}

func populateObject(s *apiextensionsv1.JSONSchemaProps, depth int) (any, bool) {
	obj := map[string]any{}
	names := make([]string, 0, len(s.Properties))
	for name := range s.Properties {
		names = append(names, name)
	}
	slices.Sort(names)
	required := map[string]bool{}
	for _, name := range s.Required {
		required[name] = true
	}
	for _, name := range names {
		prop := s.Properties[name]
		v, ok := populate(&prop, depth+1)
		if !ok {
			if required[name] {
				return nil, false
			}
			continue
		}
		obj[name] = v
	}
	if s.AdditionalProperties != nil && s.AdditionalProperties.Schema != nil && len(s.Properties) == 0 {
		v, ok := populate(s.AdditionalProperties.Schema, depth+1)
		if !ok {
			return nil, false
		}
		obj["k"] = v
	}
	if s.XPreserveUnknownFields != nil && *s.XPreserveUnknownFields && len(s.Properties) == 0 {
		obj["unknown"] = "kept"
	}
	return obj, true
}

// stringGuesses are tried in order against a string's pattern and length bounds.
var stringGuesses = []string{
	"a", "abc", "a1", "1", "10", "A", "aaaaaaaa", "a.b", "a-b-c", "/a", "a@b.co", "abc.example.com", "a/b", "a=b", "1Gi", "A_B", "a:b",
}

func populateString(s *apiextensionsv1.JSONSchemaProps) (any, bool) {
	if s.Format == "date-time" {
		return "2026-01-02T03:04:05Z", true
	}
	var pattern *regexp.Regexp
	if s.Pattern != "" {
		var err error
		if pattern, err = regexp.Compile(s.Pattern); err != nil {
			return nil, false
		}
	}
	fits := func(v string) bool {
		if s.MinLength != nil && int64(len(v)) < *s.MinLength {
			return false
		}
		if s.MaxLength != nil && int64(len(v)) > *s.MaxLength {
			return false
		}
		return pattern == nil || pattern.MatchString(v)
	}
	guesses := append([]string{}, stringGuesses...)
	if s.MinLength != nil && *s.MinLength > 1 {
		guesses = append(guesses, strings.Repeat("a", int(*s.MinLength)))
	}
	for _, g := range guesses {
		if fits(g) {
			return g, true
		}
	}
	return nil, false
}

func populateNumber(s *apiextensionsv1.JSONSchemaProps, integer bool) any {
	v := 1.0
	if s.Minimum != nil && v < *s.Minimum {
		v = *s.Minimum
	}
	if s.Maximum != nil && v > *s.Maximum {
		v = *s.Maximum
	}
	if integer {
		return int64(v)
	}
	return v
}

// lost reports the first value in want that is not in got, as a path, or "" when everything that was written is
// still there. Fields that got has beyond want (a default the new schema adds) are not a loss.
func lost(want, got any, path string) string {
	switch w := want.(type) {
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok {
			return path
		}
		keys := make([]string, 0, len(w))
		for k := range w {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			gv, present := g[k]
			if !present {
				return join(path, k)
			}
			if p := lost(w[k], gv, join(path, k)); p != "" {
				return p
			}
		}
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return path
		}
		for i := range w {
			if p := lost(w[i], g[i], fmt.Sprintf("%s[%d]", path, i)); p != "" {
				return p
			}
		}
	default:
		if fmt.Sprint(want) != fmt.Sprint(got) {
			return path
		}
	}
	return ""
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}
