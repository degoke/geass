---
title: "GeassCluster"
description: "Reference for GeassCluster: the K3s cluster Geass runs on, its add-ons (cert-manager, monitoring, Longhorn, logging, serverless), network and default region."
sidebar:
  order: 7
---

A `GeassCluster` stands for the K3s cluster that Geass manages. It holds the switches for the platform add-ons (cert-manager, monitoring, Longhorn storage, log collection and sleep-when-idle), the network mode, the default region for projects, and what the operator needs to join new workers: the cluster's API address and the Secret with the join token. The installer creates one, named `default`, in `geass-system`. You change it with `kubectl`. The dashboard writes only one field of it, the default region.

For the task-oriented view, see [How Geass works](/docs/overview/how-it-works/), [Add servers](/docs/getting-started/add-servers/) and [Storage](/docs/guides/storage/).

## Summary

| Property | Value |
| --- | --- |
| API version | `geass.geass.dev/v1alpha1` |
| Kind | `GeassCluster` |
| Plural | `geassclusters` |
| Scope | Namespaced. The installer creates it in `geass-system`. |
| Subresources | `status` |
| Printer column `Ready` | `.status.conditions[?(@.type=="Ready")].status` |
| Printer column `Phase` | `.status.phase` |
| Finalizer | None |

Geass expects one cluster. When a project is created and more than one `GeassCluster` exists, the dashboard uses the one named in `GeassPlatformConfig` `spec.defaultClusterRef`, or the first one.

## Examples

### What the installer applies

```yaml title="default-cluster.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassCluster
metadata:
  name: default
  namespace: geass-system
spec:
  version: v1
  serverURL: https://10.0.0.5:6443
  tokenSecretRef:
    name: geass-token
    namespace: geass-system
  addons:
    certManager:
      acmeEmail: ops@example.com
    longhorn:
      enabled: true
    logging:
      enabled: true
```

The installer writes the `certManager` block only when `GEASS_ACME_EMAIL` is set. Let's Encrypt refuses placeholder domains such as `example.com`, so use a real address. The installer leaves the other add-ons at their defaults, so cert-manager and monitoring are on.

### A multi-site cluster with the full monitoring profile

```yaml title="multi-site-cluster.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassCluster
metadata:
  name: default
  namespace: geass-system
spec:
  version: v1
  serverURL: https://203.0.113.10:6443
  tokenSecretRef:
    name: geass-token
    namespace: geass-system
  network:
    multiSite: true
  defaultRegion: eu
  addons:
    certManager:
      enabled: true
      acmeEmail: ops@example.com
    monitoring:
      enabled: true
      profile: full
    longhorn:
      enabled: true
    logging:
      enabled: true
      retentionDays: 14
    serverless:
      enabled: true
```

## Spec fields

