---
title: "Install Geass"
description: "Install Geass on a Linux server with one command: what the installer does, the variables it reads, where it logs, how to verify it, and what it leaves undone."
---

The installer turns one Linux server into a Geass control plane. It checks the machine, installs K3s, loads the Geass operator into it and creates the cluster object that starts everything else. It takes a few minutes and needs no files other than the installer itself. When it ends, the dashboard exists but you cannot reach it or sign in yet: read [What the installer does not do](#what-the-installer-does-not-do), then [Sign in to the dashboard](/docs/getting-started/sign-in/).

## Before you start

- A server that meets the [requirements](/docs/getting-started/requirements/): Linux on `amd64` or `arm64`, 2 GiB of memory, 10 GiB free on `/`, root access, outbound HTTPS.
- A machine that has no K3s on it. On a machine that already runs Geass the installer updates it instead, keeping its data, and on a machine with some other K3s it stops and tells you why: it deletes a cluster only when you pass `--reinstall`. See [Run the installer again](#run-the-installer-again).
- A real email address for Let's Encrypt, if you want HTTPS certificates. It is optional for the install itself. See [HTTPS certificates](#https-certificates).

## Install

SSH into the server and run:

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo sh
```

`install.sh` is a short script. It works out your architecture from `uname -m` (`x86_64` and `amd64` become `amd64`, `aarch64` and `arm64` become `arm64`), downloads `geass-linux-<arch>` and the release's `SHA256SUMS` file from the GitHub release into temporary files, checks the installer against `SHA256SUMS`, makes it executable and runs it, then deletes it when the installer ends, whether it succeeded or not. A download that does not match its checksum, or that `SHA256SUMS` does not list, is not run: the script says so and exits with status 1, and nothing on the machine has changed. If the download fails the script prints curl's error, the URL and the usual causes, and exits with status 1. The downloaded installer carries the operator image and manifests inside it, so nothing else is fetched from Geass.

Every command printed by the installer is also written to a log file. The first lines of output name it:

```text
Downloading Geass from https://github.com/degoke/geass/releases/latest/download/geass-linux-amd64
Checked geass-linux-amd64 against SHA256SUMS
Installation log: /home/ubuntu/geass-install-20261012-091500.log
→ system checks
✓ system checks
→ remove existing installation
...
```

Each step prints `→ <name>` when it starts and `✓ <name>` when it finishes. If a step fails, the installer prints `✗ <name> failed: <reason>` and `Detailed log: <path>` and exits with status 1.

## Pin a version

`latest` resolves to the newest release on GitHub. To install a specific release, use that release's `install.sh` and set `GEASS_VERSION` to the same tag. `sudo` clears most of the environment, so pass the variable through `env`:

```bash
curl -sfL https://github.com/degoke/geass/releases/download/v0.0.1/install.sh | sudo env GEASS_VERSION=v0.0.1 sh
```

Replace `v0.0.1` with a tag from the project's Releases page. The cluster records the release that its operator runs: `sudo k3s kubectl -n geass-system get geasscluster` shows it in the `RELEASE` column, and the update refuses to go to an older one. See [Upgrade Geass](/docs/operations/upgrading/).

To build the cluster on a specific K3s release instead of the current stable one, pass `GEASS_K3S_VERSION` (the full name, such as `v1.36.5+k3s1`):

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo env GEASS_K3S_VERSION=v1.36.5+k3s1 sh
```

### Check what you download

Every release has a `SHA256SUMS` file listing the installer, the command line and the two install scripts, which the scripts check for you. Each file also has signed build provenance, which shows which workflow run and commit built it. To check a file yourself, from the release page or with the GitHub CLI:

```bash
gh release download v0.0.1 --repo degoke/geass --pattern 'geass-linux-amd64' --pattern SHA256SUMS
sha256sum --check --ignore-missing SHA256SUMS
gh attestation verify geass-linux-amd64 --repo degoke/geass
```

See [Releases and versions](/docs/operations/releases/) for what a release contains and how versions are numbered.

## HTTPS certificates

Geass gets the certificates for the dashboard and for your services from [Let's Encrypt](https://letsencrypt.org/), through cert-manager. Let's Encrypt registers an account for the cluster and wants a contact address for it, which only you can give. Pass one to the installer as `GEASS_ACME_EMAIL`:

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo env GEASS_ACME_EMAIL=you@yourdomain.com sh
```

Use a real address: Let's Encrypt refuses placeholder domains such as `example.com`. The first installer step checks that the value looks like an email address (`name@host.tld`, no spaces, at most 254 characters) and stops before it changes anything if it does not.

With an address set, the installer writes it into the cluster object as `spec.addons.certManager.acmeEmail`. Once cert-manager is ready, the operator creates a cert-manager `ClusterIssuer` named `letsencrypt-prod`: ACME against Let's Encrypt's production directory, over HTTP-01, solved through the Traefik ingress class. Every Ingress that Geass creates for a hostname with **HTTPS** on, and the dashboard's own Ingress, asks for its certificate from that issuer. Two ports matter: Let's Encrypt must reach your server on 80/tcp to validate a hostname, and visitors reach it on 443/tcp. The installer opens both in an active host firewall on the control plane, but it cannot open them in your provider's firewall or security group. See [Ports and firewall](/docs/getting-started/requirements/#ports-and-firewall).

Without an address the cluster still installs and runs, but no issuer exists and **no HTTPS certificate is issued**. The installer prints a warning that starts `warning: GEASS_ACME_EMAIL is not set`. The cluster object then has a `ClusterIssuerReady` condition that says why. While the dashboard's own HTTPS is unreachable, the same message is also appended to the `PendingHTTPS` message of the `DashboardDomainReady` condition on the platform configuration. The dashboard does not show either condition, so read it with `kubectl`:

```bash
sudo k3s kubectl -n geass-system get geasscluster default \
  -o jsonpath='{.status.conditions[?(@.type=="ClusterIssuerReady")].reason}{": "}{.status.conditions[?(@.type=="ClusterIssuerReady")].message}{"\n"}'
```

To set or change the address on a running cluster, including one installed without it, edit the cluster object. The operator creates or updates the issuer on its next pass, which starts when you save:

```bash
sudo k3s kubectl -n geass-system patch geasscluster default --type merge \
  -p '{"spec":{"addons":{"certManager":{"acmeEmail":"you@yourdomain.com"}}}}'
```

`ClusterIssuerReady` is informational. A cluster without an issuer is still `Ready` and runs workloads. Its `reason` tells you what to do:

| Reason | Meaning |
| --- | --- |
| `IssuerReady` | The issuer exists and cert-manager has registered the ACME account. Certificates can be issued |
| `ACMEEmailMissing` | No address is set, so no issuer was created. Set `acmeEmail` |
| `WaitingForCertManager` | cert-manager is still installing. Geass checks again every 30 seconds |
| `IssuerRegistering` | The issuer exists and cert-manager has not yet reported on it |
| `IssuerNotReady` | cert-manager reports the issuer as not ready. The message carries its reason, most often Let's Encrypt refusing the email address |
| `ExternallyManaged` | An issuer named `letsencrypt-prod` exists that Geass did not create, and cert-manager reports it ready. Geass leaves it exactly as it is |
| `ManagedByAnotherCluster` | Another `GeassCluster` owns the issuer, which is cluster-wide. It is left unchanged |
| `IssuerApplyFailed`, `IssuerAccessDenied` | Geass could not write the issuer and tries again every 30 seconds. With `IssuerApplyFailed` the operator log says why. `IssuerAccessDenied` means the operator's role lacks access to `clusterissuers.cert-manager.io` |

Geass deliberately leaves two things alone. It never overwrites a `letsencrypt-prod` issuer that you created yourself, for example a DNS-01 wildcard issuer. And removing the address later does not delete the issuer Geass made, because certificates issued through it still renew through it. If you run your own cert-manager, set `spec.addons.certManager.enabled` to `false`: Geass then manages no issuer, and it also removes the cert-manager it had installed.

A dashboard published through a Cloudflare Tunnel gets its certificate from Cloudflare, not from cert-manager, and the tunnel reaches your services over HTTP inside the cluster, so the issuer is not involved there.

## What the installer does

The installer runs these steps in order. The names are the ones it prints.

| # | Step | What it does |
| --- | --- | --- |
| 1 | `system checks` | Requires Linux, `amd64` or `arm64`, a server with 2 GB of memory (at least 1700 MiB of `MemTotal`, which is what a machine sold as 2 GB reports) and 10 GiB free on `/`. It also checks `GEASS_ACME_EMAIL` and `GEASS_K3S_VERSION` when you set them, and with `GEASS_MULTI_SITE` it validates the public address and tests that WireGuard works |
| 2 | `remove existing installation` | Does nothing on a bare machine. Uninstalls K3s only when you passed `--reinstall`, after a confirmation and a last etcd snapshot. See [Run the installer again](#run-the-installer-again) |
| 3 | `configure firewall` | Opens 6443/tcp, 10250/tcp, the overlay port, and 80/tcp and 443/tcp for web traffic in the first active firewall it finds (`ufw`, `firewalld`, then `iptables`). With `iptables` it also saves the rules where the host restores them at boot, if it does. It runs after the old K3s is removed, so the rules it saves are the host's own and not the chains of a cluster being replaced. Prints a warning and continues when no firewall is active. See [Requirements](/docs/getting-started/requirements/#ports-and-firewall) |
| 4 | `install node prerequisites` | Installs and enables the iSCSI initiator that Longhorn needs. Failure is logged and does not stop the install |
| 5 | `install k3s (server)` | Writes `/etc/rancher/k3s/config.yaml`, downloads `https://get.k3s.io` and runs it with `INSTALL_K3S_EXEC="server --cluster-init"`: a K3s server with embedded etcd, using K3s's default release channel |
| 6 | `wait for k3s ready` | Waits up to 5 minutes for `/etc/rancher/k3s/k3s.yaml`, then runs `k3s kubectl get node` |
| 7 | `import operator image` | Writes the operator image, embedded in the installer, to a temporary file and loads it into K3s's containerd with `k3s ctr image import` |
| 8 | `deploy geass-operator` | Applies the embedded manifests with `k3s kubectl apply`: the `geass-system` namespace, the Geass custom resource definitions, RBAC, the operator Deployment and the dashboard Service |
| 9 | `wait for operator ready` | Waits up to 5 minutes for `deployment/geass-controller-manager` to roll out |
| 10 | `store worker join token` | Reads K3s's node token and saves it as the Secret `geass-token` in `geass-system`, where the operator reads it when it joins workers. The token goes in on standard input, so the installer log does not contain it |
| 11 | `apply GeassCluster CR` | Waits up to 60 seconds for the `geassclusters` definition, then applies the cluster object shown below, with this server's own address as `serverURL`. Without `GEASS_ACME_EMAIL` it prints the warning described under [HTTPS certificates](#https-certificates) |

The `/etc/rancher/k3s/config.yaml` that step 5 writes (mode 0600) sets the kubelet's container log rotation to 50Mi per file and 5 files, so the dashboard has log history to read, and turns on K3s secrets encryption, so every Secret in the cluster is encrypted in K3s's datastore and in its etcd snapshots (see [Security](/docs/operations/security/#secrets)):

```yaml title="/etc/rancher/k3s/config.yaml"
kubelet-arg:
- container-log-max-size=50Mi
- container-log-max-files=5
secrets-encryption: true
```

The installer's update mode never rewrites `/etc/rancher/k3s/config.yaml`, so it does not switch encryption on in a cluster that was built without it. To turn it on in such a cluster, follow K3s's own documentation on secrets encryption.

The cluster object from step 11:

```yaml title="GeassCluster default"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassCluster
metadata:
  name: default
  namespace: geass-system
spec:
  version: v1
  serverURL: https://10.0.0.5:6443
  tokenSecretRef:
    name: geass-token
    namespace: geass-system
  addons:
    longhorn:
      enabled: true
    logging:
      enabled: true
```

With `GEASS_ACME_EMAIL` set, the object also has this under `addons`:

```yaml
    certManager:
      acmeEmail: "you@yourdomain.com"
```

`spec.version` is a label that Geass does not use to pick a K3s version. The release the cluster runs is `status.release`, which the operator writes, and the K3s release the workers are to run is `spec.k3s.version`, which is empty until you or an update sets it. `serverURL` is the address new workers are told to join. The installer fills it with this server's own address on its default route (the `10.0.0.5` above stands for yours), which is a private address on most cloud servers, or the public address with `GEASS_MULTI_SITE`. If the installer cannot find an address it stores `https://127.0.0.1:6443` and prints a warning, and the operator then joins workers on the control plane node's own address. See [Add servers](/docs/getting-started/add-servers/#set-the-address-workers-join). The cluster object does not turn on cert-manager or monitoring because both default to on, so the operator also installs them. With Longhorn and log collection, that is four add-ons, installed through K3s's Helm controller after the installer has exited. A fifth, the serverless add-on that lets services sleep when idle, is off: the installer never sets `spec.addons.serverless.enabled`. See [How Geass works](/docs/overview/how-it-works/) and [Sleep when idle](/docs/guides/scale-to-zero/).

## Environment variables

| Variable | Read by | Meaning | Default |
| --- | --- | --- | --- |
| `GEASS_VERSION` | `install.sh` | Release tag whose installer binary is downloaded | `latest` |
| `GEASS_REPO` | `install.sh` | GitHub `owner/name` to download releases from | `degoke/geass` |
| `GEASS_DOWNLOAD_BASE` | `install.sh`, `install-cli.sh` | The address the release files are downloaded from, instead of the GitHub release: a mirror, or a directory as `file:///path`. It has to hold the installer (`geass-linux-<arch>`) or the command line (`geass-cli-<os>-<arch>`) and `SHA256SUMS`, and the download is still checked against it | the release on GitHub |
| `GEASS_K3S_VERSION` | installer | K3s release to build a new cluster on, such as `v1.36.5+k3s1`. A channel name is not accepted. On a machine that already runs Geass it is ignored, with a warning, so that a value left over from the install does not upgrade K3s: to upgrade the control plane, pass the `--k3s-version` option (see [Upgrade Geass](/docs/operations/upgrading/)) | the current `stable` release |
| `GEASS_JOIN_SERVER` | installer | The address of an existing cluster's server. Makes this machine an additional control plane server of it instead of building a cluster; it also needs the cluster's K3s release in `GEASS_K3S_VERSION`. See [High availability](/docs/operations/high-availability/#run-three-control-plane-servers) | none |
| `GEASS_JOIN_TOKEN` | installer | The cluster's server token, which a join needs | none |
| `GEASS_REINSTALL` | installer | `1`, `true`, `yes` or `on`. The same as `--reinstall`: delete the cluster on this machine and build a new one. See [Run the installer again](#run-the-installer-again) | off |
| `GEASS_YES` | installer | `1`, `true`, `yes` or `on`. The same as `--yes`: do not ask for confirmation of a reinstall | off |
| `GEASS_INSTALL_LOG` | installer | Path of the log file. Missing directories are created | `geass-install-<UTC time>.log` in the current directory |
| `GEASS_INSTALL_TRACE` | installer | `1`, `true`, `yes` or `on` runs the K3s install script with `sh -x`, so the log shows every command in it | off |
| `GEASS_ACME_EMAIL` | installer | Contact address for the cluster's Let's Encrypt account. Checked at the first step, then stored as `spec.addons.certManager.acmeEmail`. Without it Geass creates no certificate issuer. See [HTTPS certificates](#https-certificates) | none |
| `GEASS_MULTI_SITE` | installer | `1`, `true`, `yes` or `on`. Builds a cluster whose nodes are at different providers. Needs `GEASS_NODE_EXTERNAL_IP` | off |
| `GEASS_NODE_EXTERNAL_IP` | installer | This server's public IP address. Required with `GEASS_MULTI_SITE`, and must parse as an IP address | none |

The installer passes its whole environment on to the K3s install script, so K3s's own `INSTALL_K3S_*` variables take effect when you set them. Geass sets `INSTALL_K3S_EXEC` itself and overrides yours, and sets `INSTALL_K3S_VERSION` when you pass `GEASS_K3S_VERSION`. K3s selects its release channel when you set nothing: the `stable` channel at the moment you install.

The installer also takes options, which go after `-s --` when you pipe it: `--k3s-version`, `--upgrade`, `--force`, `--skip-snapshot`, `--allow-untested-k3s`, `--reinstall`, `--yes`, `--join-server`, `--etcd-snapshots`, `--version` and `--help`. The update options are described in [Upgrade Geass](/docs/operations/upgrading/#options).

### Copies of the cluster's state

On a terminal the installer asks one question before it installs K3s: whether to keep copies of the cluster's own state in an S3 bucket of your own, so that it can be rebuilt if this machine's disk is lost. The default is **no**, and it is no whenever there is no terminal to ask on, as in a script. To say yes ahead, pass `--etcd-snapshots`, or name the bucket with `GEASS_ETCD_SNAPSHOTS_BUCKET` and the variables that go with it. If you say yes it shows you the cluster's server token on the terminal, once, and asks you to store it somewhere that is not this machine. You can also turn it on later. See [Disaster recovery](/docs/operations/disaster-recovery/).

:::note
By default `sudo` drops variables you set before it. Write `sudo env NAME=value sh`, not `NAME=value sudo sh`.
:::

## Where the installer logs

The log file is `geass-install-YYYYMMDD-HHMMSS.log` (UTC) in the directory you ran the installer from, unless `GEASS_INSTALL_LOG` names another path. When that location is not writable, the installer uses your temporary directory (usually `/tmp`) and prints `Primary log path was not writable, using fallback log file`. When neither works it prints a warning and installs without a log.

Each line looks like `2026-10-12T09:15:02Z [install k3s (server)] [stdout] ...`: a time, the step, the stream (`info`, `warning`, `stdout`, `stderr` or `combined`) and the text. The file records each command line the installer runs and its output.

## Confirm it worked

```bash
sudo k3s kubectl get nodes
sudo k3s kubectl -n geass-system get pods
sudo k3s kubectl -n geass-system get geasscluster
```

You should see the node `Ready`, a `geass-controller-manager` pod `Running`, and a cluster named `default`. The cluster's `READY` column turns `True` once the add-ons are up, which takes longer than the installer. Watch them:

```bash
sudo k3s kubectl -n geass-system get geasscluster default \
  -o jsonpath='{range .status.conditions[*]}{.type}={.status}{"\n"}{end}'
sudo k3s kubectl -n kube-system get helmcharts
sudo k3s kubectl get pods -A
```

Once cert-manager is up, the conditions also include `ClusterIssuerReady`. It does not affect `Ready`. See [HTTPS certificates](#https-certificates) for what it reports, and check the issuer itself with `sudo k3s kubectl get clusterissuer letsencrypt-prod`.

The add-on charts are named `geass-cert-manager`, `geass-kube-prometheus-stack`, `geass-longhorn`, `geass-victoria-logs` and `geass-fluent-bit`. Once you turn on the serverless add-on there are two more, `geass-keda` and `geass-keda-http`. Wait for the cluster to report `Ready=True` before you create your first project. Until then a project reports `Referenced GeassCluster is not ready`, and a resource deployed in that window reports that its project or cluster is not ready. After repeated failures it stops retrying and shows **Failed** until you use **Retry**.

Logs of the operator and the dashboard are the same stream:

```bash
sudo k3s kubectl -n geass-system logs deploy/geass-controller-manager -c manager
```

## Install the command line (optional)

The installer builds the cluster. The `geass` command line is a separate program that you run on your own machine, and it needs a dashboard you can already sign in to. So install it after [Sign in to the dashboard](/docs/getting-started/sign-in/), on your laptop or in CI, not on the server:

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install-cli.sh | sh
geass login --url https://geass.example.com
```

`install-cli.sh` is a release file next to `install.sh`. It downloads the `geass-cli-<os>-<arch>` build for Linux or macOS on `amd64` or `arm64` and installs it as `geass` in `/usr/local/bin`, or in `~/.local/bin` when `/usr/local/bin` is not writable. It checks the download against the release's `SHA256SUMS`, as `install.sh` does, and installs nothing that does not match. It reads `GEASS_VERSION`, `GEASS_INSTALL_DIR`, `GEASS_REPO` and `GEASS_DOWNLOAD_BASE`. Do not confuse it with the installer binary `geass-linux-<arch>` that `install.sh` runs on the server: that one builds a cluster, and it is not the command line.

`geass login` prints a code and opens the dashboard's `/device` page, where an admin approves it. See [Sign in to the dashboard](/docs/getting-started/sign-in/#sign-in-from-the-command-line). The command line and the MCP server for AI agents (`geass mcp`) are described in [Command line](/docs/guides/command-line/) and [MCP server for AI agents](/docs/guides/mcp-server/).

## What the installer does not do

- **It does not set up dashboard sign-in.** No username or password exists after the install, and the installer prints none. You create them. See [Sign in to the dashboard](/docs/getting-started/sign-in/).
- **It does not publish the dashboard.** The dashboard is a `ClusterIP` Service, reachable from inside the cluster only. There is no ingress, domain or certificate until you configure a domain from the dashboard.
- **It does not issue certificates without your email address.** The installer enables cert-manager, and the operator creates the `letsencrypt-prod` issuer that Geass's Ingress objects ask for, but only when it has a contact address (`GEASS_ACME_EMAIL`). See [HTTPS certificates](#https-certificates) and [Domains and TLS](/docs/guides/domains-and-tls/).
- **It does not touch your provider's firewall or security group.** It opens ports only in a host firewall that is active on the control plane (`ufw`, `firewalld` or `iptables`). Ports 80 and 443 must also be open in the provider's firewall, or visitors cannot reach your services and Let's Encrypt cannot validate your hostnames. Nothing is opened on workers.
- **It does not add workers.** See [Add servers](/docs/getting-started/add-servers/).
- **It does not turn on the serverless add-on.** KEDA and its HTTP add-on, which let a service sleep when idle, are installed only after you set `spec.addons.serverless.enabled` on the cluster. See [Sleep when idle](/docs/guides/scale-to-zero/#turn-on-the-add-on).
- **It does not install the command line** on your workstation. See [Install the command line](#install-the-command-line-optional).
- **It does not configure backups, Cloudflare, GitHub or any cloud account.**
- **It does not send telemetry.** The repository contains no analytics or telemetry code.

## Install for a multi-site cluster

By default the nodes of a cluster share a private network and K3s's VXLAN overlay carries the traffic. For nodes at different providers that reach each other over the internet, give the installer this server's public address:

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo env GEASS_MULTI_SITE=1 GEASS_NODE_EXTERNAL_IP=203.0.113.10 sh
```

The installer then:

- Checks that the address is valid and that the kernel can create a WireGuard interface.
- Configures K3s with the `wireguard-native` backend, `node-external-ip`, `flannel-external-ip` and a TLS SAN for the address, and opens 51820/udp instead of 8472/udp.
- Sets `spec.serverURL` of the cluster object to `https://203.0.113.10:6443` and `spec.network.multiSite` to `true`, so workers join on the public address.

The mode is chosen at install time: switching an existing cluster means restarting every node. Keep the control plane at one site.

:::caution
Multi-provider clusters are experimental. The project has run a four-node cluster across three availability zones of one provider over public addresses, and has not yet run one across two different providers. See [Multi-provider clusters](/docs/operations/multi-provider/) for the ports to open and the limits.
:::

## Run the installer again

The installer looks at the machine and does what it needs. It never deletes a cluster on its own account.

| The machine has | The installer |
| --- | --- |
| No K3s | Builds a new cluster |
| K3s with Geass in it | **Updates** it, keeping its data, after taking a snapshot. See [Upgrade Geass](/docs/operations/upgrading/) |
| K3s without Geass, a K3s whose API server does not answer, or a K3s agent (a worker) | Stops, and says why. Nothing is changed |
| Any of the above, and you passed `--reinstall` | Deletes the cluster and builds a new one |

Running the installer again for the same release is safe: it checks, replaces nothing that has not changed and restarts nothing.

### Reinstall

:::danger
`--reinstall` uninstalls K3s from the machine and builds a new cluster. Everything in the old cluster is deleted.
:::

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo sh -s -- --reinstall
```

It says what it will delete and asks you to type `reinstall` on your terminal. A script with no terminal passes `--yes`. Before it removes anything it takes a last etcd snapshot of the old cluster into `/var/backups/geass`, outside K3s's own directories (nothing restores it for you, and a cluster that is broken may not be able to be snapshotted; the reinstall then goes on).

Step 2, `remove existing installation`, treats K3s as installed when any of these exist: `/usr/local/bin/k3s-uninstall.sh`, `/usr/local/bin/k3s-agent-uninstall.sh`, `/usr/local/bin/k3s`, `/etc/systemd/system/k3s.service` or `/etc/systemd/system/k3s-agent.service`. It then runs the first uninstall script it finds. K3s's own uninstall script removes `/etc/rancher/k3s`, the K3s data directory (`/var/lib/rancher/k3s` by default) and `/var/lib/kubelet`. The data directory holds the cluster's embedded etcd database and K3s's local-path volumes, so after a reinstall:

- Every project, service, database definition, secret, dashboard user, API token and connection of the old cluster is gone, and so is data on local-path volumes.
- Workers joined to the old cluster keep running their agents but hold credentials of a cluster that no longer exists.
- Things outside K3s's directories are untouched: S3 buckets and backups, DNS records, Cloudflare tunnels, IAM users and your provider's resources. Longhorn's data directory (`/var/lib/longhorn/` by default) is also left on disk, with nothing that refers to it.

Use a reinstall to retry on a machine where an install failed part-way and nothing of value exists yet, or to rebuild a cluster you mean to replace. For updating a working cluster, read [Upgrade Geass](/docs/operations/upgrading/).

When K3s files exist but no uninstall script does, a reinstall stops with `existing k3s installation detected but no uninstall script found at /usr/local/bin/k3s-uninstall.sh or /usr/local/bin/k3s-agent-uninstall.sh`. Remove the leftover K3s files yourself and run it again.

## What happens under the hood

On disk the installer and K3s leave:

| Path | What |
| --- | --- |
| `/usr/local/bin/k3s`, and `kubectl`, `crictl`, `ctr` symlinks | K3s |
| `/etc/rancher/k3s/config.yaml` | K3s server settings written by the installer |
| `/etc/rancher/k3s/k3s.yaml` | Admin kubeconfig. Root only |
| `/var/lib/rancher/k3s/` | Cluster data, including `server/node-token` |
| `/usr/local/bin/k3s-uninstall.sh` | K3s's uninstall script |
| `geass-install-*.log` | The installer log |
| A temporary file from `mktemp` | The downloaded installer, deleted by `install.sh` when the installer ends. Earlier versions of the script replaced themselves with the installer, which left the file behind until the temporary directory was cleared |

K3s runs as the `k3s` systemd service and starts at boot. In the cluster, the installer creates:

| Object | Namespace | Notes |
| --- | --- | --- |
| Namespace `geass-system` | | Geass's own objects and secrets live here |
| 16 `geass.geass.dev` definitions and `helmcharts.helm.cattle.io` | | `kubectl get crd` |
| Deployment `geass-controller-manager` | `geass-system` | One replica, container `manager`, required to run on the control plane, and replaced by stopping the old pod before starting the new one. Image `geass-operator:<release tag>` in release builds. Memory limit 1Gi, with `GOMEMLIMIT=800MiB`. Its pod template carries the annotations `geass.dev/release` and `geass.dev/operator-image-sha256` |
| Service `geass-geass-dashboard` | `geass-system` | `ClusterIP`, port 8085. The name has `geass-` twice because the manifest build prefixes every name |
| Service `geass-controller-manager-metrics-service` | `geass-system` | Operator metrics, port 8443 |
| Secret `geass-token` | `geass-system` | K3s join token |
| ConfigMap `geass-schema` | `geass-system` | Made by the operator when it first starts: the release that last started and the data schema level, which the operator checks when it starts and refuses to run over data that is newer than it knows of. See [Upgrade Geass](/docs/operations/upgrading/#how-an-update-stays-safe) |
| ClusterRoleBinding `geass-metrics-reader-prometheus` | cluster-wide | Lets the monitoring add-on's Prometheus read the operator's metrics. It names a service account that exists only once the monitoring add-on is installed |
| GeassCluster `default` | `geass-system` | Shown above |

## Troubleshooting

**`Could not download Geass from <url>`.** The download failed, and the line above it is curl's own error, such as `curl: (22) The requested URL returned error: 404` for a tag or file that does not exist. The usual causes are a `GEASS_VERSION` that is not a release, a release with no file for your architecture, or no outbound access to `github.com`. Open the URL in a browser, or run `curl -fI <url>`. An unsupported architecture is reported before the download, as `unsupported architecture`.

**`✗ system checks failed: insufficient memory: ... MB (a server with 2 GB is needed, which the kernel reports as at least 1700 MB)`, or `insufficient disk space on /`.** See [Requirements](/docs/getting-started/requirements/#memory-and-disk).

**`✗ system checks failed: GEASS_ACME_EMAIL "..." is not an email address`.** The value must look like `name@host.tld`, with no spaces and at most 254 characters. This step runs first, so nothing on the machine has changed. Fix the variable and run the installer again.

**`warning: GEASS_ACME_EMAIL is not set, so no certificate issuer is created ...`.** Informational. The install continues, and no HTTPS certificate is issued until you set the address. See [HTTPS certificates](#https-certificates).

**`warning: No active host firewall detected; skipping port configuration`.** Informational. K3s manages its own packet rules, so the cluster starts. If you add a firewall later, open the ports it lists, which include 80/tcp and 443/tcp.

**`warning: The iptables rules that open the ports are not saved ...`.** The host filters with `iptables` but does not restore rules at boot (there is no `/etc/iptables/rules.v4` or `/etc/sysconfig/iptables`), so the ports the installer opened close again after a reboot. Install `iptables-persistent` (Debian, Ubuntu) or `iptables-services` (Red Hat family) and save the rules. See [Requirements](/docs/getting-started/requirements/#ports-and-firewall).

**`warning: Could not work out this machine's address ...`.** The installer found no usable address on the default route, so `spec.serverURL` holds `https://127.0.0.1:6443`. The operator joins workers on the control plane node's own address anyway. To choose another address, patch `spec.serverURL`: see [Add servers](/docs/getting-started/add-servers/#set-the-address-workers-join).

**`✗ configure firewall failed: ufw allow 6443/tcp: ...`.** The firewall command failed. Run it by hand to see why, or disable the firewall for the install and open the ports afterwards.

**`✗ install k3s (server) failed: download k3s installer: ...`.** The server cannot reach `get.k3s.io`. Check DNS and outbound HTTPS.

**`k3s did not become ready within 5 minutes`.** K3s was installed but did not start. Check `sudo systemctl status k3s` and `sudo journalctl -u k3s`. Low memory is a common cause.

**`the operator did not become ready within 5m0s`**, or **`the operator pod is not starting: ...`** (a pod that is crash looping or cannot pull its image is reported at once, without waiting the five minutes). The installer prints `waiting...` with the rollout status every few seconds while it waits. Look at the pod: `sudo k3s kubectl -n geass-system describe pod -l control-plane=controller-manager`, then the logs command above. If you built the installer yourself and see `embedded operator image is empty`, rebuild it with `make build-geass`.

**`✗ apply GeassCluster CR failed: wait for GeassCluster CRD: ...`.** The definition was not established within 60 seconds. Check the operator pod and the API server, then run the installer again on this fresh machine.

**`a multi-site cluster needs this node's public address: set GEASS_NODE_EXTERNAL_IP`, `GEASS_NODE_EXTERNAL_IP "..." is not an IP address`, or `this machine's kernel cannot create a WireGuard interface`.** Fix the variable, or install the `wireguard` package on a kernel older than 5.6.

**The installer finished and the cluster stays `READY=False`.** The add-ons are not up. Check `sudo k3s kubectl -n kube-system get helmcharts` and the pods in `cert-manager`, `monitoring` and `longhorn-system`. Longhorn needs the iSCSI initiator: `command -v iscsiadm`, and the install log lines for `install node prerequisites`.

**HTTPS is on for a hostname and the browser warns about the certificate.** Traefik is serving its own certificate because none was issued. Check the issuer, then the certificates and the ACME challenges:

```bash
sudo k3s kubectl -n geass-system get geasscluster default \
  -o jsonpath='{.status.conditions[?(@.type=="ClusterIssuerReady")].message}{"\n"}'
sudo k3s kubectl get clusterissuer letsencrypt-prod
sudo k3s kubectl get certificate,challenge -A
```

A missing issuer or an `ACMEEmailMissing` reason means no address was set. A challenge that stays `pending` usually means Let's Encrypt cannot reach 80/tcp on your server for that hostname, or the hostname's DNS record does not point at it.

**`warning: failed to initialize install logging`.** Neither the log path nor the temporary directory was writable. The install continues, without a file to read afterwards.

## Related

- [Requirements](/docs/getting-started/requirements/)
- [Sign in to the dashboard](/docs/getting-started/sign-in/)
- [Command line](/docs/guides/command-line/)
- [Add servers](/docs/getting-started/add-servers/)
- [Upgrade Geass](/docs/operations/upgrading/)
- [Uninstall Geass](/docs/operations/uninstall/)
