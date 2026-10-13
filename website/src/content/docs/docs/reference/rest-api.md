---
title: "REST API"
description: "The Geass HTTP API: the versioned /api/v1, the dashboard routes, authentication, errors, every endpoint and curl examples."
---

The operator serves an HTTP API next to the dashboard. The versioned part, `/api/v1`, is what scripts, CI, the
[`geass` command line](/docs/guides/command-line/) and the [MCP server](/docs/guides/mcp-server/) use: it takes JSON, answers
every error in one shape and is described by an OpenAPI document. Underneath it are the dashboard's own `/api` routes, which
the dashboard calls and which `/api/v1` forwards to. You call either with a workspace or project
[API token](/docs/guides/users-and-tokens/), or with a signed-in session.

:::note
`/api/v1` is the version of the API. The resources it returns inside `spec` and `status` are the `v1alpha1` custom resources, so
those fields can still change between releases. The dashboard routes under `/api` are not versioned and not described by any
schema; [Stability](#stability) says which routes to depend on.
:::

## The two API surfaces

| | Versioned API | Dashboard routes |
| --- | --- | --- |
| Path | `/api/v1/...` | `/api/...` |
| Request body | JSON (a form body also works) | Form-encoded (`application/x-www-form-urlencoded`) |
| Verbs | `GET`, `POST`, `PATCH`/`PUT`, `DELETE` | `GET` and `POST` only |
| Errors | `{"error": "...", "code": "..."}` for every error | `{"error": "..."}` or a plain-text `404` |
| Described by | `GET /api/v1/openapi.json` | The tables further down this page |
| Used by | The CLI, the MCP server, scripts | The dashboard, and `/api/v1` itself |

Every write in `/api/v1` is a dashboard route in disguise: Geass turns the JSON body into the form the dashboard handler reads, and
sends the request there. That is why access rules, validation messages and the shape of a success answer are the same on both
surfaces, and why the tables for the dashboard routes below also tell you which fields a `/api/v1` write accepts.

## Conventions

**Base URL.** The dashboard's address, for example `https://geass.example.com`. The operator itself speaks plain HTTP on port
8085; put TLS in front of it (see [Security](/docs/operations/security/)).

**Authentication.** Send `Authorization: Bearer <token>` with a workspace token (`gsw_...`) or a project token (`gsp_...`). A
session cookie (`geass_session`, from `POST /api/login`) also works. A request with a Bearer header that fails never falls back to
a cookie. Programs that cannot ask a person for a password sign in with a [device code](#sign-in-from-a-program).

**Cookie writes need an origin.** A write made with a session cookie must carry an `Origin` or `Referer` header whose host is the
dashboard's. Browsers add it; with `curl` add `-H "Origin: https://geass.example.com"`. Requests with a Bearer token do not need it.

**Names and IDs.** Every resource lives in the namespace `geass-system`, and the API addresses a resource by its `metadata.name`. For
a project that is an ID such as `proj-1a2b3c4d`; the readable name is `spec.displayName`. For services, databases and buckets the
dashboard generates a name like `morning-beach`, and a display label can be kept in the annotation `geass.dev/display-name`. See
[Resources overview](/docs/reference/resources/overview/).

**Draft and deploy.** Creating a service, database or bucket saves a draft. Editing a running one saves a pending change. Nothing is
applied until a deploy call, except settings Geass treats as live: a service's replica count, autoscaling and sleep-when-idle
settings, its host and TLS settings, metrics scraping and rollout flags, and a database's backup settings. (`source.git.watchPatterns` is
also on that list: a push reads it at once. `logs.retentionHours` is too, but nothing reads it.) A function's schedules are not live: they
start with the next deploy. See [Projects and environments](/docs/guides/projects-and-environments/).

**Who can call what.** There are four kinds of caller:

| Caller | Can |
| --- | --- |
| `admin` (a user, or a workspace token) | Everything, except that only a signed-in admin's session, not a token, can manage tokens and approve device codes |
| `observer` (a workspace token) | Read everything a viewer can, and also logs, pods, deploy logs, variable names and the other reads marked **Observer** below. Cannot change anything |
| `viewer` (a user, or a workspace token) | Read the redacted overview. Cannot read logs, variables or other sensitive reads, and cannot change anything |
| Project token | The endpoints in [Project token endpoints](#project-token-endpoints), for services in its own environment |

Where a table has an **Access** column, **Admin** means admins only (and admin workspace tokens), **Observer** means observers and
admins, **Viewer** means any signed-in caller (viewers, observers and admins), and a trailing "project token" means a project token
can call it too. Tables without the column are admin writes. [Users and API tokens](/docs/guides/users-and-tokens/#roles) lists what
each role can see.

## Errors

On `/api/v1` every error is a JSON object with a message and a code a program can switch on, whichever layer produced it:

```json title="Error response from /api/v1"
{ "error": "confirmation did not match the resource name", "code": "invalid_request" }
```

| Status | `code` | Meaning |
| --- | --- | --- |
| `400` | `invalid_request` | The request is invalid, a rule refused it, or the cluster lacks capacity. `error` says why. A body that is not a JSON object, or a verb with no mapping (`there is no PUT /api/v1/nodes/...`), also gets this |
| `401` | `unauthenticated` | No valid session or token: `authentication required` or `invalid or expired API token` |
| `403` | `forbidden` | The caller's role or token scope does not allow it: `viewer role cannot access this`, `this token cannot access that endpoint`, or a token limited to another project |
| `404` | `not_found` | The resource, or the route, does not exist. A project token gets `app not found` for services it cannot see |
| `405` | `method_not_allowed` | The path does not take that method |
| `413` | `too_large` | The body is larger than 2 MiB |
| `429` | `rate_limited` | Too many sign-in requests (see [Sign in from a program](#sign-in-from-a-program)) |
| `500`, `502` | `internal_error` | Geass could not read or write the cluster: `could not complete the request` or `could not load the resources` |

A rule that fails a write returns `400` with its plain-language message, for example `a project must retain at least one
environment` or `variable name must use uppercase letters, numbers, and underscores`. The device-code endpoints answer with their own
codes (`authorization_pending`, `slow_down`, `access_denied`, `expired_token`) in the same `code` field.

The dashboard routes under `/api` answer with `{"error": "..."}` and no `code`. Most of their `404` answers are the plain text
`404 page not found`, and these statuses apply:

| Status | Meaning |
| --- | --- |
| `400` | The request is invalid, a rule refused it, or the cluster lacks capacity. `error` says why |
| `401` | No valid session or token. `authentication required` or `invalid or expired API token` |
| `403` | `viewer role cannot access this` (for a viewer, and for an observer that tries a write), `this token cannot access that endpoint`, or a token limited to another project |
| `404` | The resource does not exist. Most handlers answer with the plain text `404 page not found`, not JSON, and a project token gets `app not found` for services it cannot see |
| `405` | `{"error": "method not allowed"}`: an `/api/` path called with a method other than `GET` or `POST`. A `GET` on a `POST`-only path is a plain-text `404`, except `POST /api/databases/{db}/query`, whose `GET` is a `405` |
| `415` | `send form-encoded data`: a project token sent another content type to a dashboard route. `/api/v1` converts JSON for you, so it never gets this |
| `500`, `502` | Geass could not read or write the cluster. `could not complete the request` or `could not load dashboard` |

## Stability

| Tier | Endpoints | What to expect |
| --- | --- | --- |
| Versioned API | Every operation in `GET /api/v1/openapi.json` | Built for scripts, CI, the CLI and the MCP server. One error format, JSON bodies, an OpenAPI description |
| Token endpoints and read model | `GET /api/token`, `GET /api/token/services`, `POST /api/token/deploy`, `GET /api/bootstrap`, `GET /api/usage`, metrics and logs reads | The dashboard routes that tokens were designed around. `/api/v1` offers `GET /me`, per-resource reads, `GET /token/services` and `POST /token/deploy` for the same jobs. The shape of `/api/bootstrap` follows the `v1alpha1` resources and the dashboard's needs |
| Dashboard internals | Sign-in and sign-out, the settings endpoints (domain, GitHub, Cloudflare), webhooks, OAuth callbacks, the event stream, the terminal WebSocket, form fields that exist for UI forms | Change with the UI, and are not in the OpenAPI document. Prefer `kubectl` for these |

`/api/v1` also forwards any other `POST` or `GET` path to the dashboard route of the same name (`POST /api/v1/projects/{name}/webhooks/save`
reaches `POST /api/projects/{name}/webhooks/save`). Only the operations in the OpenAPI document are part of the versioned API;
treat the rest as dashboard internals.

## Quick start

```bash title="Check your token"
export GEASS=https://geass.example.com
export GEASS_TOKEN=gsw_1a2b3c4d_0123456789abcdef0123456789abcdef01234567
curl -fsS -H "Authorization: Bearer $GEASS_TOKEN" $GEASS/api/v1/me
```

```json title="Output"
{
  "username": "token:ci",
  "role": "observer",
  "canMutate": false,
  "canReadSensitive": true,
  "authentication": "token",
  "scope": "workspace",
  "expiresAt": "2027-01-10T09:00:00Z"
}
```

```bash title="List the services in an environment"
curl -fsS -H "Authorization: Bearer $GEASS_TOKEN" \
  "$GEASS/api/v1/apps?project=proj-1a2b3c4d&environment=production" \
  | jq -r '.items[] | [.name, .state, (.url // "-")] | @tsv'
```

```text title="Output"
storefront	ready	https://shop.example.com/
```

## The versioned API

### Requests

Send `Authorization: Bearer <token>` and, for a write, a JSON object as the body with `Content-Type: application/json`. The verbs
mean what they usually do:

| Verb | Use |
| --- | --- |
| `GET` | Read |
| `POST` on a collection (`/apps`, `/databases`, `/projects`, ...) | Create (saved as a draft where the resource has drafts) |
| `POST` on an action (`/apps/{name}/deploy`) | Do something |
| `PATCH` (or `PUT`) on `/apps/{name}`, `/databases/{name}`, `/object-stores/{name}`, `/projects/{name}` | Change settings |
| `DELETE` on `/apps/{name}` and the other collections | Delete. Add `?confirmName=<the name>` (or a body field) to confirm |

Geass turns the body into form fields before the handler sees it, so these rules apply:

- A string or number is the field's value. `true` is a checked box, so `"deploy": true` means deploy. `false` and `null` leave the field
  out, which keeps its value: to turn a switch off, send the string the handler expects, such as `"scaleToZero": "off"`.
- A list repeats the field (`"environments": ["production", "staging"]`). An object nested in a field travels as its JSON text, and a
  list of lists is refused.
- Query-string parameters become fields too, which is how a `DELETE` carries `confirmName`. Fields in the body win over the same field in
  the query string.
- The body must be a JSON object, at most 2 MiB. A form-encoded body is also accepted.

### The OpenAPI document

`GET /api/v1/openapi.json` returns the API as an OpenAPI 3.0.3 document. It is generated from the list `v1Operations` in
`internal/dashboard/api_v1_spec.go`, so what is described and what is served are kept together: a test checks that every listed
operation is answered, and that none is listed twice.

```bash title="List the paths, and who may call one"
curl -fsS -H "Authorization: Bearer $GEASS_TOKEN" $GEASS/api/v1/openapi.json | jq -r '.paths | keys[]'
curl -fsS -H "Authorization: Bearer $GEASS_TOKEN" $GEASS/api/v1/openapi.json | jq '.paths["/apps/{name}/scale"].post["x-access"]'
```

- `servers` is `/api/v1`, so every path in the document is relative to it. The security scheme is a Bearer token, and the two
  public operations (`/auth/device` and `/auth/token`) are marked as needing none.
- Each operation has a `summary`, a tag, an `operationId` built from the method and path (`getAppsNameStatus`), its path parameters and, for
  a write, a JSON request body listing its fields, with the required ones marked.
- `x-access` says who may call it: `public`, `viewer` (anyone signed in), `observer`, `project` (a project token for its own
  environment may too) or `admin`. The route table below is more exact about the few rows where that label is not the whole story.
- Responses are not described beyond the success, `400`, `401`, `403` and `404` entries, and the `4xx` ones say only that the body is
  `{"error": "...", "code": "..."}`. The **Returns** column below fills that gap.
- A project token cannot fetch the document.

### Route table

**Who** uses the terms from [Conventions](#conventions). **Project token** in the first column means a project token may call the row
too. A write answers `{"ok": true}` unless the **Returns** column says otherwise, and fields marked `*` are required. For writes
that forward to a dashboard route, the other fields of that route's table further down are accepted as well.

#### The service itself

| Method and path | Who | Body | Returns |
| --- | --- | --- | --- |
| `GET /version` | Any, project token | none | `{"api": "v1", "name": "geass"}` |
| `GET /me` | Any, project token | none | `username`, `role`, `canMutate`, `canReadSensitive`, `authentication` (`session` or `token`), and for a token `scope`, `expiresAt` and, for a project token, `project` and `environment` |
| `GET /openapi.json` | Any | none | The OpenAPI document |
| `GET /events` | Any | none | A server-sent event stream that says when something changed; see [Events](#events) |
| `GET /usage` | Any | none | What each project reserves and uses; see [Usage](#usage) |

#### Sign-in and tokens

| Method and path | Who | Body | Returns |
| --- | --- | --- | --- |
| `POST /auth/device` | Public | `clientName` (up to 60 characters) | A device code and a user code; see [Sign in from a program](#sign-in-from-a-program) |
| `POST /auth/token` | Public | `deviceCode`* | The token once a person approved the code, or a `400`/`429` with a device-flow `code` |
| `GET /auth/device/{code}` | Admin session | none (`{code}` is the user code; case and the dash do not matter) | `{"userCode", "clientName", "status", "expiresIn"}`, what the approval page shows |
| `POST /auth/device/approve` | Admin session | `userCode`* (case and the dash do not matter), `role` (`admin`, `observer` or `viewer`; default `observer`), `expiresInDays` (0 to 3650; default 30), `deny` (`true` refuses) | `{"ok": true, "item": {...}}` with the new token's record but not the token, or `{"ok": true, "denied": true}` |
| `GET /tokens` | Admin session | `?project=` | `{"items": [token, ...]}`, newest first |
| `POST /tokens` | Admin session | `name`*, `scope`* (`workspace` or `project`), `role` (`admin`, `observer`, `viewer`; workspace only; default `viewer`), `project` and `environment` (project only), `expiresInDays` (0 to 3650; 0 or none means never) | `{"ok": true, "token": "gsw_...", "item": {...}}`; the token is shown once |
| `DELETE /tokens/{id}` | Admin session | none | `{"ok": true}`; `404` `token was not found` |

#### Projects

| Method and path | Who | Body | Returns |
| --- | --- | --- | --- |
| `GET /projects` | Any | none | `{"items": [summary, ...]}`; see [Resource summaries](#resource-summaries) |
| `POST /projects` | Admin | `region` | `201` `{"project": "proj-1a2b3c4d"}` |
| `GET /projects/{name}` | Any | none | `{"resource", "spec", "status"}` |
| `PATCH /projects/{name}` | Admin | `displayName`, `environments` (list), `region` | `{"ok": true}` |
| `DELETE /projects/{name}` | Admin | `confirmName`* (the display name) | `{"ok": true}` |
| `POST /projects/{name}/deploy` | Admin | `environment`* | `{"ok": true, "deployed": 2}` |
| `POST /projects/{name}/environments` | Admin | `environment`* | `{"ok": true}`; an environment that exists is a `400` |
| `POST /projects/{name}/compliance` | Admin | `requiredTags` (list, up to 8; an empty list clears them) | `{"ok": true, "requiredTags": ["hipaa"]}` |
| `GET /projects/{name}/compliance` | Any | none | `{"requiredTags", "region", "databases": [{"name", "compliant", "missing"}], "openNodes"}`: where the project falls short of its tags |
| `POST /projects/{name}/network` | Admin | `isolation`* (`Project` or `None`), `allowFromProjects` (list, up to 20) | `{"ok": true}` |
| `POST /projects/{name}/variables/save` | Admin | `name`*, `environment`*, `value`*, `secret` (`true` stores it as a secret) | `{"ok": true}` |
| `GET /projects/{name}/metrics` | Any | `?window=`, `?environment=` | Series; see [Metrics and logs](#metrics-and-logs) |
| `GET /projects/{name}/logs` | Observer | `?environment=`, `service`, `q`, `level`, `since`, `tail`, `previous` | Merged live logs |
| `GET /projects/{name}/logs/stream` | Observer | the same query | The same as server-sent events |
| `GET /projects/{name}/logs/history` | Observer | `?environment=`, `service`, `q`, `level`, `since`, `limit` | Stored history. A project that requires node tags gets `{"available": false, ...}`: its history is not kept |

#### Services

A service is a `GeassApp`. `{name}` is its resource name.

| Method and path | Who | Body | Returns |
| --- | --- | --- | --- |
| `GET /apps` | Any, project token | `?project=`, `?environment=` | `{"items": [summary, ...]}`. A project token sees only its own environment |
| `POST /apps` | Admin | `project`*, `environment`*, `source` (`image`, `git` or `function`; an image when left out), `name`, `image`, `repository`, `branch`, `runtime`, `code`, `mode` (`http` or `event`), `functionTriggers` (list), `port`, `draft` (`true`: save a draft), `deploy` (`true`: deploy it as it is saved); the other fields of [Create and change](#create-and-change) | `{"ok": true, "name": "storefront", "kind": "apps"}`. The service is a draft unless `deploy` is true, so call a deploy next. With `deploy` the answer also has `"deployed": true`. `draft` and `deploy` together are a `400`, and a cluster with no room refuses a `deploy` before anything is saved |
| `GET /apps/{name}` | Any, project token | none | `{"resource", "spec", "status"}`. Callers who cannot change things get the redacted spec; secret values are blank for everyone |
| `PATCH /apps/{name}` | Admin, project token | `image`, `port`, `cpu`, `memory`, `replicas`, `deploy` (`true` deploys straight away), `functionCode`, `functionRuntime`, `functionMode`, `functionTriggers` (list; an empty string removes them all), `scaleToZero` (`"on"` or `"off"`), `idleSeconds`, `scaleToZeroMax`; the other fields of [Create and change](#create-and-change) | The pending state; see [Create and change](#create-and-change) |
| `DELETE /apps/{name}` | Admin | `confirmName`* | `{"ok": true}` |
| `GET /apps/{name}/status` | Any, project token | none | One read of everything a deploy leaves behind; see below |
| `GET /apps/{name}/deployments` | Any, project token | none | `{"items": [{"id", "phase", "image", "commit", "trigger", "actor", "changeTitle", "createdAt", "startedAt", "completedAt"}]}`, newest first. The `id` is what `rollback` takes |
| `GET /apps/{name}/deployments/{id}/logs` | Observer, project token | none | `{"id", "sections": [{"id", "title", "lines"}]}` |
| `POST /apps/{name}/deploy` | Admin, project token | none | `{"ok": true, "pending": [], "deployed": true, ...}` |
| `POST /apps/{name}/scale` | Admin, project token | `replicas`* (0 to 1000) | `{"ok": true}`; takes effect at once |
| `POST /apps/{name}/rollback` | Admin, project token | `revision`* (a deployment `id`) | `{"ok": true}`; functions cannot be rolled back |
| `POST /apps/{name}/build`, `/build/cancel` | Admin, project token | none | `{"ok": true}` |
| `POST /apps/{name}/retry-provision` | Admin, project token | none | `{"ok": true}` |
| `POST /apps/{name}/config/set` | Admin, project token | `key`*, `value`* | `{"ok": true, "config": {...}, "pending": ["variables"]}` |
| `POST /apps/{name}/config/delete` | Admin, project token | `key`* | The same |
| `POST /apps/{name}/secrets/set` | Admin, project token | `key`*, `value`* | `{"ok": true, "secrets": ["STRIPE_API_KEY"], "pending": ["variables"]}`; never returns values |
| `POST /apps/{name}/secrets/delete` | Admin, project token | `key`* | The same |
| `GET /apps/{name}/variables` | Observer, project token | none | `{"secrets": [names], "sharedVariableRefs", "env": [{"name", "kind"}], "config": {...}}`. Secrets and attached references are names only; the plain `config` comes with its values, because plain configuration is not secret |
| `GET /apps/{name}/logs` | Observer, project token | `?tail=` (1 to 1000, default 200, counted per pod), `since`, `q`, `level`, `previous` (`true` also reads each pod's run before its last restart) | `{"lines": [{"t", "service", "pod", "container", "line", "level", "previous"}], "services", "truncated", "note"}`: the service's recent log lines, oldest first, one entry each. It is the project's logs narrowed to the service, so the `environment` and `service` query fields are ignored. `level` and `q` look through the `tail` newest lines of each pod |
| `GET /apps/{name}/logs/stream` | Observer, project token | `?level=`, `since`, and the other log query fields | A server-sent event stream of the service's log lines, recent ones first and then new ones, across restarts |
| `GET /apps/{name}/runtime` | Observer, project token | none | `{"pods": [{"name", "phase", "container"}]}` |
| `GET /apps/{name}/metrics` | Any, project token | `?window=` | Series; see [Metrics and logs](#metrics-and-logs) |

`GET /apps/{name}/status` returns what a program needs to wait for a deploy without reading five resources: `name`, `project`,
`environment`, `state` (`ready`, `pending`, `failed`, `draft` or `deleting`), `message`, `url`, `pendingChanges` (a count), `ready`,
`replicas` (`desired`, `ready`, `available`), `sleeping` (present, and true, for a service that sleeps when idle and has no copies running), `build`
(`name`, `phase`, `failureReason`, `sourceRevision`, `ciState`, `startedAt`, `completedAt`) and `lastAttempt` (`id`, `phase`,
`startedAt`, `completedAt`, `summary`, `errors`). `build` and `lastAttempt` are left out when there is none. Any caller can read it,
including a viewer, so the build's failure reason and source revision, which the redacted overview keeps from viewers, are visible
here.

#### Databases, logical databases and buckets

| Method and path | Who | Body | Returns |
| --- | --- | --- | --- |
| `GET /databases`, `/logical-databases`, `/object-stores` | Any | `?project=`, `?environment=` | `{"items": [summary, ...]}` |
| `GET /databases/{name}`, `/logical-databases/{name}`, `/object-stores/{name}` | Any | none | `{"resource", "spec", "status"}` |
| `POST /databases` | Admin | `project`*, `environment`*, `engine` (`postgres`, `mysql`, `redis`, `sqlite`; Postgres when left out), `name`, `highAvailability`; the other fields of [Databases](#databases) | `{"ok": true, "name": "orders-db", "kind": "databases"}` (a draft) |
| `PATCH /databases/{name}` | Admin | `version`, `storageSize`, `cpu`, `memory`, `backups` (`"on"` or `"off"`), `backupRetentionDays`, `clusterSize`, `runOnTags` | `{"ok": true, "applied": "live", "liveChanged": true}` |
| `DELETE /databases/{name}`, `/logical-databases/{name}`, `/object-stores/{name}` | Admin | `confirmName`* | `{"ok": true}` |
| `POST /databases/{name}/deploy` | Admin | none | `{"ok": true}` |
| `GET /databases/{name}/connection` | Admin (a workspace admin, not a project token or observer) | `?reveal=true` | `{"keys": [...], "host", "port", "database", "username"}`; with `reveal=true` also `"password"`, `"uri"` and `"revealed": true`. The reveal is written to the operator log |
| `GET /databases/{name}/metrics` | Any | `?window=` | Series |
| `POST /databases/restore` | Admin | `archive`*, `restorePoint` | A new database restored from backups; see [Databases](#databases) |
| `POST /logical-databases` | Admin | `name`, `project`, `environment`, `server`, `database`, `connectionLimit` | `{"ok": true}`; needs no deploy |
| `GET /logical-databases/{name}/connection` | Admin (workspace admin) | `?reveal=true` | As for a database |
| `POST /object-stores` | Admin | `project`, `environment`, `name`, `placement`, `engine`, `region`, `createBucket`, `connectionRef`, `bucket`, `draft` | A draft bucket; see [Buckets](#buckets) |

#### Nodes and metrics

| Method and path | Who | Body | Returns |
| --- | --- | --- | --- |
| `GET /nodes` | Any (not a project token) | none | `{"items": [{"name", "ready", "schedulable", "controlPlane", "region", "zone", "tags": [{"name", "reserved", "compliance"}], "cpu", "memory"}]}` |
| `POST /nodes/{name}/placement` | Admin | `region`, `zone`, `tags` (list), `reserved` (list), `compliance` (list) | `{"ok": true, "warnings": [...]}`; see [Cluster and platform](#cluster-and-platform) |
| `GET /cluster/metrics` | Any (not a project token) | `?window=` | Series for the whole cluster |
| `GET /operator/metrics` | Any (not a project token) | `?window=` | Series for the operator: `reconcileErrors`, `reconcileTime`, `apiLatency`, `apiRequests`, `queueDepth`, `statusWrites`, `statusSkipped` |

#### Templates and project tokens

| Method and path | Who | Body | Returns |
| --- | --- | --- | --- |
| `GET /templates` | Observer | none | `{"items": [template, ...]}` |
| `POST /templates/{id}/deploy` | Admin | `project`*, `environment`* | `{"ok": true, "created": [...]}`; the template's resources as drafts |
| `GET /token/services` | Any token, project token | `?project=` and `?environment=` (workspace tokens only) | `{"project", "environment", "services": [{"id", "name", "kind", "source", "ready", "pendingChanges"}]}` |
| `POST /token/deploy` | Project token only | none | `{"ok": true, "deployed": 2}`: deploys every pending change in the token's environment |

#### Resource summaries

A list returns one summary per resource, in the same shape for every kind, so a program can treat them alike:

```json title="One item of GET /apps"
{
  "kind": "app",
  "name": "storefront",
  "displayName": "storefront",
  "project": "proj-1a2b3c4d",
  "environment": "production",
  "state": "ready",
  "url": "https://shop.example.com/",
  "pendingChanges": 0,
  "createdAt": "2026-10-12T09:00:00Z",
  "details": { "port": 8080, "source": "image", "image": "ghcr.io/acme/storefront:1.4.2", "replicas": 1 }
}
```

`state` is `ready`, `pending` (still being set up or rolled out), `failed`, `draft` (saved and not deployed) or `deleting`. `message`
explains a `pending` or `failed` state. `details` depends on the kind: a function has `runtime`, `mode` and `triggers`, a service that
sleeps when idle has `scaleToZero` (`idleSeconds`, `maxReplicas`, and whether it is `active`), a project has `environments`, `region`
and `requiredTags`, a database has `engine`, `highAvailability`, `placement` and `version`, a logical database has `server` and
`database`. A caller who cannot change things gets the same redactions as in the dashboard, so a viewer sees an empty `engine`.

### Sign in from a program

A program that has no browser, such as the command line, signs in with a device code. It asks Geass for a code, shows it to a person,
and polls until an admin approves it in the dashboard. The password never reaches the program, and neither endpoint it calls needs
credentials.

1. Start the sign-in:

   ```bash title="Start a sign-in"
   login=$(curl -fsS -X POST $GEASS/api/v1/auth/device \
     -H "Content-Type: application/json" \
     -d '{"clientName": "deploy script on build-1"}')
   echo "$login"
   DEVICE_CODE=$(echo "$login" | jq -r .deviceCode)
   ```

   ```json title="Response"
   {
     "deviceCode": "0233078f58dc2fb5928b728122131649c44b7c73af43c2e0bd37340458eab8ae",
     "userCode": "BCDF-GHJK",
     "verificationUri": "https://geass.example.com/device",
     "verificationUriComplete": "https://geass.example.com/device?code=BCDF-GHJK",
     "expiresIn": 600,
     "interval": 5
   }
   ```

   `deviceCode` is 64 hexadecimal characters that only your program holds. `userCode` is eight letters from an alphabet with no vowels, shown as two groups of four. `clientName` is shown to the approver and
   becomes the token's name (`CLI: deploy script on build-1`); it is whatever the program sends, so the approver must check the code, not
   the name.

2. Show the person `verificationUri` and `userCode`. They open the page, sign in as an admin if they are not, check that the code on
   the page is the one your program shows, choose the access and lifetime, and approve. The page is **Sign in the command line**; see
   [Users and API tokens](/docs/guides/users-and-tokens/#sign-in-the-command-line).

3. Poll for the answer no faster than `interval` seconds:

   ```bash title="Poll for the token"
   curl -sS -X POST $GEASS/api/v1/auth/token \
     -H "Content-Type: application/json" \
     -d "{\"deviceCode\": \"$DEVICE_CODE\"}"
   ```

   | Answer | Status | Body | Do |
   | --- | --- | --- | --- |
   | Waiting | `400` | `"code": "authorization_pending"` | Poll again after `interval` seconds |
   | Too fast | `429` | `"code": "slow_down"`, and the message says how long to wait | Wait longer; each `slow_down` adds 5 seconds, up to 30 |
   | Approved | `200` | `{"token": "gsw_...", "tokenType": "Bearer", "role": "observer", "approvedBy": "admin", "expiresAt": "..."}` | Store the token. It is handed out once, with `Cache-Control: no-store` |
   | Refused | `400` | `"code": "access_denied"` | Stop |
   | Expired or unknown | `400` | `"code": "expired_token"` | Start again |

Limits: a request lives 10 minutes and is kept in memory, so an operator restart forgets pending sign-ins. At most 100 are pending
at once, and one address may start 10 a minute; beyond that `POST /auth/device` answers `429` `too many sign-in requests, try again
in a minute`. The limit counts the connection's remote address and does not read `X-Forwarded-For`, so behind an ingress everyone
shares one count.

The approval itself is `POST /api/v1/auth/device/approve`, which needs a signed-in admin's cookie and an `Origin` header. The
dashboard page does it for you:

```bash title="Approve a code from a script (with a cookie jar)"
curl -fsS -c cookies.txt -X POST $GEASS/api/login -H "Origin: $GEASS" \
  --data-urlencode "username=admin" --data-urlencode "password=replace-with-your-passphrase"
curl -fsS -b cookies.txt -X POST $GEASS/api/v1/auth/device/approve -H "Origin: $GEASS" \
  -H "Content-Type: application/json" \
  -d '{"userCode": "BCDF-GHJK", "role": "observer", "expiresInDays": 7}'
```

### Examples

```bash title="Create a service from an image and deploy it"
curl -fsS -X POST -H "Authorization: Bearer $GEASS_TOKEN" -H "Content-Type: application/json" $GEASS/api/v1/apps -d '{
  "project": "proj-1a2b3c4d", "environment": "production", "name": "storefront",
  "source": "image", "image": "ghcr.io/acme/storefront:1.4.2", "port": 8080
}'
curl -fsS -X POST -H "Authorization: Bearer $GEASS_TOKEN" $GEASS/api/v1/apps/storefront/deploy
```

```json title="Responses"
{ "ok": true, "name": "storefront", "kind": "apps" }
{ "ok": true, "pending": [], "deployed": true, "title": "", "detail": "", "button": "" }
```

To deploy as the service is saved, add `"deploy": true` to the body of the first call and leave out the second. The answer
is then `{ "ok": true, "name": "storefront", "kind": "apps", "deployed": true }`.

```bash title="Wait for a deploy to finish"
while true; do
  status=$(curl -fsS -H "Authorization: Bearer $GEASS_TOKEN" $GEASS/api/v1/apps/storefront/status) || break
  echo "$status" | jq -e '.state == "failed" or .build.phase == "Failed" or (.ready and .pendingChanges == 0 and ((.build.phase // "Succeeded") == "Succeeded"))' >/dev/null && break
  sleep 3
done
echo "$status" | jq '{state, message, build}'
```

A deploy is done when the status says `ready` with no pending changes and a successful build (or no build), and failed when `state` is
`failed` or the build's `phase` is `Failed`; `message` and `build.failureReason` say why. The command line's `--wait` flag uses the
same rule.

```bash title="Change a variable, set a secret, deploy"
curl -fsS -X POST -H "Authorization: Bearer $GEASS_TOKEN" -H "Content-Type: application/json" \
  $GEASS/api/v1/apps/storefront/config/set -d '{"key": "LOG_LEVEL", "value": "debug"}'
curl -fsS -X POST -H "Authorization: Bearer $GEASS_TOKEN" -H "Content-Type: application/json" \
  $GEASS/api/v1/apps/storefront/secrets/set -d "{\"key\": \"STRIPE_API_KEY\", \"value\": \"$STRIPE_API_KEY\"}"
curl -fsS -X POST -H "Authorization: Bearer $GEASS_TOKEN" $GEASS/api/v1/apps/storefront/deploy
```

```bash title="Change settings and deploy in one call"
curl -fsS -X PATCH -H "Authorization: Bearer $GEASS_TOKEN" -H "Content-Type: application/json" \
  $GEASS/api/v1/apps/storefront -d '{"image": "ghcr.io/acme/storefront:1.4.3", "deploy": true}'
```

```bash title="Let a service sleep after 10 idle minutes"
curl -fsS -X PATCH -H "Authorization: Bearer $GEASS_TOKEN" -H "Content-Type: application/json" \
  $GEASS/api/v1/apps/storefront -d '{"scaleToZero": "on", "idleSeconds": 600}'
```

```bash title="Roll back to the previous deploy"
curl -fsS -H "Authorization: Bearer $GEASS_TOKEN" $GEASS/api/v1/apps/storefront/deployments \
  | jq -r '.items[] | [.id, .createdAt, .image] | @tsv'
curl -fsS -X POST -H "Authorization: Bearer $GEASS_TOKEN" -H "Content-Type: application/json" \
  $GEASS/api/v1/apps/storefront/rollback -d '{"revision": "storefront-x7k2p"}'
```

Geass keeps the last 12 deploy records for each service. A project token can list them on `/api/v1`, so it can roll back too.

```bash title="A service's recent errors (works for a project token)"
curl -fsS -H "Authorization: Bearer $GEASS_TOKEN" "$GEASS/api/v1/apps/storefront/logs?level=error&tail=100" \
  | jq -r '.lines[] | "\(.t) \(.pod) \(.line)"'
```

```bash title="Errors in the last hour (needs an observer or admin token)"
curl -fsS -H "Authorization: Bearer $GEASS_TOKEN" \
  "$GEASS/api/v1/projects/proj-1a2b3c4d/logs?environment=production&level=error&since=1h" \
  | jq -r '.lines[] | "\(.t) \(.service) \(.line)"'
```

```bash title="Require a node tag for a project, then see what is left to move"
curl -fsS -X POST -H "Authorization: Bearer $GEASS_TOKEN" -H "Content-Type: application/json" \
  $GEASS/api/v1/projects/proj-1a2b3c4d/compliance -d '{"requiredTags": ["hipaa"]}'
curl -fsS -H "Authorization: Bearer $GEASS_TOKEN" $GEASS/api/v1/projects/proj-1a2b3c4d/compliance
```

```bash title="Create an observer token for a monitoring script (needs an admin session)"
curl -fsS -b cookies.txt -X POST $GEASS/api/v1/tokens -H "Origin: $GEASS" -H "Content-Type: application/json" \
  -d '{"name": "monitoring", "scope": "workspace", "role": "observer", "expiresInDays": 90}'
```

`cookies.txt` comes from the sign-in example under [Sign in and sessions](#sign-in-and-sessions).

### Project tokens on `/api/v1`

A project token keeps its limits on `/api/v1`. It can call `GET /me`, `GET /version`, `GET /apps` (only its own environment),
`GET /apps/{name}` and the app routes marked **project token** in the table above, `GET /token/services` and `POST /token/deploy`.
Everything else answers `403` `this token cannot access that endpoint`, including `GET /openapi.json`, `/projects`, `/databases`, `/nodes` and
`/usage`. A service in another project or environment answers `404` `app not found`, like a service that does not exist, and a body
field `project` or `environment` that names another environment is refused.

## Dashboard routes

The rest of this page documents the dashboard's own routes. They take form-encoded bodies, and a `/api/v1` write forwards to one
of them. Use them when you need something `/api/v1` does not list, or to understand what a `/api/v1` write does.

**Reads and writes.** Reads are `GET` and return JSON (two endpoints stream, see below). Every change is a `POST` with a form-encoded
body, which `curl --data-urlencode` sends. These routes read form fields, not JSON bodies. Under `/api/` every `POST` is answered in
JSON, whatever `Accept` header you send, except that a missing resource gets a plain-text `404`. A method other than `GET` or
`POST` (`PUT`, `DELETE`, `HEAD`) on an `/api/` path gets `405`, and a `GET` on a path that only exists for `POST` gets a plain-text
`404`. A `POST` to an unknown `/api/...` path returns JSON `{"error": "not found"}`.

### Sign in and sessions

| Method and path | Access | Fields | Response |
| --- | --- | --- | --- |
| `POST /api/login` | Public | `username`, `password` | `{"ok":true,"authenticated":true,"username":"admin","role":"admin","canMutate":true}` and the `geass_session` cookie. `400` `invalid username or password` on failure or lockout |
| `POST /api/logout` | Public | none | `{"ok":true,"authenticated":false}`; ends every session of that user |
| `GET /api/session` | Public | none | `{"authenticated":true,"username":"admin","role":"admin","canMutate":true}`, or `{"authenticated":false}` |
| `POST /api/auth/device`, `POST /api/auth/token` | Public | As in [Sign in from a program](#sign-in-from-a-program) | The same answers as `/api/v1/auth/...` |
| `POST /api/auth/device/approve` | Admin session | `userCode`, `role`, `expiresInDays`, `deny` | The same as `/api/v1/auth/device/approve` |

```bash title="Sign in with a cookie jar"
curl -fsS -c cookies.txt -X POST $GEASS/api/login \
  -H "Origin: $GEASS" \
  --data-urlencode "username=admin" \
  --data-urlencode "password=replace-with-your-passphrase"
curl -fsS -b cookies.txt $GEASS/api/session
```

Password sign-in exists only here; there is no `/api/v1/login`. Dashboard users are `admin` or `viewer`; `observer` is a token role.

### API tokens

Only a signed-in admin session can manage tokens. Not even an admin token can.

| Method and path | Access | Fields | Response |
| --- | --- | --- | --- |
| `GET /api/tokens` | Admin session | optional `project` | `{"items":[ token, ... ]}`, newest first |
| `POST /api/tokens/create` | Admin session | `name`, `scope` (`workspace` or `project`), `role` (`admin`, `observer` or `viewer`, workspace only, default `viewer`), `project` and `environment` (project only), `expiresInDays` (0 to 3650, default never) | `{"ok":true,"token":"gsw_...","item":{...}}`; the token is shown once |
| `POST /api/tokens/revoke` | Admin session | `id` | `{"ok":true}`; `404` `token was not found` |
| `GET /api/token` | Any token | none | The calling token: `id`, `name`, `scope`, `project`, `environment`, `role`, `createdBy`, `createdAt`, `expiresAt`, `lastUsedAt`, `expired`. A session cookie gets `400` `this endpoint needs an API token` |
| `GET /api/token/services` | Any token | `project` and `environment` (workspace tokens only) | `{"project","environment","services":[{"id","name","kind","source","ready","pendingChanges"}]}` |
| `POST /api/token/deploy` | Project token | none | `{"ok":true,"deployed":2}`: deploys every pending change in the token's environment. Anyone else gets `403` |

Details and CI examples are in [Users and API tokens](/docs/guides/users-and-tokens/).

### Read model

| Method and path | Access | Meaning |
| --- | --- | --- |
| `GET /api/bootstrap` | Viewer | Everything the dashboard shows, in one response |
| `GET /api/usage` | Viewer | What each project reserves and uses |
| `GET /api/events` | Viewer | A server-sent event stream of changes |
| `GET /api/templates` | Observer | Built-in and saved templates |

#### Bootstrap

`GET /api/bootstrap` returns one object. Most keys are Kubernetes lists of the Geass resources, so each has `items` with the
resource's `metadata`, `spec` and `status`. `items` is `null` when empty, so use `// []` in `jq`.

| Key | Contents |
| --- | --- |
| `projects`, `apps`, `databases`, `logicalDatabases`, `objectStores` | The [GeassProject](/docs/reference/resources/geass-project/), [GeassApp](/docs/reference/resources/geass-app/), [GeassDatabase](/docs/reference/resources/geass-database/), [GeassLogicalDatabase](/docs/reference/resources/geass-logical-database/) and [GeassObjectStore](/docs/reference/resources/geass-object-store/) resources |
| `deployments`, `builds` | Deploy history and builds (`builds` is empty for viewers and observers) |
| `clusters`, `haReadiness`, `backupArchives` | The cluster (its `status.conditions` include `ClusterIssuerReady`, which says whether HTTPS certificates can be issued), its HA check and database backups |
| `cloudConnections`, `platformConfig` | Cloud connections and the platform configuration (empty for viewers and observers) |
| `platform` | A summary: `hasDashboardURL`, `hasGitHubApp`, `hasCloudflare`, `cloudflareReady`, `dashboardURL`, `haReady`, `healthyNodes`, `awsAvailable`, `planetScaleAvailable`, `minioAvailable`, `capacity`, `defaultRegion`, `storageExpandable`, `fixedSizeDatabases`, `defaultStorage`, `session`, and `cloudflareZoneId` and `cloudflareZoneName` for admins |
| `metrics` | Three headline numbers: running pods, nodes, CPU cores used |

Items are sorted oldest first. Secrets are never included, and for a viewer or an observer the fields described in
[Users and API tokens](/docs/guides/users-and-tokens/#roles) are removed.

**See pending changes.** A service that is a draft or has undeployed changes carries the annotation
`geass.dev/pending-changes` (a comma-separated mix of `created`, `settings`, `variables`), and a service that was never deployed
has `spec.deploy.enabled` false. Databases and buckets use the same annotation.

```bash title="Pending changes in every environment"
curl -fsS -H "Authorization: Bearer $GEASS_TOKEN" $GEASS/api/bootstrap \
  | jq -r '(.apps.items // [])[]
      | select(((.metadata.annotations["geass.dev/pending-changes"] // "") != "") or ((.spec.deploy.enabled // false) == false))
      | [.metadata.name, .spec.project, .spec.environment, (.metadata.annotations["geass.dev/pending-changes"] // "draft")]
      | @tsv'
```

With a project token, `GET /api/token/services` reports `pendingChanges` (a count) for each service, and `GET /api/v1/apps` reports it too.

#### Usage

`GET /api/usage` returns `cpuAllocatableMillis`, `memoryAllocatableBytes`, `nodes`, `actualAvailable`, a `projects` list and a
`platform` total, and `regions` when nodes have regions. Each project has `pods`, `cpuReservedMillis`, `memoryReservedBytes`,
`storageReservedBytes`, and (when Prometheus answers) `cpuUsingMillis`, `memoryUsingBytes`, `storageUsingBytes`, plus
`shareOfCpu` and `shareOfMemory` from 0 to 1. See [Usage and capacity](/docs/guides/usage-and-capacity/).

#### Events

`GET /api/events` (or `GET /api/v1/events`) is a server-sent event stream. It sends `event: connected` first, then `event: change` with
`{"type":"change","resource":"apps","name":"storefront","op":"update"}` and a `ping` every 25 seconds. `resource` is one of
`projects`, `apps`, `databases`, `logical-databases`, `object-stores`, `deployments`, `builds`, `cloud-connections`, `clusters`,
`platform-config`, `ha-readiness`, `backup-archives` (or `resources` for an object the stream does not name), and `op` is `add`, `update`, `delete`, `deploy`, `create` or `retry-provision`. Bursts are
coalesced for 250 milliseconds, so treat an event as "something changed, reload".

### Projects

All of these are admin writes.

| Method and path | Fields | Meaning |
| --- | --- | --- |
| `POST /api/projects/create` | `region` (optional) | Creates a project with a generated ID and name and one environment, `production`. `201` `{"project":"proj-1a2b3c4d"}` |
| `POST /api/projects/{project}/settings/save` | `displayName` (required), `environments` (repeat; at least one), `region` | Renames the project, sets its environments, and optionally its region. The region cannot change while the project has in-cluster databases |
| `POST /api/projects/{project}/settings/delete` | `confirmName` (the display name) | Deletes the project, its services, databases, logical databases, buckets, cloud connections and GitHub connection, and revokes its tokens. Delete AWS buckets first and wait for them to go; see [Cloud connections](/docs/guides/connections/#remove-an-aws-connection) |
| `POST /api/projects/{project}/environments/create` | `environment` | Adds an environment (a lowercase DNS label; the project ID plus the name must fit in 63 characters) |
| `POST /api/projects/{project}/environments/archive` | `environment`, `confirmName` (the environment name) | Deletes an environment and everything in it. `{"ok":true,"deleted":3}`. The last environment cannot be removed |
| `POST /api/projects/{project}/variables/save` | `name` (`^[A-Z_][A-Z0-9_]*$`), `environment`, `value`, `secret` (`on` to store it as a secret) | Creates or updates a shared variable |
| `POST /api/projects/{project}/variables/delete` | `name`, `environment` | Deletes a shared variable |
| `POST /api/projects/{project}/deploy` | `environment` (default: the first) | Deploys every pending service, database and bucket in the environment, in that order. `{"ok":true,"deployed":2}`. If the cluster lacks room for a service or database it stops there with `400` and keeps what it already deployed |
| `POST /api/projects/{project}/pr-environments/save` | `enabled` (`on`), `baseEnvironment`, `maxOpen` (1 to 25, default 5) | Configures [pull request environments](/docs/guides/pull-request-environments/) |
| `POST /api/projects/{project}/pr-environments/close` | `number`, `repository` | Removes one pull request environment. `{"ok":true,"deleted":...}` |
| `POST /api/projects/{project}/webhooks/save` | `url`, `event` (repeat: `deployment.started`, `deployment.succeeded`, `deployment.failed`), `disabled` (`on`), `id` (to update) | Creates (`{"ok":true,"id","secret":"whsec_..."}`, the secret shown once) or updates (`{"ok":true,"id"}`) a webhook. Up to 20. The `url` must be `http://` or `https://` and may not point at localhost or, by default, a private address; sending no `event`, or all three, means every event |
| `POST /api/projects/{project}/webhooks/delete` | `id` | Deletes a webhook and its secret |
| `POST /api/projects/{project}/webhooks/rotate` | `id` | New signing secret, `{"ok":true,"id","secret"}` |
| `POST /api/projects/{project}/webhooks/test` | `id` | Sends a test event, `{"ok":true,"delivery":{...}}`; `ok` is `false` when the delivery failed |
| `POST /api/projects/{project}/backup-storage` | `connection` (an AWS connection), `bucket`, `region`; or `remove=on` | Chooses or clears where the project's database backups go |
| `POST /api/projects/{project}/network` | `isolation` (`Project` or `None`), `allowFromProjects` (repeat, up to 20) | Sets [network isolation](/docs/guides/network-isolation/) |
| `POST /api/projects/{project}/compliance` | `requiredTags` (repeat, or comma-separated; at most 8) | Sets the node tags every workload of the project must run on. Each tag must exist on a node in the project's region and be reserved on every node that carries it, and a project that already has databases cannot take a tag its databases are not on. `{"ok":true,"requiredTags":[...]}`. See [Compliance tags](/docs/guides/compliance-tags/) |
| `GET /api/projects/{project}/compliance` | none | Viewer. `{"requiredTags","region","databases":[{"name","compliant","missing"}],"openNodes"}`: what the project requires and which databases fall short |
| `POST /api/projects/{project}/github/install` | `environment` | `{"ok":true,"url":"https://github.com/apps/..."}`: open it to install the GitHub App |
| `POST /api/projects/{project}/github/disconnect` | `environment` | Unlinks the project from GitHub |
| `GET /api/projects/{project}/github/repos` | none | `{"items":[{"FullName","DefaultBranch","Private","HTMLURL"}]}` (observer) |

### Services

A service is a `GeassApp`. `{app}` is its resource name.

#### Create and change

| Method and path | Fields | Meaning |
| --- | --- | --- |
| `POST /api/apps/create` | See below | Creates a draft service. `{"ok":true,"name":"storefront","kind":"apps"}` |
| `POST /api/apps/{app}/update` | Any subset of the fields below, plus `deploy=on` | Saves settings, and deploys them when `deploy=on` |
| `POST /api/apps/{app}/deploy` | none | Deploys the service |
| `POST /api/apps/{app}/scale` | `replicas` (0 to 1000) | Changes the replica count at once, without a deploy |
| `POST /api/apps/{app}/rollback` | `revision` (a `GeassDeployment` name) | Redeploys an earlier revision: an image service gets the earlier image, a Git service only gets the earlier replica count, and a function cannot be rolled back |
| `POST /api/apps/{app}/build` | none | Builds the service's branch again: puts its newest build back to the start, and the operator builds the head of the branch as it is now. It makes no build of its own, and answers an error for a service that has none yet; see [GeassBuild](/docs/reference/resources/geass-build/#how-the-dashboard-uses-builds) |
| `POST /api/apps/{app}/build/cancel` | none | Cancels the newest build |
| `POST /api/apps/{app}/retry-provision` | none | Retries a failed deploy |
| `POST /api/apps/{app}/delete` | `confirmName` (the app name) | Deletes the service |
| `POST /api/apps/{app}/display-name` | `displayName` | Sets the label shown in the dashboard |
| `POST /api/apps/{app}/attach` | `name` (a database or bucket), `kind` (`database` or `object-store`) | Adds `DATABASE_URL` (`REDIS_URL` for Redis) or `S3_ENDPOINT` as a reference to the resource's connection Secret |
| `POST /api/apps/{app}/networking/delete` | `confirmName` | Clears the host and TLS |
| `POST /api/apps/{app}/shared-variables/save` | `sharedVariable` (repeat) | Selects which shared variables the service receives |

Fields for `create`: `name`, `project`, `environment`, `source` (`git`, `function`, or omit for an image), `image`, `port` (default
8080), `host`, `metrics` (`on`), `replicas`, `cpu` and `memory`, `repository`, `branch`, `connectionRef`, `dockerfile`, `context`,
`waitForCI`, `runtime` and `code` (functions), `mode` (functions: `http` or `event`), `functionTriggers` (functions: a schedule as a JSON
object or a JSON list of them, see below), `draft`, `autoscaling`, `minReplicas`, `maxReplicas`, `targetCPU`, `scaleToZero`,
`idleSeconds`, `scaleToZeroMax`. A service made with the API is always a draft: call a deploy next.

Fields for `update`: `image`, `repository`, `branch`, `dockerfile`, `context`, `waitForCI`, `watchPatterns`, `functionRuntime`,
`functionCode`, `functionMode`, `functionTriggers`, `port`, `replicas`, `cpu` and `memory` (`cpuRequest` and `memoryRequest` are aliases), `autoscaling`,
`minReplicas`, `maxReplicas`, `targetCPU`, `scaleToZero`, `idleSeconds`, `scaleToZeroMax`, `host`, `tls`, `dnsVerification`, `metrics`, `command`, `args`, `workingDir`,
`buildCache`, `readinessPath`, `readinessPort`, `readinessPeriod`, `readinessTimeout`, `readinessFailureThreshold`, the same with a
`liveness` prefix, `runOnTags` (admin session only; tokens are refused), and `project` and `environment` (which move the service).
Switches take `on`. `cpu` must be one of `100m`, `250m`, `500m`, `1`, `2`, and `memory` one of `128Mi`, `256Mi`, `512Mi`, `1Gi`, `2Gi`,
`4Gi`. A field you leave out keeps its value.

Sleep when idle (`scaleToZero`) takes `on` or `off`. `idleSeconds` is 30 to 86400 (default 300) and `scaleToZeroMax`, the most copies
that run when the service is awake, is 1 to 100 (default 3). A field you leave out keeps its value, so sending only `idleSeconds`
neither switches the setting on nor off. It cannot be combined with CPU autoscaling: `Scale to zero decides how many copies run, so turn
CPU autoscaling off to use it`. It needs the cluster's serverless add-on and a public host; see [Scale to zero](/docs/guides/scale-to-zero/).
It is a live setting, so it takes effect without a deploy.

A function's schedules are sent as `functionTriggers`: each value is one JSON object `{"name", "schedule", "path", "payload"}` or a JSON
list of them, and an empty value removes them all. A function has at most 8, `name` is a lowercase DNS-style label of up to 32 characters,
`schedule` is a cron expression in UTC, `path` starts with `/`, and `payload` is the JSON body of the call. They start at the next deploy.
See [Functions](/docs/guides/functions/).

`update` answers with the pending state:

```json title="Response of update"
{
  "ok": true,
  "pending": ["settings"],
  "deployed": true,
  "title": "You made these changes",
  "detail": "Settings. Do you want to deploy?",
  "button": "Deploy to update",
  "applied": "deploy",
  "liveChanged": false
}
```

`applied` is `none` (nothing changed), `live` (everything took effect already), `deploy` (a change waits for a deploy) or `deployed`
(the save was a deploy, with `deploy=on`). `title`, `detail` and `button` are the dashboard's own wording and may change.
`deploy` answers with the same fields except `applied` and `liveChanged`. `update` accepts `context` (the root directory of the build) and `watchPatterns` (which pushes build, one pattern per line)
for a repository service. A `context` or `dockerfile` that leaves the repository is refused.

#### Variables and secrets

| Method and path | Fields | Response |
| --- | --- | --- |
| `POST /api/apps/{app}/config/set` | `key`, `value` | `{"ok":true,"config":{...},"pending":["variables"]}` |
| `POST /api/apps/{app}/config/delete` | `key` | Same |
| `POST /api/apps/{app}/config/raw` | `configJSON` (a JSON object of strings that replaces all config) | Same |
| `POST /api/apps/{app}/secrets/set` | `key`, `value` | `{"ok":true,"secrets":["STRIPE_API_KEY"],"pending":["variables"]}`; never returns values |
| `POST /api/apps/{app}/secrets/delete` | `key` | Same |
| `POST /api/apps/{app}/env/delete` | `key` | Removes an attached reference such as `DATABASE_URL`. Same response as `config/set` |
| `GET /api/apps/{app}/variables` | none | `{"secrets":[names],"sharedVariableRefs":[...],"env":[{"name","kind"}],"config":{...}}`: secrets and attached references as names only, plain `config` with its values (observer, project token) |

Changes to variables need a deploy before the pods see them. See [Environment variables](/docs/guides/environment-variables/).

#### Terminal

`POST /api/apps/{app}/console/create` (fields `target` as `pod|container`, and `command`, up to eight words) records an audited
console session and returns `{"ok":true,"session":"..."}`. Open `GET /api/apps/{app}/console/ws?session=<name>&cols=100&rows=30` as a
WebSocket. Binary frames carry keystrokes in and output out; a text frame `{"type":"resize","cols":120,"rows":32}` resizes and the
server sends `{"type":"exit"}` at the end. The session ends after five minutes. Creating a session needs an admin.

### Databases and buckets

#### Databases

| Method and path | Fields | Meaning |
| --- | --- | --- |
| `POST /api/databases/create` | `name`, `project`, `environment`, `engine` (`postgres`, `mysql`, `sqlite`, `redis`; case does not matter, anything else becomes Postgres), `placement` (`InCluster` or `External`), `provider` (`PlanetScale` or `AWS`), `mode` (`Create` or `Connect`), `highAvailability`, `databaseName`, `clusterSize` (PlanetScale only), `host`, `port`, `username`, `password`, `version`, `storageSize`, `connectionRef`, `cpu`, `memory`, `runOnTags` | Creates a draft. `{"ok":true,"name":"orders-db","kind":"databases"}` |
| `POST /api/databases/{db}/update` | `version`, `storageSize`, `cpu`, `memory`, `backups` (`on` or `off`), `backupRetentionDays` (1 to 365), `clusterSize` (PlanetScale only, and only until the database has been created there), `runOnTags`, `environment` | `{"ok":true,"applied":"live","liveChanged":true}` |
| `GET /api/planetscale/cluster-sizes` | `project`, `connection`, `engine` (`mysql` or `postgres`) as query fields | Lists the cluster sizes a PlanetScale connection can create: `{"items":[{"name","displayName","cpu","ramBytes","storageBytes","development","production","metal","provider","architecture","monthlyUsd","replicaMonthlyUsd"}]}`. Observer. `400` for a bad connection or engine, `502` `could not load PlanetScale cluster sizes`. See [Cloud connections](/docs/guides/connections/#choosing-a-size) |
| `POST /api/databases/{db}/deploy` | none | Deploys the draft or the pending change |
| `POST /api/databases/{db}/retry-provision` | none | Retries a failed deploy |
| `POST /api/databases/{db}/delete` | `confirmName` | Deletes it (see [Backups and restore](/docs/guides/backups-and-restore/) for the final backup) |
| `POST /api/databases/{db}/display-name` | `displayName` | Sets the label |
| `POST /api/databases/{db}/query` | `query` | Runs a statement with `psql -c`, `mysql -e`, `redis-cli` or `rqlite -e` inside the database pod: `{"output":"..."}`. Admin. Problems such as a database that is not running come back as `output` text with status `200`. `GET` on this path returns `405`. Nothing records the statements, see [Security](/docs/operations/security/#auditing) |
| `POST /api/databases/restore` | `archive`, `restorePoint` or `time` (RFC 3339), `environment` | Creates and deploys a new database from backups. [Backups and restore](/docs/guides/backups-and-restore/) |
| `POST /api/shared-databases/create` | `engine` (`Postgres` or `MySQL`), `name`, `region`, `runOnTags`, `version`, `storageSize`, `highAvailability`, `cpu`, `memory` | Starts a cluster-wide server at once. `201` `{"database":"cluster-postgres"}`. [Shared database servers](/docs/guides/shared-database-servers/) |
| `POST /api/logical-databases/create` | `name`, `project`, `environment`, `server`, `database`, `connectionLimit` (1 to 1000) | Creates a database on a server. It needs no deploy call |
| `POST /api/logical-databases/{db}/retry-provision` | none | Retries |
| `POST /api/logical-databases/{db}/delete`, `.../display-name` | `confirmName`, `displayName` | Deletes, relabels |

An external database (PlanetScale or AWS RDS) needs `placement=External`, `provider`, and either `mode=Create` with a
connection (PlanetScale creates the database and a login itself, as a MySQL or Postgres database from `engine`) or `host`,
`port`, `username` and `password` for one that exists. The dashboard sends neither `mode` nor those fields for an AWS RDS draft, so one made there cannot become ready, and its
**PlanetScale** row is greyed out, so create PlanetScale databases through this API; see [Cloud connections](/docs/guides/connections/#planetscale).

#### Buckets

:::caution
Project buckets currently stay at "waiting for deployment" after a deploy. See [GeassObjectStore](/docs/reference/resources/geass-object-store/).
:::

| Method and path | Fields | Meaning |
| --- | --- | --- |
| `POST /api/object-stores/create` | `project`, `environment`, `name`, `placement` (`InCluster` or `External`), `engine` (`MinIO` or `S3`), `region`, `createBucket`, `connectionRef`, `bucket`, `draft` | Creates a draft bucket. Pass `cluster=on` (with `region`, `runOnTags`, `name`) to create the cluster's MinIO server instead |
| `POST /api/object-stores/{store}/update` | `connectionRef`, `bucket`, `createBucket`, `project`, `environment` | `{"ok":true,"applied":"deploy","liveChanged":false}` |
| `POST /api/object-stores/{store}/deploy` | none | Deploys it |
| `POST /api/object-stores/{store}/retry-provision`, `/delete`, `/display-name` | as above | |
| `GET /api/object-stores/{name}/logs`, `GET /api/object-stores/{name}/runtime` | none | The cluster MinIO server only (its name is `cluster-minio`): `{"lines":"..."}` of its pod logs, and `{"pods":[...],"endpoint","consoleURL","namespace","chart"}`. Observer |

### Templates

| Method and path | Fields | Meaning |
| --- | --- | --- |
| `GET /api/templates` | none | `{"items":[template, ...]}`: built-in templates, then saved ones (observer) |
| `POST /api/templates/{id}/deploy` | `project`, `environment` | Creates the template's resources as drafts. `{"ok":true,"created":[...]}` |
| `POST /api/templates/create` | `project`, `environment`, `name`, `description` | Saves an environment as a template. `{"ok":true,"template":{...},"skipped":["..."]}`: `skipped` names what it left out (external databases, logical databases, buckets). A function's mode and schedules are saved with it |
| `POST /api/templates/{id}/delete` | none | Deletes a saved template (built-in ones cannot be deleted) |

See [Templates](/docs/guides/templates/).

### Metrics and logs

These need Prometheus (and, for history, the logging add-on) to be installed. See [Observability](/docs/guides/observability/).

| Method and path | Access | Query | Meaning |
| --- | --- | --- | --- |
| `GET /api/apps/{app}/metrics` | Viewer, project token | `window` | Series for one service |
| `GET /api/projects/{project}/metrics` | Viewer | `window`, `environment` | Series for a project |
| `GET /api/databases/{db}/metrics` | Viewer | `window` | Series for a database |
| `GET /api/cluster/metrics` | Viewer | `window` | Series for the whole cluster |
| `GET /api/operator/metrics` | Viewer | `window` | Series for the operator itself |
| `GET /api/apps/{app}/logs` | Observer, project token | none | The dashboard's own route: the last 200 lines of each pod as one block of text, `{"lines":"# pod-name\n..."}`. `/api/v1/apps/{name}/logs` answers structured lines instead |
| `GET /api/apps/{app}/runtime` | Observer, project token | none | `{"pods":[{"name","phase","container"}]}` |
| `GET /api/projects/{project}/logs` | Observer | `environment`, `service`, `q`, `level`, `since`, `tail`, `previous` | Merged live logs |
| `GET /api/projects/{project}/logs/stream` | Observer | same | The same as server-sent events, following new lines |
| `GET /api/projects/{project}/logs/history` | Observer | `environment`, `service`, `q`, `level`, `since`, `limit` | Stored history from the log store |
| `GET /api/{apps,databases,object-stores,logical-databases}/{name}/deployments/{id}/logs` | Observer (apps: also project token) | none | `{"id","sections":[{"id","title","lines"}]}` for one deploy |

`window` is `15m` (default), `1h`, `6h` or `24h`. A response is `{"window","step","available","series":{...}}`, where each series is a
list of `{"t": unixSeconds, "v": number}` points. Series are `cpu` (cores), `memory` (bytes), `network` and `networkIn` (bytes per
second), `requests` (per second, services), `storage` (bytes, databases), and `nodeCpu` and `nodeMemory` (cluster). The operator's series
are `reconcileErrors` and `statusWrites` and `statusSkipped` (per second), `reconcileTime` and `apiLatency` (the 99th percentile, in seconds), `apiRequests`
(per second) and `queueDepth`. When Prometheus cannot be reached, `available` is `false` with a `message`.

Log queries: `environment`; `service` (a service or database name); `q` (up to 200 characters); `level` (`error`, `warn`, `info`, `debug`);
`since` (a Go duration such as `15m`; up to 7 days live, 90 days in history, default 1 hour in history); `tail` (1 to 1000, default 200);
`previous` (`true` for output from before a container's last restart); `limit` (history only, 1 to 3000, default 500). A line is
`{"t","service","pod","container","line","level","previous"}`. The response also has `services`, `truncated` and `note`; history adds
`available` and `retentionDays`, and returns `{"available":false,"message":"..."}` when the logging add-on is off, and for a project that requires node tags:
the shared log store never receives those logs, so only live logs work.

```bash title="Errors in the last hour"
curl -fsS -H "Authorization: Bearer $GEASS_TOKEN" \
  "$GEASS/api/projects/proj-1a2b3c4d/logs?environment=production&level=error&since=1h" | jq -r '.lines[] | "\(.t) \(.service) \(.line)"'
```

```bash title="CPU and memory for the last hour"
curl -fsS -H "Authorization: Bearer $GEASS_TOKEN" \
  "$GEASS/api/apps/storefront/metrics?window=1h" | jq '.series | {cpu: (.cpu | length), memory: (.memory | length)}'
```

### Cluster and platform

| Method and path | Fields | Meaning |
| --- | --- | --- |
| `POST /api/nodes/{node}/placement` | `region`, `zone`, `tags` (repeat), `reserved` (repeat), `compliance` (repeat) | Sets a node's [region, zone and tags](/docs/guides/scaling-and-placement/). A compliance tag must be one of the node's `tags`, is always reserved, and a tag some project requires becomes a compliance tag automatically; see [Compliance tags](/docs/guides/compliance-tags/). A node has at most 16 tags. `{"ok":true,"warnings":[...]}` |
| `POST /api/platform/default-region` | `region` | Sets the cluster's default region |
| `POST /api/ha-readiness/check` | none | Creates the high availability check |
| `POST /api/settings/domain/save` | `domain`, `subdomain`, `exposure` (`ingress` or `cloudflare-tunnel`), `tunnelCNAMETarget`, `cloudflareZoneId` | Sets the dashboard domain |
| `POST /api/settings/domain/verify` | none | `{"ok":true,"state":"success","message":"..."}`; `ok` is `false` for any other `state`. When HTTPS is not reachable, `message` can end with the cluster's `ClusterIssuerReady` message, for example when no `acmeEmail` is set |
| `POST /api/settings/github/save` | `appID`, `clientID`, `slug`, `clientSecret`, `webhookSecret`, `privateKey` | Stores the GitHub App |
| `POST /api/settings/github/test`, `/clear` | `confirm=remove-github` for clear | Tests or removes it |
| `GET /api/settings/github` | none | Callback and webhook URLs and the state (observer; admins also get the manifest used to register the app) |
| `POST /api/settings/cloudflare/discover` | `apiToken` | `{"accounts":[...],"zones":[...]}` without saving anything |
| `POST /api/settings/cloudflare/save` | `apiToken`, `accountId`, `zoneId`, `zoneName`, `zonesJson`, `keepExisting` | Connects Cloudflare |
| `POST /api/settings/cloudflare/refresh`, `/clear` | none | Re-reads zones, or disconnects |
| `GET /api/settings/cloudflare` | none | `{"connected","ready"}`; admins also get account, zone and tunnel IDs |
| `POST /api/settings/registry/save` | `repository`, `username`, `password`, `keepExisting` | Sets the registry that repository builds push to. The login is optional: both `username` and `password`, or neither. It is stored as the Secret `platform-registry` in `geass-system`. `keepExisting=on` keeps the stored login when only the repository changes |
| `POST /api/settings/registry/clear` | none | Removes the registry and its login |
| `GET /api/settings/registry` | none | `{"repository","configured","hasLogin"}`. It never returns the login |
| `POST /api/cloud-connections/create` | `name`, `provider` (`AWS` or `PlanetScale`, default `AWS`), `project` (the `proj-...` ID, required), and `accessKeyId`, `secretAccessKey`, `region` or `token`, `organization` | Adds an AWS or PlanetScale connection. `{"ok":true}`. It writes the Secret `<name>-credentials` before it checks the project |

Details of the last group are in [Cloud connections](/docs/guides/connections/).

### Project token endpoints

A project token can call only these, and only for services in its own project and environment. On `/api/v1` the same routes work with
JSON bodies, and a project token can also call `GET /api/v1/apps`, `/apps/{name}`, `/apps/{name}/status`, `/apps/{name}/deployments`
and `/apps/{name}/logs/stream`.

| Method and path | Meaning |
| --- | --- |
| `GET /api/token`, `GET /api/token/services` | Describe the token; list services and databases |
| `POST /api/token/deploy` | Deploy pending changes in the token's environment |
| `GET /api/apps/{app}/logs`, `/runtime`, `/variables`, `/metrics` | Read |
| `GET /api/apps/{app}/deployments/{id}/logs` | Read a deploy's logs |
| `POST /api/apps/{app}/deploy`, `/rollback`, `/scale`, `/retry-provision`, `/update` | Change the service |
| `POST /api/apps/{app}/build`, `/build/cancel` | Build |
| `POST /api/apps/{app}/config/set`, `/config/delete`, `/config/raw` | Edit configuration |
| `POST /api/apps/{app}/secrets/set`, `/secrets/delete`, `/env/delete` | Edit secrets and references |

Anything else returns `403` `this token cannot access that endpoint`.

### Endpoints that are not for scripts

These exist for the browser flow:

| Path | Purpose |
| --- | --- |
| `GET /geass-probe` | A public health answer, `{"ok":true,"service":"geass-dashboard",...}`, used to verify the dashboard's address |
| `POST /webhooks/github` | GitHub's webhook; checked by HMAC signature |
| `GET /github/callback`, `GET /settings/github/manifest/callback` | GitHub redirects here after install or app creation; signed-in admins only |
| `GET /api/events`, `GET /api/apps/{app}/console/ws` | Streams used by the dashboard |
| `POST /api/apps/{app}/console/stream`, `POST /api/object-stores/cluster-minio/console/create` and `.../console/stream` | An older streaming form of the service terminal, and the same pair for the MinIO server. The dashboard uses neither |

### Workflows with the dashboard routes

```bash title="Create a service from an image and deploy it"
curl -fsS -X POST -H "Authorization: Bearer $GEASS_TOKEN" $GEASS/api/apps/create \
  --data-urlencode "name=storefront" \
  --data-urlencode "project=proj-1a2b3c4d" \
  --data-urlencode "environment=production" \
  --data-urlencode "image=ghcr.io/acme/storefront:1.4.2" \
  --data-urlencode "port=8080"

curl -fsS -X POST -H "Authorization: Bearer $GEASS_TOKEN" $GEASS/api/apps/storefront/deploy
```

```bash title="Change a setting, check what is pending, deploy"
curl -fsS -X POST -H "Authorization: Bearer $GEASS_TOKEN" $GEASS/api/apps/storefront/update \
  --data-urlencode "port=3000"
curl -fsS -X POST -H "Authorization: Bearer $GEASS_TOKEN" $GEASS/api/apps/storefront/secrets/set \
  --data-urlencode "key=STRIPE_API_KEY" --data-urlencode "value=$STRIPE_API_KEY"
# The response of each call lists "pending". To see pending changes everywhere, use the query under "Bootstrap".
curl -fsS -X POST -H "Authorization: Bearer $GEASS_TOKEN" $GEASS/api/projects/proj-1a2b3c4d/deploy \
  --data-urlencode "environment=production"
```

```bash title="Roll back to an earlier deploy"
curl -fsS -H "Authorization: Bearer $GEASS_TOKEN" $GEASS/api/bootstrap \
  | jq -r '(.deployments.items // [])[] | select(.spec.app == "storefront") | [.metadata.name, .metadata.creationTimestamp, .spec.image] | @tsv'
curl -fsS -X POST -H "Authorization: Bearer $GEASS_TOKEN" $GEASS/api/apps/storefront/rollback \
  --data-urlencode "revision=storefront-x7k2p"
```

A project token cannot read `/api/bootstrap`, so a script that uses one lists the revisions with `GET /api/v1/apps/{name}/deployments` instead.

## What happens under the hood

The API is a thin layer over Kubernetes. A `POST` reads the resource, changes its `spec` and annotations, and writes it back; the
controllers do the rest. You can see the same objects with `kubectl -n geass-system get geassapps,geassdatabases,geassobjectstores`.
A deploy sets `spec.deploy.enabled` to true, clears `geass.dev/pending-changes`, and records the deployed spec in
`geass.dev/last-deployed`. See [Resources overview](/docs/reference/resources/overview/).

`/api/v1` is a layer in front of that layer. It sits before authentication, so the rules for who may call what are the ones the dashboard
routes apply. For a `GET` it serves the reads that only `/api/v1` has (resource lists and single resources, `status`, `deployments`,
`connection`, `nodes`, the OpenAPI document and the device-code lookup) and passes every other `GET` to the dashboard route of the same name
without the `/v1`. For a write it maps the verb and path to a dashboard route (`POST /apps` to `POST /api/apps/create`, `PATCH /apps/{name}`
to `POST /api/apps/{name}/update`, `DELETE /apps/{name}` to `POST /api/apps/{name}/delete`), turns the JSON body into a form, and puts any
error in the `{"error","code"}` shape. The operator log records each request's method, path, status and duration, but not who sent it.

## Troubleshooting

**`400` `request origin could not be verified` on a cookie request.** Add an `Origin` header with the dashboard's address, or use a token.

**`401` on every call.** Check the header is `Authorization: Bearer <token>` with no extra quotes, and that the token has not
expired. `GET /api/v1/me` is the quickest test.

**`403` `viewer role cannot access this` with an observer token.** An observer can read, including logs, but any write is refused with this
message. Use an `admin` token or a project token.

**`403` `this token cannot access that endpoint`.** A project token called something outside its list. See
[Project tokens on /api/v1](#project-tokens-on-apiv1).

**`404 page not found` for a path that looks right.** Most "not found" answers from the dashboard routes are plain text. The resource name is wrong
(use the ID from `metadata.name`, not the display name), or the path has a typo. On `/api/v1` the same answer arrives as
`{"error":"404 page not found","code":"not_found"}`.

**`400` `there is no PUT /api/v1/...`.** A verb that has no mapping. Use `PATCH` on `/apps/{name}`, `/databases/{name}`,
`/object-stores/{name}` and `/projects/{name}`, and `POST` for actions.

**A write returns `{"ok":true}` but nothing changed.** The change is pending. Deploy it. For a service, check `pending` and `applied`
in the response.

**A switch does not turn off.** On `/api/v1`, `false` leaves the field out, which keeps its value. Send the string the handler expects,
such as `"scaleToZero": "off"`.

**`400` with a capacity message on deploy.** The cluster does not have room for the CPU and memory the change requests. See
[Usage and capacity](/docs/guides/usage-and-capacity/).

**Form fields seem ignored on a dashboard route.** You sent JSON. Use `--data-urlencode`, or call the same route under `/api/v1`.

## Related

- [Users and API tokens](/docs/guides/users-and-tokens/)
- [Command line](/docs/guides/command-line/) and [CLI reference](/docs/reference/cli/)
- [MCP server](/docs/guides/mcp-server/) and [MCP tools](/docs/reference/mcp-tools/)
- [Resources overview](/docs/reference/resources/overview/)
- [Projects and environments](/docs/guides/projects-and-environments/)
- [Observability](/docs/guides/observability/)
- [Security](/docs/operations/security/)
