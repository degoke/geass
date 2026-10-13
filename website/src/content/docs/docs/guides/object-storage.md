---
title: "Object storage"
description: "Create buckets on the cluster's MinIO server or in your own AWS S3 account, give apps their credentials, and understand deletion, sizing and limits."
---

A bucket is a place to keep files that your apps read and write over the S3 API: uploads, exports, static assets. Geass offers two kinds. An in-cluster bucket lives on a MinIO server that Geass runs on your nodes. An AWS S3 bucket lives in your own AWS account, and Geass creates a restricted IAM user for it. Each bucket resource belongs to one project environment and has its own credentials.

## The two kinds of bucket

| | In-cluster bucket | AWS S3 bucket |
| --- | --- | --- |
| Stored on | The cluster's MinIO server, one 10 GiB volume | Your AWS account |
| Needs first | The MinIO server set up under **Settings**, **Object storage** | An AWS connection scoped to the project |
| Credentials | A MinIO user named after the bucket resource, limited to its bucket | An IAM user `geass-<resource ID>`, limited to its bucket |
| Name it has there | `<project ID>.<environment>.<name>` | The name you set |
| Endpoint apps use | `http://geass-minio.geass-system.svc:9000` | `https://s3.<region>.amazonaws.com` |
| Size limit | The server's 10 GiB volume, shared by every bucket | Whatever your AWS account allows |
| Geass-managed copies or backups | None | None (use AWS features such as versioning if you need them) |

## Before you start

