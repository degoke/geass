---
title: "GeassProject"
description: "Reference for GeassProject: environments, variables, webhooks, pull request environments, GitHub reporting, backups, network isolation, region, and node tags."
sidebar:
  order: 1
---

A `GeassProject` is the top of the hierarchy in Geass. It groups the services, databases, and buckets of one product. It owns the environments that those resources run in, for example `production` and `staging`. For every environment, the operator creates one Kubernetes namespace and keeps it configured.

The dashboard creates and edits projects for you. **New project** on the **Projects** page creates a project, and **Project Settings** changes it. You can also create a project with `kubectl`, and the operator treats it the same way. Every Geass resource, including projects, is in the `geass-system` namespace.

## Summary

| Property | Value |
| --- | --- |
| API version | `geass.geass.dev/v1alpha1` |
| Kind | `GeassProject` |
| Plural | `geassprojects` |
| Scope | Namespaced |
| Namespace | `geass-system` |
| Subresources | `status` |
| Finalizer | `geass.geass.dev/project` |
| Printer columns | `Ready` (`.status.conditions[?(@.type=="Ready")].status`) |

The dashboard names the objects `proj-` followed by eight hexadecimal characters, for example `proj-32b0a870`. This name is the project ID. The project ID appears in namespace names, in labels, and in the `project` field of every resource that belongs to the project. The name that people see in the dashboard is `spec.displayName`.

## Minimal example

This project has one environment and uses the default cluster that the installer creates.

```yaml title="project.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassProject
metadata:
  name: storefront
  namespace: geass-system
spec:
  displayName: storefront
  clusterRef:
    name: default
  environments:
    - production
```

Apply the project, and check that it becomes ready:

```bash
kubectl apply -f project.yaml
kubectl get geassproject storefront -n geass-system
```

This example is a project with every optional block filled in:

```yaml title="project-full.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassProject
metadata:
  name: storefront
  namespace: geass-system
spec:
  displayName: storefront
  clusterRef:
    name: default
  region: eu
  requiredTags:
    - hipaa
  environments:
    - production
    - staging
  sharedVariables:
    - name: LOG_LEVEL
      environment: production
      value: info
    - name: PAYMENTS_API_KEY
      environment: production
      secretRef:
        name: storefront-shared-secrets
        key: production__PAYMENTS_API_KEY
  githubConnectionRef:
    name: storefront-github
  webhooks:
    - id: 3f9a1c2e
      url: https://hooks.example.com/geass
      events:
        - deployment.succeeded
        - deployment.failed
  prEnvironments:
    enabled: true
    baseEnvironment: staging
    maxOpen: 5
  githubReporting: true
  backupStorage:
    connectionRef:
      name: aws-main
    bucket: acme-storefront-backups
    region: eu-north-1
  network:
    isolation: Project
    allowFromProjects:
      - proj-32b0a870
```

The names `storefront-shared-secrets`, `storefront-github`, `aws-main`, and `proj-32b0a870` must refer to objects that exist. The sections below explain each reference.

## Spec fields

Required fields are marked. Every other field is optional.

