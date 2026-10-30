---
title: "Shared database servers"
description: "Run one Postgres or MySQL server for many projects. Give each project its own database and login, and compare the tradeoffs with dedicated databases."
---

A shared database server is one PostgreSQL or MySQL server that many projects use. Each project takes a logical database on the server. The project gets a login that reaches its own database and no other. Use a shared server when you have many small projects or environments that each need a database. You then pay the CPU and memory of one server, not of one server for each project.

An admin sets up a server. You can set up more than one server, for example one for each engine or region. Project members then add a database on the server from the same **+ Add resource** dialog that they use for other databases.

## Shared or dedicated

| | Dedicated database | Shared server |
| --- | --- | --- |
| What a project gets | Its own server (PostgreSQL, MySQL, MariaDB, Redis, or SQLite) | A database and a login on a server that others use |
| CPU and memory | Reserved for each database, 0.25 CPU and 512 MB by default | One reservation for the whole server |
| Isolation | Its own server and disk | A shared server and disk. A login reaches only its own database |
| Backups and restore | Yes, for PostgreSQL, MySQL, MariaDB, and SQLite | None |
| Engines | PostgreSQL, MySQL, MariaDB, Redis, and SQLite | PostgreSQL and MySQL |
| High availability | Through the API for PostgreSQL, MySQL, and MariaDB | A switch when you set the server up |
| Removal | Delete the database | Blocked while a project has a database on it |

Use a dedicated database in these cases:

- The data needs backups and point-in-time restore.
- One project needs its own size or version.
- The load of one project must not affect other projects.

See [Databases](/docs/guides/databases/).

## Before you start

