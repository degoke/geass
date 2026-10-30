---
title: "Cluster size"
description: "How many services one Geass cluster holds: the memory and time the operator needs as it grows, where its limits are, and what to change first."
---

A Geass cluster is one operator that keeps every project, service, and database in step with Kubernetes. This page tells you these things:

- What the operator costs as the cluster grows.
- Where the limits that the operator ships with run out.
- What to change before the limits run out.

The numbers are measured on one real machine, from 500 to 20,000 services. They are not measured above 20,000, and the page says so where it matters. The method is below, so that you can repeat it and extend it to your own size.

**The short answer.** The memory of the operator grows in a straight line, about 36 KiB for each service. The operator ships with two limits: 1 GiB for the container, and a runtime limit of 800 MiB. These limits held 20,000 services: 785 MiB at rest and 830 MiB at the heaviest moment of a restart. Earlier releases shipped 512 MiB and 400 MiB. Those limits held for about 10,000 services and failed at 20,000. A cluster that is updated to this release gets the larger limits. Above 20,000 services, raise the limits again. A restart takes about 9 seconds per 1,000 services. Nothing else in Geass is close to a limit before the machine under the control plane is. Most clusters never need this page.

## What was measured

For each size, the measurement does these steps:

1. It starts a real Kubernetes API server and etcd.
2. It runs the binary of the operator against them.
3. It fills the cluster with projects and services through the Geass objects, the way people do. There are 25 services to a project. Each one is an image-based service with one replica and a few environment variables.
4. It waits until every service is ready and the operator has been idle for a minute.
5. It reads the memory of the operator from the process itself.
6. It stops the operator and starts it again over the same cluster. A restart is the heaviest thing that the operator ever does.

The operator makes everything else itself: the namespaces, the network policies, and the Deployment, Service, and deployment record of each service. envtest has no kubelet and no controller-manager, so the measurement plays both. It starts a pod for each Deployment and marks the Deployment available, which is what takes a service to **Ready**.

The numbers below are from an AWS `m6i.xlarge` (4 vCPUs, 16 GiB, Ubuntu 24.04). The machine ran the API server, etcd, the operator, and the stand-in for the kubelet all together. The operator ran with these limits:

- Up to 8,000 services: no memory limit.
- From 10,000 up: the runtime limit of earlier releases (`GOMEMLIMIT=400MiB`).
- From 15,000 up: also inside the container limit of earlier releases, 512 MiB. The memory controller of the kernel enforced this limit. So, as in Kubernetes, the operator would have been killed if it went over the limit.
- One more run at 20,000 services: the limits that this release ships, 1 GiB and 800 MiB.

The measurement does **not** include what a real cluster adds:

- Builds.
- Databases and their backups.
- A dashboard with people on it.
- The Kubernetes controllers and the kubelets, and the events that they write.

A real cluster also has real nodes. At the Kubernetes default of 110 pods to a node, 20,000 services of one replica each need at least 182 nodes. That is a limit before any of the limits on this page. Treat the numbers as what the operator needs to hold a cluster of ready services. Then add the other work of the machine.

## What it found

| Services | Projects | Objects in the API server | Operator memory (RSS) | Live heap | First deploy: settled in | Restart: peak memory | Restart: busy for |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 500 | 20 | 2,590 | 95 MiB | 20 MiB | 72 s | 91 MiB | 7 s |
| 1,000 | 40 | 5,170 | 111 MiB | 32 MiB | 92 s | 113 MiB | 9 s |
| 2,000 | 80 | 10,330 | 145 MiB | 61 MiB | 134 s | 159 MiB | 17 s |
| 4,000 | 160 | 20,650 | 216 MiB | 128 MiB | 202 s | 251 MiB | 33 s |
| 8,000 | 320 | 41,290 | 356 MiB | 158 MiB | 354 s | 418 MiB | 67 s |
| 10,000 (earlier limits) | 400 | 51,610 | 422 MiB | 214 MiB | 434 s | 433 MiB | 87 s |
| 15,000 (earlier limits) | 600 | 77,410 | 424 MiB | 272 MiB | 689 s | 451 MiB | 178 s |
| 20,000 (earlier limits) | 800 | 103,210 | 451 MiB | 340 MiB | 1209 s | the operator did not restart: see below | none |
| 20,000 (limits as shipped now) | 800 | 103,210 | 785 MiB | 581 MiB | 991 s | 830 MiB | 200 s |

