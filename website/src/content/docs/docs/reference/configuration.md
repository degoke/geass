---
title: "Configuration"
description: "Every configuration surface in Geass: installer variables, operator flags, CLI variables, the auth Secret, platform and cluster settings, defaults, and limits."
---

Geass has no single configuration file. Settings are in six places:

- Environment variables for the installer.
- Flags and environment variables for the operator, which also serves the dashboard.
- A Secret for dashboard users.
- The `GeassPlatformConfig` and `GeassCluster` resources for platform behavior.
- The settings of each service, database, and bucket.
- Flags, variables, and a login file for the `geass` command line.

This page lists these settings, with defaults. It also says which settings take effect immediately and which need a restart.

## Quick reference

| Surface | Where | Changes take effect |
| --- | --- | --- |
| Installer variables | Environment of the `geass` installer, one time | At install time only |
| Operator flags | Arguments of the `geass-controller-manager` Deployment | After the operator restarts |
| Operator environment variables | Environment of that Deployment | After the operator restarts |
| Dashboard users and sessions | Secret `geass-dashboard-auth` | Immediately |
| Platform settings | `GeassPlatformConfig` named `platform` | Within seconds, no restart |
| Cluster add-ons and region | `GeassCluster` named `default` | Within seconds. Add-on installs take minutes |
| Resource settings | Each service, database, and bucket | Live, or at the next deploy |
| Command line | Flags, `GEASS_*` variables, and `config.json` on the machine that runs `geass` | The next time you run it |

## Installer

The installer is a single binary, `geass`, that you run as root on the first server. It works in these ways:

- On a bare machine, it builds a cluster.
- On a machine that already runs Geass, it updates the cluster and keeps its data.
- It deletes a cluster only when you pass `--reinstall`.

The installer reads the environment variables in the first table, and takes the options in the second table. For the whole procedure,
see [Install](/docs/getting-started/install/). For updates, see [Upgrade Geass](/docs/operations/upgrading/).

