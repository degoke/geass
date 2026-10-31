---
title: "Logs, metrics and terminal"
description: "Find logs, metrics, and the terminal in the dashboard. Learn what Geass collects, how long Geass keeps it, and who can see what."
---

Geass gives you four ways to see what happens in your projects:

- **Metrics** chart CPU, memory, network, and requests.
- **Logs** show what your services print, live and from the last 7 days.
- The **terminal** opens a shell inside a running service.
- **Geass health** charts Geass itself.

This page tells you where to find each one, how long Geass keeps the data, and who can use it.

## Before you start

- Metrics need the monitoring add-on. The installer turns it on.
- Log history needs the logging add-on. The installer also turns it on. Live logs need no add-on.
- Every signed-in user can see metrics. Logs and the terminal need an admin. See [Who can see what](#who-can-see-what).

## Metrics

### Where to find them

| Where | Open | Charts |
| --- | --- | --- |
| A project | The **Metrics** tab of the project. It shows the environment that you select in the environment bar. | CPU, Memory, Network out, Network in |
| A service | The **Metrics** tab of the service. | CPU, Memory, Network out, Network in, Requests |
| A database | The **Monitor** tab of the database. | CPU, Memory, Disk used, Network out, Network in |
| All servers | **Usage**, under **Cluster metrics**. | CPU used by services, Memory used by services, Machines busy (CPU), Machines memory in use, Network out, Network in |
| Geass itself | **Usage**, under **Geass health**. | See [Metrics and alerts for the operator](#metrics-and-alerts-for-the-operator). |

Each page has a **Time range** menu: **Last 15 min**, **Last hour**, **Last 6 hours**, or **Last 24 hours**. The charts refresh every 15 seconds. A chart with no data shows "No data for this time range".

A database that runs outside your servers has no charts. The dashboard says: "This database runs outside the cluster, so there is nothing here to measure."

### What each chart measures

| Chart | Measures |
| --- | --- |
| **CPU** | The CPU that the service uses, in vCPU, as an average over two minutes. |
| **Memory** | The memory that the service holds. |
| **Network out**, **Network in** | The bytes that the service sends and receives. This count includes calls to other services, not only visitor traffic. |
| **Requests** | The HTTP requests per second that arrive through the domains of the service. Calls from other services do not count. |
| **Disk used** | The space that the database uses on its volume. |
| **Machines busy (CPU)**, **Machines memory in use** | How busy your servers are. |

Charts show up to 24 hours. Geass keeps 15 days of metrics, up to 4 GB. When either limit is reached, Geass drops the oldest data.

### Your own metrics

A service can publish its own Prometheus metrics on `/metrics`. To collect them, do these steps:

1. Open the service, and select **Settings**.
2. Open **Deploy**.
3. Under **Metrics**, turn on **Enable metrics**.

The change applies immediately. Geass collects the metrics, but the dashboard does not chart them. To query them, use Prometheus itself. See [Under the hood](#under-the-hood).

If the monitoring add-on is off, a service with **Enable metrics** on cannot become ready. Its status says "metrics are enabled but Prometheus Operator is not installed in this cluster".

### From the command line

```bash
geass metrics web --window 6h
geass metrics orders --kind databases
```

From a script, call `GET /api/apps/<service>/metrics?window=1h`. The `window` is `15m`, `1h`, `6h`, or `24h`. Projects, databases, and the cluster have the same endpoints. See the [REST API](/docs/reference/rest-api/) and [CLI commands](/docs/reference/cli/).

## Logs

### Read the logs of a project

Open a project, and select its **Logs** tab. The tab shows the output of every service and database of the project in one timeline. Each line shows its time, the service that wrote it, and the text.

| Control | What it does |
| --- | --- |
| **Service** | Shows one service or database. The default is **All services**. |
| **Environment** | Shows one environment. It appears when the project has more than one environment. |
| **Search logs** | Shows only the lines that contain the text. Case does not matter. |
| **Level** | **All levels**, **Warnings and errors**, or **Errors only**. |
| **Logs from** | **What the nodes hold now**, or **History**. It appears only when the logging add-on is on. |
| **Time range** | For live logs: **Latest** to **Last 24 hours**. For history: **Last hour** to **Last 7 days**. |
| **Follow** | Live logs only. Shows new lines when they arrive. |
| **Include logs from before restarts** | Live logs only. Adds the output of the last run of a service that restarted. Use it to see why a service crashed or ran out of memory. |
| **Refresh** | Reads the logs again. |

Geass finds the level of a line from its text. A `level` or `severity` field comes first. Then Geass looks for words:

- `error`, `fatal`, `panic`, `exception`, or `traceback` mean an error.
- `warn` means a warning.
- `debug` or `trace` mean debug.

A line with no level counts as information.

### Live logs and history

| | What the nodes hold now | History |
| --- | --- | --- |
| Needs | Nothing. | The logging add-on. |
| After a service is replaced, for example by a deploy | The old output is gone. | The old output is kept. |
| How far back | What the servers still hold: up to 250 MiB for each copy of a service. | 7 days by default. An admin can set 1 to 90 days. |

A project that requires compliance tags has no history. Geass never sends its logs to the shared log store. See [Compliance node tags](/docs/guides/compliance-tags/#logs).

### Limits

Live logs show at most these lines:

- The newest 200 lines of each copy of a service.
- 3,000 lines in total, from at most 40 copies.

Geass cuts a line that is longer than 16 KiB. When a limit hides lines, the page says "Showing the newest lines. Narrow the search or time range to see more."

History shows 500 lines by default, and 3,000 at most. The **Level** filter looks only at the newest lines. So a rare level can show no lines, although older lines have that level.

### From the command line

```bash
geass logs web -f --level error --since 15m
```

A script can read logs with a workspace token that has the admin or observer role. The endpoints are `/api/projects/<project>/logs` and `/api/projects/<project>/logs/history`. See the [REST API](/docs/reference/rest-api/).

### Build and deploy logs

The **Deployments** tab of a service keeps each deploy. Open a deploy to see **Details**, **Build Logs**, and **Deploy Logs**. See [GitHub and builds](/docs/guides/github-and-builds/).

## Terminal

The **Console** tab of a service opens a shell in a running copy of the service.

1. Open the service, and select **Console**.
2. In the **Pod** menu, select a copy. If the menu says "No running pods", the service is not running.
3. Click **Connect**. A terminal opens, with `bash` if the image has it, or `sh`.
4. To end the session, click **Disconnect**.

A session lasts five minutes. A counter shows the time that is left. Then the session ends with "Session ended: Session time limit reached". To continue, connect again.

Geass records who opened each session, on which service, and when it started and ended. Geass does not record what you type or what the terminal shows.

### Database console

The **Console** tab of a database is a query box, not a shell.

1. Open the database, and select **Console**.
2. Enter one statement.
3. Click **Run query**.

Geass runs the statement and shows the output. For Redis, a command cannot have flags. On a highly available database, the statement can run on a replica, not on the primary.

## Who can see what

| | Viewer | Admin | Project token | Workspace token |
| --- | --- | --- | --- | --- |
| Metrics | Yes | Yes | Its own services, in one environment | Yes |
| Project logs and history | No | Yes | The logs of its own services only | Admin and observer |
| Terminal and database console | No | Yes | No | Admin only |

An observer token can read everything that a viewer can read, and also logs. It cannot change anything. Use it for a monitoring script. See [Users and tokens](/docs/guides/users-and-tokens/).

A project cannot read the metrics or logs of another project. Geass builds every query from the project that you open, never from the request. The services of a project also cannot reach the metrics store or the log store. See [Network isolation](/docs/guides/network-isolation/).

## Metrics and alerts for the operator

The **Geass health** section of the **Usage** page charts Geass itself. Look at it when the platform is slow, for example when a deploy is slow to start.

| Chart | Shows |
| --- | --- |
| **Failed reconciles** | The times per second that Geass failed to apply a change. |
| **Reconcile time (slowest 1%)** | How long the slowest changes take. |
| **API server answer time (slowest 1%)** | How long the cluster takes to answer Geass. |
| **Requests to the API server** | The requests that Geass sends to the cluster, per second. |
| **Work waiting** | The changes that wait for Geass. |
| **Status writes**, **Status writes avoided** | The status updates that Geass wrote, and the updates that it skipped because nothing changed. A steady cluster shows many skipped updates. |

When the monitoring add-on is on, Geass also watches itself. It sends these alerts as **Cluster problem** alerts to the channels that you set up in [Alerts](/docs/guides/alerts/):

| Alert | Fires when | Waits |
| --- | --- | --- |
| `GeassOperatorDown` | Geass reports nothing. This alert is critical. | 5 minutes |
| `GeassReconcileErrors` | Geass fails more than 0.05 changes per second. | 15 minutes |
| `GeassSlowReconciles` | The slowest 1% of changes take more than 30 seconds. | 15 minutes |
| `GeassWorkqueueBacklog` | More than 25 changes wait. | 15 minutes |
| `GeassOperatorMemoryHigh` | Geass uses more than 700 MiB of its 1 GiB. See [Cluster size](/docs/operations/sizing/). | 15 minutes |
| `GeassAPIServerSlow` | The slowest 1% of answers from the cluster take more than one second. | 10 minutes |
| `GeassAPIServerErrors` | More than 1 in 20 requests to the cluster fail. | 10 minutes |

## Change the add-ons

Both add-ons are on by default. To change them, you must be an admin.

1. Open **Settings** → **Add-ons**.
2. Find **Metrics** or **Logs**.
3. Click **Turn on** or **Turn off**. To turn off an add-on, confirm it.

Each add-on shows a state next to its title:

| State | Meaning |
| --- | --- |
| **Off** | The add-on is off. |
| **Installing** | The add-on is on and not up yet. The message under the title says what it waits for. |
| **Ready** | The add-on runs. |
| **Failed** | The add-on cannot start without help. The message under the title says why. |

When you turn off **Metrics**, Geass removes metrics, their history, and the alerts on Geass itself. When you turn off **Logs**, Geass removes log history. Live logs continue to work.

When an add-on is on, the page shows its options:

| Add-on | Option | Values |
| --- | --- | --- |
| **Metrics** | **Footprint** | `lite` (the default) or `full`. `full` adds the default components and rules of the monitoring chart. |
| **Logs** | **Keep logs for (days)** | 1 to 90. The default is 7. |

Change one option at a time, and wait until the badge says **Ready** before the next change. Fast changes can stop the install job. Then the badge says **Failed**. Click **Retry install**.

You can do the same from the command line:

```bash
geass addons
geass addons enable logging --retention-days 14 --wait
geass addons set monitoring --profile full
geass addons disable logging
geass addons retry monitoring
```

## Send metrics and logs outside the cluster

Geass can also send metrics and logs to stores outside the cluster. Then the dashboard reads from those stores. Use this setting in these cases:

- You run many clusters and keep their data in one place.
- The [dashboard runs outside the cluster](/docs/operations/dashboard-only/) and cannot reach the stores inside it.

The dashboard and the command line have no screen for this setting yet. See [Send to outside stores on the server](#send-to-outside-stores-on-the-server) under Under the hood.

Know these requirements:

- The metrics store must accept Prometheus remote write and answer Prometheus queries. Examples are Mimir, Cortex, and VictoriaMetrics.
- The log store must be VictoriaLogs, or speak its API. Loki is not supported.

Know these limits:

- The stores in the cluster continue to keep their own copy.
- The **Alerts** page still reads the alert sender in the cluster.
- Metrics of projects that require compliance tags are sent. Their logs are not.
- The store must have a certificate that is trusted by default. You cannot add your own certificate authority.
- You can name one metrics store and one log store.
- The dashboard does not fall back to the stores in the cluster when an outside store fails.

:::caution
Change this setting one step at a time, and wait about two minutes after each change. Three fast changes once stopped the monitoring add-on for five minutes. During that time, no new service could start. See [Troubleshooting](#troubleshooting).
:::

:::note
Geass was tested against VictoriaMetrics and VictoriaLogs. Mimir, Cortex, Thanos, and a multi-tenant VictoriaMetrics were not tested.
:::

## Clusters at more than one provider

In a [cluster at more than one provider](/docs/operations/multi-provider/), the service, project, and database charts cover every server. This needs TCP port 10250 open between the servers. Live logs need the same port. The **Machines busy (CPU)** and **Machines memory in use** charts cover only the server that runs monitoring.

## Troubleshooting

**Charts say "Metrics are not available yet".**
The monitoring add-on is off or not running. Turn it on. See [Change the add-ons](#change-the-add-ons).

**Charts are empty for services on one server.**
Geass cannot reach that server for metrics. In a cluster at more than one provider, open TCP port 10250 between the servers. Otherwise, make sure that the server shows **Ready** on the **Usage** page.

**The Requests chart is empty.**
Geass counts requests that arrive through a domain. A service that only other services call has no requests.

**The Logs tab says "No logs match", or the output from before a deploy is gone.**
Live logs show only what the servers still hold. Turn on **Include logs from before restarts**, or select **History** in **Logs from**.

**History says "Log history is not set up on this cluster. Turn on the logging add-on to keep logs after pods are gone."**
The logging add-on is off. See [Change the add-ons](#change-the-add-ons).

**History says "The log store is not answering yet. It may still be starting; try again in a minute."**
The log store is starting, or it is not healthy. Wait one minute, and try again.

**The Logs tab says "Your viewer role cannot read logs."**
Logs need an admin. Ask an admin for the admin role.

**The terminal says "Could not open the terminal. The session may have ended; connect again."**
The five-minute session ended, or the service stopped. Connect again. If you run your own proxy in front of the dashboard, the proxy must pass WebSocket connections.

**The terminal says "pod is not running" or "a running pod and container are required".**
The copy stopped before you connected. Select a running copy in the **Pod** menu.

**Charts or history say that the store outside the cluster "cannot be used", "refused Geass's credentials", or "is not answering".**
These messages are about the store in [Send metrics and logs outside the cluster](#send-metrics-and-logs-outside-the-cluster):

- "Cannot be used" means that the address, the tenant header, or the secret is not correct. The Geass log says which.
- "Refused" means that the store answered 401 or 403. Check the token or the password, and the tenant.
- "Not answering" means that the dashboard could not reach the store.

**Every project says "Referenced GeassCluster is not ready" after you changed the outside stores.**
The installation of the monitoring add-on stopped. Open **Settings** → **Add-ons**. If **Metrics** says **Failed**, click **Retry install**.

## Under the hood

These commands show the add-ons and the terminal sessions:

```bash
kubectl -n monitoring get pods
kubectl -n geass-logging get pods,pvc
kubectl -n geass-system get geassconsolesessions
kubectl -n geass-system logs deploy/geass-controller-manager -c manager | grep -i external
```

- Metrics are in Prometheus at `http://geass-kube-prometheus-stac-prometheus.monitoring.svc:9090`. Prometheus has a 5 GiB volume.
- Log history is in VictoriaLogs at `http://geass-victorialogs.geass-logging.svc:9428`. Fluent Bit on each server sends the logs.
- **Enable metrics** creates a `ServiceMonitor` named `<service>-metrics`. A manifest can change the path and the port with `spec.metrics.path` and `spec.metrics.port`.
- The alerts on Geass itself are in the `PrometheusRule` `geass-operator` in `monitoring`.
- On servers that Geass installs, K3s keeps five log files of 50 MiB for each container. A server that you joined yourself keeps the default of 10 MiB.
- The **Copy kubectl command** button on the **Console** tab copies a `kubectl exec` command for the same copy.
- To set a Prometheus or log store at another address, set `spec.prometheusURL` on the platform configuration, or `GEASS_PROMETHEUS_URL` and `GEASS_LOGS_URL` on Geass.

### The add-on settings

The **Add-ons** page changes the fields under `spec.addons` of the `GeassCluster` named `default`. The operator writes the state of each add-on to `status.addons`.

| Setting | Values |
| --- | --- |
| `spec.addons.monitoring.enabled` | `true` (the default) or `false`. |
| `spec.addons.monitoring.profile` | `lite` (the default) or `full`. |
| `spec.addons.logging.enabled` | `true` or `false`. |
| `spec.addons.logging.retentionDays` | 1 to 90. The default is 7. |

**Retry install** deletes the suspended install job of the chart, for example `helm-install-geass-kube-prometheus-stack` in `kube-system`. K3s then makes a new job, which finishes in about one minute.

### Send to outside stores on the server

With access to the cluster, set `spec.observability.external` on the platform configuration:

```yaml title="platform.yaml (the observability part)"
spec:
  observability:
    external:
      metrics:
        remoteWriteURL: https://metrics.example.com/api/v1/push
        queryURL: https://metrics.example.com/prometheus
        tenant: acme
        credentialRef:
          name: observability-credentials
      logs:
        pushURL: https://logs.example.com
        queryURL: https://logs.example.com
        accountID: 42
        projectID: 0
        retentionDays: 30
        credentialRef:
          name: observability-credentials
```

```bash
sudo k3s kubectl -n geass-system create secret generic observability-credentials --from-literal=token="$TOKEN"
sudo k3s kubectl -n geass-system edit geassplatformconfig platform
```

The secret holds a `token`, or a `username` and a `password`. Every field is optional. For each field, see [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/).

## Related

- [Usage and capacity](/docs/guides/usage-and-capacity/)
- [Alerts](/docs/guides/alerts/)
- [Network isolation](/docs/guides/network-isolation/)
- [Compliance node tags](/docs/guides/compliance-tags/)
- [Users and tokens](/docs/guides/users-and-tokens/)
- [Troubleshooting](/docs/operations/troubleshooting/)
