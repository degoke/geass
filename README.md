<h1 align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="geass-brand/logos/geass-horizontal-dark.svg">
    <img alt="geass" src="geass-brand/logos/geass-horizontal-light.svg" height="56">
  </picture>
</h1>

<p align="center"><strong>Absolute command over servers you already own.</strong><br>
The managed cloud experience on your own VPS or bare metal, powered by K3s.</p>

<p align="center"><a href="https://rungeass.com"><strong>Website</strong></a> ·
<a href="https://rungeass.com/docs/"><strong>Documentation</strong></a> ·
<a href="https://rungeass.com/docs/getting-started/install/"><strong>Install guide</strong></a> ·
<a href="CHANGELOG.md">Changelog</a> ·
<a href="https://github.com/degoke/geass/releases">Releases</a></p>

<p align="center">
  <a href="https://github.com/degoke/geass/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/degoke/geass/actions/workflows/ci.yml/badge.svg?branch=main"></a>
  <a href="https://github.com/degoke/geass/releases/latest"><img alt="Release" src="https://img.shields.io/github/v/release/degoke/geass"></a>
  <a href="LICENSE"><img alt="License: Elastic License 2.0" src="https://img.shields.io/badge/License-Elastic%202.0-blue.svg"></a>
</p>

Geass is a source-available platform that gives a VPS, a bare-metal machine, or a handful of both the workflow of a
managed cloud. Install it with one command and you get projects, deploys from an image, a GitHub repository or a
function, databases with backups, buckets, domains with HTTPS, metrics and logs, in a dashboard, a command line and an
MCP server for AI agents. The servers, the data and the bill stay yours.

