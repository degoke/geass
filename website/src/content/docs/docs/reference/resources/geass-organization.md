---
title: "GeassOrganization"
description: "Reference for GeassOrganization: an organization of the identity provider and its workspaces in Geass. Its people see only its projects."
sidebar:
  order: 11
---

A `GeassOrganization` tells Geass that an organization of your identity provider is a Geass organization. A person whose sign-in token names that organization can sign in and work in it. That person can see and change the projects of the organization. That person cannot see anything of another organization. The resource holds no members. Each time that a person signs in, Geass reads from the token who is in the organization, and in which of its workspaces.

Geass ignores these resources until organizations are turned on. To turn on organizations, set `spec.auth.tenancy.enabled` on the [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/). For the task-oriented view, with the shape of the token and the steps to turn on organizations, see [Organizations and workspaces](/docs/guides/organizations-and-workspaces/).

A platform administrator creates a `GeassOrganization` with the REST API (`POST /api/orgs/create`) or with `kubectl`. An organization does not create itself, with one exception. The platform can let the first sign-in of an organization's administrator create the organization. For more information, see [Organizations made by sign-in](/docs/guides/external-sign-in/#organizations-made-by-sign-in).

## Summary

| Property | Value |
| --- | --- |
| API version | `geass.geass.dev/v1alpha1` |
| Kind | `GeassOrganization` |
| Plural | `geassorganizations` |
| Scope | Namespaced |
| Namespace | `geass-system` |
| Subresources | None |
| Printer columns | `Name` (`spec.displayName`), `IdP ID` (`spec.idpID`), `Suspended` |

## Example

```yaml title="organization.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassOrganization
metadata:
  name: org-1a2b3c4d
  namespace: geass-system
spec:
  displayName: Acme
  idpID: org_acme
  workspaces:
    - id: ws-a1b2c3
      displayName: Payments team
      idpID: team_payments
    - id: ws-d4e5f6
      displayName: Web team
      idpID: team_web
```

Geass makes the name: `org-` and eight hex digits. The name does not change. Projects name the organization in `spec.org`.

## Spec fields

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.displayName` | string | none | Required. 1 to 100 characters | The name that the dashboard shows for the organization. |
| `spec.idpID` | string | none | Required. 1 to 253 characters | The identifier of the organization in the token of the identity provider. A person whose token names this identifier is a member. Two organizations cannot have the same identifier: the dashboard refuses a second one. |
| `spec.workspaces` | list | empty | At most 100, unique `id` | The groups of projects in the organization. |
| `spec.workspaces[].id` | string | none | Required. `^ws-[a-f0-9]{6}$` | The value that a project names in `spec.workspace`. Geass makes this value, and the value does not change. |
| `spec.workspaces[].displayName` | string | none | Required. 1 to 100 characters | The name that the dashboard shows for the workspace. |
| `spec.workspaces[].idpID` | string | none | Required. 1 to 253 characters | The identifier of the workspace (the team) in the token. A person who is in the workspace works in it. |
| `spec.clusterRef.name` | string | empty | An existing [GeassCluster](/docs/reference/resources/geass-cluster/) | The cluster in which Geass makes the new projects of this organization. Empty means the default cluster of the platform. A project that exists stays where it is. Geass runs on one cluster today, so this field changes nothing yet. |
| `spec.suspended` | boolean | `false` | none | Stops the people of the organization from signing in, and immediately stops their open sessions. The projects of the organization keep running. |
| `spec.maxProjects` | integer | none | 0 or more | The maximum number of projects that the organization can have. Checked when a project is made in the organization or given to it. 0 stops the organization from making any project. |
| `spec.quota.cpu`, `.memory`, `.storage`, `.pods` | quantity, quantity, quantity, integer | none | `pods` at least 1 | The maximum that all the projects of the organization can request together. The quota of each environment is held to what the other environments of the organization leave. See [Organizations and workspaces](/docs/guides/organizations-and-workspaces/#what-the-platform-sets-for-an-organization). |
| `spec.limits` | object | none | As `spec.tenantLimits.defaults` on the [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/) | The noisy-neighbour limits of the organization (`pods`, `ephemeralStorage`, `container`, `bandwidth`). Each limit replaces the default of the platform as a whole. Geass holds these limits in each environment of the organization's projects and in its build namespace. See [Noisy-neighbour limits](/docs/guides/tenant-limits/). |
| `spec.egress` | object | none | As `spec.tenancy.egress` on the [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/) | The outbound limits of the organization (`mail`, `allowedPorts`, `blockedPorts`, `allow`, `miningPools`). Each limit replaces the limit of the platform as a whole. An empty list replaces the platform's list with none. These limits apply to the organization even when the platform sets none. Geass holds them in the network policy of each environment of the organization's projects and of its build namespace. Only a platform administrator writes this field: no route of the dashboard changes it. See [Outbound limits](/docs/guides/outbound-limits/). |
| `spec.abuse` | object | none | `exempt`: boolean. `exemptProjects`: at most 100 project names. `action`: `None` or `Suspend` | How the [abuse detection](/docs/guides/abuse-detection/) of the platform treats the organization. `exempt` keeps all the services of the organization out of the sustained-CPU signal. `exemptProjects` keeps the services of the listed projects out of the signal, for legitimate heavy work. `action` replaces the action of the platform for the suspects of the organization. A report from an outside detector still applies. Only a platform administrator writes this field: no route of the dashboard changes it. |
| `spec.mail.limits` | object | none | As `spec.tenancy.mail.relay.limits` on the [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/) | The limits of the organization at the mail relay of the platform. Each field replaces the field of the platform: messages per minute, per hour, and per day, recipients per message, message size, and connections. A message limit of `0` stops the mail of the organization. Only a platform administrator writes this field. See [Mail relay](/docs/guides/mail-relay/#limits). |
| `spec.mail.allowedSenderDomains` | list of strings | none | At most 50 domains | The domains from which the organization can send through the relay, in place of the platform's list. An empty list replaces the platform's list with none. |
| `spec.region` | string | empty | `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, at most 40 characters | Where the projects that do not name a region of their own run. |
| `spec.requiredTags` | list of strings | empty | At most 8, each a DNS label | The node tags that every workload of every project of the organization must run on. Geass adds them to the tags of the project. |
| `spec.allowedDomains` | list of strings | empty | At most 50 domain names | The domains under which the services of the organization can take hostnames, and the hostname that Geass gives a service. Empty allows any hostname that nobody else holds. |
| `spec.registry.repository` | string | empty | none | Where the builds of the organization push. Together with `credentialRef`, this field replaces the registry of the platform for those builds. `credentialRef` is the Secret `org-registry-<name>`, which the dashboard makes. Under the platform's `spec.tenancy.registry`, the builds of the organization push under `<repository>/<project>/<service>`, and an organization without a repository does not build. The dashboard refuses a repository that overlaps the repository of the platform or of another organization, and that repository cannot be used. See [Registry isolation for organizations](/docs/operations/registry-isolation/). |
| `spec.idp` | object | none | none | An identity provider that belongs to the organization (`issuer`, `clientID`, `credentialRef`, `roleClaim`, `roleMap`, `requireClaim`, `scopes`, `displayName`, `enabled`). A person who signs in through this provider works in this organization, in all of it, with the role that the claim maps to, and nowhere else. A platform administrator sets up and tests the provider. A save turns the provider off. |

