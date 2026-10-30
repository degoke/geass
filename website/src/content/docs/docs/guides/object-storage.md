---
title: "Object storage"
description: "Select SeaweedFS or Garage for cluster object storage, create buckets on it or in AWS S3, give apps their credentials, and learn the deletion rules and limits."
---

A bucket keeps files that your apps read and write with the S3 API, for example uploads, exports, and static assets. Geass offers two kinds:

- An in-cluster bucket is on an object storage server that Geass runs on your servers. An admin selects SeaweedFS or Garage for the whole cluster.
- An AWS S3 bucket is in your own AWS account. Geass creates a restricted AWS user for it.

Each bucket belongs to one project environment and has its own credentials.

## The two kinds of bucket

| | In-cluster bucket | AWS S3 bucket |
| --- | --- | --- |
| Stored on | The object storage server of the cluster, on one disk. You set the size when you set up the server (10 GiB by default) | Your AWS account |
| Needs first | The object storage server, set up under **Settings**, **Object storage** | An AWS connection for the project |
| Credentials | An access key and secret that reach only this bucket | An AWS user `geass-<resource ID>` that reaches only this bucket |
| Name it has there | `<project ID>.<environment>.<name>` | The name that you set |
| Size limit | The disk of the server, shared by every bucket. You can make it larger later, but not smaller | The limit of your AWS account |
| Backups | None | None. Use AWS features such as versioning |

## Before you start

