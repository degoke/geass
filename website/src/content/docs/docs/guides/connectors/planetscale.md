---
title: "Connect PlanetScale"
description: "Connect PlanetScale to Geass to create MySQL and Postgres databases in your organization: the token, the manifest, cluster sizes and what Geass creates."
sidebar:
  label: PlanetScale
---

Use a PlanetScale connection to have Geass create a database in your PlanetScale organization and a login for it,
instead of running the database in your cluster. Geass can create MySQL and Postgres databases there.

:::caution
PlanetScale support is experimental. The dashboard cannot create PlanetScale databases yet: create them with a manifest
or the API, as described in [Using it from a database](#using-it-from-a-database).
:::

## Before you start

- A PlanetScale organization, and a service token that can create databases and logins on them. The next section lists
  the calls Geass makes.
- The ID of the project that will use the connection. It starts with `proj-`, and a connection belongs to one project.
- An admin login in Geass. Only admins can add, change or remove connections.

## Credentials and permissions

You give Geass a PlanetScale **Service token** and the **Organization** name. These are the only calls it makes:

- `GET /organizations/{org}/databases/{name}`, to see whether the database exists and whether PlanetScale has finished
  provisioning it.
- `POST /organizations/{org}/databases`, to create it. The body holds `name`, `kind` (`mysql` or `postgresql`, from
  the database's engine) and, when you set one, `cluster_size`.
- `POST /organizations/{org}/databases/{name}/branches/main/passwords` for a MySQL database, or
  `.../branches/main/roles` for a Postgres one, to create a login on the `main` branch.
- `DELETE` on that same login, only to undo a login that Geass could not store (see
  [What Geass does with the result](#what-geass-does-with-the-result)).
- `GET /organizations/{org}/cluster-size-skus?engine={kind}`, only when the dashboard API lists or checks cluster
  sizes (see [Choosing a size](#choosing-a-size)).

So the token must be allowed to create databases, to create and delete connection passwords and roles on `main`, and
to read the organization's cluster sizes. Check the access names in PlanetScale's own service token settings; Geass
does not read them.

Add the connection in **Settings** → **Connectors** → **Configure** on the **PlanetScale** card. It asks for **Name**,
**Project** (as for [AWS](/docs/guides/connectors/aws/#add-an-aws-connection)), **Organization** and **Service token**. The API takes the same
fields (`provider=PlanetScale`, `organization`, `token`). The Secret `<name>-credentials` holds `token` and
`organization`.

## Using it from a database

The dashboard cannot create PlanetScale databases yet. In the **Database** menu the **PlanetScale** row is always
greyed out and reads "Coming soon", whatever connections the project has, and the **PlanetScale** card in
**Settings** → **Connectors** says "PlanetScale databases are coming soon." Create them with a manifest or the API
instead, with `mode` set to `Create` and a connection that is ready for the project. The engine can be MySQL or
Postgres, and Geass picks the port from it (3306 or 5432).

```yaml title="planetscale-database.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassDatabase
metadata:
  name: orders-db
  namespace: geass-system
spec:
  project: proj-1a2b3c4d
  environment: production
  engine: Postgres
  placement: External
  provider: PlanetScale
  mode: Create
  connectionRef:
    name: prod-planetscale
  deploy:
    enabled: true
```

With the API, send `POST /api/databases/create` with `mode=Create`, `provider=PlanetScale`, `placement=External`,
`engine` (`MySQL` or `Postgres`), `connectionRef`, `project`, `environment` and `name`, then deploy it with
`POST /api/databases/{name}/deploy`. See the [REST API](/docs/reference/rest-api/). The `engine` must be `MySQL` or
`Postgres`: `Redis` and `SQLite` fail with "PlanetScale hosts MySQL and Postgres databases", and the API reads a missing
or unrecognized `engine` as Postgres.

What Geass creates on PlanetScale:

- A database named after the Geass database (or `spec.databaseName`), of the kind that matches the engine. If a database
  with that name already exists in the organization, Geass uses it, provided it is the same kind. Otherwise it reports
  `PlanetScale database "orders-db" already exists as mysql, not postgresql`.
- A login on the `main` branch: a branch password for MySQL, a role for Postgres. Geass creates the Postgres role with
  the `postgres` role inherited, so that your application can run migrations.

A new database takes minutes to provision, and PlanetScale will not issue a login until it is ready. Until then the
database reports "PlanetScale database is starting", which does not count as a failure. If PlanetScale has not reported
it ready after 30 minutes, the message becomes "PlanetScale database did not become ready within 30m0s; check the
database in PlanetScale", which counts towards giving up.

## What Geass does with the result

- It writes the Secret `<database>-connection` in the environment's namespace with the host, port, username,
  password, database name and a connection URI. Your apps read that Secret.
- The host is only the host name from PlanetScale's reply. The port is `spec.externalPort` if you set it, and
  otherwise the engine's default: 3306 for MySQL and 5432 for Postgres.
- The database name is the database resource's name (or `spec.databaseName`). For a Postgres database, Geass uses the
  database name PlanetScale reports for the role when there is one.
- PlanetScale shows a password only once, so Geass keeps it in the Secret and reuses that Secret on later reconciles
  instead of creating a second login. If Geass cannot store the Secret, it deletes the login it made in that pass and issues a
  new one on its next pass.

## Choosing a size

`spec.clusterSize` (the `clusterSize` field of `POST /api/databases/create` and `.../update`) is the PlanetScale cluster
size name, for example `PS_10`. Leave it empty to let PlanetScale choose. Geass sends it only when it creates the
database. After the connection Secret exists the dashboard API refuses to change it ("A PlanetScale database is sized
when it is created"), and editing the field by hand does not resize the database. A cluster size on a database that is
not on PlanetScale is refused.

List the sizes your organization can use, with their monthly prices when the token may read them:

```bash title="List PlanetScale cluster sizes"
curl -fsS -H "Authorization: Bearer $GEASS_TOKEN" \
  "https://geass.example.com/api/planetscale/cluster-sizes?project=proj-1a2b3c4d&connection=prod-planetscale&engine=postgres" \
  | jq '.items[] | {name, cpu, ramBytes, monthlyUsd}'
```

The call is `GET /api/planetscale/cluster-sizes` with the query fields `project`, `connection` (a PlanetScale connection
of that project) and `engine` (`mysql` or `postgres`). It needs an admin session or an admin or observer token. The reply
is `{"items":[...]}`, each item with `name`, `displayName`, `cpu`, `ramBytes`, `storageBytes`, `development`,
`production`, `metal`, `provider`, `architecture`, `monthlyUsd` and `replicaMonthlyUsd` (whole US dollars per month in
the organization's default region; left out when PlanetScale gives no price). Geass keeps each list for 5 minutes. When
you set a size through the API, Geass checks it against this list and refuses a size PlanetScale does not offer for the
engine; if the list cannot be loaded, it accepts the size and PlanetScale checks it when the database is created.

## Rotate and remove

Rotate the token by editing the Secret `<name>-credentials`, the same way as [for AWS](/docs/guides/connectors/aws/#rotate-aws-credentials). Databases that already
exist keep working because they hold their own login. There is no delete for a PlanetScale connection in the
dashboard, so remove the `GeassCloudConnection` and its Secret with `kubectl`.

Deleting the Geass database deletes the connection Secret. Geass has no delete call for PlanetScale databases, so the
database and the login it created stay in your PlanetScale organization until you remove them there.

## What happens under the hood

- A PlanetScale connection is a `GeassCloudConnection` in `geass-system`, with its credentials in the Secret
  `<name>-credentials`. See [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/).
- The connection controller sets `Ready` to `True` with reason `CredentialsReady`, or `False` with `CredentialsMissing` or
  `UnsupportedProvider`.
- The database controller reads the connection and its Secret when it reconciles. It also checks that the connection
  belongs to the database's project.

```bash title="Inspect PlanetScale connections"
kubectl -n geass-system get geasscloudconnections
kubectl -n geass-system get secret prod-planetscale-credentials
```

## Troubleshooting

**The connection stays Pending.** The Secret is missing `token` or `organization`. The message reads "PlanetScale token and
organization are required".

**The PlanetScale row is greyed out in the Database menu.** It always reads "Coming soon", whatever connections the
project has. Create PlanetScale databases with a manifest or the API.

**A PlanetScale database never becomes ready.** Without `mode: Create`, or a host, username and password, Geass has nothing to
connect to; see [Using it from a database](#using-it-from-a-database). If it was created with `mode: Create`, read the database's message
for the PlanetScale API error. After repeated errors Geass sets `Ready` to `False` with reason `ProvisionFailed` and stops
until you change the spec or redeploy.

**A PlanetScale database says "PlanetScale database is starting".** PlanetScale is still provisioning it, which takes minutes,
and Geass checks again on its own. After 30 minutes the message becomes "PlanetScale database did not become ready", and you
should look at the database in PlanetScale.

**"PlanetScale database ... already exists as mysql, not postgresql".** A database with that name exists in your
organization with the other engine. Rename the Geass database, set `spec.databaseName`, or change the `engine`.

**"PlanetScale hosts MySQL and Postgres databases".** The database's `engine` is Redis or SQLite. PlanetScale takes MySQL
or Postgres only.

**"PlanetScale does not offer cluster size ... for ... databases".** The `clusterSize` is not in the list that
`GET /api/planetscale/cluster-sizes` returns for that engine. Pick a name from that list.

Errors that apply to any connection saved for a project ("project was not found", "connection already exists", "scoped to project") are
listed in [Cloud connectors](/docs/guides/connectors/#troubleshooting).

## Related

- [Cloud connectors](/docs/guides/connectors/)
- [Databases](/docs/guides/databases/)
- [GeassDatabase](/docs/reference/resources/geass-database/)
- [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/)
- [REST API](/docs/reference/rest-api/)
