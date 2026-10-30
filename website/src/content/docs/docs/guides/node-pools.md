---
title: "Node pools and autoscaling"
description: "Geass can create machines at a cloud, or join servers that you own, when pods cannot start, and remove idle ones. Settings, safety, and fixes. Experimental."
---

A pool is a group of nodes that Geass adds and removes for you. These are the main behaviors of a pool:

- When pods cannot start because the cluster is full, an autoscaling pool creates a machine at a cloud. The pool installs K3s on the machine and joins it to the cluster.
- When a machine is idle, the pool moves its pods away and deletes the machine.
- A pool that does not autoscale keeps exactly the number of nodes that you ask for.

This page explains how a pool decides and how to create a pool. It also explains what Geass does with your money and your machines, and what to check when a pool stops.

:::caution
Start with a small **most nodes** on a cluster that you can afford to break, and watch the first machines come up. Node pools, and the cloud drivers behind them, are **experimental**. Amazon EC2 has been run against a live account, in one region. What ran is listed under [How far to trust it](/docs/guides/connectors/clouds/#how-far-to-trust-it). The other eleven drivers were written from the official API documentation of each provider. They were tested only against an in-memory simulation of that API. Thus, a quirk of a real account can still surprise them, for example a quota, a missing permission, or a slow image. See [Cloud connections for machines](/docs/guides/connectors/clouds/#how-far-to-trust-it).
:::

## Before you start

- You are an admin. To make, change, and delete a pool, and to list the sizes of a cloud, you need the admin role. That is, you are a dashboard admin, or you use a workspace token with **Full access**. A viewer or an observer can read pools. A project token cannot reach pools at all.
- A connection for machines to the cloud that you want to use. The connection belongs to the cluster, not to a project. Add it first: [Cloud connections for machines](/docs/guides/connectors/clouds/). A pool of servers that you own needs no connection.
- A cloud account that you can afford to let Geass spend in. Every machine that a pool creates is billed to the account of its connection until the machine is deleted. **Most nodes** is the limit on that cost, and you cannot make the pool without it.
- Network paths between the new machines and the control plane. Geass opens some of these paths for you, in a firewall that it makes at the cloud. You must open the other paths. See [How a machine joins](#how-a-machine-joins).
- A size worth buying. A node reserves about 250m CPU and 700 MiB of memory for K3s and the system pods that run on every node. Thus, a machine under 2 vCPU and 4 GiB leaves little for your services. A new machine also takes minutes to boot, install K3s, and pull images. Thus, a pool helps with load that lasts, not with a spike of seconds.

## The words

| Word | Meaning |
| --- | --- |
| **Pool** | A `GeassNodePool`: a rule for how many nodes of one kind the cluster has, and where they come from. |
| **Connection for machines** | A `GeassCloudConnection` with the purpose Compute: the credentials that Geass uses to create and delete machines at a cloud. See [Cloud connections for machines](/docs/guides/connectors/clouds/). |
| **Inventory pool** | A pool that draws on a list of servers that you own, instead of a cloud. Geass joins a server when it needs room, and removes K3s from the server when it does not. |
| **Fixed pool** | A pool with autoscaling off. It has exactly **fewest nodes** nodes. |
| **Autoscaling pool** | A pool that adds nodes when pods cannot start, and removes idle nodes, between **fewest nodes** and **most nodes**. |
| **Ephemeral node** | A node of a pool whose **data on these nodes** is `None` (the default). Databases, buckets, and volumes never run there. Thus, Geass can remove the node at any time. The dashboard marks it **Ephemeral**. |
| **Fallback** | A second size or region that a pool tries when the cloud has no room for the first. |
| **Orphan machine** | A machine at the cloud that carries the labels of a pool and belongs to no node. It might still be billing you. |

## Create a pool

Check the cost before you commit. These commands show the sizes of a cloud, cheapest first. They show the price for an hour where the cloud states one:

```bash
geass providers
geass connections list
geass sizes --connection hetzner --region fsn1 --min-vcpus 2 --min-memory 4 --limit 10
```

### In the dashboard

1. In the left rail, open **Pools**.
2. Click **Add pool**.
3. Enter a **Name**: lowercase letters, numbers, and hyphens, at most 48 characters. Geass makes the name of each node from it.
4. Under **Where do the machines come from?**, select one of these options:
   - **A cloud**. Then select the **Cloud connection**, the **Region**, and the **Machine size**.
   - **My servers**. Then list the servers. See [Pools of servers you own](#pools-of-servers-you-own).
5. Set **Fewest nodes** and **Most nodes**. **Most nodes** is required. It is the cost cap.
6. Under **Data on these nodes**, select what the nodes can hold.
7. Select whether to **Autoscale**. Under **Autoscale**, you can change the numbers in the table below.
8. Click **Create pool**. Geass creates nothing before this step. A pool with **Fewest nodes** above 0 starts to create machines immediately.

The page of the pool shows its status, its nodes with their machine IDs and addresses, and its settings. To change the settings on this page, use **Save changes**. A paused pool shows **Resume**. A change to the size, region, image, or settings applies to machines made after the change. The nodes that the pool has stay as they are.

### With the command line

```bash
# An autoscaling pool: no nodes until pods wait, then up to 5
geass pools create burst --connection hetzner --region fsn1 --size cx23 \
  --fallback-size cx33 --min 0 --max 5 --autoscale

# A fixed pool of two nodes
geass pools create fixed --connection hetzner --size cx23 --min 2 --max 2 --no-autoscale

geass pools show burst
```

`--max` is required. When `--max` is above `--min`, you must also give `--autoscale` or `--no-autoscale`. The reason is that a pool that does not autoscale ignores everything above `--min`, and this result is rarely what was meant. For every flag, see [`geass pools`](/docs/reference/cli/#geass-pools).

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

`kubectl get` shows the phase, the minimum and maximum, how many nodes exist and are ready, and the size. For the full field list, see [GeassNodePool](/docs/reference/resources/geass-node-pool/).

## Fixed or autoscaling

| | Fixed pool | Autoscaling pool |
| --- | --- | --- |
| How many nodes | Exactly **fewest nodes** | Between **fewest nodes** and **most nodes**, following the load |
| What adds a node | You raise **fewest nodes**, or a node is lost | Pods that cannot start, or **spare nodes** |
| What removes a node | You lower **fewest nodes**. Nodes that are not up yet go first, then the newest | A node under-used for long enough, as [described below](#how-a-pool-removes-nodes) |
| Role of **most nodes** | A ceiling that also counts nodes that are being replaced | The cost cap |

A fixed pool is the simple way to rent machines through Geass. The pool makes the machines, keeps the number correct, and replaces a machine that the cloud takes back. A fixed pool removes a surplus node without the checks of an autoscaling pool. The pool drains the node for up to five minutes and then deletes it.

## Settings

| Field | Default | Meaning |
| --- | --- | --- |
| `spec.cluster` | `default` | The `GeassCluster` that the nodes join. |
| `spec.connectionRef` | none | The connection for machines that creates the machines. A pool has this field or `inventory`, not both. |
| `spec.inventory.hosts` | none | Servers that you own, instead of a cloud. See [Pools of servers you own](#pools-of-servers-you-own). |
| `spec.region` | the default region of the connection, or where the cloud chooses | The cloud's own place: a location, zone, or region. What it means is [per cloud](/docs/guides/connectors/clouds/#the-clouds). |
| `spec.size` | none | The name of the cloud for a machine size. Required for a pool that creates machines. |
| `spec.fallbackSizes`, `spec.fallbackRegions` | none | Up to five each. The pool tries them in order when the cloud has no room, or the account is at a limit. See [Fallback sizes and regions](#fallback-sizes-and-regions). |
| `spec.image` | the cloud's Ubuntu 24.04 | The operating system image. It must read a cloud-init document, because the login and host key of the machine come from one. It must also let the user `geass` use `sudo`. |
| `spec.spot` | `false` | Ask the cloud for its cheaper machines that it might take back. AWS, Azure, Google Cloud, Oracle Cloud, and the webhook driver honour this field. The other drivers ignore it. |
| `spec.addressType` | `Public` | Which address of a machine Geass uses to reach it: `Public`, or `Private` for a pool whose machines share a private network with the control plane. |
| `spec.settings` | none | Values specific to the cloud, such as a subnet or a firewall. Each cloud lists its own. |
| `spec.placement` | none | The region, site, and tags that the nodes carry. These are the same fields as on a [node](/docs/reference/resources/geass-node/). |
| `spec.storage` | `None` | `None` or `Allow`. See [What runs on a pool's nodes](#what-runs-on-a-pools-nodes). |
| `spec.minNodes` | `0` | The fewest nodes that the pool has, 0 to 200. A fixed pool has exactly this many. |
| `spec.maxNodes` | required | The most nodes that the pool ever has, 0 to 200, and at least `minNodes`. |
| `spec.orphanPolicy` | `Report` | `Report` or `Delete`. See [Machines no node owns](#machines-no-node-owns). |
| `spec.suspend` | `false` | Stop adding and removing nodes. The nodes that the pool has stay, and they continue to cost what they cost. |

These are the settings of `spec.autoscaling`. If you turn `enabled` on and set nothing else, the pool uses every default.

| Field | Default | Range | Meaning |
| --- | --- | --- | --- |
| `enabled` | `false` | | Let the pool follow the load. When it is off, the pool keeps exactly `minNodes`. |
| `scaleUpDelaySeconds` | `30` | 1 to 3600 | How long a pod must have been unable to start before the pool adds a node for it. Thus, a pod that is only slow to be scheduled does not buy a machine. |
| `maxScaleUp` | `5` | 1 to 50 | The most nodes that the pool adds in one step. |
| `scaleDownUtilizationPercent` | `50` | 1 to 100 | A node is a candidate for removal when its pods reserve less than this share of its CPU or its memory, whichever is higher. |
| `scaleDownAfterMinutes` | `10` | 1 to 1440 | How long a node must have been a candidate before the pool removes it. |
| `scaleDownDelayAfterAddMinutes` | `10` | 1 to 1440 | The pool removes no node for this long after it last added one. |
| `minNodeLifetimeMinutes` | `0` | 0 to 10080 | The pool does not remove a node before it is this old. Many clouds bill each started hour. Thus, a node removed after ten minutes costs a full hour. 55 keeps the node for the hour that is paid for anyway. |
| `spareNodes` | `0` | 0 to 20 | Empty nodes kept ready for a burst. With these nodes, new pods find room immediately, and do not wait minutes for a machine. They count against `maxNodes`, and they cost money while they wait. |

The name of a pool is at most 48 characters. The reason is that the name of each node is the name of the pool, a hyphen, and five characters. The name of a node cannot be longer than 55 characters.

## What runs on a pool's nodes

Nodes of a pool come and go. Thus, by default, Geass keeps data off them. `spec.storage` says which of two kinds a pool is.

| `storage` | Node taint | What can run there |
| --- | --- | --- |
| `None` (the default) | `geass.dev/ephemeral=true:NoSchedule` | Services only. Databases, object storage servers, and volume copies never. |
| `Allow` | none | Anything, like any other worker. The autoscaler never removes a node when it holds a volume copy or runs a pod that uses a volume claim. |

These results follow from `None`:

- **Services tolerate the taint, and nothing else does.** A service keeps nothing on its node, so Kubernetes can move it. Databases, buckets, backups, and builds do not tolerate the taint, and they stay off these nodes.
- **The first pool with `storage: None` makes every service roll once.** The pods of a service must carry the toleration before they can use the node. Geass adds the toleration only when such a pool exists, or a single server added with `storage: None` exists. Thus, a cluster that uses neither sees no change. When you create the first such pool or server, Geass rolls each service one time to add the toleration. This roll restarts the pods of the service the way a deploy does. The same happens, the other way round, when you delete the last one. Do it at a time that suits you.
- **High availability does not count them.** A node that cannot hold a database replica does not help [HA readiness](/docs/operations/high-availability/#check-readiness). Thus, a pool of ephemeral nodes never makes a cluster ready for a highly available database.
- **DNS never points at them.** Geass takes the address that the dashboard and your domains resolve to from specific nodes. These nodes are in no pool, and they can hold data. The reason is that the nodes of a pool, and a server added with data kept off it, are created and removed under that address.
- **Longhorn is told to keep volume copies off them.** Geass turns scheduling off for the node in Longhorn. Geass turns it back on only if Geass turned it off, and the node stops being ephemeral. See [Storage](/docs/guides/storage/#nodes-that-must-not-hold-data).
- **The Usage page marks them.** The **Nodes** table has a **Pool** column with an **Ephemeral** badge. `geass nodes list` has `POOL` and `EPHEMERAL` columns. The **Summary** counts the capacity that every kind of workload can use. Thus, the **Summary** leaves out ephemeral nodes, the same way that it leaves out a node reserved for a tag. See [Usage and capacity](/docs/guides/usage-and-capacity/).

A project that has a region runs only on nodes of that region. A project that requires tags runs only on nodes with these tags. The nodes of a pool carry the `placement` that you give the pool, and nothing else. Give the pool the region and tags of the projects that it should help. If you do not, the pods of those projects wait for a node that the pool never adds. See [Scaling and placement](/docs/guides/scaling-and-placement/).

## How a pool adds nodes

An autoscaling pool looks about every 10 seconds.

1. **It finds the pods that wait.** A pod counts when all of these conditions are true:
   - The scheduler has said that it cannot place the pod for at least `scaleUpDelaySeconds`.
   - The pod could run on a new node of the pool. Its node selector, affinity, and tolerations allow it.
   - The pod would fit on such a node.

   The pool leaves out a pod when the reason from the scheduler is one that no new node can change. Examples are a volume that is bound in a place where a new node is not, or a volume claim that is unbound. A node made for such a pod would only be removed again and made again.
2. **It calculates how many nodes the pods need.** It packs the pods, largest first, onto the room that exists:
   - Each node that is coming up counts as a whole new node.
   - The free room of a node that became ready in the last three minutes also counts. The reason is that the scheduler takes a few seconds to notice a new node.
   - A pod that fits nowhere needs a new node, and the pool also packs the pods after it onto that new node.

   The pool plans with what the smallest ready node of the pool really offers. Before there is a ready node, the pool plans with the size of the machine, less what K3s and the system pods keep for themselves.
3. **It creates that many, within limits.** The pool creates at most `maxScaleUp` in one step, and never so many that the pool goes above `maxNodes`. Nodes that are still being created or removed count toward the maximum, because their machines cost money.
4. **It keeps `spareNodes` empty nodes**, and at least `minNodes` nodes.

For example, a pool of machines with 4 vCPU and 8 GiB offers each node about 3.75 CPU and 7.3 GiB. Six pods that each reserve 1 CPU and 2 GiB cannot start. Memory allows three of them on a node. Thus, the pool adds two nodes, not six. The pool never helps pods whose reservation is larger than a whole node of the pool. The pool says so in its message: "2 pods need more than a node of pool burst offers", followed by what a node offers.

If the pool is at its maximum, the pods wait. The pool says "N pods cannot start and the pool is at its maximum of M nodes".

Adding a node takes minutes. The cloud starts the machine, Geass logs in over SSH and installs K3s, and the node pulls the images of what is waiting. The pods start when the node is ready.

### The capacity check

[The capacity check](/docs/guides/scaling-and-placement/#the-capacity-check) refuses to create a workload that does not fit the nodes that it can use. With an autoscaling pool, the check first asks one more question: would the workload fit if the pool grew to its maximum? If yes, the check lets the workload in, and its pods wait for the nodes that the pool adds. If the pool is already at its maximum, the check refuses the workload with the usual message.

A pool counts only when all of these conditions are true:

- Geass knows what one of its nodes offers. Geass learns this from the size, when the cloud lists it, or from a ready node.
- The pool is `Ready` or `Scaling`.
- The pool is not suspended.

A pool of ephemeral nodes counts for services only. The check never makes room for a database there. The question is the same wherever the workload comes from:

- Creating or deploying it in the dashboard.
- The command line or the API.
- Applying it with `kubectl`.

From each of these sources, a full cluster with a pool that can grow lets the workload in.

## How a pool removes nodes

The pool removes a node of an autoscaling pool when all of these conditions are true:

- The node is under-used. The pods that would have to move reserve less than `scaleDownUtilizationPercent` of its CPU or of its memory, whichever is higher. The pool does not count DaemonSet pods, finished pods, and static pods, because they go with the node. An empty node is always under-used.
- Everything on the node would fit on the other ready nodes of the cluster. The check respects the node selector and tolerations of each pod. Thus, a pod that can run only in one region is not counted as fitting a node in another region.
- The node has been a candidate for `scaleDownAfterMinutes`. If the node stops being a candidate, the timer starts again.
- The node is at least `minNodeLifetimeMinutes` old.
- Nothing about the node says to stay. See the table below.

Also, these rules apply to the pool:

- It removes nothing when pods wait for room or a node is still coming up.
- It removes nothing for `scaleDownDelayAfterAddMinutes` after it last added a node.
- It removes nothing when K3s is being upgraded on the cluster.
- It never goes below `minNodes`.

When more than one node qualifies, the pool removes empty nodes together, up to ten at a time. It keeps `spareNodes` empty nodes. If no node is empty, the pool removes one node that has pods, the least used. The pool does these steps:

1. It cordons the node.
2. It asks each pod to leave through the Kubernetes eviction API. Thus, the pool honours a pod disruption budget.
3. After nothing is left, it deletes the node and its machine.

If a node does not empty within 10 minutes, the pool puts it back into service. The pool then waits 3 minutes before it tries again. New demand also takes back a node that was being emptied, because its room is the quickest to get.

Geass creates no pod disruption budget of its own. A service with one replica is down for the time that it takes to start elsewhere. Run at least two replicas of a service that cannot be interrupted. If you want a hold on the service, add a `PodDisruptionBudget` of your own.

### What keeps a node

| What | Why |
| --- | --- |
| A pod with no controller (a bare pod) | Nothing would recreate it. |
| A pod that uses a volume claim, a host path, or an `emptyDir` kept on disk | It holds data on the node. An `emptyDir` in memory does not count. |
| A pod in `kube-system` | It is a system pod. |
| A pod annotated `geass.dev/safe-to-evict: "false"` | Its owner asked to be left alone. The value `"true"` overrides every one of the guesses above. Thus, the pool can move a pod that keeps only a cache on disk. |
| The node annotated `geass.dev/scale-down-disabled: "true"` | Yours: `kubectl annotate node NAME geass.dev/scale-down-disabled=true`. |
| A node someone else cordoned | Geass leaves a node that you cordoned with `kubectl cordon` as it is. Geass marks the nodes that it cordons itself, and uncordons only those. |
| A node younger than `minNodeLifetimeMinutes` | You asked it to live at least that long. |
| A node that holds a volume copy (a pool with `storage: Allow`) | If the pool removed it, a volume would be one copy short. |
| A K3s upgrade in progress | The upgrade works through the nodes one at a time. |

To see why nodes stay, read the message of the pool with `geass pools show NAME`. Also read the operator log at verbosity 1, which lists the reason for each node.

## Fallback sizes and regions

A cloud can run out of a size in a place, or an account can reach a limit. If Geass cannot make a machine for either reason, the machine waits, because both problems can pass. Its node says so (`Capacity` or `Quota`). After the node has waited three minutes, the pool goes to the next choice and makes the node again. The order is every size in the first region, then every size in the next region. For example, take `size: cx23`, `fallbackSizes: [cx33]`, and `fallbackRegions: [nbg1]`. The pool tries these choices in order:

1. `cx23` in `fsn1`.
2. `cx33` in `fsn1`.
3. `cx23` in `nbg1`.
4. `cx33` in `nbg1`.

The pool goes back to its first choice one hour later. When a fallback is in use, the page of the pool and `geass pools show` say which size is in use. A fallback might cost more than the first choice. The cap is `maxNodes`, not a sum of money. Thus, select fallbacks that you are willing to pay for.

## Cost

- **The cap.** A pool never has more than `maxNodes` machines. That number, times the price of a machine, is the most that the pool costs for an hour. The page of the pool shows what it costs now and the most that it could cost.
- **The estimate.** The `estimatedHourlyCost` of the pool is the number of its machines times the price that the cloud lists for the size. Geass can show the estimate for the clouds whose API states a price. Geass cannot show it for Amazon, Google Cloud, Oracle Cloud, or OVHcloud. The estimate leaves out taxes, traffic, public IPv4 addresses, and anything else that the cloud bills on its own line.
- **Hours.** The drivers for Linode, OVHcloud, and Vultr say that they bill each started hour at least. Oracle Cloud bills for each second, with a one-minute minimum. For the other clouds, read the terms of your provider. Where a started hour is billed, set `minNodeLifetimeMinutes` to 55. Then a node that you pay for anyway stays for the next burst.
- **Idle cost.** `minNodes` and `spareNodes` machines cost money while they wait. If cost matters more than speed, leave both at 0.

## How a machine joins

For a machine that it creates, Geass does what it does for a server that you bring, with one difference. Geass makes the key and the machine. Thus, Geass can trust the two before the machine exists.

1. The first time that Geass needs an SSH key for the cluster, it makes one. Geass keeps this key in the Secret `geass-nodepool-ssh` in `geass-system`. Geass also makes a host key for each machine, in the Secret `<node>-hostkey`.
2. Geass creates the machine with a cloud-init document. The document creates a user `geass` with passwordless `sudo`. The document holds the **public** key of the cluster, the host key of the machine, and its hostname. The document carries no join token and no other secret. The reason is that a user can read the user data of a machine from inside the machine and from the cloud's own API.
3. When the machine is running with an address, Geass logs in over SSH with the key of the cluster. Geass checks the host key against the one that it put there. If Geass cannot check, the node waits, and Geass never sends the token unchecked. Geass cannot check when the Secret of the host key is missing, or when the machine has no address yet. After the check, Geass installs K3s as an agent with these items: the join token, the labels of the node, and, for `storage: None`, the taint. This procedure is the same as [Add servers](/docs/getting-started/add-servers/#what-happens-under-the-hood). If a machine refuses SSH while it finishes its first boot, Geass waits for it.
4. Geass waits for the node to be `Ready`.

These paths must be reachable:

| From | To | Port | Who opens it |
| --- | --- | --- | --- |
| Control plane (the operator) | The new machine | 22/tcp | The firewall that Geass makes at the cloud, for the addresses of the control plane's nodes. You open it where Geass makes no firewall (Oracle Cloud, OVHcloud), or where you named your own firewall in the `settings` of the pool |
| Control plane | The new machine | 10250/tcp (kubelet) | The same |
| The new machine | Control plane | 6443/tcp | You, in the firewall or security group of the control plane, unless you use [`controlPlaneFirewall`](#let-geass-open-the-control-plane) |
| Every node | Every other node | 51820/udp in a multi-site cluster | The firewall that Geass makes, from anywhere, because the overlay authenticates every packet |

In a cluster installed in the default mode, the overlay is VXLAN on 8472/udp between nodes. No driver opens that port. Thus, a pool in such a cluster has these requirements:

- It needs a firewall of yours that allows the port. Name this firewall in the `settings` of the pool.
- It works for machines on the same private network as the control plane, with `addressType: Private`.

For machines at another provider, use a cluster installed in [multi-site mode](/docs/operations/multi-provider/). Its overlay needs only the ports above. A pool at a second provider is part of the same experiment as any node there.

The firewall that Geass makes at the cloud lets SSH and the kubelet in only from the control plane's nodes. The firewall uses the addresses that Kubernetes lists for these nodes, external and internal. The node of the control plane might list only a private address, and the machine might be reached on its public address. In this case, the SSH of the operator arrives from an address that the firewall does not allow. To fix this problem, use `addressType: Private` in the same network, or install in multi-site mode. `kubectl get nodes -o wide` shows the addresses.

When Geass knows no address to let SSH in from, it does not make the machine. The reason is that an empty list would open the firewall to everyone. A cluster whose [control plane is hosted](/docs/operations/hosted-control-plane/) outside its nodes has no control plane node. The machines of such a cluster let SSH in from the addresses of every node. They also let SSH in from the public address that the connections of the operator leave from.

### Let Geass open the control plane

The new machine must reach the 6443/tcp port of the control plane. In a multi-site cluster, the machine must also reach 51820/udp. If the control plane is behind a security group or firewall at a cloud, Geass can open those ports for each machine. Geass closes them again when the machine goes. Name the firewall on the cluster:

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

This feature works for an AWS security group and a Hetzner firewall, and for nothing else. The connection of another cloud makes the node fail with "the provider of the control plane's firewall connection cannot change firewalls: open the control plane's ports by hand, or remove controlPlaneFirewall from the cluster". The connection can be at a different cloud from the one that makes the machines. Geass adds one rule for each address and port. Geass marks its rules: the description `geass` on AWS, and `geass-access` on Hetzner. Geass never changes a rule that it did not mark. Geass removes its rules when the machine is removed.

## Machines no node owns

An **orphan** is a machine at the cloud that carries the labels of the pool and belongs to no node. An orphan occurs in these cases:

- A node is removed when the cloud is unreachable.
- Geass crashes between the creation of a machine and the record of its ID.

An orphan is the bill that nobody watches.

Every ten minutes, the pool compares the machines at the cloud with its nodes. The pool looks only at machines that carry `geass-managed=true` and the identity of this cluster. Thus, a second Geass cluster in the same cloud account never touches yours. The pool never looks at anything else in the account.

| `orphanPolicy` | What the pool does |
| --- | --- |
| `Report` (the default) | Lists the machines in `status.orphans` and in the message of the pool, and the dashboard shows an alert. You cannot undo the deletion of a machine, so Geass leaves the deletion to you. |
| `Delete` | Deletes a machine that has had no node for 20 minutes, by the ID that the cloud gave it. |

A machine counts as an orphan only after 20 minutes without an owner. Also, the cloud must say that it created the machine more than 20 minutes ago. Thus, the pool never mistakes a machine that is being made now for an orphan.

## Safety

| What could go wrong | What Geass does |
| --- | --- |
| A pool grows without a bound | `maxNodes` is required. The pool counts machines that are being created and removed against it. |
| Machines keep failing to become nodes | If three machines in a row fail to become nodes, the pool pauses for 30 minutes. A node that becomes ready after the last failure starts the count again. See [Pause and resume](#pause-and-resume). |
| A machine that failed keeps billing | Geass deletes the machine of a failed node immediately, and the node keeps its reason. |
| Geass deletes the wrong machine | Geass deletes a machine only by the ID that the cloud gave it and that Geass recorded for the node. A machine might be created shortly before a crash, and its ID never written down. Geass finds such a machine again by its name and labels before it makes another. |
| The cloud takes a machine back (spot), or someone deletes it | The node notices that the machine is gone, and makes a new one for the same node. |
| A machine runs but its node is down | If a node with `storage: None` has been `NotReady` for 30 minutes with its machine running, Geass replaces it. Not when most of the pool is down, because then the cause is the network or the control plane, not the machines. Not when K3s is being upgraded on the node. |
| A node is removed and its machine is left | Geass releases a node only after its machine is deleted and the cloud no longer lists the machine as alive. If the connection is gone permanently, annotate the node `geass.dev/abandon-instance=true`. This annotation releases the node and leaves the machine at the cloud. |
| The cloud has no room, or the account is at a limit | The node waits, and the pool tries its fallbacks after three minutes. See [Fallback sizes and regions](#fallback-sizes-and-regions). |
| A node is removed when pods run on it | Geass drains the node first, for up to five minutes, through the eviction API. |
| Machines nobody owns | See [Machines no node owns](#machines-no-node-owns). |
| Credentials expire | A connection shows when its credentials expire, and warns two weeks before. See [Cloud connections for machines](/docs/guides/connectors/clouds/#what-the-connection-says-about-itself). |

## Pool states

The phase of a pool says how it is doing, and its message says why.

| Phase | Meaning |
| --- | --- |
| `Ready` | The pool has the nodes that it wants, and they are ready. |
| `Scaling` | The pool is adding or removing nodes, or some nodes are not ready yet. The message says which, such as "adding 2 nodes" or "removing 1 nodes". |
| `Degraded` | Something is wrong that the pool cannot fix by itself. The settings are invalid (such as `maxNodes` below `minNodes`). Or the connection is missing, is not for Compute, or belongs to a project. Or the pool sets a setting that the cloud does not read. The message says which. The pool keeps the nodes that it has and makes no more. |
| `Paused` | Three machines in a row failed to become nodes. The pool makes no more until the pause ends or you resume it. |
| `Suspended` | `spec.suspend` is on. The nodes that the pool has stay. |
| `Pending` | The cluster that the pool names does not exist. |

`status` also carries these items:

- The nodes that the pool has and wants.
- The pods that wait for room.
- The size, and what it offers a pod.
- The estimated cost.
- The last failure.
- When the pool last added and removed a node.

The same items are in `geass pools show`, the REST API, and the page of the pool.

A node of a pool goes through the phases of any node that Geass makes at a cloud:

| Node phase | Meaning |
| --- | --- |
| `Pending` | Not started. |
| `Creating` | Geass is making the machine. Or the machine is running and waits for SSH, or for the firewall of the control plane to let it in. The message says which: "creating the machine", "waiting for the provider to start the machine (state pending)", "waiting for the machine to accept SSH while it finishes its first boot". If a machine is not running with an address 10 minutes after it was requested, Geass gives up on it. |
| `Joining` | K3s is installed ("waiting for the node to register"). If the node is not ready 15 minutes later, Geass gives up on it. |
| `Ready` | The node is ready. |
| `Failed` | The machine did not become a node, and Geass deleted it. The message says why. Geass clears away a failed node of a pool 30 minutes after it was made, after its machine is gone. The pool then makes a new node if it needs one. A failed single server stays, with its reason, until you delete it, or ask for another attempt with the `geass.dev/retry` annotation. |

## Pause and resume

After three machines in a row fail to become nodes, the pool pauses for 30 minutes. Without this pause, a pool that keeps buying machines that never join would spend money until someone noticed. Examples of causes are a firewall that blocks the control plane, an image that does not boot, or a missing permission. The message of the pool and `lastFailure` say what the last machine said.

Fix the cause, then end the pause:

```bash
geass pools resume burst
```

In the dashboard, the **Pools** page shows an alert for a paused pool, with a **Resume** button. The page of the pool also shows this alert. With `kubectl`:

```bash
kubectl -n geass-system annotate geassnodepool burst geass.dev/resume=true
```

When you resume, the pool forgets the failures. Thus, three more failures are necessary to pause the pool again. When the 30 minutes end by themselves, the pool tries one more time. If that machine also fails, the pool pauses again immediately. A cause that is not fixed fails again, for example an account limit, a bad setting, or no capacity. At a cloud that bills by the hour, each failed attempt might cost an hour.

## Pools of servers you own

An inventory pool draws on servers that you already have. When the pool needs a node, Geass joins a server over SSH, as it does for a server that you add by hand. When the pool no longer needs the server, Geass drains it and removes K3s from it. Geass does not power off the server, reset it, or touch anything else on it.

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

You can also use the command line, with a JSON file of servers and one key for all of them. Each server in the file has `name`, `address`, `sshUser`, and optionally `sshPort` and `sshKeySecret`:

```bash
geass pools create spare --inventory-file ./servers.json --inventory-key-file ~/.ssh/geass_ed25519 --max 2 --autoscale
```

After a server joins, its name is the name of the node. `maxNodes` cannot be more than the number of servers listed. One node at a time uses a server. The servers need what a [server you add by hand](/docs/getting-started/add-servers/#before-you-start) needs:

- A user that is `root` or has passwordless `sudo`.
- A key without a passphrase.
- The network paths on that page.

Pin their host keys with a `known_hosts` entry in the Secret of the key, the same as on that page.

These nodes are also ephemeral, unless you set `storage: Allow`. Thus, they take services only. The autoscaler drains a node of an inventory pool before it removes the node. If you delete such a node or the pool by hand, the ordinary removal applies, and it does not drain. See [Add servers](/docs/getting-started/add-servers/#remove-a-node).

## Delete a pool or a node

When you delete a pool, Geass deletes its nodes. For each node, Geass does these steps:

1. It cordons the node.
2. It evicts the pods of the node, for up to five minutes.
3. It removes the Kubernetes node.
4. It deletes the machine.

Geass releases the node only when the cloud no longer lists the machine as alive. What ran on the nodes moves to other nodes if they have room. If they do not have room, it waits.

```bash
geass pools delete burst
geass nodes list
```

Do not delete the connection first. Without the connection, the machines would run with nothing that can delete them. Thus, the dashboard, the command line, and the API refuse to remove a connection that a pool or a machine still uses. `kubectl delete` does not check. To remove the connection, do these steps:

1. Delete the pool.
2. Wait until its nodes are gone.
3. Check the console of the cloud.
4. Remove the connection.

## What happens under the hood

| Object | Where | What it is |
| --- | --- | --- |
| `GeassNodePool` | `geass-system` | The pool. It owns its nodes. |
| `GeassNode` named `<pool>-<five characters>` | `geass-system` | One node of the pool. With `spec.cloud`, it carries the size, region, and settings of the machine. `status.cloud` holds the ID, state, and addresses of the machine. |
| Node labels | The Kubernetes node | `geass.dev/pool=<pool>`, `geass.dev/autoscaled=true` for an autoscaling pool, and `geass.dev/storage=none` for `storage: None`. |
| Node taint | The Kubernetes node | `geass.dev/ephemeral=true:NoSchedule` for `storage: None`. |
| Secret `geass-nodepool-ssh` | `geass-system` | The key that Geass uses to log in to its machines. |
| Secret `<node>-hostkey` | `geass-system` | The host key put on the machine of the node. Deleted with the node. |
| Machine labels or tags | The cloud | `geass-managed=true`, `geass-cluster=<the cluster's identity>`, `geass-pool=<pool>`, and `geass-node=<node>`. A cloud without key-value labels gets them as `key:value` tags. OVHcloud has neither, so the name of the machine holds them. |

```bash
kubectl -n geass-system get geassnodepools
kubectl -n geass-system get geassnodepool burst -o jsonpath='{.status.phase}{"  "}{.status.message}{"\n"}'
kubectl -n geass-system get geassnodes -l geass.dev/pool=burst \
  -o custom-columns='NAME:.metadata.name,PHASE:.status.phase,MACHINE:.status.cloud.instanceID,MESSAGE:.status.message'
kubectl get nodes -l geass.dev/pool=burst -o wide
```

The operator has rights on the pool resource. It also needs one Kubernetes permission that it did not have before: to create `pods/eviction`. The operator uses this permission to drain a node and honour disruption budgets. See [Security](/docs/operations/security/#what-the-operator-can-do-in-your-cluster).

## Troubleshooting

| Symptom | Cause and fix |
| --- | --- |
| The pool is `Degraded` and says the connection "is not for compute" | The purposes of the connection do not include Compute. Add Compute, or make a connection for it. For AWS, this means a separate connection from the one that holds buckets. |
| `Degraded`: the connection "belongs to project ..." | A connection for machines must not be scoped to a project. Make one that is not. |
| `Degraded`: "... does not read the setting ..." or "... needs the settings ..." | A pool setting that the cloud does not take, or one that it needs. Azure and Oracle Cloud need `subnetId`. `geass providers CLOUD` lists them. |
| The pool is `Degraded` with the message of the connection | The cloud does not accept the credentials. See `geass connections show NAME` and [Troubleshooting](/docs/guides/connectors/clouds/#troubleshooting). |
| `Paused` | Three machines in a row failed. Read `lastFailure`, fix the cause, then [resume](#pause-and-resume). |
| Pods wait and the pool adds nothing | Check these items in order. Autoscaling is on, and the pool is not `Suspended` or `Paused`. The pods have waited `scaleUpDelaySeconds`. The `placement` of the pool matches the region and tags of the pods. The pods fit a node of the size. If they do not, a message says "N pods need more than a node of pool X offers". The pool is below `maxNodes`. For a service created before the first pool, wait for its roll. |
| Pods wait and the pool says it is at its maximum | Raise `maxNodes`, or lower what the pods reserve. |
| A node stays `Creating` with "waiting for the provider to start the machine" | The cloud is slow, out of room, or at a limit. The node waits. After three minutes with a `Capacity` or `Quota` problem, the pool tries its fallback. If a machine is not running with an address after 10 minutes, Geass deletes it, and the node fails. |
| A node stays `Creating` with "waiting for the machine to accept SSH ..." | The machine is up, and SSH does not answer. The first boot might still be running. If the problem continues, the firewall of the cloud does not let the address of the operator in on 22/tcp. Check the firewall that Geass made, and [How a machine joins](#how-a-machine-joins). The wait is 18 minutes after the machine is requested. |
| A node is `Failed`: "opening the control plane's firewall for the machine: ..." | `controlPlaneFirewall` names a connection or a target that Geass cannot use. Check the connection, and make sure that it is for Compute at the cloud where the control plane runs. Make sure that the target is the correct ID. Geass cannot use a cloud other than AWS or Hetzner. |
| A node stays `Joining`, then `Failed` after 15 minutes | K3s installed, and the node did not register. The machine cannot reach the control plane on 6443/tcp, or the overlay port is closed. Open the port, or use `controlPlaneFirewall`. Geass deletes the machine as soon as the node fails, so its log is gone. Check the ports first, because they are the usual cause. |
| Machines at the cloud that no node owns | See [Machines no node owns](#machines-no-node-owns). Delete them in the console of the cloud, or set `orphanPolicy: Delete`. |
| A node does not go away on delete | Read its message. The node waits for the machine to be deleted, or for the connection. If the connection is gone permanently, annotate the node `geass.dev/abandon-instance=true`. Then delete the machine at the cloud yourself. |
| The autoscaler never removes a node | See [What keeps a node](#what-keeps-a-node). `geass pools show NAME` and the operator log name the reason. |
| The services restarted when I made the first pool | Expected, one time. See [What runs on a pool's nodes](#what-runs-on-a-pools-nodes). |
| The cloud bills more than the cost of the pool | The estimate is the list price of machines only. Public addresses, traffic, and disks that the cloud bills separately are extra. |

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
