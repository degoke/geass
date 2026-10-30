---
title: "GeassLogicalDatabase"
description: "Reference for GeassLogicalDatabase: a database that Geass creates for a project on a Postgres or MySQL server that is already running."
sidebar:
  order: 4
---

A `GeassLogicalDatabase` gives a project a database on a server that already runs. Geass publishes the connection
details as a Secret in the project's namespace. The server is one of these:

- A [GeassDatabase](/docs/reference/resources/geass-database/) of the same project and environment.
- The cluster-wide shared server, which is a `GeassDatabase` with no project.

The dashboard creates a `GeassLogicalDatabase` when you select **Logical database** or a shared server in the
add-resource dialog. You can also create it with `kubectl`.

For the task-oriented view, see [Shared database servers](/docs/guides/shared-database-servers/) and [Databases](/docs/guides/databases/).

## Summary

| Property | Value |
| --- | --- |
| API version | `geass.geass.dev/v1alpha1` |
| Kind | `GeassLogicalDatabase` |
| Plural | `geasslogicaldatabases` |
| Scope | Namespaced. Create it in `geass-system`. The dashboard and the controllers look only there. |
| Subresources | `status` |
| Printer columns | None. `kubectl get` shows only the name and age. |
| Finalizer | `geass.geass.dev/logicaldatabase`. Geass adds it only for a database on the shared server |

## Examples

### On a project's own server

```yaml title="reports-db.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassLogicalDatabase
metadata:
  name: reports
  namespace: geass-system
spec:
  project: storefront
  environment: production
  serverRef: orders-db
  databaseName: reports
```

`serverRef` is the name of a Postgres or MySQL `GeassDatabase` in the same project and environment.

### On the shared server

```yaml title="storefront-app-db.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassLogicalDatabase
metadata:
  name: storefront-app
  namespace: geass-system
spec:
  project: storefront
  environment: production
  serverRef: cluster-postgres
  databaseName: app
  connectionLimit: 50
```

## Spec fields

| Field | Type | Required or default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.project` | string | Required | Minimum length 1 | The project that owns the database. The project must exist and be `Ready`. |
| `spec.environment` | string | Required | Pattern `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, max length 63 | The project environment. The connection Secret goes in the namespace `<project>-<environment>`. |
| `spec.serverRef` | string | Required | Minimum length 1 | The name of the `GeassDatabase` that hosts the database, in `geass-system`. The server must be Postgres or MySQL. |
| `spec.databaseName` | string | Required | The controller requires letters, digits, and underscores. The name cannot start with a digit (`^[a-zA-Z_][a-zA-Z0-9_]*$`). The CRD does not check it. | The name of the database in the terms of your app. On a project's own server, it is the real database name. On the shared server, Geass adds a prefix. See [Names on the shared server](#names-on-the-shared-server). |
| `spec.connectionLimit` | integer (int32) | Optional. The controller uses 20. | Minimum 1, maximum 1000 | The number of connections that the database's own login can hold. Applies on the shared server only. The controller reads it when it first creates the database. |

:::note
The Go types and the generated CRD agree for every field on this page.
:::

### Names on the shared server

Many projects share the server, so Geass changes the names. Geass makes the database name from the project, the
environment, and `databaseName`:

- The name is in lowercase.
- Every run of characters other than letters and digits becomes `_`.
- The parts are joined with `_`.
- If the name starts with a digit, Geass adds a `d` in front.
- If the name is longer than 60 characters, Geass shortens it and ends it with a hash.

The login is `u_` followed by 16 hex characters that Geass derives from the same three parts. The login owns the one
database and can reach nothing else.

On a project's own server, Geass makes no new login. The database uses the name that you gave. The connection Secret
contains the server's own `app` login.

## Status

