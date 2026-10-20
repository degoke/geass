---
title: "High availability"
description: "High availability in Geass: three control plane servers, HA databases, what survives losing a node, and what is still a single point of failure."
---

Geass has two high availability features today. A database can run as three instances on three nodes, so that it keeps serving when one node is lost. And the control plane can run on three servers, so that the cluster's API and its data survive losing one. Neither makes your services, ingress or object storage highly available by itself. This page says what each feature does, how to set it up, and, plainly, what is still a single point of failure.

## What is and is not highly available

| Part | Highly available? | Notes |
| --- | --- | --- |
| Postgres with **High availability** | Yes, with three instances | Replicas and failover come from the Postgres operator (CloudNativePG). Needs at least three healthy nodes. |
| MySQL with **High availability** | Yes, with three or five instances | Replicas and failover come from the MySQL operator (MOCO). Needs at least three healthy nodes. See [MySQL](#mysql). |
| MariaDB with **High availability** | Yes, with three or five instances | Replicas and failover come from the MariaDB operator, with a trigger from Geass for a machine that goes silent. Needs at least three healthy nodes. See [MariaDB](#mariadb). |
| Redis | No | One instance. It refuses **High availability**. See [Redis](#redis). |
| Services | Only as far as replicas go | A service with several replicas asks the scheduler to put them on different machines, and then at different sites, where it can. See [Services](#services). |
| Control plane: the Kubernetes API and etcd | Yes, with three servers | Join two more servers to the first. The cluster works with one lost and stops with two. See [Run three control plane servers](#run-three-control-plane-servers). |
| The operator and the dashboard | Restarts on another server | One pod, which starts on another server when its own is lost, in one to two minutes. Nothing is reconciled and the dashboard is down in between. |
| Single-instance databases (including MySQL and MariaDB without high availability), the object storage server, SQLite | No | One instance each. |

## Run three control plane servers

The control plane is the Kubernetes API, the database that holds every object in the cluster (etcd), and Geass's operator. On a new install it is one K3s server. You can join two more, so that the API and the cluster's data are on three machines and survive losing one.

**What you get.** The three servers hold a copy of the cluster's database each, and any of them answers the API. With one server lost, the API keeps answering from the other two, reads and writes keep working, and the cluster's data is all there. With two lost, the database has no majority and the cluster stops answering until one comes back. Containers that already run on any node keep running either way.

**What you do not get.** Geass runs one operator, and it is the operator that deploys, scales, reconciles and serves the dashboard. It is a pod, and the installer puts its image on every server you join, so when the server it runs on is lost it starts on another one. That takes between 75 seconds and two minutes (76 to 112 seconds in tests, 87 on the last real three-server cluster). Kubernetes decides a node has stopped answering after about 40 seconds, and longer when the lost server also held the lock that Kubernetes's own controllers run under, because the next one starts counting from when it takes over. The operator's pod is then given 30 more seconds before it is moved, and the new pod starts in a few, because the image is already there. Nothing is deployed or reconciled, and the dashboard is down, until it has. Two operators would not make this faster and would double its memory, so there is one.

### Before you start

- A cluster that the installer built. Its K3s runs with embedded etcd (`--cluster-init`), which is what lets a second server join. A K3s that was started another way, or one that uses an external database, cannot take a server this way.
- Two more machines that meet the [requirements](/docs/getting-started/requirements/), on the **same private network** as the first, with fast links. Servers carry etcd's traffic to each other all the time, so keep them at one site and do not use [multi-site mode](/docs/operations/multi-provider/) for them. The installer refuses a join with `GEASS_MULTI_SITE` set.
- **Three servers, not two.** A cluster needs a majority of its servers to answer. With three it tolerates one lost, with two it tolerates none: losing either stops it, and two servers fail more often than one. Use three, or five to tolerate two.
- The ports in the next table open between the servers, in the host firewall and in the provider's security group. The installer opens them on the machine that joins, if it finds a host firewall, and checks that it can reach them on the server it joins before it changes anything. **It cannot open them on the first server**, so open them there yourself:

| Port | Between | What it is for |
| --- | --- | --- |
| 6443/tcp | Every node and every server | The Kubernetes API |
| 2379/tcp and 2380/tcp | The servers | etcd, the cluster's database: its clients and its peers |
| 8472/udp | Every node | The VXLAN overlay |
| 10250/tcp | Every node | The kubelet |

```bash
sudo ufw allow 2379/tcp && sudo ufw allow 2380/tcp     # on each server, if ufw is active
```

### Join a server

1. On the **first** server, read the token that lets a server join, and the K3s release the cluster runs:

   ```bash
   sudo cat /var/lib/rancher/k3s/server/token
   sudo k3s --version
   ```

2. On the **new** machine, as root, run the installer with the first server's address, that token and that K3s release. Pass the token in the environment, where the process list does not show it. Join one machine at a time, and wait for it to finish before you start the next:

   ```bash
   curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | \
     sudo env GEASS_JOIN_SERVER=https://10.0.0.5:6443 GEASS_JOIN_TOKEN='K10…::server:…' \
     GEASS_K3S_VERSION=v1.36.5+k3s1 sh
   ```

   A server has to run the same K3s as the others, and a machine that has not joined cannot ask the cluster which that is: K3s answers questions about its version only to clients it knows, and a join token is not one. So the installer asks you (`GEASS_K3S_VERSION`, or `--k3s-version` after `sh -s --`) and, once the new server has joined, compares it with the cluster's other servers and stops with what to do if they differ. Before that it checks the machine, the address and the two database ports, and installs K3s as a server with the same settings as the first, waits for the new node to be a ready control plane node, and imports the operator's image. It does not touch a machine that already has K3s: that is refused, and the installer never replaces one.

3. Check that the cluster has the servers:

   ```bash
   sudo k3s kubectl get nodes -L node-role.kubernetes.io/control-plane,node-role.kubernetes.io/etcd
   ```

   Each server shows `true` in both columns. The **Usage** page lists them too, and counts them in **Healthy nodes**.

### What each server keeps current

An update of Geass is made on the first server, as always, and it leaves the operator's Deployment naming a new image. That image is in no registry: it is inside the installer, and the installer puts it on the machine it runs on and on no other. The operator's pod can start on any control plane server, so **before you update the first server, run the new release's installer on each of the other servers with no options**:

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo sh
```

Run on a server that joined a cluster, it does only that: it puts the operator's new image on the server, next to the old one, and changes nothing else. It does not update the cluster, and it refuses `--upgrade`, `--reinstall` and anything else that would change one. The order matters. If the first server is updated before the others have the image, the new operator can be scheduled to one of them, cannot pull the image, and the installer sees the pod cannot pull its image, gives up and puts the previous operator back. Nothing is lost, but the update did not happen, and the installer tells you before it starts which servers do not report the image yet. A server that you have only just refreshed can still be named for some seconds, until its node reports the image; if you have refreshed it, go on.

To move the cluster's K3s to a new release, use `--k3s-version` on the first server as usual, then upgrade each of the other servers **one at a time**, waiting for the node to be ready before the next, with K3s's own install script and the version you gave the first server. The server keeps its settings, because they are in its configuration file:

```bash
curl -sfL https://get.k3s.io | sudo env INSTALL_K3S_VERSION=v1.36.5+k3s1 INSTALL_K3S_EXEC=server sh -
```

Geass does not move the workers until the oldest control plane server runs the new version, because a worker is not allowed to be newer than the API servers it talks to. The cluster object says which server it is waiting for.

### Take a server away

Do not stop two servers of three: the cluster stops. To remove one for good, delete its node **from another server** while the cluster has its majority, then remove K3s from the machine:

```bash
sudo k3s kubectl delete node <name>            # on another server
sudo /usr/local/bin/k3s-uninstall.sh           # on the machine being removed
```

K3s takes the machine out of etcd's membership when its node is deleted. Removing K3s first leaves a member in the database that is not there, and with three servers that is one more failure the cluster has to survive.

### What is still one of something

- **The operator and the dashboard.** One pod, one to two minutes to move. See above.
- **Ingress.** Traefik and CoreDNS are K3s's own, one replica each. They start on another node when theirs is lost, after Kubernetes's default five minutes, which Geass does not change. A domain that points at one address is only as available as that address. Point it at several servers, or put a load balancer in front.
- **The address workers join.** `spec.serverURL` on the cluster object names one server. Workers that have joined keep working when it is lost, because K3s's agents learn every server. A worker you add after that fails to join until you set `spec.serverURL` to a server that is up.
- **The cluster's backups.** Three copies of etcd protect against losing a machine, not against a mistake that is written to all three. The snapshot the installer takes before an update, on the first server's disk, is the way back from that. You can also have K3s send its snapshots to an S3 bucket of your own, which is what a rebuild after losing every server starts from. See [Disaster recovery](/docs/operations/disaster-recovery/) and [Upgrade Geass](/docs/operations/upgrading/#go-back).

## Before you start

- Three or more nodes that are ready and not cordoned. See [Add servers](/docs/getting-started/add-servers/).
- The readiness check has been run at least once (see below).
- A project database in a project that has a region needs its region's nodes to carry the three instances. Geass does not check that. See [Where the instances run](#where-the-instances-run).

## Check readiness

Geass lets a highly available database start only when a readiness report says the cluster can host one.

1. Open **Usage** in the left rail.
2. Scroll to **HA readiness**. It says "High availability databases need at least three healthy nodes." and shows **Healthy nodes** and **Ready for HA** (Yes or No). Until a report exists, **Healthy nodes** shows the dashboard's own count of ready nodes (cordoned ones included) while **Ready for HA** reads No.
3. Choose **Run readiness check**. Admins only: the button is disabled for viewers.

The report is a `GeassHAReadiness` object named `platform` in `geass-system`. Its conditions are:

| Condition | True when |
| --- | --- |
| `NodesReady` | At least three nodes are ready and not cordoned. Every region and the control plane node count. |
| `StorageReady` | The cluster has at least one storage class. |
| `ClusterReady` | The cluster's `Ready` condition is true. |
| `AddonsReady` | The cluster's add-ons are ready (cert-manager, monitoring, and Longhorn when it is on). |
| `Ready` | All four are true. |

```bash
kubectl -n geass-system get geasshareadiness platform -o yaml
```

:::note
**Run readiness check** creates the report, and nothing else does, so on a new cluster **Ready for HA** reads **No** until someone runs the check. After that the operator keeps the report up to date by itself. It evaluates it again when a node is added or removed, becomes ready or not ready, or is cordoned or uncordoned, when a node's region or tags change, when a storage class is created or removed, and when the `GeassCluster` changes or its `Ready` or `AddonsReady` condition flips. Choosing the button again does nothing, because the report already exists.
:::

To force an evaluation, change the object. This also works:

```bash
kubectl -n geass-system annotate geasshareadiness platform geass.dev/recheck="$(date +%s)" --overwrite
```

Deleting the object and choosing **Run readiness check** also works.

## Create a highly available database

**On a shared database server.** In **Settings**, open **Databases** and set up a server. Switch on **High availability**: "Three instances, so the server survives losing a node." The switch is off and reads "Needs at least three healthy nodes." until the report says **Ready for HA** (see [Shared database servers](/docs/guides/shared-database-servers/)).

**On a project database.** The dashboard's create dialog has no high availability option. Use the API or a manifest.

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

`highAvailability: true` forces at least three instances. So does `instances` of 3 or more, even without the flag. The dashboard has no field to change it afterwards. A database created through the API is a draft until you deploy it with **Deploy changes**. A manifest like the one above is not a draft: the operator starts provisioning it as soon as you apply it.

### What the gate does

Before it provisions any in-cluster database that asks for high availability, the operator needs the readiness report to be `Ready` with at least three healthy nodes. Until then the database's `Ready` condition reads "High availability requires a cluster with at least three healthy nodes". That is a waiting message, not a failure: Geass looks again every 30 seconds and never marks the database failed for it. The dashboard does not show that text. It shows the database as **Unavailable** with a generic hint, so read the condition with `kubectl`:

```bash
kubectl -n geass-system get geassdatabase orders \
  -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}{"\n"}'
```

The gate covers Postgres, MySQL and MariaDB. Redis is refused before it, because it runs as one instance. The capacity check also counts three instances, and for MySQL at least 1 GB of memory for each (see [Scaling and placement](/docs/guides/scaling-and-placement/)).

## Where the instances run

- **Postgres.** Geass creates a CloudNativePG cluster with three instances. It sets a node selector and tolerations only to keep the instances in the project's region and on the tags you chose. For spreading across nodes it leaves the Postgres operator's own default in place. Geass sets no rule that spreads instances across sites, and it does not check that a region has three nodes.
- **MySQL.** Geass creates a MOCO cluster of three or five instances with the same node selector and tolerations. For spreading it asks that the instances prefer different nodes, which is a preference and not a rule, so a region with fewer nodes than instances still runs the database on the nodes it has.
- **Required tags.** A project that requires node tags ([Compliance node tags](/docs/guides/compliance-tags/)) keeps all three instances on nodes that carry them. The readiness report counts every node in the cluster, not the tagged ones, so give the tags at least three nodes yourself.
- **Volumes.** With the Longhorn storage add-on, a highly available Postgres or MySQL uses the `geass-single` class. Each instance's volume has one copy, on the node where the instance runs, because the three instances already hold three copies of the data. See [Storage](/docs/guides/storage/).
- **Sites.** A node's **Site** is written as the standard `topology.kubernetes.io/zone` label, so software that reads it can use it. Geass spreads a service's replicas by site as a preference, when every node has a Site (see [Services](#services)), and does not spread a database's instances by site.

The readiness report counts nodes in the whole cluster. A project in a region with one node still passes the gate, and its three instances may then share that node. Give a region at least three nodes before you put a highly available database in it, and check where the instances landed:

```bash
kubectl get pods -n morning-beach-production -l cnpg.io/cluster=orders -o wide
```

### MariaDB

A MariaDB database that asks for high availability runs on the MariaDB operator: three or five servers, one of them the primary, with semi-synchronous replication, so a write is on a replica before it is acknowledged. The database's address is the primary's Service, `<ID>-primary`, which follows the primary.

The operator replaces a primary whose pod is lost or whose server stops answering. Killing the primary's pod made a replica the primary ten seconds later, with the write made in between on every server. It cannot replace a primary whose machine goes silent, because the replicas then lose their primary without being told, stop being ready and stop replicating, and it promotes only a replica that is ready and replicating. Geass does that part (and gives a promotion up after ten minutes if the operator cannot finish it, because a switchover needs the old primary to answer): it watches the machine of the primary, and when it has not been ready for a minute it names the replica that has seen the most of what the primary wrote, on a machine that is ready, as the primary, and the operator switches over to it. Spreading the servers over three machines, so that a replica is still on a machine that is up, is what makes this possible: with two of three servers on the lost machine there is one replica left to take over, and with all of them on it there is none.

Backups of a highly available MariaDB database are continuous: physical copies every night, and the primary's binary logs archived to the same bucket, so a restore can be to any moment. After a failover Geass asks for a new copy from the new primary, because a copy of the old one does not reach the new one's logs. See [Backups and restore](/docs/guides/backups-and-restore/#mariadb-with-high-availability-copies-and-archived-logs).

The operator itself runs two copies of each of its three parts, spread over nodes where there are enough of them, so that losing the machine one of them runs on does not stop it from promoting a replica. A database cannot change between one instance and high availability: see [High availability for MariaDB](/docs/guides/databases/#high-availability-for-mariadb).

### MySQL

A MySQL database that asks for high availability runs on MOCO: three or five MySQL servers, one of them the primary. A write is acknowledged only once a replica has it, so losing the primary does not lose an acknowledged write, and MOCO promotes the most advanced replica. The database's address, `moco-<name>-primary`, follows the primary. Apps reconnect after the few seconds the switch takes.

What Geass saw on three machines, with one MySQL server on each:

| What was lost | What happened |
| --- | --- |
| The primary's pod | A replica was primary within seconds, and writes failed for about four seconds. The old primary was a replica again in about 15 seconds. |
| The whole machine of the primary | A replica was primary after about 36 seconds, and writes failed for about 45. The machine's server rejoined as a replica within 90 seconds of the machine coming back. |
| The primary's MySQL froze without dying | A replica was primary after about three minutes, as MOCO waits for the server to stop answering. |

In each case every write that had been acknowledged was on the new primary. Run MOCO's controller on more than one node to keep failover working when it is on the machine you lose: Geass installs it with two copies. Give a highly available MySQL database three nodes. In Geass's test on two, two of the three servers shared one node, and when that node was lost nothing was promoted for as long as it stayed down (seven minutes), because the one server left was not a majority.

A single MySQL server, without high availability, is unavailable until it runs again. A database cannot change between one instance and high availability: see [High availability for MySQL](/docs/guides/databases/#high-availability-for-mysql).

### Redis

Redis runs as one instance. A Redis database that asks for high availability, or for more than one instance, is refused with "High availability requires PostgreSQL or MySQL: Redis runs as a single instance. Turn it off, or choose PostgreSQL or MySQL". Geass has no replication or failover for it. Redis runs without persistence, so it loses its data when its pod restarts.

### Services

A service with several replicas keeps serving while some of them are up. For a service that is set to run more than one replica, or may scale up to more than one, Geass asks the scheduler to spread them across machines, with a skew of at most one, and also across sites when every node that can run it has a Site. Sites are left out when any node lacks one, because the scheduler gives no score to a node for a preference on a label it does not have, so asking for sites on nodes that have none cancels the preference for machines too. That is what a first version did: on three machines with no sites, both replicas of a service started on one machine in eight cases of ten, where the scheduler's own default, and a preference for machines alone, spread them in every case. It is a preference and not a rule, so a cluster with one machine, or too few, still runs every replica, and two can share a machine when there is nowhere else. There is no pod disruption budget. A service that runs a single replica is not given the preference, because it has nothing to spread, and an update of Geass does not restart it. It only decides where a replica starts, so replicas that are already running are not moved when a node comes back. Check where replicas landed with `kubectl get pods -o wide`.

## When a node is lost

| What was on the node | What happens |
| --- | --- |
| A Postgres instance (highly available) | The Postgres operator promotes a replica if the lost instance was the primary. The `<name>-rw` service follows the primary, so clients that use the database's address reconnect. The lost instance's volume has its only copy on the lost node, so that instance is back when the node returns, unless the node is gone for [15 minutes](#what-geass-does-after-fifteen-minutes): then Geass replaces it, and a new instance is built from the primary on another node. The others keep serving. |
| A single-instance database | It is unavailable until it runs again. With Longhorn, whose volumes have copies on other nodes, it starts on another node: a MySQL server, or a SQLite database, a few minutes after Kubernetes gives up on the lost pod, and Postgres [15 minutes](#what-geass-does-after-fifteen-minutes) after the node was lost, plus up to six minutes while Kubernetes lets go of the volume on the lost node. On local storage its data is on the lost node, so it is unavailable until the node returns. See [Storage](/docs/guides/storage/). |
| A service replica | Kubernetes starts a replacement on another node that matches the project's region and tags and has room. By default it waits five minutes before it does; Geass does not change that. A service with one replica is down in the meantime. |
| A MySQL instance (highly available) | MOCO promotes a replica if the lost instance was the primary, and the `moco-<name>-primary` Service follows it. A replica that was on the lost node is back when the node is, which took about two minutes after the node returned in a test; Geass does not replace it by itself. See [MySQL](#mysql). |
| A MariaDB server (highly available) | The operator promotes a replica if the lost server was the primary; Geass does it when the primary's whole machine is lost. The `<name>-primary` Service follows it. A replica that was on the lost node is back when the node is; Geass does not replace it by itself. If a replica cannot resume replication, the MariaDB operator builds it again from a copy of a ready replica. With Longhorn, Geass keeps that copy on replicated storage, so the job that builds the replica can mount it on whichever machine the replica is: a replica was rebuilt in about a minute and a half in a test, with the data and replication intact. A database made by an earlier release kept the copy on a volume with one copy on one machine, which stopped a rebuild on any other machine from ever being scheduled; Geass replaces that volume, the template and a rebuild that was stuck on it the first time it looks at the database after an update. See [MariaDB](#mariadb). |
| A single MySQL server, or a Redis server | A MySQL server whose volume is on Longhorn's replicated storage starts on another node, because Geass sets Longhorn to replace pods that were on a node that is down. Redis keeps nothing on disk and starts empty on another node, [15 minutes](#what-geass-does-after-fifteen-minutes) after the node was lost. On local storage, a MySQL server is unavailable until its node returns. See [Redis](#redis). |
| A control plane server (of three) | The API and the cluster's data stay available from the other two. The operator starts on another server in one to two minutes if it was on the lost one. |
| The only control plane server | See below. |

### What Geass does after fifteen minutes

Kubernetes does not start a second copy of a pod while it cannot tell that the first has stopped, which is the right rule for a database: two of them writing to one volume is worse than none. So a database pod on a machine that has gone away is shown as `Terminating` for as long as the machine is away, and what would replace it waits. Geass takes a node that has been not ready for **15 minutes** to be gone, and then, for the databases it runs:

- **A database with one instance, or an instance whose volume has copies on other nodes,** is made to start elsewhere: Geass removes the pod that cannot be reached. This is Postgres, and Redis and MySQL servers. A volume is attached to the node that was lost until Kubernetes lets go of it, which it does six minutes after the pod is removed, so a Postgres database with one instance is serving again about 21 minutes after its machine stopped (it was 14, with the limit set to six minutes, when this was tested).
- **An instance of a highly available Postgres database whose volume was on the lost node alone** is replaced: Geass removes the instance and its volume, and the Postgres operator builds a new instance from the primary on another node. A small database is rebuilt in about a minute. If two of three instances were lost, both are replaced, and the primary keeps serving.
- **The same is done for an instance that is on a node that is up** when its volume has lost its only copy (a disk that was replaced, or the machine of a rebuilt control plane server) and the instance has not been able to start for 15 minutes.
- **It never replaces an instance unless the primary is up.** While the primary is on the lost node too, the data on the machines that are away may be the newest there is, and Geass leaves it until the Postgres operator has promoted another instance. It also leaves an instance alone when it cannot tell which volumes are its own.

Longhorn is set, for the pods Kubernetes manages (the MySQL servers, and the SQLite and service pods that use its volumes), to replace them when their node is down, which does not wait for the 15 minutes. Geass sets this when it is still Longhorn's default (`do-nothing`), and leaves a value you chose.

The one case this cannot tell apart from a dead machine is a machine that is cut off from the network but running. What ran on it keeps running until it can be reached, and Geass has started another copy elsewhere, on the same assumption that Longhorn makes: that a node that has been unreachable for this long has stopped.

A worker that Geass joined over SSH and that stays not ready for more than five minutes is provisioned again: the operator connects to the machine and reinstalls the K3s agent. If the machine is gone, that fails and the node's `GeassNode` shows the error (see [Troubleshooting](/docs/operations/troubleshooting/)). Backups are your protection against losing every copy: a Postgres database's continuous backups can be restored to a new database (see [Backups and restore](/docs/guides/backups-and-restore/)).

## What is not highly available

:::caution
The control plane is only as available as the servers you give it. A new install has one, and is a single point of failure until you [join two more](#run-three-control-plane-servers).
:::

- **A single control plane server.** The installer builds one K3s server with embedded etcd. With one, the API server, the cluster's database and the operator are all on that machine.
- **The operator and the dashboard.** The dashboard is part of the operator process. The operator is one pod, and its Deployment requires a node with the control-plane label, because its image is imported only on control plane servers. With one server, while that machine is down the dashboard is unreachable, nothing is reconciled, and nothing can be deployed, scaled or restored. With three servers it starts on another within about two minutes.
- **What keeps running.** Containers that are already running on other nodes generally keep running while the control plane is down, but nothing can be changed, restarted elsewhere or rescheduled until it returns.
- **Recovery data for the cluster itself.** K3s takes its own etcd snapshots by default on each server's disk, and Geass takes one more before it updates the cluster. [Upgrade Geass](/docs/operations/upgrading/#go-back) shows how to restore one. Neither survives losing the disk, unless you have turned on copies in your own S3 bucket, which Geass leaves off until you do: see [Disaster recovery](/docs/operations/disaster-recovery/). Without them, treat the control plane machine as something to protect with your provider's snapshots.
- **Ingress and DNS.** Public traffic enters through the K3s Traefik on your nodes. A domain that points at one address is only as available as that machine. Geass does not manage failover between addresses.
- **Object storage and monitoring.** The object storage server (SeaweedFS or Garage), Prometheus and the log store each run one instance on one volume. With Longhorn the object storage server's volume has copies on other nodes (up to three), which protects its data when a node is lost but does not make the server highly available.
- **A whole site.** A cluster spread over several providers is experimental (see [Nodes at more than one provider](/docs/operations/multi-provider/)), and its control plane still sits at one site.

## What happens under the hood

| Object | Where | Purpose |
| --- | --- | --- |
| `GeassHAReadiness` `platform` | `geass-system` | The readiness report. |
| CloudNativePG `Cluster` named after the database | `<project>-<environment>` (or `geass-system` for a shared server) | Three Postgres instances. The operator itself runs in `cnpg-system`. |
| Services `<name>-rw`, `<name>-ro`, `<name>-r` | The database's namespace | Read-write follows the primary. Geass hands out the `-rw` address. |
| `GeassNode` | `geass-system` | One per worker the operator joined. It reprovisions a worker that stays not ready for five minutes. |

```bash
kubectl get cluster.postgresql.cnpg.io -A
kubectl get pods -n morning-beach-production -l cnpg.io/cluster=orders -o wide
kubectl -n geass-system get geassnodes
kubectl -n geass-system get deploy geass-controller-manager -o wide
```

## Troubleshooting

**The database says "High availability requires a cluster with at least three healthy nodes".**
Check, in this order. First, `kubectl get nodes`: you need three that are ready and not cordoned. Second, whether anyone has run **Run readiness check**: without a report the gate never opens. Third, `kubectl -n geass-system get geasshareadiness platform -o jsonpath='{.status.conditions}'` for a condition that is false, such as `AddonsReady` while an add-on is still installing. The report follows changes to nodes, storage classes and the cluster by itself. If it still reads wrong after a few seconds, annotate it as shown above and check the operator's log.

**The High availability switch is off on the shared server form.**
The report does not say **Ready for HA**. Run the check if no report exists yet, and read the report's conditions.

**The report says Yes, but the three instances are on one node.**
The gate counts nodes in the whole cluster, not in the database's region. Add nodes to the region, then recreate the database in it. Placement is decided once, so changing the region later does not move it.

**One instance stays `Pending` after a node was lost.**
Its volume has a single copy on the lost node. Bring the node back, or restore the database from backup into a new one.

**Choosing Run readiness check a second time changed nothing.**
That is expected after the first run: the report exists and updates itself. See [Check readiness](#check-readiness).

## Related

- [Shared database servers](/docs/guides/shared-database-servers/)
- [Databases](/docs/guides/databases/)
- [Storage](/docs/guides/storage/)
- [Backups and restore](/docs/guides/backups-and-restore/)
- [Nodes at more than one provider](/docs/operations/multi-provider/)
