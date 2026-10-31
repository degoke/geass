---
title: "CLI commands"
description: "Every geass command, flag, environment variable, and exit code, with the API calls of each command, the token role that it needs, and an example."
---

This page lists every command of the `geass` command line. To install it, sign in, and learn the everyday workflows, read
[Command line](/docs/guides/command-line/). The command line is a thin client of the `/api/v1`
[REST API](/docs/reference/rest-api/). Each command makes one or two calls. This page lists them under **API calls**
for every command.

## Synopsis

```text
geass [global flags] <command> [arguments] [flags]
```

Global flags work on every command, before or after the command name. To see the command list, run `geass` with no
command, or `geass help`. To see the help for one command, run `geass help <command>` or `geass <command> --help`.

| Group | Commands |
| --- | --- |
| Account | `login`, `logout`, `whoami`, `version`, `config` |
| Services | `apps`, `deploy`, `build`, `status`, `logs`, `env`, `scale`, `rollback`, `metrics` |
| Functions | `functions` |
| Data and cluster | `projects` (with `quota` and `network`), `databases`, `stores`, `nodes`, `compliance`, `addons` |
| Clouds and machines | `providers`, `sizes`, `connections`, `pools`, `servers` |
| Dashboard users | `users` |
| Raw API and agents | `api`, `openapi`, `mcp` |
| Built in | `help`, `completion`, `--help` (`-h`), `--version` (`-v`) |

`deploy`, `build`, `status`, `logs`, `env`, `scale`, and `rollback` also exist under `geass apps`, where they do the same
thing. `metrics` exists only at the top level, and `deployments` only under `geass apps`.

