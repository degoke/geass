---
title: "Add servers"
description: "Add a server that you have over SSH, or have Geass create one at a cloud. Give it a region, a site, and tags, watch it start, and remove it."
---

A new install is one server that runs everything. To get more capacity, add more servers. Geass connects to each new
server over SSH, installs what the server needs, and adds it to the cluster. You can add two kinds of server:

- **A server that you have.** Any Linux server that Geass can reach over SSH.
- **A cloud machine.** Geass creates the machine at a cloud for you. It costs money until you remove the server.

For machines that come and go with the load, use a [pool](/docs/guides/node-pools/) instead. To make the cluster
survive the loss of one server, see
[High availability](/docs/operations/high-availability/#run-three-control-plane-servers).

## Before you start

You must be an admin. A cloud machine needs a [connection for machines](/docs/guides/connectors/clouds/), and Geass
opens some network paths for it. See [How a machine joins](/docs/guides/node-pools/#how-a-machine-joins).

A server that you have needs these items:

- A Linux server that meets the [requirements](/docs/getting-started/requirements/).
- A user that is `root`, or a user that can run `sudo` without a password.
- An SSH private key without a passphrase. Put its public key in `authorized_keys` of that user on the server. Use a
  key that you make only for Geass:

  ```bash
  ssh-keygen -t ed25519 -N "" -C geass-worker -f ./geass-worker
  ```

- These network paths, open in the firewall of your provider and on the new server. Geass does not open them:

| From | To | Port | Why |
| --- | --- | --- | --- |
| The first server | The new server | 22/tcp, or your SSH port | Geass installs and removes the server over SSH. |
| The new server | The first server | 6443/tcp | The new server joins the cluster. |
| Every server | Every other server | 8472/udp | The private network between servers. |
| The first server | The new server | 10250/tcp | Metrics of the services on the new server. |
| The new server | The internet | 443/tcp | Downloads of the software and the container images. |

For servers at different providers, see [Multi-provider clusters](/docs/operations/multi-provider/). Those clusters use
51820/udp instead of 8472/udp. For a server without a public address, see
[Mesh networking](/docs/operations/mesh-networking/).

## Add a server from the dashboard or the command line

1. In the left rail, open **Usage**.
2. In **Nodes**, click **Add server**.
3. Under **Where does the server come from?**, select **A server I have** or **A cloud machine**.
4. Complete the form, as the next lists tell you.
5. Click **Add server**. Geass creates nothing before this step.

For **A server I have**, enter these values:

- **Name**: lowercase letters, numbers, and hyphens. Geass uses it as the name of the server.
- **Address**: the address where Geass reaches the server over SSH.
- **SSH user**: `root`, or a user with passwordless `sudo`.
- **SSH port**: only if the port is not 22.
- **SSH private key**: the whole private key. The dashboard never shows the key again.
- **Host keys**: optional, but recommended. See
  [Pin the server's host key](#pin-the-servers-host-key-recommended).

For **A cloud machine**, select the **Cloud connection** and the **Machine size**. **Region** and **Image** are optional.
The form shows the price for an hour where the cloud gives one. If you leave **Name** empty, Geass makes a name.

:::caution
A cloud machine is billed to your account at the cloud until you remove the server. The creation of machines is
experimental. See [Cloud connections for machines](/docs/guides/connectors/clouds/#how-far-to-trust-it).
:::

Both kinds of server have these settings:

- **Data on this server**: `Allow` lets databases, buckets, and volumes run on the server. `None` keeps them off, so only
  services run there. The default is `Allow`.
- **Placement**: an optional region, site, and tags. See
  [Set a server's region, site, and tags](#set-a-servers-region-site-and-tags).

When you add the first server with `None`, every service restarts one time. The same happens when you create the first
pool. After that, services can run on servers that hold no data.

### From the command line

```bash
# A server that you have
geass servers add edge-1 --address 198.51.100.7 --ssh-user root \
  --ssh-key-file ~/.ssh/geass_ed25519 --known-hosts-file ./known_hosts --tag edge

# A cloud machine: list the sizes and prices first
geass sizes --connection hetzner --region fsn1 --min-vcpus 2 --min-memory 4 --limit 10
geass servers add --connection hetzner --region fsn1 --size cx23
```

To keep data off the server, add `--storage none`. See [`geass servers`](/docs/reference/cli/) for every flag.

### Pin the server's host key (recommended)

Without host keys, Geass does not check which machine answers at the address. Geass then sends the join secret to any
machine that answers. To pin the server, give Geass its host keys:

1. Get the host key of the server:

   ```bash
   ssh-keyscan -t ed25519 203.0.113.20 > known_hosts            # SSH on port 22
   ssh-keyscan -t ed25519 -p 2222 203.0.113.20 > known_hosts    # SSH on another port
   ```

2. Show the fingerprint:

   ```bash
   ssh-keygen -lf known_hosts
   ```

3. Compare the fingerprint with the one on the server itself or in the console of your provider.
4. Paste the contents of `known_hosts` into **Host keys**, or give the file with `--known-hosts-file`.

With host keys, Geass refuses a machine that shows a different key. It refuses before it sends anything. These rules
apply:

- Use plain lines from `ssh-keyscan`. Geass refuses hashed names (`ssh-keyscan -H`), and `@cert-authority` or
  `@revoked` lines.
- The port is part of the address. Use `ssh-keyscan -p` for a port that is not 22.
- A rebuilt machine has a new host key. Update the host keys before Geass connects to it again. See
  [Update the host keys of a server](#update-the-host-keys-of-a-server).

## Watch the server join

A server takes a few minutes to join. It appears in the **Nodes** table on the **Usage** page as soon as you add it.
The **Status** column shows where the server is:

| Status | Meaning |
| --- | --- |
| **Waiting** | Geass has not started to join the server. |
| **Creating machine** | The cloud starts the machine. Only cloud machines have this status. |
| **Joining** | Geass installs K3s on the server, or waits for the server to register. |
| **Ready** | The server joined the cluster. |
| **Failed** | The server did not join. The **Status** column shows the error. |

The footer of the left rail shows one bar for each server that joined, and the number of servers. From a terminal,
run `geass servers list`. It prints the same statuses and the error of each server that failed. `geass nodes list`
also lists a server that has not joined.

While a new server starts, the cluster can briefly report that it is not ready. Do not deploy during that time.

If the server does not reach **Ready** after 10 minutes, see [Troubleshooting](#troubleshooting).

## Fix a server that did not join

When a server fails, Geass stops and waits for you. It does not try again by itself.

1. Open **Usage** in the left rail.
2. In **Nodes**, find the server. Read the error in the **Status** column.
3. Fix the cause. See [Troubleshooting](#troubleshooting) for the usual errors.
4. Click **Retry**. Geass starts again from the first step.

From a terminal, run `geass servers list` to read the error. After you fix the cause, run `geass servers retry NAME`.

You can retry only a server that failed. A server that is **Waiting**, **Creating machine**, or **Joining** is still in
progress. If you cannot fix the cause, remove the server, and add it again. See [Remove a node](#remove-a-node).

### Update the host keys of a server

A server that you have shows **Update host keys** in its menu. Use it when you rebuilt the machine, or when the
error says `is not the key recorded in known_hosts`. A cloud machine does not have this action, because Geass records
its host key itself.

1. Get the new host key, and compare its fingerprint with the one on the server. See
   [Pin the server's host key](#pin-the-servers-host-key-recommended).
2. In **Nodes**, click the menu of the server, and select **Update host keys**.
3. Paste the lines that `ssh-keyscan` prints, and click **Save host keys**.
4. If the server failed, click **Retry**.

From a terminal, run this command. It reads the host keys from standard input:

```bash
ssh-keyscan -t ed25519 203.0.113.20 | geass servers update edge-1 --known-hosts-file -
```

Only the lines for the address of this server change. Lines for other servers in the same Secret stay. Geass refuses
host keys that list no key for the address of the server.

## Set a server's region, site, and tags

Placement groups servers, so that you can keep a project on part of the cluster. A server has these placement values:

- An optional **Region**.
- An optional **Site**: the provider or the location in the region, for example `aws-eu-north-1`.
- Up to 16 **Tags**, for example `storage` or `gpu`.

1. Open **Usage** in the left rail.
2. In **Nodes**, find the server, and click **Edit**.
3. Enter the **Region**, the **Site**, and the **Tags**. Separate the tags with a comma.
4. To keep all other work off the server, turn on **Reserve this node for `<tag>`**. A reserved server runs only work
   that asks for that tag.
5. To make a tag a compliance tag, turn on **`<tag>` is a compliance tag** under **Compliance tags**. See
   [Compliance node tags](/docs/guides/compliance-tags/).
6. Click **Save**.

Values contain lowercase letters, numbers, and hyphens, with at most 40 characters. They cannot start or end with a
hyphen. The change does not move running services. A service that restarts goes only to a server that matches. You can
edit every server, the first server included.

From a terminal, `geass nodes tag worker-1 --region eu --tags storage --reserved storage` does the same. This command
replaces all of the placement of the server, so name every tag that the server must keep.

For how projects use placement, see [Scaling and placement](/docs/guides/scaling-and-placement/).

## Remove a node

To remove a server that you added, open its menu in the **Nodes** table, and select **Remove**. Enter the name of the
server to confirm. From a terminal, run `geass servers remove NAME`. You can also remove a server that failed to join.
You cannot remove a control plane server here. See [Under the hood](#under-the-hood). To remove a server of a pool,
change the pool.

:::caution
Read the list in the **Remove server** dialog before you confirm. Data whose only copy is on the server is lost when you
remove a server that you have.
:::

The **Remove server** dialog lists the data that is on the server and nowhere else:

- A database whose only copy is on the server.
- A replica of a database that has other copies. Geass builds a new replica on another server a few minutes later. See
  [High availability](/docs/operations/high-availability/#what-geass-does-after-fifteen-minutes).
- A volume with a single copy.

For a server that you have, Geass connects over SSH, removes its software from the server, and takes the server out of
the cluster. Geass does not move services off the server first. They stop, and then start on other servers. Geass does
not delete anything else on the server.

For a cloud machine, Geass moves the services off for up to five minutes. Then Geass deletes the machine at the cloud,
with the data on its disk.

If the server was unreachable during the removal, its software stays on it, and the server can join the cluster again.
Geass has no dashboard option to clean up such a server. See [Under the hood](#under-the-hood).

## Troubleshooting

The **Status** column of the **Nodes** table shows the error of a server that failed. `geass servers list` prints the
same error. An error has the form `step <name> failed: <error>`, with the output of the command. These are the usual errors:

| Error contains | Fix |
| --- | --- |
| `i/o timeout` or `connection refused` after `ssh dial` | The first server cannot reach the SSH port. Check the firewalls, the address, and the SSH port. |
| `unable to authenticate` | The user or the key is wrong, or the public key is not in `authorized_keys` of that user. |
| `this private key is passphrase protected` | Make a key without a passphrase. |
| `sudo: a password is required` | Give the user passwordless `sudo`, or use `root`. |
| `check-cluster-reachable failed` | The new server cannot reach 6443/tcp on the first server. Open the port. If the message says `loopback address`, see [Set the address workers join](#set-the-address-workers-join). |
| `is not the key recorded in known_hosts` or `is not listed in known_hosts` | The machine is not the one that you pinned, the machine was rebuilt, or the entry is for another address or port. Check the fingerprint on the machine, and update the host keys. |
| `known_hosts uses the @revoked marker`, `has hashed host names`, or `has no host keys in it` | Replace the host keys with plain lines from `ssh-keyscan`. |
| `step check-curl failed` | The server has no `curl` and no `apt-get`. Install `curl`. |

If the server stays at `Joining`, the server installed but did not register. Usually 8472/udp or 6443/tcp is closed, or a
server with that name already exists.

After you fix the cause, click **Retry**, or run `geass servers retry NAME`. See
[Fix a server that did not join](#fix-a-server-that-did-not-join).

## Under the hood

Each server is a `GeassNode` object in the namespace `geass-system`. Its `status.phase` is `Pending`, `Creating` (cloud
machines only), `Joining`, `Ready`, or `Failed`. The [GeassNode reference](/docs/reference/resources/geass-node/) lists
every field. The **Status** column shows `status.phase` and `status.message`. To read them with `kubectl`, run this
command on the first server:

```bash
sudo k3s kubectl -n geass-system get geassnode edge-1 \
  -o jsonpath='{.status.phase}{"  "}{.status.message}{"\n"}'
```

If the phase stays `Joining`, read the K3s agent log on the new server with `sudo journalctl -u k3s-agent`.

If the server was unreachable during its removal, run `sudo /usr/local/bin/k3s-agent-uninstall.sh` on it. If you do
not, the server can join the cluster again.

Geass has no SSH access to a control plane server, because the installer joined it. So Geass cannot stop K3s on it.
The dashboard and `geass servers remove` refuse a control plane server for this reason. To remove one, see [High availability](/docs/operations/high-availability/#take-a-server-away).

**Retry** adds the annotation `geass.dev/retry` to a `Failed` server, and Geass starts again from the first step. To
add the annotation yourself, run this command:

```bash
sudo k3s kubectl -n geass-system annotate geassnode edge-1 geass.dev/retry=true
```

Geass installs a `Ready` server again when its node disappears, or stays `NotReady` for more than five minutes. This
needs the SSH key and SSH access to still work.

The SSH key of a server that you added in the dashboard is the Secret `<name>-ssh` in `geass-system`. **Update host
keys** changes the key `known_hosts` of this Secret. To write the Secret yourself, use the same private key and the new
`known_hosts`:

```bash
sudo k3s kubectl -n geass-system create secret generic edge-1-ssh \
  --from-file=ssh-privatekey=./geass-worker --from-file=known_hosts=./known_hosts \
  --dry-run=client -o yaml | sudo k3s kubectl apply -f -
```

These are the steps that Geass runs on the new server over SSH, through `sudo -n` unless the user is `root`:

1. `check-curl`: installs `curl` with `apt-get` if it is missing.
2. `install-iscsi`: installs the iSCSI initiator for replicated volumes. A failure does not stop the join.
3. `install-tailscale` and `join-tailnet`: only for a [Tailscale mesh](/docs/operations/mesh-networking/).
4. `check-cluster-reachable`: `curl <serverURL>/ping` must answer `pong`.
5. `check-wireguard` and `open-overlay-port`: only for multi-site clusters.
6. `write-k3s-config`: writes `/etc/rancher/k3s/config.yaml` with the labels and taints of the placement.
7. `install-k3s` and `enable-k3s`: install and start the K3s agent with the join token from the Secret `geass-token`.

The new server gets the oldest K3s version of the control plane. Placement becomes the labels
`topology.kubernetes.io/region`, `topology.kubernetes.io/zone`, and `tag.geass.dev/<tag>=true`. A reserved tag adds the
taint `tag.geass.dev/<tag>=reserved:NoSchedule`, and a compliance tag adds `tag.geass.dev/<tag>=compliance:NoSchedule`.
**Data on this server** `None` adds the taint `geass.dev/ephemeral`.

The join token is on the command line of the install, so local users of the new server can see it while the install
runs. Anyone who can read Secrets in `geass-system` can read the SSH keys.

### Set the address workers join

A new server joins the address in `spec.serverURL` of the `GeassCluster`. The installer writes the address of the first
server there: usually its private address, for example `https://10.0.0.5:6443`. A multi-site cluster stores the public
address. To read the address, run this command:

```bash
sudo k3s kubectl -n geass-system get geasscluster default -o jsonpath='{.spec.serverURL}{"\n"}'
```

Change the address only when new servers must reach the first server on another address. Examples are a server with
several network interfaces, or new servers that use a public address or a name:

```bash
sudo k3s kubectl -n geass-system patch geasscluster default --type merge \
  -p '{"spec":{"serverURL":"https://10.0.0.5:6443"}}'
```

If `serverURL` is a loopback address, Geass uses the address that the node of the control plane registered with. If no
such address exists, the join fails at `check-cluster-reachable`, and the message names the loopback address. Patch
`serverURL`, and then remove the server and add it again.

### Prepare the new server

You can also describe a server in a manifest, instead of in the dashboard. First save the private key, and optionally
the host keys, as a Secret:

```bash
sudo k3s kubectl -n geass-system create secret generic worker-ssh-key \
  --from-file=ssh-privatekey=./geass-worker \
  --from-file=known_hosts=./known_hosts
```

Geass reads the first of these keys that exists: `ssh-privatekey`, `id_rsa`, `private-key`, or `key`. Then apply the
server:

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

The name becomes the name of the node, so use a lowercase name that no other node uses. `eip` is the SSH address.
Optional fields are `sshPort`, `placement`, and `storage`. To remove the server, delete the object:
`sudo k3s kubectl -n geass-system delete geassnode worker-1`.

## Related

- [Requirements](/docs/getting-started/requirements/)
- [Node pools and autoscaling](/docs/guides/node-pools/)
- [Scaling and placement](/docs/guides/scaling-and-placement/)
- [Multi-provider clusters](/docs/operations/multi-provider/)
- [High availability](/docs/operations/high-availability/)
- [Storage](/docs/guides/storage/)
