---
title: "Upgrade Geass"
description: "Update Geass to a new release by running the installer again: what it checks and keeps, how the other servers follow, and how to go back."
---

To update Geass, run the installer of the new release again on the control plane server. The control plane server is the first server, where you installed Geass. The installer keeps your projects, services, databases, settings, and users. Before it changes anything, it checks the cluster and takes a snapshot of the cluster's state. If the new release does not start, the installer puts the previous release back.

If a platform runs Geass for you in [managed mode](/docs/operations/managed-mode/), the platform updates Geass. The **Version** section then says `Updating Geass is managed by <platform> and cannot be changed here.` Do not run the installer in that case.

## See which release you run

1. Open **Usage** in the left rail.
2. Find the **Version** section. It has these rows:
   - **Geass**: the release of Geass, and its data schema level.
   - **K3s (control plane)**: the release of K3s on the control plane server. K3s is the Kubernetes distribution that Geass installs.
   - **K3s on workers (target)**: the K3s release that the other servers move to. This row shows only while a K3s update is in progress or has ended. See [Workers](#workers).
3. To see the K3s release of each server, look at the **K3s** column of the **Nodes** table.

From the command line, `geass version` prints the release of the command line and of the cluster. It also says when the two releases are different.

## Before you update

1. Read the release notes on the GitHub release.
2. Check your database backups. The snapshot holds the state of the cluster, not the contents of your databases. Open the **Backups** tab of each database, and make sure that its state is **Protected** and that it has a recent restore point. See [Backups and restore](/docs/guides/backups-and-restore/).
3. Check for spare capacity on the **Usage** page. Add-on updates restart some of their parts. The server also needs disk space for the snapshot and for a second copy of the Geass image.
4. If your cluster has three control plane servers, run the new installer on the other servers first. See [Additional control plane servers](#additional-control-plane-servers).
5. If the release is the first one with network isolation, list each project that calls another project. After the update, allow each caller under **Settings** > **Network** of the receiving project. See [Network isolation](/docs/guides/network-isolation/).

## Update Geass

1. Connect to the control plane server with SSH.
2. Run the installer of the new release:

   ```bash
   curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo sh
   ```

3. Wait for the installer to end with `✓ summary`.
4. Reload the dashboard. The **Geass** row of **Version** shows the new release.

To install a specific release, set `GEASS_VERSION`, as in [Install Geass](/docs/getting-started/install/#pin-a-version). To give the installer an option, put the option after `-s --`:

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo sh -s -- --force
```

The installer looks at the machine first:

- On a machine with no K3s, it installs a new cluster.
- On a machine that runs Geass, it updates Geass.
- On any other machine, it stops and says why. Examples are a K3s that Geass did not install, a K3s that does not answer, and a worker.

The installer deletes a cluster only when you give `--reinstall`.

### What the installer does

The installer prints each step as `→ <name>` and `✓ <name>`. It stops at the first step that fails:

| Step | What it does |
| --- | --- |
| `check the cluster before updating` | Reads the cluster and changes nothing. See [What stops an update](#what-stops-an-update). |
| `snapshot the cluster's etcd` | Takes a snapshot of the cluster's state. The snapshot is the way back. See [Go back](#go-back). |
| `upgrade k3s (control plane)` | Updates K3s on this server. Only with `--k3s-version`. |
| `import operator image`, `update custom resource definitions`, `update geass-operator` | Installs the new release of Geass. |
| `wait for the new operator to take over` | Waits up to 15 minutes for the new Geass to start and report its release. If it does not, the installer puts the previous release back. See [When the new operator does not take over](#when-the-new-operator-does-not-take-over). |
| `set the K3s version for the workers` | Tells Geass to update K3s on the other servers. Only with `--k3s-version`. |
| `wait for the cluster to be ready` | Waits up to 10 minutes. A cluster that is not ready by then is a warning, because an add-on update can take longer. |
| `summary` | Says what changed and where the snapshot is. |

The update does not reset your settings, and it does not change the join token. It ignores `GEASS_ACME_EMAIL`, `GEASS_MULTI_SITE`, and `GEASS_NODE_EXTERNAL_IP`, because these variables describe a new cluster. It does not remove things that an earlier release made and the new release no longer uses.

You can run the installer again for the release that the cluster already runs. It then restarts nothing.

### What is kept

| | Kept by an update |
| --- | --- |
| Projects, services, databases, buckets, servers, connections, and settings | Yes |
| Secret variables, dashboard users, API tokens, and the join token | Yes |
| Database data and volumes | Yes. The update does not touch them. |
| Other servers | Yes. They keep running, and keep their K3s release until you update K3s. |
| Running services | Yes. Geass and the dashboard restart, and are unavailable for a minute or less. |
| Add-ons | Updated when the new release uses a newer version. Their parts restart. |

### Options

| Option | Meaning |
| --- | --- |
| `--k3s-version <release>` | Also update K3s to this release. Use the full name, for example `v1.36.5+k3s1`. The installer does not accept a channel name such as `stable`. See [Update K3s](#update-k3s). |
| `--allow-untested-k3s` | Allow a K3s release outside the range that this release is tested on. For this release line, the range is Kubernetes 1.25 to 1.36. |
| `--force` | Continue, although the cluster is not ready or a replicated volume has lost a copy. Read what the check says before you use this option. It does not allow a downgrade or data that is newer than the installer. |
| `--skip-snapshot` | Update without a snapshot. You then have no way back. |
| `--no-rollback` | If the new release does not start, leave it in place. Use this option to examine the failure. `GEASS_NO_ROLLBACK=1` does the same. |
| `--upgrade` | Say that you want an update. This is the default when the machine has a cluster. |
| `--reinstall`, `--yes` | Delete the cluster and install a new one. See [Reinstall](#reinstall). |
| `etcd-snapshots enable`, `disable`, `status` | Not an update. Send the snapshots of this server to an S3 bucket of your own. See [Disaster recovery](/docs/operations/disaster-recovery/). |
| `--join-server <url>` | Not an update. Add this machine as one more control plane server. See [High availability](/docs/operations/high-availability/#run-three-control-plane-servers). |
| `--version`, `--help` | Print the release of the installer, or the options. |

### What stops an update

The first step stops the update, and says what to do, in these cases:

- **The cluster runs a newer release than the installer.** Geass does not go back to an older release. An older release might misread data that a newer release stored.
- **The cluster stores data at a newer schema level than the installer knows.** This check also works for a build that has no release number.
- **The cluster is not ready.** An add-on might still be installing or failing. Fix that first, so that you can tell which change caused a problem.
- **A replicated volume in use has fewer good copies than it should.** A restart can put the last copy at risk.
- **The cluster still holds data in a format that the new release does not read.** Update to a release in between first.
- **With `--k3s-version`:** the release is not a K3s release, is older than the current release, is more than one minor release ahead, or is outside the tested range. A server that is not ready also stops the update.
- **Without `--k3s-version`:** the cluster runs a K3s that is older than this release needs (Kubernetes 1.25). Update K3s first.

The first step warns, and continues, in these cases:

- A server is not ready, and you do not update K3s.
- The cluster has more than one control plane server. The installer updates only the server that it runs on.
- Some databases are not backed up. The installer lists them.

## Update K3s

To update K3s together with Geass, add `--k3s-version`:

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo sh -s -- --k3s-version v1.36.5+k3s1
```

The installer updates K3s on the control plane server first. Then Geass updates the other servers, one at a time. The order is: the snapshot, K3s on the control plane server, Geass, and then the [workers](#workers).

These rules apply:

- **Go one minor release at a time.** From 1.34, go to 1.35, and then to 1.36. The installer refuses a larger step, and names the release to go to first.
- **You cannot go down.** K3s does not support a downgrade. The way back is the snapshot.
- **The control plane goes first.** Geass moves the other servers only to a release that the control plane already runs.

While K3s restarts, Geass and the dashboard are unavailable, normally for less than a minute. The installer waits up to 10 minutes for the server to be ready again. Running services continue to run. A single-instance database might restart one time. Its data stays on its volume.

### Additional control plane servers

On a cluster with [three control plane servers](/docs/operations/high-availability/#run-three-control-plane-servers), you update the first server, as on any cluster. The installer does not touch the other servers, so do these steps:

1. Before you update the first server, run the new installer on each of the other servers, with no options. On these servers, the installer only copies the new Geass image to the server. Geass can start on any control plane server, and the image is in no registry.
2. Update the first server.
3. If you used `--k3s-version`, update K3s on each of the other servers, one at a time. The installer does not do this step yet. See [Update K3s on the other control plane servers](#update-k3s-on-the-other-control-plane-servers).

If you skip step 1, the new Geass can start on a server that does not have its image. The installer then sees that it cannot start, and puts the previous release back. Before it starts, the installer names the servers that do not have the image yet. A server that you refreshed a few seconds before can still show in that list. If you refreshed that server, continue.

Geass does not move the workers until every control plane server runs the new K3s release. Until then, **Version** shows **Waiting for the control plane**, and **Detail** names the servers that are behind.

### Workers

After the control plane, Geass updates K3s on the workers. A worker is a server that runs services but is not a control plane server. To follow the progress, look at the **K3s on workers (target)** row in **Usage** > **Version**.

Geass updates one worker at a time, in name order. For each worker, Geass does these steps:

1. It waits until all servers are ready and no replicated volume has lost a copy.
2. It marks the worker so that nothing new starts on it. Services that run there continue to run.
3. It connects to the worker over SSH, and installs the new K3s release.
4. It waits until the worker is ready on the new release.
5. It removes the mark.

Geass updates machines that it created at a cloud in the same way, including the servers of a [pool](/docs/guides/node-pools/). An autoscaling pool removes no server during the update. A server that you add later gets the K3s release of the control plane.

The **Status** column shows these states:

| Status | Meaning |
| --- | --- |
| **Waiting for the control plane** | A control plane server does not run the target release yet. |
| **Waiting** | Geass waits before the next worker, because a server is not ready or a volume has lost a copy. **Detail** says which. |
| **Upgrading** | Geass is updating a worker. |
| **Up to date** | Every worker that Geass manages runs the target release. **Detail** lists workers that Geass did not join. Geass cannot update K3s on those workers. See [Under the hood](#under-the-hood). |
| **Failed** | A worker did not come back, or Geass could not reach it. **Detail** says why. |

Geass stops at the first worker that is not ready on the new release within 15 minutes. It leaves that worker marked, and does not start the next worker.

To try again after you fix the worker, see [Under the hood](#under-the-hood). The dashboard has no button for this step yet.

### When the new operator does not take over

The installer stops the previous release of Geass before it starts the new release. The new release might then fail to start. For example, it crashes, its image is missing, or it does not report its release in time. In that case, the installer does these steps:

1. It saves the log of the new release, before anything replaces it.
2. It checks that it is safe to go back.
3. It starts the previous release again, and reports the update as failed, with the reason.

The later steps do not run. K3s stays at the new release, if you updated it, because K3s does not go back.

The installer does not go back in these cases, and says why:

- The new release changed the stored data to a format that the previous release cannot read.
- The new release started and reported its release, so it might have written data that the previous release does not know.
- This run did not replace Geass, so there is no previous release to go back to.
- The installer cannot read the schema level of the data.
- You gave `--no-rollback`.

In these cases, fix the new release, install a release that fits the data, or [restore the snapshot](#go-back).

The previous release can also fail to start. The usual cause is that K3s deleted the old image to free disk space. In that case, restore the snapshot.

## Go back

To undo an update, restore the snapshot that the installer took first. The snapshot holds everything that the cluster stores: projects, services, secret variables, settings, and the previous Geass. Geass has no dashboard option or command to restore the snapshot yet. See [Restore the snapshot](#restore-the-snapshot).

After the restore, the cluster comes back as it was before the update. Everything that changed after the snapshot is lost, for example new services, projects, secret variables, and API tokens.

The restore keeps the current K3s release. The previous Geass runs on it, within its tested range.

The snapshot does not hold these things:

- **The contents of your databases and volumes.** Database backups and your own dumps hold the data.
- **The old Geass image.** K3s keeps it until it needs the disk space. If Geass does not start after a restore, run the installer of the release that you went back to. That loads the image again.
- **Anything outside the cluster**, for example S3 buckets, DNS records, Cloudflare tunnels, and cloud accounts.

:::danger
Protect the snapshot directory as you protect the disk of the server. A snapshot holds every secret variable and token in the cluster. Delete old snapshots when you do not need them. See [Security](/docs/operations/security/).
:::

## Reinstall

`--reinstall` deletes the cluster on the machine and installs a new one:

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo sh -s -- --reinstall
```

The installer says what it deletes, and asks you to enter `reinstall`. In a script with no terminal, add `--yes`. Before it deletes the cluster, the installer tries to take a last snapshot into `/var/backups/geass`. Nothing restores that snapshot for you.

After a reinstall, these things are true:

- Every project, service, database, secret variable, dashboard user, API token, and connection of the old cluster is gone.
- Data on volumes without replicated storage is gone.
- Other servers keep running, but they belong to a cluster that no longer exists. Add them again.
- Things outside the server stay: S3 buckets and backups, DNS records, Cloudflare tunnels, IAM users, and cloud machines.

Use a reinstall only for a machine where an install failed and nothing of value exists. For a cluster that you care about, update it, or move to a new server.

### Move to a new server

This path is the safest way to get a clean machine. The old cluster keeps running until you switch.

1. Install the release that you want on a new server. See [Install Geass](/docs/getting-started/install/) and [Sign in to the dashboard](/docs/getting-started/sign-in/).
2. Create again what the old cluster held:
   - Dashboard users and API tokens.
   - The Cloudflare and GitHub connectors, and the AWS and PlanetScale connections.
   - Projects, services, variables, secret variables, and domains.
   - Server regions, sites, and tags. To use [compliance node tags](/docs/guides/compliance-tags/), tag the servers first, then require the tags on the project, and then create its databases.
   - The [sleep-when-idle](/docs/guides/scale-to-zero/) add-on, if you used it.
3. Sign the command line in to the new cluster with `geass login --url <new address>`.
4. Put new tokens into your CI secrets and agent configurations. Tokens do not move between clusters.
5. Move your database data with the tools of each database, for example `pg_dump` and `pg_restore`.
6. Copy the contents of your buckets with an S3 client.
7. After you check the new server, point your DNS records at it.
8. Keep the old server for some time, and then [uninstall it](/docs/operations/uninstall/).

:::caution
Dump your databases yourself before you remove the old cluster. The new cluster cannot list or restore the backups of the old cluster, because the backup records are in the old cluster.
:::

## After you update

1. Make sure that the installer ended with `✓ summary`.
2. Reload the dashboard. **Usage** > **Version** shows the release that you installed.
3. Open each project. Its services and databases show **Ready**, and the top bar shows **Up to date**.
4. Make sure that services that call each other still can.
5. Update the command line to the same release on each machine and CI job that uses it. Run `install-cli.sh` again. Then run `geass version` and `geass whoami`.
6. If you updated K3s, the **K3s** column of the **Nodes** table shows the new release. The workers show it after Geass updates them.
7. Deploy a small change to one service, to test the full path.

## How an update stays safe

A new release must read what the old release stored, and write it back without loss. Geass protects that in four ways:

- **The data format only grows.** A release can add fields, but cannot remove or change them. A check on each change to Geass compares the format with the previous release, and fails on a change that loses data.
- **Data migrations run one time, in order.** A release that changes the shape of stored data adds the new shape beside the old shape. Before Geass starts work, it fills in the new shape. It waits until the previous release has stopped, so that the two do not write at the same time.
- **Geass does not run over newer data.** If the data is newer than Geass knows, Geass refuses to start. It says so, for example `the data in this cluster is at schema level 7 by Geass v9.9.9, but this operator only knows up to level 1`.
- **The installer checks the cluster, not only the release number.** It refuses a release that cannot read data that the cluster still holds, however many releases the cluster skipped.

## Troubleshooting

**`✗ check the cluster before updating failed: ...`.** Nothing changed. The message says what stopped the update. See [What stops an update](#what-stops-an-update).

**`this machine already has Geass installed`.** You asked for a new install on a machine that runs Geass. To update it, run the installer without options. To delete it, give `--reinstall`.

**`K3s is installed here but Geass is not in it`, `its API server did not answer`, or `this machine is a worker`.** The installer does not touch a K3s that it cannot find Geass in. To find the cause, see [Under the hood](#under-the-hood). To delete what is there, give `--reinstall`.

**`✗ wait for the new operator to take over failed`.** The new release did not start within the time limit. The installer prints the end of its log. A log line that starts with `the data in this cluster is at schema level` means that the release is older than the data. Install a newer release, or restore the snapshot.

**The installer says that it rolled back.** The new release did not start, and the previous release runs again. Fix what the log says, or install a release that fits. Then run the installer again.

**The installer says that it did not roll back.** The message says why. Services continue to run, but Geass makes no changes until you fix the new release, install a release that fits the data, or restore the snapshot.

**`✗ upgrade k3s (control plane) failed: ... the control plane did not come back`.** To read the K3s log, see [Under the hood](#under-the-hood). To go back, see [Go back](#go-back).

**`could not take an etcd snapshot`.** K3s can take a snapshot only of the embedded datastore that the installer sets up. If you accept that you have no way back, add `--skip-snapshot`.

**Services that used to reach each other time out after an update.** Network isolation now applies to their projects. Allow the caller under **Settings** > **Network** of the receiving project.

**The cluster stays not ready after an update.** An add-on is updating or failing. See [Under the hood](#under-the-hood) for the commands that show which one.

**The K3s update of the workers failed.** **Detail** names the worker and says why. Fix the worker, and try again as shown in [Workers](#workers).

**`geass` says `invalid or expired API token` or `not signed in`.** An update keeps tokens. This error comes after a move to a new server or a reinstall. Run `geass login --url <address>` again, and replace the tokens in your CI secrets and agent configurations.

## Under the hood

These commands run on the control plane server.

```bash
# Release of Geass (RELEASE) and K3s (K3S), and whether the cluster is ready
sudo k3s kubectl -n geass-system get geasscluster
# Versions of the add-ons
sudo k3s kubectl -n kube-system get helmcharts -o custom-columns=NAME:.metadata.name,VERSION:.spec.version
# Geass itself, and its log
sudo k3s kubectl -n geass-system get pods
sudo k3s kubectl -n geass-system logs deploy/geass-controller-manager -c manager
# Progress of the K3s update of the workers
sudo k3s kubectl -n geass-system get geasscluster default -o jsonpath='{.status.k3s}{"\n"}'
# Try the K3s update of the workers again, after you fix the worker
sudo k3s kubectl -n geass-system annotate geasscluster default geass.dev/retry-k3s-upgrade=now
# Why the installer cannot find Geass, or why the control plane did not come back
sudo systemctl status k3s
sudo journalctl -u k3s
```

- The K3s target for the workers is `spec.k3s.version` on the `GeassCluster`. You can set it yourself with `sudo k3s kubectl -n geass-system patch geasscluster default --type merge -p '{"spec":{"k3s":{"version":"v1.36.5+k3s1"}}}'`. An empty value means "leave K3s alone". A new value, or an empty value, stops an update in progress and uncordons the worker.
- Geass cordons a worker during its update. It does not drain the worker, because a drain would move a database away from its local data. A worker that you cordoned yourself stays cordoned. Geass also rewrites `/etc/rancher/k3s/config.yaml` on the worker.
- A worker that has no `GeassNode` has no SSH key that Geass can use. Geass does not update it. Update K3s on that worker as the [K3s documentation](https://docs.k3s.io/upgrades/manual) says, with the release of the control plane.
- The installer applies the operator manifest again on each update. This resets the memory limits of the operator. See [Cluster size](/docs/operations/sizing/).
- Geass creates the `letsencrypt-prod` issuer only when `spec.addons.certManager.acmeEmail` is set. A cluster installed before that field existed does not have it.
- The release that added [alerts](/docs/guides/alerts/) starts Alertmanager and lets Prometheus load the Geass rules. Prometheus restarts one time. If the metrics of Geass were never collected before, `GeassOperatorDown` can fire one time.
- Geass does not update the engine of existing Longhorn volumes when Longhorn moves to a new version. Read the Longhorn upgrade notes for the version that a release uses.

The add-on versions in this release line:

| Add-on | Chart version |
| --- | --- |
| cert-manager | `v1.17.2` |
| kube-prometheus-stack | `69.8.2` |
| Longhorn | `1.12.1` |
| CloudNativePG (with the first PostgreSQL database) | `0.23.2` |
| VictoriaLogs (`victoria-logs-single`) | `0.13.10` |
| Fluent Bit | `0.58.3` |
| KEDA (`geass-keda`), with the sleep-when-idle add-on | `2.21.0` |
| KEDA HTTP add-on (`geass-keda-http`), with the sleep-when-idle add-on | `0.16.0` |

### Update K3s on the other control plane servers

On a cluster with three control plane servers, run this command on each of the other servers, one at a time. Use the release that you gave the first server. Wait until each server is ready before you start the next:

```bash
curl -sfL https://get.k3s.io | sudo env INSTALL_K3S_VERSION=<release> INSTALL_K3S_EXEC=server sh -
```

### Restore the snapshot

Run these commands on the control plane server:

1. Find the snapshot. Its name starts with `geass-pre-upgrade`:

   ```bash
   sudo k3s etcd-snapshot ls
   ```

2. Stop K3s, restore the snapshot, and start K3s again:

   ```bash
   sudo systemctl stop k3s
   sudo k3s server --cluster-reset --cluster-reset-restore-path=/var/lib/rancher/k3s/server/db/snapshots/<snapshot name>
   sudo systemctl start k3s
   ```

The restore ends with `restart without --cluster-reset flag now`. That message is normal.

If you also updated K3s and no worker has moved yet, you can install the old K3s release before you restore:

```bash
curl -sfL https://get.k3s.io | sudo INSTALL_K3S_VERSION=<the old version> INSTALL_K3S_SKIP_START=true sh -s - server --cluster-init
```

Do not do that after workers have moved, because a worker must not run a newer K3s than the control plane. Keep the new K3s, and restore only the data.

To restore on a different machine, you need two things that the snapshot does not hold:

- The server token from `/var/lib/rancher/k3s/server/token` on the old machine. Without it, the restore fails with `bootstrap data already found and encrypted with different token`. Keep a copy outside the cluster.
- The line `secrets-encryption: true` in `/etc/rancher/k3s/config.yaml` on the new machine. Without it, reads of secret variables fail with `identity transformer tried to read encrypted data`.

### Update from source

You can build a release from source and apply it yourself. This path skips the checks, the snapshot, and the wait of the installer. Build on a machine with the same architecture as the control plane server, `amd64` or `arm64`:

```bash
git clone https://github.com/degoke/geass.git && cd geass
git checkout v0.2.0
make docker-build IMG=geass-operator:v0.2.0 GEASS_VERSION=v0.2.0
docker save -o geass-operator.tar geass-operator:v0.2.0
```

Then copy `geass-operator.tar` to the control plane server, and run these commands there:

```bash
sudo k3s ctr images import geass-operator.tar
sudo k3s etcd-snapshot save --name before-v0.2.0
make deploy IMG=geass-operator:v0.2.0      # with KUBECONFIG set to the cluster
sudo k3s kubectl -n geass-system rollout status deployment/geass-controller-manager
```

Do not change only the image. A release can add fields and permissions, and `make deploy` applies them with the image.

## Related

- [Install Geass](/docs/getting-started/install/)
- [Uninstall Geass](/docs/operations/uninstall/)
- [High availability](/docs/operations/high-availability/)
- [Backups and restore](/docs/guides/backups-and-restore/)
- [Command line](/docs/guides/command-line/)
- [GeassCluster reference](/docs/reference/resources/geass-cluster/)
