---
title: "Shared database servers"
description: "Run one Postgres or MySQL server for many projects. Give each project its own database and login, and compare the tradeoffs with dedicated databases."
---

A shared database server is one PostgreSQL or MySQL server that every project in the cluster can use. Each project takes a logical database on the server. The project gets a login that can reach that database, and cannot reach the database of another project. Use a shared server when you have many small projects or environments that each need a database. With a shared server, you do not run a separate server for each resource, and you do not pay the CPU and memory for each server.

A cluster admin sets up a server. You can set up more than one server, for example one for each engine or for each region. Project members then add a database on the server from the same **+ Add resource** dialog that they use for all other databases.

## Shared or dedicated

| | Dedicated database | Shared server |
| --- | --- | --- |
| What a project gets | Its own server (a PostgreSQL, MySQL, MariaDB, Redis, or SQLite resource) | A database and login on a server that others use |
| CPU and memory | Reserved for each database, 0.25 CPU and 512 MB by default | One reservation for the whole server |
| Isolation | Its own pods and volume | Shared pods and volume. A login reaches only its own database |
| Backups and restore | Yes, for PostgreSQL, MySQL, MariaDB, and SQLite | None. Shared servers have no backups |
| Engines | PostgreSQL, MySQL, MariaDB, Redis, and SQLite | PostgreSQL and MySQL |
| High availability | Through the API for PostgreSQL, MySQL, and MariaDB | A switch when you set the server up |
| Removal | Delete the database | Blocked while any project still has a database on it |

Use a dedicated database in these cases:

- The data is important enough to need backups and point-in-time restore.
- One project needs its own size or version.
- The load of one project must not affect the other projects.

See [Databases](/docs/guides/databases/).

## Before you start

