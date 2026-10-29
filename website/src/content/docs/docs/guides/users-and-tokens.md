---
title: "Users and API tokens"
description: "Dashboard users and roles, plus API tokens and deploy hooks for scripts, CI, the CLI and agents: create, scope, sign in with a device code, rotate, revoke."
---

Geass has two ways to sign in. People use the dashboard with a username and password, and each user is either an **admin** or a
**viewer**. Programs use API tokens: a workspace token that acts as an admin, an observer or a viewer, and a project token that can
deploy and edit variables in one environment of one project. The [command line](/docs/guides/command-line/) gets its token without
anyone typing a password into it: it shows a code, and an admin approves it in the dashboard. A tool that can call a URL but cannot
send a header uses a deploy hook, a URL that deploys one service. This page covers users, tokens, deploy hooks and that approval, and
ends with `curl` examples for CI.

Roles are global. There are no per-project permissions for users. By default users come from a Kubernetes Secret, which has no invitations,
sign-in by email or organizations. To get those, sign people in through an identity provider such as Better Auth instead: it owns the people,
and Geass takes their role from its token. See [Sign in with an identity provider](/docs/guides/external-sign-in/). Tokens and deploy hooks
work the same either way.

## Roles

| Role | Held by | Can do |
| --- | --- | --- |
| `admin` | Dashboard users, and workspace tokens (**Full access**) | Everything in the dashboard and the API: create, change and delete resources, deploy, open terminals, manage settings and connections. Only a signed-in admin, not a token, manages tokens and approves device codes. |
| `observer` | Workspace tokens only (**Read only, with logs**) | Read everything a viewer can, and also logs, pods, deploy logs and variable names. Cannot change anything. The role for a monitoring script or an AI agent that should only watch. |
| `viewer` | Dashboard users, and workspace tokens (**Read only**) | Read the state of the platform. Cannot change anything, and cannot read anything sensitive (see below). |

A viewer who calls a write endpoint, or a sensitive read, gets `403` with `{"error": "viewer role cannot access this"}`. An observer
gets the same answer for any write.

A viewer can still read the overview the dashboard loads, with anything that could leak a secret or reveal your setup removed. What a
viewer does not get:

- Environment variable values, secret references, config data and `envFrom` sources of services.
- Service host settings, repository names and commits, container pull-secret names, and function source code. (A service's status still
  carries its public URL.)
- Project shared variables, webhooks and delivery records, the GitHub link, and the backup storage choice.
- Database engine, version, host, port, user and connection Secret names, and bucket names, regions and endpoints.
- Cloud connections, the platform configuration and builds.
- Service logs, deploy logs, runtime details, variables, terminals, project logs (live and history), the template list,
  GitHub repositories, GitHub settings, PlanetScale cluster sizes, and the token list.

Viewers can see service names and status, deployment history, usage, metrics and cluster events. They can also read
`GET /api/v1/apps/{name}/status`, which includes the latest build's phase, failure reason and source revision.

An observer gets the same redacted overview as a viewer, plus the reads in the last bullet above, except the token list and
terminals: it cannot create a terminal session, because that is a write. Two things to know about what an observer reads:

- A service's variables come back as names for secrets, but **plain configuration comes back with its values**. Keep anything sensitive in
  a secret, not in plain configuration.
- Logs can contain whatever your app prints, including secrets it should not have printed.

Dashboard users are `admin` or `viewer`. `observer` is a token role: a user whose `role` is `observer` is dropped (see the rules
below).

## Create and change users

There is no screen for users. They live in the Secret `geass-dashboard-auth` in the `geass-system` namespace, under
the key `users`, as a JSON array.

```json title="users"
[
  { "username": "admin", "password": "a-long-passphrase", "role": "admin" },
  { "username": "maria", "password": "another-long-passphrase", "role": "viewer" }
]
```

Rules Geass applies when it reads the list:

