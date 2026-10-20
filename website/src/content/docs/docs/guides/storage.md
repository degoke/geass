---
title: "Storage and volumes"
description: "Where Geass keeps database and object storage data: local disks by default, or the Longhorn add-on for replicated, resizable volumes, and its limits."
---

Databases, and the cluster's object storage server, keep their data on persistent volumes. This page explains which resources get a volume, the two kinds of storage Geass can put it on, how to turn on replicated and resizable storage, and what you can and cannot change after a volume exists. Read it before you create databases you care about, because the storage choice is made once.

## What uses a volume

| Resource | Volume | Size | Storage class |
| --- | --- | --- | --- |
| PostgreSQL, MySQL and SQLite databases | One per instance | 10 GiB by default (1 GiB for SQLite), or the size you choose | Decided once per database, see [The class is pinned per database](#the-class-is-pinned-per-database) |
| Redis | None. Persistence is off, so Redis is a cache | Not applicable | Not applicable |
| Cluster object storage server (SeaweedFS or Garage) | One | 10 GiB by default, or the size you choose when you set it up | Decided once, like a database's: see [The object storage server's volume](#the-object-storage-servers-volume) |
| Monitoring and log add-ons | One each | 5 GiB (Prometheus), 6 GiB (log store) | The cluster default class |
| Apps and functions | None | Not applicable | Not applicable |

The dashboard labels sizes such as 10 GiB as "10 GB" in the size list. The values are binary gibibytes (`10Gi`).

## Two kinds of storage

| | Local storage | Longhorn add-on |
| --- | --- | --- |
| Where the data is | One copy on one node's disk | One, two or three copies on different nodes |
| If the node is lost | The data is unavailable until the node returns, and gone if its disk is gone | The volume stays available from another copy when at least two copies exist |
| Resize a deployed database | No | Yes, larger only |
| Storage class | The cluster default (`local-path` on K3s) | `geass-replicated` and `geass-single`, created by Geass |
| When it is used | Longhorn is not installed, or the database was created before it was | Longhorn is installed when the database's volume is first created |

Geass installs the Longhorn add-on on clusters it builds. A cluster that does not run it, such as a local `kind` cluster, uses local storage. You can use Geass on local storage, but you cannot resize those databases and a node failure takes the data with it.

## The Geass storage classes

When Longhorn is on, Geass creates two storage classes. You never choose between them. Geass picks one for each database, and for the object storage server. Regions and compliance tags add more, as the table shows.

| Class | Copies | Used by | Binding |
| --- | --- | --- | --- |
| `geass-replicated` | 1 on a one-node cluster, 2 on two nodes, 3 on three or more | PostgreSQL (not HA), MySQL, SQLite, and the cluster object storage server | Immediate |
| `geass-single` | 1, kept on the node where the pod runs | High-availability PostgreSQL and MySQL, whose instances replicate the data themselves | When the pod is scheduled |
| `geass-replicated-<region>` | As above, counted over the nodes of one region | The same databases, in a project in that region, and an object storage server placed in that region | Immediate |
| `geass-tagged-<hash>` | As above, counted over the nodes that carry a project's required tags | The same databases, in a project that requires node tags, and an object storage server placed on nodes with tags | Immediate |

The copy count follows the number of schedulable nodes: nodes that exist, are not marked unschedulable and carry no `NoSchedule` or `NoExecute` taint. A node reserved for a tag (see [Scaling and placement](/docs/guides/scaling-and-placement/)) still counts, and Longhorn is allowed to run on it. Geass counts nodes that exist, not nodes that are Ready, so a node that is briefly unreachable does not change the count. The exception is a compliance node, described under [Compliance tags and storage classes](#compliance-tags-and-storage-classes).

If your nodes carry a region, Geass also creates one class per region, named `geass-replicated-<region>`. It keeps a region's copies on that region's nodes, so a project's data does not leave its region. A database in a region uses its region's class. `geass-single` stays global, because its one copy sits on the node that runs the pod.

Longhorn is installed from chart version 1.12.1, the newest line that runs on Kubernetes 1.25 and later. Geass turns off the chart's own storage class and sets Longhorn's web UI to zero replicas, so there is nothing to log in to.

## The class is pinned per database

A volume's class cannot change after the volume exists. Geass therefore decides a database's class once, when it first needs a volume, and records the decision in the annotation `geass.dev/storage-class` on the `GeassDatabase`. An empty value means the cluster default.

- A new database gets `geass-replicated`, or `geass-single` if it is high-availability PostgreSQL or MySQL, when Longhorn is installed. In a project with a region it gets the region's class, and in a project that requires node tags it gets `geass-tagged-<hash>` (see [Compliance tags and storage classes](#compliance-tags-and-storage-classes)).
- A new database on a cluster without Longhorn gets the cluster default class.
- A new database created while Longhorn is still installing waits instead of settling for local storage. Its Ready condition reads "Storage is starting". Geass waits 15 minutes, measured from when the cluster's add-ons stopped being ready, not from when the database was created. After that the message becomes "Storage is unavailable". That counts as a failure, so the status soon turns to "Provisioning failed after repeated errors" and Geass stops retrying until you select **Retry**.
- A new database that needs a regional or tagged class that does not exist yet waits while Longhorn is serving the global class. This wait has no limit: the message stays "Storage is starting" until the cluster controller makes the class, which it does once every node in the region or tag scope has a ready Longhorn manager. If no node in that scope exists, or one of them cannot run a manager, the class is never made.
- A database that already has a volume is pinned to that volume's class. For MySQL, Geass cannot read the class back from the Helm chart, so an existing MySQL database is treated as being on the default class.

The cluster object storage server follows the same rule. Its class is decided once, when its volume is first created, and recorded in the annotation `geass.dev/storage-class` on its `GeassObjectStore`. A server created while Longhorn is still installing reports "Storage is starting" and waits, and after 15 minutes of Longhorn not being ready it reports "Storage is unavailable", which counts as a failure like a database's. See [The object storage server's volume](#the-object-storage-servers-volume).

Read the pinned class:

```bash
kubectl get geassdatabase orders -n geass-system \
  -o jsonpath='{.metadata.annotations.geass\.dev/storage-class}{"\n"}'
```

:::note
Databases that were created on local storage stay there. Geass does not migrate a volume from one class to another. To move a Postgres, MySQL or SQLite database to replicated storage, restore a backup into a new database (see [Backups and restore](/docs/guides/backups-and-restore/)). A restore creates a new database, which takes whatever class new databases get today. MySQL and SQLite restores lose changes made after the last daily backup.
:::

## Compliance tags and storage classes

A project can require compliance node tags, such as `hipaa`. See [Compliance node tags](/docs/guides/compliance-tags/). Marking a tag as a compliance tag on a node taints that node with the value `compliance`. Two things then happen to storage.

**A class for the project.** For each project that requires tags, Geass makes a replicated class named `geass-tagged-` followed by 10 hexadecimal characters. They are a hash of the project's region and its sorted tags, so projects with the same region and tags share one class. The class keeps every copy of a volume on nodes in that region that carry all the tags, and the number of copies follows the number of those nodes: 1, 2 or 3. A new PostgreSQL (not HA), MySQL or SQLite database of the project takes this class instead of `geass-replicated`. A high-availability PostgreSQL database still takes `geass-single`: its one copy stays on the node where the instance runs, and that node carries the tags because the instance is placed there. Geass makes the class once every schedulable node in its scope has a ready Longhorn manager.

**A guard for everyone else.** Longhorn can include nodes by tag but cannot leave them out. So once any node is a compliance node, Geass gives the Longhorn node of every other node the tag `geass-general`. When all of them carry it, `geass-replicated` and the regional classes name that tag in their node selector, and their copy count follows the number of other nodes only. Another project's volumes then keep their copies off compliance nodes. A volume that gains a copy later takes the same selector. Copies that were already on a node before it became a compliance node are not moved.

Without Longhorn there are no classes. A volume stays on the node its pod runs on, and the pods go only to nodes that carry the tags.

A shared database server has no project, so a project's tags do not choose its class. It takes `geass-replicated` (or its regional class), which keeps its copies off compliance nodes even when its pods run on one. See [Shared database servers](/docs/guides/shared-database-servers/#projects-that-require-node-tags). The object storage server has no project either, but it is placed with its own region and tags, and its volume follows them: see [The object storage server's volume](#the-object-storage-servers-volume).

Look at the classes and the tags Geass put on the Longhorn nodes:

```bash
kubectl get storageclass
kubectl get storageclass geass-replicated -o jsonpath='{.parameters.nodeSelector}{"\n"}'
kubectl -n longhorn-system get nodes.longhorn.io \
  -o custom-columns='NODE:.metadata.name,TAGS:.spec.tags'
```

The second command prints `geass-general` once the guard is on and nothing before that.

## The object storage server's volume

The cluster's object storage server (SeaweedFS or Garage) keeps every in-cluster bucket on one volume. It is made when the server is created, and Geass picks its class once and keeps it, as for a database.

- With Longhorn, the volume is on the replicated class, so its data is kept on up to three nodes, like other volumes. A server placed in a region keeps its copies in that region (`geass-replicated-<region>`). A server placed on nodes with tags (the `runOnTags` it was set up with, such as a compliance tag) keeps its copies on nodes that carry every one of them (`geass-tagged-<hash>`), so its data is never replicated onto other nodes. The cluster controller makes that class itself, once every node it covers has a ready Longhorn manager.
- Without Longhorn, the volume uses the cluster's default class and lives on one node.
- The size is 10 GiB unless you chose another one at setup (at least 1 GiB). You can make it bigger later, never smaller, and only on a class that can be resized, which the replicated classes are and K3s's `local-path` is not. The dashboard says "This cluster's storage can't be resized." where it cannot.

The server's volume is raised with the others as nodes join, because it is in one of the replicated classes. See [Object storage](/docs/guides/object-storage/#where-the-servers-data-is-kept) for the steps, and [Make the volume bigger](/docs/guides/object-storage/#make-the-volume-bigger).

## Enable Longhorn

The installer turns Longhorn on for the cluster it builds. For a cluster where it is off, set `spec.addons.longhorn.enabled` on the `GeassCluster`. The installer always names that object `default`.

### Requirements

- **The iSCSI initiator on every node.** Longhorn attaches volumes over iSCSI, so `iscsiadm` must be installed and `iscsid` running on each node, including nodes that joined before you enabled Longhorn. The installer and Geass's SSH provisioning install it on the machines they set up (apt, dnf, yum or zypper) and treat a failure as non-fatal. For any other node, run the commands below.
- **Free disk on each node.** Each copy of a volume occupies space on a node's disk. Geass does not set a Longhorn data path, so the chart default applies. A cluster with three nodes holds up to three copies of every volume, so plan for the sum of your volume sizes on each node.
- **Kubernetes 1.25 or later.** A `kind` cluster cannot run Longhorn, because its nodes have no iSCSI.

To install the initiator by hand on a Debian or Ubuntu node:

```bash
sudo apt-get update -qq
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y open-iscsi
sudo systemctl enable --now iscsid
```

On Red Hat family systems install `iscsi-initiator-utils` with `dnf` or `yum`. On SUSE install `open-iscsi` with `zypper`. Then run `sudo systemctl enable --now iscsid`.

### Turn it on

```bash
kubectl patch geasscluster default -n geass-system --type merge \
  -p '{"spec":{"addons":{"longhorn":{"enabled":true}}}}'
```

Geass installs the chart into the `longhorn-system` namespace. When the CSI driver `driver.longhorn.io` is registered and every node has a ready Longhorn manager pod, it creates the storage classes. Check progress:

```bash
kubectl get pods -n longhorn-system
kubectl get storageclass
```

You should see `geass-replicated` and `geass-single` in the class list. Databases you create from then on use them.

:::caution
Setting `enabled` back to `false` does not remove Longhorn. It holds user data, so Geass never uninstalls it automatically. Removing it is a manual operation, and it deletes the volumes that live on it. Restore or copy your data elsewhere first.
:::

## Resize a database volume

You can make a deployed database's disk larger. You cannot make it smaller.

1. Open the database and go to **Settings**, then **Database**.
2. In **Storage**, choose a larger **Size**. The list offers 1, 5, 10, 20, 50, 100, 200 and 500 GB (GiB values), plus the current size if it is none of those.
3. Select **Save changes**.
4. Select **Deploy changes** in the project's top bar.

The size is per instance. A high-availability PostgreSQL database has three volumes, and each one grows to the new size. Through the REST API you can send any quantity of 1Gi or more as `storageSize`, for example `20Gi`. See [Databases](/docs/guides/databases/#size-storage-and-resizing).

The field is locked, and the dashboard shows "This database's storage can't be resized, so its size is fixed", when the database's pinned class does not allow expansion, for example a database on local storage. Resizing is judged per database, so one cluster can hold resizable and fixed databases at the same time. Other messages you may see:

| Message | Meaning |
| --- | --- |
| storage can be increased but not decreased once deployed (currently X) | You asked for a smaller size than the last deploy |
| this cluster's storage can't be resized, so a deployed database stays at X | The database's class does not allow expansion |
| storage size must be at least 1Gi | Below the minimum |
| storage size must be a quantity such as 10Gi | The value could not be read |
| Redis keeps its data in memory, so there is no storage to size | Redis has no volume |
| storage size only applies to databases that run in the cluster | The database is external |

The size floor is the size from the last deploy, not the size currently in the form, because a larger value you saved but have not deployed is not applied yet.

Postgres volumes are resized by the Postgres operator, and SQLite's and MySQL's by their volume claims: when you deploy a larger size, Geass raises the claim's request itself. A claim whose storage class cannot expand is refused by Kubernetes, and Geass then logs it and leaves the database running at the size it has. After you deploy a MySQL resize, confirm it with `kubectl get pvc -n <namespace>`. The claim of a MySQL database made on an earlier development build, which the Bitnami chart owns, is not resized by Geass.

## What replication gives you

With two or three copies on different nodes, losing a node or its disk does not lose the volume, and a database pod that moves to another node can attach the volume from a surviving copy. When the node returns, or a new node joins, Longhorn rebuilds the missing copy.

Replication does not protect you from deleting data, a bad migration, a bug that corrupts rows, or deleting the database. Every copy sees the same change. Use [backups](/docs/guides/backups-and-restore/) for that.

High-availability PostgreSQL and MySQL do not rely on volume copies. They run three or more database instances that replicate to each other, each on a `geass-single` volume with one copy kept on its own node. See [Databases](/docs/guides/databases/#instances-and-high-availability).

## Existing volumes follow the node count

A storage class only applies to volumes created after it. Without further work, a cluster that grows from one node to three would keep every older volume as a single copy. Geass therefore raises existing volumes.

- It raises the replica count of volumes in `geass-replicated`, in regional `geass-replicated-<region>` classes and in `geass-tagged-<hash>` classes up to the current target. For a regional class the target follows that region's own nodes, and for a tagged class the nodes that carry the project's tags. It never lowers a count, so a node that leaves cannot quietly drop copies.
- It leaves `geass-single` volumes alone, because they are one copy on purpose. It also leaves volumes on local storage and on classes that are not Geass's.
- When the guard for compliance nodes is on, a volume of `geass-replicated` or a regional class that is raised also takes the node selector of its class (with `geass-general`), so the copy it gains is not placed on a compliance node. If Longhorn refuses the selector, the count is still raised without it.
- It waits until every node that counts has a ready Longhorn manager. With the guard on, the compliance nodes do not count for the general classes. Raising earlier asks for copies that have nowhere to go.
- It raises at most five volumes per pass and runs again 30 seconds later while volumes are waiting. A volume Longhorn refuses is retried every five minutes. A pass stops early after five refusals in a row, and one pass is limited to 30 seconds.
- It reads each volume's class from its `PersistentVolume`, because Longhorn does not record the class on its own volume object.
- When the node count changes, Geass deletes and recreates the storage class with the new copy count, because class parameters cannot be edited. Existing volumes carry their own parameters and are not affected by that.

Longhorn builds a new copy by copying the whole volume to another node. After you add a node, expect extra disk and network load until the copies finish. Geass requests the rebuilds but does not wait for them or check that each new copy found room.

## Nodes that must not hold data

The nodes of a [node pool](/docs/guides/node-pools/) come and go, so by default a pool keeps data off them (`storage: None`). A server you add one at a time can be set the same way (**Data on this server**: `None`). Such a node carries the label `geass.dev/storage=none` and the taint `geass.dev/ephemeral=true:NoSchedule`, and the dashboard calls it **Ephemeral**. Three things keep volumes off it:

- **Nothing that keeps data tolerates the taint.** Databases and the object storage server stay off these nodes. Only services tolerate it, and a service has no volume.
- **They are not counted for the number of copies.** The copy count follows the schedulable nodes, and a node with this taint is not one of them.
- **Longhorn is told not to put a replica there.** In a plain cluster the taint alone keeps Longhorn's manager off the node. Once any node is reserved for a tag, Longhorn is told to tolerate every taint, and would then use the node's disk, so Geass also switches off scheduling for that node in Longhorn (`spec.allowScheduling: false`, with the annotation `geass.dev/scheduling-disabled` to remember that Geass did it). It switches scheduling back on only for a node it switched off, and only when the node stops being ephemeral, never for one an administrator disabled.

A pool with `storage: Allow` has ordinary workers that may hold volumes. The autoscaler never removes such a node while it holds a replica of a volume, or runs a pod that uses a volume claim, so a scale-down cannot leave a volume one copy short.

## Limits

- The class is chosen once per database and cannot be changed. There is no migration between classes.
- Volumes only grow. Geass does not shrink a volume.
- The cluster object storage volume can be made bigger like a database's, never smaller, and only where its class can be resized. See [Object storage](/docs/guides/object-storage/#make-the-volume-bigger).
- Geass does not set a Longhorn data path, disk reservation or over-provisioning rule. Longhorn's own defaults apply.
- Three copies is the maximum, however many nodes you have.
- A compliance tag keeps other projects' new copies off compliance nodes. It does not move copies that are already there, and it does not cover a shared database server's pods and volume the way it covers a project's.
- The Longhorn web UI is disabled.
- Switching the add-on off does not uninstall it.

## What happens under the hood

- The cluster controller installs a `HelmChart` named `geass-longhorn` in `kube-system`. K3s's Helm controller deploys it into `longhorn-system`.
- Once the CSI driver exists and a manager runs on every node, it creates the classes with the parameters `numberOfReplicas`, `staleReplicaTimeout` (30), `fromBackup` (empty), `fsType` (ext4) and `dataLocality` (`disabled` for `geass-replicated`, `strict-local` for `geass-single`). Both classes allow volume expansion and have a `Delete` reclaim policy.
- Existing volumes are patched on their Longhorn `Volume` object, field `spec.numberOfReplicas`.
- When a node is reserved with a tag, Geass adds a toleration to Longhorn's chart values so storage can run on that node. It does not take it out again, so a reservation that comes and goes does not restart Longhorn each time.
- Regional classes work through Longhorn node tags named `geass-region-<region>`, which Geass mirrors from each node's region label. Tagged classes use `geass-tag-<tag>`, mirrored from each node's Geass tags, and the guard for compliance nodes uses `geass-general`. A tag that is not Geass's is left as it is.
Inspect the state:

```bash
kubectl get storageclass
kubectl get pv -o custom-columns=NAME:.metadata.name,CLASS:.spec.storageClassName,CLAIM:.spec.claimRef.name
kubectl get volumes.longhorn.io -n longhorn-system \
  -o custom-columns=NAME:.metadata.name,REPLICAS:.spec.numberOfReplicas,ROBUSTNESS:.status.robustness
```

## Troubleshooting

**A new database, or the object storage server, stays at "Storage is starting".**
Longhorn is still installing or a node is not ready for it. Run `kubectl get pods -n longhorn-system` and look for pods that are not running. The most common cause is a node without the iSCSI initiator. Install it (see [Requirements](#requirements)). After 15 minutes the database or server changes to "Storage is unavailable".

**A database in a region or in a project that requires node tags, or an object storage server placed in a region or on tags, stays at "Storage is starting" and never changes.**
Longhorn is running, but the class for the region or the tags was not made. Run `kubectl get storageclass` and look for `geass-replicated-<region>` or a `geass-tagged-...` class. Geass makes it only when at least one schedulable node is in the scope (the region, and every required tag) and every such node has a ready Longhorn manager. Check the nodes with `kubectl get nodes -L topology.kubernetes.io/region` (add a `-L tag.geass.dev/<tag>` for each tag) and `kubectl get pods -n longhorn-system -o wide`. This wait does not turn into "Storage is unavailable".

**Longhorn manager pods crash on a node.**
Read the log with `kubectl logs -n longhorn-system <manager-pod>`. A missing `iscsiadm` or a stopped `iscsid` is reported there. Install the initiator and start the service.

**The database's Size field is locked.**
The database is on a class that cannot expand, usually local storage from before Longhorn was installed. Restore a backup into a new database to move it, as described in the note above.

**Volumes stay at one copy after you added nodes.**
Geass waits for a ready Longhorn manager on every node. A new node without the iSCSI initiator never gets one. Check `kubectl get pods -n longhorn-system -o wide` for a node without a running manager. Only volumes in `geass-replicated` and the regional classes are raised, so databases on `geass-single` or local storage stay as they are.

**A volume is degraded after you added a node.**
Longhorn is copying the volume to the new node. Check `status.robustness` with the command above and wait for it to return to `healthy`. Rebuilds are limited by Longhorn's own per-node concurrency.

**The cluster ran out of disk.**
Each node holds a copy of every replicated volume. Free space on the node, add a node with more disk, or delete databases you no longer need.

## Related

- [Databases](/docs/guides/databases/)
- [Backups and restore](/docs/guides/backups-and-restore/)
- [Object storage](/docs/guides/object-storage/)
- [Scaling and placement](/docs/guides/scaling-and-placement/)
- [Node pools and autoscaling](/docs/guides/node-pools/)
- [Compliance node tags](/docs/guides/compliance-tags/)
- [Requirements](/docs/getting-started/requirements/)
- [High availability](/docs/operations/high-availability/)
