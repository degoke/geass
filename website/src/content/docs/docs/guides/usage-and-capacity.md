---
title: "Usage and capacity"
description: "Read the Usage page: node health, how much CPU, memory and storage each project reserves, and what to do when a node reports pressure or the cluster is full."
---

The **Usage** page answers two questions: how much of your servers is already spoken for, and which machines need attention. It lists every node with its health, totals the CPU and memory that services ask for, and breaks that down by project and by region. Open it before you create a large service or database, and whenever a service refuses to deploy for lack of capacity.

![The Usage page.](../../../../assets/docs/usage.webp)
*Screenshot with example data.*

## Before you start

- Every signed-in user can read the page. Only admins see the **Edit** button on a node (see [Scaling and placement](/docs/guides/scaling-and-placement/)), **Add server** above the table, and **Remove** on a server that was added on its own.
- The numbers describe what services reserve, not what they use at this moment. Live use needs the monitoring add-on, which the installer turns on (see [Logs, metrics and terminal](/docs/guides/observability/)).

## Open the page

Choose **Usage** in the left rail, or **Settings** and then **Usage**. Both open the same page. The address is `/cluster` on your dashboard host.

From top to bottom the page shows:

| Section | What it shows |
| --- | --- |
| **Summary** | CPU available, memory available and healthy nodes for the whole cluster. |
| **Capacity and node pressure** | One line for each node problem and each shortage. Only present when there is something to report. |
| **Network isolation** | Whether the cluster's network plugin acts on network policies, which project isolation depends on. Present once the first check has run (see [Network isolation](/docs/guides/network-isolation/#check-that-the-cluster-enforces-policies)). |
| **Version** | Which Geass release and which K3s the cluster runs, and how an upgrade of the workers' K3s is going while one is asked for. |
| **Nodes** | One row per node, with its role, the pool it belongs to, the K3s version it runs, region, tags, size, pressure and status. **Add server** is above it. |
| **Usage by project** | What each project reserves, what it uses, and its share of the capacity it runs on. |
| **Cluster metrics** | Charts of what services use across the cluster and what the machines are doing. |
| **Geass health** | Charts of how the operator is doing: failed and slow reconciles, work waiting, how the Kubernetes API server answers it, and its memory. |
| **Regions** | The default region for new projects. Only present once at least one node has a region. |
| **HA readiness** | Whether the cluster meets the prerequisites for highly available databases (see [High availability](/docs/operations/high-availability/)). |

## Requests, limits and actual usage

Three different numbers describe a workload. The page uses the first for capacity and the third only where it says "using".

| Number | Meaning | Where Geass uses it |
| --- | --- | --- |
| **Request** (shown as "reserved" or "requested") | The CPU and memory a container asks the scheduler for. The scheduler places a pod only on a node that has this much unreserved. | Every capacity figure and the capacity check on deploy. |
| **Limit** | The most a container may use. Above its CPU limit it is slowed down; above its memory limit it is killed and restarted. | When you pick a size in the dashboard, Geass sets the limit equal to the request, so a service cannot use more than it reserved, unless you set **can use up to** higher on a service. The capacity check does not count the difference. |
| **Actual usage** | What the container consumes now, measured by Prometheus. | The "using" figures in **Usage by project** and the cluster charts. Absent when monitoring is not installed. |

The capacity check looks at the whole cluster. To stop one project from using all of it, set [project limits](/docs/guides/project-limits/), which cap what each environment of a project can ask for.

A node can look full on this page while its CPU is idle, because services reserved the room. A node can also be busy while the page shows spare capacity, if a pod runs without requests. Use the page to decide whether something new fits, and the charts to see whether the machines are working hard.

:::note
Sizes you pick in the dashboard are 0.1, 0.25, 0.5, 1 or 2 CPU and 128 MB, 256 MB, 512 MB, 1 GB, 2 GB or 4 GB. A service defaults to 0.1 CPU and 128 MB. A Postgres, MySQL or Redis server defaults to 0.25 CPU and 512 MB per instance (a high availability MySQL instance is raised to at least 1 GB), and SQLite to 0.1 CPU and 128 MB. The cluster's object storage server defaults to 0.25 CPU and 512 MB with SeaweedFS, which idles at about 60 MB of memory, and to 0.1 CPU and 256 MB with Garage, which idles at about 6 MB, and its volume defaults to 10 GiB, which you can choose when you set it up and make bigger later. Through a manifest you can set any request.
:::

## Summary

The **Summary** table has three rows.

