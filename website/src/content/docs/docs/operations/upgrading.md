---
title: "Upgrade Geass"
description: "Geass has no in-place upgrade command today. What re-running the installer deletes, how to move to a new release, and what to check before and after."
---

Geass publishes each release as an installer binary. The installer builds a new cluster; it does not upgrade an existing one. This page explains how versions work, what your options are today and what each keeps or loses, and the checks to make around any update.

:::caution
Running a newer installer on a machine that already has K3s uninstalls K3s first and deletes the cluster's data. Do not use it as an upgrade on a cluster you care about. See [Option 2](#option-2-reinstall-in-place) below.
:::

## How versions work

- **Releases.** Pushing a tag that starts with `v` runs a workflow that builds `geass-linux-amd64` and `geass-linux-arm64`, then publishes them with `install.sh` as a GitHub release with generated release notes. Each installer carries the operator image (the dashboard is part of that image), the Geass custom resource definitions, the RBAC and the Deployment manifest of its release. The same release carries the `geass` command line, one `geass-cli-<os>-<arch>` file per system for Linux, macOS and Windows, and `install-cli.sh`.
- **`latest` and pinning.** `install.sh` downloads the newest release unless `GEASS_VERSION` names a tag. See [Install Geass](/docs/getting-started/install/#pin-a-version).
- **The cluster does not record its release.** `GeassCluster.spec.version` is always `v1`, release builds tag the operator image `geass-operator:v0.0.1` whatever the release number, and the dashboard shows no version. Write down the tag you installed. `geass version` prints the command line's own version, and its `api v1` line is the version of the API, not of the release.
- **The command line is updated on its own.** Nothing in a cluster update changes the `geass` program on your machines or in CI. It talks to the `/api/v1` API, whose resources carry the `v1alpha1` fields of the cluster's release, so after you update a cluster, update the command line to the same release: run `install-cli.sh` again, with `GEASS_VERSION` to pin a tag. It replaces the file. See [Command line](/docs/guides/command-line/#install).
- **K3s is separate.** The installer lets K3s's installer pick its `stable` release at install time, and nothing in Geass upgrades K3s afterwards. A new worker installs the oldest K3s version among the control plane nodes, because an agent must not be newer than its server. If you upgrade the control plane yourself, workers joined later get the new version and existing workers stay where they are until you upgrade them.
- **Add-ons are pinned by the operator.** The operator installs these Helm charts through K3s's Helm controller at fixed versions, and applies a changed pin on its next reconcile:

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

  A new Geass version that raises a pin makes the add-on upgrade itself, which restarts its pods. MinIO, MySQL, Redis and other images and charts are pinned the same way.

## Your options

| | New server | Reinstall in place | Operator from source |
| --- | --- | --- | --- |
| Projects, services, settings | You recreate them | Deleted | Kept |
| Secrets, dashboard users, API tokens (including those of the command line and agents), connections | You recreate them | Deleted | Kept |
| Database data | You dump and restore it | Deleted with the cluster | Kept |
| Data in S3 | Untouched | Untouched | Untouched |
| K3s version | Current `stable` | Current `stable` | Unchanged |
| Downtime | None until you switch DNS | The whole cluster | The operator restarts; running workloads keep running |
| Supported by the release process | Yes | Yes | No |

### Option 1: install the new release on a new server

This is the safest path because the old cluster keeps running until you decide to switch.

1. Install the release you want on a new server. See [Install Geass](/docs/getting-started/install/) and [Sign in to the dashboard](/docs/getting-started/sign-in/).
2. Recreate what the old cluster held: dashboard users, the Cloudflare and GitHub connectors, AWS and PlanetScale connections, projects, services, variables and secrets, domains, node regions, sites and tags (compliance tags included, and the project's required tags), API tokens, and the serverless add-on if you used it (`spec.addons.serverless.enabled`). Each service carries its own sleep-when-idle setting, and each function its schedules.
3. Sign the command line in to the new cluster with `geass login --url <new address>`, and put new tokens into CI secrets and agent configurations. Tokens do not move between clusters.
4. Move data with the databases' own tools, for example `pg_dump` and `pg_restore` over a port-forward, and copy bucket contents with an S3 client.
5. Point DNS at the new server when you have checked it, keep the old one for a while, then [uninstall it](/docs/operations/uninstall/).

:::caution
Geass has no export and import. Database backups in your S3 bucket survive, but the dashboard restores them from a backup record that lives in the old cluster. A new cluster has no such record, so it cannot list or restore the old cluster's backups. Dump your databases yourself before you retire a cluster.
:::

### Option 2: reinstall in place

Run the installer on the control plane. Its third step, `remove existing installation`, runs K3s's uninstall script, which deletes the cluster's database and local volumes. Then it installs a fresh cluster. Use this only when the data is disposable, or when you have dumped what you need. Details and what survives are in [Install Geass](/docs/getting-started/install/#run-the-installer-again). Workers joined to the old cluster must be joined again. Every API token is deleted with the cluster, so the command line, CI jobs and agents get `invalid or expired API token` (exit code 4) until you sign in again with `geass login`.

### Option 3: update the operator from source

The operator, the dashboard and the manifests are one Deployment and a set of Kubernetes objects. You can build a release from source, put the image on the control plane and apply the manifests over the running cluster. Everything in the cluster stays, API tokens included, so command-line logins keep working. This is an advanced path that the release process does not cover: it does not change K3s and it applies whatever the release's code does on its first reconcile.

You need Docker, Go and `make` on a build machine, a `kubectl` that points at the cluster, and the image present in the control plane's containerd (the operator Deployment is required to run there). The image is built for the CPU architecture of the build machine, so build on one that matches the control plane, `amd64` or `arm64`:

```bash
git clone https://github.com/degoke/geass.git
cd geass
git checkout v0.2.0
make docker-build IMG=geass-operator:v0.2.0
docker save -o geass-operator.tar geass-operator:v0.2.0
```

Copy `geass-operator.tar` to the control plane and import it:

```bash
sudo k3s ctr images import geass-operator.tar
```

Then, with `KUBECONFIG` pointing at the cluster, apply the CRDs, RBAC and Deployment with the new image. Do not swap only the image: a release can add custom resource fields and operator permissions, and `make deploy` applies them with it:

```bash
make deploy IMG=geass-operator:v0.2.0
sudo k3s kubectl -n geass-system rollout status deployment/geass-controller-manager
```

Replace `v0.2.0` with the tag you are updating to. `make deploy` regenerates the manifests (it downloads `controller-gen` and `kustomize` with Go on first use), and changes `config/manager/kustomization.yaml` in your checkout to name the image. It does not touch K3s, the add-ons you already run (the operator changes them itself if the new release raises a pin) or your data.

## Before you update

1. **Read the release notes** on the GitHub release.
2. **Record what you run.** The release tag you installed, and:

   ```bash
   sudo k3s kubectl -n geass-system get deploy geass-controller-manager \
     -o jsonpath='{.spec.template.spec.containers[0].image}{"\n"}'
   sudo k3s kubectl -n kube-system get helmcharts \
     -o custom-columns=NAME:.metadata.name,VERSION:.spec.version
   sudo k3s kubectl -n geass-system get geasscluster
   ```

3. **Check network isolation.** Each project is private by default: only its own services, Geass itself and visitors who arrive through your domains can reach it. Projects that existed before isolation was added become isolated when Geass is updated. List every project that calls another one (a front end calling an internal API, say) before you update, and be ready to allow it under the receiving project's **Settings → Network**. See [Network isolation](/docs/guides/network-isolation/). Two rules hold whatever you choose: services cannot read the cloud provider's credentials for their machine, and no project can read another's metrics or logs.
4. **Check backups.** In each project's **Settings → Backups**, confirm that backup storage is set. Then open each database's **Backups** tab and confirm that its state is **Protected** and that a recent restore point exists. A database that is not protected shows **Not backed up** or **Backups at risk** on its canvas card. See [Backups and restore](/docs/guides/backups-and-restore/).
5. **Keep a copy of the sign-in Secret** (`geass-dashboard-auth`) and note which connections and connectors you use, so that you can recreate them if the update is a reinstall.
6. **Check capacity.** Add-on upgrades restart pods. Look at the **Usage** page for room to spare.
7. **If you use sleep when idle, check the add-on.** `sudo k3s kubectl -n kube-system get helmcharts geass-keda geass-keda-http` shows both, and `sudo k3s kubectl get crd httpscaledobjects.http.keda.sh` exists. A release that raises these chart pins upgrades the add-on, which restarts its pods. See [Sleep when idle](/docs/guides/scale-to-zero/).
8. **If you use compliance tags, plan the order.** In a new cluster, mark the compliance tags on the nodes first, then require them on the project, and only then recreate its databases: a database is placed once, when it is first provisioned, and the dashboard refuses to add a tag that a provisioned database does not run on. The admission policy `geass-required-node-tags` is a cluster object, so a new cluster makes it again the first time a project requires tags. See [Compliance node tags](/docs/guides/compliance-tags/).
9. **Check how you get certificates.** The operator creates the `letsencrypt-prod` issuer only when the cluster object has `spec.addons.certManager.acmeEmail`, and a cluster installed before that field existed does not. An issuer of that name that you created yourself is never overwritten. See [HTTPS certificates](/docs/getting-started/install/#https-certificates).

## After you update

1. The operator is running on the new image: `sudo k3s kubectl -n geass-system get pods`, and the logs have no repeating errors: `sudo k3s kubectl -n geass-system logs deploy/geass-controller-manager -c manager`.
2. The cluster is ready: `sudo k3s kubectl -n geass-system get geasscluster` shows `READY` `True`. It can report not ready for a while if an add-on is upgrading. Look at `sudo k3s kubectl -n kube-system get helmcharts` and compare the versions with what you recorded.
3. Reload the dashboard, which is part of the new image, and sign in.
4. Open each project: services and databases should be **Ready**, and the top bar **Up to date**.
5. Check that services that call each other still can, and that each project has its isolation policies: `sudo k3s kubectl get networkpolicy -A | grep geass`.
6. The command line works: `geass whoami` answers and `geass status -p <project> -e production` lists the services. After an update from source, existing tokens still work. After a new server or a reinstall, sign in again with `geass login`.
7. With the metrics add-on on, the operator keeps its own monitor and alert rules up to date: `sudo k3s kubectl -n monitoring get servicemonitor,prometheusrule geass-operator`. The **Usage** page charts them under **Geass health**.
8. If you want Geass to manage certificates on a cluster that was installed without `GEASS_ACME_EMAIL`, set the address now: `sudo k3s kubectl -n geass-system patch geasscluster default --type merge -p '{"spec":{"addons":{"certManager":{"acmeEmail":"you@yourdomain.com"}}}}'`. Geass creates the `letsencrypt-prod` issuer once cert-manager is ready.
9. Deploy a small change to one service to confirm the whole path works.

## Troubleshooting

**The operator pod is in `ImagePullBackOff` after an update from source.** The image is not in the control plane's containerd under that name and tag. Import it there, not on a worker.

**Services that used to reach each other time out after an update.** Network isolation applies to projects that predate it. Allow the caller under the receiving project's **Settings → Network**.

**The cluster stays not ready after an update.** An add-on is upgrading or failing. `sudo k3s kubectl -n kube-system get helmcharts` and the pods in `monitoring`, `longhorn-system`, `cert-manager` and `geass-logging` show which.

**`geass` says `invalid or expired API token`, or `not signed in`, after an update.** Every API token lives in the cluster, so a new server or a reinstall deletes them. Run `geass login --url <address>` again, and replace the tokens in CI secrets and agent configurations. If the address changed, `geass config get-contexts` lists the logins the command line still has.

**A reinstall removed something you needed.** Nothing in Geass restores it. The cluster's database was deleted with K3s's data directory. Recreate it from your records, and restore data from your own dumps.

## Related

- [Install Geass](/docs/getting-started/install/)
- [Uninstall Geass](/docs/operations/uninstall/)
- [Command line](/docs/guides/command-line/)
- [Backups and restore](/docs/guides/backups-and-restore/)
- [Network isolation](/docs/guides/network-isolation/)
- [High availability](/docs/operations/high-availability/)
