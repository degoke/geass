---
title: "Connect a domain"
description: "Put the dashboard and your first app on a real domain: set the certificate email, create DNS, set the platform domain, and add a domain to an app."
---

At the start of this walkthrough, you open the dashboard at the public IP address of the server, for example
`https://203.0.113.10`. At the end, the dashboard is on `https://geass.example.com`, and your first app is on `https://shop.example.com`. The walkthrough uses `example.com` and
the server address `203.0.113.10`. Replace them with your values. For every option, see
[Domains and TLS](/docs/guides/domains-and-tls/).

## Before you start

- A domain that you control.
- An admin who is signed in to the dashboard. See [Sign in](/docs/getting-started/sign-in/).
- Ports 80 and 443 open to your server, in the firewall of your provider and on the server. The installer does not open
  them. Let's Encrypt checks a domain on port 80, and visitors use port 443. With a Cloudflare Tunnel, you do not need
  open ports.
- A real email address for Let's Encrypt. Do not use an `example.com` address.

:::caution
Make sure that Geass has a certificate email before you continue. Without one, Geass gets no HTTPS certificates for a
domain, and the dashboard domain never becomes verified. The only exception is a Cloudflare Tunnel.
:::

## 1. Set the certificate email

If you gave the installer an email address, Geass has it already. To check, or to set it, do these steps:

1. Open **Settings** → **Domain**.
2. Look at the badge next to **Certificate email**.
3. If the badge shows **Not set**, enter your email address in **Email**, and click **Save email**.

The badge shows one of these states:

- **Ready**: Let's Encrypt accepted the address, and Geass can get certificates. Continue to part 2.
- **Waiting**: Geass is still setting up. This takes a minute or two.
- **Not set**: Geass has no email, so it gets no certificates.
- **Not working**: the message below the field tells you why. Usually Let's Encrypt refused the address. Enter a real
  address, and click **Save email** again.

You do this part one time for the cluster.

## 2. Point DNS at the server

Use the option that matches where your DNS is:

- **DNS at another provider.** Create an `A` record `geass.example.com` that points at `203.0.113.10`. Create the record
  before part 3, so that the first certificate request can succeed.
- **DNS at Cloudflare.** Connect Cloudflare under **Settings** → **Connectors** → **Cloudflare**, with a token for your
  zone. See [Connect Cloudflare](/docs/guides/connectors/cloudflare/). Geass creates the record for you in part 3.

## 3. Set the platform domain

1. Open **Settings** → **Domain**.
2. Do one of these steps:
   - Without Cloudflare: under **Manual domain**, enter `example.com` in **Root domain**. Keep `geass` in
     **Subdomain**. Click **Save dashboard domain**.
   - With Cloudflare: click **Use https://geass.example.com**.
   - With a Cloudflare Tunnel: open **Customize zone or subdomain**, and set **Publish via** to **Cloudflare Tunnel**.
     Then click **Save dashboard domain**.

Geass then checks the domain every 5 seconds. Usually, the check passes after a few minutes, when Let's Encrypt issues
the certificate. Until then, admins see the **Dashboard domain** banner at the top of the dashboard. When the banner
goes away, open `https://geass.example.com`, and sign in again.

After the domain is verified, the dashboard no longer answers at the IP address of the server. Use the domain.

## 4. Put your first app on a domain

This part needs a deployed app, for example from [Deploy your first app](/docs/getting-started/first-app/).

1. Create a DNS record `shop.example.com` that points at `203.0.113.10`. With a Cloudflare Tunnel, Geass creates the
   record when you save the domain.
2. Open the app, and then open **Settings** → **Networking**.
3. In **Custom domain**, enter `shop.example.com`. Do not enter `https://`.
4. Click **Save changes**.
5. To check the domain, open `http://shop.example.com/`.
6. Turn on **HTTPS**, and click **Save changes**.
7. After a few minutes, open `https://shop.example.com/`. Until the certificate is ready, the browser shows a warning.

A service with **HTTPS** turned on does not answer on `http://`. Geass does not add a redirect.

When Cloudflare is connected, a service without a domain gets a default domain over HTTP, for example
`amber-fern-proj-1a2b3c4d.example.com`. With a Cloudflare Tunnel, Geass creates its DNS record. Otherwise, you create the
record, for example a wildcard `*.example.com`. See [Default hostnames](/docs/guides/domains-and-tls/#default-hostnames).

## If something does not work

**The Dashboard domain banner stays.** The usual causes are a missing DNS record, no certificate email, and closed
ports 80 or 443. Check **Certificate email** in part 1, the DNS record in part 2, and the ports. The dashboard does not
show the exact reason yet. See [Under the hood](#under-the-hood).

**The browser warns about the certificate.** The certificate is not ready yet. Check **Certificate email** in part 1.
If it shows **Ready**, wait a few minutes, and make sure that port 80 is open.

**`404 page not found` on an app domain.** The app is not deployed, or the name is not the same as in **Custom
domain**.

For more problems, see [Troubleshooting on Domains and TLS](/docs/guides/domains-and-tls/#troubleshooting).

## Under the hood

The certificate email creates a cert-manager `ClusterIssuer` named `letsencrypt-prod`. Every HTTPS Ingress that Geass
makes asks for that issuer. These facts apply:

- If you turned off the cert-manager add-on, you must run your own issuer with the name `letsencrypt-prod`.
- If an issuer with that name already exists and Geass did not make it, Geass reports `ExternallyManaged` and does not
  change it.

To see the issuer and the certificates, run this command:

```bash
sudo k3s kubectl get clusterissuer,certificate -A
```

The certificate of an app is `<app ID>-tls` in the namespace of its environment. To follow it, run
`sudo k3s kubectl -n <namespace> describe certificate <app ID>-tls`.

The domain check asks `https://geass.example.com/geass-probe` and expects a trusted certificate. The result is the
condition `DashboardDomainReady` on the `GeassPlatformConfig` named `platform`. To read it, run this command on the
server:

```bash
sudo k3s kubectl -n geass-system get geassplatformconfig platform \
  -o jsonpath='{range .status.conditions[*]}{.type}{"\t"}{.status}{"\t"}{.reason}{"\t"}{.message}{"\n"}{end}'
```

- `Verified`: the domain works.
- `PendingDNS`: the DNS record is missing or points at another address.
- `PendingHTTPS`: DNS is correct, but the certificate is not ready, or port 80 or 443 is closed. A problem with the
  certificate email shows at the end of the message.
- `VerificationTimedOut`: 15 minutes passed. Geass continues to check every 5 minutes.

When the domain is verified, the operator deletes the objects that serve the dashboard at the IP address. The
`DashboardPublicIP` condition of the `GeassCluster` then has the reason `DomainVerified`. See
[`DashboardPublicIP`](/docs/reference/resources/geass-cluster/#dashboardpublicip).

The badge of **Certificate email** comes from the `ClusterIssuerReady` condition of the `GeassCluster`. For every reason
that it can report, see [The certificate issuer](/docs/operations/installer/#the-certificate-issuer).

## Next

- [Domains and TLS](/docs/guides/domains-and-tls/): how verification works, the Cloudflare modes, and every option.
- [GitHub and builds](/docs/guides/github-and-builds/): deploy from a repository. This needs the dashboard on a public
  HTTPS address.
- [Apps](/docs/guides/apps/)
