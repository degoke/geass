---
title: "Organizations and workspaces"
description: "Run several organizations on one cluster and keep their projects apart: how Geass reads them from your identity provider, and what it does not isolate."
---

By default every person who can sign in to the dashboard sees every project: there is one workspace, and one set of roles. Turn on organizations when one Geass serves several groups of people who should not see each other's work: teams of one company, or customers you know.

Your identity provider already knows who is in which organization and team. Geass reads that from the sign-in token and keeps each organization's projects, services, databases, tokens, logs and numbers to itself. Nothing is copied from the provider: it still decides who exists and who belongs where.

:::caution
Organizations are for groups you trust to share a cluster. The projects of one are hidden from the others, the network between them is closed, and an organization's builds run in a namespace of their own, but they all run on the same machines and a few things are shared (see [What it does not isolate](#what-it-does-not-isolate)). If the organizations are strangers who might attack each other, give each its own cluster.
:::

## How it fits together

```text
Platform                      the people who run this Geass
└─ Organization               one group of people, set up by a platform administrator
   └─ Workspace               a team inside it (optional)
      └─ Project              what a project always was: environments, services, databases
```

- A **project** belongs to one organization and, if you use workspaces, to one workspace of it.
- A person **works in one place at a time**: an organization, and either all of it or one workspace. The dashboard has a switcher. What they see and change is limited to that place.
- The **role** in that place is `admin` or `viewer`, as everywhere in Geass. An administrator of an organization can work in all of it and in any of its workspaces. Someone who is not works in the workspaces they are in, with the role the provider gives them in the organization, or a higher one if it gives them one in the workspace.
- **Platform administrators** run the installation: nodes, add-ons, the domain, the registry, the identity provider, alerts and the organizations themselves. They are not an organization's administrators, and an organization's administrators cannot reach any of that.

| | Platform administrator | Administrator of an organization | A member of a workspace |
| --- | --- | --- | --- |
| Sees | Everything | All of the organization, or one workspace | Their workspace's projects |
| Changes | Everything, including nodes, add-ons, domain, registry, sign-in, organizations | The projects, services, databases, buckets, variables, tokens and settings of the organization | What their role allows, in their workspace |
| Makes API tokens for | The whole installation, or an organization | Their organization | Cannot make tokens (a viewer) |

## Before you start

Organizations need [sign-in through an identity provider](/docs/guides/external-sign-in/), set up and turned on, because the provider is what says who is in which organization. When you set the provider up for this, fill in **Organizations claim** there (step 4 below says what it is) and leave **Role claim** empty: the role then comes from each organization, and there is no role claim to name. They do not work with passwords from the Secret, except for the admins who keep a password as the way back in: those are **platform administrators**.

Two things change when you turn organizations on, and you should plan for both:

- **Projects that already exist belong to no organization.** Until you assign them, only platform administrators see them. Assign each one first (see below), or nobody but you will.
- **Everyone signs in again once.** The sessions of people from the provider end, because the new ones carry where each person works.

## Set it up

### 1. Make the token say who is in which organization

Geass reads one claim of the ID token. It is a list with one entry for each organization the person is in:

```json
{
  "orgs": [
    {
      "id": "org_acme",
      "role": "owner",
      "workspaces": [
        { "id": "team_payments" },
        { "id": "team_web", "role": "admin" }
      ]
    },
    { "id": "org_beta", "role": "member", "workspaces": ["team_x"] }
  ]
}
```

| Field | What it is |
| --- | --- |
| `id` | The provider's id of the organization, which you later give to Geass |
| `role` | The person's role in it. It is turned into `admin` or `viewer` with the same role map as for sign-in (`owner=admin`, `member=viewer`). A role that maps to nothing leaves the organization out. It may be a string, a list, or a comma-separated string |
| `workspaces` | Optional. The teams they are in, each a bare id or an object with an `id` and, optionally, a `role` in that team |

The claim can have any name and sit inside nested claims (`https://example.com/claims.organizations`). A token may list up to 50 organizations and 200 workspaces for each.

To make someone a **platform administrator**, name a second claim and the values that mean it, for example `geass_platform` with the value `operator`. The value is compared whole.

:::note[Better Auth]
Better Auth's organization plugin keeps members and teams in its own tables, and puts into the ID token only what your `customIdTokenClaims` function returns, so build the list there. This is a sketch: check the model and field names against your version.

```ts
customIdTokenClaims: async ({ user, scopes }) => ({
  email: user.email,
  email_verified: user.emailVerified,
  orgs: await orgsOf(user.id),
}),
```

