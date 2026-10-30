---
title: "Scaling and placement"
description: "Scale a service with replicas and autoscaling, add nodes with node pools, and place work with regions and tags. Includes the capacity check."
---

Scaling decides how many copies of a service run and how much each copy reserves. Placement decides which of your servers the copies run on. You scale with these tools:

- Replicas.
- Sizes.
- Autoscaling.
- A service that can [sleep when idle](/docs/guides/scale-to-zero/).

When the servers run out of room, a [node pool](/docs/guides/node-pools/) can add machines for you. You place work with regions, sites, and tags that you attach to nodes. For data that must stay on certain machines, you also use [compliance tags](/docs/guides/compliance-tags/). Before anything new starts, Geass makes sure that the servers it can use have room for it.

## Before you start

- To change the region, site, and tags of a node, you need the admin role. A dashboard admin has it, and a workspace API token with the admin role has it. Project tokens cannot make this change. Viewers and observers can read, but they can change nothing.
- To select the nodes that a service or database runs on (**Run on**), you must be a signed-in admin. Geass refuses API tokens of every kind, so a script cannot send `runOnTags`.
- Placement matters only with more than one node. To add servers, see [Add servers](/docs/getting-started/add-servers/), or let a [node pool](/docs/guides/node-pools/) add them.
- To see how full your servers are, open the **Usage** page. See [Usage and capacity](/docs/guides/usage-and-capacity/).

## The words

| Word | Meaning |
| --- | --- |
| **Replica** | One running copy of a service. |
| **Size** | The CPU and memory that each replica reserves. |
| **Region** | A group of nodes that are close together, for example `eu` or `us`. A project that has a region runs only on nodes in that region. |
| **Site** | The provider or data centre inside a region, for example `hetzner-fsn1`. The dashboard calls it **Site**. The Kubernetes field is the zone. |
| **Tag** | A free-form label on a node that says what the node is for, for example `storage` or `gpu`. |
| **Reserved tag** | A tag that keeps off the node every workload that does not ask for the tag. |
| **Compliance tag** | A reserved tag for data that must stay on certain nodes, for example `hipaa`. Projects that require the tag run only on nodes that carry it, and nothing else runs on those nodes. See [Compliance node tags](/docs/guides/compliance-tags/). |
| **Required tag** | A tag set on a project (`spec.requiredTags`). Every workload of the project must run on nodes with this tag. |

Names of regions, sites, and tags have these rules:

- They use lowercase letters, numbers, and hyphens.
- They start and end with a letter or a number.
- They have at most 40 characters.

A node holds at most 16 tags. A service or a database can ask for at most 8 tags.

## Scale a service

1. Open the project, select the service, and select **Settings**.
2. Open the **Scale** section.
3. Change the values in the table.
4. Click **Save changes**.

| Setting | What it does | Default | Takes effect |
| --- | --- | --- | --- |
| **Replicas** | How many copies run. The form accepts 1 or more. The API accepts 0 to 1000. | 1 | Immediately |
| **CPU reserved** and **Memory reserved** (under **Resources**) | What each replica reserves. CPU: 0.1, 0.25, 0.5, 1, or 2. Memory: 128 MB, 256 MB, 512 MB, 1 GB, 2 GB, or 4 GB. | 0.1 CPU, 128 MB | On deploy |
| **CPU can use up to** and **Memory can use up to** | The most that a replica can use while its server has room. The value is one of the same sizes, and never below what is reserved. | Same as reserved | On deploy |
| **Run on** | Node tags that the service must run on. Shown only when nodes in the region of the project carry tags. | None | On deploy |
| **Enable autoscaling** | Adds copies when average CPU goes above a target. | Off | Immediately |
| **Scale up to** | The most copies that autoscaling can run. | 3 | Immediately |
| **CPU target %** | The average CPU, as a percentage of what the service reserves, that starts a new copy. 1 to 100. | 50 | Immediately |
| **Sleep when idle** | Stops the service when it gets no requests for some time, and starts it on the next request. It needs the serverless add-on and a public domain. You cannot use it together with autoscaling. See [Sleep when idle](/docs/guides/scale-to-zero/). | Off | Immediately |
| **Sleep after (minutes)** | How long the service gets no requests before Geass stops it. 1 to 1440. | 5 | Immediately |

