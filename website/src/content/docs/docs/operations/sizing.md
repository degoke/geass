---
title: "Cluster size"
description: "How many services one Geass cluster holds: what the operator costs in memory and time as it grows, where its limits are, and what to change first."
---

A Geass cluster is one operator that keeps every project, service and database in step with Kubernetes. This page says what that costs as the cluster grows, where the limits it ships with run out, and what to change before they do. The numbers are measured, from 500 to 20,000 services, on one real machine. They are not measured above 20,000, and the page says so where it matters. The method is below, so that you can repeat it, and extend it to your own size.

**The short answer.** The operator's memory grows in a straight line, about 36 KiB for each service. The limits it ships with (1 GiB for the container, and a runtime limit of 800 MiB) held 20,000 services: 785 MiB at rest and 830 MiB at the heaviest moment of a restart. Earlier releases shipped 512 MiB and 400 MiB, which held for about 10,000 services and failed at 20,000, so a cluster that is updated to this release gets the larger limits. Above 20,000 services, raise them again. A restart takes about 9 seconds per 1,000 services. Nothing else in Geass is close to a limit before the machine under the control plane is. Most clusters will never need this page.

## What was measured

For each size the measurement starts a real Kubernetes API server and etcd, runs the operator's own binary against it, and fills the cluster with projects and services through the Geass objects, the way people do: 25 services to a project, each an image-based service with one replica and a few environment variables. The operator makes everything else itself: the namespaces, the network policies, and each service's Deployment, Service and deployment record. envtest has no kubelet and no controller-manager, so the measurement plays both: it starts a pod for each Deployment and marks the Deployment available, which is what takes a service to **Ready**. It waits until every service is ready and the operator has been idle for a minute, reads the operator's memory from the process itself, and then stops the operator and starts it again over the same cluster, which is the heaviest thing it ever does.

The numbers below are from an AWS `m6i.xlarge` (4 vCPUs, 16 GiB, Ubuntu 24.04), running the API server, etcd, the operator and the stand-in for the kubelet all together. The sizes up to 8,000 services ran without a memory limit on the operator. From 10,000 up, the operator ran with the runtime limit of earlier releases (`GOMEMLIMIT=400MiB`), and from 15,000 up inside their container limit of 512 MiB as well, enforced by the kernel's memory controller, so that going over it would have killed the operator as Kubernetes does. One more run at 20,000 services used the limits this release ships: 1 GiB and 800 MiB.

What it does **not** measure is what a real cluster adds: builds, databases and their backups, a dashboard with people on it, the Kubernetes controllers and the kubelets, and the events they write. And a real cluster has real nodes: at Kubernetes's default of 110 pods to a node, 20,000 services of one replica each need at least 182 nodes, which is a limit before any of the ones on this page. Treat the numbers as what the operator needs to hold a cluster of ready services, and add the machine's other work to it.

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

*Memory is what the operator's process holds when nothing is happening. The live heap is what it keeps in memory, mostly copies of what the cluster holds, and the rest is the Go runtime and the garbage it has not collected yet. The first deploy is the time from creating every service at once until every one of them is ready and the operator has been quiet for 45 seconds. The restart columns are for stopping the operator and starting it again: the highest its memory went, and how long it spent re-reading the cluster and checking every service before it was idle.*

Each service adds, to a first approximation, the same to everything:

| What | Per service | Where it comes from |
| --- | --- | --- |
| Objects in the API server | 5 in this measurement | The service, its Deployment, its pod, and the two Geass records (`GeassApp` and `GeassDeployment`). A real cluster adds a ReplicaSet and an EndpointSlice for each, and an Ingress when the service has a domain, a ServiceMonitor when it has metrics, and a ConfigMap or a Secret for its settings, so expect 7 to 10 |
| Etcd storage | 21 to 34 KiB | 427 MiB for 20,000 services, with the history etcd keeps. Events and Helm releases are not in it |
| Operator memory | About 19 KiB of heap, 36 KiB of memory | The operator keeps what it reads in memory, so that it can answer without asking the API server each time. These are for an operator that is free to use memory: see below for one that is held to a limit |
| Operator CPU, first deploy | 11 to 14 ms | Making everything for a new service, at any size up to 20,000 when the operator has the memory it needs |
| Operator CPU, restart | 4 to 10 ms | Reading it all again and checking it, when the operator has the memory it needs |

The memory is a straight line over the whole range: twice the services, about twice what the operator holds above its starting size of 70 to 80 MiB.

## Where the limits are

### The operator's memory

