---
title: "GeassCloudConnection"
description: "Reference for GeassCloudConnection: credentials for AWS, PlanetScale or a cloud that creates machines, kept in a Secret, and what uses them."
sidebar:
  order: 10
---

A `GeassCloudConnection` tells Geass which Secret holds credentials for a cloud or a database vendor. It has two kinds of use. A project's AWS connection serves buckets and database backups, and a PlanetScale connection serves PlanetScale databases. A connection for machines, with the purpose `Compute`, belongs to the cluster and lets [node pools](/docs/guides/node-pools/) and single servers create machines at Amazon EC2, Azure, DigitalOcean, Google Cloud, Hetzner, Linode, Oracle Cloud, OVHcloud, Scaleway, UpCloud, Vultr, or through a webhook. The resource holds no secrets: it points at a Secret in `geass-system` and records what the provider said about it.

The dashboard creates one when you save a connection in **Settings** → **Connectors**, together with the Secret. The task-oriented view is in [Cloud connectors](/docs/guides/connectors/) (with the permissions each AWS use needs and what Geass creates in your account) and [Cloud connections for machines](/docs/guides/connectors/clouds/) (what each cloud takes). Cloudflare and GitHub are not `GeassCloudConnection` objects: Cloudflare lives on the [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/), and GitHub on a [GeassGitHubConnection](/docs/reference/resources/geass-github-connection/).

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

An AWS connection for buckets and backups, and its Secret. The Secret keys are `accessKeyId`, `secretAccessKey` and `region`.

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

A PlanetScale connection. The Secret keys are `token` and `organization`. The same connection serves MySQL and Postgres databases.

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

A connection for machines at Hetzner. It has no `project`, and its Secret holds the keys that cloud names (here `token`). For AWS, which is also a vendor of buckets, say `purposes: [Compute]` and keep the connection apart from the one above.

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
| `spec.provider` | string | none | Required. `AWS`, `PlanetScale`, `GCP`, `DigitalOcean`, `Hetzner`, `OVHcloud`, `Vultr`, `Linode`, `Scaleway`, `Azure`, `OCI`, `UpCloud` or `Webhook` | The vendor. Every one except `PlanetScale` can create machines. |
| `spec.secretRef.name` | string | none | Required | A Secret in `geass-system` with the credentials. |
| `spec.project` | string | none | none | The project ID that may use the connection. Required in practice for buckets, backups and databases, and **must be empty** for `Compute`. See the rule below. |
| `spec.org` | string | none | `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, at most 63 characters | A [GeassOrganization](/docs/reference/resources/geass-organization/) that owns the connection, when it names no project: every project of that organization may use it, and no other. Ignored when `spec.project` is set. Not for `Compute`, which belongs to the cluster. |
| `spec.region` | string | none | none | The connection's default region: an AWS region for buckets and backups, and for machines the cloud's own place (a location, zone or region), used when a pool or a server names none. |
| `spec.organization` | string | none | none | The PlanetScale organization. When empty, the Secret's `organization` key is used. PlanetScale only. |
| `spec.purposes` | list | See below | At most 3. Each is `Storage`, `Database` or `Compute`. A set | What the connection is for. |

### Purposes

What a connection is for decides what may use it. Left out, `spec.purposes` means what the provider was for before purposes existed: `Storage` and `Database` for `AWS`, `Database` for `PlanetScale`, and `Compute` for every other cloud. So an AWS connection that predates purposes never creates machines, and one for a cloud that creates machines is for `Compute` without saying so. A connection for `Compute` creates and deletes machines for pools and servers. It must not be scoped to a project.

### Scope

A connection for buckets, backups or databases belongs to a project or to an organization, and every use checks that it belongs to the project that is using it:

- A connection that names a project (`spec.project`) is that project's alone. A project that names another project's connection gets `cloud connection "<name>" is scoped to project "<project>"`.
- A connection with no project that names an organization (`spec.org`) may be used by every project of that organization, and by no other. A project of another organization, or of none, gets `cloud connection "<name>" belongs to an organization that project "<project>" is not in`.
- A connection with neither cannot be used by anything. The error is `cloud connection "<name>" must be scoped to a project or an organization`.

So there is no platform-wide connection: create one per project, or one per organization. The dashboard also refuses to save a connection without a project or an organization (`org`, by a platform administrator or an administrator of the whole organization for their own). Any project of the organization can use the credentials of an organization's connection, so keep them to what its projects may all touch.

A connection for `Compute` is the opposite. It belongs to the cluster, never to a project: a pool or a server that names one with a `spec.project` or a `spec.org` is refused (`cloud connection "<name>" belongs to project "<project>", and a project's connection cannot create machines for the cluster`), and so is one that lacks the purpose (`cloud connection "<name>" is not for compute: add Compute to its purposes`). Only an admin can make one, change one or use one. Because storage must belong to a project or an organization and machines must not, one AWS connection cannot serve both: make two.

