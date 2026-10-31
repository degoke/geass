---
title: "Domains and TLS"
description: "Publish the dashboard and your services on your domain, use Cloudflare for DNS, and get HTTPS certificates from Let's Encrypt."
---

Geass publishes two kinds of things on your domain: the **dashboard**, and the **services** that you deploy. You set a
**platform domain** one time, under **Settings** → **Domain**. Services then get hostnames of their own. This page tells
you how to set the platform domain, use Cloudflare for DNS, give a service a domain, and get HTTPS certificates.

:::note
On a Geass that a platform runs for you, [managed mode](/docs/operations/managed-mode/) might be on. Then the dashboard
domain and its Cloudflare connection belong to the platform. **Settings** → **Domain** is read-only. The hostnames of
services are not affected.
:::

:::caution
Give the cluster a contact email for Let's Encrypt. Without it, Geass gets no HTTPS certificates for your domains. The dashboard domain
stays unverified, and the **HTTPS** switch of a service gives a certificate warning. A Cloudflare Tunnel does not need
the email. See [HTTPS and certificates](#https-and-certificates).
:::

## Three ways to publish

Select one of these setups when you set the platform domain.

| Setup | DNS | HTTPS | What you do yourself |
| --- | --- | --- | --- |
| **Manual DNS** | You create an `A` record at your DNS provider. | Your server, with a Let's Encrypt certificate. | Create the DNS record. Open ports 80 and 443. Give the cluster a Let's Encrypt email. |
| **DNS to cluster (proxied A record)** | Geass creates a proxied `A` record for the dashboard in your Cloudflare zone. | Cloudflare for visitors. Your server also has a Let's Encrypt certificate. | Connect Cloudflare. Open ports 80 and 443. Give the cluster a Let's Encrypt email. Create the records for service hostnames. |
| **Cloudflare Tunnel** | Geass creates proxied `CNAME` records for the dashboard and every service hostname. | Cloudflare. The cluster connects out to Cloudflare. | Connect Cloudflare. No open ports and no email. |

The installer does not open ports 80 or 443 on your server or in the firewall of your provider. Let's Encrypt checks a
hostname over port 80, and visitors use 443. See [Install](/docs/getting-started/install/#https-certificates) and
[Ports and firewall](/docs/getting-started/requirements/#ports-and-firewall).

## Before you start

- A domain that you control, and an admin login to the dashboard.
- The public IP address of the server that runs Geass. This page uses `203.0.113.10`.
- For **Manual DNS** or **DNS to cluster**:
  - Ports 80 and 443 open on that server.
  - A real email address for Let's Encrypt. Give it to the installer as `GEASS_ACME_EMAIL`, or set it later. See
    [HTTPS and certificates](#https-and-certificates).
- For Cloudflare: a connection. See [Connect Cloudflare](/docs/guides/connectors/cloudflare/).

## Set the platform domain

The **platform domain** (the **Root domain**) is your main domain, for example `example.com`. By default, the dashboard
goes on `geass.example.com`. Services that get [default hostnames](#default-hostnames) also use the platform domain.

Until the dashboard domain is verified, admins see a banner with **Configure domain**. The banner says "Dashboard
domain. Set a public host so Geass can verify HTTPS and finish platform setup." It shows on the pages outside a project.
It goes away after Geass verifies the domain.

### Without Cloudflare

1. At your DNS provider, create an `A` record from `geass.example.com` to `203.0.113.10`.
2. Make sure that the cluster has a Let's Encrypt email. See [HTTPS and certificates](#https-and-certificates).
3. Open **Settings** → **Domain**.
4. In **Manual domain**, enter the **Root domain**, for example `example.com`.
5. Optional: enter a **Subdomain**. The default is `geass`. The page previews the **Dashboard URL**.
6. Click **Save dashboard domain**.

If Cloudflare is connected, **Manual domain** is behind **Use a different domain without Cloudflare**.

### With Cloudflare

1. Connect Cloudflare under **Settings** → **Connectors** → **Cloudflare**.
2. Open **Settings** → **Domain**. The page shows **Cloudflare connected**, the **Zone**, and the **Dashboard URL**.
3. Click **Use https://geass.example.com**. Geass saves the domain with the current **Publish via** selection.

To change the zone, the subdomain, or **Publish via**, do these steps:

1. Open **Customize zone or subdomain**.
2. Set the values. **Publish via** is **DNS to cluster (proxied A record)** or **Cloudflare Tunnel**.
3. Click **Save dashboard domain**.

If Geass cannot read the zone name from Cloudflare, the page asks for the **Zone apex domain**.

:::caution
Keep the default subdomain `geass` when you publish through a tunnel. A different subdomain never verifies.
:::

### The fields

| Field | Meaning | Default |
| --- | --- | --- |
| **Root domain** / **Zone apex domain** | Your main domain. Geass lowercases it and removes `https://`, a path, a trailing dot, and a leading `geass.`. It needs at least one dot. | None |
| **Subdomain** | The host label of the dashboard. Lowercase letters, digits, and hyphens, up to 63 characters. | `geass` |
| **Cloudflare zone** | The zone that Geass writes records in. It appears when the token can read zones. | The connected zone |
| **Publish via** | How Cloudflare reaches the dashboard. | **DNS to cluster (proxied A record)** |

If you save a different domain, verification starts again.

Through the API, use a workspace admin token:

```bash title="Set the platform domain"
curl -X POST https://geass.example.com/api/settings/domain/save \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d domain=example.com -d subdomain=geass -d exposure=ingress
```

`exposure` is `ingress` or `cloudflare-tunnel`. To select a zone, add `cloudflareZoneId`.

## How Geass verifies the domain

After you save, Geass checks the domain every 5 seconds:

1. Geass routes the hostname to the dashboard, unless you use a tunnel.
2. Geass checks DNS. With Cloudflare, Geass creates or updates the record itself. Without Cloudflare, Geass checks that
   the hostname resolves to the public IP of the server.
3. Geass requests `https://geass.example.com/geass-probe` and checks the certificate. A self-signed certificate fails.

When the request succeeds, the domain is verified, and the banner goes away. If the domain is not verified after 15
minutes, Geass checks every 5 minutes. So a late fix still works.

After the domain is verified, the dashboard no longer answers at the public IP address of the server. Use the domain.

The dashboard shows only the banner, not the reason. To check now and read the reason, call the verify endpoint:

```bash
curl -X POST https://geass.example.com/api/settings/domain/verify \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json"
# {"message":"…","ok":false,"state":"pending"}
```

The endpoint can return these messages:

- `DNS lookup failed for <host>`
- `could not reach the dashboard URL`
- `<url> did not return a successful Geass probe response`
- `Dashboard responds locally, but the public URL could not be reached. Check ingress, TLS, and DNS routing.`

If the certificate setup has a problem, the message ends with it in parentheses. An example is
`(No ACME contact email is configured, so the letsencrypt-prod ClusterIssuer was not created …)`.

Everything that needs a public dashboard address waits for verification, for example the GitHub App. See
[Connect GitHub](/docs/guides/connectors/github/).

## HTTPS and certificates

Geass gets certificates from Let's Encrypt for the dashboard and for each service with **HTTPS** on. Geass needs a
contact email to do this. Let's Encrypt keeps the email as the contact of your account. It refuses placeholder domains
such as `example.com`.

To give the email at install time, see [Install](/docs/getting-started/install/#https-certificates). To set or change it
later, open **Settings** → **Domain**, enter the address in **Certificate email**, and click **Save email**.

After the cluster has an email, Geass starts to issue certificates within seconds. You must still do these things
yourself:

- **DNS.** Let's Encrypt checks a hostname over HTTP, so the name must already point to your server. Create the record
  before you turn on **HTTPS**. Let's Encrypt limits repeated failed checks.
- **Ports.** Open 80 and 443 to the server, in the firewall of your provider and on the server.

Behavior to know:

- With **HTTPS** off, a domain serves HTTP only. With **HTTPS** on, it serves HTTPS only. Geass adds no redirect. So
  `http://` to a service with HTTPS on does not answer.
- Until the certificate exists, the browser shows a warning.
- With a **Cloudflare Tunnel**, Cloudflare presents the certificate. The email and the **HTTPS** switch do not matter.
- With **DNS to cluster**, Cloudflare presents its certificate to visitors. Your server presents its own certificate to
  Cloudflare. Geass does not change the SSL/TLS mode of your zone. A mode that reaches your server over plain HTTP
  cannot reach the dashboard.

To use your own certificate issuer, for example a wildcard issuer, see [Under the hood](#under-the-hood).

## Cloudflare-managed DNS

When you connect Cloudflare, Geass manages DNS in one zone. What Geass creates depends on **Publish via**:

| Publish via | In Cloudflare |
| --- | --- |
| **DNS to cluster (proxied A record)** | A proxied `A` record for the dashboard host. Nothing for services. |
| **Cloudflare Tunnel** | A tunnel named `geass`, its routes, and a proxied `CNAME` for the dashboard host and each service host in the zone. |

The token needs **Zone** → **Zone** → **Read** and **Zone** → **DNS** → **Edit**. For a tunnel, it also needs
**Account** → **Cloudflare Tunnel** → **Edit**. To connect, rotate, and disconnect, see
[Connect Cloudflare](/docs/guides/connectors/cloudflare/).

**Zones.** If the token can read one zone, Geass selects it. If it can read several, you select one. Save a root domain
that is in that zone, because Geass writes only there. **Refresh zones** on the Cloudflare card reads the list again.

**Tunnel updates.** In tunnel mode, Geass updates the routes and records when a service gets, changes, or loses a host.
Every 5 minutes, Geass also repairs a route or record that someone deleted in Cloudflare. The route of a sleeping
service wakes it.

Geass does not create records for services in **DNS to cluster** mode. It does not change SSL/TLS modes. It does not
delete records or the tunnel when you disconnect.

## Default hostnames

When Cloudflare is connected, Geass gives each service without a hostname a default hostname:

```text
<service ID>-<project ID>.<root domain>
<service ID>-<project ID>-<environment>.<root domain>     (environments other than production)
```

For example, `amber-fern-proj-1a2b3c4d.example.com` and `amber-fern-proj-1a2b3c4d-staging.example.com`. Geass saves the
hostname as the **Custom domain** of the service, with **HTTPS** off. So the address shows `http://`, even when
Cloudflare serves it over HTTPS. If you clear the field, Geass fills it in again.

Without Cloudflare, services get no default hostname. A default hostname lets a service
[sleep when idle](/docs/guides/scale-to-zero/).

In tunnel mode, Geass creates the DNS record for the default hostname. In **DNS to cluster** mode, it does not. Create a
record yourself, for example a wildcard `*.example.com` that points to the server.

## Give a service a domain

1. Create the DNS record: an `A` record from `shop.example.com` to `203.0.113.10`. In tunnel mode, Geass creates the
   record.
2. Open the service, and then **Settings** → **Networking**.
3. In **Custom domain**, enter the hostname only, in lowercase, with no `https://` and no path.
4. Click **Save changes**. The change applies immediately.
5. Open `http://shop.example.com/`.
6. Make sure that the cluster has a Let's Encrypt email. See [HTTPS and certificates](#https-and-certificates).
7. Turn on **HTTPS**, and click **Save changes**.
8. After the certificate is issued, open `https://shop.example.com/`.

A draft shows a domain but serves nothing until you deploy. Under the fields, **Public networking** shows the address.

**Verify DNS.** With this setting on, Geass checks that the hostname resolves, and shows the result under the address.
If the hostname does not resolve, the line reads "DNS hostname is required". The check accepts any answer, so it does
not prove that the name points to your cluster. The check does not change the status of the service.

**More hosts and paths.** The dashboard takes one host and serves it at `/`. For several hosts or paths, use a
manifest. See [Apps](/docs/guides/apps/#manifest-and-api). In tunnel mode, Geass publishes only the main host. Another
host needs its own DNS record.

**Sleeping services.** A service that [sleeps when idle](/docs/guides/scale-to-zero/) needs a domain, because a
request to the domain wakes it.

## Troubleshooting

**The "Dashboard domain" banner never goes away.** Geass has not verified the domain. Call the verify endpoint to read
the reason. See [How Geass verifies the domain](#how-geass-verifies-the-domain). The usual causes are:

- The DNS record is missing, or it points to another server.
- The cluster has no Let's Encrypt email, so there is no certificate.
- A port is closed.

**`DNS lookup failed`, or DNS not detected.** The host does not resolve to the public IP of the server. Check the record
with `dig +short geass.example.com`. In tunnel mode, the `CNAME` does not point to the tunnel yet. A proxied Cloudflare
record that resolves to Cloudflare addresses is correct.

**"could not reach the dashboard URL".** DNS is correct, but the HTTPS request fails. Make sure that ports 80 and 443
reach the server and that the cluster has a Let's Encrypt email. With Cloudflare in front, also check the SSL/TLS mode
of the zone.

**"Dashboard responds locally, but the public URL could not be reached."** Geass works. The public path does not: DNS,
ports, the certificate, or Cloudflare.

**The Cloudflare token sees more than one zone.** Select one under **Customize zone or subdomain**.

**The browser warns about the certificate, or shows "TRAEFIK DEFAULT CERT".** No certificate is issued. Usually the
cluster has no email, the DNS record did not point to the server, or port 80 was closed. Fix the cause. Geass tries
again.

**A service domain returns `404 page not found`.** No route exists for that name. The service is not deployed yet,
**Custom domain** has more than the hostname, or the name reaches another server.

**`502` or `503` on a service domain.** The route exists, but no copy of the service answers. See
[Apps](/docs/guides/apps/#troubleshooting).

**A default hostname does not resolve.** In **DNS to cluster** mode, Geass creates no records for services. Add a
record or a wildcard, or switch to **Cloudflare Tunnel**.

**Cloudflare shows a 5xx error page for a service.** Set the SSL/TLS mode of the zone so that Cloudflare reaches your
server over HTTPS, or use the tunnel.

**The dashboard stopped working after you changed the root domain.** DNS and the certificate must follow the new domain.
Keep the old record until the new address works.

## Under the hood

Geass routes traffic with the Traefik ingress of K3s, and gets certificates with cert-manager from the `ClusterIssuer`
`letsencrypt-prod`. Geass creates that issuer when the `GeassCluster` has `spec.addons.certManager.acmeEmail`. Geass
never changes an issuer with that name that it did not create. So you can supply your own, for example a DNS-01 wildcard
issuer. The domain settings and the `DashboardDomainReady` condition are on the `GeassPlatformConfig` `platform`.

```bash title="Inspect domains and certificates"
kubectl -n geass-system get geassplatformconfig platform \
  -o jsonpath='{range .status.conditions[*]}{.type}{"\t"}{.reason}{"\t"}{.message}{"\n"}{end}'
kubectl -n geass-system get geasscluster default \
  -o jsonpath='{.status.conditions[?(@.type=="ClusterIssuerReady")].message}{"\n"}'
kubectl get clusterissuer letsencrypt-prod
kubectl get certificate,order,challenge -A
kubectl -n proj-1a2b3c4d-production describe certificate amber-fern-tls
```

A certificate that stays `Ready: False` names the reason in its `Order` and `Challenge`. Usually, DNS does not point to
the server, or port 80 is closed. `ClusterIssuerReady` with `ACMEEmailMissing` means that the cluster has no email.
`IssuerNotReady` usually means that Let's Encrypt refused the email.

### Set the Let's Encrypt email

With access to the cluster, this command sets or changes the email:

```bash
sudo k3s kubectl -n geass-system patch geasscluster default --type merge \
  -p '{"spec":{"addons":{"certManager":{"acmeEmail":"you@yourdomain.com"}}}}'
```

## Related

- [Connect a domain](/docs/getting-started/connect-domain/): a short walkthrough.
- [Connect Cloudflare](/docs/guides/connectors/cloudflare/)
- [Apps](/docs/guides/apps/)
- [GitHub and builds](/docs/guides/github-and-builds/)
- [Security](/docs/operations/security/)
- [Troubleshooting](/docs/operations/troubleshooting/)
