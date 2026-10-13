---
title: "GeassLogicalDatabase"
description: "Reference for GeassLogicalDatabase: a database that Geass creates for a project on a Postgres or MySQL server that is already running."
sidebar:
  order: 4
---

A `GeassLogicalDatabase` gives a project a database on a server that already runs, and publishes the connection details as a Secret in the project's namespace. The server is a [GeassDatabase](/docs/reference/resources/geass-database/) of the same project and environment, or the cluster-wide shared server (a `GeassDatabase` with no project). The dashboard creates it when you pick **Logical database** or a shared server in the add-resource dialog. You can also create it with `kubectl`.

For the task-oriented view, see [Shared database servers](/docs/guides/shared-database-servers/) and [Databases](/docs/guides/databases/).

## Summary

| Property | Value |
| --- | --- |
| API version | `geass.geass.dev/v1alpha1` |
| Kind | `GeassLogicalDatabase` |
| Plural | `geasslogicaldatabases` |
| Scope | Namespaced. Create it in `geass-system`: the dashboard and the controllers only look there. |
| Subresources | `status` |
| Printer columns | None. `kubectl get` shows only the name and age. |
| Finalizer | `geass.geass.dev/logicaldatabase`, added only for a database on the shared server |

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
| `spec.project` | string | Required | Minimum length 1 | The project that owns the database. It must exist and be `Ready`. |
| `spec.environment` | string | Required | Pattern `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, max length 63 | The project environment. The connection Secret goes in the namespace `<project>-<environment>`. |
| `spec.serverRef` | string | Required | Minimum length 1 | Name of the `GeassDatabase` that hosts the database, in `geass-system`. It must be Postgres or MySQL. |
| `spec.databaseName` | string | Required | The controller requires letters, digits and underscores, not starting with a digit (`^[a-zA-Z_][a-zA-Z0-9_]*$`). The CRD does not check it. | What the database is called in your app's terms. On a project's own server it is the real database name. On the shared server it is prefixed (see [Names on the shared server](#names-on-the-shared-server)). |
| `spec.connectionLimit` | integer (int32) | Optional. The controller uses 20. | Minimum 1, maximum 1000 | How many connections the database's own login may hold. Applies on the shared server only. It is read when the database is first created. |

:::note
The Go types and the generated CRD agree for every field on this page.
:::

### Names on the shared server

Many projects share the server, so Geass changes the names. It builds the database name from the project, environment and `databaseName`: lowercased, with every run of characters other than letters and digits turned into `_`, and joined with `_`. A name that starts with a digit gets a `d` in front. A name longer than 60 characters is shortened and ends with a hash. The login is `u_` followed by 16 hex characters derived from the same three parts. The login owns the one database and can reach nothing else.

On a project's own server there is no new login. The database uses the name you gave and the connection Secret carries the server's own `app` login.

## Status

| Field | Type | Meaning |
| --- | --- | --- |
| `status.connectionSecret` | string | Name of the Secret with the connection details, `<name>-connection`. |
| `status.jobName` | string | Name of the Job that created the database, `<name>-create`. |
| `status.database` | string | The database's real name on the server. On the shared server it is not `spec.databaseName`. |
| `status.username` | string | The login that owns the database on the shared server. |
| `status.reconcileRetry` | object | Failure counters. Same as for [GeassDatabase](/docs/reference/resources/geass-database/#ready-condition-and-retries). |
| `status.provision` | object | Deploy history. Same as for GeassDatabase. |
| `status.conditions` | list | One condition: `Ready`. |

`Ready` has three reasons:

| Reason | Status | Meaning |
| --- | --- | --- |
| `LogicalDatabaseReady` | `True` | The database exists and the connection Secret is written. The message is `Logical database connection is available`. |
| `ReconcileError` | `False` | Not ready yet. The message says what is missing. |
| `ProvisionFailed` | `False` | Geass gave up after repeated errors. The retry rules are the same as for GeassDatabase: see [Ready condition and retries](/docs/reference/resources/geass-database/#ready-condition-and-retries). |

A `GeassLogicalDatabase` has no draft step. It has no deploy gate, so Geass starts creating it as soon as it exists.

The messages you can see:

| Message | Cause |
| --- | --- |
| `project "<project>" does not exist`, `project "<project>" is not ready`, `project "<project>" has no <environment> environment` | The project or environment is missing or not ready. |
| `database server "<name>" is unavailable` | There is no `GeassDatabase` named `serverRef` in `geass-system`. |
| `database server is not ready` | The server's `Ready` condition is not `True`. |
| `database server is in a different project or environment` | A project's own server must match `project` and `environment`. |
| `database server has no connection secret` | The server has no `status.connectionSecret` yet. |
| `logical databases require a Postgres or MySQL server` | The server's engine is SQLite or Redis. |
| `database name must contain only letters, numbers, and underscores` | `spec.databaseName` fails the pattern. |
| `This database server runs in the <a> region, but the project runs in the <b> region` | A shared server and the project are pinned to different regions. |
| `This database server does not run on nodes tagged <tags>, which the project requires` | The project's `spec.requiredTags` include a tag that the shared server is not placed on (its `geass.dev/node-placement` has it in neither `tags` nor `required`). A server installed before placement existed runs anywhere, so it is refused. This message counts as a failure. |
| `database connection secret is unavailable` | The server's connection Secret is missing. |
| `Waiting for the database server's administrator login` | The server has not exposed its administrator credentials yet. |
| `creating logical database` | The create Job is running. |
| `database creation Job failed` | The create Job failed. Geass treats this as permanent. |

