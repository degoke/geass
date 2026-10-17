---
title: "GeassDatabase"
description: "Reference for GeassDatabase: a Postgres, MySQL, SQLite or Redis database that runs in your cluster, or a link to an external database."
sidebar:
  order: 3
---

A `GeassDatabase` is one database server for a project environment. It can run in your cluster (Postgres, MySQL, SQLite or Redis), or it can point at a database that runs somewhere else. The dashboard creates and edits it when you add a database to a project, and it is the normal way to work with one. You can also create it with `kubectl`. A `GeassDatabase` with no `spec.project` is the cluster-wide shared server that many projects use through [GeassLogicalDatabase](/docs/reference/resources/geass-logical-database/) resources.

For the task-oriented view, see [Databases](/docs/guides/databases/), [Backups and restore](/docs/guides/backups-and-restore/) and [Shared database servers](/docs/guides/shared-database-servers/).

## Summary

| Property | Value |
| --- | --- |
| API version | `geass.geass.dev/v1alpha1` |
| Kind | `GeassDatabase` |
| Plural | `geassdatabases` |
| Scope | Namespaced. Create it in `geass-system`: the dashboard and the other controllers only look there. |
| Subresources | `status` |
| Printer column `Environment` | `.spec.environment` |
| Printer column `Engine` | `.spec.engine` |
| Printer column `Ready` | `.status.conditions[?(@.type=="Ready")].status` |
| Finalizer | `geass.geass.dev/database` |

The database itself runs in the project environment's namespace, `<project>-<environment>`. A shared server has no project and runs in `geass-system`.

## Examples

