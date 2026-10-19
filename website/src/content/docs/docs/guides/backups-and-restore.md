---
title: "Backups and restore"
description: "Back up Postgres, MySQL, MariaDB and SQLite databases to an S3 bucket in your own AWS account, restore to a new database, and check that backups are healthy."
---

Geass backs up the Postgres, MySQL, MariaDB and SQLite databases it runs. The backups go to an S3 bucket in your own AWS account, so you keep the data and pay AWS directly. If a database's data is lost or damaged, or you delete the database, you restore it as a new database next to the original. Geass never restores in place.

Postgres is backed up continuously, and so is a MariaDB database with high availability, so you can restore either to any recent moment. MySQL, SQLite and a MariaDB database that runs as one server are copied once a day, so you restore to one of those copies and lose the changes made since.

## What is backed up

| Database | Backed up | How | You can restore to |
| --- | --- | --- | --- |
| PostgreSQL running in the cluster | Yes | Continuous archiving and a daily base backup | Any moment in the recovery window, or a daily restore point |
| MySQL running in the cluster | Yes | A compressed SQL dump every day | A daily restore point |
| MariaDB with high availability | Yes | A physical copy every night, and the binary logs archived beside it | Any moment in the recovery window, or a nightly restore point |
| MariaDB as one server | Yes | A compressed SQL dump every day | A daily restore point |
| SQLite running in the cluster | Yes | A compressed SQL dump every day | A daily restore point |
| Redis | No. It is a cache with no persistence | None | Not applicable |
| External databases (AWS RDS, PlanetScale) | No | Use your provider's backups | Not applicable |
| Shared database servers and their logical databases | No | None | Not applicable |
| Buckets | No | See [Object storage](/docs/guides/object-storage/) | Not applicable |
| Pull request environment copies | No. They are throwaway | None | Not applicable |

New PostgreSQL, MySQL, MariaDB and SQLite databases created in the dashboard, through the REST API or from a template start with backups on and a 7-day retention. Databases created from a manifest, and databases that existed before backups did, start with backups off, so you turn them on yourself. A database with backups on protects nothing until its project has somewhere to store them.

## Before you start

1. **An AWS connection for the project.** Add one under **Settings**, **Connectors**. A connection belongs to one project. See [Connect AWS](/docs/guides/connectors/aws/).
2. **Backup storage chosen for the project.** Choosing it records the bucket and connection. Geass creates the bucket when the first database with backups on needs it. See the next section.

