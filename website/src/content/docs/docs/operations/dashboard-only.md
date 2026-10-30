---
title: "Dashboard and controllers apart"
description: "Run the Geass dashboard and API in one process and the controllers in another: the flags, what each mode starts, what it needs, and its access."
---

The operator binary has two parts:

- The **controllers**, which reconcile the cluster.
- The **dashboard**, which serves the web interface and the REST API (`/api`, `/api/v1`, `/api/events`).

The installer runs both parts in one process, and nothing on this page changes that. You can use two flags to run the
parts in different places:

| Flag | Default | Meaning |
| --- | --- | --- |
| `--controllers` | `true` | Run the controllers. `--controllers=false` runs only the dashboard |
| `--dashboard` | `true` | Serve the dashboard and API. `--dashboard=false` runs only the controllers |

When both flags are on, the process is exactly the same as before the flags existed, and the installed manifests need
no change. A process with both flags off has nothing to do, and it refuses to start. A dashboard-only process whose
`--dashboard-bind-address` is `0` also refuses to start.

You split the parts because each part needs to be in a different place. The controllers need the machines of the
cluster:

- The controllers SSH into new nodes.
- The controllers reach the add-ons' services over the pod network.
- Prometheus scrapes the controllers.

The dashboard needs the Kubernetes API and, for a few features, the add-ons. Some clusters have a K3s server that is
hosted outside the cluster's own nodes. A dashboard that runs next to such a server reads the cluster over a local
connection, and the controllers stay on the cluster's own machines.

## What each mode starts

| | Both (default) | `--controllers=false` | `--dashboard=false` |
| --- | --- | --- | --- |
| Reconcilers for every Geass kind | yes | **no** | yes |
| Data migrations (`geass-schema`) | runs them | **never**: checks the level and refuses to start (see below) | runs them |
| Leader election lease | takes it with `--leader-elect` | **never**, even with `--leader-elect` | takes it with `--leader-elect` |
| Pod usage tracker, network policy check, problem exporter, and alert routing | yes | **no** | yes |
| The operator's own monitoring objects (its ServiceMonitor and alert rules) | yes | **no** | yes |
| Dashboard Service, Ingress, and tunnel route for the dashboard's address | yes | not applicable | **left alone**: no Ingress or route, and the dashboard sets its own domain condition |
| Dashboard, `/api`, `/api/v1`, `/api/events`, deploy hooks, and GitHub callbacks | yes | yes, with the same sign-in and access rules | **no HTTP server on the dashboard port** |
| Health probes (`--health-probe-bind-address`) | yes | yes | yes |
| Metrics (`--metrics-bind-address`) | if set | if set, without the controllers' series | if set |

The operator registers no admission webhooks, so there are no webhooks to start or skip.

Everything that the dashboard writes is an object that a person can edit: projects, services, databases, settings, and
the Secrets that hold users and tokens. The dashboard never writes the schema record, the leader election lease, the
cluster's add-ons, or the workloads that services become. The dashboard does write the status of a few of the same
kinds. When the dashboard starts a deploy or a retry, it records the attempt in the status of the service, database,
bucket, or build. So the access below includes status. The controllers set everything else in a status, and the
dashboard waits for them.

In one process, the two parts share some things. When the parts are apart, they do not share these things:

- The dashboard builds its own webhook dispatcher and cloud providers.
- Each process keeps its own hold-back for reports that GitHub refused. So both processes might call GitHub after
  GitHub turned reports away from the other process.

## Run only the dashboard

`--controllers=false` serves the dashboard and nothing else. The process reads and writes the cluster through the API
server that it is given, so the process can run anywhere that can reach an API server.

```bash title="Dashboard only, outside the cluster"
/manager --controllers=false \
  --kubeconfig=/etc/geass/kubeconfig \
  --dashboard-bind-address=:8085 \
  --health-probe-bind-address=:8086
```

`--kubeconfig`, or the `KUBECONFIG` variable, tells the process which cluster to use. Without either one, the process
uses the service account that it runs under, the same as the operator in the cluster. The process ignores
`--leader-elect`, and it writes a line in the log that says so.

### The controllers must be the same release, and run first

