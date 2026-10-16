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
  (Postgres continuously, MySQL and SQLite daily) with restore as a new database and a 7 day grace after deletion.
  MySQL and Redis run as one pod each from the official `mysql` and `redis` images, which Geass runs itself (not
  through a Helm chart, whose Bitnami images no longer exist on Docker Hub); high availability is for PostgreSQL only,
  and a MySQL or Redis database that asks for it, or for more than one instance, is refused with the reason. A MySQL
  volume can be made bigger. A cluster made from a development build keeps a MySQL database on the Bitnami chart as it
  is, and moves a Redis database to the new server (Redis keeps no data on disk, so that costs a restart). A MySQL or
  Redis database whose server cannot pull its image says so, "Could not pull the database image redis:7.4.11-alpine
  (ImagePullBackOff)", instead of "Service is starting" for ever, and is marked failed after 10 minutes of it, which
  gives the kubelet's retries longer than other failures get.
- **Object storage.** Buckets on an in-cluster SeaweedFS or Garage server, or in AWS S3, isolated per project.
- **Domains and HTTPS.** Domains through Cloudflare (including Tunnels and DNS records) and certificates from
  Let's Encrypt through cert-manager.
- **Scaling and placement.** Replicas, CPU autoscaling, regions, sites and node tags, compliance tags that keep a
  project on chosen nodes, project limits, and services that sleep when idle (KEDA).
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

[Unreleased]: https://github.com/degoke/geass/commits/main
