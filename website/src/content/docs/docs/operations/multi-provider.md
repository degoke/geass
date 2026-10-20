---
title: "Nodes at more than one provider"
description: "Experimental: build one cluster from servers at different providers over an encrypted WireGuard overlay. Checklist, firewall rules, checks and fixes."
---

By default, the nodes of a Geass cluster share a private network. Multi-provider mode builds a cluster whose nodes sit at different providers and reach each other over the internet, through an encrypted WireGuard overlay. Use it when you want capacity or regions at more than one provider and accept the limits below.

:::caution
Multi-provider mode is **experimental**. It has run on real servers: a four-node cluster at one provider, in several availability zones, joined over their public addresses, with traffic carried by the encrypted overlay. It has **not** been run across two different providers, and public traffic arriving from the internet has not been tested on such a cluster. Its control plane stays at one site and is a single point of failure. Do not move production workloads onto it until you have tested it on your own servers.

Creating the machines of such a cluster through Geass, with [node pools](/docs/guides/node-pools/), is experimental too, and for a separate reason: only the Amazon EC2 driver has run against a live account (in one region, in this mode), and the other eleven have not. A pool at a second provider is both experiments at once.
:::

## What changes

You choose multi-provider mode when you install the control plane. It cannot be switched on later: changing it means restarting every node, and Geass has no way to do that for you.

| | Default | Multi-provider |
| --- | --- | --- |
| Overlay between nodes | VXLAN, UDP 8472, not encrypted | WireGuard (`wireguard-native`), UDP 51820, encrypted |
| Address nodes advertise | Private | Public IP |
| Address workers join on | The cluster's `spec.serverURL`. The installer writes the control plane's own address on its default route, such as `https://10.0.0.5:6443` | `https://<control plane public IP>:6443`, set by the installer |
| API certificate | Local names | Also valid for the control plane's public IP |
| Container metrics | From each node | Prometheus reaches each kubelet on its public address |

The installer writes this into the K3s server configuration (`/etc/rancher/k3s/config.yaml`): `flannel-backend: wireguard-native`, `node-external-ip`, `flannel-external-ip: true`, `tls-san` with the public IP, the kubelet log limits, and `secrets-encryption: true`. The cluster object records `spec.network.multiSite: true`.

## Before you start

Check each item before you begin.

- [ ] **One control plane site.** The first server you install is the control plane. Keep it at one site.
- [ ] **A public address for every node.** A fixed IP that the other nodes can reach, such as `203.0.113.10`.
- [ ] **A kernel with WireGuard on every node.** Built in from Linux 5.6. Older kernels need the `wireguard` package. The installer and the worker setup both test this and stop with a clear message.
- [ ] **The usual requirements.** At least 2 GB of memory and 10 GB free on `/`. See [Requirements](/docs/getting-started/requirements/).
- [ ] **Firewall and security group rules** at every provider (next section).
- [ ] **SSH from the control plane** to each future worker, as `root` or as a user with passwordless `sudo` such as `ubuntu`. Geass joins workers over SSH.

### Ports to open

Open these between the nodes' public addresses, and in each provider's security group:

| Port | Between | Why |
| --- | --- | --- |
| **TCP 6443** | Every node to the control plane, **including the control plane to itself** | In this mode the API is advertised on the control plane's public address, so the control plane reaches its own API through it. Without this the operator and most system services cannot start. |
| **UDP 51820** | Every pair of nodes | The WireGuard overlay. |
| **TCP 10250** | Every pair of nodes | Container metrics are collected from every node, and live logs are read from each node's kubelet. |
| **TCP 22** | The control plane to each new worker | Geass joins workers over SSH. |

To serve visitors, also open **TCP 80 and 443** on the nodes your DNS points at. Let's Encrypt validates a domain over port 80, so certificates need it too. The installer opens 6443, 10250, the overlay port, 80 and 443 on the control plane when a host firewall (`ufw`, `firewalld` or `iptables`) is active, and warns when it finds none. It opens nothing on workers except in this mode, where the worker setup opens the overlay port and 10250 on an active `ufw` or `firewalld`. Neither can change a provider's security group.

