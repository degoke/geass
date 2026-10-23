---
title: "Shared database servers"
description: "Run one Postgres or MySQL server for many projects, give each project a database and login of its own, and weigh the tradeoffs against dedicated databases."
---

A shared database server is one PostgreSQL or MySQL server that every project in the cluster can use. Each project takes a logical database on it, with a login that can reach that database and no other project's database. Use it when you have many small projects or environments that each need a database and you do not want to run, and pay the CPU and memory for, a separate server per resource.

A cluster admin sets up a server. You can set up more than one, for example one per engine or per region. Project members then add a database on it from the same **+ Add resource** dialog they use for any other database.

## Shared or dedicated

| | Dedicated database | Shared server |
| --- | --- | --- |
| What a project gets | Its own server (a PostgreSQL, MySQL, Redis or SQLite resource) | A database and login on a server others use |
| CPU and memory | Reserved per database, 0.25 CPU and 512 MB by default | One reservation for the whole server |
| Isolation | Its own pods and volume | Shared pods and volume. A login reaches only its own database |
| Backups and restore | Yes, for PostgreSQL, MySQL and SQLite | None. Shared servers have no backups |
| Engines | PostgreSQL, MySQL, Redis, SQLite | PostgreSQL and MySQL |
| High availability | Through the API for PostgreSQL and MySQL | A switch when you set the server up |
| Removal | Delete the database | Blocked while any project still has a database on it |

Choose a dedicated database when the data matters enough to need backups and point-in-time restore, when one project needs its own size or version, or when one project's load must not affect the others. See [Databases](/docs/guides/databases/).

## Before you start

