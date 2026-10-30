---
title: "MCP server for AI agents"
description: "Run geass mcp so that an AI agent can inspect, deploy, scale, and configure services on Geass, within the role of the token that it uses."
---

The Model Context Protocol (MCP) is how AI agents, for example Claude Code or Claude Desktop, use tools. `geass mcp` is
an MCP server that is part of the [`geass` command line](/docs/guides/command-line/). It gives an agent the same
operations as the command line:

- See what runs.
- Read status and metrics.
- Deploy, and wait for the result.
- Scale.
- Roll back.
- Change variables.
- Schedule and call functions.

The agent acts as an API token, and the server shows the agent only the tools that the token can use. A read-only token
cannot even see a tool that deploys.

You install nothing other than the command line. [MCP tools](/docs/reference/mcp-tools/) lists every tool.

## Before you start

You need these things:

- The command line, installed and signed in. See [Command line](/docs/guides/command-line/). Or, an API token and the
  address of your dashboard.
- A decision about what the agent can do. First, read [Choose a token for the agent](#choose-a-token-for-the-agent).
  The token is the safety boundary. The flags of `geass mcp` only narrow it further.

## Choose a token for the agent

Give an agent its own token, not your token. To create the token, do these steps:

1. Open **Settings** → **Tokens**. See [Users and API tokens](/docs/guides/users-and-tokens/).
2. Create a token, and name it after the agent.
3. Give the token the shortest expiry that you can accept.

Then you can revoke the token, and nothing else is affected. **Last used** tells you whether the agent still uses the
token.

| You want the agent to | Token | Tools it sees |
| --- | --- | --- |
| Look at what runs, and read status, metrics, and the list of resources | **Read only** | 18 |
| Also read logs and the names of variables | **Read only, with logs** | 21 |
| Deploy, scale, and change variables and schedules in one environment | A project token for that environment | 19 |
| Do all of that across every project, create services, and read database connection details | **Full access** | 38 (40 with `--allow-delete`) |

**Read only, with logs** is the correct choice for an agent that only watches. A project token is the correct choice
for an agent that deploys. A project token can reach one environment of one project and nothing else.
With **Full access**, the agent can do everything that the dashboard can do and that the tools cover. Thus, use **Full access**
only for agents that you supervise.

## Run the server

### Over standard input and output

An agent host uses this mode. The host starts `geass mcp` as a child process, and it communicates with the process
through its standard input and output.

```bash
geass login --url https://geass.example.com
geass mcp
```

You do not run this command yourself in a terminal. The server waits for MCP messages on standard input. The server
uses the same login as the other commands: the saved login, `--url` and `--token`, or `GEASS_URL` and `GEASS_TOKEN`.
Before it starts, the server checks the token with Geass. It writes what it offers to standard error, because standard
output carries the protocol:

```text
geass MCP server ready with 38 tools: whoami, list_projects, list_apps, get_app, app_status, app_deployments, list_databases, get_database, database_connection, list_buckets, list_nodes, project_compliance, project_network, project_quota, set_project_quota, app_metrics, get_logs, list_variables, deployment_logs, deploy_app, rebuild_app, scale_app, set_sleep_when_idle, set_function_trigger, remove_function_trigger, invoke_function, rollback_app, set_variable, unset_variable, create_app, compute_providers, cloud_connections, node_pools, node_pool, cloud_sizes, create_node_pool, update_node_pool, resume_node_pool
```

The server decides the tools one time, when it starts. To give the agent a different role, start a new server with
another token.

### Over HTTP

With `--listen`, one server serves many agents over Streamable HTTP. Each agent sends its own Geass token. The server
keeps no credentials of its own. It needs only the address of your Geass dashboard.

```bash
geass mcp --listen 127.0.0.1:8765 --url https://geass.example.com
```

```text
geass MCP server listening on http://127.0.0.1:8765 (agents send their Geass token as a Bearer token)
```

The agent connects to that address, on any path. It sends `Authorization: Bearer <Geass token>` with every request.
Write the word `Bearer` as shown.

- **Per agent.** A request without a Bearer header gets `401` with `WWW-Authenticate: Bearer realm="geass"`. If Geass
  refuses the token of a request, or cannot check the token, the request gets `400` with `no server available`. Geass
  refuses a token that is wrong, expired, or revoked. Each token gets its own set of tools. The server remembers what a
  token can do for 5 minutes. A session keeps its tools until it ends. Thus, reconnect after you change the role of a
  token.
- **No encryption.** The server uses plain HTTP, and the token is in every request. A loopback address (`127.0.0.1`,
  `[::1]`) is safe. With any other address, the server prints `warning: this address is not local. Put it behind TLS: the
  token is sent with every request.` Put the server behind a reverse proxy that terminates TLS, or reach the server
  through an SSH tunnel.
- **Host header.** When a connection arrives on a loopback address, the server refuses a request whose `Host` header is
  not also loopback. The error is `403 Forbidden: invalid Host header`. Thus, a proxy on the same machine must send the
  loopback `Host`. For nginx, use `proxy_set_header Host 127.0.0.1:8765;`. Or, listen on an address that the proxy
  reaches over the network.
- **Sessions are in memory.** If you restart the server, the sessions end. A client that sends an old session ID gets
  `404 session not found`, and it must connect again.
- **Stopping.** To stop the server, interrupt it with Ctrl-C or send `SIGTERM`. The server shuts down within 5 seconds.

### Flags

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--read-only` | boolean | `false` | Offer only tools that change nothing, whatever the token can do. A **Full access** token still gets `database_connection`, which only reads. |
| `--allow-delete` | boolean | `false` | Also offer `delete_resource`, which permanently deletes resources. |
| `--listen` | string | none | Serve MCP over HTTP at this address, for example `127.0.0.1:8765`, instead of standard input and output. |

The global flags `--url`, `--token`, and `--context` select the Geass address and token in the usual way. See
[CLI commands](/docs/reference/cli/#global-flags). With `--listen`, the flags apply to every agent that connects, and
the server side needs no token.

## Connect an agent

The commands below use the syntax of each client. For the current form, read the documentation of the client.

### Claude Code

To let Claude Code start the server with your saved login, run this command:

```bash
claude mcp add geass -- geass mcp
```

A better method is to give Claude Code its own token and a read-only server:

```bash
claude mcp add --env GEASS_URL=https://geass.example.com --env GEASS_TOKEN=gsw_1a2b3c4d_0123456789abcdef0123456789abcdef01234567 geass -- geass mcp --read-only
```

Or, run one server over HTTP, and point Claude Code at it:

```bash
geass mcp --listen 127.0.0.1:8765 --url https://geass.example.com
claude mcp add --transport http geass http://127.0.0.1:8765 --header "Authorization: Bearer $GEASS_TOKEN"
```

In Claude Code, `/mcp` shows whether the server connected.

### Claude Desktop

Claude Desktop starts local servers from its configuration file:

- On macOS: `~/Library/Application Support/Claude/claude_desktop_config.json`.
- On Windows: `%APPDATA%\Claude\claude_desktop_config.json`.

Add the server, and then restart Claude Desktop:

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

Use the full path to `geass`. `which geass` prints the path. A desktop app often does not see the `PATH` of your shell.
Remove `--read-only` and use a **Full access** token only for an agent that must deploy.

### Other MCP clients

Geass supports two transports. Use the transport that your client offers:

| Transport | How to configure the client |
| --- | --- |
| Standard input and output | Command `geass`, arguments `mcp` and any flags. Put `GEASS_URL` and `GEASS_TOKEN` in the environment settings of the client, or let the command use your saved login. |
| Streamable HTTP | The address that `geass mcp --listen` prints, for example `http://127.0.0.1:8765`, and an `Authorization: Bearer <token>` header. |

Geass does not support the older HTTP and server-sent-events transport. Many clients read a file of this shape, but the
names of the fields are different for each client:

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

To check that an HTTP server is up, send a request to it without a token. The `401` shows that the server answers:

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
| Databases, buckets, servers, and compliance | `list_databases`, `get_database`, `database_connection`, `list_buckets`, `list_nodes`, `project_compliance` |
| Project limits and network | `project_quota`, `project_network`, and `set_project_quota` (an admin token, not read-only) |
| Clouds and pools | `compute_providers`, `cloud_connections`, `node_pools`, and `node_pool` need any workspace token. `cloud_sizes` needs an admin token. `create_node_pool`, `update_node_pool`, and `resume_node_pool` need an admin token that is not read-only. `delete_node_pool` is available only with `--allow-delete` |
| Deploy and recover | `deploy_app`, `rebuild_app`, `rollback_app` |
| Scale and configure | `scale_app`, `set_sleep_when_idle`, `set_variable`, `unset_variable` |
| Functions | `set_function_trigger`, `remove_function_trigger`, `invoke_function` |
| Create and delete | `create_app`, `delete_resource` (only with `--allow-delete`) |

A tool that deploys can wait. With `wait` set, `deploy_app` and `rebuild_app` return when the service is ready or has
failed, with the reason. Thus, the agent does not have to poll. For every argument, see
[MCP tools](/docs/reference/mcp-tools/).

## What an agent cannot do

The server has only the tools above. No tool exists for these actions, so an agent cannot do them:

- Create projects, environments, databases, or buckets. Delete projects or environments. Change the settings of a
  project other than its resource limits. Restore a backup. With `--allow-delete`, `delete_resource` can delete a
  service, database, bucket, or logical database.
- Change the image, source, size, domain, or any other setting of a service. The exceptions are its replicas, whether
  it sleeps when idle, its variables, and the schedules of a function. An agent also cannot edit the code of a function
  or create a function.
- Open a terminal in a service, or read its files.
- Tag servers, or change the compliance tags of a project or who can reach its services. An agent can only read these
  settings. With an admin token, an agent can change the resource limits of a project with `set_project_quota`.
- Create, list, or revoke API tokens, or change users or platform settings.
- Make, change, verify, or remove a connection to a cloud. Give a cloud a credential. Add or remove a single server. An
  agent can read the connections, but never their credentials. With an admin token, an agent can run pools on a
  connection that a person made. See [Node pools and money](#node-pools-and-money).
- Make a pool of servers that you own.
- Read the value of a secret, a database password, or a connection string. See the section
  The safety model, below.

The server also offers no MCP resources and no prompts. It offers only tools.

## The safety model

**The token decides.** The agent has exactly the access of its token. The server registers a tool only when the token
can use it. The API checks the token again on every call. If Geass refuses a call, the agent gets an error that says
`(this token is not allowed to do that; do not retry)`.

| Token | Reads | Reads logs and variable names | Changes things |
| --- | --- | --- | --- |
| **Read only** | Yes | No | No |
| **Read only, with logs** | Yes | Yes | No |
| **Full access** | Yes | Yes | Yes |
| Project token | One environment | Yes | Yes, in that environment |

**Flags narrow it.** `--read-only` removes every tool that changes anything, even for a **Full access** token. For that
token, `--read-only` keeps `database_connection` and `cloud_sizes`. A tool that reads how to reach a database, or what
sizes a cloud offers, changes nothing. The delete tools are off unless you pass `--allow-delete`. With that flag,
`delete_resource` and `delete_node_pool` also need the name of the resource repeated in `confirm`. Thus, the agent
cannot delete by accident from a vague request.

**The agent host can ask you.** In the standard MCP annotations, every tool says whether it is read-only, idempotent, or
destructive. A host that asks before it runs tools that are not read-only asks before `deploy_app`. A host that asks
before destructive tools asks before `delete_resource` and `delete_node_pool`. Keep that prompt on for any agent with
write access.

**Secrets stay hidden, with one exception you should know.**

- Geass never returns secret values. `get_app` empties them, `list_variables` returns secrets by name only, and the
  result of `set_variable` does not repeat the value that you set.
- Geass never returns database passwords or connection strings. `database_connection` returns the host, port,
  database, and user. It never asks Geass for the password.
- Plain configuration is not secret. `list_variables` returns the values of plain configuration, and `get_app` returns
  them to a **Full access** token. Put anything sensitive in a secret (`secret: true` in `set_variable`).

### Node pools and money

An admin token also offers `create_node_pool`, `update_node_pool`, and `resume_node_pool`. A [pool](/docs/guides/node-pools/) creates machines at a cloud. The cloud bills the account of the connection until the machines are removed. Thus, these tools stop where the decisions of a person begin:

- **No tool takes a credential.** No tool makes, changes, verifies, or removes a connection, and no tool takes a token, a key, or a password. A credential typed into the conversation of an agent would be a secret that the agent could leak. Thus, a person adds connections in the dashboard or with `geass connections add`. The tools that read return the names of what a cloud takes, never a value.
- **No tool adds a single server.** A single server is billed until someone removes it. The agent can only run a pool that the connection of a person already pays for.
- **A pool needs a most.** `create_node_pool` refuses to run without `max_nodes`. `max_nodes` is the most machines that the pool can ever have, which is also the most that the pool can cost. The tool also refuses to guess whether a pool that can grow autoscales. The agent must say. The instructions that the server sends to the agent tell it this.
- **Deleting needs `--allow-delete`.** Without the flag, `delete_node_pool` does not exist. With the flag, the agent must repeat the name of the pool in `confirm`.

An agent can still spend up to the most of every pool that it creates, on any connection for machines that exists. It can also change the size or the most of a pool. Give an agent an admin token only when you accept that risk, and set a budget alert at the cloud.

**Changes are pending until a deploy.** `set_variable`, `unset_variable`, and the schedule tools change the settings of
the service. But nothing reaches the running service until `deploy_app`, unless the agent passes `deploy` to
`set_variable` or `unset_variable`. The instructions that the server sends to the agent tell it this.

**Results are bounded.** A tool result is cut at 60,000 characters. `get_logs` returns at most 500 lines. A wait for a
deploy ends after at most 900 seconds.

**What an agent with write access can still do.** A **Full access** or project token can deploy, scale to zero copies
or a thousand, overwrite variables and break a service, and run a function. Limit the token, not the prompt.

**Untrusted text.** Log lines, function answers, and variable values can contain text that someone else wrote. An agent
that reads this text and can also change things might follow instructions hidden in it. For an agent that reads logs
of a service that the public can reach, prefer **Read only, with logs**.

**Audit.** A deployment that an agent makes shows the actor `Dashboard`, like any other deployment. **Settings** →
**Tokens** shows when each token was **Last used**. Use one token for each agent, so that you can tell them apart and
revoke one.

## Example sessions

The calls below are what the agent sends.

### Find out why a service is not ready

The agent has a **Read only, with logs** token. A person asks: "Why is `web` in production not ready?" The agent
makes these calls:

1. `list_apps` with `{ "project": "proj-1a2b3c4d", "environment": "production" }`. The service `web` is `pending`.
2. `app_status` with `{ "app": "web" }`. The service wants two copies, and none runs. No build failed.
3. `get_logs` with `{ "app": "web", "level": "error", "tail": 50 }`. The logs show
   `Error: connect ECONNREFUSED 203.0.113.10:5432`.
4. `list_variables` with `{ "app": "web" }`. The plain variable `DATABASE_HOST` is `orders-db-old`.

The agent reports that `DATABASE_HOST` points at a host that might not exist. The agent cannot change anything,
because `set_variable` was never in its tool list.

### Change a variable and redeploy

The agent has a **Full access** token, or a project token for the environment. A person tells the agent to point
`web` at the new database host:

```json title="set_variable"
{ "app": "web", "key": "DATABASE_HOST", "value": "orders-db", "deploy": true }
```

With `deploy` set, the tool deploys and waits up to 300 seconds for the result. Without `deploy`, the result has
`"pending": true` and `"next": "call deploy_app to apply it"`. After the agent makes all its changes, it calls
`deploy_app`:

```json title="deploy_app"
{ "app": "web", "wait": true, "timeout_seconds": 300 }
```

If the build fails, the result is not an error. The result has `"failed": true`, and the reason in `error`. So the
agent can decide what to do next.

### Schedule a function

1. `set_function_trigger` with `{ "app": "report", "name": "nightly", "schedule": "0 3 * * *", "path": "/run" }`. The
   result has `"pending": true`.
2. `deploy_app` with `{ "app": "report", "wait": true }`. The schedule starts with the deploy.

`invoke_function` runs the function immediately. An agent calls it only when the function must run now.

## Troubleshooting

**The agent host says the server failed to start.** Run the command yourself with the same environment, and read
standard error. `` Error: not signed in: run `geass login --url https://your-geass-address` `` means that there is no
address or token. `Error: could not sign in to Geass with this token: ...` means that Geass refused the token, because
the token is wrong, expired, or revoked. Both errors exit with code `4`.

**`geass: command not found` in the host's log.** The host does not see your `PATH`. Use the full path in the
configuration of the host.

**The agent has fewer tools than expected.** Ask the agent to call `whoami`, or read the line
`geass MCP server ready with N tools` on standard error. The tool counts are these:

- A **Read only** token sees 18.
- A **Read only, with logs** token sees 21.
- A project token sees 19.
- A **Full access** token sees 38.

`--read-only` removes the tools that change anything. Then **Full access** sees the 21 tools of **Read only, with
logs**, plus `database_connection` and `cloud_sizes`, which makes 23. `delete_resource` and `delete_node_pool` need
`--allow-delete`, which makes 40 in all. See [MCP tools](/docs/reference/mcp-tools/#which-tools-an-agent-sees).

**I changed the token's role and the agent did not notice.** A session keeps the tools that it started with. Restart the
server, or reconnect the agent. Over HTTP, wait 5 minutes or restart the server too.

**A tool answers `... (this token is not allowed to do that; do not retry)`.** Geass refused with `403`. Use a token
with more access, or do the action yourself.

**A tool answers `... (the token is missing, wrong or expired)`.** Create a new token, and update the configuration of
the agent.

**A tool answers `... (it does not exist, or is not visible to this token)`.** The name is wrong, or the service is
outside the environment of a project token. `list_apps` shows the names that the token can see.

**`get_logs` says `No log lines matched.` though the service logs.** `level` and `search` look only through the last
`tail` lines of each copy. Raise `tail`, up to 500, or loosen `since`, `level`, and `search`. A service with no running
copy also returns `No log lines matched.`

**`deployment_logs` shows `... (N earlier lines left out)`.** Each stage is cut to its last 200 lines. A failed build or
rollout says why in those lines. To read the whole log, run `geass api GET /apps/APP/deployments/ID/logs`.

**`app_metrics` returns `"available": false`.** Prometheus is not installed or not reachable. See
[Observability](/docs/guides/observability/).

**`deploy_app` with `wait` never returns, or the host gives up.** A wait lasts up to 900 seconds. Some hosts stop a tool
that runs for a long time. Pass a smaller `timeout_seconds`, or call `deploy_app` without `wait` and poll `app_status`.

**`create_app` says `"draft": true`.** The agent did not pass `deploy`. Thus, Geass saves the service, and the service
waits. Call `deploy_app`. With `deploy` set, the result says `"draft": false` and `"deploying": true`, and `app_status`
shows the progress.

**Over HTTP: `401`.** The request has no `Authorization: Bearer ...` header, or the word `Bearer` is not spelled as
shown.

**Over HTTP: `400 no server available`.** Geass refused the token, or the server cannot reach Geass. Check the token
with `geass whoami --token ... --url ...`. Also check the address that the server started with.

**Over HTTP: `403 Forbidden: invalid Host header`.** A proxy on the same machine forwards a `Host` that is not a
loopback address. See [Over HTTP](#over-http).

**Over HTTP: `404 session not found`.** The server restarted. Connect again.

**Over HTTP: `415` or `400` about `Content-Type` or `Accept`.** A client that is not an MCP client sent the request. An
MCP client sends `Content-Type: application/json`, and an `Accept` header that lists both `application/json` and
`text/event-stream`.

**`listen tcp 127.0.0.1:8765: bind: address already in use`.** Another process uses the port. Use another address.

## Under the hood

- `geass mcp` calls the same `/api/v1` API as the command line. Each tool is one or two API calls. There is no
  separate state.
- When a session starts, the server calls `GET /me` with the token, and registers only the tools that its role can
  use. Each tool call still goes to Geass with the token. So a revoked token stops working immediately, although its
  tools stay listed until the session ends.
- A deploy that waits reads the status of the service every 3 seconds, and sends progress notifications when the
  client asks for them.
- The Geass log records each API call with its method, path, and status, but not the caller:

  ```bash
  kubectl -n geass-system logs deployment/geass-controller-manager -c manager
  ```

## Related

- [MCP tools](/docs/reference/mcp-tools/)
- [Command line](/docs/guides/command-line/)
- [CLI commands](/docs/reference/cli/)
- [Users and API tokens](/docs/guides/users-and-tokens/)
- [Security](/docs/operations/security/)
