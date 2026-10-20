# Changelog

All notable changes to Geass are written here. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and releases are numbered as described in [docs/releasing.md](docs/releasing.md): one version for the installer, the
operator, the command line and its MCP server, which always ship together.

Geass is before 1.0. The custom resources are `v1alpha1`, and until 1.0 a minor release (`0.x`) may change behaviour
that a patch release (`0.x.y`) does not. What a release does to an existing cluster is always stated here, and the
installer refuses a downgrade.

Add your change under **Unreleased** in the pull request that makes it. The release process moves those notes under the
new version.

## [Unreleased]

The first public release.

### Added

- **Installer.** One command installs K3s, the Geass operator and the dashboard on a fresh Linux server (amd64 or
  arm64), and running a newer installer updates the cluster in place: it keeps the data, takes an etcd snapshot first,
  checks that the custom resource definitions stay compatible with what is stored, runs data migrations, refuses a
  downgrade and rolls the operator back if the new one does not take over. `--k3s-version` upgrades K3s one minor
  version at a time, and the operator then upgrades the workers. Extra control plane servers can be joined with
  `--join-server`.
- **Projects and environments.** Services deployed from a Docker image, from a GitHub repository built in the cluster
  with Kaniko, or as a function (Bun, Node.js or Python, as a server or as an event handler that Geass serves HTTP for,
  with cron triggers). Review pending changes and deploy them together. Templates, pull request environments, signed
  deployment webhooks and deploy hooks.
- **Catalog.** Self-hosted software you can deploy in one click from the Templates page: Gatus, Infisical, ReFHIR and
  browserbay, each with its databases, generated secrets and a size measured with the real image. You can save your own
  stack as a template too. A template can carry config files, notes about what to do after deploying, and a size that
  reserves one amount and lets the service use up to a higher one (`cpuMax` and `memoryMax`), so software that needs
  more memory only for its first start does not hold it for ever. The old starter examples are in the documentation
  to copy instead of shipping as defaults.
- **Databases.** PostgreSQL, MySQL, Redis and SQLite in the cluster, shared database servers, external databases
  (AWS RDS, PlanetScale), resizable volumes on Longhorn, and backups to an S3 bucket in your own AWS account
  (Postgres and highly available MariaDB continuously, the rest daily) with restore as a new database and a 7 day grace after deletion.
  MySQL and Redis run as one pod each from the official `mysql` and `redis` images, which Geass runs itself (not
  through a Helm chart, whose Bitnami images no longer exist on Docker Hub). High availability is for PostgreSQL and
  MySQL: a MySQL database that asks for it runs three or five servers on MOCO, a MySQL operator that Geass installs the
  first time (it needs cert-manager), with writes acknowledged by a replica, a replica promoted when the primary is
  lost, a login and database made for the application, daily copies, and a minimum of 1 GB of memory for each server.
  A MySQL database is one instance or high availability for its life.
  MariaDB is an engine of its own, chosen when the database is created, on the MariaDB operator that Geass installs the
  first time (it does not need cert-manager): one server, or with high availability three or five that replicate with
  semi-synchronous replication, a replica promoted when the primary's pod is lost (about ten seconds in a test), the
  application's login made by the operator, and backups: a daily copy with `mariadb-dump` for one server, and for a
  highly available database a physical copy every night with the binary logs archived beside it, so it can be restored to any
  moment up to about 15 minutes ago, as a new highly available database (a restore in a test, of a database of a few rows, took
  80 seconds). Geass closes the log every five minutes so it is archived, removes the logs no kept copy can use, asks for a
  new copy when the primary changes, and takes a final copy when the database is deleted. The operator cannot replace a primary
  whose whole machine goes silent, so Geass does it: when that machine has not been ready for a minute it promotes the
  replica that has the most. A MariaDB database is one instance or high availability for its life. A Redis database that asks for high
  availability, or for more than one instance, is refused with the reason. A MySQL volume can be made bigger. A cluster made from a development build keeps a MySQL database on the Bitnami chart as it
  is, and moves a Redis database to the new server (Redis keeps no data on disk, so that costs a restart). A MySQL or
  Redis database whose server cannot pull its image says so, "Could not pull the database image redis:7.4.11-alpine
  (ImagePullBackOff)", instead of "Service is starting" for ever, and is marked failed after 10 minutes of it, which
  gives the kubelet's retries longer than other failures get.
- **Object storage.** Buckets on an in-cluster SeaweedFS or Garage server, or in AWS S3, isolated per project.
- **Domains and HTTPS.** Domains through Cloudflare (including Tunnels and DNS records) and certificates from
  Let's Encrypt through cert-manager.
- **Scaling and placement.** Replicas, CPU autoscaling, regions, sites and node tags, compliance tags that keep a
  project on chosen nodes (on Kubernetes 1.36 or later a second admission policy gives pods that database operators
  create themselves the node selector and toleration they lack, so they are not refused), project limits, and services that sleep when idle (KEDA).
