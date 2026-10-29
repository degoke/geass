---
title: "GeassBuild"
description: "Reference for the GeassBuild resource: one repository build that turns a Git service's commit into an image with a Kaniko Job, and its phases and logs."
sidebar:
  order: 13
---

A `GeassBuild` is one build of a Git service. When a [GeassApp](/docs/reference/resources/geass-app/) has a repository as its source, the operator creates a `GeassBuild` for it. The build resolves the branch to a commit, optionally waits for GitHub checks, runs a Kaniko Job that clones the repository and builds the Dockerfile, and records the resulting image digest. The service then deploys that digest.

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

This is the build the operator creates for a Git service with CI gating on. The name is `<app>-build-` plus the first 12 characters of a hash of the spec, so the same settings always give the same name.

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

The operator copies these from the service. Changing a field of the service changes the hash, so the next reconcile creates a new build with a new name instead of changing the old one.

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.app` | string | none | Required | The `GeassApp` name. |
| `spec.project` | string | none | Required | The project ID. The build runs on nodes in the project's region, and on nodes that carry every tag the project requires. |
| `spec.environment` | string | none | Required | The environment of the service. |
| `spec.repository` | string | none | none | The repository, `owner/name`. A leading `https://github.com/` and a trailing `.git` are accepted. Only github.com is supported. |
| `spec.revision` | string | none | none | A commit to build, copied from `source.git.commit`. Empty means the head of the branch. |
| `spec.branch` | string | none | none | The branch to clone and to resolve to a commit. |
| `spec.dockerfile` | string | `Dockerfile` when empty | none | The Dockerfile path, relative to `spec.context`. A path that already starts with the context is taken as relative to the repository root. |
| `spec.context` | string | none | none | The root directory of the build, copied from `source.git.context`. Kaniko builds in `/workspace/<context>`. A leading slash means the repository root, and a path that leaves the repository makes the build fail. |
| `spec.waitForCI` | boolean | `false` | none | Wait until every GitHub check run of the commit has finished successfully before building. |
| `spec.connectionRef` | LocalObjectReference | none | none | The [GeassGitHubConnection](/docs/reference/resources/geass-github-connection/) of the project. Needed to resolve a branch and to read check runs. |
| `spec.gitCredentialRef` | LocalObjectReference | the connection's Secret | none | The Secret whose `token` key is used to clone. The operator sets it to the connection's `secretRef`. |
| `spec.registry` | string | none | none | The full image reference Kaniko pushes to (`--destination`), used exactly as written. Required: Geass has no registry of its own, so a build without one fails with `no container registry is set for builds`, and the operator makes no build for a service that has none. |
| `spec.credentialRef` | LocalObjectReference | none | none | A Secret with the key `.dockerconfigjson`, mounted for Kaniko as `/kaniko/.docker/config.json`. Under the platform's `spec.tenancy.registry`, a build of an organization's project whose Secret is the platform's login (by name, or holding the same login) or another organization's fails instead. |
| `spec.cache` | boolean | `false` | none | Pass `--cache=true` to Kaniko. |
| `spec.logStoreRef` | LocalObjectReference | none | none | A [GeassObjectStore](/docs/reference/resources/geass-object-store/) that receives the build log. |

`spec.registry` is a destination, not a repository prefix: no app name and no tag are added to it. If you want a tag, write it, and note that a fixed tag is overwritten by every build. The deploy does not depend on the tag, because the service runs the repository at the digest the build reports (`<repository>@sha256:…`). Set `build.registry.repository` on the service, or `registry.repository` on the [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/), before you build. The credential the build pushes with (`spec.credentialRef`) is also copied into the service's namespace for the nodes to pull the image with.

## Status

