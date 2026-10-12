package platform

// An event function does not start a server. It exports a handler, and a small program written once per
// runtime (the shim) serves HTTP, turns each request into an event, calls the handler, and turns what it
// returns into a response. A web request and a scheduled trigger look the same to the handler, and a
// handler that is not a server is the same shape as one on a platform like AWS Lambda.
//
// The event is
//
//	{ method, path, query, headers, body, trigger, triggerName }
//
// where body is the parsed JSON when the request said it was JSON and the text otherwise (null when empty),
// and trigger is "cron" for a call from one of the function's own schedules and "http" for anything else.
//
// A schedule proves itself with a token. Geass makes one secret per function (the GEASS_TRIGGER_TOKEN
// environment variable of its pods) and gives it to the schedule's CronJob, which sends it in the
// x-geass-trigger-token header. The shim compares it in constant time, so only a request that has it is a
// "cron", and a visitor who sends the header with a guess is an "http" like any other. The token and the
// x-geass-trigger-name header are removed from the event's headers, so a handler that logs its headers does
// not leak the token. The token travels inside the cluster as plain HTTP, like all traffic between services.

// What the handler returns is the response: nothing is 204, text is text, an object with a numeric
// statusCode is a response of that status (with its headers and body), and anything else is JSON. A handler
// that throws is a 500 that says so, and the error is logged.

// TriggerTokenEnv is the environment variable that holds a function's schedule token.
const TriggerTokenEnv = "GEASS_TRIGGER_TOKEN"

// FunctionShimFile is the name the shim is mounted under, next to the user's file.
const FunctionShimFile = "geass-shim"

// FunctionShimVersion changes when a shim does, so event functions roll when they should.
const FunctionShimVersion = "3"

const nodeShim = `import crypto from "node:crypto";
import http from "node:http";
import { pathToFileURL } from "node:url";

const secret = Buffer.from(process.env.GEASS_TRIGGER_TOKEN || "");
function fromSchedule(headers) {
  const given = Buffer.from(String(headers["x-geass-trigger-token"] || ""));
  return secret.length > 0 && given.length === secret.length && crypto.timingSafeEqual(given, secret);
}

// The first process in a container has no default action for SIGTERM, so without this a stopping function
// is waited for until it is killed, which makes every sleep and every rollout slower by the grace period.
process.on("SIGTERM", () => process.exit(0));

const mod = await import(pathToFileURL("%ENTRY%").href);
const handler = [mod.handler, mod.default, mod.default && mod.default.handler].find((fn) => typeof fn === "function");
if (!handler) {
  console.error("The function must export a handler: export function handler(event) { ... }");
  process.exit(1);
}

function parseBody(raw, type) {
  if (!raw.length) return null;
  const text = raw.toString("utf8");
  if (/json/i.test(type || "")) {
    try { return JSON.parse(text); } catch { return text; }
  }
  return text;
}

function reply(res, result) {
  if (result === undefined || result === null) {
    res.writeHead(204);
    return res.end();
  }
  let status = 200, headers = {}, body = result;
  if (typeof result === "object" && typeof result.statusCode === "number") {
    status = result.statusCode;
    headers = Object.assign({}, result.headers);
    body = result.body;
  }
  let payload;
  if (body === undefined || body === null) payload = "";
  else if (typeof body === "string" || Buffer.isBuffer(body)) {
    payload = body;
    if (!Object.keys(headers).some((k) => k.toLowerCase() === "content-type")) headers["content-type"] = "text/plain; charset=utf-8";
  } else {
    payload = JSON.stringify(body);
    if (!Object.keys(headers).some((k) => k.toLowerCase() === "content-type")) headers["content-type"] = "application/json";
  }
  res.writeHead(status, headers);
  res.end(payload);
}

http.createServer((req, res) => {
  const chunks = [];
  req.on("data", (chunk) => chunks.push(chunk));
  req.on("end", async () => {
    try {
      const url = new URL(req.url, "http://function");
      const query = Object.create(null);
      for (const [key, value] of url.searchParams) {
        query[key] = key in query ? [].concat(query[key], value) : value;
      }
      const scheduled = fromSchedule(req.headers);
      const headers = Object.assign({}, req.headers);
      delete headers["x-geass-trigger-token"];
      delete headers["x-geass-trigger-name"];
      const event = {
        method: req.method,
        path: url.pathname,
        query,
        headers,
        body: parseBody(Buffer.concat(chunks), req.headers["content-type"]),
        trigger: scheduled ? "cron" : "http",
        triggerName: scheduled ? req.headers["x-geass-trigger-name"] || null : null,
      };
      reply(res, await handler(event));
    } catch (error) {
      console.error(error);
      res.writeHead(500, { "content-type": "application/json" });
      res.end(JSON.stringify({ error: String((error && error.message) || error) }));
    }
  });
}).listen(Number(process.env.PORT) || 8080, () => console.log("Listening on " + (process.env.PORT || 8080)));
`

