---
title: "What is Geass"
description: "Geass is an open-source platform that gives your own servers the workflow of a managed cloud: projects, deploys, databases, buckets, backups, and logs."
---

Geass gives a VPS, a bare-metal machine, or a small group of both the workflow of a managed cloud. You install Geass on a Linux server with one command. The installer sets up these parts:

- K3s (a lightweight Kubernetes).
- A Kubernetes operator.
- A web dashboard that is part of the operator.

Then, in the dashboard, you create projects and add services, databases, and buckets. You review what you changed, and you click **Deploy changes**. You can also do the same work from a terminal with the `geass` command line, or an AI agent can do it through the MCP server. You keep the servers, the data, and the bill.

Geass is open source under the Apache License 2.0. It is not a hosted service: you operate the servers that it runs on. This page tells you who Geass is for, what you can do with it, and where it does not fit.

## Who it is for

- Developers and small teams that use VPS or bare-metal servers because a managed cloud costs more. These teams want projects, deploys, managed-style databases, backups, metrics, and logs, without the work of putting together separate tools.
- Teams that must keep their data and workloads on hardware that they control, in a location that they select.
- People who know how to work with Linux servers: SSH, a firewall, and DNS. For daily use of Geass, you do not need to know Kubernetes. To repair a broken server, you sometimes need Kubernetes. For these cases, the docs give you the `kubectl` commands (through `k3s kubectl`).

## What you can do

- **Organize work** into [projects and environments](/docs/guides/projects-and-environments/), such as `production` and `staging`. Geass isolates each environment in its own Kubernetes namespace.
- **Deploy services** in one of these ways. See [Apps](/docs/guides/apps/) and [GitHub and builds](/docs/guides/github-and-builds/).
  - From a Docker image.
  - From a GitHub repository that Geass builds in the cluster with Kaniko.
  - As a [function](/docs/guides/functions/). A function is a single file that runs on Bun, Node.js, or Python. It runs as a server, or as an event handler that Geass serves HTTP for. A cron schedule can call the function.
