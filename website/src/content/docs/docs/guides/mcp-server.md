---
title: "MCP server for AI agents"
description: "Run geass mcp to let an AI agent inspect, deploy, scale and configure services on Geass, limited by the role of the token it acts as."
---

The Model Context Protocol (MCP) is how AI agents such as Claude Code or Claude Desktop use tools. `geass mcp` is an MCP
server built into the [`geass` command line](/docs/guides/command-line/). It offers an agent the same operations as the
command line: see what runs, read status and metrics, deploy and wait for the result, scale, roll back, change
variables, schedule and call functions. The agent acts as an API token, and it is only shown the tools that token may
use. A read-only token cannot even see a tool that deploys.

There is nothing to install besides the command line, and no separate service to keep running when the agent starts the
server itself. Every tool is listed in [MCP tools](/docs/reference/mcp-tools/).

## Before you start

- The command line installed and signed in (see [Command line](/docs/guides/command-line/)), or an API token and the
  address of your dashboard.
- A decision about what the agent may do. Read [Choose a token for the agent](#choose-a-token-for-the-agent) first. The
  token is the safety boundary, and the flags of `geass mcp` only narrow it further.

## Choose a token for the agent

Give an agent its own token, not yours. Create it under **Settings** → **Tokens** (see
[Users and API tokens](/docs/guides/users-and-tokens/)), name it after the agent, and give it the shortest expiry you can
live with. Then you can revoke it without touching anything else, and **Last used** tells you whether the agent still uses
it.

| You want the agent to | Token | Tools it sees |
| --- | --- | --- |
| Look at what runs, read status, metrics and the list of resources | **Read only** | 12 |
| Also read logs and the names of variables | **Read only, with logs** | 15 |
| Deploy, scale, change variables and schedules in one environment | A project token for that environment | 18 |
| Do all of that across every project, create services, read database connection details | **Full access** | 26 (27 with `--allow-delete`) |

**Read only, with logs** is the right choice for an agent that only watches. A project token is the right choice for an
agent that deploys: it can reach one environment of one project and nothing else. **Full access** lets the agent do
everything the dashboard can do that the tools cover, so reserve it for agents you supervise.

## Run the server

### Over standard input and output

This is the mode an agent host uses. The host starts `geass mcp` as a child process and talks to it through its
standard input and output.

```bash
geass login --url https://geass.example.com
geass mcp
```

You do not run this by hand in a terminal: the server waits for MCP messages on standard input. It uses the same login
as the other commands: the saved login, or `--url` and `--token`, or `GEASS_URL` and `GEASS_TOKEN`. It checks the token
with Geass before it starts and tells you what it offers on standard error, because standard output carries the
protocol:

```text
geass MCP server ready with 29 tools: whoami, list_projects, list_apps, get_app, app_status, app_deployments, list_databases, get_database, database_connection, list_buckets, list_nodes, project_compliance, project_network, project_quota, set_project_quota, app_metrics, get_logs, list_variables, deployment_logs, deploy_app, scale_app, set_sleep_when_idle, set_function_trigger, remove_function_trigger, invoke_function, rollback_app, set_variable, unset_variable, create_app
```

The tools are decided once, when the server starts. To give the agent a different role, start a new server with
another token.

### Over HTTP

With `--listen`, one server serves many agents over Streamable HTTP, and each agent sends its own Geass token. The server
keeps no credentials of its own. It needs only the address of your Geass dashboard.

```bash
geass mcp --listen 127.0.0.1:8765 --url https://geass.example.com
```

```text
geass MCP server listening on http://127.0.0.1:8765 (agents send their Geass token as a Bearer token)
```

The agent connects to that address, on any path, and sends `Authorization: Bearer <Geass token>` with every request. Write
the word `Bearer` as shown.

- **Per agent.** A request without a Bearer header gets `401` with `WWW-Authenticate: Bearer realm="geass"`. A request
  whose token Geass refuses (wrong, expired, revoked) or cannot check gets `400` with `no server available`. Each token
  gets its own set of tools. The server remembers what a token may do for 5 minutes, and a session keeps its tools until it
  ends, so reconnect after you change a token's role.
- **No encryption.** The server speaks plain HTTP, and the token is in every request. A loopback address (`127.0.0.1`,
  `[::1]`) is safe. Any other address makes the server print `warning: this address is not local. Put it behind TLS: the
  token is sent with every request.` Put it behind a reverse proxy that terminates TLS, or reach it through an SSH tunnel.
- **Host header.** When a connection arrives on a loopback address, the server refuses a request whose `Host` header is not
  also loopback, with `403 Forbidden: invalid Host header`. A proxy on the same machine therefore has to send the
  loopback `Host` (for nginx, `proxy_set_header Host 127.0.0.1:8765;`), or you listen on an address the proxy reaches
  over the network.
- **Sessions are in memory.** Restarting the server ends them. A client that sends an old session id gets `404 session
  not found` and has to connect again.
- **Stopping.** Interrupt it with Ctrl-C or send `SIGTERM`. It shuts down within 5 seconds.

### Flags

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--read-only` | boolean | `false` | Offer only tools that change nothing, whatever the token could do. A **Full access** token still gets `database_connection`, which only reads. |
| `--allow-delete` | boolean | `false` | Also offer `delete_resource`, which permanently deletes resources. |
| `--listen` | string | none | Serve MCP over HTTP at this address, such as `127.0.0.1:8765`, instead of standard input and output. |

The global flags `--url`, `--token` and `--context` choose the Geass address and token in the usual way (see
[CLI commands](/docs/reference/cli/#global-flags)). With `--listen`, the flags apply to every agent that connects, and no
token is needed on the server's side.

## Connect an agent

The commands below are the clients' own syntax. Check the client's documentation for the current form.

### Claude Code

Let Claude Code start the server with your saved login:

```bash
claude mcp add geass -- geass mcp
```

Better, give it its own token and a read-only server:

```bash
claude mcp add --env GEASS_URL=https://geass.example.com --env GEASS_TOKEN=gsw_1a2b3c4d_0123456789abcdef0123456789abcdef01234567 geass -- geass mcp --read-only
```

Or run one server over HTTP and point Claude Code at it:

```bash
geass mcp --listen 127.0.0.1:8765 --url https://geass.example.com
claude mcp add --transport http geass http://127.0.0.1:8765 --header "Authorization: Bearer $GEASS_TOKEN"
```

In Claude Code, `/mcp` shows whether the server connected.

### Claude Desktop

Claude Desktop starts local servers from its configuration file: `~/Library/Application Support/Claude/claude_desktop_config.json`
on macOS, `%APPDATA%\Claude\claude_desktop_config.json` on Windows. Add the server and restart Claude Desktop:

```json title="claude_desktop_config.json"
{
  "mcpServers": {
    "geass": {
      "command": "/usr/local/bin/geass",
      "args": ["mcp", "--read-only"],
      "env": {
        "GEASS_URL": "https://geass.example.com",
        "GEASS_TOKEN": "gsw_1a2b3c4d_0123456789abcdef0123456789abcdef01234567"
      }
    }
  }
}
```

Use the full path to `geass` (`which geass` prints it): a desktop app often does not see the `PATH` of your shell. Drop
`--read-only` and use a **Full access** token only for an agent that should deploy.

### Other MCP clients

Geass supports two transports. Use the one your client offers:

| Transport | How to configure the client |
| --- | --- |
| Standard input and output | Command `geass`, arguments `mcp` (and any flags). Put `GEASS_URL` and `GEASS_TOKEN` in the client's environment settings, or let the command use your saved login. |
| Streamable HTTP | The address printed by `geass mcp --listen`, such as `http://127.0.0.1:8765`, and an `Authorization: Bearer <token>` header. |

The older HTTP and server-sent-events transport is not supported. Many clients read a file of this shape, but the names of the
fields differ from client to client:

```json title=".mcp.json"
{
  "mcpServers": {
    "geass": {
      "type": "http",
      "url": "http://127.0.0.1:8765",
      "headers": { "Authorization": "Bearer gsw_1a2b3c4d_0123456789abcdef0123456789abcdef01234567" }
    }
  }
}
```

To check that an HTTP server is up, ask for it without a token. The `401` is the server answering:

```bash
curl -i http://127.0.0.1:8765
```

```text
HTTP/1.1 401 Unauthorized
Www-Authenticate: Bearer realm="geass"
...
send your Geass API token as a Bearer token
```

## What an agent can do

| Area | Tools |
| --- | --- |
| Who it is | `whoami` |
| See what runs | `list_projects`, `list_apps`, `get_app`, `app_status`, `app_deployments`, `app_metrics` |
| Read logs and variable names | `get_logs`, `deployment_logs`, `list_variables` |
| Databases, buckets, nodes, compliance | `list_databases`, `get_database`, `database_connection`, `list_buckets`, `list_nodes`, `project_compliance` |
| Project limits and network | `project_quota`, `project_network`, and `set_project_quota` (an admin token, not read-only) |
| Deploy and recover | `deploy_app`, `rollback_app` |
| Scale and configure | `scale_app`, `set_sleep_when_idle`, `set_variable`, `unset_variable` |
| Functions | `set_function_trigger`, `remove_function_trigger`, `invoke_function` |
| Create and delete | `create_app`, `delete_resource` (only with `--allow-delete`) |

A tool that deploys can wait: `deploy_app` with `wait` set returns once the service is ready or has failed, with the
reason, so the agent does not have to poll. See [MCP tools](/docs/reference/mcp-tools/) for every argument.

## What an agent cannot do

The server has only the tools above. There is no tool, so an agent cannot:

- Create projects, environments, databases or buckets, delete projects or environments, change a project's settings
  other than its resource limits, or restore a backup. (With `--allow-delete`, `delete_resource` can delete a service, database, bucket or logical
  database.)
- Change a service's image, source, size, domain or any setting other than its replicas, its sleeping when idle, its
  variables and a function's schedules. It cannot edit a function's code or create a function.
- Open a terminal in a pod, or read a pod's files.
- Tag nodes, or change a project's compliance tags or who can reach its services. It can only read them. It can change a project's resource limits with `set_project_quota`, if the token is an admin's.
- Create, list or revoke API tokens, or change users, connections or cluster settings.
- Read a secret's value, a database password or a connection string. See below.

The server also offers no MCP resources and no prompts. It offers tools only.

## The safety model

**The token decides.** The agent has exactly the access of its token. The server registers a tool only when the token
may use it, and the API checks the token again on every call. A call that Geass refuses comes back to the agent as an
error that says `(this token is not allowed to do that; do not retry)`.

| Token | Reads | Reads logs and variable names | Changes things |
| --- | --- | --- | --- |
| **Read only** | Yes | No | No |
| **Read only, with logs** | Yes | Yes | No |
| **Full access** | Yes | Yes | Yes |
| Project token | One environment | Yes | Yes, in that environment |

**Flags narrow it.** `--read-only` removes every tool that changes anything, even for a **Full access** token. It
keeps `database_connection` for that token, because reading how to reach a database changes nothing. Deleting is off unless you pass `--allow-delete`, and `delete_resource` then also needs the resource's name repeated in
`confirm`, so the agent cannot delete by accident from a vague request.

**The agent host can ask you.** Every tool says whether it is read-only, idempotent or destructive, in the standard MCP
annotations. A host that asks before running tools that are not read-only will ask before `deploy_app`, and a host that
asks before destructive tools will ask before `delete_resource`. Keep that prompt on for any agent with write access.

**Secrets stay hidden, with one exception you should know.**

- Secret values are never returned: `get_app` empties them, `list_variables` returns secrets by name only, and the
  result of `set_variable` does not repeat the value you set.
- Database passwords and connection strings are never returned. `database_connection` returns the host, port, database
  and user, and never asks Geass for the password.
- Plain configuration is not secret. `list_variables` returns the values of plain configuration, and `get_app` returns
  them to a **Full access** token. Put anything sensitive in a secret (`secret: true` in `set_variable`).

**Changes are pending until a deploy.** `set_variable`, `unset_variable` and the schedule tools change the service's
settings, but nothing reaches the running service until `deploy_app`, unless the agent passes `deploy` to
`set_variable` or `unset_variable`. The instructions the server sends the agent say so.

**Results are bounded.** A tool result is cut at 60,000 characters, `get_logs` returns at most 500 lines, and a wait
for a deploy ends after at most 900 seconds.

**What an agent with write access can still do.** A **Full access** or project token can deploy, scale to zero copies or
a thousand, overwrite variables and break a service, and run a function. Limit the token, not the prompt.

**Untrusted text.** Log lines, function answers and variable values can contain text written by someone else. An agent
that reads them and can also change things may follow instructions hidden in them. For an agent that reads logs of a
service the public can reach, prefer **Read only, with logs**.

**Audit.** The operator logs each API call with its method, path and status, but not who made it
(`kubectl -n geass-system logs deployment/geass-controller-manager -c manager`). A deployment made by an agent shows the
actor `Dashboard`, like any other. What tells you which agent did something is the token: **Settings** → **Tokens**
shows when each was **Last used**. Use one token per agent, so you can tell them apart and revoke one.

## Example sessions

The calls below are what the agent sends. The results are shortened.

### Find out why a service is not ready

The agent has a **Read only, with logs** token. A person asks: "Why is `web` in production not ready?"

**Step 1. List the services.**

```json title="list_apps"
{ "project": "proj-1a2b3c4d", "environment": "production" }
```

`web` has `"state": "pending"`.

**Step 2. Read its status.**

```json title="app_status"
{ "app": "web" }
```

```json
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

Two copies are wanted and none is running, and no build failed. The next place to look is the logs.

**Step 3. Read recent errors.**

```json title="get_logs"
{ "app": "web", "level": "error", "tail": 50 }
```

```text
2026-10-12T09:41:07Z web-6d9c7b8f5-x2k4p Error: connect ECONNREFUSED 203.0.113.10:5432
```

**Step 4. Read the variables, and the memory and CPU the service uses.**

```json title="list_variables"
{ "app": "web" }
```

```json
{
  "secrets": ["STRIPE_KEY"],
  "env": [],
  "config": { "DATABASE_HOST": "orders-db-old" }
}
```

```json title="app_metrics"
{ "name": "web", "window": "1h" }
```

The agent reports that `DATABASE_HOST` points at a host that may not exist. It cannot change anything, because its token
is read-only, and `set_variable` was never in its tool list.

### Change a variable and redeploy

The agent has a **Full access** token, or a project token for `proj-1a2b3c4d` / `production`. A person tells it to
point `web` at the new database host.

```json title="set_variable"
{ "app": "web", "key": "DATABASE_HOST", "value": "orders-db", "deploy": true }
```

With `deploy` set, the tool deploys and waits up to 300 seconds for the outcome:

```json
{
  "app": "web",
  "change": "set",
  "deploy": {
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
  },
  "key": "DATABASE_HOST"
}
```

Without `deploy`, the result has `"pending": true` and `"next": "call deploy_app to apply it"`, and the agent calls
`deploy_app` with `wait` set when it has made all its changes:

```json title="deploy_app"
{ "app": "web", "wait": true, "timeout_seconds": 300 }
```

If the build fails, the result is not an error. It says `"failed": true` and carries the reason in `error` and in
`status.build.failureReason`, so the agent can decide what to do next.

### Roll back a bad release

The agent lists the history, newest first, and goes back to the previous deployment.

```json title="app_deployments"
{ "app": "web" }
```

```json
{
  "items": [
    { "id": "web-k7x2p", "phase": "Recorded", "image": "ghcr.io/acme/storefront:2f9c1d4", "actor": "Dashboard", "changeTitle": "Deploy ghcr.io/acme/storefront:2f9c1d4", "createdAt": "2026-10-12T09:38:12Z" },
    { "id": "web-9fq4m", "phase": "Recorded", "image": "ghcr.io/acme/storefront:b71e0aa", "actor": "Dashboard", "changeTitle": "Deploy ghcr.io/acme/storefront:b71e0aa", "createdAt": "2026-10-11T15:02:40Z" }
  ]
}
```

```json title="rollback_app"
{ "app": "web", "deployment": "web-9fq4m", "wait": true }
```

Leave `deployment` out to go to the one before the newest. The result has the same outcome as `deploy_app`.

### Scale under load

```json title="app_metrics"
{ "name": "web", "window": "1h" }
```

The `cpu` series stays high. The agent scales out and checks the result:

```json title="scale_app"
{ "app": "web", "replicas": 4 }
```

```json
{ "app": "web", "replicas": 4 }
```

```json title="app_status"
{ "app": "web" }
```

Scaling takes effect at once and needs no deploy.

### Schedule a function

```json title="set_function_trigger"
{ "app": "report", "name": "nightly", "schedule": "0 3 * * *", "path": "/run" }
```

```json
{ "app": "report", "next": "call deploy_app to start the schedule", "pending": true, "trigger": "nightly" }
```

```json title="deploy_app"
{ "app": "report", "wait": true }
```

```json title="invoke_function"
{ "app": "report", "path": "/run" }
```

The schedule starts with the deploy. `invoke_function` runs the function now, so an agent should call it only when it is
meant to.

## What happens under the hood

`geass mcp` is the command line's own client of the `/api/v1` API (see [REST API](/docs/reference/rest-api/)) behind an MCP
server built on the official MCP Go SDK. Each tool is one or two API calls, listed in [MCP tools](/docs/reference/mcp-tools/).
There is no separate state.

- When a session starts, the server calls `GET /me` with the token to learn its role, and registers only the tools that role
  may use. That is the only call it makes before the agent asks for something.
- Over HTTP, the server builds one MCP server per token, keeps it for 5 minutes and shares it between that token's
  sessions. Every tool call still goes to Geass with the token, so a revoked token stops working at once even though its tools
  stay listed until the session ends.
- A deploy that waits reads `GET /apps/{name}/status` every 3 seconds, and sends the client progress notifications as the
  state changes when the client asked for them.

## Troubleshooting

**The agent host says the server failed to start.** Run the command yourself with the same environment and read standard
error. `` Error: not signed in: run `geass login --url https://your-geass-address` `` means there is no address or token.
`Error: could not sign in to Geass with this token: ...` means Geass refused the token: it is wrong, expired or
revoked. Both exit with code `4`.

**`geass: command not found` in the host's log.** The host does not see your `PATH`. Use the full path in the host's
configuration.

**The agent has fewer tools than expected.** Ask it to call `whoami`, or read the line `geass MCP server ready with N
tools` on standard error. A **Read only** token sees 12, **Read only, with logs** 15, a project token 18 and **Full access**
26. `--read-only` removes the tools that change anything, so **Full access** then sees the 15 of **Read only, with
logs** and `database_connection` (16). `delete_resource` needs `--allow-delete`. See [MCP tools](/docs/reference/mcp-tools/#which-tools-an-agent-sees).

**I changed the token's role and the agent did not notice.** A session keeps the tools it started with. Restart the server,
or reconnect the agent. Over HTTP, wait 5 minutes or restart the server too.

**A tool answers `... (this token is not allowed to do that; do not retry)`.** Geass refused with `403`. Use a token with
more access, or do it yourself.

**A tool answers `... (the token is missing, wrong or expired)`.** Create a new token and update the agent's configuration.

**A tool answers `... (it does not exist, or is not visible to this token)`.** The name is wrong, or the service is outside a
project token's environment. `list_apps` shows the names the token can see.

**`get_logs` says `No log lines matched.` though the service logs.** `level` and `search` look only through the newest
lines: the dashboard reads `tail` lines from the end of each copy's log and filters them after. Raise `tail` (up to 500)
or loosen `since`, `level` and `search`. `No log lines matched.` is also what a service with no running copy returns.

**`deployment_logs` shows `... (N earlier lines left out)`.** Each stage is cut to its last 200 lines, which is where a
failed build or rollout says why. Read the whole log with `geass api GET /apps/APP/deployments/ID/logs`.

**`app_metrics` returns `"available": false`.** Prometheus is not installed or not reachable. See
[Observability](/docs/guides/observability/).

**`deploy_app` with `wait` never returns, or the host gives up.** A wait lasts up to 900 seconds. Some hosts cut off a tool
that runs long. Pass a smaller `timeout_seconds`, or call it without `wait` and poll `app_status`.

**`create_app` says `"draft": true`.** The agent did not pass `deploy`, so the service is saved and waits: call
`deploy_app`. With `deploy` set the result says `"draft": false` and `"deploying": true`, and `app_status` shows how it
is going.

**Over HTTP: `401`.** The request has no `Authorization: Bearer ...` header, or the word `Bearer` is not spelled as shown.

**Over HTTP: `400 no server available`.** Geass refused the token, or the server cannot reach Geass. Check the token with
`geass whoami --token ... --url ...`, and check the address the server was started with.

**Over HTTP: `403 Forbidden: invalid Host header`.** A proxy on the same machine forwards a `Host` that is not a loopback
address. See [Over HTTP](#over-http).

**Over HTTP: `404 session not found`.** The server restarted. Connect again.

**Over HTTP: `415` or `400` about `Content-Type` or `Accept`.** A client that is not an MCP client sent the request. An MCP
client sends `Content-Type: application/json` and an `Accept` header that lists both `application/json` and
`text/event-stream`.

**`listen tcp 127.0.0.1:8765: bind: address already in use`.** Another process has the port. Pick another address.

## Related

- [MCP tools](/docs/reference/mcp-tools/)
- [Command line](/docs/guides/command-line/)
- [CLI commands](/docs/reference/cli/)
- [Users and API tokens](/docs/guides/users-and-tokens/)
- [Security](/docs/operations/security/)
