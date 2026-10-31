---
title: "Storage and volumes"
description: "Where Geass keeps database and bucket data: one disk on one server, or replicated storage with copies on several servers, and what you can resize."
---

Databases and the object storage server keep their data on disks, also called volumes. This page explains these topics:

- Which resources get a disk.
- The two kinds of storage that a disk can be on.
- How to turn on replicated storage.
- What you can change after a disk exists.

Read this page before you create databases that you care about. Geass selects the kind of storage for a database one time only.

## What uses a volume

| Resource | Disk | Size |
| --- | --- | --- |
| PostgreSQL, MySQL, MariaDB, and SQLite databases | One for each instance | 10 GiB by default (1 GiB for SQLite), or the size that you select |
| Redis | None. Redis is a cache in memory | Not applicable |
| Object storage server (SeaweedFS or Garage) | One, shared by all in-cluster buckets | 10 GiB by default, or the size that you select at setup |
| Monitoring and log add-ons | One each | 5 GiB (metrics), 6 GiB (logs) |
| Services and functions | None | Not applicable |

The dashboard labels a size of 10 GiB as "10 GB". The sizes are gibibytes (`10Gi`).

## Two kinds of storage

| | Local storage | Replicated storage |
| --- | --- | --- |
| Where the data is | One copy on the disk of one server | One, two, or three copies on different servers |
| If the server is lost | The data is unavailable until the server returns. If the disk is gone, the data is gone | The data stays available from another copy, and the database starts on another server. See [When a node is lost](/docs/operations/high-availability/#when-a-node-is-lost) |
| Make a disk larger | No | Yes |
| When it is used | The cluster has no replicated storage, or the database is older than the replicated storage | The cluster had replicated storage when Geass first created the disk |

The installer turns on replicated storage for the clusters that it builds. A cluster without it, for example a local `kind` cluster, uses local storage. You can use Geass on local storage. But you cannot make those disks larger, and the loss of a server loses its data.

The number of copies follows the number of servers: one copy on a one-server cluster, two on two servers, and three on three or more. In a cluster with regions, the copies of a project stay in the region of the project.

## The storage choice is fixed for each database

Geass selects the kind of storage for a database when it first creates the disk. You cannot change it later.

- With replicated storage, a new database gets copies on several servers. A high-availability database gets one copy for each instance, on the server of that instance. Its instances copy the data to each other.
- Without replicated storage, a new database gets local storage.
- If replicated storage is still starting, a new database waits with "Storage is starting". It does not fall back to local storage. After 15 minutes, the message changes to "Storage is unavailable", and Geass soon stops with "Provisioning failed after repeated errors". After you fix the storage, click **Retry**.
- A database in a region, or in a project that requires node tags, waits until the storage for that region or those tags is ready. This wait has no time limit.

The object storage server follows the same rules. See [The object storage server's volume](#the-object-storage-servers-volume).

:::note
Databases created on local storage stay there. Geass does not move a disk to another kind of storage. To move a database to replicated storage, restore a backup into a new database. See [Backups and restore](/docs/guides/backups-and-restore/). A MySQL or SQLite restore loses the changes made after the last daily backup.
:::

## Compliance tags and where copies are kept

A project can require compliance node tags, for example `hipaa`. See [Compliance node tags](/docs/guides/compliance-tags/). With replicated storage, these rules apply:

- A new database of the project keeps every copy on servers in its region that carry all the required tags.
- The disks of other projects keep their copies off compliance servers. Geass does not move copies that were on a server before it became a compliance server.
- A shared database server has no project. Its copies stay off compliance servers, even when the server runs on them. See [Shared database servers](/docs/guides/shared-database-servers/#projects-that-require-node-tags).
- The object storage server keeps its copies on servers with the tags that you gave it at setup.

Without replicated storage, a disk stays on the one server where its database runs. Geass runs the database only on servers with the required tags.

## The object storage server's volume

The object storage server keeps every in-cluster bucket on one disk. Geass selects the kind of storage when it creates the server, and keeps it.

- With replicated storage, the data is on up to three servers. A server placed in a region keeps its copies in that region. A server placed on servers with tags keeps its copies on servers with those tags.
- Without replicated storage, the data is on one server.
- You can make the disk larger, but not smaller, and only on replicated storage. On local storage, the dashboard says "This cluster's storage can't be resized."

See [Where the server's data is kept](/docs/guides/object-storage/#where-the-servers-data-is-kept) and [Make the volume bigger](/docs/guides/object-storage/#make-the-volume-bigger).

## Turn on replicated storage

The installer turns on replicated storage for the cluster that it builds. If it is off, turn it on. You must be an admin.

1. Open **Settings** → **Add-ons**.
2. Under **Replicated storage**, click **Turn on**.
3. Make sure that every server has the iSCSI initiator. Then click **Turn on Replicated storage**.
4. Wait until the badge next to the title changes from **Installing** to **Ready**.

The message under the title says what Geass waits for, for example a server without the iSCSI initiator. Databases that you create after the badge says **Ready** use replicated storage.

To do the same from the command line, run `geass addons enable longhorn --wait`. If the badge says **Failed**, read the reason. If the reason names the install job, click **Retry install**, or run `geass addons retry longhorn`.

You cannot turn off replicated storage. Geass never removes it, because it holds your data.

### Requirements

- **The iSCSI initiator on every server.** The installer installs it, and so does Geass when it joins a server over SSH. A server that joined in another way needs it too. See [Install the iSCSI initiator](#install-the-iscsi-initiator) under Under the hood.
- **Free disk on each server.** Each copy uses space on the disk of a server. With three servers, plan for the sum of all your disk sizes on each server.
- **Kubernetes 1.25 or later.** A `kind` cluster cannot run replicated storage.

:::caution
Before you remove replicated storage, copy your data to another place. The removal deletes the disks on it. If you set `enabled` back to `false`, Geass does not remove the storage, because it holds your data.
:::

## Resize a database volume

You can make the disk of a deployed database larger. You cannot make it smaller.

1. Open the database.
2. Select **Settings**, then **Database**.
3. In **Storage**, select a larger **Size**. The list offers 1, 5, 10, 20, 50, 100, 200, and 500 GB, and the current size.
4. Click **Save changes**.
5. In the top bar of the project, click **Deploy changes**.

The size is for each instance. A high-availability database has three disks, and each one grows. Through the REST API, you can send any size of `1Gi` or more as `storageSize`. See [Databases](/docs/guides/databases/#size-storage-and-resizing).

The **Size** field is locked when the database is on local storage. The dashboard then shows "This storage can't be resized". One cluster can have databases of both kinds. You might also see these messages:

| Message | Meaning |
| --- | --- |
| storage can be increased but not decreased once deployed (currently X) | You asked for less than the last deployed size |
| this cluster's storage can't be resized, so a deployed database stays at X | The database is on local storage |
| storage size must be at least 1Gi | Below the minimum |
| storage size must be a quantity such as 10Gi | Geass could not read the value |
| Redis keeps its data in memory, so there is no storage to size | Redis has no disk |
| storage size only applies to databases that run in the cluster | The database is external |

The minimum is the size of the last deploy. A larger size that you saved but did not deploy does not count yet.

If the storage refuses to grow a MySQL or SQLite disk, the database continues to run at its old size. After a MySQL resize, an admin can check the size. See [Under the hood](#under-the-hood).

## What replication gives you

With two or three copies on different servers, the loss of a server or its disk does not lose the data. A database that moves to another server uses a copy that is left. When the server returns, or a new server joins, the storage makes the missing copy again.

Replication does not protect you from these events:

- Data that you delete.
- A bad migration.
- A bug that damages rows.
- The deletion of the database.

Every copy gets the same change. For these events, use [backups](/docs/guides/backups-and-restore/).

High-availability databases do not use disk copies. They run three or more instances that copy the data to each other. See [Databases](/docs/guides/databases/#instances-and-high-availability).

## Existing volumes follow the server count

When the cluster grows, Geass adds copies to existing disks, up to three. For example, a disk made on a one-server cluster gets a second copy when a second server joins. These rules apply:

- Geass never removes copies when a server leaves.
- Geass does not add copies to disks of high-availability databases or to disks on local storage.
- Geass waits until replicated storage is ready on every server.
- The storage copies the whole disk to the new server. Expect extra disk and network load until the copies finish.

## Nodes that must not hold data

The servers of a [node pool](/docs/guides/node-pools/) come and go. So by default, a pool keeps data off them. You can do the same for a server that you add one at a time: set **Data on this server** to **None**. The dashboard marks such a server **Ephemeral**.

On an ephemeral server, these rules apply:

- Databases and the object storage server do not run there. Only services run there, and services have no disk.
- The server does not count for the number of copies.
- Replicated storage does not put a copy there.

A pool with **Data on these nodes** set to **Allow** has ordinary servers that can hold data. The autoscaler does not remove such a server while it holds a copy of a disk or runs something that uses a disk. So a scale-down cannot leave a disk one copy short.

## Limits

- You cannot change the kind of storage of a database. Geass has no move tool.
- Disks only grow. Geass does not make a disk smaller.
- Three copies is the maximum, however many servers you have.
- A compliance tag keeps new copies of other projects off compliance servers. It does not move copies that are already there.
- Geass uses the default settings of the storage for data paths and disk reserves.
- If you turn off replicated storage, Geass does not remove it.

## Under the hood

This section is for admins who have `kubectl` access to the cluster.

Replicated storage is the Longhorn add-on, in the `longhorn-system` namespace. Its web UI is turned off. Geass creates the storage classes `geass-replicated` and `geass-single`. It also creates `geass-replicated-<region>` for each region, and `geass-tagged-<hash>` for each set of required tags. A database records its class in the annotation `geass.dev/storage-class`. An empty value means the default class of the cluster, which is `local-path` on K3s.

### Install the iSCSI initiator

To install the initiator on a Debian or Ubuntu server, run these commands on that server:

```bash
sudo apt-get update -qq
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y open-iscsi
sudo systemctl enable --now iscsid
```

On Red Hat family systems, install `iscsi-initiator-utils` with `dnf` or `yum`. On SUSE, install `open-iscsi` with `zypper`. Then run `sudo systemctl enable --now iscsid`.

### The add-on setting

The **Add-ons** page sets `spec.addons.longhorn.enabled` on the `GeassCluster` named `default`. The operator writes the state to `status.addons`. The state is **Ready** when the chart runs, the CSI driver `driver.longhorn.io` is registered, and every node has a ready Longhorn manager pod.

### Check the storage

To check the storage, run these commands:

```bash
kubectl get pods -n longhorn-system -o wide
kubectl get storageclass
kubectl get geassdatabase orders -n geass-system \
  -o jsonpath='{.metadata.annotations.geass\.dev/storage-class}{"\n"}'
kubectl get pvc -n proj-1a2b3c4d-production
kubectl get volumes.longhorn.io -n longhorn-system \
  -o custom-columns=NAME:.metadata.name,REPLICAS:.spec.numberOfReplicas,ROBUSTNESS:.status.robustness
```

The storage classes appear when every server has a running Longhorn manager pod. A regional or tagged class appears when every server in that region, or with those tags, has one.

## Troubleshooting

**A new database, or the object storage server, stays at "Storage is starting".**
Replicated storage is still starting, or a server is not ready for it. The most common cause is a server without the iSCSI initiator. See [Requirements](#requirements). After 15 minutes, the message changes to "Storage is unavailable". An admin can find the server that is not ready. See [Under the hood](#under-the-hood).

**A database in a region or a tagged project stays at "Storage is starting" and never changes.**
The storage for that region or those tags is not ready. It needs at least one server in the region with all the tags, and replicated storage ready on each such server. This wait does not change to "Storage is unavailable".

**The Size field of a database is locked.**
The database is on local storage. To move it, restore a backup into a new database.

**Disks stay at one copy after you added servers.**
Geass waits until replicated storage is ready on every server. A new server without the iSCSI initiator is never ready. See [Install the iSCSI initiator](#install-the-iscsi-initiator).

**A disk shows as degraded after you added a server.**
The storage is copying the disk to the new server. Wait for the copy to finish.

**The cluster ran out of disk.**
Each server holds a copy of each replicated disk. Free space on the server, add a server with more disk, or delete databases that you do not need.

## Related

- [Databases](/docs/guides/databases/)
- [Backups and restore](/docs/guides/backups-and-restore/)
- [Object storage](/docs/guides/object-storage/)
- [Scaling and placement](/docs/guides/scaling-and-placement/)
- [Node pools and autoscaling](/docs/guides/node-pools/)
- [Compliance node tags](/docs/guides/compliance-tags/)
- [Requirements](/docs/getting-started/requirements/)
- [High availability](/docs/operations/high-availability/)
