---
title: "Environment variables and secrets"
description: "Configure a service with variables, secrets, database references and shared variables, and learn when each change reaches the container."
---

A service reads its configuration from environment variables. Geass keeps them in three places: **variables** and
**secrets** belong to one service, **shared variables** belong to an environment and any service in it can receive them,
and **references** point at the connection details of a database or bucket. You manage service variables on a
service's **Variables** tab, and shared variables under **Settings** → **Shared Variables** of the project.

Every change waits for a deploy. Saving a variable never changes a running container by itself.

## Before you start

You need a service. See [Apps](/docs/guides/apps/) or [Functions](/docs/guides/functions/). Only admins can read or
change variables. A read-only user sees "Your viewer role cannot view or change variables." on the tab, and the
dashboard does not send them variable values at all. An `observer` API token, which reads but never changes anything, can
list a service's variables through the API. See [Users and API tokens](/docs/guides/users-and-tokens/#roles).

## The four kinds

| Kind | Belongs to | Value shown in the dashboard | Stored as |
| --- | --- | --- | --- |
| **Variable** | One service | Masked, with a button to show it | In the service's resource (`spec.configData`) and in a ConfigMap |
| **Secret** | One service | Never | In a Kubernetes Secret |
| **Reference** | One service | Never (it points at a Secret) | An environment variable that reads one key of another Secret |
| **Shared** | One environment of a project | Names only | In the project resource, or in a Secret for secret ones |

## Add a variable or a secret

1. Open the service and choose the **Variables** tab. The header counts the service's variables.
2. Choose **New Variable**.
3. Type the **Variable name** (`VARIABLE_NAME` is the example) and the **Variable value**. Turn on **Secret** if the
   value must stay hidden. Choose **Add**.
4. A toast says "Variable saved. Deploy to apply it." The service now has a pending change, so choose **Deploy
   changes** in the top bar.

Use letters, digits and underscores in names, and do not start with a digit. Geass only checks that the name is not
empty, but Kubernetes only exposes names that are valid environment variable names, and ignores the others.

Each row shows the name, a kind label (**Variable**, **Secret**, **Reference** or **Shared**) and the value, masked
as `••••••••`. For a plain variable:

- The eye button shows or hides the value in your browser.
- The row menu has **Edit** (type a new value and choose **Save**) and **Delete**.

You cannot read a secret back. To change a secret, add a secret with the same name and the new value. To remove one,
open the row menu and choose **Delete**. The search icon filters the list by name.

### Import many at once

Choose **Raw Editor** to edit all plain variables as text. Write one `KEY=value` per line. Blank lines and lines that
start with `#` are skipped, an `export ` prefix is removed, and a value may be wrapped in single or double quotes.
Choose **Update variables**. Saving replaces every plain variable of the service with what is in the editor. Secrets and
references are not shown and are not affected. A line without `=` is refused: `Line "…" must look like KEY=value`.

## Reference a database or bucket

A reference puts a database's connection string into the service, so you never copy a password.

1. On the **Variables** tab, choose **Add Reference**. It is the link in the banner "Trying to connect a database?",
   which appears while the environment has at least one database or bucket. The banner is the only way to open the
   dialog. Once you dismiss it, this browser does not show it again until its saved site data is cleared. Add the
   reference through the API (`POST /api/apps/{id}/attach`, below) or in the resource instead.
2. Pick the **Resource** in **Add reference**. The list holds the databases and buckets of the same project and
   environment. One that is not ready yet is greyed out and labelled "not ready".
3. Choose **Add reference**. A toast says "Connection added. Deploy to apply it."

Geass adds the variables named by what you chose:

| You chose | Variable | Reads |
| --- | --- | --- |
| A Postgres, MySQL or SQLite database | `DATABASE_URL` | Key `uri` of the database's connection Secret |
| A Redis database | `REDIS_URL` | Key `uri` of the connection Secret |
| A bucket | `S3_ENDPOINT` | Key `endpoint` of the connection Secret |
| A bucket | `S3_ACCESS_KEY_ID` | Key `accessKey` |
| A bucket | `S3_SECRET_ACCESS_KEY` | Key `secretKey` |
| A bucket | `S3_BUCKET` | Key `bucket`, the name as stored (for an in-cluster bucket `<project ID>.<environment>.<name>`) |