- **Review before you deploy.** New resources are drafts. Changes stay pending until you click **Deploy changes**. Some settings, such as replicas and domains, take effect immediately. See [Projects and environments](/docs/guides/projects-and-environments/#pending-changes-and-deploy-changes).
- **Start from a template**, or save your own. See [Templates](/docs/guides/templates/).
- **Run databases.** Run PostgreSQL, MySQL, Redis, and SQLite in the cluster. Use [shared database servers](/docs/guides/shared-database-servers/) for many small databases. Connect apps to external databases that you host in a different location, such as AWS RDS or PlanetScale. See [Databases](/docs/guides/databases/).
- **Back up** Postgres, MySQL, and SQLite databases to a bucket in your own AWS account. Geass backs up Postgres continuously, so you can restore it to any recent moment. Geass copies MySQL and SQLite daily. A restore creates a new database. You can restore a deleted database for 7 days. See [Backups and restore](/docs/guides/backups-and-restore/).
- **Store files** in buckets, on an in-cluster object storage server (SeaweedFS or Garage) or in AWS S3. See [Object storage](/docs/guides/object-storage/).
- **Resize database volumes** on Longhorn storage. See [Storage](/docs/guides/storage/).
- **Publish on your domain.** Give a service a hostname. Or connect Cloudflare and let Geass assign a hostname, publish the service through a Cloudflare Tunnel, and create its DNS record. HTTPS certificates come from Let's Encrypt. See [Domains and TLS](/docs/guides/domains-and-tls/).
- **Scale and place.** Set replicas and CPU autoscaling. To decide where workloads run, tag servers with a region, a site, and tags. See [Scaling and placement](/docs/guides/scaling-and-placement/).
- **Add servers, or let Geass add them.** Join a server that you have over SSH. Or connect a cloud and let Geass create machines there. Geass can create one machine at a time, or a [pool](/docs/guides/node-pools/). A pool adds nodes when pods cannot start, and it removes idle nodes. This feature is experimental. See [Add servers](/docs/getting-started/add-servers/) and [Cloud connections for machines](/docs/guides/connectors/clouds/).
- **Sleep when idle.** Stop a service after it gets no requests for a time that you select. Geass starts the service again on the next request. Thus, a small cluster can hold many services that are used only sometimes. This feature is opt-in. It needs the serverless add-on, which Geass installs only when you turn it on for the cluster. See [Sleep when idle](/docs/guides/scale-to-zero/).
- **Keep a project on selected servers.** Mark a node tag as a compliance tag, and require the tag on a project. Everything that the project runs stays on nodes with the tag. Those nodes do not take work from other projects. See [Compliance node tags](/docs/guides/compliance-tags/).
- **Watch what runs.** Use metrics, live logs with history, a terminal inside a service, and a query box for databases. The **Usage** page also shows charts of the health of the operator. See [Observability](/docs/guides/observability/) and [Usage and capacity](/docs/guides/usage-and-capacity/).
- **Isolate projects.** A project is private by default: other projects cannot reach its services unless you allow it. See [Network isolation](/docs/guides/network-isolation/).
- **Automate.** You can use three methods. All three use the same access rules as the dashboard:
  - The `geass` [command line](/docs/guides/command-line/). Install it with `install-cli.sh`. Sign in with `geass login`, and approve a code in the dashboard. Then run commands such as `geass deploy web --wait`. This command exits with a code that a script can act on. See [CLI commands](/docs/reference/cli/).
  - An [MCP server](/docs/guides/mcp-server/), `geass mcp`. With it, an AI agent can inspect, deploy, and scale services. The agent sees only the tools that its token allows. See [MCP tools](/docs/reference/mcp-tools/).
  - The versioned JSON API under `/api/v1`. You call it with an API token. An OpenAPI document describes it. See the [REST API](/docs/reference/rest-api/).

  An API token has one of these access levels: full access, read-only with logs, read-only, or limited to one environment of one project. See [Users and API tokens](/docs/guides/users-and-tokens/). Geass also sends signed webhooks for deployment events, and it makes [pull request environments](/docs/guides/pull-request-environments/).

## What you run and what Geass runs

| You | Geass |
| --- | --- |
| The servers, their operating system, and security updates | Installs K3s, the operator, and the dashboard |
| The network: firewall, ports 80 and 443, and DNS | Installs and connects the add-ons: certificate manager, metrics, storage, logs, and the serverless add-on when you turn it on |
| Capacity: when to add a server, unless you set up a node pool | Creates and updates Kubernetes objects from what you deploy. With a pool, creates and deletes machines in your cloud account, within the maximum that you set |
| Your accounts at AWS, Cloudflare, GitHub, PlanetScale, and the clouds that make machines | Calls those accounts with the credentials that you give it, only for the features that you set up |
| The decision when to move to a new release, and the release notes | Updates in place when you run the installer: it keeps the data of the cluster, takes an etcd snapshot first, and refuses a downgrade. It also backs up databases to your S3 bucket when you turn on backups |

## When Geass fits, and when it does not

Geass fits when you own or rent a few servers, and you run web services and databases on them. It also fits when you want to operate them through a dashboard, not by hand. Geass starts on one server and grows when you join more servers.

Know these limits before you commit:

- **It is not a managed service.** Nobody runs your servers for you, and there is no uptime guarantee.
- **A new install has one control plane server.** The installer builds a single K3s server and a single operator. If you lose that server, the dashboard stops, and everything that the operator does stops. You can join two more servers, and then the cluster continues after the loss of one server. But Geass runs one operator. When the server of the operator stops, the operator starts on another server in about two minutes. A PostgreSQL, MySQL, or MariaDB database can run as three instances on three nodes. See [High availability](/docs/operations/high-availability/).
- **You run the updates, on the control plane.** The installer updates a running cluster in place, but nothing starts an update automatically. There is no automatic update. The only rollback is to restore the snapshot that the installer takes first. See [Upgrade Geass](/docs/operations/upgrading/).
- **HTTPS needs a step from you.** Geass creates the Let's Encrypt certificate issuer only when it has a contact email address. Give the installer `GEASS_ACME_EMAIL`, or set the address on the cluster later. Without an address, Geass issues no HTTPS certificate. Ports 80 and 443 must also be open. See [Install Geass](/docs/getting-started/install/#https-certificates) and [Domains and TLS](/docs/guides/domains-and-tls/).
- **Two roles for people.** Dashboard users are either admins or viewers. They are in a Kubernetes Secret, or they come from an identity provider that you connect. Invitations, organizations, and teams stay in that identity provider. API tokens add a third role: read-only, with access to logs. See [Sign in to the dashboard](/docs/getting-started/sign-in/) and [Users and API tokens](/docs/guides/users-and-tokens/).
- **Servers at different providers are experimental.** See [Multi-provider clusters](/docs/operations/multi-provider/).
- **The creation of servers is experimental.** Geass can create machines at 11 clouds and through a webhook, alone or in an autoscaling pool. But only the Amazon EC2 driver has run against a live account. The other drivers were written from the documentation of the provider and tested against a simulation. Machines that Geass creates cost money at your cloud until they are removed. You can also use servers that you already have, and Geass joins them over SSH. See [Cloud connections for machines](/docs/guides/connectors/clouds/#how-far-to-trust-it).
- **It needs the internet.** The installer and the add-ons download from public hosts. There is no offline install.

## How it compares to a managed cloud

The workflow is the same in spirit. You describe what you want, review it, and deploy it. Then you read its status, metrics, and logs in one place. Databases come with backups, and files come with buckets. The difference is where it runs and who is responsible:

- A managed cloud runs the machines and the platform for you, and it bills you for them.
- Geass is software that you run on machines that you pay for directly. You do the operations, and you keep the control and the data.

## License

Geass is licensed under the [Apache License 2.0](https://www.apache.org/licenses/LICENSE-2.0). The full text is in the `LICENSE` file in the repository. In short, you can use, copy, modify, and redistribute Geass for any purpose, including commercially and as a hosted service. You must do these things:

- Keep the copyright and license notices.
- Include the license with each copy that you redistribute.
- Mark the files that you changed.

Geass is open source as the Open Source Initiative defines it. If you are not sure that the license covers your use, read the license or ask a lawyer. This page is not legal advice.

## Where to start

1. Check the [requirements](/docs/getting-started/requirements/).
2. [Install Geass](/docs/getting-started/install/).
3. [Sign in to the dashboard](/docs/getting-started/sign-in/).
4. [Deploy your first app](/docs/getting-started/first-app/).
5. Read [How Geass works](/docs/overview/how-it-works/) and [Core concepts](/docs/overview/concepts/), or the [FAQ](/docs/overview/faq/).
