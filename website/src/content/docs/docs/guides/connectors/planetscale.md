---
title: "Connect PlanetScale"
description: "Connect PlanetScale to Geass to create MySQL and Postgres databases in your organization: the token, the API calls, cluster sizes, and what Geass creates."
sidebar:
  label: PlanetScale
---

With a PlanetScale connection, Geass creates a database in your PlanetScale organization, and a login for it. The
database does not run in your cluster. Geass can create MySQL and Postgres databases in PlanetScale.

:::caution
PlanetScale support is experimental. The dashboard cannot create PlanetScale databases yet. Create them with the API,
as [Create a PlanetScale database](#create-a-planetscale-database) shows.
:::

## Before you start

You need these things:

- A PlanetScale organization, and a service token that can create databases and logins. The next section lists the
  permissions.
- The project that uses the connection.
- An admin login in Geass.

## Credentials and permissions

You give Geass a PlanetScale **Service token** and the **Organization** name. Geass makes only these calls:

- Read a database, to see whether it exists and whether PlanetScale finished creating it.
- Create a database.
- Create a login on the `main` branch: a password for MySQL, or a role for Postgres.
- Delete that login, only to undo a login that Geass could not store.
- Read the cluster sizes of the organization, only when you list or check sizes.

So the token must have permission to do these tasks:

- Create databases.
- Create and delete passwords and roles on the `main` branch.
- Read the cluster sizes of the organization.

Find the names of these permissions in the service token settings of PlanetScale.

## Add a PlanetScale connection

1. Open **Settings** → **Connectors**.
2. On the **PlanetScale** card, click **Configure** or **Manage**.
3. Enter a **Name**.
4. Select the **Project**. If organizations are on, you can select an **Organization** instead.
5. Enter the PlanetScale **Organization** and the **Service token**.
6. Click **Save connection**.

## Create a PlanetScale database

In the **Database** menu, the **PlanetScale** row is unavailable and reads "Coming soon". Use the
[REST API](/docs/reference/rest-api/) instead, with an admin token:

1. Send `POST /api/databases/create` with these fields:
   - `mode=Create`, `provider=PlanetScale`, and `placement=External`.
   - `engine`: `MySQL` or `Postgres`. A missing or unknown value means Postgres.
   - `connectionRef`: the name of a ready connection of the project.
   - `project`, `environment`, and `name`.
   - Optional: `clusterSize`. See [Choosing a size](#choosing-a-size).
2. Deploy the database with `POST /api/databases/{name}/deploy`.

```bash title="Create and deploy a PlanetScale database"
curl -fsS -X POST https://geass.example.com/api/databases/create \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  --data-urlencode "name=orders-db" \
  --data-urlencode "project=proj-1a2b3c4d" \
  --data-urlencode "environment=production" \
  --data-urlencode "engine=Postgres" \
  --data-urlencode "placement=External" \
  --data-urlencode "provider=PlanetScale" \
  --data-urlencode "mode=Create" \
  --data-urlencode "connectionRef=prod-planetscale"

curl -fsS -X POST -H "Authorization: Bearer $GEASS_TOKEN" \
  https://geass.example.com/api/databases/orders-db/deploy
```

Geass creates these items in PlanetScale:

- A database with the name of the Geass database, of the kind that matches the engine. If a database with that name
  exists in the organization, Geass uses it, but only if it is the same kind.
- A login on the `main` branch. For Postgres, the role inherits the `postgres` role, so your app can run migrations.

PlanetScale takes some minutes to create a new database. Until the database is ready, Geass shows
"PlanetScale database is starting". This message is not a failure. If the database is not ready after 30 minutes, the
message changes to "PlanetScale database did not become ready within 30m0s; check the database in PlanetScale".

## What your app gets

Geass gives your services the host, port, user name, password, database name, and a connection URI. The port is 3306
for MySQL and 5432 for Postgres.

PlanetScale shows a password only one time. Geass keeps it, and does not create a second login. If Geass cannot store
the password, it deletes the login and makes a new one on its next try.

## Choosing a size

`clusterSize` is the PlanetScale cluster size name, for example `PS_10`. To let PlanetScale select the size, leave it
empty. These rules apply:

- Geass sends the size only when it creates the database. Geass never resizes a PlanetScale database.
- After the database is created, the API refuses a new size with "A PlanetScale database is sized when it is created".
- Geass refuses a size that PlanetScale does not offer for the engine. If Geass cannot load the list of sizes,
  PlanetScale checks the size when it creates the database.

To list the sizes that your organization can use, run this command with an admin or observer token. If the
PlanetScale token can read prices, the list includes the monthly price in US dollars.

```bash title="List PlanetScale cluster sizes"
curl -fsS -H "Authorization: Bearer $GEASS_TOKEN" \
  "https://geass.example.com/api/planetscale/cluster-sizes?project=proj-1a2b3c4d&connection=prod-planetscale&engine=postgres" \
  | jq '.items[] | {name, cpu, ramBytes, monthlyUsd}'
```

The `engine` is `mysql` or `postgres`. Geass keeps each list for 5 minutes.

## Rotate and remove

The dashboard has no **Edit** for a PlanetScale connection, and Geass has no other way to replace the token yet. See
[Under the hood](#under-the-hood). Databases that exist continue to work after a new token, because each has its own
login.

To remove the connection, click **Remove** on it. Geass refuses while a database uses the connection.

When you delete the Geass database, Geass forgets its login. Geass does not delete the PlanetScale database. The
database and its login stay in your PlanetScale organization until you remove them there.

:::note
Compliance tags stop at the edge of the cluster. A PlanetScale database is outside the cluster. See
[Compliance tags](/docs/guides/compliance-tags/).
:::

## Troubleshooting

**The connection stays Pending.** The token or the organization is missing. The message reads "PlanetScale token and
organization are required".

**The PlanetScale row is unavailable in the Database menu.** The row always reads "Coming soon". Create the database
with the API.

**The database fails after repeated errors.** Read the message of the database for the PlanetScale error. Geass stops
after repeated errors. Fix the cause, and then deploy the database again.

**"PlanetScale database is starting".** PlanetScale is still creating the database. Geass checks again automatically.

**"PlanetScale database did not become ready".** Examine the database in PlanetScale.

**"PlanetScale database ... already exists as mysql, not postgresql".** A database with that name exists in your
organization with the other engine. Rename the Geass database, or change the `engine`.

**"PlanetScale hosts MySQL and Postgres databases".** The engine is Redis or SQLite. Use MySQL or Postgres.

**"PlanetScale does not offer cluster size ... for ... databases".** Select a size from the list that the
cluster-sizes call returns for that engine.

For the errors of every project connection, see [Cloud connectors](/docs/guides/connectors/#troubleshooting).

## Under the hood

- A PlanetScale connection is a `GeassCloudConnection` in `geass-system`. Its credentials are in the Secret
  `<name>-credentials`, with the keys `token` and `organization`. See
  [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/).
- You can also create the database as a `GeassDatabase` with `placement: External`, `provider: PlanetScale`,
  `mode: Create`, and `connectionRef`. Without `mode: Create`, Geass has nothing to connect to. The field
  `spec.databaseName` sets a PlanetScale name that is different from the Geass name. See
  [GeassDatabase](/docs/reference/resources/geass-database/).
- The login is in the Secret `<database>-connection` in the environment of the database.
- To rotate the token, replace it in the Secret of the connection on the server:

  ```bash title="Replace the PlanetScale token"
  kubectl -n geass-system create secret generic prod-planetscale-credentials \
    --from-literal=token=replace-with-the-new-service-token \
    --from-literal=organization=acme \
    --dry-run=client -o yaml | kubectl apply -f -
  ```

## Related

- [Cloud connectors](/docs/guides/connectors/)
- [Databases](/docs/guides/databases/)
- [REST API](/docs/reference/rest-api/)
- [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/)
