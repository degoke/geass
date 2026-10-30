---
title: "Glossary"
description: "Short definitions of the terms in Geass, from admin and API token to environment, region, site, and shared database server, each with a link."
---

Each entry is a short definition and a link to the page that explains it. Names in code format are the resource kinds or
fields behind the term. Sometimes the dashboard uses a friendlier word for the same thing.

## A

**ACME email.** The contact address for the Let's Encrypt account of the cluster, kept as `spec.addons.certManager.acmeEmail`. The installer reads it from `GEASS_ACME_EMAIL`. Without it, Geass creates no certificate issuer. See [HTTPS certificates](/docs/getting-started/install/#https-certificates).

**Add-on.** A platform component that Geass installs into the cluster through the Helm controller of K3s. Examples are cert-manager, monitoring, Longhorn, logging, and the serverless add-on. See [Configuration](/docs/reference/configuration/#geasscluster).

**Admin.** A dashboard user, or a workspace token, that can change everything. The other roles are viewer and, for tokens only, observer. See [Users and API tokens](/docs/guides/users-and-tokens/#roles).

**Alert channel.** A Slack channel or a list of email addresses that gets a message from Alertmanager. Alertmanager sends the message when a build or deploy fails, a service is down, backups fail, a pod crashes, storage fills, or a node stops responding. The channel has filters for the alerts, projects, and environments that it hears. See [Alerts](/docs/guides/alerts/).

**API token.** A credential for scripts, CI, the command line, and agents, sent as a Bearer token. An API token is either a workspace token (`gsw_`) or a project token (`gsp_`). See [Users and API tokens](/docs/guides/users-and-tokens/#api-tokens).

**API v1.** The versioned HTTP API under `/api/v1` that the command line, the MCP server, and scripts use. An OpenAPI document describes this API. See [REST API](/docs/reference/rest-api/).

**App.** The resource kind (`GeassApp`) behind what the dashboard calls a service. See [Apps](/docs/guides/apps/).

## B

**Backup archive.** The record (`GeassBackupArchive`) of the backups of one database. The record outlives the database for 7 days, so the database can still be restored. See [Backups and restore](/docs/guides/backups-and-restore/).

**Backup storage.** The AWS bucket where the databases of a project keep their backups. Geass reaches this bucket through an AWS connection. See [Backups and restore](/docs/guides/backups-and-restore/).

**Base environment.** The environment that Geass copies for each pull request. See [Pull request environments](/docs/guides/pull-request-environments/).

**Bucket.** S3-compatible storage for a project, kept as a `GeassObjectStore`. A bucket is either on the object storage server of the cluster (SeaweedFS or Garage) or in AWS S3. See [Object storage](/docs/guides/object-storage/).

**Build.** A repository build, run as a Kaniko Job and recorded as a `GeassBuild`. See [GitHub and builds](/docs/guides/github-and-builds/).

## C

**Capacity gate.** The check that refuses a deploy when the cluster does not have the CPU and memory that the change asks for. See [Usage and capacity](/docs/guides/usage-and-capacity/).

**Catalog.** The self-hosted software that ships with Geass, such as Gatus and Infisical, as templates that you can deploy in one step. See [Templates](/docs/guides/templates/#the-catalog).

**CLI.** The `geass` command line, which deploys and runs services from a terminal by calling the `/api/v1` API. See [Command line](/docs/guides/command-line/) and the [CLI reference](/docs/reference/cli/).

**Cloud connection.** Credentials for a cloud, kept as a `GeassCloudConnection`. A cloud connection is either the AWS or PlanetScale connection of a project, or a connection for machines. A connection for machines has the purpose `Compute` and belongs to the cluster. See [Cloud connectors](/docs/guides/connectors/).

**Cloudflare Tunnel.** An optional way to publish the dashboard and services through Cloudflare. The tunnel uses a `cloudflared` Deployment instead of open ports. See [Connect Cloudflare](/docs/guides/connectors/cloudflare/).

**Cluster.** The K3s cluster that Geass runs and manages, represented by the `GeassCluster` named `default`. See [How it works](/docs/overview/how-it-works/).

**Cluster object storage.** The single S3 server, SeaweedFS or Garage, that holds in-cluster buckets. Every project shares this server. An admin selects one of the two servers when they set it up. See [Object storage](/docs/guides/object-storage/).

**Cluster size.** The PlanetScale size that a database is created with, kept as `spec.clusterSize`. The size cannot change after the database exists. See [Connect PlanetScale](/docs/guides/connectors/planetscale/#choosing-a-size).

**ClusterIssuer.** The cert-manager issuer named `letsencrypt-prod`. Geass creates it from the ACME email, and every HTTPS Ingress asks it for its certificate. See [HTTPS certificates](/docs/getting-started/install/#https-certificates).

**Can use up to.** The most CPU or memory that a service can use when its server has room. You set it beside what the service reserves. By default, it is the same as what is reserved. See [Apps](/docs/guides/apps/#scale).

**Compliance tag.** A node tag, such as `hipaa`, that is always reserved and that a project can require. When a project requires the tag, everything the project runs stays on those nodes. See [Compliance tags](/docs/guides/compliance-tags/).

**Connection for machines.** A cloud connection with the purpose `Compute`. Geass uses its credentials to create and delete servers at a cloud or through a webhook. The clouds are Amazon EC2, Azure, DigitalOcean, Google Cloud, Hetzner, Linode, Oracle Cloud, OVHcloud, Scaleway, UpCloud, and Vultr. The connection belongs to the cluster, never to a project. Only an admin can use it. It is experimental. See [Cloud connections for machines](/docs/guides/connectors/clouds/).

**Connector.** A card in **Settings** → **Connectors** (GitHub, Cloudflare, AWS, or PlanetScale). Connector is the word that the dashboard uses for a connection. See [Cloud connectors](/docs/guides/connectors/).

**Console session.** The audit record (`GeassConsoleSession`) of a terminal opened in a service. See [Security](/docs/operations/security/#auditing).

**Control plane.** The node that runs the K3s server, the operator, and the dashboard. The installer creates it. See [How it works](/docs/overview/how-it-works/).

## D

**Dashboard.** The web interface, embedded in the operator binary and served on port 8085. See [Introduction](/docs/overview/introduction/).

**Dashboard URL.** The public HTTPS address of the dashboard, `https://<subdomain>.<root domain>`, set in **Settings** → **Domain**. See [Domains and TLS](/docs/guides/domains-and-tls/).

**Default region.** The region that projects use when they do not select one. See [Scaling and placement](/docs/guides/scaling-and-placement/).

**Deploy.** The step that applies a draft or pending change to the running cluster. You do this step with **Deploy changes** or with the API. See [Projects and environments](/docs/guides/projects-and-environments/).

**Deployment record.** A `GeassDeployment` that remembers one deploy of a service. Geass uses it for history and rollback. See [Apps](/docs/guides/apps/).

**Device code.** The short code, such as `BCDF-GHJK`, that `geass login` shows. An admin approves the code on the `/device` page of the dashboard, and the command line then gets an API token. See [Users and API tokens](/docs/guides/users-and-tokens/#sign-in-the-command-line).

**Display name.** The label that the dashboard shows for a resource, kept in the annotation `geass.dev/display-name`. For a project, the display name is in `spec.displayName`. See [Resources overview](/docs/reference/resources/overview/).

**Draft.** A resource that is created and saved but not deployed yet. See [Projects and environments](/docs/guides/projects-and-environments/).

## E

**Environment.** An isolated copy of the resources of a project, such as `production` or `staging`, with its own namespace. See [Projects and environments](/docs/guides/projects-and-environments/).

**Environment quota.** An optional cap on the CPU, memory, disk, and pods that each environment of a project can ask for. A **project quota** caps the project as a whole, added up over its environments. Either quota keeps one project from using all the room in the cluster. See [Project limits](/docs/guides/project-limits/).

**Environment variable.** A named value given to a service, as plain config, a secret, or a shared variable. See [Environment variables](/docs/guides/environment-variables/).

**Ephemeral node.** A node that keeps data off itself. It is usually a node of a pool whose `storage` is `None`. Services run there, but databases, buckets, and volumes never do, so the node can be removed at any time. It carries the taint `geass.dev/ephemeral`. See [Node pools](/docs/guides/node-pools/#what-runs-on-a-pools-nodes).

**Event function.** A function in event mode. You export a handler that gets an event for each request or schedule, and Geass serves HTTP for it. See [Functions](/docs/guides/functions/).

## F

**Function.** A service whose source is a single file that runs on a managed language runtime (Bun, Node, or Python). A function runs either as a server or as an event handler. See [Functions](/docs/guides/functions/).

## G

**Garage.** One of the two servers that Geass can run as the object storage of the cluster. Garage is a small server that suits low-memory nodes and plain uploads. See [Choosing SeaweedFS or Garage](/docs/guides/object-storage/#choosing-seaweedfs-or-garage).

**GitHub App.** The one app, registered in GitHub, that Geass uses to read repositories, receive pushes, and build. Geass also uses it to report builds, deploys, and pull request environments back to GitHub. See [Connect GitHub](/docs/guides/connectors/github/).

## H

**High availability.** A database topology with three instances. Geass allows it only when enough healthy nodes exist. See [High availability](/docs/operations/high-availability/).

**HA readiness.** The check (`GeassHAReadiness`) that reports whether the cluster has the nodes and storage for high availability. See [GeassHAReadiness](/docs/reference/resources/geass-ha-readiness/).

## I

**Inventory pool.** A node pool that draws on a list of servers that you own, instead of a cloud. Geass joins one of the servers when the pool needs room. Geass removes K3s from the server when the pool does not need it. See [Node pools](/docs/guides/node-pools/#pools-of-servers-you-own).

## J

**Join token.** The K3s secret, kept in the Secret `geass-token`, that a new server uses to join the cluster. See [Add servers](/docs/getting-started/add-servers/).

## L

**Live setting.** A setting that takes effect when you save it, without a deploy. An example is the replica count of a service. See [Configuration](/docs/reference/configuration/#live-settings-and-settings-that-need-a-deploy).

**Logical database.** A database of its own on a shared database server, with a login that reaches nothing else (`GeassLogicalDatabase`). See [Shared database servers](/docs/guides/shared-database-servers/).

**Longhorn.** The storage engine that keeps database and object storage volumes on several nodes. With Longhorn, these volumes can grow. See [Storage](/docs/guides/storage/).

## M

**MCP server.** The `geass mcp` command, a Model Context Protocol server. With it, an AI agent can see and run services with the tools that its token allows. See [MCP server](/docs/guides/mcp-server/) and [MCP tools](/docs/reference/mcp-tools/).

**Mesh.** What joins nodes at different sites, some behind a NAT, into one cluster network. The mesh is the WireGuard backend of K3s, or a Tailscale or Headscale network (`spec.network.mesh`). See [Mesh networking](/docs/operations/mesh-networking/).

**Multi-provider cluster.** A cluster whose servers are at different providers, joined over an encrypted WireGuard network. This feature is experimental. See [Multi-provider clusters](/docs/operations/multi-provider/).

## N

**Network isolation.** The default rule that only that project, the platform, and the public internet can reach the services of a project. See [Network isolation](/docs/guides/network-isolation/).

**Node.** A server in the cluster, joined over SSH or created by Geass at a cloud, and described by a `GeassNode`. See [Add servers](/docs/getting-started/add-servers/).

## O

**Object store.** The resource kind (`GeassObjectStore`) behind a bucket or the object storage server of the cluster. See [GeassObjectStore](/docs/reference/resources/geass-object-store/).

**Observer.** A workspace token role that reads everything a viewer can, plus logs and variable names. An observer changes nothing. See [Users and API tokens](/docs/guides/users-and-tokens/#roles).

**Organization.** A group of people, known to the identity provider and set up in Geass as a `GeassOrganization`. Other organizations do not see the projects of an organization. Organizations exist only when they are turned on. See [Organizations and workspaces](/docs/guides/organizations-and-workspaces/).

**Operator.** The program, running in `geass-system`, that watches the Geass resources and makes the cluster match them. See [How it works](/docs/overview/how-it-works/).

**Orphan machine.** A machine at a cloud that carries the labels of a pool and belongs to no node, so it might still be billing you. A pool reports the machine. The pool deletes it only if the `orphanPolicy` of the pool is `Delete`. See [Node pools](/docs/guides/node-pools/#machines-no-node-owns).

## P

**Pending change.** A saved edit that is not applied until you deploy it. See [Projects and environments](/docs/guides/projects-and-environments/).

**Placement.** Where a workload runs. Placement is the region of the project, plus the node tags that the workload asks for and the tags that its project requires. See [Scaling and placement](/docs/guides/scaling-and-placement/).

**Platform configuration.** The single `GeassPlatformConfig`, named `platform`, that holds the domain, Cloudflare, GitHub, and other cluster-wide settings. See [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/).

**Platform administrator.** Someone who runs the whole installation (nodes, add-ons, domain, sign-in, and organizations), as opposed to the administrators of an organization. When organizations are on, the platform administrators are the admins who sign in with a password. Anyone that the token of the identity provider names as one is also a platform administrator. See [Organizations and workspaces](/docs/guides/organizations-and-workspaces/).

**Platform domain.** The apex domain that you give Geass. Geass serves the dashboard under this domain. After Cloudflare is connected, Geass also serves default service hostnames under it. See [Domains and TLS](/docs/guides/domains-and-tls/).

**Pool.** A `GeassNodePool`: a group of nodes that Geass adds and removes for you. Geass makes the nodes at a cloud or from servers that you list. A pool has a maximum number of nodes, which it never goes above. A pool keeps a fixed number of nodes, or grows and shrinks with the load. See [Node pools and autoscaling](/docs/guides/node-pools/).

**Project.** The top-level container for the services, databases, and buckets of a team, with its own environments, region, and network rules. See [Projects and environments](/docs/guides/projects-and-environments/).

**Project token.** An API token (`gsp_`) limited to one environment of one project and a fixed list of actions. See [Users and API tokens](/docs/guides/users-and-tokens/#what-a-project-token-can-do).

**Pull request environment.** A temporary copy of an environment created for an open pull request. See [Pull request environments](/docs/guides/pull-request-environments/).

## R

**Region.** A group of nearby nodes. A project pinned to a region runs only on the nodes in that region. See [Scaling and placement](/docs/guides/scaling-and-placement/).

**Replica.** One running copy of a service. See [Scaling and placement](/docs/guides/scaling-and-placement/).

**Required tag.** A node tag that a project requires (`spec.requiredTags`). The workloads of the project run only on nodes that carry the tag. See [Compliance tags](/docs/guides/compliance-tags/).

**Reserved.** The CPU and memory that a service sets aside for itself. The capacity check and the scheduler count these amounts. See [Usage and capacity](/docs/guides/usage-and-capacity/).

**Reserved tag.** A node tag that keeps off the node every workload that does not ask for the tag. A compliance tag is always reserved. See [Scaling and placement](/docs/guides/scaling-and-placement/).

**Resource.** An object that Geass manages, such as a project, service, database, or bucket. In the API and in `kubectl`, each resource is a Kubernetes custom resource. See [Resources overview](/docs/reference/resources/overview/).

**Restore point.** A base backup, or any moment inside the recovery window, that a database can be restored to. See [Backups and restore](/docs/guides/backups-and-restore/).

**Role.** What a dashboard user or workspace token is allowed to do: `admin`, `viewer`, or, for tokens only, `observer`. See [Users and API tokens](/docs/guides/users-and-tokens/#roles).

**Runtime.** The language environment (Bun, Node, or Python) that runs a function. See [Functions](/docs/guides/functions/).

## S

**Scale to zero.** The other name for sleep when idle, made possible by the serverless add-on. See [Scale to zero](/docs/guides/scale-to-zero/).

**SeaweedFS.** One of the two servers that Geass can run as the object storage of the cluster. SeaweedFS covers more of the S3 API than Garage. See [Choosing SeaweedFS or Garage](/docs/guides/object-storage/#choosing-seaweedfs-or-garage).

**Serverless add-on.** The cluster add-on (`spec.addons.serverless`) that installs KEDA and its HTTP add-on so that services can sleep when idle. See [Configuration](/docs/reference/configuration/#geasscluster).

**Service.** What the dashboard calls a deployed app: an image, a repository build, or a function. See [Apps](/docs/guides/apps/).

**Shared database server.** One Postgres or MySQL server, shared by many projects, where each project takes logical databases. See [Shared database servers](/docs/guides/shared-database-servers/).

**Shared variable.** A variable defined once for a project environment and selected by its services. See [Environment variables](/docs/guides/environment-variables/).

**Site.** The provider or location inside a region where a node runs. The resource calls it `zone`. Geass records the site as the standard `topology.kubernetes.io/zone` node label. Geass does not use the site to place workloads itself. See [Scaling and placement](/docs/guides/scaling-and-placement/).

**Sleep when idle.** A service setting that stops the service after a period without requests and starts it again on the next request. See [Scale to zero](/docs/guides/scale-to-zero/).

**Storage class.** The Longhorn class that sets how many copies a database or object storage volume keeps: `geass-replicated` (several nodes) or `geass-single` (one). See [Storage](/docs/guides/storage/).

## T

**Tag.** A free-form label on a node, such as `storage` or `gpu`, that workloads can ask for and a project can require. See [Scaling and placement](/docs/guides/scaling-and-placement/).

**Template.** A named set of resources that you can deploy into an environment in one step. The catalog is made of templates, and you can save your own. See [Templates](/docs/guides/templates/).

**Trigger.** A cron schedule, in UTC, that calls a function (`spec.source.function.triggers`). See [Functions](/docs/guides/functions/).

## U

**Usage.** What each project reserves and uses of the CPU, memory, and storage of the cluster. See [Usage and capacity](/docs/guides/usage-and-capacity/).

## V

**Viewer.** The read-only role. A viewer sees a redacted overview, and cannot change anything or read logs. See [Users and API tokens](/docs/guides/users-and-tokens/#roles).

## W

**Webhook.** An outbound, signed notification about deployments in a project, sent to a URL that you set. See [GitHub and builds](/docs/guides/github-and-builds/#outgoing-webhooks).

**Workspace (of an organization).** A group of projects inside an organization, such as the projects of a team. A member who is not an administrator of the organization works in a workspace. Do not confuse a workspace with a workspace token. See [Organizations and workspaces](/docs/guides/organizations-and-workspaces/).

**Workspace token.** An API token (`gsw_`) that acts like a dashboard user across every project, as admin, observer, or viewer. When an admin of an organization made the token, it acts across every project of that organization. See [Users and API tokens](/docs/guides/users-and-tokens/#api-tokens).

## Related

- [Concepts](/docs/overview/concepts/)
- [Resources overview](/docs/reference/resources/overview/)
- [REST API](/docs/reference/rest-api/)
- [CLI reference](/docs/reference/cli/) and [MCP tools](/docs/reference/mcp-tools/)
