---
title: "Projects and environments"
description: "Group apps, databases and buckets into projects and environments, add or delete environments, deploy pending changes, and set per-project options."
---

A **project** is the unit you organize work in: one product, one client, one team. Inside a project, each
**environment** (for example `production` and `staging`) is an isolated copy of the place where your services,
databases and buckets run. You use projects to keep unrelated work apart, and environments to try a change before it
reaches the one your users see.

![A project with its environment tabs, pending changes, the Deploy changes button and a service's panel open on the right.](../../../../assets/docs/app-drawer.webp)
*Screenshot with example data.*

## How projects and environments relate

| | Project | Environment |
| --- | --- | --- |
| What it is | A named group. It has an ID such as `proj-1a2b3c4d` and a display name such as `morning-beach`. | A named slot inside one project. A new project starts with one, called `production`. |
| What lives in it | Settings shared by every environment: region, required node tags, GitHub link, webhooks, backup storage, network access, pull request settings and API tokens. | Services, functions, databases, logical databases and buckets, plus its own shared variables. |
| Where it runs | Nowhere by itself. | In its own Kubernetes namespace, `<project ID>-<environment>`, for example `proj-1a2b3c4d-production`. |
| Deploys | | Each environment keeps its own list of pending changes, and **Deploy changes** applies only the environment you are looking at. |

Two things follow from this:

- A resource belongs to exactly one environment. A service that runs in `production` and `staging` is two separate
  services, each with its own ID, settings, variables and domain. Geass does not copy resources between environments
  for you, except for [pull request environments](/docs/guides/pull-request-environments/) and
  [templates](/docs/guides/templates/).
- Environments of one project are not walled off from each other. Services in every environment of a project can
  reach each other privately, and other projects cannot reach them unless you allow it. See
  [Network isolation](/docs/guides/network-isolation/).

## Create a project

1. Open **Projects** and choose **New project**.
2. If your nodes are tagged with two or more regions, a region menu appears beside the button. Pick a region first, or
   leave it on **Default region (…)**. See [Scaling and placement](/docs/guides/scaling-and-placement/).

Geass creates the project at once. It generates a name like `morning-beach` and an ID like `proj-1a2b3c4d`, creates
the `production` environment and opens the project. There is no form: you rename the project afterwards under
**Settings** → **General** → **Name**.

Only admins see **New project**. A read-only user (role `viewer`) can browse projects but cannot change anything.

The same thing through the API, with a workspace admin token (see [Users and API tokens](/docs/guides/users-and-tokens/)):

```bash title="Create a project"
curl -X POST https://geass.example.com/api/projects/create \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json"
# {"project":"proj-1a2b3c4d"}
```

Add `-d region=eu-north` to choose a region. Geass uses the cluster's default region when you leave it out.

:::note
The first environment of a project is not special to Geass, but it is the default one: the dashboard opens it first,
and [pull request environments](/docs/guides/pull-request-environments/) copy it unless you choose another.
:::

## Environments

### The environment tabs

When you open a project, the top bar shows the project switcher, then one tab for each environment with a
**+ Environment** link after them (admins only). On the right are the pending-changes button, **Deploy changes** and your
account menu. The selected tab is the environment the canvas, **Deploy changes** and **+ Add resource** act on. The
choice is kept in the address as `?environment=staging`, so you can bookmark or share it.

A tab for a [pull request environment](/docs/guides/pull-request-environments/) carries a **PR #n** badge, where `n`
is the pull request number.

If you switch environments while a service or database panel is open, Geass closes the panel and shows the
canvas of the new environment, because the same resource does not exist there.

### Add an environment

1. Choose **+ Environment** in the top bar. This opens the project's **Settings**.
2. Open **Environments**.
3. Type a name in the field that shows `staging` as an example and choose **New Environment**.

The name must be lowercase letters, digits and hyphens, start and end with a letter or digit, and make the namespace
name `<project ID>-<environment>` at most 63 characters. With a 13-character project ID that leaves 49 characters. A
name that is already in use is refused ("environment already exists"). A project can have at most 100 environments.

A new environment is empty. Fill it with **+ Add resource**, **From template**, or the pull request feature.

```bash title="Add an environment through the API"
curl -X POST https://geass.example.com/api/projects/proj-1a2b3c4d/environments/create \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json" \
  -d environment=staging
```

### Delete an environment

1. Open the project's **Settings** → **Environments**.
2. Choose **Delete** on the environment. The button only appears when the project has more than one environment.
3. Read the warning in **Delete environment?** and choose **Delete environment**.

Deleting an environment removes the environment itself, all of its services, databases, logical databases and
buckets and their running workloads, its shared variables, and the API tokens that were limited to it. This cannot be
undone. A project must keep at least one environment ("a project must retain at least one environment"), so the last
one cannot be deleted.

Databases follow the same rules as when you delete them one at a time. See
[Backups and restore](/docs/guides/backups-and-restore/) for what is kept.

```bash title="Delete an environment through the API"
curl -X POST https://geass.example.com/api/projects/proj-1a2b3c4d/environments/archive \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json" \
  -d environment=staging -d confirmName=staging
```

## The project canvas

**Resources** is the first tab of a project. It shows every resource of the selected environment as a card on a
canvas.

- **Position.** Services and functions sit in the top row. Databases, logical databases and buckets sit below, three
  to a row.
- **Card.** Each card shows an icon (a function, a GitHub branch for a repository service, or a box), the display
  name, a short kind such as `app`, `function` or `postgres 16`, and the source: `acme/storefront@main`, an image name,
  or `Function · Bun (TypeScript)`. The footer shows the status and either the public host or the number of pending
  changes.
- **Status.** **Ready**, **Setting up**, **Draft**, **Unavailable**, **Failed** or **Deleting**. A **New** badge marks
  a resource that has never been deployed.
- **Lines.** A line joins a service to a database or bucket when the service has a reference to it, and a logical
  database to its server.
- **Move and zoom.** Drag the background to pan, scroll to zoom (45% to 160%), and use the three buttons for zoom in,
  zoom out and reset. Drag a card to move it. Card positions are remembered in your browser only, per project, so
  another person or browser sees the default layout.
- **Open.** Click a card to open its panel, docked on the right of the canvas. Use the close button in the panel to go
  back.

Admins also see **From template** and **+ Add resource** above the canvas. **+ Add resource** opens a search box with
six choices: **GitHub Repository**, **Database**, **Template**, **Docker Image**, **Function** and **Bucket**. You can
type to filter, or paste a GitHub repository link or an image name. Everything you add is created as a draft in the
environment named at the bottom of the box ("Adding to staging").

## Pending changes and Deploy changes

Geass does not change what is running the moment you edit a form. New resources start as **drafts**, and most edits to
a deployed resource are saved as **pending changes**. You apply them together.

The top bar shows the state of the selected environment:

- **Up to date** (disabled) means nothing is waiting.
- **N pending** counts the saved changes that are not deployed. A new draft counts 1, every saved variable change adds 1,
  and pending settings count 1 however many fields they touch, so one resource can account for several. Choose it to
  open a list of those resources with what changed, for example `Port: 8080 → 3000`, `Variable added: LOG_LEVEL` or
  `Function code updated`. Choose a row to open that resource. **Deploy all** in the list does the same as **Deploy
  changes**.
- **Deploy changes** applies every pending change in the selected environment. While it runs it reads **Deploying…**.
  A toast then reports "Deployed 1 change" or "Deployed N changes", where N counts resources, not individual edits.

A resource can wait for a deploy for three reasons, which the list calls **draft**, **settings** and **variables**:

| Reason | Meaning |
| --- | --- |
| Draft | The resource was created but never deployed. Nothing runs yet. |
| Settings | You changed a setting that replaces the running pods (for example the port, the image, or the size). |
| Variables | You changed variables, secrets, references or attached shared variables. |

Some settings take effect as soon as you save them and never appear as pending: replicas, autoscaling, sleep when idle, a
service's domain, HTTPS and DNS check, metrics, watch paths, and database backup settings. The forms mark most of them **Applies
instantly**. If you change a setting back to the value it had at the last deploy, its **settings** marker goes away.

Each deploy restarts a service's pods, even when only a variable changed, so they pick up the new values.

Things to know:

- **Deploy changes** works on the selected environment only. Switch tabs and deploy again for the next one.
- It deploys services first, then databases, then buckets. If a resource does not fit in the cluster's free capacity,
  the deploy stops there with the capacity message, and the resources before it stay deployed. See
  [Usage and capacity](/docs/guides/usage-and-capacity/).
- If nothing was deployed, you see "No pending changes were deployed. Check the environment filter and resource
  settings."
- Read-only users do not see the deploy controls.
- A resource's panel has no deploy button for its pending changes. Use **Deploy changes** or **Deploy all**. A service's
  panel header shows **Redeploy** instead, and only while nothing is pending.

Deploy from a script with a project token, which deploys the environment the token belongs to:

```bash title="Deploy pending changes from CI"
curl -X POST https://geass.example.com/api/token/deploy \
  -H "Authorization: Bearer $GEASS_PROJECT_TOKEN" \
  -H "Accept: application/json"
# {"deployed":2,"ok":true}
```

With a workspace admin token, send the project and environment yourself:
`POST /api/projects/proj-1a2b3c4d/deploy` with `-d environment=production`.

The command line does the same. `geass deploy -p proj-1a2b3c4d -e production --wait` deploys everything pending in an
environment with a workspace token and waits until each service is ready. With a project token, name the services:
`geass deploy web api --wait`. An agent uses the `deploy_app` tool. See [Command line](/docs/guides/command-line/) and
[MCP server](/docs/guides/mcp-server/).

## Project settings

Open a project's **Settings** tab. The page is called **Project Settings** and has these sections.

| Section | What it holds |
| --- | --- |
| **General** | **Name**, a **Description** field that is disabled in this release, **Region**, the **Project ID** with a copy button, **Generate Template from Project**, and **Visibility** (a note that the project is private; **Change visibility** is disabled). |
| **Usage** | What the project takes of the cluster, and a count of services, databases and buckets. See [Usage and capacity](/docs/guides/usage-and-capacity/). |
| **Environments** | The environments, **New Environment**, **Delete**, and the **PR Environments** settings. |
| **Shared Variables** | Variables that every service in an environment can receive. See [Environment variables and secrets](/docs/guides/environment-variables/). |
| **Webhooks** | URLs Geass calls when a service deploys. See [GitHub and builds](/docs/guides/github-and-builds/#outgoing-webhooks). |
| **Backups** | Where the project's databases keep backups. See [Backups and restore](/docs/guides/backups-and-restore/). |
| **Network** | Which other projects may reach this one, and whether its services are kept away from the cluster's machines. See [Network isolation](/docs/guides/network-isolation/). |
| **Limits** | What the project, and each of its environments, can ask for in CPU, memory, disk and pods. See [Project limits](/docs/guides/project-limits/). |
| **Compliance** | The node tags this project requires, such as `hipaa`. Everything the project runs stays on nodes that carry them. See [Compliance tags](/docs/guides/compliance-tags/). |
| **Tokens** | API tokens limited to this project. See [Users and API tokens](/docs/guides/users-and-tokens/). |
| **GitHub** | **Connect to GitHub** or **Disconnect**, and **Report to GitHub**. See [GitHub and builds](/docs/guides/github-and-builds/). |
| **Danger** | **Manage Services** and **Delete Project**. |

### Name and region

Change **Name** and choose **Update**. Geass lowercases the name and turns spaces and underscores into hyphens, so
`My Shop` becomes `my-shop`, and it drops any other character, so `Shop!` becomes `shop`. A name that ends up empty is
refused ("project name is required"). The name is a label: the ID in the address bar never changes. Geass does not
check that a name is unique.

**Region** appears when the cluster has regions. It is where the project's workloads run. It cannot change once the
project has an in-cluster database, because the data sits on the nodes of that region ("This project has databases, and
their data stays on the nodes of the region they were created in, so the region cannot change"). Without databases,
you can change it, and services restart in the new region.

### Save a project as a template

**Generate Template from Project** saves one environment's services and in-cluster databases as a reusable template.
See [Templates](/docs/guides/templates/).

## Move a project to another cluster

A project can be exported as a bundle of its definition and imported on another cluster as a new project, with its services, databases and buckets as drafts and without any secret value. The data in databases and buckets is not in the bundle. See [Moving a project to another cluster](/docs/guides/moving-a-project/).

## Delete a project

Open **Settings** → **Danger**.

- **Manage Services** lists every resource in every environment with a **Remove** button. Removing one asks you to
  confirm in **Delete resource permanently?** and deletes only that resource.
- **Delete Project** deletes everything. Type the project name in the box (spacing and capitalization are ignored),
  choose **Delete**, then choose **Delete permanently** in the dialog.

Deleting a project removes:

- every service, function, database, logical database and bucket in every environment, and their running workloads;
- the namespaces of all environments, with their shared variables and secrets;
- the project's webhooks and signing secrets, and its API tokens;
- the project's own cloud connections (AWS or PlanetScale connections scoped to it) and its GitHub connection.

It does not remove the GitHub App from your GitHub account, the container images your builds pushed to your registry,
or anything you created in AWS outside Geass. Databases follow the same rules as deleting them one by one: see
[Backups and restore](/docs/guides/backups-and-restore/) and [Object storage](/docs/guides/object-storage/) for what
is kept.

```bash title="Delete a project through the API"
curl -X POST https://geass.example.com/api/projects/proj-1a2b3c4d/settings/delete \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -H "Accept: application/json" \
  -d confirmName=morning-beach
```

## What happens under the hood

- A project is a `GeassProject` named by its ID in the `geass-system` namespace. Its `spec.environments` is the list of
  environments, and `spec.displayName` is the name you see.
- For each environment the project controller creates the namespace `<project ID>-<environment>`, labelled
  `geass.dev/project` and `geass.dev/environment`, with the network policies that isolate it. It also writes the
  environment's shared variables into the ConfigMap `geass-shared-variables` and the Secret `geass-shared-secrets`
  in that namespace.
- Resources are `GeassApp`, `GeassDatabase`, `GeassLogicalDatabase` and `GeassObjectStore` objects in
  `geass-system`, linked to the project and environment by `spec.project` and `spec.environment`. Their workloads
  run in the environment's namespace.
- `spec.requiredTags` on the `GeassProject` lists the node tags every workload of the project must run on (see
  [Compliance tags](/docs/guides/compliance-tags/)). `spec.environments` holds at most 100 entries.
- A resource waits for deploy when its `geass.dev/pending-changes` annotation is set, or `spec.deploy.enabled` is
  false. The operator runs a deployed resource from the snapshot in `geass.dev/last-deployed`, with the instant
  settings applied on top, until you deploy again.

```bash title="Inspect a project"
kubectl -n geass-system get geassprojects
kubectl get namespaces -l geass.dev/project=proj-1a2b3c4d
kubectl -n proj-1a2b3c4d-production get deploy,svc,ingress
kubectl -n geass-system get geassapps -o custom-columns=NAME:.metadata.name,PROJECT:.spec.project,ENV:.spec.environment
```

## Troubleshooting

**New project says "no cluster found".** The operator has no `GeassCluster` yet. Finish the install and wait for the
cluster to be ready. See [Install](/docs/getting-started/install/).

**New project fails with "No node is in the … region yet. Tag a node with it first".** You picked a region that no node
carries. Tag a node first. See [Scaling and placement](/docs/guides/scaling-and-placement/).

**Adding an environment fails with `invalid environment`.** The name is not a valid DNS label. Use lowercase letters,
digits and hyphens, starting and ending with a letter or digit. A name that makes the namespace longer than 63
characters is refused with "project/environment namespace … is invalid": keep it short.

**Deploy changes is disabled.** The environment has no pending changes ("Up to date"), you are signed in as a
read-only user, or you are on a different environment tab than the resource you edited.

**Deploy changes stops with "This service needs about … CPU".** The cluster has no room for the size you asked for. Pick
a smaller size or add capacity. See [Usage and capacity](/docs/guides/usage-and-capacity/).

**A change I saved is not running.** Check the pending list. Settings and variables wait for a deploy. Only the
settings marked **Applies instantly** take effect on save.

**The Delete button for an environment is missing.** It only shows when the project has two or more environments, and
only to admins.

**The project is deleted but a namespace is still there.** The operator deletes the environment namespaces while it
removes the project. A namespace whose workload will not stop stays in `Terminating`. Run
`kubectl get namespace -l geass.dev/project=<project ID>` and look at what still runs in it.

## Related

- [Apps](/docs/guides/apps/) and [Functions](/docs/guides/functions/): what you put in an environment.
- [Environment variables and secrets](/docs/guides/environment-variables/)
- [Pull request environments](/docs/guides/pull-request-environments/)
- [Templates](/docs/guides/templates/)
- [Moving a project to another cluster](/docs/guides/moving-a-project/)
- [Network isolation](/docs/guides/network-isolation/)
- [Users and API tokens](/docs/guides/users-and-tokens/)
