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
| `spec.dockerfile` | string | `Dockerfile` when empty | none | The Dockerfile path inside the repository. |
| `spec.context` | string | none | none | Copied from `source.git.context`. Not used: Kaniko always builds with the whole repository as its context. |
| `spec.waitForCI` | boolean | `false` | none | Wait until every GitHub check run of the commit has finished successfully before building. |
| `spec.connectionRef` | LocalObjectReference | none | none | The [GeassGitHubConnection](/docs/reference/resources/geass-github-connection/) of the project. Needed to resolve a branch and to read check runs. |
| `spec.gitCredentialRef` | LocalObjectReference | the connection's Secret | none | The Secret whose `token` key is used to clone. The operator sets it to the connection's `secretRef`. |
| `spec.registry` | string | none | none | The full image reference Kaniko pushes to (`--destination`), used exactly as written. When empty the build pushes to `registry.geass.dev/<project>/<app>:<revision>`. |
| `spec.credentialRef` | LocalObjectReference | none | none | A Secret with the key `.dockerconfigjson`, mounted for Kaniko as `/kaniko/.docker/config.json`. |
| `spec.cache` | boolean | `false` | none | Pass `--cache=true` to Kaniko. |
| `spec.logStoreRef` | LocalObjectReference | none | none | A [GeassObjectStore](/docs/reference/resources/geass-object-store/) that receives the build log. |

:::caution
`spec.registry` is a destination, not a repository prefix: no app name and no tag are added to it. If you want a tag, write it, and note that a fixed tag is overwritten by every build. The deploy does not depend on the tag, because the service runs the image digest that the build reports. The registry default `registry.geass.dev` is not a registry you control, so set `build.registry.repository` on the service (or `registry.repository` on the [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/)) before you build.
:::

## Status

| Field | Type | Meaning |
| --- | --- | --- |
| `status.phase` | string | `Pending`, `WaitingForCI`, `Running`, `Succeeded`, `Failed` or `Cancelled`. See below. |
| `status.jobRef` | string | The Job that runs the build, `<build>-kaniko`. |
| `status.image` | string | The image the build pushes to: `spec.registry`, or the default above. |
| `status.imageDigest` | string | The `sha256:` digest of the pushed image. It is read from the Job annotation `geass.dev/image-digest`, or from the first value in the Kaniko log that starts with `sha256:`. It is read only when the build has no digest yet. |
| `status.sourceRevision` | string | The commit that was built. It is resolved once, from `spec.revision` or from the head of `spec.branch`, and kept. |
| `status.ciState` | string | `pending`, `success` or `failure`, while waiting for CI. |
| `status.ciChecks` | list of strings | The names of the check runs that were found. At most 50 are kept. |
| `status.logRef` | string | Where the log is: `pod-log://<job>/kaniko`, or `object://<store>/builds/<build>.log` once it is archived. |
| `status.failureReason` | string | Why the build failed. |
| `status.retryCount` | integer | The number of failed pods of the Job. |
| `status.startedAt` | time | When the Job was created. |
| `status.completedAt` | time | When the build succeeded. A build that failed has none. |
| `status.cancelRequested` | boolean | Set to stop the build. |
| `status.lastLogLine` | string | Shown in the dashboard's build log if set. Nothing sets it. |
| `status.conditions` | list | Nothing writes conditions for this kind. |

### Phases

| Phase | Meaning | What happens next |
| --- | --- | --- |
| `Pending` | The build was reset (a build that the operator has created but not yet reconciled has an empty phase). | The next reconcile resolves the commit. |
| `WaitingForCI` | Waiting for check runs of the commit, or for GitHub to answer. | Checked again every 15 seconds. A commit with no check runs waits forever. |
| `Running` | The Kaniko Job exists. | Checked every 5 seconds. |
| `Succeeded` | The Job finished and a digest was found. | The service deploys the digest. The build is not looked at again, unless it has a log store and its log is not archived yet. |
| `Failed` | The Job failed (`kaniko Job failed`), GitHub refused a request, a check run did not succeed (`GitHub check "x" concluded failure`), or no digest was found (`Kaniko completed without an immutable image digest`). | A GitHub error or a failed check is retried every 30 seconds, until the Job has been created. A build whose Job ran stays failed until you reset it. |
| `Cancelled` | `status.cancelRequested` was set. | The Job is deleted. |

