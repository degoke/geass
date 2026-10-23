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
| A deploy hook URL | A 160-bit secret in the path that deploys one service and does nothing else; refused as an API credential |
| Writes from a browser | Same-origin check on every mutation (the request's `Origin` or `Referer` must match the dashboard host) |
| Writes from scripts | API tokens sent in a header, stored only as hashes |
| Command line and agents | A device code that a signed-in admin approves in the dashboard, then an ordinary API token; the MCP server acts as that token |
| Project to project traffic | A network policy per project environment (on by default) |
| Where a project's data runs | Compliance tags: node selectors on everything the project runs, and an admission policy as a backstop |
| Cloud metadata service | A network policy that blocks `169.254.0.0/16` from every project namespace |
| The cluster's machines | A network policy that blocks every node address, with the Kubernetes API left open on the control plane. On for projects created in the dashboard or the API; a choice for older projects |
| One project using all the room | Optional per project: a quota on the CPU, memory, disk and pods of the project, of each environment, or both |
| Metrics and log stores | Network policies that admit only platform namespaces |
| Object storage server | A network policy, `geass-s3`, that opens the S3 port to every namespace, opens Garage's admin port to the platform only, and closes every other port |
| HTTPS certificates | cert-manager and a Let's Encrypt `ClusterIssuer` that Geass creates once you give it a contact email |
| Secrets | Kubernetes Secrets, encrypted at rest by K3s on clusters the installer builds |
| Outbound webhooks | Signed with a per-webhook secret; private and loopback addresses refused |
| GitHub webhooks into Geass | HMAC-SHA256 signature check |
| Geass reporting to GitHub | The GitHub App's short-lived installation token, with write access only to commit statuses, deployments and pull requests; one switch per project turns it off |
| Machines Geass creates at a cloud | A connection that belongs to the cluster and that only an admin can use, credentials limited to a project or account of their own, a cap on the nodes of each pool, an SSH key and a host key that Geass makes (the host key is checked before the join token is sent, and a node waits if it cannot be), a firewall it makes at the cloud that is never opened wider when the control plane's addresses are unknown, and a node's connection, region and pool that cannot be edited after it is made |
| Servers you bring | Your OS, your firewall, SSH keys you supply |

## Authentication

### Dashboard users

Users come from the Secret `geass-dashboard-auth` in `geass-system`. Passwords are stored as bcrypt hashes: a plain
password you write into the Secret is replaced by its hash the first time Geass reads it. Geass refuses the sample
password `CHANGE_ME`, and it does not enforce a minimum length. Creating and changing users, and the two roles, are in
[Users and API tokens](/docs/guides/users-and-tokens/).

A successful sign-in sets the cookie `__Host-geass_session`. It is HTTP only, `SameSite=Lax`, valid for 12 hours, and signed
with an HMAC key kept in the same Secret (`session-key`). The cookie carries the username, role, an expiry and an epoch;
Geass checks the user's current role and epoch on every request. Signing out bumps the epoch, which ends every session of
that user. The cookie is marked `Secure`, except when the request is plain HTTP to a loopback address, where it is `geass_session`.
The `__Host-` prefix is what keeps a sibling subdomain from planting a cookie: a browser sets such a cookie only for the exact
host that sent it, over https, with `Path=/` and no `Domain`, and refuses a page of another host the same name. Tenants' services
run on subdomains of the dashboard's domain, so without it one could set a session cookie, or the sign-in state cookie, for the whole domain.
A cookie under the plain name is ignored where Geass sets the prefixed one.

Five failed sign-ins for one username in a minute lock that username for a minute, and the reply is the same while
locked. The counter lives in the Secret, so it survives restarts. Only usernames that are configured are recorded: a
name that is not one of your users is counted against nothing but the client address below, so someone typing random
names at the form cannot make the Secret grow. A record whose minute is over and whose lock has lifted is dropped the
next time the Secret is written, and the Secret never keeps more than 256 records.

A sign-in for a name that is not configured is checked against a dummy password hash, so it takes as long as one for a
real user and the time of the reply does not reveal which names exist.

Five failures for a username from one client address lock that username for that address, so a client that keeps guessing is shut out
and the owner of the account, signing in from elsewhere, is not (an attacker can no longer keep a known username locked for everyone by
failing from one address). Twenty-five failures for it from all clients together lock it for everyone, which is the backstop for many
clients guessing at once. On top of both, a client address that causes 20 failed sign-ins in a minute, whatever username it tries, is
refused for a minute. The counts for a username and an address, and for an address, are kept in memory (they start over when the operator
restarts), and an IPv6 client counts as its `/64`, so a client that rotates through prefixes gets a fresh allowance for each.

Geass takes a client's address from the connection when it comes from a public address. When it comes from a private one, as everything
does through the cluster's ingress or a Cloudflare Tunnel, Geass reads `X-Forwarded-For` from the right and uses the right-most address
that is public: the nearest proxy wrote it, and what a client put before it is not believed. When that cannot be told (no header, or an entry that cannot be read), the visitors cannot be told apart, and
five failures lock the username for everyone, as before. Anything inside the cluster can write the header and choose its address; that
gets it another client's allowance and never more than 25 failures a minute for a username in all. A load balancer that does not
add the address of what connected to it does not help: put one in front that does, or a rate limit that tells visitors apart.

Open streams and terminals are checked again about every 15 seconds. The stream of changes, the logs streams and terminals are checked
against their credentials when they are opened, and then again while they stay open: a person who signed out (or was signed out by the
identity provider), a token that was revoked and a user who was demoted lose what they had open within about 20 seconds (the checks of all the open streams share one view of the users that is a few seconds old, so that a few dozen open tabs do not each read the Secret). A terminal says
`Your session ended` before it closes, and its audit record says the session ended, not that the terminal was closed. When the check cannot be made (the API server did not answer), the stream stays open until the next one.

There is no two-factor authentication of its own. To get it, with invitations and sign-in by email, connect an identity
provider (see below). Use long unique passwords and restrict who can reach the dashboard.

### Signing in through an identity provider

With `spec.auth.mode` set to `external`, an OpenID Connect provider decides who signs in, and Geass only checks its answer.
Setting it up and its limits are in [Sign in with an identity provider](/docs/guides/external-sign-in/). What protects it:

- **The sign-in is bound to the browser that began it.** Geass starts with a random state, a nonce and a PKCE verifier (S256),
  keeps them in a short-lived (ten minutes), signed `__Host-` cookie (plain over http on this machine), and checks them when the
  browser comes back. A callback without that cookie, with another sign-in's cookie, with a changed state, or with a state that already came
  back (the operator remembers the ones used) is refused before the provider is asked anything, and so is an answer whose `iss`
  is not the configured issuer or that has no code. The address the provider is told to send the browser back to is the dashboard's
  published address once its domain is verified, not the Host header of the request, so a provider with a permissive redirect list cannot
  be made to deliver a code to a host an attacker chose. The page a person lands on afterwards is only ever a path on the dashboard.
- **The ID token is verified, not trusted.** Its signature (with the provider's published keys, fetched again when a token names a
  key that is not known, at most once every 15 seconds, so forged tokens cannot make the operator call the provider over and over, and
  again after six hours), issuer, audience, expiry and nonce are checked. Only public-key algorithms are accepted: never a shared-secret
  algorithm, which anyone holding the public key could forge, and never an unsigned token. The token is taken from the provider's
  token endpoint over TLS, not from the browser.
- **A role is the one the provider gave.** The role claim is mapped through the list an admin set, and a person whose roles map to
  nothing, or who lacks a required claim (compared whole, never split at commas), or whose email the provider does not say is
  verified (`true`, or the text `true`), or whose token names no subject, cannot sign in. Without a required claim, everyone the
  provider signs in with a mapped role gets in: that is the setting to restrict when anyone can sign up at the provider. The role is
  fixed in the signed session cookie at sign-in, so a change at the provider applies when the session ends (an hour by
  default, 5 to 720 minutes).
- **Only a signed-in admin can change it, never a token.** The settings, the test and the switch are refused for every API
  token, observers included. Turning it on needs a test sign-in of exactly the settings as saved, by someone who maps to admin,
  in the last half hour, and an admin who has a password. Saving is refused while people sign in through the provider.
- **The way in that does not depend on the provider stays.** A user in the Secret who is an admin and has a password can still
  sign in. While the provider decides, the password of a user who is not an admin is refused, and turning the provider on in the
  dashboard ends their open sessions (a switch made by editing the resource by hand does not, so those end when they expire).
- **The operator does not become a probe.** The issuer, and every address its discovery document lists, has to be `https`. The
  operator does not connect to this machine, link-local addresses (where the cloud's metadata service is), the metadata
  addresses of the clouds that put it elsewhere, or an IPv4 address written as NAT64, checked when the issuer is saved and again at
  every connection, so a redirect to one fails too. It reads at most 1 MiB of a response and gives up after ten seconds. Private
  network addresses are allowed, which includes addresses inside the cluster: that is why only a signed-in admin can set the address. `GEASS_ALLOW_LOOPBACK=true`, an operator setting no admin or token can
  change, lets a developer use a provider on their own machine, and lets through loopback only.
- **The client secret is not shown, and is bound to its provider.** It lives in the Secret `platform-auth`, and a change of
  issuer or client ID needs it entered again.
- **Leaving.** Signing out ends every session a person has, in every browser. The provider can end them too with a back-channel
  logout, which Geass accepts only when it is signed by the provider and recent, and acts on once. It also revokes the tokens and
  deploy hooks that person made, unless an admin turned that off (`revokeTokensOnLogout`, on by default) because CI depends on tokens
  made from the accounts of people who sign out. Without it, or with it off, a token outlives their access until it expires or is revoked.

### Organizations

With `spec.auth.tenancy.enabled`, one Geass serves several organizations that must not see each other's work. The setup is in
[Organizations and workspaces](/docs/guides/organizations-and-workspaces/). What protects it, and where it stops:

- **One check, not many.** The dashboard reads and writes the cluster through a single client that limits every call to the
  caller's organization (and workspace). A project, service, database, bucket, build, deployment, terminal session or connection of
  another organization is answered as one that does not exist, so a name or a count never tells that it is there. A kind the client
  has not been told about, a call that names no tenant, and a patch whose effect cannot be read from the object are refused,
  not allowed: forgetting something fails closed. The platform's own objects (the cluster, nodes, settings and Secrets) are not
  an organization's to read; the code that needs them for it asks for the dashboard's own access, after it has shown that what is
  asked about is the caller's.
- **Platform and organization routes are different.** A route nobody classified is the platform's alone. Settings, nodes, the
  domain, the platform's registry, sign-in, the platform's alerts, the platform's shared servers and the cluster's object store are refused to an
  organization's administrators with `403`. They are not an organization's even when they hold an admin role. What an organization has of
  its own (alert channels, a registry, shared database servers and cloud connections made for it, and, if the platform gives it one, a
  provider to sign in through) are organization routes, each checked against the organization that owns them.
- **Where someone works is signed.** The organization, the workspace and what they may switch to are in the session cookie, signed
  with the same key as before, so a member cannot edit their cookie into another organization or into an administrator. A switch
  re-issues the cookie with the same end and start, so it neither extends a session nor lets it outlive a sign-out. The organization
  is also looked up on every request: suspending or deleting it, or removing a workspace, stops open sessions at once.
- **Streams end with the place.** The overview stream, log streams and terminals are told only what happens in the caller's projects
  and are checked again every 15 seconds against where the person works.
- **Tokens belong to where they were made.** A token an organization's administrator makes acts for that organization alone, and
  organizations list and revoke only their own. A token made before organizations existed is install-wide. A project's token takes
  its project's organization and reaches nothing while the project has none. An organization may hold 100 tokens, so one cannot use
  up the rest.
- **Policy the platform sets.** An organization may be limited in how many projects it has and how much they use together, kept to a region
  and to nodes carrying tags, and kept to some domains for hostnames. A person cannot change any of it for their own organization.
- **Builds are apart.** A project of an organization builds in a namespace of the organization's own, with copies of only the credentials that
  build needs, no connection allowed to the build and no private address reachable from it, and removes them when the build ends.
- **A person is checked again.** With the check on, the provider is asked every few minutes whether each signed-in person is still its own and
  says the same of them; a refusal or a change ends their sessions. The refresh tokens it needs are kept sealed in a Secret, never shown.
- **An organization's own provider cannot reach beyond the organization.** What it says makes a role in that organization and nothing else:
  it cannot make anyone an administrator of the platform, give them another organization, or outlive a change to it.
- **Nothing is created before the caller is shown to own it.** A request that names a project is checked against it first, and
  an organization cannot take a Secret that already exists (a cloud connection's credentials, say).
- **Outside the API.** Services cannot take a hostname another service serves, or the dashboard's. Geass does not overwrite a DNS record in
  your Cloudflare zone that it did not create. With organizations on, a GitHub push or pull request event must name the installation of
  the project it is for, and an installation another organization's project uses cannot be connected to a project of yours; a person also has to show
  that GitHub lists the installation among the ones their own account can use.

What it does **not** isolate, and why you should give organizations that you do not trust their own cluster: their pods share your
machines and kernel (reserve nodes for an organization with required node tags to separate them); builds still run on those machines, and
use the platform's registry login unless the organization has its own; shared database servers and the object store are cluster-wide
(a login keeps each database apart); the operator and the dashboard hold cluster-wide access, so a flaw in either's code is a flaw in the
separation; there is one GitHub App, which anyone who can install it on an account they own can connect; and a person removed from an
organization at the provider keeps working in it until their session ends (an hour by default), unless the provider sends a back-channel
logout or the platform checks people again.

### API tokens

Tokens have a prefix (`gsw_` for a workspace token, `gsp_` for a project token, `gsh_` for a deploy hook), a public identifier and a 160-bit random
secret. Geass stores a SHA-256 hash of the secret in the Secret `geass-api-tokens`, compares in constant time, and shows
the token once. Workspace tokens are `admin`, `observer` or `viewer`. A project token can act on one environment of one project, through
a fixed list of endpoints, and cannot create resources, open terminals or change settings. Tokens can expire (up to ten
years, or never) and can be revoked at once. Only a signed-in admin can create, list or revoke tokens, or approve a device code. A
token is not tied to the account of the admin who created it, so removing that user does not revoke it. The exception is a person who
signed in through an [identity provider](/docs/guides/external-sign-in/): what they make is tied to who they are there, and is
revoked when the provider signs them out.

A deploy hook's secret lives in a URL, where proxies and logs can see it, so it is built to do little: it deploys one service, is
refused as a Bearer token everywhere, answers only `POST`, and looks the same as a missing hook when it is wrong, revoked or expired.
Anyone who has the URL can deploy that service, so treat it like a token, and delete the hook if it may have leaked. A hook answers once
every 30 seconds, so a leaked URL in a loop cannot keep builds from finishing. The operator's own request log redacts the path, but a reverse
proxy in front of it may not.

### Roles

A viewer sees a redacted copy of the platform: no variable values, hostnames in the spec, repository names, backup
storage, project webhooks or database host details, and no logs, terminals or secrets. The one exception is the clouds that create machines: a viewer sees each connection's name, cloud and whether it works, and the pools and their nodes, but not the cloud account or what the cloud said (an admin reads that), and never a credential. A viewer cannot change anything.
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

An agent with an admin token can also run [node pools](/docs/guides/node-pools/): it can read them and the clouds' connections, list a cloud's sizes,
and create, change and resume a pool (and delete one with `--allow-delete`). A pool creates machines that cost money. So the tools stop short of the
two decisions a person keeps: there is no tool that takes a credential, makes, changes, verifies or removes a connection, or adds or removes a
single server, and a pool cannot be made without `max_nodes`, its cap. The tools also have ceilings of their own: an agent may set `max_nodes` to at most 10, and
`min_nodes` (made at once) and `spare_nodes` to at most 3, where a person can set up to 200 in the dashboard or with `geass pools`. An agent cannot be
handed a secret it could leak, and it cannot choose which of your accounts to spend. It can still spend up to the cap of every pool it makes, on a
connection that already exists, so give an agent an admin token only when you accept that. See [MCP tools](/docs/reference/mcp-tools/#node-pools-and-clouds).

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
| `/webhooks/github` | GitHub cannot sign in; every request needs a valid HMAC signature. The delivery ID GitHub sends is logged and echoed in the answer, and never used to skip a delivery |
| `/assets/*` and other page loads | The dashboard's static files. They hold no data |

Everything under `/api/` other than those needs a session or a token. The unauthenticated surface is small, but it is on
the public internet once you publish the dashboard.

### Ports on your servers

| Port | Used for | Who should reach it |
| --- | --- | --- |
| 6443/tcp | Kubernetes API | Your other nodes, and your own address if you run `kubectl` remotely |
| 2379/tcp, 2380/tcp | etcd, the cluster's database, between control plane servers. Only needed with [more than one control plane server](/docs/operations/high-availability/#run-three-control-plane-servers) | The control plane servers, and nothing else |
| 10250/tcp | kubelet | Your other nodes |
| 8472/udp | Overlay network (VXLAN) | Your other nodes |
| 51820/udp | Overlay network (WireGuard, multi-provider clusters only) | Your other nodes |
| 80/tcp, 443/tcp | Apps and the dashboard through Traefik. Let's Encrypt validates hostnames on 80 | The internet, if you publish services |
| 22/tcp | SSH, used to join servers and for your own access | You, and the control plane toward workers. On a machine Geass creates, only the control plane's addresses |

The installer opens 6443, 10250, the overlay port, 80 and 443 in the host firewall of the control plane if it finds an
active one (`ufw`, `firewalld` or `iptables`), without limiting the source address. An `iptables` rule goes behind your own
rules and ahead of a catch-all `REJECT` or `DROP` at the end of the `INPUT` chain, so a block of yours still wins. Restrict 6443 and 10250 to
your node addresses in your provider's firewall or security group. The operator also serves health probes on 8086 and, in the installed manifest, metrics on 8443 (HTTPS,
protected by Kubernetes authentication and authorization); both stay inside the cluster. The operator's manifest includes a
ClusterRoleBinding (`metrics-reader-prometheus` in `config/rbac`) that gives the monitoring add-on's Prometheus service account
(`geass-kube-prometheus-stac-prometheus` in `monitoring`) the `metrics-reader` role, which is what lets Prometheus scrape the operator's
metrics. It binds nobody on a cluster without that add-on.

For a machine Geass creates at a cloud, the firewall Geass makes there lets in SSH and the kubelet port (10250/tcp) only from the addresses of the control plane's nodes, and, in a multi-site cluster, the overlay's UDP port from anywhere (every packet is authenticated). With no control plane address known it lets SSH in from anywhere, which is safe only because the machine accepts nothing but the cluster's key. At Oracle Cloud and OVHcloud it makes no firewall, so those two are open as the project's network leaves them. See [Cloud connections for machines](/docs/guides/connectors/clouds/#what-geass-makes-at-the-cloud). The control plane's own 6443/tcp must be reachable from the new machines. With [`controlPlaneFirewall`](/docs/guides/node-pools/#let-geass-open-the-control-plane) Geass edits the control plane's AWS security group or Hetzner firewall to allow each machine's address, marks the rules it adds (`geass` on AWS, `geass-access` on Hetzner), never changes a rule it did not add, and removes its rules when the machine goes. The connection it uses for that needs the right to change that one firewall.

## Secrets

### Where they live

Everything secret is a Kubernetes Secret: dashboard users and the session key, API token hashes, cloud connection keys,
the Cloudflare token, the GitHub App key, app secrets and shared secrets, function schedule tokens, database passwords, the K3s join token
(`geass-token`), SSH keys, the credentials of the clouds that create machines, and the key and host keys of those machines
(`geass-nodepool-ssh`, `<node>-hostkey`). [Cloud connectors](/docs/guides/connectors/) lists the names.

- **At rest.** The installer turns on K3s secrets encryption (`secrets-encryption: true`, AES-CBC) on the clusters it builds,
  so Secrets are encrypted in K3s's datastore and in its etcd snapshots, including the one the installer takes before an
  upgrade. It does not keep a Secret from anyone the API lets read it, because the API server decrypts it for them, or from
  root on a control plane server. K3s keeps the key in `/var/lib/rancher/k3s/server/cred/` on that server's disk, so a copy of
  the whole disk gives up every Secret. A cluster the installer did not build, such as K3s you installed by hand, has no
  encryption unless you turned it on in K3s yourself.
  The copies of the cluster that you can ask K3s to send to an S3 bucket ([Disaster recovery](/docs/operations/disaster-recovery/))
  hold every Secret too, and the key that decrypts them, protected only by the cluster's server token: keep that bucket to
  itself, with encryption on, public access blocked and a key that can use only it.
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
The password of an in-cluster database that Geass creates is random, and the MySQL root password is the same value. A database made by an earlier release
keeps the password it was started with, which Geass derived from the database's resource name (`<name>-password`). Anyone who knows the name of such a
database and can reach its port has its password. What protects these databases is [network isolation](/docs/guides/network-isolation/) and not publishing
their ports. Do not turn isolation off for a project that has databases, and see [GeassDatabase](/docs/reference/resources/geass-database/#connection-secret).
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
| Nodes | get, list, watch, patch, delete (a node is cordoned and removed when a pool or a server is removed) |
| Pods, PersistentVolumes | get, list, watch |
| `pods/exec` | create (used by the terminal and the SQL console) |
| `pods/eviction` | create (used to empty a node before it is removed, so that disruption budgets are honoured) |
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
| ValidatingAdmissionPolicies, MutatingAdmissionPolicies and their bindings (`admissionregistration.k8s.io`) | create, get, list, watch, update, patch, delete |
| MariaDB operator `MariaDB` and `PhysicalBackup` objects (`k8s.mariadb.com`) | create, get, list, watch, update, patch, delete |
| CloudNativePG clusters, backups and scheduled backups | create, get, list, watch, update, patch, delete |
| Longhorn nodes and volumes | get, list, patch |
| Geass resources | create, get, list, watch, update, patch, delete |
| Geass resource `status` and `finalizers` | get, update, patch for `status`; update for `finalizers` |

It has no rule that grants permission to change RBAC objects. It can read and write every Secret and run commands in every
pod in the cluster, so treat anyone who controls the operator, its image or its service account as a cluster administrator.
Geass also creates HelmChart resources, which K3s's built-in Helm controller installs for its add-ons (cert-manager, monitoring,
Longhorn, logging, the scale-to-zero add-on and some database engines), so review the add-ons you enable. The admission policy
rights are used for compliance tags (see [Keeping a project's data on chosen nodes](#keeping-a-projects-data-on-chosen-nodes)): the
operator creates two policies and their bindings, which apply only to namespaces it manages.

The other Geass roles in the cluster are the leader-election Role in `geass-system`, a metrics authentication role, and
admin, editor and viewer ClusterRoles for apps, databases, object stores, clusters and nodes that you can bind to people if
you give them `kubectl` access.

Workloads you deploy get no special service account. Their pods mount the default service account token of their
namespace, except function pods, which do not mount one. Geass sets no security context on your pods, so a container runs as
the user its image says, including root.

## Network isolation

Without network policies every pod in a Kubernetes cluster can reach every other pod. Geass applies these on its own:

- **Project isolation.** Every project environment namespace gets the policy `geass-project-isolation`: only the project's own
  namespaces, the projects you list, and the platform namespaces (`kube-system`, `geass-system`, `monitoring`, `cnpg-system`, `moco-system`, `mariadb-system` and
  `keda`, where the scale-to-zero proxy runs) can reach its pods. This is the default (`isolation: Project`). A project that sets `isolation: None` is open to
  every pod in the cluster. Details are in [Network isolation](/docs/guides/network-isolation/).
- **Cloud metadata.** Every project namespace gets `geass-block-cloud-metadata`, which lets pods reach everything except
  `169.254.0.0/16`, so a pod cannot read the instance credentials of the machine it runs on. It applies whether or not the
  project is isolated.
- **The cluster's machines.** A project can switch on `blockNodeAddresses`, which adds every node's addresses to that same
  policy's exceptions, so a pod cannot open a connection to a node's kubelet or the metrics of the host. The Kubernetes API
  stays open on the control plane, because Postgres databases use it. It is on for new projects, off for projects that existed
  before it, and Geass keeps it in step with the nodes. See [Network isolation](/docs/guides/network-isolation/#keep-services-away-from-the-clusters-machines).
- **Project limits.** A project can cap what it asks for as a whole and what each of its environments asks for. It is off by default. See
  [Project limits](/docs/guides/project-limits/).
- **A check that policies are enforced.** All of this depends on the network plugin acting on policies. Geass tries it, and the **Usage** page says **Not enforced** when it does not. See [Network isolation](/docs/guides/network-isolation/#check-that-the-cluster-enforces-policies).
- **Telemetry.** The Prometheus and log stores have no login and hold every project's data, so Geass limits them to the
  platform namespaces with `geass-restrict-metrics-store` and `geass-restrict-log-store`.
- **Object storage.** The cluster's object storage server gets the policy `geass-s3` in `geass-system`. Its S3 port (8333 for
  SeaweedFS, 3900 for Garage) is open to every namespace, because that is how apps reach their buckets, and each project's login
  limits it to its own bucket. Garage's admin port, 3903, is open only to `geass-system`, where the operator uses it to make buckets
  and keys. Every other port is closed. The server also closes those ports itself, so the policy is a second lock: SeaweedFS
  listens on localhost with a forwarder container publishing S3, and Garage's other ports are closed. Removing the server removes
  the policy.

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
- A second policy, `geass-required-node-tags-defaults`, adds the node selector and toleration a pod lacks, so that pods which operators Geass
  runs create themselves (the MariaDB operator's copies, for one) land on the tagged nodes instead of being refused. It only ever narrows
  where a pod may go, never fails a pod it cannot handle, and needs Kubernetes 1.36 or later.
- With Longhorn, the copies of a project's database volumes are kept on the tagged nodes (the operator makes a storage class for each
  combination of region and required tags), and other projects' volume copies are kept off compliance nodes. The same holds for the
  object storage server's own volume, which keeps its copies on the nodes carrying the tags it was set up with.
- The logs of the project are not shipped to the shared log store, so their history is not kept and the logs stay on the nodes. Live logs
  still work.
- A project with databases cannot take on a tag its databases are not on, and a shared database server or the cluster object storage server that
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
`GEASS_WEBHOOKS_ALLOW_PRIVATE_NETWORKS=true` on the operator. The address is checked when the connection is made, which stops DNS tricks. Deliveries use no proxy and follow no redirects. [Alert](/docs/guides/alerts/) channels are different: Alertmanager, in `monitoring`, connects to Slack and to the mail server, so that guard does not apply to it. Geass refuses a host that is, or resolves to, a loopback, link-local or (unless allowed) private address when the channel is saved, and cannot see a name that resolves differently later. Their secrets (the Slack URLs, the mail password) are in the Secrets `geass-alert-channels` and `geass-alert-smtp`, and only a signed-in admin, never a token, can see or change the settings.

## Credentials your connections hold

| Connection | What the credential can do | How to limit it |
| --- | --- | --- |
| AWS | Create and delete S3 buckets, and create and delete IAM users named `geass-*` | Use the policy in [Connect AWS](/docs/guides/connectors/aws/#permissions-the-aws-key-needs); restrict S3 resources to a name prefix |
| PlanetScale | Create databases, branch passwords and Postgres roles in the organization, delete the logins Geass made, and read cluster sizes | Use a token limited to what Geass needs |
| Cloudflare | Edit DNS in the zone, edit tunnels in the account | Scope the token to one zone |
| A cloud for machines | Create, list and delete machines in the project, resource group, compartment or account it is limited to, with the disks, addresses and firewalls they need, and so spend whatever those machines cost. Each cloud's permissions are on its page | Give it a project or account of its own, exactly the permissions on [its page](/docs/guides/connectors/clouds/), and a budget alert at the cloud. A pool's `maxNodes` caps how many machines Geass makes, not what your account may be charged otherwise |
| GitHub App | Read contents, metadata and checks of the repositories where it is installed. Write commit statuses, deployments and pull requests (comments) there. It cannot push code or merge a pull request, which need Contents write | Install it only on the repositories you deploy, and turn **Report to GitHub** off for a project that should not be written to |

**What Geass writes to GitHub.** Geass reports each build as a commit status (`geass/build/<service>`), each deploy as a GitHub
Deployment, and each pull request environment as one comment on the pull request. What it writes is a state, a short
reason such as `kaniko Job failed`, a service's public address and a link to the dashboard. Everyone who can see the
repository can see them, so the address of a service built from a private repository is shown to that repository's
readers, and to nobody else. The write permissions are needed for this and for nothing else. **Pull requests** write is the narrowest permission GitHub offers
for commenting, and it also allows editing a pull request's title, body and reviews, so Geass's installation token can do
that too, though Geass does not. The permissions are all or nothing per app: an app registered before Geass reported to
GitHub does not have them, and the owner has to approve them in GitHub. Until they do, the reports are refused and
dropped, and the connection's `GitHubReporting` condition says so. See
[Reporting back to GitHub](/docs/guides/github-and-builds/#reporting-back-to-github). The setting is `spec.githubReporting`
on the project, and viewers do not see it.

AWS and PlanetScale connections belong to one project and are only usable by it. The Cloudflare and GitHub credentials belong
to the whole cluster. A connection for machines belongs to the cluster too, but a project can never use it: Geass refuses a pool or
a server that names a connection scoped to a project, and only an admin can make, change or use one. An AWS connection that holds
buckets never creates machines, because it does not have the purpose Compute: make a separate connection with its own key. Per-bucket
AWS keys given to apps reach only their own bucket.

## Machines Geass creates

For a machine it creates, Geass does not depend on a key you made, or on trusting an address:

- **One SSH key for the cluster.** Geass makes it the first time it needs one, and keeps it in the Secret `geass-nodepool-ssh` in `geass-system`. Only the **public** half goes onto a machine, in its cloud-init. Anyone who can read Secrets in `geass-system` can read the private half and log in to every machine a pool made.
- **A host key for each machine.** Geass makes it, puts it on the machine in the same cloud-init, and keeps it in the Secret `<node>-hostkey`. It checks the machine's host key against it before it sends anything, so the join token can reach nothing but that machine. This is the pinning that is optional for a server you bring, done for you. It is a key that identifies one machine, so its private half in the machine's user data is not a cluster secret.
- **No join token in user data.** A machine's user data can be read from inside the machine and from the cloud's own API. Geass puts only the public key, the host key and the hostname there, and sends the join token over SSH when it installs K3s. As for any worker, the token is in the install command and visible to local users on the new machine while it runs.
- **A login of its own.** The cloud-init creates the user `geass` with passwordless `sudo`, and turns password login off.
- **Deleting is limited to what Geass can name.** It deletes a machine only by the ID the cloud gave it that it recorded for a node, and lists machines only by labels that carry this cluster's identity, so a second Geass cluster in the same account is never touched. Deleting a machine that no node owns is **opt-in**: a pool does it only when you set `orphanPolicy: Delete`, and then only for a machine unowned for 20 minutes. The default is to report it.
- **Money is capped by the pool, and by you.** A pool cannot be made without `maxNodes`, and three machines in a row that fail to join pause it. These bound how much Geass can spend through a connection. They do not bound what the connection's credentials could be used for by someone who has them: that is what the project or account boundary is for.

## SSH and joining servers

Geass joins a server by connecting over SSH as root or a sudoer, with a private key in a Secret you reference. It does not
verify the server's SSH host key unless you pin it, so by default it cannot tell if the address now points at a different
machine. Put the machine's host key in the same Secret under `known_hosts` (see [Add servers](/docs/getting-started/add-servers/#pin-the-servers-host-key-recommended)) and
Geass refuses any other machine before it sends a command or the join token. Without it, join only servers whose network
path you trust. Use a key made for this purpose, and prefer a private network. The K3s join token in `geass-token` lets
any machine that can reach port 6443 join the cluster, so guard it like a password. A machine Geass creates is different: see
[Machines Geass creates](#machines-geass-creates).

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
5. Give AWS a dedicated IAM user with the policy from [Connect AWS](/docs/guides/connectors/aws/#permissions-the-aws-key-needs), and rotate the key.
   Scope the Cloudflare token to one zone and install the GitHub App on only the repositories you deploy.
   Give a cloud that creates machines a project or account of its own, exactly the permissions on its page in
   [Cloud connections for machines](/docs/guides/connectors/clouds/), and a budget alert at the cloud. Set a pool's `maxNodes` to a number you
   could pay for, and leave `orphanPolicy` on `Report` until you have checked what it would delete.
6. Restrict 6443, 10250 and the overlay port to your node addresses in your provider's firewall. Open 80 and 443 to the
   internet only if you publish services (certificates need 80), and limit 22 to the addresses you use.
7. Keep project network isolation on (`Project`, the default), and open a project up only on purpose. In-cluster databases made by an earlier release have a
   password that can be guessed from their name, so isolation is their main protection.
8. Encrypt the control plane's disk, and keep the server token (`/var/lib/rancher/k3s/server/token`) and your etcd snapshots
   somewhere outside the cluster and protected. The installer turns on K3s secrets encryption, but K3s keeps the key on the
   server's disk, and a restore needs the token (see [Go back](/docs/operations/upgrading/#go-back)). If you run K3s you
   installed by hand, turn on `secrets-encryption` in it.
9. Only build repositories you trust, and join only servers you trust.
10. Set up database backups (see [Backups and restore](/docs/guides/backups-and-restore/)) and test a restore.
11. Approve a device code only when it matches the one on your own terminal, and choose the shortest expiry that works. Protect
    `~/.config/geass/config.json` on every machine where someone ran `geass login`.
12. Run `geass mcp --listen` only on `127.0.0.1`, or behind TLS and a firewall.
13. Put sensitive values in secrets, not plain configuration, because observer tokens and agents can read plain configuration.
14. Watch the operator log and your servers' logs.
15. If you run organizations, give them only to people and groups you trust to share machines, keep the `platformClaim` to the few who run the
    installation, keep an admin who signs in with a password, and shorten `sessionMinutes` if people are removed from organizations often.

## What stays your responsibility

Geass does not do these for you:

- **Operating system.** Patching, kernel updates, SSH configuration, disk encryption and time sync on every server.
- **K3s.** Geass upgrades K3s only when you ask it to, with `--k3s-version` (the control plane) and `spec.k3s.version` (the workers). It does not patch K3s by itself, and it does not watch for K3s security releases: that is yours. See [Upgrading](/docs/operations/upgrading/#update-k3s). Each update leaves an etcd snapshot, which holds every Secret in the cluster, in `/var/lib/rancher/k3s/server/db/snapshots`: protect it like the disk and delete old ones.
- **Network.** Provider firewalls and security groups, DNS, and DDoS protection.
- **TLS.** A real contact address for the Let's Encrypt account, port 80 reachable for validation, and the domain you point
  at the cluster. Geass creates the `letsencrypt-prod` ClusterIssuer from that address and cert-manager renews certificates,
  but if you run your own issuer or your own cert-manager (`spec.addons.certManager.enabled: false`), it is yours to keep working.
- **Control plane backups.** Geass backs up databases to S3 when you configure it. It does not back up the K3s datastore, or the Secrets
  and resources in `geass-system` (users, tokens, connections, projects). Back up the datastore yourself, for example with K3s
  etcd snapshots, and keep the snapshots as protected as the Secrets they contain. See [High availability](/docs/operations/high-availability/).
- **Your accounts.** Two-factor sign-in, key hygiene and audit logs on AWS, Cloudflare, PlanetScale and GitHub, and on every cloud you let Geass create machines at, including its bill. Geass caps the machines of a pool, but cannot see what else your account is charged.
- **Your apps.** The images you run, their dependencies and their own authentication.
- **Detection and response.** Monitoring, alerting and incident handling. With the monitoring add-on on, Geass defines Prometheus alert
  rules for the operator's own health (rule `geass-operator` in `monitoring`: the operator down, failing or slow reconciles, a backed-up
  work queue, a slow or failing Kubernetes API server). An admin can set up [alerts](/docs/guides/alerts/) to Slack or email, which Alertmanager
  sends: for failed builds and deploys, services that are down, failing backups, crashing pods, filling storage, nodes that are not ready and
  the operator's own alerts. Geass does not watch the traffic to your services, or what they log or return.

## Related

- [Users and API tokens](/docs/guides/users-and-tokens/)
- [Organizations and workspaces](/docs/guides/organizations-and-workspaces/)
- [Command line](/docs/guides/command-line/) and [MCP server](/docs/guides/mcp-server/)
- [REST API](/docs/reference/rest-api/)
- [Cloud connectors](/docs/guides/connectors/)
- [Cloud connections for machines](/docs/guides/connectors/clouds/) and [Node pools](/docs/guides/node-pools/)
- [Network isolation](/docs/guides/network-isolation/)
- [Compliance tags](/docs/guides/compliance-tags/)
- [Domains and TLS](/docs/guides/domains-and-tls/)
- [Upgrading](/docs/operations/upgrading/)
- [Troubleshooting](/docs/operations/troubleshooting/)
