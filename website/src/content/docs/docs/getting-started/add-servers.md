---
title: "Add servers"
description: "Add a server that you have over SSH, or have Geass create one at a cloud. Give it a region, a site, and tags, watch it start, and remove it."
---

A new install is a single server that runs everything. To add capacity, you add more servers as workers. To add servers to the control plane itself, so that the cluster survives the loss of one server, see [High availability](/docs/operations/high-availability/#run-three-control-plane-servers). Geass connects to each new server over SSH, installs the K3s agent on it, and waits for it to appear in the cluster. The server can be one that you already have, or a machine that Geass creates for you at a cloud. You can add a server in these ways:

- From the dashboard: **Add server** on the **Usage** page in the left rail.
- From the `geass` command line: `geass servers add`.
- With `kubectl` on the control plane: describe the worker in a `GeassNode` object.

## Ways to add a server

| You want | Do this | Needs |
| --- | --- | --- |
| A server that you already have | **Add server** on the **Usage** page, then **A server I have**. Or `geass servers add NAME --address ... --ssh-user ... --ssh-key-file ...`. Or a `GeassNode` with `kubectl`, as [Join a worker](#join-a-worker) shows | An admin, and SSH to the server |
| A machine at a cloud, made for you | **Add server**, then **A cloud machine**. Or `geass servers add --connection NAME --size SIZE` | An admin, and a [connection for machines](/docs/guides/connectors/clouds/). The machine costs money until you remove the server |
| Machines that come and go with the load | A [pool](/docs/guides/node-pools/) | An admin, and a connection or servers of your own that the pool lists |

### Add a server from the dashboard or the command line

In the dashboard, open **Usage**, find **Nodes**, and click **Add server**. Select **A server I have** or **A cloud machine**. Complete the form, and then click **Add server**. Geass creates nothing until you click **Add server**.

For a server that you have, enter these values:

- **Address**: the address that Geass uses to reach the server over SSH.
- **SSH user**: `root`, or a user with passwordless `sudo`.
- **SSH port**: only if the port is not 22.
- **SSH private key**: a key without a passphrase. Geass stores the key in a Secret named `<name>-ssh` in `geass-system`, and never shows the key again.

If you can, also paste the host keys of the server. The host keys pin the machine, as [Pin the server's host key](#pin-the-servers-host-key-recommended) tells. The name is required. Geass uses the name as the name of the node.

```bash
geass servers add edge-1 --address 198.51.100.7 --ssh-user root \
  --ssh-key-file ~/.ssh/geass_ed25519 --known-hosts-file ./known_hosts --tag edge
```

For a cloud machine, select the **Cloud connection**, the **Region**, and the **Machine size**. The form lists the sizes of the cloud. Where the cloud gives a price for an hour, the form shows that price. You can also set an image and the settings of the cloud. These values are optional. If you leave the name empty, you get a generated name.

```bash
geass sizes --connection hetzner --region fsn1 --min-vcpus 2 --min-memory 4 --limit 10
geass servers add --connection hetzner --region fsn1 --size cx23
```

:::caution
A cloud machine is **billed to your account at the cloud until you remove the server**. The creation of machines is experimental. For more information, see [Cloud connections for machines](/docs/guides/connectors/clouds/#how-far-to-trust-it).
:::

Both forms take a placement, that is, a region, a site, and tags. Both forms also take **Data on this server**:

- `Allow` lets databases, buckets, and volumes run on the server. `Allow` is the default for a single server.
- `None` keeps databases, buckets, and volumes off the server, so that only services run there.

A server with `None` carries the taint `geass.dev/ephemeral`. Services tolerate this taint only while a server or a [pool](/docs/guides/node-pools/#what-runs-on-a-pools-nodes) whose nodes hold no data exists. When you add the first such server, every service rolls one time to add the toleration. The first pool has the same effect. To watch the server start, run `geass nodes list`, or look at the **Nodes** table.

To remove a server that you added this way, click **Remove** in the **Nodes** table, or run `geass servers remove NAME`. For more information, see [Remove a node](#remove-a-node). You cannot remove the control plane or the nodes of a pool there. Change the pool instead.

## Before you start

The requirements in this section are for a server that you already have. A cloud machine needs an admin, a connection, and the network paths that [How a machine joins](/docs/guides/node-pools/#how-a-machine-joins) lists. Geass opens some of those paths for the machine.

Make sure that these conditions are true:

- The cluster is `Ready`. To check, run `sudo k3s kubectl -n geass-system get geasscluster`.
- You have a new Linux server that meets the [requirements](/docs/getting-started/requirements/). The control plane can reach the server over SSH.
- The server has a user that is `root`, or a user that can run `sudo` without a password. Every provisioning step runs through `sudo -n`.
- The network paths in the next table are open in the firewall of the provider and in any host firewall on the new server. Geass opens none of these paths on a worker in a default (single-network) cluster.

| From | To | Port | Why |
| --- | --- | --- | --- |
| Control plane | New server | 22/tcp, or the `sshPort` that you choose | SSH provisioning and removal |
| New server | Control plane | 6443/tcp | The K3s agent joins the Kubernetes API. Geass checks this path before it installs |
| Every node | Every other node | 8472/udp | VXLAN overlay network |
| Control plane and Prometheus | New server | 10250/tcp | kubelet, for container metrics |
| New server | Internet | 443/tcp | The download of K3s from `get.k3s.io` and of container images |

For nodes at different providers, see [Multi-provider clusters](/docs/operations/multi-provider/). You must install the cluster in multi-site mode, and the ports are different: 51820/udp instead of 8472/udp. For a server behind a NAT with no public address, see [Mesh networking](/docs/operations/mesh-networking/). That page also tells how to join a machine that the control plane cannot reach over SSH.

## Set the address workers join

A worker joins the address that is stored in `spec.serverURL` of the cluster object. Usually, you do not change this address. The installer writes the address of the control plane there. It is the address that the default route of the control plane uses:

- On most cloud servers, it is the private address, for example `https://10.0.0.5:6443`.
- On a server that has its public address on the network interface, it is the public address.

A cluster that you install with `GEASS_MULTI_SITE=1` has the public address of the control plane instead, because its workers are at other providers.

Read the address that the installer stored:

```bash
sudo k3s kubectl -n geass-system get geasscluster default -o jsonpath='{.spec.serverURL}{"\n"}'
```

Set the address yourself only when the workers must reach the control plane on a different address from the one that the installer found. For example:

- A server with several network interfaces.
- Workers that reach the control plane through a public address or a name.

Use an address that the new server can reach, and make sure that 6443/tcp is open to it:

```bash
sudo k3s kubectl -n geass-system patch geasscluster default --type merge \
  -p '{"spec":{"serverURL":"https://10.0.0.5:6443"}}'
```

If `serverURL` is a loopback address, Geass does not use it. A loopback address is `127.0.0.1`, `localhost`, or `::1`. Clusters that older versions installed have a loopback address. A new install that could not find its own address also has one. Instead, Geass joins the worker on the address that the Kubernetes node of the control plane registered with. This is the internal address, or the external address in a multi-site cluster. Geass does not edit the cluster object. If no control plane node has an address either, the worker fails at the step `check-cluster-reachable`. The message names the loopback address as the cause.

Geass checks the address from the new server with `curl .../ping` before it installs anything. So 6443/tcp must be open to the address. When the check fails, `status.message` says what curl found:

| Message says | Cause |
| --- | --- |
| `spec.serverURL on the GeassCluster is a loopback address` | The stored address means "this machine" to every machine. Geass found no control plane node address to use instead. Patch `serverURL` as shown earlier, then delete the node and create it again |
| `the connection was refused` | Nothing listens at that address and port, or a firewall rejects the connection |
| `the connection timed out` | A host firewall or the security group of the provider drops 6443/tcp, or the new server has no route to that address |
| `the host name does not resolve` | `serverURL` uses a name that the new server cannot look up |
| `something answered, but it is not a K3s API server` | The address belongs to another machine or service |

## Prepare the new server

1. Create a key pair that you use only for this purpose. The key must have no passphrase:

   ```bash
   ssh-keygen -t ed25519 -N "" -C geass-worker -f ./geass-worker
   ```

2. Add `geass-worker.pub` to the `authorized_keys` of the SSH user on the new server.
3. Copy the private key to the control plane.
4. Save the private key as a Secret in `geass-system`. Geass reads the first of these keys that exists, in this order: `ssh-privatekey`, `id_rsa`, `private-key`, and `key`.

   ```bash
   sudo k3s kubectl -n geass-system create secret generic worker-ssh-key \
     --from-file=ssh-privatekey=./geass-worker
   ```

5. When you no longer need the local copy of the private key, delete it.

### Pin the server's host key (recommended)

By default, Geass does not check the SSH host key of the server. Geass connects to the machine that answers at `eip`, and runs commands there as root. The commands include the join token. To make sure that the machine is yours, put the host key of the machine in the same Secret, under the key `known_hosts`. Get the host key from the machine itself or from the console of the provider, and compare the fingerprint there. A key that you get over the network that you want to protect proves nothing.

```bash
ssh-keyscan -t ed25519 203.0.113.20 > known_hosts            # sshd on port 22
ssh-keyscan -t ed25519 -p 2222 203.0.113.20 > known_hosts    # sshd on another port
ssh-keygen -lf known_hosts                                   # the fingerprint to compare

sudo k3s kubectl -n geass-system create secret generic worker-ssh-key \
  --from-file=ssh-privatekey=./geass-worker \
  --from-file=known_hosts=./known_hosts
```

When `known_hosts` is present, Geass refuses a machine that presents any other key. Geass refuses the machine before it sends any command or the join token to it. Then `status.message` says `the SSH host key of ... is not the key recorded in known_hosts` or `... is not listed in known_hosts`. The same check applies when Geass removes the node.

- The file holds plain lines in the form that `ssh-keyscan` prints them, one line for each machine. So one Secret can serve several nodes that share a key pair.
- Geass refuses hashed host names (`ssh-keyscan -H`) and `@cert-authority` or `@revoked` lines with an error. Geass does not skip these lines, so the check is never weaker than it looks.
- The port is part of the address. A key that you record for `203.0.113.20` does not vouch for `203.0.113.20` on port 2222. `ssh-keyscan -p` writes the entry in the correct form.
- A rebuilt machine has a new host key. Update `known_hosts` in the Secret before you provision the machine again or remove it. Geass reads the Secret each time. An unattended re-provisioning of a node that stayed `NotReady` fails until you update the key.
- Geass does not record a key on first use to compare it later (trust on first use). If Geass did that, a rebuilt machine would fail its own repair. Also, the first connection, which is the one that carries the token, would still be unchecked.

## Join a worker

You can describe the worker yourself with `kubectl`, instead of with the dashboard or `geass servers add`. Save this manifest as `worker-1.yaml`, and then apply it:

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

The name `worker-1` becomes the name of the Kubernetes node. So make the name a lowercase DNS label that no other node uses. `eip` is the address that Geass connects to over SSH. It can be any address or hostname that the control plane can reach. In practice, `sshUser` is required, because an empty user cannot sign in.

## Check progress

```bash
sudo k3s kubectl -n geass-system get geassnode worker-1 \
  -o jsonpath='{.status.phase}{"  "}{.status.message}{"\n"}'
sudo k3s kubectl get nodes
```

`get geassnode` lists only names and ages. So read `status.phase` and `status.message` with the command above.

In the dashboard, the node appears in the **Nodes** table on the **Usage** page after Kubernetes registers it. The footer of the left rail shows one bar for each node. The footer also shows `2 NODES`, and then `all healthy` or the node with the worst problem. The same table is also at **Settings → Usage**.

If your token can read the nodes, `geass nodes list` prints each node with its status, region, site, tags, CPU, and memory. The command also prints the pool that each node belongs to, and whether the node is ephemeral.

### States

| Phase | Meaning |
| --- | --- |
| *(empty)* or `Pending` | Not started. The controller waits until the cluster is `Ready`, and checks every 15 seconds. Then the controller provisions the node |
| `Joining` | Geass runs the provisioning steps over SSH. The message is `installing k3s agent`. Then Geass waits for a Kubernetes node with the same name to report `Ready`. Geass checks every 15 to 20 seconds |
| `Ready` | The node is `Ready`. Geass looks at the node again every 10 minutes. Geass also looks immediately when the Kubernetes node goes `NotReady` or comes back, or when its region, site, tag labels, or taints change. Geass no longer records `lastHeartbeatTime`. The kubelet keeps its own heartbeat on the Kubernetes node (`sudo k3s kubectl get node worker-1`, and the node lease) |
| `Failed` | A step failed. `status.message` has the step, the error, and the standard output and error of the command |
| `Creating` | Only for a server whose machine Geass creates. Geass is making the machine, or the machine runs and waits for SSH. For the messages, see [Node pools](/docs/guides/node-pools/#pool-states). `Joining` and `Ready` follow this phase |

Geass sets no timeout on `Joining`. If provisioning finished but the node never becomes `Ready`, the object stays in `Joining` until you fix the cause. Geass provisions a `Ready` worker again from the start in these cases:

- The Kubernetes node of the worker disappears.
- The node stays `NotReady` for more than five minutes. The message becomes `node NotReady, re-provisioning`.

Geass asks to be woken when the five minutes are over, so it does not wait for the next 10-minute check. Re-provisioning needs the SSH key and the SSH access to still work.

To retry after `Failed`, fix the cause. Then request another attempt with the retry annotation:

```bash
sudo k3s kubectl -n geass-system annotate geassnode worker-1 geass.dev/retry=true
```

Geass sets the phase back to `Pending`, clears the old message, and removes the annotation. Then Geass provisions the node again from the first step. Any value of the annotation works. On a node that is not `Failed`, there is nothing to retry, so Geass removes the annotation and does not act on it. Geass never retries a later failure automatically. To retry again, add the annotation again. You can also delete the object and create it again from the same manifest. This method clears the status completely.

When the cluster changes size, Longhorn needs a ready manager on every node before it reports ready. So the cluster can briefly report not ready while a new node starts. Avoid a deploy during that time.

## Tag a node with a region, site and tags

Placement groups nodes, so that you can limit workloads to part of the cluster. A node has these placement values:

- An optional **Region**.
- An optional **Site**: the provider or the location inside the region, such as `aws-eu-north-1`.
- Up to 16 **Tags**, such as `storage` or `gpu`.

You can reserve a tag. Then only workloads that ask for that tag run on the node. For how projects and services use placement, see [Scaling and placement](/docs/guides/scaling-and-placement/).

In the dashboard, open **Usage** in the left rail. In **Nodes**, find the node, and click **Edit**. Then complete these fields:

| Field | Meaning |
| --- | --- |
| **Region** | Projects that you pin to a region run only on nodes in that region. Empty means no region |
| **Site** | The provider or site inside the region |
| **Tags** | What the node is for, with a comma between the tags |
| **Reserve this node for `<tag>`** | One switch for each tag. A reserved node runs only workloads that ask for that tag |
| **`<tag>` is a compliance tag** | One switch for each tag, under **Compliance tags**. A compliance tag is always reserved. Projects that require the tag run only on nodes that carry it, and nothing else runs on those nodes. See [Compliance node tags](/docs/guides/compliance-tags/) |

Region, site, and tags contain lowercase letters, numbers, and hyphens. Each value is at most 40 characters, and cannot start or end with a hyphen. The edit does not move running workloads. A workload that restarts returns only to a node that matches. The edit also works on the control plane and on nodes that Geass did not join.

From a terminal, `geass nodes tag worker-1 --region eu --tags storage --reserved storage` does the same. This command replaces the whole placement of the node, so name every tag that the node must keep.

You can also declare the same placement in the `GeassNode`. Then Geass applies the placement when the node joins, as labels and taints in the K3s configuration of the agent:

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

Geass applies the declared placement to the node and keeps it there:

- The label `topology.kubernetes.io/region`.
- The label `topology.kubernetes.io/zone`.
- The label `tag.geass.dev/<tag>=true`.
- For a reserved tag, the taint `tag.geass.dev/<tag>=reserved:NoSchedule`. For a compliance tag, the taint ends with `=compliance:NoSchedule` instead.

Without `placement`, Geass does not change the labels of a node. When you edit a node in the dashboard, the edit also updates its `GeassNode`, so the operator does not undo the edit.

## Remove a node

To remove a server that you added from the dashboard or with `geass servers add`, click **Remove** in the **Nodes** table, or run `geass servers remove NAME`. In the dashboard, you enter the name of the server to confirm. Both methods delete the `GeassNode` of the server. You can also delete the object yourself:

```bash
sudo k3s kubectl -n geass-system delete geassnode worker-1
```

For a server that you have, Geass then does these steps:

1. Geass connects over SSH on `spec.sshPort`, and runs `/usr/local/bin/k3s-agent-uninstall.sh`. When the Secret has a `known_hosts` entry, Geass checks the host key against it. If this step fails, Geass logs the failure and continues the removal.
2. Geass deletes the node from the cluster, so that the node does not stay listed as `NotReady`.
3. Geass releases the object.

:::caution
Move data off a node before you remove it. The removal of a server that you have does not cordon or drain the node. When K3s is removed, its pods stop. Kubernetes starts replacements on other nodes after the node object is gone. Data does not move with the pods. A volume whose only copy is on the disk of that node, such as a local-path volume, is lost when K3s is uninstalled. A replicated Longhorn volume keeps its replicas on other nodes.

The **Remove server** dialog lists the data that is on the server and nowhere else:

- A database whose only copy is on the server.
- A replica of a database that has other copies. Geass builds a new replica on another server a few minutes after the removal.
- A volume with a single copy.

Read the list before you enter the name. Geass replaces a replica of a highly available Postgres database within about three minutes of the removal. For more information, see [what Geass does after fifteen minutes](/docs/operations/high-availability/#what-geass-does-after-fifteen-minutes).
:::

A machine that Geass created is different, because Geass deletes the machine. Geass cordons the node, and asks its pods to leave for up to five minutes. Geass uses the eviction API for this, so that it honors disruption budgets. Then Geass deletes the Kubernetes node and the machine at the cloud. Geass releases the object only after the cloud no longer lists the machine as alive. The data on the disk of the machine is deleted with the machine. If the connection that made the machine is gone permanently, annotate the node `geass.dev/abandon-instance=true`. Geass then releases the node, and leaves the machine at the cloud for you to delete.

If the server was unreachable, the K3s agent continues to run there and can register again. Run `/usr/local/bin/k3s-agent-uninstall.sh` on the machine yourself.

## What happens under the hood

The operator runs these commands on the new server in sequence. Each command runs through `sudo -n sh -c`, unless the user is `root`. The operator opens an SSH connection on `spec.sshPort` with a 30-second timeout. It authenticates only with the key. When the Secret has a `known_hosts` entry, the operator checks the host key of the server. For more information, see [Pin the server's host key](#pin-the-servers-host-key-recommended).

| Step | Command |
| --- | --- |
| `check-curl` | `which curl`. If curl is missing, `apt-get update` and `apt-get install -y curl` |
| `install-iscsi` | Installs and enables the iSCSI initiator. This step is optional: a failure does not stop the join |
| `install-tailscale`, `join-tailnet` | Only for a [Tailscale mesh](/docs/operations/mesh-networking/): installs Tailscale if it is missing, and brings the node onto the tailnet |
| `check-cluster-reachable` | `curl -sk --max-time 15 <serverURL>/ping`, which must answer `pong` |
| `check-wireguard`, `open-overlay-port` | Only for multi-site clusters: tests WireGuard, and opens 51820/udp and 10250/tcp in `ufw` or `firewalld` |
| `write-k3s-config` | Overwrites `/etc/rancher/k3s/config.yaml` with the node labels and taints from `placement`, kubelet log rotation (50Mi, 5 files), and, for multi-site, `node-external-ip` |
| `install-k3s` | Runs the K3s installer in `agent` mode, with `K3S_URL`, `K3S_TOKEN`, and `K3S_NODE_NAME` set |
| `enable-k3s` | `systemctl enable k3s-agent && systemctl start k3s-agent` |

The K3s version is the oldest version among the control plane nodes, because an agent must not be newer than its server. If there is no K3s version to match, the K3s installer selects its current stable release. A worker that joined keeps that version until `spec.k3s.version` on the cluster object requests another version. Then the operator upgrades the worker in place over SSH. For more information, see [Upgrade Geass](/docs/operations/upgrading/#workers).

These are the credentials that the join uses:

- **The SSH private key** is in the Secret that you created, in `geass-system`. The operator reads the key each time it provisions or removes the node. Geass never sends the key to the dashboard. Anyone who can read Secrets in `geass-system` can read the key. So use a key that you make for this purpose, and limit what the key unlocks.
- **The join token** is the Secret `geass-token`. The token is part of the command that runs on the new server. So local users can see the token in the process list while the install runs.
- **Host keys are verified only if you pin them.** If the Secret has no `known_hosts` entry, the operator accepts any host key that the server presents. So the operator connects to any machine that answers at `eip`. Pin the key, or provision over a network that you trust.
- You can remove the public key from the new server after the server joins. Then Geass cannot provision the node again, or uninstall the agent when you delete the node.

## Options

| `GeassNode` field | Meaning | Default |
| --- | --- | --- |
| `spec.role` | `Worker` or `ControlPlane`. Geass provisions only a `Worker`. A `ControlPlane` entry only waits for a node of that name to register, and installs nothing | required |
| `spec.eip` | The address that Geass connects to over SSH. In a multi-site cluster, it is also the address that the node advertises | |
| `spec.sshPort` | The SSH port for provisioning and for removal | `22` |
| `spec.sshUser` | The SSH user: `root`, or a user with passwordless `sudo` | |
| `spec.sshKeySecretRef` | The Secret that holds the private key. Optionally, the Secret also holds the `known_hosts` that pin the host key of the machine. Fields: `name`, and an optional `namespace` (default: the namespace of the object). Required for workers | |
| `spec.clusterRef` | The `GeassCluster` to join. Fields: `name`, and an optional `namespace` | required |
| `spec.placement` | `region`, `zone`, and `tags`. Each tag has `name`, `reserved`, and `compliance` | none |
| `spec.default` | Marks a control plane node that already exists. Geass must not provision this node | `false` |
| `spec.cloud` | Makes Geass create the machine of the node at a cloud. Fields: `connectionRef`, `region`, `size`, `image`, `spot`, `addressType`, and `settings`. With `spec.cloud`, leave out `eip`, `sshUser`, and `sshKeySecretRef`. Geass signs in with a key of its own. See [GeassNode](/docs/reference/resources/geass-node/#a-machine-geass-creates) | none |
| `spec.storage` | `None` or `Allow`: whether the node can hold data. For a node of a pool, the value comes from the pool | `Allow` for any other node |

## Troubleshooting

Read `status.message` first. Provisioning errors have the form `step <name> failed: <error>`, followed by the standard output and error of the command.

**`ssh dial 203.0.113.20: dial tcp ...: i/o timeout` or `connection refused`.** The control plane cannot reach the server on the SSH port. Check the firewall of the provider, the host firewall, `eip`, and `sshPort`.

**`ssh dial ...: ssh: handshake failed: ssh: unable to authenticate`.** The user or the key is wrong, or the public key is not in `authorized_keys` for that user.

**`ssh dial ...: parse key: ssh: this private key is passphrase protected`.** Geass cannot use a key with a passphrase. Create a key without a passphrase.

**`step install-k3s failed ... sudo: a password is required`** (or any step that fails under `sudo`). Give the user passwordless `sudo`, or use `root`.

**`step check-cluster-reachable failed ... cannot reach the cluster API`.** The rest of the message tells the cause. A refused connection or a timeout points at a closed 6443/tcp between the new server and the control plane. A loopback address points at `serverURL`. See the table in [Set the address workers join](#set-the-address-workers-join).

**`ssh dial ...: the SSH host key of ... is not the key recorded in known_hosts`, or `... is not listed in known_hosts`.** One of these is the cause:

- The machine at `eip` is not the machine whose key you recorded.
- The machine was rebuilt.
- The entry is for another address or port.

Check the fingerprint on the machine itself. Then update `known_hosts` in the Secret.

**`ssh dial ...: known_hosts uses the @revoked marker`, `has hashed host names`, or `has no host keys in it`.** Geass cannot use the `known_hosts` entry in the Secret. Replace it with plain lines from `ssh-keyscan`.

**The node stays `Failed` after you added the retry annotation.** The annotation restarts only a node that is `Failed`. Check `status.phase`. Also make sure that the annotation key is exactly `geass.dev/retry`.

**`step check-curl failed`.** The server has no `curl` and no `apt-get`. Install `curl` yourself.

**`ssh key secret ref is required for worker nodes`, `ssh key: secrets "..." not found`, or `ssh key: no private key found in secret ...`.** Check `spec.sshKeySecretRef` and the key names in the Secret.

**`join token: secrets "geass-token" not found`.** The Secret that the installer creates is missing. Create it again from the K3s token on the control plane: `sudo k3s kubectl -n geass-system create secret generic geass-token --from-file=token=/var/lib/rancher/k3s/server/node-token`.

**`cluster not found: ...`.** `spec.clusterRef` names a cluster that does not exist in that namespace.

**The phase stays `Joining`.** Provisioning finished, but the node did not register as `Ready`. On the new server, run `sudo systemctl status k3s-agent` and `sudo journalctl -u k3s-agent`. These are the typical causes:

- 8472/udp is closed between nodes.
- 6443/tcp is closed.
- A node with that name already exists in the cluster.

**The node is missing from the Nodes table.** The table lists every node that Kubernetes knows about. If the node is not there, it did not register. Check `sudo k3s kubectl get nodes` and the phase, as the section "Check progress" tells.

**The cluster shows not ready after a node joined.** Longhorn is starting on the new node. Check `sudo k3s kubectl -n longhorn-system get pods -o wide`.

## Related

- [Requirements](/docs/getting-started/requirements/)
- [Scaling and placement](/docs/guides/scaling-and-placement/)
- [Multi-provider clusters](/docs/operations/multi-provider/)
- [High availability](/docs/operations/high-availability/)
- [Storage](/docs/guides/storage/)
