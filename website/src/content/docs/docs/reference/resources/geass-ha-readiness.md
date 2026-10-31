---
title: "GeassHAReadiness"
description: "Reference for GeassHAReadiness, the report that says if the cluster has the nodes, storage class, and add-ons for highly available databases."
sidebar:
  order: 15
---

A `GeassHAReadiness` is a report that answers one question: can the cluster run a highly available database? A database that asks for high availability (three instances) waits until this report says `Ready`. The report does these checks:

- It counts the healthy nodes.
- It checks that a storage class exists.
- It checks that the [GeassCluster](/docs/reference/resources/geass-cluster/) and its add-ons are ready.

The dashboard creates the report when you click **Run readiness check** in **Settings** → **Usage**. You can also create the report with `kubectl`. There is one report, named `platform`, in `geass-system`.

:::note
The operator keeps the report current. The operator evaluates the report when the object is created or changed. It evaluates the report again in these cases:

- A node is added or removed, becomes ready or not ready, or is cordoned.
- The region or the tags of a node change.
- A storage class is created or removed.
- The cluster changes, or its `Ready` or `AddonsReady` condition flips.

**Run readiness check** creates the report only when the report does not exist. On an existing report, the button does nothing, and it does not need to.
:::

## Summary

| Property | Value |
| --- | --- |
| API version | `geass.geass.dev/v1alpha1` |
| Kind | `GeassHAReadiness` |
| Plural | `geasshareadinesses` |
| Scope | Namespaced |
| Namespace | `geass-system` |
| Subresources | `status` |
| Printer columns | None |

## Example

```yaml title="ha-readiness.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassHAReadiness
metadata:
  name: platform
  namespace: geass-system
spec:
  clusterRef: default
```

The dashboard sends an empty `spec`, which means "the first cluster Geass finds".

## Spec fields

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.clusterRef` | string | empty | none | The name of the `GeassCluster` whose readiness counts. Empty uses the first cluster in the namespace. |

## Status

| Field | Type | Meaning |
| --- | --- | --- |
| `status.healthyNodes` | integer | The nodes that are `Ready` and not marked unschedulable. The count includes every node in the cluster, control plane included, in every region. |
| `status.storageClassReady` | boolean | `true` when the cluster has at least one storage class. K3s ships one, so this value is `true` on any K3s cluster. |
| `status.clusterReady` | boolean | `true` when the `GeassCluster` has `Ready` set to `True`. |
| `status.addonsReady` | boolean | `true` when the `GeassCluster` has `AddonsReady` set to `True`. |
| `status.conditions` | list | See below. |

### Conditions

| Type | `True` when | Reason |
| --- | --- | --- |
| `NodesReady` | `status.healthyNodes` is at least 3 | `HealthyNodes` |
| `StorageReady` | At least one storage class exists | `StorageClass` |
| `ClusterReady` | The `GeassCluster` is `Ready` | `ClusterReady` |
| `AddonsReady` | The add-ons of the `GeassCluster` are ready | `PlatformAddons` |
| `Ready` | All four above are `True` | `HAReadiness` |

The reason is the same when the condition is `True` and when it is `False`. The message always states the requirement, not the current state. For example: `At least three healthy schedulable nodes are required`. To see the state, read `status` and the status of the condition.

## How the dashboard uses it

**Settings** → **Usage** has an **HA readiness** section with these items:

- **Healthy nodes**.
- **Ready for HA**: Yes or No, from the `Ready` condition.
- A table with one row for each condition: **Healthy nodes** (`NodesReady`), **Storage for databases**
  (`StorageReady`), **Cluster** (`ClusterReady`), **Add-ons** (`AddonsReady`), and **Ready for HA** (`Ready`). The
  **Result** column shows **Passed** or **Not passed**. **What it needs** shows the message of the condition, and
  **Since** shows `lastTransitionTime`. The row **Healthy nodes** also says how many healthy nodes there are now.
- The **Run readiness check** button.

The dashboard reads the report from the same overview as all other data (`haReadiness` in `GET /api/bootstrap`). The button calls `POST /api/ha-readiness/check`, which creates the report if it does not exist. See [REST API](/docs/reference/rest-api/).

## How databases use it

A [GeassDatabase](/docs/reference/resources/geass-database/) with `spec.highAvailability: true` waits until a report in `geass-system` has both of these values:

- `Ready` set to `True`.
- `status.healthyNodes` of at least 3.

While the database waits, it shows the message `High availability requires a cluster with at least three healthy nodes`. If there is no report, the database waits. When the numbers change later, a database that is already running is not affected.

## Re-evaluate the report

You do not need to do this after you add or remove nodes, because the report follows them. To force an evaluation anyway, change the object:

```bash title="Create the report, or force a new evaluation"
kubectl apply -f ha-readiness.yaml
kubectl -n geass-system annotate geasshareadiness platform geass.dev/recheck="$(date +%s)" --overwrite
kubectl -n geass-system get geasshareadiness platform \
  -o jsonpath='{.status.healthyNodes}{" nodes, ready: "}{.status.conditions[?(@.type=="Ready")].status}{"\n"}'
```

## Editing by hand

You can safely create, update, and delete the report. Do not edit `status`, because the operator overwrites it. If you delete the report, HA databases wait until you create it again.

## What happens under the hood

The operator lists the nodes, the storage classes, and the `GeassCluster` objects. Then it writes `status` in one patch, and only when the new status is different from the stored status. The operator checks the `GeassCluster` named by `spec.clusterRef`, or the first cluster when `spec.clusterRef` is empty.

The operator watches these objects, and a change to any of them re-evaluates the report:

- The nodes: their creation and deletion, when they are cordoned or tainted, when they go `Ready` or `NotReady`, and their region and tag labels.
- The storage classes.
- The clusters: their spec, and their `Ready` and `AddonsReady` conditions.

The operator ignores the status of the report itself.

## Troubleshooting

**A highly available database says `High availability requires a cluster with at least three healthy nodes`.** Check the report: `kubectl -n geass-system get geasshareadiness platform -o yaml`. If the report does not exist, create it. If the report exists with fewer than three nodes, add servers (see [Add servers](/docs/getting-started/add-servers/)). The report updates when the servers are ready. A node that is `NotReady` or cordoned does not count.

**`healthyNodes` does not change after I add a node.** The report follows nodes when they become ready, so wait for the node to be `Ready`. If the number is still wrong, force an evaluation (see above) and read the log of the operator.

**`Ready` stays `False` with three or more nodes.** Look at `clusterReady` and `addonsReady`. These values mirror the `Ready` and `AddonsReady` conditions of the `GeassCluster`. See [GeassCluster](/docs/reference/resources/geass-cluster/).

**The HA section shows a node count but Ready for HA says No.** There might be no report yet. If there is no report, the dashboard takes the node count from its capacity numbers and treats HA as not ready. Click **Run readiness check**.

## Related

- [High availability](/docs/operations/high-availability/)
- [GeassDatabase](/docs/reference/resources/geass-database/)
- [GeassCluster](/docs/reference/resources/geass-cluster/)
- [GeassNode](/docs/reference/resources/geass-node/)
- [Resources overview](/docs/reference/resources/overview/)
