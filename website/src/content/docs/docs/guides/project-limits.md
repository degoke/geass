---
title: "Project limits"
description: "Cap the CPU, memory, disk and pods a whole project and each of its environments can ask for, so one project cannot use all the room in the cluster."
---

Without limits, one project can ask for everything the cluster has. A runaway deploy, a service scaled up by mistake or a pull request environment copied one time too many leaves nothing for the other projects. A **project limit** (Kubernetes calls it a resource quota) caps what a project can ask for. It is off until you set it, and a project that never sets one is not capped.

You can cap two things, separately or together:

- **The whole project**: the most the project can ask for, added up over all its environments, pull request environments included.
- **Each environment**: the most any one environment can ask for.

## Before you start

- Only admins can set limits: dashboard admins, and workspace API tokens with the admin role. Project tokens are refused, so a deploy token cannot raise its own cap.
- **The limits count what services ask for, not what they use.** They count requests, which is what the scheduler and the [capacity check](/docs/guides/usage-and-capacity/) count too. A service that sits idle still counts in full.
- **An environment is held to the lower of the two limits.** With a whole-project limit of 8 CPUs and an environment limit of 4, no environment can ask for more than 4, and all of them together cannot ask for more than 8.

## What you can limit

You can set any of these, for either group. A field you leave empty is not capped.

| Limit | What it caps | Example |
| --- | --- | --- |
| **CPU** | The total CPU that pods ask for | `4` or `500m` |
| **Memory** | The total memory that pods ask for | `8Gi` |
| **Storage** | The total disk that volumes ask for, across every storage class | `50Gi` |
| **Pods** | How many pods can be running or waiting at once. Finished pods do not count | `20` |

A pod count also bounds how many pod objects one environment or project can create, whatever size they are.

## Set limits

1. Open the project and choose **Settings**, then **Limits**.
2. Fill in the limits you want under **Whole project**, **Each environment** or both. Leave a field empty for no cap.
3. Choose **Save**. The dashboard shows "Limits saved". The change applies at once and needs no redeploy.

Under **Whole project** the page also shows what the project asks for now, for each resource you capped, for example "CPU 2500m of 8".

To remove the limits, empty every field and choose **Save**. The dashboard shows "Limits removed". There is no way to cap a project at zero: an amount has to be above zero, and a pod count has to be a whole number from 1 to 100000.

## How a whole-project limit is shared

Kubernetes counts usage per namespace, and every environment is a namespace. It has no cap that spans several, so Geass keeps the environments' own caps in step with the total. Each environment may ask for **what the others leave**:

| Project limit | Used by `production` | Used by `staging` | `production` may ask for | `staging` may ask for |
| --- | --- | --- | --- | --- |
| 8 CPUs | 3 | 1.5 | 6.5 | 5 |

An environment can ask for what is left after the **others**, not after itself, because the total counts what it uses once. When an environment uses more, the room of the others shrinks. When it gives room back, theirs grows. Geass follows this within a moment of a pod starting or stopping.

Two things to know about it:

- **It is enforced by following, not by a lock.** Between a pod starting and the caps being rewritten, which takes a moment, another environment can start something too. Two environments that each ask for most of what is left at the same instant can briefly go past the total. Nothing that is running is stopped when that happens, and the caps close behind it. A cap on each environment is enforced by Kubernetes itself and never goes past, so set one as well if the total must not be exceeded by much.
- **A new environment gets what is left.** A pull request environment that is created while the others use most of the total finds little room, and its services wait until some is freed.

## What happens at a limit

A limit stops **new** pods. Nothing that is running is stopped, and lowering a limit below what is already running stops nothing. The next pod that would take an environment, or the project, over a cap is refused.

- **A service that cannot start says why.** It shows **Unavailable**, and its `Ready` condition reads "The environment's resource quota is used up, so this service cannot start", followed by what it asked for, what is used and the limit. It is a wait and not a failure: Geass keeps retrying, and the service starts by itself once there is room.
- **Raising the limit, or removing it, is all it takes.** Nothing about the service changes. Kubernetes retries the pod with a delay that grows, so a service can take up to a few minutes to notice. Stopping something else in the project frees room the same way.
- **An update needs room for one more copy.** A rolling update starts a new copy before it stops an old one. A limit set exactly at what a service already asks for leaves no room, so the update waits. Leave some headroom above what the project runs.
- **A volume that does not fit is refused too.** A database or a service with storage that would take the project over its storage limit waits for room.

### Containers that ask for nothing

A limit on CPU or memory refuses every pod that does not say how much it asks for. Geass sets a request on the services and databases it creates. A sidecar or an init container that something else adds might not. So when a project has a CPU or memory limit, each environment also gets a small default: a container that asks for no CPU is counted as asking for 50m, and one that asks for no memory as asking for 64Mi. Only a request is defaulted, never a limit, so the default does not start killing a container that never had one. A limit on storage or pods alone needs no defaults, and Geass adds none.

The project of an organization can also be held to its organization's [noisy-neighbour limits](/docs/guides/tenant-limits/), which a platform administrator sets: they can give containers a default request of their own, a default limit and a most, and they share the same LimitRange.

## Change it with a manifest or the API

The limits live in the project's `spec`. The fragment below shows only the limits. Add it to your project's full manifest, or use the `kubectl patch` that follows, rather than applying the fragment alone:

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
| `spec.projectQuota.cpu`, `memory`, `storage`, `pods` | The most the whole project's pods and volumes may ask for, as Kubernetes quantities (`8`, `500m`, `16Gi`), and a pod count of at least 1. | No cap |
| `spec.environmentQuota.cpu`, `memory`, `storage`, `pods` | The same, for each environment. | No cap |

