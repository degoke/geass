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

The first public release, open source under the Apache License 2.0.

### Added

- **Mantine 9 for `@geass/dashboard-ui`.** The library's peer dependencies on `@mantine/core`, `hooks`, `notifications` and
  `code-highlight` accept `^8.3.0 || ^9.0.0`, so an application on Mantine 9 can install it without an unmet-peer warning.
  The library's own tests (38) pass and its library build works with Mantine 9.7.3, and rungeass renders it under 9.7.3
  in a browser. The package itself is still developed against 8.
- **Usage by organization, for billing.** With `spec.usage.enabled`, the operator that holds the lease meters, every minute,
  what the projects of each organization reserve: CPU and memory requests while a pod is on a node, and the size of bound
  volumes, by organization and workspace through each project's `spec.org` and `spec.workspace`, with the projects of no
  organization and the platform's own kept apart. Hours are kept 62 days in ConfigMaps `geass-usage-<YYYYMMDDHH>` in
  `geass-system`, which the operator's cache leaves out, and a restart neither repeats nor loses a minute (time the meter
  did not see shows as fewer metered seconds). `GET /api/v1/usage/hours?from=&to=&org=` serves them, with each hour closed
  or open and a `next` to read on from, to the platform's administrators only. Traffic is not metered: `egressBytes` is
  always `null`. Off by default; a dashboard-only process never meters.