### Identity and placement

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.displayName` | string | none | none | The name that the dashboard shows. The dashboard stores it as a lowercase slug such as `morning-beach`, and generates one when it creates a project. The operator does not read it. |
| `spec.clusterRef.name` | string | none | Required | The `GeassCluster` that owns the project, looked up in `geass-system`. The installer creates a cluster named `default`. You cannot change this field after the project has been reconciled. |
| `spec.org` | string | empty | Pattern `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, 63 characters at most | The organization that owns the project: the name of a [GeassOrganization](/docs/reference/resources/geass-organization/). Empty means that the project belongs to no organization. After [organizations are on](/docs/guides/organizations-and-workspaces/), only the administrators of the platform can see such a project. A project that the people of an organization make has this field from the start. The platform assigns the other projects with `POST /api/projects/{project}/assign`. |
| `spec.workspace` | string | empty | Pattern `^ws-[a-f0-9]{6}$` | The workspace of the organization that the project is in. Empty means that the project belongs to the organization as a whole. Only the administrators of the organization can see such a project. |
| `spec.region` | string | empty | Pattern `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, 40 characters at most | Restricts the workloads of the project to nodes in this region. Empty means the default region of the cluster (`spec.defaultRegion` on the [GeassCluster](/docs/reference/resources/geass-cluster/)). If the cluster has no default region, empty means any node. |
| `spec.requiredTags` | list of strings | empty | At most 8 unique items. Each matches `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, 40 characters at most | Node tags that everything the project runs must run on, such as `hipaa`. A node needs all of them. See [Required node tags](#required-node-tags). |
| `spec.environments` | list of strings | none | Required, 1 to 100 items. Each item matches `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, 63 characters at most | The environments of the project. Each environment becomes a namespace named `<project>-<environment>`. |

Two more rules apply when the operator reconciles the list:

- The operator rejects duplicates. The project reports `Project environments must be unique`.
- `<project>-<environment>` must be a valid DNS label of 63 characters or fewer. If it is not, the project reports an error such as `project/environment namespace "..." is invalid`.

The region is selected when the project is created. The dashboard refuses to change the region when the project has a database that runs in the cluster. The reason is that the data of a database is on the nodes of its region. The operator itself does not enforce this rule. Thus, the rule is not enforced for edits made with `kubectl`. For how regions and node tags work, see [Scaling and placement](/docs/guides/scaling-and-placement/).

### Required node tags

`spec.requiredTags` keeps the workloads of a project on the nodes that carry its tags. The tags that a service selects under **Run on** apply only to that service. These tags apply to everything that the project runs:

- Services.
- Databases.
- Buckets.
- Builds.
- Function schedules.
- The jobs that back up and restore its data.

Geass puts a node selector and a toleration for each tag on those pods. Geass also puts the same tags on the storage class of the project. Thus, the copies of a volume also stay on those nodes.

The tags should be compliance tags on the nodes that carry them (`spec.placement.tags[].compliance` on a [GeassNode](/docs/reference/resources/geass-node/)). Then the work of no other project lands on these nodes. The dashboard and the API check these conditions before they save:

- Every tag exists on a node in the region of the project.
- Every node in the region that carries a tag is reserved for that tag.
- Every in-cluster database of the project that is already provisioned runs on nodes with the tags that you add.

A change made with `kubectl` skips these checks. A project that requires tags also has these properties:

- Its namespaces get the annotation `geass.dev/required-tags`.
- Geass does not ship the logs of those namespaces to the shared log store.
- Geass refuses a database on a shared server, unless that server runs on nodes with all the tags. The same rule applies to a bucket on the object storage server.

For the whole picture and its limits, see [Compliance node tags](/docs/guides/compliance-tags/).

### Shared variables

`spec.sharedVariables` holds values that services in one environment can use. For the guide, see [Environment variables](/docs/guides/environment-variables/).

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.sharedVariables[].name` | string | none | Required. Pattern `^[A-Z_][A-Z0-9_]*$` | The environment variable name. |
| `spec.sharedVariables[].environment` | string | none | Required. 1 to 63 characters, pattern `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$` | The environment that the variable belongs to. A variable exists in one environment only. |
| `spec.sharedVariables[].value` | string | empty | none | A plain value. The operator writes it to a ConfigMap. |
| `spec.sharedVariables[].secretRef.name` | string | none | none | A Secret in `geass-system` that holds the value. When `secretRef` is set, it wins over `value`. |
| `spec.sharedVariables[].secretRef.key` | string | none | Required inside `secretRef` | The key in that Secret. |
| `spec.sharedVariables[].secretRef.optional` | boolean | none | none | A standard `SecretKeySelector` field. The operator does not read it. |

For each environment, the operator copies the variables of that environment into the namespace:

- Plain values go into the ConfigMap `geass-shared-variables`.
- Secret values go into the Secret `geass-shared-secrets`, under the variable name as the key.

If a ConfigMap or Secret would be empty, the operator deletes it. A `secretRef` might point to a missing Secret or key. If so, the reconcile stops with an error in the operator log, for example `shared variable PAYMENTS_API_KEY secret key "production__PAYMENTS_API_KEY" was not found`. See [Troubleshooting](#troubleshooting).

The dashboard stores secret values in the Secret `<project>-shared-secrets` in `geass-system`, under keys named `<environment>__<NAME>`. The dashboard points `secretRef` to these keys. A service selects the variables that it receives with `spec.sharedVariableRefs` on the [GeassApp](/docs/reference/resources/geass-app/#variables-and-files).

:::note
The value field in the dashboard shows the placeholder `VALUE or ${{REF}}`. Nothing in Geass resolves `${{REF}}` references. Geass stores and injects the text as it is.
:::

### GitHub connection

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.githubConnectionRef.name` | string | none | none | The [GeassGitHubConnection](/docs/reference/resources/geass-github-connection/) that gives this project access to GitHub. The dashboard sets it to `<project>-github` when you click **Connect to GitHub**. |

The CRD describes this field as a reference to a Secret. The code resolves it as a `GeassGitHubConnection` in `geass-system`. The project controller of the operator does not read the field. The dashboard reads the field for these tasks:

- To list repositories.
- To validate Git services.
- To match incoming GitHub webhooks to the project.

The service and build controllers of the operator also read the field. They use it to report back to GitHub when the
service or build names no GitHub connection of its own.

### GitHub reporting

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.githubReporting` | boolean | `true` | Optional. The CRD defaults it to `true`, and unset is read as on. | Whether Geass reports the repository services of this project back to GitHub. The reports are a commit status `geass/build/<service>` for each build, a GitHub Deployment for each deploy, and one comment on each pull request that has an environment. To stop all three, set it to `false`. |

This field does nothing until the project is connected to GitHub. The three reports need these permissions of the GitHub App at read and write: **Commit statuses**, **Deployments**, and **Pull requests**. An app registered before Geass reported to GitHub does not have these permissions. In this case, the reports are refused, and the `GitHubReporting` condition of the project's [GeassGitHubConnection](/docs/reference/resources/geass-github-connection/#conditions) says so. Builds, deploys, and pull request environments are not affected, whatever the value. See [Reporting back to GitHub](/docs/guides/github-and-builds/#reporting-back-to-github).

### Webhooks

`spec.webhooks` lists up to 20 outbound webhooks. Geass sends each webhook a signed `POST` when a service deploy starts, succeeds, or fails.

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.webhooks[].id` | string | none | Required. Pattern `^[a-z0-9]{8}$` | Identifies the webhook in the dashboard, in delivery records, and as the key of its signing secret. The dashboard generates eight hexadecimal characters. |
| `spec.webhooks[].url` | string | none | Required. 8 to 2048 characters | The receiver URL. |
| `spec.webhooks[].events` | list of strings | empty | Each item is `deployment.started`, `deployment.succeeded`, or `deployment.failed` | The events to send. Empty means every event. The dashboard stores "all three events" as an empty list. Thus, future event types also reach the webhook. |
| `spec.webhooks[].disabled` | boolean | `false` | none | Pauses deliveries without deleting the webhook. |

The signing secret of a webhook is never part of the spec. Geass stores it in the Secret `<project>-webhooks` in `geass-system`, under the webhook ID. The secret has the form `whsec_` followed by 48 hexadecimal characters. If a webhook has no entry in that Secret, Geass delivers it without a signature. The dashboard shows the secret one time, when you create the webhook or rotate its secret.

How a delivery works:

| Topic | Behavior |
| --- | --- |
| When events fire | Geass sends `deployment.started` when you deploy a service from the dashboard or the REST API. Geass sends `deployment.succeeded` when the operator finds the service ready. Geass sends `deployment.failed` when the service reaches `ProvisionFailed`. Geass sends success and failure events only for deploys that were announced as started. |
| Request | `POST` with a JSON body and these headers: `Content-Type: application/json`, `User-Agent: Geass-Webhooks/1`, `X-Geass-Event`, `X-Geass-Delivery`, `X-Geass-Timestamp` (Unix seconds), and `X-Geass-Signature`. |
| Signature | `t=<timestamp>,v1=<hex>`, where `v1` is the HMAC-SHA256 of `<timestamp>.<raw body>` with the signing secret. Reject requests whose timestamp is more than a few minutes old. |
| Retries | Geass tries an event up to 3 times. It waits 1 second, and then 5 seconds. Geass retries on network errors, HTTP 5xx, 429, and 408. Geass records any other status of 300 or more as an error, without a retry. The **Send test** action makes one attempt. |
| Limits | 10 seconds for each request. Geass does not follow redirects. At most 8 deliveries run at the same time. |
| Network rules | Geass always refuses `localhost`, loopback, and link-local addresses. Geass refuses private network addresses unless the operator runs with `GEASS_WEBHOOKS_ALLOW_PRIVATE_NETWORKS=true`. Geass refuses URLs with a user name, a password, or a fragment. |

A delivery body looks like this:

```json
{
  "id": "evt_9c1e0a47b2d35f68",
  "type": "deployment.succeeded",
  "createdAt": "2026-10-12T09:30:12Z",
  "project": { "name": "storefront", "displayName": "storefront" },
  "environment": "production",
  "service": {
    "name": "storefront-web",
    "displayName": "storefront-web",
    "kind": "service",
    "url": "https://shop.example.com/"
  },
  "deployment": {
    "revision": "2026-10-12T09:29:41.123456789Z",
    "image": "ghcr.io/acme/storefront:1.4.2"
  }
}
```

`service.kind` is `service` or `function`. `deployment.commit` appears for Git services that pin a commit. `message` appears on `deployment.failed` and on test events. For test events, `type` is `test`.

### Pull request environments

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.prEnvironments.enabled` | boolean | none | Required inside `prEnvironments` | Turns pull request environments on. |
| `spec.prEnvironments.baseEnvironment` | string | first item of `spec.environments` | Pattern `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, 63 characters at most | The environment that Geass copies for each pull request. |
| `spec.prEnvironments.maxOpen` | integer | `5` | 1 to 25 | The most pull request environments that can exist at the same time. The default applies when the `prEnvironments` object exists. |

Geass adds an environment named `pr-<number>` to `spec.environments` when a pull request does one of these things:

- It opens or reopens.
- It becomes ready for review.
- It receives a push.

If two repositories in the project have a pull request with the same number, the second environment is named `pr-<number>-<repository>`. The environment gets copies of the services and in-cluster databases of the base environment, and copies of its shared variables. Geass does not copy buckets, logical databases, or external databases. When the pull request closes, Geass removes the environment and everything in it.

Geass does this only when all of these conditions are true:

- GitHub reports the event through the platform GitHub App.
- The repository belongs to a Git service of the base environment that builds the branch that the pull request targets.
- The pull request comes from the same repository. Geass ignores pull requests from forks.

`status.pullRequests` lists the open pull request environments. For the full guide, see [Pull request environments](/docs/guides/pull-request-environments/).

### Backup storage

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.backupStorage.connectionRef.name` | string | none | Required inside `backupStorage` | The AWS [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/) that owns the bucket. |
| `spec.backupStorage.bucket` | string | none | Required. 3 to 63 characters, pattern `^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$` | The S3 bucket that holds the backups of the databases of the project. Geass creates the bucket when it does not exist. |
| `spec.backupStorage.region` | string | the region of the connection | none | The AWS region of the bucket. |

Without this block, databases that turn on backups report that they need storage. When you select an AWS connection under **Backups**, the dashboard fills in the block and generates the bucket name. The database controller creates the bucket and a user in your AWS account. This user can reach only that bucket. See [Backups and restore](/docs/guides/backups-and-restore/).

### Network isolation

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.network.isolation` | string | `Project` | `Project` or `None` | With `Project`, only these sources can reach the services of the project: the project itself, the platform, and visitors who arrive through your domains. `None` leaves the services open to every service in the cluster. |
| `spec.network.allowFromProjects` | list of strings | empty | At most 20 unique items. Each matches `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, 63 characters at most | Other projects, by project ID, whose services can reach the services of this project. It has no effect when isolation is `None`. |
| `spec.network.blockNodeAddresses` | boolean | none: `false` when absent | none | `true` stops the services of the project from opening connections to the machines of the cluster. This block covers every node address and the two lowest addresses of the pod range of every node. The Kubernetes API stays open on the control plane. This field is independent of `isolation`. Geass keeps the rule in step with the nodes when they join and leave. Projects created by the dashboard or the API set it to `true`. A project that existed before this field does not have it. |

A project with no `spec.network` is isolated, and it is not blocked from the machines. The policies apply to every environment of the project, and they change within seconds of a save. See [Network isolation](/docs/guides/network-isolation/). A project of an organization might also be held to the [outbound limits](/docs/guides/outbound-limits/) of the organization. These limits are not a setting of the project.

### Quota

Without `spec.projectQuota` and `spec.environmentQuota`, nothing is capped. `spec.projectQuota` caps the project as a whole, added up over its environments. `spec.environmentQuota` caps **each** environment of the project, pull request environments included. An environment is held to the lower of the two caps.

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.projectQuota.cpu` | quantity | none | none | The most CPU that the pods of the project can ask for in total, such as `8`. |
| `spec.projectQuota.memory` | quantity | none | none | The most memory that they can ask for in total, such as `16Gi`. |
| `spec.projectQuota.storage` | quantity | none | none | The most disk that their volumes can ask for in total, such as `200Gi`. |
| `spec.projectQuota.pods` | integer | none | At least 1 | The most pods that the project can have running or waiting at the same time. |

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.environmentQuota.cpu` | quantity | none | none | The most CPU that the pods of one environment can ask for in total, such as `4` or `500m`. |
| `spec.environmentQuota.memory` | quantity | none | none | The most memory that they can ask for in total, such as `8Gi`. |
| `spec.environmentQuota.storage` | quantity | none | none | The most disk that their volumes can ask for in total, such as `50Gi`. |
| `spec.environmentQuota.pods` | integer | none | At least 1 | The most pods that one environment can have running or waiting at the same time. |

A field that you leave out is not capped. The caps count requests, not use. Each environment can ask for what the other environments leave of the cap of the project. Thus, Geass rewrites the cap of each environment when usage changes. See [Project limits](/docs/guides/project-limits/).

## Status

| Field | Type | Meaning |
| --- | --- | --- |
| `status.clusterRef` | string | The cluster that the operator observed. It is set after the first successful reconcile, and it prevents a project from moving to another cluster. |
| `status.environments[].name` | string | The environment name. At most 100 entries. |
| `status.environments[].namespace` | string | The namespace of that environment. |
| `status.conditions` | list | The `Ready` condition, `TenantLimitsApplied` for a project of an organization with noisy-neighbour limits, and `EgressLimitsApplied` for a project with outbound limits. See below. |
| `status.reconcileRetry.reconcileFailures` | integer | Consecutive errors counted toward the retry limit. |
| `status.reconcileRetry.reconcileFailureStarted` | time | When the current streak of errors began. |
| `status.reconcileRetry.reconcileFailureGeneration` | integer | The `metadata.generation` at which the project was marked `ProvisionFailed`. |
| `status.webhookDeliveries` | list, at most 20 | The newest deliveries first. Each entry has `webhookId`, `event`, `deliveryId`, `statusCode`, `error`, `attempts`, `durationMillis`, and `time`. The webhook dispatcher writes this field. |
| `status.pullRequests` | list, at most 50 | The open pull request environments. Each entry has `number`, `repository`, `branch`, `environment`, `url`, and `openedAt`. The dashboard writes this field when it handles GitHub events. |

### Conditions

The operator sets the condition type `Ready`. It also sets `TenantLimitsApplied` and `EgressLimitsApplied`, which are described below the table.

| Status | Reason | Message | Meaning |
| --- | --- | --- | --- |
| `True` | `ProjectReady` | `Project environments are ready` | Every environment in `spec.environments` has a namespace with its policies and shared variables. |
| `False` | `ReconcileError` | `Referenced GeassCluster is not available` | The cluster named in `spec.clusterRef` does not exist in `geass-system`. |
| `False` | `ReconcileError` | `Referenced GeassCluster is not ready` | The cluster exists, but its `Ready` condition is not `True`. The operator checks again every 30 seconds and does not stop. |
| `False` | `ReconcileError` | `Changing a project's clusterRef is not supported` | `spec.clusterRef` differs from `status.clusterRef`. |
| `False` | `ReconcileError` | `Project environments must be unique` | `spec.environments` contains a duplicate. |
| `False` | `ReconcileError` | `invalid project "..."`, `invalid environment "..."`, or `project/environment namespace "..." is invalid` | A name is not a valid DNS label, or the combined namespace name is too long. |
| `False` | `ProvisionFailed` | `Provisioning failed after repeated errors. Update settings or platform configuration, then save or redeploy to try again.` | The same kind of error repeated 5 times or for 3 minutes. The operator stops retrying until you change the spec. |

A project of an organization that has [noisy-neighbour limits](/docs/guides/tenant-limits/) also carries `TenantLimitsApplied`. This condition has these values:

- `True` (`Applied`): the limits are in every environment.
- `False` with `InvalidLimits`: Geass cannot hold the limits as written. Each environment keeps what it had.
- `False` with `NotApplied`: Geass could not write the limits of an environment. The operator tries again.
- `False` with `BandwidthNotApplied`: the cluster has no mutating admission policies.

`TenantLimitsApplied` does not change `Ready`. A project of an organization without limits has no such condition.

A project of an organization that has [outbound limits](/docs/guides/outbound-limits/) also carries `EgressLimitsApplied`. This condition has these values:

- `True` (`Applied`): the limits are in the network policy of every environment, and the cluster enforces network policies. The `NetworkPolicyEnforced` condition of the cluster shows this enforcement.
- `False` with `NotEnforced`: the cluster does not enforce network policies.
- `Unknown` with `EnforcementUnknown`: the cluster has not been checked yet, or the check could not tell.
- `False` with `InvalidLimits`: Geass cannot apply the limits as written. Each environment keeps the policy that it had.

`EgressLimitsApplied` does not change `Ready`. A project without such limits has no such condition.

The operator retries a failing project every 30 seconds. The operator treats some errors as temporary, and it retries them without a limit. These errors are a cluster that is not ready, a cluster change, and a duplicate environment. Other errors count toward a limit. After 5 errors in a row, or 3 minutes, the project becomes `ProvisionFailed`. The controller then pauses until `metadata.generation` changes, that is, until you edit `spec`. The dashboard has no **Retry** button for projects. Any change to `spec` raises `metadata.generation` and resumes reconciliation.

## How the dashboard maps to this resource

| Dashboard | What it does to the project |
| --- | --- |
| **Projects** page, **New project** | Creates a project with one environment, `production`. The ID is generated, `spec.displayName` is a random slug such as `morning-beach`, and `spec.clusterRef` is the default cluster. The region selector next to the button sets `spec.region`. The selector appears only when the cluster has regions to select from. |
| **Project Settings**, **General**, **Update** | Saves `spec.displayName` (normalized to a lowercase slug) and `spec.region`. The dashboard rejects a region change when the project has an in-cluster database. |
| **Project Settings**, **Environments**, **New Environment** | Appends an environment to `spec.environments`. |
| **Project Settings**, **Environments**, **Delete**, then **Delete environment** | Removes the environment from the list, removes its shared variables, and deletes every service, database, logical database, and bucket in it. The operator then deletes the namespace. A project must keep at least one environment. |
| **Project Settings**, **Environments**, **PR Environments** | Writes `spec.prEnvironments`. The fields are **Create an environment for each pull request**, **Copy this environment**, and **Open at once**. **Close** on an open pull request removes its environment. |
| **Project Settings**, **Shared Variables** | Adds or removes entries in `spec.sharedVariables`. If you turn on **Secret**, the dashboard stores the value in the Secret `<project>-shared-secrets` and sets `secretRef`. |
| **Project Settings**, **Webhooks** | Edits `spec.webhooks`. The actions are **New webhook**, **Send test**, **Edit**, **Pause** or **Enable**, **Rotate signing secret**, and **Delete**. |
| **Project Settings**, **Backups** | Writes `spec.backupStorage` when you select an AWS connection and click **Use this connection**. **Remove** clears it. |
| **Project Settings**, **Network** | Writes `spec.network`. The switches are **Keep this project's services private** and **Keep this project's services away from the cluster's machines**. The list is **Projects allowed to reach this one**. |
| **Project Settings**, **Limits** | Writes `spec.projectQuota` (**Whole project**) and `spec.environmentQuota` (**Each environment**). The fields in each are **CPU**, **Memory**, **Storage**, and **Pods**. If you empty a field, the dashboard removes that limit. |
| **Project Settings**, **Compliance** | Writes `spec.requiredTags`. The list is the tags that nodes in the region of the project carry. **Save** applies the checks above. Only an admin can save. |
| **Project Settings**, **GitHub**, **Connect to GitHub** or **Disconnect** | Sets or clears `spec.githubConnectionRef`. **Disconnect** does not delete the connection object or its Secret. |
| **Project Settings**, **GitHub**, **Report to GitHub** | Writes `spec.githubReporting`. The card appears when the project is connected to GitHub. Through the API, it is `POST /api/projects/<project>/github-reporting/save`. Send `enabled=on` to turn it on, and no `enabled` to turn it off. |
| **Project Settings**, **Danger**, **Manage Services**, **Remove** | Deletes one service, database, logical database, or bucket of the project, after a confirmation. |
| **Project Settings**, **Danger**, **Delete Project**, then **Delete** and **Delete permanently** | Deletes the services, databases, logical databases, buckets, and cloud connections of the project. Then deletes the GitHub connection object, then the project. Revokes the API tokens of the project. To confirm, you enter the project name. |

Changes to a project take effect immediately. The draft and **Deploy changes** flow applies to services, databases, and buckets, not to the project itself. The top bar button **Deploy changes** deploys the pending changes of the resources in the selected environment. It does not touch the `spec` of the project.

Read-only dashboard users see a project without these items: its shared variables, GitHub connection, GitHub reporting switch, webhooks, and backup storage. Workspace tokens with the admin role can set the required tags through `POST /api/projects/<project>/compliance`, and read the report from `GET`. Project tokens cannot.

## Editing by hand

It is safe to create a project with `kubectl`, and to edit most of its fields. The operator reconciles changes within seconds. These rules apply:

- Create the project in `geass-system`. The operator looks up projects, clusters, and connections only in that namespace.
- Select names that make a valid namespace. `<name>-<environment>` becomes the namespace name.
- The operator writes `status` and the finalizer. Do not edit them.
- When other people use the dashboard, use `kubectl edit` or `kubectl patch`. Do not use a stale `kubectl apply` of an old file. The reason is that the dashboard also changes `spec`, and the operator writes `status` all the time.

:::danger
Do not give a project a name and environment that match a namespace that you want to keep. The operator reuses a namespace named `<project>-<environment>` when it already exists. The operator deletes that namespace when you remove the environment from the spec or delete the project. The operator does not check who created the namespace.
:::

:::caution
If you edit by hand, delete the objects of an environment yourself. When you remove an environment from `spec.environments`, the operator deletes its namespace and every workload in it. But the operator does not delete the `GeassApp`, `GeassDatabase`, and similar objects in `geass-system` that belong to the environment. The dashboard deletes those objects first.

If you delete only the `GeassProject` with `kubectl delete`, Geass removes its namespaces and the two Secrets `<project>-shared-secrets` and `<project>-webhooks`. The services and databases of the project stay in `geass-system`, and they report that the project does not exist.
:::

`spec.clusterRef` cannot change after the first successful reconcile. The dashboard blocks a region change for projects with databases, but the operator does not. Thus, if you move a project with databases to another region by hand, their data stays stranded on the old nodes.

## What happens under the hood

For each environment, the operator creates and maintains these objects. `<ns>` is `<project>-<environment>`.

| Object | Name | Notes |
| --- | --- | --- |
| Namespace | `<ns>` | Labels `geass.dev/managed-by=geass`, `geass.dev/project=<project>`, `geass.dev/environment=<environment>`, and `geass.dev/cluster=<cluster>`. The operator adds `geass.dev/project` to an existing namespace that does not have it. |
| NetworkPolicy | `geass-project-isolation` | Created when isolation is `Project`, deleted when it is `None`. It allows ingress from namespaces labeled with the project or an allowed project. It also allows ingress from `kube-system`, `geass-system`, `monitoring`, `cnpg-system`, `moco-system`, `mariadb-system`, and `keda`. |
| NetworkPolicy | `geass-block-cloud-metadata` | Always present. It allows pods to reach everything except the instance metadata range `169.254.0.0/16` of the cloud provider. When `spec.network.blockNodeAddresses` is `true`, it also excepts the node addresses of the cluster, and allows the Kubernetes API port on the control plane. |
| ResourceQuota | `geass-project-quota` | Present only when `spec.environmentQuota` or `spec.projectQuota` sets a cap. Holds `requests.cpu`, `requests.memory`, `requests.storage`, and `pods` for the caps that are set. The value is the lower of two caps: the cap of the environment, and what the other environments leave of the cap of the project. |
| LimitRange | `geass-project-defaults` | Present only when the quota caps CPU or memory. It gives a default request to a container that asks for none. Thus, the quota can count the container. |
| Annotation on the namespace | `geass.dev/required-tags` | The required tags, comma-separated and sorted. The admission policy `geass-required-node-tags` reads it. Removed when the project requires no tags. |
| ConfigMap | `geass-shared-variables` | Plain shared variables of the environment, when there are any. |
| Secret | `geass-shared-secrets` | Secret shared variables of the environment, when there are any. |

In `geass-system`, the project also uses these objects:

| Object | Name | Notes |
| --- | --- | --- |
| Secret | `<project>-shared-secrets` | Secret values of shared variables, keyed `<environment>__<NAME>`. The dashboard creates this Secret. |
| Secret | `<project>-webhooks` | Signing secrets of the webhooks, keyed by webhook ID. |
| GeassGitHubConnection | `<project>-github` | Created when you connect GitHub. |
| Secret | `<project>-github-token` | The GitHub installation token of the connection. If you delete the project, Geass does not delete this Secret. |

The operator puts the project label on the namespace, so that the isolation policy can select the namespace. If you edit or delete a policy that the operator manages, the operator puts the policy back.

Inspect a project with these commands:

```bash
kubectl get geassproject storefront -n geass-system -o yaml
kubectl get namespaces -l geass.dev/project=storefront --show-labels
kubectl get networkpolicy -n storefront-production
kubectl get configmap geass-shared-variables -n storefront-production -o yaml
kubectl get secret storefront-webhooks storefront-shared-secrets -n geass-system
```

## Troubleshooting

**The project stays `False` with `Referenced GeassCluster is not ready`.** The `GeassCluster` named in `spec.clusterRef` has not reached `Ready`. Run `kubectl get geassclusters -n geass-system`, and read the conditions of the cluster. The project becomes ready automatically after the cluster is ready. See the [GeassCluster](/docs/reference/resources/geass-cluster/) reference.

**The project is `ProvisionFailed` and the message mentions repeated errors.** The cause was a counted error, such as a cluster that does not exist. Fix the cause. Then change the spec so that `metadata.generation` increases. A correction to `spec.clusterRef` does this. The operator log has the underlying message: `kubectl logs -n geass-system deployment/geass-controller-manager -c manager`. If your operator Deployment has a different name, list it with `kubectl get deploy -n geass-system`.

**A new service says `project "..." is not ready`, or `project "..." has no <environment> environment`.** Services deploy only into a project that meets two conditions. Its `Ready` condition is `True`, and its `spec.environments` lists the environment of the service. Check the project with `kubectl get geassproject <name> -n geass-system`. A service that waits about 2 minutes for a project that is not ready can end up `ProvisionFailed`. After the project is ready, use **Retry** on the service.

**Saving required tags is refused.** The message says which check failed:

- No node carries the tag in the region of the project.
- A node carries the tag, but the node is not reserved for it.
- A database keeps its data on nodes without the tag.

See [Compliance node tags](/docs/guides/compliance-tags/#troubleshooting).

**A shared variable does not reach a service.** Check these items in order:

1. The `environment` of the variable matches the environment of the service.
2. If the service sets `spec.sharedVariableRefs`, the variable name is in the list.
3. The service was deployed after the variable was added. Geass reads variables when the pods start. Thus, click **Deploy changes**.
4. For a secret variable, the Secret and key in `secretRef` exist in `geass-system`. If one is missing, the project reconcile stops. The operator log shows `shared variable NAME secret reference` or `shared variable NAME secret key "KEY" was not found`. The project status does not show this error. The project stays `Ready`.

**Webhook deliveries fail.** Open **Project Settings**, **Webhooks**, **Recent deliveries**, or read `status.webhookDeliveries`. Common errors:

- `webhook URL cannot point at a private network address unless the operator sets GEASS_WEBHOOKS_ALLOW_PRIVATE_NETWORKS=true`. The receiver is on a private address. Set the variable on the operator, or expose the receiver on a public address.
- `receiver answered HTTP 4xx`. Your receiver rejected the request. Check signature verification, and use **Send test** to try again.
- `refusing to connect to <ip>`. The hostname resolved to an address that Geass does not call.

**A pull request does not get an environment.** Check these items:

- `spec.prEnvironments.enabled` is true.
- The project is linked to GitHub.
- The GitHub App is subscribed to the **Pull request** event.
- A service in the base environment builds this repository and the target branch of the pull request.
- The pull request is not from a fork.

A response note such as `the limit of 5 pull request environments is reached` means that the project reached `maxOpen`. See [Pull request environments](/docs/guides/pull-request-environments/).

**An environment was not removed when I deleted it.** If you removed the environment from `spec.environments` by hand, delete its services, databases, and buckets yourself. The operator deletes the namespace, but it does not delete the `Geass*` objects for that environment in `geass-system`.

## Related

- [Projects and environments](/docs/guides/projects-and-environments/)
- [GeassApp](/docs/reference/resources/geass-app/), the services that run in a project
- [GeassGitHubConnection](/docs/reference/resources/geass-github-connection/)
- [Network isolation](/docs/guides/network-isolation/)
- [Compliance node tags](/docs/guides/compliance-tags/)
- [Backups and restore](/docs/guides/backups-and-restore/)
- [Pull request environments](/docs/guides/pull-request-environments/)
- [Resources overview](/docs/reference/resources/overview/)
