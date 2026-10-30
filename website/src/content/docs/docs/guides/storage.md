---
title: "Storage and volumes"
description: "Where Geass keeps database and object storage data: local disks, or the Longhorn add-on for replicated, resizable volumes, and the limits of each."
---

Databases, and the object storage server of the cluster, keep their data on persistent volumes. This page explains these topics:

- Which resources get a volume.
- The two kinds of storage that Geass can put a volume on.
- How to turn on replicated and resizable storage.
- What you can and cannot change after a volume exists.

Read this page before you create databases that you care about, because Geass makes the storage choice one time only.

## What uses a volume

| Resource | Volume | Size | Storage class |
| --- | --- | --- | --- |
| PostgreSQL, MySQL, and SQLite databases | One per instance | 10 GiB by default (1 GiB for SQLite), or the size that you select | Decided one time for each database. See [The class is pinned per database](#the-class-is-pinned-per-database) |
| Redis | None. Persistence is off, so Redis is a cache | Not applicable | Not applicable |
| Cluster object storage server (SeaweedFS or Garage) | One | 10 GiB by default, or the size that you select when you set it up | Decided one time, like the class of a database. See [The object storage server's volume](#the-object-storage-servers-volume) |
| Monitoring and log add-ons | One each | 5 GiB (Prometheus), 6 GiB (log store) | The cluster default class |
| Apps and functions | None | Not applicable | Not applicable |

The dashboard labels sizes such as 10 GiB as "10 GB" in the size list. The values are binary gibibytes (`10Gi`).

## Two kinds of storage

| | Local storage | Longhorn add-on |
| --- | --- | --- |
| Where the data is | One copy on the disk of one node | One, two, or three copies on different nodes |
| If the node is lost | The data is unavailable until the node returns. If the disk of the node is gone, the data is gone | When at least two copies exist, the volume stays available from another copy, and the workload that used it starts on another node. See [When a node is lost](/docs/operations/high-availability/#when-a-node-is-lost) |
| Resize a deployed database | No | Yes, larger only |
| Storage class | The cluster default (`local-path` on K3s) | `geass-replicated` and `geass-single`, created by Geass |
| When it is used | Longhorn is not installed, or the database was created before Longhorn was installed | Longhorn is installed when the volume of the database is first created |

Geass installs the Longhorn add-on on clusters that it builds. A cluster that does not run Longhorn, such as a local `kind` cluster, uses local storage. You can use Geass on local storage. But you cannot resize those databases, and a node failure takes the data with it.

## The Geass storage classes

When Longhorn is on, Geass creates two storage classes. You never select between them. Geass picks one for each database, and for the object storage server. Regions and compliance tags add more classes, as the table shows.

| Class | Copies | Used by | Binding |
| --- | --- | --- | --- |
| `geass-replicated` | 1 on a one-node cluster, 2 on two nodes, and 3 on three or more | PostgreSQL (not HA), MySQL, SQLite, and the cluster object storage server | Immediate |
| `geass-single` | 1, kept on the node where the pod runs | High-availability PostgreSQL, MySQL, and MariaDB, whose instances replicate the data themselves | When the pod is scheduled |
| `geass-replicated-<region>` | As above, counted over the nodes of one region | The same databases, in a project in that region, and an object storage server placed in that region | Immediate |
| `geass-tagged-<hash>` | As above, counted over the nodes that carry the required tags of a project | The same databases, in a project that requires node tags, and an object storage server placed on nodes with tags | Immediate |

The copy count follows the number of schedulable nodes. A schedulable node exists, is not marked unschedulable, and carries no `NoSchedule` or `NoExecute` taint. A node reserved for a tag (see [Scaling and placement](/docs/guides/scaling-and-placement/)) still counts, and Longhorn can run on it. Geass counts nodes that exist, not nodes that are Ready. So a node that is briefly unreachable does not change the count. The exception is a compliance node, described under [Compliance tags and storage classes](#compliance-tags-and-storage-classes).

If your nodes carry a region, Geass also creates one class for each region, named `geass-replicated-<region>`. That class keeps the copies of a region on the nodes of that region, so the data of a project does not leave its region. A database in a region uses the class of its region. `geass-single` stays global, because its one copy sits on the node that runs the pod.

Geass installs Longhorn from chart version 1.12.1, the newest line that runs on Kubernetes 1.25 and later. Geass turns off the storage class of the chart. Geass also sets the web UI of Longhorn to zero replicas, so there is nothing to log in to.

## The class is pinned per database

The class of a volume cannot change after the volume exists. So Geass decides the class of a database one time, when the database first needs a volume. Geass records the decision in the annotation `geass.dev/storage-class` on the `GeassDatabase`. An empty value means the cluster default.

- When Longhorn is installed, a new database gets `geass-replicated`. A high-availability PostgreSQL, MySQL, or MariaDB database gets `geass-single`. In a project with a region, the database gets the class of the region. In a project that requires node tags, the database gets `geass-tagged-<hash>` (see [Compliance tags and storage classes](#compliance-tags-and-storage-classes)).
- A new database on a cluster without Longhorn gets the cluster default class.
- A new database created while Longhorn is still installing waits. It does not settle for local storage. Its Ready condition reads "Storage is starting". Geass waits 15 minutes, measured from when the add-ons of the cluster stopped being ready, not from when the database was created. After that, the message becomes "Storage is unavailable". That counts as a failure, so the status soon turns to "Provisioning failed after repeated errors". Geass then stops retrying until you click **Retry**.
- A new database that needs a regional or tagged class that does not exist yet waits while Longhorn serves the global class. This wait has no limit. The message stays "Storage is starting" until the cluster controller makes the class. The cluster controller makes the class when every node in the region or tag scope has a ready Longhorn manager. If no node in that scope exists, or one of the nodes cannot run a manager, the class is never made.
- A database that already has a volume is pinned to the class of that volume. For MySQL, Geass cannot read the class back from the Helm chart. So Geass treats an existing MySQL database as being on the default class.

The cluster object storage server follows the same rule. Geass decides its class one time, when its volume is first created. Geass records the class in the annotation `geass.dev/storage-class` on its `GeassObjectStore`. A server created while Longhorn is still installing reports "Storage is starting" and waits. After Longhorn has not been ready for 15 minutes, the server reports "Storage is unavailable". That message counts as a failure, as it does for a database. See [The object storage server's volume](#the-object-storage-servers-volume).

Read the pinned class:

```bash
kubectl get geassdatabase orders -n geass-system \
  -o jsonpath='{.metadata.annotations.geass\.dev/storage-class}{"\n"}'
```

:::note
Databases that were created on local storage stay there. Geass does not migrate a volume from one class to another. To move a Postgres, MySQL, or SQLite database to replicated storage, restore a backup into a new database (see [Backups and restore](/docs/guides/backups-and-restore/)). A restore creates a new database, which takes the class that new databases get today. MySQL and SQLite restores lose the changes made after the last daily backup.
:::

## Compliance tags and storage classes

A project can require compliance node tags, such as `hipaa`. See [Compliance node tags](/docs/guides/compliance-tags/). When you mark a tag as a compliance tag on a node, Geass taints that node with the value `compliance`. Then two things happen to storage.

**A class for the project.** For each project that requires tags, Geass makes a replicated class named `geass-tagged-` followed by 10 hexadecimal characters. These characters are a hash of the region of the project and its sorted tags. So projects with the same region and tags share one class. The class keeps every copy of a volume on nodes in that region that carry all the tags. The number of copies follows the number of those nodes: 1, 2, or 3. A new PostgreSQL (not HA), MySQL, or SQLite database of the project takes this class instead of `geass-replicated`. A high-availability PostgreSQL database still takes `geass-single`. Its one copy stays on the node where the instance runs, and that node carries the tags because the instance is placed there. Geass makes the class when every schedulable node in its scope has a ready Longhorn manager.

**A guard for everyone else.** Longhorn can include nodes by tag, but it cannot leave them out. So when any node is a compliance node, Geass gives the tag `geass-general` to the Longhorn node of every other node. When all of those nodes carry the tag, `geass-replicated` and the regional classes name the tag in their node selector. Their copy count then follows the number of other nodes only. The volumes of other projects then keep their copies off compliance nodes. A volume that gains a copy later takes the same selector. Geass does not move copies that were already on a node before it became a compliance node.

Without Longhorn, there are no classes. A volume stays on the node that its pod runs on, and the pods go only to nodes that carry the tags.

A shared database server has no project, so the tags of a project do not select its class. The server takes `geass-replicated` (or its regional class). That class keeps its copies off compliance nodes, even when its pods run on a compliance node. See [Shared database servers](/docs/guides/shared-database-servers/#projects-that-require-node-tags). The object storage server has no project either. But Geass places it with its own region and tags, and its volume follows them. See [The object storage server's volume](#the-object-storage-servers-volume).

To see the classes and the tags that Geass put on the Longhorn nodes, run these commands:

```bash
kubectl get storageclass
kubectl get storageclass geass-replicated -o jsonpath='{.parameters.nodeSelector}{"\n"}'
kubectl -n longhorn-system get nodes.longhorn.io \
  -o custom-columns='NODE:.metadata.name,TAGS:.spec.tags'
```

The second command prints `geass-general` after the guard is on, and nothing before that.

## The object storage server's volume

The object storage server of the cluster (SeaweedFS or Garage) keeps every in-cluster bucket on one volume. Geass makes the volume when the server is created. As for a database, Geass picks its class one time and keeps it.

- With Longhorn, the volume is on the replicated class. So, like other volumes, its data is kept on up to three nodes. A server placed in a region keeps its copies in that region (`geass-replicated-<region>`).
- With Longhorn, a server placed on nodes with tags keeps its copies on nodes that carry every one of those tags (`geass-tagged-<hash>`). These tags are the `runOnTags` that the server was set up with, such as a compliance tag. So its data is never replicated onto other nodes. The cluster controller makes that class itself, when every node that the class covers has a ready Longhorn manager.
- Without Longhorn, the volume uses the default class of the cluster and lives on one node.
- The size is 10 GiB, unless you selected another size at setup (at least 1 GiB). You can make the volume bigger later, but never smaller. You can do this only on a class that can be resized. The replicated classes can be resized, and the K3s `local-path` class cannot. Where the class cannot be resized, the dashboard says "This cluster's storage can't be resized."

When nodes join, Geass raises the copies of the volume of the server with the other volumes, because the volume is in one of the replicated classes. For the steps, see [Object storage](/docs/guides/object-storage/#where-the-servers-data-is-kept) and [Make the volume bigger](/docs/guides/object-storage/#make-the-volume-bigger).

## Enable Longhorn

The installer turns on Longhorn for the cluster that it builds. For a cluster where Longhorn is off, set `spec.addons.longhorn.enabled` on the `GeassCluster`. The installer always names that object `default`.

### Requirements

- **The iSCSI initiator on every node.** Longhorn attaches volumes over iSCSI. So `iscsiadm` must be installed and `iscsid` must run on each node, including nodes that joined before you turned on Longhorn. The installer and the SSH provisioning of Geass install the initiator on the machines that they set up (apt, dnf, yum, or zypper). They treat a failure as non-fatal. For any other node, run the commands below.
- **Free disk on each node.** Each copy of a volume occupies space on the disk of a node. Geass does not set a Longhorn data path, so the chart default applies. A cluster with three nodes holds up to three copies of every volume. So plan for the sum of your volume sizes on each node.
- **Kubernetes 1.25 or later.** A `kind` cluster cannot run Longhorn, because its nodes have no iSCSI.

To install the initiator by hand on a Debian or Ubuntu node:

```bash
sudo apt-get update -qq
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y open-iscsi
sudo systemctl enable --now iscsid
```

On Red Hat family systems, install `iscsi-initiator-utils` with `dnf` or `yum`. On SUSE, install `open-iscsi` with `zypper`. Then run `sudo systemctl enable --now iscsid`.

### Turn it on

```bash
kubectl patch geasscluster default -n geass-system --type merge \
  -p '{"spec":{"addons":{"longhorn":{"enabled":true}}}}'
```

Geass installs the chart into the `longhorn-system` namespace. Geass creates the storage classes when both of these are true:

- The CSI driver `driver.longhorn.io` is registered.
- Every node has a ready Longhorn manager pod.

To check the progress, run these commands:

```bash
kubectl get pods -n longhorn-system
kubectl get storageclass
```

The class list shows `geass-replicated` and `geass-single`. Databases that you create from then on use these classes.

:::caution
Before you remove Longhorn, restore or copy your data to a different location. Removal is a manual operation, and it deletes the volumes that live on Longhorn. If you set `enabled` back to `false`, Longhorn is not removed. Longhorn holds user data, so Geass never uninstalls it automatically.
:::

## Resize a database volume

You can make the disk of a deployed database larger. You cannot make it smaller.

1. Open the database.
2. Select **Settings**, then **Database**.
3. In **Storage**, select a larger **Size**. The list offers 1, 5, 10, 20, 50, 100, 200, and 500 GB (GiB values). It also offers the current size if the current size is none of those.
4. Click **Save changes**.
5. In the top bar of the project, click **Deploy changes**.

The size is for each instance. A high-availability PostgreSQL database has three volumes, and each one grows to the new size. Through the REST API, you can send any quantity of 1Gi or more as `storageSize`, for example `20Gi`. See [Databases](/docs/guides/databases/#size-storage-and-resizing).

The field is locked when the pinned class of the database does not allow expansion, for example for a database on local storage. The dashboard then shows "This storage can't be resized". Geass decides resizing for each database, so one cluster can hold resizable and fixed databases at the same time. You might also see these messages:

| Message | Meaning |
| --- | --- |
| storage can be increased but not decreased once deployed (currently X) | You asked for a smaller size than the last deploy |
| this cluster's storage can't be resized, so a deployed database stays at X | The database's class does not allow expansion |
| storage size must be at least 1Gi | Below the minimum |
| storage size must be a quantity such as 10Gi | The value could not be read |
| Redis keeps its data in memory, so there is no storage to size | Redis has no volume |
| storage size only applies to databases that run in the cluster | The database is external |

The size floor is the size from the last deploy, not the size that is currently in the form. A larger value that you saved but did not deploy is not applied yet.

The Postgres operator resizes Postgres volumes. SQLite and MySQL volumes are resized through their volume claims. When you deploy a larger size, Geass raises the request of the claim itself. Kubernetes refuses a claim whose storage class cannot expand. Geass then logs the refusal and leaves the database running at the size that it has. After you deploy a MySQL resize, confirm it with `kubectl get pvc -n <namespace>`. Geass does not resize the claim of a MySQL database made on an earlier development build, which the Bitnami chart owns.

## What replication gives you

With two or three copies on different nodes, the loss of a node or its disk does not lose the volume. A database pod that moves to another node can attach the volume from a surviving copy. When the node returns, or a new node joins, Longhorn rebuilds the missing copy.

Replication does not protect you from these events:

- Data that you delete.
- A bad migration.
- A bug that corrupts rows.
- The deletion of the database.

Every copy sees the same change. For these events, use [backups](/docs/guides/backups-and-restore/).

High-availability PostgreSQL, MySQL, and MariaDB do not rely on volume copies. They run three or more database instances that replicate to each other. Each instance is on a `geass-single` volume with one copy kept on its own node. See [Databases](/docs/guides/databases/#instances-and-high-availability).

## Existing volumes follow the node count

A storage class applies only to volumes created after the class. Without more work, a cluster that grows from one node to three keeps every older volume as a single copy. So Geass raises existing volumes:

- Geass raises the replica count of volumes in these classes up to the current target: `geass-replicated`, the regional `geass-replicated-<region>` classes, and the `geass-tagged-<hash>` classes. For a regional class, the target follows the nodes of that region. For a tagged class, the target follows the nodes that carry the tags of the project. Geass never lowers a count, so a node that leaves cannot quietly drop copies.
- Geass leaves `geass-single` volumes alone, because they are one copy on purpose. It also leaves volumes on local storage, and volumes on classes that are not Geass classes.
- When the guard for compliance nodes is on, Geass raises a volume of `geass-replicated` or a regional class with the node selector of its class (with `geass-general`). So the copy that the volume gains is not placed on a compliance node. If Longhorn refuses the selector, Geass still raises the count without the selector.
- Geass waits until every node that counts has a ready Longhorn manager. With the guard on, the compliance nodes do not count for the general classes. If Geass raised the count earlier, it would ask for copies that have nowhere to go.
- Geass raises at most five volumes in each pass. While volumes wait, Geass runs again 30 seconds later. Geass retries a volume that Longhorn refuses every five minutes. A pass stops early after five refusals in a row, and one pass is limited to 30 seconds.
- Geass reads the class of each volume from its `PersistentVolume`, because Longhorn does not record the class on its own volume object.
- When the node count changes, Geass deletes the storage class and creates it again with the new copy count, because class parameters cannot be edited. Existing volumes carry their own parameters, and that change does not affect them.

Longhorn builds a new copy by copying the whole volume to another node. After you add a node, expect extra disk and network load until the copies finish. Geass requests the rebuilds. It does not wait for them, and it does not check that each new copy found room.

## Nodes that must not hold data

The nodes of a [node pool](/docs/guides/node-pools/) come and go, so by default a pool keeps data off them (`storage: None`). You can set a server that you add one at a time in the same way (**Data on this server**: `None`). Such a node carries the label `geass.dev/storage=none` and the taint `geass.dev/ephemeral=true:NoSchedule`. The dashboard calls it **Ephemeral**. Three things keep volumes off the node:

- **Nothing that keeps data tolerates the taint.** Databases and the object storage server stay off these nodes. Only services tolerate the taint, and a service has no volume.
- **They are not counted for the number of copies.** The copy count follows the schedulable nodes, and a node with this taint is not one of them.
- **Longhorn is told not to put a replica there.** In a plain cluster, the taint alone keeps the Longhorn manager off the node. But when any node is reserved for a tag, Geass tells Longhorn to tolerate every taint. Longhorn would then use the disk of the node. So Geass also turns off scheduling for that node in Longhorn (`spec.allowScheduling: false`). Geass adds the annotation `geass.dev/scheduling-disabled` to remember that Geass did it. Geass turns scheduling back on only for a node that Geass turned off, and only when the node stops being ephemeral. Geass never turns it back on for a node that an administrator disabled.

A pool with `storage: Allow` has ordinary workers that might hold volumes. The autoscaler never removes such a node while the node holds a replica of a volume, or runs a pod that uses a volume claim. So a scale-down cannot leave a volume one copy short.

## Limits

- Geass selects the class one time for each database, and you cannot change it. There is no migration between classes.
- Volumes only grow. Geass does not shrink a volume.
- You can make the cluster object storage volume bigger, like the volume of a database, but never smaller. You can do this only where its class can be resized. See [Object storage](/docs/guides/object-storage/#make-the-volume-bigger).
- Geass does not set a Longhorn data path, disk reservation, or over-provisioning rule. The Longhorn defaults apply.
- Three copies is the maximum, however many nodes you have.
- A compliance tag keeps the new copies of other projects off compliance nodes. It does not move copies that are already there. It does not cover the pods and volume of a shared database server the way it covers those of a project.
- The Longhorn web UI is disabled.
- If you turn off the add-on, Geass does not uninstall it.

## What happens under the hood

- The cluster controller installs a `HelmChart` named `geass-longhorn` in `kube-system`. The Helm controller of K3s deploys it into `longhorn-system`.
- After the CSI driver exists and a manager runs on every node, the cluster controller creates the classes with these parameters:
  - `numberOfReplicas`.
  - `staleReplicaTimeout` (30).
  - `fromBackup` (empty).
  - `fsType` (ext4).
  - `dataLocality`: `disabled` for `geass-replicated`, and `strict-local` for `geass-single`.
- Both classes allow volume expansion and have a `Delete` reclaim policy.
- Geass patches existing volumes on their Longhorn `Volume` object, in the field `spec.numberOfReplicas`.
- When a node is reserved with a tag, Geass adds a toleration to the chart values of Longhorn, so that storage can run on that node. Geass does not remove the toleration again. So a reservation that comes and goes does not restart Longhorn each time.
- Regional classes work through Longhorn node tags named `geass-region-<region>`. Geass mirrors these tags from the region label of each node.
- Tagged classes use `geass-tag-<tag>`, mirrored from the Geass tags of each node.
- The guard for compliance nodes uses `geass-general`.
- Geass leaves a tag that is not a Geass tag as it is.

To inspect the state, run these commands:

```bash
kubectl get storageclass
kubectl get pv -o custom-columns=NAME:.metadata.name,CLASS:.spec.storageClassName,CLAIM:.spec.claimRef.name
kubectl get volumes.longhorn.io -n longhorn-system \
  -o custom-columns=NAME:.metadata.name,REPLICAS:.spec.numberOfReplicas,ROBUSTNESS:.status.robustness
```

## Troubleshooting

**A new database, or the object storage server, stays at "Storage is starting".**
Longhorn is still installing, or a node is not ready for Longhorn. Run `kubectl get pods -n longhorn-system` and look for pods that are not running. The most common cause is a node without the iSCSI initiator. Install the initiator (see [Requirements](#requirements)). After 15 minutes, the database or server changes to "Storage is unavailable".

**A database in a region or in a project that requires node tags, or an object storage server placed in a region or on tags, stays at "Storage is starting" and never changes.**
Longhorn is running, but the class for the region or the tags was not made. Run `kubectl get storageclass` and look for `geass-replicated-<region>` or a `geass-tagged-...` class. Geass makes the class only when both of these are true:

- At least one schedulable node is in the scope (the region, and every required tag).
- Every such node has a ready Longhorn manager.

To check the nodes, run `kubectl get nodes -L topology.kubernetes.io/region` and `kubectl get pods -n longhorn-system -o wide`. In the first command, add a `-L tag.geass.dev/<tag>` for each tag. This wait does not turn into "Storage is unavailable".

**Longhorn manager pods crash on a node.**
Read the log with `kubectl logs -n longhorn-system <manager-pod>`. The log reports a missing `iscsiadm` or a stopped `iscsid`. Install the initiator and start the service.

**The database's Size field is locked.**
The database is on a class that cannot expand. Usually, this class is local storage from before Longhorn was installed. To move the database, restore a backup into a new database, as the note above describes.

**Volumes stay at one copy after you added nodes.**
Geass waits for a ready Longhorn manager on every node. A new node without the iSCSI initiator never gets a manager. Check `kubectl get pods -n longhorn-system -o wide` for a node without a running manager. Geass raises only volumes in `geass-replicated`, the regional classes, and the `geass-tagged-<hash>` classes, so databases on `geass-single` or local storage stay as they are.

**A volume is degraded after you added a node.**
Longhorn is copying the volume to the new node. Check `status.robustness` with the command above, and wait for it to return to `healthy`. The concurrency limit of Longhorn for each node limits the rebuilds.

**The cluster ran out of disk.**
Each node holds a copy of every replicated volume. Free space on the node, add a node with more disk, or delete databases that you no longer need.

## Related

- [Databases](/docs/guides/databases/)
- [Backups and restore](/docs/guides/backups-and-restore/)
- [Object storage](/docs/guides/object-storage/)
- [Scaling and placement](/docs/guides/scaling-and-placement/)
- [Node pools and autoscaling](/docs/guides/node-pools/)
- [Compliance node tags](/docs/guides/compliance-tags/)
- [Requirements](/docs/getting-started/requirements/)
- [High availability](/docs/operations/high-availability/)
