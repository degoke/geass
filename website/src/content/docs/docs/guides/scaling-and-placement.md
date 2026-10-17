---
title: "Scaling and placement"
description: "Scale a service with replicas, sizes and autoscaling, and choose which servers it runs on with regions, sites and tags. Includes the capacity check."
---

Scaling decides how many copies of a service run and how much each one reserves. Placement decides which of your servers they run on. You scale with replicas, sizes, autoscaling, or by letting a service [sleep when idle](/docs/guides/scale-to-zero/). You place with regions, sites and tags that you attach to nodes, and, for data that must stay on certain machines, with [compliance tags](/docs/guides/compliance-tags/). Before anything new starts, Geass checks that the servers it may use have room for it.

## Before you start

- Changing a node's region, site and tags needs the admin role: a dashboard admin, or a workspace API token with the admin role. Project tokens cannot. Viewers and observers can read but change nothing.
- Choosing which nodes a service or database runs on (**Run on**) needs a signed-in admin. API tokens of every kind are refused, so a script cannot send `runOnTags`.
- Placement only matters with more than one node. To add servers, see [Add servers](/docs/getting-started/add-servers/).
- To see how full your servers are, open the **Usage** page ([Usage and capacity](/docs/guides/usage-and-capacity/)).

## The words

| Word | Meaning |
| --- | --- |
| **Replica** | One running copy of a service. |
| **Size** | The CPU and memory each replica reserves. |
| **Region** | A group of nodes that are close together, such as `eu` or `us`. A project that has a region runs only on nodes in it. |
| **Site** | The provider or data centre inside a region, such as `hetzner-fsn1`. The dashboard calls it **Site**; the Kubernetes field is the zone. |
| **Tag** | A free-form label on a node that says what it is for, such as `storage` or `gpu`. |
| **Reserved tag** | A tag that keeps every workload that does not ask for it off the node. |
| **Compliance tag** | A reserved tag for data that must stay on certain nodes, such as `hipaa`. Projects that require it run only on nodes that carry it, and nothing else runs on those nodes. See [Compliance node tags](/docs/guides/compliance-tags/). |
| **Required tag** | A tag set on a project (`spec.requiredTags`) that every workload of the project must run on. |

Names of regions, sites and tags use lowercase letters, numbers and hyphens, start and end with a letter or number, and have at most 40 characters. A node holds at most 16 tags, and a service or database can ask for at most 8.

## Scale a service

1. Open the project, select the service, and choose **Settings**.
2. Open the **Scale** section.
3. Change the values below and choose **Save changes**.

| Setting | What it does | Default | Takes effect |
| --- | --- | --- | --- |
| **Replicas** | How many copies run. The form takes 1 or more; the API accepts 0 to 1000. | 1 | At once |
| **CPU reserved** and **Memory reserved** (under **Resources**) | What each replica reserves. CPU: 0.1, 0.25, 0.5, 1 or 2. Memory: 128 MB, 256 MB, 512 MB, 1 GB, 2 GB or 4 GB. | 0.1 CPU, 128 MB | On deploy |
| **CPU can use up to** and **Memory can use up to** | The most a replica may use while its server has room: one of the same sizes, and never below what is reserved. | Same as reserved | On deploy |
| **Run on** | Node tags the service must run on. Only shown when nodes in the project's region carry tags. | None | On deploy |
| **Enable autoscaling** | Adds copies when average CPU crosses a target. | Off | At once |
| **Scale up to** | The most copies autoscaling may run. | 3 | At once |
| **CPU target %** | Average CPU, as a percentage of what the service reserves, that triggers a new copy. 1 to 100. | 50 | At once |
| **Sleep when idle** | Stops the service when it has had no requests for a while, and starts it on the next request. It needs the serverless add-on and a public domain, and cannot be combined with autoscaling. See [Sleep when idle](/docs/guides/scale-to-zero/). | Off | At once |
| **Sleep after (minutes)** | How long without requests before the service is stopped. 1 to 1440. | 5 | At once |

Settings marked "On deploy" show up as a pending change. Choose **Deploy changes** in the top bar to apply them. Settings marked "At once" show an **Applies instantly** badge and need no deploy.

