---
title: "Command line"
description: "Install the geass command line, sign in with a code that you approve in the dashboard, and deploy, watch, scale, and configure services from a terminal or CI."
---

`geass` is a command line for a running Geass cluster. You can use it from your terminal or from a CI job to do these
tasks:

- List projects and services.
- Deploy, and wait for the result.
- Read logs.
- Change variables.
- Scale and roll back.
- Schedule functions.
- Call any endpoint of the API.

Commands print a table for a person, and most commands can print JSON for a program. Every command ends with an exit
code that a script can act on.

The command line is not the installer. You run the installer one time to set up Geass. See
[Install Geass](/docs/getting-started/install/). The command line talks to a Geass that already runs, through the same
[REST API](/docs/reference/rest-api/) as the dashboard. [CLI commands](/docs/reference/cli/) lists every command and
flag.

## Before you start

You need these things:

- The address of your dashboard. See [Sign in](/docs/getting-started/sign-in/).
- A dashboard login with the **admin** role, to approve the sign-in of the command line.
- `curl` and `sh` on the machine where you install the command line.

## Install

Run the install script from the latest release:

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install-cli.sh | sh
```

The script works on Linux and macOS, on `amd64` and `arm64`. It installs `geass` in `/usr/local/bin` when you can
write there, and otherwise in `~/.local/bin`. If that folder is not on your `PATH`, the script tells you. Check the
installed version:

```bash
geass version
```

```text
geass v0.2.0
```

The script checks the download against the `SHA256SUMS` file of the release, and installs nothing that does not
match. The script reads these variables:

| Variable | Meaning | Default |
| --- | --- | --- |
| `GEASS_VERSION` | The release tag to install. | `latest` |
| `GEASS_INSTALL_DIR` | The folder to install `geass` into. The script creates the folder if it does not exist. | `/usr/local/bin` if writable, otherwise `~/.local/bin` |
| `GEASS_REPO` | The GitHub `owner/name` to download releases from, for a fork. | `degoke/geass` |
| `GEASS_DOWNLOAD_BASE` | A different location to download the release files from: a mirror, or a directory as `file:///path`. It must hold `geass-cli-<os>-<arch>` and `SHA256SUMS`. | the release on GitHub |

Set the variables after the pipe, for `sh`. To install a specific release into a folder that you select, run this
command:

```bash
curl -sfL https://github.com/degoke/geass/releases/download/v0.2.0/install-cli.sh | GEASS_VERSION=v0.2.0 GEASS_INSTALL_DIR="$HOME/bin" sh
```

To install for every user, use `... | sudo env GEASS_VERSION=v0.2.0 sh`. To update, run the install script again.

**Install by hand.** Every release has one binary for each system:

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

On Windows, download `geass-cli-windows-amd64.exe` from the release page. Rename it to `geass.exe`, and put it in a
folder on your `PATH`.

**Build from source.** From a checkout of the repository, with Go installed, run
`make build-cli CLI_GOOS=linux CLI_GOARCH=arm64`. The command writes `bin/geass-cli-linux-arm64`.

## Sign in

The command line signs in with a code that you approve in the dashboard. You never type a password or paste a token
into the terminal.

1. Run `geass login` with the address of your dashboard:

   ```bash
   geass login --url https://geass.example.com
   ```

   The command prints a code and the address of the approval page. When the command runs in a terminal, it also opens
   that page in your browser. To stop that, add `--no-browser`, for example over SSH.

   ```text
   To sign in, open:

       https://geass.example.com/device

   and check that the code is  BCDF-GHJK

   Waiting for approval...
   ```

2. In the browser, if the dashboard asks, sign in as an admin. The page **Sign in the command line** opens.
3. If you opened the address yourself, enter the code in **Code**.
4. Check the code on the page. The page names the machine that asks, for example `geass CLI on laptop`, and shows the
   code that it found. Make sure that this code is the code in your terminal. Approve only a code that you started.