- `role` is `admin` or `viewer`. An empty role means `viewer`. Any other value, `observer` included, drops that user.
- Usernames are compared case-insensitively. Two users that differ only by case make the whole list invalid, and
  nobody can sign in until you fix it.
- A user with an empty password, or the password `CHANGE_ME`, is ignored. `CHANGE_ME` is the placeholder in the sample
  manifest, and Geass refuses it as a login. If no valid user is left, sign-in fails with "dashboard authentication is
  unavailable".
- There is no minimum password length or complexity check. Choose a long passphrase.

You can write a plain password. The next time Geass reads the Secret (the first sign-in attempt does it), it replaces
the password with a bcrypt hash and saves the Secret back. After that the Secret holds only hashes.

### Add the first admin

The installer does not create a login. Create the Secret yourself before you open the dashboard:

```yaml title="geass-dashboard-auth.yaml"
apiVersion: v1
kind: Secret
metadata:
  name: geass-dashboard-auth
  namespace: geass-system
type: Opaque
stringData:
  users: |
    [{"username":"admin","password":"replace-with-a-long-passphrase","role":"admin"}]
```

```bash title="Create the Secret"
kubectl apply -f geass-dashboard-auth.yaml
```

The operator also accepts users from environment variables when the Secret has none: `GEASS_DASHBOARD_PASSWORD`
(one admin, named by `GEASS_DASHBOARD_USERNAME`, default `admin`) or `GEASS_DASHBOARD_USERS`, either as a JSON array
like the one above or as `username:password:role` entries separated by commas. The Secret wins when it has users, and
Geass copies hashed environment users into the Secret on first use, so changing the variables later has no effect.
See [Configuration](/docs/reference/configuration/).

### Add, change or remove a user

Read the current list, change it, and write it back. This example adds a viewer and needs `jq`:

```bash title="Add a viewer"
kubectl -n geass-system get secret geass-dashboard-auth -o jsonpath='{.data.users}' | base64 -d \
  | jq '. + [{"username":"maria","password":"another-long-passphrase","role":"viewer"}]' > users.json

kubectl -n geass-system patch secret geass-dashboard-auth --type merge \
  -p "$(jq -n --rawfile users users.json '{stringData: {users: $users}}')"
rm users.json
```

The change is live. You do not restart anything.

- **Remove a user** by deleting their entry. Their session stops working on the next request.
- **Change a role** by editing `role`. It applies to sessions that are already open.
- **Change a password** by writing the new plain password over the hash. Sessions that are already open stay valid until
  they expire (12 hours) or the user signs out.

Tokens an admin created or approved are not tied to that admin's account: removing the user does not revoke them. Revoke them under
**Settings** → **Tokens**.

### Sessions and lockout

Signing in sets the cookie `__Host-geass_session` (HTTP only, `SameSite=Lax`, valid for 12 hours, not extended by use). It is
marked `Secure`, except when the request is plain HTTP to a loopback address (`localhost`, `127.0.0.1` or `::1`), where it is
`geass_session` and not `Secure`. A browser does not send a `Secure` cookie over plain HTTP, so open the dashboard over HTTPS or through
`kubectl port-forward` to `localhost`. The `__Host-` prefix means the browser accepts the cookie only from the dashboard's own host, with
no `Domain`, so a service running on a sibling subdomain of yours cannot plant a cookie of the same name for the whole domain. Upgrading
from a version that used the plain name signs everyone out once.

Signing out ends every session of that user, in every browser. To sign everyone out, delete the `session-key` key from
the Secret; Geass generates a new one and every old cookie stops working.

Five failed sign-ins for one username from one client address within a minute lock that username for that address for a
minute, so someone who keeps guessing is shut out and the person who owns the account, signing in from elsewhere, is not. Twenty-five
failures for one username from all clients together lock it for everyone for a minute. Where clients cannot be told apart, five failures
lock the username for everyone. The reply is the same `invalid username or password` while locked. The count for a username is
stored in the Secret key `login-lockouts`, so it survives an operator restart; the count for a username and an address is kept in memory.
Only your configured users get a record there, and a record is dropped once its minute is over. A client that fails 20 sign-ins in a
minute is also refused for a minute, whatever username it uses; that count is kept in memory too.

