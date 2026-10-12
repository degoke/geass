# Geass

> Managed cloud experience on your VPS or bare-metal — powered by K3s.

[![License: ELv2](https://img.shields.io/badge/License-Elastic%202.0-blue.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/degoke/geass)](https://github.com/degoke/geass/releases/latest)

Geass is a source-available platform that brings the managed public cloud experience to your own VPS or bare-metal hardware.

Powered by [K3s](https://k3s.io/), Geass turns a single affordable server—or a cluster of machines—into a cohesive, horizontally scalable environment. It abstracts away raw infrastructure management so you can deploy applications, provision databases, and manage networking as smoothly as you would on AWS or GCP, on infrastructure you control.

## Why Geass?

Managed clouds and Kubernetes services are powerful, but expensive. Running on VPS providers is cheaper, but manual setup, scaling, and routing across machines is tedious and error-prone. And the full application lifecycle—databases, storage, DNS, security—usually means juggling many disconnected tools.

Geass bridges that gap: a unified control plane for your services, with the developer velocity of a major cloud provider and without vendor lock-in or premium pricing.

## What you get

- **Horizontal scaling** — distribute workloads across multiple machines without hand-configuring load balancers
- **Unified service management** — deploy and monitor applications, databases, and storage from one place
- **Backups and restore** — Postgres, MySQL and SQLite databases are backed up to a bucket in your own AWS account (Postgres continuously, so it can be restored to any recent moment; MySQL and SQLite daily); restore as a new database, and keep deleted databases restorable for 7 days
- **Lightweight orchestration** — Kubernetes-native, built on K3s, without the overhead of a full cluster install

Geass is aimed at teams that need sovereign or on-premise deployments, self-hosted infrastructure, or cost-effective microservices on a fleet of VPS instances.

## Getting started

### Requirements

- A fresh Linux VPS (amd64 or arm64)
- At least 2 GB RAM and 10 GB free disk on `/`
- Root access (`sudo`)

### Install

SSH into your VPS and run:

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo sh
```

That downloads the Geass installer binary for your architecture and runs it. No build step and no extra files to copy—the package includes everything needed to bootstrap the cluster.

Pin a specific version:

```bash
curl -sfL https://github.com/degoke/geass/releases/download/v0.1.0/install.sh | sudo env GEASS_VERSION=v0.1.0 sh
```

The installer will:

- Verify system requirements and open required firewall ports when a host firewall is active
- Install K3s in server mode
- Deploy the Geass operator
- Apply the initial cluster configuration

Installation logs are written to disk; the path is printed when the installer starts.

### Nodes at more than one provider (experimental)

By default the cluster's nodes share a private network. To build a cluster whose nodes sit at different
providers and reach each other over the internet, give the installer this machine's public address:

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo env GEASS_MULTI_SITE=1 GEASS_NODE_EXTERNAL_IP=203.0.113.10 sh
```

The cluster then uses an encrypted WireGuard overlay (UDP 51820, instead of VXLAN's 8472) and every node
advertises its public IP. Keep the control plane at one site and use a kernel with WireGuard (built in from
Linux 5.6). Between the nodes' public addresses, and in each provider's security group, open:

- **TCP 6443** to the control plane, from every node **including the control plane itself**: it reaches its own
  API through its public address, and without this the operator and most system services cannot start.
- **UDP 51820** between every pair of nodes.
- **TCP 10250** between every pair of nodes, so container metrics are collected from every node.
- **TCP 22** to each new worker, from the control plane, because Geass joins workers over SSH.

Workers are joined over SSH as `root` or as any user with passwordless `sudo` (such as `ubuntu`).

This has been run on real servers: a four-node cluster across three availability zones, joined over their public
addresses, with traffic carried by the encrypted overlay. It has not yet been run across two different
providers. Host-level node charts (node CPU and memory) are collected only for the node that runs monitoring,
because they are published on private addresses; container metrics, which feed the service and project charts,
are collected from every node.

Tag nodes with a region, a site and tags under **Settings → Usage → Nodes**; projects then choose a region.

### Project isolation

Each project is private by default: only its own services (in every environment), Geass itself, and visitors who
arrive through your domains can reach them. Services in other projects cannot, and neither can anything else in
the cluster. A project's services can still reach each other, other services and the internet.

To let one project call another (a front end calling an internal API, say), open the receiving project's
**Settings → Network** and choose which projects may reach it. The same page can open a project to every project
in the cluster. The change applies instantly and needs no redeploy.

Two things hold whatever you choose: services cannot read the cloud provider's credentials for the machine they
run on, and no project can read another's metrics or logs. Projects that existed before isolation was added
become isolated when Geass is updated, so list any project that relied on reaching another before updating.

## License

Copyright 2026 Adegoke Adewoye.

Geass is licensed under the [Elastic License 2.0](LICENSE). You may use, modify, and deploy Geass for yourself or your organization. You may not offer Geass—or a substantially similar product—to third parties as a hosted or managed service.

See [LICENSE](LICENSE) for the full terms.
