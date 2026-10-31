---
title: "Backups and restore"
description: "Back up Postgres, MySQL, MariaDB, and SQLite databases to an S3 bucket in your own AWS account, restore to a new database, and check backup health."
---

Geass backs up the Postgres, MySQL, MariaDB, and SQLite databases that it runs. The backups go to an S3 bucket in your own AWS account, so you keep the data and you pay AWS directly. To recover lost or damaged data, or a deleted database, restore the backup into a new database. Geass never restores in place.

Postgres and MariaDB with high availability are backed up continuously. You can restore them to any recent moment. MySQL, SQLite, and MariaDB with one server are copied every day. You restore them to one of those copies, and you lose the changes made after it.

## What is backed up

| Database | How | You can restore to |
| --- | --- | --- |
| PostgreSQL | Continuous, with a full backup every day | Any moment in the recovery window, or a daily restore point |
| MariaDB with high availability | A full copy every night, with continuous change logs | Any moment in the recovery window, or a nightly restore point |
| MySQL, SQLite, and MariaDB with one server | A compressed SQL dump every day | A daily restore point |
| Redis | Not backed up. It is a cache with no disk | Not applicable |
| External databases (AWS RDS, PlanetScale) | Not backed up. Use the backups of your provider | Not applicable |
| Shared database servers and logical databases | Not backed up | Not applicable |
| Buckets | Not backed up. See [Object storage](/docs/guides/object-storage/) | Not applicable |
| Pull request environment copies | Not backed up | Not applicable |

New PostgreSQL, MySQL, MariaDB, and SQLite databases start with backups on and a 7-day retention. This applies to databases from the dashboard, the REST API, and templates. Databases that existed before backups did start with backups off. A database with backups on protects nothing until its project has backup storage.

## Before you start

1. **Add an AWS connection to the project.** Add it under **Settings**, **Connectors**. See [Connect AWS](/docs/guides/connectors/aws/).
2. **Choose backup storage for the project.** See the next section.

