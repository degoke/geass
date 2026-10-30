---
title: "GeassNodePool"
description: "Reference for GeassNodePool: nodes that Geass creates at a cloud, or joins from servers that you list, and adds and removes with the load."
sidebar:
  order: 8.5
---

A `GeassNodePool` is a rule for how many nodes of one kind the cluster has, and where they come from. Geass makes the nodes. It creates machines at a cloud with a [connection for machines](/docs/guides/connectors/clouds/), or joins servers that you list. Geass installs K3s on the nodes, and removes them again. A pool that does not autoscale keeps exactly `minNodes`. An autoscaling pool adds nodes when pods cannot start because there is no room, and removes idle nodes. `maxNodes` is required. It is the most nodes that the pool ever has, and so the cap on what the pool can cost.

For the task-oriented view, see [Node pools and autoscaling](/docs/guides/node-pools/). It tells how the pool decides, what keeps a node, and what to check when the pool stops. The dashboard makes and edits pools on the **Pools** page, and `geass pools` does the same from a terminal. Pools are experimental, like the clouds behind them.

## Summary

| Property | Value |
| --- | --- |
| API version | `geass.geass.dev/v1alpha1` |
| Kind | `GeassNodePool` |
| Plural | `geassnodepools` |
| Scope | Namespaced. Create it in `geass-system`. |
| Subresources | `status` |
| Printer columns | `Phase`, `Min`, `Max`, `Nodes`, `Ready`, `Size` |
| Owns | One [GeassNode](/docs/reference/resources/geass-node/) for each node, named `<pool>-<five characters>`, or the server's name for an inventory pool. When you delete the pool, Geass deletes them |

The pool's name is at most 48 characters. Geass makes a node's name from the pool's name, and a node's name must not be more than 55 characters.

## Examples

### An autoscaling pool at a cloud

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
  minNodes: 0
  maxNodes: 5
  autoscaling:
    enabled: true
    minNodeLifetimeMinutes: 55
  placement:
    region: eu
    zone: hetzner-fsn1
```

### A fixed pool

```yaml title="fixed-pool.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassNodePool
metadata:
  name: fixed
  namespace: geass-system
spec:
  connectionRef: hetzner
  size: cx23
  minNodes: 2
  maxNodes: 2
```

### A pool of servers you own

```yaml title="spare-pool.yaml"
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
  maxNodes: 2
  autoscaling:
    enabled: true
