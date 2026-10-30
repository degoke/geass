---
title: "Databases"
description: "Create PostgreSQL, MySQL, MariaDB, Redis, and SQLite databases, connect apps, run queries, resize storage, set up high availability, and use external ones."
---

A database in Geass belongs to one project environment. Geass runs the database on your servers, creates its login, and gives your apps its connection string. You can do these things:

- Run PostgreSQL, MySQL, MariaDB, Redis, or SQLite on your servers.
- Connect an app to a database that you already host somewhere else, for example on AWS RDS.
- Have Geass create a database in your PlanetScale organization.

![A database's panel, with its details, backups, and connect string.](../../../../assets/docs/database-drawer.webp)
*Screenshot with example data.*

This page covers one database for each resource. To share one server between many projects, see [Shared database servers](/docs/guides/shared-database-servers/). For backups, see [Backups and restore](/docs/guides/backups-and-restore/). For disks, see [Storage and volumes](/docs/guides/storage/).

## Before you start

- You need a project and an environment. See [Projects and environments](/docs/guides/projects-and-environments/).
- The cluster needs room for the database. Geass refuses the deploy when no server can fit the database. If the project requires node tags, the room must be on servers that carry those tags. See [Where a database runs](#where-a-database-runs).
- The first PostgreSQL database, the first MariaDB database, and the first highly available MySQL database each take a few extra minutes. Geass sets up the software that runs that engine the first time.
- For backups, the project needs an AWS connection. For Geass to create a PlanetScale database, the project needs a PlanetScale connection. See [Cloud connectors](/docs/guides/connectors/).
- To resize a disk later, the cluster needs replicated storage. See [Storage and volumes](/docs/guides/storage/).

## Engines

| Engine | Version | Default size | Disk | Backups |
| --- | --- | --- | --- | --- |
| PostgreSQL | 16 by default. You can set another version | 0.25 CPU, 512 MB | 10 GiB | Continuous, restore to any moment |
| MySQL | 8.4 | 0.25 CPU, 512 MB (1 GB with high availability) | 10 GiB | Daily copy |
| MariaDB | 11.8 | 0.25 CPU, 512 MB | 10 GiB | One server: daily copy. High availability: continuous |
| Redis | 7.4 | 0.25 CPU, 512 MB | None | None |
| SQLite | Served by rqlite 8.36 | 0.1 CPU, 128 MB | 1 GiB | Daily copy |

Things to know about each engine:

- **PostgreSQL** is the only engine that uses the **Version** field. The field is the tag of the Postgres image, and Geass does not check it. Use a version that exists. Geass does not move data between major versions, so select the version before the first deploy.
- **MySQL and Redis** run the versions that Geass pins. The **Version** field has no effect on them.
- **MariaDB** is a separate engine from MySQL. Use it for software that asks for MariaDB. You cannot change the engine after you create the database. MariaDB speaks the MySQL protocol, so a MySQL driver connects to it, and its connection string starts with `mysql://`.
- **Redis** keeps its data in memory. It has no disk, no size setting, and no backups. A restart empties it, so use Redis as a cache.
- **SQLite** runs as rqlite, one instance on one disk. Apps talk to it over HTTP on port 4001 with an rqlite client. They do not open a file. It has no login, so the connection string is a plain `http://` address.
- **CPU and memory** are one setting. A database never uses more than it reserved. Select CPU from 0.1, 0.25, 0.5, 1, or 2. Select memory from 128 MB, 256 MB, 512 MB, 1 GB, 2 GB, or 4 GB.

:::note
The dashboard shows a version beside the engine of every database. A MySQL, MariaDB, Redis, or SQLite database shows `16`, for example `mysql 16` on its card. The API and the command line report the same value. This number is the default of the **Version** field, not the version of the engine.
:::

## Create a database

1. Open the project and environment.
2. Click **+ Add resource**, and then select **Database**.
3. Select **Database server**, and then select **PostgreSQL**, **MySQL**, **MariaDB**, **Redis**, or **SQLite**. Geass saves a draft and opens its **Settings**.

   To use a server that already runs, select **Logical database** instead. See [Shared database servers](/docs/guides/shared-database-servers/).
4. In **Database**, set the **Version** (PostgreSQL only), the **CPU** and **Memory** under **Resources**, and the **Size** under **Storage**.
5. In **Backups**, review the defaults.
6. If your servers have tags, set **Run on** in **Scale**. You cannot change this choice after the first deploy. See [Where a database runs](#where-a-database-runs).
7. Click **Save changes**. Each **Save changes** button saves all the values on the page.
8. In the top bar of the project, click **Deploy changes**. The **N pending** button beside it lists what Geass deploys.

The status moves from **Draft** to **Setting up**, and then to **Ready**. **Unavailable** or **Failed** means that something stopped the database. The **Deployments** tab shows each attempt and its errors. See [Troubleshooting](#troubleshooting).

A new database starts with these values:

| Setting | Default |
| --- | --- |
| Name | A generated two-word resource ID, for example `falling-mist` |
| Database name | The resource ID |
| Login | `app` |
| CPU and memory | 0.25 CPU and 512 MB (SQLite: 0.1 CPU and 128 MB) |
| Disk | 10 GiB (SQLite: 1 GiB, Redis: none) |
| Backups | On for PostgreSQL, MySQL, MariaDB, and SQLite, kept 7 days. Backups protect nothing until the project has backup storage |
| Instances | 1 |

To rename the database, edit its title. This change affects the display name only. The resource ID does not change, and the API and the command line use the resource ID.

### Create a database with the REST API

Use a workspace token with the admin role. Project tokens cannot create resources. See [Users and tokens](/docs/guides/users-and-tokens/). The request creates a draft, so deploy it afterwards.

```bash
curl -X POST https://geass.example.com/api/databases/create \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json" \
  --data-urlencode project=proj-1a2b3c4d \
  --data-urlencode environment=production \
  --data-urlencode name=orders \
  --data-urlencode engine=Postgres \
  --data-urlencode storageSize=20Gi \
  --data-urlencode cpu=500m \
  --data-urlencode memory=1Gi

curl -X POST https://geass.example.com/api/databases/orders/deploy \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json"
```

The project ID is in the address of the project pages (`/projects/proj-1a2b3c4d`). The first response is `{"ok":true,"name":"orders","kind":"databases"}`.

From the command line, `geass api` calls the versioned API with JSON bodies:

```bash
geass api POST /databases -d '{"project": "proj-1a2b3c4d", "environment": "production", "name": "orders", "engine": "Postgres", "storageSize": "20Gi"}'
geass api POST /databases/orders/deploy
```

These rules apply to both APIs:

- The resource ID (`name`) must be unique in the cluster. If you leave it out, Geass generates one and returns it.
- `cpu` takes `100m`, `250m`, `500m`, `1`, or `2`. `memory` takes `128Mi`, `256Mi`, `512Mi`, `1Gi`, `2Gi`, or `4Gi`.
- Send both `cpu` and `memory`. If you send only one, the other falls back to `100m` or `128Mi`, not to the default of the engine.
- A new database starts with backups on and a 7-day retention, whatever you send. To change them afterwards, see [Turn backups on, off, or change retention](/docs/guides/backups-and-restore/#turn-backups-on-off-or-change-retention).

See [Options](#options), the [REST API reference](/docs/reference/rest-api/#databases-logical-databases-and-buckets), and the [command line](/docs/guides/command-line/#anything-else-the-raw-api).

## Connect an app to a database

To give an app the connection string of a database:

1. Open the app and select **Variables**.
2. In the hint "Trying to connect a database?", click **Add Reference**.
3. In **Add reference**, select the database under **Resource**. A database that is not ready shows "not ready" and you cannot select it.
4. Click **Add reference**. The dashboard says "Connection added".
5. Deploy the app.

The reference adds the variable `DATABASE_URL`, or `REDIS_URL` for Redis. Its value is the full connection string. These rules apply:

- The list shows only the databases and buckets of the same project and environment.
- An app can have one `DATABASE_URL` reference. A second database replaces the first.
- For more than one database, add the values as secret variables yourself. See [Environment variables](/docs/guides/environment-variables/).
- The hint shows only while the environment has a database or a bucket. If you dismiss the hint, your browser remembers it, and the dashboard has no other **Add Reference** button. The API still works: send `kind=database` and `name=<resource ID>` to `POST /api/apps/<app ID>/attach`.

The connection string has this form:

| Engine | Port | Connection string |
| --- | --- | --- |
| PostgreSQL | `5432` | `postgresql://app:<password>@<host>:5432/<database>` |
| MySQL, MariaDB | `3306` | `mysql://app:<password>@<host>:3306/<database>` |
| Redis | `6379` | `redis://:<password>@<host>:6379` |
| SQLite | `4001` | `http://<host>:4001` |

The **Endpoint** on the **Overview** tab is the host. The host works only from services in the cluster. To read the password and the full connection string, run `geass databases connection <ID> --reveal`. See [Command line and AI agents](#command-line-and-ai-agents). To reach the database from your own computer, see [Under the hood](#under-the-hood).

:::caution
Keep project network isolation on, and read the password from the connection details. Geass makes a random password for each new database, and does not rotate it. A database that an earlier release made keeps its old password, which is the resource ID followed by `-password`. For that database, network isolation is what protects it. See [Network isolation](/docs/guides/network-isolation/).
:::

## The database panel

To open the panel of a database, click the database on the project canvas. The panel has these tabs:

| Tab | What it shows |
| --- | --- |
| **Overview** | Project, environment, engine, size, storage, backups ("Continuous, restore to any moment", "Daily copy", or "Off"), and the endpoint after the database is ready |
| **Deployments** | The deploy attempts, with errors, newest first |
| **Console** | A query box. See [Run a query](#run-a-query) |
| **Monitor** | CPU, memory, disk used, and network. Needs the monitoring add-on. See [Observability](/docs/guides/observability/) |
| **Backups** | Status, settings, and restore. Only for PostgreSQL, MySQL, MariaDB, and SQLite that Geass runs |
| **Settings** | The sections below |

**Settings** has a **Filter settings** box. To move to the box, press `/`. **Settings** has these sections:

| Section | Contents |
| --- | --- |
| **General** | The project, the environment, and the resource ID, with a copy button |
| **Database** | **Version**, **Resources** (CPU and memory), and **Storage** (**Size**) |
| **Backups** | The same panel as the **Backups** tab |
| **Scale** | **Resources** again, and **Run on** if servers have tags |
| **Delete database** | The delete action. The section list calls it **Danger**. See [Delete a database](#delete-a-database) |

Changes to version, size, CPU, memory, and storage are pending until you click **Deploy changes**. Backup changes apply immediately. Viewers see "Your viewer role can inspect this resource but cannot change it."

If the setup failed, a **Retry** button shows beside the status.

## Run a query

The **Console** tab has a **Query** box and a **Run query** button. Geass runs the text with the client of the engine and shows the output. Only admins can run queries. Viewers see "Your viewer role cannot run queries."

| Engine | What Geass runs |
| --- | --- |
| PostgreSQL | `psql -c <query>` |
| MySQL | `mysql -e <query>` |
| SQLite | The query, sent to the rqlite API. The output is the JSON of rqlite, `{"results":[...]}`. A failed statement shows as `{"error":"..."}` |
| Redis | `redis-cli -- <words>`. Geass refuses words that start with `-` |

Each query is a separate command, so the console keeps no state between queries. Before the database runs, the output says "no database pods are running yet" or "The database console is available after the server reports Ready."

:::caution
To query your tables on PostgreSQL, connect with your own client. The PostgreSQL console connects to the `postgres` maintenance database as an administrator, not to the database of your app. `SELECT 1` works, but a query against your tables fails with a missing-relation error.
:::

## Size, storage and resizing

To change **CPU** and **Memory**, open **Settings**, and then **Database** or **Scale**. Then click **Deploy changes**. The database might restart. Check capacity first. See [Usage and capacity](/docs/guides/usage-and-capacity/).

**Disk size** is for each instance. The dashboard offers 1, 5, 10, 20, 50, 100, 200, and 500 GB. These sizes are gibibytes (`10Gi`). The API accepts any size of `1Gi` or more. After the first deploy, you can only make the disk larger, and only on replicated storage. [Resize a database volume](/docs/guides/storage/#resize-a-database-volume) gives the steps and the errors.

With the API, send the new values to the update route, and then deploy:

```bash
curl -X POST https://geass.example.com/api/databases/orders/update \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json" \
  --data-urlencode storageSize=50Gi \
  --data-urlencode cpu=1 \
  --data-urlencode memory=2Gi

curl -X POST https://geass.example.com/api/databases/orders/deploy \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json"
```

## Instances and high availability

By default, a database runs one instance. A high-availability (HA) database runs three instances that copy data to each other, so it survives the loss of a server. HA is available for PostgreSQL, MySQL, and MariaDB. Each instance has its own disk, and each instance reserves the CPU and memory that you selected. So an HA database needs three times the capacity.

The dashboard has no HA switch for a project database. To create an HA database, send `highAvailability=on` through the API, and then deploy it:

```bash
curl -X POST https://geass.example.com/api/databases/create \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json" \
  --data-urlencode project=proj-1a2b3c4d \
  --data-urlencode environment=production \
  --data-urlencode name=orders-ha \
  --data-urlencode engine=Postgres \
  --data-urlencode highAvailability=on
```

Redis and SQLite run one instance only. Geass refuses HA for them with "High availability requires PostgreSQL, MySQL, or MariaDB: Redis runs as a single instance. Turn it off, or choose PostgreSQL, MySQL, or MariaDB" or "SQLite does not support high availability".

### The readiness gate

An HA database does not start until the cluster passes the HA readiness check. The check passes when the cluster has at least three healthy servers, storage, and ready add-ons. Until then, the database shows **Unavailable** with "High availability requires a cluster with at least three healthy nodes". Geass checks again every 30 seconds, and does not count the wait as a failure.

To run the check:

1. Open **Settings**, and then **Usage**.
2. Under **HA readiness**, click **Run readiness check**.

The section shows **Healthy nodes** and **Ready for HA**.

:::caution
Click **Run readiness check** at least one time. Until you do, every HA database waits, and the **High availability** switch for shared servers stays off. After the first check, Geass keeps the result current when servers change. So it is enough to add a third server.
:::

This check is about databases. For HA of the cluster itself, see [High availability](/docs/operations/high-availability/).

### High availability for MySQL

- **Instances.** Three, or five if you set `instances` to 5. Geass refuses any other number with "High availability requires 3 or 5 MySQL instances".
- **One primary.** Writes go to the primary, and reach a replica before the primary confirms them. So a confirmed write survives the loss of the primary. Make your app reconnect after an error, because the connection to a lost primary breaks.
- **Failover.** When the primary is lost, Geass promotes the most up-to-date replica. In tests on three servers, a replica took over within a minute, and no confirmed write was lost. A primary that froze without stopping took about three minutes to replace. A server that comes back becomes a replica again.
- **At least 1 GB for each instance.** MySQL with HA needs about 0.5 GB before it holds any data. So Geass gives each instance at least 1 GB of memory, whatever size you selected.
- **One instance or HA for life.** A MySQL database cannot change between one instance and HA. A request to change says "High availability requires a new database". Create a new database and move the data.
- **Backups** are daily copies, taken from the primary. You cannot restore to a moment.
- **cert-manager.** MySQL with HA needs the cert-manager add-on, which is on by default. If it is off, the database shows "High availability requires cert-manager".
- **Spreading.** The instances prefer different servers. If a region has fewer than three servers, the database still runs, but it does not survive the loss of one of them. See [High availability](/docs/operations/high-availability/#where-the-instances-run).

### High availability for MariaDB

- **Instances.** Without HA, MariaDB runs one server. With HA, it runs three, or five if you set `instances` to 5. Geass refuses any other number with "High availability requires 3 or 5 MariaDB instances".
- **One instance or HA for life.** A request to change says "High availability requires a new database".
- **One primary.** Writes go to the primary, and reach a replica before the primary confirms them. If no replica answers for ten seconds, the primary continues alone. Make your app reconnect after an error.
- **Failover.** When the primary stops, Geass promotes a replica. In a test, a replica took over ten seconds after the primary was stopped. When a whole server stops responding, Geass waits one minute, and then promotes the replica that has the most data. Until then, the database shows "Promoting a replica: the primary's machine is not responding". If the promotion does not finish in ten minutes, Geass cancels it and waits half an hour before it tries again.
- **Replica repair.** Geass builds a replica again from a copy of another replica. Without replicated storage, Geass can build a replica again only on the server where the copy was taken.
- **Backups** are a physical copy every night, with the change logs archived beside it. You can restore to any moment up to about fifteen minutes ago. A MariaDB database with one server has daily copies. See [Backups and restore](/docs/guides/backups-and-restore/#mariadb-with-high-availability-copies-and-archived-logs).
- **Compliance tags.** In a project that requires node tags, MariaDB with HA needs Kubernetes 1.36 or later. On an older cluster, Geass refuses its backup job, and the backups show **At risk**. See [Compliance node tags](/docs/guides/compliance-tags/).
- **Spreading.** The instances prefer different servers, as for MySQL. See [High availability](/docs/operations/high-availability/#where-the-instances-run).

## Where a database runs

Geass decides where a database runs when it first creates the database. The database keeps its data there, and Geass does not move it. Three things make up the decision:

- **The region.** The region of the project, or the default region of the cluster.
- **Run on.** The server tags that you selected under **Scale**, **Run on**. A server needs all of them. Only an admin who is signed in to the dashboard can set them. The API, the command line, and the MCP server cannot.
- **The tags that the project requires.** A project can require compliance tags, for example `hipaa`. Geass then places each new database of the project on servers that carry all of these tags. You set these tags on the project, not on the database. See [Compliance node tags](/docs/guides/compliance-tags/).

For a project that requires tags, these rules apply:

- Geass keeps the disks, and the backup and restore jobs, on servers with the required tags.
- The backups go to your S3 bucket, outside the cluster. See [Backups and restore](/docs/guides/backups-and-restore/).
- An external database is not in the cluster, so the tags do not apply to it.
- A database on a shared server follows the placement of the server. See [Shared database servers](/docs/guides/shared-database-servers/#projects-that-require-node-tags).

### Databases that already exist

A database that existed before the project required a tag stays where it is. So Geass refuses to add a tag to a project while one of its databases does not run on servers with that tag. The message names the databases. A draft does not count.

To move a database that has backups:

1. Make sure that the database has a recent backup.
2. Delete the database. Geass first takes a final backup and keeps it for seven days.
3. Set the required tags on the project.
4. Restore the backup into a new database. Geass places the new database with the current tags of the project. It does not copy the **Run on** choice of the original.

To list each database of a project with the tags that it is missing, run `geass compliance show PROJECT`. See [Compliance node tags](/docs/guides/compliance-tags/#projects-that-already-have-databases).

## External databases

An external database is a database that Geass does not run. Apps connect to it like any other database, with **Add Reference**. You can record a database that you already host, or have Geass create a database in PlanetScale.

The dashboard lists two external rows, **AWS RDS** and **PlanetScale**. **AWS RDS** is unavailable until the project has an AWS connection. **PlanetScale** always shows "Coming soon". Create both kinds with the REST API.

External databases have no storage, size, or backups in Geass. Use the backups of your provider.

### Connect to a database you host

With `mode=Connect`, you record the host, port, login, and password of a database that you created. Geass never creates or deletes that database. The project needs no connection. **AWS RDS** is a label: it works for any Postgres that you host.

:::caution
Create external databases with the REST API, as shown below, and delete any draft that the dashboard made. The **AWS RDS** row saves a draft with no host, port, login, or password, and the dashboard has no field for them. That draft shows "external database host, username, and password are required".
:::

```bash
curl -X POST https://geass.example.com/api/databases/create \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json" \
  --data-urlencode project=proj-1a2b3c4d \
  --data-urlencode environment=production \
  --data-urlencode name=orders-rds \
  --data-urlencode engine=Postgres \
  --data-urlencode placement=External \
  --data-urlencode provider=AWS \
  --data-urlencode mode=Connect \
  --data-urlencode host=orders-db.example.com \
  --data-urlencode port=5432 \
  --data-urlencode username=app \
  --data-urlencode password='change-me' \
  --data-urlencode databaseName=orders

curl -X POST https://geass.example.com/api/databases/orders-rds/deploy \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json"
```

If you leave out `port`, Geass uses the default port of the engine.

### PlanetScale

PlanetScale support is experimental. With `mode=Create` and `provider=PlanetScale`, Geass uses the PlanetScale connection of the project to create the database. `engine` must be `MySQL` or `Postgres`. With `mode=Connect`, PlanetScale works like the AWS case above.

When you deploy a `mode=Create` database, Geass does these steps:

1. Geass creates a PlanetScale database named after `databaseName`, or after the resource ID. If a database of the same kind has that name, Geass uses it. If it has the other kind, Geass reports that it "already exists as" the other kind.
2. Geass waits while PlanetScale sets up the database. The status shows "PlanetScale database is starting". After 30 minutes, the database fails with "PlanetScale database did not become ready within 30m0s; check the database in PlanetScale".
3. Geass creates a login on the `main` branch. For Postgres, the login can run migrations.
4. Geass publishes the connection details, so apps can connect with **Add Reference**.

To select the size, send `clusterSize`, for example `PS_10`. If you leave it out, PlanetScale uses its own default. You cannot change the size after Geass creates the database. To list the sizes that your organization can use, send this request with a workspace admin token:

```bash
curl "https://geass.example.com/api/planetscale/cluster-sizes?project=proj-1a2b3c4d&connection=prod-planetscale&engine=MySQL" \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json"
```

The response is `{"items":[...]}`. Send the `name` of an item as `clusterSize`. When PlanetScale gives prices, `monthlyUsd` is the monthly cost of a primary and two replicas, in US dollars. `replicaMonthlyUsd` is the cost of one server. Geass refuses a size that PlanetScale does not offer for the engine.

To create a MySQL database in PlanetScale and deploy it:

```bash
curl -X POST https://geass.example.com/api/databases/create \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json" \
  --data-urlencode project=proj-1a2b3c4d \
  --data-urlencode environment=production \
  --data-urlencode name=orders-ps \
  --data-urlencode engine=MySQL \
  --data-urlencode placement=External \
  --data-urlencode provider=PlanetScale \
  --data-urlencode mode=Create \
  --data-urlencode connectionRef=prod-planetscale \
  --data-urlencode databaseName=orders \
  --data-urlencode clusterSize=PS_10

curl -X POST https://geass.example.com/api/databases/orders-ps/deploy \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json"
```

For a Postgres database, send `engine=Postgres`, and take the size from the list for `engine=Postgres`.

### Deleting an external database

When you delete an external database, Geass removes only its own records. Geass does not delete the remote database or its PlanetScale login. Remove them in your provider yourself.

## Delete a database

1. Open the database, and select **Settings**, then **Delete database**.
2. Under "Type `<ID>` to delete", enter the resource ID. The dashboard asks for the ID, not the display name.
3. Click **Delete database**.
4. In the confirmation, click **Delete permanently**. The dashboard says "Resource deleted".

The section tells you what happens to the data:

- If the database has backups, Geass first takes a final backup and keeps the backups for seven days. The section says "Geass takes a final backup first and keeps it for 7 days (Project settings → Backups)."
- If the database has no backups, the section says "This database has no backups. Deleting it permanently destroys its data."

Geass deletes the disks of the database and its connection details. An app that still references the database fails at its next restart. Remove the reference from the app first.

When you delete a project, Geass deletes its databases and its connections in one step. Geass might not manage a final backup, and cannot restore one afterwards. Restore what you need first. See [Backups and restore](/docs/guides/backups-and-restore/).

With the API, send the resource ID as `confirmName`:

```bash
curl -X POST https://geass.example.com/api/databases/orders/delete \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json" \
  --data-urlencode confirmName=orders
```

Databases in a pull request environment are empty copies with one instance and no backups. See [Pull request environments](/docs/guides/pull-request-environments/).

## Command line and AI agents

The `geass` command line and its MCP server list databases, show their settings and status, and show how to connect. They do not create, deploy, or restore a database. For those tasks, use the dashboard or `geass api`. See [Command line](/docs/guides/command-line/), [CLI commands](/docs/reference/cli/#geass-databases), [MCP server](/docs/guides/mcp-server/), and [MCP tools](/docs/reference/mcp-tools/#list_databases).

| To | Command | MCP tool |
| --- | --- | --- |
| List databases | `geass databases list` | `list_databases` |
| Read the settings and status of one database | `geass databases get orders` | `get_database` |
| Read the host, port, database, and login | `geass databases connection orders` | `database_connection` |
| Read the password and connection string | `geass databases connection orders --reveal` | None |
| Read CPU, memory, disk, and network | `geass metrics orders --kind databases` | `app_metrics` with `kind` set to `databases` |
| Delete | `geass api DELETE /databases/orders -q confirmName=orders` | `delete_resource`, only when the server runs with `--allow-delete` |

```text
database   orders
host       orders-rw.proj-1a2b3c4d-production.svc
port       5432
username   app
```

- `list` and `get` work with any workspace token. A token that cannot change things sees no engine, version, host, or connection details. A project token cannot read databases.
- `connection` needs a workspace token with Full access, and works after the database is ready. Before that, it fails with "no connection details yet: the resource has not finished setting up".
- `--reveal` adds the password and the connection string. Geass logs who revealed them. The MCP server cannot reveal them, so an agent can find a database but cannot log in to it.
- `geass databases list` also lists shared servers. For logical databases, use `geass api GET /logical-databases`.
- Databases do not scale to zero. Scale to zero applies to services only.

For buckets, `geass stores list` and `geass stores get` do the same. See [Object storage](/docs/guides/object-storage/#command-line-and-ai-agents).

## Options

These are the fields that the API accepts. The [resource reference](/docs/reference/resources/overview/) has the full schema.

| API field | Meaning | Default |
| --- | --- | --- |
| `project`, `environment` | The project environment that owns the database | Required |
| `engine` | `Postgres`, `MySQL`, `MariaDB`, `SQLite`, or `Redis` | `Postgres` |
| `placement` | `InCluster` or `External` | `InCluster` |
| `provider`, `mode` | External only: `AWS` or `PlanetScale`, and `Create` or `Connect`. `Create` works only for PlanetScale | None |
| `clusterSize` | PlanetScale only: the size at creation, for example `PS_10` | The PlanetScale default |
| `highAvailability` | Three instances. PostgreSQL, MySQL, and MariaDB only | Off |
| `version` | PostgreSQL version | `16` |
| `storageSize` | Disk for each instance, at least `1Gi` | `10Gi` (`1Gi` for SQLite) |
| `cpu`, `memory` | CPU and memory for each instance | `250m` and `512Mi` (SQLite `100m` and `128Mi`) |
| `backups`, `backupRetentionDays` | Backups `on` or `off`, and days to keep. Read on update only | On, 7 days |
| `connectionRef` | An AWS or PlanetScale connection | None |
| `databaseName` | The database in the server | The resource ID |
| `host`, `port`, `username`, `password` | External only | The port of the engine |

## Under the hood

This section is for admins who have `kubectl` access to the cluster.

Each database is a `GeassDatabase` object in the `geass-system` namespace. Its workloads run in the namespace of the environment, `<project ID>-<environment>`. The connection details are in the Secret `<resource ID>-connection` in that namespace. PostgreSQL runs on CloudNativePG, MySQL with HA on MOCO, and MariaDB on the MariaDB operator. SQLite runs on rqlite.

To reach a database from your computer, forward its port, and replace the host with `localhost`. This example is for PostgreSQL:

```bash
NS=proj-1a2b3c4d-production
kubectl port-forward -n "$NS" svc/orders-rw 5432:5432
```

To query your own tables from the PostgreSQL pod, run this command:

```bash
kubectl exec -n "$NS" orders-1 -c postgres -- psql -d orders -c 'SELECT count(*) FROM users'
```

To read the status message, where a database runs, and its disks:

```bash
kubectl get geassdatabase orders -n geass-system \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}{"\n"}'
kubectl get geassdatabase orders -n geass-system \
  -o jsonpath='{.metadata.annotations.geass\.dev/node-placement}{"\n"}'
kubectl get pods,pvc -n "$NS"
```

You can also apply a `GeassDatabase` manifest. Such a database starts immediately, with backups off unless you set `spec.backup`. Set `spec.deploy.enabled: true`, or the command line and the API list it as `draft`.

A MySQL database made on an early development build still uses the Bitnami chart. When you delete it, check `kubectl get pvc -n <namespace>` for a left-over volume.

## Troubleshooting

The dashboard shows the status message of the database. It rewrites some messages into plainer words.

**The database stays at Draft.**
The database is not deployed. In the top bar, click **Deploy changes**.

**"Platform setup is in progress."**
This database is the first of its engine on the cluster, and Geass is setting up the engine. Wait a few minutes.

**"Not enough capacity for this size. Choose a smaller one or add capacity."**
No server can hold the database. Select a smaller CPU and memory, or add a server. See [Usage and capacity](/docs/guides/usage-and-capacity/).

**"High availability requires a cluster with at least three healthy nodes".**
Run the readiness check, as described in [The readiness gate](#the-readiness-gate). Make sure that the cluster has three healthy servers.

**"Storage is starting", then "Storage is unavailable".**
Replicated storage is starting or does not work. Geass waits up to 15 minutes, and then counts the problem as a failure. In a region or a project with required tags, the wait has no limit. See [Storage and volumes](/docs/guides/storage/#troubleshooting).

**"Provisioning failed after repeated errors".**
After five failures in a row, or three minutes of failures, Geass stops. To read the error, open the **Deployments** tab. Fix the cause, and then click **Retry**. With the API, use `POST /api/databases/<ID>/retry-provision`.

**A MySQL or Redis database shows "Object storage is starting." for a long time.**
The dashboard shows these words for every engine while the database server starts. A first MySQL start can take a few minutes. If it takes longer, an admin can read the events of the pod. See [Under the hood](#under-the-hood).

**"Could not pull the database image redis:7.4.11-alpine (ImagePullBackOff)", or the same for `mysql:8.4.11`.**
A server cannot download the image from Docker Hub. These are the usual causes:

- A proxy, a firewall, or DNS blocks Docker Hub.
- The Docker Hub limit on anonymous pulls (`toomanyrequests`).
- The cluster has no route to the internet. Load the images onto the servers, or use a registry mirror.

Geass keeps trying. After 10 minutes, the database is **Failed**. Fix the cause, and then click **Retry**.

**"High availability requires 3 or 5 MySQL instances, not N" (or "... MariaDB instances ...").**
Set `instances` to 3 or 5, or turn off HA.

**"High availability requires a new database".**
A MySQL or MariaDB database cannot change between one instance and HA. Change the setting back, or create a new database and move the data.

**"High availability requires cert-manager".**
MySQL with HA needs the cert-manager add-on. Turn it on. It is on by default.

**"Installing MySQL operator chart" for a long time.**
Geass is setting up MySQL with HA. The servers need access to `cybozu-go.github.io` and `ghcr.io`.

**"Replacing the Redis chart with Geass's own server".**
Geass is moving an old Redis database to its current setup. This move takes about a minute. Geass does not count the wait as a failure.

**"external database host, username, and password are required".**
Create the external database through the API, as described in [Connect to a database you host](#connect-to-a-database-you-host).

**"PlanetScale database is starting" for a long time.**
PlanetScale is still setting up the database. Geass waits up to 30 minutes. Check the database in PlanetScale, and then click **Retry**.

**"a PlanetScale connection is required", "PlanetScale token and organization are required", or "PlanetScale hosts MySQL and Postgres databases, not ...".**
Send `connectionRef` to a PlanetScale connection of the same project, and an `engine` of `MySQL` or `Postgres`. See [PlanetScale](#planetscale).

**"PlanetScale database ... already exists as mysql, not postgresql" (or the reverse).**
A PlanetScale database with that name has the other engine. Use another `databaseName`.

**"cloud connection ... was not found" or "is scoped to project ...".**
The connection does not exist, or it belongs to another project. Each connection belongs to one project.

**"storage can be increased but not decreased once deployed" and similar size errors.**
See [Resize a database volume](/docs/guides/storage/#resize-a-database-volume).

**The app cannot connect.**
Check these items, in order:

1. You deployed the app after you added the reference.
2. The database is **Ready**.
3. The app is in the same project and environment as the database.
4. The app uses `DATABASE_URL` or `REDIS_URL`.
5. Network isolation allows the connection. See [Network isolation](/docs/guides/network-isolation/).

**A SQLite database answers `503` after a restart.**
A SQLite database made by an earlier release does not start again after Geass replaces its instance, for example after a server drain. The database stays unavailable, and Geass has no tool to repair it. Move its data into a new SQLite database. A SQLite database that the current release makes does not have this problem.

**A MySQL server in the sandbox says Ready, but its `app` login is refused.**
MySQL ran out of memory during its first start in the sandbox. Delete the database, and create it again with at least 1 GB of memory. See [What it costs](/docs/operations/sandboxed-runtime/#what-it-costs).

**A restored database stays at "Restoring database from backup".**
See [Failure modes](/docs/guides/backups-and-restore/#failure-modes).

## Related

- [Backups and restore](/docs/guides/backups-and-restore/)
- [Storage and volumes](/docs/guides/storage/)
- [Shared database servers](/docs/guides/shared-database-servers/)
- [Compliance node tags](/docs/guides/compliance-tags/)
- [Command line](/docs/guides/command-line/) and [MCP server](/docs/guides/mcp-server/)
- [Environment variables](/docs/guides/environment-variables/)
- [Cloud connectors](/docs/guides/connectors/)
- [High availability](/docs/operations/high-availability/)
- [Troubleshooting](/docs/operations/troubleshooting/)
