---
title: "Command line"
description: "Install the geass command line, sign in with a code you approve in the dashboard, and deploy, watch, scale and configure services from a terminal or CI."
---

`geass` is a command line for a running Geass cluster. Use it to list projects and services, deploy and wait for the
result, read logs, change variables, scale, roll back, schedule functions and call any endpoint of the API, from your
terminal or from a CI job. Commands print a table for a person, most can print JSON for a program, and every command
ends with an exit code a script can act on.

It is not the cluster installer. The installer is a separate program, run once with `install.sh` (see
[Install Geass](/docs/getting-started/install/)). The command line talks to a cluster that is already running, through
its `/api/v1` [REST API](/docs/reference/rest-api/). Every flag and command is listed in
[CLI commands](/docs/reference/cli/).

## Before you start

- A running Geass cluster whose dashboard you can reach, over HTTPS on your domain or through a `kubectl port-forward`
  to `http://localhost:8085` (see [Sign in](/docs/getting-started/sign-in/)).
- A dashboard login with the **admin** role, to approve the command line's sign-in. A viewer cannot approve it.
- `curl` and `sh` on the machine where you install it.

## Install

Run the install script from the latest release:

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install-cli.sh | sh
```

The script picks the build for your system, downloads it from the GitHub release and installs it as `geass`. It works
on Linux and macOS, on `amd64` (`x86_64`) and `arm64` (`aarch64`). It puts the file in `/usr/local/bin` when you can
write there, otherwise in `~/.local/bin`, and tells you to add that folder to your `PATH` if it is not on it. Check it:

```bash
geass version
```

```text
geass v0.2.0
```

The script downloads the build and the release's `SHA256SUMS`, checks one against the other, and installs nothing that does not match. It reads four variables:

| Variable | Meaning | Default |
| --- | --- | --- |
| `GEASS_VERSION` | The release tag to install. | `latest` |
| `GEASS_INSTALL_DIR` | The folder to install `geass` into. It is created if it does not exist. | `/usr/local/bin` if writable, otherwise `~/.local/bin` |
| `GEASS_REPO` | The GitHub `owner/name` to download releases from, for a fork. | `degoke/geass` |
| `GEASS_DOWNLOAD_BASE` | Where to download the release files from instead: a mirror, or a directory as `file:///path`. It has to hold `geass-cli-<os>-<arch>` and `SHA256SUMS`. | the release on GitHub |

The variables belong to `sh`, not to `curl`, so set them after the pipe. To install a given release into a folder of
your choice:

```bash
curl -sfL https://github.com/degoke/geass/releases/download/v0.2.0/install-cli.sh | GEASS_VERSION=v0.2.0 GEASS_INSTALL_DIR="$HOME/bin" sh
```

Use a tag whose release carries the `geass-cli-*` files. To install for every user, put `sudo` before `sh` and pass the
variables with `env`: `... | sudo env GEASS_VERSION=v0.2.0 sh`.

**Install by hand.** Every release carries one binary per system, named `geass-cli-<os>-<arch>`:

