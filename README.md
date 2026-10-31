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
  <a href="LICENSE"><img alt="License: Apache 2.0" src="https://img.shields.io/badge/License-Apache%202.0-blue.svg"></a>
</p>

Geass is an open-source platform that gives your own servers the workflow of a managed cloud. It runs on a VPS, a
bare-metal machine, or a group of both. You install it with one command. Then you get these features:

- Projects and environments.
- Deploys from a Docker image, a GitHub repository, or a function.
- Databases with backups, and buckets for files.
- Domains with HTTPS.
- Metrics and logs.

You control Geass from a web dashboard, from a command line, or from an AI agent through an MCP server. You keep the
servers, the data, and the bill.

Geass is [K3s](https://k3s.io/), a Kubernetes operator, and a dashboard that is part of the operator. The custom
resources are `geass.geass.dev/v1alpha1`. You do not need to know Kubernetes for daily use. If you know Kubernetes,
all of the cluster is available to you.

## Why Geass

Managed clouds have many features, but they are expensive. VPS providers are cheap, but you must configure, scale, and
route traffic between the machines yourself. An application also needs databases, storage, DNS, certificates, and
backups, and each one is a different tool.

Geass gives you one control plane for all of these tasks. It runs on infrastructure that you control, with no lock-in
and no premium price. Geass is for these users:

- Developers and small teams that use VPS or bare-metal servers.
- Teams that must keep their data and workloads on hardware that they can identify.

## Features

- **Services.** Deploy a Docker image, a GitHub repository that Geass builds in the cluster, or a
  [function](https://rungeass.com/docs/guides/functions/). A function runs on Bun, Node.js, or Python, as a server or
  as an event handler, and it can run on a cron schedule. You review your changes, and then you deploy them together.
  Geass also gives you pull request environments and signed webhooks.
- **Databases and storage.** Run PostgreSQL, MySQL, MariaDB, Redis, and SQLite. Share one database server between
  many databases. Resize Longhorn volumes. Keep files in buckets on SeaweedFS, Garage, or AWS S3. Make
  [backups and restore them](https://rungeass.com/docs/guides/backups-and-restore/) from a bucket in your own AWS
  account.
- **Domains and HTTPS.** Geass uses Cloudflare for DNS and Let's Encrypt for certificates.
- **Scaling and placement.** Set replicas and CPU autoscaling. Use regions and node tags to select where a workload
  runs. Use [compliance tags](https://rungeass.com/docs/guides/compliance-tags/) to keep a project on the machines that
  you select. Let [idle services sleep](https://rungeass.com/docs/guides/scale-to-zero/).
- **Machines at a cloud (experimental).** Connect Amazon EC2, Azure, DigitalOcean, Google Cloud, Hetzner, Linode,
  Oracle Cloud, OVHcloud, Scaleway, UpCloud, Vultr, or your own webhook. Geass can then create a server at that cloud.
  It can also run a [node pool](https://rungeass.com/docs/guides/node-pools/). A node pool adds machines when pods
  cannot start, and it removes idle machines. It never goes above the maximum that you set. You can also join servers
  that you already have, over SSH.
- **Isolation by default.** A project cannot connect to another project until you allow it.
- **Observability.** See metrics and live logs with history. Open a terminal in a service. Send queries to a database.
- **Updates that keep your data.** Run a newer installer on the control plane, and the cluster
  [updates in place](https://rungeass.com/docs/operations/upgrading/). The installer makes an etcd snapshot first, and
  it does not downgrade.
- **Automation.** Use the [command line](https://rungeass.com/docs/guides/command-line/), the
  [MCP server](https://rungeass.com/docs/guides/mcp-server/), or the versioned
  [JSON API](https://rungeass.com/docs/reference/rest-api/). All three use the same access rules as the dashboard.

## Limits

Know these limits before you use Geass:

- Geass is not a hosted service. You operate the servers.
- A new install has one control plane server. You can
  [join two more](https://rungeass.com/docs/operations/high-availability/#run-three-control-plane-servers), but Geass
  runs one operator. If the server of the operator stops, the operator starts on another server in about two minutes.
- A cluster with servers at more than one provider is experimental.
- Machines that Geass creates at a cloud are experimental. They cost money until you remove them.
- The Amazon EC2 driver has run against a live account, in one region. The other cloud drivers come from the
  documentation of each provider, and only a simulation has tested them.

For all of the limits, see [What is Geass](https://rungeass.com/docs/overview/introduction/).

## Install Geass

You need a new Linux server with these properties:

- An amd64 or arm64 processor.
- 2 GB of memory or more.
- 10 GB or more of free space on `/`.
- Outbound HTTPS access.
- Ports 80 and 443 open to the internet, for the dashboard and your services.
- Root access.

Connect to the server with SSH, and run this command:

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo sh
```

The script downloads the installer for your architecture, checks it against the `SHA256SUMS` file of the release, and
runs it. The installer checks the machine, installs K3s, deploys the operator, and creates the cluster. The operator
image is in the installer, so Geass downloads nothing more.

Before it has a domain, the dashboard uses HTTPS at the public IP address of the server. To get HTTPS certificates for
your domains, give the installer an email address for Let's Encrypt. To make the install repeatable, set
the release version:

```bash
curl -sfL https://github.com/degoke/geass/releases/download/v0.1.0/install.sh \
  | sudo env GEASS_VERSION=v0.1.0 GEASS_ACME_EMAIL=you@yourdomain.com sh
```

After you install Geass, do these steps:

1. Open the link that the installer printed, and create your admin user. See
   [Sign in to the dashboard](https://rungeass.com/docs/getting-started/sign-in/).
2. [Add servers](https://rungeass.com/docs/getting-started/add-servers/). You can add servers that you have, or
   machines at a cloud.
3. [Deploy your first app](https://rungeass.com/docs/getting-started/first-app/).

To update a cluster, run the installer of the new release on the control plane. The installer keeps the cluster and its
data. For more information, see [Upgrade Geass](https://rungeass.com/docs/operations/upgrading/).

## Use the command line and AI agents

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install-cli.sh | sh
geass login --url https://geass.example.com      # shows a code to approve in the dashboard
geass deploy web --wait                          # if the build or the rollout fails, exits non-zero and says why
geass logs web -f --level error
claude mcp add geass -- geass mcp                # an AI agent can deploy and monitor, as your token allows
```

The command line and its MCP server are one binary. Each release includes the binary and the installer. For more
information, see the [command line guide](https://rungeass.com/docs/guides/command-line/) and the
[MCP server guide](https://rungeass.com/docs/guides/mcp-server/).

## Releases and versions

Each release contains these files:

- The installer for Linux, on amd64 and arm64.
- The command line for Linux, macOS, and Windows.
- The install scripts.
- A `SHA256SUMS` file.

All of the files have the same version, `vMAJOR.MINOR.PATCH`. Geass is not at version 1.0 yet, so read the
[changelog](CHANGELOG.md) before you update. The release files have signed build provenance. To verify a file, run this
command:

```bash
gh attestation verify geass-linux-amd64 --repo degoke/geass
```

For the release procedure, see [docs/releasing.md](docs/releasing.md).

## Build from source and contribute

You can contribute to Geass. To propose a change, read [CONTRIBUTING.md](CONTRIBUTING.md). The file
[docs/development.md](docs/development.md) tells you about the repository layout, the build, all of the tests, and how
to run Geass against a cluster on your machine. To write user documentation, follow
[docs/writing-style.md](docs/writing-style.md).

```bash
make build-cli        # the command line, into bin/ (needs only Go)
make test             # unit tests against a real API server (envtest)
make help             # every target
```

To report a security problem, use the private procedure in [SECURITY.md](SECURITY.md). Follow the
[code of conduct](CODE_OF_CONDUCT.md). For questions and bugs, open an
[issue](https://github.com/degoke/geass/issues). For more information, see [SUPPORT.md](.github/SUPPORT.md).

## License

Copyright 2026 Adegoke Adewoye.

Geass is open source under the [Apache License 2.0](LICENSE). You can use, copy, change, and distribute Geass for any
purpose. This includes commercial use and use as a hosted service. You must keep the copyright and license notices, and
you must identify your changes. For the full terms, see [LICENSE](LICENSE).