Only the controllers migrate the cluster's data. They do this one time, when they start. See
[Upgrading](/docs/operations/upgrading/). The controllers record the level that they reached in the ConfigMap
`geass-schema` in `geass-system`. A dashboard-only process compares that level with the level that it reads, and it
changes nothing. The process **refuses to start** unless the two levels are equal:

- **Behind**: the controllers did not migrate the data to this release, or they never started in this cluster. A
  dashboard that ran would write the old shape. Update the controllers to the same release, and let them start.
- **Ahead**: a newer release than this binary migrated the data. This binary could misread what it finds. Run the newer
  release.

The process repeats the check every minute while it runs. So if the cluster is migrated under a running dashboard, the
dashboard stops, and it does not stay on the wrong level. Run the dashboard under something that restarts it, for
example a Deployment. The restart fails with the same message until the controllers catch up. The restart succeeds
immediately after they catch up.

When you update, update the controllers first. After the controllers start, update the dashboard to the same release.

### Access it needs

A dashboard-only process needs less access than the controllers. It cannot create Deployments, Services, Ingresses,
Jobs, NetworkPolicies, or volume claims, and it has no access to the leader election lease. `config/dashboard-only` in
the repository has a ServiceAccount, a ClusterRole `geass-dashboard`, and its binding:

```bash title="Give a dashboard-only process its access"
kubectl apply -k config/dashboard-only
```

To bind the ClusterRole to a user of a kubeconfig instead of that ServiceAccount, create the binding yourself:

```bash
kubectl create clusterrolebinding geass-dashboard --clusterrole=geass-dashboard --user=<the user in the kubeconfig>
```

The ClusterRole grants this access, for these reasons:

| Access | Why |
| --- | --- |
| Every Geass kind: `get`, `list`, `watch`, `create`, `update`, `patch`, `delete`. Also the status of the kinds that the dashboard keeps records in | The dashboard edits these objects |
| Secrets and ConfigMaps, cluster-wide, with write access | Users, sessions, API tokens, deploy hooks, connections' credentials, and alert channels are in Secrets in `geass-system`. The cache that the dashboard reads from watches the whole cluster, so the read access is cluster-wide |
| Pods, PersistentVolumeClaims, StorageClasses, and Deployments: `get`, `list`, `watch` | Usage, capacity, and what a service runs |
| Nodes: `get`, `list`, `watch`, `update`, `patch` | Placement: labels and taints that you edit from the dashboard |
| `pods/log`: `get`. `pods/exec`: `create` | Logs, terminals, and the database query box |
| Jobs: `delete` | When a build is started again, the dashboard removes its finished Job |

The cluster-wide read of Secrets is the largest part of this access. The controllers also have it, and the dashboard
uses it to see a project's Secrets. Do not give a dashboard-only process less access than it needs to read Secrets.
With less access, the process starts, and then it fails on the first sign-in.

### Environment

A process outside the cluster has no Services to resolve and no mounted Secrets. Set the variables that it needs:

| Variable | When | Meaning |
| --- | --- | --- |
| `KUBECONFIG` | Outside a cluster, if you do not pass `--kubeconfig` | Which API server |
| `GEASS_DASHBOARD_USERS`, or `GEASS_DASHBOARD_PASSWORD` and `GEASS_DASHBOARD_USERNAME` | Only until the Secret `geass-dashboard-auth` has users | Sign-in for the first people. The process reads the Secret from the cluster. When the Secret has users, the Secret is used instead |
| `GEASS_DASHBOARD_SESSION_KEY` | Optional | Signs session cookies. Without this variable, the process uses the key in `geass-dashboard-auth` |
| `GEASS_PUBLIC_URL` | With a GitHub App | The address at which people reach this dashboard, for GitHub's callbacks |
| `GEASS_GITHUB_APP_ID`, `GEASS_GITHUB_APP_SLUG`, `GEASS_GITHUB_APP_CLIENT_ID`, `GEASS_GITHUB_APP_CLIENT_SECRET`, `GEASS_GITHUB_APP_PRIVATE_KEY`, `GEASS_GITHUB_WEBHOOK_SECRET` | Only if the platform settings have no GitHub App | A GitHub App that you save in **Settings** → **Connectors** is stored in the cluster, and it needs none of these variables |
| `GEASS_PROMETHEUS_URL` | To show metrics, without a metrics store outside the cluster | Prometheus's address. The process uses it when the platform settings have no Prometheus URL and no `observability.external.metrics.queryURL`. The in-cluster default (`http://…svc:9090`) does not resolve outside the cluster |
| `GEASS_MAIL_RELAY_URL` | To read `GET /api/v1/mail/usage` while the [mail relay](/docs/guides/mail-relay/) runs | The relay's admin port. The in-cluster default (`http://geass-mail-relay.geass-system.svc:9187`) does not resolve outside the cluster. Also, the relay's policy lets only `geass-system` and `monitoring` reach the port. So expose the port to the dashboard yourself |
| `GEASS_ALERTMANAGER_URL` | To test alerts and see Alertmanager's state | Alertmanager's address |
| `GEASS_LOGS_URL` | To search stored logs, without a log store outside the cluster | The log store's address (VictoriaLogs). The process does not use it when the platform settings have `observability.external.logs.queryURL` |
| `GEASS_WEBHOOKS_ALLOW_PRIVATE_NETWORKS` | If a project's webhooks point at a private address | The same as on the operator |