| Variable | Default | Meaning |
| --- | --- | --- |
| `GEASS_ACME_EMAIL` | none | The contact address for the Let's Encrypt account of the cluster. The installer checks it at the first step: it must look like `name@host.tld`, with no spaces and at most 254 characters. Then the installer writes it to the `GeassCluster` as `spec.addons.certManager.acmeEmail`. Without it, Geass creates no certificate issuer, and no HTTPS certificate is issued. Let's Encrypt refuses placeholder domains such as `example.com`. [HTTPS certificates](/docs/getting-started/install/#https-certificates) |
| `GEASS_MULTI_SITE` | off | `1`, `true`, `yes`, or `on` builds a cluster whose nodes are at different providers: an encrypted WireGuard overlay instead of VXLAN. You set it at install time, because a later change means a restart of every node. [Multi-provider clusters](/docs/operations/multi-provider/) |
| `GEASS_NODE_EXTERNAL_IP` | none | The public IP address of this node. Required when `GEASS_MULTI_SITE` is on. It must be a valid IP. Workers join on it. With `GEASS_MESH=tailscale`, it is optional, and the default is the address that `tailscale ip -4` prints |
| `GEASS_MESH` | none | `wireguard` or `tailscale`. `wireguard` is the same as `GEASS_MULTI_SITE=1`, and it also records `spec.network.mesh`. That field turns on the checks of the mesh by the operator, and the installer then refuses an address that no other machine can use. `tailscale` joins a Tailscale or Headscale network and needs `GEASS_TAILSCALE_AUTH_KEY_FILE`. This machine must already be on the tailnet. The installer refuses `tailscale` together with `GEASS_MULTI_SITE=1`, and an additional server refuses it. [Mesh networking](/docs/operations/mesh-networking/) |
| `GEASS_TAILSCALE_AUTH_KEY_FILE` | none | With `GEASS_MESH=tailscale`: a file that holds the auth key that nodes join the tailnet with. The key is 8 to 256 letters, digits, dashes, and underscores. The installer writes the key to `/etc/rancher/k3s/vpn-auth` (mode 600) and stores it in the Secret `geass-mesh-auth`. The key is never logged |
| `GEASS_TAILSCALE_CONTROL_URL` | none | With `GEASS_MESH=tailscale`: an `https` address of a Headscale server. Empty means the Tailscale service itself |
| `GEASS_K3S_VERSION` | the current `stable` K3s | The full K3s release (`v1.36.5+k3s1`) to build a new cluster on. For a new cluster, it is the same as `--k3s-version`. When you update a cluster that exists, the installer ignores it and shows a warning. So a value left over from the install does not upgrade K3s. To upgrade K3s, pass `--k3s-version` |
| `GEASS_JOIN_SERVER` | none | The address of a server of a cluster, for example `https://10.0.0.5:6443`. With this variable, the installer does not build a cluster. It makes this machine an additional control plane server of that cluster. It is the same as `--join-server`. It also needs the K3s release that the cluster runs, in `GEASS_K3S_VERSION` or `--k3s-version`. See [High availability](/docs/operations/high-availability/#run-three-control-plane-servers) |
| `GEASS_JOIN_TOKEN` | none | The server token of the cluster, from `/var/lib/rancher/k3s/server/token` on its first server. Needed with `GEASS_JOIN_SERVER`. Pass it here, not as `--join-token`. Other users of the machine can read `--join-token` in the process list |
| `GEASS_ETCD_SNAPSHOTS` | off | `1`, `true`, `yes`, or `on`: the same as `--etcd-snapshots`. If you set a bucket (next row), copies are also turned on |
| `GEASS_ETCD_SNAPSHOTS_BUCKET` | none | The S3 bucket that receives copies of the state of the cluster. With this variable, the installer also records the bucket on the cluster object, stores the key, and takes a first copy immediately. Needs `_ACCESS_KEY` and `_SECRET_KEY`. [Disaster recovery](/docs/operations/disaster-recovery/#keep-copies-of-the-clusters-state) |
| `GEASS_ETCD_SNAPSHOTS_REGION`, `_ENDPOINT`, `_FOLDER`, `_INSECURE` | none | The region of the bucket. The address of an S3-compatible server that is not AWS, with no scheme. The folder in the bucket (`geass`). `1` to send over plain HTTP, for a network that is closed to everyone else |
| `GEASS_ETCD_SNAPSHOTS_ACCESS_KEY`, `_SECRET_KEY` | none | A key that can use that bucket and nothing else. Pass these values here, not as options |
| `GEASS_REINSTALL` | off | `1`, `true`, `yes`, or `on`: the same as `--reinstall` |
| `GEASS_YES` | off | `1`, `true`, `yes`, or `on`: the same as `--yes` |
| `GEASS_INSTALL_LOG` | `geass-install-<UTC timestamp>.log` in the current directory | Where the installer writes its log. If the installer cannot write to that path, it uses the system temp directory |
| `GEASS_INSTALL_TRACE` | off | `1`, `true`, `yes`, or `on` runs the downloaded K3s installer script with shell tracing (`sh -x`) |
| `GEASS_INSTALL_ROOT` | detected | A directory with installer assets. The installer binary embeds everything that it needs and never asks for an override. So this variable has no effect on a normal install |

| Option | Meaning |
| --- | --- |
| `--upgrade` (or the word `upgrade`) | Update the existing cluster. The default when the machine has one |
| `--k3s-version <release>` | For a new cluster: install this K3s. For an existing cluster: also upgrade the K3s of the control plane to it, and set `spec.k3s.version` so the operator upgrades the workers. One minor version at a time |
| `--allow-untested-k3s` | Allow a K3s outside the range that the add-ons of this release are known to run on |
| `--skip-snapshot` | Update without an etcd snapshot first |
| `--force` | Update, although the cluster is not ready or a Longhorn volume is degraded |
| `--etcd-snapshots` | For a new cluster, or a server that joins one: also keep copies of the state of the cluster in an S3 bucket of your own. Without this option, the installer asks on a terminal, and the answer is no unless you answer yes. See [Disaster recovery](/docs/operations/disaster-recovery/) |
| `etcd-snapshots enable`, `disable`, `status` | On an existing cluster: turn on, turn off, or show the upload of K3s snapshots to S3. `enable` restarts K3s, which makes the API server unavailable for about a minute |
| `--join-server <url>` | Do not build a cluster. Make this machine an additional control plane server of the cluster at this address. Needs the server token in `GEASS_JOIN_TOKEN` (or `--join-token`), and the K3s release that the cluster runs in `--k3s-version` (or `GEASS_K3S_VERSION`). See [High availability](/docs/operations/high-availability/#run-three-control-plane-servers) |
| `--reinstall` | Delete the cluster on this machine and build a new one. Asks for confirmation on the terminal |
| `--yes` | Do not ask for confirmation of `--reinstall` |
| `--version`, `--help` | Print the release of the installer, or the options |

When you pipe the script, put options after `-s --`: `curl -sfL .../install.sh \| sudo sh -s -- --force`.

The installer passes its whole environment to the K3s install script, so the `INSTALL_K3S_*` variables of K3s reach that script. Geass
itself sets `INSTALL_K3S_EXEC` to `server --cluster-init`. If a version was set, Geass also sets `INSTALL_K3S_VERSION`. Without
`GEASS_K3S_VERSION`, the installer sets no version, so K3s picks its default release. When the installer upgrades K3s on a running server,
it passes the arguments of that server, read from its systemd unit, to the K3s script.

The convenience script `scripts/install.sh` reads three variables of its own:

- `GEASS_REPO`, default `degoke/geass`.
- `GEASS_VERSION`, default `latest`.
- `GEASS_DOWNLOAD_BASE`, a mirror or a `file://` directory to download from instead.

The script downloads `geass-linux-amd64` or `geass-linux-arm64`, and the `SHA256SUMS` of the release, from the releases of that repository.
It checks the binary against the checksums, and runs the installer only if they match.

The installer does these steps, in order:

1. Checks the system: Linux, amd64 or arm64, a server with 2 GB of memory (at least 1700 MiB of `MemTotal`), 10 GiB free on `/`, and `GEASS_ACME_EMAIL` if you set it.
2. Opens ports 6443/tcp, 10250/tcp, and the overlay port (8472/udp, or 51820/udp for multi-site), if it finds an active `ufw`,
   `firewalld`, or `iptables`.
3. Removes an existing K3s installation, only when you passed `--reinstall`.
4. Installs the iSCSI initiator that Longhorn needs.
5. Installs K3s as a server with embedded etcd (`--cluster-init`).
6. Imports the operator image.
7. Applies the operator manifests.
8. Stores the K3s join token in the Secret `geass-token`.
9. Creates the `GeassCluster` named `default`. When `GEASS_ACME_EMAIL` is not set, the installer prints a warning at this last step,
   and creates the cluster without an `acmeEmail`.

:::note
If you run the installer on a machine that already runs Geass, the installer updates Geass. It does not apply your `GEASS_ACME_EMAIL`,
`GEASS_MULTI_SITE`, `GEASS_MESH`, or `GEASS_NODE_EXTERNAL_IP` again, because these variables describe a new cluster. To change a setting
of a working cluster, edit the `GeassCluster`.
:::

The installer writes these files on the server:

| Path | Content |
| --- | --- |
| `/etc/rancher/k3s/config.yaml` | K3s server settings: the kubelet log limits (described after this table), secrets encryption (`secrets-encryption: true`), and, for multi-site, the WireGuard backend, the external IP, and the certificate name. For a Tailscale mesh, the file has `vpn-auth-file`, the external IP, and the certificate name, and no flannel setting |
| `/etc/rancher/k3s/vpn-auth` | A Tailscale mesh only: the credentials that K3s joins the tailnet with, mode 600 |
| `/etc/rancher/k3s/k3s.yaml` | The admin kubeconfig that K3s writes |
| `/var/lib/rancher/k3s/server/node-token` | The join token, also copied to the Secret `geass-token` |

Geass sets `container-log-max-size=50Mi` and `container-log-max-files=5` on the kubelet of every node, so the recent logs of a service
stay on the node. Workers get the same kubelet settings, plus their labels and taints, when they join. Secrets encryption is a server
setting, so workers do not get it. A control plane server that you join with `--join-server` also gets it from the installer.

## Operator flags

The operator and the dashboard are one process, `/manager`. To run only one of them, start the process with `--controllers=false` or
`--dashboard=false`. See [Dashboard and controllers apart](/docs/operations/dashboard-only/). The installed Deployment
`geass-controller-manager` in `geass-system` runs both, with four flags:

```text title="Installed arguments"
--metrics-bind-address=:8443
--leader-elect
--health-probe-bind-address=:8086
--dashboard-bind-address=:8085
```

| Flag | Default | Meaning |
| --- | --- | --- |
| `--controllers` | `true` | Run the controllers: the reconcilers, the data migrations, and the checks that go with them. `--controllers=false` runs only the dashboard. The dashboard refuses to start unless the controllers of the same release have migrated the data of the cluster. It never takes the leader election lease, and it ignores `--leader-elect` |
| `--dashboard` | `true` | Serve the dashboard and API. `--dashboard=false` runs only the controllers, with nothing listening on the dashboard port. You cannot turn off both |
| `--dashboard-bind-address` | `:8085` | Where the dashboard and API listen (plain HTTP). `0` turns the dashboard off. With `--controllers=false`, `0` is refused, because nothing would be left to run |
| `--health-probe-bind-address` | `:8086` | Where `/healthz` and `/readyz` are served |
| `--metrics-bind-address` | `0` (off) | Where operator metrics are served. The installed manifest sets `:8443` |
| `--metrics-secure` | `true` | Serve metrics over HTTPS with Kubernetes authentication and authorization. `--metrics-secure=false` serves plain HTTP |
| `--metrics-cert-path`, `--metrics-cert-name`, `--metrics-cert-key` | none, `tls.crt`, `tls.key` | A certificate for the metrics endpoint. Without one, the operator generates a self-signed certificate |
| `--leader-elect` | `false` | Elect one active operator among replicas. The installed manifest turns it on. Ignored with `--controllers=false` |
| `--enable-http2` | `false` | Allow HTTP/2 on the metrics server. Off, to avoid the known HTTP/2 reset attacks |
| `--kubeconfig` | in-cluster | Path to a kubeconfig, to run outside the cluster. `KUBECONFIG` also works |
| `--webhook-cert-path`, `--webhook-cert-name`, `--webhook-cert-key` | none, `tls.crt`, `tls.key` | Scaffolding for a certificate for an admission webhook server. The operator registers no admission webhooks, so these flags do nothing |
| `--zap-devel` | `true` | Development logging: console format and debug level. Set `--zap-devel=false` for JSON and info level |
| `--zap-encoder`, `--zap-log-level`, `--zap-stacktrace-level`, `--zap-time-encoding` | per `--zap-devel` | Log format, verbosity, stack trace threshold, and timestamp format |

To change a flag, edit the Deployment. The container is the first and only one, called `manager`:

```bash title="Turn on info-level JSON logs"
kubectl -n geass-system patch deployment geass-controller-manager --type json \
  -p '[{"op":"add","path":"/spec/template/spec/containers/0/args/-","value":"--zap-devel=false"}]'
kubectl -n geass-system rollout status deployment/geass-controller-manager
```

:::note
If you apply the operator manifest again, for example when you reinstall, it overwrites the changes that you made to the Deployment. Keep
your edits in your own notes or automation.
:::

The pod itself runs on a control plane node, as a non-root user with a read-only root filesystem. It has a request of 10m CPU and 128Mi
memory, and a limit of 500m and 1Gi.

### Operator metrics

The metrics endpoint serves the standard controller-runtime series: reconcile counts and durations, work queue depth, and requests to the
Kubernetes API. It also serves these Geass series. When the monitoring add-on is on, Geass scrapes them and defines alert rules on them.
See [Security](/docs/operations/security/).

| Series | Labels | Meaning |
| --- | --- | --- |
| `geass_apiserver_client_request_duration_seconds` | `verb`, `resource`, `code` | How long the Kubernetes API server took to answer the operator (a histogram) |
| `geass_apiserver_client_requests_in_flight` | none | Requests sent to the API server that have no answer yet |
| `geass_reconcile_not_ready_total` | `reason` | Reconciles that ended with a resource not ready |
| `geass_provision_duration_seconds` | `phase` | How long a deploy of a service, database, or bucket took, from start to outcome (a histogram) |
| `geass_status_writes_total` | `kind`, `result` | Status updates that the controllers considered: `written`, `skipped` (nothing changed), or `failed` |

## Operator environment variables

The operator process reads these variables. So set them on the Deployment, and then restart the Deployment.

```bash title="Set an operator variable"
kubectl -n geass-system set env deployment/geass-controller-manager GEASS_PROMETHEUS_URL=http://prometheus.example.internal:9090
```

| Variable | Default | Meaning |
| --- | --- | --- |
| `GEASS_DASHBOARD_USERS` | none | Dashboard users, as a JSON array or as `username:password:role` entries separated by commas. Used only while the Secret has no users |
| `GEASS_ALLOW_LOOPBACK` | `false` | `true` lets **Settings** → **Sign-in** use an identity provider on the machine that the operator runs on, over plain `http`. An example is Better Auth on `localhost:3000`. This is for development, with the operator on your own host. `make dashboard-backend` sets it. Leave it off in a cluster, where that address is the pod of the operator itself. Link-local addresses stay refused. See [Sign in with an identity provider](/docs/guides/external-sign-in/#try-it-on-your-own-machine) |
| `GEASS_DASHBOARD_PASSWORD` | none | A single admin password. Used only if no users are set. `GEASS_DASHBOARD_USERNAME` names the user |
| `GEASS_DASHBOARD_USERNAME` | `admin` | The name of the user created from `GEASS_DASHBOARD_PASSWORD` |
| `GEASS_DASHBOARD_SESSION_KEY` | generated | The key that signs session cookies, at least 16 bytes, or `hex:` followed by hex. Overrides the key in the Secret |
| `GEASS_ALERTMANAGER_URL` | `http://geass-kube-prometheus-stac-alertmanager.monitoring.svc:9093` | Where the dashboard asks Alertmanager what is firing, and sends a test through it, on the **Alerts** page. To use an Alertmanager other than the one of the monitoring add-on, set this variable |
| `GEASS_PROMETHEUS_URL` | `http://geass-kube-prometheus-stac-prometheus.monitoring.svc:9090` | Where the dashboard queries metrics, unless the platform configuration sets `prometheusURL` |
| `GEASS_LOGS_URL` | `http://geass-victorialogs.geass-logging.svc:9428` | Where the dashboard reads stored log history (the VictoriaLogs server of the logging add-on) |
| `GEASS_WEBHOOKS_ALLOW_PRIVATE_NETWORKS` | `false` | `true` lets project webhooks reach private network addresses (RFC 1918 and shared address space). It also lets you save an alert channel with a Slack or mail host on such an address. Loopback and link-local stay blocked |
| `GEASS_GITHUB_APP_ID`, `GEASS_GITHUB_APP_CLIENT_ID`, `GEASS_GITHUB_APP_CLIENT_SECRET`, `GEASS_GITHUB_APP_SLUG`, `GEASS_GITHUB_APP_PRIVATE_KEY`, `GEASS_GITHUB_WEBHOOK_SECRET` | none | GitHub App credentials. Used only when the platform configuration has no `githubAppRef`, that is, when nothing was saved in **Settings** → **Connectors** |
| `GEASS_PUBLIC_URL` | none | The public address of the dashboard for the GitHub App. After you set a domain, the dashboard URL overrides it |
| `GOMEMLIMIT` | `800MiB` in the installed Deployment | The soft memory limit of the Go runtime. It is below the 1Gi limit of the container. So near the limit, the runtime collects harder, and the container does not get killed for the limit. See [Cluster size](/docs/operations/sizing/) |

The default Prometheus address depends on the monitoring add-on. A `prometheusURL` set on the platform configuration takes precedence over
`GEASS_PROMETHEUS_URL`. `GEASS_PROMETHEUS_URL` takes precedence over the default. Standard Kubernetes variables, for example `KUBECONFIG`,
also apply, because the operator uses controller-runtime.

A dashboard-only process outside the cluster (`--controllers=false`) cannot resolve the in-cluster defaults of the three `*_URL`
variables. So that process must set the variables for the features that it shows. See
[Dashboard and controllers apart](/docs/operations/dashboard-only/#environment).

## The dashboard auth Secret

Dashboard sign-in reads the Secret `geass-dashboard-auth` in `geass-system` on every request, so edits apply immediately. The sample is
`config/samples/geass_dashboard_auth.yaml`.

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
| `users` | A JSON array of `{"username","password","role"}`. Roles are `admin` and `viewer`. `observer` is a token role, not a user role. Geass replaces plain passwords with bcrypt hashes the first time that it reads them |
| `session-key` | The key that signs sessions, as `hex:` plus 64 hex characters. Generated on first use. To sign everyone out, delete it |
| `session-epochs` | A JSON object of counters for each user, which Geass increments on sign-out |
| `login-lockouts` | A JSON object of failed sign-in counters and lock times, for each configured username. Expired records are removed, and at most 256 records are kept |

Geass writes the last three keys. You need only `users`. For the full details, see [Users and API tokens](/docs/guides/users-and-tokens/).

## Command line

The [`geass` command line](/docs/guides/command-line/) runs on your own machine, not in the cluster. It combines a flag, an environment
variable, and the saved login, from highest to lowest precedence. Every command also takes `--json` and `--yes`. For the full list, see
the [CLI reference](/docs/reference/cli/).

| Variable | Flag | Meaning |
| --- | --- | --- |
| `GEASS_URL` | `--url` | The address of the dashboard, for example `https://geass.example.com` |
| `GEASS_TOKEN` | `--token` | An API token. `geass login` also takes the token to save from this variable |
| `GEASS_PROJECT` | `-p`, `--project` | The default project, by its `proj-...` ID |
| `GEASS_ENVIRONMENT` | `-e`, `--environment` | The default environment, for example `production` |
| `GEASS_CONTEXT` | `--context` | Which saved login to use |
| `GEASS_CONFIG` | none | The path of the login file. The default is `config.json` in the `geass` folder of your user configuration directory (`~/.config/geass/config.json` on Linux) |

`geass login` saves what it signed in to as a named login, and makes it the current login. The name of the login is the host name of the
dashboard, unless you pass `--name`. The file is JSON. The command creates the folder with mode `0700` and the file with mode `0600`. The
token is stored in plain text:

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

`geass config get-contexts` lists the saved logins, without tokens. `geass config use-context NAME` switches the login. `geass config set-context
--project ... --environment ...` sets the default project and environment for a login. `geass logout` clears only the saved token.

The installer script for the command line, `scripts/install-cli.sh`, is separate from the cluster installer. It downloads
`geass-cli-<os>-<arch>` (Linux and macOS, amd64 and arm64) from a release, and saves it as `geass`. It reads four variables:

| Variable | Default | Meaning |
| --- | --- | --- |
| `GEASS_VERSION` | `latest` | A release tag to install |
| `GEASS_INSTALL_DIR` | `/usr/local/bin` when you can write to it, otherwise `~/.local/bin` | Where to put the binary |
| `GEASS_REPO` | `degoke/geass` | The GitHub repository whose releases to download, for a fork |
| `GEASS_DOWNLOAD_BASE` | the release on GitHub | Where to download the release files from instead: a mirror, or a directory as `file:///path` that holds the build and `SHA256SUMS` |

On Windows, download `geass-cli-windows-amd64.exe` from the release page. The MCP server (`geass mcp`) has no configuration of its own. It
uses the same login, flags, and variables, plus `--read-only`, `--allow-delete`, and `--listen`. See [MCP server](/docs/guides/mcp-server/).

## Platform settings

Platform behavior is stored in two resources that Geass creates for you. You can edit them with `kubectl`. Most of their fields also have
a screen in **Settings**.

### GeassPlatformConfig

There is one, named `platform`, in `geass-system`. For the schema, see [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/).

| Field | Meaning | Edited in the dashboard at |
| --- | --- | --- |
| `rootDomain` | Your apex domain, for example `example.com` | **Settings** → **Domain** |
| `dashboardURL` | The public HTTPS address of the dashboard: `https://<subdomain>.<rootDomain>` | **Settings** → **Domain** (**Subdomain**, default `geass`) |
| `dashboardExposure` | `ingress` (the default) or `cloudflare-tunnel` | **Settings** → **Domain** (**Publish via**) |
| `tunnelCNAMETarget` | The `<id>.cfargotunnel.com` name of the tunnel. Geass sets it when Geass manages the tunnel | **Settings** → **Domain** |
| `cloudflareConnectionRef`, `cloudflareZoneID` | The Cloudflare Secret and DNS zone | **Settings** → **Connectors** → Cloudflare |
| `githubAppRef` | The Secret that holds the GitHub App | **Settings** → **Connectors** → GitHub |
| `prometheusURL` | Where to query metrics | `kubectl` only |
| `observability.external` | Metrics and log stores outside the cluster. Prometheus remote-writes to one, and Fluent Bit sends to the other, each with a tenant and credentials from a Secret. The dashboard reads metrics and log history from them. See [Send metrics and logs outside the cluster](/docs/guides/observability/#send-metrics-and-logs-outside-the-cluster) | `kubectl` only |
| `defaultClusterRef` | The cluster that new projects use when there are several | `kubectl` only |
| `registry.repository`, `registry.credentialRef` | The image destination and login for repository builds when a service sets none. The nodes also pull the built image with this login | The REST API (`/api/settings/registry/save`) or `kubectl`. The dashboard has no form for it yet |
| `usage.enabled` | Meter what each organization reserves, hour by hour, for billing. See [Usage by organization](/docs/guides/usage-and-capacity/#usage-by-organization-for-billing) | `kubectl` only |
| `tenancy.sandbox.runtimeClassName`, `.skipOnReservedNodes`, `.databaseRestarts` | Run the services, schedules, builds, and database servers of organizations under a sandboxed runtime (gVisor, Kata). Also say when a running database is restarted into that runtime. See [Sandboxed runtime for organizations](/docs/operations/sandboxed-runtime/) | `kubectl` only |
| `tenancy.egress.mail`, `.allowedPorts`, `.blockedPorts`, `.allow` | Close mail and other ports to addresses outside the cluster for the pods of organizations. See [Outbound limits](/docs/guides/outbound-limits/) | `kubectl` only |
| `tenancy.mail.relay` (`enabled`, `upstream`, `limits`, `allowedSenderDomains`, `requireSenderDomain`, `deniedRecipientDomains`) | Run a mail relay that gives each organization a login and limits, and sends through your mail service. See [Mail relay](/docs/guides/mail-relay/) | `kubectl` only |
| `tenancy.registry.policy` | Never give the builds of organizations the registry login of the platform, and keep the images of each organization apart. See [Registry isolation for organizations](/docs/operations/registry-isolation/) | `kubectl` only |
| `defaultDomain`, `traefikAccessLogs` | Present in the schema. No code reads them | Not used |

:::caution
Set `build.registry.repository` on each service, with a tag, instead of the platform default. For repository builds, Geass passes the
registry value to Kaniko as the destination image, exactly as written. Geass does not add a service name or a tag. So every service that
uses the platform default pushes to the same image reference. When neither value is set, a repository service waits with
`no container registry is set for builds`: Geass has no registry of its own. See
[GitHub and builds](/docs/guides/github-and-builds/#set-the-registry).
:::

Changes apply as soon as the controller sees them. To verify a new dashboard domain, Geass polls every 5 seconds for 15 minutes, and then
every 5 minutes. So a late DNS fix still heals. The status shows `DashboardDomainReady` and `CloudflareReady`.

### GeassCluster

The installer creates one, named `default`. See [GeassCluster](/docs/reference/resources/geass-cluster/).

| Field | Default | Meaning |
| --- | --- | --- |
| `spec.addons.certManager.enabled` | `true` | Install cert-manager. When `false`, Geass removes the cert-manager that it installed, and manages no issuer |
| `spec.addons.certManager.acmeEmail` | empty (the installer sets it from `GEASS_ACME_EMAIL`) | The Let's Encrypt contact address, at most 254 characters, in the form `name@host.tld`. When it is set, Geass creates and keeps the `letsencrypt-prod` ClusterIssuer that every HTTPS Ingress asks for. Empty means that Geass creates no issuer |
| `spec.addons.monitoring.enabled` | `true` | Install the monitoring stack (Prometheus and Alertmanager). While it is on, Geass also scrapes the metrics of the operator itself. Geass also adds alert rules for the health of the operator and for what runs in the cluster. Alerts need this add-on. See [Alerts](/docs/guides/alerts/) |
| `spec.addons.monitoring.profile` | `lite` | `lite` or `full` footprint |
| `spec.addons.longhorn.enabled` | `false` (the installer sets `true`) | Install Longhorn for database volumes. Longhorn needs iSCSI on every node |
| `spec.addons.logging.enabled` | `false` (the installer sets `true`) | Install log collection and the VictoriaLogs store |
| `spec.addons.logging.retentionDays` | `7` (1 to 90) | How long Geass keeps stored logs |
| `spec.addons.serverless.enabled` | `false` (the installer does not set it) | Install the scale-to-zero add-on, so services can sleep when idle: KEDA and its HTTP add-on (charts `geass-keda` and `geass-keda-http`, namespace `keda`). Geass also creates a K3s `HelmChartConfig` named `traefik` that lets Traefik route to the proxy of the add-on. If Traefik already has a `HelmChartConfig`, Geass does not change it, and logs what to add. If you turn the add-on off, services stop sleeping, and the sleeping services wake up. Geass never uninstalls KEDA. See [Scale to zero](/docs/guides/scale-to-zero/) |
| `spec.network.multiSite` | `false` | Nodes are at different providers. The installer sets it. A later change does not rebuild nodes |
| `spec.network.controlPlaneFirewall` | none | The cloud firewall in front of the control plane, so that Geass can let the machines that it creates reach the control plane: `connectionRef`, `target`, and `region`. AWS security groups and Hetzner firewalls only. See [Node pools](/docs/guides/node-pools/#let-geass-open-the-control-plane) |
| `spec.defaultRegion` | empty | The region for projects that do not choose one. Edited in **Settings** → **Usage** (**Default region**) |
| `spec.serverURL` | The address of the control plane itself on its default route, for a single-site install | The address that workers join. The installer fills it in with the address that its default route uses, which is a private address on most cloud servers. A multi-site install writes the public address. A loopback address is never used: workers join on the address of the control plane node itself instead. Set it only to use a different address. See [Add servers](/docs/getting-started/add-servers/#set-the-address-workers-join) |
| `spec.tokenSecretRef`, `spec.version` | `geass-token` in `geass-system`, and `v1` | The Secret with the join token, and a label that is not a K3s version |

The dashboard has no switch for the serverless add-on. Turn it on with `kubectl`:

```bash title="Turn on the scale-to-zero add-on"
kubectl -n geass-system patch geasscluster default --type merge \
  -p '{"spec":{"addons":{"serverless":{"enabled":true}}}}'
```

The add-on takes a few minutes to come up. Until it is up, a service that asks to sleep continues to run, and its `ScaleToZero` condition
says why.

The `GeassCluster` also carries a `ClusterIssuerReady` condition. This condition says if the `letsencrypt-prod` issuer exists and works. It
is informational, and it does not change the `Ready` condition of the cluster. Geass removes it when cert-manager is turned off. Its
reasons are `IssuerReady`, `ACMEEmailMissing`, `WaitingForCertManager`, `IssuerRegistering`, `IssuerNotReady`, `ExternallyManaged`,
`ManagedByAnotherCluster`, `IssuerApplyFailed`, and `IssuerAccessDenied`. While the HTTPS of the dashboard domain is unreachable, Geass
adds the same message to the status of that domain (`PendingHTTPS`). For an explanation of each reason, see
[HTTPS certificates](/docs/getting-started/install/#https-certificates).

```bash title="Read the issuer condition"
kubectl -n geass-system get geasscluster default \
  -o jsonpath='{.status.conditions[?(@.type=="ClusterIssuerReady")].reason}{": "}{.status.conditions[?(@.type=="ClusterIssuerReady")].message}{"\n"}'
```

## Defaults and limits

| What | Value |
| --- | --- |
| Dashboard session | 12 hours, not extended. Through an identity provider: 5 to 720 minutes, an hour by default |
| Identity provider sign-in | A sign-in has 10 minutes to finish. A test is good for 30 minutes. Every request to the provider times out after 10 seconds. A logout token must be less than 5 minutes old |
| Sign-in lockout | 5 failures for a username from one address in a minute lock the username for that address. 25 from all addresses lock it for everyone. Where Geass cannot tell addresses apart, 5 lock it for everyone. Each lock lasts a minute. An address with 20 failures in a minute, for any usernames, is refused for a minute |
| API tokens and deploy hooks | At most 600 together, and at most 100 for one organization. Names up to 60 characters. Lifetime 1 to 3650 days, or none |
| Deploy hooks | A hook answers one time every 30 seconds. At most one hook for each service |
| Command-line sign-in | A request lives 10 minutes. The CLI polls every 5 seconds. At most 100 pending, and 10 started each minute from each address. The token that it gives lasts 30 days, unless the admin selects 7 or 90 |
| `/api/v1` request body | At most 2 MiB |
| Webhooks | At most 20 for each project. 3 delivery attempts. 10 second timeout. Geass keeps the last 20 deliveries |
| Pull request environments | At most 25 open. Default 5 |
| New service size | 0.1 CPU and 128 MB. Allowed CPU: 100m, 250m, 500m, 1, and 2. Allowed memory: 128Mi to 4Gi |
| New database size | 250m CPU and 512Mi memory. 10Gi volume (1Gi for SQLite) |
| New object storage server size | 250m CPU and 512Mi memory for SeaweedFS, 100m and 256Mi for Garage. 10Gi volume, at least 1Gi. The volume can be raised later, and is never lowered |
| Autoscaling | Target CPU 50 percent by default |
| Replica count | 0 to 1000 |
| Sleep when idle | Idle time 30 seconds to 24 hours, 300 seconds by default. 1 to 100 copies when awake, 3 by default |
| Function schedules | At most 8 for each function, in UTC cron syntax. A run times out after 120 seconds. A run is skipped if it cannot start within 5 minutes. A run never overlaps the previous run |
| Function code | At most 256 KiB |
| Compliance | At most 8 required tags for each project. At most 16 tags for each node |
| Environments | At most 100 for each project |
| Backup retention | 7 days by default, 1 to 365. The backups of a deleted database stay for 7 days |
| Deploy history | The last 12 deploys for each service |
| Stored logs | 7 days by default (1 to 90) |
| Terminal session | 5 minutes |
| Domain verification | 15 minutes of polling every 5 seconds |

## Live settings and settings that need a deploy

For a service, database, or bucket, Geass applies some edits immediately, and holds the rest until you deploy.

| Applied immediately | Waits for a deploy |
| --- | --- |
| The replica count, autoscaling, and sleep-when-idle settings of a service. Host, path, and TLS. Metrics scraping. Log retention. Failure threshold, and the pause and rollback flags. Repository watch patterns | The source of a service (including the code, mode, and schedules of a function), port, variables and secrets, command and arguments, resources, probes, and build settings |
| The backup settings of a database | The version, volume size, CPU, and memory of a database |
| None for buckets | Every bucket setting |

Variables and secrets always need a deploy. See [Projects and environments](/docs/guides/projects-and-environments/).

:::note
One of the live settings does nothing yet. Geass accepts and saves the spec field `logs.retentionHours`, but nothing enforces the log
retention. The same is true of `build.argsFrom`: builds take no build arguments from Secrets.
:::

## Versions Geass pins

Geass installs these add-ons and images at fixed versions, through HelmChart resources that the Helm controller of K3s installs in
`kube-system`. You cannot select other versions from the dashboard.

| Component | Version | Namespace |
| --- | --- | --- |
| cert-manager (chart) | v1.17.2 | `cert-manager` |
| kube-prometheus-stack (chart) | 69.8.2 | `monitoring` |
| Longhorn (chart) | 1.12.1 | `longhorn-system` |
| CloudNativePG operator (chart) | 0.23.2 | `cnpg-system` |
| MOCO, the MySQL operator for high availability (chart) | 0.27.0 | `moco-system` |
| mariadb-operator, which runs MariaDB databases (charts `mariadb-operator-crds` and `mariadb-operator`) | 26.10.1 | `mariadb-system` |
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

Postgres runs the `ghcr.io/cloudnative-pg/postgresql` image at the major version in the `version` field of the database (default 16).

## Secrets Geass keeps in `geass-system`

| Secret | Holds |
| --- | --- |
| `geass-dashboard-auth` | Dashboard users and session state. This includes when each person who signs in through an identity provider last signed out (`sso-revoked`) |
| `platform-auth` | The client secret of the identity provider, written by **Settings** → **Sign-in** |
| `geass-api-tokens` | API token records (hashes) |
| `geass-token` | The K3s join token, read when a worker joins |
| `platform-github-app` | The GitHub App credentials |
| `geass-alert-channels` | The Slack webhook URL of each alert channel, under the id of the channel |
| `geass-alert-smtp` | The password of the alert mail server |
| `platform-cloudflare`, `geass-cloudflared-token` | The Cloudflare API token, and the tunnel connector token |
| `<name>-credentials` | An AWS or PlanetScale connection |
| `geass-backup-storage-<bucket>` | The key for a backup bucket |
| `<project>-webhooks`, `<project>-shared-secrets` | Webhook signing secrets, and secret shared variables |
| `<app>-secrets` | The secrets of a service |
| `<project>-github`, `<project>-github-token` | The GitHub link of a project. `<project>-github` is a connection resource, and the token is its Secret |

The credentials of the metrics and log stores outside the cluster stay in the Secret that `observability.external` names in `geass-system`. Geass copies the keys that they use into `geass-external-metrics` in `monitoring` and `geass-external-logs` in `geass-logging`. Prometheus and Fluent Bit read these copies. Geass removes the copies when you remove the setting.

Geass makes the `AlertmanagerConfig` `geass-alerts` in `geass-system` from the alert channels in the platform configuration. See [Alerts](/docs/guides/alerts/#how-it-works). Saved templates are ConfigMaps named `template-<id>` with the label `geass.dev/template=true`. The schedule token of a function is the Secret
`<app>-trigger`. This Secret is in the environment namespace of the service, not in `geass-system`.

## Ports

| Port | Component | Protocol |
| --- | --- | --- |
| 8085 | Dashboard and API (Service `geass-geass-dashboard`) | HTTP |
| 8086 | Operator health probes | HTTP |
| 8443 | Operator metrics (Service `geass-controller-manager-metrics-service`) | HTTPS |
| 8080 | The scale-to-zero proxy (`keda-add-ons-http-interceptor-proxy` in `keda`), in the cluster only, when that add-on is on | HTTP |
| 6443 | Kubernetes API | HTTPS |
| 10250 | kubelet | HTTPS |
| 8472 or 51820 | Overlay network (VXLAN, or WireGuard for multi-site). A Tailscale mesh opens no port for the cluster | UDP |

## Troubleshooting

**The operator does not pick up a flag.** Make sure that the argument is on the `manager` container (`kubectl -n geass-system get deployment geass-controller-manager -o yaml`). Also make sure that the pod
restarted (`rollout status`).

**A dashboard user change has no effect.** Geass uses environment users only while the Secret has none. Edit the Secret. After the Secret has a
`users` key, the Secret takes precedence.

**Metrics are empty.** Check `spec.prometheusURL` on the platform configuration and `GEASS_PROMETHEUS_URL`. Then make sure that the monitoring add-on is turned on, and that
Prometheus answers at that address. See [Observability](/docs/guides/observability/).

**Log history says "not set up".** The logging add-on is off in `spec.addons.logging.enabled`. The installer turns it on for the clusters that it builds.

**A multi-site install stops with "needs this node's public address".** Set `GEASS_NODE_EXTERNAL_IP` to the public IP of the node.

**A service never sleeps after you turn on sleep when idle.** The `ScaleToZero` condition on the service says why. The usual causes are these:

- `spec.addons.serverless.enabled` is not `true` on the `GeassCluster`.
- The add-on in `keda` is still coming up.
- The service has no public host, or it still has CPU autoscaling on.

See [Scale to zero](/docs/guides/scale-to-zero/).

**`geass` says "not signed in".** Geass found no saved login, `GEASS_URL`, or `GEASS_TOKEN`. Run `geass login --url https://geass.example.com`, or make sure that
`GEASS_CONFIG` points at the file that you expect.

## Related

- [Install](/docs/getting-started/install/)
- [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/)
- [GeassCluster](/docs/reference/resources/geass-cluster/)
- [Users and API tokens](/docs/guides/users-and-tokens/)
- [Command line](/docs/guides/command-line/) and [CLI reference](/docs/reference/cli/)
- [Security](/docs/operations/security/)
