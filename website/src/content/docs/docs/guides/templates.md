---
title: "Templates"
description: "Deploy self-hosted software from the catalog in one step, or save services and databases as a template. Learn what the catalog creates and how to write one."
---

A **template** is a named set of services, functions, and databases that Geass creates in a project in one step. Geass
ships a **catalog** of self-hosted software, such as Infisical, ReFHIR, and browserbay. You can deploy an entry from the
catalog with one click. You can also save a stack that you built as your own template, and then create the stack again
in another project. Templates do not run anything by themselves. A template adds **drafts** to your canvas, and then you
review the drafts and deploy them.

![The Templates page.](../../../../assets/docs/templates.webp)
*Screenshot with example data.*

A template is not a Helm chart. Geass has no Helm-based templates. A template is a list of Geass resources.

## Before you start

You must be an admin. A read-only user sees "Your viewer role cannot browse templates." on the **Templates** page.

## The Templates page

Click **Templates** in the left rail. The page has two groups:

- **Catalog**: the software that ships with Geass.
- **Your templates**: the templates that you saved from a project or wrote yourself. Until you save a template, this
  group shows "No saved templates yet".

Each card shows the name of the template, its description, and a list of its resources. For each resource, the list
shows the image, the database engine, or `Function · bun`. A saved template also has a trash button.

You can also open the same list from a project:

- Above the canvas, **From template** opens this page.
- **+ Add resource** → **Template** lists the templates in the add box. When you select a template there, Geass adds it
  to the current environment immediately. You do not select a **Project** or an **Environment**.

## The catalog

The catalog contains software that you would otherwise install and connect yourself. Each entry pins the image version
that it was tested with. When you deploy an entry, Geass creates drafts, so you can change anything before it runs.

| Entry | What it creates | After you deploy |
| --- | --- | --- |
| **Gatus** | One service, `gatus`, that runs `twinproduction/gatus:v5.37.0` on port 8080. The service has a `config.yaml` file that checks Gatus itself, and it sets `GATUS_CONFIG_PATH` to that file. | Add your own checks to the `config.yaml` variable on the **Variables** tab of the service. Then deploy the change. Gatus keeps results in memory, so the results reset when the service restarts. |
| **Infisical** | A Postgres database, a Redis database, and a service, `infisical`, that runs `infisical/infisical:v0.166.8` on port 8080. The service reserves 0.5 CPU and 1 GB of memory and can use up to 2 GB. It gets `DB_CONNECTION_URI` and `REDIS_URL` from the databases, and a generated `ENCRYPTION_KEY` and `AUTH_SECRET`. `SITE_URL` starts as `http://localhost:8080`. | Add a domain to the service, and set `SITE_URL` to the address of that domain. Keep a copy of `ENCRYPTION_KEY`. Without that key, nothing can read the stored secrets. The first start runs database migrations and takes a few minutes. |
| **ReFHIR** | A Postgres database and a service, `refhir`, that runs `ghcr.io/hai-hq/refhir:0.0.2` on port 8080. The service is a FHIR R4 server. It reserves 0.25 CPU and 256 MB and can use up to 1 CPU and 1 GB. It gets `REFHIR_POSTGRES_URL` from the database, and a generated `REFHIR_AUDIT_KEY`. | The FHIR packages ship inside the image, so the first start needs no internet access and takes less than a minute. Until the first start completes, `GET /readyz` answers 503. Keep a copy of `REFHIR_AUDIT_KEY`. That key is the key for the audit trail that `GET /_audit/verify` checks. Until you turn on SMART sign-in, the API needs no credentials. Secure the API before you add a domain. ReFHIR does not download packages unless you add `REFHIR_PACKAGE_DOWNLOAD=true`. Until you add that variable, an install of another implementation guide answers 403. After you add it, that install needs internet access. A search without `_count` returns 100 matches and a `next` link. |
| **browserbay** | One service, `browserbay`, that runs `ghcr.io/degoke/browserbay:0.2.2` on port 8788. The service is a relay and a Chromium node in one process. It reserves 0.5 CPU and 1 GB and can use up to 2 CPU and 4 GB. It gets a generated `BROWSERBAY_API_KEY`. | Add a domain and serve it over HTTPS before anything outside the cluster uses the service. To get a browser, your services present the API key. The service runs up to 5 browsers at the same time. A browser can open any address that the service can reach. Give browser tokens only to services that you trust. |

