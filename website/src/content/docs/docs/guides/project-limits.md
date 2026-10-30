---
title: "Project limits"
description: "Cap the CPU, memory, disk, and pods that a project and each of its environments can ask for, so that one project cannot use all of the cluster."
---

Without limits, one project can ask for all of the resources that the cluster has. A runaway deploy, a service that
someone scaled up by mistake, or one pull request environment too many leaves nothing for the other projects. A
**project limit** (Kubernetes calls it a resource quota) caps what a project can ask for. Project limits are off until
you set them. A project that never sets a limit has no cap.

You can cap two things, separately or together:

- **The whole project**: the most that the project can ask for, added up over all of its environments. This total
  includes pull request environments.
- **Each environment**: the most that any one environment can ask for.

## Before you start

- Only admins can set limits. Admins are dashboard admins and workspace API tokens with the admin role. Geass refuses
  project tokens, so a deploy token cannot raise its own cap.
- **The limits count what services ask for, not what they use.** They count requests. The scheduler and the
  [capacity check](/docs/guides/usage-and-capacity/) also count requests. A service that is idle still counts in full.
- **An environment is held to the lower of the two limits.** For example, the whole-project limit is 8 CPUs and the
  environment limit is 4. No environment can ask for more than 4, and all of the environments together cannot ask for
  more than 8.

## What you can limit

You can set any of these limits, for either group. A field that you leave empty has no cap.

| Limit | What it caps | Example |
| --- | --- | --- |
| **CPU** | The total CPU that pods ask for | `4` or `500m` |
| **Memory** | The total memory that pods ask for | `8Gi` |
| **Storage** | The total disk that volumes ask for, across all storage classes | `50Gi` |
| **Pods** | The number of pods that can be running or waiting at the same time. Finished pods do not count | `20` |

A pod count also limits the number of pod objects that one environment or project can create, whatever their size.

## Set limits

1. Open the project.
2. Select **Settings**, and then select **Limits**.
3. Enter the limits that you want under **Whole project**, **Each environment**, or both. To set no cap, leave a field
   empty.
4. Click **Save**. The dashboard shows "Limits saved". The change applies immediately. You do not need to redeploy.

Under **Whole project**, the page also shows what the project asks for now, for each resource that you capped. For
example: "CPU 2500m of 8".

To remove the limits, empty all of the fields and click **Save**. The dashboard shows "Limits removed".

You cannot cap a project at zero. An amount must be more than zero. A pod count must be a whole number from 1 to
100000.

## How a whole-project limit is shared

Kubernetes counts usage per namespace, and each environment is a namespace. Kubernetes has no cap that spans more than
one namespace. So Geass keeps the caps of the environments in step with the total. Each environment can ask for
**what the others leave**:

| Project limit | Used by `production` | Used by `staging` | `production` may ask for | `staging` may ask for |
| --- | --- | --- | --- | --- |
| 8 CPUs | 3 | 1.5 | 6.5 | 5 |

An environment can ask for what is left after the **others**, not after itself. The reason is that the total counts
what the environment uses one time only. When an environment uses more, the room for the other environments becomes
smaller. When an environment gives room back, the room for the others becomes larger. Geass updates the caps a short
time after a pod starts or stops.

Know these two things about the shared limit:

- **Geass enforces the total by following usage, not with a lock.** A short time passes between when a pod starts and
  when Geass rewrites the caps. In that time, another environment can also start something. If two environments each
  ask for most of what is left at the same instant, they can go above the total for a short time. When that happens,
  Geass does not stop anything that is running, and the caps close behind it. Kubernetes enforces a cap on each
  environment directly, and that cap is never exceeded. So if the total must not be exceeded by much, also set a cap
  on each environment.
- **A new environment gets what is left.** If you create a pull request environment while the others use most of the
  total, the new environment finds little room. Its services wait until room becomes free.

## What happens at a limit

A limit stops **new** pods. A limit does not stop anything that is running. If you lower a limit below what is already
running, Geass stops nothing. Kubernetes refuses the next pod that would take an environment, or the project, over a
cap.

- **A service that cannot start says why.** The service shows **Unavailable**. Its `Ready` condition reads "The
  environment's resource quota is used up, so this service cannot start". After that message, the condition shows
  what the service asked for, what is used, and the limit. This state is a wait and not a failure. Geass continues to
  retry, and the service starts automatically when there is room.
