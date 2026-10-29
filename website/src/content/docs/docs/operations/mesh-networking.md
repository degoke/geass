---
title: "Mesh networking"
description: "Join servers at several sites, some behind a NAT with no public address, into one cluster over WireGuard or Tailscale. What Geass sets up, refuses and checks."
---

A cluster normally keeps its nodes on one private network. A **mesh** joins nodes that are at different sites, and may be behind a NAT with no public address, into one cluster network. You choose it when you install the control plane, and record it on the cluster with `spec.network.mesh`. Geass supports two meshes: K3s's own WireGuard overlay (the default, nothing extra to run) and a Tailscale or Headscale network (an account or a server, but it finds its way through NATs for you).

:::caution
**This was run once, on EC2, in one region.** Two networks (VPCs) in the same region, reaching each other by public address only, with a Linux NAT in front of one of the workers. WireGuard joined a worker that sat behind that NAT with only SSH forwarded, with the NAT randomising source ports, and with UDP 51820 forwarded and dropped. Tailscale ran against a self-hosted Headscale. Pods reached each other both ways in every case that should have worked, and workers upgraded one at a time. **Not run:** wide-area paths (the round trips were a few milliseconds), other clouds, Tailscale's own service, a pool at a second provider, and every scenario in [How far to trust it](#how-far-to-trust-it). The older multi-site mode, which the WireGuard mesh builds on, has run on four servers at one provider over public addresses; it has not run across two providers. Treat a mesh as experimental and try it on servers you can lose. **The [overlay check](#the-overlay-check) was added after that run and has since been run once, on EC2 (2026-10-29), in one region: WireGuard with a worker behind a Linux NAT, and Tailscale against Headscale. It named every blocked UDP 51820 and every blocked tailnet rule it was tried on, and gave no false alarm over a 35 minute soak, but it also gave two kinds of false report and spoke later than this page said. Three fixes were made after that run and were tested with fakes only. Not run: many nodes, paths between two workers, large packets, wide-area paths, a worker whose `ufw` was already active when Geass joined it, and Tailscale's own service.**
:::

## Which mesh

| | WireGuard (default) | Tailscale |
| --- | --- | --- |
| What carries the traffic | The K3s `wireguard-native` flannel backend, in the kernel | Tailscale (or a Headscale server), as K3s documents it with `vpn-auth` |
| Runs besides the cluster | Nothing | A Tailscale account or a Headscale server, and the Tailscale client on every node |
| Ports | UDP 51820 between nodes, and TCP 6443 to the control plane | None for the cluster: nodes open connections outward. Tailnet access rules must allow the cluster's traffic |
| Node behind a NAT | Joins when it is the one that opens the tunnel, keeps it open (K3s sends a keepalive every 25 seconds), and talks to nodes that have an address the others can reach. Two nodes that are both behind a NAT, with nothing forwarded, do not reach each other; one forwarded UDP port at one of them is enough | Tailscale finds a path between any two nodes, through its relay servers when it has to |
| What a node advertises | An address you give it: its public address, or its NAT gateway's | Its address on the tailnet, which it learns when it joins |
| What it asks of you | A kernel with WireGuard (Linux 5.6 or newer), UDP 51820 open at every site | A reusable auth key, access rules that allow the pod network, and the K3s integration, which K3s itself marks experimental |
| Encryption | WireGuard | WireGuard, by Tailscale |

