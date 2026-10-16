---
title: "CLI commands"
description: "Every geass command, flag, environment variable and exit code, with the API calls each command makes, the token role it needs and an example."
---

This page lists every command of the `geass` command line. For installing it, signing in and everyday workflows, read
[Command line](/docs/guides/command-line/). The command line is a thin client of the `/api/v1`
[REST API](/docs/reference/rest-api/): each command makes one or two calls, which are listed under **API calls**
for every command below.

## Synopsis

```text
geass [global flags] <command> [arguments] [flags]
```

Global flags work on every command, before or after the command name. Run `geass` with no command, or `geass help`,
to see the command list, and `geass help <command>` (or `geass <command> --help`) for one command.

| Group | Commands |
| --- | --- |
| Account | `login`, `logout`, `whoami`, `version`, `config` |
| Services | `apps`, `deploy`, `build`, `status`, `logs`, `env`, `scale`, `rollback`, `metrics` |
| Functions | `functions` |
| Data and cluster | `projects` (with `quota` and `network`), `databases`, `stores`, `nodes`, `compliance` |
| Raw API and agents | `api`, `openapi`, `mcp` |
| Built in | `help`, `completion`, `--help` (`-h`), `--version` (`-v`) |

`deploy`, `build`, `status`, `logs`, `env`, `scale` and `rollback` also exist under `geass apps`, where they do the same
thing. `metrics` exists only at the top level, and `deployments` only under `geass apps`.

