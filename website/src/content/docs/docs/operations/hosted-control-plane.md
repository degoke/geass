---
title: "Hosted control plane"
description: "Run Geass on a cluster whose K3s server is hosted elsewhere. Every node is a worker, and the operator image comes from a registry."
---

The installer makes the control plane one of the machines of the cluster. The K3s server and the Geass operator run
there, and workers join it. A cluster can also have its control plane **hosted outside its nodes**. That control plane
is a K3s server that runs with `--disable-agent` in a different location, for example a pod at a hosting service. The
server keeps its state in an external datastore and runs no workloads. Every node is then a worker, and the operator
runs on one of the workers. [rungeass](https://rungeass.com) runs clusters in this way.

You do not turn on this mode. A cluster that the installer made always has a node with the label
`node-role.kubernetes.io/control-plane`. When the cluster has no node with that label, Geass finds three things in a
different way:

| | Control plane on a node | Hosted control plane |
| --- | --- | --- |
| K3s release that a new worker installs | The release that the kubelet of the control plane node reports | The release that `/version` of the API server reports |
| Where the [firewall of a machine that Geass creates](/docs/guides/node-pools/#how-a-machine-joins) lets SSH in from | The addresses of the control plane node | The addresses of every node, and the public address that the connections of the operator leave from |
| Where the pods of the network policy check run | The control plane node, where the installer put the image | Any node, because the image comes from a registry |

## What has to be true

- **The K3s server is reachable from every node** on the address in `spec.serverURL` of the `GeassCluster`. The
  server also advertises that same address, with the K3s flags `--advertise-address` and `--advertise-port`. After
  agents join, they move their connection to the advertised address. Pods also reach the API through that address. Thus
  the address of the server inside its host is not sufficient.
- **The server is started with `--egress-selector-mode=cluster`.** With this flag, the API server reaches kubelets,
  pods, and services through the tunnel that each agent keeps open to the server. `kubectl logs`, `exec`, metrics, and
  admission webhooks need this tunnel. Nodes need no inbound port for the control plane.
- **The operator is applied from `config/external-control-plane`.** This configuration pulls the image from
  `ghcr.io/degoke/geass`, and it lets the operator run on any node:

  ```bash
  cd config/external-control-plane && kustomize edit set image controller=ghcr.io/degoke/geass:vX.Y.Z
  kustomize build config/external-control-plane | kubectl apply --server-side -f -
  ```

  Then create the objects that the installer usually creates:

  - The `geass-token` Secret in `geass-system`, with the node token of the server under `token`.
  - The `GeassCluster` named `default`, with `spec.serverURL` set to the address from the first item.

## What Geass does not do here

- **Geass does not upgrade the K3s server.** The host of the server upgrades it. `spec.k3s.version` still moves the
  workers, one at a time. But Geass moves them only after the server reports that release. Until then, the upgrade
  waits and says that it waits.
- **Geass does not run the installer, take etcd snapshots, or join extra control plane servers.** The datastore and
  its backups are the responsibility of the host.
- **Geass might ask `api.ipify.org`, then `ifconfig.me`, for its own public address** when it creates a machine at a
  cloud. It asks at most one time in 15 minutes. Without this address, the firewall of the new machine does not let
  through an operator that is behind a NAT. If no answer comes, the firewall lets SSH in from the addresses of the nodes
  only.