5. Select the **Access** that the command line gets:

   | Choice | Role | What the command line can do |
   | --- | --- | --- |
   | **Read only, including logs (recommended)** | `observer` | Read everything, including logs and the names of variables. Change nothing. |
   | **Read only, without logs** | `viewer` | Read resources. No logs, no variables. Change nothing. |
   | **Full access** | `admin` | Everything that the dashboard can do. |

6. Select when the access **Expires**: **7 days**, **30 days** (the default), or **90 days**.
7. Click **Approve**. The page says `Approved. You can close this page: the command line is signed in.` and the
   terminal finishes:

   ```text
   Signed in to https://geass.example.com as token:CLI: geass CLI on laptop (read-only, with logs)
   ```

To refuse the request, click **Refuse**. The terminal then stops with `the sign-in was refused in the dashboard`.

When you approve, Geass creates an API token for the command line. The token shows in **Settings** → **Tokens** as
`CLI: geass CLI on <your machine's name>`. You can revoke it there. The code is valid for 10 minutes. If nobody
approves it in that time, the command ends with exit code `6`. Run `geass login` again.

:::note
Only a person signed in to the dashboard can approve a sign-in. A token cannot approve one, not even a
**Full access** token. So a leaked token cannot make another token that lives longer than it.
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

The last line is when the token stops working, with the time left. For a token with no expiry, it is `never`. Sign in
again before that time. `geass whoami --json` prints the same facts for a program, including `expiresAt`. A token that
never expires has no `expiresAt`. `geass config get-contexts` shows the expiry day of each saved login.

### Sign in with a token you made

For a script, or when you cannot open a browser, create a token under **Settings** → **Tokens** (see
[Users and API tokens](/docs/guides/users-and-tokens/)). Then give the token to `geass login`. Give the token through
standard input or an environment variable, not with `--token`. The process list shows the value of `--token`.

```bash
echo "$TOKEN" | geass login --url https://geass.example.com --token-stdin
GEASS_TOKEN=gsw_1a2b3c4d_0123456789abcdef0123456789abcdef01234567 geass login --url https://geass.example.com
```

The command line checks the token with the server before it saves the token. It does not save a token that does not
work.

### Where the login is kept

`geass login` writes `config.json` in the `geass` folder of your user configuration directory:

- On Linux: `~/.config/geass/config.json`.
- On macOS: `~/Library/Application Support/geass/config.json`.
- On Windows: `%AppData%\geass\config.json`.

