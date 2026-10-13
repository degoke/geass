---
title: "Connect Cloudflare"
description: "Connect Cloudflare to Geass to manage dashboard DNS and optionally publish it through a Cloudflare Tunnel: token permissions, setup and what Geass creates."
sidebar:
  label: Cloudflare
---

Use Cloudflare when your domain is on Cloudflare and you want Geass to manage the DNS records for the dashboard, and
optionally to publish the dashboard through a Cloudflare Tunnel instead of opening ports. There is one Cloudflare
connection for the whole cluster, and it is not project-scoped. See also [Domains and TLS](/docs/guides/domains-and-tls/)
and [Connect a domain](/docs/getting-started/connect-domain/).

## Before you start

- A domain whose DNS is on Cloudflare, and a custom API token with the permissions listed below.
- An admin login in Geass. Only admins can add, change or remove connections.

## Token permissions

Create a custom API token (not the Global API Key or an Origin CA key) in Cloudflare with these permissions for the
zone you use with Geass:

- **Zone** → **Zone** → **Read**
- **Zone** → **DNS** → **Edit**
- **Account** → **Cloudflare Tunnel** → **Edit** (only exercised when you publish the dashboard through a tunnel)

Geass uses these Cloudflare API calls: `GET /user/tokens/verify` (falling back to `GET /zones` and `GET /accounts`
for account-owned tokens), `GET /accounts`, `GET /zones` and `GET /zones/{id}` to find the zone, the
`cfd_tunnel` endpoints to find or create a tunnel, read its token and publish its routes, and the `dns_records`
endpoints to list, create and update records. Geass reads only the first 50 zones the token can see.

## Connect Cloudflare

1. Open **Settings** → **Connectors** and choose **Configure** on the **Cloudflare** card. **Open Cloudflare to create
   token** takes you to Cloudflare's token page.
2. Paste the token in **API token** and choose **Connect**. Geass checks the token, then lists your accounts and zones.
3. If the token can see more than one zone, choose the **DNS zone**. With a single zone, Geass picks it.

When it works, the card shows **Connected** once the controller has finished (and **Pending** while it does), with the
zone, zone ID and tunnel ID. Then go to **Settings** → **Domain** to publish the dashboard. **Publish via** offers
**DNS to cluster (proxied A record)** or **Cloudflare Tunnel**.

**Refresh zones** re-reads the zone list with the stored token. It does not call Cloudflare's token verify endpoint, and if
Cloudflare cannot list the zones it keeps the saved list and shows a warning instead of an error. The
`CloudflareReady` condition on the platform configuration (see [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/))
reports the state. Its reasons are `Connected`, `Ready`, `CredentialsIncomplete`, `CredentialsInvalid`, `TunnelError`,
`TunnelTokenError`, `TunnelConfigError` and `DNSError`.

## What Geass creates

In Cloudflare, depending on how you publish the dashboard:

| Publish via | Created in Cloudflare | Created in the cluster |
| --- | --- | --- |
| DNS to cluster | A proxied A record for the dashboard host (`geass.<your domain>` by default) pointing at the cluster's external IP | Nothing for Cloudflare |
| Cloudflare Tunnel | A remotely managed tunnel named `geass`; its route list; a proxied CNAME to `<tunnel id>.cfargotunnel.com` for the dashboard host and for every service host | Secret `geass-cloudflared-token` and Deployment `geass-cloudflared` (one replica, image `cloudflare/cloudflared:2024.10.0`) in `geass-system` |

In tunnel mode the route list sends the dashboard host to the dashboard Service on port 8085, each service host to
that service, and everything else to a 404. The tunnel's dashboard route and CNAME are always for `geass.<your domain>`,
whatever **Subdomain** you saved under **Settings** → **Domain**, so keep the default `geass` when you publish through a
tunnel. Once Cloudflare is ready, in either mode, a service without a host gets a default one,
`<service>-<project id>.<your domain>` (with `-<environment>` added for non-production environments), with TLS off. Only tunnel mode creates
DNS records for those hosts. In the other mode Geass creates only the dashboard record, so point your service hostnames at the cluster
yourself, for example with a wildcard record.

Geass rebuilds the tunnel's routes and DNS records when a service gets, changes or loses a host, and checks them again every 5 minutes
while nothing changes. A route or record you delete by hand in Cloudflare comes back within about 5 minutes. The tunnel reaches a service
without going through Traefik, so a service that [sleeps when idle](/docs/guides/scale-to-zero/) gets a route to the wake-up proxy
(`keda-add-ons-http-interceptor-proxy` in the `keda` namespace) instead of straight to the service, and the request wakes it.

## Rotate and disconnect

To rotate the token, choose **Update API token**, paste the new token and choose **Save changes**. Geass checks it and
reads the zone again. The new token needs the same permissions.

To disconnect, type `remove-cloudflare` in the confirmation field and choose **Disconnect**. Geass deletes the
Secret `platform-cloudflare`, clears the connection from the platform configuration, and stops and removes the
`geass-cloudflared` Deployment and its token Secret. Geass does not delete the tunnel or the DNS records it made in
Cloudflare, so remove them there if you no longer want them. The dashboard host stops resolving to the cluster only
when you remove the records.

The Secret `platform-cloudflare` holds `apiToken`, `accountId`, `zoneName` and `zones` (a list of the zones the token
can read).

## What happens under the hood

- The Cloudflare connection is not a `GeassCloudConnection`. It lives on the platform configuration, named `platform`, and
  its credentials are in the Secret `platform-cloudflare`. See
  [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/).
- In tunnel mode, Geass creates the Deployment `geass-cloudflared` and the Secret `geass-cloudflared-token` in `geass-system`.

```bash title="Inspect the Cloudflare connection"
kubectl -n geass-system get geassplatformconfig platform -o yaml
kubectl -n geass-system get secret platform-cloudflare
kubectl -n geass-system get deployment geass-cloudflared
```

## Troubleshooting

**Cloudflare says "No zones on this token".** Add **Zone** → **Zone** → **Read** to the token, or reconnect and choose a zone.

**Cloudflare says "Cloudflare rejected the API token".** The token was revoked or lacks a permission. Use **Update API token**.

## Related

- [Cloud connectors](/docs/guides/connectors/)
- [Domains and TLS](/docs/guides/domains-and-tls/)
- [Connect a domain](/docs/getting-started/connect-domain/)
- [Sleep when idle](/docs/guides/scale-to-zero/)
- [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/)