What a replica reserves is what the scheduler and the capacity check count. By default it is also the most the replica may use, so it cannot use more than it reserved. Set **CPU can use up to** and **Memory can use up to** higher to let it use more while its server has room, and see [Apps](/docs/guides/apps/#scale) for what that costs: a replica that goes over what it may use is stopped, and one that uses more than it reserved is the first to be stopped when its server runs out of memory.

With autoscaling, the minimum number of copies is your **Replicas** value, and the maximum is **Scale up to**. The autoscaler measures CPU, so the cluster needs its metrics server, which K3s ships.

Switching **Enable autoscaling** off turns autoscaling off. Switching on **Sleep when idle** switches autoscaling off, and the other way round.

Through the API, autoscaling is on when the request has `autoscaling=on`, and also when it carries a `targetCPU` with a `maxReplicas` above 1, even without the flag. To turn it off, send `maxReplicas=1`:

```bash
curl -sS -X POST https://geass.example.com/api/apps/web/update \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d autoscaling= -d maxReplicas=1
```

With `kubectl`, remove the field:

```bash
kubectl -n geass-system patch geassapp web --type json \
  -p '[{"op":"remove","path":"/spec/autoscaling"}]'
```

:::caution
With autoscaling on, Geass writes your **Replicas** value back into the Deployment each time it reconciles the service: when you deploy, when you save settings, when the operator restarts, and every 30 seconds while the service is not ready. Expect the count to return to **Replicas** at those moments and the autoscaler to move it again afterwards. A service that sleeps when idle is different: Geass does not set its replica count at all while it sleeps, so the add-on that starts and stops it is the only thing that does.
:::

Geass adds no anti-affinity or spread rule of its own, and it creates no pod disruption budget. The Kubernetes scheduler chooses nodes, and two replicas can end up on the same node. See [High availability](/docs/operations/high-availability/) for what this means.

### Change replicas from a script

A replica count never replaces pods, so the API applies it at once. A project token can call this for its own services:

```bash
curl -sS -X POST https://geass.example.com/api/apps/web/scale \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d replicas=3
```

The count must be a whole number from 0 to 1000. This call changes only the replica count. The autoscaling minimum is set when you save the **Scale** section, so it does not follow a count you change here. On a service that sleeps when idle the count has no effect until you switch sleeping off.

### Scale with a manifest

```yaml title="web.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassApp
metadata:
  name: web
  namespace: geass-system
spec:
  project: morning-beach
  environment: production
  source:
    image:
      image: ghcr.io/example/storefront:1.4.2
  port: 8080
  deploy:
    enabled: true
  replicas: 3
  resources:
    requests: { cpu: 250m, memory: 256Mi }
    limits: { cpu: 250m, memory: 256Mi }
  autoscaling:
    minReplicas: 3
    maxReplicas: 6
    targetCPUUtilization: 60
  runOn:
    tags: [compute]
```

A manifest without `deploy.enabled: true` does not run: the operator holds the service with the message "Service is configured and waiting for deployment". A service that sets no requests reserves 0.1 CPU and 128 MB.

## Place databases

An in-cluster database runs on nodes in its project's region, and on nodes with the tags you chose. Two things differ from services.

- The nodes are chosen once, when the database is first provisioned, and recorded on the database in the annotation `geass.dev/node-placement`. Its volumes live there, so later changes to the project's region, the default region or the tags do not move it. The **Run on** choice in the database's **Settings** closes after the first deploy.
- The number of instances comes from **High availability**, not from a replica field. See [High availability](/docs/operations/high-availability/).

Choose a database's size under **Settings**. Postgres, MySQL and Redis servers default to 0.25 CPU and 512 MB for each instance, except that a high availability MySQL instance is never given less than 1 GB; SQLite defaults to 0.1 CPU and 128 MB. The cluster's object storage server is not a database: it defaults to 0.25 CPU and 512 MB with SeaweedFS and 0.1 CPU and 256 MB with Garage, and you choose its size, and the size of its volume (10 GiB by default), when you set it up (see [Object storage](/docs/guides/object-storage/)).

## Regions, sites and tags on nodes

### Set them in the dashboard

1. Open **Usage** in the left rail. The **Nodes** table lists every node.
2. Select **Edit** on the node. The button appears for admins.
3. Fill the **Region**, **Site** and **Tags** fields. Separate tags with commas. The dialog suggests regions and tags the cluster already uses.
4. To keep other work off the node, switch on **Reserve this node for** the tag. The switch appears for each tag you typed.
5. For a tag that marks data that must stay on certain machines, switch on `<tag> is a compliance tag` under **Compliance tags**. A compliance tag is always reserved. See [Compliance node tags](/docs/guides/compliance-tags/).
6. Choose **Save**. The dashboard shows "Node updated".

The dashboard warns you when the change leaves something without a home, or changes who may use the node:

- "<project> has no node left in the <region> region, so its workloads cannot restart until one is added". Running services are not moved, but they could not restart.
- "<project> requires <tag>, and no other node carries it, so its workloads cannot restart until one does".
- "<tag> is now a compliance tag on this node. Workloads already running here keep running until they restart, so restart any that do not belong to a project requiring <tag>".

### Set them in a node manifest

When you add a worker with a `GeassNode`, declare its placement so it carries the right labels from its first moment:

```yaml title="worker-eu-1.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassNode
metadata:
  name: worker-eu-1
  namespace: geass-system
spec:
  role: Worker
  eip: 203.0.113.10
  sshUser: ubuntu
  sshKeySecretRef:
    name: worker-ssh-key
  clusterRef:
    name: default
  placement:
    region: eu
    zone: hetzner-fsn1
    tags:
      - name: storage
        reserved: true
      - name: compute
      - name: hipaa
        compliance: true
```

The operator writes `placement` onto the Kubernetes node and keeps it there. Editing the node in the dashboard also updates the `GeassNode`, so the operator does not undo your edit. A node whose `GeassNode` has no `placement` is left alone, so labels you set by hand or that a provider added stay.

### Set them with kubectl or the API

Placement is ordinary node labels and taints, so you can set it by hand on any node:

```bash
kubectl label node node-2 \
  topology.kubernetes.io/region=eu \
  topology.kubernetes.io/zone=hetzner-fsn1 \
  tag.geass.dev/storage=true
kubectl taint node node-2 tag.geass.dev/storage=reserved:NoSchedule   # only for a reserved tag
kubectl taint node node-2 tag.geass.dev/hipaa=compliance:NoSchedule   # only for a compliance tag
```

The **Nodes** table reads the same labels and taints back. With a workspace token that has the admin role:

```bash
curl -sS -X POST https://geass.example.com/api/nodes/node-2/placement \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d region=eu -d zone=hetzner-fsn1 -d tags=storage,compute,hipaa -d reserved=storage -d compliance=hipaa
```

The call replaces the node's whole placement: a field you leave out is cleared, so send the region, site and every tag you want to keep. A reserved or compliance tag must also be in `tags`. A compliance tag is reserved whether or not you list it under `reserved`, and a tag that a project requires is made a compliance tag whatever you send. The response is `{"ok": true, "warnings": [...]}`, with `warnings` set to `null` when there is nothing to warn about. From the command line, use `geass nodes tag` ([CLI commands](/docs/reference/cli/)).

:::caution
If a node's `GeassNode` declares a `placement`, the operator removes the region, site and `tag.geass.dev/` labels, and the reserved-tag taints, that the declaration does not list. Change the `GeassNode`, or use the dashboard, instead of labelling those nodes by hand. The operator notices a change to the Kubernetes node when it happens and looks again every 10 minutes.
:::

### Reserved tags

A reserved tag puts the taint `tag.geass.dev/<tag>=reserved:NoSchedule` on the node. The taint keeps new workloads off the node unless they ask for the tag. It does not evict what is already running, so reserving a busy node changes where things start next, not where they run now.

A compliance tag is a reserved tag whose taint value is `compliance`, as in `tag.geass.dev/hipaa=compliance:NoSchedule`. It behaves the same way for scheduling. The value is how Geass tells the two apart: it reads a node's compliance status back from the taint, and it keeps other projects' volume copies off compliance nodes (see [Where database volumes go](#where-database-volumes-go)).

A workload that asks for a tag may run on any node that carries the tag, reserved or not. A workload that asks for no tag never runs on a reserved node. Reserved nodes also leave the capacity totals for general work, so the **Usage** page shows less room than the machines have.

## Choose a project's region

A project runs only on nodes in its region. A project without a region uses the cluster's default region, and without one of those it may use any node.

- **New project.** On the **Projects** page, choose a region in the menu next to **New project**. It lists **Default region (<name>)**, or **Any region** when the cluster has no default, and then each region. The menu appears once at least two regions exist. Through the API, send `region` to `POST /api/projects/create`.
- **Existing project.** Open the project's **Settings**, then **General**, and use **Region** under **Project Info**. Its first choice reads **Cluster default (<name>)**, or **Any region**. Choose **Update**. Services restart in the new region when it changes. The field appears once at least one region exists.
- **Default region.** On the **Usage** page, in **Regions**, choose **Default region**. "No default (any node)" removes it. The section appears once a node has a region.

A project cannot change region while it has an in-cluster database: its data stays on the nodes of the region where it was created. The **Region** field is locked, with the explanation "Fixed, because this project has databases and their data stays on the nodes of the region they were created in." Create a new project in the other region instead.

You can also set `spec.region` on the project, or `spec.defaultRegion` on the cluster:

```bash
kubectl -n geass-system patch geassproject morning-beach --type merge -p '{"spec":{"region":"eu"}}'
kubectl -n geass-system patch geasscluster default --type merge -p '{"spec":{"defaultRegion":"eu"}}'
```

Changing a project's region this way skips the dashboard's checks, including the one for databases. Do not move a project that has databases.

The dashboard refuses a region that no node has yet: "No node is in the eu region yet. Tag a node with it first".

## Choose node tags for a service or database

1. Open the service (or the database, before its first deploy) and choose **Settings**, then the **Scale** section.
2. Under **Run on**, tick the tags. Each tag shows how many nodes carry it in the project's region. A reserved tag reads "reserved: only for work that asks for it".
3. Save and choose **Deploy changes**.

With no tag ticked, any node in the region can run it. With several ticked, a node needs all of them. If the project also requires tags ([Compliance node tags](/docs/guides/compliance-tags/)), a node needs those as well. **Run on** appears only when nodes in the region carry tags. A tag that no node carries any more stays visible as "(no node has this tag now)" so that you can untick it. The dashboard refuses a tag that no node in the region has: "No node in the eu region is tagged storage".

## How a workload is placed

A workload may start on a node only when all of these hold:

1. The node carries the project's region label, if the project has a region.
2. The node carries every tag the workload asks for, and every tag its project requires.
3. The node is ready and not cordoned.
4. Every `NoSchedule` taint on the node is one the workload tolerates. A workload tolerates a reserved or compliance tag only if it asks for it or its project requires it.

Geass turns these rules into a node selector and tolerations on the pods:

| Workload | Region from | Tags | Notes |
| --- | --- | --- | --- |
| Services | The project, or the default region | **Run on**, applied on deploy, and the project's required tags | Pods get `topology.kubernetes.io/region` and `tag.geass.dev/<tag>` selectors. |
| In-cluster databases | The project | **Run on**, before first deploy, and the project's required tags | Decided once and recorded in `geass.dev/node-placement`. |
| Database backups, restores and logical-database setup | The database's region | The project's required tags only, never the tags the database chose | Short jobs run near the database without using nodes reserved for it, but never leave the nodes a project requires. |
| Builds from a Git repository | The project | The project's required tags only | The build job runs in the project's region. |
| Schedules of a function | The project | The project's required tags only | The same as builds. |
| Shared database servers | The server's own **Region**, or the default | `runOnTags` through the API; the setup dialog has no field for it | A project can use a server only in its own region, and, when the project requires tags, only a server that runs on nodes with all of them. |
| The cluster object storage server | Chosen when it is set up | Chosen when it is set up | Decided once. Its volume's copies follow them. |
| Geass itself, monitoring, storage | Not placed | No | The operator is kept on the control plane node. |

Running pods are never moved. A pod that restarts goes back only to a node that matches, so a change to a node's labels takes effect as pods restart or are redeployed.

### Where database volumes go

With the Longhorn storage add-on (the installer turns it on), a database in a region gets a volume class for that region, `geass-replicated-<region>`. Its copies stay on nodes in the region. Geass tags each Longhorn node with `geass-region-<region>` to do this. The number of copies is the number of usable nodes in the region, up to three. A highly available Postgres uses the single-copy class `geass-single`, which keeps its one copy on the node where the instance runs.

A database of a project that requires tags gets the class `geass-tagged-<hash>` instead. Its copies stay on nodes in the region that carry every required tag, which Geass tracks with a Longhorn node tag `geass-tag-<tag>`. Once any node is a compliance node, Geass also tags every other Longhorn node `geass-general` and makes the global and regional classes name that tag, so other projects' copies are never placed on a compliance node. See [Compliance node tags](/docs/guides/compliance-tags/).

The cluster object storage server is placed the same way, once, with its own region and tags, and its volume follows: with Longhorn, copies in its region (`geass-replicated-<region>`), or on the nodes that carry its tags (`geass-tagged-<hash>`), so its data is never replicated onto other nodes. See [Object storage](/docs/guides/object-storage/#where-the-servers-data-is-kept).

A new database in a region waits with "Storage is starting" until every node in the region (or, for required tags, every node that carries them) has a ready Longhorn manager and the class exists. Without Longhorn, volumes use the cluster's default storage class and stay on the node where the database first ran. See [Storage](/docs/guides/storage/).

## The capacity check

Before it creates a workload, Geass compares what the workload reserves with what the nodes it may use have free. If it does not fit, Geass refuses and says why.

### Where the check runs

- **In the dashboard**, when you deploy a pending change to a service or database, redeploy a service, roll one back, restore a database from a backup, set up a shared database server or the object storage server, or change the object storage server's size. The request fails with the message below. Creating a draft is not checked; the check runs when you deploy it.
- **In the operator**, when it creates a service's Deployment, a Postgres cluster, a MySQL or Redis release, a SQLite database or the object storage server. If it does not fit, the resource's `Ready` condition carries the message. It checks only creation: a workload that already exists is never refused, so a healthy service on a full cluster stays up.

Scaling replicas, autoscaling and draft services are not checked. A scale-up beyond the free capacity leaves the new pods waiting in `Pending`. Look at the **Usage** page before you raise replicas.

### What it adds up

The pool is the nodes the workload may use: its region, its tags and its project's required tags. The check multiplies the size by the number of copies: **Replicas**, or **Scale up to** when that is larger (the dashboard uses the larger; the operator uses **Replicas**). A highly available database counts three instances (the dashboard counts them only when `highAvailability` is set, the operator also when `instances` is 3 or more). It then compares the total with the free CPU and memory on the nodes the workload may use, which is the part of the cluster in its region and with its tags. It also checks that a single copy fits the largest of those nodes.

It is arithmetic over the whole pool, not a simulation of the scheduler. A workload can pass the check and still leave a pod in `Pending` if the free room is spread thinly over many nodes. In the other direction, the dashboard counts a redeploy of a running service as if it were new, so a redeploy can be refused on a nearly full cluster even though the operator would let it through. Free some room, or lower the size, and try again.

### The messages

| Message | Cause | Fix |
| --- | --- | --- |
| "This service needs about 250m CPU and 256Mi memory. The cluster currently has 100m CPU and 128Mi memory available. Scale up the cluster before creating it" | Not enough free CPU or memory in the pool. | Add a node, lower the size or replicas, or stop something. |
| "This service needs about 1 CPU and 2Gi memory per instance, but no schedulable node has enough allocatable capacity. Scale up with larger nodes" | One copy is larger than any node. | Choose a smaller size, or add a larger node. |
| "Cluster capacity is unknown, so this service cannot be scheduled yet. Scale up until schedulable nodes report CPU and memory" | No ready node has reported its size, or the node list could not be read. | Wait for nodes to become ready. See **Usage**. |
| "There is no ready node in the eu region to run this service. Add a node there, or choose another region" | The region or tags match no ready node. | Add or tag a node, or change the region or tags. |
| "The node in the eu region is reserved for other work, so this service has nowhere to run. Add a node, or release a node from its reservation" | Every matching node is reserved for a tag the workload does not ask for. | Switch the reservation off in **Edit**, ask for the tag, or add a node. |
| "This service needs about … Add larger nodes there" and "The eu region currently has … available. Add nodes there before creating it" | The regional versions of the first two messages. | The same fixes, within the region. |

The word "service" is the workload's kind, so you may read "PostgreSQL database" or "Object storage server" instead.

When the operator refuses, the dashboard shows the resource as **Unavailable**. For the first and third messages it adds the hint "There isn't enough capacity for this size. Choose a smaller size or add capacity in Platform settings." For the others it shows a generic line such as "This service isn't available yet. Check Platform settings or your configuration.", so read the exact message in the `Ready` condition:

```bash
kubectl -n geass-system get geassapp web \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}{"\n"}'
```

If the shortage lasts through five passes in a row (about two minutes), the resource becomes **Failed**, with the reason `ProvisionFailed`, and Geass stops trying. After you add capacity, choose **Retry** on the resource. See [Troubleshooting](/docs/operations/troubleshooting/).

## Add capacity

1. Add a node ([Add servers](/docs/getting-started/add-servers/)). To fix "no schedulable node has enough", add a larger one. To fix a regional shortage, give it the region and tags the workload needs.
2. Or release capacity: lower a size, lower replicas, delete something you do not use, switch off a reservation, or uncordon a node.
3. Check the **Usage** page: the new node appears in **Summary** once it is ready.

New capacity serves workloads that start afterwards. Geass does not move pods that are already running.

### Cordon and remove nodes

Geass has no cordon button. Use `kubectl`:

```bash
kubectl cordon node-2      # no new pods; running pods stay
kubectl uncordon node-2
```

A cordoned node shows **Unschedulable**. It is left out of the capacity totals, out of the [HA readiness](/docs/operations/high-availability/) count, and out of the number Geass uses to decide how many copies a Longhorn volume gets. To take a node out for good, delete its `GeassNode`: the operator removes the node from the cluster and, when it can reach the machine over SSH on port 22, uninstalls K3s from it. Data that lives only on that node goes with it.

## What happens under the hood

| Object | Where | What Geass puts there |
| --- | --- | --- |
| Node | cluster | Labels `topology.kubernetes.io/region`, `topology.kubernetes.io/zone`, `tag.geass.dev/<tag>=true`; taint `tag.geass.dev/<tag>=reserved:NoSchedule` for reserved tags, and `tag.geass.dev/<tag>=compliance:NoSchedule` for compliance tags. |
| Deployment | `<project>-<environment>` | A `nodeSelector` and `tolerations` from the placement. |
| HorizontalPodAutoscaler | `<project>-<environment>` | Named like the service, when autoscaling is on. |
| GeassDatabase | `geass-system` | The annotation `geass.dev/node-placement`, for example `{"region":"eu","tags":["storage"],"required":["hipaa"]}`. |
| StorageClass | cluster | `geass-replicated`, `geass-single`, `geass-replicated-<region>` for each region, and `geass-tagged-<hash>` for each set of required tags. |

```bash
kubectl get nodes -L topology.kubernetes.io/region,topology.kubernetes.io/zone
kubectl get nodes -o custom-columns='NODE:.metadata.name,TAINTS:.spec.taints[*].key'
kubectl get pods -n morning-beach-production -o wide        # which node each pod is on
kubectl -n geass-system get geassdatabase orders \
  -o jsonpath='{.metadata.annotations.geass\.dev/node-placement}'
kubectl -n longhorn-system get nodes.longhorn.io \
  -o custom-columns='NODE:.metadata.name,TAGS:.spec.tags'
kubectl get storageclass
```

## Troubleshooting

**A pod stays `Pending` and the service stays "Setting up".**
Run `kubectl -n <project>-<environment> describe pod <pod>` and read the events. "Insufficient cpu" or "Insufficient memory" means the nodes are full: see the **Usage** page. "didn't match Pod's node affinity/selector" means no node carries the region or tags the service asks for. "had untolerated taint" means the node is reserved for a tag the service does not ask for.

**The dashboard says "No node is in the eu region yet. Tag a node with it first".**
You chose a region that no node carries. Edit a node on the **Usage** page first.

**I cannot change a project's region.**
The project has an in-cluster database. See [Choose a project's region](#choose-a-projects-region).

**A database in a region sits at "Storage is starting".**
The regional storage class is created when every node in the region has a ready Longhorn manager. Check `kubectl -n longhorn-system get pods -o wide`. A node without the iSCSI initiator keeps its manager from becoming ready. See [Storage](/docs/guides/storage/) and [Troubleshooting](/docs/operations/troubleshooting/).

**A reserved node still runs other workloads.**
The taint keeps new work off. Pods that were already there stay until they restart.

**Deploy is refused for capacity but `kubectl` shows free room.**
The check uses the sum of requests, not live use, and counts the whole workload again on a redeploy. See [The capacity check](#the-capacity-check).

**The Run on section is missing.**
It shows only when nodes in the project's region carry tags. Tag a node first.

**A service stays `Pending` after its project required tags.**
No ready node in the region carries all the required tags, or none has room. See [Compliance node tags](/docs/guides/compliance-tags/#troubleshooting).

**Replicas return to my value after the autoscaler changed them.**
Geass writes **Replicas** back when it reconciles the service. See the caution under [Scale a service](#scale-a-service).

## Related

- [Sleep when idle](/docs/guides/scale-to-zero/)
- [Compliance node tags](/docs/guides/compliance-tags/)
- [Usage and capacity](/docs/guides/usage-and-capacity/)
- [Add servers](/docs/getting-started/add-servers/)
- [High availability](/docs/operations/high-availability/)
- [Storage](/docs/guides/storage/)
- [Nodes at more than one provider](/docs/operations/multi-provider/)
