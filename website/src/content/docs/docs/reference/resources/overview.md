---
title: "Resources overview"
description: "How the Geass dashboard and its Kubernetes resources relate, which kinds exist, how to inspect them with kubectl, and which are safe to edit by hand."
sidebar:
  order: 0
---

Geass stores everything it manages as Kubernetes custom resources. The dashboard is the normal way to create and change
them, and each page in this section documents one kind: its fields, status and how the dashboard maps to it. You do not need
this section to use Geass. You need it when you automate, debug with `kubectl`, or want to know what a click really does.

## The dashboard and the resources

The dashboard is the primary interface and the resources are the model underneath it. There is no separate database. When
you click **Deploy changes**, the dashboard's API reads a resource through the Kubernetes API, changes its `spec` and
annotations, and writes it back. The operator watches the resources and makes the cluster match them, then reports what it
did in `status`. The dashboard shows that status.

```text
dashboard or API  ->  Geass resource (spec)  ->  operator reconciles  ->  workloads in your namespaces
                                              <-  status and conditions
```

Everything lives in the `geass-system` namespace, in the API group `geass.geass.dev`, version `v1alpha1`. All kinds are namespaced.
The workloads they create (pods, Services, Ingresses, Secrets) live in one namespace per project environment, named
`<project id>-<environment>`.

:::caution
The API version is `v1alpha1`. Fields can change between releases, and Geass has no conversion between versions. Pin your manifests
to a Geass release, and check them after an upgrade.
:::

### Names, display names and IDs

A project is named by an ID such as `proj-1a2b3c4d`; its readable name is `spec.displayName`. Other resources get a generated name
such as `morning-beach`. The label the dashboard shows may differ from the resource name: it is kept in the annotation
`geass.dev/display-name`. Always use the resource name, the one in `metadata.name`, in `kubectl` and in the API.

### Drafts, pending changes and deploys

Most resources use a draft-and-deploy flow so a change does not restart anything until you say so. Geass records its state on the resource
itself:

| Where | What |
| --- | --- |
| `spec.deploy.enabled` | `false` while the resource is a draft; set to `true` by a deploy. Apps, databases and buckets have it |
| Annotation `geass.dev/pending-changes` | Comma-separated kinds of waiting changes: `created`, `settings`, `variables` |
| Annotation `geass.dev/pending-updates` | A counter of saved changes not yet deployed |
| Annotation `geass.dev/last-deployed` | The spec as last deployed, as JSON |
| Annotation `geass.dev/deploy-workflow` | `true` on resources the dashboard created; turns the gating on |
| Annotation `geass.dev/deploy-revision` | Changes on every deploy so pods restart and pick up new variables |
| Labels `geass.dev/project`, `geass.dev/environment`, and `geass.dev/app` or `geass.dev/name` | Tie records and workloads to what they belong to. `geass.dev/app` is on records the dashboard creates (deploy records, console sessions), `geass.dev/name` on objects the operator creates |
| Annotation `geass.dev/required-tags` on a project environment's namespace | The node tags the project requires, sorted and comma-separated. The admission policy `geass-required-node-tags` reads it. Geass writes it from `spec.requiredTags` on the project |

