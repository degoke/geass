---
title: "Security"
description: "How Geass authenticates users, tokens, the CLI, and agents, stores secrets, isolates projects, and what it exposes, with a hardening checklist."
---

This page describes how Geass protects what it manages, based on what the code does today. It also lists what you must
still do yourself. Geass runs on servers that you own, so most of the attack surface is yours:

- The operating system.
- The network in front of the servers.
- The K3s datastore.
- The accounts that you connect.

Geass holds no security certification, and no independent party has audited it. Do not read anything on this page as a
certification or an audit.

## The model in one table

| Surface | Protected by |
| --- | --- |
| Dashboard and REST API | Username and password (bcrypt), a signed session cookie, or an API token. Users are `admin` or `viewer`. Tokens can also be `observer` |
| Settings a platform keeps for itself | Off by default. [Managed mode](/docs/operations/managed-mode/), which the platform sets in `spec.managed`. The API refuses writes from every caller to sign-in, the domain and DNS of the dashboard, and the cluster-state backup. The dashboard shows these settings as read-only. A guard rail for the dashboard and its API, not a boundary against anyone who can write the objects of the cluster |
| A deploy hook URL | A 160-bit secret in the path. The URL deploys one service and does nothing else. Geass refuses it as an API credential |
| Writes from a browser | Same-origin check on every mutation. The `Origin` or `Referer` of the request must match the dashboard host |
| Writes from scripts | API tokens sent in a header, stored only as hashes |
| Command line and agents | A device code that a signed-in admin approves in the dashboard, then an ordinary API token. The MCP server acts as that token |
| Project to project traffic | A network policy for each project environment (on by default) |
| Where the data of a project runs | Compliance tags: node selectors on everything that the project runs, and an admission policy as a backstop |
| Cloud metadata service | A network policy that blocks `169.254.0.0/16` from every project namespace |
| Mail and other ports to the outside, for organizations | Off by default. The outbound limits of the platform, written into the same policy and into the policy of the build namespace of an organization. Mail ports closed, and optionally more ports, or all ports except a list. A port heuristic against mining pools, not detection. See [Outbound limits](/docs/guides/outbound-limits/) |
| How much mail an organization sends | Off by default. The mail relay: STARTTLS required before sign-in, and a login for each organization (random, compared as a digest in constant time, rotatable). For each organization, the relay counts rates, recipients, size, connections, and sender domains, in the memory of the relay. No content scanning, DKIM, or bounce handling. See [Mail relay](/docs/guides/mail-relay/) |
| The machines of the cluster | A network policy that blocks every node address, with the Kubernetes API left open on the control plane. On for projects created in the dashboard or the API. Optional for older projects |
| One project that uses all the room | Optional for each project: a quota on the CPU, memory, disk, and pods of the project, of each environment, or both |
| Metrics and log stores | Network policies that admit only platform namespaces |
| Object storage server | A network policy, `geass-s3`. It opens the S3 port to every namespace, opens the Garage admin port only to the platform, and closes every other port |
| HTTPS certificates | cert-manager and a Let's Encrypt `ClusterIssuer` that Geass creates after you give it a contact email |
| Secrets | Kubernetes Secrets. On clusters that the installer builds, K3s encrypts them at rest |
| Outbound webhooks | Signed with a secret for each webhook. Private and loopback addresses refused |
| GitHub webhooks into Geass | HMAC-SHA256 signature check |
| Geass reporting to GitHub | The short-lived installation token of the GitHub App, with write access only to commit statuses, deployments, and pull requests. One switch for each project turns it off |
| Machines Geass creates at a cloud | A connection that belongs to the cluster and that only an admin can use. Credentials limited to a separate project or account. A cap on the nodes of each pool. An SSH key and a host key that Geass makes. Geass checks the host key before it sends the join token, and a node waits if Geass cannot check the key. A firewall that Geass makes at the cloud, which Geass never opens wider when the addresses of the control plane are unknown. The connection, region, and pool of a node cannot be edited after Geass makes the node |
| Servers you bring | Your OS, your firewall, SSH keys that you supply |

## Authentication

### Dashboard users

Users come from the Secret `geass-dashboard-auth` in `geass-system`. Geass stores passwords as bcrypt hashes. If you write
a plain password into the Secret, Geass replaces it with its hash the first time that Geass reads it. Geass refuses the
sample password `CHANGE_ME`, and it does not enforce a minimum length.
[Users and API tokens](/docs/guides/users-and-tokens/) tells you how to create and change users, and describes the two roles.

A successful sign-in sets the cookie `__Host-geass_session`. The cookie has these properties:

- It is HTTP only and `SameSite=Lax`.
- It is valid for 12 hours.
- It is signed with an HMAC key that Geass keeps in the same Secret (`session-key`).
- It carries the username, the role, an expiry, and an epoch. Geass checks the current role and epoch of the user on every
  request. When a user signs out, Geass increments the epoch, and this ends every session of that user.
- It is marked `Secure`. The exception is a plain HTTP request to a loopback address. There, the cookie is `geass_session`.

The `__Host-` prefix stops a sibling subdomain from planting a cookie. A browser sets such a cookie only for the exact host
that sent it, over https, with `Path=/` and no `Domain`. The browser refuses a cookie with the same name from a page of a
different host. The services of tenants run on subdomains of the domain of the dashboard. Without the prefix, one of these
services could set a session cookie, or the sign-in state cookie, for the whole domain. Where Geass sets the prefixed
cookie, it ignores a cookie with the plain name.

Failed sign-ins for one username lock that username for a minute. The limits follow. While the username is locked, the
reply is the same. The counter is in the Secret, so it survives restarts. Geass records only usernames that are configured. Geass
counts a name that is not one of your users only against the client address (see below). Thus, someone who types random
names into the form cannot make the Secret grow. When the minute of a record is over and its lock has lifted, Geass drops
the record the next time that it writes the Secret. The Secret never keeps more than 256 records.

Geass checks a sign-in for a name that is not configured against a dummy password hash. Thus, the sign-in takes as long as
a sign-in for a real user, and the time of the reply does not show which names exist.

Geass uses these additional limits:

- Five failures for a username from one client address in a minute lock that username for that address for a minute. Thus, Geass shuts out a client
  that continues to guess, but not the owner of the account who signs in from a different location. An attacker can no
  longer keep a known username locked for everyone by failures from one address.
- Twenty-five failures for a username from all clients together in a minute lock it for everyone for a minute. This is the backstop for many clients
  that guess at the same time.
- In addition to both limits, Geass refuses a client address for a minute after it causes 20 failed sign-ins in a minute,
  for any username.

Geass keeps the counts for a username and an address, and for an address, in memory. They start again when the operator
restarts. An IPv6 client counts as its `/64`. Thus, a client that rotates through prefixes gets a new allowance for each
prefix.

Geass takes the address of a client from the connection when the connection comes from a public address. A connection
from a private address is different. All connections through the ingress of the cluster or a Cloudflare Tunnel come from
a private address. For these connections, Geass reads `X-Forwarded-For` from the right, and it uses the right-most address
that is public. The nearest proxy wrote that address, and Geass does not believe what a client put before it.

Sometimes Geass cannot find the client address, because there is no header, or because it cannot read an entry. Then Geass
cannot tell the visitors apart, and five failures lock the username for everyone, as before. Anything inside the cluster can
write the header and select its address. That gets it the allowance of a different client, and never more than 25 failures
a minute for a username in total. A load balancer that does not add the address of the client that connected to it does
not help. Put a load balancer in front that adds the address, or a rate limit that tells visitors apart.

Geass checks open streams and terminals again about every 15 seconds. Geass checks the stream of changes, the log streams,
and terminals against their credentials when they open. Geass then checks them again while they stay open. In these cases,
the person or token loses what they had open within about 20 seconds:

- A person who signed out, or whom the identity provider signed out.
- A token that was revoked.
- A user who was demoted.

