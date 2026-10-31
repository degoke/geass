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

Every change waits for a deploy. When you save a variable, Geass does not change a running service.

## Before you start

You need a service. See [Apps](/docs/guides/apps/) or [Functions](/docs/guides/functions/).

Only admins can read or change variables. A read-only user sees "Your viewer role cannot view or change variables." on
the tab. An `observer` API token can list the variables of a service, but it cannot change them. See
[Users and API tokens](/docs/guides/users-and-tokens/#roles).

## The four kinds

| Kind | Belongs to | Value shown in the dashboard |
| --- | --- | --- |
| **Variable** | One service | Masked, with a button to show it |
| **Secret** | One service | Never |
| **Reference** | One service | Never. It points at the connection details of a database or bucket. |
| **Shared** | One environment of a project | Names only |

## Add a variable or a secret

1. Open the service and select the **Variables** tab.
2. Click **New Variable**.
3. Enter the **Variable name** and the **Variable value**.
4. If the value must stay hidden, turn on **Secret**.
5. Click **Add**. A toast says "Variable saved. Deploy to apply it."
6. In the top bar, click **Deploy changes**.

Use letters, digits, and underscores in names. Do not start a name with a digit. Geass accepts other names, but the
service does not receive them.

Each row shows the name, a kind label, and the value, masked as `••••••••`. The kind labels are **Variable**,
**Secret**, **Reference**, and **Shared**. For a plain variable, the eye button shows or hides the value. The row menu
has **Edit** and **Delete**. To change a value, click **Edit**, enter the new value, and click **Save**.

You cannot read a secret back. To change a secret, add a secret with the same name and the new value. To remove a
secret, open the row menu and click **Delete**. The search icon filters the list by name.

### Import many at once

To edit all plain variables as text, do these steps:

1. Click **Raw Editor**.
2. Write one `KEY=value` on each line.
3. Click **Update variables**.

Geass skips blank lines and lines that start with `#`. Geass removes an `export ` prefix. You can put a value in single
or double quotes.

When you save, Geass replaces all plain variables of the service with the text. The editor does not show secrets and
references, and the save does not change them. A line without `=` gets the message
`Line "…" must look like KEY=value`.

## Reference a database or bucket

A reference puts the connection details of a database or bucket into the service. So you never copy a password.

1. On the **Variables** tab, click **Add Reference**. **Add Reference** is the link in the banner "Trying to connect a
   database?". The banner appears while the environment has a database or a bucket.
2. In **Add reference**, select the **Resource**. The list holds the databases and buckets of the same environment. A
   resource that is not ready shows greyed out, with the label "not ready".
3. Click **Add reference**. A toast says "Connection added. Deploy to apply it."
4. In the top bar, click **Deploy changes**.

On the canvas, a line now joins the service to the database or bucket.

The banner is the only way to open the dialog. After you dismiss the banner, your browser does not show it again. To
add a reference after that, use the API: `POST /api/apps/{id}/attach`, in the table under
[Through the API](#through-the-api).

Geass adds these variables for the resource that you select:

| You select | Variable | Holds |
| --- | --- | --- |
| A Postgres, MySQL, or SQLite database | `DATABASE_URL` | The connection URL |
| A Redis database | `REDIS_URL` | The connection URL |
| A bucket | `S3_ENDPOINT` | The endpoint |
| A bucket | `S3_ACCESS_KEY_ID` | The access key |
| A bucket | `S3_SECRET_ACCESS_KEY` | The secret key |
| A bucket | `S3_BUCKET` | The bucket name as stored, for an in-cluster bucket `<project ID>.<environment>.<name>` |

A second reference with the same variable name replaces the first. So two Postgres databases cannot both be
`DATABASE_URL`.

Geass never replaces a variable with that name that someone set by hand. **Add reference** refuses and names that
variable. Remove that variable first.

### Reference other keys

The connection details of a database also have the keys `host`, `port`, `username`, `password`, and `database`. A
bucket has the keys `endpoint`, `accessKey`, `secretKey`, and `bucket`. The dashboard and the command line add only the
connection URL. They have no option to reference a different key yet. See
[Reference other keys](#reference-other-keys-on-the-server) under Under the hood.

Geass does not put the value of one variable into another variable. Geass stores a value such as `${{DATABASE_URL}}`
as that text.

## Shared variables

Shared variables hold values that several services in one environment need, for example a log level.

### Add a shared variable

1. In the project, open **Settings** → **Shared Variables**.
2. In the **Variable environment** list, select the environment.
3. Enter a name and a value.
4. If the value is secret, turn on **Secret**.
5. Click **Add**.

The name uses uppercase letters, digits, and underscores, and does not start with a digit. Otherwise, Geass shows
"variable name must use uppercase letters, numbers, and underscores".

The list shows the name and the environment of each variable, never the values. **Remove** deletes a variable. To
change a value, add the variable again with the same name.

A new environment starts with no shared variables. The exception is a
[pull request environment](/docs/guides/pull-request-environments/). It copies the shared variables and secrets of its
base environment.

The placeholder of the value field reads `VALUE or ${{REF}}`. But Geass does not resolve references. Geass stores the
text as you enter it.

### Choose which services receive them

A service receives all shared variables of its environment, unless you select some:

1. On the **Variables** tab of the service, click **Shared Variable**. The button is disabled while the environment
   has no shared variables.
2. Select the variables for the service. Secret variables have the mark `(secret)`.
3. Click **Save**.
4. Deploy. Attached variables appear on the tab as **Shared** rows. **Detach** removes one.

A service with no attached variables receives all shared variables, but they do not appear on its tab.

:::caution
Do not use attachment to keep a plain shared value away from a service. A selection limits only the **secret** shared
variables. A service always receives every **non-secret** shared variable of its environment. To hide a value from some
services, make it a secret, or set it on the services that need it.
:::

## Where variables appear in the container

Variables and secrets are environment variables of the container. Geass also writes them as files:

- Service variables: `/config/<NAME>`.
- Service secrets: `/secrets/<NAME>`, read-only.

If a name is set in more than one place, the container gets the value with the highest priority:

1. A **reference**. This includes a shared variable that you attached.
2. A service **secret**, then a service **variable**.
3. A shared variable that you did not attach.

Geass sets two variables of its own on [functions](/docs/guides/functions/) only: `PORT`, and `GEASS_TRIGGER_TOKEN`
for a function in handler mode or with schedules. A variable with one of these names on the **Variables** tab does not
replace them.

## When a change takes effect

| What you change | When it reaches the service |
| --- | --- |
| A service variable, secret, or reference, or a shared variable that you attach or detach | After the next deploy. The service shows a **variables** pending change. |
| The value of a shared variable | After the service restarts. Geass does not show this change as pending. Click **Redeploy** on each service that uses the variable. |

**Deploy changes** in the top bar applies the pending changes of the whole environment. Each deploy restarts the
service. **Redeploy** restarts a service that has no pending change. See
[Pending changes and Deploy changes](/docs/guides/projects-and-environments/#pending-changes-and-deploy-changes).

## From the command line or an agent

```bash
geass env list web
geass env set web LOG_LEVEL=debug
geass env set web --secret STRIPE_KEY=sk_test_example
geass env unset web LOG_LEVEL
geass env set web LOG_LEVEL=info --deploy --wait
```

`geass env list` prints names and kinds, never values. A variable that you set is a pending change. Add `--deploy` to
deploy immediately. An agent uses the `list_variables`, `set_variable`, and `unset_variable` tools. See
[Command line](/docs/guides/command-line/#variables-and-secrets), [MCP server](/docs/guides/mcp-server/), and the
[CLI reference](/docs/reference/cli/).

## Through the API

All of these requests work with a workspace admin token. They take a form body, except the first, and answer with
JSON that includes `"ok": true`. A project token can set and delete variables and secrets, replace the variables, and
delete references for the services in its environment. It cannot change shared variables or add references.

| Action | Request |
| --- | --- |
| List the variables of a service | `GET /api/apps/{id}/variables`. It never returns secret values. |
| Set a variable | `POST /api/apps/{id}/config/set` with `key` and `value` |
| Delete a variable | `POST /api/apps/{id}/config/delete` with `key` |
| Replace all variables | `POST /api/apps/{id}/config/raw` with `configJSON`, a JSON object of strings |
| Set a secret | `POST /api/apps/{id}/secrets/set` with `key` and `value`. Both must have a value. |
| Delete a secret | `POST /api/apps/{id}/secrets/delete` with `key` |
| Delete a reference | `POST /api/apps/{id}/env/delete` with `key` |
| Add a reference | `POST /api/apps/{id}/attach` with `kind` (`database` or `object-store`) and `name` (the resource ID) |
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

## How secrets are kept

- The dashboard never receives the value of a secret. The **Variables** tab shows `••••••••`.
- Plain variables are not secret. An admin can read them in the dashboard and through the API. A project token of that
  environment can also read them.
- On a cluster that the installer built, Geass stores secrets encrypted. See [Security](/docs/operations/security/).
- [Templates](/docs/guides/templates/) never copy secret values. A saved template generates a new random value for
  each secret.

## Troubleshooting

**You saved a variable, but the service does not have it.** The variable is a pending change. Click
**Deploy changes**. For a new value of a shared variable, click **Redeploy**.

**Geass accepts the name, but the variable is missing.** The name is not a valid environment variable name, for
example a name that starts with a digit or contains `-` or `.`. Rename the variable.

**The status shows `The service cannot start: app is CreateContainerConfigError`.** The service reads a key that does
not exist. For a database reference, make sure that the database is deployed and ready. Geass creates its connection
details only then. For a bucket, the message `couldn't find key uri` means that a variable reads `uri`. A bucket has no
`uri`. Change the variable to `endpoint`, `accessKey`, `secretKey`, or `bucket`, and redeploy.

**The database or bucket is greyed out in Add reference.** It shows "not ready" until it is deployed. Deploy it and
wait for **Ready**.

**The Shared Variable button is disabled.** The environment has no shared variables yet. Add one under **Settings** →
**Shared Variables**, in the same environment as the service.

**A service still receives a shared variable that you detached or never attached.** A service always receives the
non-secret shared variables. See [Choose which services receive them](#choose-which-services-receive-them).

**Services restarted after you added the first shared variable.** The first shared variable of an environment, and the
first secret one, restart its services. Later changes to a value do not.

**Raw Editor refuses your text.** Each line that is not blank needs a `=`: `Line "…" must look like KEY=value`.

**You lost the value of a secret.** Geass cannot show it. Set the secret again.

**The wrong value wins.** See the priority order in
[Where variables appear in the container](#where-variables-appear-in-the-container).

## Under the hood

For a service `amber-fern` in the environment namespace `proj-1a2b3c4d-production`, Geass writes the plain variables
to the ConfigMap `amber-fern-config` and the secrets to the Secret `amber-fern-secret`. Shared variables go to
`geass-shared-variables` and `geass-shared-secrets` in the same namespace. The source of the secrets of a service is
`amber-fern-secrets` in `geass-system`.

```bash title="Read what a service receives"
kubectl -n proj-1a2b3c4d-production exec deploy/amber-fern -- printenv | sort
```

### Reference other keys on the server

With access to the cluster, add an `env` entry to the service, and then click **Redeploy** on the service:

```bash
kubectl -n geass-system edit geassapp amber-fern
```

```yaml title="Reference individual keys (excerpt)"
spec:
  env:
    - name: DB_HOST
      valueFrom:
        secretKeyRef:
          name: calm-river-connection
          key: host
```

The name is the database or bucket ID plus `-connection`. **Settings** → **General** of each resource shows its ID.

## Related

- [Apps](/docs/guides/apps/) and [Functions](/docs/guides/functions/)
- [Databases](/docs/guides/databases/) and [Object storage](/docs/guides/object-storage/)
- [Projects and environments](/docs/guides/projects-and-environments/)
- [Users and API tokens](/docs/guides/users-and-tokens/)
- [Security](/docs/operations/security/)
