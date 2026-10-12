package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

const testToken = "s3cr3t-token-for-tests"

const jsHandler = `export async function handler(event) {
  switch (event.path) {
    case "/echo": return event;
    case "/text": return "plain words";
    case "/status": return { statusCode: 201, headers: { "x-extra": "yes" }, body: { ok: true } };
    case "/none": return;
    case "/boom": throw new Error("it broke");
    default: return { path: event.path };
  }
}
`

const pyHandler = `def handler(event):
    path = event["path"]
    if path == "/echo":
        return event
    if path == "/text":
        return "plain words"
    if path == "/status":
        return {"statusCode": 201, "headers": {"x-extra": "yes"}, "body": {"ok": True}}
    if path == "/none":
        return None
    if path == "/boom":
        raise RuntimeError("it broke")
    return {"path": path}
`

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

// startShim runs a function the way its pod does, from a directory that stands in for /function.
func startShim(t *testing.T, runtime, code, binary string, args ...string) string {
	t.Helper()
	base, _ := startShimProcess(t, runtime, code, binary, args...)
	return base
}

func startShimProcess(t *testing.T, runtime, code, binary string, args ...string) (string, *exec.Cmd) {
	t.Helper()
	if _, err := exec.LookPath(binary); err != nil {
		t.Skipf("%s is not installed", binary)
	}
	dir := t.TempDir()
	fn := &geassv1alpha1.GeassAppFunctionSource{Runtime: runtime, Code: code, Mode: FunctionModeEvent}
	files, err := FunctionFiles(fn)
	require.NoError(t, err)
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(strings.ReplaceAll(content, FunctionMountPath, dir)), 0o600))
	}
	command := FunctionCommand(fn)
	shim := filepath.Join(dir, filepath.Base(command[len(command)-1]))
	port := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, binary, append(args, shim)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), fmt.Sprintf("PORT=%d", port), TriggerTokenEnv+"="+testToken)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(base + "/ready"); err == nil {
			_ = resp.Body.Close()
			return base, cmd
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("the function did not start:\n%s", output.String())
	return "", nil
}

func call(t *testing.T, method, url, contentType, body string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	require.NoError(t, err)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, string(raw)
}

func TestEventFunctionsTurnRequestsIntoEventsInEveryRuntime(t *testing.T) {
	for _, tc := range []struct {
		runtime, code, binary string
		args                  []string
	}{
		{"node", jsHandler, "node", nil},
		{"bun", jsHandler, "bun", []string{"run"}},
		{"python", pyHandler, "python3", []string{"-u"}},
	} {
		t.Run(tc.runtime, func(t *testing.T) {
			base := startShim(t, tc.runtime, tc.code, tc.binary, tc.args...)

			resp, body := call(t, "POST", base+"/echo?a=1&b=2&b=3", "application/json", `{"hello":"world"}`)
			require.Equal(t, 200, resp.StatusCode, body)
			require.Contains(t, resp.Header.Get("Content-Type"), "application/json")
			var event map[string]any
			require.NoError(t, json.Unmarshal([]byte(body), &event))
			require.Equal(t, "POST", event["method"])
			require.Equal(t, "/echo", event["path"])
			require.Equal(t, map[string]any{"a": "1", "b": []any{"2", "3"}}, event["query"])
			require.Equal(t, map[string]any{"hello": "world"}, event["body"], "a JSON body arrives parsed")
			require.Equal(t, "http", event["trigger"], "a request without the token is a visitor")
			require.Equal(t, "application/json", event["headers"].(map[string]any)["content-type"])

			_, body = call(t, "POST", base+"/echo", "text/plain", "just text")
			require.NoError(t, json.Unmarshal([]byte(body), &event))
			require.Equal(t, "just text", event["body"])
			_, body = call(t, "GET", base+"/echo?constructor=1&toString=a&toString=b", "", "")
			require.NoError(t, json.Unmarshal([]byte(body), &event))
			require.Equal(t, map[string]any{"constructor": "1", "toString": []any{"a", "b"}}, event["query"], "names that objects inherit are ordinary query names")
			_, body = call(t, "GET", base+"/echo", "", "")
			require.NoError(t, json.Unmarshal([]byte(body), &event))
			require.Nil(t, event["body"], "no body is null")

			resp, body = call(t, "GET", base+"/text", "", "")
			require.Equal(t, 200, resp.StatusCode)
			require.Equal(t, "plain words", body)
			require.Contains(t, resp.Header.Get("Content-Type"), "text/plain")

			resp, body = call(t, "GET", base+"/status", "", "")
			require.Equal(t, 201, resp.StatusCode)
			require.Equal(t, "yes", resp.Header.Get("X-Extra"))
			require.JSONEq(t, `{"ok":true}`, body)

			resp, body = call(t, "GET", base+"/none", "", "")
			require.Equal(t, 204, resp.StatusCode)
			require.Empty(t, body)

			resp, body = call(t, "GET", base+"/boom", "", "")
			require.Equal(t, 500, resp.StatusCode)
			require.Contains(t, body, "it broke")

			// A function that threw is still serving.
			resp, _ = call(t, "GET", base+"/text", "", "")
			require.Equal(t, 200, resp.StatusCode)
		})
	}
}