The file holds the token, so only your user can read it. To use a different path, set `GEASS_CONFIG`.
[CLI commands](/docs/reference/cli/#configuration-file) gives the file format.

### Use several clusters

The command line saves each login under a name. By default, the name is the host of the address. To use a second
cluster, sign in to it with a name that you select. Then list the logins and switch:

```bash
geass login --url https://staging.example.com --name staging
geass config get-contexts
geass config use-context geass.example.com
geass --context staging apps list
```

`geass login` makes the login that it saves the current login. To use a different login for one command, use
`--context NAME` (or `GEASS_CONTEXT`).

A login belongs to its address. The command line sends a token only to the address of its login. If you give another
address with `--url` or `GEASS_URL`, the command uses the login saved for that address. If there is none, the command
stops with ``not signed in to ADDRESS: run `geass login --url ADDRESS` ``. If there are several, use `--context` to
select one.

### Set a default project and environment

Most commands must know which project and environment you mean. Save them with the login, or give them each time:

```bash
geass config set-context -p proj-1a2b3c4d -e production
geass status                      # uses the saved defaults
geass status -p proj-1a2b3c4d -e staging
```

You write a project as its ID, for example `proj-1a2b3c4d`. `geass projects list` prints the IDs, and
`geass projects get proj-1a2b3c4d` shows the display name. For one command, you can also set `GEASS_PROJECT` and
`GEASS_ENVIRONMENT`. A flag has priority over an environment variable. An environment variable has priority over the
saved default.

### Sign out

```bash
geass logout
```

This command removes the token from your machine. The token itself stays valid until it expires. To end the token now,
revoke it under **Settings** → **Tokens**.

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

The `STATE` of a service is one of these values:

- `ready`.
- `pending`: Geass is setting up or rolling out the service.
- `failed`.
- `draft`: the service is saved and not deployed yet.
- `deleting`.

Service names are unique across the whole installation, so most commands take only the name. `geass status` adds the
running copies, the latest build, and the reason that a service is not ready:

```text
NAME  STATE   READY  BUILD      URL                      MESSAGE
web   ready   2/2    Succeeded  https://web.example.com  -
api   failed  0/1    Failed     -                        Kaniko completed without an immutable image digest
```

`geass databases list`, `geass stores list`, and `geass nodes list` do the same for databases, buckets, and servers.
`geass apps get NAME` prints all the details of a service as JSON. `projects get`, `databases get`, and `stores get`
do the same.

## Create and deploy a service

When you create a service, Geass saves a draft. Nothing runs until you deploy the service, the same as in the
dashboard (see [Projects and environments](/docs/guides/projects-and-environments/)). To deploy the service when Geass
saves it, add `--deploy`.

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

The progress lines depend on what the service does, so your lines can be different.

`geass apps create` takes exactly one source:

| Source | Flags | Notes |
| --- | --- | --- |
| A container image | `--image nginx:alpine` | `--port` sets the port that the image serves on. |
| A GitHub repository | `--repo acme/storefront`, `--branch main` | First, link the project to GitHub in the dashboard. See [GitHub and builds](/docs/guides/github-and-builds/). |
| A function | `--function hook.mjs`, `--runtime node`, `--mode event` | The file on your machine becomes the code of the function. See [Functions](/docs/guides/functions/). |

With `--deploy`, the command prints `Created NAME and started deploying it`, and the service is deploying. To follow
it, run `geass status web`. If the cluster has no room for the service, the cluster refuses the command before Geass
saves anything. Thus, you do not get a draft that you did not ask for.

### Deploy and wait

`geass deploy` deploys the pending changes of the services that you name, or all pending changes in an environment:

```bash
geass deploy web api --wait
geass deploy -p proj-1a2b3c4d -e production --wait
```

Without `--wait`, the command returns immediately after Geass accepts the deploy. With `--wait`, the command reads the
status of the service until the deploy is done. Each time something changes, it prints one progress line on standard
error:

| Line | Meaning |
| --- | --- |
| `web: build running` | A repository build is in progress. The phase follows the build: `pending`, `waitingforci`, `running`. |
| `web: pending (1 of 2 ready)` | The build is done, and the copies are starting. |
| `web: ready at https://web.example.com` | Done. |

A service is done when all of these are true:

- The service is ready.
- The service has no pending changes.
- If the service has a build, its latest build succeeded.

If the state of the service becomes `failed`, or its build fails, the command exits with code `5` and gives the reason:

```text
Error: the deploy failed: web: the deploy failed: Kaniko completed without an immutable image digest
```

`--timeout` (default `10m`) is how long `--wait` waits for each service. When a wait runs out of time, the command
exits with code `6`. Geass does not roll back the service. To examine the service, run `geass status web`.

When you deploy a service, Geass records a new deployment, even when nothing was pending.

### Build again

To build again a service that Geass builds from a repository, use `geass build`. The command does the same thing that
a push to the branch of the service does:

```bash
geass build web --wait
geass build -p proj-1a2b3c4d -e production --wait --timeout 20m
```

The command takes the same `--wait` and `--timeout`. It prints `web: build started` and then the same progress lines.
It exits with `5` or `6` in the same cases. With no names, it builds every repository service of the environment.

The command does not apply changes that you saved to the service and did not deploy yet. For those changes, use
`geass deploy`. A service that runs an image or a function has no build to run, and the command tells you so. For a
pipeline that builds on a schedule, see
[Build from GitHub Actions](/docs/guides/github-and-builds/#build-from-github-actions).

## Read logs

```bash
geass logs web -f --level error --since 15m
```

```text
09:41:07 x2k4p Error: connect ECONNREFUSED 203.0.113.10:5432
09:41:09 x2k4p Error: connect ECONNREFUSED 203.0.113.10:5432
```

Each line has the time in UTC, a short ID of the copy of the service, and the text. Without `-f`, the command prints
the recent lines and stops. `-f` follows the log, across restarts and new copies, until you press Ctrl-C. The filters
are:

- `--tail`: the number of newest lines to show. The default is 200, and the maximum is 1000.
- `--since`: for example `15m` or `48h`. The maximum is 7 days.
- `--level`: `error`, `warn`, `info`, or `debug`.
- `--search`: lines that contain this text.

When nothing matches, the command prints `No log lines matched.` on standard error.

The filters look only through the last `--tail` lines of each copy. So `--level error` finds the errors in the last
200 lines. To look further back, increase `--tail`.

To read logs, you need **Read only, with logs**, **Full access**, or a project token for its own services.

There is no command for build and deploy logs. List the deployments of a service with `geass apps deployments web`.
Then read the logs of one deployment by its ID with the raw API:

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

Variables are pending changes. They get to the running service when you deploy. To apply them immediately, add
`--deploy`. To wait for the result, also add `--wait`:

```bash
geass env set web LOG_LEVEL=debug --deploy --wait
```

Without `--deploy`, the command says ``The change is pending. Run `geass deploy web` to apply it.`` Only the first `=`
in `KEY=VALUE` splits it, so a value can contain `=`. Geass stores a secret separately from usual configuration, and
never prints a secret back. See [Environment variables](/docs/guides/environment-variables/).

:::note
Only secrets are hidden. The API returns plain configuration values: `geass env list --json` and `geass apps get`
(with **Full access**) print them. Store all sensitive values with `--secret`.
:::

## Scale, sleep, and roll back

```bash
geass scale web 3
geass scale web --sleep-after 10m
geass scale web --always-on
```

`geass scale` has an immediate effect, with no deploy. The replica count is 0 to 1000.

`--sleep-after` takes a value from 30 seconds to 24 hours. With it, Geass stops the service after that time without
requests, and starts it on the next request. `--always-on` turns that off. Sleep has these conditions:

- The serverless add-on must be on in the cluster settings.
- The service must have a public host.
- You cannot use sleep together with CPU autoscaling.

See [Scale to zero](/docs/guides/scale-to-zero/).

The command saves the setting, and prints `web sleeps after ...` when the service can sleep. When it cannot, the
service stays running, and the command exits with `1` and the reason. For example:
`saved, but web cannot sleep when idle and stays running: The serverless add-on is not installed`. The setting stays
saved, and takes effect after the add-on is on.

To go back to an earlier deployment, run these commands:

```bash
geass apps deployments web                # newest first: the ids are what rollback takes
geass rollback web --wait                 # the deployment before the newest
geass rollback web web-abc12 --wait       # a deployment you choose
```

A rollback deploys immediately. It records a new deployment. Thus, if you run `geass rollback web` a second time, it
goes to the entry before that one. To select a deployment, give its ID. You cannot roll back a function. Instead,
restore the earlier code and deploy again.

A rollback puts back only what a deployment records: the image of a service that runs one, and the number of copies.
Variables, the port, and all other settings stay as they are now. This has these results:

- A variable that you added after the deployment that you go back to is still there. An older image that does not
  know the variable ignores it.
- An older image that needs a variable that you removed since then does not start.

To go back to older variables too, set them back with `geass env set` and `geass env unset`. A service that Geass
builds from a repository continues to run the code that it has. To run older code, pin the commit.

## Functions and schedules

A function runs code that you give it (see [Functions](/docs/guides/functions/)). Create a function from a file, add
schedules, and call it:

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

A schedule is a cron expression in UTC with five fields, or one of `@hourly`, `@daily`, `@weekly`, `@monthly`, or
`@yearly`. Each run is a `POST` to the function. If you add a schedule with a name that already exists, the new
schedule replaces the old one. Schedules start when you next deploy the function. Thus, deploy after you add or remove
a schedule. A function can have 8 schedules.

`geass functions invoke` calls the function at its public address, the same as a visitor. The status line goes to
standard error, and the body goes to standard output. If the status is 400 or more, the command also exits with code
`1`.

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

The windows are `15m`, `1h` (the default), `6h`, and `24h`. Metrics need Prometheus. Without Prometheus, the command
exits with `no metrics yet: ...`. See [Observability](/docs/guides/observability/).

## Databases, servers, and compliance

```bash
geass databases connection orders
geass nodes tag worker-2 --region eu --tags hipaa --compliance hipaa
geass compliance set proj-1a2b3c4d hipaa
geass compliance show proj-1a2b3c4d
```

`geass databases connection` needs **Full access**. It shows the host, port, database, and user. The password and the
connection string stay hidden unless you add `--reveal`. Geass logs each reveal.

`geass nodes tag` replaces the region, zone, and tags of a server. So give every tag that the server must keep. The
command prints each warning as `warning: ...`, for example for work on the server that does not belong there. See
[Compliance tags](/docs/guides/compliance-tags/).

## Clouds, pools, and servers

With an admin token, `geass` can connect a cloud, set the size of a pool, and add servers.

These commands make Geass create machines at a cloud, and those machines cost money. The clouds behind these commands
are experimental. See [Cloud connections for machines](/docs/guides/connectors/clouds/).

```bash
geass providers Hetzner                                              # what the cloud takes
geass connections add hetzner --provider Hetzner --credential-file token=./hetzner-token.txt
geass sizes --connection hetzner --region fsn1 --min-vcpus 2 --limit 5
geass pools create burst --connection hetzner --region fsn1 --size cx23 --min 0 --max 5 --autoscale
geass pools show burst
geass servers add --connection hetzner --region fsn1 --size cx23     # one machine, until you remove the server
geass nodes list                                                     # POOL and EPHEMERAL show what came from where
```

Give a secret from a file or from the environment. Never give a secret on the command line. You cannot make a pool
without `--max`. `--max` is the maximum number of machines that the pool can have, and the limit on what it costs. See
[Node pools and autoscaling](/docs/guides/node-pools/) and the [reference](/docs/reference/cli/#clouds-and-machines).

## Anything else: the raw API

`geass api` calls any `/api/v1` endpoint and prints the JSON that it returns. It covers all tasks that have no command
of their own, for example deleting a database or restoring a backup:

```bash
geass api GET /apps -q project=proj-1a2b3c4d
geass api POST /apps/web/scale -d '{"replicas": 3}'
geass api DELETE /databases/orders -q confirmName=orders
geass api POST /databases/restore -d '{"archive": "orders-backup-1a2b3c"}'
geass openapi > geass-openapi.json
```

`-d` takes a JSON string, `@file`, or `-` for standard input. `geass openapi` prints the OpenAPI description of every
endpoint, with who can call it. For restores, see [Backups and restore](/docs/guides/backups-and-restore/).

There is a command to delete a service. The command asks first, and a script must give `--yes`:

```bash
geass apps delete web --yes
```

## Output, JSON, and exit codes

**Two streams.** Results go to standard output. Progress and notes go to standard error. A program that reads standard
output sees only results.

**JSON.** Add `--json` to the commands that print data. These commands are a list, a status, `apps deployments`,
`whoami`, `version`, `nodes list`, `compliance show`, `env list`, `metrics`, the `functions` commands that print
something, and `logs`. Lists print `{"items": [...]}`:

```bash
geass apps list -p proj-1a2b3c4d -e production --json | jq -r '.items[] | select(.state == "failed") | .name'
geass status --json | jq '.items[] | {name, state, ready}'
geass logs web -f --json | jq -c 'select(.level == "error") | .line'
```

`apps get`, `projects get`, `databases get`, `stores get`, `api`, and `openapi` always print JSON. Some commands print
plain text lines, with or without `--json`: `scale`, `rollback`, `env set`, `apps create`, `apps delete`, `nodes tag`,
and `compliance set`. `geass logs --json` prints one indented JSON object for each log line, which `jq` reads as a
stream.

**Exit codes.** A script can use the exit code to find what failed:

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
| `GEASS_URL` | The dashboard address, for example `https://geass.example.com`. |
| `GEASS_TOKEN` | An API token. |

If the script works in one environment, also add `GEASS_PROJECT` and `GEASS_ENVIRONMENT`.

**Use the smallest token.** Create the token under **Settings** → **Tokens** (see
[Users and API tokens](/docs/guides/users-and-tokens/)). Keep it in the secret store of your CI system.

| Token | Use it for |
| --- | --- |
| Project token (**Settings** → **Tokens** inside a project) | CI that deploys one environment. It can deploy, scale, roll back, edit variables, and read logs of that environment, and nothing else. |
| **Read only, with logs** | Monitoring, reports, and agents that only watch. |
| **Full access** | Scripts that create services, change nodes, or delete things. |

A project token works with these commands: `whoami`, `version`, `apps list` and `get`, `status`, `deploy NAME`,
`build`, `logs -f`, `env`, `scale`, `rollback`, `metrics`, `functions`, and the parts of `geass api` that its routes
allow. The API refuses a project token for `projects`, `databases`, `stores`, `nodes`, `compliance`, `apps create` and
`apps delete`, `openapi`, and `logs` without `-f`. Name the services in `geass deploy`. `geass deploy` with no names
deploys by project, and the API refuses it for a project token.

This GitHub Actions job updates an image and waits for it:

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

The first command saves the new image as a pending change. The second command deploys it. If the service does not
come up, the second command fails the job with exit code `5` or `6`.

For a service that Geass builds from a repository, `geass build web --wait` runs its build again. The command follows
the build through to the rollout, with the same exit codes. See
[Build from GitHub Actions](/docs/guides/github-and-builds/#build-from-github-actions).

In CI, nobody can answer a question. Thus, `geass apps delete` needs `--yes`. If the job runs where there is no home
directory, for example a bare container, set `GEASS_CONFIG` to any path, for example `GEASS_CONFIG=/tmp/geass.json`.
Without a configuration directory, every command stops with `could not find a configuration directory`.

## Troubleshooting

**`` Error: not signed in: run `geass login --url https://your-geass-address` ``** (exit 4). There is no saved login,
and no `GEASS_URL`. Run `geass login --url ...`, or set `GEASS_URL` and `GEASS_TOKEN`.

**`` Error: not signed in to https://geass.example.com: run `geass login` ``** (exit 4). There is no token for the
address. You ran `geass logout`, or only `GEASS_URL` is set. This error also shows when you give `--url` for an
address that you have no login for.

**`Error: invalid or expired API token`, then ``Run `geass login` to sign in again.``** (exit 4). The token expired,
or someone revoked it. Run `geass login` again.

**`Error: ... (your token may not do this)`** (exit 4). The API refused with `403`. The text before the brackets gives
the reason:

- `viewer role cannot access this`: a **Read only** token tried to read logs or change something.
- `this token cannot access that endpoint`: a project token called something that is not in its list.
- `only a workspace admin can read connection details`: the token is not **Full access**.

Sign in again with more access, or use a different token.

**`Error: app not found`** (exit 3). There is no service with that name, or the service is outside the environment of
the project token. `geass apps list` shows the names.

**`Error: this deletes data; run it again with --yes to confirm`** (exit 2). Standard input is not a terminal, so
there was nobody to ask. Add `--yes`.

**`Get "geass.example.com/api/v1/me": unsupported protocol scheme ""`.** The address has no `https://`. Write the full
address.

**`the API answered something that is not JSON`.** The address is not the Geass dashboard, or something in front of
the dashboard returns a web page. Open the address in a browser. See [Sign in](/docs/getting-started/sign-in/).

**`the API answered in a shape this client does not know (...): is Geass a different version?`** The address is a
Geass dashboard, but this command line does not expect its answer. Usually, the dashboard is older than the command
line. Update the one that is older.

**A certificate error, for example `x509: certificate signed by unknown authority`.** The command line always checks
the certificate of the server. Fix the certificate (see [Domains and TLS](/docs/guides/domains-and-tls/)), or trust
your certificate authority on this machine. Before you have a domain, use the `https://` address of the dashboard at
the public IP address of the server. See [Sign in](/docs/getting-started/sign-in/).

**The approval page shows `No sign-in request has that code. It may have expired: start again from the command line.`**
The code is wrong, the code expired after 10 minutes, or Geass restarted. Run `geass login` again.

**The page says `Only an admin can approve this`.** You signed in to the dashboard as a viewer. Sign in as an admin.

**`geass login` ends with `the sign-in was not approved in time` or `the sign-in expired` (exit 6).** Nobody approved
the code in 10 minutes. Run the command again, and approve sooner.

**`could not start the sign-in: too many sign-in requests, try again in a minute`.** One address started 10 sign-ins in
a minute, or 100 sign-ins are waiting. Wait a minute. Behind a proxy, every user shares the address of the proxy.

**The address printed by `geass login` starts with `http://`, but the dashboard is on HTTPS.** A proxy in front of the
dashboard does not send `X-Forwarded-Proto: https`. Change the address to `https://` in the browser. The code still
works.

**`gave up waiting for web after 10m0s (it is pending): context deadline exceeded`** (exit 6). The service did not
become ready in time. Do one of these:

- To find the reason, run `geass status web`.
- For a slow build, increase `--timeout`.
- Make sure that the cluster has room (see [Usage and capacity](/docs/guides/usage-and-capacity/)).

**`the deploy failed: ...`** (exit 5). The text after the colon is the failure reason of the build, or the message of
the service. For a repository build, read the build logs. Run `geass apps deployments web`, then run
`geass api GET /apps/web/deployments/ID/logs` for the newest deployment.

**`geass logs web` prints nothing, and `No log lines matched.` is on standard error.** In the lines that the command
looked through, the service wrote nothing that matches. Remove `--level` and `--search`, or increase `--tail` and
`--since`. See [Read logs](#read-logs).

**`no metrics yet: ...`.** Prometheus is not installed, or the command line cannot reach it. See
[Observability](/docs/guides/observability/).

**`--sleep-after must be between 30s and 24h`** (exit 2). The value is out of range. Write a value with a unit, for
example `600s` or `10m`.

**`Scale to zero decides how many copies run, so turn CPU autoscaling off to use it`** (exit 1). The service has CPU
autoscaling on. Turn it off in the **Settings** of the service, then run the command again.

**`could not find a configuration directory (set GEASS_CONFIG)`.** The machine has no home or configuration directory.
Set `GEASS_CONFIG` to a path that you can write.

**`<path> is not valid: ...`.** The configuration file is not valid JSON. Fix it, or delete it and sign in again.

**`signed in, but could not save the login: ...`.** The token works, but the command line cannot write the
configuration file or its folder. Fix the permissions, or set `GEASS_CONFIG`.

**`geass: command not found`.** The install folder is not on your `PATH`. The install script prints the folder that it
used.

## Under the hood

- Every command calls `<address>/api/v1` over HTTPS, with `Authorization: Bearer <token>`. The same rules apply as in
  the dashboard. A call times out after 60 seconds, except for a log that you follow.
- Geass keeps pending sign-ins in memory for 10 minutes. A restart of Geass cancels them.
- The Geass log records each API call with its method, path, and status, but not the caller. **Last used** in
  **Settings** → **Tokens** shows when a token was used. Use one token for each script or person.

## Related

- [CLI commands](/docs/reference/cli/)
- [MCP server for AI agents](/docs/guides/mcp-server/)
- [Users and API tokens](/docs/guides/users-and-tokens/)
- [REST API](/docs/reference/rest-api/)
- [Projects and environments](/docs/guides/projects-and-environments/)
- [Functions](/docs/guides/functions/)
- [Scale to zero](/docs/guides/scale-to-zero/)