### The Secret

| Provider | Keys | Required |
| --- | --- | --- |
| AWS | `accessKeyId`, `secretAccessKey` | Both |
| AWS | `region` | No. For buckets the default region is `spec.region`, then the Secret's `region`, then `us-east-1`. For machines it is the Secret's `region`, then `spec.region`, then `us-east-1` |
| PlanetScale | `token` | Yes |
| PlanetScale | `organization` | Yes, unless `spec.organization` is set |
| Each other cloud | The keys that cloud names: see [the table](/docs/guides/connectors/clouds/#the-clouds) | The ones that are not marked optional |

The dashboard and `geass connections add` create the Secret `<name>-credentials`, after the connection. If a Secret with that name already exists, the AWS card keeps the old one and ignores new credentials; see [Connect AWS](/docs/guides/connectors/aws/#rotate-aws-credentials). **Edit** under **Cloud connections** and `geass connections update` replace credentials in it. When [organizations are on](/docs/guides/organizations-and-workspaces/), a connection made by an organization is refused instead ("that name is already taken"), so that it cannot adopt a Secret that is not its own.

## Status

| Field | Type | Meaning |
| --- | --- | --- |
| `status.available` | boolean | `true` when the Secret exists and has the keys the provider needs, and, for a connection for `Compute`, the cloud accepted the credentials. |
| `status.conditions` | list | One condition, `Ready`. |
| `status.account` | string | The account, project or tenant the credentials act on, as the cloud reports it. For `Compute` only. |
| `status.message` | string | What the last check found, or why it could not tell. |
| `status.verifiedAt` | date-time | When the cloud last accepted the credentials. |
| `status.expiresAt` | date-time | When the credentials stop working, for the clouds that say (OVHcloud's project, a webhook that reports it). |
| `status.warnings` | list | Things that work now and are likely to fail later, at most 10. See [Cloud connections for machines](/docs/guides/connectors/clouds/#what-the-connection-says-about-itself). |

| Condition | Status | Reason | Meaning |
| --- | --- | --- | --- |
| `Ready` | `True` | `CredentialsReady` | The Secret has the required keys. The message is `Cloud connection is ready`, or, for `Compute`, `The provider accepted the credentials`. |
| `Ready` | `False` | `CredentialsMissing` | `spec.secretRef.name` is empty (`A credentials secret is required`), the Secret does not exist (`Connection credentials are unavailable`), or it lacks a key (`AWS access key and secret key are required`, `PlanetScale token and organization are required`, or `<cloud> needs <the missing fields>`). |
| `Ready` | `False` | `VerificationFailed` | `Compute` only: the cloud refused the credentials, or could not be reached. The message is the cloud's. |
| `Ready` | `False` | `UnsupportedProvider` | The provider is not one Geass knows. The CRD does not allow that. |

:::note
For a connection that is not for `Compute`, `Ready` means the keys are present, not that they work. Geass does not call AWS or PlanetScale to test a connection for buckets, backups or databases, and a wrong key shows up when one first uses it. A connection for `Compute` is different: the cloud is asked, and the answer is what `Ready` shows. Even then, most clouds cannot say without creating something whether the credentials may create a machine, so a connection can be `Ready` and the first machine can still fail (a warning says so).
:::

## How the dashboard maps to it

| Dashboard | Effect |
| --- | --- |
| **Settings** → **Connectors** → **AWS** or **PlanetScale** → **Save connection** | Creates the Secret `<name>-credentials` and the `GeassCloudConnection`, with `spec.project` from **Project scope** and `spec.region` or `spec.organization` from the form. |
| **Settings** → **Connectors** → **Cloud connections** → **Connect a cloud** | Creates the Secret and a connection with no project and the purpose `Compute` (for AWS also `Storage` and `Database` if you tick them), then asks the cloud. |
| **Verify now**, **Edit** and **Remove** on a connection in the **Cloud connections** list | Ask the cloud again, change the default region, purposes or credentials, and delete the connection and its Secret. **Remove** refuses only while a pool or a machine uses the connection. It does not look for buckets, backups or databases. |
| The connectors list | Shows each connection with its project (`Platform` if empty) and **Ready** or **Pending** from `status.available`. |
| The **Bucket** and **Database** menus, and project **Settings** → **Backups** | Offer a provider only when the project has a connection of that kind with `status.available` true. |

The REST endpoints are `POST /api/cloud-connections/create` for a project's AWS or PlanetScale connection, and `GET`, `POST`, `PATCH` and `DELETE /api/v1/connections` for the rest (see [REST API](/docs/reference/rest-api/#machines-clouds-pools-and-servers)).

## What uses a connection

| User | Field | Provider |
| --- | --- | --- |
| [GeassObjectStore](/docs/reference/resources/geass-object-store/) | `spec.connectionRef` | AWS |
| [GeassProject](/docs/reference/resources/geass-project/) backup storage, and the [GeassBackupArchive](/docs/reference/resources/geass-backup-archive/) it creates | `spec.backupStorage.connectionRef` | AWS |
| [GeassDatabase](/docs/reference/resources/geass-database/) | `spec.connectionRef` | PlanetScale (to create a MySQL or Postgres database), AWS (checked only) |
| [GeassNodePool](/docs/reference/resources/geass-node-pool/) | `spec.connectionRef` | Any cloud that creates machines. The connection must be for `Compute` |
| [GeassNode](/docs/reference/resources/geass-node/) | `spec.cloud.connectionRef` | The same |
| [GeassCluster](/docs/reference/resources/geass-cluster/) | `spec.network.controlPlaneFirewall.connectionRef` | AWS or Hetzner, to open the control plane's firewall for new machines |

## Editing by hand

It is safe to create and edit a `GeassCloudConnection` with `kubectl`, and to edit its Secret. Changing the Secret is how you rotate credentials. Delete a connection only after the buckets, backups, databases, pools and machines that use it are gone: deleting an AWS bucket or purging a backup needs the connection, and deleting a machine needs it too. Without it they cannot finish, and a machine that nothing can delete keeps billing. (`kubectl delete` does not check this. The dashboard, the command line and the API refuse to remove a connection that a pool or a machine uses.)

## What happens under the hood

A controller watches the connections, reads the Secret, and sets `status` and the `Ready` condition. It writes the status only when it differs from the one stored. For a connection for `Compute` it also builds the cloud's driver and calls its check, with a 30 second limit. It trusts the answer for 15 minutes unless the connection, its Secret or the annotation `geass.dev/recheck` changes, asks again after 2 minutes when the cloud did not accept the credentials, and from 14 days before `status.expiresAt` (or after it) adds a warning that the credentials expire or have expired. It never calls AWS or PlanetScale for a connection that is only for buckets, backups or databases.

It watches the connection objects and not their Secrets, so a change to a Secret does not update `status` until the connection is reconciled again: at the next of those checks, or at once for any change to the connection, such as `kubectl annotate geasscloudconnection prod-aws geass.dev/recheck="$(date +%s)" --overwrite`. `geass connections verify NAME` sets that annotation. The object store, database and backup controllers read the connection and its Secret each time they reconcile, and the node and pool controllers build the cloud's driver from them and keep it until either changes. There is no finalizer, and the Secret has no owner reference, so deleting a connection (or the project, which deletes its connections) leaves the Secret behind, unless the dashboard or `geass connections remove` deleted it.

```bash title="Inspect connections"
kubectl -n geass-system get geasscloudconnections
kubectl -n geass-system get geasscloudconnection prod-aws -o jsonpath='{.status.available}{" "}{.status.conditions[0].reason}{"\n"}'
kubectl -n geass-system get geasscloudconnection hetzner -o jsonpath='{.status.account}{"  "}{.status.message}{"\n"}'
```

## Troubleshooting

**The connection is `Pending` with `CredentialsMissing`.** Read the message. The Secret named in `spec.secretRef.name` is missing or lacks `accessKeyId` and `secretAccessKey` (AWS), `token` and `organization` (PlanetScale), or one of the fields of the cloud. If you created or fixed the Secret after the connection, the status does not change by itself for a while: annotate the connection as shown under [What happens under the hood](#what-happens-under-the-hood), or run `geass connections verify NAME`.

**`VerificationFailed`.** The cloud refused the credentials, or could not be reached. The message is the cloud's: see [Cloud connections for machines](/docs/guides/connectors/clouds/#troubleshooting).

**`cloud connection "x" is scoped to project "y"`.** The resource that named the connection belongs to another project. Create a connection for it.

**`must be scoped to a project`.** `spec.project` is empty on a connection that a bucket, backup or database uses. Set it to the project ID (`proj-...`), not the display name.

**`is not for compute` or `belongs to project`.** A pool or a server named a connection that lacks the purpose `Compute`, or is scoped to a project. An AWS connection made for buckets is the usual one: make a separate AWS connection for machines.

**The credentials are wrong but the connection shows `Ready`.** Expected for a connection for buckets, backups or databases, see the note above. Deploy a bucket in a scratch project to test an AWS key.

**New credentials are ignored after I saved the connection again.** The Secret already existed. Edit the Secret, or use **Edit** under **Cloud connections**.

## Related

- [Cloud connectors](/docs/guides/connectors/)
- [Cloud connections for machines](/docs/guides/connectors/clouds/)
- [GeassNodePool](/docs/reference/resources/geass-node-pool/)
- [GeassObjectStore](/docs/reference/resources/geass-object-store/)
- [GeassDatabase](/docs/reference/resources/geass-database/)
- [GeassBackupArchive](/docs/reference/resources/geass-backup-archive/)
- [GeassProject](/docs/reference/resources/geass-project/)
- [Resources overview](/docs/reference/resources/overview/)
