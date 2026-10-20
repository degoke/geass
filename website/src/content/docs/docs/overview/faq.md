---
title: "FAQ"
description: "Answers to the questions newcomers ask: servers, installing, upgrades, data, backups, cloud accounts, security, licensing and what happens when you uninstall."
---

Each answer here is checked against what Geass does today. Where a question has a longer page behind it, the answer links to it.

## The basics

### Is Geass a managed service?

No. Geass is software that runs on servers you own or rent. Nobody operates those servers for you, and the project does not host a service. The license forbids others from offering Geass as a commercial hosted or managed service. See [What is Geass](/docs/overview/introduction/#license).

### Do I need to know Kubernetes?

Not to use it. You create projects, services and databases in the dashboard. Underneath, Geass runs on K3s, so when something breaks you may read logs or objects with `kubectl`, which on the control plane is `sudo k3s kubectl`. The docs give you the commands. See [How Geass works](/docs/overview/how-it-works/).

### Is there a command-line tool or an MCP server?

Yes, both, in one program called `geass`. The command line lists, creates, deploys, scales and rolls back services, reads logs and metrics, edits variables and calls any API endpoint. `geass mcp` runs the same operations as a Model Context Protocol server for AI agents. They are separate from the installer: you install `geass` on your own machine, after the cluster exists. See [Command line](/docs/guides/command-line/), [MCP server for AI agents](/docs/guides/mcp-server/), [CLI commands](/docs/reference/cli/) and [MCP tools](/docs/reference/mcp-tools/).

### Can I automate Geass or let an AI agent run it?

Yes. Everything the command line and the MCP server do goes through the versioned `/api/v1` API with an API token, so a script, a CI job or an agent has exactly the rights of its token. For a script, install `geass`, set `GEASS_URL` and `GEASS_TOKEN`, and run commands such as `geass deploy web --wait`: it exits with `5` if the deploy fails and `6` if it times out. For an agent, run `geass mcp` and give it its own token. A **Read only, with logs** token lets an agent watch without being able to change anything, and the agent is shown only the tools that token may use. A project token limits a deploy job to one environment of one project. See [Users and API tokens](/docs/guides/users-and-tokens/) and the [REST API](/docs/reference/rest-api/).

### How does the command line sign in?

You run `geass login --url https://geass.example.com`. It prints a short code and the address of an approval page, `/device` on your dashboard. You open it, signed in as an admin, check that the code is the one in your terminal, choose what the command line may do and for how long, and approve. Geass then hands the command line a token once and it is saved on your machine. No password is typed into the terminal, and only a person signed in with a password can approve: a token cannot. See [Sign in to the dashboard](/docs/getting-started/sign-in/#sign-in-from-the-command-line).

### Does Geass send data to its authors?

No. The repository contains no telemetry or analytics code. Geass does make outbound requests that you should expect: the installer downloads from GitHub and `get.k3s.io`; the cluster downloads Helm charts and container images from public hosts; when you connect them, the operator calls the GitHub, Cloudflare, AWS and PlanetScale APIs, and the API of any cloud you connect for machines; it asks `api.ipify.org`, then `ifconfig.me`, for the server's public IP only when no node reports one and it needs the address for dashboard DNS; and cert-manager talks to Let's Encrypt once the certificate issuer exists. The `geass` command line talks only to the Geass address you give it. See [Requirements](/docs/getting-started/requirements/#outbound-network-access).

### Which license does Geass use, and can I use it at work?

The [Functional Source License, Version 1.1, ALv2 Future License](https://fsl.software/) (FSL-1.1-ALv2), in the `LICENSE` file. You may use, copy, modify and redistribute Geass for any purpose except a competing use: making it available to others in a commercial product or service that substitutes for Geass or offers substantially similar functionality, such as a hosted Geass. Running it for your own organization is permitted, and so are non-commercial education and research and professional services for someone who runs it. You must keep its notices and pass the license on with any copy. Two years after each release is published, that release is also available under the Apache License 2.0. Until then it is source-available, not open source in the Open Source Initiative's sense. For anything unclear, read the license or ask a lawyer.

## Servers

### Do I need one server or several?

One is a complete install: the installer builds a K3s server and runs everything on it. You add capacity by joining workers over SSH, or by having Geass create machines at a cloud for you. See [Add servers](/docs/getting-started/add-servers/) and [Node pools](/docs/guides/node-pools/).

### Is there a highly available control plane?

No. There is one K3s server and one operator. If that server goes down, the dashboard and everything the operator does stop until it returns. The one high availability feature is a Postgres database that runs as three instances on three nodes, and Geass only lets one start when the cluster has at least three healthy nodes. See [High availability](/docs/operations/high-availability/).

### Can I install Geass on a machine that already runs K3s or Kubernetes?

Not alongside it. On a machine that already has a K3s, the installer stops and says why, unless Geass is already in it (then it updates) or you pass `--reinstall`, which uninstalls that K3s after a confirmation. Geass also depends on parts that K3s bundles: its Helm controller and the Traefik ingress. The installer is the only documented way to set it up. See [Install Geass](/docs/getting-started/install/#run-the-installer-again).

### Which providers and operating systems work?

Any Linux server on `amd64` or `arm64` that K3s supports, from any provider. The installer checks only that it runs on Linux, on one of those two architectures, with enough memory and disk. You can bring servers from any provider, and Geass can also create machines at Amazon EC2, Azure, DigitalOcean, Google Cloud, Hetzner, Linode, Oracle Cloud, OVHcloud, Scaleway, UpCloud and Vultr, or through a webhook of your own (experimental). A cluster whose nodes are at different providers is experimental too. See [Requirements](/docs/getting-started/requirements/), [Cloud connections for machines](/docs/guides/connectors/clouds/) and [Multi-provider clusters](/docs/operations/multi-provider/).

### Why does the installer reject my 2 GB server?

It requires at least 1700 MiB of `MemTotal`, as the kernel reports it. The kernel reports less than the size on the plan, but a "2 GB" machine reports about 1.7 to 1.95 GiB, so it passes; a 1 GB machine does not. Check with `grep MemTotal /proc/meminfo`. The installer's 2 GiB and 10 GiB are a floor: the add-ons it installs also use CPU, memory and disk. See [Requirements](/docs/getting-started/requirements/#memory-and-disk).

### Can I run it on a server without a public IP?

You can install it and use the dashboard through an SSH tunnel and `kubectl port-forward`. To publish services, Cloudflare Tunnel can carry traffic through `cloudflared` instead of open ports; it needs a Cloudflare account. Workers still need to be reachable over SSH from the control plane. See [Sign in to the dashboard](/docs/getting-started/sign-in/) and [Connect a domain](/docs/getting-started/connect-domain/).

### What happens when the server reboots?

K3s runs as a service that starts at boot, and the operator and your workloads start with it. Data on volumes is on the disk and is still there.

### Does it run on ARM?

Releases include an `arm64` installer, which carries an `arm64` operator image. The add-ons come from upstream Helm charts.

## Installing and upgrading

### Does the installer set up the dashboard login?

No. A fresh install has no user and no password. You create the first user with a Kubernetes Secret and reach the dashboard through a tunnel. Geass generates no password. See [Sign in to the dashboard](/docs/getting-started/sign-in/).

### How is the dashboard reached after the install?

It is a `ClusterIP` Service, so only inside the cluster at first. Use `kubectl port-forward` for the first sign-in, then set a domain under **Settings → Domain**. See [Sign in to the dashboard](/docs/getting-started/sign-in/#reach-the-dashboard).

### Do I need a domain?

Not to install, sign in or run a service: you can reach a service with `kubectl port-forward`. You need hostnames to publish services, to serve the dashboard over HTTPS, and to register the GitHub App. See [Deploy your first app](/docs/getting-started/first-app/#7-find-the-url).

### Is HTTPS automatic?

Not unless you give it an email address. Geass gets certificates from Let's Encrypt through cert-manager, and Let's Encrypt wants a contact address for the account. Pass one to the installer as `GEASS_ACME_EMAIL`, or set `spec.addons.certManager.acmeEmail` on the cluster later, and Geass creates the `letsencrypt-prod` issuer that the Ingress objects it generates ask for. Without an address no certificate is issued. Ports 80 and 443 must also be open, because Let's Encrypt validates a hostname over 80/tcp. With a Cloudflare Tunnel, Cloudflare serves HTTPS to visitors instead. See [HTTPS certificates](/docs/getting-started/install/#https-certificates) and [Domains and TLS](/docs/guides/domains-and-tls/).

### What does running the installer a second time do?

On a machine that runs Geass it updates the cluster and keeps its data. It deletes a cluster only when you pass `--reinstall`, after a confirmation, which uninstalls K3s from that machine and builds a new cluster: that deletes the old cluster's database (projects, services, secrets, users and tokens) and local volumes. See [Install Geass](/docs/getting-started/install/#run-the-installer-again).

### How do I upgrade Geass?

Run the new release's installer on the control plane. It checks the cluster, takes an etcd snapshot, replaces the custom resource definitions and the operator, and waits until the new operator has taken over; your projects, services, databases and settings stay. Add `--k3s-version` to upgrade K3s too, and the operator then upgrades the workers one at a time. See [Upgrade Geass](/docs/operations/upgrading/).

### How do I know which version I run?

The cluster records it. `sudo k3s kubectl -n geass-system get geasscluster` shows the release of the operator in the `RELEASE` column and the control plane's K3s version under `K3S`. A cluster installed before releases were recorded shows neither until it has been updated once. The dashboard shows both on the **Usage** page, under **Version**, and the K3s version of every node in the **Nodes** table. `geass version` prints the command line's own version, then the cluster's API version (`api v1`, which is not the release) and its release, and says when the two releases differ.

## Cloud accounts

### What does Geass need from my cloud accounts?

Nothing to install or to deploy a service. Each account unlocks specific features:

| Account | Unlocks | What Geass needs |
| --- | --- | --- |
| Cloudflare | DNS records for the dashboard and services, Cloudflare Tunnel | An API token with Account → Cloudflare Tunnel → Edit, Zone → DNS → Edit and Zone → Zone → Read |
| GitHub | Repository deploys, push-to-deploy, pull request environments, builds and deploys reported back to GitHub | A GitHub App that Geass registers. The dashboard needs a public domain first |
| AWS | S3 buckets, database backups | An access key that can create and delete S3 buckets and objects, and create and delete IAM users with their keys and policy |
| PlanetScale | PlanetScale databases | A service token and the organization name |
| Amazon EC2, Azure, DigitalOcean, Google Cloud, Hetzner, Linode, Oracle Cloud, OVHcloud, Scaleway, UpCloud, Vultr, or your own webhook | Machines that Geass creates and deletes, for [node pools](/docs/guides/node-pools/) and single servers (experimental) | Credentials that can create and delete machines in a project or account of its own. They differ per cloud: see [Cloud connections for machines](/docs/guides/connectors/clouds/) |

AWS and PlanetScale credentials belong to one project. Credentials for machines belong to the cluster, and only an admin can use them. See [Cloud connectors](/docs/guides/connectors/).

### Does Geass create servers, or work with Google Cloud or Azure?

Yes to both, as an experimental feature. With a [connection for machines](/docs/guides/connectors/clouds/), Geass creates a server at Google Cloud, Azure, Amazon EC2, DigitalOcean, Hetzner, Linode, Oracle Cloud, OVHcloud, Scaleway, UpCloud or Vultr, or through a webhook for any other provider. It can create one server (**Add server** on the **Usage** page, or `geass servers add`) or run a [pool](/docs/guides/node-pools/) that adds machines when pods cannot start and deletes them when they sit idle. Amazon EC2 has run against a live account; every other driver was written from the provider's documentation and tested against a simulation only, so start small. A server at any provider can still join as a node, as before.

## Data and backups

### Where is my data?

| Data | Where |
| --- | --- |
| Geass's own state (projects, services, settings, secrets) | K3s's embedded etcd, on the control plane's disk |
| PostgreSQL, MySQL and SQLite data | Volumes on your nodes: Longhorn, replicated across up to three nodes, or K3s's local-path storage on one node |
| Redis data | Memory only. Redis runs without persistence |
| In-cluster buckets | The object storage server in the cluster (SeaweedFS or Garage), on a volume of its own: with Longhorn replicated across up to three nodes, otherwise on one node |
| AWS S3 buckets and database backups | Your AWS account |
| Metrics and logs | Volumes on your nodes: 15 days of metrics, and 7 days of logs by default |

See [How Geass works](/docs/overview/how-it-works/#where-state-lives).

### Are my databases backed up?

Only if you turn backups on, and only for PostgreSQL, MySQL and SQLite databases that run in the cluster. They go to a bucket in your AWS account: Postgres continuously, so you can restore it to any recent moment, MySQL and SQLite daily. Redis and external databases have no backups in Geass. See [Backups and restore](/docs/guides/backups-and-restore/).

### Is Geass itself backed up?

Only if you ask. K3s's embedded etcd takes its own snapshots (twice a day) in its data directory on the same disk, so they do not survive losing that disk, and Geass takes one more before it updates the cluster, which is the way back from an update that goes wrong (see [Go back](/docs/operations/upgrading/#go-back)). Neither holds your databases' contents, and neither leaves the machine.

You can have K3s send each of its snapshots to an S3 bucket of your own as well: the installer asks, and the answer is no unless you say yes, and **Cluster backup** on the Usage page sets or changes the bucket later. A cluster whose control plane disk is lost can then be rebuilt on a new machine from the newest copy. See [Disaster recovery](/docs/operations/disaster-recovery/).

### Can I restore my backups on a new cluster?

Yes, if the bucket is still there. A restore starts from a record of the backups, and the record used to live only in the cluster that made them. Now each database's backups have a small record of what they are beside them in the bucket, and **Recover databases from a bucket** on the Usage page reads them on a new cluster, makes the projects again under their old names, and offers each database to restore. Backups made before a cluster ran this release get their record a few minutes after it does; ones that never did cannot be found this way, so dump the databases you cannot lose with their own tools before you retire a cluster. See [Disaster recovery](/docs/operations/disaster-recovery/#recover-databases-from-a-bucket).

### What happens to my data when I uninstall?

Uninstalling K3s deletes the cluster's database and its local volumes. Longhorn's replica data stays on disk, unreferenced. Things outside the cluster stay: S3 buckets and backups, DNS records, the Cloudflare tunnel, the GitHub App. Geass removes a bucket's S3 bucket and IAM user only when you delete the bucket in the dashboard first. See [Uninstall Geass](/docs/operations/uninstall/).

## Security and access

### Who can sign in, and can I use single sign-on?

By default users come from a Kubernetes Secret, and each is an admin or a viewer. For invitations, sign-in by email, organizations and teams, connect an identity provider that speaks OpenID Connect (Better Auth, Keycloak, Okta and others) under **Settings** → **Sign-in**: it owns the people and Geass takes an admin or viewer role from its token. See [Sign in with an identity provider](/docs/guides/external-sign-in/). There is no per-project permission. Scripts, the command line and agents use API tokens: **Full access**, **Read only, with logs** (the `observer` role), **Read only**, or a project token scoped to one environment of one project. See [Users and API tokens](/docs/guides/users-and-tokens/).

### Are my secrets safe?

Secret variables are stored as Kubernetes Secrets and are never shown in the dashboard after you save them, and viewers do not see variable values at all. A **Read only, with logs** token sees the names of secrets and the values of plain variables, and every log line, so keep anything sensitive in a secret and do not print it. Dashboard passwords are stored as bcrypt hashes, and repeated failed sign-ins lock a user for a minute. The installer turns on K3s's secrets encryption at rest, so on a cluster it built, Secrets are encrypted in the cluster's database and its etcd snapshots. They are still readable to anyone who can read Secrets through the API, or who can read the control plane's disk, where K3s keeps the key. Limit who has access to the control plane. See [Security](/docs/operations/security/).

### Can one project reach another?

Not by default. Each project environment is isolated by network policies, so only services of the same project, Geass itself and visitors through your domains reach its services. You allow other projects under **Settings → Network** of the receiving project. See [Network isolation](/docs/guides/network-isolation/).

## Scaling and placement

### Can I scale to zero?

Yes, as **Sleep when idle**, for any service: an image, a build from GitHub, or a function. After a set time without requests (30 seconds to 24 hours; the dashboard takes minutes) Geass stops the service, and the next request to its public host starts it again, which waits a few seconds. It is opt-in. The installer does not install what it needs, KEDA and its HTTP add-on, so you turn on the serverless add-on once for the cluster (`spec.addons.serverless.enabled`). The service also needs a public host, because a request to it is what wakes it, and it cannot use CPU autoscaling. Calls from another service to its private address do not wake it. Databases and buckets do not sleep. See [Sleep when idle](/docs/guides/scale-to-zero/).

### Can I keep a project on certain servers for compliance?

Yes, with compliance tags. Tag the nodes that qualify, for example `hipaa`, and mark the tag as a compliance tag on them (**Usage**, then **Edit** on a node). Then require it under the project's **Settings → Compliance**. Everything the project runs, including its databases, buckets, builds and backup jobs, stays on nodes with the tag, those nodes take no other project's work, and the project's log history is not sent to the shared log store. It is about where Geass places work. It does not move data that is already elsewhere (a project with databases has to restore them into new ones first), it does not protect against a compromised node, and the control plane, backups to S3 and external databases sit outside it. See [Compliance node tags](/docs/guides/compliance-tags/).

### Can the cluster add servers by itself when it is full?

Yes, with an autoscaling [node pool](/docs/guides/node-pools/): when pods cannot start for lack of room, it creates machines at a cloud (or joins servers you listed), up to a maximum you set, and removes idle ones. It is experimental, it spends money at the cloud, and its nodes run services only unless you say otherwise. Without a pool, nothing adds nodes: pods wait until you do.

### Can I run code on a schedule?

Yes, with a function. A function can have up to 8 schedules, each a cron expression in UTC that calls it with an HTTP `POST`, and a function can be an event handler that Geass serves HTTP for. Schedules start with the next deploy. See [Functions](/docs/guides/functions/) and [Command line](/docs/guides/command-line/#functions-and-schedules). There is no schedule for other kinds of service.

## Operating

### Where are the logs?

In the dashboard, under each project's **Logs** tab, and in a service's **Deployments** tab (**View logs**). From a terminal, `geass logs web -f` follows a service's logs; it needs an admin or a **Read only, with logs** token. The operator's own log is `sudo k3s kubectl -n geass-system logs deploy/geass-controller-manager -c manager`. The installer's log is a `geass-install-*.log` file. See [Observability](/docs/guides/observability/).

### Where is the kubeconfig?

`/etc/rancher/k3s/k3s.yaml` on the control plane, readable by root. `sudo k3s kubectl` uses it.

### Something is not working.

Start with [Troubleshooting](/docs/operations/troubleshooting/). The getting-started pages each end with the failures that setup step is known for: [Install Geass](/docs/getting-started/install/#troubleshooting), [Sign in to the dashboard](/docs/getting-started/sign-in/#troubleshooting), [Add servers](/docs/getting-started/add-servers/#troubleshooting) and [Deploy your first app](/docs/getting-started/first-app/#troubleshooting).

## Related

- [What is Geass](/docs/overview/introduction/)
- [How Geass works](/docs/overview/how-it-works/)
- [Core concepts](/docs/overview/concepts/)
- [Command line](/docs/guides/command-line/)
- [Glossary](/docs/reference/glossary/)