- You must be a dashboard admin to set up or remove a server. Project tokens cannot do it.
- The cluster needs room for the server. The default is 0.25 CPU, 512 MB and 10 GiB of storage.
- For a high-availability server the cluster needs at least three healthy nodes, and you must have run the readiness check. See [Instances and high availability](/docs/guides/databases/#instances-and-high-availability).
- If your cluster uses regions, a server serves only projects in its own region. A project without a region of its own uses the cluster's default region. A server or project with no region at all can be paired with any. See [Scaling and placement](/docs/guides/scaling-and-placement/).
- If a project requires compliance node tags, it can use a server only when the server runs on nodes that carry those tags. See [Projects that require node tags](#projects-that-require-node-tags).
- If the cluster has [organizations](/docs/guides/organizations-and-workspaces/), a server holds the databases of one organization only. While organizations are on, a project of an organization uses a server of its organization, and the platform's own servers are for the projects of no organization. See [Organizations](#organizations).

## Set up a shared server

1. Open **Settings**, then **Databases**.
2. Select **Set up a server**. The **Shared database server** sheet opens.
3. Fill in the form.
4. Select **Set up server**. The dashboard confirms with "Shared database server is starting".

| Field | Meaning | Default |
| --- | --- | --- |
| **Engine** | **PostgreSQL** or **MySQL** | PostgreSQL |
| **Name** | Optional resource ID that projects see when they choose a server. Lowercase letters, numbers and hyphens | `cluster-postgres` or `cluster-mysql` |
| **Region** | Where the server runs. Shown only when the cluster has regions. Projects can only use a server in their own region | Default region |
| **CPU**, **Memory** | Per instance. 0.1 to 2 CPU, 128 MB to 4 GB | 0.25 CPU, 512 MB |
| **Storage** | Disk per instance, 1 to 500 GB. You can increase it later, never decrease it, although the dashboard has no control for a shared server's size after setup | 10 GB |
| **High availability** | Three instances, so the server survives losing a node. Disabled with "Needs at least three healthy nodes." until the cluster is ready for it | Off |

The server starts at once. There is no draft or **Deploy** step, because it has no project to deploy from. The table on the page lists each server with its engine, region, usage ("Not used yet" or "2 databases · 2 projects") and status, **Starting** or **Ready**.

Set up a server with the REST API using a workspace token with the admin role:

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

The reply is `{"database":"shared-postgres"}`. The API also accepts `region`, `highAvailability=on`, `version`, `runOnTags` and `org` (an [organization](/docs/guides/organizations-and-workspaces/#shared-things-an-organization-owns) the server is for: only its projects may use it). The server is a `GeassDatabase` with no `project`, in the `geass-system` namespace. The setup sheet has no field for `runOnTags`, which places the server on nodes that carry those tags. See [Projects that require node tags](#projects-that-require-node-tags).

From the command line, the versioned API takes the same fields as JSON:

```bash
geass api POST /shared-databases/create -d '{"engine": "Postgres", "name": "shared-postgres", "cpu": "500m", "memory": "1Gi", "storageSize": "20Gi"}'
```

## Create a database on the server

1. Open the project and environment. Select **+ Add resource**, then **Database**.
2. Choose the row named after the server, for example **cluster-postgres (shared)**. Its description reads "A database of its own on the shared Postgres server" (or MySQL).
3. Geass creates the logical database and opens its **Settings**.

A server's row is greyed out until the server is **Ready**. Servers in a different region from the project do not appear.

A logical database starts working at once. It has no draft or **Deploy** step, although the dashboard message says "saved as a draft".

The dialog also has a **Logical database** row with the description "Create inside an existing server". It creates a database on a server that belongs to the same project and environment, a dedicated PostgreSQL or MySQL database you already have. It uses the first such server in the environment, and the row is disabled when the environment has none. The database is created with that server's own login instead of a new one, and Geass does not drop it when you delete the logical database.

:::caution
The dialog always names the database `app`. On a shared server the database and login are derived from the project, the environment and that name. A second logical database in the same project environment therefore maps to the same database and login. Creating it resets the login's password, so the first one's connection secret stops working, and deleting either one drops the shared database. Create one logical database per environment, or choose distinct names through the API.
:::

Create one with the REST API. This is the only way to choose the database name and a connection limit.

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
| `database` | The name in the app's own terms: letters, numbers and underscores, not starting with a digit | `app`, but only when you also leave out `name`. With a `name` and no `database` the request is refused |
| `connectionLimit` | How many connections the database's login may hold, 1 to 1000. Shared servers only | 20 |
| `name` | The logical database's own resource ID. Leave it out to get a generated one | Generated |

## How apps connect

Geass writes a Secret named `<logical database ID>-connection` in the project environment's namespace (`<project ID>-<environment>`):

| Key | Value |
| --- | --- |
| `host` | The server's in-cluster address, for example `shared-postgres-rw.geass-system.svc` |
| `port` | `5432` for PostgreSQL, `3306` for MySQL |
| `database` | The database's real name on the server |
| `username` | The database's own login |
| `password` | Its generated password |
| `uri` | A ready connection string |

On a shared server the database is called `<project>_<environment>_<name>`, lowercased with any other character replaced by an underscore. A name that would pass 60 characters is shortened and ends with eight characters of a hash, and a name that starts with a digit gets a `d` in front. The login is `u_` followed by 16 hexadecimal characters derived from the project, environment and name, so it is short enough for MySQL and cannot be guessed from the project's name. For example:

```text
postgresql://u_3f9a1c2b4d5e6f70:<password>@shared-postgres-rw.geass-system.svc:5432/proj_1a2b3c4d_production_app
```

The password is random, 16 bytes in hexadecimal, and is created once and kept. It is stored beside the server in the `<logical database ID>-login` Secret in `geass-system`, where no project can read it.

:::caution
The dashboard's **Add reference** action, which wires a database into an app, lists only the project's own databases and buckets. It does not list logical databases. Copy the `uri` value out of the connection Secret and add it to the app as a Secret variable:

```bash
kubectl get secret orders-connection -n proj-1a2b3c4d-production \
  -o jsonpath='{.data.uri}' | base64 -d
```

Open the app's **Variables** tab, choose **New Variable**, name it `DATABASE_URL`, paste the value, switch **Secret** on, select **Add**, and deploy the app. See [Environment variables](/docs/guides/environment-variables/).
:::

The dashboard shows a logical database's server and database name under **Settings**, **Logical database**. Both are read-only. Its **Console** tab does not run queries against it, because the console looks for a database resource of that name.

## Projects that require node tags

A project can require compliance node tags, such as `hipaa`, so that everything it runs stays on nodes you marked. See [Compliance node tags](/docs/guides/compliance-tags/). A shared server holds data for several projects, so it is not placed by one project's tags. It is placed once, with its own **Run on** tags, and a project that requires tags can use it only when the server runs on nodes with every one of them.

To make a server that qualifies, send `runOnTags` when you create it, with the region the nodes are in. Geass checks that a node in that region carries each tag.

```bash
curl -X POST https://geass.example.com/api/shared-databases/create \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json" \
  --data-urlencode engine=Postgres \
  --data-urlencode name=hipaa-postgres \
  --data-urlencode region=eu \
  --data-urlencode runOnTags=hipaa
```

The placement is recorded when the server is first provisioned and does not change. Read it with `kubectl get geassdatabase hipaa-postgres -n geass-system -o jsonpath='{.metadata.annotations.geass\.dev/node-placement}'`. A server installed before placement existed runs anywhere, so every project that requires tags is refused on it.

A logical database for a project that requires tags is refused on a server that does not run on all of them. Its Ready condition reads "This database server does not run on nodes tagged hipaa, which the project requires". The dashboard does not warn you first: the **Add resource** dialog lists every server that is **Ready** and in the project's region, so the refusal comes after you create the logical database.

The refusal counts as a failure. After five failed passes in a row (Geass tries every 30 seconds), or three minutes, the status becomes "Provisioning failed after repeated errors" and Geass stops retrying. Read the cause on the logical database's **Deployments** tab. Delete the logical database and create it on a server that qualifies, or change the project's tags and select **Retry**. Changing the project's tags makes Geass check a logical database that is still waiting again, but not one that has already failed.

Two limits follow from the server having no project:

- A server that runs on compliance nodes is not reserved for the projects that require the tag. Geass checks only that the project's required tags are on the server, so a project that does not require them can use it too.
- The server's volume does not use a tagged storage class. With Longhorn it uses the cluster's replicated class, and once any node is a compliance node that class keeps its copies off compliance nodes. The server's pods run on the tagged nodes, and the copies of its data may not. An HA server uses `geass-single`, which keeps its one copy on the node where the instance runs. See [Storage and volumes](/docs/guides/storage/#compliance-tags-and-storage-classes).

A **Logical database** on one of the project's own dedicated servers needs none of this, because that server is placed with the project's tags.

## Organizations

A shared server holds the data of every project that has a database on it, so with [organizations](/docs/guides/organizations-and-workspaces/) a server holds the databases of one organization only. One organization's data never sits on a server beside another organization's, or beside the platform's own projects'.

| Server | Who may take databases on it |
| --- | --- |
| An organization's (`spec.org` set) | That organization's projects only, whether organizations are on or off |
| The platform's (no `spec.org`) | While organizations are on, the projects of no organization only. While they are off, every project, as before |

A project's own dedicated server is its project's, wherever the project goes, and none of this applies to it.

### Where it is checked

- **Creating a database.** `POST /api/logical-databases/create` is refused for a project of an organization on the platform's server, with `400` and "That server is the platform's and the project is in the organization org-1a2b3c4d, but with organizations on a shared database server holds the databases of one organization only. Choose a server of org-1a2b3c4d, or set one up for it with POST /api/shared-databases/create and org=org-1a2b3c4d". Only a platform administrator sees this: an organization's people never see the platform's server, so they are told "That database server does not exist". Geass does not make a server for an organization by itself. A server costs CPU, memory and disk, and setting one up is the platform's decision.
- **Moving a project.** `POST /api/projects/{project}/assign` is refused with `409` when the project has databases on a server of another organization, or, while organizations are on, on the platform's server when it is given to an organization: "the project has databases on the platform's database server cluster-postgres, and with organizations on a shared database server holds the databases of one organization only: move their data to a server of the organization, or delete them, first". Moving a project to another workspace of the organization it is in is never refused, and neither is taking a project out of its organization when that ends a mixing.
- **Giving a server to an organization.** `POST /api/shared-databases/{name}/org` with `org=<organization>`, or `org=` empty for the platform, changes whose a shared server is. It is refused with `409` while the server holds a database whose project is not that organization's (for the platform while organizations are on, one that is any organization's), and the message names up to five of them, for example "orders (project shop, the organization org-1a2b3c4d)". A platform administrator only.
- **In the operator.** However the objects were written (with `kubectl`, a script, or before this check existed), the operator does not make a logical database that breaks the rule while organizations are on. No Job runs on the server and no login is created. `Ready` is `False` with a message that names both owners, for example "The database server cluster-postgres belongs to the platform and the project shop to the organization org-1a2b3c4d, but a shared database server holds the databases of one organization only. It is not made: …". This does not count as a failure, so Geass does not give up on it: once the platform gives the server or the project to one organization, the database is made within seconds. The logical database also gets the condition `OrganizationIsolated`, `False`, with the reason `MixedOrganizations`, and a `Warning` event with the same reason, once.

### Databases that were there before

A logical database that is already on its server when the rule finds it, because it was made before organizations were on or before this check, or through `kubectl`, is **kept as it is and still served**. Geass deletes, drops and moves nothing, `Ready` stays as it was, and the project's services keep connecting. It is reported instead:

- the condition `OrganizationIsolated` (`False`, `MixedOrganizations`), whose message names both owners and ends "It was made before this was checked, so it is kept as it is and still served, and listed for the platform's administrators to move";
- a `Warning` event `MixedOrganizations` on the logical database, when it is first found;
- the list below.

The condition goes when the server and the project belong to one owner again, or when organizations are turned off. "Already on its server" means it has a connection Secret (`status.connectionSecret`). One that never got that far is refused as above.

The condition's message is seen by the people of the project's organization, like the rest of the logical database, and so names the other organization by its resource ID (`org-…`), never by its display name.

### List the databases that mix

```bash
curl https://geass.example.com/api/shared-databases/mixed \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json"
```

or `geass api GET /shared-databases/mixed`. A platform administrator only. The reply:

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

`items` are the logical databases whose server is not their project's organization's, as it is while organizations are on. `made` says it is on the server already (kept and served) or not (refused). `servers` are the servers that hold one of them, with whose all their databases are: `""` is the projects of no organization. The list applies the rule even while organizations are off, so it shows what turning them on will report; `enforced` says whether they are on.

From the cluster:

```bash
kubectl -n geass-system get events --field-selector reason=MixedOrganizations
kubectl -n geass-system get geasslogicaldatabases -o json \
  | jq -r '.items[] | select(any(.status.conditions[]?; .type=="OrganizationIsolated")) | "\(.metadata.name) \(.spec.serverRef)"'
```

### An install that already has a mixed server

This is the case of a cluster that used the platform's server for every project and then gave projects to organizations. Do it before turning organizations on if you can: while they are off, projects are assigned as they always were.

1. List them with `GET /api/shared-databases/mixed`.
2. For each server in `servers`:
   - **All its databases are one organization's** (`owners` has one entry, an organization's): give the server to it with `POST /api/shared-databases/<server>/org` and `org=<organization>`. Nothing moves. The organization's people now see the server, and its administrators can delete it once no database is on it.
   - **It holds the databases of more than one owner:** choose who keeps it. For each database of another owner, set up a server of that owner's organization (`POST /api/shared-databases/create` with `org=`), create a logical database on it, copy the data across with `pg_dump` and `psql` (or `mysqldump` and `mysql`), point the services at the new connection Secret, and delete the old logical database, which drops its data from the old server. Geass has no tool for the copy, and this sequence has not been run end to end on a real cluster.
3. Turn organizations on, if they are not. Whatever is left is served and reported, not broken.

While organizations are on, the order matters. A project of no organization that has databases on the platform's server cannot be given to an organization, and the server cannot be given to the organization while the project is of none. Move the project's data first, or, as a platform administrator who accepts the short time in between, set the project's `spec.org` with `kubectl` and then give the server: until the server is given, the operator keeps serving the project's databases and reports them.

## What a project can and cannot do

- A login reaches only its own logical database. Geass revokes `PUBLIC` access to each logical database and grants it to that login alone. It changes nothing on the server's other databases, such as the `postgres` maintenance database, which keep the server's default access.
- Network isolation between projects does not apply to the server. The server runs in `geass-system`, and the isolation policies cover project namespaces. Any pod in the cluster can open a connection to the server's address, so the logins are what keep projects apart. See [Network isolation](/docs/guides/network-isolation/).
- A project cannot change how many connections its login may hold. Geass applies `connectionLimit` when it first creates the database and does not apply a later change. To change it afterwards, alter the login on the server, with `ALTER ROLE ... CONNECTION LIMIT` on PostgreSQL or `ALTER USER ... WITH MAX_USER_CONNECTIONS` on MySQL.

## Delete a logical database

Open the logical database's **Settings**, then **Delete logical database**, type its resource ID and confirm.

On a shared server, Geass runs a drop job that removes the database and its login, then deletes the connection Secret. On PostgreSQL the drop forces open connections closed. The drop is limited to three minutes. A server that is gone or not ready never keeps the logical database from being deleted, so after three minutes Geass lets go even if the drop did not finish. If the drop job fails, Geass lets go anyway, and the database can stay on the server. On a project's own server, Geass leaves the database on the server, as it always has.

:::danger
Dropping a shared database destroys its data. There is no backup to restore from.
:::

## Remove a server

1. Open **Settings**, then **Databases**.
2. Select **Remove** on the server's row.
3. Type the server's resource ID and select **Remove server**.

The button stays disabled while any project has a database on the server. The dialog says "Projects still have databases on it, so delete those first", and the API refuses with a count, for example "2 databases in 2 projects still use this server. Delete them first, because removing the server would destroy their data". Removing the server deletes everything on it.

## Command line and AI agents

The `geass` command line and the MCP server can read servers and logical databases, but have no command or tool to set one up. Use the dashboard, or `geass api` as above.

| To | Command | MCP tool |
| --- | --- | --- |
| List servers (they have no project) and dedicated databases | `geass databases list` | `list_databases` |
| Create a logical database | `geass api POST /logical-databases -d '{"project": "proj-1a2b3c4d", "environment": "production", "name": "orders", "server": "shared-postgres", "database": "orders"}'` | None |
| List logical databases | `geass api GET /logical-databases` | None |
| Read how to reach one | `geass api GET /logical-databases/orders/connection` | None |
| Delete one, or a server | `geass api DELETE /logical-databases/orders -q confirmName=orders` | `delete_resource` with `kind` set to `logical-databases` (for a server, `databases`), only with `--allow-delete` |

The connection call needs a workspace token with Full access. It shows the host, port, database and login. The password and connection string appear only with `-q reveal=true`, and the operator logs each reveal. The MCP server cannot ask for them. For a project token the call is refused. See [Command line and AI agents](/docs/guides/databases/#command-line-and-ai-agents).

## Limits and tradeoffs

- **One server, shared fate.** All projects share the server's CPU, memory, disk and connection slots. A busy project can slow the others, and Geass applies no per-project CPU or memory limit to it, because the server runs in `geass-system` and [project limits](/docs/guides/project-limits/) cap a project's own environments only. The only per-project limit is the connection limit on the login.
- **One volume.** The server's disk is shared. One project can fill it. Increasing the size is a change to the server, not to a project, and the dashboard offers no control for it. See [Storage and volumes](/docs/guides/storage/).
- **No backups.** Shared servers and logical databases are not backed up and cannot be restored to a point in time. Take your own dumps with `pg_dump` or `mysqldump` if you need them.
- **Only PostgreSQL and MySQL.** Redis and SQLite cannot be shared.
- **Version.** A shared PostgreSQL server runs version 16 unless you set `version` through the API.
- **High availability is chosen at setup.** The dashboard has a switch only when you create the server.
- **Regions.** When a server and a project both have a region, the two must match, so no query crosses regions. A project without a region uses the cluster's default region, and a side with no region at all is not checked.
- **No migration tool.** To move a project's data between a dedicated database and a shared one, dump and load it yourself.
- **One organization per server, and no server made for you.** While organizations are on, an organization's project needs a server of its organization, which a platform administrator sets up. Geass never splits a server or moves data between servers. The **Add resource** dialog of a platform administrator who is not in an organization lists every shared server in the project's region, including those of other owners, which the API then refuses.
- **The server's own login.** Like every database Geass creates, the server has an application login named `app` whose password is derived from the server's resource ID. It owns no logical database, and each logical database is closed to every login but its own. Treat the server's address as reachable from every project.

## What happens under the hood

- A shared server is a `GeassDatabase` with an empty `project`, in `geass-system`. Its pods, volume and connection Secret `<server ID>-connection` all live in `geass-system`.
- Creating a database and a login takes an administrative login on the server: the `<server ID>-superuser` Secret for PostgreSQL, and the root password in `<server ID>-bootstrap` for MySQL. A shared server has it from the start. A project's own server gets it when its first logical database is created, and until it exists the logical database reports "Waiting for the database server's administrator login". Once turned on, the login stays, because taking it away would strand databases still being created or dropped.
- Each logical database is a `GeassLogicalDatabase` in `geass-system`. The controller runs a Job named `<logical ID>-create` in the server's namespace, in the image `postgres:16-alpine` or `mysql:8.4`. It connects with the administrative login, creates the login and database, revokes public access and grants it to the login. It runs again safely: an existing login has its password and limit set again, and an existing database is kept.
- On delete, a Job named `<logical ID>-drop` runs `DROP DATABASE ... WITH (FORCE)` and drops the login. A finalizer, `geass.geass.dev/logicaldatabase`, holds the object until the Job finishes or three minutes pass.
- Jobs run in the server's region. They do not use the server's **Run on** tags, so on a shared server they run on nodes that are not reserved, and not on compliance nodes. On a project's own server they also carry the project's required tags.

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
The server is not **Ready** yet, or it runs in another region than the project. Wait for **Ready** on **Settings**, **Databases**, or check the region of each.

**The High availability switch is disabled.**
The cluster is not ready for it. Open **Settings**, **Usage**, and run the readiness check. See [Instances and high availability](/docs/guides/databases/#instances-and-high-availability).

**The logical database stays at "Waiting for the database server's administrator login".**
The server has not exposed its administrative login yet. This happens on a project's own server, which Geass turns the login on for when it sees its first logical database. Wait a few minutes and check the server's own status.

**"database server is not ready" or "database server ... is unavailable".**
The server is not running. Fix the server first. Neither message counts as a failure, and Geass keeps retrying.

**"database creation Job failed" and the logical database shows a failed state.**
Read the Job's output with `kubectl logs job/<logical ID>-create -n geass-system`. After fixing the cause, select **Retry** on the logical database.

**"database name must contain only letters, numbers, and underscores".**
The `database` value breaks the rule. Use letters, digits and underscores, and do not start with a digit.

**"This database server runs in the X region, but the project runs in the Y region".**
Choose a server in the project's region, or set up one there.

**"This database server does not run on nodes tagged hipaa, which the project requires".**
The project requires node tags that the server is not placed on. Set up a server with `runOnTags`, as in [Projects that require node tags](#projects-that-require-node-tags), delete the logical database and create it on that server. If the status has already become "Provisioning failed after repeated errors", the message is on the **Deployments** tab.

**"password authentication failed" for a logical database that worked.**
A later logical database in the same project environment used the same name, which reset the login's password. See the caution under [Create a database on the server](#create-a-database-on-the-server). Copy the current password from the newer database's connection Secret, or delete the extra logical database.

**"too many connections" from the database.**
The login reached its connection limit, 20 by default. Raise it on the server with `ALTER ROLE` or `ALTER USER`, and make the app use fewer connections or a pool.

**Removing the server is blocked.**
Projects still have databases on it. The message gives the count. Delete those logical databases first.

**"That server is the platform's and the project is in the organization …".**
Organizations are on, and the platform's server is for the projects of no organization. Choose a server of the project's organization, or set one up for it with `org=`. See [Organizations](#organizations).

**"… but a shared database server holds the databases of one organization only. It is not made: …".**
The logical database was written past the dashboard on a server of another owner. Nothing was made on the server. Delete it and create it on a server of the project's organization, or have a platform administrator give the server, or the project, to one organization. It is made once they have.

**"… It was made before this was checked, so it is kept as it is and still served …".**
The database was on the server before the rule found it. It works, and is listed by `GET /api/shared-databases/mixed`. See [An install that already has a mixed server](#an-install-that-already-has-a-mixed-server).

**Giving a project to an organization, or a server, is refused with 409.**
The project, or the server, has databases that would then share a server with another owner's. The message names them. Move their data or delete them first, in the order under [An install that already has a mixed server](#an-install-that-already-has-a-mixed-server).

## Related

- [Databases](/docs/guides/databases/)
- [Backups and restore](/docs/guides/backups-and-restore/)
- [Network isolation](/docs/guides/network-isolation/)
- [Scaling and placement](/docs/guides/scaling-and-placement/)
- [Compliance node tags](/docs/guides/compliance-tags/)
- [Command line](/docs/guides/command-line/)
- [Users and tokens](/docs/guides/users-and-tokens/)
