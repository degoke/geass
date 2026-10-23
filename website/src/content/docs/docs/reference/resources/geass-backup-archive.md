---
title: "GeassBackupArchive"
description: "Reference for GeassBackupArchive: Geass's record of one database's backups in S3, which outlives the database so you can restore it."
sidebar:
  order: 6
---

A `GeassBackupArchive` is Geass's record of the backups of one database. It names the S3 bucket and folder where they live, how long they are kept, and which restore points exist. Geass creates it for you, once a database has backups turned on and the project has backup storage. You do not create or edit one. The dashboard reads it to list restore points, and a restore refers to it by name. The archive outlives the database for a grace period, so you can still restore a database that was deleted.

For the task-oriented view, see [Backups and restore](/docs/guides/backups-and-restore/).

## Summary

| Property | Value |
| --- | --- |
| API version | `geass.geass.dev/v1alpha1` |
| Kind | `GeassBackupArchive` |
| Plural | `geassbackuparchives` |
| Scope | Namespaced. Geass keeps every one in `geass-system`. |
| Subresources | `status` |
| Printer column `Database` | `.spec.database` |
| Printer column `Last backup` | `.status.lastBackupTime` |
| Printer column `Delete after` | `.spec.deleteAfter`, printed as the time itself (a date that has not come yet cannot be printed as an age) |
| Finalizer | None |

## Example

Geass writes archives. This one is shown so you can read the fields.

```yaml title="orders-db-archive.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassBackupArchive
metadata:
  name: orders-db-1a2b3c4d
  namespace: geass-system
spec:
  project: storefront
  environment: production
  database: orders-db
  databaseUID: 1a2b3c4d-5e6f-7a8b-9c0d-1e2f3a4b5c6d
  serverName: orders-db-1a2b3c4d
  storage:
    connectionRef:
      name: storefront-aws
    bucket: geass-backups-0123456789ab
    region: eu-west-1
  retentionDays: 14
  source:
    engine: Postgres
    version: "16"
    databaseName: orders-db
    storageSize: 20Gi
```

## Spec fields

| Field | Type | Required or default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.project` | string | Required | Minimum length 1 | The project that owned the database. |
| `spec.environment` | string | Required | Pattern `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, max length 63 | The environment the database ran in. |
| `spec.database` | string | Required | Minimum length 1. CEL: `self == oldSelf`, message "database cannot be changed" | Name of the [GeassDatabase](/docs/reference/resources/geass-database/) that owns the backups. |
| `spec.databaseUID` | string | Required | Minimum length 1. CEL: `self == oldSelf`, message "databaseUID cannot be changed" | The database's `metadata.uid`. It tells a database apart from a later one that reuses its name. |
| `spec.serverName` | string | Required | Minimum length 1. CEL: `self == oldSelf`, message "serverName cannot be changed" | The folder the backups live in. It is the database name (cut to 40 characters) plus the first 8 characters of the UID, so a new database never writes into an old one's backups. |
| `spec.storage` | object | Required | CEL: `self == oldSelf`, message "storage cannot be changed" | Where the backups are kept. Fixed when the archive is created: an archive keeps writing where its history lives, whatever the project says later. |
| `spec.storage.connectionRef` | LocalObjectReference | Required | None | The AWS [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/) that owns the bucket. |
| `spec.storage.connectionRef.name` | string | CRD default `""` | None | Name of the connection. |
| `spec.storage.bucket` | string | Required | 3 to 63 characters, pattern `^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$` | The S3 bucket. Geass creates it when it does not exist. |
| `spec.storage.region` | string | Optional | None | The bucket's region. Geass writes the region it resolved. |
| `spec.retentionDays` | integer (int32) | Required | Minimum 1, maximum 365 | How many days back the database can be restored. Follows the database's `spec.backup.retentionDays`. |
| `spec.source` | object | Optional | None | What a restore needs to know about the original database. |
| `spec.source.engine` | string | Optional. Empty means Postgres. | Enum `Postgres`, `MySQL`, `SQLite`, `Redis`, `MariaDB` | The engine of the source database. |
| `spec.source.version` | string | Optional | None | The Postgres major version. Other engines do not record one. |
| `spec.source.databaseName` | string | Optional | None | The database name. Recorded for Postgres and MySQL. |
| `spec.source.storageSize` | Quantity | Optional | None | The source disk size. A restored database needs at least this much. |
| `spec.source.resources` | ResourceRequirements | Optional | None | The CPU and memory the source used. |
| `spec.source.highAvailability` | boolean | Optional | None | Whether the source ran as a replicated cluster. Recorded for MariaDB. A replicated MariaDB is backed up with physical copies and archived logs, restored to any moment, and has to be restored into a replicated database. |
| `spec.source.instances` | integer | Optional | None | How many instances the source ran. A restore starts with as many. |
| `spec.deleteAfter` | date-time | Optional | None | When Geass removes the backups. Empty while the database is protected. Set when the database is deleted or backups are turned off. |

