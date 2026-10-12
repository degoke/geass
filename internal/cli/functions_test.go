package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// functionStub serves one function with the given schedules, and records the changes made to it and the
// calls made to the function itself.
type functionStub struct {
	h       *harness
	patches []map[string]any
	calls   []string
}

func newFunctionStub(t *testing.T, triggersJSON string) *functionStub {
	t.Helper()
	stub := &functionStub{}
	stub.h = newHarness(t, func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		switch {
		case r.URL.Path == "/api/v1/apps/hello" && r.Method == http.MethodGet:
			_, _ = fmt.Fprintf(w, `{"resource":{"kind":"app","name":"hello","url":%q},"spec":{"source":{"function":{"runtime":"node","mode":"event","triggers":%s}}}}`, stub.h.server.URL+"/fn", triggersJSON)
		case r.URL.Path == "/api/v1/apps/hello" && r.Method == http.MethodPatch:
			var body map[string]any
			_ = json.Unmarshal(raw, &body)
			stub.patches = append(stub.patches, body)
			_, _ = w.Write([]byte(`{}`))
		case strings.HasPrefix(r.URL.Path, "/fn"):
			stub.calls = append(stub.calls, r.Method+" "+r.URL.RequestURI()+" "+string(raw))
			if r.URL.Query().Get("fail") != "" {
				http.Error(w, `{"error":"it broke"}`, http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(`{"hello":"world"}`))
		default:
			http.NotFound(w, r)
		}
	})
	stub.h.signedIn(t, "", "")
	return stub
}

func TestAddingATriggerKeepsTheOthersAndReplacesASameNamedOne(t *testing.T) {
	stub := newFunctionStub(t, `[{"name":"nightly","schedule":"0 3 * * *"},{"name":"sync","schedule":"@hourly"}]`)

	code, stdout, stderr := stub.h.run("functions", "triggers", "add", "hello", "sync", "*/15 * * * *", "--path", "/sync", "--payload", `{"full":true}`)
	require.Equal(t, ExitOK, code, stderr)
	require.Contains(t, stdout, "geass deploy hello", "a trigger starts with the next deploy")

	require.Len(t, stub.patches, 1)
	triggers := stub.patches[0]["functionTriggers"].([]any)
	require.Equal(t, []any{
		map[string]any{"name": "nightly", "schedule": "0 3 * * *"},
		map[string]any{"name": "sync", "schedule": "*/15 * * * *", "path": "/sync", "payload": `{"full":true}`},
	}, triggers)
}

func TestRemovingTheLastTriggerSendsAnEmptyValueThatClearsThem(t *testing.T) {
	stub := newFunctionStub(t, `[{"name":"nightly","schedule":"@daily"}]`)

	code, _, stderr := stub.h.run("functions", "triggers", "remove", "hello", "nightly")
	require.Equal(t, ExitOK, code, stderr)
	require.Equal(t, "", stub.patches[0]["functionTriggers"], "an empty list carries no field, so an empty string says clear")

	code, _, _ = stub.h.run("functions", "triggers", "remove", "hello", "ghost")
	require.Equal(t, ExitUsage, code)
	require.Len(t, stub.patches, 1, "nothing is changed for a schedule that does not exist")
}

func TestListingTriggers(t *testing.T) {
	stub := newFunctionStub(t, `[{"name":"nightly","schedule":"0 3 * * *","path":"/run"}]`)
	code, stdout, stderr := stub.h.run("functions", "triggers", "list", "hello")
	require.Equal(t, ExitOK, code, stderr)
	require.Contains(t, stdout, "nightly")
	require.Contains(t, stdout, "0 3 * * *")
	require.Contains(t, stdout, "/run")
}

func TestInvokingAFunctionCallsItsPublicAddress(t *testing.T) {
	stub := newFunctionStub(t, `[]`)

	code, stdout, stderr := stub.h.run("functions", "invoke", "hello")
	require.Equal(t, ExitOK, code, stderr)
	require.Contains(t, stdout, `"hello":"world"`)
	require.Equal(t, "GET /fn/ ", stub.calls[0])

	code, _, _ = stub.h.run("functions", "invoke", "hello", "--path", "/run?x=1", "--data", `{"a":1}`)
	require.Equal(t, ExitOK, code)
	require.Equal(t, `POST /fn/run?x=1 {"a":1}`, stub.calls[1], "a body makes it a POST, and the query is kept")

	code, _, stderr = stub.h.run("functions", "invoke", "hello", "--path", "/?fail=1")
	require.Equal(t, ExitError, code, "a function that answers an error is a failure a script can see")
	require.Contains(t, stderr, "HTTP 500")
}
