---
title: "Users and API tokens"
description: "Dashboard users and roles, and API tokens and deploy hooks for scripts, CI, the CLI, and agents: create, scope, approve a device code, rotate, and revoke."
---

Geass has two ways to sign in:

- People sign in to the dashboard. Each person is an **admin** or a **viewer**.
- Programs use API tokens. A workspace token acts as an admin, an observer, or a viewer. A project token can deploy
  and edit variables in one environment of one project.

The [command line](/docs/guides/command-line/) gets its token without a password: it shows a code, and an admin
approves the code in the dashboard. A tool that can call a URL but cannot send a header uses a deploy hook, a URL that
deploys one service.

Roles apply to the whole dashboard. There are no permissions for each project. By default, people sign in with a
password that an admin sets. For invitations, sign-in by email, or organizations, sign people in through
an identity provider. See [Sign in with an identity provider](/docs/guides/external-sign-in/). Tokens and deploy hooks
work the same in both cases.

## Roles

| Role | Held by | Can do |
| --- | --- | --- |
| `admin` | People, and workspace tokens with **Full access** | Everything in the dashboard and the API: create, change, and delete resources, deploy, open terminals, and manage settings and connections. Only a signed-in admin, not a token, manages tokens and approves command-line sign-ins. |
| `observer` | Workspace tokens with **Read only, with logs** | Read everything that a viewer can read, and also logs, deploy logs, runtime details, and variable names. Change nothing. Use it for a monitoring script or an AI agent that only watches. |
| `viewer` | People, and workspace tokens with **Read only** | Read the state of the platform. Change nothing, and read nothing sensitive. |

A viewer sees service names and status, deployment history, usage, metrics, and cluster events. A viewer does not see
these items:

- Variable values, secret names, domains, repositories, and the source code of functions.
- Database hosts, users, and connection details, and bucket names and endpoints.
- Connections, platform settings, builds, and the token list.
- Logs, deploy logs, terminals, and GitHub settings.

An observer also sees logs, deploy logs, runtime details, and variables, but not the token list or terminals. Know two
facts about what an observer reads:

- Secrets come back as names only, but **plain configuration comes back with its values**. Keep sensitive values in a
  secret.
- Logs contain all that your app prints, including secrets that it must not print.

If a viewer tries to change something or read something sensitive, Geass answers `403` with
`viewer role cannot access this`. An observer gets the same answer for any change.

## Manage users

An admin manages users under **Settings** → **Users**. Only an admin sees this page.

### Add the first admin

A new install creates the first admin for you. The installer prints the password during the install and again
at the end. See [Sign in to the dashboard](/docs/getting-started/sign-in/).

### Add a user

1. Open **Settings** → **Users**.
2. Click **Add user**.
3. Enter a username. Use letters, digits, and the characters `.`, `_`, `@`, `+`, and `-`. The name has 64 characters
   or fewer.
4. Select a role: **Viewer** or **Admin**.
5. To choose the password, enter 12 to 72 characters. To let Geass make a password, leave the field empty.
6. Click **Add user**.

When Geass makes the password, the dialog shows it one time. Copy the password, and give it to the person. Geass
cannot show it again.

Geass compares usernames without case. You cannot add a name that another user has, even with different letter case.

### Change a role

1. Open **Settings** → **Users**.
2. Click the **⋮** button of the user.
3. Select **Make admin** or **Make viewer**.

The new role applies to open sessions at their next request. The user does not sign in again.

### Reset a password

1. Open **Settings** → **Users**.
2. Click the **⋮** button of the user, and select **Reset password**.
3. Enter a new password, or leave the field empty to let Geass make one.
4. Click **Reset password**.

Geass signs the user out in all browsers, and the old password stops working. If you reset your own password, the
dashboard asks you to sign in again.

### Remove a user

1. Open **Settings** → **Users**.
2. Click the **⋮** button of the user, and select **Remove**.
3. Click **Remove user**.

Geass signs the user out. Geass does not revoke the tokens that the user made or approved. Revoke them under
**Settings** → **Tokens**.

Geass keeps one admin at all times. It refuses to remove the last admin or to make the last admin a viewer. To change
the last admin, first make another user an admin.

### Sign everyone out

