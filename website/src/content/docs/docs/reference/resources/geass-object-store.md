---
title: "GeassObjectStore"
description: "Reference for GeassObjectStore: the cluster's object storage server (SeaweedFS or Garage), buckets on it for a project, or buckets in your own AWS S3 account."
sidebar:
  order: 5
---

A `GeassObjectStore` is object storage. It has one of three shapes:

- With no `spec.project`, it is the object storage server of the whole cluster. This server is the S3-compatible
  service that runs in your cluster. It is SeaweedFS or Garage.
- With a project and an in-cluster engine, it is a bucket for that project on that server.
- With a project and the S3 engine, it is a bucket in your own AWS account.

The dashboard creates all three shapes, and is the usual way to manage them. You can also create one with `kubectl`.

For the task-oriented view, see [Object storage](/docs/guides/object-storage/).

## Summary

| Property | Value |
| --- | --- |
| API version | `geass.geass.dev/v1alpha1` |
| Kind | `GeassObjectStore` |
| Plural | `geassobjectstores` |
| Scope | Namespaced. Create it in `geass-system`: the dashboard and the other controllers look only there. |
| Subresources | `status` |
| Printer column `Environment` | `.spec.environment` |
| Printer column `Engine` | `.spec.engine` |
| Printer column `Ready` | `.status.conditions[?(@.type=="Ready")].status` |
| Finalizer | `geass.geass.dev/objectstore` |

## The three shapes

| Shape | How to tell | Where it runs or lives |
| --- | --- | --- |
| Cluster object storage server | No `spec.project`. Engine `SeaweedFS` or `Garage`, placement `InCluster`. | A single server pod in `geass-system`. Usually named `cluster-s3`. The dashboard allows only one. |
| Project bucket on the cluster server | A `spec.project`. Engine `SeaweedFS` or `Garage` (the engine of the server), placement `InCluster`. | Buckets on the cluster object storage server, with their own login. The server must be `Ready`. |
| Project bucket on AWS S3 | A `spec.project`. Engine `S3`, or placement `External`. | Buckets in the AWS account of a [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/). |

A store is external when `spec.engine` is `S3` or `spec.placement` is `External`. One of the two is sufficient. The
default placement, `InCluster`, does not make an `S3` store local.

## Examples

### The cluster object storage server

```yaml title="cluster-s3.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassObjectStore
metadata:
  name: cluster-s3
  namespace: geass-system
spec:
  engine: SeaweedFS   # or Garage
  placement: InCluster
  resources:
    requests:
      cpu: 250m
      memory: 512Mi
    limits:
      cpu: 250m
      memory: 512Mi
  storageSize: 20Gi   # unset means 10Gi
  serverRegion: eu
```

### A bucket on the cluster server

```yaml title="uploads-bucket.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassObjectStore
metadata:
  name: uploads
  namespace: geass-system
spec:
  project: storefront
  environment: production
  engine: SeaweedFS   # the engine of the cluster server
  placement: InCluster
  buckets:
    - uploads
  deploy:
    enabled: true
```

### A bucket in AWS S3

```yaml title="uploads-s3.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassObjectStore
metadata:
  name: uploads-s3
  namespace: geass-system
spec:
  project: storefront
  environment: production
  engine: S3
  placement: External
  connectionRef:
    name: storefront-aws
  region: eu-west-1
  createBucket: true
  buckets:
    - storefront-uploads
  deploy:
    enabled: true
```

`connectionRef` names an AWS [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/) that is scoped
to the same project.

## Spec fields