Adding a reference that uses the same variable name replaces the first one, so two Postgres databases cannot both be
`DATABASE_URL`. A variable with that name that someone set by hand, a plain environment variable or one under
**Variables**, is never replaced: **Add reference** refuses and names it, so remove it first. On the canvas, a line now
joins the service to the database.

A bucket's connection Secret has the keys `endpoint`, `accessKey`, `secretKey` and `bucket`. It has no `uri`, so a bucket
gets one variable for each key instead of a connection URL.

### Reference other keys

A database's connection Secret `<database ID>-connection` has the keys `uri`, `host`, `port`, `username`, `password`
and `database`. The dashboard adds the `uri` key of a database and the four keys of a bucket. To use another key of a database, add an `env` entry to the
service's resource and redeploy:

```yaml title="Reference individual keys (excerpt of the GeassApp spec)"
spec:
  env:
    - name: DB_HOST
      valueFrom:
        secretKeyRef:
          name: calm-river-connection
          key: host
    - name: S3_ACCESS_KEY
      valueFrom:
        secretKeyRef:
          name: quiet-lake-connection
          key: accessKey
    - name: S3_SECRET_KEY
      valueFrom:
        secretKeyRef:
          name: quiet-lake-connection
          key: secretKey
```

```bash
kubectl -n geass-system edit geassapp amber-fern
```

Then choose **Redeploy** on the service. The Secret names are the database or bucket ID plus `-connection`; the IDs are
shown under **Settings** → **General** of each resource. There is no variable-to-variable interpolation: a value such as
`${{DATABASE_URL}}` is stored as that literal text.

## Shared variables

Shared variables hold values that several services in one environment need, such as a log level or a public API key.

### Add a shared variable

1. Open the project's **Settings** → **Shared Variables**.
2. Pick the environment in the **Variable environment** list.
3. Type a name and a value, turn on **Secret** if needed, and choose **Add**.