The AWS keys need to be able to create an S3 bucket and manage a restricted IAM user, as listed under [What Geass does in your AWS account](#what-geass-does-in-your-aws-account).

## Choose where backups are stored

You choose storage once per project. Every database in the project uses it. Geass creates the bucket and a restricted user the first time a database in the project needs them.

1. Open the project and select **Settings**, then **Backups**.
2. Under **Backup storage**, choose an **AWS connection**. If the project has none, the page says "Connect an AWS account first" and links to **Connectors**.
3. Select **Use this connection**. The dashboard confirms with "Backup storage saved".

The same chooser appears on a database's **Backups** tab, under **Choose where backups are stored**, whenever the database needs storage. After you save, the page shows the **AWS connection**, **Bucket** and, if one was set, **Region**, with **Change** and **Remove** buttons.

Choosing storage takes effect at once. Databases that already have backups keep writing to the storage they started with, even if you change the project's choice later. **Remove** leaves stored backups where they are. Databases that have no backups yet cannot start them until you choose storage again.

With no extra input, Geass names the bucket `geass-backups-` followed by 12 random hexadecimal characters. Bucket names are global across AWS, so a random name avoids collisions. The bucket's region is the connection's region.

With the REST API you can also choose the bucket name and region. Use a workspace token with the admin role:

```bash
curl -X POST https://geass.example.com/api/projects/proj-1a2b3c4d/backup-storage \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json" \
  --data-urlencode connection=prod-aws \
  --data-urlencode bucket=acme-storefront-backups \
  --data-urlencode region=eu-west-1
```

Send `remove=on` instead to clear the choice. Saving again with the same connection and no `bucket` keeps the existing bucket. A different connection is a different AWS account, so Geass generates a new bucket name. The project's manifest field is `spec.backupStorage` with `connectionRef`, `bucket` and `region`. Viewers cannot see which bucket a project uses.

:::caution
Geass treats an "already exists" answer from S3 as success. If you choose a bucket name through the API and another AWS account already owns it, Geass reports the bucket as created, and backups then fail with an access error. Use the generated name unless you know the name is yours.
:::

## What Geass does in your AWS account

The first time a database needs backups, and again whenever the stored credentials are missing or the storage choice changes, Geass uses the connection's keys to:

- create the S3 bucket, if it does not exist,
- create an IAM user named `geass-` plus the bucket name, with an inline policy `geass-bucket` that allows only that bucket, and
- create one access key for that user.

The bucket-scoped key is what databases use. The connection's own keys never leave `geass-system`. Geass sets no other bucket property: it sends only the create request, so AWS's defaults for a new bucket apply. If you turn on versioning yourself, Geass removes old object versions and unfinished multipart uploads when it removes an expired MySQL or SQLite copy and when it purges the backups of a deleted database.

The connection's keys need permission for the IAM actions `CreateUser`, `PutUserPolicy`, `CreateAccessKey`, `ListAccessKeys`, `DeleteAccessKey`, `DeleteUserPolicy` and `DeleteUser`, and for S3 bucket creation, object listing (including versions and multipart uploads), object and version deletion and multipart abort.

## Turn backups on, off, or change retention

1. Open the database. The **Backups** tab and the **Backups** section of **Settings** show the same panel.
2. Turn **Back up this database** on or off.
3. Choose **Keep backups for**: 7, 14, 30 or 90 days. A value set elsewhere also appears in the list.
4. Select **Save changes**.

These changes apply at once. They do not wait for a deploy and they never restart the database. **Keep backups for** is how far back you can restore. Longer periods use more storage in your AWS account.

Through the API, send `backups=on` or `backups=off` and `backupRetentionDays` (1 to 365) to `POST /api/databases/<id>/update`. In a manifest, set `spec.backup`:

```yaml title="orders-db.yaml"
spec:
  backup:
    enabled: true
    retentionDays: 14
```

Turning backups off stops the schedule and, for Postgres, stops archiving. Geass keeps the backups it already has for seven days, then removes them. The panel says "Backups from before they were turned off are kept until" the date. Turning backups on again later starts a new set.

Retention is 1 to 365 days, and the default is 7. Lowering it lets Geass remove backups older than the new period.

## How backups work

### PostgreSQL: continuous

- Geass has Postgres copy its write-ahead log to your bucket continuously and take a full base backup once a day.
- The daily backup runs at about 02:00 UTC. The minute is derived from the database's UID, so databases do not all start together. The first base backup runs right after storage is ready.
- A **restore point** is a base backup. You can restore to a restore point, to the most recent state, or to any time in the recovery window.
- The recovery window starts at the oldest retained base backup. It ends about five minutes before now, because the newest changes may not have reached the bucket yet. It stops moving if archiving fails.
- Retention keeps the base backups and log needed to restore anywhere in the last N days.

### MariaDB with high availability: copies and archived logs

- The MariaDB operator takes a physical copy of a replica every night at about 02:00 UTC, with `mariadb-backup`. The first is taken a minute or two after backups are set up, once the database has begun to archive its logs and is ready again: telling it to archive makes the operator roll the servers and move the primary, and a copy taken before that is of a primary whose logs were never archived, which a restore to a moment could not replay. A copy is compressed and uploaded to the bucket as `<project ID>/<folder>/physical/physicalbackup-<UTC date and time>.xb.gz`. The copies in the bucket are the restore points. The operator removes copies older than the retention itself.
- The primary's binary logs are archived to `<project ID>/<folder>/binlogs/`. A log is archived only once the server has closed it, and the operator archives every ten minutes, so Geass runs a small job, `<database ID>-binlog-flush`, every five minutes that closes the log. The recovery window ends where the operator says the archived logs reach, so a database can be restored to a moment about fifteen minutes ago, and not to one a second ago.
- The operator never removes an archived log, so Geass removes the ones no copy that is kept can use: a log last written more than an hour before the oldest copy began. It does this once a day, in the hour after the nightly copy (03:00 UTC), because the logs are listed in full and there is one for every five minutes of the retention. It never removes the inventory file, which the operator's restore does not read for logs that end before a copy, and removes nothing while there is no copy.
- A copy taken from a primary that has since been replaced does not reach the logs of the new one. When the primary changes, Geass asks for another copy, from the new primary, so the window keeps moving. The moments between the last log archived from the old primary and that copy are not recoverable.
- The copy runs next to the replica it copies, with a staging volume as large as the database, on the database's own storage class, which the operator makes the first time it needs it and keeps. It is removed with the database. The final copy taken on delete is staged the same way, so in a project that requires node tags it stays on the nodes that carry them.
- In a project that requires compliance node tags the copy's job is made by the operator without a node selector. On Kubernetes 1.36 or later a policy gives it the project's, as it does for every pod the operator makes. On an older cluster the policy that checks refuses the job and the backups say they are at risk. See [Compliance node tags](/docs/guides/compliance-tags/).
- A backup that failed is reported as **At risk** until a later one works.
- A restore starts a new database from the copy nearest before the moment you chose, and the operator replays the logs up to it. The new database has to be replicated like the original, and keeps the original's password. The moment has to be inside the window: from the oldest restore point to the last moment the logs reach, which is what the operator reports and not the time of the newest copy. A restore to a chosen moment fails, and does not come up with older data, if the logs do not reach it. A restore of a few rows took about 90 seconds.
- A restore point is one copy. Restoring to one asks for the moment it was taken.

### MySQL, MariaDB and SQLite: daily copies

- A Kubernetes CronJob named `<database ID>-backup` runs daily at about 02:00 UTC (the CronJob uses the `Etc/UTC` time zone). A first copy is taken right after setup, so the database is not unprotected until the first night. If that first copy fails, Geass starts another after 10 minutes while no copy exists.
- MariaDB is dumped the same way with `mariadb-dump` inside its own image, and restored with `mariadb`. A job waits until the database can be reached before it starts, because a MariaDB client gives up on a connection that a new pod's network has not yet allowed.
- MySQL is dumped with `mysqldump` inside the database's own image, with a consistent view and without locking tables. It includes routines, events and triggers. It dumps the server's main database only, so logical databases created on a project's own MySQL server, and users and grants, are not in the copy.
- SQLite runs on rqlite, which serves a SQL dump over HTTP. Geass saves that dump.
- Each copy is compressed and uploaded to the bucket as `<project ID>/<folder>/dumps/<UTC date and time>.sql.gz`. The bucket listing is the source of truth for restore points.
- Geass removes copies older than the retention period, and never removes the newest copy, even a very old one.
- A dump job has one retry and one hour to finish. Its scratch space is limited to twice the database's size. A compressed copy is uploaded in one request, so a copy larger than 5 GB fails visibly as a failed backup.

### Where backups sit in the bucket

Every database, and every time you turn backups off and on again, gets its own folder named `<database ID, at most 40 characters>-<8 characters of its UID>`, with `-2`, `-3` and so on after re-enabling. Postgres writes `base` and `wals` inside it. MySQL, SQLite and a single MariaDB server write `dumps`. A MariaDB database with high availability writes `physical` and `binlogs`. The folder is inside a folder named for the project ID. The unique name means a new database that reuses an old name never writes into old backups.

### Backups and compliance tags

A project can require compliance node tags, such as `hipaa`. See [Compliance node tags](/docs/guides/compliance-tags/). For a database placed with those tags, the MySQL and SQLite jobs (the daily dump, the final backup and the restore) run in the project's region on nodes that carry the tags, so a copy of the data is handled where the data is allowed. They do not use the database's own **Run on** tags. Postgres backs up from the database's own instance pods, which already run on those nodes. A database that was provisioned before the project required the tags keeps the placement it had, and so do its jobs.

The backups themselves go to your S3 bucket, outside the cluster. Required tags do not reach them. See [Where a database runs](/docs/guides/databases/#where-a-database-runs).

## Restore to a new database

A restore creates a new database. The original is not changed. You check the new database, then move your apps to it.

1. Open the database and go to its **Backups** tab, or to **Settings** and then **Backups** in the database panel.
2. Under **Restore**, choose what to restore to:
   - PostgreSQL: **Restore most recent state**, a **Restore to a specific time** value (a date and time in your local time, inside the stated window) with **Restore to this time**, or a **Restore** button beside a restore point.
   - MySQL, SQLite and MariaDB as one server: **Restore latest backup**, or a **Restore** button beside a restore point.
   - MariaDB with high availability: the same choices as PostgreSQL.
3. In the **Restore to a new database** dialog, confirm. If the original environment no longer exists, choose another **Environment**.
4. Select **Restore**. The dashboard says "Restore started. Your original database is untouched." and opens the new database's **Deployments** tab.

The panel lists the five newest restore points with **Show all N restore points** for the rest. It says "Restore points appear after the first backup finishes" until one exists. A restore point stays available until retention removes it.

The new database:

- has a new resource ID and the display name `<original name>-restored`,
- is in the same project and, unless you chose another, the same environment,
- keeps the original's engine, Postgres version, database name, resources and disk size, and runs one instance even if the original was high availability, except for MariaDB with high availability, whose restore runs as many instances as the original did, because the operator replays the archived logs only on a database that replicates,
- is placed like any new database: in the project's region, on nodes that carry the tags the project requires at that moment. It does not copy the original's **Run on** choice. See [Where a database runs](/docs/guides/databases/#where-a-database-runs),
- has backups on, with the original's retention,
- deploys at once, with no draft, and shows "Restoring database from backup" until it is ready, and
- gets its own password and connection secret, except for MariaDB with high availability: a physical copy holds the logins of the server it came from, so that restore keeps the original's password (the one derived from the original's resource name) and its connection secret holds it. A database restored from a restore keeps the first one's. Restoring into another environment therefore gives it the original's password: change it by hand in the new database if the two should not share one.

Add a reference from your app to the new database and deploy the app to switch over. See [Databases](/docs/guides/databases/#connect-an-app-to-a-database). For Postgres, the new database takes a fresh owner password from the new resource, so the old connection string does not work on it.

To restore through the API, find the archive name in `GET /api/bootstrap` under `backupArchives`, then:

```bash
curl -X POST https://geass.example.com/api/databases/restore \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json" \
  --data-urlencode archive=orders-1a2b3c4d \
  --data-urlencode time=2026-10-11T14:30:00Z
```

Send either `restorePoint` (a restore point ID) or `time` (RFC 3339), never both. With neither, Geass restores the latest state. `environment` is optional. The reply names the new database: `{"ok":true,"name":"<new ID>","kind":"databases"}`.

The command line and the versioned API do the same at `POST /api/v1/databases/restore`. The command line has no restore command of its own, so use `geass api`. The archive names are in the `backupArchives` list that `GET /bootstrap` returns, and in `kubectl get geassbackuparchives -n geass-system`:

```bash
geass api GET /bootstrap | jq -r '.backupArchives.items[].metadata.name'
geass api POST /databases/restore -d '{"archive": "orders-1a2b3c4d", "time": "2026-10-11T14:30:00Z"}'
```

Restoring needs the admin role (Full access). The MCP server has no tool for backups or restores. See [Command line](/docs/guides/command-line/#anything-else-the-raw-api).

The dashboard refuses a restore with one of these messages before it creates anything:

| Message | Cause |
| --- | --- |
| Choose which backups to restore | No `archive` was given |
| These backups are no longer available | The archive was already removed |
| Choose either a restore point or a time, not both | Both were sent |
| This database can only be restored to a restore point | A time was sent for MySQL, SQLite or MariaDB as one server |
| These backups have no restore points yet | The first backup has not finished |
| The chosen restore point is not available | The ID is not in the archive |
| The chosen time is earlier than the oldest restore point | Before the recovery window |
| The chosen time is later than the newest recoverable moment | After the recovery window |
| Enter a valid date and time | The time could not be read |
| A MariaDB database restored from backups of one with high availability has to have high availability too | A manifest asked for `restoreFrom` on a MariaDB database without it. The dashboard makes the restored database replicated by itself |

A restore also needs room on the cluster, like any new database, and Geass refuses it with the usual capacity message when there is none.

A database you create from a manifest with `spec.restoreFrom` is checked by the operator after the object exists, so these messages appear as its Ready condition instead: "Backups from another project cannot be restored here", "A restored database must use the same engine as the original (X)", "A restored database must use Postgres version X, the version of the backup", "A restored database must keep the database name X" and "A restored database needs at least X of storage, the size of the original". The restore the dashboard builds already meets these rules.

## Recover databases from a bucket

The record of a database's backups lives in the cluster, and a cluster can be lost. Beside each database's backups Geass therefore keeps a small file in the bucket, `geass-archive.json`, that says what they are copies of: the engine and version, the size, the database's name, the project and the environment. It holds nothing secret. A new cluster reads these files and offers the databases for restore again. See [Disaster recovery](/docs/operations/disaster-recovery/#recover-databases-from-a-bucket) for how.

What is found there appears under **Recently deleted databases** in the project's **Settings**, **Backups**, marked "Found in the bucket", and is restored like any other. These records are offered for 30 days, and when they expire only the record is removed: Geass never deletes the data in a bucket it found, because the cluster that put it there may still be running.

## Delete a database

When you delete a database that has backups, Geass first takes a final backup so nothing written since the last one is lost. It checks every 10 seconds and waits up to 10 minutes. If the database is not running, the final backup cannot start, or it fails or times out, Geass deletes the database anyway and keeps the backups that already exist. A failing backup never leaves a database stuck deleting.

Geass then keeps the backups for seven days. That window is fixed. To restore a database you deleted:

1. Open the project, then **Settings**, then **Backups**.
2. Under **Recently deleted databases**, find the database. Each row shows its environment and "Deleted · restorable until" a date.
3. Select **Restore** and continue as above.

After seven days, Geass deletes everything under the database's folder in the bucket, including old versions and unfinished uploads, and then the record. If the bucket is already gone, Geass treats it as removed.

A database with no backups, or one whose backups were off or still waiting for storage, is destroyed with its data. The delete section says "This database has no backups. Deleting it permanently destroys its data".

:::caution
Deleting a project deletes the project's AWS connections in the same step, and removes the namespaces its databases run in, so Geass may not manage a final backup either. Geass needs a connection to remove expired backups and to restore, so after a project is deleted its backups stay in your bucket, and Geass can neither restore nor purge them. Restore anything you need before you delete the project, and empty and delete the bucket in AWS yourself when you no longer need the backups.
:::

## Check that backups are healthy

A database's **Backups** panel shows a badge and a message.

| Badge | Message | Meaning |
| --- | --- | --- |
| **Needs storage** | "Choose where backups are stored to protect this database" | The project has no backup storage |
| **Needs storage** | "Backup storage is not working: ..." | Storage is chosen, but the bucket or user could not be set up. The AWS error code and message follow |
| **Starting** | "Taking the first backup" | Storage works and the first backup has not finished |
| **Protected** | "Backups are running" | The latest backup succeeded and archiving, if it applies, works |
| **At risk** | "Recent changes are not being saved to backup storage" | Postgres archiving is failing |
| **At risk** | "The latest backup failed" | A backup failed and no later one succeeded |

When backups are off, the panel shows "Backups are off — this database has no recovery if its data is lost or deleted". A database with no storage or off backups shows **Not backed up** on its card, and one that is at risk shows **Backups at risk**. A card shows "continuous backup" or "daily backup" whenever backups are on, so read the badge, not the card, to know they work.

The panel also shows **Last backup**, and for PostgreSQL "You can restore to any moment since" a date, or for MySQL and SQLite **Oldest backup**. Check that **Last backup** is within the last day. The status refreshes about every five minutes, and Postgres reports a finished backup to Geass about 30 seconds after it ends, so a status can lag.

A backup problem never takes a working database out of **Ready**. Geass logs it and checks again after a minute. Restore errors are different: they fail the new database, because bringing it up is the restore.

Look at the objects directly:

```bash
kubectl get geassbackuparchives -n geass-system
kubectl get geassdatabase orders -n geass-system -o jsonpath='{.status.backup}{"\n"}'

# PostgreSQL
NS=proj-1a2b3c4d-production
kubectl get scheduledbackups.postgresql.cnpg.io,backups.postgresql.cnpg.io -n "$NS"
kubectl get cluster.postgresql.cnpg.io orders -n "$NS" \
  -o jsonpath='{.status.firstRecoverabilityPoint}{"  "}{.status.lastSuccessfulBackup}{"\n"}'

# MySQL and SQLite
kubectl get cronjob,jobs -n "$NS" -l geass.dev/backup-for=orders
```

The archive list shows each database's **Last backup** and **Delete after**. The second column stays empty while the database is alive.

The same summary is in the API. `geass databases get orders` and the MCP tool `get_database` print the database's `status`, whose `backup` field holds the state, the message, the archive name, the last backup time and the start of the recovery window. The bucket and the AWS connection are in the project's `spec.backupStorage`, which `geass projects get` shows only to a token that can change things.

Do a test restore now and then. Restore to a new database, check the data, and delete the new database. A backup you have never restored is unproven.

## Failure modes

| Symptom | Cause | What to do |
| --- | --- | --- |
| **Needs storage** and a card marked **Not backed up** | The project has no backup storage | Choose it under **Settings**, **Backups** |
| "Backup storage is not working: AccessDenied: ..." | The connection's keys cannot create the bucket or the user | Give the keys the permissions listed above, then save storage again |
| "Backup storage is not working" naming a missing connection | The AWS connection was removed or belongs to another project | Recreate it, or choose another connection |
| Stuck on **Starting** | The first backup has not finished. A Postgres base backup of a large database takes time. A MySQL or SQLite copy retries after 10 minutes | Check the jobs and backups with the commands above |
| **At risk**, "Recent changes are not being saved" | Postgres cannot write its log to the bucket | Check the bucket, the IAM user and network access from the nodes |
| **At risk**, "The latest backup failed" | The last backup or dump job failed | Read the job's logs. A MySQL or SQLite copy over 5 GB fails at upload |
| The new database stays at "Restoring database from backup" | The restore is still running | Wait. A Postgres restore replays log from the bucket. If it never ends, check the pods in the namespace |
| "The restore could not be completed. Check that the backups are still available, then restore again" | The MySQL or SQLite restore job failed | Confirm the restore point exists in the bucket, then restore again. Geass loads a copy only once, so a restore never overwrites newer data |
| Backups still in the bucket after the retention period | Removal failed, for example the connection is gone | Fix the connection. Geass retries every 15 minutes, and the archive shows a `Purged` condition of `False` with the reason |
| An old restore point is missing | Retention removed it | Raise **Keep backups for** before you need it |

## Options

Database `spec.backup`:

| Field | Meaning | Default |
| --- | --- | --- |
| `enabled` | Turn backups on | None. Dashboard and API creates set `true` |
| `retentionDays` | How many days back you can restore, 1 to 365 | 7 |

Database `spec.restoreFrom` (set only when the database is created):

| Field | Meaning |
| --- | --- |
| `archive` | The `GeassBackupArchive` to restore from |
| `restorePoint` | A restore point ID. Do not combine with `time` |
| `time` | A moment inside the recovery window (PostgreSQL only) |

Project `spec.backupStorage`: `connectionRef` (the AWS connection), `bucket` (3 to 63 characters) and `region` (default: the connection's region).

## What happens under the hood

- Each database with backups has a `GeassBackupArchive` in `geass-system`. It records the project, environment, database, the unique folder, the storage and the retention, and its status holds the restore points (at most 100, newest first) and the recovery window. The storage on an archive cannot change after it is created.
- The bucket-scoped key lives in the Secret `geass-backup-storage-<bucket>` in `geass-system` and is copied into the database's namespace as `<database ID>-backup-credentials`. During a restore it is also copied as `<database ID>-restore-credentials`.
- For PostgreSQL, Geass sets the database's CloudNativePG cluster to archive to `s3://<bucket>/<project ID>` under the archive's folder name, with gzip compression, and creates a `ScheduledBackup` named after the database with `immediate` set. The schedule is `0 <minute> 2 * * *`. Geass uses CloudNativePG's built-in Barman object-store backup.
- A restore bootstraps a new cluster from the old folder. A restore point is a base backup taken as it ended. A time is passed to Postgres in its own timestamp format with an explicit `+00` offset, and is exact to the second.
- For MySQL and SQLite, a CronJob and one-off Jobs run in the database's namespace: `<archive>-initial`, `<database ID>-backup-final` and `<database ID>-restore`. The dump runs in the database's own image, and a second container uploads the copy using `curlimages/curl:8.10.1`, which signs S3 requests itself.
- A janitor controller is the only thing that deletes backup data. It never touches an archive whose database is alive. If a database disappears without the normal cleanup, it retires the archive with the same seven-day grace period.
- Final backups are bounded: deleting a database waits at most 10 minutes for its last backup.

## Troubleshooting

**The Backups tab is missing from a database.**
The database is not PostgreSQL, MySQL, MariaDB or SQLite running in the cluster. Redis and external databases have no backups.

**"Backups start after you deploy this database."**
The database is still a draft. Deploy it.

**I turned backups on and nothing is stored.**
Check the badge. **Needs storage** means the project has no storage yet. Choose it in **Settings**, **Backups**.

**There is no Restore button.**
Restore points appear after the first backup finishes. Wait for **Last backup** to show a time.

**My restored database has different data than I expected.**
A MySQL or SQLite restore holds the data as of that day's copy. A Postgres "most recent state" restore ends at the last change that reached the bucket, which can trail the moment the data was lost by a few minutes.

**A restored MariaDB sits at "Restoring database from backup", and its server logs "Access denied for user 'root'".**
After six hours it is reported as failed ("The restore could not be completed"). The cause is usually this:
The restore keeps the original's password, which Geass derives from the original's name. If the original's root password was changed by hand, the copy carries that one, and the operator cannot reach the restored server with the one Geass knows. Restore again from a copy made before the change, or change the password back first.

**The first backup of a MariaDB database with high availability takes a minute or two.**
Expected. The database is told to archive its logs, which makes the operator roll its servers, and the first copy waits for it to be ready again. See [MariaDB with high availability](#mariadb-with-high-availability-copies-and-archived-logs).

**A MariaDB database's recovery window ends well before now.**
The window ends where the operator says the archived logs reach. The operator archives every ten minutes, and Geass closes the log every five, so it trails by up to about fifteen. It does not move at all if the logs cannot be archived: check `kubectl -n <namespace> get pointintimerecovery` and the agent's log, `kubectl -n <namespace> logs <name>-0 -c agent`.

**The AWS bill shows a bucket I did not make.**
Geass created it for your project's backup storage, when a database first needed it. Unless you chose a name through the API, it is named `geass-backups-` plus 12 characters. Do not delete it while databases use it.

## Related

- [Databases](/docs/guides/databases/)
- [Storage and volumes](/docs/guides/storage/)
- [Connect AWS](/docs/guides/connectors/aws/)
- [Shared database servers](/docs/guides/shared-database-servers/)
- [Compliance node tags](/docs/guides/compliance-tags/)
- [Command line](/docs/guides/command-line/)
- [Troubleshooting](/docs/operations/troubleshooting/)