func TestOnlyARequestWithTheTokenIsASchedule(t *testing.T) {
	for _, tc := range []struct {
		runtime, code, binary string
		args                  []string
	}{
		{"node", jsHandler, "node", nil},
		{"bun", jsHandler, "bun", []string{"run"}},
		{"python", pyHandler, "python3", []string{"-u"}},
	} {
		t.Run(tc.runtime, func(t *testing.T) {
			base := startShim(t, tc.runtime, tc.code, tc.binary, tc.args...)
			ask := func(headers map[string]string) map[string]any {
				req, err := http.NewRequest("GET", base+"/echo", nil)
				require.NoError(t, err)
				for key, value := range headers {
					req.Header.Set(key, value)
				}
				resp, err := http.DefaultClient.Do(req)
				require.NoError(t, err)
				defer func() { _ = resp.Body.Close() }()
				var event map[string]any
				require.NoError(t, json.NewDecoder(resp.Body).Decode(&event))
				return event
			}

			event := ask(map[string]string{"x-geass-trigger-token": testToken, "x-geass-trigger-name": "nightly"})
			require.Equal(t, "cron", event["trigger"])
			require.Equal(t, "nightly", event["triggerName"])
			headers := event["headers"].(map[string]any)
			require.NotContains(t, headers, "x-geass-trigger-token", "a handler that logs its headers does not leak the token")
			require.NotContains(t, headers, "x-geass-trigger-name")

			for name, spoof := range map[string]map[string]string{
				"the old header":  {"x-geass-trigger": "cron"},
				"a guessed token": {"x-geass-trigger-token": "guess", "x-geass-trigger-name": "nightly"},
				"a longer token":  {"x-geass-trigger-token": testToken + "x", "x-geass-trigger-name": "nightly"},
				"a name alone":    {"x-geass-trigger-name": "nightly"},
				"an empty token":  {"x-geass-trigger-token": ""},
			} {
				event = ask(spoof)
				require.Equal(t, "http", event["trigger"], name)
				require.Nil(t, event["triggerName"], name)
			}
		})
	}
}

func TestAShimStopsWhenAskedTo(t *testing.T) {
	for _, tc := range []struct {
		runtime, code, binary string
		args                  []string
	}{
		{"node", jsHandler, "node", nil},
		{"bun", jsHandler, "bun", []string{"run"}},
		{"python", pyHandler, "python3", []string{"-u"}},
	} {
		t.Run(tc.runtime, func(t *testing.T) {
			_, cmd := startShimProcess(t, tc.runtime, tc.code, tc.binary, tc.args...)
			require.NoError(t, cmd.Process.Signal(syscall.SIGTERM))
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case err := <-done:
				// A clean exit, not death by the signal: the shim handles it, which is what a container's first
				// process has to do to stop when it is told to.
				require.NoError(t, err)
			case <-time.After(10 * time.Second):
				t.Fatal("the shim did not stop")
			}
		})
	}
}

func TestWithoutATokenNothingIsASchedule(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	fn := &geassv1alpha1.GeassAppFunctionSource{Runtime: "node", Code: jsHandler, Mode: FunctionModeEvent}
	files, err := FunctionFiles(fn)
	require.NoError(t, err)
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(strings.ReplaceAll(content, FunctionMountPath, dir)), 0o600))
	}
	port := freePort(t)
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "node", filepath.Join(dir, "geass-shim.mjs"))
	cmd.Env = append(os.Environ(), fmt.Sprintf("PORT=%d", port), TriggerTokenEnv+"=")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	for range 100 {
		if resp, err := http.Get(base + "/ready"); err == nil {
			_ = resp.Body.Close()
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	req, _ := http.NewRequest("GET", base+"/echo", nil)
	req.Header.Set("x-geass-trigger-token", "")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	var event map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&event))
	require.Equal(t, "http", event["trigger"], "an empty token must never match an empty header")
}

