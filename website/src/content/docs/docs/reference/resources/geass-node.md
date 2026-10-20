---
title: "GeassNode"
description: "Reference for GeassNode: a worker joined over SSH or a machine Geass creates at a cloud, plus a node's region, zone and tags."
sidebar:
  order: 8
---

A `GeassNode` describes one server in the cluster. For a worker, it is how you ask Geass to join a server: Geass connects over SSH, installs the K3s agent and waits for the node to become `Ready`. The server is one that already exists, or, when `spec.cloud` is set, a machine that Geass creates first at a cloud. For any node it can also carry the node's placement: its region, zone and tags, which Geass writes onto the Kubernetes node as labels and taints. You can create a `GeassNode` with `kubectl`, or from **Add server** on the **Usage** page, or with `geass servers add`. A pool creates its nodes itself: see [GeassNodePool](/docs/reference/resources/geass-node-pool/).

A `GeassNode` without `spec.cloud` never creates a server. It only joins one that already exists and that Geass can reach over SSH. With `spec.cloud` (experimental) Geass creates the machine at a cloud through a [connection for machines](/docs/guides/connectors/clouds/), and deletes it when the node is removed.

For the step-by-step procedure, see [Add servers](/docs/getting-started/add-servers/). For how placement is used, see [Scaling and placement](/docs/guides/scaling-and-placement/).

## Summary

| Property | Value |
| --- | --- |
| API version | `geass.geass.dev/v1alpha1` |
| Kind | `GeassNode` |
| Plural | `geassnodes` |
| Scope | Namespaced. Create it in `geass-system`. |
| Subresources | `status` |
| Printer columns | None. `kubectl get` shows only the name and age. Read `status.phase` and `status.message` with `-o jsonpath`. |
| Finalizer | `geass.geass.dev/node`, added for workers and for non-default control plane entries |

The name of the `GeassNode` is the name of the Kubernetes node. Geass passes it to K3s as the node name and looks for a node of that name to become `Ready`. Use a lowercase DNS name that no other node uses.

## Examples

### A worker

```yaml title="worker-1.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassNode
metadata:
  name: worker-1
  namespace: geass-system
spec:
  role: Worker
  eip: 203.0.113.20
  sshUser: ubuntu
  sshKeySecretRef:
    name: worker-ssh-key
  clusterRef:
    name: default
    namespace: geass-system
```

```yaml title="worker-ssh-key.yaml"
apiVersion: v1
kind: Secret
metadata:
  name: worker-ssh-key
  namespace: geass-system
stringData:
  ssh-privatekey: |
    -----BEGIN OPENSSH PRIVATE KEY-----
    replace-with-your-private-key
    -----END OPENSSH PRIVATE KEY-----
```

The key must have no passphrase. Geass reads the first of these keys that exists in the Secret: `ssh-privatekey`, `id_rsa`, `private-key`, `key`. An optional `known_hosts` key in the same Secret pins the machine's SSH host key: see [SSH and host keys](#ssh-and-host-keys).

### A worker with a region, a site and a reserved tag

```yaml title="storage-worker.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassNode
metadata:
  name: storage-1
  namespace: geass-system
spec:
  role: Worker
  eip: 203.0.113.21
  sshPort: 22
  sshUser: ubuntu
  sshKeySecretRef:
    name: worker-ssh-key
  clusterRef:
    name: default
    namespace: geass-system
  placement:
    region: eu
    zone: aws-eu-north-1
    tags:
      - name: storage
        reserved: true
```

### A node for a compliance tag

```yaml title="hipaa-worker.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassNode
metadata:
  name: hipaa-1
  namespace: geass-system
spec:
  role: Worker
  eip: 203.0.113.22
  sshUser: ubuntu
  sshKeySecretRef:
    name: worker-ssh-key
  clusterRef:
    name: default
    namespace: geass-system
  placement:
    region: eu
    tags:
      - name: hipaa
        compliance: true
```

A compliance tag is always reserved. Projects that require the tag run only on nodes that carry it, and nothing else runs on those nodes. See [Compliance node tags](/docs/guides/compliance-tags/).

### A machine Geass creates

```yaml title="cloud-server.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassNode
metadata:
  name: edge-1
  namespace: geass-system
spec:
  role: Worker
  clusterRef:
    name: default
    namespace: geass-system
  cloud:
    connectionRef: hetzner
    region: fsn1
    size: cx23
  storage: Allow
```

