---
title: "Compliance node tags"
description: "Keep all the work of a project on nodes that you choose, and keep other projects off them. Mark a compliance tag, require it, and know the limits."
---

Some data must stay on machines that you can account for, for example health records on servers in one place. In Geass, you can mark a node tag as a **compliance tag**, and then **require** that tag on a project. After that, everything that the project runs stays on nodes that carry the tag. Those nodes take no work from other projects. This page covers how to set up compliance tags, what they cover, and what they do not cover.

## How it works

1. You tag the nodes that qualify, for example `hipaa`, and you mark the tag as a compliance tag on them. A compliance tag is always a [reserved tag](/docs/guides/scaling-and-placement/#reserved-tags). A reserved tag keeps every workload that does not ask for the tag off the node.
2. You set the tag as a required tag on the project.
3. Geass puts the tag on everything that the project runs:
   - A node selector and a toleration on its pods.
   - A storage class that keeps volume copies on those nodes.
   - The same placement for its builds, and for the jobs that back up its data.
4. An admission policy refuses a new pod in the namespaces of the project, unless the pod carries the same node selector. This policy catches pods that were created outside Geass.
5. A second policy gives a pod the node selector and the toleration that the pod does not have. This policy changes pods, and it does not refuse them. It is for pods that the operators that Geass runs create themselves. An example is the copy that the MariaDB operator takes to build a replica again. The settings of that operator cannot give such a pod a node selector. Without the second policy, the first policy would refuse those pods.

## Before you start

- At least one node in the region of the project carries the tag. Tag nodes under **Usage**. See [Scaling and placement](/docs/guides/scaling-and-placement/). To add machines, see [Add servers](/docs/getting-started/add-servers/).
- To set tags on a node or on a project, you need an admin: a dashboard admin, or a workspace API token with the admin role. Project tokens cannot set tags.
- **Databases that already hold data are not moved.** A project that has an in-cluster database on nodes without the tag cannot take the tag. See [Projects that already have databases](#projects-that-already-have-databases).
- The admission policy needs a Kubernetes version that serves `ValidatingAdmissionPolicy` in `admissionregistration.k8s.io/v1`. That is 1.30 or later. On an older cluster, everything else works, and Geass logs that pods created outside Geass are not checked.
- The policy that adds the node selector needs `MutatingAdmissionPolicy` in `admissionregistration.k8s.io/v1`. That is 1.36 or later. On an older cluster, Geass does not install that policy, and Geass logs that. Then the first policy refuses a pod that an operator creates without a node selector. Everything that Geass creates itself carries the selector in both cases.

## Mark a tag as a compliance tag

In the dashboard:

1. Open **Usage** in the left rail. The **Nodes** table lists every node.
2. Click **Edit** on a node that you want to qualify.
3. Under **Tags**, add the tag, for example `hipaa`. Separate several tags with commas.
4. Under **Compliance tags**, turn on `hipaa is a compliance tag`. The matching **Reserve this node for hipaa** switch turns on and stays on.
5. Click **Save**.
6. Repeat these steps for every node that you want to carry the tag.

The **Tags** column then shows `hipaa (compliance)`.

When you mark a tag as a compliance tag on a node, the dashboard shows a warning: "hipaa is now a compliance tag on this node. Workloads already running here keep running until they restart, so restart any that do not belong to a project requiring hipaa". Geass does not move running pods.

A tag that a project already requires stays a compliance tag on every node that carries it, whatever you send. You cannot turn it off while a project requires it.

From the command line, the API, or a manifest:

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

The node call replaces the whole placement of the node. So send the region, the site, and every tag that you want to keep. A compliance tag must also be in `tags`. See [GeassNode](/docs/reference/resources/geass-node/).

## Require tags on a project

In the dashboard:

1. Open the project and select **Settings**, then **Compliance**.
2. Select the tags. Each tag shows as `hipaa (2 nodes, compliance)`. The count is the number of nodes in the region of the project.
3. Click **Save**. The dashboard shows "Saved".

The list shows only the tags that nodes in the region of the project carry. A tag whose nodes are not reserved is unavailable, with "Mark this tag as a compliance tag on its nodes first." A tag that the project already requires stays in the list, even when no node carries it any more, as "(no node carries it)". So you can remove that tag. Geass saves the tags on `spec.requiredTags` of the project, at most 8.

From the command line, the API, or a manifest:

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

The fragment shows only the field involved. Add it to the full manifest of your project, or use `kubectl patch`. A change made with `kubectl` skips the checks that the dashboard and the API make. The next section lists these checks. Use the dashboard or the API.

### What Geass checks before it saves

| Check | Message |
| --- | --- |
| Every tag exists on a node in the region of the project. | "No node in the eu region is tagged hipaa" |
| Every node in the region that carries the tag is reserved for it. | "Nodes worker-1 carry a required tag without being reserved for it, so other projects could run beside this project's data. Mark the tag as a compliance tag on those nodes first" |
| No more than 8 tags. | "at most 8 tags can be required" |
| Every in-cluster database of the project already runs on nodes with the tags that you add. | "These databases keep their data on nodes without hipaa: "orders". Back them up and restore them into new databases, then set the tags" |

### See where a project falls short

`geass compliance show` and `GET /api/projects/<project>/compliance` report these values:

- The required tags.
- The region.
- Each in-cluster database, with `compliant` and the tags that it is `missing`.
- `openNodes`: the nodes that carry a required tag but are not reserved for it.

A service follows the tags of the project as soon as Geass reconciles the service. So databases are the only resource that can fall short.

## What the tags cover

| Part of the project | What Geass does |
| --- | --- |
| Services, including functions | Their pods get a node selector `tag.geass.dev/<tag>: "true"` and a toleration for each required tag. These come on top of the region of the project and the tags that the service chose under **Run on**. A node needs all of them. Geass updates services when the project changes: Geass changes their pod template, and Kubernetes rolls the pods onto the tagged nodes. |
| In-cluster databases | Placed one time, when first provisioned, on nodes with the tags. The placement is recorded on the database and never changes. |
| Volumes of databases | With Longhorn, a replicated volume uses the class `geass-tagged-<hash>`. This class keeps all copies of the volume on nodes in the region of the project that carry every tag. A highly available Postgres uses `geass-single`, whose one copy stays on the node where the instance runs. Without Longhorn, a volume stays on the node that its pod runs on. |
| Builds from Git | The build job runs in the region and on the required tags of the project. |
| Backups, restores, and logical-database setup | The same, so a dump of the data is as protected as the data. |
| Schedules of functions | The same. |
| Buckets in the cluster's object storage server | The server must run on nodes with the tags. If it does not, Geass refuses the bucket. |
| The object storage server's own volume | With Longhorn, its copies stay in its region, on the nodes that carry the tags that the server runs on. So its data is never replicated onto other nodes. Without Longhorn, the volume stays on the node that its pod runs on. |
| Databases on a shared server | The server must run on nodes with the tags. If it does not, Geass refuses the database. |
| Logs | Not sent to the shared log store. See [Logs](#logs). |
| Pods made by operators that Geass runs, and by anything else that lacks them | On Kubernetes 1.36 or later, the mutating policy gives them the node selector and the toleration. Where that policy is not available, the admission policy refuses them. The admission policy always refuses a pod that names its own node. |

The taint on the tagged nodes keeps other projects off them. The `geass-general` tag keeps the volume copies of other projects off them. See [What happens under the hood](#what-happens-under-the-hood).

### Shared servers and object storage

A shared database server and the object storage server do not belong to one project. Geass places them with their own **Run on** tags, which you choose when you set them up. The setup dialog has no field for these tags. Use the API (`runOnTags`), as described in [Shared database servers](/docs/guides/shared-database-servers/) and [Object storage](/docs/guides/object-storage/).

The volume of the object storage server follows the tags that the server was placed with:

- With Longhorn, the volume is created on the class `geass-tagged-<hash>` for the region and tags of the server. This class keeps all copies of the volume on nodes in that region that carry every tag. So the bucket data is never replicated onto other nodes. The cluster creates that class itself.
- Without Longhorn, the volume stays on the node that its pod runs on, and the pod runs only on the tagged nodes.
- A server with no tags uses the replicated class of its region, or the global replicated class. After any node is a compliance node, that class keeps its copies off compliance nodes.

See [Object storage](/docs/guides/object-storage/#where-the-servers-data-is-kept).

A project that requires tags can use a server only when the server runs on nodes with all of the tags. If the server does not, a database on it says "This database server does not run on nodes tagged hipaa, which the project requires". A bucket says "This object storage server does not run on nodes tagged hipaa, which the project requires". The check runs again when the project changes its tags.

### Logs

Every project shares the log store, and the log store might run on any node. So Geass never sends the logs of a project that requires tags to the log store. The operator excludes the namespaces of the project from the log shipper. So the logs also do not go to a [log store outside the cluster](/docs/guides/observability/#send-metrics-and-logs-outside-the-cluster). Metrics are not covered: a metrics store outside the cluster receives the metrics of the project like the metrics of any other project.

When you select **History** on the **Logs** page of the project, the page says "Log history is not kept for projects that require node tags, so their logs never leave the nodes they run on. Live logs are still available." Live logs are read from the nodes themselves, and they continue to work. History is not kept for these projects. Geass does not remove lines that were shipped before the project required tags. See [Logs, metrics and terminal](/docs/guides/observability/).

## Projects that already have databases

A database keeps its data on the nodes where it was first provisioned, and Geass does not move it. So the dashboard and the API refuse to add a tag if a provisioned in-cluster database of the project does not run on nodes with that tag. A database that is not provisioned yet does not count, because Geass places it with the tags.

A restore creates a new database next to the original. Geass places the new database with the tags that apply to the project at the moment that the new database is created. The restore dialog in the dashboard has no choice of nodes. So for a project whose databases have backups, use the order that follows. See [Backups and restore](/docs/guides/backups-and-restore/).

1. Make sure that each database has a recent backup.
2. Delete the databases. A delete takes a final backup first, and you can restore that backup for seven days.
3. Set the required tags on the project.
4. Restore each database into a new database. Geass places the new database on the tagged nodes.

Between steps 2 and 4, the services that use the databases have no database, so plan for that. The alternative is a new project that has the tags before its first database. You then load the data into that project.

## Limits

Compliance tags control where Geass puts work. They are not a guarantee by themselves.

- **Running work is not moved.** Pods that were on a node when it became a compliance node stay until they restart. Volume copies of other projects that are already on the node are not moved.
- **A compromised node is not covered.** This feature is placement, not isolation between nodes.
- **The control plane is not covered.** The settings, variables, and secrets of a project are Kubernetes objects that the control plane keeps. The operator and the dashboard run there.
- **Backups leave the cluster by design.** The backups of a project go to its backup storage in your AWS account. See [Backups and restore](/docs/guides/backups-and-restore/). External databases and buckets are outside the cluster.
- **Metrics are not held back.** Prometheus keeps the CPU, memory, and request counts of the workloads of the project, on whatever node Prometheus runs on.
- **An admin can still choose the tag for other work.** The **Run on** setting of a service accepts any tag that exists. So an admin can put the service of another project on a compliance node. A shared server on a compliance node can serve projects that do not require the tag. Geass checks only that the required tags of a project are on the server.
- **Volume copies of shared database servers.** Geass selects the class that keeps copies on the tagged nodes from the required tags of a project. A shared database server has no required tags of its own. So with Longhorn, its volume uses the replicated class of the cluster, which leaves out compliance nodes. Its pods run on the tagged nodes, but the copies of its data do not. The object storage server is different: its volume follows the tags that it was set up with, as described earlier on this page.
- **Removing a tag from a project does not move anything back.** Databases keep their placement, and nodes keep their reservation.

## What happens under the hood

| Object | Where | What Geass puts there |
| --- | --- | --- |
| Node | cluster | Label `tag.geass.dev/hipaa=true` and taint `tag.geass.dev/hipaa=compliance:NoSchedule`. A reserved tag that is not a compliance tag has the value `reserved`. |
| Namespace | each environment of the project | The annotation `geass.dev/required-tags`, for example `hipaa` or `gdpr,hipaa` (comma-separated, sorted). Removed when the project requires nothing. |
| ValidatingAdmissionPolicy and binding | cluster | `geass-required-node-tags`. Created the first time that a project requires tags, kept up to date when the operator changes it, and never removed. It denies a new pod in a namespace labeled `geass.dev/managed-by=geass`, unless the node selector of the pod has `tag.geass.dev/<tag>: "true"` for each required tag. It also refuses a pod that names its own node. |
| Longhorn nodes | `longhorn-system` | A tag `geass-tag-<tag>` for each Geass tag on the node, and `geass-region-<region>`. After any node is a compliance node, `geass-general` on every other node. |
| StorageClass | cluster | `geass-tagged-<10 hex characters>`, a hash of the region and tags, so projects with the same requirements share a class. The cluster controller makes the class after every node that it covers has a ready Longhorn manager. It makes one class for the requirements of each project, and one for the tags that the object storage server runs on. |
| StorageClass | cluster | The global and regional replicated classes name `geass-general` in their node selector, so the copies of other projects stay off compliance nodes. These classes switch over after every other node carries the tag. |
| GeassDatabase | `geass-system` | `geass.dev/node-placement` records the placement, including the required tags, for example `{"region":"eu","required":["hipaa"]}`. |
| HelmChart `geass-fluent-bit` | `kube-system` | Its values exclude the log files of the namespaces of the project. |

The managers and the driver of Longhorn tolerate reserved nodes. So a compliance node can hold replicas of the volumes that belong on it.

To look at these objects, run these commands:

```bash
kubectl get nodes -o custom-columns='NODE:.metadata.name,TAINTS:.spec.taints[*].key'
kubectl get namespace morning-beach-production -o jsonpath='{.metadata.annotations}{"\n"}'
kubectl get validatingadmissionpolicy,validatingadmissionpolicybinding geass-required-node-tags
kubectl get mutatingadmissionpolicy,mutatingadmissionpolicybinding geass-required-node-tags-defaults
kubectl -n longhorn-system get nodes.longhorn.io -o custom-columns='NODE:.metadata.name,TAGS:.spec.tags'
kubectl get storageclass
kubectl -n geass-system get geassdatabase orders -o jsonpath='{.metadata.annotations.geass\.dev/node-placement}{"\n"}'
kubectl -n morning-beach-production get pods -o wide
```

## Troubleshooting

**A tag is disabled in the project's Compliance section.**
The nodes of the tag are not reserved for it. Mark it as a compliance tag on every node that carries it, under **Usage** and **Edit**.

**Saving says "These databases keep their data on nodes without hipaa".**
The message suggests that you restore the databases into new databases first. A restore is placed with the tags that the project has when the restore is created. So follow the order in [Projects that already have databases](#projects-that-already-have-databases).

**A service stays Pending after I set the tags.**
No ready node in the region carries all the required tags and the tags of the service, or no such node has room. Read the pod: `kubectl -n morning-beach-production describe pod <pod>`. "didn't match Pod's node affinity/selector" means that no node matches. The capacity messages in [Scaling and placement](/docs/guides/scaling-and-placement/#the-capacity-check) name the tags.

**A database or bucket says the server "does not run on nodes tagged hipaa".**
Set up the server again on nodes with the tag, or use a database that belongs to the project. See [Shared servers and object storage](#shared-servers-and-object-storage).

**A pod I created is refused: "This project requires nodes tagged hipaa, so every pod needs the node selector ...".**
The admission policy refused the pod. Add `nodeSelector: {"tag.geass.dev/hipaa": "true"}` and a toleration for the key `tag.geass.dev/hipaa` to the pod. A pod that sets `nodeName` is refused with "This project requires tagged nodes, so a pod cannot choose its own node".

**The log history of the project is empty.**
This is expected. See [Logs](#logs).

**Other projects' pods are still on a compliance node.**
These pods were there before the node became a compliance node, and they stay until they restart. Restart them.

**A pod I created without a node selector was given one.**
That is the second policy, `geass-required-node-tags-defaults`. The policy adds the node selector and the toleration for each required tag that the pod does not have. It keeps everything that the pod already had. It also sets a tag that the pod set to anything but `"true"`. It does not touch a pod in a namespace that requires nothing.

**Pods created with `kubectl` are not refused.**
Check `kubectl get validatingadmissionpolicy geass-required-node-tags`. On a cluster older than 1.30, the cluster might not serve that kind. Then the operator log says "Admission policies are not available, so pods created outside Geass are not checked for required node tags".

## Related

- [Scaling and placement](/docs/guides/scaling-and-placement/)
- [Shared database servers](/docs/guides/shared-database-servers/)
- [Object storage](/docs/guides/object-storage/)
- [Storage](/docs/guides/storage/)
- [Network isolation](/docs/guides/network-isolation/)
- [GeassProject](/docs/reference/resources/geass-project/)
- [GeassNode](/docs/reference/resources/geass-node/)
