---
title: "Environment variables and secrets"
description: "Configure a service with variables, secrets, database references, and shared variables, and learn when each change reaches the container."
---

A service reads its configuration from environment variables. Geass keeps them in three places:

- **Variables** and **secrets** belong to one service.
- **Shared variables** belong to an environment. Any service in that environment can receive them.
- **References** point at the connection details of a database or bucket.

You manage service variables on the **Variables** tab of a service. You manage shared variables under **Settings** →
**Shared Variables** of the project.

Every change waits for a deploy. When you save a variable, Geass does not change a running container.

## Before you start

You need a service. See [Apps](/docs/guides/apps/) or [Functions](/docs/guides/functions/).

Only admins can read or change variables. A read-only user sees "Your viewer role cannot view or change variables." on
the tab. The dashboard does not send variable values to that user. An `observer` API token reads but never changes
anything. An `observer` token can list the variables of a service through the API. See
[Users and API tokens](/docs/guides/users-and-tokens/#roles).

## The four kinds

| Kind | Belongs to | Value shown in the dashboard | Stored as |
| --- | --- | --- | --- |
| **Variable** | One service | Masked, with a button to show it | In the resource of the service (`spec.configData`) and in a ConfigMap |
| **Secret** | One service | Never | In a Kubernetes Secret |
| **Reference** | One service | Never (it points at a Secret) | An environment variable that reads one key of another Secret |
| **Shared** | One environment of a project | Names only | In the project resource, or in a Secret for secret ones |

## Add a variable or a secret

1. Open the service and select the **Variables** tab. The header shows the number of variables of the service.
2. Click **New Variable**.
3. Enter the **Variable name** and the **Variable value**. The example name is `VARIABLE_NAME`.
4. If the value must stay hidden, turn on **Secret**.
5. Click **Add**. A toast says "Variable saved. Deploy to apply it." The service now has a pending change.
6. In the top bar, click **Deploy changes**.

Use letters, digits, and underscores in names. Do not start a name with a digit. Geass checks only that the name is
not empty. But Kubernetes exposes only names that are valid environment variable names, and ignores the others.

Each row shows the name, a kind label, and the value, masked as `••••••••`. The kind labels are **Variable**,
**Secret**, **Reference**, and **Shared**. For a plain variable:

- The eye button shows or hides the value in your browser.
- The row menu has **Edit** and **Delete**. To use **Edit**, enter a new value and click **Save**.

You cannot read a secret back. To change a secret, add a secret with the same name and the new value. To remove a
secret, open the row menu and click **Delete**. The search icon filters the list by name.

### Import many at once

To edit all plain variables as text, do these steps:

1. Click **Raw Editor**.
2. Write one `KEY=value` on each line.
3. Click **Update variables**.

The editor reads the text as follows:

- Geass skips blank lines and lines that start with `#`.
- Geass removes an `export ` prefix.
- You can put a value in single or double quotes.

When you save, Geass replaces all plain variables of the service with the contents of the editor. The editor does not
show secrets and references, and the save does not change them. Geass refuses a line without `=`, with the message
`Line "…" must look like KEY=value`.

## Reference a database or bucket

A reference puts the connection string of a database into the service. Thus, you never copy a password.

1. On the **Variables** tab, click **Add Reference**. **Add Reference** is the link in the banner "Trying to connect a
   database?". The banner appears while the environment has at least one database or bucket.
2. In **Add reference**, select the **Resource**. The list holds the databases and buckets of the same project and
   environment. If a resource is not ready, the list shows it greyed out, with the label "not ready".
3. Click **Add reference**. A toast says "Connection added. Deploy to apply it."

The banner is the only way to open the dialog. After you dismiss the banner, this browser does not show it again
until someone clears the saved site data of the browser. In that case, add the reference through the API
(`POST /api/apps/{id}/attach`, later on this page) or in the resource.

Geass adds the variables that the table shows for the resource that you selected:

| You chose | Variable | Reads |
| --- | --- | --- |
| A Postgres, MySQL or SQLite database | `DATABASE_URL` | Key `uri` of the database's connection Secret |
| A Redis database | `REDIS_URL` | Key `uri` of the connection Secret |
| A bucket | `S3_ENDPOINT` | Key `endpoint` of the connection Secret |
| A bucket | `S3_ACCESS_KEY_ID` | Key `accessKey` |
| A bucket | `S3_SECRET_ACCESS_KEY` | Key `secretKey` |
| A bucket | `S3_BUCKET` | Key `bucket`, the name as stored (for an in-cluster bucket `<project ID>.<environment>.<name>`) |

If you add a reference that uses the same variable name, the new reference replaces the first one. Thus, two Postgres
databases cannot both be `DATABASE_URL`.

Geass never replaces a variable with that name that someone set by hand. That can be a plain environment variable or
a variable under **Variables**. **Add reference** refuses and names that variable. Remove that variable first.

On the canvas, a line now joins the service to the database.

The connection Secret of a bucket has the keys `endpoint`, `accessKey`, `secretKey`, and `bucket`. It has no `uri`.
Thus, a bucket gets one variable for each key, not a connection URL.

### Reference other keys

The connection Secret of a database, `<database ID>-connection`, has the keys `uri`, `host`, `port`, `username`,
`password`, and `database`. The dashboard adds the `uri` key of a database and the four keys of a bucket. To use a
different key of a database, add an `env` entry to the resource of the service, and then redeploy:

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

Then click **Redeploy** on the service. The Secret name is the database or bucket ID plus `-connection`. The
dashboard shows the IDs under **Settings** → **General** of each resource.

Geass does not put the value of one variable into a different variable. Geass stores a value such as
`${{DATABASE_URL}}` as that literal text.

## Shared variables

Shared variables hold values that several services in one environment need, for example a log level or a public API
key.

### Add a shared variable

1. In the project, open **Settings** → **Shared Variables**.
2. In the **Variable environment** list, select the environment.
3. Enter a name and a value.
4. If the value is secret, turn on **Secret**.
5. Click **Add**.

The name must use uppercase letters, digits, and underscores. It must not start with a digit. If it does not follow
these rules, Geass shows "variable name must use uppercase letters, numbers, and underscores".

The list shows the name and the environment of each variable. **Remove** deletes a variable. The list never shows
values. To change a value, add the variable again with the same name.

Shared variables are per environment. A new environment starts with no shared variables. A new environment does not
get variables from a different environment. The exception is a
[pull request environment](/docs/guides/pull-request-environments/), which copies the shared variables and secrets of
its base environment.

The placeholder of the value field reads `VALUE or ${{REF}}`. But Geass does not resolve references. Geass stores the
text as you enter it.

### Choose which services receive them

A service receives all shared variables of its environment, unless you select some:

1. On the **Variables** tab of the service, click **Shared Variable**. The button is disabled while the environment
   has no shared variables and the service has none attached.
2. Select the variables that the service must get. Secret variables have the mark `(secret)`.
3. Click **Save**.
4. Deploy. Attached variables appear on the tab as **Shared** rows. **Detach** removes one.

A service with no attached variables receives all shared variables of the environment. These variables do not appear
on its tab.

:::caution
Do not use attachment to keep a plain shared value away from a service. When you select variables, Geass limits only
the **secret** shared variables. Geass loads the **non-secret** shared variables of the environment as one block.
Thus, a service that attached one variable still receives the others. If only some services can see a value, put
that value in a secret, or in the variables of those services.
:::

## Where variables appear in the container

Variables and secrets are environment variables of the container. Geass also writes them as files:

| Source | Environment | Files |
| --- | --- | --- |
| Service variables | Yes | `/config/<NAME>` |
| Service secrets | Yes | `/secrets/<NAME>` (read-only) |
| Existing ConfigMaps listed in `spec.configMapRefs` | No | `/config/refs/<ConfigMap name>/` |
| Existing Secrets listed in `spec.secretRefs` | No | `/secrets/refs/<Secret name>/` |

If a name is set in more than one place, the container gets the value with the highest priority:

1. A **reference**. This includes a shared variable that you attached explicitly.
2. A service **secret**, then a service **variable**.
3. A shared variable that was not attached explicitly.

Geass adds two variables of its own, only to [functions](/docs/guides/functions/):

- `PORT`.
- `GEASS_TRIGGER_TOKEN`, for a function in handler mode or with schedules. This variable is a random secret. With it,
  the function can tell a scheduled call from a visitor.

A variable of the same name that you put in `spec.env` has priority over both. A name that you set on the
**Variables** tab does not. In that case, the container gets the value from Geass.

## When a change takes effect

| What you change | When it reaches the container |
| --- | --- |
| A service variable, secret, or reference, or a shared variable that you attach or detach | After the next deploy. The service shows a **variables** pending change. |
| The value of a shared variable | Geass updates the Secret or ConfigMap in the environment in seconds. But running pods keep the old value until they restart. Geass does not track the change as pending. Thus, click **Redeploy** on each service that uses the variable. |

**Deploy changes** in the top bar applies the pending variable changes of the whole environment. Each deploy restarts
the pods of a service. **Redeploy** on a service restarts it without a pending change. See
[Pending changes and Deploy changes](/docs/guides/projects-and-environments/#pending-changes-and-deploy-changes).

## From the command line or an agent

```bash
geass env list web
geass env set web LOG_LEVEL=debug
geass env set web --secret STRIPE_KEY=sk_test_example
geass env unset web LOG_LEVEL
geass env set web LOG_LEVEL=info --deploy --wait
```

`geass env list` prints names and kinds, never values. A variable that you set is a pending change, as in the
dashboard. The variable gets to the container when you deploy the service, or immediately with `--deploy`. An agent
uses the `list_variables`, `set_variable`, and `unset_variable` tools. See
[Command line](/docs/guides/command-line/#variables-and-secrets), [MCP server](/docs/guides/mcp-server/), and the
[CLI reference](/docs/reference/cli/).

## Through the API

All of these requests work with a workspace admin token. All requests other than the first take a form body. They
answer with JSON that includes `"ok": true`.

A project token can do these actions on the services in its own environment:

- Set and delete variables and secrets.
- Replace the variables.
- Delete references.

A project token cannot change shared variables or add references (`attach`).

| Action | Request |
| --- | --- |
| List the variables of a service | `GET /api/apps/{id}/variables`. It returns plain variable values, the names of secrets, the references, and the attached shared variables. It never returns secret values. |
| Set a variable | `POST /api/apps/{id}/config/set` with `key` and `value` |
| Delete a variable | `POST /api/apps/{id}/config/delete` with `key` |
| Replace all variables | `POST /api/apps/{id}/config/raw` with `configJSON` set to a JSON object of strings |
| Set a secret | `POST /api/apps/{id}/secrets/set` with `key` and `value`. Both must have a value. |
| Delete a secret | `POST /api/apps/{id}/secrets/delete` with `key` |
| Delete a reference | `POST /api/apps/{id}/env/delete` with `key` |
| Add a connection reference | `POST /api/apps/{id}/attach` with `kind` (`database` or `object-store`) and `name` (the resource ID) |
| Attach shared variables | `POST /api/apps/{id}/shared-variables/save` with one `sharedVariable` per name |
| Set a shared variable | `POST /api/projects/{project}/variables/save` with `environment`, `name`, `value`, and `secret=on` for a secret |
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

- The dashboard never receives the value of a secret. The variables endpoint lists only secret names. The project
  resource has no secret values. The **Variables** tab shows `••••••••`.
- Geass stores the secrets of a service in the Secret `<service ID>-secrets` in `geass-system`. When Geass reconciles
  the service, it copies that Secret into the namespace of the environment as `<service ID>-secret`. The pods read the
  secrets from there.
- Geass stores a secret shared variable in `<project ID>-shared-secrets` in `geass-system`, under the key
  `<environment>__<NAME>`. Geass copies it into `geass-shared-secrets` in the namespace of the environment.
- Plain variables are not secret. They are in the resource of the service and in a ConfigMap. An admin can read them
  in the dashboard and through the API. A project token of that environment can also read them.
- A Kubernetes Secret is base64-encoded in the API. The installer turns on K3s secrets encryption. Thus, on a cluster
  that the installer built, the Secret is encrypted in the cluster datastore. See
  [Security](/docs/operations/security/).

[Templates](/docs/guides/templates/) never copy secret values. When you save a project as a template, Geass changes
each secret into "generate a new random secret on deploy".

## What happens under the hood

For a service `amber-fern` in `proj-1a2b3c4d`, environment `production`, the operator renders:

- `ConfigMap/amber-fern-config` with the plain variables, loaded with `envFrom` and mounted at `/config`.
- `Secret/amber-fern-secret` with the secrets, loaded with `envFrom` and mounted at `/secrets`.
- `ConfigMap/geass-shared-variables` and `Secret/geass-shared-secrets`. The project controller writes them from the
  shared variables of the project for that environment.
- `env` entries for references and for shared variables that you attached.

Kubernetes gives an `env` entry priority over `envFrom`. Kubernetes gives a later `envFrom` source priority over an
earlier one. The order is the shared Secret and ConfigMap, then the service ConfigMap, then the service Secret.

```bash title="Inspect what a service receives"
kubectl -n proj-1a2b3c4d-production get configmap amber-fern-config -o yaml
kubectl -n proj-1a2b3c4d-production get secret amber-fern-secret -o jsonpath='{.data}' | head -c 200
kubectl -n proj-1a2b3c4d-production get configmap geass-shared-variables -o yaml
kubectl -n proj-1a2b3c4d-production exec deploy/amber-fern -- printenv | sort
```

## Troubleshooting

**I saved a variable but the container does not have it.** The variable is a pending change. Click
**Deploy changes**. For a new value of a shared variable, click **Redeploy**. The pending list names what is waiting.

**The name is accepted but the variable is missing.** Kubernetes ignores names that are not valid environment variable
names. Examples are names that start with a digit, or that contain `-` or `.`. Rename the variable.
`kubectl describe pod` shows a warning about invalid names.

**A pod fails with `CreateContainerConfigError`.** The pod references a Secret or key that does not exist. For
`couldn't find key uri in Secret …` with the Secret of a bucket, a variable of the service reads the key `uri`. The
connection Secret of a bucket has no `uri` key. **Add Reference** never adds such a variable. It adds the four bucket
variables in the [table above](#reference-a-database-or-bucket). Change the `env` entry to the key `endpoint`,
`accessKey`, `secretKey`, or `bucket`, and then redeploy. For a database reference, make sure that the database is deployed and ready.
Geass creates the connection Secret of a database when the database becomes ready.

**The database or bucket I want is greyed out in Add reference.** It shows "not ready" until its connection Secret
exists. Deploy it and wait for **Ready**.

**The Shared Variable button is disabled.** The environment has no shared variables yet. Add one under **Settings** →
**Shared Variables**, in the same environment as the service.

**A shared variable is still in a service after I detached or never attached it.** Geass loads non-secret shared
variables as one block. See the caution under [Choose which services receive them](#choose-which-services-receive-them).

**My services restarted on their own after I added a shared variable.** The first shared variable of an environment
changes how Geass builds the pods of the service. The first secret shared variable does this too. Thus, the pods can
roll. Later changes to a value do not restart them.

**Raw Editor refuses my text.** Each line that is not blank needs a `=`: `Line "…" must look like KEY=value`.

**I lost the value of a secret.** Geass cannot show it. Set the secret again with the correct value.

**The wrong value wins.** Check the priority order in
[Where variables appear in the container](#where-variables-appear-in-the-container). A reference has priority over a
service variable. A service variable has priority over an unattached shared variable.

## Related

- [Apps](/docs/guides/apps/) and [Functions](/docs/guides/functions/)
- [Databases](/docs/guides/databases/) and [Object storage](/docs/guides/object-storage/): what the connection Secrets hold.
- [Projects and environments](/docs/guides/projects-and-environments/)
- [Users and API tokens](/docs/guides/users-and-tokens/)
- [Security](/docs/operations/security/)
