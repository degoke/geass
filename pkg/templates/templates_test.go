package templates

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEveryBuiltinTemplateIsValid(t *testing.T) {
	list := Builtin()
	require.NotEmpty(t, list)
	ids := map[string]bool{}
	for _, template := range list {
		require.NoError(t, template.Validate(), template.ID)
		require.True(t, template.Builtin, template.ID)
		require.False(t, ids[template.ID], "duplicate id %s", template.ID)
		ids[template.ID] = true
	}
	found, ok := Find("hasura-postgres")
	require.True(t, ok)
	require.Equal(t, "Hasura GraphQL", found.Name)
	_, ok = Find("missing")
	require.False(t, ok)
}

func valid() Template {
	return Template{
		ID: "demo", Name: "Demo",
		Resources: []Resource{
			{Key: "db", Kind: KindDatabase, Engine: "Postgres"},
			{Key: "web", Kind: KindService, Image: "nginx:alpine", Variables: []Variable{{Name: "DATABASE_URL", From: &Reference{Resource: "db", Key: "uri"}}}},
		},
	}
}

func TestValidateRejectsMalformedTemplates(t *testing.T) {
	require.NoError(t, valid().Validate())

	cases := map[string]func(*Template){
		"bad id":                func(t *Template) { t.ID = "Bad ID" },
		"no name":               func(t *Template) { t.Name = " " },
		"no resources":          func(t *Template) { t.Resources = nil },
		"duplicate key":         func(t *Template) { t.Resources[1].Key = "db" },
		"bad key":               func(t *Template) { t.Resources[1].Key = "Web!" },
		"unknown kind":          func(t *Template) { t.Resources[1].Kind = "queue" },
		"database engine":       func(t *Template) { t.Resources[0].Engine = "Oracle" },
		"service with no image": func(t *Template) { t.Resources[1].Image = "" },
		"service with both":     func(t *Template) { t.Resources[1].Repository = "acme/app" },
		"image with spaces":     func(t *Template) { t.Resources[1].Image = "nginx latest" },
		"bad repository":        func(t *Template) { t.Resources[1].Image, t.Resources[1].Repository = "", "not a repo" },
		"function bad runtime": func(t *Template) {
			t.Resources[1] = Resource{Key: "web", Kind: KindFunction, Runtime: "php", Code: "x"}
		},
		"function no code":   func(t *Template) { t.Resources[1] = Resource{Key: "web", Kind: KindFunction, Runtime: "bun"} },
		"database variables": func(t *Template) { t.Resources[0].Variables = []Variable{{Name: "X", Value: "1"}} },
		"bad variable name":  func(t *Template) { t.Resources[1].Variables[0].Name = "1BAD" },
		"duplicate variable": func(t *Template) {
			t.Resources[1].Variables = append(t.Resources[1].Variables, Variable{Name: "DATABASE_URL", Value: "x"})
		},
		"value and from":       func(t *Template) { t.Resources[1].Variables[0].Value = "x" },
		"generate unknown":     func(t *Template) { t.Resources[1].Variables[0] = Variable{Name: "K", Generate: "uuid"} },
		"from missing db":      func(t *Template) { t.Resources[1].Variables[0].From.Resource = "cache" },
		"from non-database":    func(t *Template) { t.Resources[1].Variables[0].From.Resource = "web" },
		"from unknown key":     func(t *Template) { t.Resources[1].Variables[0].From.Key = "token" },
		"port out of range":    func(t *Template) { t.Resources[1].Port = 70000 },
		"too many resources":   func(t *Template) { t.Resources = make([]Resource, MaxResources+1) },
		"oversized value":      func(t *Template) { t.Resources[1].Variables[0] = Variable{Name: "K", Value: strings.Repeat("a", 9000)} },
		"runtime on a service": func(t *Template) { t.Resources[1].Runtime = "bun" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			template := valid()
			mutate(&template)
			require.Error(t, template.Validate())
		})
	}
}

func TestGenerateSecretIsRandomHex(t *testing.T) {
	a, err := GenerateSecret()
	require.NoError(t, err)
	b, err := GenerateSecret()
	require.NoError(t, err)
	require.Len(t, a, 32)
	require.NotEqual(t, a, b)
	require.Regexp(t, "^[0-9a-f]+$", a)
}

func TestSummaryCountsResourceKinds(t *testing.T) {
	require.Equal(t, "1 service, 1 database", valid().Summary())
	hasura, _ := Find("hasura-postgres")
	require.Equal(t, "1 service, 1 database", hasura.Summary())
}