The settings of a project add to these settings. Geass uses the region and tags of a project as they are, and adds those of the organization. A change to the region, tags, or quota of the organization reaches the workloads of its projects. Geass places those workloads again, as for a change to the project.

## Who may see what

A person works in one organization at a time. In that organization, the person works either in all of the organization or in one workspace. An administrator of the organization can work in all of the organization and in any workspace. A person who is not an administrator works in the workspaces that they are in. A person who is in none of the workspaces of an organization, and who does not administer it, cannot sign in to it. See [Organizations and workspaces](/docs/guides/organizations-and-workspaces/#how-it-fits-together).

## Deleting

The dashboard refuses to delete an organization that still has projects, a database server, or a cloud connection. The dashboard also refuses to remove a workspace that has projects. These rules make sure that nothing is left that nobody can see. Move or delete these items first. To move a project, use `POST /api/projects/{project}/assign`.

Nothing checks a delete with `kubectl`. If you delete the resource with `kubectl`, its projects then belong to an organization that no longer exists. Only the administrators of the platform can see those projects, until the projects are assigned again.

The operator holds the resource with the finalizer `geass.geass.dev/organization` until the build namespace of the organization (`geass-build-<name>`) is gone. While the organization exists, the operator does these tasks:

- It holds that namespace to its noisy-neighbour limits.
- It keeps the network policy of the namespace, `geass-build-isolation`, with the outbound limits of the organization.
- It puts the limits and the network policy back when someone edits or deletes them by hand.

The dashboard also deletes the alert channels and the registry login of the organization.

## Annotations

Geass sets these annotations. Do not edit them.

| Annotation | Meaning |
| --- | --- |
| `geass.dev/auto-created` | A sign-in made the organization, not a platform administrator. While the organization is also suspended, it waits for approval. See [Sign in with an identity provider](/docs/guides/external-sign-in/#organizations-made-by-sign-in). |
| `geass.dev/auto-created-by` | The person who made the organization, as the provider knows them. |
| `geass.dev/idp-changed` | When the identity provider of the organization last changed. A session made through that provider is tied to the state that it was made for. |

## How the dashboard and API map to it

| What | Effect |
| --- | --- |
| `GET /api/orgs` | Lists the organizations, with the number of projects and workspaces of each. Only for a platform administrator. |
| `POST /api/orgs/create` (`displayName`, `idpID`, optional `clusterRef`) | Creates an organization. |
| `POST /api/orgs/{name}/update` (any of `displayName`, `idpID`, `suspended`, `clusterRef`) | Changes the fields that the request sends. |
| `POST /api/orgs/{name}/delete` (`confirmName`) | Deletes the organization, if it has no projects. |
| `POST /api/orgs/{name}/workspaces/create` (`displayName`, `idpID`) | Adds a workspace. |
| `POST /api/orgs/{name}/workspaces/{id}/delete` | Removes a workspace that has no projects. |
| `POST /api/projects/{project}/assign` (`org`, `workspace`) | Puts a project in an organization and a workspace. When `org` is empty, removes the project from its organization. The API refuses the request when the organization is at its project limit. |
| `POST /api/orgs/{name}/update` (also `maxProjects`, `quotaCPU`, `quotaMemory`, `quotaStorage`, `quotaPods`, `region`, `requiredTags`, `allowedDomains`) | Sets the policy of the organization. A field that the request sends empty removes its limit. |
| `POST /api/orgs/{name}/idp/save`, `/idp/enable`, `/idp/clear` | The identity provider of the organization: save it, turn it on after a test sign-in, or remove it. A save turns the provider off. Only for a platform administrator who is signed in to the dashboard. |
| `GET /api/alerts`, `POST /api/alerts/channels/save`, `/delete`, `/test` | The alert channels of an organization. |
| `GET /api/registry`, `POST /api/registry/save`, `/clear` | The registry of an organization. |

For the responses, see the [REST API](/docs/reference/rest-api/).

## Related

- [Organizations and workspaces](/docs/guides/organizations-and-workspaces/)
- [GeassProject](/docs/reference/resources/geass-project/)
- [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/)
