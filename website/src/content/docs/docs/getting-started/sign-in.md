---
title: "Sign in to the dashboard"
description: "Create the first dashboard login, open the dashboard before you have a domain, sign in the command line, and manage users, passwords, and sessions."
---

The dashboard requires a login, and a new install has none. Geass does not create a default password, and it does not generate one. You create the first user when you save a Kubernetes Secret. Then you open the dashboard through a tunnel from your own machine. This page tells you about these topics:

- The first sign-in.
- How the `geass` command line signs in after you have a dashboard login.
- The two roles.
- How to rotate credentials.

![The Geass sign-in page.](../../../../assets/docs/login.webp)
*Screenshot with example data.*

## Before you start

- A finished [install](/docs/getting-started/install/). The operator pod `geass-controller-manager` must be running.
- SSH access to the control plane, and `sudo` access on the control plane.

## Create the first user

1. On the control plane, save this Secret as `dashboard-auth.yaml`. Replace `CHANGE_ME` with a password that you select:

   ```yaml title="dashboard-auth.yaml"
   apiVersion: v1
   kind: Secret
   metadata:
     name: geass-dashboard-auth
     namespace: geass-system
   type: Opaque
   stringData:
     users: |
       [{"username":"admin","password":"CHANGE_ME","role":"admin"}]
   ```

   The `users` value is a JSON array. If the password contains `"` or `\`, escape these characters as JSON requires.

2. Apply the Secret and remove the file. Remove the file because it holds the password in plain text:

   ```bash
   sudo k3s kubectl apply -f dashboard-auth.yaml
   rm dashboard-auth.yaml
   ```

Geass reads the Secret when someone signs in, so the operator does not need a restart. The first sign-in attempt replaces each plain-text password in the Secret with a bcrypt hash.

:::note
This Secret is the sample at `config/samples/geass_dashboard_auth.yaml` in the repository. Geass refuses the password `CHANGE_ME`, in any letter case, for every user. Thus, if you forget to replace it, you have no working login. You do not get a login with a known password.
:::

## Reach the dashboard

The operator serves the dashboard on port 8085, behind the `ClusterIP` Service `geass-geass-dashboard`. Nothing publishes the dashboard after the install, so you reach it through a tunnel:

1. From your machine, open an SSH session that forwards a local port to the loopback address of the server:

   ```bash
   ssh -L 8085:127.0.0.1:8085 root@203.0.113.10
   ```

2. In that session, forward the Service to the loopback address of the server. Leave the command running:

   ```bash
   sudo k3s kubectl -n geass-system port-forward svc/geass-geass-dashboard 8085:8085
   ```

3. Open `http://localhost:8085` in your browser.

If you already run `kubectl` on your own machine against this cluster, run the `port-forward` command there and skip the SSH forward. K3s writes its kubeconfig to `/etc/rancher/k3s/k3s.yaml` on the server, with the address `127.0.0.1`. Change that address to one that your machine can reach, and keep 6443/tcp open to your machine.

:::note
Use `localhost` or `127.0.0.1` for this first session. The sign-in cookie is marked `Secure` unless the page comes over HTTPS or from a loopback address. Thus a browser drops the cookie on plain HTTP to a public address, and you stay on the sign-in form.
:::

After you sign in, publish the dashboard on a domain from **Settings → Domain**, and use that address from then on. See [Connect a domain](/docs/getting-started/connect-domain/).

## Sign in

The page shows these items:

- The title **Sign in**.
- A **Username** field, with the value `admin` already in it.
- A **Password** field.
- A **Continue** button.

Enter the user from your Secret.

Sessions last 12 hours. Geass matches usernames without regard to letter case and to spaces before or after the name. The lockout rules are as follows:

- After five failed attempts for one username from one client address in one minute, Geass locks that username for that address for one minute. The page still says `invalid username or password`. Thus a person who continues to guess is locked out, but you can still sign in from a different address.
- Twenty-five failures for one username in one minute, from all clients together, lock that username for everyone for one minute. Where Geass cannot tell the clients apart, five failures in one minute lock the username for everyone for one minute.
- Geass saves the count for the username in the Secret, so the count stays after an operator restart. Geass records only usernames that are in your Secret. It removes a record after the minute of that record is over.
- Separately, a client that fails 20 sign-ins in one minute, with any usernames, is refused for one minute, with the same message.

