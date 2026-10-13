---
title: "High availability"
description: "What high availability means in Geass today: how HA databases are gated and placed, what survives losing a node, and what is not highly available."
---

Geass has one high availability feature today: a database that runs as three instances on three nodes, so that it keeps serving when one node is lost. It does not make the control plane, the dashboard, your services or object storage highly available. This page says what the feature does, how Geass decides a cluster is ready for it, and, plainly, what is a single point of failure.

## What is and is not highly available

| Part | Highly available? | Notes |
| --- | --- | --- |
| Postgres with **High availability** | Yes, with three instances | Replicas and failover come from the Postgres operator (CloudNativePG). Needs at least three healthy nodes. |
| MySQL and Redis with **High availability** | Replicated, with no automatic failover | Geass asks for a primary with replicas and configures no failover. See [MySQL and Redis](#mysql-and-redis). |
| Services | Only as far as replicas go | Replicas are not spread by any Geass rule. See [Services](#services). |
| Control plane (K3s server, operator, dashboard) | **No** | One server, one operator. See [What is not highly available](#what-is-not-highly-available). |
| Single-instance databases, the object storage server, SQLite | No | One instance each. |

## Before you start

- Three or more nodes that are ready and not cordoned. See [Add servers](/docs/getting-started/add-servers/).
- The readiness check has been run at least once (see below).
- A project database in a project that has a region needs its region's nodes to carry the three instances. Geass does not check that. See [Where the instances run](#where-the-instances-run).

## Check readiness

Geass lets a highly available database start only when a readiness report says the cluster can host one.

1. Open **Usage** in the left rail.
2. Scroll to **HA readiness**. It says "High availability databases need at least three healthy nodes." and shows **Healthy nodes** and **Ready for HA** (Yes or No). Until a report exists, **Healthy nodes** shows the dashboard's own count of ready nodes (cordoned ones included) while **Ready for HA** reads No.
3. Choose **Run readiness check**. Admins only: the button is disabled for viewers.

The report is a `GeassHAReadiness` object named `platform` in `geass-system`. Its conditions are:

| Condition | True when |
| --- | --- |
| `NodesReady` | At least three nodes are ready and not cordoned. Every region and the control plane node count. |
| `StorageReady` | The cluster has at least one storage class. |
| `ClusterReady` | The cluster's `Ready` condition is true. |
| `AddonsReady` | The cluster's add-ons are ready (cert-manager, monitoring, and Longhorn when it is on). |
| `Ready` | All four are true. |

```bash
kubectl -n geass-system get geasshareadiness platform -o yaml
```

:::note
**Run readiness check** creates the report, and nothing else does, so on a new cluster **Ready for HA** reads **No** until someone runs the check. After that the operator keeps the report up to date by itself. It evaluates it again when a node is added or removed, becomes ready or not ready, or is cordoned or uncordoned, when a node's region or tags change, when a storage class is created or removed, and when the `GeassCluster` changes or its `Ready` or `AddonsReady` condition flips. Choosing the button again does nothing, because the report already exists.
:::

To force an evaluation, change the object. This also works:

```bash
kubectl -n geass-system annotate geasshareadiness platform geass.dev/recheck="$(date +%s)" --overwrite
```

Deleting the object and choosing **Run readiness check** also works.

## Create a highly available database

**On a shared database server.** In **Settings**, open **Databases** and set up a server. Switch on **High availability**: "Three instances, so the server survives losing a node." The switch is off and reads "Needs at least three healthy nodes." until the report says **Ready for HA** (see [Shared database servers](/docs/guides/shared-database-servers/)).

**On a project database.** The dashboard's create dialog has no high availability option. Use the API or a manifest.

```bash
curl -sS -X POST https://geass.example.com/api/databases/create \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d name=orders -d project=morning-beach -d environment=production \
  -d engine=Postgres -d highAvailability=on
```

```yaml title="orders-db.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassDatabase
metadata:
  name: orders
  namespace: geass-system
spec:
  project: morning-beach
  environment: production
  engine: Postgres
  version: "16"
  highAvailability: true      # at least 3 instances
  instances: 3
  storageSize: 20Gi
```

`highAvailability: true` forces at least three instances. So does `instances` of 3 or more, even without the flag. The dashboard has no field to change it afterwards. A database created through the API is a draft until you deploy it with **Deploy changes**. A manifest like the one above is not a draft: the operator starts provisioning it as soon as you apply it.

### What the gate does

Before it provisions any in-cluster database that asks for high availability, the operator needs the readiness report to be `Ready` with at least three healthy nodes. Until then the database's `Ready` condition reads "High availability requires a cluster with at least three healthy nodes". That is a waiting message, not a failure: Geass looks again every 30 seconds and never marks the database failed for it. The dashboard does not show that text. It shows the database as **Unavailable** with a generic hint, so read the condition with `kubectl`:

```bash
kubectl -n geass-system get geassdatabase orders \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}{"\n"}'
```

The gate covers Postgres, MySQL and Redis alike. The capacity check also counts three instances (see [Scaling and placement](/docs/guides/scaling-and-placement/)).

## Where the instances run

- **Postgres.** Geass creates a CloudNativePG cluster with three instances. It sets a node selector and tolerations only to keep the instances in the project's region and on the tags you chose. For spreading across nodes it leaves the Postgres operator's own default in place. Geass sets no rule that spreads instances across sites, and it does not check that a region has three nodes.
- **Required tags.** A project that requires node tags ([Compliance node tags](/docs/guides/compliance-tags/)) keeps all three instances on nodes that carry them. The readiness report counts every node in the cluster, not the tagged ones, so give the tags at least three nodes yourself.
- **Volumes.** With the Longhorn storage add-on, a highly available Postgres uses the `geass-single` class. Each instance's volume has one copy, on the node where the instance runs, because the three instances already hold three copies of the data. See [Storage](/docs/guides/storage/).
- **Sites.** A node's **Site** is written as the standard `topology.kubernetes.io/zone` label, so software that reads it can use it. Geass itself does not spread replicas by site.

The readiness report counts nodes in the whole cluster. A project in a region with one node still passes the gate, and its three instances may then share that node. Give a region at least three nodes before you put a highly available database in it, and check where the instances landed:

```bash
kubectl get pods -n morning-beach-production -l cnpg.io/cluster=orders -o wide
```

### MySQL and Redis

For MySQL and Redis, Geass asks the chart for a `replication` layout: one primary and replicas. It configures no failover. If the primary's node is lost, nothing in Geass promotes a replica. Redis runs without persistence, so a Redis instance loses its data when its pod restarts, highly available or not.

### Services

A service with several replicas keeps serving while some of them are up. Geass adds no anti-affinity, no topology spread and no pod disruption budget, so the Kubernetes scheduler alone decides where replicas go and two can share a node. Losing that node takes both. To spread replicas across nodes deliberately, give the service a region and tags that match several nodes and check with `kubectl get pods -o wide`.

## When a node is lost

| What was on the node | What happens |
| --- | --- |
| A Postgres instance (highly available) | The Postgres operator promotes a replica if the lost instance was the primary. The `<name>-rw` service follows the primary, so clients that use the database's address reconnect. The lost instance's volume has its only copy on the lost node, so that instance is back when the node returns. The other two keep serving. |
| A single-instance database | It is unavailable until it runs again. Whether it can restart on another node depends on its storage. See [Storage](/docs/guides/storage/). |
| A service replica | Kubernetes starts a replacement on another node that matches the project's region and tags and has room. By default it waits five minutes before it does; Geass does not change that. A service with one replica is down in the meantime. |
| A MySQL or Redis primary | Nothing promotes a replica. See [MySQL and Redis](#mysql-and-redis). |
| The control plane | See below. |

A worker that Geass joined over SSH and that stays not ready for more than five minutes is provisioned again: the operator connects to the machine and reinstalls the K3s agent. If the machine is gone, that fails and the node's `GeassNode` shows the error (see [Troubleshooting](/docs/operations/troubleshooting/)). Backups are your protection against losing every copy: a Postgres database's continuous backups can be restored to a new database (see [Backups and restore](/docs/guides/backups-and-restore/)).

## What is not highly available

:::danger
The control plane is a single point of failure. Geass installs one K3s server with embedded etcd and one operator pod. There is no second control plane node, and Geass has no way to add one.
:::

- **The K3s server.** The installer runs `k3s server --cluster-init` on one machine. A `GeassNode` with the `ControlPlane` role only waits for an existing node to register; it does not provision a server. Workers always join as agents.
- **The operator and the dashboard.** The dashboard is part of the operator process. The operator runs as one replica, and its Deployment requires a node with the control-plane label, because the operator image is imported on the control plane only. While that machine is down, the dashboard is unreachable, nothing is reconciled, and nothing can be deployed, scaled or restored.
- **What keeps running.** Containers that are already running on other nodes generally keep running while the control plane is down, but nothing can be changed, restarted elsewhere or rescheduled until it returns.
- **Recovery data for the cluster itself.** K3s takes its own etcd snapshots by default on the server's disk. That is a K3s feature that Geass does not configure, copy off the machine, or document restoring. Treat the control plane machine as something to protect with your provider's snapshots.
- **Ingress and DNS.** Public traffic enters through the K3s Traefik on your nodes. A domain that points at one address is only as available as that machine. Geass does not manage failover between addresses.
- **Object storage and monitoring.** The object storage server (SeaweedFS or Garage), Prometheus and the log store each run one instance on one volume. With Longhorn the object storage server's volume has copies on other nodes (up to three), which protects its data when a node is lost but does not make the server highly available.
- **A whole site.** A cluster spread over several providers is experimental (see [Nodes at more than one provider](/docs/operations/multi-provider/)), and its control plane still sits at one site.

## What happens under the hood

| Object | Where | Purpose |
| --- | --- | --- |
| `GeassHAReadiness` `platform` | `geass-system` | The readiness report. |
| CloudNativePG `Cluster` named after the database | `<project>-<environment>` (or `geass-system` for a shared server) | Three Postgres instances. The operator itself runs in `cnpg-system`. |
| Services `<name>-rw`, `<name>-ro`, `<name>-r` | The database's namespace | Read-write follows the primary. Geass hands out the `-rw` address. |
| `GeassNode` | `geass-system` | One per worker the operator joined. It reprovisions a worker that stays not ready for five minutes. |

```bash
kubectl get cluster.postgresql.cnpg.io -A
kubectl get pods -n morning-beach-production -l cnpg.io/cluster=orders -o wide
kubectl -n geass-system get geassnodes
kubectl -n geass-system get deploy geass-controller-manager -o wide
```

## Troubleshooting

**The database says "High availability requires a cluster with at least three healthy nodes".**
Check, in this order. First, `kubectl get nodes`: you need three that are ready and not cordoned. Second, whether anyone has run **Run readiness check**: without a report the gate never opens. Third, `kubectl -n geass-system get geasshareadiness platform -o jsonpath='{.status.conditions}'` for a condition that is false, such as `AddonsReady` while an add-on is still installing. The report follows changes to nodes, storage classes and the cluster by itself. If it still reads wrong after a few seconds, annotate it as shown above and check the operator's log.

**The High availability switch is off on the shared server form.**
The report does not say **Ready for HA**. Run the check if no report exists yet, and read the report's conditions.

**The report says Yes, but the three instances are on one node.**
The gate counts nodes in the whole cluster, not in the database's region. Add nodes to the region, then recreate the database in it. Placement is decided once, so changing the region later does not move it.

**One instance stays `Pending` after a node was lost.**
Its volume has a single copy on the lost node. Bring the node back, or restore the database from backup into a new one.

**Choosing Run readiness check a second time changed nothing.**
That is expected after the first run: the report exists and updates itself. See [Check readiness](#check-readiness).

## Related

- [Shared database servers](/docs/guides/shared-database-servers/)
- [Databases](/docs/guides/databases/)
- [Storage](/docs/guides/storage/)
- [Backups and restore](/docs/guides/backups-and-restore/)
- [Nodes at more than one provider](/docs/operations/multi-provider/)
