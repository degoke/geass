package dashboard

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// probePath fills a documented path with values that match no resource.
func probePath(path string) string {
	return strings.NewReplacer("{name}", "probe", "{id}", "abcd1234", "{code}", "BBBB-BBBB").Replace("/api/v1" + path)
}

// Every operation the API documents has to be one it answers. A route nobody wired is the likely mistake when
// an operation is added to the list, and it would otherwise sit there promising something that 404s.
func TestEveryDocumentedOperationIsAnswered(t *testing.T) {
	f := newTokenFixture(t)
	token := f.workspaceToken(t, "admin")

	for _, operation := range v1Operations {
		if operation.Stream {
			continue // a stream does not end, and is covered by its own tests
		}
		t.Run(operation.Method+" "+operation.Path, func(t *testing.T) {
			var body any
			if operation.Method != http.MethodGet && operation.Method != http.MethodDelete {
				body = map[string]any{}
			}
			rec := f.do(jsonRequest(token, operation.Method, probePath(operation.Path), body))

			require.NotEqual(t, http.StatusMethodNotAllowed, rec.Code, rec.Body.String())
			if rec.Code >= http.StatusBadRequest {
				message, _ := decodeError(t, rec)
				require.NotEqual(t, "not found", message, "no route answers this; the dashboard's dispatcher said so")
				require.NotContains(t, message, "there is no ", "the adapter has no mapping for this verb")
			}
		})
	}
}

func TestTheOpenAPIDocumentDescribesTheAPI(t *testing.T) {
	f := newTokenFixture(t)
	token := f.workspaceToken(t, "viewer")

	rec := f.do(jsonRequest(token, http.MethodGet, "/api/v1/openapi.json", nil))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var spec struct {
		OpenAPI string `json:"openapi"`
		Paths   map[string]map[string]struct {
			OperationID string           `json:"operationId"`
			Summary     string           `json:"summary"`
			Access      string           `json:"x-access"`
			Parameters  []map[string]any `json:"parameters"`
		} `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &spec))
	require.Equal(t, "3.0.3", spec.OpenAPI)

	ids := map[string]string{}
	operations := 0
	for path, item := range spec.Paths {
		for method, operation := range item {
			operations++
			require.NotEmpty(t, operation.Summary, method+" "+path)
			require.NotEmpty(t, operation.OperationID)
			require.NotContains(t, ids, operation.OperationID, "operation ids are unique")
			ids[operation.OperationID] = path
			for _, match := range pathParameter.FindAllStringSubmatch(path, -1) {
				declared := false
				for _, parameter := range operation.Parameters {
					declared = declared || parameter["name"] == match[1]
				}
				require.True(t, declared, "%s %s declares {%s}", method, path, match[1])
			}
		}
	}
	require.Equal(t, len(v1Operations), operations, "every operation is in the document, once")
	require.Contains(t, spec.Paths, "/apps/{name}/status")
	require.Equal(t, "project", spec.Paths["/apps/{name}/scale"]["post"].Access)
}

func TestNoOperationIsListedTwice(t *testing.T) {
	seen := map[string]bool{}
	for _, operation := range v1Operations {
		key := operation.Method + " " + operation.Path
		require.False(t, seen[key], key)
		seen[key] = true
		require.Contains(t, []string{"public", "viewer", "observer", "project", "admin"}, operation.Access, key)
		require.NotEmpty(t, operation.Summary, key)
	}
}