## Install the control plane

On the machine that will be the control plane, give the installer its public address:

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | \
  sudo env GEASS_MULTI_SITE=1 GEASS_NODE_EXTERNAL_IP=203.0.113.10 sh
```

| Variable | Meaning |
| --- | --- |
| `GEASS_MULTI_SITE` | `1`, `true`, `yes` or `on` turns multi-provider mode on. |
| `GEASS_NODE_EXTERNAL_IP` | This machine's public IP. Required in this mode, and it must be an IP address. |

The installer does the rest of the normal install ([Install Geass](/docs/getting-started/install/)). Add `GEASS_ACME_EMAIL` to the same `env` list if you want HTTPS certificates; see [A certificate is never issued](/docs/operations/troubleshooting/#a-certificate-is-never-issued). Check the cluster:

```bash
sudo k3s kubectl get nodes -o wide
sudo cat /etc/rancher/k3s/config.yaml
sudo k3s kubectl -n geass-system get geasscluster default -o jsonpath='{.spec.network}{"\n"}{.spec.serverURL}{"\n"}'
```

Expect the node's `EXTERNAL-IP` to be the public address, `{"multiSite":true}` and a server URL that begins `https://203.0.113.10:6443`.

## Add workers

For each worker you already have, add it from the dashboard or `geass servers add`, or create an SSH key Secret and a `GeassNode` as below. Declare the node's region and site so it carries them from its first moment. The full procedure is in [Add servers](/docs/getting-started/add-servers/). To have Geass create the workers at the providers instead, see [Pools at several providers](#pools-at-several-providers).

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

The node moves from `Pending` to `Joining` to `Ready`. Before it installs K3s, Geass checks that the worker can reach the control plane's API. In multi-provider mode it also checks that the worker's kernel can create a WireGuard interface, and it opens the overlay port on an active host firewall. Then it writes the worker's K3s configuration (its labels, taints, public address and log limits) and installs the agent at the control plane's K3s version.

