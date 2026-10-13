---
title: "Functions"
description: "Run Bun, Node.js or Python code as a service without building an image, as a server or a handler, and call it on a schedule."
---

A **function** is a service whose code you write in the dashboard instead of packaging in an image. You choose a
runtime, edit one source file, and Geass runs it on that runtime's stock container image. The file is either a server
you write yourself or a handler that Geass serves for you, and the function can be called on a schedule. Use a function
for a small webhook receiver, an internal API, a job that runs every night or a quick prototype. For anything that needs
installed packages or several files, build an image and run it as an [app](/docs/guides/apps/).

A function is an ordinary service in every other way: it has a port, a size, replicas, variables, a domain and a
deployment history, and it can [sleep when idle](/docs/guides/scale-to-zero/).

## Before you start

You need a project and an environment. See [Projects and environments](/docs/guides/projects-and-environments/). Only
admins can create a function or read its code.

## Create a function

1. Open the project, pick the environment tab and choose **+ Add resource** → **Function**.
2. Choose a runtime: **Bun (TypeScript)**, **Node.js** or **Python**. Geass saves a draft with starter code and opens
   the **Code** tab.
3. Edit the code. The editor has line numbers, inserts two spaces when you press Tab, and saves on **Cmd+S** or
   **Ctrl+S**. Choose **Save**, or **Discard** to throw away your edits.
4. Choose **Deploy changes** in the top bar. The status goes from **Draft** to **Setting up** to **Ready**.

After a deploy, editing the code and choosing **Save** only stores the new code as a pending change ("Code saved. Deploy
to apply it."). **Deploy changes** applies it and restarts the pods.

A function's card on the canvas shows a function icon, `function` and `Function · Bun (TypeScript)` (or the runtime you
chose) as its source.