*Memory is what the process of the operator holds when nothing is happening. The live heap is what the operator keeps in memory, mostly copies of what the cluster holds. The rest is the Go runtime and the garbage that the runtime has not collected yet. The first deploy starts when every service is created at the same time. It ends when every service is ready and the operator has been quiet for 45 seconds. The restart columns are for a stop and a start of the operator. They show the highest that its memory went, and how long the operator spent to read the cluster again and check every service before it was idle.*

To a first approximation, each service adds the same amount to each of these items:

| What | Per service | Where it comes from |
| --- | --- | --- |
| Objects in the API server | 5 in this measurement | The service, its Deployment, its pod, and the two Geass records (`GeassApp` and `GeassDeployment`). A real cluster adds a ReplicaSet and an EndpointSlice for each service. It also adds an Ingress when the service has a domain, a ServiceMonitor when it has metrics, and a ConfigMap or a Secret for its settings. Expect 7 to 10 |
| Etcd storage | 21 to 34 KiB | 427 MiB for 20,000 services, with the history that etcd keeps. Events and Helm releases are not in it |
| Operator memory | About 19 KiB of heap, 36 KiB of memory | The operator keeps what it reads in memory, so that it can answer without a request to the API server each time. These numbers are for an operator that is free to use memory. For an operator that is held to a limit, see below |
| Operator CPU, first deploy | 11 to 14 ms | The work to make everything for a new service, at any size up to 20,000, when the operator has the memory that it needs |
| Operator CPU, restart | 4 to 10 ms | The work to read everything again and check it, when the operator has the memory that it needs |

The memory is a straight line over the whole range. Twice the services means about twice what the operator holds above its starting size of 70 to 80 MiB.

## Where the limits are

### The operator's memory

The operator ships with two memory limits:

- A memory limit of 1 GiB.
- A runtime limit of 800 MiB (`GOMEMLIMIT`). As the operator nears this limit, the runtime collects garbage harder, so that the operator is slowed before it is killed.

A limit is not a reservation. The scheduler places the pod by its much smaller request (128 MiB). The operator of a small cluster uses about a tenth of the limit. The measurement ran 20,000 services with these limits, and with the 512 MiB and 400 MiB that earlier releases shipped:

- **With the limits it ships now, 20,000 services were comfortable.** The operator held 785 MiB at rest and 830 MiB at the heaviest moment of a restart. The peak that the kernel recorded for the container was 786 MiB of the 1 GiB, which leaves about a quarter to spare. Each service cost 11 ms of CPU, and a restart finished in 200 seconds.
- **With the earlier limits, up to 15,000 services were fine.** The operator held 422 MiB at 10,000 services, and 424 MiB at 15,000. The runtime limit kept it there. The work for each service stayed at 11 to 14 ms of CPU.
- **At 20,000 services the earlier limits cost CPU first.** The live heap was close to the runtime limit, and the work for each service was 61 ms. That is five times the CPU (1,227 CPU-seconds against 212), spent on garbage collection. The first deploy took 20 minutes against 16.
- **And then a restart did not finish.** The operator reads every object of the cluster before it starts work, and it exits if that takes more than two minutes. The operator was held to 512 MiB and slowed by garbage collection. In one run, it ran out of time (`Could not wait for Cache to sync`). In another run, which gave it longer, it went over its 512 MiB and the kernel killed it. On a cluster of this size, an update of Geass or any other restart of the operator would have left the operator down. That is why the limits were raised.

The line is for services like the ones measured. These items cost more:

- A service that carries a lot of configuration or many replicas.
- A database.
- A cluster that has been running for a long time, because it holds pods that were not in the measurement.

