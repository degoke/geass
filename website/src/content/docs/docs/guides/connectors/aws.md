---
title: "Connect AWS"
description: "Connect AWS to Geass: the IAM permissions it needs, how to add, test, rotate and remove the connection, and what Geass creates in your AWS account."
sidebar:
  label: AWS
---

Use an AWS connection when a project needs S3 storage that lives in your own AWS account, or when you want
database backups there. Geass supports three things with it:

- **Buckets.** A **Bucket** resource with the AWS placement creates (or attaches to) S3 buckets and hands your app
  credentials that can reach only those buckets. See [Object storage](/docs/guides/object-storage/).
- **Backups.** A project keeps its Postgres, MySQL and SQLite backups in an S3 bucket. See
  [Backups and restore](/docs/guides/backups-and-restore/).
- **AWS RDS databases.** The **Database** menu lists **AWS RDS**, but Geass has no RDS client. It stores the host,
  port, user and password you give it and builds the connection Secret. It never creates, resizes or deletes an RDS
  instance. Treat it as "attach an existing database".

This page is about the AWS connection for buckets and backups, which belongs to one project. Geass can also create **EC2
machines** in your AWS account, for [node pools](/docs/guides/node-pools/). That needs a different connection, with the purpose
Compute, that belongs to the cluster and has its own key and policy: see [Cloud connections for machines](/docs/guides/connectors/clouds/#amazon-ec2).
A connection made as described below never creates machines, and the key on this page cannot launch instances.

## Before you start

- An AWS account, and an IAM user that is only for Geass. The next section lists the permissions it needs.
- The ID of the project that will use the connection. It starts with `proj-`, and a connection belongs to one project.
- An admin login in Geass. Only admins can add, change or remove connections.

## Permissions the AWS key needs

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

## Add an AWS connection

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

## Test an AWS connection

Saving does not call AWS. The connection controller only checks that the Secret has an access key ID and a secret
access key, then sets the `Ready` condition and `status.available`. The card and the table show **Ready** or
**Pending** from that check. A wrong or revoked key looks **Ready** until something uses it. (A connection with the purpose
Compute is different: Geass asks AWS whether it accepts that key, as [Cloud connections for machines](/docs/guides/connectors/clouds/) says.)

The first real AWS call happens when you deploy a bucket or turn on backups. If the key is wrong, the failure
appears as the message on that bucket or database (for example `AWS S3 403 Forbidden` or `AWS IAM AccessDenied`).
To test a key before you rely on it, create a bucket in a scratch project and deploy it.

## What Geass creates in AWS

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

## Rotate AWS credentials

Under **Settings** → **Connectors** → **Cloud connections**, **Edit** on the connection replaces its credentials: type only the
values that change. You can also replace the values in the connection's Secret yourself:

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

## Remove an AWS connection

Remove it after you delete the buckets and backups that use it. Deleting an AWS bucket needs the connection, so a
connection you remove too early leaves the bucket stuck deleting with "AWS connection is unavailable". **Remove** on the
connection under **Settings** → **Connectors** → **Cloud connections** (or `DELETE /api/v1/connections/{name}`) refuses
while a pool, a machine, an object store, a database, the backups of a deleted database, a project's backup bucket or the
control plane firewall still uses it, and says which. `kubectl` does not check, so it will let you remove a connection that
they still need:

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

## What is cleaned up on AWS

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

## What happens under the hood

- An AWS connection is a `GeassCloudConnection` in `geass-system`, with its credentials in the Secret
  `<name>-credentials`. See [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/).
- The connection controller sets `Ready` to `True` with reason `CredentialsReady`, or `False` with `CredentialsMissing` or
  `UnsupportedProvider`.
- The object store, database and backup controllers read the connection and its Secret when they reconcile. They also
  check that the connection belongs to the resource's project.

```bash title="Inspect AWS connections"
kubectl -n geass-system get geasscloudconnections
kubectl -n geass-system get secret prod-aws-credentials
```

## Troubleshooting

**The connection stays Pending.** The Secret is missing `accessKeyId` or `secretAccessKey`. The message reads "AWS access key
and secret key are required".

**The AWS option is greyed out in the resource menu.** The menu offers **AWS RDS** only when the project has a ready AWS
connection. The text reads "Add an AWS connection in platform settings".

**A bucket fails with `AWS IAM ... AccessDenied` or `AWS S3 403`.** The connection key lacks one of the permissions in
[Permissions the AWS key needs](#permissions-the-aws-key-needs).

**A bucket reports ready but the app cannot read it.** Geass treats an S3 `409` as "bucket exists", which includes a
bucket name owned by another AWS account. Bucket names are global; pick a distinctive name.

**A bucket is stuck deleting with "AWS connection is unavailable".** The connection or its Secret was removed before the
bucket. Recreate the connection with the same name and Secret keys, and the deletion completes.

Errors that apply to any connection saved for a project ("project was not found", "connection already exists", "scoped to project") are
listed in [Cloud connectors](/docs/guides/connectors/#troubleshooting).

## Related

- [Cloud connectors](/docs/guides/connectors/)
- [Object storage](/docs/guides/object-storage/)
- [Backups and restore](/docs/guides/backups-and-restore/)
- [Databases](/docs/guides/databases/)
- [Compliance tags](/docs/guides/compliance-tags/)
- [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/)
- [Security](/docs/operations/security/)
