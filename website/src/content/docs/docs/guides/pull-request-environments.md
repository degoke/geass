---
title: "Pull-request environments"
description: "Get a temporary copy of an environment for each pull request: how GitHub events create it, what Geass copies, when it goes, and what it costs."
---

A **pull request environment** is a short-lived copy of one of your environments. Geass creates it when someone opens a
pull request, and deletes it when the pull request closes. The copy runs the branch of the pull request, so reviewers can
try the change before it merges. The feature is off until you turn it on for a project.

GitHub webhooks drive this feature. Geass learns about pull requests from the `pull_request` event that your GitHub App
sends to the dashboard.

## Before you start

You need these things:

- A project linked to GitHub, with a repository service in the environment that you want to copy. See
  [GitHub and builds](/docs/guides/github-and-builds/).
- A GitHub App that is subscribed to the **Pull request** event and can read pull requests. The app that Geass registers
  for you already has this subscription and permission. If you created the app yourself, check **Permissions & events**
  in the settings of the app in GitHub.
- Geass also writes a comment, deployments, and commit statuses on the pull request. For these, the app needs
  **Pull requests**, **Deployments**, and **Commit statuses** at **Read and write**. See
  [What Geass writes on the pull request](#what-geass-writes-on-the-pull-request). An app that Geass registered before
  Geass wrote to GitHub does not have these permissions. Its pull request environments work without them. See
  [Give an existing GitHub App the new permissions](/docs/guides/github-and-builds/#give-an-existing-github-app-the-new-permissions).
- A dashboard address that GitHub can reach over HTTPS, because GitHub delivers the webhook to
  `<dashboard URL>/webhooks/github`. See [Domains and TLS](/docs/guides/domains-and-tls/).
- Spare capacity for the copies. See [Limits and cost](#limits-and-cost).

## Turn it on

1. Open **Settings** → **Environments** of the project. The **PR Environments** block is below the list of environments.
2. Turn on **Create an environment for each pull request**.
3. Under **Copy this environment**, select the environment to copy. Select the environment whose services build your
   repository. The list does not include environments that are themselves pull request environments.
4. Set **Open at once** to the maximum number of pull request environments that can exist at the same time, from 1 to
   25. The default is 5.
5. Click **Save**. A toast says "Saved".

If the project is not linked to GitHub, the block tells you to link it first under **GitHub**.

If you turn off the switch, Geass creates no new environments. Geass still cleans up the environments that already exist
when their pull requests close.

To turn on the feature through the API, use a workspace admin token:

```bash title="Turn on pull request environments"
curl -X POST https://geass.example.com/api/projects/proj-1a2b3c4d/pr-environments/save \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d enabled=on -d baseEnvironment=production -d maxOpen=5
```

## What happens when a pull request opens

GitHub sends a `pull_request` event. Geass creates or refreshes an environment for the actions `opened`, `reopened`,
`ready_for_review`, and `synchronize`. Thus, a draft pull request also gets an environment. Geass does this only when
**all** of these conditions are true:

- PR environments are on for the project, and the event came from the GitHub installation that the project is linked to.
- The pull request is **not from a fork**. Its head repository and its base repository must both be the repository in
  the event. Code from a fork never runs next to the secrets of your project.
- The base environment has a service that builds this repository, on the branch that the pull request targets. A pull
  request into `main` gets an environment only if a service in the base environment builds `main` of that repository.
- The limit in **Open at once** is not reached.
- No other environment already uses the name.

Then Geass does these steps:

1. **Creates the environment** and adds it to the project. A tab with a **PR #12** badge appears in the top bar. The
   pull request appears under **Open pull requests**.
2. **Copies the shared variables** of the base environment into the new environment. Geass also copies secret values.
3. **Copies the databases and services**, as the next sections describe, and deploys them immediately. They are not
   drafts, and you do not click **Deploy changes**.

Later events for the same pull request only create pieces that are missing. They do not change what exists.

New commits on the branch of the pull request update the copy. GitHub sends a `push` event for each push. Geass then
runs the build again for every repository service in the copy, because these services follow the branch of the pull
request. The services roll to the new image when their builds succeed. See
[Branch deploys and push triggers](/docs/guides/github-and-builds/#branch-deploys-and-push-triggers).

The `synchronize` event that comes with the push only creates what is missing. It does not change what exists. The
**Watch paths** of a service also apply here. A push that changes only files outside the watch paths of a service does
not rebuild that service.

### Naming

| Object | Name |
| --- | --- |
| The environment | `pr-<number>`, for example `pr-12`. If another repository in the same project already has a pull request with that number, `pr-12-<repository name>`. |
| A cloned service or database | The ID of the original plus `-pr<number>`, for example `amber-fern-pr12`. The display name is the display name of the original. |
| The namespace | `<project ID>-<environment>`, for example `proj-1a2b3c4d-pr-12`. |

### What is copied

| Copied | How it changes in the copy |
| --- | --- |
| In-cluster databases (Postgres, MySQL, Redis, SQLite) | Same engine, version, size, and storage. A single instance, with no high availability. **Empty**: no data and no restore. Backups are off. |
| Services, functions, and image services | The same settings and variables. One replica, no autoscaling. A copy of the secrets of the service. References to a copied database point at the copy. The copy [sleeps when idle](#sleep-when-idle), unless the original sets that setting itself. |
| Repository services | A repository service that builds the repository of the pull request follows the branch of the pull request. A service for another repository keeps its branch. No copy keeps a pinned commit. |
| Shared variables | Copied, including secret values. |

Geass does not copy these things:

- Buckets, logical databases, and databases outside the cluster (AWS RDS, PlanetScale). A copied service that
  refers to one of them continues to point at the original.
- Domains. Each environment gets its own address, and the copy has no custom domain.
- Backups and restore points. The copied databases are throwaway.
- The schedules of a function. A copy in every pull request would repeat the action of the schedule one time for each
  open pull request. Examples of such actions are mail and writes to a shared store. Geass copies the rest of the function, its code and
  its mode. The original keeps its schedules. See [Functions](/docs/guides/functions/#schedules).

### URLs

The copy of a service has no domain of its own. How you reach the copy depends on your setup:

- If Cloudflare is connected, a service without a domain gets a default domain. Thus, you can reach the copy at
  `<service ID>-<project ID>-<environment>.<your domain>`, for example `amber-fern-pr12-proj-1a2b3c4d-pr-12.example.com`.
  See [Domains and TLS](/docs/guides/domains-and-tls/#default-hostnames).
- If Cloudflare is not connected, set a **Custom domain** on the copied service. Do this under its **Settings** →
  **Networking**, in the environment of the pull request.
- Other services in the environment reach the copy at its private address. The same section shows this address.

A copy without a public host cannot [sleep when idle](#sleep-when-idle), because a request to a host wakes the copy.

Geass stores an address for each pull request, and shows it as a link in **Open pull requests**. That address is the
pull request on GitHub, not a running service. The addresses of the copies are in the comment that Geass keeps on the
pull request. The next section describes this comment.

## What Geass writes on the pull request

Geass reports on the pull request, and also in the dashboard. Thus, reviewers find the preview without instructions
about where to look. Geass writes three things, and each thing has its own GitHub App permission. The **Report to
GitHub** switch of the project turns off all three. See
[Reporting back to GitHub](/docs/guides/github-and-builds/#reporting-back-to-github).

**One comment, edited in place.** When the environment has services, Geass adds a comment to the pull request. The
comment lists each service of the environment with its state: `Queued`, `Building`, `Build failed`, `Deploying`,
`Ready`, or `Failed`. When a service is ready, the comment also shows its address, the commit that it was built from,
and a link to the environment in the dashboard. Geass makes only this one comment. When a service changes state, Geass
edits the comment, and it does not add another comment.

```markdown title="The comment"
### Geass preview environment `pr-12`

| Service | Status | Preview |
| --- | --- | --- |
| api | Ready | <https://api-pr12-proj-1a2b3c4d-pr-12.example.com/> |
| web | Building | - |

Built from commit `9f3c2a1`. [Open it in Geass](https://geass.example.com/projects/proj-1a2b3c4d?environment=pr-12).
```

The first line of the comment is an HTML comment, `<!-- geass:pr-environment project=proj-1a2b3c4d -->`. GitHub does not
show it. Geass uses this line to find the comment again, so do not delete it from the source of the comment. The line
contains the project. Thus, two Geass projects that watch one repository each keep their own comment. Geass only edits a
comment that its own GitHub App wrote.

When the pull request closes or the environment is removed, Geass edits the comment to say that the environment was
removed. A pull request that never got a comment does not get one then.

**A GitHub Deployment for each service.** Each repository service of the copy is a deployment in an environment named
`pr-<number>`. The deployment is marked transient, and the address of the service is the environment URL. When the
environment is removed, Geass makes the deployments inactive. Geass reports the environment as a preview of a pull
request, never as production.

**A commit status for each build.** The head commit of the pull request shows `geass/build/<service name>`, as it does
for any other build. There is one status for each service of the copy that builds the repository. See
[The commit status of a build](/docs/guides/github-and-builds/#the-commit-status-of-a-build).

Pull requests from forks never get an environment, so they get none of these things.

## Sleep when idle

Most open pull requests are idle most of the time. Thus, Geass turns on **Sleep when idle** for every service that it
copies, with the default idle time of 5 minutes. The exception is an original service that has its own `scaleToZero`
setting. In that case, the copy keeps that setting, even when the setting is off.

A copy that sleeps has no pods until a request to its host starts it. The copy uses no CPU or memory while it waits.
The first request after a sleep waits a few seconds while the copy starts.

A copy sleeps only when the serverless add-on of the cluster is on and the copy has a public host. Otherwise, the copy
runs all the time, as before, and its `ScaleToZero` condition tells you why. See
[Sleep when idle](/docs/guides/scale-to-zero/).

## What happens when a pull request closes

When the pull request is closed or merged, Geass deletes the environment. This deletion includes these things:

- The services and databases of the environment, and their data.
- The shared variables of the environment.
- The API tokens that were limited to the environment.

The tab and the entry under **Open pull requests** go away. Geass makes the GitHub Deployments of the copies inactive,
and it edits the comment on the pull request to say that the environment was removed. Geass does this work in the
background, after the environment is gone. Thus, this work never delays the removal, and a refusal from GitHub does not
undo the removal.

To remove an environment earlier, do these steps:

1. Open **Settings** → **Environments** → **Open pull requests**.
2. Click **Close** next to the pull request.
3. Confirm in **Close pull request environment?**.

Geass does not change the pull request on GitHub. Use **Close**, not the **Delete** button in the list of environments.
**Close** also clears the pull request from the list.

```bash title="Close a pull request environment through the API"
curl -X POST https://geass.example.com/api/projects/proj-1a2b3c4d/pr-environments/close \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d number=12 -d repository=acme/storefront
```

## Limits and cost

- **Open at once** sets the maximum number of environments: 5 by default, and 25 at most. When the number of
  environments reaches this maximum, a new pull request gets no environment. See [Troubleshooting](#troubleshooting).
- Every environment is a full copy of the workload of the base environment. A copy that is awake uses all of that
  workload. For example, take a base environment with two services and a Postgres database. Each service uses 0.25 CPU
  and 256 MB. The database uses 0.25 CPU and 512 MB, with a 10 GiB volume. That environment costs 0.75 CPU, 1 GB of memory, and a 10 GiB volume
  for each pull request. Five open pull requests use 3.75 CPU, 5 GB, and 50 GiB of volumes.
- Geass builds every repository service in a copy when it creates the environment. It builds them again for each push
  to the branch of the pull request. Builds run in `geass-system`, and they also use the cluster.
- The usual capacity check that stops **Deploy changes** does not run for these copies. Instead, a service that does
  not fit reports **Unavailable** with a capacity message. See [Usage and capacity](/docs/guides/usage-and-capacity/).
- Pull requests from forks never get an environment.

## What happens under the hood

- The settings are `spec.prEnvironments` on the `GeassProject` (`enabled`, `baseEnvironment`, `maxOpen`). The open pull
  requests are `status.pullRequests`. Each entry has the number, the repository, the branch, the environment, the URL
  of the pull request, and the time that it opened.
- Copies are ordinary `GeassApp` and `GeassDatabase` objects labelled `geass.dev/pull-request=<number>`. Geass deploys
  them when it creates them. A copy of a service has `spec.scaleToZero.enabled: true`, unless the original has a
  `scaleToZero` setting. A copy of a function has no `triggers`.
- The environment is an ordinary entry in `spec.environments`. Thus, everything in
  [Projects and environments](/docs/guides/projects-and-environments/) applies to it.
- The operator keeps the comment up to date. When a service of the environment changes, it writes the whole comment
  from what the cluster says. Thus, the comment is correct, whichever service wrote last. The operator remembers what
  it last wrote, so a pass with nothing new sends nothing.
- The operator writes the comment only while the project still lists the pull request in `status.pullRequests`. This
  rule stops it from writing over the comment that says that the environment was removed.
- A connection that is not made through the GitHub App cannot tell which comments are the comments of the app. It uses
  only the marker to find the comment.
- Geass records the GitHub Deployment of a copy on the service, in the annotation `geass.dev/github-deployment`. The
  dashboard uses this annotation to find the deployment and make it inactive.
- The answer to the GitHub webhook lists what Geass did, for example `{"accepted":true,"environments":[{"project":"proj-1a2b3c4d","action":"ensure","environment":"pr-12","created":3}]}`.

```bash title="Inspect pull request environments"
kubectl -n geass-system get geassproject proj-1a2b3c4d -o jsonpath='{.status.pullRequests}{"\n"}'
kubectl -n geass-system get geassapps,geassdatabases -l geass.dev/pull-request=12
kubectl -n proj-1a2b3c4d-pr-12 get pods
```

## Troubleshooting

**No environment appears when I open a pull request.** Check each item in the list in
[What happens when a pull request opens](#what-happens-when-a-pull-request-opens). Then do these steps:

1. Open the GitHub App in GitHub.
2. Select **Advanced** → **Recent Deliveries**.
3. Find the `pull_request` delivery. Its response shows what Geass decided.

The response can show these results:

- `401` means that the webhook secret in Geass does not match the secret in GitHub. `503` means that Geass has no stored
  secret.
- `"environments":null` means that nothing matched. The feature is off, the pull request is from a fork, the project is
  linked to another installation, or no service in the base environment builds this repository and branch.
- A `note` explains a skip: `the limit of 5 pull request environments is reached`, `an environment named pr-12 already
  exists`, or `the base environment does not exist`.

**The deliveries are missing entirely.** The app is not subscribed to **Pull request**, or GitHub cannot reach the
dashboard. To check the webhook URL, use **Test GitHub App** in **Settings** → **Connectors**.

**The limit is reached.** Close a stale environment with **Close**. Or raise **Open at once** (up to 25), and save. A pull
request that was refused does not later get an environment by itself. After you make room, push to the pull request or
reopen it.

**The environment exists but its services never become Ready.** A repository service needs a successful build. Check the
registry and **Build Logs**. See [GitHub and builds](/docs/guides/github-and-builds/#troubleshooting). If the build
succeeded, the cluster might be out of capacity. See [Usage and capacity](/docs/guides/usage-and-capacity/).

**The copy has no URL.** Copies get no domain of their own. See [URLs](#urls).

**There is no comment on the pull request.** Check these things:

- **Report to GitHub** is on for the project.
- The pull request has an environment. The pull request then appears under **Open pull requests**.
- The GitHub App has the **Pull requests** permission at **Read and write**.

An app registered before Geass wrote to GitHub has this permission at read only. The `GitHubReporting` condition of the
connection then says `PermissionMissing`, and **Test GitHub App** lists what is missing. See
[Give an existing GitHub App the new permissions](/docs/guides/github-and-builds/#give-an-existing-github-app-the-new-permissions).

**The comment shows a service as `Building` or `Deploying` long after it came up.** Geass writes the comment when a
service changes state. Look at the service in the dashboard, and look for `Could not report to GitHub` in the operator
log. If GitHub does not accept a comment, Geass tries three times, and then tries again after ten minutes. Geass also
writes the comment again at the next change of state.

**The first request to a copy is slow.** The copy was asleep. See [Sleep when idle](#sleep-when-idle).

**The copied database is empty.** This result is intended: the copy starts with no data. Run your migrations and seed
data when the service starts.

**The environment is still there after the pull request merged.** Geass did not receive the `closed` event. Check
**Recent Deliveries**, and then use **Close**.

**A closed pull request is still listed after I deleted its environment.** You used **Delete** in the list of
environments. To clear the entry, click **Close** on the entry under **Open pull requests**.

## Related

- [GitHub and builds](/docs/guides/github-and-builds/)
- [Projects and environments](/docs/guides/projects-and-environments/)
- [Connect GitHub](/docs/guides/connectors/github/): register the GitHub App.
- [Domains and TLS](/docs/guides/domains-and-tls/)
- [Usage and capacity](/docs/guides/usage-and-capacity/)
- [Sleep when idle](/docs/guides/scale-to-zero/)
- [Functions](/docs/guides/functions/)
