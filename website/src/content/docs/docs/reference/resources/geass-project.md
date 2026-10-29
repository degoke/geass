---
title: "GeassProject"
description: "Reference for GeassProject: environments, variables, webhooks, pull request environments, GitHub reporting, backups, network isolation, region and node tags."
sidebar:
  order: 1
---

A `GeassProject` is the top of the hierarchy in Geass. It groups the services, databases and buckets of one product, and it owns the environments (`production`, `staging` and so on) those resources run in. For every environment the operator creates one Kubernetes namespace and keeps it configured.

The dashboard creates and edits projects for you: **New project** on the **Projects** page creates one, and **Project Settings** changes it. You can also create one with `kubectl`, and the operator treats it the same way. Every Geass resource, including projects, lives in the `geass-system` namespace.

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

The dashboard names the objects `proj-` followed by eight hexadecimal characters, for example `proj-32b0a870`. This name is the project ID. It appears in namespace names, in labels and in the `project` field of every resource that belongs to the project. The name people see in the dashboard is `spec.displayName`.

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

Apply it and check that it becomes ready:

```bash
kubectl apply -f project.yaml
kubectl get geassproject storefront -n geass-system
```

A project with every optional block filled in looks like this:

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

The names `storefront-shared-secrets`, `storefront-github`, `aws-main` and `proj-32b0a870` must refer to objects that exist. The sections below explain each reference.

## Spec fields

Required fields are marked. Every other field is optional.