For an in-cluster bucket, a dashboard admin must set up the object storage server one time for the cluster. See [Choosing SeaweedFS or Garage](#choosing-seaweedfs-or-garage) and [Set up the object storage server](#set-up-the-object-storage-server).

For an AWS bucket, add an AWS connection to the project in **Settings**, **Connectors**. See [Connect AWS](/docs/guides/connectors/aws/). The AWS keys of the connection need these permissions:

- The IAM actions `CreateUser`, `PutUserPolicy`, `CreateAccessKey`, `ListAccessKeys`, `DeleteAccessKey`, `DeleteUserPolicy`, and `DeleteUser`.
- S3 bucket creation and deletion.
- S3 object listing, including versions and multipart uploads.
- S3 object and version deletion, and multipart abort.

## Choosing SeaweedFS or Garage

Apps see the same thing with both servers: an S3 endpoint, a key that reaches only their own bucket, and the same four variables. The servers are different in how much memory they use and how much of S3 they support.

| | SeaweedFS | Garage |
| --- | --- | --- |
| Memory when idle | About 60 MB | About 6 MB |
| S3 support | Broad: multipart uploads, presigned links, conditional writes (`If-Match` and `If-None-Match`), server-side encryption, versioning, object lock, tagging, and bucket policies | Core S3: upload, download, list, delete, multipart uploads, and presigned links. No versioning, tagging, object lock, or bucket policies. Limited server-side encryption |
| Best for | Apps that use less common S3 features, and larger workloads | Small clusters, small servers, plain uploads, and static assets |
| License | Apache-2.0 | AGPL-3.0 (Geass runs it unmodified) |

If you are not sure, select SeaweedFS, because it works with the most S3 clients. Select Garage when your servers are small and your apps only upload, download, and list files.

Each app gets a key that reaches only the objects of its own bucket. So an app cannot change bucket settings such as versioning, policies, and lifecycle rules, with either server.

You make the choice one time, and the cluster has one object storage server. To switch later, you must delete every in-cluster bucket and remove the server. Geass does not copy data. See [Switch between SeaweedFS and Garage](#switch-between-seaweedfs-and-garage).

## Set up the object storage server

Only dashboard admins can do this task.

1. Open **Settings**, and then **Object storage**. The page shows a card for **SeaweedFS** and one for **Garage**.
2. On the card of the server that you want, click **Set up**.
3. Select **CPU**, **Memory**, and **Storage**. **Storage** is the size of the disk, 10 GiB by default. The sizes are gibibytes, labeled GB.
4. Click **Set up SeaweedFS server** or **Set up Garage server**. The dashboard says "SeaweedFS server created" or "Garage server created".

The defaults are 0.25 CPU and 512 MB for SeaweedFS, and 0.1 CPU and 256 MB for Garage. If the cluster cannot fit the CPU and memory, the page tells you to open cluster capacity first.

The page then shows the server with three tabs:

| Tab | What it shows |
| --- | --- |
| **Details** | Provider, status (Setting up, Starting, Waiting for capacity, Storage unavailable, Active, or Failed), CPU, memory, storage, and how many buckets use the server |
| **Manage** | **Size** (CPU and memory, saved with **Save size**), **Storage** (the disk, saved with **Save storage**), and **Remove object storage** |
| **Logs** | The recent output of the server, with a **Refresh** button |

When you change the CPU or memory, the server restarts. The server runs on one server with one disk. See [Where the server's data is kept](#where-the-servers-data-is-kept).

### Set up the server from the API

Send `cluster=on` and the `engine`, `SeaweedFS` or `Garage`. The optional fields are `cpu`, `memory`, `storageSize` (default `10Gi`, at least `1Gi`), `name` (default `cluster-s3`), `region`, and `runOnTags`. The setup page has no fields for the region and tags.

```bash
curl -X POST https://geass.example.com/api/object-stores/create \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json" \
  --data-urlencode cluster=on \
  --data-urlencode engine=SeaweedFS \
  --data-urlencode placement=InCluster \
  --data-urlencode cpu=500m \
  --data-urlencode memory=1Gi \
  --data-urlencode storageSize=20Gi
```

The reply is `{"ok":true}`. You cannot change the region and tags after setup.

### Remove the server

1. Delete all in-cluster buckets from their projects. Geass refuses to remove the server while one exists, and lists the buckets. AWS buckets are not affected.
2. Open **Manage**.
3. Under **Remove object storage**, click **Remove SeaweedFS** or **Remove Garage**.
4. Enter `remove`.
5. Click **Remove permanently**.

Geass deletes the server and all the data on its disk.

### Where the server's data is kept

Geass selects the kind of storage for the disk when it first creates the server. You cannot change it later.

| Cluster | Where the data is |
| --- | --- |
| Has replicated storage (the installer turns it on) | Copies on up to three servers. A server placed in a region keeps its copies in that region. A server placed on servers with tags keeps its copies on servers with those tags |
| Has no replicated storage, for example a local `kind` cluster | One copy on the server where the object storage server runs |

Copies on several servers keep the data when a server is lost. The copies are not a backup. When you delete an object, Geass deletes it from every copy. See [Storage and volumes](/docs/guides/storage/).

While replicated storage starts, a new object storage server shows "Storage is starting" and waits. If the replicated storage does not start, the server shows "Storage is unavailable". See [Troubleshooting](#troubleshooting).

### Make the volume bigger

You can make the disk of the server larger, but not smaller. Only replicated storage can grow. On other storage, the **Storage** section says "This cluster's storage can't be resized." and its list is unavailable.

1. Open **Settings**, and then **Object storage**.
2. Select the **Manage** tab.
3. Under **Storage**, select a larger **Size**. The list offers 1, 5, 10, 20, 50, 100, 200, and 500 GB, and the current size.
4. Click **Save storage**.

Geass grows the disk in place. The **Details** tab shows the current size.

To resize through the API, send `storageSize` to the update route of the server, `cluster-s3` by default:

```bash
curl -X POST https://geass.example.com/api/object-stores/cluster-s3/update \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json" \
  --data-urlencode storageSize=50Gi
```

The reply is `{"ok":true}`. If Geass refuses, the reply is `400` with one of these errors:

| Message | Meaning |
| --- | --- |
| storage size must be a quantity such as 10Gi | Geass could not read the value |
| storage size must be at least 1Gi | Below the minimum |
| storage can be increased but not decreased (currently 10Gi) | You asked for less than the current size |
| this cluster's storage can't be resized, so the server stays at 10Gi | The storage of the disk cannot grow |

### Switch between SeaweedFS and Garage

You cannot change the server in place. To use the other server:

1. With an S3 client, copy out the files that you want to keep.
2. Delete every in-cluster bucket from each project. Geass deletes the objects of each bucket.
3. Remove the server. Geass deletes everything on its disk.
4. Set up the other server.
5. Create the buckets again.
6. Point your apps at the new buckets.

## Create a bucket

1. Open the project and environment.
2. Click **+ Add resource**, and then select **Bucket**.
3. Select **In-cluster bucket** or **AWS S3** ("In your AWS account"). A row is unavailable, with the reason, when object storage is not set up or the project has no AWS connection. Geass saves a draft and opens its **Settings**.
4. In **Name**, enter the bucket name.
5. For an AWS bucket, select the **Connection**.
6. Optional: For an AWS bucket, turn on **Create bucket in AWS**. Geass then creates the bucket in your account.
7. Click **Save changes**.
8. In the top bar of the project, click **Deploy changes**.

If you never set a name, **Deploy changes** uses the resource ID as the bucket name, when the ID is a valid bucket name.

### Bucket names

A bucket name has 3 to 63 characters: lowercase letters, numbers, dots, and hyphens. It must also follow these rules:

- It starts and ends with a letter or a number.
- It does not have two dots in a row.
- It does not look like an IP address.

Geass stores an in-cluster bucket as `<project ID>.<environment>.<name>`, for example `proj-1a2b3c4d.production.uploads`. So two projects or two environments can both use the name `uploads`. They get separate buckets. The prefix counts toward the 63 characters. If your name is too long, Geass refuses it and says the limit.

The `S3_BUCKET` variable holds the stored name. Make your app read the bucket name from that variable.

:::caution
For an AWS bucket, use a name that is unique to you, for example `acme-storefront-uploads`. AWS bucket names are global across all AWS accounts, and Geass adds no prefix.
:::

### Create a bucket from the API

Use a workspace token with the admin role. The request creates a draft, so deploy it afterwards.

```bash
curl -X POST https://geass.example.com/api/object-stores/create \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json" \
  --data-urlencode project=proj-1a2b3c4d \
  --data-urlencode environment=production \
  --data-urlencode name=uploads \
  --data-urlencode engine=S3 \
  --data-urlencode placement=External \
  --data-urlencode connectionRef=prod-aws \
  --data-urlencode region=eu-west-1 \
  --data-urlencode createBucket=on \
  --data-urlencode bucket=acme-storefront-uploads

curl -X POST https://geass.example.com/api/object-stores/uploads/deploy \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json"
```

For an in-cluster bucket, send `placement=InCluster`. Leave out `engine`, `connectionRef`, `region`, and `createBucket`. If you leave out `bucket`, the resource ID is the bucket name. If you deploy a bucket that has no name through the API, Geass refuses with "set a bucket name in settings before you deploy".

From the command line, send the same fields as JSON:

```bash
geass api POST /object-stores -d '{"project": "proj-1a2b3c4d", "environment": "production", "name": "uploads", "engine": "S3", "placement": "External", "connectionRef": "prod-aws", "region": "eu-west-1", "createBucket": true, "bucket": "acme-storefront-uploads"}'
geass api POST /object-stores/uploads/deploy
```

## Compliance tags and the object storage server

A project can require compliance node tags, for example `hipaa`. See [Compliance node tags](/docs/guides/compliance-tags/). The object storage server belongs to no project, so the tags of a project do not move it. Geass places the server one time, at setup, with the `region` and `runOnTags` that you send through the API.

A project that requires tags can keep buckets on the server only when the server runs on servers with all of those tags. Otherwise, Geass refuses the bucket with "This object storage server does not run on nodes tagged hipaa, which the project requires".

The disk of the server stays on servers with the same tags. A server on compliance servers is not reserved for the projects that require the tag. Other projects can also use it.

AWS buckets are outside the cluster, so the tags do not apply to them.

## Credentials and how apps receive them

To give an app the credentials of a bucket:

1. Open the app and select **Variables**.
2. In the hint "Trying to connect a database?", click **Add Reference**.
3. Under **Resource**, select the bucket.
4. Click **Add reference**.
5. Deploy the app.

The reference adds these four variables:

| Variable | Value |
| --- | --- |
| `S3_ENDPOINT` | The S3 endpoint. For an in-cluster bucket, `http://geass-s3.geass-system.svc:8333` (SeaweedFS) or `http://geass-s3.geass-system.svc:3900` (Garage). For AWS, `https://s3.<region>.amazonaws.com` |
| `S3_ACCESS_KEY_ID` | The access key |
| `S3_SECRET_ACCESS_KEY` | The secret key |
| `S3_BUCKET` | The bucket name as stored |

These rules apply:

- The keys reach only the bucket of the resource. Geass never gives an app the admin login of the server.
- If you add the reference again, it replaces the four variables.
- If the app already has a variable with one of these names that someone added by hand, Geass refuses and names it. Remove that variable first. See [Environment variables](/docs/guides/environment-variables/#reference-a-database-or-bucket).
- If the object storage server forgets the key of a bucket, Geass makes a new key. Apps that already run keep the old values until they restart.

For an in-cluster bucket, use path-style addressing in your S3 client, because both servers require it. Garage uses the region `us-east-1`, which is the default of most S3 clients. SeaweedFS accepts any region.

The in-cluster endpoint works only from services in the cluster. The dashboard and the command line do not show the keys. To read them, or to reach the server from your own computer, see [Under the hood](#under-the-hood).

## Browse and manage files

The dashboard has no file browser for buckets. A bucket has an **Overview**, a **Deployments** tab, and **Settings**. Use an S3 client, for example the AWS CLI, `mc`, or `rclone`. For an AWS bucket, you can also use the AWS console.

## Rename a bucket

A change to **Name** on a deployed bucket is a pending change. When you deploy it, Geass creates the new bucket and moves the keys to it. Geass does not rename or delete the old bucket, and the keys no longer reach it. Move the files yourself, and remove the old bucket.

## Delete a bucket

1. Open the **Settings** of the bucket.
2. Select **Delete bucket**.
3. Enter the resource ID of the bucket.
4. Confirm.

What Geass deletes depends on the kind of bucket:

| Kind | What Geass deletes |
| --- | --- |
| In-cluster | The bucket and every object in it, and its keys. You cannot undo this delete |
| AWS, **Create bucket in AWS** on | The bucket with all object versions and unfinished uploads, and then the AWS user and its keys |
| AWS, **Create bucket in AWS** off | Only the AWS user and its keys. The bucket and its files stay in your account |

:::danger
To keep an AWS bucket, turn off **Create bucket in AWS**. Geass does not record whether it created the bucket. With the setting on, Geass deletes the bucket and its files, even a bucket that existed before.
:::

These rules also apply:

- A bucket that you never deployed is removed immediately, because Geass created nothing.
- A bucket that never became ready: Geass tries to remove what it created. If that fails, the delete still completes, and the leftovers stay. For example, the cleanup fails when the AWS keys lack a permission.
- A bucket that was ready: the delete waits until the cleanup succeeds.
- If you saved a new name and did not deploy it, Geass deletes the deployed bucket.

Before you delete a project, delete its buckets and wait for them to go. Geass deletes the AWS connections of a project with the project, and it needs the connection to remove the AWS user.

## Command line and AI agents

The `geass` command line and its MCP server list and inspect buckets. They never show credentials. They do not browse files, create a bucket, or set up the object storage server.

| To | Command | MCP tool |
| --- | --- | --- |
| List buckets | `geass stores list` | `list_buckets` |
| Read the settings and status of one bucket | `geass stores get uploads` | None |
| Create or deploy a bucket | `geass api POST /object-stores ...`, as above | None |
| Delete a bucket | `geass api DELETE /object-stores/uploads -q confirmName=uploads` | `delete_resource` with `kind` set to `object-stores`, only with `--allow-delete` |

These rules apply:

- `geass stores` is also called `geass buckets` and `geass object-stores`.
- The list includes the object storage server, which has no project. The list shows the server as `draft`, even while it runs.
- `list` and `get` work with any workspace token. A token that cannot change things does not see the bucket names, the region, or the endpoint. A project token cannot read buckets.
- A delete from the command line or an agent does the same as a delete in the dashboard. The warnings in [Delete a bucket](#delete-a-bucket) apply.

See [Command line](/docs/guides/command-line/), [CLI commands](/docs/reference/cli/#geass-stores), and [MCP tools](/docs/reference/mcp-tools/#list_buckets).

## Quotas and sizing

- Geass sets no quota and no object limit for each bucket.
- All in-cluster buckets share the disk of the server, 10 GiB by default.
- You can make the disk larger, but not smaller, and only on replicated storage. See [Make the volume bigger](#make-the-volume-bigger).
- For AWS, Geass sets no limit. Your AWS account limits and bill apply.
- The API accepts up to 32 bucket names for one bucket resource. The dashboard sets one.

## Options

These are the fields that the API accepts. The [resource reference](/docs/reference/resources/overview/) has the full schema.

| Field | Meaning | Default |
| --- | --- | --- |
| `project`, `environment` | The project environment that owns the bucket | None |
| `engine` | `S3` for an AWS bucket. For the object storage server, `SeaweedFS` or `Garage` | None |
| `placement` | `InCluster` or `External`. `S3` is always external | `InCluster` |
| `connectionRef` | The AWS connection, for AWS buckets | None |
| `region` | The AWS region of the bucket. The dashboard has no field for it | The region of the connection, then `us-east-1` |
| `createBucket` | Create the bucket in AWS, and delete it with the resource | `false` |
| `bucket` | The bucket name, 3 to 63 characters | The resource ID |

## Under the hood

This section is for admins who have `kubectl` access to the cluster.

- A bucket is a `GeassObjectStore` in `geass-system`. The object storage server is a `GeassObjectStore` with no project, named `cluster-s3` by default. It runs as the Deployment and Service `geass-s3` in `geass-system`.
- A network policy `geass-s3` lets every namespace reach the S3 port only. If someone changes the policy, Geass puts it back.
- The keys of a bucket are in the Secret `<resource ID>-connection`, in the namespace of the environment, `<project ID>-<environment>`.

To read the keys of a bucket, run these commands:

```bash
NS=proj-1a2b3c4d-production
for key in endpoint accessKey secretKey bucket; do
  printf '%s=' "$key"
  kubectl get secret uploads-connection -n "$NS" -o jsonpath="{.data.$key}" | base64 -d
  echo
done
```

To reach the object storage server from your computer, forward its port:

```bash
# SeaweedFS
kubectl port-forward -n geass-system svc/geass-s3 8333:8333

# Garage
kubectl port-forward -n geass-system svc/geass-s3 3900:3900
```

To read the status of a bucket and of the server:

```bash
kubectl get geassobjectstore uploads -n geass-system \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}{"\n"}'
kubectl describe pod -n geass-system -l app.kubernetes.io/name=geass-s3
```

## Troubleshooting

**A bucket shows "Draft" with "Resource is configured and waiting for deployment".**
The bucket is not deployed. Set its **Name** in **Settings**, **Bucket**, and save. Then, in the top bar of the project, click **Deploy changes**.

**"bucket name ... is too long: on the cluster's object storage it is stored as ...".**
The name and the prefix together must fit in 63 characters. The message gives the limit. Shorten the name in **Settings**, **Bucket**, and save.

**"Set up object storage in cluster settings first" or "Set up object storage in Platform settings."**
The cluster has no object storage server. Set it up under **Settings**, **Object storage**, and wait for **Active**. "Object storage is starting" means that the server exists but is not ready yet.

**"Configure the bucket name in settings, then deploy" or "set a bucket name in settings before you deploy".**
The **Name** of the bucket is empty. Set it in **Settings**, **Bucket**, and save.

**"This object storage server does not run on nodes tagged hipaa, which the project requires".**
The server cannot move. Delete every bucket, remove the server, and set it up again with `runOnTags`. See [Compliance tags and the object storage server](#compliance-tags-and-the-object-storage-server).

**The server stays at "Starting".**
A server might not be able to download the server image from Docker Hub. Check the network and registry access of your servers. An admin can read the events of the pod. See [Under the hood](#under-the-hood).

**The server shows "Starting" with "Storage is starting".**
Replicated storage is still starting. Geass waits. See [Storage and volumes](/docs/guides/storage/#troubleshooting).

**The server shows "Storage unavailable".**
Replicated storage did not start in 15 minutes. Geass stops after repeated errors. Fix the storage, and then send `POST /api/object-stores/<name>/retry-provision`. Geass did not create the disk, so nothing is lost.

**Saving a larger size says "this cluster's storage can't be resized, so the server stays at 10Gi".**
The disk is not on replicated storage, so it cannot grow or move. To change the size, remove the server and set it up again. This deletes its data. See [Switch between SeaweedFS and Garage](#switch-between-seaweedfs-and-garage).

**The server shows "Waiting for capacity".**
No server in the cluster has room for its CPU and memory. Select a smaller size under **Manage**, or add capacity. See [Usage and capacity](/docs/guides/usage-and-capacity/).

**"An AWS connection is required for external buckets" or "Connect a cloud account in Platform settings."**
Select a connection in **Settings**, **Bucket**. The connection must belong to the same project.

**An AWS bucket fails with an access-denied error.**
The AWS keys lack a permission from [Before you start](#before-you-start). The message includes the AWS error code.

**A bucket stays at "Deleting".**
Geass could not finish the cleanup. For an AWS bucket, the cause is usually "AWS connection is unavailable". Create the connection again with the same name and project. For an in-cluster bucket, make sure that the object storage server is ready.

**An app cannot read its bucket.**
Make sure of these items:

- The app has the four `S3_` variables.
- The app uses path-style addressing for an in-cluster bucket.
- You deployed the app after you added the variables.

## Related

- [Storage and volumes](/docs/guides/storage/)
- [Network isolation](/docs/guides/network-isolation/)
- [Compliance node tags](/docs/guides/compliance-tags/)
- [Command line](/docs/guides/command-line/)
- [Connect AWS](/docs/guides/connectors/aws/)
- [Environment variables](/docs/guides/environment-variables/)
- [Backups and restore](/docs/guides/backups-and-restore/), which uses its own S3 bucket, separate from bucket resources
- [Users and tokens](/docs/guides/users-and-tokens/)