- **To start the service, raise the limit or remove it.** You do not change anything about the service. Kubernetes
  retries the pod with a delay that increases, so a service can take up to a few minutes to start. If you stop
  something else in the project, you free room in the same way.
- **An update needs room for one more copy.** A rolling update starts a new copy before it stops an old one. If you set
  a limit at exactly what a service already asks for, there is no room, so the update waits. Leave some headroom above
  what the project runs.
- **Kubernetes also refuses a volume that does not fit.** If a database or a service with storage would take the
  project over its storage limit, that database or service waits for room.

### Containers that ask for nothing

A limit on CPU or memory refuses each pod that does not say how much it asks for. Geass sets a request on the services
and databases that it creates. A sidecar or an init container that another tool adds might not have a request. So when
a project has a CPU or memory limit, Geass also gives each environment a small default:

- A container that asks for no CPU counts as a request for 50m.
- A container that asks for no memory counts as a request for 64Mi.

Geass sets only a default request, never a default limit. So the default does not cause Geass to kill a container that
never had a limit. A limit on storage or pods alone needs no defaults, and Geass adds none.

A platform administrator can also hold the project of an organization to its organization's
[noisy-neighbour limits](/docs/guides/tenant-limits/). These limits can give containers their own default request, a
default limit, and a maximum. They share the same LimitRange.

## Change it with a manifest or the API

The limits are in the project's `spec`. The fragment below shows only the limits. Do not apply the fragment alone. Add
it to the full manifest of your project, or use the `kubectl patch` command that follows:

```yaml title="project-limits.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassProject
metadata:
  name: morning-beach
  namespace: geass-system
spec:
  projectQuota:           # the whole project, added up over its environments
    cpu: "8"
    memory: 16Gi
    storage: 200Gi
    pods: 60
  environmentQuota:       # each environment
    cpu: "4"
    memory: 8Gi
    storage: 50Gi
    pods: 20
```

| Field | Meaning | Default |
| --- | --- | --- |
| `spec.projectQuota.cpu`, `memory`, `storage`, `pods` | The most that the pods and volumes of the whole project can ask for, as Kubernetes quantities (`8`, `500m`, `16Gi`). A pod count is at least 1. | No cap |
| `spec.environmentQuota.cpu`, `memory`, `storage`, `pods` | The same, for each environment. | No cap |

To use the command line, sign in as an admin. A limit that you do not name keeps its value. A limit that you give as
empty is removed:

```bash
geass projects quota set morning-beach --project-cpu 8 --project-memory 16Gi --cpu 4
geass projects quota set morning-beach --memory ''     # remove the per-environment memory limit
geass projects quota show morning-beach                 # the limits, and what the project uses of its total
geass projects quota clear morning-beach                # remove every limit
```

An AI agent that is connected through [the MCP server](/docs/guides/mcp-server/) can read the limits with
`project_quota`. It can change them with `set_project_quota` if its token is an admin token and the server is not
read-only.

To use `kubectl`:

```bash
kubectl -n geass-system patch geassproject morning-beach --type merge \
  -p '{"spec":{"projectQuota":{"cpu":"8","memory":"16Gi"},"environmentQuota":{"cpu":"4"}}}'
```

To remove the limits, run
`kubectl -n geass-system patch geassproject morning-beach --type merge -p '{"spec":{"projectQuota":null,"environmentQuota":null}}'`.

To use the API, use a workspace token that has the admin role. The environment limits are `cpu`, `memory`, `storage`,
and `pods`. The whole-project limits are `projectCpu`, `projectMemory`, `projectStorage`, and `projectPods`. 
**A limit that you do not send keeps its value. A limit that you send empty is removed:**

```bash
curl -sS -X POST https://geass.example.com/api/projects/morning-beach/quota \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d projectCpu=8 -d projectMemory=16Gi -d cpu=4 -d memory=
```

That request sets these values:

- It sets the CPU and memory limits of the project.
- It sets the CPU limit of the environments.
- It removes the memory limit of the environments.
- It does not change the other limits.

`kubectl` does not check the amounts. The dashboard and the API refuse an amount that is not more than zero, and they
say which field is wrong.

