---
title: "Projects and environments"
description: "Group apps, databases, and buckets into projects and environments. Add or delete environments, deploy pending changes, and set project options."
---

A **project** groups related work, for example one product, one client, or one team. Inside a project, each
**environment** is a separate place where your services, databases, and buckets run. Examples are `production` and
`staging`. Use projects to keep unrelated work apart. Use environments to try a change before your users see it.

![A project with its environment tabs, pending changes, the Deploy changes button and a service's panel open on the right.](../../../../assets/docs/app-drawer.webp)
*Screenshot with example data.*

## How projects and environments relate

| | Project | Environment |
| --- | --- | --- |
| What it is | A named group. It has an ID such as `proj-1a2b3c4d` and a display name such as `morning-beach`. | A named place inside one project. A new project starts with one, called `production`. |
| What it holds | Settings for all its environments: region, required server tags, GitHub link, webhooks, backup storage, network access, pull request settings, and API tokens. | Services, functions, databases, logical databases, and buckets, and its own shared variables. |
| Deploys | | Each environment has its own pending changes. **Deploy changes** applies only the environment that you look at. |

Two things follow from this relation:

- A resource belongs to one environment. A service in `production` and `staging` is two separate services, each with
  its own ID, settings, variables, and domain. Geass does not copy resources between environments. The exceptions are
  [pull request environments](/docs/guides/pull-request-environments/) and [templates](/docs/guides/templates/).
- Services in all environments of a project can reach each other privately. Other projects cannot reach them unless you
  allow it. See [Network isolation](/docs/guides/network-isolation/).

## Create a project

1. Open **Projects**.
2. If your servers are tagged with two or more regions, a region menu appears beside **New project**. Select a region,
   or keep **Default region (…)**. See [Scaling and placement](/docs/guides/scaling-and-placement/).
3. Click **New project**.

Geass creates the project immediately, with a generated name like `morning-beach` and the `production` environment.
Then Geass opens the project. There is no form. To rename the project, use **Settings** → **General** → **Name**.

Only admins see **New project**. A read-only user (role `viewer`) can browse projects but cannot change anything.

Through the API, use a workspace admin token. To select a region, add `-d region=eu-north`. See
[Users and API tokens](/docs/guides/users-and-tokens/).

```bash title="Create a project"
curl -X POST https://geass.example.com/api/projects/create \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json"
# {"project":"proj-1a2b3c4d"}
```

## Environments

### The environment tabs

When you open a project, the top bar shows these items:

- The project switcher.
- One tab for each environment.
- **+ Environment** after the tabs. Only admins see it.
- On the right, the pending-changes button, **Deploy changes**, and your account menu.

The selected tab is the environment that the canvas, **Deploy changes**, and **+ Add resource** act on. The address
keeps the selection as `?environment=staging`. So you can bookmark or share it. The first environment opens by default.

A tab for a [pull request environment](/docs/guides/pull-request-environments/) has a **PR #n** badge, where `n` is
the pull request number.

### Add an environment

1. In the top bar, click **+ Environment**. The **Settings** of the project open.
2. Select **Environments**.
3. Enter a name in the field that shows `staging` as an example.
4. Click **New Environment**.

The name contains only lowercase letters, digits, and hyphens. It starts and ends with a letter or digit. With a
13-character project ID, the name can have up to 49 characters. A project can have at most 100 environments. Geass
refuses a name that is already in use ("environment already exists").

A new environment is empty. Fill it with **+ Add resource**, **From template**, or pull request environments.

```bash title="Add an environment through the API"
curl -X POST https://geass.example.com/api/projects/proj-1a2b3c4d/environments/create \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json" \
  -d environment=staging
```

### Delete an environment

1. Open **Settings** → **Environments** of the project.
2. On the environment, click **Delete**. The button appears only when the project has more than one environment.
3. Read the warning in **Delete environment?**.
4. Click **Delete environment**.

:::danger
Make sure that you no longer need the data. Geass deletes all services, databases, logical databases, and buckets of
the environment. It also deletes the shared variables and the API tokens limited to the environment. You cannot undo
this.
:::

A project keeps at least one environment ("a project must retain at least one environment"). For what Geass keeps of
a deleted database, see [Backups and restore](/docs/guides/backups-and-restore/).

## The project canvas

**Resources** is the first tab of a project. It shows each resource of the selected environment as a card on a canvas.

- **Position.** Services and functions sit in the top row. Databases, logical databases, and buckets sit below.
- **Card.** Each card shows an icon, the display name, a short kind (`app`, `function`, or `postgres 16`), and the
  source. The footer shows the status, and the public host or the number of pending changes.
- **Status.** **Ready**, **Setting up**, **Draft**, **Unavailable**, **Failed**, or **Deleting**. A **New** badge marks a
  resource that was never deployed.
- **Lines.** A line joins a service to a database or bucket that it references. A line also joins a logical database to
  its server.
- **Move and zoom.** Drag the background to pan. Scroll to zoom, from 45% to 160%, or use the three zoom buttons. Drag a
  card to move it. Only your browser keeps the card positions.
- **Open.** Click a card to open its panel on the right. To go back, click the close button of the panel.

Admins also see **From template** and **+ Add resource** above the canvas. **+ Add resource** offers six choices:
**GitHub Repository**, **Database**, **Template**, **Docker Image**, **Function**, and **Bucket**. You can type to filter,
or paste a GitHub link or an image name. Everything that you add is a draft in the environment named at the bottom of
the box, for example "Adding to staging".

If you switch environments while a panel is open, Geass closes the panel.

## Pending changes and Deploy changes

Geass does not change what runs when you edit a form. New resources start as **drafts**. Most edits to a deployed
resource are **pending changes**. You apply them together.

The top bar shows the state of the selected environment:

- **Up to date** (disabled) means that nothing waits.
- **N pending** counts the saved changes that are not deployed. A new draft counts 1. Each saved variable change adds 1.
  Pending settings count 1, however many fields they touch.

  Click **N pending** to open a list of the resources with what changed, for example `Port: 8080 → 3000`,
  `Variable added: LOG_LEVEL`, or `Function code updated`. Click a row to open the resource. **Deploy all** does the same
  as **Deploy changes**.
- **Deploy changes** applies every pending change in the selected environment. While it runs, it reads **Deploying…**.
  A toast then reports "Deployed 1 change" or "Deployed N changes". N counts resources.

The list gives one of three reasons for each resource:

| Reason | Meaning |
| --- | --- |
| **draft** | The resource was created but never deployed. Nothing runs yet. |
| **settings** | You changed a setting that restarts the service, for example the port, the image, or the size. |
| **variables** | You changed variables, secrets, references, or attached shared variables. |

Some settings take effect when you save them, and are never pending:

- Replicas, autoscaling, and sleep when idle.
- The domain of a service, HTTPS, and the DNS check.
- Metrics.
- Watch paths.
- Database backup settings.

The forms mark most of these settings **Applies instantly**. If you change a setting back to its deployed value, its
**settings** marker goes away.

Things to know:

- Each deploy restarts the service, even when only a variable changed.
- **Deploy changes** works on the selected environment only. For another environment, switch tabs and deploy again.
- **Deploy changes** deploys services first, then databases, then buckets. If a resource does not fit in the free
  capacity, the deploy stops there with the capacity message. The resources before it stay deployed. See
  [Usage and capacity](/docs/guides/usage-and-capacity/).
- If nothing was deployed, you see "No pending changes were deployed. Check the environment filter and resource
  settings."
- Read-only users do not see the deploy controls.
- The panel of a resource has no deploy button for pending changes. The panel of a service shows **Redeploy** only when
  nothing is pending.

### Deploy from a script

With a project token, deploy the environment of the token:

```bash title="Deploy pending changes from CI"
curl -X POST https://geass.example.com/api/token/deploy \
  -H "Authorization: Bearer $GEASS_PROJECT_TOKEN" \
  -H "Accept: application/json"
# {"deployed":2,"ok":true}
```

With a workspace admin token, call `POST /api/projects/proj-1a2b3c4d/deploy` with `-d environment=production`.

With the command line and a workspace token, `geass deploy -p proj-1a2b3c4d -e production --wait` deploys an
environment and waits until each service is ready. With a project token, name the services: `geass deploy web api
--wait`. An agent uses the `deploy_app` tool. See [Command line](/docs/guides/command-line/) and
[MCP server](/docs/guides/mcp-server/).

## Project settings

Open the **Settings** tab of a project. The page is called **Project Settings**, and it has these sections:

| Section | What it holds |
| --- | --- |
| **General** | **Name**, **Description** (disabled in this release), **Region**, the **Project ID** with a copy button, **Generate Template from Project**, and **Visibility**. Projects are private, and **Change visibility** is disabled. |
| **Usage** | What the project uses, and a count of services, databases, and buckets. See [Usage and capacity](/docs/guides/usage-and-capacity/). |
| **Environments** | The environments, **New Environment**, **Delete**, and **PR Environments**. |
| **Shared Variables** | Variables that every service in an environment can receive. See [Environment variables and secrets](/docs/guides/environment-variables/). |
| **Webhooks** | URLs that Geass calls when a service deploys. See [GitHub and builds](/docs/guides/github-and-builds/#outgoing-webhooks). |
| **Backups** | Where the databases of the project keep backups. See [Backups and restore](/docs/guides/backups-and-restore/). |
| **Network** | Which other projects can reach this project, and whether its services are kept away from the servers of the cluster. See [Network isolation](/docs/guides/network-isolation/). |
| **Limits** | How much CPU, memory, disk, and running copies the project and each environment can use. See [Project limits](/docs/guides/project-limits/). |
| **Compliance** | The server tags that the project requires, such as `hipaa`. Everything in the project runs on servers with these tags. See [Compliance tags](/docs/guides/compliance-tags/). |
| **Tokens** | API tokens limited to this project. See [Users and API tokens](/docs/guides/users-and-tokens/). |
| **GitHub** | **Connect to GitHub** or **Disconnect**, and **Report to GitHub**. See [GitHub and builds](/docs/guides/github-and-builds/). |
| **Danger** | **Manage Services** and **Delete Project**. |

### Name and region

To rename the project, change **Name** and click **Update**. Geass lowercases the name and changes spaces and
underscores into hyphens. `My Shop` becomes `my-shop`. Geass drops other characters, so `Shop!` becomes `shop`. Geass
refuses a name that ends up empty ("project name is required"). The name is a label. The ID never changes, and Geass
does not check that a name is unique.

**Region** appears when the cluster has regions. It is where the project runs. After the project has an in-cluster
database, the region cannot change. The message is "This project has databases, and their data stays on the nodes of
the region they were created in, so the region cannot change". Without databases, you can change the region, and the
services restart there.

### Save a project as a template

**Generate Template from Project** saves the services and in-cluster databases of one environment as a template. See
[Templates](/docs/guides/templates/).

## Move a project to another cluster

You can export a project as a bundle and import the bundle on another cluster as a new project. The new resources are
drafts, without secret values and without data. See
[Moving a project to another cluster](/docs/guides/moving-a-project/).

## Delete a project

Open **Settings** → **Danger**.

- **Manage Services** lists each resource in each environment with a **Remove** button. Geass asks you to confirm in
  **Delete resource permanently?**, and then deletes only that resource.
- **Delete Project** deletes everything.

To delete the project, do these steps:

1. Enter the project name in the box. Spaces and capital letters do not matter.
2. Click **Delete**.
3. In the dialog, click **Delete permanently**.

:::danger
Make sure that you no longer need the data. Geass deletes every service, function, database, logical database, and
bucket in every environment. It also deletes the shared variables, the webhooks, the API tokens, the GitHub connection,
and the cloud connections of the project.
:::

Geass does not remove the GitHub App from your GitHub account, the images that your builds pushed, or anything that you
created in AWS outside Geass. For what Geass keeps of deleted databases and buckets, see
[Backups and restore](/docs/guides/backups-and-restore/) and [Object storage](/docs/guides/object-storage/).

```bash title="Delete a project through the API"
curl -X POST https://geass.example.com/api/projects/proj-1a2b3c4d/settings/delete \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json" \
  -d confirmName=morning-beach
```

To delete an environment through the API, call `POST /api/projects/{project}/environments/archive` with `environment`
and `confirmName`, both set to the environment name.

## Troubleshooting

**New project says "no cluster found".** The install is not finished. Wait until the cluster is ready. See
[Install](/docs/getting-started/install/).

**New project fails with "No node is in the … region yet. Tag a node with it first".** No server carries the region
that you selected. Tag a server first. See [Scaling and placement](/docs/guides/scaling-and-placement/).

**Add an environment fails with `invalid environment`.** Use lowercase letters, digits, and hyphens. Start and end the
name with a letter or digit. A name that is too long fails with "project/environment namespace … is invalid". Use a
shorter name.

**Deploy changes is disabled.** One of these conditions is true:

- The environment has no pending changes ("Up to date").
- You are signed in as a read-only user.
- You are on a different environment tab than the resource that you edited.

**Deploy changes stops with "This service needs about … CPU".** The cluster has no room for that size. Select a smaller
size, or add capacity. See [Usage and capacity](/docs/guides/usage-and-capacity/).

**A change that you saved is not running.** Open the pending list. Only the settings marked **Applies instantly** take
effect on save.

**The Delete button of an environment is missing.** The button shows only to admins, and only when the project has two
or more environments.

## Under the hood

A project is a `GeassProject` in `geass-system`, named by its ID. Each environment runs in its own namespace,
`<project ID>-<environment>`, for example `proj-1a2b3c4d-production`. Resources are `GeassApp`, `GeassDatabase`,
`GeassLogicalDatabase`, and `GeassObjectStore` objects in `geass-system`.

```bash title="Inspect a project"
kubectl -n geass-system get geassprojects
kubectl get namespaces -l geass.dev/project=proj-1a2b3c4d
```

After you delete a project, a namespace can stay in `Terminating` if a workload in it does not stop. Run
`kubectl get namespace -l geass.dev/project=<project ID>`, and look at what still runs there.

## Related

- [Apps](/docs/guides/apps/) and [Functions](/docs/guides/functions/)
- [Environment variables and secrets](/docs/guides/environment-variables/)
- [Pull request environments](/docs/guides/pull-request-environments/)
- [Templates](/docs/guides/templates/)
- [Moving a project to another cluster](/docs/guides/moving-a-project/)
- [Network isolation](/docs/guides/network-isolation/)
- [Users and API tokens](/docs/guides/users-and-tokens/)