- To set up or remove a server, you must be a dashboard admin.
- The cluster needs room for the server. The default is 0.25 CPU, 512 MB, and 10 GiB of storage.
- For a high-availability server, the cluster needs at least three healthy servers, and you must run the readiness check. See [Instances and high availability](/docs/guides/databases/#instances-and-high-availability).
- If your cluster uses regions, a shared server serves only projects in its own region. A project without a region uses the default region of the cluster. See [Scaling and placement](/docs/guides/scaling-and-placement/).
- A project that requires compliance node tags can use a server only when the server runs on servers with those tags. See [Projects that require node tags](#projects-that-require-node-tags).
- If the cluster has [organizations](/docs/guides/organizations-and-workspaces/), a shared server holds the databases of one organization only. See [Organizations](#organizations).

## Set up a shared server

1. Open **Settings**, and then **Databases**.
2. Select **Set up a server**. The **Shared database server** sheet opens.
3. Complete the form.
4. Click **Set up server**. The dashboard says "Server created".

| Field | Meaning | Default |
| --- | --- | --- |
| **Engine** | **PostgreSQL** or **MySQL** | PostgreSQL |
| **Name** | Optional resource ID that projects see when they select a server. Lowercase letters, numbers, and hyphens | `cluster-postgres` or `cluster-mysql` |
| **Region** | Where the server runs. Shown only when the cluster has regions | Default region |
| **CPU**, **Memory** | For each instance. 0.1 to 2 CPU, 128 MB to 4 GB | 0.25 CPU, 512 MB |
| **Storage** | Disk for each instance, 1 to 500 GB. The dashboard cannot change it after setup | 10 GB |
| **High availability** | Three instances, so the server survives the loss of a server. Unavailable with "Needs at least three healthy nodes." until the cluster is ready | Off |

The server starts immediately. It has no draft or **Deploy** step. The table on the page lists each server with its engine, its region, its usage (for example "2 databases · 2 projects"), and its status (**Starting** or **Ready**).

### Set up a server from the API

Use a workspace token with the admin role:

```bash
curl -X POST https://geass.example.com/api/shared-databases/create \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json" \
  --data-urlencode engine=Postgres \
  --data-urlencode name=shared-postgres \
  --data-urlencode cpu=500m \
  --data-urlencode memory=1Gi \
  --data-urlencode storageSize=20Gi
```

The reply is `{"database":"shared-postgres"}`. From the command line, send the same fields as JSON:

```bash
geass api POST /shared-databases/create -d '{"engine": "Postgres", "name": "shared-postgres", "cpu": "500m", "memory": "1Gi", "storageSize": "20Gi"}'
```

The API also accepts these fields, which the setup sheet does not have:

- `region` and `highAvailability=on`.
- `version`, the PostgreSQL version. The default is 16.
- `runOnTags`, which puts the server on servers with those tags. See [Projects that require node tags](#projects-that-require-node-tags).
- `org`, the [organization](/docs/guides/organizations-and-workspaces/#shared-things-an-organization-owns) that the server is for.

## Create a database on the server

1. Open the project and environment.
2. Click **+ Add resource**, and then select **Database**.
3. Select **Logical database**. When there is no server to use, the row shows "No server yet".
4. Select the server. A shared server shows "shared" next to its engine, for example **cluster-postgres**, PostgreSQL, shared. A dedicated server of the same project environment shows "this environment".

Geass creates the logical database and opens its **Settings**. The logical database starts to work immediately. It has no draft or **Deploy** step.

The row of a server is unavailable until the server is **Ready**. The dialog does not show shared servers in another region.

You can also use a dedicated PostgreSQL or MySQL database of the same project environment as the server. In that case, Geass uses the login of that server, and does not drop the database when you delete the logical database.

:::caution
Create only one logical database for each environment in the dialog. The dialog always names the database `app`. So a second logical database in the same environment uses the same database and login, and Geass resets the password. The first one then stops working. If you delete either one, Geass drops the shared database. To create more than one, use the API with different `database` names.
:::

### Create a database from the API

The API is the only way to select the database name and a connection limit:

```bash
curl -X POST https://geass.example.com/api/logical-databases/create \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json" \
  --data-urlencode project=proj-1a2b3c4d \
  --data-urlencode environment=production \
  --data-urlencode name=orders \
  --data-urlencode server=shared-postgres \
  --data-urlencode database=orders \
  --data-urlencode connectionLimit=30
```

The reply is `{"ok":true,"name":"orders","kind":"logical-databases"}`.

| Field | Meaning | Default |
| --- | --- | --- |
| `server` | The resource ID of the server | Required |
| `database` | The name that the app uses: letters, numbers, and underscores. It must not start with a digit | `app`, but only when you also leave out `name`. If you send `name` and no `database`, Geass refuses the request |
| `connectionLimit` | The number of connections that the login can hold, 1 to 1000. Shared servers only | 20 |
| `name` | The resource ID of the logical database | Generated |

## How apps connect

The **Add reference** action of the dashboard does not list logical databases. So you copy the connection string, and add it to the app as a secret variable.

1. Read the connection string with a workspace token that has Full access:

   ```bash
   geass api GET /logical-databases/orders/connection -q reveal=true
   ```

   The `uri` field is the connection string. Geass logs each reveal.
2. Open the **Variables** tab of the app.
3. Click **New Variable**.
4. Name the variable `DATABASE_URL`, and paste the value.
5. Turn on **Secret**.
6. Click **Add**.
7. Deploy the app.

See [Environment variables](/docs/guides/environment-variables/).

The connection string looks like this:

```text
postgresql://u_3f9a1c2b4d5e6f70:<password>@shared-postgres-rw.geass-system.svc:5432/proj_1a2b3c4d_production_app
```

On a shared server, these rules apply:

- The database name is `<project>_<environment>_<name>`, in lowercase, with an underscore in place of other characters. Geass shortens a name longer than 60 characters, and adds `d` in front of a name that starts with a digit.
- The login is `u_` and 16 hexadecimal characters. Nobody can guess it from the name of the project.
- The password is random. Geass creates it one time and keeps it.

The dashboard shows the server and the database name under **Settings**, **Logical database**. Both are read-only. The **Console** tab does not run queries on a logical database.

## Projects that require node tags

A project can require compliance node tags, for example `hipaa`. See [Compliance node tags](/docs/guides/compliance-tags/). A shared server holds data for more than one project, so the tags of one project do not move it. A project that requires tags can use a shared server only when the server runs on servers with all of those tags.

To make a server for such a project, send `runOnTags` and the `region` of the tagged servers when you create it. Geass checks that a server in that region has each tag:

```bash
curl -X POST https://geass.example.com/api/shared-databases/create \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json" \
  --data-urlencode engine=Postgres \
  --data-urlencode name=hipaa-postgres \
  --data-urlencode region=eu \
  --data-urlencode runOnTags=hipaa
```

Geass places the server one time, and does not move it later. A server made by an earlier release, before Geass recorded placement, can run anywhere. So Geass refuses every project with required tags on that server.

The **Add resource** dialog does not warn you. It lists every **Ready** server in the region of the project. If the server does not have the tags, the logical database fails with "This database server does not run on nodes tagged hipaa, which the project requires". After three minutes of failures, it shows "Provisioning failed after repeated errors". To fix the problem, do one of these:

- Delete the logical database, and create it on a server that has the tags.
- Change the tags of the project, and click **Retry**.

These limits apply:

- A server on compliance servers is not reserved for projects that require the tag. Other projects can also use it.
- On replicated storage, the copies of the disk of a shared server stay off compliance servers, although the server runs on them. See [Storage and volumes](/docs/guides/storage/#compliance-tags-and-where-copies-are-kept).

A logical database on a dedicated server of the project needs none of this. Geass places that server with the tags of the project.

## Organizations

When the cluster has [organizations](/docs/guides/organizations-and-workspaces/), a shared server holds the databases of one organization only. The data of one organization never shares a server with the data of another organization or of the platform.

| Server | Who can put databases on it |
| --- | --- |
| A server of an organization | Only the projects of that organization |
| A server of the platform | While organizations are on, only the projects of no organization. While organizations are off, every project |

The dedicated server of a project belongs to the project. These rules do not apply to it.

Geass does not set up a server for an organization automatically, because a server costs CPU, memory, and disk. A platform administrator sets one up with `org=<organization>`.

### Where it is checked

- **When you create a logical database.** Geass refuses a project of an organization on the server of the platform. A platform administrator sees "That server is the platform's and the project is in the organization …". The people of the organization see "That database server does not exist", because they cannot see the server of the platform.
- **When you move a project.** Geass refuses to give a project to an organization while the project has databases on a server of another owner. The message names the server, and asks you to move or delete the databases first.
- **When you give a server to an organization.** A platform administrator sends `POST /api/shared-databases/<server>/org` with `org=<organization>`. To give the server back to the platform, send `org=` empty. Geass refuses while the server holds a database of another owner, and names up to five of them.
- **For databases made without the dashboard.** Geass does not make a logical database that breaks the rule, however it was written. The logical database shows "… but a shared database server holds the databases of one organization only. It is not made: …". When the platform gives the server or the project to one owner, Geass makes the database within seconds.

### Databases that were there before

A logical database can already be on a server of another owner, for example because it was made before organizations were on. Geass keeps that database and continues to serve it. Geass deletes and moves nothing. The logical database shows a message that ends with "It was made before this was checked, so it is kept as it is and still served, and listed for the platform's administrators to move".

### List the databases that mix

A platform administrator can list the logical databases whose server belongs to another owner:

```bash
geass api GET /shared-databases/mixed
```

The reply has these fields:

- `items`: the logical databases that mix. `made` is `true` when the database is on the server already and still served, and `false` when Geass refused it.
- `servers`: the servers that hold one of these databases, with the owners of all their databases. An empty `org` is the platform.
- `enforced`: whether organizations are on.

The list applies the rule even while organizations are off. So it shows what Geass reports when you turn organizations on.

### An install that already has a mixed server

This case is a cluster that used one platform server for every project, and then gave projects to organizations. If you can, do these steps before you turn organizations on.

1. List the databases that mix with `geass api GET /shared-databases/mixed`.
2. For each server in `servers`, do one of these:
   - If all its databases belong to one organization, give the server to that organization with `POST /api/shared-databases/<server>/org`. Nothing moves.
   - If it holds the databases of more than one owner, select the owner that keeps the server. For each database of another owner, do the steps below.
3. If organizations are not on, turn them on. Geass continues to serve the databases that are left.

To move a logical database to a server of another owner:

1. Set up a server for that owner with `org=<organization>`.
2. Create a logical database on that server.
3. Copy the data with `pg_dump` and `psql`, or with `mysqldump` and `mysql`.
4. Point the services to the new connection string.
5. Delete the old logical database. This step drops its data from the old server.

Geass has no tool for the copy. Nobody has run this sequence from start to end on a real cluster. Geass tested the organization checks on MySQL servers only.

While organizations are on, you cannot give a project with databases on the platform server to an organization. You also cannot give that server to the organization while the project is in no organization. Move the data of the project first.

## The sandboxed runtime

The platform can run the code of organizations in a [sandboxed runtime](/docs/operations/sandboxed-runtime/), for example gVisor. In that case, the shared servers of organizations also run in the sandbox. The servers of the platform do not.

| Server | With the sandbox on |
| --- | --- |
| A MySQL server of an organization | Runs in the sandbox. A server that already runs moves into the sandbox when a platform administrator approves its restart. See [Servers that already run](/docs/operations/sandboxed-runtime/#servers-that-already-run) |
| A PostgreSQL server of an organization | Not made. PostgreSQL cannot run in the sandbox, so Geass refuses a new one. A server made before continues to run outside the sandbox, but Geass changes nothing on it |
| A server of the platform | Not affected |

When a single server moves into the sandbox, it is unavailable for about 40 seconds while it restarts.

:::caution
Give a MySQL server in the sandbox at least 1 GB of memory. With 512 MB, new servers ran out of memory during their first start. They showed **Ready**, but they had no working login, and their logical databases never finished. See [What it costs](/docs/operations/sandboxed-runtime/#what-it-costs).
:::

A database in the sandbox runs slower than outside it. Test a copy first.

## What a project can and cannot do

- A login reaches only its own logical database. Other databases on the server, for example the `postgres` maintenance database, keep the default access of the server.
- Network isolation between projects does not cover a shared server. Any service in the cluster can connect to the address of the server. The logins keep projects apart. See [Network isolation](/docs/guides/network-isolation/).
- Geass applies `connectionLimit` only when it first creates the database. To change the limit later, change the login on the server: `ALTER ROLE ... CONNECTION LIMIT` on PostgreSQL, or `ALTER USER ... WITH MAX_USER_CONNECTIONS` on MySQL.

## Delete a logical database

1. Open the **Settings** of the logical database.
2. Click **Delete logical database**.
3. Enter the resource ID of the logical database.
4. Confirm.

On a shared server, Geass drops the database and its login, and closes open connections. If the drop does not finish in three minutes, or fails, Geass deletes the logical database anyway. The data can then stay on the server. On a dedicated server of the project, Geass leaves the database on the server.

:::danger
Do not delete a logical database on a shared server if you need its data. The delete destroys the data, and there is no backup.
:::

## Remove a server

1. Open **Settings**, and then **Databases**.
2. Click **Remove** on the row of the server.
3. Enter the resource ID of the server.
4. Click **Remove server**.

The button is unavailable while a project has a database on the server. The dialog shows "Projects still have databases on it, so delete those first". When you remove the server, Geass deletes everything on it.

## Command line and AI agents

The `geass` command line and the MCP server can read servers and logical databases. They have no command to set up a server. Use the dashboard, or `geass api` as shown earlier.

| To | Command | MCP tool |
| --- | --- | --- |
| List servers and dedicated databases | `geass databases list` | `list_databases` |
| Create a logical database | `geass api POST /logical-databases -d '{"project": "proj-1a2b3c4d", "environment": "production", "name": "orders", "server": "shared-postgres", "database": "orders"}'` | None |
| List logical databases | `geass api GET /logical-databases` | None |
| Read how to reach one | `geass api GET /logical-databases/orders/connection` | None |
| Delete one, or a server | `geass api DELETE /logical-databases/orders -q confirmName=orders` | `delete_resource` with `kind` set to `logical-databases` (for a server, `databases`), only with `--allow-delete` |

The connection call needs a workspace token with Full access. It shows the host, the port, the database, and the login. It shows the password and the connection string only with `-q reveal=true`. The MCP server cannot reveal them. A project token cannot use the call. See [Command line and AI agents](/docs/guides/databases/#command-line-and-ai-agents).

## Limits and tradeoffs

- **One server, shared fate.** All projects share the CPU, memory, disk, and connections of the server. A busy project can slow the others. [Project limits](/docs/guides/project-limits/) do not apply to a shared server. The only limit for each project is the connection limit of its login.
- **One disk.** One project can fill the disk of the server. The dashboard cannot make the disk larger after setup.
- **No backups.** Geass does not back up shared servers. To keep copies, make them yourself with `pg_dump` or `mysqldump`.
- **Only PostgreSQL and MySQL.** You cannot share Redis, MariaDB, or SQLite.
- **High availability is set at setup.** The dashboard has the switch only when you create the server.
- **Regions.** When a server and a project both have a region, the regions must match.
- **No move tool.** To move data between a dedicated database and a shared one, dump and load the data yourself.
- **One organization for each server.** A platform administrator sets up the server of an organization. Geass never splits a server or moves data between servers. A platform administrator who is in no organization sees every shared server in the **Add resource** dialog. The API then refuses the servers of other owners.
- **The server's own login.** The server also has a login named `app`, which owns no logical database. Treat the address of the server as reachable from every project.

## Under the hood

This section is for admins who have `kubectl` access to the cluster.

- A shared server is a `GeassDatabase` with no `project`, in `geass-system`. Its pods, disk, and Secrets are also in `geass-system`.
- A logical database is a `GeassLogicalDatabase` in `geass-system`. Geass runs a Job named `<logical ID>-create` to make the database and login, and `<logical ID>-drop` to remove them.
- The connection Secret of a logical database is `<logical ID>-connection`, in the namespace of the environment, `<project ID>-<environment>`.
- To approve the restart of a server into the sandbox, run `kubectl -n geass-system annotate geassdatabase <server> geass.dev/sandbox-restart=true`.

```bash
kubectl get geassdatabases,geasslogicaldatabases -n geass-system
kubectl get geassdatabase shared-postgres -n geass-system \
  -o jsonpath='{.metadata.annotations.geass\.dev/node-placement}{"\n"}'
kubectl logs job/orders-create -n geass-system
kubectl -n geass-system get events --field-selector reason=MixedOrganizations
```

## Troubleshooting

**The row of a server is unavailable in the Add resource dialog.**
The server is not **Ready** yet. Wait for **Ready** on **Settings**, **Databases**.

**The High availability switch is unavailable.**
The cluster is not ready for high availability. Open **Settings**, **Usage**, and run the readiness check. See [Instances and high availability](/docs/guides/databases/#instances-and-high-availability).

**The logical database stays at "Waiting for the database server's administrator login".**
This happens on a dedicated server of a project, the first time that it gets a logical database. Wait a few minutes, and check the status of the server.

**"database server is not ready" or "database server ... is unavailable".**
The server is not running. Fix the server first. Geass continues to try.

**"database creation Job failed".**
Geass could not make the database on the server. An admin can read the cause in the log of the Job. See [Under the hood](#under-the-hood). After you fix the cause, click **Retry** on the logical database.

**"database name must contain only letters, numbers, and underscores".**
Change the `database` value. Do not start it with a digit.

**"This database server runs in the X region, but the project runs in the Y region".**
Select a server in the region of the project, or set up a server there.

**"This database server does not run on nodes tagged hipaa, which the project requires".**
Do these steps:

1. Set up a server with `runOnTags`, as in [Projects that require node tags](#projects-that-require-node-tags).
2. Delete the logical database.
3. Create the logical database on the new server.

**"password authentication failed" for a logical database that worked.**
A second logical database in the same environment used the same name and reset the password. See [Create a database on the server](#create-a-database-on-the-server). Read the current connection string of the newer logical database, or delete the extra one.

**"too many connections".**
The login reached its connection limit, 20 by default. Raise the limit on the server, or make the app use fewer connections.

**Removing the server is blocked.**
Projects still have databases on it. Delete those logical databases first.

**"That server is the platform's and the project is in the organization …".**
Select a server of the organization of the project, or set one up with `org=`. See [Organizations](#organizations).

**"… but a shared database server holds the databases of one organization only. It is not made: …".**
Delete the logical database, and create it on a server of the organization of the project. Or ask a platform administrator to give the server, or the project, to one owner.

**"… It was made before this was checked, so it is kept as it is and still served …".**
The database works. See [An install that already has a mixed server](#an-install-that-already-has-a-mixed-server).

**Giving a project or a server to an organization is refused.**
The project or the server has databases that would then share a server with another owner. The message names them. Move their data or delete them first, as in [An install that already has a mixed server](#an-install-that-already-has-a-mixed-server).

**"This database cannot run in the sandbox: PostgreSQL runs on the CloudNativePG operator …".**
PostgreSQL cannot run in the sandbox. Set up a MySQL server for the organization. See [The sandboxed runtime](#the-sandboxed-runtime).

**"Runs without the sandbox until it is restarted into RuntimeClass …".**
The server waits for a platform administrator to approve its restart. The server works until then. See [Servers that already run](/docs/operations/sandboxed-runtime/#servers-that-already-run).

## Related

- [Databases](/docs/guides/databases/)
- [Backups and restore](/docs/guides/backups-and-restore/)
- [Network isolation](/docs/guides/network-isolation/)
- [Sandboxed runtime for organizations](/docs/operations/sandboxed-runtime/)
- [Scaling and placement](/docs/guides/scaling-and-placement/)
- [Compliance node tags](/docs/guides/compliance-tags/)
- [Command line](/docs/guides/command-line/)
- [Users and tokens](/docs/guides/users-and-tokens/)
