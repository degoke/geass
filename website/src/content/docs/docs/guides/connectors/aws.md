---
title: "Connect AWS"
description: "Connect AWS to Geass for S3 buckets and database backups: the IAM permissions it needs, how to add, test, rotate, and remove the connection."
sidebar:
  label: AWS
---

Use an AWS connection when a project needs S3 storage in your own AWS account, or when you want database backups
there. Geass uses an AWS connection for these tasks:

- **Buckets.** A bucket with the AWS placement creates S3 buckets, or attaches to S3 buckets that exist. Your app gets
  credentials that can reach only those buckets. See [Object storage](/docs/guides/object-storage/).
- **Backups.** A project keeps its Postgres, MySQL, and SQLite backups in an S3 bucket. See
  [Backups and restore](/docs/guides/backups-and-restore/).
- **AWS RDS databases.** The **Database** menu lists **AWS RDS**. Geass stores the host, port, user, and password that
  you give it. Geass never creates, resizes, or deletes an RDS instance. Use this option to attach a database that
  exists.

This page is about the AWS connection for buckets and backups. That connection belongs to one project, or to one
organization. To let Geass create **EC2 machines**, make a separate connection under **Server clouds**, with its own
key and policy. See [Cloud connections for machines](/docs/guides/connectors/clouds/#amazon-ec2).

The connection on this page never creates machines. It also takes only the key of an IAM user. It does not take the
session token of temporary credentials. Only a connection for machines takes a session token. See
[Temporary credentials](/docs/guides/connectors/clouds/#temporary-credentials).

## Before you start

You need these things:

- An AWS account, and an IAM user that is only for Geass. The next section lists the permissions of that user.
- The project that uses the connection.
- An admin login in Geass.

## Permissions the AWS key needs

This table lists every call that Geass makes with the connection key. These permissions are the minimum:

| Service | Actions | Why |
| --- | --- | --- |
| S3 | `s3:CreateBucket` | Create a bucket. Geass counts an HTTP 409 reply as "already exists". |
| S3 | `s3:ListBucket`, `s3:ListBucketVersions`, `s3:ListBucketMultipartUploads` | Find what to delete. |
| S3 | `s3:DeleteObject`, `s3:DeleteObjectVersion`, `s3:AbortMultipartUpload`, `s3:DeleteBucket` | Empty and delete a bucket, or remove a backup folder. |
| IAM | `iam:CreateUser`, `iam:PutUserPolicy`, `iam:CreateAccessKey` | Create the user for each bucket, its policy, and its key. |
| IAM | `iam:ListAccessKeys`, `iam:DeleteAccessKey`, `iam:DeleteUserPolicy`, `iam:DeleteUser` | Remove that user again. |

This policy grants exactly these permissions. Replace the account ID in the policy. If you can, also limit the S3
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

Geass names every IAM user that it creates `geass-...`. The IAM statement in the policy matches these names.

## Add an AWS connection

1. Open **Settings** → **Connectors**.
2. On the **AWS** card, click **Configure** or **Manage**.
3. Enter a **Name**, for example `prod-aws`.
4. Select the **Project**. If organizations are on, you can select an **Organization** instead. Then every project
   of that organization can use the connection.
5. Enter the **Access key ID**, the **Secret access key**, and the **Region**. The **Region** starts as `us-east-1`.
6. Click **Save connection**.

The connection works only for its project or organization. Add one connection for each project that needs AWS.

## Test an AWS connection

When you save the connection, Geass does not call AWS. Geass checks only that you entered both keys. So a wrong or
revoked key shows **Ready** until something uses it.

The first real AWS call occurs when you deploy a bucket or turn on backups. If the key is wrong, the bucket or the
database shows the failure as its message, for example `AWS S3 403 Forbidden` or `AWS IAM AccessDenied`. To test a key
before you rely on it, create a bucket in a scratch project, and deploy the bucket.

## What Geass creates in AWS

For a bucket with **Create bucket in AWS** on, Geass creates these resources:

- The S3 buckets that you named. Geass uses the region of the bucket. If the bucket has no region, Geass uses the
  region of the connection. If neither has a region, Geass uses `us-east-1`.
- An IAM user named `geass-<bucket name>` with one inline policy, `geass-bucket`. The policy allows list, read, write,
  and delete on those buckets only.
- One access key for that user. Your app receives this key. Geass never gives the connection key to an app.

If **Create bucket in AWS** is off, Geass creates only the IAM user and the key. The bucket must already exist.

For project backups, Geass creates these resources:

- A bucket named `geass-backups-<12 hex characters>`, unless you name a bucket when you save the backup storage.
  Bucket names are global, so the generated name is random.
- An IAM user named `geass-<bucket name>` with the same `geass-bucket` policy, and one access key.
- One folder in that bucket for each database.

## Rotate AWS credentials

1. Open **Settings** → **Connectors**, and click **Manage** on the **AWS** card.
2. Click **Edit** on the connection.
3. Enter only the values that change.
4. Click **Save**.

Geass uses the new key the next time that a bucket or a backup needs AWS. Running apps are not affected, because each
bucket has its own key. After the new key works, deactivate the old key in IAM.

:::caution
Use **Edit** to replace a key. Do not add a connection with the name of an earlier connection to replace a key. If the
earlier credentials are still stored, Geass keeps them and ignores the new keys.
:::

## Remove an AWS connection

Delete the buckets and backups that use the connection first. Geass needs the connection to delete an AWS bucket. If
you remove the connection too early, the bucket stays in deletion with "AWS connection is unavailable".

To remove the connection, click **Remove** on it, or call `DELETE /api/v1/connections/{name}`. Geass refuses while one
of these items still uses the connection, and it names the item:

- A pool or a machine.
- A bucket or a database.
- The backups of a deleted database.
- The backup bucket of a project.
- The control plane firewall.

Backups need the connection for 7 days after you delete a database. At the end of that time, Geass uses the
connection to remove the backup folder. If the connection is gone, Geass continues to try.

When you delete a project, Geass deletes its connections together with its buckets and databases. So delete the AWS
buckets first, and wait until they are gone. Then delete the project. If you do not, an AWS bucket can stay in
deletion with no credentials.

## What is cleaned up on AWS

| You delete | Removed from AWS | Left behind |
| --- | --- | --- |
| A bucket with **Create bucket in AWS** on | Every object, version, and unfinished upload, then the buckets, the IAM user, its keys, and its policy | Nothing |
| A bucket with **Create bucket in AWS** off | The IAM user, its keys, and its policy | The bucket and its objects |
| A database with backups | After 7 days, the backup folder of that database | The backup bucket and its IAM user |
| Project backup storage (**Remove**) | Nothing | The bucket, the objects, and the IAM user |
| The connection | Nothing | Everything that Geass created |

If you turn off backups for a database, Geass also removes its backups after 7 days. Geass never deletes the backup
bucket or the backup IAM user. Remove them yourself when you stop using them.

:::note
Compliance tags stop at the edge of the cluster. A bucket on AWS S3 is outside the cluster. If a project needs
compliance tags, use buckets and regions that meet the same requirement. See
[Compliance tags](/docs/guides/compliance-tags/).
:::

## Troubleshooting

**The connection stays Pending.** A key is missing. The message reads "AWS access key and secret key are required".
Click **Edit**, and enter both keys.

**The AWS option is unavailable in the resource menu.** The menu offers **AWS RDS** only when the project has a ready
AWS connection. The text reads "Add an AWS connection in platform settings".

**A bucket fails with `AWS IAM ... AccessDenied` or `AWS S3 403`.** The connection key does not have one of the
permissions in [Permissions the AWS key needs](#permissions-the-aws-key-needs).

**A bucket is ready, but the app cannot read it.** Geass treats an S3 `409` as "bucket exists", also when another AWS
account owns that name. Bucket names are global, so use a distinctive name.

**A bucket stays in deletion with "AWS connection is unavailable".** Someone removed the connection before the bucket.
Add the connection again with the same name. Then the deletion completes.

**A new key has no effect.** You added a connection with the name of an earlier connection, and Geass kept the earlier
credentials. Click **Edit** on the connection, and enter the new key there.

For the errors of every project connection, see [Cloud connectors](/docs/guides/connectors/#troubleshooting).

## Under the hood

- An AWS connection is a `GeassCloudConnection` in `geass-system`. Its credentials are in the Secret
  `<name>-credentials`, with the keys `accessKeyId`, `secretAccessKey`, and `region`. See
  [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/).
- The key of each bucket is in the Secret `<bucket>-connection` in the environment of the bucket. The key of the backup
  bucket is in `geass-backup-storage-<bucket>` in `geass-system`.
- If you add a connection with the name of an earlier connection, Geass keeps the Secret that exists. A deleted
  project or an earlier failed save can leave that Secret. To fix the key, replace the Secret:

  ```bash title="Replace the connection key"
  kubectl -n geass-system create secret generic prod-aws-credentials \
    --from-literal=accessKeyId=AKIAEXAMPLEEXAMPLE \
    --from-literal=secretAccessKey=replace-with-the-new-secret-access-key \
    --from-literal=region=eu-north-1 \
    --dry-run=client -o yaml | kubectl apply -f -
  ```

- Geass does not delete the Secret `<name>-credentials` when it deletes a project. Delete it yourself.

## Related

- [Cloud connectors](/docs/guides/connectors/)
- [Object storage](/docs/guides/object-storage/)
- [Backups and restore](/docs/guides/backups-and-restore/)
- [Databases](/docs/guides/databases/)
- [Compliance tags](/docs/guides/compliance-tags/)
- [Security](/docs/operations/security/)