| Field | Type | Meaning |
| --- | --- | --- |
| `status.connectionSecret` | string | The name of the Secret with the connection details, `<name>-connection`. |
| `status.jobName` | string | The name of the Job that created the database, `<name>-create`. |
| `status.database` | string | The real name of the database on the server. On the shared server, it is not `spec.databaseName`. |
| `status.username` | string | The login that owns the database on the shared server. |
| `status.reconcileRetry` | object | Failure counters. The same as for [GeassDatabase](/docs/reference/resources/geass-database/#ready-condition-and-retries). |
| `status.provision` | object | Deploy history. The same as for GeassDatabase. |
| `status.conditions` | list | `Ready`. `OrganizationIsolated` while the database's server does not belong to the organization of the database's project (see below). `Sandboxed` while the platform runs the code of organizations in a sandboxed runtime, and the database is on an organization's server or belongs to an organization's project (see below). |

`Ready` has three reasons:

| Reason | Status | Meaning |
| --- | --- | --- |
| `LogicalDatabaseReady` | `True` | The database exists and Geass wrote the connection Secret. The message is `Logical database connection is available`. |
| `ReconcileError` | `False` | Not ready yet. The message tells you what is missing. |
| `ProvisionFailed` | `False` | Geass stopped after repeated errors. The retry rules are the same as for GeassDatabase. See [Ready condition and retries](/docs/reference/resources/geass-database/#ready-condition-and-retries). |

`OrganizationIsolated` is present only while [organizations](/docs/guides/organizations-and-workspaces/) are on, and the
database is on a shared server that has a different owner from the database's project. These are the cases:

- The server belongs to another organization.
- The server is the platform's server, and the project belongs to an organization.
- The server belongs to an organization, and the project belongs to no organization.

In these cases, the condition status is `False` and its reason is `MixedOrganizations`. Its message names both owners by
their resource IDs. What Geass does next depends on the database:

- If the database is not on the server yet, Geass does not make it. `Ready` is `False` with the same message. This
  message does not count as a failure.
- If the database is already on the server (it has `status.connectionSecret`), Geass keeps and serves it. Geass does
  not change `Ready`.

When Geass first finds either case, it records a `Warning` event with the reason `MixedOrganizations`. Geass removes the
condition when the server and the project belong to one owner again, or when organizations are turned off. For more
information, see [Shared database servers](/docs/guides/shared-database-servers/#organizations).

`Sandboxed` tells you how the Job that made the database ran under the platform's
[sandboxed runtime](/docs/operations/sandboxed-runtime/#the-jobs-on-their-data). The condition has these values:

- `True` `RuntimeClass` (`Runs under RuntimeClass gvisor`).
- `False` `ReservedNodes`, on nodes that are reserved for the organization.
- `False` `RuntimeClassMissing`, while the RuntimeClass is missing.

While the RuntimeClass is missing, these things occur:

- Geass makes no Job.
- `Ready` is `False` with `Waiting for the sandboxed runtime: …`. This message does not count as a failure.
- Geass records a `Warning` event `SandboxRuntimeClassMissing` one time.

A Job that Geass made earlier keeps how it ran. If you delete a database while the RuntimeClass is missing, Geass does
not drop the database from its server. Geass releases the resource after three minutes.

A `GeassLogicalDatabase` has no draft step. It has no deploy gate, so Geass starts to create the database immediately
when the resource exists.

You can see these messages:

| Message | Cause |
| --- | --- |
| `project "<project>" does not exist`, `project "<project>" is not ready`, `project "<project>" has no <environment> environment` | The project or environment is missing or not ready. |
| `database server "<name>" is unavailable` | There is no `GeassDatabase` with the name in `serverRef` in `geass-system`. |
| `database server is not ready` | The server's `Ready` condition is not `True`. |
| `database server is in a different project or environment` | A project's own server must match `project` and `environment`. |
| `database server has no connection secret` | The server has no `status.connectionSecret` yet. |
| `logical databases require a Postgres or MySQL server` | The server's engine is SQLite or Redis. |
| `database name must contain only letters, numbers, and underscores` | `spec.databaseName` does not match the pattern. |
| `This database server runs in the <a> region, but the project runs in the <b> region` | A shared server and the project are pinned to different regions. |
| `This database server does not run on nodes tagged <tags>, which the project requires` | The project's `spec.requiredTags` include a tag that the shared server is not placed on. The server's `geass.dev/node-placement` has the tag in neither `tags` nor `required`. A server installed before placement existed runs anywhere, so Geass refuses it. This message counts as a failure. |
| `This database server belongs to an organization that the project is not in` | Organizations are off, the shared server has `spec.org`, and the project does not belong to that organization. This message counts as a failure. |
| `The database server <server> belongs to <owner> and the project <project> to <owner>, but a shared database server holds the databases of one organization only. It is not made: …` | Organizations are on, the shared server and the project do not belong to one organization, and the database is not on the server yet. This message does not count as a failure. Geass makes the database after the platform gives the server or the project to one owner. |
| `database connection secret is unavailable` | The server's connection Secret is missing. |
| `Waiting for the database server's administrator login` | The server has not exposed its administrator credentials yet. |
| `creating logical database` | The create Job is running. |
| `database creation Job failed` | The create Job failed. Geass treats this failure as permanent. |

## What the dashboard writes

| Where | What it does |
| --- | --- |
| **+ Add resource** > **Database** > **Logical database** > one of the project environment's own Postgres or MySQL servers | Creates a `GeassLogicalDatabase` on that server. When there is no server to use, the dashboard disables the **Logical database** row and shows "No server yet". |
| **+ Add resource** > **Database** > **Logical database** > a shared server | Creates a `GeassLogicalDatabase` on that shared server. The dashboard disables the entry until the server is `Ready`. The dashboard lists only servers in the project's region, or with no region. |
| Resource panel, **Settings** tab, **Logical DB** section | Shows **Server** and **Database name**. This section is read-only. |
| **Retry** | Resets a `ProvisionFailed` resource. |

The dashboard fills in a generated name, `spec.databaseName: app`, and the server that you selected. The dashboard has
no field for `connectionLimit`. To set it, use `kubectl` or the REST API. The success message is `Added database`, and
there is no deploy step. Geass starts immediately.

The dashboard refuses a request that cannot work. It shows one of these messages:

- `That database server does not exist`
- `A database can only be created on a Postgres or MySQL server`
- `That server belongs to another project or environment`
- `That server belongs to an organization the project is not in`
- `That server is the platform's and the project is in the organization <org>, but with organizations on a shared database server holds the databases of one organization only. …`
- `This server runs in the <a> region, but the project runs in the <b> region. Every query would cross regions`
- `The connection limit must be a whole number from 1 to 1000`
- `logical database already exists`

The dashboard does not check required node tags. It offers a shared server when the server is `Ready` and in the
project's region. The controller does the tag check after that. See the messages earlier on this page.

## From the command line and the API

The versioned API has these routes:

- `GET /api/v1/logical-databases`
- `GET /api/v1/logical-databases/{name}`
- `POST /api/v1/logical-databases`
- `DELETE /api/v1/logical-databases/{name}`
- `GET /api/v1/logical-databases/{name}/connection`

The command line has no `logical-databases` command. To call these routes, use `geass api`, for example
`geass api GET /logical-databases`.

The MCP server has no tool to list or read logical databases. `delete_resource` deletes one when `kind` is
`logical-databases` and the server runs with `--allow-delete`.

A logical database is never listed as `draft`, because it has no deploy step.

The connection call shows `host`, `port`, `database`, and `username` to a token with Full access. It adds `password`
and `uri` only for `?reveal=true`. For more information, see
[Shared database servers](/docs/guides/shared-database-servers/#command-line-and-ai-agents).

## Connection Secret

Geass writes the Secret `<name>-connection` in `<project>-<environment>`. The Secret has the keys `host`, `port`,
`database`, `username`, `password`, and `uri`. The `uri` is `postgresql://<user>:<password>@<host>:<port>/<database>` or `mysql://...`.

## Editing by hand

You can create a `GeassLogicalDatabase` with `kubectl`.

:::caution
Do not edit `spec.serverRef`, `spec.databaseName`, or `spec.connectionLimit` after the database exists. Instead,
delete the resource and create a new one. Geass does not run the create Job again after it succeeds. A changed name
gives a connection Secret that points at a database that nobody created. A changed limit never gets to the server.
:::

When you delete a `GeassLogicalDatabase`, the result depends on the server:

- On the shared server, the finalizer runs a Job `<name>-drop`. The Job drops the database and its login. Then the
  finalizer removes the connection Secret. Geass waits up to 3 minutes. If the server is gone, not ready, or slow,
  Geass stops waiting and releases the resource.
- On a project's own server, Geass drops nothing. The database stays on the server. The connection Secret
  `<name>-connection` and the Job `<name>-create` also stay, because nothing owns them. If you no longer need them,
  remove them yourself.
- When you delete the project, Geass deletes its logical databases.

The dashboard refuses to delete a shared server while logical databases use it. `kubectl` does not.

## What happens under the hood

1. Geass checks the project and the server. For the shared server, Geass adds the finalizer.
2. Geass finds the server's administrator login. For Postgres, the login is in the Secret `<server>-superuser`. For
   MySQL, the login is the user `root` with the password in `<server>-bootstrap`. A Postgres server creates that
   Secret only after a logical database refers to the server. The database controller sees the reference and turns
   on the administrator login.
3. On the shared server, Geass generates a password. Geass keeps the password in the Secret `<name>-login`, beside the
   server (in the server's namespace).
4. Geass runs a Job `<name>-create` in the server's namespace (`postgres:16-alpine` or `mysql:8.4`). The Job creates
   the login (shared server only), creates the database, and grants the login rights on it. On Postgres, the Job also
   removes the public role's rights on the database.
5. Geass writes `<name>-connection` into `<project>-<environment>` and sets `Ready`.

The Job has the labels `geass.dev/logical-database=<name>` and `geass.dev/logical-database-role=create` (or `drop`).
The Job runs in the server's region, and never on the node tags that the server asked for through **Run on**. On a
project's own server, the Job also keeps the tags that the project required when Geass placed the server. On a shared
server, there are no such tags. So the Job runs on nodes that are not reserved, and that excludes compliance nodes.

```bash title="Inspect a logical database"
kubectl -n geass-system get geasslogicaldatabases
kubectl -n geass-system get geasslogicaldatabase storefront-app -o yaml

# The Job and its output (the server's namespace is geass-system for the shared server)
kubectl get jobs -A -l geass.dev/logical-database=storefront-app
kubectl -n geass-system logs job/storefront-app-create

# The connection Secret
kubectl -n storefront-production get secret storefront-app-connection
```

## Troubleshooting

### `Waiting for the database server's administrator login`

The server has not exposed an administrator login yet. For Postgres, the database controller turns on the login when
it sees a logical database for the server. Then CloudNativePG creates `<server>-superuser`. Wait a minute. If the
message stays, check the server's `Ready` condition, and look for the Secret in the server's namespace.

### `database creation Job failed`

Geass stops retrying immediately. Read the Job's logs (`kubectl logs job/<name>-create` in the server's namespace).
These are typical causes:

- An administrator login without the rights to create databases.
- A name that the engine rejects.

Fix the cause, delete the Job, and click **Retry**.

### `database server is not ready` or `database server "<name>" is unavailable`

Wait for the server. These messages do not count as failures, so Geass continues to check every 30 seconds.

### `This database server runs in the <a> region, but the project runs in the <b> region`

A shared server pinned to one region does not serve a project in another region. Select a server in the project's
region, or create the project in the server's region.

### `This database server does not run on nodes tagged <tags>, which the project requires`

The project requires node tags, and the shared server is not placed on all of them. See
[Shared database servers](/docs/guides/shared-database-servers/#projects-that-require-node-tags).

This message counts as a failure. After five failed passes in a row, or after three minutes, these things occur:

- `Ready` becomes `ProvisionFailed`.
- The message is no longer in the condition.
- `status.provision.provisionAttempts[].errors` keeps the message.

A change to the project's tags checks again a resource that is still waiting, but not a failed resource. After you
change the tags, click **Retry**. Alternatively, delete the logical database and create it on a server that qualifies.

A server's placement does not change. To read the placement, run
`kubectl -n geass-system get geassdatabase <server> -o jsonpath='{.metadata.annotations.geass\.dev/node-placement}'`.

### A deleted database lingers

On the shared server, Geass waits up to 3 minutes for the drop Job. After that, Geass releases the resource. Check the
server's namespace for `<name>-drop` and `<name>-login`.

## Related

- [Shared database servers](/docs/guides/shared-database-servers/)
- [Databases](/docs/guides/databases/)
- [GeassDatabase](/docs/reference/resources/geass-database/)
- [GeassProject](/docs/reference/resources/geass-project/)
- [Resources overview](/docs/reference/resources/overview/)
