---
title: "Resources overview"
description: "How the Geass dashboard relates to its Kubernetes resources, which kinds exist, how to read them with kubectl, and which kinds you can edit by hand."
sidebar:
  order: 0
---

Geass keeps everything that it manages as Kubernetes custom resources. The dashboard is the usual way to create and
change these resources. Each page in this section documents one kind: its fields, its status, and how the dashboard
maps to it. You do not need this section to use Geass. You need it when you automate, when you debug with `kubectl`, or
when you want to know what a click really does.

## The dashboard and the resources

The dashboard is the primary interface, and the resources are the model under it. There is no separate database. When
you click **Deploy changes**, these steps occur:

1. The dashboard's API reads a resource through the Kubernetes API.
2. The API changes the resource's `spec` and annotations, and writes the resource back.
3. The operator watches the resources and makes the cluster match them.
4. The operator reports what it did in `status`. The dashboard shows that status.

```text
dashboard or API  ->  Geass resource (spec)  ->  operator reconciles  ->  workloads in your namespaces
                                              <-  status and conditions
```

All of the resources are in the `geass-system` namespace, in the API group `geass.geass.dev`, version `v1alpha1`. All
kinds are namespaced. The resources create workloads, for example pods, Services, Ingresses, and Secrets. These
workloads are in one namespace for each project environment, with the name `<project id>-<environment>`.

:::caution
Pin your manifests to a Geass release, and check them after an upgrade. The API version is `v1alpha1`. Fields can
change between releases, and Geass has no conversion between versions.
:::

### Names, display names and IDs

The name of a project is an ID, for example `proj-1a2b3c4d`. Its readable name is `spec.displayName`. Other resources
get a generated name, for example `morning-beach`. The label that the dashboard shows can be different from the
resource name. Geass keeps that label in the annotation `geass.dev/display-name`. Always use the resource name, the one
in `metadata.name`, in `kubectl` and in the API.

### Drafts, pending changes and deploys

Most resources use a draft-and-deploy flow. With this flow, a change does not restart anything until you tell Geass to
deploy. Geass records the state on the resource:

| Where | What |
| --- | --- |
| `spec.deploy.enabled` | `false` while the resource is a draft. A deploy sets it to `true`. Apps, databases, and buckets have it |
| Annotation `geass.dev/pending-changes` | Comma-separated kinds of waiting changes: `created`, `settings`, `variables` |
| Annotation `geass.dev/pending-updates` | A counter of saved changes that are not deployed yet |
| Annotation `geass.dev/last-deployed` | The spec as last deployed, as JSON |
| Annotation `geass.dev/deploy-workflow` | `true` on resources that the dashboard created. It turns the gating on |
| Annotation `geass.dev/deploy-revision` | Changes on every deploy, so that pods restart and get new variables |
| Labels `geass.dev/project`, `geass.dev/environment`, and `geass.dev/app` or `geass.dev/name` | Connect records and workloads to what they belong to. `geass.dev/app` is on records that the dashboard creates (deploy records, console sessions) and on every build. `geass.dev/name` is on objects that the operator creates |
| Annotation `geass.dev/required-tags` on a project environment's namespace | The node tags that the project requires, sorted and comma-separated. The admission policy `geass-required-node-tags` reads it. Geass writes it from `spec.requiredTags` on the project |

