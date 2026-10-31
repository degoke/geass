---
title: "Requirements"
description: "What your server needs before you install Geass: CPU, memory, disk, open ports, DNS, and the optional accounts that unlock some features."
---

Geass installs on one Linux server. You can add more servers later. This page tells you what the first server needs,
which ports to open, and which optional accounts unlock which features.

You do not need a domain or any outside account to install Geass and deploy your first app.

## Summary

| Requirement | Minimum | The installer checks it |
| --- | --- | --- |
| CPU architecture | `amd64` (x86_64) or `arm64` (aarch64) | Yes |
| Operating system | Linux. Any common distribution works. | Yes (Linux only) |
| Memory | A server sold as 2 GB | Yes |
| Disk | 10 GiB free on `/` | Yes |
| Access | root, or a user that can run `sudo` | No |
| Network | Outbound HTTPS to the internet | No |
| Tools on the server | `curl`, `sh`, `awk`, and `df` | No |

## Memory and disk

The installer needs at least 1700 MiB of memory, as the kernel reports it. A plan sold as "2 GB" usually passes. A
1 GB plan does not pass.

These numbers are the minimum to install. They are not a sizing guide. Geass also runs logs, metrics, certificates, and
storage on the same server. These parts use memory and disk before you deploy anything. Metrics keep 15 days of
data, up to 4 GB. Logs keep 7 days of data, up to 4 GiB.

By default, a new service reserves 0.1 CPU and 128 MB of memory. If a new service does not fit on your servers, Geass
does not start it. The service shows a message that tells you to add capacity. To see the free capacity, open **Usage** in the left rail.

## Ports and firewall

Open these ports in the firewall or security group of your provider:

| Port | From | Purpose |
| --- | --- | --- |
| 22/tcp | You, and your first server to each server that you add | SSH. Geass adds servers over SSH. |
| 80/tcp and 443/tcp | The internet | Web traffic to your services and the dashboard. Let's Encrypt also uses port 80 to issue certificates. The dashboard at the public IP address of the server needs both ports. |
| 6443/tcp, 10250/tcp, 8472/udp | Your other servers only | Traffic between your servers. You need these ports only when you have more than one server. |
| 51820/udp | Your other servers only | Traffic between servers at different providers. You need this port only for a [multi-provider cluster](/docs/operations/multi-provider/). |

If the first server has a host firewall (`ufw`, `firewalld`, or `iptables`), the installer opens these ports in it for
you. The installer cannot change the firewall of your provider. Open the ports there yourself.

:::caution
Allow 6443 and 10250 only from your other servers. These ports give access to the inside of the cluster. The rules that
the installer adds to the host firewall accept traffic from any address.
:::

## DNS

You do not need a domain to install Geass, sign in, or run a service. Before it has a domain, the dashboard uses HTTPS
at the public IP address of the server. You need a domain for these tasks:

- To give your services a public hostname.
- To give the dashboard a name instead of an IP address.
- To connect GitHub.