With the command line, signed in as an admin. A limit that you do not name keeps its value, and one you give empty is removed:

```bash
geass projects quota set morning-beach --project-cpu 8 --project-memory 16Gi --cpu 4
geass projects quota set morning-beach --memory ''     # remove the per-environment memory limit
geass projects quota show morning-beach                 # the limits, and what the project uses of its total
geass projects quota clear morning-beach                # remove every limit
```

An AI agent connected through [the MCP server](/docs/guides/mcp-server/) can read the limits with `project_quota` and change them with `set_project_quota`, if its token is an admin's and the server is not read-only.

With `kubectl`:

```bash
kubectl -n geass-system patch geassproject morning-beach --type merge \
  -p '{"spec":{"projectQuota":{"cpu":"8","memory":"16Gi"},"environmentQuota":{"cpu":"4"}}}'
```

To remove them: `kubectl -n geass-system patch geassproject morning-beach --type merge -p '{"spec":{"projectQuota":null,"environmentQuota":null}}'`.

Through the API, with a workspace token that has the admin role. The environment limits are `cpu`, `memory`, `storage` and `pods`, and the whole-project limits are `projectCpu`, `projectMemory`, `projectStorage` and `projectPods`. **A limit that you do not send keeps its value. A limit that you send empty is removed:**

```bash
curl -sS -X POST https://geass.example.com/api/projects/morning-beach/quota \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d projectCpu=8 -d projectMemory=16Gi -d cpu=4 -d memory=
```

That sets the project's CPU and memory limits and the environments' CPU limit, and removes the environments' memory limit. It leaves everything else as it was.

`kubectl` does not check the amounts. The dashboard and the API refuse an amount that is not above zero, and say which field.

## What limits do not cover

- **Shared database servers, builds and the object storage server.** They run in `geass-system`, which no project limit applies to. A project's own logical databases on a shared server take their room from the server. See [Shared database servers](/docs/guides/shared-database-servers/#limits-and-tradeoffs).
- **What a service uses.** The limits count requests. A service that asks for little and uses a lot is not stopped by them. Use [Usage](/docs/guides/usage-and-capacity/) and the charts to see real use.
- **The cluster as a whole.** Limits cap one project. The [capacity check](/docs/guides/usage-and-capacity/) still refuses a deploy that the nodes cannot hold, whatever any limit says. Limits that add up to more than the cluster has are allowed: they are a ceiling per project, and not a reservation.

## Check the limits

```bash
kubectl describe resourcequota geass-project-quota -n morning-beach-production
kubectl describe limitrange geass-project-defaults -n morning-beach-production
kubectl -n geass-system get geassproject morning-beach -o jsonpath='{.spec.projectQuota}{"\n"}{.spec.environmentQuota}{"\n"}{.status.quotaUsage}{"\n"}'
```

`describe resourcequota` lists what the environment has used against each limit. With a whole-project limit, the number it shows as the limit is what the environment may ask for now, which changes as the others do. `status.quotaUsage` on the project is what all its environments ask for together.

## What happens under the hood

The project controller writes and keeps two objects in each environment's namespace, and removes them when the project stops asking. If you edit or delete one by hand, the controller puts it back.

| Object | Name | What it does |
| --- | --- | --- |
| ResourceQuota | `geass-project-quota` | `requests.cpu`, `requests.memory`, `requests.storage` and `pods`, for each limit that is set. The value is the environment's own limit, or what the others leave of the project's limit, whichever is lower. |
| LimitRange | `geass-project-defaults` | A default request of 50m CPU and 64Mi memory for a container that asks for none, only for the resources that are capped. Absent when only storage or pods are capped. With the organization's [noisy-neighbour limits](/docs/guides/tenant-limits/) it also holds their default requests, default limits and most, which take the place of these for what they name. |

For a project with a whole-project limit the controller also watches each environment's quota for what it uses, and works the others' caps out again when it changes. A project with only environment limits is not woken by usage, which changes with every pod.

Geass removes only objects that carry its own `geass.dev/managed-by` label. A ResourceQuota that you made yourself with one of these names is left alone.

## Troubleshooting

**A service says the resource quota is used up.**
The environment, or the project, is at its limit. Open **Settings**, then **Limits**, and raise it, or stop something else in the project. The message names the resource that is short, for example `requests.cpu`, and shows what was asked for, what is used and the limit.

**A service has been waiting for minutes after I raised the limit.**
Kubernetes retries a refused pod with a growing delay. Wait a few minutes. To try at once, run `kubectl -n <project>-<environment> rollout restart deployment <service>`.

**I set a limit and nothing happened.**
A limit stops new pods only. Services that were already running keep running, and are counted against it from now on.

**One environment cannot start, but the project has room.**
It is held to the lower of its own limit and what the others leave. Check `kubectl describe resourcequota geass-project-quota -n <project>-<environment>`: the limit shown is the lower of the two.

**A pull request environment will not start, but production is fine.**
A pull request environment is a copy of its base environment, so it asks for as much, and it counts against the whole-project limit like any other environment. If the room is tight, the copy does not fit. Raise the limit, or close another pull request.

**A database does not start after I set a storage limit.**
Its volume was refused. Look at the events in the environment: `kubectl get events -n <project>-<environment> --sort-by=.lastTimestamp`. A refusal reads "exceeded quota".

## Related

- [Usage and capacity](/docs/guides/usage-and-capacity/)
- [Projects and environments](/docs/guides/projects-and-environments/)
- [Pull request environments](/docs/guides/pull-request-environments/)
- [Network isolation](/docs/guides/network-isolation/)
