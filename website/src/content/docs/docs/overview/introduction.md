---
title: "What is Geass"
description: "Geass is open-source software that gives your own servers the workflow of a managed cloud: projects, deploys, databases, buckets, backups, and logs."
---

Geass gives a VPS, a bare-metal machine, or a group of servers the workflow of a managed cloud. You install Geass on a
Linux server with one command. Then you work in a web dashboard:

1. Create a project.
2. Add services, databases, and buckets to it.
3. Review what changed, and click **Deploy changes**.

Geass runs what you deployed. It connects your services to their databases, gives them domains and HTTPS, backs up
their data, and shows you their logs and metrics. You keep the servers, the data, and the bill.

You can do the same work from a terminal with the `geass` command line, or an AI agent can do it through the MCP
server.

## Who it is for

- Developers and small teams that rent a VPS or bare-metal servers because a managed cloud costs too much. They want
  deploys, databases, backups, metrics, and logs without the work of putting separate tools together.
- Teams that must keep their data on hardware that they control, in a location that they select.
- People who can set up a Linux server: SSH, a firewall, and DNS. You do not need to know Kubernetes.

## What you can do

- **Organize your work** into [projects and environments](/docs/guides/projects-and-environments/), such as
  `production` and `staging`. Each environment is isolated from the others.
- **Deploy services** from a Docker image or from a GitHub repository. Geass builds the repository for you. See
  [Services](/docs/guides/apps/) and [Deploy from GitHub](/docs/guides/github-and-builds/).
- **Run a function.** A [function](/docs/guides/functions/) is one file of code that runs on Bun, Node.js, or Python,
  with no Dockerfile. A schedule can call it.
- **Review before you deploy.** New services are drafts, and changes stay pending until you click **Deploy changes**.
  See [Projects and environments](/docs/guides/projects-and-environments/#pending-changes-and-deploy-changes).
- **Start from a template**, or save your own. See [Templates](/docs/guides/templates/).
- **Run databases.** Run PostgreSQL, MySQL, MariaDB, Redis, and SQLite on your servers, or connect a database that you
  host somewhere else. Geass gives your services the connection details. See [Databases](/docs/guides/databases/).
- **Back up your data** to a bucket in your own AWS account, and restore a backup to a new database. See
  [Backups and restore](/docs/guides/backups-and-restore/).
- **Store files** in buckets on your servers or in AWS S3. See [Object storage](/docs/guides/object-storage/).
- **Publish on your domain** with HTTPS. Or connect Cloudflare, and Geass gives each service a hostname. See
  [Domains and TLS](/docs/guides/domains-and-tls/).
- **Deploy on every push**, with a preview environment for each pull request. See
  [Pull request environments](/docs/guides/pull-request-environments/).
- **Scale.** Set replicas and autoscaling, and select the region where a project runs. See
  [Scaling and placement](/docs/guides/scaling-and-placement/).
- **Sleep when idle.** Stop a service that nobody uses, and start it again on the next request. See
  [Sleep when idle](/docs/guides/scale-to-zero/).
- **Watch what runs.** Read metrics and live logs, open a terminal in a service, and query a database. Get an
  [alert](/docs/guides/alerts/) in Slack or by email when something breaks. See
  [Logs, metrics, and terminal](/docs/guides/observability/).
- **Add servers.** Add a server that you have, or let Geass create one at a cloud. See
  [Add servers](/docs/getting-started/add-servers/).
- **Automate.** Use the [command line](/docs/guides/command-line/), the [MCP server](/docs/guides/mcp-server/), or the
  [REST API](/docs/reference/rest-api/). Each one uses an API token with the access level that you select. See
  [Users and API tokens](/docs/guides/users-and-tokens/).

## What you do and what Geass does

| You | Geass |
| --- | --- |
| Rent or own the servers, and keep their operating system up to date | Installs and runs everything that your services need on those servers |
| Open ports 80 and 443, and set up DNS | Gets HTTPS certificates and routes traffic to your services |
| Decide when to add a server, or set a limit for a [node pool](/docs/guides/node-pools/) | Places your services on the servers that have room, and adds or removes pool servers within your limit |
| Give Geass access to your accounts at AWS, Cloudflare, GitHub, and other clouds | Uses those accounts only for the features that you set up |
| Decide when to update to a new release | Updates in place when you run the installer again, and keeps your data |

## When Geass fits, and when it does not

Geass fits when you own or rent a few servers, and you run web services and databases on them. It starts on one server,
and it grows when you add more.

Know these limits before you commit:

- **It is not a managed service.** Nobody runs your servers for you, and there is no uptime guarantee.
- **A new install has one main server.** If you lose that server, the dashboard stops until it comes back. To survive
  the loss of one server, add two more main servers. See [High availability](/docs/operations/high-availability/).
- **You start the updates.** Nothing updates Geass automatically. To update, run the installer again. See
  [Update Geass](/docs/operations/upgrading/).
- **HTTPS needs an email address.** Let's Encrypt needs a contact address before Geass can get certificates. See
  [HTTPS certificates](/docs/getting-started/install/#https-certificates).
- **Two roles for people.** Dashboard users are admins or viewers. To invite people or to use teams, connect an identity
  provider. See [Sign in with an identity provider](/docs/guides/external-sign-in/).
- **Some features are experimental.** Servers at different providers in one cluster, and servers that Geass creates at a
  cloud, are experimental. Only the Amazon EC2 driver has run against a live account. See
  [Cloud connections for machines](/docs/guides/connectors/clouds/#how-far-to-trust-it).
- **It needs the internet.** The installer downloads from public hosts. There is no offline install.

## How it compares to a managed cloud

The workflow is the same. You describe what you want, review it, and deploy it. Then you read its status, metrics, and
logs in one place. The difference is where it runs and who is responsible:

- A managed cloud runs the machines and the platform for you, and it bills you for them.
- Geass is software that you run on machines that you pay for directly. You keep the control and the data.

## Under the hood

Geass installs [K3s](https://k3s.io/), a small Kubernetes distribution, and runs on it. Each project environment is a
Kubernetes namespace, and each service is a Kubernetes workload. You never have to use Kubernetes directly. If you want
the details, see [How Geass works](/docs/overview/how-it-works/).

## License

Geass is licensed under the [Apache License 2.0](https://www.apache.org/licenses/LICENSE-2.0). The full text is in the
`LICENSE` file in the repository. In short, you can use, copy, modify, and redistribute Geass for any purpose, including
commercially and as a hosted service. You must do these things:

- Keep the copyright and license notices.
- Include the license with each copy that you redistribute.
- Mark the files that you changed.

If you are not sure that the license covers your use, read the license or ask a lawyer. This page is not legal advice.

## Where to start

1. Check the [requirements](/docs/getting-started/requirements/).
2. [Install Geass](/docs/getting-started/install/).
3. [Open the dashboard](/docs/getting-started/sign-in/).
4. [Deploy your first app](/docs/getting-started/first-app/).
5. [Put it on your domain](/docs/getting-started/connect-domain/).