```

The Secret `spare-ssh` holds the private key under `ssh-privatekey`, and optionally the servers' host keys under `known_hosts`. This Secret is the same as for a [server you add by hand](/docs/getting-started/add-servers/#prepare-the-new-server).

## Spec fields

| Field | Type | Required or default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.cluster` | string | `default` | None | The [GeassCluster](/docs/reference/resources/geass-cluster/) that the nodes join, in `geass-system`. |
| `spec.connectionRef` | string | One of this and `inventory` | None | The [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/) that creates the machines. It must have the purpose `Compute` and no `spec.project`. |
| `spec.inventory.hosts` | list | One of this and `connectionRef` | 1 to 100 items, keyed by `name` | Servers that you own, instead of a cloud. |
| `spec.inventory.hosts[].name` | string | Required | Pattern `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, at most 63 | The node's name after it joins. |
| `spec.inventory.hosts[].address` | string | Required | Not empty | Where Geass reaches the server over SSH. In a multi-site cluster, it is also the address that the node advertises. |
| `spec.inventory.hosts[].sshPort` | integer | `22` | None | The SSH port. |
| `spec.inventory.hosts[].sshUser` | string | Required | Not empty | `root`, or a user that can use `sudo` without a password. |
| `spec.inventory.hosts[].sshKeySecretRef` | SecretReference | Required | None | The Secret that holds the private key, and optionally `known_hosts`. |
| `spec.region` | string | Optional | None | The cloud's own place: a location, zone, or region. Its meaning is [per cloud](/docs/guides/connectors/clouds/#the-clouds). |
| `spec.size` | string | Required with `connectionRef` | None | The cloud's name for a machine size, such as `cx23` or `t3.medium`. |
| `spec.fallbackSizes` | list of strings | Optional | At most 5 | Geass tries these in order when the cloud has no room for `size`, or the account is at a limit. |
| `spec.fallbackRegions` | list of strings | Optional | At most 5 | Geass tries these in order when the cloud has no room in `region`. |
| `spec.image` | string | The cloud's Ubuntu 24.04 | None | The operating system image. |
| `spec.spot` | boolean | `false` | None | Ask for the cloud's cheaper machines that it might take back, where the driver supports them: AWS, Azure, Google Cloud, Oracle Cloud, and the webhook driver. Geass replaces a node that the cloud takes back. |
| `spec.addressType` | string | `Public` | Enum `Public`, `Private` | The address of a machine that Geass uses to reach it. `Private` suits a pool whose machines share a private network with the control plane. |
| `spec.settings` | map of strings | Optional | None | Values specific to the cloud, such as a subnet or a security group. The pool is `Degraded` when it sets a key that the cloud does not read, or does not set a key that the cloud needs. |
| `spec.placement` | object | Optional | As on [GeassNode](/docs/reference/resources/geass-node/#spec-fields) | The region, site, and tags that the nodes carry. |
| `spec.storage` | string | `None` | Enum `None`, `Allow` | `None` keeps databases, object storage servers, and volume copies off the nodes, which have the taint `geass.dev/ephemeral`. Only services run there. `Allow` makes them ordinary workers. |
| `spec.minNodes` | integer | `0` | 0 to 200 | The fewest nodes that the pool has. A pool that does not autoscale has exactly this many. |
| `spec.maxNodes` | integer | Required | 0 to 200, and at least `minNodes` | The most nodes that the pool ever has. It counts machines that Geass is creating or removing. |
| `spec.autoscaling.enabled` | boolean | `false` | None | Let the pool follow the load between `minNodes` and `maxNodes`. |
| `spec.autoscaling.scaleUpDelaySeconds` | integer | `30` | 1 to 3600 | How long a pod must have been unable to start before the pool adds a node for it. |
| `spec.autoscaling.maxScaleUp` | integer | `5` | 1 to 50 | The most nodes that the pool adds in one step. |
| `spec.autoscaling.scaleDownUtilizationPercent` | integer | `50` | 1 to 100 | A node is a candidate for removal when its pods reserve less than this share of its CPU or memory, whichever is higher. |
| `spec.autoscaling.scaleDownAfterMinutes` | integer | `10` | 1 to 1440 | How long a node must have been a candidate before the pool removes it. |
| `spec.autoscaling.scaleDownDelayAfterAddMinutes` | integer | `10` | 1 to 1440 | After the pool adds a node, it removes no node for this long. |
| `spec.autoscaling.minNodeLifetimeMinutes` | integer | `0` | 0 to 10080 | The pool does not remove a node before the node is this old. |
| `spec.autoscaling.spareNodes` | integer | `0` | 0 to 20 | Empty nodes kept ready for a burst. They count against `maxNodes` and cost money while idle. |
| `spec.orphanPolicy` | string | `Report` | Enum `Report`, `Delete` | What to do with a machine at the cloud that carries the pool's labels and belongs to no node. `Delete` removes such a machine after 20 minutes. |
| `spec.suspend` | boolean | `false` | None | Stop adding and removing nodes. The nodes that the pool has stay. |

The CRD also checks three rules on `spec`: `maxNodes must not be below minNodes`, `a pool uses either a cloud connection or an inventory of servers, not both and not neither`, and `a pool that creates machines at a cloud needs a size`. For an inventory pool, the operator also requires two things:

- `maxNodes` is at most the number of servers.
- No server name is listed twice.

The operator reports a breach of these rules as `Degraded`.

:::caution
Do not expect a change to the size, region, image, settings, or fallbacks to touch the nodes that the pool has. These settings apply only to machines made from then on. A pool cannot change between a cloud and an inventory. To change that, make a new pool.
:::

## Status

| Field | Type | Meaning |
| --- | --- | --- |
| `status.phase` | string | How the pool is doing. See the phases table after this table. |
| `status.message` | string | What the pool is doing, or why it is not doing something. |
| `status.observedGeneration` | integer | The generation of the spec that the status describes. |
| `status.desiredNodes` | integer | How many nodes the pool works towards. |
| `status.nodes`, `status.readyNodes` | integer | How many nodes the pool has, in any state, and how many are ready. |
| `status.pendingPods` | integer | Pods that could not start, and could start on a node of this pool, at the last look. |
| `status.size` | object | The size that Geass makes nodes with now: `name`, `vcpus`, `memoryMiB`, `hourlyPrice`, and `currency`. `name` differs from `spec.size` while a fallback is in use. |
| `status.nodeAllocatable` | object | What a new node offers to pods, after what K3s and the system pods keep. The capacity check uses this value. |
| `status.estimatedHourlyCost` | string | What the pool's machines cost for an hour at the cloud's list price, when the cloud states one. It leaves out tax, traffic, and anything that the cloud bills separately. |
| `status.consecutiveFailures`, `status.countedFailures` | integer, list | Machines that failed to become nodes since the last machine that did, and the nodes already counted. Three failures pause the pool. |
| `status.pausedUntil` | date-time | When a paused pool tries again. |
| `status.lastScaleUp`, `status.lastScaleDown` | date-time | When the pool last added and last removed a node. |
| `status.scaleDownBackoffUntil` | date-time | When a pool tries again after its last attempt to remove a node failed, for example because a pod did not leave. |
| `status.lastFailure`, `status.lastFailureAt` | string, date-time | Why the most recent machine failed to become a node, and when. |
| `status.fallbackStep`, `status.fallbackSince` | integer, date-time | Which combination of size and region new nodes use. 0 is the first of each. The value goes back to 0 an hour after the pool took a fallback. |
| `status.unneededSince` | map | For each node that is a candidate for removal, since when. |
| `status.orphans` | list | Machines at the cloud that carry the pool's labels and belong to no node. At most 50. |
| `status.conditions` | list | One condition, `Ready`. It is `True` when the phase is `Ready`, and has the phase as its reason, with the message. |

| Phase | Meaning |
| --- | --- |
| `Ready` | The pool has the nodes that it wants, and they are ready. |
| `Scaling` | The pool is adding or removing nodes, or some nodes are not ready yet. |
| `Degraded` | The spec is invalid in a way that the CRD could not catch. Or the connection is missing, is not for Compute, or belongs to a project. Or the cloud does not accept its credentials, or a setting is wrong. The message says which. The pool makes no more nodes. |
| `Paused` | Three machines in a row failed to become nodes. The pool makes no more nodes until `status.pausedUntil`, or until you resume it. |
| `Suspended` | `spec.suspend` is on. |
| `Pending` | The cluster that the pool names does not exist. |

## Annotations

| Annotation | On | Meaning |
| --- | --- | --- |
| `geass.dev/resume` | The pool | Any value. Ends a pause and forgets the failures behind it. Then the operator removes the annotation. `geass pools resume` and the dashboard's **Resume** set it. |
| `geass.dev/scale-down-disabled: "true"` | A Kubernetes node of the pool | Keeps the autoscaler from removing the node. |
| `geass.dev/safe-to-evict` | A pod | `"false"` keeps the autoscaler from removing the node that the pod runs on. `"true"` says that the autoscaler can move the pod, although the pod would otherwise block the removal. |
| `geass.dev/abandon-instance: "true"` | A GeassNode of the pool | Lets you delete the node without deleting its machine at the cloud. Use it for a connection that is gone permanently. |

## Labels, taints and what the pool makes

| Where | What |
| --- | --- |
| Each node, as a `GeassNode` | `spec.pool`, `spec.storage`, and `spec.placement` from the pool. For a cloud, `spec.cloud` with the connection, region, size, image, `spot`, `addressType`, and `settings`. The label `geass.dev/pool=<pool>`, and `geass.dev/autoscaled=true` for an autoscaling pool |
| Each node, as a Kubernetes node | The labels `geass.dev/pool=<pool>`, `geass.dev/autoscaled=true`, and `geass.dev/storage=none` (for `storage: None`), and the placement labels. For `storage: None`, the taint `geass.dev/ephemeral=true:NoSchedule` |
| A node that the autoscaler removes | The annotations `geass.dev/pool-cordon` and `geass.dev/pool-cordon-at`, so that Geass uncordons only a node that it cordoned |
| The cloud | A machine for each node, with the labels `geass-managed=true`, `geass-cluster`, `geass-pool`, and `geass-node` |

## What the dashboard writes

| Where | What it does |
| --- | --- |
| **Pools** → **Add pool** | Makes the pool (`POST /api/pools/create`). The dashboard makes nothing before you click **Create pool**. |
| A pool's page | Shows status, nodes, and settings. The settings form (**Save changes**) changes what applies to machines made from then on, the numbers of autoscaling, `suspend`, and the orphan policy. You set the placement and an inventory when you make the pool. **Resume** adds `geass.dev/resume`. **Delete pool** asks you to enter its name. |
| **Usage** → **Nodes** | A **Pool** column with an **Ephemeral** badge. A pool's nodes have no **Remove**. |

## Editing by hand

Geass supports a `GeassNodePool` that you create with `kubectl` as fully as one from the dashboard. A manifest skips the dashboard's checks. So the operator reports what is wrong as `Degraded`, and does not refuse the manifest. Do not edit `status`.

- **Changing `minNodes` or `maxNodes`.** If you raise `minNodes`, Geass makes machines immediately. If you lower `maxNodes` below the nodes that the pool has, Geass does not remove any. To remove nodes from a fixed pool, lower `minNodes`.
- **Deleting the pool.** Geass deletes its nodes with it. Geass drains each node for up to five minutes, and deletes its machine. See [Node pools](/docs/guides/node-pools/#delete-a-pool-or-a-node).
- **Deleting a node by hand** is allowed. A pool that wants the node makes another node. To lower the number, lower `minNodes`.

## What happens under the hood

The pool controller looks about every 10 seconds while a pool autoscales, and every two minutes otherwise. It also looks when one of the pool's nodes changes. At each look, the pool controller does these steps:

1. It lists the pool's nodes.
2. It counts the machines that cost money. A failed node whose machine is gone does not count.
3. It calculates how many nodes to add or remove.
4. It writes `GeassNode` objects.
5. It writes the status one time, at the end.

The node controller does the rest, for one node at a time. It creates the machine, waits, installs K3s over SSH, and watches the node. It replaces the node if the node is lost. On deletion, it drains the node, removes it, and deletes the machine. Every ten minutes, the pool lists the cloud's machines that carry its labels and compares them with its nodes.

```bash
kubectl -n geass-system get geassnodepools
kubectl -n geass-system get geassnodepool burst -o yaml
kubectl -n geass-system get geassnodes -l geass.dev/pool=burst
```

## Troubleshooting

See the table in [Node pools and autoscaling](/docs/guides/node-pools/#troubleshooting) and [Pools and machines at a cloud](/docs/operations/troubleshooting/#pools-and-machines-at-a-cloud). For the shortest checks, read `status.message` and `status.lastFailure`, and then each node's `status.message`.

## Related

- [Node pools and autoscaling](/docs/guides/node-pools/)
- [Cloud connections for machines](/docs/guides/connectors/clouds/)
- [GeassNode](/docs/reference/resources/geass-node/)
- [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/)
- [GeassCluster](/docs/reference/resources/geass-cluster/)
- [Resources overview](/docs/reference/resources/overview/)
