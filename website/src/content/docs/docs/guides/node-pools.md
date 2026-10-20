---
title: "Node pools and autoscaling"
description: "Let Geass create machines at a cloud, or join servers you own, when pods cannot start, and remove them when idle. Settings, safety and fixes. Experimental."
---

A pool is a group of nodes that Geass adds and removes for you. When pods cannot start because the cluster is full, an autoscaling pool creates a machine at a cloud, installs K3s on it and joins it to the cluster. When a machine sits idle, the pool moves its pods away and deletes it. A pool that does not autoscale keeps exactly the number of nodes you ask for. This page explains how a pool decides, how to create one, what Geass does with your money and your machines, and what to check when a pool stops.

:::caution
Node pools, and the cloud drivers behind them, are **experimental**. Amazon EC2 has been run against a live account, in one region (what ran is listed under [How far to trust it](/docs/guides/connectors/clouds/#how-far-to-trust-it)). The other eleven drivers were written from the provider's official API documentation and tested only against an in-memory simulation of that API, so a quirk of a real account (a quota, a missing permission, a slow image) can still surprise them. Start with a small **most nodes** on a cluster you can afford to break, and watch the first machines come up. See [Cloud connections for machines](/docs/guides/connectors/clouds/#how-far-to-trust-it).
:::

## Before you start

- You are an admin. Making, changing and deleting a pool, and listing a cloud's sizes, need the admin role: a dashboard admin, or a workspace token with **Full access**. A viewer or an observer can read pools. A project token cannot reach them at all.
- A connection for machines to the cloud you want to use. It belongs to the cluster, not to a project. Add it first: [Cloud connections for machines](/docs/guides/connectors/clouds/). A pool of servers you own needs no connection.
- A cloud account that you can afford to let Geass spend in. Every machine a pool creates is billed to the account of its connection until the machine is deleted. **Most nodes** is the limit on that, and the pool cannot be made without it.
- Network paths between the new machines and the control plane. Geass opens some of them for you, in a firewall it makes at the cloud, and leaves the rest to you: see [How a machine joins](#how-a-machine-joins).
- A size worth buying. A node reserves about 250m CPU and 700 MiB of memory for K3s and the system pods that run on every node, so a machine under 2 vCPU and 4 GiB leaves little for your services. A new machine also takes minutes to boot, install K3s and pull images, so a pool helps with load that lasts, not with a spike of seconds.

## The words

| Word | Meaning |
| --- | --- |
| **Pool** | A `GeassNodePool`: a rule for how many nodes of one kind the cluster has, and where they come from. |
| **Connection for machines** | A `GeassCloudConnection` with the purpose Compute: the credentials Geass uses to create and delete machines at a cloud. See [Cloud connections for machines](/docs/guides/connectors/clouds/). |
| **Inventory pool** | A pool that draws on a list of servers you own, instead of a cloud. Geass joins one when it needs room, and takes K3s off it when it does not. |
| **Fixed pool** | A pool with autoscaling off. It has exactly **fewest nodes** nodes. |
| **Autoscaling pool** | A pool that adds nodes when pods cannot start and removes idle ones, between **fewest nodes** and **most nodes**. |
| **Ephemeral node** | A node of a pool whose **data on these nodes** is `None` (the default). Databases, buckets and volumes never run there, so the node can be removed at any time. The dashboard marks it **Ephemeral**. |
| **Fallback** | A second size or region that a pool tries when the cloud has no room for the first. |
| **Orphan machine** | A machine at the cloud that carries a pool's labels and belongs to no node. It may still be billing you. |

## Create a pool

Check the cost before you commit. The sizes of a cloud, cheapest first, with the price for an hour where the cloud states one:

```bash
geass providers
geass connections list
geass sizes --connection hetzner --region fsn1 --min-vcpus 2 --min-memory 4 --limit 10
```

### In the dashboard

1. Open **Pools** in the left rail and choose **Add pool**.
2. Give it a **Name**: lowercase letters, numbers and hyphens, at most 48 characters. Each node's name is made from it.
3. Under **Where do the machines come from?** choose **A cloud** (then pick the **Cloud connection**, the **Region** and the **Machine size**) or **My servers** (then list the servers, see [Pools of servers you own](#pools-of-servers-you-own)).
4. Set **Fewest nodes** and **Most nodes**. **Most nodes** is required. It is the cost cap.
5. Choose what the nodes may hold under **Data on these nodes**, and whether to **Autoscale**. Under **Autoscale** you can change the numbers in the table below.
6. Choose **Create pool**. Nothing is created before that. A pool with **Fewest nodes** above 0 starts creating machines at once.

The pool's page shows its status, its nodes with their machine IDs and addresses, and its settings, which you change there with **Save changes**. A paused pool shows **Resume**. Size, region, image and settings apply to machines made from then on, and the nodes the pool has stay as they are.

### With the command line

```bash
# An autoscaling pool: no nodes until pods wait, then up to 5
geass pools create burst --connection hetzner --region fsn1 --size cx23 \
  --fallback-size cx33 --min 0 --max 5 --autoscale

# A fixed pool of two nodes
geass pools create fixed --connection hetzner --size cx23 --min 2 --max 2 --no-autoscale

geass pools show burst
```

`--max` is required. When `--max` is above `--min` you must also say `--autoscale` or `--no-autoscale`, because a pool that does not autoscale ignores everything above `--min`, which is rarely what was meant. See [`geass pools`](/docs/reference/cli/#geass-pools) for every flag.

### With a manifest

```yaml title="burst-pool.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassNodePool
metadata:
  name: burst
  namespace: geass-system
spec:
  connectionRef: hetzner        # a connection for Compute, not scoped to a project
  region: fsn1                  # the cloud's own place name
  size: cx23
  fallbackSizes: [cx33]
  fallbackRegions: [nbg1]
  storage: None                 # the default: services only
  minNodes: 0
  maxNodes: 5                   # required: the cost cap
  autoscaling:
    enabled: true
    minNodeLifetimeMinutes: 55  # the clouds that bill each started hour
  orphanPolicy: Report
  placement:                    # what the nodes carry, so projects in this region can use them
    region: eu
    zone: hetzner-fsn1
```

```bash
kubectl apply -f burst-pool.yaml
kubectl -n geass-system get geassnodepools
```

`kubectl get` shows the phase, the minimum and maximum, how many nodes exist and are ready, and the size. The full field list is in [GeassNodePool](/docs/reference/resources/geass-node-pool/).

## Fixed or autoscaling

| | Fixed pool | Autoscaling pool |
| --- | --- | --- |
| How many nodes | Exactly **fewest nodes** | Between **fewest nodes** and **most nodes**, following the load |
| What adds a node | You raising **fewest nodes**, or a node being lost | Pods that cannot start, or **spare nodes** |
| What removes a node | You lowering **fewest nodes**. Nodes that are not up yet go first, then the newest | A node under-used for long enough, as [described below](#how-a-pool-removes-nodes) |
| Role of **most nodes** | A ceiling that also counts nodes being replaced | The cost cap |

A fixed pool is the simple way to rent machines through Geass: it makes them, keeps the number right, and replaces one the cloud takes back. It removes a surplus node without the checks of an autoscaling pool: the node is drained for up to five minutes and then deleted.

## Settings

| Field | Default | Meaning |
| --- | --- | --- |
| `spec.cluster` | `default` | The `GeassCluster` the nodes join. |
| `spec.connectionRef` | none | The connection for machines that creates them. A pool has this or `inventory`, not both. |
| `spec.inventory.hosts` | none | Servers you own, instead of a cloud. See [Pools of servers you own](#pools-of-servers-you-own). |
| `spec.region` | the connection's default region, or where the cloud chooses | The cloud's own place: a location, zone or region. What it means is [per cloud](/docs/guides/connectors/clouds/#the-clouds). |
| `spec.size` | none | The cloud's name for a machine size. Required for a pool that creates machines. |
| `spec.fallbackSizes`, `spec.fallbackRegions` | none | Up to five each, tried in order when the cloud has no room, or the account is at a limit. See [Fallback sizes and regions](#fallback-sizes-and-regions). |
| `spec.image` | the cloud's Ubuntu 24.04 | The operating system image. It must read a cloud-init document, because the machine's login and host key come from one, and let the user `geass` use `sudo`. |
| `spec.spot` | `false` | Ask the cloud for its cheaper machines that it may take back. Honoured by AWS, Azure, Google Cloud, Oracle Cloud and the webhook driver. The other drivers ignore it. |
| `spec.addressType` | `Public` | Which address of a machine Geass reaches it on: `Public`, or `Private` for a pool whose machines share a private network with the control plane. |
| `spec.settings` | none | Values specific to the cloud, such as a subnet or a firewall. Each cloud lists its own. |
| `spec.placement` | none | The region, site and tags the nodes carry (the same fields as on a [node](/docs/reference/resources/geass-node/)). |
| `spec.storage` | `None` | `None` or `Allow`. See [What runs on a pool's nodes](#what-runs-on-a-pools-nodes). |
| `spec.minNodes` | `0` | The fewest nodes the pool has, 0 to 200. A fixed pool has exactly this many. |
| `spec.maxNodes` | required | The most nodes the pool ever has, 0 to 200, and at least `minNodes`. |
| `spec.orphanPolicy` | `Report` | `Report` or `Delete`. See [Machines no node owns](#machines-no-node-owns). |
| `spec.suspend` | `false` | Stop adding and removing nodes. The nodes the pool has stay, and keep costing what they cost. |

The settings of `spec.autoscaling`. Switching `enabled` on with nothing else set uses every default.

| Field | Default | Range | Meaning |
| --- | --- | --- | --- |
| `enabled` | `false` | | Let the pool follow the load. Off, the pool keeps exactly `minNodes`. |
| `scaleUpDelaySeconds` | `30` | 1 to 3600 | How long a pod must have been unable to start before the pool adds a node for it, so that a pod that is only slow to be scheduled does not buy a machine. |
| `maxScaleUp` | `5` | 1 to 50 | The most nodes added in one step. |
| `scaleDownUtilizationPercent` | `50` | 1 to 100 | A node whose pods reserve less than this share of its CPU or its memory, whichever is higher, is a candidate for removal. |
| `scaleDownAfterMinutes` | `10` | 1 to 1440 | How long a node must have been a candidate before it is removed. |
| `scaleDownDelayAfterAddMinutes` | `10` | 1 to 1440 | No node is removed for this long after the pool last added one. |
| `minNodeLifetimeMinutes` | `0` | 0 to 10080 | A node is not removed before it is this old. Many clouds bill each started hour, so a node removed after ten minutes costs a full hour. 55 keeps it for the hour that is paid for anyway. |
| `spareNodes` | `0` | 0 to 20 | Empty nodes kept ready for a burst, so that new pods find room at once instead of waiting minutes for a machine. They count against `maxNodes` and cost money while they wait. |

A pool's name is at most 48 characters, because each node's name is the pool's name, a hyphen and five characters, and a node's name may not pass 55.

## What runs on a pool's nodes

Nodes of a pool come and go, so by default Geass keeps data off them. `spec.storage` says which of two things a pool is.

| `storage` | Node taint | What may run there |
| --- | --- | --- |
| `None` (the default) | `geass.dev/ephemeral=true:NoSchedule` | Services only. Databases, object storage servers and volume copies never. |
| `Allow` | none | Anything, like any other worker. The autoscaler never removes a node while it holds a volume copy or runs a pod that uses a volume claim. |

What follows from `None`:

- **Services tolerate the taint, and nothing else does.** A service keeps nothing on its node, so Kubernetes can move it. Databases, buckets, backups and builds do not tolerate the taint and stay off these nodes.
- **The first pool with `storage: None` makes every service roll once.** A service's pods must carry the toleration before they can use the node, and Geass adds it only while such a pool, or a single server added with `storage: None`, exists, so a cluster that uses neither sees no change. When you create the first one, each service is rolled once to add it, which restarts its pods the way a deploy does. The same happens, the other way round, when the last one is deleted. Do it at a time that suits you.
- **High availability does not count them.** A node that cannot hold a database replica does not help [HA readiness](/docs/operations/high-availability/#check-readiness), so a pool of ephemeral nodes never makes a cluster ready for a highly available database.
- **DNS never points at them.** The address the dashboard and your domains resolve to is taken from nodes that are not in any pool and that may hold data, because a pool's nodes, and a server added with data kept off it, are created and removed under it.
- **Longhorn is told to keep volume copies off them.** Geass switches scheduling off for the node in Longhorn, and switches it back on only if Geass was what switched it off and the node stops being ephemeral. See [Storage](/docs/guides/storage/#nodes-that-must-not-hold-data).
- **The Usage page marks them.** The **Nodes** table has a **Pool** column with an **Ephemeral** badge, and `geass nodes list` has `POOL` and `EPHEMERAL` columns. The **Summary** counts the capacity every kind of workload may use, so it leaves out ephemeral nodes the way it leaves out a node reserved for a tag. See [Usage and capacity](/docs/guides/usage-and-capacity/).

A project that has a region runs only on nodes of that region, and one that requires tags only on nodes with them. A pool's nodes carry the `placement` you give the pool, and nothing else. Give the pool the region and tags of the projects it should help, or the pods of those projects will wait for a node the pool will never add. See [Scaling and placement](/docs/guides/scaling-and-placement/).

## How a pool adds nodes

An autoscaling pool looks about every 10 seconds.

1. **It finds the pods that wait.** A pod counts when the scheduler has said it cannot be placed for at least `scaleUpDelaySeconds`, when it could run on a new node of the pool (its node selector, affinity and tolerations allow it), and when it would fit there. A pod whose reason from the scheduler is one that no new node can change (its volume is bound somewhere a new node is not, or its volume claim is unbound) is left out, because a node made for it would only be removed again and made again.
2. **It works out how many nodes they need.** It packs the pods, largest first, onto the room that exists: each node that is coming up counts as a whole new node, and the free room of a node that became ready in the last three minutes counts too, because the scheduler takes a few seconds to notice a new node. A pod that fits nowhere needs a new node, and the pods after it are packed onto that one too. It plans with what the smallest ready node of the pool really offers, or, before there is one, with the machine's size less what K3s and the system pods keep for themselves.
3. **It creates that many, within limits.** At most `maxScaleUp` in one step, and never so many that the pool passes `maxNodes`. Nodes still being created or removed count toward the maximum, because their machines cost money.
4. **It keeps `spareNodes` empty nodes**, and at least `minNodes` nodes.

For example, a pool of machines with 4 vCPU and 8 GiB offers each node roughly 3.75 CPU and 7.3 GiB. Six pods that each reserve 1 CPU and 2 GiB cannot start. Memory allows three of them on a node, so the pool adds two nodes, not six. Pods whose reservation is larger than a whole node of the pool are never helped, and the pool says so in its message: "2 pods need more than a node of pool burst offers", followed by what a node offers.

If the pool is at its maximum, the pods wait and the pool says "N pods cannot start and the pool is at its maximum of M nodes".

Adding a node takes minutes: the cloud starts the machine, Geass logs in over SSH, installs K3s, and the node pulls the images of what is waiting. The pods start when the node is ready.

### The capacity check

[The capacity check](/docs/guides/scaling-and-placement/#the-capacity-check) refuses to create a workload that does not fit the nodes it may use. With an autoscaling pool it asks one more question first: would the workload fit if the pool grew to its maximum? If yes, the workload is let in, and its pods wait for the nodes the pool adds. If the pool is at its maximum already, the workload is refused with the usual message. A pool only counts once Geass knows what one of its nodes offers (from the size, when the cloud lists it, or from a ready node), while it is `Ready` or `Scaling`, and is not suspended. A pool of ephemeral nodes counts for services only: a database is never made room for there.

## How a pool removes nodes

A node of an autoscaling pool is removed when all of these hold:

- It is under-used. The pods that would have to move reserve less than `scaleDownUtilizationPercent` of its CPU or of its memory, whichever is higher. DaemonSet pods, finished pods and static pods are not counted: they go with the node. An empty node is always under-used.
- Everything on it would fit on the other ready nodes of the cluster. The check respects each pod's node selector and tolerations, so a pod that may only run in one region is not counted as fitting a node in another.
- It has been a candidate for `scaleDownAfterMinutes`. The timer starts again if the node stops being a candidate.
- It is at least `minNodeLifetimeMinutes` old.
- Nothing about it says to stay (the table below).

On top of that, the pool removes nothing while pods wait for room or a node is still coming up, nothing for `scaleDownDelayAfterAddMinutes` after it last added a node, nothing while K3s is being upgraded on the cluster, and never goes below `minNodes`.

When more than one node qualifies, the pool removes empty nodes together, up to ten at a time, and keeps `spareNodes` empty ones. Otherwise it removes one node that has pods, the least used. It cordons the node, asks each pod to leave through the Kubernetes eviction API, so that a pod disruption budget is honoured, and deletes the node and its machine once nothing is left.

A node that will not empty within 10 minutes is put back into service, and the pool waits 3 minutes before it tries again. New demand also takes back a node that was being emptied, because its room is the quickest to get.

Geass creates no pod disruption budget of its own. A service with one replica is down for the moment it takes to start elsewhere. Run at least two replicas of a service that cannot be interrupted, and add a `PodDisruptionBudget` of your own if you want a hold on it.

### What keeps a node

| What | Why |
| --- | --- |
| A pod with no controller (a bare pod) | Nothing would recreate it. |
| A pod that uses a volume claim, a host path, or an `emptyDir` kept on disk | It holds data on the node. An `emptyDir` in memory does not count. |
| A pod in `kube-system` | It is a system pod. |
| A pod annotated `geass.dev/safe-to-evict: "false"` | Its owner asked to be left alone. The value `"true"` overrides every one of the guesses above, so a pod that keeps only a cache on disk can be moved. |
| The node annotated `geass.dev/scale-down-disabled: "true"` | Yours: `kubectl annotate node NAME geass.dev/scale-down-disabled=true`. |
| A node someone else cordoned | A node you cordoned with `kubectl cordon` is left as it is. Geass marks the ones it cordons itself, and uncordons only those. |
| A node younger than `minNodeLifetimeMinutes` | You asked it to live at least that long. |
| A node that holds a volume copy (a pool with `storage: Allow`) | Removing it would leave a volume one copy short. |
| A K3s upgrade in progress | The upgrade is working through the nodes one at a time. |

To see why nodes stay, read the pool's message with `geass pools show NAME`, and the operator log at verbosity 1, which lists the reason for each node.

## Fallback sizes and regions

Clouds run out of a size in a place, or an account reaches a limit. A machine that cannot be made for either reason waits, because both can pass, and its node says so (`Capacity` or `Quota`). After the node has waited three minutes, the pool moves on to the next choice and makes the node again. The order is every size in the first region, then every size in the next: with `size: cx23`, `fallbackSizes: [cx33]` and `fallbackRegions: [nbg1]` it tries `cx23` in `fsn1`, then `cx33` in `fsn1`, then `cx23` in `nbg1`, then `cx33` in `nbg1`.

The pool goes back to its first choice an hour later. While a fallback is in use, the pool's page and `geass pools show` say which size is in use. A fallback may cost more than the first choice, and the cap is `maxNodes`, not a sum of money: choose fallbacks you are willing to pay for.

## Cost

- **The cap.** A pool never has more than `maxNodes` machines. That, times the price of a machine, is the most it costs for an hour. The pool's page shows what it costs now and the most it could.
- **The estimate.** The pool's `estimatedHourlyCost` is the number of its machines times the price the cloud lists for the size. Geass can show it for the clouds whose API states a price: not for Amazon, Google Cloud, Oracle Cloud or OVHcloud. It leaves out taxes, traffic, public IPv4 addresses and anything else the cloud bills on its own line.
- **Hours.** The drivers for Linode, OVHcloud and Vultr say that they bill each started hour at least. Oracle Cloud bills per second with a one-minute minimum. For the others, read your provider's terms. Where a started hour is billed, set `minNodeLifetimeMinutes` to 55 so that a node you are going to pay for anyway stays for the next burst.
- **Idle cost.** `minNodes` and `spareNodes` machines cost money while they wait. Leave both at 0 if cost matters more than speed.

## How a machine joins

For a machine it creates, Geass does what it does for a server you bring, with one difference: it makes the key and the machine, so the two can be trusted before the machine exists.

1. Geass makes one SSH key for the cluster the first time it needs one, and keeps it in the Secret `geass-nodepool-ssh` in `geass-system`. It makes a host key for each machine too, in the Secret `<node>-hostkey`.
2. It creates the machine with a cloud-init document. The document creates a user `geass` with passwordless `sudo`, and holds the cluster's **public** key, the machine's own host key and its hostname. It carries no join token and no other secret, because a machine's user data can be read from inside the machine and from the cloud's own API.
3. When the machine is running with an address, Geass logs in over SSH with the cluster's key, and checks the host key against the one it put there. If it cannot check (the host key's Secret is missing, or the machine has no address yet) the node waits and the token is never sent unchecked. It then installs K3s as an agent with the join token, the node's labels and, for `storage: None`, the taint. This is the same procedure as [Add servers](/docs/getting-started/add-servers/#what-happens-under-the-hood). A machine that refuses SSH while it finishes its first boot is waited for.
4. Geass waits for the node to be `Ready`.

What has to be reachable:

| From | To | Port | Who opens it |
| --- | --- | --- | --- |
| Control plane (the operator) | The new machine | 22/tcp | The firewall Geass makes at the cloud, for the addresses of the control plane's nodes. Where Geass makes none (Oracle Cloud, OVHcloud), or you named your own firewall in the pool's `settings`, you |
| Control plane | The new machine | 10250/tcp (kubelet) | The same |
| The new machine | Control plane | 6443/tcp | You, in the control plane's firewall or security group, unless you use [`controlPlaneFirewall`](#let-geass-open-the-control-plane) |
| Every node | Every other node | 51820/udp in a multi-site cluster | The firewall Geass makes, from anywhere, because the overlay authenticates every packet |

In a cluster installed in the default mode, the overlay is VXLAN on 8472/udp between nodes. No driver opens that port, so a pool in such a cluster needs a firewall of yours that allows it (name it in the pool's `settings`), and works for machines on the same private network as the control plane, with `addressType: Private`. For machines at another provider, use a cluster installed in [multi-site mode](/docs/operations/multi-provider/): its overlay needs only the ports above, and a pool at a second provider is part of the same experiment as any node there.

The firewall Geass makes at the cloud lets SSH and the kubelet in only from the addresses Kubernetes lists for the control plane's nodes (external and internal). If the control plane's node lists only a private address and the machine is reached on its public one, the operator's SSH arrives from an address the firewall does not allow: use `addressType: Private` in the same network, or install in multi-site mode. `kubectl get nodes -o wide` shows the addresses. When Geass knows no address to let SSH in from, it does not make the machine: an empty list would open the firewall to everyone. A cluster whose [control plane is hosted](/docs/operations/hosted-control-plane/) outside its nodes has no control plane node: its machines let SSH in from every node's addresses and from the public address the operator's connections leave from.

### Let Geass open the control plane

The new machine must reach the control plane's 6443/tcp (and 51820/udp in a multi-site cluster). If the control plane is behind a security group or firewall at a cloud, Geass can open those ports for each machine and close them again when the machine goes. Name the firewall on the cluster:

```yaml
spec:
  network:
    controlPlaneFirewall:
      connectionRef: aws-compute   # a connection for Compute at the cloud where the control plane runs
      target: sg-0abc123           # the security group, or the Hetzner firewall's id
      region: eu-north-1           # where it is, for a cloud that answers per region
```

```bash
kubectl -n geass-system patch geasscluster default --type merge \
  -p '{"spec":{"network":{"controlPlaneFirewall":{"connectionRef":"aws-compute","target":"sg-0abc123","region":"eu-north-1"}}}}'
```

It works for an AWS security group and a Hetzner firewall, and for nothing else: another cloud's connection makes the node fail with "the provider of the control plane's firewall connection cannot change firewalls: open the control plane's ports by hand, or remove controlPlaneFirewall from the cluster". The connection may be at another cloud than the one that makes the machines. Geass adds one rule for each address and port, marks its rules (the description `geass` on AWS, `geass-access` on Hetzner), and never changes a rule it did not mark. It removes them when the machine is removed.

## Machines no node owns

A machine at the cloud that carries the pool's labels and belongs to no node is an **orphan**. It happens when a node is removed while the cloud is unreachable, or when Geass crashes between creating a machine and writing down its ID. It is the bill that nobody is watching.

Every ten minutes the pool compares the machines at the cloud with its nodes. It looks only at machines that carry `geass-managed=true` and the identity of this cluster, so a second Geass cluster in the same cloud account never touches yours, and never at anything else in the account.

| `orphanPolicy` | What the pool does |
| --- | --- |
| `Report` (the default) | Lists the machines in `status.orphans` and in the pool's message, and the dashboard shows an alert. Deleting a machine cannot be undone, so Geass leaves it to you. |
| `Delete` | Deletes a machine that has had no node for 20 minutes, by the ID the cloud gave it. |

A machine counts as an orphan only after 20 minutes of being unowned, and only if the cloud says it was created more than 20 minutes ago, so a machine that is being made right now is never taken for one.

## Safety

| What could go wrong | What Geass does |
| --- | --- |
| A pool grows without a bound | `maxNodes` is required. The pool counts machines being created and removed against it. |
| Machines keep failing to become nodes | Three machines in a row that fail to become nodes pause the pool for 30 minutes. A node that becomes ready after the last failure starts the count again. See [Pause and resume](#pause-and-resume). |
| A machine that failed keeps billing | The machine of a failed node is deleted at once, and the node keeps its reason. |
| Geass deletes the wrong machine | It deletes a machine only by the ID the cloud gave it and that it recorded for the node. A machine created shortly before a crash, whose ID was never written down, is found again by its name and labels before another is made. |
| The cloud takes a machine back (spot), or someone deletes it | The node notices that the machine is gone and makes a new one for the same node. |
| A machine runs but its node is down | A node that has been `NotReady` for 30 minutes with its machine running is replaced, if it has `storage: None`. Not while most of the pool is down, because then the cause is the network or the control plane, not the machines. Not while K3s is being upgraded on it. |
| A node is removed and its machine is left | A node is released only after its machine is deleted and the cloud no longer lists it as alive. If the connection is gone for good, annotate the node `geass.dev/abandon-instance=true` to release it and leave the machine at the cloud. |
| The cloud has no room, or the account is at a limit | The node waits, and the pool tries its fallbacks after three minutes. See [Fallback sizes and regions](#fallback-sizes-and-regions). |
| A node is removed while pods run on it | It is drained first, for up to five minutes, through the eviction API. |
| Machines nobody owns | See [Machines no node owns](#machines-no-node-owns). |
| Credentials expire | A connection shows when its credentials expire, and warns two weeks before. See [Cloud connections for machines](/docs/guides/connectors/clouds/#what-the-connection-says-about-itself). |

## Pool states

A pool's phase says how it is doing, and its message says why.

| Phase | Meaning |
| --- | --- |
| `Ready` | The pool has the nodes it wants, and they are ready. |
| `Scaling` | Nodes are being added or removed, or some are not ready yet. The message says which, such as "adding 2 nodes" or "removing 1 nodes". |
| `Degraded` | Something is wrong that the pool cannot fix by itself: the settings are invalid (such as `maxNodes` below `minNodes`), the connection is missing or not for Compute or belongs to a project, or the pool sets a setting the cloud does not read. The message says which. The pool keeps the nodes it has and makes no more. |
| `Paused` | Three machines in a row failed to become nodes. The pool makes no more until the pause ends or you resume it. |
| `Suspended` | `spec.suspend` is on. The nodes the pool has stay. |
| `Pending` | The cluster the pool names does not exist. |

`status` also carries the nodes the pool has and wants, the pods waiting for room, the size and what it offers a pod, the estimated cost, the last failure, and when the pool last added and removed a node. The same is in `geass pools show`, the REST API and the pool's page.

A node of a pool goes through the phases of any node Geass makes at a cloud:

| Node phase | Meaning |
| --- | --- |
| `Pending` | Not started. |
| `Creating` | The machine is being made, or is running and waiting for SSH or for the control plane's firewall to let it in. The message says which: "creating the machine", "waiting for the provider to start the machine (state pending)", "waiting for the machine to accept SSH while it finishes its first boot". A machine that has not been running with an address 10 minutes after it was requested is given up on. |
| `Joining` | K3s is installed ("waiting for the node to register"). A node that is not ready 15 minutes later is given up on. |
| `Ready` | The node is ready. |
| `Failed` | The machine did not become a node, and was deleted. The message says why. A failed node of a pool is cleared away 30 minutes after it was made, once its machine is gone, and the pool makes a new one if it needs it. A failed single server stays, with its reason, until you ask for another attempt with the `geass.dev/retry` annotation or delete it. |

## Pause and resume

After three machines in a row fail to become nodes, the pool pauses for 30 minutes: a pool that keeps buying machines that never join (a firewall that blocks the control plane, an image that does not boot, a permission that is missing) would otherwise spend money until someone noticed. The pool's message and `lastFailure` say what the last machine said.

Fix the cause, then end the pause:

```bash
geass pools resume burst
```

In the dashboard the **Pools** page shows an alert for a paused pool, with a **Resume** button, and so does the pool's own page. With `kubectl`:

```bash
kubectl -n geass-system annotate geassnodepool burst geass.dev/resume=true
```

Resuming forgets the failures, so it takes three more to pause the pool again. When the 30 minutes end by themselves, the pool tries once more, and if that machine fails too it pauses again at once. A cause that is not fixed (an account limit, a bad setting, no capacity) fails again, and each failed attempt may cost an hour at a cloud that bills by the hour.

## Pools of servers you own

An inventory pool draws on servers you already have. Geass joins one over SSH when the pool needs a node, as it does for a server you add by hand, and when the pool no longer needs it, drains it and takes K3s off it. It does not power the server off, reset it or touch anything else on it.

```yaml title="spare-pool.yaml"
apiVersion: v1
kind: Secret
metadata:
  name: spare-ssh
  namespace: geass-system
stringData:
  ssh-privatekey: |
    -----BEGIN OPENSSH PRIVATE KEY-----
    replace-with-your-private-key
    -----END OPENSSH PRIVATE KEY-----
  # known_hosts: pins the servers' host keys, see Add servers
---
apiVersion: geass.geass.dev/v1alpha1
kind: GeassNodePool
metadata:
  name: spare
  namespace: geass-system
spec:
  inventory:
    hosts:
      - name: spare-1
        address: 198.51.100.11
        sshUser: root
        sshKeySecretRef: { name: spare-ssh, namespace: geass-system }
      - name: spare-2
        address: 198.51.100.12
        sshUser: root
        sshKeySecretRef: { name: spare-ssh, namespace: geass-system }
  minNodes: 0
  maxNodes: 2            # at most the number of servers listed
  autoscaling:
    enabled: true
```

Or from the command line, with a JSON file of servers (`name`, `address`, `sshUser`, and optionally `sshPort` and `sshKeySecret`) and one key for all of them:

```bash
geass pools create spare --inventory-file ./servers.json --inventory-key-file ~/.ssh/geass_ed25519 --max 2 --autoscale
```

Each server's name is the node's name once it joins. `maxNodes` cannot be more than the servers listed, and a server is used by one node at a time. The servers need what a [server you add by hand](/docs/getting-started/add-servers/#before-you-start) needs: a user that is `root` or has passwordless `sudo`, a key without a passphrase, and the network paths of that page. Pin their host keys with a `known_hosts` entry in the key's Secret, as there.

These nodes are ephemeral too unless you set `storage: Allow`: they take services only. The autoscaler drains a node of an inventory pool before it removes it. If you delete such a node or the pool by hand, the ordinary removal applies, and it does not drain: see [Add servers](/docs/getting-started/add-servers/#remove-a-node).

## Delete a pool or a node

Deleting a pool deletes its nodes. Each node is cordoned, its pods are evicted for up to five minutes, the Kubernetes node is removed, and then the machine is deleted. The node is released only when the cloud no longer lists the machine as alive. What ran on the nodes moves to other nodes if they have room, and waits if not.

```bash
geass pools delete burst
geass nodes list
```

Deleting the connection first would leave the machines running with nothing that can delete them, so the dashboard, the command line and the API refuse to remove a connection that a pool or a machine still uses. (`kubectl delete` does not check.) Delete the pool, wait until its nodes are gone, check the cloud's console, then remove the connection.

## What happens under the hood

| Object | Where | What it is |
| --- | --- | --- |
| `GeassNodePool` | `geass-system` | The pool. It owns its nodes. |
| `GeassNode` named `<pool>-<five characters>` | `geass-system` | One node of the pool. With `spec.cloud` it carries the machine's size, region and settings, and `status.cloud` holds the machine's ID, state and addresses. |
| Node labels | The Kubernetes node | `geass.dev/pool=<pool>`, `geass.dev/autoscaled=true` for an autoscaling pool, and `geass.dev/storage=none` for `storage: None`. |
| Node taint | The Kubernetes node | `geass.dev/ephemeral=true:NoSchedule` for `storage: None`. |
| Secret `geass-nodepool-ssh` | `geass-system` | The key Geass logs in to its machines with. |
| Secret `<node>-hostkey` | `geass-system` | The host key put on the node's machine. Deleted with the node. |
| Machine labels or tags | The cloud | `geass-managed=true`, `geass-cluster=<the cluster's identity>`, `geass-pool=<pool>` and `geass-node=<node>`. A cloud without key-value labels gets them as `key:value` tags, and OVHcloud, which has neither, has them in the machine's name. |

```bash
kubectl -n geass-system get geassnodepools
kubectl -n geass-system get geassnodepool burst -o jsonpath='{.status.phase}{"  "}{.status.message}{"\n"}'
kubectl -n geass-system get geassnodes -l geass.dev/pool=burst \
  -o custom-columns='NAME:.metadata.name,PHASE:.status.phase,MACHINE:.status.cloud.instanceID,MESSAGE:.status.message'
kubectl get nodes -l geass.dev/pool=burst -o wide
```

Besides its rights on the pool resource, the operator needs one Kubernetes permission it did not have before: to create `pods/eviction`, which it uses to drain a node while honouring disruption budgets. See [Security](/docs/operations/security/#what-the-operator-can-do-in-your-cluster).

## Troubleshooting

| Symptom | Cause and fix |
| --- | --- |
| The pool is `Degraded` and says the connection "is not for compute" | The connection's purposes do not include Compute. Add it, or make a connection for it. For AWS this means a separate connection from the one that holds buckets. |
| `Degraded`: the connection "belongs to project ..." | A connection for machines must not be scoped to a project. Make one that is not. |
| `Degraded`: "... does not read the setting ..." or "... needs the settings ..." | A pool setting the cloud does not take, or one it needs (Azure and Oracle Cloud need `subnetId`). `geass providers CLOUD` lists them. |
| The pool is `Degraded` with the connection's message | The cloud does not accept the credentials. See `geass connections show NAME` and [Troubleshooting](/docs/guides/connectors/clouds/#troubleshooting). |
| `Paused` | Three machines in a row failed. Read `lastFailure`, fix it, then [resume](#pause-and-resume). |
| Pods wait and the pool adds nothing | Check, in order: autoscaling is on and the pool is not `Suspended` or `Paused`; the pods have waited `scaleUpDelaySeconds`; the pool's `placement` matches the pods' region and tags; the pods fit a node of the size (a message says "N pods need more than a node of pool X offers"); the pool is below `maxNodes`. For a service created before the first pool, wait for its roll. |
| Pods wait and the pool says it is at its maximum | Raise `maxNodes`, or lower what the pods reserve. |
| A node stays `Creating` with "waiting for the provider to start the machine" | The cloud is slow, out of room or at a limit. The node waits, and after three minutes with a `Capacity` or `Quota` problem the pool tries its fallback. A machine that is not running with an address after 10 minutes is deleted and the node fails. |
| A node stays `Creating` with "waiting for the machine to accept SSH ..." | The machine is up and SSH does not answer. The first boot may still be running. If it lasts, the cloud's firewall does not let the operator's address in on 22/tcp: check the firewall Geass made, and [How a machine joins](#how-a-machine-joins). The wait is 18 minutes after the machine is requested. |
| A node is `Failed`: "opening the control plane's firewall for the machine: ..." | `controlPlaneFirewall` names a connection or a target that Geass cannot use. Check the connection, that it is for Compute at the cloud where the control plane runs, and that the target is the right ID. A cloud other than AWS or Hetzner cannot be used. |
| A node stays `Joining`, then `Failed` after 15 minutes | K3s installed and the node did not register. The machine cannot reach the control plane on 6443/tcp (or the overlay port is closed). Open the port, or use `controlPlaneFirewall`. The machine is deleted as soon as the node fails, so its log is gone: check the ports first, since they are the usual cause. |
| Machines at the cloud that no node owns | See [Machines no node owns](#machines-no-node-owns). Delete them in the cloud's console, or set `orphanPolicy: Delete`. |
| A node will not go away on delete | Read its message: it waits for the machine to be deleted, or for the connection. If the connection is gone for good, annotate it `geass.dev/abandon-instance=true`, then delete the machine at the cloud yourself. |
| The autoscaler never removes a node | See [What keeps a node](#what-keeps-a-node). `geass pools show NAME` and the operator log name the reason. |
| The services restarted when I made the first pool | Expected, once. See [What runs on a pool's nodes](#what-runs-on-a-pools-nodes). |
| The cloud bills more than the pool's cost | The estimate is the list price of machines only. Public addresses, traffic and disks that the cloud bills apart are extra. |

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
