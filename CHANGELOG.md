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

- **First sign-in with no kubectl.** A new install now creates the first dashboard user, `admin`, with a random
  password. The installer prints the username and the password when it creates the user, and again at the end of the
  install. It also prints how to open the dashboard and how to set a domain. The Secret `geass-dashboard-auth` holds
  only a bcrypt hash. The installer never replaces users that work, and the password is not in the install log. An
  update creates no user.
- **The install ends when Geass is ready.** The installer waits up to 15 minutes for certificates, metrics, and storage
  to run, and it prints what is left while it waits. You no longer check the cluster on the server before you deploy.
  If the time ends first, the installer says that Geass is still starting. The dashboard then shows **Geass is
  starting** at the top of each page until the cluster is ready.
- **The dashboard on the loopback address before it has a domain.** The installer adds the systemd service
  `geass-dashboard` on the first server, as a systemd unit or an OpenRC script. The service keeps the dashboard on
  `127.0.0.1:8085` and on no other address.
  To open the dashboard, run `ssh -L 8085:127.0.0.1:8085` to the server, and open `http://localhost:8085`. The service
  manager starts the forward again when the dashboard pod changes. A server that you installed with an earlier release does not
  get this service. See [Sign in to the dashboard](website/src/content/docs/docs/getting-started/sign-in.md).
- **Certificate email in the dashboard.** **Settings** → **Domain** has a **Certificate email** field. It sets the
  contact address of the Let's Encrypt account, `spec.addons.certManager.acmeEmail`, and it shows the state of the
  issuer. Before, you could set the address only with `GEASS_ACME_EMAIL` or `kubectl patch`.
- **Manage dashboard users without kubectl.** An admin adds, changes, and removes users under **Settings** → **Users**.
  The same actions are in `geass users list|add|set-role|reset-password|remove`, and in `GET /api/v1/users` and the other
  `/api/v1/users` routes. A user has the role `admin` or `viewer`. When Geass makes a password, it shows the password one
  time. A password reset or a removal signs the user out. Geass refuses to remove the last admin or to make the last
  admin a viewer. **Sign out everyone** (`geass users sign-out-all`) ends every session and keeps the API tokens. The
  Secret `geass-dashboard-auth` keeps its shape. A cluster that is already installed needs no change. You can still edit
  the Secret by hand. Only the platform's administrators can use these routes. A token manages viewers only: it cannot
  make an admin or set an admin's password. Managed mode refuses the routes with the sign-in settings. The dashboard's own
  tests cover the handler chain. A real browser on the real operator against K3s (server only, 2026-10-30) did these
  actions: add, change a role, reset, remove, and sign out everyone. See
  [Users and API tokens](website/src/content/docs/docs/guides/users-and-tokens.md).
- **Turn the add-ons on and off in the dashboard and the command line.** **Settings** → **Add-ons** (admins only) lists
  **Sleep when idle** (`spec.addons.serverless`), **Replicated storage** (`spec.addons.longhorn`), **Metrics**
  (`spec.addons.monitoring`, with the `lite` or `full` footprint), and **Logs** (`spec.addons.logging`, with 1 to 90 days of
  retention). Each shows a state (`Off`, `Installing`, `Ready`, or `Failed`) with the reason, and a **Retry install** button for
  an add-on whose install job failed or was suspended, which used to need `kubectl delete job`. Replicated storage can be turned
  on and cannot be turned off, because Geass never uninstalls it. The same is in `geass addons` (`list`, `enable --wait`,
  `disable`, `set`, `retry`) and in the API: `GET /api/v1/settings/addons` and `POST /api/v1/settings/addons/{name}/save` and
  `/retry`. The operator writes the state to the new field `status.addons` of the cluster (additive: `make crd-compat` passes)
  and carries out a retry when the cluster has the annotation `geass.dev/retry-addon`. Existing clusters change when the
  operator is updated: they get `status.addons` and nothing else. The guides on sleep when idle, storage, and observability
  now use the page, and keep their `kubectl` steps under their *Under the hood* sections. Metrics and logs stores outside the
  cluster and the usage meter are not on the page yet.