| Row | **Available** column | **Requested / allocatable** column |
| --- | --- | --- |
| **CPU available** | Allocatable CPU minus what pods request. | For example "450m requested of 2". |
| **Memory available** | Allocatable memory minus what pods request. | For example "1Gi requested of 3880Mi". |
| **Healthy nodes** | How many nodes are ready. | How many of those are schedulable. |

The **Load** bar on the first two rows is requested divided by allocatable. It turns amber from 70 percent and red from 90 percent.

What counts toward the totals:

- Only nodes that can take general work: ready, not cordoned, and without a reservation. A node reserved for a tag, or for a compliance tag (see [Scaling and placement](/docs/guides/scaling-and-placement/)), is left out, and so are the pods that run on it. So is an **ephemeral** node, a node of a [pool](/docs/guides/node-pools/#what-runs-on-a-pools-nodes) that keeps data off itself: it takes services only, and the totals are the room every kind of workload may use. The capacity check for a service does count it.
- All of Geass's own pods and the system pods, because they reserve room too.
- Pods that have no node yet but could run on one of the counted nodes. A pod waiting to be placed reserves capacity before it starts.
- Init containers count in full, so a pod whose init container has finished is slightly overstated.

If no node reports capacity yet, the **Summary** is replaced by a **Capacity unavailable** panel that says "Schedulable nodes must report before totals appear."

## Version

The **Version** table says what the cluster runs.

| Row | Meaning |
| --- | --- |
| **Geass** | The release of the operator that serves this dashboard, such as `v0.3.0`, and the data schema level the operator reports. A build that is not a release reads "development build". |
| **K3s (control plane)** | The oldest K3s version that a control plane node reports. |
| **K3s on workers (target)** | Only while `spec.k3s.version` asks for a version: the target, what the operator is doing (the node it is on, and how many are left), and a status of **Upgrading**, **Waiting**, **Waiting for the control plane**, **Failed** or **Up to date**. |

Geass is updated by running the installer of a newer release on the control plane, not from this page. See [Upgrade Geass](/docs/operations/upgrading/).

## Nodes

Each row of the **Nodes** table is a Kubernetes node.

| Column | Meaning |
| --- | --- |
| **Node** | The node's name. |
| **Role** | `control-plane` for the server, `worker` for every other node. |
| **Pool** | The [pool](/docs/guides/node-pools/) the node belongs to, as a link, and an **Ephemeral** badge when the node keeps data off itself: databases, buckets and volumes never run there, and it may be removed when the load goes. A dash for a node that is in no pool. `geass nodes list` shows the same as `POOL` and `EPHEMERAL`. |
| **K3s** | The K3s release the node runs, as its kubelet reports it. Worker nodes can differ from the control plane while an upgrade is going on. |
| **Region** | The region, and the site after a dot, such as `eu · hetzner-fsn1`. A dash when none is set. |
| **Tags** | The node's tags. A tag followed by "(reserved)" keeps other workloads off the node. A tag followed by "(compliance)" is reserved for the projects that require it ([Compliance node tags](/docs/guides/compliance-tags/)). |
| **CPU**, **Memory** | What the node offers to workloads (its allocatable amount, which is less than the machine's total). |
| **Pressure** | The conditions the node reports, comma separated. A dash when there are none. |
| **Status** | **Ready**, **Not ready** or **Unschedulable**. |

**Unschedulable** means the node is cordoned: Kubernetes will not start new pods on it, though pods already there keep running. Geass has no cordon button; someone ran `kubectl cordon` (see below), or Geass is upgrading K3s on the node, in which case the **Version** section says so (see [Upgrade Geass](/docs/operations/upgrading/#workers)). A node of an autoscaling pool is also cordoned for the time it takes to empty it before it is removed, and a server that is being removed shows it too.

**Add server** opens a form to add one server, a machine Geass creates at a cloud or one you have: see [Add servers](/docs/getting-started/add-servers/#add-a-server-from-the-dashboard-or-the-command-line). **Remove** on a server that was added that way asks you to type its name, then moves its services elsewhere and, for a machine Geass created, deletes the machine. The control plane and the nodes of a pool have no **Remove**: change the pool instead.

## Node health in the rail

The bottom of the left rail always shows the health of your nodes. It is hidden when the rail is collapsed.

- The label reads "1 NODE" or "3 NODES".
- One short bar represents each node: green when the node is healthy, red when it is down or under pressure, amber when it is unschedulable.
- A line under the bars names the worst problem in words, for example `node-3 memory high`, or "all healthy".
- Selecting it opens the **Usage** page.

The wording for each problem:

| You see | The node reports |
| --- | --- |
| `not ready` | It is not ready: it has stopped answering the cluster. |
| `memory high` | `MemoryPressure` |
| `disk full` | `DiskPressure` |
| `too many processes` | `PIDPressure` |
| `network down` | `NetworkUnavailable` |
| `unschedulable` | It is cordoned. |
| `under pressure` | Any other pressure condition. |

When several nodes have problems, the line names a down node first, then a node under pressure, then an unschedulable one.

## What each state means and how to act

The same states appear in the **Pressure** column and in **Capacity and node pressure** as "Node node-3 reports MemoryPressure".

| State | What it means | What to do |
| --- | --- | --- |
| **Not ready** | The node's agent has stopped reporting. The machine may be off, unreachable, or K3s may have stopped. | Check the machine and the K3s service (`systemctl status k3s` on the server, `k3s-agent` on a worker). Geass re-provisions a worker over SSH once it has been not ready for five minutes. See [Troubleshooting](/docs/operations/troubleshooting/). |
| `MemoryPressure` | The node is running out of memory. Kubernetes starts evicting pods. | Find the biggest reservations in **Usage by project**. Lower replicas or memory, move work to another node, or add a node. |
| `DiskPressure` | The node's disk is nearly full. Kubernetes evicts pods and stops starting new ones. | Free space on the node: unused images, old logs, large volumes. Container images, logs and database volumes all use the node's disk. |
| `PIDPressure` | Too many processes. | Find the pod that spawns them (`kubectl top pod`, `kubectl describe node`). Restart or remove it. |
| `NetworkUnavailable` | The node's pod network is not set up. | On a multi-provider cluster, check the overlay and firewall rules (see [Nodes at more than one provider](/docs/operations/multi-provider/)). Otherwise restart K3s on the node. |
| **Unschedulable** | The node is cordoned. | Run `kubectl uncordon <node>` when maintenance is done. |

## Capacity and node pressure

This section lists problems that need action before you scale up.

- One line for each node pressure condition: "Node node-3 reports DiskPressure". A cordoned node adds "Node node-3 reports Unschedulable".
- "Available CPU is too low for a new service. Scale up the cluster before creating in-cluster resources" when less than 0.1 CPU is free.
- "Available memory is too low for a new service. Scale up the cluster before creating in-cluster resources" when less than 128 MB is free.
- "Services may use up to … of memory between them and the servers have …. If they all do at once, those using more than they reserved restart first" when the running services are allowed more memory, added up, than the servers have. It is a warning and nothing is refused: services reserve what they need and may use more only while there is room. See [Scale](/docs/guides/apps/#scale). It is not shown for a region or tag view of the cluster.

Free more room or add a node (see [Add servers](/docs/getting-started/add-servers/), or let a [node pool](/docs/guides/node-pools/) add them). Geass refuses to start a new workload that does not fit; see the capacity check in [Scaling and placement](/docs/guides/scaling-and-placement/).

## Usage by project

**Usage by project** opens with a sentence such as "The cluster has 4.00 vCPU and 7.5 GB of memory across 3 nodes." The sentence counts nodes that are ready and not cordoned. A table follows, biggest consumer first.

| Column | Meaning |
| --- | --- |
| **Project** | The project's name. |
| **Region** | The project's region, or the cluster default. A dash when it may use any node. |
| **Services running** | Pods that are not finished or being deleted, across all of the project's environments. Databases count. |
| **CPU**, **Memory**, **Storage** | What the project reserves. With monitoring installed, each cell reads "using of reserved", for example "0.05 vCPU of 0.25 vCPU". Storage reserved is the size of the project's volumes. |
| **Share of its capacity** | The larger of its CPU share and memory share, as a bar. The capacity is that of the project's region, or of the whole cluster when it has no region. |

The last row, **Geass and system services**, is everything that is not a project: the operator, K3s system pods, monitoring, storage and the databases that projects share.

When nodes have regions, **By region** follows: for each region its node count and how much CPU and memory is reserved of what its nodes offer.

Each project's own **Settings** page, under **Usage**, shows the same table for that project alone, with its charts.

## Cluster metrics

**Cluster metrics** charts the last 15 minutes, hour, 6 hours or 24 hours (the **Time range** menu). It shows CPU and memory used by services, how busy the machines are, and network in and out. See [Logs, metrics and terminal](/docs/guides/observability/) for what each chart measures and the limit on machine charts in a multi-provider cluster.

## Geass health

**Geass health** charts the operator itself (its failures, its speed, its queues, how the API server treats it, and its memory), and has its own **Time range** menu. Look at it when the platform feels slow: a deploy that takes long to start, a status that is late to change.

| Chart | What it shows | What it means |
| --- | --- | --- |
| **Failed reconciles** | Reconciles that ended in an error, per second. | Above zero for a long time, a controller keeps failing. Read the operator log. |
| **Reconcile time (slowest 1%)** | How long the slowest one in a hundred reconciles took. | Seconds or more means the operator is slow, or the API server is. |
| **API server answer time (slowest 1%)** | How long the Kubernetes API server took to answer the operator. Watches are left out. | Slow answers mean the API server is the problem, not the operator. |
| **Requests to the API server** | Requests the operator sends, per second. | |
| **Work waiting** | Items waiting in the Geass controllers' queues. | A number that keeps growing means the operator is behind. |
| **Status writes** | Status updates the operator actually wrote, per second. | |
| **Status writes avoided** | Status updates the operator skipped because nothing had changed. | A healthy, steady cluster is mostly this. |
| **Operator memory** | The memory the operator's process holds. | It grows with the number of services, about 36 KiB each. Near 700 MiB, the limit it ships with is close: see [Cluster size](/docs/operations/sizing/). |

The charts need the monitoring add-on, like every other chart. See [Logs, metrics and terminal](/docs/guides/observability/#metrics-and-alerts-for-the-operator) for the alerts that watch the same numbers.

## What happens under the hood

The page is built from the Kubernetes API, read through the operator's watch cache, so it follows the cluster within moments and nothing is stored. The cache leaves out pods that completed successfully, which the page does not count either.

- **Capacity** comes from each node's allocatable CPU and memory and the requests of every pod. Geass adds up the requests of pods that are not finished.
- **Pressure** comes from the node's conditions (`Ready`, `MemoryPressure`, `DiskPressure`, `PIDPressure`, `NetworkUnavailable`) and its `spec.unschedulable` field.
- **Using** comes from Prometheus: a five-minute CPU rate, the working-set memory and the used bytes of each volume, summed by namespace. A project's namespaces are `<project>-<environment>`.

Compare the page with Kubernetes itself:

```bash
kubectl get nodes -o wide
kubectl describe node <node>        # Conditions, Allocatable, and Allocated resources (requests and limits)
kubectl top node                    # live use; K3s ships the metrics server
kubectl top pod -A --sort-by=memory
```

To cordon a node for maintenance, or put it back:

```bash
kubectl cordon <node>
kubectl uncordon <node>
```

The same data is available to scripts. With an API token (see [Users and tokens](/docs/guides/users-and-tokens/)):

```bash
curl -sS -H "Authorization: Bearer $GEASS_TOKEN" https://geass.example.com/api/usage
```

The response lists `cpuAllocatableMillis`, `memoryAllocatableBytes`, `nodes`, a `projects` array, a `platform` object and a `regions` array. `actualAvailable` is `true` when the "using" figures could be read. See the [REST API](/docs/reference/rest-api/).

## Usage by organization, for billing

The page shows what is reserved now. On a cluster that several [organizations](/docs/guides/organizations-and-workspaces/) share, Geass can also keep what each organization reserved, hour by hour, so that the platform can bill them for it. It is off until you turn it on, and only the platform's administrators can read it.

```bash title="Turn the meter on"
sudo k3s kubectl -n geass-system patch geassplatformconfig platform --type merge -p '{"spec":{"usage":{"enabled":true}}}'
```

### What is metered

Every minute the operator adds what is reserved at that moment, times the time since the last minute, to the hour it is in:

| Figure | What it is | Unit in the export |
| --- | --- | --- |
| CPU | What the pods ask for (their requests), from the moment a pod is placed on a node until it finishes. As the scheduler counts it: the containers together, or the largest init container if that is more, plus the pod's overhead. | Core-hours |
| Memory | The same, for memory. | GiB-hours |
| Storage | The size of each bound volume (the size of the volume it is bound to, or what it asked for when the volume does not say). | GiB-hours |
| Egress | Not metered. Always `null`. | |

A pod or a volume is an organization's through the project whose environment it runs in: the project's organization and workspace (`spec.org` and `spec.workspace`) say whose it is, at the moment of each sample. Builds in an organization's build namespace are the organization's, under the workspace `""`, which also holds the projects of the organization that are in no workspace. Two other totals are kept apart and never added to an organization: **unassigned**, the projects that belong to no organization, and **platform**, everything that is no project's (Geass, K3s, monitoring, storage, and the shared database and bucket servers).

What it does not measure:

- **What is used.** The figures are reservations, as on this page. A service that reserves 0.25 CPU and sits idle is billed 0.25 CPU, and one that sets no request is billed nothing, unless its organization has [noisy-neighbour limits](/docs/guides/tenant-limits/#how-a-containers-defaults-are-worked-out), which give every container that asks for nothing a default request, and that request is billed. What pods use comes from Prometheus, which is optional and keeps its own retention, so it is not the source.
- **Traffic.** Geass has no figure for traffic that leaves the cluster. The only network number it has is what each pod sends, from Prometheus, which counts traffic to other pods and to the cluster's databases as well. Every answer says `"egress": {"available": false, ...}`.
- **Shared servers by organization.** A logical database on a shared server is billed as the server's, under **platform**, and so are the bytes in buckets on the cluster's object storage server.
- **Nodes.** An organization with nodes of its own (reserved with node tags) is billed for its pods' reservations like any other; bill the nodes from their pool if that is the deal.

### How it is kept

Each hour (in UTC) is a ConfigMap in `geass-system` named `geass-usage-<YYYYMMDDHH>`, labelled `geass.dev/usage-hour` and `geass.dev/usage-day`, and kept for 62 days. Only the operator that holds the leader lease meters, and only where the controllers run: a dashboard-only process (`--controllers=false`) reads the hours and never writes them.

An hour says how far it has been metered, and each minute only adds the time after that. An operator that restarts within three minutes carries on where the hours end, without counting a minute twice or losing one. Time the meter did not see (it was off, or away for longer) is not counted, and shows: the hour's `meteredSeconds` is less than 3600. An hour is **closed** once it is metered to its end, or five minutes after it ends; after that it never changes.

Turning the meter off stops it, and keeps the hours it stored until it is turned on again (which deletes those older than 62 days) or you delete them:

```bash title="Delete every stored hour"
sudo k3s kubectl -n geass-system delete configmap -l geass.dev/usage-hour
```

### Read it

`GET /api/v1/usage/hours` answers with every hour from `from` to `to` (RFC 3339; the last 24 hours by default, at most 31 days at a time), for every organization or, with `?org=`, for one. It needs an admin who acts for the platform: a platform administrator, or an install-wide admin token. An organization's people and tokens, viewers, observers and project tokens get `403`.

```bash title="What each organization reserved this morning"
curl -fsS -H "Authorization: Bearer $GEASS_TOKEN" \
  "https://geass.example.com/api/v1/usage/hours?from=2026-10-23T09:00:00Z&to=2026-10-23T12:00:00Z" \
  | jq -r '.hours[] | .start as $hour | .orgs[] | [$hour, .org, .workspace, .cpuCoreHours, .memoryGiBHours, .storageGiBHours] | @tsv'
```

To bill as you go, ask every hour or so from the `next` of the last answer, and store each hour by its start, organization and workspace, replacing what you stored for it before: an hour that is still open (`"closed": false`) comes again in the next answer until it closes. The [REST API](/docs/reference/rest-api/#usage-by-organization) has the whole answer.

## Troubleshooting

**The page says "Capacity unavailable".**
No node is ready and schedulable yet, or the node list could not be read. Check `kubectl get nodes`. A cluster whose only node is cordoned, or reserved, or not ready, has no capacity for general work.

**"Healthy nodes" shows 3 but fewer are schedulable.**
Healthy counts ready nodes. Schedulable leaves out cordoned nodes and nodes reserved for a tag. Uncordon the node, or release the reservation in **Edit** on the node.

**A project shows less "using" than I expect, or "using" is missing.**
The "using" figures need Prometheus. Without it the cells show only the reserved amount and the table says "Live use appears once monitoring is installed." Check `kubectl -n monitoring get pods`.

**The numbers differ from `kubectl top`.**
`kubectl top` shows live use. This page shows reservations. Both are right; they answer different questions.

**The cluster looks full, but nothing is running.**
Pods that are waiting for a node still reserve capacity here. Look for pods stuck in `Pending`: `kubectl get pods -A --field-selector=status.phase=Pending`.

**A node is red in the rail but the page shows Ready.**
The bar turns red for pressure as well as for a down node. Read the line under the bars, or the **Pressure** column.

**The Cluster metrics or Geass health charts say "Metrics are not available yet".**
Prometheus is not installed, or the dashboard cannot reach it. See [Logs, metrics and terminal](/docs/guides/observability/#troubleshooting).

## Related

- [Scaling and placement](/docs/guides/scaling-and-placement/)
- [Node pools and autoscaling](/docs/guides/node-pools/)
- [Project limits](/docs/guides/project-limits/)
- [Cluster size](/docs/operations/sizing/)
- [Logs, metrics and terminal](/docs/guides/observability/)
- [High availability](/docs/operations/high-availability/)
- [Troubleshooting](/docs/operations/troubleshooting/)
