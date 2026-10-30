---
title: "Mesh networking"
description: "Join servers at several sites, some behind a NAT with no public address, into one cluster over WireGuard or Tailscale. What Geass sets up, refuses, and checks."
---

A cluster usually keeps its nodes on one private network. A **mesh** joins nodes into one cluster network when the nodes are at different sites, and when they might be behind a NAT with no public address. You select the mesh when you install the control plane. The installer records the mesh on the cluster with `spec.network.mesh`. Geass supports two meshes:

- The WireGuard overlay of K3s. This mesh is the default, and you run nothing extra.
- A Tailscale or Headscale network. This mesh needs an account or a server, but it finds its way through NATs for you.

:::caution
Treat a mesh as experimental, and try it on servers that you can lose.

**This was run once, on EC2, in one region.** The test used two networks (VPCs) in the same region. The networks reached each other by public address only, and a Linux NAT was in front of one of the workers. WireGuard joined a worker behind that NAT in these cases:

- Only SSH was forwarded.
- The NAT randomized source ports.
- UDP 51820 was forwarded.
- UDP 51820 was dropped.

Tailscale ran against a self-hosted Headscale. In every case that should have worked, pods reached each other in both directions, and workers upgraded one at a time.

**Not run:** wide-area paths (the round trips were a few milliseconds), other clouds, the Tailscale service itself, a pool at a second provider, and every scenario in [How far to trust it](#how-far-to-trust-it).

The WireGuard mesh builds on the older multi-site mode. That mode has run on four servers at one provider over public addresses. It has not run across two providers.

**The [overlay check](#the-overlay-check) was added after that run. It ran one time, on EC2 (2026-10-29), in one region: WireGuard with a worker behind a Linux NAT, and Tailscale against Headscale.** It named every blocked UDP 51820 and every blocked tailnet rule that it was tried on. It gave no false alarm during a 35-minute soak. But it also gave two kinds of false report, and it reported later than this page said. Three fixes were made after that run, and only fakes tested them. **Not run:** many nodes, paths between two workers, large packets, wide-area paths, a worker whose `ufw` was already active when Geass joined it, and the Tailscale service itself.
:::

## Which mesh

| | WireGuard (default) | Tailscale |
| --- | --- | --- |
| What carries the traffic | The K3s `wireguard-native` flannel backend, in the kernel | Tailscale (or a Headscale server), as K3s documents it with `vpn-auth` |
| Runs besides the cluster | Nothing | A Tailscale account or a Headscale server, and the Tailscale client on every node |
| Ports | UDP 51820 between nodes, and TCP 6443 to the control plane | None for the cluster: nodes open connections outward. The access rules of the tailnet must allow the traffic of the cluster |
| Node behind a NAT | Joins when it is the node that opens the tunnel and keeps it open. K3s sends a keepalive every 25 seconds. The node talks to nodes that have an address that the others can reach. Two nodes that are both behind a NAT, with nothing forwarded, do not reach each other. One forwarded UDP port at one of the two nodes is enough | Tailscale finds a path between any two nodes, through its relay servers when it has to |
| What a node advertises | An address that you give it: its public address, or the address of its NAT gateway | Its address on the tailnet, which it learns when it joins |
| What it asks of you | A kernel with WireGuard (Linux 5.6 or newer), and UDP 51820 open at every site | A reusable auth key, access rules that allow the pod network, and the K3s integration. K3s itself marks this integration experimental |
| Encryption | WireGuard | WireGuard, by Tailscale |

The WireGuard row for NAT was run on EC2 against a Linux NAT. See [How far to trust it](#how-far-to-trust-it). These were the results:

- A worker behind the NAT joined in less than 20 seconds, and stayed joined through ten idle minutes.
- The worker was reachable when the NAT randomized its source ports. The control plane learned the mapped port from the first packet.
- A NAT that forgets its mappings costs the node up to about 25 seconds of lost pod traffic, until its next keepalive. If you forward UDP 51820 to the node, this loss stops. The forward changes nothing else about the join.
- Two workers that were both behind a NAT, with nothing forwarded, never reached each other. Each worker reached the control plane. One forwarded port at one of the workers was enough.

Another make of NAT, a consumer router, or the NAT of a carrier might behave differently.

The Tailscale row was run against Headscale with its own relay. A node behind the NAT reached every other node. The node connected directly where it could, and through the relay where both ends were behind a NAT. After the NAT forgot its mappings, the node used the relay for a while.

Use WireGuard when every site can have one node with a reachable address, or can forward UDP 51820. Use Tailscale when nodes are behind NATs that you do not control, or when two of those nodes must reach each other.

## What a mesh asks of every node

- **The control plane stays at one site.** The first server is the control plane. Other control plane servers must be at the same site as the first server. If they are not, the etcd traffic crosses the internet. The installer refuses `GEASS_MESH` on a server that joins a cluster.
- **Geass joins a worker over SSH.** The operator runs on the control plane. It opens SSH to `spec.eip` and `spec.sshPort` of the worker. Geass cannot join a machine that has no way in. For such a machine, forward an SSH port from its NAT gateway, or [join it by hand](#join-a-machine-by-hand).
- **Every node reaches the control plane's TCP 6443**, from wherever it is. On a tailnet, this means over the tailnet.
- **The usual requirements** of a [server](/docs/getting-started/requirements/).

## Install the control plane

### With WireGuard

On the first server, give the installer the address that the other sites send traffic to. This address is the public address of the server. Or it is the public address of the NAT gateway of the server, with UDP 51820 and TCP 6443 forwarded to the server.

K3s advertises its API server on that address. The pods on the control plane call the API server on it. These pods are the operator, DNS, and metrics. So **the machine has to be able to reach itself on it.** On EC2, an Elastic IP is not on any interface of the instance. Also, EC2 does not send the traffic of an instance to its own public address back to the instance. The install then stops at "wait for operator ready". The operator is in `CrashLoopBackOff`, with `i/o timeout` against `https://10.43.0.1:443`.

Put the address on the loopback interface first. The plain command is lost at reboot, and Geass does not put the address back. So make the address part of the network configuration of the machine:

```bash
sudo ip addr add 203.0.113.10/32 dev lo
```

The installer warns when the address is on no interface of the machine, and continues. Tailscale is not affected.

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | \
  sudo env GEASS_MESH=wireguard GEASS_NODE_EXTERNAL_IP=203.0.113.10 sh
```

This setting is the same as `GEASS_MULTI_SITE=1`. It also records `spec.network.mesh`, which turns on the checks below. The installer refuses an address that no other machine can use: loopback, unspecified, link-local, or multicast. The installer warns about a private address (`10.x.x.x`, `192.168.x.x`), because a private address means a different thing at each site.

These items are the same as in the [multi-provider mode](/docs/operations/multi-provider/#what-changes): the K3s configuration, the host firewall rules (51820/udp instead of 8472/udp), the join address, and the cluster object.

### With Tailscale or Headscale

1. Install Tailscale on the server, and bring the server onto your tailnet. Use a reusable auth key that is pre-approved, tagged, and has an expiry. In the admin console of Tailscale, the keys are under **Settings**, **Keys**. For Headscale, use a pre-authentication key and `--login-server`.

   ```bash
   curl -fsSL https://tailscale.com/install.sh | sh
   sudo tailscale up --auth-key=<key> --hostname=geass-control-plane
   tailscale ip -4
   ```

2. Put the same key in a file that only root can read:

   ```bash
   sudo install -m 600 /dev/null /root/tailnet-auth-key
   sudo sh -c 'cat > /root/tailnet-auth-key' <<'EOF'
   <key>
   EOF
   ```

3. Allow the traffic of the cluster in the access rules of the tailnet. The documentation of K3s asks for two things:

   - The pod network (`10.42.0.0/16` unless you changed it), listed under `autoApprovers` for the routes.
   - When access rules are on, a rule that lets the nodes and the pod network reach each other.

   Use your own tag. Geass cannot read your rules, so Geass cannot tell you when they are wrong.
4. Run the installer:

   ```bash
   curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | \
     sudo env GEASS_MESH=tailscale GEASS_TAILSCALE_AUTH_KEY_FILE=/root/tailnet-auth-key sh
   ```

   For a Headscale server, add `GEASS_TAILSCALE_CONTROL_URL=https://headscale.example.com`.

| Variable | Meaning |
| --- | --- |
| `GEASS_MESH` | `wireguard` or `tailscale`. Any other value stops the installer before it changes anything. |
| `GEASS_NODE_EXTERNAL_IP` | WireGuard: required. The address that other sites use to reach this server. Tailscale: optional. The default is what `tailscale ip -4` prints. |
| `GEASS_TAILSCALE_AUTH_KEY_FILE` | Tailscale: required. The file that holds the auth key. The installer checks that the key has 8 to 256 letters, digits, dashes, and underscores. The installer never logs or prints the key. |
| `GEASS_TAILSCALE_CONTROL_URL` | Tailscale: optional. An `https` address of a Headscale server. |

The installer refuses `GEASS_MESH=tailscale` together with `GEASS_MULTI_SITE`, because they are two different meshes. For Tailscale, the installer writes these settings into `/etc/rancher/k3s/config.yaml`:

- `vpn-auth-file`: a file that holds the key, and that only root can read.
- `node-external-ip`: the tailnet address.
- `tls-san`.

The installer writes no flannel setting, as the documentation of K3s shows. The installer stores the key in the Secret `geass-mesh-auth` in `geass-system`, so that the operator can join workers. The installer also sets `spec.serverURL` to `https://<tailnet address>:6443`.

## The cluster object

The installer writes this object. You can read it. Edit it only to rotate the Secret of the key, or to add a mesh to a cluster that was built with `GEASS_MULTI_SITE`.

```yaml
spec:
  serverURL: https://100.64.0.7:6443
  network:
    mesh:
      provider: Tailscale          # WireGuard (the default) or Tailscale
      tailscale:
        authKeySecretRef: geass-mesh-auth    # a Secret in geass-system, with the key authKey
        controlServerURL: https://headscale.example.com    # optional: Headscale
```

| Field | Meaning |
| --- | --- |
| `spec.network.mesh` | If you leave it out, nothing changes. `spec.network.multiSite` alone is the same WireGuard mesh that it always was, joined and left alone as before. A cluster that sets neither field keeps one private network. If you set `spec.network.mesh`, it turns on the checks below. A mesh makes `multiSite` redundant. |
| `spec.network.mesh.provider` | `WireGuard` (the default) or `Tailscale`. |
| `spec.network.mesh.tailscale.authKeySecretRef` | Required with `Tailscale`. Refused with `WireGuard`. The name of a Secret in `geass-system` that holds the auth key under `authKey`. |
| `spec.network.mesh.tailscale.controlServerURL` | An `https` address, with no user name, query, fragment, comma, or space. Empty means the Tailscale service itself. |

The API server refuses these mesh settings:

- `tailscale` without the Tailscale provider, or the Tailscale provider without `tailscale`.
- An unknown provider.
- An empty or malformed Secret name.
- A control server address that is not plain `https`.

Decide on the mesh when you install. The mesh describes how the control plane was built, and Geass does not rebuild the control plane. To use a mesh on a cluster that you installed without one, reinstall the cluster.

## Join workers

Add a worker in the same way as any other worker. See [Add servers](/docs/getting-started/add-servers/). Set `spec.eip` to the address that Geass opens SSH to. In a WireGuard mesh, `eip` is also the address that the node advertises. So `eip` must be an IP address that the other nodes can send UDP 51820 to. It is one of these addresses:

- The public address of the node.
- The public address of the NAT gateway that the node is behind. Forward SSH to the node. If you want other nodes to open the tunnel to the node, also forward UDP 51820.

Geass does these steps on the machine, in sequence:

| Step | WireGuard | Tailscale |
| --- | --- | --- |
| Install the storage prerequisites (iSCSI) when it can | yes | yes |
| Install Tailscale if it is missing, and bring the node onto the tailnet with the auth key | no | yes. Stops with "did not get an IPv4 address on the tailnet" when the node does not get an address |
| Check that `GET <serverURL>/ping` answers `pong` | yes | yes, over the tailnet |
| Check that the kernel can make a WireGuard interface, and open 51820/udp and 10250/tcp in `ufw` or `firewalld` | yes | no |
| Write `/etc/rancher/k3s/config.yaml` | `node-external-ip: <eip>`, labels, taints, kubelet log limits | `vpn-auth-file`, `node-external-ip: <the node's tailnet address>`, labels, taints, kubelet log limits |
| Install and start the K3s agent at the version of the control plane | yes | yes |

Geass writes the auth key to `/etc/rancher/k3s/vpn-auth`, with mode 600. The key is in the Secret `geass-mesh-auth`. While the setup step runs, the key is on the command line of that step, as the join token already is. Other users of the worker can see the key there. **It is also in the worker's `sudo` log** (`/var/log/auth.log` on Ubuntu, which the group `adm` can read). The key appears one time in that log, in the logged command of the join step, next to the join token.

On the run on EC2, the key was not in these places:

- The objects and events of the cluster.
- The log of the operator, and the logs of the installer.
- `config.yaml`.
- The K3s and Tailscale journals.
- The process list.

Use a key that expires, is tagged, and can be revoked. When the nodes are in, revoke the key. Geass removes the key from every error that comes out of a setup step.

A **pool** creates machines at a cloud, and joins them in the same way. For a Tailscale mesh, the firewall that Geass makes at the cloud opens no UDP 51820. The rule that Geass adds to the firewall of the control plane (`controlPlaneFirewall`) is only the TCP 6443 rule. The machines still need a public address for SSH, because the operator joins them over SSH.

### Join a machine by hand

To join a machine that the operator cannot reach over SSH, run the same commands on the machine as root. Geass does not manage the machine after that. The machine has no `GeassNode`, so Geass does not upgrade it, provision it again, or remove it. To remove the machine, use `kubectl delete node` and `/usr/local/bin/k3s-agent-uninstall.sh`. On the control plane, get the join token from `/var/lib/rancher/k3s/server/node-token`, and get the K3s release from `k3s --version`.

With WireGuard:

```bash
sudo ip link add geass-wgcheck type wireguard && sudo ip link del geass-wgcheck   # the kernel can do WireGuard
sudo mkdir -p /etc/rancher/k3s
sudo tee /etc/rancher/k3s/config.yaml >/dev/null <<'EOF'
node-external-ip: 198.51.100.7
node-label:
- topology.kubernetes.io/region=eu
kubelet-arg:
- container-log-max-size=50Mi
- container-log-max-files=5
EOF
curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION=v1.36.5+k3s1 INSTALL_K3S_EXEC=agent \
  K3S_URL=https://203.0.113.10:6443 K3S_TOKEN=<token> K3S_NODE_NAME=edge-1 sh -
```

`node-external-ip` is the address that other nodes send UDP 51820 to. Do not write `flannel-external-ip` on a worker. A K3s agent refuses this setting, and the server passes that setting on.

With Tailscale:

```bash
curl -fsSL https://tailscale.com/install.sh | sh
sudo tailscale up --auth-key=<key> --hostname=edge-1 --timeout=60s     # add --login-server=<url> for Headscale
sudo mkdir -p /etc/rancher/k3s
( umask 077 && printf '%s' 'name=tailscale,joinKey=<key>' | sudo tee /etc/rancher/k3s/vpn-auth >/dev/null )
sudo sh -c 'cat > /etc/rancher/k3s/config.yaml' <<'EOF'
vpn-auth-file: /etc/rancher/k3s/vpn-auth
kubelet-arg:
- container-log-max-size=50Mi
- container-log-max-files=5
EOF
echo "node-external-ip: $(tailscale ip -4 | head -n 1)" | sudo tee -a /etc/rancher/k3s/config.yaml
curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION=v1.36.5+k3s1 INSTALL_K3S_EXEC=agent \
  K3S_URL=https://100.64.0.7:6443 K3S_TOKEN=<token> K3S_NODE_NAME=edge-1 sh -
```

For Headscale, add `,controlServerURL=<url>` to the `vpn-auth` line.

## What Geass refuses and what it reports

Geass checks a mesh in three places, and **only for a cluster that sets `spec.network.mesh`**. Geass joins, monitors, and leaves alone a cluster that sets only `multiSite`, exactly as it did before the mesh field existed.

**Refused before a worker is touched.** The node goes to `Failed`, and its `status.message` says why. Geass did nothing to the machine. Fix the cause, and annotate the node with `geass.dev/retry=true`.

| Refused | Why |
| --- | --- |
| Mesh settings that cannot be used | A provider that is not WireGuard or Tailscale, Tailscale without its settings, or an unusable control server address |
| Tailscale: the Secret is missing, has no `authKey`, or the key is not 8 to 256 letters, digits, dashes and underscores | The worker could not join the tailnet. The message never repeats the key |
| WireGuard: `spec.eip` is empty, is not an IP address, or is loopback, unspecified, link-local or multicast | The node advertises this address, and K3s needs an IP address that another machine can use |
| The control plane was built without the mesh | The control plane runs the VXLAN overlay (the flannel backend annotation on its node is not `wireguard`), or it advertises no external address. A worker that joined now would register with an address that nobody can reach, and would be isolated. Reinstall the control plane with `GEASS_MESH` |

**Reported in the `MeshReady` condition**. This condition never counts against the `Ready` of the cluster. Geass looks at the condition again every minute while it is `False`, and every five minutes otherwise. Geass looks sooner when the [overlay check](#the-overlay-check) has a node due.

```bash
kubectl -n geass-system get geasscluster default -o jsonpath='{.status.conditions[?(@.type=="MeshReady")]}{"\n"}'
```

| Reason | What it says |
| --- | --- |
| `MeshConfigured` (`True`) | Every node advertises an external address. Geass reads this address from the nodes. The message also says what the [overlay check](#the-overlay-check) did. Either a probe from the pod of the operator got an answer from the pod network of N of M other nodes, or the message says why the probe did not run. The probe is one path from one point. It is not a test that traffic flows between sites. |
| `InvalidMeshSettings` | The mesh settings cannot be used. The list is the same as above |
| `AuthKeyUnusable` | Tailscale: the Secret or the key in it cannot be used |
| `ControlPlaneNotOnMesh` | The control plane was built without the mesh, as above |
| `OverlayUnreachable` | The [overlay check](#the-overlay-check) got no answer from the pod network of the nodes that the message names, on two probes in a row. The condition is `False` for this reason only while nothing above applies. It is a probe from one point, and it can be wrong |
| `MeshNeedsAttention` | `spec.serverURL` is a private address (`10.x`, `172.16-31.x`, `192.168.x`), which means a different thing at each site. Or, for WireGuard, `spec.serverURL` is an address in `100.64.0.0/10`. Or nodes advertise no external address, for example a worker joined by hand without `node-external-ip`, or a node from before the mesh was on. Or nodes advertise an address that no other machine can reach. Or a WireGuard node advertises a private address. Another site reaches such a node only after the node opened the tunnel itself |

A private `serverURL` is a warning and not a refusal. Sites that a private link joins are a real arrangement, and the reachability check of the worker says whether the link works.

**Not checked.** Geass does not test these items. A mesh that gets them wrong looks healthy to Kubernetes, because a node reports `Ready` over the API connection, whatever its pod network does. The [overlay check](#the-overlay-check) below looks at one path, from one point, and says exactly that:

- **UDP 51820 between two nodes.** The join checks only TCP 6443. A closed UDP port leaves nodes `Ready`, while pods on them cannot reach pods on other nodes. The overlay check can notice this problem between the control plane and a node after about five to six minutes, as no answer from the pod network of that node. The check does not look at the port itself, and it does not look between two workers.
- **NAT behavior.** Whether a NAT keeps the mapping that a WireGuard tunnel needs, and which of two NATs can reach the other. Geass does not find two workers that are both behind a NAT, when each worker reaches the control plane but not the other worker. The check never probes from one worker to another.
- **The path's MTU.** Geass does not read or set the MTU. See the MTU section below. The overlay check sends one small connection request, not a full-size packet.
- **The tailnet's access rules and route approval.** Rules that stop the control plane from reaching the pod range of a node show up as no answer from that node. Rules that stop only some other pair of nodes do not show up.
- **A service, DNS, or a pod's own network.** The probe stops at the bridge address of the node.

Check these items by hand with [Verify the mesh](#verify-the-mesh).

### The overlay check

A run on EC2 blocked UDP 51820. The node, the `GeassNode`, the cluster, and `MeshReady` all stayed green, while pods lost every packet. The overlay check is the attempt of the operator to see that case. **Unit tests were written for it (a fake connection that answers, stays silent, has no route or hangs, and a real API server for the condition). Then it ran one time on EC2. See [what the run found](#what-the-run-on-ec2-found).** Read the result of the check as a hint from one place.

| | |
| --- | --- |
| **What it asks** | Whether the pod network of another node answers. The operator opens a TCP connection to port 9 on the first address after the network address of the IPv4 pod range of the node. For example, `10.42.3.1` for `10.42.3.0/24`. On K3s, that address is the `cni0` bridge of the node: the node that answers on the pod network. The kernel sends the connection by the route to that pod range. So the connection follows the route that the mesh put there: the flannel WireGuard interface in a WireGuard mesh, or `tailscale0` in a Tailscale mesh. Nothing listens on port 9, so the usual reply is a refusal. A refusal and an accepted connection both count as an answer, because each one means that a packet crossed the overlay and another packet came back. Four seconds of silence, or the "no route to host" of the machine itself, is no answer |
| **From where** | One place: the pod of the operator. The installer pins the operator to a control plane node. So the check is the pod network of the control plane that reaches each node. To find its node, the operator looks for its own address in the pod ranges of the nodes (`spec.podCIDR`). If the address is in no range, nothing is probed, and the `MeshConfigured` message says why. This happens when the operator runs outside the cluster, or on the host network of a node |
| **Which nodes** | Every other node that meets all of these conditions: it is `Ready`, it was `Ready` for two minutes, it has an IPv4 pod range, and it runs at least one pod with a network of its own (a `Running` pod that is not on the host network). The check goes from the control plane to each node, never from one worker to another. The probed address is the bridge of the node, which K3s makes when the first such pod starts. The message names each node that the check left out, with the reason: not `Ready`, `Ready` for less than two minutes, no pod on the pod network yet, or no IPv4 pod range. Geass never probes a worker that takes no ordinary pod. This is a worker with a `storage: None` taint, which keeps the K3s `svclb` pod off it, or a worker that runs only host-network pods. Geass cannot see the bridge of such a worker from the API, and a probe of it gave "no route to host" or silence while the tunnel was healthy. Check such a worker by hand. The operator reads the pods from the cache that it already keeps, with the access that it already has |
| **When** | While `MeshReady` would otherwise be `True`. Geass reports an address problem or a control plane without the mesh first, and probes nothing until that problem is fixed. Geass probes a node for the first time when the node has been `Ready` for two minutes. Geass looks at the cluster again at that moment, so the first probe comes about two minutes after `Ready`. The EC2 run found the first probe 5:00 to 5:13 after `Ready`, and 6:00 after `Ready` for a worker with no pod, because nothing looked at the cluster before the five-minute look. That timing was changed after the run, and only a fake clock has tested the change. Geass probes a node that answered again after five minutes. After a silence, Geass probes after 30 seconds, then after one, two, and four minutes, and then every five minutes. Geass reports a node only after two probes in a row got no answer. So Geass names a node that stops answering after about five to six minutes. On EC2, this took 4:35 to 5:22 after a block, and 5:35 after `Ready` for a worker that joined with UDP already dropped. By the schedule, the worst case is about 5:40. When a node answers again, Geass clears the report within five minutes (9 seconds to 4:56 on EC2). While a node runs no pod on the pod network, Geass looks at the cluster again every two minutes, and probes the node after a pod runs there |
| **How much** | One connection for each node, at most 10 at a time, and at most 20 nodes and 15 seconds in a pass. Geass probes a larger cluster over several passes. Geass probes a node only when it is due, however often the cluster is reconciled |
| **What it says** | `MeshReady` is `False` with the reason `OverlayUnreachable`. The message names the nodes, says that it is a probe from the pod of one node, and says what was tested. The message also says what to check: UDP 51820 between the sites and the mapping of a NAT (WireGuard), or the access rules and approved routes of the tailnet (Tailscale). It also names a firewall on the node that drops traffic from the pod range to the node itself, with the command `ufw allow from 10.42.0.0/16`. That range is the default pod range of K3s, so use your own range. When nodes answer, `MeshConfigured` says how many nodes answered, and that this is one path from one point |
| **What it never does** | Refuse or fail a join, hold back `Ready`, install or run anything on a node (it does not use SSH), or write anything except `MeshReady`. Geass does not probe a cluster that sets no `spec.network.mesh`, and gives it no condition, as before |
| **Turn it off** | `kubectl -n geass-system annotate geasscluster default geass.dev/mesh-overlay-check=off`. `MeshConfigured` then says that the probe did not run. To turn the check on again, remove the annotation |

**An answer is not proof, and silence is not proof either.** The probe tests one path, from the control plane, with a small packet. It says nothing about a pair of workers, a full-size packet (see [MTU](#mtu)), DNS, or any service. Geass can report a node when its pod network is fine.

**A host firewall that denies incoming traffic by default causes a false report.** With `ufw` on a worker, the input policy drops the connection of the probe from the pod range to the node itself, but pods still reach each other. This happens on WireGuard and on Tailscale. On EC2, Geass named the node after 5:18, with 0% loss between pods before and after. Then `sudo ufw allow from 10.42.0.0/16` on the worker made the node answer, and the report cleared within a minute. That range is the default pod range of K3s, so use your own range.

The check can also be wrong for these reasons, which nobody has seen on a machine:

- A CNI other than the flannel of K3s might not put the bridge on the first address.
- A network policy or a firewall around the pod of the operator could stop its connections.

A node with no pod on the pod network has no bridge address. Before a fix, Geass reported such a node, with a message that sent the reader to UDP 51820. Now Geass leaves out the node and names it. See "Which nodes" in the table.

Before you change a firewall, confirm a report between two pods with [Verify the mesh](#verify-the-mesh).

### What the run on EC2 found

The check ran on 2026-10-29, for about an hour and forty minutes, on seven small instances in two unpeered networks of one region. The software was K3s 1.36, Ubuntu 24.04, Headscale 0.29, and Tailscale 1.104. The run used two clusters:

- A WireGuard cluster with a public worker, and a worker behind a Linux NAT with only SSH forwarded.
- A Tailscale cluster against Headscale, with a public worker and a worker behind the NAT.

The pod of the operator runs as non-root, with all capabilities dropped, a read-only root file system, and the default seccomp profile. It could open the connection. On the wire, the refusal came back in less than a millisecond.

- **It found what it was built for.** Geass named every dropped UDP 51820 (at the NAT, and on the control plane) and every Headscale rule that stopped allowing the pod range. It named each one 4:35 to 5:22 after the block, and 5:35 after `Ready` for a worker that joined with UDP already dropped. Each report cleared after the fix, in 9 seconds to 4:56. Both nodes stayed `Ready` all the time.
- **It gave no false alarm during a 35-minute soak** of a healthy WireGuard mesh with two workers. The check probed each worker eight times, and every probe got an answer.
- **A host firewall gave a false report**, as described above: `ufw` with a default deny on a worker, while the tunnel was healthy and pods reached each other.
- **A worker with no pod on the pod network gave a false report with the wrong remedy.** Geass named it with "no route to host" on WireGuard and "timed out" on Tailscale, while the tunnel was healthy: a `ping` across the pod range answered in about a millisecond. The message sent the reader to UDP 51820. The first ordinary pod gave the node its bridge, and the report cleared within two minutes. This problem shows only on a worker that takes no ordinary pod: a worker with a `storage: None` taint, which keeps the K3s `svclb` pod off it, or a worker with only host-network pods.
- **Geass did not say that a node was not `Ready`.** When `tailscaled` stopped on a worker, the node became `NotReady` in about a minute. `MeshReady` stayed `True` with "no other node to probe from", and said nothing about a node that was down. `OverlayUnreachable` never appeared, because Geass does not probe a node that is not `Ready`.
- **It reported later than this page said.** The first probe of a node came 5:00 to 5:13 after `Ready` (seven of seven times), and 6:00 after `Ready` for a worker with no pod, not after about two minutes.

After the run, these changes were made:

- The first probe follows the end of the two minutes.
- Geass leaves out a node with no pod on the pod network, and names it.
- For a cluster with no node to probe from, the message says which nodes are not `Ready`.
- For an unreachable node, the message names the `ufw` rule.

**Only fakes tested these changes. They have not run on a machine.** A real run of the changes has to show these results:

- A first probe about two minutes after `Ready`.
- No report for a worker with no pod, and a probe of that worker within a few minutes of its first pod.
- The `ufw` text in the message.

**Not run:** many nodes (batches of more than 20), a path between two workers or two workers behind NATs, large packets, a wide-area path, a worker whose `ufw` was already active when Geass joined it (in the run, `ufw` was turned on after the join, with the two rules that Geass opens), a `NotReady` WireGuard worker, a network policy or egress rule around the pod of the operator, a soak longer than 35 minutes, a restart of the operator, and the Tailscale service itself.

## Verify the mesh

Run these checks after the first worker joins, and again for a worker behind a NAT.

1. **Every node is ready, with an address.** `EXTERNAL-IP` is the address that each node advertises.

   ```bash
   kubectl get nodes -o wide
   ```

2. **Pods on two sites reach each other.** Pin a pod to each of two nodes at different sites, and ping across. The [overlay check](#the-overlay-check) approximates this test from one point. Replace the node names:

   ```bash
   kubectl run net-a --image=busybox:1.36 --restart=Never --overrides='{"spec":{"nodeName":"cp"}}' -- sleep 600
   kubectl run net-b --image=busybox:1.36 --restart=Never --overrides='{"spec":{"nodeName":"edge-1"}}' -- sleep 600
   kubectl wait --for=condition=Ready pod/net-a pod/net-b --timeout=120s
   kubectl exec net-a -- ping -c 3 -W 3 "$(kubectl get pod net-b -o jsonpath='{.status.podIP}')"
   kubectl exec net-b -- ping -c 3 -W 3 "$(kubectl get pod net-a -o jsonpath='{.status.podIP}')"
   ```

   Test both directions. With a NAT, one direction can work and the other can fail.
3. **A full-size packet gets through.** On a node, find the MTU of the pod network. Send a packet of that size, with the don't-fragment bit set, to a pod on another site. Use the pod IPs from step 2:

   ```bash
   kubectl exec net-a -- cat /sys/class/net/eth0/mtu            # the pod MTU, for example 1420
   ping -M do -s <MTU minus 28> -c 3 <pod IP at the other site>   # run on a node
   kubectl delete pod net-a net-b                                  # when you are done
   ```

   "Message too long" or silence at that size, while small packets work, is an MTU problem.
4. **The tunnel has a recent handshake** (WireGuard). If `wireguard-tools` is installed, run `sudo wg show` on a node. The command lists the flannel WireGuard interface, with one peer for each other node and a "latest handshake" within the last few minutes. For Tailscale, `tailscale status` lists the other nodes. `tailscale ping <node>` says whether the path is direct or through a relay.
5. **Container metrics come from a worker.** After a minute or two, open the **Metrics** tab of a service. In both meshes, Prometheus scrapes each kubelet on its external address. So an empty chart for a node means that TCP 10250 does not reach the node.

## MTU

Geass does not set the MTU of the pod network. flannel works out the MTU from the network interface of the node, and subtracts the overhead of the tunnel. These are the results measured on EC2:

- Over WireGuard, an instance interface of 9001 bytes gave a pod MTU of 8921. A full 8921-byte packet with the don't-fragment bit set got through between sites. Public paths inside one region carry jumbo frames.
- Over WireGuard, with the interface at 1500, the pod MTU was 1420. The largest packet that passed had 1392 bytes of payload.
- Over Tailscale, the pod MTU was 1280 (largest payload 1252), whatever the interface.

In that one region, nodes whose interfaces have different MTUs did not cause a black hole. Pings up to 8000 bytes passed. A wide-area path was not tried. A path that drops fragments and ICMP messages makes large packets disappear while small packets work. Step 3 above finds this problem. If you have this problem, lower the MTU of the network interface that carries the tunnel. Then flannel derives a smaller MTU.

## Limits

- **One control plane site.** If that site is down, the dashboard and the operator are down. See [High availability](/docs/operations/high-availability/).
- **Cross-site traffic crosses the internet**. So calls between pods at different sites pay the latency and the bandwidth. The documentation of K3s warns that a high latency affects the health of the cluster. Give projects a [region](/docs/guides/scaling-and-placement/), so that their traffic stays in one place.
- **Host charts cover one node over WireGuard.** Node exporters publish on private addresses that other sites cannot reach. Container metrics come from every node. Over Tailscale, Prometheus scraped the node exporters and kubelets of every node on their tailnet addresses, including a node behind a NAT.
- **A mesh is not added to a running cluster**, and is not removed from one. Reinstall the cluster.
- **The Tailscale integration is K3s's, and K3s calls it experimental.** Geass writes what the documentation of K3s shows, and adds nothing to it. Geass does not set the flannel backend, advertise routes, or accept routes for you. K3s decides what it runs over the tailnet. On the run on EC2, K3s did these things:
  - It started flannel with the `tailscale` backend.
  - It ran `tailscale set --accept-routes --advertise-routes=<the node's pod range>`. The Headscale policy approved the routes.
  - It routed the pod ranges directly into `tailscale0`, with no VXLAN.
  - It overrode `node-external-ip` with the tailnet address.
- **Two nodes that are both behind a NAT** need Tailscale, or a forwarded UDP port at one of them, to reach each other over WireGuard. This was run on EC2 with Linux NATs. With nothing forwarded, the nodes did not reach each other. With one port forwarded, they did.

## Troubleshooting

**A node is `Failed` with "the mesh cannot take a worker".** The rest of the message is the reason. Either the control plane was built without the mesh, or its address is not on the mesh. Reinstall the control plane with `GEASS_MESH`.

**A node is `Failed` with "the mesh cannot take this node".** Its `spec.eip` is not an IP address that other machines can use. Set `spec.eip` to such an address.

**A node is `Failed` with "the Secret geass-system/geass-mesh-auth ... does not exist" or "has no authKey key".** Create the Secret with the key under `authKey`: `kubectl -n geass-system create secret generic geass-mesh-auth --from-file=authKey=./key`. Then annotate the node with `geass.dev/retry=true`.

**A node is `Failed` with "did not get an IPv4 address on the tailnet".** The key was rejected, or the node waits for approval. A key is rejected when it is expired, used up, or not for this control server. Check the admin console of Tailscale or Headscale, and read `sudo tailscale status` on the node.

**A node is `Failed` with "cannot reach the cluster API at https://... from this node".** The message says why. Over WireGuard, check TCP 6443 and UDP 51820 at both sites. On a tailnet, check that `spec.serverURL` is the tailnet address of the control plane. Also check that the access rules let the node reach TCP 6443 on that address.

**A node is `Ready` but its pods cannot reach pods elsewhere.** One of these problems is the cause:

- UDP 51820 is blocked.
- A NAT does not keep the mapping of the tunnel.
- The MTU is too large for the path.
- The rules or the route approval of the tailnet are missing.

**A blocked UDP 51820 looks exactly like this.** On the run on EC2, the node, the `GeassNode`, the cluster, and `MeshReady` all stayed green. Pods lost every packet, and pod DNS timed out. But `kubectl exec` and `kubectl logs` continued to work, because K3s uses its own connection for these commands. The [overlay check](#the-overlay-check) was added later to notice this problem between the control plane and a node. Look at `MeshReady`, which might be `False` with `OverlayUnreachable` after about five to six minutes. The check ran one time on EC2, and it named every blocked UDP 51820 that it was tried on. But it ran in one region only, and it cannot see a path between two workers. So do not rely on it to report the problem. A `True` with an answer from a node is one path from one point. No report does not mean that pods reach each other. Work through [Verify the mesh](#verify-the-mesh) between that node and another node.

**`MeshReady` is `False` with `OverlayUnreachable`.** The probe of the operator got no answer from the pod network of the nodes that the message names, on two probes in a row. Do these steps:

1. Between two pods, one on the control plane and one on a node that the message names, run steps 2 and 3 of [Verify the mesh](#verify-the-mesh).
2. If the pods do not reach each other, check UDP 51820 in both directions between the sites and the NAT (WireGuard), or the access rules and route approval of the tailnet (Tailscale).
3. If the pods reach each other, the probe is wrong for that node. See "An answer is not proof" under [The overlay check](#the-overlay-check). First, look for a host firewall on the node that denies incoming traffic by default, such as `ufw`. Allow the pod range with `sudo ufw allow from 10.42.0.0/16`, which made a node answer on EC2. That range is the default pod range of K3s, so use your own range.
4. If the cause is something else, turn off the check with `geass.dev/mesh-overlay-check=off`.

The report clears automatically within five minutes after the nodes answer (9 seconds to 4:56 on EC2).

**`MeshReady` is `True` and says "The overlay probe did not run".** The rest of the sentence gives the reason: the operator is not in the pod range of a node, the check was turned off with `geass.dev/mesh-overlay-check=off`, or there is no other node to probe. In the last case, the message lists the nodes that the check left out, and why. A node that is "not Ready" is down for Kubernetes, so look at `kubectl get nodes`. On EC2, when `tailscaled` stopped on a worker, its node became `NotReady` in about a minute. Geass does not probe a node that is `NotReady`, so you see this message, not `OverlayUnreachable`. A blocked UDP 51820 does not make a node `NotReady`: the node stays `Ready`.

**`MeshReady` is `True` and says that a node is "not probed" because no pod is running on the pod network.** The probe connects to the bridge address of the node. K3s makes that address when the first pod with a network of its own starts on the node, so a node with no such pod has no address to answer. A worker usually gets a pod within seconds, for example the K3s `svclb` pod. So Geass looks at the cluster again every two minutes, and probes the node after a pod runs there. Geass never probes a worker that takes no ordinary pod: a worker with a `storage: None` taint, or with only host-network pods. Check such a worker by hand with [Verify the mesh](#verify-the-mesh), and start a pod on it.

**`MeshReady` is `False` with `OverlayUnreachable`.** The probe of the operator got no answer from the pod network of the nodes that the message names, on two probes in a row. Run steps 2 and 3 of [Verify the mesh](#verify-the-mesh) between two pods: one pod on the control plane, and one pod on one of those nodes.

- If the pods reach each other, the probe is wrong for that node. Turn off the check with `geass.dev/mesh-overlay-check=off`. For more information, see "An answer is not proof" under [The overlay check](#the-overlay-check).
- If the pods do not reach each other, check UDP 51820 in both directions between the sites, and check the NAT (WireGuard). Or check the access rules and route approval of the tailnet (Tailscale).

The condition clears automatically within five minutes after the nodes answer.

**`MeshReady` is `True` and says "The overlay probe did not run".** The rest of the sentence says why:

- The operator is not on the pod range of a node.
- No other node is `Ready` with an IPv4 pod range yet.
- The check was turned off with `geass.dev/mesh-overlay-check=off`.

**`MeshReady` is still `True` after the auth key was removed.** Geass looks at the condition again every five minutes while it is `True`. When the Secret was deleted, the next join failed immediately. The condition changed to `False` with `AuthKeyUnusable` about three and a half minutes later.

**`MeshReady` is `False` with "nodes that advertise no external address".** A node joined without `node-external-ip`, or joined before the mesh was on. Join the node again, as described earlier on this page.

**The installer stops with "this machine has no address on the tailnet".** Before you run the installer, install Tailscale on the server, and bring it up (`sudo tailscale up --auth-key=...`). Or set `GEASS_NODE_EXTERNAL_IP` to the tailnet address of the server.

**The installer stops with "GEASS_TAILSCALE_AUTH_KEY_FILE ... letters, digits".** The file holds something that is not a key, for example a quote, a space, or a second line. Put only the key in the file.

## How far to trust it

| Run | Not run |
| --- | --- |
| Unit tests of the settings that Geass writes for each mesh, the validation, the address classification, and the checks of nodes. Tests against a real API server: that the CRD refuses the settings above and fills in the default, that a cluster without the field is stored as it was given, and that `MeshReady` is written. **For the overlay check, unit tests and one real-API-server test with a fake connection and a fake clock** that answers, stays silent, has no route, or hangs. These tests cover which node the check probes from and which node it targets, the two-probes rule, the schedule and its bounds, the look at the end of the two minutes of a node, which nodes the check leaves out and what it says about them (not `Ready`, no pod on the pod network), that a join and `Ready` are not changed, that a cluster without a mesh is not probed, and that the connection is classified as designed. One connection to the loopback address of the test machine checks that the dialer is connected. Tests that a cluster that does not use the field gets byte for byte what it got before: the K3s settings, the worker join commands, the cluster object, and the monitoring values. The Tailscale commands of the worker run on a machine of this project, with a stand-in for `tailscale` | |
| **The overlay check on EC2, once** (2026-10-29: seven small instances in one region, WireGuard with a worker behind a Linux NAT, and Tailscale against Headscale). The pod of the operator opened the connection. Geass named every blocked UDP 51820 and every blocked tailnet rule in 4:35 to 5:35, and cleared each report in 9 seconds to 4:56 after the fix. No false alarm during 35 minutes of a healthy WireGuard mesh with two workers. Two false reports: a default-deny `ufw`, and a worker with no pod on the pod network. A `NotReady` node was not named as `NotReady`. The first probe came 5:00 to 5:13 after `Ready` | The fixes made after that run: the first probe at the end of the two minutes, nodes with no pod left out, and the `NotReady` and `ufw` text. Only fakes have tested them |
| **On EC2, once**: seven small instances in two unpeered networks of one region, a Linux NAT in front of one worker, and Headscale with its own relay, on K3s 1.35 to 1.37. WireGuard joined a worker in four cases: with only SSH forwarded, with the NAT randomizing source ports, with UDP 51820 forwarded, and with UDP 51820 dropped. Pods had 0% loss in the first three cases. In the last case, pods had 100% loss, and Geass reported nothing. Two workers both behind a NAT, with nothing forwarded and with one port forwarded. Tailscale against Headscale: every pair of nodes reached the others. Joins took 16 to 23 seconds over WireGuard, and 33 to 42 seconds over Tailscale. Workers were upgraded one at a time on both meshes, with the K3s configuration unchanged. The refusals gave the messages on this page: an unusable address, a wrong or missing auth key, and a control plane built without the mesh. For the control plane built without the mesh, nothing was installed on the machine. Container and host metrics from a worker behind a NAT over Tailscale | Wide-area conditions: latency, loss, the MTU of a path, carrier-grade NAT, consumer routers, a NAT that forgets mappings in less than 25 seconds, and IPv6. Any NAT except the Linux NAT, and any cloud except AWS. The Tailscale service itself (only Headscale was run), key expiry and tags there, MagicDNS, and a Headscale policy without route approval. A pool at a second provider, the server form of the dashboard, a machine joined by hand, a public ingress on a mesh cluster, Longhorn or volumes across the mesh, logging, a reboot, a key rotation, and the loss of the control plane. Host metrics from a WireGuard node behind a NAT. Only a TCP check was done. **The overlay check beyond that run:** many nodes, a path between two workers, large packets, a wide-area path, a worker whose `ufw` was already active when Geass joined it, a `NotReady` WireGuard worker, a network policy around the pod of the operator, a soak longer than 35 minutes, and the Tailscale service itself. See [what the run on EC2 found](#what-the-run-on-ec2-found). The warning of the installer about an address on no interface. A unit test covers this warning, but the run used a build from before the warning. The Tailscale integration of K3s is still an experimental feature of K3s itself |

The scripts and findings of the runs are kept with the notes of the project. The one thing that the first run found is a closed UDP port, which `MeshReady` could not see. The [overlay check](#the-overlay-check) was written to see this problem from the control plane, and it saw the problem in the second run, with the false reports and the late first probe described there. So use the check as a hint, and check by hand with [Verify the mesh](#verify-the-mesh).

## Related

- [Nodes at more than one provider](/docs/operations/multi-provider/)
- [Add servers](/docs/getting-started/add-servers/)
- [Node pools and autoscaling](/docs/guides/node-pools/)
- [GeassCluster](/docs/reference/resources/geass-cluster/)
- [Installer variables](/docs/reference/configuration/)
- [Troubleshooting](/docs/operations/troubleshooting/)
