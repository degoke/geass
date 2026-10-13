---
title: "Requirements"
description: "Hardware, operating system, ports, DNS and optional accounts you need before installing Geass on a VPS or bare-metal server."
---

Geass installs on one Linux server and grows by adding more. This page lists what that server needs, which ports must be open and what the installer opens for you, and the optional accounts (Cloudflare, GitHub, AWS, PlanetScale) that unlock specific features. You need none of those accounts to install Geass or to deploy your first app.

## Summary

| Requirement | Minimum | Checked by the installer |
| --- | --- | --- |
| CPU architecture | `amd64` (x86_64) or `arm64` (aarch64) | Yes |
| Operating system | Linux | Yes (Linux only, no distribution check) |
| Memory | 2048 MiB total | Yes |
| Disk | 10 GiB free on the filesystem that holds `/` | Yes |
| Privileges | root, or a user that can run `sudo` | No (the install fails when it cannot write to `/etc`) |
| Network | Outbound HTTPS to the hosts listed below | No |
| Tools on the server | `curl`, `sh`, `awk`, `df` | No |

Release builds exist for `linux/amd64` and `linux/arm64`. The `install.sh` script picks the right one from `uname -m` and stops with `unsupported architecture` on anything else.

## Memory and disk

The memory check reads `MemTotal` from `/proc/meminfo` and requires at least 2048 MiB. The kernel reports a little less than the size your provider advertises, so a plan sold as "2 GB" can be rejected with `insufficient memory`. Check before you install:

```bash
grep MemTotal /proc/meminfo
df -h /
```

The disk check measures free space on `/` (`df --output=avail -k /`) and requires 10 GiB.

These numbers are the floor for the installer, not a sizing guide. The installer also turns on cert-manager, monitoring (Prometheus, `lite` profile), Longhorn storage and log collection. They run on the same machine and reserve CPU and memory before you deploy anything. Metrics and logs use disk too: Prometheus keeps 15 days of data up to 4 GB on a 5 Gi volume, and the log store keeps 7 days by default up to 4 GiB. A new service asks for 0.1 CPU and 128 MB by default.

Geass refuses to deploy a resource that does not fit in the free capacity of the nodes it may run on. The message starts with `This service needs about ... CPU and ... memory` and ends with `Scale up the cluster before creating it`. Open **Usage** in the left rail to see requested and allocatable CPU and memory per node.

## Operating system

