---
title: "Add servers"
description: "Join more servers over SSH, tag them with a region, site and tags, watch them come up and remove them. Covers credentials, node states and failures."
---

A new install is a single server that runs everything. You add capacity by joining more servers as workers. Geass connects to each new server over SSH, installs the K3s agent on it and waits for it to appear in the cluster. Neither the dashboard nor the `geass` command line can join a server today: you describe the worker in a `GeassNode` object with `kubectl` on the control plane, then watch it in the dashboard under **Usage** in the left rail.

## Before you start

- The cluster is `Ready`: `sudo k3s kubectl -n geass-system get geasscluster`.
- A new Linux server that meets the [requirements](/docs/getting-started/requirements/), reachable by SSH from the control plane.
- A user on it that is `root`, or can run `sudo` without a password. Every provisioning step runs through `sudo -n`.
- The network paths in the next table, opened in the provider's firewall and in any host firewall on the new server. Geass opens none of them on a worker in a default (single-network) cluster.

| From | To | Port | Why |
| --- | --- | --- | --- |
| Control plane | New server | 22/tcp (or the `sshPort` you choose) | SSH provisioning and removal |
| New server | Control plane | 6443/tcp | The K3s agent joins the Kubernetes API. Geass checks this before it installs |
| Every node | Every other node | 8472/udp | VXLAN overlay network |
| Control plane and Prometheus | New server | 10250/tcp | kubelet, used for container metrics |
| New server | Internet | 443/tcp | Downloading K3s from `get.k3s.io` and container images |

For nodes at different providers, see [Multi-provider clusters](/docs/operations/multi-provider/): the cluster must be installed in multi-site mode, and the ports differ (51820/udp instead of 8472/udp).

## Set the address workers join

A worker joins the address stored in `spec.serverURL` of the cluster object. The installer writes `https://127.0.0.1:6443` there for a default install, which is the control plane's own loopback address and cannot work from another machine. Nothing changes it for you. Before you add the first worker, set it to the control plane's address on the network the workers share with it:

```bash
sudo k3s kubectl -n geass-system patch geasscluster default --type merge \
  -p '{"spec":{"serverURL":"https://10.0.0.5:6443"}}'
```

Use the control plane's private address when the servers share a private network, or its public address when they do not. The address is also checked from the new server (`curl .../ping`), so 6443/tcp must be open to it. A cluster installed with `GEASS_MULTI_SITE=1` already has the public address here.

:::caution
If you skip this, the worker fails at the step `check-cluster-reachable` with the message `cannot reach the cluster API at https://127.0.0.1:6443 from this node: open TCP 6443 to it`. The message points at the firewall, but the cause is the stored address.
:::

## Prepare the new server

1. Create a key pair used only for this. The key must have no passphrase:

   ```bash
   ssh-keygen -t ed25519 -N "" -C geass-worker -f ./geass-worker
   ```

2. Add `geass-worker.pub` to the `authorized_keys` of the SSH user on the new server.
3. Copy the private key to the control plane and save it as a Secret in `geass-system`. Geass reads the first of these keys that exists: `ssh-privatekey`, `id_rsa`, `private-key`, `key`.

   ```bash
   sudo k3s kubectl -n geass-system create secret generic worker-ssh-key \
     --from-file=ssh-privatekey=./geass-worker
   ```

4. Delete the local copy of the private key when it is no longer needed.

## Join a worker

Save this as `worker-1.yaml`, then apply it:

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

```bash
sudo k3s kubectl apply -f worker-1.yaml
```

The name `worker-1` becomes the Kubernetes node name, so make it a lowercase DNS label that no other node uses. `eip` is the address Geass connects to over SSH: any address or hostname the control plane can reach. `sshUser` is required in practice, because an empty user cannot sign in.

## Check progress

```bash
sudo k3s kubectl -n geass-system get geassnode worker-1 \
  -o jsonpath='{.status.phase}{"  "}{.status.message}{"\n"}'
sudo k3s kubectl get nodes
```

`get geassnode` lists only names and ages, so read `status.phase` and `status.message` as above. In the dashboard, the node appears in the **Nodes** table on the **Usage** page once Kubernetes registers it, and the left rail's footer shows one bar per node (`2 NODES`, then `all healthy` or the node with the worst problem). The same table is reachable as **Settings → Usage**. `geass nodes list` prints the nodes with their status, region, site, tags, CPU and memory, for a token that may read them.

### States

| Phase | Meaning |
| --- | --- |
| *(empty)* or `Pending` | Not started. The controller waits, checking every 15 seconds, until the cluster is `Ready`, then provisions |
| `Joining` | Geass is running the provisioning steps over SSH (the message is `installing k3s agent`), then waiting, checking every 15 to 20 seconds, for a Kubernetes node with the same name to report `Ready` |
| `Ready` | The node is `Ready`. Geass looks at it again every 10 minutes, and at once when the Kubernetes node goes `NotReady` or comes back, or when its region, site, tag labels or taints change. It no longer records `lastHeartbeatTime`: the kubelet keeps its own heartbeat on the Kubernetes node (`sudo k3s kubectl get node worker-1`, and the node lease) |
| `Failed` | A step failed. `status.message` has the step, the error, and the command's standard output and error |