**+ Add resource** → **Function** always creates a function in server mode, with a server as the starter code. To write a
handler instead, change the mode as described under [Modes](#modes).

## Runtimes

Geass runs your code on an unmodified public image. It does not build anything for a function.

| Runtime | Image | Source file | Command in server mode |
| --- | --- | --- | --- |
| **Bun (TypeScript)** (`bun`) | `oven/bun:1-alpine` | `index.ts` | `bun run /function/index.ts` |
| **Node.js** (`node`) | `node:22-alpine` | `index.mjs` | `node /function/index.mjs` |
| **Python** (`python`) | `python:3.12-alpine` | `main.py` | `python -u /function/main.py` |

The code is mounted read-only at `/function`. Everything else in the container is what the image ships with. The Node
file is an ES module (`.mjs`), so use `import`, not `require`.

Geass does not install packages. There is no `package.json`, `requirements.txt` or build step. A function can use the
runtime's standard library and whatever else the image already contains.

To change the runtime of an existing function, change **Runtime** under **Settings** → **Source**, or the runtime
selector in the **Code** tab, and save. The code does not change, so rewrite it for the new language.

## Modes

A function runs in one of two modes. Choose the mode with the **Mode** menu in the **Code** tab, then choose **Save**.

| Mode | Menu entry | `mode` | What your file is |
| --- | --- | --- | --- |
| Server | **Server (serves HTTP)** | `http` (the default) | A program that listens on `PORT` and answers requests itself. |
| Handler | **Handler (gets an event)** | `event` | A `handler(event)` function. Geass runs a small server around it that turns each request into an event and the return value into the response. |

Changing the mode does not rewrite your code. Replace the starter server with a handler (or the reverse) before you
deploy. A mode change is a pending change, like a code change.

### Server mode

Your code starts, listens on a port, and answers requests for as long as the pods run. The code must serve HTTP on the
port in the `PORT` environment variable. Geass sets `PORT` to the service's **Port** setting (8080 by default). Listen
on all interfaces. The three starters already do.

The starter code in the editor for each runtime answers every request with a small JSON message:

```ts title="index.ts (Bun starter)"
const port = Number(process.env.PORT) || 8080;

Bun.serve({
  port,
  fetch(request) {
    return Response.json({ message: "Hello from Geass", url: request.url });
  },
});

console.log("Listening on " + port);
```

### Handler mode

You write a function. Geass starts a server for it, calls your function once for each request and answers with what it
returns. You never open a port or parse a request. A web request and a scheduled call reach the handler in the same
shape, so one function can serve both.

```ts title="index.ts (Bun handler)"
export async function handler(event: Record<string, any>) {
  if (event.trigger === "cron") {
    console.log("Scheduled run:", event.triggerName);
    return { ok: true };
  }
  return { message: "Hello from Geass", method: event.method, path: event.path };
}
```

```js title="index.mjs (Node.js handler)"
export async function handler(event) {
  return { message: "Hello from Geass", method: event.method, path: event.path, trigger: event.trigger };
}
```

```python title="main.py (Python handler)"
def handler(event):
    return {"message": "Hello from Geass", "method": event["method"], "path": event["path"], "trigger": event["trigger"]}
```

**Where the handler comes from.** In Bun and Node.js, export a function named `handler`. A default export that is a
function, or a default export object that has a `handler` function, also works. The handler may be `async`. In Python,
define a plain `def handler(event)`. Do not use `async def`: the shim calls the function and does not wait for it.

**The event.** The handler receives one argument, an object (a dictionary in Python):

| Field | Meaning |
| --- | --- |
| `method` | The HTTP method, such as `GET`. |
| `path` | The request path without the query string, such as `/sync`. |
| `query` | The query parameters as an object. A name that appears more than once holds a list of values. |
| `headers` | The request headers, with lowercase names. Geass removes `x-geass-trigger-token` and `x-geass-trigger-name` from it. |
| `body` | The parsed JSON when the content type says JSON and the text parses. Otherwise the text. `null` (`None` in Python) when the body is empty. |
| `trigger` | `cron` for a call from one of this function's [schedules](#schedules), and `http` for anything else. |
| `triggerName` | The name of the schedule on a `cron` call, and `null` (`None`) otherwise. |

**The response.** What the handler returns becomes the HTTP response:

| The handler returns | The response |
| --- | --- |
| Nothing (`undefined`, `null`, `None`) | `204` with no body. |
| Text | `200` with `text/plain; charset=utf-8`. |
| An object or a list | `200` with the value as JSON. |
| An object with a numeric `statusCode` | That status. `headers` sets response headers and `body` is the body: text is sent as text, anything else as JSON, and nothing as an empty body. A `content-type` header you set is kept. |
| An exception | `500` with `{"error": "<message>"}`. The error is written to the log. |

```js title="A handler that sets the status and a header"
export async function handler(event) {
  if (event.method !== "POST") return { statusCode: 405, body: "Use POST" };
  return { statusCode: 201, headers: { "x-example": "1" }, body: { received: event.body } };
}
```

**The shim.** The server around your handler is a short program Geass keeps for each runtime. Geass mounts it next to
your file as `geass-shim.ts`, `geass-shim.mjs` or `geass-shim.py`, and runs it in place of your file. The shim imports
your file, so a syntax error in your code stops the pod with the error in its log. When the file has no handler, the
pod exits with `The function must export a handler: export function handler(event) { ... }` (Python: `The function must
define a handler: def handler(event): ...`).

## How a function runs

- **Invocation.** Anything that can send an HTTP request invokes the function: a browser, a webhook, another service,
  one of its schedules. Give it a **Custom domain** under **Settings** → **Networking** to reach it from the internet.
  Other services in the project reach it at `<service ID>.<namespace>.svc.cluster.local:<port>`. See
  [Domains and TLS](/docs/guides/domains-and-tls/).
- **Always on, or asleep.** The pods run all the time unless you turn on **Sleep when idle** under **Settings** →
  **Scale**. A function that sleeps starts again on the next request to its public host, and a schedule wakes it too. See
  [Sleep when idle](/docs/guides/scale-to-zero/).
- **Other triggers.** A function has no queue or event-bus trigger. The ways to call it are an HTTP request and a
  [schedule](#schedules).
- **No Kubernetes access.** Function pods do not get the Kubernetes service account credentials.

## Schedules

A schedule calls the function at times you choose. Use it for a nightly report, a cleanup every 15 minutes, or a
reminder. Each run is an HTTP request to the function, so the function needs nothing special to be scheduled.

### Add a schedule

1. Open the function and choose the **Code** tab. **Schedules** is under the editor.
2. Choose **Add schedule**. Geass adds a row named `schedule-1` that runs at minute 0 of every hour (`0 * * * *`).
3. Fill in the row: the name (for example `nightly`), the cron schedule in UTC (for example `0 3 * * *`) and the path to
   call (for example `/sync`). Leave the path empty for `/`.
4. Choose **Save schedules**. A toast says "Schedules saved. Deploy to start them."
5. Choose **Deploy changes** in the top bar.

A schedule is saved as a pending change and starts with the next deploy. Removing one works the same way: choose
**Remove**, **Save schedules**, then deploy. A function can have 8 schedules. The dashboard has no field for a payload.
A payload set another way is kept when you save.

| Field | Meaning | Default |
| --- | --- | --- |
| `name` | Names the schedule and its CronJob. Lowercase letters, digits and dashes, 1 to 32 characters, unique in the function. | None |
| `schedule` | A cron expression in **UTC** with five fields (minute, hour, day of the month, month, day of the week), or `@hourly`, `@daily`, `@weekly`, `@monthly` or `@yearly`. Numbers, ranges (`1-5`), steps (`*/15`) and lists (`1,15`) work. Names such as `MON` do not. | None |
| `path` | The path that is called. It starts with `/` and is at most 256 characters. | `/` |
| `payload` | The JSON body of the request. Valid JSON, at most 4096 characters. | `{"trigger":"cron","name":"<name>"}` |

Some examples: `*/15 * * * *` every 15 minutes, `0 3 * * *` every day at 03:00 UTC, `30 8 * * 1-5` weekdays at 08:30
UTC.

### What a run does

For each schedule Geass makes one Kubernetes CronJob in the environment's namespace. At each time it fires, a short Job
sends `POST <path>` to the function with the payload as the JSON body.

- A run succeeds when the function answers with a status below 400. It fails on `400` or above, or when there is no answer
  within 120 seconds. A cold start counts toward the 120 seconds.
- A failed run is tried once more. A run that is still going when the next one is due is not doubled, and a run that
  could not start within 5 minutes of its time is skipped, not run late.
- When the function sleeps when idle and can (it has a public host and the serverless add-on is on), the run goes through
  the same proxy as a visitor and wakes it first. Otherwise the run goes straight to the service.
- Geass keeps the last successful Job and the last three failed ones.
- A [pull request environment](/docs/guides/pull-request-environments/) does not copy a function's schedules, so a copy
  does not repeat the work once per open pull request.

### Tell a schedule from a visitor

Every function that is in handler mode or has schedules gets a secret token. The pods read it as the environment variable
`GEASS_TRIGGER_TOKEN`, and every run sends it in the header `x-geass-trigger-token`, with the schedule's name in
`x-geass-trigger-name`.

In handler mode the shim compares the header with the variable in constant time. A request that carries the token is
reported as `trigger: "cron"`. A visitor who sends the header with a wrong value is an `http` call, so a handler can
trust `event.trigger`.

In server mode nothing checks the header for you. Compare it yourself when your code must tell a run from a visitor:

```js title="Server mode: recognise a scheduled call (Node.js)"
import crypto from "node:crypto";

const secret = Buffer.from(process.env.GEASS_TRIGGER_TOKEN ?? "");

export function isScheduled(req) {
  const given = Buffer.from(String(req.headers["x-geass-trigger-token"] ?? ""));
  return secret.length > 0 && given.length === secret.length && crypto.timingSafeEqual(given, secret);
}
```

The token is made once, kept in the Secret `<service ID>-trigger` (key `token`) in the environment's namespace, and removed
when the function is neither in handler mode nor scheduled. It travels inside the cluster as plain HTTP, like the rest of
the traffic between services.

### From the command line, the API or an agent

```bash
geass apps create report -p proj-1a2b3c4d -e production --function report.mjs --runtime node --mode event
geass functions triggers add report nightly "0 3 * * *"
geass functions triggers add report sync "*/15 * * * *" --path /sync --payload '{"full":true}'
geass deploy report --wait
geass functions invoke report --path /sync --data '{"full":true}'
```

`geass functions triggers add` replaces the schedule that has the same name, and the schedule starts at the next deploy.
`geass functions invoke` calls the function at its public address, as a visitor would, and prints the answer. It needs the
function to have a domain. An agent uses the
`set_function_trigger`, `remove_function_trigger` and `invoke_function` tools. See [Command line](/docs/guides/command-line/),
[MCP server](/docs/guides/mcp-server/), [CLI commands](/docs/reference/cli/) and [MCP tools](/docs/reference/mcp-tools/).

Through the REST API, `POST /api/apps/{id}/update` takes `functionMode`, `functionRuntime`, `functionCode` and
`functionTriggers`, and `POST /api/apps/create` takes `source=function` with `runtime`, `mode`, `code` and
`functionTriggers`. `functionTriggers` is a JSON array of schedules, or one JSON object. An empty value removes every
schedule. When you create a function with `mode=event` and no code, Geass fills in a starter handler.

```bash title="Set the schedules of a function"
curl -X POST https://geass.example.com/api/apps/report/update \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  --data-urlencode 'functionTriggers=[{"name":"nightly","schedule":"0 3 * * *","path":"/nightly"}]'
```

## Limits

| Limit | Value |
| --- | --- |
| Code size | 256 KB (262,144 bytes). |
| Size once saved with the service | The code, with quotes and line breaks escaped, must stay under 200 KB. Geass stores the whole service, code included, with every deploy, and Kubernetes limits that. |
| Files | One. The function is a single source file. |
| Runtimes | `bun`, `node` and `python`. |
| Modes | `http` and `event`. |
| Schedules | 8 per function. |
| Size | The same choices as an app: CPU 0.1, 0.25, 0.5, 1 or 2, memory 128 MB to 4 GB. Default 0.1 CPU and 128 MB. |
| Replicas and autoscaling | The same as an app. See [Apps](/docs/guides/apps/#scale). |

Saving code that breaks a limit is refused with a message such as "function code must be under 256 KB" or "function
code is too large to deploy: it must stay under 200 KB once saved with the service". A schedule that breaks a rule is
refused with a message such as `trigger "nightly": the minute field must be between 0 and 59`, `trigger "nightly": the
path must start with /` or `a function can have at most 8 triggers`.

## Environment variables and secrets

Use the **Variables** tab, the same as for an app. Variables and secrets arrive as environment variables:

```ts title="Read a variable in Bun or Node"
const greeting = process.env.GREETING ?? "Hello";
```

```python title="Read a variable in Python"
import os
greeting = os.environ.get("GREETING", "Hello")
```

A change to a variable applies on the next deploy. A variable named `PORT` that you add on the **Variables** tab does
not replace the `PORT` Geass sets: change the **Port** setting instead. Geass also sets `GEASS_TRIGGER_TOKEN` on a
function in handler mode or with schedules. To give a function a database connection, use **Add Reference**; the
built-in **Function with Postgres** [template](/docs/guides/templates/) does this and reads `DATABASE_URL` with Bun's
built-in SQL client. See [Environment variables and secrets](/docs/guides/environment-variables/).

## How functions differ from apps

| | Function | App |
| --- | --- | --- |
| What you provide | One source file. | An image, or a repository with a Dockerfile. |
| Build | None. Runs on a stock image. | None for an image. A Kaniko build for a repository. |
| Packages | Only what the image has. | Whatever your image contains. |
| Where the code is edited | **Code** tab. | In your repository or image. |
| Schedules | Yes, up to 8. | No. |
| Rollback | Not available. Restore the earlier code and deploy again. | Available through the API for image services. |
| Port | `PORT` is always set from the **Port** setting. | Geass does not set `PORT`. |
| Everything else | Same: size, replicas, autoscaling, sleep when idle, domain, variables, console, metrics, history. | |

Because the code is part of the service, every deploy records it in the deployment history as `Deploy function (bun)`.
**Pending changes** shows `Function code updated` when the code changed, and `Source: function (bun) → function (node)`
when the runtime changed. A change to the mode or the schedules on its own shows `Settings updated — deploy to apply`.

## Logs, console and metrics

- **Deployments** → **View logs** → **Deploy Logs** shows the last 400 lines of every running pod of the active
  deployment.
- The **Console** tab opens a shell (`sh`) in a running pod for five minutes. The image is Alpine-based.
- **Metrics** shows the service's charts.

See [Observability](/docs/guides/observability/).

## Manifest

A function is a `GeassApp` with `spec.source.function`:

```yaml title="hello.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassApp
metadata:
  name: hello-fn
  namespace: geass-system
spec:
  project: proj-1a2b3c4d
  environment: production
  port: 8080
  source:
    function:
      runtime: bun
      code: |
        Bun.serve({
          port: Number(process.env.PORT) || 8080,
          fetch: () => Response.json({ ok: true }),
        });
  deploy:
    enabled: true
```

`kubectl apply -f hello.yaml` creates and runs it. A handler with two schedules:

```yaml title="report.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassApp
metadata:
  name: report
  namespace: geass-system
spec:
  project: proj-1a2b3c4d
  environment: production
  source:
    function:
      runtime: node
      mode: event
      code: |
        export async function handler(event) {
          if (event.trigger === "cron") console.log("Run", event.triggerName, event.body);
          return { ok: true, path: event.path };
        }
      triggers:
        - name: nightly
          schedule: "0 3 * * *"
        - name: sync
          schedule: "*/15 * * * *"
          path: /sync
          payload: '{"full":true}'
  deploy:
    enabled: true
```

Without `mode`, a function is in server mode. See [Apps](/docs/guides/apps/#manifest-and-api) for what changes when you
manage a service by manifest, and [GeassApp](/docs/reference/resources/geass-app/) for every field.

## What happens under the hood

- The code is stored in a ConfigMap named `<service ID>-function` in the environment's namespace, under the key
  `index.ts`, `index.mjs` or `main.py`. A function in handler mode also has the shim in the same ConfigMap, as
  `geass-shim.ts`, `geass-shim.mjs` or `geass-shim.py`.
- The Deployment mounts that ConfigMap read-only at `/function` and runs the runtime command on it (the shim in handler
  mode). The pod template carries a hash of the runtime, the mode and the code, so a change to any of them rolls the pods.
- `PORT` is added to the container's environment from `spec.port`. `automountServiceAccountToken` is `false`.
- For a function in handler mode or with schedules, Geass makes the Secret `<service ID>-trigger` and gives the container
  `GEASS_TRIGGER_TOKEN` from it. The pod template carries a fingerprint of the token, so a token that is made again rolls
  the pods.
- Each schedule is a CronJob named `<service ID>-<schedule name>-<8 characters>` (at most 52 characters), labelled
  `geass.dev/function-trigger=<name>`, with the time zone `Etc/UTC`. Its Job runs `curl` from `curlimages/curl:8.10.1`
  on the nodes the project's builds and jobs use.

```bash title="Inspect a function"
kubectl -n proj-1a2b3c4d-production get configmap hello-fn-function -o yaml
kubectl -n proj-1a2b3c4d-production exec deploy/hello-fn -- ls -l /function
kubectl -n proj-1a2b3c4d-production logs deploy/hello-fn -c app --tail=50
```

```bash title="Inspect the schedules of a function"
kubectl -n proj-1a2b3c4d-production get cronjobs -l geass.dev/name=report
kubectl -n proj-1a2b3c4d-production get jobs -l geass.dev/function-trigger=nightly
kubectl -n proj-1a2b3c4d-production logs job/<job name>
```

## Troubleshooting

**Saving fails with "function code is too large to deploy".** The escaped code is over 200 KB. Remove large embedded
data, or move to an [app](/docs/guides/apps/) with an image.

**The pod restarts in a loop (`CrashLoopBackOff`).** The process exits. Open **Deployments** → **View logs** → **Deploy
Logs**: a syntax error, a missing module or an uncaught exception is printed there. In handler mode, `The function must
export a handler` means the file has no `handler` export.

**The status is Unavailable, then Failed, and the condition says "The service cannot start: app is CrashLoopBackOff".**
Geass read the reason from the pod. Fix the code, then deploy again. See [Apps](/docs/guides/apps/#troubleshooting).

**The domain returns `502` or `503`.** In server mode, the code is not listening on `PORT`, or listens only on `localhost`.
Read the port from the environment and bind all interfaces. A handler does not open a port: check the pod's log.

**`Cannot find module` or `ModuleNotFoundError`.** The package is not in the image and Geass does not install packages.
Use the standard library, or build an image that contains the package and run it as an app.

**`require is not defined` on Node.js.** The file is an ES module. Use `import http from "node:http"`.

**A Python handler answers `500` with a JSON error about a coroutine.** The handler is `async def`. Use a plain `def`.

**My edit is not live.** Code changes wait for **Deploy changes**. The pending list shows `Function code updated` until
you deploy.

**A schedule never runs.** Schedules start with the next deploy after you save them, so check that the function is
deployed and the pending list is empty. Then look for the CronJob with `kubectl -n <namespace> get cronjobs -l
geass.dev/name=<service ID>`. A function that is a draft has none.

**A schedule runs but the function does nothing, or the Job fails.** Read the Job: `kubectl -n <namespace> get jobs -l
geass.dev/function-trigger=<name>` and `kubectl -n <namespace> logs job/<job name>`. `curl` fails when the function answers
with `400` or above (`The requested URL returned error: 404` means the **Path** is wrong), or when it does not answer
within 120 seconds.

**The handler sees `trigger: "http"` for a scheduled run.** The pods and the schedule disagree about the token. Deploy the
function again so the pods restart with the current token. A request that does not carry the right
`x-geass-trigger-token` is always reported as `http`.

**A schedule fires at the wrong hour.** The schedule is in UTC. Convert your local time.

**"functions cannot be rolled back; restore the earlier code and deploy again".** A function has no rollback. Paste
the earlier code into the editor, save and deploy.

**I cannot open the Code tab.** Read-only users cannot view function code ("Your viewer role cannot view function
code").

## Related

- [Apps](/docs/guides/apps/)
- [Sleep when idle](/docs/guides/scale-to-zero/)
- [Environment variables and secrets](/docs/guides/environment-variables/)
- [Templates](/docs/guides/templates/): **Hello function** and **Function with Postgres**, and templates that carry a
  function's mode and schedules.
- [Domains and TLS](/docs/guides/domains-and-tls/)
- [Command line](/docs/guides/command-line/) and [MCP server](/docs/guides/mcp-server/)
- [GeassApp](/docs/reference/resources/geass-app/)
