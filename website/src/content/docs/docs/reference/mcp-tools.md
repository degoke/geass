---
title: "MCP tools"
description: "Each tool that the geass MCP server offers: its arguments, what it returns, the token role it needs, the API route it calls, and an example call."
---

The `geass mcp` command runs a Model Context Protocol (MCP) server. The server gives an AI agent the same operations
as the [command line](/docs/reference/cli/). This page lists all tools that the server can register. To run the server
and connect an agent, read [MCP server for AI agents](/docs/guides/mcp-server/).

## What the server exposes

- **Tools only.** The server registers tools. It exposes no MCP resources and no prompts, and it advertises no
  completions. It does advertise the standard logging capability of the MCP Go SDK that it is built on.
- **Identity.** The name of the server is `geass`, and its title is `Geass`. Its version is the version of the
  command line, or `dev` when the build did not set one.
- **Instructions.** When an agent connects, the server sends it a short briefing. See [Instructions](#instructions).
- **Transports.** Standard input and output, or Streamable HTTP with `--listen`.
- **40 tools in all**, or 38 without the two that delete (`delete_resource` and `delete_node_pool`). The tools that an
  agent sees depend on its token. See [Which tools an agent sees](#which-tools-an-agent-sees).

Each tool takes a JSON object. The server rejects an argument that is not in the schema of the tool. The result of a
tool is one block of text. The text is indented JSON for all tools except `get_logs`, which returns plain lines. If a
result is longer than 60,000 characters, the server cuts it. The result then ends with
`... (cut: the result was too long; narrow it with the tool's options)`.

### Hints

Each tool has the standard MCP annotations. With these annotations, an agent host can decide when to ask you first.

- `read-only` means `readOnlyHint: true`.
- `idempotent` means `idempotentHint: true`.
- All tools except `delete_resource` and `delete_node_pool` have `destructiveHint: false`.
- All tools have `openWorldHint: false`.

### Errors

When Geass refuses or fails a call, the tool returns an error result (`isError: true`). The text of the result is the
message from the API. For three statuses, the tool adds a space and a note in brackets after the message:

| Status | Text added |
| --- | --- |
| `401` | `(the token is missing, wrong or expired)` |
| `403` | `(this token is not allowed to do that; do not retry)` |
| `404` | `(it does not exist, or is not visible to this token)` |

All other statuses give only the message of the API, for example `replicas must be a whole number from 0 to 1000`.
A deploy that fails while the agent waits for it is not an error. It is a result that tells why. See
[`deploy_app`](#deploy_app).

## Overview

| Tool | What it does | Needs | Hints |
| --- | --- | --- | --- |
| [`whoami`](#whoami) | Tells who the agent acts as and what it can do | Any token | read-only |
| [`list_projects`](#list_projects) | Lists projects | Any workspace token | read-only |
| [`list_apps`](#list_apps) | Lists services | Any token | read-only |
| [`get_app`](#get_app) | The settings and status of one service | Any token | read-only |
| [`app_status`](#app_status) | Whether a service is up, and what its last deploy did | Any token | read-only |
| [`app_deployments`](#app_deployments) | The deployment history of a service | Any token | read-only |
| [`app_metrics`](#app_metrics) | CPU, memory, network, and requests over time | Any token | read-only |
| [`get_logs`](#get_logs) | Recent log lines of a service | Read only, with logs | read-only |
| [`deployment_logs`](#deployment_logs) | Build and deploy logs of one deployment | Read only, with logs | read-only |
| [`list_variables`](#list_variables) | Names of the variables of a service | Read only, with logs | read-only |
| [`list_databases`](#list_databases) | Lists databases | Any workspace token | read-only |
| [`get_database`](#get_database) | The settings and status of one database | Any workspace token | read-only |
| [`database_connection`](#database_connection) | Host, port, database, and user | Full access | read-only |
| [`list_buckets`](#list_buckets) | Lists object storage buckets | Any workspace token | read-only |
| [`list_nodes`](#list_nodes) | Lists the nodes of the cluster and their tags | Any workspace token | read-only |
| [`project_compliance`](#project_compliance) | The required node tags of a project | Any workspace token | read-only |
| [`project_network`](#project_network) | Who can reach the services of a project | Any workspace token | read-only |
| [`project_quota`](#project_quota) | The limits of a project and how much of them it uses | Any workspace token | read-only |
| [`set_project_quota`](#set_project_quota) | Changes the limits of a project | Full access | idempotent |
| [`deploy_app`](#deploy_app) | Deploys the pending changes of a service | Full access or project token | |
| [`rebuild_app`](#rebuild_app) | Runs the build of a repository service again, and rolls it out | Full access or project token | |
| [`rollback_app`](#rollback_app) | Returns a service to an earlier deployment | Full access or project token | |
| [`scale_app`](#scale_app) | Sets the number of copies | Full access or project token | idempotent |
| [`set_sleep_when_idle`](#set_sleep_when_idle) | Makes a service sleep when idle, or stay running | Full access or project token | idempotent |
| [`set_variable`](#set_variable) | Sets a variable or secret | Full access or project token | idempotent |
| [`unset_variable`](#unset_variable) | Removes a variable or secret | Full access or project token | idempotent |
| [`set_function_trigger`](#set_function_trigger) | Adds or replaces the schedule of a function | Full access or project token | idempotent |
| [`remove_function_trigger`](#remove_function_trigger) | Removes the schedule of a function | Full access or project token | idempotent |
| [`invoke_function`](#invoke_function) | Calls a function at its public address | Full access or project token | |
| [`create_app`](#create_app) | Creates a service from an image or a repository | Full access | |
| [`compute_providers`](#compute_providers) | The clouds Geass can create machines at, and what each takes | Any workspace token | read-only |
| [`cloud_connections`](#cloud_connections) | The connections to clouds, and what depends on each | Any workspace token | read-only |
| [`node_pools`](#node_pools) | Lists the pools of nodes | Any workspace token | read-only |
| [`node_pool`](#node_pool) | One pool, with its nodes | Any workspace token | read-only |
| [`cloud_sizes`](#cloud_sizes) | The machine sizes a cloud offers in a region | Full access | read-only |
| [`create_node_pool`](#create_node_pool) | Makes a pool, which creates machines that cost money | Full access | |
| [`update_node_pool`](#update_node_pool) | Changes a pool | Full access | idempotent |
| [`resume_node_pool`](#resume_node_pool) | Ends the pause of a pool that kept failing | Full access | idempotent |
| [`delete_resource`](#delete_resource) | Permanently deletes a resource | Full access, and `--allow-delete` | destructive |
| [`delete_node_pool`](#delete_node_pool) | Permanently deletes a pool, its nodes, and their machines | Full access, and `--allow-delete` | destructive |

**Needs** uses the access names of the dashboard:

- **Full access** is the `admin` role.
- **Read only, with logs** is `observer`.
- **Read only** is `viewer`.
- A **project token** is limited to one environment of one project.

If a row says **Any token**, a project token can use the tool for services in its own environment.
**Any workspace token** means that a project token cannot use the tool. **Read only, with logs** also covers
**Full access** and a project token. See [Users and API tokens](/docs/guides/users-and-tokens/).

## Which tools an agent sees

The server does not register a tool that a token cannot use. Thus, the agent never sees that tool. The flags of
`geass mcp` limit the list more:

- `--read-only` removes all tools that change anything. For a **Full access** token, it keeps `database_connection`
  and `cloud_sizes`, because those tools only read.
- `--allow-delete` adds `delete_resource` and `delete_node_pool`.

| Token | Tools |
| --- | --- |
| **Read only** (`viewer`) | `whoami`, `list_projects`, `list_apps`, `get_app`, `app_status`, `app_deployments`, `app_metrics`, `list_databases`, `get_database`, `list_buckets`, `list_nodes`, `project_compliance`, `project_network`, `project_quota`, `compute_providers`, `cloud_connections`, `node_pools`, `node_pool` (18) |
| **Read only, with logs** (`observer`) | The 18 above, plus `get_logs`, `deployment_logs`, and `list_variables` (21) |
| **Full access** (`admin`) | All except the two that delete (38), and `delete_resource` and `delete_node_pool` with `--allow-delete` (40) |
| **Full access** with `--read-only` | The 21 of an `observer`, plus `database_connection` and `cloud_sizes` (23). |
| Project token | `whoami`, `list_apps`, `get_app`, `app_status`, `app_deployments`, `app_metrics`, `get_logs`, `deployment_logs`, `list_variables`, `deploy_app`, `rebuild_app`, `rollback_app`, `scale_app`, `set_sleep_when_idle`, `set_variable`, `unset_variable`, `set_function_trigger`, `remove_function_trigger`, `invoke_function` (19) |

A project token with `--read-only` gets the first nine tools in its list. The server hides tools only for
convenience. The API checks the token on each call. Thus, a tool that the agent sees can still fail with a `403` for
something that the token cannot reach. An example is `app_metrics` with `kind` set to `databases` for a project
token.

## Instructions

When an agent connects, the server tells the agent what it can do. The text starts with `Geass runs services,
databases and buckets on a cluster. Use list_apps and app_status to see what is running.` The text that follows
depends on the token:

- For a project token: `You act with a token limited to <project>/<environment>: you can only see and change services
  there.`
- For a token that cannot change anything, or with `--read-only`: `You can only read: nothing here can change
  anything.`
- Otherwise: `Changing a variable does not apply it: call deploy_app afterwards (or pass deploy=true). To deploy and
  know whether it worked, call deploy_app with wait=true: it returns the outcome, and says why if the build failed. To
  build a service from its repository again without a push, call rebuild_app (also with wait=true).`
- For a token that can change things and is not a project token, the server adds this text: `Node pools create machines at a cloud, and
  every machine costs money until it is removed: create_node_pool needs max_nodes, the most it can ever have, which caps what it
  can cost. Connections to clouds hold credentials and single servers are billed until removed, so people make those, and no tool
  here does.`

All versions end with `Secret values are never returned by any tool. Plain configuration is not secret and is returned
as it is, so keep anything sensitive in a secret.`

## Services

### `whoami`

Tells who the agent acts as and what it can do.

- **Arguments:** none.
- **Needs:** any token. **Calls:** nothing. It returns what the server got from `GET /me` when the server started.
- **Returns:** the identity.

| Field | Meaning |
| --- | --- |
| `username` | `token:<token name>`. |
| `role` | `admin`, `observer`, or `viewer`. A project token reports `admin`. |
| `scope` | `workspace` or `project`. |
| `project`, `environment` | The limits of the project token. Only for a project token. |
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
- **Returns:** `{"items": [...]}`. Each item is a project summary with these fields:
  - `kind` (`project`).
  - `name`, the ID of the project, for example `proj-1a2b3c4d`.
  - `displayName`, `state`, `message`, and `createdAt`.
  - `details`, with `environments`, and with `region` and `requiredTags` when they are set.

### `list_apps`

Lists services with their state: `ready`, `pending`, `failed`, `draft`, or `deleting`.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `project` | string | no | Only services in this project (its ID). |
| `environment` | string | no | Only services in this environment, for example `production`. |

- **Needs:** any token. A project token sees only its own environment. **Calls:** `GET /apps` with `project` and
  `environment`.
- **Returns:** `{"items": [...]}`. Each item has `kind` (`app`), `name`, `displayName`, `project`, `environment`,
  `state`, `message`, `url`, `pendingChanges`, `createdAt`, and `details`. `details` holds these fields:
  - `port`.
  - `source`: `image`, `git`, or `function`.
  - The `image`, or the `repository` and `branch`, or the `runtime`, `mode`, and `triggers`.
  - `replicas`.
  - `scaleToZero` (`idleSeconds`, `maxReplicas`, `active`, `message`), when the service sleeps when idle.

```json title="Example call"
{ "project": "proj-1a2b3c4d", "environment": "production" }
```

### `get_app`

Shows the settings and status of one service.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `app` | string | yes | The name of the service, as `list_apps` lists it. |

- **Needs:** any token. **Calls:** `GET /apps/{app}`.
- **Returns:** `{"resource": {...}, "spec": {...}, "status": {...}}`. `resource` is the same summary as in `list_apps`.
  `spec` holds the settings of the service. `status` holds what the operator last reported.

The result never includes secret values. `spec.secretData` lists the secret names with empty values. Plain
configuration is not secret. When the token is **Full access**, plain configuration appears in `spec.configData` and
`spec.env`. For a **Read only** or **Read only, with logs** token, the server removes these fields. The description of
the tool says the same.

### `app_status`

Shows whether a service is up and what its last deploy did. To learn whether a deploy worked, call this tool.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `app` | string | yes | The name of the service. |

- **Needs:** any token. **Calls:** `GET /apps/{app}/status`.
- **Returns:**

| Field | Meaning |
| --- | --- |
| `name`, `project`, `environment` | Which service. |
| `state` | `ready`, `pending`, `failed`, `draft`, or `deleting`. |
| `message` | Why the service is not ready, when the service gives a reason. |
| `url` | Its public address, when it has one. |
| `ready` | `true` when `state` is `ready`. |
| `pendingChanges` | How many changes wait for a deploy. |
| `replicas` | `desired`, `ready`, and `available` copies. Present after the service has a Deployment. |
| `sleeping` | `true` for a service that sleeps when idle and has stopped. It is waiting for a request, not down. |
| `scaleToZero` | For a service that asked to sleep when idle: `idleSeconds` and `maxReplicas`. Also `active` and `message`, after the operator has said whether the service sleeps, and why not. |
| `build` | The latest build: `name`, `phase` (`Pending`, `WaitingForCI`, `Running`, `Succeeded`, `Failed`, or `Cancelled`), `failureReason`, `sourceRevision`, `ciState`, `startedAt`, and `completedAt`. |
| `lastAttempt` | The latest deploy attempt: `id`, `phase` (`Running`, `Succeeded`, or `Failed`), `startedAt`, `completedAt`, `summary`, and `errors`. Each error has `recordedAt` and `message`. |

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

Lists the deployment history of a service, newest first. `rollback_app` and `deployment_logs` take these ids.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `app` | string | yes | The name of the service. |

- **Needs:** any token. **Calls:** `GET /apps/{app}/deployments`.
- **Returns:** `{"items": [...]}`. Each item has `id`, `phase`, `image`, `commit`, `trigger`, `actor`, `changeTitle`,
  `createdAt`, `startedAt`, and `completedAt`. A rollback adds its own entry.

### `app_metrics`

Shows CPU, memory, network, and request use over time for a service, a database, or a project.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `name` | string | yes | The service, database, or project to measure. |
| `kind` | string | no | `apps` (the default), `databases`, or `projects`. |
| `window` | string | no | How far back: `15m`, `1h`, `6h`, or `24h`. Default `1h`: if you do not give a value, the tool sends `1h`. A raw call to the API with no `window` answers for `15m`. |

- **Needs:** any token for `apps`. A project token can use `apps` for its own services. Any workspace token for
  `databases` and `projects`. **Calls:** `GET /{kind}/{name}/metrics` with `window`.
- **Returns:** `window`, `step` (seconds between points), `available`, an optional `message`, and `series`. Each series
  is a list of `{"t": <unix seconds>, "v": <value>}`. These are the series:
  - `cpu`: cores.
  - `memory`: bytes, the total of all replicas.
  - `memoryPerReplica`: bytes, for services only. The value is for the busiest replica.
  - `network`: bytes sent per second.
  - `networkIn`: bytes received per second.
  - `requests`: requests per second, for services only.
  - `storage`: bytes, for databases only.

  If Prometheus is not installed or not reachable, `available` is `false`, with a `message`. See
  [Observability](/docs/guides/observability/).

```json title="Example call"
{ "name": "web", "window": "1h" }
```

## Logs and variables

These three tools need a token that can read more than a **Read only** token. That is **Read only, with logs**,
**Full access**, or a project token.

### `get_logs`

Reads the recent log lines of a service. To get fewer lines, use `level` or `search`.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `app` | string | yes | The name of the service. |
| `tail` | integer | no | How many of the newest lines to return in total. Default 100, maximum 500. `level` and `search` look through only the most recent lines. To look further back, increase `tail`. |
| `since` | string | no | Only lines newer than this time ago, for example `15m` or `2h`. Go duration syntax, up to 7 days. |
| `level` | string | no | Only this level: `error`, `warn`, `info`, or `debug`. |
| `search` | string | no | Only lines that contain this text, up to 200 characters. |

- **Needs:** Read only, with logs, Full access, or a project token (for its own services). **Calls:**
  `GET /apps/{app}/logs` with `tail`, `since`, `q`, and `level`.
- **Returns:** plain text, one line for each log line, oldest first, as `<time in RFC 3339> <pod> <text>`. When
  nothing matches: `No log lines matched.`
- **Filters.** The dashboard reads `tail` lines from the end of the log of each copy. It applies `level` and `search`
  to those lines. Thus, `level` `error` finds the errors among the newest lines of each copy. `level` `warn` also
  returns errors. A service with several copies returns the newest `tail` lines of all copies together.

```text title="Example result"
2026-10-12T09:41:07Z web-6d9c7b8f5-x2k4p Error: connect ECONNREFUSED 203.0.113.10:5432
```

### `deployment_logs`

Returns the build and deploy logs of one deployment. When a deploy fails, read these logs.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `app` | string | yes | The name of the service. |
| `deployment` | string | yes | A deployment id from `app_deployments`. |

- **Needs:** Read only, with logs, Full access, or a project token. **Calls:**
  `GET /apps/{app}/deployments/{deployment}/logs`.
- **Returns:** `id` and `sections`. `sections` is a list of `{"id", "title", "lines"}` for the deploy attempt, the
  build (for a repository), and the rollout. The tool leaves out empty sections. `lines` is the log of the stage as
  one block of text, with each log line on its own line.
- If a stage is longer than 200 lines, the tool keeps only its last 200 lines. A failure tells its reason in those
  lines. The stage then starts with `... (N earlier lines left out)`. The whole log is at
  `GET /apps/{app}/deployments/{deployment}/logs`.

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

Lists the names of the variables and secrets of a service.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `app` | string | yes | The name of the service. |

- **Needs:** Read only, with logs, Full access, or a project token. **Calls:** `GET /apps/{app}/variables`.
- **Returns:** `secrets`, `env`, and `config`:
  - `secrets` is a list of names.
  - `env` is a list of `{"name", "kind"}`, where `kind` is `literal` or `secret`.
  - `config` is an object of plain configuration.

The tool never returns secret values. Secrets come back only by name. `config` is an object of plain configuration
with its values, because plain configuration is not secret. The description of the tool says so. Store all sensitive
data as a secret.

```json title="Example result"
{
  "secrets": ["STRIPE_KEY"],
  "env": [],
  "config": { "LOG_LEVEL": "info" }
}
```

## Databases, buckets and nodes

The server hides all of these tools from project tokens.

### `list_databases`

Lists databases with their state.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `project` | string | no | Only databases in this project. |
| `environment` | string | no | Only databases in this environment. |

- **Needs:** any workspace token. **Calls:** `GET /databases`.
- **Returns:** `{"items": [...]}` of summaries like `list_apps`. `details` holds `engine` and `highAvailability`. It
  also holds `placement` and `version` when they are set.

### `get_database`

Shows the settings and status of one database.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `name` | string | yes | The name of the database. |

- **Needs:** any workspace token. **Calls:** `GET /databases/{name}`.
- **Returns:** `{"resource": {...}, "spec": {...}, "status": {...}}`. For a token that cannot change things
  (**Read only** or **Read only, with logs**), the server removes the engine, host, and connection details.

### `database_connection`

Shows how to reach a database: host, port, database, and user. The tool never returns the password or the
connection string.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `name` | string | yes | The name of the database. |

- **Needs:** Full access. The server also offers this tool with `--read-only`, because it only reads. **Calls:**
  `GET /databases/{name}/connection`, never with `reveal`.
- **Returns:** the connection facts that exist, from `host`, `port`, `database`, and `username`.

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
- **Returns:** `{"items": [...]}` of summaries. A token that cannot change things does not get the bucket names or
  the endpoint.

### `list_nodes`

Lists the nodes of the cluster with their region, zone, and tags, including compliance tags.

- **Arguments:** none.
- **Needs:** any workspace token. **Calls:** `GET /nodes`.
- **Returns:** `{"items": [...]}`. Each node has `name`, `ready`, `schedulable`, `controlPlane`, `region`, `zone`,
  `tags`, `cpu`, and `memory`. Each tag has `name`, and also `reserved` and `compliance` when they are true.

### `project_compliance`

Shows the node tags that a project requires, for example `hipaa`, and where the project does not meet them.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `project` | string | yes | The ID of the project. |

- **Needs:** any workspace token. **Calls:** `GET /projects/{project}/compliance`.
- **Returns:** `requiredTags`, `region`, `databases`, and `openNodes`. Each database has `name`, `compliant`, and
  `missing`. `openNodes` are the nodes that have a required tag but are not reserved for it. See
  [Compliance tags](/docs/guides/compliance-tags/).

### `project_network`

Shows who can reach the services of a project:

- Only the project itself (with the platform and public domains), or every service in the cluster.
- The other projects that are let in, if any.
- Whether the services are kept away from the machines that the cluster runs on.

This tool only reads. No tool changes this setting. To open a project, or to let it reach the machines, is a decision
for a person.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `project` | string | yes | The ID of the project. |

- **Needs:** any workspace token. **Calls:** `GET /projects/{project}/network`.
- **Returns:** `isolation` (`Project` or `None`), `allowFromProjects`, and `blockNodeAddresses`. See
  [Network isolation](/docs/guides/network-isolation/).

### `project_quota`

Shows the limits of a project, for each environment and for the whole project. It also shows what all its
environments ask for together. A project with no limits can ask for all that the cluster has.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `project` | string | yes | The ID of the project. |

- **Needs:** any workspace token. **Calls:** `GET /projects/{project}/quota`.
- **Returns:** `environment`, `project`, and `used`. Each has `cpu`, `memory`, `storage`, and `pods` where they are set.
  See [Project limits](/docs/guides/project-limits/).

## Deploying and changing services

These tools need **Full access** or a project token. `set_project_quota` needs **Full access**. With `--read-only`,
the server does not offer these tools.

### `deploy_app`

Deploys the pending changes of a service. With `wait`, the tool waits until the service is ready or has failed. It
then returns the outcome, including why a build failed.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `app` | string | yes | The service to deploy. |
| `wait` | boolean | no | Wait until it is ready or has failed, and return the outcome (recommended). |
| `timeout_seconds` | integer | no | How long to wait. Default 300, maximum 900. |

- **Calls:** `POST /apps/{app}/deploy`, and with `wait`, `GET /apps/{app}/status` every 3 seconds.
- **Returns without `wait`:** `{"app": "web", "deploying": true, "next": "call app_status to see how it is going"}`.
- **Returns with `wait`:** the outcome.

| Field | Meaning |
| --- | --- |
| `app` | The service. |
| `status` | The last status read, as returned by `app_status`. |
| `ready` | `true` only when the service is ready and has no pending changes, and its latest build succeeded. A service with no build needs only the first two. |
| `failed` | `true` when the deploy failed. Only present then. |
| `error` | Why it failed (`the deploy failed: <reason>`), or why the wait ended (`gave up waiting for web after 5m0s (it is pending): context deadline exceeded`). Absent when it succeeded. |

A failed deploy and a wait that ran out are results, not errors. Thus, the agent reads the reason. If the client of
the agent sends a progress token with the call, the server reports progress while it waits. The progress comes as
lines, for example `web: pending, build
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

Runs the build of a repository service again, and rolls the service out onto the new image. The build starts from
the head of its branch, or from the commit that the service pins. This is what a push does.

The tool does not apply changes that were saved to the service but not yet deployed. For those changes, call
`deploy_app`. With `wait`, the tool waits until the build has finished and the service is ready or has failed. It then
returns the outcome, including why the build failed.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `app` | string | yes | The service to build again. |
| `wait` | boolean | no | Wait for the build and the rollout, and return the outcome (recommended). |
| `timeout_seconds` | integer | no | How long to wait. Default 300, maximum 900. |

- **Calls:** `POST /apps/{app}/build`. With `wait`, the tool first calls `GET /apps/{app}/status` to learn which build
  ran before. After the build call, it calls `GET /apps/{app}/status` every 3 seconds. For the first 15 seconds, the
  tool does not take a status that still shows the earlier build as the outcome.
- **Returns without `wait`:** `{"app": "web", "building": true, "next": "call app_status to see how it is going"}`.
- **Returns with `wait`:** the same outcome as `deploy_app`: `app`, `status`, and `ready`. When the build or the
  rollout failed, it also returns `failed` and `error`.
- **Errors:** a service that runs an image or a function answers `the app has no repository build`. A service that
  was never deployed answers `the app has not been built yet: its build is made when it is deployed`.

```json title="Example call"
{ "app": "web", "wait": true, "timeout_seconds": 600 }
```

### `rollback_app`

Returns a service to an earlier deployment.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `app` | string | yes | The service to roll back. |
| `deployment` | string | no | The deployment to return to. The default is the one before the newest in `app_deployments`. |
| `wait` | boolean | no | Wait until the service is ready, and return the outcome. The tool waits up to 300 seconds. |

- **Calls:** `GET /apps/{app}/deployments` (without `deployment`), `POST /apps/{app}/rollback`, and, with `wait`,
  status reads.
- **Returns without `wait`:** `{"app": "web", "rollingBackTo": "web-abc12"}`. **With `wait`:** the same outcome as
  `deploy_app`.
- It fails with `<app> has no earlier deployment to go back to` when there is only one deployment. It also fails for
  a function, because you cannot roll back a function.
- A rollback records its own deployment. Thus, a second call with no `deployment` goes to the entry before that one.
  To select the deployment, pass `deployment`.

### `set_project_quota`

Changes the limits of a project on what its pods can ask for. Name the limits to change:

- A limit that you do not name keeps its value.
- A limit that you give as an empty string is removed.

A service that no longer fits stays unavailable until someone raises the limit. Thus, read
[`project_quota`](#project_quota) first. The change takes effect in a moment.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `project` | string | yes | The ID of the project. |
| `cpu`, `memory`, `storage`, `pods` | string or number | no | The maximum that the pods of one environment can ask for, for example `4`, `8Gi`, `50Gi`, and `20`. A plain number works the same as a string. |
| `project_cpu`, `project_memory`, `project_storage`, `project_pods` | string or number | no | The maximum that the whole project can ask for, as the total of its environments. |

- **Needs:** **Full access** only. A project token cannot raise its own limit. **Calls:**
  `POST /projects/{project}/quota`.
- **Returns:** the limits after the change, as `project_quota` does. If you name no limit, the call is an error.

### `scale_app`

Sets how many copies of a service run. The change takes effect immediately.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `app` | string | yes | The service to scale. |
| `replicas` | integer | yes | How many copies to run, 0 or more. The API refuses more than 1000. |

- **Calls:** `POST /apps/{app}/scale`.
- **Returns:** `{"app": "web", "replicas": 4}`.

### `set_sleep_when_idle`

Makes a service stop when it has had no requests for some time, and start again on the next request. Or the tool
keeps the service running. The feature needs the serverless add-on and a public host. The tool saves the setting
immediately. The tool then waits up to 10 seconds for the cluster to say whether the service can sleep. See
[Scale to zero](/docs/guides/scale-to-zero/).

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `app` | string | yes | The service to change. |
| `enabled` | boolean | yes | `true` to stop the service when idle and start it on the next request. `false` to keep it running. |
| `idle_seconds` | integer | no | How long without requests before the service stops, 30 to 86400. The default of the API is 300. The API refuses other values with `idle_seconds must be between 30 and 86400`. |

- **Calls:** `PATCH /apps/{app}` with `{"scaleToZero": "on" | "off", "idleSeconds": ...}`. When `enabled` is true,
  the tool then calls `GET /apps/{app}/status` until `scaleToZero.active` is `true` or the wait ends. The tool also
  waits when the service shows no request to sleep yet, because the read can be later than the change.
- **Returns:** `{"app": "web", "sleepWhenIdle": true, "active": true}` when the service sleeps when idle.
- Sometimes the service cannot sleep: the add-on is missing, the service has no public host, or CPU autoscaling is
  on. Then the setting stays saved, and the service stays running. The result says so with `"active": false`, a
  `reason`, and a `note`. The `reason` is the message of the cluster, or says that the cluster has not said yet.
- With `enabled` false: `{"app": "web", "sleepWhenIdle": false}`.
- If the tool saved the setting but could not read the state back, the result is not an error. It has
  `sleepWhenIdle` and a `note` that says that the check could not run, and why.

### `set_variable`

Sets a variable or a secret on a service. The change applies when the service is deployed.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `app` | string | yes | The service. |
| `key` | string | yes | The name of the variable, for example `LOG_LEVEL`. |
| `value` | string | yes | Its value. |
| `secret` | boolean | no | Store it as a secret. |
| `deploy` | boolean | no | Deploy to apply it now, and wait up to 300 seconds for the outcome. |

- **Calls:** `POST /apps/{app}/config/set`, or `/secrets/set` with `secret`. With `deploy`, it also calls
  `POST /apps/{app}/deploy` and does status reads.
- **Returns without `deploy`:** `{"app": "web", "change": "set", "key": "LOG_LEVEL", "next": "call deploy_app to apply
  it", "pending": true}`. **With `deploy`:** `app`, `change`, `key`, and `deploy`. `deploy` holds the outcome that
  `deploy_app` describes.
- The result never repeats the value.

### `unset_variable`

Removes a variable or a secret from a service. The change applies when the service is deployed.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `app` | string | yes | The service. |
| `key` | string | yes | The variable to remove. |
| `secret` | boolean | no | It is a secret. |
| `deploy` | boolean | no | Deploy to apply it now. |

- **Calls:** `POST /apps/{app}/config/delete`, or `/secrets/delete` with `secret`. With `deploy`, it also deploys and
  does status reads.
- **Returns:** like `set_variable`, with `"change": "removed"`. If you remove a key that does not exist, the call is
  not an error.

### `create_app`

Creates a service from an image or a GitHub repository. The tool saves the service as a draft, unless `deploy` is
set.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `project` | string | yes | The project (its ID). |
| `environment` | string | yes | The environment, for example `production`. |
| `name` | string | yes | The name of the service. |
| `image` | string | no | A container image to run. Give this or `repository`. |
| `repository` | string | no | A GitHub repository to build, `owner/name`. |
| `branch` | string | no | The branch to build. Default `main`. |
| `port` | integer | no | The port that the service serves on. |
| `deploy` | boolean | no | Deploy the service now, and do not save a draft. |

- **Needs:** Full access. Hidden from project tokens. **Calls:** `POST /apps` with `draft`, or with `deploy` when the
  argument is set.
- **Returns:** `{"created": "web", "draft": true}`. With `deploy`: `{"created": "web", "draft": false, "deploying":
  true, "next": "call app_status to see how it is going"}`, and the service is deploying. The tool reports this only
  when the API says that it deployed the service.
- A Geass that is older than the tool saves a draft. The result then says `"draft": true`, with a `note` that the tool
  did not deploy the service. In that case, call `deploy_app`.
- With neither `image` nor `repository`, the call fails with `give an image or a repository to run`. If you give both,
  `image` has priority. You cannot create a function with this tool.
- With `deploy`, Geass checks the deploy before it saves anything. If the cluster has no room for the service, the
  call fails and leaves no draft. A repository also needs the project to be linked to GitHub first. See
  [GitHub and builds](/docs/guides/github-and-builds/).

## Functions

These tools work on a service that runs a function. See [Functions](/docs/guides/functions/).

### `set_function_trigger`

Calls a function on a schedule. The tool adds the schedule, or replaces the schedule with the same name. The
schedule starts after the next deploy of the function.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `app` | string | yes | The function. |
| `name` | string | yes | The name of the schedule, for example `nightly`: lowercase letters, numbers, and dashes, up to 32 characters. |
| `schedule` | string | to add | A cron expression in UTC with five fields, or `@hourly`, `@daily`, `@weekly`, `@monthly`, or `@yearly`. Without it, the call fails with `name and schedule are required`. |
| `path` | string | no | The path to call. Must start with `/`. Default `/`. |
| `payload` | string | no | The JSON body to send. Must be valid JSON. |

- **Calls:** `GET /apps/{app}`, then `PATCH /apps/{app}` with the full list of schedules. A function can have a
  maximum of 8 schedules.
- **Returns:** `{"app": "report", "next": "call deploy_app to start the schedule", "pending": true, "trigger":
  "nightly"}`.

### `remove_function_trigger`

Stops the calls to a function on a schedule. The calls stop after the next deploy of the function.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `app` | string | yes | The function. |
| `name` | string | yes | The schedule to remove. |

- **Calls:** `GET /apps/{app}`, then `PATCH /apps/{app}`.
- **Returns:** `{"app": "report", "pending": true, "removed": "nightly"}`. If the name does not exist, the call fails
  with `<app> has no schedule named "<name>"` and changes nothing.

### `invoke_function`

Calls a function at its public address and returns its answer. This call runs the function, which might change
things. If the function is asleep, the call wakes it.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `app` | string | yes | The function to call. |
| `path` | string | no | The path to call, with a query if necessary. Default `/`. |
| `method` | string | no | `GET`, or `POST` when there is a body. `POST` is the default. |
| `body` | string | no | The JSON body to send. |

- **Calls:** `GET /apps/{app}`, then an HTTP request to the URL of the function, without a Geass token. A call times
  out after 2 minutes. The tool reads a maximum of 1 MiB of the answer.
- **Returns:** `{"status": 200, "body": "..."}`. If a function answers with an error status, the call still gives a
  result.
- For a function without a public address, the call fails with
  `the function has no public address to call: give it a domain first`.

## Node pools and clouds

These tools show an agent the clouds that Geass uses. With these tools, an agent can also run
[pools of nodes](/docs/guides/node-pools/). The nodes of a pool are machines that Geass creates at a cloud when the
load needs them. **A pool creates machines that cost money**. The cloud bills them to the account of the connection
until they are removed. The server hides all of these tools from project tokens.

No tool does these actions:

- Take a credential.
- Make, change, verify, or remove a connection.
- Add or remove a single server.

A connection holds the credentials of a billing account. A single server costs money until someone removes it. Thus,
those two decisions stay with a person, in the dashboard or with `geass connections` and `geass servers`. This is like
`project_network`, which an agent can read but not change.

An agent cannot get a secret that it could leak, and it cannot select which of your accounts to spend. An agent can
look at what exists. It can also run a pool that the connection of a person already pays for, within a maximum that
is set in the pool itself. It cannot make a pool of servers that you own. The tools that read return no credential,
only the names of the credentials that a cloud takes.

### `compute_providers`

Lists the clouds that Geass can create machines at. For each cloud, it lists the credentials that the cloud takes,
and the settings that a pool can give the cloud. For each credential, it gives the name and whether it is secret, but
never a value.

- **Arguments:** none. **Needs:** any workspace token. **Calls:** `GET /compute/providers`.
- **Returns:** `{"items": [...]}`. Each cloud has `id`, `label`, `summary`, `fields`, `settings`, `freeSettings`, and
  `regionHelp`. Each field has `key`, `label`, and `help`. A field also has `secret`, `optional`, or `multiline` when
  that value is true.

### `cloud_connections`

Lists the connections to clouds. For each connection, it shows these items:

- The cloud that it is for.
- What it is for. `Compute` makes machines.
- Whether the cloud accepts its credentials.
- The pools and machines that depend on it.

- **Arguments:** none. **Needs:** any workspace token. **Calls:** `GET /connections`.
- **Returns:** `{"items": [...]}`. Each item has `name`, `provider`, `purposes`, `project`, `region`, `available`,
  `account`, `message`, `warnings`, `verifiedAt`, `expiresAt`, `usedBy`, and `machines`. The result never has a
  credential.

### `node_pools`

Lists the pools. For each pool, it shows these items:

- Its connection and size.
- The minimum and maximum number of nodes.
- How many nodes it has, and how many are ready.
- Whether it autoscales.
- Its state, and its cost for one hour.

- **Arguments:** none. **Needs:** any workspace token. **Calls:** `GET /pools`.
- **Returns:** `{"items": [...]}` of pools without their nodes.

### `node_pool`

Shows one pool, with these items:

- Its settings, its phase, and its message.
- The pods that wait for room.
- Its cost.
- Whether it is paused, and why.
- The machines at the cloud that no node owns.
- Each of its nodes.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `name` | string | yes | The name of the pool, as `node_pools` lists it. |

- **Needs:** any workspace token. **Calls:** `GET /pools/{name}`.

### `cloud_sizes`

Lists the machine sizes that a cloud offers in a region, cheapest first. Each size has processors, memory, and the
price for one hour at the list price of the cloud. `create_node_pool` takes these names as `size`. The tool asks the
cloud with the credentials of a connection, and changes nothing.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `connection` | string | yes | The connection to ask with, as `cloud_connections` lists it. |
| `region` | string | no | The region or location of the cloud. Default: the region of the connection. |
| `min_vcpus` | integer | no | Only sizes with at least this many processors. |
| `min_memory_mib` | integer | no | Only sizes with at least this much memory, in MiB (`8192` is 8 GiB). |
| `limit` | integer | no | How many to return, cheapest first. Default 30, maximum 200. |

- **Needs:** Full access. The server also offers this tool with `--read-only`, because it only reads. **Calls:**
  `GET /compute/sizes`.
- **Returns:** `{"items": [...], "matched": 42, "note": "..."}`. If the tool returned only the cheapest sizes, the
  note says so, and tells how to make the list shorter.

### `create_node_pool`

Makes a pool. When pods cannot start because there is no room, Geass creates machines at the cloud of a connection.
Geass removes the machines when they are idle. **It creates machines that cost money.** Geass makes `min_nodes`
machines immediately.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `name` | string | yes | Lowercase letters, numbers, and hyphens, maximum 48 characters. |
| `connection` | string | yes | A connection for `Compute`, from `cloud_connections`. |
| `size` | string | yes | The name of a size at the cloud, from `cloud_sizes`. |
| `max_nodes` | integer | yes | The maximum number of nodes that the pool can have, 1 to 10. It limits the cost of the pool. Select it carefully, and ask the person if you are not sure. A person makes a larger pool, up to 200, in the dashboard or with `geass pools`. |
| `min_nodes` | integer | no | The minimum number of nodes, 0 to 3, default 0. A pool that does not autoscale has exactly this number. |
| `autoscaling` | boolean | when `max_nodes` is above `min_nodes` | `true` follows the load between the two values. `false` keeps exactly `min_nodes`. The tool never assumes a value. |
| `region` | string | no | The region or location of the cloud. |
| `fallback_sizes`, `fallback_regions` | list of strings | no | Geass tries these in sequence when the cloud has no room. A fallback might cost more. |
| `image` | string | no | The operating system image. |
| `spot` | boolean | no | Use spot machines, which the cloud can take back. |
| `storage` | string | no | `none` (the default) or `allow`. |
| `settings` | object | no | Settings of the cloud, for example `{"firewallId": "42"}`. `compute_providers` lists the keys. |
| `scale_down_after_minutes` | integer | no | Minutes that a node must be a candidate for removal before Geass removes it. Default 10. |
| `spare_nodes` | integer | no | Empty nodes that Geass keeps ready for a burst, 0 to 3. Each one costs money while it waits. |
| `placement_region`, `placement_zone` | string | no | The region and site that the nodes have. |
| `tags`, `reserved_tags` | list of strings | no | Node tags that the nodes have. |

- **Needs:** Full access (a workspace admin). **Calls:** `POST /pools`, then `GET /pools/{name}`.
- **Returns:** `created`, `connection`, `minNodes`, `maxNodes`, `note`, `next` (`call node_pool to see how it is going`),
  and the `pool`. The `note` says that the machines cost money until they are removed, and that the pool never has
  more than `max_nodes`.
- Without `max_nodes`, the call fails with `max_nodes is required and at least 1 ...`.
- If `autoscaling` is not given and the pool could grow, the call fails with
  `max_nodes N is above min_nodes M, so say whether the pool follows the load ...`.
- An agent can set a maximum of 10 for `max_nodes`, 3 for `min_nodes`, and 3 for `spare_nodes`. A larger number
  fails with a message that says to ask a person. A person can set up to 200 in the dashboard or with `geass pools`.

```json title="Example call"
{ "name": "burst", "connection": "hetzner", "size": "cx23", "region": "fsn1", "min_nodes": 0, "max_nodes": 5, "autoscaling": true, "fallback_sizes": ["cx33"] }
```

### `update_node_pool`

Changes a pool. Name only what to change.

- If you raise `max_nodes`, `min_nodes`, or `spare_nodes`, or select a larger size, the pool can cost more.
- `min_nodes` and `spare_nodes` make machines immediately.
- `size` applies only to nodes that Geass makes after the change.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `name` | string | yes | The pool to change. |
| `min_nodes`, `max_nodes` | integer | no | The minimum and the maximum number of nodes (`min_nodes` 0 to 3, `max_nodes` 1 to 10). |
| `autoscaling` | boolean | no | `true` follows the load. `false` keeps exactly `min_nodes`. |
| `suspend` | boolean | no | `true` stops the pool from adding and removing nodes. The nodes that it has stay. `false` lets the pool add and remove nodes again. |
| `size` | string | no | The size of nodes that Geass makes from now on. |
| `spare_nodes` | integer | no | Empty nodes that Geass keeps ready, 0 to 3. |

- **Needs:** Full access. **Calls:** `PATCH /pools/{name}`, then `GET /pools/{name}`. **Returns:** `updated` and the
  `pool` after the change.
- If you name nothing, the call fails with `name at least one thing to change ...`.

### `resume_node_pool`

Ends the pause of a pool. A pool pauses, and stops adding nodes, when three machines in a row fail to become nodes.
After this tool, the pool forgets the failures and tries again. This makes machines and costs money. Thus, read
`node_pool` first. Its last failure tells what went wrong. If you do not fix the cause, the pool fails again.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `name` | string | yes | The name of the pool. |

- **Needs:** Full access. **Calls:** `POST /pools/{name}/resume`. **Returns:**
  `{"resumed": "burst", "next": "call node_pool to see whether it recovers"}`.

### `delete_node_pool`

Permanently deletes a pool, its nodes, and their machines at the cloud. The workloads on those nodes move if other
nodes have room. If not, they stop. To keep the machines but stop the pool from adding and removing them, use
`update_node_pool` with `suspend` set to `true`.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `name` | string | yes | The pool to delete. |
| `confirm` | string | yes | The name of the pool again, to confirm. |

- **Needs:** Full access, and `geass mcp --allow-delete`. Without the flag, the tool does not exist. Hidden from
  project tokens and from `--read-only`. **Calls:** `DELETE /pools/{name}?confirmName={name}`.
- **Returns:** `{"deleted": "burst", "note": "its machines are being deleted at the cloud"}`. If `confirm` is not the
  name, the call fails with `not deleted: confirm must be exactly "burst"`.
- It has `destructiveHint: true`, like `delete_resource`.

## Deleting

### `delete_resource`

Permanently deletes a service, database, bucket, or logical database. You cannot undo this action, and it deletes
data.

| Argument | Type | Required | Meaning |
| --- | --- | --- | --- |
| `kind` | string | yes | `apps`, `databases`, `object-stores`, or `logical-databases`. |
| `name` | string | yes | The name of the resource. |
| `confirm` | string | yes | The name of the resource again, to confirm that you want to delete it permanently. |

- **Needs:** Full access, and `geass mcp --allow-delete`. Without the flag, the tool does not exist. Hidden from
  project tokens and from `--read-only`. **Calls:** `DELETE /{kind}/{name}?confirmName={name}`.
- **Returns:** `{"deleted": "orders"}`. If `confirm` is not the name, the call fails with
  `not deleted: confirm must be exactly "orders"`. For a different kind, the call fails with
  `kind must be apps, databases, object-stores or logical-databases`.
- This tool and [`delete_node_pool`](#delete_node_pool) are the only tools with `destructiveHint: true`. Thus, a host
  that asks before destructive tools asks before these two tools.

```json title="Example call"
{ "kind": "databases", "name": "orders", "confirm": "orders" }
```

## Related

- [MCP server for AI agents](/docs/guides/mcp-server/)
- [CLI commands](/docs/reference/cli/)
- [Command line](/docs/guides/command-line/)
- [Users and API tokens](/docs/guides/users-and-tokens/)
- [REST API](/docs/reference/rest-api/)
