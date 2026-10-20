---
title: "GeassOrganization"
description: "Reference for GeassOrganization: one organization of the identity provider, set up in Geass, with its workspaces, so that its people see only its projects."
sidebar:
  order: 11
---

A `GeassOrganization` tells Geass that an organization of your identity provider is one of its own. A person whose sign-in token names that organization may sign in, and works in it: they see and change its projects and nothing of another organization's. The resource holds no members. Who is in the organization, and in which of its workspaces, is read from the token each time someone signs in.

Nothing looks at these resources until organizations are turned on (`spec.auth.tenancy.enabled` on the [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/)). The task-oriented view, with the token's shape and the steps to turn it on, is in [Organizations and workspaces](/docs/guides/organizations-and-workspaces/).

A platform administrator creates one with the REST API (`POST /api/orgs/create`) or with `kubectl`. An organization does not create itself, unless the platform lets the first sign-in of an organization's administrator make it (see [Organizations made by sign-in](/docs/guides/external-sign-in/#organizations-made-by-sign-in)).

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

The name is made by Geass (`org-` and eight hex digits) and does not change. Projects name it in `spec.org`.

## Spec fields

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.displayName` | string | none | Required. 1 to 100 characters | What the dashboard calls the organization. |
| `spec.idpID` | string | none | Required. 1 to 253 characters | The organization's identifier in the identity provider's token. A person whose token names it is a member. No two organizations may have the same one: the dashboard refuses a second. |
| `spec.workspaces` | list | empty | At most 100, unique `id` | The groups of projects inside the organization. |
| `spec.workspaces[].id` | string | none | Required. `^ws-[a-f0-9]{6}$` | What a project names in `spec.workspace`. Made by Geass, and does not change. |
| `spec.workspaces[].displayName` | string | none | Required. 1 to 100 characters | What the dashboard calls the workspace. |
| `spec.workspaces[].idpID` | string | none | Required. 1 to 253 characters | The workspace's (team's) identifier in the token. A person who is in it works in it. |
| `spec.clusterRef.name` | string | empty | An existing [GeassCluster](/docs/reference/resources/geass-cluster/) | The cluster this organization's new projects are made in. Empty means the platform's default. A project that exists stays where it is. Geass runs on one cluster today, so this changes nothing yet. |
| `spec.suspended` | boolean | `false` | none | Keeps the organization's people from signing in, and stops their open sessions at once, while its projects keep running. |
| `spec.maxProjects` | integer | none | 0 or more | The most projects the organization may have. Checked when one is made or given to it. 0 keeps it from making any. |
| `spec.quota.cpu`, `.memory`, `.storage`, `.pods` | quantity, quantity, quantity, integer | none | `pods` at least 1 | The most all its projects may ask for together. Each environment's own quota is held to what the others of the organization have left. See [Organizations and workspaces](/docs/guides/organizations-and-workspaces/#what-the-platform-sets-for-an-organization). |
| `spec.region` | string | empty | `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, at most 40 characters | Where projects run that name no region of their own. |
| `spec.requiredTags` | list of strings | empty | At most 8, each a DNS label | Node tags every workload of every project of the organization must run on, added to the project's own. |
| `spec.allowedDomains` | list of strings | empty | At most 50 domain names | The domains the organization's services may take hostnames under, and the hostname Geass gives a service. Empty allows any hostname nobody else holds. |
| `spec.registry.repository` | string | empty | none | Where the organization's builds push. Together with `credentialRef` (the Secret `org-registry-<name>`, made by the dashboard) it replaces the platform's registry for them. |
| `spec.idp` | object | none | none | An identity provider of the organization's own (`issuer`, `clientID`, `credentialRef`, `roleClaim`, `roleMap`, `requireClaim`, `scopes`, `displayName`, `enabled`). A person who signs in through it works in this organization, in all of it, as the role its claim maps to, and nowhere else. Set up and tested by a platform administrator; saving it turns it off. |

A project's own settings add to these: a project's region and tags are used as they are, and the organization's are added. A change to the organization's region, tags or quota reaches its projects' workloads, which are placed again as for a change to the project.

## Who may see what

A person works in one organization at a time, and in it either in all of it or in one workspace. An organization's administrator may work in all of it and in any workspace. Someone who is not works in the workspaces they are in. A person who is in none of an organization's workspaces and does not administer it cannot sign in to it. See [Organizations and workspaces](/docs/guides/organizations-and-workspaces/#how-it-fits-together).

## Deleting

The dashboard refuses to delete an organization that still has projects, a database server or a cloud connection, or to remove a workspace that has projects, so that nothing is left that nobody can see. Move or delete them first (`POST /api/projects/{project}/assign`). Deleting the resource with `kubectl` is not checked: its projects then belong to an organization that no longer exists, which only the platform's administrators can see, until they are assigned again.

The operator holds the resource with the finalizer `geass.geass.dev/organization` until the namespace the organization's builds ran in (`geass-build-<name>`) is gone. The dashboard also deletes the organization's alert channels and its registry login.

## Annotations

Geass sets these. They are not for editing.

| Annotation | Meaning |
| --- | --- |
| `geass.dev/auto-created` | A sign-in made the organization, and not a platform administrator. While it is also suspended it is waiting for approval (see [Sign in with an identity provider](/docs/guides/external-sign-in/#organizations-made-by-sign-in)). |
| `geass.dev/auto-created-by` | Who made it, as the provider knows them. |
| `geass.dev/idp-changed` | When the organization's own identity provider was last changed. A session made through it is tied to the state it was made for. |

## How the dashboard and API map to it

| What | Effect |
| --- | --- |
| `GET /api/orgs` | Lists the organizations, with how many projects and workspaces each has. A platform administrator only. |
| `POST /api/orgs/create` (`displayName`, `idpID`, optional `clusterRef`) | Creates one. |
| `POST /api/orgs/{name}/update` (any of `displayName`, `idpID`, `suspended`, `clusterRef`) | Changes what is sent. |
| `POST /api/orgs/{name}/delete` (`confirmName`) | Deletes it, when it has no projects. |
| `POST /api/orgs/{name}/workspaces/create` (`displayName`, `idpID`) | Adds a workspace. |
| `POST /api/orgs/{name}/workspaces/{id}/delete` | Removes one that has no projects. |
| `POST /api/projects/{project}/assign` (`org`, `workspace`) | Puts a project in an organization and workspace, or takes it out when `org` is empty. Refused when the organization is at its project limit. |
| `POST /api/orgs/{name}/update` (also `maxProjects`, `quotaCPU`, `quotaMemory`, `quotaStorage`, `quotaPods`, `region`, `requiredTags`, `allowedDomains`) | Sets the organization's policy. A field sent empty takes its limit away. |
| `POST /api/orgs/{name}/idp/save`, `/idp/enable`, `/idp/clear` | The organization's own identity provider: save it (turns it off), turn it on after a test sign-in, or remove it. A platform administrator signed in to the dashboard only. |
| `GET /api/alerts`, `POST /api/alerts/channels/save`, `/delete`, `/test` | An organization's own alert channels. |
| `GET /api/registry`, `POST /api/registry/save`, `/clear` | An organization's own registry. |

See the [REST API](/docs/reference/rest-api/) for the answers.

## Related

- [Organizations and workspaces](/docs/guides/organizations-and-workspaces/)
- [GeassProject](/docs/reference/resources/geass-project/)
- [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/)
