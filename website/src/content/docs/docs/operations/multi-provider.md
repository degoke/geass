---
title: "Nodes at more than one provider"
description: "Experimental: build one cluster from servers at different providers over an encrypted WireGuard overlay. Checklist, firewall rules, checks, and fixes."
---

By default, the nodes of a Geass cluster share a private network. Multi-provider mode builds a cluster whose nodes are
at different providers. The nodes connect to each other over the internet, through an encrypted WireGuard overlay. Use
this mode when you want capacity or regions at more than one provider and you accept the limits below.

This page shows how to use that mode when each node has a public address. For these other cases, see
[Mesh networking](/docs/operations/mesh-networking/):

- Nodes behind a NAT with no public address.
- A Tailscale or Headscale network instead of WireGuard.
- The checks that Geass makes of a mesh (`spec.network.mesh`).

`GEASS_MESH=wireguard` is `GEASS_MULTI_SITE=1` with those checks turned on.

:::caution
Do not move production workloads onto a multi-provider cluster until you test it on your own servers. Multi-provider
mode is **experimental**:

- The mode has run on real servers: a four-node cluster at one provider, in several availability zones. The nodes
  joined over their public addresses, and the encrypted overlay carried the traffic.
- The mode has **not** run across two different providers.
- No one has tested public traffic from the internet on such a cluster.
- The control plane stays at one site and is a single point of failure.

If you use Geass to create the machines of such a cluster, with [node pools](/docs/guides/node-pools/), that is also
experimental, for a different reason. Only the Amazon EC2 driver has run against a live account, in one region, in
this mode. The other eleven drivers have not. A pool at a second provider is both experiments at the same time.
:::

## What changes

You select multi-provider mode when you install the control plane. You cannot turn it on later. To change the mode,
you must restart every node, and Geass cannot do that for you.

| | Default | Multi-provider |
| --- | --- | --- |
| Overlay between nodes | VXLAN, UDP 8472, not encrypted | WireGuard (`wireguard-native`), UDP 51820, encrypted |
| Address nodes advertise | Private | Public IP |
| Address workers join on | The cluster's `spec.serverURL`. The installer writes the control plane's own address on its default route, for example `https://10.0.0.5:6443` | `https://<control plane public IP>:6443`, set by the installer |
| API certificate | Local names | Also valid for the control plane's public IP |
| Container metrics | From each node | Prometheus reaches each kubelet on its public address |

The installer writes these settings into the K3s server configuration (`/etc/rancher/k3s/config.yaml`):

- `flannel-backend: wireguard-native`
- `node-external-ip`
- `flannel-external-ip: true`
- `tls-san` with the public IP
- The kubelet log limits
- `secrets-encryption: true`

The cluster object records `spec.network.multiSite: true`.

## Before you start

Check each item before you start.

- [ ] **One control plane site.** The first server that you install is the control plane. Keep it at one site.
- [ ] **A public address for every node.** A fixed IP that the other nodes can reach, for example `203.0.113.10`.
- [ ] **A kernel with WireGuard on every node.** WireGuard is built in from Linux 5.6. Older kernels need the
  `wireguard` package. The installer and the worker setup both test for WireGuard and stop with a clear message.
- [ ] **The usual requirements.** A server with 2 GB of memory (at least 1700 MiB of `MemTotal`) and 10 GiB free on `/`. See
  [Requirements](/docs/getting-started/requirements/).
- [ ] **Firewall and security group rules** at every provider. The next section lists them.
- [ ] **SSH from the control plane** to each future worker, as `root` or as a user with passwordless `sudo`, for
  example `ubuntu`. Geass joins workers over SSH.

### Ports to open

Open these ports between the public addresses of the nodes, and in the security group of each provider:

| Port | Between | Why |
| --- | --- | --- |
| **TCP 6443** | Every node to the control plane, **including the control plane to itself** | In this mode, the control plane advertises the API on its public address, so the control plane reaches its own API through that address. Without this rule, the operator and most system services cannot start. |
| **UDP 51820** | Every pair of nodes | The WireGuard overlay. |
| **TCP 10250** | Every pair of nodes | Prometheus collects container metrics from every node, and Geass reads live logs from the kubelet of each node. |
| **TCP 22** | The control plane to each new worker | Geass joins workers over SSH. |

To serve visitors, also open **TCP 80 and 443** on the nodes that your DNS points at. Let's Encrypt validates a domain
over port 80, so certificates also need port 80.

