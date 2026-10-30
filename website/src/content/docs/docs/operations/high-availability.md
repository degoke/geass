---
title: "High availability"
description: "High availability in Geass: three control plane servers, HA databases, what survives the loss of a node, and what is still a single point of failure."
---

Geass has two high availability features today:

- A database can run as three instances on three nodes. Thus the database continues to serve when one node is lost.
- The control plane can run on three servers. Thus the API and the data of the cluster continue to work after the loss of one server.

Neither feature makes your services, ingress, or object storage highly available by itself. This page tells you what each feature does and how to set it up. It also tells you clearly what is still a single point of failure.

## What is and is not highly available

| Part | Highly available? | Notes |
| --- | --- | --- |
| Postgres with **High availability** | Yes, with three instances | Replicas and failover come from the Postgres operator (CloudNativePG). Needs a minimum of three healthy nodes. |
| MySQL with **High availability** | Yes, with three or five instances | Replicas and failover come from the MySQL operator (MOCO). Needs a minimum of three healthy nodes. See [MySQL](#mysql). |
| MariaDB with **High availability** | Yes, with three or five instances | Replicas and failover come from the MariaDB operator, with a trigger from Geass for a machine that stops answering. Needs a minimum of three healthy nodes. See [MariaDB](#mariadb). |
| Redis | No | One instance. It refuses **High availability**. See [Redis](#redis). |
| Services | Only as far as replicas go | A service with more than one replica asks the scheduler to put the replicas on different machines, and then at different sites, where possible. See [Services](#services). |
| Control plane: the Kubernetes API and etcd | Yes, with three servers | Join two more servers to the first server. The cluster works with one server lost, and stops with two lost. See [Run three control plane servers](#run-three-control-plane-servers). |
| The operator and the dashboard | Restarts on another server | One pod. When its server is lost, the pod starts on another server in one to two minutes. During that time, nothing is reconciled and the dashboard is down. |
| Single-instance databases (including MySQL and MariaDB without high availability), the object storage server, SQLite | No | One instance each. |

## Run three control plane servers

The control plane is the Kubernetes API, the database that holds every object in the cluster (etcd), and the Geass operator. On a new install, the control plane is one K3s server. You can join two more servers. Then the API and the data of the cluster are on three machines, and they continue to work after the loss of one machine.

**What you get.** Each of the three servers holds a copy of the database of the cluster, and each server answers the API. These are the results of a loss:

- With one server lost, the API continues to answer from the other two servers. Reads and writes continue to work, and all of the data of the cluster is there.
- With two servers lost, the database has no majority. The cluster stops answering until one server comes back.
- In both cases, containers that already run on a node continue to run.

**What you do not get.** Geass runs one operator. The operator deploys, scales, reconciles, and serves the dashboard. The operator is a pod, and the installer puts its image on every server that you join. Thus when the server of the operator is lost, the operator starts on another server. This takes between 75 seconds and two minutes. In tests, it took 76 to 112 seconds, and 87 seconds on the last real three-server cluster. The time has these parts:

- Kubernetes decides that a node has stopped answering after about 40 seconds. The time is longer when the lost server also held the lock that the controllers of Kubernetes run under. The reason is that the next controller starts to count from when it takes over.
- Kubernetes then gives the pod of the operator 30 more seconds before it moves the pod.
- The new pod starts in a few seconds, because the image is already there.

Until the new pod starts, nothing is deployed or reconciled, and the dashboard is down. Two operators would not make this faster, and would double the memory. Thus there is one operator.

### Before you start

- A cluster that the installer built. Its K3s runs with embedded etcd (`--cluster-init`), and embedded etcd lets a second server join.
  - A K3s that was started in another way, or that uses an external database, cannot take a server this way.
  - The installer starts the first server with K3s secrets encryption on. K3s refuses a server that joins with a different setting. A join to a cluster that has secrets encryption off fails on the new machine. Its K3s writes `critical configuration mismatched: secrets-encryption` to `journalctl -u k3s` and starts again in a loop. The installer stops only after ten minutes, with "this server did not join".
- Two more machines that meet the [requirements](/docs/getting-started/requirements/), on the **same private network** as the first server, with fast links. Servers send etcd traffic to each other all the time. Thus keep them at one site, and do not use [multi-site mode](/docs/operations/multi-provider/) for them. The installer refuses a join with `GEASS_MULTI_SITE` set.
- **Three servers, not two.** A cluster needs a majority of its servers to answer. With three servers, the cluster continues after the loss of one server. With two servers, it cannot continue after any loss: the loss of either server stops it. Also, two servers fail more often than one server. Use three servers, or use five servers to continue after the loss of two.
- The ports in the next table must be open between the servers, in the host firewall and in the security group of the provider. If the installer finds a host firewall on the machine that joins, it opens the ports there. Before it changes anything, it checks that it can reach the ports on the server that it joins. **It cannot open them on the first server**, so open them there yourself:

| Port | Between | What it is for |
| --- | --- | --- |
| 6443/tcp | Every node and every server | The Kubernetes API |
| 2379/tcp and 2380/tcp | The servers | etcd, the database of the cluster: its clients and its peers |
| 8472/udp | Every node | The VXLAN overlay |
| 10250/tcp | Every node | The kubelet |

```bash
sudo ufw allow 2379/tcp && sudo ufw allow 2380/tcp     # on each server, if ufw is active
```

### Join a server

1. On the **first** server, read the token that lets a server join, and the K3s release that the cluster runs:

   ```bash
   sudo cat /var/lib/rancher/k3s/server/token
   sudo k3s --version
   ```

2. On the **new** machine, as root, run the installer with the address of the first server, that token, and that K3s release. Give the token in the environment, because the process list does not show the environment. Join one machine at a time. Wait for each machine to finish before you start the next:

   ```bash
   curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | \
     sudo env GEASS_JOIN_SERVER=https://10.0.0.5:6443 GEASS_JOIN_TOKEN='K10…::server:…' \
     GEASS_K3S_VERSION=v1.36.5+k3s1 sh
   ```

   A server must run the same K3s as the other servers. A machine that has not joined cannot ask the cluster for the K3s version. K3s answers questions about its version only to clients that it knows, and a join token is not one of these clients. Thus the installer asks you for the version, with `GEASS_K3S_VERSION`, or with `--k3s-version` after `sh -s --`. After the new server has joined, the installer compares its version with the other servers of the cluster. If the versions are different, the installer stops and tells you what to do.

   Before the join, the installer does these steps:

   1. It checks the machine, the address, and the two database ports.
   2. It installs K3s as a server with the same settings as the first server.
   3. It waits for the new node to be a ready control plane node.
   4. It imports the image of the operator.

   The installer does not touch a machine that already has K3s. It refuses that machine, and it never replaces a K3s.

3. Check that the cluster has the servers:

   ```bash
   sudo k3s kubectl get nodes -L node-role.kubernetes.io/control-plane,node-role.kubernetes.io/etcd
   ```

   Each server shows `true` in both columns. The **Usage** page also lists the servers, and counts them in **Healthy nodes**.

### What each server keeps current

You update Geass on the first server, as always. After the update, the Deployment of the operator names a new image. That image is in no registry. It is inside the installer, and the installer puts it only on the machine that the installer runs on. The pod of the operator can start on any control plane server. Thus **before you update the first server, run the new release's installer on each of the other servers with no options**:

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo sh
```

On a server that joined a cluster, the installer does only one thing. It puts the new image of the operator on the server, next to the old image, and changes nothing else. It does not update the cluster. It refuses `--upgrade`, `--reinstall`, and every other option that would change a cluster.

The order is important. If you update the first server before the other servers have the image, these events can occur:

1. The scheduler puts the new operator on one of the other servers.
2. The new operator cannot pull the image.
3. The installer sees that the pod cannot pull its image. It stops, and puts the previous operator back.

Nothing is lost, but the update did not occur. Before the installer starts, it tells you which servers do not report the image yet. A server that you refreshed a moment ago can show in that list for some seconds, until its node reports the image. If you have refreshed that server, continue.

To move the K3s of the cluster to a new release, do these steps:

1. Use `--k3s-version` on the first server as usual.
2. Upgrade each of the other servers **one at a time**, with the install script of K3s and the version that you gave the first server. Wait for each node to be ready before you start the next.

The server keeps its settings, because they are in its configuration file:

```bash
curl -sfL https://get.k3s.io | sudo env INSTALL_K3S_VERSION=v1.36.5+k3s1 INSTALL_K3S_EXEC=server sh -
```

Geass does not move the workers until the oldest control plane server runs the new version. The reason is that a worker must not be newer than the API servers that it talks to. The cluster object tells you which server Geass waits for.

### Take a server away

Do not stop two servers of three, because the cluster stops. To remove a server permanently, do these steps:

1. While the cluster has its majority, delete the node of the server **from another server**.
2. Remove K3s from the machine.

```bash
sudo k3s kubectl delete node <name>            # on another server
sudo /usr/local/bin/k3s-uninstall.sh           # on the machine being removed
```

When you delete the node, K3s removes the machine from the etcd membership. If you remove K3s first, the database keeps a member that is not there. With three servers, that missing member is one more failure that the cluster must survive.

### What is still one of something

- **The operator and the dashboard.** One pod, and one to two minutes to move. See [What you do not get](#run-three-control-plane-servers) earlier on this page.
- **Ingress.** Traefik and CoreDNS come with K3s, with one replica each. When their node is lost, they start on another node after the Kubernetes default of five minutes. Geass does not change this default. A domain that points to one address is only as available as that address. Point the domain to more than one server, or put a load balancer in front.
- **The address workers join.** `spec.serverURL` on the cluster object names one server. Workers that have joined continue to work when that server is lost, because the K3s agents learn every server. A worker that you add after the loss cannot join until you set `spec.serverURL` to a server that is up.
- **The cluster's backups.** Three copies of etcd protect against the loss of a machine. They do not protect against a mistake that is written to all three copies. To recover from such a mistake, use the snapshot that the installer takes before an update, on the disk of the first server. You can also make K3s send its snapshots to your own S3 bucket. A rebuild after the loss of every server starts from those snapshots. See [Disaster recovery](/docs/operations/disaster-recovery/) and [Upgrade Geass](/docs/operations/upgrading/#go-back).

## Before you start

- Three or more nodes that are ready and not cordoned. See [Add servers](/docs/getting-started/add-servers/).
- Somebody has run the readiness check at least one time. See [Check readiness](#check-readiness).
- A project database in a project that has a region needs the nodes of its region to hold the three instances. Geass does not check this. See [Where the instances run](#where-the-instances-run).

## Check readiness

Geass lets a highly available database start only when a readiness report says that the cluster can host one.

1. Open **Usage** in the left rail.
2. Scroll to **HA readiness**. The section shows "High availability databases need at least three healthy nodes." It also shows **Healthy nodes** and **Ready for HA** (Yes or No). Until a report exists, **Healthy nodes** shows the dashboard count of ready nodes, with cordoned nodes included, and **Ready for HA** shows No.
3. Click **Run readiness check**. Only admins can do this step. The button is disabled for viewers.

The report is a `GeassHAReadiness` object named `platform` in `geass-system`. Its conditions are:

| Condition | True when |
| --- | --- |
| `NodesReady` | A minimum of three nodes are ready and not cordoned. Nodes in every region and the control plane node count. A node that must not hold data (an ephemeral node of a [pool](/docs/guides/node-pools/#what-runs-on-a-pools-nodes)) does not count, because it cannot host a database replica. |
| `StorageReady` | The cluster has a minimum of one storage class. |
| `ClusterReady` | The `Ready` condition of the cluster is true. |
| `AddonsReady` | The add-ons of the cluster are ready (cert-manager, monitoring, and Longhorn when it is on). |
| `Ready` | All four are true. |

```bash
kubectl -n geass-system get geasshareadiness platform -o yaml
```

:::note
Only **Run readiness check** creates the report. Thus on a new cluster, **Ready for HA** shows **No** until somebody runs the check. After that, the operator keeps the report up to date automatically. The operator evaluates the report again in these cases:

- A node is added or removed, becomes ready or not ready, or is cordoned or uncordoned.
- The region or the tags of a node change.
- A storage class is created or removed.
- The `GeassCluster` changes, or its `Ready` or `AddonsReady` condition changes.

If you click the button again, nothing occurs, because the report already exists.
:::

To force an evaluation, change the object. For example, this command works:

```bash
kubectl -n geass-system annotate geasshareadiness platform geass.dev/recheck="$(date +%s)" --overwrite
```

You can also delete the object and click **Run readiness check**.

## Create a highly available database

**On a shared database server.** In **Settings**, open **Databases** and set up a server. Turn on **High availability**: "Three instances, so the server survives losing a node." The switch is off and shows "Needs at least three healthy nodes." until the report shows **Ready for HA**. See [Shared database servers](/docs/guides/shared-database-servers/).

**On a project database.** The create dialog of the dashboard has no high availability option. Use the API or a manifest.

```bash
curl -sS -X POST https://geass.example.com/api/databases/create \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d name=orders -d project=morning-beach -d environment=production \
  -d engine=Postgres -d highAvailability=on
```

```yaml title="orders-db.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassDatabase
metadata:
  name: orders
  namespace: geass-system
spec:
  project: morning-beach
  environment: production
  engine: Postgres
  version: "16"
  highAvailability: true      # at least 3 instances
  instances: 3
  storageSize: 20Gi
```

`highAvailability: true` forces a minimum of three instances. A value of 3 or more for `instances` also does this, even without the flag. The dashboard has no field to change this setting later. A database that you create through the API is a draft until you deploy it with **Deploy changes**. A manifest like the previous example is not a draft. The operator starts to provision it immediately when you apply it.

### What the gate does

Before the operator provisions an in-cluster database that asks for high availability, the readiness report must be `Ready` with a minimum of three healthy nodes. Until then, the `Ready` condition of the database shows "High availability requires a cluster with at least three healthy nodes". That message means that Geass waits. It is not a failure. Geass checks again every 30 seconds, and never marks the database failed for this reason. The dashboard does not show that text. It shows the database as **Unavailable** with a general hint. Thus read the condition with `kubectl`:

```bash
kubectl -n geass-system get geassdatabase orders \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}{"\n"}'
```

The gate covers Postgres, MySQL, and MariaDB. Geass refuses Redis before the gate, because Redis runs as one instance. The capacity check also counts three instances, and for MySQL a minimum of 1 GB of memory for each instance. See [Scaling and placement](/docs/guides/scaling-and-placement/).

## Where the instances run

- **Postgres.** Geass creates a CloudNativePG cluster with three instances. Geass sets a node selector and tolerations only to keep the instances in the region of the project and on the tags that you chose. To spread the instances across nodes, Geass keeps the default of the Postgres operator. Geass sets no rule that spreads instances across sites, and it does not check that a region has three nodes.
- **MySQL.** Geass creates a MOCO cluster of three or five instances with the same node selector and tolerations. To spread the instances, Geass asks that the instances prefer different nodes. This is a preference, not a rule. Thus a region with fewer nodes than instances still runs the database on the nodes that it has.
- **Ephemeral nodes.** The instances never run on a node that keeps data off itself, such as a node of a pool with `storage: None`. The readiness report does not count such a node. A pool whose nodes can hold data (`storage: Allow`) counts like any other node. The autoscaler never removes such a node while it holds a volume.
- **Required tags.** A project that requires node tags ([Compliance node tags](/docs/guides/compliance-tags/)) keeps all three instances on nodes that have those tags. The readiness report counts every node in the cluster, not only the tagged nodes. Thus make sure yourself that a minimum of three nodes have the tags.
- **Volumes.** With the Longhorn storage add-on, a highly available Postgres or MySQL uses the `geass-single` class. The volume of each instance has one copy, on the node where the instance runs, because the three instances already hold three copies of the data. See [Storage](/docs/guides/storage/).
- **Sites.** Geass writes the **Site** of a node as the standard `topology.kubernetes.io/zone` label, so software that reads that label can use it. When every node has a Site, Geass spreads the replicas of a service by site, as a preference. See [Services](#services). Geass does not spread the instances of a database by site.

The readiness report counts nodes in the whole cluster. A project in a region with one node still passes the gate, and its three instances might then share that node. Before you put a highly available database in a region, give the region a minimum of three nodes. Then check where the instances are:

```bash
kubectl get pods -n morning-beach-production -l cnpg.io/cluster=orders -o wide
```

### MariaDB

A MariaDB database that asks for high availability runs on the MariaDB operator. It has three or five servers, and one of them is the primary. It uses semi-synchronous replication, so a write is on a replica before the primary acknowledges it. The address of the database is the Service of the primary, `<ID>-primary`, which follows the primary.

The operator replaces a primary when its pod is lost or its server stops answering. In a test, after the pod of the primary was killed, a replica was the primary ten seconds later. The write made during that time was on every server.

The operator cannot replace a primary whose machine stops answering. In that case, the replicas lose their primary without a signal. They stop being ready and stop replicating. The operator promotes only a replica that is ready and replicating. Geass does that part:

1. Geass watches the machine of the primary.
2. When that machine has not been ready for one minute, Geass names a new primary. It picks the replica that has seen the most of what the primary wrote, on a machine that is ready.
3. The operator switches over to that replica.

If the operator cannot finish a promotion in ten minutes, Geass stops it, because a switchover needs the old primary to answer.

This works only when the servers are spread over three machines, so that a replica is still on a machine that is up. With two of three servers on the lost machine, one replica is left to take over. With all of them on that machine, no replica is left.

Backups of a highly available MariaDB database are continuous. Geass makes physical copies every night, and archives the binary logs of the primary to the same bucket. Thus you can restore to any moment. After a failover, Geass asks for a new copy from the new primary, because a copy of the old primary does not reach the logs of the new primary. See [Backups and restore](/docs/guides/backups-and-restore/#mariadb-with-high-availability-copies-and-archived-logs).

The operator itself runs two copies of each of its three parts, spread over nodes when there are enough nodes. Thus the loss of the machine of one copy does not stop the operator from promoting a replica. A database cannot change between one instance and high availability. See [High availability for MariaDB](/docs/guides/databases/#high-availability-for-mariadb).

### MySQL

A MySQL database that asks for high availability runs on MOCO. It has three or five MySQL servers, and one of them is the primary. The primary acknowledges a write only after a replica has it. Thus the loss of the primary does not lose an acknowledged write. MOCO promotes the most advanced replica. The address of the database, `moco-<name>-primary`, follows the primary. Apps reconnect after the few seconds that the switch takes.

These are the results that Geass saw on three machines, with one MySQL server on each machine:

| What was lost | What happened |
| --- | --- |
| The primary's pod | A replica was primary within seconds, and writes failed for about four seconds. The old primary was a replica again in about 15 seconds. |
| The whole machine of the primary | A replica was primary after about 36 seconds, and writes failed for about 45 seconds. The server of that machine joined again as a replica within 90 seconds after the machine came back. |
| The primary's MySQL froze without dying | A replica was primary after about three minutes, because MOCO waits for the server to stop answering. |

In each case, every write that had been acknowledged was on the new primary. Run the MOCO controller on more than one node, so that failover continues to work when the controller is on the machine that you lose. Geass installs the controller with two copies. Give a highly available MySQL database three nodes. In a Geass test on two nodes, two of the three servers shared one node. When that node was lost, nothing was promoted for as long as it stayed down (seven minutes), because the one server that was left was not a majority.

A single MySQL server, without high availability, is unavailable until it runs again. A database cannot change between one instance and high availability. See [High availability for MySQL](/docs/guides/databases/#high-availability-for-mysql).

### Redis

Redis runs as one instance. If a Redis database asks for high availability, or for more than one instance, Geass refuses it with "High availability requires PostgreSQL, MySQL, or MariaDB: Redis runs as a single instance. Turn it off, or choose PostgreSQL, MySQL, or MariaDB". Geass has no replication or failover for Redis. Redis runs without persistence. Thus it loses its data when its pod restarts.

### Services

A service with more than one replica continues to serve while some of its replicas are up. A service can be set to run more than one replica, or can scale up to more than one. For such a service, Geass asks the scheduler to spread the replicas across machines, with a maximum skew of one. When every node that can run the service has a Site, Geass also asks the scheduler to spread the replicas across sites.

Geass leaves out sites when any node does not have one. The reason is that the scheduler gives no score to a node for a preference on a label that the node does not have. Thus a preference for sites on nodes that have no sites also cancels the preference for machines. A first version of Geass did this. On three machines with no sites, both replicas of a service started on one machine in eight cases of ten. The default of the scheduler, and a preference for machines only, spread the replicas in every case.

These rules also apply:

- The spread is a preference, not a rule. Thus a cluster with one machine, or with too few machines, still runs every replica. Two replicas can share a machine when there is no other machine.
- There is no pod disruption budget.
- A service that runs a single replica does not get the preference, because it has nothing to spread. An update of Geass does not restart that service.
- The preferences of a service depend on the nodes. Thus the first update that brings this feature makes every service that runs more than one replica roll one time. The service rolls again when the answer changes, for example when a node without a Site joins or leaves a cluster whose other nodes all have a Site.
- The preference decides only where a replica starts. Thus Geass does not move replicas that already run when a node comes back.

To see where the replicas are, run `kubectl get pods -o wide`.

## When a node is lost

| What was on the node | What happens |
| --- | --- |
| A Postgres instance (highly available) | If the lost instance was the primary, the Postgres operator promotes a replica. The `<name>-rw` service follows the primary, so clients that use the address of the database reconnect. The volume of the lost instance has its only copy on the lost node. Thus that instance is back when the node returns. If the node is gone for [15 minutes](#what-geass-does-after-fifteen-minutes), Geass replaces the instance, and builds a new instance from the primary on another node. The other instances continue to serve. |
| A single-instance database | It is unavailable until it runs again. With Longhorn, whose volumes have copies on other nodes, it starts on another node. A MySQL server or a SQLite database starts a few minutes after Kubernetes stops waiting for the lost pod. Postgres starts [15 minutes](#what-geass-does-after-fifteen-minutes) after the node was lost, plus a maximum of six minutes while Kubernetes releases the volume on the lost node. On local storage, its data is on the lost node. Thus it is unavailable until the node returns. See [Storage](/docs/guides/storage/). |
| A service replica | Kubernetes starts a replacement on another node that matches the region and the tags of the project and has space. By default, Kubernetes waits five minutes before it does this. Geass does not change that time. During that time, a service with one replica is down. |
| A MySQL instance (highly available) | If the lost instance was the primary, MOCO promotes a replica, and the `moco-<name>-primary` Service follows it. A replica that was on the lost node is back when the node is back. In a test, this took about two minutes after the node returned. Geass does not replace that replica automatically. See [MySQL](#mysql). |
| A MariaDB server (highly available) | If the lost server was the primary, the operator promotes a replica. When the whole machine of the primary is lost, Geass does the promotion. The `<name>-primary` Service follows the primary. A replica that was on the lost node is back when the node is back. Geass does not replace that replica automatically. If a replica cannot continue replication, the MariaDB operator builds it again from a copy of a ready replica. With Longhorn, Geass keeps that copy on replicated storage. Thus the job that builds the replica can mount the copy on any machine that the replica is on. In a test, a replica was built again in about 90 seconds, with the data and replication intact. A database made by an earlier release kept the copy on a volume with one copy on one machine. Because of this, a rebuild on any other machine could never be scheduled. Geass replaces that volume, the template, and a stuck rebuild the first time that it looks at the database after an update. See [MariaDB](#mariadb). |
| A single MySQL server, or a Redis server | A MySQL server whose volume is on the replicated storage of Longhorn starts on another node. The reason is that Geass sets Longhorn to replace pods that were on a node that is down. Redis keeps nothing on disk. It starts empty on another node, [15 minutes](#what-geass-does-after-fifteen-minutes) after the node was lost. On local storage, a MySQL server is unavailable until its node returns. See [Redis](#redis). |
| A control plane server (of three) | The API and the data of the cluster stay available from the other two servers. If the operator was on the lost server, the operator starts on another server in one to two minutes. |
| The only control plane server | See below. |

### What Geass does after fifteen minutes

Kubernetes does not start a second copy of a pod while it cannot tell that the first copy has stopped. This rule is correct for a database, because two copies that write to one volume are worse than no copy. Thus a database pod on a machine that is gone shows as `Terminating` for as long as the machine is away, and its replacement waits. When a node has been not ready for **15 minutes**, Geass decides that the node is gone. Then Geass does these things for the databases that it runs:

- **A database with one instance, or an instance whose volume has copies on other nodes,** is made to start on another node. Geass removes the pod that it cannot reach. This applies to Postgres, and to Redis and MySQL servers. A volume stays attached to the lost node until Kubernetes releases it, six minutes after Geass removes the pod. Thus a Postgres database with one instance serves again about 21 minutes after its machine stopped. In the test, with the limit set to six minutes, it took 14 minutes.
- **An instance of a highly available Postgres database whose volume was on the lost node alone** is replaced. Geass removes the instance and its volume, and the Postgres operator builds a new instance from the primary on another node. A small database is built again in about one minute. If two of three instances were lost, Geass replaces both, and the primary continues to serve.
- **The same is done for an instance that is on a node that is up** in this case: its volume has lost its only copy, and the instance has not been able to start for 15 minutes. For example, the disk was replaced, or the instance is on the machine of a rebuilt control plane server.
- **An instance whose volume can only be used from a server that was removed from the cluster** is replaced after **3 minutes**, and not 15. When you remove a server, its node leaves the cluster immediately, so there is no reason to wait. The instance that the Postgres operator makes again for the volume would stay `Pending` with `volume node affinity conflict`. Geass replaces the instance only when the server is not in the cluster at all. Geass never replaces it for a server that is only not ready, because that server might come back. The primary must also be up in this case. Before you confirm, the remove dialog lists what is kept only on the server.
- **It never replaces an instance unless the primary is up.** While the primary is also on the lost node, the data on the machines that are away might be the newest data. Geass leaves that data until the Postgres operator has promoted another instance. Geass also leaves an instance alone when it cannot tell which volumes belong to that instance.

The 15 minutes is `spec.lostNodeAfter` on the [GeassCluster](/docs/reference/resources/geass-cluster/). `0s` turns this off. Set `0s` before you shut down a node for longer than that time, because the replacement of an instance removes its volume.

Geass sets Longhorn to replace the pods that Kubernetes manages when their node is down. These pods are the MySQL servers, and the SQLite and service pods that use Longhorn volumes. Longhorn does not wait for the 15 minutes. Geass sets this only when the Longhorn setting is still its default (`do-nothing`). Geass keeps a value that you chose.

Geass cannot tell one case from a dead machine: a machine that is cut off from the network but still runs. What ran on that machine continues to run until Geass can reach it. Geass has then started another copy on another node. Geass makes the same assumption as Longhorn: a node that has been unreachable for this long has stopped.

A worker that Geass joined over SSH and that stays not ready for more than five minutes is provisioned again. The operator connects to the machine and installs the K3s agent again. If the machine is gone, this fails, and the `GeassNode` of the node shows the error. See [Troubleshooting](/docs/operations/troubleshooting/). Backups protect you against the loss of every copy. You can restore the continuous backups of a Postgres database to a new database. See [Backups and restore](/docs/guides/backups-and-restore/).

## What is not highly available

:::caution
To remove the single point of failure, [join two more](#run-three-control-plane-servers) control plane servers. The control plane is only as available as the servers that you give it. A new install has one server, and is a single point of failure until you join two more.
:::

- **A single control plane server.** The installer builds one K3s server with embedded etcd. With one server, the API server, the database of the cluster, and the operator are all on that machine.
- **The operator and the dashboard.** The dashboard is part of the operator process. The operator is one pod. Its Deployment requires a node with the control-plane label, because the installer imports its image only on control plane servers. With one server, while that machine is down, the dashboard is unreachable and nothing is reconciled. You cannot deploy, scale, or restore anything. With three servers, the operator starts on another server within about two minutes.
- **What keeps running.** While the control plane is down, containers that already run on other nodes usually continue to run. But until the control plane returns, nothing can be changed, restarted on another node, or rescheduled.
- **Recovery data for the cluster itself.** By default, K3s takes its own etcd snapshots on the disk of each server. Geass takes one more snapshot before it updates the cluster. [Upgrade Geass](/docs/operations/upgrading/#go-back) shows how to restore a snapshot. Neither snapshot survives the loss of the disk, unless you have turned on copies in your own S3 bucket. Geass keeps those copies off until you turn them on. See [Disaster recovery](/docs/operations/disaster-recovery/). Without those copies, protect the control plane machine with the snapshots of your provider.
- **Ingress and DNS.** Public traffic comes in through the K3s Traefik on your nodes. A domain that points to one address is only as available as that machine. Geass does not manage failover between addresses.
- **Object storage and monitoring.** The object storage server (SeaweedFS or Garage), Prometheus, and the log store each run one instance on one volume. With Longhorn, the volume of the object storage server has copies on other nodes (a maximum of three). This protects its data when a node is lost, but does not make the server highly available.
- **A whole site.** A cluster spread over more than one provider is experimental. See [Nodes at more than one provider](/docs/operations/multi-provider/). Its control plane is still at one site.

## What happens under the hood

| Object | Where | Purpose |
| --- | --- | --- |
| `GeassHAReadiness` `platform` | `geass-system` | The readiness report. |
| CloudNativePG `Cluster` named after the database | `<project>-<environment>` (or `geass-system` for a shared server) | Three Postgres instances. The operator itself runs in `cnpg-system`. |
| Services `<name>-rw`, `<name>-ro`, `<name>-r` | The database's namespace | Read-write follows the primary. Geass gives out the `-rw` address. |
| `GeassNode` | `geass-system` | One for each worker that the operator joined. The operator provisions again a worker that stays not ready for five minutes. |

```bash
kubectl get cluster.postgresql.cnpg.io -A
kubectl get pods -n morning-beach-production -l cnpg.io/cluster=orders -o wide
kubectl -n geass-system get geassnodes
kubectl -n geass-system get deploy geass-controller-manager -o wide
```

## Troubleshooting

**The database says "High availability requires a cluster with at least three healthy nodes".**
Do these checks in this order:

1. Run `kubectl get nodes`. You need three nodes that are ready and not cordoned.
2. Check whether somebody has run **Run readiness check**. Without a report, the gate never opens.
3. Run `kubectl -n geass-system get geasshareadiness platform -o jsonpath='{.status.conditions}'`, and look for a condition that is false. For example, `AddonsReady` is false while an add-on still installs.

The report follows changes to nodes, storage classes, and the cluster automatically. If the report is still wrong after a few seconds, annotate it as shown in [Check readiness](#check-readiness), and check the log of the operator.

**The High availability switch is off on the shared server form.**
The report does not show **Ready for HA**. If no report exists yet, run the check. Read the conditions of the report.

**The report says Yes, but the three instances are on one node.**
The gate counts nodes in the whole cluster, not in the region of the database. Add nodes to the region, and then create the database in it again. Geass decides the placement one time. Thus a later change to the region does not move the database.

**One instance stays `Pending` after a node was lost.**
Its volume has a single copy on the lost node. Bring the node back, or restore the database from backup into a new database.

**Choosing Run readiness check a second time changed nothing.**
This result is expected after the first run. The report exists and updates itself. See [Check readiness](#check-readiness).

## Related

- [Shared database servers](/docs/guides/shared-database-servers/)
- [Databases](/docs/guides/databases/)
- [Storage](/docs/guides/storage/)
- [Backups and restore](/docs/guides/backups-and-restore/)
- [Nodes at more than one provider](/docs/operations/multi-provider/)