:::note
The Go types and the generated CRD agree for every field on this page.
:::

## Status

| Field | Type | Meaning |
| --- | --- | --- |
| `status.lastBackupTime` | date-time | When the latest backup finished. |
| `status.recoveryWindowStart` | date-time | The earliest moment you can restore to. |
| `status.recoveryWindowEnd` | date-time | The latest moment known to be recoverable. It trails the present by about 5 minutes while the database runs, and stops moving once the database is deleted. |
| `status.restorePoints` | list | Base backups, newest first, at most 100. Each has `id` and `time`. For MySQL and SQLite each daily snapshot is a restore point. |
| `status.conditions` | list | The only condition is `Purged`. |

The `Purged` condition is only ever `False`. Geass sets it when it cannot remove backups that are past `spec.deleteAfter`, and the archive is deleted when the removal succeeds.

| Reason | Meaning |
| --- | --- |
| `ConnectionUnavailable` | The AWS connection, its Secret or its keys are gone. The message says which, for example `cloud connection "storefront-aws" is unavailable`. |
| `PurgeFailed` | AWS refused the delete. The message is the AWS error code and text, for example `AccessDenied: Access Denied`. |

Geass tries again every 15 minutes.

## Lifecycle

1. **Created.** The database controller creates an archive the first time the database has backups on and the project has backup storage. Its name is the `serverName`. If a database had backups before, turned them off and turned them on again, the new archive takes the name with `-2`, `-3` and so on.
2. **Kept in step.** On every reconcile Geass updates `retentionDays`, `source`, the labels and the display-name annotation. The restore points and recovery window in `status` are written from what the cluster and the bucket report.
3. **Retired.** Geass sets `spec.deleteAfter` to 7 days from now when the database is deleted (after a final backup), when backups are turned off, or when the database's namespace changes. The backups stay restorable until then. If the owning database disappears without the usual clean-up, the archive controller retires its archive the same way.
4. **Purged.** Once `deleteAfter` has passed, the archive controller deletes everything under `<project>/<serverName>/` in the bucket (objects, old versions and unfinished uploads), then deletes the archive. It never touches an archive whose database is alive.

Labels on an archive: `geass.dev/managed-by=geass`, `geass.dev/kind=GeassDatabase`, `geass.dev/name=<database>`, `geass.dev/system-namespace=geass-system`, `geass.dev/project` and `geass.dev/environment`. The annotation `geass.dev/display-name` holds the database's display name.

## What the dashboard does

The dashboard only reads archives.