Prometheus, Alertmanager, and VictoriaLogs are plain HTTP services inside the cluster. So a dashboard that cannot
resolve them degrades, and it does not fail. The metrics views say that Prometheus is not reachable, and log search
shows as unavailable.

To give a dashboard outside the cluster metrics and log history, name stores outside the cluster in the platform
settings (`spec.observability.external`). The cluster sends to those stores, and every dashboard reads from them. Each
dashboard uses the tenant and credentials that the settings and their Secret give, and it does not need to reach any
address of the cluster. See
[Send metrics and logs outside the cluster](/docs/guides/observability/#send-metrics-and-logs-outside-the-cluster).

The **Alerts** page still reads Alertmanager in the cluster. So the page needs `GEASS_ALERTMANAGER_URL`, or it shows
Alertmanager as not reachable. Everything that goes through the API server needs nothing extra. Live logs of a running
pod, terminals, the database query box, and the logs of the add-ons' pods work the same as in the cluster. The
dashboard makes no HTTP request of its own to the cluster's object stores. The controllers make those requests. The
dashboard reads the logs of the object stores' pods through the API server.

Anything that calls a dashboard outside the cluster must be able to reach the dashboard:

- GitHub posts to `/webhooks/github`.
- Deploy hooks are called at `/hooks/deploy/…`.
- The identity provider redirects to the sign-in callback.

:::note
Keep one replica of a dashboard-only process, or keep the requests of one person on one replica. A few things are
in memory, and the processes do not share them:

- The sign-ins that the command line waits for.
- The stream of live changes.
- The count of failed sign-ins for each address.
:::

## Run only the controllers

`--dashboard=false` runs the reconcilers, the migrations, and the checks, and it opens nothing on the dashboard port.
Run it where the controllers must be: on the cluster's own machines.

```bash title="Controllers only"
/manager --leader-elect --dashboard=false --health-probe-bind-address=:8086
```

The controllers need nothing from the dashboard. What changes is what points at the dashboard:

- **The dashboard's address.** The controllers publish the dashboard's domain, with an Ingress or a Cloudflare tunnel
  route to the Service `geass-dashboard`. With `--dashboard=false`, there is no dashboard behind that Service. So the
  controllers create no Ingress and send no tunnel route for the dashboard. They also do not write the
  `DashboardDomainReady` condition on the platform settings. The dashboard verifies its own address and sets that
  condition. The routes of services are not affected. Publish the dashboard where it runs.
- **Probes of the dashboard's URL** are not made, for the same reason.
- **GitHub, deploy hooks, and sign-in callbacks** are served by the dashboard, wherever it runs.

The controllers still take the leader election lease and run the migrations. So a dashboard-only process of the same
release can follow them.

## Both, in one process

This mode is the installer's default, and it needs none of the setup above. A process with `--controllers` and
`--dashboard` shares the webhook dispatcher and the cloud providers between the two parts. The process takes the lease
with `--leader-elect`, and it serves the dashboard from the process that holds the lease.
