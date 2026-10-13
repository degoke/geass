---
title: "How Geass works"
description: "The architecture a user needs: the K3s cluster, the operator, the dashboard, what runs where, how Deploy changes reaches a workload, and where state lives."
---

Geass is a Kubernetes operator running on a K3s cluster that you own, plus a web dashboard served by that operator. The dashboard writes your intent into Kubernetes objects, and the operator turns those objects into running workloads. The `geass` command line and its MCP server reach the same dashboard through an API, so they follow the same path. This page describes the parts, how a change moves through them, and where your data lives, so that you know what to look at when something misbehaves.

## The parts

- **The cluster.** The installer installs [K3s](https://k3s.io/) in server mode with an embedded etcd database (`server --cluster-init`). K3s brings the pieces Geass relies on: containerd, the overlay network, CoreDNS, the Traefik ingress controller, and a Helm controller that installs charts from `HelmChart` objects. The first server is the control plane. Workers join it as K3s agents. See [Add servers](/docs/getting-started/add-servers/).
- **The operator.** One Deployment, `geass-controller-manager`, in the `geass-system` namespace, with one replica and leader election. It is required to run on the control plane, because the installer loads its image there. It has controllers for clusters, nodes, projects, services, builds, terminal sessions, databases, logical databases, buckets, backup records, connections, high availability readiness and the platform settings.
- **The dashboard.** A React application embedded in the operator binary and served by the operator on port 8085, behind the `ClusterIP` Service `geass-geass-dashboard`. It has no database and no server of its own: its HTTP API reads and writes Kubernetes objects. It signs users in itself (see [Sign in to the dashboard](/docs/getting-started/sign-in/)) and accepts API tokens for scripts. The same process serves the versioned JSON API under `/api/v1`, which is the dashboard's own operations in a shape a program can call. A stream of change events (`/api/events`) keeps open browser tabs current.
- **Add-ons.** The operator installs these by creating `HelmChart` objects in `kube-system`, which K3s's Helm controller applies: cert-manager, a lite Prometheus stack for metrics, Longhorn for database volumes, and Fluent Bit with VictoriaLogs for log history. Others are added when you first need them: CloudNativePG when you create a PostgreSQL database, MinIO when you turn on in-cluster object storage, and Helm charts for MySQL and Redis. One is opt-in: KEDA and its HTTP add-on, in the `keda` namespace, which let a service sleep when idle. The operator installs them only when `spec.addons.serverless.enabled` is set on the cluster, and never uninstalls them. See [Sleep when idle](/docs/guides/scale-to-zero/). With the metrics add-on on, the operator also creates a `ServiceMonitor` and a `PrometheusRule`, both named `geass-operator`, in the `monitoring` namespace, so Prometheus scrapes the operator and has alert rules for failing or stalled reconciles, such as `GeassReconcileErrors`. The default `lite` profile installs no Alertmanager, so a firing rule shows in Prometheus and notifies nobody. The **Usage** page charts the operator's metrics under **Geass health**. When the cluster has a Let's Encrypt contact email (the installer's `GEASS_ACME_EMAIL`), the operator also keeps the cert-manager `ClusterIssuer` named `letsencrypt-prod`, which every HTTPS certificate is requested from. See [HTTPS certificates](/docs/getting-started/install/#https-certificates).
- **Your workloads.** The Deployments, Services, Ingresses, volumes and jobs the operator creates for your services, databases and buckets, in one namespace per project environment.
- **Connectors.** Optional links to Cloudflare, GitHub, AWS and PlanetScale. The operator calls their APIs from inside the cluster with credentials you saved as Kubernetes Secrets.
- **The command line and the MCP server.** One program, `geass`, that you install on your own machine, not in the cluster. It calls `/api/v1` on the dashboard with an API token that you got by approving a code in the dashboard (`geass login`), and it never talks to Kubernetes. `geass mcp` runs the same program as a Model Context Protocol server, so an AI agent acts as that token and sees only the tools it allows. See [Command line](/docs/guides/command-line/) and [MCP server for AI agents](/docs/guides/mcp-server/).

```text
      you: a browser, the geass command line, an AI agent (geass mcp), or scripts
                            |
        HTTPS on your domain, or kubectl port-forward (first sign-in)
                            |
+---------------- control plane server: K3s server + embedded etcd ------------------+
|  Traefik ingress (ports 80 and 443)                                                |
|                                                                                    |
|  namespace geass-system                                                            |
|    geass-controller-manager = operator (controllers) + dashboard (UI and API)      |
|    Geass objects: projects, services, databases, buckets, nodes ... and Secrets    |
|                                                                                    |
|  add-ons: cert-manager, Prometheus, Longhorn, Fluent Bit + VictoriaLogs            |
|  opt-in add-on: KEDA + HTTP add-on (services that sleep when idle)                 |
|  namespaces <project-id>-<environment>: your workloads                             |
+------------------------------------------------------------------------------------+
        |  K3s agent, port 6443 and the overlay network        |
+------------------------+                       +------------------------+
| worker server          |                       | worker server          |
| your workloads         |                       | your workloads         |
+------------------------+                       +------------------------+

operator --> GitHub, Cloudflare, AWS (S3, IAM), PlanetScale      (only what you connect)
```

## What runs where

| Component | Runs on |
| --- | --- |
| K3s server, etcd, the operator, the dashboard | The control plane server. The operator is pinned there |
| Traefik and the other K3s system pods | Wherever Kubernetes schedules them |
| Services, databases, buckets, add-on pods | Any node they are allowed on: the project's region and the node tags you set decide. K3s does not keep workloads off the control plane server, so a single-server install runs everything on one machine. See [Scaling and placement](/docs/guides/scaling-and-placement/) |
| The K3s agent and your workloads on a worker | The worker |

## How a change flows

Take the flow of a new service, from [Deploy your first app](/docs/getting-started/first-app/).

1. **You add a service.** The dashboard creates a `GeassApp` object in `geass-system` as a draft: `deploy.enabled` is `false`, and annotations mark it as using the deploy workflow with one pending change, `created`. The operator reports that the service is waiting for deployment and creates nothing for it: no Deployment exists yet.
2. **You edit it.** Saving settings or variables updates the same object and adds a pending marker (`settings` or `variables`) in its annotations. The top bar of the project counts the markers.
3. **Some settings do not wait.** A replica count, autoscaling, sleep when idle, the domain and HTTPS, metrics, and a few more are listed as live. The operator applies them from the current object even while the rest waits for a deploy, and the dashboard reports `Saved. Applied now`.
4. **You choose Deploy changes.** For every pending resource in the environment, the dashboard first checks that the cluster has room for it, then sets `deploy.enabled: true`, saves a snapshot of the settings in the `geass.dev/last-deployed` annotation, clears the pending markers and, for a service, records a deployment (`GeassDeployment`) for the history.
5. **The operator reconciles.** It works from the snapshot, with live settings overlaid. For a service it creates a Deployment, a Service of the same name and, if the service has a hostname, an Ingress, plus the config and Secret objects for its variables, an autoscaler or a metrics monitor when you asked for them. For a service that sleeps when idle it also creates a wake Service and a KEDA `HTTPScaledObject`, and for a function with schedules, one `CronJob` per schedule. For a service built from a GitHub repository it first runs a Kaniko build job. For a database it creates the database server and its volume, and for a bucket the bucket and its credentials.
6. **Status comes back.** The operator writes the result to the object's status (the `Ready` condition, the URL), and the dashboard shows it as **Setting up**, then **Ready**, or **Failed**.

A deploy that fails does not retry for ever. After repeated failures the resource stops, shows **Failed**, and waits for you to use **Retry**.

### From the command line or an agent

`geass deploy web --wait` follows the same flow. The command makes one or two calls to `/api/v1`, which the dashboard turns into the operation behind the matching button, so the same rules apply: new services are drafts, edits wait as pending until a deploy, and the token's role decides what is allowed. With `--wait` the command keeps reading the service's status until the operator reports it **Ready** or **Failed**, then exits with a code a script can act on. `geass mcp` offers an agent the same operations as tools. See [Command line](/docs/guides/command-line/).

Deletes go the other way. Deleting a resource marks the object for deletion; the operator removes what it created, in the cluster and outside it (an S3 bucket, an IAM user), and only then lets the object go. See [Uninstall Geass](/docs/operations/uninstall/).

## Where state lives

Geass has no database of its own. All of it is in Kubernetes or on your servers' disks.

| What | Where |
| --- | --- |
| Projects, services, databases, buckets, nodes, connections, the platform settings and their pending changes | Kubernetes objects of the `geass.geass.dev` group, stored in K3s's embedded etcd on the control plane's disk |
| Dashboard users, session key, login lockouts | The Secret `geass-dashboard-auth` |
| API tokens (a hash of each secret), including the ones `geass login` and agents hold | The Secret `geass-api-tokens` |
| Command-line sign-ins waiting for approval | The operator's memory only, for 10 minutes. Restarting the operator cancels them |
| The command line's saved logins | `config.json` in the `geass` folder of the user configuration directory on the machine where you ran `geass login`. It holds a token, so its mode is `0600` |
| Service secrets, shared project secrets, connection credentials, the join token | Secrets in `geass-system` |
| A function's schedule token (only for a function that has schedules or is an event handler) | The Secret `<service-id>-trigger` in the service's namespace |
| TLS certificates | cert-manager's Secret `<service-id>-tls` in the service's namespace. The ACME account key, `letsencrypt-prod-account-key`, is in `cert-manager` |
| Plain service variables | The service's settings, applied to its workload as a ConfigMap |
| PostgreSQL, MySQL and SQLite data | Volumes on the nodes. With Longhorn, which the installer enables, a volume keeps one copy per node, up to three, or one per instance for highly available Postgres. A database created without Longhorn uses K3s's `local-path` storage on one node |
| Redis data | Memory only. Geass runs Redis without persistence |
| In-cluster bucket contents | The MinIO server, on a 10 Gi volume |
| AWS S3 buckets | Your AWS account |
| Database backups | A bucket in your AWS account that you choose in **Project settings** |
| Metrics | Prometheus: 15 days, at most 4 GB, on a 5 Gi volume |
| Log history | VictoriaLogs: 7 days by default, at most 4 GiB |
| Container images you build | The registry you configure |

Geass does not back up the cluster's own state on a schedule. K3s's embedded etcd takes snapshots on its defaults (twice a day, five kept, in the same data directory), so they do not protect you from losing the disk. The installer takes one more before it updates the cluster, so that an update can be undone: see [Upgrade Geass](/docs/operations/upgrading/#go-back). Database backups to S3 are the only backups of your data that Geass makes. See [FAQ](/docs/overview/faq/#is-geass-itself-backed-up).

## Namespaces and names

| Namespace | Holds |
| --- | --- |
| `geass-system` | The operator, every Geass object, and Geass's Secrets |
| `<project-id>-<environment>` | One project environment's workloads, such as `proj-1a2b3c4d-production` |
| `kube-system` | K3s's own components, Traefik, and the `HelmChart` objects for the add-ons |
| `cert-manager`, `monitoring`, `longhorn-system`, `geass-logging`, `cnpg-system`, `keda` | The add-ons. `keda` exists only when the serverless add-on is on |

A service's Kubernetes name is its ID, which Geass generates. The dashboard shows a display name that you can change. See [Core concepts](/docs/overview/concepts/).

## Networking

Traefik, which K3s installs, serves HTTP and HTTPS on ports 80 and 443. For each service with a hostname the operator creates an Ingress that routes that host to the service's Service. Inside the cluster, a service is reachable at `<service-id>.<namespace>.svc.cluster.local:<port>`. Nodes talk over K3s's overlay network, VXLAN by default, or WireGuard in a multi-site cluster.

Each project environment is isolated by network policies that the operator creates: only services of the same project, Geass itself and visitors through your domains reach a project's services, unless you allow other projects. The platform namespaces also reach them: `kube-system`, `monitoring` for Prometheus, `cnpg-system` and, when the serverless add-on is on, `keda`. See [Network isolation](/docs/guides/network-isolation/).

## What Geass connects to

Only what the features you use need. The installer downloads from GitHub and `get.k3s.io`. The Helm controller downloads charts, and containerd pulls images, from public hosts. When you connect them, the operator calls the GitHub, Cloudflare, AWS (S3 and IAM) and PlanetScale APIs. It asks `api.ipify.org`, then `ifconfig.me`, for the server's public IP only when no node reports one and it needs the address for dashboard DNS. cert-manager talks to Let's Encrypt (`acme-v02.api.letsencrypt.org`) once the certificate issuer exists, and Geass posts to the webhook URLs you add to a project. With the serverless add-on on, the Helm controller also downloads charts from `kedacore.github.io`. The `geass` command line talks only to the Geass address you give it. The repository contains no telemetry or analytics code.

## Look at it yourself

Everything above is visible with `kubectl`, through `k3s kubectl` on the control plane:

```bash
sudo k3s kubectl get crds | grep geass
sudo k3s kubectl -n geass-system get geassprojects,geassapps,geassdatabases,geassobjectstores
sudo k3s kubectl -n geass-system get geasscluster default -o yaml
sudo k3s kubectl -n kube-system get helmcharts
sudo k3s kubectl get clusterissuer
sudo k3s kubectl -n geass-system logs deploy/geass-controller-manager -c manager
```

The same facts are available without `kubectl` access, through the API: `geass status -p proj-1a2b3c4d -e production` lists a project's services with their state and the reason one is not ready. The reference for each kind is in [Resources overview](/docs/reference/resources/overview/).

## Limits worth knowing

- The control plane is a single server and a single operator. Losing that server stops the dashboard and all reconciliation. See [High availability](/docs/operations/high-availability/).
- The operator image and the dashboard ship together. A new release is installed by running its installer on the control plane, which updates the cluster in place. See [Upgrade Geass](/docs/operations/upgrading/).

## Related

- [Core concepts](/docs/overview/concepts/)
- [Install Geass](/docs/getting-started/install/)
- [Command line](/docs/guides/command-line/)
- [Projects and environments](/docs/guides/projects-and-environments/)
- [Configuration reference](/docs/reference/configuration/)
- [Security](/docs/operations/security/)
- [Troubleshooting](/docs/operations/troubleshooting/)
