---
title: "Templates"
description: "Deploy self-hosted software from the catalog in one step, or save your own set of services and databases: what the catalog creates and how to write a template."
---

A **template** is a named set of services, functions and databases that Geass creates in a project in one step. Geass
ships a **catalog** of self-hosted software, such as Infisical, ReFHIR and browserbay, that you can deploy with one click. You can also
save a stack you built as your own template, so you can create it again in another project. Templates do not run
anything by themselves: they add **drafts** to your canvas, and you review them and deploy.

![The Templates page.](../../../../assets/docs/templates.webp)
*Screenshot with example data.*

A template is not a Helm chart. Geass has no Helm-based templates. A template is a list of Geass resources.

## Before you start

You need to be an admin. A read-only user sees "Your viewer role cannot browse templates." on the **Templates** page.

## The Templates page

Choose **Templates** in the left rail. The page has two groups:

- **Catalog**: the software that ships with Geass.
- **Your templates**: templates you saved from a project or wrote yourself. It reads "No saved templates yet" until you
  save one.

Each card shows the template's name and description and a list of its resources, with the image, the database engine, or
`Function · bun`. A saved template also has a trash button. You can also reach the same list from a project: **From
template** above the canvas opens this page, and **+ Add resource** → **Template** lists the templates in the add box.
Choosing one there adds it to the current environment at once, without the **Project** and **Environment** choices.

## The catalog

The catalog is software you would otherwise install and wire up yourself. Each entry pins the image version it was
tested with. Deploying one creates drafts, so you can change anything before it runs.

| Entry | What it creates | After you deploy |
| --- | --- | --- |
| **Gatus** | One service, `gatus`, running `twinproduction/gatus:v5.37.0` on port 8080. It has a `config.yaml` file that checks Gatus itself, and sets `GATUS_CONFIG_PATH` to that file. | Add your own checks to the `config.yaml` variable on the service's **Variables** tab, then deploy the change. Results are kept in memory, so they reset when the service restarts. |
| **Infisical** | A Postgres database, a Redis database and a service, `infisical`, running `infisical/infisical:v0.166.8` on port 8080. It reserves 0.5 CPU and 1 GB of memory and may use up to 2 GB. It gets `DB_CONNECTION_URI` and `REDIS_URL` from the databases and a generated `ENCRYPTION_KEY` and `AUTH_SECRET`. `SITE_URL` starts as `http://localhost:8080`. | Add a domain to the service and set `SITE_URL` to its address. Keep a copy of `ENCRYPTION_KEY`, because without it the stored secrets cannot be read. The first start runs database migrations and takes a few minutes. |
| **ReFHIR** | A Postgres database and a service, `refhir`, running `ghcr.io/hai-hq/refhir:0.0.2` on port 8080, a FHIR R4 server. It reserves 0.25 CPU and 256 MB and may use up to 1 CPU and 1 GB. It gets `REFHIR_POSTGRES_URL` from the database and a generated `REFHIR_AUDIT_KEY`. | The FHIR packages ship inside the image, so the first start needs no internet access and takes under a minute; `GET /readyz` answers 503 until then. Keep a copy of `REFHIR_AUDIT_KEY`, which keys the audit trail that `GET /_audit/verify` checks. Until you turn on SMART sign-in the API needs no credentials, so secure it before you add a domain. Packages are not downloaded unless you add `REFHIR_PACKAGE_DOWNLOAD=true`, so installing another implementation guide answers 403 until you do, and it then needs internet access. A search without `_count` returns 100 matches and a `next` link. |
| **browserbay** | One service, `browserbay`, running `ghcr.io/degoke/browserbay:0.2.2` on port 8788: a relay and a Chromium node in one process. It reserves 0.5 CPU and 1 GB and may use up to 2 CPU and 4 GB. It gets a generated `BROWSERBAY_API_KEY`. | Add a domain and serve it over HTTPS before anything outside the cluster uses it. Your services present the API key to take a browser. It runs up to 5 browsers at once. A browser can open any address the service can reach, so give browser tokens only to services you trust. |

Geass shows these notes again in the dialog before you deploy and in the message after it.

