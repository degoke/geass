---
title: "Dashboard and controllers apart"
description: "Run the Geass dashboard and API in one process and the controllers in another: the flags, what each mode starts, what it needs, and its access."
---

The operator binary is two things: the **controllers**, which reconcile the cluster, and the **dashboard**, which serves
the web interface and the REST API (`/api`, `/api/v1`, `/api/events`). The installer runs both in one process, and
nothing here changes that. Two flags let you run them in different places:

| Flag | Default | Meaning |
| --- | --- | --- |
| `--controllers` | `true` | Run the controllers. `--controllers=false` runs only the dashboard |
| `--dashboard` | `true` | Serve the dashboard and API. `--dashboard=false` runs only the controllers |

With both left on, the process is exactly what it was before the flags existed, and the installed manifests need no
change. A process with both off has nothing to do and refuses to start. So does a dashboard-only process whose
`--dashboard-bind-address` is `0`.

The reason to split them is where each needs to be. The controllers need the machines of the cluster: they SSH into
new nodes, reach the add-ons' services over the pod network, and are scraped by Prometheus. The dashboard needs the
Kubernetes API and, for a few features, the add-ons. A dashboard that runs next to a K3s server hosted
outside the cluster's own nodes reads the cluster over a local connection, while the controllers stay on the cluster's
own machines.

## What each mode starts

| | Both (default) | `--controllers=false` | `--dashboard=false` |
| --- | --- | --- | --- |
| Reconcilers for every Geass kind | yes | **no** | yes |
| Data migrations (`geass-schema`) | runs them | **never**: checks the level and refuses to start (below) | runs them |
| Leader election lease | takes it with `--leader-elect` | **never**, even with `--leader-elect` | takes it with `--leader-elect` |
| Pod usage tracker, network policy check, problem exporter, alert routing | yes | **no** | yes |
| The operator's own monitoring objects (its ServiceMonitor and alert rules) | yes | **no** | yes |
| Dashboard Service, Ingress and tunnel route for the dashboard's address | yes | not applicable | **left alone**: no Ingress or route, and the dashboard sets its own domain condition |
| Dashboard, `/api`, `/api/v1`, `/api/events`, deploy hooks, GitHub callbacks | yes | yes, with the same sign-in and access rules | **no HTTP server on the dashboard port** |
| Health probes (`--health-probe-bind-address`) | yes | yes | yes |
| Metrics (`--metrics-bind-address`) | if set | if set, without the controllers' series | if set |

The operator registers no admission webhooks, so there is none to start or skip.

Everything the dashboard writes is an object a person can edit: projects, services, databases, settings, the
Secrets that hold users and tokens. It never writes the schema record, the leader election lease, the cluster's add-ons or
the workloads that services become. It does write the status of a few of the same kinds (a deploy or retry it starts
records its attempt in the status of the service, database, bucket or build), so the access below includes it. Everything
else in a status is the controllers' to set, and the dashboard waits for it.

What the two halves shared in one process is not shared when they are apart: the dashboard builds its own webhook dispatcher
and cloud providers, and each process keeps its own hold-back for reports that GitHub refused, so both may call GitHub
after it has turned reports away from the other.

## Run only the dashboard

`--controllers=false` serves the dashboard and nothing else. The process reads and writes the cluster through the API
server it is given, so it runs anywhere that reaches one.

```bash title="Dashboard only, outside the cluster"
/manager --controllers=false \
  --kubeconfig=/etc/geass/kubeconfig \
  --dashboard-bind-address=:8085 \
  --health-probe-bind-address=:8086
```

`--kubeconfig` (or the `KUBECONFIG` variable) says which cluster; without either, the process uses the service account it
runs under, as the operator does in the cluster. `--leader-elect` is ignored, with a line in the log saying so.

### The controllers must be the same release, and run first

Only the controllers migrate the cluster's data, once, on start (see [Upgrading](/docs/operations/upgrading/)). The level
they reached is recorded in the ConfigMap `geass-schema` in `geass-system`. A dashboard-only process compares that level
with the one it reads, changes nothing, and **refuses to start** unless they are equal:

- **Behind**: the controllers have not migrated the data to this release (or never started in this cluster). A
  dashboard that ran would write the old shape. Update the controllers to the same release and let them start.
- **Ahead**: the data was migrated by a newer release than this binary. It could misread what it finds. Run the newer
  release.

The check is repeated every minute while the process runs, so a cluster that is migrated under a running dashboard stops
it instead of leaving it on the wrong level. Run it under something that restarts it (a Deployment does): the restart
fails with the same message until the controllers have caught up, and succeeds as soon as they have.

When you update, update the controllers first. Update the dashboard to the same release after the controllers have started.

### Access it needs

A dashboard-only process needs less than the controllers: it cannot create Deployments, Services, Ingresses, Jobs,
NetworkPolicies or volume claims, and it has no access to the leader election lease. `config/dashboard-only` in the
repository is a ServiceAccount, a ClusterRole `geass-dashboard` and its binding:

```bash title="Give a dashboard-only process its access"
kubectl apply -k config/dashboard-only
```

To bind the ClusterRole to a user of a kubeconfig instead of that ServiceAccount, bind it yourself:

```bash
kubectl create clusterrolebinding geass-dashboard --clusterrole=geass-dashboard --user=<the user in the kubeconfig>
```

