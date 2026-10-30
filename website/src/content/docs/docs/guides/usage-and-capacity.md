---
title: "Usage and capacity"
description: "Read the Usage page: node health, the CPU, memory, and storage that each project reserves, and what to do when a node has pressure or the cluster is full."
---

The **Usage** page answers two questions:

- How much of your servers is already reserved?
- Which machines need attention?

The page lists every node with its health. It adds up the CPU and memory that services ask for, and divides that total by project and by region. Open the page before you create a large service or database. Also open it when a service does not deploy because there is not enough capacity.

![The Usage page.](../../../../assets/docs/usage.webp)
*Screenshot with example data.*

## Before you start

- Every signed-in user can read the page. Only admins see these controls:
  - The **Edit** button on a node. See [Scaling and placement](/docs/guides/scaling-and-placement/).
  - **Add server** above the table.
  - **Remove** on a server that was added on its own.
- The numbers show what services reserve, not what they use at this moment. Live use needs the monitoring add-on. The installer turns on that add-on. See [Logs, metrics and terminal](/docs/guides/observability/).

## Open the page

Select **Usage** in the left rail, or select **Settings** and then **Usage**. Both open the same page. The address is `/cluster` on your dashboard host.

From top to bottom, the page shows these sections:

| Section | What it shows |
| --- | --- |
| **Summary** | CPU available, memory available, and healthy nodes for the whole cluster. |
| **Capacity and node pressure** | One line for each node problem and each shortage. Present only when there is something to report. |
| **Network isolation** | Whether the network plugin of the cluster acts on network policies. Project isolation depends on these policies. Present after the first check has run. See [Network isolation](/docs/guides/network-isolation/#check-that-the-cluster-enforces-policies). |
| **Version** | The Geass release and the K3s that the cluster runs. While you ask for an upgrade of K3s on the workers, also the progress of that upgrade. |
| **Nodes** | One row for each node, with its role, the pool it belongs to, the K3s version it runs, region, tags, size, pressure, and status. **Add server** is above it. |
| **Usage by project** | What each project reserves, what it uses, and its share of the capacity that it runs on. |
| **Cluster metrics** | Charts of what services use across the cluster, and of what the machines do. |
| **Geass health** | Charts of the operator: failed and slow reconciles, work waiting, how the Kubernetes API server answers the operator, and its memory. |
| **Regions** | The default region for new projects. Present only after at least one node has a region. |
| **HA readiness** | Whether the cluster meets the prerequisites for highly available databases. See [High availability](/docs/operations/high-availability/). |

## Requests, limits and actual usage

Three different numbers describe a workload. The page uses the first number for capacity. It uses the third number only where it shows "using".

| Number | Meaning | Where Geass uses it |
| --- | --- | --- |
| **Request** (shown as "reserved" or "requested") | The CPU and memory that a container asks the scheduler for. The scheduler puts a pod only on a node that has this much unreserved. | Every capacity figure, and the capacity check on deploy. |
| **Limit** | The maximum that a container can use. Above its CPU limit, the container is slowed. Above its memory limit, the container is killed and restarted. | When you pick a size in the dashboard, Geass sets the limit equal to the request. Thus a service cannot use more than it reserved, unless you set **can use up to** higher on a service. The capacity check does not count the difference. |
| **Actual usage** | What the container uses now, as Prometheus measures it. | The "using" figures in **Usage by project**, and the cluster charts. Absent when monitoring is not installed. |

The capacity check looks at the whole cluster. To stop one project from using all of the cluster, set [project limits](/docs/guides/project-limits/). Project limits set a maximum on what each environment of a project can ask for.

A node can look full on this page while its CPU is idle, because services reserved the space. A node can also be busy while the page shows spare capacity, if a pod runs without requests. Use the page to decide whether something new fits. Use the charts to see whether the machines work hard.

:::note
These are the sizes that you can pick in the dashboard:

- CPU: 0.1, 0.25, 0.5, 1, or 2.
- Memory: 128 MB, 256 MB, 512 MB, 1 GB, 2 GB, or 4 GB.

These are the defaults:

- A service: 0.1 CPU and 128 MB.
- A Postgres, MySQL, or Redis server: 0.25 CPU and 512 MB for each instance. Geass raises a high availability MySQL instance to a minimum of 1 GB.
- SQLite: 0.1 CPU and 128 MB.
- The object storage server of the cluster with SeaweedFS: 0.25 CPU and 512 MB. SeaweedFS uses about 60 MB of memory when idle.
- The object storage server of the cluster with Garage: 0.1 CPU and 256 MB. Garage uses about 6 MB when idle.
- The volume of the object storage server: 10 GiB. You can choose the size when you set up the server, and make it bigger later.

In a manifest, you can set any request.
:::

## Summary

The **Summary** table has three rows.

| Row | **Available** column | **Requested / allocatable** column |
| --- | --- | --- |
| **CPU available** | Allocatable CPU minus what pods request. | For example "450m requested of 2". |
| **Memory available** | Allocatable memory minus what pods request. | For example "1Gi requested of 3880Mi". |
| **Healthy nodes** | The number of nodes that are ready. | The number of those nodes that are schedulable. |

The **Load** bar on the first two rows is requested divided by allocatable. The bar turns amber at 70 percent and red at 90 percent.

These items count toward the totals:

- Only nodes that can take general work: ready, not cordoned, and without a reservation.
  - Geass leaves out a node that is reserved for a tag or for a compliance tag, and the pods that run on it. See [Scaling and placement](/docs/guides/scaling-and-placement/).
  - Geass also leaves out an **ephemeral** node. An ephemeral node is a node of a [pool](/docs/guides/node-pools/#what-runs-on-a-pools-nodes) that keeps data off itself. It takes services only, and the totals are the space that every kind of workload can use. The capacity check for a service does count the ephemeral node.
- All of the pods of Geass and the system pods, because they also reserve space.
- Pods that have no node yet but can run on one of the counted nodes. A pod that waits for a node reserves capacity before it starts.
- Init containers, in full. Thus the total is slightly too high for a pod whose init container has finished.

If no node reports capacity yet, a **Capacity unavailable** panel replaces the **Summary**. The panel shows "Schedulable nodes must report before totals appear."

## Version

The **Version** table shows what the cluster runs.

| Row | Meaning |
| --- | --- |
| **Geass** | The release of the operator that serves this dashboard, for example `v0.3.0`, and the data schema level that the operator reports. A build that is not a release shows "development build". |
| **K3s (control plane)** | The oldest K3s version that a control plane node reports. |
| **K3s on workers (target)** | Only while `spec.k3s.version` asks for a version. It shows the target, and what the operator does: the node that it is on, and how many nodes are left. It also shows a status of **Upgrading**, **Waiting**, **Waiting for the control plane**, **Failed**, or **Up to date**. |

To update Geass, run the installer of a newer release on the control plane. You cannot update Geass from this page. See [Upgrade Geass](/docs/operations/upgrading/).

## Nodes

Each row of the **Nodes** table is a Kubernetes node.

| Column | Meaning |
| --- | --- |
| **Node** | The name of the node. |
| **Role** | `control-plane` for the server, `worker` for every other node. |
| **Pool** | The [pool](/docs/guides/node-pools/) that the node belongs to, as a link. An **Ephemeral** badge shows when the node keeps data off itself. Databases, buckets, and volumes never run on that node, and the node might be removed when the load goes. A dash for a node that is in no pool. `geass nodes list` shows the same as `POOL` and `EPHEMERAL`. |
| **K3s** | The K3s release that the node runs, as its kubelet reports it. During an upgrade, worker nodes can be different from the control plane. |
| **Region** | The region, and the site after a dot, for example `eu · hetzner-fsn1`. A dash when no region is set. |
| **Tags** | The tags of the node. A tag followed by "(reserved)" keeps other workloads off the node. A tag followed by "(compliance)" is reserved for the projects that require it. See [Compliance node tags](/docs/guides/compliance-tags/). |
| **CPU**, **Memory** | What the node offers to workloads. This is its allocatable amount, which is less than the total of the machine. |
| **Pressure** | The conditions that the node reports, separated by commas. A dash when there are none. |
| **Status** | **Ready**, **Not ready**, or **Unschedulable**. |

**Unschedulable** means that the node is cordoned. Kubernetes does not start new pods on the node, but pods that are already there continue to run. Geass has no cordon button. One of these is the cause:

- Somebody ran `kubectl cordon`. See the commands later on this page.
- Geass upgrades K3s on the node. In this case, the **Version** section shows the upgrade. See [Upgrade Geass](/docs/operations/upgrading/#workers).
- The node is in an autoscaling pool, and Geass empties the node before Geass removes it. The node stays cordoned while Geass empties it.
- The node is a server that Geass is removing.

**Add server** opens a form to add one server. The server can be a machine that Geass creates at a cloud, or a machine that you have. See [Add servers](/docs/getting-started/add-servers/#add-a-server-from-the-dashboard-or-the-command-line). **Remove** is on a server that was added with that form. **Remove** asks you to type the name of the server. Then Geass moves the services of the server to other nodes. For a machine that Geass created, Geass then deletes the machine. The control plane and the nodes of a pool have no **Remove**. To remove one of those nodes, change the pool.

## Node health in the rail

The bottom of the left rail always shows the health of your nodes. When you collapse the rail, the node health is hidden.

- The label shows "1 NODE" or "3 NODES".
- One short bar shows each node. The bar is green when the node is healthy, red when the node is down or under pressure, and amber when the node is unschedulable.
- A line under the bars gives the worst problem in words, for example `node-3 memory high`, or "all healthy".
- When you select the node health, the **Usage** page opens.

These are the words for each problem:

| You see | The node reports |
| --- | --- |
| `not ready` | It is not ready. It has stopped answering the cluster. |
| `memory high` | `MemoryPressure` |
| `disk full` | `DiskPressure` |
| `too many processes` | `PIDPressure` |
| `network down` | `NetworkUnavailable` |
| `unschedulable` | It is cordoned. |
| `under pressure` | Any other pressure condition. |

When more than one node has problems, the line gives a down node first, then a node under pressure, and then an unschedulable node.

## What each state means and how to act

The same states show in the **Pressure** column. They also show in **Capacity and node pressure**, as "Node node-3 reports MemoryPressure".

| State | What it means | What to do |
| --- | --- | --- |
| **Not ready** | The agent of the node has stopped reporting. The machine might be off or unreachable, or K3s might have stopped. | Check the machine and the K3s service: `systemctl status k3s` on the server, and `k3s-agent` on a worker. When a worker has been not ready for five minutes, Geass provisions it again over SSH. See [Troubleshooting](/docs/operations/troubleshooting/). |
| `MemoryPressure` | The node has almost no free memory. Kubernetes starts to evict pods. | Find the biggest reservations in **Usage by project**. Lower replicas or memory, move work to another node, or add a node. |
| `DiskPressure` | The disk of the node is almost full. Kubernetes evicts pods and stops new pods from starting. | Make space free on the node: unused images, old logs, large volumes. Container images, logs, and database volumes all use the disk of the node. |
| `PIDPressure` | Too many processes. | Find the pod that starts them (`kubectl top pod`, `kubectl describe node`). Restart or remove that pod. |
| `NetworkUnavailable` | The pod network of the node is not set up. | On a multi-provider cluster, check the overlay and the firewall rules. See [Nodes at more than one provider](/docs/operations/multi-provider/). On other clusters, restart K3s on the node. |
| **Unschedulable** | The node is cordoned. | When the maintenance is done, run `kubectl uncordon <node>`. |

## Capacity and node pressure

This section lists problems that need action before you scale up.

- One line for each node pressure condition: "Node node-3 reports DiskPressure". A cordoned node adds "Node node-3 reports Unschedulable".
- "Available CPU is too low for a new service. Scale up the cluster before creating in-cluster resources" when less than 0.1 CPU is free.
- "Available memory is too low for a new service. Scale up the cluster before creating in-cluster resources" when less than 128 MB is free.
- "Services may use up to … of memory between them and the servers have …. If they all do at once, those using more than they reserved restart first" when the total memory that the running services can use is more than the servers have.
  - This line is a warning, and Geass refuses nothing. Services reserve what they need, and can use more only while there is space. See [Scale](/docs/guides/apps/#scale).
  - This line does not show for a region or tag view of the cluster.

Make more space free, or add a node. See [Add servers](/docs/getting-started/add-servers/), or let a [node pool](/docs/guides/node-pools/) add nodes. Geass refuses to start a new workload that does not fit. See the capacity check in [Scaling and placement](/docs/guides/scaling-and-placement/).

## Usage by project

**Usage by project** starts with a sentence, for example "The cluster has 4.00 vCPU and 7.5 GB of memory across 3 nodes." The sentence counts nodes that are ready and not cordoned. A table follows, with the biggest consumer first.

| Column | Meaning |
| --- | --- |
| **Project** | The name of the project. |
| **Region** | The region of the project, or the cluster default. A dash when the project can use any node. |
| **Services running** | Pods that are not finished or being deleted, across all of the environments of the project. Databases count. |
| **CPU**, **Memory**, **Storage** | What the project reserves. With monitoring installed, each cell shows "using of reserved", for example "0.05 vCPU of 0.25 vCPU". Storage reserved is the size of the volumes of the project. |
| **Share of its capacity** | The larger of its CPU share and memory share, as a bar. The capacity is the capacity of the region of the project, or of the whole cluster when the project has no region. |

The last row, **Geass and system services**, is everything that is not a project: the operator, K3s system pods, monitoring, storage, and the databases that projects share.

When nodes have regions, **By region** follows. For each region, it shows the node count, and how much of the CPU and memory of its nodes is reserved.

The **Settings** page of each project, under **Usage**, shows the same table for that project only, with its charts.

## Cluster metrics

**Cluster metrics** shows charts of the last 15 minutes, hour, 6 hours, or 24 hours. Select the period in the **Time range** menu. The charts show the CPU and memory that services use, how busy the machines are, and the network in and out. For what each chart measures, and the limit on machine charts in a multi-provider cluster, see [Logs, metrics and terminal](/docs/guides/observability/).

## Geass health

**Geass health** shows charts of the operator itself: its failures, its speed, its queues, how the API server treats it, and its memory. The section has its own **Time range** menu. Look at it when the platform is slow, for example when a deploy takes long to start, or a status is late to change.

| Chart | What it shows | What it means |
| --- | --- | --- |
| **Failed reconciles** | Reconciles that ended in an error, per second. | If it stays above zero for a long time, a controller continues to fail. Read the operator log. |
| **Reconcile time (slowest 1%)** | The time of the slowest one in a hundred reconciles. | Seconds or more means that the operator is slow, or that the API server is slow. |
| **API server answer time (slowest 1%)** | The time that the Kubernetes API server took to answer the operator. Watches are left out. | Slow answers mean that the API server is the problem, not the operator. |
| **Requests to the API server** | Requests that the operator sends, per second. | |
| **Work waiting** | Items that wait in the queues of the Geass controllers. | A number that continues to grow means that the operator is behind. |
| **Status writes** | Status updates that the operator actually wrote, per second. | |
| **Status writes avoided** | Status updates that the operator skipped, because nothing had changed. | In a healthy, steady cluster, most status updates are of this type. |
| **Operator memory** | The memory that the process of the operator holds. | It grows with the number of services, about 36 KiB for each service. Near 700 MiB, the operator is close to the limit that it ships with. See [Cluster size](/docs/operations/sizing/). |

The charts need the monitoring add-on, like every other chart. For the alerts that watch the same numbers, see [Logs, metrics and terminal](/docs/guides/observability/#metrics-and-alerts-for-the-operator).

## What happens under the hood

Geass builds the page from the Kubernetes API, and reads the API through the watch cache of the operator. Thus the page follows the cluster within moments, and Geass stores nothing. The cache leaves out pods that completed successfully. The page also does not count those pods.

- **Capacity** comes from the allocatable CPU and memory of each node, and the requests of every pod. Geass adds up the requests of pods that are not finished.
- **Pressure** comes from the conditions of the node (`Ready`, `MemoryPressure`, `DiskPressure`, `PIDPressure`, `NetworkUnavailable`) and its `spec.unschedulable` field.
- **Using** comes from Prometheus: a five-minute CPU rate, the working-set memory, and the used bytes of each volume, added up by namespace. The namespaces of a project are `<project>-<environment>`.

To compare the page with Kubernetes itself, run these commands:

```bash
kubectl get nodes -o wide
kubectl describe node <node>        # Conditions, Allocatable, and Allocated resources (requests and limits)
kubectl top node                    # live use; K3s ships the metrics server
kubectl top pod -A --sort-by=memory
```

To cordon a node for maintenance, or to put it back, run these commands:

```bash
kubectl cordon <node>
kubectl uncordon <node>
```

Scripts can also get the same data with an API token. See [Users and tokens](/docs/guides/users-and-tokens/).

```bash
curl -sS -H "Authorization: Bearer $GEASS_TOKEN" https://geass.example.com/api/usage
```

The response lists `cpuAllocatableMillis`, `memoryAllocatableBytes`, `nodes`, a `projects` array, a `platform` object, and a `regions` array. `actualAvailable` is `true` when Geass could read the "using" figures. See the [REST API](/docs/reference/rest-api/).

## Usage by organization, for billing

The page shows what is reserved now. More than one [organization](/docs/guides/organizations-and-workspaces/) can share a cluster. On such a cluster, Geass can also keep what each organization reserved, hour by hour, so that the platform can bill the organizations for it. This meter is off until you turn it on. Only the administrators of the platform can read it.

```bash title="Turn the meter on"
sudo k3s kubectl -n geass-system patch geassplatformconfig platform --type merge -p '{"spec":{"usage":{"enabled":true}}}'
```

### What is metered

Every minute, the operator calculates what is reserved at that moment, times the time since the last minute. It adds the result to the current hour:

| Figure | What it is | Unit in the export |
| --- | --- | --- |
| CPU | What the pods ask for (their requests), from the moment that the scheduler puts a pod on a node until the pod finishes. The meter counts as the scheduler counts: the containers together, or the largest init container if that is more, plus the overhead of the pod. | Core-hours |
| Memory | The same, for memory. | GiB-hours |
| Storage | The size of each bound volume. That is the size of the volume that it is bound to, or what it asked for when the volume does not give a size. | GiB-hours |
| Egress | Not metered. Always `null`. | |

A pod or a volume belongs to an organization through the project of the environment that it runs in. The organization and workspace of the project (`spec.org` and `spec.workspace`) tell whose it is, at the moment of each sample. Builds in the build namespace of an organization belong to the organization, under the workspace `""`. That workspace also holds the projects of the organization that are in no workspace. Geass keeps two other totals separate, and never adds them to an organization:

- **unassigned**: the projects that belong to no organization.
- **platform**: everything that belongs to no project. That is Geass, K3s, monitoring, storage, and the shared database and bucket servers.

The meter does not measure these items:

- **What is used.** The figures are reservations, as on this page. A service that reserves 0.25 CPU and is idle is billed 0.25 CPU. A service that sets no request is billed nothing. The exception is an organization with [noisy-neighbour limits](/docs/guides/tenant-limits/#how-a-containers-defaults-are-worked-out). These limits give a default request to every container that asks for nothing, and that request is billed. What pods use comes from Prometheus. Prometheus is optional and has its own retention, thus it is not the source.
- **Traffic.** Geass has no figure for traffic that leaves the cluster. The only network number that it has is what each pod sends, from Prometheus. That number also counts traffic to other pods and to the databases of the cluster. Every answer shows `"egress": {"available": false, ...}`.
- **Shared servers by organization.** Geass bills a logical database on a shared server as part of the server, under **platform**. The same applies to the bytes in buckets on the object storage server of the cluster.
- **Nodes.** An organization with its own nodes (reserved with node tags) is billed for the reservations of its pods, like any other organization. If your agreement is to bill the nodes, bill them from their pool.

### How it is kept

Each hour (in UTC) is a ConfigMap in `geass-system` with these properties:

- Its name is `geass-usage-<YYYYMMDDHH>`.
- Its labels are `geass.dev/usage-hour` and `geass.dev/usage-day`.
- Geass keeps it for 62 days.

Only the operator that holds the leader lease meters, and only where the controllers run. A dashboard-only process (`--controllers=false`) reads the hours and never writes them.

An hour records up to which time it has been metered. Each minute adds only the time after that. If an operator restarts within three minutes, it continues where the hours end. It does not count a minute two times, and it does not lose a minute. The meter does not count time that it did not see, for example when it was off or away for longer. That time shows: the `meteredSeconds` of the hour is less than 3600. An hour is **closed** when it is metered to its end, or five minutes after it ends. After that, the hour never changes.

When you turn the meter off, it stops. It keeps the hours that it stored until one of these events:

- You turn the meter on again. The meter then deletes the hours that are older than 62 days.
- You delete the hours:

```bash title="Delete every stored hour"
sudo k3s kubectl -n geass-system delete configmap -l geass.dev/usage-hour
```

### Read it

`GET /api/v1/usage/hours` answers with every hour from `from` to `to`, for every organization. With `?org=`, it answers for one organization. `from` and `to` use RFC 3339. The default is the last 24 hours, and the maximum is 31 days at a time.

The request needs an admin who acts for the platform: a platform administrator, or an install-wide admin token. The people and tokens of an organization, viewers, observers, and project tokens get `403`.

```bash title="What each organization reserved this morning"
curl -fsS -H "Authorization: Bearer $GEASS_TOKEN" \
  "https://geass.example.com/api/v1/usage/hours?from=2026-10-23T09:00:00Z&to=2026-10-23T12:00:00Z" \
  | jq -r '.hours[] | .start as $hour | .orgs[] | [$hour, .org, .workspace, .cpuCoreHours, .memoryGiBHours, .storageGiBHours] | @tsv'
```

To bill as you go, do these steps:

1. About every hour, ask from the `next` of the last answer.
2. Store each hour by its start, organization, and workspace. Replace what you stored for that hour before.

An hour that is still open (`"closed": false`) comes again in the next answer, until it closes. The [REST API](/docs/reference/rest-api/#usage-by-organization) has the whole answer.

## Troubleshooting

**The page says "Capacity unavailable".**
No node is ready and schedulable yet, or Geass could not read the node list. Run `kubectl get nodes`. If the only node of a cluster is cordoned, reserved, or not ready, the cluster has no capacity for general work.

**"Healthy nodes" shows 3 but fewer are schedulable.**
Healthy counts ready nodes. Schedulable leaves out cordoned nodes and nodes that are reserved for a tag. Uncordon the node, or remove the reservation in **Edit** on the node.

**A project shows less "using" than I expect, or "using" is missing.**
The "using" figures need Prometheus. Without Prometheus, the cells show only the reserved amount, and the table shows "Live use appears once monitoring is installed." Run `kubectl -n monitoring get pods`.

**The numbers differ from `kubectl top`.**
`kubectl top` shows live use. This page shows reservations. Both are correct. They answer different questions.

**The cluster looks full, but nothing is running.**
Pods that wait for a node still reserve capacity on this page. Look for pods that stay in `Pending`: `kubectl get pods -A --field-selector=status.phase=Pending`.

**A node is red in the rail but the page shows Ready.**
The bar turns red for pressure, and also for a down node. Read the line under the bars, or the **Pressure** column.

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
