---
title: "Domains and TLS"
description: "Publish the dashboard and your services on your domain, use Cloudflare for DNS, and get HTTPS certificates from Let's Encrypt."
---

Geass publishes two kinds of things on your domain: the **dashboard**, and the **services** that you deploy. You set a
**platform domain** one time, under **Settings** → **Domain**. Services then get hostnames of their own. This page
covers these topics:

- How to set the platform domain.
- How Geass checks the platform domain.
- Cloudflare-managed DNS.
- Custom domains on a service.
- How Geass gets HTTPS certificates, including the contact email that it needs.

:::note
On a Geass that a platform runs for you, [managed mode](/docs/operations/managed-mode/) might be on. If managed mode is
on, the dashboard's domain belongs to the platform. The Cloudflare connection that writes the DNS records of the
dashboard also belongs to the platform. **Settings** → **Domain** shows read-only, and the API refuses to change these
settings (`403`, `code` `managed`). The hostnames of services are not affected.
:::

:::caution
To get certificates, give the cluster a contact email for Let's Encrypt. Geass creates the certificate issuer only when
the cluster has this email. Every HTTPS Ingress that Geass creates, for the dashboard and for services, asks
cert-manager for its certificate from a `ClusterIssuer` named `letsencrypt-prod`. Geass creates that issuer from the
email that you give the installer (`GEASS_ACME_EMAIL`), or that you set on the cluster later. Without an email, these
problems occur:

- There is no issuer.
- No certificate is issued.
- The dashboard domain stays unverified. A Cloudflare Tunnel avoids this problem. Cloudflare's proxy can also avoid
  it, depending on the SSL/TLS mode of your zone.
- The **HTTPS** toggle of a service gives you the browser's certificate warning.

