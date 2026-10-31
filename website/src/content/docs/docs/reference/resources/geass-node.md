---
title: "GeassNode"
description: "Reference for GeassNode: a worker joined over SSH or a machine that Geass creates at a cloud, with the region, zone, and tags of a node."
sidebar:
  order: 8
---

A `GeassNode` describes one server in the cluster. For a worker, the `GeassNode` is how you ask Geass to join a server.
Geass connects over SSH, installs the K3s agent, and waits for the node to become `Ready`. The server is one that
already exists. Or, when `spec.cloud` is set, the server is a machine that Geass first creates at a cloud.

For any node, the `GeassNode` can also hold the placement of the node: its region, zone, and tags. Geass writes the
placement onto the Kubernetes node as labels and taints. You can create a `GeassNode` with `kubectl`, from
**Add server** on the **Usage** page, or with `geass servers add`. A pool creates its nodes itself. See
[GeassNodePool](/docs/reference/resources/geass-node-pool/).

A `GeassNode` without `spec.cloud` never creates a server. It only joins a server that already exists and that Geass
can reach over SSH. With `spec.cloud` (experimental), Geass creates the machine at a cloud through a
[connection for machines](/docs/guides/connectors/clouds/). Geass deletes the machine when the node is removed.

For the step-by-step procedure, see [Add servers](/docs/getting-started/add-servers/). To learn how Geass uses
placement, see [Scaling and placement](/docs/guides/scaling-and-placement/).

## Summary

| Property | Value |
| --- | --- |
| API version | `geass.geass.dev/v1alpha1` |
| Kind | `GeassNode` |
| Plural | `geassnodes` |
| Scope | Namespaced. Create it in `geass-system`. |
| Subresources | `status` |
| Printer columns | None. `kubectl get` shows only the name and age. Read `status.phase` and `status.message` with `-o jsonpath`. |
| Finalizer | `geass.geass.dev/node`, added for workers and for control plane entries that are not the default |

The name of the `GeassNode` is the name of the Kubernetes node. Geass passes the name to K3s as the node name, and
waits for a node of that name to become `Ready`. Use a lowercase DNS name that no other node uses.

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

