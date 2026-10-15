---
title: "GeassPlatformConfig"
description: "Reference for GeassPlatformConfig, the single resource that holds the dashboard domain, Cloudflare and GitHub App links, metrics address, build registry and sign-in method."
sidebar:
  order: 9
---

The `GeassPlatformConfig` holds the settings that belong to the whole platform rather than to one project: the dashboard's domain and how it is published, the link to the Cloudflare connection, the link to the GitHub App, where to find Prometheus, the registry that repository builds push to, and how people sign in (passwords from a Secret, or an identity provider). There is one, named `platform`, in `geass-system`. The dashboard creates it the first time you save a domain, a Cloudflare connection or a GitHub App, and edits it from **Settings**. You can also create and edit it with `kubectl`.

For the settings as a whole, with the operator flags and environment variables, see [Configuration](/docs/reference/configuration/). For the connections it points at, see [Cloud connectors](/docs/guides/connectors/).

## Summary

| Property | Value |
| --- | --- |
| API version | `geass.geass.dev/v1alpha1` |
| Kind | `GeassPlatformConfig` |
| Plural | `geassplatformconfigs` |
| Scope | Namespaced |
| Namespace | `geass-system` |
| Name | `platform`. The dashboard and the controllers only read this name. |
| Subresources | `status` |
| Printer columns | None |

## Example

A dashboard at `geass.example.com`, published with a Cloudflare Tunnel, with the Cloudflare and GitHub App credentials already saved in their Secrets.

```yaml title="platform.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassPlatformConfig
metadata:
  name: platform
  namespace: geass-system
spec:
  rootDomain: example.com
  dashboardURL: https://geass.example.com
  dashboardExposure: cloudflare-tunnel
  cloudflareConnectionRef:
    name: platform-cloudflare
  cloudflareZoneID: 0123456789abcdef0123456789abcdef
  githubAppRef:
    name: platform-github-app
  prometheusURL: http://prometheus.example.internal:9090
```

Every field is optional. An empty `spec` is valid.

People who sign in through an identity provider instead of the users in the Secret (Better Auth here, where a member of the `acme` organization is a viewer and an owner or admin is an admin). The client secret is the `clientSecret` key of the Secret `platform-auth`.

```yaml title="platform.yaml (spec.auth)"
spec:
  auth:
    mode: external
    oidc:
      displayName: Acme
      issuer: https://auth.example.com/api/auth
      clientID: geass
      credentialRef:
        name: platform-auth
      roleClaim: org_role
      roleMap:
        owner: admin
        admin: admin
        member: viewer
      requireClaim:
        organization_slug: acme
      sessionMinutes: 60
```

Setting `mode: external` by hand skips the checks the dashboard makes before it switches (a test sign-in as an admin of the provider, and an admin with a password), so test the provider from **Settings** → **Sign-in** first.