The installer opens ports on the control plane when a host firewall (`ufw`, `firewalld`, or `iptables`) is active. It
opens 6443, 10250, the overlay port, 80, and 443. If the installer finds no host firewall, it shows a warning. The
installer opens nothing on workers, except in this mode. In this mode, the worker setup opens the overlay port and
10250 on an active `ufw` or `firewalld`. Neither the installer nor the worker setup can change the security group of a
provider.

## Install the control plane

On the machine that becomes the control plane, give the installer its public address:

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | \
  sudo env GEASS_MULTI_SITE=1 GEASS_NODE_EXTERNAL_IP=203.0.113.10 sh
```

| Variable | Meaning |
| --- | --- |
| `GEASS_MULTI_SITE` | `1`, `true`, `yes`, or `on` turns multi-provider mode on. |
| `GEASS_NODE_EXTERNAL_IP` | The public IP of this machine. Required in this mode. The value must be an IP address. |

The installer does the rest of the normal install ([Install Geass](/docs/getting-started/install/)). If you want HTTPS
certificates, add `GEASS_ACME_EMAIL` to the same `env` list. See
[A certificate is never issued](/docs/operations/troubleshooting/#a-certificate-is-never-issued).

Check the cluster:

```bash
sudo k3s kubectl get nodes -o wide
sudo cat /etc/rancher/k3s/config.yaml
sudo k3s kubectl -n geass-system get geasscluster default -o jsonpath='{.spec.network}{"\n"}{.spec.serverURL}{"\n"}'
```

Make sure that you see these results:

- The `EXTERNAL-IP` of the node is the public address.
- The network shows `{"multiSite":true}`.
- The server URL starts with `https://203.0.113.10:6443`.

## Add workers

To add a worker that you already have, do one of these:

- Add it from the dashboard.
- Run `geass servers add`.
- Create an SSH key Secret and a `GeassNode`, as shown below.