- **Server management without `kubectl`.** The **Nodes** table on the **Usage** page, `/api/v1`, and the command line now
  do what needed `kubectl` before. The changes are additive.
  - A server that has not joined, or failed to join, has a row with its phase and its error. The phase is `Waiting`,
    `Creating machine`, `Joining`, or `Failed`. `GET /servers` and `geass servers list` list these servers.
    `geass nodes list` also lists them.
  - **Retry** starts a failed server over. Use `POST /servers/{name}/retry` or `geass servers retry NAME`.
  - **Update host keys** replaces the lines for one server in the `known_hosts` of its SSH key Secret. Use
    `POST /servers/{name}/host-keys` or `geass servers update NAME --known-hosts-file FILE`. Lines for other servers stay.
    Geass refuses a machine that it created, and keys that list no key for the address of the server.
  - **Cordon** and **Uncordon** use `POST /nodes/{name}/cordon`, `POST /nodes/{name}/uncordon`,
    `geass nodes cordon NODE`, and `geass nodes uncordon NODE`. Geass refuses to cordon the last node that can start new
    services. Geass refuses to uncordon a node that it cordoned for an upgrade or for a pool.
  - The nodes in the API now have `cordoned` and `cordonedBy`. The **Status** column is now the second column. It says
    **Cordoned** where it said **Unschedulable**. The row actions are in a menu.
  - **HA readiness** shows a table with every condition and whether it passed.
  - Geass does not remove a control plane server here. Geass has no SSH access to it, so it cannot stop K3s on the machine.