Leave out `eip`, `sshUser` and `sshKeySecretRef`: Geass logs in with a key of its own, and the operator refuses a node that sets them (`a node whose machine Geass creates takes no eip or sshKeySecretRef`). The machine costs money at the cloud until the node is removed. The node's name is at most 55 characters. A node that belongs to a pool gets all of this from the pool.

### The control plane, so you can set its placement

```yaml title="control-plane.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassNode
metadata:
  name: control-plane-1
  namespace: geass-system
spec:
  role: ControlPlane
  default: true
  clusterRef:
    name: default
    namespace: geass-system
  placement:
    region: eu
```

The name must match the control plane's Kubernetes node name (its hostname). The installer does not create this object. The control plane shows in the dashboard's **Nodes** table whether or not it exists.

## Spec fields

| Field | Type | Required or default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.role` | string | Required | Enum `ControlPlane`, `Worker` | `Worker` is joined over SSH. `ControlPlane` is never provisioned: Geass only waits for a Kubernetes node of that name to become `Ready`. |
| `spec.default` | boolean | `false` | None | With `role: ControlPlane`, marks an existing control plane node that Geass must not provision. The object then has no finalizer and no cluster check. It has no effect on a worker. |
| `spec.pool` | string | Optional | None | The [GeassNodePool](/docs/reference/resources/geass-node-pool/) the node belongs to. A node of a pool is labelled with it, and the pool is what adds and removes it. |
| `spec.storage` | string | Optional. `Allow` when left out | Enum `None`, `Allow` | Whether the node may hold data. `None` labels the node `geass.dev/storage=none` and taints it `geass.dev/ephemeral=true:NoSchedule`, so that databases, buckets and volumes stay off it. For a node of a pool it comes from the pool. |
| `spec.cloud` | object | Optional | None | Makes Geass create the node's machine at a cloud. Then leave out `eip`, `sshUser` and `sshKeySecretRef`. |
| `spec.cloud.connectionRef` | string | Required with `cloud` | Not empty | The [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/) to create the machine with. It must have the purpose `Compute` and no `spec.project`. |
| `spec.cloud.region` | string | Optional | None | The cloud's own place: a location, zone or region. The connection and the region cannot be changed once the node exists, because the machine is deleted through the connection that made it; make a new node instead. `spec.pool` cannot be changed either. |
| `spec.cloud.size` | string | Required with `cloud` | Not empty | The cloud's name for a machine size. |
| `spec.cloud.image` | string | The cloud's Ubuntu 24.04 | None | The operating system image. |
| `spec.cloud.spot` | boolean | `false` | None | Ask for a cheaper machine that the cloud may take back. |
| `spec.cloud.addressType` | string | `Public` | Enum `Public`, `Private` | Which address of the machine Geass reaches it on. |
| `spec.cloud.settings` | map of strings | Optional | None | Values specific to the cloud. |
| `spec.eip` | string | Optional. Required in practice for a worker. | None | The address Geass connects to over SSH: any address or host name the control plane can reach. In a multi-site cluster it is also the address the node advertises. |
| `spec.sshPort` | integer | CRD default `22` | None | The SSH port used to provision the node and to remove it. |
| `spec.sshUser` | string | Optional. Required in practice for a worker. | None | The SSH user: `root`, or a user with passwordless `sudo`. An empty user cannot sign in. |
| `spec.sshKeySecretRef` | SecretReference | Required for a worker. Optional for a control plane. | None | The Secret that holds the private key, and optionally the `known_hosts` that pin the machine's host key. A standard Kubernetes `name` and `namespace` pair. The namespace defaults to the node's. |
| `spec.sshKeySecretRef.name` | string | Optional | None | Name of the Secret. |
| `spec.sshKeySecretRef.namespace` | string | Optional | None | Namespace of the Secret. |
| `spec.clusterRef` | ObjectReference | Required | None | The [GeassCluster](/docs/reference/resources/geass-cluster/) to join. A standard Kubernetes object reference. Geass reads only `name` and `namespace` (default: the node's namespace). The other fields are ignored. |
| `spec.placement` | object | Optional | None | The node's region, zone and tags. Leave it out to manage the node's labels by hand. |
| `spec.placement.region` | string | Optional | Pattern `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, max length 40 | Groups nodes that are close together. A project pinned to a region runs only on nodes in it. Written as the label `topology.kubernetes.io/region`. |
| `spec.placement.zone` | string | Optional | Pattern `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, max length 40 | The provider or site inside the region, such as `aws-eu-north-1`. Written as the label `topology.kubernetes.io/zone`. |
| `spec.placement.tags` | list | Optional | Max 16 items. List type map, keyed by `name`. | Tags that describe what the node is for. |
| `spec.placement.tags[].name` | string | Required | Pattern `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, max length 40 | The tag. Written as the label `tag.geass.dev/<name>=true`. |
| `spec.placement.tags[].reserved` | boolean | `false` | None | Keeps every workload that does not ask for the tag off the node. Written as the taint `tag.geass.dev/<name>=reserved:NoSchedule`. |
| `spec.placement.tags[].compliance` | boolean | `false` | None | Marks the tag as a compliance boundary, such as `hipaa`. A compliance tag is always reserved, so you do not need `reserved: true` with it. Written as the taint `tag.geass.dev/<name>=compliance:NoSchedule`. Projects that require the tag run only on nodes that carry it. |

