---
title: "GitHub and builds"
description: "Deploy a GitHub repository: link a project, choose a repository, set the registry Geass builds into, run and read builds, and use webhooks."
---

Geass can build a service from a GitHub repository. You link a project to your GitHub account, choose a repository, and
Geass builds the Dockerfile with Kaniko, pushes the image to a container registry you provide, and runs it. A build
starts when you first deploy the service, when one of its build settings changes, and when you push to the branch. This
page covers choosing a repository, how builds work, the registry you must set up, and the webhooks in both directions.

## Before you start

- **A GitHub App registered in Geass.** One admin does this once for the whole cluster. See
  [Connect GitHub](/docs/guides/connectors/github/). It needs a dashboard address that is public and verified; see
  [Domains and TLS](/docs/guides/domains-and-tls/).
- **A container registry you control.** Geass does not ship a registry. Without one, a repository service does not
  build. See [Set the registry](#set-the-registry).
- **A Dockerfile** in the repository.
- Free capacity in the cluster for the build and for the service. See
  [Usage and capacity](/docs/guides/usage-and-capacity/).

If you would rather build in your own CI, push the image to your registry and run it as an [app](/docs/guides/apps/) from
an image; that path does not involve the build system at all.

## How a repository service works

1. You choose **Deploy changes**. Geass creates a **build** for the service.
2. A short-lived Kubernetes Job in `geass-system` clones the repository at one commit and runs Kaniko on its Dockerfile.
3. Kaniko pushes the image to your registry.
4. Geass records the image's digest and runs the service from it, as `<repository>@sha256:…`. While a build runs, the
   service shows "Git source is waiting for a successful build" and the previous pods keep serving.

A push to the branch repeats the cycle. See [Build a new commit](#build-a-new-commit).

## Link a project to GitHub

Each project is linked to GitHub separately, so each project can use a different GitHub account.

1. Open the project's **Settings** → **Integrations**.
2. On the **GitHub** card choose **Connect to GitHub**. If the card says "GitHub is not set up for this cluster yet",
   choose **Set up GitHub** and ask an admin to register the app first.
3. GitHub asks where to install the app. Choose the account or organization and the repositories the app may read.
4. GitHub returns you to the project with the repository list open. On **Integrations**, the card now reads "Connected.
   Repositories can be deployed as services, and pushes build automatically." See
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
| **Root directory** | Source | The folder of the repository the build runs in. The Dockerfile path is relative to it. | Empty |
| **Wait for CI** | Source | Build only after the commit's GitHub checks succeeded. | Off |
| **Dockerfile path** | Build | The Dockerfile to build. | `Dockerfile` |
| **Watch paths** | Build | Which changed files make a push build. See below. | Empty |
| **Use build cache** | Build | Reuse image layers between builds. | Off |

Then choose **Deploy changes**. See [Apps](/docs/guides/apps/#settings) for the other settings.

### Root directory and Watch paths

**Root directory** is the folder of the repository that Kaniko builds in. Leave it empty for the repository root. A
leading slash means the repository root, so `/apps/api` and `apps/api` are the same folder, and a path that leaves the
repository (`..`) is refused when you save. **Dockerfile path** is relative to the root directory: with the root
directory `apps/api`, `Dockerfile` is `apps/api/Dockerfile`. A Dockerfile path that already starts with the root
directory (`apps/api/Dockerfile`, which was how a subfolder was built before the root directory was used) still works.
Changing the root directory changes the build's name, so it starts a new build.

**Watch paths** limit which pushes build. They are gitignore-style patterns, one per line, matched against the files the
push changed, from the repository root:

| Pattern | Matches |
| --- | --- |
| `/src/**` | Everything under `src` at the top of the repository. |
| `/src` or `src/` | The same: a pattern that names a folder covers what is in it. |
| `*.go` | Go files at any depth. A pattern without a slash is matched at any depth. |
| `api/**/*.go` | Go files anywhere under `api`. A slash in the middle anchors the pattern to the repository root. |
| `!/docs/**` | Takes `docs` back out. The last pattern that matches a file decides it. |

A push builds when at least one changed file is selected. A file that no pattern matches is not selected, unless every
pattern starts with `!`, which reads as "everything except". So `**` followed by `!/docs/**` builds for every change but
a change to `docs`. With no patterns, every push builds, and so does a push that does not list its files (a branch made
at an existing commit). Watch paths apply to pushes only: the first deploy and a change to a build setting build
regardless. They apply the moment you save them.

## Set the registry

A build needs somewhere to push its image, and Geass has no registry of its own. Until one is set, a repository service
does not build: it shows `no container registry is set for builds: set one under Settings > Registry, or set
spec.build.registry.repository on the service`, and no build is made.

Geass reads the destination from the first of these that is set:

1. The service's own `spec.build.registry.repository`.
2. The platform registry: `spec.registry.repository` on the platform configuration (`GeassPlatformConfig` named
   `platform`).

An admin sets the platform registry through the API. The dashboard has no form for it yet. The login is optional: a
registry that needs none is fine.

```bash title="Set one registry for all builds"
curl --fail -X POST https://geass.example.com/api/settings/registry/save \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d repository=ghcr.io/acme/geass-builds -d username=acme-bot --data-urlencode password="$GHCR_TOKEN"

curl https://geass.example.com/api/settings/registry -H "Authorization: Bearer $GEASS_TOKEN"
# {"repository":"ghcr.io/acme/geass-builds","configured":true,"hasLogin":true}
```

`POST /api/settings/registry/clear` removes the registry and its login. To change the repository and keep the login,
send `keepExisting=on` and no username or password. The login is stored as the Secret `platform-registry` in
`geass-system`, and is never returned. The same can be done with `kubectl`, with a Docker registry Secret in
`geass-system` of your own:

```bash title="The same with kubectl"
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
  `latest`. Every service that shares the platform repository pushes into the same one. Give each service its own
  repository if you want to keep their images apart. The service runs the image by its digest, so a tag that a later
  build moves does not change what it runs.
- The platform's credential is used only when the service sets no `repository` of its own. A service with its own
  `repository` needs its own `credentialRef`.
- The credential is read from the Secret key `.dockerconfigjson` and is mounted into the build Job.
- **The nodes pull with the same credential.** When a build used a credential, Geass copies it into the service's
  environment namespace as the Secret `<service ID>-registry-pull` and adds it to the Deployment's image pull secrets.
  The copy is removed when the service stops using the credential, and with the service. A registry that needs no
  credential gets none, so its repository must be readable by your nodes without one. The credential has to be able to
  pull as well as push, and anyone who can read Secrets in the environment namespace can read the copy. Give it only the
  access that builds need, for that repository and no other, and use a registry that scopes credentials to a path. A
  Secret of that name that Geass did not make is not replaced: the service reports it.
- A new registry setting changes the build's identity, so the next time Geass reconciles the service it starts a new
  build. A service that is waiting for a build does this within about half a minute. A running service is reconciled only
  when something else touches it, so it is not rebuilt at once.

## How a build runs

The build is a `GeassBuild` object in `geass-system`, named `<service ID>-build-<12 characters>`. The characters are a
hash of the build's settings, so changing the repository, branch, pinned commit, Dockerfile path, root directory, cache
setting, registry or CI setting starts a new build record. The same settings always give the same record, and a
finished record is run again by a push or a rebuild, which put it back to the start.

1. **Resolve the commit.** Geass asks GitHub for the head commit of the branch, unless the service pins a commit with
   `spec.source.git.commit`. It does this each time the build starts, so a rebuild builds the head of the branch as it is
   then.
2. **Wait for CI**, if the service asks for it. See [Wait for CI](#wait-for-ci).
3. **Run the Job.** A Job named `<build>-kaniko`, in the project's region and on the nodes the project requires, has two
   steps:
   - an init container with the `alpine/git` image clones the branch with the project's GitHub installation token and
     checks out that commit;
   - a Kaniko container, `gcr.io/kaniko-project/executor:v1.23.2`, builds the Dockerfile with the root directory as its
     context (the whole clone by default) and pushes the result. It adds `--cache=true` when **Use build cache** is on,
     mounts the registry credential, and writes the digest of what it pushed to its termination message.

   The Job retries a failed pod up to twice. Geass deletes a finished Job and its pods an hour after it ends.
4. **Read the digest.** When the Job succeeds, Geass reads the digest of the image from the termination message of the
   `kaniko` container of a pod of that Job that finished successfully. For a Job made by an older Geass, which did not
   ask Kaniko for it, the fallback is the `Pushed <repository>@sha256:…` line of the Kaniko output. The digest is read
   for every run, so a build that runs again records the digest of the new image. With no digest the build is marked
   failed: "Kaniko completed without an immutable image digest".
5. **Run it.** The service's `status.resolvedImage` is set to the repository the build pushed to (without its tag) at that
   digest, `<repository>@sha256:…`, which is an image a node can pull, and the pods roll to it.

The build's state is one of these:

| State | Meaning |
| --- | --- |
| `Pending` | Created, or put back to the start to run again. |
| `WaitingForCI` | Waiting for GitHub checks. |
| `Running` | The Job is running. |
| `Succeeded` | The image is pushed and has a digest. |
| `Failed` | A GitHub call failed, a check failed, or the Job failed. |
| `Cancelled` | Someone cancelled it with `POST /api/apps/{id}/build/cancel`, or set `status.cancelRequested`. |

To see what a service runs:

```bash title="The image of a repository service"
kubectl -n geass-system get geassapp amber-fern -o jsonpath='{.status.resolvedImage}{"\n"}'
# ghcr.io/acme/storefront@sha256:3b9f…
```

After a few builds, the operator deletes the oldest finished builds of a service beyond the newest 10, never the build the
service runs.

### Build logs

Open the service, then **Deployments**, choose a deployment (**View logs** or its row), and open **Build Logs**. The tab
shows the output of the build's pod: the `kaniko` container, or the `git` container when the clone failed before Kaniko
started. When the pod is gone (Geass deletes a finished Job and its pods an hour after it ends) or has no output yet, it
shows the failure reason Geass recorded (for example `kaniko Job failed`) or, when there is none, the build's name and
phase. A deployment shows the service's current build as it was when its deploy started, so a deploy that started before the
operator had made the service's first build has none.

```bash title="Read a build from the terminal"
kubectl -n geass-system get geassbuilds -l geass.dev/app=amber-fern
kubectl -n geass-system logs job/amber-fern-build-3f9a1c2b7d4e-kaniko -c git
kubectl -n geass-system logs job/amber-fern-build-3f9a1c2b7d4e-kaniko -c kaniko
```

To keep the log of every build, set `spec.logs.archiveStoreRef` on the service to a [bucket](/docs/guides/object-storage/)
of the same project and environment. When a build finishes, Geass uploads its log to `builds/<build>.log` in that bucket.
The dashboard has no field for it, and does not read the archive back.

## Branch deploys and push triggers

A service builds one branch. The setting is **Branch** under **Settings** → **Source**, and a push to it builds and
deploys the service again.

This is the path a push takes:

1. GitHub sends `push` to `<dashboard URL>/webhooks/github`. Geass checks the signature.
2. For every deployed service whose repository matches, whose branch is the pushed `refs/heads/<branch>`, that does not
   pin a commit, whose [watch paths](#root-directory-and-watch-paths) select a file the push changed, and that belongs to
   the project linked to the same GitHub installation, Geass finds the build for the service's current settings: the
   one named in the service's `status.currentBuild`, among the builds labelled `geass.dev/app=<service ID>`.
3. Geass deletes that build's Job and puts the build back to `Pending`, clearing its digest, its commit, its check
   results and its log reference. The operator then resolves the new head of the branch, builds it, and records the digest
   of that run.
4. When the build succeeds the service rolls to the new image. Until then the previous pods keep serving, and the service
   shows "Git source is waiting for a successful build".

A push that deletes the branch builds nothing. A service that has no build yet is skipped: its build is made when it is
deployed. The answer to GitHub is `{"accepted":true,"triggered":N}`, where `N` is how many services were rebuilt, so `0`
means that no service matched, or that none of them watches a file the push changed.

Every build the operator makes carries the labels `geass.dev/app=<service ID>` and `geass.dev/name=<service ID>`. The
push, `POST /api/apps/{id}/build`, **Build Logs**, `geass status` and the clean-up of old builds find a service's builds
by `geass.dev/app`. Builds the operator made before it carried that label get it when the operator starts.

To deploy a second branch, add another environment (for example `staging`) and create a second service there for the
same repository with the other branch. See [Projects and environments](/docs/guides/projects-and-environments/).
For one environment per pull request, see [Pull request environments](/docs/guides/pull-request-environments/).

## Build a new commit

What starts a build:

- **The first deploy.** **Deploy changes** on a draft creates the build, and it resolves the branch's head commit at that
  moment.
- **A push to the branch.** See [Branch deploys and push triggers](#branch-deploys-and-push-triggers).
- **A change to a build setting.** Because the build's name is a hash of its settings, changing the repository, branch,
  Dockerfile path, root directory, **Use build cache**, **Wait for CI** or the registry and then choosing **Deploy
  changes** starts a new build of the branch's current head. Changing the setting back selects the earlier, finished
  build again, which does not build a newer commit by itself: push, or rebuild.
- **A rebuild by hand.** `POST /api/apps/{id}/build` puts the service's current build back to the start, so it builds the
  head of the branch as it is now. A project token for the service's environment can call it, which is how a CI pipeline
  starts a build. The dashboard has no button for it. It makes no build of its own: for a service that has none yet it
  answers that the service has not been built.

  ```bash title="Build the branch again"
  curl --fail -X POST https://geass.example.com/api/apps/amber-fern/build \
    -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json"
  ```

  `POST /api/apps/{id}/build/cancel` cancels the current build while it is waiting or running. A build that has finished
  cannot be cancelled: it answers that there is nothing to cancel.
- **A pinned commit.** Set `spec.source.git.commit` on the service to the full commit SHA you want. The dashboard has no
  field for it. A pinned commit is built once, and a push does not rebuild it.

  ```bash title="Build one commit"
  kubectl -n geass-system patch geassapp amber-fern --type merge \
    -p '{"spec":{"source":{"git":{"commit":"0123456789abcdef0123456789abcdef01234567"}}}}'
  ```

  Do this while the service shows **Up to date** (nothing pending), because a service with pending changes runs from its
  saved copy and ignores the patch. Afterwards choose **Redeploy** on the service so the saved copy matches, or a later
  edit in the dashboard will put the old commit back.

What does not start a build:

- **Redeploy**. It restarts the pods and runs the same image.
- `POST /api/token/deploy`. It deploys what is pending, and does not build a repository service's new commit.

A build that failed before this release with "Kaniko completed without an immutable image digest" stays failed. Push, or
call `POST /api/apps/{id}/build`, to run it again.

### Wait for CI

With **Wait for CI** on, Geass reads the check runs of the commit from GitHub and starts the build only when there is at
least one and all have completed with the conclusion `success`. The build state is `WaitingForCI` until then, and Geass
checks again every 15 seconds. A failed check marks the build `Failed` with `GitHub check "<name>" concluded <result>`.

A commit with no checks at all never passes, so turn the option off for a repository that has no GitHub Actions or other
checks. Listing check runs needs the **Checks: Read** permission. The GitHub App that Geass registers now asks for read
access to **Contents**, **Metadata**, **Pull requests** and **Checks**, but an app registered by an earlier version asked
for no Checks. If **Wait for CI** fails with `GitHub API returned 403 Forbidden: reading check runs needs the Checks: Read
permission…`, add **Checks: Read** to the app in GitHub and accept the new permission in your installation.

## Webhooks

### From GitHub to Geass

GitHub calls `<dashboard URL>/webhooks/github`. The URL is public, and Geass checks the `X-Hub-Signature-256` signature
against the app's webhook secret. It handles three event types:

| Event | What Geass does | Answer |
| --- | --- | --- |
| `ping` | Nothing. | `{"accepted":true,"event":"ping"}` |
| `push` | Re-runs the current build of each service it matches, as above. | `{"accepted":true,"triggered":N}` |
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
come up. See [Command line](/docs/guides/command-line/#scripts-and-ci). For a repository service, `--wait` also follows the build: it fails with the build's reason
when the build fails.

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
| Secret `platform-registry` | `geass-system` | The platform registry's login, when it was set through the API. |
| Secret `<service ID>-registry-pull` | The environment namespace | A copy of the registry login, for the nodes to pull the built image with. |
| `GeassPlatformConfig` `platform` | `geass-system` | `spec.registry` (repository and login) and the GitHub App reference. |

```bash title="Inspect repository services"
kubectl -n geass-system get geassbuilds -o custom-columns=NAME:.metadata.name,APP:.spec.app,PHASE:.status.phase,IMAGE:.status.image,DIGEST:.status.imageDigest
kubectl -n geass-system get geassbuild amber-fern-build-3f9a1c2b7d4e -o yaml
kubectl -n geass-system get geassapp amber-fern -o jsonpath='{.status.resolvedImage}{"\n"}'
kubectl -n proj-1a2b3c4d-production get deploy amber-fern -o jsonpath='{.spec.template.spec.containers[0].image}{"\n"}'
```

## Troubleshooting

**"GitHub is not connected to this cluster yet" or "Connect GitHub in platform settings first".** No GitHub App is
registered. An admin registers it under **Settings** → **Connectors** → **GitHub** (see
[Connect GitHub](/docs/guides/connectors/github/)).

**"Link this project to GitHub first".** The project has no installation. Use **Settings** → **Integrations** →
**Connect to GitHub**.

**A repository is missing from the list.** The installation cannot see it. Change the app's repository access in GitHub,
then reopen the list.

**The service says `no container registry is set for builds`.** Neither the service nor the platform names a registry.
See [Set the registry](#set-the-registry). Once one is set, the service builds within about 30 seconds.

**The build fails and Build Logs mention `no such host`, `UNAUTHORIZED` or `denied`.** The registry is wrong, or the
credentials are. See [Set the registry](#set-the-registry). After you fix it, a service that is waiting for a build starts
a new one within about 30 seconds.

**The build fails with `GitHub API returned 404 Not Found`.** The repository or branch name is wrong, or the installation
cannot read it. Check **Repository** and **Branch** in **Settings** → **Source**.

**The build fails with `GitHub API returned 403 Forbidden` and Wait for CI is on.** The app lacks the **Checks** permission.
See [Wait for CI](#wait-for-ci).

**The build stays in `WaitingForCI`.** The commit has no checks yet, or they are still running. Turn **Wait for CI** off if the
repository has none.

**"Kaniko completed without an immutable image digest".** The Job succeeded, but Geass found no digest: the pod's
termination message was empty, and the Kaniko output has no `Pushed <repository>@sha256:…` line. Read the end of
`kubectl -n geass-system logs job/<build>-kaniko -c kaniko`. If the build is from before this release (the log scan then
could not find the digest), push or call `POST /api/apps/{id}/build` to run it again. This message does not mean the push
failed.

**The build succeeded but the pods are in `ImagePullBackOff`.** The service's condition reads `The service cannot start: app
is ImagePullBackOff`, and after a few minutes it is marked `ProvisionFailed`. Print the image the Deployment uses (see the
last command under [What happens under the hood](#what-happens-under-the-hood)); it is `<repository>@sha256:…`. If the
registry needs a login, the service must have a credential: Geass copies it to the Secret `<service ID>-registry-pull`,
so check that it exists in the environment namespace and that the credential can pull from that repository. If the
registry needs no login, check that your nodes can reach it.

**A push does not trigger a build.** In GitHub's **Recent Deliveries** for the app, `401` means the webhook secret in Geass
does not match GitHub's, `503` means none is stored, and `200` with `"triggered":0` means that the push reached Geass and
no service was rebuilt. Check, for each service: that it is deployed and not a draft; that **Repository** and **Branch**
match the push; that it does not pin a commit; that its [watch paths](#root-directory-and-watch-paths) select a file the
push changed; that it has a build already (a service that was never deployed has none); and that its project is linked
to the GitHub installation the event came from. A push that deletes a branch builds nothing.

**The service shows "Git source is waiting for a successful build" for a long time.** Check the build with
`kubectl -n geass-system get geassbuilds` and read the Job's logs (see [Build logs](#build-logs)). A build that failed
stays failed. Fix the cause, then change a build setting and choose **Deploy changes**, pin a commit, or reset the build
(see [Build a new commit](#build-a-new-commit)).

**The build cannot find the Dockerfile.** The Dockerfile path is relative to the root directory, and a leading slash is
ignored. With the root directory `apps/api`, write `Dockerfile` or `docker/prod.Dockerfile`: both are looked for under
`apps/api`. See [Root directory and Watch paths](#root-directory-and-watch-paths).

**A webhook delivery fails with "webhook URL cannot point at a private network address".** The receiver is on a private
address. Use a public URL, or ask the operator to set `GEASS_WEBHOOKS_ALLOW_PRIVATE_NETWORKS=true`.

**A webhook shows `receiver answered HTTP 4xx`.** Your receiver rejected the request. Open **Recent deliveries**, fix the
receiver, and use **Send test** to try again. Geass does not retry a `4xx`.

## Related

- [Apps](/docs/guides/apps/)
- [Pull request environments](/docs/guides/pull-request-environments/)
- [Connect GitHub](/docs/guides/connectors/github/): registering the GitHub App.
- [Users and API tokens](/docs/guides/users-and-tokens/)
- [Domains and TLS](/docs/guides/domains-and-tls/)
- [REST API](/docs/reference/rest-api/)
