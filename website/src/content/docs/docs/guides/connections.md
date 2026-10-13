---
title: "Cloud connections"
description: "Connect AWS, PlanetScale, Cloudflare and GitHub to Geass: the permissions each needs, what Geass creates in your accounts, and what it cleans up."
---

A connection is a credential that lets Geass call another service on your behalf. Geass has four: **AWS**
(S3 buckets and database backups), **PlanetScale** (a managed database), **Cloudflare** (DNS and an optional tunnel
for the dashboard) and **GitHub** (repository deploys). You manage them in **Settings** → **Connectors**, which
shows one card for each. The button on a card reads **Configure**; on the AWS, PlanetScale and Cloudflare cards it reads
**Manage** once a connection exists. Servers are different: you add them over SSH, and Geass does not talk to your server
provider (see [Servers are not connections](#servers-are-not-connections)).

![Settings, Connectors: the GitHub, Cloudflare, AWS and PlanetScale cards.](../../../../assets/docs/connections.webp)
*Screenshot with example data.*

:::caution
Connections are the least polished part of Geass. There is no screen to remove or rotate an AWS or PlanetScale
connection, the credentials are not checked when you save them, and the project field expects a project ID rather
than a name. The project's **Settings** → **Integrations** page also says that Cloudflare, AWS and PlanetScale are
"configured once for the whole cluster", which is true only of Cloudflare: AWS and PlanetScale connections belong to
one project. This page documents what the code does today, including those rough edges, so you can work around them.
:::

## Overview

| Connector | Used for | Scope | Credentials | Stored in |
| --- | --- | --- | --- | --- |
| AWS | S3 buckets, project database backups | One project | Access key ID, secret access key, region | Secret `<name>-credentials` in `geass-system` |
| PlanetScale | Creating a database and a login on it | One project | Service token, organization | Secret `<name>-credentials` in `geass-system` |
| Cloudflare | Dashboard DNS, optional tunnel, default hostnames | The whole cluster | API token, account ID, zone | Secret `platform-cloudflare` in `geass-system` |
| GitHub | Repository deploys, push webhooks, PR environments | The cluster, linked to each project | GitHub App credentials | Secret `platform-github-app` in `geass-system` |

Every connection is a Kubernetes Secret in the `geass-system` namespace. Geass does not encrypt these Secrets
itself, and the installer does not turn on K3s secrets encryption, so they are stored like any other Secret in the
cluster datastore. Read [Security](/docs/operations/security/) before you give Geass a broad AWS key.

Only admins can add, change or remove connections. A viewer, or an observer token, does not see cloud connections
at all: the dashboard strips them from what it sends. See [Users and API tokens](/docs/guides/users-and-tokens/).

## AWS

Use an AWS connection when a project needs S3 storage that lives in your own AWS account, or when you want
database backups there. Geass supports three things with it:

- **Buckets.** A **Bucket** resource with the AWS placement creates (or attaches to) S3 buckets and hands your app
  credentials that can reach only those buckets. See [Object storage](/docs/guides/object-storage/).
- **Backups.** A project keeps its Postgres, MySQL and SQLite backups in an S3 bucket. See
  [Backups and restore](/docs/guides/backups-and-restore/).
- **AWS RDS databases.** The **Database** menu lists **AWS RDS**, but Geass has no RDS client. It stores the host,
  port, user and password you give it and builds the connection Secret. It never creates, resizes or deletes an RDS
  instance. Treat it as "attach an existing database".

### Permissions the AWS key needs

Geass signs S3 and IAM calls itself (there is no AWS SDK in the operator). These are all the calls it makes with the
connection key, so these are the minimum permissions:

| Service | Actions | Why |
| --- | --- | --- |
| S3 | `s3:CreateBucket` | Create a bucket. An HTTP 409 reply counts as "already exists". |
| S3 | `s3:ListBucket`, `s3:ListBucketVersions`, `s3:ListBucketMultipartUploads` | Find what to delete. |
| S3 | `s3:DeleteObject`, `s3:DeleteObjectVersion`, `s3:AbortMultipartUpload`, `s3:DeleteBucket` | Empty and delete a bucket, or purge a backup folder. |
| IAM | `iam:CreateUser`, `iam:PutUserPolicy`, `iam:CreateAccessKey` | Create the per-bucket user, its policy and its key. |
| IAM | `iam:ListAccessKeys`, `iam:DeleteAccessKey`, `iam:DeleteUserPolicy`, `iam:DeleteUser` | Remove that user again. |

A policy that grants exactly these (replace the account ID, and narrow the S3 resources to a bucket-name prefix
you control if you can):

```json title="geass-connection-policy.json"
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "GeassBuckets",
      "Effect": "Allow",
      "Action": [
        "s3:CreateBucket",
        "s3:ListBucket",
        "s3:ListBucketVersions",
        "s3:ListBucketMultipartUploads",
        "s3:AbortMultipartUpload",
        "s3:DeleteObject",
        "s3:DeleteObjectVersion",
        "s3:DeleteBucket"
      ],
      "Resource": ["arn:aws:s3:::*", "arn:aws:s3:::*/*"]
    },
    {
      "Sid": "GeassBucketUsers",
      "Effect": "Allow",
      "Action": [
        "iam:CreateUser",
        "iam:PutUserPolicy",
        "iam:CreateAccessKey",
        "iam:ListAccessKeys",
        "iam:DeleteAccessKey",
        "iam:DeleteUserPolicy",
        "iam:DeleteUser"
      ],
      "Resource": "arn:aws:iam::123456789012:user/geass-*"
    }
  ]
}
```

Create a dedicated IAM user for Geass, attach this policy, and create one access key for it. Every user Geass
creates is named `geass-...`, which is what the IAM statement above matches.

### Add an AWS connection

1. Open **Settings** → **Connectors** and choose **Configure** (or **Manage**) on the **AWS** card.
2. Enter a **Name** such as `prod-aws`. It becomes the name of the connection.
3. Enter the **Project scope**. Use the project ID, which starts with `proj-` and appears in the project's address
   (`/projects/proj-1a2b3c4d`). The placeholder shows `payments`, but a project's display name is rejected with
   "project was not found". `kubectl -n geass-system get geassprojects` lists the IDs.
4. Enter the **Access key ID**, the **Secret access key** and the **Region** (the field starts as `us-east-1`).
5. Choose **Save connection**.

The connection is limited to that project. Another project that names it gets "cloud connection is scoped to project",
so add one connection per project. There is no unscoped (platform-wide) connection: an empty project scope is
refused with "project scope is required for cloud connections", and a connection you create with `kubectl` without a
`project` shows **Ready** but is refused by every bucket and database that names it ("must be scoped to a project").

Geass writes the Secret `<name>-credentials` before it checks the project, so a save that fails with "project was not
found" or "project scope is required" still leaves the Secret behind. See the caution under
[Rotate AWS credentials](#rotate-aws-credentials) before you try again with different keys.

You can also create it with the API (an admin session or a workspace token with `admin` access) or with `kubectl`.

```bash title="Create an AWS connection with the API"
curl -fsS -X POST https://geass.example.com/api/cloud-connections/create \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  --data-urlencode "name=prod-aws" \
  --data-urlencode "provider=AWS" \
  --data-urlencode "project=proj-1a2b3c4d" \
  --data-urlencode "accessKeyId=AKIAEXAMPLEEXAMPLE" \
  --data-urlencode "secretAccessKey=replace-with-the-secret-access-key" \
  --data-urlencode "region=eu-north-1"
```

```yaml title="aws-connection.yaml"
apiVersion: v1
kind: Secret
metadata:
  name: prod-aws-credentials
  namespace: geass-system
type: Opaque
stringData:
  accessKeyId: AKIAEXAMPLEEXAMPLE
  secretAccessKey: replace-with-the-secret-access-key
  region: eu-north-1
---
apiVersion: geass.geass.dev/v1alpha1
kind: GeassCloudConnection
metadata:
  name: prod-aws
  namespace: geass-system
spec:
  provider: AWS
  secretRef:
    name: prod-aws-credentials
  project: proj-1a2b3c4d
  region: eu-north-1
```

### Test an AWS connection

Saving does not call AWS. The connection controller only checks that the Secret has an access key ID and a secret
access key, then sets the `Ready` condition and `status.available`. The card and the table show **Ready** or
**Pending** from that check. A wrong or revoked key looks **Ready** until something uses it.

The first real AWS call happens when you deploy a bucket or turn on backups. If the key is wrong, the failure
appears as the message on that bucket or database (for example `AWS S3 403 Forbidden` or `AWS IAM AccessDenied`).
To test a key before you rely on it, create a bucket in a scratch project and deploy it.

### What Geass creates in AWS

For a bucket with **Create bucket in AWS** on:

- The S3 buckets you named, in the bucket's region (the bucket's own region, else the connection's, else
  `us-east-1`).
- An IAM user named `geass-<bucket resource name>` with one inline policy, `geass-bucket`, that allows list, read,
  write and delete on those buckets only (`s3:ListBucket`, `s3:GetBucketLocation`, `s3:ListBucketMultipartUploads`,
  `s3:ListBucketVersions`, `s3:GetObject`, `s3:PutObject`, `s3:DeleteObject`, `s3:DeleteObjectVersion`,
  `s3:AbortMultipartUpload`, `s3:ListMultipartUploadParts`).
- One access key for that user. It is stored in the Secret `<bucket resource name>-connection` in the environment's
  namespace (`<project>-<environment>`) and is what your apps receive. The connection key is not given to apps.

With **Create bucket in AWS** off, Geass only creates the IAM user and key; the bucket must already exist.

For project backups:

- A bucket named `geass-backups-<12 hex characters>`, unless you name one when you save the backup storage. Bucket
  names are global, so the generated name is random.
- An IAM user named `geass-<bucket name>` with the same `geass-bucket` policy, and one access key stored in the
  Secret `geass-backup-storage-<bucket name>` in `geass-system`.
- Objects under one folder per database inside that bucket.

### Rotate AWS credentials

There is no rotate button. Replace the values in the connection's Secret:

```bash title="Rotate the connection key"
kubectl -n geass-system create secret generic prod-aws-credentials \
  --from-literal=accessKeyId=AKIAEXAMPLEEXAMPLE \
  --from-literal=secretAccessKey=replace-with-the-new-secret-access-key \
  --from-literal=region=eu-north-1 \
  --dry-run=client -o yaml | kubectl apply -f -
```

Geass reads the Secret each time it reconciles, so the new key is used the next time a bucket or backup needs AWS.
Running apps are not affected: they use the per-bucket keys, which Geass keeps in their own Secrets. After the new
key works, deactivate the old one in IAM.

:::caution
Do not try to rotate by saving the connection again with the same name. If the Secret `<name>-credentials` already
exists, Geass keeps it and ignores the new keys, so the old credentials stay in use. This also applies after a failed
save, which leaves the Secret behind: fix a mistyped key by editing the Secret as shown above, not by saving again.
Or create a connection with a new name.
:::

### Remove an AWS connection

The dashboard and API have no delete for a single connection. Remove it with `kubectl`, after you delete the buckets
and backups that use it. Deleting an AWS bucket needs the connection, so a connection you remove too early leaves
the bucket stuck deleting with "AWS connection is unavailable".

```bash title="Remove a connection"
kubectl -n geass-system delete geasscloudconnection prod-aws
kubectl -n geass-system delete secret prod-aws-credentials
```

Backups need the connection too: when a database's 7-day grace period ends, Geass uses the connection to purge the backup
folder, and with the connection gone the archive reports `ConnectionUnavailable` and keeps retrying.

Deleting a project deletes its connections in the same pass as its buckets and databases. Delete AWS buckets first,
wait until they are gone, and then delete the project, or an external bucket can be left deleting with nothing to
authenticate with. The credentials Secrets (`<name>-credentials`) are never deleted by Geass, so remove them yourself
afterwards.

### What is cleaned up on AWS

| You delete | Removed from AWS | Left behind |
| --- | --- | --- |
| A bucket with **Create bucket in AWS** on | The buckets (every object, version and unfinished upload first), then the IAM user, its keys and its policy | Nothing |
| A bucket with **Create bucket in AWS** off | The IAM user, its keys and its policy | The bucket and its objects |
| A database with backups | After 7 days, the objects in that database's backup folder | The backup bucket and the `geass-<bucket>` IAM user |
| Project backup storage (**Remove**) | Nothing | The bucket, objects and IAM user |
| The connection | Nothing | Everything Geass created |

Turning backups off for a database also retires its backups after 7 days. Geass never deletes the backup bucket or the
backup IAM user, so remove them yourself when you stop using them.

:::note
Compliance tags keep a project's workloads on tagged nodes, but they stop at the cluster's edge. The backup and restore jobs of a
project that requires node tags run on those nodes, yet they write to your S3 bucket, and a bucket on AWS S3 or a PlanetScale
database lives outside the cluster. Choose buckets and regions that meet the same requirement. See
[Compliance tags](/docs/guides/compliance-tags/).
:::

## PlanetScale

Use a PlanetScale connection to have Geass create a database in your PlanetScale organization and a login for it,
instead of running the database in your cluster. Geass can create MySQL and Postgres databases there.

### Credentials and permissions

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
**Project scope** (the project ID, as for AWS), **Organization** and **Service token**. The API takes the same
fields (`provider=PlanetScale`, `organization`, `token`). The Secret `<name>-credentials` holds `token` and
`organization`.

### Using it from a database

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

### What Geass does with the result

- It writes the Secret `<database>-connection` in the environment's namespace with the host, port, username,
  password, database name and a connection URI. Your apps read that Secret.
- The host is only the host name from PlanetScale's reply. The port is `spec.externalPort` if you set it, and
  otherwise the engine's default: 3306 for MySQL and 5432 for Postgres.
- The database name is the database resource's name (or `spec.databaseName`). For a Postgres database, Geass uses the
  database name PlanetScale reports for the role when there is one.
- PlanetScale shows a password only once, so Geass keeps it in the Secret and reuses that Secret on later reconciles
  instead of creating a second login. If Geass cannot store the Secret, it deletes the login it made in that pass and issues a
  new one on its next pass.

### Choosing a size

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

### Rotate and remove

Rotate the token by editing the Secret `<name>-credentials`, the same way as the AWS example. Databases that already
exist keep working because they hold their own login. There is no delete for a PlanetScale connection in the
dashboard, so remove the `GeassCloudConnection` and its Secret with `kubectl`.

Deleting the Geass database deletes the connection Secret. Geass has no delete call for PlanetScale databases, so the
database and the login it created stay in your PlanetScale organization until you remove them there.

## Cloudflare

Use Cloudflare when your domain is on Cloudflare and you want Geass to manage the DNS records for the dashboard, and
optionally to publish the dashboard through a Cloudflare Tunnel instead of opening ports. There is one Cloudflare
connection for the whole cluster, and it is not project-scoped. See also [Domains and TLS](/docs/guides/domains-and-tls/)
and [Connect a domain](/docs/getting-started/connect-domain/).

### Token permissions

Create a custom API token (not the Global API Key or an Origin CA key) in Cloudflare with these permissions for the
zone you use with Geass:

- **Zone** → **Zone** → **Read**
- **Zone** → **DNS** → **Edit**
- **Account** → **Cloudflare Tunnel** → **Edit** (only exercised when you publish the dashboard through a tunnel)

Geass uses these Cloudflare API calls: `GET /user/tokens/verify` (falling back to `GET /zones` and `GET /accounts`
for account-owned tokens), `GET /accounts`, `GET /zones` and `GET /zones/{id}` to find the zone, the
`cfd_tunnel` endpoints to find or create a tunnel, read its token and publish its routes, and the `dns_records`
endpoints to list, create and update records. Geass reads only the first 50 zones the token can see.

### Connect Cloudflare

1. Open **Settings** → **Connectors** and choose **Configure** on the **Cloudflare** card. **Open Cloudflare to create
   token** takes you to Cloudflare's token page.
2. Paste the token in **API token** and choose **Connect**. Geass checks the token, then lists your accounts and zones.
3. If the token can see more than one zone, choose the **DNS zone**. With a single zone, Geass picks it.

When it works, the card shows **Connected** once the controller has finished (and **Pending** while it does), with the
zone, zone ID and tunnel ID. Then go to **Settings** → **Domain** to publish the dashboard. **Publish via** offers
**DNS to cluster (proxied A record)** or **Cloudflare Tunnel**.

**Refresh zones** re-reads the zone list with the stored token. It does not call Cloudflare's token verify endpoint, and if
Cloudflare cannot list the zones it keeps the saved list and shows a warning instead of an error. The
`CloudflareReady` condition on the platform configuration (see [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/))
reports the state. Its reasons are `Connected`, `Ready`, `CredentialsIncomplete`, `CredentialsInvalid`, `TunnelError`,
`TunnelTokenError`, `TunnelConfigError` and `DNSError`.

### What Geass creates

In Cloudflare, depending on how you publish the dashboard:

| Publish via | Created in Cloudflare | Created in the cluster |
| --- | --- | --- |
| DNS to cluster | A proxied A record for the dashboard host (`geass.<your domain>` by default) pointing at the cluster's external IP | Nothing for Cloudflare |
| Cloudflare Tunnel | A remotely managed tunnel named `geass`; its route list; a proxied CNAME to `<tunnel id>.cfargotunnel.com` for the dashboard host and for every service host | Secret `geass-cloudflared-token` and Deployment `geass-cloudflared` (one replica, image `cloudflare/cloudflared:2024.10.0`) in `geass-system` |

In tunnel mode the route list sends the dashboard host to the dashboard Service on port 8085, each service host to
that service, and everything else to a 404. The tunnel's dashboard route and CNAME are always for `geass.<your domain>`,
whatever **Subdomain** you saved under **Settings** → **Domain**, so keep the default `geass` when you publish through a
tunnel. Once Cloudflare is ready, in either mode, a service without a host gets a default one,
`<service>-<project id>.<your domain>` (with `-<environment>` added for non-production environments), with TLS off. Only tunnel mode creates
DNS records for those hosts. In the other mode Geass creates only the dashboard record, so point your service hostnames at the cluster
yourself, for example with a wildcard record.

Geass rebuilds the tunnel's routes and DNS records when a service gets, changes or loses a host, and checks them again every 5 minutes
while nothing changes. A route or record you delete by hand in Cloudflare comes back within about 5 minutes. The tunnel reaches a service
without going through Traefik, so a service that [sleeps when idle](/docs/guides/scale-to-zero/) gets a route to the wake-up proxy
(`keda-add-ons-http-interceptor-proxy` in the `keda` namespace) instead of straight to the service, and the request wakes it.

### Rotate and disconnect

To rotate the token, choose **Update API token**, paste the new token and choose **Save changes**. Geass checks it and
reads the zone again. The new token needs the same permissions.

To disconnect, type `remove-cloudflare` in the confirmation field and choose **Disconnect**. Geass deletes the
Secret `platform-cloudflare`, clears the connection from the platform configuration, and stops and removes the
`geass-cloudflared` Deployment and its token Secret. Geass does not delete the tunnel or the DNS records it made in
Cloudflare, so remove them there if you no longer want them. The dashboard host stops resolving to the cluster only
when you remove the records.

The Secret `platform-cloudflare` holds `apiToken`, `accountId`, `zoneName` and `zones` (a list of the zones the token
can read).

## GitHub

Use GitHub when you deploy from a repository: it lets Geass build on a push and create pull request environments.
The setup has two parts: one GitHub App for the whole cluster, and one install per project. The day-to-day use is in
[GitHub and builds](/docs/guides/github-and-builds/) and
[Pull request environments](/docs/guides/pull-request-environments/).

### Before you start

Set a public HTTPS dashboard address first (**Settings** → **Domain**). GitHub calls back to it, and Geass refuses a
GitHub App whose public URL is not HTTPS ("GitHub App public URL must use HTTPS"). Until then the card offers
**Configure domain** instead of the app form.

### Register the GitHub App

1. Open **Settings** → **Connectors** and choose **Configure** on the **GitHub** card.
2. Choose **Create GitHub App on GitHub**. GitHub shows a prefilled app and you confirm it. Its name comes from the dashboard
   host with the `geass.` prefix dropped and dots turned into hyphens: `Geass-example-com` for `geass.example.com`.
3. GitHub sends you back and Geass stores the credentials. The card shows **Connected**.

The app Geass asks GitHub for is private, with permissions **Contents** read, **Metadata** read and **Pull requests**
read, and events `push` and `pull_request`. The webhook URL is `<dashboard URL>/webhooks/github`, the callback and
setup URLs are `<dashboard URL>/github/callback`.

If you already have a GitHub App, use **Manual credentials** instead: enter **App ID**, **Client ID**, **App slug**,
**Client secret**, **Webhook secret** and **Private key (PEM)**, then choose **Save GitHub App**. The key must be an RSA
private key in PEM form. You can also supply the same values to the operator as environment variables (see
[Configuration](/docs/reference/configuration/)); the dashboard form and the stored Secret take precedence.

### Link a project

In a project, open **Settings** → **Integrations** and choose **Connect to GitHub** (or add a GitHub repository
service, which does the same). GitHub asks which account and repositories the app may use, then returns to Geass.
Geass creates the connection `<project id>-github` and the Secret `<project id>-github-token` with the installation
token, the installation ID and the account. The token is short-lived: Geass asks GitHub for a fresh one with the app's
private key when it needs one. **Disconnect** removes the project's link only; the connection object and Secret stay
until you delete them.

### Test, rotate and remove

- **Test GitHub App** compares the webhook URL stored in the GitHub App with `<dashboard URL>/webhooks/github`.
  It fails with `GitHub webhook URL is X, expected Y` when the dashboard address changed.
- To rotate the **Client secret**, **Webhook secret** or **Private key (PEM)**, generate the new value in GitHub and
  save it with **Manual credentials**. A field you leave empty keeps its stored value. Update the webhook secret in
  GitHub too, or deliveries fail verification.
- **Remove GitHub App** needs the confirmation text `remove-github`. It clears the platform's reference and deletes the
  Secret `platform-github-app`. The app and its installations stay in GitHub, so uninstall them there. Projects keep
  their links but cannot refresh tokens until you register an app again.

The Secret `platform-github-app` holds `app_id`, `client_id`, `client_secret`, `slug`, `private_key` and
`webhook_secret`.

### How webhooks are verified

GitHub posts to `/webhooks/github`, which is reachable without a login because GitHub cannot sign in. Geass checks the
`X-Hub-Signature-256` header (HMAC-SHA256 of the body with the webhook secret) and rejects a mismatch with `401`. It
answers `503` if no webhook secret is configured. It handles `ping`, `push` and `pull_request` and ignores other events.

## Servers are not connections

Servers join the cluster over SSH, from a node resource that holds the server's address, an SSH user and a Secret with
the private key. The user must be root or have passwordless `sudo`. Geass installs K3s on it and joins it to the
cluster. See [Add servers](/docs/getting-started/add-servers/) and [GeassNode](/docs/reference/resources/geass-node/).

Geass does not create virtual machines and has no API integration with any server provider. In particular:

- There is no AWS EC2, Hetzner, DigitalOcean or other provisioning.
- There is no GCP or Azure connection of any kind. A server at GCP or Azure can join as a node like any other Linux
  server, but Geass does not manage its cloud account, billing or networking.
- A cluster whose servers are at more than one provider is experimental. See
  [Multi-provider clusters](/docs/operations/multi-provider/).

:::caution
When Geass connects over SSH to join a server it does not verify the server's host key. Join only servers you trust
the network path to, and see [Security](/docs/operations/security/).
:::

## Where secrets are stored

| Connection | Secret | Keys |
| --- | --- | --- |
| AWS | `<name>-credentials` | `accessKeyId`, `secretAccessKey`, `region` |
| PlanetScale | `<name>-credentials` | `token`, `organization` |
| Cloudflare | `platform-cloudflare` | `apiToken`, `accountId`, `zoneName`, `zones` |
| Cloudflare tunnel | `geass-cloudflared-token` | `token` |
| GitHub App | `platform-github-app` | `app_id`, `client_id`, `client_secret`, `slug`, `private_key`, `webhook_secret` |
| GitHub project install | `<project id>-github-token` | `token`, `installation_id`, `account`, `account_type` |
| AWS bucket user | `<bucket>-connection` in `<project>-<environment>` | `endpoint`, `accessKey`, `secretKey` and bucket details |
| Backup bucket user | `geass-backup-storage-<bucket>` | `ACCESS_KEY_ID`, `ACCESS_SECRET_KEY`, `REGION` |

Secret values never appear in a resource's `status`, and the dashboard never sends them to the browser. View the
Secrets with `kubectl -n geass-system get secret` (you need cluster access for that).

## What happens under the hood

- Each connection is a `GeassCloudConnection` (AWS and PlanetScale) in `geass-system`; Cloudflare and GitHub live on the
  platform configuration, named `platform`. See [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/).
- The connection controller sets `Ready` to `True` with reason `CredentialsReady`, or `False` with `CredentialsMissing` or
  `UnsupportedProvider`.
- The object store, database and backup controllers read the connection and its Secret when they reconcile. They also
  check that the connection belongs to the resource's project.

```bash title="Inspect connections"
kubectl -n geass-system get geasscloudconnections
kubectl -n geass-system get geassplatformconfig platform -o yaml
kubectl -n geass-system get secret platform-cloudflare platform-github-app
```

## Troubleshooting

**The connection stays Pending.** The Secret is missing `accessKeyId` or `secretAccessKey` (AWS), or `token` or
`organization` (PlanetScale). The message says which: "AWS access key and secret key are required" or "PlanetScale token
and organization are required".

**"project was not found" when saving a connection.** You entered a display name. Use the project ID (`proj-` followed by
eight characters).

**"connection already exists".** A connection or resource with that name exists. Choose another name, and remember the
Secret `<name>-credentials` may still be there from an earlier attempt.

**"cloud connection X is scoped to project Y".** A bucket or database in project Z named a connection that belongs to
another project. Add a connection for Z.

**The AWS option is greyed out in the resource menu.** The menu offers **AWS RDS** only when the project has a ready AWS
connection. The text reads "Add an AWS connection in platform settings". The **PlanetScale** row is always greyed out
("Coming soon"): create PlanetScale databases with a manifest or the API.

**A bucket fails with `AWS IAM ... AccessDenied` or `AWS S3 403`.** The connection key lacks one of the permissions in
[Permissions the AWS key needs](#permissions-the-aws-key-needs).

**A bucket reports ready but the app cannot read it.** Geass treats an S3 `409` as "bucket exists", which includes a
bucket name owned by another AWS account. Bucket names are global; pick a distinctive name.

**A bucket is stuck deleting with "AWS connection is unavailable".** The connection or its Secret was removed before the
bucket. Recreate the connection with the same name and Secret keys, and the deletion completes.

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

**Cloudflare says "No zones on this token".** Add **Zone** → **Zone** → **Read** to the token, or reconnect and choose a zone.

**Cloudflare says "Cloudflare rejected the API token".** The token was revoked or lacks a permission. Use **Update API token**.

**GitHub says "dashboard URL is not configured" or "GitHub App is not configured".** Finish **Settings** → **Domain** first,
then register the app.

**GitHub webhooks return 401.** The webhook secret in GitHub differs from the one Geass stores. Save the matching value with
**Manual credentials**.

## Related

- [Object storage](/docs/guides/object-storage/)
- [Backups and restore](/docs/guides/backups-and-restore/)
- [Databases](/docs/guides/databases/)
- [GitHub and builds](/docs/guides/github-and-builds/)
- [Domains and TLS](/docs/guides/domains-and-tls/)
- [Users and API tokens](/docs/guides/users-and-tokens/)
- [Security](/docs/operations/security/)
