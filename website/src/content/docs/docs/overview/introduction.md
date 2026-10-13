---
title: "What is Geass"
description: "Geass is a source-available platform that gives servers you own the workflow of a managed cloud: projects, deploys, databases, buckets, backups and logs."
---

Geass gives a VPS, a bare-metal machine, or a handful of both the workflow of a managed cloud. You install it on a Linux server with one command. It sets up K3s (a lightweight Kubernetes), a Kubernetes operator and a web dashboard that is built into the operator. You then create projects, add services, databases and buckets in the dashboard, review what you changed, and choose **Deploy changes**. The same work can be done from a terminal with the `geass` command line, or by an AI agent through its MCP server. The servers, the data and the bill stay yours.

Geass is source-available under the Elastic License 2.0. It is not a hosted service: you operate the servers it runs on. This page says who it suits, what you can do with it, and where it does not fit.

## Who it is for

- Developers and small teams who run on VPS or bare-metal servers because a managed cloud costs more, and who want projects, deploys, managed-style databases, backups, metrics and logs without assembling them from separate tools.
- Teams that need their data and workloads on hardware they control, in a place they choose.
- People who are comfortable with Linux servers: SSH, a firewall, DNS. Daily use of Geass needs no Kubernetes knowledge. Fixing a broken server does sometimes, and the docs give you the `kubectl` commands (through `k3s kubectl`) when it comes to that.

## What you can do

