---
title: "Security"
description: "How Geass authenticates users, tokens, the CLI and agents, stores secrets, isolates projects and what it exposes, with a hardening checklist."
---

This page describes how Geass protects what it manages, based on what the code does today, and lists what you still
have to do yourself. Geass runs on servers you own, so most of the surface is yours: the operating system, the network
in front of the servers, the K3s datastore and the accounts you connect. Geass holds no security certification and has
had no independent audit, and nothing here should be read as one.

## The model in one table

| Surface | Protected by |
| --- | --- |
| Dashboard and REST API | Username and password (bcrypt), a signed session cookie, or an API token; users are `admin` or `viewer`, tokens can also be `observer` |
| Writes from a browser | Same-origin check on every mutation (the request's `Origin` or `Referer` must match the dashboard host) |
| Writes from scripts | API tokens sent in a header, stored only as hashes |
| Command line and agents | A device code that a signed-in admin approves in the dashboard, then an ordinary API token; the MCP server acts as that token |
| Project to project traffic | A network policy per project environment (on by default) |
| Where a project's data runs | Compliance tags: node selectors on everything the project runs, and an admission policy as a backstop |
| Cloud metadata service | A network policy that blocks `169.254.0.0/16` from every project namespace |
| Metrics and log stores | Network policies that admit only platform namespaces |
| HTTPS certificates | cert-manager and a Let's Encrypt `ClusterIssuer` that Geass creates once you give it a contact email |
| Secrets | Kubernetes Secrets, not encrypted by Geass |
| Outbound webhooks | Signed with a per-webhook secret; private and loopback addresses refused |
| GitHub webhooks into Geass | HMAC-SHA256 signature check |
| Servers | Your OS, your firewall, SSH keys you supply |

## Authentication

### Dashboard users

Users come from the Secret `geass-dashboard-auth` in `geass-system`. Passwords are stored as bcrypt hashes: a plain
password you write into the Secret is replaced by its hash the first time Geass reads it. Geass refuses the sample
password `CHANGE_ME`, and it does not enforce a minimum length. Creating and changing users, and the two roles, are in
[Users and API tokens](/docs/guides/users-and-tokens/).

A successful sign-in sets the cookie `geass_session`. It is HTTP only, `SameSite=Lax`, valid for 12 hours, and signed
with an HMAC key kept in the same Secret (`session-key`). The cookie carries the username, role, an expiry and an epoch;
Geass checks the user's current role and epoch on every request. Signing out bumps the epoch, which ends every session of
that user. The cookie is marked `Secure`, except when the request is plain HTTP to a loopback address.

Five failed sign-ins for one username in a minute lock that username for a minute, and the reply is the same while
locked. The counter lives in the Secret, so it survives restarts. The lock is per username, not per client address, so an
attacker can keep a known username locked. Put a rate limit in front of the dashboard if that matters to you.

There is no two-factor authentication and no single sign-on. Use long unique passwords and restrict who can reach the
dashboard.

### API tokens

Tokens have a prefix (`gsw_` for a workspace token, `gsp_` for a project token), a public identifier and a 160-bit random
secret. Geass stores a SHA-256 hash of the secret in the Secret `geass-api-tokens`, compares in constant time, and shows
the token once. Workspace tokens are `admin`, `observer` or `viewer`. A project token can act on one environment of one project, through
a fixed list of endpoints, and cannot create resources, open terminals or change settings. Tokens can expire (up to ten
years, or never) and can be revoked at once. Only a signed-in admin can create, list or revoke tokens, or approve a device code. A
token is not tied to the account of the admin who created it, so removing that user does not revoke it.

### Roles

A viewer sees a redacted copy of the platform: no variable values, hostnames in the spec, repository names, connections, backup
storage, project webhooks or database host details, and no logs, terminals or secrets. A viewer cannot change anything.
An observer, a role only a token can have, sees the same redacted copy and also reads logs, pods, deploy logs and variable names,
and changes nothing. Everything else needs an admin. The detail is in [Users and API tokens](/docs/guides/users-and-tokens/#roles).

Two limits of the observer role are worth knowing. It reads the values of a service's plain configuration (only secret values are
withheld), so keep sensitive values in secrets. And the logs it reads are whatever your apps print.

The terminal's WebSocket checks that the console session exists and is active, and that the request's origin is allowed. It does not check
the caller's role again, and the access check lets observer tokens through to it. An observer cannot create a session, so it has
nothing to connect to unless it knows the name of one an admin opened in the last five minutes.

## The command line and AI agents

The [`geass` command line](/docs/guides/command-line/) and the [MCP server](/docs/guides/mcp-server/) are clients of the
[`/api/v1` API](/docs/reference/rest-api/). They add no new way to act on the cluster: each does what its token may do through the
same checks as any other API call.

### Signing in with a device code

`geass login` starts a sign-in with `POST /api/v1/auth/device`, which needs no credentials, and shows you a code. A signed-in admin
approves the code on the dashboard page `/device`, choosing the access (read only with logs is the recommended choice and the default)
and a lifetime of 7, 30 or 90 days. The command line, which has been polling, is then handed a workspace token once. What protects this:

- Only a signed-in admin session can approve. A token, even an admin's, cannot, so a leaked token cannot mint another that outlives it.
- A request lives 10 minutes, in the operator's memory. It can be approved once. At most 100 are pending and one address may start 10 a
  minute; the operator counts the connection's address and does not read `X-Forwarded-For`, so behind an ingress that count is shared.
- The user code is eight letters with no vowels. The device code that collects the token is 32 random bytes.
- Approving writes the line `Approved a command-line sign-in` to the operator log, with the token ID, role, client name and approver.

What it does not protect against is an admin approving a code they did not start. Whoever holds the device code receives the token, and
the program that started the sign-in chooses the name the approval page shows. Approve a code only when it matches the one on your own
terminal, and keep the access at read only unless the program must change things.

### Where the command line keeps its token

The command line stores logins in `config.json` in the `geass` folder of your user configuration directory (for example
`~/.config/geass/config.json` on Linux), or at the path in `GEASS_CONFIG`. It creates the folder with mode `0700` and the file with mode
`0600`, and writes the token in plain text, so protect the file as you would an SSH key. `geass logout` removes the saved token from the
file only; the token stays valid until it expires or you revoke it in **Settings** → **Tokens**. `geass config get-contexts --json`
shows saved logins without their tokens. A `GEASS_TOKEN` environment variable or `--token` flag overrides the saved token, and then no
file is involved. In CI, keep the token in your secret store.

### The MCP server

`geass mcp` lets an AI agent see and run your services. It acts as the token you are signed in with, and it offers an agent only the tools
that token may use: a read-only token cannot even see a tool that deploys. `--read-only` removes every tool that changes something, whatever
the token could do, and the tool that deletes resources exists only with `--allow-delete`. No tool returns secret values or database
passwords, but the logs and plain configuration an agent can read may contain sensitive text.

- **Standard input and output (the default).** The agent starts `geass mcp` as a child process. Nothing listens on a network port.
- **HTTP (`--listen`).** The server holds no credentials. Each agent sends its own Geass token as `Authorization: Bearer ...`, and the
  server answers `401` without one, then builds the tool list from that token. The server speaks plain HTTP: it prints a warning when
  the address is not a loopback address, because the token travels with every request. Bind it to `127.0.0.1`, or put it behind TLS and a
  firewall. Geass checks the token on every tool call, so a revoked token stops working at once; the list of tools built for a token is
  cached for up to 5 minutes.
- **Which token to give an agent.** An `observer` token for an agent that should only watch, a project token for one that deploys a single
  environment, and an `admin` token only when you accept that the agent can create services and change anything the tools expose
  (deleting needs `--allow-delete`).

Nothing records which agent called which tool. The operator log shows each API request's method, path and status, not the caller, and the
token's `Last used` time in **Settings** → **Tokens** is updated at most once every 10 minutes. Give each agent its own token so you can
revoke one without the others.

### Cross-site requests and headers

A write that carries a cookie must also carry an `Origin` or `Referer` header whose host matches the dashboard's, which is
what stops another site from using a visitor's session. The terminal WebSocket applies the same rule. Calls with a Bearer
token skip this check, because browsers never attach one.

Every response carries `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: same-origin` and a
content security policy. The policy takes scripts, fonts and connections only from the dashboard's own address (inline styles and
`data:` images are also allowed), refuses to be framed, and limits form posts to the dashboard and `github.com`.

