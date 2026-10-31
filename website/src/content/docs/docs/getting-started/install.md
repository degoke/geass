---
title: "Install Geass"
description: "Install Geass on a Linux server with one command. The installer prints a link that opens the dashboard, where you create your admin user."
---

You install Geass with one command on a fresh Linux server. The install takes a few minutes. When it ends, Geass is
ready, and the installer prints a link to the dashboard. You open the link, create your admin user, and start to
deploy. This is the only time that you work on the server.

## Before you start

You need these things:

- A server that meets the [requirements](/docs/getting-started/requirements/): Linux on `amd64` or `arm64`, 2 GB of
  memory, 10 GiB free on `/`, and root access.
- A fresh server. If the server already runs Geass, the installer updates Geass and keeps your data.
- Ports 80 and 443 open to the internet in the firewall of your provider. The dashboard uses them before it has a
  domain.
- A real email address, if you want HTTPS certificates for your domains. Let's Encrypt uses it to contact you about
  your certificates.

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
4. Open the link that the installer printed. See [Open the dashboard](/docs/getting-started/sign-in/#open-the-dashboard).

When the installer ends, it prints a setup link, for example `https://203.0.113.10/setup#...`. The link opens the
dashboard at the public IP address of the server, where you create your admin user. The link works one time, for
24 hours. If you lose it, you can [get a new setup link](/docs/guides/users-and-tokens/#get-a-new-setup-link).

If the dashboard cannot use the public IP address, the installer prints the reason. It then prints an `ssh` command and
a link to `http://localhost:8085` instead.

If the install stops with an error, fix the cause, and run the command again. Nothing is lost. See
[Troubleshooting](#troubleshooting).

## HTTPS certificates

Geass gets free HTTPS certificates for the dashboard and your services from [Let's Encrypt](https://letsencrypt.org/).

Before the dashboard has a domain, Geass gets a certificate for the public IP address of the server. This certificate
needs no email address. Geass renews it automatically.

For a domain, Let's Encrypt needs a contact email address before it issues certificates. Without the address, Geass
serves no HTTPS certificates for your domains.

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

- **It does not give the dashboard a domain.** Until you do, the dashboard is at the public IP address of the server.
  See [Connect a domain](/docs/getting-started/connect-domain/).
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

**The installer printed no setup link.** The installer creates a setup link only when the dashboard has no users. An
update keeps your users. Sign in with an existing user. If nobody can sign in, [get a new setup link](/docs/guides/users-and-tokens/#get-a-new-setup-link).

**The installer printed an `ssh` command instead of an `https://` link.** The dashboard cannot use the public IP
address, and the installer printed the reason. Usually, port 80 is closed in the firewall of your provider. Use the
`ssh` command, or see [Troubleshooting](/docs/getting-started/sign-in/#troubleshooting).

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