## Global flags

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--context` | string | the current login | Which saved login to use. List them with `geass config get-contexts`. |
| `--url` | string | the saved address | The Geass address, such as `https://geass.example.com`. Overrides the saved one and `$GEASS_URL`. The command line uses a saved login only for its own address. See the list after this table. |
| `--token` | string | the saved token | An API token. Overrides the saved one and `$GEASS_TOKEN`. |
| `-p`, `--project` | string | none | The project, written as its ID, such as `proj-1a2b3c4d`. Also `$GEASS_PROJECT`. |
| `-e`, `--environment` | string | none | The environment, such as `production`. Also `$GEASS_ENVIRONMENT`. |
| `--json` | boolean | `false` | Print JSON, for programs. Not every command honors it. See [Output](#output). |
| `-y`, `--yes` | boolean | `false` | Do not ask before deleting. |
| `-h`, `--help` | boolean | `false` | Show help for the command. |
| `-v`, `--version` | boolean | `false` | Root command only. Print `geass version <version>`. |

A saved login belongs to the address that it was made for, and the command line sends its token only to that address.
When `--url` or `$GEASS_URL` points to another address, the command line does not use the current login's token,
project, or environment for that address:

- If you have exactly one login saved for that address, the command uses it. If you have several, for example an admin
  login and a read-only one, the command uses none of them. It stops with the same message as when you have none. Use
  `--context` to say which login to use.
- The command line never swaps a login that you chose with `--context` or `$GEASS_CONTEXT` for another login. With an
  address that is not its own, that login supplies no token, project, or environment.
- If you have none, the command stops with ``not signed in to ADDRESS: run `geass login --url ADDRESS` `` (exit `4`) and
  sends nothing.
- A token that you give with `--token` or `$GEASS_TOKEN` is your choice for this run. The command line uses it for
  whatever address you give.

Two addresses are the same when their scheme, host, port, and path are the same. A host in capitals, a trailing slash,
and the default port (`:443` for `https`, `:80` for `http`) make no difference. `http://geass.example.com` is not
`https://geass.example.com`: a token saved for one is never sent to the other.

## Environment variables

| Variable | Used by | Meaning |
| --- | --- | --- |
| `GEASS_URL` | every command | The Geass address. Same as `--url`. |
| `GEASS_TOKEN` | every command | An API token. Same as `--token`. `geass login` also reads it as the token to save. |
| `GEASS_PROJECT` | commands that use a project | Same as `--project`. |
| `GEASS_ENVIRONMENT` | commands that use an environment | Same as `--environment`. |
| `GEASS_CONTEXT` | every command that uses a saved login | Which saved login to use. Same as `--context`. |
| `GEASS_CONFIG` | every command | Full path of the configuration file. Without it, the file is `geass/config.json` in your configuration directory. |
| `GEASS_VERSION` | `install-cli.sh` only | Release tag to install. Default: `latest`. |
| `GEASS_INSTALL_DIR` | `install-cli.sh` only | Directory to install `geass` into. |
| `GEASS_REPO` | `install-cli.sh` only | GitHub `owner/name` to download releases from. Default: `degoke/geass`. |
| `GEASS_DOWNLOAD_BASE` | `install-cli.sh` only | Where to download the release files from instead: a mirror, or a directory as `file:///path` that holds `geass-cli-<os>-<arch>` and `SHA256SUMS`. |

The command line itself reads the first five. The install script reads the last four, and `geass` does not read them.

### Precedence

For the address, token, project, and environment, the first of these that is set wins:

1. The flag (`--url`, `--token`, `--project`, `--environment`).
2. The environment variable (`GEASS_URL`, `GEASS_TOKEN`, `GEASS_PROJECT`, `GEASS_ENVIRONMENT`).
3. The saved login.

The saved login is `--context`, else `$GEASS_CONTEXT`, else `current` in the configuration file. It supplies the token,
project, and environment only for its own address. See [Global flags](#global-flags).

The command line might not find a configuration directory, for example when a bare container has no `$HOME`. Then
every command fails with `could not find a configuration directory (set GEASS_CONFIG)`, even when `GEASS_URL` and
`GEASS_TOKEN` are set. To fix it, set `GEASS_CONFIG` to any path. The file does not have to exist.

## Configuration file

`geass login` saves what the command line remembers between runs in `config.json`, in the `geass` folder of your
configuration directory:

| System | Path |
| --- | --- |
| Linux | `$XDG_CONFIG_HOME/geass/config.json`, or `~/.config/geass/config.json` |
| macOS | `~/Library/Application Support/geass/config.json` |
| Windows | `%AppData%\geass\config.json` |

`GEASS_CONFIG` replaces the whole path. The file holds tokens. So Geass writes it with mode `0600` (owner only),
creates its folder with mode `0700`, and replaces it atomically. A file that is not valid JSON stops every command with
`<path> is not valid: ...`.

```json title="config.json"
{
  "current": "geass.example.com",
  "contexts": {
    "geass.example.com": {
      "url": "https://geass.example.com",
      "token": "gsw_1a2b3c4d_0123456789abcdef0123456789abcdef01234567",
      "project": "proj-1a2b3c4d",
      "environment": "production",
      "user": "token:CLI: geass CLI on laptop",
      "role": "observer",
      "expiresAt": "2026-11-11T09:00:00Z"
    }
  }
}
```

| Field | Meaning |
| --- | --- |
| `current` | The name of the login that the command line uses when you do not select one. `geass login` and `geass config use-context` set it. |
| `contexts` | The saved logins, by name. The name is the host of the address, unless you give `--name` or `--context` to `geass login`. For example, `geass.example.com`, or `localhost:8085` when the address has a port. |
| `url` | The Geass address, without a trailing slash. |
| `token` | The API token. Empty after `geass logout`. |
| `project`, `environment` | The defaults set by `geass config set-context`. |
| `user`, `role` | What the token was when you signed in: `token:<token name>`, and `admin`, `observer`, or `viewer`. For information only. |
| `expiresAt` | When the token expires, as `geass login` last saw it. Absent for a token that never expires. `geass config get-contexts` shows it, and `geass logout` clears it. |

## Output

- **Results go to standard output. Progress and notes go to standard error.** A line such as `web: build running`,
  `set LOG_LEVEL`, or `The change is pending...` is on standard error. So a program that reads standard output never
  sees it.
- Lists and statuses print an aligned table by default. With `--json`, they print a JSON object with an `items` array.
- `geass apps get`, `geass projects get`, `geass databases get`, `geass stores get`, `geass api`, and `geass openapi`
  always print JSON.
- With `--json`, `geass deploy` and `geass build` print `{"deployed": [...]}` or `{"building": [...]}` when they do not
  wait. When they wait, they print `{"items": [...]}`, which holds the services' statuses.
- Some commands print plain text lines whatever `--json` says. These commands are `scale`, `rollback`, `env set`,
  `env unset`, `apps create`, `apps delete`, `nodes tag`, `compliance set`, `compliance clear`, `projects network set`,
  `functions triggers add`, `functions triggers remove`, `logout`, `config use-context`, and `config set-context`.
  The commands that change connections, pools, and servers also print plain text: `connections add`, `update`,
  `remove`, and `verify`, `pools create`, `update`, `delete`, and `resume`, and `servers add` and `remove`.
- With `--json`, `geass logs` prints one indented JSON object per log line. The objects are not on one line each. So
  read them as a stream (`jq -c .` reads them).
- Ages in tables are `30s`, `5m`, `3h` (under 48 hours), or `4d`.

## Exit codes

A script can tell what kind of failure it had from the code alone. The command line prints the error text on standard
error as `Error: <message>`.

| Code | Meaning | Examples |
| --- | --- | --- |
| `0` | Success | |
| `1` | Any other error | A network failure, a `400` from the API (such as a value out of range), `cancelled` after you answered no to a delete, a function that answered HTTP 400 or above, or `no metrics yet`. |
| `2` | Usage error | An unknown command or flag, a missing argument, `KEY=VALUE` without an `=`, `--sleep-after` out of range, or a delete without `--yes` when the command line cannot ask anybody. |
| `3` | Not found | The API answered `404` (also for a service that a project token must not see), `no saved login called ...`, or `has no earlier deployment to go back to`. |
| `4` | Not allowed | The API answered `401` or `403`, you are not signed in, or someone refused the sign-in in the dashboard. On `401`, the command line also prints ``Run `geass login` to sign in again.`` |
| `5` | Deploy failed | A `--wait` ended with a failed build or a failed rollout. The reason is in the message. |
| `6` | Timed out | A `--wait` ran past `--timeout`, or nobody approved a sign-in before its code expired. |

## Account commands

### `geass login`

Signs in to a Geass dashboard and saves the login.

```text
geass login [--url URL] [--token-stdin] [--no-browser] [--name NAME]
```

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--token-stdin` | boolean | `false` | Read the token from the first line of standard input. |
| `--no-browser` | boolean | `false` | Do not open the browser. The command prints the address either way. |
| `--name` | string | the `--context` value, else the host of the address | What to call this login. |

The command also uses the global flags `--url`, `--token`, and `--context`.

- **Address.** `--url`, else `$GEASS_URL`, else the address of the current login. With none of them, the command stops
  with `say which Geass to sign in to: geass login --url https://your-geass-address` (exit 2).
- **Token.** The command takes a token only from `--token`, `$GEASS_TOKEN`, or `--token-stdin`. It never reuses a token
  that an earlier login saved, because you run `geass login` again to renew the token.
- **No token.** The command starts a device sign-in:
  1. It prints a code and the address of the approval page.
  2. When it runs in a terminal, and not with `--no-browser`, it opens `<address>/device?code=<code>` in your browser.
  3. It waits for you to approve the code in the dashboard. It asks every 5 seconds, and waits 5 seconds longer each
     time the server says `slow_down`. It stops when the code expires, 10 minutes after the code was issued.
- **Check and save.** The command checks the token with `GET /me` before it saves anything. It does not save a login
  that fails. It saves a login that works under `--name`, else `--context`, else the host of the address. That login
  becomes the current login. It keeps the project and environment defaults of an earlier login of that name, when the
  earlier login was for the same address. A name that points to another address starts with no defaults, because a
  project ID from one Geass means nothing on another. The command saves the token's expiry time with it.
- **Output.** `Signed in to <url> as <user> (<access>)`, where access is `admin`, `read-only, with logs`, `read-only`,
  or `project token for <project>/<environment>`. When [organizations](/docs/guides/organizations-and-workspaces/) are
  on, the access says where you work: `admin of Acme`, `read-only of Acme / Payments team`, or `admin of the platform`.
  With `--json`: `{"context", "url", "user", "role"}`, and `expiresAt` when the token expires.

**API calls:** `POST /auth/device`, `POST /auth/token` (repeated), `GET /me`. The first two need no token.
**Needs:** nothing to start. An admin signed in to the dashboard must approve the code. A token that you give with
`--token` only has to be valid.

```bash
geass login --url https://geass.example.com
echo "$TOKEN" | geass login --url https://geass.example.com --token-stdin --name ci
```

### `geass logout`

Forgets the token, user, role, and expiry of the current login, or of the login that you selected with `--context` or
`$GEASS_CONTEXT`. The address and the project and environment defaults stay.

```text
geass logout
```

Prints `Signed out of <url>.`, or `Not signed in.` when there is no such login. The token itself stays valid until it
expires, or until you revoke it under **Settings** → **Tokens** in the dashboard.

**API calls:** none. **Needs:** nothing.

### `geass whoami`

Shows who the saved token is and what it can do.

```text
geass whoami
```

```text
token:CLI: geass CLI on laptop on https://geass.example.com
access: read-only, with logs
can change things: no
expires: 2026-11-11 09:00 UTC (in 30 days)
```

The `expires` line says when the token stops working, and how much time is left: `in 30 days`, `in 5 hours`, `in under a
minute`, or `expired 2d ago`. For a token with no expiry, it says `never`.

With `--json`, it prints the identity that the API returns. The identity has these fields:

- `username`, `role`, and `scope` (`workspace` or `project`).
- `project` and `environment`, for project tokens only.
- `canMutate`, `canReadSensitive`, and `authentication` (`token`).
- `expiresAt`, which is absent for a token that never expires.
- When the platform has organizations on: `org`, `orgName`, `workspace`, `workspaceName`, or `platformAdmin`. The
  `access` line then ends with where you work, for example `read-only of Acme / Payments team`.

**API calls:** `GET /me`. **Needs:** any signed-in token.

### `geass version`

Shows the version of the command line, and the API version and Geass release of the cluster that it talks to.

```text
geass version
```

```text
geass v0.2.0
api v1
release v0.3.0
note: this command line is older than the cluster. Update it to v0.3.0: run install-cli.sh again (GEASS_VERSION=v0.3.0 pins it)
```

The `api` and `release` lines appear only when the address is known and the cluster accepts the token. A cluster from
before Geass reported its release prints no `release` line. The `note` line appears when the command line and the
cluster are different releases that can be ordered. The command line talks to the cluster's API, whose resources carry
the fields of the cluster's release. So keep the two releases in step. The command line sends the saved token only to
the address that it was saved for. Without a login, the command prints only the first line, with exit 0. A command line
built without a release tag reports `dev`, and says nothing about order. With `--json`:
`{"cli": "...", "api": "...", "release": "..."}`.

**API calls:** `GET /version` (optional). **Needs:** nothing.

### `geass config`

Manages saved logins and their defaults. None of these commands calls the API.

#### `geass config get-contexts`

Lists the saved logins. The first column is `*` for the current one.

```text
geass config get-contexts
```

```text
   NAME                 URL                          USER                            PROJECT        ENVIRONMENT  EXPIRES
*  geass.example.com    https://geass.example.com    token:CLI: geass CLI on laptop  proj-1a2b3c4d  production   2026-11-11
   staging.example.com  https://staging.example.com  -                               -              -            -
```

`EXPIRES` is the day that the token expires, as `geass login` saw it. After that day has passed, `(expired)` follows it.
The column shows `-` when the expiry was not recorded, or when the token never expires. With `--json`, the command
prints the whole configuration, with each token replaced by `(saved)`.

#### `geass config use-context NAME`

Makes a saved login the current one. Prints `Using NAME.`. If there is no such login, it exits `3` and lists the names
that you have.

#### `geass config set-context [NAME]`

Sets the default project and environment of a login from the global `--project` and `--environment` flags. A flag
you leave out keeps its current value. `NAME` defaults to `--context`, then to the current login. Prints
`NAME now defaults to <project> / <environment>.`. If there is no such login, it exits `3`.

```bash
geass config set-context -p proj-1a2b3c4d -e production
```

There is no command that clears a default. Edit `config.json`, or sign in again under another name.

## Services

Most commands in this group take the service's name, which is unique across the whole Geass install, not only inside
a project. `geass apps` is also known as `geass app`, `geass services`, and `geass service`.

### `geass apps list`

Lists services (aliases: `geass apps ls`).

```text
geass apps list [-p PROJECT] [-e ENVIRONMENT]
```

Takes no arguments. The columns are `NAME`, `PROJECT`, `ENV`, `STATE`, `DETAIL`, and `AGE`. `STATE` is `ready`,
`pending`, `failed`, `draft`, or `deleting`. `DETAIL` is the public URL when there is one. Otherwise, it is the image,
the repository, or the function runtime.

```text
NAME  PROJECT        ENV         STATE  DETAIL                   AGE
web   proj-1a2b3c4d  production  ready  https://web.example.com  3h
api   proj-1a2b3c4d  production  draft  nginx:alpine             3d
```

The command narrows the list only when you pass `--project` or `--environment` on the command line. A project or
environment that comes from `$GEASS_PROJECT`, `$GEASS_ENVIRONMENT`, or `geass config set-context` does not narrow it,
because a list with nothing said means everything. When you pass one of the flags, the command fills in the other from
those defaults.

The same listing exists for `geass projects list`, `geass databases list`, and `geass stores list`, with the same
columns and rules.

**API calls:** `GET /apps` with `project` and `environment` when narrowed. **Needs:** any signed-in token. A project
token sees only its own environment.

### `geass apps get`

Shows one service with its settings and status, as JSON.

```text
geass apps get NAME
```

The output has three keys: `resource` (the summary row), `spec` (the settings), and `status`. The command empties secret
values in `spec`. It does not empty plain configuration values. See
[Configuration values are visible](#configuration-values-are-visible). `geass projects get`, `geass databases get`, and
`geass stores get` work the same way for their kind.

**API calls:** `GET /apps/{name}`. **Needs:** any signed-in token.

### `geass status`

Shows whether services are up, and what the last deploy left behind (also `geass apps status`).

```text
geass status [APP...]
```

With names, it shows those services. With no names, it shows every service in the project and environment that you
give. These values come from the flags, the environment variables, or the saved defaults. If you give no project and
environment, it shows every service in the install, with one API call for each service.

```text
NAME  STATE   READY  BUILD      URL                      MESSAGE
web   ready   2/2    Succeeded  https://web.example.com  -
api   failed  0/1    Failed     -                        Kaniko completed without an immutable image digest
```

| Column | Meaning |
| --- | --- |
| `STATE` | `ready`, `pending`, `failed`, `draft`, or `deleting`. |
| `READY` | Running copies over wanted copies, such as `2/2`. A service that sleeps when idle and has stopped shows `0/0 (asleep)`. `-` when the service has no Deployment yet. |
| `BUILD` | The phase of the latest build: `Pending`, `WaitingForCI`, `Running`, `Succeeded`, `Failed`, or `Cancelled`. `-` for a service that has no build. |
| `MESSAGE` | Why the service is not ready, when the service says. |

With `--json`, it prints `{"items": [...]}`. Each item has these fields:

- `name`, `project`, `environment`, `state`, `message`, `url`, `ready`, and `pendingChanges`.
- `replicas`: `desired`, `ready`, and `available`.
- `sleeping`.
- `scaleToZero`, for a service that asked to sleep when idle: `idleSeconds` and `maxReplicas`, and `active` and
  `message` after the cluster has said.
- `build`: `name`, `phase`, `failureReason`, `sourceRevision`, `ciState`, `startedAt`, and `completedAt`.
- `lastAttempt`: `id`, `phase`, `startedAt`, `completedAt`, `summary`, and `errors`.

**API calls:** `GET /apps` (without names), then `GET /apps/{name}/status` for each service. **Needs:** any signed-in
token.

### `geass deploy`

Deploys the pending changes of services (also `geass apps deploy`).

```text
geass deploy [APP...] [--wait] [--timeout DURATION]
```

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--wait` | boolean | `false` | Wait until each service is ready. Exits `5` if one service fails. |
| `--timeout` | duration | `10m` | How long `--wait` waits for each service. The waits run one after another. |

- **With names,** the command deploys each named service.
- **With no names,** the command deploys everything pending in the environment that `--project` and `--environment`
  give. With `--wait`, the command then waits for every service in that environment that is not a draft and is not
  being deleted. Without both values, it stops with `say what to deploy: name services, or give --project and
  --environment to deploy everything pending` (exit 2).
- **Progress** goes to standard error: `web: deploying`, then lines such as `web: build running`,
  `web: pending (1 of 2 ready)`, and `web: ready at https://web.example.com`, one line each time the state changes.
- **With `--wait`** the command prints the final statuses as a table, or as `{"items": [...]}` with `--json`. A service
  is done when it is ready, has no pending changes, and its latest build, if it has one, succeeded. A service has failed
  when its state is `failed` or its build phase is `Failed`. Then the exit code is `5`, and the message gives the
  build's failure reason. A wait that runs past `--timeout` exits `6`.
- **Without `--wait`** and with `--json`, it prints `{"deployed": ["web"]}`.

If you deploy a service that has no pending changes, the command records a new deployment anyway.

**API calls:** `POST /apps/{name}/deploy` for each name. When there are no names, `POST /projects/{project}/deploy` with
`{"environment": ...}` and `GET /apps`. With `--wait`, `GET /apps/{name}/status` every 3 seconds.
**Needs:** Full access or a project token for named services. Full access for the environment form. A project token
gets `403` there, so name the services.

```bash
geass deploy web --wait
geass deploy -p proj-1a2b3c4d -e production --wait
```

### `geass build`

Runs the build of services that are built from a repository again (also `geass apps build`). It does what a push to the
branch does, for a pipeline that has pushed nothing.

```text
geass build [APP...] [--wait] [--timeout DURATION]
```

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--wait` | boolean | `false` | Follow each build to the end of the rollout, and wait until the service is ready. Exits `5` if the build or the rollout fails. |
| `--timeout` | duration | `10m` | How long `--wait` waits for each service. The waits run one after another. |

- **With names,** the command runs each named service's build again, from the head of its branch, or from the commit
  that the service pins. A service that runs an image or a function has no build to run. For such a service, the
  command stops with `<name>: the app has no repository build` (exit 1). A service that was never deployed has no
  build yet, and the command says `the app has not been built yet: its build is made when it is deployed`.
- **With no names,** the command builds every service built from a repository in the environment that `--project`
  and `--environment` give. It leaves out drafts and services that are being deleted. Without both values, it stops
  with `say what to build: name services, or give --project and --environment to build every service built from a
  repository` (exit 2). When the environment has no such service, the command says so on standard error and exits `0`.
- **Progress** goes to standard error: `web: build started`. Then, with `--wait`, lines such as `web: build running`,
  `web: pending (1 of 2 ready)`, and `web: ready at https://web.example.com`, one line each time the state changes.
- **With `--wait`** the command first reads the build of each service as it was before the command. A status can still
  show that finished build: the same build, phase, failure reason, commit, and start and end times. For the first 15
  seconds of the wait for a service, the command does not take such a status as the answer. A build that restarts shows
  as not finished within moments. After the 15 seconds, the command takes such a status as the answer, because a new
  build can end exactly as the last one did, for example when it fails immediately in the same way. The command prints the final statuses as a table, or as
  `{"items": [...]}` with `--json`. A service is done when it is ready, has no pending changes, and its build
  succeeded. A service has failed when its state is `failed` or its build phase is `Failed`. Then the exit code is `5`,
  and the message gives the build's failure reason. A wait that runs past `--timeout` exits `6`.
- **Without `--wait`** and with `--json`, it prints `{"building": ["web"]}`.

A service that is already running continues to serve from its current image until Geass builds the new one. Then Geass
rolls the service out onto the new image. The command does not apply changes that you saved to the service and did not
deploy yet. To apply those changes, use `geass deploy`.

**API calls:** `POST /apps/{name}/build` for each name, or `GET /apps` when there are no names. With `--wait`,
`GET /apps/{name}/status` one time before the build starts, and then every 3 seconds. **Needs:** Full access or a
project token for the service's environment.

```bash
geass build web --wait
geass build -p proj-1a2b3c4d -e production --wait --timeout 20m
```

### `geass apps deployments`

Lists a service's deployments, newest first.

```text
geass apps deployments APP
```

```text
ID         PHASE     AGE  TRIGGER  ACTOR      CHANGE
web-7x2kq  Recorded  3h   -        Dashboard  Deploy nginx:1.27
web-4m8zp  Recorded  3d   push     GitHub     Deploy acme/web@main
```

The newest entry is the one that runs. `ID` is what `geass rollback APP DEPLOYMENT` takes. A rollback adds an entry of
its own. Empty columns show `-`. With `--json`, it prints `{"items": [...]}`. Each item has `id`, `phase`, `image`,
`commit`, `trigger`, `actor`, `changeTitle`, `createdAt`, `startedAt`, and `completedAt`. A service that was never
deployed lists nothing.

**API calls:** `GET /apps/{name}/deployments`. **Needs:** any signed-in token (a project token for its own services).

### `geass logs`

Shows a service's recent logs, or follows them (also `geass apps logs`).

```text
geass logs APP [-f] [--tail N] [--since DURATION] [--level LEVEL] [--search TEXT]
```

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `-f`, `--follow` | boolean | `false` | Keep following new lines, across restarts. Ends with Ctrl-C, which exits `0`. |
| `--tail` | integer | `200` | How many of the newest lines to show. The API accepts 1 to 1000. See the notes after this table. |
| `--since` | string | none | Only lines newer than this long ago, such as `15m` or `2h`. Go duration syntax, up to 7 days: write `48h`, not `2d`. |
| `--level` | string | none | Only lines of this level: `error`, `warn`, `info`, or `debug`. |
| `--search` | string | none | Only lines containing this text, up to 200 characters. |

Each line prints as `HH:MM:SS <pod suffix> <text>`, in UTC. The pod suffix is the last part of the pod's name, so
`web-6d9c7b8f5-x2k4p` shows as `x2k4p`. With `--json`, each line is a JSON object with `t`, `service`, `pod`,
`container`, `line`, and `level`. When nothing matches, the command prints `No log lines matched.` on standard error,
and standard output stays empty.

`--tail` counts the newest lines in all, oldest first. The dashboard reads that many lines from the end of each copy's
log before it applies `--level` and `--search`. So a filter looks through the most recent lines only. For example, with
`--tail 200`, `--level error` finds the errors among each copy's last 200 lines. To look further back, raise `--tail`
(up to 1000). `--level warn` also shows errors. With `-f`, `--tail` is how many recent lines each copy starts with.

**API calls:** `GET /apps/{name}/logs` with `tail`, `since`, `q`, and `level`. With `-f`, one
`GET /apps/{name}/logs/stream` (server-sent events) with the same filters. **Needs:** Read only, with logs, Full access,
or a project token (for its own services). A **Read only** (viewer) token gets `403`.

```bash
geass logs web -f --level error --since 15m
```

### `geass env`

Lists, sets, and removes a service's variables (also `geass apps env`).

Variables are changes like any other. They apply when you next deploy the service, unless you pass `--deploy`.

#### `geass env list`

```text
geass env list APP
```

Prints a `NAME` and `KIND` table. `KIND` is one of these values:

- `secret` for secrets.
- `config` for plain configuration.
- For variables injected from elsewhere, the kind that the service reports: `literal` or `secret`.

The command does not print values. With `--json`, it prints the whole answer, which includes the values of plain
configuration. See [Configuration values are visible](#configuration-values-are-visible).

**API calls:** `GET /apps/{name}/variables`. **Needs:** Read only, with logs, Full access, or a project token.

#### `geass env set`

```text
geass env set APP KEY=VALUE... [--secret] [--deploy] [--wait]
```

Sets each pair. Only the first `=` splits, so a value can contain `=`. A pair without an `=` stops the command with
exit `2`. Prints `set KEY` for each pair, then ``The change is pending. Run `geass deploy APP` to apply it.``

#### `geass env unset`

```text
geass env unset APP KEY... [--secret] [--deploy] [--wait]
```

Removes each key, and prints `removed KEY`. If a key is not there, that is not an error. The command removes plain
configuration, or secrets with `--secret`. It cannot remove variables that Geass injected for you, such as a database
connection string.

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--secret` | boolean | `false` | The variable is a secret, kept apart from ordinary configuration. |
| `--deploy` | boolean | `false` | Deploy the change now. |
| `--wait` | boolean | `false` | With `--deploy`: wait until the service is ready (10 minutes at most), and print its status. Without `--deploy`, it does nothing. |

**API calls:** `POST /apps/{name}/config/set` or `/secrets/set` with `{"key", "value"}`. `POST
/apps/{name}/config/delete` or `/secrets/delete` with `{"key"}`. With `--deploy`, `POST /apps/{name}/deploy` and
status reads. **Needs:** Full access or a project token.

```bash
geass env set web LOG_LEVEL=debug
geass env set web --secret STRIPE_KEY=sk_test_example --deploy --wait
geass env unset web LOG_LEVEL
```

### `geass scale`

Sets how many copies of a service run, or makes it sleep when idle (also `geass apps scale`).

```text
geass scale APP [REPLICAS] [--sleep-after DURATION | --always-on]
```

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--sleep-after` | duration | none | Stop the service after this long without requests, and start it on the next request. 30 seconds to 24 hours, such as `10m`. |
| `--always-on` | boolean | `false` | Keep the service running instead of sleeping when idle. |

`REPLICAS` is a whole number from 0 to 1000. You cannot use the two flags together. With only `APP`, you must give a
flag. Scaling takes effect immediately, with no deploy. So does the sleep setting, after the cluster has accepted it.

```text
$ geass scale web 3
web scaled to 3
$ geass scale web --sleep-after 10m
web sleeps after 10m0s without requests
$ geass scale web --always-on
web stays running
```

`--sleep-after` works only when the serverless add-on is on in the cluster settings, and the service has a public
host. It does not work together with CPU autoscaling. The command saves the setting. Then it waits up to 10 seconds for
the cluster to say whether the service can sleep. It prints `APP sleeps after ...` only when the service can sleep.
When it cannot, the service stays running, the setting stays saved, and the command exits `1` and says why:

```text
$ geass scale web --sleep-after 10m
Error: saved, but web cannot sleep when idle and stays running: The serverless add-on is not installed
```

If you gave `REPLICAS` too, the command applies it before it reports the failure. When the cluster has not answered
within 10 seconds, the message says so and points to `resource.details.scaleToZero`. There, `active` and `message` give
the state. `geass status APP --json` shows them as `scaleToZero`. The command also waits for a service that shows no
request to sleep at all, because the read can lag the change. That wait ends the same way. `--always-on` needs no
check. See [Scale to zero](/docs/guides/scale-to-zero/).

**API calls:** `POST /apps/{name}/scale` with `{"replicas"}`. `PATCH /apps/{name}` with
`{"scaleToZero": "on" | "off", "idleSeconds"}`, then `GET /apps/{name}/status` until `scaleToZero.active` is `true` or
the wait ends. **Needs:** Full access or a project token.

### `geass rollback`

Goes back to an earlier deployment (also `geass apps rollback`).

```text
geass rollback APP [DEPLOYMENT] [--wait]
```

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--wait` | boolean | `false` | Wait until the service is ready (10 minutes at most), and print its status. |

With no `DEPLOYMENT`, the command goes to the deployment before the newest one. With fewer than two deployments, it
stops with `APP has no earlier deployment to go back to` (exit 3). To list the IDs, use
[`geass apps deployments APP`](#geass-apps-deployments). The `ID` of each row is what `DEPLOYMENT` takes.

A rollback deploys immediately and records a new deployment. So if you run `geass rollback APP` again, it goes to the
entry before that one, not further back. To select a deployment, pass its ID. You cannot roll back functions. The API
answers `functions
cannot be rolled back; restore the earlier code and deploy again`.

**API calls:** `GET /apps/{name}/deployments` (without `DEPLOYMENT`), `POST /apps/{name}/rollback` with
`{"revision"}`, and status reads with `--wait`. **Needs:** Full access or a project token.

### `geass apps create`

Creates a service from an image, a GitHub repository, or a function file.

```text
geass apps create NAME (--image IMAGE | --repo OWNER/NAME | --function FILE) [flags]
```

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--image` | string | none | Run this container image. |
| `--repo` | string | none | Build from this GitHub repository, `owner/name`. |
| `--branch` | string | `main` | The branch to build, with `--repo`. |
| `--function` | string | none | Run this file as a function. The command reads the file on your machine and sends it as the code. |
| `--runtime` | string | `bun` | The function's runtime: `bun`, `node`, or `python`. |
| `--mode` | string | `http` | The function's mode: `http` (a program that serves HTTP) or `event` (a handler). |
| `--port` | integer | none | The port that it serves on. Without it, the API uses 8080. |
| `--deploy` | boolean | `false` | Deploy it immediately, and do not save a draft. |

It needs `--project` and `--environment`, or their defaults. It also needs exactly one of `--image`, `--repo`, and
`--function`. If you give several, the first in that order wins. `--runtime` and `--mode` apply only to `--function`.
The service is named `NAME`.

```bash
geass apps create web -p proj-1a2b3c4d -e production --image nginx:alpine --port 80
geass apps create api -p proj-1a2b3c4d -e production --repo acme/storefront --branch main
geass apps create hook -p proj-1a2b3c4d -e production --function hook.mjs --runtime node --mode event
```

Without `--deploy`, Geass saves the service as a draft, and the command prints ``Created NAME as a draft. Deploy it with `geass deploy NAME`.``

With `--deploy`, Geass deploys the service when it saves it, and the command prints `Created NAME and started
deploying it`. Follow it with `geass status NAME`, or with `geass deploy NAME --wait` after it exists. Geass checks the
deploy before it saves anything. A cluster with no room for the service refuses the command and leaves no draft. The
command prints `Created NAME and started deploying it` only when the answer says that Geass deployed the service. A
Geass older than the command line ignores `--deploy` and saves a draft. The command then exits `1`. It says that it
did not deploy the service, and that you must run `geass deploy NAME`.

To create a service from a repository, the project must also be linked to GitHub. You link it in the dashboard. See
[GitHub and builds](/docs/guides/github-and-builds/). With `--deploy`, a missing link is an error. Geass accepts a
draft without a link.

**API calls:** `POST /apps` with these fields:

- `project`, `environment`, and `name`.
- Either `draft` (without `--deploy`) or `deploy` (with it).
- `source`: `image`, `git`, or `function`.
- `image`, or `repository` and `branch`, or `runtime`, `code`, and `mode`.
- `port`.

**Needs:** Full access.

### `geass apps delete`

Deletes a service.

```text
geass apps delete NAME [-y]
```

Asks `Delete app "NAME"? This cannot be undone. [y/N]` on standard error, and reads the answer from standard input.
Any answer other than `y` or `yes` cancels the delete (`cancelled`, exit 1). The command might not be able to ask
anybody, because standard input is not a terminal. Then the command refuses with
`this deletes data; run it again with --yes to confirm` (exit 2). `--yes` skips the question. Prints `Deleted NAME.`

There is no `delete` command for databases, buckets, or projects. Use `geass api DELETE`. See
[`geass api`](#geass-api).

**API calls:** `DELETE /apps/{name}?confirmName={name}`. **Needs:** Full access.

### `geass metrics`

Shows CPU, memory, network, and request use over a window of time.

```text
geass metrics NAME [--window WINDOW] [--kind KIND]
```

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--window` | string | `1h` | How far back: `15m`, `1h`, `6h`, or `24h`. |
| `--kind` | string | `apps` | What `NAME` is: `apps`, `databases`, or `projects`. |

Prints one row per series, to three significant figures. Each row has the latest value, and in brackets the lowest and
highest values in the window:

```text
SERIES            LATEST (MIN - MAX)
cpu               0.0123 (0.0101 - 0.0456)
memory            1.34e+08 (1.3e+08 - 1.36e+08)
memoryPerReplica  6.7e+07 (6.5e+07 - 6.9e+07)
network           2.1e+03 (1.2e+03 - 9.8e+03)
networkIn         1.7e+03 (900 - 8.4e+03)
requests          4.2 (1.1 - 9.7)
```

The series have these units:

- `cpu` is in cores.
- `memory` is in bytes, the total of every replica.
- `network` (sent) and `networkIn` (received) are in bytes per second.
- `requests` (services only) is in requests per second.
- A service also has `memoryPerReplica`, in bytes. It is the memory of the service's busiest replica, which is what
  **Memory reserved** is set against.
- Databases add `storage`, the used space of their volumes, in bytes.

With `--json`, it prints the answer unchanged: `window`, `step`, `available`, an optional `message`, and `series`. Each
series is a list of `{"t": <unix seconds>, "v": <value>}`.

Metrics need Prometheus. When Prometheus is not installed or not reachable, the command exits `1` with
`no metrics yet: ...` and the reason. With `--json`, it prints `"available": false` and exits `0`. See
[Observability](/docs/guides/observability/).

**API calls:** `GET /{kind}/{name}/metrics?window=...`. **Needs:** any signed-in token for services. A project token
can read only its own services. Any workspace token for `databases` and `projects`.

## Functions

### `geass functions`

Schedules and calls functions. Also `geass function` and `geass fn`. A function in event mode is a handler. It gets an
event for each request and returns the response. A function in http mode is a program that serves HTTP itself. A
trigger calls the function on a schedule, and first wakes it when it is asleep. See [Functions](/docs/guides/functions/).

#### `geass functions triggers list`

```text
geass functions triggers list APP
```

Prints `NAME`, `SCHEDULE (UTC)`, and `PATH` for each schedule. The path shows `/` when no path was set. With `--json`,
it prints the function: `runtime`, `mode`, `triggers`, and `url`. `triggers` is also `geass functions trigger`.

**API calls:** `GET /apps/{name}`. **Needs:** any signed-in token. The command fails with `APP is not a function` for
any other service.

#### `geass functions triggers add`

```text
geass functions triggers add APP NAME SCHEDULE [--path PATH] [--payload JSON]
```

Adds a schedule, or replaces the one with the same name. The other schedules are kept.

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--path` | string | `/` | The path to call. Must start with `/`. |
| `--payload` | string | none | The JSON body to send. Must be valid JSON. |

`NAME` is lowercase letters, numbers, and dashes, up to 32 characters. It starts and ends with a letter or number. A
function can have 8 schedules. `SCHEDULE` is one of these values:

- A cron expression in UTC with five fields of numbers, ranges, steps, and lists. The command does not accept names
  such as `MON`.
- `@hourly`, `@daily`, `@midnight`, `@weekly`, `@monthly`, `@yearly`, or `@annually`.

Each run is a `POST` to the function. A schedule applies when you next deploy the function. So the command prints
``APP will call NAME on schedule "..." once it is deployed. Deploy it with `geass deploy APP`.``

```bash
geass functions triggers add report nightly "0 3 * * *"
geass functions triggers add report sync "*/15 * * * *" --path /sync --payload '{"full":true}'
geass deploy report
```

**API calls:** `GET /apps/{name}`, then `PATCH /apps/{name}` with `{"functionTriggers": [...]}` (the whole list).
**Needs:** Full access or a project token.

#### `geass functions triggers remove`

```text
geass functions triggers remove APP NAME
```

Also `geass functions triggers rm`. Stops calling the function on that schedule after you deploy it. Prints `Removed
NAME. It stops once APP is deployed.` A name that does not exist is a usage error (exit 2) and changes nothing.

**API calls:** `GET /apps/{name}`, then `PATCH /apps/{name}` with `{"functionTriggers": [...]}` (an empty string when
the last schedule goes). **Needs:** Full access or a project token.

#### `geass functions invoke`

Calls a function at its public address, as any visitor would, and prints the answer.

```text
geass functions invoke APP [--method METHOD] [--path PATH] [--data JSON]
```

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--method` | string | `GET`, or `POST` when there is `--data` | The HTTP method. |
| `--path` | string | `/` | The path to call, with an optional query. The command joins it to the function's address. |
| `--data` | string | none | The JSON body to send. |

The status line `HTTP 200` goes to standard error, and the body goes to standard output. A status of 400 or above also
exits `1`. With `--json`, it prints `{"status": 200, "body": "..."}` and exits `0` whatever the status. The call has no
Geass token, so the function's own rules apply. The call wakes a sleeping function, so the first call can take a few
seconds. The call times out after 2 minutes. If the function has no public address, the command stops with `the function has
no public address to call: give it a domain first`.

**API calls:** `GET /apps/{name}`, then an HTTP request to the function's URL. **Needs:** any signed-in token.

## Data and cluster

### `geass projects`

Lists and inspects projects. Also `geass project`.

```text
geass projects list
geass projects get NAME
```

`list` (also `ls`) prints the same table as `geass apps list`. The `NAME` column is the project's ID, such as
`proj-1a2b3c4d`, and the `DETAIL` column lists its environments. The table has no column for the display name. To read
the display name, use `geass projects get NAME`. `get` prints `resource`, `spec`, and `status` as JSON.

**API calls:** `GET /projects`, `GET /projects/{name}`. **Needs:** any workspace token.

#### `geass projects quota`

Caps what a project's environments, and the project as a whole, can ask for. A project with no limits can ask for
everything that the cluster has. See [Project limits](/docs/guides/project-limits/).

```text
geass projects quota show PROJECT
geass projects quota set PROJECT [--cpu Q] [--memory Q] [--storage Q] [--pods N]
                                 [--project-cpu Q] [--project-memory Q] [--project-storage Q] [--project-pods N]
geass projects quota clear PROJECT
```

`show` prints a table of the limits for each environment and for the whole project. The table also shows what the
project uses of its whole-project limits. With `--json`, `show` prints the same as JSON. `set` changes only the limits
that you name. A limit that you leave out keeps its value. A limit that you give empty (`--memory ''`) is removed.
`clear` removes every limit. `set` and `clear` print the limits as they are afterwards.

| Flag | Type | Meaning |
| --- | --- | --- |
| `--cpu`, `--memory`, `--storage`, `--pods` | string | The most that one environment's pods can ask for in CPU (such as `4` or `500m`), memory (`8Gi`), disk (`50Gi`), and pods |
| `--project-cpu`, `--project-memory`, `--project-storage`, `--project-pods` | string | The most that the whole project can ask for, added up over its environments |

```bash
geass projects quota set shop --project-cpu 8 --project-memory 16Gi --cpu 4
geass projects quota set shop --memory ''
```

**API calls:** `GET /projects/{name}/quota`, and `POST /projects/{name}/quota` for `set` and `clear`. **Needs:** any
workspace token to show, **Full access** to change.

#### `geass projects network`

Sets who can reach a project's services, and whether the services are kept away from the machines that the cluster
runs on. See [Network isolation](/docs/guides/network-isolation/).

```text
geass projects network show PROJECT
geass projects network set PROJECT [--isolation project|none] [--allow-from PROJECT,...] [--block-nodes[=false]]
```

`set` changes only what you name, and leaves the rest as it was. It reads the current settings first, because the API
takes the full list of who can reach a project each time.

| Flag | Type | Meaning |
| --- | --- | --- |
| `--isolation` | string | `project` (also `private`) lets in only the project itself, the platform, and public domains. `none` (also `open`) lets in every service in the cluster |
| `--allow-from` | list of strings | Other projects that can reach this one. An empty value lets in none |
| `--block-nodes` | boolean | Keep the project's services away from the addresses of the cluster's machines. `--block-nodes=false` allows it |

```bash
geass projects network set shop --block-nodes
geass projects network set shop --allow-from billing,search
```

**API calls:** `GET /projects/{name}/network`, and `POST /projects/{name}/network` for `set`. **Needs:** any workspace
token to show, **Full access** to change.

### `geass databases`

Lists databases and shows how to connect to them. Also `geass database`, `geass dbs`, and `geass db`.

```text
geass databases list
geass databases get NAME
geass databases connection NAME [--reveal]
```

`list` (also `ls`) and `get` work as for projects. The `DETAIL` column of `list` is the database engine.

`connection` shows how to reach a database: `database`, `host`, `port`, and `username`, whichever the database has.

```text
database   orders
host       orders-db.example.svc
port       5432
username   app
```

The command hides the password and the connection string, and a note on standard error says so. `--reveal` adds
`password` and `uri`, and prints everything as JSON, with `"revealed": true`. The operator log records each reveal, with
the database name and the user who asked.

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--reveal` | boolean | `false` | Include the password and the connection string. |

**API calls:** `GET /databases`, `GET /databases/{name}`, and `GET /databases/{name}/connection` (with `?reveal=true`
for `--reveal`). **Needs:** any workspace token for `list` and `get`. Full access for `connection`, with or without
`--reveal`. Until the database is ready, the command fails with
`no connection details yet: the resource has not finished setting up`.

### `geass stores`

Lists object storage buckets. Also `geass buckets` and `geass object-stores`.

```text
geass stores list
geass stores get NAME
```

`list` (also `ls`) and `get` work as for projects, with one difference. `DETAIL` shows the bucket's message, if there is
one. For projects and databases, `DETAIL` shows their environments or engine. There is no command for the connection
details of a bucket.

**API calls:** `GET /object-stores`, `GET /object-stores/{name}`. **Needs:** any workspace token.

### `geass nodes`

Lists the cluster's nodes, tags them, and cordons them. Also `geass node`.

#### `geass nodes list`

```text
geass nodes list
```

Also `geass nodes ls`. Prints these columns:

- `NAME`, `STATUS` (`Ready`, `NotReady`, or `Ready, cordoned`), `REGION`, `ZONE`, `TAGS`, `CPU`, and `MEMORY`.
- `POOL`: the [pool](/docs/guides/node-pools/) that the node belongs to.
- `EPHEMERAL`: `yes` when the node keeps data off itself. Databases and volumes never run there, and the pool might
  remove the node when the load goes.

A reserved tag shows as `gpu (reserved)`, and a compliance tag as `hipaa (compliance)`.

```text
NAME         STATUS    REGION  ZONE  TAGS                CPU  MEMORY  POOL   EPHEMERAL
worker-1     Ready     eu      -     -                   4    8Gi     -      -
worker-2     Ready     eu      fsn1  hipaa (compliance)  4    8Gi     -      -
burst-x7k2m  Ready     eu      fsn1  -                   2    3Gi     burst  yes
worker-3     NotReady  -       -     -                   -    -       -      -
edge-1       Failed    -       -     -                   -    -       -      -

edge-1: ssh dial 198.51.100.7:22: connect: connection refused
  Fix it, then run `geass servers retry edge-1`.
```

A server that has not joined yet, or failed to, has no node. It is a row with its phase as `STATUS` (`Pending`, `Creating`,
`Joining`, or `Failed`), and the error is under the table.

With `--json`, it prints `{"items": [...], "servers": [...]}`. Each node has `name`, `ready`, `schedulable`,
`controlPlane`, `region`, `zone`, `tags`, `cpu`, and `memory`, and `pool`, `ephemeral`, and `cordoned` when they apply.
`servers` lists the servers that have not joined, in the format of [`geass servers list`](#geass-servers-list).

**API calls:** `GET /nodes`, `GET /servers`. **Needs:** any workspace token. A project token gets `403`.

#### `geass nodes cordon` and `geass nodes uncordon`

Stops new services on a node, or allows them again.

```text
geass nodes cordon NODE
geass nodes uncordon NODE
```

What runs on the node stays. The command refuses to cordon the last node that can start new services. It refuses to
uncordon a node that Geass cordoned to upgrade it or to empty it, because Geass uncordons that node itself. A cordoned node
does not count for [high availability](/docs/operations/high-availability/#check-readiness).

**API calls:** `POST /nodes/{name}/cordon`, `POST /nodes/{name}/uncordon`. **Needs:** Full access.

#### `geass nodes tag`

Sets a node's region, zone, and tags.

```text
geass nodes tag NODE [--region REGION] [--zone ZONE] [--tags TAG,...] [--reserved TAG,...] [--compliance TAG,...]
```

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--region` | string | empty | The region, such as `eu`. |
| `--zone` | string | empty | The site inside the region. |
| `--tags` | list of strings | none | Tags that the node carries. Comma separated, or repeat the flag. |
| `--reserved` | list of strings | none | Tags to reserve the node for. All other work stays off the node. Implies the tag. |
| `--compliance` | list of strings | none | Compliance tags, such as `hipaa`. Always reserved, and required by projects that need them. Implies the tag. |

This command replaces everything that the node had. The command removes a region, zone, or tag that you leave out. A
name is lowercase letters, numbers, and hyphens, up to 40 characters. A node can have 16 tags. The command prints
`Updated NODE. Running
workloads are not moved: one that restarts only returns to a node that matches.` Then it prints each warning that the
API returns on standard error, as `warning: ...`. The warnings say what the change leaves to do or to watch for. For
example:

- A tag is now a compliance tag, but work that does not belong is still running on the node.
- A project has no node left to restart on.

A change with nothing to warn about prints no warnings.

```bash
geass nodes tag worker-2 --region eu --tags hipaa --compliance hipaa
```

**API calls:** `POST /nodes/{name}/placement` with `region`, `zone`, `tags`, `reserved`, and `compliance`, each tag
list as a comma-separated string. **Needs:** Full access. See [Compliance tags](/docs/guides/compliance-tags/) and
[Scaling and placement](/docs/guides/scaling-and-placement/).

### `geass compliance`

Requires node tags, such as `hipaa`, for everything that a project runs. See
[Compliance tags](/docs/guides/compliance-tags/).

#### `geass compliance show`

```text
geass compliance show PROJECT
```

Prints the tags that the project requires, and where the project falls short.

```text
proj-1a2b3c4d requires nodes tagged: hipaa
DATABASE  COMPLIANCE
orders    missing hipaa

These nodes carry a required tag without being reserved for it: n3
```

A project that requires nothing prints `PROJECT requires no node tags.` With `--json`, it prints `requiredTags`,
`region`, `databases` (each with `name`, `compliant`, and `missing`), and `openNodes`.

**API calls:** `GET /projects/{name}/compliance`. **Needs:** any workspace token.

#### `geass compliance set`

```text
geass compliance set PROJECT TAG...
```

Requires these node tags for everything in the project. It replaces the current tags. A project can require 8 tags.
The API refuses tags that are not compliance tags on the nodes that carry them. It also refuses tags that a database's
nodes do not have. Prints `PROJECT now requires: hipaa, pci`.

#### `geass compliance clear`

```text
geass compliance clear PROJECT
```

Stops requiring node tags for a project. Prints `PROJECT no longer requires node tags.`

**API calls (set and clear):** `POST /projects/{name}/compliance` with `{"requiredTags": "hipaa,pci"}`, or an empty
string for `clear`. **Needs:** Full access.

### `geass addons`

Turns the cluster add-ons on and off, and shows how each one is doing. The add-ons are `serverless` (sleep when idle),
`longhorn` (replicated storage), `monitoring` (metrics), and `logging` (logs). See
[Sleep when idle](/docs/guides/scale-to-zero/), [Storage](/docs/guides/storage/), and
[Observability](/docs/guides/observability/#change-the-add-ons).

#### `geass addons list`

```text
geass addons [list]
```

Prints one row for each add-on: its name, whether it is on, its state (`Off`, `Installing`, `Ready`, or `Failed`), and
details. For an add-on that is not ready, the details say what it waits for or why it failed. For a stuck add-on, they say
to run `geass addons retry`. With `--json`, it prints the API answer.

**API calls:** `GET /settings/addons`. **Needs:** Full access.

#### `geass addons enable`

```text
geass addons enable ADD-ON [--profile lite|full] [--retention-days N] [--wait] [--timeout 15m]
```

Turns an add-on on. `--profile` is for `monitoring`, and `--retention-days` is for `logging`. With `--wait`, the command
shows each change of state on the error stream and ends when the add-on is `Ready`. It stops with an error when the add-on is
`Failed`, or when `--timeout` ends.

**API calls:** `POST /settings/addons/{name}/save` with `{"enabled": true}`, and `GET /settings/addons` with `--wait`.
**Needs:** Full access.

#### `geass addons disable`

```text
geass addons disable ADD-ON
```

Turns an add-on off. Turning `monitoring` or `logging` off removes it and what it kept, so the command asks first. In a script,
add `--yes`. Turning `serverless` off leaves KEDA installed. `longhorn` cannot be turned off, because Geass never removes
it and databases keep their data on it.

**API calls:** `POST /settings/addons/{name}/save` with `{"enabled": false}`. **Needs:** Full access.

#### `geass addons set`

```text
geass addons set ADD-ON [--profile lite|full] [--retention-days N]
```

Changes an option of an add-on, and does not turn it on or off.

**API calls:** `POST /settings/addons/{name}/save`. **Needs:** Full access.

#### `geass addons retry`

```text
geass addons retry ADD-ON [--wait] [--timeout 15m]
```

Starts the install of a stuck add-on over. The operator deletes the install job, and K3s makes a new one. The command
works only for an add-on that is `Failed` because its install job failed or is suspended.

**API calls:** `POST /settings/addons/{name}/retry`. **Needs:** Full access.

## Clouds and machines

These commands make Geass create and delete machines at a cloud, which costs money. Every command that spends or
changes something needs a **Full access** token. A project token cannot reach any of these commands. They are
experimental, like the clouds behind them. See
[Cloud connections for machines](/docs/guides/connectors/clouds/#how-far-to-trust-it).

### `geass providers`

Lists the clouds that Geass can create machines at, and what each cloud takes. Also `geass clouds`.

```text
geass providers [NAME]
```

Without a name, it prints these columns:

- `ID` and `NAME`.
- `CREDENTIALS`. A secret is marked `secret`, and a credential that you can leave out is marked `optional`.
- `SETTINGS`: the settings that a pool can give the cloud.

With a name, such as `geass providers Hetzner`, it describes the cloud. Case does not matter. The description gives
what the credentials should be, what a region means there, and each credential and setting with its help text. The
command marks a credential of several lines as one to give with `--credential-file`.

**API calls:** `GET /compute/providers`. **Needs:** any workspace token.

### `geass sizes`

Lists the machine sizes that a cloud offers in a region, cheapest first.

```text
geass sizes --connection NAME [--region REGION] [--min-vcpus N] [--min-memory GIB] [--limit N]
```

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--connection` | string | none | The connection to ask the cloud with. Required. |
| `--region` | string | the connection's | The cloud's region or location. |
| `--min-vcpus` | integer | none | Only sizes with at least this many processors. |
| `--min-memory` | number | none | Only sizes with at least this much memory, in GiB. |
| `--limit` | integer | all | Show at most this many sizes. |

Prints `SIZE`, `VCPUS`, `MEMORY`, `DISK`, `ARCH`, and `PER HOUR`. `PER HOUR` is the cloud's list price for an hour, or
`-` where the cloud states none. A size's name is what a pool's `--size` takes. With `--json`, it prints
`{"items": [...]}`.

```bash
geass sizes --connection hetzner --region fsn1
geass sizes --connection aws --region eu-north-1 --min-vcpus 4 --min-memory 8 --limit 10
```

**API calls:** `GET /compute/sizes?connection=&region=`. **Needs:** Full access, because it calls the cloud with the connection's
credentials.

### `geass connections`

Connects clouds, so that Geass can create machines there. Also `geass connection` and `geass conn`.

```text
geass connections list
geass connections show NAME
geass connections add NAME --provider CLOUD [--region REGION] [--purpose PURPOSE]... [--credential KEY=VALUE]... [--credential-file KEY=PATH]...
geass connections update NAME [--region REGION] [--purpose PURPOSE]... [--credential KEY=VALUE]... [--credential-file KEY=PATH]...
geass connections remove NAME
geass connections verify NAME
```

`list` (also `ls`) prints these columns:

- `NAME`, `PROVIDER`, `PURPOSES`, and `REGION`.
- `STATUS`: `accepted`, `not accepted`, or `not checked yet`.
- `USED BY`: the pools and machines that depend on the connection.
- `AGE`.

`show` prints the connection with the cloud's message, the account, and when the cloud last accepted it. It also prints
when the credentials expire, each warning, and what uses the connection.

`add` does these steps:

1. It stores the credentials in a Secret.
2. It asks the cloud immediately whether the cloud accepts them.
3. It waits a few seconds for the answer.
4. It prints the answer with the cloud's warnings.

`add` fails with the cloud's message when the cloud refuses the credentials. `update` changes only what you name. After
`update` replaces credentials, it asks the cloud again. `verify` asks again. `remove` asks you to confirm (`--yes` skips
the question). It refuses while a pool or a machine uses the connection, so that Geass can still delete those machines
at the cloud. It does not revoke the credentials at the cloud.

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--provider` | string | none | `add` only. The cloud, as `geass providers` lists it. Case does not matter. Required. |
| `--region` | string | none | The connection's default region, used when a pool or a server names none. On `update`, an empty value clears it. |
| `--purpose` | list of strings | `Compute` | What it is for: `Compute`, and for AWS also `Storage` and `Database`. `add` makes connections that create machines, so `Compute` must be one of them. On `update`, the list replaces the old one. |
| `--credential` | list of `KEY=VALUE` | none | A credential that is not secret. A secret given this way stays in your shell's history, and the command says so. |
| `--credential-file` | list of `KEY=PATH` | none | A credential read from a file (`-` reads standard input). The command drops a trailing newline. |

A credential can also come from the environment. The variable name is `GEASS_CREDENTIAL_` and the key in capitals,
with an underscore at each capital inside the key. For example, `GEASS_CREDENTIAL_TOKEN` or
`GEASS_CREDENTIAL_ACCESS_KEY_ID`. The command refuses a key that the cloud does not take, and no value appears in an
error. When a person is at the keyboard, the command asks for a required credential that nobody gave, and does not
show it. A credential of several lines is an exception: it must come from a file.

```bash
geass connections add hetzner --provider Hetzner --credential-file token=./hetzner-token.txt
GEASS_CREDENTIAL_TOKEN=$(pass hetzner/geass) geass connections add hetzner --provider Hetzner
geass connections add aws-compute --provider AWS --region eu-north-1 \
  --credential accessKeyId=AKIA... --credential-file secretAccessKey=./secret.txt
geass connections update hetzner --credential-file token=./new-token.txt
```

**API calls:**

- `GET /connections` and `GET /connections/{name}`.
- `POST /connections` for `add`, `PATCH /connections/{name}` for `update`,
  `DELETE /connections/{name}?confirmName={name}` for `remove`, and `POST /connections/{name}/verify` for `verify`.
- With each of these, `GET /compute/providers`. After a change, reads of the connection to wait for the cloud's answer.

**Needs:** any workspace token to list and show, **Full access** to change. You make a connection for buckets or
databases in the dashboard. See [Cloud connections for machines](/docs/guides/connectors/clouds/).

### `geass pools`

Pools of nodes that Geass adds and removes when the load needs it. Also `geass pool`. See [Node pools and autoscaling](/docs/guides/node-pools/).

```text
geass pools list
geass pools show NAME
geass pools create NAME --max N (--connection CLOUD --size SIZE | --inventory-file FILE) [flags]
geass pools update NAME [flags]
geass pools delete NAME
geass pools resume NAME
```

`list` (also `ls`) prints these columns:

- `NAME`.
- `SOURCE`: the connection and its cloud, or `inventory (N)`.
- `SIZE`, `READY/NODES`, and `MIN-MAX`.
- `AUTOSCALING`: `on`, `off`, or `suspended`.
- `PHASE`.
- `PER HOUR`: what its machines cost now, at the cloud's list price.
- `AGE`.

`show` prints the settings, what the pool is doing and why, its pending pods, its cost, and the last failure. It also
prints any machines that no node owns, and a table of its nodes with their phase, address, size, region, age, and
message.

`create` needs `--max` and a source, and refuses to run without them:

- `--max` is the most nodes that the pool can ever have, and the cap on what the pool costs.
- The source is `--connection` and `--size` for a cloud, or `--inventory-file` for servers that you own.

When `--max` is above `--min`, `create` also needs `--autoscale` or `--no-autoscale`. A pool that does not autoscale
keeps exactly `--min` nodes. `update` sends only the flags that you give. The size, region, image, and settings apply
to machines made from then on. `delete` asks you to confirm (`--yes` skips the question). Then it deletes the pool's
nodes and their machines, and tells you to check the cloud. `resume` ends the pause of a pool that stopped after three
failed machines in a row, and forgets those failures.

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--connection` | string | none | The connection that creates the machines. On `update`, another connection for machines made from now on. |
| `--inventory-file` | path | none | Your own servers instead of a cloud: a JSON list of `{"name", "address", "sshUser", "sshPort"?, "sshKeySecret"?}` (`-` reads standard input). An unknown field is an error. |
| `--inventory-key-file` | path | none | The SSH private key of those servers, stored as a Secret. Use it with `--inventory-file`. |
| `--region` | string | the connection's | The cloud's region or location. |
| `--size` | string | none | The cloud's name for a machine size. Required for a cloud. |
| `--fallback-size`, `--fallback-region` | list of strings | none | Sizes and regions to try, in order, when the cloud has no room. Up to five each. Empty clears them. |
| `--image` | string | the cloud's Ubuntu 24.04 | The operating system image. |
| `--spot`, `--no-spot` | switch | off | Use the cloud's spot machines, which the cloud might take back. |
| `--address-type` | string | `public` | `public` or `private`: the address of a machine that Geass uses to reach it. |
| `--setting` | list of `KEY=VALUE` | none | A setting of the cloud, such as `firewallId=42`. On `update`, the other settings keep their values, and `KEY=` removes a setting. |
| `--storage` | string | `none` | `none` keeps databases and volumes off the nodes. `allow` makes them ordinary workers. |
| `--min`, `--max` | integer | 0 and none | The fewest and the most nodes, each 0 to 200. A pool that does not autoscale has exactly `--min`. |
| `--autoscale`, `--no-autoscale` | switch | none | Follow the load between `--min` and `--max`, or keep exactly `--min`. |
| `--suspend`, `--no-suspend` | switch | off | Stop adding and removing nodes, or let the pool add and remove them again. |
| `--scale-up-delay` | integer | 30 | Seconds that a pod must be unable to start before the pool adds a node. |
| `--max-scale-up` | integer | 5 | The most nodes that the pool adds in one step. |
| `--scale-down-utilization` | integer | 50 | A node used less than this percent is a candidate for removal. |
| `--scale-down-after` | integer | 10 | Minutes that a node must be a candidate before the pool removes it. |
| `--scale-down-delay-after-add` | integer | 10 | After the pool adds a node, it removes no node for this many minutes. |
| `--min-node-lifetime` | integer | 0 | The minimum minutes that a node lives, for clouds that bill each started hour. |
| `--spare-nodes` | integer | 0 | Empty nodes kept ready for a burst. Each one costs money while it waits. |
| `--placement-region`, `--placement-zone` | string | none | The region and site that the nodes carry. |
| `--tag`, `--reserved-tag` | list of strings | none | Node tags that the nodes carry. A reserved tag also keeps all other work off them. A change to the placement replaces the region, site, and tags together, so name every tag. |
| `--orphans` | string | `report` | `report` or `delete`: what to do with a machine at the cloud that no node owns. |

```bash
geass pools create burst --connection hetzner --region fsn1 --size cx23 --fallback-size cx33 --min 0 --max 5 --autoscale
geass pools create fixed --connection hetzner --size cx23 --min 2 --max 2 --no-autoscale
geass pools create spare --inventory-file ./servers.json --inventory-key-file ~/.ssh/geass_ed25519 --max 3 --autoscale
geass pools update burst --max 8
geass pools update burst --suspend
geass pools update burst --setting firewallId=77 --fallback-size ''
geass pools resume burst
```

**API calls:**

- `GET /pools` and `GET /pools/{name}`.
- `POST /pools` for `create`.
- `PATCH /pools/{name}` for `update`. When a change to the settings or placement needs the current values, the command
  reads the pool first.
- `DELETE /pools/{name}?confirmName={name}` for `delete`.
- `POST /pools/{name}/resume` for `resume`.

**Needs:** any workspace token to list and show, **Full access** to change.

### `geass servers`

Adds one server to the cluster, shows why one did not join, retries it, or removes it. Also `geass server`. See [Add servers](/docs/getting-started/add-servers/).

```text
geass servers list
geass servers add [NAME] (--connection CLOUD --size SIZE | --address ADDRESS --ssh-user USER (--ssh-key-file FILE | --ssh-key-secret SECRET)) [flags]
geass servers retry NAME
geass servers update NAME --known-hosts-file FILE
geass servers remove NAME
```

#### `geass servers list`

Lists the servers that Geass joins, whether they came from `geass servers add`, from a pool, or with the cluster. Also
`geass servers ls`. It prints `NAME`, `ROLE` (`worker` or `control-plane`), `STATUS`, `ADDRESS`, `SOURCE` (`cloud` or
`existing`), and `POOL`. `STATUS` is `Pending`, `Creating`, `Joining`, `Ready`, or `Failed`, and `Ready, cordoned` for a
server that takes no new work. Under the table, it prints the error of each server that is not ready. With `--json`, it
prints `{"items": [...]}`.

**API calls:** `GET /servers`. **Needs:** any workspace token. A token that cannot read logs does not get the errors.

#### `geass servers retry`

Starts a server that failed to join over. Fix the cause first. The command refuses a server that has not failed.

**API calls:** `POST /servers/{name}/retry`. **Needs:** Full access.

#### `geass servers update`

Replaces the host keys that Geass accepts from a server that you have, for example after you rebuilt the machine.

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--known-hosts-file` | path | none | The lines that `ssh-keyscan` prints for the server. `-` reads standard input. Required. |

Only the lines for the address of this server change. The command refuses host keys that list no key for that address. It
refuses a machine that Geass created.

```bash
ssh-keyscan -t ed25519 198.51.100.7 | geass servers update edge-1 --known-hosts-file -
```

**API calls:** `POST /servers/{name}/host-keys` with `knownHosts`. **Needs:** Full access.

#### `geass servers add` and `geass servers remove`

With `--connection` and `--size`, Geass creates a machine at the cloud. The machine costs money until you remove the
server. If you give no name, the command makes one up. With `--address`, the server is one that you have. A name is
required, and Geass joins the server over SSH as `--ssh-user`.

`remove` (also `rm` and `delete`) asks you to confirm (`--yes` skips the question). Then it moves the server's
services elsewhere if there is room, and deletes a machine that Geass made for the server. It refuses a node that
belongs to a pool, and the control plane.

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--connection`, `--region`, `--size`, `--image` | string | none | For a machine at a cloud: the connection, its region, the size, and the image. |
| `--setting` | list of `KEY=VALUE` | none | For a machine at a cloud: a setting of the cloud. |
| `--address` | string | none | For a server that you have: where Geass reaches it over SSH. |
| `--ssh-user` | string | none | For a server that you have: `root`, or a user that can use `sudo` without a password. |
| `--ssh-port` | integer | 22 | For a server that you have. |
| `--ssh-key-file` | path | none | For a server that you have: the private key, stored as a Secret named `NAME-ssh` (`-` reads standard input). |
| `--ssh-key-secret` | string | none | For a server that you have: a Secret that already holds the key. |
| `--known-hosts-file` | path | none | For a server that you have: its host keys, to pin it. |
| `--placement-region`, `--placement-zone`, `--tag`, `--reserved-tag` | string, list | none | The region, site, and tags that the node carries. |
| `--storage` | string | `allow` | `allow` or `none`: whether databases and volumes can run on it. |

```bash
geass servers add --connection hetzner --region fsn1 --size cx23
geass servers add edge-1 --address 198.51.100.7 --ssh-user root --ssh-key-file ~/.ssh/geass_ed25519 --tag edge
geass servers remove edge-1
```

**API calls:** `POST /servers`, `DELETE /servers/{name}?confirmName={name}`. **Needs:** Full access.

## Dashboard users

### `geass users`

Lists, adds, changes, and removes the users who sign in to the dashboard with a password. Also `geass user`. These
commands do the same as **Settings** → **Users**. See [Manage users](/docs/guides/users-and-tokens/#manage-users).

```text
geass users list
geass users add USERNAME [--role admin|viewer] [--password-file FILE]
geass users set-role USERNAME admin|viewer
geass users reset-password USERNAME [--password-file FILE]
geass users remove USERNAME
geass users sign-out-all
```

`add` and `reset-password` print a password that Geass makes, one time, on standard output. Geass cannot show the
password again. With `--password-file`, the command reads the password from the file (`-` reads standard input) and
prints nothing. A password has 12 to 72 characters. No flag takes the password as an argument, because the process list
and the shell history show arguments.

`remove` (also `rm` and `delete`) and `sign-out-all` ask you to confirm (`--yes` skips the question). `sign-out-all`
ends every dashboard session, yours too. API tokens, including the token of the command line, keep working. Geass
refuses to remove the last admin or to make the last admin a viewer.

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--role` | string | `viewer` | For `add`: `admin` or `viewer`. |
| `--password-file` | path | none | For `add` and `reset-password`: read the password from this file (`-` reads standard input). |

```bash
geass users add maria --role viewer
printf '%s' "$PASSWORD" | geass users add ops --role admin --password-file -
geass users set-role maria admin
geass --yes users remove maria
```

**API calls:** `GET /users`, `POST /users`, `POST /users/{name}/role`, `POST /users/{name}/password`,
`DELETE /users/{name}`, `POST /users/sign-out-all`. **Needs:** Full access with an install-wide token. A project token
or an organization token cannot manage users. A token manages viewers only: it cannot make an admin, make a viewer an
admin, or set the password of an admin. Geass answers `403` `a token can manage viewers only`. Do these in the dashboard.

## Raw API and agents

### `geass api`

Calls any `/api/v1` endpoint and prints the JSON it answers.

```text
geass api METHOD PATH [-d BODY] [-q KEY=VALUE]...
```

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `-d`, `--data` | string | none | The JSON body: a string, `@file` to read a file, or `-` to read standard input. The command refuses a body that is not valid JSON before it sends it. |
| `-q`, `--query` | list of strings | none | A query parameter, `key=value`. Repeat the flag for more. |

`METHOD` is any HTTP method, in any case. `PATH` is under `/api/v1`, such as `/apps`. `PATH` can carry a query string
of its own, and the command adds the `-q` values to it. The output is always JSON, or `null` when the API answers with
nothing. An error exits with the exit codes that this page lists. The command reads the whole answer before it
prints it. So it does not work for endpoints that stream, such as `/events` and the `logs/stream` routes.
`geass openapi` lists every endpoint.

```bash
geass api GET /me
geass api GET /apps -q project=proj-1a2b3c4d -q environment=production
geass api POST /apps/web/scale -d '{"replicas": 3}'
geass api GET /apps/web/deployments
geass api DELETE /databases/orders -q confirmName=orders
geass api POST /databases/restore -d '{"archive": "orders-backup-1a2b3c"}'
```

A `DELETE` needs `confirmName` set to the resource's name. A restore needs the name of the backup archive. You find
that name in the dashboard. See [Backups and restore](/docs/guides/backups-and-restore/).

**API calls:** the one you give. **Needs:** whatever that endpoint needs.

### `geass openapi`

Prints the OpenAPI 3 description of the API as JSON.

```text
geass openapi
```

Each operation carries an `x-access` value that says who can call it: `public`, `viewer`, `observer`, `project`, or
`admin`.

**API calls:** `GET /openapi.json`. **Needs:** any workspace token.

### `geass mcp`

Runs an MCP server, so that AI agents can deploy and monitor services. With an admin token, it can also look at the
clouds that Geass uses, and run pools of nodes.

```text
geass mcp [--read-only] [--allow-delete] [--listen ADDRESS]
```

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--read-only` | boolean | `false` | Offer only tools that change nothing, whatever the token can do. An admin token still gets `database_connection` and `cloud_sizes`, which only read. |
| `--allow-delete` | boolean | `false` | Also offer the tools that permanently delete resources: `delete_resource` and `delete_node_pool`. |
| `--listen` | string | none | Serve MCP over HTTP at this address, such as `127.0.0.1:8765`, instead of standard input and output. |

Without `--listen`, it speaks MCP over standard input and output as the saved login, or as `--token` or `$GEASS_TOKEN`.
With `--listen`, each agent sends its own Geass token as a Bearer token, and the command needs only an address.
Everything meant for a person goes to standard error. See [MCP server](/docs/guides/mcp-server/) and
[MCP tools](/docs/reference/mcp-tools/).

**API calls:** `GET /me`, then whatever the tools call. **Needs:** the token decides which tools exist.

## Built-in commands

The command line is built on [Cobra](https://cobra.dev), which adds these commands:

| Command | Meaning |
| --- | --- |
| `geass help [command]` | Help for any command. |
| `geass completion bash`, `zsh`, `fish`, or `powershell` | Print a shell completion script. |
| `geass --version`, `geass -v` | Print `geass version <version>`. |

## Configuration values are visible

:::note
Store anything sensitive as a secret. Plain configuration (`geass env set` without `--secret`) is not secret, and the
API returns it. The API returns secrets by name only, and `spec.secretData` in `geass apps get` has its values emptied.
But `geass env list --json` and `geass apps get` (with a Full access token) print plain configuration values
(`config` and `spec.configData`). Also, `spec.env` lists literal environment variables with their values.
`geass env list` without `--json` prints names only. The help of `geass env` and the MCP tools `get_app` and
`list_variables` say the same.
:::

## Related

- [Command line](/docs/guides/command-line/)
- [MCP server](/docs/guides/mcp-server/)
- [MCP tools](/docs/reference/mcp-tools/)
- [REST API](/docs/reference/rest-api/)
- [Users and API tokens](/docs/guides/users-and-tokens/)