### Identity and placement

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.displayName` | string | none | none | The name the dashboard shows. The dashboard stores it as a lowercase slug such as `morning-beach` and generates one when it creates a project. The operator does not read it. |
| `spec.clusterRef.name` | string | none | Required | The `GeassCluster` that owns the project, looked up in `geass-system`. The installer creates a cluster named `default`. You cannot change it once the project has been reconciled. |
| `spec.org` | string | empty | Pattern `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, 63 characters at most | The organization that owns the project: the name of a [GeassOrganization](/docs/reference/resources/geass-organization/). Empty means the project belongs to none, which only the platform's administrators can see once [organizations are on](/docs/guides/organizations-and-workspaces/). A project made by an organization's own people has it from the start. The platform assigns the others with `POST /api/projects/{project}/assign`. |
| `spec.workspace` | string | empty | Pattern `^ws-[a-f0-9]{6}$` | The workspace of the organization the project is in. Empty means the project belongs to the organization as a whole, which only its administrators can see. |
| `spec.region` | string | empty | Pattern `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, 40 characters at most | Restricts the project's workloads to nodes in this region. Empty means the default region of the cluster (`spec.defaultRegion` on the [GeassCluster](/docs/reference/resources/geass-cluster/)), or any node when the cluster has none. |
| `spec.requiredTags` | list of strings | empty | At most 8 unique items. Each matches `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, 40 characters at most | Node tags that everything the project runs must run on, such as `hipaa`. A node needs all of them. See [Required node tags](#required-node-tags). |
| `spec.environments` | list of strings | none | Required, 1 to 100 items. Each item matches `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, 63 characters at most | The environments of the project. Each one becomes a namespace named `<project>-<environment>`. |

Two more rules apply when the operator reconciles the list:

- Duplicates are rejected. The project reports `Project environments must be unique`.
- `<project>-<environment>` must be a valid DNS label of 63 characters or fewer. If it is not, the project reports an error such as `project/environment namespace "..." is invalid`.

The region is chosen when the project is created. The dashboard refuses to change it while the project has a database that runs in the cluster, because the data of a database lives on the nodes of its region. The operator itself does not enforce this rule, so it is not enforced for edits made with `kubectl`. See [Scaling and placement](/docs/guides/scaling-and-placement/) for how regions and node tags work.

### Required node tags

`spec.requiredTags` keeps a project's workloads on the nodes that carry its tags. Unlike the tags a service chooses under **Run on**, these apply to everything the project runs: services, databases, buckets, builds, function schedules, and the jobs that back up and restore its data. Geass puts a node selector and a toleration for each tag on those pods, and puts the same tags on the project's storage class, so the copies of a volume stay on those nodes too.

The tags should be compliance tags on the nodes that carry them (`spec.placement.tags[].compliance` on a [GeassNode](/docs/reference/resources/geass-node/)), so that no other project's work lands there. The dashboard and the API check this before they save:

- every tag exists on a node in the project's region;
- every node in the region that carries a tag is reserved for it;
- every in-cluster database of the project that is already provisioned runs on nodes with the tags you are adding.

A change made with `kubectl` skips these checks. A project that requires tags also gets the annotation `geass.dev/required-tags` on its namespaces, the logs of those namespaces are not shipped to the shared log store, and a database on a shared server, or a bucket on the object storage server, is refused unless that server runs on nodes with all the tags. See [Compliance node tags](/docs/guides/compliance-tags/) for the whole picture and its limits.

### Shared variables

`spec.sharedVariables` holds values that services in one environment can use. See [Environment variables](/docs/guides/environment-variables/) for the guide.

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.sharedVariables[].name` | string | none | Required. Pattern `^[A-Z_][A-Z0-9_]*$` | The environment variable name. |
| `spec.sharedVariables[].environment` | string | none | Required. 1 to 63 characters, pattern `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$` | The environment the variable belongs to. A variable exists in one environment only. |
| `spec.sharedVariables[].value` | string | empty | none | A plain value. The operator writes it to a ConfigMap. |
| `spec.sharedVariables[].secretRef.name` | string | none | none | A Secret in `geass-system` that holds the value. When `secretRef` is set it wins over `value`. |
| `spec.sharedVariables[].secretRef.key` | string | none | Required inside `secretRef` | The key in that Secret. |
| `spec.sharedVariables[].secretRef.optional` | boolean | none | none | A standard `SecretKeySelector` field. The operator does not read it. |

For each environment the operator copies the variables of that environment into the namespace:

- Plain values go into the ConfigMap `geass-shared-variables`.
- Secret values go into the Secret `geass-shared-secrets`, under the variable name as the key.

A ConfigMap or Secret that would be empty is deleted. A `secretRef` that points at a missing Secret or key stops the reconcile with an error in the operator log, for example `shared variable PAYMENTS_API_KEY secret key "production__PAYMENTS_API_KEY" was not found`. See [Troubleshooting](#troubleshooting).

The dashboard stores secret values in the Secret `<project>-shared-secrets` in `geass-system`, under keys named `<environment>__<NAME>`, and points `secretRef` at them. A service picks the variables it receives with `spec.sharedVariableRefs` on the [GeassApp](/docs/reference/resources/geass-app/#variables-and-files).

:::note
The value field in the dashboard shows the placeholder `VALUE or ${{REF}}`. Nothing in Geass resolves `${{REF}}` references. The text is stored and injected as it is.
:::

### GitHub connection

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.githubConnectionRef.name` | string | none | none | The [GeassGitHubConnection](/docs/reference/resources/geass-github-connection/) that gives this project access to GitHub. The dashboard sets it to `<project>-github` when you click **Connect to GitHub**. |

The CRD describes this field as a reference to a Secret. The code resolves it as a `GeassGitHubConnection` in `geass-system`. The operator's project controller does not read the field. The dashboard reads it to list repositories, to validate Git services and to match incoming GitHub webhooks to the project.

### GitHub reporting

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.githubReporting` | boolean | `true` | Optional. The CRD defaults it to `true`, and unset is read as on. | Whether Geass reports this project's repository services back to GitHub: a commit status `geass/build/<service>` for each build, a GitHub Deployment for each deploy, and one comment on each pull request that has an environment. Set it to `false` to stop all three. |

It does nothing until the project is connected to GitHub, and the three reports need the GitHub App's **Commit statuses**,
**Deployments** and **Pull requests** permissions at read and write. An app registered before Geass reported to GitHub does
not have them: the reports are refused, and the `GitHubReporting` condition of the project's
[GeassGitHubConnection](/docs/reference/resources/geass-github-connection/#conditions) says so. Builds, deploys and pull request
environments are not affected, whatever the value. See [Reporting back to GitHub](/docs/guides/github-and-builds/#reporting-back-to-github).

### Webhooks

`spec.webhooks` lists up to 20 outbound webhooks. Geass sends each one a signed `POST` when a service deploy starts, succeeds or fails.

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.webhooks[].id` | string | none | Required. Pattern `^[a-z0-9]{8}$` | Identifies the webhook in the dashboard, in delivery records and as the key of its signing secret. The dashboard generates eight hexadecimal characters. |
| `spec.webhooks[].url` | string | none | Required. 8 to 2048 characters | The receiver URL. |
| `spec.webhooks[].events` | list of strings | empty | Each item is `deployment.started`, `deployment.succeeded` or `deployment.failed` | The events to send. Empty means every event. The dashboard stores "all three events" as an empty list so that future event types also reach the webhook. |
| `spec.webhooks[].disabled` | boolean | `false` | none | Pauses deliveries without deleting the webhook. |

The signing secret of a webhook is never part of the spec. It is stored in the Secret `<project>-webhooks` in `geass-system`, under the webhook ID, and has the form `whsec_` followed by 48 hexadecimal characters. A webhook with no entry in that Secret is delivered without a signature. The dashboard shows the secret once, when you create the webhook or rotate its secret.

How a delivery works:

| Topic | Behavior |
| --- | --- |
| When events fire | `deployment.started` is sent when you deploy a service from the dashboard or the REST API. `deployment.succeeded` is sent when the operator finds the service ready. `deployment.failed` is sent when the service reaches `ProvisionFailed`. Success and failure events are only sent for deploys that were announced as started. |
| Request | `POST` with a JSON body and these headers: `Content-Type: application/json`, `User-Agent: Geass-Webhooks/1`, `X-Geass-Event`, `X-Geass-Delivery`, `X-Geass-Timestamp` (Unix seconds) and `X-Geass-Signature`. |
| Signature | `t=<timestamp>,v1=<hex>`, where `v1` is the HMAC-SHA256 of `<timestamp>.<raw body>` with the signing secret. Reject requests whose timestamp is more than a few minutes old. |
| Retries | An event is attempted up to 3 times, waiting 1 second and then 5 seconds. Geass retries on network errors, HTTP 5xx, 429 and 408. Any other status of 300 or more is recorded as an error without a retry. The **Send test** action makes one attempt. |
| Limits | 10 seconds per request. Redirects are not followed. At most 8 deliveries run at the same time. |
| Network rules | `localhost`, loopback and link-local addresses are always refused. Private network addresses are refused unless the operator runs with `GEASS_WEBHOOKS_ALLOW_PRIVATE_NETWORKS=true`. URLs with a user name or password or a fragment are refused. |

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

`service.kind` is `service` or `function`. `deployment.commit` appears for Git services that pin a commit. `message` appears on `deployment.failed` and on test events (`type` is `test` there).

### Pull request environments

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.prEnvironments.enabled` | boolean | none | Required inside `prEnvironments` | Turns pull request environments on. |
| `spec.prEnvironments.baseEnvironment` | string | first item of `spec.environments` | Pattern `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, 63 characters at most | The environment that is copied for each pull request. |
| `spec.prEnvironments.maxOpen` | integer | `5` | 1 to 25 | The most pull request environments that can exist at once. The default applies when the `prEnvironments` object exists. |

When a pull request opens, reopens, becomes ready for review or receives a push, Geass adds an environment named `pr-<number>` to `spec.environments`. If two repositories in the project have a pull request with the same number, the second one is named `pr-<number>-<repository>`. The environment gets copies of the services and in-cluster databases of the base environment, and copies of its shared variables. Buckets, logical databases and external databases are not copied. When the pull request closes, Geass removes the environment and everything in it.

Geass only does this when GitHub reports the event through the platform GitHub App, when the repository belongs to a Git service of the base environment that builds the branch the pull request targets, and when the pull request comes from the same repository (pull requests from forks are ignored). The open ones are listed in `status.pullRequests`. See [Pull request environments](/docs/guides/pull-request-environments/) for the full guide.

### Backup storage

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.backupStorage.connectionRef.name` | string | none | Required inside `backupStorage` | The AWS [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/) that owns the bucket. |
| `spec.backupStorage.bucket` | string | none | Required. 3 to 63 characters, pattern `^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$` | The S3 bucket that holds the backups of the project's databases. Geass creates it when it does not exist. |
| `spec.backupStorage.region` | string | the region of the connection | none | The AWS region of the bucket. |

Without this block, databases that turn on backups report that they need storage. The dashboard fills the block in when you choose an AWS connection under **Backups**, and it generates the bucket name. The database controller creates the bucket and a user in your AWS account that can only reach that bucket. See [Backups and restore](/docs/guides/backups-and-restore/).

### Network isolation

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.network.isolation` | string | `Project` | `Project` or `None` | `Project` lets only the project itself, the platform and visitors who arrive through your domains reach the project's services. `None` leaves them open to every service in the cluster. |
| `spec.network.allowFromProjects` | list of strings | empty | At most 20 unique items. Each matches `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, 63 characters at most | Other projects, by project ID, whose services may reach this project's services. It has no effect when isolation is `None`. |
| `spec.network.blockNodeAddresses` | boolean | none: `false` when absent | none | `true` stops the project's services from opening connections to the cluster's machines: every node address and the two lowest addresses of every node's pod range. The Kubernetes API stays open on the control plane. It is independent of `isolation`, and Geass keeps the rule in step with the nodes as they join and leave. Projects created by the dashboard or the API set it to `true`, and one that existed before it does not have it. |

A project with no `spec.network` is isolated and not blocked from the machines. The policies apply to every environment of the project and change within seconds of a save. See [Network isolation](/docs/guides/network-isolation/). A project of an organization may also be held to the organization's [outbound limits](/docs/guides/outbound-limits/), which are not a setting of the project.

### Quota

Without `spec.projectQuota` and `spec.environmentQuota` nothing is capped. `spec.projectQuota` caps the project as a whole, added up over its environments. `spec.environmentQuota` caps **each** environment of the project, pull request environments included. An environment is held to the lower of the two.

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.projectQuota.cpu` | quantity | none | none | The most CPU the project's pods may ask for in total, such as `8`. |
| `spec.projectQuota.memory` | quantity | none | none | The most memory they may ask for in total, such as `16Gi`. |
| `spec.projectQuota.storage` | quantity | none | none | The most disk their volumes may ask for in total, such as `200Gi`. |
| `spec.projectQuota.pods` | integer | none | At least 1 | The most pods the project may have running or waiting at once. |

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.environmentQuota.cpu` | quantity | none | none | The most CPU one environment's pods may ask for in total, such as `4` or `500m`. |
| `spec.environmentQuota.memory` | quantity | none | none | The most memory they may ask for in total, such as `8Gi`. |
| `spec.environmentQuota.storage` | quantity | none | none | The most disk their volumes may ask for in total, such as `50Gi`. |
| `spec.environmentQuota.pods` | integer | none | At least 1 | The most pods one environment may have running or waiting at once. |

A field left out is not capped. The caps count requests, not use. Each environment may ask for what the others leave of the project's cap, so the environments' own caps are rewritten as usage changes. See [Project limits](/docs/guides/project-limits/).

## Status

| Field | Type | Meaning |
| --- | --- | --- |
| `status.clusterRef` | string | The cluster the operator observed. It is set after the first successful reconcile and prevents a project from moving to another cluster. |
| `status.environments[].name` | string | The environment name. At most 100 entries. |
| `status.environments[].namespace` | string | The namespace of that environment. |
| `status.conditions` | list | The `Ready` condition, `TenantLimitsApplied` for a project of an organization with noisy-neighbour limits, and `EgressLimitsApplied` for one with outbound limits. See below. |
| `status.reconcileRetry.reconcileFailures` | integer | Consecutive errors counted toward the retry limit. |
| `status.reconcileRetry.reconcileFailureStarted` | time | When the current streak of errors began. |
| `status.reconcileRetry.reconcileFailureGeneration` | integer | The `metadata.generation` at which the project was marked `ProvisionFailed`. |
| `status.webhookDeliveries` | list, at most 20 | The newest deliveries first. Each entry has `webhookId`, `event`, `deliveryId`, `statusCode`, `error`, `attempts`, `durationMillis` and `time`. Written by the webhook dispatcher. |
| `status.pullRequests` | list, at most 50 | The open pull request environments. Each entry has `number`, `repository`, `branch`, `environment`, `url` and `openedAt`. Written by the dashboard when it handles GitHub events. |

### Conditions

The operator sets the condition type `Ready`, and `TenantLimitsApplied` and `EgressLimitsApplied` (below the table).

| Status | Reason | Message | Meaning |
| --- | --- | --- | --- |
| `True` | `ProjectReady` | `Project environments are ready` | Every environment in `spec.environments` has a namespace with its policies and shared variables. |
| `False` | `ReconcileError` | `Referenced GeassCluster is not available` | The cluster named in `spec.clusterRef` does not exist in `geass-system`. |
| `False` | `ReconcileError` | `Referenced GeassCluster is not ready` | The cluster exists but its `Ready` condition is not `True`. The operator checks again every 30 seconds and does not give up. |
| `False` | `ReconcileError` | `Changing a project's clusterRef is not supported` | `spec.clusterRef` differs from `status.clusterRef`. |
| `False` | `ReconcileError` | `Project environments must be unique` | `spec.environments` contains a duplicate. |
| `False` | `ReconcileError` | `invalid project "..."`, `invalid environment "..."` or `project/environment namespace "..." is invalid` | A name is not a valid DNS label, or the combined namespace name is too long. |
| `False` | `ProvisionFailed` | `Provisioning failed after repeated errors. Update settings or platform configuration, then save or redeploy to try again.` | The same kind of error repeated 5 times or for 3 minutes. The operator stops retrying until you change the spec. |

A project of an organization that has [noisy-neighbour limits](/docs/guides/tenant-limits/) also carries `TenantLimitsApplied`: `True` (`Applied`) when they are in every environment, or `False` with `InvalidLimits` (they cannot be held as written, and each environment keeps what it had), `NotApplied` (an environment's could not be written; the operator tries again) or `BandwidthNotApplied` (the cluster has no mutating admission policies). It does not change `Ready`. A project of an organization without limits has no such condition.

A project of an organization that has [outbound limits](/docs/guides/outbound-limits/) also carries `EgressLimitsApplied`: `True` (`Applied`) when they are in the network policy of every environment and the cluster enforces network policies (its `NetworkPolicyEnforced` condition), `False` with `NotEnforced` when the cluster does not, `Unknown` with `EnforcementUnknown` before the cluster has been checked or when the check could not tell, and `False` with `InvalidLimits` when they cannot be applied as written (each environment keeps the policy it had). It does not change `Ready`. A project without such limits has no such condition.

The operator retries a failing project every 30 seconds. Errors that the operator treats as temporary (a cluster that is not ready, a cluster change, a duplicate environment) are retried without a limit. Other errors count: after 5 in a row, or 3 minutes, the project becomes `ProvisionFailed` and the controller pauses until `metadata.generation` changes, which means until you edit `spec`. The dashboard has no **Retry** button for projects. Any change to `spec` raises `metadata.generation` and resumes reconciliation.

## How the dashboard maps to this resource

| Dashboard | What it does to the project |
| --- | --- |
| **Projects** page, **New project** | Creates a project with one environment, `production`. The ID is generated, `spec.displayName` is a random slug such as `morning-beach`, and `spec.clusterRef` is the default cluster. The region selector next to the button sets `spec.region`. It only appears when the cluster has regions to choose from. |
| **Project Settings**, **General**, **Update** | Saves `spec.displayName` (normalized to a lowercase slug) and `spec.region`. The dashboard rejects a region change when the project has an in-cluster database. |
| **Project Settings**, **Environments**, **New Environment** | Appends an environment to `spec.environments`. |
| **Project Settings**, **Environments**, **Delete**, then **Delete environment** | Removes the environment from the list, removes its shared variables and deletes every service, database, logical database and bucket in it. The operator then deletes the namespace. A project must keep at least one environment. |
| **Project Settings**, **Environments**, **PR Environments** | Writes `spec.prEnvironments`. The fields are **Create an environment for each pull request**, **Copy this environment** and **Open at once**. **Close** on an open pull request removes its environment. |
| **Project Settings**, **Shared Variables** | Adds or removes entries in `spec.sharedVariables`. Turning on **Secret** stores the value in the Secret `<project>-shared-secrets` and sets `secretRef`. |
| **Project Settings**, **Webhooks** | Edits `spec.webhooks`. The actions are **New webhook**, **Send test**, **Edit**, **Pause** or **Enable**, **Rotate signing secret** and **Delete**. |
| **Project Settings**, **Backups** | Writes `spec.backupStorage` when you choose an AWS connection and click **Use this connection**. **Remove** clears it. |
| **Project Settings**, **Network** | Writes `spec.network`. The switches are **Keep this project's services private** and **Keep this project's services away from the cluster's machines**, and the list is **Projects allowed to reach this one**. |
| **Project Settings**, **Limits** | Writes `spec.projectQuota` (**Whole project**) and `spec.environmentQuota` (**Each environment**). The fields in each are **CPU**, **Memory**, **Storage** and **Pods**. Emptying a field removes that limit. |
| **Project Settings**, **Compliance** | Writes `spec.requiredTags`. The list is the tags that nodes in the project's region carry. **Save** applies the checks above. Only an admin can save. |
| **Project Settings**, **GitHub**, **Connect to GitHub** or **Disconnect** | Sets or clears `spec.githubConnectionRef`. Disconnecting does not delete the connection object or its Secret. |
| **Project Settings**, **GitHub**, **Report to GitHub** | Writes `spec.githubReporting`. The card appears when the project is connected to GitHub. Through the API it is `POST /api/projects/<project>/github-reporting/save`, with `enabled=on` to turn it on and no `enabled` to turn it off. |
| **Project Settings**, **Danger**, **Manage Services**, **Remove** | Deletes one service, database, logical database or bucket of the project, after a confirmation. |
| **Project Settings**, **Danger**, **Delete Project**, then **Delete** and **Delete permanently** | Deletes the project's services, databases, logical databases, buckets and cloud connections, the GitHub connection object, then the project, and revokes the project's API tokens. You type the project name to confirm. |

Changes to a project take effect at once. The draft and **Deploy changes** flow applies to services, databases and buckets, not to the project itself. The top bar button **Deploy changes** deploys the pending changes of the resources in the selected environment, and it does not touch `spec` of the project.

Read-only dashboard users see a project without its shared variables, GitHub connection, GitHub reporting switch, webhooks and backup storage. Workspace tokens with the admin role can set the required tags through `POST /api/projects/<project>/compliance`, and read the report from `GET`. Project tokens cannot.

## Editing by hand

It is safe to create a project with `kubectl` and to edit most of its fields. The operator reconciles changes within seconds. These rules matter:

- Create it in `geass-system`. The operator looks up projects, clusters and connections only in that namespace.
- Choose names that make a valid namespace. `<name>-<environment>` becomes the namespace name.
- The operator writes `status` and the finalizer. Do not edit them.
- The dashboard changes `spec` too, and the operator writes `status` all the time, so use `kubectl edit` or `kubectl patch` and not a stale `kubectl apply` of an old file when others use the dashboard.

:::danger
The operator reuses a namespace named `<project>-<environment>` when it already exists, and it deletes that namespace when you remove the environment from the spec or delete the project. It does not check who created it. Do not give a project a name and environment that match a namespace you want to keep.
:::

:::caution
Removing an environment from `spec.environments` deletes its namespace and every workload in it. It does not delete the `GeassApp`, `GeassDatabase` and similar objects in `geass-system` that belong to the environment. The dashboard deletes those first. If you edit by hand, delete them yourself.

Deleting only the `GeassProject` with `kubectl delete` removes its namespaces and the two Secrets `<project>-shared-secrets` and `<project>-webhooks`. The services and databases of the project stay in `geass-system` and report that the project does not exist.
:::

`spec.clusterRef` cannot change after the first successful reconcile. The dashboard blocks a region change for projects with databases, but the operator does not, so moving a project with databases to another region by hand strands their data on the old nodes.

## What happens under the hood

For each environment the operator creates and maintains these objects. `<ns>` is `<project>-<environment>`.

| Object | Name | Notes |
| --- | --- | --- |
| Namespace | `<ns>` | Labels `geass.dev/managed-by=geass`, `geass.dev/project=<project>`, `geass.dev/environment=<environment>` and `geass.dev/cluster=<cluster>`. The operator adds `geass.dev/project` to an existing namespace that lacks it. |
| NetworkPolicy | `geass-project-isolation` | Created when isolation is `Project`, deleted when it is `None`. It allows ingress from namespaces labeled with the project or an allowed project, and from `kube-system`, `geass-system`, `monitoring`, `cnpg-system`, `moco-system`, `mariadb-system` and `keda`. |
| NetworkPolicy | `geass-block-cloud-metadata` | Always present. It lets pods reach everything except the cloud provider's instance metadata range `169.254.0.0/16`. When `spec.network.blockNodeAddresses` is `true` it also excepts the cluster's node addresses, and allows the Kubernetes API port on the control plane. |
| ResourceQuota | `geass-project-quota` | Present only when `spec.environmentQuota` or `spec.projectQuota` sets a cap. Holds `requests.cpu`, `requests.memory`, `requests.storage` and `pods` for the caps that are set: the environment's own, or what the others leave of the project's, whichever is lower. |
| LimitRange | `geass-project-defaults` | Present only when the quota caps CPU or memory. It gives a container that asks for none a default request, so the quota can count it. |
| Annotation on the namespace | `geass.dev/required-tags` | The required tags, comma-separated and sorted. The admission policy `geass-required-node-tags` reads it. Removed when the project requires nothing. |
| ConfigMap | `geass-shared-variables` | Plain shared variables of the environment, when there are any. |
| Secret | `geass-shared-secrets` | Secret shared variables of the environment, when there are any. |

In `geass-system` the project also uses:

| Object | Name | Notes |
| --- | --- | --- |
| Secret | `<project>-shared-secrets` | Secret values of shared variables, keyed `<environment>__<NAME>`. Created by the dashboard. |
| Secret | `<project>-webhooks` | Signing secrets of the webhooks, keyed by webhook ID. |
| GeassGitHubConnection | `<project>-github` | Created when you connect GitHub. |
| Secret | `<project>-github-token` | The GitHub installation token of the connection. Deleting the project does not delete this Secret. |

The operator puts the project label on the namespace so that the isolation policy can select it, and it puts any policy it manages back when you edit or delete it by hand.

Inspect a project with these commands:

```bash
kubectl get geassproject storefront -n geass-system -o yaml
kubectl get namespaces -l geass.dev/project=storefront --show-labels
kubectl get networkpolicy -n storefront-production
kubectl get configmap geass-shared-variables -n storefront-production -o yaml
kubectl get secret storefront-webhooks storefront-shared-secrets -n geass-system
```

## Troubleshooting

**The project stays `False` with `Referenced GeassCluster is not ready`.** The `GeassCluster` named in `spec.clusterRef` has not reached `Ready`. Run `kubectl get geassclusters -n geass-system` and read the cluster's conditions. The project becomes ready on its own once the cluster is. See the [GeassCluster](/docs/reference/resources/geass-cluster/) reference.

**The project is `ProvisionFailed` and the message mentions repeated errors.** The cause was a counted error, such as a cluster that does not exist. Fix the cause, then change the spec so that `metadata.generation` increases. Correcting `spec.clusterRef` does this. The operator log has the underlying message: `kubectl logs -n geass-system deployment/geass-controller-manager -c manager`. If your operator Deployment has a different name, list it with `kubectl get deploy -n geass-system`.

**A new service says `project "..." is not ready`, or `project "..." has no <environment> environment`.** Services deploy only into a project whose `Ready` condition is `True` and whose `spec.environments` lists the service's environment. Check the project with `kubectl get geassproject <name> -n geass-system`. A service that waits about 2 minutes for a project that is not ready can end up `ProvisionFailed`. Use **Retry** on the service once the project is ready.

**Saving required tags is refused.** The message says which check failed: no node carries the tag in the project's region, a node carries it without being reserved for it, or a database keeps its data on nodes without it. See [Compliance node tags](/docs/guides/compliance-tags/#troubleshooting).

**A shared variable does not reach a service.** Check these in order:

1. The variable's `environment` matches the service's environment.
2. If the service sets `spec.sharedVariableRefs`, the variable name is in the list.
3. The service was deployed after the variable was added. Variables are read when the pods start, so click **Deploy changes**.
4. For a secret variable, the Secret and key in `secretRef` exist in `geass-system`. A missing one aborts the project reconcile with `shared variable NAME secret reference` or `shared variable NAME secret key "KEY" was not found` in the operator log. The project status does not show this error and stays `Ready`.

**Webhook deliveries fail.** Open **Project Settings**, **Webhooks**, **Recent deliveries**, or read `status.webhookDeliveries`. Common errors:

- `webhook URL cannot point at a private network address unless the operator sets GEASS_WEBHOOKS_ALLOW_PRIVATE_NETWORKS=true`. The receiver is on a private address. Set the variable on the operator, or expose the receiver on a public address.
- `receiver answered HTTP 4xx`. Your receiver rejected the request. Check signature verification, and use **Send test** to try again.
- `refusing to connect to <ip>`. The hostname resolved to an address that Geass does not call.

**A pull request does not get an environment.** Check that `spec.prEnvironments.enabled` is true, that the project is linked to GitHub, that the GitHub App is subscribed to the **Pull request** event, that a service in the base environment builds this repository and the pull request's target branch, and that the pull request is not from a fork. A response note such as `the limit of 5 pull request environments is reached` means `maxOpen` was hit. See [Pull request environments](/docs/guides/pull-request-environments/).

**An environment was not removed when I deleted it.** If you removed it from `spec.environments` by hand, delete its services, databases and buckets yourself. The namespace is deleted by the operator, but the `Geass*` objects for that environment in `geass-system` are not.

## Related

- [Projects and environments](/docs/guides/projects-and-environments/)
- [GeassApp](/docs/reference/resources/geass-app/), the services that run in a project
- [GeassGitHubConnection](/docs/reference/resources/geass-github-connection/)
- [Network isolation](/docs/guides/network-isolation/)
- [Compliance node tags](/docs/guides/compliance-tags/)
- [Backups and restore](/docs/guides/backups-and-restore/)
- [Pull request environments](/docs/guides/pull-request-environments/)
- [Resources overview](/docs/reference/resources/overview/)
