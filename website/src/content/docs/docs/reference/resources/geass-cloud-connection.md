---
title: "GeassCloudConnection"
description: "Reference for GeassCloudConnection: credentials in a Secret for AWS, PlanetScale, or a cloud that creates machines, and what uses them."
sidebar:
  order: 10
---

A `GeassCloudConnection` tells Geass which Secret holds credentials for a cloud or a database vendor. A connection has
two kinds of use:

- The AWS connection of a project serves buckets and database backups. A PlanetScale connection serves PlanetScale
  databases.
- A connection for machines, with the purpose `Compute`, belongs to the cluster. With that connection,
  [node pools](/docs/guides/node-pools/) and single servers can create machines at Amazon EC2, Azure, DigitalOcean,
  Google Cloud, Hetzner, Linode, Oracle Cloud, OVHcloud, Scaleway, UpCloud, Vultr, or through a webhook.

The resource holds no secrets. It points at a Secret in `geass-system`, and it records what the provider said about
that Secret.

When you save a connection in **Settings** → **Connectors**, the dashboard creates the connection together with the
Secret. For the task-oriented view, see these pages:

- [Cloud connectors](/docs/guides/connectors/): the permissions that each AWS use needs, and what Geass creates in your
  account.
- [Cloud connections for machines](/docs/guides/connectors/clouds/): what each cloud takes.

Cloudflare and GitHub are not `GeassCloudConnection` objects. Cloudflare is on the
[GeassPlatformConfig](/docs/reference/resources/geass-platform-config/), and GitHub is on a
[GeassGitHubConnection](/docs/reference/resources/geass-github-connection/).

## Summary

| Property | Value |
| --- | --- |
| API version | `geass.geass.dev/v1alpha1` |
| Kind | `GeassCloudConnection` |
| Plural | `geasscloudconnections` |
| Scope | Namespaced |
| Namespace | `geass-system` |
| Subresources | `status` |
| Printer columns | None |

## Examples

The first example is an AWS connection for buckets and backups, with its Secret. The Secret keys are `accessKeyId`,
`secretAccessKey`, and `region`.

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

The next example is a PlanetScale connection. The Secret keys are `token` and `organization`. The same connection
serves MySQL and Postgres databases.

```yaml title="planetscale-connection.yaml"
apiVersion: v1
kind: Secret
metadata:
  name: prod-planetscale-credentials
  namespace: geass-system
type: Opaque
stringData:
  token: replace-with-the-service-token
  organization: acme
---
apiVersion: geass.geass.dev/v1alpha1
kind: GeassCloudConnection
metadata:
  name: prod-planetscale
  namespace: geass-system
spec:
  provider: PlanetScale
  secretRef:
    name: prod-planetscale-credentials
  project: proj-1a2b3c4d
  organization: acme
```

The last example is a connection for machines at Hetzner. It has no `project`. Its Secret holds the keys that the cloud
names, here `token`. AWS is also a vendor of buckets. So for AWS, set `purposes: [Compute]`, and keep the connection
separate from the AWS connection above.

```yaml title="hetzner-connection.yaml"
apiVersion: v1
kind: Secret
metadata:
  name: hetzner-credentials
  namespace: geass-system
type: Opaque
stringData:
  token: replace-with-the-api-token
---
apiVersion: geass.geass.dev/v1alpha1
kind: GeassCloudConnection
metadata:
  name: hetzner
  namespace: geass-system
spec:
  provider: Hetzner
  secretRef:
    name: hetzner-credentials
  purposes: [Compute]
  region: fsn1
```

## Spec fields

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.provider` | string | none | Required. `AWS`, `PlanetScale`, `GCP`, `DigitalOcean`, `Hetzner`, `OVHcloud`, `Vultr`, `Linode`, `Scaleway`, `Azure`, `OCI`, `UpCloud`, or `Webhook` | The vendor. Every vendor except `PlanetScale` can create machines. |
| `spec.secretRef.name` | string | none | Required | A Secret in `geass-system` with the credentials. |
| `spec.project` | string | none | none | The ID of the project that can use the connection. In practice, it is required for buckets, backups, and databases, and it **must be empty** for `Compute`. See the rule below. |
| `spec.org` | string | none | `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, at most 63 characters | A [GeassOrganization](/docs/reference/resources/geass-organization/) that owns the connection, when the connection names no project. Every project of that organization can use the connection, and no other project can. Geass ignores this field when `spec.project` is set. Not for `Compute`, which belongs to the cluster. |
| `spec.region` | string | none | none | The default region of the connection. For buckets and backups, it is an AWS region. For machines, it is the place of the cloud itself: a location, zone, or region. Geass uses it when a pool or a server names none. |
| `spec.organization` | string | none | none | The PlanetScale organization. When it is empty, Geass uses the `organization` key of the Secret. PlanetScale only. |
| `spec.purposes` | list | See below | At most 3. Each is `Storage`, `Database`, or `Compute`. A set | What the connection is for. |

