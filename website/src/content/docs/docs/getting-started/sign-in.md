---
title: "Sign in to the dashboard"
description: "Open the dashboard the first time with the login that the installer prints, before you have a domain, and sign in the command line."
---

The installer creates the first dashboard user, `admin`, with a random password. It prints the username and the
password when it creates the user, and again at the end of the install. Before you have a domain, the dashboard has no public address. You open it
through an SSH tunnel from your computer:

1. Open the SSH tunnel.
2. Sign in at `http://localhost:8085`.
3. Put the dashboard on a domain. Then you can open the dashboard from anywhere, and you no longer need the tunnel.

![The Geass sign-in page.](../../../../assets/docs/login.webp)
*Screenshot with example data.*

## Before you start

- A finished [install](/docs/getting-started/install/).
- The username, the password, and the `ssh` command that the installer printed at the end of the install.
- SSH access to the server from your computer.

Save the password in your password manager. Geass keeps only a hash of the password, so nobody can show it again. If
you lost the password, ask another admin to reset it under **Settings** → **Users**. See
[Users and API tokens](/docs/guides/users-and-tokens/#reset-a-password).

## Open the SSH tunnel

The dashboard listens on the loopback address of the server, `127.0.0.1:8085`. Only programs on the server can reach
that address. An SSH tunnel connects port 8085 on your computer to that address.

1. On your computer, run the `ssh` command that the installer printed. Replace `<server address>` with the IP address
   or the hostname of your server:

   ```bash
   ssh -L 8085:127.0.0.1:8085 ubuntu@203.0.113.10
   ```

2. Keep this SSH session open while you use the dashboard.

If port 8085 is in use on your computer, use a different local port, for example `-L 9085:127.0.0.1:8085`. Then open
`http://localhost:9085`.

If you installed Geass on the computer that you use, you do not need the tunnel. Open `http://localhost:8085` directly.

## Sign in

1. In your browser, open `http://localhost:8085`.
2. In **Username**, enter `admin`. The field shows `admin` at first.
3. In **Password**, enter the password from the installer.
4. Click **Continue**.

Use `localhost` or `127.0.0.1` in the address. On plain HTTP to any other address, the browser drops the sign-in cookie,
and you return to the sign-in page.

A session lasts 12 hours. Usernames do not depend on letter case.

After five failed attempts for one username from one address in one minute, Geass locks that username for that address.
The lock lasts one minute. The page still says `invalid username or password`. Wait one minute, and then try again. See
[Security](/docs/operations/security/#dashboard-users) for the full lockout rules.

To sign out, open the account menu at the top right, and select **Sign out**. Sign-out ends the sessions of that user in
every browser.

To invite people, let them sign in by email, or use organizations and teams, connect an identity provider. Open
**Settings** → **Sign-in**. See [Sign in with an identity provider](/docs/guides/external-sign-in/).

## Put the dashboard on a domain

Before you use the dashboard from other places, give it a domain:

1. Open **Settings** → **Domain**.
2. If **Certificate email** shows **Not set**, enter your email address, and click **Save email**. Let's Encrypt needs
   this address before it issues HTTPS certificates.
3. Enter your domain, and click **Save dashboard domain**.

From then on, open the dashboard at its domain, for example `https://geass.example.com`. For the DNS record and the
ports, see [Connect a domain](/docs/getting-started/connect-domain/).

## Sign in from the command line

The `geass` command line has no password. It signs in with a code that you approve in the dashboard. You need a
dashboard login first. To install the command line, see
[Install Geass](/docs/getting-started/install/#install-the-command-line-optional).

1. Run `geass login` with the address of your dashboard:

   ```bash
   geass login --url https://geass.example.com
   ```

   Before you have a domain, open the [SSH tunnel](#open-the-ssh-tunnel). Then use `--url http://localhost:8085`.

2. The command prints a code, for example `BCDF-GHJK`, and opens the page `/device` of your dashboard in your browser. To
   stop the browser from opening, add `--no-browser`.
3. If the dashboard asks, sign in as an admin.
4. On the page **Sign in the command line**, make sure that the code is the code in your terminal. Approve only a code
   that you started.
5. Select an **Access** level: **Read only, including logs (recommended)**, **Read only, without logs**, or **Full
   access**.
6. Select an **Expires** period: **7 days**, **30 days**, or **90 days**. The default is **30 days**.
7. Click **Approve**.

The terminal then prints `Signed in to ... as token:CLI: geass CLI on <machine>`. The code is valid for 10 minutes. Only
an admin who signed in to the dashboard can approve a code. A viewer and an API token cannot.

The approval creates an API token named `CLI: geass CLI on <machine>` in **Settings** → **Tokens**. To end the token,
revoke it there. `geass logout` only removes the token from your computer. For scripts and CI, create a token under
**Settings** → **Tokens**, and set `GEASS_URL` and `GEASS_TOKEN`. See [Command line](/docs/guides/command-line/#sign-in)
and [Users and API tokens](/docs/guides/users-and-tokens/).

## Roles

Each user has one role.

| Role | What the user can do |
| --- | --- |
| `admin` | Everything in the dashboard: create, change, deploy, and delete, change settings, and create API tokens. |
| `viewer` | Read only. A viewer cannot see logs, terminals, variable values, API tokens, connection details, or hostnames. |

The user that the installer creates is an `admin`. A user without a `role` is a `viewer`. API tokens have one more
role, **Read only, with logs**. See [Users and API tokens](/docs/guides/users-and-tokens/).

## Troubleshooting

**`localhost:8085` does not respond.** The SSH session closed, or the dashboard on the server stopped. Start the
`ssh` command again. If the page still does not respond, see [Under the hood](#under-the-hood).

**`invalid username or password`.** The username or the password is wrong, or the username is locked for one minute.
Wait one minute, and then try again. Copy the password from the install output. Do not enter it by hand.

**You sign in and return to the sign-in page.** The browser rejected the sign-in cookie. Open the dashboard at
`http://localhost:8085` through the tunnel, or at an HTTPS address.

**The installer printed no password.** The installer creates a user only when the server has no dashboard users. An
update and a server that already had users print `The dashboard already has users`. Sign in with an existing user.

**`request origin could not be verified`.** A proxy or a tunnel changed the host name of the request. Forward the
original `Host` header, or use the SSH tunnel from [Open the SSH tunnel](#open-the-ssh-tunnel).

**`dashboard authentication is unavailable` when you click Continue.** Geass found no valid user. See
[Under the hood](#under-the-hood) to check the users.

**The page says `Dashboard unavailable`.** Click **Try again**. If the message stays, read the Geass log. See
[Under the hood](#under-the-hood).

**`geass login` says `the sign-in was not approved in time`, or the page says `No sign-in request has that code`.** The
code expired after 10 minutes, or Geass restarted. Run `geass login` again.

**The approval page says `Only an admin can approve this`.** You signed in as a viewer. Sign in as an admin.

## Under the hood

### When the dashboard does not answer on the server

To see whether the dashboard service on the server runs, run this command on the server, and read its last lines:

```bash
sudo systemctl status geass-dashboard
```

On a server that uses OpenRC, run `sudo rc-service geass-dashboard status` instead.

### How the dashboard is reached

The dashboard is the `ClusterIP` Service `geass-geass-dashboard` on port 8085 in the namespace `geass-system`. The
installer adds the service `geass-dashboard` on the first server: a systemd unit, or an OpenRC script on a server that
uses OpenRC. That service runs
`k3s kubectl port-forward --address 127.0.0.1` to the dashboard Service, so the dashboard answers on `127.0.0.1:8085`
and on no other address. The service manager starts the port forward again after a restart or an update of Geass. The installer
adds this service on a new install only. On a server that you installed with an earlier release, start the same port
forward by hand, and keep it running:

```bash
sudo k3s kubectl -n geass-system port-forward --address 127.0.0.1 svc/geass-geass-dashboard 8085:8085
```

The users are in the Kubernetes Secret `geass-dashboard-auth` in the namespace `geass-system`. The installer creates
this Secret with one `admin` user and a bcrypt hash of the generated password. The installer never replaces users that
work, and it does not write the password to its log. If the Secret exists with no valid user, the installer adds the
`admin` user to it. You edit only the `users` key. Geass manages the other keys:
`session-key`, `session-epochs`, and `login-lockouts`.

To read the users, run this command. The output shows usernames, roles, and password hashes:

```bash
sudo k3s kubectl -n geass-system get secret geass-dashboard-auth -o jsonpath='{.data.users}' | base64 -d
```

### Set a new password

To add users, change roles, and reset passwords, use **Settings** → **Users**. See
[Users and API tokens](/docs/guides/users-and-tokens/#manage-users). When no admin can sign in, add an admin to the
Secret by hand. See [Users and API tokens](/docs/guides/users-and-tokens/#the-user-list).

### Other details

Geass can also read users from the environment variables `GEASS_DASHBOARD_PASSWORD`, `GEASS_DASHBOARD_USERNAME`, and
`GEASS_DASHBOARD_USERS` on the `manager` container of the Deployment `geass-controller-manager`. Geass uses them only when
the Secret has no valid users. At the first sign-in, Geass copies them into the Secret, and from then on the Secret has
priority. `GEASS_DASHBOARD_SESSION_KEY` sets the session key and has priority over the Secret. Use the Secret instead,
because the variables show the password in plain text to anyone who can read the Deployment.

The session cookie is `__Host-geass_session`, or `geass_session` over plain HTTP on `localhost`. It is `HttpOnly`,
`SameSite=Lax`, and `Secure` except on a loopback address. Geass checks the `Origin` or `Referer` of each change against
the `Host` of the request. A reverse proxy in front of the dashboard must pass the `Host` header without a change.

If you run `kubectl` on your own computer against this cluster, you can run the `port-forward` command there instead of
the SSH tunnel. K3s writes its kubeconfig to `/etc/rancher/k3s/k3s.yaml` on the server, with the address `127.0.0.1`.
Change that address to one that your computer can reach, and open 6443/tcp to your computer.

To read the Geass log, run this command on the server:

```bash
sudo k3s kubectl -n geass-system logs deploy/geass-controller-manager -c manager
```

## Related

- [Deploy your first app](/docs/getting-started/first-app/)
- [Connect a domain](/docs/getting-started/connect-domain/)
- [Command line](/docs/guides/command-line/)
- [Users and API tokens](/docs/guides/users-and-tokens/)
- [Security](/docs/operations/security/)