Geass shows these notes again in the dialog before you deploy, and in the message after the deploy.

### How the catalog is sized

A catalog entry reserves what the software needs to run. The entry can use more, up to a ceiling. The ceiling is one
of these amounts:

- What the software needs for a short time, for example its first start (Infisical and browserbay).
- Room for more data that was not measured (ReFHIR).

The capacity check and your project limits count the reserved amount. So the entry does not hold memory that it needs
only occasionally. When its server runs out of memory, a service that uses more than it reserved is the first to be
stopped. A service that uses more than its ceiling is stopped immediately. For more information, see
[Apps](/docs/guides/apps/#scale). Databases always use what they reserve.

Each size comes from a run of the pinned image in Docker under a memory limit. Swap was off unless a row says
otherwise. The memory figures are the working set of the container, in MiB. In the dashboard, 1 GB is 1024 MiB. A
"peak" also counts the file cache, which the server gives back when it needs the memory.

| Entry | Reserved | Can use up to | What was measured |
| --- | --- | --- | --- |
| **Infisical** | 0.5 CPU, 1 GB | 0.5 CPU, 2 GB | In Docker: 835 to 961 MiB after it started, and 1147 MiB at its first-start peak. It was ready after 84 seconds at 0.5 CPU. On K3s through Geass: 937 MiB at the peak and 804 MiB settled. Geass showed it ready 102 seconds after the deploy, databases included. |
| **ReFHIR** | 0.25 CPU, 256 MB | 1 CPU, 1 GB | In Docker with Postgres, on a network with no internet access, the first start was ready after 23 seconds at 0.5 CPU and 33 seconds at 0.25 CPU. Those starts peaked at 99 and 122 MiB. The load was 900 writes in transactions, validation against four profiles and a plain resource, six value set expansions, and 960 concurrent searches. ReFHIR refused two more value set expansions, including `all-languages`, the largest, so the test did not exercise that expansion. After the load, the peak was 107 MiB, and 122 MiB under a 256 MB limit, with no restart. A restart was ready in 10 to 17 seconds. The data was about 900 resources. If your data is much larger, raise the reserve. The 1 GB ceiling is room for more data, not a measured figure. These runs used limits of 1 GB at 0.5 CPU and 256 MB at 0.25 CPU, not the reserve and the ceiling of the entry together. The runs left swap on. Swap did not matter, because the peak stayed far below the limit. Version 0.0.2 ran in Docker only. Version 0.0.1 peaked at 3190 MiB on its first start in earlier Docker runs here. ReFHIR's own release notes give about 2.2 GB for their measurement. Version 0.0.1 also ran on K3s through Geass. The ReFHIR release notes explain what changed in 0.0.2. |
| **browserbay** | 0.5 CPU, 1 GB | 2 CPU, 4 GB | 220 to 520 MiB idle, and 730 MiB with one page open. Five busy pages (two Wikipedia pages, GitHub, BBC News, and Hacker News) peaked at 1569 MiB at 2 CPU and 1202 MiB at 0.5 CPU. At 0.5 CPU, the last page loaded after 37 seconds instead of 16. A 1 GB limit made one of the five pages time out. On K3s through Geass: 217 MiB idle, a peak of 1415 MiB with the five pages, and 302 MiB after they closed. |

Starter examples, such as a database with a web UI or a function with a database, are not in the catalog. They are in
[Examples to copy](#examples-to-copy) below. You can add any of them to **Your templates**.

## Deploy a template

1. On the **Templates** page, click **Deploy** on a card.
2. In **Deploy** *template name*, select the **Project**. The list also has **New project**, which creates a project
   with a `production` environment first.
3. If the project has more than one environment, select the **Environment**.
4. Click **Add to project**.

Geass opens the project and shows a toast, "Added *template*". The new resources are drafts with a **New** badge. Open
the drafts to check or change settings. Then click **Deploy changes** in the top bar. See
[Projects and environments](/docs/guides/projects-and-environments/#pending-changes-and-deploy-changes).

Geass creates each resource as follows:

| Resource | Created as |
| --- | --- |
| A database | An in-cluster database of that engine, with the default size and volume. The default volume is 10 GiB for Postgres and MySQL, and 1 GiB for SQLite. Backups are on where the engine supports them. |
| A service | A draft with one replica, the size from the template, and the port from the template. If the template names no size, the size is 0.1 CPU and 128 MB. Unless the template names a ceiling, the service uses no more than it reserves. If the template names no port, the port is 8080. No domain. |
| A function | The same as a service, with the runtime, code, mode, and schedules of the template. The schedules start when you deploy the function, like any other change. |
| A file | A config file in the `/config` directory of the service, for example `/config/config.yaml`. The file also appears as a variable on the **Variables** tab, where you can edit it. |
| A literal variable | A service variable with that value. |
| A generated secret | A service secret with a new random value of 32 hexadecimal characters. Each deploy of the template makes a new value. |
| A database reference | An environment variable that reads the connection string of the new database, or one part of it. |

Every resource gets a generated ID, such as `calm-river`. Its display name is the name that the template gives it. If
Geass cannot create a resource, Geass removes the resources that it already created. So a failed deploy leaves nothing
behind.

After you deploy a template, know these facts:

- A service that references a database starts when the database is ready. Until then, its pods wait with
  `CreateContainerConfigError`. This error is expected. The service shows "Waiting for database *name*" and does not
  count the wait as a failure. On a cluster that never had a database, the first database takes a few minutes, because
  the Postgres operator is installed first.
- A template sets no health check, so Geass shows a service as ready when its pod runs. Geass can show Infisical and
  ReFHIR as ready before they answer. The first start of Infisical takes minutes, and ReFHIR answers `GET /readyz` with
  503 for 20 to 35 seconds. If something depends on the service, add a health check in the settings of the service.
  See [Deploy settings](/docs/guides/apps/#deploy).
- Backups are on by default for new Postgres, MySQL, and SQLite databases. But the backups protect nothing until the
  project has backup storage. See [Backups and restore](/docs/guides/backups-and-restore/). If you do not want backups,
  turn them off in the settings of the database.
- The dashboard never shows secret values, so the dashboard cannot show a generated secret. To replace the secret with
  a value that you choose, add a secret of the same name on the **Variables** tab of the service. To read the secret,
  use `kubectl`:

```bash title="Read the generated Infisical encryption key"
kubectl -n geass-system get secret <infisical service ID>-secrets \
  -o jsonpath='{.data.ENCRYPTION_KEY}' | base64 -d
```

## Save your own template

To turn the environment of a project into a template:

1. Open **Settings** → **General** for the project.
2. In **Generate Template from Project**, enter a **Template name**. You can also enter a **Description**.
3. If the project has more than one environment, select the **Environment**.
4. Click **Generate template**. A **View in Templates** link appears.

Geass saves the **services, functions, and in-cluster databases** of that environment. Geass keeps these settings:

- For a service, Geass keeps the image, or the repository and branch. Geass keeps the port when the port is not 8080.
  Geass also keeps the variables: the plain variables except empty ones, references to databases in the same
  environment, and each secret as "generate a new secret". Geass keeps a config entry that is not a valid variable
  name, such as `config.yaml`, as a file.
- For a function, Geass keeps the runtime, code, mode (`http` or `event`),
  [schedules](/docs/guides/functions/#schedules), and variables.
- For a database, Geass keeps the engine and version.

Geass does not keep sizes, replicas, domains, sleep-when-idle settings, health checks, build settings, shared
variables, backup settings, or volume sizes. Geass never copies the value of a secret. Under the form, Geass lists the
resources that it cannot include: "Not included: …". These resources are buckets, logical databases, and databases
outside the cluster, such as AWS RDS or PlanetScale. If an environment has nothing to save, Geass refuses it with "this
environment has no services or in-cluster databases to save".

To delete a saved template, click the trash button on its card, and then confirm in **Delete template?**. Projects that
already used the template are not affected. You cannot delete catalog entries.

:::caution
Link the project to GitHub before you deploy a template that has a repository service. See
[GitHub and builds](/docs/guides/github-and-builds/). A repository service in a template takes its GitHub link from the
project that you deploy the template into, at the time of the deploy. A service that Geass created before the link has
no GitHub link, and Geass cannot build it. A repository service also needs the registry that GitHub and builds
describes.
:::

### Write a template by hand

A saved template is a ConfigMap in `geass-system`. The [examples below](#examples-to-copy) are ready to apply. You can
also write a template yourself, for example to keep templates in version control. The ConfigMap must have these
properties:

- The name is `template-<id>`.
- The label `geass.dev/template: "true"` is set.
- The key `template.json` holds the template.

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

The template appears under **Your templates**. Geass checks these rules:

| Field | Rule |
| --- | --- |
| `id` | Lowercase letters, digits, and dashes. The ConfigMap name must be `template-` plus the `id`. The `id` cannot reuse a catalog ID, such as `gatus` or `infisical`. |
| `name`, `description`, `notes` | `name` is required, up to 80 characters. `description` is up to 400 and `notes` up to 800. Geass shows `notes` before and after a deploy. The `name` of a resource is up to 63. |
| `resources` | 1 to 25. Each resource has a unique `key` of lowercase letters, digits, and dashes. The `key` starts with a letter and is up to 31 characters. |
| `kind` | `service`, `function`, or `database`. |
| Service | Exactly one of `image` or `repository`. A `repository` is `owner/name`, with an optional `branch`. The default `branch` is `main`. `port` is 0 to 65535. |
| Function | `runtime` (`bun`, `node`, or `python`) and `code`, within the [function limits](/docs/guides/functions/#limits). Optionally, `mode`: `http`, the default, or `event`. Optionally, up to 8 `triggers`. Each trigger has a `name`, a `schedule`, and optionally a `path` and a `payload`, under the rules in [Functions](/docs/guides/functions/#schedules). A service cannot have `runtime`, `code`, `mode`, or `triggers`. |
| Database | `engine` of `Postgres`, `MySQL`, `Redis`, or `SQLite`, and an optional `version`. A database has no `variables`, `size`, or `files`. |
| `size` | On a service or function. `cpu` and `memory` are what the resource reserves, which the capacity check counts. `cpuMax` and `memoryMax` are the most that it can use while its server has room. Use at least one of the four. Each value is one of the sizes that the dashboard offers. `cpu` and `cpuMax` are `100m`, `250m`, `500m`, `1`, or `2`. `memory` and `memoryMax` are `128Mi`, `256Mi`, `512Mi`, `1Gi`, `2Gi`, or `4Gi`. If you leave out `cpu` or `memory`, it gets the default: 0.1 CPU or 128 MB. If you leave out a ceiling, the ceiling is the reservation, so the service can never use more than it reserves. Geass refuses a ceiling below the reservation. To learn what a ceiling costs, see [Apps](/docs/guides/apps/#scale). |
| `files` | On a service only. Up to 20 files, as `{ "name": "content" }`. A name uses letters, numbers, dots, dashes, and underscores. A name starts with a letter or number and is up to 128 characters. A file is up to 256 KB and cannot share a name with a variable. Geass writes the files to `/config`. |
| `variables` | Up to 100 per resource, with names of letters, digits, and underscores. Each variable has one of `value`, `generate` (only `"secret"`), or `from`. In `from`, `resource` is a database `key` in the same template, and `key` is one of `uri`, `host`, `port`, `username`, `password`, or `database`. |
| Size | Geass refuses to save a template over 900 KB. A ConfigMap itself has a limit of 1 MiB. |

A function resource looks like this example. It goes in the `resources` list next to the other resources:

```json title="A function with a schedule, in template.json"
{
  "key": "report", "kind": "function", "name": "report",
  "runtime": "node", "mode": "event",
  "code": "export async function handler(event) { return { ok: true, trigger: event.trigger }; }",
  "triggers": [{ "name": "nightly", "schedule": "0 3 * * *" }]
}
```

## Examples to copy

These five templates are not in the catalog, but each one is a complete template. Save the block to a file, and apply
the file with `kubectl apply -f`. The template appears under **Your templates**. Before you apply the file, you can
change the image, the name, or the variables.

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

Geass does not list a template that breaks a rule. Geass skips the template and logs "Skipped invalid template".

## Through the API

All of these requests need a workspace admin token. See the [REST API](/docs/reference/rest-api/).

```bash title="List, deploy and delete"
curl -H "Authorization: Bearer $GEASS_TOKEN" https://geass.example.com/api/templates

curl -X POST https://geass.example.com/api/templates/infisical/deploy \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d project=proj-1a2b3c4d -d environment=staging

curl -X POST https://geass.example.com/api/templates/acme-stack/delete \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json"
```

To save a template, use `POST /api/templates/create` with `project`, `environment`, `name`, and `description`. The
answer includes the template and a `skipped` list.

## What happens under the hood

- The catalog is part of the operator binary. Saved templates are ConfigMaps in `geass-system`. Each one is named
  `template-<id>`, has the label `geass.dev/template=true`, and holds the template in `template.json`. The ID of a
  saved template is the slug of its name plus four random characters.
- A deploy creates `GeassDatabase` objects first, and then `GeassApp` objects, in `geass-system`. Geass marks these
  objects as drafts with the `geass.dev/deploy-workflow` annotation and a pending `created` change. A secret goes into
  `<service ID>-secrets`.
- A database reference becomes an `env` entry that reads a key of `<database ID>-connection`. The result is the same
  as [Add reference](/docs/guides/environment-variables/#reference-a-database-or-bucket).

## Troubleshooting

**"project was not found" or "environment was not found in this project".** The project or environment in the request
does not exist. In the dialog, select from the lists. Through the API, send the ID of the project (`proj-…`), not its
name.

**"template is not valid: …".** A hand-written template breaks a rule in the table above, or the schedule of a function
breaks a [schedule rule](/docs/guides/functions/#schedules). The message names the resource and the field.

**My ConfigMap template is not listed.** Do these checks:

- Make sure that the name is exactly `template-<id>`, with the same `id` inside.
- Make sure that the label is set.
- Look for a "Skipped" line in `kubectl -n geass-system logs deploy/geass-controller-manager | grep -i template`.

**A service is stuck on `CreateContainerConfigError`.** The service references the connection Secret of the database.
That Secret exists only after the database is deployed and ready. Until then, the service shows "Waiting for database
*name*". Deploy the database and wait. If the service shows `The service cannot start: app is CreateContainerConfigError`
instead, the reference is to a Secret or key that does not exist. In that case, waiting does not help.

**A repository service from a template does not build.** The project was not linked to GitHub when you deployed the
template, so the service has no GitHub connection. Delete the service, link the project, and deploy the template again.

**Saving fails with "this environment has no services or in-cluster databases to save".** Geass does not include
buckets, logical databases, and external databases, and the environment has nothing else.

**Saving fails with "this environment is too large to save as a template".** The template would be over 900 KB,
usually because of function code.

**The new database is marked "Not backed up" or "Needs storage".** The card shows "Not backed up", and the **Backups**
tab of the database shows "Needs storage". Backups are on, but the project has no backup storage. See
[Backups and restore](/docs/guides/backups-and-restore/).

## Related

- [Projects and environments](/docs/guides/projects-and-environments/)
- [Environment variables and secrets](/docs/guides/environment-variables/)
- [Functions](/docs/guides/functions/)
- [Databases](/docs/guides/databases/)
- [GitHub and builds](/docs/guides/github-and-builds/)
