package platform

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	geassv1alpha1 "github.com/degoke/geass/api/v1alpha1"
)

// The two things a function's code can be.
const (
	// FunctionModeHTTP is a program that serves HTTP itself.
	FunctionModeHTTP = "http"
	// FunctionModeEvent is a handler that Geass serves HTTP for. See function_shims.go.
	FunctionModeEvent = "event"

	// MaxFunctionTriggers mirrors the CRD limit.
	MaxFunctionTriggers = 8
)

// FunctionMountPath is where a function's source file is mounted in its container.
const FunctionMountPath = "/function"

// MaxFunctionCodeBytes mirrors the CRD limit and stays well inside the 1 MiB ConfigMap cap.
const MaxFunctionCodeBytes = 262144

// MaxFunctionSnapshotBytes bounds the JSON-escaped size of function code. Every deploy stores the whole
// spec, code included, in the geass.dev/last-deployed annotation, and Kubernetes rejects objects whose
// annotations total more than 256 KiB, so the code needs to leave room for the rest of the spec.
const MaxFunctionSnapshotBytes = 200 * 1024

// FunctionRuntime describes how a language runtime runs a function.
type FunctionRuntime struct {
	ID    string
	Label string
	Image string
	// File is the file name the code is mounted as.
	File string
	// Command runs the mounted file.
	Command []string
	// shim is the file an event function's server is mounted as, and its source: %ENTRY% is the path of File.
	shimFile, shim string
	// shimCommand runs the shim.
	shimCommand []string
}

var functionRuntimes = map[string]FunctionRuntime{
	"bun": {
		ID: "bun", Label: "Bun (TypeScript)", Image: "oven/bun:1-alpine", File: "index.ts",
		Command:  []string{"bun", "run", FunctionMountPath + "/index.ts"},
		shimFile: FunctionShimFile + ".ts", shim: bunShim, shimCommand: []string{"bun", "run", FunctionMountPath + "/" + FunctionShimFile + ".ts"},
	},
	"node": {
		ID: "node", Label: "Node.js", Image: "node:22-alpine", File: "index.mjs",
		Command:  []string{"node", FunctionMountPath + "/index.mjs"},
		shimFile: FunctionShimFile + ".mjs", shim: nodeShim, shimCommand: []string{"node", FunctionMountPath + "/" + FunctionShimFile + ".mjs"},
	},
	"python": {
		ID: "python", Label: "Python", Image: "python:3.12-alpine", File: "main.py",
		Command:  []string{"python", "-u", FunctionMountPath + "/main.py"},
		shimFile: FunctionShimFile + ".py", shim: pythonShim, shimCommand: []string{"python", "-u", FunctionMountPath + "/" + FunctionShimFile + ".py"},
	},
}

// FunctionRuntimeFor returns the runtime definition for id.
func FunctionRuntimeFor(id string) (FunctionRuntime, bool) {
	runtime, ok := functionRuntimes[id]
	return runtime, ok
}