const bunShim = `import crypto from "node:crypto";

const secret = Buffer.from(process.env.GEASS_TRIGGER_TOKEN || "");
function fromSchedule(headers) {
  const given = Buffer.from(String(headers["x-geass-trigger-token"] || ""));
  return secret.length > 0 && given.length === secret.length && crypto.timingSafeEqual(given, secret);
}

// The first process in a container has no default action for SIGTERM, so without this a stopping function
// is waited for until it is killed, which makes every sleep and every rollout slower by the grace period.
process.on("SIGTERM", () => process.exit(0));

const mod = await import("%ENTRY%");
const handler = [mod.handler, mod.default, mod.default && mod.default.handler].find((fn) => typeof fn === "function");
if (!handler) {
  console.error("The function must export a handler: export function handler(event) { ... }");
  process.exit(1);
}

async function parseBody(request) {
  const text = await request.text();
  if (!text.length) return null;
  if (/json/i.test(request.headers.get("content-type") || "")) {
    try { return JSON.parse(text); } catch { return text; }
  }
  return text;
}

function respond(result) {
  if (result === undefined || result === null) return new Response(null, { status: 204 });
  let status = 200, headers = {}, body = result;
  if (typeof result === "object" && typeof result.statusCode === "number") {
    status = result.statusCode;
    headers = Object.assign({}, result.headers);
    body = result.body;
  }
  const hasType = Object.keys(headers).some((k) => k.toLowerCase() === "content-type");
  if (body === undefined || body === null) return new Response(null, { status, headers });
  if (typeof body === "string") {
    if (!hasType) headers["content-type"] = "text/plain; charset=utf-8";
    return new Response(body, { status, headers });
  }
  if (!hasType) headers["content-type"] = "application/json";
  return new Response(JSON.stringify(body), { status, headers });
}

Bun.serve({
  port: Number(process.env.PORT) || 8080,
  async fetch(request) {
    try {
      const url = new URL(request.url);
      const query = Object.create(null);
      for (const [key, value] of url.searchParams) {
        query[key] = key in query ? [].concat(query[key], value) : value;
      }
      const headers = Object.fromEntries(request.headers);
      const scheduled = fromSchedule(headers);
      const triggerName = headers["x-geass-trigger-name"] || null;
      delete headers["x-geass-trigger-token"];
      delete headers["x-geass-trigger-name"];
      const event = {
        method: request.method,
        path: url.pathname,
        query,
        headers,
        body: await parseBody(request),
        trigger: scheduled ? "cron" : "http",
        triggerName: scheduled ? triggerName : null,
      };
      return respond(await handler(event));
    } catch (error) {
      console.error(error);
      return Response.json({ error: String((error && error.message) || error) }, { status: 500 });
    }
  },
});
console.log("Listening on " + (process.env.PORT || 8080));
`

const pythonShim = `import hmac
import importlib.util
import json
import os
import signal
import sys
import traceback
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qsl, urlsplit

SECRET = os.environ.get("GEASS_TRIGGER_TOKEN", "").encode()

# The first process in a container has no default action for SIGTERM, so without this a stopping function
# is waited for until it is killed, which makes every sleep and every rollout slower by the grace period.
signal.signal(signal.SIGTERM, lambda *_: sys.exit(0))


def from_schedule(headers):
    given = headers.get("x-geass-trigger-token", "").encode()
    return len(SECRET) > 0 and hmac.compare_digest(given, SECRET)


spec = importlib.util.spec_from_file_location("function", "%ENTRY%")
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)
handler = getattr(module, "handler", None)
if not callable(handler):
    print("The function must define a handler: def handler(event): ...", file=sys.stderr, flush=True)
    sys.exit(1)


class Shim(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def _send(self, status, headers, payload):
        self.send_response(status)
        for key, value in headers.items():
            self.send_header(key, str(value))
        self.send_header("content-length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def _reply(self, result):
        if result is None:
            return self._send(204, {}, b"")
        status, headers, body = 200, {}, result
        if isinstance(result, dict) and isinstance(result.get("statusCode"), int):
            status, headers, body = result["statusCode"], dict(result.get("headers") or {}), result.get("body")
        has_type = any(key.lower() == "content-type" for key in headers)
        if body is None:
            payload = b""
        elif isinstance(body, bytes):
            payload = body
        elif isinstance(body, str):
            payload = body.encode()
            if not has_type:
                headers["content-type"] = "text/plain; charset=utf-8"
        else:
            payload = json.dumps(body).encode()
            if not has_type:
                headers["content-type"] = "application/json"
        self._send(status, headers, payload)

    def _handle(self):
        try:
            length = int(self.headers.get("content-length") or 0)
            raw = self.rfile.read(length) if length else b""
            text = raw.decode("utf-8", "replace")
            body = None
            if text:
                body = text
                if "json" in (self.headers.get("content-type") or "").lower():
                    try:
                        body = json.loads(text)
                    except ValueError:
                        pass
            url = urlsplit(self.path)
            query = {}
            for key, value in parse_qsl(url.query, keep_blank_values=True):
                if key in query:
                    query[key] = (query[key] if isinstance(query[key], list) else [query[key]]) + [value]
                else:
                    query[key] = value
            headers = {key.lower(): value for key, value in self.headers.items()}
            scheduled = from_schedule(headers)
            trigger_name = headers.get("x-geass-trigger-name")
            headers.pop("x-geass-trigger-token", None)
            headers.pop("x-geass-trigger-name", None)
            event = {
                "method": self.command,
                "path": url.path,
                "query": query,
                "headers": headers,
                "body": body,
                "trigger": "cron" if scheduled else "http",
                "triggerName": trigger_name if scheduled else None,
            }
            self._reply(handler(event))
        except Exception as error:
            traceback.print_exc()
            self._send(500, {"content-type": "application/json"}, json.dumps({"error": str(error)}).encode())

    do_GET = do_POST = do_PUT = do_PATCH = do_DELETE = do_HEAD = do_OPTIONS = _handle


port = int(os.environ.get("PORT", "8080"))
print("Listening on %d" % port, flush=True)
ThreadingHTTPServer(("0.0.0.0", port), Shim).serve_forever()
`