Geass takes a client's address from the connection when it comes from a public address. When it comes from a private one, as everything
does through the cluster's ingress or a Cloudflare Tunnel, Geass reads `X-Forwarded-For` from the right and uses the right-most address
that is public: the nearest proxy wrote it, and what a client put before it is not believed.
A caller inside the cluster can write that header itself and pick its address. What that buys it is another client's allowance, and
never more than 25 failed sign-ins for a username in a minute in all.

## API tokens

An API token lets a script call the [REST API](/docs/reference/rest-api/) without a browser. Geass shows the token once,
when you create it, and stores only a hash.

A token looks like `gsw_1a2b3c4d_` followed by 40 hexadecimal characters. The prefix tells the kind, the next part is a
public identifier used to find it, and the rest is the secret. A third kind, the [deploy hook](#deploy-hooks) (`gsh_`), is a URL
rather than a credential for the API.

| | Workspace token | Project token |
| --- | --- | --- |
| Prefix | `gsw_` | `gsp_` |
| Acts as | A dashboard user, across every project (of one organization, when [organizations are on](/docs/guides/organizations-and-workspaces/#tokens) and an organization's admin made it) | A fixed list of actions on one environment of one project |
| Access | `admin` (**Full access**), `observer` (**Read only, with logs**) or `viewer` (**Read only**) | Fixed list, see below |
| Choose when | A script manages many things, or you only need to read | CI deploys one service and edits its variables |

Prefer a project token whenever a script only needs to deploy, and an observer token whenever a script or an agent only needs to watch.

### Create a token

You need to be signed in to the dashboard as an admin. Tokens cannot create, list or revoke tokens, not even an
admin token (`403`: "API tokens can only be managed from the dashboard by an admin").

1. For a workspace token, open **Settings** → **Tokens**. For a project token, open the project's **Settings** →
   **Tokens**.
2. Choose **New token**.
3. Enter a **Name** that says where it is used, such as `github-actions` (up to 60 characters).
4. For a workspace token, choose **Access**: **Read only** (the default), **Read only, with logs** or **Full access**. For a project
   token, choose the **Environment** it may touch.
5. Choose an **Expiration**: **30 days**, **90 days**, **1 year** or **No expiration**. The default is 90 days.
6. Choose **Create token**, copy the token from **Your new token**, and store it in your secret manager. Geass shows it
   only once.

The API accepts any lifetime from 1 to 3650 days, and treats a missing `expiresInDays`, or `0`, as no expiration. A project
token needs a project and an environment that exist. At most 600 tokens, deploy hooks included, can exist at once, and 100 of them for one [organization](/docs/guides/organizations-and-workspaces/#tokens).

Create one with the API from a dashboard session (a token cannot do this). Sign in first and keep the cookie:

```bash title="Sign in and create a project token"
curl -fsS -c cookies.txt -X POST https://geass.example.com/api/login \
  -H "Origin: https://geass.example.com" \
  --data-urlencode "username=admin" \
  --data-urlencode "password=replace-with-your-passphrase"

curl -fsS -b cookies.txt -X POST https://geass.example.com/api/v1/tokens \
  -H "Origin: https://geass.example.com" \
  -H "Content-Type: application/json" \
  -d '{"name": "github-actions", "scope": "project", "project": "proj-1a2b3c4d", "environment": "production", "expiresInDays": 90}'
```

```json title="Response"
{
  "ok": true,
  "token": "gsp_1a2b3c4d_0123456789abcdef0123456789abcdef01234567",
  "item": {
    "id": "1a2b3c4d",
    "name": "github-actions",
    "scope": "project",
    "project": "proj-1a2b3c4d",
    "environment": "production",
    "role": "admin",
    "createdBy": "admin",
    "createdAt": "2026-10-12T09:00:00Z",
    "expiresAt": "2027-01-10T09:00:00Z",
    "expired": false
  }
}
```

For a workspace token for a monitoring script, send `{"name": "monitoring", "scope": "workspace", "role": "observer", "expiresInDays": 90}`
instead. The response carries `Cache-Control: no-store`. A project token reports `role: "admin"`, but its fixed list of actions
is what limits it.

### Use a token

Send it as a Bearer token on every request:

```bash title="Call the API with a token"
export GEASS_TOKEN=gsw_1a2b3c4d_0123456789abcdef0123456789abcdef01234567
curl -fsS -H "Authorization: Bearer $GEASS_TOKEN" https://geass.example.com/api/v1/me
```

`GET /api/v1/me` says who the token is and what it may do: its role, `canMutate`, `canReadSensitive` (true for an admin or an
observer), its scope, project, environment and expiry. `GET /api/token` shows the token's record: its name, scope, project,
environment, role and expiry. If you send a Bearer header with a bad, expired or revoked token, Geass answers `401` with
`invalid or expired API token`. It does not fall back to a cookie.

Calls made with a token skip the same-origin check that protects browser sessions, because a browser never attaches a
Bearer header by itself. Under `/api/v1`, send writes as JSON. The older dashboard routes under `/api` read form fields, not
JSON, so send those as `application/x-www-form-urlencoded`, which `curl --data-urlencode` does. A project token that sends another
content type to a dashboard route gets `415`; for any other caller a JSON body is not read and the fields arrive empty. See
[REST API](/docs/reference/rest-api/).

### Use the identity provider's access token

With [sign-in through an identity provider](/docs/guides/external-sign-in/) and `spec.auth.oidc.accessTokens` turned on, a program that is signed in at the provider can send **the access token the provider issued to the person** in place of a Geass token, `Authorization: Bearer <jwt>`, to everything an API token can reach (the REST API, the stream of events and the log streams, and a terminal's WebSocket as the subprotocol `geass.bearer.<jwt>`). Nothing is created in **Settings** → **Tokens**, and nothing long-lived is kept: the person is who acts, with the role and organizations their claims map to, and the actor Geass records is their address (or their subject). The token has to be a JWT access token whose audience is this cluster's, which the provider's documentation and the [setup guide](/docs/guides/external-sign-in/#access-tokens-as-a-bearer-credential) describe; the call is refused with `401` for any that is not (an ID token among them) and with `403` for a person whose claims map to no access.

Like an API token it cannot manage sign-in, make tokens or approve a device code (those need a signed-in admin's session), and a stream or terminal it opened ends when the token expires. It is off by default.

### What a workspace token can do

- An `admin` workspace token can call every endpoint a signed-in admin can, except the ones that manage tokens or approve device
  codes (they need a signed-in admin's session) and `POST /api/token/deploy`, which is for project tokens. That includes opening a
  terminal, so a leaked admin token is as serious as an admin password.
- An `observer` workspace token can read what a viewer can, with the same redactions, plus the reads listed under [Roles](#roles). Every
  write is refused.
- A `viewer` workspace token can read what a viewer can, with the same redactions.

### What a project token can do

A project token can use only these endpoints, and only for services in its own project and environment:

| Endpoint | Meaning |
| --- | --- |
| `GET /api/token`, `GET /api/token/services` | Describe the token; list the services and databases it can reach |
| `POST /api/token/deploy` | Deploy every pending change in the token's environment |
| `GET /api/apps/{name}/logs`, `/runtime`, `/variables`, `/metrics` | Read a service's logs, pods, variables (secrets as names only, plain configuration with its values) and metrics |
| `GET /api/apps/{name}/deployments/{id}/logs` | Read the logs of one deploy |
| `POST /api/apps/{name}/deploy`, `/rollback`, `/scale`, `/retry-provision`, `/update` | Deploy, roll back, scale, retry, edit settings |
| `POST /api/apps/{name}/build`, `/build/cancel` | Start or cancel a repository build |
| `POST /api/apps/{name}/config/set`, `/config/delete`, `/config/raw` | Edit plain configuration |
| `POST /api/apps/{name}/secrets/set`, `/secrets/delete`, `/env/delete` | Edit secrets and environment references |

On `/api/v1` the same routes work with JSON bodies (`PATCH /api/v1/apps/{name}` is `update`), and a project token can also call
`GET /api/v1/me`, `/version`, `/apps` (only its own environment), `/apps/{name}`, `/apps/{name}/status`, `/apps/{name}/deployments`
and `/apps/{name}/logs/stream`, `GET /api/v1/token/services` and `POST /api/v1/token/deploy`.

Everything else is refused with `403` and `this token cannot access that endpoint`: creating or deleting resources,
databases and buckets, terminals, networking and domains, settings, projects, users, connections, and reading project or
cluster logs. A request for a service in another project or environment gets `404` and `app not found`, the same
answer as a service that does not exist, so names do not leak. A form field `project` or `environment` that names a
different project is refused with `this token is limited to <project> / <environment>`.

A project token cannot read the dashboard's overview (`/api/bootstrap`), but on `/api/v1` `GET /api/v1/apps/{name}/deployments` lists
the deployment revisions that `POST /api/v1/apps/{name}/rollback` needs, so a project token can roll back there. On the older routes,
use a workspace token for rollbacks.

### Deploy with a project token

`POST /api/token/deploy` (or `POST /api/v1/token/deploy`) is the call CI makes. It deploys everything with pending changes in the
token's environment (see [Projects and environments](/docs/guides/projects-and-environments/)):

```bash title="Deploy pending changes"
curl -fsS -X POST -H "Authorization: Bearer $GEASS_TOKEN" https://geass.example.com/api/v1/token/deploy
```

```json title="Response"
{ "ok": true, "deployed": 2 }
```

`deployed` counts services, databases and buckets that had changes. A workspace token gets `403` here; it uses
`POST /api/projects/{project}/deploy` with an `environment` field instead. Geass deploys services first, then databases,
then buckets. If the cluster lacks room for one of the services or databases, the call stops there and fails with `400`
and the capacity shortfall as the message. Anything it deployed before that point stays deployed, and `GET /api/v1/token/services`
shows which services still have `pendingChanges`.

### Deploy hooks

A deploy hook is a URL that deploys one service when something calls it with `POST`. Use one from a tool that can call a URL but cannot
send a header: a registry that notifies when an image is pushed, a scheduler, a CMS, a button on a page. Anything that can send
`Authorization: Bearer` should use a project token instead.

```bash title="Call a deploy hook"
curl -X POST https://geass.example.com/hooks/deploy/gsh_1a2b3c4d_0123456789abcdef0123456789abcdef01234567
```

```json title="Response"
{ "ok": true, "action": "build", "service": "storefront" }
```

What it does depends on the service:

| The service is | `action` | What happens |
| --- | --- | --- |
| Built from a repository | `build` | Its current build starts over from the head of the branch, and the service rolls out when the build succeeds. A service with no build yet, one that was never deployed, or one that pins a commit answers `409`, as a push to the branch skips them too. |
| An image or a function | `deploy` | Its pending changes are deployed, which is what **Deploy changes** and **Redeploy** do in the dashboard, so changes someone saved but did not deploy go out too, and a draft is deployed for the first time. It is held to the same capacity check: a cluster with no room answers `400` and deploys nothing. |

Make one in the service's settings: open the service, **Settings** → **Deploy**, **Deploy hook**, and choose **Create deploy hook**. The URL
is shown once, with a copy button. A service has one hook. To get a new URL, delete the hook and make another, and the old URL stops
working at once. A hook appears in **Settings** → **Tokens** (and in the project's **Tokens**) as `Deploy hook · <service> · <environment>`,
where it shows when it was last called and can be revoked. The API makes one with `scope: "service"`, a `project`, an `environment` and an
`app`: only a signed-in admin can.

The secret is in the URL, because that is the only place such a tool can put it. So it is built to be harmless if it leaks into a proxy's
log:

- It is good for one service and one action. It is refused as a Bearer token on every API endpoint (`401`), whatever it is asked.
- Only `POST` does anything. `GET`, `HEAD`, `PUT` and `DELETE` answer `405`, so a link preview or a crawler cannot deploy anything.
- A hook that does not exist, is revoked, has expired or belongs to a service that is gone all answer `404` `hook not found`, so a caller
  cannot tell which. A token of another kind put in the URL is a `404` too.
- Deleting the service, its environment or its project revokes its hook.
- A hook that was called less than 30 seconds ago answers `429` with a `Retry-After` header and does nothing, so a URL that leaked into a
  loop cannot keep a service's builds from ever finishing or fill its deployment history. A call that did nothing (the cluster had no
  room, a `409`) does not start the wait.
- A client that keeps guessing is slowed down like one guessing passwords: after 20 misses in a minute even a good URL gets `429` for a
  minute (this limit applies to clients that connect from a public address, not through a proxy or tunnel).
- The operator's request log shows the path as `/hooks/deploy/<redacted>`, so reading the log does not hand over a hook. A reverse proxy or
  ingress in front of the dashboard may log the URL itself; check what yours writes down.
- The token list never shows the secret again.

Anyone who has the URL can deploy that service, so keep it as secret as a token.

### Rotate a token

Tokens do not rotate in place. Create a new one, put it where the old one was used, check that `Last used` moves on the
new one, and then revoke the old one. The token list shows when each was created, who created it, when it was last used
and when it expires. The "last used" time is written at most once every 10 minutes, so it can lag.

### Revoke a token

Open **Settings** → **Tokens** (or the project's **Tokens**), choose the menu on the token, and choose **Revoke**, then
**Revoke token**. Anything using it stops working immediately. This cannot be undone.

```bash title="Revoke a token"
curl -fsS -b cookies.txt -X DELETE https://geass.example.com/api/v1/tokens/1a2b3c4d \
  -H "Origin: https://geass.example.com"
```

Deleting a project revokes its project tokens, and removing an environment revokes the tokens for that environment.

### How tokens are stored

Records live in the Secret `geass-api-tokens` in `geass-system`, one key per token ID. A record holds the name, scope,
project, environment, role, who created it, when, when it expires, when it was last used and the SHA-256 hash of the
secret part. The token itself is never stored, so Geass cannot show it again. Creating and revoking a token write a line
to the operator log: `Created API token` with the ID, scope, project, environment and creator, and `Revoked API token`
with the ID. Approving a command-line sign-in writes `Approved a command-line sign-in` with the token ID, role, client name and
approver instead of the first of these.

## Sign in the command line

`geass login` signs in without a password and without you pasting a token. It asks Geass for a short code, shows it, and waits while
an admin approves it in the dashboard. The token it is then handed is saved on your machine. Programs that are not the `geass`
command line can use the same flow: see [Sign in from a program](/docs/reference/rest-api/#sign-in-from-a-program).

1. Run `geass login --url https://geass.example.com`. It prints an address and a code such as `BCDF-GHJK`, opens the page in your
   browser when it runs in a terminal (`--no-browser` prints the address only), and waits up to 10 minutes.
2. In the browser, sign in to the dashboard as an admin if you are not. The page, **Sign in the command line**, is at `/device`; the
   **Code** field is filled in when you arrive from the link.
3. Check that the code on the page is the one your terminal shows. The page names the program that is asking.
4. Choose the **Access**: **Read only, including logs (recommended)**, **Read only, without logs** or **Full access**. Choose when it
   **Expires**: **7 days**, **30 days** (the default) or **90 days**.
5. Choose **Approve**. The page says "Approved. You can close this page: the command line is signed in." Choose **Refuse** to turn the
   request down.

The token is a normal workspace token. It appears in **Settings** → **Tokens** named `CLI: geass CLI on <host name>`, created by the
admin who approved it, and you revoke it there like any other. A viewer who opens the page sees "Only an admin can approve this".
Approving needs a signed-in admin session, not a token, so a leaked token cannot mint more tokens that outlive it.

For a machine with no one to approve a code, create a token under **Settings** → **Tokens** and give it to the command line with
`geass login --token-stdin`, or set `GEASS_URL` and `GEASS_TOKEN`. See [Command line](/docs/guides/command-line/).

:::caution
Approve only a code you started yourself. The program that asked chooses the name the page shows, and whoever holds the device code
receives the token, so an attacker who starts a sign-in and gets you to approve its code is handed a token with the access you chose.
Compare the code character by character, keep the access at the recommended read-only level unless the program needs to change things, and
choose a short expiry.
:::

`geass logout` forgets the saved token on your machine only. The token stays valid until it expires: revoke it in **Settings** →
**Tokens** to end it now.

## CI examples

The examples use a project token in `GEASS_TOKEN` and `https://geass.example.com` as the dashboard address. Replace
`storefront` with a service's resource name (`GET /api/v1/token/services` lists them). In CI you can also install the
[command line](/docs/guides/command-line/) and set `GEASS_URL` and `GEASS_TOKEN`; the examples below use `curl`.

List the services the token can reach:

```bash title="List services"
curl -fsS -H "Authorization: Bearer $GEASS_TOKEN" https://geass.example.com/api/v1/token/services
```

```json title="Response"
{
  "project": "proj-1a2b3c4d",
  "environment": "production",
  "services": [
    { "id": "storefront", "name": "storefront", "kind": "service", "source": "image", "ready": true },
    { "id": "orders-db", "name": "orders-db", "kind": "database", "source": "postgres", "ready": true }
  ]
}
```

Point an image-based service at a new image and deploy it in one call:

```bash title="Ship a new image"
curl -fsS -X PATCH -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"image\": \"ghcr.io/acme/storefront:$GITHUB_SHA\", \"deploy\": true}" \
  https://geass.example.com/api/v1/apps/storefront
```

The response reports `applied` (`deployed`), and the pending state. Without `"deploy": true`, the change is saved as a pending
change and `POST /api/v1/token/deploy` ships it later. A service built from a repository or written as a function ignores
`image`.

Set a secret and deploy it:

```bash title="Set a secret"
curl -fsS -X POST -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"key\": \"STRIPE_API_KEY\", \"value\": \"$STRIPE_API_KEY\"}" \
  https://geass.example.com/api/v1/apps/storefront/secrets/set

curl -fsS -X POST -H "Authorization: Bearer $GEASS_TOKEN" https://geass.example.com/api/v1/token/deploy
```

Scale a service, and read its logs:

```bash title="Scale and read logs"
curl -fsS -X POST -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Content-Type: application/json" -d '{"replicas": 3}' \
  https://geass.example.com/api/v1/apps/storefront/scale

curl -fsS -H "Authorization: Bearer $GEASS_TOKEN" https://geass.example.com/api/v1/apps/storefront/logs
```

Check a service from a monitoring job with an observer workspace token, which can read status and logs but change nothing:

```bash title="Read the status with an observer token"
curl -fsS -H "Authorization: Bearer $GEASS_TOKEN" https://geass.example.com/api/v1/apps/storefront/status
```

A GitHub Actions job that deploys after the image is pushed. Store the token as a repository secret named `GEASS_TOKEN`:

```yaml title=".github/workflows/deploy.yml"
name: deploy
on:
  push:
    branches: [main]
jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - name: Update the image and deploy
        env:
          GEASS_TOKEN: ${{ secrets.GEASS_TOKEN }}
        run: |
          curl -fsS -X PATCH \
            -H "Authorization: Bearer $GEASS_TOKEN" \
            -H "Content-Type: application/json" \
            -d "{\"image\": \"ghcr.io/acme/storefront:${GITHUB_SHA}\", \"deploy\": true}" \
            https://geass.example.com/api/v1/apps/storefront
```

## Troubleshooting

**`401` with `invalid or expired API token`.** The token was revoked, has expired, was copied incompletely, or belongs to a
cluster whose Secret `geass-api-tokens` was removed. The format is `gsw_` or `gsp_`, an 8-character ID and a
40-character secret. Create a new token.

**`403` with `this token cannot access that endpoint`.** A project token called something outside its list. Use a workspace
token, or do the action in the dashboard.

**`403` with `viewer role cannot access this`.** The user or workspace token cannot do this. A viewer cannot change anything or read
logs; an observer cannot change anything. Use an `admin` identity.

**`403` with `only project tokens can use this endpoint`.** A workspace token called `/api/token/deploy`. Call
`POST /api/projects/{project}/deploy` with `environment` instead.

**`403` with `API tokens can only be managed from the dashboard by an admin`.** A token tried to create, list or revoke a token, or to
approve a device code. Do it from a signed-in admin session.

**`404` with `app not found` for a service that exists.** The service belongs to another project or environment than the
project token. Check `GET /api/v1/token/services`.

**`415` with `send form-encoded data`.** A project token sent a body that is not form-encoded to a dashboard route (`/api/...`). Use
`--data-urlencode`, or call the same route under `/api/v1` with a JSON body.

**`400` with `request origin could not be verified`.** A cookie-authenticated write has no `Origin` or `Referer` header
matching the dashboard host. Browsers add one. For scripts, use a token instead.

**`400` with `too many API tokens; revoke some before creating more`.** The 200-token limit is reached. Deploy hooks and approving a device code count
too.

**`geass login` says the sign-in was not approved in time.** Nobody approved the code within 10 minutes, or the operator restarted
(pending sign-ins are kept in memory). Run it again.

**The approval page says "No sign-in request has that code".** The code expired, was already used or refused, or was mistyped. Run
`geass login` again and use the new code.

**`429` with `too many sign-in requests, try again in a minute`.** One address started more than 10 sign-ins in a minute, or 100 are
waiting. Wait a minute.

**Sign-in succeeds but you are signed out at once.** The cookie is `Secure` and you reached the dashboard over plain HTTP at a
non-loopback address. Use HTTPS or `localhost`.

**Nobody can sign in: "dashboard authentication is unavailable".** The Secret has no usable user, has duplicate usernames,
still has only `CHANGE_ME`, or its `users` value is not valid JSON. Fix `users`.

**A user is locked out.** Wait one minute after five failed attempts from your address (or from anywhere, 25 for the username, or five when clients cannot be told apart). A separate limit of 20 failed sign-ins a minute applies to each client, whichever name it tries.

**A deploy hook answers `404` `hook not found`.** The URL is wrong or incomplete, the hook was deleted or has expired, or its service,
environment or project was deleted. It is the same answer for all of these. Make a new hook in the service's settings.

**A deploy hook answers `405`.** It only accepts `POST`.

**A deploy hook answers `409`.** The service is built from a repository and has no build yet, was never deployed, or pins a commit. Deploy it
once from the dashboard, or unpin the commit.

**A deploy hook answers `429`.** Either the hook was called less than 30 seconds ago (the `Retry-After` header says how long to wait), or the
client address missed too many hook URLs in a minute. Wait and call it again.

**A deploy hook answers `401` when used as `Authorization: Bearer`.** A hook is not a credential for the API. Use a project token for that.

## Related

- [Sign in](/docs/getting-started/sign-in/)
- [Command line](/docs/guides/command-line/)
- [MCP server](/docs/guides/mcp-server/)
- [REST API](/docs/reference/rest-api/)
- [Security](/docs/operations/security/)
- [Projects and environments](/docs/guides/projects-and-environments/)
- [Configuration](/docs/reference/configuration/)
