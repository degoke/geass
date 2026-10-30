---
title: "Functions"
description: "Run Bun, Node.js, or Python code as a service without an image build, as a server or a handler, and call it on a schedule."
---

A **function** is a service whose code you write in the dashboard. You do not package the code in an image. You
select a runtime and edit one source file. Geass runs the file on the stock image of that runtime. The file is one of
these:

- A server that you write yourself.
- A handler that Geass serves for you.

Geass can also call a function on a schedule. Use a function for a webhook receiver, an internal API, a nightly job, or
a prototype. If your code needs installed packages or several files, build an image and run it as an
[app](/docs/guides/apps/).

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
4. Edit the code. The editor inserts two spaces when you press Tab. It saves on **Cmd+S** or **Ctrl+S**.
5. Click **Save**. To remove your edits, click **Discard**.
6. In the top bar, click **Deploy changes**. The status goes from **Draft** to **Setting up** to **Ready**.

After the first deploy, a code change is a pending change ("Code saved. Deploy to apply it."). **Deploy changes**
applies the code and restarts the function.

**+ Add resource** → **Function** always creates a function in server mode. To write a handler, change the mode. See
[Modes](#modes).

## Runtimes

Geass runs your code on an unmodified public image. Geass builds nothing for a function.

| Runtime | Image | Source file |
| --- | --- | --- |
| **Bun (TypeScript)** | `oven/bun:1-alpine` | `index.ts` |
| **Node.js** | `node:22-alpine` | `index.mjs` |
| **Python** | `python:3.12-alpine` | `main.py` |

The Node.js file is an ES module, so use `import`, not `require`. Geass does not install packages. There is no
`package.json`, no `requirements.txt`, and no build step. A function can use the standard library of the runtime and
what the image already contains.

To change the runtime, change **Runtime** under **Settings** → **Source**, or use the runtime selector on the **Code**
tab. Then save. Geass does not change the code, so rewrite the code for the new language.

## Modes

To set the mode, select it in the **Mode** menu on the **Code** tab. Then click **Save**.

| Mode | Menu entry | What your file is |
| --- | --- | --- |
| Server | **Server (serves HTTP)** | A program that listens on `PORT` and answers requests itself. This mode is the default. |
| Handler | **Handler (gets an event)** | A `handler(event)` function. Geass runs a small server around it. The server changes each request into an event, and the return value into the response. |

A mode change does not rewrite your code. Before you deploy, replace the starter server with a handler, or the other way
round. A mode change is a pending change.

### Server mode

Your code serves HTTP on the port in the `PORT` environment variable. Geass sets `PORT` from the **Port** setting, 8080
by default. Listen on all interfaces. The starter code does this:

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

You write a function, and Geass serves it. Geass calls your function one time for each request, and answers with the
return value. A web request and a scheduled call reach the handler in the same shape. So one function can serve both.

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

In Bun and Node.js, export a function named `handler`. A default export that is a function also works. So does a
default export object with a `handler` function. The handler can be `async`. In Python, define a plain
`def handler(event)`. Do not use `async def`, because Geass does not wait for it.

**The event.** The handler gets one object (a dictionary in Python):

| Field | Meaning |
| --- | --- |
| `method` | The HTTP method, for example `GET`. |
| `path` | The request path without the query string, for example `/sync`. |
| `query` | The query parameters as an object. A name that occurs more than one time holds a list of values. |
| `headers` | The request headers, with lowercase names. Geass removes `x-geass-trigger-token` and `x-geass-trigger-name`. |
| `body` | The parsed JSON when the content type is JSON and the text parses. Otherwise, the text. `null` (`None` in Python) when the body is empty. |
| `trigger` | `cron` for a call from one of the [schedules](#schedules) of the function. `http` for all other calls. |
| `triggerName` | The name of the schedule on a `cron` call. `null` (`None`) for other calls. |

**The response.** The return value of the handler becomes the HTTP response:

| The handler returns | The response |
| --- | --- |
| Nothing (`undefined`, `null`, `None`) | `204` with no body. |
| Text | `200` with `text/plain; charset=utf-8`. |
| An object or a list | `200` with the value as JSON. |
| An object with a numeric `statusCode` | That status. `headers` sets response headers, and `body` is the body. Text goes as text, any other value as JSON. |
| An exception | `500` with `{"error": "<message>"}`. Geass writes the error to the log. |

```js title="A handler that sets the status and a header"
export async function handler(event) {
  if (event.method !== "POST") return { statusCode: 405, body: "Use POST" };
  return { statusCode: 201, headers: { "x-example": "1" }, body: { received: event.body } };
}
```

A syntax error in your code stops the function, and **Deploy Logs** shows the error. If the file has no handler, the
log shows `The function must export a handler: export function handler(event) { ... }`. In Python, the message is
`The function must define a handler: def handler(event): ...`.

## How a function runs

- **Invocation.** Any HTTP request invokes the function: a browser, a webhook, another service, or a schedule. To
  make the function public, give it a **Custom domain** under **Settings** → **Networking**. Other services in the
  project use the private address in the same section. See [Domains and TLS](/docs/guides/domains-and-tls/).
- **Always on, or asleep.** The function runs all the time, unless you turn on **Sleep when idle** under **Settings** →
  **Scale**. A sleeping function starts again on the next request to its public domain. A schedule also wakes it. See
  [Sleep when idle](/docs/guides/scale-to-zero/).
- **Other triggers.** A function has no queue or event-bus trigger. Only an HTTP request and a schedule call it.

## Schedules

A schedule calls the function at the times that you set, for example for a nightly report or a cleanup every 15
minutes. Each run is an HTTP request to the function. So the function needs nothing special for a schedule.

### Add a schedule

1. Open the function, and select the **Code** tab. **Schedules** is under the editor.
2. Click **Add schedule**. Geass adds a row named `schedule-1` that runs at minute 0 of every hour (`0 * * * *`).
3. In the row, enter the name, for example `nightly`.
4. Enter the cron schedule in UTC, for example `0 3 * * *`.
5. Enter the path to call, for example `/sync`. For `/`, leave the path empty.
6. Click **Save schedules**. A toast shows "Schedules saved".
7. In the top bar, click **Deploy changes**. The schedules start with this deploy.

To remove a schedule, click **Remove**, click **Save schedules**, and then deploy. A function can have 8 schedules. The
dashboard has no field for a payload. The command line and the API can set one.

| Field | Meaning | Default |
| --- | --- | --- |
| Name | Lowercase letters, digits, and dashes, 1 to 32 characters, unique in the function. | None |
| Schedule | A cron expression in **UTC** with five fields: minute, hour, day of the month, month, and day of the week. Or `@hourly`, `@daily`, `@weekly`, `@monthly`, or `@yearly`. Numbers, ranges (`1-5`), steps (`*/15`), and lists (`1,15`) work. Names, for example `MON`, do not. | None |
| Path | The path that the schedule calls. It starts with `/` and has 256 characters or fewer. | `/` |
| Payload | The JSON body of the request, 4096 characters or fewer. | `{"trigger":"cron","name":"<name>"}` |

These are some examples:

- `*/15 * * * *`: every 15 minutes.
- `0 3 * * *`: every day at 03:00 UTC.
- `30 8 * * 1-5`: weekdays at 08:30 UTC.

### What a run does

Each run sends `POST <path>` to the function, with the payload as the JSON body.

- A run succeeds when the function answers with a status below 400. A run fails on `400` or above, or when there is
  no answer within 120 seconds. A cold start counts toward the 120 seconds.
- A failed run gets one more try.
- If a run is still in progress when the next run is due, the next run does not start.
- If a run cannot start within 5 minutes of its time, Geass skips it.
- A run wakes a sleeping function first.
- A [pull request environment](/docs/guides/pull-request-environments/) does not copy the schedules of a function. So a
  copy does not repeat the work for each open pull request.

The dashboard does not show a history of runs. To read the result of a run, see [Under the hood](#under-the-hood).

### Tell a schedule from a visitor

Every function in handler mode or with schedules gets a secret token. The function reads it as the environment variable
`GEASS_TRIGGER_TOKEN`. Every run sends the token in the header `x-geass-trigger-token`, and the schedule name in
`x-geass-trigger-name`.

In handler mode, Geass compares the header with the token for you. Only a request with the correct token is a `cron`
call. So a handler can trust `event.trigger`.

In server mode, nothing checks the header for you. To tell a run from a visitor, compare the header yourself:

```js title="Server mode: recognise a scheduled call (Node.js)"
import crypto from "node:crypto";

const secret = Buffer.from(process.env.GEASS_TRIGGER_TOKEN ?? "");

export function isScheduled(req) {
  const given = Buffer.from(String(req.headers["x-geass-trigger-token"] ?? ""));
  return secret.length > 0 && given.length === secret.length && crypto.timingSafeEqual(given, secret);
}
```

### From the command line, the API, or an agent

```bash
geass apps create report -p proj-1a2b3c4d -e production --function report.mjs --runtime node --mode event
geass functions triggers add report nightly "0 3 * * *"
geass functions triggers add report sync "*/15 * * * *" --path /sync --payload '{"full":true}'
geass deploy report --wait
geass functions invoke report --path /sync --data '{"full":true}'
```

`geass functions triggers add` replaces a schedule that has the same name. The schedule starts at the next deploy.
`geass functions invoke` calls the function at its public address and prints the answer. The function needs a domain
for this command.

An agent uses the `set_function_trigger`, `remove_function_trigger`, and `invoke_function` tools. See
[Command line](/docs/guides/command-line/), [MCP server](/docs/guides/mcp-server/),
[CLI commands](/docs/reference/cli/), and [MCP tools](/docs/reference/mcp-tools/).

Through the REST API, `POST /api/apps/{id}/update` takes `functionMode`, `functionRuntime`, `functionCode`, and
`functionTriggers`. `functionTriggers` is a JSON array of schedules. An empty value removes every schedule.

```bash title="Set the schedules of a function"
curl -X POST https://geass.example.com/api/apps/report/update \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  --data-urlencode 'functionTriggers=[{"name":"nightly","schedule":"0 3 * * *","path":"/nightly"}]'
```

## Limits

| Limit | Value |
| --- | --- |
| Code size | 256 KB (262,144 bytes). |
| Size when saved with the service | Under 200 KB, with quotes and line breaks escaped. |
| Files | One source file. |
| Runtimes | Bun, Node.js, and Python. |
| Schedules | 8 for each function. |
| Size | The same choices as an app. The default is 0.1 CPU and 128 MB. |
| Replicas and autoscaling | The same as an app. See [Apps](/docs/guides/apps/#scale). |

If code breaks a limit, Geass refuses to save it with a message, for example "function code must be under 256 KB". If a
schedule breaks a rule, Geass refuses it with a message, for example these messages:

- `trigger "nightly": the minute field must be between 0 and 59`
- `trigger "nightly": the path must start with /`
- `a function can have at most 8 triggers`

## Environment variables and secrets

Use the **Variables** tab, the same as for an app. Variables and secrets reach the function as environment variables:

```ts title="Read a variable in Bun or Node.js"
const greeting = process.env.GREETING ?? "Hello";
```

```python title="Read a variable in Python"
import os
greeting = os.environ.get("GREETING", "Hello")
```

A variable change applies on the next deploy. A `PORT` variable on the **Variables** tab does not replace the `PORT`
that Geass sets. To change the port, change the **Port** setting.

To give a function a database connection, use **Add Reference**. The **Function with Postgres**
[example template](/docs/guides/templates/#examples-to-copy) does this. See
[Environment variables and secrets](/docs/guides/environment-variables/).

## How functions differ from apps

| | Function | App |
| --- | --- | --- |
| What you provide | One source file. | An image, or a repository with a Dockerfile. |
| Build | None. | None for an image. A build for a repository. |
| Packages | Only what the image has. | All that your image contains. |
| Where you edit the code | The **Code** tab. | Your repository or image. |
| Schedules | Up to 8. | None. |
| Rollback | None. Restore the earlier code and deploy again. | With the command line, for image services. |
| Port | Geass sets `PORT` from the **Port** setting. | Geass does not set `PORT`. |

Everything else is the same: size, replicas, autoscaling, sleep when idle, domain, variables, console, metrics, and
history. Each deploy records the code in the history as `Deploy function (bun)`. **Pending changes** shows
`Function code updated` for a code change. A runtime change shows `Source: function (bun) → function (node)`. A
change to only the mode or the schedules shows `Settings updated`.

## Logs, console, and metrics

- **Deployments** → **View logs** → **Deploy Logs** shows the last 400 lines of each running copy.
- The **Console** tab opens a shell (`sh`) in a running copy for five minutes. The image is Alpine-based.
- **Metrics** shows the charts of the service.

See [Observability](/docs/guides/observability/).

## Troubleshooting

**Saving fails with "function code is too large to deploy".** The escaped code is over 200 KB. Remove large embedded
data, or move to an [app](/docs/guides/apps/).

**The status is Unavailable, then Failed, with `The service cannot start: app is CrashLoopBackOff`.** The process
exits. Open **Deployments** → **View logs** → **Deploy Logs**. The log shows a syntax error, a missing module, or an
uncaught exception. Fix the code, and deploy again.

**The domain returns `502` or `503`.** In server mode, the code does not listen on `PORT`, or listens only on
`localhost`. Read the port from the environment, and listen on all interfaces. In handler mode, read the log.

**`Cannot find module` or `ModuleNotFoundError`.** Geass does not install packages. Use the standard library, or build
an image that contains the package and run it as an app.

**`require is not defined` on Node.js.** The file is an ES module. Use `import http from "node:http"`.

**A Python handler answers `500` with an error about a coroutine.** The handler is `async def`. Use a plain `def`.

**An edit is not live.** Code changes wait for **Deploy changes**. The pending list shows `Function code updated` until
you deploy.

**A schedule never runs.** Schedules start with the next deploy after you save them. Make sure that the function is
deployed and that nothing is pending. A draft runs no schedules.

**A scheduled run fails.** The function answered with `400` or above, or did not answer within 120 seconds. A `404`
means that the path of the schedule is wrong. To read the run, see [Under the hood](#under-the-hood).

**A handler sees `trigger: "http"` for a scheduled run.** The function and the schedule do not agree about the token.
Deploy the function again.

**A schedule runs at the wrong hour.** The schedule is in UTC. Convert your local time.

**"functions cannot be rolled back; restore the earlier code and deploy again".** Paste the earlier code into the
editor, save, and deploy.

**You cannot open the Code tab.** Read-only users cannot see function code ("Your viewer role cannot view function
code").

## Under the hood

A function is a `GeassApp` with `spec.source.function`. Geass keeps the code in the ConfigMap `<service ID>-function`
in the namespace `<project ID>-<environment>`. Each schedule is a CronJob in the same namespace. The token is in the
Secret `<service ID>-trigger`. The function gets no Kubernetes service account token.

```bash title="Read the schedules of a function and their runs"
kubectl -n proj-1a2b3c4d-production get cronjobs -l geass.dev/name=report
kubectl -n proj-1a2b3c4d-production get jobs -l geass.dev/function-trigger=nightly
kubectl -n proj-1a2b3c4d-production logs job/<job name>
```

Geass keeps the last successful run and the last three failed runs.

```yaml title="report.yaml: a handler with two schedules"
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

Without `mode`, a function is in server mode. For what changes when you manage a service with a manifest, see
[Apps](/docs/guides/apps/#manifest-and-api). For every field, see [GeassApp](/docs/reference/resources/geass-app/).

## Related

- [Apps](/docs/guides/apps/)
- [Sleep when idle](/docs/guides/scale-to-zero/)
- [Environment variables and secrets](/docs/guides/environment-variables/)
- [Templates](/docs/guides/templates/): the **Hello function** and **Function with Postgres** examples.
- [Domains and TLS](/docs/guides/domains-and-tls/)
- [Command line](/docs/guides/command-line/) and [MCP server](/docs/guides/mcp-server/)
- [GeassApp](/docs/reference/resources/geass-app/)
