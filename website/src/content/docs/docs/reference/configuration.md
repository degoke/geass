---
title: "Configuration"
description: "Every configuration surface in Geass: installer variables, operator flags, CLI variables, the auth Secret, platform and cluster settings, defaults and limits."
---

Geass has no single configuration file. Settings live in six places: environment variables for the installer, flags and
environment variables for the operator (which also serves the dashboard), a Secret for dashboard users, the
`GeassPlatformConfig` and `GeassCluster` resources for platform behavior, the settings of each service, database and
bucket, and a flags, variables and login file for the `geass` command line. This page lists them, with defaults, and says which
take effect at once and which need a restart.

## Quick reference

| Surface | Where | Changes take effect |
| --- | --- | --- |
| Installer variables | Environment of the `geass` installer, once | At install time only |
| Operator flags | Arguments of the `geass-controller-manager` Deployment | After the operator restarts |
| Operator environment variables | Environment of that Deployment | After the operator restarts |
| Dashboard users and sessions | Secret `geass-dashboard-auth` | At once |
| Platform settings | `GeassPlatformConfig` named `platform` | Within seconds, no restart |
| Cluster add-ons and region | `GeassCluster` named `default` | Within seconds; add-on installs take minutes |
| Resource settings | Each service, database and bucket | Live, or at the next deploy |
| Command line | Flags, `GEASS_*` variables and `config.json` on the machine that runs `geass` | The next time you run it |

## Installer

The installer is a single binary, `geass`, that you run as root on the first server. On a bare machine it builds a cluster; on a machine
that already runs Geass it updates it, keeping its data; it deletes a cluster only when you pass `--reinstall`. It takes the options below and
reads these environment variables (see [Install](/docs/getting-started/install/) for the whole procedure, and
[Upgrade Geass](/docs/operations/upgrading/) for updates):

| Variable | Default | Meaning |
| --- | --- | --- |
| `GEASS_ACME_EMAIL` | none | The contact address for the cluster's Let's Encrypt account. Checked at the first step (it must look like `name@host.tld`, with no spaces and at most 254 characters), then written to the `GeassCluster` as `spec.addons.certManager.acmeEmail`. Without it Geass creates no certificate issuer and no HTTPS certificate is issued. Let's Encrypt refuses placeholder domains such as `example.com`. [HTTPS certificates](/docs/getting-started/install/#https-certificates) |
| `GEASS_MULTI_SITE` | off | `1`, `true`, `yes` or `on` builds a cluster whose nodes are at different providers: an encrypted WireGuard overlay instead of VXLAN. Chosen at install time because changing it later means restarting every node. [Multi-provider clusters](/docs/operations/multi-provider/) |
| `GEASS_NODE_EXTERNAL_IP` | none | This node's public IP address. Required when `GEASS_MULTI_SITE` is on; must be a valid IP. Workers join on it |
| `GEASS_K3S_VERSION` | the current `stable` K3s | The K3s release, in full (`v1.36.5+k3s1`), to build a new cluster on. The same as `--k3s-version` for a new cluster; when a cluster that exists is updated it is ignored (with a warning), so a value left over from the install does not upgrade K3s: pass `--k3s-version` for that |
| `GEASS_JOIN_SERVER` | none | The address of a cluster's server, such as `https://10.0.0.5:6443`. With it the installer does not build a cluster: it makes this machine an additional control plane server of that one. The same as `--join-server`, and it also needs the K3s release the cluster runs, in `GEASS_K3S_VERSION` or `--k3s-version`. See [High availability](/docs/operations/high-availability/#run-three-control-plane-servers) |
| `GEASS_JOIN_TOKEN` | none | The cluster's server token, from `/var/lib/rancher/k3s/server/token` on its first server. Needed with `GEASS_JOIN_SERVER`. Pass it here and not as `--join-token`, which other users of the machine can read in the process list |
| `GEASS_REINSTALL` | off | `1`, `true`, `yes` or `on`: same as `--reinstall` |
| `GEASS_YES` | off | `1`, `true`, `yes` or `on`: same as `--yes` |
| `GEASS_INSTALL_LOG` | `geass-install-<UTC timestamp>.log` in the current directory | Where the installer writes its log. If that path is not writable it falls back to the system temp directory |
| `GEASS_INSTALL_TRACE` | off | `1`, `true`, `yes` or `on` runs the downloaded K3s installer script with shell tracing (`sh -x`) |
| `GEASS_INSTALL_ROOT` | detected | A directory with installer assets. The installer binary embeds everything it needs and never asks for an override, so setting it has no effect on a normal install |

