---
title: "Users and API tokens"
description: "Dashboard users and roles, and API tokens and deploy hooks for scripts, CI, the CLI, and agents: create, scope, approve a device code, rotate, and revoke."
---

Geass has two ways to sign in:

- People use the dashboard with a username and password. Each user is an **admin** or a **viewer**.
- Programs use API tokens. A workspace token acts as an admin, an observer, or a viewer. A project token can deploy
  and edit variables in one environment of one project.

The [command line](/docs/guides/command-line/) gets its token without a password. The command line shows a code, and
an admin approves the code in the dashboard. A tool that can call a URL but cannot send a header uses a deploy hook.
A deploy hook is a URL that deploys one service. This page covers users, tokens, deploy hooks, and that approval. It
ends with `curl` examples for CI.

Roles are global. Users have no permissions for each project. By default, users come from a Kubernetes Secret. That
Secret has no invitations, no sign-in by email, and no organizations. To get those features, sign people in through an
identity provider, for example Better Auth. The identity provider owns the people, and Geass takes their role from its
token. See [Sign in with an identity provider](/docs/guides/external-sign-in/). Tokens and deploy hooks work the same
in both cases.

## Roles

| Role | Held by | Can do |
| --- | --- | --- |
| `admin` | Dashboard users, and workspace tokens (**Full access**) | Everything in the dashboard and the API: create, change, and delete resources, deploy, open terminals, and manage settings and connections. Only a signed-in admin, not a token, manages tokens and approves device codes. |
| `observer` | Workspace tokens only (**Read only, with logs**) | Read everything that a viewer can read, and also logs, pods, deploy logs, and variable names. Cannot change anything. The role for a monitoring script or an AI agent that must only watch. |
| `viewer` | Dashboard users, and workspace tokens (**Read only**) | Read the state of the platform. Cannot change anything, and cannot read anything sensitive. See the list that follows. |

If a viewer calls a write endpoint or a sensitive read, Geass answers `403` with
`{"error": "viewer role cannot access this"}`. An observer gets the same answer for any write.

A viewer can still read the overview that the dashboard loads. Geass removes from that overview all data that could
leak a secret or show your setup. A viewer does not get these items:

- Environment variable values, secret references, config data, and `envFrom` sources of services.
- Service host settings, repository names and commits, container pull-secret names, and function source code. The
  status of a service still has its public URL.
- Project shared variables, webhooks and delivery records, the GitHub link, and the backup storage choice.
- Database engine, version, host, port, user, and connection Secret names, and bucket names, regions, and endpoints.
- Cloud connections, the platform configuration, and builds.
- Service logs, deploy logs, runtime details, variables, terminals, project logs (live and history), the template
  list, GitHub repositories, GitHub settings, PlanetScale cluster sizes, and the token list.

Viewers can see service names and status, deployment history, usage, metrics, and cluster events. They can also read
`GET /api/v1/apps/{name}/status`. This response includes the phase, failure reason, and source revision of the latest
build.

An observer gets the same redacted overview as a viewer. An observer also gets the reads in the last item of the list,
except the token list and terminals. An observer cannot create a terminal session, because that is a write. Know these
two facts about what an observer reads:

- The variables of a service come back as names for secrets, but **plain configuration comes back with its values**.
  Keep all sensitive data in a secret, not in plain configuration.
- Logs can contain all that your app prints, including secrets that it must not print.

Dashboard users are `admin` or `viewer`. `observer` is a token role. Geass drops a user whose `role` is `observer`.
See the rules in the next section.

## Create and change users

The dashboard has no screen for users. Users are in the Secret `geass-dashboard-auth` in the `geass-system`
namespace, under the key `users`, as a JSON array.

```json title="users"
[
  { "username": "admin", "password": "a-long-passphrase", "role": "admin" },
  { "username": "maria", "password": "another-long-passphrase", "role": "viewer" }
]
```

Geass applies these rules when it reads the list:

- `role` is `admin` or `viewer`. An empty role means `viewer`. Any other value, `observer` included, drops that user.
- Geass compares usernames without case. If two users differ only by case, the whole list is invalid. Nobody can sign
  in until you fix the list.
- Geass ignores a user with an empty password or the password `CHANGE_ME`. `CHANGE_ME` is the placeholder in the sample
  manifest, and Geass refuses it as a login. If no valid user is left, sign-in fails with "dashboard authentication is
  unavailable".
