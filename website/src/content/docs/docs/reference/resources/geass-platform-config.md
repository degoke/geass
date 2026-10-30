---
title: "GeassPlatformConfig"
description: "Reference for GeassPlatformConfig, the one resource for the dashboard domain, Cloudflare and GitHub App links, metrics address, registry, sign-in, and alerts."
sidebar:
  order: 9
---

The `GeassPlatformConfig` holds the settings that belong to the whole platform, not to one project:

- The domain of the dashboard, and how Geass publishes it.
- The link to the Cloudflare connection.
- The link to the GitHub App.
- Where to find Prometheus.
- The registry that repository builds push to.
- How people sign in: passwords from a Secret, or an identity provider.
- Where Geass sends alerts about builds, deploys, services, backups, and nodes (Slack or email).
- The metrics and log stores outside the cluster that Geass sends to and the dashboard reads from.

There is one `GeassPlatformConfig`, named `platform`, in `geass-system`. The dashboard creates it the first time that
you save a domain, a Cloudflare connection, or a GitHub App. The dashboard edits it from **Settings**. You can also
create and edit it with `kubectl`.

For all the settings, with the operator flags and environment variables, see
[Configuration](/docs/reference/configuration/). For the connections that this resource points at, see
[Cloud connectors](/docs/guides/connectors/).

## Summary

| Property | Value |
| --- | --- |
| API version | `geass.geass.dev/v1alpha1` |
| Kind | `GeassPlatformConfig` |
| Plural | `geassplatformconfigs` |
| Scope | Namespaced |
| Namespace | `geass-system` |
| Name | `platform`. The dashboard and the controllers read only this name. |
| Subresources | `status` |
| Printer columns | None |

## Example

This example is a dashboard at `geass.example.com`, published with a Cloudflare Tunnel. The Cloudflare and GitHub App
credentials are already saved in their Secrets.

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

In the next example, people sign in through an identity provider instead of with the users in the Secret. The
provider is Better Auth. A member of the `acme` organization is a viewer, and an owner or admin is an admin. The client
secret is the `clientSecret` key of the Secret `platform-auth`.

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

In the next example, the code of organizations runs under gVisor, except on nodes reserved for the organization. For
what the nodes need first, see [Sandboxed runtime for organizations](/docs/operations/sandboxed-runtime/).

```yaml title="platform.yaml (spec.tenancy)"
spec:
  tenancy:
    sandbox:
      runtimeClassName: gvisor
      skipOnReservedNodes: true
```

Test the provider from **Settings** → **Sign-in** before you set `mode: external` by hand. When you set it by hand, you
skip the checks that the dashboard makes before it switches: a test sign-in as an admin of the provider, and an admin
with a password.

The next example is a mail relay for organizations. The relay signs each organization in with its own login, holds it
to its limits, and sends through Amazon SES (see [Mail relay](/docs/guides/mail-relay/)):

```yaml title="platform.yaml (spec.tenancy)"
spec:
  tenancy:
    mail:
      relay:
        enabled: true
        upstream:
          host: email-smtp.eu-west-1.amazonaws.com
          credentialRef:
            name: mail-upstream
        limits:
          messagesPerDay: 2000
        requireSenderDomain: true
```

In the next example, the builds of organizations never get the registry login of the platform, and Geass keeps the
images of each organization apart (see [Registry isolation for organizations](/docs/operations/registry-isolation/)):

```yaml title="platform.yaml (spec.tenancy)"
spec:
  tenancy:
    registry:
      policy: requireOwnRegistry
```

The next example turns on abuse detection for the services of organizations. Geass leaves a suspect running until a
platform administrator has examined it (see [Abuse detection](/docs/guides/abuse-detection/)):

```yaml title="platform.yaml (spec.tenancy)"
spec:
  tenancy:
    abuse:
      action: None
      cpu:
        thresholdPercent: 90
        windowMinutes: 30
```

