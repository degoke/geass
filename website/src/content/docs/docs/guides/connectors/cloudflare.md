---
title: "Connect Cloudflare"
description: "Connect Cloudflare so that Geass manages the dashboard DNS and can publish it through a tunnel: token permissions, setup, and what Geass creates."
sidebar:
  label: Cloudflare
---

Use Cloudflare when your domain is on Cloudflare. Geass then manages the DNS records for the dashboard. Geass can
also publish the dashboard through a Cloudflare Tunnel, so that you do not open ports. The cluster has one Cloudflare
connection. That connection is for the whole cluster, not for one project. See also
[Domains and TLS](/docs/guides/domains-and-tls/) and [Connect a domain](/docs/getting-started/connect-domain/).

## Before you start

- A domain whose DNS is on Cloudflare, and a custom API token with the permissions in the next section.
- An admin login in Geass. Only admins can add, change, or remove connections.

## Token permissions

In Cloudflare, create a custom API token with these permissions for the zone that you use with Geass. Do not use the
Global API Key or an Origin CA key.

- **Zone** → **Zone** → **Read**
- **Zone** → **DNS** → **Edit**
- **Account** → **Cloudflare Tunnel** → **Edit**. Geass uses this permission only when you publish the dashboard
  through a tunnel.

Geass uses these Cloudflare API calls:

- `GET /user/tokens/verify` to check the token. If Cloudflare refuses this call, for example for an account-owned
  token, Geass calls `GET /zones`. If that call also fails, Geass calls `GET /accounts`. The token passes when one of
  the three calls succeeds. If all three fail, Geass shows the error of the first call.
- `GET /accounts`, `GET /zones`, and `GET /zones/{id}` to find the zone.
- The `cfd_tunnel` endpoints to find or create a tunnel, read its token, and publish its routes.
- The `dns_records` endpoints to list, create, and update records.

Geass reads only the first 50 zones that the token can see.

## Connect Cloudflare

1. Open **Settings** → **Connectors**.
2. On the **Cloudflare** card, click **Configure**. **Open Cloudflare to create token** opens the token page in
   Cloudflare.
3. Paste the token in **API token**.
4. Click **Connect**. Geass checks the token, and then lists your accounts and zones.
5. If the token can see more than one zone, select the **DNS zone**. If the token can see one zone, Geass selects
   that zone.

When the connection works, the card shows the zone, the zone ID, and the tunnel ID. The card shows **Pending** while
the controller works, and **Connected** after the controller finishes. Then go to **Settings** → **Domain** to
publish the dashboard. **Publish via** gives two options: **DNS to cluster (proxied A record)** or
**Cloudflare Tunnel**.

**Refresh zones** reads the zone list again with the stored token. It does not call the token verify endpoint of
Cloudflare. If Cloudflare cannot list the zones, Geass keeps the saved list and shows a warning, not an error.

The `CloudflareReady` condition on the platform configuration reports the state. For the platform configuration, see
[GeassPlatformConfig](/docs/reference/resources/geass-platform-config/). The condition reasons are `Connected`,
`Ready`, `CredentialsIncomplete`, `CredentialsInvalid`, `TunnelError`, `TunnelTokenError`, `TunnelConfigError`, and
`DNSError`.

## What Geass creates

What Geass creates in Cloudflare depends on how you publish the dashboard:

| Publish via | Created in Cloudflare | Created in the cluster |
| --- | --- | --- |
| DNS to cluster | A proxied A record for the dashboard host that points at the external IP of the cluster. The default host is `geass.<your domain>`. | Nothing for Cloudflare |
| Cloudflare Tunnel | A remotely managed tunnel named `geass`, its route list, and a proxied CNAME to `<tunnel id>.cfargotunnel.com` for the dashboard host and for each service host | Secret `geass-cloudflared-token` and Deployment `geass-cloudflared` in `geass-system`. The Deployment has one replica and uses image `cloudflare/cloudflared:2024.10.0`. |

In tunnel mode, the route list sends traffic as follows:

- The dashboard host goes to the dashboard Service on port 8085.
- Each service host goes to that service.
- All other traffic gets a 404.

The dashboard route and the CNAME of the tunnel are always for `geass.<your domain>`. The value of **Subdomain** that
you save under **Settings** → **Domain** does not change them. Thus, when you publish through a tunnel, keep the
default `geass`.

After Cloudflare is ready, in both modes, a service without a host gets a default host:
`<service>-<project id>.<your domain>`. For non-production environments, Geass adds `-<environment>` to that host.
TLS is off for these default hosts. Only tunnel mode creates DNS records for these hosts. In the other mode, Geass
creates only the dashboard record. Thus, you must point your service hostnames at the cluster yourself, for example
with a wildcard record.

Geass rebuilds the routes and DNS records of the tunnel when a service gets, changes, or loses a host. When nothing
changes, Geass checks the routes and records again every 5 minutes. If you delete a route or a record by hand in
Cloudflare, it comes back in approximately 5 minutes.

The tunnel goes to a service directly, not through Traefik. Thus, a service that
[sleeps when idle](/docs/guides/scale-to-zero/) gets a route to the wake-up proxy, not directly to the service. The
wake-up proxy is `keda-add-ons-http-interceptor-proxy` in the `keda` namespace. The request wakes the service.

## Rotate and disconnect

To rotate the token, do these steps:

1. Click **Update API token**.
2. Paste the new token.
3. Click **Save changes**. Geass checks the token and reads the zone again.

The new token needs the same permissions.

To disconnect, enter `remove-cloudflare` in the confirmation field, and then click **Disconnect**. Geass then does
these actions:

- Deletes the Secret `platform-cloudflare`.
- Clears the connection from the platform configuration.
- Stops and removes the `geass-cloudflared` Deployment and its token Secret.

Geass does not delete the tunnel or the DNS records that it made in Cloudflare. If you do not want them, remove them
in Cloudflare. The dashboard host stops resolving to the cluster only when you remove the records.

The Secret `platform-cloudflare` holds `apiToken`, `accountId`, `zoneName`, and `zones`. The `zones` key is a list of
the zones that the token can read.

## What happens under the hood

- The Cloudflare connection is not a `GeassCloudConnection`. It is on the platform configuration, named `platform`.
  Its credentials are in the Secret `platform-cloudflare`. See
  [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/).
- In tunnel mode, Geass creates the Deployment `geass-cloudflared` and the Secret `geass-cloudflared-token` in
  `geass-system`.

```bash title="Inspect the Cloudflare connection"
kubectl -n geass-system get geassplatformconfig platform -o yaml
kubectl -n geass-system get secret platform-cloudflare
kubectl -n geass-system get deployment geass-cloudflared
```

## Troubleshooting

**Cloudflare says "No zones on this token".** Add **Zone** → **Zone** → **Read** to the token. Or reconnect and
select a zone.

**Cloudflare says "Cloudflare rejected the API token".** Someone revoked the token, or the token does not have a
permission. Use **Update API token**.

## Related

- [Cloud connectors](/docs/guides/connectors/)
- [Domains and TLS](/docs/guides/domains-and-tls/)
- [Connect a domain](/docs/getting-started/connect-domain/)
- [Sleep when idle](/docs/guides/scale-to-zero/)
- [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/)
