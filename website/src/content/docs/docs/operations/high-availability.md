---
title: "High availability"
description: "Keep Geass and your databases running when a server is lost: three control plane servers, highly available databases, and the limits that remain."
---

Geass can continue to work after the loss of one server. It has two features for this:

- **Three control plane servers.** The cluster's state and its API are on three servers, and continue to work when one server is lost.
- **Highly available databases.** A PostgreSQL, MySQL, or MariaDB database runs as three or more instances on different servers, and continues to serve when one server is lost.

Neither feature makes your services, domains, or object storage highly available by itself. This page says how to set up each feature, and what is still a single point of failure.

## What is and is not highly available

| Part | Highly available? | Notes |
| --- | --- | --- |
| PostgreSQL with **High availability** | Yes, with three instances | Needs at least three healthy servers. |
| MySQL with **High availability** | Yes, with three or five instances | Needs at least three healthy servers. See [MySQL](#mysql). |
| MariaDB with **High availability** | Yes, with three or five instances | Needs at least three healthy servers. See [MariaDB](#mariadb). |
| Redis | No | One instance. See [Redis](#redis). |
| Services | As far as their replicas go | Geass spreads the replicas of a service over different servers where it can. See [Services](#services). |
| The cluster's state and API (the control plane) | Yes, with three servers | Works with one server lost, and stops with two lost. See [Run three control plane servers](#run-three-control-plane-servers). |
| Geass itself and the dashboard | Restarts on another server | When its server is lost, Geass starts on another control plane server in one to two minutes. |
| Single-instance databases, SQLite, and the object storage server | No | One instance each. |

## Run three control plane servers

A new install has one control plane server. That server holds the state of the cluster and runs Geass. You can join two more servers. Then the state of the cluster is on three servers, and it continues to work after the loss of one server.

With three servers, these things happen when servers are lost:

- With one server lost, the cluster continues to work, and no data is lost.
- With two servers lost, the cluster stops until one server comes back.
- In both cases, services that already run continue to run.

Geass itself runs as one copy. When the server that runs Geass is lost, Geass starts on another control plane server. This takes between 75 seconds and two minutes. Until then, the dashboard is unavailable, and Geass deploys and changes nothing.

### Before you start

You need these things:

- A cluster that the Geass installer built. A K3s that was installed in another way, or that uses an external database, cannot take more servers.
- Two more machines that meet the [requirements](/docs/getting-started/requirements/). Put them on the **same private network** as the first server, with fast links between them. Do not use [multi-site mode](/docs/operations/multi-provider/) for the servers. The installer refuses a join with `GEASS_MULTI_SITE` set.
- **Three servers, not two.** The cluster needs most of its servers to answer. With two servers, the loss of either server stops the cluster. Use three servers to survive the loss of one, or five servers to survive the loss of two.
- These ports open between the servers, in the host firewall and in the security group of your provider:

| Port | Between | Use |
| --- | --- | --- |
| 6443/tcp | All servers | The cluster API |
| 2379/tcp and 2380/tcp | The control plane servers | The cluster's state |
| 8472/udp | All servers | The private network between servers |
| 10250/tcp | All servers | Each server's agent |

The installer opens these ports in the host firewall of the machine that joins. It cannot open them on the first server. If the first server has a host firewall, see [Under the hood](#under-the-hood).

### Join a server

To join a server, you run the installer on the new machine. The dashboard **Add server** button adds workers only.

1. Get the join token of the cluster. Geass has no dashboard option to show the token yet. See [Under the hood](#under-the-hood).
2. In the dashboard, open **Usage** in the left rail. In **Version**, read the release in **K3s (control plane)**.
3. On the **new** machine, run the installer with the address of the first server, the token, and the K3s release. Give the token in the environment, so that the process list does not show it:

   ```bash
   curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | \
     sudo env GEASS_JOIN_SERVER=https://10.0.0.5:6443 GEASS_JOIN_TOKEN='K10…::server:…' \
     GEASS_K3S_VERSION=v1.36.5+k3s1 sh
   ```

4. Wait for the installer to finish.
5. Repeat steps 3 and 4 for the third machine. Join one machine at a time.
6. In the dashboard, open **Usage** in the left rail. The **Nodes** table shows each new server with the role `control-plane`.

The new server must run the same K3s release as the other servers. The installer cannot read the release from the cluster before the join, so you give it. After the join, the installer compares the releases, and stops with instructions if they are different.

The installer checks the machine, the address, and the ports before it changes anything. It refuses a machine that already has K3s.

### Update a cluster with three servers

Before you update Geass on the first server, run the new installer on each of the other servers. See [Additional control plane servers](/docs/operations/upgrading/#additional-control-plane-servers).

### Take a server away

Do not stop two of three servers, because the cluster stops. The dashboard **Remove** button and `geass servers remove` do not remove control plane servers. Geass has no SSH access to a control plane server, because the installer joined it. So Geass cannot stop K3s on the machine. A removal that stops half way leaves the cluster with a member that is not there. To remove a control plane server, see [Commands on the servers](#commands-on-the-servers).

### What is still one of something

- **Geass and the dashboard.** One copy, which needs one to two minutes to move.
- **Incoming traffic.** A domain that points to one address is only as available as that server. Point the domain to more than one server, or put a load balancer in front. When the server that routes traffic is lost, routing starts on another server after five minutes.
- **The address that new workers join.** Workers that already joined continue to work when that server is lost. A new worker cannot join until you set a new address. See [Set the address workers join](/docs/getting-started/add-servers/#set-the-address-workers-join).
- **Backups of the cluster's state.** Three copies protect against the loss of a server. They do not protect against a mistake, because the mistake is copied to all three. Use the snapshot that the installer takes before an update, or send snapshots to your own S3 bucket. See [Disaster recovery](/docs/operations/disaster-recovery/) and [Upgrade Geass](/docs/operations/upgrading/#go-back).

## Check readiness

Geass starts a highly available database only when a readiness check says that the cluster can hold one.

1. Open **Usage** in the left rail.
2. Scroll to **HA readiness**. It shows "Needs at least three healthy nodes.", **Healthy nodes**, and **Ready for HA**.
3. Click **Run readiness check**. Only admins can click this button.
4. Read the table under **Ready for HA**. It has one row for each check, and its **Result** column shows **Passed** or
   **Not passed**. To fix a check that did not pass, read what it needs in the next section.

From a terminal, run `geass nodes list`. It shows the servers that are ready and the servers that are cordoned.

You click the button one time for each cluster. Until then, **Ready for HA** shows **No**. After that, Geass keeps the check up to date when servers, storage, or the cluster change. If you click the button again, nothing changes.

**Ready for HA** shows **Yes** when all these things are true:

- At least three servers are ready and accept new work. Control plane servers count. Ephemeral servers of a [pool](/docs/guides/node-pools/#what-runs-on-a-pools-nodes) do not count, because they cannot hold a database.
- The cluster has storage for databases.
- The cluster is ready.
- The add-ons are ready.

## Create a highly available database

**On a shared database server.** Open **Settings** > **Databases**, and set up a server. Turn on **High availability**. The switch is available for PostgreSQL and MySQL. Until **Ready for HA** shows **Yes**, the switch is off and shows "Needs at least three healthy nodes." See [Shared database servers](/docs/guides/shared-database-servers/).

**On a project database.** The create dialog has no high availability option. Use the API:

```bash
curl -sS -X POST https://geass.example.com/api/databases/create \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d name=orders -d project=morning-beach -d environment=production \
  -d engine=Postgres -d highAvailability=on
```

A database that you create through the API is a draft. To start it, click **Deploy changes**. You cannot change a database between one instance and high availability later.

### While Geass waits for three servers

If the readiness check does not show **Yes**, Geass waits before it creates the database. The dashboard shows the database as **Unavailable**. Geass checks again every 30 seconds, and does not mark the database as failed. To fix this, see [Troubleshooting](#troubleshooting).

Geass refuses high availability for Redis. The capacity check counts all the instances. For MySQL, it counts at least 1 GB of memory for each instance. See [Scaling and placement](/docs/guides/scaling-and-placement/).

## Where the instances run

- **Different servers.** Geass spreads the instances of a PostgreSQL, MySQL, or MariaDB database over different servers where it can. This is a preference, not a rule. A region with fewer servers than instances still runs the database on the servers that it has.
- **The project's region and tags.** The instances stay in the region of the project, and on servers with the [compliance node tags](/docs/guides/compliance-tags/) that the project requires.
- **Not on ephemeral servers.** The instances never run on a server that keeps no data, for example a pool server with no data allowed.
- **One copy of each volume.** Each instance keeps its volume on its own server only, because the instances already hold three copies of the data. See [Storage](/docs/guides/storage/).
- **Not by site.** Geass does not spread the instances of a database over sites.

The readiness check counts servers in the whole cluster, not in one region. A region with one server passes the check, and all three instances then share that server. Before you put a highly available database in a region, give the region at least three servers. The same applies to required tags.

### MariaDB

A highly available MariaDB database has three or five servers, and one of them is the primary. A write is on a replica before the primary confirms it. The address of the database follows the primary.

When the primary stops, a replica becomes the primary. In a test, this took ten seconds, and no write was lost. When the machine of the primary stops answering, Geass waits one minute. Then it names the replica that has the most data, on a server that is ready, as the new primary. If the switch does not finish in ten minutes, Geass stops it.

This works only when the database servers are on three different machines. Backups are continuous, so you can restore to any moment. See [Backups and restore](/docs/guides/backups-and-restore/#mariadb-with-high-availability-copies-and-archived-logs) and [High availability for MariaDB](/docs/guides/databases/#high-availability-for-mariadb).

### MySQL

A highly available MySQL database has three or five servers, and one of them is the primary. The primary confirms a write only after a replica has it, so the loss of the primary loses no confirmed write. The address of the database follows the primary. Apps reconnect after a few seconds.

Geass tested this on three machines, with one MySQL server on each:

| What was lost | What happened |
| --- | --- |
| The process of the primary | A replica was the primary within seconds. Writes failed for about four seconds. |
| The whole machine of the primary | A replica was the primary after about 36 seconds. Writes failed for about 45 seconds. |
| The primary froze but did not stop | A replica was the primary after about three minutes. |

Give a highly available MySQL database three servers. In a test on two servers, two of the three MySQL servers shared one machine. When that machine was lost, no replica became the primary until it came back. See [High availability for MySQL](/docs/guides/databases/#high-availability-for-mysql).

### Redis

Redis runs as one instance. If you ask for high availability or more than one instance, Geass refuses with "High availability requires PostgreSQL, MySQL, or MariaDB: Redis runs as a single instance. Turn it off, or choose PostgreSQL, MySQL, or MariaDB". Redis keeps no data on disk, so it starts empty after a restart.

### Services

A service with more than one replica continues to serve while some of its replicas run. Geass asks for the replicas to go on different servers. When every server has a **Site**, Geass also asks for the replicas to go on different sites.

These rules apply:

- The spread is a preference. On one server, or with too few servers, all replicas still run, and two replicas can share a server.
- Geass places a replica only when it starts. Geass does not move replicas when a server comes back.
- A service with one replica has nothing to spread. While its server is lost, the service is down for at least five minutes.

## When a node is lost

| What was on the server | What happens |
| --- | --- |
| A highly available PostgreSQL instance | If it was the primary, another instance becomes the primary. The address of the database follows it. After [15 minutes](#what-geass-does-after-fifteen-minutes), Geass builds a new instance on another server. |
| A highly available MySQL or MariaDB server | If it was the primary, a replica becomes the primary. The lost replica comes back when its server comes back. Geass does not replace it. |
| A single-instance database | Unavailable until it runs again. With replicated storage, it starts on another server: MySQL and SQLite after a few minutes, PostgreSQL after [15 minutes](#what-geass-does-after-fifteen-minutes) plus up to six minutes. Without replicated storage, its data is on the lost server, so it waits for that server. See [Storage](/docs/guides/storage/). |
| A Redis database | It starts empty on another server after [15 minutes](#what-geass-does-after-fifteen-minutes). |
| A service replica | It starts on another server that matches the project's region and tags, after five minutes. |
| One of three control plane servers | The cluster continues to work. If Geass ran there, it starts on another server in one to two minutes. |
| The only control plane server | See [What is not highly available](#what-is-not-highly-available). |

### What Geass does after fifteen minutes

A database cannot start a second copy while its first copy might still write to the volume. So when a server stops answering, its databases wait. When a server has not been ready for **15 minutes**, Geass decides that the server is gone. Then Geass does these things:

- It starts each single-instance database from the lost server on another server, if its volume has copies there.
- It replaces each highly available PostgreSQL instance whose volume was only on the lost server. It builds the new instance from the primary. A small database is built again in about one minute.
- It does the same for an instance on a server that is up, if its volume lost its only copy and it has not started for 15 minutes. For example, the disk was replaced.
- When you remove a server from the cluster, it replaces such an instance after 3 minutes. Before you confirm the removal, the **Remove server** dialog lists what is kept only on that server.

Geass replaces an instance only while the primary is up. If the primary was also on the lost server, the newest data might be there, so Geass waits for a new primary first.

:::caution
Turn off the 15-minute limit before you shut down a server for longer than 15 minutes. A replacement deletes the volume of the instance. The dashboard has no setting for this limit yet. See [Under the hood](#under-the-hood).
:::

Geass cannot tell a stopped machine from a machine that is cut off from the network but still runs. It assumes that a server that does not answer for this long has stopped.

Geass also installs its agent again on a worker that it joined over SSH and that stays not ready for five minutes. If the machine is gone, this fails, and the server shows the error. See [Troubleshooting](/docs/operations/troubleshooting/).

## What is not highly available

:::caution
Join two more control plane servers to remove the single point of failure. A new install has one control plane server. See [Run three control plane servers](#run-three-control-plane-servers).
:::

- **A single control plane server.** It holds the state of the cluster and runs Geass. While it is down, the dashboard is unavailable. You cannot deploy, scale, or restore anything. Services on other servers usually continue to run, but nothing restarts them elsewhere.
- **Backups of the cluster's state.** By default, the snapshots stay on the disk of each server. They are lost with the disk, unless you send them to your own S3 bucket. See [Disaster recovery](/docs/operations/disaster-recovery/).
- **Incoming traffic and DNS.** Geass does not move a domain from one address to another when a server is lost.
- **Object storage and monitoring.** The object storage server, metrics, and logs each run one instance. With replicated storage, the data of the object storage server has copies on other servers, but the server itself is not highly available.
- **A whole site.** A cluster at more than one provider is experimental, and its control plane is at one site. See [Nodes at more than one provider](/docs/operations/multi-provider/).

## Troubleshooting

**A highly available database stays Unavailable.** Do these checks in this order:

1. Open **Usage**, and look at **Healthy nodes**. You need three servers that are ready and accept new work. A cordoned server does not count. To let new work start on it, select **Uncordon** in its menu.
2. Make sure that somebody clicked **Run readiness check**. Without the first check, Geass never creates the database.
3. Look at **Ready for HA**. If it shows **No**, find the row in the table under it with the result **Not passed**. For example, an add-on might still be installing.

**The High availability switch is off on the shared server form.** **Ready for HA** does not show **Yes**. Run the check, or fix what it reports.

**The check shows Yes, but the three instances are on one server.** The check counts servers in the whole cluster, not in the region. Add servers to the region, and create the database again. Geass places a database one time, so a later change does not move it.

**One instance does not start after a server was lost.** Its volume had its only copy on the lost server. Bring the server back, or wait for Geass to replace the instance. You can also restore the database from a backup into a new database.

**A new control plane server does not join, and the installer stops with "this server did not join".** The first server might have been installed without secrets encryption. To confirm the cause, see [Under the hood](#under-the-hood).

## Under the hood

The control plane uses K3s with embedded etcd. The readiness check is a `GeassHAReadiness` object named `platform` in `geass-system`. A highly available PostgreSQL database is a CloudNativePG `Cluster`. MySQL uses MOCO, and MariaDB uses the MariaDB operator.

```bash
# The control plane servers (true in both columns)
sudo k3s kubectl get nodes -L node-role.kubernetes.io/control-plane,node-role.kubernetes.io/etcd
# The readiness check and its conditions (NodesReady, StorageReady, ClusterReady, AddonsReady, Ready). The dashboard shows them in a table.
sudo k3s kubectl -n geass-system get geasshareadiness platform -o jsonpath='{.status.conditions}'
# Run the readiness check again now
sudo k3s kubectl -n geass-system annotate geasshareadiness platform geass.dev/recheck="$(date +%s)" --overwrite
# Why a highly available database waits
sudo k3s kubectl -n geass-system get geassdatabase orders -o jsonpath='{.status.conditions[?(@.type=="Ready")].message}{"\n"}'
# Where the instances of a PostgreSQL database run
sudo k3s kubectl get pods -n morning-beach-production -l cnpg.io/cluster=orders -o wide
```

- A highly available database can also be a `GeassDatabase` with `highAvailability: true`, or `instances: 3` or more. A manifest is not a draft. Geass starts it when you apply it.
- While Geass waits for three servers, the `Ready` condition of the database says "High availability requires a cluster with at least three healthy nodes".
- The **Site** of a server is the `topology.kubernetes.io/zone` label.
- With Longhorn, a highly available PostgreSQL or MySQL database uses the `geass-single` storage class.
- Geass sets the Longhorn setting for pods on a down node to replace those pods, if the setting still has its default value `do-nothing`.
- Traefik and CoreDNS run one replica each. They move after the Kubernetes default of five minutes.
- The address that new workers join is `spec.serverURL` on the `GeassCluster`.

### Commands on the servers

The dashboard and the command line cannot do these tasks yet.

**Read the join token.** On the first server, run `sudo cat /var/lib/rancher/k3s/server/token`.

**Open the ports on the first server.** If `ufw` is active on the first server, run this command there:

```bash
sudo ufw allow 2379/tcp && sudo ufw allow 2380/tcp
```

**Remove a control plane server permanently.** Do these steps in this order, while the other servers are up:

1. On **another** server, delete the server from the cluster:

   ```bash
   sudo k3s kubectl delete node <name>
   ```

2. On the server that you remove, uninstall K3s:

   ```bash
   sudo /usr/local/bin/k3s-uninstall.sh
   ```

If you uninstall K3s first, the cluster keeps counting the missing server. That is one more failure that the cluster must survive.

**Turn off the 15-minute limit.** Run this command on the control plane server:

```bash
sudo k3s kubectl -n geass-system patch geasscluster default --type merge -p '{"spec":{"lostNodeAfter":"0s"}}'
```

To turn it on again, set a duration, for example `15m`. See [GeassCluster](/docs/reference/resources/geass-cluster/).

**Find why a control plane server did not join.** On the new server, read the K3s log with `sudo journalctl -u k3s`. If the first server has no secrets encryption, the log shows `critical configuration mismatched: secrets-encryption`.

## Related

- [Shared database servers](/docs/guides/shared-database-servers/)
- [Databases](/docs/guides/databases/)
- [Storage](/docs/guides/storage/)
- [Backups and restore](/docs/guides/backups-and-restore/)
- [Disaster recovery](/docs/operations/disaster-recovery/)
- [Nodes at more than one provider](/docs/operations/multi-provider/)