The Job runs two containers in order. The init container `git` (image `alpine/git:latest`) clones the branch with the token, using an `Authorization` header, and checks out the commit. The container `kaniko` (image `gcr.io/kaniko-project/executor:v1.23.2`) builds the Dockerfile and pushes. The Job allows two retries (`backoffLimit: 2`).

Geass removes a finished Job, its pods and so their log one hour after it finishes (`ttlSecondsAfterFinished: 3600`). By then the result is in the build's status. If the build has a `logStoreRef`, its log is uploaded to the store before that. A finished build is not run again because its Job is gone: building again takes a reset, as shown under [Re-run a build](#re-run-a-build).

## How the dashboard uses builds

A Git service has no build screen of its own. When you deploy, the **Deployments** tab shows a **Build** section in the logs of that deploy, with the failure reason of the build. The REST API has two actions for builds, which the dashboard does not use:

| Request | Effect |
| --- | --- |
| `POST /api/apps/<app>/build` | Finds the builds labelled `geass.dev/app=<app>`. If there are none and the service has a Git source, it creates one. Then it sets the first one in the list (not the newest) back to `Pending`, and clears its start and completion times. |
| `POST /api/apps/<app>/build/cancel` | Sets `status.cancelRequested` and the phase `Cancelled` on the first one in the list (not the newest). |

:::caution
Builds created by the operator carry the labels `geass.dev/name=<app>` and `geass.dev/kind=GeassBuild`, not `geass.dev/app`. Everything that looks a service's builds up does so by `geass.dev/app`:

- The GitHub push webhook, so a push does not rebuild a service whose builds were all created by the operator. The webhook answers `"triggered":0`.
- `POST /api/apps/<app>/build` and `/build/cancel`. The first creates a second build record, named `<app>-build-` plus a random suffix, instead of re-running the one the service uses. The service only deploys the image of its own build. Once that record exists, a push re-runs it, and still no service uses it.
- The link between a deploy and its build. The dashboard attaches a build to a deploy by this lookup when it starts the deploy, so the **Build Logs** tab usually has nothing to show. The latest build that `geass status` and the `app_status` tool report is found the same way, so they report no build for a service whose builds the operator made.
- The operator's own tidy-up. After each successful build it is meant to delete the oldest finished builds of the service beyond the newest 10, never the build the service runs. It finds them by the same label, so today it deletes none, and builds accumulate.