// FunctionRuntimes lists the supported runtimes in a stable order.
func FunctionRuntimes() []FunctionRuntime {
	out := slices.Collect(maps.Values(functionRuntimes))
	slices.SortFunc(out, func(a, b FunctionRuntime) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// FunctionCodeHash fingerprints code so pods roll when it changes.
func FunctionCodeHash(runtime, code string) string {
	sum := sha256.Sum256([]byte(runtime + "\x00" + code))
	return hex.EncodeToString(sum[:])[:16]
}

// FunctionMode is how a function's code is run, with an empty mode being http.
func FunctionMode(fn *geassv1alpha1.GeassAppFunctionSource) string {
	if fn != nil && fn.Mode == FunctionModeEvent {
		return FunctionModeEvent
	}
	return FunctionModeHTTP
}

// FunctionPodHash fingerprints what the pods run, so they roll when it changes. An http function's hash is
// the one it always had, so this does not restart the functions that are already running.
func FunctionPodHash(fn *geassv1alpha1.GeassAppFunctionSource) string {
	if FunctionMode(fn) == FunctionModeHTTP {
		return FunctionCodeHash(fn.Runtime, fn.Code)
	}
	return FunctionCodeHash(fn.Runtime+"\x00"+FunctionModeEvent+"\x00"+FunctionShimVersion, fn.Code)
}

// FunctionFiles are the files a function's pods mount: its code, and for an event function the program that
// serves it.
func FunctionFiles(fn *geassv1alpha1.GeassAppFunctionSource) (map[string]string, error) {
	runtime, ok := functionRuntimes[fn.Runtime]
	if !ok {
		return nil, fmt.Errorf("unsupported function runtime %q", fn.Runtime)
	}
	files := map[string]string{runtime.File: fn.Code}
	if FunctionMode(fn) == FunctionModeEvent {
		files[runtime.shimFile] = strings.ReplaceAll(runtime.shim, "%ENTRY%", FunctionMountPath+"/"+runtime.File)
	}
	return files, nil
}

// FunctionCommand is what a function's container runs.
func FunctionCommand(fn *geassv1alpha1.GeassAppFunctionSource) []string {
	runtime := functionRuntimes[fn.Runtime]
	if FunctionMode(fn) == FunctionModeEvent {
		return runtime.shimCommand
	}
	return runtime.Command
}

// ValidateFunction checks a function source before it is saved or deployed.
func ValidateFunction(runtime, code string) error {
	if _, ok := functionRuntimes[runtime]; !ok {
		return fmt.Errorf("runtime must be one of bun, node, or python")
	}
	if len(code) == 0 {
		return fmt.Errorf("function code is required")
	}
	if len(code) > MaxFunctionCodeBytes {
		return fmt.Errorf("function code must be under %d KB", MaxFunctionCodeBytes/1024)
	}
	if escaped, err := json.Marshal(code); err != nil || len(escaped) > MaxFunctionSnapshotBytes {
		return fmt.Errorf("function code is too large to deploy: it must stay under %d KB once saved with the service (quotes and line breaks take extra space)", MaxFunctionSnapshotBytes/1024)
	}
	return nil
}

var triggerNamePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,30}[a-z0-9])?$`)

// ValidateFunctionSource checks everything about a function: its code, its mode and its triggers.
func ValidateFunctionSource(fn *geassv1alpha1.GeassAppFunctionSource) error {
	if err := ValidateFunction(fn.Runtime, fn.Code); err != nil {
		return err
	}
	if fn.Mode != "" && fn.Mode != FunctionModeHTTP && fn.Mode != FunctionModeEvent {
		return fmt.Errorf("mode must be http or event")
	}
	if len(fn.Triggers) > MaxFunctionTriggers {
		return fmt.Errorf("a function can have at most %d triggers", MaxFunctionTriggers)
	}
	seen := map[string]bool{}
	for _, trigger := range fn.Triggers {
		if !triggerNamePattern.MatchString(trigger.Name) {
			return fmt.Errorf("trigger name %q must be lowercase letters, numbers and dashes, up to 32 characters", trigger.Name)
		}
		if seen[trigger.Name] {
			return fmt.Errorf("trigger %q is listed twice", trigger.Name)
		}
		seen[trigger.Name] = true
		if err := ValidateCronSchedule(trigger.Schedule); err != nil {
			return fmt.Errorf("trigger %q: %w", trigger.Name, err)
		}
		if trigger.Path != "" && !strings.HasPrefix(trigger.Path, "/") {
			return fmt.Errorf("trigger %q: the path must start with /", trigger.Name)
		}
		if trigger.Payload != "" && !json.Valid([]byte(trigger.Payload)) {
			return fmt.Errorf("trigger %q: the payload must be valid JSON", trigger.Name)
		}
	}
	return nil
}

var cronMacros = []string{"@yearly", "@annually", "@monthly", "@weekly", "@daily", "@midnight", "@hourly"}

// cronFields are the five fields of a cron expression and the values each may take.
var cronFields = []struct {
	name     string
	low, top int
}{{"minute", 0, 59}, {"hour", 0, 23}, {"day of the month", 1, 31}, {"month", 1, 12}, {"day of the week", 0, 7}}

// ValidateCronSchedule checks a schedule the way a CronJob reads it: five fields of numbers, ranges, steps
// and lists, or one of the @ shorthands. Names such as MON are not accepted, so a schedule means the same
// to everyone reading it.
func ValidateCronSchedule(schedule string) error {
	schedule = strings.TrimSpace(schedule)
	if slices.Contains(cronMacros, schedule) {
		return nil
	}
	fields := strings.Fields(schedule)
	if len(fields) != len(cronFields) {
		return fmt.Errorf("the schedule must have five fields (minute hour day month weekday) or be one of @hourly, @daily, @weekly, @monthly, @yearly")
	}
	for i, field := range fields {
		spec := cronFields[i]
		for item := range strings.SplitSeq(field, ",") {
			base, step, hasStep := strings.Cut(item, "/")
			if hasStep {
				if n, err := strconv.Atoi(step); err != nil || n < 1 {
					return fmt.Errorf("the %s field has a step that is not a positive number", spec.name)
				}
			}
			if base == "*" {
				continue
			}
			from, to, isRange := strings.Cut(base, "-")
			low, errLow := strconv.Atoi(from)
			high := low
			var errHigh error
			if isRange {
				high, errHigh = strconv.Atoi(to)
			}
			if errLow != nil || errHigh != nil || low < spec.low || high > spec.top || low > high {
				return fmt.Errorf("the %s field must be between %d and %d", spec.name, spec.low, spec.top)
			}
		}
	}
	return nil
}

// FunctionStarterCodeFor is the example a new function begins with: a server for an http function and a
// handler for an event one.
func FunctionStarterCodeFor(runtime, mode string) string {
	if mode != FunctionModeEvent {
		return FunctionStarterCode(runtime)
	}
	switch runtime {
	case "node":
		return `export async function handler(event) {
  // event: { method, path, query, headers, body, trigger ("http" or "cron"), triggerName }
  return { message: "Hello from Geass", method: event.method, path: event.path, trigger: event.trigger };
}
`
	case "python":
		return `def handler(event):
    # event: {"method", "path", "query", "headers", "body", "trigger" ("http" or "cron"), "triggerName"}
    return {"message": "Hello from Geass", "method": event["method"], "path": event["path"], "trigger": event["trigger"]}
