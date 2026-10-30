---
title: "Templates"
description: "Deploy self-hosted software from the catalog in one step, or save services and databases as a template. Learn what the catalog creates and how to write one."
---

A **template** is a named set of services, functions, and databases that Geass creates in a project in one step. Geass
ships a **catalog** of self-hosted software, such as Infisical, ReFHIR, and browserbay. You can also save a stack that
you built as your own template, and create the stack again in another project. A template adds **drafts** to your
canvas. You review the drafts, and then you deploy them.

![The Templates page.](../../../../assets/docs/templates.webp)
*Screenshot with example data.*

## Before you start

You must be an admin. A read-only user sees "Your viewer role cannot browse templates." on the **Templates** page.

## The Templates page

Click **Templates** in the left rail. The page has two groups:

- **Catalog**: the software that ships with Geass.
- **Your templates**: the templates that you saved or wrote. Until you save one, this group shows "No saved templates
  yet".

Each card shows the name, the description, and the resources of the template. A saved template also has a trash
button.

You can also open the list from a project:

- Above the canvas, **From template** opens the **Templates** page.
- **+ Add resource** → **Template** lists the templates in the add box. When you select one there, Geass adds it to the
  current environment immediately.

## The catalog

Each catalog entry pins the image version that it was tested with. When you deploy an entry, Geass creates drafts, so
you can change anything before it runs.

| Entry | What it creates | After you deploy |
| --- | --- | --- |
| **Gatus** | One service, `gatus`, that runs `twinproduction/gatus:v5.37.0` on port 8080. It has a `config.yaml` file that checks Gatus itself. | Add your own checks to the `config.yaml` variable on the **Variables** tab, and deploy. Gatus keeps results in memory, so a restart clears them. |
| **Infisical** | A Postgres database, a Redis database, and a service, `infisical`, that runs `infisical/infisical:v0.166.8` on port 8080. The service gets the database URLs and a generated `ENCRYPTION_KEY` and `AUTH_SECRET`. `SITE_URL` starts as `http://localhost:8080`. | Add a domain, and set `SITE_URL` to its address. Keep a copy of `ENCRYPTION_KEY`. Without that key, nothing can read the stored secrets. The first start takes a few minutes. |
| **ReFHIR** | A Postgres database and a service, `refhir`, that runs `ghcr.io/hai-hq/refhir:0.0.2` on port 8080. The service is a FHIR R4 server. It gets the database URL and a generated `REFHIR_AUDIT_KEY`. | The first start needs no internet access and takes less than a minute. Until then, `GET /readyz` answers 503. Keep a copy of `REFHIR_AUDIT_KEY`, the key of the audit trail. The API needs no credentials until you turn on SMART sign-in. So secure the API before you add a domain. To install other implementation guides, add `REFHIR_PACKAGE_DOWNLOAD=true`. Without it, such an install answers 403. |
| **browserbay** | One service, `browserbay`, that runs `ghcr.io/degoke/browserbay:0.2.2` on port 8788. It is a relay and a Chromium node in one process. It gets a generated `BROWSERBAY_API_KEY`. | Add a domain with HTTPS before anything outside the cluster uses it. Your services present the API key to get a browser. It runs up to 5 browsers at the same time. A browser can open any address that the service can reach, so give tokens only to services that you trust. |

Geass shows these notes again before and after you deploy.

### How the catalog is sized

