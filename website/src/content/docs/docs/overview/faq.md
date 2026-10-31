---
title: "FAQ"
description: "Answers to the questions that newcomers ask: servers, installing, upgrades, data, backups, cloud accounts, security, licensing, and uninstalling."
---

These answers describe what Geass does today. When a question has a longer page, the answer links to it.

## The basics

### Is Geass a managed service?

No. Geass is software that you run on servers that you own or rent. Nobody operates those servers for you. See
[What is Geass](/docs/overview/introduction/#license).

### Do I need to know Kubernetes?

No. You create projects, services, databases, and buckets in the dashboard or with the `geass` command line.

Geass runs on K3s, a small Kubernetes, but you do not use K3s yourself. A few tasks have no dashboard option or command
yet. The page of each task tells you what to do. See
[How Geass works](/docs/overview/how-it-works/).

### Is there a command line or an MCP server?

Yes, both, in one program called `geass`. With the command line, you can deploy, scale, and roll back services, read
logs and metrics, and edit variables. `geass mcp` gives the same operations to AI agents as a Model Context Protocol
server. You install `geass` on your own computer after the install. See [Command line](/docs/guides/command-line/) and
[MCP server for AI agents](/docs/guides/mcp-server/).

### Can I automate Geass or let an AI agent run it?

Yes. The command line, the MCP server, and scripts use the `/api/v1` API with an API token. A script or an agent has
exactly the rights of its token:

- For a script, set `GEASS_URL` and `GEASS_TOKEN`, and run commands such as `geass deploy web --wait`.
- For an agent, run `geass mcp` with a token of its own. A **Read only, with logs** token can watch but cannot change
  anything.
- A project token limits a deploy job to one environment of one project.

See [Users and API tokens](/docs/guides/users-and-tokens/) and the [REST API](/docs/reference/rest-api/).

### How does the command line sign in?

Run `geass login --url https://geass.example.com`. The command shows a code. An admin approves that code in the
dashboard, and selects the access level and how long it lasts. You enter no password in the terminal. See
[Sign in from the command line](/docs/getting-started/sign-in/#sign-in-from-the-command-line).

### Does Geass send data to its authors?

No. Geass has no telemetry and no analytics. Geass connects only to these hosts:

- GitHub and `get.k3s.io`, for the install, and public hosts for container images and add-ons.
- The services that you connect: GitHub, Cloudflare, AWS, PlanetScale, and clouds for machines.
- Let's Encrypt, after you set a certificate email.
- `api.ipify.org` or `ifconfig.me`, only when Geass needs the public address of the server and cannot find it.

The `geass` command line talks only to the address that you give it. See
[Requirements](/docs/getting-started/requirements/#outbound-network-access).

### Which license does Geass use, and can I use it at work?

Geass uses the [Apache License 2.0](https://www.apache.org/licenses/LICENSE-2.0). You can use, change, and give away
Geass for any purpose, at work and commercially included. You must keep the copyright and license notices, include the
license with any copy that you give away, and say which files you changed. If something is not clear, read the license
or ask a lawyer.

## Servers

### Do I need one server or several?

One server is a complete install. To get more capacity, add servers that you have, or let Geass create machines at a
cloud. See [Add servers](/docs/getting-started/add-servers/) and
[Node pools and autoscaling](/docs/guides/node-pools/).

### Is there a highly available control plane?

Yes, after you add two more control plane servers. A new install has one. If that server stops, the dashboard stops
until it returns. With three, the cluster continues after the loss of one server.

A PostgreSQL, MySQL, or MariaDB database can also run as three copies on three servers. See
[High availability](/docs/operations/high-availability/).

### Can I install Geass on a machine that already runs Kubernetes?

No. On a machine that has K3s already, the installer stops and says why. There are two exceptions:

- If Geass already runs there, the installer updates it.
- If you add `--reinstall`, the installer removes that K3s after you confirm. You lose its data.

See [Install Geass](/docs/getting-started/install/#run-the-installer-again).

### Which providers and operating systems work?

Any Linux server on `amd64` or `arm64` that K3s supports, at any provider. Geass can also create machines at Amazon
EC2, Azure, DigitalOcean, Google Cloud, Hetzner, Linode, Oracle Cloud, OVHcloud, Scaleway, UpCloud, and Vultr, or through
a webhook. The creation of machines is experimental. A cluster with servers at different providers is also experimental.
See [Requirements](/docs/getting-started/requirements/) and
[Cloud connections for machines](/docs/guides/connectors/clouds/).

### Why does the installer reject my 2 GB server?

The installer needs at least 1700 MiB of memory, as the kernel reports it. The kernel reports less than the size of
the plan. Most "2 GB" servers report 1.7 to 1.95 GiB, so they pass. A 1 GB server does not. To check, run `grep MemTotal /proc/meminfo`. The installer also needs 10 GiB
free on `/`. These numbers are a minimum. See [Requirements](/docs/getting-started/requirements/#memory-and-disk).

### Can I run it on a server without a public IP?

Yes. You reach the dashboard with the `ssh` command that the installer prints. To publish services without open ports, use a Cloudflare Tunnel.
That needs a Cloudflare account. The first server must still reach other servers over SSH. See
[Sign in to the dashboard](/docs/getting-started/sign-in/) and [Connect a domain](/docs/getting-started/connect-domain/).

### What happens when the server reboots?

Geass and your services start again with the server. Data on volumes stays on the disk.

### Does it run on ARM?

Yes. Each release has an `arm64` installer with an `arm64` build of Geass.

## Installing and upgrading

### Does the installer set up the dashboard login?

The installer prints a setup link. You open the link, and you choose the username and the password of your admin. The
link works one time, for 24 hours. The installer prints no password. An update prints a link only when the dashboard has
no users. See [Open the dashboard](/docs/getting-started/sign-in/#open-the-dashboard).

### What if I lose the setup link or every password?

Run the `dashboard-link` command of the installer on the server. It prints a new setup link. See
[Get a new setup link](/docs/guides/users-and-tokens/#get-a-new-setup-link).

### How is the dashboard reached after the install?

Before it has a domain, the dashboard is at `https://<public IP>` of the server, with a Let's Encrypt certificate for
that address. The link that the installer prints opens it. Ports 80 and 443 must be open. Then set a domain under
**Settings → Domain**. After the domain is verified, the IP address no longer answers. See
[Open the dashboard](/docs/getting-started/sign-in/#open-the-dashboard).

If the server has no public IPv4 address, the installer prints an `ssh` command and a link to `http://localhost:8085`.

### Do I need a domain?

Not to install Geass, sign in, or run a service. You need a domain to publish services on HTTPS, to give the dashboard
a name, and to connect GitHub. See [Connect a domain](/docs/getting-started/connect-domain/).

### Is HTTPS automatic?

For the dashboard at its IP address, yes. For a domain, yes, after you give Geass an email for Let's Encrypt. Give it to the installer as `GEASS_ACME_EMAIL`, or enter it later
under **Settings** → **Domain** → **Certificate email**. Ports 80 and 443 must be open. With a Cloudflare Tunnel, Cloudflare serves HTTPS instead.
See [Set the certificate email](/docs/getting-started/connect-domain/#1-set-the-certificate-email) and
[Domains and TLS](/docs/guides/domains-and-tls/).

### What does running the installer a second time do?

It updates Geass and keeps your data. Only `--reinstall` deletes the cluster, after you confirm. A reinstall deletes the
projects, services, secrets, users, tokens, and the volumes on that server. See
[Install Geass](/docs/getting-started/install/#run-the-installer-again).

### How do I upgrade Geass?

Run the installer of the new release on the server. It checks the cluster, saves a copy of the cluster state, and
replaces Geass. Your projects, services, databases, and settings stay. To also upgrade K3s, add `--k3s-version`. See
[Upgrade Geass](/docs/operations/upgrading/).

### How do I know which version I run?

Open **Usage** in the left rail. **Version** shows the release of Geass and the version of K3s. The **Nodes** table
shows the K3s version of each server. `geass version` shows the version of the command line and of the cluster.

## Cloud accounts

### What does Geass need from my cloud accounts?

Nothing to install Geass or to deploy a service. Each account adds features:

| Account | Adds | What Geass needs |
| --- | --- | --- |
| Cloudflare | DNS records for the dashboard and services, and Cloudflare Tunnel | An API token with Account → Cloudflare Tunnel → Edit, Zone → DNS → Edit, and Zone → Zone → Read |
| GitHub | Deploys from a repository, push to deploy, and pull request environments | A GitHub App that Geass registers. The dashboard needs a public domain first |
| AWS | S3 buckets and database backups | An access key that can manage S3 buckets and objects, and IAM users with their keys and policy |
| PlanetScale | PlanetScale databases | A service token and the organization name |
| A cloud for machines | Servers and pools that Geass creates (experimental) | Credentials that can create and delete machines. See [Cloud connections for machines](/docs/guides/connectors/clouds/) |

AWS and PlanetScale credentials belong to one project. Credentials for machines belong to the cluster, and only an admin
can use them. See [Cloud connectors](/docs/guides/connectors/).

### Does Geass create servers, or work with Google Cloud or Azure?

Yes, as an experimental feature. With a [connection for machines](/docs/guides/connectors/clouds/), Geass creates
servers at the clouds in the previous answer. Click **Add server** on the **Usage** page for one server, or create a
[pool](/docs/guides/node-pools/). Only the Amazon EC2 driver has run against a live account, so start small. A server
from any provider can also join over SSH.

## Data and backups

### Where is my data?

| Data | Where |
| --- | --- |
| Projects, services, settings, and secrets | On the disk of the control plane server |
| PostgreSQL, MySQL, MariaDB, and SQLite data | Volumes on your servers, copied to up to three servers when replicated storage is on |
| Redis data | Memory only. Redis does not save to disk |
| Buckets in the cluster | A volume on your servers |
| AWS S3 buckets and database backups | Your AWS account |
| Metrics and logs | Volumes on your servers: 15 days of metrics and 7 days of logs by default |

See [How Geass works](/docs/overview/how-it-works/#where-state-lives).

### Are my databases backed up?

Only if you turn on backups. Backups go to a bucket in your AWS account:

- Geass backs up PostgreSQL continuously, so you can restore to a recent moment.
- Geass backs up MySQL and SQLite every day.

Redis and external databases have no backups in Geass. See [Backups and restore](/docs/guides/backups-and-restore/).

### Is Geass itself backed up?

Only on the same disk, unless you add a bucket. K3s saves the cluster state twice a day on the disk of the server. Geass
saves one more copy before each update. These copies do not survive the loss of the disk, and they do not include your
database data.

To keep copies off the server, set a bucket in **Cluster backup** on the **Usage** page. The installer also asks about
it. With those copies, you can rebuild the cluster on a new server. See
[Disaster recovery](/docs/operations/disaster-recovery/).

### Can I restore my backups on a new cluster?

Yes, if the bucket still exists. On the new cluster, open **Recover databases from a bucket** on the **Usage** page.
Geass creates the projects again with their old names, and offers each database for restore. Backups from a cluster that
never ran this release cannot be found this way. Before you retire such a cluster, export the databases with their own
tools. See [Disaster recovery](/docs/operations/disaster-recovery/#recover-databases-from-a-bucket).

### What happens to my data when I uninstall?

The cluster state and the volumes on the servers are deleted. Things outside the cluster stay: S3 buckets, backups, DNS
records, the Cloudflare Tunnel, and the GitHub App. To remove an S3 bucket, delete it in the dashboard before you
uninstall. See [Uninstall Geass](/docs/operations/uninstall/).

## Security and access

### Who can sign in, and can I use single sign-on?

By default, each user is an admin or a viewer. An admin adds and removes users under **Settings** → **Users**. See
[Users and API tokens](/docs/guides/users-and-tokens/). For invitations,
sign-in by email, organizations, and teams, connect an OpenID Connect identity provider under **Settings** →
**Sign-in**. Examples are Better Auth, Keycloak, and Okta. See
[Sign in with an identity provider](/docs/guides/external-sign-in/).

There are no permissions for each project. Scripts and agents use API tokens: **Full access**, **Read only, with
logs**, **Read only**, or a project token for one environment. See
[Users and API tokens](/docs/guides/users-and-tokens/).

### Are my secrets safe?

On a cluster that the installer built, secret variables are encrypted on disk. The dashboard never shows them after
you save them. Viewers do not see any
variable values. A **Read only, with logs** token sees plain variables and every log line, so do not print secrets to
the log. Dashboard passwords are stored as hashes. Anyone with root access to the control plane server can read the
secrets, so limit that access. See [Security](/docs/operations/security/).

### Can one project reach another?

No, by default. Each project environment accepts traffic only from its own project, from Geass, and from visitors on
your domains. To allow another project, open **Settings** → **Network** of the project that receives the traffic. See
[Network isolation](/docs/guides/network-isolation/).

## Scaling and placement

### Can I scale to zero?

Yes. Turn on **Sleep when idle** for a service. After a time without requests, Geass stops the service. The next
request to its public address starts it again, and that request waits a few seconds. The service needs a public domain.
Calls from other services do not wake it. The service cannot also use CPU autoscaling. Databases and buckets do not
sleep.

The feature needs an add-on that you turn on one time, under **Settings** → **Add-ons**. See
[Sleep when idle](/docs/guides/scale-to-zero/).

### Can I keep a project on certain servers for compliance?

Yes, with compliance tags:

1. On the **Usage** page, click **Edit** on each server that qualifies. Add a tag, for example `hipaa`, and mark it as a
   compliance tag.
2. In the project, require the tag under **Settings** → **Compliance**.

Then the project runs only on those servers, its databases, buckets, and backups included. Those servers take no work
from other projects. Compliance tags do not move data that exists already, and they do not protect against a
compromised server. See [Compliance node tags](/docs/guides/compliance-tags/).

### Can the cluster add servers by itself when it is full?

Yes, with an autoscaling [pool](/docs/guides/node-pools/). When services cannot start because there is no room, the
pool adds servers, up to a maximum that you set. It removes idle servers. Pools are experimental, and a cloud pool
costs money. Without a pool, services wait until you add a server.

### Can I run code on a schedule?

Yes, with a function. A function can have up to 8 schedules, each a cron expression in UTC. See
[Functions](/docs/guides/functions/). Other kinds of service have no schedules.

## Operating

### Where are the logs?

In the dashboard, open the **Logs** tab of a project, or **View logs** on the **Deployments** tab of a service. From a
terminal, run `geass logs web -f`. To read the logs, you need an admin login or a **Read only, with logs** token. See
[Observability](/docs/guides/observability/).

### Something is not working.

Start with [Troubleshooting](/docs/operations/troubleshooting/). Each getting-started page also ends with its known
problems: [Install Geass](/docs/getting-started/install/#troubleshooting),
[Sign in to the dashboard](/docs/getting-started/sign-in/#troubleshooting),
[Add servers](/docs/getting-started/add-servers/#troubleshooting), and
[Deploy your first app](/docs/getting-started/first-app/#troubleshooting).

## Related

- [What is Geass](/docs/overview/introduction/)
- [How Geass works](/docs/overview/how-it-works/)
- [Core concepts](/docs/overview/concepts/)
- [Command line](/docs/guides/command-line/)
- [Glossary](/docs/reference/glossary/)