### Purposes

The purposes of a connection decide what can use it. If you leave out `spec.purposes`, it means what the provider was
for before purposes existed:

- `Storage` and `Database` for `AWS`.
- `Database` for `PlanetScale`.
- `Compute` for every other cloud.

So an AWS connection that is older than purposes never creates machines. A connection for a cloud that creates machines
is for `Compute`, even when it does not say so. A connection for `Compute` creates and deletes machines for pools and
servers. It must not be scoped to a project.

### Scope

A connection for buckets, backups, or databases belongs to a project or to an organization. Every use checks that the
connection belongs to the project that uses it:

- A connection that names a project (`spec.project`) belongs to that project only. If a project names the connection
  of another project, it gets `cloud connection "<name>" is scoped to project "<project>"`.
- A connection with no project that names an organization (`spec.org`) can be used by every project of that
  organization, and by no other project. A project of another organization, or of no organization, gets
  `cloud connection "<name>" belongs to an organization that project "<project>" is not in`.
- Nothing can use a connection that has neither. The error is
  `cloud connection "<name>" must be scoped to a project or an organization`.

So there is no platform-wide connection. Create one connection for each project, or one for each organization. The
dashboard also refuses to save a connection without a project or an organization. A platform administrator can set
`org`. An administrator of the whole organization can set `org` to their own organization. Any project of the
organization can use the credentials of an organization connection. So limit those credentials to what all of its
projects can touch.

A connection for `Compute` is the opposite. It belongs to the cluster, never to a project. Geass refuses a pool or a
server that names a connection with a `spec.project` or a `spec.org`:
`cloud connection "<name>" belongs to project "<project>", and a project's connection cannot create machines for the cluster`.
Geass also refuses a pool or a server that names a connection without the purpose:
`cloud connection "<name>" is not for compute: add Compute to its purposes`. Only an admin can make, change, or use a
`Compute` connection. Storage must belong to a project or an organization, and machines must not. So one AWS
connection cannot serve both. Make two connections.

### The Secret