- To set up or remove a server, you must be a dashboard admin. Project tokens cannot do it.
- The cluster needs space for the server. The default is 0.25 CPU, 512 MB, and 10 GiB of storage.
- For a high-availability server, the cluster needs a minimum of three healthy nodes, and you must have run the readiness check. See [Instances and high availability](/docs/guides/databases/#instances-and-high-availability).
- If your cluster uses regions, a server serves only projects in its own region. A project without its own region uses the default region of the cluster. A server or project with no region at all can pair with any server or project. See [Scaling and placement](/docs/guides/scaling-and-placement/).
- If a project requires compliance node tags, it can use a server only when the server runs on nodes that have those tags. See [Projects that require node tags](#projects-that-require-node-tags).
- If the cluster has [organizations](/docs/guides/organizations-and-workspaces/), a server holds the databases of one organization only. While organizations are on, a project of an organization uses a server of its organization. The servers of the platform are for the projects of no organization. See [Organizations](#organizations).
- The platform can run the code of organizations in a [sandboxed runtime](/docs/operations/sandboxed-runtime/). In that case, the server of an organization also runs in the sandboxed runtime, and thus must be MySQL. Geass does not make a PostgreSQL server for an organization in that case. See [The sandboxed runtime](#the-sandboxed-runtime).

## Set up a shared server

1. Open **Settings**, then **Databases**.
2. Select **Set up a server**. The **Shared database server** sheet opens.
3. Complete the form.
4. Click **Set up server**. The dashboard confirms with "Server created".

| Field | Meaning | Default |
| --- | --- | --- |
| **Engine** | **PostgreSQL** or **MySQL** | PostgreSQL |
| **Name** | Optional resource ID that projects see when they select a server. Lowercase letters, numbers, and hyphens | `cluster-postgres` or `cluster-mysql` |
| **Region** | Where the server runs. Shown only when the cluster has regions. Projects can use only a server in their own region | Default region |
| **CPU**, **Memory** | For each instance. 0.1 to 2 CPU, 128 MB to 4 GB | 0.25 CPU, 512 MB |
| **Storage** | Disk for each instance, 1 to 500 GB. You can increase it later, but never decrease it. The dashboard has no control for the size of a shared server after setup | 10 GB |
| **High availability** | Three instances, so the server continues to work after the loss of a node. Disabled with "Needs at least three healthy nodes." until the cluster is ready for it | Off |

The server starts immediately. There is no draft or **Deploy** step, because the server has no project to deploy from. The table on the page lists each server with these items:

- Its engine.
- Its region.
- Its usage: "Not used yet" or "2 databases · 2 projects".
- Its status: **Starting** or **Ready**.

To set up a server with the REST API, use a workspace token with the admin role:

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

The reply is `{"database":"shared-postgres"}`. The API also accepts these fields:

- `region`.
- `highAvailability=on`.
- `version`.
- `runOnTags`. This field puts the server on nodes that have those tags. The setup sheet has no field for it. See [Projects that require node tags](#projects-that-require-node-tags).
- `org`. This field is the [organization](/docs/guides/organizations-and-workspaces/#shared-things-an-organization-owns) that the server is for. Only the projects of that organization can use the server.

The server is a `GeassDatabase` with no `project`, in the `geass-system` namespace.

From the command line, the versioned API takes the same fields as JSON:

```bash
geass api POST /shared-databases/create -d '{"engine": "Postgres", "name": "shared-postgres", "cpu": "500m", "memory": "1Gi", "storageSize": "20Gi"}'
```

## Create a database on the server

1. Open the project and environment.
2. Select **+ Add resource**, and then select **Database**.
3. Select **Logical database**. The other option, **Database server**, makes a separate server. When there is no server to use, the row is unavailable with "No server yet".
4. Select the server. A shared server shows "shared" next to its engine, for example **cluster-postgres**, PostgreSQL, shared. A server of the project itself in this environment shows "this environment".

Geass creates the logical database and opens its **Settings**.

The row of a server is unavailable until the server is **Ready**. Shared servers in a region that is different from the region of the project do not show.

A logical database starts to work immediately. It has no draft or **Deploy** step.

You can also use a server that belongs to the same project and environment, that is, a dedicated PostgreSQL or MySQL database that you already have. In that case, Geass creates the database with the login of that server, not with a new login. Also, Geass does not drop the database when you delete the logical database.

:::caution
Create only one logical database for each environment in the dialog, or use different names through the API. The dialog always names the database `app`. On a shared server, Geass makes the database and login from the project, the environment, and that name. Thus a second logical database in the same project environment uses the same database and login. When you create the second one, Geass resets the password of the login. The connection secret of the first one then stops working. If you delete either one, Geass drops the shared database.
:::

You can also create a logical database with the REST API. The API is the only way to choose the database name and a connection limit.

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
| `database` | The name that the app uses: letters, numbers, and underscores. It must not start with a digit | `app`, but only when you also leave out `name`. If you send a `name` and no `database`, Geass refuses the request |
| `connectionLimit` | The number of connections that the login of the database can hold, 1 to 1000. Shared servers only | 20 |
| `name` | The resource ID of the logical database. If you leave it out, Geass generates one | Generated |

## How apps connect

Geass writes a Secret named `<logical database ID>-connection` in the namespace of the project environment (`<project ID>-<environment>`):

| Key | Value |
| --- | --- |
| `host` | The in-cluster address of the server, for example `shared-postgres-rw.geass-system.svc` |
| `port` | `5432` for PostgreSQL, `3306` for MySQL |
| `database` | The real name of the database on the server |
| `username` | The login of the database |
| `password` | Its generated password |
| `uri` | A ready connection string |

On a shared server, the database name is `<project>_<environment>_<name>`. Geass changes the name to lowercase, and replaces all other characters with an underscore. These rules also apply:

- If a name is longer than 60 characters, Geass shortens it, and ends it with eight characters of a hash.
- If a name starts with a digit, Geass adds a `d` in front of it.
- The login is `u_` followed by 16 hexadecimal characters that Geass makes from the project, the environment, and the name. Thus the login is short enough for MySQL, and nobody can guess it from the name of the project.

For example:

```text
postgresql://u_3f9a1c2b4d5e6f70:<password>@shared-postgres-rw.geass-system.svc:5432/proj_1a2b3c4d_production_app
```

The password is random, 16 bytes in hexadecimal. Geass creates it one time and keeps it. Geass keeps it beside the server, in the `<logical database ID>-login` Secret in `geass-system`. No project can read that Secret.

:::caution
To connect an app to a logical database, copy the `uri` value from the connection Secret, and add it to the app as a Secret variable. The **Add reference** action of the dashboard connects a database to an app, but it lists only the databases and buckets of the project. It does not list logical databases. To get the value, run this command:

```bash
kubectl get secret orders-connection -n proj-1a2b3c4d-production \
  -o jsonpath='{.data.uri}' | base64 -d
```

Then do these steps:

1. Open the **Variables** tab of the app.
2. Click **New Variable**.
3. Name the variable `DATABASE_URL`, and paste the value.
4. Turn on **Secret**.
5. Click **Add**.
6. Deploy the app.

See [Environment variables](/docs/guides/environment-variables/).
:::

The dashboard shows the server and the database name of a logical database under **Settings**, **Logical database**. Both are read-only. The **Console** tab of a logical database does not run queries against it, because the console looks for a database resource of that name.

## Projects that require node tags

A project can require compliance node tags, such as `hipaa`, so that everything it runs stays on nodes that you marked. See [Compliance node tags](/docs/guides/compliance-tags/). A shared server holds data for more than one project. Thus the tags of one project do not set where the server runs. Geass places the server one time, with its own **Run on** tags. A project that requires tags can use the server only when the server runs on nodes with all of those tags.

To make a server that a project with tags can use, send `runOnTags` when you create the server. Also send the region that the nodes are in. Geass checks that a node in that region has each tag.

```bash
curl -X POST https://geass.example.com/api/shared-databases/create \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json" \
  --data-urlencode engine=Postgres \
  --data-urlencode name=hipaa-postgres \
  --data-urlencode region=eu \
  --data-urlencode runOnTags=hipaa
```

Geass records the placement when it first provisions the server. The placement does not change. Read it with `kubectl get geassdatabase hipaa-postgres -n geass-system -o jsonpath='{.metadata.annotations.geass\.dev/node-placement}'`. A server that was installed before placement existed runs anywhere. Thus Geass refuses every project that requires tags on that server.

Geass refuses a logical database for a project that requires tags on a server that does not run on all of those tags. Its Ready condition shows "This database server does not run on nodes tagged hipaa, which the project requires". The dashboard does not warn you before. The **Add resource** dialog lists every server that is **Ready** and in the region of the project. Thus the refusal comes after you create the logical database.

The refusal counts as a failure. Geass tries every 30 seconds. After five failed tries in sequence, or after three minutes, the status becomes "Provisioning failed after repeated errors", and Geass stops. Read the cause on the **Deployments** tab of the logical database. To fix the problem, do one of these:

- Delete the logical database, and create it on a server that has the tags.
- Change the tags of the project, and click **Retry**.

When you change the tags of the project, Geass checks again a logical database that still waits. Geass does not check again a logical database that has already failed.

The server has no project, and this causes two limits:

- A server that runs on compliance nodes is not reserved for the projects that require the tag. Geass checks only that the required tags of the project are on the server. Thus a project that does not require those tags can also use the server.
- The volume of the server does not use a tagged storage class. With Longhorn, the volume uses the replicated class of the cluster. After any node is a compliance node, that class keeps its copies off compliance nodes. Thus the pods of the server run on the tagged nodes, but the copies of its data might not. An HA server uses `geass-single`, which keeps its one copy on the node where the instance runs. See [Storage and volumes](/docs/guides/storage/#compliance-tags-and-storage-classes).

A **Logical database** on a dedicated server of the project itself needs none of this, because Geass places that server with the tags of the project.

## Organizations

A shared server holds the data of every project that has a database on it. Thus with [organizations](/docs/guides/organizations-and-workspaces/), a server holds the databases of one organization only. The data of one organization is never on a server with the data of another organization, or with the data of the projects of the platform.

| Server | Who can take databases on it |
| --- | --- |
| A server of an organization (`spec.org` set) | The projects of that organization only, when organizations are on and when they are off |
| A server of the platform (no `spec.org`) | While organizations are on, only the projects of no organization. While organizations are off, every project, as before |

The dedicated server of a project belongs to that project, wherever the project goes. None of these rules apply to it.

### Where it is checked

- **When you create a database.** Geass refuses `POST /api/logical-databases/create` for a project of an organization on the server of the platform. The response is `400` and "That server is the platform's and the project is in the organization org-1a2b3c4d, but with organizations on a shared database server holds the databases of one organization only. Choose a server of org-1a2b3c4d, or set one up for it with POST /api/shared-databases/create and org=org-1a2b3c4d".
  - Only a platform administrator sees this message. The people of an organization never see the server of the platform. Thus they get "That database server does not exist".
  - Geass does not make a server for an organization automatically. A server costs CPU, memory, and disk, and the platform decides to set one up.
- **When you move a project.** Geass refuses `POST /api/projects/{project}/assign` with `409` in these cases:
  - The project has databases on a server of another organization.
  - While organizations are on, the project has databases on the server of the platform, and you give the project to an organization.

  The message is "the project has databases on the platform's database server cluster-postgres, and with organizations on a shared database server holds the databases of one organization only: move their data to a server of the organization, or delete them, first". Geass never refuses these moves:
  - A move of a project to another workspace of its organization.
  - A move of a project out of its organization, when that move ends a mix.
- **When you give a server to an organization.** `POST /api/shared-databases/{name}/org` with `org=<organization>` changes the owner of a shared server. Send `org=` empty to give the server to the platform. Geass refuses the request with `409` while the server holds a database whose project is not in that organization. For the platform while organizations are on, Geass refuses when the server holds a database of any organization. The message names a maximum of five of these databases, for example "orders (project shop, the organization org-1a2b3c4d)". Only a platform administrator can send this request.
- **In the operator.** The operator checks the objects however they were written: with `kubectl`, with a script, or before this check existed. While organizations are on, the operator does not make a logical database that breaks the rule. No Job runs on the server, and the operator creates no login. `Ready` is `False` with a message that names both owners, for example "The database server cluster-postgres belongs to the platform and the project shop to the organization org-1a2b3c4d, but a shared database server holds the databases of one organization only. It is not made: …".
  - This state does not count as a failure, so Geass does not stop. When the platform gives the server or the project to one organization, Geass makes the database within seconds.
  - The logical database also gets the condition `OrganizationIsolated`, `False`, with the reason `MixedOrganizations`. It also gets one `Warning` event with the same reason.

### Databases that were there before

A logical database can already be on its server when the rule finds it. For example, somebody made it before organizations were on, before this check existed, or with `kubectl`. That database is **kept as it is and still served**. Geass deletes, drops, and moves nothing. `Ready` stays as it was, and the services of the project continue to connect. Geass reports the database in these places:

- The condition `OrganizationIsolated` (`False`, `MixedOrganizations`). Its message names both owners and ends with "It was made before this was checked, so it is kept as it is and still served, and listed for the platform's administrators to move".
- A `Warning` event `MixedOrganizations` on the logical database, when Geass first finds it.
- The list in the next section.

The condition goes when the server and the project belong to one owner again, or when you turn organizations off. "Already on its server" means that the logical database has a connection Secret (`status.connectionSecret`). Geass refuses a logical database that does not have a connection Secret, as described earlier.

The people of the organization of the project can see the message of the condition, like the rest of the logical database. Thus the message names the other organization by its resource ID (`org-…`), never by its display name.

### List the databases that mix

```bash
curl https://geass.example.com/api/shared-databases/mixed \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json"
```

You can also use `geass api GET /shared-databases/mixed`. Only a platform administrator can send this request. The reply:

```json
{
  "enforced": true,
  "items": [
    {"name": "orders", "project": "shop", "environment": "production", "projectOrg": "org-1a2b3c4d",
     "server": "cluster-postgres", "serverOrg": "", "made": true,
     "message": "The database server cluster-postgres belongs to the platform and the project shop to the organization org-1a2b3c4d, but a shared database server holds the databases of one organization only"}
  ],
  "servers": [
    {"name": "cluster-postgres", "org": "", "owners": [{"org": "", "databases": 3}, {"org": "org-1a2b3c4d", "databases": 1}]}
  ]
}
```

The reply has these fields:

- `items` are the logical databases whose server does not belong to the organization of their project, with the rule that applies while organizations are on.
- `made` tells whether the database is on the server already (kept and served) or not (refused).
- `servers` are the servers that hold one of these databases, with the owners of all of their databases. `""` is the projects of no organization.
- `enforced` tells whether organizations are on.

The list applies the rule even while organizations are off. Thus it shows what Geass reports when you turn organizations on.

To get the same information from the cluster, run these commands:

```bash
kubectl -n geass-system get events --field-selector reason=MixedOrganizations
kubectl -n geass-system get geasslogicaldatabases -o json \
  | jq -r '.items[] | select(any(.status.conditions[]?; .type=="OrganizationIsolated")) | "\(.metadata.name) \(.spec.serverRef)"'
```

### An install that already has a mixed server

This case is a cluster that used the server of the platform for every project, and then gave projects to organizations. If you can, do these steps before you turn organizations on. While organizations are off, Geass assigns projects as it always did.

1. List the databases that mix with `GET /api/shared-databases/mixed`.
2. For each server in `servers`, do one of these:
   - **All its databases are one organization's** (`owners` has one entry, an organization's): give the server to that organization with `POST /api/shared-databases/<server>/org` and `org=<organization>`. Nothing moves. The people of the organization now see the server. Its administrators can delete the server when no database is on it.
   - **It holds the databases of more than one owner:** choose the owner that keeps the server. For each database of another owner, do these steps:
     1. Set up a server of the organization of that owner (`POST /api/shared-databases/create` with `org=`).
     2. Create a logical database on that server.
     3. Copy the data with `pg_dump` and `psql`, or with `mysqldump` and `mysql`.
     4. Point the services to the new connection Secret.
     5. Delete the old logical database. This step drops its data from the old server.

     Geass has no tool for the copy. Nobody has run this sequence from start to end on a real cluster.
3. If organizations are not on, turn them on. Geass serves and reports the databases that are left. They do not break.

While organizations are on, the order is important. You cannot give a project of no organization that has databases on the server of the platform to an organization. Also, you cannot give the server to the organization while the project is in no organization. Do one of these:

- Move the data of the project first.
- As a platform administrator who accepts the short time between the two steps, set `spec.org` of the project with `kubectl`, and then give the server. Until you give the server, the operator continues to serve the databases of the project, and reports them.

### What has been run on a real cluster

The test cluster ran K3s v1.36.4+k3s1 with organizations on. It had three single MySQL servers: one of the platform, and one for each of two organizations. Each case on this page gave the documented result:

- The `400` for a project of an organization on the server of the platform, and on the server of another organization.
- The `409`s for the move of a project, and for a server given to an organization or to the platform. The messages named the databases.
- The refusal by the operator of a logical database written with `kubectl`: `OrganizationIsolated` `False`, one `Warning` event, no Job, and nothing on the server. After the platform gave the server to the organization, Geass made the database.
- A database made while organizations were off. With organizations on again, Geass kept that database. The database still reached its data through its login. The condition and `GET /api/shared-databases/mixed` reported it (`made: true`). Geass refused a second database for that project on that server.

**Not run:** shared PostgreSQL or MariaDB servers, high availability, regions, and node tags. The checks do not depend on the engine, but only MySQL ran.

## The sandboxed runtime

When `spec.tenancy.sandbox` is set on the platform, the server of an organization runs under the RuntimeClass of the platform, such as gVisor. The services of the organization also run there. The reason is that a login with enough rights can run programs through its server. See [Sandboxed runtime for organizations](/docs/operations/sandboxed-runtime/#database-servers). The servers of the platform do not run in the sandbox. While organizations are on, those servers hold only the databases of projects of no organization.

| Server | With the sandbox on |
| --- | --- |
| A MySQL server of an organization, one server or highly available | Made in the sandbox. A server that already runs moves into the sandbox when a platform administrator approves its restart (`kubectl -n geass-system annotate geassdatabase <server> geass.dev/sandbox-restart=true`), or immediately with `databaseRestarts: Immediate`. Until then, its `Sandboxed` condition shows `RestartPending` |
| A PostgreSQL server of an organization | Not made, because CloudNativePG, which runs it, cannot give its pods a RuntimeClass. `POST /api/shared-databases/create` with `org` refuses it with `400`. Geass does not give a PostgreSQL server to an organization (`409`). Geass holds a server that was made before. That server continues to run and serve, outside the sandbox, but Geass changes nothing on it. Its `Sandboxed` condition shows this (`EngineCannotBeSandboxed`) |
| A server of the platform, of either engine | Not sandboxed, and not affected |

The Jobs that make and drop the logical databases of an organization also run under the RuntimeClass, from their next run, on any server. While the RuntimeClass is missing, Geass makes no Job. A new logical database waits, not ready, with `Sandboxed` `False` `RuntimeClassMissing`. Geass releases a logical database that is being deleted after three minutes, without a drop from its server.

When a server moves into the sandbox, it restarts:

- MOCO rolls a highly available MySQL server one server at a time, and switches the primary to a replica. This case was not run. Only single servers were run.
- A single server is unavailable while its pod starts again. This took about 40 seconds for a MySQL 8.4.11 server that held data, on K3s v1.36.4+k3s1 with gVisor `release-20261019.0`.

On that cluster, a shared MySQL server of an organization ran under gVisor. The Job that makes a logical database on that server ran with the RuntimeClass and succeeded.

**Give it at least 1 GiB.** Two new servers with the Geass default of 512 MiB were killed for lack of memory during their first start. They had no `app` login and no password for `root@localhost`, but they showed `Ready`. A logical database on them never finished. [What it costs](/docs/operations/sandboxed-runtime/#what-it-costs) has the symptom.

As the table says, Geass refused a new PostgreSQL server for an organization with `400`, and held a server that was made before. Geass made nothing of the refused server. Under gVisor, a database runs slower than on the kernel of the node. Also, some of what MySQL can ask of the kernel is only partly available. See [What it costs](/docs/operations/sandboxed-runtime/#what-it-costs), and try a copy first.

## What a project can and cannot do

- A login reaches only its own logical database. Geass revokes `PUBLIC` access to each logical database, and grants access to that login only. Geass changes nothing on the other databases of the server, such as the `postgres` maintenance database. Those databases keep the default access of the server.
- Network isolation between projects does not apply to the server. The server runs in `geass-system`, and the isolation policies cover project namespaces. Any pod in the cluster can open a connection to the address of the server. Thus the logins keep projects apart. See [Network isolation](/docs/guides/network-isolation/).
- A project cannot change the number of connections that its login can hold. Geass applies `connectionLimit` when it first creates the database, and does not apply a later change. To change the limit later, alter the login on the server:
  - On PostgreSQL, use `ALTER ROLE ... CONNECTION LIMIT`.
  - On MySQL, use `ALTER USER ... WITH MAX_USER_CONNECTIONS`.

## Delete a logical database

1. Open the **Settings** of the logical database.
2. Click **Delete logical database**.
3. Type the resource ID of the logical database.
4. Confirm.

On a shared server, Geass runs a drop job that removes the database and its login. Then Geass deletes the connection Secret. On PostgreSQL, the drop closes open connections. The drop has a limit of three minutes. A server that is gone or not ready never stops the deletion of the logical database. Thus after three minutes, Geass releases the logical database, even if the drop did not finish. If the drop job fails, Geass also releases the logical database, and the database can stay on the server. On a dedicated server of the project, Geass leaves the database on the server, as it always did.

:::danger
Do not drop a shared database if you need its data. The drop destroys the data, and there is no backup to restore from.
:::

## Remove a server

1. Open **Settings**, then **Databases**.
2. Click **Remove** on the row of the server.
3. Type the resource ID of the server.
4. Click **Remove server**.

The button stays disabled while any project has a database on the server. The dialog shows "Projects still have databases on it, so delete those first". The API refuses with a count, for example "2 databases in 2 projects still use this server. Delete them first, because removing the server would destroy their data". When you remove the server, Geass deletes everything on it.

## Command line and AI agents

The `geass` command line and the MCP server can read servers and logical databases. They have no command or tool to set up a server. Use the dashboard, or `geass api` as shown earlier.

| To | Command | MCP tool |
| --- | --- | --- |
| List servers (they have no project) and dedicated databases | `geass databases list` | `list_databases` |
| Create a logical database | `geass api POST /logical-databases -d '{"project": "proj-1a2b3c4d", "environment": "production", "name": "orders", "server": "shared-postgres", "database": "orders"}'` | None |
| List logical databases | `geass api GET /logical-databases` | None |
| Read how to reach one | `geass api GET /logical-databases/orders/connection` | None |
| Delete one, or a server | `geass api DELETE /logical-databases/orders -q confirmName=orders` | `delete_resource` with `kind` set to `logical-databases` (for a server, `databases`), only with `--allow-delete` |

`geass api` sends each path under `/api/v1`. So the create command calls `POST /api/v1/logical-databases`. This v1 route does the same as `POST /api/logical-databases/create`, the dashboard route in the REST example earlier. Both routes work.

The connection call needs a workspace token with Full access. It shows the host, the port, the database, and the login. The password and the connection string show only with `-q reveal=true`, and the operator logs each reveal. The MCP server cannot ask for them. Geass refuses the call for a project token. See [Command line and AI agents](/docs/guides/databases/#command-line-and-ai-agents).

## Limits and tradeoffs

- **One server, shared fate.** All projects share the CPU, memory, disk, and connection slots of the server. A busy project can slow the other projects. Geass applies no CPU or memory limit for each project on the server. The reason is that the server runs in `geass-system`, and [project limits](/docs/guides/project-limits/) apply only to the environments of a project. The only limit for each project is the connection limit on the login.
- **One volume.** The disk of the server is shared. One project can fill it. A size increase is a change to the server, not to a project, and the dashboard has no control for it. See [Storage and volumes](/docs/guides/storage/).
- **No backups.** Geass does not back up shared servers and logical databases, and cannot restore them to a point in time. If you need dumps, make them yourself with `pg_dump` or `mysqldump`.
- **Only PostgreSQL and MySQL.** You cannot share Redis and SQLite.
- **Version.** A shared PostgreSQL server runs version 16, unless you set `version` through the API.
- **High availability is chosen at setup.** The dashboard has a switch only when you create the server.
- **Regions.** When a server and a project both have a region, the two regions must match. Thus no query crosses regions. A project without a region uses the default region of the cluster. Geass does not check a side that has no region at all.
- **No migration tool.** To move the data of a project between a dedicated database and a shared database, dump and load the data yourself.
- **One organization per server, and no server made for you.** While organizations are on, a project of an organization needs a server of its organization. A platform administrator sets up that server. Geass never splits a server, and never moves data between servers. Some platform administrators are not in an organization. For these administrators, the **Add resource** dialog lists every shared server in the region of the project, including the servers of other owners. The API then refuses those servers.
- **The server's own login.** Like every database that Geass creates, the server has an application login named `app`. Geass makes its password from the resource ID of the server. That login owns no logical database. Each logical database is closed to every login except its own login. Treat the address of the server as reachable from every project.

## What happens under the hood

- A shared server is a `GeassDatabase` with an empty `project`, in `geass-system`. Its pods, its volume, and its connection Secret `<server ID>-connection` are all in `geass-system`.
- To create a database and a login, Geass needs an administrative login on the server. For PostgreSQL, this login is the `<server ID>-superuser` Secret. For MySQL, it is the root password in `<server ID>-bootstrap`.
  - A shared server has this login from the start.
  - A dedicated server of a project gets this login when Geass creates its first logical database. Until the login exists, the logical database reports "Waiting for the database server's administrator login".
  - After Geass turns the login on, the login stays. If Geass removed it, databases that Geass still creates or drops would be stranded.
- Each logical database is a `GeassLogicalDatabase` in `geass-system`. The controller runs a Job named `<logical ID>-create` in the namespace of the server, in the image `postgres:16-alpine` or `mysql:8.4`. The Job connects with the administrative login and creates the login and the database. Then it revokes public access and grants access to the login. The Job is safe to run again. It sets the password and limit of an existing login again, and keeps an existing database.
- On delete, a Job named `<logical ID>-drop` runs `DROP DATABASE ... WITH (FORCE)` and drops the login. A finalizer, `geass.geass.dev/logicaldatabase`, holds the object until the Job finishes or three minutes pass.
- Jobs run in the region of the server. They do not use the **Run on** tags of the server. Thus on a shared server, Jobs run on nodes that are not reserved, and not on compliance nodes. On a dedicated server of a project, Jobs also have the required tags of the project.
- When the [sandboxed runtime](/docs/operations/sandboxed-runtime/) of the platform is on, Geass makes some Jobs with the RuntimeClass (`runtimeClassName` in its pod). These are the Jobs of the server of an organization, and the Jobs of the project of an organization on any server. A Job that already exists continues to run as before.

```bash
kubectl get geassdatabases -n geass-system
kubectl get geasslogicaldatabases -n geass-system
kubectl get geassdatabase shared-postgres -n geass-system \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}{"\n"}'
kubectl get jobs -n geass-system
kubectl logs job/orders-create -n geass-system
```

## Troubleshooting

**The server's row is greyed out in the Add resource dialog.**
The server is not **Ready** yet, or it runs in a region that is different from the region of the project. Wait for **Ready** on **Settings**, **Databases**, or check the region of each.

**The High availability switch is disabled.**
The cluster is not ready for high availability. Open **Settings**, **Usage**, and run the readiness check. See [Instances and high availability](/docs/guides/databases/#instances-and-high-availability).

**The logical database stays at "Waiting for the database server's administrator login".**
The server has not exposed its administrative login yet. This occurs on a dedicated server of a project. Geass turns on the login for that server when it sees the first logical database. Wait a few minutes, and check the status of the server.

**"database server is not ready" or "database server ... is unavailable".**
The server is not running. Fix the server first. Neither message counts as a failure, and Geass continues to try again.

**"database creation Job failed" and the logical database shows a failed state.**
Read the output of the Job with `kubectl logs job/<logical ID>-create -n geass-system`. After you fix the cause, click **Retry** on the logical database.

**"database name must contain only letters, numbers, and underscores".**
The `database` value breaks the rule. Use letters, digits, and underscores, and do not start with a digit.

**"This database server runs in the X region, but the project runs in the Y region".**
Select a server in the region of the project, or set up a server there.

**"This database server does not run on nodes tagged hipaa, which the project requires".**
The project requires node tags that the server is not placed on. Do these steps:

1. Set up a server with `runOnTags`, as in [Projects that require node tags](#projects-that-require-node-tags).
2. Delete the logical database.
3. Create the logical database on the new server.

If the status has already become "Provisioning failed after repeated errors", the message is on the **Deployments** tab.

**"password authentication failed" for a logical database that worked.**
A later logical database in the same project environment used the same name. This reset the password of the login. See the caution under [Create a database on the server](#create-a-database-on-the-server). Copy the current password from the connection Secret of the newer database, or delete the extra logical database.

**"too many connections" from the database.**
The login reached its connection limit, 20 by default. Raise the limit on the server with `ALTER ROLE` or `ALTER USER`. Also make the app use fewer connections, or a pool.

**Removing the server is blocked.**
Projects still have databases on it. The message gives the count. Delete those logical databases first.

**"That server is the platform's and the project is in the organization …".**
Organizations are on, and the server of the platform is for the projects of no organization. Select a server of the organization of the project, or set up a server for that organization with `org=`. See [Organizations](#organizations).

**"… but a shared database server holds the databases of one organization only. It is not made: …".**
Somebody wrote the logical database without the dashboard, on a server of another owner. Geass made nothing on the server. Do one of these:

- Delete the logical database, and create it on a server of the organization of the project.
- Ask a platform administrator to give the server, or the project, to one organization. After the administrator does this, Geass makes the database.

**"… It was made before this was checked, so it is kept as it is and still served …".**
The database was on the server before the rule found it. The database works, and `GET /api/shared-databases/mixed` lists it. See [An install that already has a mixed server](#an-install-that-already-has-a-mixed-server).

**Giving a project to an organization, or a server, is refused with 409.**
The project, or the server, has databases that would then share a server with the databases of another owner. The message names them. First move their data or delete them, in the order under [An install that already has a mixed server](#an-install-that-already-has-a-mixed-server).

**"This database cannot run in the sandbox: PostgreSQL runs on the CloudNativePG operator …".**
The platform runs the code of organizations in a sandboxed runtime, and PostgreSQL cannot run in it. Set up a MySQL server for the organization instead. Geass holds a server that was made before as it is. See [The sandboxed runtime](#the-sandboxed-runtime).

**"Runs without the sandbox until it is restarted into RuntimeClass …".**
The server ran before the sandbox was set. It waits for a platform administrator to approve its restart. The server works until the restart. See [Servers that already run](/docs/operations/sandboxed-runtime/#servers-that-already-run).

## Related

- [Databases](/docs/guides/databases/)
- [Backups and restore](/docs/guides/backups-and-restore/)
- [Network isolation](/docs/guides/network-isolation/)
- [Sandboxed runtime for organizations](/docs/operations/sandboxed-runtime/)
- [Scaling and placement](/docs/guides/scaling-and-placement/)
- [Compliance node tags](/docs/guides/compliance-tags/)
- [Command line](/docs/guides/command-line/)
- [Users and tokens](/docs/guides/users-and-tokens/)