:::note
The Go types and the generated CRD agree for every field on this page.
:::

:::caution
The Go comment on `zone` says replicas of a highly available database are spread across zones. No controller reads the zone to place anything. Geass only writes the label. Use regions and tags for placement.
:::

### How placement is applied

When `spec.placement` is set, the operator writes the labels and taints on the Kubernetes node. It does this on every reconcile of the object. The object is reconciled when it changes, when its Kubernetes node goes `Ready` or `NotReady` or has its region, site, tag labels or taints changed, and every 10 minutes for a `Ready` node. It removes `tag.geass.dev/` labels and taints that are not declared, and leaves every other label and taint alone. When `spec.placement` is left out, Geass leaves the node's labels alone. A worker also gets the labels and taints in its K3s configuration at join time, so it carries them from the first moment it can be scheduled.

Running workloads are not moved. A workload that restarts only returns to a node that matches.

## Status

| Field | Type | Meaning |
| --- | --- | --- |
| `status.phase` | string | The stage of the node. See below. |
| `status.message` | string | A human-readable message. Empty when the node is `Ready`. On a failure it holds the step, the error and the command's output. |
| `status.nodeName` | string | The Kubernetes node's name, set when the node is `Ready`. |
| `status.lastHeartbeatTime` | date-time | No longer updated. A node created now has none, and an older node keeps the last value it had. Kubelet keeps a heartbeat on the Kubernetes node, in its `Ready` condition and its lease. Read the node's own conditions to see when it last reported. |
| `status.conditions` | list | Never written. It stays empty. |
| `status.cloud` | object | The machine Geass created for the node, when `spec.cloud` is set. See below. |

The phases Geass sets:

