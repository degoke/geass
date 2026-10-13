---
title: "GeassObjectStore"
description: "Reference for GeassObjectStore: the cluster's object storage server (SeaweedFS or Garage), buckets on it for a project, or buckets in your own AWS S3 account."
sidebar:
  order: 5
---

A `GeassObjectStore` is object storage. It takes one of three shapes. With no `spec.project` it is the cluster-wide object storage server, the S3-compatible service that runs in your cluster and is either SeaweedFS or Garage. With a project and an in-cluster engine it is a bucket for that project on that server. With a project and the S3 engine it is a bucket in your own AWS account. The dashboard creates all three and is the normal way to manage them. You can also create one with `kubectl`.

For the task-oriented view, see [Object storage](/docs/guides/object-storage/).

## Summary

| Property | Value |
| --- | --- |
| API version | `geass.geass.dev/v1alpha1` |
| Kind | `GeassObjectStore` |
| Plural | `geassobjectstores` |
| Scope | Namespaced. Create it in `geass-system`: the dashboard and the other controllers only look there. |
| Subresources | `status` |
| Printer column `Environment` | `.spec.environment` |
| Printer column `Engine` | `.spec.engine` |
| Printer column `Ready` | `.status.conditions[?(@.type=="Ready")].status` |
| Finalizer | `geass.geass.dev/objectstore` |

## The three shapes

