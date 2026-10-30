---
title: "Install Geass"
description: "Install Geass on a Linux server with one command. The installer prints your dashboard login and the steps to open the dashboard."
---

You install Geass with one command on a fresh Linux server. The install takes a few minutes. When it ends, Geass is
ready, and the installer prints your dashboard login. Then you [open the dashboard](/docs/getting-started/sign-in/) and
start to deploy. This is the only time that you work on the server.

## Before you start

You need these things:

- A server that meets the [requirements](/docs/getting-started/requirements/): Linux on `amd64` or `arm64`, 2 GB of
  memory, 10 GiB free on `/`, and root access.
- A fresh server. If the server already runs Geass, the installer updates Geass and keeps your data.
- A real email address, if you want HTTPS certificates. Let's Encrypt uses it to contact you about your certificates.

## Install

1. Connect to the server with SSH.
2. Run this command. Replace `you@yourdomain.com` with your email address:

   ```bash
   curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo env GEASS_ACME_EMAIL=you@yourdomain.com sh
   ```

   To install without an email address, run the command without `env GEASS_ACME_EMAIL=you@yourdomain.com`. You can add
   the address later. See [HTTPS certificates](#https-certificates).

3. If the installer asks whether to keep copies of the cluster state in an S3 bucket, answer `n` for now. You can turn
   on these copies later. See [Disaster recovery](/docs/operations/disaster-recovery/).

When the installer ends, it prints these items:

- The username and the password of your first dashboard user. Save the password now. Nothing can show it again
  after the installer ends.
- An `ssh` command to run on your computer, to open the dashboard before it has a domain.

Next, [open the dashboard](/docs/getting-started/sign-in/).

If the install stops with an error, fix the cause, and run the command again. Nothing is lost. See
[Troubleshooting](#troubleshooting).

## HTTPS certificates

Geass gets free HTTPS certificates for the dashboard and your services from [Let's Encrypt](https://letsencrypt.org/).
Let's Encrypt needs a contact email address before it issues certificates. Without the address, Geass serves no
HTTPS certificates.

Use a real address. Let's Encrypt refuses placeholder domains, such as `example.com`. Also open ports 80 and 443 in
the firewall of your provider. Let's Encrypt uses port 80 to check that you control a hostname.

To add or change the address after you install, open **Settings** → **Domain** in the dashboard, and enter it in
**Certificate email**. See [Connect a domain](/docs/getting-started/connect-domain/).

If you publish the dashboard through a Cloudflare Tunnel, Cloudflare gives it a certificate. Then you do not need the
email address for the dashboard. See [Connect a domain](/docs/getting-started/connect-domain/).

## Pin a version

The command in [Install](#install) installs the newest release. To install a specific release, use the `install.sh` of
that release, and set `GEASS_VERSION` to the same tag:

```bash
curl -sfL https://github.com/degoke/geass/releases/download/v0.1.0/install.sh | sudo env GEASS_VERSION=v0.1.0 sh
```

Replace `v0.1.0` with a tag from the Releases page of the project. To check the files that you download, see
[How the installer works](/docs/operations/installer/#check-what-you-download).

## Install the command line (optional)

The `geass` command line lets you deploy, watch, and scale from a terminal or from CI. Install it on your own computer,
not on the server. You need a dashboard login first.

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install-cli.sh | sh
geass login --url https://geass.example.com
```

`geass login` opens the dashboard in your browser, where you approve the sign-in. See
[Command line](/docs/guides/command-line/).

## Run the installer again

You can run the installer again on the same server at any time:

- On a server that runs Geass, the installer updates Geass to the new release and keeps your data. See
  [Update Geass](/docs/operations/upgrading/).
- On a server that runs the same release, the installer checks everything and changes nothing.
- On a server that runs something else, the installer stops and tells you why. It changes nothing.

### Reinstall

:::danger
Use `--reinstall` only when you can lose everything on the server. A reinstall deletes every project, service,
database, user, and setting, and builds a new, empty Geass.
:::

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo sh -s -- --reinstall
```

The installer tells you what it deletes, and it asks you to enter `reinstall` to confirm. A reinstall does not touch
things outside the server, such as your S3 buckets and backups, DNS records, and cloud accounts.

Use a reinstall to retry an install that failed part of the way. To update a working Geass, run the installer without
`--reinstall`.

## What the installer does not do

- **It does not publish the dashboard.** The dashboard is private until you give it a domain. See
  [Connect a domain](/docs/getting-started/connect-domain/).
- **It does not open ports in the firewall of your provider.** Open ports 80 and 443 there yourself.
- **It does not add servers.** See [Add servers](/docs/getting-started/add-servers/).
- **It does not connect GitHub, Cloudflare, AWS, or backups.** You do that in the dashboard.
- **It does not send telemetry.**

## Troubleshooting

**`Could not download Geass from <url>`.** The server cannot download the installer. Check that the server can reach
`github.com` over HTTPS, and that the `GEASS_VERSION` tag exists.

**`insufficient memory` or `insufficient disk space on /`.** The server is too small. See
[Requirements](/docs/getting-started/requirements/#memory-and-disk).

**`GEASS_ACME_EMAIL "..." is not an email address`.** Enter an address of the form `name@host.tld`, and run the command
again. Nothing on the server changed.

**`warning: GEASS_ACME_EMAIL is not set ...`.** The install continues without HTTPS certificates. To add the address
later, see [HTTPS certificates](#https-certificates).

**`warning: The iptables rules that open the ports are not saved ...`.** The ports close again after a reboot. See
[Requirements](/docs/getting-started/requirements/#ports-and-firewall).

**`k3s did not become ready within 5 minutes`** or **`the operator did not become ready within 5m0s`.** The server
could be too small, or it cannot download what it needs. Read the log file that the installer named.

**`Geass is installed, and it is still starting`.** The server or its network is slow. Geass continues to start. Open
the dashboard. It shows **Geass is starting** until Geass is ready. Deploy after that message goes away.

**The installer printed no password.** The installer creates a user only on a new install. An update keeps your users.
Sign in with an existing user.

**The browser warns about the certificate of a hostname.** Geass has no certificate for that hostname yet. Check that
Geass has an email address, that the DNS record points at your server, and that port 80 is open.

For every error that the installer can print, see
[How the installer works](/docs/operations/installer/#every-installer-error).

## Under the hood

The installer installs [K3s](https://k3s.io/), a small Kubernetes distribution. Then it starts Geass on it. Geass
installs the parts that it needs for certificates, metrics, logs, and storage. For the details, the installer variables,
and a multi-provider install, see [How the installer works](/docs/operations/installer/).

## Related

- [Requirements](/docs/getting-started/requirements/)
- [Open the dashboard](/docs/getting-started/sign-in/)
- [Update Geass](/docs/operations/upgrading/)
- [Uninstall Geass](/docs/operations/uninstall/)