Under the hood it is [K3s](https://k3s.io/), a Kubernetes operator (the custom resources are `geass.geass.dev/v1alpha1`)
and a dashboard built into the operator. Daily use needs no Kubernetes knowledge, and nothing is hidden from you if you
have it.

## Why Geass

Managed clouds are powerful and expensive. VPS providers are cheap, but setting up, scaling and routing across machines
by hand is tedious, and the full application lifecycle (databases, storage, DNS, certificates, backups) means juggling
many disconnected tools. Geass is one control plane for all of it, on infrastructure you control, without lock-in or
premium pricing. It suits developers and small teams on VPS or bare metal, and anyone who needs their data and workloads
on hardware they can account for.

## What you get

- **Services** from a Docker image, a GitHub repository built in the cluster, or a [function](https://rungeass.com/docs/guides/functions/) (Bun, Node.js or Python, as a server or an event handler, with cron schedules). Review changes, then deploy them together; pull request environments; signed webhooks.
- **Databases and storage**: PostgreSQL, MySQL, Redis and SQLite, shared database servers, resizable Longhorn volumes, buckets on SeaweedFS, Garage or AWS S3, and [backups and restore](https://rungeass.com/docs/guides/backups-and-restore/) to a bucket in your own AWS account.
- **Domains and HTTPS** through Cloudflare and Let's Encrypt.
- **Scaling and placement**: replicas, CPU autoscaling, regions and node tags, [compliance tags](https://rungeass.com/docs/guides/compliance-tags/) that keep a project on chosen machines, and [services that sleep when idle](https://rungeass.com/docs/guides/scale-to-zero/).
- **Machines at a cloud (experimental)**: connect Amazon EC2, Azure, DigitalOcean, Google Cloud, Hetzner, Linode, Oracle Cloud, OVHcloud, Scaleway, UpCloud or Vultr, or a webhook of your own, and Geass can create a server there, or run a [node pool](https://rungeass.com/docs/guides/node-pools/) that adds machines when pods cannot start and removes idle ones, up to a maximum you set. Amazon EC2 has run against a live account (one region); the other drivers were written from the provider's documentation and tested against a simulation only. You can also just join servers you already have over SSH.
- **Isolation by default**: projects cannot reach each other until you allow it.
- **Observability**: metrics, live logs with history, a terminal in a service, and a query box for databases.
- **Updates that keep your data**: run a newer installer on the control plane and the cluster [updates in place](https://rungeass.com/docs/operations/upgrading/), with an etcd snapshot first and no downgrades.
- **Automation**: a [command line](https://rungeass.com/docs/guides/command-line/), an [MCP server](https://rungeass.com/docs/guides/mcp-server/) and a versioned [JSON API](https://rungeass.com/docs/reference/rest-api/), all under the same access rules as the dashboard.

Be aware of what it is not: it is not a hosted service (you run the servers), and the control plane is one server. Servers
at more than one provider in one cluster is experimental, and so is having Geass create machines at a cloud for you, which
costs money until they are removed. [What is Geass](https://rungeass.com/docs/overview/introduction/) lists the limits
before you commit.

## Install

You need a fresh Linux server (amd64 or arm64) with at least 2 GB of memory, 10 GB free on `/`, outbound HTTPS and root
access. SSH in and run:

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo sh
```

That downloads the installer for your architecture, checks it against the release's `SHA256SUMS`, and runs it. The
installer checks the machine, installs K3s, deploys the operator and creates the cluster. Nothing else is fetched from
Geass: the operator image is inside the installer.

For HTTPS certificates give it an email address for Let's Encrypt, and pin a release for repeatable installs:

```bash
curl -sfL https://github.com/degoke/geass/releases/download/v0.1.0/install.sh \
  | sudo env GEASS_VERSION=v0.1.0 GEASS_ACME_EMAIL=you@yourdomain.com sh
```

Then [sign in to the dashboard](https://rungeass.com/docs/getting-started/sign-in/), [add servers](https://rungeass.com/docs/getting-started/add-servers/) (ones you have, or machines at a cloud) and
[deploy your first app](https://rungeass.com/docs/getting-started/first-app/). To update a running cluster, run the installer
of the release you want on the control plane: it keeps the cluster and its data. See
[Upgrade Geass](https://rungeass.com/docs/operations/upgrading/).

## Command line and AI agents

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install-cli.sh | sh
geass login --url https://geass.example.com      # shows a code; you approve it in the dashboard
geass deploy web --wait                          # exits non-zero, saying why, if the build or rollout fails
geass logs web -f --level error
claude mcp add geass -- geass mcp                # let an AI agent deploy and monitor, as your token allows
```

The command line and its MCP server are one binary, released together with the installer. The
[command line guide](https://rungeass.com/docs/guides/command-line/) and the [MCP server guide](https://rungeass.com/docs/guides/mcp-server/) have the rest.

## Releases and versions

Every release carries the installer for Linux (amd64 and arm64), the command line for Linux, macOS and Windows, the
install scripts, and a `SHA256SUMS` file. They are versioned together as `vMAJOR.MINOR.PATCH`, and Geass is before 1.0:
read the [changelog](CHANGELOG.md) before updating. Release files carry signed build provenance:

```bash
gh attestation verify geass-linux-amd64 --repo degoke/geass
```

How a release is made is in [docs/releasing.md](docs/releasing.md).

## Build from source and contribute

Contributions are welcome. [CONTRIBUTING.md](CONTRIBUTING.md) says how to propose a change, and
[docs/development.md](docs/development.md) covers the repository layout, building, running Geass against a cluster on
your machine, and every kind of test.

```bash
make build-cli        # the command line, into bin/ (needs only Go)
make test             # unit tests against a real API server (envtest)
make help             # every target
```

Please report security problems privately, as [SECURITY.md](SECURITY.md) describes, and follow the
[code of conduct](CODE_OF_CONDUCT.md). Questions and bugs go to [issues](https://github.com/degoke/geass/issues); see
[SUPPORT.md](.github/SUPPORT.md).

## License

Copyright 2026 Adegoke Adewoye.

Geass is licensed under the [Elastic License 2.0](LICENSE). You may use, modify and deploy Geass for yourself or your
organization. You may not offer Geass, or a substantially similar product, to third parties as a hosted or managed
service. It is source-available, not open source as the Open Source Initiative defines it. See [LICENSE](LICENSE) for the
full terms.
