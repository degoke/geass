---
title: "Requirements"
description: "The hardware, operating system, ports, DNS, and optional accounts that you need before you install Geass on a VPS or bare-metal server."
---

Geass installs on one Linux server, and you add more servers to grow the cluster. This page lists these items:

- What that server needs.
- Which ports must be open, and which ports the installer opens for you.
- The optional accounts (Cloudflare, GitHub, AWS, and PlanetScale) that unlock specific features.

You do not need any of those accounts to install Geass or to deploy your first app.

## Summary

| Requirement | Minimum | Checked by the installer |
| --- | --- | --- |
| CPU architecture | `amd64` (x86_64) or `arm64` (aarch64) | Yes |
| Operating system | Linux | Yes (Linux only, no distribution check) |
| Memory | A server with 2 GB: at least 1700 MiB of total memory as the kernel reports it | Yes |
| Disk | 10 GiB free on the filesystem that holds `/` | Yes |
| Privileges | root, or a user that can run `sudo` | No (the install fails when it cannot write to `/etc`) |
| Network | Outbound HTTPS to the hosts in the list below | No |
| Tools on the server | `curl`, `sh`, `awk`, `df` | No |

Release builds exist for `linux/amd64` and `linux/arm64`. The `install.sh` script uses `uname -m` to select the correct build. On any other architecture, the script stops with `unsupported architecture`.

## Memory and disk

The memory check reads `MemTotal` from `/proc/meminfo` and requires at least 1700 MiB. The kernel reports less than the size that your provider advertises, because the kernel reserves memory for these items:

- The kernel itself.
- Firmware.
- A crash kernel, on Red Hat family systems.

A plan sold as "2 GB" usually reports between about 1.7 and 1.95 GiB, so it passes. A 1 GB plan reports about 950 MiB, so it does not pass. Check the server before you install:

```bash
grep MemTotal /proc/meminfo
df -h /
```

The disk check measures free space on `/` (`df --output=avail -k /`) and requires 10 GiB.

These numbers are the minimum for the installer. They are not a sizing guide. The installer also turns on these add-ons:

- cert-manager.
- Monitoring (Prometheus, `lite` profile).
- Longhorn storage.
- Log collection.

The add-ons run on the same machine, and they reserve CPU and memory before you deploy anything. Metrics and logs also use disk:

- Prometheus keeps 15 days of data, up to 4 GB, on a 5 Gi volume.
- By default, the log store keeps 7 days of data, up to 4 GiB.

By default, a new service requests 0.1 CPU and 128 MB.

Geass refuses to deploy a resource that does not fit in the free capacity of the nodes that it can run on. The message starts with `This service needs about ... CPU and ... memory` and ends with `Scale up the cluster before creating it`. To see the requested and allocatable CPU and memory for each node, open **Usage** in the left rail.

## Operating system