- **Managed mode, for a platform that runs Geass for other people.** `spec.managed.enabled` on the platform configuration
  (`spec.managed.provider` names the platform in the messages) makes the dashboard's API refuse every write to sign-in
  (`/api/settings/auth/*`, and an organization's own provider), to the dashboard's domain and DNS (`/api/settings/domain/*` and the
  Cloudflare connection) and to the bucket for the copies of the cluster's state (`/api/settings/etcd-snapshots/*`), whoever asks,
  with `403`, `code` `managed`, and a message that says who manages it. It does so on the dashboard's routes and on the same
  operations under `/api/v1`, and nothing is written. The dashboard is told in its bootstrap (`platform.managed`, for the platform's
  administrators) and shows those pages as they are, read-only, with a notice, no installer command, no banner that asks for a
  domain, and a Version section that says who updates Geass and K3s. The dashboard has no route that updates Geass or sets the K3s
  version, so for those two it only says who does. Reads, and everything else (organizations, pools, servers, tokens, registry,
  alerts, GitHub), are untouched, and so are the controllers. It is off unless set, and with it off the answers and the bootstrap are
  the same, byte for byte, as without the field (tested). It is a guard rail and not a boundary: whoever can write the platform
  configuration or the cluster's objects, or runs the installer, can still change these. Tested with a fake client through the
  dashboard's whole handler chain, and the dashboard's own tests in jsdom (17 new); **run on a real K3s on EC2 (2026-10-28): all 32 writes to those areas refused, nothing written, writes work again
  with it off**; not tested in a browser, with a real sign-in session or identity provider, or with rungeass's own operator. See [Managed mode](website/src/content/docs/docs/operations/managed-mode.md).
- **Mesh networking: nodes at several sites, some behind a NAT.** `spec.network.mesh` on the cluster says what joins them:
  `provider: WireGuard` (the default, K3s's own `wireguard-native` backend, which is what `multiSite` always meant) or
  `provider: Tailscale` (a Tailscale or Headscale network through K3s's `vpn-auth`, with `tailscale.authKeySecretRef` and an
  optional `controlServerURL`). The installer takes `GEASS_MESH=wireguard|tailscale`, with `GEASS_TAILSCALE_AUTH_KEY_FILE` and
  `GEASS_TAILSCALE_CONTROL_URL`; for Tailscale it writes `vpn-auth-file`, the tailnet address as `node-external-ip` and
  `tls-san` and no flannel setting, keeps the key in a mode 600 file and in the Secret `geass-mesh-auth`, and sets
  `spec.serverURL` to the control plane's tailnet address. A worker joined by the operator brings itself onto the tailnet
  first (new steps `install-tailscale` and `join-tailnet`, with the key taken out of every error) and advertises the
  address it got there; a WireGuard worker advertises its `spec.eip`. A Tailscale mesh opens no UDP 51820, in the firewalls
  Geass makes at clouds or at the control plane. With the field set, the operator refuses a join that cannot work (mesh
  settings that cannot be used, a missing or unusable auth key, a WireGuard address that is not a usable IP address, a
  control plane that was built without the mesh) and writes a `MeshReady` condition on the cluster naming what needs
  attention (a private `serverURL`, nodes that are not on the mesh, a control plane without the mesh); the installer
  refuses a WireGuard mesh address no other machine can use. Apart from the overlay check in the next entry, it never tests that traffic flows between sites, that UDP 51820
  is open, a NAT's behaviour or the path's MTU: the documentation gives the commands to check. A cluster that does not set the field gets exactly the K3s settings,
  join commands, cluster object and monitoring values it got before (tests compare them with what the code produced earlier),
  and `multiSite` alone is still joined and left alone as it was. Off by default; additive to the CRD. **Run once, on EC2, in
  two networks of one region** (2026-10-28): a WireGuard worker behind a Linux NAT that forwarded only SSH joined in 17 s and stayed joined; with randomised source
  ports and with UDP 51820 forwarded it worked too; with UDP 51820 dropped the join succeeded and every check in Geass stayed
  green while pods lost every packet. Tailscale worked against a self-hosted Headscale with exactly what Geass writes. The
  installer now warns when `GEASS_NODE_EXTERNAL_IP` is on no interface of the machine (an EC2 Elastic IP is not, and the
  WireGuard install fails without it on `lo`). **Not run:** wide-area paths, other clouds, Tailscale's own service, a pool
  at a second provider. Also tested with unit tests, tests against a real API server (the CRD's rules and defaults, the
  condition) and the worker's new shell commands run locally with a stand-in `tailscale`. See
  [Mesh networking](website/src/content/docs/docs/operations/mesh-networking.md).
- **A non-fatal overlay check for a mesh.** On the EC2 run a blocked UDP 51820 left the node, the `GeassNode`, the cluster and
  `MeshReady` green while pods lost every packet. For a cluster that sets `spec.network.mesh`, the operator now makes a TCP
  connection from its own pod (which the installer pins to a control plane node) to port 9 on the first address of each other
  node's IPv4 pod range, which on K3s is the node's `cni0` bridge. The kernel routes it by the route to that range, so it follows
  the flannel WireGuard interface or `tailscale0`. A refusal or an accepted connection is an answer; silence for 4 seconds or "no
  route to host" is not. A node that gives no answer on two probes in a row turns `MeshReady` `False` with the new reason
  `OverlayUnreachable`, a message that names the nodes, says it is one probe from one point, and says what to check (UDP 51820 and
  the NAT, or the tailnet's rules and routes, or a firewall on the node). When nodes answer, `MeshConfigured` says how many and
  what was and was not tested, or why no probe ran (the operator is not on a node's pod range, no other node is ready, or it is
  turned off with the annotation `geass.dev/mesh-overlay-check=off` on the cluster). It never refuses or fails a join, never
  changes the cluster's `Ready`, runs nothing on a node and uses no SSH, and runs only while `MeshReady` would otherwise be
  `True`. Bounded: one connection per node, at most 10 at a time, 20 nodes and 15 seconds per pass, five minutes between probes of
  a node that answers and 30 seconds doubling to five minutes after a silence. It does not test two workers against each other,
  large packets (MTU), DNS or a service, and it can be wrong (a node with no pod yet has no bridge address, a firewall on the node
  can drop the connection while pods reach each other). A cluster that does not set the field is not probed and gets no new
  condition (tested). No API change. Tested with unit tests and a fake connection (answers, silence, no route, hang; schedule,
  bounds, nothing written for a cluster without a mesh), one test against a real API server for the condition, and one connection
  to this machine's own loopback address. **Run once on EC2 (2026-10-29), in one region:** the operator's pod can open the
  connection; with WireGuard (a public worker and one behind a Linux NAT) and with Tailscale against Headscale, every dropped
  UDP 51820 and every Headscale rule that stopped allowing the pod range was named, 4:35 to 5:22 after the block (5:35 after
  `Ready` for a worker joined with UDP already dropped; about 5:40 is the worst case by the schedule) and cleared in 9 seconds
  to 4:56 after the fix; there was no false alarm over a 35 minute soak of a healthy WireGuard mesh with two workers. It also
  gave two false reports and was late: a default-deny `ufw` on a worker dropped the probe while pods reached each other, a
  worker with no pod on the pod network had no bridge address to answer, and the first probe of a node came 5:00 to 5:13 after
  `Ready`, not about two minutes (see Fixed). **Not run:** many nodes, a path between two workers, large packets, a wide-area
  path, a worker whose `ufw` was already active when Geass joined it, a `NotReady` WireGuard worker, a network policy around
  the operator's pod, a soak longer than 35 minutes, Tailscale's own service. See
  [Mesh networking](website/src/content/docs/docs/operations/mesh-networking.md#the-overlay-check).
- **Move a project between clusters: export and import.** `POST /api/v1/projects/{p}/export` gives a project's definition as a
  bundle (a gzip tar of YAML with a JSON manifest): its services, databases and buckets, their configuration, hostnames and
  schedules, and none of its secret values, credentials, tenant or data. What it leaves out is listed in the manifest,
  including every secret key and every value whose name or content looks like a secret, to be entered again.
  `POST /api/v1/projects/import` makes a new project from a bundle, on the same or another cluster: it plans first (`?dryRun=true`
  says what would be made and what the cluster lacks, and changes nothing), refuses with the plan on a taken name or id, a region
  or required node tag the cluster lacks, a hostname another service has, missing object storage or the organization's project
  limit, and otherwise makes the project, its databases, buckets and services as drafts under new names, so that nothing runs and
  no secret exists until it is deployed; a step that fails removes what was made. Both are for administrators (not a project's own
  token), run for the caller's organization, and are logged with who and how many, never what. A bundle is read as untrusted
  input: it is refused when it names a Secret, ConfigMap, login or connection of the cluster that reads it, or is over 16 MiB.
  The data of databases and buckets is not moved: the guide describes moving a database's data with the backups Geass already
  makes (the import can keep the project's id for platform administrators, which is what restoring them needs), and a
  design for restoring at import time. There is no setting: nothing changes until someone calls the two routes. Tried with
  unit tests, the routes of a fake cluster with two organizations (and added to the tenant matrix), a real API server
  (envtest) that accepts what an import makes and has the project controller make its environments, and **two real K3s
  clusters on EC2 (2026-10-28): export, dry run, import, refusals, and deploying what an import made**. Not tried: a real
  backup restored into an imported project, a dashboard page, the command line (`geass api` cannot receive the file; use
  `curl`).
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
- **Metrics and logs in stores outside the cluster.** `spec.observability.external` on the platform configuration names a
  metrics store (Prometheus remote write, and a Prometheus-compatible query API, with a tenant header, `X-Scope-OrgID`
  unless another is named) and a log store (VictoriaLogs, with the `AccountID` and `ProjectID` tenant), each with a
  bearer token or a username and password from a Secret. Prometheus remote-writes every sample to the one, Fluent Bit
  sends every line it collects to the other as well, and the dashboard reads metrics and log history from them instead
  of the cluster's own stores, so a dashboard that runs outside the cluster (`--controllers=false`) shows them too. The
  credentials are copied beside the add-ons that send, never into their settings. A store that is named wrongly is
  logged and left out, and one that does not answer or refuses the credentials is shown as unavailable. Off unless set;
  alerts still read Alertmanager in the cluster, and Loki is not supported. Tested against fakes of the stores, and run on a
  real K3s (v1.36.4+k3s1) against single-node VictoriaMetrics v1.155.0 and VictoriaLogs v1.53.0, not a running Mimir.
  Prometheus began to write about 100 seconds after the setting changed, and three changes in about ten seconds once
  left K3s's helm controller's install Job of the monitoring chart suspended and the cluster `AddonsPending` until the
  Job was deleted: the docs say to change the setting one step at a time.
- **GitHub through a provider's App.** A provider that runs Geass for many clusters, such as rungeass, can run one GitHub App
  for all of them: a `GeassGitHubConnection` with `spec.external`, made through `PUT /api/v1/github/connections/{name}` by the
  platform's administrators. The cluster then holds no App private key and registers no App. The provider records which
  installations are the cluster's and which organization's projects may use each (`.../installations/{id}`), gives Geass each
  installation's token and its expiry (`.../installations/{id}/token`, stored in a Secret, never shown or logged), and forwards
  GitHub's events to `/webhooks/github/forwarded/{name}`, signed `X-Geass-Signature: t=...,v1=...` (HMAC-SHA256 of
  `<t>.<event>.<body>`, five minutes either way, a copy refused), which Geass handles as it handles GitHub's own. The connection
  shows each token's state and expiry (`status.installations`, condition `TokensReady`); a build waits, and says why, while a
  token is missing or expired, instead of failing. Settings → Connectors → GitHub shows the provider's installations and their
  tokens in place of the App setup. Clusters with their own App are unchanged.
- **A sandboxed runtime for organizations' code.** With `spec.tenancy.sandbox.runtimeClassName` (such as `gvisor` or a
  Kata class), the pods Geass makes for a project that belongs to an organization run under that RuntimeClass: its
  services' copies (pull request environments included), its functions' schedules and its builds. The projects of no
  organization, Geass's own pods and object storage are left as they are (for database servers, see the entry below).
  It fails closed: while the RuntimeClass is not in the cluster, such a service writes no Deployment (one that was running is left as it was) and
  a build stays `Pending`, each with the condition `Sandboxed` `False` `RuntimeClassMissing`, a `Warning` event
  `SandboxRuntimeClassMissing` and a message naming the class, and they go on when it appears. Turning it on, off or to
  another class rolls the organizations' services through their Deployments; builds take it from their next run.
  `skipOnReservedNodes` runs without the sandbox only the pods that can certainly only run on nodes reserved for their own
  organization. The nodes need the runtime installed and the cluster the RuntimeClass; the docs give the K3s containerd
  configuration for gVisor. Run on K3s v1.36.4+k3s1 with gVisor `release-20261019.0` (services, a Kaniko build and database
  servers), never with Kata. Off by default: without it no pod template changes. The
  operator now records events and reads RuntimeClasses.
- **Organizations' database servers in the sandboxed runtime.** With `spec.tenancy.sandbox` set, an organization's MySQL
  (one server, or highly available on MOCO through its `spec.podTemplate`), Redis and SQLite (rqlite) servers run under
  the RuntimeClass, for the databases of its projects and for shared servers whose `spec.org` names it, with the Jobs that
  copy, restore and make logins and logical databases on them; the platform's own servers and the object storage server
  are left as they are. It fails closed as services do: while the RuntimeClass is missing a new server is not made and a
  running one is left as it is (`Sandboxed` `False` `RuntimeClassMissing`, one event, no failure counted). PostgreSQL
  (CloudNativePG 1.25, whose `Cluster` takes no RuntimeClass) and MariaDB (mariadb-operator, likewise) are not run outside
  the sandbox for an organization: the dashboard refuses them (`400`, "This database cannot run in the sandbox: …"), the
  operator refuses one written past it, and one made before is held, running and serving but no longer changed
  (`EngineCannotBeSandboxed`, event `SandboxEngineUnsupported`). A running server is not restarted for the setting:
  `spec.tenancy.sandbox.databaseRestarts` is `OnApproval` by default, so it moves when a platform administrator
  annotates it `geass.dev/sandbox-restart` (and says `RestartPending` until then), or `Immediate`. Restarts are the
  StatefulSet's, Deployment's or MOCO's own; a Redis server comes back empty. Tested against fake clients and envtest, and
  run on a real K3s with gVisor: MySQL 8.4.11 and Redis servers, rqlite servers (started, not queried), the Jobs, both ways of approving a restart,
  the refusal of PostgreSQL and MariaDB and the hold on a PostgreSQL made before. A MySQL server of Geass's default 512 MiB
  was killed during its first start under gVisor and left with no password for `root@localhost` and no `app` login, while
  saying `Ready`; one of 1 GiB started cleanly, and the docs say so. MOCO, and PostgreSQL or MariaDB in the sandbox, did not
  run. Nothing changes without the setting.
- **Noisy-neighbour limits for shared clusters.** `spec.tenantLimits.defaults` on the platform configuration, and `spec.limits`
  on an organization in their place, hold each namespace an organization's pods run in (every environment of its projects,
  and its build namespace): a ResourceQuota `geass-tenant-quota` on its pods and ephemeral storage; a LimitRange that gives a
  container that asks for nothing a default request and limit of CPU, memory and ephemeral storage (so that no pod is free to
  the scheduler or the usage meter) and refuses one above the most; and each pod's bandwidth, which a mutating admission
  policy `geass-pod-bandwidth` puts on every pod as the CNI bandwidth plugin's annotations (Kubernetes 1.36, and only where
  the network plugin runs that plugin). An organization's builds are given the most a container may have. A project says on
  its `TenantLimitsApplied` condition whether they are applied, and a service whose pods they refuse says why. Pods per node
  and disk I/O are not limited: Kubernetes has no means to. Off by default, and nothing has changed for a platform that
  does not set them. Tested against fake clients and the API server's own admission policy code, and once on a real K3s,
  whose API server admitted pods through the limits and whose flannel shaped a pod's traffic (after a large default burst).
- **Outbound limits for shared clusters.** `spec.tenancy.egress` on the platform configuration, and `spec.egress` on an
  organization in its place field by field, close ports to addresses outside the cluster for the pods of organizations'
  projects, in every environment and in their build namespace: the mail ports (TCP 25, 465, 587, 2465, 2525 and 2587) by
  default, `blockedPorts` besides, or all but `allowedPorts`, with `allow` opening ports to ranges of addresses such as a mail
  relay. They are written into the policies that already decide where those pods go (`geass-block-cloud-metadata` and
  `geass-build-isolation`), as port ranges, because a policy of their own could not close what those open; the metadata, node
  and private-range blocks and the cluster's DNS are untouched, and the cluster's API port stays open from environments. A
  project says on its `EgressLimitsApplied` condition whether they are applied and whether the cluster enforces network
  policies. Closing mining pools' ports is offered as a heuristic, and nothing detects mining or counts mail. Off by default,
  and every policy is as it was for a platform that does not set them. Tested against fake clients and envtest, and once on a
  real K3s with real traffic: mail and a port range closed, the ports beside them open, a relay allowed; and, on K3s v1.36.4+k3s1,
  the mail ports of a public host refused for an organization's pod and reached from a pod of no organization and with `mail: Open`.
- **A mail relay for organizations.** With `spec.tenancy.mail.relay`, Geass runs an SMTP submission server, `geass-mail-relay` in
  `geass-system`, from the operator's own image (`manager mailrelay`), which organizations' services reach inside the cluster
  whatever their outbound limits say, and which sends through one mail service the platform chooses (SES, Postmark, a company's
  relay: `upstream` with `host`, `port`, `tls` and `credentialRef`). A client must start TLS before it signs in, and checks the
  relay's certificate against an authority Geass keeps; each organization signs in with a login of its own
  (`geass-mail-credentials-<organization>`, 32 random bytes, compared as a digest in constant time, rotated with the annotation
  `geass.dev/rotate-mail-credentials` with the old password taken for a day, revoked by deleting it), which each environment of
  its projects has as the Secret `geass-mail` (`SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_SECURITY`,
  `SMTP_CA_CERT`) for a service to reference; nothing is put into pods by itself. Each organization is held to messages per minute,
  hour and day (`450 4.7.1`), recipients per message (`452 4.5.3`), message size (`552 5.3.4`) and connections (`454 4.7.0`), with
  defaults, the platform's `limits` and an organization's `spec.mail.limits` field by field, and to its sender domains in the
  envelope and the `From` and `Sender` headers (`allowedSenderDomains`, `requireSenderDomain`, an organization's own list in place
  of the platform's), and nobody sends to `deniedRecipientDomains` (`550 5.7.1`). A message is answered only once the upstream took
  it, so there is no queue; an upstream that is down, defers or refuses the relay's login is a temporary failure. The counts are
  kept in the memory of the relay's one replica: a restart starts them over, which the docs say plainly. Metrics by organization
  (`geass_mail_relay_*`, scraped with the admin token when monitoring is on), structured logs without a secret, and
  `GET /api/v1/mail/usage` for the platform's administrators; the condition `MailRelayReady` on the platform configuration. No
  content scanning, DKIM, SPF, DMARC or bounce handling. Off by default: nothing is made or changed until it is set, and turning it
  off removes everything it made. Tested in-process with go-smtp's client against the relay and a fake upstream (under `-race`),
  against fake clients and envtest, and run on a real K3s against an SMTP sink with Python's `smtplib`; not against a real mail
  service. Adds `github.com/emersion/go-smtp` and
  `github.com/emersion/go-sasl` (both MIT).
- **Registry isolation for organizations.** With `spec.tenancy.registry` (`policy: requireOwnRegistry`, the default), a
  build of a project that belongs to an organization is never given the platform's registry login, nor another
  organization's: one that would be fails before its Job is made, and one running with it when the setting is turned on
  is stopped. The organization's namespaces never hold a copy of it (old pull secrets and build copies are deleted). An
  organization's builds push to its own registry under `<repository>/<project>/<service>`, and an organization without
  one does not build: the platform's registry is no fallback, as Geass cannot give a login for a part of it. Parts of
  registries belong to the platform (its repository), to each organization (`<platform repository>/<organization>`, and
  its own registry), and an organization's service may not run an image, nor its build push, in a part that is not its
  own; two registries that overlap are nobody's. The dashboard refuses an organization a registry that overlaps another's
  (`409`), and `GET /api/registry` says `ownRequired`. Refusals are the condition `RegistryIsolated` `False`
  (`OwnRegistryRequired`, `PlatformCredential`, `ForeignRepository`), a `Registry isolation: …` message and a `Warning`
  event `RegistryRefused`. A registry that authorizes each organization for its own path (Harbor, zot) is how one registry
  is shared; the docs give the steps. Run on a real K3s against two `registry:2` instances with real Kaniko builds, not
  Harbor or zot; Kaniko needs a registry it can push to over HTTPS or by a name it takes as local, which the docs say. Off by default: without it the build Jobs, copies
  and pull secrets are unchanged. The operator now records events.
- **Abuse detection for shared clusters.** With `spec.tenancy.abuse`, Geass looks for the services of organizations' projects
  that may be abusing the machines, such as by mining, and says so; what it finds is a signal, not proof, and every answer says
  so. The sustained-CPU signal samples each service once a minute from the metrics server (`metrics.k8s.io`, which K3s runs):
  what all its copies use over what they are allowed (each container's limit, or its request), and suspects one that stays at
  90% or more for 30 minutes without a break (`cpu.thresholdPercent`, `cpu.windowMinutes`), only while it is settled, so that
  a rollout, a restart, a change in the number of copies and scale to zero never add up to a suspicion. Optionally it counts
  only while the ingress passes the service few requests (`cpu.quietTraffic`, from Prometheus with the monitoring add-on),
  with a stricter threshold and window when they cannot be read. The runs are kept in the ConfigMap `geass-abuse-signal`, so a
  restart carries on. A suspect gets the condition `AbuseSuspected` (and its project an aggregate), a `Warning` event once, and
  the alert **Abuse suspected** (`abuse.suspected`), which only the platform's channels hear. `action: Suspend` scales a
  suspect to zero, reversibly, deleting nothing (the default, `None`, changes nothing). `GET /api/v1/abuse/suspects` lists the
  suspects with their evidence, and `POST .../suspects/{name}/clear` and `/release` review them; `POST /api/v1/abuse/reports`
  takes a report from an outside detector (falcosidekick's webhook body as it is), which Geass does not install, and which
  cannot see inside gVisor-sandboxed pods. All of it is the platform's administrators' alone. An organization can be exempted
  whole or by project (`spec.abuse` on it), for legitimate heavy work, and given its own action. The outbound limits gain
  `miningPools: Blocked`, which closes a curated list of mining pools' default ports, a heuristic that a miner on port 443
  walks past. It misses a miner that stays below the threshold, pauses, restarts itself or uses a GPU. Off by default, and
  nothing is sampled or written for a platform that does not set it. Tested against fake clients, a fake clock and envtest, and
  run on a real K3s (v1.36.4+k3s1) with busy loops, the metrics server K3s ships, a falcosidekick-shaped report and gVisor; not
  against a real Falco or a real miner. The action is decided when a suspicion is raised, so switching `action` to `Suspend`
  does not suspend a service that is a suspect already. The operator now reads `metrics.k8s.io`.
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
  makes a node wait and does not fail it. A connection without a `sessionToken` works as before. Run against a live AWS
  account with an assumed role's credentials: accepted, refused with a corrupted token, renewed, and machines launched and
  terminated with them.
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

- **The documentation follows the Google developer documentation style guide and the ASD-STE100 writing rules.** The
  README and every page at rungeass.com/docs use short sentences, the active voice, and one word for one meaning. The rules
  are in `docs/writing-style.md`. A check of the pages against the code also corrected about 30 statements, for example the
  highly available control plane, the token limits, MariaDB support, and the sign-in lockout.

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

- **A terminal that reaches its time limit is always recorded as expired.** Before this fix, the dashboard and the operator
  both ended the session, and the record depended on which one wrote first. The dashboard wrote `Closed` with the reason
  `Terminal closed`, and the operator wrote `Expired` with the reason `Session timeout`. Now both write `Expired` and
  `Session timeout`.
- **The message that refuses high availability for Redis now names MariaDB.** The message said "requires PostgreSQL or
  MySQL", but MariaDB also supports high availability. It now says "requires PostgreSQL, MySQL, or MariaDB".
- **The CRD description of `githubConnectionRef` on a project is correct.** It said that the field names a Secret. The field
  names a `GeassGitHubConnection` in `geass-system`. Only the description changed.
- **What the first run of the mesh overlay check on EC2 found.** (1) The first probe of a node waited for the five-minute look
  of the cluster (5:00 to 5:13 after `Ready` in seven of seven cases, 6:00 for a worker with no pod): nothing remembered nodes
  that were still in their two minutes, so nothing asked for the cluster to be looked at when the two minutes ended. The check
  now looks at the cluster again at the moment the first such node has been `Ready` for two minutes (never sooner than 5
  seconds, never later than the five-minute look), so the first probe is meant to come about two minutes after `Ready`; the
  five-minute cadence after an answer, the two silent probes and the back-off are as they were. (2) A worker with no pod on the
  pod network (a `storage: None` taint keeping `svclb` off it, or only host-network pods) has no `cni0` bridge address, and was
  named `OverlayUnreachable` with a healthy tunnel and a message that sent the reader to UDP 51820. The operator now reads the
  pods it already caches (no new permission: `pods` was already `get`, `list` and `watch`, and `config/rbac/role.yaml` is
  unchanged), and probes only a node that runs a `Running` pod that is not on the host network. Any other node is left out
  and named in the message with the reason, and the cluster is looked at again every two minutes while a node is in that state.
  The rule is one-sided: a pod that runs proves the bridge exists, so a node with one is probed as before, and a node that
  once had pods and has none now (its bridge may still exist) is left out as well, so a real block there is not reported
  until a pod runs. (3) With no node to probe, the message said "none is Ready for more than 2m0s", which did not say a node
  was not `Ready` (a stopped `tailscaled` turns a node `NotReady`); it now lists the nodes left out and why ("not Ready",
  "Ready for less than 2m0s", "no pod is running on the pod network yet", "no IPv4 pod range"). (4) The message for an
  unreachable node now says that a firewall that denies incoming traffic by default, as `ufw` does, drops the probe while pods
  reach each other, and names `ufw allow from 10.42.0.0/16` (K3s's default pod range), which made the node answer on EC2.
  The docs give the measured numbers (named in about five to six minutes, cleared in 9 seconds to 4:56) and say what the run
  found and what it did not cover. **Tested with fakes only** (a fake clock and connection, fake pods, one test against a real
  API server); **not run on any real machine.** A real run has to show the first probe about two minutes after `Ready`, no
  report for a worker with no pod, and the `ufw` text.
- **The first deploy of a service that carried a secret could fail with `Secret "<service>-secrets" not found`.** A secret set on a
  service that is still a draft (`POST /api/apps/{name}/secrets/set`) is written to the Secret `<service>-secrets` in the system
  namespace, and `spec.secretRef` points at it. The operator's pass over a service that is waiting to be deployed cleared what it
  had made for it, and that clearing also deleted the Secret `spec.secretRef` names. The values were gone before the deploy, which
  read a Secret that was not there and ended as "Provisioning failed after repeated errors". It depends on whether the operator
  looks at the draft between setting the secret and deploying, which fits what was seen (a first deploy long after the secret
  failed; a second service, a service already deployed and a draft with no secret all worked). The same clearing ran for a Git
  service waiting for its build and when a deployed service was moved to another environment. The values are now removed only when
  the service is deleted. A service that already lost its secret is not repaired by the upgrade: set the secret again, then deploy
  or retry the provisioning. The bug is older than the managed-mode, mesh and project-move changes of this release. Found on two
  real K3s clusters (2026-10-28). **Tested** with five new unit tests on a fake client: a draft, a Git draft and a service moved
  to another environment keep the Secret, and a draft's secret reaches the service's namespace when it is deployed (these four
  fail on the code before this change); deleting a service still removes the Secret `spec.secretRef` names (passes before and
  after). **Not run** on a real cluster, against a real API server (envtest) or through the dashboard and the operator together:
  the timing above is read from the code and the two runs, not reproduced.
- **A service could not be deployed from a dashboard that runs outside the cluster.** Deploying records a deployment and then writes
  its status, which the `--controllers=false` dashboard's role (`config/dashboard-only`) did not allow, so the service was saved as a
  draft and "deploying it failed". The role can now update the status of a `GeassDeployment`.
- **Making and deploying a service in one request could fail with "saved as a draft" although it was made.** The service is saved a
  second time with its deploy marked; the controller has by then added its finalizer, so that save conflicts, and the latest copy was
  read through a cache that had not seen the new object yet and found nothing. A copy that is not found yet is now asked for again.
- **The console of a SQLite database never worked on rqlite 8.** It ran `rqlite -e <statement>`, which rqlite v8.36.16 does
  not have (its shell only reads a prompt), so every statement answered `ERR! undefined option -e`. The statement now goes
  to rqlite's HTTP API as one argument of `wget`, with no shell. Found on a real K3s.
- **A SQLite server was lost when its pod was replaced.** Geass started rqlite without `-node-id`, so rqlite named the node
  after the pod's host name, which a Deployment changes with every pod (a drain, a rollout, a restart into the sandbox). The new
  node was not in the raft configuration kept on the volume, and the server answered `503` ("not part of a stable
  configuration") for good. A server made from now on is started with `-node-id <database name>`. One made before is left as
  it is, because a different ID at its next rollout would strand its data the same way: it still has the problem, and needs its
  raft configuration recovered or its data moved. Found on a real K3s, with and without the sandbox; a fixed `-node-id` on the same image survived a pod replacement there.

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
- **A shared database server holds the databases of one organization.** While organizations are on, the platform's
  shared server is for the projects of no organization, as an organization's server already was for its own projects.
  The dashboard refuses a logical database, a project's move (`POST /api/projects/{p}/assign`) and a server given to an
  organization (the new `POST /api/shared-databases/{name}/org`) that would put two owners' data on one server, and the
  operator does not make such a database however its objects were written. One that was already on its server is kept
  and served, and reported: the condition `OrganizationIsolated` (`False`, `MixedOrganizations`), a Warning event, and
  `GET /api/shared-databases/mixed` for the platform's administrators, which also shows, before organizations are turned
  on, what they will report. The operator may now create `events.k8s.io` events. With organizations off nothing changes.
  Run on a real K3s with single MySQL servers, not PostgreSQL or MariaDB.
- **Secrets are encrypted at rest.** The installer turns on K3s secrets encryption (`secrets-encryption: true`) on every
  cluster it builds, so the dashboard users, API token hashes, cloud keys, app secrets and database passwords are encrypted
  in K3s's datastore and in the etcd snapshots, including the one taken before an upgrade. A control plane server joined
  with `--join-server` gets the same setting. The key stays on the server's disk, and anyone the API lets read Secrets still
  reads them. Only the Secret resource is encrypted: a value written into a service's `spec.secretData`, and plain
  variables, stay readable in the datastore. A restore on another machine needs the cluster's server token and the same
  setting; the Upgrading page of the documentation says how.

[Unreleased]: https://github.com/degoke/geass/commits/main
