---
title: "Object storage"
description: "Choose SeaweedFS or Garage for the cluster's object storage, create buckets on it or in AWS S3, give apps their credentials, and understand deletion and limits."
---

A bucket is a place to keep files that your apps read and write over the S3 API: uploads, exports, static assets. Geass offers two kinds. An in-cluster bucket lives on an object storage server that Geass runs on your nodes. It is either SeaweedFS or Garage, and an admin chooses one for the whole cluster. An AWS S3 bucket lives in your own AWS account, and Geass creates a restricted IAM user for it. Each bucket resource belongs to one project environment and has its own credentials.

## The two kinds of bucket

| | In-cluster bucket | AWS S3 bucket |
| --- | --- | --- |
| Stored on | The cluster's object storage server (SeaweedFS or Garage), on one volume that you size when you set the server up (10 GiB by default) | Your AWS account |
| Needs first | The object storage server set up under **Settings**, **Object storage** | An AWS connection scoped to the project |
| Credentials | A login on the server, `geass-<resource ID>`, with its own access key and secret, limited to its bucket | An IAM user `geass-<resource ID>`, limited to its bucket |
| Name it has there | `<project ID>.<environment>.<name>` | The name you set |
| Endpoint apps use | `http://geass-s3.geass-system.svc:8333` (SeaweedFS) or `http://geass-s3.geass-system.svc:3900` (Garage) | `https://s3.<region>.amazonaws.com` |
| Size limit | The server's volume, shared by every bucket. It can be made bigger later, never smaller | Whatever your AWS account allows |
| Geass-managed copies or backups | None | None (use AWS features such as versioning if you need them) |

## Before you start