Declare the region and site of the node, so that the node has them from its first moment. For the full procedure, see
[Add servers](/docs/getting-started/add-servers/). If you want Geass to create the workers at the providers instead,
see [Pools at several providers](#pools-at-several-providers).

```bash
kubectl -n geass-system create secret generic worker-ssh-key \
  --from-file=ssh-privatekey=$HOME/.ssh/id_ed25519
```

```yaml title="worker-eu-1.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassNode
metadata:
  name: worker-eu-1
  namespace: geass-system
spec:
  role: Worker
  eip: 198.51.100.20          # the worker's public IP: SSH host and advertised address
  sshUser: ubuntu
  sshKeySecretRef:
    name: worker-ssh-key
  clusterRef:
    name: default
  placement:
    region: eu
    zone: provider-b-fsn1
```

```bash
kubectl apply -f worker-eu-1.yaml
kubectl -n geass-system get geassnode worker-eu-1 -w
```

The node moves from `Pending` to `Joining` to `Ready`. Geass does these steps:

1. Before Geass installs K3s, it checks that the worker can reach the API of the control plane.
2. In multi-provider mode, Geass also checks that the kernel of the worker can create a WireGuard interface.
3. In multi-provider mode, Geass opens the overlay port on an active host firewall.
4. Geass writes the K3s configuration of the worker: its labels, taints, public address, and log limits.
5. Geass installs the agent at the K3s version of the control plane.

:::caution
Over the public internet, pin the SSH host key of the worker. Geass does not verify the host key of the worker unless
you pin it. Put the host key of the worker in the SSH key Secret under `known_hosts` (see
[Add servers](/docs/getting-started/add-servers/#pin-the-servers-host-key-recommended)). Then only the machine that you
mean can receive the join token. Also restrict port 22 on the worker to the address of the control plane.
:::

## Pools at several providers

A [node pool](/docs/guides/node-pools/) creates machines at a cloud through a
[connection for machines](/docs/guides/connectors/clouds/). So a cluster can grow at more than one provider, with one
pool for each provider. Each pool has its own region and placement.

You must install a cluster that spans providers in multi-site mode, as shown above. In a default (single-network)
cluster, the overlay is VXLAN, on 8472/udp between nodes, and no driver opens that port. A pool in a default cluster
needs a firewall of yours that allows that port. Such a pool suits machines on the same private network as the
control plane, with `addressType: Private`.

This table shows what Geass does for each machine that it creates, and what it leaves to you:

| Path | Who opens it |
| --- | --- |
| **TCP 22** from the control plane to the machine | Geass, in the firewall that it makes at the provider (a security group, a firewall, or firewall rules), for the addresses that Kubernetes lists for the nodes of the control plane. Not at Oracle Cloud or OVHcloud, where Geass makes no firewall |
| **TCP 10250** from the control plane to the machine | The same firewall. It lets the kubelet port in from the addresses of the control plane only. If Prometheus runs on a node other than the control plane, add the address of that node to a firewall of your own for the pool |
| **UDP 51820** between every pair of nodes | The same firewall, from anywhere, because the overlay authenticates every packet |
| **TCP 6443** from the machine to the control plane (and UDP 51820 in multi-site mode) | **You**, unless you set [`spec.network.controlPlaneFirewall`](/docs/guides/node-pools/#let-geass-open-the-control-plane) on the cluster. With that setting, Geass adds the address of the machine to the security group or Hetzner firewall of the control plane before the machine joins. Geass removes the address when the machine goes. For a control plane at any other provider, open the port yourself |

Geass never opens a port on a machine that you brought. Geass never changes a firewall that you name yourself in the
`settings` of a pool.

Machines at different providers cost money at each provider. The account of the connection pays for the machines of
that connection.

Give each pool the region and tags of the projects that must use it. A project in the region `eu` runs only on nodes
in `eu`. So a pool whose nodes have no region does not help that project. See
[Node pools](/docs/guides/node-pools/#what-runs-on-a-pools-nodes).

## Choose regions

To tag nodes with a region, a site, and tags, open the **Usage** page and click **Edit** on a node. See
[Scaling and placement](/docs/guides/scaling-and-placement/). A common layout is one region for each provider.

Give each project a region. A project without a region can run on any node. Its database volumes then spread their
copies across every node, and in this mode, that means across the internet. A project in a region keeps its workloads,
its builds, and its volume copies on the nodes of that region. A project that also requires a
[compliance tag](/docs/guides/compliance-tags/) keeps them on the nodes of that region that have the tag.

## Verify the cluster

After you add the first worker, do these checks.

1. **Every node is ready, with a public address.**

   ```bash
   kubectl get nodes -o wide
   ```

   `EXTERNAL-IP` shows the public address of each node, and `STATUS` is `Ready`.

2. **Pods on different nodes can talk.** Start two pods pinned to two nodes in different places. Then ping one pod
   from the other:

   ```bash
   kubectl run net-a --image=busybox:1.36 --restart=Never \
     --overrides='{"spec":{"nodeName":"node-1"}}' -- sleep 600
   kubectl run net-b --image=busybox:1.36 --restart=Never \
     --overrides='{"spec":{"nodeName":"worker-eu-1"}}' -- sleep 600
   IP=$(kubectl get pod net-b -o jsonpath='{.status.podIP}')
   kubectl exec net-a -- ping -c 3 "$IP"
   kubectl delete pod net-a net-b
   ```

   Replace the node names with your node names. If `wireguard-tools` is installed, `sudo wg show` on a node lists the
   WireGuard interface and one peer for each other node.

3. **The Usage page lists every node** with the region and site that you set. No node shows **NetworkUnavailable**.
4. **Container metrics come from a worker.** Deploy a service to the region of a worker and open its **Metrics** tab.
   CPU and memory appear after one or two minutes. If they do not appear, TCP 10250 is closed.
5. **A project in a region runs only in that region.** Create a project in the new region and deploy a service.
   `kubectl get pods -n <project>-<environment> -o wide` shows the service on a node of that region.

## What is different in this mode

- **Host charts cover one node.** Geass collects **Machines busy (CPU)** and **Machines memory in use** only for the
  node that runs monitoring. The reason is that the node exporters publish on private addresses that other sites
  cannot reach. Geass collects container metrics from every node. Container metrics feed the service, project, and
  database charts. See [Logs, metrics and terminal](/docs/guides/observability/).
- **One control plane.** The control plane stays at one site. If that site is down, the dashboard and the operator are
  down. See [High availability](/docs/operations/high-availability/).
- **Cross-site traffic crosses the internet.** Calls between pods at different providers have the latency of the
  internet, and you pay for the bandwidth. Regions keep the traffic of a project in one place.
- **No conversion.** You cannot convert a cluster that you installed without `GEASS_MULTI_SITE`. To change the mode,
  install again.

## What happens under the hood

| On | File or object | Content |
| --- | --- | --- |
| Control plane | `/etc/rancher/k3s/config.yaml` | `flannel-backend: wireguard-native`, `node-external-ip`, `flannel-external-ip: true`, `tls-san`, `kubelet-arg`, `secrets-encryption: true`. |
| Worker | `/etc/rancher/k3s/config.yaml` | `node-label`, `node-taint`, `node-external-ip`, `kubelet-arg`. |
| Worker | Service `k3s-agent` | The K3s agent. Check it with `systemctl status k3s-agent` and `journalctl -u k3s-agent`. |
| Cluster | `GeassCluster` `default` | `spec.network.multiSite: true` and a public `serverURL`. |
| Cluster | `HelmChart` `geass-kube-prometheus-stack` | The monitoring operator starts with `--kubelet-node-address-priority=external`. |

K3s workers reject the `flannel-external-ip` setting of the control plane, so Geass writes that setting only on the
server.

## Troubleshooting

**The installer stops with "a multi-site cluster needs this node's public address: set GEASS_NODE_EXTERNAL_IP".**
Pass the variable through `sudo`: `sudo env GEASS_MULTI_SITE=1 GEASS_NODE_EXTERNAL_IP=203.0.113.10 sh`. If the value is
not an IP address, the installer stops with `GEASS_NODE_EXTERNAL_IP "<value>" is not an IP address`.

**The installer stops with "this machine's kernel cannot create a WireGuard interface".**
The kernel is older than 5.6 or does not have the module. Install the `wireguard` package, or use a newer kernel.

**After install, the operator and system pods never become ready.**
The control plane cannot reach its own API on its public address. Open TCP 6443 from the public IP of the control
plane in the security group of the provider, and in all host firewalls.

**A `GeassNode` is `Failed` with "cannot reach the cluster API at https://203.0.113.10:6443 from this node: ...".**
The rest of the message gives the cause:

- "The connection was refused" or "the connection timed out" means that the worker cannot reach the control plane on
  6443/tcp. The message also says that UDP 51820 must be open between every pair of nodes. Fix the rule at both
  providers, in the host firewall and in the security group.
- "The host name does not resolve" or "something answered, but it is not a K3s API server" means that
  `spec.serverURL` of the cluster is wrong. In this mode, the value is `https://<control plane public IP>:6443`.

**A `GeassNode` is `Failed` with "this node's kernel cannot create a WireGuard interface".**
The worker has the same kernel problem.

**A `GeassNode` is `Failed` with `ssh dial` or "no private key found in secret".**
`ssh dial` means one of these problems:

- The control plane cannot reach the SSH port on the worker (`spec.sshPort`, default 22).
- The user or the key is wrong.
- The address in `eip` is wrong.
- The host key of the machine does not match the `known_hosts` in the Secret.

A missing key means that the Secret does not hold the key under `ssh-privatekey`, `id_rsa`, `private-key`, or `key`.

**A step fails with `sudo: a password is required`.**
A user that is not `root` needs passwordless `sudo`. Geass runs each setup step through `sudo -n`.

**A node stays `Joining`.**
K3s installed, but the node did not register. On the worker, read `journalctl -u k3s-agent`. Look for connection
errors to port 6443 or a blocked UDP 51820.

**A node is `Ready` but pods on it cannot reach pods elsewhere.**
UDP 51820 is blocked between that node and another node. Do the ping test above between that pair of nodes, and check
the rules at both providers. The **Usage** page might show **NetworkUnavailable**.

**Charts are empty for services on a worker.**
Prometheus cannot reach the kubelet of that worker. Open TCP 10250 between the nodes.

**Node charts show only one machine.**
This result is expected. See [What is different in this mode](#what-is-different-in-this-mode).

**A machine made by a pool stays `Creating` or `Joining`, or fails.**
The new machine cannot reach 6443/tcp on the control plane, or UDP 51820 is closed between the machine and the other
nodes. Open these ports at the provider of the control plane, or use `controlPlaneFirewall`. See
[Node pools](/docs/guides/node-pools/#troubleshooting).

**A failed `GeassNode` does not retry after I fix the cause.**
When you fix the cause, Geass does not restart the provisioning of a node that already failed. Add the retry
annotation: `kubectl -n geass-system annotate geassnode <name> geass.dev/retry=true`. Geass sets the node back to
`Pending` and provisions it again.

You can also delete the `GeassNode` and apply it again. When you delete the `GeassNode`, Geass also removes the node
from the cluster. If Geass can reach the machine over SSH on `spec.sshPort`, Geass also uninstalls K3s from the
machine.

## Related

- [Node pools and autoscaling](/docs/guides/node-pools/)
- [Cloud connections for machines](/docs/guides/connectors/clouds/)
- [Add servers](/docs/getting-started/add-servers/)
- [Scaling and placement](/docs/guides/scaling-and-placement/)
- [High availability](/docs/operations/high-availability/)
- [Troubleshooting](/docs/operations/troubleshooting/)
- [Requirements](/docs/getting-started/requirements/)