| Phase | Meaning |
| --- | --- |
| *(empty)* or `Pending` | Not started. The controller waits, checking every 15 seconds, until the cluster's `status.phase` is `Ready`, then provisions. |
| `Joining` | For a worker, the message is `installing k3s agent` while the SSH steps run, then Geass waits for a Kubernetes node of the same name to report `Ready`. For a control plane entry, the message is `waiting for control plane node registration`. |
| `Ready` | The Kubernetes node is `Ready`. Geass looks at it again whenever the Kubernetes node's health or placement changes, and every 10 minutes. |
| `Failed` | A step failed. `status.message` says which. Add the `geass.dev/retry` annotation to try again (see [Editing by hand](#editing-by-hand)), or delete the object and create it again. |
| `Creating` | Only for a node whose machine Geass creates. The machine is being made, or is running and waiting for SSH or for the control plane's firewall to let it in. It is followed by `Joining` (the message is `waiting for the node to register`) and `Ready`. |

The type also defines the phases `Deleting`, `Deleted`, `Error` and `Unknown`. No code sets them. A node that is being removed keeps its phase and has a deletion time.

For a cloud node, `Creating` gives up after 10 minutes without a running machine that has an address, and after 18 minutes without SSH, and `Joining` after 15 minutes without a ready node. Each failure deletes the machine at once, and the node stays `Failed` with its reason. A node of a pool is cleared away 30 minutes after it was made, and the pool makes a new one.

### The machine

`status.cloud` holds what Geass knows about a node's machine.

| Field | Meaning |
| --- | --- |
| `status.cloud.instanceName` | The name the machine is created with, written before the cloud is called, so that a machine made just before a crash is found again by its name and not made twice. |
| `status.cloud.instanceID` | The cloud's ID for the machine. The machine is deleted by this ID and no other. |
| `status.cloud.attempt` | The number of machines made for the node before this one. It is part of the machine's name after the first, so that a machine still being removed never shares a name with its replacement. |
| `status.cloud.size`, `status.cloud.region` | What the machine was created with, which differs from the spec when a pool used a fallback. |
| `status.cloud.problem` | `Capacity` (the cloud has no room) or `Quota` (the account is at a limit) while the machine cannot be made. The node's pool reads it to try another size or place. |
| `status.cloud.state` | The machine's state as the cloud last reported it. |
| `status.cloud.publicIP`, `status.cloud.privateIP` | The machine's addresses. |
| `status.cloud.createdAt` | When the cloud accepted the machine. |
| `status.cloud.controlPlaneAccess` | The address opened for the machine in the control plane's firewall (`spec.network.controlPlaneFirewall` of the cluster), closed again when the machine is removed. |
| `status.cloud.controlPlaneAccessFirewall` | The firewall that address was opened on, kept with it. The rule is closed on this firewall, even if the cluster's `controlPlaneFirewall` was changed or removed meanwhile. |
| `status.cloud.joinAttempts`, `status.cloud.joinStartedAt` | How many times Geass set about installing K3s, and when it began. A node that is not ready 15 minutes after it began is given up on. |

What the node does with a machine that is lost: if the cloud no longer has it (a spot machine taken back, or one someone deleted), the node removes the Kubernetes node and makes a new machine for the same node. A node with `storage: None` is also replaced when its machine is stopped, or when the node has been `NotReady` for 30 minutes while the machine runs, unless most of its pool is `NotReady` too (then the cause is the network or the control plane), or K3s is being upgraded on it.

The messages Geass writes when it sets `Failed`:

| Message | Cause |
| --- | --- |
| `cluster not found: <error>` | `spec.clusterRef` names a cluster that does not exist. |
| `ssh key secret ref is required for worker nodes` | `spec.sshKeySecretRef` is empty on a worker. |
| `ssh key: <error>` | The Secret is missing, or has none of the four key names. |
| `join token: <error>` | The Secret named by the cluster's `spec.tokenSecretRef` is missing, or has none of `token`, `value`, `k3s-token`. |
| `could not determine the cluster's k3s version: <error>` | Geass could not list the control plane nodes. |
| `ssh dial <host>: <error>` | The SSH connection failed: a timeout, a refused connection, a key the server does not accept, or a key with a passphrase (`parse key: ...`). |
| `ssh dial <host>: ... the SSH host key of <host> is not the key recorded in known_hosts`, or `... is not listed in known_hosts` | The Secret has a `known_hosts` entry and the machine does not match it. See [SSH and host keys](#ssh-and-host-keys). |
| `ssh dial <host>: known_hosts ...` | The `known_hosts` entry in the Secret cannot be used: it has hashed host names, an `@` marker, no host keys, or is not valid. |
| `step check-cluster-reachable failed: ... cannot reach the cluster API at <url> from this node: <why>` | The worker cannot reach the control plane. `<why>` names the cause: a refused connection, a timeout, a host name that does not resolve, an address that is not a K3s API server, or a loopback `spec.serverURL`. See [Troubleshooting](#failed-at-check-cluster-reachable). |
| `step <name> failed: <error>` followed by `stdout:` and `stderr:` | An SSH step failed. |
| `step <name> cancelled: <error>` | The operator stopped while the step ran. |
| `the node's name is longer than 55 characters, which a machine name cannot be` | A node with `spec.cloud` has too long a name. |
| `a node whose machine Geass creates takes no eip or sshKeySecretRef: Geass logs in with a key of its own` | `spec.cloud` is set together with `eip` or `sshKeySecretRef`. |
| `this operator has no cloud provider configured` | The operator was built without the cloud drivers. |
| `the provider refused the credentials: ... (the connection's key, or a policy of the account or its organization, may not allow this)` | The cloud refused the request. The text after the colon is the cloud's own reason: a wrong key, a policy of the key that does not allow the call, or a policy of the account's organization (at Amazon, a service control policy, which the message names). |
| `the provider no longer has the machine it accepted: ...`, `the machine was not running with an address 10m0s after it was requested (state ...)`, `the provider is removing the machine (state ...)` | The cloud refused the machine after it accepted the request, or never started it, or is deleting it. |
| `K3s was installed but the node was not ready 15m0s later: see journalctl -u k3s-agent on the machine` | The node never registered. |
| `opening the control plane's firewall for the machine: ...` | `spec.network.controlPlaneFirewall` of the cluster names a connection that cannot change that firewall. Only AWS and Hetzner can. |

While a cloud node waits (for the cloud, for capacity, for SSH, for its connection to be fixed), its phase stays and `status.message` says what it waits for. See [Node pools](/docs/guides/node-pools/#pool-states).
| `unsupported node role "<role>"` | `spec.role` is not one Geass knows. |

## What the dashboard writes

The dashboard creates a `GeassNode` from **Add server**, and deletes one it created with **Remove**. It edits a node's placement.

| Where | What it does |
| --- | --- |
| **Settings** > **Usage** > **Nodes** | Lists every Kubernetes node: **Node**, **Role**, **Region**, **Tags**, **CPU**, **Memory**, **Pressure** and **Status** (`Ready`, `Not ready` or `Unschedulable`). It shows nodes whether or not a `GeassNode` exists. |
| **Add server** | Opens a form that creates a `GeassNode`: for **A server I have** with `eip`, `sshUser`, the port and a Secret `<name>-ssh` it makes from the key you paste, for **A cloud machine** with `spec.cloud`. Names are lowercase letters, numbers and hyphens, at most 55 characters. |
| **Remove** on a server | Deletes the `GeassNode`, after you type its name. Only for a worker that is not part of a pool and not the control plane. |
| **Edit** on a node | Opens a dialog with **Region**, **Site**, **Tags**, one **Reserve this node for `<tag>`** switch per tag and, under **Compliance tags**, one `<tag> is a compliance tag` switch per tag. **Save** writes the labels and taints straight onto the Kubernetes node. If a `GeassNode` has the same name, or its `status.nodeName` matches, the dialog also writes `spec.placement` on it, so the operator does not undo the edit. **Site** is `spec.placement.zone`. |

The dialog refuses an invalid name (for example `Region may only contain lowercase letters, numbers and hyphens, and must start and end with a letter or number`), more than 16 tags (`A node can have at most 16 tags`), a reservation for a tag the node does not have (`<tag> cannot be reserved because the node does not have that tag`), and a compliance tag the node does not have (`<tag> cannot be a compliance tag because the node does not have that tag`). A compliance tag is saved as reserved too. A tag that a project requires is saved as a compliance tag whatever the dialog sends. If the node changed while you were editing, it says `The node changed while you were editing it. Reload and try again`. After a save it warns about projects whose region no longer has a node, projects that lose the last node carrying a tag they require, and workloads that were already running on a node that has become a compliance node.

## Editing by hand

Creating a `GeassNode` with `kubectl` is the supported way to add a worker.

- Do not rename a node by changing `metadata.name`: you cannot. Delete the object and create a new one.
- Changing `spec.eip`, `spec.sshUser`, the Secret or the cluster on an object that is already `Ready` does nothing until the node is provisioned again.
- To retry a `Failed` node, fix the cause and add the `geass.dev/retry` annotation:

  ```bash
  kubectl -n geass-system annotate geassnode worker-1 geass.dev/retry=true
  ```

  The controller sets `status.phase` back to `Pending` through the status subresource, clears `status.message`, and removes the annotation, then provisions the node again from the first step. Any value works. On a node that is not `Failed` there is nothing to retry, so the annotation is removed without any action, and a later failure is not retried unless you annotate again. Deleting the object and creating it again from the same manifest also works, and clears the status completely.
- A `Ready` worker is provisioned again from scratch when its Kubernetes node disappears, or when the node has been `NotReady` for more than 5 minutes (the message becomes `node NotReady, re-provisioning`). That needs the SSH key and the access to still work. A node with `spec.cloud` is handled differently: if its Kubernetes node disappears while its machine is there, K3s is installed on the machine again. A node whose machine is gone at the cloud gets a new machine, and so does a `storage: None` node whose machine is stopped, or that stays `NotReady` for 30 minutes while its machine runs.
- Geass joins one worker at a time, because the SSH steps run inside the reconcile.

### SSH and host keys

Authentication is by private key only. The join token is part of the install command, so it is visible in the process list on the new server while the install runs.

By default Geass does not verify the server's SSH host key. It accepts whatever key the server presents, so it connects to whatever answers at `spec.eip`, and runs commands there as root. To pin the machine, add its host key to the Secret that `spec.sshKeySecretRef` names, under the key `known_hosts`, in the plain format `ssh-keyscan` prints:

```bash
ssh-keyscan -t ed25519 203.0.113.20 > known_hosts
ssh-keygen -lf known_hosts        # compare the fingerprint with the machine's own
kubectl -n geass-system create secret generic worker-ssh-key \
  --from-file=ssh-privatekey=./geass-worker --from-file=known_hosts=./known_hosts
```

When the Secret has `known_hosts`, every connection to the node (provisioning and removal) checks the key against it, and a machine that presents another key is refused before any command or the join token is sent. The port is part of the address, as in OpenSSH: use `ssh-keyscan -p <port>` for a node whose `spec.sshPort` is not 22. One Secret can hold the keys of several machines, one line each. Hashed host names (`ssh-keyscan -H`), `@cert-authority` and `@revoked` lines are refused with an error, not skipped.

A machine that Geass creates needs none of this: Geass makes the SSH key and a host key for each machine itself (the Secrets `geass-nodepool-ssh` and `<node>-hostkey` in `geass-system`), puts the public key and the host key in the machine's cloud-init, and pins the machine's address to that host key before it sends anything. See [Security](/docs/operations/security/#machines-geass-creates).

Geass does not record a key the first time it connects and compare it afterwards (trust on first use). A rebuilt machine has a new host key, so a recorded key would make Geass fail to re-provision it, and the first connection, which carries the token, would still be unchecked. After a rebuild, update `known_hosts` in the Secret. The Secret is read each time the node is provisioned or removed.

### Removing a node

Delete the `GeassNode`. For a worker Geass then:

1. Connects over SSH, on `spec.sshPort`, and runs `/usr/local/bin/k3s-agent-uninstall.sh`. It checks the host key against `known_hosts` when the Secret has one. A failure is logged and does not stop the removal.
2. Deletes the Kubernetes node, so it does not stay listed as `NotReady`.
3. Releases the object.

:::caution
Removal of a node without `spec.cloud` does not cordon or drain the node. Uninstalling K3s stops its pods. Move data off the node before you remove it.
:::

A node with `spec.cloud` is removed differently, because its machine is deleted. Geass cordons the node and evicts its pods, through the eviction API so that disruption budgets are honoured, for up to five minutes. It then deletes the Kubernetes node, deletes the machine by its ID (or, if the ID was never written down, finds the machine by its name and labels), waits until the cloud no longer lists the machine as alive, takes the machine's address out of the control plane's firewall, and releases the object. If the connection is gone, the node waits and says so: annotate it `geass.dev/abandon-instance=true` to release it without deleting the machine, and delete the machine at the cloud yourself.

A `GeassNode` with `default: true` has no finalizer: deleting it removes only the object. A non-default `ControlPlane` entry also removes only the object (and, if it names an SSH key, runs the uninstall script, which does not exist on a server).

## What happens under the hood

For a worker, the operator waits for the cluster, then runs these steps over SSH in order. Each runs through `sudo -n sh -c` unless the user is `root`. The connection times out after 30 seconds.

| Step | What it does |
| --- | --- |
| `check-curl` | Uses `curl`, or installs it with `apt-get`. |
| `install-iscsi` | Installs and enables the iSCSI initiator Longhorn needs. Optional: a failure does not stop the join. |
| `check-cluster-reachable` | Runs `curl -sk --max-time 15 <serverURL>/ping`, which must answer `pong`. `<serverURL>` is the cluster's `spec.serverURL`, or, when that is a loopback address, the address the control plane node registered under (its external address in a multi-site cluster). On failure the message is `cannot reach the cluster API at <serverURL> from this node:` followed by what curl found: `the connection was refused`, `the connection timed out`, `the host name does not resolve`, `something answered, but it is not a K3s API server`, or, for a loopback address, that `spec.serverURL` is a loopback address. |
| `check-wireguard`, `open-overlay-port` | Multi-site clusters only. The first needs a kernel that can create a WireGuard interface. The second opens 51820/udp and 10250/tcp in `ufw` or `firewalld`, if one is active. Optional. |
| `write-k3s-config` | Overwrites `/etc/rancher/k3s/config.yaml` with the node labels and taints from `spec.placement`, kubelet log rotation settings and, for multi-site, `node-external-ip`. |
| `install-k3s` | Runs the K3s installer in `agent` mode with the cluster's `serverURL`, the join token and the node name. |
| `enable-k3s` | `systemctl enable k3s-agent && systemctl start k3s-agent`. |

The K3s version is the oldest K3s version among the control plane nodes, because an agent must not be newer than its server. If none reports one, Geass uses the cluster's `spec.version` when it looks like a K3s release. Otherwise the K3s installer picks its current stable release.

```bash title="Inspect a node"
kubectl -n geass-system get geassnodes
kubectl -n geass-system get geassnode worker-1 \
  -o jsonpath='{.status.phase}{"  "}{.status.message}{"\n"}'

# The Kubernetes node and its placement
kubectl get node worker-1 --show-labels
kubectl get node worker-1 -o jsonpath='{.spec.taints}'
kubectl get nodes -L topology.kubernetes.io/region,topology.kubernetes.io/zone

# On the new server
sudo systemctl status k3s-agent
sudo journalctl -u k3s-agent
```

## Troubleshooting

### `Failed` with `ssh dial ...: i/o timeout` or `connection refused`

The control plane cannot reach `spec.eip` on `spec.sshPort`. Check the provider firewall, the host firewall and the address.

### `Failed` with `ssh: unable to authenticate` or `parse key: ...passphrase protected`

The user or the key is wrong, or the public key is not in `authorized_keys` for that user, or the key has a passphrase. Make a key without one.

### `Failed` at `check-cluster-reachable`

The message after `from this node:` says why. A refused connection or a timeout means 6443/tcp is closed between the worker and the control plane, in the host firewall or the provider's security group. A host name that does not resolve, or an answer that is not a K3s API server, means `spec.serverURL` of the [GeassCluster](/docs/reference/resources/geass-cluster/) is wrong. A loopback address means no control plane node has an address Geass could use instead: set `spec.serverURL` to the control plane's private or public address. See [Add servers](/docs/getting-started/add-servers/#set-the-address-workers-join).

### `Failed` with `the SSH host key ... is not the key recorded in known_hosts`

The Secret has a `known_hosts` entry and the machine at `spec.eip` does not match it: another machine answers at that address, the machine was rebuilt, or the entry is for a different address or port. Check the fingerprint on the machine itself, then update `known_hosts` in the Secret and add the retry annotation.

### `Failed` at `install-k3s` with a `sudo` error

The user needs passwordless `sudo`, or use `root`.

### The phase stays `Joining`

Provisioning finished but no Kubernetes node of that name reports `Ready`. Geass sets no timeout. On the server, read `sudo journalctl -u k3s-agent`. Common causes: 8472/udp is closed between nodes (51820/udp in a multi-site cluster), a node with that name already exists, or the agent cannot reach 6443/tcp.

### The cluster shows not ready after a node joined

With Longhorn on, the cluster waits for a ready `longhorn-manager` on every schedulable node. See [GeassCluster](/docs/reference/resources/geass-cluster/).

### The control plane has no tags

Use **Edit** on it under **Settings** > **Usage** > **Nodes**. That works without a `GeassNode`. Create one with `default: true` only if you want the placement kept in a resource.

## Related

- [Add servers](/docs/getting-started/add-servers/)
- [GeassNodePool](/docs/reference/resources/geass-node-pool/)
- [Cloud connections for machines](/docs/guides/connectors/clouds/)
- [Scaling and placement](/docs/guides/scaling-and-placement/)
- [Multi-provider clusters](/docs/operations/multi-provider/)
- [High availability](/docs/operations/high-availability/)
- [GeassCluster](/docs/reference/resources/geass-cluster/)
- [GeassHAReadiness](/docs/reference/resources/geass-ha-readiness/)
