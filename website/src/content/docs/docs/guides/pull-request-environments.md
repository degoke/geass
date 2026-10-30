---
title: "Pull-request environments"
description: "Get a temporary copy of an environment for each pull request: how GitHub events create it, what Geass copies, when it goes, and what it costs."
---

A **pull request environment** is a short-lived copy of one of your environments. Geass creates it when someone opens a
pull request, and deletes it when the pull request closes. The copy runs the branch of the pull request. So reviewers
can try the change before it merges. The feature is off until you turn it on for a project.

## Before you start

You need these things:

- A project linked to GitHub, with a repository service in the environment that you want to copy. See
  [GitHub and builds](/docs/guides/github-and-builds/).
- A GitHub App that receives the **Pull request** event and can read pull requests. The app that Geass registers for
  you already can.
- For the comment, deployments, and commit statuses on the pull request: **Pull requests**, **Deployments**, and
  **Commit statuses** at **Read and write**. An app that an older Geass registered might not have them. Its pull request
  environments work without them. See
  [Give an existing GitHub App the new permissions](/docs/guides/github-and-builds/#give-an-existing-github-app-the-new-permissions).
- A dashboard address that GitHub can reach over HTTPS. See [Domains and TLS](/docs/guides/domains-and-tls/).
- Spare capacity for the copies. See [Limits and cost](#limits-and-cost).

## Turn it on

1. Open **Settings** → **Environments** of the project. The **PR Environments** block is below the list of
   environments.
2. Turn on **Create an environment for each pull request**.
3. Under **Copy this environment**, select the environment whose services build your repository.
4. Set **Open at once** to the maximum number of pull request environments at the same time, from 1 to 25. The default
   is 5.
5. Click **Save**. A toast says "Saved".

If the project is not linked to GitHub, the block tells you to link it first under **GitHub**.

If you turn off the switch, Geass creates no new environments. Geass still removes the existing ones when their pull
requests close.

To turn on the feature through the API, use a workspace admin token:

```bash title="Turn on pull request environments"
curl -X POST https://geass.example.com/api/projects/proj-1a2b3c4d/pr-environments/save \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d enabled=on -d baseEnvironment=production -d maxOpen=5
```

## What happens when a pull request opens

Geass creates an environment when a pull request opens, reopens, or becomes ready for review. A draft pull request also
gets an environment. Geass does this only when **all** of these conditions are true:

- The feature is on, and the pull request comes from the GitHub installation that the project is linked to.
- The pull request is **not from a fork**. Code from a fork never runs next to the secrets of your project.
- The copied environment has a service that builds this repository, on the branch that the pull request targets. For
  example, a pull request into `main` needs a service that builds `main`.
- The **Open at once** limit is not reached.
- No other environment already uses the name.

Then Geass does these steps:

1. Creates the environment. A tab with a **PR #12** badge appears in the top bar. The pull request appears under
   **Open pull requests**.
2. Copies the shared variables, including secret values.
3. Copies the databases and services, and deploys them immediately. They are not drafts, and you do not click
   **Deploy changes**.

New commits on the branch update the copy. Geass builds each repository service of the copy again, and the services
roll to the new image when their builds succeed. **Watch paths** also apply here. See
[Branch deploys and push triggers](/docs/guides/github-and-builds/#branch-deploys-and-push-triggers).

### Naming

| Item | Name |
| --- | --- |
| The environment | `pr-<number>`, for example `pr-12`. If another repository in the project already has a pull request with that number, `pr-12-<repository name>`. |
| A copied service or database | The ID of the original plus `-pr<number>`, for example `amber-fern-pr12`. The display name stays the same. |

### What is copied

| Copied | How it changes in the copy |
| --- | --- |
| In-cluster databases (Postgres, MySQL, Redis, SQLite) | Same engine, version, size, and storage. One instance, with no high availability. **Empty**: no data. Backups are off. |
| Services and functions | The same settings, variables, and secrets. One replica, no autoscaling. References to a copied database point at the copy. The copy [sleeps when idle](#sleep-when-idle), unless the original has its own sleep setting. |
| Repository services | A service that builds the repository of the pull request follows the branch of the pull request. A service for another repository keeps its branch. No copy keeps a pinned commit. |
| Shared variables | Copied, including secret values. |

Geass does not copy these things:

- Buckets, logical databases, and databases outside the cluster (AWS RDS, PlanetScale). A copied service that uses one
  of them points at the original.
- Domains. The copy has no custom domain.
- Backups and restore points.
- The schedules of a function. Otherwise each open pull request repeats the scheduled work, for example a mail. See
  [Functions](/docs/guides/functions/#schedules).

### URLs

A copied service has no domain of its own. How you reach it depends on your setup:

- If Cloudflare is connected, the copy gets a default domain, for example
  `amber-fern-pr12-proj-1a2b3c4d-pr-12.example.com`. See
  [Domains and TLS](/docs/guides/domains-and-tls/#default-hostnames).
- If Cloudflare is not connected, set a **Custom domain** under **Settings** → **Networking** of the copied service.
- Other services in the environment reach the copy at its private address, in the same section.

A copy without a public domain cannot [sleep when idle](#sleep-when-idle).

The link under **Open pull requests** opens the pull request on GitHub, not a running service. The addresses of the
copies are in the comment that Geass keeps on the pull request.

## What Geass writes on the pull request

Geass writes three things on GitHub. Each needs its own GitHub App permission. The **Report to GitHub** switch of the
project turns off all three. See [Reporting back to GitHub](/docs/guides/github-and-builds/#reporting-back-to-github).

**One comment, edited in place.** The comment lists each service of the environment with its state: `Queued`,
`Building`, `Build failed`, `Deploying`, `Ready`, or `Failed`. A ready service also shows its address, its commit, and
a link to the environment in the dashboard. When a service changes state, Geass edits the comment. It does not add
another one.

```markdown title="The comment"
### Geass preview environment `pr-12`

| Service | Status | Preview |
| --- | --- | --- |
| api | Ready | <https://api-pr12-proj-1a2b3c4d-pr-12.example.com/> |
| web | Building | - |

Built from commit `9f3c2a1`. [Open it in Geass](https://geass.example.com/projects/proj-1a2b3c4d?environment=pr-12).
```

The source of the comment starts with a hidden line, `<!-- geass:pr-environment project=proj-1a2b3c4d -->`. Do not
delete this line. Geass uses it to find the comment again. When the environment is removed, Geass edits the comment to
say so.

**A GitHub Deployment for each service.** Each repository service of the copy is a deployment in the GitHub environment
`pr-<number>`, marked transient. The address of the service is the environment URL. When the environment is removed,
Geass makes the deployments inactive.

**A commit status for each build.** The head commit of the pull request shows `geass/build/<service name>`. See
[The commit status of a build](/docs/guides/github-and-builds/#the-commit-status-of-a-build).

## Sleep when idle

Most pull requests are idle most of the time. So Geass turns on **Sleep when idle** for every service that it copies,
with an idle time of 5 minutes. If the original service has its own sleep setting, the copy keeps that setting, even
when it is off.

A sleeping copy uses no CPU or memory. The first request after a sleep waits a few seconds while the copy starts. A copy
sleeps only when the serverless add-on is on and the copy has a public domain. Otherwise, the copy runs all the time. See
[Sleep when idle](/docs/guides/scale-to-zero/).

## What happens when a pull request closes

When the pull request is closed or merged, Geass deletes the environment. This deletion includes these things:

- The services and databases of the environment, and their data.
- The shared variables of the environment.
- The API tokens that were limited to the environment.

The tab and the entry under **Open pull requests** go away. Geass makes the GitHub Deployments inactive and edits the
comment. A refusal from GitHub does not stop the removal.

To remove an environment earlier, do these steps:

1. Open **Settings** → **Environments** → **Open pull requests**.
2. Click **Close** next to the pull request.
3. Confirm in **Close pull request environment?**.

Geass does not change the pull request on GitHub. Use **Close**, not **Delete** in the list of environments. **Close**
also removes the pull request from the list. Through the API, call
`POST /api/projects/{project}/pr-environments/close` with `number` and `repository`.

## Limits and cost

- **Open at once** sets the maximum number of environments: 5 by default, and 25 at most. At the maximum, a new pull
  request gets no environment.
- Each environment is a full copy of the workload of the copied environment. For example, take two services of 0.25 CPU
  and 256 MB, and a Postgres database of 0.25 CPU and 512 MB with a 10 GiB volume. Each pull request then costs 0.75
  CPU, 1 GB of memory, and 10 GiB of disk while awake. Five open pull requests cost 3.75 CPU, 5 GB, and 50 GiB.
- Geass builds every repository service of a copy when it creates the environment, and again for each push. Builds
  also use the cluster.
- The capacity check of **Deploy changes** does not run for copies. A copy that does not fit shows **Unavailable** with
  a capacity message. See [Usage and capacity](/docs/guides/usage-and-capacity/).

## Troubleshooting

**No environment appears when you open a pull request.** Check the conditions under
[What happens when a pull request opens](#what-happens-when-a-pull-request-opens). Then do these steps:

1. Open the GitHub App on GitHub.
2. Select **Advanced** → **Recent Deliveries**.
3. Find the `pull_request` delivery. Its response shows what Geass decided.

The response can show these results:

- `401` means that the webhook secret in Geass does not match the secret on GitHub. `503` means that Geass has no
  secret.
- `"environments":null` means that nothing matched. The feature is off, the pull request is from a fork, the project is
  linked to another installation, or no service builds this repository and branch.
- A `note` explains a skip, for example `the limit of 5 pull request environments is reached`,
  `an environment named pr-12 already exists`, or `the base environment does not exist`.

**No deliveries appear.** The app does not receive the **Pull request** event, or GitHub cannot reach the dashboard. To
check the webhook URL, click **Test GitHub App** in **Settings** → **Connectors**.

**The limit is reached.** Close a stale environment with **Close**, or raise **Open at once**. A refused pull request
does not get an environment later by itself. After you make room, push to the pull request or reopen it.

**The services never become Ready.** A repository service needs a successful build. Check the registry and
**Build Logs**. See [GitHub and builds](/docs/guides/github-and-builds/#troubleshooting). If the build succeeded, the
cluster might be out of capacity.

**The copy has no URL.** See [URLs](#urls).

**There is no comment on the pull request.** Make sure that **Report to GitHub** is on, and that the pull request is
under **Open pull requests**. Then click **Test GitHub App** to find a missing **Pull requests** permission.

**The comment shows an old state.** Geass writes the comment when a service changes state. If GitHub refuses, Geass
tries again after ten minutes. Look at the service in the dashboard for the current state.

**The first request to a copy is slow.** The copy was asleep. See [Sleep when idle](#sleep-when-idle).

**The copied database is empty.** This result is intended. Run your migrations and seed data when the service starts.

**The environment is still there after the pull request merged.** Geass did not receive the event. Check
**Recent Deliveries**, and then click **Close**.

**A closed pull request is still listed after you deleted its environment.** You used **Delete** in the list of
environments. Click **Close** on the entry under **Open pull requests**.

## Under the hood

The settings are `spec.prEnvironments` on the `GeassProject`, and the open pull requests are its
`status.pullRequests`. The copies are ordinary `GeassApp` and `GeassDatabase` objects with the label
`geass.dev/pull-request=<number>`.

```bash title="Inspect pull request environments"
kubectl -n geass-system get geassproject proj-1a2b3c4d -o jsonpath='{.status.pullRequests}{"\n"}'
kubectl -n geass-system get geassapps,geassdatabases -l geass.dev/pull-request=12
```

## Related

- [GitHub and builds](/docs/guides/github-and-builds/)
- [Projects and environments](/docs/guides/projects-and-environments/)
- [Connect GitHub](/docs/guides/connectors/github/)
- [Domains and TLS](/docs/guides/domains-and-tls/)
- [Usage and capacity](/docs/guides/usage-and-capacity/)
- [Sleep when idle](/docs/guides/scale-to-zero/)
- [Functions](/docs/guides/functions/)