To end all sessions, click **Sign out everyone** on **Settings** → **Users**, and confirm. Everyone signs in again,
and that includes you. API tokens keep working.

### Manage users from the command line

The `geass users` commands do the same as the page. Run `geass login` as an admin first. A project token or an
organization token cannot manage users.

A token manages viewers only. It cannot make an admin, make a viewer an admin, or set the password of an admin. Do
these actions in the dashboard.

```bash title="Manage users"
geass users list
geass users add maria --role viewer
geass users set-role sam viewer
geass users reset-password maria
geass users remove maria
geass users sign-out-all
```

Without `--password-file`, `add` and `reset-password` print the password that Geass made, one time. To set a password
that you choose, send it in a file or through standard input:

```bash title="Add a user with a password that you choose"
printf '%s' "$PASSWORD" | geass users add ops --role viewer --password-file -
```

`remove` and `sign-out-all` ask for confirmation. In a script, add `--yes`. See the
[command-line reference](/docs/reference/cli/) and the [REST API](/docs/reference/rest-api/).

### Sessions and lockout

A sign-in lasts 12 hours. Use does not make it longer. When a person clicks **Sign out**, Geass ends all of their
sessions, in all browsers.

Open the dashboard over HTTPS, or on `localhost`. Over plain HTTP at any other address, the browser drops the session
cookie, and you are signed out immediately.

Geass slows down guessing:

- Five failed sign-ins for one username from one address in one minute lock that username for that address for one
  minute. The owner, at a different address, is not locked out.
- 25 failed sign-ins for one username in one minute, from all addresses, lock that username for everyone for one
  minute.
- A client with 20 failed sign-ins in one minute is refused for one minute, whatever username it tries.

While a username is locked, the reply is the same `invalid username or password`.

## API tokens

With an API token, a script can call the [REST API](/docs/reference/rest-api/) without a browser. Geass shows the token
one time, when you create it.

