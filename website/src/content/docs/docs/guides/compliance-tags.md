---
title: "Compliance node tags"
description: "Keep all the work of a project on servers that you choose, and keep other projects off them. Mark a compliance tag, require it, and know the limits."
---

Some data must stay on servers that you can account for, for example health records on servers in one place. In Geass, you mark a server tag as a **compliance tag**. Then you **require** that tag on a project. After that, everything that the project runs stays on servers with the tag. Those servers take no work from other projects.

## How it works

1. You give the tag, for example `hipaa`, to the servers that qualify. On each server, you mark the tag as a compliance tag. A compliance tag is always a [reserved tag](/docs/guides/scaling-and-placement/#reserved-tags), so it keeps other work off the server.
2. You require the tag on the project.
3. Geass keeps everything of the project on those servers: services, databases and their data copies, builds, and backup jobs.
4. Geass also refuses or corrects work in the project that was created outside Geass and does not ask for the tag.

## Before you start

- At least one server in the region of the project must have the tag. Tag servers on the **Usage** page. See [Scaling and placement](/docs/guides/scaling-and-placement/). To add servers, see [Add servers](/docs/getting-started/add-servers/).
- To set tags on a server or a project, you need the admin role. Project tokens cannot set tags.
- **Geass does not move databases that already hold data.** If a database of the project runs on servers without the tag, the project cannot take the tag. See [Projects that already have databases](#projects-that-already-have-databases).
- The check of work created outside Geass needs Kubernetes 1.30 or later. The correction needs 1.36 or later. Everything that Geass creates itself stays on the tagged servers on every version.

## Mark a tag as a compliance tag

1. In the left rail, open **Usage**. The **Nodes** table lists every server.
2. Click **Edit** on a server that qualifies.
3. Under **Tags**, add the tag, for example `hipaa`. Separate tags with commas.
4. Under **Compliance tags**, turn on `hipaa is a compliance tag`. **Reserve this node for hipaa** also turns on, and stays on.
5. Click **Save**.
6. Do these steps again for every server that qualifies.

The **Tags** column then shows `hipaa (compliance)`.

The dashboard warns you: "hipaa is now a compliance tag on this node. Workloads already running here keep running until they restart, so restart any that do not belong to a project requiring hipaa". Geass does not move running work.

You cannot turn off a compliance tag while a project requires it.

From the command line:

```bash
geass nodes tag worker-2 --region eu --tags hipaa --compliance hipaa
```

This command replaces all the placement of the server. So give the region, the site, and every tag that you want to keep. See [CLI commands](/docs/reference/cli/).

## Require tags on a project

1. Open the project, and select **Settings**, and then **Compliance**.
2. Select the tags. Each tag shows the number of servers in the region of the project, for example `hipaa (2 nodes, compliance)`.
3. Click **Save**. The dashboard shows "Saved".

The list shows only the tags of servers in the region of the project. You cannot select a tag that shows "Not a compliance tag yet." A tag that the project requires stays in the list when no server has it any more, as "(no node carries it)". So you can remove it. A project can require up to 8 tags.

From the command line:

```bash
geass compliance set morning-beach hipaa      # replaces the current tags
geass compliance show morning-beach           # what is required, and where it falls short
geass compliance clear morning-beach
```

### What Geass checks before it saves

| Check | Message |
| --- | --- |
| Every tag is on a server in the region of the project. | "No node in the eu region is tagged hipaa" |
| Every server in the region with the tag is reserved for it. | "Nodes worker-1 carry a required tag without being reserved for it, so other projects could run beside this project's data. Mark the tag as a compliance tag on those nodes first" |
| No more than 8 tags. | "at most 8 tags can be required" |
| Every database of the project already runs on servers with the new tags. | "These databases keep their data on nodes without hipaa: "orders". Back them up and restore them into new databases, then set the tags" |

### See where a project falls short

`geass compliance show` reports the required tags, the region, and each database of the project. For each database, it says if the database complies, and which tags it is missing. It also lists the servers that have a required tag but are not reserved for it. Services follow the tags of the project immediately, so only databases can fall short.

## What the tags cover

| Part of the project | What Geass does |
| --- | --- |
| Services and functions | They run only on servers with every required tag. This is in addition to the region and the **Run on** tags of the service. When you change the tags of the project, Geass moves the services to the tagged servers. |
| Databases | Placed one time, when Geass first creates them, on servers with the tags. They never move. |
| Data copies of databases | With the storage add-on, every copy stays on tagged servers in the region. Without it, the data stays on the server where the database runs. |
| Builds, backups, restores, and function schedules | They run on servers with the tags. So a copy of the data is as protected as the data. |
| Buckets on the object storage server | The server must run on servers with the tags. If it does not, Geass refuses the bucket. |
| Databases on a shared server | The shared server must run on servers with the tags. If it does not, Geass refuses the database. |
| Logs | Not sent to the shared log store. See [Logs](#logs). |
| Work created outside Geass | Geass gives it the tags, or refuses it. |

### Shared servers and object storage

A shared database server and the object storage server do not belong to one project. They have their own **Run on** tags. The setup dialog has no field for these tags. Set them through the API with `runOnTags`. See [Shared database servers](/docs/guides/shared-database-servers/) and [Object storage](/docs/guides/object-storage/).

The data of the object storage server stays on the servers with its tags. See [Object storage](/docs/guides/object-storage/#where-the-servers-data-is-kept).

A project that requires tags can use a shared server only when that server runs on servers with all the tags. If not, a database says "This database server does not run on nodes tagged hipaa, which the project requires". A bucket says "This object storage server does not run on nodes tagged hipaa, which the project requires".

### Logs

Every project shares the log store, and the log store can run on any server. So Geass never sends the logs of a project that requires tags to the log store. The logs also do not go to a [log store outside the cluster](/docs/guides/observability/#send-metrics-and-logs-outside-the-cluster).

When you select **History** on the **Logs** tab of the project, the page says "Log history is not kept for projects that require node tags, so their logs never leave the nodes they run on. Live logs are still available." Live logs continue to work. Geass does not remove lines that it sent before the project required tags.

## Projects that already have databases

A database keeps its data on the servers where Geass first created it. So Geass refuses a tag if a database of the project does not run on servers with that tag. A database that is not created yet does not count.

A restore creates a new database, and Geass places it with the tags that the project has at that moment. So use this order. See [Backups and restore](/docs/guides/backups-and-restore/).

1. Make sure that each database has a recent backup.
2. Delete the databases. Geass takes a final backup first. You can restore that backup for seven days.
3. Set the required tags on the project.
4. Restore each database into a new database. Geass places it on the tagged servers.

Between steps 2 and 4, the services of the project have no database. Plan for that time. As an alternative, create a new project with the tags before its first database, and load the data into it.

## Limits

Compliance tags control where Geass puts work. They are not a guarantee by themselves.

- **Running work is not moved.** Work that was on a server when it became a compliance server stays until it restarts. The data copies of other projects on that server also stay.
- **A server that an attacker controls is not covered.** This feature is placement, not isolation between servers.
- **The control plane is not covered.** The settings, variables, and secret variables of a project are kept on the control plane.
- **Backups leave the cluster by design.** They go to the backup storage of the project in your cloud account. External databases and buckets are outside the cluster.
- **Metrics are not held back.** The metrics store keeps the CPU, memory, and requests of the project, on whatever server it runs on.
- **An admin can still put other work on the tag.** **Run on** accepts any tag. A shared server on a compliance server can serve projects that do not require the tag.
- **Data copies of shared database servers.** Their copies do not follow the tags, although the servers do. The object storage server is different: its data follows its tags.
- **Removing a tag from a project moves nothing back.** Databases keep their placement, and servers keep their reservation.

## Troubleshooting

**A tag shows "Not a compliance tag yet." and you cannot select it.**
Mark the tag as a compliance tag on every server that has it. Use **Edit** on the **Usage** page.

**Saving says "These databases keep their data on nodes without hipaa".**
Follow the order in [Projects that already have databases](#projects-that-already-have-databases).

**A service stays at "Setting up" after you set the tags.**
No ready server in the region has all the required tags and the tags of the service, or none has room. The capacity messages in [Scaling and placement](/docs/guides/scaling-and-placement/#the-capacity-check) name the tags.

**A database or bucket says the server "does not run on nodes tagged hipaa".**
Set up the shared server again on servers with the tag, or create a database that belongs to the project. See [Shared servers and object storage](#shared-servers-and-object-storage).

**The log history of the project is empty.**
This is expected. See [Logs](#logs).

**Other projects still run on a compliance server.**
They were there before the server became a compliance server. They stay until they restart. Redeploy them.

**A pod that you created is refused: "This project requires nodes tagged hipaa, so every pod needs the node selector ...".**
Add the node selector and the toleration that [Under the hood](#under-the-hood) describes. A pod that names its own server is refused with "This project requires tagged nodes, so a pod cannot choose its own node".

## Under the hood

| Object | What Geass puts there |
| --- | --- |
| Node | The label `tag.geass.dev/hipaa=true`, and the taint `tag.geass.dev/hipaa=compliance:NoSchedule`. |
| Pods of the project | The node selector `tag.geass.dev/<tag>: "true"`, and a toleration for the key `tag.geass.dev/<tag>`, for each required tag. |
| Namespace of each environment | The annotation `geass.dev/required-tags`, for example `gdpr,hipaa`. |
| `ValidatingAdmissionPolicy` `geass-required-node-tags` | Refuses a new pod in the project without the node selector, or with its own `nodeName`. Kubernetes 1.30 or later. |
| `MutatingAdmissionPolicy` `geass-required-node-tags-defaults` | Adds the node selector and the toleration to a pod without them, for example a pod that a database operator creates. Kubernetes 1.36 or later. On older versions, the first policy refuses such a pod. |
| `StorageClass` `geass-tagged-<hash>` | Keeps Longhorn data copies on the tagged servers. Other classes avoid compliance servers through the Longhorn node tag `geass-general`. |
| Fluent Bit | Leaves out the namespaces of the project. |

On a cluster older than 1.30, the Geass log says "Admission policies are not available, so pods created outside Geass are not checked for required node tags".

```bash
kubectl get nodes -o custom-columns='NODE:.metadata.name,TAINTS:.spec.taints[*].key'
kubectl get validatingadmissionpolicy geass-required-node-tags
kubectl get mutatingadmissionpolicy geass-required-node-tags-defaults
kubectl -n morning-beach-production get pods -o wide
kubectl -n morning-beach-production describe pod <pod>
```

The required tags are in `spec.requiredTags` of the project. A change with `kubectl` skips the checks of the dashboard. See [GeassProject](/docs/reference/resources/geass-project/) and [GeassNode](/docs/reference/resources/geass-node/).

## Related

- [Scaling and placement](/docs/guides/scaling-and-placement/)
- [Shared database servers](/docs/guides/shared-database-servers/)
- [Object storage](/docs/guides/object-storage/)
- [Storage](/docs/guides/storage/)
- [Network isolation](/docs/guides/network-isolation/)