For HTTPS on a domain, you also need a real email address for Let's Encrypt. See
[HTTPS certificates](/docs/getting-started/install/#https-certificates).

To set up the domain after you install, see [Connect a domain](/docs/getting-started/connect-domain/).

## Optional accounts

| Account | What it gives you | What Geass needs |
| --- | --- | --- |
| [GitHub](/docs/guides/connectors/github/) | Deploys from a repository, deploy on push, and pull request environments | A GitHub account or organization. The dashboard must have a public domain first. |
| [Cloudflare](/docs/guides/connectors/cloudflare/) | Automatic DNS records and hostnames. A Cloudflare Tunnel, which publishes services without open ports. | An API token for your domain |
| [AWS](/docs/guides/connectors/aws/) | S3 buckets, database backups, and AWS RDS databases | An access key |
| [PlanetScale](/docs/guides/connectors/planetscale/) | PlanetScale databases | A service token and the organization name |
| [A cloud that creates servers](/docs/guides/connectors/clouds/) | Servers that Geass creates and removes for you. Experimental. | Credentials that can create and delete machines |
| Let's Encrypt (no account) | HTTPS certificates for your domains | A real contact email address |

## Your own machine

To use the dashboard, you need a browser. Before you have a domain, the dashboard is at the public IPv4 address of the
server. If the server has no public IPv4 address, or port 80 is closed, you also need `ssh` on your computer. See
[Open the dashboard](/docs/getting-started/sign-in/#open-the-dashboard).

The `geass` command line is optional. It runs on Linux, macOS, and Windows. See
[Command line](/docs/guides/command-line/).

## Check your server

The installer checks the architecture, the memory, and the disk. To check the tools and the access, run these
commands on the server before you install:

```bash
command -v curl awk           # both must print a path
sudo -n true && echo "sudo ok"
```

## Troubleshooting

**`insufficient memory: ... MB`.** The server has less than 2 GB of memory. Use a larger plan.

**`insufficient disk space on /: ... MB (10240 MB required)`.** Free space on the root filesystem, or make the disk
larger. The check counts free space on `/`, even if you store data in a different location later.

**`unsupported architecture`.** Geass runs only on `amd64` and `arm64`.

**The installer finishes, but the internet cannot reach the dashboard or your services.** Ports 80 and 443 are probably
closed in the firewall of your provider. Open them, and run the [`dashboard-link` command](/docs/guides/users-and-tokens/#get-a-new-setup-link)
to get a new link to the dashboard.

## Under the hood

### Operating system details

- The installer installs [K3s](https://k3s.io/), so K3s must support the machine.
- Database volumes need the iSCSI initiator on every server. The installer installs it with `apt-get`, `dnf`, `yum`, or
  `zypper`. If it cannot, it logs the problem and continues. Then install `open-iscsi` (Debian, Ubuntu) or
  `iscsi-initiator-utils` (Red Hat family) yourself.
- [Compliance node tags](/docs/guides/compliance-tags/) check everything only on Kubernetes 1.36 or later. To see the
  version, run `sudo k3s kubectl version`.
- A multi-provider cluster needs WireGuard in the kernel. Linux 5.6 and later have it. On an older kernel, install the
  `wireguard` package.

### Outbound network access

Geass has no offline install. If you limit outbound traffic, allow HTTPS to these hosts:

| Host | Used for |
| --- | --- |
| `github.com` and the hosts that it redirects to | The Geass installer and K3s releases |
| `get.k3s.io`, `update.k3s.io` | The K3s installer and its release channel |
| `charts.jetstack.io`, `prometheus-community.github.io`, `charts.longhorn.io`, `victoriametrics.github.io`, `fluent.github.io` | The add-ons that the installer turns on |
| `acme-v02.api.letsencrypt.org` | HTTPS certificates |
| `cloudnative-pg.github.io`, `cybozu-go.github.io` | PostgreSQL, and high availability MySQL. Only when you create one. |
| `kedacore.github.io` | Sleep when idle. Only when you turn it on. |
| Docker Hub, `ghcr.io`, and the registries of your own images | Container images |
| `api.github.com`, `api.cloudflare.com`, AWS, `api.planetscale.com`, and the API of a cloud that creates servers | Only for the connectors that you set up |
| `api.ipify.org`, then `ifconfig.me` | Only when Geass needs the public IP of the server and cannot find it |

Servers that you add must also reach `get.k3s.io` and port 6443 on the first server.

### What the installer does to the host firewall

The installer uses the first active firewall that it finds: `ufw`, then `firewalld`, then `iptables`. It opens 6443/tcp,
10250/tcp, 80/tcp, 443/tcp, and the overlay port (8472/udp, or 51820/udp for a multi-provider cluster). It does not
configure the firewall of servers that you add, except in a multi-provider cluster.

With `iptables`, the installer puts each rule before the first catch-all `REJECT` or `DROP`, and it does not add a rule
two times. It saves the rules only if the host already restores rules at boot. If it cannot save them, it prints
`The iptables rules that open the ports are not saved`. To save them yourself on Debian or Ubuntu, run these commands:

```bash
sudo apt-get install iptables-persistent
sudo netfilter-persistent save
```

If the installer finds no active firewall, it prints `No active host firewall detected; skipping port configuration`.

## Related

- [Install Geass](/docs/getting-started/install/)
- [Sizing](/docs/operations/sizing/)
- [Add servers](/docs/getting-started/add-servers/)
- [Multi-provider clusters](/docs/operations/multi-provider/)