While a deployed resource has pending changes, the operator continues to run the last-deployed snapshot. Some settings
never replace pods: the replica count, autoscaling, host and TLS, and backup settings. The operator reads these
settings from the current spec, so they take effect immediately. For more information, see
[Configuration](/docs/reference/configuration/#live-settings-and-settings-that-need-a-deploy) and
[Projects and environments](/docs/guides/projects-and-environments/).

## The kinds

There are 18 Geass kinds. For a resource that the dashboard creates for you, the table gives the dashboard screen.

| Kind | Represents | Created by |
| --- | --- | --- |
| [GeassProject](/docs/reference/resources/geass-project/) | A project, its environments, region, required node tags, webhooks, PR environments, backup storage, and network rules | The dashboard (**New project**) |
| [GeassApp](/docs/reference/resources/geass-app/) | A service: an image, a repository build, or a function (with its schedules). A service can sleep when it is idle | The dashboard |
| [GeassDatabase](/docs/reference/resources/geass-database/) | A database (Postgres, MySQL, SQLite, Redis), in the cluster or external, and the shared database servers | The dashboard |
| [GeassLogicalDatabase](/docs/reference/resources/geass-logical-database/) | A database of its own on a shared server | The dashboard |
| [GeassObjectStore](/docs/reference/resources/geass-object-store/) | A bucket, on the cluster's object storage server or in AWS S3, and that server (SeaweedFS or Garage) | The dashboard |
| [GeassBackupArchive](/docs/reference/resources/geass-backup-archive/) | The backups of one database | Geass, when backups are on |
| [GeassCluster](/docs/reference/resources/geass-cluster/) | The K3s cluster and its add-ons | The installer |
| [GeassNode](/docs/reference/resources/geass-node/) | A server joined to the cluster over SSH, or a machine that Geass creates at a cloud | The dashboard (**Add server**), `geass servers add`, you with `kubectl`, or a pool |
| [GeassNodePool](/docs/reference/resources/geass-node-pool/) | A group of nodes that Geass adds and removes with the load. Geass makes the nodes at a cloud or from servers that you list | The dashboard (**Pools**), `geass pools create`, or you with `kubectl` |
| [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/) | Domain, Cloudflare, GitHub App, and other cluster-wide settings | The dashboard (**Settings**) |
| [GeassOrganization](/docs/reference/resources/geass-organization/) | An organization of the identity provider, with its workspaces. The people of an organization see only its projects | A platform administrator (the API, or `kubectl`) |
| [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/) | A connection to AWS, PlanetScale, or a cloud that creates machines | The dashboard (**Connectors**), or `geass connections add` for machines |
| [GeassGitHubConnection](/docs/reference/resources/geass-github-connection/) | A project's link to GitHub | The dashboard (project **GitHub**) |
| [GeassDeployment](/docs/reference/resources/geass-deployment/) | One recorded deploy of a service | Geass |
| [GeassBuild](/docs/reference/resources/geass-build/) | One repository build | Geass |
| [GeassConsoleSession](/docs/reference/resources/geass-console-session/) | An audited terminal in a service | The dashboard (terminal) |
| [GeassHAReadiness](/docs/reference/resources/geass-ha-readiness/) | The high availability check | The dashboard, or you |
| [GeassNetworkLog](/docs/reference/resources/geass-network-log/) | A request log record | Nothing in the current code |

The cluster also contains objects that Geass creates but does not own:

- CloudNativePG `Cluster`, `Backup`, and `ScheduledBackup` objects for Postgres.
- K3s `HelmChart` objects for add-ons.
- A CronJob for each schedule of a function.
- KEDA `HTTPScaledObject`s for services that sleep when they are idle.
- The admission policies and bindings that enforce required node tags.
- MOCO `MySQLCluster` objects for highly available MySQL, and mariadb-operator `MariaDB` and `PhysicalBackup` objects
  for MariaDB.
- The monitor and alert rules for the operator.

Treat these objects as internals. To change one of them, change the Geass resource that makes it.

## Inspect resources with kubectl

List the kinds, and then read them. The printer columns show the readiness quickly.

```bash title="List the kinds and the resources"
kubectl api-resources --api-group=geass.geass.dev
kubectl -n geass-system get geassprojects,geassapps,geassdatabases,geassobjectstores
```

```bash title="Show readable names next to the IDs"
kubectl -n geass-system get geassapps \
  -o custom-columns='NAME:.metadata.name,DISPLAY:.metadata.annotations.geass\.dev/display-name,PROJECT:.spec.project,ENV:.spec.environment'
```

```bash title="Read the conditions of one resource"
kubectl -n geass-system get geassapp storefront \
  -o jsonpath='{range .status.conditions[*]}{.type}{"\t"}{.status}{"\t"}{.reason}{"\t"}{.message}{"\n"}{end}'
```

```bash title="Find what is waiting for a deploy"
kubectl -n geass-system get geassapps,geassdatabases,geassobjectstores \
  -o custom-columns='KIND:.kind,NAME:.metadata.name,PENDING:.metadata.annotations.geass\.dev/pending-changes'
```

```bash title="See the workloads for an environment"
kubectl -n proj-1a2b3c4d-production get pods,svc,ingress
```

The same data is available over HTTP from `GET /api/bootstrap`. To get one resource at a time, use
`GET /api/v1/apps`, `GET /api/v1/apps/{name}`, and the matching routes for the other kinds. These routes return a
summary with the resource's `spec` and `status`. For more information, see the [REST API](/docs/reference/rest-api/).

## Which resources are safe to edit by hand

| Kind | By hand |
| --- | --- |
| GeassProject, GeassApp, GeassDatabase, GeassObjectStore, GeassLogicalDatabase | Safe to create and edit, with care (see the rules after this table). The dashboard edits the same fields |
| GeassPlatformConfig, GeassCluster | Safe. They are the only way to set some fields, for example the add-ons and `prometheusURL` |
| GeassOrganization | Safe to create and edit. The dashboard's API refuses a second organization with the same `idpID`. It also refuses to delete an organization that still has projects. `kubectl` does not do these checks |
| GeassNode | To add a server, you create a GeassNode. Edit `placement` through it. A node of a pool belongs to the pool: change the pool |
| GeassNodePool | Safe to create and edit. The size, region, image, and settings apply to machines that Geass makes after the edit |
| GeassCloudConnection, GeassGitHubConnection | Possible, but they only point at Secrets. The dashboard also writes the Secret. To rotate a credential, edit the Secret |
| GeassHAReadiness | Safe to create |
| GeassBackupArchive, GeassDeployment, GeassBuild, GeassConsoleSession, GeassNetworkLog | Do not edit. Geass writes them, and an edit can break a restore or a rollback |
| Any `status` field | Never edit. The operator overwrites it |

Follow these rules for resources that you write by hand:

- Create the resource in the `geass-system` namespace. In `spec.project`, give the ID of the project.
- If you create a resource with `spec.deploy.enabled: true` and no `geass.dev/*` annotations, Geass deploys it as
  written. Later edits apply immediately. Without `spec.deploy.enabled: true`, the resource stays a draft.
- A resource that the dashboard created, with `geass.dev/pending-updates` above `0`, runs its last-deployed snapshot.
  A `kubectl edit` of its spec has no effect until you deploy from the dashboard, or until you reset the annotations.
- Do not edit `metadata.finalizers`, `status`, or the `geass.dev/*` annotations.
- Do not remove a resource by deleting its finalizer. The controller uses finalizers to clean up volumes, AWS buckets,
  and backups.
- Change Secrets that Geass manages (for example `<app>-secrets`) through the dashboard or API, not by hand. The
  exception is when a page tells you otherwise.
- Set a project's `spec.requiredTags` through the dashboard or API. The dashboard and the API refuse these tags:
  - A tag that no node in the project's region has.
  - A tag that is not reserved on every node that has it.
  - A tag that the project's existing databases are not on.

  A hand edit skips these checks.

```yaml title="app.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassApp
metadata:
  name: hello
  namespace: geass-system
spec:
  project: proj-1a2b3c4d
  environment: production
  source:
    image:
      image: nginx:1.27
  port: 80
  deploy:
    enabled: true
```

## What happens under the hood

The operator runs one controller for each kind. A controller reconciles its resource when the resource changes, or
when an object that the resource owns changes. The controller then sets conditions in `status`:

- A `Ready` condition tells you whether the resource works. Its `reason` and `message` tell you why it does not work.
- After repeated errors, a controller sets `Ready` to `False` with the reason `ProvisionFailed`. The controller then
  stops retrying until you change the spec or redeploy.

When you delete a resource, its finalizer runs first. The finalizer removes what Geass created: workloads, volumes,
buckets and IAM users, and the final backup of a database.

## Troubleshooting

**A resource shows `Ready: False` with "Resource is configured and waiting for deployment".** The resource is a draft,
or it has pending changes. A service shows "Service is configured and waiting for deployment". Deploy the resource from
the dashboard or the API.

**A manual edit has no effect.** The resource has pending changes, so the operator runs its last-deployed snapshot.
Deploy from the dashboard. Alternatively, update `geass.dev/last-deployed`, and then set `geass.dev/pending-updates` to
`0`.

**`kubectl apply` is refused with a validation error.** The error names the field. Compare the field with the table on
the page of that kind. The CRD enforces patterns and limits. For a few kinds, it also enforces rules, for example "set
either restorePoint or time, not both".

**A deleted resource stays in `Terminating`.** Its finalizer is waiting for cleanup. Usually the finalizer cannot reach
an external resource, for example an AWS bucket, a volume, or a database backup. Read the resource's `status` and the
operator log (`kubectl -n geass-system logs deployment/geass-controller-manager -c manager`).

## Related

- [REST API](/docs/reference/rest-api/)
- [Configuration](/docs/reference/configuration/)
- [Projects and environments](/docs/guides/projects-and-environments/)
- [Glossary](/docs/reference/glossary/)