- **Services that reserve less than they may use.** A service can reserve a small amount of CPU and memory and be
  allowed to use more, up to a ceiling, while its server has room (**can use up to** in its settings; `cpuMax` and
  `memoryMax` in the API). Software that needs a lot only for a while, such as on its first start, does not hold that
  much for ever. The capacity check and project limits count only what is reserved, and the **Usage** page warns when
  services may use more memory between them than the servers have. A copy that is stopped for memory says so in the
  service's status (`Ran out of memory`) and in an `OutOfMemory` condition, and the Scale settings say when a service
  typically uses more memory than it reserves, since such a service is the first to be stopped when its server runs
  short (the metrics gain a per-replica `memoryPerReplica` series for this). Databases and object storage always use
  what they reserve.
- **Network isolation.** Projects are private by default, with an allow list per project.
- **Observability.** Metrics, live logs with history, a terminal in a service and a query box for databases, and charts
  of the operator's own health.
- **Dashboard sign-in.** Local admin and viewer users, or an external OpenID Connect provider with a role claim mapped to
  admin or viewer.
- **Organizations and workspaces.** One cluster can serve several organizations that must not see each other's work.
  They are read from the identity provider's sign-in token (a list of organizations, each with the person's role and the
  teams they are in), so Geass keeps no list of people. A platform administrator sets each organization up
  (`GeassOrganization`, `/api/orgs`, **Settings** → **Organizations**) and assigns projects to it and to a workspace of it.
  A person works in one organization and workspace at a time, with a switcher, and sees and changes only what is theirs:
  every read and write of the dashboard, the API, the command line and the MCP server goes through one check, another
  organization's resource is answered as one that does not exist, and a route nobody classified is the platform's
  alone. Tokens belong to where they were made (an installation holds at most 600, an organization 100). The platform's
  own pages (nodes, add-ons, domain, registry, sign-in, alerts, shared servers) stay with the platform's administrators,
  who are the admins with a password and anyone a `platformClaim` names. It is off by default and changes nothing while
  off; turning it on needs a test sign-in that put someone in an organization that is set up, and ends everyone's
  sessions once. It is for organizations that trust each other: they share machines. See
  [Organizations and workspaces](website/src/content/docs/docs/guides/organizations-and-workspaces.md).
- **What an organization has of its own.** A platform administrator can limit an organization (most projects, a quota for
  all its projects together, a region, node tags every workload must run on, and the domains its services may take
  hostnames under). An organization has its own Slack alert channels, which hear only about its projects; its own
  registry, with builds that run in a namespace of the organization's own that holds only that build's credentials and
  reaches no private address; shared database servers and cloud connections made for it, which only its projects use;
  and, when the platform allows, its own identity provider to sign in through (a link that names the organization, a
  claim mapped to a role, tested before it is turned on). A token is scoped by where it was made, and a platform
  administrator can make one for a named organization (`org`, `workspace`); there is no organization header or flag.
  Organizations' buckets are made on the platform's object storage server.
- **Sign-in checks people again.** With `spec.auth.oidc.refreshMinutes` set, Geass asks the identity provider again, every
  so often, whether each signed-in person is still its own and says the same of them (their role, organizations and
  platform claim), and ends the sessions of one it does not, so a removal or a role change is noticed in minutes and not
  when the session ends. Refresh tokens are kept sealed. A provider that cannot be reached does not end sessions until
  `staleGraceMinutes` (120) have passed.
- **Organizations made by sign-in.** With `spec.auth.tenancy.autoProvision`, the first sign-in of an organization's
  administrator (with a verified address) makes the organization in Geass: suspended until approved, or ready at once
  for the ids on a list, up to a limit.
- **GitHub installations are proved.** With organizations on, a person who connects a project to a GitHub App
  installation has to show that GitHub lists it among the ones their account can use. An app made by Geass asks for
  that when it is installed; an app you registered yourself needs **Request user authorization (OAuth) during
  installation** turned on.
- **Command line** (`geass`), installed with `install-cli.sh` for Linux and macOS (and as a download for Windows):
  `geass login` signs in by approving a code in the dashboard, and commands print JSON with `--json` and exit with
  codes a script can act on.
- **MCP server** (`geass mcp`) so an AI agent can inspect, deploy and scale services, shown only the tools its token
  allows.
- **JSON API** under `/api/v1`, described by an OpenAPI document, with full access, read-only, read-only with logs and
  per-environment project tokens.
- **Release files** carry a `SHA256SUMS` file that the install scripts check what they download against, and build
  provenance that can be verified with `gh attestation verify`.

### Security

- Report vulnerabilities privately, as described in [SECURITY.md](SECURITY.md).
- A project's environment namespace that already exists is used only if it is that project's: a project no longer takes
  over another project's namespace, or one Geass did not create, and deleting a project or an environment deletes only
  namespaces that are its own.
- A service's hostname is lowercased and checked, and cannot be one another service serves or the dashboard's. Geass no
  longer overwrites a CNAME record in your Cloudflare zone that it did not create. Moving a service to another project
  or environment needs one that exists.
- A cloud connection is saved after its project is checked, so a failed save no longer leaves its credentials Secret
  behind.
- The check that a hostname is free of another service's is made against every service, not only the caller's, so an
  organization can no longer take a hostname another organization's service serves.
- Builds no longer mount the operator's service account token, and an organization's builds run apart from the
  platform's namespace and secrets (see above).

[Unreleased]: https://github.com/degoke/geass/commits/main
