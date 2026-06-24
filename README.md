# Geass

Geass is a source-available platform that brings the managed public cloud experience to your own VPS or bare-metal hardware.

Powered by [K3s](https://k3s.io/), Geass turns a single affordable server—or a cluster of machines—into a cohesive, horizontally scalable environment. It abstracts away raw infrastructure management so you can deploy applications, provision databases, and manage networking as smoothly as you would on AWS or GCP, on infrastructure you control.

## Why Geass?

Managed clouds and Kubernetes services are powerful, but expensive. Running on VPS providers is cheaper, but manual setup, scaling, and routing across machines is tedious and error-prone. And the full application lifecycle—databases, storage, DNS, security—usually means juggling many disconnected tools.

Geass bridges that gap: a unified control plane for your services, with the developer velocity of a major cloud provider and without vendor lock-in or premium pricing.

## What you get

- **Horizontal scaling** — distribute workloads across multiple machines without hand-configuring load balancers
- **Unified service management** — deploy and monitor applications, databases, and storage from one place
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

- Verify system requirements and open required firewall ports
- Install K3s in server mode
- Deploy the Geass operator
- Apply the initial cluster configuration

Installation logs are written to disk; the path is printed when the installer starts.

## License

Copyright 2026 Adegoke Adewoye.

Geass is licensed under the [Elastic License 2.0](LICENSE). You may use, modify, and deploy Geass for yourself or your organization. You may not offer Geass—or a substantially similar product—to third parties as a hosted or managed service.

See [LICENSE](LICENSE) for the full terms.
