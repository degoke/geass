---
title: "Deploy your first app"
description: "Go from a fresh install to a running app: create a project, add a public Docker image, set its port and variables, deploy, and find its URL."
---

This walkthrough deploys `mendhak/http-https-echo:41`. This small public image answers every request with a description of the request. The description includes the environment variables that you give the image. In this walkthrough, you do these tasks:

- Create a project.
- Add the image as a service.
- Set variables.
- Review the pending change.
- Deploy the service, and reach it.

The walkthrough uses only the dashboard and one `curl`. A last section shows the same steps with the `geass` command line.

![The project canvas after a deploy: each resource is a card with its status.](../../../../assets/docs/canvas.webp)
*Screenshot with example data.*

## Before you start

- Geass is [installed](/docs/getting-started/install/), and the cluster reports `READY=True`. To check, run `sudo k3s kubectl -n geass-system get geasscluster`.
- You can [sign in](/docs/getting-started/sign-in/) as an `admin`. A `viewer` sees no **New project** or **Add resource** buttons.
- For a URL that works from the internet, you need a hostname that points at the server, and port 80 must be open. Step 7 shows the options, and one of the options needs no domain.

## 1. Create a project

A project holds the services, databases, and buckets of one application. At the start, a project has one environment, `production`.

1. Open **Projects** in the left rail.
2. Click **New project**. If your nodes belong to two or more regions, a drop-down list is beside the button. You can select a region in it first. If your nodes do not, there is no choice to make.

Geass immediately creates the project with a generated name, such as `swift-river`, and an ID of the form `proj-1a2b3c4d`. Then Geass opens the project. The canvas says **No resources deployed**. To rename the project later, use **Settings → General** of the project.

## 2. Add the image

1. Click **+ Add resource** at the right of the project tabs (**Resources**, **Metrics**, **Logs**, and **Settings**). A search box opens with the prompt `Search, or paste a repo link or image name`.
2. Select **Docker Image**.
3. Enter `mendhak/http-https-echo:41`, and click **Create**.

You can also paste the image into the first box. Then Geass offers **Deploy image mendhak/http-https-echo:41** (`From a container registry`). With both methods, the box rejects references with spaces or unusual characters.

Geass saves the service as a draft, and shows the message `Added service`. Geass opens the panel of the service on **Settings**, docked on the right. Nothing runs yet:

- The service shows **Draft**.
- The canvas card shows `1 change`.
- The top bar shows **1 pending**.

