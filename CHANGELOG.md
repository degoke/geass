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
- **Command line** (`geass`), installed with `install-cli.sh` for Linux and macOS (and as a download for Windows):
  `geass login` signs in by approving a code in the dashboard, and commands print JSON with `--json` and exit with
  codes a script can act on.
- **MCP server** (`geass mcp`) so an AI agent can inspect, deploy and scale services, shown only the tools its token
  allows.
- **JSON API** under `/api/v1`, described by an OpenAPI document, with full access, read-only, read-only with logs and
  per-environment project tokens.
- **Disaster recovery.** Copies of the cluster's own state in an S3 bucket of your own, which you opt in to: the
  installer asks on a terminal (the answer is no unless it is yes), takes `--etcd-snapshots`, or turns it on later with
  `etcd-snapshots enable`, and the dashboard's **Cluster backup** sets or changes the bucket with no restart. K3s sends
  each snapshot there, and the operator says whether they are arriving and raises a **Cluster problem** alert when they
  stop. A cluster whose control plane disk is lost can be rebuilt on a new machine from the newest copy and the server
  token, which the installer asks you to store; the steps are in the documentation and the restore is tested against a
  real K3s (`make test-k3s-restore`). **Recover databases from a bucket** reads a record that each database's backups now
  have beside them and offers them for restore again on a new cluster; it never deletes what it finds. New
  **Storage failing** alerts for a disk that has gone read-only and for a volume that has lost a copy or cannot be read
  (Longhorn is scraped when monitoring is on), checked with `make test-alert-rules`. A service set to run more than one
  replica asks the scheduler to spread them over machines and then sites, as a preference (a nudge: it was seen to put
  both replicas on one machine). A database on a node that has been lost for 15 minutes is looked after: a database with
  one instance, or whose volume has copies on other nodes, starts on another node (Postgres, Redis and MySQL servers),
  and a lost instance of a highly available Postgres database, or one whose volume has lost its only copy, is replaced
  by a new one built from the primary, never while the primary is down. Longhorn is set to replace pods that were on a
  node that is down (only when it is still `do-nothing`). Rebuilding a control plane server is tested on real machines;
  the documentation says to run the installer with `--force` when it refuses for volumes that are short of a copy, and
  how to tell Longhorn about the new disk. `etcd-snapshots enable` on a rebuilt machine says the cluster already has its
  bucket and takes a copy.
- **Release files** carry a `SHA256SUMS` file that the install scripts check what they download against, and build
  provenance that can be verified with `gh attestation verify`.

### Security

- Report vulnerabilities privately, as described in [SECURITY.md](SECURITY.md).

[Unreleased]: https://github.com/degoke/geass/commits/main
