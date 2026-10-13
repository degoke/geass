---
title: "GeassObjectStore"
description: "Reference for GeassObjectStore: the cluster-wide MinIO server, buckets on it for a project, or buckets in your own AWS S3 account."
sidebar:
  order: 5
---

A `GeassObjectStore` is object storage. It takes one of three shapes. With no `spec.project` it is the cluster-wide MinIO server, the S3-compatible service that runs in your cluster. With a project and the MinIO engine it is a bucket for that project on the MinIO server. With a project and the S3 engine it is a bucket in your own AWS account. The dashboard creates all three and is the normal way to manage them. You can also create one with `kubectl`.

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
| Cluster MinIO server | No `spec.project`. Engine `MinIO`, placement `InCluster`. | A single MinIO pod in `geass-system`. Normally named `cluster-minio`. Only one is allowed by the dashboard. |
| Project bucket on MinIO | A `spec.project`. Engine `MinIO`, placement `InCluster`. | Buckets on the cluster MinIO server, with a login of their own. Needs the server to be `Ready`. |
| Project bucket on AWS S3 | A `spec.project`. Engine `S3`, or placement `External`. | Buckets in the AWS account of a [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/). |

A store is external when `spec.engine` is `S3` or `spec.placement` is `External`. Either one is enough: the default placement, `InCluster`, does not make an `S3` store local.

## Examples

### The cluster MinIO server

```yaml title="cluster-minio.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassObjectStore
metadata:
  name: cluster-minio
  namespace: geass-system
spec:
  engine: MinIO
  placement: InCluster
  resources:
    requests:
      cpu: 250m
      memory: 512Mi
    limits:
      cpu: 250m
      memory: 512Mi
  serverRegion: eu
```

### A bucket on the MinIO server