## Spec fields

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.rootDomain` | string | none | Lowercase hostname with at least one dot, such as `example.com` | Your apex domain. Geass serves the dashboard at `geass.<rootDomain>` (or another subdomain, see `dashboardURL`). If it is empty, Geass takes it from `dashboardURL` when that host starts with `geass.`. |
| `spec.dashboardURL` | string | none | `^https://[a-zA-Z0-9.-]+(?::[0-9]+)?$`: HTTPS, host and optional port, no path | The dashboard's public address. Used for the GitHub App's callback and webhook URLs. When it is empty, Geass uses `https://geass.<rootDomain>`. |
| `spec.dashboardExposure` | string | `ingress` | `ingress` or `cloudflare-tunnel` | How the dashboard is published. `ingress` creates an Ingress (and, when Cloudflare is connected, a proxied A record). `cloudflare-tunnel` runs a tunnel instead. |
| `spec.tunnelCNAMETarget` | string | none | `^[a-z0-9-]+(\.[a-z0-9-]+)+$` | The tunnel's hostname, `<tunnel id>.cfargotunnel.com`. Geass sets it itself when it manages the tunnel. Set it by hand only if you run your own tunnel. |
| `spec.cloudflareConnectionRef.name` | string | none | none | The Secret with the Cloudflare credentials (`apiToken`, `accountId`, and optionally `zoneName` and `zones`). Setting it turns the Cloudflare connection on. |
| `spec.cloudflareZoneID` | string | none | none | The Cloudflare DNS zone for automatic records. When empty, Geass picks the zone whose name equals the root domain, or the only zone the token can see. |
| `spec.githubAppRef.name` | string | none | none | The Secret with the GitHub App credentials (`app_id`, `client_id`, `client_secret`, `slug`, `private_key`, `webhook_secret`). |
| `spec.prometheusURL` | string | none | none | Where the dashboard queries metrics. Wins over the `GEASS_PROMETHEUS_URL` environment variable and the default service address. |
| `spec.defaultClusterRef.name` | string | none | none | The cluster new projects use when more than one `GeassCluster` exists. With one cluster it is ignored. |
| `spec.registry.repository` | string | none | none | The image destination for repository builds when a service sets no `build.registry.repository`. Used exactly as written. |
| `spec.registry.credentialRef.name` | string | none | none | A Secret with the key `.dockerconfigjson` for that registry. Builds push with it, and Geass copies it into a service's namespace for the nodes to pull the built image with. |
| `spec.defaultDomain` | string | none | none | In the schema. No code reads it. |
| `spec.traefikAccessLogs.enabled`, `.format`, `.networkLogEndpoint` | boolean, string, string | none | none | In the schema. No code reads them. See [GeassNetworkLog](/docs/reference/resources/geass-network-log/). |
| `spec.auth.mode` | string | `local` | `local` or `external` | How people sign in. `local` is the users in the Secret `geass-dashboard-auth`. `external` is the identity provider in `spec.auth.oidc`; a user of the Secret who is an admin and has a password can still sign in. See [Sign in with an identity provider](/docs/guides/external-sign-in/). |
| `spec.auth.oidc.displayName` | string | none | At most 60 characters | What the sign-in button calls the provider. |
| `spec.auth.oidc.issuer` | string | none | At most 2048 characters | The provider's issuer URL, exactly as its discovery document names it. `https`, and not this machine or a link-local address. |
| `spec.auth.oidc.clientID` | string | none | At most 512 characters | The id Geass is registered with at the provider. |
| `spec.auth.oidc.credentialRef.name` | string | none | none | A Secret in `geass-system` whose `clientSecret` key is the client secret. Leave it out for a public client that uses PKCE alone. |
| `spec.auth.oidc.scopes` | list of strings | `email`, `profile` | At most 20 | Asked for besides `openid`. |
| `spec.auth.oidc.roleClaim` | string | none | At most 253 characters | The claim of the ID token that holds a person's role at the provider: a name, or a dotted path into nested claims. A string, a list, or a comma-separated string. |
| `spec.auth.oidc.roleMap` | map of strings | none | At most 50 entries; values `admin` or `viewer` | Which Geass role each role at the provider is. Someone whose roles map to none cannot sign in. The highest wins. |
| `spec.auth.oidc.requireClaim` | map of strings | none | At most 10 entries | Claims the ID token must have, with these values (or a list that contains them), for a person to sign in. |
| `spec.auth.oidc.sessionMinutes` | integer | `60` | 5 to 720 | How long a sign-in lasts before the person goes back to the provider. |
| `spec.auth.oidc.revokeTokensOnLogout` | boolean | `true` | none | Whether a back-channel logout from the provider also revokes the API tokens and deploy hooks the person made. It always ends their sessions. Turn it off when CI depends on tokens made from the accounts of people who sign out. |

:::caution
`spec.registry.repository` is the full destination image of every build that does not set its own, and Geass adds neither a service name nor a tag. Every service that relies on it pushes to the same reference. Set `build.registry.repository` on each service instead. Without any registry setting, a repository service waits with `no container registry is set for builds`: Geass has no registry of its own. See [GeassBuild](/docs/reference/resources/geass-build/).
:::

## Status

| Field | Type | Meaning |
| --- | --- | --- |
| `status.cloudflareTunnelID` | string | The ID of the Cloudflare tunnel Geass manages, when the tunnel is in use. |
| `status.conditions` | list | `DashboardDomainReady` and `CloudflareReady`. |

