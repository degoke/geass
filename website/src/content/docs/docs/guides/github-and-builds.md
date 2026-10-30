---
title: "GitHub and builds"
description: "Deploy a GitHub repository: link a project, set the build registry, run and read builds, report to GitHub, and use webhooks."
---

Geass can build a service from a GitHub repository. You link a project to your GitHub account and select a repository.
Geass then builds the Dockerfile with Kaniko, pushes the image to a container registry that you provide, and runs the
image. A build starts in these cases:

- When you first deploy the service.
- When one of its build settings changes.
- When you push to the branch.

Geass also tells GitHub the result of each build and deploy. This page tells you about these topics:

- How to select a repository.
- How builds work.
- The registry that you must set up.
- What Geass reports back to GitHub.
- The webhooks in both directions.

## Before you start

- **A GitHub App registered in Geass.** One admin does this one time for the whole cluster. See
  [Connect GitHub](/docs/guides/connectors/github/). The app needs a dashboard address that is public and verified. See
  [Domains and TLS](/docs/guides/domains-and-tls/). An app that an earlier Geass registered builds and deploys as before.
  But it cannot report back to GitHub until its owner approves three more permissions: **Commit statuses**,
  **Deployments**, and **Pull requests** at **Read and write**. An app from an even earlier Geass also has no **Checks**
  permission, and **Wait for CI** fails for it. See
  [Give an existing GitHub App the new permissions](#give-an-existing-github-app-the-new-permissions).
- **A container registry you control.** Geass does not include a registry. Without a registry, a repository service does
  not build. See [Set the registry](#set-the-registry).
- **A Dockerfile** in the repository.
- Free capacity in the cluster for the build and for the service. See
  [Usage and capacity](/docs/guides/usage-and-capacity/).

If you prefer to build in your own CI, push the image to your registry, and run it as an [app](/docs/guides/apps/) from
an image. That path does not use the build system at all.

## How a repository service works

1. You click **Deploy changes**. Geass creates a **build** for the service.
2. A short-lived Kubernetes Job in `geass-system` clones the repository at one commit and runs Kaniko on its Dockerfile.
   For a project that belongs to an [organization](/docs/guides/organizations-and-workspaces/#builds-and-the-registry),
   the Job runs in the own build namespace of the organization, `geass-build-<organization>`. That namespace holds only
   the credentials of that build, and it reaches no private address. The organization can have a registry of its own.
3. Kaniko pushes the image to your registry.
4. Geass records the digest of the image and runs the service from it, as `<repository>@sha256:…`. While a build runs,
   the service shows "Git source is waiting for a successful build", and the previous pods continue to serve.

A push to the branch does the cycle again. See [Build a new commit](#build-a-new-commit). On GitHub, the commit shows a
`geass/build/<service name>` status, and the deploy shows as a deployment. See [Reporting back to GitHub](#reporting-back-to-github).

## Link a project to GitHub

You link each project to GitHub separately, so each project can use a different GitHub account.

1. Open **Settings** → **GitHub** of the project.
2. On the **GitHub** card, click **Connect to GitHub**. If the card says "GitHub is not set up for this cluster yet",
   click **Set up GitHub**, and ask an admin to register the app first.
3. GitHub asks where to install the app. Select the account or organization, and the repositories that the app can read.
4. GitHub returns you to the project, with the repository list open. On **GitHub**, the card now shows "Connected.
   Repositories can be deployed as services, and pushes build automatically." See
   [Build a new commit](#build-a-new-commit).

To unlink the project, click **Disconnect**. Services that you already created keep their settings. A project that is
unlinked ignores the `push` and `pull_request` events of GitHub.

When the project is not linked yet, you can also start the same flow from **+ Add resource** → **GitHub Repository**.

Geass lists and builds only the repositories that the installation can access. To add a repository, change the
repository access of the app in GitHub. In GitHub, open **Settings** → **Applications** → **Installed GitHub Apps** of
your account.

## Choose a repository

1. Select **+ Add resource** → **GitHub Repository**.
2. The list shows the repositories of the linked installation, up to 500, sorted by name. Each repository is marked
   **Public** or **Private**. To filter the list, enter text. The list shows the first 50 matches.
3. Select a repository. Geass saves a **draft** service that builds the default branch of the repository, and opens its
   **Settings**.

As an alternative, paste `acme/storefront`, or a link such as `https://github.com/acme/storefront/tree/staging`, into
the first search box. Then click **Deploy acme/storefront**. A link with `/tree/<branch>` uses that branch. A bare
`owner/name` uses `main`. If the project is not linked yet, Geass asks you to link it first.

Then open the **Settings** of the service, and check these settings:

| Setting | Section | Meaning | Default |
| --- | --- | --- | --- |
| **Repository** | Source | `owner/name`. | The repository that you selected |
| **Branch** | Source | The branch that Geass builds and deploys. | The default branch |
| **Root directory** | Source | The folder of the repository that the build runs in. The Dockerfile path is relative to it. | Empty |
| **Wait for CI** | Source | Build only after the GitHub checks of the commit succeeded. | Off |
| **Dockerfile path** | Build | The Dockerfile to build. | `Dockerfile` |
| **Watch paths** | Build | Which changed files make a push start a build. See [Root directory and Watch paths](#root-directory-and-watch-paths). | Empty |
| **Use build cache** | Build | Use image layers again between builds. | Off |

Then click **Deploy changes**. For the other settings, see [Apps](/docs/guides/apps/#settings).

### Root directory and Watch paths

**Root directory** is the folder of the repository that Kaniko builds in. For the repository root, leave it empty. A
leading slash means the repository root, so `/apps/api` and `apps/api` are the same folder. When you save, Geass refuses
a path that leaves the repository (`..`).

**Dockerfile path** is relative to the root directory. With the root directory `apps/api`, `Dockerfile` is
`apps/api/Dockerfile`. A Dockerfile path that already starts with the root directory still works, for example
`apps/api/Dockerfile`. Before the root directory existed, this was how you built a subfolder. A change to the root
directory changes the name of the build, so it starts a new build.

**Watch paths** limit which pushes start a build. They are gitignore-style patterns, one on each line. Geass matches them
against the files that the push changed, from the repository root:

| Pattern | Matches |
| --- | --- |
| `/src/**` | All under `src` at the top of the repository. |
| `/src` or `src/` | The same: a pattern that names a folder includes what is in the folder. |
| `*.go` | Go files at any depth. A pattern without a slash matches at any depth. |
| `api/**/*.go` | Go files anywhere under `api`. A slash in the middle anchors the pattern to the repository root. |
| `!/docs/**` | Removes `docs` again. The last pattern that matches a file decides for that file. |

A push starts a build when at least one changed file is selected. A file that no pattern matches is not selected. The
exception is when every pattern starts with `!`. In that case, the patterns mean "all files except". Thus `**` followed
by `!/docs/**` starts a build for every change except a change to `docs`.

With no patterns, every push starts a build. A push that does not list its files also starts a build, for example a
branch made at an existing commit. Watch paths apply to pushes only. The first deploy and a change to a build setting
start a build in all cases. Watch paths apply immediately when you save them.

## Set the registry

A build needs a location to push its image to, and Geass has no registry of its own. Until a registry is set, a
repository service does not build, and Geass makes no build. The service shows `no container registry is set for builds: set one under Settings > Registry, or set spec.build.registry.repository on the service`.

Geass reads the destination from the first of these that is set:

1. The own `spec.build.registry.repository` of the service.
2. For a project of an [organization](/docs/guides/organizations-and-workspaces/#builds-and-the-registry), the own
   registry of the organization.
3. The platform registry: `spec.registry.repository` on the platform configuration (`GeassPlatformConfig` named
   `platform`). With [registry isolation](/docs/operations/registry-isolation/) set, Geass never uses the platform
   registry for the project of an organization.

An admin sets the platform registry through the API. The dashboard has no form for it yet. The login is optional. A
registry that needs no login is acceptable.

```bash title="Set one registry for all builds"
curl --fail -X POST https://geass.example.com/api/settings/registry/save \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d repository=ghcr.io/acme/geass-builds -d username=acme-bot --data-urlencode password="$GHCR_TOKEN"

curl https://geass.example.com/api/settings/registry -H "Authorization: Bearer $GEASS_TOKEN"
# {"repository":"ghcr.io/acme/geass-builds","configured":true,"hasLogin":true}
```

`POST /api/settings/registry/clear` removes the registry and its login. To change the repository and keep the login,
send `keepExisting=on` and no username or password. Geass stores the login as the Secret `platform-registry` in
`geass-system`, and never returns it. You can do the same with `kubectl`, with your own Docker registry Secret in
`geass-system`:

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

To give one service its own repository instead, set the repository on the service:

```yaml title="Per-service registry (excerpt of the GeassApp spec)"
spec:
  build:
    registry:
      repository: ghcr.io/acme/storefront
      credentialRef:
        name: registry-creds
```

Know these facts about the registry:

- Geass uses `repository` as the Kaniko destination exactly as written, with no tag. Thus Kaniko pushes the image with
  the tag `latest`. Every service that shares the platform repository pushes into the same repository. To keep the
  images of services apart, give each service its own repository. The service runs the image by its digest. Thus a tag
  that a later build moves does not change what the service runs.
- Geass uses the credential of the platform only when the service sets no `repository` of its own. A service with its
  own `repository` needs its own `credentialRef`.
- Geass reads the credential from the Secret key `.dockerconfigjson`, and it mounts the credential into the build Job.
- **The nodes pull with the same credential.** When a build used a credential, Geass copies the credential into the
  environment namespace of the service as the Secret `<service ID>-registry-pull`. Geass also adds it to the image pull
  secrets of the Deployment. Geass removes the copy when the service stops using the credential, and with the service.
- A registry that needs no credential gets none. Thus your nodes must be able to read its repository without a
  credential.
- The credential must be able to pull as well as push. Anyone who can read Secrets in the environment namespace can read
  the copy. Give the credential only the access that builds need, for that repository and no other. Use a registry that
  limits credentials to a path.
- Geass does not replace a Secret of that name that Geass did not make. The service reports the problem.
- A new registry setting changes the identity of the build. Thus Geass starts a new build the next time it reconciles
  the service. A service that waits for a build does this in about half a minute. Geass reconciles a running service
  only when something else touches it. Thus Geass does not rebuild a running service immediately.

## How a build runs

The build is a `GeassBuild` object in `geass-system`, named `<service ID>-build-<12 characters>`. Its Job can run in the
build namespace of an organization. The `status.jobNamespace` of the build says where. The 12 characters are a hash of
the settings of the build. Thus a change to any of these settings starts a new build record:

- The repository.
- The branch.
- The pinned commit.
- The Dockerfile path.
- The root directory.
- The cache setting.
- The registry.
- The CI setting.

The same settings always give the same record. A push or a rebuild runs a finished record again, because it puts the
record back to the start.

1. **Resolve the commit.** Geass asks GitHub for the head commit of the branch, unless the service pins a commit with
   `spec.source.git.commit`. Geass does this each time the build starts. Thus a rebuild builds the head of the branch as
   it is at that time.
2. **Wait for CI**, if the service asks for it. See [Wait for CI](#wait-for-ci).
3. **Run the Job.** A Job named `<build>-kaniko` runs in the region of the project, on the nodes that the project
   requires. The Job has two steps:
   - An init container with the `alpine/git` image clones the branch with the GitHub installation token of the project.
     Then it checks out that commit.
   - A Kaniko container, `gcr.io/kaniko-project/executor:v1.23.2`, builds the Dockerfile with the root directory as its
     context, and pushes the result. By default, the context is the whole clone. The container adds `--cache=true` when
     **Use build cache** is on. It mounts the registry credential. It writes the digest of what it pushed to its
     termination message.

   The Job retries a failed pod up to two times. Geass deletes a finished Job and its pods one hour after the Job ends.
4. **Read the digest.** When the Job succeeds, Geass reads the digest of the image from the termination message. Geass
   reads it from the `kaniko` container of a pod of that Job that finished successfully. An older Geass made Jobs that
   did not ask Kaniko for the digest. For such a Job, Geass reads the `Pushed <repository>@sha256:…` line of the Kaniko
   output instead. Geass reads the digest for every run. Thus a build that runs again records the digest of the new
   image. If there is no digest, Geass marks the build as failed: "Kaniko completed without an immutable image digest".
5. **Run it.** Geass sets the `status.resolvedImage` of the service to the repository that the build pushed to, without
   its tag, at that digest: `<repository>@sha256:…`. A node can pull this image. The pods roll to it.

The state of the build is one of these:

| State | Meaning |
| --- | --- |
| `Pending` | Created, or put back to the start to run again. |
| `WaitingForCI` | Waits for GitHub checks. |
| `Running` | The Job is running. |
| `Succeeded` | The image is pushed and has a digest. |
| `Failed` | A GitHub call failed, a check failed, or the Job failed. |
| `Cancelled` | A person cancelled it with `POST /api/apps/{id}/build/cancel`, or set `status.cancelRequested`. |

To see what a service runs, use this command:

```bash title="The image of a repository service"
kubectl -n geass-system get geassapp amber-fern -o jsonpath='{.status.resolvedImage}{"\n"}'
# ghcr.io/acme/storefront@sha256:3b9f…
```

After some builds, the operator deletes the oldest finished builds of a service, after the newest 10. It never deletes
the build that the service runs.

### Build logs

To see the log of a build, do these steps:

1. Open the service, then **Deployments**.
2. Select a deployment. Click **View logs**, or click its row.
3. Open **Build Logs**.

The tab shows the output of the pod of the build. This output is from the `kaniko` container. If the clone failed before
Kaniko started, the output is from the `git` container. Geass deletes a finished Job and its pods one hour after the Job
ends. When the pod is gone, or has no output yet, the tab shows the failure reason that Geass recorded, for example
`kaniko Job failed`. When there is no failure reason, the tab shows the name and the phase of the build.

A deployment shows the current build of the service as it was when its deploy started. Thus a deploy that started
before the operator made the first build of the service shows no build.

```bash title="Read a build from the terminal"
kubectl -n geass-system get geassbuilds -l geass.dev/app=amber-fern
kubectl -n geass-system logs job/amber-fern-build-3f9a1c2b7d4e-kaniko -c git
kubectl -n geass-system logs job/amber-fern-build-3f9a1c2b7d4e-kaniko -c kaniko
```

To keep the log of every build, set `spec.logs.archiveStoreRef` on the service to a [bucket](/docs/guides/object-storage/)
of the same project and environment. When a build finishes, Geass uploads its log to `builds/<build>.log` in that bucket.
The dashboard has no field for this setting, and it does not read the archive back.

## Branch deploys and push triggers

A service builds one branch. The setting is **Branch** under **Settings** → **Source**. A push to that branch builds
and deploys the service again.

A push takes this path:

1. GitHub sends `push` to `<dashboard URL>/webhooks/github`. Geass checks the signature.
2. Geass finds each service that matches all of these conditions:
   - The service is deployed.
   - Its repository matches.
   - Its branch is the pushed `refs/heads/<branch>`.
   - It does not pin a commit.
   - Its [watch paths](#root-directory-and-watch-paths) select a file that the push changed.
   - It belongs to the project that is linked to the same GitHub installation.

   For each such service, Geass finds the build for the current settings of the service. That build is the one named in
   the `status.currentBuild` of the service, among the builds with the label `geass.dev/app=<service ID>`.
3. Geass deletes the Job of that build and puts the build back to `Pending`. It clears the digest, the commit, the check
   results, and the log reference of the build. The operator then resolves the new head of the branch, builds it, and
   records the digest of that run.
4. When the build succeeds, the service rolls to the new image. Until then, the previous pods continue to serve, and the
   service shows "Git source is waiting for a successful build".

A push that deletes the branch builds nothing. Geass skips a service that has no build yet. Geass makes its build when
the service is deployed. The answer to GitHub is `{"accepted":true,"triggered":N}`, where `N` is how many services Geass
rebuilt. Thus `0` means that no service matched, or that none of them watches a file that the push changed.

Every build that the operator makes carries the labels `geass.dev/app=<service ID>` and `geass.dev/name=<service ID>`.
These features find the builds of a service by `geass.dev/app`: the push, `POST /api/apps/{id}/build`, **Build Logs**,
`geass status`, and the clean-up of old builds. Some builds were made by the operator before it added that label. These
builds get the label when the operator starts.

To deploy a second branch, add a different environment, for example `staging`. Then create a second service there for
the same repository, with the other branch. See [Projects and environments](/docs/guides/projects-and-environments/).
For one environment for each pull request, see [Pull request environments](/docs/guides/pull-request-environments/).

## Build a new commit

These events start a build:

- **The first deploy.** **Deploy changes** on a draft creates the build. The build resolves the head commit of the
  branch at that moment.
- **A push to the branch.** See [Branch deploys and push triggers](#branch-deploys-and-push-triggers).
- **A change to a build setting.** The name of the build is a hash of its settings. Thus a change to one of the
  settings in this list starts a new build of the current head of the branch, after you click **Deploy changes**:
  - The repository.
  - The branch.
  - The Dockerfile path.
  - The root directory.
  - **Use build cache**.
  - **Wait for CI**.
  - The registry.

  If you change the setting back, Geass selects the earlier, finished build again. That build does not build a newer
  commit by itself. To build a newer commit, push, or rebuild.
- **A rebuild by hand.** `POST /api/apps/{id}/build` puts the current build of the service back to the start. Thus it
  builds the head of the branch as it is now. A project token for the environment of the service can call this
  endpoint. A CI pipeline starts a build in this way. The dashboard has no button for it. The endpoint makes no build
  of its own. For a service that has no build yet, it answers that the service has not been built.

  ```bash title="Build the branch again"
  curl --fail -X POST https://geass.example.com/api/apps/amber-fern/build \
    -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json"
  ```

  `POST /api/apps/{id}/build/cancel` cancels the current build while it waits or runs. You cannot cancel a build that
  has finished. The endpoint answers that there is nothing to cancel.
- **A pinned commit.** Set `spec.source.git.commit` on the service to the full commit SHA that you want. The dashboard
  has no field for it. Geass builds a pinned commit one time, and a push does not rebuild it.

  ```bash title="Build one commit"
  kubectl -n geass-system patch geassapp amber-fern --type merge \
    -p '{"spec":{"source":{"git":{"commit":"0123456789abcdef0123456789abcdef01234567"}}}}'
  ```

  Do this while the service shows **Up to date**, with nothing pending. A service with pending changes runs from its
  saved copy, and it ignores the patch. Then click **Redeploy** on the service, so that the saved copy matches. If you
  do not, a later edit in the dashboard puts the old commit back.

These events do not start a build:

- **Redeploy**. It restarts the pods and runs the same image.
- `POST /api/token/deploy`. It deploys what is pending. It does not build the new commit of a repository service.

A build that failed before this release with "Kaniko completed without an immutable image digest" stays failed. To run
it again, push, or call `POST /api/apps/{id}/build`.

### Wait for CI

With **Wait for CI** on, Geass reads the check runs of the commit from GitHub. Geass starts the build only when these
conditions are true:

- There is at least one check run.
- All check runs completed with the conclusion `success`.

Until then, the build state is `WaitingForCI`, and Geass checks again every 15 seconds. If a check fails, Geass marks
the build `Failed` with `GitHub check "<name>" concluded <result>`.

A commit with no checks at all never passes. Thus turn off the option for a repository that has no GitHub Actions or
other checks.

To list check runs, the app needs the **Checks: Read** permission. The GitHub App that Geass registers asks for it. See
[the permissions](#permissions). But an app from the earliest versions of Geass asked for no Checks permission. **Wait for CI**
can fail with `GitHub API returned 403 Forbidden: reading check runs needs the Checks: Read permission…`. In
that case, add **Checks: Read** to the app in GitHub, and accept the new permission in your installation. See
[Give an existing GitHub App the new permissions](#give-an-existing-github-app-the-new-permissions).

## Reporting back to GitHub

Geass tells GitHub what it did with your repository. Thus you see the result where you already look: on the commit, on
the **Deployments** of the repository, and on the pull request.

| What | How it shows on GitHub | Permission it needs |
| --- | --- | --- |
| A build | A commit status named `geass/build/<service name>` on the commit that Geass built. | **Commit statuses**: Read and write |
| A deploy | A GitHub Deployment of that commit, in an environment named like the Geass environment, with a status that follows the deploy. | **Deployments**: Read and write |
| A [pull request environment](/docs/guides/pull-request-environments/#what-geass-writes-on-the-pull-request) | One comment on the pull request, edited in place. | **Pull requests**: Read and write |

Geass reports only services built from a repository, because only these services have a commit to name. A service that
runs an image, and a function, report nothing. The reports are a side effect. Whatever GitHub answers, a build or a
deploy continues, and it does not fail because of the report.

### The commit status of a build

`geass/build/<service name>` follows the build:

| State on GitHub | When | Description |
| --- | --- | --- |
| `pending` | The build waits for CI, or it runs. | `Waiting for CI checks to pass before building`, then `Building the image` |
| `success` | Geass built and pushed the image. | `Built the image` |
| `failure` | The code did not build. The Kaniko Job failed, or a CI check failed with **Wait for CI** on. | `The build failed`, or the failure message of the check |
| `error` | The build stopped for a reason that is not the code: no registry is set, GitHub or the cluster did not answer, or the build was cancelled. | The failure reason of the build, or `The build was cancelled` |

When the dashboard address is set, the **Details** link of the status opens the **Deployments** page of the service in
the dashboard. Geass reports a commit when the build for that commit starts. Some builds fail before Geass can ask GitHub
which commit they build. These builds report nothing.

Sometimes one environment has many services that build the same repository, as a monorepo does. Each of these services
reports as `geass/build/<service name>`, with the name that the dashboard shows. With one status name for all of them,
the service that reported last hides the others. Thus a failed service can show as passed.

Every service has its own status name from the start. The name does not change when you add a different service. Require
that name under branch protection: `geass/build/web` for a service called `web`. When you rename the service in the
dashboard, Geass renames its status.

### The GitHub Deployment of a deploy

A deploy is a commit that rolls out. These are deploys:

- A push that Geass builds and rolls out.
- **Deploy changes** or a `geass deploy` of the commit that already runs, because it restarts the pods and applies new
  variables.

For each deploy, Geass makes a GitHub Deployment of the commit, in the environment named like the Geass environment
(`production`). Geass adds statuses to the GitHub Deployment:

| Status | When |
| --- | --- |
| `in_progress` | The new image rolls out, and its pods are not ready yet. |
| `success` | The pods are ready. The public address of the service is the **environment URL**. |
| `failure` | Geass stopped its attempts to deploy. The service is marked `ProvisionFailed`. |

The **log URL** of the status opens the **Deployments** page of the service. These cases report nothing:

- A service that restarts, scales, or loses a node after the deploy. Such a service does not deploy again.
- A service that already ran when you upgraded Geass. Geass does not announce it as freshly deployed.

Many services in one environment each make their own deployment there. Geass makes deployments without waiting for the
checks of the commit. Thus Geass reports a commit that has not passed CI yet.

A [pull request environment](/docs/guides/pull-request-environments/) uses an environment named `pr-<number>`, marked
transient. When Geass removes the pull request environment, it makes its deployments inactive.

### Permissions

The GitHub App that Geass registers asks for these permissions:

| Permission | Level | What Geass does with it |
| --- | --- | --- |
| Metadata | Read | GitHub requires it of every app. |
| Contents | Read | Clones repositories to build them. |
| Checks | Read | **Wait for CI** reads the check runs of a commit. |
| Pull requests | Read and write | Pull request environments read pull requests, and Geass comments on them. GitHub has no permission narrower than this one for a comment on a pull request. |
| Commit statuses | Read and write | The `geass/build/<service name>` status. |
| Deployments | Read and write | The GitHub Deployments. |

The app subscribes to the `push` and `pull_request` events. Contents stays read-only. A push of code or a merge of a pull
request needs Contents write. Thus none of these permissions let Geass change your code.

### What a missing permission does

An app that was registered before Geass reported to GitHub does not have the three write permissions. GitHub does not
give an installation a permission that its owner has not accepted. When GitHub refuses a report with `401`, `403`, or
`404` for the repository, these things happen:

- The build or deploy continues, exactly as if there were nobody to tell.
- Geass logs the refusal one time, not on every reconcile. A line shows `GitHub refused a report, so reports of this kind wait until the app is given the permission`.
- The GitHub connection of the project gets a condition `GitHubReporting` with status `False` and reason
  `PermissionMissing`. Its message names the permission and says how to approve it. See [GeassGitHubConnection](/docs/reference/resources/geass-github-connection/#conditions).
- Geass stops reports of that kind to that connection for ten minutes. The next report after that finds out whether the
  permission was approved. The condition goes back to `True` when one report goes through.
- Geass does not lose the state that GitHub refused. Geass does not record that state as reported, and it examines the
  build or service again after the ten minutes. Thus, after the permission is approved, Geass sends the state that the
  build or service is in at that time.

Some failures might pass, for example when GitHub does not answer, or answers with a `5xx` or a rate limit. Geass tries
such a report up to three times in total, at least ten seconds apart. If all three attempts fail, Geass keeps the report
as owed, and tries again after ten minutes. Thus a GitHub outage does not leave a deployment that shows `in progress`
permanently.

Geass does not retry a request that GitHub rejects as it is, for example a commit that GitHub does not know. A `404` for
one GitHub Deployment means that the deployment was deleted. Geass rejects it in the same way, and it does not hold back
other reports.

Geass sends reports in the background, not as part of the build or the deploy. Thus a slow GitHub never holds up the
work of other services.

### Give an existing GitHub App the new permissions

A change to the permissions of an app in GitHub does not change what its installations can do until the owner accepts the
change. To give an app that an earlier Geass registered the permissions that it needs, do these steps:

1. In Geass, open **Settings** → **Connectors** → **GitHub**, and click **Test GitHub App**. When the webhook is correct
   but permissions are missing, the message lists them and links to the permissions page of the app.
2. In GitHub, open the **Permissions & events** page of the app, at
   `https://github.com/settings/apps/<app slug>/permissions`. For an app that an organization owns, use the same page
   under the settings of that organization.
3. Set **Commit statuses**, **Deployments**, and **Pull requests** to **Read and write**. If **Checks** has no access,
   set it to **Read-only**. Then save.
4. Accept the new permissions on the installation. GitHub sends an email to the owner of the installation. As an
   alternative, open **Settings** → **Applications** → **Installed GitHub Apps**, click **Configure** for the app, and
   accept the new permissions.
5. Click **Test GitHub App** again. The message says that the app has the permissions that Geass needs. The
   `GitHubReporting` condition of the connection returns to `True` the next time a report goes through, in about ten
   minutes.

An app that Geass registers from now on asks for all of these permissions when Geass creates it. Thus you have nothing
to do for such an app.

### Turn it off

Reporting is on for every project by default. To turn it off for one project, open **Settings** → **GitHub** of the
project, and turn off **Report to GitHub**. This stops all three reports for the project: no commit statuses, no
deployments, and no pull request comment. Builds, deploys, and pull request environments do not change. A read-only user
does not see the setting.

The setting is `spec.githubReporting` on the [GeassProject](/docs/reference/resources/geass-project/#github-reporting).
When it is unset, reporting is on. To turn it off through the API, with a workspace admin token, leave out `enabled`:

```bash title="Turn reporting to GitHub off"
curl -X POST https://geass.example.com/api/projects/proj-1a2b3c4d/github-reporting/save \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json"
```

To turn it on again, add `-d enabled=on`.

## Webhooks

### From GitHub to Geass

GitHub calls `<dashboard URL>/webhooks/github`. The URL is public. Geass checks the `X-Hub-Signature-256` signature
against the webhook secret of the app. Geass handles three event types:

| Event | What Geass does | Answer |
| --- | --- | --- |
| `ping` | Nothing. | `{"accepted":true,"event":"ping","delivery":"…"}` |
| `push` | Runs again the current build of each service that it matches, as [Branch deploys and push triggers](#branch-deploys-and-push-triggers) tells you. | `{"accepted":true,"triggered":N,"delivery":"…"}` |
| `pull_request` | Creates or removes [pull request environments](/docs/guides/pull-request-environments/). | `{"accepted":true,"environments":[…],"delivery":"…"}` |
| All other events | Ignores them. | `{"accepted":true,"ignored":true,"delivery":"…"}` |

A bad signature gets `401`. A `503` means that the webhook secret is not configured. In GitHub, **Advanced** →
**Recent Deliveries** of the app shows each delivery and the answer of Geass. The **Test GitHub App** button in
**Settings** → **Connectors** checks two things:

- The webhook URL of the app matches the dashboard address.
- The app has the [permissions](#permissions) that Geass needs.

**The delivery ID.** GitHub numbers every delivery, and it sends the number in the `X-GitHub-Delivery` header. Geass
puts this number in the answer as `delivery`. Geass also puts it in every log line that the webhook handler writes. Thus
you can match a delivery listed under **Recent Deliveries** to what the dashboard logged for it:

```bash title="Find what the dashboard did with a delivery"
kubectl -n geass-system logs deployment/geass-controller-manager -c manager | grep 72d3162e-cc78-11e3-81ab-4c9367dc0958
```

Geass leaves out the field when GitHub sent no ID, or an ID with characters that an ID does not have. Geass does not use
the ID to skip anything. A delivery that you **Redeliver** from the settings of the app keeps its ID. Geass handles it
as the new event that it is. Thus a redelivered push runs the builds again.

**Through a provider.** The GitHub App of the cluster can be an app that a provider runs. See
[GitHub through a provider](/docs/guides/connectors/github/#github-through-a-provider). In this case, GitHub calls the
provider. The provider forwards each event to `<dashboard URL>/webhooks/github/forwarded/<connection>`, signed with a
secret that only the provider and the cluster know. Geass handles the event in the same way, and only for the projects
connected to the installation that the event names. For the signature and the answers, see
[Forwarded GitHub events](/docs/reference/rest-api/#forwarded-github-events).

### From your CI to Geass

If your CI can send a header, use an API token:

1. Create a **project token** under **Settings** → **Tokens** of the project, limited to one environment.
2. Store the token as a secret in your CI.
3. Call the API.

See [Users and API tokens](/docs/guides/users-and-tokens/).

Some tools can only call a URL, for example a registry that sends a notification when an image is pushed, a scheduler,
or a CMS. For these tools, use a **deploy hook**. A deploy hook is a URL for one service that deploys the service when
called with `POST`. Make it in **Settings** → **Deploy** of the service. For a service built from a repository, the deploy
hook builds the branch again, as a push does. For any other service, it deploys the pending changes. It answers one time
every 30 seconds. See [Deploy hooks](/docs/guides/users-and-tokens/#deploy-hooks).

For a service that runs an image, set the new image and deploy in one call. This is the usual CI step:

```bash title="Deploy a new image from CI"
curl --fail -X POST https://geass.example.com/api/apps/amber-fern/update \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d image=ghcr.io/acme/storefront:$GIT_SHA -d deploy=on
```

To deploy all that is pending in the environment of the token, use the deploy endpoint. For example, use it after you
set variables:

```bash title="Deploy pending changes"
curl --fail -X POST https://geass.example.com/api/token/deploy \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json"
```

`/api/token/deploy` does not build the new commit of a repository service. See [Build a new commit](#build-a-new-commit).
A project token can also scale, change variables and secrets, retry a failed provision, and read logs. But it can do
these actions only for the services of its own environment.

The `geass` command line does the same in a pipeline:

- `geass api PATCH /apps/amber-fern -d '{"image": "…"}'` saves the new image.
- `geass deploy amber-fern --wait` deploys the image. If the service does not come up, the command fails the job with
  exit code `5` or `6`.

See [Command line](/docs/guides/command-line/#scripts-and-ci). For a repository service, `--wait` also follows the build.
If the build fails, the command fails with the reason of the build.

### Build from GitHub Actions

A push already rebuilds a repository service. Use `geass build` when your pipeline must start the build instead. For
example:

- On a schedule.
- From a manual run.
- For a repository whose webhook cannot reach the dashboard.
- When a job must wait until the new version is live before the job continues.

`geass build` runs the build again from the head of the branch of the service, or from the commit that the service pins.
It does this exactly as a push does. With `--wait`, it follows the build through to the rollout.

This workflow rebuilds `web` every night, so that the image gets updates to its base image. The workflow fails the run
when the build or the rollout fails:

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

`GEASS_TOKEN` is a **project token** for the environment that `web` is in, kept as a secret of the repository. A project
token can restart the builds of the services in its environment, and nothing else. See
[Users and API tokens](/docs/guides/users-and-tokens/).

The step prints `web: build started`, and then each change of state (`build running`, `ready at https://…`). It exits
with these codes:

- `0` when the service is ready.
- `5` when the build fails or the rollout fails. The step says why, for example `kaniko Job failed`, or a CI check that
  failed with **Wait for CI** on.
- `6` when `--timeout` runs out.

Know these facts about `geass build`:

- It builds what the branch holds when the call arrives. This is not necessarily the commit that started your workflow.
  To build a particular commit, pin it. See [Build a new commit](#build-a-new-commit).
- It does not apply changes that you saved to the service but did not deploy. For these changes, use `geass deploy`.
- If the push webhook also works, a workflow that builds on every push starts the build two times. The second start runs
  the build again from the beginning. Build from a schedule or a manual run, or let the webhook start the build.
- `geass build` with no service names, and with `--project` and `--environment`, builds every deployed repository service
  in that environment.
- The same call is `POST /api/v1/apps/{name}/build`. The MCP server offers it as `rebuild_app`.

### Outgoing webhooks

Geass sends a signed `POST` to the URLs that you add when a service deploy starts, succeeds, or fails. Use these webhooks
for chat messages, a pipeline trigger, or an audit log.

1. Open **Settings** → **Webhooks** of the project, and click **New webhook**.
2. Enter the **Payload URL**. It must be `http://` or `https://`, with no username or password.
3. Select the **Events**: **Deployment started**, **Deployment succeeded**, or **Deployment failed**. To receive every
   event, clear all of them.
4. Click **Create webhook**. A dialog shows the **Signing secret** one time. Copy it immediately.

The menu of a webhook has **Send test**, **Edit**, **Pause** or **Enable**, **Rotate signing secret**, and **Delete**.
**Recent deliveries** lists the last 20 attempts: time, event, webhook, HTTP result, attempts, and duration. A project can
have at most 20 webhooks.

**When events are sent.** Geass sends these events:

- `deployment.started` when a deploy of a service starts from the dashboard, a token, or the API.
- `deployment.succeeded` when the pods of that deploy are ready.
- `deployment.failed` when provisioning stops its attempts.

Geass sends each event one time for each deploy. Deploys of databases and buckets send nothing, and a build by itself
sends nothing. **Send test** sends an event of type `test`.

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

`service.kind` is `service` or `function`. `deployment.commit` appears only for a repository service that pins a commit
in `spec.source.git.commit`. `message` carries a reason for a failure.

The request has these headers:

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

**Retries and limits.** A delivery times out after 10 seconds. Geass retries in these cases:

- Geass cannot connect.
- The receiver answers `5xx`, `429`, or `408`.

Geass makes up to three attempts in total. It waits 1 second, and then 5 seconds. Any other answer of 300 or more is
final. Geass does not follow redirects. Up to eight deliveries run at the same time.

**Allowed addresses.** A webhook cannot point at these addresses:

- `localhost`.
- A loopback address.
- A link-local address, including the cloud metadata address.
- A multicast address.

A webhook also cannot point at a private network address (RFC 1918 or 100.64.0.0/10). The exception is when the
operator sets `GEASS_WEBHOOKS_ALLOW_PRIVATE_NETWORKS=true` in its environment. This setting lets a receiver inside the
cluster work. See [Configuration](/docs/reference/configuration/). Geass checks the address again when it connects. Thus
Geass refuses a name that resolves to a blocked address.

Through the API, with a workspace admin token, use these calls. See the [REST API](/docs/reference/rest-api/).

- `POST /api/projects/{project}/webhooks/save`, with the fields `url`, repeated `event`, `disabled`, and `id` to update.
  The create call answers with `id` and `secret`.
- `…/delete`, `…/rotate`, and `…/test`, each with `id`.

## What happens under the hood

| Object | Namespace | What it is |
| --- | --- | --- |
| `GeassGitHubConnection` `<project ID>-github` | `geass-system` | The link of the project to GitHub. |
| Secret `<project ID>-github-token` | `geass-system` | The installation token, the installation ID, and the account. |
| `GeassBuild` `<service ID>-build-<hash>` | `geass-system` | One build. |
| Job `<build>-kaniko` and its pod | `geass-system` | The clone and Kaniko steps. |
| Secret `platform-registry` | `geass-system` | The login of the platform registry, when it was set through the API. |
| Secret `<service ID>-registry-pull` | The environment namespace | A copy of the registry login, that the nodes use to pull the built image. |
| `GeassPlatformConfig` `platform` | `geass-system` | `spec.registry` (repository and login) and the GitHub App reference. |

Geass records on the objects what it told GitHub. Thus Geass sends a state one time, and not on every reconcile:

- The build carries the annotation `geass.dev/github-status`, `<commit>:<build phase>`, for the commit status that it
  last sent. Geass does not touch a build without this annotation that does not change state. Thus Geass does not report
  the builds that exist when you upgrade Geass. The value `owed` marks a build whose first report did not get through.
- The service carries `geass.dev/github-deployment`, `<commit>:<GitHub deployment ID>:<state>:<deploy revision>`. An ID
  of `0` is a deploy that Geass saw and did not send. Geass records in this way a service that already ran at the
  upgrade.
- The `status.conditions` of the connection has `GitHubReporting`. This condition says whether GitHub accepts the reports.

```bash title="Inspect repository services"
kubectl -n geass-system get geassbuilds -o custom-columns=NAME:.metadata.name,APP:.spec.app,PHASE:.status.phase,IMAGE:.status.image,DIGEST:.status.imageDigest
kubectl -n geass-system get geassbuild amber-fern-build-3f9a1c2b7d4e -o yaml
kubectl -n geass-system get geassapp amber-fern -o jsonpath='{.status.resolvedImage}{"\n"}'
kubectl -n proj-1a2b3c4d-production get deploy amber-fern -o jsonpath='{.spec.template.spec.containers[0].image}{"\n"}'
kubectl -n geass-system get geassapp amber-fern -o jsonpath='{.metadata.annotations.geass\.dev/github-deployment}{"\n"}'
kubectl -n geass-system get geassgithubconnection proj-1a2b3c4d-github -o jsonpath='{.status.conditions[?(@.type=="GitHubReporting")]}{"\n"}'
```

## Troubleshooting

**"GitHub is not connected to this cluster yet" or "Connect GitHub in platform settings first".** No GitHub App is
registered. An admin registers it under **Settings** → **Connectors** → **GitHub**. See
[Connect GitHub](/docs/guides/connectors/github/).

**"Link this project to GitHub first".** The project has no installation. Use **Settings** → **GitHub** →
**Connect to GitHub**.

**A repository is missing from the list.** The installation cannot see the repository. Change the repository access of
the app in GitHub, and then open the list again.

**The service says `no container registry is set for builds`.** Neither the service nor the platform names a registry.
See [Set the registry](#set-the-registry). After you set a registry, the service builds in about 30 seconds.

**The build fails and Build Logs mention `no such host`, `UNAUTHORIZED` or `denied`.** The registry is incorrect, or the
credentials are incorrect. See [Set the registry](#set-the-registry). After you fix the problem, a service that waits for
a build starts a new build in about 30 seconds.

**The build fails with `GitHub API returned 404 Not Found`.** The repository name or the branch name is incorrect, or the
installation cannot read the repository. Check **Repository** and **Branch** in **Settings** → **Source**.

**The build fails with `GitHub API returned 403 Forbidden` and Wait for CI is on.** The app does not have the **Checks**
permission. See [Wait for CI](#wait-for-ci).

**No `geass/build/<service>` status or GitHub Deployment appears.** Check these items in sequence:

1. The service must be built from a repository. Geass does not report image services and functions.
2. **Report to GitHub** must be on for the project, under **Settings** → **GitHub**. The project must be linked to
   GitHub.
3. Examine the connection: `kubectl -n geass-system get geassgithubconnection <project ID>-github -o yaml`. A
   `GitHubReporting` condition that is `False` with the reason `PermissionMissing` means that GitHub refused. Its
   message names the permission. Follow [Give an existing GitHub App the new permissions](#give-an-existing-github-app-the-new-permissions).
   **Test GitHub App** lists the permissions that the app does not have.
4. Geass reports a build when the build moves to a new state. A build made before you upgraded Geass, and a service that
   already ran at that time, report from their next build or deploy.
5. Read the operator log for `GitHub refused a report` or `Could not report to GitHub`.

**A required check named `geass/build` is never reported.** Every service reports as `geass/build/<service name>`.
Require that name under branch protection. See [The commit status of a build](#the-commit-status-of-a-build).

**The connection says `PermissionMissing` after I approved the permissions.** Geass tries again ten minutes after the
last refusal. The condition changes when a report goes through. If the condition stays, make sure that the installation
accepted the change (**Settings** → **Applications** → **Installed GitHub Apps**), and use **Test GitHub App**.

**GitHub shows a delivery that Geass answered `200` and nothing happened.** Match the delivery to the log with its
delivery ID. The delivery ID is in the answer as `delivery`. See [Webhooks](#from-github-to-geass).

**The build stays in `WaitingForCI`.** The commit has no checks yet, or the checks still run. If the repository has no
checks, turn off **Wait for CI**.

**"Kaniko completed without an immutable image digest".** The Job succeeded, but Geass found no digest. The termination
message of the pod was empty, and the Kaniko output has no `Pushed <repository>@sha256:…` line. Read the end of
`kubectl -n geass-system logs job/<build>-kaniko -c kaniko`. The build can be from before this release. In that case, the
log scan could not find the digest. To run the build again, push, or call `POST /api/apps/{id}/build`. This message does
not mean that the push failed.

**The build succeeded but the pods are in `ImagePullBackOff`.** The condition of the service shows `The service cannot start: app is ImagePullBackOff`.
After some minutes, Geass marks it `ProvisionFailed`. Do these steps:

1. Print the image that the Deployment uses, with the last command under
   [What happens under the hood](#what-happens-under-the-hood). The image is `<repository>@sha256:…`.
2. If the registry needs a login, the service must have a credential. Geass copies the credential to the Secret
   `<service ID>-registry-pull`. Make sure that this Secret exists in the environment namespace, and that the credential
   can pull from that repository.
3. If the registry needs no login, make sure that your nodes can reach it.

**A push does not trigger a build.** In **Recent Deliveries** of the app in GitHub, the answer tells you the cause:

- `401` means that the webhook secret in Geass does not match the secret in GitHub.
- `503` means that Geass stores no webhook secret.
- `200` with `"triggered":0` means that the push reached Geass, and Geass rebuilt no service.

For each service, check these items:

- The service is deployed, and it is not a draft.
- **Repository** and **Branch** match the push.
- The service does not pin a commit.
- Its [watch paths](#root-directory-and-watch-paths) select a file that the push changed.
- It has a build already. A service that was never deployed has no build.
- Its project is linked to the GitHub installation that the event came from.

A push that deletes a branch builds nothing.

**The service shows "Git source is waiting for a successful build" for a long time.** Check the build with
`kubectl -n geass-system get geassbuilds`, and read the logs of the Job. See [Build logs](#build-logs). A build that
failed stays failed. Fix the cause. Then do one of these actions:

- Change a build setting and click **Deploy changes**.
- Pin a commit.
- Reset the build.

See [Build a new commit](#build-a-new-commit).

**The build cannot find the Dockerfile.** The Dockerfile path is relative to the root directory, and Geass ignores a
leading slash. With the root directory `apps/api`, write `Dockerfile` or `docker/prod.Dockerfile`. Geass looks for both
under `apps/api`. See [Root directory and Watch paths](#root-directory-and-watch-paths).

**A webhook delivery fails with "webhook URL cannot point at a private network address".** The receiver is on a private
address. Use a public URL, or ask the operator to set `GEASS_WEBHOOKS_ALLOW_PRIVATE_NETWORKS=true`.

**A webhook shows `receiver answered HTTP 4xx`.** Your receiver rejected the request. Open **Recent deliveries**, fix the
receiver, and use **Send test** to try again. Geass does not retry a `4xx`.

## Related

- [Apps](/docs/guides/apps/)
- [Pull request environments](/docs/guides/pull-request-environments/)
- [Connect GitHub](/docs/guides/connectors/github/): register the GitHub App.
- [Users and API tokens](/docs/guides/users-and-tokens/)
- [Domains and TLS](/docs/guides/domains-and-tls/)
- [REST API](/docs/reference/rest-api/)
