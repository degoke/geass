---
title: "Organizations and workspaces"
description: "Run many organizations on one cluster and keep their projects apart. Geass reads them from your identity provider. See what it does not isolate."
---

By default, every person who can sign in to the dashboard sees every project. There is one workspace and one set of roles. Turn on organizations when one Geass serves many groups of people who must not see the work of each other. For example, these groups can be teams of one company, or customers that you know.

Your identity provider already knows who is in which organization and team. Geass reads that data from the sign-in token. It keeps the projects, services, databases, tokens, logs, and numbers of each organization to that organization. Geass copies nothing from the provider. The provider still decides who exists and who belongs where.

:::caution
If the organizations are strangers who might attack each other, give each organization its own cluster. Organizations are for groups that you trust to share a cluster. The projects of one organization are hidden from the others, and the network between them is closed. The builds of an organization run in a namespace of their own. But all organizations run on the same machines, and they share some things. See [What it does not isolate](#what-it-does-not-isolate).
:::

## How it fits together

```text
Platform                      the people who run this Geass
└─ Organization               one group of people, set up by a platform administrator
   └─ Workspace               a team inside it (optional)
      └─ Project              what a project always was: environments, services, databases
```

- A **project** belongs to one organization. If you use workspaces, it also belongs to one workspace of that organization.
- A person **works in one place at a time**. That place is an organization, and either all of the organization or one workspace. The dashboard has a switcher. What the person sees and changes is limited to that place.
- The **role** in that place is `admin` or `viewer`, as everywhere in Geass. An administrator of an organization can work in all of the organization and in any of its workspaces. A person who is not an administrator works in the workspaces that they are in. That person has the role that the provider gives them in the organization. If the provider gives them a higher role in the workspace, they have that higher role.
- **Platform administrators** run the installation: nodes, add-ons, the domain, the registry, the identity provider, alerts, and the organizations. Platform administrators are not the administrators of an organization. The administrators of an organization cannot reach any of these platform items.

| | Platform administrator | Administrator of an organization | A member of a workspace |
| --- | --- | --- | --- |
| Sees | All | All of the organization, or one workspace | The projects of their workspace |
| Changes | All, including nodes, add-ons, domain, registry, sign-in, and organizations | The projects, services, databases, buckets, variables, tokens, and settings of the organization | What their role allows, in their workspace |
| Makes API tokens for | The whole installation, or an organization | Their organization | Their workspace, with the `admin` role there. A `viewer` cannot make tokens |

## Before you start

Organizations need [sign-in through an identity provider](/docs/guides/external-sign-in/), set up and turned on. The provider is what says who is in which organization. When you set up the provider for organizations, enter a value in **Organizations claim**. Step 4 tells you what this value is. Leave **Role claim** empty. The role then comes from each organization, so there is no role claim to name. Organizations do not work with passwords from the Secret. The exception is the admins who keep a password as the way back in. These admins are **platform administrators**.

Two things change when you turn on organizations. Plan for both:

- **Projects that already exist belong to no organization.** Until you assign these projects, only platform administrators see them. Assign each project first, as step 3 tells you. If you do not, nobody but you sees them.
- **Everyone signs in again one time.** The sessions of people from the provider end, because the new sessions carry the place where each person works.

## Set it up

### 1. Make the token say who is in which organization

Geass reads one claim of the ID token. This claim is a list with one entry for each organization that the person is in:

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
| `id` | The id of the organization at the provider. You give this id to Geass later |
| `role` | The role of the person in the organization. Geass changes it into `admin` or `viewer` with the same role map as for sign-in (`owner=admin`, `member=viewer`). If a role maps to nothing, Geass leaves out the organization. The role can be a string, a list, or a comma-separated string |
| `workspaces` | Optional. The teams that the person is in. Each team is a bare id, or an object with an `id` and an optional `role` in that team |

The claim can have any name. It can be inside nested claims, for example `https://example.com/claims.organizations`. A token can list up to 50 organizations, and up to 200 workspaces for each organization.

To make a person a **platform administrator**, name a second claim and the values that mean platform administrator. For example, use `geass_platform` with the value `operator`. Geass compares the whole value.

:::note[Better Auth]
The organization plugin of Better Auth keeps members and teams in its own tables. It puts into the ID token only what your `customIdTokenClaims` function returns, so build the list in that function. This code is a sketch. Check the model and field names against your version.

```ts
customIdTokenClaims: async ({ user, scopes }) => ({
  email: user.email,
  email_verified: user.emailVerified,
  orgs: await orgsOf(user.id),
}),
```

`orgsOf` reads the rows of the person in the `member` model (their `organizationId` and `role`) and in the `teamMember` model. It returns `[{ id, role, workspaces: [{ id }] }]`. Find these rows through `auth.$context.adapter`, as in the [single organization recipe](/docs/guides/external-sign-in/#better-auth).
:::

### 2. Set up the organizations in Geass

People can sign in only to organizations that you set up here. Thus a provider that lets anyone create an organization cannot give anyone a place in your Geass. A platform administrator creates each organization and gives it the id that the provider uses:

```bash
curl -X POST "$GEASS/api/orgs/create" -H "Authorization: Bearer $TOKEN" \
  -d displayName=Acme -d idpID=org_acme
# {"name":"org-1a2b3c4d","ok":true}

curl -X POST "$GEASS/api/orgs/org-1a2b3c4d/workspaces/create" -H "Authorization: Bearer $TOKEN" \
  -d displayName="Payments team" -d idpID=team_payments
```

`$TOKEN` is an install-wide API token that a platform administrator made. You can also apply the resource yourself. See [GeassOrganization](/docs/reference/resources/geass-organization/). Each organization and workspace gets its own Geass name (`org-…`, `ws-…`), and that name never changes. The ids of the provider can change.

### 3. Give projects to organizations

```bash
curl -X POST "$GEASS/api/projects/proj-5c2c6d05/assign" -H "Authorization: Bearer $TOKEN" \
  -d org=org-1a2b3c4d -d workspace=ws-a1b2c3
```

To take a project back, leave out `org`. The project then belongs to the platform again. A project that the people of an organization make belongs to that organization from the start. It also belongs to the workspace that they were in.

### 4. Tell Geass how the token lists them, and test it

Use **Settings** → **Sign-in**, or this command:

```bash
curl -X POST "$GEASS/api/settings/auth/tenancy" -b "$SESSION" \
  -d orgsClaim=orgs -d platformClaim=geass_platform -d platformValues=operator
```

A signed-in admin must do this step, not a token. This is the same as for every other setting that decides who can sign in. When you save, nothing turns on. Run **Test sign-in**. The page now lists the organizations that the token named, and which of them are set up. Thus you can see that Geass reads the claim as you meant.

### 5. Turn it on

```bash
curl -X POST "$GEASS/api/settings/auth/tenancy" -b "$SESSION" -d enabled=on
```

To turn on organizations, all of these conditions must be true. These conditions make sure that a mistake cannot lock everyone out:

- Sign-in through the provider is on.
- A test sign-in of exactly these settings happened in the last half hour. The person who did it administers an organization that is set up, or is a platform administrator.
- An admin signs in with a password.
- At least one organization exists, or a platform claim is set.

You can always turn organizations off. You cannot change the claims while organizations are on.

## Working in more than one place

A person who is in many organizations, or in an organization with workspaces, selects where they work in the switcher. They can also use `POST /api/session/scope` (`org`, `workspace`). Geass checks the choice against what the person got when they signed in. A change of place does not make the session longer. A sign-out at the provider ends the session, as it ends any other session.

A platform administrator starts as the platform. A platform administrator can go into any organization to see what it sees, as its administrator, and then go back.

## What happens when something changes

| What changes | When it takes effect |
| --- | --- |
| An organization is **suspended** (`spec.suspended`) or deleted, or a workspace is removed | Immediately, for sessions that are already open. Nobody from it can sign in |
| A person is removed from an organization at the provider | When their session ends (an hour by default, `sessionMinutes`). Immediately, if the provider sends a back-channel logout. Within the interval of [checking people again](/docs/guides/external-sign-in/#check-people-again-while-they-are-signed-in), if that feature is on |
| A project is assigned to a different organization | Immediately. Tokens made for the project continue to work for the new owner |
| Their role changes at the provider | At their next sign-in, or within the interval of checking people again. That check ends their session, so that they sign in to what is now true |
| The quota, region, required tags, or allowed domains of an organization change | Its workloads follow, as for a change to a project. Geass does not check again a hostname that is already in use until the service changes |
| The own provider of an organization is changed, turned off, or removed | Immediately, for the sessions made through it |

## Tokens

An API token is made for the place where its maker works. A token that the administrator of an organization makes acts for that organization (and that workspace) and for nothing else. It cannot read the projects of a different organization, and it cannot reach the pages of the platform. An organization sees and revokes its own tokens, and the tokens and deploy hooks of its projects. It never sees one of a different organization.

The scope of a token comes from what the token is, not from anything that the caller sends. There is no organization header and no `--org` flag. A request cannot point a token at a different organization. These rules apply to the scope:

- By default, a token acts where its maker works.
- A person who works in all of an organization can make a token for a single workspace of it (`workspace=ws-…`).
- A platform administrator can make a token for any organization that is set up and open (`org=org-…`, optionally `workspace=ws-…`). This is how you make a key for the automation of a customer.
- Nobody gets a wider scope than their own.

To use the command line in a different organization, sign in again from there. `geass login` approves a token for the place that the dashboard session is in.

Some tokens are **install-wide**, and they act for the platform:

- Tokens that were made before organizations existed.
- Tokens that a platform administrator makes without naming an organization.

A token of a project (`gsp_`) acts for that one project. If the project belongs to no organization, the token reaches nothing until the project is assigned.

An installation holds at most 600 tokens, and an organization holds at most 100.

## What is kept apart

- **What people see and change.** Every read and write of the dashboard, the API, the command line, and the MCP server goes through one check against the organization of the person. Geass answers about the project, service, database, or bucket of a different organization as if it does not exist. This is the same answer as for a name that was never used.
- **Lists and numbers.** The overview, usage, logs, metrics, terminals, and live updates carry the projects of the organization only. They do not carry the machines of the platform or the names of other organizations.
- **Tokens, deploy hooks, and sessions.** See the Tokens section.
- **The network.** Each project is closed to the others. See [Network isolation](/docs/guides/network-isolation/). An organization cannot name the project of a different organization when it selects who can reach its own projects.
- **Hostnames.** A service cannot take a hostname that a different service serves, or the hostname of the dashboard. Geass does not overwrite a DNS record that it did not create. You can also limit an organization to some domains. See [Policy](#what-the-platform-sets-for-an-organization).
- **GitHub.** An installation of the GitHub App that the project of one organization uses cannot be connected to the project of a different organization. A person must show that GitHub lists the installation among the installations that their own account can use. See [GitHub](#github).
- **Builds.** A project of an organization builds in a namespace that belongs to the organization. That namespace holds only the credentials of that organization and that project. See [Builds and the registry](#builds-and-the-registry).
- **The kernel, when you ask for it.** With `spec.tenancy.sandbox`, these workloads of organizations run under a sandboxed runtime such as gVisor: services, schedules, builds, and MySQL, Redis, and SQLite servers. Without the runtime, they wait and do not start. A PostgreSQL or MariaDB server of an organization cannot run in the runtime, so Geass does not make it. See [Sandboxed runtime for organizations](/docs/operations/sandboxed-runtime/).
- **Alerts.** An organization has its own channels, which get alerts only about its projects. See [Alerts](#alerts).

## What the platform sets for an organization

A platform administrator can set these items on an organization in one of these places:

- **Settings** → **Organizations**.
- The API: `POST /api/orgs/{name}/update`.
- The [GeassOrganization](/docs/reference/resources/geass-organization/).

All of the items are optional. If you leave out an item, Geass does not limit it.

| Setting | What it does |
| --- | --- |
| **Most projects** (`maxProjects`) | The organization can have no more projects than this number. Geass checks it when a project is made or given to the organization. When you lower it, Geass never removes the projects that are there. 0 stops the organization from making any project. |
| **Quota** (`quota`) | The most CPU, memory, storage, and pods that all of its projects can ask for together. A quota in the namespace of each environment holds that environment to what the other environments of the organization leave. The environment is also held to the quota of its project, if that quota is lower. If two environments start at the same instant, they can briefly go above the quota. When the quota is passed, Geass stops nothing that is running. |
| **Region** (`region`) | Where its projects run when they name no region of their own. |
| **Required node tags** (`requiredTags`) | Every workload of every project of the organization runs only on nodes that carry all of these tags. These tags are added to the tags of the project. With nodes that are tagged and reserved for the tag, an organization gets machines of its own. The tags must exist on nodes and be reserved, as for a project. |
| **Allowed domains** (`allowedDomains`) | Its services can take hostnames under these domains only, and the hostname that Geass gives a service. You cannot give two organizations domains that overlap. |
| **Noisy-neighbour limits** (`limits`) | These limits replace the defaults of the platform (`spec.tenantLimits.defaults`): the pods and ephemeral storage of each of its namespaces, what a container that asks for nothing gets, the most that a container can ask for, and the bandwidth of each pod. Set with `kubectl` only. See [Noisy-neighbour limits](/docs/guides/tenant-limits/). |
| **Outbound limits** (`egress`) | The ports that its pods can reach outside the cluster, in place of the ports of the platform (`spec.tenancy.egress`). The modes are: mail closed unless opened, other ports closed, or only a list open. Set with `kubectl` only. See [Outbound limits](/docs/guides/outbound-limits/). |
| **Abuse detection** (`abuse`) | Whether the abuse detection of the platform (`spec.tenancy.abuse`) leaves its services, or the services of some of its projects, out of the sustained-CPU signal, for legitimate heavy work. Also what Geass does to its suspects, in place of the action of the platform. Set with `kubectl` only. See [Abuse detection](/docs/guides/abuse-detection/). |

## Billing what each organization reserves

With `spec.usage.enabled` on the platform configuration, the operator records what the projects of each organization reserve: CPU, memory, and storage. It records these values hour by hour and by workspace. `GET /api/v1/usage/hours` gives these records to the administrators of the platform. The operator keeps projects that belong to no organization apart from every organization. No organization can read these records, its own figures included. See [Usage by organization, for billing](/docs/guides/usage-and-capacity/#usage-by-organization-for-billing).

## Alerts

An organization has its own alert channels, under **Settings** → **Alerts**. These channels are Slack channels that get alerts only about the projects of the organization. The alerts are about what is firing, and about failed builds, deploys, backups, crash loops, and storage. The channels never get alerts about the cluster or about a different organization.

- An administrator of an organization makes and sees its channels.
- A member of one workspace manages the channels that name projects of that workspace.
- A channel posts to `hooks.slack.com`. Mail goes out through the mail server of the platform, and an organization cannot send through it.
- Each organization can have 10 channels, and the platform keeps its own 20.

When you delete an organization, Geass deletes its channels and their Slack addresses.

## Builds and the registry

Builds run what the Dockerfile of a repository says, as root, with the login that pushes the image mounted. A project that belongs to an organization builds in a namespace that belongs to the organization (`geass-build-<organization>`), not in the namespace of the platform. Geass copies there only what the build needs: the registry login and the git token of its project. Geass removes the copies when the build ends.

The namespace has these limits:

- It allows no connection to the build.
- It lets the build reach the internet and the DNS of the cluster.
- It lets the build reach no private address: not the pods of a different project, the services of the platform, the API of the cluster, or the metadata of the cloud.
- It holds at most 20 build pods.
- Geass removes it when it removes the organization.

Thus a build of an organization cannot reach the secrets of the platform. But the build still holds a registry login. That login is the login of the platform, unless the organization has its own:

- **Set the organization's registry** under **Settings** → **Registry**, as an administrator of the whole organization. You can also use `POST /api/registry/save` (`repository`, `username`, `password`). The builds of the organization then push there, with that login. The login of the platform is then never in the namespace of the organization. A service that names a registry of its own keeps that registry.
- The build namespace reaches only public addresses. Thus the registry must be reachable on the internet.
- **On a cluster shared by organizations that do not trust each other**, a platform administrator sets [registry isolation](/docs/operations/registry-isolation/) (`spec.tenancy.registry`). With this setting:
  - Geass never gives the build of an organization the login of the platform.
  - An organization without a registry of its own does not build.
  - Each service gets a repository of its own under the repository of the organization (`<repository>/<project>/<service>`).
  - No service of an organization can run an image from the repository of the platform or from the registry of a different organization.

## Shared things an organization owns

- **Database servers.** A platform administrator can make a shared Postgres or MySQL server for an organization with `POST /api/shared-databases/create` and `org=`. The platform administrator can also give the organization a server that the platform made, with `POST /api/shared-databases/{name}/org`. Only the projects of that organization take databases on the server, and only its people see it. The server runs where the organization requires its work to run, in its region and on its tags. Its administrators can delete it when no database is on it. A shared server holds the databases of one organization only. While organizations are on, the own server of the platform is for the projects of no organization. Before organizations, every project shared this server. See [Shared database servers and organizations](/docs/guides/shared-database-servers/#organizations).
- **Cloud connections.** A connection can be made without a project, for an organization. A platform administrator makes it with `org=`, or an administrator of the whole organization makes it for their own organization. Every project of that organization can use the connection, and no other project can. Any project of the organization can use its credentials. Thus keep the connections of an organization to what all of its projects are allowed to touch.
- **Object storage.** Geass makes the buckets of an organization in the cluster on the object storage server of the platform. Each bucket reaches that server with its own login. The platform sets up the server.

You cannot delete an organization while it owns a database server or a connection. You cannot give a project to a different organization while the project has databases on a server that belongs to an organization. While organizations are on, you also cannot give a project to an organization while the project has databases on the server of the platform.

## An organization's own identity provider

An organization can prefer that its people sign in through its own provider, for example its Okta or its Entra ID. You can give that organization its own provider. To set it up, a platform administrator does these steps:

1. Open **Settings** → **Organizations** → the organization → **Sign-in provider of its own**.
2. Enter the issuer, the client, a role claim, and what each value of the role claim maps to (`admin` or `viewer`). Optionally, enter claims that a person must have.
3. Tell the provider to send people back to the callback address that the page shows. This address is the same as for the provider of the platform.
4. Click **Test sign-in**, as a person who maps to admin.
5. Click **Turn on**.

The people of the organization sign in from the link that the page shows (`/auth/oidc/login?org=<name>`). They work in that organization, in all of it, with the role that their claim maps to. An administrator can change the organization, and a viewer reads all of it. Geass asks the provider of the organization nothing else. That provider cannot make anyone an administrator of the platform, put a person in a different organization, or select a workspace.

- When you save the provider, Geass turns it off, and the provider needs a new test. These actions end every session made through the provider immediately: you turn the provider off or on, you remove it, or you suspend the organization.
- The provider needs organizations on. With [checking people again](/docs/guides/external-sign-in/#check-people-again-while-they-are-signed-in) on, Geass checks people of this provider in the same way as people of the provider of the platform. Geass does not read a back-channel logout from the provider of an organization. Its sessions end with their session length or with the check.
- Geass keeps the client secret in a Secret (`org-idp-<organization>`). The client secret never comes back.

## GitHub

One GitHub App serves every organization. With organizations on, a person who connects a project to an installation must show that GitHub lists that installation among the installations that their own account can use. This works as follows:

1. When the person installs the App, the App asks the person to authorize it.
2. Geass trades the code that GitHub sends back for the token of the person.
3. Geass asks GitHub about the installation.

An installation id that is only written into an address is not sufficient. In the settings of the App on GitHub, turn on **Request user authorization (OAuth) during installation**. An App that Geass made has this setting on already. Without this setting, Geass refuses a person of an organization, with a message that says so. Geass does not ask a platform administrator, because a platform administrator can connect any installation as it is. An installation that the project of one organization uses cannot also be connected to the project of a different organization, whoever asks.

## What it does not isolate

The organizations share these things. This is acceptable for organizations that trust each other:

- **Machines.** The pods of every organization run on the same nodes. To change this, reserve nodes with [node tags](/docs/guides/scaling-and-placement/), and give each organization its own tags, as [What the platform sets for an organization](#what-the-platform-sets-for-an-organization) tells you. A pod that escapes its container reaches the node. To keep strangers from doing this, run the services, schedules, builds, and database servers of organizations under gVisor or Kata with the [sandboxed runtime](/docs/operations/sandboxed-runtime/). Their PostgreSQL and MariaDB servers cannot run in the sandboxed runtime. The object storage server, which holds the buckets of every organization, does not run in it. [Noisy-neighbour limits](/docs/guides/tenant-limits/) limit the pods, disk, and bandwidth of each organization. They do not limit the pods of an organization on one node, or its disk reads and writes.
- **The network of shared servers.** A shared database server runs in the namespace of the platform, and any pod can open a connection to it. Each logical database has its own login that reaches that database and nothing else. The login of the administrator is in a Secret that no organization reads.
- **The operator and the dashboard.** They hold cluster-wide access. Their code and the network policy make the separation.
- **The GitHub App.** There is one App for the installation. Anyone who can install it on an account that they own can connect that installation.
- **The platform's domain, Cloudflare zone and sign-in.** These belong to the platform. Organizations do not see their settings.

Not built: sending the projects of an organization to a different cluster. Each organization names the cluster that Geass makes its new projects in (`clusterRef`). But Geass runs on one cluster today, so this field changes nothing yet.

## If something goes wrong

- **"Your account is not in an organization set up here."** The sign-in code is `no_org`. One of these is true:
  - The token named no organization that is set up. Check the claim and the `idpID` of the organizations.
  - The person is in the organization, but in none of its workspaces, and the person does not administer it.
- **Everyone sees nothing after you turn it on.** Only platform administrators can see projects that belonged to nobody. Assign these projects, or sign in as a platform administrator.
- **You are locked out.** Sign in with the password of an admin. An admin with a password is a platform administrator. Open **Settings** → **Sign-in** and turn off organizations. Without the dashboard, run this command:

  ```bash title="Turn organizations off"
  sudo k3s kubectl -n geass-system patch geassplatformconfig platform --type merge -p '{"spec":{"auth":{"tenancy":{"enabled":false}}}}'
  ```

## Related

- [Sign in with an identity provider](/docs/guides/external-sign-in/)
- [Users and API tokens](/docs/guides/users-and-tokens/)
- [Network isolation](/docs/guides/network-isolation/)
- [GeassOrganization](/docs/reference/resources/geass-organization/)
- [Security](/docs/operations/security/)