### DashboardDomainReady

This condition says whether the dashboard answers at its public address. It is `True` only for the generation of the spec it was checked against, so any change to `spec` makes the controller check again. While it is false, the controller checks every 5 seconds. After 15 minutes without success it reports `VerificationTimedOut` and checks every 5 minutes, so a late DNS fix still heals.

| Reason | Status | Meaning |
| --- | --- | --- |
| `Verified` | `True` | The dashboard's HTTPS address answers the Geass probe (`/geass-probe`), or DNS and the probe agree. |
| `NoDomain` | `False` | There is no root domain. |
| `PendingDNS` | `False` | The host does not resolve to the cluster's public IP yet, or Geass could not create the Cloudflare record. The message says which. |
| `PendingHTTPS` | `False` | DNS is right, but `https://<host>/geass-probe` is not reachable yet. The certificate may still be pending. In ingress mode, when a [GeassCluster](/docs/reference/resources/geass-cluster/) reports `ClusterIssuerReady: False`, the message ends with its reason in parentheses, for example that no ACME contact email is set. |
| `PendingTunnel` | `False` | Tunnel mode, and no tunnel hostname is known yet. |
| `ZoneRequired` | `False` | Cloudflare is connected, but the token sees several zones and none matches the root domain. Choose a zone in **Settings** → **Domain**. |
| `Error` | `False` | The Cloudflare credentials are unavailable or incomplete, the dashboard Service was not found, or the cluster nodes could not be listed. |
| `VerificationTimedOut` | `False` | The check has been pending for 15 minutes. It keeps trying. |

### CloudflareReady

This condition reports the Cloudflare side. It is only set when `cloudflareConnectionRef` is set.

| Reason | Status | Meaning |
| --- | --- | --- |
| `Connected` | `True` | Ingress mode: Cloudflare is connected and, when there is a root domain, the dashboard's proxied A record exists. |
| `Ready` | `True` | Tunnel mode: the tunnel exists, `cloudflared` is deployed and the routes are published. |
| `CredentialsIncomplete` | `False` | The Secret lacks the API token or account ID, or no zone could be chosen. |
| `CredentialsInvalid` | `False` | Cloudflare rejected the token. The message is the reason Cloudflare gave. |
| `TunnelError` | `False` | The tunnel could not be found or created. |
| `TunnelTokenError` | `False` | The tunnel's connector token could not be fetched. |
| `TunnelConfigError` | `False` | The tunnel's routes could not be published. |
| `DNSError` | `False` | Ingress mode: the dashboard's A record could not be created. |

While `CloudflareReady` is true, services without a host get a default one under the root domain. See [Connect Cloudflare](/docs/guides/connectors/cloudflare/).

## How the dashboard maps to it

| Dashboard | Writes |
| --- | --- |
| **Settings** → **Domain** → **Save dashboard domain** | `rootDomain`, `dashboardURL` (from **Subdomain**, default `geass`), `dashboardExposure` (**Publish via**), `tunnelCNAMETarget`, `cloudflareZoneID`. The page's verify action sets the `DashboardDomainReady` condition. |
| **Settings** → **Connectors** → **Cloudflare** → **Connect** | `cloudflareConnectionRef` (the Secret `platform-cloudflare`) and `cloudflareZoneID`. **Disconnect** clears both. |
| **Settings** → **Connectors** → **GitHub** | `githubAppRef` (the Secret `platform-github-app`). **Remove GitHub App** clears it. |
| **Settings** → **Sign-in** → **Also revoke the tokens and deploy hooks they made** | `auth.oidc.revokeTokensOnLogout`. Changes at once, in either mode. |
| **Settings** → **Sign-in** → **Save**, **Use ... to sign in**, **Remove** | `auth.oidc` (and `auth.oidc.credentialRef`, the Secret `platform-auth`, key `clientSecret`), then `auth.mode`. **Switch back to passwords** sets `auth.mode` to `local`. **Remove** clears `auth` and deletes that Secret. |