Look at the memory of the operator before you need to. The **Geass health** panel on the **Usage** page charts it. The alert `GeassOperatorMemoryHigh` fires when the memory stays above 700 MiB. On services like these, that is at about **17,000** services. When the alert fires, or if you run more than 20,000 services, raise the limits in the same proportion. The numbers below are not measured. They follow the same ratio as the numbers that were measured:

```bash
kubectl -n geass-system set resources deployment geass-controller-manager \
  --limits=memory=2Gi --requests=memory=256Mi
kubectl -n geass-system set env deployment geass-controller-manager GOMEMLIMIT=1600MiB
```

:::caution
After each update of Geass, set these limits again. The installer applies the manifest of the operator again every time it updates Geass, and that puts the limits back to what the release ships. To check the limits, run `kubectl -n geass-system get deploy geass-controller-manager -o jsonpath='{.spec.template.spec.containers[0].resources}'`.
:::

### Time: what a restart costs

The operator checks every service when it starts, and each of its controllers does one thing at a time. The measurement put this check at about **9 seconds per 1,000 services**: 33 seconds at 4,000, 67 at 8,000, and 87 at 10,000. The time per service grows a little as the cluster grows. During the check, a change that you make waits behind the services in front of it in the queue. A deploy that you ask for during the first minute of a restart starts when the queue reaches it. An update of Geass has the same cost, because an update restarts the operator.

Geass sets no concurrency for its controllers, so each controller reconciles one object at a time. Do not raise the concurrency on a hunch. Two services in one project share namespaces, secrets, and policies. Two reconciles that change them at the same moment conflict. A reconcile that fails several times in a row ends in **Provisioning failed after repeated errors**. That state pauses the service until someone edits it. One reconcile at a time is slower, but it cannot cause that state.

The alerts `GeassWorkqueueBacklog` and `GeassSlowReconciles` watch for an operator that is not getting through its work. `GeassWorkqueueBacklog` fires when more than 25 items wait for fifteen minutes.

### The first deploy

The first measurement found a cost that grew with the size of the cluster. The cost was in the creation of a very large number of services at the same time. You do that when you restore a cluster or move into Geass in bulk. Before a service is created, the operator checks that the nodes have room for it. The operator did that check by reading every pod in the cluster and adding up what the pods ask for. At 10,000 pods, that is about a fifth of a second of work for each new service. So each service cost more to add than the one before it.

The operator now keeps what the pods ask for, node by node, and updates it from their events. The operator reads that record instead. The table shows the same sizes, measured on the same machine before and after the change. The operator had no memory limit up to 8,000 services, and the earlier runtime limit at 10,000:

| Services | Operator CPU before | Operator CPU after | First deploy settled in, before | After |
| --- | --- | --- | --- | --- |
| 500 | 10 CPU-s (21 ms each) | 7 CPU-s (14 ms each) | 75 s | 72 s |
| 1,000 | 23 CPU-s (23 ms each) | 14 CPU-s (14 ms each) | 100 s | 92 s |
| 2,000 | 58 CPU-s (29 ms each) | 27 CPU-s (13 ms each) | 151 s | 134 s |
| 4,000 | 170 CPU-s (42 ms each) | 48 CPU-s (12 ms each) | 272 s | 202 s |
| 8,000 | 572 CPU-s (72 ms each) | 86 CPU-s (11 ms each) | 637 s | 354 s |
| 10,000 | 1004 CPU-s (100 ms each) | 106 CPU-s (11 ms each) | 940 s | 434 s |

The cost of each service is now level, and a first deploy takes time in proportion to the number of services. The time for one new service in a cluster that is already running was not measured.

### Etcd, the cluster's database

K3s runs etcd with the upstream default of a **2 GiB** database. At 21 to 34 KiB a service (427 MiB at 20,000), the services alone do not matter until tens of thousands of services. But events, Helm releases, and every other thing in the cluster share the database. A database that reaches its limit stops accepting writes. Then the whole cluster goes read-only until the database is compacted.

Check what the database holds, and raise the limit before you are near it:

1. On each server, set the limit in the K3s configuration (`/etc/rancher/k3s/config.yaml`):