Geass sets no timeout on `Joining`. If provisioning finished but the node never becomes `Ready`, the object stays there until you fix the cause. A `Ready` worker is provisioned again from scratch when its Kubernetes node disappears, or when the node stays `NotReady` for more than five minutes (the message becomes `node NotReady, re-provisioning`). Geass asks to be woken when the five minutes are up, so it does not wait for the next 10-minute look. That needs the SSH key and access to still work.

To retry after `Failed`, fix the cause, then delete the object and create it again from the same manifest:

```bash
sudo k3s kubectl -n geass-system delete geassnode worker-1
sudo k3s kubectl apply -f worker-1.yaml
```

:::caution
The `geass.dev/retry` annotation looks like a retry switch, but it does not work. The operator removes the annotation and tries to set the phase back to `Pending`, but the phase lives in the object's status, and that update is ignored, so the node stays `Failed`. Delete and recreate it instead.
:::

When the cluster changes size, Longhorn needs a ready manager on every node before it reports ready, so the cluster can briefly report not ready while a new node starts. Avoid deploying in that window.

## Tag a node with a region, site and tags

Placement groups nodes so workloads can be limited to part of the cluster. A node has an optional **Region**, an optional **Site** (the provider or location inside the region, such as `aws-eu-north-1`) and up to 16 **Tags** such as `storage` or `gpu`. A tag can be reserved, so only workloads that ask for that tag run on the node. See [Scaling and placement](/docs/guides/scaling-and-placement/) for how projects and services use them.

In the dashboard, open **Usage** in the left rail, find the node in **Nodes**, choose **Edit** and fill in:

| Field | Meaning |
| --- | --- |
| **Region** | Projects pinned to a region only run on nodes in it. Empty means no region |
| **Site** | The provider or site inside the region |
| **Tags** | What the node is for, separated by commas |
| **Reserve this node for `<tag>`** | One switch per tag. A reserved node runs only workloads that ask for that tag |
| **`<tag>` is a compliance tag** | One switch per tag, under **Compliance tags**. A compliance tag is always reserved. Projects that require it run only on nodes that carry it, and nothing else runs on those nodes. See [Compliance node tags](/docs/guides/compliance-tags/) |

Region, site and tags are lowercase letters, numbers and hyphens, at most 40 characters each, and cannot start or end with a hyphen. Running workloads are not moved; one that restarts only returns to a node that matches. The edit also works on the control plane and on nodes that were not joined by Geass. From a terminal, `geass nodes tag worker-1 --region eu --tags storage --reserved storage` does the same and replaces the node's whole placement, so name every tag it should keep.

You can declare the same thing in the `GeassNode` so that it is applied when the node joins, as labels and taints in the agent's K3s configuration:

```yaml
spec:
  placement:
    region: eu
    zone: aws-eu-north-1
    tags:
      - name: storage
        reserved: true
      - name: hipaa
        compliance: true
```

Geass applies the declared placement to the node as labels and keeps it there: `topology.kubernetes.io/region`, `topology.kubernetes.io/zone`, `tag.geass.dev/<tag>=true`, and for reserved tags the taint `tag.geass.dev/<tag>=reserved:NoSchedule` (`=compliance:NoSchedule` for a compliance tag). Without `placement`, Geass leaves a node's labels alone. Editing a node in the dashboard also updates its `GeassNode`, so the operator does not undo the edit.

## Remove a node

Delete the object:

```bash
sudo k3s kubectl -n geass-system delete geassnode worker-1
```

Geass then:

1. Connects over SSH and runs `/usr/local/bin/k3s-agent-uninstall.sh`. It uses port 22 whatever `sshPort` says. A failure is logged and does not stop the removal.
2. Deletes the node from the cluster, so it does not stay listed as `NotReady`.
3. Releases the object.

:::caution
Removal does not cordon or drain the node. Removing K3s stops its pods, and Kubernetes starts replacements elsewhere once the node object is gone. Data does not move with them. A volume whose only copy is on that node's disk, such as a local-path volume, goes when K3s is uninstalled. A replicated Longhorn volume keeps its replicas on other nodes. Move data off a node before you remove it.
:::

If the server was unreachable, the K3s agent keeps running there and can register again. Run `/usr/local/bin/k3s-agent-uninstall.sh` on the machine yourself.

## What happens under the hood

The operator runs these commands on the new server in order, each through `sudo -n sh -c` unless the user is `root`. It opens an SSH connection with a 30-second timeout and authenticates with the key only.