| Where | What it does |
| --- | --- |
| Resource panel, **Backups** tab | Shows the restore points, **Last backup** and the recovery window from the archive that belongs to the database. When `spec.deleteAfter` is set it says the backups are kept until that date. |
| **Restore** buttons on the **Backups** tab | Creates a new [GeassDatabase](/docs/reference/resources/geass-database/) whose `spec.restoreFrom.archive` is this archive's name. If the original environment no longer exists, the dialog asks for an **Environment**. |
| Project **Settings** > **Backups** > **Recently deleted databases** | Lists archives whose database is gone and that have a `spec.deleteAfter`, as `Deleted · restorable until <date>`, each with a **Restore** button. |
| Project **Settings** > **Backups** > **Backup storage** (**Use this connection**) | Sets the project's `spec.backupStorage`. New archives copy it into `spec.storage`. |

A viewer in the dashboard sees archives with `spec.storage` cleared. The API reads them as `backupArchives` in `GET /api/bootstrap` (`geass api GET /bootstrap`), and a token that cannot change things gets the same cleared copy. The command line and the MCP server have no archive command or tool. A restore from them goes through `POST /api/v1/databases/restore`, which creates the new database with `spec.restoreFrom.archive`.

## Editing by hand

Do not create archives. Do not delete them either: the resource has no finalizer, so deleting it does not remove the backups in S3. Geass forgets them and they stay in the bucket. Let `spec.deleteAfter` run its course instead.

:::danger
Setting `spec.deleteAfter` to a time in the past makes Geass delete the backups at once. Setting it for a live archive makes the next reconcile treat the archive as retired.
:::

The fields `database`, `databaseUID`, `serverName` and `storage` cannot be changed: the API server rejects the edit.

## What happens under the hood

- The bucket holds one folder per archive: `s3://<bucket>/<project>/<serverName>/`. Postgres writes its base backups and change log there. MySQL and SQLite write `dumps/<timestamp>.sql.gz` files there.
- Geass creates a bucket named `geass-backups-<random>` and an IAM user `geass-<bucket>` whose inline policy `geass-bucket` is limited to that bucket. The credentials are stored in the Secret `geass-backup-storage-<bucket>` in `geass-system` (keys `ACCESS_KEY_ID`, `ACCESS_SECRET_KEY` and `REGION`). A copy sits in each database's namespace as `<database>-backup-credentials`. The AWS root keys never leave `geass-system`.
- Postgres archives use a CloudNativePG `ScheduledBackup` and the cluster's own archiving. MySQL and SQLite use a CronJob `<database>-backup` and a Job per backup, which run on nodes that carry the project's required tags when the database was placed with them. See [GeassDatabase](/docs/reference/resources/geass-database/#backups).
- The bucket is in your AWS account, so required node tags do not reach the backups. See [Backups and restore](/docs/guides/backups-and-restore/#backups-and-compliance-tags).

```bash title="Inspect archives"
kubectl -n geass-system get geassbackuparchives
kubectl -n geass-system get geassbackuparchive orders-db-1a2b3c4d -o yaml

# Credentials Geass created for the bucket
kubectl -n geass-system get secrets | grep geass-backup-storage
```

## Troubleshooting

### `Purged` is `False`

The archive is past `deleteAfter` and Geass could not delete the backups. For `ConnectionUnavailable`, restore the AWS [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/) it names, with the same name and working keys. For `PurgeFailed`, read the AWS error in the message. The usual cause is a missing permission. Geass retries every 15 minutes.

### The archive stays after you deleted the database

That is intended. Geass keeps it for 7 days after the database is deleted so you can restore. `Delete after` shows the date.

### A restore says `These backups are no longer available`

The archive named in `spec.restoreFrom.archive` does not exist. It was purged, or the name is wrong. Check `kubectl -n geass-system get geassbackuparchives`.

### The archive has no restore points

The first backup has not finished. See the `status.backup` message on the database: `Taking the first backup`, or an AWS error. A restore needs at least one restore point and refuses with `These backups have no restore points yet`.

## Related

- [Backups and restore](/docs/guides/backups-and-restore/)
- [GeassDatabase](/docs/reference/resources/geass-database/)
- [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/)
- [GeassProject](/docs/reference/resources/geass-project/)
- [Resources overview](/docs/reference/resources/overview/)
