---
title: "Object storage"
description: "Select SeaweedFS or Garage for cluster object storage, create buckets on it or in AWS S3, give apps their credentials, and learn the deletion rules and limits."
---

A bucket is a place to keep files that your apps read and write over the S3 API: uploads, exports, and static assets. Geass offers two kinds:

- An in-cluster bucket lives on an object storage server that Geass runs on your nodes. The server is either SeaweedFS or Garage, and an admin selects one for the whole cluster.
- An AWS S3 bucket lives in your own AWS account, and Geass creates a restricted IAM user for it.

Each bucket resource belongs to one project environment and has its own credentials.

## The two kinds of bucket

| | In-cluster bucket | AWS S3 bucket |
| --- | --- | --- |
| Stored on | The object storage server of the cluster (SeaweedFS or Garage), on one volume. You set the size of the volume when you set up the server (10 GiB by default) | Your AWS account |
| Needs first | The object storage server set up under **Settings**, **Object storage** | An AWS connection scoped to the project |
| Credentials | A login on the server, `geass-<resource ID>`, with its own access key and secret, limited to its bucket | An IAM user `geass-<resource ID>`, limited to its bucket |
| Name it has there | `<project ID>.<environment>.<name>` | The name that you set |
| Endpoint apps use | `http://geass-s3.geass-system.svc:8333` (SeaweedFS) or `http://geass-s3.geass-system.svc:3900` (Garage) | `https://s3.<region>.amazonaws.com` |
| Size limit | The volume of the server, shared by every bucket. You can make it bigger later, but never smaller | The limit that your AWS account allows |
| Geass-managed copies or backups | None | None. If you need them, use AWS features such as versioning |

## Before you start

