---
title: "GeassBuild"
description: "Reference for the GeassBuild resource: one build that turns a Git service's commit into an image with a Kaniko Job, with its phases and logs."
sidebar:
  order: 13
---

A `GeassBuild` is one build of a Git service. When a [GeassApp](/docs/reference/resources/geass-app/) has a repository as its source, the operator creates a `GeassBuild` for it. The build does these steps:

1. It resolves the branch to a commit.
2. Optionally, it waits for GitHub checks.
3. It runs a Kaniko Job that clones the repository and builds the Dockerfile.
4. It records the digest of the resulting image.

The service then deploys that digest.

You do not create or edit these objects in normal use. The operator creates and updates them, and the dashboard shows their state in the build section of a deploy's logs. Every build lives in `geass-system`.

## Summary

| Property | Value |
| --- | --- |
| API version | `geass.geass.dev/v1alpha1` |
| Kind | `GeassBuild` |
| Plural | `geassbuilds` |
| Scope | Namespaced |
| Namespace | `geass-system` |
| Subresources | `status` |
| Printer columns | None |

## Example

This example is the build that the operator creates for a Git service with CI gating on. The name is `<app>-build-` plus the first 12 characters of a hash of the spec. So the same settings always give the same name.

```yaml title="storefront-build-3f9a1c0b7d2e.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassBuild
metadata:
  name: storefront-build-3f9a1c0b7d2e
  namespace: geass-system
  labels:
    geass.dev/kind: GeassBuild
    geass.dev/app: storefront
    geass.dev/name: storefront
    geass.dev/managed-by: geass
    geass.dev/system-namespace: geass-system
    geass.dev/project: proj-1a2b3c4d
    geass.dev/environment: production
spec:
  app: storefront
  project: proj-1a2b3c4d
  environment: production
  repository: acme/storefront
  branch: main
  dockerfile: Dockerfile
  context: "."
  waitForCI: true
  connectionRef:
    name: proj-1a2b3c4d-github
  gitCredentialRef:
    name: proj-1a2b3c4d-github-token
  registry: registry.example.com/acme/storefront:main
  cache: true
```

## Spec fields

The operator copies these fields from the service. When you change a field of the service, the hash changes. So the next reconcile creates a new build with a new name, and does not change the old build.

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.app` | string | none | Required | The `GeassApp` name. |
| `spec.project` | string | none | Required | The project ID. The build runs on nodes in the project's region, and on nodes that carry every tag that the project requires. |
| `spec.environment` | string | none | Required | The environment of the service. |
| `spec.repository` | string | none | none | The repository, `owner/name`. Geass accepts a leading `https://github.com/` and a trailing `.git`. Geass supports only github.com. |
| `spec.revision` | string | none | none | A commit to build, copied from `source.git.commit`. Empty means the head of the branch. |
| `spec.branch` | string | none | none | The branch to clone and to resolve to a commit. |
| `spec.dockerfile` | string | `Dockerfile` when empty | none | The Dockerfile path, relative to `spec.context`. Geass reads a path that already starts with the context as relative to the repository root. |
| `spec.context` | string | none | none | The root directory of the build, copied from `source.git.context`. Kaniko builds in `/workspace/<context>`. A leading slash means the repository root. A path that leaves the repository makes the build fail. |
| `spec.waitForCI` | boolean | `false` | none | Wait until every GitHub check run of the commit has finished successfully, and then build. |
| `spec.connectionRef` | LocalObjectReference | none | none | The [GeassGitHubConnection](/docs/reference/resources/geass-github-connection/) of the project. The build needs it to resolve a branch and to read check runs. |
| `spec.gitCredentialRef` | LocalObjectReference | the connection's Secret | none | The Secret whose `token` key the build uses to clone. The operator sets it to the connection's `secretRef`. |
| `spec.registry` | string | none | none | The full image reference that Kaniko pushes to (`--destination`), used exactly as written. Required. Geass has no registry of its own. So a build without a registry fails with `no container registry is set for builds`, and the operator makes no build for a service that has no registry. |
| `spec.credentialRef` | LocalObjectReference | none | none | A Secret with the key `.dockerconfigjson`, mounted for Kaniko as `/kaniko/.docker/config.json`. Under the platform's `spec.tenancy.registry`, a build of an organization's project fails if its Secret is the platform's login or another organization's login. A Secret is the platform's login by name, or when it holds the same login. |
| `spec.cache` | boolean | `false` | none | Pass `--cache=true` to Kaniko. |
| `spec.logStoreRef` | LocalObjectReference | none | none | A [GeassObjectStore](/docs/reference/resources/geass-object-store/) that receives the build log. |

