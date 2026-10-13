---
title: "GitHub and builds"
description: "Deploy a GitHub repository: link a project, choose a repository, set the registry Geass builds into, run and read builds, and use webhooks."
---

Geass can build a service from a GitHub repository. You link a project to your GitHub account, choose a repository, and
Geass builds the Dockerfile with Kaniko, pushes the image to a container registry you provide, and runs it. A build
starts when you first deploy the service or when one of its build settings changes. A push to the branch does not start
one today. This page covers choosing a repository, how builds work, the registry you must set up, and the webhooks in
both directions.

## Before you start

- **A GitHub App registered in Geass.** One admin does this once for the whole cluster. See
  [Cloud connections](/docs/guides/connections/#github). It needs a dashboard address that is public and verified; see
  [Domains and TLS](/docs/guides/domains-and-tls/).
- **A container registry you control.** Geass does not ship a registry. Without one, builds cannot push their image. See
  [Set the registry](#set-the-registry).
- **A Dockerfile** in the repository.
- Free capacity in the cluster for the build and for the service. See
  [Usage and capacity](/docs/guides/usage-and-capacity/).

:::caution
Repository builds are not dependable in this release. The code shows these problems:

- A push to the branch never starts a build. See [Build a new commit](#build-a-new-commit).
- The image Geass runs is the value in `status.resolvedImage`. Geass fills it from the first word of the Kaniko output
  that starts with `sha256:`, or from a `geass.dev/image-digest` annotation on the build Job, which Geass never writes.
  A word that starts with `sha256:` has no registry or repository in front of it, so the Deployment is given a bare
  `sha256:…` image that the pods cannot pull.
- The registry setting has no form in the dashboard.
- Several lookups find a service's builds by a label that the builds Geass creates do not carry. That is why a push
  starts nothing, and also why the **Build Logs** tab, `geass status`, the `app_status` tool and the clean-up of old
  builds do not see them. See [GeassBuild](/docs/reference/resources/geass-build/#how-the-dashboard-uses-builds).

If you would rather build in your own CI, push the image to your registry and run it as an [app](/docs/guides/apps/) from
an image; that path does not involve the build system at all.
:::

## How a repository service works

1. You choose **Deploy changes**. Geass creates a **build** for the service.
2. A short-lived Kubernetes Job in `geass-system` clones the repository at one commit and runs Kaniko on its Dockerfile.
3. Kaniko pushes the image to your registry.
4. Geass records the image's digest and runs the service from it. While a build runs, the service shows "Git source is
   waiting for a successful build" and the previous pods keep serving.

A new commit on the branch does not repeat the cycle by itself. See [Build a new commit](#build-a-new-commit).

## Link a project to GitHub

Each project is linked to GitHub separately, so each project can use a different GitHub account.

1. Open the project's **Settings** → **Integrations**.
2. On the **GitHub** card choose **Connect to GitHub**. If the card says "GitHub is not set up for this cluster yet",
   choose **Set up GitHub** and ask an admin to register the app first.
3. GitHub asks where to install the app. Choose the account or organization and the repositories the app may read.
4. GitHub returns you to the project with the repository list open. On **Integrations**, the card now reads "Connected.
   Repositories can be deployed as services, and pushes build automatically." The second half is not true today: see
   [Build a new commit](#build-a-new-commit). **Disconnect** unlinks the project. Services already created keep their
   settings, and a project that is unlinked ignores GitHub's `push` and `pull_request` events.

You can also start the same flow from **+ Add resource** → **GitHub Repository** when the project is not linked yet.

Only repositories the installation can access are listed or built. To add one, change the app's repository access in
GitHub (your account's **Settings** → **Applications** → **Installed GitHub Apps**).

## Choose a repository

1. Choose **+ Add resource** → **GitHub Repository**.
2. The list shows the repositories of the linked installation (up to 500, sorted by name), each marked **Public** or
   **Private**. Type to filter. The list shows the first 50 matches.
3. Choose one. Geass saves a **draft** service that builds the repository's default branch and opens its **Settings**.

Another way is to paste `acme/storefront`, or a link such as `https://github.com/acme/storefront/tree/staging`, into the
first search box and choose **Deploy acme/storefront**. A link with `/tree/<branch>` uses that branch. A bare
`owner/name` uses `main`. If the project is not linked yet, you are asked to link it first.

Then open the service's **Settings** and check:

| Setting | Section | Meaning | Default |
| --- | --- | --- | --- |
| **Repository** | Source | `owner/name`. | The repository you chose |
| **Branch** | Source | The branch built and deployed. | The default branch |
| **Root directory** | Source | Intended build directory. The build ignores it; see below. | Empty |
| **Wait for CI** | Source | Build only after the commit's GitHub checks succeeded. | Off |
| **Dockerfile path** | Build | The Dockerfile to build. | `Dockerfile` |
| **Watch paths** | Build | Intended filter for pushes. Saved, but not applied; see below. | Empty |
| **Use build cache** | Build | Reuse image layers between builds. | Off |

Then choose **Deploy changes**. See [Apps](/docs/guides/apps/#settings) for the other settings.

:::caution
**Root directory** and **Watch paths** are saved but do nothing today. The build always uses the whole repository as
its context (`dir:///workspace`), and nothing reads **Watch paths**. Changing **Root directory** does change the build's
name, so it starts a new build. To build from a subdirectory, put its Dockerfile path in **Dockerfile path**
(`apps/api/Dockerfile`; the field's placeholder shows a leading slash, but the clone is in `/workspace` and Geass passes
the path to Kaniko as written), and make the Dockerfile's `COPY` lines relative to the repository root.
:::

## Set the registry

A build needs somewhere to push its image. Geass reads the destination from the first of these that is set:

1. The service's own `spec.build.registry.repository`.
2. The cluster's `spec.registry.repository` on the platform configuration (`GeassPlatformConfig` named `platform`).

If neither is set, Geass uses `registry.geass.dev/<project ID>/<service ID>:<revision>`. That is a placeholder, not a
registry you can use, and the build fails when it tries to push. The dashboard has no form for the registry: you set it
with `kubectl`.

Create a pull and push credential as a Docker registry Secret in `geass-system`, then name the repository and the Secret
on the platform configuration. This sets it for every repository service in the cluster:

```bash title="Set one registry for all builds"
kubectl -n geass-system create secret docker-registry registry-creds \
  --docker-server=ghcr.io --docker-username=acme-bot --docker-password="$GHCR_TOKEN"

kubectl apply -f - <<'EOF'
apiVersion: geass.geass.dev/v1alpha1
kind: GeassPlatformConfig
metadata:
  name: platform
  namespace: geass-system
spec:
  registry:
    repository: ghcr.io/acme/geass-builds
    credentialRef:
      name: registry-creds
EOF
```

To give one service its own repository instead, set it on the service:

```yaml title="Per-service registry (excerpt of the GeassApp spec)"
spec:
  build:
    registry:
      repository: ghcr.io/acme/storefront
      credentialRef:
        name: registry-creds
```

A few things to know:

- `repository` is used as the Kaniko destination exactly as written, with no tag, so the image is pushed with the tag
  `latest`. Every service that shares a platform-wide repository pushes into the same one. Give each service its own
  repository if you want to keep their images apart.
- The platform's credential is used only when the service sets no `repository` of its own. A service with its own
  `repository` needs its own `credentialRef`.
- The credential is read from the Secret key `.dockerconfigjson` and is mounted into the build Job only.
- Geass attaches an image pull secret only to services that run an image you gave it. A pod that runs a built image has
  none, so the repository must be readable by your nodes without credentials (a public repository), or by credentials you
  configured on the nodes themselves.
- A new registry setting changes the build's identity, so the next time Geass reconciles the service it starts a new
  build. A service that is waiting for a build does this within about half a minute. A running service is reconciled only
  when something else touches it, so it is not rebuilt at once.

## How a build runs

The build is a `GeassBuild` object in `geass-system`, named `<service ID>-build-<12 characters>`. The characters are a
hash of the build's settings, so changing the repository, branch, pinned commit, Dockerfile path, root directory, cache
setting, registry or CI setting starts a new build record. The same settings always give the same record, and a
finished record is not run again by itself.

1. **Resolve the commit.** Geass asks GitHub for the head commit of the branch, unless the service pins a commit with
   `spec.source.git.commit`.
2. **Wait for CI**, if the service asks for it. See [Wait for CI](#wait-for-ci).
3. **Run the Job.** A Job named `<build>-kaniko`, in the project's region and on the nodes the project requires, has two
   steps:
   - an init container with the `alpine/git` image clones the branch with the project's GitHub installation token and
     checks out that commit;
   - a Kaniko container, `gcr.io/kaniko-project/executor:v1.23.2`, builds the Dockerfile with the whole clone as its context
     and pushes the result. It adds `--cache=true` when **Use build cache** is on, and mounts the registry credential.

   The Job retries a failed pod up to twice. Geass deletes a finished Job and its pods an hour after it ends.
4. **Read the digest.** When the Job succeeds, Geass reads the image digest from a `geass.dev/image-digest` annotation on
   the Job or, failing that, from the Kaniko output (the first word that starts with `sha256:`). With no digest, the build
   is marked failed: "Kaniko completed without an immutable image digest". A build that already has a digest from an
   earlier run does not read a new one, so clear it when you run a build again (see
   [GeassBuild](/docs/reference/resources/geass-build/#re-run-a-build)).
5. **Run it.** The service's `status.resolvedImage` is set to the digest, and the pods roll to it.

The build's state is one of these:

| State | Meaning |
| --- | --- |
| `Pending` | Created, or reset to run again. |
| `WaitingForCI` | Waiting for GitHub checks. |
| `Running` | The Job is running. |
| `Succeeded` | The image is pushed and has a digest. |
| `Failed` | A GitHub call failed, a check failed, or the Job failed. |
| `Cancelled` | Someone set `status.cancelRequested`. The `/build/cancel` endpoint can only do this to a build it can find; see [Build a new commit](#build-a-new-commit). |

:::caution
Geass runs the service from the value it stored in `status.resolvedImage`, exactly as stored. Check it after your first
successful build: `kubectl -n geass-system get geassapp <service ID> -o jsonpath='{.status.resolvedImage}'`. If it is a bare
`sha256:…` value, the pods stay in `ImagePullBackOff`; see [Troubleshooting](#troubleshooting).
:::

### Build logs

Open the service, then **Deployments**, choose a deployment (**View logs** or its row), and open **Build Logs**. The tab
has content only for a deployment that has a build attached, and it shows the failure reason Geass recorded (for example
`kaniko Job failed`) or, when there is none, the build's name and phase. It does not show the Kaniko output: Geass looks for
a pod named `<build>-kaniko`, and the pods of a Job have a random suffix. Read the output with `kubectl`, within the hour
before Geass deletes the Job and its pods.

```bash title="Read a build from the terminal"
kubectl -n geass-system get geassbuilds
kubectl -n geass-system logs job/amber-fern-build-3f9a1c2b7d4e-kaniko -c git
kubectl -n geass-system logs job/amber-fern-build-3f9a1c2b7d4e-kaniko -c kaniko
```

To keep the log of every build, set `spec.logs.archiveStoreRef` on the service to a [bucket](/docs/guides/object-storage/)
of the same project and environment. When a build finishes, Geass uploads its log to `builds/<build>.log` in that bucket.
The dashboard has no field for it.

## Branch deploys and push triggers

A service builds one branch. The setting is **Branch** under **Settings** → **Source**, and the form calls it "Branch
connected to production" and says pushes to it "are built and deployed automatically". That is not what happens today.

:::caution
A push to the branch does not start a build. GitHub's `push` event reaches Geass and is accepted, and the answer says
`"triggered":0`, but nothing is rebuilt or redeployed.
:::

This is the path a push takes, so you can see where it stops:

1. GitHub sends `push` to `<dashboard URL>/webhooks/github`. Geass checks the signature.
2. For every deployed service whose repository matches, whose branch is the pushed `refs/heads/<branch>`, and that belongs
   to the project linked to the same GitHub installation, Geass looks for the service's build by the label
   `geass.dev/app=<service ID>` and re-runs the newest one it finds.
3. The builds Geass creates for a service carry the label `geass.dev/name=<service ID>`, not `geass.dev/app`. The lookup
   finds none, so Geass skips the service without an error.

The same missing label means other things work less than the dashboard suggests. A build Geass made is not found by
`POST /api/apps/{id}/build`, the **Deployments** list cannot attach it to a deployment record, `geass status` and the
`app_status` tool report no build for the service, and the clean-up that is meant to delete builds beyond the newest 10 finds
none to delete.

To deploy a second branch, add another environment (for example `staging`) and create a second service there for the
same repository with the other branch. See [Projects and environments](/docs/guides/projects-and-environments/).
For one environment per pull request, see [Pull request environments](/docs/guides/pull-request-environments/).

## Build a new commit

What starts a build today:

- **The first deploy.** **Deploy changes** on a draft creates the build, and it resolves the branch's head commit at that
  moment.
- **A change to a build setting.** Because the build's name is a hash of its settings, changing the repository, branch,
  Dockerfile path, root directory, **Use build cache** or **Wait for CI** and then choosing **Deploy changes** starts a new
  build of the branch's current head. Changing the setting back selects the earlier, finished build again, so it does not
  build a newer commit.
- **A reset by hand.** Put the service's own build back to `Pending`, as shown in
  [GeassBuild](/docs/reference/resources/geass-build/#re-run-a-build). That is what builds the current head of the branch
  again, or the same commit again, without changing a setting.
- **A pinned commit.** Set `spec.source.git.commit` on the service to the full commit SHA you want. The dashboard has no
  field for it.

  ```bash title="Build one commit"
  kubectl -n geass-system patch geassapp amber-fern --type merge \
    -p '{"spec":{"source":{"git":{"commit":"0123456789abcdef0123456789abcdef01234567"}}}}'
  ```

  Do this while the service shows **Up to date** (nothing pending), because a service with pending changes runs from its
  saved copy and ignores the patch. Afterwards choose **Redeploy** on the service so the saved copy matches, or a later
  edit in the dashboard will put the old commit back.

What does not start a build:

- A push, as above.
- **Redeploy**. It restarts the pods and runs the same image.
- `POST /api/apps/{id}/build`. It looks builds up by the label `geass.dev/app`, finds none for a service whose build Geass
  made, and creates a separate `GeassBuild` named `<service ID>-build-<random>`. No service uses that build, and it has
  no GitHub credential, so it cannot clone a private repository. `POST /api/apps/{id}/build/cancel` has the same gap and
  does not cancel the service's own build.

If you want a pipeline that ships every commit, build the image in your CI and deploy it as an image service, as in
[From your CI to Geass](#from-your-ci-to-geass).

### Wait for CI

With **Wait for CI** on, Geass reads the check runs of the commit from GitHub and starts the build only when there is at
least one and all have completed with the conclusion `success`. The build state is `WaitingForCI` until then, and Geass
checks again every 15 seconds. A failed check marks the build `Failed` with `GitHub check "<name>" concluded <result>`.

A commit with no checks at all never passes, so turn the option off for a repository that has no GitHub Actions or other
checks. The GitHub App Geass registers asks for read access to **Contents**, **Metadata** and **Pull requests**, not to
**Checks**. If **Wait for CI** fails with `GitHub API returned 403 Forbidden`, add the **Checks: Read** permission to the
app in GitHub and accept the new permission in your installation.

## Webhooks

### From GitHub to Geass

GitHub calls `<dashboard URL>/webhooks/github`. The URL is public, and Geass checks the `X-Hub-Signature-256` signature
against the app's webhook secret. It handles three event types:

| Event | What Geass does | Answer |
| --- | --- | --- |
| `ping` | Nothing. | `{"accepted":true,"event":"ping"}` |
| `push` | Looks for builds to re-run, as above. It finds none for builds Geass created. | `{"accepted":true,"triggered":N}`, with `N` always 0 for those builds |
| `pull_request` | Creates or removes [pull request environments](/docs/guides/pull-request-environments/). | `{"accepted":true,"environments":[…]}` |
| Anything else | Ignores it. | `{"accepted":true,"ignored":true}` |

A bad signature gets `401`, and `503` means the webhook secret is not configured. In GitHub, **Advanced** → **Recent
Deliveries** on the app shows each delivery and Geass's answer. The **Test GitHub App** button in **Settings** →
**Connectors** checks that the app's webhook URL matches the dashboard address.

### From your CI to Geass

Geass has no per-service deploy hook URL. Use an API token. Create a **project token** under the project's **Settings** →
**Tokens**, limited to one environment, store it as a secret in your CI, and call the API. See
[Users and API tokens](/docs/guides/users-and-tokens/).

For a service that runs an image, set the new image and deploy in one call. This is the usual CI step:

```bash title="Deploy a new image from CI"
curl --fail -X POST https://geass.example.com/api/apps/amber-fern/update \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d image=ghcr.io/acme/storefront:$GIT_SHA -d deploy=on
```

To deploy whatever is pending in the token's environment (for example after you set variables), use the deploy endpoint:

```bash title="Deploy pending changes"
curl --fail -X POST https://geass.example.com/api/token/deploy \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json"
```

`/api/token/deploy` does not build a repository service's new commit; see [Build a new commit](#build-a-new-commit). A
project token can also scale, change variables and secrets, retry a failed provision and read logs, but only for the
services of its own environment.

The `geass` command line does the same in a pipeline: `geass api PATCH /apps/amber-fern -d '{"image": "…"}'` saves the new
image and `geass deploy amber-fern --wait` deploys it and fails the job with exit code `5` or `6` when the service does not
come up. See [Command line](/docs/guides/command-line/#scripts-and-ci). For a repository service, `--wait` has no build to report on, because of the label problem above, so it follows the
service's state alone.

### Outgoing webhooks

Geass sends a signed `POST` to the URLs you add when a service deploy starts, succeeds or fails. Use it for chat
messages, a pipeline trigger or an audit log.

1. Open the project's **Settings** → **Webhooks** and choose **New webhook**.
2. Enter the **Payload URL**. It must be `http://` or `https://`, with no username or password. Choose the **Events**
   (**Deployment started**, **Deployment succeeded**, **Deployment failed**). Leave all unticked to receive every event.
3. Choose **Create webhook**. A dialog shows the **Signing secret** once. Copy it now.

A webhook's menu has **Send test**, **Edit**, **Pause** or **Enable**, **Rotate signing secret** and **Delete**. **Recent
deliveries** lists the last 20 attempts (time, event, webhook, HTTP result, attempts and duration). A project can have at
most 20 webhooks.

**When events are sent.** `deployment.started` when a deploy of a service starts from the dashboard, a token or the API.
`deployment.succeeded` when that deploy's pods are ready, and `deployment.failed` when provisioning gives up. Each is sent
once per deploy. Deploys of databases and buckets send nothing, and a build by itself sends nothing. **Send test** sends
an event of type `test`.

A delivery is a JSON body like this:

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

`service.kind` is `service` or `function`. `deployment.commit` appears only for a repository service that pins a commit in
`spec.source.git.commit`. `message` carries a reason for a failure.

The request has these headers:

| Header | Value |
| --- | --- |
| `Content-Type` | `application/json` |
| `User-Agent` | `Geass-Webhooks/1` |
| `X-Geass-Event` | The event type. |
| `X-Geass-Delivery` | A delivery ID, `dlv_…`. |
| `X-Geass-Timestamp` | Unix seconds. |
| `X-Geass-Signature` | `t=<unix seconds>,v1=<hex>`, where `v1` is the HMAC-SHA256 of `<t>.<raw body>` with the signing secret. |

Verify the signature on the raw body and reject old timestamps:

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

**Retries and limits.** A delivery times out after 10 seconds. Geass retries up to three attempts in total, waiting 1
second and then 5 seconds, when it cannot connect or the receiver answers `5xx`, `429` or `408`. Any other answer of 300
or more is final. Redirects are not followed, and up to eight deliveries run at once.

**Allowed addresses.** A webhook cannot point at `localhost`, a loopback address, a link-local address (including the
cloud metadata address) or a multicast address. It also cannot point at a private network address (RFC 1918 or
100.64.0.0/10) unless the operator sets `GEASS_WEBHOOKS_ALLOW_PRIVATE_NETWORKS=true` in its environment, which lets a receiver
inside the cluster work (see [Configuration](/docs/reference/configuration/)). Geass checks the address again when it connects, so a name that resolves to a
blocked address is refused.

Through the API, with a workspace admin token: `POST /api/projects/{project}/webhooks/save` (fields `url`, repeated `event`,
`disabled`, and `id` to update), `…/delete`, `…/rotate` and `…/test`, the last three with `id`. The create call answers
with `id` and `secret`. See the [REST API](/docs/reference/rest-api/).

## What happens under the hood

| Object | Namespace | What it is |
| --- | --- | --- |
| `GeassGitHubConnection` `<project ID>-github` | `geass-system` | The project's link to GitHub. |
| Secret `<project ID>-github-token` | `geass-system` | The installation token, installation ID and account. |
| `GeassBuild` `<service ID>-build-<hash>` | `geass-system` | One build. |
| Job `<build>-kaniko` and its pod | `geass-system` | The clone and Kaniko steps. |
| `GeassPlatformConfig` `platform` | `geass-system` | `spec.registry` and the GitHub App reference. |

```bash title="Inspect repository services"
kubectl -n geass-system get geassbuilds -o custom-columns=NAME:.metadata.name,APP:.spec.app,PHASE:.status.phase,IMAGE:.status.image
kubectl -n geass-system get geassbuild amber-fern-build-3f9a1c2b7d4e -o yaml
kubectl -n geass-system get geassapp amber-fern -o jsonpath='{.status.resolvedImage}{"\n"}'
kubectl -n proj-1a2b3c4d-production get deploy amber-fern -o jsonpath='{.spec.template.spec.containers[0].image}{"\n"}'
```

## Troubleshooting

**"GitHub is not connected to this cluster yet" or "Connect GitHub in platform settings first".** No GitHub App is
registered. An admin registers it under **Settings** → **Connectors** → **GitHub** (see
[Cloud connections](/docs/guides/connections/#github)).

**"Link this project to GitHub first".** The project has no installation. Use **Settings** → **Integrations** →
**Connect to GitHub**.

**A repository is missing from the list.** The installation cannot see it. Change the app's repository access in GitHub,
then reopen the list.

**The build fails and Build Logs mention `registry.geass.dev`, `no such host`, `UNAUTHORIZED` or `denied`.** No registry is
set, or the credentials are wrong. See [Set the registry](#set-the-registry). After you fix it, a service that is waiting
for a build starts a new one within about 30 seconds.

**The build fails with `GitHub API returned 404 Not Found`.** The repository or branch name is wrong, or the installation
cannot read it. Check **Repository** and **Branch** in **Settings** → **Source**.

**The build fails with `GitHub API returned 403 Forbidden` and Wait for CI is on.** The app lacks the **Checks** permission.
See [Wait for CI](#wait-for-ci).

**The build stays in `WaitingForCI`.** The commit has no checks yet, or they are still running. Turn **Wait for CI** off if the
repository has none.

**"Kaniko completed without an immutable image digest".** The build pushed (or failed to push) and Geass found no word
starting with `sha256:` in the Kaniko output. A word that carries the digest after the repository name, such as
`<repository>@sha256:…`, does not match, so this can happen on every build even when the push worked. Read the end of
`kubectl -n geass-system logs job/<build>-kaniko -c kaniko`. If the push succeeded, build the image in your own CI instead
and run it as an image service.

**The build succeeded but the pods are in `ImagePullBackOff`.** The service's condition reads `The service cannot start: app is
ImagePullBackOff`, and after a few minutes it is marked `ProvisionFailed`. Print the image the Deployment uses (see the last command
under [What happens under the hood](#what-happens-under-the-hood)). If it is a repository reference, the registry
repository is private and the nodes have no credentials for it: make it readable, or run the image you built as an image
service with a pull secret ([Apps](/docs/guides/apps/#pull-a-private-image)). If it is only a `sha256:…` value with no
repository in front of it, the pods cannot pull it, so run your own image as an image service until that is fixed.

**A push does not trigger a build.** It never does today, even when everything is set up: see
[Branch deploys and push triggers](#branch-deploys-and-push-triggers) and [Build a new commit](#build-a-new-commit). In
GitHub's **Recent Deliveries**, `401` means the webhook secret in Geass does not match GitHub's, `503` means none is
stored, and `200` with `"triggered":0` is the normal answer.

**The service shows "Git source is waiting for a successful build" for a long time.** Check the build with
`kubectl -n geass-system get geassbuilds` and read the Job's logs (see [Build logs](#build-logs)). A build that failed
stays failed. Fix the cause, then change a build setting and choose **Deploy changes**, pin a commit, or reset the build
(see [Build a new commit](#build-a-new-commit)).

**Root directory or Watch paths do nothing.** They are saved but not used. See the caution under
[Choose a repository](#choose-a-repository).

**A webhook delivery fails with "webhook URL cannot point at a private network address".** The receiver is on a private
address. Use a public URL, or ask the operator to set `GEASS_WEBHOOKS_ALLOW_PRIVATE_NETWORKS=true`.

**A webhook shows `receiver answered HTTP 4xx`.** Your receiver rejected the request. Open **Recent deliveries**, fix the
receiver, and use **Send test** to try again. Geass does not retry a `4xx`.

## Related

- [Apps](/docs/guides/apps/)
- [Pull request environments](/docs/guides/pull-request-environments/)
- [Cloud connections](/docs/guides/connections/#github): registering the GitHub App.
- [Users and API tokens](/docs/guides/users-and-tokens/)
- [Domains and TLS](/docs/guides/domains-and-tls/)
- [REST API](/docs/reference/rest-api/)