For an in-cluster bucket, a dashboard admin must set up the MinIO server once for the whole cluster. See [Set up the MinIO server](#set-up-the-minio-server).

For an AWS bucket, add an AWS connection to the project in **Settings**, **Connectors**. The connection is scoped to one project. See [Connect AWS](/docs/guides/connectors/aws/). The keys must let Geass create and delete IAM users, put user policies, create access keys, and create, list and delete S3 buckets and objects. Concretely Geass calls these IAM actions: `CreateUser`, `PutUserPolicy`, `CreateAccessKey`, `ListAccessKeys`, `DeleteAccessKey`, `DeleteUserPolicy` and `DeleteUser`. On S3 it creates buckets, lists objects, versions and multipart uploads, deletes objects and versions, aborts multipart uploads and deletes buckets.

## Set up the MinIO server

Only dashboard admins can do this, and only once per cluster.

1. Open **Settings**, then **Object storage**.
2. On the **MinIO** card, select **Set up**. The page also shows **Garage SFS** and **SeaweedFS** cards marked "Coming soon". They are not built.
3. Choose **CPU** and **Memory**. The defaults are 0.25 CPU and 512 MB. If the cluster cannot fit that size, the page tells you to open cluster capacity first.
4. Select **Set up MinIO server**. The dashboard confirms with "MinIO server created".

The page now shows the server with three tabs.

| Tab | What it shows |
| --- | --- |
| **Details** | Provider, status (Setting up, Installing, Starting, Waiting for capacity, Active, Failed), CPU, memory, and how many project buckets use the server |
| **Manage** | **Size** (CPU and memory, saved with **Save size**) and **Remove cluster storage** |
| **Logs** | The server's recent output, with a **Refresh** button |

Changing the size restarts MinIO with the new limits. The server's volume is fixed at 10 GiB and runs as a single copy on one node. It uses the cluster default storage class, so it is not replicated (see [Storage and volumes](/docs/guides/storage/)).

To remove the server, open **Manage**, select **Remove MinIO**, type `remove`, and select **Remove permanently**. Geass refuses while any in-cluster project bucket exists and lists the buckets that block removal. Delete those buckets from their projects first. External S3 buckets are not affected.

To set the server up with the API instead, send `cluster=on`. The optional fields are `cpu`, `memory`, `name` (default `cluster-minio`), `region` and `runOnTags`:

```bash
curl -X POST https://geass.example.com/api/object-stores/create \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json" \
  --data-urlencode cluster=on \
  --data-urlencode cpu=500m \
  --data-urlencode memory=1Gi
```

The reply is `{"ok":true}`. The server's region and tags are chosen when it is created, and its volume stays where it is created.

## Create a bucket

1. Open the project and environment. Select **+ Add resource**, then **Bucket**.
2. Choose **In-cluster bucket** ("Stored on this cluster with MinIO") or **AWS S3** ("In your AWS account"). A row is greyed out, with the reason, when MinIO is not set up or the project has no AWS connection.
3. Geass saves a draft and opens its **Settings** on the **Bucket** section.
4. Set **Name**, the bucket name. It is required. For an AWS bucket you can also choose the **Connection**, and turn on **Create bucket in AWS** so Geass creates the bucket in your account.
5. Select **Save changes**, then **Deploy changes** in the project's top bar.

If you deploy a draft whose bucket you never named, **Deploy changes** uses the resource ID as the bucket name when that is a valid bucket name. Deploying one bucket through `POST /api/object-stores/<ID>/deploy` with no bucket name set is refused with "set a bucket name in settings before you deploy".

### Bucket names

A bucket name has 3 to 63 characters: lowercase letters, numbers, dots and hyphens. It must start and end with a letter or number, must not contain two dots in a row, and must not look like an IP address.

Every project's in-cluster buckets live on one MinIO server, where bucket names are global. So Geass stores an in-cluster bucket under `<project ID>.<environment>.<name>`, for example `proj-1a2b3c4d.production.uploads`. The parts are joined with dots because a project ID or an environment cannot contain one, so no two combinations ever give the same name. Two projects, or two environments of one project, that both name a bucket `uploads` get two separate buckets, and each one's user can reach only its own. Use path-style addressing, as for any in-cluster bucket. The `bucket` key of the bucket's Secret holds the name as stored, so have your app read it from there. **Add reference** does.

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

For an in-cluster bucket, send `engine=MinIO` and `placement=InCluster` and leave out `connectionRef`, `region` and `createBucket`. If you leave out `bucket`, the resource ID is the bucket name, and it must be a valid bucket name (see [Bucket names](#bucket-names)).

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

## Compliance tags and the MinIO server

A project can require compliance node tags, such as `hipaa`, so that everything it runs stays on nodes you marked. See [Compliance node tags](/docs/guides/compliance-tags/). The cluster MinIO server belongs to no project, so a project's tags do not place it. The server is placed once, when it is first set up, with its own region and **Run on** tags, and it does not move afterwards. The setup page has no field for them. Send `region` and `runOnTags` when you create the server through the API, as shown under [Set up the MinIO server](#set-up-the-minio-server). Geass checks that a node in that region carries each tag.

A project that requires tags can keep buckets on the server only when the server runs on nodes with every one of them. A bucket on a server that does not run there is refused with "This MinIO server does not run on nodes tagged hipaa, which the project requires", and the refusal counts as a failure. The check is part of provisioning.

The server's volume uses the cluster's default storage class. On K3s that is `local-path`, which keeps the volume on the node its pod runs on, so a server placed on the tagged nodes keeps its data there. See [Storage and volumes](/docs/guides/storage/). A server that runs on compliance nodes is not reserved for the projects that require the tag: Geass checks only that the project's tags are on the server.

AWS S3 buckets live in your AWS account, outside the cluster, so required tags do not reach them.

## Credentials and how apps receive them

When a bucket is ready, Geass writes a Secret named `<resource ID>-connection` in the project environment's namespace (`<project ID>-<environment>`).

| Key | Value |
| --- | --- |
| `endpoint` | The S3 endpoint, as in the table above |
| `accessKey` | For an in-cluster bucket, the bucket resource's ID. For AWS, the IAM user's access key ID |
| `secretKey` | The matching secret. Generated once and kept |
| `bucket` | The bucket name as stored: for an in-cluster bucket `<project ID>.<environment>.<name>`. With several names in `buckets`, the first |

The credentials are limited to the bucket (or buckets) the resource names. For an in-cluster bucket, Geass never gives an app the MinIO root credentials. They stay in `cluster-minio-root` in `geass-system`. Use path-style addressing in your S3 client with an in-cluster bucket, because the MinIO server is not configured with a bucket domain.

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

The in-cluster endpoint resolves only inside the cluster. To reach it from your machine, forward the port:

```bash
kubectl port-forward -n geass-system svc/geass-minio 9000:9000
```

## Browse and manage files

The dashboard has no file browser for buckets. A bucket resource has an **Overview**, a **Deployments** tab and **Settings**. Use any S3 client, such as the AWS CLI, `mc` or `rclone`, with the endpoint and keys from the Secret. For an AWS bucket you can also use the AWS console.

## Rename a bucket

Changing **Name** on a deployed bucket is a pending change. When you deploy it, Geass creates the new bucket and points the credentials at it. Geass does not rename or delete the old bucket, and the bucket's user no longer has access to it. Move the files yourself and remove the old bucket.

## Delete a bucket

Open the bucket's **Settings**, then **Delete bucket**, type its resource ID and confirm. The effect depends on the kind.

| Kind | What Geass removes |
| --- | --- |
| In-cluster | The bucket and every object in it, the bucket's MinIO user and its Secrets. This cannot be undone and there is no trash |
| AWS, **Create bucket in AWS** on | The bucket with all object versions and unfinished multipart uploads, then the IAM user with its keys and policy |
| AWS, **Create bucket in AWS** off | Only the IAM user, its keys and policy. The bucket and its files stay in your account |

:::danger
Geass does not record whether it created an AWS bucket or found it already in your account. With **Create bucket in AWS** on, deleting the resource deletes the bucket and everything in it, even a bucket that existed before. Turn the switch off for a bucket you want to keep.
:::

A bucket you never deployed is removed without touching MinIO or AWS, because Geass created nothing yet. For a bucket that was deployed but never became ready, Geass tries to remove whatever it had already created, such as the bucket on MinIO or the bucket and IAM user in AWS. That cleanup is best effort: if it fails, for example because the AWS key lacks a permission, the deletion still completes and the leftovers stay where they are. One that stopped before it created anything, for example because the MinIO server was not set up, is removed without calling MinIO or AWS. A bucket that has been ready is different: its deletion waits until the cleanup succeeds. If you saved a new name and did not deploy it, Geass removes the bucket that was deployed, not the new name.

Delete a project's buckets and wait for them to disappear before you delete the project. Deleting a project also deletes its AWS connections, and Geass needs the connection to clean up an AWS bucket's user.

## Command line and AI agents

The `geass` command line and its MCP server list and inspect buckets. They never show credentials, and they do not browse files, create a bucket or set up the MinIO server.

| To | Command | MCP tool |
| --- | --- | --- |
| List buckets | `geass stores list` | `list_buckets` |
| Read one bucket's settings and status | `geass stores get uploads` | None |
| Create or deploy a bucket | `geass api POST /object-stores ...`, as above | None |
| Delete a bucket | `geass api DELETE /object-stores/uploads -q confirmName=uploads` | `delete_resource` with `kind` set to `object-stores`, only with `--allow-delete` |

`geass stores` is also called `geass buckets` and `geass object-stores`. The list includes the cluster MinIO server, which has no project and is shown as `draft` even while it runs, because nothing sets `spec.deploy.enabled` on it. `list` and `get` work with any workspace token. A token that cannot change things does not get the bucket names, the connection, the region or the endpoint, and a project token cannot read buckets at all. There is no command that returns a bucket's access key or secret key: read them from the Secret, as under [Credentials and how apps receive them](#credentials-and-how-apps-receive-them).

Deleting through the command line or an agent does exactly what deleting in the dashboard does, so the warnings under [Delete a bucket](#delete-a-bucket) apply. See [Command line](/docs/guides/command-line/), [CLI commands](/docs/reference/cli/#geass-stores) and [MCP tools](/docs/reference/mcp-tools/#list_buckets).

## Quotas and sizing

- Geass sets no per-bucket quota and no object limit.
- All in-cluster buckets share the MinIO server's single 10 GiB volume. Geass has no setting for growing it, and it is not replicated.
- MinIO's CPU and memory are the only server settings you can change. The default is 0.25 CPU and 512 MB.
- For AWS, Geass applies no limit. AWS account limits and your storage bill apply.
- One bucket resource can list up to 32 bucket names. The dashboard sets one.

## Options

| Field | Meaning | Default |
| --- | --- | --- |
| `project`, `environment` | The project environment that owns the bucket. Empty `project` means the cluster MinIO server | None |
| `engine` | `MinIO` or `S3` | None (required) |
| `placement` | `InCluster` or `External`. `S3` is always external | `InCluster` |
| `connectionRef` | The AWS connection (name), for AWS buckets | None |
| `region` | AWS region for the bucket. The dashboard has no field for it | The connection's region, then `us-east-1` |
| `createBucket` | Create (and on delete, remove) the bucket in AWS | `false` |
| `buckets` | Bucket names, up to 32, each 3 to 63 characters | None. The bucket name must be set before deploy |
| `resources` | CPU and memory of the MinIO server (the cluster server only) | 0.25 CPU, 512 MB |
| `serverRegion`, `runOn.tags` | Where the MinIO server runs (the cluster server only, set through the API, fixed when it is first set up). A project that requires tags can use the server only when `runOn.tags` covers them | The default region, any node |

The full schema is in the [resource reference](/docs/reference/resources/overview/).

## What happens under the hood

- The cluster MinIO server is a `GeassObjectStore` with no project, in `geass-system`. It is named `cluster-minio` unless you chose another name through the API. Geass installs MinIO as a `HelmChart` named `geass-minio` (chart 5.4.0, standalone mode) in `kube-system` with its Service and pod in `geass-system`. The chart pins the images `minio/minio:RELEASE.2025-02-18T16-25-55Z` and `minio/mc:RELEASE.2025-02-21T16-00-46Z`.
- Its root credentials are in the Secret `<server name>-root` (`cluster-minio-root` by default) in `geass-system`.
- For each in-cluster bucket, Geass creates the bucket under its stored name with the root credentials, then a user and a policy named `geass-<resource ID>` that allow only that bucket. The user's key is kept in `<resource ID>-minio-user` in `geass-system`. Geass re-renders the MinIO chart values to add the user, so you see "Installing MinIO chart" briefly.
- For each AWS bucket, Geass optionally creates the bucket, then the IAM user `geass-<resource ID>` with an inline policy named `geass-bucket`, and one access key. It reuses the stored key on later reconciles, so it does not create a new one each time. Geass changes no other bucket setting: it sends only the create request, so AWS's defaults for a new bucket apply.
- The connection Secret `<resource ID>-connection` lives in the project environment's namespace.
- Just before it first creates a bucket or a user, Geass sets the annotation `geass.dev/provisioning-started` on the resource. It is how deleting a bucket that never became ready knows there is something to remove. Do not edit it.

```bash
kubectl get geassobjectstores -n geass-system
kubectl get geassobjectstore uploads -n geass-system \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}{"\n"}'
kubectl get helmchart geass-minio -n kube-system
kubectl get pods -n geass-system
```

## Troubleshooting

**A bucket shows "Draft" with the message "Resource is configured and waiting for deployment".**
The bucket is still a draft, or has not been deployed. Set its **Name** in **Settings**, **Bucket**, save, then select **Deploy changes** in the project's top bar. Geass creates nothing for a draft. Once you deploy, the message changes to "Installing MinIO chart" for an in-cluster bucket and then to ready. Check the message with the second command above.

**"bucket name ... is too long: on the MinIO server it is stored as ...".**
An in-cluster bucket is stored behind its project ID and environment, and the two together have to fit in 63 characters. The message says how many characters the name can have. Shorten the name in **Settings**, **Bucket** and save.

**"Set up the MinIO server in cluster settings first" or "Turn on object storage in Platform settings, then try again."**
There is no cluster MinIO server, or it is not ready. Set it up under **Settings**, **Object storage** and wait for **Active**.

**"Configure the bucket name in settings, then deploy" or "set a bucket name in settings before you deploy".**
The bucket's **Name** is empty. Set it in **Settings**, **Bucket** and save.

**"This MinIO server does not run on nodes tagged hipaa, which the project requires".**
The project requires node tags that the MinIO server is not placed on, and the server cannot move. Remove the server (after deleting every project bucket) and set it up again with `runOnTags`, as under [Compliance tags and the MinIO server](#compliance-tags-and-the-minio-server).

**"Installing MinIO chart" for a long time, or the MinIO pod is in `ImagePullBackOff`.**
Run `kubectl describe pod -n geass-system` on the MinIO pod and read the events. A node that cannot pull the pinned MinIO images from Docker Hub never starts the server. Check the node's network and registry access.

**The MinIO server shows "Waiting for capacity".**
The cluster has no node with room for its CPU and memory. Choose a smaller size under **Manage**, or add capacity. See [Usage and capacity](/docs/guides/usage-and-capacity/).

**"An AWS connection is required for external buckets" or "Connect your cloud account in Platform settings to use an external bucket."**
The bucket has no AWS connection, or the connection is missing credentials. Choose a connection in **Settings**, **Bucket**. It must belong to the same project.

**An AWS bucket fails with an access-denied error.**
The connection's keys lack an IAM or S3 permission from the list under [Before you start](#before-you-start). The message includes AWS's error code.

**A bucket stays at "Deleting".**
Geass could not finish cleanup. For an AWS bucket the cause is usually "AWS connection is unavailable", because the connection was removed first. Recreate the connection with the same name and project, and the deletion completes. For an in-cluster bucket the MinIO server must be ready, so check **Settings**, **Object storage**.

**An app cannot read its bucket.**
Confirm the app has the four values from the Secret, that it uses path-style addressing for an in-cluster bucket, and that you deployed the app after saving its variables. The access key can reach only the buckets listed on the resource.

## Related

- [Storage and volumes](/docs/guides/storage/)
- [Compliance node tags](/docs/guides/compliance-tags/)
- [Command line](/docs/guides/command-line/)
- [Connect AWS](/docs/guides/connectors/aws/)
- [Environment variables](/docs/guides/environment-variables/)
- [Backups and restore](/docs/guides/backups-and-restore/), which uses its own S3 bucket and is separate from bucket resources
- [Users and tokens](/docs/guides/users-and-tokens/)
