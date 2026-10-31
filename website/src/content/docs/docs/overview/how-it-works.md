---
title: "How Geass works"
description: "The architecture a user needs: the K3s cluster, the operator, the dashboard, what runs where, how Deploy changes reaches a workload, and where state lives."
---

Geass is a Kubernetes operator that runs on a K3s cluster that you own, and a web dashboard that the operator serves. The dashboard writes your intent into Kubernetes objects. The operator turns those objects into running workloads. The `geass` command line and its MCP server reach the same dashboard through an API, so they follow the same path. This page describes the parts, how a change moves through them, and where your data lives. When something does not work correctly, this information tells you where to look.

## The parts

- **The cluster.** The installer installs [K3s](https://k3s.io/) in server mode with an embedded etcd database (`server --cluster-init`). K3s supplies the parts that Geass uses: containerd, the overlay network, CoreDNS, the Traefik ingress controller, and a Helm controller. The Helm controller installs charts from `HelmChart` objects. The first server is the control plane. Workers join it as K3s agents. See [Add servers](/docs/getting-started/add-servers/).
- **The operator.** One Deployment, `geass-controller-manager`, in the `geass-system` namespace, with one replica and leader election. The operator must run on the control plane, because the installer loads its image there. The operator has controllers for clusters, nodes, node pools, projects, services, builds, terminal sessions, databases, logical databases, buckets, backup records, connections, high availability readiness, and the platform settings.
- **The dashboard.** A React application that is embedded in the operator binary. The operator serves the dashboard on port 8085, behind the `ClusterIP` Service `geass-geass-dashboard`. The dashboard has no database and no server of its own: its HTTP API reads and writes Kubernetes objects. The dashboard signs users in itself (see [Sign in to the dashboard](/docs/getting-started/sign-in/)) and accepts API tokens for scripts. The same process serves the versioned JSON API under `/api/v1`. This API gives the operations of the dashboard in a shape that a program can call. A stream of change events (`/api/events`) keeps open browser tabs current.
- **Add-ons.** The operator installs these add-ons. It creates `HelmChart` objects in `kube-system`, and the Helm controller of K3s applies them:
  - cert-manager.
  - A lite Prometheus stack for metrics.
  - Longhorn for database and object storage volumes.
  - Fluent Bit with VictoriaLogs for log history.

  The operator adds other add-ons when you first need them: CloudNativePG when you create a PostgreSQL database, and Helm charts for MySQL and Redis. In-cluster object storage is not a chart. When you set up object storage, the operator itself runs the server that you selected, SeaweedFS or Garage. The operator runs that server from a pinned image, as a Deployment in `geass-system`.

  One add-on is opt-in: KEDA and its HTTP add-on, in the `keda` namespace. KEDA and its HTTP add-on let a service sleep when idle. The operator installs them only when `spec.addons.serverless.enabled` is set on the cluster, and the operator never uninstalls them. See [Sleep when idle](/docs/guides/scale-to-zero/).

  When the metrics add-on is on, the operator also creates a `ServiceMonitor` and a `PrometheusRule` in the `monitoring` namespace. Both objects are named `geass-operator`. With these objects, Prometheus scrapes the operator and has alert rules for failing or stalled reconciles, for example `GeassReconcileErrors`. Alertmanager is part of the metrics add-on. Alertmanager sends the alerts for which an admin sets up channels (see [Alerts](/docs/guides/alerts/)). The operator alerts go to those channels as cluster problems. The **Usage** page charts the metrics of the operator under **Geass health**.

  The cluster can have a Let's Encrypt contact email, which is the `GEASS_ACME_EMAIL` setting of the installer. In that case, the operator also keeps the cert-manager `ClusterIssuer` named `letsencrypt-prod`. Every HTTPS certificate for a hostname is requested from that issuer. See [HTTPS certificates](/docs/getting-started/install/#https-certificates).

  Before the dashboard has a domain, the operator serves it at the public IPv4 address of the server (`spec.dashboard.publicIP`). It keeps a second issuer, `geass-letsencrypt-ip`, a certificate for the address, a Traefik `TLSStore`, and an `IngressRoute`. It deletes them when the dashboard domain is verified. See [The dashboard at the public IP address](/docs/operations/installer/#the-dashboard-at-the-public-ip-address).
- **Your workloads.** The Deployments, Services, Ingresses, volumes, and jobs that the operator creates for your services, databases, and buckets. They are in one namespace for each project environment.
- **Connectors.** Optional links to Cloudflare, GitHub, AWS, and PlanetScale, and to the clouds that create machines for the cluster. The operator calls their APIs from inside the cluster, with credentials that you saved as Kubernetes Secrets.
- **Node pools.** A `GeassNodePool` has a controller that adds and removes nodes. For a node whose machine Geass creates, the node controller does these steps:
  1. It asks the cloud for the machine.
  2. It logs in to the machine over SSH with a key of its own.
  3. It installs the K3s agent.
  4. Later, it drains the node and deletes the machine.

  At the cloud, Geass creates only the machines and the firewall that it needs for them. See [Node pools and autoscaling](/docs/guides/node-pools/).
- **The command line and the MCP server.** One program, `geass`, that you install on your own machine, not in the cluster. The program calls `/api/v1` on the dashboard with an API token. You get the token when you approve a code in the dashboard (`geass login`). The program never talks to Kubernetes. `geass mcp` runs the same program as a Model Context Protocol server. An AI agent then acts as that token, and the agent sees only the tools that the token allows. See [Command line](/docs/guides/command-line/) and [MCP server for AI agents](/docs/guides/mcp-server/).

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
operator --> a cloud's API to create and delete machines          (only if you connect one, for pools and servers)
```

## What runs where

| Component | Runs on |
| --- | --- |
| K3s server, etcd, the operator, the dashboard | The control plane server. The operator is pinned there |
| Traefik and the other K3s system pods | Wherever Kubernetes schedules them |
| Services, databases, buckets, add-on pods | Any node that they are allowed on. The region of the project and the node tags that you set decide. K3s does not keep workloads off the control plane server, so a single-server install runs everything on one machine. See [Scaling and placement](/docs/guides/scaling-and-placement/) |
| The K3s agent and your workloads on a worker | The worker |

## How a change flows

This section follows the flow of a new service, from [Deploy your first app](/docs/getting-started/first-app/).

1. **You add a service.** The dashboard creates a `GeassApp` object in `geass-system` as a draft. In the draft, `deploy.enabled` is `false`. Annotations mark the object as one that uses the deploy workflow, with one pending change, `created`. The operator reports that the service waits for deployment. The operator creates nothing for the service: no Deployment exists yet.
2. **You edit the service.** When you save settings or variables, the dashboard updates the same object. The dashboard also adds a pending marker (`settings` or `variables`) in the annotations of the object. The top bar of the project counts the markers.
3. **Some settings do not wait.** Some settings are listed as live: a replica count, autoscaling, sleep when idle, the domain and HTTPS, metrics, and a few more. The operator applies these settings from the current object, even while the rest waits for a deploy. The dashboard reports `Saved and applied`.
4. **You click Deploy changes.** For every pending resource in the environment, the dashboard does these steps:
   1. It checks that the cluster has room for the resource.
   2. It sets `deploy.enabled: true`.
   3. It saves a snapshot of the settings in the `geass.dev/last-deployed` annotation.
   4. It clears the pending markers.
   5. For a service, it records a deployment (`GeassDeployment`) for the history.
5. **The operator reconciles.** The operator works from the snapshot, with the live settings on top. For a service, the operator creates these objects:
   - A Deployment.
   - A Service of the same name.
   - An Ingress, if the service has a hostname.
   - The config and Secret objects for the variables of the service.
   - An autoscaler or a metrics monitor, when you asked for them.

   For a service that sleeps when idle, the operator also creates a wake Service and a KEDA `HTTPScaledObject`. For a function with schedules, the operator creates one `CronJob` for each schedule. For a service built from a GitHub repository, the operator first runs a Kaniko build job. For a database, the operator creates the database server and its volume. For a bucket, the operator creates the bucket and its credentials.
6. **Status comes back.** The operator writes the result to the status of the object: the `Ready` condition and the URL. The dashboard shows the status as **Setting up**, then **Ready**, or **Failed**.

A deploy that fails does not retry for ever. After repeated failures, the resource stops and shows **Failed**. The resource then waits for you to click **Retry**.

### From the command line or an agent

`geass deploy web --wait` follows the same flow. The command makes one or two calls to `/api/v1`. The dashboard turns these calls into the operation behind the matching button, so the same rules apply:

- New services are drafts.
- Edits wait as pending until a deploy.
- The role of the token decides what is allowed.

With `--wait`, the command keeps reading the status of the service until the operator reports it **Ready** or **Failed**. The command then exits with a code that a script can act on. `geass mcp` offers an agent the same operations as tools. See [Command line](/docs/guides/command-line/).

Deletes go the other way. When you delete a resource, Kubernetes marks the object for deletion. The operator removes what it created, in the cluster and outside it, for example an S3 bucket or an IAM user. Only then does the operator let the object go. See [Uninstall Geass](/docs/operations/uninstall/).

## Where state lives

Geass has no database of its own. All of its state is in Kubernetes or on the disks of your servers.

| What | Where |
| --- | --- |
| Projects, services, databases, buckets, nodes, connections, the platform settings, and their pending changes | Kubernetes objects of the `geass.geass.dev` group, stored in the embedded etcd of K3s on the disk of the control plane |
| Dashboard users, session key, login lockouts, and the hash of the setup link | The Secret `geass-dashboard-auth` |
| API tokens (a hash of each secret), including the tokens that `geass login` and agents hold | The Secret `geass-api-tokens` |
| Command-line sign-ins that wait for approval | The memory of the operator only, for 10 minutes. A restart of the operator cancels them |
| The saved logins of the command line | `config.json` in the `geass` folder of the user configuration directory, on the machine where you ran `geass login`. The file holds a token, so its mode is `0600` |
| Service secrets, shared project secrets, connection credentials, the join token | Secrets in `geass-system` |
| The schedule token of a function (only for a function that has schedules or is an event handler) | The Secret `<service-id>-trigger` in the namespace of the service |
| TLS certificates | The cert-manager Secret `<service-id>-tls` in the namespace of the service. The ACME account key, `letsencrypt-prod-account-key`, is in `cert-manager`. The certificate of the dashboard at the public IP address is the Secret `geass-dashboard-public-ip-tls` in `geass-system` |
| Plain service variables | The settings of the service. Geass applies them to the workload of the service as a ConfigMap |
| PostgreSQL, MySQL, and SQLite data | Volumes on the nodes. The installer turns on Longhorn. With Longhorn, a volume keeps one copy on each node, up to three, or one copy for each instance for highly available Postgres. A database created without Longhorn uses the K3s `local-path` storage on one node |
| Redis data | Memory only. Geass runs Redis without persistence |
| In-cluster bucket contents | The object storage server (SeaweedFS or Garage), on one volume that you size when you set it up. The default size is 10 Gi. With Longhorn, the server keeps up to three copies on different nodes. Without Longhorn, it keeps one copy on one node |
| AWS S3 buckets | Your AWS account |
| Database backups | A bucket in your AWS account that you select in **Project settings** |
| Metrics | Prometheus: 15 days, at most 4 GB, on a 5 Gi volume |
| Log history | VictoriaLogs: 7 days by default, at most 4 GiB |
| Container images you build | The registry that you configure |

By default, the state of the cluster stays on the control plane disk. The embedded etcd of K3s takes snapshots on its defaults: twice a day, with five kept, in the same data directory. So these snapshots do not protect you if you lose the disk. The installer takes one more snapshot before it updates the cluster, so that you can undo an update. See [Upgrade Geass](/docs/operations/upgrading/#go-back). If you turn it on, K3s also sends each snapshot to an S3 bucket of your own. See [Disaster recovery](/docs/operations/disaster-recovery/#keep-copies-of-the-clusters-state). Database backups to S3 are the only backups of the contents of your databases. See [FAQ](/docs/overview/faq/#is-geass-itself-backed-up).

## Namespaces and names

| Namespace | Holds |
| --- | --- |
| `geass-system` | The operator, every Geass object, and the Secrets of Geass |
| `<project-id>-<environment>` | The workloads of one project environment, for example `proj-1a2b3c4d-production` |
| `kube-system` | The components of K3s, Traefik, and the `HelmChart` objects for the add-ons |
| `cert-manager`, `monitoring`, `longhorn-system`, `geass-logging`, `cnpg-system`, `moco-system`, `mariadb-system`, `keda` | The add-ons. `keda` exists only when the serverless add-on is on |

The Kubernetes name of a service is its ID, which Geass generates. The dashboard shows a display name that you can change. See [Core concepts](/docs/overview/concepts/).

## Networking

Traefik, which K3s installs, serves HTTP and HTTPS on ports 80 and 443. For each service with a hostname, the operator creates an Ingress that routes that host to the Kubernetes Service of the service. Inside the cluster, a service is reachable at `<service-id>.<namespace>.svc.cluster.local:<port>`. Nodes talk over the overlay network of K3s. The overlay network uses VXLAN by default, or WireGuard in a multi-site cluster.

The operator creates network policies that isolate each project environment. Unless you allow other projects, only these clients reach the services of a project:

- Services of the same project.
- Geass itself.
- Visitors through your domains.
- The platform namespaces: `kube-system`, `monitoring` for Prometheus, `cnpg-system`, `moco-system`, `mariadb-system`, and, when the serverless add-on is on, `keda`.

See [Network isolation](/docs/guides/network-isolation/).

## What Geass connects to

Geass connects only to what the features that you use need:

- The installer downloads from GitHub and `get.k3s.io`.
- The Helm controller downloads charts, and containerd pulls images, from public hosts.
- When you connect them, the operator calls the GitHub, Cloudflare, AWS (S3 and IAM), and PlanetScale APIs.
- For a connection for machines, the operator calls the API of that cloud: EC2, Azure Resource Manager, DigitalOcean, Compute Engine, Hetzner Cloud, Linode, Oracle Cloud, OVHcloud, Scaleway, UpCloud, Vultr, or your own webhook. The Azure driver also reads the public price list of Azure.
- The operator asks `api.ipify.org`, then `ifconfig.me`, for the public IP of the server. The operator does this only when no node reports a public IP and the operator needs the address for dashboard DNS or for the dashboard at the public IP address.
- cert-manager talks to Let's Encrypt (`acme-v02.api.letsencrypt.org`) after the certificate issuer exists.
- Geass posts to the webhook URLs that you add to a project.
- When the serverless add-on is on, the Helm controller also downloads charts from `kedacore.github.io`.
- The `geass` command line talks only to the Geass address that you give it.

The repository contains no telemetry or analytics code.

## Look at it yourself

You can see everything on this page with `kubectl`. On the control plane, use `k3s kubectl`:

```bash
sudo k3s kubectl get crds | grep geass
sudo k3s kubectl -n geass-system get geassprojects,geassapps,geassdatabases,geassobjectstores
sudo k3s kubectl -n geass-system get geasscluster default -o yaml
sudo k3s kubectl -n kube-system get helmcharts
sudo k3s kubectl get clusterissuer
sudo k3s kubectl -n geass-system logs deploy/geass-controller-manager -c manager
```

The same facts are available through the API, without `kubectl` access. For example, `geass status -p proj-1a2b3c4d -e production` lists the services of a project with their state, and the reason that a service is not ready. For the reference for each kind, see [Resources overview](/docs/reference/resources/overview/).

## Limits worth knowing

- The control plane is a single server and a single operator. If you lose that server, the dashboard and all reconciliation stop. See [High availability](/docs/operations/high-availability/).
- The operator image and the dashboard ship together. To install a new release, run its installer on the control plane. The installer updates the cluster in place. See [Upgrade Geass](/docs/operations/upgrading/).

## Related

- [Core concepts](/docs/overview/concepts/)
- [Install Geass](/docs/getting-started/install/)
- [Command line](/docs/guides/command-line/)
- [Projects and environments](/docs/guides/projects-and-environments/)
- [Configuration reference](/docs/reference/configuration/)
- [Security](/docs/operations/security/)
- [Troubleshooting](/docs/operations/troubleshooting/)