The checks of all the open streams share one view of the users that is a few seconds old. Thus, a few dozen open tabs do
not each read the Secret. A terminal says `Your session ended` before it closes. Its audit record says that the session
ended, not that the terminal was closed. When Geass cannot make the check (the API server did not answer), the stream stays
open until the next check.

Geass has no two-factor authentication of its own. To get two-factor authentication, with invitations and sign-in by email,
connect an identity provider (see below). Use long unique passwords, and restrict who can reach the dashboard.

### Signing in through an identity provider

When `spec.auth.mode` is `external`, an OpenID Connect provider decides who signs in, and Geass only checks its answer.
[Sign in with an identity provider](/docs/guides/external-sign-in/) tells you how to set it up, and what its limits are.
These measures protect it:

- **The sign-in is bound to the browser that began it.** Geass starts with a random state, a nonce, and a PKCE verifier
  (S256). Geass keeps them in a short-lived (ten minutes), signed `__Host-` cookie (plain over http on this machine). Geass
  checks them when the browser comes back. Geass refuses these callbacks before it asks the provider anything:
  - A callback without that cookie.
  - A callback with the cookie of a different sign-in.
  - A callback with a changed state.
  - A callback with a state that already came back. The operator remembers the states that were used.
  - An answer whose `iss` is not the configured issuer, or that has no code.

  Geass tells the provider to send the browser back to the published address of the dashboard after its domain is
  verified. Geass does not use the Host header of the request. Thus, an attacker cannot make a provider with a permissive
  redirect list deliver a code to a host that the attacker selected. After the sign-in, the page that a person goes to is
  always a path on the dashboard.
- **Geass verifies the ID token. It does not trust it.** Geass checks these items:
  - The signature, with the published keys of the provider. Geass fetches the keys again when a token names a key that
    Geass does not know, but at most one time every 15 seconds. Thus, forged tokens cannot make the operator call the
    provider again and again. Geass also fetches the keys again after six hours.
  - The issuer, audience, expiry, and nonce.

  Geass accepts only public-key algorithms. It never accepts a shared-secret algorithm, because anyone who has the public
  key could forge such a token. It never accepts an unsigned token. Geass gets the token from the token endpoint of the
  provider over TLS, not from the browser.
- **A role is the role that the provider gave.** Geass maps the role claim through the list that an admin set. A person
  cannot sign in in these cases:
  - Their roles map to nothing.
  - They do not have a required claim. Geass compares the claim whole, and never splits it at commas.
  - The provider does not say that their email is verified (`true`, or the text `true`).
  - Their token names no subject.

  Without a required claim, everyone whom the provider signs in with a mapped role gets in. If anyone can sign up at the
  provider, restrict this setting. Geass fixes the role in the signed session cookie at sign-in. Thus, a change at the
  provider applies when the session ends (an hour by default, 5 to 720 minutes).
- **Only a signed-in admin can change it, never a token, and nobody when a platform manages it.** These rules apply:
  - With [managed mode](/docs/operations/managed-mode/) on, Geass refuses the settings, the test, and the switch (and the
    own provider of an organization) for everyone, administrators included. This rule applies whatever the rest of this
    list says.
  - Geass refuses the settings, the test, and the switch for every API token, observers included.
  - To turn it on, someone who maps to admin must make a test sign-in of exactly the saved settings, in the last half hour.
    Also, an admin who has a password must exist.
  - Geass refuses a save while people sign in through the provider.
- **The way in that does not depend on the provider stays.** A user in the Secret who is an admin and has a password can
  still sign in. While the provider decides, Geass refuses the password of a user who is not an admin. When you turn on the
  provider in the dashboard, Geass ends the open sessions of those users. A switch made by an edit of the resource by hand
  does not end them, so those sessions end when they expire.
- **The operator does not become a probe.** The issuer, and every address that its discovery document lists, must be
  `https`. The operator does not connect to these addresses:
  - This machine.
  - Link-local addresses, where the metadata service of the cloud is.
  - The metadata addresses of the clouds that put the metadata service at a different address.
  - An IPv4 address written as NAT64.

  Geass checks the address when you save the issuer, and again at every connection, so a redirect to one of these addresses
  also fails. The operator reads at most 1 MiB of a response, and it stops after ten seconds. Private network addresses are
  allowed, and these include addresses inside the cluster. For this reason, only a signed-in admin can set the address.
  `GEASS_ALLOW_LOOPBACK=true` is an operator setting that no admin or token can change. It lets a developer use a provider
  on their own machine, and it lets through loopback only.
- **The client secret is not shown, and it is bound to its provider.** It is in the Secret `platform-auth`. If you change
  the issuer or the client ID, you must enter the secret again.
- **Leaving.** When a person signs out, Geass ends every session of that person, in every browser. The provider can also
  end the sessions with a back-channel logout. Geass accepts the logout only when the provider signed it and it is recent,
  and Geass acts on it only one time. The logout also revokes the tokens and deploy hooks that the person made. An admin can
  turn this off (`revokeTokensOnLogout`, on by default), because CI depends on tokens made from the accounts of people who
  sign out. Without a back-channel logout, or with this setting off, a token stays valid after the access of the person
  ends, until the token expires or someone revokes it.

### Organizations

With `spec.auth.tenancy.enabled`, one Geass serves several organizations that must not see the work of each other.
[Organizations and workspaces](/docs/guides/organizations-and-workspaces/) tells you how to set it up. These measures
protect it, and they have these limits:

- **One check, not many.** The dashboard reads and writes the cluster through a single client. This client limits every
  call to the organization (and workspace) of the caller.
  - The client answers for a project, service, database, bucket, build, deployment, terminal session, or connection of a
    different organization as if it does not exist. Thus, a name or a count never shows that the object is there.
  - The client refuses three types of calls, and does not allow them. These are a kind that the client does not know, a
    call that names no tenant, and a patch whose effect it cannot read from the object. Thus, if a developer forgets
    something, the call fails closed.
  - The objects of the platform (the cluster, nodes, settings, and Secrets) do not belong to an organization, and an
    organization cannot read them. When code needs these objects for an organization, it first shows that the object is
    the caller's. Then it asks for the access of the dashboard itself.
- **Platform and organization routes are different.** A route that nobody classified belongs only to the platform. Geass
  refuses these routes to the administrators of an organization with `403`:
  - Settings, nodes, and the domain.
  - The registry of the platform.
  - Sign-in.
  - The alerts of the platform.
  - The shared servers of the platform.
  - The object store of the cluster.

  These routes do not belong to an organization, even when its members have an admin role. The things that an organization
  has of its own are organization routes. Geass checks each one against the organization that owns it. These things are:
  - Alert channels.
  - A registry.
  - Shared database servers and cloud connections made for the organization.
  - If the platform gives it one, a provider to sign in through.
- **The place where someone works is signed.** The organization, the workspace, and what the person can switch to are in
  the session cookie. Geass signs them with the same key as before. Thus, a member cannot edit their cookie into a
  different organization or into an administrator. A switch issues the cookie again with the same end and start. Thus, a
  switch does not extend a session, and it does not let a session live after a sign-out. Geass also looks up the
  organization on every request. If you suspend or delete the organization, or remove a workspace, Geass stops the open
  sessions immediately.
- **Streams end with the place.** Geass tells the overview stream, log streams, and terminals only what happens in the
  projects of the caller. Geass checks them again every 15 seconds against the place where the person works.
- **Tokens belong to the place where they were made.** These rules apply:
  - A token that an administrator of an organization makes acts only for that organization.
  - Organizations list and revoke only their own tokens.
  - A token made before organizations existed applies to the whole installation.
  - A project token takes the organization of its project. It reaches nothing while the project has no organization.
  - An organization can hold 100 tokens, so one organization cannot use up all the tokens.
