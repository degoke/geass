---
title: "Install Geass"
description: "Install Geass on a Linux server with one command: what the installer does, its variables, its log, how to verify it, and what it does not do."
---

The installer changes one Linux server into a Geass control plane. It checks the machine, installs K3s, loads the Geass operator into K3s, and creates the cluster object. The cluster object starts everything else. The install takes a few minutes, and it needs no files other than the installer.

When the installer ends, the dashboard exists, but you cannot reach it or sign in yet. Read [What the installer does not do](#what-the-installer-does-not-do), and then [Sign in to the dashboard](/docs/getting-started/sign-in/).

## Before you start

You need these things:

- A server that meets the [requirements](/docs/getting-started/requirements/): Linux on `amd64` or `arm64`, 2 GiB of memory, 10 GiB free on `/`, root access, and outbound HTTPS.
- A machine that has no K3s on it. On a machine that already runs Geass, the installer updates that installation instead, and it keeps its data. On a machine with some other K3s, the installer stops and tells you why. The installer deletes a cluster only when you pass `--reinstall`. See [Run the installer again](#run-the-installer-again).
- A real email address for Let's Encrypt, if you want HTTPS certificates. The install itself does not need the address. See [HTTPS certificates](#https-certificates).

## Install

Connect to the server with SSH, and run this command:

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo sh
```

`install.sh` is a short script. It does these steps:

1. It finds your architecture from `uname -m`. `x86_64` and `amd64` become `amd64`. `aarch64` and `arm64` become `arm64`.
2. It downloads `geass-linux-<arch>` and the `SHA256SUMS` file of the release from the GitHub release into temporary files.
3. It checks the installer against `SHA256SUMS`.
4. It makes the installer executable and runs it.
5. When the installer ends, the script deletes the installer, whether the installer succeeded or not.

If a download does not match its checksum, or if `SHA256SUMS` does not list the download, the script does not run it. The script tells you, and it exits with status 1. Nothing on the machine has changed. If the download fails, the script prints the curl error, the URL, and the usual causes, and it exits with status 1. The downloaded installer contains the operator image and manifests, so the installer downloads nothing more from Geass.

The installer also writes every command that it prints to a log file. The first lines of output name the log file:

```text
Downloading Geass from https://github.com/degoke/geass/releases/latest/download/geass-linux-amd64
Checked geass-linux-amd64 against SHA256SUMS
Installation log: /home/ubuntu/geass-install-20261012-091500.log
→ system checks
✓ system checks
→ remove existing installation
...
```

Each step prints `→ <name>` when it starts and `✓ <name>` when it finishes. If a step fails, the installer prints `✗ <name> failed: <reason>` and `Detailed log: <path>`, and it exits with status 1.

## Pin a version

`latest` resolves to the newest release on GitHub. To install a specific release, use the `install.sh` of that release, and set `GEASS_VERSION` to the same tag. `sudo` clears most of the environment, so pass the variable through `env`:

```bash
curl -sfL https://github.com/degoke/geass/releases/download/v0.1.0/install.sh | sudo env GEASS_VERSION=v0.1.0 sh
```

Replace `v0.1.0` with a tag from the Releases page of the project. The cluster records the release that its operator runs. `sudo k3s kubectl -n geass-system get geasscluster` shows the release in the `RELEASE` column. An update refuses to go to an older release. See [Upgrade Geass](/docs/operations/upgrading/).

To build the cluster on a specific K3s release instead of the current stable release, pass `GEASS_K3S_VERSION`. Use the full name, for example `v1.36.5+k3s1`:

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo env GEASS_K3S_VERSION=v1.36.5+k3s1 sh
```

### Check what you download

Every release has a `SHA256SUMS` file. This file lists the installer, the command line, and the two install scripts, and the scripts check the files against it for you. Each file also has signed build provenance. The provenance shows which workflow run and which commit built the file. You can check a file yourself, from the release page or with the GitHub CLI:

```bash
gh release download v0.1.0 --repo degoke/geass --pattern 'geass-linux-amd64' --pattern SHA256SUMS
sha256sum --check --ignore-missing SHA256SUMS
gh attestation verify geass-linux-amd64 --repo degoke/geass
```

For what a release contains and how versions are numbered, see [Releases and versions](/docs/operations/releases/).

## HTTPS certificates

Geass gets the certificates for the dashboard and for your services from [Let's Encrypt](https://letsencrypt.org/), through cert-manager. Let's Encrypt registers an account for the cluster, and it wants a contact address for that account. Only you can give this address. Pass the address to the installer as `GEASS_ACME_EMAIL`:

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo env GEASS_ACME_EMAIL=you@yourdomain.com sh
```

Use a real address, because Let's Encrypt refuses placeholder domains such as `example.com`. The first installer step checks that the value looks like an email address: `name@host.tld`, no spaces, and at most 254 characters. If the value does not, the installer stops before it changes anything.

If you set an address, the installer writes it into the cluster object as `spec.addons.certManager.acmeEmail`. When cert-manager is ready, the operator creates a cert-manager `ClusterIssuer` named `letsencrypt-prod`. This issuer uses ACME against the production directory of Let's Encrypt, over HTTP-01, solved through the Traefik ingress class. Every Ingress that Geass creates for a hostname with **HTTPS** on asks that issuer for its certificate. The Ingress of the dashboard also asks that issuer.

Two ports are important:

- Let's Encrypt must reach your server on 80/tcp to validate a hostname.
- Visitors reach your server on 443/tcp.

The installer opens both ports in an active host firewall on the control plane. But the installer cannot open them in the firewall or security group of your provider. See [Ports and firewall](/docs/getting-started/requirements/#ports-and-firewall).

If you do not set an address, the cluster still installs and runs. But no issuer exists, and **no HTTPS certificate is issued**. The installer prints a warning that starts with `warning: GEASS_ACME_EMAIL is not set`. The cluster object then has a `ClusterIssuerReady` condition that tells you why. The operator also adds the same message to the `PendingHTTPS` message of the `DashboardDomainReady` condition on the platform configuration. It does this while the HTTPS of the dashboard is unreachable. The dashboard does not show either condition, so read the condition with `kubectl`:

```bash
sudo k3s kubectl -n geass-system get geasscluster default \
  -o jsonpath='{.status.conditions[?(@.type=="ClusterIssuerReady")].reason}{": "}{.status.conditions[?(@.type=="ClusterIssuerReady")].message}{"\n"}'
```

To set or change the address on a running cluster, edit the cluster object. This procedure also applies to a cluster that you installed without an address. The operator creates or updates the issuer on its next pass, which starts when you save:

```bash
sudo k3s kubectl -n geass-system patch geasscluster default --type merge \
  -p '{"spec":{"addons":{"certManager":{"acmeEmail":"you@yourdomain.com"}}}}'
```

`ClusterIssuerReady` is informational. A cluster without an issuer is still `Ready`, and it runs workloads. The `reason` of the condition tells you what to do:

| Reason | Meaning |
| --- | --- |
| `IssuerReady` | The issuer exists, and cert-manager has registered the ACME account. Certificates can be issued |
| `ACMEEmailMissing` | No address is set, so Geass created no issuer. Set `acmeEmail` |
| `WaitingForCertManager` | cert-manager is still installing. Geass checks again every 30 seconds |
| `IssuerRegistering` | The issuer exists, and cert-manager has not yet reported on it |
| `IssuerNotReady` | cert-manager reports the issuer as not ready. The message gives the reason. The most frequent reason is that Let's Encrypt refused the email address |
| `ExternallyManaged` | An issuer named `letsencrypt-prod` exists that Geass did not create, and cert-manager reports it ready. Geass leaves the issuer exactly as it is |
| `ManagedByAnotherCluster` | Another `GeassCluster` owns the issuer, which is cluster-wide. Geass does not change the issuer |
| `IssuerApplyFailed`, `IssuerAccessDenied` | Geass could not write the issuer, and it tries again every 30 seconds. With `IssuerApplyFailed`, the operator log tells you why. `IssuerAccessDenied` means that the role of the operator does not have access to `clusterissuers.cert-manager.io` |

Geass deliberately does not touch two things:

- Geass never overwrites a `letsencrypt-prod` issuer that you created yourself, for example a DNS-01 wildcard issuer.
- If you remove the address later, Geass does not delete the issuer that it made. Certificates that the issuer issued still renew through it.

If you run your own cert-manager, set `spec.addons.certManager.enabled` to `false`. Geass then manages no issuer, and it also removes the cert-manager that it installed.

A dashboard that you publish through a Cloudflare Tunnel gets its certificate from Cloudflare, not from cert-manager. The tunnel reaches your services over HTTP inside the cluster. Thus, the issuer is not involved there.

## What the installer does

The installer runs these steps in sequence. The names are the names that it prints.

| # | Step | What it does |
| --- | --- | --- |
| 1 | `system checks` | Requires Linux, `amd64` or `arm64`, a server with 2 GB of memory, and 10 GiB free on `/`. For memory, it requires at least 1700 MiB of `MemTotal`, which is what a machine sold as 2 GB reports. It also checks `GEASS_ACME_EMAIL` and `GEASS_K3S_VERSION` when you set them. With `GEASS_MULTI_SITE`, it validates the public address and tests that WireGuard works |
| 2 | `remove existing installation` | Does nothing on a bare machine. Uninstalls K3s only when you passed `--reinstall`, after a confirmation and a last etcd snapshot. See [Run the installer again](#run-the-installer-again) |
| 3 | `configure firewall` | Opens 6443/tcp, 10250/tcp, the overlay port, and 80/tcp and 443/tcp for web traffic. It opens them in the first active firewall that it finds: `ufw`, `firewalld`, then `iptables`. With `iptables`, it also saves the rules where the host restores them at boot, if the host does that. This step runs after the installer removes the old K3s. Thus, the rules that it saves are the rules of the host, not the chains of a cluster that is being replaced. If no firewall is active, it prints a warning and continues. See [Requirements](/docs/getting-started/requirements/#ports-and-firewall) |
| 4 | `install node prerequisites` | Installs and enables the iSCSI initiator that Longhorn needs. If this step fails, the installer logs the failure and does not stop the install |
| 5 | `install k3s (server)` | Writes `/etc/rancher/k3s/config.yaml`, downloads `https://get.k3s.io`, and runs it with `INSTALL_K3S_EXEC="server --cluster-init"`. The result is a K3s server with embedded etcd, on the default release channel of K3s |
| 6 | `wait for k3s ready` | Waits up to 5 minutes for `/etc/rancher/k3s/k3s.yaml`, then runs `k3s kubectl get node` |
| 7 | `import operator image` | Writes the operator image, which is embedded in the installer, to a temporary file. Then loads the image into the containerd of K3s with `k3s ctr image import` |
| 8 | `deploy geass-operator` | Applies the embedded manifests with `k3s kubectl apply`: the `geass-system` namespace, the Geass custom resource definitions, RBAC, the operator Deployment, and the dashboard Service |
| 9 | `wait for operator ready` | Waits up to 5 minutes for `deployment/geass-controller-manager` to roll out |
| 10 | `store worker join token` | Reads the node token of K3s, and saves it as the Secret `geass-token` in `geass-system`. The operator reads the token there when it joins workers. The installer gives the token on standard input, so the installer log does not contain the token |
| 11 | `apply GeassCluster CR` | Waits up to 60 seconds for the `geassclusters` definition. Then applies the cluster object that this page shows below, with the address of this server as `serverURL`. Without `GEASS_ACME_EMAIL`, it prints the warning that [HTTPS certificates](#https-certificates) describes |

Step 5 writes `/etc/rancher/k3s/config.yaml` with mode 0600. The file sets these things:

- The container log rotation of the kubelet: 50Mi per file and 5 files. Thus, the dashboard has log history to read.
- K3s secrets encryption. K3s encrypts every Secret in the cluster in its datastore and in its etcd snapshots. See [Security](/docs/operations/security/#secrets).

The file has this content:

```yaml title="/etc/rancher/k3s/config.yaml"
kubelet-arg:
- container-log-max-size=50Mi
- container-log-max-files=5
secrets-encryption: true
```

The update mode of the installer never rewrites `/etc/rancher/k3s/config.yaml`. Thus, an update does not turn on encryption in a cluster that was built without encryption. To turn on encryption in such a cluster, follow the K3s documentation on secrets encryption.

Step 11 applies this cluster object:

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

If you set `GEASS_ACME_EMAIL`, the object also has these lines under `addons`:

```yaml
    certManager:
      acmeEmail: "you@yourdomain.com"
```

`spec.version` is a label. Geass does not use it to select a K3s version. Two other fields hold the releases:

- `status.release` is the release that the cluster runs. The operator writes this field.
- `spec.k3s.version` is the K3s release that the workers are to run. This field is empty until you or an update sets it.

`serverURL` is the address that Geass tells new workers to join. The installer sets it to the address of this server on its default route. The `10.0.0.5` in the example stands for your address. On most cloud servers, this address is private. With `GEASS_MULTI_SITE`, the installer uses the public address instead.

If the installer cannot find an address, it stores `https://127.0.0.1:6443` and prints a warning. The operator then joins workers on the address of the control plane node itself. See [Add servers](/docs/getting-started/add-servers/#set-the-address-workers-join).

The cluster object does not turn on cert-manager or monitoring, because both are on by default. The operator also installs them. With Longhorn and log collection, that makes four add-ons. The Helm controller of K3s installs them after the installer has exited. A fifth add-on, the serverless add-on, is off. The serverless add-on lets services sleep when they are idle. The installer never sets `spec.addons.serverless.enabled`. See [How Geass works](/docs/overview/how-it-works/) and [Sleep when idle](/docs/guides/scale-to-zero/).

## Environment variables

| Variable | Read by | Meaning | Default |
| --- | --- | --- | --- |
| `GEASS_VERSION` | `install.sh` | The release tag of the installer binary to download | `latest` |
| `GEASS_REPO` | `install.sh` | GitHub `owner/name` to download releases from | `degoke/geass` |
| `GEASS_DOWNLOAD_BASE` | `install.sh`, `install-cli.sh` | The address to download the release files from, instead of the GitHub release: a mirror, or a directory as `file:///path`. It must hold `SHA256SUMS`, and the installer (`geass-linux-<arch>`) or the command line (`geass-cli-<os>-<arch>`). The script still checks the download against `SHA256SUMS` | the release on GitHub |
| `GEASS_K3S_VERSION` | installer | K3s release to build a new cluster on, such as `v1.36.5+k3s1`. The installer does not accept a channel name. On a machine that already runs Geass, the installer ignores this variable and prints a warning. Thus, a value left over from the install does not upgrade K3s. To upgrade the control plane, pass the `--k3s-version` option. See [Upgrade Geass](/docs/operations/upgrading/) | the current `stable` release |
| `GEASS_JOIN_SERVER` | installer | The address of a server of an existing cluster. Makes this machine an additional control plane server of that cluster, instead of building a cluster. It also needs the K3s release of the cluster in `GEASS_K3S_VERSION`. See [High availability](/docs/operations/high-availability/#run-three-control-plane-servers) | none |
| `GEASS_JOIN_TOKEN` | installer | The server token of the cluster, which a join needs | none |
| `GEASS_REINSTALL` | installer | `1`, `true`, `yes`, or `on`. The same as `--reinstall`: delete the cluster on this machine and build a new one. See [Run the installer again](#run-the-installer-again) | off |
| `GEASS_YES` | installer | `1`, `true`, `yes`, or `on`. The same as `--yes`: do not ask for confirmation of a reinstall | off |
| `GEASS_INSTALL_LOG` | installer | Path of the log file. The installer creates missing directories | `geass-install-<UTC time>.log` in the current directory |
| `GEASS_INSTALL_TRACE` | installer | `1`, `true`, `yes`, or `on` runs the K3s install script with `sh -x`. Then the log shows every command in that script | off |
| `GEASS_ACME_EMAIL` | installer | Contact address for the Let's Encrypt account of the cluster. The installer checks it at the first step, and then stores it as `spec.addons.certManager.acmeEmail`. Without it, Geass creates no certificate issuer. See [HTTPS certificates](#https-certificates) | none |
| `GEASS_MULTI_SITE` | installer | `1`, `true`, `yes`, or `on`. Builds a cluster whose nodes are at different providers. Needs `GEASS_NODE_EXTERNAL_IP` | off |
| `GEASS_NODE_EXTERNAL_IP` | installer | The public IP address of this server. Required with `GEASS_MULTI_SITE`. It must parse as an IP address | none |
| `GEASS_MESH` | installer | `wireguard` or `tailscale`: nodes at different sites, some behind a NAT, joined over a mesh. `tailscale` also needs `GEASS_TAILSCALE_AUTH_KEY_FILE`. See [Mesh networking](/docs/operations/mesh-networking/) | none |
| `GEASS_TAILSCALE_AUTH_KEY_FILE` | installer | With `GEASS_MESH=tailscale`: a file that holds the auth key | none |
| `GEASS_TAILSCALE_CONTROL_URL` | installer | With `GEASS_MESH=tailscale`: the `https` address of a Headscale server | none |

The installer passes its full environment to the K3s install script. Thus, the `INSTALL_K3S_*` variables of K3s take effect when you set them. Geass sets `INSTALL_K3S_EXEC` itself, and it overrides your value. Geass also sets `INSTALL_K3S_VERSION` when you pass `GEASS_K3S_VERSION`. If you set nothing, K3s selects its release channel: the `stable` channel at the time that you install.

The installer also takes options. When you pipe the installer, put the options after `-s --`. The options are `--k3s-version`, `--upgrade`, `--force`, `--skip-snapshot`, `--allow-untested-k3s`, `--reinstall`, `--yes`, `--join-server`, `--etcd-snapshots`, `--version`, and `--help`. For the update options, see [Upgrade Geass](/docs/operations/upgrading/#options).

### Copies of the cluster's state

On a terminal, the installer asks one question before it installs K3s. The question is whether to keep copies of the state of the cluster in an S3 bucket of your own. With these copies, you can rebuild the cluster if the disk of this machine is lost. The default is **no**. The answer is also no when there is no terminal to ask on, for example in a script. To say yes in advance, pass `--etcd-snapshots`, or name the bucket with `GEASS_ETCD_SNAPSHOTS_BUCKET` and the related variables.

If you say yes, the installer shows the server token of the cluster on the terminal, one time. It asks you to store the token in a place that is not this machine. You can also turn on the copies later. See [Disaster recovery](/docs/operations/disaster-recovery/).

:::note
By default, `sudo` drops variables that you set before it. Write `sudo env NAME=value sh`, not `NAME=value sudo sh`.
:::

## Where the installer logs

The log file is `geass-install-YYYYMMDD-HHMMSS.log` (UTC), in the directory that you ran the installer from. To use another path, set `GEASS_INSTALL_LOG`. If that location is not writable, the installer uses your temporary directory, usually `/tmp`. It then prints `Primary log path was not writable, using fallback log file`. If neither location works, the installer prints a warning and installs without a log.

Each line looks like `2026-10-12T09:15:02Z [install k3s (server)] [stdout] ...`. A line has a time, the step, the stream, and the text. The stream is `info`, `warning`, `stdout`, `stderr`, or `combined`. The file records each command line that the installer runs, and the output of that command.

## Confirm it worked

To check the install, run these commands:

```bash
sudo k3s kubectl get nodes
sudo k3s kubectl -n geass-system get pods
sudo k3s kubectl -n geass-system get geasscluster
```

You see the node `Ready`, a `geass-controller-manager` pod `Running`, and a cluster named `default`. The `READY` column of the cluster becomes `True` when the add-ons are up. The add-ons take longer than the installer. To watch them, run these commands:

```bash
sudo k3s kubectl -n geass-system get geasscluster default \
  -o jsonpath='{range .status.conditions[*]}{.type}={.status}{"\n"}{end}'
sudo k3s kubectl -n kube-system get helmcharts
sudo k3s kubectl get pods -A
```

When cert-manager is up, the conditions also include `ClusterIssuerReady`. This condition does not affect `Ready`. For what it reports, see [HTTPS certificates](#https-certificates). To check the issuer itself, run `sudo k3s kubectl get clusterissuer letsencrypt-prod`.

The add-on charts are named `geass-cert-manager`, `geass-kube-prometheus-stack`, `geass-longhorn`, `geass-victoria-logs`, and `geass-fluent-bit`. When you turn on the serverless add-on, there are two more: `geass-keda` and `geass-keda-http`.

Wait until the cluster reports `Ready=True` before you create your first project. Until then, a project reports `Referenced GeassCluster is not ready`. A resource that you deploy in that time reports that its project or cluster is not ready. After repeated failures, the resource stops retrying. It shows **Failed** until you click **Retry**.

The logs of the operator and the dashboard are the same stream:

```bash
sudo k3s kubectl -n geass-system logs deploy/geass-controller-manager -c manager
```

## Install the command line (optional)

The installer builds the cluster. The `geass` command line is a separate program that you run on your own machine. The command line needs a dashboard that you can already sign in to. Thus, install the command line after you do the steps in [Sign in to the dashboard](/docs/getting-started/sign-in/). Install it on your laptop or in CI, not on the server:

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install-cli.sh | sh
geass login --url https://geass.example.com
```

`install-cli.sh` is a release file next to `install.sh`. The script does these things:

- It downloads the `geass-cli-<os>-<arch>` build for Linux or macOS on `amd64` or `arm64`.
- It installs the build as `geass` in `/usr/local/bin`. If `/usr/local/bin` is not writable, it uses `~/.local/bin`.
- It checks the download against the `SHA256SUMS` of the release, as `install.sh` does. It installs nothing that does not match.
- It reads `GEASS_VERSION`, `GEASS_INSTALL_DIR`, `GEASS_REPO`, and `GEASS_DOWNLOAD_BASE`.

Do not confuse the command line with the installer binary `geass-linux-<arch>` that `install.sh` runs on the server. The installer binary builds a cluster. It is not the command line.

`geass login` prints a code and opens the `/device` page of the dashboard. An admin approves the code on that page. See [Sign in to the dashboard](/docs/getting-started/sign-in/#sign-in-from-the-command-line). For the command line and the MCP server for AI agents (`geass mcp`), see [Command line](/docs/guides/command-line/) and [MCP server for AI agents](/docs/guides/mcp-server/).

## What the installer does not do

- **It does not set up dashboard sign-in.** No username or password exists after the install, and the installer prints none. You create them. See [Sign in to the dashboard](/docs/getting-started/sign-in/).
- **It does not publish the dashboard.** The dashboard is a `ClusterIP` Service, which you can reach only from inside the cluster. There is no ingress, domain, or certificate until you configure a domain from the dashboard.
- **It does not issue certificates without your email address.** The installer enables cert-manager. The operator creates the `letsencrypt-prod` issuer that the Ingress objects of Geass ask for. But the operator creates the issuer only when it has a contact address (`GEASS_ACME_EMAIL`). See [HTTPS certificates](#https-certificates) and [Domains and TLS](/docs/guides/domains-and-tls/).
- **It does not touch your provider's firewall or security group.** It opens ports only in a host firewall that is active on the control plane: `ufw`, `firewalld`, or `iptables`. Ports 80 and 443 must also be open in the firewall of the provider. If they are not, visitors cannot reach your services, and Let's Encrypt cannot validate your hostnames. The installer opens no ports on workers.
- **It does not add workers.** See [Add servers](/docs/getting-started/add-servers/).
- **It does not turn on the serverless add-on.** KEDA and its HTTP add-on let a service sleep when it is idle. Geass installs them only after you set `spec.addons.serverless.enabled` on the cluster. See [Sleep when idle](/docs/guides/scale-to-zero/#turn-on-the-add-on).
- **It does not install the command line** on your workstation. See [Install the command line](#install-the-command-line-optional).
- **It does not configure backups, Cloudflare, GitHub, or any cloud account.**
- **It does not send telemetry.** The repository contains no analytics or telemetry code.

## Install for a multi-site cluster

By default, the nodes of a cluster share a private network, and the VXLAN overlay of K3s carries the traffic. If your nodes are at different providers and reach each other over the internet, give the installer the public address of this server:

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo env GEASS_MULTI_SITE=1 GEASS_NODE_EXTERNAL_IP=203.0.113.10 sh
```

The installer then does these things:

- It checks that the address is valid, and that the kernel can create a WireGuard interface.
- It configures K3s with the `wireguard-native` backend, `node-external-ip`, `flannel-external-ip`, and a TLS SAN for the address. It opens 51820/udp instead of 8472/udp.
- It sets `spec.serverURL` of the cluster object to `https://203.0.113.10:6443`, and `spec.network.multiSite` to `true`. Thus, workers join on the public address.

You select the mode at install time. To switch an existing cluster, you must restart every node. Keep the control plane at one site.

For nodes behind a NAT with no public address, use `GEASS_MESH=wireguard` or `GEASS_MESH=tailscale`. Also use these values to use Tailscale or Headscale instead of WireGuard. See [Mesh networking](/docs/operations/mesh-networking/). That page states clearly that mesh networking was not run on real machines.

:::caution
Before you build a multi-provider cluster, read [Multi-provider clusters](/docs/operations/multi-provider/) for the ports to open and the limits. Multi-provider clusters are experimental. The project has run a four-node cluster across three availability zones of one provider, over public addresses. The project has not yet run a cluster across two different providers.
:::

## Run the installer again

The installer examines the machine and does what the machine needs. It never deletes a cluster unless you tell it to.

| The machine has | The installer |
| --- | --- |
| No K3s | Builds a new cluster |
| K3s with Geass in it | **Updates** the cluster and keeps its data. It makes a snapshot first. See [Upgrade Geass](/docs/operations/upgrading/) |
| K3s without Geass, a K3s whose API server does not answer, or a K3s agent (a worker) | Stops, and tells you why. It changes nothing |
| Any of the above, and you passed `--reinstall` | Deletes the cluster and builds a new one |

You can safely run the installer again for the same release. The installer runs its checks, replaces nothing that has not changed, and restarts nothing.

### Reinstall

:::danger
Use `--reinstall` only when you can lose everything in the old cluster. `--reinstall` uninstalls K3s from the machine and builds a new cluster. It deletes everything in the old cluster.
:::

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo sh -s -- --reinstall
```

The installer tells you what it deletes, and it asks you to enter `reinstall` on your terminal. A script with no terminal passes `--yes`. Before the installer removes anything, it makes a last etcd snapshot of the old cluster in `/var/backups/geass`. This directory is outside the K3s directories. Nothing restores this snapshot for you. If the cluster is broken, the installer might not be able to make a snapshot. In that case, the reinstall continues.

Step 2, `remove existing installation`, treats K3s as installed when any of these files exist:

- `/usr/local/bin/k3s-uninstall.sh`
- `/usr/local/bin/k3s-agent-uninstall.sh`
- `/usr/local/bin/k3s`
- `/etc/systemd/system/k3s.service`
- `/etc/systemd/system/k3s-agent.service`

The installer then runs the first uninstall script that it finds. The K3s uninstall script removes `/etc/rancher/k3s`, the K3s data directory (`/var/lib/rancher/k3s` by default), and `/var/lib/kubelet`. The data directory holds the embedded etcd database of the cluster and the local-path volumes of K3s. Thus, after a reinstall, these statements are true:

- Every project, service, database definition, secret, dashboard user, API token, and connection of the old cluster is gone. The data on local-path volumes is also gone.
- Workers that joined the old cluster continue to run their agents. But they hold credentials of a cluster that no longer exists.
- Things outside the K3s directories are not touched: S3 buckets and backups, DNS records, Cloudflare tunnels, IAM users, and the resources of your provider. The data directory of Longhorn (`/var/lib/longhorn/` by default) also stays on disk, with nothing that refers to it.

Use a reinstall to retry on a machine where an install failed part-way and nothing of value exists yet. You can also use a reinstall to rebuild a cluster that you mean to replace. To update a working cluster, read [Upgrade Geass](/docs/operations/upgrading/).

If K3s files exist but no uninstall script exists, a reinstall stops with `existing k3s installation detected but no uninstall script found at /usr/local/bin/k3s-uninstall.sh or /usr/local/bin/k3s-agent-uninstall.sh`. Remove the leftover K3s files yourself, and run the installer again.

## What happens under the hood

The installer and K3s leave these files on disk:

| Path | What |
| --- | --- |
| `/usr/local/bin/k3s`, and `kubectl`, `crictl`, `ctr` symlinks | K3s |
| `/etc/rancher/k3s/config.yaml` | K3s server settings that the installer writes |
| `/etc/rancher/k3s/k3s.yaml` | Admin kubeconfig. Root only |
| `/var/lib/rancher/k3s/` | Cluster data, including `server/node-token` |
| `/usr/local/bin/k3s-uninstall.sh` | The K3s uninstall script |
| `geass-install-*.log` | The installer log |
| A temporary file from `mktemp` | The downloaded installer. `install.sh` deletes it when the installer ends. Earlier versions of the script replaced themselves with the installer. That left the file behind until the temporary directory was cleared |

K3s runs as the `k3s` systemd service, and it starts at boot. In the cluster, the installer creates these objects:

| Object | Namespace | Notes |
| --- | --- | --- |
| Namespace `geass-system` | | The objects and secrets of Geass live here |
| 16 `geass.geass.dev` definitions and `helmcharts.helm.cattle.io` | | `kubectl get crd` |
| Deployment `geass-controller-manager` | `geass-system` | One replica, container `manager`. It must run on the control plane. On replacement, the old pod stops before the new pod starts. Image `geass-operator:<release tag>` in release builds. Memory limit 1Gi, with `GOMEMLIMIT=800MiB`. Its pod template has the annotations `geass.dev/release` and `geass.dev/operator-image-sha256` |
| Service `geass-geass-dashboard` | `geass-system` | `ClusterIP`, port 8085. The name has `geass-` two times, because the manifest build adds a prefix to every name |
| Service `geass-controller-manager-metrics-service` | `geass-system` | Operator metrics, port 8443 |
| Secret `geass-token` | `geass-system` | K3s join token |
| ConfigMap `geass-schema` | `geass-system` | The operator makes it when it first starts. It holds the release that last started and the data schema level. The operator checks these values when it starts. It refuses to run over data that is newer than it knows. See [Upgrade Geass](/docs/operations/upgrading/#how-an-update-stays-safe) |
| ClusterRoleBinding `geass-metrics-reader-prometheus` | cluster-wide | Gives the Prometheus of the monitoring add-on access to the metrics of the operator. It names a service account that exists only after the monitoring add-on is installed |
| GeassCluster `default` | `geass-system` | Shown above |

## Troubleshooting

**`Could not download Geass from <url>`.** The download failed. The line above it is the curl error, for example `curl: (22) The requested URL returned error: 404` for a tag or file that does not exist. These are the usual causes: a `GEASS_VERSION` that is not a release, a release with no file for your architecture, or no outbound access to `github.com`. Open the URL in a browser, or run `curl -fI <url>`. The script reports an unsupported architecture before the download, as `unsupported architecture`.

**`✗ system checks failed: insufficient memory: ... MB (a server with 2 GB is needed, which the kernel reports as at least 1700 MB)`, or `insufficient disk space on /`.** See [Requirements](/docs/getting-started/requirements/#memory-and-disk).

**`✗ system checks failed: GEASS_ACME_EMAIL "..." is not an email address`.** The value must look like `name@host.tld`, with no spaces and at most 254 characters. This step runs first, so nothing on the machine has changed. Fix the variable, and run the installer again.

**`warning: GEASS_ACME_EMAIL is not set, so no certificate issuer is created ...`.** This warning is informational. The install continues. Geass issues no HTTPS certificate until you set the address. See [HTTPS certificates](#https-certificates).

**`warning: No active host firewall detected; skipping port configuration`.** This warning is informational. K3s manages its own packet rules, so the cluster starts. If you add a firewall later, open the ports that the warning lists. These ports include 80/tcp and 443/tcp.

**`warning: The iptables rules that open the ports are not saved ...`.** The host filters with `iptables`, but it does not restore rules at boot. There is no `/etc/iptables/rules.v4` or `/etc/sysconfig/iptables`. Thus, the ports that the installer opened close again after a reboot. Install `iptables-persistent` (Debian, Ubuntu) or `iptables-services` (Red Hat family), and save the rules. See [Requirements](/docs/getting-started/requirements/#ports-and-firewall).

**`warning: Could not work out this machine's address ...`.** The installer found no usable address on the default route, so `spec.serverURL` holds `https://127.0.0.1:6443`. The operator joins workers on the address of the control plane node itself anyway. To select another address, patch `spec.serverURL`. See [Add servers](/docs/getting-started/add-servers/#set-the-address-workers-join).

**`✗ configure firewall failed: ufw allow 6443/tcp: ...`.** The firewall command failed. To see why, run the command yourself. Or turn off the firewall for the install, and open the ports afterwards.

**`✗ install k3s (server) failed: download k3s installer: ...`.** The server cannot reach `get.k3s.io`. Check DNS and outbound HTTPS.

**`k3s did not become ready within 5 minutes`.** The installer installed K3s, but K3s did not start. Check `sudo systemctl status k3s` and `sudo journalctl -u k3s`. Low memory is a frequent cause.

**`the operator did not become ready within 5m0s`**, or **`the operator pod is not starting: ...`**. If a pod is crash looping or cannot pull its image, the installer reports it immediately, without the five-minute wait. While the installer waits, it prints `waiting...` with the rollout status every few seconds. Look at the pod with `sudo k3s kubectl -n geass-system describe pod -l control-plane=controller-manager`, and then use the logs command above. If you built the installer yourself and you see `embedded operator image is empty`, rebuild it with `make build-geass`.

**`✗ apply GeassCluster CR failed: wait for GeassCluster CRD: ...`.** The definition was not established within 60 seconds. Check the operator pod and the API server. Then run the installer again on this fresh machine.

**`a multi-site cluster needs this node's public address: set GEASS_NODE_EXTERNAL_IP`, `GEASS_NODE_EXTERNAL_IP "..." is not an IP address`, or `this machine's kernel cannot create a WireGuard interface`.** Fix the variable. Or, on a kernel older than 5.6, install the `wireguard` package.

**The installer finished and the cluster stays `READY=False`.** The add-ons are not up. Check `sudo k3s kubectl -n kube-system get helmcharts` and the pods in `cert-manager`, `monitoring`, and `longhorn-system`. Longhorn needs the iSCSI initiator. Check `command -v iscsiadm`, and the install log lines for `install node prerequisites`.

**HTTPS is on for a hostname and the browser warns about the certificate.** Traefik serves its own certificate, because no certificate was issued. Check the issuer, and then the certificates and the ACME challenges:

```bash
sudo k3s kubectl -n geass-system get geasscluster default \
  -o jsonpath='{.status.conditions[?(@.type=="ClusterIssuerReady")].message}{"\n"}'
sudo k3s kubectl get clusterissuer letsencrypt-prod
sudo k3s kubectl get certificate,challenge -A
```

A missing issuer or an `ACMEEmailMissing` reason means that no address was set. If a challenge stays `pending`, usually Let's Encrypt cannot reach 80/tcp on your server for that hostname. Or the DNS record of the hostname does not point at your server.

**`warning: failed to initialize install logging`.** Neither the log path nor the temporary directory was writable. The install continues, but there is no log file to read afterwards.

## Related

- [Requirements](/docs/getting-started/requirements/)
- [Sign in to the dashboard](/docs/getting-started/sign-in/)
- [Command line](/docs/guides/command-line/)
- [Add servers](/docs/getting-started/add-servers/)
- [Upgrade Geass](/docs/operations/upgrading/)
- [Uninstall Geass](/docs/operations/uninstall/)