## What Geass exposes

### The dashboard and API

The operator serves the dashboard and API over plain HTTP on port 8085 inside the cluster, behind the Service
`geass-geass-dashboard` (type ClusterIP). Nothing outside the cluster can reach it until you publish it.

You publish it in **Settings** → **Domain**. Geass then creates the Ingress `geass-dashboard` in `geass-system` on the
`websecure` entry point of Traefik (K3s's ingress controller), with TLS from cert-manager, or it points DNS at the cluster
through Cloudflare, or it runs a Cloudflare Tunnel. See [Domains and TLS](/docs/guides/domains-and-tls/).

:::caution
The dashboard's Ingress, like every service Ingress with **HTTPS** on, asks cert-manager for a certificate from a
ClusterIssuer named `letsencrypt-prod`. Geass creates that issuer only when the cluster has a Let's Encrypt contact email:
`spec.addons.certManager.acmeEmail`, which the installer sets from `GEASS_ACME_EMAIL`. Without one, no issuer exists and no
certificate is issued; the `ClusterIssuerReady` condition on the cluster says so, and the dashboard's domain stays
`PendingHTTPS` with the same message. See [HTTPS certificates](/docs/getting-started/install/#https-certificates).
:::

Let's Encrypt must reach your server on 80/tcp to validate a hostname. An issuer you created yourself under that name is
never overwritten, and removing the email later does not delete the issuer Geass made.

Paths that work without a login:

| Path | Why |
| --- | --- |
| `/api/login`, `/api/logout`, `/api/session` | Sign in, sign out, and ask whether you are signed in |
| `POST /api/v1/auth/device`, `POST /api/v1/auth/token` (also under `/api/auth/`) | Start a command-line sign-in and collect its token. Starting one only creates a request that a signed-in admin has to approve |
| `/geass-probe` | A small JSON answer used to verify the public address |
| `/webhooks/github` | GitHub cannot sign in; every request needs a valid HMAC signature |
| `/assets/*` and other page loads | The dashboard's static files. They hold no data |

Everything under `/api/` other than those needs a session or a token. The unauthenticated surface is small, but it is on
the public internet once you publish the dashboard.

### Ports on your servers

| Port | Used for | Who should reach it |
| --- | --- | --- |
| 6443/tcp | Kubernetes API | Your other nodes, and your own address if you run `kubectl` remotely |
| 10250/tcp | kubelet | Your other nodes |
| 8472/udp | Overlay network (VXLAN) | Your other nodes |
| 51820/udp | Overlay network (WireGuard, multi-provider clusters only) | Your other nodes |
| 80/tcp, 443/tcp | Apps and the dashboard through Traefik. Let's Encrypt validates hostnames on 80 | The internet, if you publish services |
| 22/tcp | SSH, used to join servers and for your own access | You, and the control plane toward workers |

The installer opens 6443, 10250 and the overlay port in the host firewall if it finds an active one (`ufw`, `firewalld` or
`iptables`), without limiting the source address. Restrict them to your node addresses in your provider's firewall or
security group. The operator also serves health probes on 8086 and, in the installed manifest, metrics on 8443 (HTTPS,
protected by Kubernetes authentication and authorization); both stay inside the cluster. The operator's manifest includes a
ClusterRoleBinding (`metrics-reader-prometheus` in `config/rbac`) that gives the monitoring add-on's Prometheus service account
(`geass-kube-prometheus-stac-prometheus` in `monitoring`) the `metrics-reader` role, which is what lets Prometheus scrape the operator's
metrics. It binds nobody on a cluster without that add-on.

## Secrets

### Where they live

Everything secret is a Kubernetes Secret: dashboard users and the session key, API token hashes, cloud connection keys,
the Cloudflare token, the GitHub App key, app secrets and shared secrets, function schedule tokens, database passwords, the K3s join token
(`geass-token`) and SSH keys. [Cloud connections](/docs/guides/connections/) lists the names.

- **At rest.** Geass does not encrypt Secrets, and the installer does not enable K3s secrets encryption, so they are stored
  in the cluster datastore like any other Secret. Anyone who can read the datastore, an etcd snapshot or the server's disk
  can read them. K3s offers secrets encryption at rest; if you want it, turn it on in K3s yourself.
- **In the API.** Secret values are never copied into a resource's `status`. The dashboard sends the names of secret keys,
  never their values, and viewers do not even get those. The exception is a service you wrote `spec.secretData` into by
  hand: that field is part of the resource, so admins get its values in `GET /api/bootstrap` (the values are blanked in the
  resource reads of `/api/v1`). Secrets saved through the dashboard or API go into a Secret instead. Plain configuration is not a
  secret: admins, observers and project tokens read its values.
- **In your apps.** App secrets are copied into the environment's namespace as a Secret and injected as environment variables.
  Whatever runs in your app can read them, as with any environment variable.
- **Function schedules.** A function that is a handler, or has schedules, gets a random token in the environment variable
  `GEASS_TRIGGER_TOKEN`, from the Secret `<app>-trigger` in the environment's namespace. The schedule's CronJob sends it in the
  `x-geass-trigger-token` header, and the function tells a scheduled call from a visitor by comparing it in constant time. Anyone who can
  read that Secret can forge a scheduled call. The token travels inside the cluster as plain HTTP, like all traffic between services.
- **In transit.** Inside the cluster, traffic to the dashboard is plain HTTP. Publish it over HTTPS.

:::caution
The password of an in-cluster database that Geass creates is not random. Geass derives it from the database's resource name (`<name>-password`), and the
MySQL root password is the same value. Anyone who knows or can guess a database's name and can reach its port has its password. What protects these
databases is [network isolation](/docs/guides/network-isolation/) and not publishing their ports. Do not turn isolation off for a project that has
databases, and see [GeassDatabase](/docs/reference/resources/geass-database/#connection-secret).
:::

### Who can read them

An admin cannot read secret values through the dashboard, but an admin can open a terminal in a running service and read its
environment. Anyone with `kubectl` and rights on `geass-system` can read every Secret there. The operator itself has
cluster-wide access to Secrets (see the next section).

## What the operator can do in your cluster

The operator and dashboard run as one pod, `geass-controller-manager`, in `geass-system`, on a control plane node. The pod runs
as a non-root user with a read-only root filesystem, no added capabilities and no privilege escalation. Its service account is bound
to the ClusterRole `geass-manager-role`, which allows:

| Resources | Verbs |
| --- | --- |
| Secrets, ConfigMaps, Services, PersistentVolumeClaims, Namespaces | create, get, list, watch, update, patch, delete |
| Nodes | get, list, watch, patch, delete |
| Pods, PersistentVolumes | get, list, watch |
| `pods/exec` | create (used by the terminal and the SQL console) |
| `pods/log` | get |
| Deployments | create, get, list, watch, update, patch, delete |
| StatefulSets | get, list, watch |
| Jobs, CronJobs, HorizontalPodAutoscalers, Ingresses, NetworkPolicies | create, get, list, watch, update, patch, delete |
| StorageClasses | create, get, list, watch, update, patch, delete |
| CSIDrivers | get, list, watch |
| `helm.cattle.io` HelmCharts | create, get, list, watch, update, patch, delete |
| `helm.cattle.io` HelmChartConfigs | create, get, list, watch (it creates one only when the scale-to-zero add-on is on and K3s's Traefik has no configuration of its own) |
| cert-manager ClusterIssuers | get, list, watch, create, update, patch |
| Prometheus Operator ServiceMonitors, PodMonitors and PrometheusRules | create, get, list, watch, update, patch, delete |
| KEDA HTTP add-on `HTTPScaledObjects` (`http.keda.sh`) | create, get, list, watch, update, patch, delete |
| ValidatingAdmissionPolicies and their bindings (`admissionregistration.k8s.io`) | create, get, list, watch, update, patch, delete |
| CloudNativePG clusters, backups and scheduled backups | create, get, list, watch, update, patch, delete |
| Longhorn nodes and volumes | get, list, patch |
| Geass resources | create, get, list, watch, update, patch, delete |
| Geass resource `status` and `finalizers` | get, update, patch for `status`; update for `finalizers` |

It has no rule that grants permission to change RBAC objects. It can read and write every Secret and run commands in every
pod in the cluster, so treat anyone who controls the operator, its image or its service account as a cluster administrator.
Geass also creates HelmChart resources, which K3s's built-in Helm controller installs for its add-ons (cert-manager, monitoring,
Longhorn, logging, MinIO, the scale-to-zero add-on and some database engines), so review the add-ons you enable. The admission policy
rights are used for compliance tags (see [Keeping a project's data on chosen nodes](#keeping-a-projects-data-on-chosen-nodes)): the
operator creates one policy and one binding, which apply only to namespaces it manages.

The other Geass roles in the cluster are the leader-election Role in `geass-system`, a metrics authentication role, and
admin, editor and viewer ClusterRoles for apps, databases, object stores, clusters and nodes that you can bind to people if
you give them `kubectl` access.

Workloads you deploy get no special service account. Their pods mount the default service account token of their
namespace, except function pods, which do not mount one. Geass sets no security context on your pods, so a container runs as
the user its image says, including root.

## Network isolation

Without network policies every pod in a Kubernetes cluster can reach every other pod. Geass applies these on its own:

- **Project isolation.** Every project environment namespace gets the policy `geass-project-isolation`: only the project's own
  namespaces, the projects you list, and the platform namespaces (`kube-system`, `geass-system`, `monitoring`, `cnpg-system` and
  `keda`, where the scale-to-zero proxy runs) can reach its pods. This is the default (`isolation: Project`). A project that sets `isolation: None` is open to
  every pod in the cluster. Details are in [Network isolation](/docs/guides/network-isolation/).
- **Cloud metadata.** Every project namespace gets `geass-block-cloud-metadata`, which lets pods reach everything except
  `169.254.0.0/16`, so a pod cannot read the instance credentials of the machine it runs on. It applies whether or not the
  project is isolated.
- **Telemetry.** The Prometheus and log stores have no login and hold every project's data, so Geass limits them to the
  platform namespaces with `geass-restrict-metrics-store` and `geass-restrict-log-store`.

Policies only help if your network plugin enforces them. K3s's default does. If you replaced the network plugin, check that yours does.

## Keeping a project's data on chosen nodes

Network isolation limits who can talk to a project. Compliance tags limit which machines it runs on. You mark a node tag, such as `hipaa`,
as a **compliance tag** on the nodes that qualify (a compliance tag is always reserved, so no other project's work lands there), and you
set it as a required tag on the project. After that:

- The operator puts the node selector on everything the project runs: services, databases, buckets, builds, and the jobs that back up and
  restore them. Schedules of its functions run there too.
- An admission policy, `geass-required-node-tags`, refuses a new pod in the project's namespaces that does not carry the node selector for
  each required tag, or that names its own node. It is the backstop for pods Geass did not create, such as a chart installed by hand. It
  applies to namespaces Geass manages, and the operator never removes it. It needs a Kubernetes version that serves
  `ValidatingAdmissionPolicy` (1.30 or later); on an older cluster the operator logs that pods created outside Geass are not checked, and
  the node selectors still apply.
- With Longhorn, the copies of a project's database volumes are kept on the tagged nodes (the operator makes a storage class for each
  combination of region and required tags), and other projects' volume copies are kept off compliance nodes.
- The logs of the project are not shipped to the shared log store, so their history is not kept and the logs stay on the nodes. Live logs
  still work.
- A project with databases cannot take on a tag its databases are not on, and a shared database server or the cluster MinIO server that
  does not run on tagged nodes is refused for the project.

It does not move data that is already elsewhere, protect against a compromised node, or cover data outside the cluster: buckets in AWS S3,
PlanetScale databases, and the backups a project writes to S3 (the backup jobs run on the tagged nodes, but they write to your bucket).
See [Compliance tags](/docs/guides/compliance-tags/).

:::caution
Repository builds run as Kaniko Jobs in `geass-system`, which the isolation policies treat as part of the platform, and
a Dockerfile can run any command. Only build repositories you trust. Pods that Geass starts for a build run as root.
:::

## Outbound webhooks

Project webhooks sign each delivery with a per-webhook secret (`whsec_...`), sent as `X-Geass-Signature: t=<time>,v1=<HMAC>`
over `<time>.<body>`. The signing secrets are kept in the Secret `<project id>-webhooks`, not in the project. Deliveries refuse
loopback and link-local addresses always, and private ranges (RFC 1918 and shared address space) unless you set
`GEASS_WEBHOOKS_ALLOW_PRIVATE_NETWORKS=true` on the operator. The address is checked when the connection is made, which stops DNS tricks. Deliveries use no proxy and follow no redirects.

## Credentials your connections hold

| Connection | What the credential can do | How to limit it |
| --- | --- | --- |
| AWS | Create and delete S3 buckets, and create and delete IAM users named `geass-*` | Use the policy in [Cloud connections](/docs/guides/connections/#permissions-the-aws-key-needs); restrict S3 resources to a name prefix |
| PlanetScale | Create databases, branch passwords and Postgres roles in the organization, delete the logins Geass made, and read cluster sizes | Use a token limited to what Geass needs |
| Cloudflare | Edit DNS in the zone, edit tunnels in the account | Scope the token to one zone |
| GitHub App | Read contents, metadata and pull requests of the repositories where it is installed | Install it only on the repositories you deploy |

AWS and PlanetScale connections belong to one project and are only usable by it. The Cloudflare and GitHub credentials belong
to the whole cluster. Per-bucket AWS keys given to apps reach only their own bucket.

## SSH and joining servers

Geass joins a server by connecting over SSH as root or a sudoer, with a private key in a Secret you reference. It does not
verify the server's SSH host key, so it cannot tell if the address now points at a different machine. Join only servers
whose network path you trust, use a key made for this purpose, and prefer a private network. The K3s join token in `geass-token` lets
any machine that can reach port 6443 join the cluster, so guard it like a password.

## Auditing

- **Terminals.** Opening a terminal in a service creates a `GeassConsoleSession` that records who (`token:<name>` for a
  token), which pod and container, the command and a timeout of five minutes. List them with
  `kubectl -n geass-system get geassconsolesessions`. The operator log also notes when each terminal opens and closes. Nothing records what was typed.
- **SQL console.** An admin can run a statement in a database from the dashboard (`POST /api/databases/{db}/query`), which
  executes it inside the database pod with `psql`, `mysql`, `redis-cli` or `rqlite`. No session object is created for it and
  the operator log holds only the request path, so nothing records the statements.
- **Tokens.** Creating and revoking a token writes a line to the operator log, and so does approving a command-line sign-in
  (`Approved a command-line sign-in`, with the token ID, role, client name and approver). Refusing one is not logged.
- **Revealed connection details.** `GET /api/v1/databases/{name}/connection?reveal=true`, which returns a database's password and
  connection string to a workspace admin, writes `Connection details revealed` with the kind, name and caller.
- **Agents and the command line.** Nothing records which command or MCP tool was used. The token's name and `Last used` time are the only
  trace of who called, so give each person, script and agent its own token.
- **Requests.** The operator logs every dashboard request (method, path, status, duration) but not the caller or the body, and the path
  logged is the one the client sent, so `/api/v1/...` calls appear with that path. Geass does not keep a general audit trail of changes.
  Kubernetes audit logging is the way to get one, and it is a K3s setting.

```bash title="Read the operator log"
kubectl -n geass-system logs deployment/geass-controller-manager -c manager
```

## Hardening checklist

1. Create each user with a long, unique password and give people the `viewer` role unless they need to change things. Remove
   users who leave, and revoke the tokens they approved or created. See [Users and API tokens](/docs/guides/users-and-tokens/).
2. Publish the dashboard only over HTTPS. Give the installer a real contact address (`GEASS_ACME_EMAIL`) so Geass creates the
   `letsencrypt-prod` ClusterIssuer, or use a Cloudflare Tunnel, and do not expose port 8085.
3. Put the dashboard behind something that rate limits sign-in, or restrict it to your address range.
4. Use project tokens in CI, give every token an expiration, store it in your CI's secret store, and revoke tokens you no longer use.
   Do not put an admin workspace token in CI. Give monitoring scripts and AI agents an `observer` token, one for each, and never an
   admin token unless the agent has to change things.
5. Give AWS a dedicated IAM user with the policy from [Cloud connections](/docs/guides/connections/), and rotate the key.
   Scope the Cloudflare token to one zone and install the GitHub App on only the repositories you deploy.
6. Restrict 6443, 10250 and the overlay port to your node addresses in your provider's firewall. Open 80 and 443 to the
   internet only if you publish services (certificates need 80), and limit 22 to the addresses you use.
7. Keep project network isolation on (`Project`, the default), and open a project up only on purpose. In-cluster database passwords are predictable,
   so isolation is their main protection.
8. Turn on K3s secrets encryption, and encrypt the control plane's disk and any etcd snapshots, because they contain every
   Secret.
9. Only build repositories you trust, and join only servers you trust.
10. Set up database backups (see [Backups and restore](/docs/guides/backups-and-restore/)) and test a restore.
11. Approve a device code only when it matches the one on your own terminal, and choose the shortest expiry that works. Protect
    `~/.config/geass/config.json` on every machine where someone ran `geass login`.
12. Run `geass mcp --listen` only on `127.0.0.1`, or behind TLS and a firewall.
13. Put sensitive values in secrets, not plain configuration, because observer tokens and agents can read plain configuration.
14. Watch the operator log and your servers' logs.

## What stays your responsibility

Geass does not do these for you:

- **Operating system.** Patching, kernel updates, SSH configuration, disk encryption and time sync on every server.
- **K3s.** Geass does not upgrade or patch K3s on your servers. See [Upgrading](/docs/operations/upgrading/).
- **Network.** Provider firewalls and security groups, DNS, and DDoS protection.
- **TLS.** A real contact address for the Let's Encrypt account, port 80 reachable for validation, and the domain you point
  at the cluster. Geass creates the `letsencrypt-prod` ClusterIssuer from that address and cert-manager renews certificates,
  but if you run your own issuer or your own cert-manager (`spec.addons.certManager.enabled: false`), it is yours to keep working.
- **Control plane backups.** Geass backs up databases to S3 when you configure it. It does not back up the K3s datastore, or the Secrets
  and resources in `geass-system` (users, tokens, connections, projects). Back up the datastore yourself, for example with K3s
  etcd snapshots, and keep the snapshots as protected as the Secrets they contain. See [High availability](/docs/operations/high-availability/).
- **Your accounts.** Two-factor sign-in, key hygiene and audit logs on AWS, Cloudflare, PlanetScale and GitHub.
- **Your apps.** The images you run, their dependencies and their own authentication.
- **Detection and response.** Monitoring, alerting and incident handling. With the monitoring add-on on, Geass defines Prometheus alert
  rules for the operator's own health (rule `geass-operator` in `monitoring`: the operator down, failing or slow reconciles, a backed-up
  work queue, a slow or failing Kubernetes API server). It sets up no notification channel, and the default `lite` monitoring profile
  turns Alertmanager off. It has no alerting for your services.

## Related

- [Users and API tokens](/docs/guides/users-and-tokens/)
- [Command line](/docs/guides/command-line/) and [MCP server](/docs/guides/mcp-server/)
- [REST API](/docs/reference/rest-api/)
- [Cloud connections](/docs/guides/connections/)
- [Network isolation](/docs/guides/network-isolation/)
- [Compliance tags](/docs/guides/compliance-tags/)
- [Domains and TLS](/docs/guides/domains-and-tls/)
- [Upgrading](/docs/operations/upgrading/)
- [Troubleshooting](/docs/operations/troubleshooting/)