The name must be uppercase letters, digits and underscores, and not start with a digit ("variable name must use
uppercase letters, numbers, and underscores"). The list shows each variable's name and environment, and **Remove**
deletes it. It never shows values. To change a value, add the variable again with the same name.

Shared variables are per environment. A new environment starts with none, and nothing is inherited from another
environment, except that a [pull request environment](/docs/guides/pull-request-environments/) copies its base
environment's shared variables and secrets. The value field's placeholder reads `VALUE or ${{REF}}`, but Geass does not
resolve references: the text is stored as typed.

### Choose which services receive them

A service receives every shared variable of its environment, unless you choose some:

1. On the service's **Variables** tab, choose **Shared Variable**. The button is disabled while the environment has
   no shared variables and the service has none attached.
2. Tick the variables the service should get (secret ones are marked `(secret)`) and choose **Save**.
3. Deploy. Attached variables appear on the tab as **Shared** rows. **Detach** removes one.

A service with nothing attached receives all of the environment's shared variables, and they do not appear on its tab.

:::caution
Choosing variables narrows the **secret** shared variables only. The **non-secret** shared variables of the environment
are loaded as one block, so a service that attached one variable still receives the others. Do not rely on attaching to
keep a plain shared value away from a service. Put values that only some services may see in a secret, or in those
services' own variables.
:::

## Where variables appear in the container

Variables and secrets are environment variables of the container. Geass also writes them as files:

| Source | Environment | Files |
| --- | --- | --- |
| Service variables | Yes | `/config/<NAME>` |
| Service secrets | Yes | `/secrets/<NAME>` (read-only) |
| Existing ConfigMaps listed in `spec.configMapRefs` | No | `/config/refs/<ConfigMap name>/` |
| Existing Secrets listed in `spec.secretRefs` | No | `/secrets/refs/<Secret name>/` |

If one name is set in more than one place, the container gets the strongest one:

1. A **reference** (including a shared variable you attached explicitly).
2. A service **secret**, then a service **variable**.
3. A shared variable that was not attached explicitly.

Geass adds two variables of its own, both only to [functions](/docs/guides/functions/): `PORT`, and `GEASS_TRIGGER_TOKEN` for
a function in handler mode or with schedules. The second is a random secret that lets the function tell a scheduled call from
a visitor. A variable of the same name that you put in `spec.env` wins over both. A name you set on the **Variables** tab
does not: Geass's value is the one the container gets.

## When a change takes effect

| What you change | When it reaches the container |
| --- | --- |
| A service variable, secret or reference, or a shared variable you attach or detach | After the next deploy. The service shows a **variables** pending change. |
| The value of a shared variable | The Secret or ConfigMap in the environment is updated within seconds, but running pods keep the old value until they restart. The change is not tracked as pending, so choose **Redeploy** on each service that uses it. |

**Deploy changes** in the top bar applies the pending variable changes of the whole environment, and every deploy
restarts a service's pods. **Redeploy** on a service restarts it without any pending change. See
[Pending changes and Deploy changes](/docs/guides/projects-and-environments/#pending-changes-and-deploy-changes).

## From the command line or an agent

```bash
geass env list web
geass env set web LOG_LEVEL=debug
geass env set web --secret STRIPE_KEY=sk_test_example
geass env unset web LOG_LEVEL
geass env set web LOG_LEVEL=info --deploy --wait
```

`geass env list` prints names and kinds, never values. A variable you set is a pending change, as in the dashboard: it reaches
the container when the service is deployed, or at once with `--deploy`. An agent uses the `list_variables`, `set_variable` and
`unset_variable` tools. See [Command line](/docs/guides/command-line/#variables-and-secrets), [MCP server](/docs/guides/mcp-server/)
and the [CLI reference](/docs/reference/cli/).

## Through the API

All of these work with a workspace admin token. Apart from the first, they take a form body and answer with JSON that
includes `"ok": true`. A
project token can set and delete variables and secrets, replace the variables, and delete references of the services in
its own environment. It cannot change shared variables or add references (`attach`).

| Action | Request |
| --- | --- |
| List a service's variables | `GET /api/apps/{id}/variables`. It returns plain variable values, the names of secrets, the references and the attached shared variables. Secret values are never returned. |
| Set a variable | `POST /api/apps/{id}/config/set` with `key` and `value` |
| Delete a variable | `POST /api/apps/{id}/config/delete` with `key` |
| Replace all variables | `POST /api/apps/{id}/config/raw` with `configJSON` set to a JSON object of strings |
| Set a secret | `POST /api/apps/{id}/secrets/set` with `key` and `value` (neither may be empty) |
| Delete a secret | `POST /api/apps/{id}/secrets/delete` with `key` |
| Delete a reference | `POST /api/apps/{id}/env/delete` with `key` |
| Add a connection reference | `POST /api/apps/{id}/attach` with `kind` (`database` or `object-store`) and `name` (the resource ID) |
| Attach shared variables | `POST /api/apps/{id}/shared-variables/save` with one `sharedVariable` per name |
| Set a shared variable | `POST /api/projects/{project}/variables/save` with `environment`, `name`, `value` and `secret=on` for a secret |
| Delete a shared variable | `POST /api/projects/{project}/variables/delete` with `environment` and `name` |

```bash title="Set a variable and a secret, then deploy"
curl -X POST https://geass.example.com/api/apps/amber-fern/config/set \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d key=LOG_LEVEL -d value=info

curl -X POST https://geass.example.com/api/apps/amber-fern/secrets/set \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d key=STRIPE_KEY --data-urlencode value=sk_test_example

curl -X POST https://geass.example.com/api/apps/amber-fern/deploy \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json"
```

See the [REST API](/docs/reference/rest-api/) for authentication and errors.

## How secrets are stored and masked

- The dashboard never receives a secret's value. The variables endpoint lists secret names only, the project resource
  carries no secret values, and the **Variables** tab shows `••••••••`.
- A service's secrets are stored in the Secret `<service ID>-secrets` in `geass-system`. When it reconciles the service,
  Geass copies it into the environment's namespace as `<service ID>-secret`, and the pods read it from there.
- A secret shared variable is stored in `<project ID>-shared-secrets` in `geass-system` under the key
  `<environment>__<NAME>`, and copied into `geass-shared-secrets` in the environment's namespace.
- Plain variables are not secret. They sit in the service's resource and in a ConfigMap, and an admin can read them in the
  dashboard and through the API, as can a project token of that environment.
- A Kubernetes Secret is base64-encoded in the cluster datastore, not encrypted by Geass. See
  [Security](/docs/operations/security/).

[Templates](/docs/guides/templates/) never copy secret values: saving a project as a template turns each secret into
"generate a new random secret on deploy".

## What happens under the hood

For a service `amber-fern` in `proj-1a2b3c4d`, environment `production`, the operator renders:

- `ConfigMap/amber-fern-config` with the plain variables, loaded with `envFrom` and mounted at `/config`.
- `Secret/amber-fern-secret` with the secrets, loaded with `envFrom` and mounted at `/secrets`.
- `ConfigMap/geass-shared-variables` and `Secret/geass-shared-secrets`, written by the project controller from the
  project's shared variables for that environment.
- `env` entries for references and for shared variables you attached.

Kubernetes gives an `env` entry priority over `envFrom`, and a later `envFrom` source priority over an earlier one. The
order is the shared Secret and ConfigMap, then the service ConfigMap, then the service Secret.

```bash title="Inspect what a service receives"
kubectl -n proj-1a2b3c4d-production get configmap amber-fern-config -o yaml
kubectl -n proj-1a2b3c4d-production get secret amber-fern-secret -o jsonpath='{.data}' | head -c 200
kubectl -n proj-1a2b3c4d-production get configmap geass-shared-variables -o yaml
kubectl -n proj-1a2b3c4d-production exec deploy/amber-fern -- printenv | sort
```

## Troubleshooting

**I saved a variable but the container does not have it.** It is a pending change. Choose **Deploy changes**, or
**Redeploy** for a shared variable's new value. The pending list names what is waiting.

**The name is accepted but the variable is missing.** Kubernetes ignores names that are not valid environment variable
names, such as ones that start with a digit or contain `-` or `.`. Rename it. `kubectl describe pod` shows a warning
about invalid names.

**A pod fails with `CreateContainerConfigError`.** The pod references a Secret or key that does not exist. For
`couldn't find key uri in Secret …` after adding a bucket reference, see the caution
[above](#reference-a-database-or-bucket). For a database reference, check that the database is deployed and ready,
because its connection Secret is created when it becomes ready.

**The database or bucket I want is greyed out in Add reference.** It shows "not ready" until its connection Secret
exists. Deploy it and wait for **Ready**.

**The Shared Variable button is disabled.** The environment has no shared variables yet. Add one under **Settings** →
**Shared Variables**, in the same environment as the service.

**A shared variable is still in a service after I detached or never attached it.** Non-secret shared variables are
loaded as one block. See the caution under [Choose which services receive them](#choose-which-services-receive-them).

**My services restarted on their own after I added a shared variable.** The first shared variable (or the first
secret one) of an environment changes how the service's pods are built, so they can roll. Later changes to a value do
not restart them.

**Raw Editor refuses my text.** Every non-blank line needs a `=`: `Line "…" must look like KEY=value`.

**I lost a secret's value.** Geass cannot show it. Set the secret again with the correct value.

**The wrong value wins.** Check the precedence in [Where variables appear in the container](#where-variables-appear-in-the-container).
A reference beats a service variable, which beats an unattached shared variable.

## Related

- [Apps](/docs/guides/apps/) and [Functions](/docs/guides/functions/)
- [Databases](/docs/guides/databases/) and [Object storage](/docs/guides/object-storage/): what the connection Secrets hold.
- [Projects and environments](/docs/guides/projects-and-environments/)
- [Users and API tokens](/docs/guides/users-and-tokens/)
- [Security](/docs/operations/security/)