To rebuild, use [Re-run a build](#re-run-a-build).
:::

## Re-run a build

A finished build is not rebuilt on its own: the same settings give the same build name, its recorded commit does not move, and a build whose Job has run is not run again when the Job disappears. To build again, delete the Job, then put the build back to `Pending`. The patch clears the old digest too: a build reads a digest from its Job only when it has none, so a rebuild that kept the old one would never record the new one.

```bash title="Build the same commit again"
kubectl -n geass-system delete job storefront-build-3f9a1c0b7d2e-kaniko --ignore-not-found
kubectl -n geass-system patch geassbuild storefront-build-3f9a1c0b7d2e --subresource=status --type merge \
  -p '{"status":{"phase":"Pending","failureReason":"","imageDigest":null,"startedAt":null,"completedAt":null}}'
```

Delete the Job first. While it exists, the build reads its old result and finishes at once.

```bash title="Build the current head of the branch"
kubectl -n geass-system delete job storefront-build-3f9a1c0b7d2e-kaniko --ignore-not-found
kubectl -n geass-system patch geassbuild storefront-build-3f9a1c0b7d2e --subresource=status --type merge \
  -p '{"status":{"phase":"Pending","failureReason":"","sourceRevision":"","ciState":"","imageDigest":null,"startedAt":null,"completedAt":null}}'
```

Deleting the whole `GeassBuild` also works: the service recreates it on its next reconcile, with the same name and an empty status. Delete its Job first and wait for it to go, so the new build does not read the old Job's result.

## Editing by hand

Do not create builds yourself. A build with a name that no service owns builds an image that nothing deploys. Edit `spec` only through the service. Setting `status.cancelRequested` to `true` cancels a running build.

## What happens under the hood

For each build the operator creates the Job `<build>-kaniko` in `geass-system` with an owner reference to the build, so deleting the build deletes the Job and its pods. The Job's pods run on nodes in the project's region and on the nodes the project requires. The build also reads the GitHub API: `GET /repos/<repository>/commits/<branch or sha>` to resolve the commit, and `GET /repos/<repository>/commits/<sha>/check-runs` when `waitForCI` is on, using the connection's token, which Geass refreshes through the GitHub App. When a build finishes and `spec.logStoreRef` names a ready object store, the operator uploads the Kaniko log to `builds/<build>.log` in the store's bucket. A finished build that has succeeded is not reconciled again, so a restart of the operator does not ask GitHub about every build it ever made.

```bash title="Inspect builds"
kubectl -n geass-system get geassbuilds -l geass.dev/name=storefront
kubectl -n geass-system get geassbuild storefront-build-3f9a1c0b7d2e -o jsonpath='{.status.phase}{"  "}{.status.failureReason}{"\n"}'
kubectl -n geass-system get jobs,pods -l job-name=storefront-build-3f9a1c0b7d2e-kaniko
kubectl -n geass-system logs job/storefront-build-3f9a1c0b7d2e-kaniko -c git
kubectl -n geass-system logs job/storefront-build-3f9a1c0b7d2e-kaniko -c kaniko
```

Read the logs within the hour: after that the Job and its pods are gone. Kaniko builds run as root in `geass-system`, with no security context, and a Dockerfile can run any command. Build only repositories you trust. See [Security](/docs/operations/security/).

## Troubleshooting

**The service says `Git source is waiting for a successful build`.** Read the build's phase and `failureReason`. `Running` means the Job is still working: read its logs. `WaitingForCI` means checks are not finished (see below). `Failed` has a reason.

**The build is `WaitingForCI` and never moves.** With `waitForCI` the commit needs at least one check run, and every check run has to be `completed` with the conclusion `success`. A commit with no check runs waits forever. Turn off **Wait for CI**, or add a workflow that runs on that branch.

**The build fails with `GitHub API returned 403 Forbidden` while waiting for CI.** Listing check runs needs the Checks permission, and the GitHub App that Geass registers asks only for read access to Contents, Metadata and Pull requests. Add **Checks: Read** to the app in GitHub, and accept the new permission on the installation.

**The build fails with `GitHub API returned 404 Not Found` or `401 Unauthorized`.** GitHub did not accept the connection's token for that repository. Check that the installation includes the repository, and that the repository name is right. See [GeassGitHubConnection](/docs/reference/resources/geass-github-connection/).

**`GitHub connection reference is required to resolve a branch`.** The build has no `spec.connectionRef` and no `spec.revision`. Link the project to GitHub.

**`GitHub check "<name>" concluded failure`.** A CI check failed for the commit this build recorded. Until the Job has been created, the operator checks again every 30 seconds, so re-running the check until it passes lets the build continue. A new push does not change the commit of an existing build: to build a newer commit, reset the build as shown under [Re-run a build](#re-run-a-build).

**`kaniko Job failed`.** The build failed, or the push did. Read the logs of the `kaniko` container (and `git` if the clone failed) within the hour that the Job is kept. Common causes are a Dockerfile error and a registry that refuses the push (a wrong `spec.registry`, or no `credentialRef` for a private registry).

**`Kaniko completed without an immutable image digest`.** The Job succeeded but its log did not contain a value that starts with `sha256:`. Check that the push worked and the registry returned a digest. A value that carries the digest after the repository name, such as `<repository>@sha256:…`, does not start with it.

**`kubectl logs` for a finished build says the Job or pod was not found.** The Job is removed an hour after it finishes. Set `spec.logs.archiveStoreRef` on the service so the log of each build is kept in an object store.

**A push to GitHub does not start a build.** See the caution under [How the dashboard uses builds](#how-the-dashboard-uses-builds). Use [Re-run a build](#re-run-a-build).

**A re-run build ends as `Succeeded` straight away.** The Job of the earlier run still existed, or the old digest was kept. Delete the Job first and clear `imageDigest` as shown under [Re-run a build](#re-run-a-build).

## Related

- [GeassApp](/docs/reference/resources/geass-app/)
- [GeassDeployment](/docs/reference/resources/geass-deployment/)
- [GeassGitHubConnection](/docs/reference/resources/geass-github-connection/)
- [GitHub and builds](/docs/guides/github-and-builds/)
- [Resources overview](/docs/reference/resources/overview/)