For an in-cluster bucket, a dashboard admin must set up the object storage server one time for the whole cluster. See [Choosing SeaweedFS or Garage](#choosing-seaweedfs-or-garage) and [Set up the object storage server](#set-up-the-object-storage-server).

For an AWS bucket, add an AWS connection to the project in **Settings**, **Connectors**. The connection is scoped to one project. See [Connect AWS](/docs/guides/connectors/aws/). With the keys, Geass must be able to do these actions:

- Create and delete IAM users.
- Put user policies.
- Create access keys.
- Create, list, and delete S3 buckets and objects.

Geass calls these IAM actions: `CreateUser`, `PutUserPolicy`, `CreateAccessKey`, `ListAccessKeys`, `DeleteAccessKey`, `DeleteUserPolicy`, and `DeleteUser`. On S3, Geass does these actions:

- Creates buckets.
- Lists objects, versions, and multipart uploads.
- Deletes objects and versions.
- Aborts multipart uploads.
- Deletes buckets.

## Choosing SeaweedFS or Garage

Geass can run either server as the object storage of the cluster. Apps see the same thing with both servers: an S3 endpoint, a login limited to their own bucket, and a connection Secret with the same four keys. The two servers are different in what they ask of a node and in how much of S3 they cover.

| | SeaweedFS | Garage |
| --- | --- | --- |
| Memory when idle | About 60 MB | About 6 MB |
| S3 coverage | Broad: multipart uploads, presigned links, conditional writes (`If-Match` and `If-None-Match`), server-side encryption, object versioning, object lock, object tagging, and bucket policies | Core S3: upload, download, list, delete, multipart uploads, and presigned links. No object versioning, tagging, object lock, or bucket policies. Server-side encryption is limited |
| Best for | Apps that use less common S3 features, and bigger or busier workloads | Small clusters, low-memory nodes, plain uploads, and static assets |
| Make-up | One process running four components | One small binary |
| License | Apache-2.0 | AGPL-3.0 (Geass runs it unmodified) |

If you are not sure, select SeaweedFS, because it works with the widest range of S3 clients. Select Garage when your nodes are small and your apps only upload, download, and list files.

Geass gives each app a login limited to the objects of its own bucket. So today, an app cannot change bucket-level settings such as versioning, policies, and lifecycle rules, whichever server you select.

You make the choice one time, and only one server can exist at a time. To switch later, you must delete every in-cluster bucket, remove the server, and set up the other server. Geass has no migration tool and does not copy data. See [Switch between SeaweedFS and Garage](#switch-between-seaweedfs-and-garage).

## Set up the object storage server

Only dashboard admins can do this task, and only one time for each cluster.

1. Open **Settings**, then **Object storage**. The page shows a card for **SeaweedFS** and one for **Garage**, with a comparison of the two below them.
2. On the card of the server that you want, click **Set up**.
3. Select **CPU**, **Memory**, and **Storage**. The defaults are 0.25 CPU and 512 MB for SeaweedFS, and 0.1 CPU and 256 MB for Garage. **Storage** is the size of the volume of the server: 10 GiB by default, and never less than 1 GiB. As for databases, the dashboard lists sizes as GB, which are gibibytes. If the cluster cannot fit the CPU and memory, the page tells you to open cluster capacity first.
4. Click **Set up SeaweedFS server** or **Set up Garage server**. The dashboard confirms with "SeaweedFS server created" or "Garage server created".

The page now shows the server with three tabs.

| Tab | What it shows |
| --- | --- |
| **Details** | Provider (SeaweedFS or Garage), status (Setting up, Starting, Waiting for capacity, Storage unavailable, Active, Failed), CPU, memory, storage, and how many project buckets use the server |
| **Manage** | **Size** (CPU and memory, saved with **Save size**), **Storage** (the volume, saved with **Save storage**), and **Remove object storage** |
| **Logs** | The recent output of the server, with a **Refresh** button |

When you change the CPU or memory, the server restarts with the new limits. The server runs as one server on one node, with one volume. The storage of the cluster sets whether that volume is copied to other nodes. See [Where the server's data is kept](#where-the-servers-data-is-kept). You can make the volume bigger later. See [Make the volume bigger](#make-the-volume-bigger).

To remove the server:

1. Delete all in-cluster project buckets from their projects. Geass refuses to remove the server while any in-cluster project bucket exists, and it lists the buckets that block removal. External S3 buckets are not affected.
2. Open **Manage**.
3. Under **Remove object storage**, click **Remove SeaweedFS** or **Remove Garage**.
4. Enter `remove`.
5. Click **Remove permanently**.

When you remove the server, Geass deletes the server, its `geass-s3` network policy, and all the data on its volume.

To set up the server with the API instead, send `cluster=on` and the `engine`, `SeaweedFS` or `Garage` (in any letter case). These fields are optional:

- `cpu`.
- `memory`.
- `storageSize`: default `10Gi`, at least `1Gi`.
- `name`: default `cluster-s3`.
- `region`.
- `runOnTags`.

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

The reply is `{"ok":true}`. You set the region and the tags of the server when you create it. Its volume stays where it is created. The recent output of the server is also available at `GET /api/object-stores/<name>/logs`.

### Where the server's data is kept

Geass makes the volume of the server one time, when the server is first created. Geass then decides which storage class the volume uses. Geass keeps that decision, because the class of a volume cannot change later. A server that already has a volume keeps the class of that volume.

| Cluster | The volume is on | Where the data is |
| --- | --- | --- |
| Has the replicated storage of Geass (the Longhorn add-on, which the installer turns on) | The replicated class, like database volumes | Several nodes, up to three. A server placed in a region keeps its copies in that region. A server placed on nodes with tags keeps its copies on nodes that carry those tags, so its data is never replicated onto other nodes. These tags are the `runOnTags` that the server was set up with, such as a compliance tag. The cluster creates the matching storage class itself |
| Has no Longhorn, such as a local `kind` cluster | The default storage class of the cluster (`local-path` on K3s) | One node: the node that the pod of the server runs on |

Copies on several nodes keep the data when a node is lost. The copies are not a backup. When you delete an object, it is deleted from every copy, and Geass makes no backup of bucket contents. For the classes, see [Storage and volumes](/docs/guides/storage/). For tags, see [Compliance tags and the object storage server](#compliance-tags-and-the-object-storage-server).

While the replicated storage is still being installed, a new server reports "Storage is starting". Geass waits. It does not create the volume on local storage, because a volume cannot move to another class afterwards. If the replicated storage never comes up, the server reports "Storage is unavailable". See [Troubleshooting](#troubleshooting).

### Make the volume bigger

You can make the volume of the server bigger. You cannot make it smaller. The volume can grow only where the storage of the cluster can be resized. A cluster with the replicated storage of Geass (Longhorn) can resize it. The default K3s `local-path` storage cannot. On a cluster that cannot resize the volume, the **Storage** section says "This cluster's storage can't be resized." and its list is disabled.

1. Open **Settings**, then **Object storage**.
2. Select the **Manage** tab.
3. Under **Storage**, select a larger **Size**. The list offers the current size, and the larger sizes of 1, 5, 10, 20, 50, 100, 200, and 500 GB (GiB values).
4. Click **Save storage**.

Geass grows the volume in place, so nothing is created again. The current size of the server is on the **Details** tab.

To resize through the API, send `storageSize` to the update of the server. The name of the server is `cluster-s3`, unless you selected another name:

```bash
curl -X POST https://geass.example.com/api/object-stores/cluster-s3/update \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json" \
  --data-urlencode storageSize=50Gi
```

The reply is `{"ok":true}`. A request that Geass refuses returns `400`, with one of these messages as `error`:

| Message | Meaning |
| --- | --- |
| storage size must be a quantity such as 10Gi | The value could not be read |
| storage size must be at least 1Gi | Below the minimum |
| storage can be increased but not decreased (currently 10Gi) | You asked for less than the current size |
| this cluster's storage can't be resized, so the server stays at 10Gi | The storage class of the volume does not allow it to grow |

The `platform` object of `GET /api/bootstrap` tells a client what to offer:

- `objectStorageResizable` says whether the volume can be made bigger.
- `defaultStorage.objectstore` is the default size, `10Gi`.

See the [REST API](/docs/reference/rest-api/).

### Switch between SeaweedFS and Garage

Geass keeps one server, which you select when you set it up. You cannot change the server in place. To use the other server:

1. With an S3 client, copy out any files that you want to keep. Geass does not move data between servers.
2. Delete every in-cluster bucket, from each project. When you delete a bucket, its objects are deleted.
3. Remove the server, as described above. This step deletes everything on its volume.
4. Set up the other server.
5. Create the buckets again.
6. Point your apps at the new buckets.

## Create a bucket

1. Open the project and environment.
2. Click **+ Add resource**, then select **Bucket**.
3. Select **In-cluster bucket** ("Stored on this cluster with SeaweedFS", or with Garage) or **AWS S3** ("In your AWS account"). A row is greyed out, with the reason, when object storage is not set up or the project has no AWS connection. Geass saves a draft and opens its **Settings** on the **Bucket** section.
4. In **Name**, enter the bucket name. The name is required.
5. Optional: For an AWS bucket, select the **Connection**.
6. Optional: For an AWS bucket, turn on **Create bucket in AWS**, so that Geass creates the bucket in your account.
7. Click **Save changes**.
8. In the top bar of the project, click **Deploy changes**.

If you deploy a draft and you never named its bucket, **Deploy changes** uses the resource ID as the bucket name, when that ID is a valid bucket name. If you deploy one bucket through `POST /api/object-stores/<ID>/deploy` with no bucket name set, Geass refuses with "set a bucket name in settings before you deploy".

### Bucket names

A bucket name has 3 to 63 characters: lowercase letters, numbers, dots, and hyphens. A bucket name must follow these rules:

- It starts and ends with a letter or number.
- It does not contain two dots in a row.
- It does not look like an IP address.

The in-cluster buckets of every project live on one object storage server, where bucket names are global. So Geass stores an in-cluster bucket under `<project ID>.<environment>.<name>`, for example `proj-1a2b3c4d.production.uploads`. Dots join the parts, because a project ID or an environment cannot contain a dot. So no two combinations ever give the same name. Two projects, or two environments of one project, can both name a bucket `uploads`. They get two separate buckets, and the login of each bucket can reach only its own bucket. Use path-style addressing, as for any in-cluster bucket. The `bucket` key of the Secret of the bucket holds the name as stored, so make your app read the name from there. **Add reference** does this.

The prefix counts towards the 63 characters. For example, a project whose ID and environment take 25 characters, such as `proj-1a2b3c4d.production.`, leaves 38 for the name that you set. When you save a longer name, Geass refuses it and says what the limit is.

:::caution
Use a name that is unique to you, for example `acme-storefront-uploads`. AWS bucket names are global across all AWS accounts, and Geass does not add a prefix to them.
:::

### Create a bucket with the REST API

Use a workspace token with the admin role. Project tokens cannot create resources. See [Users and tokens](/docs/guides/users-and-tokens/). Geass creates the resource as a draft, so deploy it afterwards.

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

For an in-cluster bucket, send `placement=InCluster` and leave out `connectionRef`, `region`, and `createBucket`. You do not need to send `engine`, because the bucket uses the engine of the server of the cluster. If you leave out `bucket`, the resource ID is the bucket name. The resource ID must then be a valid bucket name (see [Bucket names](#bucket-names)).

The versioned API takes the same fields as JSON, at `POST /api/v1/object-stores`. The command line calls this API:

```bash
geass api POST /object-stores -d '{"project": "proj-1a2b3c4d", "environment": "production", "name": "uploads", "engine": "S3", "placement": "External", "connectionRef": "prod-aws", "region": "eu-west-1", "createBucket": true, "bucket": "acme-storefront-uploads"}'
geass api POST /object-stores/uploads/deploy
```

The same resource as a manifest:

```yaml title="uploads.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassObjectStore
metadata:
  name: uploads
  namespace: geass-system
spec:
  project: proj-1a2b3c4d
  environment: production
  engine: S3
  placement: External
  connectionRef:
    name: prod-aws
  region: eu-west-1
  createBucket: true
  buckets:
    - acme-storefront-uploads
```

The ID of the project is in the address of its pages (`/projects/proj-1a2b3c4d`).

## Compliance tags and the object storage server

A project can require compliance node tags, such as `hipaa`, so that everything it runs stays on nodes that you marked. See [Compliance node tags](/docs/guides/compliance-tags/). The cluster object storage server belongs to no project, so the tags of a project do not place it. Geass places the server one time, when it is first set up, with its own region and **Run on** tags. The server does not move afterwards. The setup page has no field for the region and tags. To set them, send `region` and `runOnTags` when you create the server through the API, as shown under [Set up the object storage server](#set-up-the-object-storage-server). Geass checks that a node in that region carries each tag.

A project that requires tags can keep buckets on the server only when the server runs on nodes with every one of those tags. If the server does not run on those nodes, Geass refuses the bucket with "This object storage server does not run on nodes tagged hipaa, which the project requires". The refusal counts as a failure. The check is part of provisioning.

The volume of the server follows the tags that the server runs on:

- With the replicated storage of Geass (Longhorn), Geass creates the volume on a class that keeps all its copies on certain nodes. These nodes are in the region of the server and carry every tag that the server was set up with. So its data is never replicated onto other nodes. The cluster creates that storage class itself, when every node that the class covers has a ready Longhorn manager.
- Without Longhorn, the volume is on the default class of the cluster (`local-path` on K3s). That class keeps the volume on the node that its pod runs on, and the pod runs only on the tagged nodes.

See [Where the server's data is kept](#where-the-servers-data-is-kept) and [Storage and volumes](/docs/guides/storage/#compliance-tags-and-storage-classes). A server that runs on compliance nodes is not reserved for the projects that require the tag. Geass checks only that the tags of the project are on the server.

AWS S3 buckets live in your AWS account, outside the cluster, so required tags do not reach them.

## Credentials and how apps receive them

When a bucket is ready, Geass writes a Secret named `<resource ID>-connection` in the namespace of the project environment (`<project ID>-<environment>`).

| Key | Value |
| --- | --- |
| `endpoint` | The S3 endpoint, as in the table above |
| `accessKey` | For an in-cluster bucket, the access key ID of the own login of the bucket on the server. For AWS, the access key ID of the IAM user |
| `secretKey` | The matching secret. Generated one time and kept |
| `bucket` | The bucket name as stored. For an in-cluster bucket, `<project ID>.<environment>.<name>`. With several names in `buckets`, the first name |

The credentials are limited to the bucket (or buckets) that the resource names. For an in-cluster bucket, the server creates the access key and secret, so they do not look like the name of the resource. Geass never gives an app the admin login of the server. That login stays in `<server name>-root` in `geass-system` (`cluster-s3-root` by default). If the server is reset and forgets the login of a bucket, Geass issues a new key on its next pass and updates the Secret. Apps that already started keep the old values until they restart.

With an in-cluster bucket, use path-style addressing in your S3 client, because both servers require it. The region of Garage is `us-east-1`, which is the default of most S3 clients, so your app does not need to set a region. SeaweedFS accepts any region.

The **Add reference** action on the **Variables** tab of an app adds all four values for a bucket as variables that read the Secret. So the app gets the endpoint, the keys, and the stored bucket name together:

| Variable | Reads |
| --- | --- |
| `S3_ENDPOINT` | `endpoint` |
| `S3_ACCESS_KEY_ID` | `accessKey` |
| `S3_SECRET_ACCESS_KEY` | `secretKey` |
| `S3_BUCKET` | `bucket` |

If you add the reference again, it replaces these variables when they were added the same way. The service might already have a variable with one of these names that someone set by hand, as a plain environment variable or under **Variables**. In that case, Geass refuses and names the variable, so remove that variable first. Deploy the app afterwards. See [Environment variables](/docs/guides/environment-variables/#reference-a-database-or-bucket).

To read the values, run these commands:

```bash
NS=proj-1a2b3c4d-production
for key in endpoint accessKey secretKey bucket; do
  printf '%s=' "$key"
  kubectl get secret uploads-connection -n "$NS" -o jsonpath="{.data.$key}" | base64 -d
  echo
done
```

Deploy the app after you save its variables. See [Environment variables](/docs/guides/environment-variables/).

The in-cluster endpoint resolves only inside the cluster. To reach it from your machine, forward the port of the server that you selected:

```bash
# SeaweedFS
kubectl port-forward -n geass-system svc/geass-s3 8333:8333

# Garage
kubectl port-forward -n geass-system svc/geass-s3 3900:3900
```

## Browse and manage files

The dashboard has no file browser for buckets. A bucket resource has an **Overview**, a **Deployments** tab, and **Settings**. Use any S3 client, such as the AWS CLI, `mc`, or `rclone`, with the endpoint and keys from the Secret. For an AWS bucket, you can also use the AWS console.

## Rename a bucket

A change to **Name** on a deployed bucket is a pending change. When you deploy the change, Geass creates the new bucket and points the credentials at it. Geass does not rename or delete the old bucket, and the login of the bucket no longer has access to the old bucket. Move the files yourself and remove the old bucket.

## Delete a bucket

1. Open the **Settings** of the bucket.
2. Select **Delete bucket**.
3. Enter the resource ID of the bucket.
4. Confirm.

The result is different for each kind of bucket.

| Kind | What Geass removes |
| --- | --- |
| In-cluster | The bucket and every object in it, the login of the bucket on the server, and its Secrets. You cannot undo this deletion, and there is no trash |
| AWS, **Create bucket in AWS** on | The bucket with all object versions and unfinished multipart uploads, then the IAM user with its keys and policy |
| AWS, **Create bucket in AWS** off | Only the IAM user, its keys, and its policy. The bucket and its files stay in your account |

:::danger
For a bucket that you want to keep, turn off **Create bucket in AWS**. Geass does not record whether it created an AWS bucket or found it already in your account. With **Create bucket in AWS** on, when you delete the resource, Geass deletes the bucket and everything in it, even a bucket that existed before.
:::

What Geass does depends on the state of the bucket:

- A bucket that you never deployed: Geass removes it without a call to the object storage server or AWS, because Geass created nothing yet.
- A bucket that was deployed but never became ready: Geass tries to remove what it had already created, such as the bucket on the server, or the bucket and IAM user in AWS. That cleanup is best effort. If the cleanup fails, the deletion still completes and the leftovers stay where they are. For example, the cleanup fails if the AWS key does not have a permission.
- A bucket that stopped before Geass created anything, for example because the object storage server was not set up: Geass removes the bucket without a call to the server or AWS.
- A bucket that has been ready: Its deletion waits until the cleanup succeeds.

If you saved a new name and did not deploy it, Geass removes the bucket that was deployed, not the new name.

Before you delete a project, delete the buckets of the project and wait for them to disappear. When you delete a project, Geass also deletes its AWS connections. Geass needs the connection to clean up the user of an AWS bucket.

## Command line and AI agents

The `geass` command line and its MCP server list and inspect buckets. They never show credentials. They do not browse files, create a bucket, or set up the object storage server.

| To | Command | MCP tool |
| --- | --- | --- |
| List buckets | `geass stores list` | `list_buckets` |
| Read the settings and status of one bucket | `geass stores get uploads` | None |
| Create or deploy a bucket | `geass api POST /object-stores ...`, as above | None |
| Delete a bucket | `geass api DELETE /object-stores/uploads -q confirmName=uploads` | `delete_resource` with `kind` set to `object-stores`, only with `--allow-delete` |

`geass stores` is also called `geass buckets` and `geass object-stores`. The list includes the cluster object storage server, which has no project. The list shows the server as `draft` even while it runs, because nothing sets `spec.deploy.enabled` on it. `list` and `get` work with any workspace token. A token that cannot change things does not get the bucket names, the connection, the region, or the endpoint. A project token cannot read buckets at all. No command returns the access key or secret key of a bucket. Read them from the Secret, as under [Credentials and how apps receive them](#credentials-and-how-apps-receive-them).

A deletion through the command line or an agent does exactly what a deletion in the dashboard does, so the warnings under [Delete a bucket](#delete-a-bucket) apply. See [Command line](/docs/guides/command-line/), [CLI commands](/docs/reference/cli/#geass-stores), and [MCP tools](/docs/reference/mcp-tools/#list_buckets).

## Quotas and sizing

- Geass sets no per-bucket quota and no object limit.
- All in-cluster buckets share the single volume of the server. The volume is 10 GiB, unless you selected another size when you set up the server, and at least 1 GiB.
- You can make the volume bigger, but never smaller, and only where the storage of the cluster can be resized. See [Make the volume bigger](#make-the-volume-bigger). With the replicated storage of Geass, the data is kept on up to three nodes. Without it, the data is on one node.
- The CPU, memory, and storage of the server are the only server settings that you can change. The default is 0.25 CPU and 512 MB for SeaweedFS, and 0.1 CPU and 256 MB for Garage.
- For AWS, Geass applies no limit. AWS account limits and your storage bill apply.
- One bucket resource can list up to 32 bucket names. The dashboard sets one.

## Options

| Field | Meaning | Default |
| --- | --- | --- |
| `project`, `environment` | The project environment that owns the bucket. Empty `project` means the cluster object storage server | None |
| `engine` | `SeaweedFS` or `Garage` for the cluster server and the buckets on it, and `S3` for an AWS bucket. A bucket on the cluster server uses the engine of the server | None (required) |
| `placement` | `InCluster` or `External`. `S3` is always external | `InCluster` |
| `connectionRef` | The AWS connection (name), for AWS buckets | None |
| `region` | AWS region for the bucket. The dashboard has no field for it | The region of the connection, then `us-east-1` |
| `createBucket` | Create (and on delete, remove) the bucket in AWS | `false` |
| `buckets` | Bucket names, up to 32, each 3 to 63 characters | None. The bucket name must be set before deploy |
| `resources` | CPU and memory of the object storage server (the cluster server only) | 0.25 CPU, 512 MB for SeaweedFS. 0.1 CPU, 256 MB for Garage |
| `storageSize` | Size of the volume of the object storage server, for example `20Gi` (the cluster server only). You can raise it later, but never lower it, and only where the storage of the cluster can be resized. The operator writes the value onto the resource | `10Gi`, and at least `1Gi` |
| `serverRegion`, `runOn.tags` | Where the object storage server runs (the cluster server only). You set these fields through the API, and they are fixed when the server is first set up. A project that requires tags can use the server only when `runOn.tags` covers them | The default region, any node |

The full schema is in the [resource reference](/docs/reference/resources/overview/).

## What happens under the hood

- The cluster object storage server is a `GeassObjectStore` with no project, in `geass-system`. Its name is `cluster-s3`, unless you selected another name through the API. Geass runs the server itself from a pinned image, with no Helm chart. Geass creates these objects:
  - A Deployment of one pod, a volume claim, and a Service, all named `geass-s3` in `geass-system`.
  - A network policy with the same name.
- The claim is 10 GiB, unless you selected another size. The claim is on the replicated storage class of Geass when the cluster has Longhorn. Otherwise, it is on the default class of the cluster.
- The Service publishes the S3 port: 8333 for SeaweedFS, or 3900 for Garage. For Garage, the Service also publishes the admin port, 3903, which Geass uses to make buckets and keys. Beside S3, SeaweedFS serves internal services that have no login of their own. So SeaweedFS listens only on localhost, and a small second container in the same pod forwards the S3 port and nothing else. Both servers run as a non-root user.
- The network policy `geass-s3` in `geass-system` selects the pod of the server. It allows ingress only to the ports that are meant to be reached:
  - The S3 port (8333 for SeaweedFS, 3900 for Garage) is open to every namespace, because apps reach their buckets through it. The login of each project limits that access to its own bucket.
  - The admin port of Garage, 3903, is open only to the namespace of the platform, `geass-system`.
  - Every other port is closed.
- The policy takes effect on clusters whose network enforces policies. The network of K3s does. The server also closes those ports itself. SeaweedFS listens on localhost, with a forwarder container that publishes S3, and the other ports of Garage are closed. The policy is a second lock. If someone changes or deletes the policy by hand, Geass puts it back.
- The own login of the server is in the Secret `<server name>-root` (`cluster-s3-root` by default) in `geass-system`. The files that the server reads at start are in the Secret `geass-s3-config`.
- For each in-cluster bucket, Geass uses the login of the server to create the bucket under its stored name. Then Geass creates a login that can reach only that bucket. For SeaweedFS, this login is a user `geass-<resource ID>` made through the IAM API of SeaweedFS. For Garage, it is a key made through the admin API of Garage. The server generates the access key and secret, and Geass keeps them in the connection Secret.
- For each AWS bucket, Geass optionally creates the bucket. Then it creates the IAM user `geass-<resource ID>` with an inline policy named `geass-bucket`, and one access key. Geass uses the stored key again on later reconciles, so it does not create a new key each time. Geass changes no other bucket setting. It sends only the create request, so the AWS defaults for a new bucket apply.
- The connection Secret `<resource ID>-connection` lives in the namespace of the project environment.
- Immediately before Geass first creates a bucket or a login, it sets the annotation `geass.dev/provisioning-started` on the resource. When you delete a bucket that never became ready, Geass uses this annotation to know that there is something to remove. Do not edit the annotation.

```bash
kubectl get geassobjectstores -n geass-system
kubectl get geassobjectstore uploads -n geass-system \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}{"\n"}'
kubectl get deployment geass-s3 -n geass-system
kubectl get pods -n geass-system
```

## Troubleshooting

**A bucket shows "Draft" with the message "Resource is configured and waiting for deployment".**
The bucket is still a draft, or it is not deployed. Set its **Name** in **Settings**, **Bucket**, and save. Then, in the top bar of the project, click **Deploy changes**. Geass creates nothing for a draft. After you deploy, the message changes to ready for an in-cluster bucket. Check the message with the second command above.

**"bucket name ... is too long: on the cluster's object storage it is stored as ...".**
Geass stores an in-cluster bucket behind its project ID and environment, and the name and prefix together must fit in 63 characters. The message says how many characters the name can have. Shorten the name in **Settings**, **Bucket**, and save.

**"Set up object storage in cluster settings first" or "Set up object storage in Platform settings."**
There is no cluster object storage server. Set it up under **Settings**, **Object storage**, and wait for **Active**. "Object storage is starting" means that the server exists and is not ready yet.

**"Configure the bucket name in settings, then deploy" or "set a bucket name in settings before you deploy".**
The **Name** of the bucket is empty. Set it in **Settings**, **Bucket**, and save.

**"This object storage server does not run on nodes tagged hipaa, which the project requires".**
The project requires node tags that the server is not placed on, and the server cannot move. Delete every project bucket, and then remove the server. Set it up again with `runOnTags`, as under [Compliance tags and the object storage server](#compliance-tags-and-the-object-storage-server).

**The server stays at "Starting", or its pod is in `ImagePullBackOff`.**
Run `kubectl describe pod -n geass-system` on the `geass-s3` pod and read the events. A node that cannot pull the pinned image from Docker Hub never starts the server. Check the network and registry access of the node.

**The server shows "Starting", and its condition reads "Storage is starting".**
The replicated storage of the cluster (Longhorn) is still being installed, or the storage class for the region or tags of the server is not made yet. Geass waits. It does not create the volume on local storage. Run `kubectl get pods -n longhorn-system` and `kubectl get storageclass`. A node without the iSCSI initiator holds Longhorn back. See [Storage and volumes](/docs/guides/storage/#troubleshooting).

**The server shows "Storage unavailable", and its condition reads "Storage is unavailable".**
The replicated storage did not come up in 15 minutes. The message counts as a failure. So after repeated errors, the server reports that provisioning failed, and Geass stops retrying until you retry it. Fix Longhorn (see the check above). Then retry with `POST /api/object-stores/<name>/retry-provision`. The volume was not created, so nothing is lost.

**Saving a larger storage size says "this cluster's storage can't be resized, so the server stays at 10Gi".**
The volume of the server is on a storage class that cannot grow, such as the default `local-path` class of K3s. You cannot change the size, and you cannot move a volume to another class. Select the size that you need when you set up the server. To change the size, you must remove the server and set it up again. When you remove the server, its data is deleted (see [Switch between SeaweedFS and Garage](#switch-between-seaweedfs-and-garage)).

**The object storage server shows "Waiting for capacity".**
The cluster has no node with room for the CPU and memory of the server. Select a smaller size under **Manage**, or add capacity. See [Usage and capacity](/docs/guides/usage-and-capacity/).

**"An AWS connection is required for external buckets" or "Connect a cloud account in Platform settings."**
The bucket has no AWS connection, or the connection does not have credentials. Select a connection in **Settings**, **Bucket**. The connection must belong to the same project.

**An AWS bucket fails with an access-denied error.**
The keys of the connection do not have an IAM or S3 permission from the list under [Before you start](#before-you-start). The message includes the AWS error code.

**A bucket stays at "Deleting".**
Geass could not finish cleanup. For an AWS bucket, the cause is usually "AWS connection is unavailable", because the connection was removed first. Create the connection again with the same name and project, and the deletion completes. For an in-cluster bucket, the object storage server must be ready, so check **Settings**, **Object storage**.

**An app cannot read its bucket.**
Confirm these items:

- The app has the four values from the Secret.
- The app uses path-style addressing for an in-cluster bucket.
- You deployed the app after you saved its variables.

The access key can reach only the buckets listed on the resource.

## Related

- [Storage and volumes](/docs/guides/storage/)
- [Network isolation](/docs/guides/network-isolation/)
- [Compliance node tags](/docs/guides/compliance-tags/)
- [Command line](/docs/guides/command-line/)
- [Connect AWS](/docs/guides/connectors/aws/)
- [Environment variables](/docs/guides/environment-variables/)
- [Backups and restore](/docs/guides/backups-and-restore/), which uses its own S3 bucket and is separate from bucket resources
- [Users and tokens](/docs/guides/users-and-tokens/)