- **The provider's access token as a bearer credential.** With `spec.auth.oidc.accessTokens.enabled`, an application that is
  signed in at the identity provider can call the API, the event and log streams and a terminal's WebSocket with the access
  token the provider issued to the person (a JWT, RFC 9068, for the cluster's audience), as `Authorization: Bearer` or the
  `geass.bearer.<token>` subprotocol. It maps to a session exactly as an ID token does, records the person as the actor, is
  refused the routes that manage sign-in and tokens, and ends a stream when the token expires. Off by default.
- **Dashboard and controllers apart.** `--controllers=false` runs only the dashboard and API, and `--dashboard=false`
  only the controllers; both stay on by default, so installed clusters are unchanged. A dashboard-only process runs no
  reconciler, migration or check, never takes the leader election lease, and refuses to start (and stops, if the data
  moves) unless the cluster's data is at the schema level it reads. `config/dashboard-only` is the ClusterRole it
  needs, and `GEASS_PROMETHEUS_URL`, `GEASS_ALERTMANAGER_URL` and `GEASS_LOGS_URL` point it at the add-ons when it runs
  outside the cluster. The operator's `--kubeconfig` flag is now read, as it was parsed after the client configuration.
- **Hosted control plane.** A cluster whose K3s server runs outside its nodes (with `--disable-agent`, as rungeass
  runs it) has no control plane node, and Geass now works there: workers install the release the API server's
  `/version` reports, machines from node pools let the operator's SSH in from the nodes and its outbound address,
  the K3s upgrade waits for the hosted server and says who upgrades it, the network policy check runs on any node,
  and `config/external-control-plane` runs the operator on any node from `ghcr.io/degoke/geass`, which each release
  now publishes.
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
- **Light and dark themes.** The dashboard, the website and the documentation follow the system's theme, and use light
  when the system has none. The dashboard's account menu has System, Light and Dark, and the documentation's picker
  saves its choice for the whole website. The dashboard used to be dark only, so someone whose system is light now sees
  it light.
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
  replica asks the scheduler to spread them over machines, and over sites when every node has one, as a preference. (Asking
  for sites on nodes that have none cancelled the preference for machines too, and put both replicas on one machine in
  eight cases of ten on three real machines; found by testing it, and fixed.) A database on a node that has been lost for 15 minutes is looked after: a database with
  one instance, or whose volume has copies on other nodes, starts on another node (Postgres, Redis and MySQL servers),
  and a lost instance of a highly available Postgres database, or one whose volume has lost its only copy, is replaced
  by a new one built from the primary, never while the primary is down. Longhorn is set to replace pods that were on a
  node that is down (only when it is still `do-nothing`). Rebuilding a control plane server is tested on real machines;
  the documentation says to run the installer with `--force` when it refuses for volumes that are short of a copy, and
  how to tell Longhorn about the new disk. `etcd-snapshots enable` on a rebuilt machine says the cluster already has its
  bucket and takes a copy. Testing it on real machines also found two faults that stopped Geass promoting a MariaDB
  replica when the primary's machine was lost, now fixed: a highly available database that already existed was held by
  the check for three healthy nodes (which a cluster of three fails the moment it loses one), and Longhorn's removal
  of the lost primary's pod left it with no machine for the promotion to look at. How long a node may be away before this
  starts is `spec.lostNodeAfter` on the GeassCluster (15 minutes; `0s` turns it off). Updating to this release rolls each
  service that runs more than one replica once, because its pods now carry the spread preference. An update is no longer
  stopped by a snapshot that K3s wrote but could not send to its bucket, the snapshot bucket is looked in under its
  folder with a slash (a folder called geass-eu is not a part of geass) and, with copies arriving, only every quarter of an
  hour, and recovering databases from a bucket skips a folder it cannot make again instead of stopping, and no longer
  gives a project that is already there a backup bucket.
- **A MariaDB replica that has to be built again is built.** The copy the operator builds a replica from was kept on the
  servers' own storage class, one copy on one machine, and the job that restores it into a replica mounts that volume and
  the replica's own: with the two on different machines it was never scheduled, and the replica stayed down. The copy is
  now kept on Longhorn's replicated class, in the database's region and tags, and a database made before has its volume,
  template and stuck rebuild replaced when the operator first looks at it. Tested on four machines: the stuck rebuild
  reproduced, and repaired by the update, with the data and replication intact. The check for three healthy nodes also
  no longer stops a new highly available database being made on a cluster whose operator is not installed yet.
- **Release files** carry a `SHA256SUMS` file that the install scripts check what they download against, and build
  provenance that can be verified with `gh attestation verify`.

- **Machines at a cloud (experimental).** Geass creates and deletes servers at Amazon EC2, Microsoft Azure,
  DigitalOcean, Google Cloud, Hetzner Cloud, Linode, Oracle Cloud, OVHcloud Public Cloud, Scaleway, UpCloud and Vultr,
  and, through a webhook that follows a small HTTP contract, at any other provider: 12 drivers. A
  `GeassCloudConnection` with the purpose `Compute` holds the credentials. It belongs to the cluster, never to a
  project, and only an admin can use it. The credentials go into a Secret and are never shown again. The cloud is asked
  whether it accepts them when they are saved and every 15 minutes, and `status` carries the account, the answer,
  warnings and, where the cloud says, the expiry. An AWS connection made for buckets never creates machines: make
  another, with its own key (the IAM policy is in the docs). **Amazon EC2 has run against a live account**
  (`eu-north-1`): a real control plane, a pool that scaled up and down, a replaced machine, a quota refusal, a paused and
  resumed pool, control plane firewall sync, a single server, and the dashboard. Spot machines were not tested: the
  account's organization denied the launch. The other eleven drivers were written from the provider's documentation and
  tested against an in-memory simulation of its API only.
- **Amazon EC2 with temporary credentials.** An AWS connection for machines can hold what `sts:AssumeRole` returns, not
  only an IAM user's key. The optional secret `sessionToken` is sent with every EC2 and STS request and signed with it.
  The optional `expiresAt` makes the connection show when the credentials lapse, which AWS does not say; credentials with
  a session token get no "expires in N days" warning, since they are renewed every hour. Credentials that are replaced
  (through the API, or in the Secret) are used by the next machine made or deleted, with no restart, and a lapsed token
  makes a node wait and does not fail it. A connection without a `sessionToken` works as before. Not run against a live
  AWS account: only against a simulation that checks the signature.
- **Node pools and autoscaling (experimental).** A `GeassNodePool` makes machines at a cloud, joins them over SSH
  with a key and a host key that Geass makes (the join token is never put in a machine's user data), and removes them.
  `maxNodes` is required and caps the cost. A fixed pool keeps `minNodes`. An autoscaling pool adds nodes for pods that
  cannot start (after a delay, packed by size, up to `maxScaleUp`) and removes under-used nodes after emptying them
  through the eviction API, with `minNodeLifetimeMinutes` for clouds that bill each started hour, `spareNodes`, fallback
  sizes and regions, and spot machines. Safety: three machines in a row that fail to join pause the pool for 30
  minutes, a failed node's machine is deleted at once, machines are deleted only by the ID recorded for them, a
  machine the cloud took back is replaced, and machines that no node owns are reported (or deleted, with
  `orphanPolicy: Delete`).
- **Pools of servers you own.** An inventory pool joins servers from a list when it needs room, and takes K3s off
  them when it does not.
- **Add a server** from **Add server** on the **Usage** page or with `geass servers add`: a machine at a cloud, or a
  server you have, which is joined over SSH. The `GeassNode` route with `kubectl` still works.
- **Control plane firewall sync.** `spec.network.controlPlaneFirewall` on the cluster lets Geass open the control
  plane's 6443/tcp (and 51820/udp in a multi-site cluster) for each new machine in an AWS security group or a Hetzner
  firewall, and close it when the machine goes.
- **Dashboard.** A **Pools** page, **Cloud connections** under **Settings** → **Connectors** (connect, verify, edit,
  remove), and on **Usage** an **Add server** button and a **Pool** column with an **Ephemeral** badge. **Remove**
  on a connection refuses, and names what is in the way, while a pool, a machine, an object store, a database, the
  backups of a deleted database, a project's backup bucket or the control plane firewall still uses it. A viewer sees
  which clouds are connected and whether they work, but not the cloud account or the cloud's own message.
- **Command line.** `geass providers`, `sizes`, `connections`, `pools` and `servers`, and `POOL` and `EPHEMERAL`
  columns in `geass nodes list`.
- **MCP tools.** `compute_providers`, `cloud_connections`, `node_pools`, `node_pool`, `cloud_sizes`,
  `create_node_pool`, `update_node_pool`, `resume_node_pool` and, with `--allow-delete`, `delete_node_pool`. No tool takes
  a credential, makes a connection or adds a server, and what an agent may set is capped (a pool of at most 10 machines,
  3 made at once, 3 kept spare; a person sets up to 200). An admin token now sees 38 tools (40 with `--allow-delete`), an
  observer 21 and a viewer 18.
- **API.** `/api/v1/compute/providers`, `/compute/sizes`, `/connections`, `/pools` and `/servers`, and `pool` and
  `ephemeral` on `/nodes`.
- **The dashboard as a library.** `dashboard-ui` also builds as the package `@geass/dashboard-ui`, whose `GeassDashboard`
  component another React app (Mantine, TanStack Router and Query) can mount under a path of its own. The host says where
  the Geass API is (`apiBaseUrl`) and how a request signs in (`getToken`, an API token sent as `Authorization: Bearer`,
  also on the stream of changes, the log stream and the terminal), instead of the dashboard's cookie, and can leave out
  the pages a self-hosted cluster is set up through (`hide`). The dashboard the operator embeds is built as before. See
  [docs/development.md](docs/development.md#the-dashboard-as-a-library).
- **Terminal with an API token from a browser.** A browser cannot set `Authorization` on a WebSocket, so the terminal's
  WebSocket also takes the token as the subprotocol `geass.bearer.<token>`, next to `geass.v1`. It is checked as the
  header is, and read for no other request.

### Changed

- The first pool whose nodes hold no data (`storage: None`, the default), or the first single server added with
  `storage: None`, makes every service roll once, so that its pods tolerate the taint `geass.dev/ephemeral`. It happens
  again when the last one is deleted. A cluster that has neither sees no change.
- `GeassCloudConnection.spec.provider` accepts `GCP`, `DigitalOcean`, `Hetzner`, `OVHcloud`, `Vultr`, `Linode`,
  `Scaleway`, `Azure`, `OCI`, `UpCloud` and `Webhook` besides `AWS` and `PlanetScale`, and the resource gains
  `spec.purposes` and `status.account`, `message`, `verifiedAt`, `expiresAt` and `warnings`. Connections without
  `purposes` keep their old meaning. `GeassCluster` gains `spec.network.controlPlaneFirewall`, `GeassNode` gains
  `spec.pool`, `spec.storage` and `spec.cloud`, and there is a new `GeassNodePool` resource.
- The capacity check lets a workload in when an autoscaling pool could make room for it, and refuses it, as before,
  when the pool is at its maximum. The dashboard, the command line and the API ask the same question the operator does,
  so a full cluster with a pool that can grow no longer refuses what the pool would have made room for.
- Removing a server. The **Remove server** dialog lists the data that is on that server and nowhere else (a database
  whose only copy is there, a replica of one that has other copies, a volume with one copy). An instance of a highly
  available Postgres database whose volume was on a server that is no longer in the cluster is replaced after three
  minutes, where it waited fifteen for the lost-node limit and about twenty-four in all. The operator hears of a Postgres
  instance's pod that is made, removed or cannot be placed, so that look is made at once and not at the next regular one.
- Postgres backups. A base backup is listed as a restore point only when every log file it needs is in the bucket, and
  a restore is pinned to one that holds (a new backup is trusted for seven minutes, which a quiet database needs to archive
  its last log file, and looked at every minute until it is checked). A first backup that came before the database could
  save its log files says so and is taken again once a log file has reached the bucket, up to three times, and a restore that cannot start says
  what the restore reported instead of staying on "Restoring database from backup". The jobs that dump and load MySQL,
  MariaDB and SQLite databases wait until the server accepts a connection, which they did not on K3s: the first
  connection from a new pod is refused for a moment, so the first backup and the final backup on delete failed.
- A database shows the version it runs. Every database carried a version field that started as a Postgres version, so
  Redis showed 16; MySQL, MariaDB, Redis and SQLite now show the version Geass runs, and only a Postgres database has a
  **Version** setting.
- A service whose image cannot be pulled says which image, and a service or database that is given up on keeps saying
  why it cannot start instead of "Provisioning failed after repeated errors". A service hears of a pod that cannot start
  when it happens. A database whose volume Longhorn has no disk for says so and what to do, and does not count that
  as a failure.
- The dashboard says less. Intro paragraphs, hints that repeat a label, footnotes and toasts that give instructions are gone
  from every page, and the notes on the catalog templates are shorter. A success toast reads "Saved" or "Added gatus".
- Settings, Connectors is grouped by what each connects: **Code and domains** (GitHub, Cloudflare), **Server clouds** (the
  connections that create servers, which belong to the cluster) and **Storage and databases** (AWS and PlanetScale, each
  saved for one project or an organization). A project's connections are listed, with Verify, Edit and Remove, in the AWS
  and PlanetScale drawers. A connection is shown as for Servers, Buckets or Databases, and its project by name.
  The project's **Integrations** page is now **GitHub**: it held only the project's link to GitHub and a pointer to
  Connectors.
- The alert channel and mail server forms open in the side drawer, like the other settings forms.
- Adding a database asks first whether it is a **Database server** or a **Logical database**. The servers a database
  can go on, the project's own and the shared ones, are listed together instead of below the engines.
- The projects allowed to reach a project are shown by name, not by id.
- `GET /api/v1/connections` says which organization a connection belongs to (`org`), and the AWS and PlanetScale drawers list the
  connections of that provider that hold buckets or databases, including a cluster one that also creates servers.
- The first etcd copy is looked for every minute for the first quarter of an hour, so the **Usage** panel stops saying
  "Waiting for the first copy" soon after the installer has taken it.
- A deploy made by a deploy hook is recorded as the hook's, and one made with an API token as the token's, where both
  said "Dashboard". `kubectl get geassbackuparchive` shows **Delete after** as the time, where it showed `<invalid>`.
  The cloud connection form has one default region box. A failed restore shows the cluster's reason in the dialog.
  `geass rollback` says that only the image and the number of copies go back: variables and other settings stay.
- Nodes that hold no data are not counted by high availability readiness, are never used for volume replicas
  (Longhorn is told not to schedule on them), and DNS never points at a node of a pool.
- The operator's pod cache keeps the volumes that hold data on a node (claims, host paths and on-disk `emptyDir`s), which
  the autoscaler needs to know before it removes a node.
- The operator may now create `pods/eviction`, to drain a node before removing it.
- A machine Geass creates is joined only after its SSH host key has been checked against the one Geass put on it, and
  only once the control plane's addresses are known for its firewall: either missing makes the node wait. A node's cloud
  connection, region and pool cannot be edited after it is made. A rule opened in the control plane's firewall is closed
  on the firewall it was opened on, even if the cluster's `controlPlaneFirewall` was changed. The dashboard no longer
  shows a viewer what the cloud said about a pool or its nodes. A webhook service is never connected to at a link-local
  address, however it is reached. DNS never points at a server added with `storage: None`.

### Fixed

- **A service could not be deployed from a dashboard that runs outside the cluster.** Deploying records a deployment and then writes
  its status, which the `--controllers=false` dashboard's role (`config/dashboard-only`) did not allow, so the service was saved as a
  draft and "deploying it failed". The role can now update the status of a `GeassDeployment`.
- **Making and deploying a service in one request could fail with "saved as a draft" although it was made.** The service is saved a
  second time with its deploy marked; the controller has by then added its finalizer, so that save conflicts, and the latest copy was
  read through a cache that had not seen the new object yet and found nothing. A copy that is not found yet is now asked for again.

### Security

- Report vulnerabilities privately, as described in [SECURITY.md](SECURITY.md).
- The password of an in-cluster database that Geass makes is random (it was the resource name and `-password`). A
  database that already exists keeps the password it has, because everything that connects to it would have to change
  with it. The backup folder's `geass-archive.json` of a highly available MariaDB database holds the password of the
  server its physical copies came from, which a restored copy needs and which the copies hold anyway.
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
- **Secrets are encrypted at rest.** The installer turns on K3s secrets encryption (`secrets-encryption: true`) on every
  cluster it builds, so the dashboard users, API token hashes, cloud keys, app secrets and database passwords are encrypted
  in K3s's datastore and in the etcd snapshots, including the one taken before an upgrade. A control plane server joined
  with `--join-server` gets the same setting. The key stays on the server's disk, and anyone the API lets read Secrets still
  reads them. Only the Secret resource is encrypted: a value written into a service's `spec.secretData`, and plain
  variables, stay readable in the datastore. A restore on another machine needs the cluster's server token and the same
  setting; the Upgrading page of the documentation says how.

[Unreleased]: https://github.com/degoke/geass/commits/main
