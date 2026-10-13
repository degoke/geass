---
title: "Pull-request environments"
description: "Get a temporary copy of an environment for each pull request: how GitHub events create it, what is copied, when it goes, and its cost."
---

A **pull request environment** is a short-lived copy of one of your environments that Geass creates when someone opens a
pull request, and deletes when the pull request closes. The copy runs the pull request's branch, so reviewers can try
the change before it merges. The feature is off until you turn it on for a project.

It is driven by GitHub webhooks: Geass learns about pull requests from the `pull_request` event that your GitHub App
sends to the dashboard.

## Before you start

- A project linked to GitHub, with a repository service in the environment you want to copy. See
  [GitHub and builds](/docs/guides/github-and-builds/).
- A GitHub App that is subscribed to the **Pull request** event and can read pull requests. The app Geass registers for you
  already is. If you created the app yourself, check **Permissions & events** in the app's settings in GitHub.
- A dashboard address that GitHub can reach over HTTPS, because the webhook is delivered to `<dashboard URL>/webhooks/github`.
  See [Domains and TLS](/docs/guides/domains-and-tls/).
- Spare capacity for the copies (see [Limits and cost](#limits-and-cost)).

## Turn it on

1. Open the project's **Settings** → **Environments**. The **PR Environments** block is below the list of environments.
2. Turn on **Create an environment for each pull request**.
3. Under **Copy this environment**, choose the environment to copy. It is the one whose services build your repository. The
   list leaves out environments that are themselves pull request environments.
4. Set **Open at once** to the most pull request environments that may exist together, from 1 to 25. The default is 5.
5. Choose **Save**. A toast says "PR environments are on".

If the project is not linked to GitHub, the block tells you to link it first under **Integrations**.

Turning the switch off stops new environments. Environments that already exist are still cleaned up when their pull
requests close.

Through the API, with a workspace admin token:

```bash title="Turn on pull request environments"
curl -X POST https://geass.example.com/api/projects/proj-1a2b3c4d/pr-environments/save \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d enabled=on -d baseEnvironment=production -d maxOpen=5
```

## What happens when a pull request opens

GitHub sends a `pull_request` event. Geass creates or refreshes an environment for the actions `opened`, `reopened`,
`ready_for_review` and `synchronize`, so a draft pull request gets one as well. It does so only when **all** of these hold:

- PR environments are on for the project, and the event came from the GitHub installation the project is linked to.
- The pull request is **not from a fork**. Its head and base repository must both be the repository in the event. Code
  from a fork never runs next to your project's secrets.
- The base environment has a service that builds this repository, on the branch the pull request targets. A pull request
  into `main` gets an environment only if a service in the base environment builds `main` of that repository.
- The limit in **Open at once** is not reached.
- No other environment already uses the name.

Then Geass:

1. **Creates the environment** and adds it to the project. A tab appears in the top bar with a **PR #12** badge, and the
   pull request is listed under **Open pull requests**.
2. **Copies the shared variables** of the base environment into it. Secret values are copied too.
3. **Copies the databases and services** (see below) and deploys them at once. They are not drafts, and you do not choose
   **Deploy changes**.

Later events for the same pull request only create pieces that are missing. They do not change what exists.

New commits on the pull request's branch update the copy. GitHub sends a `push` event for each push, and Geass re-runs the
build of every repository service in the copy, because they follow the pull request's branch. The services roll to the new
image when their builds succeed. See [Branch deploys and push triggers](/docs/guides/github-and-builds/#branch-deploys-and-push-triggers).
The `synchronize` event that comes with the push only creates what is missing, and does not change what exists. A
service's **Watch paths** apply here too, so a push that changes only files outside them does not rebuild it.

### Naming

| Object | Name |
| --- | --- |
| The environment | `pr-<number>`, for example `pr-12`. If another repository in the same project already has a pull request with that number, `pr-12-<repository name>`. |
| A cloned service or database | The original's ID plus `-pr<number>`, for example `amber-fern-pr12`. The display name is the original's. |
| The namespace | `<project ID>-<environment>`, for example `proj-1a2b3c4d-pr-12`. |

### What is copied

| Copied | How it changes in the copy |
| --- | --- |
| In-cluster databases (Postgres, MySQL, Redis, SQLite) | Same engine, version, size and storage. A single instance, with no high availability. **Empty**: no data and no restore. Backups are off. |
| Services, functions and image services | The same settings and variables. One replica, no autoscaling. A copy of the service's secrets. References to a copied database point at the copy. The copy [sleeps when idle](#sleep-when-idle) unless the original sets that itself. |
| Repository services | A repository service that builds the pull request's repository follows the pull request's branch. A service for another repository keeps its branch. No copy keeps a pinned commit. |
| Shared variables | Copied, including secret values. |

Not copied:

- Buckets, logical databases, and databases outside the cluster (AWS RDS, PlanetScale). A copied service that
  references one of them keeps pointing at the original.
- Domains. Each environment gets its own address, and the copy has no custom domain.
- Backups and restore points. The copied databases are throwaway.
- A function's schedules. A copy in every pull request would repeat whatever the schedule does (mail, writes to a shared
  store) once per open pull request. The rest of the function, its code and its mode, is copied, and the original keeps
  its schedules. See [Functions](/docs/guides/functions/#schedules).

### URLs

The copy of a service has no domain of its own. How you reach it depends on your setup:

- With Cloudflare connected, a service without a domain gets a default one, so the copy is reachable at
  `<service ID>-<project ID>-<environment>.<your domain>`, for example `amber-fern-pr12-proj-1a2b3c4d-pr-12.example.com`.
  See [Domains and TLS](/docs/guides/domains-and-tls/#default-hostnames).
- Otherwise, set a **Custom domain** on the copied service under its **Settings** → **Networking**, in the pull request's
  environment.
- Other services in the environment reach it at its private address, shown in the same section.

A copy without a public host cannot [sleep when idle](#sleep-when-idle), because it is the request to a host that wakes it.

The address Geass stores for a pull request, and shows as a link in **Open pull requests**, is the pull request on GitHub, not
a running service.

## Sleep when idle

Most open pull requests are idle most of the time. So Geass turns on **Sleep when idle** for every service it copies, with
the default idle time of 5 minutes, unless the original service has its own `scaleToZero` setting, which the copy then
keeps (even when it is off). A copy that sleeps has no pods until a request to its host starts it. It takes no CPU or
memory while it waits, and the first request after a sleep waits a few seconds while it starts.

A copy sleeps only when the cluster's serverless add-on is on and the copy has a public host. Otherwise it runs always on, as
before, and its `ScaleToZero` condition says why. See [Sleep when idle](/docs/guides/scale-to-zero/).

## What happens when a pull request closes

When the pull request is closed or merged, Geass deletes the environment: its services and databases and their data, its
shared variables, and the API tokens that were limited to it. The tab and the entry under **Open pull requests**
go away.

To remove one earlier, open **Settings** → **Environments** → **Open pull requests** and choose **Close** beside it.
Confirm in **Close pull request environment?**. The pull request on GitHub is not changed. Use **Close** rather than the
**Delete** button in the list of environments: **Close** also clears the pull request from the list.

```bash title="Close a pull request environment through the API"
curl -X POST https://geass.example.com/api/projects/proj-1a2b3c4d/pr-environments/close \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d number=12 -d repository=acme/storefront
```

## Limits and cost

- **Open at once** caps the number of environments, 5 by default and 25 at most. When the cap is reached, a new pull request
  gets no environment (see [Troubleshooting](#troubleshooting)).
- Every environment is a full copy of the base environment's workload, and a copy that is awake takes the whole of it. A
  base environment with two services at 0.25 CPU
  and 256 MB each, and a Postgres database at 0.25 CPU and 512 MB with a 10 GiB volume, costs 0.75 CPU, 1 GB of memory
  and a 10 GiB volume for each pull request. Five open pull requests take 3.75 CPU, 5 GB and 50 GiB of volumes.
- Every repository service in a copy is built when the environment is created, and again for each push to the pull
  request's branch. Builds run in `geass-system` and use the cluster too.
- The usual capacity check that stops **Deploy changes** does not run for these copies. A service that does not fit
  reports **Unavailable** with a capacity message instead. See [Usage and capacity](/docs/guides/usage-and-capacity/).
- Pull requests from forks never get an environment.

## What happens under the hood

- The settings are `spec.prEnvironments` on the `GeassProject` (`enabled`, `baseEnvironment`, `maxOpen`). The open pull
  requests are `status.pullRequests`, each with its number, repository, branch, environment, the pull request's URL and the
  time it opened.
- Copies are ordinary `GeassApp` and `GeassDatabase` objects labelled `geass.dev/pull-request=<number>`, deployed on
  creation. A copy of a service has `spec.scaleToZero.enabled: true` unless the original has a `scaleToZero` setting, and a
  copy of a function has no `triggers`.
- The environment is an ordinary entry in `spec.environments`, so everything in
  [Projects and environments](/docs/guides/projects-and-environments/) applies to it.
- The answer to GitHub's webhook lists what Geass did, for example `{"accepted":true,"environments":[{"project":"proj-1a2b3c4d","action":"ensure","environment":"pr-12","created":3}]}`.

```bash title="Inspect pull request environments"
kubectl -n geass-system get geassproject proj-1a2b3c4d -o jsonpath='{.status.pullRequests}{"\n"}'
kubectl -n geass-system get geassapps,geassdatabases -l geass.dev/pull-request=12
kubectl -n proj-1a2b3c4d-pr-12 get pods
```

## Troubleshooting

**No environment appears when I open a pull request.** Work down the list in
[What happens when a pull request opens](#what-happens-when-a-pull-request-opens). Then open the GitHub App in GitHub,
choose **Advanced** → **Recent Deliveries**, and find the `pull_request` delivery. Its response shows what Geass decided:

- `401` means the webhook secret in Geass does not match GitHub's; `503` means none is stored.
- `"environments":null` means nothing matched: the feature is off, the pull request is from a fork, the project is linked
  to another installation, or no service in the base environment builds this repository and branch.
- A `note` explains a skip: `the limit of 5 pull request environments is reached`, `an environment named pr-12 already
  exists` or `the base environment does not exist`.

**The deliveries are missing entirely.** The app is not subscribed to **Pull request**, or GitHub cannot reach the dashboard.
Use **Test GitHub App** in **Settings** → **Connectors** to check the webhook URL.

**The limit is reached.** Close a stale environment with **Close**, or raise **Open at once** (up to 25) and save. A pull
request that was refused does not get an environment later by itself: push to it or reopen it after you make room.

**The environment exists but its services never become Ready.** A repository service needs a successful build, so check
the registry and **Build Logs** ([GitHub and builds](/docs/guides/github-and-builds/#troubleshooting)). Otherwise the cluster may
be out of capacity ([Usage and capacity](/docs/guides/usage-and-capacity/)).

**The copy has no URL.** Copies get no domain of their own. See [URLs](#urls).

**The first request to a copy is slow.** The copy was asleep. See [Sleep when idle](#sleep-when-idle).

**The copied database is empty.** It is meant to be: the copy starts with no data. Run your migrations and seed data when
the service starts.

**The environment is still there after the pull request merged.** Geass did not receive the `closed` event. Check
**Recent Deliveries**, then use **Close**.

**A closed pull request is still listed after I deleted its environment.** You used **Delete** in the list of environments.
Choose **Close** on the entry under **Open pull requests** to clear it.

## Related

- [GitHub and builds](/docs/guides/github-and-builds/)
- [Projects and environments](/docs/guides/projects-and-environments/)
- [Connect GitHub](/docs/guides/connectors/github/): registering the GitHub App.
- [Domains and TLS](/docs/guides/domains-and-tls/)
- [Usage and capacity](/docs/guides/usage-and-capacity/)
- [Sleep when idle](/docs/guides/scale-to-zero/)
- [Functions](/docs/guides/functions/)