The WireGuard row for NAT was run on EC2 against a Linux NAT (see [How far to trust it](#how-far-to-trust-it)): a worker behind it joined in under 20 seconds, stayed joined through ten idle minutes, and was reached when the NAT randomised its source ports (the control plane learned the mapped port from the first packet). A NAT that forgets its mappings costs the node up to about 25 seconds of lost pod traffic, until its next keepalive; forwarding UDP 51820 to the node removes that, and it changes nothing else about joining. Two workers that were both behind a NAT, with nothing forwarded, never reached each other (each reached the control plane), and one forwarded port at one of them was enough. Another make of NAT, a consumer router or a carrier's NAT may behave differently. The Tailscale row was run against Headscale with its own relay: a node behind the NAT reached every other node, directly where it could and through the relay where both ends were behind a NAT, and fell back to the relay for a while after the NAT forgot its mappings.

Pick WireGuard when every site can have one node with a reachable address, or can forward UDP 51820. Pick Tailscale when nodes are behind NATs you do not control, or two of them must reach each other.

## What a mesh asks of every node

- **The control plane stays at one site.** The first server is the control plane. Other control plane servers must be at the same site as the first: etcd's traffic would cross the internet otherwise. `GEASS_MESH` is refused on a server that joins a cluster.
- **Geass joins a worker over SSH.** The operator, which runs on the control plane, opens SSH to the worker's `spec.eip` and `spec.sshPort`. A machine with no way in cannot be joined by Geass: forward an SSH port from its NAT gateway, or [join it by hand](#join-a-machine-by-hand).
- **Every node reaches the control plane's TCP 6443**, from wherever it is. On a tailnet that means over the tailnet.
- **The usual requirements** of a [server](/docs/getting-started/requirements/).

## Install the control plane

### With WireGuard

On the first server, give the installer the address the other sites send traffic to: its public address, or its NAT gateway's public address with UDP 51820 and TCP 6443 forwarded to it.

K3s advertises its API server on that address, and the pods on the control plane (the operator, DNS, metrics) call it. So **the machine has to be able to reach itself on it.** On EC2 an Elastic IP is not on any interface of the instance, and EC2 does not send an instance's traffic to its own public address back: the install stops at "wait for operator ready" with the operator in `CrashLoopBackOff` and `i/o timeout` against `https://10.43.0.1:443`. Put the address on the loopback interface first. The plain command is lost at reboot, and Geass does not put it back, so make it part of the machine's own network configuration:

```bash
sudo ip addr add 203.0.113.10/32 dev lo
```

The installer warns when the address is on no interface of the machine, and goes on. Tailscale is not affected.

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | \
  sudo env GEASS_MESH=wireguard GEASS_NODE_EXTERNAL_IP=203.0.113.10 sh
```

This is `GEASS_MULTI_SITE=1` and it also records `spec.network.mesh`, which turns on the checks below. The installer refuses an address no other machine can use (loopback, unspecified, link-local, multicast) and warns about a private one (`10.x.x.x`, `192.168.x.x`), because a private address means one thing at each site. The K3s configuration, the host firewall rules (51820/udp instead of 8472/udp), the join address and the cluster object are those of the [multi-provider mode](/docs/operations/multi-provider/#what-changes).

### With Tailscale or Headscale

1. Install Tailscale on the server and bring it onto your tailnet, with a reusable auth key (in Tailscale's admin console under **Settings**, **Keys**) that is pre-approved, tagged and has an expiry. For Headscale, use a pre-authentication key and `--login-server`.

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

3. Allow the cluster's traffic in the tailnet's access rules. K3s's documentation asks for the pod network (`10.42.0.0/16` unless you changed it) to be listed under `autoApprovers` for the routes, and, when access rules are on, for a rule that lets the nodes and the pod network reach each other. Use your own tag. Geass cannot read your rules, so it cannot tell you when they are wrong.
4. Run the installer:

   ```bash
   curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | \
     sudo env GEASS_MESH=tailscale GEASS_TAILSCALE_AUTH_KEY_FILE=/root/tailnet-auth-key sh
   ```

   Add `GEASS_TAILSCALE_CONTROL_URL=https://headscale.example.com` for a Headscale server.

| Variable | Meaning |
| --- | --- |
| `GEASS_MESH` | `wireguard` or `tailscale`. Anything else stops the installer before it changes anything. |
| `GEASS_NODE_EXTERNAL_IP` | WireGuard: required, the address other sites reach this server on. Tailscale: optional, defaults to what `tailscale ip -4` prints. |
| `GEASS_TAILSCALE_AUTH_KEY_FILE` | Tailscale: required. The file that holds the auth key. Checked: 8 to 256 letters, digits, dashes and underscores. The key is never logged or printed. |
| `GEASS_TAILSCALE_CONTROL_URL` | Tailscale: optional. An `https` address of a Headscale server. |

`GEASS_MESH=tailscale` together with `GEASS_MULTI_SITE` is refused: they are two different meshes. For Tailscale the installer writes `vpn-auth-file` (a file only root can read, holding the key), `node-external-ip` (the tailnet address) and `tls-san` into `/etc/rancher/k3s/config.yaml`, and no flannel setting, which is what K3s's documentation shows. It stores the key in the Secret `geass-mesh-auth` in `geass-system`, so that the operator can join workers, and sets `spec.serverURL` to `https://<tailnet address>:6443`.

## The cluster object

The installer writes this. You can read it, and you edit it only to rotate the key's Secret or to add a mesh to a cluster that was built with `GEASS_MULTI_SITE`.

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
| `spec.network.mesh` | Left out, nothing changes: `spec.network.multiSite` alone is the same WireGuard mesh it always was, joined and left alone as before, and a cluster that sets neither keeps one private network. Set, it turns on the checks below. A mesh makes `multiSite` redundant. |
| `spec.network.mesh.provider` | `WireGuard` (the default) or `Tailscale`. |
| `spec.network.mesh.tailscale.authKeySecretRef` | Required with `Tailscale`, refused with `WireGuard`. The name of a Secret in `geass-system` that holds the auth key under `authKey`. |
| `spec.network.mesh.tailscale.controlServerURL` | An `https` address, with no user name, query, fragment, comma or space. Empty means Tailscale's own service. |

The API server refuses a mesh that sets `tailscale` without the Tailscale provider or the other way round, an unknown provider, an empty or malformed Secret name and a control server address that is not plain `https`. Choose it when you install: it describes how the control plane was built, and Geass does not rebuild that. To use a mesh on a cluster installed without one, reinstall it.

## Join workers

Add a worker as you add any other ([Add servers](/docs/getting-started/add-servers/)), with `spec.eip` set to the address Geass opens SSH to. In a WireGuard mesh `eip` is also the address the node advertises, so it must be an IP address that the other nodes can send UDP 51820 to: its public address, or the public address of the NAT gateway it is behind, with SSH (and, if you want other nodes to open the tunnel to it, UDP 51820) forwarded to it.

What Geass does to the machine, in order:

| Step | WireGuard | Tailscale |
| --- | --- | --- |
| Install the storage prerequisites (iSCSI) when it can | yes | yes |
| Install Tailscale if it is missing, and bring the node onto the tailnet with the auth key | no | yes. Stops with "did not get an IPv4 address on the tailnet" when it does not get one |
| Check `GET <serverURL>/ping` answers `pong` | yes | yes, over the tailnet |
| Check the kernel can make a WireGuard interface, and open 51820/udp and 10250/tcp in `ufw` or `firewalld` | yes | no |
| Write `/etc/rancher/k3s/config.yaml` | `node-external-ip: <eip>`, labels, taints, kubelet log limits | `vpn-auth-file`, `node-external-ip: <the node's tailnet address>`, labels, taints, kubelet log limits |
| Install and start the K3s agent at the control plane's version | yes | yes |

The auth key is written to `/etc/rancher/k3s/vpn-auth`, mode 600. It is in the Secret `geass-mesh-auth`, and it is on the command line of the setup step while that runs (as the join token already is), where other users of the worker can see it. **It is also in the worker's `sudo` log** (`/var/log/auth.log` on Ubuntu, which the group `adm` can read), once, in the logged command of the join step, next to the join token. On the run on EC2 the key was not found in the cluster's objects and events, the operator's log, the installer's logs, `config.yaml`, the K3s and Tailscale journals or the process list. Use a key that expires, is tagged and can be revoked, and revoke it when the nodes are in. Every error that comes out of a setup step has the key taken out of it.

A **pool** creates machines at a cloud and joins them the same way. For a Tailscale mesh the firewall Geass makes at the cloud opens no UDP 51820, and the rule Geass adds to the control plane's firewall (`controlPlaneFirewall`) is the TCP 6443 rule only. The machines still need a public address for SSH, because the operator joins them over it.

### Join a machine by hand

A machine that the operator cannot reach over SSH is joined with the same commands, run on the machine as root. Geass does not manage it afterwards: it has no `GeassNode`, so it is not upgraded, re-provisioned or removed by Geass, and you remove it with `kubectl delete node` and `/usr/local/bin/k3s-agent-uninstall.sh`. Take the join token from `/var/lib/rancher/k3s/server/node-token` and the K3s release from `k3s --version` on the control plane.

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

`node-external-ip` is the address other nodes send UDP 51820 to. Do not write `flannel-external-ip` on a worker: a K3s agent refuses it, and the server passes that setting on.

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

Add `,controlServerURL=<url>` to the `vpn-auth` line for Headscale.

## What Geass refuses and what it reports

Geass checks a mesh in three places, and **only for a cluster that sets `spec.network.mesh`**. A cluster that sets only `multiSite` is joined, monitored and left alone exactly as it was before the mesh field existed.

**Refused before a worker is touched.** The node goes to `Failed`, its `status.message` says why, and nothing was done to the machine. Fix the cause and annotate the node with `geass.dev/retry=true`.

| Refused | Why |
| --- | --- |
| Mesh settings that cannot be used | A provider that is not WireGuard or Tailscale, Tailscale without its settings, an unusable control server address |
| Tailscale: the Secret is missing, has no `authKey`, or the key is not 8 to 256 letters, digits, dashes and underscores | The worker could not join the tailnet. The message never repeats the key |
| WireGuard: `spec.eip` is empty, is not an IP address, or is loopback, unspecified, link-local or multicast | It is what the node advertises, and K3s needs an IP address that another machine can use |
| The control plane was built without the mesh | It runs the VXLAN overlay (flannel's backend annotation on its node is not `wireguard`), or it advertises no external address. A worker joined now would register with an address nobody can reach and be isolated. Reinstall the control plane with `GEASS_MESH` |

**Reported in the `MeshReady` condition**, which is never held against the cluster's `Ready`, and looked at again every minute while it is `False` and every five minutes otherwise (sooner when the [overlay check](#the-overlay-check) has a node due).

```bash
kubectl -n geass-system get geasscluster default -o jsonpath='{.status.conditions[?(@.type=="MeshReady")]}{"\n"}'
```

| Reason | What it says |
| --- | --- |
| `MeshConfigured` (`True`) | Every node advertises an external address, which is read from the nodes. The message also says what the [overlay check](#the-overlay-check) did: that a probe from the operator's pod got an answer from the pod network of N of M other nodes (one path from one point, not a test that traffic flows between sites), or why the probe did not run. |
| `InvalidMeshSettings` | The mesh settings cannot be used (the same list as above) |
| `AuthKeyUnusable` | Tailscale: the Secret or the key in it cannot be used |
| `ControlPlaneNotOnMesh` | The control plane was built without the mesh, as above |
| `OverlayUnreachable` | The [overlay check](#the-overlay-check) got no answer from the pod network of the nodes the message names, on two probes in a row. It is `False` only while nothing above applies. It is a probe from one point and can be wrong |
| `MeshNeedsAttention` | `spec.serverURL` is a private address (`10.x`, `172.16-31.x`, `192.168.x`), which means one thing at each site, or, for WireGuard, an address in `100.64.0.0/10`. Or nodes advertise no external address (a worker joined by hand without `node-external-ip`, or one from before the mesh was on). Or nodes advertise an address no other machine can reach. Or a WireGuard node advertises a private address, which is reached from another site only after it has opened the tunnel itself |

A private `serverURL` is a warning and not a refusal, because sites joined by a private link are a real arrangement, and the worker's own reachability check says whether it works.

**Not checked.** Geass does not test these, and a mesh that gets them wrong looks healthy to Kubernetes, because a node reports `Ready` over the API connection whatever its pod network does. The [overlay check](#the-overlay-check) below looks at one path, from one point, and says exactly that:

- **UDP 51820 between two nodes.** The join checks only TCP 6443. A closed UDP port leaves nodes `Ready` while pods on them cannot reach pods elsewhere. The overlay check can notice it between the control plane and a node, after about five to six minutes, as no answer from that node's pod network. It does not look at the port itself, and it does not look between two workers.
- **NAT behaviour.** Whether a NAT keeps the mapping a WireGuard tunnel needs, and which of two NATs can reach the other. Two workers that are both behind a NAT, each reaching the control plane and not each other, are not found: the check never probes from one worker to another.
- **The path's MTU.** Geass neither reads nor sets it (see below), and the overlay check sends one small connection request, not a full-size packet.
- **The tailnet's access rules and route approval.** Rules that stop the control plane from reaching a node's pod range show up as no answer from that node. Rules that stop only some other pair do not.
- **A service, DNS, or a pod's own network.** The probe stops at the node's bridge address.

Check these by hand with [Verify the mesh](#verify-the-mesh).

### The overlay check

A run on EC2 found that with UDP 51820 blocked the node, the `GeassNode`, the cluster and `MeshReady` all stayed green while pods lost every packet. The overlay check is the operator's attempt to see that case. **It was written with unit tests (a fake connection that answers, stays silent, has no route or hangs, and a real API server for the condition), and then run once on EC2 (see [what the run found](#what-the-run-on-ec2-found)).** Read what it says as a hint from one place.

| | |
| --- | --- |
| **What it asks** | Whether another node's pod network answers. The operator opens a TCP connection to port 9 on the first address after the network address of the node's IPv4 pod range (`10.42.3.1` for `10.42.3.0/24`; on K3s that is the node's `cni0` bridge, the node answering on the pod network). The kernel sends it by the route to that pod range, so it follows whatever the mesh put there: the flannel WireGuard interface in a WireGuard mesh, `tailscale0` in a Tailscale mesh. Nothing listens on port 9, so the normal reply is a refusal. A refusal and an accepted connection both count as an answer, because either means a packet crossed the overlay and another came back. Four seconds of silence, or the machine's own "no route to host", is no answer |
| **From where** | One place: the operator's own pod. The installer pins the operator to a control plane node, so this is the control plane's pod network reaching each node. The operator finds its node by looking for its own address in the nodes' pod ranges (`spec.podCIDR`). If it is in none (the operator runs outside the cluster, or on a node's host network) nothing is probed, and the `MeshConfigured` message says why |
| **Which nodes** | Every other node that is `Ready`, has been for two minutes, has an IPv4 pod range and runs at least one pod with a network of its own (a `Running` pod that is not on the host network): the control plane to each node, never one worker to another. The address probed is the node's bridge, which K3s makes when the first such pod starts. A node left out is named in the message with the reason: not `Ready`, `Ready` for less than two minutes, no pod on the pod network yet, or no IPv4 pod range. A worker that takes no ordinary pod (a `storage: None` taint keeps K3s's `svclb` pod off it, or it runs only host-network pods) is never probed: Geass cannot see its bridge from the API, and probing it gave "no route to host" or silence with a healthy tunnel. Check it by hand. The pods are read from the cache the operator already keeps, with the access it already has |
| **When** | While `MeshReady` would otherwise be `True`: an address problem or a control plane without the mesh is reported first, and nothing is probed until it is fixed. A node is first probed when it has been `Ready` for two minutes: the cluster is looked at again at that moment, so the first probe should come about two minutes after `Ready`. (The EC2 run found it at 5:00 to 5:13 after `Ready`, and at 6:00 for a worker with no pod, because nothing looked at the cluster before the five-minute look. That was changed afterwards and has been tested with a fake clock only, not on a machine.) A node that answered is probed again after five minutes. After a silence: 30 seconds, then a minute, two, four, and every five minutes. A node is reported only after two probes in a row got no answer, so a node that stops answering is named after about five to six minutes (4:35 to 5:22 measured on EC2 after a block, 5:35 after `Ready` for a worker joined with UDP already dropped; about 5:40 is the worst case by the schedule), and one that answers again is cleared within five (9 seconds to 4:56 measured). While a node runs no pod on the pod network the cluster is looked at again every two minutes, and the node is probed once one runs |
| **How much** | One connection per node, at most 10 at a time, at most 20 nodes and 15 seconds in a pass. A larger cluster is probed over several passes. However often the cluster is reconciled, a node is probed only when it is due |
| **What it says** | `MeshReady` is `False` with the reason `OverlayUnreachable` and a message that names the nodes, says it is a probe from one node's pod, says what was tested, and says what to check: UDP 51820 between the sites and a NAT's mapping (WireGuard), or the tailnet's access rules and approved routes (Tailscale), and a firewall on the node that drops traffic from the pod range to the node itself (it names `ufw allow from 10.42.0.0/16`, K3s's default pod range; use yours). When nodes answer, `MeshConfigured` says how many, and that this is one path from one point |
| **What it never does** | Refuse or fail a join, hold back `Ready`, install or run anything on a node (it does not use SSH), or write anything but `MeshReady`. A cluster that sets no `spec.network.mesh` is not probed and gets no condition, as before |
| **Turn it off** | `kubectl -n geass-system annotate geasscluster default geass.dev/mesh-overlay-check=off`. `MeshConfigured` then says the probe did not run. Remove the annotation to turn it on |

**An answer is not proof, and silence is not proof either.** The probe tests one path, from the control plane, with a small packet. It says nothing about a pair of workers, a full-size packet (see [MTU](#mtu)), DNS or any service. A node can be reported when its pod network is fine. **A host firewall that denies incoming traffic by default does it:** with `ufw` on a worker, the probe's connection from the pod range to the node itself is dropped by the input policy while pods reach each other, on WireGuard and on Tailscale alike. On EC2 the node was named after 5:18 with 0% loss between pods before and after, and `sudo ufw allow from 10.42.0.0/16` on the worker (K3s's default pod range; use yours) made it answer and cleared the report within a minute. Other ways to be wrong, not seen on a machine: a CNI other than K3s's flannel may not put the bridge on the first address, and a network policy or firewall around the operator's pod could stop its connections. A node with no pod on the pod network has no bridge address and used to be reported for it, with a message that sent the reader to UDP 51820; it is now left out and named (see "Which nodes"). Confirm a report between two pods with [Verify the mesh](#verify-the-mesh) before you change a firewall.

### What the run on EC2 found

The check was run on 2026-10-29, for about an hour and forty minutes, on seven small instances in two unpeered networks of one region (K3s 1.36, Ubuntu 24.04, Headscale 0.29, Tailscale 1.104): a WireGuard cluster with a public worker and a worker behind a Linux NAT with only SSH forwarded, and a Tailscale cluster against Headscale with a public worker and one behind the NAT. The operator's pod, which runs as non-root with all capabilities dropped, a read-only root filesystem and the default seccomp profile, could open the connection, and on the wire the refusal came back in under a millisecond.

- **It found what it was built for.** Every dropped UDP 51820 (at the NAT, and on the control plane) and every Headscale rule that stopped allowing the pod range was named: 4:35 to 5:22 after the block, and 5:35 after `Ready` for a worker that joined with UDP already dropped. Each was cleared after the fix, in 9 seconds to 4:56. Both nodes stayed `Ready` throughout.
- **It gave no false alarm over a 35 minute soak** of a healthy WireGuard mesh with two workers (eight probes of each, every one answered).
- **A host firewall gave a false report** (above): `ufw` with a default deny on a worker, the tunnel healthy, pods reaching each other.
- **A worker with no pod on the pod network gave a false report** with the wrong remedy: it was named "no route to host" on WireGuard and "timed out" on Tailscale, with the tunnel healthy (a `ping` across the pod range answered in about a millisecond), and the message sent the reader to UDP 51820. The first ordinary pod gave the node its bridge and cleared it within two minutes. It shows only on a worker that takes no ordinary pod (a `storage: None` taint that keeps K3s's `svclb` pod off it, or host-network pods only).
- **A node that is not `Ready` was not said to be.** Stopping `tailscaled` on a worker turned the node `NotReady` in about a minute, and `MeshReady` stayed `True` with "no other node to probe from" and no word about a node being down. `OverlayUnreachable` never appeared: a node that is not `Ready` is not probed.
- **It spoke later than this page said.** The first probe of a node came 5:00 to 5:13 after `Ready` (seven of seven), and 6:00 for a worker with no pod, not after about two minutes.

After the run the first probe was made to follow the end of the two minutes, a node with no pod on the pod network is left out and named, the message for a cluster with no node to probe from says which nodes are not `Ready`, and the message for an unreachable node names the `ufw` rule. **Those changes were tested with fakes only and have not been run on a machine.** What a real run of them has to show: a first probe about two minutes after `Ready`; no report for a worker with no pod, and a probe of it within a couple of minutes of its first pod; the `ufw` text in the message.

**Not run:** many nodes (batching of more than 20); a path between two workers, or two workers behind NATs; large packets; a wide-area path; a worker whose `ufw` was already active when Geass joined it (it was enabled after the join, with the two rules Geass opens); a `NotReady` WireGuard worker; a network policy or egress rule around the operator's pod; a soak longer than 35 minutes; the operator restarting; Tailscale's own service.

## Verify the mesh

Run these after the first worker joins, and again for a worker behind a NAT.

1. **Every node is ready, with an address.** `EXTERNAL-IP` is what each node advertises.

   ```bash
   kubectl get nodes -o wide
   ```

2. **Pods on two sites reach each other.** Pin a pod to each of two nodes at different sites and ping across. This is the test the [overlay check](#the-overlay-check) approximates from one point. Replace the node names:

   ```bash
   kubectl run net-a --image=busybox:1.36 --restart=Never --overrides='{"spec":{"nodeName":"cp"}}' -- sleep 600
   kubectl run net-b --image=busybox:1.36 --restart=Never --overrides='{"spec":{"nodeName":"edge-1"}}' -- sleep 600
   kubectl wait --for=condition=Ready pod/net-a pod/net-b --timeout=120s
   kubectl exec net-a -- ping -c 3 -W 3 "$(kubectl get pod net-b -o jsonpath='{.status.podIP}')"
   kubectl exec net-b -- ping -c 3 -W 3 "$(kubectl get pod net-a -o jsonpath='{.status.podIP}')"
   ```

   Test both directions: with a NAT, one direction can work and the other not.
3. **A full-size packet gets through.** On a node, find the pod network's MTU and send a packet that size with the don't-fragment bit set to a pod on another site (use the pod IPs from step 2):

   ```bash
   kubectl exec net-a -- cat /sys/class/net/eth0/mtu            # the pod MTU, for example 1420
   ping -M do -s <MTU minus 28> -c 3 <pod IP at the other site>   # run on a node
   kubectl delete pod net-a net-b                                  # when you are done
   ```

   "Message too long" or silence at that size, with small packets working, is an MTU problem.
4. **The tunnel has a recent handshake** (WireGuard). With `wireguard-tools` installed, `sudo wg show` on a node lists the flannel WireGuard interface with one peer for each other node and a "latest handshake" within the last few minutes. For Tailscale, `tailscale status` lists the other nodes, and `tailscale ping <node>` says whether the path is direct or through a relay.
5. **Container metrics come from a worker.** Open a service's **Metrics** tab after a minute or two. Prometheus scrapes each kubelet on its external address in either mesh, so an empty chart for a node means TCP 10250 does not reach it.

## MTU

Geass does not set the pod network's MTU. flannel works it out from the node's network interface and takes the tunnel's overhead off it. Measured on EC2: over WireGuard an instance interface of 9001 bytes gave a pod MTU of 8921, and a full 8921-byte packet with the don't-fragment bit set got through between sites (public paths inside one region carry jumbo frames); with the interface at 1500 the pod MTU was 1420 and the largest packet that passed was 1392 bytes of payload. Over Tailscale the pod MTU was 1280 (largest payload 1252), whatever the interface. Nodes whose interfaces have different MTUs did not cause a black hole in that one region (pings up to 8000 bytes passed), but a wide-area path was not tried. A path that drops fragments and ICMP messages makes large packets vanish while small ones work, which is what step 3 above finds. If you hit it, lower the MTU of the network interface that carries the tunnel, so that flannel derives a smaller one.

## Limits

- **One control plane site.** If its site is down, the dashboard and the operator are down. See [High availability](/docs/operations/high-availability/).
- **Cross-site traffic crosses the internet**, so calls between pods at different sites pay the latency and the bandwidth. K3s's documentation warns that a high latency affects cluster health. Give projects a [region](/docs/guides/scaling-and-placement/) so their traffic stays in one place.
- **Host charts cover one node over WireGuard.** Node exporters publish on private addresses that other sites cannot reach. Container metrics come from every node. Over Tailscale the node exporters and kubelets of every node, including one behind a NAT, were scraped on their tailnet addresses.
- **A mesh is not added to a running cluster**, and not removed from one. Reinstall.
- **The Tailscale integration is K3s's, and K3s calls it experimental.** Geass writes what K3s's documentation shows and does not add to it: it does not set the flannel backend, advertise routes or accept routes for you. What K3s runs over the tailnet is K3s's decision. On the run on EC2 it started flannel with the `tailscale` backend, ran `tailscale set --accept-routes --advertise-routes=<the node's pod range>` (approved by the Headscale policy), routed the pod ranges straight into `tailscale0` (no VXLAN) and overrode `node-external-ip` with the tailnet address.
- **Two nodes that are both behind a NAT** need Tailscale, or a forwarded UDP port at one of them, to reach each other over WireGuard (run on EC2 with Linux NATs: with nothing forwarded they did not; with one port forwarded they did).

## Troubleshooting

**A node is `Failed` with "the mesh cannot take a worker".** The rest of the message is the reason: the control plane was built without the mesh, or its address is not on it. Reinstall the control plane with `GEASS_MESH`.

**A node is `Failed` with "the mesh cannot take this node".** Its `spec.eip` is not an IP address that other machines can use. Set it to one.

**A node is `Failed` with "the Secret geass-system/geass-mesh-auth ... does not exist" or "has no authKey key".** Create the Secret with the key under `authKey`: `kubectl -n geass-system create secret generic geass-mesh-auth --from-file=authKey=./key`. Then annotate the node with `geass.dev/retry=true`.

**A node is `Failed` with "did not get an IPv4 address on the tailnet".** The key was rejected (expired, used up, or not for this control server), or the node is waiting for approval. Check the Tailscale or Headscale admin console, and read `sudo tailscale status` on the node.

**A node is `Failed` with "cannot reach the cluster API at https://... from this node".** The message says why. Over WireGuard check TCP 6443 and UDP 51820 at both sites. On a tailnet check that `spec.serverURL` is the control plane's tailnet address and that the access rules let the node reach TCP 6443 on it.

**A node is `Ready` but its pods cannot reach pods elsewhere.** UDP 51820 is blocked, a NAT does not keep the tunnel's mapping, the MTU is too large for the path, or the tailnet's rules or route approval are missing. **A blocked UDP 51820 looks exactly like this.** On the run on EC2 the node, the `GeassNode`, the cluster and `MeshReady` all stayed green, pods lost every packet and pod DNS timed out, while `kubectl exec` and `kubectl logs` kept working (K3s uses its own connection for those). The [overlay check](#the-overlay-check) has since been added to notice it between the control plane and a node: look at `MeshReady`, which may be `False` with `OverlayUnreachable` after about five to six minutes. It was run once, on EC2, and named every blocked UDP 51820 it was tried on, but it was run in one region only and cannot see a path between two workers, so do not rely on it to speak: a `True` with an answer from a node is one path from one point, and no report does not mean pods reach each other. Work through [Verify the mesh](#verify-the-mesh) between that node and another.

**`MeshReady` is `False` with `OverlayUnreachable`.** The operator's probe got no answer from the pod network of the nodes the message names, on two probes in a row. Between two pods on the control plane and on one of those nodes, run steps 2 and 3 of [Verify the mesh](#verify-the-mesh). If pods do not reach each other, check UDP 51820 in both directions between the sites and the NAT (WireGuard), or the tailnet's access rules and route approval (Tailscale). If they do, the probe is wrong for that node (see "An answer is not proof" under [The overlay check](#the-overlay-check)): first look for a host firewall on the node that denies incoming traffic by default, such as `ufw`, and allow the pod range with `sudo ufw allow from 10.42.0.0/16` (K3s's default pod range; use yours), which is what made a node answer on EC2. If the cause is something else, turn the check off with `geass.dev/mesh-overlay-check=off`. The report clears by itself within five minutes of the nodes answering (9 seconds to 4:56 on EC2).

**`MeshReady` is `True` and says "The overlay probe did not run".** The rest of the sentence says why: the operator is not on a node's pod range, or the check was turned off with `geass.dev/mesh-overlay-check=off`, or there is no other node to probe, and it then lists the nodes it left out and for what. A node that is "not Ready" is down as far as Kubernetes is concerned: look at `kubectl get nodes`. Stopping `tailscaled` on a worker turned its node `NotReady` in about a minute on EC2, and a node that is `NotReady` is not probed, so this is what you see instead of `OverlayUnreachable`. (A blocked UDP 51820 does not do that: the node stays `Ready`.)

**`MeshReady` is `True` and says a node is "not probed" because no pod is running on the pod network.** The probe connects to the node's bridge address, which K3s makes when the first pod that has a network of its own starts there, and a node with no such pod has none to answer. A worker normally gets a pod within seconds (K3s's `svclb` pod is one), so the cluster is looked at again every two minutes and the node is probed once a pod runs. A worker that takes no ordinary pod (a `storage: None` taint, or only host-network pods) is not probed at all: check it by hand with [Verify the mesh](#verify-the-mesh), starting a pod on it.

**`MeshReady` is still `True` after the auth key was removed.** It is looked at again every five minutes while it is `True`: deleting the Secret made the next join fail at once, and the condition changed to `False` with `AuthKeyUnusable` about three and a half minutes later.

**`MeshReady` is `False` with "nodes that advertise no external address".** A node joined without `node-external-ip`, or from before the mesh was on. Rejoin it as above.

**The installer stops with "this machine has no address on the tailnet".** Install Tailscale on the server and bring it up (`sudo tailscale up --auth-key=...`) before you run the installer, or set `GEASS_NODE_EXTERNAL_IP` to its tailnet address.

**The installer stops with "GEASS_TAILSCALE_AUTH_KEY_FILE ... letters, digits".** The file holds something that is not a key: a quote, a space, a second line. Put only the key in it.

## How far to trust it

| Run | Not run |
| --- | --- |
| Unit tests of the settings written for each mesh, the validation, the address classification and the checks of nodes. Tests against a real API server that the CRD refuses the settings above and fills in the default, that a cluster without the field is stored as it was given, and that `MeshReady` is written. **For the overlay check, unit tests and one real-API-server test with a fake connection and a fake clock** that answers, stays silent, has no route or hangs: which node it probes from and which it targets, the two-probes rule, the schedule and its bounds, the look at the end of a node's two minutes, which nodes are left out and what is said of them (not `Ready`, no pod on the pod network), that a join and `Ready` are untouched, that a cluster without a mesh is not probed, and that the connection is classified as designed. One connection to this machine's own loopback address checks that the dialer is wired up. Tests that a cluster that does not use the field gets byte for byte what it got before: the K3s settings, the worker join commands, the cluster object and the monitoring values. The worker's Tailscale commands run on this project's machine with a stand-in for `tailscale` | |
| **The overlay check on EC2, once** (2026-10-29: seven small instances, one region; WireGuard with a worker behind a Linux NAT, and Tailscale against Headscale): the operator's pod opened the connection; every blocked UDP 51820 and every blocked tailnet rule was named, in 4:35 to 5:35, and cleared in 9 seconds to 4:56 of the fix; no false alarm over 35 minutes of a healthy WireGuard mesh with two workers; two false reports (a default-deny `ufw`, and a worker with no pod on the pod network); a `NotReady` node not named as such; a first probe 5:00 to 5:13 after `Ready` | The fixes made after it (the first probe at the end of the two minutes, nodes with no pod left out, the `NotReady` and `ufw` wording), which only fakes have seen |
| **On EC2, once** (seven small instances in two unpeered networks of one region, a Linux NAT in front of one worker, Headscale with its own relay; K3s 1.35 to 1.37): WireGuard joined a worker with only SSH forwarded, with the NAT randomising source ports, with UDP 51820 forwarded, and with it dropped (0% loss between pods in the first three, 100% in the last, with nothing reported by Geass). Two workers both behind a NAT, with nothing forwarded and with one port forwarded. Tailscale against Headscale: every pair of nodes reached the others. Joins took 16 to 23 seconds over WireGuard and 33 to 42 over Tailscale. Workers were upgraded one at a time on both, with the K3s configuration unchanged. The refusals (an unusable address, a wrong or missing auth key, a control plane built without the mesh) gave the messages on this page (for the control plane built without the mesh, with nothing installed on the machine). Container and host metrics from a worker behind a NAT over Tailscale | Wide-area conditions: latency, loss, a path's MTU, carrier-grade NAT, consumer routers, a NAT that forgets mappings in under 25 seconds, IPv6. Any NAT but Linux's, and any cloud but AWS. Tailscale's own service (only Headscale was run), key expiry and tags there, MagicDNS, and a Headscale policy without route approval. A pool at a second provider, the dashboard's server form, joining a machine by hand, a public ingress on a mesh cluster, Longhorn or volumes across the mesh, logging, a reboot, rotating a key, and the loss of the control plane. Host metrics from a WireGuard node behind a NAT (only a TCP check was done). **The overlay check beyond that run:** many nodes, a path between two workers, large packets, a wide-area path, a worker whose `ufw` was already active when Geass joined it, a `NotReady` WireGuard worker, a network policy around the operator's pod, a soak longer than 35 minutes, and Tailscale's own service (see [what the run on EC2 found](#what-the-run-on-ec2-found)). The installer's warning about an address on no interface (it is covered by a unit test; the run used a build from before it). K3s's Tailscale integration is still K3s's own experimental feature |

The runs' scripts and findings are kept with the project's notes. A closed UDP port, which `MeshReady` could not see, is the one thing the first run found. The [overlay check](#the-overlay-check) was written to see it from the control plane and saw it in the second run, with the false reports and the late first probe described there, so use it as a hint and check by hand with [Verify the mesh](#verify-the-mesh).

## Related

- [Nodes at more than one provider](/docs/operations/multi-provider/)
- [Add servers](/docs/getting-started/add-servers/)
- [Node pools and autoscaling](/docs/guides/node-pools/)
- [GeassCluster](/docs/reference/resources/geass-cluster/)
- [Installer variables](/docs/reference/configuration/)
- [Troubleshooting](/docs/operations/troubleshooting/)
