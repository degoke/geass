---
title: "Connect a domain"
description: "Put the dashboard and your first app on a real domain: check the certificate email, create DNS, set the platform domain, and add a domain to an app."
---

This walkthrough starts with a dashboard that only the cluster can reach. At the end, the dashboard is on
`https://geass.example.com`, and your first app is on `https://shop.example.com`. The walkthrough uses `example.com` and
the server IP `203.0.113.10`. Replace them with your values. For the reasons behind each step, and for troubleshooting,
see [Domains and TLS](/docs/guides/domains-and-tls/).

## Before you start

- A domain that you control, and an admin who is signed in to the dashboard. See [Sign in](/docs/getting-started/sign-in/).
- Ports 80 and 443 reachable on your server, in the firewall of your provider and on the host. The installer does not
  open these ports. Let's Encrypt validates a hostname over port 80, and visitors use port 443. If you use a Cloudflare
  Tunnel in part 3, skip this item.
- A real email address for the Let's Encrypt account. Do not use an `example.com` address. Give the address to the
  installer as `GEASS_ACME_EMAIL`, or set it on the cluster in part 1.
- `kubectl` access on the server: `sudo k3s kubectl`.

:::caution
Make sure that the cluster has a contact email for Let's Encrypt. Part 1 checks that. Geass creates the certificate
issuer that every HTTPS Ingress asks for, a `ClusterIssuer` named `letsencrypt-prod`, only when the cluster has that
email. Without an email, the dashboard never gets its HTTPS certificate, and the domain stays unverified. The exception
is a Cloudflare Tunnel.
:::

## 1. Check the certificate email

If you publish the dashboard through a Cloudflare Tunnel, skip this part.

If you installed with `GEASS_ACME_EMAIL`, Geass has already created the issuer. Read the state of the issuer:

```bash
sudo k3s kubectl -n geass-system get geasscluster default \
  -o jsonpath='{.status.conditions[?(@.type=="ClusterIssuerReady")].reason}{": "}{.status.conditions[?(@.type=="ClusterIssuerReady")].message}{"\n"}'
```

`IssuerReady` means that the issuer exists and that Let's Encrypt accepted the account. You do not need to do more.
`ACMEEmailMissing` means that the cluster has no email, so Geass did not create an issuer. To set an email, run this
command with your own address:

```bash
sudo k3s kubectl -n geass-system patch geasscluster default --type merge \
  -p '{"spec":{"addons":{"certManager":{"acmeEmail":"you@yourdomain.com"}}}}'
```

Wait a minute or so, and then check that the issuer exists and is ready. cert-manager must be ready first.

```bash
sudo k3s kubectl get clusterissuer letsencrypt-prod
```

If the command prints nothing, the condition is not there. These are the possible causes:

- The cert-manager add-on is turned off on the cluster. You then run your own issuer, and its name must be
  `letsencrypt-prod`.
- The operator has not reconciled the cluster yet.

If Geass reports `ExternallyManaged`, an issuer with that name that you created is in use. Geass does not change that
issuer. For every reason that Geass can report, see [HTTPS certificates](/docs/getting-started/install/#https-certificates).
You do this part one time for the cluster.

## 2. Point DNS at the server

Use the option that matches where your DNS is.

**DNS at your own provider.** Create an `A` record `geass.example.com` that points at `203.0.113.10`. Create the record
before you save the domain in part 3. Then the certificate request can succeed on the first try.

**DNS at Cloudflare.** Connect Cloudflare under **Settings** → **Connectors** → **Cloudflare** with a token for your
zone. See [Connect Cloudflare](/docs/guides/connectors/cloudflare/). Geass creates the dashboard record for you in part 3.

## 3. Set the platform domain

1. Open **Settings** → **Domain**.
2. Without Cloudflare: under **Manual domain**, enter `example.com` as the **Root domain**. Leave **Subdomain** as
   `geass`. Click **Save dashboard domain**.
3. With Cloudflare: click **Use https://geass.example.com**. To publish through a tunnel instead of a DNS record, do
   these steps:
   1. Open **Customize zone or subdomain**.
   2. Set **Publish via** to **Cloudflare Tunnel**.
   3. Click **Save dashboard domain**.

   A tunnel needs no open ports and no issuer.

Geass then creates the Ingress for the dashboard and checks the domain every 5 seconds. The domain is verified when
`https://geass.example.com/geass-probe` answers with a trusted certificate. Verification usually takes a few minutes,
while Let's Encrypt issues the certificate. The banner "Dashboard domain" shows above the pages outside a project. The
banner disappears when verification is done.

To see the progress, read the condition:

```bash
sudo k3s kubectl -n geass-system get geassplatformconfig platform \
  -o jsonpath='{range .status.conditions[*]}{.type}{"\t"}{.status}{"\t"}{.reason}{"\t"}{.message}{"\n"}{end}'
```

The condition values are these:

- `DashboardDomainReady` becomes `True` with the reason `Verified`.
- `PendingDNS` means that the DNS record is not correct.
- `PendingHTTPS` means that DNS is correct, but the certificate or a port is not. If the issuer has a problem, the
  `PendingHTTPS` message ends with that problem, in parentheses.
- After 15 minutes, the condition reports `VerificationTimedOut`. Geass continues to try every 5 minutes.

When the condition is `True`, open `https://geass.example.com`.

## 4. Put your first app on a domain

This part assumes that you have a deployed app, for example from [Your first app](/docs/getting-started/first-app/).

1. Create a DNS record `shop.example.com` that points at `203.0.113.10`. In Cloudflare tunnel mode, Geass creates the
   record for you when you save the domain.
2. Open the app, and then open **Settings** → **Networking**.
3. In **Custom domain**, enter `shop.example.com`. Enter the name only, with no `https://`.
4. Click **Save changes**. The change applies immediately.
5. To check the domain, open `http://shop.example.com/`.
6. Turn on **HTTPS** and click **Save changes**. Geass asks cert-manager for a certificate through the issuer from
   part 1.
7. After a few minutes, open `https://shop.example.com/`. Until the certificate is issued, the browser shows a warning.
   To follow the certificate, run `sudo k3s kubectl -n <namespace> describe certificate <app ID>-tls`.

When Cloudflare is connected and ready, a service without a domain gets a default domain over HTTP, for example
`amber-fern-proj-1a2b3c4d.example.com`. In tunnel mode, Geass creates the record of that domain for you. In the DNS to
cluster mode, you add the record yourself, for example a wildcard `*.example.com`. See
[Default hostnames](/docs/guides/domains-and-tls/#default-hostnames).

A service with **HTTPS** turned on does not answer on plain `http://`. Geass does not add a redirect.

## If something does not work

- **The banner stays.** Run the command in part 3 and read the reason. The usual causes are missing DNS, no
  certificate email, and closed ports 80 or 443. No certificate email means that there is no `letsencrypt-prod`
  issuer. **Settings** → **Domain** does not show the status, so use the command.
- **The browser warns about the certificate.** The certificate is not issued yet. Run the command in part 1, and then
  run `sudo k3s kubectl get clusterissuer,certificate -A`.
- **`404 page not found` on an app domain.** The app is not deployed, or the name does not match **Custom domain**.

For details about each problem, see [Troubleshooting on Domains and TLS](/docs/guides/domains-and-tls/#troubleshooting).

## Next

- [Domains and TLS](/docs/guides/domains-and-tls/): how verification works, the Cloudflare modes, and every option.
- [GitHub and builds](/docs/guides/github-and-builds/): deploy from a repository. This needs the dashboard on a public
  HTTPS address.
- [Apps](/docs/guides/apps/)