`orgsOf` reads the person's rows of the `member` model (their `organizationId` and `role`) and of the `teamMember` model, and returns `[{ id, role, workspaces: [{ id }] }]`. Look them up through `auth.$context.adapter`, as in the [single organization recipe](/docs/guides/external-sign-in/#better-auth).
:::

### 2. Set up the organizations in Geass

Only organizations you set up here can be signed in to, so a provider that lets anyone create an organization cannot give anyone a place in your Geass. A platform administrator creates each one and gives it the id the provider uses:

```bash
curl -X POST "$GEASS/api/orgs/create" -H "Authorization: Bearer $TOKEN" \
  -d displayName=Acme -d idpID=org_acme
# {"name":"org-1a2b3c4d","ok":true}

curl -X POST "$GEASS/api/orgs/org-1a2b3c4d/workspaces/create" -H "Authorization: Bearer $TOKEN" \
  -d displayName="Payments team" -d idpID=team_payments
```

`$TOKEN` is an install-wide API token made by a platform administrator. Or apply the resource yourself: see [GeassOrganization](/docs/reference/resources/geass-organization/). Each organization and workspace gets a Geass name of its own (`org-…`, `ws-…`) that never changes. The provider's ids can.

### 3. Give projects to organizations

```bash
curl -X POST "$GEASS/api/projects/proj-5c2c6d05/assign" -H "Authorization: Bearer $TOKEN" \
  -d org=org-1a2b3c4d -d workspace=ws-a1b2c3
```

Leave out `org` to take a project back: it is then the platform's again. A project made by an organization's own people belongs to that organization (and to the workspace they were in) from the start.

### 4. Tell Geass how the token lists them, and test it

Under **Settings** → **Sign-in**, or:

```bash
curl -X POST "$GEASS/api/settings/auth/tenancy" -b "$SESSION" \
  -d orgsClaim=orgs -d platformClaim=geass_platform -d platformValues=operator
```

This is a signed-in admin's to do, not a token's, like every other setting that decides who can sign in. Saving does not turn anything on. Run **Test sign-in**: the page now lists the organizations the token named and which of them are set up, so you can see the claim is read as you meant.

### 5. Turn it on

```bash
curl -X POST "$GEASS/api/settings/auth/tenancy" -b "$SESSION" -d enabled=on
```

It needs all of these, so that a mistake cannot lock everyone out:

- sign-in through the provider is on;
- a test sign-in of exactly these settings, within the last half hour, as someone who administers an organization that is set up, or is a platform administrator;
- an admin who signs in with a password;
- at least one organization (or a platform claim).

Turning it off is always allowed. The claims cannot be changed while it is on.

## Working in more than one place

A person who is in several organizations, or an organization with workspaces, picks where they work in the switcher, or with `POST /api/session/scope` (`org`, `workspace`). The choice is checked against what was granted when they signed in. Changing where you work does not lengthen the session, and a sign-out at the provider ends it as it ends any other.

A platform administrator starts as the platform. They may go into any organization to see what it sees, as its administrator, and back.

## What happens when something changes

