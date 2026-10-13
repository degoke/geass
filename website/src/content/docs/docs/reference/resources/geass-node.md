---
title: "GeassNode"
description: "Reference for GeassNode: a server that joins your cluster as a worker over SSH, plus a node's region, zone and tags. Create it with kubectl."
sidebar:
  order: 8
---

A `GeassNode` describes one server in the cluster. For a worker, it is how you ask Geass to join an existing server: Geass connects over SSH, installs the K3s agent and waits for the node to become `Ready`. For any node it can also carry the node's placement: its region, zone and tags, which Geass writes onto the Kubernetes node as labels and taints. You create a `GeassNode` with `kubectl`. The dashboard has no form for adding a server. It shows nodes and edits their placement.

A `GeassNode` never creates a server. It only joins one that already exists and that Geass can reach over SSH. Geass has no provider integration that creates virtual machines.

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

The phases Geass sets:

| Phase | Meaning |
| --- | --- |
| *(empty)* or `Pending` | Not started. The controller waits, checking every 15 seconds, until the cluster's `status.phase` is `Ready`, then provisions. |
| `Joining` | For a worker, the message is `installing k3s agent` while the SSH steps run, then Geass waits for a Kubernetes node of the same name to report `Ready`. For a control plane entry, the message is `waiting for control plane node registration`. |
| `Ready` | The Kubernetes node is `Ready`. Geass looks at it again whenever the Kubernetes node's health or placement changes, and every 10 minutes. |
| `Failed` | A step failed. `status.message` says which. Add the `geass.dev/retry` annotation to try again (see [Editing by hand](#editing-by-hand)), or delete the object and create it again. |

The type also defines the phases `Creating`, `Deleting`, `Deleted`, `Error` and `Unknown`. No code sets them.

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
| `unsupported node role "<role>"` | `spec.role` is not one Geass knows. |

## What the dashboard writes

The dashboard cannot create, delete or provision a `GeassNode`.

| Where | What it does |
| --- | --- |
| **Settings** > **Usage** > **Nodes** | Lists every Kubernetes node: **Node**, **Role**, **Region**, **Tags**, **CPU**, **Memory**, **Pressure** and **Status** (`Ready`, `Not ready` or `Unschedulable`). It shows nodes whether or not a `GeassNode` exists. |
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
- A `Ready` worker is provisioned again from scratch when its Kubernetes node disappears, or when the node has been `NotReady` for more than 5 minutes (the message becomes `node NotReady, re-provisioning`). That needs the SSH key and the access to still work.
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

Geass does not record a key the first time it connects and compare it afterwards (trust on first use). A rebuilt machine has a new host key, so a recorded key would make Geass fail to re-provision it, and the first connection, which carries the token, would still be unchecked. After a rebuild, update `known_hosts` in the Secret. The Secret is read each time the node is provisioned or removed.

### Removing a node

Delete the `GeassNode`. For a worker Geass then:

1. Connects over SSH, on `spec.sshPort`, and runs `/usr/local/bin/k3s-agent-uninstall.sh`. It checks the host key against `known_hosts` when the Secret has one. A failure is logged and does not stop the removal.
2. Deletes the Kubernetes node, so it does not stay listed as `NotReady`.
3. Releases the object.

:::caution
Removal does not cordon or drain the node. Uninstalling K3s stops its pods. Move data off the node before you remove it.
:::

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
- [Scaling and placement](/docs/guides/scaling-and-placement/)
- [Multi-provider clusters](/docs/operations/multi-provider/)
- [High availability](/docs/operations/high-availability/)
- [GeassCluster](/docs/reference/resources/geass-cluster/)
- [GeassHAReadiness](/docs/reference/resources/geass-ha-readiness/)