| Field | Type | Required or default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.version` | string | Required | None | A label for the Geass cluster. The installer writes `v1`. It is not a K3s version and Geass does not use it to install anything. The one exception: when a worker joins and no control plane node reports a K3s version, Geass uses this value, but only if it looks like a K3s release such as `v1.31.4+k3s1`. |
| `spec.serverURL` | string | Required | None | The address workers join, for example `https://203.0.113.10:6443`. The installer writes the control plane's own address on its default route (`https://10.0.0.5:6443`, a private address on most cloud servers), and a multi-site install writes the public address instead. A loopback address (`127.0.0.1`, `localhost`, `::1`) is never used to join a worker: Geass uses the address the control plane node registered under. Set `serverURL` only to use a different address: see [Add servers](/docs/getting-started/add-servers/#set-the-address-workers-join). |
| `spec.tokenSecretRef` | SecretReference | Required | None | The Secret that holds the K3s join token. A standard Kubernetes `name` and `namespace` pair. The installer writes `geass-token` in `geass-system`. The token is read from the key `token`, then `value`, then `k3s-token`. |
| `spec.tokenSecretRef.name` | string | Optional | None | Name of the Secret. |
| `spec.tokenSecretRef.namespace` | string | Optional. Defaults to the cluster's namespace. | None | Namespace of the Secret. |
| `spec.addons.certManager.enabled` | boolean | CRD default `true` | None | Installs cert-manager. Turning it off removes the HelmChart. |
| `spec.addons.certManager.acmeEmail` | string | Optional. Empty by default. | Empty, or an address that matches `^[^@\s]+@[^@\s]+\.[^@\s]+$`. At most 254 characters | The contact address of the Let's Encrypt account, which also receives expiry notices. When it is set (and cert-manager is on), the operator creates and keeps the `letsencrypt-prod` ClusterIssuer that every ingress asks for. When it is empty, no issuer is created and no HTTPS certificate can be issued, which the `ClusterIssuerReady` condition reports. The installer sets it from `GEASS_ACME_EMAIL`. Let's Encrypt refuses placeholder domains such as `example.com`. |
| `spec.addons.monitoring.enabled` | boolean | CRD default `true` | None | Installs kube-prometheus-stack. Turning it off removes the HelmChart. |
| `spec.addons.monitoring.profile` | string | CRD default `lite` | Enum `lite`, `full` | `lite` turns off Grafana, the default rules and dashboards. `full` leaves those at the chart's defaults. Alertmanager is on in both: it sends the alerts of [Alerts](/docs/guides/alerts/). |
| `spec.addons.longhorn.enabled` | boolean | CRD default `false` | None | Installs Longhorn and the storage classes Geass uses. It needs iSCSI on every node. The installer sets it to `true`. Turning it off never uninstalls Longhorn. |
| `spec.addons.logging.enabled` | boolean | CRD default `false` | None | Installs log collection: a Fluent Bit pod per node and a VictoriaLogs server. The installer sets it to `true`. Turning it off removes both HelmCharts, and the logs they kept with them. |
| `spec.addons.logging.retentionDays` | integer (int32) | CRD default `7` | Minimum 1, maximum 90 | How long stored logs are kept. |
| `spec.addons.serverless.enabled` | boolean | CRD default `false` | None | Installs KEDA and its HTTP add-on, so that services can [sleep when idle](/docs/guides/scale-to-zero/). It is opt-in: the installer does not set it. It also lets the K3s Traefik route to a Service that is only a name (a `HelmChartConfig` named `traefik`, created only when none exists). Turning it off stops the operator managing it and never uninstalls KEDA. |
| `spec.network.multiSite` | boolean | `false` | None | The nodes are at different providers and reach each other over the internet. See [Network mode](#network-mode). |
| `spec.k3s.version` | string | Optional. Empty by default. | Empty, or a K3s release such as `v1.36.5+k3s1`: `^(v1\.[0-9]+\.[0-9]+\+k3s[0-9]+)?$`. At most 40 characters | The K3s release the **worker** nodes are brought to. When it is set and a worker runs an older one, the operator upgrades the workers one at a time over SSH: cordon, replace K3s, wait for the node to be ready on the new version, uncordon. It only moves workers to a version the control plane already runs, and it never touches the control plane, which the installer upgrades (`--k3s-version` sets this field for you). Empty means leave K3s alone. See [Upgrade Geass](/docs/operations/upgrading/#workers). |
| `spec.defaultRegion` | string | Optional | Pattern `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, max length 40 | The region of projects that do not choose one. Empty means such projects may run on any node. |

:::note
The Go types and the generated CRD agree for every field on this page.
:::

### Network mode

With `spec.network.multiSite: true` the overlay network between nodes is encrypted (the K3s WireGuard backend) and every node advertises its public address. Set it when you install, with the installer variables `GEASS_MULTI_SITE` and `GEASS_NODE_EXTERNAL_IP`. Changing it later means restarting every node, and Geass does not do that. The operator reads the field only in two places: when it joins a worker (the worker then installs WireGuard support, opens its ports if it can, and advertises its public address) and when it renders the monitoring chart (Prometheus then scrapes kubelets at their external addresses). Do not flip it on a running cluster.

## Status

| Field | Type | Meaning |
| --- | --- | --- |
| `status.phase` | string | `Ready` when the cluster is ready. Empty otherwise. |
| `status.release` | string | The Geass release of the operator that last reconciled the cluster, such as `v0.2.0`. The installer reads it before an update, to refuse a downgrade, and after it, to know that the new operator has taken over. Empty on a cluster whose operator has not been updated since releases were recorded. Shown as `RELEASE` by `kubectl get geasscluster`. |
| `status.schemaVersion` | integer | The level of data migrations applied to what the cluster stores. An operator that knows of fewer migrations than this refuses to start. See [Upgrade Geass](/docs/operations/upgrading/#how-an-update-stays-safe). |
| `status.k3s.controlPlaneVersion` | string | The oldest K3s version that a control plane node reports. Shown as `K3S`. |
| `status.k3s.upgrade` | object | Progress of an upgrade of the workers to `spec.k3s.version`: `phase` (`WaitingForControlPlane`, `Waiting`, `Upgrading`, `Complete`, `Failed`), `targetVersion`, `currentNode`, `step`, `startedAt` and a `message` that says what it is doing or why it is not. Absent while `spec.k3s.version` is empty. |
| `status.reconcileRetry` | object | Failure counters (`reconcileFailures`, `reconcileFailureStarted`, `reconcileFailureGeneration`). |
| `status.conditions` | list | `Ready`, `AddonsReady`, `ClusterIssuerReady` while cert-manager is on, `NetworkPolicyEnforced` once the check that the cluster acts on network policies has run (`True`, `False` or `Unknown`, see [Network isolation](/docs/guides/network-isolation/#check-that-the-cluster-enforces-policies)) and, while `spec.k3s.version` is set, `K3sUpgraded`: `True` once every worker runs that version, otherwise `False` with the phase of `status.k3s.upgrade` as its reason (so `kubectl wait --for=condition=K3sUpgraded` works). |

| Condition | Reason | Status | Meaning |
| --- | --- | --- | --- |
| `AddonsReady` | `AddonsReady` | `True` | Every enabled add-on that gates readiness is installed and running. The message is `AddonsReady is ready`. |
| `AddonsReady` | `AddonsPending` | `False` | At least one add-on is still installing. The message is `AddonsReady is not ready`. |
| `AddonsReady` | `ReconcileError` | `False` | Geass could not apply an add-on. The message is the error. |
| `Ready` | `ClusterReady` | `True` | The message is `Geass cluster is ready`. `status.phase` is `Ready`. |
| `Ready` | `ClusterPending` | `False` | The add-ons are not ready. The message is `Geass cluster is not ready`. Geass checks again every 30 seconds. |
| `Ready` | `ReconcileError` | `False` | An error while applying add-ons. The message is the error. |
| `Ready` | `ProvisionFailed` | `False` | Geass gave up after repeated errors. The message is `Provisioning failed after repeated errors. Update settings or platform configuration, then save or redeploy to try again.` The controller then stops until you change the spec. |

`ClusterIssuerReady` says whether HTTPS certificates can be issued. It is informational: it never changes `Ready`, and a cluster without an issuer still runs workloads. The operator removes the condition while `spec.addons.certManager.enabled` is `false`.

| Status | Reason | Meaning |
| --- | --- | --- |
| `True` | `IssuerReady` | The `letsencrypt-prod` ClusterIssuer exists and cert-manager reports it ready. The message is `The letsencrypt-prod ClusterIssuer is ready to issue certificates`. |
| `False` | `WaitingForCertManager` | cert-manager is not ready yet, or its kinds are not served yet. Checked again after 30 seconds. |
| `False` | `ACMEEmailMissing` | No issuer exists and `spec.addons.certManager.acmeEmail` is empty. The message names the field and the installer variable. |
| `False` | `IssuerRegistering` | The issuer exists and cert-manager has not yet reported on its ACME account. |
| `False` | `IssuerNotReady` | cert-manager reports the issuer is not ready. The message carries cert-manager's reason, for example Let's Encrypt refusing the email. |
| `False` | `IssuerApplyFailed` | The operator could not create or update the issuer. It retries, and its log says why. |
| `False` | `IssuerAccessDenied` | The operator's ClusterRole may not manage `clusterissuers.cert-manager.io`. |
| `True` | `ExternallyManaged` | An issuer named `letsencrypt-prod` exists that Geass did not create (it lacks the label `geass.dev/managed-by=geass`), and cert-manager reports it ready. Geass leaves it as it is. If cert-manager does not report it ready, the reason is `IssuerRegistering` or `IssuerNotReady` instead. |
| `True` | `ManagedByAnotherCluster` | The issuer belongs to another `GeassCluster` that still exists (label `geass.dev/cluster`), and cert-manager reports it ready. Geass leaves it as it is. |

The retry rules are the same as for [GeassDatabase](/docs/reference/resources/geass-database/#ready-condition-and-retries). A cluster has no **Retry** button: to resume after `ProvisionFailed`, change something in the spec.

What gates `Ready`:

- cert-manager and monitoring, when enabled: the HelmChart's install Job must have succeeded and a pod of the release must be ready.
- Longhorn, when enabled: its HelmChart must be installed, the CSI driver `driver.longhorn.io` must be registered, and every schedulable node must have a ready `longhorn-manager` pod. Only then does Geass write the storage classes.
- Logging never gates readiness. It is installed on the side, and a problem is only logged.
- The serverless add-on never gates readiness either. It comes up on the side, and the operator looks again every 20 seconds until it has.

:::caution
With Longhorn on, a node that has joined but has no ready Longhorn manager yet keeps `AddonsReady` and `Ready` at `False` until the manager starts. While that lasts, resources that reconcile report `project "<project>" cluster "<cluster>" is not ready`. This clears by itself, but it can take a few minutes while a new node pulls images. Once any node is a [compliance node](/docs/guides/compliance-tags/) and every other node is tagged for general volumes, only those other nodes need a ready manager for this check. This comes from reading the controller, not from timing it on a real cluster.
:::

## What the dashboard writes

| Where | What it does |
| --- | --- |
| **Settings** > **Usage** > **Regions** > **Default region** | Writes `spec.defaultRegion` of the first cluster. The choices are the regions your nodes carry, and **No default (any node)**. It is only shown once at least one node belongs to a region. |
| **Settings** > **Usage** > **Nodes** | Shows each node's role, region, tags, CPU, memory, pressure and status. **Edit** changes the node: see [GeassNode](/docs/reference/resources/geass-node/). It does not write the `GeassCluster`. |
| **Settings** > **General** | Shows the number of clusters. |

Everything else is `kubectl`. The dashboard does not toggle add-ons or edit `serverURL`.

## Editing by hand

It is safe to edit the add-on toggles, `defaultRegion`, `serverURL` and `k3s.version` with `kubectl`. The operator applies the change on its next pass.

```bash title="Set the address workers join"
kubectl -n geass-system patch geasscluster default --type merge \
  -p '{"spec":{"serverURL":"https://203.0.113.10:6443"}}'
```

- Do not delete the cluster. It has no finalizer and no owner references, so deleting it leaves the add-on HelmCharts and everything else running. Projects stop validating, because they need their cluster to exist and be `Ready`.
- Do not change `spec.network.multiSite` on a running cluster.
- Turning an add-on off removes its HelmChart, which uninstalls the release. For logging that deletes the stored logs. For Longhorn and the serverless add-on it does nothing: take them out by hand if you really want them gone. For Longhorn, move every volume first. For serverless, switch each service's own sleep setting off first: the operator decides whether a service can sleep by whether KEDA's `HTTPScaledObject` kind exists in the cluster, so services keep sleeping after the add-on is switched off.
- The controller does not rewrite the `GeassCluster` spec. It writes status. An update run by the installer does not apply the cluster object again either: it only sets `spec.k3s.version` when you ask for a K3s upgrade.
- To ask for another try of a worker upgrade that failed, annotate the cluster: `kubectl -n geass-system annotate geasscluster default geass.dev/retry-k3s-upgrade=now`. The operator removes the annotation when it has acted on it.

## What happens under the hood

Each add-on is a HelmChart object in `kube-system`. K3s's Helm controller runs a Job `helm-install-<name>` for it and installs the release.

| Add-on | HelmChart | Namespace | Notes |
| --- | --- | --- | --- |
| cert-manager | `geass-cert-manager` | `cert-manager` | Chart v1.17.2 with `installCRDs: true`. The operator then creates the `letsencrypt-prod` ClusterIssuer when `acmeEmail` is set, see below. |
| Monitoring | `geass-kube-prometheus-stack` | `monitoring` | Chart 69.8.2. Prometheus keeps 15 days or 4GB on a 5Gi volume. The names are pinned so the dashboard can find the Prometheus Service. |
| Longhorn | `geass-longhorn` | `longhorn-system` | Chart 1.12.1. The chart's own storage class and the Longhorn UI are switched off. |
| Logging | `geass-victoria-logs` and `geass-fluent-bit` | `geass-logging` | The store is bounded to 4GiB on a 6Gi volume and your retention. The shippers skip system namespaces. |

Beyond the charts the cluster controller keeps these in place:

- **Storage classes**, once Longhorn is ready: `geass-replicated` (1, 2 or 3 copies, by the number of schedulable nodes), `geass-single` (one copy kept on the node the pod runs on, for databases that replicate themselves), for each region `geass-replicated-<region>` restricted to that region's nodes and, for each set of node tags a project requires, or that the cluster object storage server runs on, `geass-tagged-<hash>` restricted to the nodes that carry them. Existing volumes are raised, never lowered, to the replica count as nodes join.
- **Longhorn node tags**: each Longhorn node gets `geass-region-<region>` and `geass-tag-<tag>` tags that mirror its Kubernetes node, which is how a storage class can name a region or tags. Once any node is a compliance node, every other node also gets `geass-general`, and the global and regional classes name it, so other projects' volume copies stay off compliance nodes.
- **The ClusterIssuer** `letsencrypt-prod`, once cert-manager is ready and `spec.addons.certManager.acmeEmail` is set: ACME against Let's Encrypt's production server, solved over HTTP-01 through the `traefik` ingress class, with the account key in the Secret `letsencrypt-prod-account-key` in the `cert-manager` namespace. It carries the labels `geass.dev/managed-by=geass` and `geass.dev/cluster=<cluster>`. The operator watches it and puts it back when it is deleted or edited. An issuer without the managed-by label is never touched. If you later empty `acmeEmail`, the issuer Geass made stays in place so that existing certificates can renew.
- **Network policies** `geass-restrict-metrics-store` in `monitoring` and `geass-restrict-log-store` in `geass-logging`, so only the platform can read metrics and logs.
- **A PodMonitor** `geass-ingress` in `monitoring`, so Prometheus scrapes Traefik and the request charts fill in.
- **A ServiceMonitor and a PrometheusRule** named `geass-operator` in `monitoring`, while monitoring is on, so Prometheus scrapes the operator's own metrics and holds seven alerts on them. See [Logs, metrics and terminal](/docs/guides/observability/#metrics-and-alerts-for-the-operator).
- **A PrometheusRule** `geass-alerts` in `monitoring`, while monitoring is on: the alerts on builds, deploys, services, backups, nodes, crashing pods and filling storage that [Alerts](/docs/guides/alerts/) sends. The operator also makes the `AlertmanagerConfig` `geass-alerts` in `geass-system` from the channels in the platform configuration, when there are any.
- **The serverless add-on**, while `spec.addons.serverless.enabled` is true: the HelmCharts `geass-keda` and `geass-keda-http` (KEDA 2.21.0 and the HTTP add-on 0.16.0, installed into `keda`) and, when none exists, a `HelmChartConfig` `traefik` in `kube-system` that sets `providers.kubernetesIngress.allowExternalNameServices: true`. They install in that order, and the operator looks again every 20 seconds until both are up. A `traefik` configuration that already exists is left alone, and the operator logs what to add to it.
- **The K3s upgrade of the workers**, while `spec.k3s.version` is set. A separate controller does it, because a node's upgrade takes minutes of SSH and the cluster controller has one worker. It cordons and uncordons nodes with the annotation `geass.dev/k3s-upgrade-cordon`, writes `status.k3s`, and does nothing to a worker that has no `GeassNode` (so no SSH key). See [Upgrade Geass](/docs/operations/upgrading/#workers).
- **The network policy check**, a background task of the operator: once the cluster is ready it runs a short check in the namespace `geass-netcheck` that the cluster's network plugin acts on policies, records the answer as the condition `NetworkPolicyEnforced`, and notes when it ran in the annotation `geass.dev/network-policy-checked-at`. It runs again after a day, after half an hour when it could not tell, and when the annotation `geass.dev/recheck-network-policy` is set on the cluster, which it removes.
- **The release and the schema level**, written to `status.release` and `status.schemaVersion` at the start of every reconcile, and the ConfigMap `geass-schema` that the operator keeps in `geass-system` before any controller starts.
- **The admission policy** `geass-required-node-tags`, a ValidatingAdmissionPolicy and its binding, created the first time a project requires node tags and never removed. See [Compliance node tags](/docs/guides/compliance-tags/).

```bash title="Inspect the cluster"
kubectl -n geass-system get geassclusters
kubectl -n geass-system get geasscluster default -o jsonpath='{range .status.conditions[*]}{.type}={.status} {.reason}: {.message}{"\n"}{end}'

# The add-ons
kubectl -n kube-system get helmcharts
kubectl -n kube-system get jobs | grep helm-install
kubectl get storageclass
kubectl -n longhorn-system get pods

# The nodes and their regions
kubectl get nodes -L topology.kubernetes.io/region,topology.kubernetes.io/zone
```

## Troubleshooting

### `Ready` is `False` with `ClusterPending`

An add-on is still installing. List the HelmCharts and their install Jobs (the commands above). A Job that keeps failing or a pod in `ImagePullBackOff` is the usual cause. Geass only logs `Waiting for cert-manager add-on`, `Waiting for monitoring add-on` or `Waiting for Longhorn add-on`; the condition does not name the add-on.

### Longhorn never becomes ready

Longhorn needs the iSCSI initiator on every node. The installer and the worker join step install it, but the worker step is optional, so a worker where it failed has no Longhorn manager. Check `kubectl -n longhorn-system get pods -o wide` for a node without a ready `longhorn-manager`. Also check `kubectl get csidriver driver.longhorn.io`. Until every schedulable node has a ready manager, the storage classes are not created and databases report `Storage is starting`.

### The reason is `ProvisionFailed`

Read the add-on that failed: `AddonsReady` still carries the last error. Fix the cause, then change a field in the spec so that `metadata.generation` changes. The controller resumes when the generation changes. Saving the same values again does not change it.

### A worker cannot join: `cannot reach the cluster API at <url>`

The rest of the message says why: a refused connection or a timeout means 6443/tcp is closed between the worker and the control plane, and a loopback address means `spec.serverURL` is `127.0.0.1` and no control plane node has an address to use instead. See [Add servers](/docs/getting-started/add-servers/#set-the-address-workers-join).

### Projects say `project "<project>" cluster "<cluster>" is not ready`

The `GeassCluster` is not `Ready`. See the cases above.

### HTTPS certificates are not issued

Read the `ClusterIssuerReady` condition: `kubectl -n geass-system get geasscluster default -o jsonpath='{.status.conditions[?(@.type=="ClusterIssuerReady")]}'`. `ACMEEmailMissing` means no contact address is set: patch `spec.addons.certManager.acmeEmail` with a real address. `IssuerNotReady` carries cert-manager's reason, most often Let's Encrypt refusing the address (it rejects placeholder domains such as `example.com`). Then check `kubectl get clusterissuer letsencrypt-prod`. See [Domains and TLS](/docs/guides/domains-and-tls/).

### A worker's K3s upgrade is `Failed` or `Waiting`

`status.k3s.upgrade.message` says which node and why. `Waiting` is the upgrade holding back on purpose while a node is not ready, a replicated volume is degraded or a worker has not reported its K3s version yet: it goes on by itself when that is fixed. `Failed` stops at the node that did not come back ready on the new version, which stays cordoned. Look at the node (`kubectl describe node <name>`, `journalctl -u k3s-agent` on the machine), then annotate the cluster to try again. See [Upgrade Geass](/docs/operations/upgrading/#workers).

### A service cannot sleep and says `AddonNotInstalled`

The serverless add-on is off or not finished. Check `kubectl -n kube-system get helmcharts geass-keda geass-keda-http` and `kubectl -n keda get pods`. See [Sleep when idle](/docs/guides/scale-to-zero/#troubleshooting).

### Metrics or logs screens are empty

Check that `monitoring` or `logging` is enabled and that the pods in `monitoring` or `geass-logging` run. After you turn logging off, the screens that read history have nothing to show.

## Related

- [How Geass works](/docs/overview/how-it-works/)
- [Add servers](/docs/getting-started/add-servers/)
- [Storage](/docs/guides/storage/)
- [Observability](/docs/guides/observability/)
- [Scaling and placement](/docs/guides/scaling-and-placement/)
- [Sleep when idle](/docs/guides/scale-to-zero/)
- [Compliance node tags](/docs/guides/compliance-tags/)
- [GeassNode](/docs/reference/resources/geass-node/)
- [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/)
- [GeassHAReadiness](/docs/reference/resources/geass-ha-readiness/)
- [Configuration](/docs/reference/configuration/)
