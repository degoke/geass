---
title: "Organizations and workspaces"
description: "Run many organizations on one cluster and keep their projects apart. Geass reads them from your identity provider. See what it does not isolate."
---

By default, every person who can sign in to the dashboard sees every project. Turn on organizations when one Geass
serves several groups of people who must not see the work of each other. For example, these groups can be teams of
one company, or customers that you know.

Your identity provider already knows who is in which organization and team. Geass reads that from the sign-in token.
Geass keeps the projects, services, databases, tokens, logs, and numbers of each organization to that organization.
The provider still decides who exists and who belongs where.

:::caution
If the organizations are strangers who might attack each other, give each organization its own cluster.
Organizations are for groups that you trust to share a cluster. All organizations run on the same servers. See
[What it does not isolate](#what-it-does-not-isolate).
:::

## How it fits together

```text
Platform                      the people who run this Geass
└─ Organization               one group of people, set up by a platform administrator
   └─ Workspace               a team inside it (optional)
      └─ Project              environments, services, databases
```

- A **project** belongs to one organization, and optionally to one workspace of that organization.
- A person **works in one place at a time**: all of an organization, or one workspace. The top bar of the dashboard
  shows where you work, and lets you change it. What a person sees and changes is limited to that place.
- The **role** in that place is `admin` or `viewer`. An administrator of an organization can work in all of it and in
  each of its workspaces. Another person works in the workspaces that they are in, with the role that the provider
  gives them.
- **Platform administrators** run the installation: servers, the domain, the registry, sign-in, alerts, and the
  organizations. The administrators of an organization cannot reach these settings.

| | Platform administrator | Administrator of an organization | Member of a workspace |
| --- | --- | --- | --- |
| Sees | All | All of the organization, or one workspace | The projects of their workspace |
| Changes | All, including servers, domain, registry, sign-in, and organizations | The projects, services, databases, buckets, variables, tokens, and settings of the organization | What their role allows, in their workspace |
| Makes API tokens for | The whole installation, or an organization | Their organization | Their workspace, as an `admin` there. A `viewer` cannot make tokens |

## Before you start

Organizations need [sign-in through an identity provider](/docs/guides/external-sign-in/), set up and turned on. For
organizations, leave **Role claim** empty in the provider settings. The role then comes from each organization.

Organizations do not work with passwords, except for the admins who keep a password as the way back in. Those admins
are **platform administrators**.

Plan for two changes when you turn on organizations:

- **Projects that exist belong to no organization.** Only platform administrators see them until you assign them.
- **Everyone signs in again one time.** The sessions of people from the provider end.

## Set it up

### 1. Make the token list the organizations

Geass reads one claim of the ID token. The claim is a list with one entry for each organization of the person:

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
| `id` | The id of the organization at the provider. You give this id to Geass in step 2. |
| `role` | The role of the person in the organization. Geass maps it to `admin` or `viewer` with the **Roles** list of the sign-in settings, for example `owner=admin` and `member=viewer`. If the role maps to nothing, Geass leaves out the organization. |
| `workspaces` | Optional. The teams of the person. Each team is an id, or an object with an `id` and an optional `role` in that team. |

The claim can have any name, and can be nested, for example `https://example.com/claims.organizations`. A token can
list up to 50 organizations, and up to 200 workspaces for each organization.

To make a person a **platform administrator**, name a second claim and the values that mean platform administrator.
For example, use the claim `geass_platform` with the value `operator`.

:::note[Better Auth]
The organization plugin of Better Auth keeps members and teams in its own tables. Build the list in your
`customIdTokenClaims` function. This code is a sketch:

```ts
customIdTokenClaims: async ({ user, scopes }) => ({
  email: user.email,
  email_verified: user.emailVerified,
  orgs: await orgsOf(user.id),
}),
```

`orgsOf` reads the rows of the person in the `member` and `teamMember` models, and returns
`[{ id, role, workspaces: [{ id }] }]`. Find these rows through `auth.$context.adapter`, as in the
[single organization recipe](/docs/guides/external-sign-in/#better-auth).
:::

### 2. Create the organizations in Geass

People can sign in only to organizations that a platform administrator created. So a provider that lets anyone create
an organization cannot give anyone a place in your Geass.

1. Open **Settings** → **Organizations**.
2. Click **New organization**.
3. Enter a **Name**, and the **Id at the identity provider**, for example `org_acme`.
4. Click **Create organization**.

To add a workspace, click **Edit** on the organization. Under **Workspaces**, enter the **New workspace** name and the
**Id at the provider**, for example `team_payments`. Then click **Add**.

Each organization and workspace also gets a Geass name (`org-…` or `ws-…`) that never changes. The ids of the provider
can change.

### 3. Give projects to organizations

After organizations are on, a platform administrator assigns a project in the dashboard:

1. Open the project, and then **Settings** → **General**.
2. Under **Visibility**, select the **Organization**, and optionally the **Workspace**.
3. Click **Assign**.

To give a project back to the platform, select **None: the platform's**. A project that the people of an organization
create belongs to their organization and workspace from the start.

Before organizations are on, the dashboard does not show **Visibility**. To assign projects before you turn
organizations on, use the API with a **Full access** token:

```bash
curl -X POST "$GEASS/api/projects/proj-5c2c6d05/assign" -H "Authorization: Bearer $TOKEN" \
  -d org=org-1a2b3c4d -d workspace=ws-a1b2c3
```

### 4. Tell Geass how the token lists them, and test it

1. Open **Settings** → **Sign-in**.
2. Under **Organizations**, enter the **Organizations claim**, for example `orgs`.
3. Optional: enter the **Platform administrator claim** and its **Values**.
4. Click **Save how the token names them**. Nothing turns on yet.
5. Click **Test sign-in**. The result lists the organizations that the token named, and which of them exist in Geass.

A signed-in admin must do this step, not a token.

### 5. Turn it on

Click **Turn organizations on**. To prevent a lockout, the button works only when all of these conditions are true:

- Sign-in through the provider is on.
- A test sign-in with these settings passed in the last half hour, as the administrator of an organization that
  exists, or as a platform administrator.
- An admin signs in with a password.
- At least one organization exists, or a platform administrator claim is set.

You can always turn organizations off with **Turn organizations off**. While organizations are on, you cannot change
the claims.

## Work in more than one place

A person in several organizations, or in an organization with workspaces, selects where they work in the top bar. A
change of place does not make the session longer.

A platform administrator starts as the platform. A platform administrator can go into any organization to see what
it sees, as its administrator, and then go back.

## What happens when something changes

| What changes | When it takes effect |
| --- | --- |
| An organization is suspended or deleted, or a workspace is removed | Immediately, for open sessions. Nobody from it can sign in |
| A person is removed from an organization at the provider | When their session ends, an hour by default. Immediately, with a back-channel logout. Within the interval of [checking people again](/docs/guides/external-sign-in/#check-people-again-while-they-are-signed-in), if that is on |
| A project is assigned to another organization | Immediately. Tokens of the project continue to work for the new owner |
| The role of a person changes at the provider | At their next sign-in, or at the next check |
| The limits or policy of an organization change | Its services follow, as for a change to a project. Geass does not check a hostname in use again until the service changes |
| The own provider of an organization changes, or is turned off or removed | Immediately, for the sessions made through it |

To suspend an organization, click **Edit** on it, and turn on **Suspended**.

## Tokens

A token acts for the place where its maker works. A token that the administrator of an organization makes acts for
that organization, and for nothing else. An organization sees and revokes only its own tokens and deploy hooks.

The scope of a token comes from the token itself. A request cannot point a token at another organization. These rules
apply:

- By default, a token acts where its maker works.
- A person who works in all of an organization can make a token for one of its workspaces.
- A platform administrator can make a token for any organization that exists and is not suspended. Use this to make a
  key for the automation of a customer.
- Nobody can make a token with a wider scope than their own.

To use the command line in another organization, change where you work in the dashboard, and run `geass login` again.

These tokens act for the whole installation:

- Tokens made before organizations were on.
- Tokens that a platform administrator makes without an organization.

A project token acts for its project. If the project belongs to no organization, the token reaches nothing until the
project is assigned.

An installation can have at most 600 tokens, and an organization at most 100.

## What is kept apart

- **What people see and change.** Every read and change, in the dashboard, the API, the command line, and the MCP
  server, is checked against the organization of the person. A project, service, database, or bucket of another
  organization looks as if it does not exist.
- **Lists and numbers.** The overview, usage, logs, metrics, terminals, and live updates show only the projects of the
  organization.
- **Tokens, deploy hooks, and sessions.** See [Tokens](#tokens).
- **The network.** Each project is closed to the others. See [Network isolation](/docs/guides/network-isolation/).
- **Hostnames.** A service cannot take the hostname of another service or of the dashboard. You can also limit an
  organization to some domains.
- **GitHub.** See [GitHub](#github).
- **Builds.** See [Builds and the registry](#builds-and-the-registry).
- **Untrusted code, when you ask for it.** The platform can run the services, schedules, builds, and some database
  servers of organizations in a sandboxed runtime. PostgreSQL and MariaDB servers of organizations cannot run in it.
  See [Sandboxed runtime for organizations](/docs/operations/sandboxed-runtime/).
- **Alerts.** See [Alerts](#alerts).

## What the platform sets for an organization

A platform administrator sets limits and policy on an organization:

1. Open **Settings** → **Organizations**.
2. Click **Edit** on the organization.
3. Under **Limits and policy**, enter the values. Empty means no limit.
4. Click **Save policy**.

| Setting | What it does |
| --- | --- |
| **Most projects** | The most projects that the organization can have. When you lower it, Geass removes no project. 0 allows none. |
| **CPU in total**, **Memory in total**, **Storage in total**, **Pods in total** | The most that all projects of the organization can reserve together. Geass stops nothing that runs when the total is passed. |
| **Region** | Where its projects run when they name no region. |
| **Required node tags** | Every service of the organization runs only on servers with all of these tags. With tagged and reserved servers, an organization gets servers of its own. |
| **Allowed domains** | Its services can use hostnames only under these domains, and the hostname that Geass gives a service. Two organizations cannot have domains that overlap. |

Geass has no dashboard or command-line setting for three more policies yet. See [Under the hood](#under-the-hood).

- **Noisy-neighbour limits**: what each environment can run, its temporary disk, and its bandwidth. See
  [Noisy-neighbour limits](/docs/guides/tenant-limits/).
- **Outbound limits**: the ports that its services can reach outside the cluster. See
  [Outbound limits](/docs/guides/outbound-limits/).
- **Abuse detection**: what Geass does with suspect services of the organization. See
  [Abuse detection](/docs/guides/abuse-detection/).

## Billing what each organization reserves

The platform can record what the projects of each organization reserve, hour by hour: CPU, memory, and storage.
Only platform administrators can read these records, with `GET /api/v1/usage/hours`. See
[Usage by organization, for billing](/docs/guides/usage-and-capacity/#usage-by-organization-for-billing).

## Alerts

An organization has its own alert channels, under **Settings** → **Alerts**. These Slack channels get alerts only
about the projects of the organization. They never get alerts about the cluster or another organization.

- An administrator of an organization makes and sees its channels.
- A member of one workspace manages the channels that name projects of that workspace.
- A channel posts to `hooks.slack.com`. An organization cannot send mail through the mail server of the platform.
- Each organization can have 10 channels. The platform keeps its own 20.

When you delete an organization, Geass deletes its channels.

## Builds and the registry

A build runs what the Dockerfile of a repository says, as root, with the registry login that pushes the image. A
project of an organization builds in a place that belongs to the organization. That place has only the registry
login and the GitHub token of the project. It can reach the internet, but not other projects, the platform, or the
cloud metadata. It runs at most 20 builds at the same time.

The registry login is the login of the platform, unless the organization has its own:

- **Set the registry of the organization** under **Settings** → **Registry**, as an administrator of the whole
  organization. Enter the **Repository**, **Username**, and **Password or token**, and click **Save**. The registry
  must be reachable from the internet. The builds of the organization then push there, and the login of the platform
  never reaches them.
- **On a cluster shared by organizations that do not trust each other**, a platform administrator turns on
  [registry isolation](/docs/operations/registry-isolation/). Then an organization without its own registry does not
  build, and no service can run an image from the registry of the platform or of another organization.

## Shared things an organization owns

- **Database servers.** A platform administrator can make a shared Postgres or MySQL server for an organization. Only
  the projects of that organization put databases on it. See
  [Shared database servers and organizations](/docs/guides/shared-database-servers/#organizations).
- **Cloud connections.** An AWS or PlanetScale connection can belong to an organization instead of a project. In the
  connection form, select **Or an organization**. Every project of the organization can use its credentials. So give
  it only what all of its projects can touch.
- **Object storage.** Geass makes the buckets of an organization on the object storage server of the platform. Each
  bucket has its own login.

You cannot delete an organization while it owns a database server or a connection. You cannot move a project to
another organization while the project has databases on a server of an organization.

## An organization's own identity provider

An organization can have its people sign in through its own provider, for example its Okta or Entra ID. A platform
administrator sets it up:

1. Open **Settings** → **Organizations**, and click **Edit** on the organization.
2. Under **Sign-in provider of its own**, click **Set up a provider**.
3. Enter the **Name**, **Issuer**, **Client ID**, **Client secret**, **Role claim**, and **Role mapping**. Optionally,
   enter **Only people with**.
4. Click **Save**.
5. At the provider, register the **Redirect URI** that the page shows.
6. Click **Test sign-in**, and sign in as a person who maps to admin.
7. Click **Turn on**.

The people of the organization sign in from the **Sign-in link** that the page shows. They work in all of that
organization, with the role that their claim maps to. That provider cannot make anyone a platform administrator, put a
person in another organization, or select a workspace.

- When you save the provider, Geass turns it off, and it needs a new test.
- Turning it off or on, removing it, or suspending the organization ends every session made through it.
- Geass does not accept a back-channel logout from the provider of an organization. Its sessions end with their
  length, or at the next [check](/docs/guides/external-sign-in/#check-people-again-while-they-are-signed-in).

## GitHub

One GitHub App serves every organization. When a person of an organization links a project to an installation,
GitHub must list that installation among the installations that the account of the person can use. An installation
that one organization uses cannot be linked to a project of another organization.

For this check, the GitHub App must have **Request user authorization (OAuth) during installation** turned on. An app
that Geass made has it already. Without it, Geass refuses a person of an organization with a message that says so.
See [Several organizations on one app](/docs/guides/connectors/github/#several-organizations-on-one-app).

## What it does not isolate

Organizations share these things. This is acceptable for organizations that trust each other:

- **Servers.** The services of every organization run on the same servers. To change this, give each organization its
  own **Required node tags**, on tagged and reserved servers. See
  [Scaling and placement](/docs/guides/scaling-and-placement/). Code that escapes its container reaches the server. To
  keep strangers from doing this, use the [sandboxed runtime](/docs/operations/sandboxed-runtime/).
- **Shared database servers.** Any service can open a connection to a shared server. Each database has its own login
  that reaches only that database.
- **Geass itself.** Geass has access to the whole cluster. Its code and the network rules keep organizations apart.
- **The GitHub App.** Anyone who can install it on an account that they own can link that installation.
- **The domain, Cloudflare zone, and sign-in of the platform.** Organizations do not see their settings.

Not built: running the projects of an organization on another cluster.

## If something goes wrong

- **"Your account is not in an organization set up here."** The token named no organization that exists in Geass.
  Check the claim, and the **Id at the identity provider** of each organization. Or the person is in none of the
  workspaces of the organization, and does not administer it.
- **Nobody sees any project after you turn it on.** Only platform administrators see projects that belong to nobody.
  Assign the projects.
- **You are locked out.** Sign in with the password of an admin. Open **Settings** → **Sign-in**, and click
  **Turn organizations off**. If you cannot use the dashboard, Geass has no other way yet. See
  [Under the hood](#under-the-hood).

## Under the hood

- An organization is a `GeassOrganization`. Its limits are `maxProjects`, `quota`, `region`, `requiredTags`, and
  `allowedDomains`. The policies without a dashboard setting are `limits`, `egress`, and `abuse`. See
  [GeassOrganization](/docs/reference/resources/geass-organization/).
- The organization switch is `spec.auth.tenancy` of the platform configuration. Billing records need
  `spec.usage.enabled`. The sandboxed runtime is `spec.tenancy.sandbox`, and registry isolation is
  `spec.tenancy.registry`.
- The builds of an organization run in the namespace `geass-build-<organization>`.
- The client secret of the own provider of an organization is in the Secret `org-idp-<organization>`.
- The API can also create organizations and workspaces: `POST /api/orgs/create` and
  `POST /api/orgs/{name}/workspaces/create`, with `displayName` and `idpID`.
- To set the noisy-neighbour limits, the outbound limits, or abuse detection of an organization, edit `spec.limits`,
  `spec.egress`, or `spec.abuse` of its `GeassOrganization` on the server.
- To turn organizations off when you cannot use the dashboard, run this command on the server:

  ```bash title="Turn organizations off"
  sudo k3s kubectl -n geass-system patch geassplatformconfig platform --type merge -p '{"spec":{"auth":{"tenancy":{"enabled":false}}}}'
  ```

## Related

- [Sign in with an identity provider](/docs/guides/external-sign-in/)
- [Users and API tokens](/docs/guides/users-and-tokens/)
- [Network isolation](/docs/guides/network-isolation/)
- [GeassOrganization](/docs/reference/resources/geass-organization/)
- [Security](/docs/operations/security/)
