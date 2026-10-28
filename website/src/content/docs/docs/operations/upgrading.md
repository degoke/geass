---
title: "Upgrade Geass"
description: "Update a running cluster to a new Geass release, and K3s with it, without rebuilding it: what the installer checks and keeps, how workers follow, rolling back."
---

Run the installer of the release you want on the control plane. When the machine already runs Geass, the installer updates it: it keeps the cluster, its data and its settings, takes a snapshot of the cluster's etcd first, replaces the Geass custom resource definitions and the operator, and waits until the new operator has taken over. It never removes K3s unless you tell it to with `--reinstall`.

Three things can be updated, and each has its own rules:

| What | Who does it | How |
| --- | --- | --- |
| **Geass**: the operator, the dashboard, the custom resource definitions, the RBAC | The installer, on the control plane | Run the new release's installer. See [Update Geass](#update-geass) |
| **K3s** on the control plane | The installer, on the control plane | Add `--k3s-version`. See [Update K3s](#update-k3s) |
| **K3s** on the workers | The operator, over SSH, one node at a time | Follows the control plane once `spec.k3s.version` says so. See [Workers](#workers) |

On a Geass that a platform runs for you, with [managed mode](/docs/operations/managed-mode/) on, the platform does all three, and the
dashboard says so instead of telling you to run the installer. Managed mode does not change the installer itself.

## Update Geass

SSH into the control plane and run the installer of the release you want:

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo sh
```

To pin a release, use its `install.sh` and set `GEASS_VERSION`, as in [Install Geass](/docs/getting-started/install/#pin-a-version). To pass an option, put it after `-s --`:

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo sh -s -- --force
```

The installer looks at the machine first. A machine with no K3s gets a new cluster. A machine whose K3s has Geass in it gets an update. Anything else it refuses, and tells you why: a K3s that Geass did not set up, a K3s whose API server does not answer, a worker. The installer deletes a cluster only when you pass `--reinstall`.

### What it does

The installer prints each step as `→ <name>` and `✓ <name>`. An update runs these, in order, and stops at the first that fails:

| # | Step | What it does |
| --- | --- | --- |
| 1 | `check the cluster before updating` | Reads the cluster and changes nothing. Refuses a downgrade, a cluster that is not ready, degraded Longhorn volumes, and custom resource definitions that would leave objects unreadable. See [What stops an update](#what-stops-an-update) |
| 2 | `snapshot the cluster's etcd` | Runs `k3s etcd-snapshot save` before anything is changed. The snapshot is the way back: see [Go back](#go-back) |
| 3 | `upgrade k3s (control plane)` | Only with `--k3s-version`. See [Update K3s](#update-k3s) |
| 4 | `import operator image` | Loads the operator image that the installer carries into K3s's containerd |
| 5 | `update custom resource definitions` | Applies the definitions with server-side apply and waits until the API server serves them |
| 6 | `update geass-operator` | Applies the operator's namespace, permissions, Deployment and Services |
| 7 | `wait for the new operator to take over` | Waits for the Deployment to roll out (up to 15 minutes, so that a data migration that takes the 12 minutes the pod's startup probe allows is not cut short, and gives up at once on a pod that is crash looping or cannot pull its image), then up to 5 minutes until the cluster object reports the release that was installed (`status.release`). A rollout that finished only says that some operator is running; the report says it is the new one and that it got past its migrations. If it does not take over, the installer **rolls the operator back**: see [When the new operator does not take over](#when-the-new-operator-does-not-take-over) |
| 8 | `set the K3s version for the workers` | Only with `--k3s-version`. Sets `spec.k3s.version` on the cluster object |
| 9 | `wait for the cluster to be ready` | Waits up to 10 minutes for `READY` `True`. A cluster that is not ready by then is a warning and not a failure: an add-on whose chart version moved takes its time |
| 10 | `summary` | What changed, and where the snapshot is |

Things the update does not do: it does not apply the cluster object again (that would reset your settings: change them on the object itself), it does not read `GEASS_ACME_EMAIL`, `GEASS_MULTI_SITE` or `GEASS_NODE_EXTERNAL_IP` (they describe a new cluster, and the installer says it ignores them), it does not touch the join token, and it does not remove anything that an earlier release created and this one no longer ships.

Running the installer again for the release the cluster already runs is safe and restarts nothing: the operator's pod template changes only when the release or the image does.

### Options

| Option | Meaning |
| --- | --- |
| `--k3s-version <release>` | Also upgrade K3s, to this release. The full name, such as `v1.36.5+k3s1`: a channel name such as `stable` is not accepted. Only the option does this: `GEASS_K3S_VERSION` picks the K3s of a new cluster and is ignored, with a warning, when a cluster is updated |
| `--allow-untested-k3s` | Allow a K3s version outside the range this release's add-ons are known to run on (Kubernetes 1.25 to 1.36 for this release line) |
| `--force` | Go on although the cluster is not ready, or a Longhorn volume is degraded. The checks protect data: use it after you have read what they say. It does not go past the rules that keep the data readable: a downgrade, a data schema newer than the installer's, and the K3s rules (no downgrade, one minor version at a time) are refused whatever you pass |
| `--skip-snapshot` | Update without taking a snapshot first. There is then no way back to the cluster as it is |
| `--no-rollback` | If the new operator does not take over, leave it where it is instead of rolling the operator back, to look at what went wrong in the cluster it went wrong in. Also `GEASS_NO_ROLLBACK=1` |
| `--upgrade` | Say that you mean to update. `geass upgrade` reads the same. The default when the machine has a cluster |
| `--reinstall`, `--yes` | Delete the cluster and build a new one. See [Reinstall](#reinstall) |
| `etcd-snapshots enable`, `disable`, `status` | Not an update: turn on, turn off or look at K3s on this server sending its snapshots to an S3 bucket of your own. See [Disaster recovery](/docs/operations/disaster-recovery/) |
| `--join-server <url>` | Not an update: make a machine that has no K3s an additional control plane server of this cluster, at the K3s release you give with `--k3s-version`. See [High availability](/docs/operations/high-availability/#run-three-control-plane-servers) |
| `--version`, `--help` | Print the installer's release, or the options |

### What stops an update

The first step refuses, and says what to do, when:

- **The cluster runs a newer release than the installer.** Geass does not downgrade: the newer release may have stored data in a shape that an older operator misreads. A cluster installed before releases were recorded has no release to compare, and is updated.
- **The cluster is not ready.** An add-on may still be installing or failing, and updating the operator on top of that makes it harder to tell which change broke what.
- **A Longhorn volume that is attached has fewer good copies than it should.** Anything that restarts components risks the copy that is left.
- **The release would leave objects unreadable.** The installer compares the definitions it carries with what the cluster stores: if the cluster has objects stored in an API version that the release no longer serves, the API server could not read them. Update to the release in between first.
- **With `--k3s-version`:** the version is not a K3s release, is older than what runs, is more than one minor version ahead, is outside the supported range, or a node is not ready.
- **Without it:** the cluster runs a K3s older than this release's add-ons need (Kubernetes 1.25). Upgrade K3s first.

It also refuses a cluster whose data is at a schema level newer than the installer knows of, which it can compare even when the two releases cannot be ordered (a build from an untagged checkout has no version number to compare).

It warns, and goes on, about nodes that are not ready (when K3s is not being upgraded), about a cluster with more than one control plane node (the installer upgrades only the one it runs on), and about databases that are not backed up: the etcd snapshot holds the cluster's objects, **not the contents of your databases**.

### What is kept

| | Kept by an update |
| --- | --- |
| Projects, services, databases, buckets, nodes, connections, settings | Yes. Stored in etcd, which the update does not touch |
| Secrets, dashboard users, API tokens (including the command line's and agents'), the join token | Yes |
| Database data and volumes | Yes. The update does not touch them |
| Workers | Yes. They keep running, and keep their K3s until `spec.k3s.version` says otherwise |
| Running services | Yes, apart from the operator and the dashboard, which restart: expect them to be away for a minute or less |
| Add-ons | Upgraded by the operator when the new release raises a chart pin: see [Add-ons](#add-ons) |

## Update K3s

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo sh -s -- --k3s-version v1.36.5+k3s1
```

With `--k3s-version` the installer upgrades the control plane's K3s in the way K3s documents: it runs K3s's install script again with the new version, which replaces the binary and restarts the service. The cluster's data (etcd, the join token, the configuration) is left where it is, and the server keeps the arguments it runs with: the installer reads them out of the service unit, so a server that an administrator changed is not silently reset to the default.

The rules are Kubernetes's own:

- **One minor version at a time.** From 1.34 you go to 1.35, then to 1.36. The installer refuses a longer step and names the version to go to first.
- **Never down.** K3s does not support downgrades. The way back is the snapshot.
- **Control plane first.** Workers are only ever moved to a version the control plane already runs.

The API server and the operator are away while the service restarts, normally for under a minute. The installer waits up to 10 minutes for the control plane to report the new version and be ready again. Containers that are running are not stopped: K3s's service is configured so that restarting it leaves them alone. A Kubernetes release that changes how the kubelet computes a container's hash can restart containers in place, so a single-instance database may restart once. Its data is on its volume and is not touched.

K3s and the add-ons move together, because some chart versions need a minimum Kubernetes version (Longhorn 1.13, for example, needs 1.34, and this release pins Longhorn 1.12 for that reason). The range that this release's add-ons are known to run on is built into the installer; outside it, the installer refuses unless you add `--allow-untested-k3s`.

If you ask for a K3s version when you update Geass, the order is: snapshot, K3s on the control plane, then the operator. The operator must come up on the K3s it will run on.

### Additional control plane servers

A cluster that has [three control plane servers](/docs/operations/high-availability/#run-three-control-plane-servers) is updated on the **first** one, with the installer, as everything above says. The others are not touched by it, and two things follow:

- **The operator's image, first.** The update leaves the operator's Deployment naming a new image, and the installer puts that image on the machine it runs on and on no other: it is in no registry. The operator can start on any control plane server, so **before you update the first server**, run the new release's installer on each of the other servers with no options. On a server that joined a cluster, that is all it does: it puts the new image on the server, next to the old one, and changes nothing else. It refuses `--upgrade` and `--reinstall` on such a server. If you update the first server before the others have the image, the new operator can land on one of them, cannot pull it, and the installer sees the pod cannot pull its image and puts the previous operator back. It names the servers that do not report the image before it changes anything. A server that you have only just refreshed can still be named for some seconds, until its node reports the image; if you have refreshed it, go on.
- **K3s.** With `--k3s-version` the installer upgrades the first server. Then upgrade each of the others **one at a time**, waiting for its node to be ready before the next, with K3s's own install script and the release you gave the first (`curl -sfL https://get.k3s.io | sudo env INSTALL_K3S_VERSION=<release> INSTALL_K3S_EXEC=server sh -`). The server keeps its settings, which are in its configuration file. Geass does not move the workers until the oldest control plane server runs the new release, because a worker must not run newer than the servers it talks to. Until then the cluster's status says so and names the servers that are behind (`kubectl -n geass-system get geasscluster default -o jsonpath='{.status.k3s}'` shows `WaitingForControlPlane`). When the last one runs the new release, the operator moves each worker, one at a time. On a test cluster of three servers and one worker, each server was ready again within about 40 seconds of its upgrade, and the worker was moved within about 25 seconds of the last server.

### Workers

The operator moves workers when `spec.k3s.version` on the cluster object asks for a version. The installer sets it for you when you use `--k3s-version`. You can also set it yourself:

```bash
sudo k3s kubectl -n geass-system patch geasscluster default --type merge \
  -p '{"spec":{"k3s":{"version":"v1.36.5+k3s1"}}}'
```

The operator never touches the control plane: it is a pod on that machine and cannot restart K3s underneath itself. If `spec.k3s.version` is ahead of the control plane, nothing happens and the status says to upgrade the control plane first. Left empty, the field means "leave K3s alone on every node", and a new worker installs the K3s that the control plane runs.

For each worker that runs an older K3s than the target, one at a time and in name order, the operator:

1. Waits until no node is not ready and no attached Longhorn volume has lost a copy. Taking a node down while another is already down is how a volume loses its last copy.
2. **Cordons** the node (and marks that it did).
3. Connects over SSH with the node's key (and the host key you recorded for it, if any), checks that the cluster's address is reachable from it, writes the node's `/etc/rancher/k3s/config.yaml` from its `placement` as joining does (so a hand edit of that file is replaced), and runs K3s's install script for the new version with the node's own name, address and token. That replaces the binary and restarts the agent.
4. Waits until the node is ready and reports the new version.
5. Uncordons the node, if the upgrade is what cordoned it.

Machines that Geass created at a cloud, including the nodes of a [pool](/docs/guides/node-pools/), are upgraded the same way, with the SSH key Geass made for them. An autoscaling pool removes no node while the upgrade is going on, and a machine made after it installs the control plane's K3s version.

The node is **cordoned, not drained**. The agent restarts without stopping the containers it runs, so services keep serving through it, and a drain would move a database's pod away from its local data. A node that you had cordoned yourself stays cordoned.

It stops at the first node that does not come back ready on the new version within 15 minutes, leaves that node cordoned, and never starts the next one. Watch it:

```bash
sudo k3s kubectl -n geass-system get geasscluster default -o jsonpath='{.status.k3s}{"\n"}'
```

| `status.k3s.upgrade.phase` | Meaning |
| --- | --- |
| `WaitingForControlPlane` | The workers are behind the target and the control plane is not on it yet |
| `Waiting` | The next worker is not started because a node is not ready or a volume is degraded. The message says which |
| `Upgrading` | A worker is being upgraded: `currentNode` |
| `Complete` | Every worker Geass manages runs the target or newer. The message lists worker nodes that Geass does not manage (no `GeassNode`, so no SSH key): upgrade those by hand |
| `Failed` | A node did not come back, or could not be reached. The message says why and how to try again |

After you have fixed what failed, ask for another try. The request is consumed:

```bash
sudo k3s kubectl -n geass-system annotate geasscluster default geass.dev/retry-k3s-upgrade=now
```

Setting `spec.k3s.version` to something else, or to empty, abandons the upgrade in progress and uncordons the node it was working on.

### When the new operator does not take over

An update stops the old operator before it starts the new one (a new operator migrates data when it starts, and an old one must not be writing meanwhile). A new operator that then cannot start would leave a cluster with nothing reconciling, so the installer puts the old one back.

When the new operator crash-loops, cannot pull its image, never becomes ready, or never reports its release, the installer:

1. Reads what says why, **before** it changes anything: the operator's pods, the end of its log, and the log of the container before it restarted. A rollback replaces the pod that holds them.
2. Checks that going back is safe (below).
3. Runs `kubectl rollout undo` on the operator Deployment, waits for the previous operator to be running again, and stops there. The update is reported as failed with the reason, the later steps (the K3s target for the workers, the wait for the cluster) do not run, and the exit status is not zero.

What is left changed: the custom resource definitions (they only grow, so the previous operator reads everything), the operator's permissions, and K3s on the control plane when you asked for that (K3s does not go back). The cluster object, `spec.k3s.version` and the workers are not touched. The snapshot taken first is still the way to undo all of it.

The installer rolls back only when it is safe, and says why when it is not:

| It does not roll back when | Because |
| --- | --- |
| The data's schema level is past what the previous operator reported (or past the baseline, for a cluster from before levels were recorded) | The new operator migrated and then failed. The previous operator would refuse that data, or misread it if it predates the record. Fix forward, install a release that fits, or restore the snapshot |
| The new operator reported its release | It ran, and may have written fields that the previous one does not know. An operator that writes an object back drops what it does not know |
| This run did not replace the operator | There is no earlier one to go back to: `rollout undo` would go to an older revision than the one that was running |
| The data's schema level cannot be read | It cannot be said that the previous operator can read it |
| `--no-rollback` was passed | You asked to look at it |

A rollback can fail too. If the previous operator does not start either, the installer says so. The usual cause is that K3s removed its image from containerd, which it does when it needs disk space and the image is unused (a crash-looping new operator leaves the old image unused): restore the snapshot, and install the release again once the new operator's problem is fixed.

## Add-ons

The operator installs these Helm charts through K3s's Helm controller at fixed versions, and applies a changed pin on its next reconcile:

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

A release that raises a pin makes the add-on upgrade itself, which restarts its pods. The SeaweedFS and Garage images that the in-cluster object storage server runs from, MySQL, Redis and other images and charts are pinned the same way. The update waits for the cluster to report ready again, but does not manage the add-ons' own upgrades: Geass does not upgrade the engine of Longhorn volumes that already exist when Longhorn itself moves, so read Longhorn's upgrade notes for the version a release pins.

## Go back

K3s does not downgrade and the operator refuses to run over data that a newer release has changed, so the way back from an update that went wrong is the snapshot that the installer took first. It is a copy of everything the cluster stores in etcd (the custom resources, Secrets, the operator's own Deployment and the definitions) as it was before the update.

The snapshot is in K3s's snapshot directory, named for the update:

```bash
sudo ls /var/lib/rancher/k3s/server/db/snapshots/ | grep geass-pre-upgrade
sudo k3s etcd-snapshot ls
```

To restore it, on the control plane:

```bash
sudo systemctl stop k3s
sudo k3s server --cluster-reset --cluster-reset-restore-path=/var/lib/rancher/k3s/server/db/snapshots/<snapshot name>
sudo systemctl start k3s
```

`--cluster-reset` ends with `restart without --cluster-reset flag now`: that is the normal end. Starting K3s again brings the cluster back as it was when the snapshot was taken, with the old operator and the old definitions. Everything that changed in the cluster since is lost: services and projects that were created, secrets, API tokens that were issued.

The snapshot holds the Secrets encrypted, because the installer turns on K3s secrets encryption. On the machine the snapshot came from, the server token and the setting are already there. To restore on another machine you need two things the snapshot does not carry:

- **The cluster's server token**, the one in `/var/lib/rancher/k3s/server/token`. K3s encrypts the snapshot's bootstrap data with it, and a different token stops the restore with `bootstrap data already found and encrypted with different token`. Keep a copy outside the cluster: the one in the Secret `geass-token` is inside the data you would be restoring.
- **`secrets-encryption: true` in that machine's `/etc/rancher/k3s/config.yaml`**, which is what the installer writes. Without it the restore finishes, but reading an encrypted Secret fails with `identity transformer tried to read encrypted data`.

The restore brings the cluster's data back and leaves K3s on the version it runs now, which is what you want when only Geass needs to go back: the older operator runs on the newer K3s, within the range its add-ons were tested on.

If you also upgraded K3s and want the old version back, and no worker has been upgraded yet, install the old version before you restore. K3s supports going back only this way, from a snapshot that was taken on that version:

```bash
curl -sfL https://get.k3s.io | sudo INSTALL_K3S_VERSION=<the old version> INSTALL_K3S_SKIP_START=true sh -s - server --cluster-init
```

Do not do that once workers have been upgraded: an agent must not be newer than its server, and K3s does not support going down on a worker either. Restore the data, keep the control plane's K3s where it is, and set `spec.k3s.version` back to empty (the restored cluster object has the version it had before the update, which is usually empty).

Three things the snapshot does not cover:

- **The contents of your databases and volumes.** Only the cluster's objects. Database backups to S3 and your own dumps cover the data.
- **The operator's old image.** It stays in K3s's containerd until K3s needs the disk space. If the operator is in `ImagePullBackOff` after a restore, the image was removed: run the installer of the release you went back to, which loads its image again (releases that have an update mode do; a release before this one does not, and needs to be rebuilt from source as in [Update from source](#update-from-source)).
- **Anything outside the cluster.** S3 buckets, DNS records, Cloudflare tunnels and cloud accounts are untouched by an update, and so by a restore.

A snapshot contains every Secret in the cluster. Treat the snapshot directory as you treat the machine's disk, and delete old snapshots when you no longer need them. K3s also takes its own scheduled snapshots, twice a day, in the same directory. See [Security](/docs/operations/security/).

## Reinstall

`--reinstall` deletes the cluster on the machine and builds a new one:

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo sh -s -- --reinstall
```

It asks you to type `reinstall` on the terminal first, after saying what it will delete. In a script with no terminal, add `--yes`. Before it removes K3s it takes a last etcd snapshot into `/var/backups/geass`, where K3s's uninstall cannot reach it. Nothing restores that snapshot for you, and a snapshot of a cluster that is broken may not be possible, in which case the reinstall goes on without it.

K3s's uninstall script removes `/etc/rancher/k3s`, the K3s data directory (`/var/lib/rancher/k3s` by default) and `/var/lib/kubelet`. After a reinstall:

- Every project, service, database definition, secret, dashboard user, API token and connection of the old cluster is gone, and so is data on local-path volumes.
- Workers joined to the old cluster keep running their agents but hold credentials of a cluster that no longer exists. Join them again.
- Things outside K3s's directories are untouched: S3 buckets and backups, DNS records, Cloudflare tunnels, IAM users and your provider's resources. Longhorn's data directory is left on disk with nothing that refers to it.

Reinstall is for a machine where an install failed part-way and nothing of value exists yet, or to rebuild a cluster you mean to replace. For anything you care about, update, or [move to a new server](#move-to-a-new-server).

### Move to a new server

This is the safest path when you want a clean machine, or a different one: the old cluster keeps running until you decide to switch.

1. Install the release you want on a new server. See [Install Geass](/docs/getting-started/install/) and [Sign in to the dashboard](/docs/getting-started/sign-in/).
2. Recreate what the old cluster held: dashboard users, the Cloudflare and GitHub connectors, AWS and PlanetScale connections, projects, services, variables and secrets, domains, node regions, sites and tags (compliance tags included, and the project's required tags), API tokens, and the serverless add-on if you used it (`spec.addons.serverless.enabled`). Each service carries its own sleep-when-idle setting, and each function its schedules.
3. Sign the command line in to the new cluster with `geass login --url <new address>`, and put new tokens into CI secrets and agent configurations. Tokens do not move between clusters.
4. Move data with the databases' own tools, for example `pg_dump` and `pg_restore` over a port-forward, and copy bucket contents with an S3 client.
5. Point DNS at the new server when you have checked it, keep the old one for a while, then [uninstall it](/docs/operations/uninstall/).

:::caution
Geass has no export and import. Database backups in your S3 bucket survive, but the dashboard restores them from a backup record that lives in the old cluster. A new cluster has no such record, so it cannot list or restore the old cluster's backups. Dump your databases yourself before you retire a cluster.
:::

### Update from source

The operator, the dashboard and the manifests are one Deployment and a set of Kubernetes objects, so you can also build a release from source, load its image on the control plane and apply the manifests yourself. This is an advanced path that the release process does not cover, and it skips the checks, the snapshot and the wait that the installer does. Build on a machine of the control plane's architecture, `amd64` or `arm64`:

```bash
git clone https://github.com/degoke/geass.git
cd geass
git checkout v0.2.0
make docker-build IMG=geass-operator:v0.2.0 GEASS_VERSION=v0.2.0
docker save -o geass-operator.tar geass-operator:v0.2.0
```

Copy `geass-operator.tar` to the control plane, import it with `sudo k3s ctr images import geass-operator.tar`, take a snapshot (`sudo k3s etcd-snapshot save --name before-v0.2.0`), then with `KUBECONFIG` pointing at the cluster:

```bash
make deploy IMG=geass-operator:v0.2.0
sudo k3s kubectl -n geass-system rollout status deployment/geass-controller-manager
```

Do not swap only the image: a release can add custom resource fields and operator permissions, and `make deploy` applies them with it. Replace `v0.2.0` with the tag you are updating to.

## Before you update

1. **Read the release notes** on the GitHub release.
2. **Know what you run.** The dashboard shows it under **Usage**, **Version**, with the K3s version of every node, and `geass version` prints the cluster's release. The cluster records it too:

   ```bash
   sudo k3s kubectl -n geass-system get geasscluster
   sudo k3s kubectl -n kube-system get helmcharts \
     -o custom-columns=NAME:.metadata.name,VERSION:.spec.version
   ```

   The `RELEASE` column is the operator's release and `K3S` the control plane's. A cluster installed before releases were recorded shows neither until it has been updated once.
3. **Check network isolation.** Each project is private by default: only its own services, Geass itself and visitors who arrive through your domains can reach it. Projects that existed before isolation was added become isolated when Geass is updated. List every project that calls another one (a front end calling an internal API, say) before you update, and be ready to allow it under the receiving project's **Settings → Network**. See [Network isolation](/docs/guides/network-isolation/). Two rules hold whatever you choose: services cannot read the cloud provider's credentials for their machine, and no project can read another's metrics or logs.
4. **Check backups.** In each project's **Settings → Backups**, confirm that backup storage is set. Then open each database's **Backups** tab and confirm that its state is **Protected** and that a recent restore point exists. A database that is not protected shows **Not backed up** or **Backups at risk** on its canvas card. The installer lists the ones that are not. See [Backups and restore](/docs/guides/backups-and-restore/).
5. **Keep a copy of the sign-in Secret** (`geass-dashboard-auth`) and note which connections and connectors you use, in case you have to recreate them.
6. **Check capacity.** Add-on upgrades restart pods. Look at the **Usage** page for room to spare, and for room on the disk for a snapshot and a second operator image.
7. **If you use sleep when idle, check the add-on.** `sudo k3s kubectl -n kube-system get helmcharts geass-keda geass-keda-http` shows both, and `sudo k3s kubectl get crd httpscaledobjects.http.keda.sh` exists. A release that raises these chart pins upgrades the add-on, which restarts its pods. See [Sleep when idle](/docs/guides/scale-to-zero/).
8. **If you use compliance tags, plan the order.** In a new cluster, mark the compliance tags on the nodes first, then require them on the project, and only then recreate its databases: a database is placed once, when it is first provisioned, and the dashboard refuses to add a tag that a provisioned database does not run on. The admission policy `geass-required-node-tags` is a cluster object, so a new cluster makes it again the first time a project requires tags. See [Compliance node tags](/docs/guides/compliance-tags/).
9. **Check how you get certificates.** The operator creates the `letsencrypt-prod` issuer only when the cluster object has `spec.addons.certManager.acmeEmail`, and a cluster installed before that field existed does not. An issuer of that name that you created yourself is never overwritten. See [HTTPS certificates](/docs/getting-started/install/#https-certificates).
10. **If you upgrade K3s, look at your workers.** `sudo k3s kubectl get nodes` shows each node's version. Workers that Geass did not join have no SSH key in a `GeassNode` and are upgraded by hand.

## After you update

1. The installer ended with `✓ summary`. The operator is running the new image and its log has no repeating errors: `sudo k3s kubectl -n geass-system get pods`, and `sudo k3s kubectl -n geass-system logs deploy/geass-controller-manager -c manager`.
2. The cluster is ready: `sudo k3s kubectl -n geass-system get geasscluster` shows `READY` `True` and the `RELEASE` you installed. It can report not ready for a while if an add-on is upgrading. Look at `sudo k3s kubectl -n kube-system get helmcharts` and compare the versions.
3. Reload the dashboard, which is part of the new image, and sign in. **Usage**, **Version** shows the release you installed.
4. Open each project: services and databases should be **Ready**, and the top bar **Up to date**.
5. Check that services that call each other still can, and that each project has its isolation policies: `sudo k3s kubectl get networkpolicy -A | grep geass`.
6. **Update the command line** to the same release, on every machine and CI job that uses it: run `install-cli.sh` again, with `GEASS_VERSION` to pin a tag. The command line talks to the `/api/v1` API, whose resources carry the fields of the cluster's release, and `geass version` says when it and the cluster are different releases. Then `geass whoami` answers and `geass status -p <project> -e production` lists the services. Existing tokens keep working: they live in the cluster, and an update keeps the cluster.
7. With the metrics add-on on, the operator keeps its own monitor and alert rules up to date: `sudo k3s kubectl -n monitoring get servicemonitor,prometheusrule geass-operator geass-alerts`. The **Usage** page charts them under **Geass health**. The release that brought [alerts](/docs/guides/alerts/) changes the monitoring chart's settings: it starts Alertmanager, and opens Prometheus's rule selector, so that Prometheus loads rules that carry no `release` label, the operator's own seven among them, which clusters installed before it never did. Prometheus restarts once for that. Expect `GeassOperatorDown` to fire if the operator's metrics were never scraped, and look at `sudo k3s kubectl -n monitoring get pods`.
8. If you upgraded K3s, `sudo k3s kubectl get nodes` shows the new version on the control plane, and on the workers once the operator has finished with them (see [Workers](#workers)).
9. Deploy a small change to one service to confirm the whole path works.

## How an update stays safe

Updating a cluster is dangerous for one reason: what the cluster stored with the old release has to be read by the new one, and written back without loss. Geass guards that in four places.

- **Definitions only grow.** The custom resource definitions can add fields, loosen bounds and widen enums. They cannot remove or rename a field, change a type, add a required field or tighten a bound, because the API server drops fields that the schema no longer has from every stored object the next time it is written. Every change to the definitions is compared with the previous release in CI (`make crd-compat`), and a change that would lose or invalidate stored data fails the build unless it is listed, with its reason, in `hack/crd-compat-exceptions.yaml`. A test installs the previous release's definitions in a real API server, stores objects with every field set, swaps in the current definitions and checks that nothing was lost and everything can still be updated.
- **Data migrations.** A release that has to reshape stored data adds the new shape beside the old one and a migration that fills it in. The operator runs the migrations it knows once each, in order, before any controller starts, and records how far it got in the ConfigMap `geass-schema`. It waits first until the operator it replaces has let go of leader election, so that the old one cannot write back over what a migration changed.
- **The operator will not run over newer data.** If the cluster has been migrated further than the operator knows, it refuses to start and says so: `the data in this cluster is at schema level 7 by Geass v9.9.9, but this operator only knows up to level 1`. It crash-loops until you install the release that wrote the data or a newer one, or restore a snapshot. The installer does not roll back over data like that: see [When the new operator does not take over](#when-the-new-operator-does-not-take-over).
- **The installer checks the cluster, not just the release.** It refuses definitions that would stop serving a version the cluster still has objects in, however many releases the cluster skipped.

## Troubleshooting

**`✗ check the cluster before updating failed: ...`** Nothing was changed. The message says what stopped it: see [What stops an update](#what-stops-an-update).

**`this machine already has Geass installed`.** You asked for an install on a machine that runs Geass. Run the installer without options to update it, or pass `--reinstall` to delete what is there.

**`K3s is installed here but Geass is not in it`, `its API server did not answer`, or `this machine is a worker`.** The installer will not touch a K3s that it cannot see Geass in, whatever else you pass. Look at why (`sudo systemctl status k3s`, `sudo k3s kubectl get nodes`), or pass `--reinstall` to delete what is there.

**`✗ wait for the new operator to take over failed`.** The new operator did not report itself within 5 minutes. The installer prints the end of its log. A message that starts `the data in this cluster is at schema level` means the operator is older than the data: install a newer release, or restore the snapshot. Otherwise look at the pod: `sudo k3s kubectl -n geass-system describe pod -l control-plane=controller-manager`.

**The installer says the operator was rolled back.** The new release could not start, and the previous operator is running again: read the log in the message, fix what it says (or install a release that fits), and run the installer again. See [When the new operator does not take over](#when-the-new-operator-does-not-take-over).

**The installer says the operator was not rolled back.** It says why in the same message: the data was already migrated past what the previous operator knows, the new operator had started, or you passed `--no-rollback`. With nothing running that can reconcile, services keep running but nothing changes until you fix the new operator, install the release that fits the data, or restore the snapshot.

**The operator pod is in `ImagePullBackOff`.** The image is not in the control plane's containerd under that name and tag. The installer imports it there; if you applied the manifests yourself, import it on the control plane, not on a worker.

**`✗ upgrade k3s (control plane) failed: ... the control plane did not come back`.** Look at `sudo journalctl -u k3s`. The snapshot taken before is the way back: see [Go back](#go-back). The installer cannot downgrade K3s.

**`could not take an etcd snapshot`.** K3s snapshots only an embedded etcd datastore, which is what Geass installs. If this server was set up some other way and you accept that there is no way back, add `--skip-snapshot`.

**Services that used to reach each other time out after an update.** Network isolation applies to projects that predate it. Allow the caller under the receiving project's **Settings → Network**.

**The cluster stays not ready after an update.** An add-on is upgrading or failing. `sudo k3s kubectl -n kube-system get helmcharts` and the pods in `monitoring`, `longhorn-system`, `cert-manager` and `geass-logging` show which.

**The workers' K3s upgrade is `Failed`.** The message in `status.k3s.upgrade` names the node and says why. The node is cordoned; fix it, then annotate the cluster with `geass.dev/retry-k3s-upgrade` as shown in [Workers](#workers).

**`geass` says `invalid or expired API token`, or `not signed in`.** Tokens live in the cluster, so an update keeps them. This happens after a new server or a reinstall: run `geass login --url <address>` again, and replace the tokens in CI secrets and agent configurations. If the address changed, `geass config get-contexts` lists the logins the command line still has.

**A reinstall removed something you needed.** Nothing in Geass restores it. The cluster's database was deleted with K3s's data directory; the last snapshot is in `/var/backups/geass`. Recreate what you need from your records, and restore data from your own dumps.

## Related

- [Install Geass](/docs/getting-started/install/)
- [Uninstall Geass](/docs/operations/uninstall/)
- [Command line](/docs/guides/command-line/)
- [Backups and restore](/docs/guides/backups-and-restore/)
- [Network isolation](/docs/guides/network-isolation/)
- [High availability](/docs/operations/high-availability/)
- [GeassCluster reference](/docs/reference/resources/geass-cluster/)