- Geass does not check the minimum length or the complexity of a password. Use a long passphrase.

You can write a plain password. The next time that Geass reads the Secret, it replaces the password with a bcrypt hash
and saves the Secret again. The first sign-in attempt causes this read. After that, the Secret holds only hashes.

### Add the first admin

The installer does not create a login. Before you open the dashboard, create the Secret yourself:

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

When the Secret has no users, the operator also accepts users from environment variables:

- `GEASS_DASHBOARD_PASSWORD`, for one admin. `GEASS_DASHBOARD_USERNAME` gives the name, and the default is `admin`.
- `GEASS_DASHBOARD_USERS`, as a JSON array like the one in the previous example, or as `username:password:role`
  entries separated by commas.

When the Secret has users, the Secret has priority. On first use, Geass copies the hashed environment users into the
Secret. Thus, if you change the variables later, the change has no effect. See
[Configuration](/docs/reference/configuration/).

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
- **Change a role** by editing `role`. The change applies to sessions that are already open.
- **Change a password** by writing the new plain password over the hash. Sessions that are already open stay valid
  until they expire after 12 hours, or until the user signs out.

Tokens that an admin created or approved are not tied to the account of that admin. If you remove the user, Geass
does not revoke these tokens. Revoke them under **Settings** → **Tokens**.

### Sessions and lockout

When you sign in, Geass sets the cookie `__Host-geass_session`. The cookie is HTTP only and `SameSite=Lax`. It is
valid for 12 hours, and use does not extend it.

The cookie is marked `Secure`, with one exception. If the request is plain HTTP to a loopback address (`localhost`,
`127.0.0.1`, or `::1`), the cookie is `geass_session` and not `Secure`. A browser does not send a `Secure` cookie over
plain HTTP. Thus, open the dashboard over HTTPS, or through `kubectl port-forward` to `localhost`.

The `__Host-` prefix means that the browser accepts the cookie only from the own host of the dashboard, with no
`Domain`. Thus, a service on a sibling subdomain of yours cannot plant a cookie of the same name for the whole domain.
An update from a version that used the plain name signs everyone out one time.

When a user signs out, Geass ends all sessions of that user, in all browsers. To sign everyone out, delete the
`session-key` key from the Secret. Geass makes a new key, and all old cookies stop working.

Geass uses these sign-in limits:

- Five failed sign-ins for one username from one client address in one minute lock that username for that address
  for one minute. Thus, Geass shuts out a person who continues to guess. The owner of the account, who signs in from
  a different address, is not shut out.
- 25 failures for one username in one minute from all clients together lock the username for everyone for one minute.
- If Geass cannot tell clients apart, five failures in one minute lock the username for everyone for one minute.
- A client that fails 20 sign-ins in one minute is also refused for one minute, whatever username it uses.

While a username is locked, the reply is the same `invalid username or password`.

Geass stores the count for a username in the Secret key `login-lockouts`, so the count stays after an operator
restart. Geass keeps the count for a username and an address in memory. Only your configured users get a record in
the Secret. Geass drops a record when its minute is over. Geass keeps the count for each client in memory too.

If a connection comes from a public address, Geass takes the client address from the connection. If the connection
comes from a private address, Geass reads `X-Forwarded-For` from the right. All connections through the ingress of the
cluster or a Cloudflare Tunnel come from a private address. Geass uses the right-most address that is public, because
the nearest proxy wrote it. Geass does not believe the addresses that a client put before it.

A caller inside the cluster can write that header itself and select its address. With this, the caller can use the
allowance of a different client. But the caller can never have more than 25 failed sign-ins for a username in one
minute in total.

## API tokens

With an API token, a script can call the [REST API](/docs/reference/rest-api/) without a browser. Geass shows the token
one time, when you create it. Geass stores only a hash.

A token looks like `gsw_1a2b3c4d_` followed by 40 hexadecimal characters:

- The prefix tells the kind.
- The next part is a public identifier that Geass uses to find the token.
- The rest is the secret.