func TestAnEventFunctionWithoutAHandlerFailsLoudly(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	dir := t.TempDir()
	fn := &geassv1alpha1.GeassAppFunctionSource{Runtime: "node", Code: "export const nothing = 1;", Mode: FunctionModeEvent}
	files, err := FunctionFiles(fn)
	require.NoError(t, err)
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(strings.ReplaceAll(content, FunctionMountPath, dir)), 0o600))
	}
	out, err := exec.Command("node", filepath.Join(dir, "geass-shim.mjs")).CombinedOutput()
	require.Error(t, err, "the pod must crash, not serve nothing")
	require.Contains(t, string(out), "must export a handler")
}

func TestFunctionModes(t *testing.T) {
	httpFn := &geassv1alpha1.GeassAppFunctionSource{Runtime: "bun", Code: "x"}
	eventFn := &geassv1alpha1.GeassAppFunctionSource{Runtime: "bun", Code: "x", Mode: FunctionModeEvent}

	files, err := FunctionFiles(httpFn)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"index.ts": "x"}, files, "an http function is mounted as it always was")
	require.Equal(t, []string{"bun", "run", "/function/index.ts"}, FunctionCommand(httpFn))
	require.Equal(t, FunctionCodeHash("bun", "x"), FunctionPodHash(httpFn), "existing functions do not roll")

	files, err = FunctionFiles(eventFn)
	require.NoError(t, err)
	require.Contains(t, files, "geass-shim.ts")
	require.Contains(t, files["geass-shim.ts"], "/function/index.ts")
	require.Equal(t, []string{"bun", "run", "/function/geass-shim.ts"}, FunctionCommand(eventFn))
	require.NotEqual(t, FunctionPodHash(httpFn), FunctionPodHash(eventFn), "changing the mode rolls the pods")
}

func TestFunctionTriggersAreValidated(t *testing.T) {
	fn := func(triggers ...geassv1alpha1.GeassFunctionTrigger) *geassv1alpha1.GeassAppFunctionSource {
		return &geassv1alpha1.GeassAppFunctionSource{Runtime: "node", Code: "x", Triggers: triggers}
	}
	trigger := func(name, schedule string) geassv1alpha1.GeassFunctionTrigger {
		return geassv1alpha1.GeassFunctionTrigger{Name: name, Schedule: schedule}
	}
	require.NoError(t, ValidateFunctionSource(fn(trigger("nightly", "0 3 * * *"), trigger("often", "*/5 9-17 * * 1-5"), trigger("hourly", "@hourly"))))
	for name, bad := range map[string]*geassv1alpha1.GeassAppFunctionSource{
		"four fields":   fn(trigger("a", "* * * *")),
		"minute 60":     fn(trigger("a", "60 * * * *")),
		"backwards":     fn(trigger("a", "30-10 * * * *")),
		"zero step":     fn(trigger("a", "*/0 * * * *")),
		"day names":     fn(trigger("a", "0 0 * * MON")),
		"bad name":      fn(trigger("Not Valid", "@daily")),
		"duplicate":     fn(trigger("a", "@daily"), trigger("a", "@hourly")),
		"relative path": fn(geassv1alpha1.GeassFunctionTrigger{Name: "a", Schedule: "@daily", Path: "run"}),
		"bad payload":   fn(geassv1alpha1.GeassFunctionTrigger{Name: "a", Schedule: "@daily", Payload: "{nope"}),
		"bad mode":      {Runtime: "node", Code: "x", Mode: "stream"},
	} {
		require.Error(t, ValidateFunctionSource(bad), name)
	}
	many := make([]geassv1alpha1.GeassFunctionTrigger, 0, MaxFunctionTriggers+1)
	for i := range MaxFunctionTriggers + 1 {
		many = append(many, trigger(fmt.Sprintf("t%d", i), "@daily"))
	}
	require.Error(t, ValidateFunctionSource(fn(many...)))
}