| Option | Meaning |
| --- | --- |
| `--upgrade` (or the word `upgrade`) | Update the existing cluster. The default when the machine has one |
| `--k3s-version <release>` | A new cluster: install this K3s. An existing cluster: also upgrade the control plane's K3s to it, and set `spec.k3s.version` so the operator upgrades the workers. One minor version at a time |
| `--allow-untested-k3s` | Allow a K3s outside the range this release's add-ons are known to run on |
| `--skip-snapshot` | Update without an etcd snapshot first |
| `--force` | Update although the cluster is not ready or a Longhorn volume is degraded |
| `--join-server <url>` | Do not build a cluster: make this machine an additional control plane server of the one at this address. Needs the server token in `GEASS_JOIN_TOKEN` (or `--join-token`) and the K3s release the cluster runs in `--k3s-version` (or `GEASS_K3S_VERSION`). See [High availability](/docs/operations/high-availability/#run-three-control-plane-servers) |
| `--reinstall` | Delete the cluster on this machine and build a new one. Asks for confirmation on the terminal |
| `--yes` | Do not ask for confirmation of `--reinstall` |
| `--version`, `--help` | Print the installer's release, or the options |

When you pipe the script, put options after `-s --`: `curl -sfL .../install.sh \| sudo sh -s -- --force`.

The installer passes its whole environment to the K3s install script, so K3s's own `INSTALL_K3S_*` variables reach it. Geass
itself sets `INSTALL_K3S_EXEC` to `server --cluster-init` and, if a version was set, `INSTALL_K3S_VERSION`. Without `GEASS_K3S_VERSION`
the installer sets no version, so K3s picks its default release. When it upgrades K3s on a running server it passes the server's own
arguments, read from its systemd unit, to the K3s script.

The convenience script `scripts/install.sh` reads three variables of its own: `GEASS_REPO` (default `degoke/geass`),
`GEASS_VERSION` (default `latest`) and `GEASS_DOWNLOAD_BASE` (a mirror or a `file://` directory to download from instead). It
downloads `geass-linux-amd64` or `geass-linux-arm64` and the release's `SHA256SUMS` from that repository's releases, checks the
one against the other, and runs the installer only if they match.

What the installer does, in order: checks the system (Linux, amd64 or arm64, at least 2 GB of memory and 10 GB free on `/`, and
`GEASS_ACME_EMAIL` if you set it);
opens ports 6443/tcp, 10250/tcp and the overlay port (8472/udp, or 51820/udp for multi-site) if it finds an active `ufw`,
`firewalld` or `iptables`; removes an existing K3s installation (only when you passed `--reinstall`); installs the iSCSI initiator that Longhorn needs; installs K3s as a
server with embedded etcd (`--cluster-init`); imports the operator image; applies the operator manifests; stores the K3s join token
in the Secret `geass-token`; and creates the `GeassCluster` named `default`. When `GEASS_ACME_EMAIL` is not set it prints a
warning at that last step and creates the cluster without an `acmeEmail`.

:::note
Running the installer on a machine that already runs Geass updates it and does not apply your `GEASS_ACME_EMAIL`,
`GEASS_MULTI_SITE` or `GEASS_NODE_EXTERNAL_IP` again: they describe a new cluster. To change a setting of a working cluster,
edit the `GeassCluster`.
:::

Files it writes on the server:

| Path | Content |
| --- | --- |
| `/etc/rancher/k3s/config.yaml` | K3s server settings: the kubelet log limits (below) and, for multi-site, the WireGuard backend, external IP and certificate name |
| `/etc/rancher/k3s/k3s.yaml` | The admin kubeconfig K3s writes |
| `/var/lib/rancher/k3s/server/node-token` | The join token, also copied to the Secret `geass-token` |

Geass sets `container-log-max-size=50Mi` and `container-log-max-files=5` on every node's kubelet, so a service's recent logs stay on
the node. Workers get the same settings plus their labels and taints when they join.

## Operator flags

The operator and the dashboard are one process, `/manager`. The installed Deployment `geass-controller-manager` in
`geass-system` runs it with four flags:

```text title="Installed arguments"
--metrics-bind-address=:8443
--leader-elect
--health-probe-bind-address=:8086
--dashboard-bind-address=:8085
```

| Flag | Default | Meaning |
| --- | --- | --- |
| `--dashboard-bind-address` | `:8085` | Where the dashboard and API listen (plain HTTP). `0` turns the dashboard off |
| `--health-probe-bind-address` | `:8086` | Where `/healthz` and `/readyz` are served |
| `--metrics-bind-address` | `0` (off) | Where operator metrics are served. The installed manifest sets `:8443` |
| `--metrics-secure` | `true` | Serve metrics over HTTPS with Kubernetes authentication and authorization. `--metrics-secure=false` serves plain HTTP |
| `--metrics-cert-path`, `--metrics-cert-name`, `--metrics-cert-key` | none, `tls.crt`, `tls.key` | A certificate for the metrics endpoint. Without one, a self-signed certificate is generated |
| `--leader-elect` | `false` | Elect one active operator among replicas. The installed manifest turns it on |
| `--enable-http2` | `false` | Allow HTTP/2 on the metrics server. Off to avoid the known HTTP/2 reset attacks |
| `--kubeconfig` | in-cluster | Path to a kubeconfig, for running outside the cluster |
| `--webhook-cert-path`, `--webhook-cert-name`, `--webhook-cert-key` | none, `tls.crt`, `tls.key` | Scaffolding for a certificate for an admission webhook server. The operator registers no admission webhooks, so these do nothing |
| `--zap-devel` | `true` | Development logging: console format and debug level. Set `--zap-devel=false` for JSON and info level |
| `--zap-encoder`, `--zap-log-level`, `--zap-stacktrace-level`, `--zap-time-encoding` | per `--zap-devel` | Log format, verbosity, stack trace threshold and timestamp format |

Change a flag by editing the Deployment. The container is the first and only one, called `manager`:

```bash title="Turn on info-level JSON logs"
kubectl -n geass-system patch deployment geass-controller-manager --type json \
  -p '[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--zap-devel=false"}]'
kubectl -n geass-system rollout status deployment/geass-controller-manager
```

:::note
Applying the operator manifest again (for example by reinstalling) overwrites changes you made to the Deployment. Keep your edits
in your own notes or automation.
:::

The pod itself runs on a control plane node, with a request of 10m CPU and 128Mi memory and a limit of 500m and 1Gi, as a
non-root user with a read-only root filesystem.

### Operator metrics

Besides the standard controller-runtime series (reconcile counts and durations, work queue depth, requests to the Kubernetes API), the
metrics endpoint serves these Geass series. With the monitoring add-on on, Geass scrapes them and defines alert rules on them (see
[Security](/docs/operations/security/)).

| Series | Labels | Meaning |
| --- | --- | --- |
| `geass_apiserver_client_request_duration_seconds` | `verb`, `resource`, `code` | How long the Kubernetes API server took to answer the operator (a histogram) |
| `geass_apiserver_client_requests_in_flight` | none | Requests sent to the API server that have not been answered yet |
| `geass_reconcile_not_ready_total` | `reason` | Reconciles that ended with a resource not ready |
| `geass_provision_duration_seconds` | `phase` | How long a deploy of a service, database or bucket took, start to outcome (a histogram) |
| `geass_status_writes_total` | `kind`, `result` | Status updates the controllers considered: `written`, `skipped` (nothing changed) or `failed` |

## Operator environment variables

These are read by the operator process, so set them on the Deployment, and restart it afterwards.

```bash title="Set an operator variable"
kubectl -n geass-system set env deployment/geass-controller-manager GEASS_PROMETHEUS_URL=http://prometheus.example.internal:9090
```

| Variable | Default | Meaning |
| --- | --- | --- |
| `GEASS_DASHBOARD_USERS` | none | Dashboard users, as a JSON array or as `username:password:role` entries separated by commas. Used only while the Secret has no users |
| `GEASS_ALLOW_LOOPBACK` | `false` | `true` lets **Settings** → **Sign-in** use an identity provider on the machine the operator runs on, such as Better Auth on `localhost:3000`, over plain `http`. For development, with the operator running on your own host (`make dashboard-backend` sets it). Leave it off in a cluster, where that address is the operator's own pod. Link-local addresses stay refused. See [Sign in with an identity provider](/docs/guides/external-sign-in/#try-it-on-your-own-machine) |
| `GEASS_DASHBOARD_PASSWORD` | none | A single admin password. Used only if no users are set. The user is named by `GEASS_DASHBOARD_USERNAME` |
| `GEASS_DASHBOARD_USERNAME` | `admin` | The name of the user created from `GEASS_DASHBOARD_PASSWORD` |
| `GEASS_DASHBOARD_SESSION_KEY` | generated | The key that signs session cookies, at least 16 bytes (or `hex:` followed by hex). Overrides the key in the Secret |
| `GEASS_ALERTMANAGER_URL` | `http://geass-kube-prometheus-stac-alertmanager.monitoring.svc:9093` | Where the dashboard asks Alertmanager what is firing and sends a test through it, on the **Alerts** page. Set it to use an Alertmanager other than the monitoring add-on's |
| `GEASS_PROMETHEUS_URL` | `http://geass-kube-prometheus-stac-prometheus.monitoring.svc:9090` | Where the dashboard queries metrics, unless the platform configuration sets `prometheusURL` |
| `GEASS_LOGS_URL` | `http://geass-victorialogs.geass-logging.svc:9428` | Where the dashboard reads stored log history (the logging add-on's VictoriaLogs server) |
| `GEASS_WEBHOOKS_ALLOW_PRIVATE_NETWORKS` | `false` | `true` lets project webhooks reach private network addresses (RFC 1918 and shared address space), and lets an alert channel be saved with a Slack or mail host on one. Loopback and link-local stay blocked |
| `GEASS_GITHUB_APP_ID`, `GEASS_GITHUB_APP_CLIENT_ID`, `GEASS_GITHUB_APP_CLIENT_SECRET`, `GEASS_GITHUB_APP_SLUG`, `GEASS_GITHUB_APP_PRIVATE_KEY`, `GEASS_GITHUB_WEBHOOK_SECRET` | none | GitHub App credentials, used only when the platform configuration has no `githubAppRef` (that is, nothing was saved in **Settings** → **Connectors**) |
| `GEASS_PUBLIC_URL` | none | The dashboard's public address for the GitHub App, overridden by the dashboard URL once you set a domain |
| `GOMEMLIMIT` | `800MiB` in the installed Deployment | The Go runtime's soft memory limit. It sits below the container's 1Gi limit, so the runtime collects harder near the limit instead of being killed for it. See [Cluster size](/docs/operations/sizing/) |

The default Prometheus address depends on the monitoring add-on. A `prometheusURL` set on the platform configuration wins over
`GEASS_PROMETHEUS_URL`, which wins over the default. Standard Kubernetes variables such as `KUBECONFIG` also apply, because the
operator uses controller-runtime.

## The dashboard auth Secret

Dashboard sign-in reads the Secret `geass-dashboard-auth` in `geass-system`, on every request, so edits apply at once. The
sample is `config/samples/geass_dashboard_auth.yaml`.

```yaml title="geass-dashboard-auth.yaml"
apiVersion: v1
kind: Secret
metadata:
  name: geass-dashboard-auth
  namespace: geass-system
type: Opaque
stringData:
  users: |
    [{"username":"admin","password":"replace-with-a-long-passphrase","role":"admin"}]
```

| Key | Content |
| --- | --- |
| `users` | A JSON array of `{"username","password","role"}`. Roles are `admin` and `viewer` (`observer` is a token role, not a user role). Plain passwords are replaced by bcrypt hashes the first time they are read |
| `session-key` | The key that signs sessions, as `hex:` plus 64 hex characters. Generated on first use. Delete it to sign everyone out |
| `session-epochs` | A JSON object of per-user counters that Geass increments on sign-out |
| `login-lockouts` | A JSON object of failed sign-in counters and lock times, per configured username. Expired records are removed and at most 256 are kept |

Geass writes the last three keys; you only need `users`. The full story is in [Users and API tokens](/docs/guides/users-and-tokens/).

## Command line

The [`geass` command line](/docs/guides/command-line/) runs on your own machine, not in the cluster. It combines, from highest to lowest
precedence, a flag, an environment variable and the saved login. Every command also takes `--json` and `--yes`; the full list is in the
[CLI reference](/docs/reference/cli/).

| Variable | Flag | Meaning |
| --- | --- | --- |
| `GEASS_URL` | `--url` | The dashboard's address, such as `https://geass.example.com` |
| `GEASS_TOKEN` | `--token` | An API token. `geass login` also takes the token to save from it |
| `GEASS_PROJECT` | `-p`, `--project` | The default project, by its `proj-...` ID |
| `GEASS_ENVIRONMENT` | `-e`, `--environment` | The default environment, such as `production` |
| `GEASS_CONTEXT` | `--context` | Which saved login to use |
| `GEASS_CONFIG` | none | The path of the login file. Default: `config.json` in the `geass` folder of your user configuration directory (`~/.config/geass/config.json` on Linux) |

`geass login` saves what it signed in to as a named login (its name is the dashboard's host name unless you pass `--name`) and makes it the
current one. The file is JSON; the folder is created with mode `0700` and the file with mode `0600`, and the token is stored in plain text:

```json title="~/.config/geass/config.json"
{
  "current": "geass.example.com",
  "contexts": {
    "geass.example.com": {
      "url": "https://geass.example.com",
      "token": "gsw_1a2b3c4d_0123456789abcdef0123456789abcdef01234567",
      "user": "token:CLI: geass CLI on laptop",
      "role": "observer",
      "project": "proj-1a2b3c4d",
      "environment": "production"
    }
  }
}
```

`geass config get-contexts` lists the saved logins (without tokens), `geass config use-context NAME` switches, and `geass config set-context
--project ... --environment ...` sets the default project and environment for a login. `geass logout` clears the saved token only.

The installer script for the command line, `scripts/install-cli.sh`, is separate from the cluster installer. It downloads
`geass-cli-<os>-<arch>` (Linux and macOS, amd64 and arm64) from a release and saves it as `geass`. It reads four variables:

| Variable | Default | Meaning |
| --- | --- | --- |
| `GEASS_VERSION` | `latest` | A release tag to install |
| `GEASS_INSTALL_DIR` | `/usr/local/bin` when you can write to it, otherwise `~/.local/bin` | Where to put the binary |
| `GEASS_REPO` | `degoke/geass` | The GitHub repository whose releases to download, for a fork |
| `GEASS_DOWNLOAD_BASE` | the release on GitHub | Where to download the release files from instead: a mirror, or a directory as `file:///path` holding the build and `SHA256SUMS` |

On Windows, download `geass-cli-windows-amd64.exe` from the release page. The MCP server (`geass mcp`) has no configuration of its own: it uses
the same login, flags and variables, plus `--read-only`, `--allow-delete` and `--listen`; see [MCP server](/docs/guides/mcp-server/).

## Platform settings

Platform behavior is stored in two resources that Geass creates for you. You can edit them with `kubectl`; most of their fields also have a
screen in **Settings**.

### GeassPlatformConfig

There is one, named `platform`, in `geass-system`. See [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/) for the schema.

| Field | Meaning | Edited in the dashboard at |
| --- | --- | --- |
| `rootDomain` | Your apex domain, such as `example.com` | **Settings** → **Domain** |
| `dashboardURL` | The dashboard's public HTTPS address; `https://<subdomain>.<rootDomain>` | **Settings** → **Domain** (**Subdomain**, default `geass`) |
| `dashboardExposure` | `ingress` (the default) or `cloudflare-tunnel` | **Settings** → **Domain** (**Publish via**) |
| `tunnelCNAMETarget` | The tunnel's `<id>.cfargotunnel.com` name; Geass sets it when it manages the tunnel | **Settings** → **Domain** |
| `cloudflareConnectionRef`, `cloudflareZoneID` | The Cloudflare Secret and DNS zone | **Settings** → **Connectors** → Cloudflare |
| `githubAppRef` | The Secret holding the GitHub App | **Settings** → **Connectors** → GitHub |
| `prometheusURL` | Where to query metrics | `kubectl` only |
| `defaultClusterRef` | The cluster new projects use when there are several | `kubectl` only |
| `registry.repository`, `registry.credentialRef` | The image destination and login for repository builds when a service sets none. The login is also what the nodes pull the built image with | The REST API (`/api/settings/registry/save`) or `kubectl`. The dashboard has no form for it yet |
| `defaultDomain`, `traefikAccessLogs` | Present in the schema; no code reads them | Not used |

:::caution
For repository builds, the registry value is passed to Kaniko as the destination image exactly as written. Geass does not add a service
name or a tag, so every service that uses the platform default pushes to the same image reference. Set `build.registry.repository` on each
service instead, with a tag. When neither is set, a repository service waits with `no container registry is set for builds`: Geass has
no registry of its own. See [GitHub and builds](/docs/guides/github-and-builds/#set-the-registry).
:::

Changes apply as soon as the controller sees them. Verifying a new dashboard domain polls every 5 seconds for 15 minutes, then every
5 minutes, so a late DNS fix still heals. The status shows `DashboardDomainReady` and `CloudflareReady`.

### GeassCluster

The installer creates one, named `default`. See [GeassCluster](/docs/reference/resources/geass-cluster/).

| Field | Default | Meaning |
| --- | --- | --- |
| `spec.addons.certManager.enabled` | `true` | Install cert-manager. When `false`, Geass removes the cert-manager it installed and manages no issuer |
| `spec.addons.certManager.acmeEmail` | empty (the installer sets it from `GEASS_ACME_EMAIL`) | The Let's Encrypt contact address, at most 254 characters, in the form `name@host.tld`. When set, Geass creates and keeps the `letsencrypt-prod` ClusterIssuer that every HTTPS Ingress asks for. Empty means no issuer is created |
| `spec.addons.monitoring.enabled` | `true` | Install the monitoring stack (Prometheus and Alertmanager). While it is on, Geass also scrapes the operator's own metrics and adds alert rules for the operator's health and for what runs in the cluster. Alerts need it: see [Alerts](/docs/guides/alerts/) |
| `spec.addons.monitoring.profile` | `lite` | `lite` or `full` footprint |
| `spec.addons.longhorn.enabled` | `false` (the installer sets `true`) | Install Longhorn for database volumes. It needs iSCSI on every node |
| `spec.addons.logging.enabled` | `false` (the installer sets `true`) | Install log collection and the VictoriaLogs store |
| `spec.addons.logging.retentionDays` | `7` (1 to 90) | How long stored logs are kept |
| `spec.addons.serverless.enabled` | `false` (the installer does not set it) | Install the scale-to-zero add-on, so services can sleep when idle: KEDA and its HTTP add-on (charts `geass-keda` and `geass-keda-http`, namespace `keda`). It also creates a K3s `HelmChartConfig` named `traefik` that lets Traefik route to the add-on's proxy, unless Traefik already has one, which it leaves alone and logs what to add. Turning it off stops services from sleeping and wakes the ones that are asleep, but never uninstalls KEDA. See [Scale to zero](/docs/guides/scale-to-zero/) |
| `spec.network.multiSite` | `false` | Nodes are at different providers. Set by the installer; changing it later does not rebuild nodes |
| `spec.defaultRegion` | empty | The region for projects that do not choose one. Edited in **Settings** → **Usage** (**Default region**) |
| `spec.serverURL` | The control plane's own address on its default route, for a single-site install | The address workers join. The installer fills it in with the address its default route uses (a private address on most cloud servers), and a multi-site install writes the public address. A loopback address is never used: workers join on the control plane node's own address instead. Set it only to use a different address. See [Add servers](/docs/getting-started/add-servers/#set-the-address-workers-join) |
| `spec.tokenSecretRef`, `spec.version` | `geass-token` in `geass-system`, and `v1` | The Secret with the join token, and a label that is not a K3s version |

The dashboard has no switch for the serverless add-on. Turn it on with `kubectl`:

```bash title="Turn on the scale-to-zero add-on"
kubectl -n geass-system patch geasscluster default --type merge \
  -p '{"spec":{"addons":{"serverless":{"enabled":true}}}}'
```

The add-on takes a few minutes to come up; until it does, a service that asks to sleep keeps running, and its `ScaleToZero` condition says why.

The `GeassCluster` also carries a `ClusterIssuerReady` condition that says whether the `letsencrypt-prod` issuer exists and
works. It is informational and does not change the cluster's `Ready`; Geass removes it when cert-manager is turned off. Its
reasons are `IssuerReady`, `ACMEEmailMissing`, `WaitingForCertManager`, `IssuerRegistering`, `IssuerNotReady`,
`ExternallyManaged`, `ManagedByAnotherCluster`, `IssuerApplyFailed` and `IssuerAccessDenied`. The same message is added to the
dashboard domain's status (`PendingHTTPS`) while its HTTPS is unreachable. Each reason is explained in
[HTTPS certificates](/docs/getting-started/install/#https-certificates).

```bash title="Read the issuer condition"
kubectl -n geass-system get geasscluster default \
  -o jsonpath='{.status.conditions[?(@.type=="ClusterIssuerReady")].reason}{": "}{.status.conditions[?(@.type=="ClusterIssuerReady")].message}{"\n"}'
```

## Defaults and limits

| What | Value |
| --- | --- |
| Dashboard session | 12 hours, not extended. Through an identity provider: 5 to 720 minutes, an hour by default |
| Identity provider sign-in | A sign-in has 10 minutes to finish; a test is good for 30 minutes; every request to the provider times out after 10 seconds; a logout token must be less than 5 minutes old |
| Sign-in lockout | 5 failures in a minute lock a username for a minute |
| API tokens and deploy hooks | At most 200 together; names up to 60 characters; lifetime 1 to 3650 days or none |
| Deploy hooks | A hook answers once every 30 seconds; at most one hook per service |
| Command-line sign-in | A request lives 10 minutes; the CLI polls every 5 seconds; at most 100 pending, and 10 started a minute per address. The token it hands out lasts 30 days unless the admin chooses 7 or 90 |
| `/api/v1` request body | At most 2 MiB |
| Webhooks | At most 20 per project; 3 delivery attempts; 10 second timeout; the last 20 deliveries are kept |
| Pull request environments | At most 25 open; default 5 |
| New service size | 0.1 CPU and 128 MB; allowed CPU 100m, 250m, 500m, 1, 2; allowed memory 128Mi to 4Gi |
| New database size | 250m CPU, 512Mi memory; 10Gi volume (1Gi for SQLite) |
| New object storage server size | 250m CPU, 512Mi memory for SeaweedFS, 100m and 256Mi for Garage; 10Gi volume, at least 1Gi, raised later and never lowered |
| Autoscaling | Target CPU 50 percent by default |
| Replica count | 0 to 1000 |
| Sleep when idle | Idle time 30 seconds to 24 hours, 300 seconds by default; 1 to 100 copies when awake, 3 by default |
| Function schedules | At most 8 per function, in UTC cron syntax; a run times out after 120 seconds, is skipped if it cannot start within 5 minutes, and never overlaps the previous run |
| Function code | At most 256 KiB |
| Compliance | At most 8 required tags per project; at most 16 tags per node |
| Environments | At most 100 per project |
| Backup retention | 7 days by default, 1 to 365; a deleted database's backups stay for 7 days |
| Deploy history | The last 12 deploys per service |
| Stored logs | 7 days by default (1 to 90) |
| Terminal session | 5 minutes |
| Domain verification | 15 minutes of polling every 5 seconds |

## Live settings and settings that need a deploy

For a service, database or bucket, Geass applies some edits at once and holds the rest until you deploy.

| Applied at once | Waits for a deploy |
| --- | --- |
| A service's replica count, autoscaling and sleep-when-idle settings; host, path and TLS; metrics scraping; log retention; failure threshold and the pause and rollback flags; repository watch patterns | A service's source (a function's code, mode and schedules included), port, variables and secrets, command and arguments, resources, probes and build settings |
| A database's backup settings | A database's version, volume size, CPU and memory |
| none for buckets | Every bucket setting |

Variables and secrets always need a deploy. See [Projects and environments](/docs/guides/projects-and-environments/).

:::note
One of the live settings does nothing yet. The spec field `logs.retentionHours` is accepted and saved, but nothing enforces the
log retention. The same is true of `build.argsFrom`: builds take no build arguments from Secrets.
:::

## Versions Geass pins

Geass installs these add-ons and images at fixed versions, through HelmChart resources that K3s's Helm controller installs in `kube-system`.
You cannot choose other versions from the dashboard.

| Component | Version | Namespace |
| --- | --- | --- |
| cert-manager (chart) | v1.17.2 | `cert-manager` |
| kube-prometheus-stack (chart) | 69.8.2 | `monitoring` |
| Longhorn (chart) | 1.12.1 | `longhorn-system` |
| CloudNativePG operator (chart) | 0.23.2 | `cnpg-system` |
| MOCO, the MySQL operator for high availability (chart) | 0.27.0 | `moco-system` |
| MOCO's MySQL, for high availability MySQL (image, no chart) | 8.4.10 | the project environment |
| Victoria Logs (chart) | 0.13.10 | `geass-logging` |
| Fluent Bit (chart) | 0.58.3 | `geass-logging` |
| MySQL (image, no chart) | 8.4.11 | the project environment |
| Redis (image, no chart) | 7.4.11-alpine | the project environment |
| SeaweedFS (image, no chart), for in-cluster object storage | 4.48 | `geass-system` (cluster server) |
| Garage (image, no chart), for in-cluster object storage | v2.4.1 | `geass-system` (cluster server) |
| alpine/socat (image), the port forwarder next to SeaweedFS | 1.8.1.1 | `geass-system` (cluster server) |
| rqlite for SQLite databases | 8.36.16 | the project environment |
| KEDA (chart), for sleep when idle | 2.21.0 | `keda` |
| KEDA HTTP add-on (chart), for sleep when idle | 0.16.0 | `keda` |
| Kaniko for repository builds | v1.23.2 | `geass-system` |
| cloudflared | 2024.10.0 | `geass-system` |

Postgres runs the `ghcr.io/cloudnative-pg/postgresql` image at the major version in the database's `version` field (default 16).

## Secrets Geass keeps in `geass-system`

| Secret | Holds |
| --- | --- |
| `geass-dashboard-auth` | Dashboard users and session state, including when each person who signs in through an identity provider last signed out (`sso-revoked`) |
| `platform-auth` | The client secret of the identity provider, written by **Settings** → **Sign-in** |
| `geass-api-tokens` | API token records (hashes) |
| `geass-token` | The K3s join token, read when a worker joins |
| `platform-github-app` | The GitHub App credentials |
| `geass-alert-channels` | The Slack webhook URL of each alert channel, under the channel's id |
| `geass-alert-smtp` | The password of the alert mail server |
| `platform-cloudflare`, `geass-cloudflared-token` | The Cloudflare API token, and the tunnel connector token |
| `<name>-credentials` | An AWS or PlanetScale connection |
| `geass-backup-storage-<bucket>` | The key for a backup bucket |
| `<project>-webhooks`, `<project>-shared-secrets` | Webhook signing secrets, and secret shared variables |
| `<app>-secrets` | A service's own secrets |
| `<project>-github`, `<project>-github-token` | A project's GitHub link (`<project>-github` is a connection resource; the token is its Secret) |

The `AlertmanagerConfig` `geass-alerts` in `geass-system` is made from the alert channels in the platform configuration: see [Alerts](/docs/guides/alerts/#how-it-works). Saved templates are ConfigMaps named `template-<id>` with the label `geass.dev/template=true`. A function's schedule token is the Secret
`<app>-trigger`, which lives in the service's environment namespace, not in `geass-system`.

## Ports

| Port | Component | Protocol |
| --- | --- | --- |
| 8085 | Dashboard and API (Service `geass-geass-dashboard`) | HTTP |
| 8086 | Operator health probes | HTTP |
| 8443 | Operator metrics (Service `geass-controller-manager-metrics-service`) | HTTPS |
| 8080 | The scale-to-zero proxy (`keda-add-ons-http-interceptor-proxy` in `keda`), in the cluster only, when that add-on is on | HTTP |
| 6443 | Kubernetes API | HTTPS |
| 10250 | kubelet | HTTPS |
| 8472 or 51820 | Overlay network (VXLAN, or WireGuard for multi-site) | UDP |

## Troubleshooting

**The operator does not pick up a flag.** Check the argument is on the `manager` container (`kubectl -n geass-system get deployment geass-controller-manager -o yaml`) and that the pod
restarted (`rollout status`).

**A dashboard user change has no effect.** Environment users are used only while the Secret has none. Edit the Secret, which wins once it has a
`users` key.

**Metrics are empty.** Check `spec.prometheusURL` on the platform configuration and `GEASS_PROMETHEUS_URL`; then that the monitoring add-on is enabled and
Prometheus answers at that address. See [Observability](/docs/guides/observability/).

**Log history says "not set up".** The logging add-on is off in `spec.addons.logging.enabled`. The installer turns it on for clusters it builds.

**A multi-site install stops with "needs this node's public address".** Set `GEASS_NODE_EXTERNAL_IP` to the node's public IP.

**A service never sleeps after you turn on sleep when idle.** The `ScaleToZero` condition on the service says why. The usual causes are that
`spec.addons.serverless.enabled` is not `true` on the `GeassCluster`, that the add-on in `keda` is still coming up, or that the service has no
public host or still has CPU autoscaling on. See [Scale to zero](/docs/guides/scale-to-zero/).

**`geass` says "not signed in".** No saved login, `GEASS_URL` or `GEASS_TOKEN` was found. Run `geass login --url https://geass.example.com`, or check
`GEASS_CONFIG` points at the file you expect.

## Related

- [Install](/docs/getting-started/install/)
- [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/)
- [GeassCluster](/docs/reference/resources/geass-cluster/)
- [Users and API tokens](/docs/guides/users-and-tokens/)
- [Command line](/docs/guides/command-line/) and [CLI reference](/docs/reference/cli/)
- [Security](/docs/operations/security/)