## What the dashboard writes

| Where | What it does |
| --- | --- |
| **+ Add resource** > **Database** > **Logical database** | Creates a `GeassLogicalDatabase` on the first Postgres or MySQL server of the project environment. The entry is disabled with `Create a database server first` when there is none. |
| **+ Add resource** > **Database** > the shared server, shown as `<name> (shared)` | Creates one on that shared server. The entry is disabled until the server is `Ready`. Only servers in the project's region, or with no region, are listed. |
| Resource panel, **Settings** tab, **Logical DB** section | Shows **Server** and **Database name**. It is read-only. |
| **Retry** | Resets a `ProvisionFailed` resource. |

The dashboard fills in a generated name, `spec.databaseName: app` and the server you chose. It has no field for `connectionLimit`: set it with `kubectl` or the REST API. The success message says `Logical database saved as a draft`, but there is no deploy step. Geass starts at once.

The dashboard refuses a request that cannot work, with these messages: `That database server does not exist`, `A database can only be created on a Postgres or MySQL server`, `That server belongs to another project or environment`, `This server runs in the <a> region, but the project runs in the <b> region. Every query would cross regions`, `The connection limit must be a whole number from 1 to 1000` and `logical database already exists`. It does not check required node tags: a shared server is offered when it is `Ready` and in the project's region, and the tag check happens afterwards in the controller (see the messages above).

## From the command line and the API

