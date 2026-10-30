---
title: "Apps"
description: "Run a container image or a GitHub repository as a service: create it, set its port, size, domain, and health check, deploy it, and delete it."
---

An **app** is a container that Geass keeps running for you. The dashboard also calls an app a **service**. You create a
service from a container image or from a GitHub repository. You give the service a port, a size, and optionally a
public domain. To run a small piece of code without an image, see [Functions](/docs/guides/functions/).

![An app's panel, docked beside the canvas, with tabs for Overview, Deployments, Variables, Metrics, Console, and Settings.](../../../../assets/docs/app-drawer.webp)
*Screenshot with example data.*

## Before you start

You need these items:

- A project and an environment for the service. See
  [Projects and environments](/docs/guides/projects-and-environments/).
- For a container image: the image reference. A private image also needs your registry login. See
  [Pull a private image](#pull-a-private-image).
- For a GitHub repository: a connected GitHub App, a Dockerfile in the repository, and a container registry that you
  control. Geass builds the image and pushes it to that registry. See [GitHub and builds](/docs/guides/github-and-builds/).
- Free capacity for the size that you select. See [Usage and capacity](/docs/guides/usage-and-capacity/).

## Create a service from a container image

1. Open the project.
2. Select the environment tab.
3. Click **+ Add resource**.
4. Select **Docker Image**.
5. Enter the image reference, for example `ghcr.io/acme/storefront:1.4.0` or `nginx:alpine`.
6. Press Enter, or click **Create**. Geass saves the service as a **draft** and opens its **Settings**. A draft runs
   nothing.
7. Under **Networking**, set **Port** to the port that your container listens on.
8. On the **Variables** tab, add variables.
9. If the service must be public, add a domain.
10. In the top bar, click **Deploy changes**. The status goes from **Draft** to **Setting up** to **Ready**.

You can also paste a reference that contains `/` or `:` into the first search box. Then select the **Deploy image …**
row.

An image reference can contain letters, digits, and `. _ / @ : -`. It starts with a letter or a digit and has no
spaces. Docker Hub short names, for example `nginx:alpine`, work. Geass does not look up the image when you save it. A
wrong name shows when you deploy, as an image that cannot be pulled.

Pin a tag or a digest (`:1.4.0`, `@sha256:…`), not `latest`. A server that already has an image with a moving tag, for
example `:stable`, keeps the old image. A pinned tag also gives meaning to the
[deployment history](#deployments-redeploy-and-rollback).

## Create a service from a GitHub repository

1. Click **+ Add resource**, and then select **GitHub Repository**.
2. If the project is not linked to GitHub yet, click **Connect GitHub** and follow the pages of GitHub. If the cluster
   has no GitHub App yet, the box tells you so and links to **Settings** → **Connectors**.
3. Select a repository from the list. Geass saves a draft and opens its **Settings**.
4. Under **Source** and **Build**, check **Branch** and **Dockerfile path**.
5. Click **Deploy changes**. Geass builds the image, pushes it to your registry, and runs it.

You can also paste `acme/storefront` or a GitHub link into the first search box. The draft follows the default branch
of the repository. If you paste a `/tree/` link, the draft follows the branch in the link. If you paste a bare name, the
draft follows `main`.

After the first deploy, a push to the branch starts a build. [GitHub and builds](/docs/guides/github-and-builds/) tells
you how the build works, where the image goes, and what to do when a build fails.

## The service panel

To open the panel of a service, click the service on the canvas. The title is the **display name**. To change it, click
the name, enter a new name, and press Enter. To cancel, press Escape. The display name is only a label. Geass also
generates a **service ID**, for example `amber-fern`. The service ID never changes. The command line and the API use it.

The header shows the status. When it applies, the header also shows **Redeploy** or **Retry**. The panel has these
tabs:

| Tab | What it shows |
| --- | --- |
| **Overview** | Project, environment, source, replicas (with the autoscaling ceiling), size, and endpoint. |
| **Deployments** | The live deployment and the history, with logs. |
| **Code** | Functions only: the code, its mode, and its schedules. See [Functions](/docs/guides/functions/). |
| **Variables** | Variables, secrets, references, and shared variables. See [Environment variables and secrets](/docs/guides/environment-variables/). |
| **Metrics** | Charts for the service. See [Observability](/docs/guides/observability/). |
| **Console** | A terminal in a running copy of the service. Admins only. See [Observability](/docs/guides/observability/). |
| **Settings** | All the settings in the next section. |

### Statuses

| Status | Meaning |
| --- | --- |
| **Draft** | The service was never deployed. Nothing runs. |
| **Setting up** | A deploy, a build, or a rollout is in progress, or the copies are starting. A sleeping service that a request wakes also shows this status. |
| **Ready** | All the replicas that you asked for are available. A service with 0 replicas counts as ready. A service that [sleeps when idle](/docs/guides/scale-to-zero/) and is asleep also counts as ready. |
| **Unavailable** | Geass cannot continue until something outside the service changes, for example free capacity. Or the container cannot start. |
| **Failed** | The rollout failed, or Geass stopped after repeated errors. **Retry** appears when Geass stopped. |
| **Deleting** | Geass is removing the service. |

## Settings

Open **Settings** on the service panel. **Settings** has a filter box at the top and a list of sections on the right.
To focus the filter box, press `/`. Each section has its own **Save changes** button. After you save, a toast tells you
the result:

- "Saved and applied".
- "Saved": the change waits for a deploy.
- "Saved and deployed".
- "Nothing to save".

A setting marked **Applies instantly** takes effect when you save, even for a deployed service. Every other change waits
until you click **Deploy changes**. See
[Pending changes and Deploy changes](/docs/guides/projects-and-environments/#pending-changes-and-deploy-changes).

### Source

This section shows what the service runs. Only the block that matches the source of the service appears.

| Setting | Meaning | Default | Applies |
| --- | --- | --- | --- |
| **Image** | The container image reference. | What you entered. | On deploy |
| **Repository** | The GitHub repository, as `owner/name`. | The repository that you selected. | On deploy |
| **Root directory** | The folder of the repository that the build runs in. See [GitHub and builds](/docs/guides/github-and-builds/#root-directory-and-watch-paths). | Empty | On deploy |
| **Branch** | The branch that Geass builds. After the first deploy, a push to the branch starts a build. See [Branch deploys and push triggers](/docs/guides/github-and-builds/#branch-deploys-and-push-triggers). | The default branch | On deploy |
| **Wait for CI** | Start a build only after all GitHub checks of the commit succeed. See [Wait for CI](/docs/guides/github-and-builds/#wait-for-ci). | Off | On deploy |
| **Runtime** | Functions only: **Bun (TypeScript)**, **Node.js**, or **Python**. | **Bun (TypeScript)** | On deploy |

### Networking

| Setting | Meaning | Default | Applies |
| --- | --- | --- | --- |
| **Custom domain** | The hostname that reaches the service, for example `shop.example.com`. Enter the name only, without `https://`. For no public address, leave it empty. | None | Instantly |
| **Port** | The port that the container listens on, 1 to 65535. | 8080 | On deploy |
| **HTTPS** | Serve the domain over HTTPS with a Let's Encrypt certificate. See [Domains and TLS](/docs/guides/domains-and-tls/#https-and-certificates). | Off | Instantly |
| **Verify DNS** | Check that the hostname resolves, and show the result under the URL. This check never holds the service back from **Ready**. | Off | Instantly |

After the service is ready, **Public networking** shows the URL with a link. **Private networking** shows the address
that other services in the project use, with a copy button. This address appears after the first deploy.

A service has one port, and no protocol setting. Public traffic is HTTP or HTTPS. Other services in the project can use
any TCP protocol on the private address. Geass does not publish raw TCP to the internet. Make the process listen on all
interfaces (`0.0.0.0`), not only on `localhost`.

[Domains and TLS](/docs/guides/domains-and-tls/) tells you how a domain reaches your service and how to fix DNS
problems.

### Scale

| Setting | Meaning | Default | Applies |
| --- | --- | --- | --- |
| **Replicas** | How many copies run. The form takes 1 or more. The API accepts 0 to 1000. | 1 | Instantly |
| **CPU reserved** and **Memory reserved** | What each copy sets aside for itself. The capacity check counts this amount. | 0.1 CPU, 128 MB | On deploy |
| **CPU can use up to** and **Memory can use up to** | The most that a copy can use while its server has room. | Same as reserved | On deploy |
| **Run on** | Use only servers with the tags that you select. This setting appears only when servers carry tags. See [Scaling and placement](/docs/guides/scaling-and-placement/). | None selected | On deploy |
| **Enable autoscaling** | Add copies when the average CPU use goes above the target. | Off | Instantly |
| **Scale up to** | The most copies that autoscaling can run. | 3 | Instantly |
| **CPU target %** | The average CPU use that starts scaling, 1 to 100. | 50 | Instantly |
| **Sleep when idle** | Stop the service when nobody uses it, and start it on the next request. Needs a public domain and the serverless add-on. It turns off **Enable autoscaling**, and the other way round. | Off | Instantly |
| **Sleep after (minutes)** | How long the service waits without a request before it stops, 1 to 1440. | 5 | Instantly |

CPU sizes are 0.1, 0.25, 0.5, 1, and 2. Memory sizes are 128 MB, 256 MB, 512 MB, 1 GB, 2 GB, and 4 GB. **can use up to**
offers the sizes from the reserved size upward.

By default, a copy cannot use more than it reserves. Select a higher **can use up to** for software that needs more
only for a short time, for example on its first start. The capacity check and the project limits count only what is
reserved. So the extra room is not guaranteed:

- A copy that goes over its memory ceiling is stopped and started again. A copy at its CPU ceiling is slowed down.
- When a server runs out of memory, Geass first stops the copies that use more than they reserve. Reserve at least
  what the service uses when it is quiet. With the monitoring add-on, the form tells you under **Memory reserved** when
  a copy typically used more.
- Autoscaling measures CPU against what is reserved. So a copy that reserves less scales out sooner.

When Geass stops a copy for memory, the status reads `Ran out of memory` for one hour. To fix the problem, raise
**Memory can use up to**. If the status says that the server ran short, raise **Memory reserved**.

With autoscaling on, **Replicas** is the minimum. While a service sleeps when idle, it runs from none up to 3 copies. If
the service cannot sleep, the block tells you why. See [Sleep when idle](/docs/guides/scale-to-zero/).

Before a deploy, Geass checks that the size times the replicas, or the autoscaling ceiling, fits in the free capacity.
See [Usage and capacity](/docs/guides/usage-and-capacity/).

### Build

Only repository services have build settings. For other services, the section says that there is nothing to build.

| Setting | Meaning | Default | Applies |
| --- | --- | --- | --- |
| **Dockerfile path** | The Dockerfile to build, relative to the root directory. | `Dockerfile` | On deploy |
| **Watch paths** | Gitignore-style rules, one on each line, that limit which pushes start a build. See [GitHub and builds](/docs/guides/github-and-builds/#root-directory-and-watch-paths). | Empty | Instantly |
| **Use build cache** | Use image layers again between builds. A change to this setting starts a new build. | Off | On deploy |

### Deploy

| Setting | Meaning | Default | Applies |
| --- | --- | --- | --- |
| **Command** | Replaces the entrypoint of the image. | The image default | On deploy |
| **Arguments** | Replaces the default arguments of the image. | The image default | On deploy |
| **Working directory** | The directory that the process starts in. | The image default | On deploy |
| **Path** (healthcheck) | An HTTP path that Geass calls on the service port to decide that a copy is ready. Empty means no check. | None | On deploy |
| **Policy** (restart) | Fixed at **Always**. The field is read-only. | Always | |
| **Enable metrics** | Expose Prometheus metrics from `/metrics` on the service port. Needs the monitoring add-on. | Off | Instantly |
| **Deploy hook** | A URL that deploys the service when a tool calls it. See [Users and API tokens](/docs/guides/users-and-tokens/#deploy-hooks). | None | |

**Command and arguments.** Geass splits both fields on spaces and does not read quotes. So an argument cannot contain a
space in these fields. For such an argument, use a manifest. See [Under the hood](#under-the-hood).

**Healthcheck.** Geass sends an HTTP GET to the path every 10 seconds, with a 2-second timeout. After 3 failures, the
copy gets no traffic, and a rolling deploy waits. Use a cheap path that returns a status below 400 when the service can
take requests. Without a check, a copy is ready when its container starts.

### General and Danger

**General** shows the project, the environment, and the **Service ID** with a copy button. **Danger** deletes the
service. See [Delete a service](#delete-a-service).

### Settings only available through the API

The dashboard has no form for these settings. Send them to `POST /api/apps/{id}/update`. See the
[REST API](/docs/reference/rest-api/).

| Field | Meaning |
| --- | --- |
| `livenessPath` and `livenessPort` | A liveness check. When it fails, Geass restarts the container. |
| `readinessPort`, `readinessPeriod`, `readinessTimeout`, `readinessFailureThreshold` | The details of the healthcheck. |
| `minReplicas` | The autoscaling floor, when it must differ from **Replicas**. |
| `scaleToZeroMax` | The most copies that a sleeping service runs when it is awake, 1 to 100. |
| `deploy=on` | Save and deploy in the same request. |

A service has no persistent disk. When a copy restarts, the files that it wrote are lost. Keep state in a
[database](/docs/guides/databases/) or a [bucket](/docs/guides/object-storage/).

## Pull a private image

The dashboard and the command line have no field for registry logins yet. So you cannot run a private image from the
dashboard. See [A registry login for a private image](#a-registry-login-for-a-private-image) under Under the hood.

A repository service does not need a login of its own. Geass gives it the login of the build registry. See
[GitHub and builds](/docs/guides/github-and-builds/#set-the-registry).

## Deployments, redeploy and rollback

**Deployments** lists every deploy. The first card is the live deploy. **History (n)** below it lists earlier deploys,
newest first. **Hide skipped** removes skipped entries. Each row has these items:

- A badge: **ACTIVE**, **FAILED**, **SKIPPED**, or **REMOVED** for an older deploy.
- A title, for example `Deploy ghcr.io/acme/storefront:1.4.0` or `Deploy acme/storefront@main`.
- The time when the deploy ran.

Geass keeps the 12 most recent deployments of a service.

To open the log panel, click **View logs**, or click a row. The log panel has four tabs:

- **Details**: the outcome, any errors, and the messages of the deploy.
- **Build Logs**: for a repository service, the output of the build. See
  [Build logs](/docs/guides/github-and-builds/#build-logs).
- **Deploy Logs**: the last 400 lines of every running copy, for the active deployment only.
- **Network Logs**: not available yet. The tab tells you so.

**Redeploy** restarts the copies with the saved settings. It is in the header when nothing is pending. A redeploy does
not rebuild a repository service. To build a newer commit, see
[Build a new commit](/docs/guides/github-and-builds/#build-a-new-commit).

**Rollback** has no button in the dashboard. To go back to an earlier deploy of an image service, use the command line:

```bash title="Roll back an image service"
geass apps deployments web
geass rollback web
```

A rollback sets the image back to the image of that deployment, restores its replica count, and redeploys. A rollback
does not restore variables or other settings. For a repository service, a rollback only restores the replica count. To
go back, revert the commit and build it. You cannot roll back a function. The message is
`functions cannot be rolled back; restore the earlier code and deploy again`.

## From the command line or an agent

Each action in the dashboard is also a `geass` command and an MCP tool. Drafts, pending changes, and deploys work the
same way as in the dashboard.

| To | Command | MCP tool |
| --- | --- | --- |
| Create a draft from an image, a repository, or a function | `geass apps create web -p proj-1a2b3c4d -e production --image nginx:alpine --port 80` | `create_app` (image or repository) |
| Deploy the pending changes, and wait | `geass deploy web --wait` | `deploy_app` |
| Read the state and replicas of a service | `geass status web` | `app_status` |
| Read its logs | `geass logs web` or `geass logs web -f` | `get_logs` |
| List its deployments | `geass apps deployments web` | `app_deployments` |
| Set a variable or a secret | `geass env set web LOG_LEVEL=debug` | `set_variable` |
| Set the replicas, or sleep when idle | `geass scale web 3`, `geass scale web --sleep-after 10m` | `scale_app`, `set_sleep_when_idle` |
| Go back to an earlier deploy | `geass rollback web` | `rollback_app` |
| Build the branch again | `geass build web --wait` | `rebuild_app` |
| Schedule a function | `geass functions triggers add report nightly "0 3 * * *"` | `set_function_trigger` |

A project token can do these actions for the services of its environment. See
[Users and API tokens](/docs/guides/users-and-tokens/), [Command line](/docs/guides/command-line/),
[MCP server](/docs/guides/mcp-server/), the [CLI reference](/docs/reference/cli/), and the
[MCP tools](/docs/reference/mcp-tools/).

## Delete a service

1. Open the service.
2. Select **Settings** → **Danger**. The section is named **Delete service**.
3. In the box, enter the service **ID**, not the display name.
4. Click **Delete service**.
5. Click **Delete permanently**.

You can also delete a service from the project. Select **Settings** → **Danger** → **Manage Services**, and then click
**Remove**.

When you delete a service, Geass removes the running copies, the domain, the autoscaling, and the function schedules.
Geass also removes the variables, secrets, builds, and deployment history of the service. A delete does not remove the
image from your registry. You cannot undo a delete.

## Troubleshooting

**The service stays a Draft after you save.** A save does not deploy a draft. In the top bar, click **Deploy changes**.

**The service stays in Setting up.** Open **Deployments** → **View logs** → **Details**. For a repository service, the
message `Git source is waiting for a successful build` means that the build has not finished. See
[GitHub and builds](/docs/guides/github-and-builds/#troubleshooting). Otherwise, the cluster might have no room for the
service. See [Usage and capacity](/docs/guides/usage-and-capacity/).

**The status is Unavailable with `The service cannot start: app is ImagePullBackOff`.** Geass cannot get the image.
The image name or tag is wrong, or the image is private. Check **Image** under **Source**. For a private image, see
[Pull a private image](#pull-a-private-image).

**The status is Unavailable with `The service cannot start: app is CrashLoopBackOff`.** The process exits. Read
**Deploy Logs**. These are common causes:

- A missing variable. Variables apply only after a deploy.
- A wrong **Command**.
- A process that listens on a port different from **Port**.

If the container keeps failing, the status becomes **Failed** after five failed checks or three minutes, and **Retry**
appears.

**The domain returns `502`, `503`, or "no available server".** No copy takes traffic. One of these problems is the
cause:

- **Port** does not match the port that the process listens on.
- The process listens only on `localhost`.
- The healthcheck fails.

**The domain returns `404 page not found`.** No route exists for the hostname. These are possible causes:

- The service is not deployed yet. Geass makes the route on deploy.
- **Custom domain** contains more than the hostname.
- The DNS record points to a different server.

See [Domains and TLS](/docs/guides/domains-and-tls/#troubleshooting).

**The status says "Ran out of memory".** A copy used more memory than it could. Select a higher
**Memory can use up to**, or reserve more, and then deploy. If the problem continues, look for a leak on the **Metrics**
tab.

**Deploy changes fails with "This service needs about … CPU and … memory".** The cluster does not have that much free
capacity. Select a smaller size, lower the replicas or the autoscaling ceiling, or add a server.

**The status says "Provisioning failed after repeated errors" and shows Retry.** Geass stopped after repeated errors.
Read the error in **Deployments** → **View logs** → **Details**. Fix the cause, and then click **Retry**.

**Enable metrics fails with "metrics are enabled but Prometheus Operator is not installed in this cluster".** Turn on
the monitoring add-on, or turn off metrics.

**A variable that you added is not in the container.** Variable changes wait for a deploy. See
[Environment variables and secrets](/docs/guides/environment-variables/).

**A change to Replicas or the domain took effect, but other changes did not.** Only the settings marked **Applies
instantly** take effect on save. The other settings wait for **Deploy changes**.

**The service has no running copies and the status is Ready.** The service is asleep. The next request to its public
domain starts it. See [Sleep when idle](/docs/guides/scale-to-zero/).

## Under the hood

A service is a `GeassApp` object in the `geass-system` namespace, named by the service ID. It runs as a Deployment,
Service, and Ingress with the same name in the namespace `<project ID>-<environment>`. For every field and condition,
see [GeassApp](/docs/reference/resources/geass-app/).

```bash title="Inspect a service"
kubectl -n geass-system get geassapp amber-fern -o yaml
kubectl -n proj-1a2b3c4d-production get deploy,pods,svc,ingress -l app.kubernetes.io/name=amber-fern
kubectl -n proj-1a2b3c4d-production logs -l app.kubernetes.io/name=amber-fern -c app --tail=100 --previous
```

### A registry login for a private image

With access to the cluster, these commands store your registry login in the environment and attach it to the service:

```bash title="Give a service a registry login"
kubectl -n proj-1a2b3c4d-production create secret docker-registry acme-registry \
  --docker-server=ghcr.io --docker-username=acme-bot --docker-password="$GHCR_TOKEN"

kubectl -n geass-system patch geassapp amber-fern --type merge \
  -p '{"spec":{"source":{"image":{"pullSecret":{"name":"acme-registry"}}}}}'
```

Replace `proj-1a2b3c4d` with the project ID, `production` with the environment, and `amber-fern` with the service ID.
Then click **Redeploy** on the service, or **Deploy changes** if the service has pending changes.

### Manifest and API

You can create a service as a `GeassApp` manifest. Such a service has no draft step. It runs when
`spec.deploy.enabled` is `true`, and later edits apply directly. A manifest also gives you settings that the API does
not have: `spec.ingress.rules` for more hosts and paths, `spec.build.command` and `spec.build.args` for arguments with
spaces, and `spec.logs.archiveStoreRef` to keep build logs.

```yaml title="storefront.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassApp
metadata:
  name: amber-fern
  namespace: geass-system
  annotations:
    geass.dev/display-name: storefront
spec:
  project: proj-1a2b3c4d
  environment: production
  source:
    image:
      image: ghcr.io/acme/storefront:1.4.0
  port: 3000
  replicas: 2
  ingress:
    host: shop.example.com
    tlsEnabled: true
  resources:
    requests: { cpu: 250m, memory: 256Mi }
    limits: { cpu: 250m, memory: 256Mi }
  deploy:
    enabled: true
    readinessProbe:
      httpGet: { path: /healthz, port: 3000 }
```

`spec.project` is the project ID, not its name. To edit a service made this way in the dashboard, first click
**Redeploy** on it. The dashboard forms need the copy that a dashboard deploy records.

Through the API, `POST /api/apps/create` makes a draft from `project`, `environment`, and `image`. For a repository, send
`source=git` with `repository` and `branch`. For a function, send `source=function` with `runtime`. Add `draft=on` to
get a generated ID. `POST /api/apps/{id}/deploy` deploys the draft. See the [REST API](/docs/reference/rest-api/).

## Related

- [Projects and environments](/docs/guides/projects-and-environments/)
- [Environment variables and secrets](/docs/guides/environment-variables/)
- [GitHub and builds](/docs/guides/github-and-builds/)
- [Domains and TLS](/docs/guides/domains-and-tls/)
- [Functions](/docs/guides/functions/)
- [Sleep when idle](/docs/guides/scale-to-zero/)
- [Command line](/docs/guides/command-line/) and [MCP server](/docs/guides/mcp-server/)
- [Scaling and placement](/docs/guides/scaling-and-placement/)
- [Observability](/docs/guides/observability/)