:::tip
You can also keep this service as a template. The [HTTP echo example](/docs/guides/templates/#examples-to-copy) is a ready-made template. Apply it one time. Then, to add the image, variables, and port as a draft in any project, click **Deploy** on its card under **Templates**.
:::

## 3. Check the port

The echo image listens on 8080, and the Geass default port is also 8080. So you do not have to change the port. To see or change the port, do these steps:

1. In the service panel, open **Settings**, and find **Networking**. To go to it quickly, use **Filter settings…**.
2. **Port** is `8080`. If the port is different from the port that the container listens on, the ingress gives a bad gateway.

The defaults match the other settings of the demo:

- One replica (**Scale → Replicas**).
- The start command of the image. **Deploy → Custom start command** is empty.
- 0.1 CPU with 128 MB of memory for each replica (**Scale → Resources**).

## 4. Add the variables

1. Open the **Variables** tab.
2. Click **Raw Editor**, paste these lines, and click **Update variables**:

   ```text
   ECHO_INCLUDE_ENV_VARS=1
   APP_NAME=geass-demo
   APP_ENV=staging
   FEATURE_FLAG=true
   ```

   The Raw Editor takes one `KEY=value` on each line. It replaces all plain variables of the service. To add one variable at a time instead, click **New Variable**. Enter the **Variable name** and the **Variable value**, leave **Secret** off, and click **Add**.

3. The tab now shows `4 Service Variables`. The values are masked. To show them, click the eye icon.

Geass answers `Variables saved. Deploy to apply them.` Variables, like most settings, take effect on the next deploy.

Do not put real secrets in this demo. The image prints its environment in every response. For real secrets, turn on **Secret**. Then Geass stores the value as a Kubernetes Secret, and never shows the value again. For more information, see [Environment variables](/docs/guides/environment-variables/).

## 5. Review the pending change

The top bar now shows **2 pending**: one change for the creation of the draft, and one change for the variables. The number counts saved changes, not resources. If you added the four variables one at a time, the number would be 5. To see a list of what changed for each resource, click the number. The list shows the draft and the variables. Geass holds the changes to a service that was never deployed as pending. Nothing runs until you deploy.

Some settings never wait. These settings apply the moment that you save them, and the message says `Saved and applied`:

- A replica count.
- Autoscaling.
- Sleep when idle.
- The domain.
- HTTPS.
- Metrics.

A change to the container waits for a deploy, and the message says `Saved`. These are changes to the image, the port, the variables, the command, or the size.

## 6. Deploy

Click **Deploy changes** in the top bar. This button deploys every pending change in the current environment. Here, that is one service. **Deploy all** in the pending list does the same.

Geass checks that the cluster has room for the service, and records a deployment. Then Geass tells the operator to create the service. You get the message `Deployed 1 change`, and you return to the canvas. On the canvas, the card goes from **Draft** to **Setting up** to **Ready**. The first deploy pulls the image from Docker Hub, so it takes longer than later deploys.

To watch the deploy, open the service and its **Deployments** tab. The newest deployment has an **ACTIVE** badge. To read the container output, click **View logs**.

## 7. Find the URL

The **Deployments** tab shows the address of the service at the top, as a link. If there is no address, the tab shows one of these messages: `Not deployed yet`, `Service is starting`, `Service is offline`, or `No public domain`. The same address is in **Settings → Networking → Public networking**. A service has a public URL only when it has a hostname. A new service has no hostname unless Cloudflare is connected. Use one of these methods.

### With a domain you own

1. Create an `A` record for a name, such as `echo.example.com`, that points at the public IP of your server.
2. In **Settings → Networking** of the service, enter `echo.example.com` in **Custom domain**, and click **Save changes**. The change applies immediately.
3. Open `http://echo.example.com/`.

For this walkthrough, leave **HTTPS** off, unless you gave the installer a `GEASS_ACME_EMAIL`. These facts apply when **HTTPS** is on:

- The Ingress answers only on HTTPS.
- cert-manager asks Let's Encrypt for a certificate from the `letsencrypt-prod` issuer.
- Geass creates that issuer only when it has a contact email.
- Let's Encrypt must reach 80/tcp on your server to validate the name.
- Until the certificate exists, Traefik serves its own certificate, and the browser shows a warning.

For more information, see [HTTPS certificates](/docs/getting-started/install/#https-certificates) and [Domains and TLS](/docs/guides/domains-and-tls/). **Verify DNS** only makes Geass check that the name resolves before Geass calls the domain ready.

### With Cloudflare connected

If Cloudflare is connected and ready under **Settings → Connectors**, Geass automatically gives a hostname under your domain to a service that has no hostname. For the `production` environment, the hostname is `<service-id>-<project-id>.<your domain>`, over HTTP. Geass creates the DNS records for service hostnames only in Cloudflare Tunnel mode. So in the DNS-to-cluster mode, make sure that a record covers the name, for example a wildcard record. For more information, see [Connect a domain](/docs/getting-started/connect-domain/).

### Without any domain

Reach the service from the server itself. Its Kubernetes namespace is `<project-id>-production`. The name of its Service is the ID of the service, a generated name such as `quiet-harbor`. The **General** section of **Settings** of the service has the **Service ID**. After the service is deployed, **Networking → Private networking** has the full address.

```bash
sudo k3s kubectl -n proj-1a2b3c4d-production port-forward svc/quiet-harbor 8080:8080
```

In another terminal on the server, run `curl -s http://localhost:8080/`.

With each method, the response describes your request. The response includes an `env` object that lists `APP_NAME`, `APP_ENV`, `FEATURE_FLAG`, and `ECHO_INCLUDE_ENV_VARS`.

## 8. Change something

1. In **Settings** of the service, open the **Scale** section. Set **Replicas** to `2`, and click **Save changes**. The change applies immediately (`Saved and applied`), because it does not replace the running container. The top bar stays at **Up to date**.
2. In **Variables**, change `APP_ENV` to `production`. Use **Edit** in the menu of the row. The top bar shows **1 pending**. The running container still reports `staging`.
3. Click **Deploy changes**. The new container starts with the new value. When nothing is pending, the service header has a **Redeploy** button. This button deploys the service again.

## 9. Clean up

1. In **Settings** of the service, go to **Delete service**.
2. Enter the service ID.
3. Click **Delete service**.
4. To confirm, click **Delete permanently**.

Geass removes the Deployment, the Service, and the Ingress that it created. Geass also removes the variables of the service and its deployment history. To also remove the project, use the **Danger** section of **Settings** of the project, and enter the name of the project. For what outlives a deleted resource, see [Uninstall Geass](/docs/operations/uninstall/).

## Do the same from the command line (optional)

You can do all of the steps above with the `geass` command line. The command line calls the same dashboard through its `/api/v1` API. You need these items:

- The command line, installed and signed in as an admin. [Install Geass](/docs/getting-started/install/#install-the-command-line-optional) and [Sign in to the dashboard](/docs/getting-started/sign-in/#sign-in-from-the-command-line) tell how.
- The project from step 1. `geass projects list` prints its ID, such as `proj-1a2b3c4d`.

```bash
# Steps 2 and 3: add the image as a draft. The name you give becomes the service's ID.
geass apps create echo -p proj-1a2b3c4d -e production --image mendhak/http-https-echo:41 --port 8080

# Step 4: the variables, saved as a pending change.
geass env set echo ECHO_INCLUDE_ENV_VARS=1 APP_NAME=geass-demo APP_ENV=staging FEATURE_FLAG=true

# Steps 5 and 6: deploy, and wait until the service is ready.
geass deploy echo --wait

# Step 7: see the state, replicas and URL.
geass status echo

# Step 8: more replicas apply at once; a changed variable waits for a deploy.
geass scale echo 2
geass env set echo APP_ENV=production --deploy --wait

# Read the latest container output, then watch it until you press Ctrl-C.
geass logs echo --tail 20
geass logs echo -f

# Step 9: clean up. It asks first; a script has to add --yes.
geass apps delete echo --yes
```

`geass apps create` saves a draft, the same as **Add resource**. Nothing runs until `geass deploy`.

`geass deploy echo --wait` prints a line on standard error each time the service changes. At the end, it prints a status table. It exits with one of these codes:

- `0` when the service is ready.
- `5` if the deploy failed. The command says why.
- `6` if the service was not ready within 10 minutes. `--timeout` changes that time.

`geass status` shows `-` as the URL when the service has no hostname. To give the service a hostname, use step 7. You can also use the `port-forward` in the same step. With the command line, the Service is named `echo`.

`geass logs` needs a **Full access** token or a **Read only, with logs** token. `geass apps create echo ... --deploy` saves and deploys in one command. A plain create followed by `geass deploy` does the same in two commands.

For the other commands, see [Command line](/docs/guides/command-line/). For every flag, see [CLI commands](/docs/reference/cli/).

## What happens under the hood

A service is a `GeassApp` object in `geass-system`. When you added the service, Geass created the object as a draft with `deploy.enabled: false`. Geass annotated the object with `geass.dev/deploy-workflow: "true"`, and gave it a pending change of the kind `created`.

**Deploy changes** does these things:

- It sets `deploy.enabled: true`.
- It saves a snapshot of the spec in the `geass.dev/last-deployed` annotation.
- It clears the pending markers.
- It records a `GeassDeployment`.

The operator reconciles from that snapshot. It creates a Deployment and a Service of the same name in the namespace `<project-id>-production`. After the service has a hostname, the operator also creates an Ingress. The operator puts the settings that apply immediately from the live spec on top of the snapshot.

```bash
sudo k3s kubectl -n geass-system get geassapps
sudo k3s kubectl -n proj-1a2b3c4d-production get deploy,svc,ingress,pods
```

## Troubleshooting

**The message `Nothing was deployed`.** The top bar is on a different environment from the one that you edited. Switch to the environment tab that holds the service.

**`This service needs about ... CPU and ... memory. The cluster currently has ... available. Scale up the cluster before creating it`.** The nodes have no room for even a 0.1 CPU service. The add-ons that Geass installs also use CPU and memory. See **Usage** in the left rail and [Usage and capacity](/docs/guides/usage-and-capacity/).

**The service stays on `Setting up` or shows `Unavailable`.** Open **Deployments → View logs**. A message that says that the project or the cluster is not ready means that the cluster is still installing its add-ons. If the image cannot be pulled, check `sudo k3s kubectl -n <project-id>-production describe pod` for `ImagePullBackOff`. The server needs outbound access to the registry.

**The status is `Failed` and the service header shows Retry.** Provisioning failed after several attempts. Fix the cause that the message names, and then click **Retry**.

**The URL shows `404 page not found`.** Traefik has no route for that hostname. Check **Custom domain** for typos. Also make sure that the request uses the same host name, not the bare IP address.

**The URL shows `Bad Gateway`.** The ingress cannot reach the container. Set **Port** to the port that the image listens on, and deploy.

**The URL does not respond from the internet.** Port 80 is closed in the firewall of the provider or in the host firewall, or the DNS record points to a different address. The installer does not open port 80.

**The browser warns about the certificate after you turned HTTPS on.** No certificate is issued yet, so Traefik serves its own certificate. Check whether the cluster has the issuer, and what cert-manager says about the certificate:

```bash
sudo k3s kubectl -n geass-system get geasscluster default \
  -o jsonpath='{.status.conditions[?(@.type=="ClusterIssuerReady")].message}{"\n"}'
sudo k3s kubectl -n proj-1a2b3c4d-production get certificate,challenge
```

A message about a missing ACME contact email means that the cluster was installed without `GEASS_ACME_EMAIL`. For more information, see [HTTPS certificates](/docs/getting-started/install/#https-certificates). Otherwise, make sure that the DNS record of the hostname points at the server, and that 80/tcp is open to Let's Encrypt. Or turn **HTTPS** off. For more information, see [Domains and TLS](/docs/guides/domains-and-tls/).

**The response does not include your variables.** Variable changes apply on the next deploy. Make sure that **Up to date** shows, or deploy.

## Related

- [Projects and environments](/docs/guides/projects-and-environments/)
- [Command line](/docs/guides/command-line/)
- [Apps](/docs/guides/apps/)
- [Environment variables](/docs/guides/environment-variables/)
- [Connect a domain](/docs/getting-started/connect-domain/)
- [Databases](/docs/guides/databases/)