| What changes | When it takes effect |
| --- | --- |
| An organization is **suspended** (`spec.suspended`) or deleted, or a workspace is removed | At once, for sessions already open. Nobody from it can sign in |
| Someone is removed from an organization at the provider | When their session ends (an hour by default, `sessionMinutes`), at once if the provider sends a back-channel logout, or within the interval of [checking people again](/docs/guides/external-sign-in/#check-people-again-while-they-are-signed-in) if that is on |
| A project is assigned to another organization | At once. Tokens made for the project keep working for whoever owns it now |
| Their role changes at the provider | At their next sign-in, or within the interval of checking people again, which ends their session so that they sign in to what is now true |
| An organization's quota, region, required tags or allowed domains change | Its workloads follow as for a change to a project; a hostname that is already in use is not re-checked until the service changes |
| An organization's own provider is changed, turned off or removed | At once, for the sessions made through it |

## Tokens

An API token is made for where its maker works. A token that an organization's administrator makes acts for that organization (and that workspace) and nothing else: it cannot read another organization's projects, and it cannot reach the platform's pages. An organization sees and revokes its own tokens, and its projects' tokens and deploy hooks, and never one of another's.

A token is scoped by what it is, not by anything the caller sends: there is no organization header and no `--org` flag, and a token cannot be pointed at another organization by a request. By default a token acts where its maker works. A person who works in all of an organization may make one for a single workspace of it (`workspace=ws-…`), and a platform administrator may make one for any organization that is set up and open (`org=org-…`, optionally `workspace=ws-…`), which is how a key for a customer's automation is made. Nobody gets a wider scope than their own. To use the command line in another organization, sign in again from there: `geass login` approves a token for the place the dashboard session is in.

Tokens that were made before organizations existed, and those a platform administrator makes without naming an organization, are **install-wide**: they act for the platform. A project's token (`gsp_`) acts for that one project. If it belongs to no organization, it reaches nothing until the project is assigned.

An installation holds at most 600 tokens, and an organization at most 100.

## What is kept apart

- **What people see and change.** Every read and write of the dashboard, the API, the command line and the MCP server goes through one check against the person's organization. Another organization's project, service, database or bucket is answered as one that does not exist, the same answer as for a name that was never used.
- **Lists and numbers.** The overview, usage, logs, metrics, terminals and live updates carry the organization's own projects, and not the platform's machines or other organizations' names.
- **Tokens, deploy hooks and sessions** (above).
- **The network.** Each project is closed to the others (see [Network isolation](/docs/guides/network-isolation/)), and an organization cannot name another's project when it chooses who may reach its own.
- **Hostnames.** A service cannot take a hostname another service serves, or the dashboard's, and Geass does not overwrite a DNS record it did not create. An organization can also be limited to some domains (see [Policy](#what-the-platform-sets-for-an-organization)).
- **GitHub.** An installation of the GitHub App that one organization's project uses cannot be connected to another's, and a person has to show that GitHub lists the installation among the ones their own account can use (see [GitHub](#github)).
- **Builds.** A project of an organization builds in a namespace of the organization's own, with only the credentials of that organization and that project in it (see [Builds and the registry](#builds-and-the-registry)).
- **Alerts.** An organization has channels of its own, which hear only about its projects (see [Alerts](#alerts)).

## What the platform sets for an organization

A platform administrator can set these on an organization, in **Settings** → **Organizations**, with the API (`POST /api/orgs/{name}/update`) or on the [GeassOrganization](/docs/reference/resources/geass-organization/). They are all optional, and one that is left out is not limited.

| Setting | What it does |
| --- | --- |
| **Most projects** (`maxProjects`) | The organization may have no more. It is checked when a project is made or given to it, and lowering it never removes what is there. 0 keeps it from making any. |
| **Quota** (`quota`) | The most CPU, memory, storage and pods all its projects can ask for together. Each environment is held to what the others of the organization have left, through a quota in its namespace, and to its project's own quota if that is lower. Two environments starting at the same instant can briefly pass it, and nothing that is running is stopped when it is passed. |
| **Region** (`region`) | Where its projects run when they name no region of their own. |
| **Required node tags** (`requiredTags`) | Every workload of every project of the organization runs only on nodes carrying all of them, added to the project's own. With nodes tagged and reserved for the tag, this is how an organization gets machines of its own. The tags have to exist on nodes, and be reserved, as a project's must. |
| **Allowed domains** (`allowedDomains`) | Its services may take hostnames under these domains only, and the hostname Geass gives a service. Two organizations cannot be given overlapping domains. |

## Alerts

An organization has alert channels of its own, under **Settings** → **Alerts**: Slack channels that hear only about the organization's projects (what is firing, and failed builds, deploys, backups, crash loops and storage), never about the cluster or another organization. An organization's administrator makes and sees them; a member of one workspace manages the channels that name projects of that workspace. A channel posts to `hooks.slack.com`: mail goes out through the platform's mail server and is not for an organization to send through. Each organization may have 10, and the platform's own keep their 20.

Deleting an organization deletes its channels and their Slack addresses.

## Builds and the registry

Builds run what a repository's Dockerfile says, as root, with the login that pushes the image mounted. A project that belongs to an organization builds in a namespace of the organization's own (`geass-build-<organization>`), not in the platform's. Only what the build needs is copied there for it: the registry login and the git token of its project, and the copies are removed when the build ends. The namespace allows no connection to the build and lets it reach the internet and the cluster's DNS, but no private address: not another project's pods, the platform's services, the cluster's API or the cloud's metadata. It holds at most 20 build pods, and it goes when the organization does.

So a build of an organization cannot reach the platform's secrets. It does still hold a registry login, which is the platform's unless the organization has its own:

- **Set the organization's registry** under **Settings** → **Registry** (an administrator of the whole organization), or with `POST /api/registry/save` (`repository`, `username`, `password`). Its builds then push there, with that login, and the platform's login is never in the organization's namespace. A service that names a registry of its own keeps it.
- Because the build namespace reaches only public addresses, the registry has to be one that is reachable on the internet.

## Shared things an organization owns

- **Database servers.** A platform administrator can make a shared Postgres or MySQL server for an organization (`POST /api/shared-databases/create` with `org=`). Only that organization's projects take databases on it, and only its people see it. It runs where the organization requires its work to (its region and tags). Its administrators can delete it when no database is on it. A platform's own server, which every project shared before, is still not available to organizations.
- **Cloud connections.** A connection made without a project, for an organization (`org=`, or by an administrator of the whole organization for their own), can be used by every project of that organization and by no other. Any project of the organization can use its credentials, so keep an organization's connections to what its projects may all touch.
- **Object storage.** An organization's buckets in the cluster are made on the platform's object storage server, which each bucket reaches with a login of its own. The server is the platform's to set up.

An organization cannot be deleted while it owns a database server or a connection, and a project cannot be given to another organization while it has databases on a server that belongs to one.

## An organization's own identity provider

An organization that would rather its people sign in through its own provider (its Okta, its Entra ID) can be given one. A platform administrator sets it up under **Settings** → **Organizations** → the organization → **Sign-in provider of its own**: the issuer, the client, a role claim and what each of its values maps to (`admin` or `viewer`), and optionally claims a person must have. Tell the provider to send people back to the callback address the page shows (the same one as for the platform's provider). Then **Test sign-in**, as someone who maps to admin, and **Turn on**.

The organization's people sign in from the link the page shows (`/auth/oidc/login?org=<name>`). They work in that organization, in all of it, as the role their claim maps to: an administrator may change it, a viewer reads all of it. The organization's provider is asked nothing else. It cannot make anyone an administrator of the platform, put them in another organization, or pick a workspace.

- Saving the provider turns it off and needs a new test; turning it off or on, removing it, or suspending the organization ends every session made through it at once.
- It needs organizations on, and with [checking people again](/docs/guides/external-sign-in/#check-people-again-while-they-are-signed-in) on it is checked like the platform's. A back-channel logout from an organization's provider is not read: its sessions end with their session length or the check.
- The client secret is kept in a Secret (`org-idp-<organization>`) and never comes back.

## GitHub

One GitHub App serves every organization. With organizations on, a person who connects a project to an installation has to show that GitHub lists that installation among the ones their own account can use: the App asks the person to authorize it when they install it, and Geass trades the code GitHub sends back for their token and asks GitHub. An installation id that is only written into an address is not enough. In the App's settings on GitHub, turn on **Request user authorization (OAuth) during installation** (an App made by Geass has it on already); without it an organization's person is refused with a message that says so. A platform administrator is not asked, since they may connect any installation as it is. An installation that one organization's project uses cannot also be connected to another's, whoever is asking.

## What it does not isolate

These are shared, and fine for organizations that trust each other:

- **Machines.** Every organization's pods run on the same nodes, unless you reserve nodes with [node tags](/docs/guides/scaling-and-placement/) and give each organization its own tags (see above). A pod that escapes its container reaches the node.
- **The network of shared servers.** A shared database server runs in the platform's namespace, and any pod can open a connection to it. Each logical database has its own login that reaches that database and nothing else, and the administrator's login is in a Secret no organization reads.
- **The operator and the dashboard.** They hold cluster-wide access; the separation is made by their code and by the network policy.
- **The GitHub App.** There is one for the installation, and anyone who can install it on an account they own can connect that installation.
- **The platform's domain, Cloudflare zone and sign-in.** They are the platform's. Organizations do not see their settings.

Not built: sending an organization's projects to another cluster. Each organization names the cluster its new projects are made in (`clusterRef`), but Geass runs on one cluster today, so that changes nothing yet.

## If something goes wrong

- **"Your account is not in an organization set up here."** The sign-in code is `no_org`. The token named no organization that is set up (check the claim and the organizations' `idpID`), or the person is in the organization but in none of its workspaces and does not administer it.
- **Everyone sees nothing after turning it on.** Projects that belonged to nobody are visible to platform administrators only. Assign them, or sign in as a platform administrator.
- **You are locked out.** Sign in with an admin's password (it is a platform administrator), open **Settings** → **Sign-in** and turn organizations off, or without the dashboard:

  ```bash title="Turn organizations off"
  sudo k3s kubectl -n geass-system patch geassplatformconfig platform --type merge -p '{"spec":{"auth":{"tenancy":{"enabled":false}}}}'
  ```

## Related

- [Sign in with an identity provider](/docs/guides/external-sign-in/)
- [Users and API tokens](/docs/guides/users-and-tokens/)
- [Network isolation](/docs/guides/network-isolation/)
- [GeassOrganization](/docs/reference/resources/geass-organization/)
- [Security](/docs/operations/security/)