```yaml title="uploads-bucket.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassObjectStore
metadata:
  name: uploads
  namespace: geass-system
spec:
  project: storefront
  environment: production
  engine: MinIO
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
| `spec.project` | string | Optional | None | The owning project. Empty makes this the cluster-wide MinIO server. |
| `spec.environment` | string | Optional. Required in practice when `project` is set. | Pattern `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, max length 63 | The project environment. |
| `spec.engine` | string | Required | Enum `MinIO`, `S3` | `MinIO` runs in the cluster. `S3` uses AWS. |
| `spec.placement` | string | CRD default `InCluster` | Enum `InCluster`, `External` | `External` uses AWS S3. See [The three shapes](#the-three-shapes). |
| `spec.connectionRef` | LocalObjectReference | Optional. Required for an S3 store. | None | An AWS [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/) in `geass-system`, scoped to the same project. Not used for MinIO. |
| `spec.connectionRef.name` | string | CRD default `""` | None | Name of the connection. |
| `spec.region` | string | Optional | None | AWS region for an S3 store. When empty Geass uses the connection's region, then the `region` key of its Secret, then `us-east-1`. |
| `spec.createBucket` | boolean | `false` | None | S3 stores only. Geass creates the named buckets when they do not exist. It also deletes them when the store is deleted. |
| `spec.buckets` | array of strings | Optional. A project store needs at least one to deploy. | Max 32 items. Each 3 to 63 characters, pattern `^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`. CEL: `self.all(b, !b.matches('^[0-9]+[.][0-9]+[.][0-9]+[.][0-9]+$') && !b.contains('..'))`, message "bucket names must not be formatted as an IP address or contain consecutive periods". | The buckets. A bucket on MinIO is stored on the server as `<project>.<environment>.<name>`, and the first stored name is the `bucket` key of the connection Secret. The dashboard manages a single bucket. |
| `spec.resources` | ResourceRequirements | Optional. Defaults to 250m CPU and 512Mi memory. | None | CPU and memory for the cluster MinIO server. Ignored for project buckets. |
| `spec.deploy.enabled` | boolean | `false` | None | Gates provisioning of a project bucket, but only when the resource carries the `geass.dev/deploy-workflow: "true"` annotation. The cluster MinIO server is never gated. |
| `spec.runOn.tags` | array of strings | Optional | Max 8 items, each matching `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$` and at most 40 characters, set semantics | Node tags the cluster MinIO server must run on. Read once, when it is first set up. A project that requires node tags can keep buckets on the server only when these cover them. |
| `spec.serverRegion` | string | Optional | Pattern `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, max length 40 | Region of the cluster MinIO server. Project buckets follow their project instead. |

:::note
The Go types and the generated CRD agree for every field on this page.
:::

Geass itself fills in `spec.resources` on the cluster MinIO server when it has no `requests`. You will see 250m and 512Mi appear after the first reconcile.

## Status

| Field | Type | Meaning |
| --- | --- | --- |
| `status.targetNamespace` | string | The namespace of the connection Secret: `geass-system` for the server, `<project>-<environment>` for a bucket. |
| `status.connectionSecret` | string | Name of the Secret with the access details, `<name>-connection`. |
| `status.endpoint` | string | The S3 endpoint. For MinIO, `http://geass-minio.geass-system.svc:9000`. For AWS, `https://s3.<region>.amazonaws.com`. |
| `status.reconcileRetry` | object | Failure counters. Same as for [GeassDatabase](/docs/reference/resources/geass-database/#ready-condition-and-retries). |
| `status.provision` | object | Deploy history. Same as for GeassDatabase. |
| `status.conditions` | list | One condition: `Ready`. |

`Ready` has three reasons:

| Reason | Status | Meaning |
| --- | --- | --- |
| `ObjectStoreReady` | `True` | The message is `Cluster MinIO server is ready`, `Bucket is ready on the cluster MinIO server` or `AWS S3 bucket is ready`. |
| `ReconcileError` | `False` | Not ready yet. The message says what Geass waits for or what failed. Geass checks again after 30 seconds. |
| `ProvisionFailed` | `False` | Geass gave up after repeated errors. The retry rules are the same as for GeassDatabase: see [Ready condition and retries](/docs/reference/resources/geass-database/#ready-condition-and-retries). |

The messages you can see:

| Message | Cause |
| --- | --- |
| `Resource is configured and waiting for deployment` | A draft: a project store with the `geass.dev/deploy-workflow: "true"` annotation and `spec.deploy.enabled: false`. Nothing is created for it until you deploy. |
| `Configure the bucket name in settings, then deploy` | `spec.buckets` is empty. |
| `Set up the MinIO server in cluster settings first` | The cluster MinIO server does not exist or is not `Ready`. |
| `This MinIO server does not run on nodes tagged <tags>, which the project requires` | The project's `spec.requiredTags` include a tag that the MinIO server is not placed on (its `geass.dev/node-placement` has it in neither `tags` nor `required`). The message counts as a failure. |
| `cluster MinIO credentials are unavailable` | The server's root Secret is missing or empty. |
| `Installing MinIO chart` | Helm is installing or upgrading the MinIO chart. |
| `Service is starting`, `Starting service`, `Service failed to start` | The MinIO pod is starting or failing. |
| `an AWS connection is required for external buckets` | An S3 store has no `connectionRef`. |
| `AWS connection is unavailable`, `AWS credentials are unavailable`, `AWS access key and secret key are required` | The connection or its Secret is missing or incomplete. |
| `external buckets require an AWS connection` | The connection's provider is not `AWS`. |
| `cloud connection "<name>" was not found`, `... is scoped to project "<project>"`, `... must be scoped to a project` | The connection is not usable by this project. |
| `bucket name "<name>" must be 3-63 characters` and similar | A bucket name is not a legal S3 name. |
| `bucket name "<name>" is too long: on the MinIO server it is stored as "<project>.<environment>.<name>" ...` | A project bucket on MinIO is stored behind its project and environment, and the whole name has to be 63 characters or fewer. The message gives the longest name that fits. |
| `AWS S3 <status>: <body>` | AWS refused a call. |
| `unsupported engine "<engine>"` | `spec.engine` is not `MinIO` or `S3`. |

## What the dashboard writes

| Where | What it does |
| --- | --- |
| **Settings** > **Object storage**, **MinIO** card, **Set up**, then **Set up MinIO server** | Creates the cluster MinIO server `cluster-minio`: no project, `engine: MinIO`, `placement: InCluster`, `resources` from the CPU and memory you choose. It is not a draft. The card is the only backend you can pick. **Garage SFS** and **SeaweedFS** show as `Coming soon`. |
| **Settings** > **Object storage**, **Size** | Updates `spec.resources` of the server after a capacity check. |
| **Settings** > **Object storage**, **Remove cluster storage** section, **Remove MinIO** | Opens a confirmation. Type `remove` and click **Remove permanently** to delete the server. The dashboard refuses with `delete project buckets before removing the MinIO server` while project buckets exist. |
| **+ Add resource** > **Bucket** > **In-cluster bucket** | Creates a draft bucket (`engine: MinIO`, `placement: InCluster`). The entry is disabled with `Set up the MinIO server in platform settings first` until the server exists. |
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

They call `GET /api/v1/object-stores` and `GET /api/v1/object-stores/{name}`. `details` of a summary holds `placement` and the bucket names, and `state` follows the same rules as for [GeassDatabase](/docs/reference/resources/geass-database/#from-the-command-line-and-the-api). That includes `draft` for a store whose `spec.deploy.enabled` is `false`, which is the case for the cluster MinIO server: nothing sets the field on it, so it is listed as `draft` while it runs. There is no connection endpoint for buckets, so neither the command line nor the MCP server returns an access key or a secret key: read them from the connection Secret. See [Object storage](/docs/guides/object-storage/#command-line-and-ai-agents).

## Connection Secret

Geass writes `<name>-connection` with four keys: `endpoint`, `accessKey`, `secretKey` and `bucket` (the first entry of `spec.buckets`, or the resource name). For a bucket on MinIO the value is the name as stored on the server, `<project>.<environment>.<name>`. For the cluster server the keys are the MinIO root credentials, in `geass-system`. For a project bucket they are credentials of its own, in `<project>-<environment>`.

A bucket's connection Secret has no `uri` key. The **Add reference** action on a service's **Variables** tab adds four variables for a bucket: `S3_ENDPOINT` from `endpoint`, `S3_ACCESS_KEY_ID` from `accessKey`, `S3_SECRET_ACCESS_KEY` from `secretKey` and `S3_BUCKET` from `bucket`. It refuses, naming the variable, when the service already has one of those names set by hand.

## Editing by hand

You can create and edit a `GeassObjectStore` with `kubectl`.

- Geass pins the cluster MinIO server's placement (`serverRegion` and `runOn`) in the annotation `geass.dev/node-placement` the first time. Its volume stays there. Do not edit the annotation.
- The server's storage is fixed at 10Gi in the chart values. There is no field for it.
- Do not rename a bucket after it exists. Geass would create the new name and leave the old bucket, and the credentials, behind.
- On delete, Geass runs the finalizer. For a MinIO bucket it deletes the bucket and its login. For an S3 store it deletes the IAM user, and when `createBucket` is `true` it empties and deletes the bucket too.
- Just before it first creates a bucket or a user, Geass sets the annotation `geass.dev/provisioning-started: "true"` on a project store. A store with it, or with a connection Secret, may own resources outside the cluster, so deleting it tries to clean them up even if it never became ready. For a store that never had a connection Secret that cleanup is best effort: a failure is logged and the deletion still completes. Do not edit the annotation.
- Deleting the cluster MinIO server is refused while project buckets exist: Geass returns the error `delete project buckets before removing the MinIO server: <names>` and the resource stays in `Terminating`.

:::danger
With `spec.createBucket: true`, deleting an S3 `GeassObjectStore` empties the bucket, including old object versions, and then deletes it. The data is gone. Leave `createBucket` off for a bucket that holds data you want to keep.
:::

## What happens under the hood

### The cluster MinIO server

- Secret `<name>-root` in `geass-system` holds generated root credentials.
- HelmChart `geass-minio` in `kube-system` installs the MinIO chart (version 5.4.0, standalone mode, a 10Gi volume) into `geass-system`. The images are pinned.
- Pod `geass-minio-...`, Service `geass-minio` on port 9000 and Service `geass-minio-console` on port 9001.
- Secret `<name>-connection` in `geass-system`.

### A bucket on MinIO

1. Geass creates each bucket on the server with the root credentials, under its stored name `<project>.<environment>.<name>`.
2. It writes `<name>-connection` into the project's namespace with generated credentials: the access key is the store's name and the secret key is random.
3. It stores the same credentials in the Secret `<name>-minio-user` in `geass-system`.
4. It re-renders the `geass-minio` HelmChart. The chart creates a MinIO user for the store and a policy named `geass-<name>` that allows only that store's buckets.

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
kubectl -n geass-system get geassobjectstore cluster-minio -o yaml

# The MinIO server
kubectl -n kube-system get helmchart geass-minio
kubectl -n geass-system get pods,svc | grep minio
kubectl -n geass-system logs deploy/geass-minio

# A project bucket's credentials
kubectl -n storefront-production get secret uploads-connection \
  -o jsonpath='{.data.endpoint}' | base64 -d
```

## Troubleshooting

### `Resource is configured and waiting for deployment`

The bucket is a draft. Geass creates nothing for a draft until you deploy it. Fill in **Name** on the **Settings** tab, save, then select **Deploy changes** in the project's top bar. After a deploy the message moves on to `Installing MinIO chart` (MinIO) and then to ready. If it stays at this message after a deploy, check that `spec.deploy.enabled` is `true`.

### `Configure the bucket name in settings, then deploy`

Open the bucket's **Settings** tab, fill in **Name**, save, then deploy.

### `Set up the MinIO server in cluster settings first`

A bucket on MinIO needs the server. Open **Settings** > **Object storage** and set up MinIO. The bucket waits until the server's `Ready` is `True`.

### `Installing MinIO chart` stays for minutes

Helm is installing or upgrading the chart in `kube-system`. Look at the install Job: `kubectl -n kube-system get jobs | grep helm-install-geass-minio`, then its pod logs. Image pulls on a new node can be slow.

### The MinIO server cannot be removed

`delete project buckets before removing the MinIO server` lists the buckets that still use it. Delete those first.

### An S3 bucket reports `Ready` but the app gets access denied

Check that the bucket name belongs to your account (see the caution above) and that the credentials the app uses are the ones in `<name>-connection`.

### `AWS S3 <status>: ...`

AWS refused the call. The body names the cause. Common ones are `AccessDenied` (the connection's keys lack S3 or IAM permissions) and `InvalidBucketName`.

## Related

- [Object storage](/docs/guides/object-storage/)
- [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/)
- [GeassDatabase](/docs/reference/resources/geass-database/)
- [Connections](/docs/guides/connections/)
- [GeassProject](/docs/reference/resources/geass-project/)
- [Resources overview](/docs/reference/resources/overview/)
