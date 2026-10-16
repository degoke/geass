---
title: "MCP tools"
description: "Every tool the geass MCP server offers: its arguments, what it returns, the token role it needs, the API route it calls and an example call."
---

The `geass mcp` command runs a Model Context Protocol (MCP) server that gives an AI agent the same operations as the
[command line](/docs/reference/cli/). This page lists every tool the server can register. To run the server and connect
an agent, read [MCP server for AI agents](/docs/guides/mcp-server/).

## What the server exposes

- **Tools only.** The server registers tools. It exposes no MCP resources and no prompts, and it advertises no
  completions. It does advertise the standard logging capability of the MCP Go SDK it is built on.
- **Identity.** The server's name is `geass`, its title is `Geass` and its version is the command line's version
  (`dev` when the build did not set one).
- **Instructions.** When an agent connects, the server sends it a short briefing (see [Instructions](#instructions)).
- **Transports.** Standard input and output, or Streamable HTTP with `--listen`.
- **31 tools in all**, 30 without `delete_resource`. Which of them an agent sees depends on its token
  (see [Which tools an agent sees](#which-tools-an-agent-sees)).

Every tool takes a JSON object. An argument that is not in the tool's schema is rejected. A tool's result is one block
of text: indented JSON for every tool except `get_logs`, which returns plain lines. A result longer than 60,000
characters is cut and ends with `... (cut: the result was too long; narrow it with the tool's options)`.

### Hints

Each tool carries the standard MCP annotations, so an agent host can decide when to ask you first. `read-only` means
`readOnlyHint: true`. `idempotent` means `idempotentHint: true`. Every tool except `delete_resource` has
`destructiveHint: false`, and every tool has `openWorldHint: false`.

### Errors

When Geass refuses or fails a call, the tool returns an error result (`isError: true`) whose text is the message from
the API, with a note in brackets added after a space, for three statuses:

| Status | Text added |
| --- | --- |
| `401` | `(the token is missing, wrong or expired)` |
| `403` | `(this token is not allowed to do that; do not retry)` |
| `404` | `(it does not exist, or is not visible to this token)` |

Any other status gives the API's message alone, such as `replicas must be a whole number from 0 to 1000`. A deploy that
fails while the agent waits for it is not an error: it is a result that says why (see [`deploy_app`](#deploy_app)).

## Overview

| Tool | What it does | Needs | Hints |
| --- | --- | --- | --- |
| [`whoami`](#whoami) | Says who the agent acts as and what it may do | Any token | read-only |
| [`list_projects`](#list_projects) | Lists projects | Any workspace token | read-only |
| [`list_apps`](#list_apps) | Lists services | Any token | read-only |
| [`get_app`](#get_app) | One service's settings and status | Any token | read-only |
| [`app_status`](#app_status) | Whether a service is up, and what its last deploy did | Any token | read-only |
| [`app_deployments`](#app_deployments) | A service's deployment history | Any token | read-only |
| [`app_metrics`](#app_metrics) | CPU, memory, network and requests over time | Any token | read-only |
| [`get_logs`](#get_logs) | Recent log lines of a service | Read only, with logs | read-only |
| [`deployment_logs`](#deployment_logs) | Build and deploy logs of one deployment | Read only, with logs | read-only |
| [`list_variables`](#list_variables) | Names of a service's variables | Read only, with logs | read-only |
| [`list_databases`](#list_databases) | Lists databases | Any workspace token | read-only |
| [`get_database`](#get_database) | One database's settings and status | Any workspace token | read-only |
| [`database_connection`](#database_connection) | Host, port, database and user | Full access | read-only |
| [`list_buckets`](#list_buckets) | Lists object storage buckets | Any workspace token | read-only |
| [`list_nodes`](#list_nodes) | Lists the cluster's nodes and their tags | Any workspace token | read-only |
| [`project_compliance`](#project_compliance) | A project's required node tags | Any workspace token | read-only |
| [`project_network`](#project_network) | Who can reach a project's services | Any workspace token | read-only |
| [`project_quota`](#project_quota) | A project's limits and what it uses of them | Any workspace token | read-only |
| [`set_project_quota`](#set_project_quota) | Changes a project's limits | Full access | idempotent |
| [`deploy_app`](#deploy_app) | Deploys a service's pending changes | Full access or project token | |
| [`rebuild_app`](#rebuild_app) | Runs a repository service's build again, and rolls it out | Full access or project token | |
| [`rollback_app`](#rollback_app) | Returns a service to an earlier deployment | Full access or project token | |
| [`scale_app`](#scale_app) | Sets the number of copies | Full access or project token | idempotent |
| [`set_sleep_when_idle`](#set_sleep_when_idle) | Makes a service sleep when idle, or stay running | Full access or project token | idempotent |
| [`set_variable`](#set_variable) | Sets a variable or secret | Full access or project token | idempotent |
| [`unset_variable`](#unset_variable) | Removes a variable or secret | Full access or project token | idempotent |
| [`set_function_trigger`](#set_function_trigger) | Adds or replaces a function's schedule | Full access or project token | idempotent |
| [`remove_function_trigger`](#remove_function_trigger) | Removes a function's schedule | Full access or project token | idempotent |
| [`invoke_function`](#invoke_function) | Calls a function at its public address | Full access or project token | |
| [`create_app`](#create_app) | Creates a service from an image or a repository | Full access | |
| [`delete_resource`](#delete_resource) | Permanently deletes a resource | Full access, and `--allow-delete` | destructive |

**Needs** uses the access names of the dashboard. **Full access** is the `admin` role, **Read only, with logs** is
`observer`, and **Read only** is `viewer`. A **project token** is limited to one environment of one project. Where a
row says **Any token**, a project token can use the tool for services in its own environment. **Any workspace token**
means a project token cannot. **Read only, with logs** also covers **Full access** and a project token. See
[Users and API tokens](/docs/guides/users-and-tokens/).

## Which tools an agent sees

A tool that a token cannot use is not registered at all, so the agent never sees it. The flags of `geass mcp` narrow
this further. `--read-only` removes every tool that changes anything. It keeps `database_connection` for a **Full
access** token, because that tool only reads. `--allow-delete` adds `delete_resource`.

| Token | Tools |
| --- | --- |
| **Read only** (`viewer`) | `whoami`, `list_projects`, `list_apps`, `get_app`, `app_status`, `app_deployments`, `app_metrics`, `list_databases`, `get_database`, `list_buckets`, `list_nodes`, `project_compliance`, `project_network`, `project_quota` (14) |
| **Read only, with logs** (`observer`) | The 14 above, plus `get_logs`, `deployment_logs`, `list_variables` (17) |
| **Full access** (`admin`) | All except `delete_resource` (30), and `delete_resource` with `--allow-delete` (31) |
| **Full access** with `--read-only` | The 17 of an `observer`, plus `database_connection` (18). |
| Project token | `whoami`, `list_apps`, `get_app`, `app_status`, `app_deployments`, `app_metrics`, `get_logs`, `deployment_logs`, `list_variables`, `deploy_app`, `rebuild_app`, `rollback_app`, `scale_app`, `set_sleep_when_idle`, `set_variable`, `unset_variable`, `set_function_trigger`, `remove_function_trigger`, `invoke_function` (19) |

A project token with `--read-only` gets the first nine tools in its list. Hiding a tool is a convenience. The API
checks the token on every call, so a tool that is shown can still fail with a `403` for something the token may not
reach, such as `app_metrics` with `kind` set to `databases` for a project token.

## Instructions

On connecting, the server tells the agent what it is allowed to do. The text starts with `Geass runs services,
databases and buckets on a cluster. Use list_apps and app_status to see what is running.` and then, depending on the
token:

- For a project token: `You act with a token limited to <project>/<environment>: you can only see and change services
  there.`
- For a token that cannot change anything, or with `--read-only`: `You can only read: nothing here can change
  anything.`
- Otherwise: `Changing a variable does not apply it: call deploy_app afterwards (or pass deploy=true). To deploy and
  know whether it worked, call deploy_app with wait=true: it returns the outcome, and says why if the build failed. To
  build a service from its repository again without a push, call rebuild_app (also with wait=true).`

Every version ends with `Secret values are never returned by any tool. Plain configuration is not secret and is returned
as it is, so keep anything sensitive in a secret.`

## Services

### `whoami`

Says who the agent acts as and what it may do.

- **Arguments:** none.
- **Needs:** any token. **Calls:** nothing. It returns what the server learned from `GET /me` when it started.
- **Returns:** the identity.

| Field | Meaning |
| --- | --- |
| `username` | `token:<token name>`. |
| `role` | `admin`, `observer` or `viewer`. A project token reports `admin`. |
| `scope` | `workspace` or `project`. |
| `project`, `environment` | The project token's limits. Only for a project token. |
| `canMutate` | Whether the token can change things. |
| `canReadSensitive` | Whether it can read logs and variable names. |
| `authentication` | `token`. |

```json title="Example result"
{
  "username": "token:CLI: geass CLI on laptop",
  "role": "observer",
  "scope": "workspace",
  "canMutate": false,
  "canReadSensitive": true,
  "authentication": "token"
}
```

### `list_projects`

Lists the projects.

- **Arguments:** none.
- **Needs:** any workspace token. **Calls:** `GET /projects`.
- **Returns:** `{"items": [...]}`. Each item is a project summary: `kind` (`project`), `name` (the project's ID, such
  as `proj-1a2b3c4d`), `displayName`, `state`, `message`, `createdAt` and `details` with `environments`, and `region` and
  `requiredTags` when set.

### `list_apps`

Lists services with their state (`ready`, `pending`, `failed`, `draft` or `deleting`).

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `project` | string | no | Only services in this project (its ID). |
| `environment` | string | no | Only services in this environment, such as `production`. |

- **Needs:** any token. A project token sees only its own environment. **Calls:** `GET /apps` with `project` and
  `environment`.
- **Returns:** `{"items": [...]}`. Each item has `kind` (`app`), `name`, `displayName`, `project`, `environment`,
  `state`, `message`, `url`, `pendingChanges`, `createdAt` and `details`. `details` holds `port`, `source` (`image`,
  `git` or `function`), the `image`, or the `repository` and `branch`, or the `runtime`, `mode` and `triggers`,
  `replicas`, and `scaleToZero` (`idleSeconds`, `maxReplicas`, `active`, `message`) when the service sleeps when idle.

```json title="Example call"
{ "project": "proj-1a2b3c4d", "environment": "production" }
```

### `get_app`

Shows one service's settings and status.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `app` | string | yes | The service's name, as listed by `list_apps`. |

- **Needs:** any token. **Calls:** `GET /apps/{app}`.
- **Returns:** `{"resource": {...}, "spec": {...}, "status": {...}}`. `resource` is the same summary as in `list_apps`.
  `spec` holds the service's settings and `status` what the operator last reported.

Secret values are never included: `spec.secretData` lists their names with empty values. Plain configuration is not
secret and appears in `spec.configData` and `spec.env` when the token is **Full access**. A **Read only** or **Read only,
with logs** token gets them removed. The tool's description says the same.

### `app_status`

Shows whether a service is up and what its last deploy did. Call it to find out whether a deploy worked.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `app` | string | yes | The service's name. |

- **Needs:** any token. **Calls:** `GET /apps/{app}/status`.
- **Returns:**

| Field | Meaning |
| --- | --- |
| `name`, `project`, `environment` | Which service. |
| `state` | `ready`, `pending`, `failed`, `draft` or `deleting`. |
| `message` | Why the service is not ready, when it says. |
| `url` | Its public address, when it has one. |
| `ready` | `true` when `state` is `ready`. |
| `pendingChanges` | How many changes wait for a deploy. |
| `replicas` | `desired`, `ready` and `available` copies. Present once the service has a Deployment. |
| `sleeping` | `true` for a service that sleeps when idle and has stopped. It is waiting for a request, not down. |
| `scaleToZero` | For a service that asked to sleep when idle: `idleSeconds`, `maxReplicas`, and `active` and `message` once the operator has said whether it does and why not. |
| `build` | The latest build: `name`, `phase` (`Pending`, `WaitingForCI`, `Running`, `Succeeded`, `Failed` or `Cancelled`), `failureReason`, `sourceRevision`, `ciState`, `startedAt`, `completedAt`. |
| `lastAttempt` | The latest deploy attempt: `id`, `phase` (`Running`, `Succeeded` or `Failed`), `startedAt`, `completedAt`, `summary` and `errors` (each with `recordedAt` and `message`). |

```json title="Example result"
{
  "name": "web",
  "project": "proj-1a2b3c4d",
  "environment": "production",
  "state": "pending",
  "ready": false,
  "pendingChanges": 0,
  "replicas": { "desired": 2, "ready": 0, "available": 0 }
}
```

### `app_deployments`

Lists a service's deployment history, newest first. The ids are what `rollback_app` and `deployment_logs` take.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `app` | string | yes | The service's name. |

- **Needs:** any token. **Calls:** `GET /apps/{app}/deployments`.
- **Returns:** `{"items": [...]}`. Each item has `id`, `phase`, `image`, `commit`, `trigger`, `actor`, `changeTitle`,
  `createdAt`, `startedAt` and `completedAt`. A rollback adds an entry of its own.

### `app_metrics`

Shows CPU, memory, network and request use over time for a service, a database or a project.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `name` | string | yes | The service, database or project to measure. |
| `kind` | string | no | `apps` (the default), `databases` or `projects`. |
| `window` | string | no | How far back: `15m`, `1h`, `6h` or `24h`. Default `1h`: the tool sends `1h` when you leave it out. (A raw call to the API with no `window` answers for `15m`.) |

- **Needs:** any token for `apps` (a project token for its own services). Any workspace token for `databases` and
  `projects`. **Calls:** `GET /{kind}/{name}/metrics` with `window`.
- **Returns:** `window`, `step` (seconds between points), `available`, an optional `message`, and `series`. Each series
  is a list of `{"t": <unix seconds>, "v": <value>}`. The series are `cpu` (cores), `memory` (bytes, the total of every replica), `memoryPerReplica` (bytes, services
  only: the busiest replica), `network` (bytes
  sent per second), `networkIn` (bytes received per second), `requests` (requests per second, services only) and
  `storage` (bytes, databases only). `available` is `false`, with a `message`, when Prometheus is not installed or not
  reachable (see [Observability](/docs/guides/observability/)).

```json title="Example call"
{ "name": "web", "window": "1h" }
```

## Logs and variables

These three tools need a token that may read what a **Read only** token may not: **Read only, with logs**, **Full
access** or a project token.

### `get_logs`

Reads a service's recent log lines. Use `level` or `search` to narrow them.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `app` | string | yes | The service's name. |
| `tail` | integer | no | How many of the newest lines to return in all. Default 100, at most 500. `level` and `search` look through the most recent lines only, so raise it to look further back. |
| `since` | string | no | Only lines newer than this long ago, such as `15m` or `2h`. Go duration syntax, up to 7 days. |
| `level` | string | no | Only this level: `error`, `warn`, `info` or `debug`. |
| `search` | string | no | Only lines containing this text, up to 200 characters. |

- **Needs:** Read only, with logs, Full access, or a project token (for its own services). **Calls:**
  `GET /apps/{app}/logs` with `tail`, `since`, `q` and `level`.
- **Returns:** plain text, one line per log line, oldest first, `<time in RFC 3339> <pod> <text>`. When nothing matches:
  `No log lines matched.`
- **Filters.** The dashboard reads `tail` lines from the end of each copy's log and applies `level` and `search` to
  those, so `level` `error` finds the errors among each copy's newest lines. `level` `warn` also returns errors. A
  service with several copies returns the newest `tail` lines of all of them together.

```text title="Example result"
2026-10-12T09:41:07Z web-6d9c7b8f5-x2k4p Error: connect ECONNREFUSED 203.0.113.10:5432
```

### `deployment_logs`

Returns the build and deploy logs of one deployment. Read these when a deploy failed.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `app` | string | yes | The service's name. |
| `deployment` | string | yes | A deployment id from `app_deployments`. |

- **Needs:** Read only, with logs, Full access, or a project token. **Calls:**
  `GET /apps/{app}/deployments/{deployment}/logs`.
- **Returns:** `id` and `sections`, a list of `{"id", "title", "lines"}` for the deploy attempt, the build (for a
  repository) and the rollout. Sections with nothing in them are left out. `lines` is the stage's log as one block of
  text, a line to a line. A stage longer than 200 lines is cut to its last 200, which is where a failure says why, and
  starts with `... (N earlier lines left out)`. The whole log is at `GET /apps/{app}/deployments/{deployment}/logs`.

```json title="Example result"
{
  "id": "web-7x2kq",
  "sections": [
    { "id": "provision", "title": "Provision", "lines": "Provisioning failed\n2026-10-12T09:41:07Z image pull failed" },
    { "id": "build", "title": "Build", "lines": "Kaniko completed without an immutable image digest" }
  ]
}
```

### `list_variables`

Lists the names of a service's variables and secrets.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `app` | string | yes | The service's name. |

- **Needs:** Read only, with logs, Full access, or a project token. **Calls:** `GET /apps/{app}/variables`.
- **Returns:** `secrets` (a list of names), `env` (a list of `{"name", "kind"}`, where `kind` is `literal` or
  `secret`) and `config` (an object of plain configuration).

Secret values are never returned: secrets come back by name only. `config` is an object of plain configuration with
its values, because plain configuration is not secret. The tool's description says so. Store anything sensitive as a
secret.

```json title="Example result"
{
  "secrets": ["STRIPE_KEY"],
  "env": [],
  "config": { "LOG_LEVEL": "info" }
}
```

## Databases, buckets and nodes

All of these are hidden from project tokens.

### `list_databases`

Lists databases with their state.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `project` | string | no | Only databases in this project. |
| `environment` | string | no | Only databases in this environment. |

- **Needs:** any workspace token. **Calls:** `GET /databases`.
- **Returns:** `{"items": [...]}` of summaries like `list_apps`. `details` holds `engine`, `highAvailability`, and
  `placement` and `version` when set.

### `get_database`

Shows one database's settings and status.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `name` | string | yes | The database's name. |

- **Needs:** any workspace token. **Calls:** `GET /databases/{name}`.
- **Returns:** `{"resource": {...}, "spec": {...}, "status": {...}}`. A token that cannot change things (**Read only**
  or **Read only, with logs**) gets the engine, host and connection details removed.

### `database_connection`

Shows how to reach a database: host, port, database and user. The password and the connection string are never
returned.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `name` | string | yes | The database's name. |

- **Needs:** Full access. It is offered with `--read-only` too, since it only reads. **Calls:**
  `GET /databases/{name}/connection`, never with `reveal`.
- **Returns:** the connection facts that exist, among `host`, `port`, `database` and `username`.

```json title="Example result"
{
  "database": "orders",
  "host": "orders-db.example.svc",
  "port": "5432",
  "username": "app"
}
```

### `list_buckets`

Lists object storage buckets.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `project` | string | no | Only buckets in this project. |
| `environment` | string | no | Only buckets in this environment. |

- **Needs:** any workspace token. **Calls:** `GET /object-stores`.
- **Returns:** `{"items": [...]}` of summaries. A token that cannot change things does not get the bucket names or the
  endpoint.

### `list_nodes`

Lists the cluster's nodes with their region, zone and tags, including compliance tags.

- **Arguments:** none.
- **Needs:** any workspace token. **Calls:** `GET /nodes`.
- **Returns:** `{"items": [...]}`. Each node has `name`, `ready`, `schedulable`, `controlPlane`, `region`, `zone`, `tags`
  (each with `name`, and `reserved` and `compliance` when true), `cpu` and `memory`.

### `project_compliance`

Shows the node tags a project requires, such as `hipaa`, and where it falls short of them.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `project` | string | yes | The project's ID. |

- **Needs:** any workspace token. **Calls:** `GET /projects/{project}/compliance`.
- **Returns:** `requiredTags`, `region`, `databases` (each with `name`, `compliant` and `missing`) and `openNodes`,
  the nodes that carry a required tag without being reserved for it. See
  [Compliance tags](/docs/guides/compliance-tags/).

### `project_network`

Shows who can reach a project's services: only the project itself (with the platform and public domains) or every
service in the cluster, any other projects let in, and whether the services are kept away from the machines the cluster
runs on. This tool only reads. There is no tool that changes it, because opening a project up, or letting it reach the
machines, is a choice for a person.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `project` | string | yes | The project's ID. |

- **Needs:** any workspace token. **Calls:** `GET /projects/{project}/network`.
- **Returns:** `isolation` (`Project` or `None`), `allowFromProjects` and `blockNodeAddresses`. See
  [Network isolation](/docs/guides/network-isolation/).

### `project_quota`

Shows what a project is capped at, per environment and for the project as a whole, and what its environments ask for
together. A project with no limits can ask for everything the cluster has.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `project` | string | yes | The project's ID. |

- **Needs:** any workspace token. **Calls:** `GET /projects/{project}/quota`.
- **Returns:** `environment`, `project` and `used`, each with `cpu`, `memory`, `storage` and `pods` where they are set.
  See [Project limits](/docs/guides/project-limits/).

## Deploying and changing services

These tools need **Full access** or a project token (`set_project_quota` needs **Full access**), and are absent with `--read-only`.

### `deploy_app`

Deploys a service's pending changes. With `wait`, waits until the service is ready or has failed and returns the
outcome, including why a build failed.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `app` | string | yes | The service to deploy. |
| `wait` | boolean | no | Wait until it is ready or has failed, and return the outcome (recommended). |
| `timeout_seconds` | integer | no | How long to wait. Default 300, at most 900. |

- **Calls:** `POST /apps/{app}/deploy`, and with `wait`, `GET /apps/{app}/status` every 3 seconds.
- **Returns without `wait`:** `{"app": "web", "deploying": true, "next": "call app_status to see how it is going"}`.
- **Returns with `wait`:** the outcome.

| Field | Meaning |
| --- | --- |
| `app` | The service. |
| `status` | The last status read, as returned by `app_status`. |
| `ready` | `true` only when the service is ready, has no pending changes and its latest build, if it has one, succeeded. |
| `failed` | `true` when the deploy failed. Only present then. |
| `error` | Why it failed (`the deploy failed: <reason>`), or why the wait ended (`gave up waiting for web after 5m0s (it is pending): context deadline exceeded`). Absent when it succeeded. |

A failed deploy and a wait that ran out are results, not errors, so the agent reads the reason. If the agent's client
sends a progress token with the call, the server reports progress while it waits, as lines such as `web: pending, build
Running`.

```json title="Example call"
{ "app": "web", "wait": true, "timeout_seconds": 300 }
```

```json title="Example result"
{
  "app": "web",
  "ready": true,
  "status": {
    "name": "web",
    "project": "proj-1a2b3c4d",
    "environment": "production",
    "state": "ready",
    "url": "https://web.example.com",
    "ready": true,
    "pendingChanges": 0,
    "replicas": { "desired": 2, "ready": 2, "available": 2 }
  }
}
```

```json title="Example result of a failed build"
{
  "app": "web",
  "error": "the deploy failed: Kaniko completed without an immutable image digest",
  "failed": true,
  "ready": false,
  "status": {
    "name": "web",
    "project": "proj-1a2b3c4d",
    "environment": "production",
    "state": "pending",
    "ready": false,
    "pendingChanges": 0,
    "build": {
      "name": "web-build-x7k2p",
      "phase": "Failed",
      "failureReason": "Kaniko completed without an immutable image digest"
    }
  }
}
```

### `rebuild_app`

Runs the build of a service that is built from a repository again, from the head of its branch (or the commit it pins),
and rolls the service out onto the new image. It is what a push does. It does not apply changes that were saved to the
service and not yet deployed: call `deploy_app` for those. With `wait`, waits until the build has finished and the service
is ready or has failed, and returns the outcome, including why the build failed.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `app` | string | yes | The service to build again. |
| `wait` | boolean | no | Wait for the build and the rollout, and return the outcome (recommended). |
| `timeout_seconds` | integer | no | How long to wait. Default 300, at most 900. |

- **Calls:** `POST /apps/{app}/build`, preceded with `wait` by `GET /apps/{app}/status` to learn which build ran before,
  and followed by `GET /apps/{app}/status` every 3 seconds. A status that still shows the build that ran before is not
  taken for the outcome until 15 seconds have passed.
- **Returns without `wait`:** `{"app": "web", "building": true, "next": "call app_status to see how it is going"}`.
- **Returns with `wait`:** the same outcome as `deploy_app`: `app`, `status`, `ready`, and `failed` and `error` when the
  build or the rollout failed.
- **Errors:** a service that runs an image or a function answers `the app has no repository build`, and one that was
  never deployed `the app has not been built yet: its build is made when it is deployed`.

```json title="Example call"
{ "app": "web", "wait": true, "timeout_seconds": 600 }
```

### `rollback_app`

Returns a service to an earlier deployment.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `app` | string | yes | The service to roll back. |
| `deployment` | string | no | The deployment to return to. The one before the newest in `app_deployments` by default. |
| `wait` | boolean | no | Wait until it is ready and return the outcome. It waits up to 300 seconds. |

- **Calls:** `GET /apps/{app}/deployments` (without `deployment`), `POST /apps/{app}/rollback`, and with `wait`, status
  reads.
- **Returns without `wait`:** `{"app": "web", "rollingBackTo": "web-abc12"}`. **With `wait`:** the same outcome as
  `deploy_app`.
- It fails with `<app> has no earlier deployment to go back to` when there is only one deployment, and for a function
  (functions cannot be rolled back). A rollback records a deployment of its own, so a second call with no `deployment`
  goes to the entry before that one. Pass `deployment` to choose.

### `set_project_quota`

Changes a project's limits on what its pods may ask for. Name the limits to change. One you leave out keeps its value,
and one you give as an empty string is removed. A service that no longer fits stays unavailable until the limit is
raised, so read [`project_quota`](#project_quota) first. It takes effect within a moment.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `project` | string | yes | The project's ID. |
| `cpu`, `memory`, `storage`, `pods` | string or number | no | The most one environment's pods may ask for, such as `4`, `8Gi`, `50Gi` and `20`. A plain number is as good as a string. |
| `project_cpu`, `project_memory`, `project_storage`, `project_pods` | string or number | no | The most the whole project may ask for, added up over its environments. |

- **Needs:** **Full access** only: a project token cannot raise its own limit. **Calls:** `POST /projects/{project}/quota`.
- **Returns:** the limits as they are afterwards, as `project_quota` does. It is an error to name no limit.

### `scale_app`

Sets how many copies of a service run. It takes effect at once.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `app` | string | yes | The service to scale. |
| `replicas` | integer | yes | How many copies should run, 0 or more. The API refuses more than 1000. |

- **Calls:** `POST /apps/{app}/scale`.
- **Returns:** `{"app": "web", "replicas": 4}`.

### `set_sleep_when_idle`

Makes a service stop when it has had no requests for a while and start again on the next request, or keeps it
running. It needs the serverless add-on and a public host. The setting is saved at once, and the tool then waits up to 10
seconds for the cluster to say whether the service can sleep. See [Scale to zero](/docs/guides/scale-to-zero/).

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `app` | string | yes | The service to change. |
| `enabled` | boolean | yes | `true` to stop it when idle and start it on the next request, `false` to keep it running. |
| `idle_seconds` | integer | no | How long without requests before it stops, 30 to 86400. The API's default is 300. Anything else is refused with `idle_seconds must be between 30 and 86400`. |

- **Calls:** `PATCH /apps/{app}` with `{"scaleToZero": "on" | "off", "idleSeconds": ...}`, then, when `enabled` is
  true, `GET /apps/{app}/status` until `scaleToZero.active` is `true` or the wait ends. A service that shows no request
  to sleep yet is waited for too, since the read can lag the change.
- **Returns:** `{"app": "web", "sleepWhenIdle": true, "active": true}` when the service sleeps when idle. When it
  cannot, because the add-on is missing, the service has no public host or CPU autoscaling is on, the setting stays
  saved, the service stays running, and the result says so: `"active": false`, a `reason` (the cluster's message, or
  that it has not said yet) and a `note`. With `enabled` false: `{"app": "web", "sleepWhenIdle": false}`. When the
  setting was saved but reading the state back failed, the result is not an error: it has `sleepWhenIdle` and a `note`
  that says the check could not be made and why.

### `set_variable`

Sets a variable or a secret on a service. It applies when the service is deployed.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `app` | string | yes | The service. |
| `key` | string | yes | The variable's name, such as `LOG_LEVEL`. |
| `value` | string | yes | Its value. |
| `secret` | boolean | no | Store it as a secret. |
| `deploy` | boolean | no | Apply it now by deploying, and wait up to 300 seconds for the outcome. |

- **Calls:** `POST /apps/{app}/config/set`, or `/secrets/set` with `secret`; with `deploy`, `POST /apps/{app}/deploy`
  and status reads.
- **Returns without `deploy`:** `{"app": "web", "change": "set", "key": "LOG_LEVEL", "next": "call deploy_app to apply
  it", "pending": true}`. **With `deploy`:** `app`, `change`, `key` and `deploy`, which holds the outcome described
  under `deploy_app`.
- The result never repeats the value.

### `unset_variable`

Removes a variable or a secret from a service. It applies when the service is deployed.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `app` | string | yes | The service. |
| `key` | string | yes | The variable to remove. |
| `secret` | boolean | no | It is a secret. |
| `deploy` | boolean | no | Apply it now by deploying. |

- **Calls:** `POST /apps/{app}/config/delete`, or `/secrets/delete` with `secret`; with `deploy`, a deploy and status
  reads.
- **Returns:** like `set_variable`, with `"change": "removed"`. Removing a key that does not exist is not an error.

### `create_app`

Creates a service from an image or a GitHub repository. It is saved as a draft unless `deploy` is set.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `project` | string | yes | The project (its ID). |
| `environment` | string | yes | The environment, such as `production`. |
| `name` | string | yes | The service's name. |
| `image` | string | no | A container image to run. Give this or `repository`. |
| `repository` | string | no | A GitHub repository to build, `owner/name`. |
| `branch` | string | no | The branch to build. Default `main`. |
| `port` | integer | no | The port it serves on. |
| `deploy` | boolean | no | Deploy it now instead of saving a draft. |

- **Needs:** Full access. Hidden from project tokens. **Calls:** `POST /apps` with `draft`, or with `deploy` when the
  argument is set.
- **Returns:** `{"created": "web", "draft": true}`. With `deploy`: `{"created": "web", "draft": false, "deploying":
  true, "next": "call app_status to see how it is going"}`, and the service is deploying. The tool reports that only
  when the API says it deployed the service. A Geass older than the tool saves a draft instead, and the result then
  says `"draft": true` with a `note` that it did not deploy it: call `deploy_app`. With neither `image` nor
  `repository` the call fails with `give an image or a repository to run`. If you give both, `image` wins. A function
  cannot be created with this tool.
- With `deploy`, the deploy is checked before anything is saved: a cluster with no room for the service fails the
  call and leaves no draft behind. A repository also needs the project to be linked to GitHub first (see
  [GitHub and builds](/docs/guides/github-and-builds/)).

## Functions

These tools work on a service that runs a function (see [Functions](/docs/guides/functions/)).

### `set_function_trigger`

Calls a function on a schedule, adding the schedule or replacing the one with the same name. The schedule starts once
the function is next deployed.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `app` | string | yes | The function. |
| `name` | string | yes | The schedule's name, such as `nightly`: lowercase letters, numbers and dashes, up to 32 characters. |
| `schedule` | string | to add | A cron expression in UTC with five fields, or `@hourly`, `@daily`, `@weekly`, `@monthly`, `@yearly`. The call fails with `name and schedule are required` without it. |
| `path` | string | no | The path to call. Must start with `/`. Default `/`. |
| `payload` | string | no | The JSON body to send. Must be valid JSON. |

- **Calls:** `GET /apps/{app}`, then `PATCH /apps/{app}` with the full list of schedules. A function can have 8.
- **Returns:** `{"app": "report", "next": "call deploy_app to start the schedule", "pending": true, "trigger":
  "nightly"}`.

### `remove_function_trigger`

Stops calling a function on a schedule. It stops once the function is next deployed.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `app` | string | yes | The function. |
| `name` | string | yes | The schedule to remove. |

- **Calls:** `GET /apps/{app}`, then `PATCH /apps/{app}`.
- **Returns:** `{"app": "report", "pending": true, "removed": "nightly"}`. A name that does not exist fails with
  `<app> has no schedule named "<name>"` and changes nothing.

### `invoke_function`

Calls a function at its public address and returns its answer. This runs the function, which may change things, and
wakes it if it is asleep.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `app` | string | yes | The function to call. |
| `path` | string | no | The path to call, with a query if needed. Default `/`. |
| `method` | string | no | `GET`, or `POST` when there is a body (the default). |
| `body` | string | no | The JSON body to send. |

- **Calls:** `GET /apps/{app}`, then an HTTP request to the function's URL, without a Geass token. A call times out
  after 2 minutes, and at most 1 MiB of the answer is read.
- **Returns:** `{"status": 200, "body": "..."}`. A function that answers an error status is still a result.
- It fails with `the function has no public address to call: give it a domain first` for a function without a
  public address.

## Deleting

### `delete_resource`

Permanently deletes a service, database, bucket or logical database. This cannot be undone and deletes data.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `kind` | string | yes | `apps`, `databases`, `object-stores` or `logical-databases`. |
| `name` | string | yes | The resource's name. |
| `confirm` | string | yes | The resource's name again, to confirm that it is to be deleted for good. |

- **Needs:** Full access, and `geass mcp --allow-delete`. Without the flag the tool does not exist. Hidden from
  project tokens and from `--read-only`. **Calls:** `DELETE /{kind}/{name}?confirmName={name}`.
- **Returns:** `{"deleted": "orders"}`. It fails with `not deleted: confirm must be exactly "orders"` when `confirm` is
  not the name, and with `kind must be apps, databases, object-stores or logical-databases` for another kind.
- It is the only tool with `destructiveHint: true`, so a host that asks before destructive tools asks before this one.

```json title="Example call"
{ "kind": "databases", "name": "orders", "confirm": "orders" }
```

## Related

- [MCP server for AI agents](/docs/guides/mcp-server/)
- [CLI commands](/docs/reference/cli/)
- [Command line](/docs/guides/command-line/)
- [Users and API tokens](/docs/guides/users-and-tokens/)
- [REST API](/docs/reference/rest-api/)