What it grants, and why:

| Access | Why |
| --- | --- |
| Every Geass kind: `get`, `list`, `watch`, `create`, `update`, `patch`, `delete`; and the status of the kinds the dashboard keeps records in | The dashboard is the editor of these objects |
| Secrets and ConfigMaps, cluster-wide, with write access | Users, sessions, API tokens, deploy hooks, connections' credentials and alert channels live in Secrets in `geass-system`. The cache the dashboard reads from watches the whole cluster, so the read access is cluster-wide |
| Pods, PersistentVolumeClaims, StorageClasses and Deployments: `get`, `list`, `watch` | Usage, capacity and what a service is running |
| Nodes: `get`, `list`, `watch`, `update`, `patch` | Placement: labels and taints edited from the dashboard |
| `pods/log`: `get`; `pods/exec`: `create` | Logs, terminals and the database query box |
| Jobs: `delete` | A build that is started again has its finished Job removed |

The cluster-wide read of Secrets is the largest part of this. The controllers have it too, and it is how the dashboard
sees a project's Secrets. Do not give a dashboard-only process less than it needs to read them: it would start, and
fail on the first sign-in.

### Environment

A process outside the cluster has no Services to resolve and no mounted Secrets. Set what it needs:

| Variable | When | Meaning |
| --- | --- | --- |
| `KUBECONFIG` | Outside a cluster, if you do not pass `--kubeconfig` | Which API server |
| `GEASS_DASHBOARD_USERS`, or `GEASS_DASHBOARD_PASSWORD` and `GEASS_DASHBOARD_USERNAME` | Only until the Secret `geass-dashboard-auth` has users | Sign-in for the first people. The Secret is read from the cluster and wins when it has users |
| `GEASS_DASHBOARD_SESSION_KEY` | Optional | Signs session cookies. Without it, the key in `geass-dashboard-auth` is used |
| `GEASS_PUBLIC_URL` | With a GitHub App | The address people reach this dashboard at, for GitHub's callbacks |
| `GEASS_GITHUB_APP_ID`, `GEASS_GITHUB_APP_SLUG`, `GEASS_GITHUB_APP_CLIENT_ID`, `GEASS_GITHUB_APP_CLIENT_SECRET`, `GEASS_GITHUB_APP_PRIVATE_KEY`, `GEASS_GITHUB_WEBHOOK_SECRET` | Only if the platform settings have no GitHub App | A GitHub App saved in **Settings** → **Connectors** is stored in the cluster and needs none of these |
| `GEASS_PROMETHEUS_URL` | To show metrics | Prometheus's address. Used when the platform settings have no Prometheus URL. The in-cluster default (`http://…svc:9090`) does not resolve outside the cluster |
| `GEASS_ALERTMANAGER_URL` | To test alerts and see Alertmanager's state | Alertmanager's address |
| `GEASS_LOGS_URL` | To search stored logs | The log store's address (VictoriaLogs) |
| `GEASS_WEBHOOKS_ALLOW_PRIVATE_NETWORKS` | If a project's webhooks point at a private address | As on the operator |

Prometheus, Alertmanager and VictoriaLogs are plain HTTP services inside the cluster, so a dashboard that cannot
resolve them degrades instead of failing: the metrics views say that Prometheus is not reachable, and log search is
reported unavailable. Everything that goes through the API server needs nothing extra: live logs of a running pod,
terminals, the database query box and the logs of the add-ons' pods work the same as in the cluster. The dashboard makes
no HTTP request of its own to the cluster's object stores (the controllers do); it reads their pods' logs through the API
server.

A dashboard that is not in the cluster must be reachable by whatever calls it: GitHub posts to
`/webhooks/github`, deploy hooks are called at `/hooks/deploy/…`, and the identity provider redirects to the sign-in
callback.

:::note
Keep one replica of a dashboard-only process, or keep a person's requests on one. A few things are held in memory
and are not shared between processes: the sign-ins the command line is waiting on, the stream of live changes, and the
count of failed sign-ins per address.
:::

## Run only the controllers

`--dashboard=false` runs the reconcilers, the migrations and the checks, and opens nothing on the dashboard port. Run it
where the controllers must be: on the cluster's own machines.

```bash title="Controllers only"
/manager --leader-elect --dashboard=false --health-probe-bind-address=:8086
```

Nothing the controllers need comes from the dashboard. What changes is what points at it:

- **The dashboard's address.** The controllers publish the dashboard's domain, with an Ingress or a Cloudflare tunnel route
  to the Service `geass-dashboard`. With `--dashboard=false` there is no dashboard behind that Service, so they create
  no Ingress and send no tunnel route for it, and they do not write the `DashboardDomainReady` condition on the platform
  settings: the dashboard verifies its own address and sets it. Services' own routes are unaffected. Publish the dashboard where it
  runs.
- **Probes of the dashboard's URL** are not made, for the same reason.
- **GitHub, deploy hooks and sign-in callbacks** are served by the dashboard, wherever it runs.

The controllers still take the leader election lease and run the migrations, so a dashboard-only process of the same
release can follow them.

## Both, in one process

This is the installer's default, and it needs none of the above. A process with `--controllers` and `--dashboard` shares
the webhook dispatcher and the cloud providers between them, takes the lease with `--leader-elect`, and serves the
dashboard from the process that holds it.
