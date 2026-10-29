---
title: "Logs, metrics and terminal"
description: "Find logs, metrics, and the terminal in the dashboard. Learn what Geass collects and for how long, what the add-ons install, and who can see what."
---

Geass gives you three ways to see what your services do, and a fourth way to see how Geass itself does:

- **Metrics** chart CPU, memory, network, and requests.
- **Logs** show what services print. You see live logs and, with the logging add-on, logs from before the pods were replaced.
- The **terminal** opens a shell inside a running service.
- **Geass health** charts the operator.

This page tells where each one is, what Geass collects, how long Geass keeps it, and who can use it.

## Before you start

- **Metrics** need the monitoring add-on, which installs Prometheus. This add-on is on by default.
- **Log history** needs the logging add-on. In the cluster definition, this add-on is off by default, but the installer turns it on. Live logs need nothing extra.
- Metrics are open to every signed-in user. Logs and the terminal need an admin. Logs are also open to a token with the observer role. See [Who can see what](#who-can-see-what).

## Metrics

### Where to find them

| Where | Open | Charts |
| --- | --- | --- |
| A project | The **Metrics** tab of the project. It follows the environment that you select in the environment bar. | CPU, Memory, Network out, Network in |
| A service | The **Metrics** tab of the service. | CPU, Memory, Network out, Network in, Requests |
| A database | The **Monitor** tab of the database. | CPU, Memory, Disk used, Network out, Network in |
| The cluster | **Usage**, under **Cluster metrics**. | CPU used by services, Memory used by services, Machines busy (CPU), Machines memory in use, Network out, Network in |
| Geass itself | **Usage**, under **Geass health**. | Failed reconciles, Reconcile time (slowest 1%), API server answer time (slowest 1%), Requests to the API server, Work waiting, Status writes, Status writes avoided |

Each page has a **Time range** menu: **Last 15 min**, **Last hour**, **Last 6 hours**, or **Last 24 hours**. Charts refresh every 15 seconds. A chart with no samples shows "No data for this time range".

A database that runs outside the cluster has nothing to measure. The dashboard says: "This database runs outside the cluster, so there is nothing here to measure."

### What each chart measures

| Chart | Measures |
| --- | --- |
| **CPU** | The CPU that the containers use, as an average rate over two minutes, in vCPU. |
| **Memory** | The memory that the containers hold (the working set), summed. |
| **Network out**, **Network in** | The bytes that the pods send and receive, as a two-minute rate. This chart counts all traffic, including calls to other services in the cluster, not only the traffic that crosses the public ingress. |
| **Requests** | The HTTP requests per second that the ingress controller (Traefik) sends to the service. The chart does not count calls between services inside the cluster. |
| **Disk used** | The space used on the volume of the database. |
| **Machines busy (CPU)** | How busy the machines are, from the node exporter. |
| **Machines memory in use** | The memory in use on the machines, from the node exporter. |

The **Geass health** charts measure the operator:

| Chart | Measures |
| --- | --- |
| **Failed reconciles** | The reconciles that ended in an error, per second, summed over every controller. |
| **Reconcile time (slowest 1%)** | The 99th percentile of how long a reconcile takes, over five minutes. |
| **API server answer time (slowest 1%)** | The 99th percentile of how long the Kubernetes API server took to answer the operator, over five minutes. Watches are left out, because a watch stays open for minutes. |
| **Requests to the API server** | The requests that the operator sends, per second. |
| **Work waiting** | The depth of the work queues of the Geass controllers. |
| **Status writes** | The status updates that the operator wrote, per second. |
| **Status writes avoided** | The status updates that the operator compared with the stored status and skipped, because nothing had changed. |

The operator compares a status with the stored status before it writes. So a steady cluster shows few writes and many avoided writes.

The space between points depends on the time range:

- Over 15 minutes, points are 15 seconds apart.
- Over an hour, points are one minute apart.
- Over 6 hours, points are five minutes apart.
- Over 24 hours, points are 15 minutes apart.

Charts show up to 24 hours, although Prometheus keeps more.

### Your own metrics

A service can also publish its own Prometheus metrics. To turn on these metrics, do these steps:

1. In **Settings** of the service, open **Deploy**.
2. Under **Metrics**, turn on **Enable metrics**. The text of this switch is "Expose Prometheus metrics from /metrics. Requires Prometheus Operator in the cluster."

The change applies immediately. Geass then creates a `ServiceMonitor` named `<service>-metrics`. With this monitor, Prometheus scrapes `/metrics` on the `http` port of the service. A manifest can change the path and the port with `spec.metrics.path` and `spec.metrics.port`. Prometheus stores those series, but the dashboard does not chart them. To query the series, use Prometheus itself.

The `ServiceMonitor` kind comes with the monitoring add-on. If monitoring is off, a service that has **Enable metrics** on cannot become ready. Its `Ready` condition says "metrics are enabled but Prometheus Operator is not installed in this cluster".

### How long it is kept

Prometheus keeps 15 days of samples, up to 4 GB, on a 5 GiB volume. The limit that Prometheus reaches first applies, and Prometheus drops the oldest samples.

### Read metrics from a script

```bash
curl -sS -H "Authorization: Bearer $GEASS_TOKEN" \
  "https://geass.example.com/api/apps/web/metrics?window=1h"
```

`window` is `15m`, `1h`, `6h`, or `24h`. The response holds `available` and a `series` object. The keys of `series` are `cpu`, `memory`, `network`, `networkIn`, and, for services, `requests`. Each series is a list of `{"t": <unix seconds>, "v": <value>}`. These endpoints return the same shape:

- `/api/projects/<project>/metrics`. To narrow the result, add `&environment=production`.
- `/api/databases/<name>/metrics`.
- `/api/cluster/metrics`.

The charts of the operator come from `/api/operator/metrics`, with the keys `reconcileErrors`, `reconcileTime`, `apiLatency`, `apiRequests`, `queueDepth`, `statusWrites`, and `statusSkipped`. The cluster and operator endpoints take a workspace token, not a project token. See the [REST API](/docs/reference/rest-api/).

## Logs

### Open the logs

Open a project, and select its **Logs** tab. This tab merges the output of every service and database in the project into one timeline. Each line shows its time, the service that wrote it, and the text.

| Control | What it does |
| --- | --- |
| **Service** | Limit to one service or database. Default: **All services**. |
| **Environment** | Limit to one environment. Shows when the project has more than one environment. |
| **Search logs** | Show only lines that contain the text. Case does not matter. |
| **Level** | **All levels**, **Warnings and errors**, or **Errors only**. |
| **Logs from** | **What the nodes hold now** or **History (kept for 7 days)**. Shows only when the logging add-on is on. |
| **Time range** | Live: **Latest**, **Last 15 minutes**, **Last hour**, **Last 6 hours**, and **Last 24 hours**. History: **Last hour** to **Last 7 days**. |
| **Follow** | Live only. Streams new lines when they arrive. |
| **Include logs from before restarts** | Live only. Adds the previous run of any container that restarted. The previous run is what explains a crash loop or an out-of-memory kill. Not available when you follow the logs. |
| **Refresh** | Reads the logs again. |

Geass works out the level of a line from its text. A stated `level` or `severity` field wins. Then Geass looks for words:

- `error`, `fatal`, `panic`, `exception`, and `traceback` (error).
- `warn` (warning).
- `debug` or `trace`.

Lines with no recognizable level count as information.

### Live logs and history

| | What the nodes hold now | History |
| --- | --- | --- |
| Source | The container logs on each node, read through the Kubernetes API. | VictoriaLogs. Fluent Bit fills it from every node. |
| Needs | Nothing. | The logging add-on. |
| Pods that were replaced | Gone. You can include only the run before a restart. | Kept. |
| How far back | As much as the node still holds: up to five files of 50 MiB for each container. | 7 days by default, 1 to 90 days. |

Projects that require node tags have no history. Geass never sends their logs to the shared store. So the **History** view says "Log history is not kept for projects that require node tags, so their logs never leave the nodes they run on. Live logs are still available." See [Compliance node tags](/docs/guides/compliance-tags/#logs).

Geass sets the limit of five files of 50 MiB (`container-log-max-size` and `container-log-max-files`) in the K3s configuration of the nodes that it installs and joins. The Kubernetes default of 10 MiB for each file holds only minutes of logs of a busy service. A node that you joined yourself keeps the default.

### Limits

Live logs read these lines:

- The newest 200 lines of each container, up to 1 MiB for each container.
- 3,000 lines in total.
- Lines from at most 40 containers.

Geass cuts a line that is longer than 16 KiB. A read stops after 25 seconds. When any limit hides lines, the page says "Showing the newest lines. Narrow the search or time range to see more."

History returns 500 lines by default, and 3,000 lines at most. The **Level** filter works on the newest lines that the store returns. That is five times the limit, and at most 15,000 lines. So a rare level can come back empty when it is not in those lines.

**Follow** keeps the newest 3,000 lines in your browser. Every 10 seconds, it picks up pods that start later. It follows at most 40 containers.

### Read logs from a script

To read logs, use a workspace token that has the admin or observer role:

```bash
curl -sS -H "Authorization: Bearer $GEASS_TOKEN" \
  "https://geass.example.com/api/projects/morning-beach/logs?service=web&level=error&since=1h"
curl -sS -H "Authorization: Bearer $GEASS_TOKEN" \
  "https://geass.example.com/api/projects/morning-beach/logs/history?since=24h&q=timeout&limit=500"
```

`since` is a duration, such as `90m`, `24h`, or `168h`. There is no `d` unit. For live logs, `since` is up to 7 days. For history, `since` is up to 90 days. Both endpoints take `q` for the search text, `level`, and `environment`. The live endpoint also takes these parameters:

- `tail`: the number of lines to read from each container, from 1 to 1000. The default is 200.
- `previous=1`.

History takes `limit`, from 1 to 3000. The default is 500. The history menu of the dashboard stops at **Last 7 days**, but the API goes further.

A project token can read the text logs of its own services at `/api/apps/<service>/logs`. The response is JSON with one `lines` string. This string holds the last 200 lines of the `app` container of each pod. Each pod is under a `# <pod>` heading.

### Build and deploy logs

The **Deployments** tab of a service keeps the history of each deploy. Open a deploy to see **Details**, **Build Logs**, and **Deploy Logs**. A failed deploy shows the errors that Geass recorded. See [GitHub and builds](/docs/guides/github-and-builds/).

## Terminal

The **Console** tab of a service opens a shell in one of its running pods.

1. Open the service, and select **Console**.
2. Select a pod in the **Pod** menu. The menu lists only running pods. If the menu says "No running pods", the service is not running.
3. Click **Connect**. A terminal opens. The terminal uses `bash` when the image has it, and `sh` if not. A counter shows how much time the session has left.
4. To end the session, click **Disconnect**.

**Full screen** makes the terminal larger. **Copy kubectl command** copies `kubectl exec -it -n <namespace> <pod> -c <container> -- sh`.

A session lasts five minutes. Then the session ends with "Session ended: Session time limit reached". Geass records each session as a `GeassConsoleSession`. The record names the signed-in user, the pod, the container, the start, and the end. Geass does not store the output of the terminal. A session allows one open terminal. The log of the operator notes when a terminal opens and closes.

Only admins can open a terminal. Viewers see "Your viewer role cannot open consoles." Project tokens cannot use the terminal.

The **Console** tab of a database is a query box, not a shell. Enter one statement, and click **Run query**. Geass runs the statement in the first running pod of the database, and shows the output. Geass uses `psql -c`, `mysql -e`, or `redis-cli`. For SQLite, Geass sends a request to the HTTP API of rqlite. Redis commands cannot carry flags. The first running pod is not always the primary. On a highly available database, the statement can land on a replica.

From a script, send the statement with `POST`. Geass refuses a `GET` to the same address. The response is `{"output": "..."}`:

```bash
curl -sS -X POST https://geass.example.com/api/databases/orders/query \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  --data-urlencode "query=select count(*) from orders"
```

This request needs a workspace token with the admin role. Project tokens cannot reach this endpoint.

## What the add-ons install

The installer turns on both add-ons. Neither add-on has a switch in the dashboard. To change them, edit the cluster definition.

| Add-on | Installs | Where |
| --- | --- | --- |
| **Monitoring** (default on) | The `kube-prometheus-stack` chart (69.8.2): Prometheus, the Prometheus operator, kube-state-metrics, and a node exporter on each node. With the default `lite` profile, there is no Grafana and there are no default alert rules. Alertmanager is installed in both profiles. It sends the alerts of [Alerts](/docs/guides/alerts/). | Namespace `monitoring` |
| **Monitoring**, for the operator | A `ServiceMonitor` and a `PrometheusRule`, both named `geass-operator`. See [Metrics and alerts for the operator](#metrics-and-alerts-for-the-operator). | Namespace `monitoring` |
| **Logging** (opt-in, on after install) | The `victoria-logs-single` chart (0.13.10) and the `fluent-bit` chart (0.58.3): a VictoriaLogs server, and one Fluent Bit pod on every node. | Namespace `geass-logging` |

These details are important:

- Prometheus keeps 15 days, up to 4 GB, on a 5 GiB volume. Geass lets Prometheus pick up monitors that it did not label itself. So Prometheus scrapes the service monitors that Geass creates.
- A `PodMonitor` named `geass-ingress` makes Prometheus scrape the K3s Traefik. The **Requests** chart comes from this monitor. If your ingress controller is not Traefik, this monitor matches nothing.
- VictoriaLogs keeps logs for the retention period, up to 4 GiB, on a 6 GiB volume. Its memory limit is 384 MiB. Fluent Bit requests 10 millicores and 24 MiB on each node, with a memory limit of 96 MiB. Fluent Bit runs on every node, including reserved nodes.
- Fluent Bit collects every namespace, except the namespaces of every project that requires node tags, and except these namespaces: `kube-system`, `kube-public`, `kube-node-lease`, `local-path-storage`, `monitoring`, `longhorn-system`, `cnpg-system`, `moco-system`, `mariadb-system`, `cert-manager`, and `geass-logging`.
- The first time, Fluent Bit reads the log of each container from its beginning. So logs that were written before the add-on was installed show up. Fluent Bit remembers its place across restarts. The dashboard only asks for the namespaces of the project itself.
- The installation of the logging add-on never holds the cluster back. Geass logs a problem and retries.

### Metrics and alerts for the operator

The operator serves its own metrics on port 8443 over HTTPS, through the Service `geass-controller-manager-metrics-service` in `geass-system`. In addition to the reconcile, queue, and request metrics of the controller library, the operator adds these metrics:

| Metric | What it counts |
| --- | --- |
| `geass_status_writes_total` | Status updates, by resource `kind` and `result`: `written`, `skipped` (nothing had changed), or `failed`. |
| `geass_apiserver_client_request_duration_seconds` | A histogram of how long the Kubernetes API server took to answer the operator, by `verb`, `resource`, and `code`. |
| `geass_apiserver_client_requests_in_flight` | Requests sent and not yet answered. |
| `geass_reconcile_not_ready_total` | Reconciles that ended with a resource not ready, by `reason`. |
| `geass_provision_duration_seconds` | A histogram of how long a deploy of a service, a database, or a bucket took, by outcome `phase`. |

When the monitoring add-on is on, the cluster controller creates the objects that collect and watch these metrics:

- The `ServiceMonitor` `geass-operator` makes Prometheus scrape that Service every 30 seconds. The endpoint needs a login, so Prometheus sends the token of its own service account. A ClusterRoleBinding `geass-metrics-reader-prometheus` ships with the operator, and lets that account read the endpoint. The certificate of the operator is the self-signed certificate that the operator makes for itself. So Prometheus does not verify it.
- The `PrometheusRule` `geass-operator` defines seven alerts. Each alert waits for the problem to last for the time in the For column:

| Alert | Fires when | For |
| --- | --- | --- |
| `GeassOperatorDown` | The operator reports no metrics. Critical. | 5 minutes |
| `GeassReconcileErrors` | A controller fails more than 0.05 reconciles per second over 10 minutes. | 15 minutes |
| `GeassSlowReconciles` | The slowest one percent of the reconciles of a controller take more than 30 seconds. | 15 minutes |
| `GeassWorkqueueBacklog` | A Geass controller has more than 25 items waiting. | 15 minutes |
| `GeassOperatorMemoryHigh` | The memory of the operator stays above 700 MiB. This is most of the 1 GiB that the operator is allowed. See [Cluster size](/docs/operations/sizing/). | 15 minutes |
| `GeassAPIServerSlow` | The slowest one percent of the answers of the API server take more than a second. | 10 minutes |
| `GeassAPIServerErrors` | More than one in twenty requests to the API server fail. | 10 minutes |

These alerts go as **cluster problems** to the channels that an admin sets up under **Settings** → **Alerts**. So a channel that is not limited to projects gets a message when the operator is down, or when the operator is not getting through its work. See [Alerts](/docs/guides/alerts/). Geass makes more alerts than these, for builds, deploys, services, backups, nodes, crashing pods, and filling storage. These other alerts are in the `PrometheusRule` `geass-alerts`, which the Alerts page describes.

Prometheus loads these rules because Geass installs the monitoring chart with its rule selector open. By default, the chart selects only rules with the label `release: geass-kube-prometheus-stack`. The rules of Geass do not have this label. Clusters that were installed before this fix never loaded the rules: the alerts above existed, but Prometheus never evaluated them. The next update of the monitoring chart corrects this problem. Geass makes that update when Geass updates. The `full` profile leaves the other components of the chart, and its default rules, at the defaults of the chart. If you edit or delete the objects, the operator puts them back.

To change the add-ons, edit the cluster with `kubectl -n geass-system edit geasscluster default`, or with the patch below. The fragment shows only the fields that are involved. Do not apply the fragment as a whole manifest. `kubectl apply` can drop the other fields of the cluster.

```yaml title="cluster-addons.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassCluster
metadata:
  name: default
  namespace: geass-system
spec:
  addons:
    monitoring:
      enabled: true
      profile: lite        # lite (default) or full
    logging:
      enabled: true
      retentionDays: 14    # 1 to 90, default 7
```

```bash
kubectl -n geass-system patch geasscluster default --type merge \
  -p '{"spec":{"addons":{"logging":{"retentionDays":14}}}}'
```

The `full` profile applies the Prometheus settings of Geass on top of the defaults of the chart. These defaults include the other components of the chart. When you turn off logging, Geass uninstalls Fluent Bit and VictoriaLogs, so history is no longer available. When you turn off monitoring, Geass uninstalls the monitoring chart.

If Prometheus runs in a different location, set `spec.prometheusURL` on the `platform` object. To edit this object, run `kubectl -n geass-system edit geassplatformconfig platform`. You can also set the `GEASS_PROMETHEUS_URL` environment variable on the operator. To set the address of the log store, use `GEASS_LOGS_URL`. To send metrics and logs to stores outside the cluster, with a tenant and credentials, see the next section.

## Send metrics and logs outside the cluster

`spec.observability.external` on the `platform` object names a metrics store and a log store outside the cluster. Geass then sends to these stores, and also to the stores in the cluster. The dashboard reads from the outside stores instead. These are two uses of the setting:

- A platform that runs many clusters keeps their metrics and logs in multi-tenant stores of its own.
- A [dashboard that runs outside the cluster](/docs/operations/dashboard-only/) shows metrics and log history. This dashboard cannot reach Prometheus or VictoriaLogs inside the cluster.

Nothing changes until you set `spec.observability.external`, and every part of it is optional.

| Part | What Geass does with it | Needs |
| --- | --- | --- |
| `metrics.remoteWriteURL` | Prometheus remote-writes every sample that it keeps to this address. For example, `/api/v1/push` of Mimir or Cortex, `/api/v1/write` of VictoriaMetrics, or anything else that speaks the protocol. Prometheus keeps its own 15 days as before. | The monitoring add-on |
| `metrics.queryURL` | The dashboard reads every metric from this address: the charts, the overview numbers, and the measured use on the **Usage** page. It is the address that `/api/v1/query` and `/api/v1/query_range` are added to. For example, `https://mimir.example.com/prometheus` for Mimir, or `https://vmselect.example.com/select/12/prometheus` for VictoriaMetrics. | A Prometheus-compatible query API |
| `metrics.tenant`, `metrics.tenantHeader` | Sent with every write and every query. The header is `X-Scope-OrgID` (Mimir, Cortex, Loki), unless you name another header, such as `THANOS-TENANT`. Leave out the tenant for a store that takes it from the URL (the cluster version of VictoriaMetrics) or from the credentials. | |
| `logs.pushURL` | Fluent Bit also sends every line that it collects to this address, with the same fields as to the store in the cluster. Geass adds `/insert/jsonline`, so this value is the base address of the store. | The logging add-on |
| `logs.queryURL` | The dashboard reads log history from this address. Geass adds `/select/logsql/query`. | VictoriaLogs, or something in front of it that speaks its API |
| `logs.accountID`, `logs.projectID` | The VictoriaLogs tenant. These values are sent as the `AccountID` and `ProjectID` headers with every line and every query. Each value is `0` when you leave it out. | |
| `logs.retentionDays` | How long the store keeps logs. Log history reports this value instead of the retention of the logging add-on. | |
| `metrics.credentialRef`, `logs.credentialRef` | A Secret in `geass-system`. It has a `token` key for `Authorization: Bearer`, or `username` and `password` keys for basic authentication. Leave it out for a store that takes no credentials. | |

```yaml title="platform.yaml (the observability part)"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassPlatformConfig
metadata:
  name: platform
  namespace: geass-system
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

```bash title="The credentials"
kubectl -n geass-system create secret generic observability-credentials --from-literal=token="$TOKEN"
# or: --from-literal=username=acme --from-literal=password="$PASSWORD"
```

To set this value, use `kubectl -n geass-system edit geassplatformconfig platform` or a merge patch. The dashboard has no screen for it. The operator applies a change within seconds, and also a change to the Secret. Prometheus takes longer. The change from the operator reaches Prometheus through two steps: the kubelet refreshes the mounted Secret, and Prometheus reloads its configuration. On K3s v1.36.4+k3s1, the first remote write reached the store about 100 seconds after the setting was patched. **Change the setting one step at a time and wait for each**. For the reason, see "Do not change the setting several times in quick succession" later in this section.

**How the credentials travel.** Geass never writes the credentials into the settings of the add-ons. These settings are in a HelmChart, and anyone who can read HelmCharts can see them. Geass copies the keys that the store uses into the Secret `geass-external-metrics` in `monitoring` and `geass-external-logs` in `geass-logging`. The settings name the copy. The Prometheus operator reads the copy for Prometheus. Fluent Bit reads the copy as environment variables, so new credentials start new Fluent Bit pods. When you remove the setting, Geass removes the copies. Geass refuses a Secret with both a `token` and a `username` or `password`. Geass also refuses a Secret with neither.

**What the dashboard reads.** Every page uses a query address in place of the store of the cluster, for everyone who can see the page. A query address also wins over `spec.prometheusURL` and the environment variables. Without a query address, the dashboard reads the stores of the cluster as before, even while Geass writes to a different location.

The one exception is the three overview numbers that the bootstrap shows to a viewer: running pods, nodes, and CPU cores used. These numbers never read the platform settings. So they come from the Prometheus of the cluster, as they always did, and they show "unavailable" on a dashboard outside the cluster.

The queries are the same as for the stores of the cluster. Geass builds them from the namespaces of the project itself. When the outside store does not answer, the charts and log history say so ("The metrics store outside the cluster is not answering. Try again in a minute."). When the store refuses the credentials, they say that it "refused Geass's credentials (HTTP 401)". The dashboard does not show what the store wrote back. The dashboard never falls back to the store in the cluster.

**When something is wrong.** The operator ignores a store in these cases:

- An address that is not `http` or `https`.
- An address that holds a username or password.
- A query address or a `pushURL` that has a query string.
- A tenant header that Prometheus sets itself, such as `Authorization`, `Content-Type`, or `User-Agent`.
- A Secret that is missing or refused.

In each case, the operator logs the problem ("Ignored the external metrics store", "Ignored the external store: its credentials could not be read"), and leaves that store out. So the metrics and logs of the cluster continue as if the store were not set. The dashboard then says that the store "cannot be used". There is no status condition for this problem yet. Look in the log of the operator.

**Do not change the setting several times in quick succession.** A change to the settings of the add-ons makes the helm controller of K3s replace the install Job of the chart. On K3s v1.36.4+k3s1, three changes within about ten seconds left `helm-install-geass-kube-prometheus-stack` **suspended** (`spec.suspend: true`, and the events `Suspended Job ... for delete` from the HelmChart). The three changes were an address with a query string, a Secret that does not exist, and the removal of the setting. Then these things happened:

- The cluster said `AddonsReady` `False` (`AddonsPending`) and `Ready` `False`.
- Every project said `Ready` `False` ("Referenced GeassCluster is not ready").
- A new service or logical database waited with "project ... is not ready".
- The operator logged only "Waiting for monitoring add-on" every 30 seconds.

This state lasted about five minutes, until the suspended Job was deleted (`kubectl -n kube-system delete job helm-install-geass-kube-prometheus-stack`). The helm controller then made a new Job, which finished in 39 seconds. This problem happened one time. A later set, verify, and remove, with one change at a time and a wait for each change, did not cause it. The problem comes from the helm controller. But Geass shows it as a stall of the whole cluster, and does not say which Job.

These are the limits of the setting:

- **Alerts stay in the cluster.** Alertmanager still sends them, but the **Alerts** page reads Alertmanager in the cluster. So a dashboard outside the cluster shows Alertmanager as not reachable. Prometheus remote-writes the `ALERTS` series of the alerts that fire. So you can ask the outside store what fires, but the dashboard does not ask it yet.
- **Loki is not supported for logs.** The shipper writes the JSON lines of VictoriaLogs, and the dashboard asks in LogsQL.
- **Metrics of projects that require node tags are sent.** Geass does not collect their logs at all, so it does not send their logs either. But [compliance tags](/docs/guides/compliance-tags/) do not cover metrics.
- **The stores in the cluster stay.** If the outside store is the one that keeps history, lower the `retentionDays` of the logging add-on. The **Logs from** menu still labels history with the retention of the add-on. The API reports `logs.retentionDays`.
- **A store that does not take lines holds the shipper back.** Fluent Bit keeps up to 8 MB of lines on each node in memory for its two outputs, and retries each batch five times. While the outside store refuses the lines, that buffer fills. Lines can then reach the store in the cluster late, or be dropped. This behavior was not measured.
- **No custom certificate authority.** An `https` store must have a certificate that Prometheus and Fluent Bit trust by default.
- **One store of each.** There is no list of remote writes or outputs.

:::caution[Run on K3s against VictoriaMetrics and VictoriaLogs, not against Mimir]
The tests of Geass use fakes of the stores, of the input of the Prometheus operator, and of the input of Fluent Bit. Geass also ran on real K3s v1.36.4+k3s1 with the monitoring and logging add-ons: kube-prometheus-stack 69.8.2 with Prometheus v3.2.1, and Fluent Bit 5.1.3. It ran against single-node VictoriaMetrics v1.155.0 and VictoriaLogs v1.53.0 on the network of the machine. Each store was behind a proxy that recorded the headers that the store received:

- Prometheus remote-wrote with `X-Scope-OrgID` (the `tenant`) and a bearer token from the Secret. Fluent Bit pushed with `AccountID`, `ProjectID`, and the same token. The token was in the copies `geass-external-metrics` and `geass-external-logs`, and in no HelmChart.
- VictoriaLogs kept the lines under the tenant (`AccountID` 42, `ProjectID` 7), and none under the default tenant. Single-node VictoriaMetrics ignores the tenant header, so the test shows only that the header was sent.
- The metrics and log history of the dashboard came from the stores. Its `query_range` and `/select/logsql/query` calls reached the stores with the headers and the token. The stores still answered when the Prometheus and VictoriaLogs of the cluster were off. Log history said `retentionDays` 30 (the value of the store) instead of 7 (the value of the add-on).
- A store that answered 401 gave "refused Geass's credentials (HTTP 401)". A store that did not answer gave "is not answering". An address with a query string gave "cannot be used", and so did a `credentialRef` to a Secret that does not exist. Both also gave the log lines of the operator.
- The removal of the setting immediately removed both copies of the credentials. About 20 seconds later, the removal took `remoteWrite` off Prometheus. The dashboard read the stores of the cluster again.

**Not run:**

- Mimir, Cortex, or Thanos.
- A multi-tenant VictoriaMetrics.
- Loki, which is not supported.
- A dashboard that runs outside the cluster.
- A store with its own certificate authority.
- The `ALERTS` series.
- The behavior of the Fluent Bit buffer while a store refuses its lines.
:::

## Who can see what

| | Viewer | Admin | Project token | Workspace token |
| --- | --- | --- | --- | --- |
| Metrics | Yes | Yes | Its own services, one environment | By its role |
| Project logs and history | No | Yes | The text logs of its own services only | Admin and observer |
| Terminal and database console | No | Yes | No | Admin only |

A workspace token has the role admin, observer, or viewer. An observer reads everything that a viewer can read, and also logs, but changes nothing. The observer role is for a monitoring script or an agent that only watches. See [Users and tokens](/docs/guides/users-and-tokens/).

A project cannot read the metrics or logs of another project. Three layers prevent this:

- A project token reaches only services in its own project and environment. A service that the token cannot see answers "app not found", so names do not leak.
- Geass builds every metrics query and log query from the namespaces of the project itself, never from anything in the request.
- Network policies close Prometheus and the log store to everything except the platform. So the pods of a project cannot query them. See [Network isolation](/docs/guides/network-isolation/).

## Clusters at more than one provider

In a [multi-provider cluster](/docs/operations/multi-provider/):

- **Container metrics** (the service, project, and database charts) come from every node. Geass starts the monitoring operator with `--kubelet-node-address-priority=external`. So Prometheus reaches each kubelet on the public address of the node. This needs TCP 10250 open between nodes.
- **Host charts** (**Machines busy (CPU)** and **Machines memory in use**) cover only the node that runs monitoring. The node exporters publish on private addresses, and the other sites cannot reach these addresses.
- **Live logs** are read from the kubelet of each node through the control plane. The control plane uses the same TCP 10250.

## What happens under the hood

```bash
kubectl -n monitoring get pods
kubectl -n monitoring get servicemonitors,podmonitors,prometheusrules
kubectl -n monitoring get svc geass-kube-prometheus-stac-prometheus
kubectl -n geass-logging get pods,pvc
kubectl -n kube-system get helmcharts          # geass-kube-prometheus-stack, geass-victoria-logs, geass-fluent-bit
kubectl -n geass-system get geassconsolesessions
kubectl -n geass-system logs deploy/geass-controller-manager -c manager | grep "Console terminal"
```

The name of the Prometheus service, `geass-kube-prometheus-stac-prometheus`, is cut short on purpose. Helm shortens the release name to 26 characters, and Geass keeps the name that clusters already use. The dashboard reaches Prometheus at `http://geass-kube-prometheus-stac-prometheus.monitoring.svc:9090` and VictoriaLogs at `http://geass-victorialogs.geass-logging.svc:9428`.

## Troubleshooting

**Charts say "Metrics are not available yet".**
Prometheus is not installed, or the dashboard cannot reach it. Check `kubectl -n monitoring get pods`. Also check `kubectl -n geass-system get geasscluster default -o jsonpath='{.spec.addons.monitoring}'` for `enabled: false`. A custom address in `spec.prometheusURL` or `GEASS_PROMETHEUS_URL` must be reachable from the operator.

**Charts are empty for services on one node only.**
Prometheus cannot reach the kubelet of that node. In a multi-provider cluster, open TCP 10250 between nodes. On a single site, make sure that the node is ready.

**The Geass health charts are empty.**
These charts come from the metrics of the operator. Check that the `ServiceMonitor` exists (`kubectl -n monitoring get servicemonitor geass-operator`). Also check that Prometheus can read the endpoint of the operator. The ClusterRoleBinding `geass-metrics-reader-prometheus` must exist (`kubectl get clusterrolebinding geass-metrics-reader-prometheus`).

**The Requests chart is empty.**
Geass counts requests at the ingress. Check `kubectl -n monitoring get podmonitor geass-ingress`, and check that traffic arrives through Traefik. A service that only other services call has no requests.

**A node shows machine charts for one node only.**
This result is expected on a multi-provider cluster. See [Clusters at more than one provider](#clusters-at-more-than-one-provider).

**The Logs tab says "No logs match", or I cannot see output from a pod that was replaced.**
Live logs show only what the nodes still hold. Turn on **Include logs from before restarts**, or select **History** in **Logs from**.

**There is no Logs from menu, or History says "Log history is not set up on this cluster. Turn on the logging add-on to keep logs after pods are gone."**
The logging add-on is off. Turn on the add-on in the cluster definition, as described earlier on this page.

**History says "The log store is not answering yet. It may still be starting; try again in a minute."**
VictoriaLogs is starting, or it is not healthy. Check `kubectl -n geass-logging get pods`.

**Every project says "Referenced GeassCluster is not ready", and the cluster says `AddonsPending`, after the setting was changed.**
Look at the install Job of the monitoring chart: `kubectl -n kube-system get jobs`. If `helm-install-geass-kube-prometheus-stack` is `Suspended`, delete it (`kubectl -n kube-system delete job helm-install-geass-kube-prometheus-stack`). The helm controller then makes a new Job. See [Send metrics and logs outside the cluster](#send-metrics-and-logs-outside-the-cluster).

**Charts or history say the store "outside the cluster cannot be used", "refused Geass's credentials" or "is not answering".**
These messages come from the store that `spec.observability.external` names. See [Send metrics and logs outside the cluster](#send-metrics-and-logs-outside-the-cluster).

- "Cannot be used" means that the address, the tenant header, or the Secret of the store is not correct. The log of the operator says which: `kubectl -n geass-system logs deploy/geass-controller-manager -c manager | grep "external"`.
- "Refused" means that the store answered 401 or 403. Check the `token`, or the `username` and `password`, in the Secret. Also check the tenant.
- "Not answering" means that the dashboard could not reach the store.

**The Logs tab says "Your viewer role cannot read logs."**
Logs need an admin. Ask for the admin role.

**The terminal says "Could not open the terminal. The session may have ended; connect again."**
The five-minute session ended, or the pod stopped. Connect again. If you use your own proxy, the proxy must pass WebSocket connections.

**"pod is not running" or "a running pod and container are required".**
The pod stopped after the menu listed it and before you connected. Select a running pod.

## Related

- [Usage and capacity](/docs/guides/usage-and-capacity/)
- [Network isolation](/docs/guides/network-isolation/)
- [Compliance node tags](/docs/guides/compliance-tags/)
- [Users and tokens](/docs/guides/users-and-tokens/)
- [Troubleshooting](/docs/operations/troubleshooting/)
