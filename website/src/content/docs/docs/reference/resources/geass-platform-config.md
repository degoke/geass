---
title: "GeassPlatformConfig"
description: "Reference for GeassPlatformConfig, the one resource for the dashboard domain, Cloudflare and GitHub App links, metrics address, registry, sign-in and alerts."
sidebar:
  order: 9
---

The `GeassPlatformConfig` holds the settings that belong to the whole platform rather than to one project: the dashboard's domain and how it is published, the link to the Cloudflare connection, the link to the GitHub App, where to find Prometheus, the registry that repository builds push to, how people sign in (passwords from a Secret, or an identity provider), where alerts about builds, deploys, services, backups and nodes are sent (Slack or email), and the metrics and log stores outside the cluster that Geass sends to and the dashboard reads from. There is one, named `platform`, in `geass-system`. The dashboard creates it the first time you save a domain, a Cloudflare connection or a GitHub App, and edits it from **Settings**. You can also create and edit it with `kubectl`.

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

Organizations' code under gVisor, except on nodes reserved for the organization (see [Sandboxed runtime for organizations](/docs/operations/sandboxed-runtime/) for what the nodes need first):

```yaml title="platform.yaml (spec.tenancy)"
spec:
  tenancy:
    sandbox:
      runtimeClassName: gvisor
      skipOnReservedNodes: true
```

Setting `mode: external` by hand skips the checks the dashboard makes before it switches (a test sign-in as an admin of the provider, and an admin with a password), so test the provider from **Settings** → **Sign-in** first.

Organizations' builds never given the platform's registry login, and each organization's images kept apart (see [Registry isolation for organizations](/docs/operations/registry-isolation/)):

