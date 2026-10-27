---
title: "Mesh networking"
description: "Join servers at several sites, some behind a NAT with no public address, into one cluster over WireGuard or Tailscale. What Geass sets up, refuses and checks."
---

A cluster normally keeps its nodes on one private network. A **mesh** joins nodes that are at different sites, and may be behind a NAT with no public address, into one cluster network. You choose it when you install the control plane, and record it on the cluster with `spec.network.mesh`. Geass supports two meshes: K3s's own WireGuard overlay (the default, nothing extra to run) and a Tailscale or Headscale network (an account or a server, but it finds its way through NATs for you).

:::caution
**This was not run on real machines.** The code that writes the K3s settings, joins workers, refuses a join that cannot work and reports the state of the mesh is covered by unit tests and tests against a real API server, and the shell commands it sends to a worker were run on this project's own machine with a stand-in for `tailscale`. **No node was joined to a real mesh, no node behind a NAT was tried, and Tailscale and Headscale were never started with it.** The older multi-site mode, which the WireGuard mesh builds on, has run on four servers at one provider over public addresses; it has not run across two providers. Until a test on real machines (see [How far to trust it](#how-far-to-trust-it)) says otherwise, treat a mesh as unproven and try it on servers you can lose.
:::

## Which mesh

| | WireGuard (default) | Tailscale |
| --- | --- | --- |
| What carries the traffic | The K3s `wireguard-native` flannel backend, in the kernel | Tailscale (or a Headscale server), as K3s documents it with `vpn-auth` |
| Runs besides the cluster | Nothing | A Tailscale account or a Headscale server, and the Tailscale client on every node |
| Ports | UDP 51820 between nodes, and TCP 6443 to the control plane | None for the cluster: nodes open connections outward. Tailnet access rules must allow the cluster's traffic |
| Node behind a NAT | Joins when it is the one that opens the tunnel, and talks to nodes that have an address the others can reach. Two nodes that are both behind a NAT, with nothing forwarded, are not expected to reach each other | Tailscale finds a path between any two nodes, through its relay servers when it has to |
| What a node advertises | An address you give it: its public address, or its NAT gateway's | Its address on the tailnet, which it learns when it joins |
| What it asks of you | A kernel with WireGuard (Linux 5.6 or newer), UDP 51820 open at every site | A reusable auth key, access rules that allow the pod network, and the K3s integration, which K3s itself marks experimental |
| Encryption | WireGuard | WireGuard, by Tailscale |

The WireGuard row for NAT comes from how WireGuard and K3s's backend are built (K3s configures a keepalive every 25 seconds, and a WireGuard peer learns the address a valid packet came from). It is a reading of those, not something this project has run. The Tailscale row is Tailscale's own description of what it does.

Pick WireGuard when every site can have one node with a reachable address, or can forward UDP 51820. Pick Tailscale when nodes are behind NATs you do not control, or two of them must reach each other.

## What a mesh asks of every node

- **The control plane stays at one site.** The first server is the control plane. Other control plane servers must be at the same site as the first: etcd's traffic would cross the internet otherwise. `GEASS_MESH` is refused on a server that joins a cluster.
- **Geass joins a worker over SSH.** The operator, which runs on the control plane, opens SSH to the worker's `spec.eip` and `spec.sshPort`. A machine with no way in cannot be joined by Geass: forward an SSH port from its NAT gateway, or [join it by hand](#join-a-machine-by-hand).
- **Every node reaches the control plane's TCP 6443**, from wherever it is. On a tailnet that means over the tailnet.
- **The usual requirements** of a [server](/docs/getting-started/requirements/).

## Install the control plane

### With WireGuard

On the first server, give the installer the address the other sites send traffic to: its public address, or its NAT gateway's public address with UDP 51820 and TCP 6443 forwarded to it.

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

The auth key is written to `/etc/rancher/k3s/vpn-auth`, mode 600, and appears nowhere else on the node. It is in the Secret `geass-mesh-auth`, and it is on the command line of the setup step while that runs (as the join token already is), where other users of the worker can see it. Use a key that expires, is tagged and can be revoked, and revoke it when the nodes are in. Every error that comes out of a setup step has the key taken out of it.

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