## Spec fields

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.rootDomain` | string | none | Lowercase hostname with at least one dot, for example `example.com` | Your apex domain. Geass serves the dashboard at `geass.<rootDomain>` (or a different subdomain, see `dashboardURL`). If it is empty and the host of `dashboardURL` starts with `geass.`, Geass takes the domain from `dashboardURL`. |
| `spec.dashboardURL` | string | none | `^https://[a-zA-Z0-9.-]+(?::[0-9]+)?$`: HTTPS, host and optional port, no path | The public address of the dashboard. Used for the callback and webhook URLs of the GitHub App. When it is empty, Geass uses `https://geass.<rootDomain>`. |
| `spec.dashboardExposure` | string | `ingress` | `ingress` or `cloudflare-tunnel` | How Geass publishes the dashboard. `ingress` creates an Ingress (and, when Cloudflare is connected, a proxied A record). `cloudflare-tunnel` runs a tunnel instead. |
| `spec.tunnelCNAMETarget` | string | none | `^[a-z0-9-]+(\.[a-z0-9-]+)+$` | The hostname of the tunnel, `<tunnel id>.cfargotunnel.com`. Geass sets it itself when it manages the tunnel. Set it by hand only if you run your own tunnel. |
| `spec.cloudflareConnectionRef.name` | string | none | none | The Secret with the Cloudflare credentials (`apiToken`, `accountId`, and optionally `zoneName` and `zones`). When you set it, the Cloudflare connection is on. |
| `spec.cloudflareZoneID` | string | none | none | The Cloudflare DNS zone for automatic records. When it is empty, Geass selects the zone whose name is the root domain, or the only zone that the token can see. |
| `spec.githubAppRef.name` | string | none | none | The Secret with the GitHub App credentials (`app_id`, `client_id`, `client_secret`, `slug`, `private_key`, `webhook_secret`). |
| `spec.prometheusURL` | string | none | none | Where the dashboard queries metrics. Has priority over the `GEASS_PROMETHEUS_URL` environment variable and the default service address. `spec.observability.external.metrics.queryURL` has priority over it. |
| `spec.defaultClusterRef.name` | string | none | none | The cluster that new projects use when more than one `GeassCluster` exists. With one cluster, Geass ignores it. |
| `spec.registry.repository` | string | none | none | The image destination for repository builds when a service sets no `build.registry.repository`. Used exactly as written. |
| `spec.registry.credentialRef.name` | string | none | none | A Secret with the key `.dockerconfigjson` for that registry. Builds push with it. Geass copies it into the namespace of a service, so that the nodes can pull the built image with it. Under `spec.tenancy.registry`, never for a project of an organization. |
| `spec.tenancy.registry.policy` | string | none (off). `requireOwnRegistry` when `registry` is set | `requireOwnRegistry` | For the projects of organizations. Their builds never get the registry login of the platform, or the login of a different organization. Their namespaces never hold a copy of it. They push to the organization's own registry under `<repository>/<project>/<service>`. An organization without its own registry does not build. Their services cannot run an image in the repository of the platform or in the registry of a different organization. Refusals are the condition `RegistryIsolated` `False` on the service or build. Geass does not change the projects that have no organization. Not on the settings page. See [Registry isolation for organizations](/docs/operations/registry-isolation/). |
| `spec.defaultDomain` | string | none | none | In the schema. No code reads it. |
| `spec.traefikAccessLogs.enabled`, `.format`, `.networkLogEndpoint` | boolean, string, string | none | none | In the schema. No code reads them. See [GeassNetworkLog](/docs/reference/resources/geass-network-log/). |
| `spec.auth.mode` | string | `local` | `local` or `external` | How people sign in. `local` is the users in the Secret `geass-dashboard-auth`. `external` is the identity provider in `spec.auth.oidc`. With `external`, a user of the Secret who is an admin and has a password can still sign in. See [Sign in with an identity provider](/docs/guides/external-sign-in/). |
| `spec.auth.oidc.displayName` | string | none | At most 60 characters | The name of the provider on the sign-in button. |
| `spec.auth.oidc.issuer` | string | none | At most 2048 characters | The issuer URL of the provider, exactly as its discovery document gives it. `https`, and not this machine or a link-local address. |
| `spec.auth.oidc.clientID` | string | none | At most 512 characters | The ID that Geass is registered with at the provider. |
| `spec.auth.oidc.credentialRef.name` | string | none | none | A Secret in `geass-system` whose `clientSecret` key is the client secret. For a public client that uses only PKCE, do not set it. |
| `spec.auth.oidc.scopes` | list of strings | `email`, `profile` | At most 20 | Scopes that Geass asks for in addition to `openid`. |
| `spec.auth.oidc.roleClaim` | string | none | At most 253 characters | The claim of the ID token that holds the role of a person at the provider: a name, or a dotted path into nested claims. A string, a list, or a comma-separated string. |
| `spec.auth.oidc.roleMap` | map of strings | none | At most 50 entries. Values `admin` or `viewer` | The Geass role for each role at the provider. A person whose roles map to no Geass role cannot sign in. The highest role applies. |
| `spec.auth.oidc.requireClaim` | map of strings | none | At most 10 entries | Claims that the ID token must have, with these values (or a list that contains them), for a person to sign in. |
| `spec.auth.oidc.sessionMinutes` | integer | `60` | 5 to 720 | How long a sign-in lasts before the person must go back to the provider. |
| `spec.auth.oidc.revokeTokensOnLogout` | boolean | `true` | none | Whether a back-channel logout from the provider also revokes the API tokens and deploy hooks that the person made. The logout always ends the sessions of the person. Turn it off when CI depends on tokens made from the accounts of people who sign out. |
| `spec.auth.oidc.accessTokens.enabled` | boolean | `false` | Has an effect only while `spec.auth.mode` is `external` | Whether Geass accepts the JWT access tokens of the provider (RFC 9068, `typ` `at+jwt`) as `Authorization: Bearer`. Geass accepts a token for the person that it was issued to, with the role and organizations that its claims map to, the same as for an ID token. Not on the settings page. When you save the provider, Geass keeps this value. See [Access tokens as a bearer credential](/docs/guides/external-sign-in/#access-tokens-as-a-bearer-credential). |
| `spec.auth.oidc.accessTokens.audience` | string | the client ID | At most 512 characters | A value that the `aud` of the token must contain. Thus, Geass refuses a token for a different cluster of the same provider. |
| `spec.auth.tenancy.enabled` | boolean | `false` | Has an effect only while `spec.auth.mode` is `external` | Whether people belong to organizations, and each organization sees only its own projects. While it is off, everyone who can sign in sees everything, as before. While it is on, only the administrators of the platform see a project that belongs to no organization. See [Organizations and workspaces](/docs/guides/organizations-and-workspaces/). |
| `spec.auth.oidc.refreshMinutes` | integer | `0` (off) | 0, or 5 to 1440 | How often Geass asks the provider again about each signed-in person. Thus, Geass notices a changed role or a removed person within minutes. Adds the `offline_access` scope. See [Check people again](/docs/guides/external-sign-in/#check-people-again-while-they-are-signed-in). |
| `spec.auth.oidc.staleGraceMinutes` | integer | `120` | 5 to 1440 | How long the provider can be unreachable before Geass ends the sessions that it could not check. |
| `spec.auth.tenancy.orgsClaim` | string | none | At most 253 characters | The claim of the ID token that lists the organizations of the person: a name, or a dotted path. A list of objects. Each object has the `id` of the organization, the `role` of the person in it (mapped by `roleMap`), and optionally `workspaces`. When this field is set, `roleClaim` is not necessary: Geass reads the role from each organization. |
| `spec.auth.tenancy.autoProvision.mode`, `.allow`, `.max` | `Pending` or `Open`. List of strings. Integer | none (off) | `allow` at most 50 entries. `max` 1 to 500, default 20. `Open` needs `allow` | Makes an organization of the provider that Geass does not know, when one of its administrators signs in with a verified address. `Pending` makes it suspended until approved. `Open` makes it ready, for the IDs in `allow` (an ID as it is, or a prefix followed by `*`). See [Sign in with an identity provider](/docs/guides/external-sign-in/#organizations-made-by-sign-in). |
| `spec.auth.tenancy.platformClaim`, `.platformValues` | string, list of strings | none | At most 253 characters. At most 20 values | A claim whose value, compared as a whole, makes a person an administrator of the whole platform and not of an organization. Without these fields, only the admins who sign in with a password are platform administrators. |
| `spec.usage.enabled` | boolean | `false` | none | Whether the operator meters what the projects of each organization reserve (CPU and memory requests, and the size of bound volumes). The operator records this hour by hour into the ConfigMaps `geass-usage-<YYYYMMDDHH>` in `geass-system`, keeps them 62 days, and serves them at `GET /api/v1/usage/hours`. When you turn it off, the meter stops, and Geass keeps what it stored. Not on the settings page. See [Usage by organization, for billing](/docs/guides/usage-and-capacity/#usage-by-organization-for-billing). |
| `spec.managed.enabled` | boolean | `false` | none | Whether a platform runs this Geass and keeps these items for itself: the installer, updates, the K3s release, sign-in, and the domain and DNS of the dashboard. The API of the dashboard refuses writes to them (`403`, `code` `managed`), and the dashboard shows them read-only. Has an effect with no restart. If it is off or not set, nothing changes. Not on the settings page, and no route of the API writes it. See [Managed mode](/docs/operations/managed-mode/). |
| `spec.managed.provider` | string | none | At most 60 characters: letters, digits, spaces, dots, underscores, hyphens | Who manages the cluster, for example `rungeass`, for the messages: "managed by rungeass". If it is empty, the messages say "the platform that runs this cluster". |
| `spec.tenancy.sandbox.runtimeClassName` | string | none (off) | 1 to 253 characters, a DNS subdomain. Required when `sandbox` is set | The RuntimeClass (gVisor, Kata) that the pods of the projects of organizations run with. These pods are the copies of their services, the schedules of their functions, their builds, and their MySQL, Redis, and SQLite servers, with the Jobs that copy, restore, and make databases on those servers. The servers are those of their projects, and shared servers that an organization owns. Geass does not change the projects that have no organization, the shared servers of the platform, or the pods of Geass itself. While the cluster has no RuntimeClass of this name, a service, build, or new database server of an organization waits, with the condition `Sandboxed` `False` `RuntimeClassMissing`. Geass never starts it without the RuntimeClass. The operators of PostgreSQL and MariaDB cannot run a server under a RuntimeClass. Thus, Geass does not make a PostgreSQL or MariaDB server for an organization, and holds one made before as it is (`EngineCannotBeSandboxed`). A change of this field rolls the services of the organizations. A running database server moves only as `databaseRestarts` says. Not on the settings page. See [Sandboxed runtime for organizations](/docs/operations/sandboxed-runtime/). |
| `spec.tenancy.sandbox.skipOnReservedNodes` | boolean | `false` | none | Runs without the sandbox the pods that the scheduler can put only on nodes reserved for their own organization. These pods ask for a tag that the organization requires, no other organization requires that tag, and every node that has the tag is reserved for that organization. Geass sandboxes all pods where this is less certain. See [Reserved nodes](/docs/operations/sandboxed-runtime/#reserved-nodes). |
| `spec.tenancy.sandbox.databaseRestarts` | string | `OnApproval` (also when empty) | `OnApproval` or `Immediate` | When Geass restarts a database server of an organization that already runs, to move it into the sandbox, out of it, or to a different class. This restarts the database. `OnApproval`: only after a platform administrator annotates its GeassDatabase with `geass.dev/sandbox-restart`. Until then, the server runs as before, with `Sandboxed` `RestartPending`. `Immediate`: every such server, immediately. A new server, or a server that was never ready, takes the sandbox immediately in both modes. See [Servers that already run](/docs/operations/sandboxed-runtime/#servers-that-already-run). |
| `spec.tenancy.egress` | object | none (no limits) | none | Limits what the pods of the projects of organizations can reach outside the cluster. The limits apply in each environment of their projects and in their build namespaces. If you set this field at all, mail is closed. Geass never limits a project that has no organization. The `spec.egress` of an organization replaces each field that it sets. See [Outbound limits](/docs/guides/outbound-limits/). |
| `spec.tenancy.egress.mail` | string | `Blocked` | `Blocked` or `Open` | `Blocked` closes TCP 25, 465, 587, 2465, 2525, and 2587 to addresses outside the cluster, except where `allow` opens them. |
| `spec.tenancy.egress.allowedPorts` | list of ports | none (every port open except the closed ones) | 1 to 64 | When set, these are the only ports open to addresses outside the cluster, minus the closed ports. The API port of the cluster stays open from project environments. |
| `spec.tenancy.egress.blockedPorts` | list of ports | none | At most 64 | Ports closed to addresses outside the cluster, in addition to the mail ports. For example, the ports that mining pools listen on. This is a heuristic, and it detects nothing. |
| `spec.tenancy.egress.miningPools` | string | `Open` | `Blocked` or `Open` | `Blocked` closes a curated list of the TCP ports that public mining pools listen on by default (3333 to 3336, 4444, 5555, 7777, 14433, 14444, and 45700), except where `allow` opens them. This is a heuristic. It does not stop a miner on port 443 or behind a proxy, and it detects nothing. See [Mining](/docs/guides/outbound-limits/#mining). |
| `spec.tenancy.egress.allow[].cidr`, `.ports`, `.description` | string. List of ports. String | none | At most 32. `cidr` is an IPv4 range written from its first address, not in `169.254.0.0/16` or `127.0.0.0/8`. `ports` 1 to 16. `description` at most 200 characters | Opens the ports to that range, even if other settings close them. For example, 587 to a mail relay. It cannot open the metadata range, the nodes of a project that keeps away from them, or the private ranges of a build. |
| `spec.tenancy.abuse` | object | none (off) | none | Looks for services of the projects of organizations that might abuse the machines, for example by mining. The signals are CPU use that stays near what the copies are allowed, and reports from a detector outside Geass. A suspect gets the condition `AbuseSuspected`, an event, and the alert **Abuse suspected**. `GET /api/v1/abuse/suspects` lists it. A signal, not proof. See [Abuse detection](/docs/guides/abuse-detection/). |
| `spec.tenancy.abuse.action` | string | `None` | `None` or `Suspend` | `Suspend` scales a suspect to zero copies until a platform administrator releases or clears it. Geass deletes nothing. The `spec.abuse.action` of an organization replaces it. Geass reads it when it raises a suspicion. A change from `None` to `Suspend` does not suspend a service that is already a suspect. |
| `spec.tenancy.abuse.reviewExemptionDays` | integer | 30 | 1 to 365 | How long Geass leaves a service that a platform administrator cleared out of the CPU signal. |
| `spec.tenancy.abuse.cpu.thresholdPercent`, `.windowMinutes` | integer | 90, 30 | 50 to 100. 10 to 1440 | Geass suspects a service when every sample in the window is at or above this share of the CPU limits (or requests) of its copies. Geass takes one sample each minute, from `metrics.k8s.io`. |
| `spec.tenancy.abuse.cpu.quietTraffic.maxRequestsPerMinute` | integer | none | 0 or more | Geass counts a sample only while the ingress passes the service this many requests a minute or fewer (from Prometheus, with the monitoring add-on). |
| `spec.tenancy.abuse.cpu.quietTraffic.withoutTrafficData.thresholdPercent`, `.windowMinutes` | integer | the larger of 95 and the threshold. Two times the window | 50 to 100. 10 to 1440 | The values that apply instead while Geass cannot read the requests. |
| A port: `port`, `endPort`, `protocol` | integer, integer, string | `protocol` `TCP` | `port` and `endPort` 1 to 65535, `endPort` not below `port`. `TCP` or `UDP` | One port, or the range from `port` to `endPort`, both included. |
| `spec.tenancy.mail.relay.enabled` | boolean | `false` | Needs `upstream` | Runs the mail relay `geass-mail-relay` in `geass-system`. Gives each organization a login (`geass-mail-credentials-<organization>`), and gives each environment of its projects the Secret `geass-mail`. When you turn it off, Geass removes all of them. Not on the settings page. See [Mail relay](/docs/guides/mail-relay/). |
| `spec.tenancy.mail.relay.upstream.host`, `.port` | string, integer | none. `587`, or `465` with `tls: TLS` | A host name or IPv4 address, at most 253 characters. 1 to 65535 | The mail service that the relay gives every message to. |
| `spec.tenancy.mail.relay.upstream.tls` | string | `StartTLS` | `StartTLS`, `TLS`, or `None`. `None` cannot have `credentialRef` | `StartTLS` requires the upstream to offer STARTTLS. If it does not, the relay sends nothing. `TLS` is TLS from the first byte. `None` is plain text, for an upstream in the cluster or on a private network. The relay checks the certificate of the upstream against its host name and the public authorities. |
| `spec.tenancy.mail.relay.upstream.credentialRef.name` | string | none | none | A Secret in `geass-system` with `username` and `password`, which the relay uses to sign in to the upstream. Geass mounts it into the relay pod, so the relay uses a new password without a restart. |
| `spec.tenancy.mail.relay.limits.messagesPerMinute`, `.messagesPerHour`, `.messagesPerDay` | integers | 60, 600, 2000 | 0 to 100000, 1000000, and 10000000 | The maximum number of messages that each organization can send in any 60 seconds, 60 minutes, and 24 hours. `0` sends nothing. The `spec.mail.limits` of an organization replaces each field that it sets. |
| `spec.tenancy.mail.relay.limits.recipientsPerMessage`, `.connections` | integers | 50, 5 | 1 to 1000 each | The maximum number of recipients of one message, and of connections signed in at the same time. |
| `spec.tenancy.mail.relay.limits.maxMessageSize` | quantity | `10Mi` | 1Ki to 50Mi, checked by the operator | The largest message. |
| `spec.tenancy.mail.relay.allowedSenderDomains` | list of strings | none (any sender) | At most 50 domains | The domains that organizations can send from: the envelope sender, and the addresses of the `From` and `Sender` headers. A domain covers the domains below it. An organization's own list replaces this list. |
| `spec.tenancy.mail.relay.requireSenderDomain` | boolean | `false` | none | Refuses every message of an organization that has no allowed sender domain, its own or the platform's. |
| `spec.tenancy.mail.relay.deniedRecipientDomains` | list of strings | none | At most 200 domains | Domains, and the domains below them, that the relay never sends to. |
| `spec.alerting.smtp.host` | string | none | 1 to 253 characters | The mail server that email channels send through. Required when `smtp` is set. Alertmanager sends the mail, from the `monitoring` namespace. |
| `spec.alerting.smtp.port` | integer | `587` | 1 to 65535 | The port of the mail server. |
| `spec.alerting.smtp.security` | string | `starttls` | `starttls`, `tls`, or `none` | How Alertmanager protects the connection. `starttls` upgrades a plain connection (port 587). `tls` is encrypted from the first byte, and Alertmanager does that only on port 465. `none` sends in plain text, and cannot have a username, because Alertmanager does not send a password without encryption. |
| `spec.alerting.smtp.username` | string | none | At most 256 characters | The user to sign in as. For a server that does not ask, do not set it. |
| `spec.alerting.smtp.credentialRef.name` | string | none | none | A Secret in `geass-system` whose `password` key is the password. The dashboard makes the Secret `geass-alert-smtp`. |
| `spec.alerting.smtp.from` | string | none | 3 to 320 characters. An address, or `Name <address>` | The address that alerts come from. Required when `smtp` is set. |
| `spec.alerting.channels` | list | none | At most 20 | The places that alerts go to. |
| `spec.alerting.channels[].id` | string | none | `^[a-z0-9]{8}$` | Identifies the channel, and names its secret. |
| `spec.alerting.channels[].name` | string | none | 1 to 60 characters | The name of the channel in the dashboard. |
| `spec.alerting.channels[].type` | string | none | `slack` or `email` | A Slack incoming webhook (or any service that takes the same JSON), or email. |
| `spec.alerting.channels[].recipients` | list of strings | none | At most 20 | The addresses that an email channel writes to. |
| `spec.alerting.channels[].events` | list of strings | every kind | `build.failed`, `deploy.failed`, `service.down`, `backup.failed`, `node.unhealthy`, `cluster.issue`, `workload.crashlooping`, `storage.filling` | The alerts that the channel gets. Empty means all, including kinds that are added later. |
| `spec.alerting.channels[].projects` | list of strings | every project | At most 100 | Limits the channel to these projects. Only a channel with no projects also gets alerts about the cluster itself. |
| `spec.alerting.channels[].environments` | list of strings | every environment | At most 100 | Limits the channel to these environments. This field does not limit alerts about the cluster. |
| `spec.alerting.channels[].notifyResolved` | boolean | `true` | none | Whether the channel also gets a message when a problem is resolved. |
| `spec.alerting.channels[].disabled` | boolean | `false` | none | Pauses the channel without deleting it. |
| `spec.observability.external.metrics.remoteWriteURL` | string | none | `^https?://[^\s]+$`, at most 2048 characters. No username or password in it | Prometheus remote-writes every sample to it, and also keeps its own copy. Needs the monitoring add-on. See [Send metrics and logs outside the cluster](/docs/guides/observability/#send-metrics-and-logs-outside-the-cluster). |
| `spec.observability.external.metrics.queryURL` | string | none | As above, and no query string | A Prometheus-compatible query API. Geass adds `/api/v1/query` and `/api/v1/query_range` to it. When it is set, the dashboard reads every metric from it, and never from the Prometheus of the cluster. |
| `spec.observability.external.metrics.tenant` | string | none | `^[A-Za-z0-9!._*'()-]+$`, at most 150 characters | Sent in `tenantHeader` with every write and query. If it is empty, Geass sends no tenant header. |
| `spec.observability.external.metrics.tenantHeader` | string | `X-Scope-OrgID` | `^[A-Za-z0-9-]+$`, at most 64 characters. Not a header that Prometheus sets itself, for example `Authorization` | The header that carries the tenant. If Geass refuses the header, it does not use the store, and the operator logs the reason. |
| `spec.observability.external.metrics.credentialRef.name` | string | none | none | A Secret in `geass-system` with a `token` key (bearer), or `username` and `password` keys (basic). Geass copies the keys into `geass-external-metrics` in `monitoring`, which Prometheus reads. |
| `spec.observability.external.logs.pushURL` | string | none | `^https?://[^\s]+$`, at most 2048 characters. No username, password, or query string | A VictoriaLogs server that Fluent Bit also sends every line to. Geass adds `/insert/jsonline` to it. Needs the logging add-on. Geass does not collect the logs of projects that require node tags, so it does not send them. |
| `spec.observability.external.logs.queryURL` | string | none | As `pushURL` | A VictoriaLogs server that answers LogsQL. Geass adds `/select/logsql/query` to it. When it is set, Geass reads log history from it, and never from the store of the cluster. |
| `spec.observability.external.logs.accountID`, `.projectID` | integer | `0` | 0 to 4294967295 | The VictoriaLogs tenant, sent as the `AccountID` and `ProjectID` headers with every line and query. |
| `spec.observability.external.logs.credentialRef.name` | string | none | none | As for metrics. Geass copies the keys into `geass-external-logs` in `geass-logging`, which Fluent Bit reads as environment variables. |
| `spec.observability.external.logs.retentionDays` | integer | the value of the logging add-on | 1 to 3650 | How long the store keeps logs. Log history reports this value. |
| `spec.tenantLimits.defaults.pods` | integer | none | 1 to 100000 | The maximum number of pods, running or waiting, in each namespace of every organization: each environment of its projects, and its build namespace. A ResourceQuota `geass-tenant-quota` in each namespace. See [Noisy-neighbour limits](/docs/guides/tenant-limits/). |
| `spec.tenantLimits.defaults.ephemeralStorage` | quantity | none | Above zero, checked by the operator. Needs a default limit for the ephemeral storage of containers | The maximum ephemeral storage for all the pods of each such namespace together (the total of the limits and requests of their containers). |
| `spec.tenantLimits.defaults.container.defaultRequest`, `.defaultLimit`, `.max` | each `cpu`, `memory`, `ephemeralStorage` (quantities) | none | Above zero. `defaultRequest` at most the limit that a container gets, `defaultLimit` at most `max`. Checked by the operator | The request and the limit that a container gets for a resource that it asks for none of, and the maximum that one container can set. A LimitRange in each namespace. |
| `spec.tenantLimits.defaults.bandwidth.ingress`, `.egress` | quantity | none | `1k` to `1P`, checked by the operator | The maximum that each pod can receive and send, in bits per second, through the annotations of the CNI bandwidth plugin. Needs Kubernetes 1.36 and a network plugin that acts on the annotations. |

:::caution
Set `build.registry.repository` on each service. Do not rely on `spec.registry.repository`. That field is the full
destination image of every build that does not set its own, and Geass adds no service name and no tag. Every service
that relies on it pushes to the same reference. Without any registry setting, a repository service waits with
`no container registry is set for builds`: Geass has no registry of its own. See
[GeassBuild](/docs/reference/resources/geass-build/).
:::

## Status

| Field | Type | Meaning |
| --- | --- | --- |
| `status.cloudflareTunnelID` | string | The ID of the Cloudflare tunnel that Geass manages, when the tunnel is in use. |
| `status.conditions` | list | `DashboardDomainReady`, `CloudflareReady`, and `MailRelayReady` while the mail relay is set. |

### DashboardDomainReady

This condition says whether the dashboard answers at its public address. The condition is `True` only for the
generation of the spec that the controller checked. Thus, any change to `spec` makes the controller check again.

- While the condition is false, the controller checks every 5 seconds.
- After 15 minutes without success, the controller reports `VerificationTimedOut` and checks every 5 minutes. Thus, a
  late DNS fix still repairs the condition.

| Reason | Status | Meaning |
| --- | --- | --- |
| `Verified` | `True` | The HTTPS address of the dashboard answers the Geass probe (`/geass-probe`), or DNS and the probe agree. |
| `NoDomain` | `False` | There is no root domain. |
| `PendingDNS` | `False` | The host does not resolve to the public IP of the cluster yet, or Geass could not create the Cloudflare record. The message says which. |
| `PendingHTTPS` | `False` | DNS is correct, but `https://<host>/geass-probe` is not reachable yet. The certificate might still be pending. In ingress mode, when a [GeassCluster](/docs/reference/resources/geass-cluster/) reports `ClusterIssuerReady: False`, the message ends with its reason in parentheses. For example, that no ACME contact email is set. |
| `PendingTunnel` | `False` | Tunnel mode, and no tunnel hostname is known yet. |
| `ZoneRequired` | `False` | Cloudflare is connected, but the token sees several zones and none matches the root domain. Select a zone in **Settings** → **Domain**. |
| `Error` | `False` | The Cloudflare credentials are unavailable or incomplete, the controller did not find the dashboard Service, or the controller could not list the cluster nodes. |
| `VerificationTimedOut` | `False` | The check has been pending for 15 minutes. The controller continues to try. |

### CloudflareReady

This condition reports the Cloudflare side. The controller sets it only when `cloudflareConnectionRef` is set.

| Reason | Status | Meaning |
| --- | --- | --- |
| `Connected` | `True` | Ingress mode: Cloudflare is connected. When there is a root domain, the proxied A record of the dashboard exists. |
| `Ready` | `True` | Tunnel mode: the tunnel exists, `cloudflared` is deployed, and the routes are published. |
| `CredentialsIncomplete` | `False` | The Secret does not have the API token or account ID, or the controller could not select a zone. |
| `CredentialsInvalid` | `False` | Cloudflare rejected the token. The message is the reason that Cloudflare gave. |
| `TunnelError` | `False` | The controller could not find or create the tunnel. |
| `TunnelTokenError` | `False` | The controller could not get the connector token of the tunnel. |
| `TunnelConfigError` | `False` | The controller could not publish the routes of the tunnel. |
| `DNSError` | `False` | Ingress mode: the controller could not create the A record of the dashboard. |

While `CloudflareReady` is true, services without a host get a default host under the root domain. See
[Connect Cloudflare](/docs/guides/connectors/cloudflare/).

### MailRelayReady

This condition says whether the [mail relay](/docs/guides/mail-relay/) runs. The condition is there only while
`spec.tenancy.mail.relay` is enabled.

| Reason | Status | Meaning |
| --- | --- | --- |
| `Ready` | `True` | The relay pod is ready. The message names its address and the upstream. |
| `Starting` | `False` | The pod is not ready yet. |
| `UpstreamCredentialsMissing` | `False` | The Secret that `upstream.credentialRef` names is missing, or does not have `username` or `password`. |
| `ImageUnknown` | `False` | The controller could not read the Deployment of the operator. The relay runs the image of that Deployment. |
| `InvalidSettings` | `False` | Geass cannot apply the settings (for example, a message size over 50Mi, or a login over plain text). The relay keeps the configuration that it had, and Geass records the `Warning` event `MailRelaySettingsInvalid`. |

## How the dashboard maps to it

| Dashboard | Writes |
| --- | --- |
| **Settings** → **Domain** → **Save dashboard domain** | `rootDomain`, `dashboardURL` (from **Subdomain**, default `geass`), `dashboardExposure` (**Publish via**), `tunnelCNAMETarget`, `cloudflareZoneID`. The verify action of the page sets the `DashboardDomainReady` condition. |
| **Settings** → **Connectors** → **Cloudflare** → **Connect** | `cloudflareConnectionRef` (the Secret `platform-cloudflare`) and `cloudflareZoneID`. **Disconnect** clears both. |
| **Settings** → **Connectors** → **GitHub** | `githubAppRef` (the Secret `platform-github-app`). **Remove GitHub App** clears it. |
| **Settings** → **Sign-in** → **Also revoke the tokens and deploy hooks they made** | `auth.oidc.revokeTokensOnLogout`. Changes immediately, in both modes. |
| **Settings** → **Sign-in** → **Organizations** | `auth.tenancy` (the claims, then `enabled`). When you turn it on or off, Geass ends the earlier sessions of the people of the provider. The claims cannot change while it is on. Also `POST /api/settings/auth/tenancy`. |
| **Settings** → **Alerts** → **Set up mail server**, **Remove** | `alerting.smtp` (and `alerting.smtp.credentialRef`, the Secret `geass-alert-smtp`, key `password`). **Remove** clears it and deletes that Secret. |
| **Settings** → **Alerts** → **Slack channel**, **Email**, a channel's **Edit**, **Pause**, **Delete** | An entry of `alerting.channels`. The webhook URL of a Slack channel is the key of its ID in the Secret `geass-alert-channels`. **Delete** removes that key. See [Alerts](/docs/guides/alerts/). |
| **Settings** → **Sign-in** → **Save**, **Use ... to sign in**, **Remove** | `auth.oidc` (and `auth.oidc.credentialRef`, the Secret `platform-auth`, key `clientSecret`), then `auth.mode`. **Switch back to passwords** sets `auth.mode` to `local`. **Remove** clears `auth` and deletes that Secret. |

The dashboard has no screen for `prometheusURL`, `defaultClusterRef`, `observability`, or `registry`. Set the first
three with `kubectl`. To set `registry`, an admin can use `POST /api/settings/registry/save` (see the
[REST API](/docs/reference/rest-api/)), which stores the login as the Secret `platform-registry`. An admin can also set
`registry` with `kubectl`.

## Editing by hand

You can safely edit the spec with `kubectl`. The controller applies a change within seconds. Do not edit `status`.

```bash title="Edit and inspect"
kubectl -n geass-system edit geassplatformconfig platform
kubectl -n geass-system get geassplatformconfig platform \
  -o jsonpath='{range .status.conditions[*]}{.type}{"\t"}{.status}{"\t"}{.reason}{"\t"}{.message}{"\n"}{end}'
```

If you set `cloudflareConnectionRef` by hand, create the Secret `platform-cloudflare` first. If you clear it, delete
the Secret yourself.

## What happens under the hood

In ingress mode, the controller creates the Ingress `geass-dashboard` in `geass-system` for the dashboard host. The
Ingress is on the `websecure` entry point of Traefik, with TLS from cert-manager (the secret `geass-dashboard-tls`).
The controller then checks DNS and probes the HTTPS address. If Cloudflare is connected, the controller also creates a
proxied A record that points at the public IP of the cluster.

:::note
The Ingress asks cert-manager for a certificate from a ClusterIssuer named `letsencrypt-prod`. The operator creates
that issuer when `spec.addons.certManager.acmeEmail` is set on the
[GeassCluster](/docs/reference/resources/geass-cluster/). The installer sets it from `GEASS_ACME_EMAIL`. Without it,
cert-manager issues no certificate, and the condition stays at `PendingHTTPS`. See
[Security](/docs/operations/security/#what-geass-exposes).
:::

In tunnel mode, the controller creates the tunnel `geass`, the Secret `geass-cloudflared-token`, and the Deployment
`geass-cloudflared`. It publishes the routes and creates CNAME records. Without a Cloudflare connection, the controller
deletes the Deployment and the Secret.

The routes are the host of the dashboard and the host of every service that has one.

- First, the controller calculates what it would send. It compares that with what it last sent in full.
- When nothing has changed, the controller sends nothing to Cloudflare. It looks again after five minutes. This finds
  a route or record that someone deleted in Cloudflare.
- The controller watches the services for changes to their spec. Thus, it reacts immediately when a service gets,
  changes, or loses a host.
- If the controller cannot create a CNAME record, it only logs the error, and tries again in five seconds.
- The route of a service that [sleeps when idle](/docs/guides/scale-to-zero/) points at the wake proxy of the add-on,
  not at the service, because the tunnel does not go through Traefik.

These components also read the platform configuration:

- The build controller: the registry, and `spec.tenancy.sandbox` and `spec.tenancy.registry` for the builds of an
  organization.
- The service controller: `spec.tenancy.sandbox` and `spec.tenancy.registry`. A change of these fields reaches every
  service.
- The operator that holds the leader lease: `spec.usage`, for the meter.
- The dashboard: the metrics address, the stores outside the cluster, GitHub readiness, and `spec.managed`. With
  `spec.managed`, the API of the dashboard refuses writes to sign-in, the domain of the dashboard, and the
  cluster-state backup.
- Project creation: the default cluster.
- The cluster controller: the stores outside the cluster. The controller adds them to the monitoring and logging
  add-ons, with the copies of their credentials.
- The project and organization controllers:
  - The default noisy-neighbour limits. They write these limits into the namespaces of each organization whenever
    `spec.tenantLimits` changes.
  - The outbound limits of `spec.tenancy.egress`. They write these limits into the network policies of the namespaces
    of organizations.
- The abuse detection: `spec.tenancy.abuse`. This is the detector on the operator that holds the lease, and the
  controller that writes the `AbuseSuspected` conditions and removes them when the setting is removed.
- The controller of the mail relay: `spec.tenancy.mail`.
- The database controller: `spec.tenancy.sandbox`, for the database servers of organizations.
- The shared database controller: whether organizations are on, from `spec.auth.tenancy.enabled`.
- The alert routing controller: `spec.alerting`. It writes the AlertmanagerConfig of the cluster, with a receiver and
  a route for each alert channel.

## Troubleshooting

**`DashboardDomainReady` is `PendingDNS`.** The host does not point at the cluster yet. Create an A record for
`geass.<rootDomain>` at the public IP of the cluster. Or connect Cloudflare, so that Geass creates the record. The
message names the address that the controller expects.

**`PendingHTTPS` for a long time.** DNS is correct, but HTTPS does not answer. Do these checks:

- Check the Ingress and its certificate (`kubectl -n geass-system get ingress,certificate`).
- Make sure that the `letsencrypt-prod` ClusterIssuer exists and is ready (`kubectl get clusterissuer letsencrypt-prod`).
- If the ClusterIssuer is not ready, the `ClusterIssuerReady` condition of the `GeassCluster` gives the reason, for
  example `ACMEEmailMissing`.

**`ZoneRequired`.** Select the zone in **Settings** → **Domain**, or set `cloudflareZoneID`.

**`CloudflareReady` is `CredentialsInvalid`.** Update the token with **Update API token**. See
[Connect Cloudflare](/docs/guides/connectors/cloudflare/).

**The GitHub App cannot be saved: `dashboard URL must be configured first`.** The GitHub App needs a verified HTTPS
dashboard address. Finish the domain first.

**I changed an unrelated field and the dashboard briefly says the domain is not verified.** A spec change starts a new
verification. The condition returns to `Verified` at the next check.

## Related

- [Configuration](/docs/reference/configuration/)
- [Cloud connectors](/docs/guides/connectors/)
- [Domains and TLS](/docs/guides/domains-and-tls/)
- [GeassCluster](/docs/reference/resources/geass-cluster/)
- [Sleep when idle](/docs/guides/scale-to-zero/)
- [Resources overview](/docs/reference/resources/overview/)