The key must have no passphrase. Geass reads the first of these keys that exists in the Secret, in this order:
`ssh-privatekey`, `id_rsa`, `private-key`, `key`. An optional `known_hosts` key in the same Secret pins the SSH host
key of the machine. See [SSH and host keys](#ssh-and-host-keys).

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

A compliance tag is always reserved. Projects that require the tag run only on nodes that have it, and nothing else
runs on those nodes. See [Compliance node tags](/docs/guides/compliance-tags/).

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

Leave out `eip`, `sshUser`, and `sshKeySecretRef`. Geass logs in with a key of its own, and the operator refuses a node
that sets them (`a node whose machine Geass creates takes no eip or sshKeySecretRef`). The machine costs money at the
cloud until the node is removed. The name of the node is at most 55 characters. A node that belongs to a pool gets all
of these settings from the pool.

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

The name must match the Kubernetes node name of the control plane, which is its hostname. The installer does not
create this object. The control plane shows in the **Nodes** table of the dashboard whether or not this object exists.

## Spec fields

| Field | Type | Required or default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.role` | string | Required | Enum `ControlPlane`, `Worker` | Geass joins a `Worker` over SSH. Geass never provisions a `ControlPlane`. It only waits for a Kubernetes node of that name to become `Ready`. |
| `spec.default` | boolean | `false` | None | With `role: ControlPlane`, marks an existing control plane node that Geass must not provision. The object then has no finalizer and no cluster check. The field has no effect on a worker. |
| `spec.pool` | string | Optional | None | The [GeassNodePool](/docs/reference/resources/geass-node-pool/) that the node belongs to. A node of a pool has a label that names the pool, and the pool adds and removes the node. |
| `spec.storage` | string | Optional. `Allow` when left out | Enum `None`, `Allow` | Whether the node can hold data. `None` labels the node `geass.dev/storage=none` and taints it `geass.dev/ephemeral=true:NoSchedule`, so that databases, buckets, and volumes stay off it. For a node of a pool, the value comes from the pool. |
| `spec.cloud` | object | Optional | None | Makes Geass create the machine of the node at a cloud. Then leave out `eip`, `sshUser`, and `sshKeySecretRef`. |
| `spec.cloud.connectionRef` | string | Required with `cloud` | Not empty | The [GeassCloudConnection](/docs/reference/resources/geass-cloud-connection/) to create the machine with. It must have the purpose `Compute` and no `spec.project`. |
| `spec.cloud.region` | string | Optional | None | The place of the cloud itself: a location, zone, or region. After the node exists, you cannot change the connection or the region, because Geass deletes the machine through the connection that made it. Make a new node instead. You cannot change `spec.pool` either. |
| `spec.cloud.size` | string | Required with `cloud` | Not empty | The machine size, as the cloud names it. |
| `spec.cloud.image` | string | The cloud's Ubuntu 24.04 | None | The operating system image. |
| `spec.cloud.spot` | boolean | `false` | None | Ask for a cheaper machine that the cloud might take back. |
| `spec.cloud.addressType` | string | `Public` | Enum `Public`, `Private` | Which address of the machine Geass uses to reach it. |
| `spec.cloud.settings` | map of strings | Optional | None | Values specific to the cloud. |
| `spec.eip` | string | Optional. Required in practice for a worker. | None | The address that Geass connects to over SSH: any address or host name that the control plane can reach. In a multi-site cluster, it is also the address that the node advertises. In a [WireGuard mesh](/docs/operations/mesh-networking/), it must be an IP address that other machines can use. A Tailscale node advertises its tailnet address instead. |
| `spec.sshPort` | integer | CRD default `22` | None | The SSH port that Geass uses to provision the node and to remove it. |
| `spec.sshUser` | string | Optional. Required in practice for a worker. | None | The SSH user: `root`, or a user with passwordless `sudo`. An empty user cannot sign in. |
| `spec.sshKeySecretRef` | SecretReference | Required for a worker. Optional for a control plane. | None | The Secret that holds the private key, and optionally the `known_hosts` that pin the host key of the machine. A standard Kubernetes `name` and `namespace` pair. The namespace defaults to the namespace of the node. |
| `spec.sshKeySecretRef.name` | string | Optional | None | The name of the Secret. |
| `spec.sshKeySecretRef.namespace` | string | Optional | None | The namespace of the Secret. |
| `spec.clusterRef` | ObjectReference | Required | None | The [GeassCluster](/docs/reference/resources/geass-cluster/) to join. A standard Kubernetes object reference. Geass reads only `name` and `namespace`. The default `namespace` is the namespace of the node. Geass ignores the other fields. |
| `spec.placement` | object | Optional | None | The region, zone, and tags of the node. To manage the labels of the node by hand, leave it out. |
| `spec.placement.region` | string | Optional | Pattern `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, max length 40 | Groups nodes that are close together. A project pinned to a region runs only on nodes in that region. Geass writes it as the label `topology.kubernetes.io/region`. |
| `spec.placement.zone` | string | Optional | Pattern `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, max length 40 | The provider or site inside the region, such as `aws-eu-north-1`. Geass writes it as the label `topology.kubernetes.io/zone`. |
| `spec.placement.tags` | list | Optional | Max 16 items. List type map, keyed by `name`. | Tags that describe what the node is for. |
| `spec.placement.tags[].name` | string | Required | Pattern `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, max length 40 | The tag. Geass writes it as the label `tag.geass.dev/<name>=true`. |
| `spec.placement.tags[].reserved` | boolean | `false` | None | Keeps off the node every workload that does not ask for the tag. Geass writes it as the taint `tag.geass.dev/<name>=reserved:NoSchedule`. |
| `spec.placement.tags[].compliance` | boolean | `false` | None | Marks the tag as a compliance boundary, such as `hipaa`. A compliance tag is always reserved, so you do not need `reserved: true` with it. Geass writes it as the taint `tag.geass.dev/<name>=compliance:NoSchedule`. Projects that require the tag run only on nodes that have it. |

:::note
The Go types and the generated CRD agree for every field on this page.
:::

:::caution
Use regions and tags for placement. The Go comment on `zone` says that replicas of a highly available database are
spread across zones. But no controller reads the zone to place anything. Geass only writes the label.
:::

### How placement is applied

When `spec.placement` is set, the operator writes the labels and taints on the Kubernetes node. It does this on every
reconcile of the object. The object is reconciled in these cases:

- When the object changes.
- When its Kubernetes node goes `Ready` or `NotReady`, or the region, site, or tag labels or taints of that node change.
- Every 10 minutes for a `Ready` node.

The operator removes `tag.geass.dev/` labels and taints that are not declared. It does not change any other label or
taint. When `spec.placement` is left out, Geass does not change the labels of the node. A worker also gets the labels
and taints in its K3s configuration at join time. So the worker has them from the first moment that it can be
scheduled.

Geass does not move running workloads. A workload that restarts returns only to a node that matches.

## Status

| Field | Type | Meaning |
| --- | --- | --- |
| `status.phase` | string | The stage of the node. See below. |
| `status.message` | string | A human-readable message. Empty when the node is `Ready`. On a failure, it holds the step, the error, and the output of the command. |
| `status.nodeName` | string | The name of the Kubernetes node, set when the node is `Ready`. |
| `status.lastHeartbeatTime` | date-time | No longer updated. A node created now has no value, and an older node keeps its last value. The kubelet keeps a heartbeat on the Kubernetes node, in its `Ready` condition and its lease. To see when the node last reported, read the conditions of the node itself. |
| `status.conditions` | list | Never written. It stays empty. |
| `status.cloud` | object | The machine that Geass created for the node, when `spec.cloud` is set. See below. |

Geass sets these phases:

| Phase | Meaning |
| --- | --- |
| *(empty)* or `Pending` | Not started. The controller checks every 15 seconds, and waits until the `status.phase` of the cluster is `Ready`. Then it provisions the node. |
| `Joining` | For a worker, the message is `installing k3s agent` while the SSH steps run. Then Geass waits for a Kubernetes node of the same name to report `Ready`. For a control plane entry, the message is `waiting for control plane node registration`. |
| `Ready` | The Kubernetes node is `Ready`. Geass checks it again when the health or placement of the Kubernetes node changes, and every 10 minutes. |
| `Failed` | A step failed. `status.message` says which step. To try again, add the `geass.dev/retry` annotation (see [Editing by hand](#editing-by-hand)). Or delete the object and create it again. |
| `Creating` | Only for a node whose machine Geass creates. The machine is being made. Or it is running and waits for SSH, or for the firewall of the control plane to let it in. After `Creating` come `Joining` (the message is `waiting for the node to register`) and `Ready`. |

The type also defines the phases `Deleting`, `Deleted`, `Error`, and `Unknown`. No code sets them. A node that is being
removed keeps its phase and has a deletion time.

For a cloud node, Geass stops waiting in these cases:

- `Creating`: after 10 minutes without a running machine that has an address, or after 18 minutes without SSH.
- `Joining`: after 15 minutes without a ready node.

Each failure deletes the machine immediately, and the node stays `Failed` with its reason. A node of a pool is cleared
away 30 minutes after it was made, and the pool makes a new one.

### The machine

`status.cloud` holds what Geass knows about the machine of a node.

| Field | Meaning |
| --- | --- |
| `status.cloud.instanceName` | The name that the machine is created with. Geass writes it before it calls the cloud. So if Geass made a machine immediately before a crash, Geass finds the machine again by its name, and does not make it twice. |
| `status.cloud.instanceID` | The ID of the machine at the cloud. Geass deletes the machine by this ID and no other. |
| `status.cloud.attempt` | The number of machines made for the node before this one. After the first machine, the number is part of the machine name. So a machine that is still being removed never shares a name with its replacement. |
| `status.cloud.size`, `status.cloud.region` | What the machine was created with. These values differ from the spec when a pool used a fallback. |
| `status.cloud.problem` | `Capacity` (the cloud has no room) or `Quota` (the account is at a limit), while the machine cannot be made. The pool of the node reads it to try another size or place. |
| `status.cloud.state` | The state of the machine, as the cloud last reported it. |
| `status.cloud.publicIP`, `status.cloud.privateIP` | The addresses of the machine. |
| `status.cloud.createdAt` | When the cloud accepted the machine. |
| `status.cloud.controlPlaneAccess` | The address opened for the machine in the firewall of the control plane (`spec.network.controlPlaneFirewall` of the cluster). Geass closes it again when the machine is removed. |
| `status.cloud.controlPlaneAccessFirewall` | The firewall that the address was opened on, kept with the address. Geass closes the rule on this firewall, even if the `controlPlaneFirewall` of the cluster changed or was removed in the meantime. |
| `status.cloud.joinAttempts`, `status.cloud.joinStartedAt` | How many times Geass started to install K3s, and when it began. If a node is not ready 15 minutes after the start, Geass gives up on it. |

The node does these things with a machine that is lost:

- If the cloud no longer has the machine, the node removes the Kubernetes node and makes a new machine for the same
  node. For example, the cloud took back a spot machine, or someone deleted the machine.
- A node with `storage: None` is also replaced when its machine is stopped. It is also replaced when the node has been
  `NotReady` for 30 minutes while the machine runs. The node is not replaced if most of its pool is `NotReady` too,
  because then the cause is the network or the control plane. The node is also not replaced while K3s is being
  upgraded on it.

Geass writes these messages when it sets `Failed`:

| Message | Cause |
| --- | --- |
| `cluster not found: <error>` | `spec.clusterRef` names a cluster that does not exist. |
| `ssh key secret ref is required for worker nodes` | `spec.sshKeySecretRef` is empty on a worker. |
| `ssh key: <error>` | The Secret is missing, or it has none of the four key names. |
| `join token: <error>` | The Secret that the `spec.tokenSecretRef` of the cluster names is missing, or it has none of `token`, `value`, and `k3s-token`. |
| `could not determine the cluster's k3s version: <error>` | Geass could not list the control plane nodes. |
| `ssh dial <host>: <error>` | The SSH connection failed. The cause is a timeout, a refused connection, a key that the server does not accept, or a key with a passphrase (`parse key: ...`). |
| `ssh dial <host>: ... the SSH host key of <host> is not the key recorded in known_hosts`, or `... is not listed in known_hosts` | The Secret has a `known_hosts` entry, and the machine does not match it. See [SSH and host keys](#ssh-and-host-keys). |
| `ssh dial <host>: known_hosts ...` | Geass cannot use the `known_hosts` entry in the Secret. The entry has hashed host names, an `@` marker, or no host keys, or it is not valid. |
| `step check-cluster-reachable failed: ... cannot reach the cluster API at <url> from this node: <why>` | The worker cannot reach the control plane. `<why>` names the cause: a refused connection, a timeout, a host name that does not resolve, an address that is not a K3s API server, or a loopback `spec.serverURL`. See [Troubleshooting](#failed-at-check-cluster-reachable). |
| `step <name> failed: <error>` followed by `stdout:` and `stderr:` | An SSH step failed. |
| `step <name> cancelled: <error>` | The operator stopped while the step ran. |
| `the node's name is longer than 55 characters, which a machine name cannot be` | A node with `spec.cloud` has a name that is too long. |
| `a node whose machine Geass creates takes no eip or sshKeySecretRef: Geass logs in with a key of its own` | `spec.cloud` is set together with `eip` or `sshKeySecretRef`. |
| `this operator has no cloud provider configured` | The operator was built without the cloud drivers. |
| `the provider refused the credentials: ... (the connection's key, or a policy of the account or its organization, may not allow this)` | The cloud refused the request. The text after the colon is the reason from the cloud: a wrong key, a policy of the key that does not allow the call, or a policy of the organization of the account. At Amazon, that policy is a service control policy, which the message names. |
| `the provider no longer has the machine it accepted: ...`, `the machine was not running with an address 10m0s after it was requested (state ...)`, `the provider is removing the machine (state ...)` | The cloud refused the machine after it accepted the request, or never started it, or is deleting it. |
| `K3s was installed but the node was not ready 15m0s later: see journalctl -u k3s-agent on the machine` | The node never registered. |
| `opening the control plane's firewall for the machine: ...` | The `spec.network.controlPlaneFirewall` of the cluster names a connection that cannot change that firewall. Only AWS and Hetzner can. |
| `unsupported node role "<role>"` | `spec.role` is not one Geass knows. |

While a cloud node waits, its phase stays the same, and `status.message` says what it waits for. It can wait for the
cloud, for capacity, for SSH, or for a fix to its connection. See [Node pools](/docs/guides/node-pools/#pool-states).

## What the dashboard writes

The dashboard creates a `GeassNode` from **Add server**, and deletes a `GeassNode` that it created with **Remove**. The
dashboard also edits the placement of a node, retries a server that failed, and cordons a node.

| Where | What it does |
| --- | --- |
| **Settings** > **Usage** > **Nodes** | Lists every Kubernetes node: **Node**, **Status**, **Role**, **Region**, **Tags**, **CPU**, **Memory**, and **Pressure**. **Status** is `Ready`, `Not ready`, or `Cordoned`. It shows nodes whether or not a `GeassNode` exists. A `GeassNode` that has no node yet, or that failed, has a row too. Its **Status** is `Waiting`, `Creating machine`, `Joining`, or `Failed`, from `status.phase`, and the error is `status.message`. |
| **Retry** on a server that failed | Adds the annotation `geass.dev/retry` to the `GeassNode`. The operator starts over from the first step and removes the annotation. Only for a `GeassNode` whose `status.phase` is `Failed`. |
| **Update host keys** on a server that you have | Replaces the lines for the address of the server in the key `known_hosts` of the Secret in `spec.sshKeySecretRef`. Lines for other addresses stay. Not for a machine that Geass creates. |
| **Cordon**, **Uncordon** in the menu of a node | Sets `spec.unschedulable` on the Kubernetes node. Geass refuses to cordon the last node that can start new work, and to uncordon a node that the operator cordoned for an upgrade or for a pool. |
| **Add server** | Opens a form that creates a `GeassNode`. For **A server I have**, the node gets `eip`, `sshUser`, the port, and a Secret `<name>-ssh` that the dashboard makes from the key that you paste. For **A cloud machine**, the node gets `spec.cloud`. Names are lowercase letters, numbers, and hyphens, at most 55 characters. |
| **Remove** on a server | Deletes the `GeassNode` after you enter its name. Only for a worker that is not part of a pool and is not the control plane. |
| **Edit** on a node | Opens a dialog with **Region**, **Site**, and **Tags**. The dialog has one **Reserve this node for `<tag>`** switch for each tag. Under **Compliance tags**, it has one `<tag> is a compliance tag` switch for each tag. **Save** writes the labels and taints directly onto the Kubernetes node. If a `GeassNode` has the same name, or its `status.nodeName` matches, the dialog also writes `spec.placement` on it, so that the operator does not undo the edit. **Site** is `spec.placement.zone`. |

The dialog refuses these inputs:

- A name that is not valid, for example
  `Region may only contain lowercase letters, numbers and hyphens, and must start and end with a letter or number`.
- More than 16 tags (`A node can have at most 16 tags`).
- A reservation for a tag that the node does not have
  (`<tag> cannot be reserved because the node does not have that tag`).
- A compliance tag that the node does not have
  (`<tag> cannot be a compliance tag because the node does not have that tag`).

A compliance tag is also saved as reserved. A tag that a project requires is saved as a compliance tag, whatever the
dialog sends. If the node changed while you edited it, the dialog says
`The node changed while you were editing it. Reload and try again`. After a save, the dialog warns about these
problems:

- Projects whose region no longer has a node.
- Projects that lose the last node that has a tag that they require.
- Workloads that were already running on a node that became a compliance node.

## Editing by hand

To add a worker, the supported way is to create a `GeassNode` with `kubectl`.

- You cannot rename a node by changing `metadata.name`. Delete the object and create a new one.
- If the object is already `Ready`, a change to `spec.eip`, `spec.sshUser`, the Secret, or the cluster does nothing
  until the node is provisioned again.
- To retry a `Failed` node, fix the cause, and then add the `geass.dev/retry` annotation:

  ```bash
  kubectl -n geass-system annotate geassnode worker-1 geass.dev/retry=true
  ```

  The controller sets `status.phase` back to `Pending` through the status subresource, clears `status.message`, and
  removes the annotation. Then it provisions the node again from the first step. Any value works. On a node that is
  not `Failed`, there is nothing to retry. So the controller removes the annotation and does nothing else. It does not
  retry a later failure unless you annotate again. You can also delete the object and create it again from the same
  manifest. That method also clears the status completely.
- Geass provisions a `Ready` worker again from scratch when its Kubernetes node disappears, or when the node has been
  `NotReady` for more than 5 minutes. The message then becomes `node NotReady, re-provisioning`. For this, the SSH key
  and the access must still work. A node with `spec.cloud` is handled differently:
  - If its Kubernetes node disappears while its machine is there, K3s is installed on the machine again.
  - A node whose machine is gone at the cloud gets a new machine.
  - A `storage: None` node also gets a new machine when its machine is stopped, or when the node stays `NotReady` for
    30 minutes while its machine runs.
- Geass joins one worker at a time, because the SSH steps run inside the reconcile.

### SSH and host keys

Authentication is by private key only. The join token is part of the install command. So while the install runs, the
token is visible in the process list on the new server.

By default, Geass does not verify the SSH host key of the server. Geass accepts any key that the server presents. So
Geass connects to whatever answers at `spec.eip`, and runs commands there as root. To pin the machine, add its host key
to the Secret that `spec.sshKeySecretRef` names. Use the key `known_hosts`, and the plain format that `ssh-keyscan`
prints:

```bash
ssh-keyscan -t ed25519 203.0.113.20 > known_hosts
ssh-keygen -lf known_hosts        # compare the fingerprint with the machine's own
kubectl -n geass-system create secret generic worker-ssh-key \
  --from-file=ssh-privatekey=./geass-worker --from-file=known_hosts=./known_hosts
```

When the Secret has `known_hosts`, every connection to the node checks the host key against it. That includes
provisioning and removal. Geass refuses a machine that presents another key before it sends any command or the join
token. As in OpenSSH, the port is part of the address. For a node whose `spec.sshPort` is not 22, use
`ssh-keyscan -p <port>`.

One Secret can hold the keys of several machines, one line for each. Geass refuses hashed host names
(`ssh-keyscan -H`), `@cert-authority` lines, and `@revoked` lines with an error. It does not skip them.

A machine that Geass creates needs none of this setup. Geass makes the SSH key and a host key for each machine itself:
the Secrets `geass-nodepool-ssh` and `<node>-hostkey` in `geass-system`. Geass puts the public key and the host key in
the cloud-init of the machine. Before Geass sends anything, it pins the address of the machine to that host key. See
[Security](/docs/operations/security/#machines-geass-creates).

Geass does not record a key the first time that it connects and compare the key afterward (trust on first use). A
rebuilt machine has a new host key. So a recorded key would make Geass fail to provision the machine again. Also, the
first connection, which carries the token, would still be unchecked. After a rebuild, update `known_hosts` in the
Secret. Geass reads the Secret each time that it provisions or removes the node.

### Removing a node

Delete the `GeassNode`. For a worker, Geass then does these steps:

1. Connects over SSH on `spec.sshPort`, and runs `/usr/local/bin/k3s-agent-uninstall.sh`. If the Secret has
   `known_hosts`, Geass checks the host key against it. If this step fails, Geass logs the failure and continues the
   removal.
2. Deletes the Kubernetes node, so that the node does not stay listed as `NotReady`.
3. Releases the object.

:::caution
Move data off the node before you remove it. Removal of a node without `spec.cloud` does not cordon or drain the node.
When Geass uninstalls K3s, the pods of the node stop.
:::

Geass removes a node with `spec.cloud` differently, because it deletes the machine. Geass does these steps:

1. Cordons the node and evicts its pods for up to five minutes. Geass uses the eviction API, so that it honors
   disruption budgets.
2. Deletes the Kubernetes node.
3. Deletes the machine by its ID. If the ID was never written down, Geass finds the machine by its name and labels.
4. Waits until the cloud no longer lists the machine as alive.
5. Takes the address of the machine out of the firewall of the control plane.
6. Releases the object.

If the connection is gone, the node waits and says so. To release the node without deleting the machine, annotate it
`geass.dev/abandon-instance=true`. Then delete the machine at the cloud yourself.

A `GeassNode` with `default: true` has no finalizer. If you delete it, Geass removes only the object. A `ControlPlane`
entry that is not the default also removes only the object. If that entry names an SSH key, Geass also runs the
uninstall script, which does not exist on a server.

## What happens under the hood

For a worker, the operator waits for the cluster. Then it runs these steps over SSH, in order. Each step runs through
`sudo -n sh -c`, unless the user is `root`. The connection times out after 30 seconds.

| Step | What it does |
| --- | --- |
| `check-curl` | Uses `curl`, or installs it with `apt-get`. |
| `install-iscsi` | Installs and enables the iSCSI initiator that Longhorn needs. Optional: a failure does not stop the join. |
| `install-tailscale`, `join-tailnet` | Only for a [Tailscale mesh](/docs/operations/mesh-networking/), before the check below. The first step installs Tailscale with its own script when Tailscale is missing. The second step writes `/etc/rancher/k3s/vpn-auth` (mode 600) and brings the node onto the tailnet with the auth key. The second step fails when the node gets no tailnet address. |
| `check-cluster-reachable` | Runs `curl -sk --max-time 15 <serverURL>/ping`, which must answer `pong`. `<serverURL>` is the `spec.serverURL` of the cluster. When that value is a loopback address, `<serverURL>` is the address that the control plane node registered under. In a multi-site cluster, that address is its external address. On failure, the message is `cannot reach the cluster API at <serverURL> from this node:` followed by what curl found: `the connection was refused`, `the connection timed out`, `the host name does not resolve`, `something answered, but it is not a K3s API server`, or, for a loopback address, that `spec.serverURL` is a loopback address. |
| `check-wireguard`, `open-overlay-port` | Only for multi-site clusters. The first step needs a kernel that can create a WireGuard interface. The second step opens 51820/udp and 10250/tcp in `ufw` or `firewalld`, if one is active. Optional. |
| `write-k3s-config` | Overwrites `/etc/rancher/k3s/config.yaml` with the node labels and taints from `spec.placement`, kubelet log rotation settings, and, for multi-site, `node-external-ip`. For a Tailscale mesh, the file has `vpn-auth-file`, and `node-external-ip` is the tailnet address of the node. |
| `install-k3s` | Runs the K3s installer in `agent` mode with the `serverURL` of the cluster, the join token, and the node name. |
| `enable-k3s` | `systemctl enable k3s-agent && systemctl start k3s-agent`. |

The K3s version is the oldest K3s version among the control plane nodes, because an agent must not be newer than its
server. If no control plane node reports a version, Geass uses the `spec.version` of the cluster when it looks like a
K3s release. Otherwise, the K3s installer picks its current stable release.

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

The control plane cannot reach `spec.eip` on `spec.sshPort`. Check the provider firewall, the host firewall, and the
address.

### `Failed` with `ssh: unable to authenticate` or `parse key: ...passphrase protected`

The user or the key is wrong, the public key is not in `authorized_keys` for that user, or the key has a passphrase.
Make a key without a passphrase.

### `Failed` at `check-cluster-reachable`

The message after `from this node:` says why:

- A refused connection or a timeout means that 6443/tcp is closed between the worker and the control plane. The block
  is in the host firewall or in the security group of the provider.
- A host name that does not resolve, or an answer that is not a K3s API server, means that `spec.serverURL` of the
  [GeassCluster](/docs/reference/resources/geass-cluster/) is wrong.
- A loopback address means that no control plane node has an address that Geass could use instead. Set
  `spec.serverURL` to the private or public address of the control plane.

See [Add servers](/docs/getting-started/add-servers/#set-the-address-workers-join).

### `Failed` with `the SSH host key ... is not the key recorded in known_hosts`

The Secret has a `known_hosts` entry, and the machine at `spec.eip` does not match it. Another machine answers at that
address, the machine was rebuilt, or the entry is for a different address or port. Check the fingerprint on the
machine itself. Then update `known_hosts` in the Secret, and add the retry annotation.

### `Failed` at `install-k3s` with a `sudo` error

The user needs passwordless `sudo`. Or use `root`.

### The phase stays `Joining`

Provisioning finished, but no Kubernetes node of that name reports `Ready`. Geass sets no timeout. On the server, read
`sudo journalctl -u k3s-agent`. These are common causes:

- 8472/udp is closed between nodes, or 51820/udp in a multi-site cluster.
- A node with that name already exists.
- The agent cannot reach 6443/tcp.

### The cluster shows not ready after a node joined

With Longhorn on, the cluster waits for a ready `longhorn-manager` on every schedulable node. See
[GeassCluster](/docs/reference/resources/geass-cluster/).

### The control plane has no tags

Use **Edit** on the control plane under **Settings** > **Usage** > **Nodes**. That works without a `GeassNode`. Create
a `GeassNode` with `default: true` only if you want to keep the placement in a resource.

## Related

- [Add servers](/docs/getting-started/add-servers/)
- [GeassNodePool](/docs/reference/resources/geass-node-pool/)
- [Cloud connections for machines](/docs/guides/connectors/clouds/)
- [Scaling and placement](/docs/guides/scaling-and-placement/)
- [Multi-provider clusters](/docs/operations/multi-provider/)
- [High availability](/docs/operations/high-availability/)
- [GeassCluster](/docs/reference/resources/geass-cluster/)
- [GeassHAReadiness](/docs/reference/resources/geass-ha-readiness/)