For an in-cluster bucket, a dashboard admin must set up the object storage server once for the whole cluster. See [Choosing SeaweedFS or Garage](#choosing-seaweedfs-or-garage) and [Set up the object storage server](#set-up-the-object-storage-server).

For an AWS bucket, add an AWS connection to the project in **Settings**, **Connectors**. The connection is scoped to one project. See [Connect AWS](/docs/guides/connectors/aws/). The keys must let Geass create and delete IAM users, put user policies, create access keys, and create, list and delete S3 buckets and objects. Concretely Geass calls these IAM actions: `CreateUser`, `PutUserPolicy`, `CreateAccessKey`, `ListAccessKeys`, `DeleteAccessKey`, `DeleteUserPolicy` and `DeleteUser`. On S3 it creates buckets, lists objects, versions and multipart uploads, deletes objects and versions, aborts multipart uploads and deletes buckets.

## Choosing SeaweedFS or Garage

Geass can run either server as the cluster's object storage. Apps see the same thing with both: an S3 endpoint, a login limited to their own bucket, and a connection Secret with the same four keys. The two differ in what they ask of a node and in how much of S3 they cover.

| | SeaweedFS | Garage |
| --- | --- | --- |
| Memory when idle | About 60 MB | About 6 MB |
| S3 coverage | Broad: multipart uploads, presigned links, conditional writes (`If-Match` and `If-None-Match`), server-side encryption, object versioning, object lock, object tagging and bucket policies | Core S3: upload, download, list, delete, multipart uploads and presigned links. No object versioning, tagging, object lock or bucket policies, and server-side encryption is limited |
| Best for | Apps that use less common S3 features, and bigger or busier workloads | Small clusters, low-memory nodes, plain uploads and static assets |
| Make-up | One process running four components | One small binary |
| License | Apache-2.0 | AGPL-3.0 (Geass runs it unmodified) |

If you are not sure, choose SeaweedFS: it works with the widest range of S3 clients. Choose Garage when your nodes are small and your apps only upload, download and list files.

Geass gives each app a login limited to the objects of its own bucket. Bucket-level settings such as versioning, policies and lifecycle rules are therefore not something an app can change today, whichever server you choose.

The choice is made once, and only one server can exist at a time. Switching later means deleting every in-cluster bucket, removing the server and setting up the other one. Geass has no migration tool and does not copy data. See [Switch between SeaweedFS and Garage](#switch-between-seaweedfs-and-garage).

## Set up the object storage server

Only dashboard admins can do this, and only once per cluster.

1. Open **Settings**, then **Object storage**.
2. The page shows a card for **SeaweedFS** and one for **Garage**, with a comparison of the two below them. Select **Set up** on the one you want.
3. Choose **CPU**, **Memory** and **Storage**. The defaults are 0.25 CPU and 512 MB for SeaweedFS, and 0.1 CPU and 256 MB for Garage. **Storage** is the size of the server's volume: 10 GiB by default, and never less than 1 GiB. The dashboard lists sizes as GB, which are gibibytes, as it does for databases. If the cluster cannot fit the CPU and memory, the page tells you to open cluster capacity first.
4. Select **Set up SeaweedFS server** or **Set up Garage server**. The dashboard confirms with "SeaweedFS server created" or "Garage server created".

The page now shows the server with three tabs.

| Tab | What it shows |
| --- | --- |
| **Details** | Provider (SeaweedFS or Garage), status (Setting up, Starting, Waiting for capacity, Storage unavailable, Active, Failed), CPU, memory, storage, and how many project buckets use the server |
| **Manage** | **Size** (CPU and memory, saved with **Save size**), **Storage** (the volume, saved with **Save storage**) and **Remove object storage** |
| **Logs** | The server's recent output, with a **Refresh** button |

Changing the CPU or memory restarts the server with the new limits. The server runs as one server on one node, with one volume. Whether that volume is copied to other nodes depends on the cluster's storage: see [Where the server's data is kept](#where-the-servers-data-is-kept). You can make the volume bigger later: see [Make the volume bigger](#make-the-volume-bigger).

To remove the server, open **Manage**, select **Remove SeaweedFS** or **Remove Garage** under **Remove object storage**, type `remove`, and select **Remove permanently**. Geass refuses while any in-cluster project bucket exists and lists the buckets that block removal. Delete those buckets from their projects first. External S3 buckets are not affected. Removing the server deletes it, its `geass-s3` network policy and all the data on its volume.

To set the server up with the API instead, send `cluster=on` and the `engine`, `SeaweedFS` or `Garage` (in any letter case). The optional fields are `cpu`, `memory`, `storageSize` (default `10Gi`, at least `1Gi`), `name` (default `cluster-s3`), `region` and `runOnTags`:

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

The reply is `{"ok":true}`. The server's region and tags are chosen when it is created, and its volume stays where it is created. The server's recent output is also available at `GET /api/object-stores/<name>/logs`.

### Where the server's data is kept

The server's volume is made once, when the server is first created, and Geass decides then which storage class it uses. The decision is kept: a volume's class cannot change later, and a server that already has a volume keeps that volume's class.

| Cluster | The volume is on | Where the data is |
| --- | --- | --- |
| Has Geass's replicated storage (the Longhorn add-on, which the installer turns on) | The replicated class, like database volumes | Several nodes, up to three. A server placed in a region keeps its copies in that region. A server placed on nodes with tags (the `runOnTags` it was set up with, such as a compliance tag) keeps its copies on nodes that carry those tags, so its data is never replicated onto other nodes. The cluster creates the matching storage class itself |
| Has no Longhorn, such as a local `kind` cluster | The cluster's default storage class (`local-path` on K3s) | One node, the one the server's pod runs on |

Copies on several nodes keep the data when a node is lost. They are not a backup: deleting an object deletes it from every copy, and Geass makes no backup of bucket contents. See [Storage and volumes](/docs/guides/storage/) for the classes and [Compliance tags and the object storage server](#compliance-tags-and-the-object-storage-server) for tags.

While the replicated storage is still being installed, a new server reports "Storage is starting". Geass waits instead of creating the volume on local storage, because a volume cannot move to another class afterwards. If the replicated storage never comes up, the server reports "Storage is unavailable". See [Troubleshooting](#troubleshooting).

### Make the volume bigger

You can make the server's volume bigger. You cannot make it smaller. It can grow only where the cluster's storage can be resized: a cluster with Geass's replicated storage (Longhorn) can resize it, and the default K3s `local-path` storage cannot. On a cluster that cannot, the **Storage** section says "This cluster's storage can't be resized." and its list is disabled.

1. Open **Settings**, then **Object storage**, then the **Manage** tab.
2. Under **Storage**, choose a larger **Size**. The list offers the current size and the larger ones of 1, 5, 10, 20, 50, 100, 200 and 500 GB (GiB values).
3. Select **Save storage**.

Geass grows the volume in place, so nothing is recreated. The server's current size is on the **Details** tab.

Through the API, send `storageSize` to the update of the server (its name is `cluster-s3` unless you chose another):

```bash
curl -X POST https://geass.example.com/api/object-stores/cluster-s3/update \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json" \
  --data-urlencode storageSize=50Gi
```

The reply is `{"ok":true}`. A request Geass refuses returns `400`, with one of these messages as `error`:

| Message | Meaning |
| --- | --- |
| storage size must be a quantity such as 10Gi | The value could not be read |
| storage size must be at least 1Gi | Below the minimum |
| storage can be increased but not decreased (currently 10Gi) | You asked for less than the current size |
| this cluster's storage can't be resized, so the server stays at 10Gi | The volume's storage class does not allow growing |

The `platform` object of `GET /api/bootstrap` tells a client what to offer: `objectStorageResizable` says whether the volume can be made bigger, and `defaultStorage.objectstore` is the default size, `10Gi`. See the [REST API](/docs/reference/rest-api/).

### Switch between SeaweedFS and Garage

Geass keeps one server, chosen when you set it up, and it cannot be changed in place. To use the other one:

1. Copy out any files you want to keep with an S3 client. Geass does not move data between servers.
2. Delete every in-cluster bucket, from each project. Deleting a bucket deletes its objects.
3. Remove the server, as described above. This deletes everything on its volume.
4. Set up the other server.
5. Create the buckets again and point your apps at them.

## Create a bucket

1. Open the project and environment. Select **+ Add resource**, then **Bucket**.
2. Choose **In-cluster bucket** ("Stored on this cluster with SeaweedFS", or with Garage) or **AWS S3** ("In your AWS account"). A row is greyed out, with the reason, when object storage is not set up or the project has no AWS connection.
3. Geass saves a draft and opens its **Settings** on the **Bucket** section.
4. Set **Name**, the bucket name. It is required. For an AWS bucket you can also choose the **Connection**, and turn on **Create bucket in AWS** so Geass creates the bucket in your account.
5. Select **Save changes**, then **Deploy changes** in the project's top bar.

If you deploy a draft whose bucket you never named, **Deploy changes** uses the resource ID as the bucket name when that is a valid bucket name. Deploying one bucket through `POST /api/object-stores/<ID>/deploy` with no bucket name set is refused with "set a bucket name in settings before you deploy".

### Bucket names

A bucket name has 3 to 63 characters: lowercase letters, numbers, dots and hyphens. It must start and end with a letter or number, must not contain two dots in a row, and must not look like an IP address.

Every project's in-cluster buckets live on one object storage server, where bucket names are global. So Geass stores an in-cluster bucket under `<project ID>.<environment>.<name>`, for example `proj-1a2b3c4d.production.uploads`. The parts are joined with dots because a project ID or an environment cannot contain one, so no two combinations ever give the same name. Two projects, or two environments of one project, that both name a bucket `uploads` get two separate buckets, and each one's login can reach only its own. Use path-style addressing, as for any in-cluster bucket. The `bucket` key of the bucket's Secret holds the name as stored, so have your app read it from there. **Add reference** does.

The prefix counts towards the 63 characters. A project whose ID and environment take 25 characters, such as `proj-1a2b3c4d.production.`, leaves 38 for the name you set. Geass refuses a longer name when you save it and says what the limit is.

:::caution
AWS bucket names are global across all AWS accounts, and Geass does not add a prefix to them. Choose a name that is unique to you, for example `acme-storefront-uploads`.
:::

### Create a bucket with the REST API

Use a workspace token with the admin role. Project tokens cannot create resources. See [Users and tokens](/docs/guides/users-and-tokens/). The resource is created as a draft, so deploy it afterwards.

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

For an in-cluster bucket, send `placement=InCluster` and leave out `connectionRef`, `region` and `createBucket`. You do not need to send `engine`: the bucket uses the engine of the cluster's server. If you leave out `bucket`, the resource ID is the bucket name, and it must be a valid bucket name (see [Bucket names](#bucket-names)).

The versioned API takes the same fields as JSON, at `POST /api/v1/object-stores`, and the command line calls it:

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

Find the project's ID in the address of its pages (`/projects/proj-1a2b3c4d`).

## Compliance tags and the object storage server

A project can require compliance node tags, such as `hipaa`, so that everything it runs stays on nodes you marked. See [Compliance node tags](/docs/guides/compliance-tags/). The cluster object storage server belongs to no project, so a project's tags do not place it. The server is placed once, when it is first set up, with its own region and **Run on** tags, and it does not move afterwards. The setup page has no field for them. Send `region` and `runOnTags` when you create the server through the API, as shown under [Set up the object storage server](#set-up-the-object-storage-server). Geass checks that a node in that region carries each tag.

A project that requires tags can keep buckets on the server only when the server runs on nodes with every one of them. A bucket on a server that does not run there is refused with "This object storage server does not run on nodes tagged hipaa, which the project requires", and the refusal counts as a failure. The check is part of provisioning.

The server's volume follows the tags the server runs on. With Geass's replicated storage (Longhorn), the volume is created on a class that keeps all its copies on nodes in the server's region that carry every tag it was set up with, so its data is never replicated onto other nodes. The cluster creates that storage class itself, once every node it covers has a ready Longhorn manager. Without Longhorn the volume is on the cluster's default class (`local-path` on K3s), which keeps it on the node its pod runs on, and the pod runs only on the tagged nodes. See [Where the server's data is kept](#where-the-servers-data-is-kept) and [Storage and volumes](/docs/guides/storage/#compliance-tags-and-storage-classes). A server that runs on compliance nodes is not reserved for the projects that require the tag: Geass checks only that the project's tags are on the server.

AWS S3 buckets live in your AWS account, outside the cluster, so required tags do not reach them.

## Credentials and how apps receive them

When a bucket is ready, Geass writes a Secret named `<resource ID>-connection` in the project environment's namespace (`<project ID>-<environment>`).

| Key | Value |
| --- | --- |
| `endpoint` | The S3 endpoint, as in the table above |
| `accessKey` | For an in-cluster bucket, the access key ID of the bucket's own login on the server. For AWS, the IAM user's access key ID |
| `secretKey` | The matching secret. Generated once and kept |
| `bucket` | The bucket name as stored: for an in-cluster bucket `<project ID>.<environment>.<name>`. With several names in `buckets`, the first |

The credentials are limited to the bucket (or buckets) the resource names. For an in-cluster bucket, the server creates the access key and secret, so they do not look like the resource's name. Geass never gives an app the server's own admin login. It stays in `<server name>-root` in `geass-system` (`cluster-s3-root` by default). If the server is reset and forgets a bucket's login, Geass issues a new key on its next pass and updates the Secret. Apps that already started keep the old values until they restart.

Use path-style addressing in your S3 client with an in-cluster bucket: both servers require it. Garage's region is `us-east-1`, which is the default of most S3 clients, so your app does not need to set one. SeaweedFS accepts any region.

The **Add reference** action on an app's **Variables** tab adds all four values for a bucket as variables that read the Secret, so the app gets the endpoint, the keys and the stored bucket name together:

| Variable | Reads |
| --- | --- |
| `S3_ENDPOINT` | `endpoint` |
| `S3_ACCESS_KEY_ID` | `accessKey` |
| `S3_SECRET_ACCESS_KEY` | `secretKey` |
| `S3_BUCKET` | `bucket` |

Adding the reference again replaces these variables when they were added the same way. If the service already has a variable with one of these names that someone set by hand, a plain environment variable or one under **Variables**, Geass refuses and names it, so remove that variable first. Deploy the app afterwards. See [Environment variables](/docs/guides/environment-variables/#reference-a-database-or-bucket).

Read the values:

```bash
NS=proj-1a2b3c4d-production
for key in endpoint accessKey secretKey bucket; do
  printf '%s=' "$key"
  kubectl get secret uploads-connection -n "$NS" -o jsonpath="{.data.$key}" | base64 -d
  echo
done
```

Deploy the app after you save its variables. See [Environment variables](/docs/guides/environment-variables/).

The in-cluster endpoint resolves only inside the cluster. To reach it from your machine, forward the port of the server you chose:

```bash
# SeaweedFS
kubectl port-forward -n geass-system svc/geass-s3 8333:8333

# Garage
kubectl port-forward -n geass-system svc/geass-s3 3900:3900
```

## Browse and manage files

The dashboard has no file browser for buckets. A bucket resource has an **Overview**, a **Deployments** tab and **Settings**. Use any S3 client, such as the AWS CLI, `mc` or `rclone`, with the endpoint and keys from the Secret. For an AWS bucket you can also use the AWS console.

## Rename a bucket

Changing **Name** on a deployed bucket is a pending change. When you deploy it, Geass creates the new bucket and points the credentials at it. Geass does not rename or delete the old bucket, and the bucket's login no longer has access to it. Move the files yourself and remove the old bucket.

## Delete a bucket

Open the bucket's **Settings**, then **Delete bucket**, type its resource ID and confirm. The effect depends on the kind.

| Kind | What Geass removes |
| --- | --- |
| In-cluster | The bucket and every object in it, the bucket's login on the server and its Secrets. This cannot be undone and there is no trash |
| AWS, **Create bucket in AWS** on | The bucket with all object versions and unfinished multipart uploads, then the IAM user with its keys and policy |
| AWS, **Create bucket in AWS** off | Only the IAM user, its keys and policy. The bucket and its files stay in your account |

:::danger
Geass does not record whether it created an AWS bucket or found it already in your account. With **Create bucket in AWS** on, deleting the resource deletes the bucket and everything in it, even a bucket that existed before. Turn the switch off for a bucket you want to keep.
:::

A bucket you never deployed is removed without touching the object storage server or AWS, because Geass created nothing yet. For a bucket that was deployed but never became ready, Geass tries to remove whatever it had already created, such as the bucket on the server or the bucket and IAM user in AWS. That cleanup is best effort: if it fails, for example because the AWS key lacks a permission, the deletion still completes and the leftovers stay where they are. One that stopped before it created anything, for example because the object storage server was not set up, is removed without calling the server or AWS. A bucket that has been ready is different: its deletion waits until the cleanup succeeds. If you saved a new name and did not deploy it, Geass removes the bucket that was deployed, not the new name.

Delete a project's buckets and wait for them to disappear before you delete the project. Deleting a project also deletes its AWS connections, and Geass needs the connection to clean up an AWS bucket's user.

## Command line and AI agents

The `geass` command line and its MCP server list and inspect buckets. They never show credentials, and they do not browse files, create a bucket or set up the object storage server.

| To | Command | MCP tool |
| --- | --- | --- |
| List buckets | `geass stores list` | `list_buckets` |
| Read one bucket's settings and status | `geass stores get uploads` | None |
| Create or deploy a bucket | `geass api POST /object-stores ...`, as above | None |
| Delete a bucket | `geass api DELETE /object-stores/uploads -q confirmName=uploads` | `delete_resource` with `kind` set to `object-stores`, only with `--allow-delete` |

`geass stores` is also called `geass buckets` and `geass object-stores`. The list includes the cluster object storage server, which has no project and is shown as `draft` even while it runs, because nothing sets `spec.deploy.enabled` on it. `list` and `get` work with any workspace token. A token that cannot change things does not get the bucket names, the connection, the region or the endpoint, and a project token cannot read buckets at all. There is no command that returns a bucket's access key or secret key: read them from the Secret, as under [Credentials and how apps receive them](#credentials-and-how-apps-receive-them).

Deleting through the command line or an agent does exactly what deleting in the dashboard does, so the warnings under [Delete a bucket](#delete-a-bucket) apply. See [Command line](/docs/guides/command-line/), [CLI commands](/docs/reference/cli/#geass-stores) and [MCP tools](/docs/reference/mcp-tools/#list_buckets).

## Quotas and sizing

- Geass sets no per-bucket quota and no object limit.
- All in-cluster buckets share the server's single volume. It is 10 GiB unless you chose another size when you set the server up, and at least 1 GiB.
- You can make the volume bigger, never smaller, and only where the cluster's storage can be resized. See [Make the volume bigger](#make-the-volume-bigger). With Geass's replicated storage the data is kept on up to three nodes. Without it, the data is on one node.
- The server's CPU, memory and storage are the only server settings you can change. The default is 0.25 CPU and 512 MB for SeaweedFS, and 0.1 CPU and 256 MB for Garage.
- For AWS, Geass applies no limit. AWS account limits and your storage bill apply.
- One bucket resource can list up to 32 bucket names. The dashboard sets one.

## Options

| Field | Meaning | Default |
| --- | --- | --- |
| `project`, `environment` | The project environment that owns the bucket. Empty `project` means the cluster object storage server | None |
| `engine` | `SeaweedFS` or `Garage` for the cluster server and the buckets on it, `S3` for an AWS bucket. A bucket on the cluster server uses the server's engine | None (required) |
| `placement` | `InCluster` or `External`. `S3` is always external | `InCluster` |
| `connectionRef` | The AWS connection (name), for AWS buckets | None |
| `region` | AWS region for the bucket. The dashboard has no field for it | The connection's region, then `us-east-1` |
| `createBucket` | Create (and on delete, remove) the bucket in AWS | `false` |
| `buckets` | Bucket names, up to 32, each 3 to 63 characters | None. The bucket name must be set before deploy |
| `resources` | CPU and memory of the object storage server (the cluster server only) | 0.25 CPU, 512 MB for SeaweedFS. 0.1 CPU, 256 MB for Garage |
| `storageSize` | Size of the object storage server's volume, for example `20Gi` (the cluster server only). It can be raised later, never lowered, and only where the cluster's storage can be resized. The operator writes the value onto the resource | `10Gi`, and at least `1Gi` |
| `serverRegion`, `runOn.tags` | Where the object storage server runs (the cluster server only, set through the API, fixed when it is first set up). A project that requires tags can use the server only when `runOn.tags` covers them | The default region, any node |

The full schema is in the [resource reference](/docs/reference/resources/overview/).

## What happens under the hood

- The cluster object storage server is a `GeassObjectStore` with no project, in `geass-system`. It is named `cluster-s3` unless you chose another name through the API. Geass runs the server itself from a pinned image, with no Helm chart: a Deployment of one pod, a volume claim and a Service, all named `geass-s3` in `geass-system`, and a network policy with the same name. The claim is 10 GiB unless you chose another size. It is on Geass's replicated storage class when the cluster has Longhorn, and on the cluster's default class otherwise.
- The Service publishes the S3 port, 8333 for SeaweedFS or 3900 for Garage. For Garage it also publishes the admin port, 3903, which Geass uses to make buckets and keys. SeaweedFS serves internal services beside S3 that have no login of their own, so it listens only on localhost and a small second container in the same pod forwards the S3 port and nothing else. Both servers run as a non-root user.
- The network policy `geass-s3` in `geass-system` selects the server's pod and allows ingress only to the ports that are meant to be reached. The S3 port (8333 for SeaweedFS, 3900 for Garage) is open to every namespace, because that is how apps reach their buckets, and each project's login limits it to its own bucket. Garage's admin port, 3903, is open only to the platform's own namespace, `geass-system`. Every other port is closed. The policy takes effect on clusters whose network enforces policies, which K3s's does. The server also closes those ports itself: SeaweedFS listens on localhost with a forwarder container publishing S3, and Garage's other ports are closed. The policy is a second lock. Geass puts it back if it is changed or deleted by hand.
- The server's own login is in the Secret `<server name>-root` (`cluster-s3-root` by default) in `geass-system`. The files the server reads at start are in the Secret `geass-s3-config`.
- For each in-cluster bucket, Geass creates the bucket under its stored name with the server's login, then a login for it that can reach only that bucket: a user `geass-<resource ID>` made through SeaweedFS's IAM API, or a key made through Garage's admin API. The server generates the access key and secret, and Geass keeps them in the connection Secret.
- For each AWS bucket, Geass optionally creates the bucket, then the IAM user `geass-<resource ID>` with an inline policy named `geass-bucket`, and one access key. It reuses the stored key on later reconciles, so it does not create a new one each time. Geass changes no other bucket setting: it sends only the create request, so AWS's defaults for a new bucket apply.
- The connection Secret `<resource ID>-connection` lives in the project environment's namespace.
- Just before it first creates a bucket or a login, Geass sets the annotation `geass.dev/provisioning-started` on the resource. It is how deleting a bucket that never became ready knows there is something to remove. Do not edit it.

```bash
kubectl get geassobjectstores -n geass-system
kubectl get geassobjectstore uploads -n geass-system \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}{"\n"}'
kubectl get deployment geass-s3 -n geass-system
kubectl get pods -n geass-system
```

## Troubleshooting

**A bucket shows "Draft" with the message "Resource is configured and waiting for deployment".**
The bucket is still a draft, or has not been deployed. Set its **Name** in **Settings**, **Bucket**, save, then select **Deploy changes** in the project's top bar. Geass creates nothing for a draft. Once you deploy, the message changes to ready for an in-cluster bucket. Check the message with the second command above.

**"bucket name ... is too long: on the cluster's object storage it is stored as ...".**
An in-cluster bucket is stored behind its project ID and environment, and the two together have to fit in 63 characters. The message says how many characters the name can have. Shorten the name in **Settings**, **Bucket** and save.

**"Set up object storage in cluster settings first" or "Set up object storage in Platform settings, then try again."**
There is no cluster object storage server. Set it up under **Settings**, **Object storage** and wait for **Active**. "Object storage is starting" means the server exists and is not ready yet.

**"Configure the bucket name in settings, then deploy" or "set a bucket name in settings before you deploy".**
The bucket's **Name** is empty. Set it in **Settings**, **Bucket** and save.

**"This object storage server does not run on nodes tagged hipaa, which the project requires".**
The project requires node tags that the server is not placed on, and the server cannot move. Remove the server (after deleting every project bucket) and set it up again with `runOnTags`, as under [Compliance tags and the object storage server](#compliance-tags-and-the-object-storage-server).

**The server stays at "Starting", or its pod is in `ImagePullBackOff`.**
Run `kubectl describe pod -n geass-system` on the `geass-s3` pod and read the events. A node that cannot pull the pinned image from Docker Hub never starts the server. Check the node's network and registry access.

**The server shows "Starting", and its condition reads "Storage is starting".**
The cluster's replicated storage (Longhorn) is still being installed, or the storage class for the server's region or tags is not made yet. Geass waits instead of creating the volume on local storage. Run `kubectl get pods -n longhorn-system` and `kubectl get storageclass`. A node without the iSCSI initiator holds Longhorn back. See [Storage and volumes](/docs/guides/storage/#troubleshooting).

**The server shows "Storage unavailable", and its condition reads "Storage is unavailable".**
The replicated storage did not come up in 15 minutes. The message counts as a failure, so after repeated errors the server reports that provisioning failed, and Geass stops retrying until you retry it. Fix Longhorn (see the check above), then retry with `POST /api/object-stores/<name>/retry-provision`. The volume has not been created, so nothing is lost.

**Saving a larger storage size says "this cluster's storage can't be resized, so the server stays at 10Gi".**
The server's volume is on a storage class that cannot grow, such as the default `local-path` class of K3s. The size cannot be changed, and a volume cannot be moved to another class. Choose the size you need when you set the server up. To change it you would remove the server, which deletes the data (see [Switch between SeaweedFS and Garage](#switch-between-seaweedfs-and-garage)), and set it up again.

**The object storage server shows "Waiting for capacity".**
The cluster has no node with room for its CPU and memory. Choose a smaller size under **Manage**, or add capacity. See [Usage and capacity](/docs/guides/usage-and-capacity/).

**"An AWS connection is required for external buckets" or "Connect your cloud account in Platform settings to use an external bucket."**
The bucket has no AWS connection, or the connection is missing credentials. Choose a connection in **Settings**, **Bucket**. It must belong to the same project.

**An AWS bucket fails with an access-denied error.**
The connection's keys lack an IAM or S3 permission from the list under [Before you start](#before-you-start). The message includes AWS's error code.

**A bucket stays at "Deleting".**
Geass could not finish cleanup. For an AWS bucket the cause is usually "AWS connection is unavailable", because the connection was removed first. Recreate the connection with the same name and project, and the deletion completes. For an in-cluster bucket the object storage server must be ready, so check **Settings**, **Object storage**.

**An app cannot read its bucket.**
Confirm the app has the four values from the Secret, that it uses path-style addressing for an in-cluster bucket, and that you deployed the app after saving its variables. The access key can reach only the buckets listed on the resource.

## Related

- [Storage and volumes](/docs/guides/storage/)
- [Network isolation](/docs/guides/network-isolation/)
- [Compliance node tags](/docs/guides/compliance-tags/)
- [Command line](/docs/guides/command-line/)
- [Connect AWS](/docs/guides/connectors/aws/)
- [Environment variables](/docs/guides/environment-variables/)
- [Backups and restore](/docs/guides/backups-and-restore/), which uses its own S3 bucket and is separate from bucket resources
- [Users and tokens](/docs/guides/users-and-tokens/)
