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

### HTTPS certificates

Geass gets certificates for the dashboard and for your services from [Let's Encrypt](https://letsencrypt.org/), through
cert-manager. Let's Encrypt registers an account for the cluster and wants a contact address for it, which only you can
give, so pass one to the installer as `GEASS_ACME_EMAIL`:

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo env GEASS_ACME_EMAIL=you@yourdomain.com sh
```

Use a real address: Let's Encrypt refuses placeholder domains such as `example.com`. The installer checks that the
value looks like an email address and stops before changing anything if it does not.

With an address set, Geass creates a cert-manager `ClusterIssuer` named `letsencrypt-prod` as soon as cert-manager is
ready (ACME over HTTP-01, solved through the Traefik ingress that K3s ships). Every ingress Geass creates asks for its
certificate from that issuer. Let's Encrypt must be able to reach your server on **TCP 80** to validate a domain, and
visitors reach your services on **TCP 443**; the installer does not open these, so allow them in your firewall and
your provider's security group.

Without an address the cluster still installs and runs, but no issuer is created, so **no HTTPS certificate is issued**
and the dashboard's domain stays pending. Geass says so rather than failing: the `GeassCluster` has a
`ClusterIssuerReady` condition that names the problem, and the same message is added to the dashboard domain's status
(**Settings → Domain** and the `DashboardDomainReady` condition) while its HTTPS is not reachable.

```bash
kubectl -n geass-system get geasscluster default -o jsonpath='{.status.conditions[?(@.type=="ClusterIssuerReady")].message}'
```

To set or change the address on a running cluster (including one installed before this setting existed), edit the
cluster; Geass creates or updates the issuer within seconds:

```bash
kubectl -n geass-system patch geasscluster default --type merge \
  -p '{"spec":{"addons":{"certManager":{"acmeEmail":"you@yourdomain.com"}}}}'
```

Two things Geass deliberately leaves alone: a `letsencrypt-prod` issuer that you created yourself is never overwritten
(it is reported as `ExternallyManaged`), and removing the address later does not delete the issuer that Geass made,
because certificates issued through it still renew through it. If you run your own cert-manager, turn the add-on off
(`spec.addons.certManager.enabled: false`) and Geass manages no issuer at all.

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
