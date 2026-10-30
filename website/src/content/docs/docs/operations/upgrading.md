---
title: "Upgrade Geass"
description: "Update a running cluster to a new Geass release and K3s, with no rebuild: what the installer checks and keeps, how workers follow, and how to go back."
---

To update Geass, run the installer of the release that you want on the control plane. When the machine already runs Geass, the installer updates it. The installer does these things:

- It keeps the cluster, its data, and its settings.
- It takes a snapshot of the etcd of the cluster first.
- It replaces the Geass custom resource definitions and the operator.
- It waits until the new operator has taken over.

The installer never removes K3s, unless you tell it to with `--reinstall`.

You can update three things, and each one has its own rules:

| What | Who does it | How |
| --- | --- | --- |
| **Geass**: the operator, the dashboard, the custom resource definitions, the RBAC | The installer, on the control plane | Run the new release's installer. See [Update Geass](#update-geass) |
| **K3s** on the control plane | The installer, on the control plane | Add `--k3s-version`. See [Update K3s](#update-k3s) |
| **K3s** on the workers | The operator, over SSH, one node at a time | Follows the control plane after `spec.k3s.version` says so. See [Workers](#workers) |

On a Geass that a platform runs for you, with [managed mode](/docs/operations/managed-mode/) on, the platform does all three. The
dashboard then says so, and does not tell you to run the installer. Managed mode does not change the installer itself.

## Update Geass

SSH into the control plane and run the installer of the release that you want:

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo sh
```

To pin a release, use its `install.sh` and set `GEASS_VERSION`, as in [Install Geass](/docs/getting-started/install/#pin-a-version). To pass an option, put it after `-s --`:

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo sh -s -- --force
```

The installer looks at the machine first:

- A machine with no K3s gets a new cluster.
- A machine whose K3s has Geass in it gets an update.
- The installer refuses anything else, and tells you why. Examples are a K3s that Geass did not set up, a K3s whose API server does not answer, and a worker.

The installer deletes a cluster only when you pass `--reinstall`.

### What it does

The installer prints each step as `→ <name>` and `✓ <name>`. An update runs these steps in order, and stops at the first step that fails:

| # | Step | What it does |
| --- | --- | --- |
| 1 | `check the cluster before updating` | Reads the cluster and changes nothing. Refuses a downgrade, a cluster that is not ready, degraded Longhorn volumes, and custom resource definitions that would leave objects unreadable. See [What stops an update](#what-stops-an-update) |
| 2 | `snapshot the cluster's etcd` | Runs `k3s etcd-snapshot save` before anything is changed. The snapshot is the way back. See [Go back](#go-back) |
| 3 | `upgrade k3s (control plane)` | Only with `--k3s-version`. See [Update K3s](#update-k3s) |
| 4 | `import operator image` | Loads the operator image that the installer carries into the containerd of K3s |
| 5 | `update custom resource definitions` | Applies the definitions with server-side apply and waits until the API server serves them |
| 6 | `update geass-operator` | Applies the namespace, permissions, Deployment, and Services of the operator |
| 7 | `wait for the new operator to take over` | Waits up to 15 minutes for the Deployment to roll out. The startup probe of the pod allows a data migration 12 minutes, and this wait does not cut it short. The installer gives up immediately on a pod that is crash looping or cannot pull its image. Then the installer waits up to 5 minutes until the cluster object reports the release that was installed (`status.release`). A rollout that finished says only that some operator is running. The report says that it is the new operator and that it got past its migrations. If the new operator does not take over, the installer **rolls the operator back**. See [When the new operator does not take over](#when-the-new-operator-does-not-take-over) |
| 8 | `set the K3s version for the workers` | Only with `--k3s-version`. Sets `spec.k3s.version` on the cluster object |
| 9 | `wait for the cluster to be ready` | Waits up to 10 minutes for `READY` `True`. A cluster that is not ready by then is a warning, not a failure, because an add-on whose chart version moved takes its time |
| 10 | `summary` | What changed, and where the snapshot is |

The update does not do these things:

- It does not apply the cluster object again, because that would reset your settings. Change your settings on the object itself.
- It does not read `GEASS_ACME_EMAIL`, `GEASS_MULTI_SITE`, or `GEASS_NODE_EXTERNAL_IP`. These variables describe a new cluster, and the installer says that it ignores them.
- It does not touch the join token.
- It does not remove anything that an earlier release created and this release no longer ships.

You can safely run the installer again for the release that the cluster already runs, and it restarts nothing. The pod template of the operator changes only when the release or the image changes.

### Options

| Option | Meaning |
| --- | --- |
| `--k3s-version <release>` | Also upgrade K3s, to this release. Use the full name, such as `v1.36.5+k3s1`. The installer does not accept a channel name such as `stable`. Only this option upgrades K3s. `GEASS_K3S_VERSION` picks the K3s of a new cluster. When a cluster is updated, the installer ignores it with a warning |
| `--allow-untested-k3s` | Allow a K3s version outside the range that the add-ons of this release are known to run on (Kubernetes 1.25 to 1.36 for this release line) |
| `--force` | Go on, although the cluster is not ready or a Longhorn volume is degraded. The checks protect data, so use this option after you read what they say. It does not go past the rules that keep the data readable. The installer refuses these, whatever you pass: a downgrade, a data schema newer than the schema of the installer, and the K3s rules (no downgrade, one minor version at a time) |
| `--skip-snapshot` | Update without a snapshot first. There is then no way back to the cluster as it is |
| `--no-rollback` | If the new operator does not take over, leave it where it is, and do not roll the operator back. Use this option to look at what went wrong in the cluster where it went wrong. Also `GEASS_NO_ROLLBACK=1` |
| `--upgrade` | Say that you mean to update. `geass upgrade` does the same. This is the default when the machine has a cluster |
| `--reinstall`, `--yes` | Delete the cluster and build a new one. See [Reinstall](#reinstall) |
| `etcd-snapshots enable`, `disable`, `status` | Not an update. Turn on, turn off, or look at the upload of snapshots from K3s on this server to an S3 bucket of your own. See [Disaster recovery](/docs/operations/disaster-recovery/) |
| `--join-server <url>` | Not an update. Make a machine that has no K3s an additional control plane server of this cluster, at the K3s release that you give with `--k3s-version`. See [High availability](/docs/operations/high-availability/#run-three-control-plane-servers) |
| `--version`, `--help` | Print the release of the installer, or the options |

### What stops an update

The first step refuses, and says what to do, in these cases:

- **The cluster runs a newer release than the installer.** Geass does not downgrade, because the newer release might have stored data in a shape that an older operator misreads. A cluster installed before releases were recorded has no release to compare, and the installer updates it.
- **The cluster is not ready.** An add-on might still be installing or failing. If you update the operator on top of that, it is harder to tell which change broke what.
- **A Longhorn volume that is attached has fewer good copies than it should.** Anything that restarts components puts the copy that is left at risk.
- **The release would leave objects unreadable.** The installer compares the definitions that it carries with what the cluster stores. The cluster might have objects stored in an API version that the release no longer serves. The API server then could not read them. Update to the release in between first.
- **With `--k3s-version`:** the version is not a K3s release, is older than what runs, is more than one minor version ahead, or is outside the supported range. Or a node is not ready.
- **Without it:** the cluster runs a K3s older than the add-ons of this release need (Kubernetes 1.25). Upgrade K3s first.

The first step also refuses a cluster whose data is at a schema level newer than the installer knows of. The installer can compare schema levels even when the two releases cannot be ordered. For example, a build from an untagged checkout has no version number to compare.

The first step warns, and goes on, in these cases:

- Nodes are not ready, when K3s is not being upgraded.
- The cluster has more than one control plane node. The installer upgrades only the node that it runs on.
- Databases are not backed up. The etcd snapshot holds the objects of the cluster, **not the contents of your databases**.

### What is kept

| | Kept by an update |
| --- | --- |
| Projects, services, databases, buckets, nodes, connections, settings | Yes. Stored in etcd, which the update does not touch |
| Secrets, dashboard users, API tokens (including the tokens of the command line and of agents), the join token | Yes |
| Database data and volumes | Yes. The update does not touch them |
| Workers | Yes. They keep running, and keep their K3s until `spec.k3s.version` says otherwise |
| Running services | Yes, apart from the operator and the dashboard, which restart. Expect them to be away for a minute or less |
| Add-ons | Upgraded by the operator when the new release raises a chart pin. See [Add-ons](#add-ons) |

## Update K3s

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo sh -s -- --k3s-version v1.36.5+k3s1
```

With `--k3s-version`, the installer upgrades the K3s of the control plane in the way that K3s documents. It runs the install script of K3s again with the new version, which replaces the binary and restarts the service. The installer leaves the data of the cluster where it is: etcd, the join token, and the configuration. The server keeps the arguments that it runs with. The installer reads them out of the service unit, so it does not silently reset a server that an administrator changed to the default.

The rules are the Kubernetes rules:

- **One minor version at a time.** From 1.34 you go to 1.35, then to 1.36. The installer refuses a longer step and names the version to go to first.
- **Never down.** K3s does not support downgrades. The way back is the snapshot.
- **Control plane first.** Geass moves workers only to a version that the control plane already runs.

The API server and the operator are away while the service restarts, normally for less than a minute. The installer waits up to 10 minutes for the control plane to report the new version and be ready again. Containers that are running are not stopped, because the K3s service is configured so that a restart leaves them alone. A Kubernetes release that changes how the kubelet computes the hash of a container can restart containers in place. So a single-instance database might restart one time. Its data is on its volume and is not touched.

K3s and the add-ons move together, because some chart versions need a minimum Kubernetes version. For example, Longhorn 1.13 needs 1.34, and so this release pins Longhorn 1.12. The range that the add-ons of this release are known to run on is built into the installer. Outside that range, the installer refuses, unless you add `--allow-untested-k3s`.

If you ask for a K3s version when you update Geass, the order is: the snapshot, then K3s on the control plane, then the operator. The operator must come up on the K3s that it is going to run on.

### Additional control plane servers

On a cluster that has [three control plane servers](/docs/operations/high-availability/#run-three-control-plane-servers), you update the **first** server with the installer, as everything above says. The installer does not touch the other servers, and two things follow from that:

- **The operator's image, first.** After the update, the Deployment of the operator names a new image. The installer puts that image on the machine that it runs on, and on no other machine, because the image is in no registry. The operator can start on any control plane server. So **before you update the first server**, run the installer of the new release on each of the other servers, with no options. On a server that joined a cluster, that is all the installer does. It puts the new image on the server, next to the old image, and changes nothing else. It refuses `--upgrade` and `--reinstall` on such a server.

  If you update the first server before the other servers have the image, the new operator can land on one of them and fail to pull the image. The installer then sees that the pod cannot pull its image, and it puts the previous operator back. Before it changes anything, the installer names the servers that do not report the image. A server that you refreshed only a moment ago can still be named for some seconds, until its node reports the image. If you refreshed that server, go on.
- **K3s.** With `--k3s-version`, the installer upgrades the first server. Then upgrade each of the other servers **one at a time**. Wait for the node of each server to be ready before you start the next. Use the K3s install script and the release that you gave the first server: `curl -sfL https://get.k3s.io | sudo env INSTALL_K3S_VERSION=<release> INSTALL_K3S_EXEC=server sh -`. The server keeps its settings, which are in its configuration file.

  Geass does not move the workers until the oldest control plane server runs the new release, because a worker must not run newer than the servers that it talks to. Until then, the status of the cluster says so and names the servers that are behind. `kubectl -n geass-system get geasscluster default -o jsonpath='{.status.k3s}'` shows `WaitingForControlPlane`. When the last server runs the new release, the operator moves each worker, one at a time. On a test cluster of three servers and one worker, each server was ready again within about 40 seconds of its upgrade. The worker was moved within about 25 seconds of the last server.

### Workers

The operator moves workers when `spec.k3s.version` on the cluster object asks for a version. The installer sets this field for you when you use `--k3s-version`. You can also set it yourself:

```bash
sudo k3s kubectl -n geass-system patch geasscluster default --type merge \
  -p '{"spec":{"k3s":{"version":"v1.36.5+k3s1"}}}'
```

The operator never touches the control plane. The operator is a pod on that machine, and it cannot restart K3s underneath itself. If `spec.k3s.version` is ahead of the control plane, nothing happens, and the status says to upgrade the control plane first. If the field is empty, it means "leave K3s alone on every node", and a new worker installs the K3s that the control plane runs.

The operator upgrades each worker that runs an older K3s than the target, one at a time and in name order. For each worker, the operator does these steps:

1. Waits until all nodes are ready and no attached Longhorn volume has lost a copy. If you take a node down while another node is already down, a volume can lose its last copy.
2. **Cordons** the node, and marks that it did.
3. Connects over SSH with the key of the node, and with the host key that you recorded for it, if any.
4. Checks that the address of the cluster is reachable from the node.
5. Writes the `/etc/rancher/k3s/config.yaml` of the node from its `placement`, as a join does. So the operator replaces a hand edit of that file.
6. Runs the K3s install script for the new version, with the name, address, and token of the node. That step replaces the binary and restarts the agent.
7. Waits until the node is ready and reports the new version.
8. Uncordons the node, if the upgrade is what cordoned it.

Machines that Geass created at a cloud, including the nodes of a [pool](/docs/guides/node-pools/), are upgraded in the same way, with the SSH key that Geass made for them. An autoscaling pool removes no node during the upgrade. A machine made after the upgrade installs the K3s version of the control plane.

The node is **cordoned, not drained**. The agent restarts without stopping the containers that it runs, so services keep serving through the restart. A drain would move the pod of a database away from its local data. A node that you cordoned yourself stays cordoned.

The operator stops at the first node that does not come back ready on the new version within 15 minutes. It leaves that node cordoned, and never starts the next node. To watch the upgrade, run this command:

```bash
sudo k3s kubectl -n geass-system get geasscluster default -o jsonpath='{.status.k3s}{"\n"}'
```

| `status.k3s.upgrade.phase` | Meaning |
| --- | --- |
| `WaitingForControlPlane` | The workers are behind the target and the control plane is not on it yet |
| `Waiting` | The next worker is not started because a node is not ready or a volume is degraded. The message says which |
| `Upgrading` | A worker is being upgraded: `currentNode` |
| `Complete` | Every worker that Geass manages runs the target or newer. The message lists worker nodes that Geass does not manage (no `GeassNode`, so no SSH key). Upgrade those nodes by hand |
| `Failed` | A node did not come back, or could not be reached. The message says why and how to try again |

After you fix what failed, ask for another try. The operator consumes the request:

```bash
sudo k3s kubectl -n geass-system annotate geasscluster default geass.dev/retry-k3s-upgrade=now
```

If you set `spec.k3s.version` to a different value, or to empty, the operator abandons the upgrade in progress and uncordons the node that it was working on.

### When the new operator does not take over

An update stops the old operator before it starts the new operator. A new operator migrates data when it starts, and an old operator must not write at the same time. If the new operator then cannot start, the cluster has nothing that reconciles. So the installer puts the old operator back.

The new operator might crash-loop, fail to pull its image, never become ready, or never report its release. In those cases, the installer does these steps:

1. **Before** it changes anything, the installer reads the information that says why: the pods of the operator, the end of its log, and the log of the container before it restarted. A rollback replaces the pod that holds this information.
2. The installer checks that it is safe to go back (see below).
3. The installer runs `kubectl rollout undo` on the operator Deployment, waits until the previous operator runs again, and stops there. The installer reports the update as failed, with the reason. The later steps do not run: the K3s target for the workers, and the wait for the cluster. The exit status is not zero.

These changes stay in place:

- The custom resource definitions. They only grow, so the previous operator reads everything.
- The permissions of the operator.
- K3s on the control plane, when you asked for it. K3s does not go back.

The installer does not touch the cluster object, `spec.k3s.version`, or the workers. The snapshot taken first is still the way to undo all of these changes.

The installer rolls back only when it is safe. When it is not safe, the installer says why:

| It does not roll back when | Because |
| --- | --- |
| The schema level of the data is past what the previous operator reported (or past the baseline, for a cluster from before levels were recorded) | The new operator migrated and then failed. The previous operator would refuse that data, or misread it if the previous operator predates the record. Fix forward, install a release that fits, or restore the snapshot |
| The new operator reported its release | It ran, and might have written fields that the previous operator does not know. An operator that writes an object back drops what it does not know |
| This run did not replace the operator | There is no earlier operator to go back to. `rollout undo` would go to an older revision than the one that was running |
| The schema level of the data cannot be read | Nobody can say that the previous operator can read the data |
| `--no-rollback` was passed | You asked to look at the failure |

A rollback can fail too. If the previous operator does not start either, the installer says so. The usual cause is that K3s removed its image from containerd. K3s does this when it needs disk space and the image is unused, and a crash-looping new operator leaves the old image unused. In that case, restore the snapshot. After the problem of the new operator is fixed, install the release again.

## Add-ons

The operator installs these Helm charts at fixed versions, through the Helm controller of K3s. The operator applies a changed pin on its next reconcile:

| Add-on | Chart version in this release line |
| --- | --- |
| cert-manager | `v1.17.2` |
| kube-prometheus-stack | `69.8.2` |
| Longhorn | `1.12.1` |
| CloudNativePG (installed with the first PostgreSQL database) | `0.23.2` |
| VictoriaLogs (`victoria-logs-single`) | `0.13.10` |
| Fluent Bit | `0.58.3` |
| KEDA (`geass-keda`), only with the serverless add-on | `2.21.0` |
| KEDA HTTP add-on (`geass-keda-http`), only with the serverless add-on | `0.16.0` |

When a release raises a pin, the add-on upgrades itself, which restarts its pods. These images and charts are pinned in the same way:

- The SeaweedFS and Garage images that the in-cluster object storage server runs from.
- MySQL, Redis, and other images and charts.

The update waits for the cluster to report ready again, but it does not manage the upgrades of the add-ons. When Longhorn itself moves, Geass does not upgrade the engine of Longhorn volumes that already exist. So read the Longhorn upgrade notes for the version that a release pins.

## Go back

K3s does not downgrade, and the operator refuses to run over data that a newer release has changed. So the way back from an update that went wrong is the snapshot that the installer took first. The snapshot is a copy of everything that the cluster stores in etcd, as it was before the update. That includes the custom resources, the Secrets, the Deployment of the operator, and the definitions.

The snapshot is in the snapshot directory of K3s, with a name for the update:

```bash
sudo ls /var/lib/rancher/k3s/server/db/snapshots/ | grep geass-pre-upgrade
sudo k3s etcd-snapshot ls
```

To restore the snapshot, run these commands on the control plane:

```bash
sudo systemctl stop k3s
sudo k3s server --cluster-reset --cluster-reset-restore-path=/var/lib/rancher/k3s/server/db/snapshots/<snapshot name>
sudo systemctl start k3s
```

`--cluster-reset` ends with `restart without --cluster-reset flag now`. That is the normal end. When you start K3s again, the cluster comes back as it was when the snapshot was taken, with the old operator and the old definitions. Everything that changed in the cluster after the snapshot is lost: services and projects that were created, secrets, and API tokens that were issued.

The snapshot holds the Secrets encrypted, because the installer turns on K3s secrets encryption. On the machine that the snapshot came from, the server token and the setting are already there. To restore on another machine, you need two things that the snapshot does not carry:

- **The cluster's server token**, the token in `/var/lib/rancher/k3s/server/token`. K3s encrypts the bootstrap data of the snapshot with this token. A different token stops the restore with `bootstrap data already found and encrypted with different token`. Keep a copy outside the cluster, because the copy in the Secret `geass-token` is inside the data that you would restore.
- **`secrets-encryption: true` in that machine's `/etc/rancher/k3s/config.yaml`**, which is what the installer writes. Without it, the restore finishes, but a read of an encrypted Secret fails with `identity transformer tried to read encrypted data`.

The restore brings back the data of the cluster and leaves K3s on the version that it runs now. That is what you want when only Geass needs to go back. The older operator runs on the newer K3s, within the range that its add-ons were tested on.

You might also have upgraded K3s and want the old version back. If no worker has been upgraded yet, install the old version before you restore. K3s supports going back only in this way, from a snapshot that was taken on that version:

```bash
curl -sfL https://get.k3s.io | sudo INSTALL_K3S_VERSION=<the old version> INSTALL_K3S_SKIP_START=true sh -s - server --cluster-init
```

Do not do that after workers have been upgraded. An agent must not be newer than its server, and K3s does not support a downgrade on a worker either. Instead, do these steps:

1. Restore the data.
2. Keep the K3s of the control plane where it is.
3. Set `spec.k3s.version` back to empty. The restored cluster object has the version that it had before the update, which is usually empty.

The snapshot does not cover three things:

- **The contents of your databases and volumes.** The snapshot holds only the objects of the cluster. Database backups to S3 and your own dumps cover the data.
- **The operator's old image.** The image stays in the containerd of K3s until K3s needs the disk space. If the operator is in `ImagePullBackOff` after a restore, the image was removed. Run the installer of the release that you went back to, which loads its image again. Releases that have an update mode do this. A release before this one does not, and you must build it again from source as in [Update from source](#update-from-source).
- **Anything outside the cluster.** An update does not touch S3 buckets, DNS records, Cloudflare tunnels, and cloud accounts, and so a restore does not either.

A snapshot contains every Secret in the cluster. Protect the snapshot directory as you protect the disk of the machine, and delete old snapshots when you no longer need them. K3s also takes its own scheduled snapshots, two times a day, in the same directory. See [Security](/docs/operations/security/).

## Reinstall

`--reinstall` deletes the cluster on the machine and builds a new one:

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo sh -s -- --reinstall
```

The installer first says what it is going to delete. Then it asks you to enter `reinstall` on the terminal. In a script with no terminal, add `--yes`. Before the installer removes K3s, it takes a last etcd snapshot into `/var/backups/geass`, where the K3s uninstall cannot reach it. Nothing restores that snapshot for you. A snapshot of a broken cluster might not be possible. In that case, the reinstall goes on without a snapshot.

The K3s uninstall script removes `/etc/rancher/k3s`, the K3s data directory (`/var/lib/rancher/k3s` by default), and `/var/lib/kubelet`. After a reinstall:

- Every project, service, database definition, secret, dashboard user, API token, and connection of the old cluster is gone. Data on local-path volumes is also gone.
- Workers joined to the old cluster keep running their agents, but they hold credentials of a cluster that no longer exists. Join them again.
- Things outside the K3s directories are not touched: S3 buckets and backups, DNS records, Cloudflare tunnels, IAM users, and the resources of your provider. The data directory of Longhorn is left on disk, with nothing that refers to it.

Use a reinstall for a machine where an install failed part-way and nothing of value exists yet. You can also use it to rebuild a cluster that you mean to replace. For anything that you care about, update, or [move to a new server](#move-to-a-new-server).

### Move to a new server

This path is the safest when you want a clean machine, or a different machine. The old cluster keeps running until you decide to switch.

1. Install the release that you want on a new server. See [Install Geass](/docs/getting-started/install/) and [Sign in to the dashboard](/docs/getting-started/sign-in/).
2. Create again what the old cluster held:
   - Dashboard users.
   - The Cloudflare and GitHub connectors, and the AWS and PlanetScale connections.
   - Projects, services, variables and secrets, and domains.
   - Node regions, sites, and tags, including compliance tags and the required tags of each project.
   - API tokens.
   - The serverless add-on, if you used it (`spec.addons.serverless.enabled`). Each service carries its own sleep-when-idle setting, and each function carries its schedules.
3. Sign the command line in to the new cluster with `geass login --url <new address>`.
4. Put new tokens into CI secrets and agent configurations. Tokens do not move between clusters.
5. Move data with the tools of the databases, for example `pg_dump` and `pg_restore` over a port-forward.
6. Copy bucket contents with an S3 client.
7. When you have checked the new server, point DNS at it.
8. Keep the old server for a while, then [uninstall it](/docs/operations/uninstall/).

:::caution
Before you retire a cluster, dump your databases yourself. Geass has no export and import. Database backups in your S3 bucket survive, but the dashboard restores them from a backup record that lives in the old cluster. A new cluster has no such record, so it cannot list or restore the backups of the old cluster.
:::

### Update from source

The operator, the dashboard, and the manifests are one Deployment and a set of Kubernetes objects. So you can also build a release from source, load its image on the control plane, and apply the manifests yourself. This path is advanced, and the release process does not cover it. It skips the checks, the snapshot, and the wait that the installer does. Build on a machine with the same architecture as the control plane, `amd64` or `arm64`:

```bash
git clone https://github.com/degoke/geass.git
cd geass
git checkout v0.2.0
make docker-build IMG=geass-operator:v0.2.0 GEASS_VERSION=v0.2.0
docker save -o geass-operator.tar geass-operator:v0.2.0
```

Then do these steps:

1. Copy `geass-operator.tar` to the control plane.
2. Import the image with `sudo k3s ctr images import geass-operator.tar`.
3. Take a snapshot: `sudo k3s etcd-snapshot save --name before-v0.2.0`.
4. With `KUBECONFIG` pointing at the cluster, run these commands:

```bash
make deploy IMG=geass-operator:v0.2.0
sudo k3s kubectl -n geass-system rollout status deployment/geass-controller-manager
```

Do not swap only the image. A release can add custom resource fields and operator permissions, and `make deploy` applies them with the image. Replace `v0.2.0` with the tag that you update to.

## Before you update

1. **Read the release notes** on the GitHub release.
2. **Know what you run.** The dashboard shows the release under **Usage**, **Version**, with the K3s version of every node. `geass version` prints the release of the cluster. The cluster records it too:

   ```bash
   sudo k3s kubectl -n geass-system get geasscluster
   sudo k3s kubectl -n kube-system get helmcharts \
     -o custom-columns=NAME:.metadata.name,VERSION:.spec.version
   ```

   The `RELEASE` column is the release of the operator, and `K3S` is the K3s version of the control plane. A cluster installed before releases were recorded shows neither until it has been updated one time.
3. **Check network isolation.** Each project is private by default. Only its own services, Geass itself, and visitors who arrive through your domains can reach it. Projects that existed before isolation was added become isolated when Geass is updated. Before you update, list every project that calls another project, for example a front end that calls an internal API. Be ready to allow each caller under **Settings → Network** of the receiving project. See [Network isolation](/docs/guides/network-isolation/). Two rules hold, whatever you allow: services cannot read the credentials of the cloud provider for their machine, and no project can read the metrics or logs of another project.
4. **Check backups.** In **Settings → Backups** of each project, confirm that backup storage is set. Then open the **Backups** tab of each database. Confirm that its state is **Protected** and that a recent restore point exists. A database that is not protected shows **Not backed up** or **Backups at risk** on its canvas card. The installer lists the databases that are not protected. See [Backups and restore](/docs/guides/backups-and-restore/).
5. **Keep a copy of the sign-in Secret** (`geass-dashboard-auth`). Also write down which connections and connectors you use, in case you must create them again.
6. **Check capacity.** Add-on upgrades restart pods. On the **Usage** page, look for room to spare. Also look for room on the disk for a snapshot and a second operator image.
7. **If you use sleep when idle, check the add-on.** `sudo k3s kubectl -n kube-system get helmcharts geass-keda geass-keda-http` shows both charts, and `sudo k3s kubectl get crd httpscaledobjects.http.keda.sh` shows that the definition exists. A release that raises these chart pins upgrades the add-on, which restarts its pods. See [Sleep when idle](/docs/guides/scale-to-zero/).
8. **If you use compliance tags, plan the order.** In a new cluster, first mark the compliance tags on the nodes. Then require the tags on the project. Only then create its databases again. Geass places a database one time, when it is first provisioned. The dashboard refuses to add a tag that a provisioned database does not run on. The admission policy `geass-required-node-tags` is a cluster object, so a new cluster makes it again the first time a project requires tags. See [Compliance node tags](/docs/guides/compliance-tags/).
9. **Check how you get certificates.** The operator creates the `letsencrypt-prod` issuer only when the cluster object has `spec.addons.certManager.acmeEmail`. A cluster installed before that field existed does not have it. The operator never overwrites an issuer of that name that you created yourself. See [HTTPS certificates](/docs/getting-started/install/#https-certificates).
10. **If you upgrade K3s, look at your workers.** `sudo k3s kubectl get nodes` shows the version of each node. Workers that Geass did not join have no SSH key in a `GeassNode`. You must upgrade those workers by hand.

## After you update

1. Make sure that the installer ended with `✓ summary`.
2. Make sure that the operator runs the new image and that its log has no repeating errors. Run `sudo k3s kubectl -n geass-system get pods` and `sudo k3s kubectl -n geass-system logs deploy/geass-controller-manager -c manager`.
3. Make sure that the cluster is ready. `sudo k3s kubectl -n geass-system get geasscluster` shows `READY` `True` and the `RELEASE` that you installed. If an add-on is upgrading, the cluster can report not ready for a while. Look at `sudo k3s kubectl -n kube-system get helmcharts` and compare the versions.
4. Reload the dashboard, which is part of the new image, and sign in. **Usage**, **Version** shows the release that you installed.
5. Open each project. Services and databases must be **Ready**, and the top bar must show **Up to date**.
6. Make sure that services that call each other still can, and that each project has its isolation policies: `sudo k3s kubectl get networkpolicy -A | grep geass`.
7. **Update the command line** to the same release, on every machine and CI job that uses it. Run `install-cli.sh` again, with `GEASS_VERSION` to pin a tag. The command line talks to the `/api/v1` API, whose resources carry the fields of the release of the cluster. `geass version` says when the command line and the cluster are different releases. Then `geass whoami` answers, and `geass status -p <project> -e production` lists the services. Existing tokens keep working, because they live in the cluster, and an update keeps the cluster.
8. With the metrics add-on on, the operator keeps its own monitor and alert rules up to date: `sudo k3s kubectl -n monitoring get servicemonitor,prometheusrule geass-operator geass-alerts`. The **Usage** page charts them under **Geass health**. The release that brought [alerts](/docs/guides/alerts/) changes the settings of the monitoring chart in two ways:
   - It starts Alertmanager.
   - It opens the rule selector of Prometheus. Prometheus then loads rules that carry no `release` label, the seven rules of the operator among them. Clusters installed before that release never loaded these rules.

   Prometheus restarts one time for that change. If the metrics of the operator were never scraped, expect `GeassOperatorDown` to fire, and look at `sudo k3s kubectl -n monitoring get pods`.
9. If you upgraded K3s, `sudo k3s kubectl get nodes` shows the new version on the control plane. It shows the new version on the workers after the operator has finished with them (see [Workers](#workers)).
10. To confirm that the whole path works, deploy a small change to one service.

## How an update stays safe

An update of a cluster is dangerous for one reason. The new release must read what the cluster stored with the old release, and write it back without loss. Geass guards that in four places:

- **Definitions only grow.** The custom resource definitions can add fields, loosen bounds, and widen enums. They cannot remove or rename a field, change a type, add a required field, or tighten a bound. The reason is that the API server drops fields that the schema no longer has from every stored object the next time that the object is written. CI compares every change to the definitions with the previous release (`make crd-compat`). A change that would lose or invalidate stored data fails the build, unless it is listed with its reason in `hack/crd-compat-exceptions.yaml`. A test installs the definitions of the previous release in a real API server and stores objects with every field set. Then it swaps in the current definitions, and checks that nothing was lost and that everything can still be updated.
- **Data migrations.** A release that must reshape stored data adds the new shape beside the old one, and a migration that fills it in. Before any controller starts, the operator runs the migrations that it knows, one time each, in order. It records how far it got in the ConfigMap `geass-schema`. First, the operator waits until the operator that it replaces has let go of leader election. So the old operator cannot write back over what a migration changed.
- **The operator does not run over newer data.** If the cluster has been migrated further than the operator knows, the operator refuses to start and says so: `the data in this cluster is at schema level 7 by Geass v9.9.9, but this operator only knows up to level 1`. It crash-loops until you install the release that wrote the data or a newer release, or restore a snapshot. The installer does not roll back over data like that. See [When the new operator does not take over](#when-the-new-operator-does-not-take-over).
- **The installer checks the cluster, not only the release.** The installer refuses definitions that would stop serving a version that the cluster still has objects in, however many releases the cluster skipped.

## Troubleshooting

**`✗ check the cluster before updating failed: ...`** Nothing was changed. The message says what stopped the update. See [What stops an update](#what-stops-an-update).

**`this machine already has Geass installed`.** You asked for an install on a machine that runs Geass. To update it, run the installer without options. To delete what is there, pass `--reinstall`.

**`K3s is installed here but Geass is not in it`, `its API server did not answer`, or `this machine is a worker`.** The installer does not touch a K3s that it cannot see Geass in, whatever else you pass. Find out why (`sudo systemctl status k3s`, `sudo k3s kubectl get nodes`), or pass `--reinstall` to delete what is there.

**`✗ wait for the new operator to take over failed`.** The new operator did not report itself within 5 minutes. The installer prints the end of its log. A message that starts `the data in this cluster is at schema level` means that the operator is older than the data. Install a newer release, or restore the snapshot. Otherwise, look at the pod: `sudo k3s kubectl -n geass-system describe pod -l control-plane=controller-manager`.

**The installer says the operator was rolled back.** The new release could not start, and the previous operator runs again. Read the log in the message, and fix what it says, or install a release that fits. Then run the installer again. See [When the new operator does not take over](#when-the-new-operator-does-not-take-over).

**The installer says the operator was not rolled back.** The installer says why in the same message. There are three possible reasons:

- The data was already migrated past what the previous operator knows.
- The new operator had started.
- You passed `--no-rollback`.

Nothing that can reconcile is running. Services keep running, but nothing changes until you do one of these things: fix the new operator, install the release that fits the data, or restore the snapshot.

**The operator pod is in `ImagePullBackOff`.** The image is not in the containerd of the control plane under that name and tag. The installer imports the image there. If you applied the manifests yourself, import the image on the control plane, not on a worker.

**`✗ upgrade k3s (control plane) failed: ... the control plane did not come back`.** Look at `sudo journalctl -u k3s`. The snapshot taken before is the way back. See [Go back](#go-back). The installer cannot downgrade K3s.

**`could not take an etcd snapshot`.** K3s snapshots only an embedded etcd datastore, which is what Geass installs. If this server was set up in a different way, and you accept that there is no way back, add `--skip-snapshot`.

**Services that used to reach each other time out after an update.** Network isolation applies to projects that predate it. Allow the caller under **Settings → Network** of the receiving project.

**The cluster stays not ready after an update.** An add-on is upgrading or failing. To see which add-on, look at `sudo k3s kubectl -n kube-system get helmcharts` and the pods in `monitoring`, `longhorn-system`, `cert-manager`, and `geass-logging`.

**The workers' K3s upgrade is `Failed`.** The message in `status.k3s.upgrade` names the node and says why. The node is cordoned. Fix the node, then annotate the cluster with `geass.dev/retry-k3s-upgrade` as shown in [Workers](#workers).

**`geass` says `invalid or expired API token`, or `not signed in`.** Tokens live in the cluster, so an update keeps them. This error occurs after a new server or a reinstall. Run `geass login --url <address>` again, and replace the tokens in CI secrets and agent configurations. If the address changed, `geass config get-contexts` lists the logins that the command line still has.

**A reinstall removed something you needed.** Nothing in Geass restores it. The database of the cluster was deleted with the data directory of K3s. The last snapshot is in `/var/backups/geass`. Create again what you need from your records, and restore data from your own dumps.

## Related

- [Install Geass](/docs/getting-started/install/)
- [Uninstall Geass](/docs/operations/uninstall/)
- [Command line](/docs/guides/command-line/)
- [Backups and restore](/docs/guides/backups-and-restore/)
- [Network isolation](/docs/guides/network-isolation/)
- [High availability](/docs/operations/high-availability/)
- [GeassCluster reference](/docs/reference/resources/geass-cluster/)