:::caution
Geass does not verify the worker's SSH host key unless you pin it. Over the public internet, put the worker's host key in the SSH key Secret under `known_hosts` (see [Add servers](/docs/getting-started/add-servers/#pin-the-servers-host-key-recommended)), so that nothing but the machine you mean can receive the join token, and restrict port 22 on the worker to the control plane's address.
:::

## Pools at several providers

A [node pool](/docs/guides/node-pools/) creates machines at a cloud through a [connection for machines](/docs/guides/connectors/clouds/), so a cluster can grow at more than one provider: one pool for each, each with its own region and placement. A cluster that spans providers has to be installed in multi-site mode, as above. In a default (single-network) cluster the overlay is VXLAN, on 8472/udp between nodes, and no driver opens that port. A pool there needs a firewall of yours that allows it, and suits machines on the same private network as the control plane, with `addressType: Private`.

What Geass does for each machine it creates, and what it leaves to you:

| Path | Who opens it |
| --- | --- |
| **TCP 22** from the control plane to the machine | Geass, in the firewall it makes at the provider (a security group, a firewall, firewall rules), for the addresses Kubernetes lists for the control plane's nodes. Not at Oracle Cloud or OVHcloud, where it makes none |
| **TCP 10250** from the control plane to the machine | The same firewall. It lets the kubelet port in from the control plane's addresses only: if Prometheus runs on another node than the control plane, add that node's address to a firewall of your own for the pool |
| **UDP 51820** between every pair of nodes | The same firewall, from anywhere, because the overlay authenticates every packet |
| **TCP 6443** from the machine to the control plane (and UDP 51820 in multi-site mode) | **You**, unless you set [`spec.network.controlPlaneFirewall`](/docs/guides/node-pools/#let-geass-open-the-control-plane) on the cluster. Then Geass adds the machine's address to the control plane's security group or Hetzner firewall before the machine joins, and takes it out when the machine goes. For a control plane at any other provider, open the port yourself |

Geass never opens a port on a machine you brought, and never changes a firewall you name yourself in a pool's `settings`. Machines at different providers cost money at each, and the bill of each is the bill of its connection's account. Give each pool the region and tags of the projects that should use it: a project in the region `eu` runs only on nodes in `eu`, so a pool whose nodes carry no region does not help it. See [Node pools](/docs/guides/node-pools/#what-runs-on-a-pools-nodes).

## Choose regions

Tag nodes with a region, a site and tags on the **Usage** page: open **Usage** and select **Edit** on a node. See [Scaling and placement](/docs/guides/scaling-and-placement/). A common layout is one region per provider.

Give each project a region. A project without one may run on any node, and its database volumes then spread their copies across every node, which in this mode means across the internet. A project in a region keeps its workloads, its builds and its volume copies on that region's nodes. A project that also requires a [compliance tag](/docs/guides/compliance-tags/) keeps them on the nodes of that region that carry the tag.

## Verify the cluster

Work through these after you add the first worker.

1. **Every node is ready, with a public address.**

   ```bash
   kubectl get nodes -o wide
   ```

   `EXTERNAL-IP` shows each node's public address, and `STATUS` is `Ready`.

2. **Pods on different nodes can talk.** Start two pods pinned to two nodes in different places and ping one from the other:

   ```bash
   kubectl run net-a --image=busybox:1.36 --restart=Never \
     --overrides='{"spec":{"nodeName":"node-1"}}' -- sleep 600
   kubectl run net-b --image=busybox:1.36 --restart=Never \
     --overrides='{"spec":{"nodeName":"worker-eu-1"}}' -- sleep 600
   IP=$(kubectl get pod net-b -o jsonpath='{.status.podIP}')
   kubectl exec net-a -- ping -c 3 "$IP"
   kubectl delete pod net-a net-b
   ```

   Replace the node names with yours. If `wireguard-tools` is installed, `sudo wg show` on a node lists the WireGuard interface and one peer for each other node.

3. **The Usage page lists every node** with the region and site you set, none under **NetworkUnavailable**.
4. **Container metrics come from a worker.** Deploy a service to a worker's region and open its **Metrics** tab. CPU and memory appear after a minute or two. If they do not, TCP 10250 is closed.
5. **A project in a region runs only there.** Create a project in the new region and deploy a service. `kubectl get pods -n <project>-<environment> -o wide` shows it on a node of that region.

## What is different in this mode

- **Host charts cover one node.** **Machines busy (CPU)** and **Machines memory in use** are collected only for the node that runs monitoring, because the node exporters publish on private addresses that other sites cannot reach. Container metrics, which feed the service, project and database charts, are collected from every node. See [Logs, metrics and terminal](/docs/guides/observability/).
- **One control plane.** It stays at one site. If that site is down, the dashboard and the operator are down. See [High availability](/docs/operations/high-availability/).
- **Cross-site traffic crosses the internet.** Calls between pods on different providers pay the latency, and your bandwidth bills. Regions exist to keep a project's traffic in one place.
- **No conversion.** A cluster installed without `GEASS_MULTI_SITE` cannot be converted. Reinstall to switch.

## What happens under the hood

| On | File or object | Content |
| --- | --- | --- |
| Control plane | `/etc/rancher/k3s/config.yaml` | `flannel-backend: wireguard-native`, `node-external-ip`, `flannel-external-ip: true`, `tls-san`, `kubelet-arg`, `secrets-encryption: true`. |
| Worker | `/etc/rancher/k3s/config.yaml` | `node-label`, `node-taint`, `node-external-ip`, `kubelet-arg`. |
| Worker | Service `k3s-agent` | The K3s agent. Check it with `systemctl status k3s-agent` and `journalctl -u k3s-agent`. |
| Cluster | `GeassCluster` `default` | `spec.network.multiSite: true` and a public `serverURL`. |
| Cluster | `HelmChart` `geass-kube-prometheus-stack` | The monitoring operator starts with `--kubelet-node-address-priority=external`. |

K3s workers reject the control plane's `flannel-external-ip` setting, so Geass writes it only on the server.

## Troubleshooting

**The installer stops with "a multi-site cluster needs this node's public address: set GEASS_NODE_EXTERNAL_IP".**
Pass the variable through `sudo`: `sudo env GEASS_MULTI_SITE=1 GEASS_NODE_EXTERNAL_IP=203.0.113.10 sh`. A value that is not an IP address stops with `GEASS_NODE_EXTERNAL_IP "<value>" is not an IP address`.

**The installer stops with "this machine's kernel cannot create a WireGuard interface".**
The kernel is older than 5.6 or lacks the module. Install the `wireguard` package, or use a newer kernel.

**After install, the operator and system pods never become ready.**
The control plane cannot reach its own API on its public address. Open TCP 6443 from the control plane's own public IP in the provider's security group, and in any host firewall.

**A `GeassNode` is `Failed` with "cannot reach the cluster API at https://203.0.113.10:6443 from this node: ...".**
The rest of the message says why. "The connection was refused" or "the connection timed out" means the worker cannot reach the control plane on 6443/tcp, and the message adds that UDP 51820 must be open between every pair of nodes: fix the rule at both providers, in the host firewall and in the security group. "The host name does not resolve" or "something answered, but it is not a K3s API server" means `spec.serverURL` of the cluster is wrong: in this mode it is `https://<control plane public IP>:6443`.

**A `GeassNode` is `Failed` with "this node's kernel cannot create a WireGuard interface".**
The same kernel problem on the worker.

**A `GeassNode` is `Failed` with `ssh dial` or "no private key found in secret".**
`ssh dial` means the SSH port on the worker (`spec.sshPort`, default 22) is not reachable from the control plane, the user or key is wrong, the address in `eip` is wrong, or the machine's host key does not match the `known_hosts` in the Secret. A missing key means the Secret does not hold the key under `ssh-privatekey`, `id_rsa`, `private-key` or `key`.

**A step fails with `sudo: a password is required`.**
A user that is not `root` needs passwordless `sudo`. Geass runs each setup step through `sudo -n`.

**A node stays `Joining`.**
K3s installed but the node did not register. On the worker, read `journalctl -u k3s-agent`. Look for connection errors to port 6443 or a blocked UDP 51820.

**A node is `Ready` but pods on it cannot reach pods elsewhere.**
UDP 51820 is blocked between that node and another. Test the ping above between that pair of nodes, and check both providers' rules. The **Usage** page may show **NetworkUnavailable**.

**Charts are empty for services on a worker.**
Prometheus cannot reach that worker's kubelet. Open TCP 10250 between the nodes.

**Node charts show only one machine.**
Expected. See [What is different in this mode](#what-is-different-in-this-mode).

**A machine made by a pool stays `Creating` or `Joining`, or fails.**
The new machine cannot reach the control plane's 6443/tcp, or UDP 51820 is closed between it and the other nodes. Open them at the control plane's provider, or use `controlPlaneFirewall`. See [Node pools](/docs/guides/node-pools/#troubleshooting).

**A failed `GeassNode` does not retry after I fix the cause.**
Fixing the cause does not restart provisioning of a node that already failed by itself. Add the retry annotation: `kubectl -n geass-system annotate geassnode <name> geass.dev/retry=true`. Geass sets the node back to `Pending` and provisions it again. You can also delete the `GeassNode` and apply it again. Deleting also removes the node from the cluster and uninstalls K3s from the machine when Geass can reach it over SSH, on `spec.sshPort`.

## Related

- [Node pools and autoscaling](/docs/guides/node-pools/)
- [Cloud connections for machines](/docs/guides/connectors/clouds/)
- [Add servers](/docs/getting-started/add-servers/)
- [Scaling and placement](/docs/guides/scaling-and-placement/)
- [High availability](/docs/operations/high-availability/)
- [Troubleshooting](/docs/operations/troubleshooting/)
- [Requirements](/docs/getting-started/requirements/)
