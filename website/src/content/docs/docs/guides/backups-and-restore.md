---
title: "Backups and restore"
description: "Back up Postgres, MySQL, MariaDB, and SQLite databases to an S3 bucket in your own AWS account, restore to a new database, and check backup health."
---

Geass backs up the Postgres, MySQL, MariaDB, and SQLite databases that it runs. The backups go to an S3 bucket in your own AWS account, so you keep the data and you pay AWS directly. If the data of a database is lost or damaged, restore the database as a new database next to the original. Do the same if you delete the database. Geass never restores in place.

Postgres and a MariaDB database with high availability are backed up continuously. You can restore either one to any recent moment. MySQL, SQLite, and a MariaDB database that runs as one server are copied every day. You restore these databases to one of those copies, and you lose the changes made after that copy.

## What is backed up

| Database | Backed up | How | You can restore to |
| --- | --- | --- | --- |
| PostgreSQL running in the cluster | Yes | Continuous archiving and a daily base backup | Any moment in the recovery window, or a daily restore point |
| MySQL running in the cluster | Yes | A compressed SQL dump every day | A daily restore point |
| MariaDB with high availability | Yes | A physical copy every night, and the binary logs archived beside it | Any moment in the recovery window, or a nightly restore point |
| MariaDB as one server | Yes | A compressed SQL dump every day | A daily restore point |
| SQLite running in the cluster | Yes | A compressed SQL dump every day | A daily restore point |
| Redis | No. It is a cache with no persistence | None | Not applicable |
| External databases (AWS RDS, PlanetScale) | No | Use the backups of your provider | Not applicable |
| Shared database servers and their logical databases | No | None | Not applicable |
| Buckets | No | See [Object storage](/docs/guides/object-storage/) | Not applicable |
| Pull request environment copies | No. They are throwaway | None | Not applicable |

New PostgreSQL, MySQL, MariaDB, and SQLite databases start with backups on and a 7-day retention. This applies when you create them in the dashboard, through the REST API, or from a template. Databases created from a manifest, and databases that existed before backups did, start with backups off. You turn backups on for them yourself. A database with backups on protects nothing until its project has somewhere to store the backups.

## Before you start

1. **An AWS connection for the project.** Add one under **Settings**, **Connectors**. A connection belongs to one project. See [Connect AWS](/docs/guides/connectors/aws/).
2. **Backup storage chosen for the project.** When you choose storage, Geass records the bucket and the connection. Geass creates the bucket when the first database with backups on needs it. See the next section.