| Field | Type | Required or default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.project` | string | Optional | None | The project that owns the store. If it is empty, this store is the object storage server of the whole cluster. |
| `spec.environment` | string | Optional. Required in practice when `project` is set. | Pattern `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, max length 63 | The project environment. |
| `spec.engine` | string | Required | Enum `SeaweedFS`, `Garage`, `S3` | `SeaweedFS` and `Garage` run in the cluster. For the cluster server, it is the engine that you selected. A bucket on the server takes the engine of the server. `S3` uses AWS. You cannot change the engine after the object exists. The API server refuses the edit with `engine cannot be changed; delete the object storage and set it up again`. |
| `spec.placement` | string | CRD default `InCluster` | Enum `InCluster`, `External` | `External` uses AWS S3. See [The three shapes](#the-three-shapes). |
| `spec.connectionRef` | LocalObjectReference | Optional. Required for an S3 store. | None | An AWS [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/) in `geass-system`, scoped to the same project. Not used for buckets in the cluster. |
| `spec.connectionRef.name` | string | CRD default `""` | None | Name of the connection. |
| `spec.region` | string | Optional | None | AWS region for an S3 store. If it is empty, Geass uses the region of the connection. If that is empty, Geass uses the `region` key of the connection Secret, then `us-east-1`. |
| `spec.createBucket` | boolean | `false` | None | S3 stores only. Geass creates the named buckets if they do not exist. Geass also deletes them when you delete the store. |
| `spec.buckets` | array of strings | Optional. A project store needs at least one to deploy. | Max 32 items. Each 3 to 63 characters, pattern `^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`. CEL: `self.all(b, !b.matches('^[0-9]+[.][0-9]+[.][0-9]+[.][0-9]+$') && !b.contains('..'))`, message "bucket names must not be formatted as an IP address or contain consecutive periods". | The buckets. The cluster server stores a bucket as `<project>.<environment>.<name>`. The first stored name is the `bucket` key of the connection Secret. The dashboard manages a single bucket. |
| `spec.resources` | ResourceRequirements | Optional. Defaults to 250m CPU and 512Mi memory for SeaweedFS, and 100m and 256Mi for Garage. | None | CPU and memory for the cluster object storage server. Ignored for project buckets. |
| `spec.storageSize` | quantity | Optional. Unset means `10Gi`, and Geass writes the value onto the resource. | None in the CRD. The dashboard and the API require at least `1Gi`, and Geass never makes a volume smaller than that. | Size of the volume of the cluster object storage server, for example `20Gi`. You can raise it, but never lower it. You can raise it only where the storage class of the volume allows expansion. Ignored for project buckets. |
| `spec.deploy.enabled` | boolean | `false` | None | Controls provisioning of a project bucket, but only when the resource has the `geass.dev/deploy-workflow: "true"` annotation. This field never controls the cluster object storage server. |
| `spec.runOn.tags` | array of strings | Optional | Max 8 items, each matching `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$` and at most 40 characters, set semantics | Node tags that the cluster object storage server must run on. Geass reads them one time, when it first sets up the server. A project that requires node tags can keep buckets on the server only when these tags cover them. |
| `spec.serverRegion` | string | Optional | Pattern `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, max length 40 | Region of the cluster object storage server. Project buckets follow their project instead. |

:::note
The Go types and the generated CRD agree for every field on this page.
:::

Geass itself fills in `spec.resources` on the cluster object storage server when the field has no `requests`. It also
fills in `spec.storageSize` when the field is not set. After the first reconcile, you see these values: 250m and
512Mi (SeaweedFS) or 100m and 256Mi (Garage), and `10Gi`.

## Status

| Field | Type | Meaning |
| --- | --- | --- |
| `status.targetNamespace` | string | The namespace of the connection Secret: `geass-system` for the server, `<project>-<environment>` for a bucket. |
| `status.connectionSecret` | string | Name of the Secret with the access details, `<name>-connection`. |
| `status.endpoint` | string | The S3 endpoint. For the cluster server and the buckets on it, `http://geass-s3.geass-system.svc:8333` (SeaweedFS) or `http://geass-s3.geass-system.svc:3900` (Garage). For AWS, `https://s3.<region>.amazonaws.com`. |
| `status.reconcileRetry` | object | Failure counters. Same as for [GeassDatabase](/docs/reference/resources/geass-database/#ready-condition-and-retries). |
| `status.provision` | object | Deploy history. Same as for GeassDatabase. |
| `status.conditions` | list | One condition: `Ready`. |

`Ready` has three reasons:

| Reason | Status | Meaning |
| --- | --- | --- |
| `ObjectStoreReady` | `True` | The message is `Cluster object storage is ready`, `Bucket is ready on the cluster's object storage`, or `AWS S3 bucket is ready`. |
| `ReconcileError` | `False` | Not ready yet. The message says what Geass waits for, or what failed. Geass checks again after 30 seconds. |
| `ProvisionFailed` | `False` | Geass stopped after repeated errors. The retry rules are the same as for GeassDatabase: see [Ready condition and retries](/docs/reference/resources/geass-database/#ready-condition-and-retries). |

You can see these messages:

| Message | Cause |
| --- | --- |
| `Resource is configured and waiting for deployment` | A draft: a project store with the `geass.dev/deploy-workflow: "true"` annotation and `spec.deploy.enabled: false`. Geass creates nothing for it until you deploy. |
| `Configure the bucket name in settings, then deploy` | `spec.buckets` is empty. |
| `Set up object storage in cluster settings first` | The cluster has no object storage server. |
| `Object storage is starting` | The server exists but is not `Ready` yet: its pod is still starting. |
| `This Object storage server needs about … Scale up the cluster before creating it` | The cluster has no room for the size of the server. Geass checks only when it creates the server. See [The capacity check](/docs/guides/scaling-and-placement/#the-capacity-check). |
| `This object storage server does not run on nodes tagged <tags>, which the project requires` | The `spec.requiredTags` of the project include a tag that the server is not placed on. The `geass.dev/node-placement` of the server has the tag in neither `tags` nor `required`. The message counts as a failure. |
| `Storage is starting` | Geass is still installing the replicated storage of the cluster (Longhorn), or the storage class for the region or tags of the server does not exist yet. Geass waits before it creates the volume, because a volume made now stays on local storage permanently. Geass checks again after 30 seconds. |
| `Storage is unavailable` | The replicated storage did not start in 15 minutes. The message counts as a failure. Thus, after repeated errors, the `Ready` reason becomes `ProvisionFailed` until you retry. |
| `Cluster object storage credentials are unavailable` | The root Secret of the server is missing or empty. |
| `an AWS connection is required for external buckets` | An S3 store has no `connectionRef`. |
| `AWS connection is unavailable`, `AWS credentials are unavailable`, `AWS access key and secret key are required` | The connection or its Secret is missing or incomplete. |
| `external buckets require an AWS connection` | The provider of the connection is not `AWS`. |
| `cloud connection "<name>" was not found`, `... is scoped to project "<project>"`, `... must be scoped to a project` | This project cannot use the connection. |
| `bucket name "<name>" must be 3-63 characters` and similar | A bucket name is not a valid S3 name. |
| `bucket name "<name>" is too long: on the cluster's object storage it is stored as "<project>.<environment>.<name>" ...` | The cluster server stores a project bucket with its project and environment before its name. The full name must be 63 characters or fewer. The message gives the longest name that fits. |
| `AWS S3 <status>: <body>`, `Garage <status> <code>: <message>` | AWS, the server of the cluster, or the admin API of Garage refused a call. |

## What the dashboard writes

| Where | What it does |
| --- | --- |
| **Settings** > **Object storage**, **Set up** on the **SeaweedFS** or **Garage** card, then **Set up SeaweedFS server** or **Set up Garage server** | Creates the cluster object storage server `cluster-s3`: no project, `engine` set to the card that you selected, `placement: InCluster`, `resources` from the CPU and memory that you select, and `storageSize` from **Storage**. It is not a draft. Only one server can exist. The REST API makes the same choice with `cluster=on` and `engine=SeaweedFS` or `engine=Garage`. |
| **Settings** > **Object storage**, **Manage**, **Size** | Updates `spec.resources` of the server after a capacity check. |
| **Settings** > **Object storage**, **Manage**, **Storage**, then **Save storage** | Updates `spec.storageSize`. Geass refuses a smaller size, a size under `1Gi`, and all changes on a cluster whose storage cannot be resized. In that case, the section says "This cluster's storage can't be resized.". Otherwise, the operator grows the volume in place. The REST API does the same with `storageSize` on `POST /api/object-stores/<name>/update`. |
| **Settings** > **Object storage**, **Remove object storage** section, **Remove SeaweedFS** or **Remove Garage** | Opens a confirmation. To delete the server, its network policy, and all the data on its volume, type `remove` and click **Remove permanently**. While project buckets exist, the dashboard refuses with `delete project buckets before removing the object storage server`. |
| **+ Add resource** > **Bucket** > **In-cluster bucket** | Creates a draft bucket (`placement: InCluster`, and `engine` set to the engine of the cluster server). Until the server exists, the entry is unavailable, and shows the reason. |
| **+ Add resource** > **Bucket** > **AWS S3** | Creates a draft bucket (`engine: S3`, `placement: External`), and sets `connectionRef` to the first AWS connection of the project. Unavailable until an AWS connection exists. |
| Resource panel, **Settings** tab, **Bucket** section | **Name** writes `spec.buckets` (a single entry). For an S3 bucket, **Connection** writes `connectionRef`, and **Create bucket in AWS** writes `createBucket`. These changes wait for a deploy. |
| **Deploy changes** in the top bar | For every pending resource of the environment, sets `spec.deploy.enabled: true` and stores the last deployed copy. If `spec.buckets` is empty and the resource name is a valid bucket name, it uses the resource name as the bucket. The single-resource deploy of the API, `POST /api/object-stores/<name>/deploy`, refuses instead, with `set a bucket name in settings before you deploy`. |
| **Retry** | Resets a `ProvisionFailed` store. |

The dashboard creates the draft with the annotation `geass.dev/deploy-workflow: "true"`, `spec.deploy.enabled: false`,
and no bucket. The draft rules are the same as for GeassDatabase: see
[Drafts and the Deploy flow](/docs/reference/resources/geass-database/#drafts-and-the-deploy-flow). A project store
has no live settings. Every edit waits for a deploy.

A viewer in the dashboard sees a redacted copy. The copy has no bucket names, connection, region, secret name, or
endpoint. A token that cannot change things gets the same copy from the API.

## From the command line and the API

| To | Command | MCP tool |
| --- | --- | --- |
| List buckets | `geass stores list` | `list_buckets` |
| Read `resource`, `spec`, and `status` | `geass stores get NAME` | None |
| Create, deploy, or delete | `geass api POST /object-stores`, `geass api POST /object-stores/NAME/deploy`, `geass api DELETE /object-stores/NAME -q confirmName=NAME` | `delete_resource` with `kind` set to `object-stores`, only with `--allow-delete` |

These commands call `GET /api/v1/object-stores` and `GET /api/v1/object-stores/{name}`. The `details` of a summary
holds `placement` and the bucket names. The `state` follows the same rules as for
[GeassDatabase](/docs/reference/resources/geass-database/#from-the-command-line-and-the-api). That includes `draft`
for a store whose `spec.deploy.enabled` is `false`. The cluster object storage server is such a store: nothing sets
the field on it, so the list shows it as `draft` while it runs.

There is no connection endpoint for buckets. Thus, the command line and the MCP server do not return an access key or
a secret key. Read them from the connection Secret. See
[Object storage](/docs/guides/object-storage/#command-line-and-ai-agents).

## Connection Secret

Geass writes `<name>-connection` with four keys: `endpoint`, `accessKey`, `secretKey`, and `bucket`. The `bucket` key
is the first entry of `spec.buckets`, or the resource name. For a bucket on the cluster server, the value is the name
that the server stores, `<project>.<environment>.<name>`.

- For the cluster server, the keys are the login of the server itself, in `geass-system`.
- For a project bucket, the keys are its own credentials, in `<project>-<environment>`.

The connection Secret of a bucket has no `uri` key. The **Add reference** action on the **Variables** tab of a service
adds four variables for a bucket:

- `S3_ENDPOINT` from `endpoint`.
- `S3_ACCESS_KEY_ID` from `accessKey`.
- `S3_SECRET_ACCESS_KEY` from `secretKey`.
- `S3_BUCKET` from `bucket`.

If you already set one of those names by hand on the service, the action refuses, and names the variable.

## Editing by hand

You can create and edit a `GeassObjectStore` with `kubectl`.

- You cannot edit `spec.engine`. To change the server, delete the project buckets and the object storage server. Then
  set up the server again.
- The first time, Geass pins the placement (`serverRegion` and `runOn`) of the cluster object storage server in the
  annotation `geass.dev/node-placement`. The volume of the server stays there. Do not edit the annotation.
- Geass makes the volume of the server one time, with the class and the size that the server asks for:
  - `spec.storageSize` sets the size (`10Gi` when unset).
  - Geass grows the volume to a larger value when the storage class of the volume allows expansion. Geass never makes
    the volume smaller.
  - A value that you set with `kubectl` skips the checks of the dashboard and the API. Thus, Geass applies its own
    checks. Geass never makes a new volume under `1Gi`.
  - Geass does not apply a size that the volume cannot take: a size smaller than the volume, or a larger size on a
    class that cannot grow. Instead, Geass writes the real size of the volume back to `spec.storageSize`. Thus, the
    resource shows the size that the volume has.
- The first time, Geass records the storage class that it selected for the volume in the annotation
  `geass.dev/storage-class`. An empty value means the cluster default. The class of a volume cannot change, so do not
  edit the annotation.
- Do not rename a bucket after it exists. If you do, Geass creates the new name, and leaves the old bucket and the
  credentials behind.
- When you delete the store, Geass runs the finalizer. For a bucket on the cluster server, Geass deletes the bucket
  and its login. For an S3 store, Geass deletes the IAM user. If `createBucket` is `true`, Geass also empties and
  deletes the bucket.
- On a project store, Geass sets the annotation `geass.dev/provisioning-started: "true"` immediately before it first
  creates a bucket or a user. A store with this annotation, or with a connection Secret, might own resources outside
  the cluster. Thus, when you delete the store, Geass tries to clean up those resources, even if the store never
  became ready. For a store that never had a connection Secret, that cleanup is best effort. Geass logs a failure, and
  the deletion still completes. Do not edit the annotation.
- Geass refuses to delete the cluster object storage server while project buckets exist. Geass returns the error
  `delete project buckets before removing the object storage server: <names>`, and the resource stays in
  `Terminating`. When the deletion completes, Geass removes the server, its network policy `geass-s3`, and all the data
  on its volume.

:::danger
Do not set `createBucket` for a bucket that holds data that you want to keep. With `spec.createBucket: true`, when you
delete an S3 `GeassObjectStore`, Geass empties the bucket, including old object versions, and then deletes it. The
data is gone.
:::

## What happens under the hood

### The cluster object storage server

Geass runs the server itself, from a pinned image. There is no Helm chart. Geass creates these objects:

- Secret `<name>-root` in `geass-system`. It holds the generated login of the server. For Garage, it also holds the
  admin token, an RPC secret, and a metrics token.
- Secret `geass-s3-config` in `geass-system`. It holds the files that the server reads at start.
- PersistentVolumeClaim `geass-s3`, of `spec.storageSize` (10Gi when unset). Geass makes it one time. When the cluster
  has Longhorn, Geass uses the replicated storage class. Otherwise, it uses the default class of the cluster (see
  [The volume](#the-volume)). Geass can raise the size later, and never lowers it.
- NetworkPolicy `geass-s3`. It closes every port of the server pod except the ports that other pods must reach (see
  [The network policy](#the-network-policy)).
- Deployment `geass-s3` with one pod, and Service `geass-s3`.
  - The pod has one container for the server. For SeaweedFS, it has a second container, `s3-forwarder`, that publishes
    the S3 port.
  - The Service publishes the S3 port: 8333 for SeaweedFS and 3900 for Garage. For Garage, the Service also publishes
    the admin port 3903, which Geass uses to make buckets and keys.
  - Geass recreates the pod and does not roll it, because only one pod at a time can mount the volume.
- Secret `<name>-connection` in `geass-system`.

SeaweedFS runs its master, volume server, filer, and S3 gateway as one process. SeaweedFS listens only on localhost,
because it serves internal services in addition to S3, and those services need no login. The `s3-forwarder` container
is the only thing in the pod that other pods can reach. Both servers run as a non-root user. The pod gets 60 seconds to
stop, because SeaweedFS needs about half a minute to shut down cleanly.

#### The volume

Geass selects the storage class of the volume one time, when it first creates the server. Geass records the class in
the annotation `geass.dev/storage-class`. A server that already has a volume keeps the class of that volume.

| Cluster | Storage class | Where the data is |
| --- | --- | --- |
| The replicated storage of Geass (Longhorn) is installed | `geass-replicated`, or the class of the region `geass-replicated-<region>` when the server is placed in a region | Copies on several nodes, up to three, as for database volumes. A server placed in a region keeps its copies in that region. |
| Longhorn is installed and the server is placed on nodes with tags (`spec.runOn.tags`) | `geass-tagged-<hash>`, for the region and tags of the server | Copies only on nodes in that region that have all of the tags. Thus, Geass never replicates the data onto other nodes. The cluster controller creates the class itself, after every node that the class covers has a ready Longhorn manager. |
| No Longhorn | The default class of the cluster (`local-path` on K3s) | One node, the node that the pod runs on. |

While Geass installs Longhorn, or while the class for the region or tags of the server does not exist yet, Geass makes
no volume. The server then reports `Storage is starting`. A wait for Longhorn itself has a limit of 15 minutes. After
that, the server reports `Storage is unavailable`. A wait for the class of a region or of tags has no limit. See
[Storage and volumes](/docs/guides/storage/) for the classes.

The size is `spec.storageSize`. A larger value grows the volume in place when its class allows expansion. Clusters with
Longhorn can resize. The default `local-path` storage of K3s cannot resize. On such a cluster, the dashboard says
"This cluster's storage can't be resized." The REST API refuses with
`this cluster's storage can't be resized, so the server stays at 10Gi` (with the current size).

#### The network policy

The NetworkPolicy `geass-s3` in `geass-system` selects the server pod (label `app.kubernetes.io/name: geass-s3`) and
limits ingress:

| Port | Who can reach it |
| --- | --- |
| S3: 8333 for SeaweedFS, 3900 for Garage | Every namespace. Apps reach their buckets through this port, and the login of each project limits it to its own bucket. |
| The admin port of Garage, 3903 | Only the namespace of the platform itself, `geass-system`. |
| Every other port | Nobody. |

The policy has an effect on clusters whose network enforces policies. The K3s network does. The server closes the same
ports itself:

- SeaweedFS listens only on localhost, and the forwarder container publishes the S3 port.
- Garage closes its other ports.

Thus, the policy is a second lock. The controller keeps the policy in place. If you edit or delete the policy by hand,
the controller puts it back. When you delete the server, Geass deletes the policy.

### A bucket on the cluster server

1. Geass creates each bucket on the server with the login of the server itself, under its stored name
   `<project>.<environment>.<name>`.
2. Geass creates a login for the store that can reach only the buckets of that store. For SeaweedFS, the login is a
   user `geass-<name>`, created through the IAM API of SeaweedFS. For Garage, it is a key, created through the admin
   API of Garage. The server generates the access key and the secret key.
3. Geass writes `<name>-connection` into the namespace of the project, with those credentials.

If the server was reset and forgets the login, Geass issues a new key on its next pass, and updates the
Secret.

### A bucket on AWS

1. If `createBucket` is `true`, Geass creates the buckets.
2. Geass creates an IAM user `geass-<name>` with an inline policy named `geass-bucket`. The policy allows list, read,
   write, and delete on the buckets of the store only.
3. Geass creates an access key one time, and keeps it in `<name>-connection`.

The AWS credentials in the connection need permission to create buckets, and to create IAM users, policies, and access
keys.

:::caution
Select a bucket name that is unique to you. S3 bucket names are global. If the name belongs to a different AWS
account, S3 returns a conflict, and Geass treats that as success. The store then reports `Ready`, but AWS denies access
to your app.
:::

```bash title="Inspect an object store"
kubectl -n geass-system get geassobjectstores
kubectl -n geass-system get geassobjectstore cluster-s3 -o yaml

# The object storage server
kubectl -n geass-system get deployment,service,pvc geass-s3
kubectl -n geass-system logs deploy/geass-s3

# A project bucket's credentials
kubectl -n storefront-production get secret uploads-connection \
  -o jsonpath='{.data.endpoint}' | base64 -d
```

## Troubleshooting

### `Resource is configured and waiting for deployment`

The bucket is a draft. Geass creates nothing for a draft until you deploy it.

1. On the **Settings** tab, enter the **Name**.
2. Save.
3. In the top bar of the project, click **Deploy changes**.

After a deploy, the message changes to ready. If the message stays after a deploy, make sure that
`spec.deploy.enabled` is `true`.

### `Configure the bucket name in settings, then deploy`

Open the **Settings** tab of the bucket, enter the **Name**, save, then deploy.

### `Set up object storage in cluster settings first`

A bucket on the cluster server needs the server. Open **Settings** > **Object storage**, and set up SeaweedFS or
Garage. The bucket waits until the `Ready` of the server is `True`.

### `Object storage is starting` stays for minutes

The server pod is not ready. Examine the pod: run `kubectl -n geass-system get pods | grep geass-s3`. Then run
`kubectl -n geass-system describe pod` on the pod, and read its logs. Image pulls on a new node can be slow.

### `Storage is starting` stays

The server waits for the storage class of its volume. Examine Longhorn and the classes: `kubectl get pods -n longhorn-system`,
`kubectl get storageclass`.

For a server placed in a region or on tags, look for `geass-replicated-<region>` or a `geass-tagged-...` class. The
cluster controller makes these classes only when every node in that scope has a ready Longhorn manager. A node without
the iSCSI initiator prevents this. See [Storage and volumes](/docs/guides/storage/#troubleshooting).

### `Storage is unavailable`

Longhorn did not start in 15 minutes, so Geass did not create the volume. The message counts as a failure. After
repeated errors, `Ready` becomes `ProvisionFailed`, and Geass stops retrying. Fix Longhorn. Then retry with
`POST /api/object-stores/<name>/retry-provision`.

### `this cluster's storage can't be resized`

The volume of the server is on a class that cannot grow, for example the `local-path` class of K3s. The class of a
volume cannot change. Thus, the size stays what it was when Geass created the server. Remove the server, and set it up
again with the size that you need. That deletes everything on its volume.

### The object storage server cannot be removed

`delete project buckets before removing the object storage server` lists the buckets that still use the server. Delete
those buckets first.

### An S3 bucket reports `Ready` but the app gets access denied

Make sure that the bucket name belongs to your account (see the caution above). Also make sure that the app uses the
credentials in `<name>-connection`.

### `AWS S3 <status>: ...`

AWS refused the call. The body names the cause. Frequent causes are `AccessDenied` (the keys of the connection do
not have S3 or IAM permissions) and `InvalidBucketName`.

## Related

- [Object storage](/docs/guides/object-storage/)
- [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/)
- [GeassDatabase](/docs/reference/resources/geass-database/)
- [Connect AWS](/docs/guides/connectors/aws/)
- [GeassProject](/docs/reference/resources/geass-project/)
- [Resources overview](/docs/reference/resources/overview/)
