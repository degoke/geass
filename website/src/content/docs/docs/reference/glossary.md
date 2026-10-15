---
title: "Glossary"
description: "Short definitions of the terms you meet in Geass, from admin and API token to environment, region, site and shared database server, each with a link."
---

Each entry is one sentence and a link to the page that explains it. Names in code format are the resource kinds or fields behind
the term; the dashboard sometimes uses a friendlier word for the same thing.

## A

**ACME email.** The contact address for the cluster's Let's Encrypt account, kept as `spec.addons.certManager.acmeEmail` (the installer reads it from `GEASS_ACME_EMAIL`); without it Geass creates no certificate issuer. See [HTTPS certificates](/docs/getting-started/install/#https-certificates).

**Add-on.** A platform component that Geass installs into the cluster through K3s's Helm controller, such as cert-manager, monitoring, Longhorn, logging and the serverless add-on. See [Configuration](/docs/reference/configuration/#geasscluster).

**Admin.** A dashboard user, or a workspace token, that can change everything; the other roles are viewer and, for tokens only, observer. See [Users and API tokens](/docs/guides/users-and-tokens/#roles).

**Alert channel.** A Slack channel or a list of email addresses that gets a message, sent by Alertmanager, when a build or deploy fails, a service is down, backups fail, a pod crashes, storage fills or a node stops responding, with filters for which alerts, projects and environments it hears. See [Alerts](/docs/guides/alerts/).

**API token.** A credential for scripts, CI, the command line and agents that is sent as a Bearer token, either a workspace token (`gsw_`) or a project token (`gsp_`). See [Users and API tokens](/docs/guides/users-and-tokens/#api-tokens).

**API v1.** The versioned HTTP API under `/api/v1`, described by an OpenAPI document, that the command line, the MCP server and scripts use. See [REST API](/docs/reference/rest-api/).

**App.** The resource kind (`GeassApp`) behind what the dashboard calls a service. See [Apps](/docs/guides/apps/).

## B

**Backup archive.** The record (`GeassBackupArchive`) of one database's backups, which outlives the database for 7 days so it can still be restored. See [Backups and restore](/docs/guides/backups-and-restore/).

**Backup storage.** The AWS bucket, reached through an AWS connection, where a project's databases keep their backups. See [Backups and restore](/docs/guides/backups-and-restore/).

**Base environment.** The environment that Geass copies for each pull request. See [Pull request environments](/docs/guides/pull-request-environments/).

**Bucket.** S3-compatible storage for a project, either on the cluster's object storage server (SeaweedFS or Garage) or in AWS S3, kept as a `GeassObjectStore`. See [Object storage](/docs/guides/object-storage/).

**Build.** A repository build, run as a Kaniko Job and recorded as a `GeassBuild`. See [GitHub and builds](/docs/guides/github-and-builds/).

## C

**Capacity gate.** The check that refuses a deploy when the cluster does not have the CPU and memory the change asks for. See [Usage and capacity](/docs/guides/usage-and-capacity/).

**CLI.** The `geass` command line, which deploys and runs services from a terminal by calling the `/api/v1` API. See [Command line](/docs/guides/command-line/) and the [CLI reference](/docs/reference/cli/).

**Cloud connection.** A project's credentials for AWS or PlanetScale, kept as a `GeassCloudConnection`. See [Cloud connectors](/docs/guides/connectors/).

**Cloudflare Tunnel.** An optional way to publish the dashboard and services through Cloudflare, using a `cloudflared` Deployment instead of open ports. See [Connect Cloudflare](/docs/guides/connectors/cloudflare/).

**Cluster.** The K3s cluster that Geass runs and manages, represented by the `GeassCluster` named `default`. See [How it works](/docs/overview/how-it-works/).

**Cluster object storage.** The single S3 server, SeaweedFS or Garage, shared by every project, that holds in-cluster buckets. An admin chooses one when setting it up. See [Object storage](/docs/guides/object-storage/).

**Cluster size.** The PlanetScale size a database is created with, kept as `spec.clusterSize`; it cannot change after the database exists. See [Connect PlanetScale](/docs/guides/connectors/planetscale/#choosing-a-size).

**ClusterIssuer.** The cert-manager issuer named `letsencrypt-prod` that Geass creates from the ACME email and that every HTTPS Ingress asks for its certificate. See [HTTPS certificates](/docs/getting-started/install/#https-certificates).

**Compliance tag.** A node tag, such as `hipaa`, that is always reserved and that a project can require, so everything the project runs stays on those nodes. See [Compliance tags](/docs/guides/compliance-tags/).

**Connector.** A card in **Settings** → **Connectors** (GitHub, Cloudflare, AWS or PlanetScale); the dashboard's word for a connection. See [Cloud connectors](/docs/guides/connectors/).

**Console session.** The audit record (`GeassConsoleSession`) of a terminal opened in a service. See [Security](/docs/operations/security/#auditing).

**Control plane.** The node that runs K3s's server, the operator and the dashboard, created by the installer. See [How it works](/docs/overview/how-it-works/).

## D

**Dashboard.** The web interface, embedded in the operator binary and served on port 8085. See [Introduction](/docs/overview/introduction/).

**Dashboard URL.** The public HTTPS address of the dashboard, `https://<subdomain>.<root domain>`, set in **Settings** → **Domain**. See [Domains and TLS](/docs/guides/domains-and-tls/).

**Default region.** The region used by projects that do not choose one. See [Scaling and placement](/docs/guides/scaling-and-placement/).

**Deploy.** The step that applies a draft or pending change to the running cluster, done with **Deploy changes** or the API. See [Projects and environments](/docs/guides/projects-and-environments/).

**Deployment record.** A `GeassDeployment` that remembers one deploy of a service, used for history and rollback. See [Apps](/docs/guides/apps/).

**Device code.** The short code, such as `BCDF-GHJK`, that `geass login` shows and that an admin approves on the dashboard's `/device` page to hand the command line an API token. See [Users and API tokens](/docs/guides/users-and-tokens/#sign-in-the-command-line).

**Display name.** The label the dashboard shows for a resource, kept in the annotation `geass.dev/display-name` (for a project, in `spec.displayName`). See [Resources overview](/docs/reference/resources/overview/).

**Draft.** A resource that has been created and saved but not deployed yet. See [Projects and environments](/docs/guides/projects-and-environments/).

## E

**Environment.** An isolated copy of a project's resources, such as `production` or `staging`, with its own namespace. See [Projects and environments](/docs/guides/projects-and-environments/).

**Environment quota.** An optional cap on the CPU, memory, disk and pods that each environment of a project can ask for. A **project quota** caps the project as a whole, added up over its environments. Either keeps one project from using all the room in the cluster. See [Project limits](/docs/guides/project-limits/).

**Environment variable.** A named value given to a service, as plain config, a secret, or a shared variable. See [Environment variables](/docs/guides/environment-variables/).

**Event function.** A function in event mode: you export a handler that is given an event for each request or schedule, and Geass serves HTTP for it. See [Functions](/docs/guides/functions/).

## F

**Function.** A service whose source is a single file run on a managed language runtime (Bun, Node or Python), either as a server or as an event handler. See [Functions](/docs/guides/functions/).

## G

**Garage.** One of the two servers Geass can run as the cluster's object storage. A small server that suits low-memory nodes and plain uploads. See [Choosing SeaweedFS or Garage](/docs/guides/object-storage/#choosing-seaweedfs-or-garage).

**GitHub App.** The one app, registered in GitHub, that lets Geass read repositories, receive pushes, build, and report builds, deploys and pull request environments back to GitHub. See [Connect GitHub](/docs/guides/connectors/github/).

## H

**High availability.** A database topology with three instances, allowed only when enough healthy nodes exist. See [High availability](/docs/operations/high-availability/).

**HA readiness.** The check (`GeassHAReadiness`) that reports whether the cluster has the nodes and storage for high availability. See [GeassHAReadiness](/docs/reference/resources/geass-ha-readiness/).

## J

**Join token.** The K3s secret, kept in the Secret `geass-token`, that lets a new server join the cluster. See [Add servers](/docs/getting-started/add-servers/).

## L

**Live setting.** A setting that takes effect when saved, without a deploy, such as a service's replica count. See [Configuration](/docs/reference/configuration/#live-settings-and-settings-that-need-a-deploy).

**Logical database.** A database of its own on a shared database server, with a login that reaches nothing else (`GeassLogicalDatabase`). See [Shared database servers](/docs/guides/shared-database-servers/).

**Longhorn.** The storage engine that keeps database and object storage volumes on several nodes and lets them grow. See [Storage](/docs/guides/storage/).

## M

**MCP server.** The `geass mcp` command, a Model Context Protocol server that lets an AI agent see and run services with the tools its token allows. See [MCP server](/docs/guides/mcp-server/) and [MCP tools](/docs/reference/mcp-tools/).

**Multi-provider cluster.** A cluster whose servers are at different providers, joined over an encrypted WireGuard network; experimental. See [Multi-provider clusters](/docs/operations/multi-provider/).

## N

**Network isolation.** The default rule that a project's services can be reached only by that project, the platform and the public internet. See [Network isolation](/docs/guides/network-isolation/).

**Node.** A server in the cluster, added over SSH and described by a `GeassNode`. See [Add servers](/docs/getting-started/add-servers/).

## O

**Object store.** The resource kind (`GeassObjectStore`) behind a bucket or the cluster's object storage server. See [GeassObjectStore](/docs/reference/resources/geass-object-store/).

**Observer.** A workspace token role that reads everything a viewer can plus logs and variable names, and changes nothing. See [Users and API tokens](/docs/guides/users-and-tokens/#roles).

**Operator.** The program, running in `geass-system`, that watches the Geass resources and makes the cluster match them. See [How it works](/docs/overview/how-it-works/).

## P

**Pending change.** A saved edit that is not applied until you deploy it. See [Projects and environments](/docs/guides/projects-and-environments/).

**Placement.** Where a workload runs: the project's region plus any node tags the workload asks for and the tags its project requires. See [Scaling and placement](/docs/guides/scaling-and-placement/).

**Platform configuration.** The single `GeassPlatformConfig`, named `platform`, that holds the domain, Cloudflare, GitHub and other cluster-wide settings. See [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/).

**Platform domain.** The apex domain you give Geass, under which it serves the dashboard and, once Cloudflare is connected, default service hostnames. See [Domains and TLS](/docs/guides/domains-and-tls/).

**Project.** The top-level container for a team's services, databases and buckets, with its own environments, region and network rules. See [Projects and environments](/docs/guides/projects-and-environments/).

**Project token.** An API token (`gsp_`) limited to one environment of one project and a fixed list of actions. See [Users and API tokens](/docs/guides/users-and-tokens/#what-a-project-token-can-do).

**Pull request environment.** A temporary copy of an environment created for an open pull request. See [Pull request environments](/docs/guides/pull-request-environments/).

## R

**Region.** A group of nearby nodes; a project pinned to a region runs only on nodes in it. See [Scaling and placement](/docs/guides/scaling-and-placement/).

**Replica.** One running copy of a service. See [Scaling and placement](/docs/guides/scaling-and-placement/).

**Required tag.** A node tag a project requires (`spec.requiredTags`), so its workloads run only on nodes that carry it. See [Compliance tags](/docs/guides/compliance-tags/).

**Reserved tag.** A node tag that keeps every workload that does not ask for it off that node; a compliance tag is always reserved. See [Scaling and placement](/docs/guides/scaling-and-placement/).

**Resource.** An object Geass manages, such as a project, service, database or bucket; in the API and in `kubectl` each is a Kubernetes custom resource. See [Resources overview](/docs/reference/resources/overview/).

**Restore point.** A base backup, or any moment inside the recovery window, that a database can be restored to. See [Backups and restore](/docs/guides/backups-and-restore/).

**Role.** What a dashboard user or workspace token may do: `admin`, `viewer` or, for tokens only, `observer`. See [Users and API tokens](/docs/guides/users-and-tokens/#roles).

**Runtime.** The language environment (Bun, Node or Python) that runs a function. See [Functions](/docs/guides/functions/).

## S

**Scale to zero.** The other name for sleep when idle, made possible by the serverless add-on. See [Scale to zero](/docs/guides/scale-to-zero/).

**SeaweedFS.** One of the two servers Geass can run as the cluster's object storage. It covers more of the S3 API than Garage. See [Choosing SeaweedFS or Garage](/docs/guides/object-storage/#choosing-seaweedfs-or-garage).

**Serverless add-on.** The cluster add-on (`spec.addons.serverless`) that installs KEDA and its HTTP add-on so services can sleep when idle. See [Configuration](/docs/reference/configuration/#geasscluster).

**Service.** What the dashboard calls a deployed app: an image, a repository build or a function. See [Apps](/docs/guides/apps/).

**Shared database server.** One Postgres or MySQL server, shared by many projects, where each project takes logical databases. See [Shared database servers](/docs/guides/shared-database-servers/).

**Shared variable.** A variable defined once for a project environment and selected by its services. See [Environment variables](/docs/guides/environment-variables/).

**Site.** The provider or location inside a region where a node runs (called `zone` in the resource); Geass records it as the standard `topology.kubernetes.io/zone` node label and does not use it to place workloads itself. See [Scaling and placement](/docs/guides/scaling-and-placement/).

**Sleep when idle.** A service setting that stops the service after a period without requests and starts it again on the next request. See [Scale to zero](/docs/guides/scale-to-zero/).

**Storage class.** The Longhorn class that sets how many copies a database or object storage volume keeps: `geass-replicated` (several nodes) or `geass-single` (one). See [Storage](/docs/guides/storage/).

## T

**Tag.** A free-form label on a node, such as `storage` or `gpu`, that workloads can ask for and a project can require. See [Scaling and placement](/docs/guides/scaling-and-placement/).

**Template.** A saved set of resources you can deploy into an environment in one step. See [Templates](/docs/guides/templates/).

**Trigger.** A cron schedule, in UTC, that calls a function (`spec.source.function.triggers`). See [Functions](/docs/guides/functions/).

## U

**Usage.** What each project reserves and uses of the cluster's CPU, memory and storage. See [Usage and capacity](/docs/guides/usage-and-capacity/).

## V

**Viewer.** The read-only role: it sees a redacted overview and cannot change anything or read logs. See [Users and API tokens](/docs/guides/users-and-tokens/#roles).

## W

**Webhook.** An outbound, signed notification about deployments in a project, sent to a URL you choose. See [GitHub and builds](/docs/guides/github-and-builds/#outgoing-webhooks).

**Workspace token.** An API token (`gsw_`) that acts like a dashboard user across every project, as admin, observer or viewer. See [Users and API tokens](/docs/guides/users-and-tokens/#api-tokens).

## Related

- [Concepts](/docs/overview/concepts/)
- [Resources overview](/docs/reference/resources/overview/)
- [REST API](/docs/reference/rest-api/)
- [CLI reference](/docs/reference/cli/) and [MCP tools](/docs/reference/mcp-tools/)