The installer checks that it runs on Linux. It does not check anything else about the distribution. The installer then uses the K3s installer script to install [K3s](https://k3s.io/), so K3s must support the machine.

Longhorn is the storage engine that Geass uses for database volumes. Longhorn needs the iSCSI initiator on every node. The installer installs the initiator for you with `apt-get`, `dnf`, `yum`, or `zypper`, and it enables the `iscsid` service. In these cases, the installer logs the problem and continues:

- The machine has none of those package managers.
- The installer cannot download the package.

Then Geass cannot provision database volumes until you install the initiator yourself. The package is `open-iscsi` on Debian and Ubuntu, and `iscsi-initiator-utils` on Red Hat family systems.

One feature depends on the Kubernetes version. Compliance node tags use two admission policies:

- A `ValidatingAdmissionPolicy`, which needs Kubernetes 1.30 or later.
- A `MutatingAdmissionPolicy`, which needs Kubernetes 1.36 or later. This policy gives a node selector to pods that database operators make.

The installer lets K3s select its `stable` release. To see which version you have, run `sudo k3s kubectl version`. An older Kubernetes is possible, for example a version that you pinned with `INSTALL_K3S_VERSION`. On an older Kubernetes, all other parts of compliance tags work, and the operator logs that it does not check pods created outside Geass. See [Compliance node tags](/docs/guides/compliance-tags/).

A multi-site cluster (nodes at different providers) also needs a kernel that can create WireGuard interfaces. WireGuard is built in from Linux 5.6. On older kernels, install the `wireguard` package. To test for WireGuard, the installer creates and removes a temporary interface named `geass-wgcheck`. This test needs the `ip` command.

## Root access

Run the installer as root. The one-line command in [Install Geass](/docs/getting-started/install/) uses `sudo`. The installer does not check for root. Without root, the install fails when it first writes to `/etc/rancher/k3s`.

## Outbound network access

The server must reach these hosts over HTTPS during and after the installation. Geass has no offline install mode.

| Host | Used for |
| --- | --- |
| `github.com` and the release asset hosts that it redirects to | Download of the Geass installer and of K3s releases |
| `get.k3s.io`, `update.k3s.io` | K3s installer script and the K3s release channel |
| `charts.jetstack.io`, `prometheus-community.github.io`, `charts.longhorn.io`, `victoriametrics.github.io`, `fluent.github.io` | Helm charts for the add-ons that the installer turns on |
| `acme-v02.api.letsencrypt.org` | cert-manager registers the ACME account of the cluster and requests certificates. Only after the `letsencrypt-prod` issuer exists, which needs `GEASS_ACME_EMAIL` |
| `cloudnative-pg.github.io` | The chart that Geass installs on demand for PostgreSQL (CloudNativePG) |
| `cybozu-go.github.io` | The chart that Geass installs on demand for high availability MySQL (MOCO). Its images come from `ghcr.io` |
| `kedacore.github.io` | The charts for the serverless add-on (KEDA and its HTTP add-on). Only if you turn on the add-on, so that services can sleep when idle |
| The container registries that these items use: the charts, the MySQL and Redis servers, the in-cluster object storage server, and your own images. Geass runs the MySQL, Redis, and object storage servers from pinned images on Docker Hub, not from charts. High availability MySQL pulls MOCO and its MySQL build from `ghcr.io` | Image pulls |
| `api.github.com`, `api.cloudflare.com`, AWS (S3 and IAM), `api.planetscale.com` | Only for the connectors that you set up |
| The API of a cloud that creates machines for you (EC2, Azure Resource Manager and its sign-in, DigitalOcean, Compute Engine, Hetzner Cloud, Linode, Oracle Cloud, OVHcloud, Scaleway, UpCloud, Vultr, or your own webhook) | Only for a [connection for machines](/docs/guides/connectors/clouds/) that you set up. The Azure driver also reads the Azure public price list |
| `api.ipify.org`, then `ifconfig.me` | Only when Geass needs the public IP of the server for dashboard DNS and no node reports one |

New workers must also reach `get.k3s.io` and the API of the control plane (port 6443).

## Ports and firewall

Open these ports between the machines. Open them in the firewall or security group of the provider, and in each host firewall.

| Port | Where | Purpose | Opened by the installer |
| --- | --- | --- | --- |
| 22/tcp | From you to every server, and from the control plane to each new worker | SSH. Geass joins workers over SSH | No. For a machine that Geass creates at a cloud, the firewall that Geass makes there allows SSH from the addresses of the control plane |
| 80/tcp and 443/tcp | From the internet to the nodes that serve web traffic | The ingress controller (Traefik, which K3s installs) for your services and the dashboard domain. Let's Encrypt also needs 80/tcp to validate a hostname before it issues a certificate | Yes, on the control plane |
| 6443/tcp | To the control plane, from every worker. In a multi-site cluster, from every node, including the control plane itself | Kubernetes API | Yes, on the control plane |
| 10250/tcp | Between nodes | kubelet, used for container metrics | Yes, on the control plane |
| 8472/udp | Between nodes | VXLAN overlay network (default network mode) | Yes, on the control plane |
| 51820/udp | Between every pair of nodes | WireGuard overlay network, used instead of 8472 in a multi-site cluster | Yes, on the control plane |

### What the installer opens

The installer looks for an active host firewall on the control plane in this order:

1. `ufw`.
2. `firewalld`.
3. `iptables`. The installer considers `iptables` active when the `INPUT` chain has a `DROP` policy or any rule.

In the first firewall that it finds, the installer opens these ports:

- 6443/tcp.
- 10250/tcp.
- The overlay port: 8472/udp, or 51820/udp for a multi-site cluster.
- 80/tcp and 443/tcp. These ports carry your services, the domain of the dashboard, and the Let's Encrypt validation.

The installer uses these commands:

- `ufw`: `ufw allow <port>/<protocol>`.
- `firewalld`: `firewall-cmd --permanent --add-port=...`, then `firewall-cmd --reload`.
- `iptables`: `iptables -w -I INPUT <position> -p <protocol> --dport <port> -j ACCEPT`, or `-A INPUT ...` when the installer appends the rule.

For `iptables`, the installer also does these things:

- It skips a rule when `iptables -C` says that the rule already exists. Thus, if you run the installer again, it does not add a rule two times.
- It uses `-w` to make iptables wait for the lock. It does not use `-w` on an iptables version that is too old to know this flag.
- It puts the rule immediately before the first catch-all `REJECT` or `DROP` in the chain. Many hosts that filter with `iptables` end `INPUT` with a catch-all rule, and a rule after the catch-all never matches. A catch-all rule is the default on Oracle Cloud and Red Hat images.
- Your rules that come before the catch-all stay first, and they still win. Examples are a blocked source or a port that you closed on purpose.
- When the chain has no catch-all, the installer appends the rule.

If the installer finds no active firewall, it prints a warning that starts `No active host firewall detected; skipping port configuration`. The warning lists the ports that you must open yourself if you add a firewall later.

`ufw` and `firewalld` keep their rules across reboots. For `iptables`, the installer saves the rules only in this case:

- The installer added a rule.
- The host already restores the rules at boot.

To save the rules, the installer writes the current ruleset (`iptables-save`) over one of these files, if the file exists:

- `/etc/iptables/rules.v4` (`iptables-persistent`, Debian and Ubuntu).
- `/etc/sysconfig/iptables` (`iptables-services`, Red Hat family).

On a host that restores no rules, the installer prints a warning that starts `The iptables rules that open the ports are not saved`. The ports then close again at the next reboot. The installer does not install a persistence package or create the file, because that change would alter how the firewall of the machine is managed. Install a package and save the rules yourself:

```bash
sudo apt-get install iptables-persistent   # Debian, Ubuntu
sudo netfilter-persistent save
```

:::caution
Also open the ports above in the firewall or security group of the provider. The installer changes only the host firewall of the control plane. It does not change a provider firewall or security group. It does not configure the firewall of workers. The only exception is a multi-site cluster. When Geass joins a worker there, it opens 51820/udp and 10250/tcp on that worker, but only with `ufw` or `firewalld`.

If your server has a public address, restrict 6443 and 10250 to your other nodes in the firewall of the provider. The rules that the installer adds accept traffic from any source. An `iptables` rule goes after your own rules and before a catch-all `REJECT` or `DROP` at the end. Thus, your rule that blocks a source or a port still wins. But the catch-all no longer blocks these ports. If you meant to block these ports, delete the rule.
:::

## DNS

You do not need a domain to install Geass, sign in, or run a service. You need a domain for these tasks:

- To publish services on a hostname.
- To serve the dashboard over HTTPS.
- To register the GitHub App.

For HTTPS certificates, you also need these items:

- A real email address, which you give to the installer as `GEASS_ACME_EMAIL`.
- 80/tcp and 443/tcp open.

See [HTTPS certificates](/docs/getting-started/install/#https-certificates).

- **Dashboard.** The dashboard host is `geass.` plus your root domain, for example `geass.example.com`. You can select a different subdomain under **Settings → Domain**. Point an `A` record at the public IP of the server. Or, when Cloudflare is connected, let Geass create the record.
- **Services.** Each service gets the hostname that you enter in the **Settings** tab of the service, under **Networking**, in **Custom domain**. Point an `A` record at the public IP of a node. When Cloudflare is connected, Geass can assign hostnames itself.

See [Connect a domain](/docs/getting-started/connect-domain/) and [Domains and TLS](/docs/guides/domains-and-tls/).

## Optional accounts

| Account | Needed for | What Geass needs from it |
| --- | --- | --- |
| Cloudflare | Automatic DNS records for the dashboard and services. Cloudflare Tunnel, to publish the dashboard without open ports | An API token for your domain, not the Global API Key. The token needs Account → Cloudflare Tunnel → Edit, Zone → DNS → Edit, and Zone → Zone → Read |
| GitHub | Repository deploys, push-to-deploy, pull request environments, and build and deploy reports back to GitHub | A GitHub account or organization where Geass registers a GitHub App. The dashboard needs a public domain first, because GitHub calls back to it. Geass pushes built images to a container registry that the cluster can reach |
| AWS | S3 buckets for your services, backups of Postgres, MySQL, and SQLite databases, and attached AWS RDS databases | An access key. Geass creates and deletes S3 buckets and objects. For each bucket, Geass creates and deletes a scoped IAM user with an inline policy and access keys |
| PlanetScale | PlanetScale databases | A service token and the organization name |
| Amazon EC2, Azure, DigitalOcean, Google Cloud, Hetzner, Linode, Oracle Cloud, OVHcloud, Scaleway, UpCloud, or Vultr (or your own webhook) | Machines that Geass creates for [node pools](/docs/guides/node-pools/) and single servers (experimental) | Credentials that can create and delete machines, in a separate project or account. The credentials are different for each cloud. See [Cloud connections for machines](/docs/guides/connectors/clouds/) |
| Let's Encrypt (no account) | HTTPS certificates for the dashboard and your services | A real contact email address. Give it to the installer as `GEASS_ACME_EMAIL`, or set it later on the cluster. Let's Encrypt refuses placeholder domains such as `example.com` |

Geass saves AWS and PlanetScale credentials as connections that belong to one project. Credentials for machines belong to the cluster. See [Cloud connectors](/docs/guides/connectors/).

## Your own machine

To use the dashboard, you need a browser and a way to reach the dashboard. At first, use an SSH tunnel and `kubectl port-forward`. Later, use your domain. You do not need anything else on your own machine.

The `geass` command line is optional. Its install script needs `curl` and `sh` on Linux or macOS, on `amd64` or `arm64`. For Windows, download `geass-cli-windows-amd64.exe` from the release page. The machine must reach the address of the dashboard over HTTPS, or over `http://localhost:8085` through a port-forward. An admin in the dashboard must approve the first sign-in of the command line. See [Command line](/docs/guides/command-line/).

## Check your machine

```bash
uname -m                      # x86_64 or aarch64
grep MemTotal /proc/meminfo   # at least 1740800 kB
df -h /                       # at least 10G available
command -v curl awk           # both must print a path
sudo -n true && echo "sudo ok"
```

## Troubleshooting

**`insufficient memory: ... MB (a server with 2 GB is needed, which the kernel reports as at least 1700 MB)`.** The kernel reports less than 1700 MiB of total memory on the machine. Thus, the plan is smaller than 2 GB. Use a larger plan.

**`insufficient disk space on /: ... MB (10240 MB required)`.** Make free space on the root filesystem. Remove files or make the disk larger. The check counts free space on `/`, even when you later mount data in a different location.

**`unsupported architecture`.** Geass ships only for `amd64` and `arm64`.

**The installer finishes but the dashboard is unreachable from the internet.** Ports 80 and 443 are probably closed in the firewall or security group of the provider. The installer cannot change that firewall. The installer opens these ports only in an active host firewall on the control plane. Also, Geass does not publish the dashboard until you configure a domain.

## Related

- [Install Geass](/docs/getting-started/install/)
- [Add servers](/docs/getting-started/add-servers/)
- [Multi-provider clusters](/docs/operations/multi-provider/)
- [Security](/docs/operations/security/)