| File | System |
| --- | --- |
| `geass-cli-linux-amd64`, `geass-cli-linux-arm64` | Linux |
| `geass-cli-darwin-amd64`, `geass-cli-darwin-arm64` | macOS |
| `geass-cli-windows-amd64.exe` | Windows (the install script does not support Windows) |

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/geass-cli-linux-amd64 -o geass
chmod +x geass
sudo mv geass /usr/local/bin/geass
```

On Windows, download `geass-cli-windows-amd64.exe` from the release page and put it in a folder on your `PATH`. Rename it
to `geass.exe` to type `geass`.

**Build from source.** From a checkout of the repository, with Go installed:

```bash
make build-cli CLI_GOOS=linux CLI_GOARCH=arm64 CLI_VERSION=v0.2.0
```

This writes `bin/geass-cli-linux-arm64`. Without the variables it builds for your own system and takes the version from
`git describe`. To update or reinstall, run the install script again: it replaces the file.

## Sign in

The command line signs in with a code that you approve in the dashboard, so you never type a password or paste a token
into the terminal.

1. Run `geass login` with the address of your dashboard:

   ```bash
   geass login --url https://geass.example.com
   ```

   It prints a code and the address of the approval page. It also opens that page in your browser when it runs in a
   terminal. Add `--no-browser` to stop it, for instance over SSH.

   ```text
   To sign in, open:

       https://geass.example.com/device

   and check that the code is  BCDF-GHJK

   Waiting for approval...
   ```

2. In the browser, sign in to the dashboard as an admin if it asks. The page **Sign in the command line** opens. If you
   opened the address yourself, enter the code in **Code**. The page names the machine that is asking, such as
   `geass CLI on laptop`, and shows the code it found. Check that this is the code in your terminal. Only approve a
   code you started.
3. Choose the **Access** the command line gets:

   | Choice | Role | What the command line can do |
   | --- | --- | --- |
   | **Read only, including logs (recommended)** | `observer` | Read everything, including logs and the names of variables. Change nothing. |
   | **Read only, without logs** | `viewer` | Read resources. No logs, no variables. Change nothing. |
   | **Full access** | `admin` | Everything the dashboard can do. |

4. Choose when it **Expires**: **7 days**, **30 days** (the default) or **90 days**.
5. Choose **Approve**. The page says `Approved. You can close this page: the command line is signed in.` and the
   terminal finishes:

   ```text
   Signed in to https://geass.example.com as token:CLI: geass CLI on laptop (read-only, with logs)
   ```

Choose **Refuse** to turn the request down. The terminal then stops with `the sign-in was refused in the dashboard`.

Approving creates an API token for the command line and hands it over once. It appears in **Settings** → **Tokens**
as `CLI: geass CLI on <your machine's name>`, with the access and expiry you chose, and you can revoke it there. The
code is good for 10 minutes. If you do not approve it in that time, the command ends with `the sign-in expired` or
`the sign-in was not approved in time` and exit code `6`. Run `geass login` again.

:::note
Only a person signed in to the dashboard with a password can approve a sign-in. A token, even an admin token, cannot
approve one, so a leaked token cannot mint another that outlives it.
:::

### Check who you are

```bash
geass whoami
```

```text
token:CLI: geass CLI on laptop on https://geass.example.com
access: read-only, with logs
can change things: no
expires: 2026-11-11 09:00 UTC (in 30 days)
```

The last line is when the token stops working, with the time left, or `never` for a token with no expiry. Sign in again
before it passes. `geass whoami --json` prints the same facts for a program, including `expiresAt` (absent when the
token never expires). `geass config get-contexts` shows the expiry day of each saved login.

### Sign in with a token you made

For a script, or when you cannot open a browser, create a token under **Settings** → **Tokens** (see
[Users and API tokens](/docs/guides/users-and-tokens/)) and give it to `geass login`. Prefer standard input or an
environment variable to `--token`, which shows in the process list:

```bash
echo "$TOKEN" | geass login --url https://geass.example.com --token-stdin
GEASS_TOKEN=gsw_1a2b3c4d_0123456789abcdef0123456789abcdef01234567 geass login --url https://geass.example.com
```

The token is checked with the server before it is saved. A token that does not work is not saved.

### Where the login is kept

