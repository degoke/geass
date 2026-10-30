---
title: "Connect AWS"
description: "Connect AWS to Geass: the IAM permissions it needs, how to add, test, rotate, and remove the connection, and what Geass creates in AWS."
sidebar:
  label: AWS
---

Use an AWS connection if a project needs S3 storage in your own AWS account, or if you want database backups there.
Geass uses an AWS connection for three things:

- **Buckets.** A **Bucket** resource with the AWS placement creates S3 buckets, or attaches to S3 buckets. It gives
  your app credentials that can reach only those buckets. See [Object storage](/docs/guides/object-storage/).
- **Backups.** A project keeps its Postgres, MySQL, and SQLite backups in an S3 bucket. See
  [Backups and restore](/docs/guides/backups-and-restore/).
- **AWS RDS databases.** The **Database** menu lists **AWS RDS**, but Geass has no RDS client. Geass stores the host,
  port, user, and password that you give it, and it builds the connection Secret. Geass never creates, resizes, or
  deletes an RDS instance. Treat this option as "attach an existing database".

This page is about the AWS connection for buckets and backups. That connection belongs to one project. Geass can also
create **EC2 machines** in your AWS account, for [node pools](/docs/guides/node-pools/). EC2 machines need a different
connection, with the purpose Compute. That connection belongs to the cluster, and it has its own key and policy. See
[Cloud connections for machines](/docs/guides/connectors/clouds/#amazon-ec2).

A connection that you make as this page describes never creates machines. The key on this page cannot launch
instances. This connection also takes only the key of an IAM user. It does not read the session token of temporary
credentials, for example the credentials that an assumed role gets. The connection for machines reads that session
token. See [Temporary credentials](/docs/guides/connectors/clouds/#temporary-credentials).

## Before you start

You need these things:

- An AWS account, and an IAM user that is only for Geass. The next section lists the permissions that the user needs.
- The ID of the project that uses the connection. The ID starts with `proj-`. A connection belongs to one project.
- An admin login in Geass. Only admins can add, change, or remove connections.

## Permissions the AWS key needs

Geass signs the S3 and IAM calls itself. The operator does not contain an AWS SDK. This table lists all of the calls
that Geass makes with the connection key, so these permissions are the minimum:

| Service | Actions | Why |
| --- | --- | --- |
| S3 | `s3:CreateBucket` | Create a bucket. Geass counts an HTTP 409 reply as "already exists". |
| S3 | `s3:ListBucket`, `s3:ListBucketVersions`, `s3:ListBucketMultipartUploads` | Find what to delete. |
| S3 | `s3:DeleteObject`, `s3:DeleteObjectVersion`, `s3:AbortMultipartUpload`, `s3:DeleteBucket` | Empty and delete a bucket, or purge a backup folder. |
| IAM | `iam:CreateUser`, `iam:PutUserPolicy`, `iam:CreateAccessKey` | Create the per-bucket user, its policy, and its key. |
| IAM | `iam:ListAccessKeys`, `iam:DeleteAccessKey`, `iam:DeleteUserPolicy`, `iam:DeleteUser` | Remove that user again. |

This policy grants exactly these permissions. Replace the account ID in the policy. If you can, also narrow the S3
resources to a bucket-name prefix that you control.

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

To prepare the key, do these steps in AWS:

1. Create an IAM user that is only for Geass.
2. Attach this policy to the user.
3. Create one access key for the user.

Geass names every user that it creates `geass-...`. The IAM statement in the policy matches these names.

## Add an AWS connection

1. Open **Settings** → **Connectors**.
2. On the **AWS** card, click **Configure** or **Manage**.
3. Enter a **Name**, for example `prod-aws`. This value becomes the name of the connection.
4. Select the **Project** by its name. Geass saves the connection under the ID of the project. The ID starts with
   `proj-`, and it appears in the address of the project (`/projects/proj-1a2b3c4d`). The API and
   `kubectl -n geass-system get geassprojects` use the ID.
5. Enter the **Access key ID**, the **Secret access key**, and the **Region**. The **Region** field starts as `us-east-1`.
6. Click **Save connection**.

The connection is limited to that project. If another project names the connection, that project gets "cloud
connection is scoped to project". Thus, add one connection for each project. There is no unscoped (platform-wide)
connection:

- Geass refuses a connection with an empty project scope, with "project scope is required for cloud connections".
- If you create a connection with `kubectl` without a `project`, the connection shows **Ready**. But every bucket and
  database that names the connection refuses it, with "must be scoped to a project".

Geass checks the project before it writes anything. Thus, a save that fails with "project was not found" or "project
scope is required" leaves nothing behind. Before Geass did this check, such a save left the Secret
`<name>-credentials`. If that Secret is still there from an earlier attempt, see the caution under
[Rotate AWS credentials](#rotate-aws-credentials).

You can also create the connection with the API or with `kubectl`. For the API, use an admin session or a workspace
token with `admin` access.

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

When you save the connection, Geass does not call AWS. The connection controller only checks that the Secret has an
access key ID and a secret access key. Then the controller sets the `Ready` condition and `status.available`. The card
and the table show **Ready** or **Pending** from that check. A wrong or revoked key shows **Ready** until something
uses the key.

A connection with the purpose Compute is different. For that connection, Geass asks AWS whether AWS accepts the key,
as [Cloud connections for machines](/docs/guides/connectors/clouds/) describes.

The first real AWS call occurs when you deploy a bucket or turn on backups. If the key is wrong, the bucket or the
database shows the failure as its message, for example `AWS S3 403 Forbidden` or `AWS IAM AccessDenied`. To test a key
before you rely on it, create a bucket in a scratch project and deploy the bucket.

## What Geass creates in AWS

For a bucket with **Create bucket in AWS** on, Geass creates these resources:

- The S3 buckets that you named. Geass creates them in the region of the bucket resource. If the bucket resource has
  no region, Geass uses the region of the connection. If neither has a region, Geass uses `us-east-1`.
- An IAM user named `geass-<bucket resource name>` with one inline policy, `geass-bucket`. The policy allows list,
  read, write, and delete on those buckets only (`s3:ListBucket`, `s3:GetBucketLocation`,
  `s3:ListBucketMultipartUploads`, `s3:ListBucketVersions`, `s3:GetObject`, `s3:PutObject`, `s3:DeleteObject`,
  `s3:DeleteObjectVersion`, `s3:AbortMultipartUpload`, `s3:ListMultipartUploadParts`).
- One access key for that user. Geass stores the key in the Secret `<bucket resource name>-connection` in the
  namespace of the environment (`<project>-<environment>`). Your apps receive this key. Geass does not give the
  connection key to apps.

If **Create bucket in AWS** is off, Geass creates only the IAM user and the key. The bucket must already exist.

For project backups, Geass creates these resources:

- A bucket named `geass-backups-<12 hex characters>`, unless you name a bucket when you save the backup storage.
  Bucket names are global, so the generated name is random.
- An IAM user named `geass-<bucket name>` with the same `geass-bucket` policy. Geass stores one access key for that
  user in the Secret `geass-backup-storage-<bucket name>` in `geass-system`.
- Objects in that bucket, under one folder for each database.

## Rotate AWS credentials

To replace the credentials of the connection, click **Edit** on the connection under **Settings** → **Connectors** →
**AWS**. Enter only the values that change. You can also replace the values in the Secret of the connection yourself:

```bash title="Rotate the connection key"
kubectl -n geass-system create secret generic prod-aws-credentials \
  --from-literal=accessKeyId=AKIAEXAMPLEEXAMPLE \
  --from-literal=secretAccessKey=replace-with-the-new-secret-access-key \
  --from-literal=region=eu-north-1 \
  --dry-run=client -o yaml | kubectl apply -f -
```

Geass reads the Secret each time that it reconciles. Thus, Geass uses the new key the next time that a bucket or a
backup needs AWS. Running apps are not affected, because they use the per-bucket keys. Geass keeps the per-bucket keys
in their own Secrets. After the new key works, deactivate the old key in IAM.

:::caution
Do not try to rotate the key by saving the connection again with the same name. If the Secret `<name>-credentials`
already exists, Geass keeps that Secret and ignores the new keys. The old credentials stay in use. This rule also
applies to a Secret that an earlier failed save left behind. To fix a mistyped key, edit the Secret as the command above
shows. Do not save again. You can also create a connection with a new name. If
[organizations are on](/docs/guides/organizations-and-workspaces/), Geass refuses the save from an organization with
"that name is already taken" instead. An organization cannot take a Secret that is not its own.
:::

## Remove an AWS connection

Remove the connection after you delete the buckets and backups that use it. Geass needs the connection to delete an
AWS bucket. If you remove the connection too early, the bucket stays stuck in deletion with "AWS connection is
unavailable".

You can click **Remove** on the connection under **Settings** → **Connectors** → **AWS**, or call
`DELETE /api/v1/connections/{name}`. Geass refuses to remove the connection while one of these still uses it, and it
tells you which one:

- A pool.
- A machine.
- An object store.
- A database.
- The backups of a deleted database.
- The backup bucket of a project.
- The control plane firewall.

`kubectl` does not do this check. With `kubectl`, you can remove a connection that these resources still need:

```bash title="Remove a connection"
kubectl -n geass-system delete geasscloudconnection prod-aws
kubectl -n geass-system delete secret prod-aws-credentials
```

Backups also need the connection. When the 7-day grace period of a database ends, Geass uses the connection to purge
the backup folder. If the connection is gone, the archive reports `ConnectionUnavailable` and continues to retry.

When you delete a project, Geass deletes its connections in the same pass as its buckets and databases. Thus, delete
the AWS buckets first, and wait until they are gone. Then delete the project. If you do not, an external bucket can
stay in deletion with no credentials to authenticate with. Geass never deletes the credentials Secrets
(`<name>-credentials`), so remove them yourself afterwards.

## What is cleaned up on AWS

| You delete | Removed from AWS | Left behind |
| --- | --- | --- |
| A bucket with **Create bucket in AWS** on | The buckets (first every object, version, and unfinished upload), then the IAM user, its keys, and its policy | Nothing |
| A bucket with **Create bucket in AWS** off | The IAM user, its keys, and its policy | The bucket and its objects |
| A database with backups | After 7 days, the objects in the backup folder of that database | The backup bucket and the `geass-<bucket>` IAM user |
| Project backup storage (**Remove**) | Nothing | The bucket, the objects, and the IAM user |
| The connection | Nothing | Everything that Geass created |

If you turn off backups for a database, Geass also retires its backups after 7 days. Geass never deletes the backup
bucket or the backup IAM user. Remove them yourself when you stop using them.

:::note
Compliance tags keep the workloads of a project on tagged nodes, but the tags stop at the edge of the cluster. If a
project requires node tags, its backup and restore jobs run on those nodes. But the jobs write to your S3 bucket. A
bucket on AWS S3 or a PlanetScale database is outside the cluster. Use buckets and regions that meet the same
requirement. See [Compliance tags](/docs/guides/compliance-tags/).
:::

## What happens under the hood

- An AWS connection is a `GeassCloudConnection` in `geass-system`. Its credentials are in the Secret
  `<name>-credentials`. See [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/).
- The connection controller sets `Ready` to `True` with reason `CredentialsReady`. Or it sets `Ready` to `False` with
  `CredentialsMissing` or `UnsupportedProvider`.
- The object store, database, and backup controllers read the connection and its Secret when they reconcile. They
  also check that the connection belongs to the project of the resource.

```bash title="Inspect AWS connections"
kubectl -n geass-system get geasscloudconnections
kubectl -n geass-system get secret prod-aws-credentials
```

## Troubleshooting

**The connection stays Pending.** The Secret does not have `accessKeyId` or `secretAccessKey`. The message reads "AWS
access key and secret key are required".

**The AWS option is grayed out in the resource menu.** The menu offers **AWS RDS** only when the project has a ready
AWS connection. The text reads "Add an AWS connection in platform settings".

**A bucket fails with `AWS IAM ... AccessDenied` or `AWS S3 403`.** The connection key does not have one of the
permissions in [Permissions the AWS key needs](#permissions-the-aws-key-needs).

**A bucket reports ready but the app cannot read it.** Geass treats an S3 `409` as "bucket exists". This rule includes
a bucket name that another AWS account owns. Bucket names are global, so use a distinctive name.

**A bucket is stuck deleting with "AWS connection is unavailable".** The connection or its Secret was removed before
the bucket. Recreate the connection with the same name and the same Secret keys. Then the deletion completes.

For the errors that apply to every connection that is saved for a project, see
[Cloud connectors](/docs/guides/connectors/#troubleshooting). These errors include "project was not found",
"connection already exists", and "scoped to project".

## Related

- [Cloud connectors](/docs/guides/connectors/)
- [Object storage](/docs/guides/object-storage/)
- [Backups and restore](/docs/guides/backups-and-restore/)
- [Databases](/docs/guides/databases/)
- [Compliance tags](/docs/guides/compliance-tags/)
- [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/)
- [Security](/docs/operations/security/)
