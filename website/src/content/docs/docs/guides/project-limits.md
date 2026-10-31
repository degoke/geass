---
title: "Project limits"
description: "Cap the CPU, memory, disk, and running copies that a project and each of its environments can reserve, so that one project cannot take the whole cluster."
---

Without limits, one project can reserve all the resources of the cluster. A runaway deploy, a service that someone scaled up by mistake, or too many pull request environments can leave nothing for the other projects. A **project limit** caps what a project can reserve. Limits are off until you set them.

You can cap two things, separately or together:

- **The whole project**: the total of all its environments, pull request environments included.
- **Each environment**: the most that any one environment can reserve.

## Before you start

- Only admins can set limits. Project tokens cannot, so a deploy token cannot raise its own cap.
- **Limits count what services reserve, not what they use.** An idle service counts in full. The [capacity check](/docs/guides/scaling-and-placement/#the-capacity-check) counts the same way.
- **An environment gets the lower of the two limits.** For example, the whole project has 8 CPUs, and each environment has 4. No environment can reserve more than 4, and all environments together cannot reserve more than 8.

## What you can limit

A field that you leave empty has no cap.

| Limit | What it caps | Example |
| --- | --- | --- |
| **CPU** | The total CPU that services and databases reserve | `4` or `500m` |
| **Memory** | The total memory that they reserve | `8Gi` |
| **Storage** | The total disk that their volumes reserve | `50Gi` |
| **Pods** | The number of copies that run or wait at the same time. Copies that finished do not count. | `20` |

## Set limits

1. Open the project.
2. Select **Settings**, and then **Limits**.
3. Under **Whole project**, **Each environment**, or both, enter the limits. To set no cap, leave a field empty.
4. Click **Save**. The dashboard shows "Limits saved".

The change applies immediately, without a deploy. Under **Whole project**, the page also shows what the project reserves now, for example "CPU 2500m of 8".

To remove the limits, empty all the fields, and click **Save**. The dashboard shows "Limits removed".

A limit must be more than zero. A pod count must be a whole number from 1 to 100000.

## How a whole-project limit is shared

Each environment can reserve what the other environments leave. For example:

| Project limit | `production` uses | `staging` uses | `production` can reserve | `staging` can reserve |
| --- | --- | --- | --- | --- |
| 8 CPUs | 3 | 1.5 | 6.5 | 5 |

When one environment uses more, the others get less room. Geass updates the room of each environment a short time after a service starts or stops.

Know these two things:

- **For a short time, the total can go over.** If two environments both start something at the same instant, they can pass the total together. Geass stops nothing that runs, and closes the room behind them. The limit of each environment is never passed. So if the total is strict, also set a limit for each environment.
- **A new environment gets what is left.** A new pull request environment might find little room. Its services wait until room is free.

## What happens at a limit

A limit stops **new** copies only. It does not stop anything that runs. If you lower a limit below what runs now, nothing stops. The next copy that does not fit cannot start.

- **A service that cannot start says why.** It shows **Unavailable**, with "The environment's resource quota is used up, so this service cannot start". The message also shows what the service asked for, what is used, and the limit. Geass continues to try, and the service starts when there is room.
- **To start the service, raise or remove the limit.** You can also stop something else in the project. The service can take a few minutes to start after that.
- **A deploy needs room for one more copy.** A deploy starts a new copy before it stops the old one. If the limit is exactly what runs now, the deploy waits. Leave some room above what the project runs.
- **A database or volume that does not fit also waits.** This happens when the project is at its storage limit.

### Services that reserve nothing

A CPU or memory limit refuses a copy that reserves nothing. Geass sets a reservation on every service and database that it creates. A container that another tool adds might reserve nothing. So when a project has a CPU or memory limit, Geass gives each environment a small default:

- A container that reserves no CPU counts as 50m.
- A container that reserves no memory counts as 64Mi.

The default is a reservation only, not a cap on use. So it never stops a container. A platform administrator can also apply the [noisy-neighbour limits](/docs/guides/tenant-limits/) of an organization, which can set other defaults.

## From the command line

Sign in as an admin. A limit that you do not name keeps its value. A limit that you give as empty is removed:

```bash
geass projects quota set morning-beach --project-cpu 8 --project-memory 16Gi --cpu 4
geass projects quota set morning-beach --memory ''     # remove the memory limit of each environment
geass projects quota show morning-beach                 # the limits, and what the project uses of its total
geass projects quota clear morning-beach                # remove every limit
```

An AI agent can read the limits with the `project_quota` tool. It can change them with `set_project_quota`, if its token has the admin role and the server is not read-only. See [MCP server](/docs/guides/mcp-server/).

A script calls `POST /api/projects/<project>/quota` with a token that has the admin role. The environment limits are `cpu`, `memory`, `storage`, and `pods`. The whole-project limits are `projectCpu`, `projectMemory`, `projectStorage`, and `projectPods`. See the [REST API](/docs/reference/rest-api/).

## What limits do not cover

- **Shared database servers, builds, and the object storage server.** These belong to the platform, not to a project. A database of a project on a shared server uses the room of the shared server. See [Shared database servers](/docs/guides/shared-database-servers/#limits-and-tradeoffs).
- **What a service uses.** A service that reserves little and uses a lot is not stopped. To see real use, open [Usage](/docs/guides/usage-and-capacity/) and the charts.
- **The cluster as a whole.** The capacity check still refuses a deploy that the servers cannot hold. Limits can add up to more than the cluster has. They are a ceiling for each project, not a reservation.

## Troubleshooting

**A service says that the resource quota is used up.**
The environment or the project is at its limit. The message names the resource that is short, for example `requests.cpu`. To give the service room, do one of these steps:

- Open **Settings** → **Limits**, and raise the limit.
- Stop something else in the project.

**A service still waits minutes after you raised the limit.**
Geass tries again with a delay that grows after each attempt. Wait a few minutes. To retry at once, see
[Under the hood](#under-the-hood).

**You set a limit, and nothing happened.**
A limit stops only new copies. Services that already run continue to run. They count against the limit from now on.

**One environment cannot start, but the project has room.**
The environment gets the lower of its own limit and what the other environments leave.

**A pull request environment does not start, but production runs.**
A pull request environment reserves the same as the environment that it copies. It counts against the whole-project limit. Raise the limit, or close another pull request.

**A database does not start after you set a storage limit.**
Its volume does not fit in the limit. Raise the storage limit.

## Under the hood

Geass keeps a ResourceQuota `geass-project-quota` and a LimitRange `geass-project-defaults` in the namespace of each environment. The quota holds the lower of the environment limit and what the others leave. Geass puts both back if you change them. The limits are in `spec.projectQuota` and `spec.environmentQuota` of the project. See [GeassProject](/docs/reference/resources/geass-project/).

```bash
kubectl describe resourcequota geass-project-quota -n morning-beach-production
kubectl describe limitrange geass-project-defaults -n morning-beach-production
kubectl get events -n morning-beach-production --sort-by=.lastTimestamp     # a refused volume says "exceeded quota"
kubectl -n morning-beach-production rollout restart deployment <service>  # try a refused service immediately
```

## Related

- [Usage and capacity](/docs/guides/usage-and-capacity/)
- [Projects and environments](/docs/guides/projects-and-environments/)
- [Pull request environments](/docs/guides/pull-request-environments/)
- [Network isolation](/docs/guides/network-isolation/)
