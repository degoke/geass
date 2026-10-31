---
title: "Scaling and placement"
description: "Scale a service with replicas and autoscaling, add servers with node pools, and place work with regions and tags. Includes the capacity check."
---

Scaling decides how many copies of a service run, and how much each copy reserves. Placement decides which of your servers the copies run on. You scale with these tools:

- Replicas.
- Sizes.
- Autoscaling.
- [Sleep when idle](/docs/guides/scale-to-zero/).

When your servers are full, a [node pool](/docs/guides/node-pools/) can add servers for you. To place work, you give servers a region, a site, and tags. For data that must stay on certain servers, use [compliance tags](/docs/guides/compliance-tags/). Before anything new starts, Geass makes sure that there is room for it.

## Before you start

- To change the region, site, and tags of a server, you need the admin role. Viewers and observers can only read them.
- To select **Run on** tags for a service or database, you must be a signed-in admin. API tokens cannot set them.
- Placement matters only when you have more than one server. To add servers, see [Add servers](/docs/getting-started/add-servers/).
- To see how full your servers are, open the **Usage** page. See [Usage and capacity](/docs/guides/usage-and-capacity/).

## The words

| Word | Meaning |
| --- | --- |
| **Replica** | One running copy of a service. |
| **Size** | The CPU and memory that each copy reserves. |
| **Region** | A group of servers that are close together, for example `eu` or `us`. A project with a region runs only on servers in that region. |
| **Site** | The provider or data center in a region, for example `hetzner-fsn1`. |
| **Tag** | A label on a server that says what the server is for, for example `storage` or `gpu`. |
| **Reserved tag** | A tag that keeps off the server all work that does not ask for the tag. |
| **Compliance tag** | A reserved tag for data that must stay on certain servers, for example `hipaa`. See [Compliance node tags](/docs/guides/compliance-tags/). |
| **Required tag** | A tag on a project. All the work of the project must run on servers with this tag. |

The names of regions, sites, and tags have these rules:

- They use lowercase letters, numbers, and hyphens.
- They start and end with a letter or a number.
- They have 40 characters or fewer.

A server can have up to 16 tags. A service or a database can ask for up to 8 tags.

## Scale a service

1. Open the project, select the service, and select **Settings**.
2. Open the **Scale** section.
3. Change the values.
4. Click **Save changes**.

| Setting | What it does | Default | Applies |
| --- | --- | --- | --- |
| **Replicas** | How many copies run. The form accepts 1 or more. | 1 | Immediately |
| **CPU reserved**, **Memory reserved** (under **Resources**) | What each copy reserves. CPU: 0.1, 0.25, 0.5, 1, or 2. Memory: 128 MB, 256 MB, 512 MB, 1 GB, 2 GB, or 4 GB. | 0.1 CPU, 128 MB | On deploy |
| **CPU can use up to**, **Memory can use up to** | The most that a copy can use while its server has room. It is never less than what is reserved. | Same as reserved | On deploy |
| **Run on** | The server tags that the service must run on. It appears only when servers in the region of the project have tags. | None | On deploy |
| **Enable autoscaling** | Adds copies when the average CPU goes above a target. | Off | Immediately |
| **Scale up to** | The most copies that autoscaling can run. | 3 | Immediately |
| **CPU target %** | The average CPU, as a percent of what the service reserves, that adds a copy. 1 to 100. | 50 | Immediately |
| **Sleep when idle** | Stops the service when it gets no requests, and starts it on the next request. See [Sleep when idle](/docs/guides/scale-to-zero/). | Off | Immediately |
| **Sleep after (minutes)** | How long the service gets no requests before it stops. 1 to 1440. | 5 | Immediately |

A setting that applies on deploy shows as a pending change. To apply it, click **Deploy changes** in the top bar. A setting that applies immediately shows an **Applies instantly** badge.

