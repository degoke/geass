package platform

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
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
}

var functionRuntimes = map[string]FunctionRuntime{
	"bun": {
		ID: "bun", Label: "Bun (TypeScript)", Image: "oven/bun:1-alpine", File: "index.ts",
		Command: []string{"bun", "run", FunctionMountPath + "/index.ts"},
	},
	"node": {
		ID: "node", Label: "Node.js", Image: "node:22-alpine", File: "index.mjs",
		Command: []string{"node", FunctionMountPath + "/index.mjs"},
	},
	"python": {
		ID: "python", Label: "Python", Image: "python:3.12-alpine", File: "main.py",
		Command: []string{"python", "-u", FunctionMountPath + "/main.py"},
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