A third kind, the [deploy hook](#deploy-hooks) (`gsh_`), is a URL, not a credential for the API.

| | Workspace token | Project token |
| --- | --- | --- |
| Prefix | `gsw_` | `gsp_` |
| Acts as | A dashboard user, across all projects. When [organizations are on](/docs/guides/organizations-and-workspaces/#tokens) and the admin of an organization made the token, across all projects of that organization. | A fixed list of actions on one environment of one project |
| Access | `admin` (**Full access**), `observer` (**Read only, with logs**), or `viewer` (**Read only**) | Fixed list, see below |
| Use when | A script manages many things, or you need only to read | CI deploys one service and edits its variables |

If a script needs only to deploy, use a project token. If a script or an agent needs only to watch, use an observer
token.

### Create a token

You must be signed in to the dashboard as an admin. Tokens cannot create, list, or revoke tokens, not even an admin
token. Geass answers `403`: "API tokens can only be managed from the dashboard by an admin".

1. Open the tokens page:
   - For a workspace token, open **Settings** → **Tokens**.
   - For a project token, open **Settings** → **Tokens** of the project.
2. Click **New token**.
3. Enter a **Name** that tells where you use the token, for example `github-actions`. The maximum is 60 characters.
4. Set the access:
   - For a workspace token, select the **Access**: **Read only** (the default), **Read only, with logs**, or
     **Full access**.
   - For a project token, select the **Environment** that the token can change.
5. Select an **Expiration**: **30 days**, **90 days**, **1 year**, or **No expiration**. The default is 90 days.
6. Click **Create token**.
7. Copy the token from **Your new token**, and store it in your secret manager. Geass shows it only one time.

The API accepts all lifetimes from 1 to 3650 days. The API treats a missing `expiresInDays`, or `0`, as no expiration.
A project token needs a project and an environment that exist. A maximum of 600 tokens, deploy hooks included, can
exist at the same time. A maximum of 100 of them can be for one
[organization](/docs/guides/organizations-and-workspaces/#tokens).

To create a token with the API, use a dashboard session. A token cannot do this. Sign in first and keep the cookie:

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

For a workspace token for a monitoring script, send
`{"name": "monitoring", "scope": "workspace", "role": "observer", "expiresInDays": 90}`. The response has the header
`Cache-Control: no-store`. A project token reports `role: "admin"`, but its fixed list of actions limits it.

### Use a token

Send the token as a Bearer token on each request:

```bash title="Call the API with a token"
export GEASS_TOKEN=gsw_1a2b3c4d_0123456789abcdef0123456789abcdef01234567
curl -fsS -H "Authorization: Bearer $GEASS_TOKEN" https://geass.example.com/api/v1/me
```

`GET /api/v1/me` tells who the token is and what it can do. The response gives these fields:

- Its role.
- `canMutate`.
- `canReadSensitive`, which is true for an admin or an observer.
- Its scope, project, environment, and expiry.

`GET /api/token` shows the record of the token: its name, scope, project, environment, role, and expiry.

If you send a Bearer header with a bad, expired, or revoked token, Geass answers `401` with
`invalid or expired API token`. Geass does not then try a cookie.

Calls that use a token skip the same-origin check that protects browser sessions. The reason is that a browser never
adds a Bearer header automatically. Send the requests as follows:

- Under `/api/v1`, send writes as JSON.
- The older dashboard routes under `/api` read form fields, not JSON. Send those requests as
  `application/x-www-form-urlencoded`, which `curl --data-urlencode` does.

If a project token sends a different content type to a dashboard route, Geass answers `415`. For all other callers,
Geass does not read a JSON body, and the fields arrive empty. See [REST API](/docs/reference/rest-api/).

### Use the identity provider's access token

This section applies when you use [sign-in through an identity provider](/docs/guides/external-sign-in/) and
`spec.auth.oidc.accessTokens` is turned on. In that case, a program that is signed in at the provider can send
**the access token the provider issued to the person** in place of a Geass token, as `Authorization: Bearer <jwt>`.
The program can send it to all that an API token can reach:

- The REST API.
- The stream of events and the log streams.
- The WebSocket of a terminal, as the subprotocol `geass.bearer.<jwt>`.

Geass creates nothing in **Settings** → **Tokens**, and keeps nothing long-lived. The person is the actor, with the
role and organizations that their claims map to. Geass records the address of the person as the actor, or their
subject.

The token must be a JWT access token with the audience of this cluster. The documentation of the provider and the
[setup guide](/docs/guides/external-sign-in/#access-tokens-as-a-bearer-credential) describe this. Geass refuses the
call in these cases:

- With `401`, for all tokens that are not such an access token. An ID token is one example.
- With `403`, for a person whose claims map to no access.

Like an API token, this access token cannot manage sign-in, make tokens, or approve a device code. Those actions need
the session of a signed-in admin. A stream or terminal that the access token opened ends when the token expires. This
feature is off by default.

### What a workspace token can do

- An `admin` workspace token can call all endpoints that a signed-in admin can call, with these exceptions:
  - The endpoints that manage tokens or approve device codes. They need the session of a signed-in admin.
  - `POST /api/token/deploy`, which is for project tokens.

  An admin token can also open a terminal. Thus, a leaked admin token is as serious as an admin password.
- An `observer` workspace token can read what a viewer can read, with the same redactions. It can also do the reads in
  [Roles](#roles). Geass refuses all writes.
- A `viewer` workspace token can read what a viewer can read, with the same redactions.

### What a project token can do

A project token can use only these endpoints, and only for services in its own project and environment:

| Endpoint | Meaning |
| --- | --- |
| `GET /api/token`, `GET /api/token/services` | Describe the token. List the services and databases that it can reach. |
| `POST /api/token/deploy` | Deploy all pending changes in the environment of the token |
| `GET /api/apps/{name}/logs`, `/runtime`, `/variables`, `/metrics` | Read the logs, pods, variables, and metrics of a service. Variables show secrets as names only and plain configuration with its values. |
| `GET /api/apps/{name}/deployments/{id}/logs` | Read the logs of one deploy |
| `POST /api/apps/{name}/deploy`, `/rollback`, `/scale`, `/retry-provision`, `/update` | Deploy, roll back, scale, retry, and edit settings |
| `POST /api/apps/{name}/build`, `/build/cancel` | Start or cancel a repository build |
| `POST /api/apps/{name}/config/set`, `/config/delete`, `/config/raw` | Edit plain configuration |
| `POST /api/apps/{name}/secrets/set`, `/secrets/delete`, `/env/delete` | Edit secrets and environment references |

On `/api/v1`, the same routes work with JSON bodies. `PATCH /api/v1/apps/{name}` is `update`. On `/api/v1`, a project
token can also call these routes:

- `GET /api/v1/me`, `/version`, `/apps` (only its own environment), `/apps/{name}`, `/apps/{name}/status`,
  `/apps/{name}/deployments`, and `/apps/{name}/logs/stream`.
- `GET /api/v1/token/services` and `POST /api/v1/token/deploy`.

Geass refuses all other requests with `403` and `this token cannot access that endpoint`. These include:

- Requests that create or delete resources, databases, and buckets.
- Requests for terminals, networking and domains, settings, projects, users, and connections.
- Requests that read project or cluster logs.

A request for a service in a different project or environment gets `404` and `app not found`. This is the same answer
as for a service that does not exist, so names do not leak. If a form field `project` or `environment` names a
different project, Geass refuses it with `this token is limited to <project> / <environment>`.

A project token cannot read the overview of the dashboard (`/api/bootstrap`). But on `/api/v1`,
`GET /api/v1/apps/{name}/deployments` lists the deployment revisions that `POST /api/v1/apps/{name}/rollback` needs.
Thus, a project token can roll back there. On the older routes, use a workspace token for rollbacks.

### Deploy with a project token

CI calls `POST /api/token/deploy` or `POST /api/v1/token/deploy`. The call deploys all items with pending changes in
the environment of the token. See [Projects and environments](/docs/guides/projects-and-environments/).

```bash title="Deploy pending changes"
curl -fsS -X POST -H "Authorization: Bearer $GEASS_TOKEN" https://geass.example.com/api/v1/token/deploy
```

```json title="Response"
{ "ok": true, "deployed": 2 }
```

`deployed` counts the services, databases, and buckets that had changes. A workspace token gets `403` here. A
workspace token uses `POST /api/projects/{project}/deploy` with an `environment` field.

Geass deploys services first, then databases, then buckets. If the cluster does not have room for one of the services
or databases, the call stops there. It fails with `400`, and the message gives the capacity shortfall. All items that
the call deployed before that point stay deployed. `GET /api/v1/token/services` shows which services still have
`pendingChanges`.

### Deploy hooks

A deploy hook is a URL that deploys one service when something calls it with `POST`. Use a deploy hook from a tool
that can call a URL but cannot send a header. Examples are a registry that notifies when an image is pushed, a
scheduler, a CMS, or a button on a page. If a tool can send `Authorization: Bearer`, use a project token.

```bash title="Call a deploy hook"
curl -X POST https://geass.example.com/hooks/deploy/gsh_1a2b3c4d_0123456789abcdef0123456789abcdef01234567
```

```json title="Response"
{ "ok": true, "action": "build", "service": "storefront" }
```

What the hook does depends on the service:

| The service is | `action` | What happens |
| --- | --- | --- |
| Built from a repository | `build` | Geass starts its current build again from the head of the branch. The service rolls out when the build succeeds. A service with no build yet, a service that was never deployed, or a service that pins a commit answers `409`. A push to the branch also skips these services. |
| An image or a function | `deploy` | Geass deploys its pending changes. This is what **Deploy changes** and **Redeploy** do in the dashboard. Thus, changes that someone saved but did not deploy also go out, and Geass deploys a draft for the first time. The same capacity check applies: a cluster with no room answers `400` and deploys nothing. |

To make a deploy hook, do these steps in the settings of the service:

1. Open the service.
2. Select **Settings** → **Deploy**, then **Deploy hook**.
3. Click **Create deploy hook**. Geass shows the URL one time, with a copy button.

A service has one hook. To get a new URL, delete the hook and make a new one. The old URL stops working immediately.

A hook appears in **Settings** → **Tokens**, and in **Tokens** of the project, as
`Deploy hook · <service> · <environment>`. There, you can see when it was last called, and you can revoke it. The API
makes a hook with `scope: "service"`, a `project`, an `environment`, and an `app`. Only a signed-in admin can do this.

The secret is in the URL, because that is the only place where such a tool can put it. Thus, Geass makes the URL
harmless if it leaks into the log of a proxy:

- The hook is good for one service and one action. If you use it as a Bearer token, all API endpoints refuse it
  (`401`), whatever the request.
- Only `POST` does something. `GET`, `HEAD`, `PUT`, and `DELETE` answer `405`. Thus, a link preview or a crawler
  cannot deploy anything.
- All these hooks answer `404` `hook not found`: a hook that does not exist, is revoked, has expired, or belongs to a
  service that is gone. Thus, a caller cannot tell which case applies. A token of a different kind in the URL also
  gets `404`.
- When you delete the service, its environment, or its project, Geass revokes its hook.
- A hook that was called less than 30 seconds ago answers `429` with a `Retry-After` header and does nothing. Thus,
  a URL that leaked into a loop cannot stop the builds of a service from finishing, or fill its deployment history. A
  call that did nothing does not start the wait. Examples are a call that found no room in the cluster, or a `409`.
- Geass slows down a client that continues to guess, as it does for a client that guesses passwords. After 20 misses
  in one minute, even a good URL gets `429` for one minute. This limit applies to clients that connect from a public
  address, not through a proxy or tunnel.
- The request log of the operator shows the path as `/hooks/deploy/<redacted>`. Thus, a person who reads the log does
  not get a hook. A reverse proxy or ingress in front of the dashboard might log the URL itself. Check what yours
  writes.
- The token list never shows the secret again.

Anyone who has the URL can deploy that service. Keep the URL as secret as a token.

### Rotate a token

Geass does not rotate tokens in place. To rotate a token, do these steps:

1. Create a new token.
2. Put the new token where you used the old one.
3. Make sure that `Last used` changes on the new token.
4. Revoke the old token.

The token list shows when Geass created each token, who created it, when it was last used, and when it expires. Geass
writes the "last used" time a maximum of one time each 10 minutes. Thus, that time can be late.

### Revoke a token

1. Open **Settings** → **Tokens**, or **Tokens** of the project.
2. Click the menu on the token.
3. Click **Revoke**.
4. Click **Revoke token**. All clients that use the token stop working immediately. You cannot undo this action.

```bash title="Revoke a token"
curl -fsS -b cookies.txt -X DELETE https://geass.example.com/api/v1/tokens/1a2b3c4d \
  -H "Origin: https://geass.example.com"
```

When you delete a project, Geass revokes its project tokens. When you remove an environment, Geass revokes the tokens
for that environment.

### How tokens are stored

Geass keeps records in the Secret `geass-api-tokens` in `geass-system`, with one key for each token ID. A record holds
these items:

- The name, scope, project, environment, and role.
- Who created the token, and when.
- When the token expires, and when it was last used.
- The SHA-256 hash of the secret part.

Geass never stores the token itself, so Geass cannot show it again.

When you create or revoke a token, Geass writes a line to the operator log:

- `Created API token`, with the ID, scope, project, environment, and creator.
- `Revoked API token`, with the ID.

When an admin approves a command-line sign-in, Geass writes `Approved a command-line sign-in`, not
`Created API token`. The line has the token ID, role, client name, and approver.

## Sign in the command line

`geass login` signs in without a password, and you do not paste a token. The command asks Geass for a short code,
shows it, and waits while an admin approves it in the dashboard. Geass then gives the command a token, and the command
saves the token on your machine. Programs other than the `geass` command line can use the same flow. See
[Sign in from a program](/docs/reference/rest-api/#sign-in-from-a-program).

1. Run `geass login --url https://geass.example.com`. The command prints an address and a code, for example
   `BCDF-GHJK`. When it runs in a terminal, it opens the page in your browser. `--no-browser` prints only the
   address. The command waits up to 10 minutes.
2. In the browser, if you are not signed in, sign in to the dashboard as an admin. The page,
   **Sign in the command line**, is at `/device`. When you arrive from the link, the **Code** field is already filled
   in.
3. Make sure that the code on the page is the code that your terminal shows. The page names the program that asks.
4. Select the **Access**: **Read only, including logs (recommended)**, **Read only, without logs**, or
   **Full access**.
5. Select when it **Expires**: **7 days**, **30 days** (the default), or **90 days**.
6. Click **Approve**. The page says "Approved. You can close this page: the command line is signed in." To refuse the
   request, click **Refuse**.

The token is a usual workspace token. It appears in **Settings** → **Tokens** with the name
`CLI: geass CLI on <host name>`, created by the admin who approved it. You revoke it there like any other token. A
viewer who opens the page sees "Only an admin can approve this". To approve, you need a signed-in admin session, not
a token. Thus, a leaked token cannot make more tokens that live longer than it.

For a machine with no person to approve a code, create a token under **Settings** → **Tokens**. Give it to the
command line with `geass login --token-stdin`, or set `GEASS_URL` and `GEASS_TOKEN`. See
[Command line](/docs/guides/command-line/).

:::caution
Approve only a code that you started yourself. The program that asks selects the name that the page shows. The holder
of the device code receives the token. Thus, an attacker can start a sign-in and get you to approve its code. The
attacker then gets a token with the access that you selected. To prevent this:

- Compare the code character by character.
- Keep the access at the recommended read-only level, unless the program must change things.
- Select a short expiry.
:::

`geass logout` removes the saved token only on your machine. The token stays valid until it expires. To end it
immediately, revoke it in **Settings** → **Tokens**.

## CI examples

The examples use a project token in `GEASS_TOKEN`, and `https://geass.example.com` as the dashboard address. Replace
`storefront` with the resource name of a service. `GET /api/v1/token/services` lists these names. In CI, you can also
install the [command line](/docs/guides/command-line/) and set `GEASS_URL` and `GEASS_TOKEN`. The examples that follow
use `curl`.

List the services that the token can reach:

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

Point an image-based service at a new image, and deploy it in one call:

```bash title="Ship a new image"
curl -fsS -X PATCH -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"image\": \"ghcr.io/acme/storefront:$GITHUB_SHA\", \"deploy\": true}" \
  https://geass.example.com/api/v1/apps/storefront
```

The response reports `applied` (`deployed`) and the pending state. Without `"deploy": true`, Geass saves the change as
a pending change, and `POST /api/v1/token/deploy` ships it later. A service that is built from a repository or written
as a function ignores `image`.

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

To check a service from a monitoring job, use an observer workspace token. An observer token can read status and logs,
but it cannot change anything:

```bash title="Read the status with an observer token"
curl -fsS -H "Authorization: Bearer $GEASS_TOKEN" https://geass.example.com/api/v1/apps/storefront/status
```

This GitHub Actions job deploys after the image is pushed. Store the token as a repository secret named
`GEASS_TOKEN`:

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

**`401` with `invalid or expired API token`.** One of these is the cause:

- Someone revoked the token.
- The token expired.
- The token was copied incompletely.
- The token belongs to a cluster whose Secret `geass-api-tokens` was removed.

The format is `gsw_` or `gsp_`, an 8-character ID, and a 40-character secret. Create a new token.

**`403` with `this token cannot access that endpoint`.** A project token called an endpoint that is not on its list.
Use a workspace token, or do the action in the dashboard.

**`403` with `viewer role cannot access this`.** The user or workspace token cannot do this action. A viewer cannot
change anything or read logs. An observer cannot change anything. Use an `admin` identity.

**`403` with `only project tokens can use this endpoint`.** A workspace token called `/api/token/deploy`. Call
`POST /api/projects/{project}/deploy` with `environment`.

**`403` with `API tokens can only be managed from the dashboard by an admin`.** A token tried to create, list, or
revoke a token, or to approve a device code. Do the action from a signed-in admin session.

**`404` with `app not found` for a service that exists.** The service belongs to a different project or environment
than the project token. Check `GET /api/v1/token/services`.

**`415` with `send form-encoded data`.** A project token sent a body that is not form-encoded to a dashboard route
(`/api/...`). Use `--data-urlencode`, or call the same route under `/api/v1` with a JSON body.

**`400` with `request origin could not be verified`.** A cookie-authenticated write has no `Origin` or `Referer`
header that matches the dashboard host. Browsers add one. For scripts, use a token.

**`400` with `too many API tokens; revoke some before creating more`.** The limit of 600 tokens is reached, or the
limit of 100 tokens for one organization. Deploy hooks and approved device codes also count.

**`geass login` says the sign-in was not approved in time.** Nobody approved the code in 10 minutes, or the operator
restarted. Geass keeps pending sign-ins in memory. Run the command again.

**The approval page says "No sign-in request has that code".** The code expired, was already used or refused, or was
mistyped. Run `geass login` again and use the new code.

**`429` with `too many sign-in requests, try again in a minute`.** One address started more than 10 sign-ins in one
minute, or 100 sign-ins are waiting. Wait one minute.

**Sign-in succeeds but you are signed out immediately.** The cookie is `Secure`, and you opened the dashboard over plain
HTTP at an address that is not a loopback address. Use HTTPS or `localhost`.

**Nobody can sign in: "dashboard authentication is unavailable".** One of these is the cause:

- The Secret has no usable user.
- The Secret has duplicate usernames.
- The Secret still has only `CHANGE_ME`.
- The `users` value of the Secret is not valid JSON.

Fix `users`.

**A user is locked out.** Wait one minute. These failures cause a lock:

- Five failed attempts for the username from your address in one minute.
- 25 failed attempts for the username, from all addresses, in one minute.
- Five failed attempts for the username in one minute, when Geass cannot tell clients apart.

A separate limit of 20 failed sign-ins in one minute applies to each client, whatever name it tries.

**A deploy hook answers `404` `hook not found`.** One of these is the cause:

- The URL is wrong or incomplete.
- Someone deleted the hook, or the hook expired.
- Someone deleted its service, environment, or project.

The answer is the same for all of these causes. Make a new hook in the settings of the service.

**A deploy hook answers `405`.** The hook accepts only `POST`.

**A deploy hook answers `409`.** The service is built from a repository and has no build yet, was never deployed, or
pins a commit. Deploy the service one time from the dashboard, or unpin the commit.

**A deploy hook answers `429`.** One of these is the cause:

- Something called the hook less than 30 seconds ago. The `Retry-After` header tells how long to wait.
- The client address missed too many hook URLs in one minute.

Wait, and then call the hook again.

**A deploy hook answers `401` when used as `Authorization: Bearer`.** A hook is not a credential for the API. For the
API, use a project token.

## Related

- [Sign in](/docs/getting-started/sign-in/)
- [Command line](/docs/guides/command-line/)
- [MCP server](/docs/guides/mcp-server/)
- [REST API](/docs/reference/rest-api/)
- [Security](/docs/operations/security/)
- [Projects and environments](/docs/guides/projects-and-environments/)
- [Configuration](/docs/reference/configuration/)