- **Organize work** into [projects and environments](/docs/guides/projects-and-environments/), such as `production` and `staging`. Each environment is isolated in its own Kubernetes namespace.
- **Deploy services** from a Docker image, from a GitHub repository that Geass builds in the cluster with Kaniko, or as a [function](/docs/guides/functions/): a single file that runs on Bun, Node.js or Python, either as a server or as an event handler that Geass serves HTTP for, and that can be called on a cron schedule. See [Apps](/docs/guides/apps/) and [GitHub and builds](/docs/guides/github-and-builds/).
- **Review before you deploy.** New resources are drafts. Changes wait as pending until you choose **Deploy changes**, and some settings, such as replicas and domains, take effect at once. See [Projects and environments](/docs/guides/projects-and-environments/#pending-changes-and-deploy-changes).
- **Start from a template**, or save your own. See [Templates](/docs/guides/templates/).
- **Run databases**: PostgreSQL, MySQL, Redis and SQLite in the cluster, with [shared database servers](/docs/guides/shared-database-servers/) for many small databases, and connect apps to external databases you host elsewhere, such as AWS RDS or PlanetScale. See [Databases](/docs/guides/databases/).
- **Back up** Postgres, MySQL and SQLite databases to a bucket in your own AWS account. Postgres is backed up continuously, so you can restore it to any recent moment; MySQL and SQLite are copied daily. Restores create a new database, and a deleted database stays restorable for 7 days. See [Backups and restore](/docs/guides/backups-and-restore/).
- **Store files** in buckets, either on an in-cluster object storage server (SeaweedFS or Garage) or in AWS S3. See [Object storage](/docs/guides/object-storage/).
- **Resize database volumes** on Longhorn storage. See [Storage](/docs/guides/storage/).
- **Publish on your domain.** Give a service a hostname, or connect Cloudflare and let Geass assign one, publish it through a Cloudflare Tunnel and create its DNS record. Certificates for HTTPS come from Let's Encrypt. See [Domains and TLS](/docs/guides/domains-and-tls/).
- **Scale and place.** Set replicas and CPU autoscaling, and tag servers with a region, a site and tags to decide where workloads run. See [Scaling and placement](/docs/guides/scaling-and-placement/).
- **Sleep when idle.** Stop a service after it has had no requests for a time you choose, and start it again on the next request, so a small cluster can hold many services that are used now and then. It is opt-in: it needs the serverless add-on, which Geass installs only when you turn it on for the cluster. See [Sleep when idle](/docs/guides/scale-to-zero/).
- **Keep a project on chosen servers.** Mark a node tag as a compliance tag and require it on a project: everything the project runs stays on nodes with the tag, and those nodes take no other project's work. See [Compliance node tags](/docs/guides/compliance-tags/).
- **Watch what runs.** Metrics, live logs with history, a terminal inside a service and a query box for databases, and charts of the operator's own health on the **Usage** page. See [Observability](/docs/guides/observability/) and [Usage and capacity](/docs/guides/usage-and-capacity/).
- **Isolate projects.** A project is private by default: other projects cannot reach its services unless you allow it. See [Network isolation](/docs/guides/network-isolation/).
- **Automate.** Three ways in, all with the same access rules as the dashboard:
  - The `geass` [command line](/docs/guides/command-line/). Install it with `install-cli.sh`, sign in with `geass login` by approving a code in the dashboard, then run commands such as `geass deploy web --wait`, which exits with a code a script can act on. See [CLI commands](/docs/reference/cli/).
  - An [MCP server](/docs/guides/mcp-server/), `geass mcp`, so an AI agent can inspect, deploy and scale services. It sees only the tools its token allows. See [MCP tools](/docs/reference/mcp-tools/).
  - The versioned JSON API under `/api/v1`, called with an API token, described by an OpenAPI document. See the [REST API](/docs/reference/rest-api/).

  API tokens can be full access, read-only with logs, read-only, or limited to one environment of one project. See [Users and API tokens](/docs/guides/users-and-tokens/). Geass also sends signed webhooks for deployment events and makes [pull request environments](/docs/guides/pull-request-environments/).

## What you run and what Geass runs

| You | Geass |
| --- | --- |
| The servers, their operating system and security updates | Installs K3s, the operator and the dashboard |
| The network: firewall, ports 80 and 443, DNS | Installs and wires the add-ons: certificate manager, metrics, storage, logs, and the serverless add-on when you turn it on |
| Capacity: when to add a server | Creates and updates Kubernetes objects from what you deploy |
| Your accounts at AWS, Cloudflare, GitHub or PlanetScale | Calls those accounts with the credentials you give it, only for the features you set up |
| Choosing when to move to a new release, and reading its notes | Updates in place when you run the installer: it keeps the cluster's data, takes an etcd snapshot first and refuses a downgrade. It also backs up databases to your S3 bucket when you turn backups on |

## When Geass fits, and when it does not

Geass fits when you own or rent a few servers, run web services and databases on them, and want to operate them through a dashboard rather than by hand. It is built to start on one server and grow by joining more.

Be aware of these limits before you commit:

- **It is not a managed service.** Nobody runs your servers for you, and there is no uptime guarantee.
- **The control plane is one server.** The installer builds a single K3s server and a single operator. Losing that server stops the dashboard and everything the operator does. The only high availability feature is a Postgres database that runs as three instances on three nodes. See [High availability](/docs/operations/high-availability/).
- **Updates are run by you, on the control plane.** The installer updates a running cluster in place, but nothing updates it by itself: there is no automatic update, and no rollback other than restoring the snapshot the installer takes first. See [Upgrade Geass](/docs/operations/upgrading/).
- **HTTPS needs a step from you.** Geass creates the Let's Encrypt certificate issuer only when it has a contact email address: give the installer `GEASS_ACME_EMAIL`, or set the address on the cluster later. Without one, no HTTPS certificate is issued. Ports 80 and 443 must also be open. See [Install Geass](/docs/getting-started/install/#https-certificates) and [Domains and TLS](/docs/guides/domains-and-tls/).
- **Two roles for people and no single sign-on.** Dashboard users live in a Kubernetes Secret and are either admins or viewers. API tokens add a third, read-only role that can also read logs. See [Sign in to the dashboard](/docs/getting-started/sign-in/) and [Users and API tokens](/docs/guides/users-and-tokens/).
- **Servers at different providers are experimental.** See [Multi-provider clusters](/docs/operations/multi-provider/).
- **Geass does not create servers.** It joins servers you already have, over SSH. It has no connector for creating machines at any provider, and no Google Cloud or Azure integration.
- **It needs the internet.** The installer and the add-ons download from public hosts. There is no offline install.

## How it compares to a managed cloud

The workflow is the same in spirit: you describe what you want, review it, deploy it, and read its status, metrics and logs in one place; databases come with backups, and files with buckets. The difference is where it runs and who is responsible. A managed cloud runs the machines and the platform for you and bills for them. Geass is software that you run on machines you pay for directly, so you carry the operations, and you keep the control and the data.

## License

Geass is licensed under the [Elastic License 2.0](https://www.elastic.co/licensing/elastic-license); the full text is the `LICENSE` file in the repository. In short, you may use, copy, modify and deploy Geass for yourself or your organization. You may not offer Geass, or a substantially similar product, to third parties as a hosted or managed service that gives them access to a substantial set of its features. You must keep the license and copyright notices, and mark modified copies. Geass is source-available, not open source in the sense the Open Source Initiative defines. If you are unsure whether your use is covered, read the license or ask a lawyer: this page is not legal advice.

## Where to start

1. Check the [requirements](/docs/getting-started/requirements/).
2. [Install Geass](/docs/getting-started/install/).
3. [Sign in to the dashboard](/docs/getting-started/sign-in/).
4. [Deploy your first app](/docs/getting-started/first-app/).
5. Read [How Geass works](/docs/overview/how-it-works/) and [Core concepts](/docs/overview/concepts/), or the [FAQ](/docs/overview/faq/).