Geass checks a mesh in two places, and **only for a cluster that sets `spec.network.mesh`**. A cluster that sets only `multiSite` is joined, monitored and left alone exactly as it was before the mesh field existed.

**Refused before a worker is touched.** The node goes to `Failed`, its `status.message` says why, and nothing was done to the machine. Fix the cause and annotate the node with `geass.dev/retry=true`.

| Refused | Why |
| --- | --- |
| Mesh settings that cannot be used | A provider that is not WireGuard or Tailscale, Tailscale without its settings, an unusable control server address |
| Tailscale: the Secret is missing, has no `authKey`, or the key is not 8 to 256 letters, digits, dashes and underscores | The worker could not join the tailnet. The message never repeats the key |
| WireGuard: `spec.eip` is empty, is not an IP address, or is loopback, unspecified, link-local or multicast | It is what the node advertises, and K3s needs an IP address that another machine can use |
| The control plane was built without the mesh | It runs the VXLAN overlay (flannel's backend annotation on its node is not `wireguard`), or it advertises no external address. A worker joined now would register with an address nobody can reach and be isolated. Reinstall the control plane with `GEASS_MESH` |

**Reported in the `MeshReady` condition**, which is never held against the cluster's `Ready`, and looked at again every minute while it is `False` and every five minutes otherwise.

```bash
kubectl -n geass-system get geasscluster default -o jsonpath='{.status.conditions[?(@.type=="MeshReady")]}{"\n"}'
```

| Reason | What it says |
| --- | --- |
| `MeshConfigured` (`True`) | Every node advertises an external address. **This does not test that traffic flows between sites.** |
| `InvalidMeshSettings` | The mesh settings cannot be used (the same list as above) |
| `AuthKeyUnusable` | Tailscale: the Secret or the key in it cannot be used |
| `ControlPlaneNotOnMesh` | The control plane was built without the mesh, as above |
| `MeshNeedsAttention` | `spec.serverURL` is a private address (`10.x`, `172.16-31.x`, `192.168.x`), which means one thing at each site, or, for WireGuard, an address in `100.64.0.0/10`. Or nodes advertise no external address (a worker joined by hand without `node-external-ip`, or one from before the mesh was on). Or nodes advertise an address no other machine can reach. Or a WireGuard node advertises a private address, which is reached from another site only after it has opened the tunnel itself |

A private `serverURL` is a warning and not a refusal, because sites joined by a private link are a real arrangement, and the worker's own reachability check says whether it works.

**Not checked.** Geass does not test these, and a mesh that gets them wrong looks healthy to Kubernetes, because a node reports `Ready` over the API connection whatever its pod network does:

- **UDP 51820 between two nodes.** The join checks only TCP 6443. A closed UDP port leaves nodes `Ready` while pods on them cannot reach pods elsewhere.
- **NAT behaviour.** Whether a NAT keeps the mapping a WireGuard tunnel needs, and which of two NATs can reach the other.
- **The path's MTU.** Geass neither reads nor sets it (see below).
- **The tailnet's access rules and route approval.**

Check these by hand with [Verify the mesh](#verify-the-mesh).

## Verify the mesh

Run these after the first worker joins, and again for a worker behind a NAT.

1. **Every node is ready, with an address.** `EXTERNAL-IP` is what each node advertises.

   ```bash
   kubectl get nodes -o wide
   ```

2. **Pods on two sites reach each other.** Pin a pod to each of two nodes at different sites and ping across. Replace the node names:

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

Geass does not set the pod network's MTU. flannel works it out from the node's network interface and takes the tunnel's overhead off it: a WireGuard tunnel adds about 60 bytes over IPv4 (80 over IPv6), and Tailscale's interface, `tailscale0`, has an MTU of 1280 unless it is changed. So expect a pod MTU near 1420 over WireGuard on an ordinary 1500-byte link, and lower than that on a link that has less. These are expectations from how the tunnels are built, not something measured here. A path that drops fragments and ICMP messages makes large packets vanish while small ones work, which is what step 3 above finds. If you hit it, lower the MTU of the network interface that carries the tunnel, so that flannel derives a smaller one.

## Limits

- **One control plane site.** If its site is down, the dashboard and the operator are down. See [High availability](/docs/operations/high-availability/).
- **Cross-site traffic crosses the internet**, so calls between pods at different sites pay the latency and the bandwidth. K3s's documentation warns that a high latency affects cluster health. Give projects a [region](/docs/guides/scaling-and-placement/) so their traffic stays in one place.
- **Host charts cover one node.** Node exporters publish on private addresses that other sites cannot reach. Container metrics come from every node.
- **A mesh is not added to a running cluster**, and not removed from one. Reinstall.
- **The Tailscale integration is K3s's, and K3s calls it experimental.** Geass writes what K3s's documentation shows and does not add to it: it does not set the flannel backend, advertise routes or accept routes for you. What K3s runs over the tailnet is K3s's decision.
- **Two nodes that are both behind a NAT** are expected to need Tailscale (or a forwarded UDP port at one of them) to reach each other.

## Troubleshooting

**A node is `Failed` with "the mesh cannot take a worker".** The rest of the message is the reason: the control plane was built without the mesh, or its address is not on it. Reinstall the control plane with `GEASS_MESH`.

**A node is `Failed` with "the mesh cannot take this node".** Its `spec.eip` is not an IP address that other machines can use. Set it to one.

**A node is `Failed` with "the Secret geass-system/geass-mesh-auth ... does not exist" or "has no authKey key".** Create the Secret with the key under `authKey`: `kubectl -n geass-system create secret generic geass-mesh-auth --from-file=authKey=./key`. Then annotate the node with `geass.dev/retry=true`.

**A node is `Failed` with "did not get an IPv4 address on the tailnet".** The key was rejected (expired, used up, or not for this control server), or the node is waiting for approval. Check the Tailscale or Headscale admin console, and read `sudo tailscale status` on the node.

**A node is `Failed` with "cannot reach the cluster API at https://... from this node".** The message says why. Over WireGuard check TCP 6443 and UDP 51820 at both sites. On a tailnet check that `spec.serverURL` is the control plane's tailnet address and that the access rules let the node reach TCP 6443 on it.

**A node is `Ready` but its pods cannot reach pods elsewhere.** UDP 51820 is blocked, a NAT does not keep the tunnel's mapping, the MTU is too large for the path, or the tailnet's rules or route approval are missing. Work through [Verify the mesh](#verify-the-mesh) between that node and another.

**`MeshReady` is `False` with "nodes that advertise no external address".** A node joined without `node-external-ip`, or from before the mesh was on. Rejoin it as above.

**The installer stops with "this machine has no address on the tailnet".** Install Tailscale on the server and bring it up (`sudo tailscale up --auth-key=...`) before you run the installer, or set `GEASS_NODE_EXTERNAL_IP` to its tailnet address.

**The installer stops with "GEASS_TAILSCALE_AUTH_KEY_FILE ... letters, digits".** The file holds something that is not a key: a quote, a space, a second line. Put only the key in it.

## How far to trust it

| Run | Not run |
| --- | --- |
| Unit tests of the settings written for each mesh, the validation, the address classification and the checks of nodes. Tests against a real API server that the CRD refuses the settings above and fills in the default, that a cluster without the field is stored as it was given, and that `MeshReady` is written. Tests that a cluster that does not use the field gets byte for byte what it got before: the K3s settings, the worker join commands, the cluster object and the monitoring values. The worker's Tailscale commands run on this project's machine with a stand-in for `tailscale`: the key file's mode and content, that a node already on the tailnet is not brought up again, that a node with no address stops | Any real machine. A node behind a NAT. A real WireGuard tunnel between sites, and the K3s agent with the configuration written here. Tailscale or Headscale at all. The pod network's MTU. The time a join takes. What happens when UDP is blocked. A pool at a second provider |

The plan for the run on real machines (two sites, one behind a NAT) is kept with the project's notes. When it has been run, this page is changed to say what it found.

## Related

- [Nodes at more than one provider](/docs/operations/multi-provider/)
- [Add servers](/docs/getting-started/add-servers/)
- [Node pools and autoscaling](/docs/guides/node-pools/)
- [GeassCluster](/docs/reference/resources/geass-cluster/)
- [Installer variables](/docs/reference/configuration/)
- [Troubleshooting](/docs/operations/troubleshooting/)
