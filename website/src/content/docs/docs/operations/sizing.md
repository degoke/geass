---
title: "Cluster size"
description: "How many services one Geass cluster holds, how to size the control plane server, what to watch as the cluster grows, and what to change first."
---

This page tells you how large one Geass cluster can grow, and what to change as it grows. Most clusters never need this page.

**The short answer.** With the limits that it ships with, Geass holds 20,000 services. Its memory grows by about 36 KiB for each service. After a restart or an update, Geass needs about 9 seconds per 1,000 services before it is idle again. Above about 17,000 services, an alert tells you to raise the memory limit of Geass.

The numbers come from a measurement of 500 to 20,000 services on one machine. Nothing above 20,000 services was measured. See [Under the hood](#under-the-hood) for the method.

## Size your servers

The [requirements](/docs/getting-started/requirements/) give the minimum server: 2 GB of memory and 10 GiB of free disk. That minimum is for the installer. It is not a size for your workload. The add-ons that Geass installs, for example metrics and logs, use part of the server before you deploy anything.

To see how much room your servers have, open **Usage** in the left rail. The summary shows **CPU available**, **Memory available**, and **Healthy nodes**. The **Nodes** table shows the CPU and memory of each server. See [Usage and capacity](/docs/guides/usage-and-capacity/).

For a large cluster, use these sizes:

- **The control plane server.** From 10,000 services, use 4 CPU cores, 8 GiB of memory, and fast SSD or NVMe storage. The control plane server holds the state of the cluster, and that state is sensitive to a slow disk.
- **The number of servers.** A server runs at most 110 replicas, and the add-ons on it count too. So 10,000 services with one replica each need at least 91 servers. 20,000 services need at least 182 servers.
- **Three control plane servers.** Before you go large, [run three control plane servers](/docs/operations/high-availability/#run-three-control-plane-servers).

## What to do at each size

| Services | What to do |
| --- | --- |
| Up to 10,000 | Nothing for Geass. After a restart, Geass is back to work in about a minute and a half. Use fast disks under the control plane server. |
| 10,000 to 20,000 | Watch **Geass health**. The memory alert fires at about 17,000 services. [Raise the size limit of the cluster's state](#the-clusters-state), and run three control plane servers. |
| More than 20,000 | Not measured. Geass reaches its 1 GiB limit at about 27,000 services. [Raise its memory limit](#memory), and measure your own cluster. Think about more than one cluster, because one Geass holds its whole cluster in memory. |

## Watch the cluster as it grows

Open **Usage**, and scroll to **Geass health**. These charts are the most useful for size:

- **Operator memory**: the memory that Geass uses.
- **Work waiting**: changes that wait for Geass. After a restart, this number goes up, and then goes down.
- **Reconcile time (slowest 1%)**: how long Geass needs for one change.

With [alerts](/docs/guides/alerts/) on, these alerts warn you:

- `GeassOperatorMemoryHigh`: the memory of Geass stays above 700 MiB for 15 minutes.
- `GeassWorkqueueBacklog`: more than 25 changes wait for 15 minutes.
- `GeassSlowReconciles`: one change takes more than 30 seconds.

## Where the limits are

### Memory

Geass ships with a memory limit of 1 GiB. As its memory gets near 800 MiB, Geass collects unused memory harder, so that it slows down before it is stopped. A small cluster uses about a tenth of the limit.

| Services | Memory at rest | Memory at the peak of a restart | Restart time |
| --- | --- | --- | --- |
| 500 | 95 MiB | 91 MiB | 7 s |
| 1,000 | 111 MiB | 113 MiB | 9 s |
| 4,000 | 216 MiB | 251 MiB | 33 s |
| 10,000 | 422 MiB | 433 MiB | 87 s |
| 20,000 | 785 MiB | 830 MiB | 200 s |

A service with a lot of configuration or many replicas uses more memory. Databases and a cluster that runs for a long time also use more.

Earlier releases shipped a limit of 512 MiB. That limit held about 15,000 services. At 20,000 services, Geass could not restart. An update to this release gives you the 1 GiB limit.

Geass has no dashboard option or command to raise this limit yet. See [Under the hood](#under-the-hood).

:::caution
Set the limit again after each update of Geass. The installer puts back the limit of the release each time that it updates Geass.
:::

### Time after a restart

After a restart or an update, Geass checks every service, one at a time. This takes about **9 seconds per 1,000 services**. During that time, a change that you make waits for its turn. A deploy that you ask for in the first minute starts when Geass reaches it. **Work waiting** shows the queue as it gets shorter.

Geass makes one change at a time on purpose. Two services in one project share some settings. Two changes at the same moment can conflict, and a service that fails many times shows **Provisioning failed after repeated errors**.

### The cluster's state

The cluster's state has a size limit of **2 GiB**. Each service uses about 21 to 34 KiB, so 20,000 services use about 427 MiB. Events and add-ons use the same space. When the state reaches its limit, the cluster stops accepting changes until somebody compacts it.

You can raise the limit to 8 GiB, the largest recommended size. Geass has no dashboard option or command for this yet. See [Under the hood](#under-the-hood).

A larger state makes snapshots slower and larger.

## Troubleshooting

**Geass restarts, and the reason is `OOMKilled`.** Geass reached its memory limit. [Raise the limit](#memory).

**Deploys wait for minutes after an update of Geass.** Geass checks every service after a restart, and your change waits for its turn. The wait is about 9 seconds per 1,000 services. **Work waiting** shows the queue.

**Geass restarts again and again, and its log says `Could not wait for Cache to sync`.** Geass could not read the cluster within two minutes, because it has too little memory. [Raise the limit](#memory), or update to this release.

**A change fails with `etcdserver: mvcc: database space exceeded`.** The cluster's state is full, and the cluster accepts no changes. Geass has no dashboard option to compact the state. See [Under the hood](#under-the-hood). Then [raise the limit](#the-clusters-state).

## Under the hood

The memory limit is the container limit of the `geass-controller-manager` Deployment in `geass-system`, with `GOMEMLIMIT` set to 800 MiB. The scheduler places the pod by its request of 128 MiB. Geass runs one worker for each controller. The cluster's state is the embedded etcd of K3s. To count what Geass manages, run `sudo k3s kubectl get geassapps,geassdatabases -A --no-headers | wc -l`.

To raise the memory limit of Geass, run these commands on the control plane server. These values follow the same ratio as the measured values, but were not measured:

```bash
sudo k3s kubectl -n geass-system set resources deployment geass-controller-manager \
  --limits=memory=2Gi --requests=memory=256Mi
sudo k3s kubectl -n geass-system set env deployment geass-controller-manager GOMEMLIMIT=1600MiB
```

To see the current limit, run this command:

```bash
sudo k3s kubectl -n geass-system get deploy geass-controller-manager -o jsonpath='{.spec.template.spec.containers[0].resources}'
```

To raise the size limit of the cluster's state to 8 GiB, do these steps on each control plane server:

1. Add these lines to `/etc/rancher/k3s/config.yaml`:

   ```yaml
   etcd-arg:
     - quota-backend-bytes=8589934592
   ```

2. Restart K3s with `sudo systemctl restart k3s`. Restart one server at a time.

When the state is full, compact and defragment it as the K3s documentation says.

The measurement started a real API server and etcd, ran the operator, and created projects with 25 services each. Each service was image-based, with one replica and a few variables. It waited until every service was ready, and then restarted the operator. It ran on an AWS `m6i.xlarge` (4 vCPUs, 16 GiB, Ubuntu 24.04), with no kubelet. It did not include builds, databases, backups, dashboard users, or the CPU of the API server and etcd. Each service made about 5 objects in the measurement. A real cluster has 7 to 10.

To run the measurement yourself, use a machine with Go and the Kubernetes test binaries. `make test` fetches the binaries.

```bash
make loadtest LOADTEST_ARGS="-apps 250,500,1000"
```

The options `-per-project`, `-replicas`, `-gomemlimit 800MiB`, and `-json` are useful. A run takes a few minutes for 500 services, and more than half an hour for 20,000. Run it on a machine that does nothing else.

## Related

- [Usage and capacity](/docs/guides/usage-and-capacity/)
- [High availability](/docs/operations/high-availability/)
- [Upgrade Geass](/docs/operations/upgrading/)
- [Logs, metrics and terminal](/docs/guides/observability/#metrics-and-alerts-for-the-operator)