Settings marked "On deploy" show as a pending change. To apply them, click **Deploy changes** in the top bar. Settings marked "Immediately" show an **Applies instantly** badge, and they need no deploy.

The scheduler and the capacity check count what a replica reserves. By default, this value is also the most that the replica can use, so the replica cannot use more than it reserved. To let a replica use more while its server has room, set **CPU can use up to** and **Memory can use up to** higher. For what this costs, see [Apps](/docs/guides/apps/#scale). In summary:

- Geass stops a replica that goes above what it can use.
- When its server runs out of memory, a replica that uses more than it reserved is the first replica to stop.

With autoscaling, the minimum number of copies is your **Replicas** value, and the maximum is **Scale up to**. The autoscaler measures CPU, so the cluster needs its metrics server. K3s includes the metrics server.

When you turn off **Enable autoscaling**, autoscaling stops. When you turn on **Sleep when idle**, Geass turns off autoscaling. When you turn on autoscaling, Geass turns off **Sleep when idle**.

Through the API, autoscaling is on in these cases:

- The request has `autoscaling=on`.
- The request carries a `targetCPU` with a `maxReplicas` above 1, even without the flag.

To turn off autoscaling through the API, send `maxReplicas=1`:

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
With autoscaling on, expect the replica count to go back to **Replicas** each time Geass reconciles the service. The autoscaler then moves the count again. Geass writes your **Replicas** value back into the Deployment at these times:

- When you deploy.
- When you save settings.
- When the operator restarts.
- Every 30 seconds while the service is not ready.

A service that sleeps when idle is different. Geass does not set its replica count at all while it sleeps. Only the add-on that starts and stops the service sets the count.
:::

Some services are set to run more than one replica, or can scale up to more than one. Such a service asks the scheduler to spread its replicas over machines. When every node has a Site, the service also asks the scheduler to spread them over sites. These requests are preferences:

- The service never refuses to run a replica.
- Geass creates no pod disruption budget.
- Two replicas can still be on the same node.
- Running replicas do not move.

The spread is a preference to the scheduler, not a guarantee. For what this means, see [High availability](/docs/operations/high-availability/#services).

### Change replicas from a script

A replica count never replaces pods, so the API applies it immediately. A project token can call this endpoint for its own services:

```bash
curl -sS -X POST https://geass.example.com/api/apps/web/scale \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d replicas=3
```

The count must be a whole number from 0 to 1000. This call changes only the replica count. Geass sets the autoscaling minimum when you save the **Scale** section. Thus the autoscaling minimum does not follow a count that you change with this call. On a service that sleeps when idle, the count has no effect until you turn off sleep.

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

A manifest without `deploy.enabled: true` does not run. The operator holds the service with the message "Service is configured and waiting for deployment". A service that sets no requests reserves 0.1 CPU and 128 MB.

## Scale the cluster

Replicas and autoscaling change how many copies of a service run. They do not add servers. When the nodes are full, the new pods wait in `Pending`. A [node pool](/docs/guides/node-pools/) closes that gap. An autoscaling pool does these things:

- It watches for pods that cannot start because there is not sufficient room.
- It creates a machine at a cloud, or it joins a server of yours.
- It removes the machine again when the machine is idle.

The two autoscalers work at different levels, and they work together:

| | Service autoscaling | Node autoscaling (a pool) |
| --- | --- | --- |
| What it changes | The number of copies of one service | The number of nodes in the cluster |
| What starts it | The average CPU of the service is above a target | Pods cannot start because no node has room, for the delay of the pool (30 seconds by default) |
| What limits it | **Scale up to** | The **most nodes** of the pool, which is also the limit on its cost |
| Who can set it up | Any admin, for a service | An admin, with a connection to a cloud. See [Node pools](/docs/guides/node-pools/) |

A service that scales up to more copies than the nodes can hold leaves pods that wait. If an autoscaling pool can host them, the pool adds a node, and the pods start there. The nodes of a pool whose data setting is `None` run services only. `None` is the default. A project with a region runs only on nodes of that region. Thus give the pool the region and tags of the projects that it must help.

## Place databases

An in-cluster database runs on nodes in the region of its project, and on nodes with the tags that you selected. Two things are different from services:

- Geass selects the nodes one time, when it first provisions the database. It records them on the database in the annotation `geass.dev/node-placement`. The volumes of the database are on those nodes. Thus later changes to the region of the project, the default region, or the tags do not move the database. The **Run on** choice in the **Settings** of the database closes after the first deploy.
- The number of instances comes from **High availability**, not from a replica field. See [High availability](/docs/operations/high-availability/).

Set the size of a database under **Settings**. These are the defaults for each instance:

- Postgres, MySQL, and Redis servers: 0.25 CPU and 512 MB. A high availability MySQL instance always gets 1 GB or more.
- SQLite: 0.1 CPU and 128 MB.

The object storage server of the cluster is not a database. Its default is 0.25 CPU and 512 MB with SeaweedFS, and 0.1 CPU and 256 MB with Garage. When you set it up, you select its size and the size of its volume. The default volume size is 10 GiB. See [Object storage](/docs/guides/object-storage/).

## Regions, sites and tags on nodes

### Set them in the dashboard

1. In the left rail, open **Usage**. The **Nodes** table lists every node.
2. On the node, click **Edit**. This button appears for admins.
3. Enter values in the **Region**, **Site**, and **Tags** fields. Separate tags with commas. The dialog suggests regions and tags that the cluster already uses.
4. To keep other work off the node, turn on **Reserve this node for** the tag. The switch appears for each tag that you entered.
5. For a tag that marks data that must stay on certain machines, turn on `<tag> is a compliance tag` under **Compliance tags**. A compliance tag is always reserved. See [Compliance node tags](/docs/guides/compliance-tags/).
6. Click **Save**. The dashboard shows "Node updated".

The dashboard warns you when the change leaves something with no node to run on, or changes who can use the node:

- "<project> has no node left in the <region> region, so its workloads cannot restart until one is added". Geass does not move running services, but they cannot restart.
- "<project> requires <tag>, and no other node carries it, so its workloads cannot restart until one does".
- "<tag> is now a compliance tag on this node. Workloads already running here keep running until they restart, so restart any that do not belong to a project requiring <tag>".

### Set them in a node manifest

When you add a worker with a `GeassNode`, declare its placement. The node then carries the correct labels from its first moment:

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

The operator writes `placement` onto the Kubernetes node and keeps it there. When you edit the node in the dashboard, the dashboard also updates the `GeassNode`, so the operator does not undo your edit. The operator does not touch a node whose `GeassNode` has no `placement`. Thus labels that you set by hand, or that a provider added, stay.

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

The **Nodes** table reads the same labels and taints back. With a workspace token that has the admin role, use this call:

```bash
curl -sS -X POST https://geass.example.com/api/nodes/node-2/placement \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d region=eu -d zone=hetzner-fsn1 -d tags=storage,compute,hipaa -d reserved=storage -d compliance=hipaa
```

The call replaces the whole placement of the node:

- The call clears a field that you leave out. Thus send the region, the site, and every tag that you want to keep.
- A reserved tag or a compliance tag must also be in `tags`.
- A compliance tag is reserved whether or not you list it under `reserved`.
- Geass makes a tag that a project requires into a compliance tag, whatever you send.

The response is `{"ok": true, "warnings": [...]}`. When there is nothing to warn about, `warnings` is `null`. From the command line, use `geass nodes tag`. See [CLI commands](/docs/reference/cli/).

:::caution
If the `GeassNode` of a node declares a `placement`, do not label that node by hand. Change the `GeassNode`, or use the dashboard. The operator removes the region, site, and `tag.geass.dev/` labels, and the reserved-tag taints, that the declaration does not list. The operator sees a change to the Kubernetes node when it happens, and it looks again every 10 minutes.
:::

### Reserved tags

A reserved tag puts the taint `tag.geass.dev/<tag>=reserved:NoSchedule` on the node. The taint keeps new workloads off the node, unless they ask for the tag. The taint does not evict the workloads that already run. Thus when you reserve a busy node, you change where workloads start next, not where they run now.

A compliance tag is a reserved tag whose taint value is `compliance`, as in `tag.geass.dev/hipaa=compliance:NoSchedule`. It has the same effect on scheduling. Geass uses the value to tell the two kinds apart. Geass reads the compliance status of a node back from the taint. Geass also keeps the volume copies of other projects off compliance nodes. See [Where database volumes go](#where-database-volumes-go).

A workload that asks for a tag can run on any node that carries the tag, reserved or not. A workload that asks for no tag never runs on a reserved node. Reserved nodes are also not in the capacity totals for general work. Thus the **Usage** page shows less room than the machines have.

## Choose a project's region

A project runs only on nodes in its region. A project without a region uses the default region of the cluster. If the cluster has no default region, the project can use any node.

- **New project.** On the **Projects** page, select a region in the menu next to **New project**. The menu lists **Default region (<name>)**, or **Any region** when the cluster has no default, and then each region. The menu appears when at least two regions exist. Through the API, send `region` to `POST /api/projects/create`.
- **Existing project.** Open the **Settings** of the project, then **General**. Use **Region** under **Project Info**. Its first choice is **Cluster default (<name>)**, or **Any region**. Click **Update**. When the region changes, services restart in the new region. The field appears when at least one region exists.
- **Default region.** On the **Usage** page, in **Regions**, select a value in **Default region**. "No default (any node)" removes the default. The section appears when a node has a region.

A project cannot change its region while it has an in-cluster database. The data of the database stays on the nodes of the region where Geass created it. The **Region** field is locked, with the explanation "Fixed, because this project has databases and their data stays on the nodes of the region they were created in." Instead, create a new project in the other region.

You can also set `spec.region` on the project, or `spec.defaultRegion` on the cluster:

```bash
kubectl -n geass-system patch geassproject morning-beach --type merge -p '{"spec":{"region":"eu"}}'
kubectl -n geass-system patch geasscluster default --type merge -p '{"spec":{"defaultRegion":"eu"}}'
```

A region change made in this way skips the checks of the dashboard, including the check for databases. Do not move a project that has databases.

The dashboard refuses a region that no node has yet: "No node is in the eu region yet. Tag a node with it first".

## Choose node tags for a service or database

1. Open the service, or the database before its first deploy. Select **Settings**, then the **Scale** section.
2. Under **Run on**, select the tags. Each tag shows how many nodes in the region of the project carry it. A reserved tag shows "reserved: only for work that asks for it".
3. Save the change.
4. Click **Deploy changes**.

With no tag selected, any node in the region can run the workload. With many tags selected, a node needs all of them. If the project also requires tags, a node needs those tags as well. See [Compliance node tags](/docs/guides/compliance-tags/). **Run on** appears only when nodes in the region carry tags. A tag that no node carries any more stays visible as "(no node has this tag now)", so that you can clear it. The dashboard refuses a tag that no node in the region has: "No node in the eu region is tagged storage".

## How a workload is placed

A workload can start on a node only when all of these conditions are true:

1. The node carries the region label of the project, if the project has a region.
2. The node carries every tag that the workload asks for, and every tag that its project requires.
3. The node is ready and not cordoned.
4. The workload tolerates every `NoSchedule` taint on the node. A workload tolerates a reserved tag or a compliance tag only if it asks for the tag or its project requires the tag. Only a service tolerates `geass.dev/ephemeral`, the taint of the nodes of a pool that hold no data.

Geass changes these rules into a node selector and tolerations on the pods:

| Workload | Region from | Tags | Notes |
| --- | --- | --- | --- |
| Services | The project, or the default region | **Run on**, applied on deploy, and the required tags of the project | Pods get `topology.kubernetes.io/region` and `tag.geass.dev/<tag>` selectors. While a [pool](/docs/guides/node-pools/#what-runs-on-a-pools-nodes) of nodes that hold no data exists, they also tolerate the taint `geass.dev/ephemeral`, so they can run on those nodes. |
| In-cluster databases | The project | **Run on**, before the first deploy, and the required tags of the project | Decided one time and recorded in `geass.dev/node-placement`. Never on the nodes of a pool that holds no data. |
| Database backups, restores, and logical-database setup | The region of the database | Only the required tags of the project, never the tags that the database selected | Short jobs run near the database, but they do not use nodes reserved for it. They never leave the nodes that a project requires. |
| Builds from a Git repository | The project | Only the required tags of the project | The build job runs in the region of the project. |
| Schedules of a function | The project | Only the required tags of the project | The same as builds. |
| Shared database servers | The own **Region** of the server, or the default | `runOnTags` through the API. The setup dialog has no field for it | A project can use a server only in its own region. When the project requires tags, it can use only a server that runs on nodes with all of those tags. |
| The cluster object storage server | Selected when it is set up | Selected when it is set up | Decided one time. The copies of its volume follow them. |
| Geass itself, monitoring, storage | Not placed | No | Geass keeps the operator on the control plane node. |

Geass never moves running pods. A pod that restarts goes back only to a node that matches. Thus a change to the labels of a node takes effect as pods restart or are redeployed.

### Where database volumes go

The installer turns on the Longhorn storage add-on. With this add-on, a database in a region gets a volume class for that region, `geass-replicated-<region>`. Its copies stay on nodes in the region. To do this, Geass tags each Longhorn node with `geass-region-<region>`. The number of copies is the number of usable nodes in the region, up to three. A highly available Postgres uses the single-copy class `geass-single`. This class keeps its one copy on the node where the instance runs.

A database of a project that requires tags gets the class `geass-tagged-<hash>` instead. Its copies stay on nodes in the region that carry every required tag. Geass tracks these nodes with a Longhorn node tag `geass-tag-<tag>`. When any node is a compliance node, Geass also tags every other Longhorn node `geass-general`. Geass then makes the global and regional classes name that tag. Thus Geass never puts the copies of other projects on a compliance node. See [Compliance node tags](/docs/guides/compliance-tags/).

Geass places the cluster object storage server in the same way, one time, with its own region and tags. Its volume follows it. With Longhorn, the copies are in its region (`geass-replicated-<region>`), or on the nodes that carry its tags (`geass-tagged-<hash>`). Thus Geass never replicates its data onto other nodes. See [Object storage](/docs/guides/object-storage/#where-the-servers-data-is-kept).

A new database in a region waits with "Storage is starting" until two conditions are true:

- Every node in the region has a ready Longhorn manager. For required tags, this applies to every node that carries the tags.
- The class exists.

Without Longhorn, volumes use the default storage class of the cluster. They stay on the node where the database first ran. See [Storage](/docs/guides/storage/).

## The capacity check

Before Geass creates a workload, it compares what the workload reserves with what the nodes that it can use have free. If the workload does not fit, Geass refuses it and says why.

### Where the check runs

- **In the dashboard.** The check runs when you do one of these actions:
  - Deploy a pending change to a service or a database.
  - Redeploy a service, or roll one back.
  - Restore a database from a backup.
  - Set up a shared database server or the object storage server.
  - Change the size of the object storage server.

  The request fails with one of the messages in The messages section. Geass does not check a draft when you create it. The check runs when you deploy the draft.
- **In the operator.** The check runs when the operator creates the Deployment of a service, a Postgres cluster, a MySQL or Redis release, a SQLite database, or the object storage server. If the workload does not fit, the `Ready` condition of the resource carries the message. The operator checks only creation. It never refuses a workload that already exists, so a healthy service on a full cluster stays up.

Geass does not check replica changes, autoscaling, or draft services. If you scale up beyond the free capacity, the new pods wait in `Pending`. Look at the **Usage** page before you increase replicas.

### What it adds up

The pool is the nodes that the workload can use: its region, its tags, and the required tags of its project. The check multiplies the size by the number of copies:

- The dashboard uses **Replicas**, or **Scale up to** when that value is larger.
- The operator uses **Replicas**.

A highly available database counts three instances. The dashboard counts three only when `highAvailability` is set. The operator also counts three when `instances` is 3 or more.

The check then compares the total with the free CPU and memory on the nodes that the workload can use. These nodes are the part of the cluster in its region and with its tags. The check also makes sure that a single copy fits on the largest of those nodes.

With an [autoscaling node pool](/docs/guides/node-pools/#the-capacity-check), the check asks one more question when the workload does not fit: does it fit if the pool grows to its maximum? If it does, Geass lets the workload in, and its pods wait for the nodes that the pool adds. Geass refuses the workload with the usual message in these cases:

- The pool is already at its maximum.
- No pool can host the workload.

A pool of nodes that hold no data counts for services only.

The check is arithmetic over the whole pool, not a simulation of the scheduler. A workload can pass the check and still leave a pod in `Pending`, if the free room is spread thinly over many nodes. In the other direction, the dashboard counts a redeploy of a running service as if it were new. Thus the dashboard can refuse a redeploy on a nearly full cluster, although the operator lets it through. Free some room, or lower the size, and try again.

### The messages

| Message | Cause | Fix |
| --- | --- | --- |
| "This service needs about 250m CPU and 256Mi memory. The cluster currently has 100m CPU and 128Mi memory available. Scale up the cluster before creating it" | Not sufficient free CPU or memory in the pool. | Add a node, lower the size or replicas, or stop a workload. |
| "This service needs about 1 CPU and 2Gi memory per instance, but no schedulable node has enough allocatable capacity. Scale up with larger nodes" | One copy is larger than any node. | Select a smaller size, or add a larger node. |
| "Cluster capacity is unknown, so this service cannot be scheduled yet. Scale up until schedulable nodes report CPU and memory" | No ready node has reported its size, or Geass could not read the node list. | Wait for nodes to become ready. See **Usage**. |
| "There is no ready node in the eu region to run this service. Add a node there, or choose another region" | The region or tags match no ready node. | Add or tag a node, or change the region or tags. |
| "The node in the eu region is reserved for other work, so this service has nowhere to run. Add a node, or release a node from its reservation" | Every matching node is reserved for a tag that the workload does not ask for. | Turn off the reservation in **Edit**, ask for the tag, or add a node. |
| "This service needs about … Add larger nodes there" and "The eu region currently has … available. Add nodes there before creating it" | The regional versions of the first two messages. | The same fixes, in the region. |

The word "service" is the kind of the workload. Thus you might see "PostgreSQL database" or "Object storage server" in its place.

When the operator refuses, the dashboard shows the resource as **Unavailable**. For the first and third messages, it adds the hint "Not enough capacity for this size. Choose a smaller one or add capacity." For the other messages, it shows a generic line, for example "This service isn't available yet.". Thus read the exact message in the `Ready` condition:

```bash
kubectl -n geass-system get geassapp web \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}{"\n"}'
```

If the shortage continues through five passes in a row (about two minutes), the resource becomes **Failed**, with the reason `ProvisionFailed`. Geass then stops its attempts. After you add capacity, click **Retry** on the resource. See [Troubleshooting](/docs/operations/troubleshooting/).

## Add capacity

1. Add a node, as [Add servers](/docs/getting-started/add-servers/) tells you, or let an autoscaling [node pool](/docs/guides/node-pools/) add it. To fix "no schedulable node has enough", add a larger node. To fix a regional shortage, give the node the region and tags that the workload needs.
2. Or release capacity. Lower a size, lower replicas, delete a workload that you do not use, turn off a reservation, or uncordon a node.
3. Look at the **Usage** page. The new node appears in **Summary** when it is ready.

New capacity serves workloads that start after it is added. Geass does not move pods that are already running.

### Cordon and remove nodes

Geass has no cordon button. Use `kubectl`:

```bash
kubectl cordon node-2      # no new pods; running pods stay
kubectl uncordon node-2
```

A cordoned node shows **Unschedulable**. Geass does not include it in these counts:

- The capacity totals.
- The [HA readiness](/docs/operations/high-availability/) count.
- The number that Geass uses to decide how many copies a Longhorn volume gets.

To remove a node permanently, delete its `GeassNode`. The operator removes the node from the cluster. When the operator can reach the machine over SSH on port 22, it also uninstalls K3s from the machine. Data that is only on that node is lost with it. Geass cordons and empties a machine that it created, and then deletes the machine at the cloud.

The autoscaler of a [node pool](/docs/guides/node-pools/#how-a-pool-removes-nodes) cordons the nodes that it is about to remove. It marks them with the annotation `geass.dev/pool-cordon`. The autoscaler uncordons only a node that it cordoned. A node that you cordoned stays as you left it, and the autoscaler never removes it.

## What happens under the hood

| Object | Where | What Geass puts there |
| --- | --- | --- |
| Node | cluster | Labels `topology.kubernetes.io/region`, `topology.kubernetes.io/zone`, and `tag.geass.dev/<tag>=true`. The taint `tag.geass.dev/<tag>=reserved:NoSchedule` for reserved tags, and `tag.geass.dev/<tag>=compliance:NoSchedule` for compliance tags. |
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
Run `kubectl -n <project>-<environment> describe pod <pod>` and read the events:

- "Insufficient cpu" or "Insufficient memory" means that the nodes are full. See the **Usage** page.
- "didn't match Pod's node affinity/selector" means that no node carries the region or tags that the service asks for.
- "had untolerated taint" means that the node is reserved for a tag that the service does not ask for.

**The dashboard says "No node is in the eu region yet. Tag a node with it first".**
You selected a region that no node carries. Edit a node on the **Usage** page first.

**I cannot change a project's region.**
The project has an in-cluster database. See [Choose a project's region](#choose-a-projects-region).

**A database in a region sits at "Storage is starting".**
Geass creates the regional storage class when every node in the region has a ready Longhorn manager. Check `kubectl -n longhorn-system get pods -o wide`. If a node does not have the iSCSI initiator, its manager cannot become ready. See [Storage](/docs/guides/storage/) and [Troubleshooting](/docs/operations/troubleshooting/).

**A reserved node still runs other workloads.**
The taint keeps new work off. Pods that were already there stay until they restart.

**Deploy is refused for capacity but `kubectl` shows free room.**
The check uses the sum of requests, not live use. On a redeploy, it counts the whole workload again. See [The capacity check](#the-capacity-check).

**The Run on section is missing.**
The section shows only when nodes in the region of the project carry tags. Tag a node first.

**A service stays `Pending` after its project required tags.**
No ready node in the region carries all of the required tags, or no such node has room. See [Compliance node tags](/docs/guides/compliance-tags/#troubleshooting).

**Replicas return to my value after the autoscaler changed them.**
Geass writes **Replicas** back when it reconciles the service. See the caution under [Scale a service](#scale-a-service).

**Pods wait in `Pending` and no node is added.**
A pool adds nodes, and a pool exists only if someone made one. If a pool exists, see [Node pools](/docs/guides/node-pools/#troubleshooting). Its region and tags must match the region and tags of the project, and it must be below its maximum.

## Related

- [Sleep when idle](/docs/guides/scale-to-zero/)
- [Compliance node tags](/docs/guides/compliance-tags/)
- [Usage and capacity](/docs/guides/usage-and-capacity/)
- [Node pools and autoscaling](/docs/guides/node-pools/)
- [Add servers](/docs/getting-started/add-servers/)
- [High availability](/docs/operations/high-availability/)
- [Storage](/docs/guides/storage/)
- [Nodes at more than one provider](/docs/operations/multi-provider/)