| Shape | How to tell | Where it runs or lives |
| --- | --- | --- |
| Cluster object storage server | No `spec.project`. Engine `SeaweedFS` or `Garage`, placement `InCluster`. | A single server pod in `geass-system`. Normally named `cluster-s3`. Only one is allowed by the dashboard. |
| Project bucket on the cluster server | A `spec.project`. Engine `SeaweedFS` or `Garage` (the server's own), placement `InCluster`. | Buckets on the cluster object storage server, with a login of their own. Needs the server to be `Ready`. |
| Project bucket on AWS S3 | A `spec.project`. Engine `S3`, or placement `External`. | Buckets in the AWS account of a [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/). |

A store is external when `spec.engine` is `S3` or `spec.placement` is `External`. Either one is enough: the default placement, `InCluster`, does not make an `S3` store local.

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

`connectionRef` names an AWS [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/) scoped to the same project.

## Spec fields

| Field | Type | Required or default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.project` | string | Optional | None | The owning project. Empty makes this the cluster-wide object storage server. |
| `spec.environment` | string | Optional. Required in practice when `project` is set. | Pattern `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, max length 63 | The project environment. |
| `spec.engine` | string | Required | Enum `SeaweedFS`, `Garage`, `S3` | `SeaweedFS` and `Garage` run in the cluster: for the cluster server it is the one you chose, and a bucket on it takes the server's engine. `S3` uses AWS. It cannot be changed once the object exists: the API server refuses the edit with `engine cannot be changed; delete the object storage and set it up again`. |
| `spec.placement` | string | CRD default `InCluster` | Enum `InCluster`, `External` | `External` uses AWS S3. See [The three shapes](#the-three-shapes). |
| `spec.connectionRef` | LocalObjectReference | Optional. Required for an S3 store. | None | An AWS [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/) in `geass-system`, scoped to the same project. Not used for buckets in the cluster. |
| `spec.connectionRef.name` | string | CRD default `""` | None | Name of the connection. |
| `spec.region` | string | Optional | None | AWS region for an S3 store. When empty Geass uses the connection's region, then the `region` key of its Secret, then `us-east-1`. |
| `spec.createBucket` | boolean | `false` | None | S3 stores only. Geass creates the named buckets when they do not exist. It also deletes them when the store is deleted. |
| `spec.buckets` | array of strings | Optional. A project store needs at least one to deploy. | Max 32 items. Each 3 to 63 characters, pattern `^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`. CEL: `self.all(b, !b.matches('^[0-9]+[.][0-9]+[.][0-9]+[.][0-9]+$') && !b.contains('..'))`, message "bucket names must not be formatted as an IP address or contain consecutive periods". | The buckets. A bucket on the cluster server is stored on it as `<project>.<environment>.<name>`, and the first stored name is the `bucket` key of the connection Secret. The dashboard manages a single bucket. |
| `spec.resources` | ResourceRequirements | Optional. Defaults to 250m CPU and 512Mi memory for SeaweedFS, and 100m and 256Mi for Garage. | None | CPU and memory for the cluster object storage server. Ignored for project buckets. |
| `spec.storageSize` | quantity | Optional. Unset means `10Gi`, and Geass writes the value onto the resource. | None in the CRD. The dashboard and the API require at least `1Gi`, and Geass never makes a volume smaller than that. | Size of the cluster object storage server's volume, for example `20Gi`. It can be raised, never lowered, and only where the volume's storage class allows expansion. Ignored for project buckets. |
| `spec.deploy.enabled` | boolean | `false` | None | Gates provisioning of a project bucket, but only when the resource carries the `geass.dev/deploy-workflow: "true"` annotation. The cluster object storage server is never gated. |
| `spec.runOn.tags` | array of strings | Optional | Max 8 items, each matching `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$` and at most 40 characters, set semantics | Node tags the cluster object storage server must run on. Read once, when it is first set up. A project that requires node tags can keep buckets on the server only when these cover them. |
| `spec.serverRegion` | string | Optional | Pattern `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, max length 40 | Region of the cluster object storage server. Project buckets follow their project instead. |

:::note
The Go types and the generated CRD agree for every field on this page.
:::

Geass itself fills in `spec.resources` on the cluster object storage server when it has no `requests`, and `spec.storageSize` when it has none. You will see 250m and 512Mi (SeaweedFS) or 100m and 256Mi (Garage), and `10Gi`, appear after the first reconcile.

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
| `ObjectStoreReady` | `True` | The message is `Cluster object storage is ready`, `Bucket is ready on the cluster's object storage` or `AWS S3 bucket is ready`. |
| `ReconcileError` | `False` | Not ready yet. The message says what Geass waits for or what failed. Geass checks again after 30 seconds. |
| `ProvisionFailed` | `False` | Geass gave up after repeated errors. The retry rules are the same as for GeassDatabase: see [Ready condition and retries](/docs/reference/resources/geass-database/#ready-condition-and-retries). |

The messages you can see:

| Message | Cause |
| --- | --- |
| `Resource is configured and waiting for deployment` | A draft: a project store with the `geass.dev/deploy-workflow: "true"` annotation and `spec.deploy.enabled: false`. Nothing is created for it until you deploy. |
| `Configure the bucket name in settings, then deploy` | `spec.buckets` is empty. |
| `Set up object storage in cluster settings first` | The cluster has no object storage server. |
| `Object storage is starting` | The server exists but is not `Ready` yet: its pod is still coming up. |
| `This Object storage server needs about … Scale up the cluster before creating it` | The cluster has no room for the server's size. Geass checks only when it creates the server. See [The capacity check](/docs/guides/scaling-and-placement/#the-capacity-check). |
| `This object storage server does not run on nodes tagged <tags>, which the project requires` | The project's `spec.requiredTags` include a tag that the server is not placed on (its `geass.dev/node-placement` has it in neither `tags` nor `required`). The message counts as a failure. |
| `Storage is starting` | The cluster's replicated storage (Longhorn) is still being installed, or the storage class for the server's region or tags is not made yet. Geass waits before it creates the volume, because a volume made now would stay on local storage for good. It checks again after 30 seconds. |
| `Storage is unavailable` | The replicated storage did not come up in 15 minutes. The message counts as a failure, so after repeated errors the `Ready` reason becomes `ProvisionFailed` until you retry. |
| `Cluster object storage credentials are unavailable` | The server's root Secret is missing or empty. |
| `an AWS connection is required for external buckets` | An S3 store has no `connectionRef`. |
| `AWS connection is unavailable`, `AWS credentials are unavailable`, `AWS access key and secret key are required` | The connection or its Secret is missing or incomplete. |
| `external buckets require an AWS connection` | The connection's provider is not `AWS`. |
| `cloud connection "<name>" was not found`, `... is scoped to project "<project>"`, `... must be scoped to a project` | The connection is not usable by this project. |
| `bucket name "<name>" must be 3-63 characters` and similar | A bucket name is not a legal S3 name. |
| `bucket name "<name>" is too long: on the cluster's object storage it is stored as "<project>.<environment>.<name>" ...` | A project bucket on the cluster server is stored behind its project and environment, and the whole name has to be 63 characters or fewer. The message gives the longest name that fits. |
| `AWS S3 <status>: <body>`, `Garage <status> <code>: <message>` | AWS, the cluster's server, or Garage's admin API refused a call. |

## What the dashboard writes

| Where | What it does |
| --- | --- |
| **Settings** > **Object storage**, **Set up** on the **SeaweedFS** or **Garage** card, then **Set up SeaweedFS server** or **Set up Garage server** | Creates the cluster object storage server `cluster-s3`: no project, `engine` set to the card you chose, `placement: InCluster`, `resources` from the CPU and memory you choose, and `storageSize` from **Storage**. It is not a draft. Only one server can exist. The REST API makes the same choice with `cluster=on` and `engine=SeaweedFS` or `engine=Garage`. |
| **Settings** > **Object storage**, **Manage**, **Size** | Updates `spec.resources` of the server after a capacity check. |
| **Settings** > **Object storage**, **Manage**, **Storage**, then **Save storage** | Updates `spec.storageSize`. Geass refuses a smaller size, a size under `1Gi`, and any change on a cluster whose storage cannot be resized (the section then says "This cluster's storage can't be resized."). Otherwise the operator grows the volume in place. The REST API does the same with `storageSize` on `POST /api/object-stores/<name>/update`. |
| **Settings** > **Object storage**, **Remove object storage** section, **Remove SeaweedFS** or **Remove Garage** | Opens a confirmation. Type `remove` and click **Remove permanently** to delete the server, its network policy and all the data on its volume. The dashboard refuses with `delete project buckets before removing the object storage server` while project buckets exist. |
| **+ Add resource** > **Bucket** > **In-cluster bucket** | Creates a draft bucket (`placement: InCluster`, and `engine` set to the cluster server's engine). The entry is disabled, with the reason, until the server exists. |
| **+ Add resource** > **Bucket** > **AWS S3** | Creates a draft bucket (`engine: S3`, `placement: External`) and sets `connectionRef` to the project's first AWS connection. Disabled until an AWS connection exists. |
| Resource panel, **Settings** tab, **Bucket** section | **Name** writes `spec.buckets` (a single entry). For an S3 bucket, **Connection** writes `connectionRef` and **Create bucket in AWS** writes `createBucket`. These wait for a deploy. |
| **Deploy changes** in the top bar | Sets `spec.deploy.enabled: true` and stores the last deployed copy, for every pending resource of the environment. It fills in the resource name as the bucket when `spec.buckets` is empty and the name is a valid bucket name. The API's single-resource deploy, `POST /api/object-stores/<name>/deploy`, refuses instead with `set a bucket name in settings before you deploy`. |
| **Retry** | Resets a `ProvisionFailed` store. |

The draft is created with the annotation `geass.dev/deploy-workflow: "true"`, `spec.deploy.enabled: false` and no bucket. The draft rules are the same as for GeassDatabase: see [Drafts and the Deploy flow](/docs/reference/resources/geass-database/#drafts-and-the-deploy-flow). A project store has no live settings: every edit waits for a deploy.

A viewer in the dashboard sees a redacted copy: bucket names, connection, region, secret name and endpoint are cleared. A token that cannot change things gets the same copy from the API.

## From the command line and the API

| To | Command | MCP tool |
| --- | --- | --- |
| List buckets | `geass stores list` | `list_buckets` |
| Read `resource`, `spec` and `status` | `geass stores get NAME` | None |
| Create, deploy or delete | `geass api POST /object-stores`, `geass api POST /object-stores/NAME/deploy`, `geass api DELETE /object-stores/NAME -q confirmName=NAME` | `delete_resource` with `kind` set to `object-stores`, only with `--allow-delete` |

They call `GET /api/v1/object-stores` and `GET /api/v1/object-stores/{name}`. `details` of a summary holds `placement` and the bucket names, and `state` follows the same rules as for [GeassDatabase](/docs/reference/resources/geass-database/#from-the-command-line-and-the-api). That includes `draft` for a store whose `spec.deploy.enabled` is `false`, which is the case for the cluster object storage server: nothing sets the field on it, so it is listed as `draft` while it runs. There is no connection endpoint for buckets, so neither the command line nor the MCP server returns an access key or a secret key: read them from the connection Secret. See [Object storage](/docs/guides/object-storage/#command-line-and-ai-agents).

## Connection Secret

Geass writes `<name>-connection` with four keys: `endpoint`, `accessKey`, `secretKey` and `bucket` (the first entry of `spec.buckets`, or the resource name). For a bucket on the cluster server the value is the name as stored on the server, `<project>.<environment>.<name>`. For the cluster server the keys are the server's own login, in `geass-system`. For a project bucket they are credentials of its own, in `<project>-<environment>`.

A bucket's connection Secret has no `uri` key. The **Add reference** action on a service's **Variables** tab adds four variables for a bucket: `S3_ENDPOINT` from `endpoint`, `S3_ACCESS_KEY_ID` from `accessKey`, `S3_SECRET_ACCESS_KEY` from `secretKey` and `S3_BUCKET` from `bucket`. It refuses, naming the variable, when the service already has one of those names set by hand.

## Editing by hand

You can create and edit a `GeassObjectStore` with `kubectl`.

- `spec.engine` cannot be edited. To change the server, delete the project buckets and the object storage server, then set it up again.
- Geass pins the cluster object storage server's placement (`serverRegion` and `runOn`) in the annotation `geass.dev/node-placement` the first time. Its volume stays there. Do not edit the annotation.
- The server's volume is made once, with the class and the size it asks for. `spec.storageSize` sets the size (`10Gi` when unset). Geass grows the volume to a larger value when the volume's storage class allows expansion, and never shrinks it. A value set with `kubectl` skips the checks the dashboard and the API make, so Geass applies its own. A new volume is never made under `1Gi`. A size the volume cannot take, either smaller than it has or larger on a class that cannot grow, is not applied: Geass writes the volume's real size back to `spec.storageSize`, so the resource says the size the volume has.
- Geass records the storage class it chose for the volume in the annotation `geass.dev/storage-class` the first time. An empty value means the cluster default. A volume's class cannot change, so do not edit the annotation.
- Do not rename a bucket after it exists. Geass would create the new name and leave the old bucket, and the credentials, behind.
- On delete, Geass runs the finalizer. For a bucket on the cluster server it deletes the bucket and its login. For an S3 store it deletes the IAM user, and when `createBucket` is `true` it empties and deletes the bucket too.
- Just before it first creates a bucket or a user, Geass sets the annotation `geass.dev/provisioning-started: "true"` on a project store. A store with it, or with a connection Secret, may own resources outside the cluster, so deleting it tries to clean them up even if it never became ready. For a store that never had a connection Secret that cleanup is best effort: a failure is logged and the deletion still completes. Do not edit the annotation.
- Deleting the cluster object storage server is refused while project buckets exist: Geass returns the error `delete project buckets before removing the object storage server: <names>` and the resource stays in `Terminating`. Deleting it removes the server, its network policy `geass-s3` and all the data on its volume.

:::danger
With `spec.createBucket: true`, deleting an S3 `GeassObjectStore` empties the bucket, including old object versions, and then deletes it. The data is gone. Leave `createBucket` off for a bucket that holds data you want to keep.
:::

## What happens under the hood

### The cluster object storage server

Geass runs the server itself from a pinned image. There is no Helm chart.

- Secret `<name>-root` in `geass-system` holds the generated login of the server. For Garage it also holds the admin token, an RPC secret and a metrics token.
- Secret `geass-s3-config` in `geass-system` holds the files the server reads at start.
- PersistentVolumeClaim `geass-s3`, of `spec.storageSize` (10Gi when unset). It is made once, on the replicated storage class when the cluster has Longhorn and on the cluster's default class otherwise (see [The volume](#the-volume)). Geass raises the size later and never lowers it.
- NetworkPolicy `geass-s3`, which closes every port of the server's pod except the ones that are meant to be reached (see [The network policy](#the-network-policy)).
- Deployment `geass-s3` with one pod, and Service `geass-s3`. The pod has one container for the server, and for SeaweedFS a second one, `s3-forwarder`, that publishes the S3 port. The Service publishes the S3 port, 8333 for SeaweedFS and 3900 for Garage, and for Garage the admin port 3903, which Geass uses to make buckets and keys. The pod is recreated rather than rolled, because the volume can be mounted by one pod only.
- Secret `<name>-connection` in `geass-system`.

SeaweedFS runs its master, volume server, filer and S3 gateway as one process. It listens only on localhost, because it serves internal services beside S3 that need no login, and the `s3-forwarder` container is the only thing in the pod that other pods can reach. Both servers run as a non-root user, and the pod is given 60 seconds to stop, because SeaweedFS needs about half a minute to shut down cleanly.

#### The volume

Geass decides the volume's storage class once, when the server is first created, and records it in the annotation `geass.dev/storage-class`. A server that already has a volume keeps that volume's class.

| Cluster | Storage class | Where the data is |
| --- | --- | --- |
| Geass's replicated storage (Longhorn) is installed | `geass-replicated`, or the region's class `geass-replicated-<region>` when the server is placed in a region | Copies on several nodes, up to three, as for database volumes. A server placed in a region keeps its copies in that region. |
| Longhorn is installed and the server is placed on nodes with tags (`spec.runOn.tags`) | `geass-tagged-<hash>`, for the server's region and tags | Copies only on nodes in that region that carry every one of the tags, so the data is never replicated onto other nodes. The cluster controller creates the class itself, once every node it covers has a ready Longhorn manager. |
| No Longhorn | The cluster's default class (`local-path` on K3s) | One node, the one the pod runs on. |

While Longhorn is being installed, or while the class for the server's region or tags is not made yet, Geass makes no volume and the server reports `Storage is starting`. Waiting on Longhorn itself is limited to 15 minutes, after which it reports `Storage is unavailable`. Waiting on a region's or tags' class has no limit. See [Storage and volumes](/docs/guides/storage/) for the classes.

The size is `spec.storageSize`. A larger value grows the volume in place when its class allows expansion. Clusters with Longhorn can resize. The default `local-path` storage of K3s cannot, so there the dashboard says "This cluster's storage can't be resized." and the REST API refuses with `this cluster's storage can't be resized, so the server stays at 10Gi` (with the current size).

#### The network policy

The NetworkPolicy `geass-s3` in `geass-system` selects the server's pod (label `app.kubernetes.io/name: geass-s3`) and limits ingress:

| Port | Who can reach it |
| --- | --- |
| S3: 8333 for SeaweedFS, 3900 for Garage | Every namespace. That is how apps reach their buckets, and each project's login limits it to its own bucket. |
| Garage's admin port, 3903 | Only the platform's own namespace, `geass-system`. |
| Every other port | Nobody. |

It takes effect on clusters whose network enforces policies, which K3s's does. The server closes the same ports itself: SeaweedFS listens only on localhost, and the forwarder container publishes the S3 port, and Garage's other ports are closed. So the policy is a second lock. The controller keeps the policy in place and puts it back if it is edited or deleted by hand. Deleting the server deletes it.

### A bucket on the cluster server

1. Geass creates each bucket on the server with the server's own login, under its stored name `<project>.<environment>.<name>`.
2. It creates a login for the store that can reach only that store's buckets: a user `geass-<name>` through SeaweedFS's IAM API, or a key through Garage's admin API. The server generates the access key and the secret key.
3. It writes `<name>-connection` into the project's namespace with those credentials.

If the server has been reset and forgets the login, Geass issues a new key on its next pass and updates the Secret.

### A bucket on AWS

1. If `createBucket` is `true`, Geass creates the buckets.
2. It creates an IAM user `geass-<name>` with an inline policy named `geass-bucket` that allows list, read, write and delete on the store's buckets only.
3. It creates an access key once and keeps it in `<name>-connection`.

The AWS credentials in the connection need permission to create buckets and to create IAM users, policies and access keys.

:::caution
S3 bucket names are global. If the name belongs to another AWS account, S3 answers with a conflict and Geass treats that as success. The store then reports `Ready`, but your app is denied access. Pick a name that is unique to you.
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

The bucket is a draft. Geass creates nothing for a draft until you deploy it. Fill in **Name** on the **Settings** tab, save, then select **Deploy changes** in the project's top bar. After a deploy the message moves on to ready. If it stays at this message after a deploy, check that `spec.deploy.enabled` is `true`.

### `Configure the bucket name in settings, then deploy`

Open the bucket's **Settings** tab, fill in **Name**, save, then deploy.

### `Set up object storage in cluster settings first`

A bucket on the cluster server needs the server. Open **Settings** > **Object storage** and set up SeaweedFS or Garage. The bucket waits until the server's `Ready` is `True`.

### `Object storage is starting` stays for minutes

The server's pod is not ready. Look at it: `kubectl -n geass-system get pods | grep geass-s3`, then `kubectl -n geass-system describe pod` on it and its logs. Image pulls on a new node can be slow.

### `Storage is starting` stays

The server waits for its volume's storage class. Look at Longhorn and the classes: `kubectl get pods -n longhorn-system`, `kubectl get storageclass`. For a server placed in a region or on tags, look for `geass-replicated-<region>` or a `geass-tagged-...` class, which the cluster controller makes only when every node in that scope has a ready Longhorn manager. A node without the iSCSI initiator holds this back. See [Storage and volumes](/docs/guides/storage/#troubleshooting).

### `Storage is unavailable`

Longhorn did not come up within 15 minutes, so Geass did not create the volume. The message counts as a failure: after repeated errors `Ready` becomes `ProvisionFailed` and Geass stops retrying. Fix Longhorn, then retry with `POST /api/object-stores/<name>/retry-provision`.

### `this cluster's storage can't be resized`

The server's volume is on a class that cannot grow, such as K3s's `local-path`. A volume's class cannot change, so the size stays what it was when the server was created. Remove the server and set it up again with the size you need. That deletes everything on its volume.

### The object storage server cannot be removed

`delete project buckets before removing the object storage server` lists the buckets that still use it. Delete those first.

### An S3 bucket reports `Ready` but the app gets access denied

Check that the bucket name belongs to your account (see the caution above) and that the credentials the app uses are the ones in `<name>-connection`.

### `AWS S3 <status>: ...`

AWS refused the call. The body names the cause. Common ones are `AccessDenied` (the connection's keys lack S3 or IAM permissions) and `InvalidBucketName`.

## Related

- [Object storage](/docs/guides/object-storage/)
- [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/)
- [GeassDatabase](/docs/reference/resources/geass-database/)
- [Connect AWS](/docs/guides/connectors/aws/)
- [GeassProject](/docs/reference/resources/geass-project/)
- [Resources overview](/docs/reference/resources/overview/)
