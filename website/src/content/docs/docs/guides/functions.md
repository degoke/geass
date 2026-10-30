---
title: "Functions"
description: "Run Bun, Node.js, or Python code as a service without an image build, as a server or a handler, and call it on a schedule."
---

A **function** is a service whose code you write in the dashboard. You do not package the code in an image. You
select a runtime and edit one source file. Geass runs the file on the stock container image of that runtime. The file
is one of these:

- A server that you write yourself.
- A handler that Geass serves for you.

Geass can call a function on a schedule. Use a function for a small webhook receiver, an internal API, a job that runs
every night, or a quick prototype. If your code needs installed packages or several files, build an image and run it
as an [app](/docs/guides/apps/).

In every other way, a function is an ordinary service. It has a port, a size, replicas, variables, a domain, and a
deployment history. It can also [sleep when idle](/docs/guides/scale-to-zero/).

## Before you start

You need a project and an environment. See [Projects and environments](/docs/guides/projects-and-environments/). Only
admins can create a function or read its code.

## Create a function

1. Open the project, and select the environment tab.
2. Click **+ Add resource** → **Function**.
3. Select a runtime: **Bun (TypeScript)**, **Node.js**, or **Python**. Geass saves a draft with starter code and opens
   the **Code** tab.
4. Edit the code. The editor has line numbers. It inserts two spaces when you press Tab, and it saves on **Cmd+S** or
   **Ctrl+S**.
5. Click **Save**. To remove your edits, click **Discard**.
6. Click **Deploy changes** in the top bar. The status goes from **Draft** to **Setting up** to **Ready**.

