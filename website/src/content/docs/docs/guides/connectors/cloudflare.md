---
title: "Connect Cloudflare"
description: "Connect Cloudflare so that Geass manages the dashboard DNS and can publish it through a tunnel: token permissions, setup, and what Geass creates."
sidebar:
  label: Cloudflare
---

Use Cloudflare when your domain is on Cloudflare. Geass then manages the DNS records of the dashboard. Geass can also
publish the dashboard through a Cloudflare Tunnel, so that you do not open ports. The cluster has one Cloudflare
connection, for the whole cluster. See also [Domains and TLS](/docs/guides/domains-and-tls/) and
[Connect a domain](/docs/getting-started/connect-domain/).

## Before you start

You need these things:

- A domain whose DNS is on Cloudflare.
- A custom API token with the permissions in the next section.
- An admin login in Geass.

## Token permissions

In Cloudflare, create a custom API token with these permissions for the zone that you use with Geass. Do not use the
Global API Key or an Origin CA key.

- **Zone** → **Zone** → **Read**
- **Zone** → **DNS** → **Edit**
- **Account** → **Cloudflare Tunnel** → **Edit**. Geass uses this permission only when you publish the dashboard
  through a tunnel.

Geass uses the token to check itself, find the zone, manage the tunnel, and list, create, and update DNS records.
Geass reads only the first 50 zones that the token can see.

## Connect Cloudflare

1. Open **Settings** → **Connectors**.
2. On the **Cloudflare** card, click **Configure**.
3. To create the token, click **Open Cloudflare to create token**.
4. Paste the token in **API token**.
5. Click **Connect**. Geass checks the token, and then lists your accounts and zones.
6. If the token can see more than one zone, select the **DNS zone**. If it can see one zone, Geass selects it.

The card shows **Pending** while Geass sets up, and **Connected** when it finishes. The drawer then shows the zone, the
zone ID, and the tunnel ID.

To publish the dashboard, open **Settings** → **Domain**. **Publish via** has two options:
**DNS to cluster (proxied A record)** and **Cloudflare Tunnel**.

**Refresh zones** reads the zone list again with the stored token. If Cloudflare cannot list the zones, Geass keeps
the saved list and shows a warning.

## What Geass creates

What Geass creates in Cloudflare depends on how you publish the dashboard:

| Publish via | Created in Cloudflare |
| --- | --- |
| DNS to cluster | A proxied A record for the dashboard host that points at the public IP address of the cluster. The default host is `geass.<your domain>`. |
| Cloudflare Tunnel | A tunnel named `geass`, its routes, and a proxied CNAME record for the dashboard host and for each service host. Geass also runs the tunnel connector in the cluster. |

In tunnel mode, the dashboard host goes to the dashboard, and each service host goes to its service. All other traffic
gets a 404. A service that [sleeps when idle](/docs/guides/scale-to-zero/) wakes up when a request arrives through the
tunnel.

The dashboard route and record of the tunnel are always for `geass.<your domain>`. The **Subdomain** that you save
under **Settings** → **Domain** does not change them. So when you publish through a tunnel, keep the default `geass`.

After Cloudflare is ready, a service without a host gets a default host: `<service>-<project id>.<your domain>`. For
an environment that is not production, Geass adds `-<environment>` to that host. TLS is off for these default hosts.
Only tunnel mode creates DNS records for them. In the other mode, point your service hostnames at the cluster
yourself, for example with a wildcard record.

Geass updates the routes and records of the tunnel when a service gets, changes, or loses a host. Geass also checks
them every 5 minutes. If you delete a route or a record by hand in Cloudflare, it comes back in about 5 minutes.

## Rotate and disconnect

To rotate the token, do these steps:

1. Click **Update API token**.
2. Paste the new token. The new token needs the same permissions.
3. Click **Save changes**. Geass checks the token and reads the zone again.

To disconnect, do these steps:

1. Under **Disconnect Cloudflare**, enter `remove-cloudflare` in **Confirmation**.
2. Click **Disconnect**.

Geass then forgets the token, and stops the tunnel connector in the cluster. Geass does not delete the tunnel or the
DNS records in Cloudflare. If you do not want them, remove them in Cloudflare. The dashboard host stops pointing at
the cluster only when you remove the records.

## Troubleshooting

**"No zones on this token".** Add **Zone** → **Zone** → **Read** to the token. Or connect again and select a zone.

**"Cloudflare rejected the API token".** Someone revoked the token, or the token does not have a permission. Click
**Update API token**.

## Under the hood

- The Cloudflare connection is on the platform configuration, named `platform`. Its `CloudflareReady` condition shows
  the state. See [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/).
- The token is in the Secret `platform-cloudflare` in `geass-system`.
- In tunnel mode, the connector is the Deployment `geass-cloudflared`, with its token in the Secret
  `geass-cloudflared-token`.

```bash title="Inspect the Cloudflare connection"
kubectl -n geass-system get geassplatformconfig platform -o yaml
kubectl -n geass-system get deployment geass-cloudflared
```

## Related

- [Cloud connectors](/docs/guides/connectors/)
- [Domains and TLS](/docs/guides/domains-and-tls/)
- [Connect a domain](/docs/getting-started/connect-domain/)
- [Sleep when idle](/docs/guides/scale-to-zero/)