`geass login` writes `config.json` in the `geass` folder of your user configuration directory (on Linux
`~/.config/geass/config.json`, on macOS `~/Library/Application Support/geass/config.json`, on Windows
`%AppData%\geass\config.json`). The file holds the token, so it is written with mode `0600` and its folder with `0700`.
Set `GEASS_CONFIG` to use another path. The file format is in [CLI commands](/docs/reference/cli/#configuration-file).

### Use several clusters

Each login is saved under a name, by default the host of the address. Sign in to a second cluster with a name of your
choice, then list the logins and switch:

```bash
geass login --url https://staging.example.com --name staging
geass config get-contexts
geass config use-context geass.example.com
geass --context staging apps list
```

`geass login` makes the login it saves the current one. `--context NAME` (or `GEASS_CONTEXT`) uses another login
for one command.

A login belongs to its address, and its token is sent only there. `--url` (or `GEASS_URL`) with another address does not
carry the current login's token to it: the command uses the login you saved for that address, or stops with
``not signed in to ADDRESS: run `geass login --url ADDRESS` `` if there is none, or if there are several (say which
with `--context`). A login you chose with `--context` or `GEASS_CONTEXT` is never swapped for another. A token you give
with `--token` or `GEASS_TOKEN` is used as you gave it.

### Set a default project and environment

Most commands need to know which project and environment you mean. Save them with the login, or give them each time:

```bash
geass config set-context -p proj-1a2b3c4d -e production
geass status                      # uses the saved defaults
geass status -p proj-1a2b3c4d -e staging
```

A project is written as its ID, such as `proj-1a2b3c4d`. `geass projects list` prints the IDs, and
`geass projects get proj-1a2b3c4d` shows the display name. For one command you can also set `GEASS_PROJECT` and
`GEASS_ENVIRONMENT`. A flag beats an environment variable, and an environment variable beats the saved default.

### Sign out

```bash
geass logout
```

This forgets the token on your machine. The token itself stays valid until it expires. To end it now, revoke it under
**Settings** → **Tokens**.

## Look around

```bash
geass projects list
geass apps list -p proj-1a2b3c4d -e production
geass status -p proj-1a2b3c4d -e production
geass apps get web
```

```text
NAME  PROJECT        ENV         STATE  DETAIL                   AGE
web   proj-1a2b3c4d  production  ready  https://web.example.com  3h
api   proj-1a2b3c4d  production  draft  nginx:alpine             3d
```

A service's `STATE` is `ready`, `pending` (being set up or rolled out), `failed`, `draft` (saved and not deployed yet)
or `deleting`. Service names are unique across the whole install, which is why most commands take only the name.
`geass status` adds the replicas, the latest build and the reason a service is not ready:

```text
NAME  STATE   READY  BUILD      URL                      MESSAGE
web   ready   2/2    Succeeded  https://web.example.com  -
api   failed  0/1    Failed     -                        Kaniko completed without an immutable image digest
```

`geass databases list`, `geass stores list` and `geass nodes list` do the same for databases, buckets and the cluster's
nodes. `geass apps get NAME` (and `projects get`, `databases get`, `stores get`) prints the whole resource as JSON: its
`resource` summary, its `spec` and its `status`.

## Create and deploy a service

Creating a service saves a draft. Nothing runs until you deploy it, the same as in the dashboard (see
[Projects and environments](/docs/guides/projects-and-environments/)). Add `--deploy` to deploy it as it is saved.

```bash
geass apps create web -p proj-1a2b3c4d -e production --image nginx:alpine --port 80
geass deploy web --wait
```

```text
Created web as a draft. Deploy it with `geass deploy web`.
web: deploying
web: pending (0 of 1 ready)
web: ready at https://web.example.com
NAME  STATE  READY  BUILD  URL                      MESSAGE
web   ready  1/1    -      https://web.example.com  -
```

The progress lines depend on what the service is doing, so yours can differ.

`geass apps create` takes exactly one source:

| Source | Flags | Notes |
| --- | --- | --- |
| A container image | `--image nginx:alpine` | `--port` sets the port it serves on. |
| A GitHub repository | `--repo acme/storefront`, `--branch main` | The project must be linked to GitHub in the dashboard first. See [GitHub and builds](/docs/guides/github-and-builds/). |
| A function | `--function hook.mjs`, `--runtime node`, `--mode event` | The file on your machine becomes the function's code. See [Functions](/docs/guides/functions/). |

With `--deploy` the command prints `Created NAME and started deploying it`, and the service is deploying. Follow it with
`geass status web`. A cluster with no room for the service refuses the command before anything is saved, so you are
not left with a draft you did not ask for.

### Deploy and wait

`geass deploy` deploys the pending changes of the services you name, or everything pending in an environment:

```bash
geass deploy web api --wait
geass deploy -p proj-1a2b3c4d -e production --wait
```

Without `--wait` the command returns as soon as Geass accepts the deploy. With `--wait` it keeps reading the service's
status until it is done, and prints one progress line each time something changes, on standard error:

| Line | Meaning |
| --- | --- |
| `web: build running` | A repository build is in progress. The phase follows the build: `pending`, `waitingforci`, `running`. |
| `web: pending (1 of 2 ready)` | The build is done and the copies are starting. |
| `web: ready at https://web.example.com` | Done. |

A service is done when it is ready, has no pending changes and its latest build, if it has one, succeeded. If its state
becomes `failed` or its build fails, the command exits with code `5` and says why:

```text
Error: the deploy failed: web: the deploy failed: Kaniko completed without an immutable image digest
```

`--timeout` (default `10m`) is how long `--wait` waits for each service. When a wait runs out, the command exits with
code `6`. The service is not rolled back: look at it with `geass status web`.

Deploying a service records a new deployment even when nothing was pending.

### Build again

A service that is built from a repository is built again by `geass build`, which does what a push to its branch does:

```bash
geass build web --wait
geass build -p proj-1a2b3c4d -e production --wait --timeout 20m
```

It takes the same `--wait` and `--timeout`, prints `web: build started` and then the same progress lines, and exits
`5` or `6` in the same cases. With no names it builds every repository service of the environment. It does not apply
changes saved to the service and not yet deployed: use `geass deploy` for those. A service that runs an image or a
function has no build to run, and the command says so. For a pipeline that builds on a schedule, see
[Build from GitHub Actions](/docs/guides/github-and-builds/#build-from-github-actions).

## Read logs

```bash
geass logs web -f --level error --since 15m
```

```text
09:41:07 x2k4p Error: connect ECONNREFUSED 203.0.113.10:5432
09:41:09 x2k4p Error: connect ECONNREFUSED 203.0.113.10:5432
```

Each line is the time in UTC, the end of the pod's name and the text. Without `-f` the command prints the recent lines
and stops. `-f` keeps following, across restarts and new pods, until you press Ctrl-C. The filters are `--tail` (the
newest lines to show, default 200, at most 1000), `--since` (such as `15m` or `48h`, at most 7 days), `--level`
(`error`, `warn`, `info` or `debug`) and `--search` (lines containing this text). When nothing matches, `No log lines
matched.` is printed on standard error.

The filters look through the most recent lines. The dashboard reads `--tail` lines from the end of each copy's log
first and filters them after, so `--level error` finds the errors among each copy's last 200 lines. To look further
back, raise `--tail`.

Reading logs needs **Read only, with logs**, **Full access**, or a project token for its own services.

Build and deploy logs are not a command. List the deployments of a service with `geass apps deployments web`, then read
one deployment's logs by its ID with the raw API:

```bash
geass api GET /apps/web/deployments/web-abc12/logs | jq -r '.sections[] | "== " + .title, .lines'
```

## Variables and secrets

```bash
geass env list web
geass env set web LOG_LEVEL=debug FEATURE_X=on
geass env set web --secret STRIPE_KEY=sk_test_example
geass env unset web FEATURE_X
```

```text
NAME        KIND
LOG_LEVEL   config
STRIPE_KEY  secret
```

Variables are pending changes. They reach the running service when you deploy, or immediately if you add `--deploy`
(and `--wait` to wait for the result):

```bash
geass env set web LOG_LEVEL=debug --deploy --wait
```

Without `--deploy` the command says ``The change is pending. Run `geass deploy web` to apply it.`` Only the first `=` in
`KEY=VALUE` splits it, so a value can contain `=`. A secret is stored apart from ordinary configuration, and is never
printed back. See [Environment variables](/docs/guides/environment-variables/).

:::note
Only secrets are hidden. Plain configuration values are returned by the API: `geass env list --json` and
`geass apps get` (with **Full access**) print them. Store anything sensitive with `--secret`.
:::

## Scale, sleep and roll back

```bash
geass scale web 3
geass scale web --sleep-after 10m
geass scale web --always-on
```

`geass scale` takes effect at once, with no deploy. The replica count is 0 to 1000. `--sleep-after` (30 seconds to 24
hours) stops the service after that long without requests and starts it on the next request; `--always-on` turns
that off. It needs the serverless add-on to be on in the cluster settings and a service with a public host, and it
cannot be combined with CPU autoscaling. See [Scale to zero](/docs/guides/scale-to-zero/). The command saves the setting,
then waits up to 10 seconds for the cluster to say whether the service can sleep. It prints `web sleeps after ...` only
when it can. When it cannot, the service stays running and the command exits `1` with the reason, such as
`saved, but web cannot sleep when idle and stays running: The serverless add-on is not installed`. The setting stays
saved, so it takes effect once the add-on is on. `geass apps get web` shows `resource.details.scaleToZero`, where
`active` says whether it does.

To go back to an earlier deployment:

```bash
geass apps deployments web                # newest first: the ids are what rollback takes
geass rollback web --wait                 # the deployment before the newest
geass rollback web web-abc12 --wait       # a deployment you choose
```

Rolling back deploys at once. It records a new deployment, so running `geass rollback web` a second time goes to the
entry before that one. Pass an id to pick. Functions cannot be rolled back: restore the earlier code and deploy again.

## Functions and schedules

A function runs code you give it (see [Functions](/docs/guides/functions/)). Create one from a file, add schedules and
call it:

```bash
geass apps create report -p proj-1a2b3c4d -e production --function report.mjs --runtime node --mode event
geass deploy report --wait
geass functions triggers add report nightly "0 3 * * *"
geass functions triggers add report sync "*/15 * * * *" --path /sync --payload '{"full":true}'
geass deploy report --wait
geass functions triggers list report
geass functions invoke report --path /sync --data '{"full":true}'
```

```text
NAME     SCHEDULE (UTC)  PATH
nightly  0 3 * * *       /
sync     */15 * * * *    /sync
```

A schedule is a cron expression in UTC with five fields, or `@hourly`, `@daily`, `@weekly`, `@monthly` or `@yearly`.
Each run is a `POST` to the function. Adding a schedule with a name that already exists replaces it. Schedules start
when the function is next deployed, so deploy after adding or removing one. A function can have 8 schedules.

`geass functions invoke` calls the function at its public address, as any visitor would. The status line goes to
standard error and the body to standard output. A status of 400 or above also makes the command exit with code `1`.

## Metrics

```bash
geass metrics web --window 6h
geass metrics orders --kind databases
```

```text
SERIES     LATEST (MIN - MAX)
cpu        0.0123 (0.0101 - 0.0456)
memory     1.34e+08 (1.3e+08 - 1.36e+08)
network    2.1e+03 (1.2e+03 - 9.8e+03)
networkIn  1.7e+03 (900 - 8.4e+03)
requests   4.2 (1.1 - 9.7)
```

Windows are `15m`, `1h` (the default), `6h` and `24h`. Metrics need Prometheus. Without it the command exits with
`no metrics yet: ...`. See [Observability](/docs/guides/observability/).

## Databases, nodes and compliance

```bash
geass databases connection orders
geass nodes tag worker-2 --region eu --tags hipaa --compliance hipaa
geass compliance set proj-1a2b3c4d hipaa
geass compliance show proj-1a2b3c4d
```

`geass databases connection` needs **Full access**. It shows the host, port, database and user. The password and
connection string stay hidden unless you add `--reveal`, and a reveal is written to the operator log. `geass nodes tag`
replaces a node's region, zone and tags, so give every tag the node should keep. It prints each warning the change
brings on standard error as `warning: ...`, such as work already on the node that does not belong there. See
[Compliance tags](/docs/guides/compliance-tags/).

## Clouds, pools and servers

With an admin token, `geass` can connect a cloud, size a pool and add servers. These commands make Geass create machines at a cloud, which costs money, and the clouds behind them are experimental: see [Cloud connections for machines](/docs/guides/connectors/clouds/).

```bash
geass providers Hetzner                                              # what the cloud takes
geass connections add hetzner --provider Hetzner --credential-file token=./hetzner-token.txt
geass sizes --connection hetzner --region fsn1 --min-vcpus 2 --limit 5
geass pools create burst --connection hetzner --region fsn1 --size cx23 --min 0 --max 5 --autoscale
geass pools show burst
geass servers add --connection hetzner --region fsn1 --size cx23     # one machine, until you remove the server
geass nodes list                                                     # POOL and EPHEMERAL show what came from where
```

Give a secret from a file or the environment, never on the command line. A pool cannot be made without `--max`, the most machines it can ever have and the cap on what it costs. See [Node pools and autoscaling](/docs/guides/node-pools/) and the [reference](/docs/reference/cli/#clouds-and-machines).

## Anything else: the raw API

`geass api` calls any `/api/v1` endpoint and prints the JSON it answers. It covers whatever has no command of its own,
such as deleting a database or restoring a backup:

```bash
geass api GET /apps -q project=proj-1a2b3c4d
geass api POST /apps/web/scale -d '{"replicas": 3}'
geass api DELETE /databases/orders -q confirmName=orders
geass api POST /databases/restore -d '{"archive": "orders-backup-1a2b3c"}'
geass openapi > geass-openapi.json
```

`-d` takes a JSON string, `@file` or `-` for standard input. `geass openapi` prints the OpenAPI description of every
endpoint, with who may call it. For restores see [Backups and restore](/docs/guides/backups-and-restore/).

To delete a service there is a command. It asks first, and a script has to say `--yes`:

```bash
geass apps delete web --yes
```

## Output, JSON and exit codes

**Two streams.** Results go to standard output. Progress and notes go to standard error. A program that reads standard
output sees only results.

**JSON.** Add `--json` to the commands that print data: a list, a status, `apps deployments`, `whoami`, `version`,
`nodes list`, `compliance show`, `env list`, `metrics`, the `functions` commands that print something, or `logs`. Lists
print `{"items": [...]}`:

```bash
geass apps list -p proj-1a2b3c4d -e production --json | jq -r '.items[] | select(.state == "failed") | .name'
geass status --json | jq '.items[] | {name, state, ready}'
geass logs web -f --json | jq -c 'select(.level == "error") | .line'
```

`apps get`, `projects get`, `databases get`, `stores get`, `api` and `openapi` always print JSON. A few commands print
plain text lines whatever `--json` says (`scale`, `rollback`, `env set`, `apps create`, `apps delete`, `nodes tag`,
`compliance set`). `geass logs --json` prints one indented JSON object per log line, which `jq` reads as a stream.

**Exit codes.** A script can tell what failed from the code:

| Code | Meaning |
| --- | --- |
| `0` | Success |
| `1` | Any other error, including a network failure or a `400` from the API |
| `2` | Usage error: an unknown command or flag, a missing argument, a delete with no `--yes` and nobody to ask |
| `3` | Not found (`404`) |
| `4` | Not allowed: not signed in, `401` or `403`, or the sign-in was refused |
| `5` | A deploy failed (`--wait` saw a failed build or rollout) |
| `6` | Timed out (`--wait` ran out of time, or a sign-in was not approved in time) |

```bash
#!/usr/bin/env bash
status=0
geass deploy web --wait --timeout 15m || status=$?
case "$status" in
  0) echo "web is ready" ;;
  5) echo "the deploy failed, see the message above" >&2; exit 1 ;;
  6) echo "web did not become ready in time" >&2; exit 1 ;;
  *) echo "geass exited with code $status" >&2; exit 1 ;;
esac
```

## Scripts and CI

A script does not need `geass login` or a configuration file. Set two environment variables:

| Variable | Meaning |
| --- | --- |
| `GEASS_URL` | The dashboard address, such as `https://geass.example.com`. |
| `GEASS_TOKEN` | An API token. |

Add `GEASS_PROJECT` and `GEASS_ENVIRONMENT` if the script works in one environment.

**Choose the smallest token.** Create it under **Settings** → **Tokens** (see
[Users and API tokens](/docs/guides/users-and-tokens/)) and keep it in your CI system's secret store.

| Token | Use it for |
| --- | --- |
| Project token (**Settings** → **Tokens** inside a project) | CI that deploys one environment. It can deploy, scale, roll back, edit variables and read logs of that environment, and nothing else. |
| **Read only, with logs** | Monitoring, reports and agents that only watch. |
| **Full access** | Scripts that create services, change nodes or delete things. |

A project token works with `whoami`, `version`, `apps list` and `get`, `status`, `deploy NAME`, `build`, `logs -f`, `env`, `scale`,
`rollback`, `metrics`, `functions` and the parts of `geass api` that its routes allow. It is refused by `projects`,
`databases`, `stores`, `nodes`, `compliance`, `apps create` and `apps delete`, `openapi`, and `logs` without `-f`. Name
the services in `geass deploy`: `geass deploy` with no names deploys by project and is refused for a project token.

A job that updates an image and waits for it, in GitHub Actions:

```yaml title=".github/workflows/deploy.yml"
name: deploy
on:
  push:
    branches: [main]
jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - name: Install the geass command line
        run: |
          curl -sfL https://github.com/degoke/geass/releases/latest/download/install-cli.sh \
            | GEASS_INSTALL_DIR="$HOME/.local/bin" sh
          echo "$HOME/.local/bin" >> "$GITHUB_PATH"
      - name: Deploy
        env:
          GEASS_URL: https://geass.example.com
          GEASS_TOKEN: ${{ secrets.GEASS_TOKEN }}
        run: |
          geass api PATCH /apps/web -d "{\"image\": \"ghcr.io/acme/storefront:${GITHUB_SHA}\"}"
          geass deploy web --wait --timeout 15m
```

The first command saves the new image as a pending change. The second deploys it and fails the job (exit code `5` or
`6`) if the service does not come up.

For a service built from a repository, `geass build web --wait` runs its build again and follows it through to the
rollout, with the same exit codes. See [Build from GitHub Actions](/docs/guides/github-and-builds/#build-from-github-actions).

In CI nobody can answer a question, so `geass apps delete` needs `--yes`. If the job runs where there is no home
directory, such as a bare container, set `GEASS_CONFIG` to any path, for example `GEASS_CONFIG=/tmp/geass.json`:
without a configuration directory every command stops with `could not find a configuration directory`.

## What happens under the hood

Every command makes one or two HTTPS calls to `<address>/api/v1`, with `Authorization: Bearer <token>` and the user
agent `geass-cli/<version>`. A call times out after 60 seconds. Following logs has no timeout. The API turns each call
into the dashboard's own operation, so the same rules apply as in the dashboard, and answers every error as
`{"error": "...", "code": "..."}`. `geass api GET /openapi.json` (or `geass openapi`) describes every endpoint.

- **Sign-in.** `geass login` calls `POST /api/v1/auth/device`, which answers with a device code, an 8-letter user code
  (shown as `BCDF-GHJK`) and the approval address. The operator keeps pending requests in memory for 10 minutes, so
  restarting the operator cancels them. The command asks `POST /api/v1/auth/token` every 5 seconds. One address can
  start 10 sign-ins a minute, and at most 100 can wait at once. When an admin approves, the operator creates a
  workspace API token named `CLI: <client name>` and hands it over once. Only a hash is stored, in the Secret
  `geass-api-tokens` in the `geass-system` namespace, like every API token. The operator log records it:

  ```bash
  kubectl -n geass-system logs deployment/geass-controller-manager -c manager | grep "command-line sign-in"
  ```

- **Services.** A service you create is a `GeassApp` in `geass-system` (`kubectl -n geass-system get geassapps`). A
  deploy is the dashboard's **Deploy changes**. See [Projects and environments](/docs/guides/projects-and-environments/).
- **Audit.** Each API call is logged as `Dashboard request completed` with the method, path and status, but not the
  caller. A token's last use is in **Settings** → **Tokens**. Use one token per script or person so you can tell them
  apart and revoke one without the others.

## Troubleshooting

**`` Error: not signed in: run `geass login --url https://your-geass-address` ``** (exit 4). There is no saved login, and no
`GEASS_URL`. Run `geass login --url ...`, or set `GEASS_URL` and `GEASS_TOKEN`.

**`` Error: not signed in to https://geass.example.com: run `geass login` ``** (exit 4). The address is known, but there is
no token: you ran `geass logout`, or only `GEASS_URL` is set.

**`Error: invalid or expired API token`, then ``Run `geass login` to sign in again.``** (exit 4). The token expired or was
revoked. Run `geass login` again.

**`Error: ... (your token may not do this)`** (exit 4). The API refused with `403`. The text before the brackets says
why: `viewer role cannot access this` means a **Read only** token tried to read logs or change something, `this token
cannot access that endpoint` means a project token called something outside its list, and `only a workspace admin can
read connection details` means the token is not **Full access**. Sign in again with more access, or use another token.

**`Error: app not found`** (exit 3). There is no service with that name, or it is outside the project token's
environment. `geass apps list` shows the names.

**`Error: this deletes data; run it again with --yes to confirm`** (exit 2). Standard input is not a terminal, so there
was nobody to ask. Add `--yes`.

**`Get "geass.example.com/api/v1/me": unsupported protocol scheme ""`.** The address has no `https://`. Write the
whole address.

**`the API answered something that is not JSON`.** The address does not point at the Geass dashboard, or something in
front of it answers with a web page. Open the address in a browser, and see [Sign in](/docs/getting-started/sign-in/).

**`the API answered in a shape this client does not know (...): is Geass a different version?`** The address is a Geass,
but one whose answer this command line does not expect, usually an older dashboard than the command line. Update
whichever is older.

**A certificate error such as `x509: certificate signed by unknown authority`.** The command line checks the server's
certificate and has no option to skip the check. Fix the certificate (see [Domains and TLS](/docs/guides/domains-and-tls/))
or trust your certificate authority on this machine. For a first session without a domain, reach the dashboard through
`kubectl port-forward` and use `--url http://localhost:8085`. Standard proxy variables such as `HTTPS_PROXY` apply.

**The approval page shows `No sign-in request has that code. It may have expired: start again from the command line.`**
The code is wrong, has expired after 10 minutes, or the operator restarted. Run `geass login` again.

**The page says `Only an admin can approve this`.** You are signed in to the dashboard as a viewer. Sign in as an admin.

**`geass login` ends with `the sign-in was not approved in time` or `the sign-in expired` (exit 6).** Nobody approved
the code in 10 minutes. Run it again and approve sooner.

**`could not start the sign-in: too many sign-in requests, try again in a minute`.** One address started 10 sign-ins in
a minute, or 100 are waiting. Wait a minute. Behind a proxy, every user shares the proxy's address.

**The address printed by `geass login` starts with `http://` but the dashboard is on HTTPS.** The operator builds the
address from the request. A proxy that does not send `X-Forwarded-Proto: https` makes it print `http://`. Change it to
`https://` in the browser. The code still works.

**`gave up waiting for web after 10m0s (it is pending): context deadline exceeded`** (exit 6). The service did not become
ready in time. Run `geass status web` for the reason, raise `--timeout` for a slow build, or check that the cluster has
room (see [Usage and capacity](/docs/guides/usage-and-capacity/)).

**`the deploy failed: ...`** (exit 5). The text after the colon is the build's failure reason, or the service's message.
For a repository build, read the build logs: `geass apps deployments web`, then
`geass api GET /apps/web/deployments/ID/logs` for the newest deployment.

**`geass logs web` prints nothing, and `No log lines matched.` is on standard error.** The service wrote nothing that
matches in the lines the command looked through. Drop `--level` and `--search`, or raise `--tail` and `--since`. See
[Read logs](#read-logs).

**``not signed in to https://...: run `geass login --url https://...` ``** (exit 4). You gave `--url` or `GEASS_URL` for an
address you have no login for, and the current login's token is not sent there. Run `geass login --url` for that
address, or give a token for it with `--token`.

**`no metrics yet: ...`.** Prometheus is not installed or not reachable. See [Observability](/docs/guides/observability/).

**`--sleep-after must be between 30s and 24h`** (exit 2). The value is out of range. A value with no unit, such as
`600`, is refused earlier, with `invalid argument "600" for "--sleep-after" flag`: write `600s` or `10m`.

**`Scale to zero decides how many copies run, so turn CPU autoscaling off to use it`** (exit 1). The service has CPU
autoscaling on. Turn it off in the service's **Settings**, then run the command again.

**`could not find a configuration directory (set GEASS_CONFIG)`.** The machine has no home or configuration directory.
Set `GEASS_CONFIG` to a path you can write.

**`<path> is not valid: ...`.** The configuration file is not valid JSON. Fix it or delete it and sign in again.

**`signed in, but could not save the login: ...`.** The token works, but the configuration file or its folder is not
writable. Fix the permissions, or set `GEASS_CONFIG`.

**`geass: command not found`.** The install folder is not on your `PATH`. The install script prints which one it used.

## Related

- [CLI commands](/docs/reference/cli/)
- [MCP server for AI agents](/docs/guides/mcp-server/)
- [Users and API tokens](/docs/guides/users-and-tokens/)
- [REST API](/docs/reference/rest-api/)
- [Projects and environments](/docs/guides/projects-and-environments/)
- [Functions](/docs/guides/functions/)
- [Scale to zero](/docs/guides/scale-to-zero/)
