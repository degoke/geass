---
title: "Sandboxed runtime for organizations"
description: "Run the services, builds, and database servers of organizations under gVisor or Kata: node setup, turn-on, failing closed, database moves, and limits."
---

With [organizations](/docs/guides/organizations-and-workspaces/), the pods of every organization still run on the same
nodes. A container shares the node's kernel. So code that escapes its container gets to the node, and to the pods of
every other organization on that node. If the organizations of a cluster do not trust each other, run their code
under a **sandboxed runtime**. You can use one of these runtimes:

- [gVisor](https://gvisor.dev), which gives each pod its own kernel in user space.
- [Kata Containers](https://katacontainers.io), which gives each pod a small virtual machine.

Kubernetes selects the runtime by a [RuntimeClass](https://kubernetes.io/docs/concepts/containers/runtime-class/).
Geass puts the name of the RuntimeClass on the pods of organizations' projects and on their
[database servers](#database-servers).

The sandboxed runtime is off until you set it. While it is off, no pod changes.

:::caution[Tested on real K3s with gVisor, not with Kata]
Before you rely on the sandboxed runtime, try it on one cluster, with your own services and a copy of your own data.

Geass's tests check the pods that Geass writes against a fake API server and envtest. The tests also check that Geass
waits when the RuntimeClass is missing. Geass has also run on real K3s v1.36.4+k3s1 with gVisor `release-20261019.0`.
It ran in two setups: a K3s server in a Docker container, and K3s installed on an Ubuntu 24.04 host.
[Prepare the nodes](#prepare-the-nodes) tells you how. On the host, these were the results:

- With the RuntimeClass missing, running services kept the same pods and said why. Geass did not make a new service
  and a new MySQL server. When the RuntimeClass appeared, organizations' services rolled into the sandbox, and Geass's
  own pods did not.
- A Kaniko build (v1.23.2) of an organization's project ran under gVisor and pushed to the organization's own registry.
- MySQL 8.4.11 (1 GiB) and Redis 7.4.11 servers ran under gVisor and answered. The Job that makes a logical database
  on the MySQL server also ran under gVisor.
- A new rqlite v8.36.16 server started and was `Ready` under gVisor. Nobody sent queries to it.
- A running server moved into the sandbox when its restart was approved, and with `databaseRestarts: Immediate`.
- Geass refused an organization's PostgreSQL, held one made before, and refused MariaDB.
- Two things went wrong:
  - A MySQL server at Geass's default size of 512 MiB was killed during its first start, and was left without its
    logins. See [What it costs](#what-it-costs).
  - A SQLite server made before Geass gave rqlite a fixed node ID does not come back from a restart. See
    [Servers that already run](#servers-that-already-run).

**Not tried:** Kata, `skipOnReservedNodes` on real reserved nodes, highly available MySQL on MOCO, CloudNativePG's
`podPatch`, and database servers under load.
:::

## What runs in the sandbox

When `spec.tenancy.sandbox` is set on the [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/),
Geass gives `runtimeClassName` to the pods that it makes for a project that belongs to an organization. Such a project
has `spec.org` set.

| Pods | Sandboxed |
| --- | --- |
| A service's copies (its Deployment), including the services of [pull request environments](/docs/guides/pull-request-environments/) | Yes |
| The pods of a [function's schedules](/docs/guides/functions/) (CronJobs) | Yes |
| [Builds](/docs/guides/github-and-builds/) (the Kaniko Job in the organization's `geass-build-<organization>` namespace), which run the repository's Dockerfile | Yes |
| An organization's MySQL, Redis, and SQLite servers: the databases of its projects and the [shared servers](/docs/guides/shared-database-servers/) that it owns, highly available MySQL included | Yes, when Geass makes the server. A server that already runs moves when its restart is approved. See [Servers that already run](#servers-that-already-run) |
| The Jobs that copy and restore the data of those servers, make their logins, and make and drop [logical databases](/docs/reference/resources/geass-logical-database/) on them | Yes, from their next run |
| An organization's PostgreSQL and MariaDB servers | No. Their operators cannot run them under a RuntimeClass, so Geass does not make them for an organization. See [Engines that cannot run in it](#engines-that-cannot-run-in-it) |
| All parts of a project that belongs to no organization, and the platform's shared servers | No. These belong to the platform |
| The object storage server, and what database operators run themselves | No. See [What it does not cover](#what-it-does-not-cover) |
| Geass's own pods (the operator, the network policy check) and the add-ons | No |

A project that names an organization is sandboxed whether or not organizations are turned on for sign-in
(`spec.auth.tenancy.enabled`). So if you switch sign-in back to passwords for a while, the sandbox stays. A project
that a pod names, but that Geass cannot find, is also sandboxed.

## Prepare the nodes

Install the runtime on **every node an organization's pods can run on**, and make the RuntimeClass. On a shared
cluster, these nodes are all of the workers. A node without the runtime cannot start a sandboxed pod. The pod stays
`ContainerCreating` with a `FailedCreatePodSandBox` event (`no runtime for "runsc" is configured`).

Nodes reserved for one organization can be without the runtime only with `skipOnReservedNodes`, and only while its
rule holds. See [Reserved nodes](#reserved-nodes). When the rule stops holding, the pods of that organization are
sandboxed and cannot start on those nodes.

These steps are for gVisor on K3s, which runs its own containerd. The steps were run on K3s v1.36.4+k3s1 with gVisor
`release-20261019.0`, in two setups:

- One time in a K3s server in a Docker container on an Ubuntu 24.04 host.
- One time on K3s installed on an Ubuntu 24.04.5 host (kernel `7.0.0-1014-aws`).

Then an organization's service ran under the `gvisor` RuntimeClass. Inside its pod, `dmesg` printed
`Starting gVisor...`, and `uname` showed `4.19.0-gvisor` where the host shows its own kernel. Check the steps against
the [gVisor installation guide](https://gvisor.dev/docs/user_guide/install/) and K3s's
[containerd configuration](https://docs.k3s.io/advanced#configuring-containerd) for your releases. gVisor has changed
how it ships before.

1. **Install `runsc` and its containerd shim** on each node:

   ```bash title="Install gVisor"
   ARCH=$(uname -m)
   URL=https://storage.googleapis.com/gvisor/releases/release/latest/${ARCH}
   wget ${URL}/gvisor.tar.bz2 ${URL}/gvisor.tar.bz2.sha512
   sha512sum -c gvisor.tar.bz2.sha512
   tar -xjf gvisor.tar.bz2
   # The tarball has runsc, containerd-shim-runsc-v1 and a gvisor-bin directory of helpers that runsc needs beside it.
   sudo mv runsc containerd-shim-runsc-v1 gvisor-bin /usr/local/bin
   ```

   An earlier layout had `runsc` and the shim as separate files under the same address. Now a release is the tarball,
   and also a `.zstd` tarball if you prefer it. If the address answers 404, list `gs://gvisor/releases/release/` to see
   what is there.

   `tar -xjf` needs `bzip2`. The minimal Ubuntu 24.04 image does not have `bzip2`, and `tar` stopped with
   `bzip2: Cannot exec`. Install `bzip2` first (`sudo apt-get install -y bzip2`), or use the `.zstd` tarball.

2. **Tell K3s's containerd about it.** K3s writes its containerd configuration itself on every start, from a template
   that you can extend. Recent K3s releases ship containerd 2, whose file is `config-v3.toml.tmpl`. An older release
   with containerd 1.7 reads `config.toml.tmpl`. To see which file K3s generated, run
   `sudo ls /var/lib/rancher/k3s/agent/etc/containerd/`. On a host install, the directory belongs to root.

   ```toml title="/var/lib/rancher/k3s/agent/etc/containerd/config-v3.toml.tmpl (containerd 2)"
   {{ template "base" . }}

   [plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runsc]
     runtime_type = "io.containerd.runsc.v1"
   ```

   ```toml title="/var/lib/rancher/k3s/agent/etc/containerd/config.toml.tmpl (containerd 1.7)"
   {{ template "base" . }}

   [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runsc]
     runtime_type = "io.containerd.runsc.v1"
   ```

   Then restart K3s: `sudo systemctl restart k3s-agent` on a worker, `k3s` on a server. Check that the generated file
   has the `runsc` runtime. On K3s v1.36.4+k3s1 (containerd 2.3.4), the generated file was `config.toml`. The file had
   `version = 3` and a header that said to use `config-v3.toml.tmpl`. After the restart, the file had the `runsc`
   runtime.

3. **Make the RuntimeClass** one time for the cluster. The name of the RuntimeClass is the name that you give to
   Geass. Its `handler` is the name of the runtime in the containerd configuration.

   ```yaml title="runtimeclass-gvisor.yaml"
   apiVersion: node.k8s.io/v1
   kind: RuntimeClass
   metadata:
     name: gvisor
   handler: runsc
   # Only if some nodes have no gVisor: keep sandboxed pods to the nodes that carry this label.
   # scheduling:
   #   nodeSelector:
   #     sandbox.example.com/gvisor: "true"
   # Only to account for the sandbox's own memory and CPU. Kubernetes adds it to each pod, and to what the
   # namespace's quota counts.
   # overhead:
   #   podFixed:
   #     cpu: 50m
   #     memory: 64Mi
   ```

   ```bash
   kubectl apply -f runtimeclass-gvisor.yaml
   ```

4. **Check one pod by hand** before Geass sends the code of every organization to the sandbox:

   ```bash title="A pod under gVisor"
   kubectl run sandbox-check --rm -it --restart=Never --image=busybox \
     --overrides='{"apiVersion":"v1","spec":{"runtimeClassName":"gvisor"}}' -- dmesg
   ```

   If you see gVisor's own boot messages (`Starting gVisor...`), the pod ran in the sandbox.

For Kata Containers, install Kata with its `kata-deploy`. Then give Geass the name of one of the RuntimeClasses that
`kata-deploy` makes, for example `kata-qemu`. Kata runs a virtual machine for each pod. So the nodes need hardware
virtualization: bare metal, or instances that allow nested virtualization.

## Turn it on

```yaml title="platform.yaml (spec.tenancy)"
spec:
  tenancy:
    sandbox:
      runtimeClassName: gvisor
      skipOnReservedNodes: false
      # When a database server that already runs is restarted into the sandbox: OnApproval (the default) or Immediate.
      databaseRestarts: OnApproval
```

```bash
kubectl -n geass-system patch geassplatformconfig platform --type merge \
  -p '{"spec":{"tenancy":{"sandbox":{"runtimeClassName":"gvisor"}}}}'
```

There is no form for this setting in the dashboard.

This is what happens to the workloads that already run:

- **Services roll immediately.** The setting gets to every service. An organization's service gets a pod template with
  the RuntimeClass. So its Deployment replaces its copies the way it rolls out any change:
  - A few copies at a time, and never more than a quarter of them down at one time.
  - A service with one to three copies starts a new copy before it stops an old one.

  The services of all organizations start to roll at the same time. So a cluster that is nearly full needs room for
  the extra copies while they roll. Until there is room, the copies that are not replaced yet continue to run. If you
  turn the setting off, or change the class, the services roll again in the same way. A service whose rollout is
  paused, or that is not deployed, gets the change when it next deploys.
- **Builds take it from their next run.** A build that is running finishes as it started.
- **Schedules**: Geass rewrites them with the service, and their next run is sandboxed.
- **Database servers are not restarted for it.** Geass makes a new server in the sandbox. A server that already runs
  continues to run as it does, until a platform administrator approves its restart, or until `databaseRestarts` is
  `Immediate`. Geass does not make an organization's Postgres or MariaDB, or holds it as it is. See
  [Database servers](#database-servers).

## When the RuntimeClass is missing

Geass never starts the code of an organization without the sandbox. While the cluster has no RuntimeClass of that
name, these things occur:

- **A service waits.** Geass does not write its Deployment. A new service gets no Deployment, and a service that was
  already running keeps exactly the pods that it had. The service is not ready, with the message
  `Waiting for the sandboxed runtime: RuntimeClass "gvisor" is not in the cluster, ...`. Its `Sandboxed` condition is
  `False` with the reason `RuntimeClassMissing`. When the wait begins, the service gets one `Warning` event,
  `SandboxRuntimeClassMissing`. The wait does not count against the retry limit, so Geass does not give up on the
  service.
- **A build waits** in `Pending`, with the same condition and event. The build waits before Geass makes any part of it
  or copies any part of it into the organization's namespace. The service that waits for the build shows the build's
  message.
- **A database server waits**, and Geass makes no part of it: no volume, Secret, Service, or pod. The server is not
  ready, with the same message, condition, and event. The wait does not count as a failure. Geass leaves a server that
  already runs exactly as it is:
  - Geass writes no change to the server.
  - The pods of the server continue to run and serve.
  - The server's `Ready` condition keeps what it last said.
- **The Jobs on an organization's data are not made.** A
  [logical database](/docs/reference/resources/geass-logical-database/) waits like a server, with its own condition
  and event. A database that is being deleted skips its final copy. The copies taken before stay. A logical database
  that is being deleted is not dropped from its server. Geass releases it after three minutes, the same as a logical
  database whose server is not ready. Its database and login stay on the server.
- **When the class appears**, services and database servers continue immediately, because the operator watches
  RuntimeClasses. Builds and logical databases continue within half a minute.

If the RuntimeClass is deleted later, the same thing occurs to what changes next. Kubernetes itself never runs a pod
whose RuntimeClass does not exist with another runtime. The API server refuses the pod, or the kubelet fails it. So a
copy that a Deployment replaces in the meantime does not start without the sandbox either.

```bash title="See what waits"
kubectl -n geass-system get geassapps,geassbuilds,geassdatabases,geasslogicaldatabases -o json | jq -r '.items[]
  | select(any(.status.conditions[]?; .type == "Sandboxed" and .reason == "RuntimeClassMissing")) | .metadata.name'
kubectl -n geass-system get events --field-selector reason=SandboxRuntimeClassMissing
```

## Reserved nodes

An organization can have nodes of its own: a node pool whose tag is reserved, and that the organization requires of
all its projects. See [Scaling and placement](/docs/guides/scaling-and-placement/). That organization shares those
nodes with nobody, and can run there without the sandbox. To allow this, set `skipOnReservedNodes: true`. A pod then
runs without the sandbox only when all of these conditions are true when Geass writes the pod:

1. The pod's node selector asks for a tag. So the pod can be scheduled only on nodes that have the tag.
2. The pod's organization requires that tag of all its projects. This is the `spec.requiredTags` of the
   [GeassOrganization](/docs/reference/resources/geass-organization/), which only a platform administrator sets.
3. No other organization requires the same tag.
4. Every node that has the tag is reserved for it: tainted `tag.geass.dev/<tag>`, `NoSchedule` or `NoExecute`. So
   nothing that does not ask for the tag is scheduled there. There is also at least one such node.

In all less certain cases, the pod is sandboxed. The service's `Sandboxed` condition tells you which: `True` with
`RuntimeClass`, or `False` with `ReservedNodes` and the tag.

One of the four conditions can stop being true. For example, another organization starts to require the tag, a node
with the tag is no longer reserved, or the organization stops requiring the tag. Then the operator detects the change
and rolls the organization's services into the sandbox. If the RuntimeClass is missing, the operator holds the
services instead. Their copies continue to run as they are, and the operator starts no copy without the sandbox.
Builds take the change from their next run.

A reserved node without the sandbox is a node that its organization's code can take over, the same as any node without
the sandbox. All else that runs on that node, for example the DaemonSets of the add-ons, is exposed to that
organization.

## Database servers

A database server runs what its users send it. A login with enough rights runs programs through the server. For
example, Postgres has `COPY ... PROGRAM` and untrusted languages, and MySQL can load a plugin or a function from a
file. The server's own code also reads all that the organization stores. So with `spec.tenancy.sandbox` set, an
organization's database server runs under the RuntimeClass, the same as its services. These are an organization's
database servers:

- A database of a project that belongs to an organization.
- A [shared server](/docs/guides/shared-database-servers/) whose `spec.org` names an organization.

The decision follows the same rules as for services:

- With `skipOnReservedNodes`, a server that can run only on nodes reserved for its organization can run without the
  sandbox.
- Geass makes nothing while the RuntimeClass is missing.
- The decision does not depend on whether organizations are on for sign-in. So a change to sign-in never restarts a
  database.

The platform's shared servers (no `spec.org`) are not sandboxed, the same as the other pods of the platform. While
organizations are on, these servers hold only the databases of projects of no organization, which belong to the
platform. See [Organizations](/docs/guides/shared-database-servers/#organizations). While organizations are off, the
platform's server might also hold a database of an organization's project. Then that server is not sandboxed, but the
Jobs that make and drop that database are sandboxed.

### How each engine is run

| Engine | What runs it | Can its pods carry a RuntimeClass? | For an organization, with the sandbox on |
| --- | --- | --- | --- |
| PostgreSQL | The CloudNativePG operator that Geass installs (chart 0.23.2, CloudNativePG 1.25.1), as a `Cluster` | No. The `Cluster` has no `runtimeClassName` and no pod template. Only a `Pooler` has one, and Geass does not use a `Pooler`. CloudNativePG 1.26 added the `cnpg.io/podPatch` annotation. This annotation can patch the instance pods, but not the Jobs that make, restore, and join instances. It also does not restart the instance pods by itself. 1.30 has nothing more | Not made. Held as it is when made before |
| MySQL, one server | Geass's own StatefulSet `geass-mysql-<name>`, from the official image | Yes | In the sandbox |
| MySQL, highly available | MOCO (chart 0.27.0, MOCO 0.37.0), as a `MySQLCluster`. Its `spec.podTemplate.spec` is a whole pod spec, from which MOCO makes the pod of each server | Yes. `spec.podTemplate.spec.runtimeClassName` gets to the pod of every server, including MOCO's agent beside it | In the sandbox |
| MySQL still on the Bitnami chart that made it | The chart, which Geass leaves as it is | Geass changes nothing in it | Held as it is |
| MariaDB | mariadb-operator 26.10.1, as a `MariaDB` | No. No field of its `MariaDB`, or of any other kind that it has (`PhysicalBackup`, `PointInTimeRecovery`, `Backup`, `Restore`, `SqlJob`, `MaxScale`), names a RuntimeClass, up to 26.10.2, the newest | Not made. Held as it is when made before |
| Redis | Geass's own StatefulSet, from the official image | Yes | In the sandbox |
| SQLite | Geass's own Deployment of an [rqlite](https://rqlite.io) server on a volume. It is a server of its own, not a file in the app's pod, so the app's sandbox does not cover it | Yes | In the sandbox |
| AWS RDS, PlanetScale | The provider. No part of it runs in the cluster | Not applicable | Unchanged |

These are the other pods that hold or touch an organization's data:

| Pods | Made by | With the sandbox on |
| --- | --- | --- |
| Copies and restores of MySQL, MariaDB (one server), and SQLite: the first copy, the nightly CronJob, a restore, and the last copy taken when the database is deleted | Geass | In the sandbox, from their next run |
| The Job that makes the application's login on a MOCO cluster | Geass | In the sandbox |
| The Jobs that make and drop a logical database on a server | Geass | In the sandbox when the server or the database's project belongs to an organization |
| PostgreSQL's backups and the archiving of its log | CloudNativePG, inside the server's own pods | The same as its server. Geass does not make an organization's server |
| CloudNativePG's Jobs that make, restore, and join an instance | CloudNativePG | Not sandboxed. Geass does not make an organization's server |
| MariaDB's physical copies, its point-in-time recovery, and the CronJob that closes its binary log | mariadb-operator, and Geass | Not sandboxed. Geass does not make an organization's server |
| The database console, which runs its client inside the server's pod | The server's pod | The same as its server |
| The object storage server | Geass, one for the whole cluster | Not sandboxed. See [What it does not cover](#what-it-does-not-cover) |

### Engines that cannot run in it

While the sandbox is on, Geass never runs an organization's PostgreSQL or MariaDB server outside the sandbox:

- **A new one is not made.** The dashboard and the API refuse it with `400`. This applies to the database form, a
  shared server for an organization, a restore, and a template. The message is: "This database cannot run in the
  sandbox: PostgreSQL runs on the CloudNativePG operator, and the release Geass installs (chart 0.23.2, CloudNativePG
  1.25) has no way to give a server's pods a RuntimeClass, and an organization's database server is not started
  outside the sandbox that the platform runs organizations' code in, so it is not made. Choose MySQL, Redis or SQLite
  instead, or ask a platform administrator".

  If someone writes a server past the dashboard, with `kubectl` or as the copy of a pull request environment, the
  operator refuses it:
  - Nothing of the server is made, not even its operator.
  - `Ready` is `False` with the same message. This message does not count as a failure.
  - Its `Sandboxed` condition is `False` with the reason `EngineCannotBeSandboxed`.
  - It gets one `Warning` event, `SandboxEngineUnsupported`.

  A server on nodes reserved for its organization (with `skipOnReservedNodes`) needs no sandbox, and Geass does not
  refuse it.
- **One made before is held.** Geass deletes nothing of it, stops nothing, and changes nothing. Its pods continue to
  run outside the sandbox, and continue to serve its apps and logical databases. Its `Ready` condition keeps what it
  last said. But Geass writes nothing to the server while it is held:
  - Geass does not apply a new size, version, or setting.
  - Its backups continue only as they were already set up.
  - Geass does not refresh its status.

  The server tells you this on `Sandboxed` (`False`, `EngineCannotBeSandboxed`): "This database cannot run in the
  sandbox: …. It was made before, so it keeps running as it is, outside the sandbox, but Geass changes nothing of it
  …". It also gets one `Warning` event `SandboxEngineUnsupported`. You can delete the server as usual. When the
  sandbox is turned off, Geass manages the server again immediately.
- **A shared PostgreSQL or MariaDB server is not given to an organization**. Geass refuses
  `POST /api/shared-databases/{name}/org` with `409`.

To end a hold, move the data to a MySQL server, or turn the sandbox off. To move the data, dump and load it, as for
any change of engine. A CloudNativePG of 1.26 or later could put the RuntimeClass on the instance pods of Postgres
through its `cnpg.io/podPatch` annotation. But Geass does not install such a release, and that patch would leave the
Jobs of CloudNativePG outside the sandbox.

```bash title="See which servers cannot run in the sandbox"
kubectl -n geass-system get geassdatabases -o json | jq -r '.items[]
  | select(any(.status.conditions[]?; .type == "Sandboxed" and .reason == "EngineCannotBeSandboxed")) | .metadata.name'
```

### Servers that already run

A database restart is not the same as a service rollout. A server of one pod is down while it restarts. A Redis
server keeps nothing on disk, so it comes back empty. So Geass does not restart a running server because the setting
changed. Geass writes the pods of a server under the RuntimeClass at these times:

- **Immediately**, when Geass makes the server, and while the server has never been ready. Such a server holds
  nothing in use yet.
- **When a platform administrator approves it**, one server at a time:

  ```bash
  kubectl -n geass-system annotate geassdatabase <name> geass.dev/sandbox-restart=true
  ```

  Geass then writes the server's pods under the RuntimeClass, and this restarts the server. Geass records a
  `SandboxRestart` event and removes the annotation.
- **All together**: every waiting server at the same time, with `databaseRestarts: Immediate` on
  `spec.tenancy.sandbox`.

The default is to wait (`databaseRestarts: OnApproval`, also when it is not set). Until the server moves, it runs as
it did, and tells you so. Its `Sandboxed` condition is `False` with the reason `RestartPending` and the message "Runs
without the sandbox until it is restarted into RuntimeClass gvisor, which restarts the database. A platform
administrator annotates it geass.dev/sandbox-restart to restart it now, or sets spec.tenancy.sandbox.databaseRestarts
to Immediate". It also gets one `Warning` event `SandboxRestartPending`.

Meanwhile, Geass manages the server as usual, as it runs. A change that restarts the server anyway, for example a new
size, restarts it outside the sandbox. If you want both changes in one restart, approve the move with that change. The
same is true when you turn the setting off and when you change the class. A server keeps the class that it runs with
until its restart is approved. Geass records the class of a server on its GeassDatabase, in the annotation
`geass.dev/sandbox-runtime-class`. Do not edit this annotation.

The restart is the usual restart of the operator or of Kubernetes, and nothing more:

- **MySQL (one server), Redis and SQLite** run one pod each. So the database is unavailable while its pod is replaced
  and starts again. **A Redis server comes back empty.** SQLite's Deployment replaces its one copy as it does for any
  change. These results were seen on K3s v1.36.4+k3s1:
  - A platform administrator approved a MySQL 8.4.11 server of 512 MiB that held data. `mysqld` answered again about
    40 seconds after the annotation, and the data was there.
  - `databaseRestarts: Immediate` moved a Redis, a SQLite, and a MySQL server within seconds. The key of the Redis
    server was gone.
- **Highly available MySQL**: MOCO rolls it one server at a time, from the highest-numbered, and only while the
  cluster is healthy. When the primary's pod is deleted, MOCO switches the primary to a replica. So writes pause for a
  few seconds then.
- **PostgreSQL and MariaDB** are never restarted for it, because they cannot run in the sandbox.

**A SQLite server made before Geass gave rqlite a fixed node ID does not come back from this restart, or from any other
replacement of its pod.** Without `-node-id`, rqlite names its node after the host name of the pod. A pod of a
Deployment has a new name every time. So the new node is not in the raft configuration that rqlite keeps on the
volume. rqlite answers `503` ("not part of a stable configuration") and does not recover.

rqlite v8.36.16 did this to a server restarted into the sandbox. It did the same to a server of a project of no
organization, never sandboxed, whose pod was deleted. In the same test, the same image with a fixed `-node-id` on a
volume answered with its data after a pod replacement. A server that this release makes starts with
`-node-id <database name>`. Unit tests check the arguments, but a server made by this release was not run.

For a server made earlier, you must recover its raft configuration, or move its data to a new server. rqlite's own
documentation describes the recovery, but it was not tried here. Geass has no tool for either task.

Approve one server at a time, at a quiet hour, with its backups up to date.

```bash title="See which servers wait for their restart"
kubectl -n geass-system get geassdatabases -o json | jq -r '.items[]
  | select(any(.status.conditions[]?; .type == "Sandboxed" and .reason == "RestartPending")) | .metadata.name'
```

### The Jobs on their data

The Jobs that copy, restore, and make databases and logins on an organization's server take the RuntimeClass from
their next run. This is true whether the server itself has moved yet or not. A Job that already exists keeps how it
runs, because Kubernetes does not let the pod of a Job change. A logical database's `Sandboxed` condition tells you how
the Job that made it ran.

### What it costs

- **Speed.** gVisor runs its own kernel in user space, and every system call and file access goes through that
  kernel. A database does little else. So a database runs slower under gVisor than on the node's kernel. The amount
  depends on the workload: measure yours. See gVisor's
  [performance guide](https://gvisor.dev/docs/architecture_guide/performance/). Kata runs a virtual machine for each
  pod, with its own memory and its own path to the disk. A test on one machine gave these results:
  - A stored procedure of 20,000 single-row inserts (a commit every 200) took 3.2 to 3.4 seconds on a MySQL 8.4.11
    server under gVisor (1 GiB, 500m CPU). It took 3.7 to 4.0 seconds on a server outside gVisor (512 MiB, 250m CPU).
  - 50 scans of 20,000 rows took 1.3 to 2.1 seconds under gVisor, against 1.8 to 3.3 seconds outside.

  The limits of the two servers were different, and it is one workload. So the test shows only that this one workload
  was not slower under gVisor.
- **Features.** gVisor implements most of Linux, not all of it. Its
  [compatibility list](https://gvisor.dev/docs/user_guide/compatibility/linux/amd64/) marks these items:
  - `io_uring` and Linux AIO (`io_setup` and `io_submit`, which InnoDB uses) as partly supported.
  - `fadvise` as ignored.
  - `mlock` as a stub.
  - Some options of `fallocate` and `madvise` as unsupported.

  Huge pages, direct I/O (`O_DIRECT`), and how the filesystem flushes might not behave as on the node. A server whose
  settings ask for these features might start slower, run slower, or not start. MySQL 8.4.11 started under gVisor
  `release-20261019.0` with `innodb_use_native_aio` on and `innodb_flush_method` `O_DIRECT`. Before you move a
  database, test a copy of it under the RuntimeClass, with its own settings and data. Keep its backups.
- **Memory and CPU.** A RuntimeClass `overhead` is also added to every database pod, and to what the project's quota
  and the usage meter count.
- **MySQL needed more than 512 MiB to start.** Geass's default size for a database is 250m CPU and 512 MiB, with the
  limit equal to the request. Two new MySQL 8.4.11 servers of that size were killed for lack of memory (`OOMKilled`,
  exit code 137). This occurred about a minute into their first start under gVisor, and the servers restarted. The
  entrypoint of the image does not resume an interrupted first start. So after the restart, `mysqld` ran with only
  `root@localhost`, which had no password. There was no `root@%` and no `app` login. These were the effects:
  - The server refused the password in the `<ID>-bootstrap` Secret (`Access denied`).
  - The server said `Ready` ("MySQL database is ready").
  - The first logical database on the server stayed at "creating logical database".

  A server made with 1 GiB started cleanly under gVisor: no restart, and `root@%`, `root@localhost`, and `app@%` were
  all there. When idle, MySQL used about 430 to 440 MiB outside the sandbox and about 490 MiB in it. A server of 512
  MiB that already held data, and that moved in by approval, ran at 500 MiB of its 512. Sizes between 512 MiB and 1
  GiB were not tried. Give at least 1 GiB to a MySQL server that is to start in the sandbox. Before you rely on the
  server, check that its logins work. If a server started broken, you must delete it, with its volume, and make it
  again.
- **A restart** for every server that already runs (see earlier on this page).

## What it does not cover

- **PostgreSQL and MariaDB servers.** Their operators cannot run them under a RuntimeClass. So while the sandbox is
  on, Geass does not make an organization's server of these engines. A server made before is held, and continues to
  run outside the sandbox. See [Engines that cannot run in it](#engines-that-cannot-run-in-it). The operators' own
  Jobs are never sandboxed: the Jobs of CloudNativePG that make, restore, and join an instance, and the copies and
  recovery of mariadb-operator.
- **The platform's shared servers, and the object storage server.** They belong to the platform. The object storage
  server holds the buckets of every project of the cluster, of every organization. It runs no code that an
  organization sends it. An organization gets to it only through the S3 API, with keys for its own buckets. A sandbox
  around the server would not keep organizations apart inside it. The keys do that.
- **A server that waits for its restart**. This server runs outside the sandbox until its restart is approved. See
  [Servers that already run](#servers-that-already-run).
- **Other organizations asking for a reserved tag themselves.** A service's `runOn` tags and a project's own required
  tags can name any tag, including a tag reserved for another organization. That work then runs on the nodes of the
  other organization. The work itself is sandboxed, because its organization does not require the tag. But it runs
  beside the owner's pods, which are not sandboxed with `skipOnReservedNodes`.
- **Capacity.** The check that a new service or database fits counts the nodes of its region and tags. The check does
  not count the `scheduling` of the RuntimeClass. A RuntimeClass `overhead` is added to every pod, to what the
  project's quota counts, and to what the
  [usage meter](/docs/guides/usage-and-capacity/#usage-by-organization-for-billing) bills the organization.
- **Compatibility.** gVisor implements most of Linux, but not all. Some system calls and `/proc` files behave
  differently, and I/O-heavy work runs slower. A service that does not start under gVisor shows the usual
  `The service cannot start: …`, or stays starting. Read its pod's events. A database server that does not start
  shows `Service is starting` until it starts. Read its pod's events and logs, and see [What it costs](#what-it-costs).
- **Watching what the pods do.** A tool on the node that watches system calls (Falco, Tetragon) sees gVisor's own
  process, not the program inside. gVisor handles the organization's system calls, and they never get to the node's
  kernel. So such a tool cannot report a miner in a sandboxed pod. But what a pod uses is still measured. So the CPU
  signal of [abuse detection](/docs/guides/abuse-detection/) works under the sandbox. On K3s v1.36.4+k3s1, the metrics
  server measured a busy loop in a pod under gVisor at its 100m limit, and the signal suspected the pod.

## Related

- [Organizations and workspaces](/docs/guides/organizations-and-workspaces/)
- [Shared database servers](/docs/guides/shared-database-servers/) and [GeassDatabase](/docs/reference/resources/geass-database/)
- [Security](/docs/operations/security/)
- [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/)
- [Scaling and placement](/docs/guides/scaling-and-placement/)
- [Noisy-neighbour limits](/docs/guides/tenant-limits/), [Outbound limits](/docs/guides/outbound-limits/), [Registry isolation for organizations](/docs/operations/registry-isolation/), and [Abuse detection](/docs/guides/abuse-detection/), the other settings for a cluster that organizations share
