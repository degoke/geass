---
title: "GeassCluster"
description: "Reference for GeassCluster: the K3s cluster that Geass runs on, its add-ons, network mode, default region, and the data that workers use to join."
sidebar:
  order: 7
---

A `GeassCluster` represents the K3s cluster that Geass manages. It holds these settings:

- The switches for the platform add-ons: cert-manager, monitoring, Longhorn storage, log collection, and sleep-when-idle.
- The network mode.
- The default region for projects.
- Whether the dashboard answers at the public IP address of the server before it has a domain.
- What the operator needs to join new workers: the API address of the cluster, and the Secret with the join token.

The installer creates one `GeassCluster`, named `default`, in `geass-system`. You change it with `kubectl`. The dashboard writes only one field of it: the default region.

For the task-oriented view, see [How Geass works](/docs/overview/how-it-works/), [Add servers](/docs/getting-started/add-servers/), and [Storage](/docs/guides/storage/).

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

Geass expects one cluster. When you create a project and more than one `GeassCluster` exists, the dashboard uses the cluster named in `GeassPlatformConfig` `spec.defaultClusterRef`. If that field is not set, the dashboard uses the first cluster.

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
  dashboard:
    publicIP: true
```

The installer writes the `certManager` block only when `GEASS_ACME_EMAIL` is set. Use a real address, because Let's Encrypt refuses placeholder domains such as `example.com`. The installer leaves the other add-ons at their defaults, so cert-manager and monitoring are on.

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
| `spec.version` | string | Required | None | A label for the Geass cluster. The installer writes `v1`. It is not a K3s version, and Geass does not use it to install anything. The one exception: when a worker joins and no control plane node reports a K3s version, Geass uses this value. Geass uses it only if it looks like a K3s release, such as `v1.31.4+k3s1`. |
| `spec.serverURL` | string | Required | None | The address that workers join, for example `https://203.0.113.10:6443`. The installer writes the address of the control plane on its default route (`https://10.0.0.5:6443`, a private address on most cloud servers). A multi-site install writes the public address instead. Geass never uses a loopback address (`127.0.0.1`, `localhost`, `::1`) to join a worker. Instead, Geass uses the address that the control plane node registered with. Set `serverURL` only to use a different address. See [Add servers](/docs/getting-started/add-servers/#set-the-address-workers-join). |
| `spec.tokenSecretRef` | SecretReference | Required | None | The Secret that holds the K3s join token. A standard Kubernetes `name` and `namespace` pair. The installer writes `geass-token` in `geass-system`. Geass reads the token from the key `token`, then `value`, then `k3s-token`. |
| `spec.tokenSecretRef.name` | string | Optional | None | Name of the Secret. |
| `spec.tokenSecretRef.namespace` | string | Optional. Defaults to the namespace of the cluster. | None | Namespace of the Secret. |
| `spec.addons.certManager.enabled` | boolean | CRD default `true` | None | Installs cert-manager. When you turn it off, Geass removes the HelmChart. |
| `spec.addons.certManager.acmeEmail` | string | Optional. Empty by default. | Empty, or an address that matches `^[^@\s]+@[^@\s]+\.[^@\s]+$`. At most 254 characters | The contact address of the Let's Encrypt account. This address also gets expiry notices. When it is set and cert-manager is on, the operator creates and keeps the `letsencrypt-prod` ClusterIssuer that every ingress asks for. When it is empty, the operator creates no issuer, and no HTTPS certificate can be issued. The `ClusterIssuerReady` condition reports this. The installer sets the field from `GEASS_ACME_EMAIL`. Let's Encrypt refuses placeholder domains such as `example.com`. |
| `spec.addons.monitoring.enabled` | boolean | CRD default `true` | None | Installs kube-prometheus-stack. When you turn it off, Geass removes the HelmChart. |
| `spec.addons.monitoring.profile` | string | CRD default `lite` | Enum `lite`, `full` | `lite` turns off Grafana, the default rules, and the default dashboards. `full` leaves those at the chart defaults. Alertmanager is on in both profiles. It sends the alerts of [Alerts](/docs/guides/alerts/). |
| `spec.addons.longhorn.enabled` | boolean | CRD default `false` | None | Installs Longhorn and the storage classes that Geass uses. Longhorn needs iSCSI on every node. The installer sets this field to `true`. When you turn it off, Geass never uninstalls Longhorn. |
| `spec.addons.logging.enabled` | boolean | CRD default `false` | None | Installs log collection: a Fluent Bit pod on each node, and a VictoriaLogs server. The installer sets this field to `true`. When you turn it off, Geass removes both HelmCharts, and the logs that they kept. |
| `spec.addons.logging.retentionDays` | integer (int32) | CRD default `7` | Minimum 1, maximum 90 | How long Geass keeps stored logs. |
| `spec.addons.serverless.enabled` | boolean | CRD default `false` | None | Installs KEDA and its HTTP add-on, so that services can [sleep when idle](/docs/guides/scale-to-zero/). It is opt-in: the installer does not set it. It also lets the K3s Traefik route to a Service that is only a name. For this, Geass creates a `HelmChartConfig` named `traefik`, but only when none exists. When you turn it off, the operator stops managing the add-on, and it never uninstalls KEDA. |
| `spec.network.multiSite` | boolean | `false` | None | The nodes are at different providers, and they reach each other over the internet. See [Network mode](#network-mode). |
| `spec.network.mesh` | object | Optional | None | How Geass joins nodes at different sites, some behind a NAT, into one network. If you leave it out, nothing changes. `multiSite` alone is the same WireGuard mesh that it always was, and a cluster that sets neither field keeps one private network. When you set it, it turns on the checks of the mesh that the operator does. The `MeshReady` condition then includes a probe of the pod networks of the other nodes. Also, the operator refuses a worker while the control plane is not on the mesh. Set it when you install. See [Mesh networking](/docs/operations/mesh-networking/) |
| `spec.network.mesh.provider` | string | `WireGuard` | `WireGuard` or `Tailscale` | What carries the traffic: the K3s `wireguard-native` backend, or a Tailscale or Headscale network through the K3s `vpn-auth` |
| `spec.network.mesh.tailscale` | object | Required with `Tailscale`, refused with `WireGuard` | A rule of the CRD | What a node needs to join the tailnet |
| `spec.network.mesh.tailscale.authKeySecretRef` | string | Required with the object | A DNS name, 1 to 253 characters | A Secret in `geass-system` that holds the auth key under the key `authKey`. Geass copies the key to `/etc/rancher/k3s/vpn-auth` (mode 600) on every node. Geass never shows the key in a status, an event, or the log |
| `spec.network.mesh.tailscale.controlServerURL` | string | Optional. Empty means the Tailscale service | `https://` and a host, with an optional port and path. No user name, query, fragment, comma, or space. At most 253 characters | A Headscale server (or a different Tailscale-compatible control server) |
| `spec.network.controlPlaneFirewall` | object | Optional | None | The cloud firewall in front of the control plane. When it is set, Geass opens this firewall to each machine that a pool creates (TCP 6443, and UDP 51820 in a WireGuard mesh) before the machine joins. Geass removes the address again when the machine is removed. See [Let Geass open the control plane](/docs/guides/node-pools/#let-geass-open-the-control-plane). |
| `spec.network.controlPlaneFirewall.connectionRef` | string | Required with the object | Not empty | The [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/), for `Compute`, at the cloud where the control plane runs. It can be at a different cloud from the cloud that creates the machines. Only an AWS or a Hetzner connection can change a firewall. |
| `spec.network.controlPlaneFirewall.target` | string | Required with the object | Not empty | The ID of the firewall at the cloud: a security group such as `sg-0abc123`, or the ID of a Hetzner firewall. |
| `spec.network.controlPlaneFirewall.region` | string | Optional | None | The location of the firewall, for a cloud that answers for each region (AWS). |
| `spec.k3s.version` | string | Optional. Empty by default. | Empty, or a K3s release such as `v1.36.5+k3s1`: `^(v1\.[0-9]+\.[0-9]+\+k3s[0-9]+)?$`. At most 40 characters | The K3s release for the **worker** nodes. When it is set and a worker runs an older release, the operator upgrades the workers one at a time over SSH. For each worker, it cordons the node, replaces K3s, waits until the node is ready on the new version, and uncordons the node. The operator moves workers only to a version that the control plane already runs. It never touches the control plane: the installer upgrades the control plane (`--k3s-version` sets this field for you). Empty means that Geass leaves K3s alone. See [Upgrade Geass](/docs/operations/upgrading/#workers). |
| `spec.lostNodeAfter` | duration | Optional. 15 minutes when left out. | A duration such as `30m` or `2h`. `0s` turns it off | How long a node can be not ready before Geass considers it lost. Then Geass makes a database with one instance, or a database with volume copies on other nodes, start on a different node. Geass also replaces an instance of a highly available Postgres database whose volume was only on the lost node: it removes the volume and builds a new instance from the primary. Set `0s` before you shut down a node for longer than the limit. See [When a node is lost](/docs/operations/high-availability/#what-geass-does-after-fifteen-minutes). |
| `spec.dashboard.publicIP` | boolean | Optional. Off when left out. The installer sets `true` on a new cluster. | None | Serves the dashboard over HTTPS at the public IPv4 address of the control plane, with a Let's Encrypt certificate for that address, until a dashboard domain is verified. It needs cert-manager on. When it is off, or after the domain is verified, the operator deletes the objects that serve the address. The installer `dashboard-link` command sets it to `true`. See [The dashboard at the public IP address](/docs/operations/installer/#the-dashboard-at-the-public-ip-address). |
| `spec.defaultRegion` | string | Optional | Pattern `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, max length 40 | The region of projects that do not select a region. Empty means that such projects can run on any node. |

:::note
The Go types and the generated CRD agree for every field on this page.
:::

### Network mode

When `spec.network.multiSite` is `true`, the overlay network between nodes is encrypted (the K3s WireGuard backend), and every node advertises its public address. Set this field when you install, with the installer variables `GEASS_MULTI_SITE` and `GEASS_NODE_EXTERNAL_IP`.

`spec.network.mesh` is the newer way to say the same thing. It is also the only way to ask for a Tailscale network. The installer writes it for `GEASS_MESH=wireguard` and `GEASS_MESH=tailscale`. With a mesh, these things occur:

- A Tailscale worker advertises its address on the tailnet.
- A WireGuard worker advertises its `spec.eip`.
- The operator refuses a join that cannot work, and it writes the `MeshReady` condition. See [Mesh networking](/docs/operations/mesh-networking/#what-geass-refuses-and-what-it-reports).

If you change the network mode later, you must restart every node, and Geass does not do that. The operator reads the field in only two places:

- When it joins a worker. The worker then installs WireGuard support, opens its ports if it can, and advertises its public address.
- When it renders the monitoring chart. Prometheus then scrapes kubelets at their external addresses.

Do not change the network mode on a running cluster.

Geass reads `spec.network.controlPlaneFirewall` only when a machine that Geass creates at a cloud is about to join. Without this field, the control plane must already accept the new machines on TCP 6443, and on UDP 51820 in a multi-site cluster. With this field, Geass does these things through the connection:

- It adds one rule for the address of the machine.
- It marks the rule as its own, with the description `geass` on AWS or `geass-access` on Hetzner.
- It never changes a rule that it did not add.
- It removes its rules when the machine goes away.

If the connection of a node cannot do this, the node fails with a message that tells you so, and Geass deletes its machine.

```bash
kubectl -n geass-system patch geasscluster default --type merge \
  -p '{"spec":{"network":{"controlPlaneFirewall":{"connectionRef":"aws-compute","target":"sg-0abc123","region":"eu-north-1"}}}}'
```

## Status

| Field | Type | Meaning |
| --- | --- | --- |
| `status.phase` | string | `Ready` when the cluster is ready. Empty otherwise. |
| `status.release` | string | The Geass release of the operator that last reconciled the cluster, such as `v0.2.0`. The installer reads it before an update, to refuse a downgrade. The installer reads it again after the update, to know that the new operator has taken over. Empty on a cluster whose operator has not been updated since Geass started to record releases. `kubectl get geasscluster` shows it as `RELEASE`. |
| `status.schemaVersion` | integer | The level of data migrations applied to what the cluster stores. An operator that knows fewer migrations than this level refuses to start. See [Upgrade Geass](/docs/operations/upgrading/#how-an-update-stays-safe). |
| `status.k3s.controlPlaneVersion` | string | The oldest K3s version that a control plane node reports. Shown as `K3S`. |
| `status.k3s.upgrade` | object | Progress of an upgrade of the workers to `spec.k3s.version`: `phase` (`WaitingForControlPlane`, `Waiting`, `Upgrading`, `Complete`, `Failed`), `targetVersion`, `currentNode`, `step`, `startedAt`, and a `message` that tells you what the upgrade does or why it does not continue. Absent while `spec.k3s.version` is empty. |
| `status.addons` | list | How each add-on that you can turn on and off is doing. Each item has `name` (`serverless`, `longhorn`, `monitoring`, or `logging`), `state` (`Off`, `Installing`, `Ready`, or `Failed`), `reason` (`InstallFailed`, `InstallSuspended`, or `NoHelmController`, for `Failed`), `message`, and `installed`. The **Add-ons** page of the dashboard and `geass addons` show it. |
| `status.dashboardPublicURL` | string | The HTTPS address of the dashboard at the public IP address, for example `https://203.0.113.10`, after its certificate is issued. Empty otherwise. |
| `status.reconcileRetry` | object | Failure counters (`reconcileFailures`, `reconcileFailureStarted`, `reconcileFailureGeneration`). |
| `status.conditions` | list | `Ready`. `AddonsReady`. `ClusterIssuerReady`, while cert-manager is on. `DashboardPublicIP`, while `spec.dashboard.publicIP` is `true` and cert-manager is on. `MeshReady`, while `spec.network.mesh` is set. `NetworkPolicyEnforced`, after the check that the cluster acts on network policies has run (`True`, `False`, or `Unknown`, see [Network isolation](/docs/guides/network-isolation/#check-that-the-cluster-enforces-policies)). `K3sUpgraded`, while `spec.k3s.version` is set: `True` after every worker runs that version, otherwise `False` with the phase of `status.k3s.upgrade` as its reason. Thus, `kubectl wait --for=condition=K3sUpgraded` works. |

| Condition | Reason | Status | Meaning |
| --- | --- | --- | --- |
| `AddonsReady` | `AddonsReady` | `True` | Every enabled add-on that gates readiness is installed and running. The message is `AddonsReady is ready`. |
| `AddonsReady` | `AddonsPending` | `False` | At least one add-on is still installing. The message is `AddonsReady is not ready`. |
| `AddonsReady` | `ReconcileError` | `False` | Geass could not apply an add-on. The message is the error. |
| `Ready` | `ClusterReady` | `True` | The message is `Geass cluster is ready`. `status.phase` is `Ready`. |
| `Ready` | `ClusterPending` | `False` | The add-ons are not ready. The message is `Geass cluster is not ready`. Geass checks again every 30 seconds. |
| `Ready` | `ReconcileError` | `False` | An error occurred while Geass applied the add-ons. The message is the error. |
| `Ready` | `ProvisionFailed` | `False` | Geass stopped after repeated errors. The message is `Provisioning failed after repeated errors. Update settings or platform configuration, then save or redeploy to try again.` The controller then stops until you change the spec. |

`MeshReady` exists only while `spec.network.mesh` is set. It tells you if the mesh is in place on every node:

- `True`, with the reason `MeshConfigured`, when every node advertises an external address. Also, when the operator could probe, a connection from its pod must get an answer from the pod network of the other nodes. This probe is one path from one point, so it does not test that traffic flows between sites. The message tells you what the operator probed, or why no probe ran.
- Otherwise `False`, with a message and one of these reasons: `InvalidMeshSettings`, `AuthKeyUnusable`, `ControlPlaneNotOnMesh`, `MeshNeedsAttention`, or `OverlayUnreachable`. `OverlayUnreachable` means that the [overlay check](/docs/operations/mesh-networking/#the-overlay-check) got no answer from the nodes that the message names, two times in sequence.

The annotation `geass.dev/mesh-overlay-check=off` on the cluster turns off the probe. The overlay check ran one time, on EC2, with these results:

- It named every blocked UDP 51820 and every tailnet rule that it was tried on.
- It gave no false alarm during a 35-minute soak.
- It gave two kinds of false report: for a host firewall that denies incoming traffic, and for a worker that has no pod yet.
- Its first probe was late.

The fixes since that run are tested only with fakes. For more information, see [what the run found](/docs/operations/mesh-networking/#what-the-run-on-ec2-found). `MeshReady` never changes `Ready`.

`ClusterIssuerReady` tells you if HTTPS certificates can be issued. It is for information only: it never changes `Ready`, and a cluster without an issuer still runs workloads. The operator removes the condition while `spec.addons.certManager.enabled` is `false`.

| Status | Reason | Meaning |
| --- | --- | --- |
| `True` | `IssuerReady` | The `letsencrypt-prod` ClusterIssuer exists, and cert-manager reports that it is ready. The message is `The letsencrypt-prod ClusterIssuer is ready to issue certificates`. |
| `False` | `WaitingForCertManager` | cert-manager is not ready yet, or the API server does not serve its kinds yet. Geass checks again after 30 seconds. |
| `False` | `ACMEEmailMissing` | No issuer exists, and `spec.addons.certManager.acmeEmail` is empty. The message names the field and the installer variable. |
| `False` | `IssuerRegistering` | The issuer exists, and cert-manager has not yet reported on its ACME account. |
| `False` | `IssuerNotReady` | cert-manager reports that the issuer is not ready. The message gives the reason from cert-manager, for example that Let's Encrypt refused the email. |
| `False` | `IssuerApplyFailed` | The operator could not create or update the issuer. The operator tries again, and its log tells you why. |
| `False` | `IssuerAccessDenied` | The ClusterRole of the operator does not have permission to manage `clusterissuers.cert-manager.io`. |
| `True` | `ExternallyManaged` | An issuer named `letsencrypt-prod` exists that Geass did not create (it does not have the label `geass.dev/managed-by=geass`), and cert-manager reports that it is ready. Geass leaves it as it is. If cert-manager does not report that it is ready, the reason is `IssuerRegistering` or `IssuerNotReady` instead. |
| `True` | `ManagedByAnotherCluster` | The issuer belongs to a different `GeassCluster` that still exists (label `geass.dev/cluster`), and cert-manager reports that it is ready. Geass leaves it as it is. |

The retry rules are the same as for [GeassDatabase](/docs/reference/resources/geass-database/#ready-condition-and-retries). A cluster has no **Retry** button. To continue after `ProvisionFailed`, change something in the spec.

These add-ons gate `Ready`:

- cert-manager and monitoring, when enabled. The install Job of the HelmChart must succeed, and a pod of the release must be ready.
- Longhorn, when enabled. Its HelmChart must be installed, the CSI driver `driver.longhorn.io` must be registered, and every schedulable node must have a ready `longhorn-manager` pod. Only then does Geass write the storage classes.

These add-ons do not gate `Ready`:

- Logging never gates readiness. It installs separately, and Geass only logs a problem.
- The serverless add-on also never gates readiness. It starts separately, and the operator looks again every 20 seconds until it is up.

:::caution
When Longhorn is on, a node that has joined but has no ready Longhorn manager yet keeps `AddonsReady` and `Ready` at `False` until the manager starts. During that time, resources that reconcile report `project "<project>" cluster "<cluster>" is not ready`. This state clears without action from you, but it can take a few minutes while a new node pulls images. A special case applies after any node is a [compliance node](/docs/guides/compliance-tags/) and every other node is tagged for general volumes. Then only those other nodes need a ready manager for this check. This information comes from a reading of the controller code. Nobody has timed it on a real cluster.
:::

### `DashboardPublicIP`

`DashboardPublicIP` tells you if the dashboard answers at the public IP address. It is for information only: it never changes `Ready`. The operator removes the condition, and empties `status.dashboardPublicURL`, while `spec.dashboard.publicIP` is off or cert-manager is off. While the operator sets up the address, it checks again every 30 seconds. After that, it checks every 10 minutes.

| Status | Reason | Meaning |
| --- | --- | --- |
| `True` | `Ready` | The certificate is issued, and the dashboard answers at the address. The message is `The dashboard answers at <url>`. |
| `False` | `WaitingForCertManager` | cert-manager is not ready yet. |
| `False` | `NoPublicAddress` | The cluster has no public IPv4 address. A private address, or an address in `100.64.0.0/10` (carrier-grade NAT or Tailscale), does not count. |
| `False` | `IssuingCertificate` | Let's Encrypt has not issued the certificate yet. The message says that port 80 must be open to the internet, and gives the reason from cert-manager. |
| `False` | `DomainVerified` | The dashboard has a verified domain, so the operator does not serve it at the IP address. |
| `False` | `TLSStoreInUse` | A Traefik `TLSStore` named `default` that Geass did not create is in `geass-system`. Geass leaves it as it is. |
| `False` | `TraefikMissing` | The Traefik `IngressRoute` and `TLSStore` kinds are not installed. |
| `False` | `ApplyFailed` | The operator could not read or create an object. The message names it, and the operator log tells you why. |

## What the dashboard writes

| Where | What it does |
| --- | --- |
| **Settings** > **Usage** > **Regions** > **Default region** | Writes `spec.defaultRegion` of the first cluster. The options are the regions that your nodes have, and **No default (any node)**. The dashboard shows this setting only after at least one node belongs to a region. |
| **Settings** > **Usage** > **Nodes** | Shows the role, region, tags, CPU, memory, pressure, and status of each node. **Edit** changes the node: see [GeassNode](/docs/reference/resources/geass-node/). It does not write the `GeassCluster`. |
| **Settings** > **General** | Shows the number of clusters. |

For all other changes, use `kubectl`. The dashboard does not turn add-ons on or off, and it does not edit `serverURL`.

## Editing by hand

You can safely edit the add-on switches, `defaultRegion`, `serverURL`, `k3s.version`, and `dashboard.publicIP` with `kubectl`. The operator applies the change on its next pass.

```bash title="Set the address workers join"
kubectl -n geass-system patch geasscluster default --type merge \
  -p '{"spec":{"serverURL":"https://203.0.113.10:6443"}}'
```

- Do not delete the cluster. It has no finalizer and no owner references. Thus, if you delete it, the add-on HelmChart objects and everything else continue to run. Projects stop validating, because they need their cluster to exist and to be `Ready`.
- Do not change `spec.network.multiSite` on a running cluster.
- When you turn an add-on off, Geass removes its HelmChart, which uninstalls the release. The result is different for each add-on:
  - For logging, this deletes the stored logs.
  - For Longhorn and the serverless add-on, it does nothing. If you really want to remove them, remove them by hand:
    - For Longhorn, move every volume first.
    - For serverless, first turn off the sleep setting of each service. The operator decides if a service can sleep by whether the KEDA `HTTPScaledObject` kind exists in the cluster. Thus, services continue to sleep after you turn off the add-on.
- The controller does not rewrite the `GeassCluster` spec. It writes status. An update that the installer runs also does not apply the cluster object again. The installer only sets `spec.k3s.version` when you ask for a K3s upgrade.
- To try a failed worker upgrade again, annotate the cluster: `kubectl -n geass-system annotate geasscluster default geass.dev/retry-k3s-upgrade=now`. The operator removes the annotation after it acts on it.

## What happens under the hood

Each add-on is a HelmChart object in `kube-system`. The K3s Helm controller runs a Job `helm-install-<name>` for it and installs the release.

| Add-on | HelmChart | Namespace | Notes |
| --- | --- | --- | --- |
| cert-manager | `geass-cert-manager` | `cert-manager` | Chart v1.21.3 with `installCRDs: true`. The operator then creates the `letsencrypt-prod` ClusterIssuer when `acmeEmail` is set. See the list below. |
| Monitoring | `geass-kube-prometheus-stack` | `monitoring` | Chart 69.8.2. Prometheus keeps 15 days or 4GB on a 5Gi volume. The names are pinned so that the dashboard can find the Prometheus Service. |
| Longhorn | `geass-longhorn` | `longhorn-system` | Chart 1.12.1. Geass turns off the storage class of the chart and the Longhorn UI. |
| Logging | `geass-victoria-logs` and `geass-fluent-bit` | `geass-logging` | The store is limited to 4GiB on a 6Gi volume, and to your retention. The shippers skip system namespaces. |

In addition to the charts, the cluster controller keeps these objects in place:

- **Storage classes**, after Longhorn is ready:
  - `geass-replicated`: 1, 2, or 3 copies, by the number of schedulable nodes.
  - `geass-single`: one copy, kept on the node that the pod runs on, for databases that replicate themselves.
  - `geass-replicated-<region>` for each region, restricted to the nodes of that region.
  - `geass-tagged-<hash>` for each set of node tags that a project requires, or that the cluster object storage server runs on. Each class is restricted to the nodes that have those tags.

  As nodes join, Geass raises existing volumes to the replica count. It never lowers them.
- **Longhorn node tags.** Each Longhorn node gets `geass-region-<region>` and `geass-tag-<tag>` tags that mirror its Kubernetes node. With these tags, a storage class can name a region or tags. After any node is a compliance node, every other node also gets `geass-general`, and the global and regional classes name it. Thus, the volume copies of other projects stay off compliance nodes.
- **The ClusterIssuer** `letsencrypt-prod`, after cert-manager is ready and `spec.addons.certManager.acmeEmail` is set. It uses these settings:
  - ACME against the Let's Encrypt production server.
  - HTTP-01 challenges through the `traefik` ingress class.
  - The account key in the Secret `letsencrypt-prod-account-key` in the `cert-manager` namespace.
  - The labels `geass.dev/managed-by=geass` and `geass.dev/cluster=<cluster>`.

  The operator watches the issuer and puts it back when someone deletes or edits it. The operator never touches an issuer without the managed-by label. If you later empty `acmeEmail`, the issuer that Geass made stays in place, so that existing certificates can renew.
- **The dashboard at the public IP address**, while `spec.dashboard.publicIP` is `true`, cert-manager is on, and no dashboard domain is verified. The operator keeps these objects:
  - The ClusterIssuer `geass-letsencrypt-ip`: ACME with the Let's Encrypt `shortlived` profile, no email, and HTTP-01 through the `traefik` ingress class.
  - The Certificate `geass-dashboard-public-ip` in `geass-system`, with `ipAddresses` set to the address, and the Secret `geass-dashboard-public-ip-tls`. The certificate is valid for about 6 days, and cert-manager renews it.
  - The Traefik `TLSStore` `default` in `geass-system`. Its default certificate is that Secret, because browsers send no server name for an IP address.
  - The Traefik `IngressRoute` `geass-dashboard-public-ip`, on the `websecure` entry point. It matches ``Host(`<ip>`)`` and sends traffic to the dashboard Service on port 8085.
  - The Traefik `Middleware` and `IngressRoute` `geass-dashboard-public-ip-http`. They redirect plain HTTP for the address to
    HTTPS. Requests under `/.well-known/acme-challenge/` are not redirected, so that the certificate renews.

  The `letsencrypt-prod` issuer for hostnames does not change. It still needs `acmeEmail`.
- **Network policies** `geass-restrict-metrics-store` in `monitoring` and `geass-restrict-log-store` in `geass-logging`. With these policies, only the platform can read metrics and logs.
- **A PodMonitor** `geass-ingress` in `monitoring`. With it, Prometheus scrapes Traefik, and the request charts get data.
- **A ServiceMonitor and a PrometheusRule** named `geass-operator` in `monitoring`, while monitoring is on. With them, Prometheus scrapes the metrics of the operator and holds seven alerts on those metrics. See [Logs, metrics and terminal](/docs/guides/observability/#metrics-and-alerts-for-the-operator).
- **A PrometheusRule** `geass-alerts` in `monitoring`, while monitoring is on. It holds the alerts that [Alerts](/docs/guides/alerts/) sends: builds, deploys, services, backups, nodes, crashing pods, and filling storage. The operator also makes the `AlertmanagerConfig` `geass-alerts` in `geass-system` from the channels in the platform configuration, when there are channels.
- **The serverless add-on**, while `spec.addons.serverless.enabled` is true. It has these parts:
  - The HelmCharts `geass-keda` and `geass-keda-http`: KEDA 2.21.0 and the HTTP add-on 0.16.0, installed into `keda`.
  - When none exists, a `HelmChartConfig` `traefik` in `kube-system` that sets `providers.kubernetesIngress.allowExternalNameServices: true`.

  The parts install in that order, and the operator looks again every 20 seconds until both charts are up. If a `traefik` configuration already exists, the operator does not change it, and it logs what to add to it.
- **The K3s upgrade of the workers**, while `spec.k3s.version` is set. A separate controller does the upgrade, because the upgrade of a node takes minutes of SSH, and the cluster controller has one worker. This controller does these things:
  - It cordons and uncordons nodes with the annotation `geass.dev/k3s-upgrade-cordon`.
  - It writes `status.k3s`.
  - It does nothing to a worker that has no `GeassNode`, because that worker has no SSH key.

  See [Upgrade Geass](/docs/operations/upgrading/#workers).
- **The network policy check**, a background task of the operator. After the cluster is ready, the check does these things:
  - It runs a short check in the namespace `geass-netcheck` that the network plugin of the cluster acts on policies.
  - It records the result as the condition `NetworkPolicyEnforced`.
  - It records when it ran in the annotation `geass.dev/network-policy-checked-at`.

  The check runs again after a day, or after half an hour when it could not tell. It also runs when the annotation `geass.dev/recheck-network-policy` is set on the cluster. The check then removes that annotation.
- **The state of the add-ons.** After the other background work, the operator works out how each add-on of `spec.addons` is doing from its HelmChart, its install Job, and its pods. It writes the result to `status.addons`. It looks again every 30 seconds while an add-on that is on is not ready, and every minute while one has failed. For `longhorn`, the state is `Installing` until the CSI driver is registered and every node has a ready manager.
- **A retry of an add-on.** When the annotation `geass.dev/retry-addon` on the cluster names an add-on that is on, the operator deletes the install Jobs of that add-on. K3s then makes new Jobs. The operator removes the annotation. The **Retry install** button and `geass addons retry` set it.
- **The release and the schema level.** The operator writes them to `status.release` and `status.schemaVersion` at the start of every reconcile. Before any controller starts, the operator also keeps the ConfigMap `geass-schema` in `geass-system`.
- **The admission policies.** Geass creates each of these policies the first time that it is needed, and never removes it:
  - `geass-required-node-tags`, a ValidatingAdmissionPolicy and its binding, and `geass-required-node-tags-defaults`, a MutatingAdmissionPolicy and its binding (Kubernetes 1.36 or later). Geass creates them the first time that a project requires node tags. See [Compliance node tags](/docs/guides/compliance-tags/).
  - `geass-pod-bandwidth`, a MutatingAdmissionPolicy and its binding (Kubernetes 1.36 or later). Geass creates it the first time that the limits of an organization name a bandwidth. See [Noisy-neighbour limits](/docs/guides/tenant-limits/#bandwidth).

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

An add-on is still installing. List the HelmCharts and their install Jobs with the commands above. The usual cause is a Job that continues to fail, or a pod in `ImagePullBackOff`. Geass only logs `Waiting for cert-manager add-on`, `Waiting for monitoring add-on`, or `Waiting for Longhorn add-on`. The condition does not name the add-on.

### Longhorn never becomes ready

Longhorn needs the iSCSI initiator on every node. The installer and the worker join step install it. But the worker step is optional, so a worker where the step failed has no Longhorn manager. To find a node without a ready `longhorn-manager`, run `kubectl -n longhorn-system get pods -o wide`. Also run `kubectl get csidriver driver.longhorn.io`. Until every schedulable node has a ready manager, Geass does not create the storage classes, and databases report `Storage is starting`.

### The reason is `ProvisionFailed`

Find the add-on that failed: `AddonsReady` still has the last error. Fix the cause. Then change a field in the spec so that `metadata.generation` changes. The controller continues when the generation changes. If you save the same values again, the generation does not change.

### A worker cannot join: `cannot reach the cluster API at <url>`

The rest of the message tells you the cause:

- A refused connection or a timeout means that 6443/tcp is closed between the worker and the control plane.
- A loopback address means that `spec.serverURL` is `127.0.0.1`, and no control plane node has an address to use instead.

See [Add servers](/docs/getting-started/add-servers/#set-the-address-workers-join).

### Projects say `project "<project>" cluster "<cluster>" is not ready`

The `GeassCluster` is not `Ready`. See the cases above.

### HTTPS certificates are not issued

Read the `ClusterIssuerReady` condition: `kubectl -n geass-system get geasscluster default -o jsonpath='{.status.conditions[?(@.type=="ClusterIssuerReady")]}'`.

- `ACMEEmailMissing` means that no contact address is set. Patch `spec.addons.certManager.acmeEmail` with a real address.
- `IssuerNotReady` gives the reason from cert-manager. Most often, Let's Encrypt refused the address. Let's Encrypt rejects placeholder domains such as `example.com`.

Then run `kubectl get clusterissuer letsencrypt-prod`. See [Domains and TLS](/docs/guides/domains-and-tls/).

### The dashboard does not answer at the public IP address

Read the [`DashboardPublicIP`](#dashboardpublicip) condition and `status.dashboardPublicURL`. Usually, port 80 is closed in the firewall of the provider, and the reason is `IssuingCertificate`. See [The dashboard does not answer at the public IP address](/docs/operations/troubleshooting/#the-dashboard-does-not-answer-at-the-public-ip-address).

### A worker's K3s upgrade is `Failed` or `Waiting`

`status.k3s.upgrade.message` tells you which node and why.

- `Waiting` means that the upgrade stops on purpose in one of these cases: a node is not ready, a replicated volume is degraded, or a worker has not reported its K3s version yet. The upgrade continues automatically when the problem is fixed.
- `Failed` stops at the node that did not come back ready on the new version. That node stays cordoned. Examine the node (`kubectl describe node <name>`, and `journalctl -u k3s-agent` on the machine). Then annotate the cluster to try again. See [Upgrade Geass](/docs/operations/upgrading/#workers).

### A service cannot sleep and says `AddonNotInstalled`

The serverless add-on is off or not finished. Run `kubectl -n kube-system get helmcharts geass-keda geass-keda-http` and `kubectl -n keda get pods`. See [Sleep when idle](/docs/guides/scale-to-zero/#troubleshooting).

### Metrics or logs screens are empty

Make sure that `monitoring` or `logging` is enabled, and that the pods in `monitoring` or `geass-logging` run. After you turn off logging, the screens that read history have nothing to show.

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
