---
title: "GeassCloudConnection"
description: "Reference for GeassCloudConnection: a project's AWS or PlanetScale credentials, kept in a Secret, that buckets, backups and external databases use."
sidebar:
  order: 10
---

A `GeassCloudConnection` tells Geass which Secret holds a project's credentials for AWS or PlanetScale. Buckets in your AWS account, database backups and PlanetScale databases use it. The resource holds no secrets: it points at a Secret in `geass-system` and records whether that Secret has what the provider needs.

The dashboard creates one when you save a connection in **Settings** → **Connectors**, together with the Secret. The task-oriented view, with the permissions each provider needs and what Geass creates in your account, is in [Cloud connectors](/docs/guides/connectors/). Cloudflare and GitHub are not `GeassCloudConnection` objects: Cloudflare lives on the [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/), and GitHub on a [GeassGitHubConnection](/docs/reference/resources/geass-github-connection/).

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

An AWS connection and its Secret. The Secret keys are `accessKeyId`, `secretAccessKey` and `region`.

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

## Spec fields

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.provider` | string | none | Required. `AWS` or `PlanetScale` | The vendor. |
| `spec.secretRef.name` | string | none | Required | A Secret in `geass-system` with the credentials. |
| `spec.project` | string | none | none | The project ID that may use the connection. See the rule below. |
| `spec.region` | string | none | none | The default AWS region. Used when a bucket or backup storage names none. AWS only. |
| `spec.organization` | string | none | none | The PlanetScale organization. When empty, the Secret's `organization` key is used. PlanetScale only. |

### Scope

Every use of a connection checks that `spec.project` names the project that is using it:

- A connection with an empty `spec.project` cannot be used by anything. The error is `cloud connection "<name>" must be scoped to a project`, or `use a project-scoped cloud connection` in the dashboard.
- A project that names another project's connection gets `cloud connection "<name>" is scoped to project "<project>"`.

So there is no platform-wide connection: create one per project. The dashboard also refuses to save a connection without a project.

### The Secret

| Provider | Keys | Required |
| --- | --- | --- |
| AWS | `accessKeyId`, `secretAccessKey` | Both |
| AWS | `region` | No. The default region is `spec.region`, then the Secret's `region`, then `us-east-1` |
| PlanetScale | `token` | Yes |
| PlanetScale | `organization` | Yes, unless `spec.organization` is set |

The dashboard creates the Secret `<name>-credentials`. If a Secret with that name already exists, it keeps the old one and ignores new credentials; see [Connect AWS](/docs/guides/connectors/aws/#rotate-aws-credentials).

## Status

| Field | Type | Meaning |
| --- | --- | --- |
| `status.available` | boolean | `true` when the Secret exists and has the keys the provider needs. |
| `status.conditions` | list | One condition, `Ready`. |

| Condition | Status | Reason | Meaning |
| --- | --- | --- | --- |
| `Ready` | `True` | `CredentialsReady` | The Secret has the required keys. The message is `Cloud connection is ready`. |
| `Ready` | `False` | `CredentialsMissing` | `spec.secretRef.name` is empty (`A credentials secret is required`), the Secret does not exist (`Connection credentials are unavailable`), or it lacks a key (`AWS access key and secret key are required`, `PlanetScale token and organization are required`). |
| `Ready` | `False` | `UnsupportedProvider` | The provider is not `AWS` or `PlanetScale`. The CRD does not allow that. |

:::note
`Ready` means the keys are present, not that they work. Geass does not call AWS or PlanetScale to test a connection. A wrong key shows up when a bucket, backup or database first uses it.
:::

## How the dashboard maps to it

| Dashboard | Effect |
| --- | --- |
| **Settings** → **Connectors** → **AWS** or **PlanetScale** → **Save connection** | Creates the Secret `<name>-credentials` and the `GeassCloudConnection`, with `spec.project` from **Project scope** and `spec.region` or `spec.organization` from the form. |
| The connectors list | Shows each connection with its project (`Platform` if empty) and **Ready** or **Pending** from `status.available`. |
| The **Bucket** and **Database** menus, and project **Settings** → **Backups** | Offer a provider only when the project has a connection of that kind with `status.available` true. |

The dashboard has no edit or delete for a connection, and a read-only user does not see connections at all. The REST endpoint is `POST /api/cloud-connections/create` (see [REST API](/docs/reference/rest-api/#cluster-and-platform)).

## What uses a connection

| User | Field | Provider |
| --- | --- | --- |
| [GeassObjectStore](/docs/reference/resources/geass-object-store/) | `spec.connectionRef` | AWS |
| [GeassProject](/docs/reference/resources/geass-project/) backup storage, and the [GeassBackupArchive](/docs/reference/resources/geass-backup-archive/) it creates | `spec.backupStorage.connectionRef` | AWS |
| [GeassDatabase](/docs/reference/resources/geass-database/) | `spec.connectionRef` | PlanetScale (to create a MySQL or Postgres database), AWS (checked only) |

## Editing by hand

It is safe to create and edit a `GeassCloudConnection` with `kubectl`, and to edit its Secret. Changing the Secret is how you rotate credentials. Delete a connection only after the buckets, backups and databases that use it are gone: deleting an AWS bucket or purging a backup needs the connection, and without it they cannot finish.

## What happens under the hood

A controller watches the connections, reads the Secret, and sets `status` and the `Ready` condition. It writes the status only when it differs from the one stored. It never calls the provider. It watches the connection objects and not their Secrets, so a change to a Secret does not update `status` until the connection is reconciled again. Any change to the connection does that, such as `kubectl annotate geasscloudconnection prod-aws geass.dev/recheck="$(date +%s)" --overwrite`. The object store, database and backup controllers read the connection and its Secret each time they reconcile. There is no finalizer, and the Secret has no owner reference, so deleting a connection (or the project, which deletes its connections) leaves the Secret behind.

```bash title="Inspect connections"
kubectl -n geass-system get geasscloudconnections
kubectl -n geass-system get geasscloudconnection prod-aws -o jsonpath='{.status.available}{" "}{.status.conditions[0].reason}{"\n"}'
```

## Troubleshooting

**The connection is `Pending` with `CredentialsMissing`.** Read the message. The Secret named in `spec.secretRef.name` is missing or lacks `accessKeyId` and `secretAccessKey` (AWS), or `token` and `organization` (PlanetScale). If you created or fixed the Secret after the connection, the status does not change by itself: annotate the connection as shown under [What happens under the hood](#what-happens-under-the-hood).

**`cloud connection "x" is scoped to project "y"`.** The resource that named the connection belongs to another project. Create a connection for it.

**`must be scoped to a project`.** `spec.project` is empty. Set it to the project ID (`proj-...`), not the display name.

**The credentials are wrong but the connection shows `Ready`.** Expected, see the note above. Deploy a bucket in a scratch project to test an AWS key.

**New credentials are ignored after I saved the connection again.** The Secret already existed. Edit the Secret.

## Related

- [Cloud connectors](/docs/guides/connectors/)
- [GeassObjectStore](/docs/reference/resources/geass-object-store/)
- [GeassDatabase](/docs/reference/resources/geass-database/)
- [GeassBackupArchive](/docs/reference/resources/geass-backup-archive/)
- [GeassProject](/docs/reference/resources/geass-project/)
- [Resources overview](/docs/reference/resources/overview/)