The installer checks that it runs on Linux and nothing else about the distribution. It then installs [K3s](https://k3s.io/) with K3s's own installer script, so the machine must be one that K3s supports.

Longhorn, the storage engine Geass uses for database volumes, needs the iSCSI initiator on every node. The installer installs it for you with `apt-get`, `dnf`, `yum` or `zypper`, and enables the `iscsid` service. If the machine has none of those package managers, or the package cannot be downloaded, the installer logs the problem and carries on. Database volumes then cannot be provisioned until you install the initiator yourself (`open-iscsi` on Debian and Ubuntu, `iscsi-initiator-utils` on Red Hat family systems).

One feature depends on the Kubernetes version. Compliance node tags use an admission policy (`ValidatingAdmissionPolicy`), which needs Kubernetes 1.30 or later. The installer lets K3s choose its `stable` release, and `sudo k3s kubectl version` shows which one you got. On an older Kubernetes, for example one you pinned with `INSTALL_K3S_VERSION`, everything else about compliance tags works and the operator logs that pods created outside Geass are not checked. See [Compliance node tags](/docs/guides/compliance-tags/).

A multi-site cluster (nodes at different providers) also needs a kernel that can create WireGuard interfaces: built in from Linux 5.6, or the `wireguard` package on older kernels. The installer tests this by creating and removing a throwaway interface named `geass-wgcheck`, which needs the `ip` command.

## Root access

Run the installer as root. The one-line command in [Install Geass](/docs/getting-started/install/) uses `sudo`. The installer does not check for root: without it, the install fails when it first writes to `/etc/rancher/k3s`.

## Outbound network access

The server needs to reach these hosts over HTTPS during and after installation. There is no offline install mode.

| Host | Used for |
| --- | --- |
| `github.com` and the release asset hosts it redirects to | Downloading the Geass installer, and K3s releases |
| `get.k3s.io`, `update.k3s.io` | K3s installer script and the K3s release channel |
| `charts.jetstack.io`, `prometheus-community.github.io`, `charts.longhorn.io`, `victoriametrics.github.io`, `fluent.github.io` | Helm charts for the add-ons the installer enables |
| `acme-v02.api.letsencrypt.org` | cert-manager registers the cluster's ACME account and requests certificates. Only once the `letsencrypt-prod` issuer exists, which needs `GEASS_ACME_EMAIL` |
| `cloudnative-pg.github.io`, `charts.min.io`, `charts.bitnami.com` | Charts installed on demand: PostgreSQL (CloudNativePG), in-cluster object storage (MinIO), MySQL and Redis |
| `kedacore.github.io` | The charts for the serverless add-on (KEDA and its HTTP add-on). Only if you turn it on, to let services sleep when idle |
| Container registries that those charts and your own images use | Pulling images |
| `api.github.com`, `api.cloudflare.com`, AWS (S3 and IAM), `api.planetscale.com` | Only for the connectors you set up |
| `api.ipify.org`, then `ifconfig.me` | Only when Geass needs the server's public IP for dashboard DNS and no node reports one |

New workers need to reach `get.k3s.io` and the control plane's API (port 6443) too.

## Ports and firewall

Open these between the machines, in the provider's firewall or security group and in any host firewall.

| Port | Where | Purpose | Opened by the installer |
| --- | --- | --- | --- |
| 22/tcp | From you to every server, and from the control plane to each new worker | SSH. Geass joins workers over SSH | No |
| 80/tcp and 443/tcp | From the internet to the nodes that serve web traffic | The ingress controller (Traefik, which K3s installs) for your services and the dashboard domain. Let's Encrypt also needs 80/tcp to validate a hostname before it issues a certificate | No |
| 6443/tcp | To the control plane, from every worker. In a multi-site cluster, from every node including the control plane itself | Kubernetes API | Yes, on the control plane |
| 10250/tcp | Between nodes | kubelet, used for container metrics | Yes, on the control plane |
| 8472/udp | Between nodes | VXLAN overlay network (default network mode) | Yes, on the control plane |
| 51820/udp | Between every pair of nodes | WireGuard overlay network, used instead of 8472 in a multi-site cluster | Yes, on the control plane |

### What the installer opens

The installer looks for an active host firewall in this order: `ufw`, `firewalld`, then `iptables` (active when the `INPUT` chain has a `DROP` policy or any rule). For the first one it finds, it opens 6443/tcp, 10250/tcp and the overlay port (8472/udp, or 51820/udp for a multi-site cluster):

- `ufw`: `ufw allow <port>/<protocol>`.
- `firewalld`: `firewall-cmd --permanent --add-port=...`, then `firewall-cmd --reload`.
- `iptables`: meant to be `iptables -A INPUT -p <protocol> --dport <port> -j ACCEPT`, skipped when the rule already exists. See the second caution below: at present it skips every rule that is missing.

If it finds no active firewall it prints a warning that starts `No active host firewall detected; skipping port configuration` and lists the ports to open yourself if you add a firewall later.

:::caution
The installer opens only the three ports above, and only on the control plane. It does not open 80 or 443, so your services are unreachable from the internet until you open them. It does not change a provider firewall or security group. It does not configure the firewall of workers. The only exception is a multi-site cluster, where joining a worker opens 51820/udp and 10250/tcp, and only with `ufw` or `firewalld`.

The rules it adds accept traffic from any source. If your server has a public address, restrict 6443 and 10250 to your other nodes in the provider's firewall. An `iptables` rule is appended to the end of the `INPUT` chain, so a `REJECT` or `DROP` rule earlier in the chain still wins, and the rule is not saved across reboots.
:::

:::caution
When the only active firewall is plain `iptables`, the installer does not open the ports. Its test for an existing rule runs `iptables -C` and reads the reply `Bad rule (does a matching rule exist in that chain?)` as "the rule is already there". `iptables -C` prints that reply when the rule is missing, so the installer logs `iptables rule already present` and adds nothing. Add the rules yourself before you join workers (use 51820/udp instead of 8472/udp for a multi-site cluster):

```bash
sudo iptables -A INPUT -p tcp --dport 6443 -j ACCEPT
sudo iptables -A INPUT -p tcp --dport 10250 -j ACCEPT
sudo iptables -A INPUT -p udp --dport 8472 -j ACCEPT
```

These rules are lost on reboot unless you save them with your distribution's tooling. With `ufw` or `firewalld` the installer works as described.
:::

## DNS

You do not need a domain to install Geass, sign in or run a service. You need one to publish services on a hostname, to serve the dashboard over HTTPS, and to register the GitHub App. For HTTPS certificates you also need a real email address, passed to the installer as `GEASS_ACME_EMAIL`, and 80/tcp and 443/tcp open: see [HTTPS certificates](/docs/getting-started/install/#https-certificates).

- **Dashboard.** The dashboard host is `geass.` plus your root domain, for example `geass.example.com`. You can pick another subdomain under **Settings → Domain**. Point an `A` record at the server's public IP, or let Geass create the record when Cloudflare is connected.
- **Services.** Each service takes a hostname that you enter in the service's **Settings** tab, under **Networking**, in **Custom domain**. Point an `A` record at the public IP of a node. With Cloudflare connected, Geass can assign hostnames itself.

See [Connect a domain](/docs/getting-started/connect-domain/) and [Domains and TLS](/docs/guides/domains-and-tls/).

## Optional accounts

| Account | Needed for | What Geass needs from it |
| --- | --- | --- |
| Cloudflare | Automatic DNS records for the dashboard and services, and Cloudflare Tunnel as a way to publish the dashboard without opening ports | An API token (not the Global API Key) with Account → Cloudflare Tunnel → Edit, Zone → DNS → Edit and Zone → Zone → Read, for your domain |
| GitHub | Deploying a repository, push-to-deploy and pull request environments | A GitHub account or organization where Geass registers a GitHub App. The dashboard needs a public domain first, because GitHub calls back to it. Built images are pushed to a container registry that the cluster can reach |
| AWS | S3 buckets for your services, backups of Postgres, MySQL and SQLite databases, and attaching AWS RDS databases | An access key. Geass creates and deletes S3 buckets and objects, and creates and deletes a scoped IAM user with an inline policy and access keys for each bucket |
| PlanetScale | PlanetScale databases | A service token and the organization name |
| Let's Encrypt (no account) | HTTPS certificates for the dashboard and your services | A real contact email address, passed to the installer as `GEASS_ACME_EMAIL` or set later on the cluster. Let's Encrypt refuses placeholder domains such as `example.com` |

AWS and PlanetScale credentials are saved as connections that belong to one project. See [Connections](/docs/guides/connections/).

## Your own machine

To use the dashboard you need a browser and a way to reach it: an SSH tunnel and `kubectl port-forward` at first, then your domain. Nothing else is needed on your own machine.

The `geass` command line is optional. Its install script needs `curl` and `sh` on Linux or macOS, on `amd64` or `arm64`. For Windows, download `geass-cli-windows-amd64.exe` from the release page. The machine must reach the dashboard's address over HTTPS, or over `http://localhost:8085` through a port-forward, and a person who is an admin in the dashboard must approve its first sign-in. See [Command line](/docs/guides/command-line/).

## Check your machine

```bash
uname -m                      # x86_64 or aarch64
grep MemTotal /proc/meminfo   # at least 2097152 kB
df -h /                       # at least 10G available
command -v curl awk           # both must print a path
sudo -n true && echo "sudo ok"
```

## Troubleshooting

**`insufficient memory: ... MB (2048 MB required)`.** The machine has less than 2048 MiB of total memory as the kernel reports it. Use a larger plan.

**`insufficient disk space on /: ... MB (10240 MB required)`.** Free space on the root filesystem. Remove files or grow the disk. The check counts free space on `/`, even when you later mount data elsewhere.

**`unsupported architecture`.** Geass ships for `amd64` and `arm64` only.

**The installer finishes but the dashboard is unreachable from the internet.** Ports 80 and 443 are closed. The installer does not open them.

## Related

- [Install Geass](/docs/getting-started/install/)
- [Add servers](/docs/getting-started/add-servers/)
- [Multi-provider clusters](/docs/operations/multi-provider/)
- [Security](/docs/operations/security/)