### How the catalog is sized

A catalog entry reserves what the software needs to run. It may use up to a ceiling, which is either what it needs for a
short while, such as its first start (Infisical and browserbay), or room for more data that was not measured (ReFHIR).
The reserved amount is what the capacity check and your project limits count, so the entry does not hold memory it
needs only occasionally. A service that goes over what it reserved is the first to be stopped when its server runs out
of memory, and one that goes over what it may use is stopped at once (see [Apps](/docs/guides/apps/#scale)). Databases
always use what they reserve.

Each size comes from running the pinned image in Docker under a memory limit, with swap off unless a row says otherwise. The memory figures are the
container's working set, in MiB (1 GB in the dashboard is 1024 MiB). A "peak" also counts the file cache, which the
server gives back when it needs the memory.

| Entry | Reserved | May use up to | What was measured |
| --- | --- | --- | --- |
| **Infisical** | 0.5 CPU, 1 GB | 0.5 CPU, 2 GB | In Docker, 835 to 961 MiB once it has started and 1147 MiB at its first-start peak, ready after 84 seconds at 0.5 CPU. On K3s through Geass, 937 MiB at the peak and 804 MiB settled, with Geass showing it ready 102 seconds after the deploy, databases included. |
| **ReFHIR** | 0.25 CPU, 256 MB | 1 CPU, 1 GB | In Docker with Postgres, on a network with no internet access, the first start was ready after 23 seconds at 0.5 CPU and 33 seconds at 0.25 CPU, and peaked at 99 and 122 MiB. After 900 writes in transactions, validation against four profiles and a plain resource, six value set expansions (two more were refused, including `all-languages`, the largest, so it was not exercised) and 960 concurrent searches it had peaked at 107 MiB (122 MiB under a 256 MB limit, with no restart), and a restart was ready in 10 to 17 seconds. The data was about 900 resources, so raise the reserve if yours is much larger. The 1 GB ceiling is room for more data, not a figure that was measured. These runs used limits of 1 GB at 0.5 CPU and 256 MB at 0.25 CPU, not the entry's reserve and ceiling together, and left swap on, which did not matter because the peak stayed far below the limit. Version 0.0.2 was run in Docker only. Version 0.0.1 peaked at 3190 MiB on its first start in earlier Docker runs here (ReFHIR's own release notes give about 2.2 GB for their measurement) and was also run on K3s through Geass; ReFHIR's release notes explain what changed in 0.0.2. |
| **browserbay** | 0.5 CPU, 1 GB | 2 CPU, 4 GB | 220 to 520 MiB idle and 730 MiB with one page open. Five busy pages (Wikipedia, GitHub, BBC News and Hacker News) peaked at 1569 MiB at 2 CPU and 1202 MiB at 0.5 CPU, where the last page loaded after 37 seconds instead of 16. A 1 GB limit made one of the five pages time out. On K3s through Geass, 217 MiB idle, a peak of 1415 MiB with the five pages and 302 MiB after they closed. |

Starter examples, such as a database with a web UI or a function with a database, are not in the catalog. They are in
[Examples to copy](#examples-to-copy) below, and you can add any of them to **Your templates**.

## Deploy a template

1. On the **Templates** page, choose **Deploy** on a card.
2. In **Deploy** *template name*, choose the **Project**. The list also has **New project**, which creates a project with a
   `production` environment first. If the project has more than one environment, choose the **Environment**.
3. Choose **Add to project**.

Geass opens the project and shows a toast, "Added *template*". The new resources
are drafts with a **New** badge. Open them to check or change settings, then choose **Deploy changes** in the top bar.
See [Projects and environments](/docs/guides/projects-and-environments/#pending-changes-and-deploy-changes).

What Geass creates for each resource:

| Resource | Created as |
| --- | --- |
| A database | An in-cluster database of that engine, with the default size and volume (10 GiB for Postgres and MySQL, 1 GiB for SQLite) and backups turned on where the engine supports them. |
| A service | A draft with one replica, the size from the template (0.1 CPU and 128 MB if it names none, and no more than it reserves unless the template names a ceiling) and the port from the template (8080 if it names none). No domain. |
| A function | The same as a service, with the template's runtime, code, mode and schedules. The schedules start when you deploy the function, like any other change. |
| A file | A config file in the service's `/config` directory, such as `/config/config.yaml`. It also appears as a variable on the **Variables** tab, where you can edit it. |
| A literal variable | A service variable with that value. |
| A generated secret | A service secret with a new random value, 32 hexadecimal characters. Each deploy of the template makes a new one. |
| A database reference | An environment variable that reads the new database's connection string or one part of it. |

Every resource gets a generated ID such as `calm-river`, and the template's name for it as its display name. If creating any
resource fails, Geass removes the ones it already created, so a failed deploy leaves nothing behind.

A few things to know after you deploy a template:

- A service that references a database starts once the database is ready. Until then its pods wait with
  `CreateContainerConfigError`, which is expected, and the service says "Waiting for database *name*" without counting it as a
  failure. On a cluster that has never had a database, the first one takes a few minutes, because the Postgres operator is
  installed first.
- A template sets no health check, so Geass shows a service as ready once its pod runs. Infisical and ReFHIR can be shown
  ready before they answer: Infisical's first start takes minutes, and ReFHIR answers `GET /readyz` with 503 for 20 to 35
  seconds. Add a health check in the service's settings if
  something depends on it (see [Deploy settings](/docs/guides/apps/#deploy)).
- Backups are on by default for new Postgres, MySQL and SQLite databases, but they protect nothing until the project has
  backup storage. See [Backups and restore](/docs/guides/backups-and-restore/). Turn them off in the database's
  settings if you do not want them.
- The dashboard never shows secret values, so it cannot show a generated secret. Read it with `kubectl`, or replace it
  with a value you choose by adding a secret of the same name on the service's **Variables** tab:

```bash title="Read the generated Infisical encryption key"
kubectl -n geass-system get secret <infisical service ID>-secrets \
  -o jsonpath='{.data.ENCRYPTION_KEY}' | base64 -d
```

## Save your own template

Turn a project's environment into a template:

1. Open the project's **Settings** → **General**.
2. In **Generate Template from Project**, enter a **Template name** and optionally a **Description**. If the project has
   more than one environment, choose the **Environment**.
3. Choose **Generate template**. A **View in Templates** link appears.

Geass saves the **services, functions and in-cluster databases** of that environment. It keeps:

- for a service, its image, or its repository and branch, its port (when it is not 8080), and its variables: the plain
  variables (empty ones are dropped), references to databases in the same environment, and each secret as "generate a
  new secret". A config entry that is not a valid variable name, such as `config.yaml`, is kept as a file;
- for a function, its runtime, code, mode (`http` or `event`), [schedules](/docs/guides/functions/#schedules) and variables;
- for a database, its engine and version.

It does not keep sizes, replicas, domains, sleep-when-idle settings, health checks, build settings, shared variables, backup
settings or volume sizes, and it never copies a secret's value. Resources it cannot include are listed under the form: "Not included: …"
(buckets, logical databases, and databases outside the cluster, such as AWS RDS or PlanetScale). An environment with
nothing to save is refused: "this environment has no services or in-cluster databases to save".

Delete a saved template with the trash button on its card and confirm in **Delete template?**. Projects that already used
it are not affected. Catalog entries cannot be deleted.

:::caution
A repository service in a template takes its GitHub link from the project you deploy the template into, at the moment you
deploy it. Link the project to GitHub first (see [GitHub and builds](/docs/guides/github-and-builds/)). A service created
before the link has none and cannot be built. A repository service also needs the registry described there.
:::

### Write a template by hand

A saved template is a ConfigMap in `geass-system`. The [examples below](#examples-to-copy) are ready to apply. You can write one yourself, for example to keep templates in version
control. The ConfigMap must be named `template-<id>`, carry the label `geass.dev/template: "true"`, and hold the
template in the key `template.json`:

```yaml title="template-acme-stack.yaml"
apiVersion: v1
kind: ConfigMap
metadata:
  name: template-acme-stack
  namespace: geass-system
  labels:
    geass.dev/template: "true"
data:
  template.json: |
    {
      "id": "acme-stack",
      "name": "Acme stack",
      "description": "The storefront with its database.",
      "resources": [
        { "key": "database", "kind": "database", "name": "postgres", "engine": "Postgres" },
        {
          "key": "web", "kind": "service", "name": "storefront",
          "image": "ghcr.io/acme/storefront:1.4.0", "port": 3000,
          "variables": [
            { "name": "DATABASE_URL", "from": { "resource": "database", "key": "uri" } },
            { "name": "SESSION_SECRET", "generate": "secret" },
            { "name": "LOG_LEVEL", "value": "info" }
          ]
        }
      ]
    }
```

```bash
kubectl apply -f template-acme-stack.yaml
kubectl -n geass-system get configmaps -l geass.dev/template=true
```

The template appears under **Your templates**. The rules Geass checks:

| Field | Rule |
| --- | --- |
| `id` | Lowercase letters, digits and dashes. The ConfigMap name must be `template-` plus the `id`. It cannot reuse a catalog ID such as `gatus` or `infisical`. |
| `name`, `description`, `notes` | `name` is required, up to 80 characters. `description` is up to 400 and `notes` up to 800. `notes` is shown before and after a deploy. A resource's `name` is up to 63. |
| `resources` | 1 to 25. Each has a unique `key` of lowercase letters, digits and dashes, starting with a letter, up to 31 characters. |
| `kind` | `service`, `function` or `database`. |
| Service | Exactly one of `image` or `repository` (`owner/name`, with optional `branch`, default `main`). `port` 0 to 65535. |
| Function | `runtime` (`bun`, `node` or `python`) and `code`, within the [function limits](/docs/guides/functions/#limits). Optionally `mode` (`http`, the default, or `event`) and up to 8 `triggers`, each with a `name`, a `schedule` and optionally a `path` and a `payload`, under the rules in [Functions](/docs/guides/functions/#schedules). A service cannot have `runtime`, `code`, `mode` or `triggers`. |
| Database | `engine` of `Postgres`, `MySQL`, `Redis` or `SQLite`, and an optional `version`. A database has no `variables`, `size` or `files`. |
| `size` | On a service or function: `cpu` and `memory` are what it reserves, which the capacity check counts, and `cpuMax` and `memoryMax` are the most it may use while its server has room. Use at least one of the four. Each is one of the sizes the dashboard offers: `cpu` and `cpuMax` are `100m`, `250m`, `500m`, `1` or `2`, and `memory` and `memoryMax` are `128Mi`, `256Mi`, `512Mi`, `1Gi`, `2Gi` or `4Gi`. A `cpu` or `memory` you leave out is the default, 0.1 CPU or 128 MB. A ceiling you leave out is the reservation, so the service can never use more than it reserves, and a ceiling below the reservation is refused. See [Apps](/docs/guides/apps/#scale) for what a ceiling costs. |
| `files` | On a service only: up to 20 files, as `{ "name": "content" }`. A name uses letters, numbers, dots, dashes and underscores, starts with a letter or number, and is up to 128 characters. A file is up to 256 KB and cannot share a name with a variable. Files are written to `/config`. |
| `variables` | Up to 100 per resource, with names of letters, digits and underscores. Each has one of `value`, `generate` (only `"secret"`) or `from` (`resource` is a database `key` in the same template, `key` is one of `uri`, `host`, `port`, `username`, `password`, `database`). |
| Size | Geass refuses to save a template over 900 KB. A ConfigMap is itself limited to 1 MiB. |

A function resource looks like this. It goes in the `resources` list next to the others:

```json title="A function with a schedule, in template.json"
{
  "key": "report", "kind": "function", "name": "report",
  "runtime": "node", "mode": "event",
  "code": "export async function handler(event) { return { ok: true, trigger: event.trigger }; }",
  "triggers": [{ "name": "nightly", "schedule": "0 3 * * *" }]
}
```

## Examples to copy

These five are not in the catalog, but each is a complete template. Save the block to a file, apply it with
`kubectl apply -f`, and the template appears under **Your templates**. Change the image, the name or the variables first
if you like.

```yaml title="template-hello-function.yaml"
apiVersion: v1
kind: ConfigMap
metadata:
  name: template-hello-function
  namespace: geass-system
  labels:
    geass.dev/template: "true"
data:
  template.json: |
    {
      "id": "hello-function",
      "name": "Hello function",
      "description": "A Bun function that answers every request. The smallest thing you can deploy.",
      "resources": [
        {
          "key": "hello",
          "kind": "function",
          "name": "hello",
          "runtime": "bun",
          "code": "const port = Number(process.env.PORT) || 8080;\n\nBun.serve({\n  port,\n  fetch(request) {\n    return Response.json({ message: \"Hello from Geass\", url: request.url });\n  },\n});\n\nconsole.log(\"Listening on \" + port);\n"
        }
      ]
    }
```

```yaml title="template-echo-demo.yaml"
apiVersion: v1
kind: ConfigMap
metadata:
  name: template-echo-demo
  namespace: geass-system
  labels:
    geass.dev/template: "true"
data:
  template.json: |
    {
      "id": "echo-demo",
      "name": "HTTP echo",
      "description": "Echoes each request back with its headers and environment variables. Good for checking routing and variables.",
      "resources": [
        {
          "key": "echo",
          "kind": "service",
          "name": "echo",
          "image": "mendhak/http-https-echo:41",
          "port": 8080,
          "variables": [
            {
              "name": "ECHO_INCLUDE_ENV_VARS",
              "value": "1"
            },
            {
              "name": "APP_NAME",
              "value": "geass-demo"
            },
            {
              "name": "APP_ENV",
              "value": "staging"
            },
            {
              "name": "FEATURE_FLAG",
              "value": "true"
            }
          ]
        }
      ]
    }
```

```yaml title="template-postgres-adminer.yaml"
apiVersion: v1
kind: ConfigMap
metadata:
  name: template-postgres-adminer
  namespace: geass-system
  labels:
    geass.dev/template: "true"
data:
  template.json: |
    {
      "id": "postgres-adminer",
      "name": "Postgres with Adminer",
      "description": "A PostgreSQL database and the Adminer web UI, pre-pointed at it.",
      "resources": [
        {
          "key": "database",
          "kind": "database",
          "name": "postgres",
          "engine": "Postgres"
        },
        {
          "key": "adminer",
          "kind": "service",
          "name": "adminer",
          "image": "adminer:4",
          "port": 8080,
          "variables": [
            {
              "name": "ADMINER_DEFAULT_SERVER",
              "from": {
                "resource": "database",
                "key": "host"
              }
            }
          ]
        }
      ]
    }
```

```yaml title="template-function-postgres.yaml"
apiVersion: v1
kind: ConfigMap
metadata:
  name: template-function-postgres
  namespace: geass-system
  labels:
    geass.dev/template: "true"
data:
  template.json: |
    {
      "id": "function-postgres",
      "name": "Function with Postgres",
      "description": "A Bun function that queries a PostgreSQL database through DATABASE_URL.",
      "resources": [
        {
          "key": "database",
          "kind": "database",
          "name": "postgres",
          "engine": "Postgres"
        },
        {
          "key": "api",
          "kind": "function",
          "name": "api",
          "runtime": "bun",
          "code": "import { sql } from \"bun\";\n\nconst port = Number(process.env.PORT) || 8080;\n\nBun.serve({\n  port,\n  async fetch() {\n    try {\n      const [row] = await sql`select now() as now`;\n      return Response.json({ database: \"connected\", now: row.now });\n    } catch (error) {\n      return Response.json({ database: \"unavailable\", error: String(error) }, { status: 503 });\n    }\n  },\n});\n\nconsole.log(\"Listening on \" + port);\n",
          "variables": [
            {
              "name": "DATABASE_URL",
              "from": {
                "resource": "database",
                "key": "uri"
              }
            }
          ]
        }
      ]
    }
```

```yaml title="template-hasura-postgres.yaml"
apiVersion: v1
kind: ConfigMap
metadata:
  name: template-hasura-postgres
  namespace: geass-system
  labels:
    geass.dev/template: "true"
data:
  template.json: |
    {
      "id": "hasura-postgres",
      "name": "Hasura GraphQL",
      "description": "Hasura GraphQL Engine on a PostgreSQL database, with the console enabled and a generated admin secret.",
      "resources": [
        {
          "key": "database",
          "kind": "database",
          "name": "postgres",
          "engine": "Postgres"
        },
        {
          "key": "hasura",
          "kind": "service",
          "name": "hasura",
          "image": "hasura/graphql-engine:v2.40.0",
          "port": 8080,
          "variables": [
            {
              "name": "HASURA_GRAPHQL_DATABASE_URL",
              "from": {
                "resource": "database",
                "key": "uri"
              }
            },
            {
              "name": "HASURA_GRAPHQL_ENABLE_CONSOLE",
              "value": "true"
            },
            {
              "name": "HASURA_GRAPHQL_ADMIN_SECRET",
              "generate": "secret"
            }
          ]
        }
      ]
    }
```

A template that breaks a rule is not listed. Geass skips it and logs "Skipped invalid template".

## Through the API

All of these need a workspace admin token. See the [REST API](/docs/reference/rest-api/).

```bash title="List, deploy and delete"
curl -H "Authorization: Bearer $GEASS_TOKEN" https://geass.example.com/api/templates

curl -X POST https://geass.example.com/api/templates/infisical/deploy \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d project=proj-1a2b3c4d -d environment=staging

curl -X POST https://geass.example.com/api/templates/acme-stack/delete \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json"
```

Saving uses `POST /api/templates/create` with `project`, `environment`, `name` and `description`; the answer includes the
template and a `skipped` list.

## What happens under the hood

- The catalog is part of the operator binary. Saved templates are ConfigMaps named `template-<id>` in
  `geass-system`, labelled `geass.dev/template=true`, with the template in `template.json`. The ID of a saved template is
  the slug of its name plus four random characters.
- Deploying creates `GeassDatabase` objects first, then `GeassApp` objects, in `geass-system`, marked as drafts with the
  `geass.dev/deploy-workflow` annotation and a pending `created` change. A secret goes into `<service ID>-secrets`.
- A database reference becomes an `env` entry that reads a key of `<database ID>-connection`, the same as
  [Add reference](/docs/guides/environment-variables/#reference-a-database-or-bucket).

## Troubleshooting

**"project was not found" or "environment was not found in this project".** The project or environment in the request
does not exist. In the dialog, pick from the lists. Through the API, send the project's ID (`proj-…`), not its name.

**"template is not valid: …".** A hand-written template breaks a rule in the table above, or a function's schedule breaks a
[schedule rule](/docs/guides/functions/#schedules). The message names the resource and the field.

**My ConfigMap template is not listed.** Check the name is exactly `template-<id>` with the same `id` inside, the label is
set, and look for a "Skipped" line in `kubectl -n geass-system logs deploy/geass-controller-manager | grep -i template`.

**A service is stuck on `CreateContainerConfigError`.** It references the database's connection Secret, which exists
only once the database is deployed and ready. While it is on its way the service says "Waiting for database *name*". Deploy the
database and wait. If the service says `The service cannot start: app is CreateContainerConfigError` instead, the reference
is to a Secret or key that does not exist, and waiting will not help.

**A repository service from a template will not build.** The project was not linked to GitHub when you deployed the
template, so the service has no GitHub connection. Delete it, link the project, and deploy the template again.

**Saving fails with "this environment has no services or in-cluster databases to save".** Buckets, logical databases and
external databases are not included, and the environment has nothing else.

**Saving fails with "this environment is too large to save as a template".** The template would be over 900 KB, usually
because of function code.

**The new database is marked "Not backed up" or "Needs storage".** The card says "Not backed up" and the database's
**Backups** tab says "Needs storage". Backups are on and the project has no backup storage. See
[Backups and restore](/docs/guides/backups-and-restore/).

## Related

- [Projects and environments](/docs/guides/projects-and-environments/)
- [Environment variables and secrets](/docs/guides/environment-variables/)
- [Functions](/docs/guides/functions/)
- [Databases](/docs/guides/databases/)
- [GitHub and builds](/docs/guides/github-and-builds/)
