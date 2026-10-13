---
title: "Connect a domain"
description: "Put the dashboard and your first app on a real domain: check the certificate email, create DNS, set the platform domain, and add a domain to an app."
---

This walkthrough takes you from a dashboard that only the cluster can reach to `https://geass.example.com`, and then puts your
first app on `https://shop.example.com`. It uses `example.com` and the server IP `203.0.113.10`. Replace them with yours.
For the reasons behind each step, and for troubleshooting, see [Domains and TLS](/docs/guides/domains-and-tls/).

## Before you start

- A domain you control, and a signed-in admin in the dashboard (see [Sign in](/docs/getting-started/sign-in/)).
- Ports 80 and 443 reachable on your server, in your provider's firewall and on the host. The installer does not open them.
  Let's Encrypt validates a hostname over port 80, and visitors use 443. Skip this if you use a Cloudflare Tunnel in part 3.
- A real email address for the Let's Encrypt account (not `example.com`), given to the installer as `GEASS_ACME_EMAIL` or
  set on the cluster in part 1.
- `kubectl` access on the server: `sudo k3s kubectl`.

:::caution
Geass creates the certificate issuer that every HTTPS Ingress asks for, a `ClusterIssuer` named `letsencrypt-prod`, but
only when the cluster has a contact email for Let's Encrypt. Part 1 checks that. Without an email the dashboard's HTTPS
certificate is never issued and the domain stays unverified, unless you use a Cloudflare Tunnel.
:::

## 1. Check the certificate email

Skip this part if you will publish the dashboard through a Cloudflare Tunnel.

If you installed with `GEASS_ACME_EMAIL`, Geass has already created the issuer. Read its state:

```bash
sudo k3s kubectl -n geass-system get geasscluster default \
  -o jsonpath='{.status.conditions[?(@.type=="ClusterIssuerReady")].reason}{": "}{.status.conditions[?(@.type=="ClusterIssuerReady")].message}{"\n"}'
```

`IssuerReady` means the issuer exists and Let's Encrypt accepted the account: nothing more to do. `ACMEEmailMissing` means
the cluster has no email, so Geass created no issuer. Set one, using your own address:

```bash
sudo k3s kubectl -n geass-system patch geasscluster default --type merge \
  -p '{"spec":{"addons":{"certManager":{"acmeEmail":"you@yourdomain.com"}}}}'
```

Within a minute or so (cert-manager must be ready first), check that the issuer exists and is ready:

```bash
sudo k3s kubectl get clusterissuer letsencrypt-prod
```

If the command prints nothing, the condition is not there: the cert-manager add-on is switched off on the cluster (you then
run your own issuer, and it must be named `letsencrypt-prod`), or the operator has not reconciled the cluster yet. If Geass
reports `ExternallyManaged`, an issuer of that name that you created is in use and
Geass leaves it alone. For every reason Geass can report, see
[HTTPS certificates](/docs/getting-started/install/#https-certificates). You do this once for the cluster.

## 2. Point DNS at the server

Choose the option that matches where your DNS lives.

**DNS at your own provider.** Create an `A` record `geass.example.com` pointing at `203.0.113.10`. Create the record before you
save the domain in part 3, so the certificate request can succeed on the first try.

**DNS at Cloudflare.** Connect Cloudflare under **Settings** → **Connectors** → **Cloudflare** with a token for your zone
(see [Cloud connections](/docs/guides/connections/#cloudflare)). Geass creates the dashboard record for you in part 3.

## 3. Set the platform domain

1. Open **Settings** → **Domain**.
2. Without Cloudflare: under **Manual domain**, enter `example.com` as the **Root domain**. Leave **Subdomain** as `geass`.
   Choose **Save dashboard domain**.
3. With Cloudflare: choose **Use https://geass.example.com**. To publish through a tunnel instead of a DNS record, open
   **Customize zone or subdomain**, set **Publish via** to **Cloudflare Tunnel** and choose **Save dashboard domain**. A
   tunnel needs no open ports and no issuer.

Geass now creates the dashboard's Ingress, and checks the domain every 5 seconds. It is verified when
`https://geass.example.com/geass-probe` answers with a trusted certificate. That usually takes a few minutes while the
certificate is issued. The banner "Dashboard domain", which shows above the pages outside a project, disappears when it is
done.

To see where it is, read the condition:

```bash
sudo k3s kubectl -n geass-system get geassplatformconfig platform \
  -o jsonpath='{range .status.conditions[*]}{.type}{"\t"}{.status}{"\t"}{.reason}{"\t"}{.message}{"\n"}{end}'
```

`DashboardDomainReady` goes `True` with the reason `Verified`. `PendingDNS` means the record is not right, and `PendingHTTPS`
means DNS is right but the certificate or a port is not. The `PendingHTTPS` message ends with what is wrong with the
issuer, in parentheses, when there is something. After 15 minutes it reports `VerificationTimedOut` and keeps
trying every 5 minutes. Open `https://geass.example.com` when it is `True`.

## 4. Put your first app on a domain

This assumes you have an app deployed, for example from [Your first app](/docs/getting-started/first-app/).

1. Create a DNS record `shop.example.com` pointing at `203.0.113.10`. In Cloudflare tunnel mode Geass creates it for you when
   you save the domain.
2. Open the app, then **Settings** → **Networking**. Type `shop.example.com` in **Custom domain**: the name only, with no
   `https://`. Choose **Save changes**. It applies at once. Open `http://shop.example.com/` to check it.
3. Turn on **HTTPS** and choose **Save changes**. Geass asks cert-manager for a certificate through the issuer from part 1.
   Open `https://shop.example.com/` after a few minutes. Until the certificate is issued the browser warns. To follow it,
   run `sudo k3s kubectl -n <namespace> describe certificate <app ID>-tls`.

With Cloudflare connected and ready, a service without a domain gets a default one such as
`amber-fern-proj-1a2b3c4d.example.com` over HTTP. In tunnel mode its record is created for you. In the DNS to cluster mode
you add the record yourself, for example a wildcard `*.example.com`. See
[Default hostnames](/docs/guides/domains-and-tls/#default-hostnames).

A service with **HTTPS** on does not answer on plain `http://`: Geass adds no redirect.

## If something does not work

- **The banner stays.** Run the command in part 3 and read the reason. Missing DNS, no certificate email (no
  `letsencrypt-prod` issuer) and closed ports 80 or 443 are the usual causes. **Settings** → **Domain** does not show the
  status, so use the command.
- **The browser warns about the certificate.** The certificate is not issued yet. Check the command in part 1, then
  `sudo k3s kubectl get clusterissuer,certificate -A`.
- **`404 page not found` on an app domain.** The app is not deployed, or the name does not match **Custom domain**.

Details for each are in [Troubleshooting on Domains and TLS](/docs/guides/domains-and-tls/#troubleshooting).

## Next

- [Domains and TLS](/docs/guides/domains-and-tls/): how verification works, Cloudflare modes and every option.
- [GitHub and builds](/docs/guides/github-and-builds/): deploy from a repository, which needs the dashboard on a public HTTPS address.
- [Apps](/docs/guides/apps/)
