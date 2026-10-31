---
title: "REST API"
description: "The Geass HTTP API: the versioned /api/v1, the dashboard routes, authentication, errors, every endpoint, and curl examples."
---

The operator serves an HTTP API next to the dashboard. The API has two parts:

- The versioned part, `/api/v1`, is for scripts, CI, the [`geass` command line](/docs/guides/command-line/), and the
  [MCP server](/docs/guides/mcp-server/). It takes JSON, it gives every error in one shape, and an OpenAPI document
  describes it.
- Under the versioned part are the dashboard's own `/api` routes. The dashboard calls these routes, and `/api/v1`
  forwards to them.

To call either part, use a workspace or project [API token](/docs/guides/users-and-tokens/), or a signed-in session.

:::note
`/api/v1` is the version of the API. The resources that it returns inside `spec` and `status` are the `v1alpha1` custom
resources, so those fields can still change between releases. The dashboard routes under `/api` are not versioned, and
no schema describes them. [Stability](#stability) tells you which routes to depend on.
:::

## The two API surfaces

| | Versioned API | Dashboard routes |
| --- | --- | --- |
| Path | `/api/v1/...` | `/api/...` |
| Request body | JSON (a form body also works) | Form-encoded (`application/x-www-form-urlencoded`) |
| Verbs | `GET`, `POST`, `PATCH`/`PUT`, `DELETE` | `GET` and `POST` only |
| Errors | `{"error": "...", "code": "..."}` for every error | `{"error": "..."}` or a plain-text `404` |
| Described by | `GET /api/v1/openapi.json` | The tables further down this page |
| Used by | The CLI, the MCP server, and scripts | The dashboard, and `/api/v1` itself |

Each write in `/api/v1` is a dashboard route in disguise. Geass turns the JSON body into the form that the dashboard
handler reads, and sends the request to that handler. For this reason, these things are the same on both surfaces:

- The access rules.
- The validation messages.
- The shape of a success answer.

For the same reason, the tables for the dashboard routes below also tell you which fields a `/api/v1` write accepts.

## Conventions

**Base URL.** The address of the dashboard, for example `https://geass.example.com`. The operator itself speaks plain
HTTP on port 8085. Put TLS in front of it. See [Security](/docs/operations/security/).

**Authentication.** Send `Authorization: Bearer <token>` with a workspace token (`gsw_...`) or a project token
(`gsp_...`). A session cookie from `POST /api/login` also works. The cookie is `__Host-geass_session`, or
`geass_session` over plain http on this machine. If a request with a Bearer header fails, Geass never falls back to a
cookie. A program that cannot ask a person for a password signs in with a [device code](#sign-in-from-a-program).

**Cookie writes need an origin.** A write that uses a session cookie must have an `Origin` or `Referer` header. The
host in that header must be the host of the dashboard. Browsers add this header. With `curl`, add `-H "Origin: https://geass.example.com"`.
Requests with a Bearer token do not need the header.

**Names and IDs.** All resources are in the namespace `geass-system`, and the API identifies a resource by its
`metadata.name`. For a project, that name is an ID, for example `proj-1a2b3c4d`. The readable name of a project is
`spec.displayName`. For services, databases, and buckets, the dashboard generates a name like `morning-beach`. You can
keep a display label in the annotation `geass.dev/display-name`. See
[Resources overview](/docs/reference/resources/overview/).

**Draft and deploy.** When you create a service, database, or bucket, Geass saves a draft. When you edit a running
resource, Geass saves a pending change. Geass applies nothing until a deploy call, except the settings that Geass treats
as live:

- The replica count, autoscaling, and sleep-when-idle settings of a service.
- The host and TLS settings of a service.
- The metrics scraping and rollout flags of a service.
- The backup settings of a database.
- `source.git.watchPatterns`. A push reads it immediately.
- `logs.retentionHours`, but nothing reads it.

The schedules of a function are not live. They start with the next deploy. See
[Projects and environments](/docs/guides/projects-and-environments/).

**Who can call what.** There are four kinds of caller:

| Caller | Can |
| --- | --- |
| `admin` (a user, or a workspace token) | Everything, except that only a signed-in admin's session, not a token, can manage tokens and approve device codes |
| `observer` (a workspace token) | Read everything a viewer can, and also logs, pods, deploy logs, variable names, and the other reads marked **Observer** below. Cannot change anything |
| `viewer` (a user, or a workspace token) | Read the redacted overview. Cannot read logs, variables, or other sensitive reads, and cannot change anything |
| Project token | The endpoints in [Project token endpoints](#project-token-endpoints), for services in its own environment |

When [organizations](/docs/guides/organizations-and-workspaces/) are on, each caller also works in one place, and
everything above applies to what is in that place. A caller that belongs to an organization sees and changes only the
projects of that organization, and what belongs to them. Such a caller is a person who signed in to the organization,
or a token that the administrator of the organization made. For such a caller:

- A resource of another organization gets a `404` answer, as if it does not exist.
- A route of the platform (settings, nodes, shared servers, `/api/orgs`) gets `403`.

Platform administrators and install-wide tokens act for the platform and see everything. `GET /api/v1/me` says where
the caller works (`org`, `orgName`, `workspace`, `workspaceName`, or `platformAdmin`).

Some tables have an **Access** column. In that column:

- **Admin** means admins only, including admin workspace tokens.
- **Observer** means observers and admins.
- **Viewer** means any signed-in caller: viewers, observers, and admins.
- A trailing "project token" means that a project token can also call the route.

Tables without the column are admin writes. [Users and API tokens](/docs/guides/users-and-tokens/#roles) lists what
each role can see.

## Errors

On `/api/v1`, every error is a JSON object with a message and a code that a program can switch on. This shape applies
whichever layer produced the error:

```json title="Error response from /api/v1"
{ "error": "confirmation did not match the resource name", "code": "invalid_request" }
```

| Status | `code` | Meaning |
| --- | --- | --- |
| `400` | `invalid_request` | The request is invalid, a rule refused it, or the cluster lacks capacity. `error` says why. A body that is not a JSON object, or a verb with no mapping (`there is no PUT /api/v1/nodes/...`), also gets this code |
| `401` | `unauthenticated` | No valid session or token: `authentication required` or `invalid or expired API token` |
| `403` | `forbidden` | The role or token scope of the caller does not allow it: `viewer role cannot access this`, `this token cannot access that endpoint`, or a token limited to another project |
| `403` | `managed` | With [managed mode](/docs/operations/managed-mode/) on, the write is to something that the platform that runs this Geass keeps: sign-in, the domain and DNS of the dashboard, or the backup of the cluster state. `error` says who manages it. `area` and `managedBy` say which area and who. Geass writes nothing |
| `404` | `not_found` | The resource, or the route, does not exist. A project token gets `app not found` for services that it cannot see |
| `405` | `method_not_allowed` | The path does not take that method |
| `413` | `too_large` | The body is larger than 2 MiB |
| `429` | `rate_limited` | Too many sign-in requests. See [Sign in from a program](#sign-in-from-a-program) |
| `500`, `502` | `internal_error` | Geass could not read or write the cluster: `could not complete the request` or `could not load the resources` |

A rule that fails a write returns `400` with its plain-language message. For example:
`a project must retain at least one environment` or
`variable name must use uppercase letters, numbers, and underscores`. The device-code endpoints answer with their own
codes (`authorization_pending`, `slow_down`, `access_denied`, `expired_token`) in the same `code` field.

The dashboard routes under `/api` answer with `{"error": "..."}` and no `code`. The exception is the refusal of managed
mode, `403`, which has `code`, `area`, and `managedBy`. Most of their `404` answers are the plain text
`404 page not found`. These statuses apply:

| Status | Meaning |
| --- | --- |
| `400` | The request is invalid, a rule refused it, or the cluster lacks capacity. `error` says why |
| `401` | No valid session or token. `authentication required` or `invalid or expired API token` |
| `403` | `viewer role cannot access this` (for a viewer, and for an observer that tries a write), `this token cannot access that endpoint`, a token limited to another project, or, with [managed mode](/docs/operations/managed-mode/) on, a write to something the platform keeps, as `{"error": "...", "code": "managed", "area": "signIn"}` |
| `404` | The resource does not exist. Most handlers answer with the plain text `404 page not found`, not JSON. A project token gets `app not found` for services that it cannot see |
| `405` | `{"error": "method not allowed"}`: an `/api/` path called with a method other than `GET` or `POST`. A `GET` on a `POST`-only path is a plain-text `404`, except `POST /api/databases/{db}/query`, whose `GET` is a `405` |
| `415` | `send form-encoded data`: a project token sent another content type to a dashboard route. `/api/v1` converts JSON for you, so a `/api/v1` call never gets this status |
| `500`, `502` | Geass could not read or write the cluster. `could not complete the request` or `could not load dashboard` |

## Stability

| Tier | Endpoints | What to expect |
| --- | --- | --- |
| Versioned API | Every operation in `GET /api/v1/openapi.json` | Built for scripts, CI, the CLI, and the MCP server. One error format, JSON bodies, and an OpenAPI description |
| Token endpoints and read model | `GET /api/token`, `GET /api/token/services`, `POST /api/token/deploy`, `GET /api/bootstrap`, `GET /api/usage`, metrics and logs reads | The dashboard routes that tokens were designed around. `/api/v1` offers `GET /me`, per-resource reads, `GET /token/services`, and `POST /token/deploy` for the same jobs. The shape of `/api/bootstrap` follows the `v1alpha1` resources and the needs of the dashboard |
| Dashboard internals | Sign-in and sign-out, the settings endpoints (domain, GitHub, Cloudflare), webhooks, OAuth callbacks, the event stream, the terminal WebSocket, and form fields that exist for UI forms | Change with the UI, and are not in the OpenAPI document. Prefer `kubectl` for these |

`/api/v1` also forwards any other `POST` or `GET` path to the dashboard route of the same name. For example,
`POST /api/v1/projects/{name}/webhooks/save` reaches `POST /api/projects/{name}/webhooks/save`. Only the operations in
the OpenAPI document are part of the versioned API. Treat the rest as dashboard internals.

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

Send `Authorization: Bearer <token>`. For a write, also send a JSON object as the body, with
`Content-Type: application/json`. The verbs have their usual meanings:

| Verb | Use |
| --- | --- |
| `GET` | Read |
| `POST` on a collection (`/apps`, `/databases`, `/projects`, ...) | Create (saved as a draft where the resource has drafts) |
| `POST` on an action (`/apps/{name}/deploy`) | Do something |
| `PATCH` (or `PUT`) on `/apps/{name}`, `/databases/{name}`, `/object-stores/{name}`, `/projects/{name}` | Change settings |
| `DELETE` on `/apps/{name}` and the other collections | Delete. To confirm, add `?confirmName=<the name>` (or a body field) |

Geass turns the body into form fields before the handler sees it, so these rules apply:

- A string or a number is the value of the field. `true` is a checked box, so `"deploy": true` means deploy.
- `false` and `null` leave the field out, which keeps its value. To turn a switch off, send the string that the handler
  expects, for example `"scaleToZero": "off"`.
- A list repeats the field (`"environments": ["production", "staging"]`).
- An object nested in a field travels as its JSON text. Geass refuses a list of lists.
- Query-string parameters also become fields. A `DELETE` uses this rule to carry `confirmName`. A field in the body
  wins over the same field in the query string.
- The body must be a JSON object, at most 2 MiB. A form-encoded body is also accepted.

### The OpenAPI document

`GET /api/v1/openapi.json` returns the API as an OpenAPI 3.0.3 document. Geass generates the document from the list
`v1Operations` in `internal/dashboard/api_v1_spec.go`. So what the document describes and what the API serves stay
together. A test checks that the API answers every listed operation, and that no operation is listed twice.

```bash title="List the paths, and who may call one"
curl -fsS -H "Authorization: Bearer $GEASS_TOKEN" $GEASS/api/v1/openapi.json | jq -r '.paths | keys[]'
curl -fsS -H "Authorization: Bearer $GEASS_TOKEN" $GEASS/api/v1/openapi.json | jq '.paths["/apps/{name}/scale"].post["x-access"]'
```

- `servers` is `/api/v1`, so every path in the document is relative to it. The security scheme is a Bearer token. The
  two public operations (`/auth/device` and `/auth/token`) are marked as needing none.
- Each operation has a `summary`, a tag, an `operationId` built from the method and path (`getAppsNameStatus`), and its
  path parameters. A write also has a JSON request body that lists its fields, with the required fields marked.
- `x-access` says who can call the operation: `public`, `viewer` (anyone signed in), `observer`, `project` (a project
  token for its own environment can also call it), or `admin`. For a few rows, that label is not the whole story. The
  route table below is more exact for those rows.
- The document describes only the success, `400`, `401`, `403`, and `404` responses. The `4xx` entries say only that
  the body is `{"error": "...", "code": "..."}`. The **Returns** column below fills that gap.
- A project token cannot fetch the document.

### Route table

**Who** uses the terms from [Conventions](#conventions). In the **Who** column, "project token" after a comma means
that a project token can also call the row. "not a project token" means that a project token cannot call the row. A write answers `{"ok": true}` unless the **Returns** column says otherwise. Fields marked
`*` are required. If a write forwards to a dashboard route, the write also accepts the other fields of the table for
that route further down.

#### The service itself

| Method and path | Who | Body | Returns |
| --- | --- | --- | --- |
| `GET /version` | Any, project token | none | `{"api": "v1", "name": "geass", "release": "v0.3.0"}`. `release` is the Geass release that runs the API, which the command line compares itself with |
| `GET /me` | Any, project token | none | `username`, `role`, `canMutate`, `canReadSensitive`, `authentication` (`session`, `token`, or `oidc` for the identity provider's access token, see [Access tokens as a bearer credential](/docs/guides/external-sign-in/#access-tokens-as-a-bearer-credential)), and for a token `scope`, `expiresAt` and, for a project token, `project` and `environment`. `expiresAt` is also there for an access token |
| `GET /openapi.json` | Any | none | The OpenAPI document |
| `GET /events` | Any | none | A server-sent event stream that says when something changed. See [Events](#events) |
| `GET /usage` | Any | none | What each project reserves and uses. See [Usage](#usage) |
| `GET /usage/hours` | Admin acting for the platform (a platform administrator or an install-wide admin token, not an organization's caller, a viewer, an observer, or a project token) | `?from=`, `?to=` (RFC 3339), `?org=` | What each organization reserved, hour by hour, after `spec.usage.enabled` is on. See [Usage by organization](#usage-by-organization) |
| `GET /settings/addons` | Admin | none | The add-ons that you can turn on and off, in the order `serverless`, `longhorn`, `monitoring`, `logging`: `{"addons":[{"name","title","enabled","state","reason","message","installed","canRetry","canDisable","keptWhenOff", "profile","profiles","retentionDays"}],"retryPending"}`. `state` is `Off`, `Installing`, `Ready`, or `Failed`. See [Add-ons](#add-ons) |
| `POST /settings/addons/{name}/save` | Admin | `enabled` (`true` or `false`), `profile` (`monitoring`: `lite` or `full`), `retentionDays` (`logging`: 1 to 90) | Turns an add-on on or off, or changes its options. A field that you do not send stays as it is. `400` for `longhorn` with `enabled` `false`, because Geass never removes replicated storage |
| `POST /settings/addons/{name}/retry` | Admin | none | Asks the operator to start the install of an add-on over. `409` when the add-on is not `Failed` with a reason that a new job can put right, or when another retry waits |
| `GET /mail/usage` | Admin acting for the platform, as for `/usage/hours` | `?org=` | What each organization sent through the mail relay, and was refused, since the relay started. See [Mail relay usage](#mail-relay-usage) |

#### Sign-in and tokens

| Method and path | Who | Body | Returns |
| --- | --- | --- | --- |
| `POST /auth/device` | Public | `clientName` (up to 60 characters) | A device code and a user code. See [Sign in from a program](#sign-in-from-a-program) |
| `POST /auth/token` | Public | `deviceCode`* | The token after a person approves the code, or a `400`/`429` with a device-flow `code` |
| `GET /auth/device/{code}` | Admin session | none (`{code}` is the user code, and case and the dash do not matter) | `{"userCode", "clientName", "status", "expiresIn"}`, what the approval page shows |
| `POST /auth/device/approve` | Admin session | `userCode`* (case and the dash do not matter), `role` (`admin`, `observer`, or `viewer`, default `observer`), `expiresInDays` (0 to 3650, default 30), `deny` (`true` refuses) | `{"ok": true, "item": {...}}` with the new token's record but not the token, or `{"ok": true, "denied": true}` |
| `GET /tokens` | Admin session | `?project=` | `{"items": [token, ...]}`, newest first |
| `POST /tokens` | Admin session | `name`*, `scope`* (`workspace`, `project` or `service`, a deploy hook), `role` (`admin`, `observer`, or `viewer`, workspace only, default `viewer`), `project` and `environment` (project and service), `app` (service only: the service the hook deploys), `expiresInDays` (0 to 3650, and 0 or none means never) | `{"ok": true, "token": "gsw_...", "item": {...}}`. Geass shows the token one time only. A hook's token starts with `gsh_`. You put it in a [hook URL](/docs/guides/users-and-tokens/#deploy-hooks), and a service has one (`400` otherwise) |
| `DELETE /tokens/{id}` | Admin session | none | `{"ok": true}`. `404` `token was not found` |
| `GET /users` | Install-wide admin | none | `{"items": [{"username", "role", "current"}, ...], "identityProvider"}`. Never a password or a hash. `current` marks the caller |
| `POST /users` | Install-wide admin | `username`* (letters, digits, and `. _ @ + -`, up to 64 characters), `role` (`admin` or `viewer`, default `viewer`), `password` (12 to 72 characters) | `{"ok": true, "username", "role"}`. When you send no password, Geass makes one and adds `"password"` to the answer. Geass shows it one time. `409` for a name in use, in any letter case |
| `POST /users/{name}/role` | Install-wide admin | `role`* (`admin` or `viewer`) | `{"ok": true}`. `409` `this is the last admin` for the last admin. `404` `user was not found` |
| `POST /users/{name}/password` | Install-wide admin | `password` (12 to 72 characters) | `{"ok": true}`, with a made-up `"password"` when you send none. Geass signs the user out |
| `DELETE /users/{name}` | Install-wide admin | none | `{"ok": true}`. Geass signs the user out. `409` for the last admin |
| `POST /users/sign-out-all` | Install-wide admin | none | `{"ok": true}`. Geass ends every session, the session of the caller included. Tokens keep working. `400` when `GEASS_DASHBOARD_SESSION_KEY` sets the key |

An install-wide admin is a signed-in admin, or an admin workspace token that no organization owns. A token of an
organization or of a project cannot call the `/users` routes, and a viewer cannot read them. A token manages viewers
only. It cannot make an admin, make a viewer an admin, or set the password of an admin, and Geass answers `403`
`a token can manage viewers only`. Geass does not accept a password in the query string.

#### Projects

| Method and path | Who | Body | Returns |
| --- | --- | --- | --- |
| `GET /projects` | Any | none | `{"items": [summary, ...]}`. See [Resource summaries](#resource-summaries) |
| `POST /projects` | Admin | `region` | `201` `{"project": "proj-1a2b3c4d"}`. The project is isolated from other projects and kept away from the cluster's machines, as [Network isolation](/docs/guides/network-isolation/) describes |
| `GET /projects/{name}` | Any | none | `{"resource", "spec", "status"}` |
| `PATCH /projects/{name}` | Admin | `displayName`, `environments` (list), `region` | `{"ok": true}` |
| `DELETE /projects/{name}` | Admin | `confirmName`* (the display name) | `{"ok": true}` |
| `POST /projects/{name}/deploy` | Admin | `environment`* | `{"ok": true, "deployed": 2}` |
| `POST /projects/{name}/environments` | Admin | `environment`* | `{"ok": true}`. An environment that exists is a `400` |
| `POST /projects/{name}/export` | Admin, not a project token | `?summary=true` for the manifest alone | The project's bundle, `application/gzip` (`Content-Disposition: attachment`), or with `summary` the manifest as JSON: what is to be entered again and what a bundle does not carry. It only reads. See [Moving a project to another cluster](/docs/guides/moving-a-project/) |
| `POST /projects/import` | Admin, not a project token | The bundle as the raw body (`Content-Type: application/gzip`) with `?dryRun=`, `?name=`, `?region=`, `?keepProjectId=` (platform administrators). Or JSON `bundle` (base64), `dryRun`, `name`, `region`, `keepProjectId` | `200` `{"dryRun": true, "ok": ..., "plan": {...}}` for a dry run. `201` `{"project": {"id", "name"}, "created": [...], "warnings", "toReenter", "notMoved", "data", "next"}` for an import, which makes a new project and drafts and no Secret. `409` with the `plan` when a blocker stands in the way. `400` for a bundle that is not valid. `413` over 16 MiB. These two do not go through the JSON-to-form conversion, so they take a body of up to 16 MiB and not 2 MiB |
| `POST /projects/{name}/compliance` | Admin | `requiredTags` (list, up to 8, and an empty list clears them) | `{"ok": true, "requiredTags": ["hipaa"]}` |
| `GET /projects/{name}/compliance` | Any | none | `{"requiredTags", "region", "databases": [{"name", "compliant", "missing"}], "openNodes"}`: where the project falls short of its tags |
| `GET /projects/{name}/network` | Any workspace token | none | `{"isolation", "allowFromProjects", "blockNodeAddresses"}`: who can reach the project's services now |
| `POST /projects/{name}/network` | Admin | `isolation`* (`Project` or `None`), `allowFromProjects` (list, up to 20), `blockNodeAddresses` (`true` or `false`, as JSON booleans or the words, and left as it is when not sent) | `{"ok": true}` |
| `GET /projects/{name}/quota` | Any workspace token | none | `{"environment", "project", "used"}`, each an object of `cpu`, `memory`, `storage` and `pods`. A limit that is not set is left out |
| `POST /projects/{name}/quota` | Admin | Per environment: `cpu`, `memory`, `storage` (amounts above zero, such as `4`, `8Gi`, `50Gi`), `pods` (1 to 100000). For the whole project: `projectCpu`, `projectMemory`, `projectStorage`, `projectPods`. A limit that is not sent keeps its value. A limit sent empty is removed | `{"ok": true}` |
| `POST /projects/{name}/variables/save` | Admin | `name`*, `environment`*, `value`*, `secret` (`true` stores it as a secret) | `{"ok": true}` |
| `GET /projects/{name}/metrics` | Any | `?window=`, `?environment=` | Series. See [Metrics and logs](#metrics-and-logs) |
| `GET /projects/{name}/logs` | Observer | `?environment=`, `service`, `q`, `level`, `since`, `tail`, `previous` | Merged live logs |
| `GET /projects/{name}/logs/stream` | Observer | the same query | The same as server-sent events |
| `GET /projects/{name}/logs/history` | Observer | `?environment=`, `service`, `q`, `level`, `since`, `limit` | Stored history. A project that requires node tags gets `{"available": false, ...}`: its history is not kept |
| `POST /projects/{name}/github/install` | Admin | `environment`, and with [a provider's GitHub App](#github-an-app-a-provider-runs) `installation` | With the cluster's own App, `{"ok": true, "url": "https://github.com/apps/.../installations/new?state=..."}`. With a provider's App, the project is connected to an installation of it: the one sent, or the only one its organization has (`{"ok": true, "connected": true, "installation": 42, "account": "acme"}`). With none, `{"ok": true, "url": "<the provider's install address>?project=...&environment=..."}`. With several and none sent, a `400` that names them |
| `GET /projects/{name}/github/installations` | Admin | none | With a provider's App: `{"items": [{"id", "account", "accountType", "provider", "tokenState"}], "external": true, "installURL"}`, the installations that the project's organization can connect to. Never a token |

#### Services

A service is a `GeassApp`. `{name}` is its resource name.

| Method and path | Who | Body | Returns |
| --- | --- | --- | --- |
| `GET /apps` | Any, project token | `?project=`, `?environment=` | `{"items": [summary, ...]}`. A project token sees only its own environment |
| `POST /apps` | Admin | `project`*, `environment`*, `source` (`image`, `git`, or `function`, an image when left out), `name`, `image`, `repository`, `branch`, `runtime`, `code`, `mode` (`http` or `event`), `functionTriggers` (list), `port`, `cpu` and `memory` (what it reserves), `cpuMax` and `memoryMax` (the most it can use while the server has room, and the same as `cpu` and `memory` when left out), or `cpuRequest`, `cpuLimit`, `memoryRequest` and `memoryLimit` for exact amounts, `draft` (`true`: save a draft), `deploy` (`true`, `on`, `1` or `yes`: deploy it as it is saved, and `draft` is read the same way), and the other fields of [Create and change](#create-and-change) | `{"ok": true, "name": "storefront", "kind": "apps"}`. The service is a draft unless `deploy` is true, so call a deploy next. With `deploy` the answer also has `"deployed": true`. `draft` and `deploy` together are a `400` however they are spelled, and a cluster with no room refuses a `deploy` before anything is saved |
| `GET /apps/{name}` | Any, project token | none | `{"resource", "spec", "status"}`. Callers who cannot change things get the redacted spec. Secret values are blank for everyone |
| `PATCH /apps/{name}` | Admin, project token | `image`, `port`, `cpu`, `memory` (what it reserves: one of the dashboard's sizes), `cpuMax`, `memoryMax` (the most it can use, one of the same sizes and not below what is reserved. Sent empty, the same as what is reserved. Left out, a ceiling it already has is kept), `cpuRequest`, `cpuLimit`, `memoryRequest`, `memoryLimit` (exact amounts of any size instead of the four above), `replicas`, `deploy` (`true` deploys straight away), `functionCode`, `functionRuntime`, `functionMode`, `functionTriggers` (list, and an empty string removes them all), `scaleToZero` (`"on"` or `"off"`), `idleSeconds`, `scaleToZeroMax`, and the other fields of [Create and change](#create-and-change) | The pending state. See [Create and change](#create-and-change) |
| `DELETE /apps/{name}` | Admin | `confirmName`* | `{"ok": true}` |
| `GET /apps/{name}/status` | Any, project token | none | One read of everything a deploy leaves behind. See below |
| `GET /apps/{name}/deployments` | Any, project token | none | `{"items": [{"id", "phase", "image", "commit", "trigger", "actor", "changeTitle", "createdAt", "startedAt", "completedAt"}]}`, newest first. The `id` is what `rollback` takes |
| `GET /apps/{name}/deployments/{id}/logs` | Observer, project token | none | `{"id", "sections": [{"id", "title", "lines"}]}` |
| `POST /apps/{name}/deploy` | Admin, project token | none | `{"ok": true, "pending": [], "deployed": true, ...}` |
| `POST /apps/{name}/scale` | Admin, project token | `replicas`* (0 to 1000) | `{"ok": true}`. Takes effect immediately |
| `POST /apps/{name}/rollback` | Admin, project token | `revision`* (a deployment `id`) | `{"ok": true}`. Functions cannot be rolled back |
| `POST /apps/{name}/build`, `/build/cancel` | Admin, project token | none | `{"ok": true}` |
| `POST /apps/{name}/retry-provision` | Admin, project token | none | `{"ok": true}` |
| `POST /apps/{name}/config/set` | Admin, project token | `key`*, `value`* | `{"ok": true, "config": {...}, "pending": ["variables"]}` |
| `POST /apps/{name}/config/delete` | Admin, project token | `key`* | The same |
| `POST /apps/{name}/secrets/set` | Admin, project token | `key`*, `value`* | `{"ok": true, "secrets": ["STRIPE_API_KEY"], "pending": ["variables"]}`. Never returns values |
| `POST /apps/{name}/secrets/delete` | Admin, project token | `key`* | The same |
| `GET /apps/{name}/variables` | Observer, project token | none | `{"secrets": [names], "sharedVariableRefs", "env": [{"name", "kind"}], "config": {...}}`. Secrets and attached references are names only. The plain `config` comes with its values, because plain configuration is not secret |
| `GET /apps/{name}/logs` | Observer, project token | `?tail=` (1 to 1000, default 200: the newest lines across the service's pods, each of which is read for that many first), `since`, `q`, `level`, `previous` (`true` also reads each pod's run before its last restart) | `{"lines": [{"t", "service", "pod", "container", "line", "level", "previous"}], "services", "truncated", "note"}`: the service's recent log lines, oldest first, one entry each. It is the project's logs narrowed to the service, so the `environment` and `service` query fields are ignored, and `services` holds only this service's name. `level` and `q` look through the `tail` newest lines of each pod |
| `GET /apps/{name}/logs/stream` | Observer, project token | `?level=`, `since`, and the other log query fields | A server-sent event stream of the service's log lines, recent ones first and then new ones, across restarts |
| `GET /apps/{name}/runtime` | Observer, project token | none | `{"pods": [{"name", "phase", "container"}]}` |
| `GET /apps/{name}/metrics` | Any, project token | `?window=` | Series. See [Metrics and logs](#metrics-and-logs) |

`GET /apps/{name}/status` returns what a program needs to wait for a deploy without reading five resources. The answer
has these fields:

- `name`, `project`, and `environment`.
- `state`: `ready`, `pending`, `failed`, `draft`, or `deleting`.
- `message`, `url`, `pendingChanges` (a count), and `ready`.
- `replicas`: `desired`, `ready`, and `available`.
- `sleeping`: present, and true, for a service that sleeps when idle and has no copies running.
- `scaleToZero`: for a service that asked to sleep when idle. It has `idleSeconds` and `maxReplicas`. It also has
  `active` and `message` after the operator has said whether the service does sleep when idle.
- `build`: `name`, `phase`, `failureReason`, `sourceRevision`, `ciState`, `startedAt`, and `completedAt`.
- `lastAttempt`: `id`, `phase`, `startedAt`, `completedAt`, `summary`, and `errors`.

`build` and `lastAttempt` are left out when there is none. Any caller can read the status, including a viewer. So the
failure reason and the source revision of the build are visible here, although the redacted overview keeps them from
viewers.

#### Databases, logical databases and buckets

| Method and path | Who | Body | Returns |
| --- | --- | --- | --- |
| `GET /databases`, `/logical-databases`, `/object-stores` | Any | `?project=`, `?environment=` | `{"items": [summary, ...]}` |
| `GET /databases/{name}`, `/logical-databases/{name}`, `/object-stores/{name}` | Any | none | `{"resource", "spec", "status"}` |
| `POST /databases` | Admin | `project`*, `environment`*, `engine` (`postgres`, `mysql`, `redis`, or `sqlite`, Postgres when left out), `name`, `highAvailability`, and the other fields of [Databases](#databases) | `{"ok": true, "name": "orders-db", "kind": "databases"}` (a draft) |
| `PATCH /databases/{name}` | Admin | `version`, `storageSize`, `cpu`, `memory`, `backups` (`"on"` or `"off"`), `backupRetentionDays`, `clusterSize`, `runOnTags` | `{"ok": true, "applied": "live", "liveChanged": true}` |
| `DELETE /databases/{name}`, `/logical-databases/{name}`, `/object-stores/{name}` | Admin | `confirmName`* | `{"ok": true}` |
| `POST /databases/{name}/deploy` | Admin | none | `{"ok": true}` |
| `GET /databases/{name}/connection` | Admin (a workspace admin, not a project token or observer) | `?reveal=true` | `{"keys": [...], "host", "port", "database", "username"}`. With `reveal=true`, also `"password"`, `"uri"` and `"revealed": true`. The reveal is written to the operator log |
| `GET /databases/{name}/metrics` | Any | `?window=` | Series |
| `POST /databases/restore` | Admin | `archive`*, `restorePoint` | A new database restored from backups. See [Databases](#databases) |
| `POST /logical-databases` | Admin | `name`, `project`, `environment`, `server`, `database`, `connectionLimit` | `{"ok": true}`. Needs no deploy |
| `GET /logical-databases/{name}/connection` | Admin (workspace admin) | `?reveal=true` | As for a database |
| `POST /object-stores` | Admin | `project`, `environment`, `name`, `placement`, `engine`, `region`, `createBucket`, `connectionRef`, `bucket`, `draft` | A draft bucket. See [Buckets](#buckets) |

#### Nodes and metrics

| Method and path | Who | Body | Returns |
| --- | --- | --- | --- |
| `GET /nodes` | Any (not a project token) | none | `{"items": [{"name", "ready", "schedulable", "controlPlane", "region", "zone", "tags": [{"name", "reserved", "compliance"}], "cpu", "memory", "pool", "ephemeral", "cordoned", "cordonedBy"}]}`. `pool` is the pool the node belongs to, and `ephemeral` is `true` for a node that keeps data off itself. Both are left out when they do not apply |
| `POST /nodes/{name}/placement` | Admin | `region`, `zone`, `tags` (list), `reserved` (list), `compliance` (list) | `{"ok": true, "warnings": [...]}`. See [Cluster and platform](#cluster-and-platform) |
| `POST /nodes/{name}/cordon` | Admin | none | `{"ok": true, "name": "worker-2", "cordoned": true}`. Pods that run on the node stay. `400` for the last node that can start new pods |
| `POST /nodes/{name}/uncordon` | Admin | none | `{"ok": true, "name": "worker-2", "cordoned": false}`. `400` for a node that Geass cordoned itself, to upgrade it (`cordonedBy` is `upgrade`) or to empty it (`pool`) |
| `GET /cluster/metrics` | Any (not a project token) | `?window=` | Series for the whole cluster |
| `GET /operator/metrics` | Any (not a project token) | `?window=` | Series for the operator: `reconcileErrors`, `reconcileTime`, `apiLatency`, `apiRequests`, `queueDepth`, `statusWrites`, `statusSkipped` |

#### Machines: clouds, pools and servers

Geass creates machines at clouds for [node pools](/docs/guides/node-pools/) and single servers. A connection that
creates machines belongs to the cluster, never to a project. None of these routes is open to a project token. These
routes are experimental, like the clouds behind them. A write answers `{"ok": true}`, and a create also has the `name`.
An error is a `400` with the reason.

| Method and path | Who | Body | Returns |
| --- | --- | --- | --- |
| `GET /compute/providers` | Any (not a project token) | none | `{"items": [{"id", "label", "summary", "fields": [{"key", "label", "help", "secret", "optional", "multiline"}], "settings": [{"key", "label", "help", "required"}], "freeSettings", "regionHelp"}]}`: the clouds, the credentials each takes and the settings a pool can give it. Never a credential value |
| `GET /compute/sizes` | Admin (not a project token) | `?connection=`* , `?region=` | `{"items": [{"id", "description", "vcpus", "memoryMiB", "diskGB", "hourlyPrice", "currency", "arch"}]}`, cheapest first. It calls the cloud with the connection's credentials, so `403` for anyone but an admin, `502` when the cloud cannot list its sizes, and `503` when the server cannot reach clouds |
| `GET /connections` | Any (not a project token) | none | `{"items": [connection, ...]}` of every cloud connection, sorted by name. A connection has `name`, `provider`, `purposes`, `project`, `org` (the organization it belongs to, when it has no project), `region`, `available`, `account`, `message`, `warnings`, `verifiedAt`, `expiresAt`, `usedBy` (pools), `machines` and `createdAt`, and never a credential |
| `GET /connections/{name}` | Any (not a project token) | none | One connection. `404` `no such connection` |
| `POST /connections` | Admin | `name`* (lowercase letters, numbers and hyphens, at most 50 characters), `provider`* (an ID from `/compute/providers`), `purposes` (list: `Compute`, and for AWS `Storage` and `Database`, and `Compute` when left out), `region`, and each credential the cloud names, as a field of its own key (`token`, `accessKeyId`, ...) | `{"ok": true, "name": "hetzner"}`. The credentials go into the Secret `<name>-credentials` in `geass-system` and are never shown again. `400` when a required credential is missing or `project` is sent |
| `PATCH /connections/{name}` | Admin | `purposes`, `region`, and the credentials to replace (send only the ones that change. For temporary AWS credentials, send `accessKeyId`, `secretAccessKey` and `sessionToken` together, and `expiresAt`). A credential that is left out or empty keeps its value | `{"ok": true}`. The credentials are used by the next machine made or deleted. `POST /connections/{name}/verify` refreshes the connection's status immediately |
| `DELETE /connections/{name}` | Admin | `confirmName`* | `{"ok": true}`. Removes the connection and its Secret. Refused while a pool or a machine uses it |
| `POST /connections/{name}/verify` | Admin | none | `{"ok": true}`. Asks the cloud again whether it accepts the credentials. Read `GET /connections/{name}` for the answer |
| `GET /pools` | Any (not a project token) | none | `{"items": [pool, ...]}` without nodes |
| `GET /pools/{name}` | Any (not a project token) | none | One pool, with its nodes under `members` |
| `POST /pools` | Admin | `name`*, `maxNodes`* (the cost cap), `connection` or `inventory` (a list of `{name, address, sshUser, sshPort?, sshKeySecret?}`) with `inventoryKey`, `region`, `size`, `fallbackSizes`, `fallbackRegions`, `image`, `spot`, `addressType` (`Public` or `Private`), `settings` (a JSON object), `placementRegion`, `placementZone`, `tags`, `reservedTags`, `storage` (`None` or `Allow`), `minNodes`, `autoscaling`, `scaleUpDelaySeconds`, `maxScaleUp`, `scaleDownUtilizationPercent`, `scaleDownAfterMinutes`, `scaleDownDelayAfterAddMinutes`, `minNodeLifetimeMinutes`, `spareNodes`, `orphanPolicy` (`Report` or `Delete`) | `{"ok": true, "name": "burst"}`. `400` without `maxNodes`, for a connection that is not for Compute or belongs to a project, and for a setting the cloud does not read |
| `PATCH /pools/{name}` | Admin | Any of the fields of `POST /pools`. A change to the placement replaces the region, site and tags together | `{"ok": true}`. `400` when it would change a pool from a cloud to servers or the other way |
| `DELETE /pools/{name}` | Admin | `confirmName`* | `{"ok": true}`. Deletes the pool, its nodes and their machines |
| `POST /pools/{name}/resume` | Admin | none | `{"ok": true}`. Ends the pause of a pool that kept failing, and forgets the failures |
| `POST /servers` | Admin | `name` (made up for a machine at a cloud), `source` (`cloud` or `existing`, and `existing` when `address` is sent). For a cloud: `connection`*, `region`, `size`*, `image`, `settings`. For a server that you have: `address`*, `sshUser`*, `sshPort`, and `sshPrivateKey` or `sshKeySecret`*, `knownHosts`. Also `placementRegion`, `placementZone`, `tags`, `storage` (`None` or `Allow`, `Allow` by default) | `{"ok": true, "name": "server-x7k2m"}`. A machine at a cloud costs money until the server is removed |
| `GET /servers` | Any (not a project token) | none | `{"items": [{"name", "role", "phase", "message", "address", "source", "pool", "node", "joined", "cordoned", "cordonedBy", "canRetry", "canUpdateHostKeys", "canRemove"}]}`. `phase` is `Pending`, `Creating`, `Joining`, `Ready`, `Failed`, or `Deleting`. `message` is the error of a server that failed, and only an admin or an observer gets it |
| `POST /servers/{name}/retry` | Admin | none | `{"ok": true}`. `400` for a server whose `phase` is not `Failed` |
| `POST /servers/{name}/host-keys` | Admin | `knownHosts`* | `{"ok": true, "failed": false}`. `failed` says that the server is still `Failed`, so a retry is next. `400` for a machine that Geass created, and for lines that list no key for the address of the server |
| `DELETE /servers/{name}` | Admin | `confirmName`* | `{"ok": true}`. Refused for the control plane, and for a node that belongs to a pool |

A pool in a response has these fields:

- The settings of the pool: `name`, `cluster`, `connection`, `provider`, `inventory`, `region`, `size`,
  `fallbackSizes`, `fallbackRegions`, `image`, and `spot`. Also `addressType`, `settings`, `placement`, `storage`,
  `minNodes`, `maxNodes`, `autoscaling` (with every setting, defaults filled in), `orphanPolicy`, and `suspended`.
- The state of the pool: `phase`, `message`, `desiredNodes`, `nodes`, `readyNodes`, `pendingPods`, `activeSize`,
  `estimatedHourlyCost`, `consecutiveFailures`, `lastFailure`, `lastFailureAt`, `pausedUntil`, `lastScaleUp`,
  `lastScaleDown`, `orphans`, `fallbackStep`, and `createdAt`.

A node under `members` has `name`, `phase`, `message`, `ready`, `deleting`, `instanceId`, `address`, `size`, `region`,
`createdAt`, and `readyAt`.

#### GitHub: an App a provider runs

A cluster normally has a GitHub App of its own. See [Connect GitHub](/docs/guides/connectors/github/). A provider that
runs Geass for many clusters, for example rungeass, can run one App for all of them instead. These routes tell a
cluster about that App. Only the provider holds the private key of the App. The provider does these things:

- It records which installations of the App belong to the cluster, and to which
  [organization](/docs/guides/organizations-and-workspaces/).
- It gives Geass an installation access token for each installation, and renews the token.
- It forwards the events of GitHub. See [Forwarded GitHub events](#forwarded-github-events).

Every route here belongs to the platform:

- A caller of an organization gets `403`.
- A viewer can read and cannot write.
- No project token reaches these routes.

A token or a webhook secret is never in an answer, a log line, or an event. If you send a token or a secret in the
query string instead of the body, Geass refuses it (`400`). See
[GitHub through a provider](/docs/guides/connectors/github/#github-through-a-provider).

| Method and path | Who | Body | Returns |
| --- | --- | --- | --- |
| `GET /github/connections` | Any platform caller | none | `{"items": [connection, ...]}` |
| `PUT /github/connections/{name}` | Admin | `provider`* (on creation), `appSlug`, `installURL` (https), `webhookSecret`* (on creation, 32 to 512 characters. To change it, send it again). A field that is not sent stays. `appSlug` or `installURL` sent empty is cleared | `201` on creation, `200` after, with the connection. `{name}` is lowercase letters, digits, and hyphens, at most 40, and does not end in `-github`. The secret goes into the Secret `github-app-<name>-webhook` and is never shown again. `409` for a name that is a project's connection |
| `GET /github/connections/{name}` | Any platform caller | none | One connection. `404` for a name that is not one |
| `DELETE /github/connections/{name}` | Admin | `confirmName`* | `{"deleted": "<name>"}`, with its token Secrets and its webhook Secret. `409` with `projects` while a project is connected through it |
| `GET /github/connections/{name}/installations` | Any platform caller | none | `{"items": [installation, ...]}` |
| `PUT /github/connections/{name}/installations/{id}` | Admin | `account`* (the GitHub login the App is installed on), `accountType` (`User`, `Organization` or `Enterprise`), `org` (the [organization](/docs/guides/organizations-and-workspaces/) whose projects can connect to it. Empty is the platform's: projects of no organization) | The installation. `{id}` is GitHub's installation id. `400` for an `org` that does not exist. `409` for an installation another connection lists, and for moving it to another organization while a project of the one it is in is connected to it |
| `DELETE /github/connections/{name}/installations/{id}` | Admin | none | `{"removed": 42, "projects": ["proj-1a2b3c4d"]}`: the projects that were connected through it, which stop building from GitHub (a build says to connect the project again) until they are connected again |
| `PUT /github/connections/{name}/installations/{id}/token` | Admin | `token`*, `expiresAt`* (RFC 3339, as GitHub returns with the token) | `{"connection", "installation", "tokenState", "tokenExpiresAt"}`, never the token. Stored in the Secret `github-app-<name>-installation-<id>`. `400` for a token with characters a token does not have, and for an `expiresAt` that has passed or is less than a minute away. `404` for an installation the connection does not list (record it first) |

A connection in an answer is `{"name", "provider", "appSlug", "installURL", "webhookPath", "webhookURL", "ready",
"tokensReady", "tokensReason", "tokensMessage", "installations"}`. In a connection:

- `webhookPath` is `/webhooks/github/forwarded/<name>`.
- `webhookURL` is the same path on the address of the dashboard, when the cluster has one.
- `ready` is whether the webhook secret is stored.
- `tokensReady` (`True` or `False`), `tokensReason`, and `tokensMessage` are the `TokensReady` condition of the
  connection, worked out at the moment of the request.
- The reason is `TokensValid`, `NoInstallations`, `TokenExpiringSoon`, `TokenMissing`, or `TokenExpired`.
  `TokenExpiringSoon` means that every token still works, and one token has less than ten minutes left.

An installation is `{"id", "account", "accountType", "org", "tokenState", "tokenExpiresAt", "projects"}`. `tokenState`
is `Valid`, `ExpiringSoon`, `Expired`, or `Missing`. `Expired` also applies when less than a minute is left.

```bash title="Give a cluster the provider's App, an installation and its token"
curl -X PUT https://geass.example.com/api/v1/github/connections/rungeass \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Content-Type: application/json" \
  -d '{"provider":"rungeass","appSlug":"rungeass","installURL":"https://app.rungeass.app/github/install","webhookSecret":"'"$FORWARDING_SECRET"'"}'
curl -X PUT https://geass.example.com/api/v1/github/connections/rungeass/installations/42 \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Content-Type: application/json" \
  -d '{"account":"acme","accountType":"Organization","org":"org-acme"}'
curl -X PUT https://geass.example.com/api/v1/github/connections/rungeass/installations/42/token \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Content-Type: application/json" \
  -d '{"token":"'"$INSTALLATION_TOKEN"'","expiresAt":"2026-10-23T13:00:00Z"}'
# {"connection":"rungeass","installation":42,"tokenExpiresAt":"2026-10-23T13:00:00Z","tokenState":"Valid"}
```

#### Forwarded GitHub events

The provider forwards each GitHub event for an installation of the cluster to
`POST /webhooks/github/forwarded/<name>`. This path is not under `/api`. A caller reaches it without signing in,
because the signature is the credential. The provider sends GitHub's body as it came, byte for byte, with these
headers:

| Header | Value |
| --- | --- |
| `X-GitHub-Event` | GitHub's event name, for example `push`. Required |
| `X-GitHub-Delivery` | GitHub's delivery id. Optional: Geass logs it and echoes it as `delivery` |
| `X-Geass-Signature` | `t=<unix seconds>,v1=<hex>`, where `<hex>` is HMAC-SHA256, keyed with the webhook secret of the connection, of `<t>.<X-GitHub-Event>.<body>`. Geass accepts several `v1=` values (up to four), so a provider that changes the secret can sign with both |

Geass does these checks:

- It compares in constant time.
- After the signature matches, it refuses a `t` more than five minutes from its own clock.
- It remembers the signatures of the events that it handled for ten minutes, and answers `409` to the same event
  again. Geass does not remember an event that it answered with `5xx`, so the provider can send that event again as it
  was. Each dashboard process remembers its own signatures.
- It reads at most 2 MiB. Above that size, it answers `413`.

Geass answers `401` in the same way for a connection that does not exist, a connection without a webhook secret, and a
signature that does not match. After Geass verifies the event, it handles the event exactly as one from GitHub's own
webhook: `ping`, `push`, and `pull_request`, and it ignores others. There is one difference. The event must name an
installation that the connection lists:

- If the event names another installation, Geass answers `422`.
- If the event names no installation, Geass answers `{"accepted": true, "ignored": true}`.

The event counts only for the projects connected to that installation.

```bash title="Sign and forward an event"
t=$(date +%s)
# The body is signed exactly as it is sent: the bytes of the file, a final newline included.
sig=$({ printf '%s.push.' "$t"; cat body.json; } | openssl dgst -sha256 -hmac "$FORWARDING_SECRET" | awk '{print $2}')
curl -X POST https://geass.example.com/webhooks/github/forwarded/rungeass \
  -H "X-GitHub-Event: push" -H "X-GitHub-Delivery: $DELIVERY" -H "X-Geass-Signature: t=$t,v1=$sig" \
  --data-binary @body.json
# {"accepted":true,"delivery":"...","triggered":1}
```

#### Abuse detection

This section covers the suspects of [abuse detection](/docs/guides/abuse-detection/) (`spec.tenancy.abuse`), and what
the platform does about them. Every route is for an admin who acts for the platform: a platform administrator or an
install-wide admin token. Geass refuses an organization's caller, a viewer, an observer, and a project token with
`403`, and tells them nothing. While abuse detection is off, the writes answer `409`.

| Method and path | Who | Body | Returns |
| --- | --- | --- | --- |
| `GET /abuse/suspects` | Admin acting for the platform | `?org=` | `{"enabled", "note", "action", "cpu", "suspects": [...], "counting": [...], "reviewed": [...]}`: each suspect with where it is (`service`, `project`, `environment`, `namespace`, `org`), its evidence (for the CPU signal `averagePercent`, `minPercent`, `thresholdPercent`, `windowMinutes`, `samples`, `traffic`, and for a report `reporter`, `detail`, `pod`, `priority`), `since`, `detectedAt`, `suspended`, `releasedAt` and the condition's `message`. It also has the services that the CPU signal counts now, and the services that are cleared, with the time until when. `cpu` gives the settings and whether the metrics server (and Prometheus, for `quietTraffic`) answered at the last sample. `note` says, every time, that a suspect is a signal and not proof |
| `POST /abuse/suspects/{name}/clear` | Admin acting for the platform | `days` (1 to 365, default `reviewExemptionDays`, or 30), `note` | `{"service", "wasSuspected", "reviewedUntil", "message"}`. The suspicion goes (a suspended service runs again) and the CPU signal leaves the service alone until then. A service that is not suspected can be cleared ahead of a heavy job. `404` for no such service |
| `POST /abuse/suspects/{name}/release` | Admin acting for the platform | none | `{"service", "released", "message"}`. A suspended service runs again and stays suspected. `409` for one that is not suspended |
| `POST /abuse/reports` | Admin acting for the platform (a token, from a detector) | `service`, or `namespace` and `pod`. Also `reporter`, `reason`, `priority`, `time` (RFC 3339). Or falcosidekick's webhook body as it is: `rule`, `priority`, `time` and `output_fields` (`k8s.ns.name`, `k8s.pod.name`) are read, and Falco's `output` and command line are not kept | `{"service", "project", "org", "alreadySuspected", "suspended"}`. `400` for no service or pod to go by, `404` for a service that does not exist or does not run in the namespace, `422` for a pod that is not a copy of a service, and for a service of a project of no organization |

#### Templates and project tokens

| Method and path | Who | Body | Returns |
| --- | --- | --- | --- |
| `GET /templates` | Observer | none | `{"items": [template, ...]}` |
| `POST /templates/{id}/deploy` | Admin | `project`*, `environment`* | `{"ok": true, "created": [...]}`. The template's resources as drafts |
| `GET /token/services` | Any token, project token | `?project=` and `?environment=` (workspace tokens only) | `{"project", "environment", "services": [{"id", "name", "kind", "source", "ready", "pendingChanges"}]}` |
| `POST /token/deploy` | Project token only | none | `{"ok": true, "deployed": 2}`: deploys every pending change in the token's environment |

#### Resource summaries

A list returns one summary for each resource, in the same shape for every kind, so a program can treat all summaries
the same way:

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

`state` is `ready`, `pending` (still being set up or rolled out), `failed`, `draft` (saved and not deployed), or
`deleting`. `message` explains a `pending` or `failed` state. `details` depends on the kind:

- A function has `runtime`, `mode`, and `triggers`.
- A service that sleeps when idle has `scaleToZero` (`idleSeconds`, `maxReplicas`, and whether it is `active`).
- A project has `environments`, `region`, and `requiredTags`.
- A database has `engine`, `highAvailability`, `placement`, and `version`.
- A logical database has `server` and `database`.

A caller who cannot change things gets the same redactions as in the dashboard, so a viewer sees an empty `engine`.

### Usage by organization

`GET /api/v1/usage/hours` returns what the usage meter kept. For each hour (UTC), it gives what the projects of each
organization reserved, by workspace. The route is for billing the organizations of a shared cluster, so it belongs to
the platform alone. These callers get `403`:

- The people and tokens of an organization, also for the figures of their own organization.
- Viewers, observers, and project tokens.

The meter runs only while `spec.usage.enabled` is on in the
[platform configuration](/docs/reference/resources/geass-platform-config/). For what the meter measures, and what it
does not measure, see
[Usage by organization, for billing](/docs/guides/usage-and-capacity/#usage-by-organization-for-billing).

| Query | Default | Meaning |
| --- | --- | --- |
| `from` | 24 hours before `to` | The first hour, as an RFC 3339 time. Geass moves it back to the start of its hour. If it is older than the oldest hour that is kept (`retainedFrom`, 62 days back), Geass moves it to that hour |
| `to` | the end of the hour going on | The answer holds the hours that start before it. A later time becomes the end of the hour going on: Geass answers no hour that has not started |
| `org` | every organization | The name of one organization. The answer then has only its entries, and no `unassigned` or `platform` |

`from` must be before `to`, and the range must be at most 744 hours (31 days). If not, the answer is `400`. Geass
answers every hour of the range. An hour that the meter never wrote comes back as empty, so an hour is never missing.

```json title="GET /api/v1/usage/hours?from=2026-10-23T09:00:00Z (one hour shown)"
{
  "enabled": true,
  "source": "requests",
  "from": "2026-10-23T09:00:00Z",
  "to": "2026-10-23T11:00:00Z",
  "next": "2026-10-23T10:00:00Z",
  "retainedFrom": "2026-08-22T10:00:00Z",
  "egress": { "available": false, "reason": "Geass does not meter traffic that leaves the cluster. ..." },
  "hours": [
    {
      "start": "2026-10-23T09:00:00Z",
      "end": "2026-10-23T10:00:00Z",
      "closed": true,
      "meteredSeconds": 3600,
      "orgs": [
        { "org": "org-a", "workspace": "", "cpuCoreHours": 0.25, "memoryGiBHours": 0.5, "storageGiBHours": 0, "egressBytes": null,
          "cpuMilliSeconds": 900000, "memoryByteSeconds": 1932735283200, "storageByteSeconds": 0 },
        { "org": "org-a", "workspace": "ws-aaaaaa", "cpuCoreHours": 0.5, "memoryGiBHours": 1, "storageGiBHours": 10, "egressBytes": null,
          "cpuMilliSeconds": 1800000, "memoryByteSeconds": 3865470566400, "storageByteSeconds": 38654705664000 }
      ],
      "unassigned": { "cpuCoreHours": 0.1, "memoryGiBHours": 0.125, "storageGiBHours": 0, "egressBytes": null,
        "cpuMilliSeconds": 360000, "memoryByteSeconds": 483183820800, "storageByteSeconds": 0 },
      "platform": { "cpuCoreHours": 0.2, "memoryGiBHours": 0.25, "storageGiBHours": 0, "egressBytes": null,
        "cpuMilliSeconds": 720000, "memoryByteSeconds": 966367641600, "storageByteSeconds": 0 }
    }
  ]
}
```

| Field | Meaning |
| --- | --- |
| `enabled` | Whether the meter is on now. Hours that it stored before it was turned off are still answered |
| `source` | `requests`: the figures are what pods asked for (their CPU and memory requests) and the size of bound volumes, not what was used |
| `from`, `to` | The hours the answer holds, from the start of the first to the end of the last |
| `next` | Where the next read must start: the first hour of the answer that is still open, or `to` when all are closed |
| `retainedFrom` | The oldest hour that is kept |
| `egress` | Always `{"available": false, "reason": ...}`: Geass does not meter traffic, and every `egressBytes` is `null` |
| `hours[].closed` | Whether the hour can still change. The hour closes when it is metered to its end, or five minutes after it ends, and it never changes after that. An open hour comes again in the next answer |
| `hours[].meteredSeconds` | How much of the hour the meter saw, 0 to 3600. In a closed hour, less than 3600 is time that the meter did not see, which is not counted. `0` is an hour that the meter did not run in, which is not the same as an hour in which nothing was reserved |
| `hours[].orgs[]` | One entry for each organization and workspace that had a pod on a node or a bound volume in the hour, sorted by organization and workspace. `workspace` is `""` for the organization as a whole: its projects in no workspace, and its builds |
| `hours[].unassigned` | The projects that belong to no organization. Never added to an organization |
| `hours[].platform` | What belongs to no project: Geass, K3s, monitoring, storage, and the shared database and bucket servers |
| `cpuCoreHours`, `memoryGiBHours`, `storageGiBHours` | The amounts in the units they are billed in, to six decimals |
| `cpuMilliSeconds`, `memoryByteSeconds`, `storageByteSeconds` | The same amounts exactly, in the units they were added up in (millicores, or bytes, times seconds) |

A program that bills as it goes asks approximately every hour, with `from` set to the `next` of its last answer. The
program stores each hour by its `start`, `org`, and `workspace`, and replaces what it stored for the same hour before.
If you ask again for an hour that is closed, you get the same numbers. This is true until the hour is older than 62
days and Geass no longer keeps it.

### Add-ons

`GET /api/v1/settings/addons` says which add-ons of the cluster are on, and how each is doing. It reads the state that the
operator writes to `status.addons` of the cluster. An add-on that is on and that the operator has not reported yet is
`Installing` with the message "Waiting for the operator to report." Only an admin can call it, because the add-ons name what
the cluster runs.

```json title="GET /api/v1/settings/addons"
{
  "addons": [
    {"name": "serverless", "title": "Sleep when idle", "enabled": true, "state": "Installing",
     "message": "KEDA HTTP add-on: Waiting to start the install.", "installed": true, "canRetry": false,
     "canDisable": true, "keptWhenOff": true},
    {"name": "monitoring", "title": "Metrics", "enabled": true, "state": "Failed", "reason": "InstallSuspended",
     "message": "The install job is suspended.",
     "installed": true, "canRetry": true, "canDisable": true, "keptWhenOff": false, "profile": "lite", "profiles": ["lite", "full"]}
  ],
  "retryPending": ""
}
```

`reason` is `InstallFailed`, `InstallSuspended`, or `NoHelmController`. Only the first two set `canRetry`. `canDisable` is
`false` for replicated storage once it is on. `retryPending` names the add-on whose retry the operator has not carried out yet.

To turn on sleep when idle and wait for it, send `POST /api/v1/settings/addons/serverless/save` with `{"enabled": true}`.
Then read `GET /api/v1/settings/addons` until its `state` is `Ready` or `Failed`.

### Mail relay usage

`GET /api/v1/mail/usage` returns what the [mail relay](/docs/guides/mail-relay/) counted for each organization. Like
the usage hours, this route belongs to the platform alone. The people and tokens of an organization, viewers,
observers, and project tokens get `403`. The dashboard asks the relay on its admin port, with the token from the Secret
of the relay. So the answer is as current as the relay. The relay keeps the counts only in its memory. `countedSince`
is when the relay started, and a restart starts the counts again from zero.

```json title="GET /api/v1/mail/usage?org=org-a"
{
  "enabled": true,
  "counters": "memory",
  "countedSince": "2026-10-27T09:00:00Z",
  "now": "2026-10-27T10:00:00Z",
  "org": "org-a",
  "organizations": [
    {
      "org": "org-a",
      "limits": { "messagesPerMinute": 60, "messagesPerHour": 600, "messagesPerDay": 2000, "recipientsPerMessage": 50,
        "maxMessageBytes": 10485760, "connections": 5 },
      "lastMinute": 2, "lastHour": 40, "lastDay": 40, "connections": 1,
      "accepted": 40, "recipients": 52, "bytes": 812345,
      "rejected": { "rate_minute": 3, "sender_domain": 1 },
      "upstreamFailures": { "unreachable": 1 },
      "authFailures": 0,
      "lastAcceptedAt": "2026-10-27T09:59:12Z"
    }
  ]
}
```

| Field | Meaning |
| --- | --- |
| `enabled` | Whether the relay runs. When it does not, `organizations` is empty and there is no `countedSince` |
| `counters` | Always `memory`: the counts are in the memory of the relay's one replica |
| `countedSince`, `now` | When the relay started, and when it answered |
| `organizations[].limits` | The limits the organization is held to, with every default worked out |
| `lastMinute`, `lastHour`, `lastDay` | The messages its windows hold now, which its rates are checked against: those sent, and those being sent |
| `connections` | Its connections signed in now |
| `accepted`, `recipients`, `bytes` | What the upstream took, since `countedSince` |
| `rejected` | What the relay refused, by reason: `rate_minute`, `rate_hour`, `rate_day`, `recipients`, `size`, `sender_domain`, `recipient_domain`, `connections`, `organization_removed` |
| `upstreamFailures` | What the upstream did not take, by kind: `unreachable`, `deferred`, `refused`, `auth` |
| `authFailures` | Failed sign-ins with the organization's username |

If `?org=` is not a name, the answer is `400`. If a relay is set but does not answer, or has no admin token yet, the
answer is `503`, with the reason.

### Sign in from a program

A program that has no browser, for example the command line, signs in with a device code. The program asks Geass for a
code and shows the code to a person. Then the program polls until an admin approves the code in the dashboard. The
password never reaches the program, and neither endpoint that the program calls needs credentials.

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

   `deviceCode` is 64 hexadecimal characters that only your program holds. `userCode` is eight letters from an
   alphabet with no vowels, shown as two groups of four. The approver sees `clientName`, and it becomes the name of
   the token (`CLI: deploy script on build-1`). The program can send any `clientName`, so the approver must check the
   code, not the name.

2. Show the person `verificationUri` and `userCode`. The person does these steps:

   1. Open the page. The page is **Sign in the command line**. See
      [Users and API tokens](/docs/guides/users-and-tokens/#sign-in-the-command-line).
   2. If not signed in as an admin, sign in as an admin.
   3. Check that the code on the page is the one that your program shows.
   4. Select the access and the lifetime.
   5. Approve the code.

3. Poll for the answer. Wait at least `interval` seconds between polls:

   ```bash title="Poll for the token"
   curl -sS -X POST $GEASS/api/v1/auth/token \
     -H "Content-Type: application/json" \
     -d "{\"deviceCode\": \"$DEVICE_CODE\"}"
   ```

   | Answer | Status | Body | Do |
   | --- | --- | --- | --- |
   | Waiting | `400` | `"code": "authorization_pending"` | Poll again after `interval` seconds |
   | Too fast | `429` | `"code": "slow_down"`, and the message says how long to wait | Wait longer. Each `slow_down` adds 5 seconds, up to 30 |
   | Approved | `200` | `{"token": "gsw_...", "tokenType": "Bearer", "role": "observer", "approvedBy": "admin", "expiresAt": "..."}` | Store the token. Geass gives it one time only, with `Cache-Control: no-store` |
   | Refused | `400` | `"code": "access_denied"` | Stop |
   | Expired or unknown | `400` | `"code": "expired_token"` | Start again |

These limits apply:

- A request lives 10 minutes, and Geass keeps it in memory. So an operator restart forgets pending sign-ins.
- At most 100 requests are pending at the same time.
- One address can start 10 requests a minute.

Above those limits, `POST /auth/device` answers `429` `too many sign-in requests, try again in a minute`. The limit
counts the remote address of the connection and does not read `X-Forwarded-For`. So behind an ingress, all callers
share one count.

The approval itself is `POST /api/v1/auth/device/approve`. It needs the cookie of a signed-in admin and an `Origin`
header. The dashboard page does the approval for you:

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

To deploy when the service is saved, add `"deploy": true` to the body of the first call, and leave out the second
call. The answer is then `{ "ok": true, "name": "storefront", "kind": "apps", "deployed": true }`.

```bash title="Wait for a deploy to finish"
while true; do
  status=$(curl -fsS -H "Authorization: Bearer $GEASS_TOKEN" $GEASS/api/v1/apps/storefront/status) || break
  echo "$status" | jq -e '.state == "failed" or .build.phase == "Failed" or (.ready and .pendingChanges == 0 and ((.build.phase // "Succeeded") == "Succeeded"))' >/dev/null && break
  sleep 3
done
echo "$status" | jq '{state, message, build}'
```

A deploy is done when the status says `ready` with no pending changes and a successful build, or no build. A deploy
failed when `state` is `failed` or the `phase` of the build is `Failed`. `message` and `build.failureReason` give the
cause. The `--wait` flag of the command line uses the same rule.

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

Geass keeps the last 12 deploy records for each service. A project token can list them on `/api/v1`, so a project
token can also roll back.

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

A project token keeps its limits on `/api/v1`. It can call these routes:

- `GET /me` and `GET /version`.
- `GET /apps`, only for its own environment.
- `GET /apps/{name}`, and the app routes marked **project token** in the table above.
- `GET /token/services` and `POST /token/deploy`.

Everything else answers `403` `this token cannot access that endpoint`, including `GET /openapi.json`, `/projects`,
`/databases`, `/nodes`, and `/usage`. A service in another project or environment answers `404` `app not found`, like
a service that does not exist. Geass refuses a body field `project` or `environment` that names another environment.

## Dashboard routes

The rest of this page documents the dashboard's own routes. They take form-encoded bodies, and a `/api/v1` write
forwards to one of them. Use them when you need something that `/api/v1` does not list, or to understand what a
`/api/v1` write does.

**Reads and writes.** Reads are `GET` and return JSON. Two endpoints stream, as described below. Every change is a
`POST` with a form-encoded body, which `curl --data-urlencode` sends. These routes read form fields, not JSON bodies.
Under `/api/`, every `POST` gets a JSON answer, whatever `Accept` header you send. The exception is a missing resource,
which gets a plain-text `404`. Also:

- A method other than `GET` or `POST` (`PUT`, `DELETE`, `HEAD`) on an `/api/` path gets `405`.
- A `GET` on a path that exists only for `POST` gets a plain-text `404`.
- A `POST` to an unknown `/api/...` path returns JSON `{"error": "not found"}`.

### Sign in and sessions

| Method and path | Access | Fields | Response |
| --- | --- | --- | --- |
| `POST /api/login` | Public | `username`, `password` | `{"ok":true,"authenticated":true,"username":"admin","role":"admin","canMutate":true}` and the `__Host-geass_session` cookie (`geass_session` over plain http on this machine). `400` `invalid username or password` on failure or lockout |
| `POST /api/setup` | Public. The token is the credential | `token`, `username`, `password` (12 characters to 72 bytes) | Uses a [setup link](/docs/operations/installer/#the-setup-link). Creates the admin `username`, or gives an existing user that password and the admin role, and ends their other sessions. Answers as `POST /api/login`, with the session cookie. `403` `this setup link was used or has expired`. `429` `too many attempts: wait one minute`. A wrong token counts as a failed sign-in for the address. With [managed mode](/docs/operations/managed-mode/) on, `403` with `code` `managed` |
| `POST /api/logout` | Public | none | `{"ok":true,"authenticated":false}`. Ends every session of that user |
| `GET /api/session` | Public | none | `{"authenticated":true,"username":"admin","role":"admin","canMutate":true}`, or `{"authenticated":false}`. While people sign in through an identity provider, the second form also has `"sso":{"name":"Acme"}`, which is what the sign-in page offers a button for. With [organizations](/docs/guides/organizations-and-workspaces/) on, the first form also has `org`, `workspace`, `platformAdmin` and `scopes`: the places the person can work in, each with `org`, `orgName`, `workspace`, `workspaceName` and `role` (the platform's own place has none of them) |
| `POST /api/session/scope` | Signed-in person from the identity provider | `org`, `workspace` | Says where the person works now, when organizations are on. `{"ok":true,"org","workspace","role"}` and a new session cookie with the same end and start. `org` and `workspace` empty is the platform, for a platform administrator. `404` for a place they were not given, `400` for a token or a local sign-in or when organizations are off |
| `POST /api/auth/device`, `POST /api/auth/token` | Public | As in [Sign in from a program](#sign-in-from-a-program) | The same answers as `/api/v1/auth/...` |
| `POST /api/auth/device/approve` | Admin session | `userCode`, `role`, `expiresInDays`, `deny` | The same as `/api/v1/auth/device/approve` |

```bash title="Sign in with a cookie jar"
curl -fsS -c cookies.txt -X POST $GEASS/api/login \
  -H "Origin: $GEASS" \
  --data-urlencode "username=admin" \
  --data-urlencode "password=replace-with-your-passphrase"
curl -fsS -b cookies.txt $GEASS/api/session
```

Password sign-in exists only here. There is no `/api/v1/login`. Dashboard users are `admin` or `viewer`. `observer` is
a token role. Sign-in through an [identity provider](/docs/guides/external-sign-in/) is a browser flow (below), and it
ends with the same session cookie.

### API tokens

Only a signed-in admin session can manage tokens. Not even an admin token can.

| Method and path | Access | Fields | Response |
| --- | --- | --- | --- |
| `GET /api/tokens` | Admin session | optional `project` | `{"items":[ token, ... ]}`, newest first |
| `POST /api/tokens/create` | Admin session | `name`, `scope` (`workspace`, `project` or `service`, a deploy hook), `role` (`admin`, `observer` or `viewer`, workspace only, default `viewer`), `org` and `workspace` (workspace tokens, [when organizations are on](/docs/guides/organizations-and-workspaces/#tokens): where the token acts), `project` and `environment` (project and service), `app` (service only), `expiresInDays` (0 to 3650, default never) | `{"ok":true,"token":"gsw_...","item":{...}}`. Geass shows the token one time only |
| `POST /api/tokens/revoke` | Admin session | `id` | `{"ok":true}`. `404` `token was not found` |
| `GET /api/token` | Any token | none | The calling token: `id`, `name`, `scope`, `project`, `environment`, `role`, `createdBy`, `createdAt`, `expiresAt`, `lastUsedAt`, `expired`. A session cookie gets `400` `this endpoint needs an API token` |
| `GET /api/token/services` | Any token | `project` and `environment` (workspace tokens only) | `{"project","environment","services":[{"id","name","kind","source","ready","pendingChanges"}]}` |
| `POST /api/token/deploy` | Project token | none | `{"ok":true,"deployed":2}`: deploys every pending change in the token's environment. Anyone else gets `403` |

For details and CI examples, see [Users and API tokens](/docs/guides/users-and-tokens/).

### Deploy hooks

| Method and path | Access | Response |
| --- | --- | --- |
| `POST /hooks/deploy/{token}` | The `gsh_` token in the path | `{"ok":true,"action":"build","service":"web"}` for a service built from a repository (its build starts over), or `"action":"deploy"` for any other (its pending changes are deployed). `404` `hook not found` for a token that is wrong, revoked, expired, or of another kind, or whose service is gone. `405` for any method but `POST`. `409` for a repository service with no build yet, never deployed, or pinned to a commit. `400` when the cluster has no room. `429` (with `Retry-After`) when the hook ran less than 30 seconds ago, or after too many wrong URLs from one client |

A hook needs no cookie, no `Origin`, and no `Authorization` header. The secret in the path is the credential, and it is
good for that service only. Geass refuses a hook token as a Bearer token. See
[Deploy hooks](/docs/guides/users-and-tokens/#deploy-hooks).

### Read model

| Method and path | Access | Meaning |
| --- | --- | --- |
| `GET /api/bootstrap` | Viewer | Everything the dashboard shows, in one response |
| `GET /api/usage` | Viewer | What each project reserves and uses |
| `GET /api/events` | Viewer | A server-sent event stream of changes |
| `GET /api/templates` | Observer | Built-in and saved templates |

#### Bootstrap

`GET /api/bootstrap` returns one object. Most keys are Kubernetes lists of the Geass resources, so each key has `items`
with the `metadata`, `spec`, and `status` of the resource. `items` is `null` when empty, so use `// []` in `jq`.

| Key | Contents |
| --- | --- |
| `projects`, `apps`, `databases`, `logicalDatabases`, `objectStores` | The [GeassProject](/docs/reference/resources/geass-project/), [GeassApp](/docs/reference/resources/geass-app/), [GeassDatabase](/docs/reference/resources/geass-database/), [GeassLogicalDatabase](/docs/reference/resources/geass-logical-database/) and [GeassObjectStore](/docs/reference/resources/geass-object-store/) resources |
| `deployments`, `builds` | Deploy history and builds (`builds` is empty for viewers and observers) |
| `clusters`, `haReadiness`, `backupArchives` | The cluster (its `status.conditions` include `ClusterIssuerReady`, which says whether HTTPS certificates can be issued), its HA check, and database backups |
| `cloudConnections`, `platformConfig` | Cloud connections and the platform configuration (empty for viewers and observers) |
| `platform` | A summary: `hasDashboardURL`, `hasGitHubApp`, `hasCloudflare`, `cloudflareReady`, `dashboardURL`, `haReady`, `healthyNodes`, `awsAvailable`, `planetScaleAvailable`, `objectStorageAvailable`, `objectStorageEngine`, `objectStorageServers`, `objectStorageResizable` (whether the object storage server's volume can be made bigger), `capacity`, `defaultRegion`, `storageExpandable`, `fixedSizeDatabases`, `defaultStorage` (the default volume size per kind of resource: `objectstore` is `10Gi`), `session`, and `cloudflareZoneId` and `cloudflareZoneName` for admins |
| `metrics` | Three headline numbers: running pods, nodes, and CPU cores used |

Items are sorted oldest first. Secrets are never included. For a viewer or an observer, Geass removes the fields
described in [Users and API tokens](/docs/guides/users-and-tokens/#roles).

**See pending changes.** A service that is a draft or has undeployed changes has the annotation
`geass.dev/pending-changes`. Its value is a comma-separated mix of `created`, `settings`, and `variables`. A service
that was never deployed has `spec.deploy.enabled` false. Databases and buckets use the same annotation.

```bash title="Pending changes in every environment"
curl -fsS -H "Authorization: Bearer $GEASS_TOKEN" $GEASS/api/bootstrap \
  | jq -r '(.apps.items // [])[]
      | select(((.metadata.annotations["geass.dev/pending-changes"] // "") != "") or ((.spec.deploy.enabled // false) == false))
      | [.metadata.name, .spec.project, .spec.environment, (.metadata.annotations["geass.dev/pending-changes"] // "draft")]
      | @tsv'
```

With a project token, `GET /api/token/services` reports `pendingChanges` (a count) for each service. `GET /api/v1/apps`
reports it too.

#### Usage

`GET /api/usage` returns these fields:

- `cpuAllocatableMillis`, `memoryAllocatableBytes`, `nodes`, and `actualAvailable`.
- A `projects` list and a `platform` total.
- `regions`, when nodes have regions.

Each project has `pods`, `cpuReservedMillis`, `memoryReservedBytes`, and `storageReservedBytes`. When Prometheus
answers, each project also has `cpuUsingMillis`, `memoryUsingBytes`, and `storageUsingBytes`, plus `shareOfCpu` and
`shareOfMemory` from 0 to 1. See [Usage and capacity](/docs/guides/usage-and-capacity/).

#### Events

`GET /api/events` (or `GET /api/v1/events`) is a server-sent event stream. The stream sends `event: connected` first.
Then it sends `event: change` with `{"type":"change","resource":"apps","name":"storefront","op":"update"}`, and a
`ping` every 25 seconds.

- `resource` is one of `projects`, `apps`, `databases`, `logical-databases`, `object-stores`, `deployments`,
  `builds`, `cloud-connections`, `clusters`, `platform-config`, `ha-readiness`, or `backup-archives`. It is
  `resources` for an object that the stream does not name.
- `op` is `add`, `update`, `delete`, `deploy`, `create`, or `retry-provision`.

Geass coalesces bursts for 250 milliseconds, so treat an event as "something changed, reload".

### Projects

All of these are admin writes.

| Method and path | Fields | Meaning |
| --- | --- | --- |
| `POST /api/projects/create` | `region` (optional) | Creates a project with a generated ID and name and one environment, `production`. It starts isolated from other projects and kept away from the cluster's machines. `201` `{"project":"proj-1a2b3c4d"}` |
| `POST /api/projects/{project}/settings/save` | `displayName` (required), `environments` (repeat, at least one), `region` | Renames the project, sets its environments, and optionally its region. The region cannot change while the project has in-cluster databases |
| `POST /api/projects/{project}/settings/delete` | `confirmName` (the display name) | Deletes the project, its services, databases, logical databases, buckets, cloud connections and GitHub connection, and revokes its tokens. Delete AWS buckets first and wait for them to go. See [Connect AWS](/docs/guides/connectors/aws/#remove-an-aws-connection) |
| `POST /api/projects/{project}/export` | none, or `?summary=true` | The project's bundle, `application/gzip`, or its manifest as JSON. Admin. The same as `POST /api/v1/projects/{name}/export` |
| `POST /api/projects/import` | the bundle as the body, and `dryRun`, `name`, `region`, `keepProjectId` in the address | Makes a new project from a bundle, or says what it would make. Admin. The same as `POST /api/v1/projects/import` |
| `POST /api/projects/{project}/environments/create` | `environment` | Adds an environment (a lowercase DNS label. The project ID plus the name must fit in 63 characters) |
| `POST /api/projects/{project}/environments/archive` | `environment`, `confirmName` (the environment name) | Deletes an environment and everything in it. `{"ok":true,"deleted":3}`. The last environment cannot be removed |
| `POST /api/projects/{project}/variables/save` | `name` (`^[A-Z_][A-Z0-9_]*$`), `environment`, `value`, `secret` (`on` to store it as a secret) | Creates or updates a shared variable |
| `POST /api/projects/{project}/variables/delete` | `name`, `environment` | Deletes a shared variable |
| `POST /api/projects/{project}/deploy` | `environment` (default: the first) | Deploys every pending service, database and bucket in the environment, in that order. `{"ok":true,"deployed":2}`. If the cluster lacks room for a service or database, the deploy stops there with `400` and keeps what it already deployed |
| `POST /api/projects/{project}/pr-environments/save` | `enabled` (`on`), `baseEnvironment`, `maxOpen` (1 to 25, default 5) | Configures [pull request environments](/docs/guides/pull-request-environments/) |
| `POST /api/projects/{project}/pr-environments/close` | `number`, `repository` | Removes one pull request environment. `{"ok":true,"deleted":...}` |
| `POST /api/projects/{project}/webhooks/save` | `url`, `event` (repeat: `deployment.started`, `deployment.succeeded`, `deployment.failed`), `disabled` (`on`), `id` (to update) | Creates (`{"ok":true,"id","secret":"whsec_..."}`, the secret shown once) or updates (`{"ok":true,"id"}`) a webhook. Up to 20. The `url` must be `http://` or `https://` and must not point at localhost or, by default, a private address. Sending no `event`, or all three, means every event |
| `POST /api/projects/{project}/webhooks/delete` | `id` | Deletes a webhook and its secret |
| `POST /api/projects/{project}/webhooks/rotate` | `id` | New signing secret, `{"ok":true,"id","secret"}` |
| `POST /api/projects/{project}/webhooks/test` | `id` | Sends a test event, `{"ok":true,"delivery":{...}}`. `ok` is `false` when the delivery failed |
| `POST /api/projects/{project}/backup-storage` | `connection` (an AWS connection), `bucket`, `region`, or `remove=on` | Chooses or clears where the project's database backups go |
| `POST /api/projects/{project}/network` | `isolation` (`Project` or `None`), `allowFromProjects` (repeat, up to 20), `blockNodeAddresses` (`true` or `false`) | Sets [network isolation](/docs/guides/network-isolation/) and whether services are kept away from the cluster's machines. A request that leaves `blockNodeAddresses` out keeps it as it was |
| `POST /api/projects/{project}/quota` | `cpu`, `memory`, `storage`, `pods`, `projectCpu`, `projectMemory`, `projectStorage`, `projectPods` | Sets the [project limits](/docs/guides/project-limits/): what each environment, and the whole project, can ask for. A limit that is not sent keeps its value. A limit sent empty is removed |
| `POST /api/projects/{project}/compliance` | `requiredTags` (repeat, or comma-separated, at most 8) | Sets the node tags every workload of the project must run on. Each tag must exist on a node in the project's region and be reserved on every node that carries it, and a project that already has databases cannot take a tag its databases are not on. `{"ok":true,"requiredTags":[...]}`. See [Compliance tags](/docs/guides/compliance-tags/) |
| `GET /api/projects/{project}/compliance` | none | Viewer. `{"requiredTags","region","databases":[{"name","compliant","missing"}],"openNodes"}`: what the project requires and which databases fall short |
| `POST /api/projects/{project}/github/install` | `environment` | `{"ok":true,"url":"https://github.com/apps/..."}`: open it to install the GitHub App |
| `POST /api/projects/{project}/github/disconnect` | `environment` | Unlinks the project from GitHub |
| `POST /api/projects/{project}/github-reporting/save` | `enabled` (`on`, or `true`) | Turns [reporting back to GitHub](/docs/guides/github-and-builds/#reporting-back-to-github) on, or off when `enabled` is left out. `{"ok":true,"enabled":true}`. Workspace admin only |
| `GET /api/projects/{project}/github/repos` | none | `{"items":[{"FullName","DefaultBranch","Private","HTMLURL"}]}` (observer) |

### Services

A service is a `GeassApp`. `{app}` is its resource name.

#### Create and change

| Method and path | Fields | Meaning |
| --- | --- | --- |
| `POST /api/apps/create` | See below | Creates a service. `{"ok":true,"name":"storefront","kind":"apps"}`, with `"deployed":true` after a deploy |
| `POST /api/apps/{app}/update` | Any subset of the fields below, plus `deploy=on` | Saves settings, and deploys them when `deploy=on` |
| `POST /api/apps/{app}/deploy` | none | Deploys the service |
| `POST /api/apps/{app}/scale` | `replicas` (0 to 1000) | Changes the replica count immediately, without a deploy |
| `POST /api/apps/{app}/rollback` | `revision` (a `GeassDeployment` name) | Redeploys an earlier revision: an image service gets the earlier image, a Git service only gets the earlier replica count, and a function cannot be rolled back |
| `POST /api/apps/{app}/build` | none | Builds the service's branch again: puts its current build back to the start, and the operator builds the head of the branch as it is now. It makes no build of its own, and answers an error for a service that has none yet. See [GeassBuild](/docs/reference/resources/geass-build/#how-the-dashboard-uses-builds). The command line calls it as `geass build` and the MCP server as `rebuild_app` |
| `POST /api/apps/{app}/build/cancel` | none | Cancels the current build while it is waiting or running. A build that has finished answers an error |
| `POST /api/apps/{app}/retry-provision` | none | Retries a failed deploy |
| `POST /api/apps/{app}/delete` | `confirmName` (the app name) | Deletes the service |
| `POST /api/apps/{app}/display-name` | `displayName` | Sets the label shown in the dashboard |
| `POST /api/apps/{app}/attach` | `name` (a database or bucket), `kind` (`database` or `object-store`) | Adds `DATABASE_URL` (`REDIS_URL` for Redis), or for a bucket `S3_ENDPOINT`, `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY` and `S3_BUCKET`, as references to the resource's connection Secret. Answers `400` when the service already has a plain or configured variable of that name |
| `POST /api/apps/{app}/networking/delete` | `confirmName` | Clears the host and TLS |
| `POST /api/apps/{app}/shared-variables/save` | `sharedVariable` (repeat) | Selects which shared variables the service receives |

`create` takes these fields:

- `name`, `project`, `environment`, and `source` (`git`, `function`, or omit for an image).
- `image`, `port` (default 8080), `host`, `metrics` (`on`), `replicas`, `cpu`, and `memory`.
- `repository`, `branch`, `connectionRef`, `dockerfile`, `context`, and `waitForCI`.
- For functions: `runtime` and `code`, `mode` (`http` or `event`), and `functionTriggers` (a schedule as a JSON object
  or a JSON list of them, see below).
- `draft`, `deploy`, `autoscaling`, `minReplicas`, `maxReplicas`, `targetCPU`, `scaleToZero`, `idleSeconds`, and
  `scaleToZeroMax`.

`POST /api/apps/create` and `POST /api/v1/apps` save the service as a draft, unless you send `deploy`. To deploy a
draft, call a deploy next. `deploy` takes `on`, `true`, `1`, or `yes`, and it deploys the service as it is saved. If
you send `draft` and `deploy` together, the answer is `400`.

`update` takes these fields:

- `image`, `repository`, `branch`, `dockerfile`, `context`, `waitForCI`, and `watchPatterns`.
- `functionRuntime`, `functionCode`, `functionMode`, and `functionTriggers`.
- `port`, `replicas`, `cpu`, `memory`, `cpuMax`, and `memoryMax`. If you send no `cpuLimit` and no `memoryLimit`,
  Geass reads `cpuRequest` and `memoryRequest` as other names for `cpu` and `memory`.
- `autoscaling`, `minReplicas`, `maxReplicas`, `targetCPU`, `scaleToZero`, `idleSeconds`, and `scaleToZeroMax`.
- `host`, `tls`, `dnsVerification`, `metrics`, `command`, `args`, `workingDir`, and `buildCache`.
- `readinessPath`, `readinessPort`, `readinessPeriod`, `readinessTimeout`, `readinessFailureThreshold`, and the same
  fields with a `liveness` prefix.
- `runOnTags`. Only an admin session can send this field. Geass refuses it from tokens.
- `project` and `environment`, which move the service.

Switches take `on`. `cpu` must be one of `100m`, `250m`, `500m`, `1`, or `2`. `memory` must be one of `128Mi`,
`256Mi`, `512Mi`, `1Gi`, `2Gi`, or `4Gi`. A field that you leave out keeps its value.

To set requests and limits that are not on those lists, send `cpuLimit` or `memoryLimit`, with `cpuRequest` and
`memoryRequest` as you need them. For example, send `150m` and `900Mi`. When the body has `cpuLimit` or
`memoryLimit`, Geass reads the amounts exactly. Do not send `cpu`, `memory`, `cpuMax`, or `memoryMax` with them. If
you mix them, the answer is `400`. These rules apply:

- If you send neither of the two fields of a resource, the resource keeps what it has.
- A limit sent without a request is also the request.
- A request sent without a limit is also the limit. So a resource never has only one of the two.
- A limit below its request is a `400`.
- Amounts must be more than zero.

Databases and object storage do not read these fields. They always use what they reserve.

Sleep when idle (`scaleToZero`) takes `on` or `off`. `idleSeconds` is 30 to 86400 (default 300). `scaleToZeroMax`, the
most copies that run when the service is awake, is 1 to 100 (default 3). A field that you leave out keeps its value. So
if you send only `idleSeconds`, the setting does not turn on or off. You cannot combine sleep when idle with CPU
autoscaling: `Scale to zero decides how many copies run, so turn CPU autoscaling off to use it`. Sleep when idle needs
the serverless add-on of the cluster and a public host. See [Scale to zero](/docs/guides/scale-to-zero/). It is a live
setting, so it takes effect without a deploy.

Send the schedules of a function as `functionTriggers`. Each value is one JSON object
`{"name", "schedule", "path", "payload"}` or a JSON list of them. An empty value removes all of the schedules. These
rules apply:

- A function has at most 8 schedules.
- `name` is a lowercase DNS-style label of up to 32 characters.
- `schedule` is a cron expression in UTC.
- `path` starts with `/`.
- `payload` is the JSON body of the call.

The schedules start at the next deploy. See [Functions](/docs/guides/functions/).

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

`applied` is one of these values:

- `none`: nothing changed.
- `live`: everything took effect already.
- `deploy`: a change waits for a deploy.
- `deployed`: the save was a deploy, with `deploy=on`.

`title`, `detail`, and `button` are the dashboard's own wording and might change. `deploy` answers with the same fields
except `applied` and `liveChanged`. For a repository service, `update` accepts `context` (the root directory of the
build) and `watchPatterns` (which pushes build, one pattern per line). Geass refuses a `context` or `dockerfile` that
leaves the repository.

#### Variables and secrets

| Method and path | Fields | Response |
| --- | --- | --- |
| `POST /api/apps/{app}/config/set` | `key`, `value` | `{"ok":true,"config":{...},"pending":["variables"]}` |
| `POST /api/apps/{app}/config/delete` | `key` | Same |
| `POST /api/apps/{app}/config/raw` | `configJSON` (a JSON object of strings that replaces all config) | Same |
| `POST /api/apps/{app}/secrets/set` | `key`, `value` | `{"ok":true,"secrets":["STRIPE_API_KEY"],"pending":["variables"]}`. Never returns values |
| `POST /api/apps/{app}/secrets/delete` | `key` | Same |
| `POST /api/apps/{app}/env/delete` | `key` | Removes an attached reference such as `DATABASE_URL`. Same response as `config/set` |
| `GET /api/apps/{app}/variables` | none | `{"secrets":[names],"sharedVariableRefs":[...],"env":[{"name","kind"}],"config":{...}}`: secrets and attached references as names only, plain `config` with its values (observer, project token) |

Changes to variables need a deploy before the pods see them. See [Environment variables](/docs/guides/environment-variables/).

#### Terminal

`POST /api/apps/{app}/console/create` records an audited console session and returns
`{"ok":true,"session":"..."}`. Its fields are `target`, as `pod|container`, and `command`, up to eight words. Then open
`GET /api/apps/{app}/console/ws?session=<name>&cols=100&rows=30` as a WebSocket:

- Binary frames carry keystrokes in and output out.
- A text frame `{"type":"resize","cols":120,"rows":32}` resizes the terminal.
- At the end, the server sends `{"type":"exit"}`.

The session ends after five minutes. Only an admin can create a session.

### Databases and buckets

#### Databases

| Method and path | Fields | Meaning |
| --- | --- | --- |
| `POST /api/databases/create` | `name`, `project`, `environment`, `engine` (`postgres`, `mysql`, `sqlite`, or `redis`. Case does not matter. Anything else becomes Postgres), `placement` (`InCluster` or `External`), `provider` (`PlanetScale` or `AWS`), `mode` (`Create` or `Connect`), `highAvailability`, `databaseName`, `clusterSize` (PlanetScale only), `host`, `port`, `username`, `password`, `version`, `storageSize`, `connectionRef`, `cpu`, `memory`, `runOnTags` | Creates a draft. `{"ok":true,"name":"orders-db","kind":"databases"}`. `400` for a PostgreSQL or MariaDB database of an organization's project while the platform runs organizations' code in a [sandboxed runtime](/docs/operations/sandboxed-runtime/#engines-that-cannot-run-in-it), whose message starts `This database cannot run in the sandbox` |
| `POST /api/databases/{db}/update` | `version`, `storageSize`, `cpu`, `memory`, `backups` (`on` or `off`), `backupRetentionDays` (1 to 365), `clusterSize` (PlanetScale only, and only until the database has been created there), `runOnTags`, `environment` | `{"ok":true,"applied":"live","liveChanged":true}` |
| `GET /api/planetscale/cluster-sizes` | `project`, `connection`, `engine` (`mysql` or `postgres`) as query fields | Lists the cluster sizes a PlanetScale connection can create: `{"items":[{"name","displayName","cpu","ramBytes","storageBytes","development","production","metal","provider","architecture","monthlyUsd","replicaMonthlyUsd"}]}`. Observer. `400` for a bad connection or engine, `502` `could not load PlanetScale cluster sizes`. See [Connect PlanetScale](/docs/guides/connectors/planetscale/#choosing-a-size) |
| `POST /api/databases/{db}/deploy` | none | Deploys the draft or the pending change |
| `POST /api/databases/{db}/retry-provision` | none | Retries a failed deploy |
| `POST /api/databases/{db}/delete` | `confirmName` | Deletes it (see [Backups and restore](/docs/guides/backups-and-restore/) for the final backup) |
| `POST /api/databases/{db}/display-name` | `displayName` | Sets the label |
| `POST /api/databases/{db}/query` | `query` | Runs a statement with `psql -c`, `mysql -e` or `redis-cli` inside the database pod, or for SQLite sends it to rqlite's HTTP API from inside the pod: `{"output":"..."}`. Admin. Problems such as a database that is not running come back as `output` text with status `200`. `GET` on this path returns `405`. Nothing records the statements. See [Security](/docs/operations/security/#auditing) |
| `POST /api/databases/restore` | `archive`, `restorePoint` or `time` (RFC 3339), `environment` | Creates and deploys a new database from backups. [Backups and restore](/docs/guides/backups-and-restore/). `400` as for `create` for an engine that cannot run in the sandbox |
| `POST /api/shared-databases/create` | `engine` (`Postgres` or `MySQL`), `name`, `region`, `runOnTags`, `version`, `storageSize`, `highAvailability`, `cpu`, `memory`, `org` | Starts a cluster-wide server immediately, for an organization with `org`. `201` `{"database":"cluster-postgres"}`. `400` for PostgreSQL with `org` while the platform runs organizations' code in a sandboxed runtime. [Shared database servers](/docs/guides/shared-database-servers/#the-sandboxed-runtime) |
| `POST /api/shared-databases/{db}/org` | `org` (empty for the platform) | Gives a shared server to an organization, or to the platform: `{"ok":true,"database","org"}`. `409` while the server holds a database whose project is not that organization's (for the platform while organizations are on, any organization's), naming up to five. `409` for a PostgreSQL server given to an organization while the platform runs organizations' code in a sandboxed runtime. `404` for an organization or server that does not exist. `400` for a project's own server. Platform administrators only. [Organizations](/docs/guides/shared-database-servers/#organizations) |
| `GET /api/shared-databases/mixed` | none | The logical databases whose shared server is not their project's organization's, as it is while organizations are on: `{"enforced","items":[{"name","project","environment","projectOrg","server","serverOrg","made","message"}],"servers":[{"name","org","owners":[{"org","databases"}]}]}`. `enforced` says that organizations are on. `made` says that the database is on its server already, and so kept and served. Also `GET /api/v1/shared-databases/mixed`. Platform administrators only |
| `POST /api/logical-databases/create` | `name`, `project`, `environment`, `server`, `database`, `connectionLimit` (1 to 1000) | Creates a database on a server. It needs no deploy call. `400` for a server of another organization than the project's, or, while organizations are on, the platform's server for a project of an organization |
| `POST /api/logical-databases/{db}/retry-provision` | none | Retries |
| `POST /api/logical-databases/{db}/delete`, `.../display-name` | `confirmName`, `displayName` | Deletes, relabels |

An external database (PlanetScale or AWS RDS) needs `placement=External` and `provider`. It also needs one of these:

- `mode=Create` with a connection. PlanetScale creates the database and a login itself, as a MySQL or Postgres
  database from `engine`.
- `host`, `port`, `username`, and `password`, for a database that exists.

The dashboard sends neither `mode` nor those fields for an AWS RDS draft, so an AWS RDS draft made in the dashboard
cannot become ready. The **PlanetScale** row in the dashboard is greyed out. So create PlanetScale databases through
this API. See [Connect PlanetScale](/docs/guides/connectors/planetscale/).

#### Buckets

| Method and path | Fields | Meaning |
| --- | --- | --- |
| `POST /api/object-stores/create` | `project`, `environment`, `name`, `placement` (`InCluster` or `External`), `engine` (`S3` for an AWS bucket. Leave it out for a bucket in the cluster, which uses the cluster server's engine), `region`, `createBucket`, `connectionRef`, `bucket`, `draft` | Creates a draft bucket. Pass `cluster=on` with `engine` (`SeaweedFS` or `Garage`, required, any letter case) and `placement=InCluster` to create the cluster's object storage server instead. It also takes `cpu`, `memory`, `storageSize` (the volume, default `10Gi`, at least `1Gi`), `name` (default `cluster-s3`), `region` and `runOnTags`. `400` `choose SeaweedFS or Garage for cluster object storage` when `engine` is neither, `400` `Cluster object storage is already set up` when the server exists, and `400` `storage size must be a quantity such as 10Gi` or `storage size must be at least 1Gi` for a bad `storageSize` |
| `POST /api/object-stores/{store}/update` | `connectionRef`, `bucket`, `createBucket`, `project`, `environment` | `{"ok":true,"applied":"deploy","liveChanged":false}`. On the cluster object storage server it takes `cpu`, `memory` and `storageSize` instead, and returns `{"ok":true}`. `storageSize` can only grow the volume, and only where the cluster's storage can be resized: `400` `storage can be increased but not decreased (currently 10Gi)`, `this cluster's storage can't be resized, so the server stays at 10Gi`, and the two `storageSize` messages above |
| `POST /api/object-stores/{store}/deploy` | none | Deploys it |
| `POST /api/object-stores/{store}/retry-provision`, `/delete`, `/display-name` | as above | |
| `GET /api/object-stores/{name}/logs`, `GET /api/object-stores/{name}/runtime` | none | The cluster object storage server only (its name is the one it was created with, `cluster-s3` by default): `{"lines":"..."}` of its pod logs, and `{"pods":[...],"endpoint","engine","namespace"}`. Observer |

### Templates

| Method and path | Fields | Meaning |
| --- | --- | --- |
| `GET /api/templates` | none | `{"items":[template, ...]}`: the catalog, then saved templates. Catalog entries have `"builtin":true` (observer) |
| `POST /api/templates/{id}/deploy` | `project`, `environment` | Creates the template's resources as drafts. `{"ok":true,"created":[...]}` |
| `POST /api/templates/create` | `project`, `environment`, `name`, `description` | Saves an environment as a template. `{"ok":true,"template":{...},"skipped":["..."]}`: `skipped` names what it left out (external databases, logical databases, buckets). A function's mode and schedules, and a service's config files, are saved with it |
| `POST /api/templates/{id}/delete` | none | Deletes a saved template (catalog entries cannot be deleted) |

See [Templates](/docs/guides/templates/).

### Metrics and logs

These routes need Prometheus to be installed, and for history, the logging add-on. Or they need the metrics and log
stores outside the cluster that `spec.observability.external` names. In that case, Geass reads from those stores. See
[Observability](/docs/guides/observability/).

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

`window` is `15m` (default), `1h`, `6h`, or `24h`. A response is `{"window","step","available","series":{...}}`. Each
series is a list of `{"t": unixSeconds, "v": number}` points. The series are:

- `cpu` (cores).
- `memory` (bytes, the total of every replica).
- `network` and `networkIn` (bytes per second).
- `memoryPerReplica` (bytes, services: the busiest replica).
- `requests` (per second, services).
- `storage` (bytes, databases).
- `nodeCpu` and `nodeMemory` (cluster).

The series of the operator are:

- `reconcileErrors`, `statusWrites`, and `statusSkipped` (per second).
- `reconcileTime` and `apiLatency` (the 99th percentile, in seconds).
- `apiRequests` (per second).
- `queueDepth`.

Sometimes Prometheus, or the metrics store outside the cluster, cannot be reached, refuses the credentials of Geass,
or cannot be used. Then `available` is `false`, with a `message` that says which.

Log queries take these fields:

- `environment`.
- `service`: a service or database name.
- `q`: up to 200 characters.
- `level`: `error`, `warn`, `info`, or `debug`.
- `since`: a Go duration, for example `15m`. Up to 7 days live, and 90 days in history. The default is 1 hour in
  history.
- `tail`: 1 to 1000, default 200.
- `previous`: `true` for output from before the last restart of a container.
- `limit`: history only, 1 to 3000, default 500.

A line is `{"t","service","pod","container","line","level","previous"}`. The response also has `services`,
`truncated`, and `note`. History adds `available` and `retentionDays`. When Geass reads the log store outside the
cluster and that store gives a `retentionDays`, the history uses that value. History returns
`{"available":false,"message":"..."}` in these cases:

- The logging add-on is off.
- The log store does not answer, or refuses the credentials of Geass.
- The project requires node tags. The shared log store never receives the logs of such a project, so only live logs
  work.

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
| `POST /api/nodes/{node}/placement` | `region`, `zone`, `tags` (repeat), `reserved` (repeat), `compliance` (repeat) | Sets a node's [region, zone and tags](/docs/guides/scaling-and-placement/). A compliance tag must be one of the node's `tags`, is always reserved, and a tag some project requires becomes a compliance tag automatically. See [Compliance tags](/docs/guides/compliance-tags/). A node has at most 16 tags. `{"ok":true,"warnings":[...]}` |
| `POST /api/platform/default-region` | `region` | Sets the cluster's default region |
| `POST /api/ha-readiness/check` | none | Creates the high availability check |
| `POST /api/settings/domain/save` | `domain`, `subdomain`, `exposure` (`ingress` or `cloudflare-tunnel`), `tunnelCNAMETarget`, `cloudflareZoneId` | Sets the dashboard domain |
| `POST /api/settings/domain/verify` | none | `{"ok":true,"state":"success","message":"..."}`. `ok` is `false` for any other `state`. When HTTPS is not reachable, `message` can end with the cluster's `ClusterIssuerReady` message, for example when no `acmeEmail` is set |
| `POST /api/settings/domain/acme-email` | `email` | Sets the contact address of the Let's Encrypt account, `spec.addons.certManager.acmeEmail` on the cluster. Platform administrators only |
| `GET /api/settings/certificates` | none | `{"email":"...","state":"...","message":"..."}`. `state` is `missing`, `waiting`, `ready`, or `failing`, from the cluster's `ClusterIssuerReady` condition. Platform administrators only |
| `POST /api/settings/github/save` | `appID`, `clientID`, `slug`, `clientSecret`, `webhookSecret`, `privateKey` | Stores the GitHub App |
| `POST /api/settings/github/test`, `/clear` | `confirm=remove-github` for clear | Tests or removes it. The test checks the webhook URL, and then the app's permissions: it answers `{"ok":true,"warning":"..."}` when the webhook is right and permissions Geass needs are missing, with how to approve them |
| `GET /api/settings/github` | none | Callback and webhook URLs and the state (observer, and admins also get the manifest used to register the app) |
| `POST /api/settings/cloudflare/discover` | `apiToken` | `{"accounts":[...],"zones":[...]}` without saving anything |
| `POST /api/settings/cloudflare/save` | `apiToken`, `accountId`, `zoneId`, `zoneName`, `zonesJson`, `keepExisting` | Connects Cloudflare |
| `POST /api/settings/cloudflare/refresh`, `/clear` | none | Re-reads zones, or disconnects |
| `GET /api/settings/cloudflare` | none | `{"connected","ready"}`. Admins also get account, zone and tunnel IDs |
| `GET /api/settings/alerts` | none | How alerting is set up, and what is firing: `smtp` (`host`, `port`, `security`, `username`, `from`, `hasPassword`), `channels` (each with `id`, `name`, `type`, `recipients`, `events`, `projects`, `environments`, `notifyResolved`, `disabled`, and for Slack `target`, the host alone, and `hasSecret`), `listening`, `alertmanager` (`ready`, and `error` when it cannot be reached), `firing` (what Alertmanager holds, each with `kind`, `severity`, `project`, `projectName`, `environment`, `resource`, `title`, `summary`, `since`), `deliveries` (requests to send and failed ones since Alertmanager started, by `slack` and `email`), and `kinds`. It never returns the mail password or a Slack URL. A signed-in admin only, not a token |
| `POST /api/settings/alerts/smtp/save` | `host`, `port`, `security` (`starttls`, `tls` or `none`), `username`, `password`, `keepPassword`, `from` | Sets the mail server. The password goes in the Secret `geass-alert-smtp`. `keepPassword=on` keeps the stored one. `400` with a reason for a value that is not valid. When it is saved but Alertmanager could not be told yet, the answer is `200` with a `warning`. A signed-in admin only, not a token |
| `POST /api/settings/alerts/smtp/test`, `/clear` | none | The test connects from the operator, encrypts and signs in without sending: `{"ok":true}` or `{"ok":false,"error":"..."}`. `clear` removes the mail server and its password |
| `POST /api/settings/alerts/channels/save` | `id` (to edit), `name`, `type` (`slack` or `email`), `url` (Slack, and blank keeps the saved one), `recipients` (email), `event` (repeat), `project` (repeat), `environment` (repeat), `notifyResolved`, `disabled` | Makes or changes a channel. `{"ok":true,"id":"..."}`, with a `warning` when it is saved but Alertmanager could not be told yet. A channel's type cannot be changed. At most 20 channels |
| `POST /api/settings/alerts/channels/test`, `/delete` | `id` | The test puts a test alert into Alertmanager, waits for it to have loaded the channel and to report sending, and answers `{"ok":true}` or `{"ok":false,"error":"..."}`, whatever the channel is limited to and even while paused. It can take up to a minute. `delete` removes the channel and its Slack URL, after Alertmanager's configuration no longer refers to it |
| `GET /api/settings/addons`, `POST /api/settings/addons/{name}/save`, `.../retry` | As `GET /settings/addons` and `POST /settings/addons/{name}/save` and `/retry` in the route table above | The add-ons. Admins only. See [Add-ons](#add-ons) |
| `POST /api/settings/registry/save` | `repository`, `username`, `password`, `keepExisting` | Sets the registry that repository builds push to. The login is optional: both `username` and `password`, or neither. It is stored as the Secret `platform-registry` in `geass-system`. `keepExisting=on` keeps the stored login when only the repository changes |
| `POST /api/settings/registry/clear` | none | Removes the registry and its login |
| `GET /api/settings/registry` | none | `{"repository","configured","hasLogin"}`. It never returns the login |
| `GET /api/settings/auth` | none | How people sign in: `{"mode","configured","displayName","issuer","clientID","hasSecret","scopes","roleClaim","roleMap","requireClaim","sessionMinutes","refreshMinutes","staleGraceMinutes","callbackURL","backchannelLogoutURL","revokeTokensOnLogout","accessTokens","accessTokenAudience","test","hasPasswordAdmin","canEnable","tenancy"}`. `test` is what the last test sign-in found (`ok`, `email`, `role`, `reason`, `detail`, and with organizations read, `platformAdmin` and `orgs`: each organization the token named, with `idpID`, `name` when it is set up, `role`, `suspended` and `workspaces`). `tenancy` is `{"enabled","orgsClaim","platformClaim","platformValues","organizations","enableProblem","autoProvision","autoProvisionAllow","autoProvisionMax"}`. It never returns the client secret. A signed-in admin only: any token, observers included, and viewers get `403` |
| `POST /api/settings/auth/tenancy` | `orgsClaim`, `platformClaim`, `platformValues`, `enabled` (`on` or `off`), `autoProvision` (`off`, `pending` or `open`), `autoProvisionAllow` (ids or prefixes ending in `*`), `autoProvisionMax` (1 to 500). Only what is sent changes | Says how the token lists a person's organizations, and turns [organizations](/docs/guides/organizations-and-workspaces/) on or off. Turning on needs sign-in through the provider, a test sign-in of exactly these settings in the last 30 minutes that put the person in an organization that is set up (or made them a platform administrator), an admin with a password, and an organization. `400` says which. The claims cannot change while it is on. Turning it on or off ends the earlier sessions of the provider's people. A signed-in admin only, not a token |
| `GET /api/orgs` | none | `{"items":[{"name","displayName","idpID","suspended","clusterRef","workspaces":[{"id","displayName","idpID","projects"}],"projects","maxProjects","quota":{"cpu","memory","storage","pods"},"region","requiredTags","allowedDomains","autoCreated","idp"}]}`. `idp` is the organization's own identity provider, without its secret: `{"displayName","issuer","clientID","hasSecret","scopes","roleClaim","roleMap","requireClaim","enabled","loginPath","callbackPath","test","canEnable"}`. The platform's administrators only |
| `POST /api/orgs/create` | `displayName`, `idpID`, `clusterRef` (optional) | Creates an organization. `201` `{"ok":true,"name":"org-1a2b3c4d"}`. `409` when another is known by that `idpID`. `400` for a missing or too long value or a cluster that does not exist. Platform administrators only |
| `POST /api/orgs/{name}/update` | any of `displayName`, `idpID`, `suspended` (`on` or `off`), `clusterRef` (empty unbinds), `maxProjects` (empty for no limit), `quotaCPU`, `quotaMemory`, `quotaStorage`, `quotaPods` (empty takes one away), `region`, `requiredTags`, `allowedDomains` | Changes what is sent. Suspending stops the organization's open sessions immediately. A region and tags have to exist on nodes, and tags be reserved. Domains must not overlap another organization's. Platform administrators only |
| `POST /api/orgs/{name}/idp/save`, `.../idp/enable`, `.../idp/clear` | For `save`: `displayName`, `issuer`, `clientID`, `clientSecret` (or `keepSecret=on`), `roleClaim`, `roleMap`, `requireClaim`, `scopes`. For `enable`: `enabled` (`on` or `off`). For `clear`: none | Sets up an organization's own identity provider (turned off, and tested again before it is turned on), turns it on or off, or removes it. Turning it on needs organizations on and a test sign-in of the provider as it is saved (`/auth/oidc/login?org=<name>&purpose=test`). Ends the sessions made through it. A platform administrator signed in to the dashboard only, not a token |
| `GET /api/alerts` | none | An organization's own channels, the alerts it can choose from and what is firing for its projects: `{"kinds","channels","listening","alertmanager","firing"}`. A signed-in administrator of the organization. `403` for the platform, which has `/api/settings/alerts` |
| `POST /api/alerts/channels/save`, `.../delete`, `.../test` | For `save`: `name`, `type` (`slack`), `url` (`https://hooks.slack.com/...`), `event`, `project`, `environment`, `notifyResolved`, `disabled`, `id` (to change one). For `delete` and `test`: `id` | An organization's alert channel. `400` "not found" for another organization's. Ten per organization |
| `GET /api/registry` | none | An organization's own registry: `{"repository","configured","hasLogin","platformRepository","ownRequired"}`. Never the login. `ownRequired` is true while the platform's `spec.tenancy.registry` is set, when the organization's builds use its own registry or none. An administrator of the whole organization, signed in |
| `POST /api/registry/save`, `.../clear` | For `save`: `repository`, `username`, `password` (both or neither), `keepExisting=on`. For `clear`: none | Sets or removes the registry the organization's builds push to, with a login of its own. While `spec.tenancy.registry` is set, `409` for a repository that overlaps the platform's repository (outside `<platform repository>/<organization>`) or another organization's registry |
| `POST /api/orgs/{name}/delete` | `confirmName` (the name) | Deletes an organization that has no projects, database servers or connections (`409` otherwise), with its alert channels and registry login. Platform administrators only |
| `POST /api/orgs/{name}/workspaces/create`, `.../workspaces/{id}/delete` | `displayName`, `idpID` (to create) | Adds a workspace (`{"ok":true,"workspace":"ws-a1b2c3"}`), or removes one that has no projects (`409` otherwise). Platform administrators only |
| `POST /api/projects/{project}/assign` | `org`, `workspace` | Puts a project in an organization and a workspace of it. Both empty takes it out of its organization. `404` for an organization or workspace that does not exist. `409` when the organization is at its project limit, or the project has databases on a server another organization owns, or, while organizations are on, on the platform's server when it is given to an organization (a move within the organization it is in is not refused). Platform administrators only: an organization's own administrators get `403` |
| `POST /api/settings/auth/save` | `displayName`, `issuer`, `clientID`, `clientSecret`, `keepSecret`, `scopes`, `roleClaim`, `orgsClaim`, `roleMap` (`provider-role=admin` or `=viewer`, one per line), `requireClaim` (`claim=value`, one per line), `sessionMinutes` (5 to 720) | Stores the identity provider's settings and the client secret (the Secret `platform-auth`). It does not connect and does not turn the provider on. `orgsClaim` is the claim that lists the organizations, the same as `POST /api/settings/auth/tenancy` saves. With it, `roleClaim` can be left out, which is how a first setup for organizations starts, and while organizations are on it cannot change (`400`). A form that does not send `orgsClaim` leaves it as it was. A form that sends it empty takes it away. `keepSecret=on` with no `clientSecret` keeps the saved one, only for the same issuer and client ID. `400` for an invalid setting and while people sign in through the provider. A signed-in admin only, not a token |
| `POST /api/settings/auth/mode` | `mode` (`local` or `external`) | Switches how people sign in. `external` needs the settings to be complete, a test sign-in of exactly them in the last 30 minutes as someone who maps to admin, and an admin with a password. `400` says which is missing. Turning it on ends the open sessions of users of the Secret who are not admins. `local` is always allowed. A signed-in admin only, not a token |
| `POST /api/settings/auth/logout-policy` | `revokeTokens` (`on` or `off`) | Sets whether a back-channel logout from the identity provider also revokes the tokens and deploy hooks the person made (their sessions always end). Default on. It does not change who can sign in, so it can be changed while people sign in through the provider. `400` for another value or while no provider is set up. A signed-in admin only, not a token |
| `POST /api/settings/auth/clear` | none | Removes the identity provider and its client secret and goes back to local sign-in. A signed-in admin only, not a token |
| `POST /api/cloud-connections/create` | `name`, `provider` (`AWS` or `PlanetScale`, default `AWS`), `project` (the `proj-...` ID), or `org` (an organization, for all of its projects: named by a platform administrator, and by an administrator of the whole organization for their own, who need not name it), and `accessKeyId`, `secretAccessKey`, `region` or `token`, `organization` | Adds an AWS or PlanetScale connection for buckets, backups or databases. `{"ok":true}`. It checks the project first, then creates the connection and the Secret `<name>-credentials`. A Secret of that name that already exists is kept for the platform, and refused ("that name is already taken") for an organization. A request that names a cloud that creates machines, or the purpose `Compute`, goes to `/api/connections/create` instead |
| `POST /api/connections/create`, `/api/connections/{name}/update`, `/delete`, `/verify` | As `POST`, `PATCH` and `DELETE /connections` and `POST /connections/{name}/verify` in the table above | Connections for machines. `delete` takes `confirmName` |
| `POST /api/pools/create`, `/api/pools/{name}/update`, `/delete`, `/resume` | As `POST` and `PATCH /pools`, `DELETE /pools/{name}` and `POST /pools/{name}/resume`. `delete` takes `confirmName` | Node pools. On a form, a switch (`spot`, `suspend`, `autoscaling`) is on when sent as `on`, `true`, `1` or `yes`, and a list is the field repeated |
| `POST /api/servers/create`, `/api/servers/{name}/delete` | As `POST /servers` and `DELETE /servers/{name}` | Single servers |

For details of the last groups, see [Cloud connectors](/docs/guides/connectors/),
[Cloud connections for machines](/docs/guides/connectors/clouds/), and [Node pools](/docs/guides/node-pools/).

### Project token endpoints

A project token can call only these routes, and only for services in its own project and environment. On `/api/v1`,
the same routes work with JSON bodies. A project token can also call `GET /api/v1/apps`, `/apps/{name}`,
`/apps/{name}/status`, `/apps/{name}/deployments`, and `/apps/{name}/logs/stream`.

| Method and path | Meaning |
| --- | --- |
| `GET /api/token`, `GET /api/token/services` | Describe the token, and list services and databases |
| `POST /api/token/deploy` | Deploy pending changes in the token's environment |
| `GET /api/apps/{app}/logs`, `/runtime`, `/variables`, `/metrics` | Read |
| `GET /api/apps/{app}/deployments/{id}/logs` | Read a deploy's logs |
| `POST /api/apps/{app}/deploy`, `/rollback`, `/scale`, `/retry-provision`, `/update` | Change the service |
| `POST /api/apps/{app}/build`, `/build/cancel` | Build |
| `POST /api/apps/{app}/config/set`, `/config/delete`, `/config/raw` | Edit configuration |
| `POST /api/apps/{app}/secrets/set`, `/secrets/delete`, `/env/delete` | Edit secrets and references |

Anything else returns `403` `this token cannot access that endpoint`.

### Endpoints that are not for scripts

These endpoints exist for the browser flow:

| Path | Purpose |
| --- | --- |
| `GET /geass-probe` | A public health answer, `{"ok":true,"service":"geass-dashboard",...}`, used to verify the dashboard's address |
| `POST /webhooks/github` | GitHub's webhook, checked by HMAC signature. The answer has `delivery`, the ID GitHub sent in `X-GitHub-Delivery`. See [Webhooks](/docs/guides/github-and-builds/#from-github-to-geass) |
| `GET /github/callback`, `GET /settings/github/manifest/callback` | GitHub redirects here after install or app creation. Signed-in admins only |
| `GET /auth/oidc/login` | Sends the browser to the identity provider (`?next=/path` says where to land afterwards, on this dashboard only). It sets a signed, single-use state cookie. `?purpose=test` starts a test sign-in and needs a signed-in admin session, not a token. Without the provider turned on it goes back to `/?sso_error=disabled` |
| `GET /auth/oidc/callback` | The provider sends the browser back here with a code. It checks the state cookie, the issuer and the ID token, and starts the session (or records the test). A failure goes to `/?sso_error=<code>`, where the code is one of `state`, `forbidden`, `unavailable`, `disabled`, `provider`, `provider_denied`, `no_email`, `email_unverified`, `required_claim`, `no_role` or, with organizations on, `no_org` (the token puts the person in no organization that is set up) or `org_pending` (the organization was made for them by their sign-in and waits for approval). `GET /auth/oidc/login?org=<name>` signs in to an organization through its own provider |
| `POST /auth/oidc/backchannel-logout` | The provider calls this when someone signed out or was removed, with `logout_token` (form-encoded). It ends that person's sessions and, unless the setting above is off, revokes the tokens and deploy hooks they made. Its credential is the provider's signature on the token: `400` for a token that is not right, `429` for a source that keeps sending them, `200` for one it acted on or already had |
| `GET /api/events`, `GET /api/apps/{app}/console/ws` | Streams used by the dashboard |
| `POST /api/apps/{app}/console/stream`, `POST /api/object-stores/<server name>/console/create` and `.../console/stream` | An older streaming form of the service terminal, and the same pair for the object storage server. The dashboard uses neither |

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

The API is a thin layer over Kubernetes. A `POST` reads the resource, changes its `spec` and annotations, and writes it
back. The controllers do the rest. You can see the same objects with
`kubectl -n geass-system get geassapps,geassdatabases,geassobjectstores`. A deploy sets `spec.deploy.enabled` to true,
clears `geass.dev/pending-changes`, and records the deployed spec in `geass.dev/last-deployed`. See
[Resources overview](/docs/reference/resources/overview/).

`/api/v1` is a layer in front of that layer. It sits before authentication, so the rules for who can call what are the
rules that the dashboard routes apply:

- For a `GET`, `/api/v1` serves the reads that only `/api/v1` has. These reads are resource lists and single resources,
  `status`, `deployments`, `connection`, `nodes`, the OpenAPI document, and the device-code lookup. `/api/v1` passes
  every other `GET` to the dashboard route of the same name, without the `/v1`.
- For a write, `/api/v1` maps the verb and path to a dashboard route. For example, it maps `POST /apps` to
  `POST /api/apps/create`, `PATCH /apps/{name}` to `POST /api/apps/{name}/update`, and `DELETE /apps/{name}` to
  `POST /api/apps/{name}/delete`. Then it turns the JSON body into a form, and puts any error in the
  `{"error","code"}` shape.

The operator log records the method, path, status, and duration of each request, but not who sent it.

## Troubleshooting

**`400` `request origin could not be verified` on a cookie request.** Add an `Origin` header with the address of the
dashboard, or use a token.

**`401` on every call.** Make sure that the header is `Authorization: Bearer <token>` with no extra quotes, and that
the token has not expired. `GET /api/v1/me` is the quickest test.

**`403` `viewer role cannot access this` with an observer token.** An observer can read, including logs, but Geass
refuses any write with this message. Use an `admin` token or a project token.

**`403` `this token cannot access that endpoint`.** A project token called something outside its list. See
[Project tokens on /api/v1](#project-tokens-on-apiv1).

**`404 page not found` for a path that looks right.** Most "not found" answers from the dashboard routes are plain
text. The resource name is wrong, or the path has a typo. Use the ID from `metadata.name`, not the display name. On
`/api/v1`, the same answer arrives as `{"error":"404 page not found","code":"not_found"}`.

**`400` `there is no PUT /api/v1/...`.** The verb has no mapping. Use `PATCH` on `/apps/{name}`, `/databases/{name}`,
`/object-stores/{name}`, and `/projects/{name}`. Use `POST` for actions.

**A write returns `{"ok":true}` but nothing changed.** The change is pending. Deploy it. For a service, check `pending`
and `applied` in the response.

**A switch does not turn off.** On `/api/v1`, `false` leaves the field out, which keeps its value. Send the string that
the handler expects, for example `"scaleToZero": "off"`.

**`400` with a capacity message on deploy.** The cluster does not have room for the CPU and memory that the change
requests. See [Usage and capacity](/docs/guides/usage-and-capacity/).

**Form fields seem ignored on a dashboard route.** You sent JSON. Use `--data-urlencode`, or call the same route under
`/api/v1`.

## Related

- [Users and API tokens](/docs/guides/users-and-tokens/)
- [Command line](/docs/guides/command-line/) and [CLI reference](/docs/reference/cli/)
- [MCP server](/docs/guides/mcp-server/) and [MCP tools](/docs/reference/mcp-tools/)
- [Resources overview](/docs/reference/resources/overview/)
- [Projects and environments](/docs/guides/projects-and-environments/)
- [Observability](/docs/guides/observability/)
- [Security](/docs/operations/security/)