A catalog entry reserves what the software needs to run, and can use more, up to a ceiling. The capacity check and your
project limits count only the reserved amount. When a server runs out of memory, Geass first stops the services that
use more than they reserve. For more information, see [Apps](/docs/guides/apps/#scale).

| Entry | Reserved | Can use up to | Why |
| --- | --- | --- | --- |
| **Infisical** | 0.5 CPU, 1 GB | 0.5 CPU, 2 GB | It used 835 to 961 MiB after it started, and up to 1147 MiB on its first start. |
| **ReFHIR** | 0.25 CPU, 256 MB | 1 CPU, 1 GB | It peaked at 122 MiB under load with about 900 resources. The ceiling is room for more data, not a measured figure. If your data is much larger, raise the reserve. |
| **browserbay** | 0.5 CPU, 1 GB | 2 CPU, 4 GB | It used 220 to 520 MiB idle, and up to 1569 MiB with five busy pages. At 1 GB, one of five pages timed out. |

Each size comes from a run of the pinned image under a memory limit. Infisical and browserbay also ran on a real
cluster through Geass.

Starter examples, such as a function with a database, are not in the catalog. See
[Examples to copy](#examples-to-copy).

## Deploy a template

1. On the **Templates** page, click **Deploy** on a card.
2. In **Deploy** *template name*, select the **Project**. **New project** creates a project with a `production`
   environment first.
3. If the project has more than one environment, select the **Environment**.
4. Click **Add to project**.

Geass opens the project and shows a toast, "Added *template*". The new resources are drafts with a **New** badge. Open
the drafts to check or change their settings. Then click **Deploy changes** in the top bar. See
[Projects and environments](/docs/guides/projects-and-environments/#pending-changes-and-deploy-changes).

Geass creates each resource as follows:

| Resource | Created as |
| --- | --- |
| A database | An in-cluster database of that engine, with the default size and disk. The default disk is 10 GiB for Postgres and MySQL, and 1 GiB for SQLite. Backups are on where the engine supports them. |
| A service | A draft with one replica, and the size and port of the template. Without a size, it gets 0.1 CPU and 128 MB. Without a ceiling, it uses no more than it reserves. Without a port, it gets 8080. It has no domain. |
| A function | The same as a service, with the runtime, code, mode, and schedules of the template. |
| A file | A config file in `/config` of the service, for example `/config/config.yaml`. The file also appears as a variable on the **Variables** tab. |
| A literal variable | A service variable with that value. |
| A generated secret | A service secret with a new random value of 32 hexadecimal characters. Each deploy of the template makes a new value. |
| A database reference | A variable that reads the connection URL of the new database, or one part of it. |

Each resource gets a generated ID, such as `calm-river`, and the display name from the template. If Geass cannot create
a resource, it removes the resources that it already created. So a failed deploy leaves nothing behind.

After you deploy a template, know these facts:

- A service that uses a database starts when the database is ready. Until then, the service shows "Waiting for
  database *name*". This wait is not a failure. On a cluster that never had a database, the first one takes a few
  minutes.
- A template sets no healthcheck. So Geass can show Infisical and ReFHIR as ready before they answer. If something
  depends on the service, add a healthcheck. See [Deploy settings](/docs/guides/apps/#deploy).
- Backups are on by default for new Postgres, MySQL, and SQLite databases. They protect nothing until the project has
  backup storage. See [Backups and restore](/docs/guides/backups-and-restore/).
- To replace a generated secret with your own value, add a secret of the same name on the **Variables** tab.

The dashboard and the command line never show secret values. They have no option to read a generated secret, for
example the Infisical `ENCRYPTION_KEY`, yet. See [Read a generated secret](#read-a-generated-secret) under Under the
hood.

## Save your own template

To turn an environment of a project into a template, do these steps:

1. Open **Settings** → **General** of the project.
2. In **Generate Template from Project**, enter a **Template name**. You can also enter a **Description**.
3. If the project has more than one environment, select the **Environment**.
4. Click **Generate template**. A **View in Templates** link appears.

Geass saves the **services, functions, and in-cluster databases** of that environment, with these settings:

- For a service: the image, or the repository and branch, and the port when it is not 8080. Also the variables: plain
  variables that are not empty, references to databases in the same environment, and each secret as "generate a new
  secret".
- For a function: the runtime, code, mode, [schedules](/docs/guides/functions/#schedules), and variables.
- For a database: the engine and version.

Geass does not save sizes, replicas, domains, sleep settings, healthchecks, build settings, shared variables, backup
settings, or disk sizes. Geass never saves the value of a secret. Under the form, "Not included: …" lists what Geass
cannot save: buckets, logical databases, and databases outside the cluster.

To delete a saved template, click the trash button on its card, and confirm in **Delete template?**. Projects that
used the template do not change. You cannot delete catalog entries.

:::caution
Link the project to GitHub before you deploy a template with a repository service. See
[GitHub and builds](/docs/guides/github-and-builds/). The service takes its GitHub link from the project when you
deploy the template. A service created before the link cannot build. A repository service also needs a registry.
:::

### Write a template by hand

To keep a template in version control, write it yourself as a `template.json` file. The dashboard and the command
line have no option to add a hand-written template yet. See
[Add a hand-written template](#add-a-hand-written-template) under Under the hood.

```json title="template.json"
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

After you add the template, it appears under **Your templates**. Geass checks these rules:

| Field | Rule |
| --- | --- |
| `id` | Lowercase letters, digits, and dashes. The ConfigMap name is `template-` plus the `id`. The `id` cannot reuse a catalog ID, such as `gatus`. |
| `name`, `description`, `notes` | `name` is required, up to 80 characters. `description` is up to 400, and `notes` up to 800. Geass shows `notes` before and after a deploy. The `name` of a resource is up to 63. |
| `resources` | 1 to 25. Each has a unique `key` of lowercase letters, digits, and dashes. The `key` starts with a letter and has up to 31 characters. |
| `kind` | `service`, `function`, or `database`. |
| Service | Exactly one of `image` or `repository`. A `repository` is `owner/name`, with an optional `branch`. The default `branch` is `main`. `port` is 0 to 65535. |
| Function | `runtime` (`bun`, `node`, or `python`) and `code`, within the [function limits](/docs/guides/functions/#limits). An optional `mode`: `http` (the default) or `event`. Up to 8 `triggers`, each with `name`, `schedule`, and an optional `path` and `payload`. See [Functions](/docs/guides/functions/#schedules). |
| Database | `engine` of `Postgres`, `MySQL`, `Redis`, or `SQLite`, and an optional `version`. |
| `size` | On a service or function. `cpu` and `memory` are what it reserves. `cpuMax` and `memoryMax` are the most that it can use. `cpu` and `cpuMax` are `100m`, `250m`, `500m`, `1`, or `2`. `memory` and `memoryMax` are `128Mi`, `256Mi`, `512Mi`, `1Gi`, `2Gi`, or `4Gi`. A missing ceiling equals the reservation. Geass refuses a ceiling below the reservation. |
| `files` | On a service only. Up to 20 files, as `{ "name": "content" }`. A name has letters, numbers, dots, dashes, and underscores, up to 128 characters. A file is up to 256 KB and cannot share a name with a variable. |
| `variables` | Up to 100 for each resource. Each has one of `value`, `generate` (only `"secret"`), or `from`. In `from`, `resource` is a database `key` in the same template. `key` is `uri`, `host`, `port`, `username`, `password`, or `database`. |
| Size | Geass refuses a template over 900 KB. |

A function resource looks like this example:

```json title="A function with a schedule, in template.json"
{
  "key": "report", "kind": "function", "name": "report",
  "runtime": "node", "mode": "event",
  "code": "export async function handler(event) { return { ok: true, trigger: event.trigger }; }",
  "triggers": [{ "name": "nightly", "schedule": "0 3 * * *" }]
}
```

Geass does not list a template that breaks a rule.

## Through the API

All of these requests need a workspace admin token. See the [REST API](/docs/reference/rest-api/).

```bash title="List, deploy, and delete"
curl -H "Authorization: Bearer $GEASS_TOKEN" https://geass.example.com/api/templates

curl -X POST https://geass.example.com/api/templates/infisical/deploy \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d project=proj-1a2b3c4d -d environment=staging

curl -X POST https://geass.example.com/api/templates/acme-stack/delete \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json"
```

To save a template, use `POST /api/templates/create` with `project`, `environment`, `name`, and `description`. The
answer includes the template and a `skipped` list.

## Troubleshooting

**"project was not found" or "environment was not found in this project".** Select the project and environment from
the lists. Through the API, send the project ID (`proj-…`), not its name.

**"template is not valid: …".** A hand-written template breaks a rule in the table above. The message names the
resource and the field.

**A hand-written template is not listed.** Make sure that the name is exactly `template-<id>`, with the same `id`
inside, and that the label is set. Geass logs `Skipped invalid template` for a template that breaks a rule. See
[Under the hood](#under-the-hood).

**A service shows "Waiting for database *name*".** The database is not ready yet. Deploy the database and wait. If the
service shows `The service cannot start: app is CreateContainerConfigError` instead, it reads a key that does not
exist. Waiting does not help.

**A repository service from a template does not build.** The project was not linked to GitHub when you deployed the
template. Delete the service, link the project, and deploy the template again.

**Saving fails with "this environment has no services or in-cluster databases to save".** The environment has only
buckets, logical databases, or external databases.

**Saving fails with "this environment is too large to save as a template".** The template would be over 900 KB,
usually because of function code.

**The new database shows "Not backed up" or "Needs storage".** Backups are on, but the project has no backup storage.
See [Backups and restore](/docs/guides/backups-and-restore/).

## Under the hood

The catalog is part of Geass. Saved templates are ConfigMaps in `geass-system`, named `template-<id>` with the label
`geass.dev/template=true`. A saved template gets the slug of its name plus four random characters as its ID.

```bash title="List saved templates and skipped ones"
kubectl -n geass-system get configmaps -l geass.dev/template=true
kubectl -n geass-system logs deploy/geass-controller-manager | grep -i template
```

### Read a generated secret

With access to the cluster, this command reads a generated secret:

```bash title="Read the generated Infisical encryption key"
kubectl -n geass-system get secret <infisical service ID>-secrets \
  -o jsonpath='{.data.ENCRYPTION_KEY}' | base64 -d
```

### Add a hand-written template

A template is a ConfigMap in `geass-system` with these properties:

- The name is `template-<id>`.
- The label `geass.dev/template: "true"` is set.
- The key `template.json` holds the template.

This ConfigMap holds the `template.json` file from [Write a template by hand](#write-a-template-by-hand):

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

To add it, run this command on the first server:

```bash
sudo k3s kubectl apply -f template-acme-stack.yaml
```

### Examples to copy

These five templates are not in the catalog, but each one is complete. To add one, save the block to a file. Then,
with access to the cluster, run `kubectl apply -f <file>`. The template appears under **Your templates**. You can change
the image, the name, or the variables first.

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

## Related

- [Projects and environments](/docs/guides/projects-and-environments/)
- [Environment variables and secrets](/docs/guides/environment-variables/)
- [Functions](/docs/guides/functions/)
- [Databases](/docs/guides/databases/)
- [GitHub and builds](/docs/guides/github-and-builds/)
