---
title: "Connect PlanetScale"
description: "Connect PlanetScale to Geass to create MySQL and Postgres databases in your organization: the token, the manifest, cluster sizes, and what Geass creates."
sidebar:
  label: PlanetScale
---

With a PlanetScale connection, Geass creates a database in your PlanetScale organization, and a login for it. The
database does not run in your cluster. Geass can create MySQL and Postgres databases in PlanetScale.

:::caution
Create PlanetScale databases with a manifest or the API, as
[Using it from a database](#using-it-from-a-database) shows. The dashboard cannot create PlanetScale databases yet.
PlanetScale support is experimental.
:::

## Before you start

- A PlanetScale organization, and a service token that can create databases and logins on them. The next section lists
  the calls that Geass makes.
- The ID of the project that uses the connection. The ID starts with `proj-`. A connection belongs to one project.
- An admin login in Geass. Only admins can add, change, or remove connections.

## Credentials and permissions

You give Geass a PlanetScale **Service token** and the **Organization** name. Geass makes only these calls:

- `GET /organizations/{org}/databases/{name}`, to see whether the database exists, and whether PlanetScale has
  finished provisioning it.
- `POST /organizations/{org}/databases`, to create the database. The body holds `name`, `kind`, and `cluster_size`
  when you set one. The `kind` is `mysql` or `postgresql`, from the engine of the database.
- `POST /organizations/{org}/databases/{name}/branches/main/passwords` for a MySQL database, or
  `.../branches/main/roles` for a Postgres database, to create a login on the `main` branch.
- `DELETE` on that same login, only to undo a login that Geass could not store (see
  [What Geass does with the result](#what-geass-does-with-the-result)).
- `GET /organizations/{org}/cluster-size-skus?engine={kind}`, only when the dashboard API lists or checks cluster
  sizes (see [Choosing a size](#choosing-a-size)).

Thus, the token must have permission to do these tasks:

- Create databases.
- Create and delete connection passwords and roles on `main`.
- Read the cluster sizes of the organization.

Find the names of these permissions in the service token settings of PlanetScale. Geass does not read them.

To add the connection, open **Settings** → **Connectors**, and click **Configure** on the **PlanetScale** card. The
form asks for **Name**, **Project** (as for [AWS](/docs/guides/connectors/aws/#add-an-aws-connection)),
**Organization**, and **Service token**. The API takes the same fields (`provider=PlanetScale`, `organization`,
`token`). The Secret `<name>-credentials` holds `token` and `organization`.

## Using it from a database

The dashboard cannot create PlanetScale databases yet. In the **Database** menu, the **PlanetScale** row is always
grayed out and reads "Coming soon", for all connections that the project has. The **PlanetScale** card in
**Settings** → **Connectors** says "PlanetScale databases are coming soon."

Instead, create PlanetScale databases with a manifest or the API. Set `mode` to `Create`, and use a connection that
is ready for the project. The engine can be MySQL or Postgres. Geass selects the port from the engine (3306 or 5432).

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

To use the API, do these steps:

1. Send `POST /api/databases/create` with these fields: `mode=Create`, `provider=PlanetScale`, `placement=External`,
   `engine` (`MySQL` or `Postgres`), `connectionRef`, `project`, `environment`, and `name`.
2. Deploy the database with `POST /api/databases/{name}/deploy`.

See the [REST API](/docs/reference/rest-api/). The `engine` must be `MySQL` or `Postgres`. `Redis` and `SQLite` fail
with "PlanetScale hosts MySQL and Postgres databases". The API reads a missing or unrecognized `engine` as Postgres.

Geass creates these items on PlanetScale:

- A database with the name of the Geass database (or `spec.databaseName`), of the kind that matches the engine. If a
  database with that name already exists in the organization, Geass uses it, but only if it is the same kind. If it is
  a different kind, Geass reports `PlanetScale database "orders-db" already exists as mysql, not postgresql`.
- A login on the `main` branch: a branch password for MySQL, and a role for Postgres. Geass creates the Postgres role
  with the `postgres` role inherited, so that your application can run migrations.

PlanetScale takes minutes to provision a new database. PlanetScale does not issue a login until the database is ready.
Until then, the database reports "PlanetScale database is starting". This message does not count as a failure. If
PlanetScale does not report the database ready after 30 minutes, the message becomes "PlanetScale database did not
become ready within 30m0s; check the database in PlanetScale". This second message counts as a failure toward the
limit at which Geass stops.

## What Geass does with the result

- Geass writes the Secret `<database>-connection` in the namespace of the environment. The Secret has the host, port,
  username, password, database name, and a connection URI. Your apps read that Secret.
- The host is only the host name from the reply of PlanetScale. The port is `spec.externalPort` if you set it.
  Otherwise, the port is the default of the engine: 3306 for MySQL and 5432 for Postgres.
- The database name is the name of the database resource (or `spec.databaseName`). For a Postgres database, if
  PlanetScale reports a database name for the role, Geass uses that name.
- PlanetScale shows a password only one time. Thus, Geass keeps the password in the Secret, and uses that Secret
  again on later reconciles. Geass does not create a second login. If Geass cannot store the Secret, it deletes the
  login that it made in that pass. Then it issues a new login on its next pass.

## Choosing a size

`spec.clusterSize` is the PlanetScale cluster size name, for example `PS_10`. It is also the `clusterSize` field of
`POST /api/databases/create` and `.../update`. To let PlanetScale select the size, leave the field empty.

- Geass sends the size only when it creates the database.
- After the connection Secret exists, the dashboard API refuses to change the size ("A PlanetScale database is sized
  when it is created").
- If you edit the field by hand, Geass does not resize the database.
- Geass refuses a cluster size on a database that is not on PlanetScale.

To list the sizes that your organization can use, run this command. If the token has permission to read prices, the
list includes the monthly prices.

```bash title="List PlanetScale cluster sizes"
curl -fsS -H "Authorization: Bearer $GEASS_TOKEN" \
  "https://geass.example.com/api/planetscale/cluster-sizes?project=proj-1a2b3c4d&connection=prod-planetscale&engine=postgres" \
  | jq '.items[] | {name, cpu, ramBytes, monthlyUsd}'
```

The call is `GET /api/planetscale/cluster-sizes` with these query fields:

- `project`.
- `connection`: a PlanetScale connection of that project.
- `engine`: `mysql` or `postgres`.

The call needs an admin session, or an admin or observer token. The reply is `{"items":[...]}`. Each item has `name`,
`displayName`, `cpu`, `ramBytes`, `storageBytes`, `development`, `production`, `metal`, `provider`, `architecture`,
`monthlyUsd`, and `replicaMonthlyUsd`. The two price fields are whole US dollars each month in the default region of
the organization. If PlanetScale gives no price, the reply does not include them.

Geass keeps each list for 5 minutes. When you set a size through the API, Geass checks it against this list. Geass
refuses a size that PlanetScale does not offer for the engine. If Geass cannot load the list, Geass accepts the size.
Then PlanetScale checks the size when it creates the database.

## Rotate and remove

To rotate the token, edit the Secret `<name>-credentials`, the same as
[for AWS](/docs/guides/connectors/aws/#rotate-aws-credentials). Databases that already exist continue to work, because
they have their own login. The dashboard cannot delete a PlanetScale connection. To remove the connection, delete the
`GeassCloudConnection` and its Secret with `kubectl`.

When you delete the Geass database, Geass deletes the connection Secret. Geass has no delete call for PlanetScale
databases. Thus, the database and the login that Geass created stay in your PlanetScale organization until you remove
them there.

## What happens under the hood

- A PlanetScale connection is a `GeassCloudConnection` in `geass-system`, with its credentials in the Secret
  `<name>-credentials`. See [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/).
- The connection controller sets `Ready` to `True` with reason `CredentialsReady`. Or it sets `Ready` to `False` with
  `CredentialsMissing` or `UnsupportedProvider`.
- The database controller reads the connection and its Secret when it reconciles. It also checks that the connection
  belongs to the project of the database.

```bash title="Inspect PlanetScale connections"
kubectl -n geass-system get geasscloudconnections
kubectl -n geass-system get secret prod-planetscale-credentials
```

## Troubleshooting

**The connection stays Pending.** The Secret does not have `token` or `organization`. The message reads "PlanetScale
token and organization are required".

**The PlanetScale row is grayed out in the Database menu.** The row always reads "Coming soon", for all connections
that the project has. Create PlanetScale databases with a manifest or the API.

**A PlanetScale database never becomes ready.** Without `mode: Create`, or without a host, username, and password,
Geass has nothing to connect to. See [Using it from a database](#using-it-from-a-database). If you created the database
with `mode: Create`, read the message of the database for the PlanetScale API error. After repeated errors, Geass sets
`Ready` to `False` with reason `ProvisionFailed`. Then Geass stops until you change the spec or redeploy.

**A PlanetScale database says "PlanetScale database is starting".** PlanetScale is still provisioning the database,
which takes minutes. Geass checks again automatically. After 30 minutes, the message becomes "PlanetScale database did
not become ready". Then examine the database in PlanetScale.

**"PlanetScale database ... already exists as mysql, not postgresql".** A database with that name exists in your
organization with the other engine. Rename the Geass database, set `spec.databaseName`, or change the `engine`.

**"PlanetScale hosts MySQL and Postgres databases".** The `engine` of the database is Redis or SQLite. PlanetScale
takes only MySQL or Postgres.

**"PlanetScale does not offer cluster size ... for ... databases".** The `clusterSize` is not in the list that
`GET /api/planetscale/cluster-sizes` returns for that engine. Select a name from that list.

[Cloud connectors](/docs/guides/connectors/#troubleshooting) lists the errors that apply to all connections saved for
a project ("project was not found", "connection already exists", "scoped to project").

## Related

- [Cloud connectors](/docs/guides/connectors/)
- [Databases](/docs/guides/databases/)
- [GeassDatabase](/docs/reference/resources/geass-database/)
- [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/)
- [REST API](/docs/reference/rest-api/)