When the connection comes through a proxy or a tunnel, Geass reads the address of the client from `X-Forwarded-For`. See [Security](/docs/operations/security/#dashboard-users).

To sign out, open the account menu at the top right, and select **Sign out**. The account menu shows your initial. When you sign out, Geass ends every session of that user in every browser, not only the current session.

You can connect an identity provider under **Settings** → **Sign-in** after you sign in. With an identity provider, you can invite people, let them sign in by email, or keep organizations and teams. The sign-in page then shows **Sign in with ...** first, and it keeps the admin password below it. See [Sign in with an identity provider](/docs/guides/external-sign-in/).

## Sign in from the command line

The `geass` command line has no password. It signs in with a code that you approve in the dashboard, so you need a dashboard login first. Install the command line, as [Install Geass](/docs/getting-started/install/#install-the-command-line-optional) tells you. Then run it with the address of your dashboard:

```bash
geass login --url https://geass.example.com
```

For the first session, before you have a domain, start the tunnel from [Reach the dashboard](#reach-the-dashboard), and use `--url http://localhost:8085`.

1. `geass login` prints a code, for example `BCDF-GHJK`, and the address `<dashboard>/device`. When `geass login` runs in a terminal, it opens that page in your browser. To stop this, add `--no-browser`.
2. If the dashboard asks, sign in as an `admin`. The page **Sign in the command line** shows the name of the machine that asks, for example `geass CLI on laptop`. Make sure that the code on the page is the code in your terminal. Approve only a code that you started.
3. Select an **Access** level: **Read only, including logs (recommended)**, **Read only, without logs**, or **Full access**.
4. Select an **Expires** period: **7 days**, **30 days**, or **90 days**. The default is **30 days**.
5. Click **Approve**.

The terminal then prints `Signed in to ... as token:CLI: geass CLI on laptop`. The command line saves the token in `config.json` in the `geass` folder of your user configuration directory, with mode `0600`. On Linux, this file is `~/.config/geass/config.json`. The code is valid for 10 minutes. A viewer cannot approve a code, and an API token cannot approve one either. Thus a leaked token cannot make another token.

The approval creates an API token. The token appears in **Settings → Tokens** as `CLI: geass CLI on <machine>`. To end the token, revoke it there. `geass logout` only removes the token from your machine. Scripts and CI cannot approve a code. For them, create a token under **Settings → Tokens**, and set `GEASS_URL` and `GEASS_TOKEN`. See [Command line](/docs/guides/command-line/#sign-in) and [Users and API tokens](/docs/guides/users-and-tokens/).

## Roles

Each user has one role.

| Role | What the user can do |
| --- | --- |
| `admin` | All that the dashboard offers: create, change, deploy, and delete resources, change settings, and create API tokens |
| `viewer` | Read-only. Geass refuses every write with `viewer role cannot access this`. It also refuses the reads that show secrets: logs, terminals, variables, templates, API tokens, and the GitHub repository list. Geass removes hostnames, connection details, variable values, and cloud settings from the data that the browser of the viewer receives |

A user entry without a `role` is a `viewer`. Geass ignores an entry with any other role, `observer` included. If no valid entry is left, sign-in fails as the Troubleshooting section tells you. Role changes apply to sessions that are already open.

API tokens have their own roles:

- **Full access** (`admin`).
- **Read only, with logs** (`observer`). This role can also read logs and the names of variables, but it can change nothing.
- **Read only** (`viewer`).

`observer` exists only for tokens. The page **Sign in the command line** uses other labels for the same roles:
**Read only, including logs (recommended)** is `observer`, and **Read only, without logs** is `viewer`. See
[Users and tokens](/docs/guides/users-and-tokens/).

## Manage users and passwords

The Secret `geass-dashboard-auth` in `geass-system` holds all of the sign-in data:

| Key | Content |
| --- | --- |
| `users` | JSON array of `{"username", "password", "role"}` |
| `session-key` | The key that signs session cookies. Geass generates it |
| `session-epochs` | A counter for each user. Geass increases it at sign-out, and Geass manages it |
| `login-lockouts` | Records of failed attempts, for your users only. Geass removes expired records, and Geass manages this key |

Edit only `users`. To end all sessions, you can also edit `session-key`. Each username must be unique, without regard to letter case.

**Change a password.** Patch the whole `users` array, with the new plain-text password in place of `CHANGE_ME`. Geass hashes the password at the next sign-in:

```bash
sudo k3s kubectl -n geass-system patch secret geass-dashboard-auth --type merge \
  -p '{"stringData":{"users":"[{\"username\":\"admin\",\"password\":\"CHANGE_ME\",\"role\":\"admin\"}]"}}'
```

The patch replaces the list, so include every user that you want to keep. You can copy existing hashes from the current value. To read the current value, run this command:

```bash
sudo k3s kubectl -n geass-system get secret geass-dashboard-auth -o jsonpath='{.data.users}' | base64 -d
```

That output holds usernames, roles, and bcrypt hashes. Geass accepts a hash in a `password` field as it is.

**Add or remove a user, or change a role.** Edit the array in the same way. The sessions of a removed user stop working immediately. API tokens are not tied to the account of the user, and they continue to work. This includes the tokens that the user approved for the command line. Revoke these tokens under **Settings → Tokens**.

**End every session.** A password change does not end sessions that are already open. These sessions last until they expire or the user signs out. To end all of them, delete the signing key. Geass creates a new key on the next request, and every cookie signed with the old key stops working. This does not apply if `GEASS_DASHBOARD_SESSION_KEY` is set, because that variable has priority over the Secret:

```bash
sudo k3s kubectl -n geass-system patch secret geass-dashboard-auth --type json \
  -p '[{"op":"remove","path":"/data/session-key"}]'
```

**Forgot the password.** Patch the Secret as the previous steps show. After the first sign-in, Geass stores only the hash, so you cannot read back a password.

## Configure users with environment variables

The operator can read users from its environment instead of from the Secret. Set these variables on the `manager` container of the Deployment `geass-controller-manager`:

| Variable | Meaning |
| --- | --- |
| `GEASS_DASHBOARD_PASSWORD` | Password of one `admin` user. Geass uses it only when `GEASS_DASHBOARD_USERS` is empty |
| `GEASS_DASHBOARD_USERNAME` | Name of that user. The default is `admin` |
| `GEASS_DASHBOARD_USERS` | Many users: a JSON array like the array in the Secret, or `name:password:role` entries separated by commas |
| `GEASS_DASHBOARD_SESSION_KEY` | Session signing key: at least 16 characters, or `hex:` followed by at least 16 bytes in hex. It has priority over `session-key` in the Secret |

:::caution
Use the Secret, not environment variables. Environment variables are a weaker choice. The password is in plain text in the Deployment, and anyone who can read Deployments sees it. After the first sign-in, Geass copies the hashed users into the Secret. From then on, the Secret has priority over the variables, so a later change to `GEASS_DASHBOARD_PASSWORD` has no effect.
:::

## What happens under the hood

- `/api/login` uses bcrypt to check the user against the Secret. When the Secret has no valid users, it checks against the variables. Then it sets the cookie `__Host-geass_session`. Over plain http on this machine, the cookie is `geass_session`. The cookie is `HttpOnly` and `SameSite=Lax`, it is signed with the session key, and it is valid for 12 hours. It carries the user name, the role, and the epoch of the user.
- Every API request needs that cookie, or an API token in an `Authorization: Bearer` header. Only these items are public:
  - The static page shell and its files.
  - The login, logout, and session endpoints.
  - The two endpoints that a program uses to start and collect a command-line sign-in: `POST /api/auth/device` and `POST /api/auth/token`, also under `/api/v1/auth/`.
  - `/geass-probe`.
  - The GitHub webhook endpoint.

  The approval of a sign-in is not public. It needs a signed-in admin.
- Geass checks the `Origin` or `Referer` header of a write against the `Host` that the request used to reach the dashboard. Thus a form on a different site cannot act as you. Geass trusts forwarded host headers only when the request reaches the dashboard by its in-cluster Service name. A reverse proxy in front of the dashboard must pass the `Host` header through without a change. A request that carries an API token has no cookie to protect, so it skips this check.

## Troubleshooting

**`dashboard authentication is unavailable` when you press Continue.** Geass found no valid user. Check these items in sequence:

1. The Secret exists: `sudo k3s kubectl -n geass-system get secret geass-dashboard-auth`.
2. `users` is valid JSON.
3. Every entry has a username and a password that are not empty.
4. No password is `CHANGE_ME`.
5. Each role is `admin` or `viewer`.
6. No two usernames are equal, without regard to letter case.

The same message appears when Geass cannot read the Kubernetes API.

**`invalid username or password`.** One of these conditions is true:

- The credentials do not match.
- Your address failed five times for this username in the last minute, or the username failed 25 times from all addresses in the last minute.
- Your address caused 20 failed sign-ins in the last minute.

Wait one minute and try one more time.

**You sign in and return to the sign-in form.** The browser rejected the session cookie. Reach the dashboard on `localhost` or `127.0.0.1` through the tunnel, or on an HTTPS address.

**`request origin could not be verified`.** The `Origin` of the request does not match its `Host`. A proxy or a tunnel that rewrites the host name causes this error. Forward the original `Host` header, or use a port-forward.

**You are sent back to the sign-in form after some hours.** Sessions last 12 hours. Sign in again.

**The page says `Dashboard unavailable` and offers Try again.** The dashboard answered with an error that is not a sign-in prompt. Check the operator with `sudo k3s kubectl -n geass-system get pods` and `sudo k3s kubectl -n geass-system logs deploy/geass-controller-manager -c manager`.

**`geass login` ends with `the sign-in was not approved in time`, or the page says `No sign-in request has that code`.** The code is valid for 10 minutes. The operator keeps pending requests in memory, so an operator restart cancels them. Run `geass login` again.

**The approval page says `Only an admin can approve this`.** You are signed in to the dashboard as a viewer. Sign in as an admin.

**`localhost:8085` does not respond.** The `port-forward` command stopped, or the SSH forward is not running. The Service is `geass-geass-dashboard`. To see it, run `sudo k3s kubectl -n geass-system get svc`.

## Related

- [Deploy your first app](/docs/getting-started/first-app/)
- [Connect a domain](/docs/getting-started/connect-domain/)
- [Command line](/docs/guides/command-line/)
- [Users and tokens](/docs/guides/users-and-tokens/)
- [Security](/docs/operations/security/)