The versioned API has `GET /api/v1/logical-databases`, `GET /api/v1/logical-databases/{name}`, `POST /api/v1/logical-databases`, `DELETE /api/v1/logical-databases/{name}` and `GET /api/v1/logical-databases/{name}/connection`. The command line has no `logical-databases` command, so call them with `geass api`, for example `geass api GET /logical-databases`. The MCP server has no tool to list or read logical databases, and `delete_resource` deletes one when `kind` is `logical-databases` and the server runs with `--allow-delete`. A logical database is never listed as `draft`: it has no deploy step. The connection call shows `host`, `port`, `database` and `username` to a token with Full access, and adds `password` and `uri` only for `?reveal=true`. See [Shared database servers](/docs/guides/shared-database-servers/#command-line-and-ai-agents).

## Connection Secret

Geass writes the Secret `<name>-connection` in `<project>-<environment>` with the keys `host`, `port`, `database`, `username`, `password` and `uri`. The `uri` is `postgresql://<user>:<password>@<host>:<port>/<database>` or `mysql://...`.

## Editing by hand

You can create a `GeassLogicalDatabase` with `kubectl`.

:::caution
Do not edit `spec.serverRef`, `spec.databaseName` or `spec.connectionLimit` after the database exists. Geass does not run the create Job again once it succeeds, so a changed name would produce a connection Secret that points at a database nobody created, and a changed limit never reaches the server. Delete the resource and create a new one instead.
:::

When you delete one:

- On the shared server, the finalizer runs a Job `<name>-drop` that drops the database and its login, then removes the connection Secret. Geass waits up to 3 minutes. If the server is gone, not ready or slow, Geass stops waiting and releases the resource.
- On a project's own server, nothing is dropped. The database stays on the server. The connection Secret `<name>-connection` and the Job `<name>-create` also stay, because nothing owns them. Remove them yourself if you no longer need them.
- Deleting the project deletes its logical databases.

The dashboard refuses to delete a shared server while logical databases use it. `kubectl` does not.

## What happens under the hood

1. Geass checks the project and the server. For the shared server it adds the finalizer.
2. Geass finds the server's administrator login: for Postgres the Secret `<server>-superuser`, for MySQL the user `root` with the password in `<server>-bootstrap`. A Postgres server only creates that Secret once a logical database refers to it. The database controller notices and turns it on.
3. On the shared server Geass generates a password and stores it in the Secret `<name>-login`, beside the server (in its namespace).
4. Geass runs a Job `<name>-create` in the server's namespace (`postgres:16-alpine` or `mysql:8.4`). It creates the login (shared server only), creates the database, and grants the login rights on it. On Postgres it also removes the public role's rights on the database.
5. Geass writes `<name>-connection` into `<project>-<environment>` and sets `Ready`.

The Job carries the labels `geass.dev/logical-database=<name>` and `geass.dev/logical-database-role=create` (or `drop`). It runs in the server's region and never on the node tags the server asked for through **Run on**. On a project's own server it also keeps the tags the project required when the server was placed. On a shared server there are none, so the Job runs on nodes that are not reserved, which excludes compliance nodes.

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

The server has not yet exposed an administrator login. For Postgres, the database controller enables it once it sees a logical database for the server, and CloudNativePG then creates `<server>-superuser`. Wait a minute. If it persists, check the server's `Ready` condition and look for the Secret in the server's namespace.

### `database creation Job failed`

Geass stops retrying at once. Read the Job's logs (`kubectl logs job/<name>-create` in the server's namespace). Typical causes are an administrator login without the rights to create databases and a name the engine rejects. Fix it, delete the Job and press **Retry**.

### `database server is not ready` or `database server "<name>" is unavailable`

Wait for the server. These messages do not count as failures, so Geass keeps checking every 30 seconds.

### `This database server runs in the <a> region, but the project runs in the <b> region`

A shared server pinned to one region does not serve a project in another. Choose a server in the project's region, or create the project in the server's region.

### `This database server does not run on nodes tagged <tags>, which the project requires`

The project requires node tags and the shared server is not placed on all of them. See [Shared database servers](/docs/guides/shared-database-servers/#projects-that-require-node-tags). The message counts as a failure, so after five failed passes in a row, or three minutes, `Ready` becomes `ProvisionFailed`, the message above is gone from the condition, and `status.provision.provisionAttempts[].errors` keeps it. A change to the project's tags re-checks a resource that is still waiting, but not a failed one: press **Retry** after you change them, or delete the logical database and create it on a server that qualifies. A server's placement does not change. Read it with `kubectl -n geass-system get geassdatabase <server> -o jsonpath='{.metadata.annotations.geass\.dev/node-placement}'`.

### A deleted database lingers

On the shared server, Geass waits up to 3 minutes for the drop Job. After that it lets go. Check the server's namespace for `<name>-drop` and `<name>-login`.

## Related

- [Shared database servers](/docs/guides/shared-database-servers/)
- [Databases](/docs/guides/databases/)
- [GeassDatabase](/docs/reference/resources/geass-database/)
- [GeassProject](/docs/reference/resources/geass-project/)
- [Resources overview](/docs/reference/resources/overview/)