The AWS keys must be able to create an S3 bucket and manage a restricted IAM user. The permissions are listed under [What Geass does in your AWS account](#what-geass-does-in-your-aws-account).

## Choose where backups are stored

You choose storage one time for each project. Every database in the project uses that storage. Geass creates the bucket and a restricted user the first time that a database in the project needs them.

1. Open the project and select **Settings**, then **Backups**.
2. Under **Backup storage**, select an **AWS connection**. If the project has no connection, the page says "Connect an AWS account first" and links to **Connectors**.
3. Click **Use this connection**. The dashboard confirms with "Saved".

The same chooser appears on the **Backups** tab of a database, under **Choose where backups are stored**, when the database needs storage. After you save, the page shows the **AWS connection**, the **Bucket**, and the **Region**, if one was set. The page also shows the **Change** and **Remove** buttons.

Your storage choice takes effect immediately. Databases that already have backups continue to write to the storage that they started with, even if you change the choice for the project later. **Remove** leaves stored backups where they are. Databases that have no backups yet cannot start backups until you choose storage again.

If you give no other input, Geass names the bucket `geass-backups-` followed by 12 random hexadecimal characters. Bucket names are global across AWS, so a random name prevents collisions. The region of the bucket is the region of the connection.

With the REST API, you can also choose the bucket name and the region. Use a workspace token with the admin role:

```bash
curl -X POST https://geass.example.com/api/projects/proj-1a2b3c4d/backup-storage \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json" \
  --data-urlencode connection=prod-aws \
  --data-urlencode bucket=acme-storefront-backups \
  --data-urlencode region=eu-west-1
```

To clear the choice, send `remove=on` instead. If you save again with the same connection and no `bucket`, Geass keeps the existing bucket. A different connection is a different AWS account, so Geass generates a new bucket name. The manifest field of the project is `spec.backupStorage`, with `connectionRef`, `bucket`, and `region`. Viewers cannot see which bucket a project uses.

:::caution
Use the generated name unless you know that the name is yours. Geass treats an "already exists" answer from S3 as success. If you choose a bucket name through the API and another AWS account already owns that bucket, Geass reports the bucket as created. Backups then fail with an access error.
:::

## What Geass does in your AWS account

Geass uses the keys of the connection the first time that a database needs backups. Geass uses them again when the stored credentials are missing, and when the storage choice changes. With these keys, Geass does these actions:

- Creates the S3 bucket, if it does not exist.
- Creates an IAM user named `geass-` plus the bucket name, with an inline policy `geass-bucket` that allows only that bucket.
- Creates one access key for that user.

Databases use the bucket-scoped key. The keys of the connection never leave `geass-system`. Geass sets no other bucket property. Geass sends only the create request, so the AWS defaults for a new bucket apply. If you turn on versioning yourself, Geass removes old object versions and unfinished multipart uploads in these cases:

- When Geass removes an expired MySQL or SQLite copy.
- When Geass purges the backups of a deleted database.

The keys of the connection need permission for these actions:

- The IAM actions `CreateUser`, `PutUserPolicy`, `CreateAccessKey`, `ListAccessKeys`, `DeleteAccessKey`, `DeleteUserPolicy`, and `DeleteUser`.
- S3 bucket creation.
- S3 object listing, including versions and multipart uploads.
- S3 object and version deletion.
- S3 multipart abort.

## Turn backups on, off, or change retention

1. Open the database. The **Backups** tab and the **Backups** section of **Settings** show the same panel.
2. Turn **Back up this database** on or off.
3. In **Keep backups for**, select 7, 14, 30, or 90 days. A value set elsewhere also appears in the list.
4. Click **Save changes**.

These changes apply immediately. They do not wait for a deploy, and they never restart the database. **Keep backups for** sets how far back you can restore. Longer periods use more storage in your AWS account.

Through the API, send `backups=on` or `backups=off`, and `backupRetentionDays` (1 to 365), to `POST /api/databases/<id>/update`. In a manifest, set `spec.backup`:

```yaml title="orders-db.yaml"
spec:
  backup:
    enabled: true
    retentionDays: 14
```

When you turn backups off, the schedule stops. For Postgres, archiving also stops. Geass keeps the backups that it already has for seven days, and then removes them. The panel says "Backups from before they were turned off are kept until" and the date. If you turn backups on again later, a new set starts.

Retention is 1 to 365 days, and the default is 7. If you lower the retention, Geass can remove backups that are older than the new period.

## How backups work

### PostgreSQL: continuous

- Geass makes Postgres copy its write-ahead log to your bucket continuously. Geass also makes Postgres take a full base backup every day.
- The daily backup runs at about 02:00 UTC. The minute comes from the UID of the database, so the databases do not all start together. The first base backup runs immediately after storage is ready.
- A **restore point** is a base backup. You can restore to a restore point, to the most recent state, or to any time in the recovery window.
- You can restore a base backup only if every log file from its start to its end is in the bucket. Geass checks this condition. Geass lists a backup as a restore point only when the condition is true.
- A backup taken from a standby has a special last file: that file is still being written when the backup finishes. A quiet database archives that file after about five minutes. So Geass trusts a new backup for seven minutes and checks it after that. Geass looks at the database every minute until it has checked the backup.
- The first backup after you choose storage can come before the database starts to save its log files. The login that saves the log files is made a moment later. In this case:
  - Geass does not list that backup.
  - The status says "A backup was taken before the database could save its log files, so it cannot be restored. A new one is being taken".
  - Geass takes another backup after a log file reaches the bucket, up to three times.
- A restore is pinned to a backup that passes the check. So a restore does not pick a backup that cannot be restored.
- If the new database cannot start because of a backup, its status says what the restore reported. The status does not stay on "Restoring database from backup".
- The recovery window starts at the oldest retained base backup. The window ends about five minutes before now, because the newest changes might not have reached the bucket yet. The window stops moving if archiving fails.
- Retention keeps the base backups and the log that you need to restore to any moment in the last N days.

### MariaDB with high availability: copies and archived logs

- The MariaDB operator takes a physical copy of a replica every night at about 02:00 UTC, with `mariadb-backup`.
- The operator takes the first copy a minute or two after backups are set up. The operator waits until the database has started to archive its logs and is ready again. The reason is this:
  - An instruction to archive makes the operator roll the servers and move the primary.
  - A copy taken before that is of a primary whose logs were never archived.
  - A restore to a moment could not replay the logs for that copy.
- Each copy is compressed and uploaded to the bucket as `<project ID>/<folder>/physical/physicalbackup-<UTC date and time>.xb.gz`. The copies in the bucket are the restore points. The operator itself removes copies that are older than the retention.
- The binary logs of the primary are archived to `<project ID>/<folder>/binlogs/`. A log is archived only after the server has closed it, and the operator archives every ten minutes. So Geass runs a small job, `<database ID>-binlog-flush`, every five minutes to close the log.
- The recovery window ends where the operator says that the archived logs reach. So you can restore a database to a moment about fifteen minutes ago, but not to a moment one second ago.
- The operator never removes an archived log. So Geass removes the logs that no kept copy can use: a log that was last written more than an hour before the oldest copy began.
  - Geass does this one time each day, in the hour after the nightly copy (03:00 UTC). The reason is that Geass lists the logs in full, and there is one log for every five minutes of the retention.
  - Geass never removes the inventory file. The restore of the operator does not read that file for logs that end before a copy.
  - Geass removes nothing while there is no copy.
- A copy taken from a primary that was replaced later does not reach the logs of the new primary. When the primary changes, Geass asks for another copy from the new primary, so the window continues to move. The moments between the last log archived from the old primary and that new copy are not recoverable.
- The copy runs next to the replica that it copies. The copy uses a staging volume as large as the database, on the storage class of the database. The operator makes this volume the first time that it needs the volume, and keeps it. The volume is removed with the database. The final copy taken on delete uses a staging volume in the same way. So in a project that requires node tags, the final copy stays on the nodes that carry the tags.
- In a project that requires compliance node tags, the operator makes the job of the copy without a node selector. On Kubernetes 1.36 or later, a policy gives the job the node tags of the project. The policy does this for every pod that the operator makes. On an older cluster, the policy that checks refuses the job, and the backups say that they are at risk. See [Compliance node tags](/docs/guides/compliance-tags/).
- A backup that failed is reported as **At risk** until a later backup works.
- A restore starts a new database from the copy nearest before the moment that you chose. Then the operator replays the logs up to that moment.
  - The new database must be replicated like the original, and it keeps the password of the original.
  - The moment must be inside the window. The window goes from the oldest restore point to the last moment that the logs reach. The operator reports that last moment. It is not the time of the newest copy.
  - If the logs do not reach the chosen moment, a restore to that moment fails. The restore does not come up with older data.
  - A restore of a few rows took about 90 seconds.
- A restore point is one copy. A restore to a restore point asks for the moment when that copy was taken.

### MySQL, MariaDB and SQLite: daily copies

- A Kubernetes CronJob named `<database ID>-backup` runs every day at about 02:00 UTC. The CronJob uses the `Etc/UTC` time zone. Geass takes a first copy immediately after setup, so the database does not stay unprotected until the first night. If that first copy fails, Geass starts another copy after 10 minutes, while no copy exists.
- MariaDB is dumped the same way, with `mariadb-dump` inside its own image. MariaDB is restored with `mariadb`. A job waits until it can reach the database before it starts. The reason is that a MariaDB client stops trying on a connection that the network of a new pod has not allowed yet.
- A job that dumps or loads a database first waits, for up to a minute, until the server accepts a connection. The reasons are these:
  - The first connection of a new pod can be refused for a moment, while the network policy that lets the pod reach the server is applied.
  - K3s enforces that network policy.
  - The first backup and the final backup on delete both run in new pods.
  - The dump tools do not retry by themselves.
- MySQL is dumped with `mysqldump` inside the image of the database, with a consistent view and with no table locks. The dump includes routines, events, and triggers. The dump contains only the main database of the server. So the copy does not contain the logical databases created on the own MySQL server of a project, or users and grants.
- SQLite runs on rqlite, which serves a SQL dump over HTTP. Geass saves that dump.
- Each copy is compressed and uploaded to the bucket as `<project ID>/<folder>/dumps/<UTC date and time>.sql.gz`. The bucket listing is the source of truth for restore points.
- Geass removes copies that are older than the retention period. Geass never removes the newest copy, even a very old one.
- A dump job has one retry and one hour to finish. Its scratch space is limited to twice the size of the database. A compressed copy is uploaded in one request. So a copy larger than 5 GB fails visibly, as a failed backup.

### Where backups sit in the bucket

Every database gets its own folder. Every time that you turn backups off and on again, the database also gets its own folder. The folder name is `<database ID, at most 40 characters>-<8 characters of its UID>`. After you turn backups on again, the name ends with `-2`, `-3`, and so on.

- Postgres writes `base` and `wals` inside the folder.
- MySQL, SQLite, and a single MariaDB server write `dumps`.
- A MariaDB database with high availability writes `physical` and `binlogs`.

The folder is inside a folder named for the project ID. Because the name is unique, a new database that reuses an old name never writes into old backups.

### Backups and compliance tags

A project can require compliance node tags, such as `hipaa`. See [Compliance node tags](/docs/guides/compliance-tags/). For a database placed with those tags, the MySQL and SQLite jobs run in the region of the project, on nodes that carry the tags. These jobs are the daily dump, the final backup, and the restore. So a copy of the data is handled where the data is allowed. The jobs do not use the **Run on** tags of the database. Postgres backs up from the instance pods of the database, which already run on those nodes. A database that was provisioned before the project required the tags keeps the placement that it had. Its jobs also keep that placement.

The backups themselves go to your S3 bucket, outside the cluster. Required tags do not reach them. See [Where a database runs](/docs/guides/databases/#where-a-database-runs).

## Restore to a new database

A restore creates a new database. The original does not change. You check the new database, and then you move your apps to it.

1. Open the database and go to its **Backups** tab. You can also go to **Settings** and then **Backups** in the database panel.
2. Under **Restore**, select what to restore to:
   - PostgreSQL: click **Restore most recent state**, or click a **Restore** button beside a restore point. To restore to a time, enter a value in **Restore to a specific time**, and then click **Restore to this time**. The value is a date and time in your local time, inside the stated window.
   - MySQL, SQLite, and MariaDB as one server: click **Restore latest backup**, or click a **Restore** button beside a restore point.
   - MariaDB with high availability: the same choices as PostgreSQL.
3. In the **Restore to a new database** dialog, confirm. If the original environment no longer exists, select another **Environment**.
4. Click **Restore**. The dashboard says "Restore started" and opens the **Deployments** tab of the new database. Your original database is not changed.

The panel lists the five newest restore points. **Show all N restore points** shows the rest. Until a restore point exists, the panel says "Restore points appear after the first backup finishes". A restore point stays available until retention removes it.

The new database has these properties:

- It has a new resource ID and the display name `<original name>-restored`.
- It is in the same project. It is also in the same environment, unless you chose another one.
- It keeps the engine, the Postgres version, the database name, the resources, and the disk size of the original.
- It runs one instance, even if the original had high availability. The exception is MariaDB with high availability. That restore runs as many instances as the original did, because the operator replays the archived logs only on a database that replicates.
- It is placed like any new database: in the region of the project, on nodes that carry the tags that the project requires at that moment. It does not copy the **Run on** choice of the original. See [Where a database runs](/docs/guides/databases/#where-a-database-runs).
- It has backups on, with the retention of the original.
- It deploys immediately, with no draft. It shows "Restoring database from backup" until it is ready.
- It gets its own random password and connection secret. The exception is MariaDB with high availability, which the next paragraph describes.

A physical MariaDB copy holds the logins of the server that it came from. So a restore of MariaDB with high availability keeps the password of the original, and its connection secret holds that password. Geass keeps that password in a Secret that goes with the backups and lives as long as the backups do. So the password is available when the original database is gone. For a database made by an earlier release, the password is the one derived from the resource name of the original (`<name>-password`). A database restored from a restore keeps the password of the first database. So a restore into another environment gets the password of the original. If you do not want the two databases to share a password, change it manually in the new database.

To switch over, add a reference from your app to the new database, and deploy the app. See [Databases](/docs/guides/databases/#connect-an-app-to-a-database). For Postgres, the new database takes a new owner password from the new resource. So the old connection string does not work on the new database.

To restore through the API, find the archive name in `GET /api/bootstrap` under `backupArchives`. Then send this request:

```bash
curl -X POST https://geass.example.com/api/databases/restore \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json" \
  --data-urlencode archive=orders-1a2b3c4d \
  --data-urlencode time=2026-10-11T14:30:00Z
```

Send either `restorePoint` (a restore point ID) or `time` (RFC 3339), never both. If you send neither, Geass restores the latest state. `environment` is optional. The reply names the new database: `{"ok":true,"name":"<new ID>","kind":"databases"}`.

The command line and the versioned API do the same at `POST /api/v1/databases/restore`. The command line has no restore command of its own, so use `geass api`. The archive names are in the `backupArchives` list that `GET /bootstrap` returns, and in `kubectl get geassbackuparchives -n geass-system`:

```bash
geass api GET /bootstrap | jq -r '.backupArchives.items[].metadata.name'
geass api POST /databases/restore -d '{"archive": "orders-1a2b3c4d", "time": "2026-10-11T14:30:00Z"}'
```

To restore, you need the admin role (Full access). The MCP server has no tool for backups or restores. See [Command line](/docs/guides/command-line/#anything-else-the-raw-api).

Before the dashboard creates anything, it can refuse a restore with one of these messages:

| Message | Cause |
| --- | --- |
| Choose which backups to restore | No `archive` was given |
| These backups are no longer available | The archive was already removed |
| Choose either a restore point or a time, not both | Both were sent |
| This database can only be restored to a restore point | A time was sent for MySQL, SQLite, or MariaDB as one server |
| These backups have no restore points yet | The first backup has not finished |
| The chosen restore point is not available | The ID is not in the archive |
| The chosen time is earlier than the oldest restore point | Before the recovery window |
| The chosen time is later than the newest recoverable moment | After the recovery window |
| Enter a valid date and time | The time could not be read |
| A MariaDB database restored from backups of one with high availability has to have high availability too | A manifest asked for `restoreFrom` on a MariaDB database without high availability. The dashboard makes the restored database replicated automatically |

A restore also needs room on the cluster, like any new database. If there is no room, Geass refuses the restore with the usual capacity message.

If you create a database from a manifest with `spec.restoreFrom`, the operator checks it after the object exists. So these messages appear as its Ready condition instead:

- "Backups from another project cannot be restored here"
- "A restored database must use the same engine as the original (X)"
- "A restored database must use Postgres version X, the version of the backup"
- "A restored database must keep the database name X"
- "A restored database needs at least X of storage, the size of the original"

The restore that the dashboard builds already meets these rules.

## Recover databases from a bucket

The cluster holds the record of the backups of a database, and you can lose a cluster. So Geass keeps a small file, `geass-archive.json`, in the bucket beside the backups of each database. The file says what the backups are copies of: the engine and version, the size, the name of the database, the project, and the environment.

For a MariaDB database with high availability, the file also holds the password of the server that the copies were taken from. A restored copy holds the logins of that server, and it cannot start without that password. The copies themselves hold the same logins. So read access to the folder in the bucket opens that database, as it opens the copies. For every other database, the file holds nothing secret.

A new cluster reads these files and offers the databases for restore again. To learn how, see [Disaster recovery](/docs/operations/disaster-recovery/#recover-databases-from-a-bucket).

The databases found in the bucket appear under **Recently deleted databases** in **Settings**, **Backups** of the project, marked "Found in the bucket". You restore them like any other database. These records are offered for 30 days. When they expire, only the record is removed. Geass never deletes the data in a bucket that it found, because the cluster that put the data there might still be running.

## Delete a database

When you delete a database that has backups, Geass first takes a final backup, so nothing written after the last backup is lost. Geass checks every 10 seconds and waits up to 10 minutes. In these cases, Geass deletes the database anyway and keeps the backups that already exist:

- The database is not running.
- The final backup cannot start.
- The final backup fails or times out.

A backup that fails never leaves a database stuck in deletion.

Geass then keeps the backups for seven days. That period is fixed. To restore a database that you deleted:

1. Open the project, then **Settings**, then **Backups**.
2. Under **Recently deleted databases**, find the database. Each row shows its environment and "Deleted · restorable until" a date.
3. Click **Restore**, and continue as in the restore steps earlier on this page.

After seven days, Geass deletes everything under the folder of the database in the bucket, including old versions and unfinished uploads. Then Geass deletes the record. If the bucket is already gone, Geass treats the data as removed.

A database with no backups is destroyed with its data. This also applies to a database whose backups were off or still waiting for storage. The delete section says "This database has no backups. Deleting it permanently destroys its data".

:::caution
Restore anything that you need before you delete the project. When you no longer need the backups, empty and delete the bucket in AWS yourself. When you delete a project, Geass deletes the AWS connections of the project in the same step. Geass also removes the namespaces that the databases of the project run in, so Geass might not manage a final backup. Geass needs a connection to remove expired backups and to restore. So after you delete a project, its backups stay in your bucket, and Geass can neither restore nor purge them.
:::

## Check that backups are healthy

The **Backups** panel of a database shows a badge and a message.

| Badge | Message | Meaning |
| --- | --- | --- |
| **Needs storage** | "Choose where backups are stored to protect this database" | The project has no backup storage |
| **Needs storage** | "Backup storage is not working: ..." | Storage is chosen, but the bucket or user could not be set up. The AWS error code and message follow |
| **Starting** | "Taking the first backup" | Storage works and the first backup has not finished |
| **Protected** | "Backups are running" | The latest backup succeeded and archiving, if it applies, works |
| **At risk** | "Recent changes are not being saved to backup storage" | Postgres archiving is failing |
| **At risk** | "The latest backup failed" | A backup failed and no later one succeeded |

When backups are off, the panel shows "Backups are off — this database has no recovery if its data is lost or deleted". A database with no storage, or with backups off, shows **Not backed up** on its card. A database that is at risk shows **Backups at risk**. A card shows "continuous backup" or "daily backup" whenever backups are on. So to know that backups work, read the badge, not the card.

The panel also shows **Last backup**. For PostgreSQL, the panel shows "You can restore to any moment since" and a date. For MySQL and SQLite, the panel shows **Oldest backup**. Make sure that **Last backup** is within the last day. The status refreshes about every five minutes. Postgres reports a finished backup to Geass about 30 seconds after the backup ends. So a status can lag.

A backup problem never takes a working database out of **Ready**. Geass logs the problem and checks again after a minute. Restore errors are different: they fail the new database, because the restore is what brings the new database up.

To look at the objects directly, run these commands:

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

The archive list shows **Last backup** and **Delete after** for each database. The **Delete after** column stays empty while the database is alive.

The API has the same summary. `geass databases get orders` and the MCP tool `get_database` print the `status` of the database. Its `backup` field holds the state, the message, the archive name, the last backup time, and the start of the recovery window. The bucket and the AWS connection are in `spec.backupStorage` of the project. `geass projects get` shows that field only to a token that can change things.

Do a test restore from time to time. Restore to a new database, check the data, and delete the new database. A backup that you have never restored is unproven.

## Failure modes

| Symptom | Cause | What to do |
| --- | --- | --- |
| **Needs storage** and a card marked **Not backed up** | The project has no backup storage | Choose storage under **Settings**, **Backups** |
| "Backup storage is not working: AccessDenied: ..." | The keys of the connection cannot create the bucket or the user | Give the keys the permissions listed earlier on this page, and then save storage again |
| "Backup storage is not working" naming a missing connection | The AWS connection was removed or belongs to another project | Recreate the connection, or select another connection |
| Stuck on **Starting** | The first backup has not finished. A Postgres base backup of a large database takes time. A MySQL or SQLite copy retries after 10 minutes | Check the jobs and backups with the commands earlier on this page |
| **At risk**, "A backup was taken before the database could save its log files, so it cannot be restored. A new one is being taken" | The first Postgres backup came before archiving worked | Wait. Geass takes another backup when a log file has reached the bucket, up to three times. If the status stays, check the archive with the commands earlier on this page |
| **At risk**, "Recent changes are not being saved" | Postgres cannot write its log to the bucket | Check the bucket, the IAM user, and network access from the nodes |
| **At risk**, "The latest backup failed" | The last backup or dump job failed | Read the logs of the job. A MySQL or SQLite copy over 5 GB fails at upload |
| The new database stays at "Restoring database from backup" | The restore is still running | Wait. A Postgres restore replays log from the bucket. If the restore never ends, check the pods in the namespace |
| "The restore could not be completed. Check that the backups are still available, then restore again" | The MySQL or SQLite restore job failed | Make sure that the restore point exists in the bucket, and then restore again. Geass loads a copy only one time, so a restore never overwrites newer data |
| Backups still in the bucket after the retention period | Removal failed, for example because the connection is gone | Fix the connection. Geass retries every 15 minutes, and the archive shows a `Purged` condition of `False` with the reason |
| An old restore point is missing | Retention removed it | Raise **Keep backups for** before you need the restore point |

## Options

Database `spec.backup`:

| Field | Meaning | Default |
| --- | --- | --- |
| `enabled` | Turn backups on | None. Databases created in the dashboard or through the API get `true` |
| `retentionDays` | How many days back you can restore, 1 to 365 | 7 |

Database `spec.restoreFrom`. You set it only when you create the database:

| Field | Meaning |
| --- | --- |
| `archive` | The `GeassBackupArchive` to restore from |
| `restorePoint` | A restore point ID. Do not combine with `time` |
| `time` | A moment inside the recovery window (PostgreSQL only) |

Project `spec.backupStorage` has these fields:

- `connectionRef`: the AWS connection.
- `bucket`: 3 to 63 characters.
- `region`: the default is the region of the connection.

## What happens under the hood

- Each database with backups has a `GeassBackupArchive` in `geass-system`. The archive records the project, the environment, the database, the unique folder, the storage, and the retention. Its status holds the restore points (at most 100, newest first) and the recovery window. The storage on an archive cannot change after the archive is created.
- The bucket-scoped key is in the Secret `geass-backup-storage-<bucket>` in `geass-system`. Geass copies the key into the namespace of the database as `<database ID>-backup-credentials`. During a restore, Geass also copies the key as `<database ID>-restore-credentials`.
- For PostgreSQL, Geass sets the CloudNativePG cluster of the database to archive to `s3://<bucket>/<project ID>`, under the folder name of the archive, with gzip compression. Geass also creates a `ScheduledBackup` named after the database, with `immediate` set. The schedule is `0 <minute> 2 * * *`. Geass uses the built-in Barman object-store backup of CloudNativePG.
- A restore bootstraps a new cluster from the old folder. A restore point is a base backup taken as it ended. Geass passes a time to Postgres in the timestamp format of Postgres, with an explicit `+00` offset. The time is exact to the second.
- For MySQL and SQLite, a CronJob and one-off Jobs run in the namespace of the database: `<archive>-initial`, `<database ID>-backup-final`, and `<database ID>-restore`. The dump runs in the image of the database. A second container uploads the copy with `curlimages/curl:8.10.1`, which signs S3 requests itself.
- A janitor controller is the only thing that deletes backup data. The janitor never touches an archive whose database is alive. If a database disappears without the normal cleanup, the janitor retires the archive with the same seven-day grace period.
- Final backups are bounded: when you delete a database, Geass waits at most 10 minutes for its last backup.

## Troubleshooting

**The Backups tab is missing from a database.**
The database is not PostgreSQL, MySQL, MariaDB, or SQLite running in the cluster. Redis and external databases have no backups.

**"Backups start after you deploy this database."**
The database is still a draft. Deploy it.

**I turned backups on and nothing is stored.**
Check the badge. **Needs storage** means that the project has no storage yet. Choose storage in **Settings**, **Backups**.

**There is no Restore button.**
Restore points appear after the first backup finishes. Wait until **Last backup** shows a time.

**My restored database has different data than I expected.**
A MySQL or SQLite restore holds the data as of the copy of that day. A Postgres "most recent state" restore ends at the last change that reached the bucket. That change can be a few minutes before the moment when the data was lost.

**A restored MariaDB sits at "Restoring database from backup", and its server logs "Access denied for user 'root'".**
After six hours, the restore is reported as failed ("The restore could not be completed"). The usual cause is this:
The restore keeps the password of the original, which Geass keeps in a Secret with the backups. If the root password of the original was changed manually, the copy carries the changed password. Then the operator cannot reach the restored server with the password that Geass knows. Restore again from a copy made before the change, or first change the password back.

**The first backup of a MariaDB database with high availability takes a minute or two.**
This is expected. The database is told to archive its logs, and this instruction makes the operator roll its servers. The first copy waits until the database is ready again. See [MariaDB with high availability](#mariadb-with-high-availability-copies-and-archived-logs).

**A MariaDB database's recovery window ends well before now.**
The window ends where the operator says that the archived logs reach. The operator archives every ten minutes, and Geass closes the log every five minutes. So the window trails by up to about fifteen minutes. The window does not move at all if the logs cannot be archived. In that case, check `kubectl -n <namespace> get pointintimerecovery` and the log of the agent, `kubectl -n <namespace> logs <name>-0 -c agent`.

**The AWS bill shows a bucket I did not make.**
Geass created the bucket for the backup storage of your project, when a database first needed it. Unless you chose a name through the API, the name is `geass-backups-` plus 12 characters. Do not delete the bucket while databases use it.

## Related

- [Databases](/docs/guides/databases/)
- [Storage and volumes](/docs/guides/storage/)
- [Connect AWS](/docs/guides/connectors/aws/)
- [Shared database servers](/docs/guides/shared-database-servers/)
- [Compliance node tags](/docs/guides/compliance-tags/)
- [Command line](/docs/guides/command-line/)
- [Troubleshooting](/docs/operations/troubleshooting/)