The operator ships with a memory limit of 1 GiB, and a runtime limit of 800 MiB (`GOMEMLIMIT`) that makes it collect garbage harder as it nears that, so that it is slowed before it is killed. A limit is not a reservation: the scheduler places the pod by its much smaller request (128 MiB), and the operator of a small cluster uses about a tenth of the limit. The measurement ran 20,000 services with these limits, and with the 512 MiB and 400 MiB that earlier releases shipped:

- **With the limits it ships now, 20,000 services were comfortable.** The operator held 785 MiB at rest and 830 MiB at the heaviest moment of a restart. The kernel's own peak for its container was 786 MiB of the 1 GiB, which leaves about a quarter to spare. Each service cost 11 ms of CPU, and a restart finished in 200 seconds.
- **With the earlier limits, up to 15,000 services were fine.** The operator held 422 MiB at 10,000 services, and 424 MiB at 15,000, with the runtime limit keeping it there, and the work for each service stayed at 11 to 14 ms of CPU.
- **At 20,000 services the earlier limits cost CPU first.** With the live heap close to the runtime limit, the work for each service was 61 ms: five times the CPU (1,227 CPU-seconds against 212), spent on garbage collection, and a first deploy of 20 minutes against 16.
- **And then a restart did not finish.** The operator reads every object of the cluster before it starts work, and it exits if that takes more than two minutes. Held to 512 MiB and slowed by garbage collection, it ran out of time in one run (`Could not wait for Cache to sync`). In another, which gave it longer, it went over its 512 MiB and the kernel killed it. On a cluster this size, an update of Geass or any other restart of the operator would have left it down. That is why the limits were raised.

The line is for services like the ones measured. A service that carries a lot of configuration or many replicas costs more, a database costs more, and a cluster that has been running for a long time holds pods that were not in the measurement.

Look at the operator's memory before you need to: the **Geass health** panel on the **Usage** page charts it, and the alert `GeassOperatorMemoryHigh` fires when it stays above 700 MiB, which on services like these is at about **17,000**. When it does, or if you run more than 20,000 services, raise the limits in the same proportion. The numbers below are not measured; they follow the same ratio as the ones that were:

```bash
kubectl -n geass-system set resources deployment geass-controller-manager \
  --limits=memory=2Gi --requests=memory=256Mi
kubectl -n geass-system set env deployment geass-controller-manager GOMEMLIMIT=1600MiB
```

:::caution
The installer applies the operator's manifest again every time it updates Geass, which puts these back to what the release ships. After an update, set them again, and check them with `kubectl -n geass-system get deploy geass-controller-manager -o jsonpath='{.spec.template.spec.containers[0].resources}'`.
:::

### Time: what a restart costs

The operator checks every service when it starts, and each of its controllers does one thing at a time. The measurement put that at about **9 seconds per 1,000 services**: 33 seconds at 4,000, 67 at 8,000 and 87 at 10,000, with a little more per service as the cluster grows. While it does, a change you make waits behind the services in front of it in the queue: a deploy you ask for during the first minute of a restart starts when the queue reaches it. That is the cost of an update of Geass too, because an update restarts the operator.

Geass sets no concurrency for its controllers, so each reconciles one object at a time, and raising it is not something to do on a hunch. Two services in one project share namespaces, secrets and policies, and two reconciles that change them at the same moment conflict. A reconcile that fails several times in a row ends in **Provisioning failed after repeated errors**, which pauses the service until someone edits it. One at a time is slower, and cannot do that.

The alerts `GeassWorkqueueBacklog` (more than 25 items waiting for fifteen minutes) and `GeassSlowReconciles` watch for an operator that is not getting through its work.

### The first deploy

Creating a very large number of services at once, as you would when you restore a cluster or move into Geass in bulk, is where the first measurement found a cost that grew with the size of the cluster. Before a service is created the operator checks that the nodes have room for it, and it did that by reading every pod in the cluster and adding up what they ask for. At 10,000 pods that is about a fifth of a second of work for each new service, so filling a cluster cost more for each service than for the one before.

The operator now keeps what the pods ask for, node by node, up to date from their events, and reads that instead. The same sizes, measured on the same machine before and after the change (no memory limit on the operator up to 8,000 services, the earlier runtime limit at 10,000):

| Services | Operator CPU before | Operator CPU after | First deploy settled in, before | After |
| --- | --- | --- | --- | --- |
| 500 | 10 CPU-s (21 ms each) | 7 CPU-s (14 ms each) | 75 s | 72 s |
| 1,000 | 23 CPU-s (23 ms each) | 14 CPU-s (14 ms each) | 100 s | 92 s |
| 2,000 | 58 CPU-s (29 ms each) | 27 CPU-s (13 ms each) | 151 s | 134 s |
| 4,000 | 170 CPU-s (42 ms each) | 48 CPU-s (12 ms each) | 272 s | 202 s |
| 8,000 | 572 CPU-s (72 ms each) | 86 CPU-s (11 ms each) | 637 s | 354 s |
| 10,000 | 1004 CPU-s (100 ms each) | 106 CPU-s (11 ms each) | 940 s | 434 s |