After a deploy, when you edit the code and click **Save**, Geass only keeps the new code as a pending change ("Code
saved. Deploy to apply it."). **Deploy changes** applies the code and restarts the pods.

On the canvas, the card of a function shows a function icon and `function`. As its source, the card shows
`Function · Bun (TypeScript)`, or the runtime that you selected.

**+ Add resource** → **Function** always creates a function in server mode, with a server as the starter code. To write
a handler, change the mode as [Modes](#modes) tells you.

## Runtimes

Geass runs your code on an unmodified public image. Geass does not build anything for a function.

| Runtime | Image | Source file | Command in server mode |
| --- | --- | --- | --- |
| **Bun (TypeScript)** (`bun`) | `oven/bun:1-alpine` | `index.ts` | `bun run /function/index.ts` |
| **Node.js** (`node`) | `node:22-alpine` | `index.mjs` | `node /function/index.mjs` |
| **Python** (`python`) | `python:3.12-alpine` | `main.py` | `python -u /function/main.py` |

Geass mounts the code read-only at `/function`. Everything else in the container is what the image contains. The Node
file is an ES module (`.mjs`), so use `import`, not `require`.

Geass does not install packages. There is no `package.json`, no `requirements.txt`, and no build step. A function can
use the standard library of the runtime, and anything else that the image already contains.

To change the runtime of an existing function, change **Runtime** under **Settings** → **Source**, or use the runtime
selector in the **Code** tab. Then save. Geass does not change the code, so rewrite the code for the new language.

## Modes

A function runs in one of two modes. To set the mode, select it in the **Mode** menu in the **Code** tab. Then click
**Save**.

| Mode | Menu entry | `mode` | What your file is |
| --- | --- | --- | --- |
| Server | **Server (serves HTTP)** | `http` (the default) | A program that listens on `PORT` and answers requests itself. |
| Handler | **Handler (gets an event)** | `event` | A `handler(event)` function. Geass runs a small server around it. The server changes each request into an event, and the return value into the response. |

A change of the mode does not rewrite your code. Before you deploy, replace the starter server with a handler, or
replace the handler with a server. A mode change is a pending change, the same as a code change.

### Server mode

Your code starts, listens on a port, and answers requests while the pods run. The code must serve HTTP on the port in
the `PORT` environment variable. Geass sets `PORT` to the service's **Port** setting (8080 by default). Listen on all
interfaces. The three starters already do this.

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

You write a function. Geass starts a server for it. The server calls your function one time for each request, and
answers with the return value. You never open a port or parse a request. A web request and a scheduled call get to
the handler in the same shape, so one function can serve both.

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

**Where the handler comes from.** In Bun and Node.js, export a function with the name `handler`. These exports also
work:

- A default export that is a function.
- A default export object that has a `handler` function.

The handler can be `async`. In Python, define a plain `def handler(event)`. Do not use `async def`. The shim calls the
function and does not wait for it.

**The event.** The handler gets one argument, an object (a dictionary in Python):

| Field | Meaning |
| --- | --- |
| `method` | The HTTP method, for example `GET`. |
| `path` | The request path without the query string, for example `/sync`. |
| `query` | The query parameters as an object. A name that occurs more than one time holds a list of values. |
| `headers` | The request headers, with lowercase names. Geass removes `x-geass-trigger-token` and `x-geass-trigger-name` from them. |
| `body` | The parsed JSON when the content type says JSON and the text parses. Otherwise, the text. `null` (`None` in Python) when the body is empty. |
| `trigger` | `cron` for a call from one of the function's [schedules](#schedules), and `http` for all other calls. |
| `triggerName` | The name of the schedule on a `cron` call, and `null` (`None`) for other calls. |

**The response.** The return value of the handler becomes the HTTP response:

| The handler returns | The response |
| --- | --- |
| Nothing (`undefined`, `null`, `None`) | `204` with no body. |
| Text | `200` with `text/plain; charset=utf-8`. |
| An object or a list | `200` with the value as JSON. |
| An object with a numeric `statusCode` | That status. `headers` sets response headers, and `body` is the body. Text goes as text, any other value as JSON, and nothing as an empty body. Geass keeps a `content-type` header that you set. |
| An exception | `500` with `{"error": "<message>"}`. Geass writes the error to the log. |

```js title="A handler that sets the status and a header"
export async function handler(event) {
  if (event.method !== "POST") return { statusCode: 405, body: "Use POST" };
  return { statusCode: 201, headers: { "x-example": "1" }, body: { received: event.body } };
}
```

**The shim.** The server around your handler is a short program that Geass keeps for each runtime. Geass mounts the
shim next to your file as `geass-shim.ts`, `geass-shim.mjs`, or `geass-shim.py`, and runs it in place of your file.
The shim imports your file. So a syntax error in your code stops the pod, and the pod's log shows the error. If the
file has no handler, the pod exits with
`The function must export a handler: export function handler(event) { ... }`. In Python, the message is
`The function must define a handler: def handler(event): ...`.

## How a function runs

- **Invocation.** Anything that can send an HTTP request invokes the function: a browser, a webhook, another service,
  or one of its schedules. To make the function available from the internet, give it a **Custom domain** under
  **Settings** → **Networking**. Other services in the project get to the function at
  `<service ID>.<namespace>.svc.cluster.local:<port>`. See [Domains and TLS](/docs/guides/domains-and-tls/).
- **Always on, or asleep.** The pods run all the time, unless you turn on **Sleep when idle** under **Settings** →
  **Scale**. A function that sleeps starts again on the next request to its public host. A schedule also wakes it. See
  [Sleep when idle](/docs/guides/scale-to-zero/).
- **Other triggers.** A function has no queue trigger or event-bus trigger. The ways to call a function are an HTTP
  request and a [schedule](#schedules).
- **No Kubernetes access.** Function pods do not get the Kubernetes service account credentials.

## Schedules

A schedule calls the function at the times that you select. Use a schedule for a nightly report, a cleanup every 15
minutes, or a reminder. Each run is an HTTP request to the function, so the function needs nothing special for a
schedule.

### Add a schedule

1. Open the function, and select the **Code** tab. **Schedules** is under the editor.
2. Click **Add schedule**. Geass adds a row with the name `schedule-1`. This schedule runs at minute 0 of every hour
   (`0 * * * *`).
3. In the row, enter these values:
   - The name, for example `nightly`.
   - The cron schedule in UTC, for example `0 3 * * *`.
   - The path to call, for example `/sync`. For `/`, leave the path empty.
4. Click **Save schedules**. A toast shows "Schedules saved". To start the schedules, deploy.
5. Click **Deploy changes** in the top bar.

Geass saves a schedule as a pending change, and the schedule starts with the next deploy. To remove a schedule, do the
same steps: click **Remove**, click **Save schedules**, and then deploy. A function can have 8 schedules. The dashboard
has no field for a payload. If you set a payload another way, Geass keeps it when you save.

| Field | Meaning | Default |
| --- | --- | --- |
| `name` | The name of the schedule and its CronJob. Lowercase letters, digits, and dashes, 1 to 32 characters, unique in the function. | None |
| `schedule` | A cron expression in **UTC** with five fields (minute, hour, day of the month, month, day of the week), or `@hourly`, `@daily`, `@weekly`, `@monthly`, or `@yearly`. Numbers, ranges (`1-5`), steps (`*/15`), and lists (`1,15`) work. Names, for example `MON`, do not work. | None |
| `path` | The path that the schedule calls. It starts with `/` and has 256 characters or fewer. | `/` |
| `payload` | The JSON body of the request. Valid JSON, 4096 characters or fewer. | `{"trigger":"cron","name":"<name>"}` |

These are some examples:

- `*/15 * * * *`: every 15 minutes.
- `0 3 * * *`: every day at 03:00 UTC.
- `30 8 * * 1-5`: weekdays at 08:30 UTC.

### What a run does

For each schedule, Geass makes one Kubernetes CronJob in the environment's namespace. Each time the CronJob fires, a
short Job sends `POST <path>` to the function, with the payload as the JSON body.

- A run succeeds when the function answers with a status below 400. A run fails on `400` or above, or when there is
  no answer within 120 seconds. A cold start counts toward the 120 seconds.
- A failed run gets one more try.
- If a run is still in progress when the next run is due, the next run does not start in parallel.
- If a run cannot start within 5 minutes of its time, the run is skipped. It does not run late.
- The function can sleep when idle if it has a public host and the serverless add-on is on. In that case, the run
  goes through the same proxy as a visitor, and the proxy wakes the function first. In other cases, the run goes
  directly to the service.
- Geass keeps the last successful Job and the last three failed Jobs.
- A [pull request environment](/docs/guides/pull-request-environments/) does not copy the schedules of a function. So
  a copy does not repeat the work one time for each open pull request.

### Tell a schedule from a visitor

Every function that is in handler mode or has schedules gets a secret token:

- The pods read the token as the environment variable `GEASS_TRIGGER_TOKEN`.
- Every run sends the token in the header `x-geass-trigger-token`, with the schedule's name in `x-geass-trigger-name`.

In handler mode, the shim compares the header with the variable in constant time. The shim reports a request that has
the token as `trigger: "cron"`. A visitor who sends the header with a wrong value is an `http` call. So a handler can
trust `event.trigger`.

In server mode, nothing checks the header for you. If your code must tell a run from a visitor, compare the header
yourself:

```js title="Server mode: recognise a scheduled call (Node.js)"
import crypto from "node:crypto";

const secret = Buffer.from(process.env.GEASS_TRIGGER_TOKEN ?? "");

export function isScheduled(req) {
  const given = Buffer.from(String(req.headers["x-geass-trigger-token"] ?? ""));
  return secret.length > 0 && given.length === secret.length && crypto.timingSafeEqual(given, secret);
}
```

Geass makes the token one time, and keeps it in the Secret `<service ID>-trigger` (key `token`) in the environment's
namespace. Geass removes the token when the function is not in handler mode and has no schedules. The token goes
inside the cluster as plain HTTP, the same as the other traffic between services.

### From the command line, the API or an agent

```bash
geass apps create report -p proj-1a2b3c4d -e production --function report.mjs --runtime node --mode event
geass functions triggers add report nightly "0 3 * * *"
geass functions triggers add report sync "*/15 * * * *" --path /sync --payload '{"full":true}'
geass deploy report --wait
geass functions invoke report --path /sync --data '{"full":true}'
```

`geass functions triggers add` replaces the schedule that has the same name. The schedule starts at the next deploy.

`geass functions invoke` calls the function at its public address, as a visitor does, and prints the answer. The
function must have a domain for this command.

An agent uses the `set_function_trigger`, `remove_function_trigger`, and `invoke_function` tools. See
[Command line](/docs/guides/command-line/), [MCP server](/docs/guides/mcp-server/),
[CLI commands](/docs/reference/cli/), and [MCP tools](/docs/reference/mcp-tools/).

The REST API has these routes for functions:

- `POST /api/apps/{id}/update` takes `functionMode`, `functionRuntime`, `functionCode`, and `functionTriggers`.
- `POST /api/apps/create` takes `source=function` with `runtime`, `mode`, `code`, and `functionTriggers`.

`functionTriggers` is a JSON array of schedules, or one JSON object. An empty value removes every schedule. When you
create a function with `mode=event` and no code, Geass fills in a starter handler.

```bash title="Set the schedules of a function"
curl -X POST https://geass.example.com/api/apps/report/update \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  --data-urlencode 'functionTriggers=[{"name":"nightly","schedule":"0 3 * * *","path":"/nightly"}]'
```

## Limits

| Limit | Value |
| --- | --- |
| Code size | 256 KB (262,144 bytes). |
| Size when saved with the service | The code, with quotes and line breaks escaped, must stay under 200 KB. Geass keeps the whole service, code included, with every deploy, and Kubernetes limits that size. |
| Files | One. The function is a single source file. |
| Runtimes | `bun`, `node`, and `python`. |
| Modes | `http` and `event`. |
| Schedules | 8 for each function. |
| Size | The same choices as an app: CPU 0.1, 0.25, 0.5, 1, or 2, and memory 128 MB to 4 GB. The default is 0.1 CPU and 128 MB. |
| Replicas and autoscaling | The same as an app. See [Apps](/docs/guides/apps/#scale). |

If code breaks a limit, Geass refuses to save it. Geass shows a message, for example "function code must be under 256
KB" or "function code is too large to deploy: it must stay under 200 KB once saved with the service".

If a schedule breaks a rule, Geass refuses it with a message, for example these messages:

- `trigger "nightly": the minute field must be between 0 and 59`
- `trigger "nightly": the path must start with /`
- `a function can have at most 8 triggers`

## Environment variables and secrets

Use the **Variables** tab, the same as for an app. Variables and secrets get to the function as environment variables:

```ts title="Read a variable in Bun or Node"
const greeting = process.env.GREETING ?? "Hello";
```

```python title="Read a variable in Python"
import os
greeting = os.environ.get("GREETING", "Hello")
```

A change to a variable applies on the next deploy. A variable with the name `PORT` that you add on the **Variables**
tab does not replace the `PORT` that Geass sets. To change the port, change the **Port** setting. Geass also sets
`GEASS_TRIGGER_TOKEN` on a function in handler mode or with schedules.

To give a function a database connection, use **Add Reference**. The **Function with Postgres**
[example template](/docs/guides/templates/#examples-to-copy) does this. It reads `DATABASE_URL` with the built-in SQL
client of Bun. See [Environment variables and secrets](/docs/guides/environment-variables/).

## How functions differ from apps

| | Function | App |
| --- | --- | --- |
| What you provide | One source file. | An image, or a repository with a Dockerfile. |
| Build | None. Runs on a stock image. | None for an image. A Kaniko build for a repository. |
| Packages | Only what the image has. | All that your image contains. |
| Where you edit the code | **Code** tab. | In your repository or image. |
| Schedules | Yes, up to 8. | No. |
| Rollback | Not available. Restore the earlier code and deploy again. | Available through the API for image services. |
| Port | Geass always sets `PORT` from the **Port** setting. | Geass does not set `PORT`. |
| Everything else | The same: size, replicas, autoscaling, sleep when idle, domain, variables, console, metrics, and history. | |

The code is part of the service. So every deploy records the code in the deployment history as
`Deploy function (bun)`. **Pending changes** shows `Function code updated` when the code changed. It shows
`Source: function (bun) → function (node)` when the runtime changed. A change to only the mode or the schedules shows
`Settings updated`.

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

`kubectl apply -f hello.yaml` creates and runs the function. This example is a handler with two schedules:

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

Without `mode`, a function is in server mode. For what changes when you manage a service by manifest, see
[Apps](/docs/guides/apps/#manifest-and-api). For every field, see [GeassApp](/docs/reference/resources/geass-app/).

## What happens under the hood

- Geass keeps the code in a ConfigMap with the name `<service ID>-function` in the environment's namespace. The key is
  `index.ts`, `index.mjs`, or `main.py`. For a function in handler mode, the same ConfigMap also has the shim, as
  `geass-shim.ts`, `geass-shim.mjs`, or `geass-shim.py`.
- The Deployment mounts that ConfigMap read-only at `/function`. The Deployment runs the runtime command on the file
  (on the shim in handler mode).
- The pod template has a hash of the runtime, the mode, and the code. So a change to any of them rolls the pods.
- Geass adds `PORT` to the container's environment from `spec.port`. `automountServiceAccountToken` is `false`.
- For a function in handler mode or with schedules, Geass makes the Secret `<service ID>-trigger`. Geass gives the
  container `GEASS_TRIGGER_TOKEN` from that Secret. The pod template has a fingerprint of the token. So when Geass
  makes the token again, the pods roll.
- Each schedule is a CronJob with the name `<service ID>-<schedule name>-<8 characters>` (52 characters or fewer). The
  CronJob has the label `geass.dev/function-trigger=<name>` and the time zone `Etc/UTC`. Its Job runs `curl` from
  `curlimages/curl:8.10.1` on the nodes that the project's builds and jobs use.

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

**The pod restarts in a loop (`CrashLoopBackOff`).** The process exits. Open **Deployments** → **View logs** →
**Deploy Logs**. The log shows a syntax error, a missing module, or an uncaught exception. In handler mode,
`The function must export a handler` means that the file has no `handler` export.

**The status is Unavailable, then Failed, and the condition says "The service cannot start: app is CrashLoopBackOff".**
Geass read the reason from the pod. Fix the code, and then deploy again. See [Apps](/docs/guides/apps/#troubleshooting).

**The domain returns `502` or `503`.** In server mode, the code does not listen on `PORT`, or listens only on
`localhost`. Read the port from the environment, and bind all interfaces. A handler does not open a port. For a
handler, check the pod's log.

**`Cannot find module` or `ModuleNotFoundError`.** The package is not in the image, and Geass does not install
packages. Use the standard library. Alternatively, build an image that contains the package, and run it as an app.

**`require is not defined` on Node.js.** The file is an ES module. Use `import http from "node:http"`.

**A Python handler answers `500` with a JSON error about a coroutine.** The handler is `async def`. Use a plain `def`.

**My edit is not live.** Code changes wait for **Deploy changes**. The pending list shows `Function code updated`
until you deploy.

**A schedule never runs.** Schedules start with the next deploy after you save them. Make sure that the function is
deployed and that the pending list is empty. Then look for the CronJob with
`kubectl -n <namespace> get cronjobs -l geass.dev/name=<service ID>`. A function that is a draft has no CronJob.

**A schedule runs but the function does nothing, or the Job fails.** Read the Job with
`kubectl -n <namespace> get jobs -l geass.dev/function-trigger=<name>` and
`kubectl -n <namespace> logs job/<job name>`. `curl` fails in these cases:

- The function answers with `400` or above. `The requested URL returned error: 404` means that the **Path** is wrong.
- The function does not answer within 120 seconds.

**The handler sees `trigger: "http"` for a scheduled run.** The pods and the schedule do not agree about the token.
Deploy the function again, so that the pods restart with the current token. A request that does not have the correct
`x-geass-trigger-token` is always reported as `http`.

**A schedule fires at the wrong hour.** The schedule is in UTC. Convert your local time.

**"functions cannot be rolled back; restore the earlier code and deploy again".** A function has no rollback. Paste
the earlier code into the editor, save, and deploy.

**I cannot open the Code tab.** Read-only users cannot see function code ("Your viewer role cannot view function
code").

## Related

- [Apps](/docs/guides/apps/)
- [Sleep when idle](/docs/guides/scale-to-zero/)
- [Environment variables and secrets](/docs/guides/environment-variables/)
- [Templates](/docs/guides/templates/): ready-made **Hello function** and **Function with Postgres** examples, and
  templates that keep the mode and schedules of a function.
- [Domains and TLS](/docs/guides/domains-and-tls/)
- [Command line](/docs/guides/command-line/) and [MCP server](/docs/guides/mcp-server/)
- [GeassApp](/docs/reference/resources/geass-app/)
