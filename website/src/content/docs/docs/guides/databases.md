---
title: "Databases"
description: "Create PostgreSQL, MySQL, MariaDB, Redis, and SQLite databases, connect apps, run queries, resize storage, set up high availability, and use external ones."
---

A database in Geass is a resource that belongs to one project environment. Geass runs the database on your servers and creates its login. Geass also publishes a connection string that your apps read from a Secret. You can do these things:

- Run PostgreSQL, MySQL, MariaDB, Redis, or SQLite in the cluster.
- Connect an app to a database that you already host somewhere else, for example AWS RDS.
- Have Geass create a database in your PlanetScale organization.

![A database's panel, with its details, backups, and connect string.](../../../../assets/docs/database-drawer.webp)
*Screenshot with example data.*

This page covers one database for each resource. To share a single server between many projects, see [Shared database servers](/docs/guides/shared-database-servers/). For backups, see [Backups and restore](/docs/guides/backups-and-restore/). For how disks work, see [Storage and volumes](/docs/guides/storage/). To keep the data of a project on nodes that you select, see [Where a database runs](#where-a-database-runs). To list databases and read how to connect from a terminal or an AI agent, see [Command line and AI agents](#command-line-and-ai-agents).

## Before you start

- You need a project and an environment. See [Projects and environments](/docs/guides/projects-and-environments/).
- The cluster needs room for the database. Geass refuses the deploy when no node can fit the database. If the project requires node tags, the room must be on nodes that carry those tags. See [Where a database runs](#where-a-database-runs).
- The first PostgreSQL database on a cluster installs the CloudNativePG operator. This installation takes a few minutes. In the same way, the first MariaDB database installs the MariaDB operator, and the first highly available MySQL database installs MOCO.
- For backups, the project needs an AWS connection. For Geass to create a PlanetScale database, the project needs a PlanetScale connection. To connect to an external database that you already host, you need no connection. See [Cloud connectors](/docs/guides/connectors/).
- For resizable disks, the cluster needs the Longhorn add-on. See [Storage and volumes](/docs/guides/storage/).

## Engines

| Engine | What runs | Default size per instance | Disk | Backups |
| --- | --- | --- | --- | --- |
| PostgreSQL | A CloudNativePG cluster that uses the image `ghcr.io/cloudnative-pg/postgresql:<version>`. Version 16 by default | 0.25 CPU, 512 MB | 10 GiB | Continuous, restore to any moment |
| MySQL | The official `mysql:8.4.11` image, one pod on a volume. With high availability, three or five servers that MOCO runs, from its own build of MySQL 8.4 | 0.25 CPU, 512 MB (1 GB with high availability) | 10 GiB | Daily copy |
| MariaDB | The official `mariadb:11.8.9` image, run by the MariaDB operator. One server on a volume, or, with high availability, three or five servers that replicate to each other | 0.25 CPU, 512 MB | 10 GiB | One server: daily copy. With high availability: continuous, restore to any moment |
| Redis | The official `redis:7.4.11-alpine` image, one pod, with persistence off | 0.25 CPU, 512 MB | None | None |
| SQLite | rqlite 8.36.16, which serves SQLite over HTTP on port 4001 | 0.1 CPU, 128 MB | 1 GiB | Daily copy |

Things to know about each engine:

- **PostgreSQL** is the only engine that uses the **Version** field, although the dashboard shows the field for every database. The field is the tag of the Postgres image. Geass does not check it, so use a tag that exists. Geass does not migrate data between major versions. Select the version before the first deploy.
- **MySQL and Redis** versions come from the images that Geass pins (MySQL 8.4, Redis 7.4). The **Version** field has no effect on them. You can make MySQL highly available, but not Redis. See [Instances and high availability](#instances-and-high-availability).
- **MariaDB** is a separate engine from MySQL. MariaDB is for software that asks for MariaDB or uses features that only MariaDB has. Select MariaDB when you create the database, because you cannot change the engine afterwards. The **Version** field has no effect on MariaDB. The engine is MariaDB 11.8, and it speaks the MySQL protocol. So a MySQL client or driver connects to it, and the connection string starts with `mysql://`. You can make MariaDB highly available. See [High availability for MariaDB](#high-availability-for-mariadb).
- **Redis** keeps its data in memory. It has no disk, no size setting, and no backups. A restart empties it, so use Redis as a cache.
- **SQLite** runs as rqlite, one instance on one volume. Apps talk to it over HTTP at port 4001. They do not open a file. It has no login: the connection string is a plain `http://` address.
- **CPU and memory** are one setting. The limit equals the request, so a database never bursts beyond what it reserved. Select CPU from 0.1, 0.25, 0.5, 1, or 2. Select memory from 128 MB, 256 MB, 512 MB, 1 GB, 2 GB, or 4 GB.

:::note
The dashboard shows every database with a version beside its engine. So a MySQL, MariaDB, Redis, or SQLite database appears as `mysql 16` on its card and "MySQL 16" in its panel. The API and the command line report the same `version`. The number is the default of `spec.version`, which is `16` for every engine. The number is not the version of the engine.
:::

## Create a database

1. Open the project and environment.
2. Click **+ Add resource**, and then select **Database**.
3. Select **Database server**, and then **PostgreSQL**, **MySQL**, **MariaDB**, **Redis**, or **SQLite**. Geass saves a draft and opens its **Settings**.

   To use a server that already runs, select **Logical database** instead. See [Shared database servers](/docs/guides/shared-database-servers/).
4. In **Database**, set these values:
   - The **Version**. Only PostgreSQL uses it.
   - **CPU** and **Memory** under **Resources**.
   - The **Size** under **Storage**.
5. In **Backups**, review the defaults.
6. If your nodes have tags, set **Run on** in **Scale**. A database keeps its data on the nodes that it is first deployed to, so this choice closes at the first deploy. If the project requires compliance tags, you do not select them here: Geass adds them. See [Scaling and placement](/docs/guides/scaling-and-placement/) and [Where a database runs](#where-a-database-runs).
7. Click **Save changes**. Every **Save changes** button on the page saves all the values on the page.
8. In the top bar of the project, click **Deploy changes**. The **N pending** button beside it lists what Geass deploys.

The status moves from **Draft** to **Setting up** and then **Ready**. **Unavailable** or **Failed** means that something stopped the database. See [Troubleshooting](#troubleshooting). The **Deployments** tab shows each attempt and its errors.

A new database starts with these values:

| Setting | Default |
| --- | --- |
| Name | A generated two-word resource ID, for example `falling-mist` |
| Database name | The resource ID. For PostgreSQL and MySQL, this is the database in the server |
| Login | `app` |
| CPU and memory | 0.25 CPU and 512 MB (SQLite: 0.1 CPU and 128 MB) |
| Disk | 10 GiB (SQLite: 1 GiB, Redis: none) |
| Backups | On for PostgreSQL, MySQL, and SQLite, kept 7 days. Backups protect nothing until the project has backup storage |
| Instances | 1 |

To rename the database, edit its title. This change affects the display name only. The resource ID does not change. You use the resource ID in `kubectl` and the API.

### Create a database with the REST API

Use a workspace token with the admin role. Project tokens cannot create resources. See [Users and tokens](/docs/guides/users-and-tokens/) and the [REST API reference](/docs/reference/rest-api/). The request creates a draft, so deploy the draft afterwards.

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

The versioned API does the same with JSON bodies: `POST /api/v1/databases`, `PATCH /api/v1/databases/{name}`, `POST /api/v1/databases/{name}/deploy`, and `DELETE /api/v1/databases/{name}`. From a terminal, `geass api` calls them:

```bash
geass api POST /databases -d '{"project": "proj-1a2b3c4d", "environment": "production", "name": "orders", "engine": "Postgres", "storageSize": "20Gi"}'
geass api POST /databases/orders/deploy
```

The fields are the fields in [Options](#options). See the [REST API reference](/docs/reference/rest-api/#databases-logical-databases-and-buckets) and the [command line](/docs/guides/command-line/#anything-else-the-raw-api).

The resource ID (`name`) must be unique in the cluster, because every database is an object in `geass-system`. If you leave out `name`, Geass generates a two-word ID and returns it. `cpu` and `memory` take the sizes that the dashboard offers:

- `cpu`: `100m`, `250m`, `500m`, `1`, or `2`.
- `memory`: `128Mi`, `256Mi`, `512Mi`, `1Gi`, `2Gi`, or `4Gi`.

Send both. If you send only one, the other falls back to `100m` or `128Mi`, not to the default of the engine. A PostgreSQL, MySQL, or SQLite database created this way starts with backups on and a 7-day retention, whatever you send. To change these values, send `backups` and `backupRetentionDays` to `POST /api/databases/<ID>/update`, as described in [Backups and restore](/docs/guides/backups-and-restore/#turn-backups-on-off-or-change-retention).

### Create a database with a manifest

You can also apply a `GeassDatabase` in the `geass-system` namespace. An object created this way has no draft step, and it starts provisioning immediately. Backups stay off unless you set `spec.backup`. Also set `spec.deploy.enabled` to `true`. Without this field, the command line, the MCP server, and the API list the database as `draft`, even while it runs. The dashboard also still offers settings that close at the first deploy, for example **Run on**.

```yaml title="orders-db.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassDatabase
metadata:
  name: orders
  namespace: geass-system
spec:
  project: proj-1a2b3c4d
  environment: production
  engine: Postgres
  version: "16"
  storageSize: 20Gi
  resources:
    requests:
      cpu: 500m
      memory: 1Gi
    limits:
      cpu: 500m
      memory: 1Gi
  backup:
    enabled: true
    retentionDays: 14
  deploy:
    enabled: true
```

```bash
kubectl apply -f orders-db.yaml
kubectl get geassdatabases -n geass-system
```

## Connect an app to a database

When a database is ready, Geass writes a Secret named `<resource ID>-connection` in the namespace of the environment (`<project ID>-<environment>`). The Secret has these keys:

| Key | Value |
| --- | --- |
| `host` | The in-cluster address of the database |
| `port` | `5432` PostgreSQL, `3306` MySQL and MariaDB, `6379` Redis, `4001` SQLite |
| `database` | The database name |
| `username` | `app` |
| `password` | The password |
| `uri` | The complete connection string |

The addresses and strings look like this:

| Engine | Host | Connection string |
| --- | --- | --- |
| PostgreSQL | `<ID>-rw.<namespace>.svc` | `postgresql://app:<password>@<host>:5432/<database>` |
| MySQL | `geass-mysql-<ID>.<namespace>.svc`. With HA, `moco-<ID>-primary.<namespace>.svc` | `mysql://app:<password>@<host>:3306/<database>` |
| MariaDB | `<ID>.<namespace>.svc`. With HA, `<ID>-primary.<namespace>.svc` | `mysql://app:<password>@<host>:3306/<database>` |
| Redis | `geass-redis-db-<ID>-master.<namespace>.svc` | `redis://:<password>@<host>:6379` |
| SQLite | `<ID>.<namespace>.svc` | `http://<host>:4001` |

To give an app the connection string:

1. Open the app and select **Variables**.
2. In the hint "Trying to connect a database?", click **Add Reference**.
3. In **Add reference**, select the database under **Resource**. A database that is not ready is grayed out with "not ready".
4. Click **Add reference**. The dashboard says "Connection added. Deploy to apply it."
5. Deploy the app.

The reference adds a variable that reads the `uri` key of the Secret. The variable is `DATABASE_URL`, or `REDIS_URL` for Redis. The list contains only databases and buckets from the same project and environment. A second database replaces the first under the same name, so an app can have one `DATABASE_URL` reference. For more than one database, add the values as Secret variables yourself. See [Environment variables](/docs/guides/environment-variables/).

The hint appears only while the environment has at least one database or bucket. The hint has a dismiss button, and your browser remembers the dismissal. After you dismiss the hint, the dashboard offers no other entry point to **Add Reference**. The API still works: send `kind=database` and `name=<resource ID>` to `POST /api/apps/<app ID>/attach`. Either way, the reference is a pending change to the app, so deploy the app afterwards.

The address of the database resolves only inside the cluster. To reach a Postgres database from your machine, run these commands:

```bash
NS=proj-1a2b3c4d-production
kubectl port-forward -n "$NS" svc/orders-rw 5432:5432
kubectl get secret orders-connection -n "$NS" -o jsonpath='{.data.uri}' | base64 -d
```

Then replace the host in that string with `localhost`. To read the host, port, database, and login without `kubectl`, see [Command line and AI agents](#command-line-and-ai-agents).

:::caution
Read the password from the connection Secret, not from the name of the database, and keep project network isolation on. Geass makes a random password for the `app` login of an in-cluster database (48 hexadecimal characters). For MySQL, the root password has the same value. Geass creates the password one time, in the `<ID>-bootstrap` Secret, and does not rotate it.

A database that an earlier release made keeps the password that it has. That password is the resource ID followed by `-password`, for example `orders-password`. Geass does not change the password of a running database, because everything that connects to the database would have to change too. Databases are reachable only inside the cluster. For an older database, network reachability is what protects it. See [Network isolation](/docs/guides/network-isolation/).
:::

## The database panel

To open the panel of a database, click the database on the project canvas. The panel has these tabs:

| Tab | What it shows |
| --- | --- |
| **Overview** | Project, environment, source (the engine), size (CPU and memory), storage, backups ("Continuous, restore to any moment", "Daily copy", or "Off"), and the endpoint (the host) after the database is ready |
| **Deployments** | The deploy attempts, with errors, newest first |
| **Console** | A query box. See [Run a query](#run-a-query) |
| **Monitor** | CPU, memory, disk used, and network in and out. Needs the monitoring add-on. See [Observability](/docs/guides/observability/) |
| **Backups** | Status, settings, and restore. Present for PostgreSQL, MySQL, and SQLite that run in the cluster |
| **Settings** | The sections below |

Databases have no **Variables** tab. That tab belongs to services.

**Settings** has a **Filter settings** box. To focus the box, press `/`. **Settings** has these sections:

| Section | Contents |
| --- | --- |
| **General** | The project, the environment, and the resource ID, with a copy button |
| **Database** | **Version**, **Resources** (CPU and memory), and **Storage** (**Size**). Resources and storage appear for in-cluster databases. Storage appears only for engines that have a disk |
| **Backups** | The same panel as the **Backups** tab |
| **Scale** | **Resources** again, and **Run on** if nodes have tags. In-cluster databases only |
| **Delete database** | Deletion. The section list beside the settings calls this section **Danger**. See [Delete a database](#delete-a-database) |

Changes to version, size, CPU, memory, and storage are pending until you deploy them. The card of the resource on the canvas shows the change. The **N pending** button in the top bar counts the change, and **Deploy changes** applies it. Backup changes apply immediately. Viewers see "Your viewer role can inspect this resource but cannot change it."

If provisioning failed, a **Retry** button appears beside the status.

## Run a query

The **Console** tab has a **Query** box, filled with `SELECT 1`, and a **Run query** button. Geass runs the text in the pod of the database with the client of the engine, and shows the output. For SQLite, the client is a request to the HTTP API of rqlite. Only admins can run queries. Viewers see "Your viewer role cannot run queries."

| Engine | Command Geass runs |
| --- | --- |
| PostgreSQL | `psql -c <query>` |
| MySQL | `mysql -e <query>` |
| SQLite | `wget` posts `["<query>"]` to the rqlite `/db/request` endpoint on `127.0.0.1:4001`. rqlite runs the query as a read or a write. The output is the JSON of rqlite, `{"results":[...]}`. A statement that fails comes back inside it as `{"error":"..."}` |
| Redis | `redis-cli -- <words>`. Geass refuses words that start with `-` |

A query needs a running pod. Without one, the output says "no database pods are running yet", or "The database console is available after the server reports Ready." Each query is a separate command, so a session does not keep state between queries.

:::caution
To query your data on PostgreSQL, connect from your own client, or run `kubectl exec -n <namespace> <ID>-1 -c postgres -- psql -d <database> -c '<query>'`. On PostgreSQL, the console connects with the defaults of the pod: the `postgres` administrative role and the `postgres` maintenance database, not the database of your application. `SELECT 1` works, but a query against your tables fails with a missing-relation error.
:::

External databases also show a **Console** tab. The tab has no pod to run in.

## Size, storage and resizing

**CPU and memory** change in **Settings**, in **Database** or **Scale**. Then click **Deploy changes**. Geass rolls out the new limits and might restart the database. Check capacity first. See [Usage and capacity](/docs/guides/usage-and-capacity/).

**Disk size** is for each instance. The dashboard offers 1, 5, 10, 20, 50, 100, 200, and 500 GB. These values are gibibyte values (`10Gi`) that the list labels as GB. The API accepts any quantity of `1Gi` or more. After the first deploy, you can only grow the disk, and only when the storage class of the database allows it. [Storage and volumes](/docs/guides/storage/#resize-a-database-volume) gives the rules, the errors, and how volumes are chosen.

With the API, send the new values to the update route, and then deploy. The change stays pending until the deploy:

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

By default, a database runs one instance. A high-availability (HA) PostgreSQL database runs three CloudNativePG instances that replicate to each other. So the database survives the loss of a node. A high-availability MySQL database runs three or five MySQL servers. MOCO keeps these servers replicating to each other. When the primary is lost, MOCO promotes a replica. A high-availability MariaDB database runs three or five MariaDB servers, and the MariaDB operator keeps them replicating. Each instance has its own disk of the size that you selected. With Longhorn, each disk is a single copy on its own node.

The dashboard has no switch for HA on a project database. Create the database through the API with `highAvailability=on`, and then deploy it like any other database. Geass sets `instances` to 3. A database that asks for three or more instances counts as HA.

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

### The readiness gate

An HA database does not start until the cluster passes the HA readiness check. Until then, these things are true:

- The Ready condition of the database reads "High availability requires a cluster with at least three healthy nodes". The dashboard shows **Unavailable**.
- Geass checks again every 30 seconds.
- Geass does not count the wait as a failure.

The check passes when all of these conditions are true:

- At least three nodes are Ready and schedulable.
- At least one storage class exists.
- The Geass cluster is Ready.
- The cluster add-ons are Ready.

The check applies to every PostgreSQL, MySQL, and MariaDB database that asks for HA. Geass refuses Redis before the check (see below). The check does not concern control-plane HA, which is a separate topic in [High availability](/docs/operations/high-availability/).

To run the check, open **Settings**, then **Usage**. Find **HA readiness**, and click **Run readiness check**. The section shows **Healthy nodes** and **Ready for HA**.

:::caution
Click **Run readiness check** at least one time. Geass creates the readiness report only when you first click **Run readiness check**. Until then, every HA database waits, and the shared-server **High availability** switch stays off. The button creates the report if it does not exist, and does nothing if it exists.

After the report exists, the operator keeps it current. The operator calculates the report again when a node, a storage class, or the Geass cluster changes. So it is enough to add a third node. An HA database that waits checks again every 30 seconds.
:::

HA is built for PostgreSQL, MySQL, and MariaDB. The instances and failover of PostgreSQL come from CloudNativePG. Those of MySQL come from MOCO, and those of MariaDB come from the MariaDB operator. Redis runs as one instance. So Geass refuses a Redis database that asks for HA, or for more than one instance. Its Ready condition reads "High availability requires PostgreSQL, MySQL, or MariaDB: Redis runs as a single instance. Turn it off, or choose PostgreSQL, MySQL, or MariaDB". The create calls of the dashboard refuse it before they start, with the same words. SQLite refuses HA with "SQLite does not support high availability".

Each HA instance reserves the CPU and memory that you selected, so an HA database needs three times the capacity.

### High availability for MySQL

A MySQL database that asks for HA runs on [MOCO](https://github.com/cybozu-go/moco), a MySQL operator. Geass installs MOCO the first time that a database asks for it, as it installs the Postgres operator. The installation takes a few minutes. MOCO needs cert-manager, which is on unless you turned off the add-on. If cert-manager is off, the database reads "High availability requires cert-manager" until you turn it on.

- **Instances.** Three, or five if you set `instances` to 5. MOCO runs an odd number of servers. Geass refuses any other number with "High availability requires 3 or 5 MySQL instances".
- **One primary, the rest replicas.** Writes go to the primary, and replicate to a replica before the primary acknowledges them (semi-synchronous replication). So an acknowledged write survives the loss of the primary. Apps connect to the Service of the primary, `moco-<ID>-primary`. Geass publishes this Service as the host of the database. Reconnect on an error. The connection to a lost primary breaks, and the Service points at the new primary a few seconds later.
- **Failover.** When the primary is lost, MOCO promotes the most advanced replica. Geass tested failover on three machines. When the pod of the primary was killed, or the whole machine that it ran on was stopped, a replica was promoted within a minute. No acknowledged write was lost. A primary whose MySQL froze without dying took about three minutes to be replaced. A server that was lost and comes back becomes a replica again by itself. MOCO reports a replica that holds changes that the primary does not have as errant, and that replica must be built again.
- **At least 1 GB for each instance.** The MySQL of MOCO needs about half a gigabyte before it has a single row. So Geass gives an HA database at least 1 GB of memory for each server, whatever size you selected. The capacity check counts that memory.
- **One instance or HA for life.** A single MySQL server and a MOCO cluster are not the same servers, so a MySQL database cannot change from one to the other. If you ask for the change, the message is "High availability requires a new database". Create a new database and move the data.
- **Backups** are the daily copies that the other single-server engines have, taken from the primary. MySQL has no restore to a moment.
- **Spreading.** The servers prefer different nodes. This is a preference, not a rule. In a region with fewer than three nodes, the database still runs, on the nodes that the region has. The database then does not survive the loss of one of those nodes. See [High availability](/docs/operations/high-availability/#where-the-instances-run).

### High availability for MariaDB

A MariaDB database runs on the [MariaDB operator](https://github.com/mariadb-operator/mariadb-operator). Geass installs the operator the first time that a MariaDB database is made. The installation takes a few minutes, and it does not need cert-manager. The HA request decides how many servers the database runs.

- **Without HA**, the database is one server on a volume, at `<ID>.<namespace>.svc`.
- **With HA**, the database is three servers, or five if you set `instances` to 5. Geass refuses any other number with "High availability requires 3 or 5 MariaDB instances". As for MySQL on MOCO, a MariaDB database is one instance or highly available for its life. A request to change says "High availability requires a new database".
- **One primary, the rest replicas.** Writes go to the primary, and reach a replica before the primary acknowledges them (semi-synchronous replication). If no replica answers, the primary stops waiting after ten seconds, so a lone primary keeps working. Replicas refuse writes. Apps connect to the Service of the primary, `<ID>-primary`, which Geass publishes as the host of the database. `<ID>-secondary` reaches the replicas, for reads that can be a moment behind. Reconnect on an error. The connection to a lost primary breaks, and the Service points at the new primary a few seconds later.
- **Failover.** When the pod of the primary is lost, or its server stops answering, the operator promotes a replica. In a test, Geass killed the pod of the primary. Ten seconds later, a replica was the primary, and a write made then was on every server. The old primary came back as a replica by itself.

  The operator cannot promote a replica when a whole machine goes silent. The replicas lose their primary without a notice, and then they are neither ready nor replicating. The operator promotes only a replica that is ready and replicating. Geass covers that case. When the machine of the primary has not been ready for a minute, Geass selects the replica that has seen the most of what the primary wrote, on a machine that is ready. Geass then tells the operator to make that replica the primary.

  Until the promotion is done, the database reads "Promoting a replica: the primary's machine is not responding". A switchover needs the old primary to answer. So a switchover that cannot reach the old primary never finishes. After ten minutes, Geass stops the switchover, and names the primary that is serving as the primary again. Geass then does not ask for another switchover for half an hour.
- **A replica that cannot catch up** is built again by the operator, from a copy of a ready replica. Geass keeps the template for that copy, `<ID>-recovery`. The template never runs on a schedule, and the volume of the copy is made only when the copy is used.

  With Longhorn, the copy is kept on the replicated storage class, not on the own storage class of the servers. The volume of a server has its one copy on the machine that the server runs on. A copy kept the same way would be on the machine of the replica that it was taken from. But the job that builds the replica mounts the copy and the volume of the replica, and runs where the volume of the replica is. If the two volumes are on different machines, that job is never scheduled, and the replica stays down. Without Longhorn, the volumes are on local storage. A replica can then only be built again on the machine that its copy was taken on.
- **The application's login** is `app`. The operator makes this login with its database after every server is up. Until then, the database is not reported ready, even though a server is running.
- **Starting a server** is given five minutes. A server that is stopped while it sets up its data directory starts again with no root password. A later start does not repair that server. On a busy node, the own limit of the operator, 50 seconds, stopped a server in this way.
- **Spreading.** The servers prefer different nodes, as for MySQL. This is a preference, not a rule. See [High availability](/docs/operations/high-availability/#where-the-instances-run).
- **Backups** are a physical copy every night, with the binary logs of the primary archived beside it. So you can restore the database to any moment up to about fifteen minutes ago, as for Postgres. A MariaDB database that runs as one server has the daily copies that the other single-server engines have, taken with `mariadb-dump`. See [Backups and restore](/docs/guides/backups-and-restore/#mariadb-with-high-availability-copies-and-archived-logs).
- **Compliance tags.** The operator makes some pods itself, for example the copy used to build a replica again. On Kubernetes 1.36 or later, a policy gives these pods the node selector of a project that requires tags. See [Compliance node tags](/docs/guides/compliance-tags/).

## Where a database runs

A database keeps its data on the nodes that it is first provisioned on, and Geass does not move it. When Geass first creates the database, Geass decides where the database runs. Geass records the decision in the annotation `geass.dev/node-placement`, and keeps to it. Three things make up the decision:

- **The region.** The region of the project, or the default region of the cluster when the project has none.
- **Run on.** The node tags that you selected under **Scale**, **Run on**. A node needs all of them. Only a dashboard admin who is signed in can select them. Geass refuses API tokens, so the command line and the MCP server cannot set them.
- **The project's required tags.** A project can require compliance tags, for example `hipaa`. After that, Geass places every database that it provisions for the project on nodes that carry all of these tags. These tags are in addition to the **Run on** tags. You set the tags on the project, not on the database. See [Compliance node tags](/docs/guides/compliance-tags/).

For a database in a project that requires tags, Geass does these things:

| Part | What Geass does |
| --- | --- |
| The pods of the database | Gives them a node selector `tag.geass.dev/<tag>: "true"` and a toleration for each required tag, next to the region and the **Run on** tags. |
| The volume | With Longhorn, a replicated volume (PostgreSQL, MySQL, and SQLite) uses the class `geass-tagged-<hash>`. This class keeps every copy on nodes in the region of the project that carry all the tags. An HA PostgreSQL volume uses `geass-single`, one copy on the node where the instance runs. Without Longhorn, the volume stays on the node that its pod runs on. See [Storage and volumes](/docs/guides/storage/#compliance-tags-and-storage-classes). |
| Backup, restore, and setup jobs | Run in the region of the project, on nodes with the required tags. They do not use the **Run on** tags, so they take no room on nodes reserved for the database. |
| The backups | Go to your S3 bucket, which is outside the cluster. See [Backups and restore](/docs/guides/backups-and-restore/). |
| An external database | Is not in the cluster, so required tags do not reach it. |

A database on a shared server follows the placement of the server, not of the project. A project that requires tags can use a shared server only when the server runs on nodes with all of those tags. See [Shared database servers](/docs/guides/shared-database-servers/#projects-that-require-node-tags).

### Databases that already exist

A database provisioned before the project required a tag stays where it is. So the dashboard and the API refuse to add a tag to a project while one of its in-cluster databases does not run on nodes with that tag. The message names the databases. A database that is still a draft does not count. Geass places it with the tags when you first deploy it.

To move a database that has backups:

1. Make sure that the database has a recent backup.
2. Delete the database. Geass first takes a final backup and keeps it for seven days.
3. Set the required tags on the project.
4. Restore the backup into a new database. Geass places the new database with the tags of the project at that moment. The new database does not copy the **Run on** choice of the original.

A change to the project with `kubectl` skips the check, so a database can end up outside the tags. `geass compliance show PROJECT` lists each in-cluster database with `compliant` and the tags that it is missing. [Compliance node tags](/docs/guides/compliance-tags/#projects-that-already-have-databases) explains the order above.

To read where Geass placed a database, run this command:

```bash
kubectl get geassdatabase orders -n geass-system \
  -o jsonpath='{.metadata.annotations.geass\.dev/node-placement}{"\n"}'
```

```text
{"region":"eu","tags":["storage"],"required":["hipaa"]}
```

`tags` are the **Run on** tags, and `required` are the tags of the project. A database provisioned before placement existed has an empty `{}`: it runs anywhere.

## External databases

An external database is a database that Geass does not run. Geass publishes the same connection Secret for it, so apps connect to it like any other database. You either record a database that you already host, or, for PlanetScale, have Geass create one in your PlanetScale organization.

The database list of the dashboard offers two external rows: **AWS RDS** and **PlanetScale**. **AWS RDS** is grayed out until the project has an available AWS connection ("Add an AWS connection in platform settings"). **PlanetScale** is always grayed out and reads "Coming soon", whatever connections the project has. Create PlanetScale databases with the REST API or a manifest, as described under [PlanetScale](#planetscale).

External databases have no storage, size, or backups in Geass. Use the backups of your provider.

### Connect to a database you host

With `mode=Connect`, Geass never creates or deletes the remote database server. No code calls the AWS RDS API, so **AWS RDS** is a label for any Postgres that you host on AWS. You create the instance in AWS. In Geass, you record its host, port, login, and password. Geass stores a `connectionRef` to an AWS connection, but does not use it to reach the database. The project needs no connection at all.

:::caution
Create external databases with the REST API or a manifest, as shown below, and delete any draft that the dashboard made. The dashboard cannot finish an external database. Its **AWS RDS** row saves a draft with the provider, but with no host, port, login, or password. The dashboard has no field to enter them. The draft then reports "external database host, username, and password are required".
:::

To connect to a Postgres database on AWS, send these requests:

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

Geass stores the password in a Secret named `<ID>-external` in `geass-system`, and reads it from there. If you leave out `port`, Geass uses the default port of the engine. After the deploy, the connection Secret appears with the same keys as for any database. Connect apps with **Add Reference**.

### PlanetScale

With `mode=Create` and `provider=PlanetScale`, Geass uses the PlanetScale connection of the project to call the PlanetScale API, and creates the database for you. The connection is an organization and a service token. `engine` must be `MySQL` or `Postgres`, and decides which kind of PlanetScale database Geass creates. Any other engine fails with "PlanetScale hosts MySQL and Postgres databases, not Redis". With `mode=Connect`, PlanetScale works like the AWS case above: you give the host, login, and password of a database that exists.

When you deploy a `mode=Create` database, Geass does these steps:

1. Geass creates a PlanetScale database named after `databaseName`, or after the resource ID if you leave out `databaseName`. If a database with that name exists, Geass does not create one. An existing database must be of the same kind. If it is not, Geass reports that it "already exists as" the other kind, and creates nothing.
2. Geass waits for PlanetScale to finish provisioning the database, which takes minutes. Until then, the status reads "PlanetScale database is starting", and Geass does not count this as a failure. After 30 minutes, the database fails with "PlanetScale database did not become ready within 30m0s; check the database in PlanetScale".
3. Geass creates a login on the `main` branch of the database, named after the resource ID. For MySQL, the login is a branch password. For Postgres, the login is a role. The role inherits the `postgres` role of PlanetScale, so the application can run migrations.
4. Geass publishes `<ID>-connection` with the host, login, and password that PlanetScale returned. For MySQL, the `database` key is the database that Geass created. For Postgres, the `database` key is the own database of the role. The port is `externalPort` if you set one. Otherwise, the port is `3306` for MySQL and `5432` for Postgres.

PlanetScale shows a password one time, so Geass keeps it only in the connection Secret. If Geass cannot store a new login, Geass revokes that login and issues another login on the next pass.

To select the size that Geass creates the database with, use `clusterSize` (`spec.clusterSize` in a manifest), for example `PS_10`. If you leave it out, PlanetScale applies its own default. The size applies only when Geass creates the database. After the connection Secret exists, the update route refuses a change to the size with "A PlanetScale database is sized when it is created, so its size cannot change after that". To list the sizes that your organization can use for an engine, ask the API. Only workspace admin tokens can do this:

```bash
curl "https://geass.example.com/api/planetscale/cluster-sizes?project=proj-1a2b3c4d&connection=prod-planetscale&engine=MySQL" \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json"
```

The response is `{"items":[...]}`. Each item has these fields:

- `name`, which is the value to send as `clusterSize`.
- `displayName`, `cpu`, `ramBytes`, `development`, `production`, `metal`, `provider`, and `architecture`.
- `storageBytes`, which is absent for elastic storage.

When PlanetScale gives prices, `monthlyUsd` is the cost of the highly available cluster (a primary and two replicas). `replicaMonthlyUsd` is the cost of one node. Both prices are for the default region of the organization. The PlanetScale API states no unit, and Geass reads the prices as whole US dollars per month. Geass caches the list for five minutes. When you create or update the database, Geass refuses a size that PlanetScale does not offer for the engine ("PlanetScale does not offer cluster size ... for MySQL databases"). If Geass cannot reach PlanetScale to check, Geass accepts the size, and PlanetScale checks it when it creates the database.

To create a MySQL database in PlanetScale and deploy it, send these requests:

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

Here is the same database as a manifest. An object created this way starts provisioning immediately:

```yaml title="orders-ps.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassDatabase
metadata:
  name: orders-ps
  namespace: geass-system
spec:
  project: proj-1a2b3c4d
  environment: production
  engine: MySQL
  placement: External
  provider: PlanetScale
  mode: Create
  connectionRef:
    name: prod-planetscale
  databaseName: orders
  clusterSize: PS_10
```

For a Postgres database, send `engine=Postgres`, and take the size from the list for `engine=Postgres`. The two kinds offer different sizes.

:::caution
Use the API or a manifest for PlanetScale. When you delete the Geass database, remove the PlanetScale database and its login in PlanetScale yourself. A delete of the Geass database does not delete them. PlanetScale support is experimental, and the dashboard cannot start it. The **PlanetScale** row is grayed out ("Coming soon"). After a PlanetScale connection exists, the **Connectors** card says "PlanetScale databases are coming soon".
:::

### Deleting an external database

When you delete an external database, Geass removes only its own records and Secrets. Geass does not delete the remote database, its credentials in PlanetScale, or the `<ID>-external` Secret.

## Delete a database

1. Open the database, and select **Settings**, then **Delete database**.
2. Under "Type `<ID>` to delete", enter the resource ID. The dashboard asks for the ID, not the display name.
3. Click **Delete database**.
4. In the confirmation, click **Delete permanently**. The dashboard says "Resource deleted".

With the API, send the resource ID as `confirmName`:

```bash
curl -X POST https://geass.example.com/api/databases/orders/delete \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json" \
  --data-urlencode confirmName=orders
```

The section tells you what happens to the data:

- If the database has backups running, Geass first takes a final backup and keeps backups for seven days. The section says "Geass takes a final backup first and keeps it for 7 days (Project settings → Backups)."
- If the database has no backups, the section says "This database has no backups. Deleting it permanently destroys its data."

See [Backups and restore](/docs/guides/backups-and-restore/).

Geass removes these items, by engine:

| Engine | Removed |
| --- | --- |
| PostgreSQL | The CloudNativePG cluster and, with it, its volumes, plus the Secrets that Geass made |
| MySQL | The StatefulSet, its Services, the volume claim `data-geass-mysql-<ID>-0`, and the Secrets. With HA, the MOCO cluster and its volume claims. A database that still runs on the old Bitnami chart loses the Helm release and the claim `data-geass-mysql-<ID>-0`. For that database, check `kubectl get pvc -n <namespace>` afterwards |
| MariaDB | The `MariaDB`, its backup objects, the volume claims `storage-<ID>-<index>`, and the Secrets |
| Redis | The StatefulSet, its Services, and the Secrets. The data was in memory |
| SQLite | The Deployment, the Service, the volume claim `<ID>-sqlite`, and the Secrets |
| External | The records and Secrets of Geass only |

Geass removes the connection Secret of a deleted database. So any app that references that Secret fails to start the next time that the app restarts. Remove the reference from the app first.

A delete of a project deletes its databases, its connections, and the namespaces that they run in, in one step. So Geass might not manage a final backup, and it cannot restore one afterwards. Restore what you need first. See [Backups and restore](/docs/guides/backups-and-restore/).

Databases that Geass copies into a pull request environment are empty copies with one instance and no backups. See [Pull request environments](/docs/guides/pull-request-environments/).

## Command line and AI agents

The `geass` command line and its MCP server read databases through the versioned API. They list databases, show their settings and status, and show how to connect. Neither creates, deploys, or restores a database. For those tasks, use the dashboard, or `geass api`. Databases do not scale to zero. Scale to zero applies to services only. See [Command line](/docs/guides/command-line/), [CLI commands](/docs/reference/cli/#geass-databases), [MCP server](/docs/guides/mcp-server/), and [MCP tools](/docs/reference/mcp-tools/#list_databases).

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

- `list` and `get` work with any workspace token. A token that cannot change things sees a redacted copy, with no engine, version, host, or connection details. A project token cannot read databases at all.
- `connection` needs a workspace token with Full access, and works after the database is ready. Before that, it fails with "no connection details yet: the resource has not finished setting up".
- Geass never returns the password and the connection string unless you ask. `--reveal` adds `password` and `uri`, and the operator logs who revealed them for which database. The MCP server has no way to ask. `database_connection` never returns a password or a connection string. So an agent can find where a database is, but it cannot log in to it.
- `geass databases list` includes shared servers, which have no project. Logical databases have no command of their own. `geass api GET /logical-databases` lists them, and `geass api GET /logical-databases/orders/connection` shows how to reach one. `delete_resource` also deletes logical databases and buckets.
- A database whose `spec.deploy.enabled` is not `true` is listed as `draft`. See [Create a database with a manifest](#create-a-database-with-a-manifest).

For buckets, `geass stores list` and `geass stores get` do the same, and neither shows credentials. See [Object storage](/docs/guides/object-storage/#command-line-and-ai-agents).

## Options

Each row gives the `spec` field and the form field that the API accepts.

| Field | API field | Meaning | Default |
| --- | --- | --- | --- |
| `project` | `project` | The project that owns the database. Empty means a shared server | Required for project databases |
| `environment` | `environment` | The project environment | Required |
| `engine` | `engine` | `Postgres`, `MySQL`, `MariaDB`, `SQLite`, or `Redis` | `Postgres` in the API |
| `placement` | `placement` | `InCluster` or `External` | `InCluster` |
| `provider`, `mode` | `provider`, `mode` | External only: `AWS` or `PlanetScale`, and `Create` or `Connect`. `Create` does something only for PlanetScale | None |
| `clusterSize` | `clusterSize` | PlanetScale only: the size that Geass creates the database with, for example `PS_10`. It does not resize a database later | The default of PlanetScale |
| `highAvailability` | `highAvailability` | Three instances. Needs the readiness check. PostgreSQL, MySQL, and MariaDB only. Redis and SQLite refuse it | Off |
| `instances` | none | Number of instances. HA forces at least 3. MySQL and MariaDB run 1, 3, or 5. Redis runs one | 1 |
| `version` | `version` | Postgres image tag | `16` |
| `storageSize` | `storageSize` | Disk for each instance, at least `1Gi` | `10Gi` (`1Gi` for SQLite) |
| `resources` | `cpu`, `memory` | CPU and memory for each instance | 250m and 512Mi (SQLite 100m and 128Mi) |
| `backup.enabled`, `backup.retentionDays` | `backups` (`on` or `off`), `backupRetentionDays`, read on update only | Backups. See [Backups and restore](/docs/guides/backups-and-restore/) | Off in a manifest. On when created from the dashboard or API |
| `restoreFrom` | none (use `POST /api/databases/restore`) | Restore from the backups of another database. Read one time, at creation | None |
| `runOn.tags` | `runOnTags` | Node tags to run on. The tags that the project requires also apply. Fixed at the first deploy. Only a dashboard admin who is signed in can set it: Geass refuses API tokens | Any node in the region (that carries the required tags of the project, if any) |
| `connectionRef` | `connectionRef` | An AWS or PlanetScale connection | None |
| `databaseName` | `databaseName` | The database in the server | The resource ID |
| `externalHost`, `externalPort` | `host`, `port` | External only | The port of the engine |
| `username`, `passwordSecretRef` | `username`, `password` | External only | None |

The [resource reference](/docs/reference/resources/overview/) has the full schema.

## What happens under the hood

Each database is a `GeassDatabase` in `geass-system`. Its workloads live in the namespace of the project environment, `<project ID>-<environment>`. Before the first deploy, Geass parks the object with the message "Resource is configured and waiting for deployment". A deploy records a snapshot of the settings. Pending edits stay frozen until you deploy them. Backup settings are the exception: they follow the current settings immediately. When Geass first provisions an in-cluster database, it writes these annotations on the object:

- `geass.dev/node-placement`, which records where the database runs. See [Where a database runs](#where-a-database-runs).
- `geass.dev/storage-class`, for engines with a volume. See [Storage and volumes](/docs/guides/storage/#the-class-is-pinned-per-database).

Each engine runs as follows:

- **PostgreSQL.** On first use, Geass installs the chart `geass-cloudnative-pg` (version 0.23.2) into `cnpg-system`. Each database is a CloudNativePG `Cluster` named after the resource ID. Its pods are `<ID>-1`, and also `<ID>-2` and `<ID>-3` for HA. The read-write Service is `<ID>-rw`. The `<ID>-bootstrap` Secret holds the `app` login.
- **MySQL.** A StatefulSet `geass-mysql-<ID>` of one pod from the official image, on the volume claim `data-geass-mysql-<ID>-0`, behind the Service `geass-mysql-<ID>`. The root and `app` logins and the database are created from the `<ID>-bootstrap` Secret, the first time that the pod starts. A database made on an earlier development build still uses the Bitnami chart (a `HelmChart` `geass-mysql-<ID>` in `kube-system`). Geass leaves that database as it is.
- **MariaDB.** On first use, Geass installs the charts `geass-mariadb-operator-crds` and `geass-mariadb-operator` (version 26.10.1) into `mariadb-system`. The database is a `MariaDB` named after the resource ID. Its pods are `<ID>-0`, and with HA also `-1` and `-2`, or up to `-4`. Each pod is on a claim `storage-<ID>-<index>`. The Services are `<ID>` and, with HA, `<ID>-primary` and `<ID>-secondary`. The operator makes the `app` login and the database from the `<ID>-bootstrap` Secret. With HA, there is also a suspended `PhysicalBackup` named `<ID>-recovery`.
- **MySQL with high availability.** On first use, Geass installs the chart `geass-moco` (version 0.27.0) into `moco-system`. The database is a MOCO `MySQLCluster` named after the resource ID. Its pods are `moco-<ID>-0` to `moco-<ID>-2` (or `-4`), each on a claim `mysql-data-moco-<ID>-<index>`. The Service that reaches the primary is `moco-<ID>-primary`, and `moco-<ID>-replica` reaches the replicas. MOCO makes only its own logins. So a job `<ID>-login-<id>` makes the `app` login and the database from the `<ID>-bootstrap` Secret, as the writing login of MOCO. The connection Secret holds the `app` login, not a root login.
- **Redis.** A StatefulSet `geass-redis-db-<ID>-master` of one pod from the official image, with a password and no volume, behind the Service `geass-redis-db-<ID>-master`. A Redis database made on the Bitnami chart is moved to this setup the next time that it is reconciled.
- **SQLite.** A Deployment and a Service named `<ID>`, and a volume claim `<ID>-sqlite`, run rqlite.
- **External.** No workloads. Geass writes the connection Secret from the host, login, and password that you gave. For a PlanetScale database that Geass creates, Geass writes the Secret from the login that it issued.

```bash
NS=proj-1a2b3c4d-production
kubectl get geassdatabase orders -n geass-system \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}{"\n"}'
kubectl get cluster.postgresql.cnpg.io,pods,pvc -n "$NS"
kubectl get helmcharts.helm.cattle.io -n kube-system
kubectl get pods -n cnpg-system
kubectl get mysqlcluster.moco.cybozu.com -n "$NS"
kubectl get pods -n moco-system
```

Geass stops provisioning that fails repeatedly. After five counted failures in a row, or three minutes of them, the status becomes "Provisioning failed after repeated errors. Update settings or platform configuration, then save or redeploy to try again." Geass then stops retrying until you change something or click **Retry**. Geass does not count waiting states, for example "is starting", "Installing ... chart", the HA gate, and a restore in progress.

## Troubleshooting

The messages below are the text of the Ready condition. To read the condition, run `kubectl get geassdatabase <ID> -n geass-system -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}'`. The dashboard rewrites some messages into plainer sentences, as noted, and shows a general note for the rest.

**The database stays at Draft.**
The database is not deployed. In the top bar, click **Deploy changes**.

**"Platform setup is in progress." (raw message "Installing Postgres operator chart").**
This database is the first PostgreSQL database on the cluster, and Geass is installing the operator. Wait. If the installation does not finish, check `kubectl get helmcharts.helm.cattle.io -n kube-system` and the pods in `cnpg-system`.

**"Not enough capacity for this size. Choose a smaller one or add capacity." (raw message "This PostgreSQL database needs about 250m CPU and 512Mi memory...").**
No node can hold the database. Select a smaller CPU and memory, or add a node. Geass refuses the deploy until there is room. See [Usage and capacity](/docs/guides/usage-and-capacity/).

**"High availability requires a cluster with at least three healthy nodes".**
Run the readiness check, as described under [The readiness gate](#the-readiness-gate). If you already ran it with fewer nodes, delete the report and run the check again.

**"Storage is starting", then "Storage is unavailable".**
Longhorn is installing or does not work. Geass waits up to 15 minutes. "Storage is unavailable" counts as a failure, so the status then becomes "Provisioning failed after repeated errors". One wait has no limit. When Longhorn runs but the storage class for the region or the required tags of the project does not exist yet, the database keeps saying "Storage is starting". See [Storage and volumes](/docs/guides/storage/#troubleshooting).

**"Provisioning failed after repeated errors".**
To read the failing attempt, open the **Deployments** tab. Fix the cause, and then click **Retry**. With the API, use `POST /api/databases/<ID>/retry-provision`.

**A MySQL or Redis database shows "Object storage is starting." for a long time.**
For every engine, the dashboard shows the raw message "Service is starting" with these words. The pod of the server is not ready, and its image is not the problem. A pod that cannot get its image says so, as the next entry describes. Run `kubectl get pods -n <namespace>`, and run `kubectl describe pod` on the pod of the database (`geass-mysql-<ID>-0` or `geass-redis-db-<ID>-master-0`). A first MySQL start sets up its volume and can take a few minutes. A large image that is still being pulled shows as `ContainerCreating`, and it is also "Service is starting".

**"Could not pull the database image redis:7.4.11-alpine (ImagePullBackOff)" (or `mysql:8.4.11`, or `ErrImagePull`, or `InvalidImageName`).**
The node cannot get the image that its MySQL or Redis server runs from. Geass pulls `mysql` and `redis` from Docker Hub. `ErrImagePull` is a pull that failed a moment ago, and `ImagePullBackOff` is the wait before the next try. The two alternate. Read the words of the registry in the events of the pod of the database:

```bash
kubectl -n <namespace> describe pod geass-redis-db-<ID>-master-0
```

These are the usual causes:

- A node that cannot reach Docker Hub, because of a proxy, a firewall, or DNS.
- The Docker Hub limit on anonymous pulls (`toomanyrequests`).
- A cluster with no route to the internet. In that cluster, the images must be loaded onto the nodes or served by a registry mirror.

The kubelet keeps trying. So when a node can pull the image, the database becomes ready by itself. A database that cannot pull its image for 10 minutes is **Failed**. This time is longer than for other failures, because the kubelet waits minutes between tries. The message stays in the attempt history of the database under **Deployments**. Fix the cause, and then click **Retry**.

**"SQLite does not support high availability", or "High availability requires PostgreSQL, MySQL, or MariaDB: Redis runs as a single instance".**
SQLite and Redis run one instance. Turn off HA and set `instances` back to 1, or select PostgreSQL, MySQL, or MariaDB.

**"High availability requires 3 or 5 MySQL instances, not N" (or "... MariaDB instances ...").**
MOCO and the MariaDB operator run an odd number of servers. Set `instances` to 3 or 5, or turn off HA.

**"High availability requires a new database: this MySQL database runs one instance ..." (or "... runs three or more instances ...").**
A MySQL or MariaDB database cannot change between one instance and high availability. Change the setting back, or create a new database and move the data.

**"High availability requires cert-manager".**
MOCO needs cert-manager. Turn on the cert-manager add-on. It is on by default.

**A MySQL HA database says "Installing MySQL operator chart" for a long time.**
Geass is installing MOCO. Check `kubectl -n kube-system get helmcharts.helm.cattle.io geass-moco` and `kubectl -n moco-system get pods`. A node that cannot reach `cybozu-go.github.io` (the chart) or `ghcr.io` (the MOCO images) cannot install MOCO.

**"Replacing the Redis chart with Geass's own server".**
Geass is moving a Redis database made on the Bitnami chart to Geass's own server. The database waits for Helm to remove the chart, which takes about a minute. Geass does not count this wait as a failure. If the message stays for a long time, look at the `HelmChart` `geass-redis-db-<ID>` in `kube-system`, and at the job that removes its release (`kubectl -n kube-system get helmcharts,jobs`).

**"external database host, username, and password are required".**
An external database has none of these values. Create it through the API with the fields in [Connect to a database you host](#connect-to-a-database-you-host).

**"PlanetScale database is starting" for a long time.**
PlanetScale is still provisioning the database, which takes minutes. Geass waits up to 30 minutes, and then fails with "PlanetScale database did not become ready within 30m0s; check the database in PlanetScale". Check the database in PlanetScale, and then click **Retry**.

**"a PlanetScale connection is required", "PlanetScale token and organization are required", or "PlanetScale hosts MySQL and Postgres databases, not ...".**
A `mode=Create` PlanetScale database needs these values:

- `connectionRef` to a PlanetScale connection of the same project, with a service token and an organization.
- An `engine` of `MySQL` or `Postgres`.

See [PlanetScale](#planetscale).

**"PlanetScale database ... already exists as mysql, not postgresql" (or the reverse).**
A PlanetScale database with that name exists with the other engine. Geass never reuses it. Use another `databaseName`.

**"cloud connection ... was not found" or "is scoped to project ...".**
The `connectionRef` names a connection that does not exist or that belongs to another project. Connections are scoped to one project.

**"storage can be increased but not decreased once deployed" and similar size errors.**
See the table in [Storage and volumes](/docs/guides/storage/#resize-a-database-volume).

**The app cannot connect.**
Check these items, in order:

1. You deployed the app after you added the reference ("Deploy to apply it").
2. The database is **Ready**.
3. The app is in the same project and environment, or you copied the connection details yourself.
4. The app uses the `uri` of the Secret, with the `host` that ends in `.svc`.
5. Network isolation allows the connection from where the app runs. See [Network isolation](/docs/guides/network-isolation/).

**The console shows "no database pods are running yet".**
The database is not running. Check its status and pods.

**A SQLite database answers `503` (rqlite logs "not part of a stable configuration") after its pod was replaced.**
A SQLite server made before Geass gave rqlite a fixed node ID (`-node-id`) names its node after the host name of the pod. The pod of a Deployment has a new name every time that it is replaced, for example after a drain, a rollout, or a restart into the [sandbox](/docs/operations/sandboxed-runtime/#servers-that-already-run). The new node is not in the raft configuration kept on the volume. So rqlite does not elect itself, and it stays unavailable. A server that this release makes starts with `-node-id <ID>`. A server made earlier needs one of two fixes: recover its raft configuration, or move its data to a new server. The rqlite documentation describes the recovery, but it was not tried here. Geass has no tool for either fix.

**A MySQL server in the sandbox says Ready, but its `app` login and the password in `<ID>-bootstrap` are refused.**
At 512 MiB, MySQL was killed for lack of memory during its first start under gVisor. It was left with only `root@localhost`, and no password for it. See [What it costs](/docs/operations/sandboxed-runtime/#what-it-costs). A server made with 1 GiB started cleanly. Delete the broken server and its volume, and make it again with more memory.

**A restored database stays at "Restoring database from backup".**
See [Backups and restore](/docs/guides/backups-and-restore/#failure-modes).

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
