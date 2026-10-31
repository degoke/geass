---
title: "GitHub and builds"
description: "Deploy a GitHub repository: link a project, set the build registry, run and read builds, report to GitHub, and use webhooks."
---

Geass can build a service from a GitHub repository. You link a project to GitHub and select a repository. Geass builds
the Dockerfile, pushes the image to a container registry that you provide, and runs the image. A build starts in these
cases:

- When you first deploy the service.
- When you deploy a change to a build setting.
- When you push to the branch.

Geass also tells GitHub the result of each build and deploy.

## Before you start

- **A GitHub App registered in Geass.** One admin does this one time for the whole cluster. The app needs a public,
  verified dashboard address. See [Connect GitHub](/docs/guides/connectors/github/) and
  [Domains and TLS](/docs/guides/domains-and-tls/). An app that an older Geass registered might need more permissions.
  See [Give an existing GitHub App the new permissions](#give-an-existing-github-app-the-new-permissions).
- **A container registry that you control.** Geass does not include a registry. Without a registry, a repository
  service does not build. See [Set the registry](#set-the-registry).
- **A Dockerfile** in the repository.
- Free capacity for the build and for the service. See [Usage and capacity](/docs/guides/usage-and-capacity/).

To build in your own CI, push the image to your registry, and run it as an [app](/docs/guides/apps/) from an image. That
path does not use the build system of Geass.

## How a repository service works

1. You click **Deploy changes**. Geass starts a **build** of the service.
2. Geass clones the repository at the head commit of the branch, and builds the Dockerfile with Kaniko.
3. Geass pushes the image to your registry.
4. Geass runs the service from that exact image, by its digest. While a build runs, the service shows
   `Git source is waiting for a successful build`. The previous copies continue to serve.

A push to the branch does the cycle again. On GitHub, the commit shows a `geass/build/<service name>` status, and the
deploy shows as a GitHub Deployment. See [Reporting back to GitHub](#reporting-back-to-github).

For a project of an [organization](/docs/guides/organizations-and-workspaces/#builds-and-the-registry), the build runs
apart from the builds of other organizations. The organization can have its own registry.

## Link a project to GitHub

You link each project separately. So each project can use a different GitHub account.

1. Open **Settings** → **GitHub** of the project.
2. On the **GitHub** card, click **Connect to GitHub**. If the card says "GitHub is not set up for this cluster yet",
   ask an admin to register the app first.
3. On GitHub, select the account or organization, and the repositories that the app can read.
4. GitHub returns you to the project, with the repository list open. The card now shows "Connected. Repositories can
   be deployed as services, and pushes build automatically."

To unlink the project, click **Disconnect**. Services that you already created keep their settings. An unlinked project
ignores pushes and pull requests.

When the project is not linked yet, you can also start this flow from **+ Add resource** → **GitHub Repository**.

Geass lists and builds only the repositories that the app installation can access. To add a repository, change the
repository access of the app on GitHub. Open **Settings** → **Applications** → **Installed GitHub Apps** of your GitHub
account.

## Choose a repository

1. Select **+ Add resource** → **GitHub Repository**.
2. Select a repository. The list shows up to 500 repositories, sorted by name, each marked **Public** or **Private**.
   To filter the list, enter text.
3. Geass saves a **draft** service that builds the default branch, and opens its **Settings**.

You can also paste `acme/storefront`, or a link such as `https://github.com/acme/storefront/tree/staging`, into the
first search box. Then click **Deploy acme/storefront**. A link with `/tree/<branch>` uses that branch. A bare
`owner/name` uses `main`.

Then check these settings, and click **Deploy changes**:

| Setting | Section | Meaning | Default |
| --- | --- | --- | --- |
| **Repository** | Source | `owner/name`. | The repository that you selected |
| **Branch** | Source | The branch that Geass builds and deploys. | The default branch |
| **Root directory** | Source | The folder that the build runs in. | Empty |
| **Wait for CI** | Source | Build only after the GitHub checks of the commit succeed. | Off |
| **Dockerfile path** | Build | The Dockerfile to build, relative to the root directory. | `Dockerfile` |
| **Watch paths** | Build | Which changed files make a push start a build. | Empty |
| **Use build cache** | Build | Use image layers again between builds. | Off |

For the other settings, see [Apps](/docs/guides/apps/#settings).

### Root directory and Watch paths

**Root directory** is the folder of the repository that the build runs in. For the repository root, leave it empty.
`/apps/api` and `apps/api` are the same folder. Geass refuses a path that leaves the repository (`..`).

**Dockerfile path** is relative to the root directory. With the root directory `apps/api`, `Dockerfile` means
`apps/api/Dockerfile`. A path that already starts with the root directory also works.

**Watch paths** limit which pushes start a build. They are gitignore-style patterns, one on each line. Geass matches
them against the files that the push changed, from the repository root:

| Pattern | Matches |
| --- | --- |
| `/src/**` | All under `src` at the top of the repository. |
| `/src` or `src/` | The same. A pattern that names a folder includes what is in the folder. |
| `*.go` | Go files at any depth. A pattern without a slash matches at any depth. |
| `api/**/*.go` | Go files anywhere under `api`. A slash in the middle anchors the pattern to the root. |
| `!/docs/**` | Removes `docs` again. The last pattern that matches a file decides for that file. |

A push starts a build when a pattern selects at least one changed file. If every pattern starts with `!`, the patterns
mean "all files except". With no patterns, every push starts a build.

Watch paths apply to pushes only. The first deploy and a change to a build setting always start a build. Watch paths
apply immediately when you save them.

## Set the registry

A build needs a place to push its image, and Geass has no registry of its own. Until a registry is set, a repository
service does not build. The service shows
`no container registry is set for builds: set one under Settings > Registry, or set spec.build.registry.repository on the service`.

Geass uses the first of these registries that is set:

1. The registry of the service itself. Only a manifest can set it. See [Under the hood](#under-the-hood).
2. For a project of an [organization](/docs/guides/organizations-and-workspaces/#builds-and-the-registry), the registry
   of the organization. An organization admin sets it under **Settings** → **Registry**.
3. The platform registry. A platform admin sets it through the API.

The dashboard has no form for the platform registry yet. To set it, use the API with a workspace admin token. The login
is optional:

```bash title="Set one registry for all builds"
curl --fail -X POST https://geass.example.com/api/settings/registry/save \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d repository=ghcr.io/acme/geass-builds -d username=acme-bot --data-urlencode password="$GHCR_TOKEN"

curl https://geass.example.com/api/settings/registry -H "Authorization: Bearer $GEASS_TOKEN"
# {"repository":"ghcr.io/acme/geass-builds","configured":true,"hasLogin":true}
```

`POST /api/settings/registry/clear` removes the registry and its login. To change the repository and keep the login,
send `keepExisting=on` and no username or password. Geass never returns the login.

Know these facts about the registry:

- Geass pushes to the repository with the tag `latest`. All services that share a repository push into it. The service
  runs its image by digest, so a later push does not change what runs.
- Your servers pull the image with the same login. So the login must be able to pull as well as push. Give it access
  to that repository only.
- A registry that needs no login gets none. Your servers must then be able to read the repository without a login.
- A change to the registry starts a new build. A service that waits for a build starts it in about 30 seconds. A
  running service does not build again immediately.

## How a build runs

Each set of build settings has its own build. These settings define it:

- The repository, the branch, and a pinned commit.
- The Dockerfile path and the root directory.
- **Use build cache** and **Wait for CI**.
- The registry.

A change to one of these settings makes a new build. If you change a setting back, Geass uses the earlier build again.
A push or a rebuild runs the current build again from the start.

1. **Resolve the commit.** Geass asks GitHub for the head commit of the branch, unless the service pins a commit.
2. **Wait for CI**, if the service asks for it. See [Wait for CI](#wait-for-ci).
3. **Build.** Geass clones the commit, builds the Dockerfile, and pushes the image. The build runs in the region of the
   project, on the servers that the project requires. A failed build gets up to two more tries.
4. **Run.** Geass records the digest of the image, and the service rolls to it.

A build is in one of these states:

| State | Meaning |
| --- | --- |
| `Pending` | Created, or set back to the start to run again. |
| `WaitingForCI` | Waits for GitHub checks. |
| `Running` | The build is running. |
| `Succeeded` | The image is pushed and has a digest. |
| `Failed` | A GitHub call failed, a check failed, or the build failed. |
| `Cancelled` | Someone cancelled it. |

Geass keeps the newest 10 finished builds of a service, and always the build that the service runs.

### Build logs

To read the log of a build, do these steps:

1. Open the service, then **Deployments**.
2. Click **View logs** on a deployment, or click its row.
3. Select **Build Logs**.

The tab shows the output of the build. If the clone failed before the build started, the tab shows the output of the
clone. Geass deletes this output one hour after the build ends. After that, the tab shows the failure reason that Geass
recorded, for example `kaniko Job failed`. A deploy that started before the first build of the service shows no build.

## Branch deploys and push triggers

A service builds one branch: **Branch** under **Settings** → **Source**. A push to that branch builds and deploys the
service again. Geass rebuilds each service that matches all of these conditions:

- The service is deployed, and it is not a draft.
- Its repository and branch match the push.
- It does not pin a commit.
- Its [watch paths](#root-directory-and-watch-paths) select a file that the push changed.
- Its project is linked to the GitHub installation that sent the push.

When the build succeeds, the service rolls to the new image. Until then, the previous copies continue to serve. A push
that deletes the branch builds nothing.

To deploy a second branch, add another environment, for example `staging`. Then create a second service there for the
same repository, with the other branch. See [Projects and environments](/docs/guides/projects-and-environments/). For one
environment for each pull request, see [Pull request environments](/docs/guides/pull-request-environments/).

## Build a new commit

These events start a build:

- **The first deploy.** **Deploy changes** on a draft builds the head commit of the branch.
- **A push to the branch.** See [Branch deploys and push triggers](#branch-deploys-and-push-triggers).
- **A change to a build setting**, after you click **Deploy changes**. The list is under
  [How a build runs](#how-a-build-runs).
- **A rebuild.** `geass build web` builds the head of the branch again. See
  [Build from GitHub Actions](#build-from-github-actions). The dashboard has no button for it.

**Redeploy** does not start a build. It restarts the service on the same image. A deploy of pending changes, from the
dashboard or with a project token, also does not build a newer commit.

To cancel the current build while it waits or runs, call `POST /api/apps/{id}/build/cancel`. You cannot cancel a build
that has finished.

**Build one commit.** To build one commit and ignore later pushes, pin it. The dashboard and the command line have no
field for it yet. See [Pin a commit](#pin-a-commit) under Under the hood.

### Wait for CI

With **Wait for CI** on, Geass starts the build only when these conditions are true:

- The commit has at least one GitHub check.
- All checks completed with the conclusion `success`.

Until then, the build is `WaitingForCI`, and Geass checks again every 15 seconds. If a check fails, the build fails with
`GitHub check "<name>" concluded <result>`.

A commit with no checks never passes. Turn off the option for a repository that has no GitHub Actions or other checks.

The app needs the **Checks: Read** permission. An app from the earliest versions of Geass does not have it, and the
build fails with `GitHub API returned 403 Forbidden: reading check runs needs the Checks: Read permission…`. See
[Give an existing GitHub App the new permissions](#give-an-existing-github-app-the-new-permissions).

## Reporting back to GitHub

Geass tells GitHub what it did with your repository. So you see the result on the commit, in the **Deployments** of the
repository, and on the pull request.

| What | How it shows on GitHub | Permission it needs |
| --- | --- | --- |
| A build | A commit status named `geass/build/<service name>`. | **Commit statuses**: Read and write |
| A deploy | A GitHub Deployment of the commit, in an environment named like the Geass environment. | **Deployments**: Read and write |
| A [pull request environment](/docs/guides/pull-request-environments/#what-geass-writes-on-the-pull-request) | One comment on the pull request, edited in place. | **Pull requests**: Read and write |

Geass reports only services built from a repository. Image services and functions report nothing. A report never stops
a build or a deploy, whatever GitHub answers.

### The commit status of a build

| State on GitHub | When | Description |
| --- | --- | --- |
| `pending` | The build waits for CI, or it runs. | `Waiting for CI checks to pass before building`, then `Building the image` |
| `success` | Geass built and pushed the image. | `Built the image` |
| `failure` | The code did not build, or a CI check failed with **Wait for CI** on. | `The build failed`, or the failure message of the check |
| `error` | The build stopped for a reason that is not the code, for example no registry, no answer, or a cancel. | The failure reason, or `The build was cancelled` |

The **Details** link opens the **Deployments** tab of the service in the dashboard.

Each service has its own status name, with the name that the dashboard shows. To require a build under branch
protection, require that name, for example `geass/build/web` for a service called `web`. When you rename the service,
Geass renames its status.

### The GitHub Deployment of a deploy

A deploy is a push that Geass builds and rolls out, or a **Deploy changes** of the commit that already runs. For each
deploy, Geass makes a GitHub Deployment, with these statuses:

| Status | When |
| --- | --- |
| `in_progress` | The new image rolls out. |
| `success` | The service is ready. The public address of the service is the **environment URL**. |
| `failure` | Geass stopped its attempts to deploy. |

The **log URL** opens the **Deployments** tab of the service. A restart, a scale, or a server loss after the deploy
reports nothing. Geass makes a deployment without waiting for the checks of the commit.

A [pull request environment](/docs/guides/pull-request-environments/) uses a GitHub environment named `pr-<number>`,
marked transient. When Geass removes the pull request environment, it makes its deployments inactive.

### Permissions

The GitHub App that Geass registers asks for these permissions:

| Permission | Level | What Geass does with it |
| --- | --- | --- |
| Metadata | Read | GitHub requires it of every app. |
| Contents | Read | Clones repositories to build them. |
| Checks | Read | **Wait for CI** reads the checks of a commit. |
| Pull requests | Read and write | Reads pull requests and comments on them. GitHub has no narrower permission for a comment. |
| Commit statuses | Read and write | The `geass/build/<service name>` status. |
| Deployments | Read and write | The GitHub Deployments. |

The app subscribes to the `push` and `pull_request` events. Contents stays read-only. So Geass cannot change your code.

When GitHub refuses a report because a permission is missing, the build or deploy continues. Geass tries that kind of
report again after ten minutes. After you approve the permission, Geass sends the current state.

### Give an existing GitHub App the new permissions

An app that an older Geass registered might not have the three write permissions, or **Checks**. To give it the
permissions, do these steps:

1. In Geass, open **Settings** → **Connectors** → **GitHub**, and click **Test GitHub App**. The message lists the
   missing permissions and links to the permissions page of the app.
2. On GitHub, open the **Permissions & events** page of the app, at
   `https://github.com/settings/apps/<app slug>/permissions`. For an app that an organization owns, use the settings of
   that organization.
3. Set **Commit statuses**, **Deployments**, and **Pull requests** to **Read and write**.
4. If **Checks** has no access, set it to **Read-only**.
5. Save the page.
6. Accept the new permissions on the installation. GitHub sends an email to the owner. You can also open **Settings** →
   **Applications** → **Installed GitHub Apps** on GitHub, click **Configure** for the app, and accept them.
7. In Geass, click **Test GitHub App** again. The message says that the app has the permissions that Geass needs.

Reports start again in about ten minutes. An app that Geass registers now asks for all of these permissions.

### Turn it off

Reporting is on for every project by default. To turn it off for one project, open **Settings** → **GitHub** of the
project, and turn off **Report to GitHub**. This stops all three reports. Builds, deploys, and pull request environments
do not change.

## Webhooks

### From GitHub to Geass

GitHub calls `<dashboard URL>/webhooks/github`. Geass checks the signature of each call. Geass handles these events:

| Event | What Geass does | Answer |
| --- | --- | --- |
| `ping` | Nothing. | `{"accepted":true,"event":"ping","delivery":"…"}` |
| `push` | Rebuilds the matching services. See [Branch deploys and push triggers](#branch-deploys-and-push-triggers). | `{"accepted":true,"triggered":N,"delivery":"…"}` |
| `pull_request` | Creates or removes [pull request environments](/docs/guides/pull-request-environments/). | `{"accepted":true,"environments":[…],"delivery":"…"}` |
| All other events | Ignores them. | `{"accepted":true,"ignored":true,"delivery":"…"}` |

`N` is the number of services that Geass rebuilt. A bad signature gets `401`. A `503` means that Geass has no webhook
secret. On GitHub, **Advanced** → **Recent Deliveries** of the app shows each delivery and the answer. A redelivered
push runs the builds again.

**Test GitHub App** in **Settings** → **Connectors** checks two things:

- The webhook URL of the app matches the dashboard address.
- The app has the [permissions](#permissions) that Geass needs.

The GitHub App can also be an app that a provider runs. In that case, the provider forwards the events. See
[GitHub through a provider](/docs/guides/connectors/github/#github-through-a-provider) and
[Forwarded GitHub events](/docs/reference/rest-api/#forwarded-github-events).

### From your CI to Geass

If your CI can send a header, use an API token:

1. Create a **project token** under **Settings** → **Tokens** of the project, limited to one environment.
2. Store the token as a secret in your CI.
3. Call the API or the command line.

A project token works only on the services of its own environment. See
[Users and API tokens](/docs/guides/users-and-tokens/).

Some tools can only call a URL, for example a registry that announces a pushed image. For these tools, use a
**deploy hook**. Create it under **Settings** → **Deploy** → **Deploy hook** of the service. A call to the hook
rebuilds a repository service, or deploys the pending changes of any other service. It answers one time every 30
seconds. See [Deploy hooks](/docs/guides/users-and-tokens/#deploy-hooks).

For a service that runs an image, set the new image and deploy in one call. This is the usual CI step:

```bash title="Deploy a new image from CI"
curl --fail -X POST https://geass.example.com/api/apps/amber-fern/update \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d image=ghcr.io/acme/storefront:$GIT_SHA -d deploy=on
```

To deploy all that is pending in the environment of the token, call `POST /api/token/deploy`. With the command line,
`geass deploy amber-fern --wait` deploys and waits. If the service does not come up, the command exits with `5` or `6`.
See [Command line](/docs/guides/command-line/#scripts-and-ci).

### Build from GitHub Actions

A push already rebuilds a repository service. Use `geass build` when your pipeline must start the build, for example on
a schedule, from a manual run, or when a job must wait for the new version. `geass build` builds the head of the
branch, as a push does. With `--wait`, it follows the build to the rollout.

This workflow rebuilds `web` every night, so the image gets updates to its base image:

```yaml title=".github/workflows/geass-build.yml"
name: rebuild web
on:
  schedule:
    - cron: "0 3 * * *"
  workflow_dispatch:
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - name: Install the geass command line
        run: |
          curl -sfL https://github.com/degoke/geass/releases/latest/download/install-cli.sh \
            | GEASS_INSTALL_DIR="$HOME/.local/bin" sh
          echo "$HOME/.local/bin" >> "$GITHUB_PATH"
      - name: Build and roll out
        env:
          GEASS_URL: https://geass.example.com
          GEASS_TOKEN: ${{ secrets.GEASS_TOKEN }}
        run: geass build web --wait --timeout 20m
```

`GEASS_TOKEN` is a **project token** for the environment of `web`. The step prints `web: build started` and each change
of state. It exits with these codes:

- `0` when the service is ready.
- `5` when the build or the rollout fails. The step says why.
- `6` when `--timeout` runs out.

Know these facts about `geass build`:

- It builds what the branch holds when the call arrives, not always the commit that started your workflow.
- It does not apply pending changes. For these changes, use `geass deploy`.
- If the push webhook also works, a workflow that builds on every push starts the build two times. Build from a
  schedule or a manual run, or let the webhook start the build.
- With no service names, and with `--project` and `--environment`, it builds every deployed repository service in that
  environment.
- The same call is `POST /api/v1/apps/{name}/build`. The MCP tool is `rebuild_app`.

### Outgoing webhooks

Geass sends a signed `POST` to the URLs that you add when a service deploy starts, succeeds, or fails. Use these
webhooks for chat messages, a pipeline trigger, or an audit log.

1. Open **Settings** → **Webhooks** of the project, and click **New webhook**.
2. Enter the **Payload URL**. It must be `http://` or `https://`, with no username or password.
3. Select the **Events**: **Deployment started**, **Deployment succeeded**, or **Deployment failed**. To receive every
   event, clear all of them.
4. Click **Create webhook**. A dialog shows the **Signing secret** one time. Copy it immediately.

The menu of a webhook has **Send test**, **Edit**, **Pause** or **Enable**, **Rotate signing secret**, and **Delete**.
**Recent deliveries** lists the last 20 attempts. A project can have at most 20 webhooks.

Geass sends `deployment.started`, `deployment.succeeded`, and `deployment.failed` one time for each service deploy.
Database and bucket deploys send nothing, and a build alone sends nothing. **Send test** sends an event of type `test`.

```json title="Request body"
{
  "id": "evt_9f2c41a7b3d80e15",
  "type": "deployment.succeeded",
  "createdAt": "2026-10-12T09:30:02Z",
  "project": { "name": "proj-1a2b3c4d", "displayName": "morning-beach" },
  "environment": "production",
  "service": {
    "name": "amber-fern",
    "displayName": "storefront",
    "kind": "service",
    "url": "https://shop.example.com/"
  },
  "deployment": {
    "revision": "2026-10-12T09:29:41.512093847Z",
    "image": "ghcr.io/acme/storefront:1.4.0"
  }
}
```

`service.kind` is `service` or `function`. `deployment.commit` appears only for a service that pins a commit. `message`
gives the reason for a failure.

| Header | Value |
| --- | --- |
| `Content-Type` | `application/json` |
| `User-Agent` | `Geass-Webhooks/1` |
| `X-Geass-Event` | The event type. |
| `X-Geass-Delivery` | A delivery ID, `dlv_…`. |
| `X-Geass-Timestamp` | Unix seconds. |
| `X-Geass-Signature` | `t=<unix seconds>,v1=<hex>`, where `v1` is the HMAC-SHA256 of `<t>.<raw body>` with the signing secret. |

Verify the signature on the raw body, and reject old timestamps:

```js title="Verify a delivery in Node.js"
import { createHmac, timingSafeEqual } from "node:crypto";

export function verify(secret, header, rawBody) {
  const parts = Object.fromEntries(header.split(",").map((part) => part.split("=")));
  const expected = Buffer.from(createHmac("sha256", secret).update(`${parts.t}.${rawBody}`).digest("hex"));
  const received = Buffer.from(parts.v1 ?? "");
  const fresh = Math.abs(Date.now() / 1000 - Number(parts.t)) < 300;
  return fresh && expected.length === received.length && timingSafeEqual(expected, received);
}
```

**Retries.** A delivery times out after 10 seconds. If Geass cannot connect, or the receiver answers `5xx`, `429`, or
`408`, Geass tries again. Geass makes up to three attempts, 1 second and then 5 seconds apart. Any other answer of 300
or more is final. Geass does not follow redirects.

**Allowed addresses.** A webhook cannot point at `localhost`, a loopback, link-local, or multicast address, or the
cloud metadata address. A webhook also cannot point at a private network address, unless the platform allows it with
`GEASS_WEBHOOKS_ALLOW_PRIVATE_NETWORKS=true`. See [Configuration](/docs/reference/configuration/).

Through the API, use `POST /api/projects/{project}/webhooks/save` with `url`, repeated `event`, `disabled`, and `id` to
update. `…/delete`, `…/rotate`, and `…/test` each take `id`. See the [REST API](/docs/reference/rest-api/).

## Troubleshooting

**"GitHub is not connected to this cluster yet" or "Connect GitHub in platform settings first".** No GitHub App is
registered. An admin registers it under **Settings** → **Connectors** → **GitHub**. See
[Connect GitHub](/docs/guides/connectors/github/).

**"Link this project to GitHub first".** Open **Settings** → **GitHub** of the project, and click **Connect to GitHub**.

**A repository is missing from the list.** The app installation cannot see the repository. Change the repository access
of the app on GitHub, and open the list again.

**The service says `no container registry is set for builds`.** No registry is set. See
[Set the registry](#set-the-registry). After you set one, the service builds in about 30 seconds.

**Build Logs show `no such host`, `UNAUTHORIZED`, or `denied`.** The registry or its login is wrong. See
[Set the registry](#set-the-registry).

**The build fails with `GitHub API returned 404 Not Found`.** The repository or the branch is wrong, or the app cannot
read the repository. Check **Repository** and **Branch** in **Settings** → **Source**.

**The build fails with `GitHub API returned 403 Forbidden` and Wait for CI is on.** The app does not have the **Checks**
permission. See [Wait for CI](#wait-for-ci).

**The build stays in `WaitingForCI`.** The commit has no checks yet, or the checks still run. If the repository has no
checks, turn off **Wait for CI**.

**The build fails with "Kaniko completed without an immutable image digest".** The build finished, but Geass found no
digest of the image. Read **Build Logs**. To run the build again, push, or run `geass build`.

**The build succeeded, but the service shows `The service cannot start: app is ImagePullBackOff`.** Your servers cannot
pull the image. If the registry needs a login, make sure that the login can pull from that repository. If the registry
needs no login, make sure that your servers can reach it.

**The build cannot find the Dockerfile.** The Dockerfile path is relative to the root directory. With the root
directory `apps/api`, write `Dockerfile`. See [Root directory and Watch paths](#root-directory-and-watch-paths).

**The service shows `Git source is waiting for a successful build` for a long time.** Read **Build Logs**. A failed
build stays failed. Fix the cause. Then push, run `geass build`, or change a build setting and deploy.

**A push does not start a build.** On GitHub, open **Recent Deliveries** of the app:

- `401` means that the webhook secret in Geass does not match the secret on GitHub.
- `503` means that Geass has no webhook secret.
- `200` with `"triggered":0` means that no service matched.

For a `200` answer, check the conditions under [Branch deploys and push triggers](#branch-deploys-and-push-triggers). A
service that was never deployed does not build on a push.

**No `geass/build/<service>` status or GitHub Deployment appears.** Check these items in sequence:

1. The service must be built from a repository.
2. **Report to GitHub** must be on under **Settings** → **GitHub** of the project.
3. Click **Test GitHub App**. If it lists missing permissions, follow
   [Give an existing GitHub App the new permissions](#give-an-existing-github-app-the-new-permissions).
4. Geass reports a build when it changes state. A service that ran before you upgraded Geass reports from its next
   build or deploy.

**A required check named `geass/build` never appears.** Each service reports as `geass/build/<service name>`. Require
that name. See [The commit status of a build](#the-commit-status-of-a-build).

**A webhook delivery fails with "webhook URL cannot point at a private network address".** Use a public URL, or ask the
platform admin to allow private addresses.

**A webhook shows `receiver answered HTTP 4xx`.** Your receiver rejected the request. Fix the receiver, and click
**Send test**. Geass does not retry a `4xx`.

## Under the hood

- A build is a `GeassBuild` object in `geass-system`, named `<service ID>-build-<hash>`. It runs as the Job
  `<build>-kaniko`, with a `git` and a `kaniko` container. For an organization, the Job runs in the namespace
  `geass-build-<organization>`.
- The link of a project to GitHub is the `GeassGitHubConnection` `<project ID>-github`. Its `GitHubReporting`
  condition says whether GitHub accepts the reports.
- The platform registry is `spec.registry` on the `GeassPlatformConfig` `platform`. Geass copies the registry login to
  the Secret `<service ID>-registry-pull` in the environment namespace.
- A manifest can set the registry of one service with `spec.build.registry`. It can keep every build log in a bucket
  with `spec.logs.archiveStoreRef`.

```bash title="Inspect builds"
kubectl -n geass-system get geassbuilds -l geass.dev/app=amber-fern
kubectl -n geass-system logs job/amber-fern-build-3f9a1c2b7d4e-kaniko -c kaniko
kubectl -n geass-system get geassapp amber-fern -o jsonpath='{.status.resolvedImage}{"\n"}'
kubectl -n geass-system get geassgithubconnection proj-1a2b3c4d-github -o jsonpath='{.status.conditions[?(@.type=="GitHubReporting")]}{"\n"}'
```

To match a GitHub delivery to the log, search for its delivery ID:

```bash
kubectl -n geass-system logs deployment/geass-controller-manager -c manager | grep 72d3162e-cc78-11e3-81ab-4c9367dc0958
```

### Pin a commit

With access to the cluster, this command pins the commit of a service:

```bash title="Build one commit"
kubectl -n geass-system patch geassapp amber-fern --type merge \
  -p '{"spec":{"source":{"git":{"commit":"0123456789abcdef0123456789abcdef01234567"}}}}'
```

Run the command while the service shows **Up to date**. A service with pending changes ignores it. Then click
**Redeploy** on the service. If you do not, a later edit in the dashboard puts the old commit back.

## Related

- [Apps](/docs/guides/apps/)
- [Pull request environments](/docs/guides/pull-request-environments/)
- [Connect GitHub](/docs/guides/connectors/github/)
- [Users and API tokens](/docs/guides/users-and-tokens/)
- [Domains and TLS](/docs/guides/domains-and-tls/)
- [REST API](/docs/reference/rest-api/)