| Step | Command |
| --- | --- |
| `check-curl` | `which curl`, else `apt-get update` and `apt-get install -y curl` |
| `install-iscsi` | Installs and enables the iSCSI initiator. Failure does not stop the join (optional) |
| `check-cluster-reachable` | `curl -sk --max-time 15 <serverURL>/ping`, which must answer `pong` |
| `check-wireguard`, `open-overlay-port` | Multi-site clusters only: tests WireGuard and opens 51820/udp and 10250/tcp in `ufw` or `firewalld` |
| `write-k3s-config` | Overwrites `/etc/rancher/k3s/config.yaml` with the node labels and taints from `placement`, kubelet log rotation (50Mi, 5 files) and, for multi-site, `node-external-ip` |
| `install-k3s` | Runs the K3s installer in `agent` mode with `K3S_URL`, `K3S_TOKEN` and `K3S_NODE_NAME` set |
| `enable-k3s` | `systemctl enable k3s-agent && systemctl start k3s-agent` |

The K3s version is the oldest version among the control plane nodes, because an agent must not be newer than its server. Without a K3s version to match, the K3s installer picks its current stable release.

The credentials involved:

- **The SSH private key** is in the Secret you created, in `geass-system`. The operator reads it each time it provisions or removes the node. It is never sent to the dashboard. Anyone who can read Secrets in `geass-system` can read it, so use a key made for this and limit what it unlocks.
- **The join token** is the Secret `geass-token`. It is part of the command that runs on the new server, so it is visible to local users in the process list while the install runs.
- **Host keys are not verified.** The operator accepts whatever host key the server presents, so it will connect to whatever answers at `eip`. Provision over a network you trust.
- You can remove the public key from the new server after it joins. Then Geass cannot re-provision the node or uninstall the agent when you delete it.

## Options

| `GeassNode` field | Meaning | Default |
| --- | --- | --- |
| `spec.role` | `Worker` or `ControlPlane`. Only `Worker` is provisioned. A `ControlPlane` entry only waits for a node of that name to register and installs nothing | required |
| `spec.eip` | Address Geass connects to over SSH. In a multi-site cluster it is also the address the node advertises | |
| `spec.sshPort` | SSH port for provisioning | `22` |
| `spec.sshUser` | SSH user: `root`, or a user with passwordless `sudo` | |
| `spec.sshKeySecretRef` | Secret (`name`, optional `namespace`, default the object's own) holding the private key. Required for workers | |
| `spec.clusterRef` | The `GeassCluster` to join (`name`, optional `namespace`) | required |
| `spec.placement` | `region`, `zone` and `tags` (each with `name`, `reserved` and `compliance`) | none |
| `spec.default` | Marks a control plane node that already exists and must not be provisioned | `false` |

## Troubleshooting

Read `status.message` first. Provisioning errors have the form `step <name> failed: <error>` followed by the command's standard output and error.

**`ssh dial 203.0.113.20: dial tcp ...: i/o timeout` or `connection refused`.** The control plane cannot reach the server on the SSH port. Check the provider firewall, the host firewall, `eip` and `sshPort`.

**`ssh dial ...: ssh: handshake failed: ssh: unable to authenticate`.** The user or key is wrong, or the public key is not in `authorized_keys` for that user.

**`ssh dial ...: parse key: ssh: this private key is passphrase protected`.** Geass cannot use a key with a passphrase. Create one without.

**`step install-k3s failed ... sudo: a password is required`** (or any step failing under `sudo`). The user needs passwordless `sudo`, or use `root`.

**`step check-cluster-reachable failed ... cannot reach the cluster API`.** Either `serverURL` is still the loopback address (see [Set the address workers join](#set-the-address-workers-join)), or 6443/tcp is closed between the new server and the control plane.

**`step check-curl failed`.** The server has no `curl` and no `apt-get`. Install `curl` yourself.

**`ssh key secret ref is required for worker nodes`, `ssh key: secrets "..." not found`, or `ssh key: no private key found in secret ...`.** Check `spec.sshKeySecretRef` and the key names in the Secret.

**`join token: secrets "geass-token" not found`.** The Secret the installer creates is missing. Recreate it from the K3s token on the control plane: `sudo k3s kubectl -n geass-system create secret generic geass-token --from-file=token=/var/lib/rancher/k3s/server/node-token`.

**`cluster not found: ...`.** `spec.clusterRef` names a cluster that does not exist in that namespace.

**The phase stays `Joining`.** Provisioning finished but the node did not register as `Ready`. On the new server run `sudo systemctl status k3s-agent` and `sudo journalctl -u k3s-agent`. Typical causes: 8472/udp is closed between nodes, 6443/tcp is closed, or a node with that name already exists in the cluster.

**The node is missing from the Nodes table.** The table lists every node Kubernetes knows about. If the node is absent, it has not registered: check `sudo k3s kubectl get nodes` and the phase above.

**The cluster shows not ready after a node joined.** Longhorn is starting on the new node. Check `sudo k3s kubectl -n longhorn-system get pods -o wide`.

## Related

- [Requirements](/docs/getting-started/requirements/)
- [Scaling and placement](/docs/guides/scaling-and-placement/)
- [Multi-provider clusters](/docs/operations/multi-provider/)
- [High availability](/docs/operations/high-availability/)
- [Storage](/docs/guides/storage/)