## Global flags

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--context` | string | the current login | Which saved login to use. List them with `geass config get-contexts`. |
| `--url` | string | the saved address | The Geass address, such as `https://geass.example.com`. Overrides the saved one and `$GEASS_URL`. A saved login is used only for its own address, see below. |
| `--token` | string | the saved token | An API token. Overrides the saved one and `$GEASS_TOKEN`. |
| `-p`, `--project` | string | none | The project, written as its ID, such as `proj-1a2b3c4d`. Also `$GEASS_PROJECT`. |
| `-e`, `--environment` | string | none | The environment, such as `production`. Also `$GEASS_ENVIRONMENT`. |
| `--json` | boolean | `false` | Print JSON, for programs. Not every command honors it, see [Output](#output). |
| `-y`, `--yes` | boolean | `false` | Do not ask before deleting. |
| `-h`, `--help` | boolean | `false` | Show help for the command. |
| `-v`, `--version` | boolean | `false` | Root command only. Print `geass version <version>`. |

A saved login belongs to the address it was made for, and its token is sent only there. When `--url` or `$GEASS_URL`
points somewhere else, the current login's token, project and environment are not used for that address:

- If you have exactly one login saved for that address, the command uses it. If you have several (an admin and a
  read-only one, say), it uses none of them and stops as below: say which with `--context`.
- A login you chose with `--context` or `$GEASS_CONTEXT` is never swapped for another. With an address that is not its
  own, it supplies no token, project or environment.
- If you have none, the command stops with ``not signed in to ADDRESS: run `geass login --url ADDRESS` `` (exit `4`) and
  sends nothing.
- A token you give with `--token` or `$GEASS_TOKEN` is your choice for this run and is used for whatever address you
  give.

Two addresses are the same when their scheme, host, port and path are. A host in capitals, a trailing slash and the
default port (`:443` for `https`, `:80` for `http`) make no difference. `http://geass.example.com` is not
`https://geass.example.com`: a token saved for one is never sent to the other.

## Environment variables

| Variable | Used by | Meaning |
| --- | --- | --- |
| `GEASS_URL` | every command | The Geass address. Same as `--url`. |
| `GEASS_TOKEN` | every command | An API token. Same as `--token`. `geass login` also reads it as the token to save. |
| `GEASS_PROJECT` | commands that use a project | Same as `--project`. |
| `GEASS_ENVIRONMENT` | commands that use an environment | Same as `--environment`. |
| `GEASS_CONTEXT` | every command that uses a saved login | Which saved login to use. Same as `--context`. |
| `GEASS_CONFIG` | every command | Full path of the configuration file. Without it, the file is `geass/config.json` in the user's configuration directory. |
| `GEASS_VERSION` | `install-cli.sh` only | Release tag to install. Default: `latest`. |
| `GEASS_INSTALL_DIR` | `install-cli.sh` only | Directory to install `geass` into. |
| `GEASS_REPO` | `install-cli.sh` only | GitHub `owner/name` to download releases from. Default: `degoke/geass`. |
| `GEASS_DOWNLOAD_BASE` | `install-cli.sh` only | Where to download the release files from instead: a mirror, or a directory as `file:///path` holding `geass-cli-<os>-<arch>` and `SHA256SUMS`. |

The first five are read by the command line itself. The last four are read by the install script, not by `geass`.

### Precedence

For the address, token, project and environment, the first of these that is set wins:

1. The flag (`--url`, `--token`, `--project`, `--environment`).
2. The environment variable (`GEASS_URL`, `GEASS_TOKEN`, `GEASS_PROJECT`, `GEASS_ENVIRONMENT`).
3. The saved login.

The saved login is `--context`, else `$GEASS_CONTEXT`, else `current` in the configuration file. It supplies the token,
project and environment only for its own address, see [Global flags](#global-flags).

If the command line cannot find a configuration directory (no `$HOME` in a bare container, for instance), every
command fails with `could not find a configuration directory (set GEASS_CONFIG)`, even when `GEASS_URL` and
`GEASS_TOKEN` are set. Set `GEASS_CONFIG` to any path to fix it. The file does not have to exist.

## Configuration file

`geass login` saves what the command line remembers between runs in `config.json`, in the `geass` folder of the
user's configuration directory:

| System | Path |
| --- | --- |
| Linux | `$XDG_CONFIG_HOME/geass/config.json`, or `~/.config/geass/config.json` |
| macOS | `~/Library/Application Support/geass/config.json` |
| Windows | `%AppData%\geass\config.json` |

`GEASS_CONFIG` replaces the whole path. The file holds tokens, so Geass writes it with mode `0600` (owner only),
creates its folder with mode `0700`, and replaces it atomically. A file that is not valid JSON stops every command:
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
| `current` | The name of the login used when you do not choose one. `geass login` and `geass config use-context` set it. |
| `contexts` | The saved logins, by name. The name is the host of the address (`geass.example.com`, or `localhost:8085` when the address has a port) unless you give `--name` or `--context` to `geass login`. |
| `url` | The Geass address, without a trailing slash. |
| `token` | The API token. Empty after `geass logout`. |
| `project`, `environment` | The defaults set by `geass config set-context`. |
| `user`, `role` | What the token was when you signed in: `token:<token name>` and `admin`, `observer` or `viewer`. For information only. |
| `expiresAt` | When the token expires, as `geass login` last saw it. Absent for a token that never expires. `geass config get-contexts` shows it, and `geass logout` clears it. |

## Output

- **Results go to standard output. Progress and notes go to standard error.** A line such as `web: build running`,
  `set LOG_LEVEL` or `The change is pending...` is on standard error, so a program that reads standard output never
  sees it.
- Lists and statuses print an aligned table by default. With `--json` they print a JSON object with an `items` array.
- `geass apps get`, `geass projects get`, `geass databases get`, `geass stores get`, `geass api` and `geass openapi`
  always print JSON.
- `geass deploy` and `geass build` print `{"deployed": [...]}` or `{"building": [...]}` with `--json` when they do not wait, and `{"items": [...]}` (the services' statuses) when they do.
- These commands print plain text lines whatever `--json` says: `scale`, `rollback`, `env set`, `env unset`,
  `apps create`, `apps delete`, `nodes tag`, `compliance set`, `compliance clear`, `projects network set`, `functions triggers add`,
  `functions triggers remove`, `logout`, `config use-context` and `config set-context`.
- With `--json`, `geass logs` prints one indented JSON object per log line. The objects are not on one line each, so
  read them as a stream (`jq -c .` reads them).
- Ages in tables are `30s`, `5m`, `3h` (under 48 hours) or `4d`.

## Exit codes

A script can tell what kind of failure it had from the code alone. The error text is printed on standard error as
`Error: <message>`.

| Code | Meaning | Examples |
| --- | --- | --- |
| `0` | Success | |
| `1` | Any other error | A network failure, a `400` from the API (such as a value out of range), `cancelled` after you answered no to a delete, a function that answered HTTP 400 or above, `no metrics yet`. |
| `2` | Usage error | An unknown command or flag, a missing argument, `KEY=VALUE` without an `=`, `--sleep-after` out of range, a delete without `--yes` when nobody can be asked. |
| `3` | Not found | The API answered `404` (also for a service a project token may not see), `no saved login called ...`, `has no earlier deployment to go back to`. |
| `4` | Not allowed | The API answered `401` or `403`, you are not signed in, or the sign-in was refused in the dashboard. On `401` the command line also prints ``Run `geass login` to sign in again.`` |
| `5` | Deploy failed | A `--wait` ended with a failed build or a failed rollout. The reason is in the message. |
| `6` | Timed out | A `--wait` ran past `--timeout`, or a sign-in was not approved before its code expired. |

## Account commands

### `geass login`

Signs in to a Geass dashboard and saves the login.

```text
geass login [--url URL] [--token-stdin] [--no-browser] [--name NAME]
```

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--token-stdin` | boolean | `false` | Read the token from the first line of standard input. |
| `--no-browser` | boolean | `false` | Do not open the browser. The address is printed either way. |
| `--name` | string | the `--context` value, else the host of the address | What to call this login. |

The command also uses the global flags `--url`, `--token` and `--context`.

- **Address.** `--url`, else `$GEASS_URL`, else the address of the current login. With none of them the command stops
  with `say which Geass to sign in to: geass login --url https://your-geass-address` (exit 2).
- **Token.** A token is taken only from `--token`, `$GEASS_TOKEN` or `--token-stdin`. A token saved by an earlier
  login is never reused, because renewing it is the reason you run `geass login` again.
- **No token.** The command starts a device sign-in: it prints a code and the address of the approval page, opens
  `<address>/device?code=<code>` in your browser when it runs in a terminal (not with `--no-browser`), and waits for
  you to approve the code in the dashboard. It asks every 5 seconds, waits 5 seconds longer each time the server says
  `slow_down`, and gives up when the code expires, 10 minutes after it was issued.
- **Check and save.** The token is checked with `GET /me` before anything is saved. A login that fails is not saved. A
  login that works is saved under `--name`, else `--context`, else the host of the address, becomes the current login,
  and keeps the project and environment defaults an earlier login of that name had, when it was for the same address.
  A name that is pointed at another address starts with none, because a project ID from one Geass means nothing on
  another. When the token expires is saved with it.
- **Output.** `Signed in to <url> as <user> (<access>)`, where access is `admin`, `read-only, with logs`, `read-only`
  or `project token for <project>/<environment>`. With `--json`: `{"context", "url", "user", "role"}`, and
  `expiresAt` when the token expires.

**API calls:** `POST /auth/device`, `POST /auth/token` (repeated), `GET /me`. The first two need no token.
**Needs:** nothing to start. An admin signed in to the dashboard must approve the code. A token given with `--token`
only has to be valid.

```bash
geass login --url https://geass.example.com
echo "$TOKEN" | geass login --url https://geass.example.com --token-stdin --name ci
```

### `geass logout`

Forgets the token, user, role and expiry of the current login (or the one chosen with `--context` or `$GEASS_CONTEXT`). The
address and the project and environment defaults stay.

```text
geass logout
```

Prints `Signed out of <url>.`, or `Not signed in.` when there is no such login. The token itself stays valid until it
expires or you revoke it under **Settings** → **Tokens** in the dashboard.

**API calls:** none. **Needs:** nothing.

### `geass whoami`

Shows who the saved token is and what it may do.

```text
geass whoami
```

```text
token:CLI: geass CLI on laptop on https://geass.example.com
access: read-only, with logs
can change things: no
expires: 2026-11-11 09:00 UTC (in 30 days)
```

The `expires` line says when the token stops working and how long is left (`in 30 days`, `in 5 hours`, `in under a
minute`, or `expired 2d ago`), or `never` for a token with no expiry.

With `--json` it prints the identity returned by the API: `username`, `role`, `scope` (`workspace` or `project`),
`project` and `environment` (project tokens only), `canMutate`, `canReadSensitive`, `authentication` (`token`) and
`expiresAt` (absent for a token that never expires).

**API calls:** `GET /me`. **Needs:** any signed-in token.

### `geass version`

Shows the version of the command line, and the API version and Geass release of the cluster it talks to.

```text
geass version
```

```text
geass v0.2.0
api v1
release v0.3.0
note: this command line is older than the cluster. Update it to v0.3.0: run install-cli.sh again (GEASS_VERSION=v0.3.0 pins it)
```

The `api` and `release` lines appear only when an address is known and the token is accepted (a cluster from before the
release was reported prints no `release` line). The `note` line appears when the command line and the cluster are different
releases that can be ordered: the command line talks to the cluster's API, whose resources carry the fields of the cluster's
release, so keep them in step. The saved token is sent only to the address it was saved for. Without a login the command
prints only the first line, with exit 0. A command line built without a release tag reports `dev`, and says nothing about
order. With `--json`: `{"cli": "...", "api": "...", "release": "..."}`.

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

`EXPIRES` is the day the token expires, as `geass login` saw it, followed by `(expired)` once that day has passed, or `-`
when it was not recorded or the token never expires. With `--json` it prints the whole configuration with each token replaced by `(saved)`.

#### `geass config use-context NAME`

Makes a saved login the current one. Prints `Using NAME.`. If there is no such login it exits `3` and lists the names
you have.

#### `geass config set-context [NAME]`

Sets the default project and environment of a login from the global `--project` and `--environment` flags. A flag
you leave out keeps its current value. `NAME` defaults to `--context`, then to the current login. Prints
`NAME now defaults to <project> / <environment>.`. If there is no such login it exits `3`.

```bash
geass config set-context -p proj-1a2b3c4d -e production
```

There is no command that clears a default. Edit `config.json`, or sign in again under another name.

## Services

Most commands in this group take the service's name, which is unique across the whole Geass install, not only inside
a project. `geass apps` is also known as `geass app`, `geass services` and `geass service`.

### `geass apps list`

Lists services (aliases: `geass apps ls`).

```text
geass apps list [-p PROJECT] [-e ENVIRONMENT]
```

Takes no arguments. The columns are `NAME`, `PROJECT`, `ENV`, `STATE`, `DETAIL` and `AGE`. `STATE` is `ready`,
`pending`, `failed`, `draft` or `deleting`. `DETAIL` is the public URL when there is one, otherwise the image, the
repository or the function runtime.

```text
NAME  PROJECT        ENV         STATE  DETAIL                   AGE
web   proj-1a2b3c4d  production  ready  https://web.example.com  3h
api   proj-1a2b3c4d  production  draft  nginx:alpine             3d
```

The list is narrowed only when you pass `--project` or `--environment` on the command line. A project or environment
that comes from `$GEASS_PROJECT`, `$GEASS_ENVIRONMENT` or `geass config set-context` does not narrow it, because a
list with nothing said means everything. When you do pass one of the flags, the other is filled in from those
defaults.

The same listing exists for `geass projects list`, `geass databases list` and `geass stores list`, with the same
columns and rules.

**API calls:** `GET /apps` with `project` and `environment` when narrowed. **Needs:** any signed-in token. A project
token sees only its own environment.

### `geass apps get`

Shows one service with its settings and status, as JSON.

```text
geass apps get NAME
```

The output has three keys: `resource` (the summary row), `spec` (the settings) and `status`. Secret values in `spec`
are emptied. Plain configuration values are not (see [Configuration values are visible](#configuration-values-are-visible)).
`geass projects get`, `geass databases get` and `geass stores get` work the same way for their kind.

**API calls:** `GET /apps/{name}`. **Needs:** any signed-in token.

### `geass status`

Shows whether services are up and what the last deploy left behind (also `geass apps status`).

```text
geass status [APP...]
```

With names, shows those services. With none, shows every service in the project and environment you give (from the
flags, the environment variables or the saved defaults) and, when you give none, every service in the install, one API
call each.

```text
NAME  STATE   READY  BUILD      URL                      MESSAGE
web   ready   2/2    Succeeded  https://web.example.com  -
api   failed  0/1    Failed     -                        Kaniko completed without an immutable image digest
```

| Column | Meaning |
| --- | --- |
| `STATE` | `ready`, `pending`, `failed`, `draft` or `deleting`. |
| `READY` | Running copies over wanted copies, such as `2/2`. A service that sleeps when idle and has stopped shows `0/0 (asleep)`. `-` when the service has no Deployment yet. |
| `BUILD` | The phase of the latest build: `Pending`, `WaitingForCI`, `Running`, `Succeeded`, `Failed` or `Cancelled`. `-` for a service that has no build. |
| `MESSAGE` | Why the service is not ready, when it says. |

With `--json` it prints `{"items": [...]}`. Each item has `name`, `project`, `environment`, `state`, `message`,
`url`, `ready`, `pendingChanges`, `replicas` (`desired`, `ready`, `available`), `sleeping`, `scaleToZero` (for a service
that asked to sleep when idle: `idleSeconds`, `maxReplicas`, and `active` and `message` once the cluster has said),
`build` (`name`, `phase`,
`failureReason`, `sourceRevision`, `ciState`, `startedAt`, `completedAt`) and `lastAttempt` (`id`, `phase`,
`startedAt`, `completedAt`, `summary`, `errors`).

**API calls:** `GET /apps` (without names), then `GET /apps/{name}/status` for each service. **Needs:** any signed-in
token.

### `geass deploy`

Deploys the pending changes of services (also `geass apps deploy`).

```text
geass deploy [APP...] [--wait] [--timeout DURATION]
```

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--wait` | boolean | `false` | Wait until each service is ready. Exits `5` if one fails. |
| `--timeout` | duration | `10m` | How long `--wait` waits for each service. The waits run one after another. |

- **With names,** each named service is deployed.
- **With no names,** everything pending in the environment given by `--project` and `--environment` is deployed, and
  with `--wait` the command then waits for every service in that environment that is not a draft and not being
  deleted. Without both values it stops with `say what to deploy: name services, or give --project and
  --environment to deploy everything pending` (exit 2).
- **Progress** goes to standard error: `web: deploying`, then lines such as `web: build running`,
  `web: pending (1 of 2 ready)` and `web: ready at https://web.example.com`, one each time the state changes.
- **With `--wait`** the final statuses are printed as a table (or `{"items": [...]}` with `--json`). A service is done
  when it is ready, has no pending changes and its latest build, if it has one, succeeded. It has failed when its state is `failed` or
  its build phase is `Failed`: the exit code is `5` and the message gives the build's failure reason. A wait that runs
  past `--timeout` exits `6`.
- **Without `--wait`** and with `--json`, it prints `{"deployed": ["web"]}`.

Deploying a service that has no pending changes records a new deployment anyway.

**API calls:** `POST /apps/{name}/deploy` for each name, or `POST /projects/{project}/deploy` with
`{"environment": ...}` and `GET /apps` when there are none; with `--wait`, `GET /apps/{name}/status` every 3 seconds.
**Needs:** Full access or a project token for named services. Full access for the environment form: a project token
gets `403` there, so name the services.

```bash
geass deploy web --wait
geass deploy -p proj-1a2b3c4d -e production --wait
```

### `geass build`

Runs the build of services that are built from a repository again (also `geass apps build`). It is what a push to the
branch does, for a pipeline that has pushed nothing.

```text
geass build [APP...] [--wait] [--timeout DURATION]
```

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--wait` | boolean | `false` | Follow each build through to the rollout, and wait until the service is ready. Exits `5` if the build or the rollout fails. |
| `--timeout` | duration | `10m` | How long `--wait` waits for each service. The waits run one after another. |

- **With names,** each named service's build is run again, from the head of its branch (or the commit it pins). A service
  that runs an image or a function has no build to run: the command stops with `<name>: the app has no repository
  build` (exit 1). A service that was never deployed has none yet, and the command says `the app has not been built
  yet: its build is made when it is deployed`.
- **With no names,** every service built from a repository in the environment given by `--project` and `--environment`
  is built, leaving out drafts and services being deleted. Without both values it stops with `say what to build: name
  services, or give --project and --environment to build every service built from a repository` (exit 2). When the
  environment has none, it says so on standard error and exits `0`.
- **Progress** goes to standard error: `web: build started`, then, with `--wait`, lines such as `web: build running`,
  `web: pending (1 of 2 ready)` and `web: ready at https://web.example.com`, one each time the state changes.
- **With `--wait`** the build as it was before the command is read first. A status that still shows that finished
  build is not taken for the answer, until 15 seconds have passed: a build that restarts shows as not finished within
  moments, and one that really ends exactly as the last did (it fails at once, the same way) is accepted after that. The
  final statuses are printed as a table (or `{"items": [...]}` with `--json`). A service is done when it is ready, has no
  pending changes and its build succeeded. It has failed when its state is `failed` or its build phase is `Failed`: the
  exit code is `5` and the message gives the build's failure reason. A wait that runs past `--timeout` exits `6`.
- **Without `--wait`** and with `--json`, it prints `{"building": ["web"]}`.

A service that is already running keeps serving from its current image until the new one is built, and is then rolled
out onto it. Changes saved to the service and not yet deployed are not applied: use `geass deploy` for those.

**API calls:** `POST /apps/{name}/build` for each name, or `GET /apps` when there are none; with `--wait`,
`GET /apps/{name}/status` once before the build is started and then every 3 seconds. **Needs:** Full access or a project
token for the service's environment.

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

The newest entry is the one running. `ID` is what `geass rollback APP DEPLOYMENT` takes. A rollback adds an entry of its
own. Empty columns show `-`. With `--json` it prints `{"items": [...]}`, each item with `id`, `phase`, `image`,
`commit`, `trigger`, `actor`, `changeTitle`, `createdAt`, `startedAt` and `completedAt`. A service that was never
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
| `--tail` | integer | `200` | How many of the newest lines to show. The API accepts 1 to 1000. See below. |
| `--since` | string | none | Only lines newer than this long ago, such as `15m` or `2h`. Go duration syntax, up to 7 days: write `48h`, not `2d`. |
| `--level` | string | none | Only lines of this level: `error`, `warn`, `info` or `debug`. |
| `--search` | string | none | Only lines containing this text, up to 200 characters. |

Each line prints as `HH:MM:SS <pod suffix> <text>`, in UTC. The pod suffix is the last part of the pod's name, so
`web-6d9c7b8f5-x2k4p` shows as `x2k4p`. With `--json`, each line is a JSON object with `t`, `service`, `pod`,
`container`, `line` and `level`. When nothing matches, `No log lines matched.` is printed on standard error and
standard output stays empty.

`--tail` counts the newest lines in all, oldest first. The dashboard reads that many from the end of each copy's log
before it applies `--level` and `--search`, so a filter looks through the most recent lines only: with `--tail 200`,
`--level error` finds the errors among each copy's last 200 lines. Raise `--tail` (up to 1000) to look further back.
`--level warn` also shows errors. With `-f`, `--tail` is how many recent lines to start each copy with.

**API calls:** `GET /apps/{name}/logs` with `tail`, `since`, `q` and `level`; with `-f`, one
`GET /apps/{name}/logs/stream` (server-sent events) with the same filters. **Needs:** Read only, with logs, Full access,
or a project token (for its own services). A **Read only** (viewer) token gets `403`.

```bash
geass logs web -f --level error --since 15m
```

### `geass env`

Lists, sets and removes a service's variables (also `geass apps env`).

Variables are changes like any other: they apply when the service is next deployed, unless you pass `--deploy`.

#### `geass env list`

```text
geass env list APP
```

Prints a `NAME` and `KIND` table: `secret` for secrets, `config` for plain configuration, and the kind the service
reports for variables injected from elsewhere (`literal` or `secret`). Values are not printed. With `--json` it prints
the whole answer, which includes the values of plain configuration (see
[Configuration values are visible](#configuration-values-are-visible)).

**API calls:** `GET /apps/{name}/variables`. **Needs:** Read only, with logs, Full access, or a project token.

#### `geass env set`

```text
geass env set APP KEY=VALUE... [--secret] [--deploy] [--wait]
```

Sets each pair. Only the first `=` splits, so a value can contain `=`. A pair without an `=` stops the command with
exit `2`. Prints `set KEY` for each, then ``The change is pending. Run `geass deploy APP` to apply it.``

#### `geass env unset`

```text
geass env unset APP KEY... [--secret] [--deploy] [--wait]
```

Removes each key, printing `removed KEY`. Removing a key that is not there is not an error. It removes plain
configuration, or secrets with `--secret`. It cannot remove variables that Geass injected for you, such as a database
connection string.

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--secret` | boolean | `false` | The variable is a secret, kept apart from ordinary configuration. |
| `--deploy` | boolean | `false` | Deploy the change now. |
| `--wait` | boolean | `false` | With `--deploy`: wait until the service is ready (10 minutes at most) and print its status. Without `--deploy` it does nothing. |

**API calls:** `POST /apps/{name}/config/set` or `/secrets/set` with `{"key", "value"}`; `POST
/apps/{name}/config/delete` or `/secrets/delete` with `{"key"}`; with `--deploy`, `POST /apps/{name}/deploy` and
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
| `--sleep-after` | duration | none | Stop the service after this long without requests, and start it on the next one. 30 seconds to 24 hours, such as `10m`. |
| `--always-on` | boolean | `false` | Keep the service running instead of sleeping when idle. |

`REPLICAS` is a whole number from 0 to 1000. The two flags cannot be used together. With only `APP` you must give a
flag. Scaling takes effect at once, with no deploy. So does the sleep setting, once the cluster has accepted it.

```text
$ geass scale web 3
web scaled to 3
$ geass scale web --sleep-after 10m
web sleeps after 10m0s without requests
$ geass scale web --always-on
web stays running
```

`--sleep-after` only works when the serverless add-on is on in the cluster settings and the service has a public
host, and not together with CPU autoscaling. The command saves the setting and then waits up to 10 seconds for the
cluster to say whether the service can sleep. It prints `APP sleeps after ...` only when it can. When it cannot, the
service stays running, the setting stays saved, and the command exits `1` and says why:

```text
$ geass scale web --sleep-after 10m
Error: saved, but web cannot sleep when idle and stays running: The serverless add-on is not installed
```

If you gave `REPLICAS` too, it is applied before the command reports that. When the cluster has not answered within
10 seconds, the message says so and points at `resource.details.scaleToZero`, where `active` and `message` give the
state (`geass status APP --json` shows them as `scaleToZero`). A service that shows no request to sleep at all is waited
for too, since the read can lag the change, and ends the wait the same way. `--always-on` needs no check. See
[Scale to zero](/docs/guides/scale-to-zero/).

**API calls:** `POST /apps/{name}/scale` with `{"replicas"}`; `PATCH /apps/{name}` with
`{"scaleToZero": "on" | "off", "idleSeconds"}`, then `GET /apps/{name}/status` until `scaleToZero.active` is `true` or
the wait ends. **Needs:** Full access or a project token.

### `geass rollback`

Goes back to an earlier deployment (also `geass apps rollback`).

```text
geass rollback APP [DEPLOYMENT] [--wait]
```

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--wait` | boolean | `false` | Wait until the service is ready (10 minutes at most) and print its status. |

With no `DEPLOYMENT`, the command goes to the deployment before the newest one. With fewer than two deployments it
stops with `APP has no earlier deployment to go back to` (exit 3). List the ids with
[`geass apps deployments APP`](#geass-apps-deployments): the `ID` of each row is what `DEPLOYMENT` takes.

Rolling back deploys at once and records a new deployment, so running `geass rollback APP` again goes to the entry
before that one, not further back. Pass an id to choose. Functions cannot be rolled back (the API answers `functions
cannot be rolled back; restore the earlier code and deploy again`).

**API calls:** `GET /apps/{name}/deployments` (without `DEPLOYMENT`), `POST /apps/{name}/rollback` with
`{"revision"}`, and status reads with `--wait`. **Needs:** Full access or a project token.

### `geass apps create`

Creates a service from an image, a GitHub repository or a function file.

```text
geass apps create NAME (--image IMAGE | --repo OWNER/NAME | --function FILE) [flags]
```

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--image` | string | none | Run this container image. |
| `--repo` | string | none | Build from this GitHub repository, `owner/name`. |
| `--branch` | string | `main` | The branch to build, with `--repo`. |
| `--function` | string | none | Run this file as a function. The file is read on your machine and sent as the code. |
| `--runtime` | string | `bun` | The function's runtime: `bun`, `node` or `python`. |
| `--mode` | string | `http` | The function's mode: `http` (a program that serves HTTP) or `event` (a handler). |
| `--port` | integer | none | The port it serves on. Without it the API uses 8080. |
| `--deploy` | boolean | `false` | Deploy it straight away instead of saving a draft. |

It needs `--project` and `--environment` (or their defaults) and exactly one of `--image`, `--repo` and
`--function`; if you give several, the first in that order wins. `--runtime` and `--mode` apply only to `--function`.
The service is named `NAME`.

```bash
geass apps create web -p proj-1a2b3c4d -e production --image nginx:alpine --port 80
geass apps create api -p proj-1a2b3c4d -e production --repo acme/storefront --branch main
geass apps create hook -p proj-1a2b3c4d -e production --function hook.mjs --runtime node --mode event
```

Without `--deploy` the service is saved as a draft and the command prints ``Created NAME as a draft. Deploy it with `geass deploy NAME`.`` With `--deploy` it is deployed as it is saved and the command prints `Created NAME and started
deploying it`. Follow it with `geass status NAME`, or `geass deploy NAME --wait` once it exists. The deploy is checked
before anything is saved: a cluster with no room for the service refuses the command and leaves no draft behind. The
command prints that only when the answer says the service was deployed. A Geass older than the command line ignores
`--deploy` and saves a draft: the command then exits `1`, saying that it did not deploy the service and to run
`geass deploy NAME`.

Creating a service from a repository also needs the project to be linked to GitHub, which you do in the dashboard
([GitHub and builds](/docs/guides/github-and-builds/)). With `--deploy`, a missing link is an error. As a draft it is
accepted without one.

**API calls:** `POST /apps` with `project`, `environment`, `name`, either `draft` (without `--deploy`) or `deploy`
(with it), `source` (`image`, `git` or `function`), `image` or `repository` and `branch` or `runtime`, `code` and
`mode`, and `port`. **Needs:** Full access.

### `geass apps delete`

Deletes a service.

```text
geass apps delete NAME [-y]
```

Asks `Delete app "NAME"? This cannot be undone. [y/N]` on standard error and reads the answer from standard input.
Anything other than `y` or `yes` cancels it (`cancelled`, exit 1). When nobody can be asked (standard input is not a
terminal), the command refuses with `this deletes data; run it again with --yes to confirm` (exit 2). `--yes` skips
the question. Prints `Deleted NAME.`

There is no `delete` command for databases, buckets or projects. Use `geass api DELETE`, see
[`geass api`](#geass-api).

**API calls:** `DELETE /apps/{name}?confirmName={name}`. **Needs:** Full access.

### `geass metrics`

Shows CPU, memory, network and request use over a window of time.

```text
geass metrics NAME [--window WINDOW] [--kind KIND]
```

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--window` | string | `1h` | How far back: `15m`, `1h`, `6h` or `24h`. |
| `--kind` | string | `apps` | What `NAME` is: `apps`, `databases` or `projects`. |

Prints one row per series with the latest value and, in brackets, the lowest and highest in the window, to three
significant figures:

```text
SERIES            LATEST (MIN - MAX)
cpu               0.0123 (0.0101 - 0.0456)
memory            1.34e+08 (1.3e+08 - 1.36e+08)
memoryPerReplica  6.7e+07 (6.5e+07 - 6.9e+07)
network           2.1e+03 (1.2e+03 - 9.8e+03)
networkIn         1.7e+03 (900 - 8.4e+03)
requests          4.2 (1.1 - 9.7)
```

`cpu` is in cores, `memory` in bytes (the total of every replica), `network` (sent) and `networkIn` (received) in bytes
per second, and `requests` (services only) in requests per second. A service also has `memoryPerReplica`, in bytes: the
memory of its busiest replica, which is what **Memory reserved** is set against. Databases add `storage`, the used space
of their volumes, in bytes. With `--json` it prints the answer unchanged: `window`, `step`, `available`, an optional `message` and `series`, where each
series is a list of `{"t": <unix seconds>, "v": <value>}`.

Metrics need Prometheus. When it is not installed or not reachable, the command exits `1` with `no metrics yet: ...`
and the reason (with `--json` it prints `"available": false` and exits `0`). See
[Observability](/docs/guides/observability/).

**API calls:** `GET /{kind}/{name}/metrics?window=...`. **Needs:** any signed-in token for services (a project token
for its own services only); any workspace token for `databases` and `projects`.

## Functions

### `geass functions`

Schedules and calls functions. Also `geass function` and `geass fn`. A function in event mode is a handler that is
given an event for each request and returns the response. One in http mode is a program that serves HTTP itself. A
trigger calls the function on a schedule and wakes it first when it is asleep. See [Functions](/docs/guides/functions/).

#### `geass functions triggers list`

```text
geass functions triggers list APP
```

Prints `NAME`, `SCHEDULE (UTC)` and `PATH` for each schedule. The path shows `/` when none was set. With `--json` it
prints the function: `runtime`, `mode`, `triggers` and `url`. `triggers` is also `geass functions trigger`.

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

`NAME` is lowercase letters, numbers and dashes, up to 32 characters, starting and ending with a letter or number. A
function can have 8 schedules. `SCHEDULE` is a cron expression in UTC with five fields of numbers, ranges, steps and
lists (names such as `MON` are not accepted), or `@hourly`, `@daily`, `@midnight`, `@weekly`, `@monthly`, `@yearly` or
`@annually`. Each run is a `POST` to the function. A schedule applies when the function is next deployed, so the
command prints ``APP will call NAME on schedule "..." once it is deployed. Deploy it with `geass deploy APP`.``

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

Also `geass functions triggers rm`. Stops calling the function on that schedule once it is deployed. Prints `Removed
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
| `--path` | string | `/` | The path to call, with a query if you like. It is joined to the function's address. |
| `--data` | string | none | The JSON body to send. |

The status line `HTTP 200` goes to standard error and the body to standard output. A status of 400 or above also
exits `1`. With `--json` it prints `{"status": 200, "body": "..."}` and exits `0` whatever the status. The call has no
Geass token: the function's own rules apply. A sleeping function is woken, so the first call can take a few seconds.
The call times out after 2 minutes. If the function has no public address, the command stops with `the function has
no public address to call: give it a domain first`.

**API calls:** `GET /apps/{name}`, then an HTTP request to the function's URL. **Needs:** any signed-in token.

## Data and cluster

### `geass projects`

Lists and inspects projects. Also `geass project`.

```text
geass projects list
geass projects get NAME
```

`list` (also `ls`) prints the same table as `geass apps list`; the `NAME` column is the project's ID, such as
`proj-1a2b3c4d`, and the `DETAIL` column lists its environments. The table has no column for the display name. Read it
with `geass projects get NAME`. `get` prints `resource`, `spec` and `status` as JSON.

**API calls:** `GET /projects`, `GET /projects/{name}`. **Needs:** any workspace token.

#### `geass projects quota`

Caps what a project's environments, and the project as a whole, can ask for. A project with no limits can ask for
everything the cluster has. See [Project limits](/docs/guides/project-limits/).

```text
geass projects quota show PROJECT
geass projects quota set PROJECT [--cpu Q] [--memory Q] [--storage Q] [--pods N]
                                 [--project-cpu Q] [--project-memory Q] [--project-storage Q] [--project-pods N]
geass projects quota clear PROJECT
```

`show` prints a table of the limits for each environment and for the whole project, with what the project uses of its
whole-project limits, or the same as JSON with `--json`. `set` changes only the limits you name: one you leave out keeps
its value, and one you give empty (`--memory ''`) is removed. `clear` removes every limit. `set` and `clear` print the
limits as they are afterwards.

| Flag | Type | Meaning |
| --- | --- | --- |
| `--cpu`, `--memory`, `--storage`, `--pods` | string | The most one environment's pods may ask for in CPU (such as `4` or `500m`), memory (`8Gi`), disk (`50Gi`) and pods |
| `--project-cpu`, `--project-memory`, `--project-storage`, `--project-pods` | string | The most the whole project may ask for, added up over its environments |

```bash
geass projects quota set shop --project-cpu 8 --project-memory 16Gi --cpu 4
geass projects quota set shop --memory ''
```

**API calls:** `GET /projects/{name}/quota`, and `POST /projects/{name}/quota` for `set` and `clear`. **Needs:** any
workspace token to show, **Full access** to change.

#### `geass projects network`

Chooses who can reach a project's services, and whether they are kept away from the machines the cluster runs on. See
[Network isolation](/docs/guides/network-isolation/).

```text
geass projects network show PROJECT
geass projects network set PROJECT [--isolation project|none] [--allow-from PROJECT,...] [--block-nodes[=false]]
```

`set` changes only what you name and leaves the rest as it was. It reads the current settings first, because the API
takes all of who may reach a project each time.

| Flag | Type | Meaning |
| --- | --- | --- |
| `--isolation` | string | `project` (also `private`) lets in only the project itself, the platform and public domains. `none` (also `open`) lets in every service in the cluster |
| `--allow-from` | list of strings | Other projects that may reach this one. An empty value lets in none |
| `--block-nodes` | boolean | Keep the project's services away from the addresses of the cluster's machines. `--block-nodes=false` allows it |

```bash
geass projects network set shop --block-nodes
geass projects network set shop --allow-from billing,search
```

**API calls:** `GET /projects/{name}/network`, and `POST /projects/{name}/network` for `set`. **Needs:** any workspace
token to show, **Full access** to change.

### `geass databases`

Lists databases and shows how to connect to them. Also `geass database`, `geass dbs` and `geass db`.

```text
geass databases list
geass databases get NAME
geass databases connection NAME [--reveal]
```

`list` (also `ls`) and `get` work as for projects; the `DETAIL` column of `list` is the database engine.

`connection` shows how to reach a database: `database`, `host`, `port` and `username`, whichever the database has.

```text
database   orders
host       orders-db.example.svc
port       5432
username   app
```

The password and the connection string are hidden, and a note on standard error says so. `--reveal` adds `password`
and `uri` and prints everything as JSON, with `"revealed": true`. Revealing is written to the operator log, with the
database name and the user who asked.

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--reveal` | boolean | `false` | Include the password and the connection string. |

**API calls:** `GET /databases`, `GET /databases/{name}`, `GET /databases/{name}/connection` (with `?reveal=true` for
`--reveal`). **Needs:** any workspace token for `list` and `get`. Full access for `connection`, with or without
`--reveal`. The command fails with `no connection details yet: the resource has not finished setting up` until the
database is ready.

### `geass stores`

Lists object storage buckets. Also `geass buckets` and `geass object-stores`.

```text
geass stores list
geass stores get NAME
```

`list` (also `ls`) and `get` work as for projects, except that `DETAIL` shows the bucket's message, if any, where
projects and databases show their environments or engine. There is no command for the connection details of a bucket.

**API calls:** `GET /object-stores`, `GET /object-stores/{name}`. **Needs:** any workspace token.

### `geass nodes`

Lists the cluster's nodes and tags them. Also `geass node`.

#### `geass nodes list`

```text
geass nodes list
```

Also `geass nodes ls`. Prints `NAME`, `STATUS` (`Ready` or `NotReady`), `REGION`, `ZONE`, `TAGS`, `CPU` and `MEMORY`.
A reserved tag shows as `gpu (reserved)` and a compliance tag as `hipaa (compliance)`.

```text
NAME      STATUS    REGION  ZONE  TAGS                CPU  MEMORY
worker-1  Ready     eu      -     -                   4    8Gi
worker-2  Ready     eu      fsn1  hipaa (compliance)  4    8Gi
worker-3  NotReady  -       -     -                   -    -
```

With `--json` it prints `{"items": [...]}` where each node has `name`, `ready`, `schedulable`, `controlPlane`,
`region`, `zone`, `tags`, `cpu` and `memory`.

**API calls:** `GET /nodes`. **Needs:** any workspace token. A project token gets `403`.

#### `geass nodes tag`

Sets a node's region, zone and tags.

```text
geass nodes tag NODE [--region REGION] [--zone ZONE] [--tags TAG,...] [--reserved TAG,...] [--compliance TAG,...]
```

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--region` | string | empty | The region, such as `eu`. |
| `--zone` | string | empty | The site inside the region. |
| `--tags` | list of strings | none | Tags the node carries. Comma separated, or repeat the flag. |
| `--reserved` | list of strings | none | Tags to reserve the node for: all other work stays off it. Implies the tag. |
| `--compliance` | list of strings | none | Compliance tags, such as `hipaa`. Always reserved, and required by projects that need them. Implies the tag. |

This replaces everything the node had. A region, zone or tag you leave out is removed. A name is lowercase letters,
numbers and hyphens, up to 40 characters, and a node can have 16 tags. The command prints `Updated NODE. Running
workloads are not moved: one that restarts only returns to a node that matches.` Then it prints each warning the API
returns on standard error, as `warning: ...`. They say what the change leaves to do or watch for, such as a tag that is
now a compliance tag while work that does not belong is still running on the node, or a project that has no node left
to restart on. A change with nothing to warn about prints none.

```bash
geass nodes tag worker-2 --region eu --tags hipaa --compliance hipaa
```

**API calls:** `POST /nodes/{name}/placement` with `region`, `zone`, `tags`, `reserved` and `compliance`, each tag
list as a comma-separated string. **Needs:** Full access. See [Compliance tags](/docs/guides/compliance-tags/) and
[Scaling and placement](/docs/guides/scaling-and-placement/).

### `geass compliance`

Requires node tags, such as `hipaa`, for everything a project runs. See
[Compliance tags](/docs/guides/compliance-tags/).

#### `geass compliance show`

```text
geass compliance show PROJECT
```

Prints the tags the project requires and where it falls short.

```text
proj-1a2b3c4d requires nodes tagged: hipaa
DATABASE  COMPLIANCE
orders    missing hipaa

These nodes carry a required tag without being reserved for it: n3
```

A project that requires nothing prints `PROJECT requires no node tags.` With `--json` it prints `requiredTags`,
`region`, `databases` (each with `name`, `compliant` and `missing`) and `openNodes`.

**API calls:** `GET /projects/{name}/compliance`. **Needs:** any workspace token.

#### `geass compliance set`

```text
geass compliance set PROJECT TAG...
```

Requires these node tags for everything in the project. It replaces the current ones. A project can require 8 tags.
The API refuses tags that are not compliance tags on the nodes that carry them, and tags a database's nodes lack.
Prints `PROJECT now requires: hipaa, pci`.

#### `geass compliance clear`

```text
geass compliance clear PROJECT
```

Stops requiring node tags for a project. Prints `PROJECT no longer requires node tags.`

**API calls (set and clear):** `POST /projects/{name}/compliance` with `{"requiredTags": "hipaa,pci"}` (an empty
string for `clear`). **Needs:** Full access.

## Raw API and agents

### `geass api`

Calls any `/api/v1` endpoint and prints the JSON it answers.

```text
geass api METHOD PATH [-d BODY] [-q KEY=VALUE]...
```

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `-d`, `--data` | string | none | The JSON body: a string, `@file` to read a file, or `-` to read standard input. A body that is not valid JSON is refused before it is sent. |
| `-q`, `--query` | list of strings | none | A query parameter, `key=value`. Repeat the flag for more. |

`METHOD` is any HTTP method, in any case. `PATH` is under `/api/v1`, such as `/apps`, and may carry a query string
of its own, which `-q` values are added to. The output is always JSON (`null` when the API answers with nothing). An
error exits with the codes above. The command reads the whole answer before printing it, so it does not work for
endpoints that stream (`/events`, the `logs/stream` routes). `geass openapi` lists every endpoint.

```bash
geass api GET /me
geass api GET /apps -q project=proj-1a2b3c4d -q environment=production
geass api POST /apps/web/scale -d '{"replicas": 3}'
geass api GET /apps/web/deployments
geass api DELETE /databases/orders -q confirmName=orders
geass api POST /databases/restore -d '{"archive": "orders-backup-1a2b3c"}'
```

A `DELETE` needs `confirmName` set to the resource's name. A restore needs the name of the backup archive, which you
find in the dashboard ([Backups and restore](/docs/guides/backups-and-restore/)).

**API calls:** the one you give. **Needs:** whatever that endpoint needs.

### `geass openapi`

Prints the OpenAPI 3 description of the API as JSON.

```text
geass openapi
```

Each operation carries an `x-access` value that says who may call it: `public`, `viewer`, `observer`, `project` or
`admin`.

**API calls:** `GET /openapi.json`. **Needs:** any workspace token.

### `geass mcp`

Runs an MCP server so AI agents can deploy and monitor services.

```text
geass mcp [--read-only] [--allow-delete] [--listen ADDRESS]
```

| Flag | Type | Default | Meaning |
| --- | --- | --- | --- |
| `--read-only` | boolean | `false` | Offer only tools that change nothing, whatever the token could do. An admin token still gets `database_connection`, which only reads. |
| `--allow-delete` | boolean | `false` | Also offer the tool that permanently deletes resources. |
| `--listen` | string | none | Serve MCP over HTTP at this address, such as `127.0.0.1:8765`, instead of standard input and output. |

Without `--listen` it speaks MCP over standard input and output as the saved login (or `--token`, `$GEASS_TOKEN`).
With `--listen`, each agent sends its own Geass token as a Bearer token and the command needs only an address.
Everything meant for a person goes to standard error. See [MCP server](/docs/guides/mcp-server/) and
[MCP tools](/docs/reference/mcp-tools/).

**API calls:** `GET /me`, then whatever the tools call. **Needs:** the token decides which tools exist.

## Built-in commands

The command line is built on [Cobra](https://cobra.dev), which adds these:

| Command | Meaning |
| --- | --- |
| `geass help [command]` | Help for any command. |
| `geass completion bash`, `zsh`, `fish` or `powershell` | Print a shell completion script. |
| `geass --version`, `geass -v` | Print `geass version <version>`. |

## Configuration values are visible

:::note
Plain configuration (`geass env set` without `--secret`) is not secret, and the API returns it. Secrets are returned
by name only, and `spec.secretData` in `geass apps get` has its values emptied. But `geass env list --json` and
`geass apps get` (with a Full access token) print plain configuration values (`config` and `spec.configData`), and
`spec.env` lists literal environment variables with their values. `geass env list` without `--json` prints names
only. Store anything sensitive as a secret. The help of `geass env` and the MCP tools `get_app` and `list_variables`
say the same.
:::

## Related

- [Command line](/docs/guides/command-line/)
- [MCP server](/docs/guides/mcp-server/)
- [MCP tools](/docs/reference/mcp-tools/)
- [REST API](/docs/reference/rest-api/)
- [Users and API tokens](/docs/guides/users-and-tokens/)