```yaml
etcd-arg:
  - quota-backend-bytes=8589934592     # 8 GiB, the most etcd recommends
```

2. Restart K3s one server at a time.

K3s snapshots etcd on its own schedule, and the installer takes a snapshot before an update. A larger database makes both snapshots slower and larger.

### Kubernetes itself

Upstream Kubernetes is tested to 5,000 nodes, 150,000 pods, and 300,000 containers in one cluster, with no more than 110 pods on a node. A Geass cluster on K3s reaches the limits of its control plane machine long before these numbers. Watch that machine. K3s runs the API server, etcd, and the scheduler on it, and also the Geass operator. A large number of services means a large number of watches and writes. This measurement did not measure the CPU of the API server or of etcd. So watch the CPU and the disk latency of the machine, because etcd is sensitive to a slow disk. Before you go large, use [three control plane servers](/docs/operations/high-availability/#run-three-control-plane-servers) and fast disks.

## What to do at each size

| Services | What to do |
| --- | --- |
| Up to 10,000 | Nothing for the operator. The defaults are generous, and after a restart the operator is back to work in about a minute and a half. Use fast disks under the control plane. |
| 10,000 to 20,000 | In the measurement, the limits of the operator held 20,000 services, with a quarter of the container limit to spare at the heaviest moment. Watch **Geass health**: the alert fires at about 17,000. Raise the limit of etcd and run three control plane servers. Size the control plane machine for the load: 4 cores, 8 GiB, and fast SSD or NVMe storage. You have at least 91 nodes to run the services on. |
| More than 20,000 | Not measured. At 36 KiB a service, the operator reaches 1 GiB at about 27,000 services. Raise its limits as above, and measure your own cluster. Consider more than one cluster, because one operator holds everything of its cluster in memory, and a restart goes over all of it. Geass has no sharding. |

## Check it yourself

The measurement is part of the repository (`hack/loadtest`). It runs against a Kubernetes API server and etcd that it starts itself. Run it on a machine with Go and the Kubernetes test binaries. `make test` fetches the binaries.

```bash
make loadtest LOADTEST_ARGS="-apps 250,500,1000"
```

The measurement builds the operator that it measures from the checkout, prints a table like the one above, and removes everything that it made. These options are useful:

- `-per-project`: the number of services in each project.
- `-replicas`.
- `-gomemlimit 800MiB`: measures the operator the way the manifest runs it.
- `-json`: keeps the results.

A run takes a few minutes for 500 services, about fifteen minutes for 10,000, and more than half an hour for 20,000. Run it on a machine that nothing else uses. A run on a busy machine is several times slower, and says little about the operator.

## Troubleshooting

**The operator is restarted by Kubernetes with the reason `OOMKilled`.**
The operator reached its 1 GiB limit. First, check how many services and databases the cluster holds: `kubectl get geassapps,geassdatabases -A --no-headers | wc -l`. Then raise the limit as above.

**Deploys wait for minutes after an update of Geass.**
The operator checks every service after its restart, and a change waits its turn. The wait is about 9 seconds per 1,000 services. The **Work waiting** chart on **Geass health** shows the queue as it shrinks.

**The operator restarts again and again, and its log says `Could not wait for Cache to sync`.**
The operator could not read the cluster within the two minutes that it gets. That happens on a very large cluster when the operator is held to less memory than it needs. See [The operator's memory](#the-operators-memory). Raise its limits as shown there, or update to this release, which ships larger limits.

**A write to the cluster fails with `etcdserver: mvcc: database space exceeded`.**
Etcd has reached its limit and the cluster is read-only. On a K3s server, compact and defragment etcd. Then raise the limit as above. This is a K3s procedure. For `etcdctl`, see the K3s documentation.

## Related

- [Usage and capacity](/docs/guides/usage-and-capacity/)
- [High availability](/docs/operations/high-availability/)
- [Upgrade Geass](/docs/operations/upgrading/)
- [Logs, metrics and terminal](/docs/guides/observability/#metrics-and-alerts-for-the-operator)