Every example sets `spec.deploy.enabled: true`. The dashboard reads that field to tell a deployed database from a draft. The `kubectl` examples do not carry the `geass.dev/deploy-workflow` annotation, so Geass provisions them at once. See [Drafts and the Deploy flow](#drafts-and-the-deploy-flow).

### Postgres in the cluster, with backups

```yaml title="orders-db.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassDatabase
metadata:
  name: orders-db
  namespace: geass-system
spec:
  project: storefront
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

Backups only protect the database once the project has backup storage. See [Backups](#backups).

### Highly available Postgres

```yaml title="orders-db-ha.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassDatabase
metadata:
  name: orders-db
  namespace: geass-system
spec:
  project: storefront
  environment: production
  engine: Postgres
  highAvailability: true
  storageSize: 20Gi
  deploy:
    enabled: true
```

Geass runs three instances. It holds the database back until a [GeassHAReadiness](/docs/reference/resources/geass-ha-readiness/) report says the cluster has at least three healthy nodes.

### Restore into a new database

```yaml title="orders-db-restored.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassDatabase
metadata:
  name: orders-db-restored
  namespace: geass-system
spec:
  project: storefront
  environment: production
  engine: Postgres
  version: "16"
  storageSize: 20Gi
  backup:
    enabled: true
  restoreFrom:
    archive: orders-db-1a2b3c4d
    time: "2026-10-11T09:30:00Z"
  deploy:
    enabled: true
```

`restoreFrom.archive` is the name of a [GeassBackupArchive](/docs/reference/resources/geass-backup-archive/). The new database must match the original's engine, Postgres version, database name and have at least its storage. See [Restoring](#restoring).

### PlanetScale, created by Geass

```yaml title="orders-planetscale.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassDatabase
metadata:
  name: orders-planetscale
  namespace: geass-system
spec:
  project: storefront
  environment: production
  engine: MySQL
  placement: External
  provider: PlanetScale
  mode: Create
  connectionRef:
    name: storefront-planetscale
  deploy:
    enabled: true
```

`connectionRef` names a [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/) for PlanetScale that is scoped to the same project. `engine` can be `MySQL` or `Postgres`: PlanetScale hosts only those two. `externalPort` is optional and defaults to the engine's port. You can add `clusterSize` to choose the size PlanetScale creates the database with. See [External databases](#external-databases).

### An existing external database

```yaml title="orders-external.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassDatabase
metadata:
  name: orders-external
  namespace: geass-system
spec:
  project: storefront
  environment: production
  engine: Postgres
  placement: External
  mode: Connect
  externalHost: db.example.com
  externalPort: 5432
  username: orders
  databaseName: orders
  passwordSecretRef:
    name: orders-external-password
    key: password
  deploy:
    enabled: true
```

```yaml title="orders-external-password.yaml"
apiVersion: v1
kind: Secret
metadata:
  name: orders-external-password
  namespace: geass-system
stringData:
  password: change-me
```

The Secret must be in `geass-system`, next to the `GeassDatabase`.

### A shared server

```yaml title="cluster-postgres.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassDatabase
metadata:
  name: cluster-postgres
  namespace: geass-system
spec:
  engine: Postgres
  serverRegion: eu
  storageSize: 20Gi
  deploy:
    enabled: true
```

With no `project`, this is the cluster-wide server. Projects take databases on it with [GeassLogicalDatabase](/docs/reference/resources/geass-logical-database/).

## Spec fields

Defaults marked "CRD default" are filled in by the API server. Defaults marked "controller" are applied by the operator when the field is empty.

| Field | Type | Required or default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.project` | string | Optional | None | The owning project. Empty makes this the cluster-wide shared server, which runs in `geass-system`. |
| `spec.environment` | string | Optional. Required in practice for a project database. | Pattern `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, max length 63 | The project environment. It must be one of the project's `spec.environments`. Empty for a shared server. |
| `spec.engine` | string | Required | Enum `Postgres`, `MySQL`, `SQLite`, `Redis` | The database engine. `SQLite` runs rqlite, which serves SQLite over HTTP on port 4001. `Redis` runs without a volume. A PlanetScale database can be `MySQL` or `Postgres`. |
| `spec.placement` | string | CRD default `InCluster` | Enum `InCluster`, `External` | `InCluster` runs the database in the cluster. `External` connects to a database elsewhere. |
| `spec.provider` | string | Optional | Enum `PlanetScale`, `AWS` | The external provider. Only meaningful with `placement: External`. `PlanetScale` with `mode: Create` makes Geass create the database on PlanetScale. `AWS` only labels the status message: Geass has no RDS client. |
| `spec.mode` | string | Optional | Enum `Create`, `Connect` | Only `Create` with `provider: PlanetScale` changes behavior. `Connect` and an empty value behave the same: Geass uses the host, username and password you give it. |
| `spec.highAvailability` | boolean | `false` | None | Asks for a multi-instance topology. In the cluster only. Needs a ready [GeassHAReadiness](/docs/reference/resources/geass-ha-readiness/) report. Not supported for SQLite. |
| `spec.connectionRef` | LocalObjectReference | Optional | None | A [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/) in `geass-system`. It must be scoped to the same project. |
| `spec.connectionRef.name` | string | CRD default `""` | None | Name of the connection. |
| `spec.databaseName` | string | Optional. The controller uses the resource name. | None | The database name. Used for Postgres (`initdb`), for MySQL, for the `database` key of the connection Secret, and as the PlanetScale database name. |
| `spec.externalHost` | string | Optional | None | Host of an external database. Used for `placement: External`. |
| `spec.externalPort` | integer (int32) | Optional. The controller uses the engine's default port (5432, 3306, 6379 or 4001). | None | Port of an external database. |
| `spec.clusterSize` | string | Optional. Empty leaves the size to PlanetScale. | None in the CRD. The dashboard API rejects a size on a non-PlanetScale database, and a name PlanetScale does not offer for the engine when it can check | The PlanetScale cluster size, for example `PS_10`, that a database is created with. It is read only when Geass creates the database on PlanetScale (`provider: PlanetScale`, `mode: Create`). Changing it later does not resize the database, and the dashboard API refuses the change once the connection Secret exists. The dashboard API lists the sizes at `GET /api/planetscale/cluster-sizes?project=<project>&connection=<connection>&engine=MySQL` (or `Postgres`). The dashboard has no field for it. |
| `spec.username` | string | Optional | None | Login for an external database. |
| `spec.passwordSecretRef` | SecretKeySelector | Optional | None | Secret in `geass-system` that holds the external password. The key defaults to `password` when empty. |
| `spec.passwordSecretRef.name` | string | CRD default `""` | None | Secret name. |
| `spec.passwordSecretRef.key` | string | Required when `passwordSecretRef` is set | None | Key inside the Secret. |
| `spec.passwordSecretRef.optional` | boolean | Optional | None | Standard Kubernetes field. Geass does not read it. |
| `spec.version` | string | CRD default `"16"` | None | Postgres major version. Geass uses it as the image tag `ghcr.io/cloudnative-pg/postgresql:<version>`. MySQL, Redis and SQLite do not read it: their versions come from the image Geass pins (`mysql:8.4.11`, `redis:7.4.11-alpine`, `rqlite/rqlite:8.36.16`). |
| `spec.instances` | integer (int32) | CRD default `1` | Minimum 1 | Number of instances. With `highAvailability` Geass uses at least 3. See the caution under [Engines and high availability](#engines-and-high-availability). |
| `spec.storageSize` | Quantity | Optional. The controller uses `10Gi`, or `1Gi` for SQLite. | None | Disk size for each instance. Not used by Redis or external databases. The dashboard writes it explicitly. |
| `spec.resources` | ResourceRequirements | Optional. The controller uses 250m CPU and 512Mi memory (SQLite: 100m and 128Mi). | None | CPU and memory for each instance. Geass only reads it when `requests` is set. Set `requests` and `limits` together. |
| `spec.deploy.enabled` | boolean | `false` | None | Gates provisioning, but only for resources that carry the `geass.dev/deploy-workflow: "true"` annotation. The API, the command line and the MCP server report a database with this field `false` as `draft`, whatever the annotation says. See [Drafts and the Deploy flow](#drafts-and-the-deploy-flow). |
| `spec.backup` | object | Optional | None | Backups for in-cluster Postgres, MySQL and SQLite. Ignored for Redis and external databases. |
| `spec.backup.enabled` | boolean | Required when `backup` is set | None | Turns backups on. |
| `spec.backup.retentionDays` | integer (int32) | CRD default `7` | Minimum 1, maximum 365 | How many days back you can restore. |
| `spec.restoreFrom` | object | Optional | CEL: `!(has(self.restorePoint) && has(self.time))`, message "set either restorePoint or time, not both" | Creates this database from another database's backups. Read once, while the database is first provisioned. |
| `spec.restoreFrom.archive` | string | Required when `restoreFrom` is set | Minimum length 1 | Name of the [GeassBackupArchive](/docs/reference/resources/geass-backup-archive/) to restore from. |
| `spec.restoreFrom.restorePoint` | string | Optional | Exclusive with `time` | ID of a restore point listed on the archive. |
| `spec.restoreFrom.time` | date-time | Optional | Exclusive with `restorePoint` | Restore to this moment. Postgres only. It must be inside the archive's recovery window. |
| `spec.runOn.tags` | array of strings | Optional | Max 8 items, each matching `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$` and at most 40 characters, set semantics | Node tags the database must run on. A node needs all of them. The project's `spec.requiredTags` apply as well, without being listed here. Read once, when the database is first provisioned. |
| `spec.serverRegion` | string | Optional | Pattern `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, max length 40 | Where a shared server runs. A database with a project follows its project's region instead. |

:::note
The Go types and the generated CRD agree for every field on this page.
:::

### Engines and high availability

| Engine | What Geass runs | Host in `status.host` | Backups |
| --- | --- | --- | --- |
| `Postgres` | A CloudNativePG `Cluster` named after the database. | `<name>-rw.<namespace>.svc` | Continuous. Restore to any moment. |
| `MySQL` | One pod from the official `mysql:8.4.11` image, as the StatefulSet `geass-mysql-<name>` with a volume. With `highAvailability: true`, a MOCO `MySQLCluster` of three or five servers instead. | `geass-mysql-<name>.<namespace>.svc`, or with high availability `moco-<name>-primary.<namespace>.svc` | Daily snapshot. |
| `SQLite` | One rqlite pod (`rqlite/rqlite:8.36.16`) with a volume, as a Deployment. | `<name>.<namespace>.svc` | Daily snapshot. |
| `Redis` | One pod from the official `redis:7.4.11-alpine` image, as the StatefulSet `geass-redis-db-<name>-master`. No volume: data does not survive the pod being replaced. | `geass-redis-db-<name>-master.<namespace>.svc` | None. |

With `highAvailability: true` Postgres and MySQL run three instances. When Longhorn is installed they use the single-replica storage class `geass-single`, because the instances replicate the data themselves. Redis runs as one instance: a request for `highAvailability: true`, or for more than one instance, is refused with the message `High availability requires PostgreSQL or MySQL: Redis runs as a single instance. Turn it off, or choose PostgreSQL or MySQL`. SQLite refuses with the message `SQLite does not support high availability`.

:::caution
Geass also treats `instances: 3` or more as a request for high availability when it checks readiness.

Redis never runs more than one instance, so a Redis database with `instances` above 1 is refused with the same message as one that asks for high availability. Leave `instances` at 1 for it.

A MySQL database runs 1, 3 or 5 instances, because MOCO runs an odd number of servers. `highAvailability: true` with `instances` of 4 or 7, or `instances: 2` without it, is refused with `High availability requires 3 or 5 MySQL instances, not N`. A MySQL database cannot change between one instance and high availability once it exists: it says `High availability requires a new database`, and keeps what it has.
:::

### External databases

A database with `placement: External` is never created in your cluster. Geass reads the host, port, username and password from the spec, writes the connection Secret, and reports `Ready`. It does not test the connection.

For `provider: PlanetScale` with `mode: Create`, Geass calls the PlanetScale API with the token from the connection:

1. It looks for a PlanetScale database named `spec.databaseName` (or the resource name). If there is none, it creates one of the kind that matches `spec.engine`, `mysql` for `MySQL` and `postgresql` for `Postgres`, with `spec.clusterSize` when you set one. A database that already exists with the other kind is refused with `PlanetScale database "<name>" already exists as <kind>, not <kind>`. Any other engine is refused with `PlanetScale hosts MySQL and Postgres databases, not <engine>`.
2. A new database takes minutes to provision. Until PlanetScale reports it ready, `Ready` is `False` with the message `PlanetScale database is starting`, which does not count as a failure. After 30 minutes the message becomes `PlanetScale database did not become ready within 30m0s; check the database in PlanetScale` and it counts as one.
3. Once the database is ready, Geass issues a login on the `main` branch: a branch password for MySQL, and a role that inherits the `postgres` role for Postgres. PlanetScale shows the password only once, so Geass stores the host, username and password in `<name>-connection` at once. If it cannot store them, it revokes the login again.

The port is `spec.externalPort`, or the engine's default (`3306` for MySQL, `5432` for Postgres). If you delete the connection Secret, Geass issues a new login. For Postgres the `database` key of the Secret is the database name PlanetScale gives the role.

:::caution
The dashboard's **AWS RDS** choice creates a draft with `provider`, `engine` and `connectionRef` but no `mode`, `externalHost`, `username` or password, and the settings screen has no fields for them. Deployed from the dashboard alone, such a database reports `external database host, username, and password are required`. The **PlanetScale** choice is greyed out ("Coming soon"). Create these databases with `kubectl` or the REST API. `provider: AWS` is connect-only: no code creates an RDS instance.
:::

## Drafts and the Deploy flow

Resources the dashboard creates start as drafts:

- The dashboard sets `spec.deploy.enabled: false` and adds the annotation `geass.dev/deploy-workflow: "true"`, a pending-change counter (`geass.dev/pending-updates`) and the kind list `geass.dev/pending-changes`.
- While `spec.deploy.enabled` is `false`, the controller removes any resources it created for the database and reports `Ready` as `False` with the message `Resource is configured and waiting for deployment`.
- **Deploy changes** in the top bar sets `spec.deploy.enabled: true` on every pending resource of the environment, clears the pending markers and stores a copy of the spec in the `geass.dev/last-deployed` annotation.
- After the first deploy, edits you save in **Settings** are held back. The controller keeps running the last deployed copy while `geass.dev/pending-updates` is above zero. The one exception is `spec.backup`, which takes effect as soon as you save it.

The gate only applies when the annotation `geass.dev/deploy-workflow` is `"true"`. A `GeassDatabase` without it provisions as soon as it is valid, whatever `spec.deploy.enabled` says. Set `spec.deploy.enabled: true` anyway: the dashboard uses it to decide whether volumes exist, whether storage can only grow, and whether the node choice is locked.

## Status

| Field | Type | Meaning |
| --- | --- | --- |
| `status.targetNamespace` | string | The namespace the database runs in. |
| `status.connectionSecret` | string | Name of the Secret with the connection details, `<name>-connection`. |
| `status.host` | string | Host of the database. Shown as **Endpoint** in the dashboard. |
| `status.restoredAt` | date-time | When a snapshot restore (MySQL or SQLite) finished. It stops the snapshot from being loaded a second time. |
| `status.backup` | object | Backup summary. Absent while backups are off. |
| `status.backup.state` | string | One of `NeedsStorage`, `Starting`, `Protected`, `AtRisk`. |
| `status.backup.message` | string | Plain-language explanation of the state. |
| `status.backup.archive` | string | Name of the [GeassBackupArchive](/docs/reference/resources/geass-backup-archive/) that holds this database's backups. |
| `status.backup.lastBackupTime` | date-time | When the latest backup finished. |
| `status.backup.recoveryWindowStart` | date-time | The earliest moment you can restore to. |
| `status.reconcileRetry` | object | Counters for consecutive failures: `reconcileFailures`, `reconcileFailureStarted`, `reconcileFailureGeneration`. |
| `status.provision` | object | Deploy history. `activeProvisionAttempt` is the ID of the running attempt. `provisionAttempts` keeps the newest 8, each with `id`, `startedAt`, `completedAt`, `phase` (`Running`, `Succeeded` or `Failed`), `summary` and up to 12 `errors` (`recordedAt`, `message`). `buildRef` and `deploymentRef` are only used by services. |
| `status.conditions` | list | One condition: `Ready`. |

### Ready condition and retries

`Ready` is the only condition. It has three reasons:

| Reason | Status | Meaning |
| --- | --- | --- |
| `DatabaseReady` | `True` | The database is running and the connection Secret exists. The message names the engine, for example `Postgres database is ready`. For an external database it is `PlanetScale database is ready` or `AWS database is ready`. |
| `ReconcileError` | `False` | Not ready yet. The message says what Geass is waiting for or what failed. Geass checks again after 30 seconds. |
| `ProvisionFailed` | `False` | Geass gave up. The message is always `Provisioning failed after repeated errors. Update settings or platform configuration, then save or redeploy to try again.` |

Messages that describe waiting do not count as failures. Everything else does. Geass moves to `ProvisionFailed` when it sees 5 counted failures in a row, when counted failures last 3 minutes, or at once for a permanent error (an unsupported engine, an access-denied error, or an invalid credential or configuration). The exception is `Could not pull the database image …`: the kubelet waits minutes between pulls, so it moves to `ProvisionFailed` only when the image has failed to pull for 10 minutes, however many passes that takes.

Once the reason is `ProvisionFailed`, the controller stops reconciling that database until you change its spec (any edit that raises `metadata.generation`) or press **Retry**. **Retry** resets the counters, sets `Ready` back to `ReconcileError` with the message `Retrying provisioning`, and starts a new entry in `status.provision`.

:::caution
When `ProvisionFailed` is set, the original error message is gone from the condition. `status.provision.provisionAttempts[].errors` keeps a shortened, sanitized form of it. To see the real cause, watch the `Ready` message while Geass is still retrying, or inspect the objects Geass created. See [Troubleshooting](#troubleshooting).
:::

The `Ready` messages you can see, in the order Geass checks them:

| Message | Meaning |
| --- | --- |
| `Resource is configured and waiting for deployment` | A draft. Deploy it. |
| `resource has pending changes and no last deployed snapshot` | The database has saved edits but no `geass.dev/last-deployed` copy. Deploy again from the dashboard. |
| `project "<project>" does not exist`, `project "<project>" is not ready`, `project "<project>" has no <environment> environment` | The project or environment is missing or not ready. |
| `project "<project>" cluster "<cluster>" is not ready` | The project's [GeassCluster](/docs/reference/resources/geass-cluster/) is not `Ready`. |
| `cloud connection "<name>" was not found`, `... is scoped to project "<project>"`, `... must be scoped to a project` | `spec.connectionRef` is wrong. |
| `High availability requires a cluster with at least three healthy nodes` | See [Troubleshooting](#troubleshooting). |
| `Storage is starting`, `Storage is unavailable` | Waiting for the Longhorn storage class. See [Troubleshooting](#troubleshooting). |
| `Installing Postgres operator chart`, `Installing MySQL database chart`, `Installing Redis database chart` | Helm is installing a chart. |
| `PlanetScale database is starting` | PlanetScale is still creating the database. Waiting for it does not count as a failure, for up to 30 minutes. |
| `Postgres database is starting`, `Service is starting`, `Starting service` | Pods are starting. |
| `Could not pull the database image <image> (<reason>)` | The pod of a MySQL or Redis server cannot get its image. `<reason>` is `ErrImagePull`, `ImagePullBackOff` or `InvalidImageName`. A pull that is only slow is `Service is starting`. It counts as a failure, but only after 10 minutes. |
| `Service failed to start` | Every pod of the chart failed. |
| `Restoring database from backup` | A restore is running. |

## What the dashboard writes

| Where | What it does to the `GeassDatabase` |
| --- | --- |
| **+ Add resource** > **Database** > **PostgreSQL**, **MySQL**, **Redis** or **SQLite** | Creates a draft with a generated name such as `falling-mist`. It sets `project`, `environment`, `engine`, `placement: InCluster`, `resources` (250m and 512Mi, or 100m and 128Mi for SQLite), `storageSize` (`10Gi`, or `1Gi` for SQLite, none for Redis) and, for Postgres, MySQL and SQLite, `backup: {enabled: true, retentionDays: 7}`. A `runOn` choice is saved only by a dashboard admin. |
| **+ Add resource** > **Database** > **PlanetScale** or **AWS RDS** | Creates a draft with `placement: External`, `provider`, `connectionRef` and an `engine` (`MySQL` for PlanetScale, `Postgres` for AWS RDS). See the caution under [External databases](#external-databases). |
| **+ Add resource** > **Database** > **Logical database**, or the entry for a shared server (shown as `<name> (shared)`) | Creates a [GeassLogicalDatabase](/docs/reference/resources/geass-logical-database/), not a `GeassDatabase`. |
| Resource panel, **Settings** tab, **Database** section | **Version** writes `spec.version`. **Resources** writes `spec.resources` (the sizes are 0.1, 0.25, 0.5, 1 or 2 CPU and 128 MB to 4 GB). **Size** under **Storage** writes `spec.storageSize`. These wait for a deploy. |
| **Settings** tab, **Scale** section, **Run on** | Writes `spec.runOn.tags`. It is only editable before the first deploy. After that the dashboard refuses with `A database keeps its data on the nodes it was first deployed to, so which nodes it runs on cannot change after that`. |
| **Settings** tab, **Backups** section (also the **Backups** tab) | **Back up this database** and **Keep backups for** write `spec.backup`. They apply at once. |
| **Deploy changes** in the top bar | Runs the deploy flow for every pending resource of the environment. It first checks that the cluster has room for the database (or three instances). The API deploys one database with `POST /api/databases/<name>/deploy`. |
| **Retry** | Resets a `ProvisionFailed` database. |
| **Console** tab, **Run query** | Runs your SQL in the database pod with `psql -c`, `mysql -e`, `redis-cli` or `rqlite -e`. It is not a field of the resource. |
| **Restore** buttons on the **Backups** tab | Creates a new database with `spec.restoreFrom`. It is deployed at once, without a draft. |
| **Settings** > **Databases** > **Set up a server** | Creates a shared server: no project, `spec.deploy.enabled: true`, `highAvailability` from the **High availability** switch (which also sets `instances: 3`), and `serverRegion` from **Region**. |
| **Settings** tab, **Danger** section | Deletes the database after you type its resource ID. |

The storage rules the dashboard enforces: at least 1Gi, only growth after the first deploy, and no change at all when the storage class cannot expand volumes. The messages are `storage can be increased but not decreased once deployed (currently <size>)` and `this cluster's storage can't be resized, so a deployed database stays at <size>`.

A viewer in the dashboard sees a redacted copy of each database: the engine, host, user, password reference and connection details are cleared. A token that cannot change things gets the same copy from the API.

## From the command line and the API

| To | Command | MCP tool |
| --- | --- | --- |
| List databases | `geass databases list` | `list_databases` |
| Read `resource`, `spec` and `status` | `geass databases get NAME` | `get_database` |
| Read `host`, `port`, `database` and `username` of the connection Secret | `geass databases connection NAME` | `database_connection` |
| Also read `password` and `uri` | `geass databases connection NAME --reveal` | None: the tool never returns them |

They call `GET /api/v1/databases`, `GET /api/v1/databases/{name}` and `GET /api/v1/databases/{name}/connection`. The `state` they report is `deleting` once `metadata.deletionTimestamp` is set, then `draft` when `spec.deploy.enabled` is `false`, then `ready` when `Ready` is `True`, `failed` when its reason is `ProvisionFailed`, and `pending` otherwise. The connection call needs Full access, works once `status.targetNamespace` and `status.connectionSecret` are set, and a reveal is written to the operator log. See [Databases](/docs/guides/databases/#command-line-and-ai-agents).

## Connection Secret

Once the database is ready, Geass writes a Secret named `<name>-connection` in the database's namespace. It has these keys: `host`, `port`, `database`, `username`, `password` and `uri`.

| Engine | `uri` |
| --- | --- |
| Postgres | `postgresql://<user>:<password>@<host>:5432/<database>` |
| MySQL | `mysql://<user>:<password>@<host>:3306/<database>` |
| Redis | `redis://:<password>@<host>:6379` |
| SQLite | `http://<host>:4001` |

The login is `app`. Geass first writes the credentials to a Secret named `<name>-bootstrap` and the connection Secret copies them.

:::caution
Geass derives the generated password from the resource name: it is `<name>-password`. For MySQL the root password is the same value. The password is not random, so do not count on it being secret. Rely on [network isolation](/docs/guides/network-isolation/) and keep the database off the public internet.
:::

## Backups

`spec.backup` protects in-cluster databases only. The dashboard refuses to change it for Redis or external databases with `Backups are not available for this kind of database`.

- Postgres is archived continuously (CloudNativePG, Barman object store) and a base backup runs daily at about 02:00 UTC. You can restore to any moment in the recovery window.
- MySQL and SQLite are copied once a day by a CronJob named `<name>-backup` at about 02:00 UTC. The copies are compressed SQL files, and a restore returns the data as of one of them.
- The backups go to the project's backup storage: an S3 bucket in the AWS account of a [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/). Geass creates the bucket (`geass-backups-<random>`) and an IAM user scoped to it when the project's `spec.backupStorage` is set. Until then `status.backup.state` is `NeedsStorage`. A shared server has no project, so it has nowhere to put backups.
- Geass records each database's backups in a [GeassBackupArchive](/docs/reference/resources/geass-backup-archive/) in `geass-system`.
- Backups are an add-on. If Geass cannot work out the backup plan, the database keeps running and Geass tries again after a minute.
- Deleting a database takes one last backup (waiting up to 10 minutes), then keeps the archive for 7 days.
- The Jobs that dump, restore and take the final backup of a MySQL or SQLite database run in the database's region, on nodes that carry the project's required tags when the database was placed with them. They do not use `spec.runOn.tags`.

`status.backup.state` values:

| State | Meaning |
| --- | --- |
| `NeedsStorage` | Backups are on but the project has nowhere to put them, or the storage failed. The message is `Choose where backups are stored to protect this database` or `Backup storage is not working: <AWS error>`. |
| `Starting` | Storage works and the first backup has not finished. The message is `Taking the first backup`. |
| `Protected` | Backups are running. The message is `Backups are running`. |
| `AtRisk` | The message is `Recent changes are not being saved to backup storage` or `The latest backup failed`. |

### Restoring

A restore always creates a new database next to the original. The original is not touched. Geass refuses a restore that cannot succeed, with these messages:

| Message | Cause |
| --- | --- |
| `These backups are no longer available` | The archive does not exist any more. |
| `Backups from another project cannot be restored here` | The archive belongs to a different project. |
| `A restored database must use the same engine as the original (<engine>)` | Wrong `spec.engine`. |
| `A restored database must use Postgres version <n>, the version of the backup` | Wrong `spec.version`. |
| `A restored database must keep the database name <name>` | Wrong `spec.databaseName`. |
| `A restored database needs at least <size> of storage, the size of the original` | `spec.storageSize` is too small. |
| `These backups have no restore points yet` | The first backup has not finished. |
| `The chosen restore point is not available` | `restorePoint` is not on the archive. |
| `The chosen time is earlier than the oldest restore point`, `The chosen time is later than the newest recoverable moment` | `time` is outside the recovery window. |
| `This database can only be restored to a restore point` | `time` on a MySQL or SQLite archive. |
| `Choose either a restore point or a time, not both` | Both fields are set. |
| `The backup storage is not available: <reason>` | Geass could not reach the bucket. |
| `The restore could not be completed. Check that the backups are still available, then restore again` | The restore Job failed. |

A restore only runs while the database is first provisioned. Postgres recovers while the cluster is created. For MySQL and SQLite a Job named `<name>-restore` loads the snapshot, then `status.restoredAt` is set so it never loads twice.

## Editing by hand

You can create a `GeassDatabase` with `kubectl` and you can edit one. The dashboard shows it like any other. Keep these in mind.

- Geass writes the annotations `geass.dev/storage-class` and `geass.dev/node-placement` once, when it first provisions the database. They pin the volume's storage class and the nodes it runs on. Do not remove or edit them. `geass.dev/node-placement` is JSON with the `region`, the `tags` from `spec.runOn.tags` and the `required` tags of the project at that moment, for example `{"region":"eu","tags":["storage"],"required":["hipaa"]}`. An empty `{}` means the database runs anywhere. See [Where a database runs](/docs/guides/databases/#where-a-database-runs).
- The dashboard writes `geass.dev/display-name`, `geass.dev/deploy-workflow`, `geass.dev/pending-updates`, `geass.dev/pending-changes` and `geass.dev/last-deployed`.
- The controller does not rewrite the spec of a `GeassDatabase`. Status is the controller's.
- `spec.restoreFrom`, `spec.runOn` and the storage class are read once. Changing them later has no effect.
- A volume's size can grow but not shrink.

:::danger
Do not change `spec.project` or `spec.environment` on a deployed database. Geass sees that the target namespace moved, deletes the database's resources in the old namespace (including its volume) and retires its backup archive, then creates a new empty database in the new one. Do not change `spec.engine` either: Geass starts the new engine and leaves the old resources behind.
:::

Setting `spec.deploy.enabled` back to `false` on a resource that carries the `geass.dev/deploy-workflow` annotation makes Geass treat it as a draft again. It deletes the resources it built for it. For Postgres and SQLite the data volume goes with them.

To delete a database, delete the `GeassDatabase`. The finalizer first takes a final backup if backups are on, then removes the resources below. The dashboard refuses to delete a shared server while logical databases still use it. `kubectl` does not stop you.

## What happens under the hood

Geass keeps the resource in `geass-system`. The workloads live in `<project>-<environment>`. Because they are in a different namespace, Geass cannot use owner references, so it deletes them itself when you delete the resource. Objects Geass creates carry these labels: `geass.dev/managed-by=geass`, `geass.dev/kind=GeassDatabase`, `geass.dev/name=<name>`, `geass.dev/system-namespace=geass-system`, `geass.dev/project` and `geass.dev/environment`.

Common to every in-cluster engine:

- Secret `<name>-bootstrap` and Secret `<name>-connection` in the database's namespace.
- Optionally, Secrets `<name>-backup-credentials` and `<name>-restore-credentials`, which hold credentials scoped to the backup bucket.
- Annotations `geass.dev/storage-class` and `geass.dev/node-placement` on the `GeassDatabase`.

Per engine:

- **Postgres**: a HelmChart `geass-cloudnative-pg` in `kube-system` installs the CloudNativePG operator into `cnpg-system`. Then a CNPG `Cluster` `<name>` runs in the database's namespace, with pods `<name>-1`, `<name>-2`, a volume per pod and the Services `<name>-rw`, `<name>-ro` and `<name>-r`. With backups there is a `ScheduledBackup` `<name>`. On delete, a `Backup` named `<name>-final`. A Postgres database that hosts logical databases also gets a `<name>-superuser` Secret.
- **MySQL**: a StatefulSet `geass-mysql-<name>` of one pod, the Services `geass-mysql-<name>` and `geass-mysql-<name>-headless`, and the volume claim `data-geass-mysql-<name>-0`. The pod's logins come from the `<name>-bootstrap` Secret. A MySQL database made on an earlier development build of Geass is still a HelmChart `geass-mysql-<name>` in `kube-system` (the Bitnami chart, whose images Bitnami has removed from Docker Hub). Geass leaves it exactly as it is.
- **MySQL with high availability**: a HelmChart `geass-moco` in `kube-system` installs MOCO into `moco-system`, with two controller pods, after cert-manager is running. Then a `MySQLCluster` `<name>` runs in the database's namespace: the StatefulSet `moco-<name>`, its pods `moco-<name>-0` and on, a claim `mysql-data-moco-<name>-<index>` for each, and the Services `moco-<name>-primary` and `moco-<name>-replica`. A Job `<name>-login-<id>` makes the application's login and database as MOCO's writing login, `moco-writable`, whose password is in MOCO's own Secret `moco-<name>`. Each server is given at least 1 GiB of memory.
- **Redis**: a StatefulSet `geass-redis-db-<name>-master` of one pod and the Services `geass-redis-db-<name>-master` and `geass-redis-db-<name>-headless`, with no volume. A Redis database made on an earlier development build, as the Bitnami chart `geass-redis-db-<name>`, is moved to this server the next time it is reconciled: the chart is removed and the server is made in its place, with the message `Replacing the Redis chart with Geass's own server`. Redis keeps no data on disk, so the move costs what a restart of its pod costs.
- **SQLite**: a Deployment `<name>`, a Service `<name>` on port 4001 and a PVC `<name>-sqlite`.
- **External**: only the connection Secret.

Inspect them:

```bash title="Inspect a database"
# The resource and its status
kubectl -n geass-system get geassdatabases
kubectl -n geass-system get geassdatabase orders-db -o yaml

# Where it runs
NS=$(kubectl -n geass-system get geassdatabase orders-db -o jsonpath='{.status.targetNamespace}')

# Everything Geass labelled for it
kubectl -n "$NS" get all,pvc,secrets -l geass.dev/name=orders-db

# Postgres
kubectl -n "$NS" get clusters.postgresql.cnpg.io orders-db
kubectl -n "$NS" get pods -l cnpg.io/cluster=orders-db
kubectl -n cnpg-system get pods

# MySQL or Redis
kubectl -n "$NS" get statefulsets,pods,pvc -l app.kubernetes.io/instance=geass-mysql-orders-db

# Backups
kubectl -n "$NS" get scheduledbackups,backups
kubectl -n geass-system get geassbackuparchives

# Read the connection details
kubectl -n "$NS" get secret orders-db-connection -o jsonpath='{.data.uri}' | base64 -d
```

## Troubleshooting

### The database stays in Draft or says it is waiting for deployment

The message is `Resource is configured and waiting for deployment`. The resource carries the `geass.dev/deploy-workflow: "true"` annotation and `spec.deploy.enabled` is `false`. Press **Deploy changes** in the top bar, or set `spec.deploy.enabled: true`. Saved edits wait for a deploy too: the dashboard shows the pending count in the top bar.

### `High availability requires a cluster with at least three healthy nodes`

Geass looks for a [GeassHAReadiness](/docs/reference/resources/geass-ha-readiness/) in `geass-system` whose `Ready` condition is `True` and whose `status.healthyNodes` is at least 3. If none exists, the database waits. Open **Settings** > **Usage** > **HA readiness** and press **Run readiness check**, which creates the report. Nothing else creates it. Add nodes if the cluster has fewer than three healthy ones. Once the report exists the operator works it out again when a node, a storage class or the Geass cluster changes, and a waiting database checks again every 30 seconds.

### `Storage is starting`, then `Storage is unavailable`

New volumes wait for the Longhorn storage classes `geass-replicated` or `geass-single` (or the regional `geass-replicated-<region>`, or `geass-tagged-<hash>` in a project that requires node tags). If Longhorn is enabled and still installing, the database waits up to 15 minutes, then reports `Storage is unavailable` and counts it as a failure. Check the [GeassCluster](/docs/reference/resources/geass-cluster/) `AddonsReady` condition and the pods in `longhorn-system`. Once Geass picks a class it pins it, so installing Longhorn later does not change an existing volume.

One wait has no limit. When Longhorn is serving the global class but the regional or tagged class a database needs has not been made, the message stays `Storage is starting`. The cluster controller makes the class once at least one schedulable node is in its scope and every such node has a ready Longhorn manager. See [Storage](/docs/guides/storage/#troubleshooting).

### Capacity messages

`This <engine> database needs about <cpu> CPU and <memory> memory. The cluster currently has ... available` means no node has room. Add nodes or lower `spec.resources`. For `There is no ready node in <place> to run this ...` and `... reserved for other work`, see [Scaling and placement](/docs/guides/scaling-and-placement/) and [GeassNode](/docs/reference/resources/geass-node/). `<place>` names the region and every tag the database needs, the project's required tags included. Geass only checks capacity when it first creates the database.

### `project "<project>" is not ready` or `has no <environment> environment`

The database waits for its project and the project's cluster to be `Ready`, and for the environment to be listed in the project's `spec.environments`.

### The reason is `ProvisionFailed` and you cannot see why

Press **Retry** and watch the `Ready` message:

```bash title="Watch the Ready message"
kubectl -n geass-system get geassdatabase orders-db -w \
  -o custom-columns=NAME:.metadata.name,READY:.status.conditions[0].status,MESSAGE:.status.conditions[0].message
```

Geass keeps trying for up to 5 failures or 3 minutes, and the message names the real problem. Also read the events and status of the objects in [What happens under the hood](#what-happens-under-the-hood).

### A PlanetScale or external database is not ready

- `external database host, username, and password are required`: set `externalHost`, `username` and `passwordSecretRef`, or use `mode: Create` with a PlanetScale connection.
- `external password secret "<name>" is unavailable`: the Secret is missing from `geass-system`.
- `a PlanetScale connection is required`: `spec.connectionRef` is empty.
- `connection "<name>" is not a PlanetScale connection`: the connection's provider is `AWS`.
- `PlanetScale token and organization are required`: the connection's Secret lacks `token`, or neither the connection's `spec.organization` nor the Secret's `organization` key is set.
- `PlanetScale API <status>: ...`: PlanetScale refused the call. Check the token's permissions, the organization name and, if you set `spec.clusterSize`, that PlanetScale offers that size for the engine.
- `PlanetScale hosts MySQL and Postgres databases, not <engine>`: `spec.engine` is `Redis` or `SQLite`. Use `MySQL` or `Postgres`.
- `PlanetScale database "<name>" already exists as <kind>, not <kind>`: a database with that name exists in the organization with the other engine. Choose another `spec.databaseName`.
- `PlanetScale database is starting` for a long time, then `PlanetScale database did not become ready within 30m0s`: PlanetScale did not finish creating the database. Check it in PlanetScale, then press **Retry**.

### Backups are `NeedsStorage` or `AtRisk`

`NeedsStorage` means the project has no backup storage or Geass cannot reach it. Choose an AWS connection under **Choose where backups are stored**, then check the AWS error in `status.backup.message`. `AtRisk` means the last backup failed or the change log is not reaching the bucket. Check the CloudNativePG `Cluster` conditions or the `<name>-backup` Jobs in the database's namespace.

### A restore does not start

Read the message against the table in [Restoring](#restoring). Postgres reports `Restoring database from backup` while the new cluster recovers. A recovery that cannot finish shows as a database that stays in that state, so check the pods `<name>-1` and their logs.

### Deleting a database takes a while

Geass takes a final backup first and waits up to 10 minutes for it. If the database is not running, or the backup fails, Geass skips the wait. The finalizer is released when the resources are gone.

### MySQL volumes remain after deletion

Geass deletes the HelmChart object and does not delete persistent volume claims the chart created. Check `kubectl -n <namespace> get pvc` after you delete a MySQL database.

## Related

- [Databases](/docs/guides/databases/)
- [Backups and restore](/docs/guides/backups-and-restore/)
- [Shared database servers](/docs/guides/shared-database-servers/)
- [Storage](/docs/guides/storage/)
- [GeassLogicalDatabase](/docs/reference/resources/geass-logical-database/)
- [GeassBackupArchive](/docs/reference/resources/geass-backup-archive/)
- [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/)
- [GeassHAReadiness](/docs/reference/resources/geass-ha-readiness/)
- [GeassProject](/docs/reference/resources/geass-project/)
- [Resources overview](/docs/reference/resources/overview/)