By default, a copy cannot use more than it reserves. To let a copy use more while its server has room, set **CPU can use up to** and **Memory can use up to** higher. Know these two results. For more, see [Apps](/docs/guides/apps/#scale).

- Geass stops a copy that goes above what it can use.
- When a server runs out of memory, a copy that uses more than it reserved stops first.

### Autoscaling

With autoscaling, the fewest copies is **Replicas**, and the most is **Scale up to**. You cannot use autoscaling and **Sleep when idle** together. When you turn on one, Geass turns off the other.

:::caution
With autoscaling on, expect the number of copies to go back to **Replicas** for a short time. This happens when you deploy, when you save settings, and when Geass restarts. It also happens every 30 seconds while the service is not ready. Then autoscaling changes the number again.
:::

### Spread over servers

A service that can run more than one copy asks to spread its copies over servers. When every server has a site, it also asks to spread them over sites. This is a preference, not a guarantee:

- Two copies can still run on the same server.
- Running copies do not move.

See [High availability](/docs/operations/high-availability/#services).

### From the command line

```bash
geass scale web 3
```

The number is 0 to 1000, and it applies immediately. A project token can scale the services of its own project. This command changes only **Replicas**, not the autoscaling settings. On a service that sleeps when idle, the number has no effect until you turn off sleep. See [CLI commands](/docs/reference/cli/).

From a script, call `POST /api/apps/<service>/scale` with `replicas`. To turn off autoscaling, call `POST /api/apps/<service>/update` with `autoscaling=` and `maxReplicas=1`. See the [REST API](/docs/reference/rest-api/).

## Scale the cluster

Replicas and autoscaling do not add servers. When the servers are full, new copies wait. A [node pool](/docs/guides/node-pools/) can fix this. An autoscaling pool does these things:

- It sees copies that cannot start because no server has room.
- It creates a machine at a cloud, or joins a server of yours.
- It removes the machine when the machine is idle.

| | Service autoscaling | Server autoscaling (a pool) |
| --- | --- | --- |
| What it changes | The number of copies of one service | The number of servers |
| What starts it | The average CPU of the service is above the target | Copies cannot start because no server has room, for 30 seconds by default |
| What limits it | **Scale up to** | The most servers of the pool, which also limits the cost |
| Who sets it up | An admin, for each service | An admin, with a connection to a cloud |

A pool helps only the projects whose region and tags it matches. So give the pool the region and tags of the projects that it must help.

## Place databases

A database runs on servers in the region of its project, with the tags that you selected. Two things are different from services:

- Geass selects the servers one time, when it first creates the database. The data stays on those servers. Later changes to regions or tags do not move the database. So **Run on** in the **Settings** of a database closes after the first deploy.
- **High availability** sets the number of instances, not **Replicas**. See [High availability](/docs/operations/high-availability/).

Set the size of a database under **Settings**. The default for each instance is 0.25 CPU and 512 MB for Postgres, MySQL, and Redis. A highly available MySQL instance always gets 1 GB or more. The default for SQLite is 0.1 CPU and 128 MB.

The object storage server is also placed one time, when you set it up. Its default is 0.25 CPU and 512 MB with SeaweedFS, and 0.1 CPU and 256 MB with Garage. Its default volume is 10 GiB. See [Object storage](/docs/guides/object-storage/).

## Regions, sites and tags on servers

### Set them in the dashboard

1. In the left rail, open **Usage**. The **Nodes** table lists every server.
2. On the server, click **Edit**.
3. Enter values in **Region**, **Site**, and **Tags**. Separate tags with commas. The dialog suggests the regions and tags that you already use.
4. To keep other work off the server, turn on **Reserve this node for** the tag.
5. For a tag that marks data that must stay on certain servers, turn on `<tag> is a compliance tag` under **Compliance tags**. A compliance tag is always reserved.
6. Click **Save**. The dashboard shows "Node updated".

The dashboard warns you when a change leaves something with no server to run on:

- "<project> has no node left in the <region> region, so its workloads cannot restart until one is added".
- "<project> requires <tag>, and no other node carries it, so its workloads cannot restart until one does".
- "<tag> is now a compliance tag on this node. Workloads already running here keep running until they restart, so restart any that do not belong to a project requiring <tag>".

Geass does not move running services. A change applies when a service restarts or deploys.

### From the command line

```bash
geass nodes tag node-2 --region eu --zone hetzner-fsn1 --tags storage,compute --reserved storage
```

The command replaces all the placement of the server. So give the region, the site, and every tag that you want to keep. A reserved tag or compliance tag must also be in `--tags`. See [CLI commands](/docs/reference/cli/).

### Reserved tags

A reserved tag keeps new work off the server, unless the work asks for the tag. Work that already runs on the server stays. So when you reserve a busy server, you change where work starts next, not where it runs now.

Work that asks for a tag can run on any server with the tag, reserved or not. Work that asks for no tag never runs on a reserved server. Reserved servers are not in the totals of the **Usage** page. So the page shows less room than the machines have.

A compliance tag works the same way. Geass also keeps the data copies of other projects off a compliance server. See [Compliance node tags](/docs/guides/compliance-tags/).

## Choose a project's region

A project runs only on servers in its region. A project without a region uses the default region. If there is no default region, the project can use any server.

- **New project.** On the **Projects** page, select a region in the menu next to **New project**. The menu appears when two or more regions exist.
- **Existing project.** Open the **Settings** of the project, and then **General**. Under **Project Info**, select a **Region**, and click **Update**. The services restart in the new region.
- **Default region.** On the **Usage** page, under **Regions**, select a **Default region**. "No default (any node)" removes the default.

You cannot change the region of a project that has a database in the cluster. The data stays in the region where Geass created it. The **Region** field says "Fixed, because this project has databases and their data stays on the nodes of the region they were created in." To move, create a new project in the other region.

The dashboard refuses a region that no server has: "No node is in the eu region yet. Tag a node with it first".

## Choose server tags for a service or database

1. Open the service, or a database before its first deploy.
2. Select **Settings**, and then the **Scale** section.
3. Under **Run on**, select the tags. Each tag shows how many servers in the region have it. A reserved tag shows "reserved: only for work that asks for it".
4. Save the change.
5. Click **Deploy changes**.

With no tag, any server in the region can run the work. With several tags, a server needs all of them. If the project requires tags, a server needs those tags too. A tag that no server has any more shows "(no node has this tag now)", so that you can clear it. The dashboard refuses a tag that no server in the region has: "No node in the eu region is tagged storage".

## How work is placed

Work can start on a server only when all of these are true:

1. The server is in the region of the project, if the project has one.
2. The server has every tag that the work asks for, and every tag that the project requires.
3. The server is ready, and not **Cordoned**.
4. The server has no reserved tag that the work does not ask for.

| Work | Region | Tags |
| --- | --- | --- |
| Services | The project, or the default region | **Run on**, and the required tags of the project. Services can also run on the **Ephemeral** servers of a [pool](/docs/guides/node-pools/#what-runs-on-a-pools-nodes). |
| Databases | The project | **Run on**, and the required tags of the project. Decided one time. Never on **Ephemeral** servers. |
| Backups, restores, and database setup | The region of the database | Only the required tags of the project |
| Builds and function schedules | The project | Only the required tags of the project |
| Shared database servers | Their own **Region**, or the default | Through the API only (`runOnTags`) |
| The object storage server | Selected when you set it up | Selected when you set it up |

Geass never moves running work. A change to the tags of a server applies when work restarts or deploys.

### Where database volumes go

With the storage add-on that the installer turns on, the data copies of a database stay in its region. A database gets up to three copies, one for each usable server in the region. The copies of a project that requires tags stay on servers with those tags. Geass keeps the copies of other projects off compliance servers.

A new database in a region shows "Storage is starting" until the storage add-on is ready on every server of the region. See [Storage](/docs/guides/storage/).

## The capacity check

Before Geass creates new work, it compares what the work reserves with what is free on the servers that it can use. If the work does not fit, Geass refuses it and says why.

### When the check runs

The dashboard checks when you do one of these actions:

- Deploy a pending change to a service or a database.
- Redeploy a service, or roll it back.
- Restore a database from a backup.
- Set up a shared database server or the object storage server.
- Change the size of the object storage server.

Geass checks again when it creates the service or database. It never stops work that already runs, so a healthy service on a full cluster stays up.

Geass does not check a change to **Replicas** or autoscaling. If you add copies past the free room, the new copies wait. Look at the **Usage** page before you add copies.

### What it adds up

The check multiplies the size by the number of copies. The dashboard uses **Replicas**, or **Scale up to** when it is larger. A highly available database counts three instances. The check then compares the total with the free CPU and memory of the servers that the work can use. It also makes sure that one copy fits on the largest of those servers.

With an [autoscaling node pool](/docs/guides/node-pools/#the-capacity-check), Geass also asks if the work fits when the pool grows to its most servers. If it does, Geass lets the work in, and it waits for the new servers. Geass refuses the work when the pool is at its most, or when no pool can host the work.

The check is a sum, not an exact plan. Work can pass the check and still wait, if the free room is spread over many servers. The dashboard also counts a redeploy as new work. So on a nearly full cluster, the dashboard can refuse a redeploy. Make room free or lower the size, and try again.

### The messages

| Message | Cause | Fix |
| --- | --- | --- |
| "This service needs about 250m CPU and 256Mi memory. The cluster currently has 100m CPU and 128Mi memory available. Scale up the cluster before creating it" | Not enough free CPU or memory. | Add a server, lower the size or copies, or stop other work. |
| "This service needs about 1 CPU and 2Gi memory per instance, but no schedulable node has enough allocatable capacity. Scale up with larger nodes" | One copy is larger than any server. | Select a smaller size, or add a larger server. |
| "Cluster capacity is unknown, so this service cannot be scheduled yet. Scale up until schedulable nodes report CPU and memory" | No server has reported its size yet. | Wait for the servers to become ready. See **Usage**. |
| "There is no ready node in the eu region to run this service. Add a node there, or choose another region" | No ready server has the region or tags. | Add or tag a server, or change the region or tags. |
| "The node in the eu region is reserved for other work, so this service has nowhere to run. Add a node, or release a node from its reservation" | Every matching server is reserved for a tag that the work does not ask for. | Turn off the reservation in **Edit**, ask for the tag, or add a server. |
| "This service needs about … Add larger nodes there" and "The eu region currently has … available. Add nodes there before creating it" | The same shortages, in a region. | The same fixes, in the region. |

Instead of "service", a message can say "PostgreSQL database" or "Object storage server".

When Geass refuses work after the dashboard check, the work shows **Unavailable**. For a shortage, the dashboard adds "Not enough capacity for this size. Choose a smaller one or add capacity." After about two minutes of shortage, the work shows **Failed**, and Geass stops. When you have added room, click **Retry**. See [Troubleshooting](/docs/operations/troubleshooting/).

## Add capacity

1. Add a server. See [Add servers](/docs/getting-started/add-servers/), or let an autoscaling [node pool](/docs/guides/node-pools/) add it. For a regional shortage, give the server the region and tags that the work needs.
2. Or make room free. Lower a size or the copies, delete work that you do not use, or turn off a reservation.
3. Open the **Usage** page. The new server appears in **Summary** when it is ready.

New room serves work that starts after you add it. Geass does not move running work.

### Take a server out of use

To stop new work on a server, cordon it. Open **Usage**, open the menu of the server in **Nodes**, and select **Cordon**. From a terminal, run `geass nodes cordon NODE`. The services on the server continue to run. A cordoned server shows **Cordoned**, and it is not in the totals. A pool never removes a server that you cordoned. To allow new work again, select **Uncordon**, or run `geass nodes uncordon NODE`. See [Cordon a server](/docs/guides/usage-and-capacity/#cordon-a-server).

To remove a server, open its menu on the **Usage** page, and select **Remove**. See [Usage and capacity](/docs/guides/usage-and-capacity/#add-or-remove-a-server). Data that is only on that server is lost.

## Troubleshooting

**A service stays at "Setting up".**
The service has no server to run on. See [Under the hood](#under-the-hood) to read the reason. These are the usual causes:

- The servers are full. See the **Usage** page.
- No server has the region or tags that the service asks for.
- The only matching server is reserved for a tag that the service does not ask for.

**The dashboard says "No node is in the eu region yet. Tag a node with it first".**
No server has that region. Edit a server on the **Usage** page first.

**You cannot change the region of a project.**
The project has a database in the cluster. See [Choose a project's region](#choose-a-projects-region).

**A database in a region stays at "Storage is starting".**
The storage add-on is not ready on every server of the region. See [Storage](/docs/guides/storage/) and [Troubleshooting](/docs/operations/troubleshooting/).

**A reserved server still runs other work.**
A reservation keeps new work off. Work that was already there stays until it restarts.

**A deploy is refused for room, but the servers look idle.**
The check counts what is reserved, not what is used. A redeploy counts the whole service again. See [The capacity check](#the-capacity-check).

**The Run on section is missing.**
It appears only when servers in the region of the project have tags. Tag a server first.

**A service waits after its project required tags.**
No ready server in the region has all the required tags, or none has room. See [Compliance node tags](/docs/guides/compliance-tags/#troubleshooting).

**Replicas go back to your value after autoscaling changed them.**
See the caution in [Autoscaling](#autoscaling).

**Services wait, and no server is added.**
Only a pool adds servers. If you have a pool, its region and tags must match the project, and it must be below its most servers. See [Node pools](/docs/guides/node-pools/#troubleshooting).

## Under the hood

A region is the node label `topology.kubernetes.io/region`, and a site is `topology.kubernetes.io/zone`. A tag is `tag.geass.dev/<tag>=true`. A reserved tag adds the taint `tag.geass.dev/<tag>=reserved:NoSchedule`, and a compliance tag adds `tag.geass.dev/<tag>=compliance:NoSchedule`. Autoscaling uses a HorizontalPodAutoscaler. A database records its placement in the annotation `geass.dev/node-placement`.

If a `GeassNode` declares a `placement`, do not label that node by hand. Geass removes the labels that the declaration does not list. Change the `GeassNode`, or use the dashboard.

**Cordon** runs the same change as `kubectl cordon <node>`, and **Uncordon** runs `kubectl uncordon <node>`.

To see why a service waits, read the events of its pod:

```bash
kubectl get pods -n <project>-<environment> -o wide
kubectl -n <project>-<environment> describe pod <pod>
```

- "Insufficient cpu" or "Insufficient memory" means that the servers are full.
- "didn't match Pod's node affinity/selector" means that no server has the region or tags.
- "had untolerated taint" means that the server is reserved for another tag.

To read the exact capacity message of a service, and to see placement, run these commands:

```bash
kubectl -n geass-system get geassapp web -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}{"\n"}'
kubectl get nodes -L topology.kubernetes.io/region,topology.kubernetes.io/zone
kubectl get nodes -o custom-columns='NODE:.metadata.name,TAINTS:.spec.taints[*].key'
kubectl -n longhorn-system get pods -o wide
```

## Related

- [Sleep when idle](/docs/guides/scale-to-zero/)
- [Compliance node tags](/docs/guides/compliance-tags/)
- [Usage and capacity](/docs/guides/usage-and-capacity/)
- [Node pools and autoscaling](/docs/guides/node-pools/)
- [Add servers](/docs/getting-started/add-servers/)
- [High availability](/docs/operations/high-availability/)
- [Storage](/docs/guides/storage/)
- [Nodes at more than one provider](/docs/operations/multi-provider/)