The AWS keys must be able to create an S3 bucket and a restricted user. See [What Geass does in your AWS account](#what-geass-does-in-your-aws-account).

## Choose where backups are stored

You choose storage one time for each project. Every database in the project uses it. Geass creates the bucket when the first database needs it.

1. Open the project, and select **Settings**, then **Backups**.
2. Under **Backup storage**, select an **AWS connection**. If the project has no connection, the page says "Connect an AWS account first".
3. Click **Use this connection**. The dashboard says "Saved".

The **Backups** tab of a database also shows this choice, under **Choose where backups are stored**, when the database needs storage. After you save, the page shows the **AWS connection**, the **Bucket**, and the **Region**, with **Change** and **Remove** buttons.

These rules apply:

- The choice takes effect immediately.
- A database that already has backups continues to use its first storage, even if you change the choice later.
- **Remove** leaves the stored backups where they are. A database with no backups yet cannot start backups until you choose storage again.
- Geass names the bucket `geass-backups-` and 12 random characters, in the region of the connection.

To select the bucket name and the region, use the REST API with a workspace admin token:

```bash
curl -X POST https://geass.example.com/api/projects/proj-1a2b3c4d/backup-storage \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json" \
  --data-urlencode connection=prod-aws \
  --data-urlencode bucket=acme-storefront-backups \
  --data-urlencode region=eu-west-1
```

To clear the choice, send `remove=on`. If you save again with the same connection and no `bucket`, Geass keeps the bucket. A different connection gets a new bucket name. Viewers cannot see which bucket a project uses.

:::caution
Use the generated name unless you know that the name is yours. If another AWS account owns the bucket, Geass reports the bucket as created, and backups then fail with an access error.
:::

## What Geass does in your AWS account

The first time that a database needs backups, Geass uses the keys of the connection to do these actions:

- Create the S3 bucket, if it does not exist.
- Create an AWS user named `geass-` and the bucket name, with a policy that allows only that bucket.
- Create one access key for that user.

Databases use the key of that user. The keys of the connection never reach your databases. Geass changes no other bucket setting, so the AWS defaults apply. If you turn on versioning yourself, Geass also removes old versions when it removes expired backups.

The keys of the connection need permission for these actions:

- The IAM actions `CreateUser`, `PutUserPolicy`, `CreateAccessKey`, `ListAccessKeys`, `DeleteAccessKey`, `DeleteUserPolicy`, and `DeleteUser`.
- S3 bucket creation.
- S3 object listing, including versions and multipart uploads.
- S3 object and version deletion.
- S3 multipart abort.

## Turn backups on, off, or change retention

1. Open the database. The **Backups** tab and the **Backups** section of **Settings** show the same panel.
2. Turn **Back up this database** on or off.
3. In **Keep backups for**, select 7, 14, 30, or 90 days.
4. Click **Save changes**.

These changes apply immediately, without a deploy and without a restart. **Keep backups for** sets how far back you can restore. A longer period uses more storage in your AWS account. If you lower it, Geass can remove backups older than the new period.

Through the API, send `backups=on` or `backups=off`, and `backupRetentionDays` (1 to 365), to `POST /api/databases/<ID>/update`.

When you turn backups off, Geass stops taking backups. It keeps the backups that it has for seven days, and then removes them. The panel says "Backups from before they were turned off are kept until" and the date. If you turn backups on again, a new set starts.

## How backups work

### PostgreSQL: continuous

- Geass saves the changes of the database to your bucket continuously. It also takes a full backup every day at about 02:00 UTC. The first full backup starts when storage is ready.
- A **restore point** is a full backup. You can restore to a restore point, to the most recent state, or to any time in the recovery window.
- Geass checks each full backup, and lists it as a restore point only when it can be restored. A new backup is checked after about seven minutes.
- The first backup can come before the database starts to save its changes. In that case, the status says "A backup was taken before the database could save its log files, so it cannot be restored. A new one is being taken". Geass tries again up to three times.
- The recovery window starts at the oldest kept full backup. It ends about five minutes before now, because the newest changes might not be in the bucket yet. The window stops moving if saving the changes fails.

### MariaDB with high availability: copies and archived logs

- Geass takes a full copy every night at about 02:00 UTC. The first copy comes a minute or two after you turn on backups, because the servers restart first.
- Geass archives the change logs of the primary about every ten minutes. So you can restore to a moment about fifteen minutes ago, but not to the last second.
- The copies in the bucket are the restore points. Geass removes copies older than the retention, and the logs that no kept copy needs.
- When the primary changes, Geass takes a new copy from the new primary. The moments between the last log of the old primary and that new copy cannot be restored.
- Each copy needs temporary space as large as the database, on the storage of the database.
- In a project that requires node tags, this backup needs Kubernetes 1.36 or later. On an older cluster, the backups show **At risk**. See [Compliance node tags](/docs/guides/compliance-tags/).
- A restore starts from the nearest copy before the moment that you chose, and then replays the logs. If the logs do not reach that moment, the restore fails. It does not come up with older data. A small restore took about 90 seconds.

### MySQL, MariaDB and SQLite: daily copies

- Geass takes a compressed SQL dump every day at about 02:00 UTC. It takes the first dump immediately after setup. If the first dump fails, Geass tries again after 10 minutes.
- A MySQL dump has the main database of the server, with routines, events, and triggers. It does not have users, grants, or logical databases on the same server.
- Geass removes dumps older than the retention. Geass never removes the newest dump, even an old one.
- A dump has one hour to finish. A dump larger than 5 GB fails, and shows as a failed backup.

### Where backups sit in the bucket

Each database gets its own folder in the bucket, inside a folder for the project ID. The folder name is the database ID and part of a unique ID. If you turn backups off and on again, the database gets a new folder that ends with `-2`, `-3`, and so on. So a new database that reuses an old name never writes into old backups.

### Backups and compliance tags

A project can require compliance node tags, for example `hipaa`. See [Compliance node tags](/docs/guides/compliance-tags/). For a database of such a project, Geass handles each backup and restore on servers with those tags. A database created before the project required the tags keeps its old placement, and so do its backups.

The backups go to your S3 bucket, outside the cluster, so the tags do not apply to them. See [Where a database runs](/docs/guides/databases/#where-a-database-runs).

## Restore to a new database

A restore creates a new database. The original does not change. You check the new database, and then move your apps to it.

1. Open the database, and select the **Backups** tab.
2. Under **Restore**, select what to restore:
   - PostgreSQL and MariaDB with high availability: click **Restore most recent state**, or click **Restore** beside a restore point. To restore to a time, enter it in **Restore to a specific time**, and then click **Restore to this time**. The time is in your local time zone, inside the window that the panel shows.
   - MySQL, SQLite, and MariaDB with one server: click **Restore latest backup**, or click **Restore** beside a restore point.
3. In the **Restore to a new database** dialog, check the details. If the original environment no longer exists, select another **Environment**.
4. Click **Restore**. The dashboard says "Restore started" and opens the **Deployments** tab of the new database.

The panel lists the five newest restore points. To see the others, click **Show all N restore points**. Until the first backup finishes, the panel says "Restore points appear after the first backup finishes".

The new database has these properties:

- It has a new resource ID and the display name `<original name>-restored`.
- It is in the same project and environment, unless you selected another environment.
- It has the engine, version, database name, size, and disk of the original.
- It runs one instance, even if the original had high availability. MariaDB with high availability is the exception: the restore has as many instances as the original.
- Geass places it like any new database, with the current tags of the project. It does not copy the **Run on** choice of the original.
- It has backups on, with the retention of the original.
- It starts immediately, with no draft. It shows "Restoring database from backup" until it is ready.
- It gets its own password. MariaDB with high availability is the exception: it keeps the password of the original. To use a different password, change it in the new database.

To switch over, add a reference from your app to the new database, and deploy the app. See [Connect an app to a database](/docs/guides/databases/#connect-an-app-to-a-database). The old connection string does not work on the new database.

A restore needs room on the cluster, like any new database.

### Restore from the API

To find the name of the backups of a database, list them:

```bash
geass api GET /bootstrap | jq -r '.backupArchives.items[].metadata.name'
```

Then start the restore:

```bash
geass api POST /databases/restore -d '{"archive": "orders-1a2b3c4d", "time": "2026-10-11T14:30:00Z"}'
```

Send `restorePoint` (a restore point ID) or `time` (RFC 3339), not both. If you send neither, Geass restores the latest state. `environment` is optional. The reply names the new database: `{"ok":true,"name":"<new ID>","kind":"databases"}`. You need the admin role (Full access). The MCP server has no tool for backups or restores.

Before it creates anything, Geass can refuse a restore with one of these messages:

| Message | Cause |
| --- | --- |
| Choose which backups to restore | No `archive` was sent |
| These backups are no longer available | The backups were already removed |
| Choose either a restore point or a time, not both | Both were sent |
| This database can only be restored to a restore point | A time was sent for MySQL, SQLite, or MariaDB with one server |
| These backups have no restore points yet | The first backup has not finished |
| The chosen restore point is not available | The ID is not in the backups |
| The chosen time is earlier than the oldest restore point | The time is before the recovery window |
| The chosen time is later than the newest recoverable moment | The time is after the recovery window |
| Enter a valid date and time | Geass could not read the time |

## Recover databases from a bucket

If you lose the cluster, a new cluster can find your backups in the bucket. Geass keeps a small description file next to the backups of each database. For MariaDB with high availability, that file also holds the database password. So anyone who can read the bucket folder can open that database, as they can open the copies.

The databases found in the bucket show under **Recently deleted databases** in **Settings**, **Backups** of the project, marked "Found in the bucket". Restore them like any other database. They are offered for 30 days. Geass never deletes the data in a bucket that it found. See [Disaster recovery](/docs/operations/disaster-recovery/#recover-databases-from-a-bucket).

## Delete a database

When you delete a database that has backups, Geass first takes a final backup. Geass waits up to 10 minutes for it. If the database is not running, or the final backup fails, Geass deletes the database anyway and keeps the backups that exist.

Geass then keeps the backups for seven days. To restore a deleted database:

1. Open the project, and select **Settings**, then **Backups**.
2. Under **Recently deleted databases**, find the database. Each row shows "Deleted · restorable until" and a date.
3. Click **Restore**, and continue as in [Restore to a new database](#restore-to-a-new-database).

After seven days, Geass deletes the backups of the database from the bucket.

A database with no backups is destroyed with its data. The delete section says "This database has no backups. Deleting it permanently destroys its data".

:::caution
Restore what you need before you delete a project. When you delete a project, Geass deletes its AWS connections in the same step, and might not take a final backup. Without a connection, Geass cannot restore or remove the backups. They stay in your bucket. Empty and delete the bucket in AWS yourself when you no longer need it.
:::

## Check that backups are healthy

The **Backups** panel of a database shows a badge and a message:

| Badge | Message | Meaning |
| --- | --- | --- |
| **Needs storage** | "Choose where backups are stored to protect this database" | The project has no backup storage |
| **Needs storage** | "Backup storage is not working: ..." | Geass could not set up the bucket or the user. The AWS error follows |
| **Starting** | "Taking the first backup" | The first backup has not finished |
| **Protected** | "Backups are running" | The latest backup succeeded |
| **At risk** | "Recent changes are not being saved to backup storage" | Postgres cannot save its changes |
| **At risk** | "The latest backup failed" | A backup failed, and no later one succeeded |

When backups are off, the panel shows "Backups are off — this database has no recovery if its data is lost or deleted". On the canvas, a card shows **Not backed up** or **Backups at risk** when there is a problem. A card shows "continuous backup" or "daily backup" whenever backups are on, even when they fail. So read the badge, not the card.

The panel also shows **Last backup**. For PostgreSQL, it shows "You can restore to any moment since" and a date. For MySQL and SQLite, it shows **Oldest backup**. Make sure that **Last backup** is less than one day old. The status refreshes about every five minutes.

A backup problem never takes a working database out of **Ready**. A restore problem fails the new database.

From the command line, `geass databases get orders` shows the backup state, the last backup, and the start of the recovery window.

Do a test restore from time to time. Restore to a new database, check the data, and delete the new database. A backup that you never restored is not proven.

## Failure modes

| Symptom | Cause | What to do |
| --- | --- | --- |
| **Needs storage**, and a card marked **Not backed up** | The project has no backup storage | Choose storage under **Settings**, **Backups** |
| "Backup storage is not working: AccessDenied: ..." | The AWS keys cannot create the bucket or the user | Give the keys the permissions in [What Geass does in your AWS account](#what-geass-does-in-your-aws-account), and then save storage again |
| "Backup storage is not working", naming a missing connection | The AWS connection was removed, or belongs to another project | Create the connection again, or select another one |
| **Starting** for a long time | The first backup has not finished. A first backup of a large Postgres database takes time | Wait. A daily copy retries after 10 minutes |
| **At risk**, "A backup was taken before the database could save its log files, so it cannot be restored. A new one is being taken" | The first Postgres backup came too early | Wait. Geass takes another backup, up to three times |
| **At risk**, "Recent changes are not being saved" | Postgres cannot write to the bucket | Check the bucket, the AWS user, and network access from your servers |
| **At risk**, "The latest backup failed" | The last backup failed | A daily copy larger than 5 GB fails. An admin can read the job logs. See [Under the hood](#under-the-hood) |
| The new database stays at "Restoring database from backup" | The restore is still running | Wait. A large Postgres restore takes time |
| "The restore could not be completed. Check that the backups are still available, then restore again" | The restore failed | Make sure that the restore point still exists, and restore again |
| Backups still in the bucket after the retention period | Geass could not remove them, for example because the connection is gone | Fix the connection. Geass retries every 15 minutes |
| An old restore point is missing | Retention removed it | Set a longer **Keep backups for** before you need the restore point |

## Under the hood

This section is for admins who have `kubectl` access to the cluster.

- Each database with backups has a `GeassBackupArchive` in `geass-system`. It records the restore points and the recovery window.
- PostgreSQL backs up through CloudNativePG. MySQL, SQLite, and MariaDB with one server use a CronJob `<database ID>-backup` in the namespace of the environment. MariaDB with high availability uses the backups of the MariaDB operator.
- A database manifest sets backups in `spec.backup` (`enabled`, `retentionDays`) and a restore in `spec.restoreFrom` (`archive`, and `restorePoint` or `time`).

```bash
kubectl get geassbackuparchives -n geass-system
kubectl get geassdatabase orders -n geass-system -o jsonpath='{.status.backup}{"\n"}'

NS=proj-1a2b3c4d-production
# PostgreSQL
kubectl get backups.postgresql.cnpg.io -n "$NS"
# MySQL, SQLite, and MariaDB with one server
kubectl get cronjob,jobs -n "$NS" -l geass.dev/backup-for=orders
# MariaDB with high availability, when the recovery window does not move
kubectl -n "$NS" get pointintimerecovery
kubectl -n "$NS" logs orders-0 -c agent
```

## Troubleshooting

**The Backups tab is missing from a database.**
The database is not PostgreSQL, MySQL, MariaDB, or SQLite that Geass runs. Redis and external databases have no backups.

**"Backups start after you deploy this database."**
The database is still a draft. Deploy it.

**I turned backups on and nothing is stored.**
Look at the badge. **Needs storage** means that the project has no storage. Choose storage in **Settings**, **Backups**.

**There is no Restore button.**
Restore points show after the first backup finishes. Wait until **Last backup** shows a time.

**My restored database has different data than I expected.**
A MySQL or SQLite restore has the data of that day's copy. A Postgres "most recent state" restore ends at the last change in the bucket, which can be a few minutes before the data was lost.

**A restored MariaDB stays at "Restoring database from backup", and then fails.**
After six hours, the restore shows "The restore could not be completed". The usual cause is a root password that someone changed by hand in the original. Restore from a copy made before the change, or change the password back first.

**The first backup of MariaDB with high availability takes a minute or two.**
This is expected, because the servers restart first. See [MariaDB with high availability](#mariadb-with-high-availability-copies-and-archived-logs).

**The recovery window of MariaDB ends well before now.**
The window trails by up to about fifteen minutes. If it does not move at all, the logs cannot be archived. An admin can check them. See [Under the hood](#under-the-hood).

**The AWS bill shows a bucket that I did not make.**
Geass created it for the backup storage of your project. Its name starts with `geass-backups-`. Do not delete the bucket while databases use it.

## Related

- [Databases](/docs/guides/databases/)
- [Storage and volumes](/docs/guides/storage/)
- [Connect AWS](/docs/guides/connectors/aws/)
- [Shared database servers](/docs/guides/shared-database-servers/)
- [Compliance node tags](/docs/guides/compliance-tags/)
- [Command line](/docs/guides/command-line/)
- [Troubleshooting](/docs/operations/troubleshooting/)
