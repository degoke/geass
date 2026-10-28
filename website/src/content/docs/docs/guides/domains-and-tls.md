---
title: "Domains and TLS"
description: "Publish the dashboard on your domain, give services their own hostnames, use Cloudflare for DNS, and how Geass gets HTTPS certificates from Let's Encrypt."
---

Geass publishes two kinds of things on your domain: the **dashboard**, and the **services** you deploy. You set a
**platform domain** once, under **Settings** → **Domain**. Services then get hostnames of their own. This page covers
setting the platform domain, how Geass checks it, Cloudflare-managed DNS, custom domains on a service, and how Geass gets
HTTPS certificates, including the contact email it needs.

:::note
On a Geass that a platform runs for you, with [managed mode](/docs/operations/managed-mode/) on, the dashboard's domain, and the
Cloudflare connection that writes its DNS records, are the platform's: **Settings** → **Domain** is shown read-only, and the API
refuses to change them (`403`, `code` `managed`). Services' hostnames are not affected.
:::

:::caution
Geass creates the certificate issuer only when the cluster has a contact email for Let's Encrypt. Every HTTPS Ingress
Geass creates, for the dashboard and for services, asks cert-manager for its certificate from a `ClusterIssuer` named
`letsencrypt-prod`, and Geass creates that issuer from the email you give the installer (`GEASS_ACME_EMAIL`) or set later
on the cluster. Without an email there is no issuer, no certificate is issued, the dashboard domain stays unverified
(a Cloudflare Tunnel avoids this, and so can Cloudflare's proxy, depending on your zone's SSL/TLS mode) and a service's
**HTTPS** toggle gives you the browser's certificate warning. See
[HTTPS and certificates](#https-and-certificates).
:::

## Three ways to publish

Pick one when you set the platform domain.

| Setup | DNS | Where TLS ends | What you do yourself |
| --- | --- | --- | --- |
| **Manual DNS** | You create an `A` record at your DNS provider. | On your server, in Traefik, with a certificate from cert-manager. | Create the DNS record. Open ports 80 and 443. Give the cluster a Let's Encrypt email. |
| **Cloudflare, DNS to cluster** (**DNS to cluster (proxied A record)**) | Geass creates a proxied `A` record for the dashboard in your Cloudflare zone. | At Cloudflare for visitors. Between Cloudflare and your server it depends on your zone's SSL/TLS mode, and the server's own certificate comes from cert-manager. | Connect Cloudflare. Open ports 80 and 443. Give the cluster a Let's Encrypt email. Records for service hostnames are yours to create. |
| **Cloudflare Tunnel** (**Cloudflare Tunnel**) | Geass creates proxied `CNAME` records for the dashboard and every service hostname. | At Cloudflare. A `cloudflared` pod in the cluster connects out to Cloudflare. | Connect Cloudflare. No inbound ports, no issuer. |

The installer does not open ports 80 or 443 on your server or on your provider's firewall. Let's Encrypt validates a
hostname over port 80, and visitors use 443. See [Install](/docs/getting-started/install/#https-certificates) and
[Ports and firewall](/docs/getting-started/requirements/#ports-and-firewall).

## Before you start

- A domain you control, and an admin login to the dashboard.
- The public IP address of the server that runs Geass (this page uses `203.0.113.10`).
- For the manual setup or Cloudflare DNS to cluster: ports 80 and 443 reachable on that server. K3s runs Traefik, which
  listens on them. Also a real email address for the Let's Encrypt account, given to the installer as `GEASS_ACME_EMAIL`
  or set on the cluster later (see [HTTPS and certificates](#https-and-certificates)). Let's Encrypt refuses placeholder
  domains such as `example.com`.
- For Cloudflare: a connection, set up as described in [Connect Cloudflare](/docs/guides/connectors/cloudflare/).

## Set the platform domain

The **platform domain** (the **Root domain**) is your main domain, for example `example.com`. The dashboard goes on
`geass.example.com` by default, and services that get default hostnames use it too (see
[Default hostnames](#default-hostnames)).

Until the dashboard domain is set and verified, admins see a banner: "Dashboard domain. Set a public host so Geass can verify
HTTPS and finish platform setup." with **Configure domain**. It shows above the pages outside a project (**Projects**,
**Templates**, **Usage**, **Pools** and **Settings**, except **Settings** → **Domain**), not inside a project. It disappears once the
domain is verified.

### Without Cloudflare

1. Create a DNS record `geass.example.com` → `203.0.113.10` (type `A`) at your DNS provider.
2. Check that the cluster has a Let's Encrypt email, so Geass creates the `letsencrypt-prod` issuer
   ([below](#https-and-certificates)).
3. Open **Settings** → **Domain**. In **Manual domain** enter the **Root domain** (`example.com`, apex only) and optionally
   a **Subdomain** (default `geass`). The page previews the **Dashboard URL**.
4. Choose **Save dashboard domain**.

If Cloudflare is connected, **Manual domain** is behind **Use a different domain without Cloudflare**.

### With Cloudflare

1. Connect Cloudflare under **Settings** → **Connectors** → **Cloudflare**.
2. Open **Settings** → **Domain**. The page shows **Cloudflare connected**, the **Zone**, and the **Dashboard URL**.
3. Choose **Use https://geass.example.com**. This saves the domain using the current **Publish via** choice.
4. To change the zone, the subdomain or **Publish via**, open **Customize zone or subdomain**, set them, and choose
   **Save dashboard domain**. **Publish via** is **DNS to cluster (proxied A record)** or **Cloudflare Tunnel**.

If Geass cannot read the zone name from Cloudflare, the page asks for the **Zone apex domain**.

### The fields

| Field | Meaning | Default |
| --- | --- | --- |
| **Root domain** / **Zone apex domain** | Your main domain. Geass lowercases it and drops `https://`, any path, a trailing dot and a leading `geass.`. It needs at least one dot. | None |
| **Subdomain** | The host label for the dashboard. Lowercase letters, digits and hyphens, up to 63 characters. | `geass` |
| **Cloudflare zone** | The zone Geass writes records in. Appears when the token can read zones. | The connected zone |
| **Publish via** | How Cloudflare reaches the dashboard. | **DNS to cluster (proxied A record)** |

Saving stores the root domain, the dashboard URL `https://<subdomain>.<root domain>` and the publish mode on the platform
configuration. Saving a different domain starts verification again.

Through the API, with a workspace admin token:

```bash title="Set the platform domain"
curl -X POST https://geass.example.com/api/settings/domain/save \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d domain=example.com -d subdomain=geass -d exposure=ingress
```

`exposure` is `ingress` or `cloudflare-tunnel`. For the tunnel, Cloudflare must be connected (or you must send
`tunnelCNAMETarget`). Add `cloudflareZoneId` to choose a zone.

## How Geass verifies the domain

After you save, the operator works through these steps and repeats them every 5 seconds:

1. **Create the dashboard Ingress** (not in tunnel mode): `geass-dashboard` in `geass-system`, class `traefik`, entry point
   `websecure`, TLS secret `geass-dashboard-tls`, and the annotation `cert-manager.io/cluster-issuer: letsencrypt-prod`. It
   routes your hostname to the dashboard Service on port 8085.
2. **Point DNS.**
   - With Cloudflare: create or update a proxied `A` record for the dashboard host with the server's IP.
   - Without Cloudflare: look the host up and compare it with the server's public IP. Geass finds that IP from a node's
     external address, or a public node address, or, failing both, by asking `api.ipify.org` and `ifconfig.me` from the
     operator.
   - In tunnel mode: it checks the host's `CNAME` (when the HTTPS check below does not already succeed).
3. **Probe over HTTPS.** Geass requests `https://geass.example.com/geass-probe`, a public path that answers
   `{"ok":true}`. The request verifies the certificate against the operator's normal trusted certificate authorities, so a
   self-signed or default Traefik certificate fails it.

When the probe answers, the domain is verified, and Geass saves `Verified` on the platform configuration. After 15 minutes
without success the status becomes `VerificationTimedOut`, and Geass keeps trying every 5 minutes, so a late fix still
turns it green. Changing the domain resets the clock.

Nothing in the dashboard shows the progress, so read it from the platform configuration:

```bash title="Read the verification state"
kubectl -n geass-system get geassplatformconfig platform \
  -o jsonpath='{range .status.conditions[*]}{.type}{"\t"}{.status}{"\t"}{.reason}{"\t"}{.message}{"\n"}{end}'
```

The `DashboardDomainReady` condition has these reasons:

| Reason | Message | Meaning |
| --- | --- | --- |
| `NoDomain` | Dashboard domain is not configured | You have not saved a domain. |
| `PendingDNS` | DNS not detected yet, or Could not create dashboard DNS record in Cloudflare, or could not determine this server's public IP | The record is missing or points elsewhere. |
| `PendingHTTPS` | Dashboard HTTPS endpoint is not reachable yet, followed in parentheses by what is wrong with the issuer, when something is (not in tunnel mode) | DNS is right, but `https://…/geass-probe` fails: no certificate, a closed port, or the wrong SSL mode. |
| `ZoneRequired` | Select a Cloudflare zone in domain settings (multiple zones match this token) | Choose a zone in **Customize zone or subdomain**. |
| `PendingTunnel` | Cloudflare tunnel ID is not configured | Tunnel mode without a tunnel yet. |
| `Error` | Cloudflare credentials are unavailable, Cloudflare credentials are incomplete, or dashboard service was not found | Reconnect Cloudflare, or check the dashboard Service. |
| `VerificationTimedOut` | Not verified after 15 minutes: followed by the last message | Fix the cause. Geass retries every 5 minutes. |
| `Verified` | DNS is configured for the dashboard domain, or Dashboard HTTPS endpoint is reachable | Done. |

To check right now instead of waiting, call the verify endpoint. It looks the host up, probes the public URL and, when the
probe works, records `Verified`:

```bash
curl -X POST https://geass.example.com/api/settings/domain/verify \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json"
# {"message":"…","ok":false,"state":"pending"}
```

Its messages are `DNS lookup failed for <host>`, `could not reach the dashboard URL`, `<url> did not return a successful Geass
probe response`, and, when the dashboard answers on the server but not at the public address, `Dashboard responds locally,
but the public URL could not be reached. Check ingress, TLS, and DNS routing.` Except for the DNS lookup message, and
unless the dashboard is published through a tunnel, each of these ends with the issuer's problem in parentheses when the
cluster reports one, for example `(No ACME contact email is configured, so the letsencrypt-prod ClusterIssuer was not
created …)`.

Everything that needs a public dashboard address waits for verification. Registering the GitHub App is the main one; see
[Connect GitHub](/docs/guides/connectors/github/).

## HTTPS and certificates

Geass gets certificates from Let's Encrypt through cert-manager. What it does:

- It installs **cert-manager** (the `geass-cert-manager` Helm chart, `cert-manager` namespace) on every cluster, unless you
  turn the add-on off with `spec.addons.certManager.enabled: false` on the `GeassCluster`.
- Every Ingress that should have a certificate carries `cert-manager.io/cluster-issuer: letsencrypt-prod` and a TLS secret
  name: `geass-dashboard-tls` for the dashboard, and `<service ID>-tls` for a service with **HTTPS** on.
- **When the cluster has an ACME email, it creates the `letsencrypt-prod` ClusterIssuer.** The email is
  `spec.addons.certManager.acmeEmail` on the `GeassCluster`. The installer sets it from `GEASS_ACME_EMAIL`, and you can
  set or change it later. Geass creates the issuer as soon as cert-manager reports ready, and looks again every 30
  seconds until then.

What the issuer is: ACME against Let's Encrypt's production directory
(`https://acme-v02.api.letsencrypt.org/directory`), solving the HTTP-01 challenge through the `traefik` ingress class,
with the account key in the Secret `letsencrypt-prod-account-key` that cert-manager creates in its own namespace. It
carries the labels `geass.dev/managed-by: geass` and `geass.dev/cluster: <GeassCluster name>`, which is how Geass tells it
from an issuer you made yourself.

Set or change the email on a running cluster, including one installed without it. Geass creates or updates the issuer
within seconds:

```bash
sudo k3s kubectl -n geass-system patch geasscluster default --type merge \
  -p '{"spec":{"addons":{"certManager":{"acmeEmail":"you@yourdomain.com"}}}}'
```

Use a real address. Let's Encrypt keeps it as the contact for your account and refuses placeholder domains such as
`example.com`. See [Install](/docs/getting-started/install/#https-certificates) for passing it to the installer.

Geass looks after the issuer like this:

- If you edit the fields it sets (the email, the server, the account key name or the solver), or delete the issuer, it puts
  them back.
- An issuer named `letsencrypt-prod` that Geass did not create is never changed. Once cert-manager reports it ready, Geass
  reports it as `ExternallyManaged`. Use this to keep your own issuer, for example a DNS-01 wildcard one.
- Removing the email later does not delete the issuer Geass made, because certificates issued through it still renew
  through it.
- If another `GeassCluster` owns the issuer, it is left alone (`ManagedByAnotherCluster`).
- With the cert-manager add-on off, Geass manages no issuer.

What you still do yourself:

- **DNS.** Let's Encrypt validates a hostname by requesting it over HTTP, so the name must already point at your server.
  Create the record before you turn **HTTPS** on, because Let's Encrypt limits repeated failed validations.
- **Ports.** Open 80 and 443 to the server, in your provider's firewall and on the host. Geass does not open them, and it
  adds no HTTP to HTTPS redirect.
- **A working email.** If Let's Encrypt refuses it, the issuer never becomes ready.

### Check the issuer and the certificate

The `GeassCluster` carries a `ClusterIssuerReady` condition. It is informational: a cluster without an issuer is still
`Ready` and runs workloads.

```bash
sudo k3s kubectl -n geass-system get geasscluster default \
  -o jsonpath='{.status.conditions[?(@.type=="ClusterIssuerReady")].reason}{": "}{.status.conditions[?(@.type=="ClusterIssuerReady")].message}{"\n"}'
sudo k3s kubectl get clusterissuer letsencrypt-prod
```

`IssuerReady` (status `True`) means the issuer exists and cert-manager registered the ACME account. The common other
reasons are `ACMEEmailMissing` (no email, so no issuer), `WaitingForCertManager`, `IssuerRegistering` and `IssuerNotReady`
(cert-manager's own reason follows, usually Let's Encrypt refusing the email). The full list is in
[Install](/docs/getting-started/install/#https-certificates).

Then follow one certificate. cert-manager creates a `Certificate` named after the TLS secret the first time it sees the
Ingress:

```bash
sudo k3s kubectl get certificate -A
sudo k3s kubectl -n geass-system describe certificate geass-dashboard-tls
sudo k3s kubectl -n proj-1a2b3c4d-production describe certificate amber-fern-tls
sudo k3s kubectl get order,challenge -A
```

A certificate that is `Ready: True` is issued. One that stays `False` is waiting on its `Order` and `Challenge`, which name
the reason: usually DNS that does not point at the server, or port 80 closed.

Behavior to know:

- An Ingress with **HTTPS** off serves plain HTTP only, on Traefik's `web` entry point. An Ingress with **HTTPS** on
  serves HTTPS only, on `websecure`. Geass adds no redirect, so `http://` to a service with HTTPS on does not answer.
- Until the certificate exists, Traefik answers with its own default certificate, and browsers warn.
- With a **Cloudflare Tunnel**, traffic does not pass through Traefik, so neither the issuer nor **HTTPS** matters:
  Cloudflare presents the certificate for the hostname. A cluster without an email still shows `ACMEEmailMissing`, which
  you can ignore in this mode.
- With **DNS to cluster**, Cloudflare presents its certificate to visitors, and your server presents its own to
  Cloudflare. Geass does not change your zone's **SSL/TLS** encryption mode. The dashboard's Ingress only listens on
  HTTPS, so a mode that connects to your server over plain HTTP cannot reach it. Cloudflare's proxy is also in the path of
  Let's Encrypt's request, so if a certificate is never issued, check your zone's SSL/TLS mode and any redirect rules.

## Cloudflare-managed DNS

Connecting Cloudflare lets Geass manage DNS in one zone. What it creates depends on **Publish via**:

| Publish via | In Cloudflare | In the cluster |
| --- | --- | --- |
| **DNS to cluster (proxied A record)** | A proxied `A` record for the dashboard host, pointing at the server's IP. Nothing for services. | Nothing for Cloudflare. |
| **Cloudflare Tunnel** | A tunnel named `geass`, its routes, and a proxied `CNAME` to `<tunnel ID>.cfargotunnel.com` for the dashboard host and for each service host in the zone. | Deployment `geass-cloudflared` and Secret `geass-cloudflared-token` in `geass-system`. |

The token needs **Zone** → **Zone** → **Read** and **Zone** → **DNS** → **Edit**, and **Account** → **Cloudflare Tunnel** →
**Edit** for tunnel mode. See [Connect Cloudflare](/docs/guides/connectors/cloudflare/) for connecting, rotating and
disconnecting.

:::caution
In tunnel mode Geass creates the dashboard's tunnel route and `CNAME` record for `geass.<root domain>`, whatever
**Subdomain** you chose, while it checks the dashboard at `<subdomain>.<root domain>`. Keep the default subdomain
`geass` when you publish through a tunnel. A different subdomain never verifies.
:::

**Zone discovery.** When you paste a token, Geass lists the zones it can read. With one zone it picks it. With several, you
choose, and the choice is stored. The root domain you save should be the zone you chose, because that is the only zone
Geass writes in. **Refresh zones** on the Cloudflare card re-reads the list.

**When the tunnel is synced.** In tunnel mode Geass works out the routes and DNS records from the dashboard host and from
every service that has a host, and sends them to Cloudflare when that set changes: when a service gets, changes or loses a
host, or when you change the domain or the token. When nothing changed it sends nothing and looks again every 5 minutes,
which repairs a route or a record that someone deleted in Cloudflare. While the dashboard domain is not verified, the
operator looks again every 5 seconds. The route of a service that [sleeps when idle](/docs/guides/scale-to-zero/) points at
the scale-to-zero proxy and not at the service, so a request through the tunnel wakes it.

**What it does not do.** Geass does not create records for services in **DNS to cluster** mode, does not change SSL/TLS
modes, and does not delete records or the tunnel when you disconnect.

## Default hostnames

When Cloudflare is connected and ready and a service has no hostname, Geass gives it one:

```text
<service ID>-<project ID>.<root domain>
<service ID>-<project ID>-<environment>.<root domain>     (environments other than production)
```

For example, `amber-fern-proj-1a2b3c4d.example.com` and `amber-fern-proj-1a2b3c4d-staging.example.com`. Geass fills it in
the next time it reconciles the service, including drafts, and saves it as the service's **Custom domain** with HTTPS off.
The displayed address therefore starts with `http://` even when Cloudflare serves it over HTTPS. If you clear the field
while Cloudflare is connected, Geass fills it in again.

Without Cloudflare, services get no default hostname. A default hostname is a host like any other, so it also lets a service
[sleep when idle](/docs/guides/scale-to-zero/), which needs one.

In tunnel mode Geass creates the DNS record and route for it. In **DNS to cluster** mode it does not, so a record must
exist, for example a wildcard `*.example.com` pointing at the server, which covers these one-label names.

## Give a service a domain

1. Create the DNS record: `shop.example.com` → `203.0.113.10` (type `A`), or let Cloudflare do it in tunnel mode.
2. Open the service, then **Settings** → **Networking**, and type the name in **Custom domain**: the hostname only, in
   lowercase, with no `https://` and no path. Choose **Save changes**. It applies at once.
3. Open `http://shop.example.com/`.
4. For HTTPS, check that the cluster has an issuer ([above](#https-and-certificates)) and that the DNS record from step 1
   resolves, then turn on **HTTPS** and choose **Save changes**. Check `https://shop.example.com/` once the certificate is
   issued. Follow it with `kubectl -n <namespace> describe certificate <service ID>-tls`.

The Ingress is created when the service is deployed, so a draft shows a domain but serves nothing until you deploy.
Under the fields, **Public networking** shows the address and a hint such as "Routed through the cluster ingress".

**Verify DNS.** Turn it on and Geass resolves the hostname when it reconciles the service, within 2 seconds, and records
whether it resolved. If it did not, the line under the address reads "DNS hostname is required", whatever the cause. The
check accepts any answer, so it does not prove the name points at this cluster. It does not stop traffic and does not
change the service's status, even though the form says it checks "before reporting the domain as ready". Geass does not
poll: it runs again the next time the service is reconciled.

**Paths and several hosts.** The dashboard form takes one host and serves it at `/`. The `GeassApp` also accepts
`spec.ingress.path`, and `spec.ingress.rules` for several host and path pairs. See [Apps](/docs/guides/apps/#manifest-and-api).
In tunnel mode only `spec.ingress.host` with `spec.ingress.path` is added to the tunnel's routes and DNS. A host that is
only in `rules` reaches the service through Traefik, and so needs its own record.

**Sleeping services.** A service that [sleeps when idle](/docs/guides/scale-to-zero/) needs a host, because the requests to it
wake it. Its Ingress sends traffic to the Service `<service ID>-wake`, a name for the scale-to-zero proxy, and not to the
service itself.

## What happens under the hood

| Object | Where | Purpose |
| --- | --- | --- |
| `GeassPlatformConfig` `platform` | `geass-system` | `spec.rootDomain`, `spec.dashboardURL`, `spec.dashboardExposure`, `spec.cloudflareZoneID`, and the conditions above. |
| Ingress `geass-dashboard` | `geass-system` | The dashboard route (not in tunnel mode). |
| Ingress `<service ID>` | `<project ID>-<environment>` | A service's route, when it has a host. For a service that sleeps when idle, its backend is the Service `<service ID>-wake`. |
| Secret `geass-dashboard-tls`, `<service ID>-tls` | Same as the Ingress | The certificate, once issued. |
| Deployment `geass-cloudflared` | `geass-system` | The tunnel connector (tunnel mode). |
| `ClusterIssuer` `letsencrypt-prod` | cluster-wide | The issuer every certificate is requested from. Geass creates it when the cluster has an ACME email. |
| Condition `ClusterIssuerReady` | On the `GeassCluster` | Whether certificates can be issued. |

```bash title="Inspect domains and certificates"
kubectl get pods -n cert-manager
kubectl get clusterissuer letsencrypt-prod
kubectl -n geass-system get geasscluster default -o jsonpath='{.status.conditions[?(@.type=="ClusterIssuerReady")].message}{"\n"}'
kubectl get certificate,order,challenge -A
kubectl -n geass-system describe ingress geass-dashboard
kubectl -n proj-1a2b3c4d-production get ingress amber-fern -o yaml
kubectl -n geass-system get geassplatformconfig platform -o yaml
```

## Troubleshooting

**The "Dashboard domain" banner never goes away.** The domain is not verified. Read the condition reason (see above). Usual
causes: the DNS record is missing or points at another server, no certificate (the cluster has no ACME email, so Geass
created no `letsencrypt-prod` issuer), or a port is closed. **Settings** → **Domain** does not show the status. The banner
does not show inside a project.

**`PendingDNS` with "DNS not detected yet".** The host does not resolve to the server's public IP from inside the cluster.
Check the record with `dig +short geass.example.com`. In tunnel mode the message means the host's `CNAME` does not point at
the tunnel yet. (When Geass manages the record through Cloudflare it does not compare IP addresses, so a proxied record
that resolves to Cloudflare's addresses is fine.)

**`PendingHTTPS`, or "could not reach the dashboard URL".** DNS is right but the HTTPS probe fails. Check that ports 80 and
443 reach the server, that `kubectl get clusterissuer letsencrypt-prod` finds an issuer, and `kubectl get certificate -n
geass-system`. The message after `PendingHTTPS` often names the issuer problem. A certificate that is not `Ready` is why:
`kubectl describe certificate geass-dashboard-tls -n geass-system` and the `challenge` objects say why. With Cloudflare in
front, also check the zone's SSL/TLS mode.

**"Dashboard responds locally, but the public URL could not be reached."** The operator is fine. The public path
(DNS, ports, Ingress, certificate or Cloudflare) is not.

**`ZoneRequired`.** The token sees more than one zone. Pick one under **Customize zone or subdomain**.

**The browser warns about the certificate, or shows "TRAEFIK DEFAULT CERT".** There is no issued certificate. Check
`ClusterIssuerReady` first, then `kubectl get certificate -A`. A certificate that stays `False` usually means DNS
did not point at the server, or port 80 was closed, when cert-manager tried.

**`ClusterIssuerReady` says `ACMEEmailMissing`.** The cluster has no contact email, so Geass created no issuer. Set
`spec.addons.certManager.acmeEmail` (see [HTTPS and certificates](#https-and-certificates)).

**`ClusterIssuerReady` says `IssuerNotReady`.** cert-manager could not register the Let's Encrypt account. The message
carries its reason. The usual cause is an email at a domain Let's Encrypt refuses, such as `example.com`. Set a real
address.

**`kubectl get certificate -A` shows nothing.** cert-manager did not act on the Ingress. The issuer is missing (the cert-manager
log complains that the issuer is not found), the add-on is off, or the Ingress has HTTPS off.

**A service domain returns `404 page not found`.** Traefik has no route for that name. The service is not deployed yet, the
**Custom domain** has more than the hostname, or the name reaches another server. Check
`kubectl -n <namespace> get ingress`.

**`502` or `503` on a service domain.** The route exists but no pod answers. See
[Apps](/docs/guides/apps/#troubleshooting).

**A default hostname does not resolve.** In **DNS to cluster** mode Geass does not create records for services. Add one, or
a wildcard, or switch to **Cloudflare Tunnel**.

**Cloudflare shows a 5xx error page for a service.** Geass does not manage your zone's SSL/TLS mode. Set it so Cloudflare
reaches your server over HTTPS, make sure the origin has a certificate if the mode requires a valid one, or use the tunnel.

**I changed the root domain and the dashboard stopped working.** The old Ingress is replaced with the new host, but DNS and
the certificate must follow. Verification starts again. Keep the old record until the new address works.

## Related

- [Connect a domain](/docs/getting-started/connect-domain/): a short walkthrough.
- [Connect Cloudflare](/docs/guides/connectors/cloudflare/)
- [Apps](/docs/guides/apps/)
- [GitHub and builds](/docs/guides/github-and-builds/)
- [Security](/docs/operations/security/)
- [Troubleshooting](/docs/operations/troubleshooting/)
