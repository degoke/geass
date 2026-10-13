---
title: "Compliance node tags"
description: "Keep everything a project runs on nodes you choose and other projects off them. Mark a compliance tag, require it on a project, and know the limits."
---

Some data has to stay on machines you can account for, such as health records on servers in one place. Geass lets you mark a node tag as a **compliance tag** and then **require** that tag on a project. From then on everything the project runs stays on nodes that carry the tag, and those nodes take no other project's work. This page covers how to set it up, what is covered and what is not.

## How it works

1. You tag the nodes that qualify, for example `hipaa`, and mark the tag as a compliance tag on them. A compliance tag is always a [reserved tag](/docs/guides/scaling-and-placement/#reserved-tags): it keeps every workload that does not ask for it off the node.
2. You set the tag as a required tag on the project.
3. Geass puts the tag on everything the project runs: a node selector and a toleration on its pods, a storage class that keeps volume copies on those nodes, and the same placement for its builds and the jobs that back up its data.
4. An admission policy refuses a new pod in the project's namespaces unless it carries the same node selector, which catches pods created outside Geass.

## Before you start

- At least one node in the project's region carries the tag. Tag nodes under **Usage** ([Scaling and placement](/docs/guides/scaling-and-placement/)). See [Add servers](/docs/getting-started/add-servers/) to add machines.
- Setting tags on a node or on a project needs an admin: a dashboard admin, or a workspace API token with the admin role. Project tokens cannot.
- **Databases that already hold data are not moved.** A project that has an in-cluster database on nodes without the tag cannot take the tag. See [Projects that already have databases](#projects-that-already-have-databases).
- The admission policy needs a Kubernetes version that serves `ValidatingAdmissionPolicy` in `admissionregistration.k8s.io/v1`, which is 1.30 or later. On an older cluster everything else works, and Geass logs that pods created outside Geass are not checked.

## Mark a tag as a compliance tag

In the dashboard:

1. Open **Usage** in the left rail. The **Nodes** table lists every node.
2. Select **Edit** on a node that should qualify.
3. Add the tag under **Tags**, for example `hipaa`. Separate several tags with commas.
4. Under **Compliance tags**, switch on `hipaa is a compliance tag`. The matching **Reserve this node for hipaa** switch turns on and stays on.
5. Choose **Save**. Repeat for every node that should carry the tag.

The **Tags** column then reads `hipaa (compliance)`.

When you mark a tag as a compliance tag on a node, the dashboard warns you: "hipaa is now a compliance tag on this node. Workloads already running here keep running until they restart, so restart any that do not belong to a project requiring hipaa". Geass does not move running pods.

A tag that a project already requires stays a compliance tag on every node that carries it, whatever you send. You cannot switch it off while a project requires it.

From the command line, the API or a manifest:

```bash
geass nodes tag worker-2 --region eu --tags hipaa --compliance hipaa
```

```bash
curl -sS -X POST https://geass.example.com/api/nodes/worker-2/placement \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d region=eu -d tags=hipaa -d compliance=hipaa
```

```yaml title="hipaa-worker.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassNode
metadata:
  name: worker-2
  namespace: geass-system
spec:
  role: Worker
  eip: 203.0.113.21
  sshUser: ubuntu
  sshKeySecretRef:
    name: worker-ssh-key
  clusterRef:
    name: default
  placement:
    region: eu
    tags:
      - name: hipaa
        compliance: true
```

The node call replaces the node's whole placement, so send the region, the site and every tag you want to keep. A compliance tag must also be in `tags`. See [GeassNode](/docs/reference/resources/geass-node/).

## Require tags on a project

In the dashboard:

1. Open the project and choose **Settings**, then **Compliance**.
2. Tick the tags. Each tag reads like `hipaa (2 nodes, compliance)`. The count is the nodes in the project's region.
3. Choose **Save**. The dashboard shows "Compliance settings saved".

Only tags that nodes in the project's region carry are listed. A tag whose nodes are not reserved is disabled, with "Mark this tag as a compliance tag on its nodes first." A tag the project already requires stays listed even when no node carries it any more, as "(no node carries it)", so you can remove it. The tags are saved on `spec.requiredTags` of the project, at most 8.

From the command line, the API or a manifest:

```bash
geass compliance set morning-beach hipaa      # replaces the current tags
geass compliance show morning-beach           # what is required, and where it falls short
geass compliance clear morning-beach
```

```bash
curl -sS -X POST https://geass.example.com/api/projects/morning-beach/compliance \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d requiredTags=hipaa
```

```yaml title="project-compliance.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassProject
metadata:
  name: morning-beach
  namespace: geass-system
spec:
  requiredTags:
    - hipaa
```

The fragment shows only the field involved. Add it to your project's full manifest, or use `kubectl patch`. A change made with `kubectl` skips the checks the dashboard and the API make (below). Use the dashboard or the API.

### What Geass checks before it saves

| Check | Message |
| --- | --- |
| Every tag exists on a node in the project's region. | "No node in the eu region is tagged hipaa" |
| Every node in the region that carries the tag is reserved for it. | "Nodes worker-1 carry a required tag without being reserved for it, so other projects could run beside this project's data. Mark the tag as a compliance tag on those nodes first" |
| No more than 8 tags. | "at most 8 tags can be required" |
| Every in-cluster database of the project already runs on nodes with the tags you are adding. | "These databases keep their data on nodes without hipaa: "orders". Back them up and restore them into new databases, then set the tags" |

### See where a project falls short

`geass compliance show` and `GET /api/projects/<project>/compliance` report the required tags, the region, each in-cluster database with `compliant` and the tags it `missing`, and `openNodes`, the nodes that carry a required tag without being reserved for it. A service follows the project's tags as soon as it is reconciled, so databases are the only resource that can fall short.

## What the tags cover

| Part of the project | What Geass does |
| --- | --- |
| Services, including functions | Their pods get a node selector `tag.geass.dev/<tag>: "true"` for each required tag, and a toleration for each, on top of the project's region and the tags the service chose under **Run on**. A node needs all of them. Services are updated when the project changes: Geass changes their pod template and Kubernetes rolls the pods onto the tagged nodes. |
| In-cluster databases | Placed once, when first provisioned, on nodes with the tags. The placement is recorded on the database and never changes. |
| Volumes of databases | With Longhorn, a replicated volume uses the class `geass-tagged-<hash>`, which keeps all its copies on nodes in the project's region that carry every tag. A highly available Postgres uses `geass-single`, whose one copy stays on the node where the instance runs. Without Longhorn, a volume stays on the node its pod runs on. |
| Builds from Git | The build job runs on the project's region and required tags. |
| Backups, restores and logical-database setup | The same, so a dump of the data is as protected as the data. |
| Schedules of functions | The same. |
| Buckets in the cluster's MinIO server | The server must run on nodes with the tags. Otherwise the bucket is refused. |
| Databases on a shared server | The server must run on nodes with the tags. Otherwise the database is refused. |
| Logs | Not sent to the shared log store. See [Logs](#logs). |
| Pods made by anything else | Refused by the admission policy. |

Other projects are kept off the tagged nodes by the taint on them, and their volume copies by the `geass-general` tag (see [What happens under the hood](#what-happens-under-the-hood)).

### Shared servers and MinIO

A shared database server and the MinIO server are not owned by one project. They are placed with their own **Run on** tags, chosen when you set them up, and the setup dialog has no field for that. Use the API (`runOnTags`), as described in [Shared database servers](/docs/guides/shared-database-servers/) and [Object storage](/docs/guides/object-storage/).

A project that requires tags can use a server only when the server runs on nodes with all of them. A database on a server that does not says "This database server does not run on nodes tagged hipaa, which the project requires". A bucket says "This MinIO server does not run on nodes tagged hipaa, which the project requires". The check runs again when the project changes its tags.

### Logs

The log store is shared by every project and may run on any node, so logs of a project that requires tags are never sent to it. The operator excludes the project's namespaces from the log shipper. When you choose **History** on the project's **Logs** page, it says "Log history is not kept for projects that require node tags, so their logs never leave the nodes they run on. Live logs are still available." Live logs are read from the nodes themselves and keep working. History is not kept for these projects, and Geass does not remove lines that were shipped before the project required tags. See [Logs, metrics and terminal](/docs/guides/observability/).

## Projects that already have databases

A database keeps its data on the nodes where it was first provisioned, and Geass does not move it. So the dashboard and the API refuse to add a tag that one of the project's provisioned in-cluster databases does not run on. A database that is not provisioned yet does not count: it is placed with the tags.

A restore creates a new database next to the original, and the new database is placed with the tags that apply to the project at the moment it is created. The dashboard's restore dialog has no choice of nodes. The order that follows from this, for a project whose databases have backups ([Backups and restore](/docs/guides/backups-and-restore/)):

1. Check that each database has a recent backup.
2. Delete the databases. Deleting takes a final backup first, which you can restore for seven days.
3. Set the required tags on the project.
4. Restore each database into a new database. It is placed on the tagged nodes.

Services that use the databases have none in between, so plan for that. The alternative is a new project that has the tags before its first database, with the data loaded into it.

## Limits

Compliance tags control where Geass puts work. They are not a guarantee on their own.

- **Running work is not moved.** Pods that were on a node when it became a compliance node stay until they restart. Volume copies of other projects that already sit on it are not moved.
- **A compromised node is not covered.** This is placement, not isolation between nodes.
- **The control plane is not covered.** A project's settings, variables and secrets are Kubernetes objects, kept by the control plane. The operator and dashboard run there.
- **Backups leave the cluster by design.** A project's backups go to its backup storage in your AWS account ([Backups and restore](/docs/guides/backups-and-restore/)). External databases and buckets are outside the cluster.
- **Metrics are not held back.** Prometheus keeps the CPU, memory and request counts of the project's workloads on whatever node it runs on.
- **An admin can still choose the tag for other work.** A service's own **Run on** accepts any tag that exists, so an admin can put another project's service on a compliance node. A shared server on a compliance node can serve projects that do not require the tag. Geass checks only that a project's required tags are on the server.
- **Volume copies of shared servers.** The class that keeps copies on the tagged nodes is chosen from a project's required tags. A shared server has none of its own, so with Longhorn its volume uses the cluster's replicated class, which compliance nodes are left out of. Its pods run on the tagged nodes, and the copies of its data do not.
- **Removing a tag from a project does not move anything back.** Databases keep their placement and nodes keep their reservation.

## What happens under the hood

| Object | Where | What Geass puts there |
| --- | --- | --- |
| Node | cluster | Label `tag.geass.dev/hipaa=true` and taint `tag.geass.dev/hipaa=compliance:NoSchedule`. A reserved tag that is not a compliance tag has the value `reserved`. |
| Namespace | each environment of the project | The annotation `geass.dev/required-tags`, such as `hipaa` or `gdpr,hipaa` (comma-separated, sorted). Removed when the project requires nothing. |
| ValidatingAdmissionPolicy and binding | cluster | `geass-required-node-tags`. Created the first time a project requires tags, kept up to date when the operator changes it, and never removed. It denies a new pod in a namespace labelled `geass.dev/managed-by=geass` unless the pod's node selector has `tag.geass.dev/<tag>: "true"` for each required tag, and refuses a pod that names its own node. |
| Longhorn nodes | `longhorn-system` | A tag `geass-tag-<tag>` for each Geass tag on the node, `geass-region-<region>`, and, once any node is a compliance node, `geass-general` on every other node. |
| StorageClass | cluster | `geass-tagged-<10 hex characters>`, a hash of the region and tags, so projects with the same requirements share a class. The cluster controller makes it once every node it covers has a ready Longhorn manager. |
| StorageClass | cluster | The global and regional replicated classes name `geass-general` in their node selector, so other projects' copies stay off compliance nodes. They switch over once every other node carries the tag. |
| GeassDatabase | `geass-system` | `geass.dev/node-placement` records the placement, including the required tags, for example `{"region":"eu","required":["hipaa"]}`. |
| HelmChart `geass-fluent-bit` | `kube-system` | Its values exclude the log files of the project's namespaces. |

Longhorn's managers and its driver tolerate reserved nodes, so a compliance node can hold replicas of the volumes that belong on it.

```bash
kubectl get nodes -o custom-columns='NODE:.metadata.name,TAINTS:.spec.taints[*].key'
kubectl get namespace morning-beach-production -o jsonpath='{.metadata.annotations}{"\n"}'
kubectl get validatingadmissionpolicy,validatingadmissionpolicybinding geass-required-node-tags
kubectl -n longhorn-system get nodes.longhorn.io -o custom-columns='NODE:.metadata.name,TAGS:.spec.tags'
kubectl get storageclass
kubectl -n geass-system get geassdatabase orders -o jsonpath='{.metadata.annotations.geass\.dev/node-placement}{"\n"}'
kubectl -n morning-beach-production get pods -o wide
```

## Troubleshooting

**A tag is disabled in the project's Compliance section.**
Its nodes are not reserved for it. Mark it as a compliance tag on every node that carries it, under **Usage** and **Edit**.

**Saving says "These databases keep their data on nodes without hipaa".**
The message suggests restoring them into new databases first. A restore is placed with the tags the project has when it is created, so follow the order in [Projects that already have databases](#projects-that-already-have-databases).

**A service stays Pending after I set the tags.**
No ready node in the region carries all the required tags and the service's own tags, or none has room. Read the pod: `kubectl -n morning-beach-production describe pod <pod>`. "didn't match Pod's node affinity/selector" means no node matches. The capacity messages in [Scaling and placement](/docs/guides/scaling-and-placement/#the-capacity-check) name the tags.

**A database or bucket says the server "does not run on nodes tagged hipaa".**
Set the server up again on nodes with the tag, or use a database of the project's own. See [Shared servers and MinIO](#shared-servers-and-minio).

**A pod I created is refused: "This project requires nodes tagged hipaa, so every pod needs the node selector ...".**
The admission policy refused it. Add `nodeSelector: {"tag.geass.dev/hipaa": "true"}` and a toleration for the key `tag.geass.dev/hipaa` to the pod. A pod that sets `nodeName` is refused with "This project requires tagged nodes, so a pod cannot choose its own node".

**The log history of the project is empty.**
Expected. See [Logs](#logs).

**Other projects' pods are still on a compliance node.**
They were there before the node became a compliance node, and stay until they restart. Restart them.

**Pods created with `kubectl` are not refused.**
Check `kubectl get validatingadmissionpolicy geass-required-node-tags`. On a cluster older than 1.30 the kind may not be served, and the operator log says "Admission policies are not available, so pods created outside Geass are not checked for required node tags".

## Related

- [Scaling and placement](/docs/guides/scaling-and-placement/)
- [Shared database servers](/docs/guides/shared-database-servers/)
- [Object storage](/docs/guides/object-storage/)
- [Storage](/docs/guides/storage/)
- [Network isolation](/docs/guides/network-isolation/)
- [GeassProject](/docs/reference/resources/geass-project/)
- [GeassNode](/docs/reference/resources/geass-node/)