For more information, see [HTTPS and certificates](#https-and-certificates).
:::

## Three ways to publish

Select one of these setups when you set the platform domain.

| Setup | DNS | Where TLS ends | What you do yourself |
| --- | --- | --- | --- |
| **Manual DNS** | You create an `A` record at your DNS provider. | On your server, in Traefik, with a certificate from cert-manager. | Create the DNS record. Open ports 80 and 443. Give the cluster a Let's Encrypt email. |
| **Cloudflare, DNS to cluster** (**DNS to cluster (proxied A record)**) | Geass creates a proxied `A` record for the dashboard in your Cloudflare zone. | At Cloudflare for visitors. Between Cloudflare and your server, it depends on the SSL/TLS mode of your zone. The server's own certificate comes from cert-manager. | Connect Cloudflare. Open ports 80 and 443. Give the cluster a Let's Encrypt email. You create the records for service hostnames. |
| **Cloudflare Tunnel** (**Cloudflare Tunnel**) | Geass creates proxied `CNAME` records for the dashboard and every service hostname. | At Cloudflare. A `cloudflared` pod in the cluster connects out to Cloudflare. | Connect Cloudflare. No inbound ports, no issuer. |

The installer does not open ports 80 or 443 on your server or on the firewall of your provider. Let's Encrypt validates
a hostname over port 80, and visitors use 443. For more information, see
[Install](/docs/getting-started/install/#https-certificates) and
[Ports and firewall](/docs/getting-started/requirements/#ports-and-firewall).

## Before you start

- A domain that you control, and an admin login to the dashboard.
- The public IP address of the server that runs Geass. This page uses `203.0.113.10`.
- For the manual setup or Cloudflare DNS to cluster:
  - Ports 80 and 443 reachable on that server. K3s runs Traefik, which listens on these ports.
  - A real email address for the Let's Encrypt account. Give it to the installer as `GEASS_ACME_EMAIL`, or set it on
    the cluster later. For more information, see [HTTPS and certificates](#https-and-certificates). Let's Encrypt
    refuses placeholder domains such as `example.com`.
- For Cloudflare: a connection, set up as described in [Connect Cloudflare](/docs/guides/connectors/cloudflare/).

## Set the platform domain

The **platform domain** (the **Root domain**) is your main domain, for example `example.com`. By default, the dashboard
goes on `geass.example.com`. Services that get default hostnames also use the platform domain. For more information,
see [Default hostnames](#default-hostnames).

Until the dashboard domain is set and verified, admins see a banner with **Configure domain**. The banner says
"Dashboard domain. Set a public host so Geass can verify HTTPS and finish platform setup." The banner shows above the
pages outside a project. These pages are **Projects**, **Templates**, **Usage**, **Pools**, and **Settings**, except
**Settings** → **Domain**. The banner does not show inside a project. The banner goes away after the domain is verified.

### Without Cloudflare

1. At your DNS provider, create a DNS record `geass.example.com` → `203.0.113.10` (type `A`).
2. Make sure that the cluster has a Let's Encrypt email. With this email, Geass creates the `letsencrypt-prod` issuer.
   For more information, see [HTTPS and certificates](#https-and-certificates).
3. Open **Settings** → **Domain**.
4. In **Manual domain**, enter the **Root domain** (`example.com`, apex only).
5. Optional: enter a **Subdomain**. The default is `geass`. The page previews the **Dashboard URL**.
6. Click **Save dashboard domain**.

If Cloudflare is connected, **Manual domain** is behind **Use a different domain without Cloudflare**.

### With Cloudflare

1. Connect Cloudflare under **Settings** → **Connectors** → **Cloudflare**.
2. Open **Settings** → **Domain**. The page shows **Cloudflare connected**, the **Zone**, and the **Dashboard URL**.
3. Click **Use https://geass.example.com**. Geass saves the domain with the current **Publish via** selection.
4. To change the zone, the subdomain, or **Publish via**, do these steps:
   1. Open **Customize zone or subdomain**.
   2. Set the values. **Publish via** is **DNS to cluster (proxied A record)** or **Cloudflare Tunnel**.
   3. Click **Save dashboard domain**.

If Geass cannot read the zone name from Cloudflare, the page asks for the **Zone apex domain**.

### The fields

| Field | Meaning | Default |
| --- | --- | --- |
| **Root domain** / **Zone apex domain** | Your main domain. Geass lowercases it and drops `https://`, any path, a trailing dot, and a leading `geass.`. It needs at least one dot. | None |
| **Subdomain** | The host label for the dashboard. Lowercase letters, digits, and hyphens, up to 63 characters. | `geass` |
| **Cloudflare zone** | The zone that Geass writes records in. Appears when the token can read zones. | The connected zone |
| **Publish via** | How Cloudflare reaches the dashboard. | **DNS to cluster (proxied A record)** |

When you save, Geass stores these values on the platform configuration: the root domain, the dashboard URL
`https://<subdomain>.<root domain>`, and the publish mode. If you save a different domain, verification starts again.

You can also set the domain through the API, with a workspace admin token:

```bash title="Set the platform domain"
curl -X POST https://geass.example.com/api/settings/domain/save \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d domain=example.com -d subdomain=geass -d exposure=ingress
```

`exposure` is `ingress` or `cloudflare-tunnel`. For the tunnel, Cloudflare must be connected, or you must send
`tunnelCNAMETarget`. To select a zone, add `cloudflareZoneId`.

## How Geass verifies the domain

After you save, the operator does these steps, and repeats them every 5 seconds:

1. **Create the dashboard Ingress** (not in tunnel mode). The operator creates `geass-dashboard` in `geass-system`
   with these values: class `traefik`, entry point `websecure`, TLS secret `geass-dashboard-tls`, and the annotation
   `cert-manager.io/cluster-issuer: letsencrypt-prod`. The Ingress routes your hostname to the dashboard Service on
   port 8085.
2. **Point DNS.**
   - With Cloudflare: Geass creates or updates a proxied `A` record for the dashboard host with the server's IP.
   - Without Cloudflare: Geass looks up the host and compares it with the public IP of the server. Geass finds that IP
     from the first of these sources that works:
     - The external address of a node.
     - A public node address.
     - A request from the operator to `api.ipify.org` and `ifconfig.me`.
   - In tunnel mode: Geass checks the `CNAME` of the host, when the HTTPS check below does not already succeed.
3. **Probe over HTTPS.** Geass requests `https://geass.example.com/geass-probe`, a public path that answers
   `{"ok":true}`. The request verifies the certificate against the normal trusted certificate authorities of the
   operator. Thus, a self-signed or default Traefik certificate fails the probe.

When the probe answers, the domain is verified, and Geass saves `Verified` on the platform configuration. If the
domain is not verified after 15 minutes, the status becomes `VerificationTimedOut`. Geass then continues to try every 5
minutes. Thus, a late fix still turns the status green. When you change the domain, the clock resets.

The dashboard does not show the progress. To see the progress, read it from the platform configuration:

```bash title="Read the verification state"
kubectl -n geass-system get geassplatformconfig platform \
  -o jsonpath='{range .status.conditions[*]}{.type}{"\t"}{.status}{"\t"}{.reason}{"\t"}{.message}{"\n"}{end}'
```

The `DashboardDomainReady` condition has these reasons:

| Reason | Message | Meaning |
| --- | --- | --- |
| `NoDomain` | Dashboard domain is not configured | You have not saved a domain. |
| `PendingDNS` | DNS not detected yet, or Could not create dashboard DNS record in Cloudflare, or could not determine this server's public IP | The record is missing or points to a different address. |
| `PendingHTTPS` | Dashboard HTTPS endpoint is not reachable yet, followed in parentheses by what is wrong with the issuer, when something is (not in tunnel mode) | DNS is correct, but `https://…/geass-probe` fails: no certificate, a closed port, or the wrong SSL mode. |
| `ZoneRequired` | Select a Cloudflare zone in domain settings (multiple zones match this token) | Select a zone in **Customize zone or subdomain**. |
| `PendingTunnel` | Cloudflare tunnel ID is not configured | Tunnel mode without a tunnel yet. |
| `Error` | Cloudflare credentials are unavailable, Cloudflare credentials are incomplete, or dashboard service was not found | Reconnect Cloudflare, or check the dashboard Service. |
| `VerificationTimedOut` | Not verified after 15 minutes: followed by the last message | Fix the cause. Geass retries every 5 minutes. |
| `Verified` | DNS is configured for the dashboard domain, or Dashboard HTTPS endpoint is reachable | Done. |

To check now instead of waiting, call the verify endpoint. The endpoint looks up the host and probes the public URL.
When the probe works, the endpoint records `Verified`:

```bash
curl -X POST https://geass.example.com/api/settings/domain/verify \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json"
# {"message":"…","ok":false,"state":"pending"}
```

The endpoint can return these messages:

- `DNS lookup failed for <host>`
- `could not reach the dashboard URL`
- `<url> did not return a successful Geass probe response`
- `Dashboard responds locally, but the public URL could not be reached. Check ingress, TLS, and DNS routing.` The
  endpoint returns this message when the dashboard answers on the server but not at the public address.

The cluster might report a problem with the issuer. If so, each of these messages ends with that problem in
parentheses, for example `(No ACME contact email is configured, so the letsencrypt-prod ClusterIssuer was not created …)`.
The exceptions are the DNS lookup message, and all messages when the dashboard is published through a tunnel.

Everything that needs a public dashboard address waits for verification. The main item is the registration of the
GitHub App. For more information, see [Connect GitHub](/docs/guides/connectors/github/).

## HTTPS and certificates

Geass gets certificates from Let's Encrypt through cert-manager. Geass does these things:

- It installs **cert-manager** (the `geass-cert-manager` Helm chart, `cert-manager` namespace) on every cluster. To
  turn this add-on off, set `spec.addons.certManager.enabled: false` on the `GeassCluster`.
- Every Ingress that should have a certificate carries `cert-manager.io/cluster-issuer: letsencrypt-prod` and a TLS
  secret name. The name is `geass-dashboard-tls` for the dashboard, and `<service ID>-tls` for a service with
  **HTTPS** on.
- **When the cluster has an ACME email, it creates the `letsencrypt-prod` ClusterIssuer.** The email is
  `spec.addons.certManager.acmeEmail` on the `GeassCluster`. The installer sets it from `GEASS_ACME_EMAIL`, and you can
  set or change it later. Geass creates the issuer when cert-manager reports ready. Until then, Geass looks again every
  30 seconds.

The issuer has these properties:

- It uses ACME against the production directory of Let's Encrypt
  (`https://acme-v02.api.letsencrypt.org/directory`).
- It solves the HTTP-01 challenge through the `traefik` ingress class.
- Its account key is in the Secret `letsencrypt-prod-account-key`. cert-manager creates this Secret in its own
  namespace.
- It carries the labels `geass.dev/managed-by: geass` and `geass.dev/cluster: <GeassCluster name>`. Geass uses these
  labels to tell its issuer from an issuer that you made yourself.

You can set or change the email on a running cluster, including a cluster that you installed without an email. Geass
creates or updates the issuer within seconds:

```bash
sudo k3s kubectl -n geass-system patch geasscluster default --type merge \
  -p '{"spec":{"addons":{"certManager":{"acmeEmail":"you@yourdomain.com"}}}}'
```

Use a real address. Let's Encrypt keeps the address as the contact for your account, and it refuses placeholder
domains such as `example.com`. To give the address to the installer, see
[Install](/docs/getting-started/install/#https-certificates).

Geass looks after the issuer like this:

- If you edit the fields that Geass sets, Geass puts them back. These fields are the email, the server, the account
  key name, and the solver. If you delete the issuer, Geass puts it back.
- Geass never changes an issuer named `letsencrypt-prod` that Geass did not create. After cert-manager reports this
  issuer ready, Geass reports it as `ExternallyManaged`. Use this behavior to keep your own issuer, for example a DNS-01
  wildcard issuer.
- If you remove the email later, Geass does not delete the issuer that it made. The reason is that certificates issued
  through this issuer still renew through it.
- If another `GeassCluster` owns the issuer, Geass leaves it alone (`ManagedByAnotherCluster`).
- If the cert-manager add-on is off, Geass manages no issuer.

You must still do these things yourself:

- **DNS.** Let's Encrypt validates a hostname with a request over HTTP. Thus, the name must already point to your
  server. Create the record before you turn **HTTPS** on, because Let's Encrypt limits repeated failed validations.
- **Ports.** Open 80 and 443 to the server, in the firewall of your provider and on the host. Geass does not open
  these ports, and it adds no HTTP to HTTPS redirect.
- **A working email.** If Let's Encrypt refuses the email, the issuer never becomes ready.

### Check the issuer and the certificate

The `GeassCluster` carries a `ClusterIssuerReady` condition. This condition is for information only. A cluster without
an issuer is still `Ready` and runs workloads.

```bash
sudo k3s kubectl -n geass-system get geasscluster default \
  -o jsonpath='{.status.conditions[?(@.type=="ClusterIssuerReady")].reason}{": "}{.status.conditions[?(@.type=="ClusterIssuerReady")].message}{"\n"}'
sudo k3s kubectl get clusterissuer letsencrypt-prod
```

`IssuerReady` (status `True`) means that the issuer exists and cert-manager registered the ACME account. The common
other reasons are:

- `ACMEEmailMissing`: there is no email, so there is no issuer.
- `WaitingForCertManager`.
- `IssuerRegistering`.
- `IssuerNotReady`: the reason from cert-manager follows. Usually, Let's Encrypt refused the email.

For the full list, see [Install](/docs/getting-started/install/#https-certificates).

Then follow one certificate. The first time that cert-manager sees the Ingress, it creates a `Certificate` named after
the TLS secret:

```bash
sudo k3s kubectl get certificate -A
sudo k3s kubectl -n geass-system describe certificate geass-dashboard-tls
sudo k3s kubectl -n proj-1a2b3c4d-production describe certificate amber-fern-tls
sudo k3s kubectl get order,challenge -A
```

A certificate that is `Ready: True` is issued. A certificate that stays `False` waits on its `Order` and `Challenge`.
These objects name the reason. Usually, DNS does not point to the server, or port 80 is closed.

Behavior to know:

- An Ingress with **HTTPS** off serves plain HTTP only, on the `web` entry point of Traefik. An Ingress with **HTTPS**
  on serves HTTPS only, on `websecure`. Geass adds no redirect. Thus, `http://` to a service with HTTPS on does not
  answer.
- Until the certificate exists, Traefik answers with its own default certificate, and browsers show a warning.
- With a **Cloudflare Tunnel**, traffic does not go through Traefik. Thus, the issuer and **HTTPS** do not matter.
  Cloudflare presents the certificate for the hostname. A cluster without an email still shows `ACMEEmailMissing`. You
  can ignore this reason in tunnel mode.
- With **DNS to cluster**, Cloudflare presents its certificate to visitors, and your server presents its own
  certificate to Cloudflare. These points apply:
  - Geass does not change the **SSL/TLS** encryption mode of your zone.
  - The dashboard's Ingress listens only on HTTPS. Thus, a mode that connects to your server over plain HTTP cannot
    reach the Ingress.
  - Cloudflare's proxy is also in the path of the request from Let's Encrypt. If a certificate is never issued, check
    the SSL/TLS mode of your zone and any redirect rules.

## Cloudflare-managed DNS

When you connect Cloudflare, Geass can manage DNS in one zone. What Geass creates depends on **Publish via**:

| Publish via | In Cloudflare | In the cluster |
| --- | --- | --- |
| **DNS to cluster (proxied A record)** | A proxied `A` record for the dashboard host, pointing to the server's IP. Nothing for services. | Nothing for Cloudflare. |
| **Cloudflare Tunnel** | A tunnel named `geass`, its routes, and a proxied `CNAME` to `<tunnel ID>.cfargotunnel.com` for the dashboard host and for each service host in the zone. | Deployment `geass-cloudflared` and Secret `geass-cloudflared-token` in `geass-system`. |

The token needs **Zone** → **Zone** → **Read** and **Zone** → **DNS** → **Edit**. For tunnel mode, the token also
needs **Account** → **Cloudflare Tunnel** → **Edit**. To connect, rotate, and disconnect, see
[Connect Cloudflare](/docs/guides/connectors/cloudflare/).

:::caution
Keep the default subdomain `geass` when you publish through a tunnel. A different subdomain never verifies. In tunnel
mode, Geass creates the dashboard's tunnel route and `CNAME` record for `geass.<root domain>`, whatever **Subdomain**
you selected. But Geass checks the dashboard at `<subdomain>.<root domain>`.
:::

**Zone discovery.** When you paste a token, Geass lists the zones that the token can read. If there is one zone, Geass
selects it. If there are several zones, you select one, and Geass stores your selection. The root domain that you save
should be the zone that you selected, because Geass writes only in that zone. **Refresh zones** on the Cloudflare card
reads the list again.

**When the tunnel is synced.** In tunnel mode, Geass calculates the routes and DNS records. It uses the dashboard host
and every service that has a host. Geass sends them to Cloudflare when that set changes. The set changes in these
cases:

- A service gets, changes, or loses a host.
- You change the domain or the token.

When nothing changed, Geass sends nothing, and it looks again every 5 minutes. This check repairs a route or a record
that someone deleted in Cloudflare. When the dashboard domain is not verified, the operator looks again every 5
seconds. The route of a service that [sleeps when idle](/docs/guides/scale-to-zero/) points to the scale-to-zero proxy,
not to the service. Thus, a request through the tunnel wakes the service.

**What it does not do.** Geass does not do these things:

- Create records for services in **DNS to cluster** mode.
- Change SSL/TLS modes.
- Delete records or the tunnel when you disconnect.

## Default hostnames

When Cloudflare is connected and ready, and a service has no hostname, Geass gives the service a hostname:

```text
<service ID>-<project ID>.<root domain>
<service ID>-<project ID>-<environment>.<root domain>     (environments other than production)
```

For example, `amber-fern-proj-1a2b3c4d.example.com` and `amber-fern-proj-1a2b3c4d-staging.example.com`. Geass fills in
the hostname the next time that it reconciles the service, including drafts. Geass saves the hostname as the
**Custom domain** of the service, with HTTPS off. Thus, the displayed address starts with `http://`, even when
Cloudflare serves it over HTTPS. If you clear the field when Cloudflare is connected, Geass fills it in again.

Without Cloudflare, services get no default hostname. A default hostname is a host like any other. Thus, with a default
hostname, a service can also [sleep when idle](/docs/guides/scale-to-zero/), which needs a host.

In tunnel mode, Geass creates the DNS record and route for the default hostname. In **DNS to cluster** mode, Geass does
not. Thus, a record must exist, for example a wildcard `*.example.com` that points to the server. A wildcard covers
these one-label names.

## Give a service a domain

1. Create the DNS record: `shop.example.com` → `203.0.113.10` (type `A`). In tunnel mode, Cloudflare can do this step
   for you.
2. Open the service, then **Settings** → **Networking**.
3. In **Custom domain**, enter the name. Enter the hostname only, in lowercase, with no `https://` and no path.
4. Click **Save changes**. The change applies immediately.
5. Open `http://shop.example.com/`.
6. For HTTPS, make sure that the cluster has an issuer. For more information, see
   [HTTPS and certificates](#https-and-certificates).
7. Make sure that the DNS record from step 1 resolves.
8. Turn on **HTTPS**, and click **Save changes**.
9. After the certificate is issued, check `https://shop.example.com/`. To follow the certificate, run
   `kubectl -n <namespace> describe certificate <service ID>-tls`.

Geass creates the Ingress when the service is deployed. Thus, a draft shows a domain but serves nothing until you
deploy. Under the fields, **Public networking** shows the address and a hint such as "Routed through the cluster
ingress".

**Verify DNS.** When you turn this setting on, Geass resolves the hostname when it reconciles the service, within 2
seconds. Geass records whether the hostname resolved. If the hostname did not resolve, the line under the address reads
"DNS hostname is required", whatever the cause. These limits apply:

- The check accepts any answer. Thus, the check does not prove that the name points to this cluster.
- The check does not stop traffic, and it does not change the status of the service. The form says that it checks
  "before reporting the domain as ready", but the check does not affect the status.
- Geass does not poll. The check runs again the next time that Geass reconciles the service.

**Paths and several hosts.** The dashboard form takes one host and serves it at `/`. The `GeassApp` also accepts
`spec.ingress.path`, and `spec.ingress.rules` for several host and path pairs. For more information, see
[Apps](/docs/guides/apps/#manifest-and-api). In tunnel mode, Geass adds only `spec.ingress.host` with
`spec.ingress.path` to the routes and DNS of the tunnel. A host that is only in `rules` reaches the service through
Traefik. Thus, that host needs its own record.

**Sleeping services.** A service that [sleeps when idle](/docs/guides/scale-to-zero/) needs a host, because the requests
to the service wake it. The Ingress of the service sends traffic to the Service `<service ID>-wake`, not to the service
itself. `<service ID>-wake` is a name for the scale-to-zero proxy.

## What happens under the hood

| Object | Where | Purpose |
| --- | --- | --- |
| `GeassPlatformConfig` `platform` | `geass-system` | `spec.rootDomain`, `spec.dashboardURL`, `spec.dashboardExposure`, `spec.cloudflareZoneID`, and the conditions above. |
| Ingress `geass-dashboard` | `geass-system` | The dashboard route. Not in tunnel mode. |
| Ingress `<service ID>` | `<project ID>-<environment>` | The route of a service, when it has a host. For a service that sleeps when idle, its backend is the Service `<service ID>-wake`. |
| Secret `geass-dashboard-tls`, `<service ID>-tls` | Same as the Ingress | The certificate, after it is issued. |
| Deployment `geass-cloudflared` | `geass-system` | The tunnel connector, in tunnel mode. |
| `ClusterIssuer` `letsencrypt-prod` | cluster-wide | The issuer that every certificate is requested from. Geass creates it when the cluster has an ACME email. |
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

**The "Dashboard domain" banner never goes away.** The domain is not verified. Read the condition reason, as described
above. The usual causes are:

- The DNS record is missing, or it points to another server.
- There is no certificate. The cluster has no ACME email, so Geass created no `letsencrypt-prod` issuer.
- A port is closed.

**Settings** → **Domain** does not show the status. The banner does not show inside a project.

**`PendingDNS` with "DNS not detected yet".** From inside the cluster, the host does not resolve to the public IP of the
server. Check the record with `dig +short geass.example.com`. In tunnel mode, the message means that the `CNAME` of the
host does not point to the tunnel yet. When Geass manages the record through Cloudflare, Geass does not compare IP
addresses. Thus, a proxied record that resolves to Cloudflare's addresses is correct.

**`PendingHTTPS`, or "could not reach the dashboard URL".** DNS is correct, but the HTTPS probe fails. Do these checks:

- Make sure that ports 80 and 443 reach the server.
- Make sure that `kubectl get clusterissuer letsencrypt-prod` finds an issuer.
- Run `kubectl get certificate -n geass-system`.

The message after `PendingHTTPS` often names the issuer problem. If a certificate is not `Ready`, that is the cause.
`kubectl describe certificate geass-dashboard-tls -n geass-system` and the `challenge` objects tell you why. With
Cloudflare in front, also check the SSL/TLS mode of the zone.

**"Dashboard responds locally, but the public URL could not be reached."** The operator works. The public path does
not work. The public path is DNS, ports, Ingress, certificate, or Cloudflare.

**`ZoneRequired`.** The token sees more than one zone. Select one under **Customize zone or subdomain**.

**The browser warns about the certificate, or shows "TRAEFIK DEFAULT CERT".** There is no issued certificate. First,
check `ClusterIssuerReady`. Then run `kubectl get certificate -A`. A certificate that stays `False` usually means that,
when cert-manager tried, DNS did not point to the server or port 80 was closed.

**`ClusterIssuerReady` says `ACMEEmailMissing`.** The cluster has no contact email, so Geass created no issuer. Set
`spec.addons.certManager.acmeEmail`. For more information, see [HTTPS and certificates](#https-and-certificates).

**`ClusterIssuerReady` says `IssuerNotReady`.** cert-manager could not register the Let's Encrypt account. The message
carries the reason. The usual cause is an email at a domain that Let's Encrypt refuses, such as `example.com`. Set a
real address.

**`kubectl get certificate -A` shows nothing.** cert-manager did not act on the Ingress. One of these conditions is
true:

- The issuer is missing. The cert-manager log says that the issuer is not found.
- The add-on is off.
- The Ingress has HTTPS off.

**A service domain returns `404 page not found`.** Traefik has no route for that name. One of these conditions is true:

- The service is not deployed yet.
- The **Custom domain** has more than the hostname.
- The name reaches another server.

Check `kubectl -n <namespace> get ingress`.

**`502` or `503` on a service domain.** The route exists, but no pod answers. For more information, see
[Apps](/docs/guides/apps/#troubleshooting).

**A default hostname does not resolve.** In **DNS to cluster** mode, Geass does not create records for services. Add a
record or a wildcard, or switch to **Cloudflare Tunnel**.

**Cloudflare shows a 5xx error page for a service.** Geass does not manage the SSL/TLS mode of your zone. Do one of
these things:

- Set the mode so that Cloudflare reaches your server over HTTPS. If the mode requires a valid certificate, make sure
  that the origin has one.
- Use the tunnel.

**I changed the root domain and the dashboard stopped working.** Geass replaces the old Ingress with the new host. But
DNS and the certificate must follow. Verification starts again. Keep the old record until the new address works.

## Related

- [Connect a domain](/docs/getting-started/connect-domain/): a short walkthrough.
- [Connect Cloudflare](/docs/guides/connectors/cloudflare/)
- [Apps](/docs/guides/apps/)
- [GitHub and builds](/docs/guides/github-and-builds/)
- [Security](/docs/operations/security/)
- [Troubleshooting](/docs/operations/troubleshooting/)
