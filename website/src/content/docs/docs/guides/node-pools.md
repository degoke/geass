---
title: "Node pools and autoscaling"
description: "Geass can create cloud machines, or join servers that you own, when services cannot start, and remove idle ones. Settings, safety, and fixes. Experimental."
---

A pool is a group of servers that Geass adds and removes for you:

- An **autoscaling pool** adds a server when services cannot start because the cluster is full. It removes a server
  when the server is idle.
- A **fixed pool** keeps exactly the number of servers that you ask for.

The servers come from a cloud, where Geass creates the machines, or from a list of servers that you own.

:::caution
Start with a small **Most nodes** on a cluster that you can afford to break, and watch the first machines start. Pools
and the cloud drivers are experimental. Only the Amazon EC2 driver has run against a live account. The other eleven
drivers were tested only against a simulation of the cloud API. See
[How far to trust it](/docs/guides/connectors/clouds/#how-far-to-trust-it).
:::

## Before you start

- You must be an admin, or use a workspace token with **Full access**. A viewer can read pools.
- For a cloud pool, add a [connection for machines](/docs/guides/connectors/clouds/) first. A pool of your own servers
  needs no connection.
- Every machine that a pool creates is billed to the cloud account until Geass deletes it. **Most nodes** is the limit on
  that cost.
- The new machines must reach the first server. Geass opens some network paths for you. See
  [How a machine joins](#how-a-machine-joins).
- Select a size that is worth the cost. Each server keeps about a quarter of a CPU and 700 MiB of memory for itself. A machine
  smaller than 2 vCPU and 4 GiB leaves little room for your services.

A new machine takes minutes to start. A pool helps with load that lasts, not with a spike of a few seconds.

## Create a pool

To see the sizes and prices of a cloud, cheapest first, run this command:

```bash
geass sizes --connection hetzner --region fsn1 --min-vcpus 2 --min-memory 4 --limit 10
```

1. In the left rail, open **Pools**.
2. Click **Add pool**.
3. Enter a **Name**: lowercase letters, numbers, and hyphens, at most 48 characters.
4. Under **Where do the machines come from?**, select an option:
   - **A cloud**: select the **Cloud connection** and the **Machine size**. **Region** is optional.
   - **My servers**: list the servers. See [Pools of servers you own](#pools-of-servers-you-own).
5. Enter **Fewest nodes** and **Most nodes**. **Most nodes** is the cost cap.
6. In **Data on these nodes**, select what the servers can hold. See
   [What runs on a pool's nodes](#what-runs-on-a-pools-nodes).
7. To follow the load, turn on **Autoscale**.
8. Read the cost in the form, and then click **Create pool**. If **Fewest nodes** is above 0, Geass starts to create
   machines immediately.

The page of the pool shows its status, its servers, and its settings. To change a setting, edit it and click **Save
changes**. A change to the size, region, or image applies only to machines that the pool creates later.

### From the command line

```bash
# An autoscaling pool: no servers until services wait, then up to 5
geass pools create burst --connection hetzner --region fsn1 --size cx23 \
  --fallback-size cx33 --min 0 --max 5 --autoscale

# A fixed pool of two servers
geass pools create fixed --connection hetzner --size cx23 --min 2 --max 2 --no-autoscale

geass pools show burst
```

`--max` is required. If `--max` is above `--min`, give `--autoscale` or `--no-autoscale`. See
[`geass pools`](/docs/reference/cli/#geass-pools) for every flag.

## Fixed or autoscaling

| | Fixed pool | Autoscaling pool |
| --- | --- | --- |
| Number of servers | Exactly **Fewest nodes** | From **Fewest nodes** to **Most nodes**, with the load |
| What adds a server | You raise **Fewest nodes**, or a server is lost | Services that cannot start, or **Keep empty nodes ready** |
| What removes a server | You lower **Fewest nodes** | A server that stays idle, as [How a pool removes nodes](#how-a-pool-removes-nodes) tells |

A fixed pool is the simple way to rent machines through Geass. The pool creates the machines and replaces a machine that
the cloud takes back.

## Settings

The form has these settings:

| Setting | Meaning |
| --- | --- |
| **Region** | The place at the cloud, in the words of that cloud. Empty means the region of the connection, or the choice of the cloud. |
| **Machine size** | The size name of the cloud. |
| **Fallback sizes**, **Fallback regions** | Up to five each. See [Fallback sizes and regions](#fallback-sizes-and-regions). |
| **Use spot machines** | Cheaper machines that the cloud can take back. AWS, Azure, Google Cloud, Oracle Cloud, and the webhook driver use this setting. |
| **Fewest nodes** | 0 to 200. A fixed pool has exactly this number. |
| **Most nodes** | 0 to 200. The pool never has more. |
| **Data on these nodes** | `None` (default) or `Allow`. |
| **Suspend scaling** | On the page of a pool only. The pool stops adding and removing servers. The servers stay and continue to cost money. |
| **Image** | Under **More options**. The operating system image. The default is the Ubuntu 24.04 of the cloud. The image must read cloud-init and let the user `geass` use `sudo`. |
| **Address Geass uses** | Under **More options**. `Public` (default), or `Private` when the machines share a private network with the first server. |
| **Machines no node owns** | Under **More options**. See [Machines no node owns](#machines-no-node-owns). |
| Placement | Under **More options**. The region, site, and tags that the servers carry. You set placement only when you create the pool. |

When **Autoscale** is on, these settings show. **Use the defaults** sets them all back.

| Setting | Default | Meaning |
| --- | --- | --- |
| **Wait before adding a node** | 30 seconds | A service must wait this long before the pool adds a server for it. |
| **Add at most** | 5 | The most servers that the pool adds in one step. |
| **A node is idle below** | 50 % | A server is idle when its services reserve less than this share of its CPU or memory. |
| **Remove an idle node after** | 10 minutes | How long a server must be idle before the pool removes it. |
| **No removals after adding a node for** | 10 minutes | The pool removes nothing for this time after it adds a server. |
| **Keep every node at least** | 0 minutes | The pool does not remove a server younger than this. Use 55 at a cloud that bills each started hour. |
| **Keep empty nodes ready** | 0 | Empty servers that wait for a burst. They count against **Most nodes** and cost money. |

## What runs on a pool's nodes

Servers of a pool come and go, so by default they hold no data. **Data on these nodes** has two values:

- `None` (the default): only services run there. Databases, buckets, and volumes never do. The **Nodes** table on the
  **Usage** page marks these servers **Ephemeral**.
- `Allow`: anything runs there, like on any other server. The pool never removes a server that holds a volume.

`None` has these effects:

- **Your services restart one time.** When you create the first pool with `None`, every service restarts one time, as at
  a deploy. After that, services can run on these servers. The same happens when you delete the last such pool. Do it at
  a time that suits you.
- **High availability does not count them.** These servers do not help a cluster get ready for a
  [highly available database](/docs/operations/high-availability/#check-readiness).
- **DNS never points at them.** The address of the dashboard and your domains comes from servers that are in no pool.
- **The Usage summary leaves them out.** The **Summary** counts room that every kind of work can use. See
  [Usage and capacity](/docs/guides/usage-and-capacity/).

A project with a region runs only on servers in that region. A project that requires tags runs only on servers with
those tags. Give the pool the region and tags of the projects that it must help. If you do not, the services of those
projects wait for a server that the pool never adds. See [Scaling and placement](/docs/guides/scaling-and-placement/).

## How a pool adds nodes

An autoscaling pool looks about every 10 seconds. It adds servers when all of these conditions are true:

- A service cannot start because no server has room.
- It waited longer than **Wait before adding a node**.
- It can run on a server of this pool, and it fits on one.

The pool calculates how many servers the waiting services need, and adds that number. It adds at most **Add at most**,
and never goes above **Most nodes**. Servers that are starting count toward the maximum.

For example, a size of 4 vCPU and 8 GiB offers about 3.75 CPU and 7.3 GiB to services. Six services that each reserve 1
CPU and 2 GiB cannot start. Three fit on one server, so the pool adds two servers.

The pool cannot help a service that reserves more than a whole server offers. The pool then says
`2 pods need more than a node of pool burst offers`. At its maximum, the pool says
`N pods cannot start and the pool is at its maximum of M nodes`.

### The capacity check

[The capacity check](/docs/guides/scaling-and-placement/#the-capacity-check) refuses work that does not fit the
servers. With an autoscaling pool, the check also asks if the work fits when the pool grows to **Most nodes**. If it
does, Geass accepts the work, and the work waits for the new servers. A pool counts only when it is `Ready` or `Scaling`
and Geass knows the size of its servers. A pool with **Data on these nodes** `None` counts for services only, never for
databases.

## How a pool removes nodes

An autoscaling pool removes a server when all of these conditions are true:

- The server is idle: its services reserve less than **A node is idle below**.
- Everything on the server fits on the other servers.
- The server stayed idle for **Remove an idle node after**.
- The server is older than **Keep every node at least**.
- Nothing keeps the server. See [What keeps a node](#what-keeps-a-node).

The pool removes nothing while services wait for room, while a server starts, during **No removals after adding a node
for**, or during a K3s upgrade. It never goes below **Fewest nodes**.

The pool removes empty servers together, up to ten at a time. Otherwise, it removes the least-used server. It moves the
services off first. If the server does not empty in 10 minutes, the pool puts it back into service and tries again 3
minutes later.

A service with one copy is down while it starts on another server. Run at least two copies of a service that must not
stop.

### What keeps a node

The pool does not remove a server that runs any of these items:

- Work with data on the server: a volume, or a folder on the server disk.
- A system component of the cluster.
- Work that you marked as one to keep, as [Under the hood](#under-the-hood) shows.

The pool also keeps a server that holds a volume copy, a server that you marked to keep, and a server that someone else
took out of service. To see why servers stay, run `geass pools show NAME` and read the message.

## Fallback sizes and regions

A cloud can run out of a size in a place, or an account can reach a limit. Then the new server waits. After three
minutes, the pool tries the next choice: every size in the first region, then every size in the next region. For
example, with the size `cx23`, the fallback size `cx33`, and the fallback region `nbg1`, the pool tries these choices in
order:

1. `cx23` in `fsn1`.
2. `cx33` in `fsn1`.
3. `cx23` in `nbg1`.
4. `cx33` in `nbg1`.

One hour later, the pool goes back to its first choice. The page of the pool and `geass pools show` show the size in
use. A fallback can cost more. Select only fallbacks that you are willing to pay for.

## Cost

- **The cap.** A pool never has more than **Most nodes** machines. The page of the pool shows the cost now and the most
  that the pool can cost.
- **The estimate.** The estimate is the number of machines times the list price of the size. Geass cannot show it for
  Amazon, Google Cloud, Oracle Cloud, or OVHcloud, because their API gives no price. The estimate does not include taxes,
  traffic, public IPv4 addresses, or disks that the cloud bills separately.
- **Hours.** Linode, OVHcloud, and Vultr bill each started hour. Oracle Cloud bills each second, with a one-minute
  minimum. For other clouds, read the terms of your provider. Where a started hour is billed, set **Keep every node at
  least** to 55.
- **Idle cost.** **Fewest nodes** and **Keep empty nodes ready** cost money while they wait. If cost is more important
  than speed, keep both at 0.

## How a machine joins

Geass creates an SSH key for the cluster and a host key for each machine. It creates the machine with a user `geass` and
those keys. The machine gets no secret at creation. Geass then connects over SSH, checks the host key, and installs the
server software. This is the same as for [a server that you add](/docs/getting-started/add-servers/#under-the-hood).

These network paths must be open:

| From | To | Port | Who opens it |
| --- | --- | --- | --- |
| The first server | The new machine | 22/tcp and 10250/tcp | Geass, in a firewall that it creates at the cloud. You open them on Oracle Cloud and OVHcloud, or when you name your own firewall in the settings of the pool. |
| The new machine | The first server | 6443/tcp | You, unless you [let Geass open the control plane](#let-geass-open-the-control-plane). |
| Every server | Every other server | 51820/udp, in a multi-site cluster | Geass, in the firewall that it creates. |

In a cluster installed in the default mode, servers also talk on 8472/udp, and no driver opens it. Then a pool needs one
of these setups:

- Your own firewall that allows the port. Name it in the settings of the pool.
- Machines on the same private network as the first server, with **Address Geass uses** set to `Private`.

For machines at another provider, use a cluster installed in [multi-site mode](/docs/operations/multi-provider/).

The firewall that Geass creates lets SSH in only from the addresses of the control plane. If the control plane has only
a private address, and Geass reaches the machine on its public address, SSH fails. To fix this problem, use `Private`
on the same network, or install in multi-site mode.

### Let Geass open the control plane

If a cloud firewall protects the first server, Geass can open 6443/tcp for each new machine, and 51820/udp in a
multi-site cluster. Geass closes these ports again when the machine goes. This works only for an AWS security group and
a Hetzner firewall. Geass has no dashboard option or command for this setting yet. See
[Under the hood](#under-the-hood).

The connection must be a connection for machines at the cloud where the first server runs. Geass marks its rules with
the description `geass` on AWS and `geass-access` on Hetzner. It never changes a rule that it did not make.

## Machines no node owns

An orphan is a machine at the cloud that belongs to the pool but to no server. It occurs when the cloud is unreachable
during a removal, or when Geass stops between the creation and the record of a machine. An orphan can still cost money.

Every ten minutes, the pool compares the machines at the cloud with its servers. It looks only at machines that Geass
made for this cluster. Under **More options**, **Machines no node owns** has two values:

- **Report** (default): the dashboard shows an alert that lists the machines. You delete them at the cloud.
- **Delete**: Geass deletes a machine that has had no server for 20 minutes. You cannot undo this deletion.

## Safety

| Risk | What Geass does |
| --- | --- |
| A pool grows without limit | **Most nodes** is required. Machines that start or stop count against it. |
| Machines keep failing to join | After three failures in a row, the pool pauses for 30 minutes. See [Pause and resume](#pause-and-resume). |
| A failed machine keeps billing | Geass deletes the machine of a failed server immediately. |
| Geass deletes the wrong machine | Geass deletes only by the machine ID that it recorded. |
| The cloud takes a machine back | Geass creates a new machine for the same server. |
| A machine runs, but its server is down | Geass replaces a server with **Data on these nodes** `None` after 30 minutes down. It does not when most of the pool is down. |
| A server is removed, and its machine stays | Geass releases a server only after the cloud no longer lists the machine. |
| Credentials expire | The connection warns two weeks before. See [Cloud connections for machines](/docs/guides/connectors/clouds/#what-the-connection-says-about-itself). |

## Pool states

The pool shows one of these states, with a message that says why:

| State | Meaning |
| --- | --- |
| `Ready` | The pool has the servers that it wants, and they are ready. |
| `Scaling` | The pool adds or removes servers, for example `adding 2 nodes`. |
| `Degraded` | The pool has a problem that it cannot fix. Examples are invalid settings, or a missing or wrong connection. The pool keeps its servers and adds no more. |
| `Paused` | Three machines in a row failed. See [Pause and resume](#pause-and-resume). |
| `Suspended` | **Suspend scaling** is on. |
| `Pending` | The cluster of the pool does not exist. |

Each server of a pool also shows a state:

| State | Meaning |
| --- | --- |
| `Pending` | Not started. |
| `Creating` | Geass creates the machine, or waits for it to accept SSH. After 10 minutes without a running machine, Geass stops. |
| `Joining` | The server software is installed, and Geass waits for the server. After 15 minutes, Geass stops. |
| `Ready` | The server is ready. |
| `Failed` | The machine did not join, and Geass deleted it. The message says why. Geass clears the failed server 30 minutes later. |

## Pause and resume

After three machines in a row fail to join, the pool pauses for 30 minutes. This stops a pool from buying machines that
never join. Usual causes are a closed firewall, an image that does not start, or a missing permission. The message of
the pool says what the last machine reported.

Fix the cause. Then, on the **Pools** page or the page of the pool, click **Resume**. From a terminal, run
`geass pools resume burst`.

After a resume, three more failures pause the pool again. If the 30 minutes end without a resume, the pool tries one
time. If that machine fails, the pool pauses again. At a cloud that bills by the hour, each failed machine can cost an
hour.

## Pools of servers you own

A pool can use servers that you already have. When the pool needs a server, Geass joins one over SSH. When the pool no
longer needs it, Geass moves the services off and removes the server software. Geass does not turn off the server, reset
it, or change anything else on it.

1. Under **Where do the machines come from?**, select **My servers**.
2. For each server, enter the **Name**, the **Address**, the **SSH user**, and the **Port**. To add a row, click **Add a
   server**.
3. In **SSH private key**, paste one key for all of the servers.
4. Optionally, in **Host keys**, paste the host keys of the servers. See
   [Pin the server's host key](/docs/getting-started/add-servers/#pin-the-servers-host-key-recommended).

You cannot change the list of servers after you create the pool. **Most nodes** cannot be more than the number of
servers. The servers need what a [server that you add](/docs/getting-started/add-servers/#before-you-start) needs: a
`root` user or passwordless `sudo`, a key without a passphrase, and the network paths.

From a terminal, use a JSON file of servers. Each server has `name`, `address`, `sshUser`, and optionally `sshPort` and
`sshKeySecret`:

```bash
geass pools create spare --inventory-file ./servers.json --inventory-key-file ~/.ssh/geass_ed25519 --max 2 --autoscale
```

These servers also hold no data unless you select `Allow`.

## Delete a pool or a node

To delete a pool, open the page of the pool, and click **Delete pool**. Enter the name to confirm. From a terminal, run
`geass pools delete burst`.

For each server, Geass moves the services off for up to five minutes, and removes the server. For a cloud pool, Geass
then deletes the machine. A pool of your own servers only removes the server software. Services move to other servers
if they have room. If they do not, they wait.

:::caution
Delete the pool before you remove its connection. Without the connection, nothing can delete the machines. The
dashboard, the command line, and the API refuse to remove a connection that a pool still uses.
:::

To remove the connection, do these steps:

1. Delete the pool.
2. Wait until its servers are gone. To check, run `geass nodes list`.
3. Check the console of the cloud.
4. Remove the connection.

## Troubleshooting

| Problem | Cause and fix |
| --- | --- |
| `Degraded`, and the connection "is not for compute" | Add the Compute purpose to the connection, or make a connection for it. For AWS, use a connection different from the one for buckets. |
| `Degraded`, and the connection "belongs to project ..." | A connection for machines must not belong to a project. Make one for the cluster. |
| `Degraded`, and "... does not read the setting ..." or "... needs the settings ..." | Remove the setting, or add it. Azure and Oracle Cloud need `subnetId`. `geass providers CLOUD` lists the settings. |
| `Degraded`, with the message of the connection | The cloud refuses the credentials. Run `geass connections show NAME`, and see [Troubleshooting](/docs/guides/connectors/clouds/#troubleshooting). |
| `Paused` | Read the message, fix the cause, and [resume](#pause-and-resume). |
| Services wait, and the pool adds nothing | Check these items: **Autoscale** is on, the pool is not suspended or paused, and the wait is longer than **Wait before adding a node**. The placement of the pool must match the projects, and the services must fit the size. The pool must also be below **Most nodes**. A service made before the first pool needs its one restart. |
| Services wait, and the pool is at its maximum | Raise **Most nodes**, or lower what the services reserve. |
| A server stays `Creating` with "waiting for the provider to start the machine" | The cloud is slow, full, or at a limit. After three minutes, the pool tries its fallback. After 10 minutes, Geass deletes the machine. |
| A server stays `Creating` with "waiting for the machine to accept SSH ..." | The firewall does not let SSH in from the control plane. See [How a machine joins](#how-a-machine-joins). Geass stops after 18 minutes. |
| `Failed`: "opening the control plane's firewall for the machine: ..." | Check the connection and the firewall ID in [Let Geass open the control plane](#let-geass-open-the-control-plane). Only AWS and Hetzner work. |
| `Joining`, then `Failed` after 15 minutes | The machine cannot reach 6443/tcp on the first server, or the port between servers is closed. Open the ports. Geass deletes the machine, so its log is gone. |
| Machines at the cloud that no server owns | Delete them at the cloud, or select **Delete** in **Machines no node owns**. |
| A server does not go away after a delete | Read its message. The server waits for the machine to be deleted. If the connection is gone for good, see [Under the hood](#under-the-hood). |
| The pool never removes a server | See [What keeps a node](#what-keeps-a-node). |
| The services restarted when I created the first pool | This is expected, one time. See [What runs on a pool's nodes](#what-runs-on-a-pools-nodes). |
| The cloud bills more than the estimate | The estimate is the list price of the machines only. |

## Under the hood

A pool is a `GeassNodePool` in `geass-system`. Each server of the pool is a `GeassNode` named `<pool>-<five
characters>`. Its nodes carry the labels `geass.dev/pool=<pool>`, `geass.dev/autoscaled=true`, and, for `None`,
`geass.dev/storage=none` with the taint `geass.dev/ephemeral=true:NoSchedule`. Only services tolerate that taint. Geass
also turns off Longhorn scheduling on these nodes. The [GeassNodePool reference](/docs/reference/resources/geass-node-pool/)
lists every field.

```bash
sudo k3s kubectl -n geass-system get geassnodepools
sudo k3s kubectl -n geass-system get geassnodes -l geass.dev/pool=burst \
  -o custom-columns='NAME:.metadata.name,PHASE:.status.phase,MACHINE:.status.cloud.instanceID,MESSAGE:.status.message'
```

A pool as a manifest:

```yaml title="burst-pool.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassNodePool
metadata:
  name: burst
  namespace: geass-system
spec:
  connectionRef: hetzner
  region: fsn1
  size: cx23
  fallbackSizes: [cx33]
  fallbackRegions: [nbg1]
  storage: None
  minNodes: 0
  maxNodes: 5
  autoscaling:
    enabled: true
    minNodeLifetimeMinutes: 55
  orphanPolicy: Report
  placement:
    region: eu
    zone: hetzner-fsn1
```

A pool of your own servers lists them under `spec.inventory.hosts`, each with `name`, `address`, `sshUser`, and
`sshKeySecretRef`.

These annotations control the autoscaler:

- `geass.dev/safe-to-evict: "false"` on a pod keeps its node. `"true"` lets the pool move a pod that it would otherwise
  keep, for example one with only a cache on disk.
- `geass.dev/scale-down-disabled: "true"` on a node keeps the node:
  `sudo k3s kubectl annotate node NAME geass.dev/scale-down-disabled=true`.
- `geass.dev/resume=true` on a pool resumes it, as **Resume** does.
- `geass.dev/abandon-instance=true` on a `GeassNode` releases a server whose connection is gone for good. Geass then
  leaves the machine at the cloud, and you delete it there.

The pool also keeps a node with a bare pod, a pod in `kube-system`, a pod with a volume claim, a host path, or an
`emptyDir` on disk, and a node that someone cordoned. The operator log at verbosity 1 gives the reason for each node.

Geass keeps the SSH key of the cluster in the Secret `geass-nodepool-ssh`, and the host key of each machine in
`<node>-hostkey`. At the cloud, machines carry the labels or tags `geass-managed=true`, `geass-cluster`, `geass-pool`, and
`geass-node`. To drain a node, Geass uses the eviction API, so it honors a `PodDisruptionBudget` that you add. Geass
makes none.

To let Geass open the control plane, run this command on the first server, with your connection, firewall ID, and
region:

```bash
sudo k3s kubectl -n geass-system patch geasscluster default --type merge \
  -p '{"spec":{"network":{"controlPlaneFirewall":{"connectionRef":"aws-compute","target":"sg-0abc123","region":"eu-north-1"}}}}'
```

`kubectl delete` does not check that a connection is in use. Use the dashboard or the command line to remove a
connection.

## Related

- [Cloud connections for machines](/docs/guides/connectors/clouds/)
- [Add servers](/docs/getting-started/add-servers/)
- [Scaling and placement](/docs/guides/scaling-and-placement/)
- [Usage and capacity](/docs/guides/usage-and-capacity/)
- [Nodes at more than one provider](/docs/operations/multi-provider/)
- [Storage](/docs/guides/storage/)
- [GeassNodePool](/docs/reference/resources/geass-node-pool/)
- [CLI commands](/docs/reference/cli/#geass-pools)
- [Security](/docs/operations/security/)