| Field | Type | Meaning |
| --- | --- | --- |
| `status.phase` | string | `Pending`, `WaitingForCI`, `Running`, `Succeeded`, `Failed` or `Cancelled`. See below. |
| `status.jobRef` | string | The Job that runs the build, `<build>-kaniko`. |
| `status.jobNamespace` | string | The namespace the Job runs in when it is not the build's own: the [organization's build namespace](/docs/guides/organizations-and-workspaces/#builds-and-the-registry), for a project that belongs to an organization. Empty means the build's own namespace. |
| `status.image` | string | The image the build pushes to: `spec.registry`. |
| `status.imageDigest` | string | The `sha256:` digest of the image the last run pushed. Kaniko writes it to the termination message of the `kaniko` container (`--digest-file=/dev/termination-log`), and Geass reads it from the pod of the Job that succeeded. For a Job made before that, the fallback is the `Pushed <repository>@sha256:…` line of the Kaniko log. It is read again for every run. A run that starts over clears it. The service runs `<repository of status.image>@<this digest>`. |
| `status.sourceRevision` | string | The commit that was built. It is resolved when a run starts, from `spec.revision` or from the head of `spec.branch`, and kept for that run. A run that starts over clears it, so the next run resolves the head again. |
| `status.ciState` | string | `pending`, `success` or `failure`, while waiting for CI. |
| `status.ciChecks` | list of strings | The names of the check runs that were found. At most 50 are kept. |
| `status.logRef` | string | Where the log is: `pod-log://<job>/kaniko`, or `object://<store>/builds/<build>.log` once it is archived. |
| `status.failureReason` | string | Why the build failed. |
| `status.retryCount` | integer | The number of failed pods of the Job. |
| `status.startedAt` | time | When the Job was created. |
| `status.completedAt` | time | When the build succeeded. A build that failed has none. |
| `status.cancelRequested` | boolean | Set to stop the build. |
| `status.lastLogLine` | string | Shown in the dashboard's build log if set. Nothing sets it. |
| `status.conditions` | list | `Sandboxed`, only on a build of an organization's project while the platform's `spec.tenancy.sandbox` is set: `True` `RuntimeClass` when its pod runs under the sandboxed runtime, `False` `ReservedNodes` when it runs without it on nodes reserved for the organization, `False` `RuntimeClassMissing` while the build waits for the RuntimeClass (in `Pending`, with a `Warning` event `SandboxRuntimeClassMissing`). See [Sandboxed runtime for organizations](/docs/operations/sandboxed-runtime/). `RegistryIsolated`, only on a build of an organization's project while the platform's `spec.tenancy.registry` is set: `True` `Isolated` when its login and the repository it pushes to are the organization's, `False` with `PlatformCredential` (its login is the platform's, or another organization's) or `ForeignRepository` (it would push into a part of a registry that is the platform's or another organization's) when it was refused, with a `Warning` event `RegistryRefused`. See [Registry isolation for organizations](/docs/operations/registry-isolation/). Nothing else writes conditions for this kind. |

### Phases

| Phase | Meaning | What happens next |
| --- | --- | --- |
| `Pending` | The build was put back to the start by a push, a rebuild or a reset (a build that the operator has created but not yet reconciled has an empty phase). Also a build of an organization's project that waits for the platform's sandboxed runtime, whose `Sandboxed` condition says so. | The next reconcile resolves the commit. A build that waits for the runtime is looked at again every 30 seconds, and its Job is made once the RuntimeClass is there. |
| `WaitingForCI` | Waiting for check runs of the commit, or for GitHub to answer. | Checked again every 15 seconds. A commit with no check runs waits forever. |
| `Running` | The Kaniko Job exists. | Checked every 5 seconds. |
| `Succeeded` | The Job finished and a digest was found. | The service deploys the digest. The build is not looked at again, unless it has a log store and its log is not archived yet. |
| `Failed` | The Job failed (`kaniko Job failed`), GitHub refused a request, a check run did not succeed (`GitHub check "x" concluded failure`), no digest was found (`Kaniko completed without an immutable image digest`), or the build cannot run as written (no registry, or a path that leaves the repository). Also a build of an organization's project that the platform's `spec.tenancy.registry` refused (`Registry isolation: …`), before its Job was made or, for one that was running with the platform's login when the setting was turned on, after its Job was deleted. | A GitHub error or a failed check is retried every 30 seconds, until the Job has been created. A build whose Job ran stays failed until a push, a rebuild or a reset runs it again. A refused build is looked at again on each of its events, and runs if what refused it has changed. |
| `Cancelled` | `status.cancelRequested` was set. | The Job is deleted. |

The Job runs two containers in order. The init container `git` (image `alpine/git:latest`) clones the branch with the token, using an `Authorization` header, and checks out the commit. The container `kaniko` (image `gcr.io/kaniko-project/executor:v1.23.2`) builds the Dockerfile and pushes. The Job allows two retries (`backoffLimit: 2`).

Geass removes a finished Job, its pods and so their log one hour after it finishes (`ttlSecondsAfterFinished: 3600`). By then the result is in the build's status. If the build has a `logStoreRef`, its log is uploaded to the store before that. A finished build is not run again because its Job is gone: building again takes a push, a rebuild or a reset, as shown under [Re-run a build](#re-run-a-build).

## How the dashboard uses builds

A Git service has no build screen of its own. When you deploy, the **Deployments** tab shows a **Build** section in the logs of that deploy: the output of the build's pod, or the failure reason when the pod is gone. A push to the service's branch re-runs the build for its current settings, and the REST API has two actions for builds, which the dashboard does not use:

| Request | Effect |
| --- | --- |
| `POST /api/apps/<app>/build` | Finds the service's current build (below), deletes its Job and puts it back to `Pending`, clearing its digest, commit, check results and log reference. The operator then builds the head of the branch as it is now. It never creates a build: for a service that has none it answers `the app has not been built yet`. |
| `POST /api/apps/<app>/build/cancel` | Sets `status.cancelRequested` and the phase `Cancelled` on the current build, while it is `Pending`, `WaitingForCI` or `Running`. For a build that has finished it answers `the build has already finished, so there is nothing to cancel`: cancelling a build that succeeded would take its image out from under the service. |

The current build is the one in the service's `status.currentBuild`, which the operator keeps up to date. A build is named by a hash of the service's settings, so a service that turns a setting on and off again goes back to its first build, which is older than the second. The newest build is then one the service does not use, and a push builds the one it does. A service the operator has not reconciled yet has no record, and the newest build is used.

Every build the operator makes carries the labels `geass.dev/app=<app>`, `geass.dev/name=<app>`, `geass.dev/kind=GeassBuild`, `geass.dev/managed-by`, `geass.dev/system-namespace`, `geass.dev/project` and `geass.dev/environment`. `geass.dev/app` is the one a service's builds are found by:

- The GitHub push webhook, which re-runs the current build of each service the push matches.
- `POST /api/apps/<app>/build` and `/build/cancel`, which the command line calls as `geass build` and the MCP server as `rebuild_app`.
- The link between a deploy and its build: the dashboard attaches the current build to a deploy when it starts, so **Build Logs** has something to show. `geass status`, `geass deploy --wait` and the `app_status` tool report the current build the same way, and say why it failed.
- The operator's own tidy-up. After each successful build it deletes the oldest finished builds of the service beyond the newest 10, never the build the service runs.

A build made before `geass.dev/app` was put on every build gets it when the operator starts, so those builds are found too.

## What the build tells GitHub

A build with a `repository` reports to GitHub as it goes, as a commit status named `geass/build/<service>` (the service's name in the dashboard) on the commit in `status.sourceRevision`: `pending` while it is `WaitingForCI` or `Running`, `success` when it has `Succeeded` with a digest, `failure` when its Job failed or a CI check did (`status.ciState` is `failure`), and `error` for any other failed or `Cancelled` build. See [Reporting back to GitHub](/docs/guides/github-and-builds/#the-commit-status-of-a-build).

The annotation `geass.dev/github-status` records the last report as `<commit>:<phase>`, so each phase of a commit is sent once. A build is reported when its phase changes, or when it already carries the annotation, so the builds that exist when you upgrade Geass are not reported. The value `owed` means the first report did not get through and is being tried again. A project with **Report to GitHub** off, and a GitHub App without the **Commit statuses** permission, send nothing, and the build goes on.

## Re-run a build

A finished build is run again by a push to the service's branch, by `POST /api/apps/<app>/build`, or by hand. All of them put the build back to the start and clear what the last run recorded. The Job of the last run is deleted too: a Job that still exists is what the build reads its result from, so the build would finish at once with the old result. A build that has not started has no Job of its own, so the controller removes one that it finds, which makes a reset by hand safe even when the Job was not deleted first. The controller reads the digest of every run from that run's own pod, so a rebuild records the digest of the new image, and it never takes a digest recorded before the run ended for the result of this one.

To do it by hand:

```bash title="Build the current head of the branch"
kubectl -n geass-system delete job storefront-build-3f9a1c0b7d2e-kaniko --ignore-not-found
kubectl -n geass-system patch geassbuild storefront-build-3f9a1c0b7d2e --subresource=status --type merge \
  -p '{"status":{"phase":"Pending","failureReason":"","sourceRevision":"","ciState":"","imageDigest":null,"logRef":"","startedAt":null,"completedAt":null}}'
```

To build the same commit again, leave `sourceRevision` out of the patch.

Deleting the Job first is not required, since the controller removes the last run's Job from a build that was put back to the start, but it saves a pass.

Deleting the whole `GeassBuild` also works: the service recreates it on its next reconcile, with the same name and an empty status. Delete its Job first and wait for it to go, so the new build does not read the old Job's result.

## Editing by hand

Do not create builds yourself. A build with a name that no service owns builds an image that nothing deploys. Edit `spec` only through the service. Setting `status.cancelRequested` to `true` cancels a running build.

## What happens under the hood

For each build the operator creates the Job `<build>-kaniko` in `geass-system` with an owner reference to the build, so deleting the build deletes the Job and its pods. The Job's pods run on nodes in the project's region and on the nodes the project requires. The build also reads the GitHub API: `GET /repos/<repository>/commits/<branch or sha>` to resolve the commit, and `GET /repos/<repository>/commits/<sha>/check-runs` when `waitForCI` is on, using the connection's token, which Geass refreshes through the GitHub App. It writes to it too, to report the build: `POST /repos/<repository>/statuses/<sha>` (see [What the build tells GitHub](#what-the-build-tells-github)), which a project can turn off. When a build finishes and `spec.logStoreRef` names a ready object store, the operator uploads the Kaniko log to `builds/<build>.log` in the store's bucket. A finished build that has succeeded is not reconciled again, so a restart of the operator does not ask GitHub about every build it ever made.

```bash title="Inspect builds"
kubectl -n geass-system get geassbuilds -l geass.dev/app=storefront
kubectl -n geass-system get geassbuild storefront-build-3f9a1c0b7d2e -o jsonpath='{.status.phase}{"  "}{.status.failureReason}{"\n"}'
kubectl -n geass-system get jobs,pods -l job-name=storefront-build-3f9a1c0b7d2e-kaniko
kubectl -n geass-system logs job/storefront-build-3f9a1c0b7d2e-kaniko -c git
kubectl -n geass-system logs job/storefront-build-3f9a1c0b7d2e-kaniko -c kaniko
```

Read the logs within the hour: after that the Job and its pods are gone. Kaniko builds run as root in `geass-system`, with no security context, and a Dockerfile can run any command. Build only repositories you trust. See [Security](/docs/operations/security/).

## Troubleshooting

**The service says `Git source is waiting for a successful build`.** Read the build's phase and `failureReason`. `Running` means the Job is still working: read its logs. `WaitingForCI` means checks are not finished (see below). `Failed` has a reason.

**The build is `WaitingForCI` and never moves.** With `waitForCI` the commit needs at least one check run, and every check run has to be `completed` with the conclusion `success`. A commit with no check runs waits forever. Turn off **Wait for CI**, or add a workflow that runs on that branch.

**The build fails with `GitHub API returned 403 Forbidden: reading check runs needs the Checks: Read permission…` while waiting for CI.** Listing check runs needs the Checks permission. The GitHub App that Geass registers now asks for it, together with Contents, Metadata, Pull requests, Commit statuses and Deployments, but an app registered by an earlier version did not. Add **Checks: Read** to the app in GitHub, and accept the new permission on the installation.

**The build fails with `GitHub API returned 404 Not Found` or `401 Unauthorized`.** GitHub did not accept the connection's token for that repository. Check that the installation includes the repository, and that the repository name is right. See [GeassGitHubConnection](/docs/reference/resources/geass-github-connection/).

**`GitHub connection reference is required to resolve a branch`.** The build has no `spec.connectionRef` and no `spec.revision`. Link the project to GitHub.

**`GitHub check "<name>" concluded failure`.** A CI check failed for the commit this build recorded. Until the Job has been created, the operator checks again every 30 seconds, so re-running the check until it passes lets the build continue. A new push puts the build back to the start and resolves the new head of the branch, so it builds the newer commit.

**`kaniko Job failed`.** The build failed, or the push did. Read the logs of the `kaniko` container (and `git` if the clone failed) within the hour that the Job is kept. Common causes are a Dockerfile error and a registry that refuses the push (a wrong `spec.registry`, or no `credentialRef` for a private registry).

**`Kaniko completed without an immutable image digest`.** The Job succeeded, but Geass found no digest: the termination message of the `kaniko` container was empty, and the Kaniko output has no `Pushed <repository>@sha256:…` line. Read the end of `kubectl logs job/<build>-kaniko -c kaniko`. A build that failed this way before Geass read the termination message (the log scan then could not find a digest in Kaniko's output) did push its image: run it again with a push or `POST /api/apps/<app>/build`.

**`no container registry is set for builds`.** Neither the service nor the platform names a registry. See [Set the registry](/docs/guides/github-and-builds/#set-the-registry).

**`kubectl logs` for a finished build says the Job or pod was not found.** The Job is removed an hour after it finishes. Set `spec.logs.archiveStoreRef` on the service so the log of each build is kept in an object store.

**A push to GitHub does not start a build.** See [A push does not trigger a build](/docs/guides/github-and-builds/#troubleshooting): the service must be deployed, match the repository and branch, not pin a commit, watch a file the push changed, and already have a build. Or use [Re-run a build](#re-run-a-build).

**A re-run build ends as `Succeeded` straight away.** This should not happen: the controller removes the Job of the last run from a build that was put back to the start. If it does, the build was not put back to the start (its `status.startedAt` is still set). Reset it as shown under [Re-run a build](#re-run-a-build).

## Related

- [GeassApp](/docs/reference/resources/geass-app/)
- [GeassDeployment](/docs/reference/resources/geass-deployment/)
- [GeassGitHubConnection](/docs/reference/resources/geass-github-connection/)
- [GitHub and builds](/docs/guides/github-and-builds/)
- [Resources overview](/docs/reference/resources/overview/)