| Provider | Keys | Required |
| --- | --- | --- |
| AWS | `accessKeyId`, `secretAccessKey` | Both |
| AWS | `sessionToken`, `expiresAt` | No, and for a connection for `Compute` only. `sessionToken` is the third part of temporary credentials (from `sts:AssumeRole`). Geass sends it, signed, with every request. `expiresAt` is an RFC 3339 time that says when the credentials stop working. AWS does not say that. See [Temporary credentials](/docs/guides/connectors/clouds/#temporary-credentials) |
| AWS | `region` | No. For buckets, the default region is `spec.region`, then the `region` of the Secret, then `us-east-1`. For machines, it is the `region` of the Secret, then `spec.region`, then `us-east-1` |
| PlanetScale | `token` | Yes |
| PlanetScale | `organization` | Yes, unless `spec.organization` is set |
| Each other cloud | The keys that the cloud names. See [the table](/docs/guides/connectors/clouds/#the-clouds) | The keys that are not marked optional |

The dashboard and `geass connections add` create the Secret `<name>-credentials` after the connection. If a Secret
with that name already exists, the AWS card keeps the old Secret and ignores new credentials. See
[Connect AWS](/docs/guides/connectors/aws/#rotate-aws-credentials). **Edit** on the connection and
`geass connections update` replace the credentials in the Secret. When
[organizations are on](/docs/guides/organizations-and-workspaces/) and an organization makes the connection, Geass
refuses the connection instead ("that name is already taken"). So the connection cannot adopt a Secret that is not its
own.

## Status

| Field | Type | Meaning |
| --- | --- | --- |
| `status.available` | boolean | `true` when the Secret exists and has the keys that the provider needs, and, for a connection for `Compute`, the cloud accepted the credentials. |
| `status.conditions` | list | One condition, `Ready`. |
| `status.account` | string | The account, project, or tenant that the credentials act on, as the cloud reports it. For `Compute` only. |
| `status.message` | string | What the last check found, or why it could not tell. |
| `status.verifiedAt` | date-time | When the cloud last accepted the credentials. |
| `status.expiresAt` | date-time | When the credentials stop working. Set for the clouds that report it (an OVHcloud project, a webhook that reports it), and for an AWS connection that holds an `expiresAt`. |
| `status.warnings` | list | Things that work now and are likely to fail later, at most 10. See [Cloud connections for machines](/docs/guides/connectors/clouds/#what-the-connection-says-about-itself). |

| Condition | Status | Reason | Meaning |
| --- | --- | --- | --- |
| `Ready` | `True` | `CredentialsReady` | The Secret has the required keys. The message is `Cloud connection is ready`, or, for `Compute`, `The provider accepted the credentials`. |
| `Ready` | `False` | `CredentialsMissing` | `spec.secretRef.name` is empty (`A credentials secret is required`), the Secret does not exist (`Connection credentials are unavailable`), or the Secret lacks a key (`AWS access key and secret key are required`, `PlanetScale token and organization are required`, or `<cloud> needs <the missing fields>`). |
| `Ready` | `False` | `VerificationFailed` | `Compute` only. The cloud refused the credentials, or Geass could not reach the cloud. The message is from the cloud. |
| `Ready` | `False` | `UnsupportedProvider` | Geass does not know the provider. The CRD does not allow that value. |

:::note
For a connection that is not for `Compute`, `Ready` means that the keys are present, not that they work. Geass does not
call AWS or PlanetScale to test a connection for buckets, backups, or databases. A wrong key shows when something first
uses it. A connection for `Compute` is different. Geass asks the cloud, and `Ready` shows the answer. Even then, most
clouds cannot say whether the credentials can create a machine without creating something. So a connection can be
`Ready`, and the first machine can still fail. A warning says so.
:::

## How the dashboard maps to it

| Dashboard | Effect |
| --- | --- |
| **Settings** → **Connectors** → **AWS** or **PlanetScale** → **Save connection** | Creates the Secret `<name>-credentials` and the `GeassCloudConnection`. `spec.project` comes from **Project**, and `spec.region` or `spec.organization` comes from the form. |
| **Settings** → **Connectors** → **Server clouds** → **Connect a cloud** | Creates the Secret and a connection with no project and the purpose `Compute`. For AWS, the connection also gets `Storage` and `Database` if you select them. Then the dashboard asks the cloud. |
| **Verify now**, **Edit** and **Remove** on a connection in the **Server clouds** list, or in the **AWS** or **PlanetScale** drawer | **Verify now** asks the cloud again. **Edit** changes the default region, purposes, or credentials. **Remove** deletes the connection and its Secret. **Remove** refuses only while a pool or a machine uses the connection. It does not look for buckets, backups, or databases. |
| The connectors list | Shows each connection with its project (`Platform` if empty), and **Ready** or **Pending** from `status.available`. |
| The **Bucket** and **Database** menus, and project **Settings** → **Backups** | Offer a provider only when the project has a connection of that kind with `status.available` true. |

The REST endpoints are `POST /api/cloud-connections/create` for the AWS or PlanetScale connection of a project. For the
other connections, they are `GET`, `POST`, `PATCH`, and `DELETE /api/v1/connections`. See
[REST API](/docs/reference/rest-api/#machines-clouds-pools-and-servers).

## What uses a connection

| User | Field | Provider |
| --- | --- | --- |
| [GeassObjectStore](/docs/reference/resources/geass-object-store/) | `spec.connectionRef` | AWS |
| [GeassProject](/docs/reference/resources/geass-project/) backup storage, and the [GeassBackupArchive](/docs/reference/resources/geass-backup-archive/) that it creates | `spec.backupStorage.connectionRef` | AWS |
| [GeassDatabase](/docs/reference/resources/geass-database/) | `spec.connectionRef` | PlanetScale (to create a MySQL or Postgres database), AWS (checked only) |
| [GeassNodePool](/docs/reference/resources/geass-node-pool/) | `spec.connectionRef` | Any cloud that creates machines. The connection must be for `Compute` |
| [GeassNode](/docs/reference/resources/geass-node/) | `spec.cloud.connectionRef` | The same |
| [GeassCluster](/docs/reference/resources/geass-cluster/) | `spec.network.controlPlaneFirewall.connectionRef` | AWS or Hetzner, to open the firewall of the control plane for new machines |

## Editing by hand

You can safely create and edit a `GeassCloudConnection` with `kubectl`, and edit its Secret. To rotate credentials,
change the Secret. Delete a connection only after the buckets, backups, databases, pools, and machines that use it are
gone. To delete an AWS bucket or purge a backup, Geass needs the connection. To delete a machine, Geass also needs the
connection. Without the connection, these operations cannot finish, and a machine that nothing can delete continues to
cost money. `kubectl delete` does not check for these users. The dashboard, the command line, and the API refuse to
remove a connection that a pool or a machine uses.

## What happens under the hood

A controller watches the connections, reads the Secret, and sets `status` and the `Ready` condition. The controller
writes the status only when it differs from the stored status. For a connection for `Compute`, the controller also
builds the driver of the cloud and calls its check, with a 30-second limit. Then the controller does these things:

- It trusts the answer for 15 minutes, unless the connection, its Secret, or the annotation `geass.dev/recheck`
  changes.
- It asks again after 2 minutes when the cloud did not accept the credentials.
- From 14 days before `status.expiresAt`, or after that time, it adds a warning that the credentials expire or have
  expired.

The controller never calls AWS or PlanetScale for a connection that is only for buckets, backups, or databases.

The controller watches the connection objects, not their Secrets. So a change to a Secret does not update `status`
until the connection is reconciled again. That happens at the next of those checks, or immediately for any change to
the connection. For example:
`kubectl annotate geasscloudconnection prod-aws geass.dev/recheck="$(date +%s)" --overwrite`.
`geass connections verify NAME` sets that annotation. The object store, database, and backup controllers read the
connection and its Secret each time they reconcile. The node and pool controllers build the driver of the cloud from
them, and keep the driver until either one changes. There is no finalizer, and the Secret has no owner reference. So
when you delete a connection, or the project, which deletes its connections, the Secret stays. The exception is when
the dashboard or `geass connections remove` deleted the Secret.

```bash title="Inspect connections"
kubectl -n geass-system get geasscloudconnections
kubectl -n geass-system get geasscloudconnection prod-aws -o jsonpath='{.status.available}{" "}{.status.conditions[0].reason}{"\n"}'
kubectl -n geass-system get geasscloudconnection hetzner -o jsonpath='{.status.account}{"  "}{.status.message}{"\n"}'
```

## Troubleshooting

**The connection is `Pending` with `CredentialsMissing`.** Read the message. The Secret named in `spec.secretRef.name`
is missing, or it lacks `accessKeyId` and `secretAccessKey` (AWS), `token` and `organization` (PlanetScale), or one of
the fields of the cloud. If you created or fixed the Secret after the connection, the status does not change by itself
for some time. Annotate the connection as shown under [What happens under the hood](#what-happens-under-the-hood), or
run `geass connections verify NAME`.

**`VerificationFailed`.** The cloud refused the credentials, or Geass could not reach the cloud. The message is from
the cloud. See [Cloud connections for machines](/docs/guides/connectors/clouds/#troubleshooting).

**`cloud connection "x" is scoped to project "y"`.** The resource that named the connection belongs to another project.
Create a connection for that project.

**`must be scoped to a project`.** `spec.project` is empty on a connection that a bucket, backup, or database uses. Set
it to the project ID (`proj-...`), not the display name.

**`is not for compute` or `belongs to project`.** A pool or a server named a connection that lacks the purpose
`Compute`, or that is scoped to a project. The usual cause is an AWS connection made for buckets. Make a separate AWS
connection for machines.

**The credentials are wrong but the connection shows `Ready`.** This result is expected for a connection for buckets,
backups, or databases. See the note above. To test an AWS key, deploy a bucket in a scratch project.

**New credentials are ignored after I saved the connection again.** The Secret already existed. Edit the Secret, or use
**Edit** on the connection.

## Related

- [Cloud connectors](/docs/guides/connectors/)
- [Cloud connections for machines](/docs/guides/connectors/clouds/)
- [GeassNodePool](/docs/reference/resources/geass-node-pool/)
- [GeassObjectStore](/docs/reference/resources/geass-object-store/)
- [GeassDatabase](/docs/reference/resources/geass-database/)
- [GeassBackupArchive](/docs/reference/resources/geass-backup-archive/)
- [GeassProject](/docs/reference/resources/geass-project/)
- [Resources overview](/docs/reference/resources/overview/)