## What limits do not cover

- **Shared database servers, builds, and the object storage server.** They run in `geass-system`, and no project limit
  applies to that namespace. The logical databases of a project on a shared server take their room from the server.
  See [Shared database servers](/docs/guides/shared-database-servers/#limits-and-tradeoffs).
- **What a service uses.** The limits count requests. If a service asks for little and uses a lot, the limits do not
  stop it. To see real use, use [Usage](/docs/guides/usage-and-capacity/) and the charts.
- **The cluster as a whole.** Limits cap one project. The [capacity check](/docs/guides/usage-and-capacity/) still
  refuses a deploy that the nodes cannot hold, whatever any limit says. Limits that add up to more than the cluster
  has are allowed. They are a ceiling for each project, not a reservation.

## Check the limits

```bash
kubectl describe resourcequota geass-project-quota -n morning-beach-production
kubectl describe limitrange geass-project-defaults -n morning-beach-production
kubectl -n geass-system get geassproject morning-beach -o jsonpath='{.spec.projectQuota}{"\n"}{.spec.environmentQuota}{"\n"}{.status.quotaUsage}{"\n"}'
```

`describe resourcequota` lists what the environment uses against each limit. With a whole-project limit, the number
that it shows as the limit is what the environment can ask for now. That number changes when the other environments
change. `status.quotaUsage` on the project is what all of its environments ask for together.

## What happens under the hood

The project controller writes and keeps two objects in the namespace of each environment. The controller removes them
when the project no longer asks for limits. If you edit or delete one of these objects manually, the controller puts it
back.

| Object | Name | What it does |
| --- | --- | --- |
| ResourceQuota | `geass-project-quota` | `requests.cpu`, `requests.memory`, `requests.storage`, and `pods`, for each limit that is set. The value is the lower of two values: the limit of the environment, or what the others leave of the project limit. |
| LimitRange | `geass-project-defaults` | A default request of 50m CPU and 64Mi memory for a container that asks for none, only for the resources that are capped. Not present when only storage or pods are capped. With the organization's [noisy-neighbour limits](/docs/guides/tenant-limits/), it also holds their default requests, default limits, and maximum. These values replace the Geass defaults for the resources that they name. |

For a project with a whole-project limit, the controller also watches the quota of each environment for usage. When
the usage changes, the controller calculates the caps of the other environments again. A change in usage does not wake
the controller for a project with only environment limits, because usage changes with each pod.

Geass removes only objects that have its own `geass.dev/managed-by` label. If you made a ResourceQuota yourself with
one of these names, Geass does not change it.

## Troubleshooting

**A service says the resource quota is used up.**
The environment, or the project, is at its limit. To give the service room, do one of these:

- Open **Settings**, select **Limits**, and raise the limit.
- Stop something else in the project.

The message names the resource that is short, for example `requests.cpu`. It also shows what the service asked for,
what is used, and the limit.

**A service has been waiting for minutes after I raised the limit.**
Kubernetes retries a refused pod with a delay that increases. Wait a few minutes. To try immediately, run
`kubectl -n <project>-<environment> rollout restart deployment <service>`.

**I set a limit and nothing happened.**
A limit stops only new pods. Services that were already running continue to run. From now on, they count against the
limit.

**One environment cannot start, but the project has room.**
The environment is held to the lower of its own limit and what the others leave. Run
`kubectl describe resourcequota geass-project-quota -n <project>-<environment>`. The limit that it shows is the lower
of the two.

**A pull request environment does not start, but production is fine.**
A pull request environment is a copy of its base environment, so it asks for the same amount. It counts against the
whole-project limit like all other environments. If the room is small, the copy does not fit. Raise the limit, or
close another pull request.

**A database does not start after I set a storage limit.**
Kubernetes refused its volume. Look at the events in the environment:
`kubectl get events -n <project>-<environment> --sort-by=.lastTimestamp`. A refusal reads "exceeded quota".

## Related

- [Usage and capacity](/docs/guides/usage-and-capacity/)
- [Projects and environments](/docs/guides/projects-and-environments/)
- [Pull request environments](/docs/guides/pull-request-environments/)
- [Network isolation](/docs/guides/network-isolation/)
