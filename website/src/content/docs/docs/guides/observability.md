---
title: "Logs, metrics and terminal"
description: "Find logs, metrics and the terminal in the dashboard, learn what is collected and for how long, what the add-ons install, and who can see what."
---

Geass gives you three ways to see what your services are doing, and a fourth to see how Geass itself is doing. **Metrics** chart CPU, memory, network and requests. **Logs** show what services print, live and, with the logging add-on, from before their pods were replaced. The **terminal** opens a shell inside a running service. **Geass health** charts the operator. This page covers where each lives, what is collected, how long it is kept, and who may use it.

## Before you start

- **Metrics** need the monitoring add-on, which installs Prometheus. It is on by default.
- **Log history** needs the logging add-on, which is off in the cluster definition by default and turned on by the installer. Live logs need nothing extra.
- Metrics are open to every signed-in user. Logs and the terminal need an admin, and logs are also open to a token with the observer role. See [Who can see what](#who-can-see-what).

## Metrics

### Where to find them

| Where | Open | Charts |
| --- | --- | --- |
| A project | The project's **Metrics** tab. It follows the environment selected in the environment bar. | CPU, Memory, Network out, Network in |
| A service | The service's **Metrics** tab. | CPU, Memory, Network out, Network in, Requests |
| A database | The database's **Monitor** tab. | CPU, Memory, Disk used, Network out, Network in |
| The cluster | **Usage**, under **Cluster metrics**. | CPU used by services, Memory used by services, Machines busy (CPU), Machines memory in use, Network out, Network in |
| Geass itself | **Usage**, under **Geass health**. | Failed reconciles, Reconcile time (slowest 1%), API server answer time (slowest 1%), Requests to the API server, Work waiting, Status writes, Status writes avoided |

Each page has a **Time range** menu: **Last 15 min**, **Last hour**, **Last 6 hours** or **Last 24 hours**. Charts refresh every 15 seconds. A chart with no samples reads "No data for this time range".

A database that runs outside the cluster has nothing to measure: "This database runs outside the cluster, so there is nothing here to measure."

### What each chart measures

| Chart | Measures |
| --- | --- |
| **CPU** | CPU used by the containers, as an average rate over two minutes, in vCPU. |
| **Memory** | Memory the containers hold (the working set), summed. |
| **Network out**, **Network in** | Bytes the pods send and receive, as a two-minute rate. This counts all traffic, including calls to other services in the cluster, not only what crosses the public ingress. |
| **Requests** | HTTP requests per second that the ingress controller (Traefik) sends to the service. Calls between services inside the cluster are not counted. |
| **Disk used** | Space used on the database's volume. |
| **Machines busy (CPU)** | How busy the machines are, from the node exporter. |
| **Machines memory in use** | Memory in use on the machines, from the node exporter. |

The **Geass health** charts measure the operator:

| Chart | Measures |
| --- | --- |
| **Failed reconciles** | Reconciles that ended in an error, per second, summed over every controller. |
| **Reconcile time (slowest 1%)** | The 99th percentile of how long a reconcile takes, over five minutes. |
| **API server answer time (slowest 1%)** | The 99th percentile of how long the Kubernetes API server took to answer the operator, over five minutes. Watches are left out: one stays open for minutes. |
| **Requests to the API server** | Requests the operator sends, per second. |
| **Work waiting** | The depth of the work queues of the Geass controllers. |
| **Status writes** | Status updates the operator wrote, per second. |
| **Status writes avoided** | Status updates the operator compared with what was already stored and skipped because nothing had changed. |

The operator compares a status with the stored one before it writes. That is why a steady cluster shows few writes and many avoided ones.

Points are spaced 15 seconds apart over 15 minutes, one minute over an hour, five minutes over 6 hours and 15 minutes over 24 hours. Charts show up to 24 hours, though Prometheus keeps more.

### Your own metrics

A service can also publish its own Prometheus metrics. In the service's **Settings**, open **Deploy** and switch on **Enable metrics** under **Metrics** ("Expose Prometheus metrics from /metrics. Requires Prometheus Operator in the cluster."). This applies at once. Geass then creates a `ServiceMonitor` named `<service>-metrics` that has Prometheus scrape `/metrics` on the service's `http` port. A manifest can change the path and port with `spec.metrics.path` and `spec.metrics.port`. Prometheus stores those series, but the dashboard does not chart them. Use Prometheus itself to query them.

The `ServiceMonitor` kind comes with the monitoring add-on. With monitoring switched off, a service that has **Enable metrics** on cannot become ready, and its `Ready` condition reads "metrics are enabled but Prometheus Operator is not installed in this cluster".

### How long it is kept

Prometheus keeps 15 days of samples, capped at 4 GB, on a 5 GiB volume. Whichever limit comes first applies, and the oldest samples are dropped.

### Read metrics from a script

```bash
curl -sS -H "Authorization: Bearer $GEASS_TOKEN" \
  "https://geass.example.com/api/apps/web/metrics?window=1h"
```

`window` is `15m`, `1h`, `6h` or `24h`. The response holds `available` and a `series` object whose keys are `cpu`, `memory`, `network`, `networkIn` and, for services, `requests`. Each series is a list of `{"t": <unix seconds>, "v": <value>}`. The same shape comes from `/api/projects/<project>/metrics` (add `&environment=production` to narrow it), `/api/databases/<name>/metrics` and `/api/cluster/metrics`. The operator's own charts come from `/api/operator/metrics`, with the keys `reconcileErrors`, `reconcileTime`, `apiLatency`, `apiRequests`, `queueDepth`, `statusWrites` and `statusSkipped`. The cluster and operator endpoints take a workspace token, not a project token. See the [REST API](/docs/reference/rest-api/).

## Logs

### Open the logs

Open a project and choose its **Logs** tab. It merges the output of every service and database in the project into one timeline. Each line shows its time, the service that wrote it and the text.

| Control | What it does |
| --- | --- |
| **Service** | Limit to one service or database. Default **All services**. |
| **Environment** | Limit to one environment. Shown when the project has more than one. |
| **Search logs** | Show only lines that contain the text, ignoring case. |
| **Level** | **All levels**, **Warnings and errors** or **Errors only**. |
| **Logs from** | **What the nodes hold now** or **History (kept for 7 days)**. Shown only when the logging add-on is on. |
| **Time range** | Live: **Latest**, **Last 15 minutes**, **Last hour**, **Last 6 hours**, **Last 24 hours**. History: **Last hour** to **Last 7 days**. |
| **Follow** | Live only. Streams new lines as they arrive. |
| **Include logs from before restarts** | Live only. Adds the previous run of any container that has restarted, which is what explains a crash loop or an out-of-memory kill. Not available while following. |
| **Refresh** | Reads again. |

A line's level is worked out from its text: a stated `level` or `severity` field wins, then words such as `error`, `fatal`, `panic`, `exception` and `traceback` (error), `warn` (warning) and `debug` or `trace`. Lines with no recognisable level count as information.

### Live logs and history

| | What the nodes hold now | History |
| --- | --- | --- |
| Source | The container logs on each node, read through the Kubernetes API. | VictoriaLogs, filled from every node by Fluent Bit. |
| Needs | Nothing. | The logging add-on. |
| Pods that were replaced | Gone. Only the run before a restart can be included. | Kept. |
| How far back | As much as the node still holds: up to five files of 50 MiB per container. | 7 days by default, 1 to 90 days. |

Projects that require node tags have no history: their logs are never sent to the shared store, so the **History** view says "Log history is not kept for projects that require node tags, so their logs never leave the nodes they run on. Live logs are still available." See [Compliance node tags](/docs/guides/compliance-tags/#logs).

Geass sets the five-file, 50 MiB limit (`container-log-max-size` and `container-log-max-files`) in the K3s configuration of the nodes it installs and joins. The Kubernetes default of 10 MiB per file would hold only minutes of a busy service. A node you joined yourself keeps the default.

### Limits

Live logs read the newest 200 lines of each container, up to 1 MiB per container, 3,000 lines in all, from at most 40 containers. A line longer than 16 KiB is cut. A read stops after 25 seconds. When any limit hides lines, the page says "Showing the newest lines. Narrow the search or time range to see more."

History returns 500 lines by default and at most 3,000. The **Level** filter works on the newest lines the store returns (five times the limit, at most 15,000), so a rare level can come back empty when it is not in them.

**Follow** keeps the newest 3,000 lines in your browser. It picks up pods that start later, every 10 seconds, and follows at most 40 containers.

### Read logs from a script

With a workspace token that has the admin or observer role:

```bash
curl -sS -H "Authorization: Bearer $GEASS_TOKEN" \
  "https://geass.example.com/api/projects/morning-beach/logs?service=web&level=error&since=1h"
curl -sS -H "Authorization: Bearer $GEASS_TOKEN" \
  "https://geass.example.com/api/projects/morning-beach/logs/history?since=24h&q=timeout&limit=500"
```

`since` is a duration written like `90m`, `24h` or `168h` (there is no `d` unit): up to 7 days live and up to 90 days for history. Both endpoints take `q` for the search text, `level` and `environment`. The live endpoint also takes `tail`, the lines to read from each container (1 to 1000, default 200), and `previous=1`. History takes `limit` (1 to 3000, default 500). The dashboard's history menu stops at **Last 7 days**, though the API goes further.

A project token can read the text logs of its own services at `/api/apps/<service>/logs`. The response is JSON with one `lines` string that holds the last 200 lines of each pod's `app` container, each pod under a `# <pod>` heading.

### Build and deploy logs

A service's **Deployments** tab keeps the history of each deploy. Open one to see **Details**, **Build Logs** and **Deploy Logs**. A failed deploy shows the errors Geass recorded. See [GitHub and builds](/docs/guides/github-and-builds/).

## Terminal

The **Console** tab of a service opens a shell in one of its running pods.

1. Open the service and choose **Console**.
2. Pick a pod in the **Pod** menu. It lists running pods only. If it says "No running pods", the service is not running.
3. Choose **Connect**. A terminal opens, with `bash` when the image has it and `sh` otherwise. A counter shows how long the session has left.
4. Choose **Disconnect** to end it. **Full screen** enlarges the terminal. **Copy kubectl command** copies `kubectl exec -it -n <namespace> <pod> -c <container> -- sh`.

A session lasts five minutes, then ends with "Session ended: Session time limit reached". Each session is recorded as a `GeassConsoleSession` that names the signed-in user, the pod, the container, the start and the end. The terminal's output is not stored. A session allows one open terminal. The operator's log notes when a terminal opens and closes.

Only admins can open a terminal. Viewers see "Your viewer role cannot open consoles." Project tokens cannot use it.

A database's **Console** tab is a query box, not a shell. Type one statement and choose **Run query**. Geass runs it in the database's first running pod with `psql -c`, `mysql -e`, `rqlite -e` or `redis-cli` and shows the output. Redis commands may not carry flags. The first running pod is not always the primary: on a highly available database the statement can land on a replica.

From a script, send the statement with `POST`; a `GET` to the same address is refused. The response is `{"output": "..."}`:

```bash
curl -sS -X POST https://geass.example.com/api/databases/orders/query \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  --data-urlencode "query=select count(*) from orders"
```

This needs a workspace token with the admin role. Project tokens cannot reach it.

## What the add-ons install

The installer turns on both add-ons. Neither has a dashboard switch. You change them on the cluster definition.

| Add-on | Installs | Where |
| --- | --- | --- |
| **Monitoring** (default on) | The `kube-prometheus-stack` chart (69.8.2): Prometheus, the Prometheus operator, kube-state-metrics and a node exporter on each node. With the default `lite` profile there is no Grafana, no Alertmanager and no default alert rules. | Namespace `monitoring` |
| **Monitoring**, for the operator | A `ServiceMonitor` and a `PrometheusRule`, both named `geass-operator`. See [Metrics and alerts for the operator](#metrics-and-alerts-for-the-operator). | Namespace `monitoring` |
| **Logging** (opt-in, on after install) | The `victoria-logs-single` chart (0.13.10) and the `fluent-bit` chart (0.58.3): a VictoriaLogs server, and one Fluent Bit pod on every node. | Namespace `geass-logging` |

Details that matter:

- Prometheus keeps 15 days, capped at 4 GB, on a 5 GiB volume. Geass lets Prometheus pick up monitors it did not label itself, so service monitors that Geass creates are scraped.
- A `PodMonitor` named `geass-ingress` has Prometheus scrape the K3s Traefik. It is where the **Requests** chart comes from. It matches nothing if your ingress controller is not Traefik.
- VictoriaLogs keeps logs for the retention period, up to 4 GiB, on a 6 GiB volume, with a memory limit of 384 MiB. Fluent Bit asks for 10 millicores and 24 MiB per node, with a memory limit of 96 MiB. It runs on every node, reserved ones included.
- Fluent Bit collects every namespace except `kube-system`, `kube-public`, `kube-node-lease`, `local-path-storage`, `monitoring`, `longhorn-system`, `cnpg-system`, `cert-manager` and `geass-logging`, and the namespaces of every project that requires node tags. It reads each container's log from its beginning the first time, so logs written before the add-on was installed show up, and it remembers its place across restarts. The dashboard only ever asks for a project's own namespaces.
- Installing the logging add-on never holds the cluster back. A problem is logged and retried.

### Metrics and alerts for the operator

The operator serves its own metrics on port 8443 over HTTPS (the Service `geass-controller-manager-metrics-service` in `geass-system`). Besides the controller library's reconcile, queue and request metrics, it adds:

| Metric | What it counts |
| --- | --- |
| `geass_status_writes_total` | Status updates, by resource `kind` and `result`: `written`, `skipped` (nothing had changed) or `failed`. |
| `geass_apiserver_client_request_duration_seconds` | A histogram of how long the Kubernetes API server took to answer the operator, by `verb`, `resource` and `code`. |
| `geass_apiserver_client_requests_in_flight` | Requests sent and not yet answered. |
| `geass_reconcile_not_ready_total` | Reconciles that ended with a resource not ready, by `reason`. |
| `geass_provision_duration_seconds` | A histogram of how long a deploy of a service, database or bucket took, by outcome `phase`. |

With the monitoring add-on on, the cluster controller creates the objects that collect and watch them:

- The `ServiceMonitor` `geass-operator` has Prometheus scrape that Service every 30 seconds. The endpoint needs a login, so Prometheus sends its own service account's token. A ClusterRoleBinding `geass-metrics-reader-prometheus`, which ships with the operator, lets that account read it. The operator's certificate is the self-signed one it makes for itself, so Prometheus does not verify it.
- The `PrometheusRule` `geass-operator` defines seven alerts, each of which waits for the problem to last:

| Alert | Fires when | For |
| --- | --- | --- |
| `GeassOperatorDown` | The operator reports no metrics. Critical. | 5 minutes |
| `GeassReconcileErrors` | A controller fails more than 0.05 reconciles per second over 10 minutes. | 15 minutes |
| `GeassSlowReconciles` | The slowest one percent of a controller's reconciles take over 30 seconds. | 15 minutes |
| `GeassWorkqueueBacklog` | A Geass controller has more than 25 items waiting. | 15 minutes |
| `GeassOperatorMemoryHigh` | The operator's memory stays above 700 MiB, most of the 1 GiB it is allowed. See [Cluster size](/docs/operations/sizing/). | 15 minutes |
| `GeassAPIServerSlow` | The slowest one percent of the API server's answers take over a second. | 10 minutes |
| `GeassAPIServerErrors` | More than one in twenty requests to the API server fail. | 10 minutes |

The `lite` profile has no Alertmanager, so nothing in Geass sends these anywhere. The `full` profile leaves Alertmanager and the chart's other components at the chart's defaults. The operator puts the two objects back if you edit or delete them.

To change the add-ons, edit the cluster (`kubectl -n geass-system edit geasscluster default`, or the patch below). The fragment shows only the fields involved. Do not apply it as a whole manifest: `kubectl apply` can drop the cluster's other fields.

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

The `full` profile applies Geass's Prometheus settings on top of the chart's own defaults, which include its other components. Switching logging off uninstalls Fluent Bit and VictoriaLogs, so history stops being available. Switching monitoring off uninstalls the monitoring chart.

If Prometheus runs elsewhere, set `spec.prometheusURL` on the `platform` object (`kubectl -n geass-system edit geassplatformconfig platform`), or set the `GEASS_PROMETHEUS_URL` environment variable on the operator. The log store address can be set with `GEASS_LOGS_URL`.

## Who can see what

| | Viewer | Admin | Project token | Workspace token |
| --- | --- | --- | --- | --- |
| Metrics | Yes | Yes | Its own services, one environment | By its role |
| Project logs and history | No | Yes | The text logs of its own services only | Admin and observer |
| Terminal and database console | No | Yes | No | Admin only |

A workspace token has the role admin, observer or viewer. An observer reads everything a viewer can, and also logs, but changes nothing. It is the role for a monitoring script or an agent that only watches. See [Users and tokens](/docs/guides/users-and-tokens/).

A project cannot read another project's metrics or logs, at three layers:

- A project token reaches only services in its own project and environment. A service it cannot see answers "app not found", so names do not leak.
- Every metrics and log query is built from the project's own namespaces, never from anything in the request.
- Network policies close Prometheus and the log store to everything but the platform, so a project's pods cannot query them. See [Network isolation](/docs/guides/network-isolation/).

## Clusters at more than one provider

In a [multi-provider cluster](/docs/operations/multi-provider/):

- **Container metrics** (the service, project and database charts) come from every node. Geass starts the monitoring operator with `--kubelet-node-address-priority=external`, so Prometheus reaches each kubelet on the node's public address. That needs TCP 10250 open between nodes.
- **Host charts** (**Machines busy (CPU)** and **Machines memory in use**) cover only the node that runs monitoring. The node exporters publish on private addresses, which the other sites cannot reach.
- **Live logs** are read from each node's kubelet through the control plane, which uses the same TCP 10250.

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

The Prometheus service name `geass-kube-prometheus-stac-prometheus` is cut short on purpose: Helm shortens the release name to 26 characters and Geass keeps the name that clusters already use. The dashboard reaches Prometheus at `http://geass-kube-prometheus-stac-prometheus.monitoring.svc:9090` and VictoriaLogs at `http://geass-victorialogs.geass-logging.svc:9428`.

## Troubleshooting

**Charts say "Metrics are not available yet".**
Prometheus is not installed or the dashboard cannot reach it. Check `kubectl -n monitoring get pods`, and `kubectl -n geass-system get geasscluster default -o jsonpath='{.spec.addons.monitoring}'` for `enabled: false`. A custom address in `spec.prometheusURL` or `GEASS_PROMETHEUS_URL` must be reachable from the operator.

**Charts are empty for services on one node only.**
Prometheus cannot reach that node's kubelet. In a multi-provider cluster, open TCP 10250 between nodes. On a single site, check the node is ready.

**The Geass health charts are empty.**
They come from the operator's own metrics. Check that the `ServiceMonitor` exists (`kubectl -n monitoring get servicemonitor geass-operator`) and that Prometheus may read the operator's endpoint: the ClusterRoleBinding `geass-metrics-reader-prometheus` must exist (`kubectl get clusterrolebinding geass-metrics-reader-prometheus`).

**The Requests chart is empty.**
Requests are counted at the ingress. Check `kubectl -n monitoring get podmonitor geass-ingress` and that traffic arrives through Traefik. A service that only other services call has no requests.

**A node shows machine charts for one node only.**
Expected on a multi-provider cluster. See [Clusters at more than one provider](#clusters-at-more-than-one-provider).

**The Logs tab says "No logs match", or I cannot see output from a pod that was replaced.**
Live logs show only what nodes still hold. Turn on **Include logs from before restarts**, or switch **Logs from** to **History**.

**There is no Logs from menu, or History says "Log history is not set up on this cluster. Turn on the logging add-on to keep logs after pods are gone."**
The logging add-on is off. Enable it on the cluster (see above).

**History says "The log store is not answering yet. It may still be starting; try again in a minute."**
VictoriaLogs is starting or unhealthy. Check `kubectl -n geass-logging get pods`.

**The Logs tab says "Your viewer role cannot read logs."**
Logs need an admin. Ask for the admin role.

**The terminal says "Could not open the terminal. The session may have ended; connect again."**
The five-minute session ended or the pod stopped. Connect again. Behind your own proxy, the proxy must pass WebSocket connections.

**"pod is not running" or "a running pod and container are required".**
The pod stopped between listing and connecting. Pick a running pod.

## Related

- [Usage and capacity](/docs/guides/usage-and-capacity/)
- [Network isolation](/docs/guides/network-isolation/)
- [Compliance node tags](/docs/guides/compliance-tags/)
- [Users and tokens](/docs/guides/users-and-tokens/)
- [Troubleshooting](/docs/operations/troubleshooting/)
