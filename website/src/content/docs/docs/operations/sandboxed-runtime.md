---
title: "Sandboxed runtime for organizations"
description: "Run organizations' services, builds and database servers under gVisor or Kata: node setup, turning it on, failing closed, moving databases, and limits."
---

With [organizations](/docs/guides/organizations-and-workspaces/), every organization's pods still run on the same nodes,
and a container shares the node's kernel: code that escapes its container reaches the node, and every other organization's
pods on it. When the organizations of a cluster do not trust each other, run their code under a **sandboxed runtime**
instead: [gVisor](https://gvisor.dev), which gives each pod a kernel of its own in user space, or
[Kata Containers](https://katacontainers.io), which gives each pod a small virtual machine. Kubernetes picks the runtime
by a [RuntimeClass](https://kubernetes.io/docs/concepts/containers/runtime-class/), and Geass puts its name on the pods
of organizations' projects and on their [database servers](#database-servers).

It is off until you set it, and while it is off no pod changes.

:::caution[Run once with gVisor, never with Kata]
Geass's tests check the pods it writes, and that it waits when the RuntimeClass is missing, against a fake API server.
On a real K3s it was run once with gVisor ([Prepare the nodes](#prepare-the-nodes) says on what): while the RuntimeClass
was missing a running service was left as it was and said why, and when it appeared both organizations' services rolled
into the sandbox and Geass's own pods did not; an organization's service then ran under gVisor. Kata has not been run with
Geass, no Kaniko build has been seen to work under either, and `skipOnReservedNodes` has not been tried on real reserved
nodes. **No database server has been run in the sandbox at all**: the tests check what Geass writes for one (the pod
templates, the MOCO cluster, the Jobs), what it refuses and holds back, and that a running server is not restarted until
it is told to, against a fake API server and envtest, with no database operator and no gVisor. Try it on one cluster,
with your own services and a copy of your own data, before you rely on it.
:::

## What runs in the sandbox

With `spec.tenancy.sandbox` set on the [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/), the pods
Geass makes for a project that belongs to an organization (its `spec.org` is set) get `runtimeClassName`:

| Pods | Sandboxed |
| --- | --- |
| A service's copies (its Deployment), including the services of [pull request environments](/docs/guides/pull-request-environments/) | Yes |
| The pods of a [function's schedules](/docs/guides/functions/) (CronJobs) | Yes |
| [Builds](/docs/guides/github-and-builds/) (the Kaniko Job in the organization's `geass-build-<organization>` namespace), which run the repository's Dockerfile | Yes |
| An organization's MySQL, Redis and SQLite servers: the databases of its projects and the [shared servers](/docs/guides/shared-database-servers/) it owns, highly available MySQL included | Yes, when made. One that already runs moves when its restart is approved: see [Servers that already run](#servers-that-already-run) |
| The Jobs that copy and restore those servers' data, make their logins, and make and drop [logical databases](/docs/reference/resources/geass-logical-database/) on them | Yes, from their next run |
| An organization's PostgreSQL and MariaDB servers | No: their operators cannot run them under a RuntimeClass, so they are not made for an organization (see [Engines that cannot run in it](#engines-that-cannot-run-in-it)) |
| Everything of a project that belongs to no organization, and the platform's shared servers | No: those are the platform's own |
| The object storage server, and what database operators run themselves | No: see [What it does not cover](#what-it-does-not-cover) |
| Geass's own pods (the operator, the network policy check) and the add-ons | No |

A project that names an organization is sandboxed whether or not organizations are turned on for sign-in
(`spec.auth.tenancy.enabled`), so that switching sign-in back to passwords for a while never takes the sandbox away. A
project that a pod names but that cannot be found is sandboxed too.

## Prepare the nodes

Install the runtime on **every node an organization's pods can run on**, which on a shared cluster is every worker, and
make the RuntimeClass. A node without the runtime cannot start a sandboxed pod: the pod stays `ContainerCreating` with a
`FailedCreatePodSandBox` event (`no runtime for "runsc" is configured`). Nodes reserved for one organization can go
without it only with `skipOnReservedNodes` and only while its rule holds (see [Reserved nodes](#reserved-nodes)): when it
stops holding, that organization's pods are sandboxed and cannot start there.

These steps are for gVisor on K3s, which runs its own containerd. They were run once, on K3s v1.36.4+k3s1 (a K3s server in
a Docker container on an Ubuntu 24.04 host) with gVisor `release-20261019.0`: an organization's service then ran under
the `gvisor` RuntimeClass, with `dmesg` inside its pod printing `Starting gVisor...` and `uname` saying `4.19.0-gvisor`
where the host says its own kernel. Check them against the [gVisor installation
guide](https://gvisor.dev/docs/user_guide/install/) and K3s's [containerd
configuration](https://docs.k3s.io/advanced#configuring-containerd) for your releases: gVisor has changed how it ships
before.

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

   (An earlier layout had `runsc` and the shim as separate files under the same address; a release now is the tarball,
   and a `.zstd` one if you prefer it. If the address answers 404, list `gs://gvisor/releases/release/` for what is there.)

2. **Tell K3s's containerd about it.** K3s writes its containerd configuration itself on every start, from a template
   you can extend. Recent K3s releases ship containerd 2, whose file is `config-v3.toml.tmpl`; an older one with
   containerd 1.7 reads `config.toml.tmpl`. See which K3s generated: `ls /var/lib/rancher/k3s/agent/etc/containerd/`.

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

   Then restart K3s (`sudo systemctl restart k3s-agent` on a worker, `k3s` on a server) and check that the generated
   `config-v3.toml` (or `config.toml`) has the `runsc` runtime.

3. **Make the RuntimeClass** once for the cluster. Its name is what Geass is told; its `handler` is the runtime's name in
   containerd's configuration.

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

4. **Check one pod by hand** before Geass sends every organization's code there:

   ```bash title="A pod under gVisor"
   kubectl run sandbox-check --rm -it --restart=Never --image=busybox \
     --overrides='{"apiVersion":"v1","spec":{"runtimeClassName":"gvisor"}}' -- dmesg
   ```

   gVisor's own boot messages (`Starting gVisor...`) mean the pod ran in the sandbox.

For Kata Containers, install it with its `kata-deploy` and name one of the RuntimeClasses it makes, such as `kata-qemu`.
Kata runs a virtual machine per pod, so the nodes need hardware virtualization: bare metal, or instances that allow
nested virtualization.

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

There is no form for it in the dashboard.

What happens to what is already running:

- **Services are rolled at once.** The setting reaches every service, and an organization's service gets a pod template
  with the RuntimeClass, so its Deployment replaces its copies the way it rolls out any change: a few at a time, and
  never more than a quarter of them down at once, so a service with one to three copies starts a new copy before it
  stops an old one. All organizations' services begin at the same time, so a cluster that is nearly full needs room for
  the extra copies while they roll; until there is, the copies not yet replaced keep running. Turning the setting off,
  or changing the class, rolls them again the same way. A service whose rollout is paused, or that is not deployed,
  takes it when it next deploys.
- **Builds take it from their next run.** A build that is running finishes as it started.
- **Schedules** are rewritten with the service, and their next run is sandboxed.
- **Database servers are not restarted for it.** A new one is made in the sandbox; one that already runs keeps running
  as it does until a platform administrator approves its restart, or `databaseRestarts` is `Immediate`. An
  organization's Postgres or MariaDB is not made, or is held as it is. See [Database servers](#database-servers).

## When the RuntimeClass is missing

Geass never starts an organization's code without the sandbox. While the cluster has no RuntimeClass of that name:

- **A service waits.** Its Deployment is not written: a new service gets none, and one that was already running keeps
  exactly the pods it had. The service is not ready, with the message
  `Waiting for the sandboxed runtime: RuntimeClass "gvisor" is not in the cluster, ...`, its `Sandboxed` condition is
  `False` with the reason `RuntimeClassMissing`, and it gets one `Warning` event, `SandboxRuntimeClassMissing`, when the
  wait begins. The wait does not count against the retry limit, so the service is not given up on.
- **A build waits** in `Pending`, with the same condition and event, before anything of it is made or copied into the
  organization's namespace. The service that waits for the build shows the build's message.
- **A database server waits** with nothing of it made (no volume, Secret, Service or pod), not ready, with the same
  message, condition and event, and without counting as a failure. One that already runs is left exactly as it is: no
  change is written to it, its pods keep running and serving, and its `Ready` condition keeps what it last said.
- **The Jobs on an organization's data are not made.** A [logical database](/docs/reference/resources/geass-logical-database/)
  waits like a server, with its own condition and event. A database being deleted skips its final copy (the copies taken
  before stay), and a logical database being deleted is not dropped from its server: like one whose server is not ready,
  it is let go after three minutes, and its database and login stay on the server.
- **When the class appears**, services and database servers go on at once (the operator watches RuntimeClasses), and
  builds and logical databases within half a minute.

A RuntimeClass that is deleted later does the same to what changes next. Kubernetes itself never runs a pod whose
RuntimeClass does not exist with another runtime (the API server refuses it, or the kubelet fails it), so a copy that a
Deployment replaces in the meantime is not started without the sandbox either.

```bash title="See what waits"
kubectl -n geass-system get geassapps,geassbuilds,geassdatabases,geasslogicaldatabases -o json | jq -r '.items[]
  | select(any(.status.conditions[]?; .type == "Sandboxed" and .reason == "RuntimeClassMissing")) | .metadata.name'
kubectl -n geass-system get events --field-selector reason=SandboxRuntimeClassMissing
```

## Reserved nodes

An organization that has nodes of its own (a node pool whose tag is reserved, and that the organization requires of all
its projects; see [Scaling and placement](/docs/guides/scaling-and-placement/)) shares those nodes with nobody, and can
run there without the sandbox. Set `skipOnReservedNodes: true` for that. A pod then runs without the sandbox only when
all of these hold when Geass writes it:

1. Its node selector asks for a tag, so it can only be scheduled on nodes that carry the tag.
2. Its organization requires that tag of all its projects (`spec.requiredTags` of the
   [GeassOrganization](/docs/reference/resources/geass-organization/), which only a platform administrator sets).
3. No other organization requires the same tag.
4. Every node that carries the tag is reserved for it (tainted `tag.geass.dev/<tag>`, `NoSchedule` or `NoExecute`), so
   nothing that does not ask for the tag is scheduled there, and there is at least one such node.

Anything less certain is sandboxed. The service's `Sandboxed` condition says which: `True` with `RuntimeClass`, or
`False` with `ReservedNodes` and the tag. When one of the four stops being true (another organization comes to require
the tag, a node with the tag is no longer reserved, the organization stops requiring it), the operator hears of it and
rolls the organization's services into the sandbox. If the RuntimeClass is missing it holds them instead: their copies
keep running as they are, and none is started without the sandbox. Builds take it from their next run.

A reserved node without the sandbox is a node that its organization's code can take over, like any node without it:
whatever else runs there, such as the add-ons' DaemonSets, is exposed to that organization.

## Database servers

A database server runs what its users send it. A login with enough rights runs programs through it (Postgres's
`COPY ... PROGRAM` and untrusted languages, a MySQL plugin or function loaded from a file), and the server's own code
reads whatever the organization stores. So with `spec.tenancy.sandbox` set, an organization's database server runs under
the RuntimeClass like its services: a database of a project that belongs to an organization, and a
[shared server](/docs/guides/shared-database-servers/) whose `spec.org` names one. The decision is the services' own:
`skipOnReservedNodes` lets a server that can only run on nodes reserved for its organization go without the sandbox, and
nothing is made while the RuntimeClass is missing. It is also whether or not organizations are on for sign-in, so
switching sign-in never restarts a database.

The platform's shared servers (no `spec.org`) are not sandboxed, like the rest of the platform's pods: while
organizations are on, they hold only the databases of projects of no organization (see
[Organizations](/docs/guides/shared-database-servers/#organizations)), which are the platform's own. While organizations
are off, the platform's server may hold a database of an organization's project too, and it is not sandboxed then; the
Jobs that make and drop that database are.

### How each engine is run

| Engine | What runs it | Can its pods carry a RuntimeClass? | For an organization, with the sandbox on |
| --- | --- | --- | --- |
| PostgreSQL | The CloudNativePG operator Geass installs (chart 0.23.2, CloudNativePG 1.25.1), as a `Cluster` | No. The `Cluster` has no `runtimeClassName` and no pod template (only a `Pooler` has one, which Geass does not use). CloudNativePG 1.26 added the `cnpg.io/podPatch` annotation, which can patch the instance pods but not the Jobs that make, restore and join instances, and does not restart them by itself; 1.30 has nothing more | Not made; held as it is when made before |
| MySQL, one server | Geass's own StatefulSet `geass-mysql-<name>`, from the official image | Yes | In the sandbox |
| MySQL, highly available | MOCO (chart 0.27.0, MOCO 0.37.0), as a `MySQLCluster`, whose `spec.podTemplate.spec` is a whole pod spec MOCO makes each server's pod from | Yes: `spec.podTemplate.spec.runtimeClassName` reaches every server's pod, MOCO's agent beside it included | In the sandbox |
| MySQL still on the Bitnami chart it was made with | The chart, which Geass leaves as it is | Geass changes nothing of it | Held as it is |
| MariaDB | mariadb-operator 26.10.1, as a `MariaDB` | No. No field of its `MariaDB`, nor of any other kind it has (`PhysicalBackup`, `PointInTimeRecovery`, `Backup`, `Restore`, `SqlJob`, `MaxScale`), names a RuntimeClass, up to 26.10.2, the newest | Not made; held as it is when made before |
| Redis | Geass's own StatefulSet, from the official image | Yes | In the sandbox |
| SQLite | Geass's own Deployment of an [rqlite](https://rqlite.io) server on a volume. It is a server of its own, not a file in the app's pod, so the app's sandbox does not cover it | Yes | In the sandbox |
| AWS RDS, PlanetScale | The provider: nothing of it runs in the cluster | Not applicable | Unchanged |

And the other pods that hold or touch an organization's data:

| Pods | Made by | With the sandbox on |
| --- | --- | --- |
| Copies and restores of MySQL, MariaDB (one server) and SQLite: the first copy, the nightly CronJob, a restore, and the last copy taken when the database is deleted | Geass | In the sandbox, from their next run |
| The Job that makes the application's login on a MOCO cluster | Geass | In the sandbox |
| The Jobs that make and drop a logical database on a server | Geass | In the sandbox when the server or the database's project is an organization's |
| PostgreSQL's backups and the archiving of its log | CloudNativePG, inside the server's own pods | As its server: an organization's is not made |
| CloudNativePG's Jobs that make, restore and join an instance | CloudNativePG | Not sandboxed; an organization's server is not made |
| MariaDB's physical copies, its point-in-time recovery, and the CronJob that closes its binary log | mariadb-operator, and Geass | Not sandboxed; an organization's server is not made |
| The database console, which runs its client inside the server's pod | The server's pod | As its server |
| The object storage server | Geass, one for the whole cluster | Not sandboxed: see [What it does not cover](#what-it-does-not-cover) |

### Engines that cannot run in it

While the sandbox is on, Geass never runs an organization's PostgreSQL or MariaDB server outside it:

- **A new one is not made.** The dashboard and the API refuse it with `400`, from the database form, a shared server for
  an organization, a restore and a template: "This database cannot run in the sandbox: PostgreSQL runs on the
  CloudNativePG operator, and the release Geass installs (chart 0.23.2, CloudNativePG 1.25) has no way to give a server's
  pods a RuntimeClass, and an organization's database server is not started outside the sandbox that the platform runs
  organizations' code in, so it is not made. Choose MySQL, Redis or SQLite instead, or ask a platform administrator". One
  written past the dashboard (with `kubectl`, or as a pull request environment's copy) is refused by the operator: nothing
  of it is made, not even its operator; `Ready` is `False` with the same message, which does not count as a failure; its
  `Sandboxed` condition is `False` with the reason `EngineCannotBeSandboxed`; and it gets one `Warning` event,
  `SandboxEngineUnsupported`. A server on nodes reserved for its organization (with `skipOnReservedNodes`) needs no
  sandbox, and is not refused.
- **One made before is held.** Geass deletes, stops and changes nothing of it: its pods keep running, outside the
  sandbox, and keep serving its apps and logical databases, and its `Ready` condition keeps what it last said. But Geass
  writes nothing to it while it is held: a new size, version or setting is not applied, its backups go on only as they
  were already set up, and its status is not refreshed. It says so, on `Sandboxed` (`False`, `EngineCannotBeSandboxed`):
  "This database cannot run in the sandbox: …. It was made before, so it keeps running as it is, outside the sandbox, but
  Geass changes nothing of it …", with one `Warning` event `SandboxEngineUnsupported`. It can be deleted as usual, and it
  is looked after again as soon as the sandbox is turned off.
- **A shared PostgreSQL or MariaDB server is not given to an organization**: `POST /api/shared-databases/{name}/org` is
  refused with `409`.

To end a hold, move the data to a MySQL server (dump and load it, as for any change of engine), or turn the sandbox off.
A CloudNativePG of 1.26 or later could put the RuntimeClass on Postgres's instance pods through its `cnpg.io/podPatch`
annotation; Geass does not install one, and that patch would leave CloudNativePG's own Jobs outside the sandbox.

```bash title="See which servers cannot run in the sandbox"
kubectl -n geass-system get geassdatabases -o json | jq -r '.items[]
  | select(any(.status.conditions[]?; .type == "Sandboxed" and .reason == "EngineCannotBeSandboxed")) | .metadata.name'
```

### Servers that already run

Restarting a database is not rolling a service: a server of one pod is down while it restarts, and a Redis server, which
keeps nothing on disk, comes back empty. So Geass does not restart a running server because the setting changed. A
server's pods are written under the RuntimeClass:

- **at once** when it is made, and while it has never been ready (it holds nothing in use yet);
- **when a platform administrator approves it**, one server at a time:

  ```bash
  kubectl -n geass-system annotate geassdatabase <name> geass.dev/sandbox-restart=true
  ```

  Geass then writes its pods under the RuntimeClass, which restarts it, records a `SandboxRestart` event, and takes the
  annotation away;
- **all at once**, every waiting server together, with `databaseRestarts: Immediate` on `spec.tenancy.sandbox`.

The default is to wait (`databaseRestarts: OnApproval`, also when it is not set). Until it is moved, a server runs as it
did, and says so: `Sandboxed` `False` with the reason `RestartPending` and the message "Runs without the sandbox until it
is restarted into RuntimeClass gvisor, which restarts the database. A platform administrator annotates it
geass.dev/sandbox-restart to restart it now, or sets spec.tenancy.sandbox.databaseRestarts to Immediate", with one
`Warning` event `SandboxRestartPending`. It is looked after as usual meanwhile, as it runs: a change that restarts it
anyway, such as a new size, restarts it outside the sandbox, so approve the move with that change if you want both in one
restart. The same holds for turning the setting off and for a change of class: a server keeps the class it runs with
until its restart is approved. The class a server is written with is recorded on its GeassDatabase, in the annotation
`geass.dev/sandbox-runtime-class`; do not edit it.

What the restart is, which is the operator's or Kubernetes's own, and nothing more:

- **MySQL (one server), Redis and SQLite** run one pod each, so the database is unavailable while its pod is replaced and
  starts again. **A Redis server comes back empty.** SQLite's Deployment replaces its one copy as it does for any change.
- **Highly available MySQL** is rolled by MOCO: one server at a time, from the highest-numbered, only while the cluster is
  healthy, and MOCO switches the primary to a replica when the primary's pod is deleted, so writes pause for a few
  seconds then.
- **PostgreSQL and MariaDB** are never restarted for it: they cannot run in the sandbox.

Approve one server at a time, at a quiet hour, with its backups up to date.

```bash title="See which servers wait for their restart"
kubectl -n geass-system get geassdatabases -o json | jq -r '.items[]
  | select(any(.status.conditions[]?; .type == "Sandboxed" and .reason == "RestartPending")) | .metadata.name'
```

### The Jobs on their data

The Jobs that copy, restore and make databases and logins on an organization's server take the RuntimeClass from their
next run, whether the server itself has moved yet or not. A Job that is already made keeps how it runs: Kubernetes does
not let a Job's pod change. A logical database's `Sandboxed` condition says how the Job that made it ran.

### What it costs

- **Speed.** gVisor runs a kernel of its own in user space, and every system call and file access goes through it. A
  database does little else, so it runs slower under gVisor than on the node's kernel, by an amount that depends on the
  workload: measure yours. See gVisor's [performance guide](https://gvisor.dev/docs/architecture_guide/performance/). Kata
  runs a virtual machine for each pod, with its own memory and its own path to the disk.
- **Features.** gVisor implements most of Linux, not all of it. Its
  [compatibility list](https://gvisor.dev/docs/user_guide/compatibility/linux/amd64/) marks `io_uring` and Linux AIO
  (`io_setup` and `io_submit`, which InnoDB uses) as partly supported, `fadvise` as ignored, `mlock` as a stub, and some
  options of `fallocate` and `madvise` as unsupported. Huge pages, direct I/O (`O_DIRECT`) and how the filesystem flushes
  may not behave as on the node. A server whose settings ask for these may start slower, run slower, or not start. Test a
  copy of your database, with its own settings and data, under the RuntimeClass before you move it, and keep its backups.
- **Memory and CPU.** A RuntimeClass `overhead` is added to every database pod too, and to what the project's quota and
  the usage meter count.
- **A restart** for every server that already runs (above).

## What it does not cover

- **PostgreSQL and MariaDB servers.** Their operators cannot run them under a RuntimeClass, so an organization's is not
  made while the sandbox is on, and one made before is held, still running outside it (see
  [Engines that cannot run in it](#engines-that-cannot-run-in-it)). Their operators' own Jobs (CloudNativePG's that make,
  restore and join an instance; mariadb-operator's copies and recovery) are never sandboxed.
- **The platform's shared servers, and the object storage server.** They are the platform's. The object storage server
  holds the buckets of every project of the cluster, of every organization, and runs no code an organization sends it: an
  organization reaches it only through the S3 API, with keys for its own buckets. A sandbox around it would not keep
  organizations apart inside it, which is what those keys do.
- **A server that waits for its restart**, which runs outside the sandbox until it is approved (see
  [Servers that already run](#servers-that-already-run)).
- **Other organizations asking for a reserved tag themselves.** A service's `runOn` tags and a project's own required
  tags can name any tag, including one reserved for another organization, and that work then runs on the other
  organization's nodes. It is sandboxed itself (its organization does not require the tag), but it sits beside the
  owner's pods, which with `skipOnReservedNodes` are not.
- **Capacity.** The check that a new service or database fits counts the nodes of its region and tags, not the
  RuntimeClass's `scheduling`, and a RuntimeClass `overhead` is added to every pod, to what the project's quota counts and
  to what the [usage meter](/docs/guides/usage-and-capacity/#usage-by-organization-for-billing) bills the organization.
- **Compatibility.** gVisor implements most, not all, of Linux: some system calls and `/proc` files behave differently,
  and I/O-heavy work runs slower. A service that does not start under it shows the usual
  `The service cannot start: …` or stays starting; read its pod's events. A database server that does not start says
  `Service is starting` until it does: read its pod's events and logs, and see [What it costs](#what-it-costs).
- **Watching what the pods do.** A tool on the node that watches system calls (Falco, Tetragon) sees gVisor's own process,
  not the program inside: the organization's system calls are handled by gVisor and never reach the node's kernel. So
  such a tool cannot report a miner in a sandboxed pod. What a pod uses is measured all the same, so the
  [abuse detection](/docs/guides/abuse-detection/)'s CPU signal works under the sandbox.

## Related

- [Organizations and workspaces](/docs/guides/organizations-and-workspaces/)
- [Shared database servers](/docs/guides/shared-database-servers/) and [GeassDatabase](/docs/reference/resources/geass-database/)
- [Security](/docs/operations/security/)
- [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/)
- [Scaling and placement](/docs/guides/scaling-and-placement/)
- [Noisy-neighbour limits](/docs/guides/tenant-limits/), [Outbound limits](/docs/guides/outbound-limits/), [Registry isolation for organizations](/docs/operations/registry-isolation/) and [Abuse detection](/docs/guides/abuse-detection/), the other settings for a cluster that organizations share