- **Policy that the platform sets.** The platform can limit an organization in these ways:
  - How many projects it has, and how much the projects use together.
  - A region, and nodes that have tags.
  - Some domains for hostnames.

  A person cannot change any of these limits for their own organization.
- **Builds are separate.** A project of an organization builds in a namespace that belongs to the organization. The build
  gets copies of only the credentials that it needs. No connection to the build is allowed, and the build cannot reach a
  private address. Geass removes the copies when the build ends.
- **Geass checks a person again.** With this check on, Geass asks the provider every few minutes if each signed-in person
  still belongs to it, and if the provider says the same about them. A refusal or a change ends their sessions. Geass keeps
  the refresh tokens that it needs sealed in a Secret, and never shows them.
- **The own provider of an organization cannot reach beyond the organization.** What this provider says makes a role in
  that organization and nothing else. It cannot make anyone an administrator of the platform, or give them a different
  organization. It also cannot outlive a change to it.
- **Geass creates nothing before it shows that the caller owns it.** Geass first checks a request that names a project
  against that project. An organization cannot take a Secret that already exists, for example the credentials of a cloud
  connection.
- **Outside the API.** These rules apply:
  - A service cannot take a hostname that a different service serves, or the hostname of the dashboard.
  - Geass does not overwrite a DNS record in your Cloudflare zone that it did not create.
  - With organizations on, a GitHub push or pull request event must name the installation of the project that it is for.
  - You cannot connect to your project an installation that the project of a different organization uses.
  - A person must also show that GitHub lists the installation among the installations that their own account can use.

Geass does **not** isolate the items in this list. For this reason, give organizations that you do not trust their own
cluster:

- Their pods share your machines and kernel. To separate them, reserve nodes for an organization with required node tags.
- Builds still run on those machines. They use the registry login of the platform, unless the organization has its own.
- Shared database servers and the object store are for the whole cluster. A login keeps each database separate.
- The operator and the dashboard have access to the whole cluster. Thus, a flaw in the code of either one is a flaw in the
  separation.
- There is one GitHub App. Anyone who can install it on an account that they own can connect it.
- A person removed from an organization at the provider continues to work in it until their session ends (an hour by
  default). The exception is when the provider sends a back-channel logout, or when the platform checks people again.

### API tokens

A token has these parts:

- A prefix: `gsw_` for a workspace token, `gsp_` for a project token, `gsh_` for a deploy hook.
- A public identifier.
- A 160-bit random secret.

Geass stores a SHA-256 hash of the secret in the Secret `geass-api-tokens`. It compares in constant time, and it shows the
token only one time. Workspace tokens are `admin`, `observer`, or `viewer`. A project token can act on one environment of
one project, through a fixed list of endpoints. A project token cannot create resources, open terminals, or change
settings. Tokens can expire (up to ten years, or never). You can revoke a token immediately. Only a signed-in admin can
create, list, or revoke tokens, or approve a device code.

A token is not tied to the account of the admin who created it. Thus, if you remove that user, the token is not revoked.
The exception is a person who signed in through an [identity provider](/docs/guides/external-sign-in/). What this person
makes is tied to their identity at the provider. Geass revokes it when the provider signs them out.

The secret of a deploy hook is in a URL, where proxies and logs can see it. For this reason, a deploy hook does little:

- It deploys one service.
- Geass refuses it as a Bearer token everywhere.
- It answers only `POST`.
- When it is wrong, revoked, or expired, it looks the same as a missing hook.

Anyone who has the URL can deploy that service. Thus, treat the URL like a token, and delete the hook if it might have
leaked. A hook answers one time every 30 seconds. Thus, a leaked URL in a loop cannot stop builds from finishing. The request
log of the operator redacts the path, but a reverse proxy in front of the operator might not.

### Roles

A viewer sees a redacted copy of the platform. A viewer does not see these items:

- Variable values.
- Hostnames in the spec.
- Repository names.
- Backup storage.
- Project webhooks.
- Database host details.
- Logs, terminals, or secrets.

The one exception is the clouds that create machines. A viewer sees the name and cloud of each connection, and if the
connection works. A viewer also sees the pools and their nodes. But a viewer does not see the cloud account or what the
cloud said (an admin reads that), and never sees a credential. A viewer cannot change anything.