The dashboard has no screen for `prometheusURL`, `defaultClusterRef` or `registry`. Set the first two with `kubectl`. An admin can set `registry` with `POST /api/settings/registry/save` (see the [REST API](/docs/reference/rest-api/)), which stores the login as the Secret `platform-registry`, or with `kubectl`.

## Editing by hand

It is safe to edit the spec with `kubectl`. The controller applies a change within seconds. Do not edit `status`.

```bash title="Edit and inspect"
kubectl -n geass-system edit geassplatformconfig platform
kubectl -n geass-system get geassplatformconfig platform \
  -o jsonpath='{range .status.conditions[*]}{.type}{"\t"}{.status}{"\t"}{.reason}{"\t"}{.message}{"\n"}{end}'
```

If you set `cloudflareConnectionRef` by hand, create the Secret `platform-cloudflare` first. If you clear it, delete the Secret yourself.

## What happens under the hood

In ingress mode the controller creates the Ingress `geass-dashboard` in `geass-system` for the dashboard host, on the `websecure` entry point of Traefik, with TLS from cert-manager (the secret `geass-dashboard-tls`). It then checks DNS and probes the HTTPS address. If Cloudflare is connected it also creates a proxied A record pointing at the cluster's public IP.

:::note
The Ingress asks cert-manager for a certificate from a ClusterIssuer named `letsencrypt-prod`. The operator creates that issuer when `spec.addons.certManager.acmeEmail` is set on the [GeassCluster](/docs/reference/resources/geass-cluster/) (the installer sets it from `GEASS_ACME_EMAIL`). Without it no certificate is issued and the condition stays at `PendingHTTPS`. See [Security](/docs/operations/security/#what-geass-exposes).
:::

In tunnel mode the controller creates the tunnel `geass`, the Secret `geass-cloudflared-token` and the Deployment `geass-cloudflared`, publishes the routes, and creates CNAME records. Without a Cloudflare connection it deletes the Deployment and Secret.

The routes are the dashboard's host and the host of every service that has one. The controller works out what it would send first, and compares it with what it last sent in full. When nothing has changed it sends nothing to Cloudflare, and looks again after five minutes, which catches a route or record that someone deleted in Cloudflare. It reacts at once when a service gets, changes or loses a host, because it watches the services for changes to their spec. A CNAME record that cannot be created is only logged, and the controller tries again in five seconds. The route of a service that [sleeps when idle](/docs/guides/scale-to-zero/) points at the add-on's wake proxy instead of at the service, because the tunnel does not go through Traefik. The platform configuration is also read by the build controller (the registry), the dashboard (metrics address, GitHub readiness) and project creation (the default cluster).

## Troubleshooting

**`DashboardDomainReady` is `PendingDNS`.** The host does not point at the cluster yet. Create an A record for `geass.<rootDomain>` at the cluster's public IP, or connect Cloudflare so Geass does it. The message names the address it expects.

**`PendingHTTPS` for a long time.** DNS is right but HTTPS does not answer. Check the Ingress and its certificate (`kubectl -n geass-system get ingress,certificate`), and that the `letsencrypt-prod` ClusterIssuer exists and is ready (`kubectl get clusterissuer letsencrypt-prod`). The `ClusterIssuerReady` condition of the `GeassCluster` says why it is not, for example `ACMEEmailMissing`.

**`ZoneRequired`.** Choose the zone in **Settings** → **Domain**, or set `cloudflareZoneID`.

**`CloudflareReady` is `CredentialsInvalid`.** Update the token with **Update API token**. See [Connect Cloudflare](/docs/guides/connectors/cloudflare/).

**The GitHub App cannot be saved: `dashboard URL must be configured first`.** The GitHub App needs a verified HTTPS dashboard address. Finish the domain first.

**I changed an unrelated field and the dashboard briefly says the domain is not verified.** A spec change starts a new verification. It returns to `Verified` at the next check.

## Related

- [Configuration](/docs/reference/configuration/)
- [Cloud connectors](/docs/guides/connectors/)
- [Domains and TLS](/docs/guides/domains-and-tls/)
- [GeassCluster](/docs/reference/resources/geass-cluster/)
- [Sleep when idle](/docs/guides/scale-to-zero/)
- [Resources overview](/docs/reference/resources/overview/)