`spec.registry` is a destination, not a repository prefix. Geass adds no app name and no tag to it. If you want a tag, write it. Every build overwrites a fixed tag. The deploy does not depend on the tag, because the service runs the repository at the digest that the build reports (`<repository>@sha256:…`). Before you build, set `build.registry.repository` on the service, or `registry.repository` on the [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/). Geass also copies the credential that the build pushes with (`spec.credentialRef`) into the service's namespace. The nodes use it to pull the image.

## Status

| Field | Type | Meaning |
| --- | --- | --- |
| `status.phase` | string | `Pending`, `WaitingForCI`, `Running`, `Succeeded`, `Failed`, or `Cancelled`. See the phases in the next table. |
| `status.jobRef` | string | The Job that runs the build, `<build>-kaniko`. |
| `status.jobNamespace` | string | The namespace that the Job runs in when it is not the build's own namespace: the [organization's build namespace](/docs/guides/organizations-and-workspaces/#builds-and-the-registry), for a project that belongs to an organization. Empty means the build's own namespace. |
| `status.image` | string | The image that the build pushes to: `spec.registry`. |
| `status.imageDigest` | string | The `sha256:` digest of the image that the last run pushed. Kaniko writes it to the termination message of the `kaniko` container (`--digest-file=/dev/termination-log`). Geass reads it from the pod of the Job that succeeded. For a Job made before that change, the fallback is the `Pushed <repository>@sha256:…` line of the Kaniko log. Geass reads the digest again for every run. A run that starts over clears it. The service runs `<repository of status.image>@<this digest>`. |
| `status.sourceRevision` | string | The commit that was built. Geass resolves it when a run starts, from `spec.revision` or from the head of `spec.branch`, and keeps it for that run. A run that starts over clears it, so the next run resolves the head again. |
| `status.ciState` | string | `pending`, `success`, or `failure`, while the build waits for CI. |
| `status.ciChecks` | list of strings | The names of the check runs that Geass found. Geass keeps at most 50. |
| `status.logRef` | string | Where the log is: `pod-log://<job>/kaniko`, or `object://<store>/builds/<build>.log` after Geass archives it. |
| `status.failureReason` | string | Why the build failed. |
| `status.retryCount` | integer | The number of failed pods of the Job. |
| `status.startedAt` | time | When the Job was created. |
| `status.completedAt` | time | When the build succeeded. A build that failed has none. |
| `status.cancelRequested` | boolean | Set to stop the build. |
| `status.lastLogLine` | string | If set, the dashboard shows it in the build log. Nothing sets it. |
| `status.conditions` | list | `Sandboxed`, only on a build of an organization's project while the platform's `spec.tenancy.sandbox` is set. `True` `RuntimeClass` when its pod runs under the sandboxed runtime. `False` `ReservedNodes` when it runs without the sandboxed runtime on nodes reserved for the organization. `False` `RuntimeClassMissing` while the build waits for the RuntimeClass, in `Pending`, with a `Warning` event `SandboxRuntimeClassMissing`. See [Sandboxed runtime for organizations](/docs/operations/sandboxed-runtime/). `RegistryIsolated`, only on a build of an organization's project while the platform's `spec.tenancy.registry` is set. `True` `Isolated` when its login and the repository that it pushes to are the organization's. `False` when Geass refused the build, with a `Warning` event `RegistryRefused`. The reason is `PlatformCredential` (its login is the platform's, or another organization's) or `ForeignRepository` (it would push into a part of a registry that is the platform's or another organization's). See [Registry isolation for organizations](/docs/operations/registry-isolation/). Nothing else writes conditions for this kind. |

### Phases

| Phase | Meaning | What happens next |
| --- | --- | --- |
| `Pending` | A push, a rebuild, or a reset put the build back to the start. A build that the operator has created but not yet reconciled has an empty phase. `Pending` is also the phase of a build of an organization's project that waits for the platform's sandboxed runtime. Its `Sandboxed` condition says so. | The next reconcile resolves the commit. The operator examines a build that waits for the runtime again every 30 seconds, and makes its Job when the RuntimeClass exists. |
| `WaitingForCI` | The build waits for check runs of the commit, or for GitHub to answer. | The operator checks again every 15 seconds. A commit with no check runs waits forever. |
| `Running` | The Kaniko Job exists. | The operator checks every 5 seconds. |
| `Succeeded` | The Job finished, and Geass found a digest. | The service deploys the digest. The operator does not examine the build again, unless it has a log store and its log is not archived yet. |
| `Failed` | The Job failed (`kaniko Job failed`). Or GitHub refused a request, or a check run did not succeed (`GitHub check "x" concluded failure`). Or Geass found no digest (`Kaniko completed without an immutable image digest`). Or the build cannot run as written: no registry, or a path that leaves the repository. `Failed` is also the phase of a build of an organization's project that the platform's `spec.tenancy.registry` refused (`Registry isolation: …`). The refusal comes before its Job was made. For a build that was running with the platform's login when the setting was turned on, the refusal comes after its Job was deleted. | The operator retries a GitHub error or a failed check every 30 seconds, until the Job has been created. A build whose Job ran stays failed until a push, a rebuild, or a reset runs it again. The operator examines a refused build again on each of its events, and runs it if the cause of the refusal has changed. |
| `Cancelled` | `status.cancelRequested` was set. | The Job is deleted. |

The Job runs two containers in order. The init container `git` (image `alpine/git:latest`) clones the branch with the token in an `Authorization` header, and checks out the commit. The container `kaniko` (image `gcr.io/kaniko-project/executor:v1.23.2`) builds the Dockerfile and pushes. The Job allows two retries (`backoffLimit: 2`).

Geass removes a finished Job, its pods, and so their log one hour after the Job finishes (`ttlSecondsAfterFinished: 3600`). By then, the result is in the build's status. If the build has a `logStoreRef`, the operator uploads its log to the store before that. The operator does not run a finished build again because its Job is gone. To build again, use a push, a rebuild, or a reset, as shown under [Re-run a build](#re-run-a-build).

## How the dashboard uses builds

A Git service has no build screen of its own. When you deploy, the **Deployments** tab shows a **Build** section in the logs of that deploy. The section shows the output of the build's pod, or the failure reason when the pod is gone. A push to the service's branch re-runs the build for its current settings. The REST API has two actions for builds, which the dashboard does not use:

| Request | Effect |
| --- | --- |
| `POST /api/apps/<app>/build` | Finds the service's current build (see the next paragraph), deletes its Job, and puts the build back to `Pending`. It clears the build's digest, commit, check results, and log reference. The operator then builds the head of the branch as it is now. This request never creates a build. For a service that has no build, it answers `the app has not been built yet`. |
| `POST /api/apps/<app>/build/cancel` | Sets `status.cancelRequested` and the phase `Cancelled` on the current build, while it is `Pending`, `WaitingForCI`, or `Running`. For a build that has finished, it answers `the build has already finished, so there is nothing to cancel`. To cancel a build that succeeded would take its image away from the service. |

The current build is the one in the service's `status.currentBuild`, which the operator keeps up to date. A hash of the service's settings names a build. So a service that turns a setting on and then off again goes back to its first build, which is older than the second. The newest build is then one that the service does not use, and a push builds the one that it does use. A service that the operator has not reconciled yet has no record, and Geass uses the newest build.

Every build that the operator makes carries the labels `geass.dev/app=<app>`, `geass.dev/name=<app>`, `geass.dev/kind=GeassBuild`, `geass.dev/managed-by`, `geass.dev/system-namespace`, `geass.dev/project`, and `geass.dev/environment`. These parts of Geass use `geass.dev/app` to find a service's builds:

- The GitHub push webhook, which re-runs the current build of each service that the push matches.
- `POST /api/apps/<app>/build` and `/build/cancel`, which the command line calls as `geass build` and the MCP server as `rebuild_app`.
- The link between a deploy and its build. The dashboard attaches the current build to a deploy when the deploy starts, so **Build Logs** has something to show. `geass status`, `geass deploy --wait`, and the `app_status` tool report the current build the same way, and say why it failed.
- The operator's own cleanup. After each successful build, the operator deletes the oldest finished builds of the service beyond the newest 10. It never deletes the build that the service runs.

A build made before Geass put `geass.dev/app` on every build gets the label when the operator starts. So Geass finds those builds too.

## What the build tells GitHub

A build with a `repository` reports its progress to GitHub. The report is a commit status named `geass/build/<service>` on the commit in `status.sourceRevision`. The last part of that name is the service's name in the dashboard. The commit status is one of these values:

- `pending` while the build is `WaitingForCI` or `Running`.
- `success` when the build has `Succeeded` with a digest.
- `failure` when its Job failed, or a CI check failed (`status.ciState` is `failure`).
- `error` for any other failed or `Cancelled` build.

See [Reporting back to GitHub](/docs/guides/github-and-builds/#the-commit-status-of-a-build).

The annotation `geass.dev/github-status` records the last report as `<commit>:<phase>`, so Geass sends each phase of a commit one time. Geass reports a build when its phase changes, or when the build already carries the annotation. So Geass does not report the builds that exist when you upgrade Geass. The value `owed` means that the first report did not get through, and Geass tries it again. A project with **Report to GitHub** off sends nothing. A GitHub App without the **Commit statuses** permission sends nothing. In both cases, the build continues.

## Re-run a build

A push to the service's branch, `POST /api/apps/<app>/build`, or a manual reset runs a finished build again. All of them put the build back to the start and clear what the last run recorded. They also delete the Job of the last run. The build reads its result from a Job that still exists, so with the old Job the build would finish immediately with the old result. A build that has not started has no Job of its own. So the controller removes a Job that it finds for such a build. This makes a manual reset safe, even when you did not delete the Job first. The controller reads the digest of every run from that run's own pod. So a rebuild records the digest of the new image. The controller never takes a digest recorded before the run ended as the result of this run.

To do it by hand:

```bash title="Build the current head of the branch"
kubectl -n geass-system delete job storefront-build-3f9a1c0b7d2e-kaniko --ignore-not-found
kubectl -n geass-system patch geassbuild storefront-build-3f9a1c0b7d2e --subresource=status --type merge \
  -p '{"status":{"phase":"Pending","failureReason":"","sourceRevision":"","ciState":"","imageDigest":null,"logRef":"","startedAt":null,"completedAt":null}}'
```

To build the same commit again, leave `sourceRevision` out of the patch.

You do not have to delete the Job first, because the controller removes the last run's Job from a build that was put back to the start. But to delete it first saves a pass.

You can also delete the whole `GeassBuild`. The service recreates it on its next reconcile, with the same name and an empty status. Delete its Job first, and wait until the Job is gone. Then the new build does not read the old Job's result.

## Editing by hand

Do not create builds yourself. A build with a name that no service owns builds an image that nothing deploys. Edit `spec` only through the service. To cancel a running build, set `status.cancelRequested` to `true`.

## What happens under the hood

For each build, the operator creates the Job `<build>-kaniko` in `geass-system` with an owner reference to the build. So when you delete the build, Kubernetes deletes the Job and its pods. The Job's pods run on nodes in the project's region and on the nodes that the project requires.

The build also uses the GitHub API with the connection's token, which Geass refreshes through the GitHub App:

- It reads `GET /repos/<repository>/commits/<branch or sha>` to resolve the commit.
- It reads `GET /repos/<repository>/commits/<sha>/check-runs` when `waitForCI` is on.
- It writes `POST /repos/<repository>/statuses/<sha>` to report the build (see [What the build tells GitHub](#what-the-build-tells-github)). A project can turn off this report.

When a build finishes and `spec.logStoreRef` names a ready object store, the operator uploads the Kaniko log to `builds/<build>.log` in the store's bucket. The operator does not reconcile a finished build that has succeeded again. So a restart of the operator does not ask GitHub about every build that it ever made.

```bash title="Inspect builds"
kubectl -n geass-system get geassbuilds -l geass.dev/app=storefront
kubectl -n geass-system get geassbuild storefront-build-3f9a1c0b7d2e -o jsonpath='{.status.phase}{"  "}{.status.failureReason}{"\n"}'
kubectl -n geass-system get jobs,pods -l job-name=storefront-build-3f9a1c0b7d2e-kaniko
kubectl -n geass-system logs job/storefront-build-3f9a1c0b7d2e-kaniko -c git
kubectl -n geass-system logs job/storefront-build-3f9a1c0b7d2e-kaniko -c kaniko
```

Read the logs within the hour. After that, the Job and its pods are gone. Kaniko builds run as root in `geass-system`, with no security context, and a Dockerfile can run any command. Build only repositories that you trust. See [Security](/docs/operations/security/).

## Troubleshooting

**The service says `Git source is waiting for a successful build`.** Read the build's phase and `failureReason`. `Running` means the Job is still working: read its logs. `WaitingForCI` means that checks are not finished (see the next item). `Failed` has a reason.

**The build is `WaitingForCI` and never moves.** With `waitForCI`, the commit needs at least one check run. Every check run must be `completed` with the conclusion `success`. A commit with no check runs waits forever. Turn off **Wait for CI**, or add a workflow that runs on that branch.

**The build fails with `GitHub API returned 403 Forbidden: reading check runs needs the Checks: Read permission…` while waiting for CI.** To list check runs, the app needs the Checks permission. The GitHub App that Geass registers now asks for it, together with Contents, Metadata, Pull requests, Commit statuses, and Deployments. An app that an earlier version registered did not ask for it. Add **Checks: Read** to the app in GitHub, and accept the new permission on the installation.

**The build fails with `GitHub API returned 404 Not Found` or `401 Unauthorized`.** GitHub did not accept the connection's token for that repository. Make sure that the installation includes the repository, and that the repository name is correct. See [GeassGitHubConnection](/docs/reference/resources/geass-github-connection/).

**`GitHub connection reference is required to resolve a branch`.** The build has no `spec.connectionRef` and no `spec.revision`. Link the project to GitHub.

**`GitHub check "<name>" concluded failure`.** A CI check failed for the commit that this build recorded. Until the Job has been created, the operator checks again every 30 seconds. So if you re-run the check until it passes, the build continues. A new push puts the build back to the start and resolves the new head of the branch, so the build builds the newer commit.

**`kaniko Job failed`.** The build failed, or the push failed. Read the logs of the `kaniko` container within the hour that Geass keeps the Job. If the clone failed, read the logs of `git` too. Common causes are a Dockerfile error and a registry that refuses the push: a wrong `spec.registry`, or no `credentialRef` for a private registry.

**`Kaniko completed without an immutable image digest`.** The Job succeeded, but Geass found no digest. The termination message of the `kaniko` container was empty, and the Kaniko output has no `Pushed <repository>@sha256:…` line. Read the end of `kubectl logs job/<build>-kaniko -c kaniko`. Some builds failed this way before Geass read the termination message, because the log scan could not find a digest in Kaniko's output. Such a build did push its image. Run it again with a push or `POST /api/apps/<app>/build`.

**`no container registry is set for builds`.** Neither the service nor the platform names a registry. See [Set the registry](/docs/guides/github-and-builds/#set-the-registry).

**`kubectl logs` for a finished build says the Job or pod was not found.** Geass removes the Job an hour after it finishes. To keep the log of each build in an object store, set `spec.logs.archiveStoreRef` on the service.

**A push to GitHub does not start a build.** See [A push does not trigger a build](/docs/guides/github-and-builds/#troubleshooting). The service must meet all these conditions:

- It is deployed.
- It matches the repository and branch.
- It does not pin a commit.
- It watches a file that the push changed.
- It already has a build.

Or use [Re-run a build](#re-run-a-build).

**A re-run build ends as `Succeeded` straight away.** This should not happen, because the controller removes the Job of the last run from a build that was put back to the start. If it happens, the build was not put back to the start. Its `status.startedAt` is still set. Reset it as shown under [Re-run a build](#re-run-a-build).

## Related

- [GeassApp](/docs/reference/resources/geass-app/)
- [GeassDeployment](/docs/reference/resources/geass-deployment/)
- [GeassGitHubConnection](/docs/reference/resources/geass-github-connection/)
- [GitHub and builds](/docs/guides/github-and-builds/)
- [Resources overview](/docs/reference/resources/overview/)
