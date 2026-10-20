---
title: "Hosted control plane"
description: "Run Geass on a cluster whose K3s server is hosted outside its nodes: every node a worker, the operator on a worker, the image from a registry. What changes and what to set up."
---

The installer makes the control plane one of the cluster's own machines: the K3s server and the Geass operator run there,
and workers join it. A cluster can also have its control plane **hosted outside its nodes**: a K3s server run with
`--disable-agent` somewhere else, such as a pod at a hosting service, which keeps its state in an external datastore and
runs no workloads. Every node is then a worker, and the operator runs on one of them. This is how
[rungeass](https://rungeass.com) runs clusters.

You do not turn this on. Geass sees that the cluster has no node labelled `node-role.kubernetes.io/control-plane`, which
a cluster the installer made always has, and works out three things another way:

| | Control plane on a node | Hosted control plane |
| --- | --- | --- |
| K3s release a new worker installs | What the control plane node's kubelet reports | What the API server's `/version` reports |
| Where the [firewall of a machine Geass creates](/docs/guides/node-pools/#how-a-machine-joins) lets SSH in from | The control plane node's addresses | Every node's addresses, and the public address the operator's connections leave from |
| Where the network policy check's pods run | The control plane node, where the installer put the image | Any node: the image comes from a registry |

## What has to be true

- **The K3s server is reachable from every node** on the address in the `GeassCluster`'s `spec.serverURL`, and it
  advertises that same address (K3s's `--advertise-address` and `--advertise-port`). Agents move their connection to
  the advertised address after they join, and pods reach the API through it, so the server's own address inside its
  host is not enough.
- **The server is started with `--egress-selector-mode=cluster`.** The API server then reaches kubelets, pods and
  services through the tunnel each agent keeps open to it, which `kubectl logs` and `exec`, metrics and admission
  webhooks need. Nodes need no inbound port for the control plane.
- **The operator is applied from `config/external-control-plane`**, which pulls the image from `ghcr.io/degoke/geass`
  and lets it run on any node:

  ```bash
  cd config/external-control-plane && kustomize edit set image controller=ghcr.io/degoke/geass:vX.Y.Z
  kustomize build config/external-control-plane | kubectl apply --server-side -f -
  ```

  Then create what the installer would have: the `geass-token` Secret in `geass-system` with the server's node token
  under `token`, and the `GeassCluster` named `default`, with `spec.serverURL` set to the address above.

## What Geass does not do here

- **It does not upgrade the K3s server.** Whoever hosts it does. `spec.k3s.version` still moves the workers, one at a
  time, but only once the server reports that release: until then the upgrade waits and says so.
- **It does not run the installer, take etcd snapshots or join extra control plane servers.** The datastore and its
  backups belong to the host.
- **It may ask `api.ipify.org`, then `ifconfig.me`, for its own public address**, at most every 15 minutes, when it
  creates a machine at a cloud. An operator behind a NAT is otherwise not let through the new machine's firewall. If
  the answer does not come, the firewall lets SSH in from the nodes' addresses only.