An observer is a role that only a token can have. An observer sees the same redacted copy. It also reads logs, pods, deploy
logs, and variable names, and it changes nothing. Everything else needs an admin. For the details, see
[Users and API tokens](/docs/guides/users-and-tokens/#roles).

Know these two limits of the observer role:

- It reads the values of the plain configuration of a service. Geass withholds only secret values. Thus, keep sensitive
  values in secrets.
- The logs that it reads are whatever your apps print.

The WebSocket of the terminal checks that the console session exists and is active, and that the origin of the request is
allowed. It does not check the role of the caller again, and the access check lets observer tokens through to it. An
observer cannot create a session. Thus, it has nothing to connect to, unless it knows the name of a session that an admin
opened in the last five minutes.

## The command line and AI agents

The [`geass` command line](/docs/guides/command-line/) and the [MCP server](/docs/guides/mcp-server/) are clients of the
[`/api/v1` API](/docs/reference/rest-api/). They add no new way to act on the cluster. Each one does what its token can do,
through the same checks as any other API call.

### Signing in with a device code

`geass login` starts a sign-in with `POST /api/v1/auth/device`, which needs no credentials. The command shows you a code.
A signed-in admin approves the code on the dashboard page `/device`. The admin selects these items:

- The access. Read only with logs is the recommended option and the default.
- A lifetime of 7, 30, or 90 days.

The command line polls the server. After the approval, the command line gets a workspace token one time. These measures
protect the sign-in:

- Only a signed-in admin session can approve. A token cannot approve, even the token of an admin. Thus, a leaked token
  cannot make a different token that outlives it.
- A request lives 10 minutes, in the memory of the operator. An admin can approve it one time.
- At most 100 requests are pending. One address can start 10 requests a minute. The operator counts the address of the
  connection and does not read `X-Forwarded-For`. Thus, behind an ingress, all clients share that count.
- The user code is eight letters with no vowels. The device code that collects the token is 32 random bytes.
- An approval writes the line `Approved a command-line sign-in` to the operator log, with the token ID, role, client name,
  and approver.

These measures do not protect against an admin who approves a code that they did not start. The holder of the device code
gets the token. The program that started the sign-in selects the name that the approval page shows. Approve a code only
when it matches the code on your own terminal. Keep the access at read only, unless the program must change things.

### Where the command line keeps its token

The command line stores logins in `config.json` in the `geass` folder of your user configuration directory (for example
`~/.config/geass/config.json` on Linux), or at the path in `GEASS_CONFIG`. It creates the folder with mode `0700` and the
file with mode `0600`. It writes the token in plain text, so protect the file as you protect an SSH key.

- `geass logout` removes the saved token only from the file. The token stays valid until it expires or you revoke it in
  **Settings** → **Tokens**.
- `geass config get-contexts --json` shows saved logins without their tokens.
- A `GEASS_TOKEN` environment variable or `--token` flag overrides the saved token. Then the command line does not use the
  file.
- In CI, keep the token in your secret store.

### The MCP server

With `geass mcp`, an AI agent can see and run your services. The server acts as the token that you are signed in with. It
offers an agent only the tools that the token can use. A read-only token cannot even see a tool that deploys. `--read-only`
removes every tool that changes something, whatever the token could do. The tool that deletes resources exists only with
`--allow-delete`. No tool returns secret values or database passwords. But the logs and plain configuration that an agent
can read might contain sensitive text.

- **Standard input and output (the default).** The agent starts `geass mcp` as a child process. Nothing listens on a network
  port.
- **HTTP (`--listen`).** The server holds no credentials. Each agent sends its own Geass token as
  `Authorization: Bearer ...`. Without a token, the server answers `401`. With a token, the server builds the tool list from
  that token. The server speaks plain HTTP. It prints a warning when the address is not a loopback address, because the
  token travels with every request. Bind the server to `127.0.0.1`, or put it behind TLS and a firewall. Geass checks the
  token on every tool call, so a revoked token stops working immediately. The server caches the list of tools built for a
  token for up to 5 minutes.
- **Which token to give an agent.** Give an `observer` token to an agent that must only watch. Give a project token to an
  agent that deploys a single environment. Give an `admin` token only when you accept that the agent can create services
  and change anything that the tools expose. Deletion needs `--allow-delete`.

Nothing records which agent called which tool. The operator log shows the method, path, and status of each API request,
not the caller. Geass updates the `Last used` time of the token in **Settings** → **Tokens** at most one time every 10
minutes. Give each agent its own token, so that you can revoke one token without the others.

An agent with an admin token can also run [node pools](/docs/guides/node-pools/). The agent can do these things:

- Read the pools and the connections of the clouds.
- List the sizes of a cloud.
- Create, change, and resume a pool, and delete a pool with `--allow-delete`.

A pool creates machines that cost money. Thus, the tools stop before the two decisions that a person keeps:

- No tool takes a credential, or makes, changes, verifies, or removes a connection.
- No tool adds or removes a single server.

Also, a pool cannot be made without `max_nodes`, its cap. The tools also have their own ceilings:

- An agent can set `max_nodes` to at most 10.
- An agent can set `min_nodes` (made immediately) and `spare_nodes` to at most 3.
- A person can set up to 200 in the dashboard or with `geass pools`.

Nobody can give an agent a secret that it could leak, and the agent cannot select which of your accounts to spend. But the
agent can still spend up to the cap of every pool that it makes, on a connection that already exists. Thus, give an agent
an admin token only when you accept that cost. See [MCP tools](/docs/reference/mcp-tools/#node-pools-and-clouds).

### Cross-site requests and headers

A write that carries a cookie must also carry an `Origin` or `Referer` header whose host matches the host of the dashboard.
This rule stops a different site from using the session of a visitor. The terminal WebSocket applies the same rule. Calls
with a Bearer token skip this check, because browsers never attach one.

A browser cannot set `Authorization` on a WebSocket. Some pages work with an API token, for example the dashboard shown
inside a different app (see
[Development](https://github.com/degoke/geass/blob/main/docs/development.md#the-dashboard-as-a-library)). Such a page
offers the token as the subprotocol `geass.bearer.<token>`, next to `geass.v1`. The server answers with `geass.v1` only. The
server reads a token from the subprotocol only for the upgrade of the WebSocket of a terminal. It applies the same rules as
for the header:

- The server refuses the token of a viewer or of a project.
- An open socket ends when its token is revoked.

Every response carries `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: same-origin`, and a
content security policy. This policy does these things:

- It takes scripts, fonts, and connections only from the address of the dashboard. Inline styles and `data:` images are
  also allowed.
- It refuses to be framed.
- It limits form posts to the dashboard and `github.com`.

## What Geass exposes

### The dashboard and API

The operator serves the dashboard and API over plain HTTP on port 8085 inside the cluster, behind the Service
`geass-geass-dashboard` (type ClusterIP). Nothing outside the cluster can reach it until you publish it.

You publish the dashboard in **Settings** → **Domain**. Geass then does one of these things. See
[Domains and TLS](/docs/guides/domains-and-tls/).

- It creates the Ingress `geass-dashboard` in `geass-system` on the `websecure` entry point of Traefik (the K3s ingress
  controller), with TLS from cert-manager.
- It points DNS at the cluster through Cloudflare.
- It runs a Cloudflare Tunnel.

:::caution
Give the cluster a Let's Encrypt contact email, or the dashboard gets no certificate. The Ingress of the dashboard, like
every service Ingress with **HTTPS** on, asks cert-manager for a certificate from a ClusterIssuer named `letsencrypt-prod`.
Geass creates that issuer only when the cluster has a Let's Encrypt contact email:
`spec.addons.certManager.acmeEmail`, which the installer sets from `GEASS_ACME_EMAIL`. Without an email, no issuer exists,
and no certificate is issued. The `ClusterIssuerReady` condition on the cluster says so, and the domain of the dashboard
stays `PendingHTTPS` with the same message. See [HTTPS certificates](/docs/getting-started/install/#https-certificates).
:::

Let's Encrypt must reach your server on 80/tcp to validate a hostname. Geass never overwrites an issuer with that name that
you created yourself. If you remove the email later, Geass does not delete the issuer that it made.

These paths work without a login:

| Path | Why |
| --- | --- |
| `/api/login`, `/api/logout`, `/api/session` | Sign in, sign out, and ask if you are signed in |
| `POST /api/v1/auth/device`, `POST /api/v1/auth/token` (also under `/api/auth/`) | Start a command-line sign-in and collect its token. A start only creates a request that a signed-in admin must approve |
| `/geass-probe` | A small JSON answer, used to verify the public address |
| `/webhooks/github` | GitHub cannot sign in. Every request needs a valid HMAC signature. Geass logs the delivery ID that GitHub sends and echoes it in the answer. Geass never uses it to skip a delivery |
| `/assets/*` and other page loads | The static files of the dashboard. They hold no data |

All other paths under `/api/` need a session or a token. The unauthenticated surface is small, but it is on the public
internet after you publish the dashboard.

### Ports on your servers

| Port | Used for | Who should reach it |
| --- | --- | --- |
| 6443/tcp | Kubernetes API | Your other nodes, and your own address if you run `kubectl` remotely |
| 2379/tcp, 2380/tcp | etcd, the database of the cluster, between control plane servers. Only needed with [more than one control plane server](/docs/operations/high-availability/#run-three-control-plane-servers) | The control plane servers, and nothing else |
| 10250/tcp | kubelet | Your other nodes |
| 8472/udp | Overlay network (VXLAN) | Your other nodes |
| 51820/udp | Overlay network (WireGuard, multi-provider clusters only) | Your other nodes |
| 80/tcp, 443/tcp | Apps and the dashboard through Traefik. Let's Encrypt validates hostnames on 80 | The internet, if you publish services |
| 22/tcp | SSH, used to join servers and for your own access | You, and the control plane to the workers. On a machine that Geass creates, only the addresses of the control plane |

If the installer finds an active host firewall on the control plane (`ufw`, `firewalld`, or `iptables`), it opens 6443,
10250, the overlay port, 80, and 443 in it. It does not limit the source address. An `iptables` rule goes after your own
rules and before a catch-all `REJECT` or `DROP` at the end of the `INPUT` chain. Thus, a block of yours still wins.
Restrict 6443 and 10250 to your node addresses in the firewall or security group of your provider.

The operator also serves these endpoints. Both stay inside the cluster.

- Health probes on 8086.
- In the installed manifest, metrics on 8443 (HTTPS, protected by Kubernetes authentication and authorization).

The manifest of the operator includes a ClusterRoleBinding (`metrics-reader-prometheus` in `config/rbac`). This binding gives
the `metrics-reader` role to the Prometheus service account of the monitoring add-on
(`geass-kube-prometheus-stac-prometheus` in `monitoring`). With this role, Prometheus can scrape the metrics of the
operator. On a cluster without that add-on, the binding binds nobody.

For a machine that Geass creates at a cloud, Geass also makes a firewall at the cloud. This firewall lets in these ports:

- SSH and the kubelet port (10250/tcp), only from the addresses of the control plane nodes.
- In a multi-site cluster, the UDP port of the overlay, from anywhere. Every packet is authenticated.

When Geass knows no control plane address, the firewall lets in SSH from anywhere. This is safe only because the machine
accepts nothing but the key of the cluster. At Oracle Cloud and OVHcloud, Geass makes no firewall, so those machines are
as open as the network of the project leaves them. See
[Cloud connections for machines](/docs/guides/connectors/clouds/#what-geass-makes-at-the-cloud).

The new machines must be able to reach 6443/tcp of the control plane. With
[`controlPlaneFirewall`](/docs/guides/node-pools/#let-geass-open-the-control-plane), Geass does these things:

- It edits the AWS security group or Hetzner firewall of the control plane to allow the address of each machine.
- It marks the rules that it adds (`geass` on AWS, `geass-access` on Hetzner).
- It never changes a rule that it did not add.
- It removes its rules when the machine goes away.

The connection that Geass uses for this needs the right to change that one firewall.

## Secrets

### Where they live

Everything secret is a Kubernetes Secret:

- Dashboard users and the session key.
- API token hashes.
- Cloud connection keys.
- The Cloudflare token.
- The GitHub App key.
- App secrets and shared secrets.
- Function schedule tokens.
- Database passwords.
- The K3s join token (`geass-token`).
- SSH keys.
- The credentials of the clouds that create machines.
- The key and host keys of those machines (`geass-nodepool-ssh`, `<node>-hostkey`).

[Cloud connectors](/docs/guides/connectors/) lists the names.

- **At rest.** On the clusters that it builds, the installer turns on K3s secrets encryption (`secrets-encryption: true`,
  AES-CBC). Thus, Secrets are encrypted in the K3s datastore and in its etcd snapshots. This includes the snapshot that the
  installer takes before an upgrade. Encryption has these limits:
  - It does not keep a Secret from anyone that the API lets read it, because the API server decrypts the Secret for them.
  - It does not keep a Secret from root on a control plane server.
  - K3s keeps the key in `/var/lib/rancher/k3s/server/cred/` on the disk of that server. Thus, a copy of the whole disk
    gives up every Secret.
  - A cluster that the installer did not build, such as K3s that you installed by hand, has no encryption, unless you
    turned it on in K3s yourself.

  You can ask K3s to send copies of the cluster to an S3 bucket
  ([Disaster recovery](/docs/operations/disaster-recovery/)). These copies also hold every Secret, and the key that decrypts
  them. Only the server token of the cluster protects them. Keep that bucket for this use only, with encryption on, public
  access blocked, and a key that can use only that bucket.
- **In the API.** Geass never copies Secret values into the `status` of a resource. The dashboard sends the names of secret
  keys, never their values, and viewers do not even get the names. The exception is a service that you wrote
  `spec.secretData` into by hand. That field is part of the resource, so admins get its values in `GET /api/bootstrap`. The
  resource reads of `/api/v1` blank the values. Geass puts secrets saved through the dashboard or API into a Secret instead.
  Plain configuration is not a secret: admins, observers, and project tokens read its values.
- **In your apps.** Geass copies app secrets into the namespace of the environment as a Secret, and injects them as
  environment variables. Anything that runs in your app can read them, as with any environment variable.
- **Function schedules.** A function that is a handler, or that has schedules, gets a random token in the environment
  variable `GEASS_TRIGGER_TOKEN`, from the Secret `<app>-trigger` in the namespace of the environment. The CronJob of the
  schedule sends the token in the `x-geass-trigger-token` header. The function compares the token in constant time to tell
  a scheduled call from a visitor. Anyone who can read that Secret can forge a scheduled call. The token travels inside the
  cluster as plain HTTP, like all traffic between services.
- **The mail relay.** While [the relay](/docs/guides/mail-relay/) runs, Geass keeps the secrets of the relay like this:
  - The login of each organization is in `geass-mail-credentials-<organization>` in `geass-system`. A copy is in
    `geass-mail` in each environment of the projects of that organization, where its own services can read it.
  - The relay gets only the SHA-256 digests of the passwords, in the Secret `geass-mail-relay`. The passwords are 32 random
    bytes, so a fast digest is sufficient. The same Secret holds the token that Geass uses to read the metrics and usage of
    the relay.
  - Geass never mounts the authority key of the relay certificate, `geass-mail-relay-ca`, in the relay.
  - The login for the upstream stays in the Secret that you name, which the relay mounts.

  Geass never logs any of these secrets.
- **In transit.** Inside the cluster, traffic to the dashboard is plain HTTP. Publish the dashboard over HTTPS. The mail
  relay requires TLS before an organization signs in, with a certificate that clients check against `SMTP_CA_CERT`.

:::caution
Do not turn off isolation for a project that has databases. The password of an in-cluster database that Geass creates is random, and the MySQL root password is the same
value. But a database made by an earlier release keeps the password that it started with. Geass derived that password from
the resource name of the database (`<name>-password`). Anyone who knows the name of such a database and can reach its port
has its password. [Network isolation](/docs/guides/network-isolation/), and ports that you do not publish, protect these
databases. See [GeassDatabase](/docs/reference/resources/geass-database/#connection-secret).
:::

### Who can read them

An admin cannot read secret values through the dashboard. But an admin can open a terminal in a running service and read
its environment. Anyone with `kubectl` and rights on `geass-system` can read every Secret there. The operator itself has
access to Secrets in the whole cluster (see the next section).

## What the operator can do in your cluster

The operator and dashboard run as one pod, `geass-controller-manager`, in `geass-system`, on a control plane node. The pod
runs as a non-root user, with a read-only root filesystem, no added capabilities, and no privilege escalation. Its service
account is bound to the ClusterRole `geass-manager-role`, which allows these actions:

| Resources | Verbs |
| --- | --- |
| Secrets, ConfigMaps, Services, PersistentVolumeClaims, Namespaces | create, get, list, watch, update, patch, delete |
| Nodes | get, list, watch, patch, delete (a node is cordoned and removed when a pool or a server is removed) |
| Pods, PersistentVolumes | get, list, watch |
| `pods/exec` | create (used by the terminal and the SQL console) |
| `pods/eviction` | create (used to empty a node before Geass removes it, so that Geass obeys disruption budgets) |
| `pods/log` | get |
| Deployments | create, get, list, watch, update, patch, delete |
| StatefulSets | get, list, watch |
| Jobs, CronJobs, HorizontalPodAutoscalers, Ingresses, NetworkPolicies | create, get, list, watch, update, patch, delete |
| StorageClasses | create, get, list, watch, update, patch, delete |
| CSIDrivers | get, list, watch |
| `helm.cattle.io` HelmCharts | create, get, list, watch, update, patch, delete |
| `helm.cattle.io` HelmChartConfigs | create, get, list, watch (Geass creates one only when the scale-to-zero add-on is on and the K3s Traefik has no configuration of its own) |
| cert-manager ClusterIssuers | get, list, watch, create, update, patch |
| Prometheus Operator ServiceMonitors, PodMonitors, and PrometheusRules | create, get, list, watch, update, patch, delete |
| KEDA HTTP add-on `HTTPScaledObjects` (`http.keda.sh`) | create, get, list, watch, update, patch, delete |
| ValidatingAdmissionPolicies, MutatingAdmissionPolicies, and their bindings (`admissionregistration.k8s.io`) | create, get, list, watch, update, patch, delete |
| MariaDB operator `MariaDB` and `PhysicalBackup` objects (`k8s.mariadb.com`) | create, get, list, watch, update, patch, delete |
| CloudNativePG clusters, backups, and scheduled backups | create, get, list, watch, update, patch, delete |
| Longhorn nodes and volumes | get, list, patch |
| Geass resources | create, get, list, watch, update, patch, delete |
| Geass resource `status` and `finalizers` | get, update, patch for `status`, and update for `finalizers` |

The ClusterRole has no rule that grants permission to change RBAC objects. The operator can read and write every Secret,
and run commands in every pod in the cluster. Thus, treat anyone who controls the operator, its image, or its service
account as a cluster administrator.

Geass also creates HelmChart resources. The built-in Helm controller of K3s installs them for the add-ons of Geass:
cert-manager, monitoring, Longhorn, logging, the scale-to-zero add-on, and some database engines. Thus, review the add-ons
that you turn on. Geass uses the admission policy rights for compliance tags (see
[Keeping a project's data on chosen nodes](#keeping-a-projects-data-on-chosen-nodes)). The operator creates two policies
and their bindings, which apply only to namespaces that the operator manages.

The other Geass roles in the cluster are these:

- The leader-election Role in `geass-system`.
- A metrics authentication role.
- Admin, editor, and viewer ClusterRoles for apps, databases, object stores, clusters, and nodes. You can bind these roles
  to people if you give them `kubectl` access.

Workloads that you deploy get no special service account. Their pods mount the default service account token of their
namespace. Function pods are the exception: they do not mount a token. Geass sets no security context on your pods. Thus,
a container runs as the user that its image specifies, including root.

## Network isolation

Without network policies, every pod in a Kubernetes cluster can reach every other pod. Geass applies these policies
automatically:

- **Project isolation.** Every project environment namespace gets the policy `geass-project-isolation`. Only these
  namespaces can reach its pods:
  - The namespaces of the project itself.
  - The projects that you list.
  - The platform namespaces: `kube-system`, `geass-system`, `monitoring`, `cnpg-system`, `moco-system`, `mariadb-system`,
    and `keda`, where the scale-to-zero proxy runs.

  This is the default (`isolation: Project`). A project that sets `isolation: None` is open to every pod in the cluster.
  For the details, see [Network isolation](/docs/guides/network-isolation/).
- **Cloud metadata.** Every project namespace gets `geass-block-cloud-metadata`. This policy lets pods reach everything
  except `169.254.0.0/16`. Thus, a pod cannot read the instance credentials of the machine that it runs on. The policy
  applies whether the project is isolated or not.
- **The machines of the cluster.** A project can turn on `blockNodeAddresses`. This setting adds the addresses of every
  node to the exceptions of that same policy. Thus, a pod cannot open a connection to the kubelet of a node or to the
  metrics of the host. The Kubernetes API stays open on the control plane, because Postgres databases use it. The setting
  is on for new projects, and off for projects that existed before it. Geass keeps it in step with the nodes. See
  [Network isolation](/docs/guides/network-isolation/#keep-services-away-from-the-clusters-machines).
- **Project limits.** A project can cap what it asks for as a whole, and what each of its environments asks for. The
  limits are off by default. See [Project limits](/docs/guides/project-limits/).
- **Outbound limits.** On a cluster that organizations share, the platform can close mail and other ports to addresses
  outside the cluster for the pods of every organization (`spec.tenancy.egress`). Geass writes these limits into the same
  policy, because a separate policy could not close what this policy opens. The limits are off by default. They do not
  detect mining, and they do not limit how much mail is sent. See [Outbound limits](/docs/guides/outbound-limits/).
- **The mail relay.** When the relay runs, it has its own policy, `geass-mail-relay`. Every pod can reach its submission
  port, which asks for the login of an organization. Only `geass-system` and `monitoring` can reach its admin port. The
  relay reaches only the DNS of the cluster and the port of the upstream. Organizations reach the relay whatever their
  outbound limits say. See [Mail relay](/docs/guides/mail-relay/#networking).
- **A check that policies are enforced.** All of this depends on a network plugin that acts on policies. Geass tests the
  plugin, and the **Usage** page says **Not enforced** when the plugin does not act on them. See
  [Network isolation](/docs/guides/network-isolation/#check-that-the-cluster-enforces-policies).
- **Telemetry.** The Prometheus and log stores have no login, and they hold the data of every project. Thus, Geass limits
  them to the platform namespaces with `geass-restrict-metrics-store` and `geass-restrict-log-store`.
- **Object storage.** The object storage server of the cluster gets the policy `geass-s3` in `geass-system`:
  - Its S3 port (8333 for SeaweedFS, 3900 for Garage) is open to every namespace, because apps reach their buckets through
    it. The login of each project limits the project to its own bucket.
  - The Garage admin port, 3903, is open only to `geass-system`. The operator uses this port there to make buckets and
    keys.
  - Every other port is closed.

  The server also closes those ports itself, so the policy is a second lock. SeaweedFS listens on localhost, and a
  forwarder container publishes S3. The other Garage ports are closed. When you remove the server, Geass removes the policy.

Policies help only if your network plugin enforces them. The default K3s plugin enforces them. If you replaced the network
plugin, make sure that your plugin enforces them.

## Keeping a project's data on chosen nodes

Network isolation limits who can talk to a project. Compliance tags limit which machines the project runs on. You mark a
node tag, such as `hipaa`, as a **compliance tag** on the nodes that qualify. A compliance tag is always reserved, so the
work of other projects does not go to those nodes. Then you set the tag as a required tag on the project. After that, these
rules apply:

- The operator puts the node selector on everything that the project runs: services, databases, buckets, builds, and the
  jobs that back them up and restore them. The schedules of its functions also run there.
- An admission policy, `geass-required-node-tags`, refuses a new pod in the namespaces of the project in these cases:
  - The pod does not have the node selector for each required tag.
  - The pod names its own node.

  This policy is the backstop for pods that Geass did not create, such as a chart that someone installed by hand. It
  applies to namespaces that Geass manages, and the operator never removes it. It needs a Kubernetes version that serves
  `ValidatingAdmissionPolicy` (1.30 or later). On an older cluster, the operator logs that it does not check pods created
  outside Geass, and the node selectors still apply.
- A second policy, `geass-required-node-tags-defaults`, adds the node selector and toleration that a pod does not have.
  Thus, pods that the operators that Geass runs create themselves go to the tagged nodes, and the first policy does not
  refuse them. An example is the copies of the MariaDB operator. This policy only narrows where a pod can go. It never fails
  a pod that it cannot handle. It needs Kubernetes 1.36 or later.
- With Longhorn, Geass keeps the copies of the database volumes of a project on the tagged nodes. The operator makes a
  storage class for each combination of region and required tags. Geass keeps the volume copies of other projects off
  compliance nodes. The same rule applies to the volume of the object storage server itself. That volume keeps its copies
  on the nodes that have the tags that the server was set up with.
- Geass does not ship the logs of the project to the shared log store. Thus, Geass does not keep their history, and the
  logs stay on the nodes. Live logs still work.
- A project with databases cannot take a tag that its databases are not on. Geass refuses for the project a shared database
  server, or the cluster object storage server, that does not run on tagged nodes.

Compliance tags do not do these things:

- Move data that is already in a different location.
- Protect against a compromised node.
- Cover data outside the cluster: buckets in AWS S3, PlanetScale databases, and the backups that a project writes to S3.
  The backup jobs run on the tagged nodes, but they write to your bucket.

See [Compliance tags](/docs/guides/compliance-tags/).

:::caution
Build only repositories that you trust. Repository builds run as Kaniko Jobs in `geass-system`, which the isolation
policies treat as part of the platform. A Dockerfile can run any command. Pods that Geass starts for a build run as root.
:::

## Outbound webhooks

Project webhooks sign each delivery with a secret for each webhook (`whsec_...`). Geass sends the signature as
`X-Geass-Signature: t=<time>,v1=<HMAC>` over `<time>.<body>`. Geass keeps the signing secrets in the Secret
`<project id>-webhooks`, not in the project.

Deliveries always refuse loopback and link-local addresses. Deliveries also refuse private ranges (RFC 1918 and shared
address space), unless you set `GEASS_WEBHOOKS_ALLOW_PRIVATE_NETWORKS=true` on the operator. Geass checks the address when
it makes the connection, which stops DNS tricks. Deliveries use no proxy and follow no redirects.

[Alert](/docs/guides/alerts/) channels are different. Alertmanager, in `monitoring`, connects to Slack and to the mail
server, so that guard does not apply to it. When you save a channel, Geass refuses a host that is, or resolves to, a
loopback, link-local, or private address. Private addresses are refused unless they are allowed. Geass cannot see a name
that resolves to a different address later. The secrets of the channels (the Slack URLs and the mail password) are in the
Secrets `geass-alert-channels` and `geass-alert-smtp`. Only a signed-in admin, never a token, can see or change the
settings.

## Credentials your connections hold

| Connection | What the credential can do | How to limit it |
| --- | --- | --- |
| AWS | Create and delete S3 buckets, and create and delete IAM users named `geass-*` | Use the policy in [Connect AWS](/docs/guides/connectors/aws/#permissions-the-aws-key-needs). Restrict S3 resources to a name prefix |
| PlanetScale | Create databases, branch passwords, and Postgres roles in the organization, delete the logins that Geass made, and read cluster sizes | Use a token limited to what Geass needs |
| Cloudflare | Edit DNS in the zone, edit tunnels in the account | Scope the token to one zone |
| A cloud for machines | Create, list, and delete machines in the project, resource group, compartment, or account that it is limited to, with the disks, addresses, and firewalls that they need. Thus, spend whatever those machines cost. The permissions of each cloud are on its page | Give it a separate project or account, exactly the permissions on [its page](/docs/guides/connectors/clouds/), and a budget alert at the cloud. The `maxNodes` of a pool caps how many machines Geass makes. It does not cap what your account can be charged otherwise |
| GitHub App | Read contents, metadata, and checks of the repositories where it is installed. Write commit statuses, deployments, and pull requests (comments) there. It cannot push code or merge a pull request, because these actions need Contents write | Install it only on the repositories that you deploy. For a project that Geass must not write to, turn off **Report to GitHub** |

**What Geass writes to GitHub.** Geass reports each build as a commit status (`geass/build/<service>`). Geass reports each
deploy as a GitHub Deployment, and each pull request environment as one comment on the pull request. Geass writes these items:

- A state.
- A short reason, such as `kaniko Job failed`.
- The public address of a service.
- A link to the dashboard.

Everyone who can see the repository can see these items. Thus, the address of a service built from a private repository
is shown to the readers of that repository, and to nobody else. Geass needs the write permissions for this and for nothing
else.

**Pull requests** write is the narrowest permission that GitHub offers for comments. It also allows edits to the title,
body, and reviews of a pull request. Thus, the installation token of Geass can do that too, although Geass does not.

The permissions are all or nothing for each app. An app registered before Geass reported to GitHub does not have them,
and the owner must approve them in GitHub. Until the owner approves them, the reports are refused and dropped.
The `GitHubReporting` condition of the connection says so. See
[Reporting back to GitHub](/docs/guides/github-and-builds/#reporting-back-to-github). The setting is
`spec.githubReporting` on the project, and viewers do not see it.

AWS and PlanetScale connections belong to one project, and only that project can use them. The Cloudflare and GitHub
credentials belong to the whole cluster. A connection for machines also belongs to the cluster, but a project can never
use it. Geass refuses a pool or a server that names a connection scoped to a project. Only an admin can make, change, or
use a connection for machines. An AWS connection that holds buckets never creates machines, because it does not have the
purpose Compute. Make a separate connection with its own key. The AWS keys for each bucket that Geass gives to apps reach
only their own bucket.

## Machines Geass creates

For a machine that it creates, Geass does not depend on a key that you made, or on trust in an address:

- **One SSH key for the cluster.** Geass makes it the first time that it needs one, and keeps it in the Secret
  `geass-nodepool-ssh` in `geass-system`. Only the **public** half goes onto a machine, in its cloud-init. Anyone who can
  read Secrets in `geass-system` can read the private half and log in to every machine that a pool made.
- **A host key for each machine.** Geass makes it, puts it on the machine in the same cloud-init, and keeps it in the
  Secret `<node>-hostkey`. Before Geass sends anything, it checks the host key of the machine against this key. Thus, the
  join token can reach only that machine. For a server that you bring, this pinning is optional. Here, Geass does it for
  you. The host key identifies one machine, so its private half in the user data of the machine is not a cluster secret.
- **No join token in user data.** The user data of a machine can be read from inside the machine and from the API of the
  cloud. Geass puts only the public key, the host key, and the hostname there. Geass sends the join token over SSH when it
  installs K3s. As for any worker, the token is in the install command. Local users on the new machine can see it while the
  command runs.
- **A login of its own.** The cloud-init creates the user `geass` with passwordless `sudo`, and turns off password login.
- **Deletion is limited to what Geass can name.** Geass deletes a machine only by the ID that the cloud gave it and that
  Geass recorded for a node. Geass lists machines only by labels that carry the identity of this cluster. Thus, Geass never
  touches a second Geass cluster in the same account. The deletion of a machine that no node owns is **opt-in**. A pool
  does it only when you set `orphanPolicy: Delete`, and then only for a machine that nothing has owned for 20 minutes. The
  default is to report the machine.
- **The pool and you cap the money.** A pool cannot be made without `maxNodes`. When three machines in sequence fail to
  join, Geass pauses the pool. These rules limit how much Geass can spend through a connection. They do not limit what
  someone who has the credentials of the connection could use them for. The project or account boundary is for that.

## SSH and joining servers

Geass joins a server over SSH, as root or a sudoer, with a private key in a Secret that you reference. Geass does not
verify the SSH host key of the server unless you pin it. Thus, by default, Geass cannot tell if the address now points at a
different machine.

To pin the key, put the host key of the machine in the same Secret under `known_hosts` (see
[Add servers](/docs/getting-started/add-servers/#pin-the-servers-host-key-recommended)). Then Geass refuses any other
machine before it sends a command or the join token. Without a pinned key, join only servers whose network path you trust.
Use a key made for this purpose, and prefer a private network.

The K3s join token in `geass-token` lets any machine that can reach port 6443 join the cluster. Thus, protect it like a
password. A machine that Geass creates is different. See [Machines Geass creates](#machines-geass-creates).

## Auditing

- **Terminals.** When someone opens a terminal in a service, Geass creates a `GeassConsoleSession`. It records who opened
  the terminal (`token:<name>` for a token), which pod and container, the command, and a timeout of five minutes. To list
  the sessions, run `kubectl -n geass-system get geassconsolesessions`. The operator log also records when each terminal
  opens and closes. Nothing records what was typed.
- **SQL console.** An admin can run a statement in a database from the dashboard (`POST /api/databases/{db}/query`). Geass
  runs the statement inside the database pod with `psql`, `mysql`, or `redis-cli`. For SQLite, Geass sends a request to
  the HTTP API of rqlite. Geass creates no session object for the statement, and the operator log holds only the request
  path. Thus, nothing records the statements.
- **Tokens.** When someone creates or revokes a token, Geass writes a line to the operator log. Geass also writes a line
  when an admin approves a command-line sign-in (`Approved a command-line sign-in`, with the token ID, role, client name,
  and approver). Geass does not log a refusal.
- **Revealed connection details.** `GET /api/v1/databases/{name}/connection?reveal=true` returns the password and
  connection string of a database to a workspace admin. It writes `Connection details revealed` with the kind, name, and
  caller.
- **Agents and the command line.** Nothing records which command or MCP tool was used. The name and `Last used` time of
  the token are the only record of who called. Thus, give each person, script, and agent its own token.
- **Requests.** The operator logs every dashboard request (method, path, status, duration), but not the caller or the
  body. The logged path is the path that the client sent, so `/api/v1/...` calls appear with that path. Geass does not
  keep a general audit trail of changes. To get one, use Kubernetes audit logging, which is a K3s setting.

```bash title="Read the operator log"
kubectl -n geass-system logs deployment/geass-controller-manager -c manager
```

## Hardening checklist

1. Create each user with a long, unique password. Give people the `viewer` role, unless they must change things. Remove
   users who leave, and revoke the tokens that they approved or created. See
   [Users and API tokens](/docs/guides/users-and-tokens/).
2. Publish the dashboard only over HTTPS. Give the installer a real contact address (`GEASS_ACME_EMAIL`) so that Geass
   creates the `letsencrypt-prod` ClusterIssuer, or use a Cloudflare Tunnel. Do not expose port 8085.
3. Put the dashboard behind something that rate limits sign-in, or restrict the dashboard to your address range.
4. Use project tokens in CI. Give every token an expiration, store it in the secret store of your CI, and revoke tokens
   that you no longer use. Do not put an admin workspace token in CI. Give monitoring scripts and AI agents an `observer`
   token, one for each. Never give an agent an admin token, unless the agent must change things.
5. Limit the credentials of your connections:
   - Give AWS a dedicated IAM user with the policy from
     [Connect AWS](/docs/guides/connectors/aws/#permissions-the-aws-key-needs), and rotate the key.
   - Scope the Cloudflare token to one zone.
   - Install the GitHub App only on the repositories that you deploy.
   - Give a cloud that creates machines a separate project or account, exactly the permissions on its page in
     [Cloud connections for machines](/docs/guides/connectors/clouds/), and a budget alert at the cloud.
   - Set the `maxNodes` of a pool to a number that you can pay for.
   - Keep `orphanPolicy` on `Report` until you have checked what it would delete.
6. In the firewall of your provider, restrict 6443, 10250, and the overlay port to your node addresses. Open 80 and 443 to
   the internet only if you publish services (certificates need 80). Limit 22 to the addresses that you use.
7. Keep project network isolation on (`Project`, the default). Open a project only on purpose. In-cluster databases made
   by an earlier release have a password that someone can guess from their name, so isolation is their main protection.
8. Encrypt the disk of the control plane. Keep the server token (`/var/lib/rancher/k3s/server/token`) and your etcd
   snapshots outside the cluster, in a protected location. The installer turns on K3s secrets encryption, but K3s keeps the
   key on the disk of the server. A restore needs the token (see [Go back](/docs/operations/upgrading/#go-back)). If you
   run K3s that you installed by hand, turn on `secrets-encryption` in it.
9. Build only repositories that you trust, and join only servers that you trust.
10. Set up database backups (see [Backups and restore](/docs/guides/backups-and-restore/)), and test a restore.
11. Approve a device code only when it matches the code on your own terminal. Select the shortest expiry that works.
    Protect `~/.config/geass/config.json` on every machine where someone ran `geass login`.
12. Run `geass mcp --listen` only on `127.0.0.1`, or behind TLS and a firewall.
13. Put sensitive values in secrets, not in plain configuration, because observer tokens and agents can read plain
    configuration.
14. Watch the operator log and the logs of your servers.
15. If organizations that do not trust each other share the cluster, run their code under a
    [sandboxed runtime](/docs/operations/sandboxed-runtime/) (gVisor or Kata). Their builds and their MySQL, Redis, and
    SQLite servers also use the sandbox. Their PostgreSQL and MariaDB servers cannot run in it. Thus, Geass does not make
    these servers for an organization when the sandbox is set, and the servers made before continue to run outside the
    sandbox. Move their data, and approve the restart of each database server that ran before the sandbox was set.
16. If organizations that do not trust each other share the cluster, do these things:
    - Set [registry isolation](/docs/operations/registry-isolation/), so that Geass never gives their builds the registry
      login of the platform.
    - Give each organization its own registry login.
    - Turn on `AlwaysPullImages`.
17. If you run organizations, do these things:
    - Give organizations only to people and groups that you trust to share machines.
    - Keep the `platformClaim` to the few people who run the installation.
    - Keep an admin who signs in with a password.
    - Make `sessionMinutes` shorter if people are often removed from organizations.
    - Close mail to the outside for the organizations with [outbound limits](/docs/guides/outbound-limits/), and send their
      mail through a relay that you run.
18. If strangers run code on the cluster, do these things:
    - Turn on [abuse detection](/docs/guides/abuse-detection/) with `action: None`.
    - Review what it finds (`GET /api/v1/abuse/suspects`).
    - Exempt the projects that do legitimate heavy work.
    - Only then, consider `Suspend`.

    Abuse detection is a signal, not proof. It misses a miner that stays below its threshold, pauses, or uses a GPU. A
    runtime monitor such as Falco can report to it. But Falco cannot see into gVisor-sandboxed pods, so it cannot report
    from inside them. Also close the ports of the mining pools (`miningPools: Blocked`), and know that this is a heuristic.
    Close mail to the outside for these organizations with [outbound limits](/docs/guides/outbound-limits/). Send their
    mail through the [mail relay](/docs/guides/mail-relay/), with these settings:
    - A required sender domain.
    - The domains of each organization verified at the upstream.
    - A sending quota at the upstream too, because the counts of the relay start again when the relay restarts.
19. If a platform runs your Geass for you, or you run Geass for others, turn on
    [managed mode](/docs/operations/managed-mode/) (`spec.managed.enabled`). Then nobody signed in to the dashboard can
    change sign-in, the domain of the dashboard, or the cluster-state backup. Managed mode is only a guard rail. Protect
    `GeassPlatformConfig`, `GeassCluster`, and the Deployment of the operator with Kubernetes RBAC. Keep the installer off a
    control plane that the platform hosts.

## What stays your responsibility

Geass does not do these things for you:

- **Operating system.** Patches, kernel updates, SSH configuration, disk encryption, and time sync on every server.
- **K3s.** Geass upgrades K3s only when you ask it to, with `--k3s-version` (the control plane) and `spec.k3s.version`
  (the workers). It does not patch K3s automatically, and it does not watch for K3s security releases. These tasks are
  yours. See [Upgrading](/docs/operations/upgrading/#update-k3s). Each update leaves an etcd snapshot in
  `/var/lib/rancher/k3s/server/db/snapshots`, and this snapshot holds every Secret in the cluster. Protect the snapshots as
  you protect the disk, and delete old snapshots.
- **Network.** Provider firewalls and security groups, DNS, and DDoS protection.
- **TLS.** A real contact address for the Let's Encrypt account, port 80 reachable for validation, and the domain that you
  point at the cluster. Geass creates the `letsencrypt-prod` ClusterIssuer from that address, and cert-manager renews
  certificates. But if you run your own issuer or your own cert-manager (`spec.addons.certManager.enabled: false`), you
  must keep it working.
- **Control plane backups.** Geass backs up databases to S3 when you configure it. The K3s datastore holds the Secrets
  and resources in `geass-system` (users, tokens, connections, and projects). By default, its etcd snapshots stay on the
  disk of the server. K3s sends copies to an S3 bucket of your own only after you turn this on. See
  [Disaster recovery](/docs/operations/disaster-recovery/#keep-copies-of-the-clusters-state). Keep the snapshots and the
  bucket as protected as the Secrets that they contain.
- **Your accounts.** Two-factor sign-in, key hygiene, and audit logs on AWS, Cloudflare, PlanetScale, and GitHub. The same
  applies to every cloud that you let Geass create machines at, including its bill. Geass caps the machines of a pool, but
  it cannot see what else your account is charged.
- **Your apps.** The images that you run, their dependencies, and their own authentication.
- **Detection and response.** Monitoring, alerts, and incident handling. Geass gives you these tools:
  - With the monitoring add-on on, Geass defines Prometheus alert rules for the health of the operator. The rule is
    `geass-operator` in `monitoring`. It covers the operator when it is down, failing or slow reconciles, a backed-up work
    queue, and a slow or failing Kubernetes API server.
  - An admin can set up [alerts](/docs/guides/alerts/) to Slack or email, which Alertmanager sends. These alerts cover
    failed builds and deploys, services that are down, failing backups, and crashing pods. They also cover filling storage,
    nodes that are not ready, and the alerts of the operator itself.
  - The [abuse detection](/docs/guides/abuse-detection/) of Geass watches the CPU use of the services of organizations for
    patterns that look like mining. It also takes reports from a runtime monitor that you run. It does not look inside
    containers, and what it finds is a signal for you to review.

  Geass does not watch the traffic to your services, or what they log or return.

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