```yaml title="platform.yaml (spec.tenancy)"
spec:
  tenancy:
    registry:
      policy: requireOwnRegistry
```

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
| `spec.prometheusURL` | string | none | none | Where the dashboard queries metrics. Wins over the `GEASS_PROMETHEUS_URL` environment variable and the default service address. `spec.observability.external.metrics.queryURL` wins over it. |
| `spec.defaultClusterRef.name` | string | none | none | The cluster new projects use when more than one `GeassCluster` exists. With one cluster it is ignored. |
| `spec.registry.repository` | string | none | none | The image destination for repository builds when a service sets no `build.registry.repository`. Used exactly as written. |
| `spec.registry.credentialRef.name` | string | none | none | A Secret with the key `.dockerconfigjson` for that registry. Builds push with it, and Geass copies it into a service's namespace for the nodes to pull the built image with. Under `spec.tenancy.registry`, never for a project of an organization. |
| `spec.tenancy.registry.policy` | string | none (off); `requireOwnRegistry` when `registry` is set | `requireOwnRegistry` | For the projects of organizations: their builds are never given the platform's registry login (nor another organization's), their namespaces never hold a copy of it, they push to the organization's own registry under `<repository>/<project>/<service>`, and an organization without one does not build. Their services may not run an image in the platform's repository or in another organization's registry. Refusals are the condition `RegistryIsolated` `False` on the service or build. The projects of no organization are left as they are. Not on the settings page. See [Registry isolation for organizations](/docs/operations/registry-isolation/). |
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
| `spec.auth.oidc.accessTokens.enabled` | boolean | `false` | Takes effect only while `spec.auth.mode` is `external` | Whether Geass accepts the provider's JWT access tokens (RFC 9068, `typ` `at+jwt`) as `Authorization: Bearer`, for the person they were issued to, with the role and organizations their claims map to as an ID token's do. Not on the settings page; saving the provider keeps it. See [Access tokens as a bearer credential](/docs/guides/external-sign-in/#access-tokens-as-a-bearer-credential). |
| `spec.auth.oidc.accessTokens.audience` | string | the client ID | At most 512 characters | A value the token's `aud` has to contain, so that a token for another cluster of the same provider is refused. |
| `spec.auth.tenancy.enabled` | boolean | `false` | Takes effect only while `spec.auth.mode` is `external` | Whether people belong to organizations, each seeing only its own projects. While it is off everyone who may sign in sees everything, as before, and a project that belongs to no organization is seen by the platform's administrators only once it is on. See [Organizations and workspaces](/docs/guides/organizations-and-workspaces/). |
| `spec.auth.oidc.refreshMinutes` | integer | `0` (off) | 0, or 5 to 1440 | How often Geass asks the provider again about each signed-in person, so a changed role or a removed person is noticed within minutes. Adds the `offline_access` scope. See [Check people again](/docs/guides/external-sign-in/#check-people-again-while-they-are-signed-in). |
| `spec.auth.oidc.staleGraceMinutes` | integer | `120` | 5 to 1440 | How long the provider may be unreachable before the sessions that could not be checked are ended. |
| `spec.auth.tenancy.orgsClaim` | string | none | At most 253 characters | The claim of the ID token that lists the person's organizations: a name, or a dotted path. A list of objects, each with the organization's `id`, the person's `role` in it (mapped by `roleMap`) and optionally `workspaces`. With it set, `roleClaim` is not needed: the role is read from each organization. |
| `spec.auth.tenancy.autoProvision.mode`, `.allow`, `.max` | `Pending` or `Open`; list of strings; integer | none (off) | `allow` at most 50 entries; `max` 1 to 500, default 20; `Open` needs `allow` | Makes an organization of the provider that Geass does not know when one of its administrators, with a verified address, signs in. `Pending` makes it suspended until approved; `Open` makes it ready, for the ids in `allow` (an id as it is, or a prefix followed by `*`). See [Sign in with an identity provider](/docs/guides/external-sign-in/#organizations-made-by-sign-in). |
| `spec.auth.tenancy.platformClaim`, `.platformValues` | string, list of strings | none | At most 253 characters; at most 20 values | A claim whose value, compared whole, makes someone an administrator of the whole platform and not of an organization. Without them, only the admins who sign in with a password are. |
| `spec.usage.enabled` | boolean | `false` | none | Whether the operator meters what each organization's projects reserve (CPU and memory requests, and the size of bound volumes), hour by hour, into the ConfigMaps `geass-usage-<YYYYMMDDHH>` in `geass-system`, kept 62 days, for `GET /api/v1/usage/hours`. Turning it off stops the meter and keeps what it stored. Not on the settings page. See [Usage by organization, for billing](/docs/guides/usage-and-capacity/#usage-by-organization-for-billing). |
| `spec.tenancy.sandbox.runtimeClassName` | string | none (off) | 1 to 253 characters, a DNS subdomain; required when `sandbox` is set | The RuntimeClass (gVisor, Kata) that the pods of organizations' projects run with: their services' copies, their functions' schedules and their builds. The projects of no organization and Geass's own pods are left alone. While the cluster has no RuntimeClass of this name, an organization's service or build waits, with the condition `Sandboxed` `False` `RuntimeClassMissing`, and is never started without it. Changing it rolls the organizations' services. Not on the settings page. See [Sandboxed runtime for organizations](/docs/operations/sandboxed-runtime/). |
| `spec.tenancy.sandbox.skipOnReservedNodes` | boolean | `false` | none | Runs without the sandbox the pods that can only be scheduled on nodes reserved for their own organization: they ask for a tag the organization requires, no other organization requires it, and every node that carries it is reserved for it. Anything less certain is sandboxed. See [Reserved nodes](/docs/operations/sandboxed-runtime/#reserved-nodes). |
| `spec.tenancy.egress` | object | none (no limits) | none | Limits what the pods of organizations' projects reach outside the cluster, in each environment of their projects and in their build namespaces. Setting it at all closes mail. A project of no organization is never limited. An organization's `spec.egress` replaces each field it sets. See [Outbound limits](/docs/guides/outbound-limits/). |
| `spec.tenancy.egress.mail` | string | `Blocked` | `Blocked` or `Open` | `Blocked` closes TCP 25, 465, 587, 2465, 2525 and 2587 to addresses outside the cluster, except where `allow` opens them. |
| `spec.tenancy.egress.allowedPorts` | list of ports | none (every port open but the closed ones) | 1 to 64 | When set, the only ports open to addresses outside the cluster, less the closed ones. The cluster's API port stays open from project environments. |
| `spec.tenancy.egress.blockedPorts` | list of ports | none | At most 64 | Closed to addresses outside the cluster, besides mail's: for example the ports mining pools listen on, which is a heuristic and detects nothing. |
| `spec.tenancy.egress.allow[].cidr`, `.ports`, `.description` | string; list of ports; string | none | At most 32. `cidr` an IPv4 range written from its first address, not in `169.254.0.0/16` or `127.0.0.0/8`; `ports` 1 to 16; `description` at most 200 characters | Opens the ports to that range whatever else closes them, such as 587 to a mail relay. It cannot open the metadata range, the nodes of a project that keeps away from them, or a build's private ranges. |
| A port: `port`, `endPort`, `protocol` | integer, integer, string | `protocol` `TCP` | `port` and `endPort` 1 to 65535, `endPort` not below `port`; `TCP` or `UDP` | One port, or the range from `port` to `endPort`, both included. |
| `spec.alerting.smtp.host` | string | none | 1 to 253 characters | The mail server email channels send through. Required when `smtp` is set. Alertmanager sends the mail, from the `monitoring` namespace. |
| `spec.alerting.smtp.port` | integer | `587` | 1 to 65535 | The mail server's port. |
| `spec.alerting.smtp.security` | string | `starttls` | `starttls`, `tls` or `none` | How the connection is protected: `starttls` upgrades a plain connection (port 587), `tls` is encrypted from the first byte and Alertmanager does that only on port 465, `none` sends in the clear and cannot have a username, because Alertmanager will not send a password unencrypted. |
| `spec.alerting.smtp.username` | string | none | At most 256 characters | The user to sign in as. Leave it out for a server that does not ask. |
| `spec.alerting.smtp.credentialRef.name` | string | none | none | A Secret in `geass-system` whose `password` key is the password. The dashboard makes the Secret `geass-alert-smtp`. |
| `spec.alerting.smtp.from` | string | none | 3 to 320 characters; an address, or `Name <address>` | The address alerts come from. Required when `smtp` is set. |
| `spec.alerting.channels` | list | none | At most 20 | The places alerts go. |
| `spec.alerting.channels[].id` | string | none | `^[a-z0-9]{8}$` | Identifies the channel, and names its secret. |
| `spec.alerting.channels[].name` | string | none | 1 to 60 characters | What the dashboard calls it. |
| `spec.alerting.channels[].type` | string | none | `slack` or `email` | A Slack incoming webhook (or anything that takes the same JSON), or email. |
| `spec.alerting.channels[].recipients` | list of strings | none | At most 20 | The addresses an email channel writes to. |
| `spec.alerting.channels[].events` | list of strings | every kind | `build.failed`, `deploy.failed`, `service.down`, `backup.failed`, `node.unhealthy`, `cluster.issue`, `workload.crashlooping`, `storage.filling` | Which alerts the channel hears. Empty means all, including kinds added later. |
| `spec.alerting.channels[].projects` | list of strings | every project | At most 100 | Limits the channel to these projects. Only a channel with none also hears about the cluster itself. |
| `spec.alerting.channels[].environments` | list of strings | every environment | At most 100 | Limits the channel to these environments. Alerts about the cluster are not narrowed by it. |
| `spec.alerting.channels[].notifyResolved` | boolean | `true` | none | Whether the channel is also told when a problem is resolved. |
| `spec.alerting.channels[].disabled` | boolean | `false` | none | Pauses the channel without deleting it. |
| `spec.observability.external.metrics.remoteWriteURL` | string | none | `^https?://[^\s]+$`, at most 2048 characters; no username or password in it | Prometheus remote-writes every sample to it, besides keeping its own. Needs the monitoring add-on. See [Send metrics and logs outside the cluster](/docs/guides/observability/#send-metrics-and-logs-outside-the-cluster). |
| `spec.observability.external.metrics.queryURL` | string | none | As above, and no query string | A Prometheus-compatible query API (`/api/v1/query` and `/api/v1/query_range` are added to it). When set, the dashboard reads every metric from it and never from the cluster's Prometheus. |
| `spec.observability.external.metrics.tenant` | string | none | `^[A-Za-z0-9!._*'()-]+$`, at most 150 characters | Sent in `tenantHeader` with every write and query. Empty sends no tenant header. |
| `spec.observability.external.metrics.tenantHeader` | string | `X-Scope-OrgID` | `^[A-Za-z0-9-]+$`, at most 64 characters; not a header Prometheus sets itself, such as `Authorization` | The header that carries the tenant. A refused header leaves the store out, and the operator logs why. |
| `spec.observability.external.metrics.credentialRef.name` | string | none | none | A Secret in `geass-system` with a `token` key (bearer), or `username` and `password` keys (basic). Geass copies the keys into `geass-external-metrics` in `monitoring`, which Prometheus reads. |
| `spec.observability.external.logs.pushURL` | string | none | `^https?://[^\s]+$`, at most 2048 characters; no username, password or query string | A VictoriaLogs server that Fluent Bit sends every line to as well (`/insert/jsonline` is added to it). Needs the logging add-on. Projects that require node tags are not collected, so not sent. |
| `spec.observability.external.logs.queryURL` | string | none | As `pushURL` | A VictoriaLogs server that answers LogsQL (`/select/logsql/query` is added to it). When set, log history is read from it and never from the cluster's store. |
| `spec.observability.external.logs.accountID`, `.projectID` | integer | `0` | 0 to 4294967295 | The VictoriaLogs tenant, sent as the `AccountID` and `ProjectID` headers with every line and query. |
| `spec.observability.external.logs.credentialRef.name` | string | none | none | As for metrics. Geass copies the keys into `geass-external-logs` in `geass-logging`, which Fluent Bit reads as environment variables. |
| `spec.observability.external.logs.retentionDays` | integer | the logging add-on's | 1 to 3650 | How long the store keeps logs, which log history reports. |
| `spec.tenantLimits.defaults.pods` | integer | none | 1 to 100000 | The most pods each namespace of every organization may have running or waiting: each environment of its projects, and its build namespace. A ResourceQuota `geass-tenant-quota` in each. See [Noisy-neighbour limits](/docs/guides/tenant-limits/). |
| `spec.tenantLimits.defaults.ephemeralStorage` | quantity | none | Above zero, checked by the operator; needs a default limit for containers' ephemeral storage | The most ephemeral storage the pods of each such namespace may have together (the total of their containers' limits and requests). |
| `spec.tenantLimits.defaults.container.defaultRequest`, `.defaultLimit`, `.max` | each `cpu`, `memory`, `ephemeralStorage` (quantities) | none | Above zero; `defaultRequest` at most the limit a container is given, `defaultLimit` at most `max`; checked by the operator | What a container that asks for none of a resource is given as its request and its limit, and the most one may set. A LimitRange in each namespace. |
| `spec.tenantLimits.defaults.bandwidth.ingress`, `.egress` | quantity | none | `1k` to `1P`, checked by the operator | The most each pod may receive and send, in bits per second, through the CNI bandwidth plugin's annotations. Needs Kubernetes 1.36 and a network plugin that acts on them. |

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
| **Settings** → **Sign-in** → **Organizations** | `auth.tenancy` (the claims, then `enabled`). Turning it on or off ends the earlier sessions of the provider's people. The claims cannot change while it is on. Also `POST /api/settings/auth/tenancy`. |
| **Settings** → **Alerts** → **Set up mail server**, **Remove** | `alerting.smtp` (and `alerting.smtp.credentialRef`, the Secret `geass-alert-smtp`, key `password`). **Remove** clears it and deletes that Secret. |
| **Settings** → **Alerts** → **Slack channel**, **Email**, a channel's **Edit**, **Pause**, **Delete** | An entry of `alerting.channels`. A Slack channel's webhook URL is the key of its id in the Secret `geass-alert-channels`, which **Delete** removes. See [Alerts](/docs/guides/alerts/). |
| **Settings** → **Sign-in** → **Save**, **Use ... to sign in**, **Remove** | `auth.oidc` (and `auth.oidc.credentialRef`, the Secret `platform-auth`, key `clientSecret`), then `auth.mode`. **Switch back to passwords** sets `auth.mode` to `local`. **Remove** clears `auth` and deletes that Secret. |

The dashboard has no screen for `prometheusURL`, `defaultClusterRef`, `observability` or `registry`. Set the first three with `kubectl`. An admin can set `registry` with `POST /api/settings/registry/save` (see the [REST API](/docs/reference/rest-api/)), which stores the login as the Secret `platform-registry`, or with `kubectl`.

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

The routes are the dashboard's host and the host of every service that has one. The controller works out what it would send first, and compares it with what it last sent in full. When nothing has changed it sends nothing to Cloudflare, and looks again after five minutes, which catches a route or record that someone deleted in Cloudflare. It reacts at once when a service gets, changes or loses a host, because it watches the services for changes to their spec. A CNAME record that cannot be created is only logged, and the controller tries again in five seconds. The route of a service that [sleeps when idle](/docs/guides/scale-to-zero/) points at the add-on's wake proxy instead of at the service, because the tunnel does not go through Traefik. The platform configuration is also read by the build controller (the registry), the dashboard (metrics address, the stores outside the cluster, GitHub readiness), project creation (the default cluster), the cluster controller (the stores outside the cluster, which it adds to the monitoring and logging add-ons, with the copies of their credentials beside them), and the project and organization controllers (the default noisy-neighbour limits, which they write into each organization's namespaces whenever `spec.tenantLimits` changes, and the outbound limits of `spec.tenancy.egress`, which they write into the network policies of organizations' namespaces).

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
