---
title: "GeassHAReadiness"
description: "Reference for GeassHAReadiness: the report of whether the cluster has three healthy nodes, a storage class and ready add-ons for highly available databases."
sidebar:
  order: 15
---

A `GeassHAReadiness` is a report that answers one question: can the cluster run a highly available database? A database that asks for high availability (three instances) waits until this report says `Ready`. The report counts healthy nodes, checks that a storage class exists, and checks that the [GeassCluster](/docs/reference/resources/geass-cluster/) and its add-ons are ready.

The dashboard creates the report when you choose **Run readiness check** in **Settings** → **Usage**. You can also create it with `kubectl`. There is one report, named `platform`, in `geass-system`.

:::note
The operator keeps the report current. It evaluates it when the object is created or changed, and again when a node is added, removed, becomes ready or not ready, is cordoned or has its region or tags changed, when a storage class is created or removed, and when the cluster changes or its `Ready` or `AddonsReady` condition flips. **Run readiness check** creates the report only when it does not exist, so on an existing report it does nothing, and does not need to.
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
| `status.healthyNodes` | integer | The nodes that are `Ready` and not marked unschedulable. It counts every node in the cluster, control plane included, in every region. |
| `status.storageClassReady` | boolean | `true` when the cluster has at least one storage class. K3s ships one, so this is `true` on any K3s cluster. |
| `status.clusterReady` | boolean | `true` when the `GeassCluster` has `Ready` set to `True`. |
| `status.addonsReady` | boolean | `true` when the `GeassCluster` has `AddonsReady` set to `True`. |
| `status.conditions` | list | See below. |

### Conditions

| Type | `True` when | Reason |
| --- | --- | --- |
| `NodesReady` | `status.healthyNodes` is at least 3 | `HealthyNodes` |
| `StorageReady` | At least one storage class exists | `StorageClass` |
| `ClusterReady` | The `GeassCluster` is `Ready` | `ClusterReady` |
| `AddonsReady` | The `GeassCluster`'s add-ons are ready | `PlatformAddons` |
| `Ready` | All four above are `True` | `HAReadiness` |

The reason is the same whether the condition is `True` or `False`, and the message always states the requirement (for example `At least three healthy schedulable nodes are required`), not the current state. Read `status` and the status of the condition to see the state.

## How the dashboard uses it

**Settings** → **Usage** has an **HA readiness** section with **Healthy nodes**, **Ready for HA** (Yes or No, from the `Ready` condition) and the **Run readiness check** button. It reads the report from the same overview as everything else (`haReadiness` in `GET /api/bootstrap`). The button calls `POST /api/ha-readiness/check`, which creates the report if it does not exist. See [REST API](/docs/reference/rest-api/).

## How databases use it

A [GeassDatabase](/docs/reference/resources/geass-database/) with `spec.highAvailability: true` waits, with the message `High availability requires a cluster with at least three healthy nodes`, until a report in `geass-system` has `Ready` set to `True` and `status.healthyNodes` of at least 3. With no report at all, the database waits. A database that is already running is not affected when the numbers change later.

## Re-evaluate the report

You do not need this after adding or removing nodes: the report follows them. To force an evaluation anyway, change the object:

```bash title="Create the report, or force a new evaluation"
kubectl apply -f ha-readiness.yaml
kubectl -n geass-system annotate geasshareadiness platform geass.dev/recheck="$(date +%s)" --overwrite
kubectl -n geass-system get geasshareadiness platform \
  -o jsonpath='{.status.healthyNodes}{" nodes, ready: "}{.status.conditions[?(@.type=="Ready")].status}{"\n"}'
```

## Editing by hand

It is safe to create, update and delete the report. Do not edit `status`: the operator overwrites it. Deleting the report makes HA databases wait until you create it again.

## What happens under the hood

The operator lists the nodes, the storage classes and the `GeassCluster` objects, then writes `status` in one patch, and only when it differs from what is stored. The `GeassCluster` it checks is the one named by `spec.clusterRef`, or the first when that is empty. It watches the nodes (their creation and deletion, being cordoned or tainted, going `Ready` or `NotReady`, and their region and tag labels), the storage classes, and the clusters (their spec, and their `Ready` and `AddonsReady` conditions), so any of them re-evaluates the report. It ignores the status of the report itself.

## Troubleshooting

**A highly available database says `High availability requires a cluster with at least three healthy nodes`.** Check the report: `kubectl -n geass-system get geasshareadiness platform -o yaml`. If it does not exist, create it. If it exists with fewer than three nodes, add servers (see [Add servers](/docs/getting-started/add-servers/)). The report updates when they are ready. A node that is `NotReady` or cordoned does not count.

**`healthyNodes` does not change after I add a node.** The report follows nodes as they become ready, so wait for the node to be `Ready`. If the number is still wrong, force an evaluation (see above) and read the operator's log.

**`Ready` stays `False` with three or more nodes.** Look at `clusterReady` and `addonsReady`. They mirror the `Ready` and `AddonsReady` conditions of the `GeassCluster`. See [GeassCluster](/docs/reference/resources/geass-cluster/).

**The HA section shows a node count but Ready for HA says No.** There may be no report yet: with none, the dashboard takes the node count from its capacity numbers and treats HA as not ready. Choose **Run readiness check**.

## Related

- [High availability](/docs/operations/high-availability/)
- [GeassDatabase](/docs/reference/resources/geass-database/)
- [GeassCluster](/docs/reference/resources/geass-cluster/)
- [GeassNode](/docs/reference/resources/geass-node/)
- [Resources overview](/docs/reference/resources/overview/)