While a deployed resource has pending changes, the operator keeps running the last-deployed snapshot. A few settings that never replace pods
(replica count, autoscaling, host and TLS, backup settings) are read from the current spec so they take effect at once. See
[Configuration](/docs/reference/configuration/#live-settings-and-settings-that-need-a-deploy) and
[Projects and environments](/docs/guides/projects-and-environments/).

## The kinds

There are 16 Geass kinds. Resources that the dashboard creates for you are marked with its screen.

| Kind | Represents | Created by |
| --- | --- | --- |
| [GeassProject](/docs/reference/resources/geass-project/) | A project, its environments, region, required node tags, webhooks, PR environments, backup storage and network rules | The dashboard (**New project**) |
| [GeassApp](/docs/reference/resources/geass-app/) | A service: an image, a repository build or a function (with its schedules), which can sleep when idle | The dashboard |
| [GeassDatabase](/docs/reference/resources/geass-database/) | A database (Postgres, MySQL, SQLite, Redis), in the cluster or external, and the shared database servers | The dashboard |
| [GeassLogicalDatabase](/docs/reference/resources/geass-logical-database/) | A database of its own on a shared server | The dashboard |
| [GeassObjectStore](/docs/reference/resources/geass-object-store/) | A bucket, in MinIO or AWS S3, and the cluster MinIO server | The dashboard |
| [GeassBackupArchive](/docs/reference/resources/geass-backup-archive/) | The backups of one database | Geass, when backups are on |
| [GeassCluster](/docs/reference/resources/geass-cluster/) | The K3s cluster and its add-ons | The installer |
| [GeassNode](/docs/reference/resources/geass-node/) | A server joined to the cluster over SSH | You, with `kubectl` |
| [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/) | Domain, Cloudflare, GitHub App and other cluster-wide settings | The dashboard (**Settings**) |
| [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/) | An AWS or PlanetScale connection | The dashboard (**Connectors**) |
| [GeassGitHubConnection](/docs/reference/resources/geass-github-connection/) | A project's link to GitHub | The dashboard (project **Integrations**) |
| [GeassDeployment](/docs/reference/resources/geass-deployment/) | One recorded deploy of a service | Geass |
| [GeassBuild](/docs/reference/resources/geass-build/) | One repository build | Geass |
| [GeassConsoleSession](/docs/reference/resources/geass-console-session/) | An audited terminal in a service | The dashboard (terminal) |
| [GeassHAReadiness](/docs/reference/resources/geass-ha-readiness/) | The high availability check | The dashboard, or you |
| [GeassNetworkLog](/docs/reference/resources/geass-network-log/) | A request log record | Nothing in the current code |

The cluster also contains objects that Geass creates but does not own: CloudNativePG `Cluster`, `Backup` and `ScheduledBackup` objects for
Postgres, K3s `HelmChart` objects for add-ons, a CronJob for each schedule of a function, KEDA `HTTPScaledObject`s for services that sleep
when idle, the admission policy and binding that enforce required node tags, and the monitor and alert rules for the operator. Treat those as
internals; change them through the Geass resource that produces them.

## Inspect resources with kubectl

List the kinds, then read them. The printer columns show readiness at a glance.

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

The same data is available over HTTP from `GET /api/bootstrap`, and one resource at a time from `GET /api/v1/apps`, `GET /api/v1/apps/{name}`
and the matching routes for the other kinds, which return a summary with the resource's `spec` and `status`; see the
[REST API](/docs/reference/rest-api/).

## Which resources are safe to edit by hand

| Kind | By hand |
| --- | --- |
| GeassProject, GeassApp, GeassDatabase, GeassObjectStore, GeassLogicalDatabase | Safe to create and edit, with care (see below). The dashboard edits the same fields |
| GeassPlatformConfig, GeassCluster | Safe. They are the only way to set some fields, such as the add-ons and `prometheusURL` |
| GeassNode | Creating one is how you add a server. Edit `placement` through it |
| GeassCloudConnection, GeassGitHubConnection | Possible, but they only point at Secrets; the dashboard also writes the Secret. Rotate by editing the Secret |
| GeassHAReadiness | Safe to create |
| GeassBackupArchive, GeassDeployment, GeassBuild, GeassConsoleSession, GeassNetworkLog | Do not edit. Geass writes them, and an edit can break a restore or a rollback |
| Any `status` field | Never edit. The operator overwrites it |

Rules for hand-written resources:

- Create in the `geass-system` namespace, and name the project by its ID in `spec.project`.
- A resource you create with `spec.deploy.enabled: true` and no `geass.dev/*` annotations is deployed as written, and later edits apply at once.
  Without `spec.deploy.enabled: true` it stays a draft.
- A resource the dashboard created, with `geass.dev/pending-updates` above `0`, runs its last-deployed snapshot, so a `kubectl edit` of its spec does not take
  effect until you deploy from the dashboard (or reset the annotations).
- Do not edit `metadata.finalizers`, `status`, or the `geass.dev/*` annotations, and do not remove a resource by deleting its finalizer: the controller uses finalizers to
  clean up volumes, AWS buckets and backups.
- Secrets that Geass manages (for example `<app>-secrets`) change through the dashboard or API, not by hand, unless a page says otherwise.
- Set a project's `spec.requiredTags` through the dashboard or API. They refuse a tag that no node in the project's region carries, a tag that is
  not reserved on every node that has it, and a tag the project's existing databases are not on. A hand edit skips those checks.

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

The operator runs one controller per kind. Each reconciles its resource when it, or an object it owns, changes, and sets conditions in `status`. A `Ready`
condition tells you whether the thing works; its `reason` and `message` say why not. After repeated errors a controller sets `Ready` to `False` with reason
`ProvisionFailed` and stops retrying until you change the spec or redeploy. Deleting a resource runs its finalizer first, which removes what Geass created
(workloads, volumes, buckets and IAM users, and the final backup of a database).

## Troubleshooting

**A resource shows `Ready: False` with "Resource is configured and waiting for deployment".** It is a draft or has pending changes (a service says
"Service is configured and waiting for deployment"). Deploy it from the dashboard or the API.

**A manual edit has no effect.** The resource has pending changes, so the operator runs its last-deployed snapshot. Deploy from the dashboard, or set
`geass.dev/pending-updates` to `0` after you update `geass.dev/last-deployed`.

**`kubectl apply` is refused with a validation error.** The error names the field. Check it against the table on that kind's page; the CRD enforces patterns, limits and (for a few kinds) rules such as "set
either restorePoint or time, not both".

**A deleted resource stays in `Terminating`.** Its finalizer is waiting for cleanup, usually an external resource (an AWS bucket, a volume, a database backup) that it cannot reach. Read the
resource's `status` and the operator log (`kubectl -n geass-system logs deployment/geass-controller-manager -c manager`).

## Related

- [REST API](/docs/reference/rest-api/)
- [Configuration](/docs/reference/configuration/)
- [Projects and environments](/docs/guides/projects-and-environments/)
- [Glossary](/docs/reference/glossary/)