| | Workspace token | Project token |
| --- | --- | --- |
| Prefix | `gsw_` | `gsp_` |
| Acts as | A dashboard user, across all projects. When [organizations are on](/docs/guides/organizations-and-workspaces/#tokens), across the projects of the organization of its maker. | A fixed list of actions in one environment of one project |
| Access | **Full access**, **Read only, with logs**, or **Read only** | A fixed list. See [What a project token can do](#what-a-project-token-can-do). |
| Use when | A script manages many things, or only reads | CI deploys services and edits their variables |

If a script only deploys, use a project token. If a script or an agent only watches, use **Read only, with logs**.

### Create a token

You must sign in to the dashboard as an admin. A token cannot create, list, or revoke tokens, not even a
**Full access** token.

1. Open the token list:
   - For a workspace token, open **Settings** → **Tokens**.
   - For a project token, open the project, and then **Settings** → **Tokens**.
2. Click **New token**.
3. Enter a **Name** that tells where you use the token, for example `github-actions`. The maximum is 60 characters.
4. Set the access:
   - For a workspace token, select the **Access**: **Read only** (the default), **Read only, with logs**, or
     **Full access**.
   - For a project token, select the **Environment** that the token can change.
5. Select an **Expiration**: **30 days**, **90 days** (the default), **1 year**, or **No expiration**.
6. Click **Create token**.
7. Copy the token from **Your new token**, and store it in your secret manager. Geass shows it only one time.

An installation can have at most 600 tokens, deploy hooks and approved command-line sign-ins included. An
[organization](/docs/guides/organizations-and-workspaces/#tokens) can have at most 100.

### Use a token

Send the token as a Bearer token with each request:

```bash title="Call the API with a token"
export GEASS_TOKEN=gsw_1a2b3c4d_0123456789abcdef0123456789abcdef01234567
curl -fsS -H "Authorization: Bearer $GEASS_TOKEN" https://geass.example.com/api/v1/me
```

`GET /api/v1/me` tells who the token is: its role, whether it can change things, its scope, and its expiry.

Under `/api/v1`, send writes as JSON. The older routes under `/api` read form fields. Send those requests with
`curl --data-urlencode`. See [REST API](/docs/reference/rest-api/).

### Use the access token of the identity provider

When people [sign in through an identity provider](/docs/guides/external-sign-in/), a program that is signed in at the
provider can send the access token of the person instead of a Geass token. This feature is off by default. To turn it
on and set it up, see
[Access tokens as a bearer credential](/docs/guides/external-sign-in/#access-tokens-as-a-bearer-credential).

### What a workspace token can do

- A **Full access** token can do all that a signed-in admin can do, except manage tokens and approve command-line
  sign-ins. It can also open a terminal. So a leaked **Full access** token is as serious as an admin password.
- A **Read only, with logs** token reads what an observer reads.
- A **Read only** token reads what a viewer reads.

### What a project token can do

A project token works only for the services of its own project and environment. It can do these tasks:

- List the services and databases that it can reach: `GET /api/v1/token/services`.
- Deploy all pending changes of its environment: `POST /api/v1/token/deploy`.
- Deploy, roll back, scale, and change the settings of a service.
- Start or cancel a build of a service.
- Set and remove variables and secrets.
- Read the logs, deployments, deploy logs, runtime details, variables, status, and metrics of a service. Variables
  show secrets as names only, and plain configuration with its values.

A project token cannot create or delete services, databases, or buckets. It cannot open a terminal, or change
domains, projects, settings, users, or connections. For these requests, Geass answers `403` with
`this token cannot access that endpoint`. For a service in another project or environment, Geass answers `404` with
`app not found`, the same as for a service that does not exist.

### Deploy with a project token

CI calls `POST /api/v1/token/deploy`. The call deploys all services, databases, and buckets with pending changes in
the environment of the token. See [Projects and environments](/docs/guides/projects-and-environments/).

```bash title="Deploy pending changes"
curl -fsS -X POST -H "Authorization: Bearer $GEASS_TOKEN" https://geass.example.com/api/v1/token/deploy
```

```json title="Response"
{ "ok": true, "deployed": 2 }
```

Geass deploys services first, then databases, then buckets. If the cluster does not have room for one item, the call
stops there with `400` and the capacity shortfall. The items deployed before that point stay deployed.

### Deploy hooks

A deploy hook is a URL that deploys one service when something calls it with `POST`. Use a deploy hook from a tool
that can call a URL but cannot send a header. Examples are a registry that sends a notice when you push an image, a
scheduler, or a CMS. If a tool can send `Authorization: Bearer`, use a project token.

To make a deploy hook, do these steps:

1. Open the service.
2. Select **Settings** → **Deploy**.
3. Under **Deploy hook**, click **Create deploy hook**.
4. Copy the URL. Geass shows it only one time.

```bash title="Call a deploy hook"
curl -X POST https://geass.example.com/hooks/deploy/gsh_1a2b3c4d_0123456789abcdef0123456789abcdef01234567
```

What the hook does depends on the service:

- **A service built from a repository**: Geass builds the head of its branch again, and deploys the result. The hook
  answers `409` for a service that has no build yet, was never deployed, or pins a commit.
- **An image or a function**: Geass deploys the pending changes of the service, as **Deploy changes** does in the
  dashboard. If the cluster has no room, the hook answers `400` and deploys nothing.

A service has one hook. To get a new URL, click **Delete hook**, and make a new hook. The old URL stops working
immediately. The hook also shows in **Settings** → **Tokens** as `Deploy hook · <service> · <environment>`. There you
can see when it was last called, and revoke it.

Anyone who has the URL can deploy that service. Keep the URL as secret as a token. Geass limits the harm of a leaked
URL:

- The hook can only deploy its service. The API refuses it as a Bearer token.
- Only `POST` does something. Other methods get `405`, so a link preview cannot deploy.
- A wrong, revoked, or expired hook, or a hook of a deleted service, gets `404` `hook not found`.
- A hook called less than 30 seconds ago answers `429` and does nothing.
- After 20 wrong hook URLs in one minute, a client gets `429` for one minute.
- Geass removes the hook from its own request log. A proxy in front of the dashboard might still log the URL.

When you delete the service, its environment, or its project, Geass revokes its hook.

### Rotate a token

Geass does not rotate a token in place. To rotate a token, do these steps:

1. Create a new token.
2. Put the new token where you used the old token.
3. Make sure that **Last used** changes on the new token. Geass updates it at most every 10 minutes.
4. Revoke the old token.

### Revoke a token

1. Open **Settings** → **Tokens**, or **Settings** → **Tokens** of the project.
2. Open the menu of the token, and click **Revoke**.
3. Click **Revoke token**. Every client that uses the token stops working immediately. You cannot undo this action.

When you delete a project, Geass revokes its project tokens. When you remove an environment, Geass revokes the tokens
of that environment.

## Sign in the command line

`geass login` signs in without a password, and you do not paste a token. The command shows a short code, and waits
while an admin approves the code in the dashboard. Other programs can use the same flow. See
[Sign in from a program](/docs/reference/rest-api/#sign-in-from-a-program).

1. Run `geass login --url https://geass.example.com`. The command prints an address and a code, for example
   `BCDF-GHJK`. It also opens the page in your browser. It waits up to 10 minutes.
2. If the dashboard asks, sign in as an admin. The page **Sign in the command line** opens, with the **Code** filled in.
3. Make sure that the code on the page is the code in your terminal. The page names the program that asks.
4. Select the **Access**: **Read only, including logs (recommended)**, **Read only, without logs**, or
   **Full access**.
5. Select when it **Expires**: **7 days**, **30 days** (the default), or **90 days**.
6. Click **Approve**. To refuse the request, click **Refuse**.

The token is a usual workspace token. It shows in **Settings** → **Tokens** as `CLI: geass CLI on <host name>`. A
viewer who opens the page sees "Only an admin can approve this".

For a machine where nobody can approve a code, create a token under **Settings** → **Tokens**. Give it to
`geass login --token-stdin`, or set `GEASS_URL` and `GEASS_TOKEN`. See [Command line](/docs/guides/command-line/).

:::caution
Approve only a code that you started yourself. Whoever started the request gets the token. An attacker can start a
sign-in and ask you to approve the code. To prevent this, do these things:

- Compare the code character by character.
- Keep the access at the recommended read-only level, unless the program must change things.
- Select a short expiry.
:::

`geass logout` removes the token only from your machine. To end the token, revoke it in **Settings** → **Tokens**.

## CI examples

The examples use a project token in `GEASS_TOKEN`, and `https://geass.example.com` as the dashboard address. Replace
`storefront` with the name of a service. In CI, you can also use the [command line](/docs/guides/command-line/).

Point an image service at a new image, and deploy it in one call:

```bash title="Ship a new image"
curl -fsS -X PATCH -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"image\": \"ghcr.io/acme/storefront:$GITHUB_SHA\", \"deploy\": true}" \
  https://geass.example.com/api/v1/apps/storefront
```

Without `"deploy": true`, Geass saves a pending change, and `POST /api/v1/token/deploy` ships it later. A service
built from a repository, or a function, ignores `image`.

Set a secret, and deploy it:

```bash title="Set a secret"
curl -fsS -X POST -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"key\": \"STRIPE_API_KEY\", \"value\": \"$STRIPE_API_KEY\"}" \
  https://geass.example.com/api/v1/apps/storefront/secrets/set

curl -fsS -X POST -H "Authorization: Bearer $GEASS_TOKEN" https://geass.example.com/api/v1/token/deploy
```

This GitHub Actions job deploys after you push. Store the token as a repository secret named `GEASS_TOKEN`:

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

**`401` with `invalid or expired API token`.** Someone revoked the token, the token expired, or the token was copied
incompletely. Create a new token.

**`403` with `this token cannot access that endpoint`.** A project token called something that is not on its list.
Use a workspace token, or do the action in the dashboard.

**`403` with `viewer role cannot access this`.** The user or token cannot do this action. Use an admin identity.

**`403` with `only project tokens can use this endpoint`.** A workspace token called the token deploy route. Call
`POST /api/projects/{project}/deploy` with an `environment` field.

**`403` with `API tokens can only be managed from the dashboard by an admin`.** A token tried to manage tokens or
approve a sign-in. Do it from the dashboard as an admin.

**`404` with `app not found` for a service that exists.** The service is in another project or environment than the
project token.

**`415` with `send form-encoded data`.** A project token sent JSON to an older `/api/...` route. Use
`--data-urlencode`, or call the same route under `/api/v1`.

**`400` with `request origin could not be verified`.** A script used a dashboard cookie. Use a token.

**`400` with `too many API tokens; revoke some before creating more`.** The limit of 600 tokens, or 100 for one
organization, is reached. Revoke tokens that you do not use.

**`geass login` says that the sign-in was not approved in time.** Nobody approved the code in 10 minutes, or Geass
restarted. Run the command again.

**The approval page says "No sign-in request has that code".** The code expired, was used, or was mistyped. Run
`geass login` again.

**`429` with `too many sign-in requests, try again in a minute`.** One address started more than 10 sign-ins in one
minute, or 100 sign-ins are waiting. Wait one minute.

**You sign in and are signed out immediately.** You opened the dashboard over plain HTTP at an address that is not
`localhost`. Use HTTPS.

**Nobody can sign in: "dashboard authentication is unavailable".** The list of users has no usable user, has duplicate
usernames, has only `CHANGE_ME`, or is not valid JSON. Fix the list. See [Under the hood](#under-the-hood).

**A user is locked out.** Wait one minute.

**A deploy hook answers `404` `hook not found`.** The URL is wrong, or the hook, its service, its environment, or its
project is gone. Make a new hook.

**A deploy hook answers `405`.** Send `POST`.

**A deploy hook answers `409`.** The service is built from a repository, and has no build yet, was never deployed, or
pins a commit. Deploy it one time from the dashboard, or remove the pinned commit.

**A deploy hook answers `429`.** The hook was called less than 30 seconds ago, or the address tried too many wrong
hooks. Wait, and call it again. The `Retry-After` header says how long.

## Under the hood

### The user list

Use this section only when nobody can sign in. For every other task, use **Settings** → **Users**.

The list of users is a JSON array in the Secret `geass-dashboard-auth` in `geass-system`, under the key `users`. The
page and the command line change this key.

```json title="users"
[
  { "username": "admin", "password": "a-bcrypt-hash", "role": "admin" },
  { "username": "maria", "password": "another-bcrypt-hash", "role": "viewer" }
]
```

Geass applies these rules to the list:

- `role` is `admin` or `viewer`. An empty role means `viewer`. Geass ignores a user with any other role.
- Geass compares usernames without case. If two users differ only by case, nobody can sign in until you fix the list.
- Geass ignores a user with an empty password or the placeholder `CHANGE_ME`.

You can write a plain password. Geass replaces it with a hash at the next sign-in attempt.

If every admin lost the password, add an admin by hand on the server. This example needs `jq`:

```bash title="Add an admin"
sudo k3s kubectl -n geass-system get secret geass-dashboard-auth -o jsonpath='{.data.users}' | base64 -d \
  | jq '. + [{"username":"rescue","password":"a-long-passphrase","role":"admin"}]' > users.json

sudo k3s kubectl -n geass-system patch secret geass-dashboard-auth --type merge \
  -p "$(jq -n --rawfile users users.json '{stringData: {users: $users}}')"
rm users.json
```

Sign in as `rescue`, reset the other passwords in the dashboard, and then remove `rescue`.

You can also set the first users with environment variables. See [Configuration](/docs/reference/configuration/).

### Tokens and sessions

- Geass keeps a hash of each token, never the token, in the Secret `geass-api-tokens` in `geass-system`.
- The operator log records `Created API token`, `Revoked API token`, and `Approved a command-line sign-in`.
- **Sign out everyone** makes a new `session-key` in the Secret `geass-dashboard-auth`. If you set
  `GEASS_DASHBOARD_SESSION_KEY` on the operator, Geass refuses the action. Change the variable instead.
- When the Secret has no users, Geass reads `GEASS_DASHBOARD_PASSWORD`, `GEASS_DASHBOARD_USERNAME`, or
  `GEASS_DASHBOARD_USERS` from the environment. When the Secret has users, the Secret wins.

## Related

- [Sign in](/docs/getting-started/sign-in/)
- [Command line](/docs/guides/command-line/)
- [MCP server](/docs/guides/mcp-server/)
- [REST API](/docs/reference/rest-api/)
- [Security](/docs/operations/security/)
- [Projects and environments](/docs/guides/projects-and-environments/)