`
	default:
		return `export async function handler(event: Record<string, any>) {
  // event: { method, path, query, headers, body, trigger ("http" or "cron"), triggerName }
  return { message: "Hello from Geass", method: event.method, path: event.path, trigger: event.trigger };
}
`
	}
}

// FunctionStarterCode is the example a new function begins with.
func FunctionStarterCode(runtime string) string {
	switch runtime {
	case "node":
		return `import http from "node:http";

const port = Number(process.env.PORT) || 8080;

http
  .createServer((req, res) => {
    res.setHeader("content-type", "application/json");
    res.end(JSON.stringify({ message: "Hello from Geass", method: req.method, url: req.url }));
  })
  .listen(port, () => console.log("Listening on " + port));
`
	case "python":
		return `import json
import os
from http.server import BaseHTTPRequestHandler, HTTPServer


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        body = json.dumps({"message": "Hello from Geass", "path": self.path}).encode()
        self.send_response(200)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


port = int(os.environ.get("PORT", "8080"))
print(f"Listening on {port}", flush=True)
HTTPServer(("0.0.0.0", port), Handler).serve_forever()
`
	default:
		return `const port = Number(process.env.PORT) || 8080;

Bun.serve({
  port,
  fetch(request) {
    return Response.json({ message: "Hello from Geass", url: request.url });
  },
});

console.log("Listening on " + port);
`
	}
}