The cost of each service is now level, and a first deploy takes time in proportion to the number of services. How long one new service takes in a cluster that is already running was not measured.

### Etcd, the cluster's database

K3s runs etcd with the upstream default of a **2 GiB** database. At 21 to 34 KiB a service (427 MiB at 20,000), that is tens of thousands of services before the services themselves matter, but events, Helm releases and every other thing in the cluster share it, and a database that reaches its limit stops accepting writes: the whole cluster goes read-only until it is compacted. Check what it holds, and raise the limit before you are near it, in the K3s configuration on each server (`/etc/rancher/k3s/config.yaml`), then restart K3s one server at a time:

```yaml
etcd-arg:
  - quota-backend-bytes=8589934592     # 8 GiB, the most etcd recommends
```

K3s snapshots etcd on its own schedule, and the installer takes one before an update. A larger database makes both slower and larger.

### Kubernetes itself

Upstream Kubernetes is tested to 5,000 nodes, 150,000 pods and 300,000 containers in one cluster, with no more than 110 pods on a node. A Geass cluster on K3s reaches the limits of its control plane machine long before these. The machine is what to watch: K3s runs the API server, etcd and the scheduler on it, as well as Geass's operator, and a large number of services means a large number of watches and writes. This measurement did not measure the API server's or etcd's CPU, so watch the machine's CPU and disk latency (etcd is sensitive to a slow disk), and use [three control plane servers](/docs/operations/high-availability/#run-three-control-plane-servers) and fast disks before you go large.

## What to do at each size

| Services | What to do |
| --- | --- |
| Up to 10,000 | Nothing for the operator. The defaults are generous, and a restart is back to work in about a minute and a half. Use fast disks under the control plane. |
| 10,000 to 20,000 | The operator's limits held 20,000 services in the measurement, with a quarter of the container limit to spare at the heaviest moment. Watch **Geass health**: the alert fires at about 17,000. Raise etcd's limit, run three control plane servers, and size the control plane machine for it: 4 cores, 8 GiB, fast SSD or NVMe storage. You will have at least 91 nodes to run it on. |
| More than 20,000 | Not measured. At 36 KiB a service the operator would reach 1 GiB at about 27,000 services, so raise its limits as above, measure your own cluster, and consider more than one cluster: one operator holds everything of its cluster in memory, and a restart goes over all of it. Geass has no sharding. |

## Check it yourself

The measurement is part of the repository (`hack/loadtest`) and runs against a Kubernetes API server and etcd that it starts itself, on a machine with Go and the Kubernetes test binaries (`make test` fetches them):

```bash
make loadtest LOADTEST_ARGS="-apps 250,500,1000"
```

It prints a table like the one above, builds the operator it measures from the checkout, and removes everything it made. Useful options are `-per-project` (services in each project), `-replicas`, `-gomemlimit 800MiB` (to measure the operator the way the manifest runs it) and `-json` (to keep the results). It takes a few minutes for 500 services, about fifteen for 10,000 and more than half an hour for 20,000. Run it on a machine that nothing else is using: a run on one that is busy with something else is several times slower, and says little about the operator.

## Troubleshooting

**The operator is restarted by Kubernetes with the reason `OOMKilled`.**
It reached its 1 GiB limit. Raise it as above. Check how many services and databases the cluster holds first: `kubectl get geassapps,geassdatabases -A --no-headers | wc -l`.

**Deploys wait for minutes after an update of Geass.**
The operator is checking every service after its restart, and a change waits its turn. The wait is about 10 seconds per 1,000 services. The **Work waiting** chart on **Geass health** shows the queue shrinking.

**The operator restarts again and again, and its log says `Could not wait for Cache to sync`.**
It could not read the cluster within the two minutes it is given. That happens on a very large cluster when the operator is held to less memory than it needs: see [The operator's memory](#the-operators-memory). Raise its limits as shown there, or update to this release, which ships larger ones.

**A write to the cluster fails with `etcdserver: mvcc: database space exceeded`.**
Etcd has reached its limit and the cluster is read-only. On a K3s server, compact and defragment it, then raise the limit as above. This is a K3s procedure; see its documentation for `etcdctl`.

## Related

- [Usage and capacity](/docs/guides/usage-and-capacity/)
- [High availability](/docs/operations/high-availability/)
- [Upgrade Geass](/docs/operations/upgrading/)
- [Logs, metrics and terminal](/docs/guides/observability/#metrics-and-alerts-for-the-operator)
