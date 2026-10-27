---
title: "Noisy-neighbour limits"
description: "Keep organizations sharing a cluster from crowding each other out: pods, local disk, container defaults and bandwidth, and what Kubernetes cannot limit."
---

On a cluster that several [organizations](/docs/guides/organizations-and-workspaces/) share, their pods run side by side on the same nodes. An organization's [quota](/docs/guides/organizations-and-workspaces/#what-the-platform-sets-for-an-organization) caps the CPU, memory, storage and pods all its projects ask for together. It does not stop one container from filling a node's disk, one pod from taking a node's network, or a container that asks for nothing from being free to the scheduler and to the [usage meter](/docs/guides/usage-and-capacity/). **Noisy-neighbour limits** cover what Kubernetes itself can hold beyond the quota, in every namespace an organization's pods run in.

They are off until the platform sets them. A Geass without organizations, or whose platform sets none, is unchanged: no object is written and no pod is touched.

## What is limited, and how firmly

| Limit | Held by | Firm? |
| --- | --- | --- |
| **Pods in each namespace** | A ResourceQuota (`pods`) in the namespace. The API server refuses the pod that would pass it. | Enforced by Kubernetes. |
| **Ephemeral storage of each namespace** | The same ResourceQuota (`requests.ephemeral-storage` and `limits.ephemeral-storage`). A pod whose containers' total would pass it is refused. | Enforced by Kubernetes, at admission. |
| **A container's ephemeral storage** | Its limit, from the LimitRange's default or its own. The kubelet evicts a pod that writes more than its containers' limits to its own filesystem, logs and `emptyDir` volumes. | Enforced by the kubelet, which measures on a period: a pod can briefly write past its limit before it is evicted. |
| **Defaults for a container that asks for nothing** (CPU, memory, ephemeral storage) | A LimitRange in the namespace. The API server copies the default request and limit into each container that leaves them out. | Enforced by Kubernetes, for every pod made after the limits are set, whoever makes it. |
| **The most one container may ask for** (CPU, memory, ephemeral storage) | The same LimitRange (`max`). A pod with a container above it is refused. | Enforced by Kubernetes. |
| **Bandwidth of each pod** (in and out) | The annotations `kubernetes.io/ingress-bandwidth` and `kubernetes.io/egress-bandwidth`, which a mutating admission policy puts on every pod made in the namespace. | **Depends on the network plugin**, and on Kubernetes 1.36 or later. See [Bandwidth](#bandwidth). |
| **An organization's pods on one node** | Nothing. See [What Kubernetes cannot limit](#what-kubernetes-cannot-limit). | Not enforceable by Kubernetes. Reserved nodes are how an organization gets machines of its own. |
| **Disk reads and writes (IOPS, throughput)** | Nothing. See [What Kubernetes cannot limit](#what-kubernetes-cannot-limit). | Not enforceable by Kubernetes. |

The limits are per namespace, because Kubernetes counts per namespace: every environment of every project of the organization, and its build namespace (`geass-build-<organization>`), gets the whole of each limit. The organization's total over all its environments stays its quota (`spec.quota`, including `pods`), which Geass shares out between them as it always has.

## Before you start

- Organizations are set up, and projects are given to them (`spec.org`). A project of no organization is never limited by these, and neither is anything in `geass-system`: shared database servers, and builds of projects of no organization.
- Only a platform administrator can set them, with `kubectl` (or anything that writes the resources). The dashboard does not show them yet.
- **What they do to running workloads.** Nothing that runs is stopped or changed. A pod made after the limits are set, which includes every pod of a service's next rollout and every restart of a pod, gets the defaults and is held to the most. Set the most above what the organization's services ask for, or they stop rolling out (they say why).
- Bandwidth needs Kubernetes 1.36 or later and a network plugin that acts on the annotations. Everything else works on any version Geass supports.

## Set the limits

The platform's defaults are in the [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/), under `spec.tenantLimits.defaults`. They apply to every organization:

```yaml title="platform-tenant-limits.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassPlatformConfig
metadata:
  name: platform
  namespace: geass-system
spec:
  tenantLimits:
    defaults:
      pods: 40                  # in each namespace, running or waiting
      ephemeralStorage: 20Gi    # in each namespace: the total of the containers' limits
      container:
        defaultRequest:         # what a container that asks for none is given as its request
          cpu: 100m
          memory: 128Mi
          ephemeralStorage: 100Mi
        defaultLimit:           # what a container that sets no limit is given
          cpu: "1"
          memory: 512Mi
          ephemeralStorage: 1Gi
        max:                    # the most a container may set
          cpu: "4"
          memory: 8Gi
          ephemeralStorage: 10Gi
      bandwidth:                # bits per second, each pod
        ingress: 100M
        egress: 50M
```

The fragment shows only the limits. Patch them in rather than applying it alone:

```bash
kubectl -n geass-system patch geassplatformconfig platform --type merge -p '{"spec":{"tenantLimits":{"defaults":{
  "pods":40,"ephemeralStorage":"20Gi",
  "container":{"defaultRequest":{"cpu":"100m","memory":"128Mi","ephemeralStorage":"100Mi"},
               "defaultLimit":{"cpu":"1","memory":"512Mi","ephemeralStorage":"1Gi"},
               "max":{"cpu":"4","memory":"8Gi","ephemeralStorage":"10Gi"}},
  "bandwidth":{"ingress":"100M","egress":"50M"}}}}}'
```

To give one organization other limits, set `spec.limits` on its [GeassOrganization](/docs/reference/resources/geass-organization/). Each of `pods`, `ephemeralStorage`, `container` and `bandwidth` that it sets **replaces the platform's whole**: an organization that sets `container` gets none of the platform's container defaults, so give it all three of `defaultRequest`, `defaultLimit` and `max` that it should have. What it leaves out is the platform's. An organization cannot take a default away, only replace it with a value of its own.

```bash
kubectl -n geass-system patch geassorganization org-1a2b3c4d --type merge \
  -p '{"spec":{"limits":{"pods":200,"bandwidth":{"ingress":"1G","egress":"1G"}}}}'
```

To remove them: `kubectl -n geass-system patch geassplatformconfig platform --type merge -p '{"spec":{"tenantLimits":null}}'`, and the same with `{"spec":{"limits":null}}` on an organization. Geass takes the objects it wrote away again, within a moment.

| Field | Meaning |
| --- | --- |
| `pods` | The most pods each namespace may have running or waiting. Finished pods are not counted. 1 to 100000. |
| `ephemeralStorage` | The most ephemeral storage the pods of each namespace may have together: the total of their containers' limits, and of their requests. It needs a default limit for containers that set none (`container.defaultLimit.ephemeralStorage` or `container.max.ephemeralStorage`), or every such pod would be refused. |
| `container.defaultRequest.cpu`, `.memory`, `.ephemeralStorage` | The request a container is given for a resource it asks for none of. |
| `container.defaultLimit.cpu`, `.memory`, `.ephemeralStorage` | The limit a container is given for a resource it sets no limit on. Without it, that is `max`. |
| `container.max.cpu`, `.memory`, `.ephemeralStorage` | The most a container may set as its limit. A pod with a container above it is refused. |
| `bandwidth.ingress`, `bandwidth.egress` | The most a pod may receive and send, in bits per second (`100M` is 100 megabits), from `1k` to `1P`. |

Every field is optional, and one that is left out is not limited.

### How a container's defaults are worked out

A request is what the scheduler sets aside for a container and what the quota and the usage meter count. A limit is the most it may use: past it its CPU is slowed, it is stopped for memory, or its pod is evicted for disk.

- A resource with a `max` or a `defaultLimit` gives every container a limit for it. A container with no request for it is given the `defaultRequest`, or, if there is none, a small one: 50m CPU, 64Mi memory and 64Mi ephemeral storage, or the limit if that is lower. Kubernetes itself would make the request the limit, so that a container that asks for nothing would ask for the most it may have, be reserved for all of it on a node and be billed for all of it. Geass writes the request out to avoid that.
- `defaultRequest` alone gives a request and no limit.
- Geass's services already set their own CPU and memory (100m and 128Mi, or what the service asks for), so the container defaults mostly reach what Geass does not write the resources of: builds, the pods of the database operators, init containers and sidecars, and every container's ephemeral storage.
- A service that sets a request but no limit, above the default limit, is not valid once the default limit is added. Kubernetes refuses its pods, and the service says so. Give it a limit too.

### Builds

An organization's builds run in its build namespace, which holds the same limits. A build asks for nothing itself and needs far more than a service for the few minutes it runs, so where the limits set a `max`, both containers of a build are given it as their limit, and the default request as their request. A resource with no `max` is left to the defaults like anything else. With `ephemeralStorage` set, each running build counts its `max` ephemeral storage against the namespace's total, so a total of 20Gi with a most of 10Gi runs two builds at a time; a third waits for room. The build namespace keeps its own cap of 20 build pods besides.

## Bandwidth

The CNI bandwidth plugin reads two annotations of a pod when the pod's network is set up on its node, and shapes the pod's traffic with a queue on its interface: `kubernetes.io/ingress-bandwidth` for what it receives, `kubernetes.io/egress-bandwidth` for what it sends. Geass writes the values on each namespace (`geass.dev/pod-ingress-bandwidth`, `geass.dev/pod-egress-bandwidth`), and a mutating admission policy, `geass-pod-bandwidth`, puts them on every pod made there, whoever makes it, over whatever the pod said. A pod's limit is set when it starts: a change applies to pods made after it.

What this depends on:

- **The network plugin.** Only a plugin that runs the CNI `bandwidth` plugin in its chain acts on the annotations. Elsewhere they are words on a pod and nothing is limited. Check your cluster's CNI configuration (`/etc/cni/net.d` on a node, or K3s's own under `/var/lib/rancher/k3s/agent/etc/cni/net.d`) for `"type": "bandwidth"`. K3s's built-in flannel configuration chains it (checked on K3s v1.36.4+k3s1: `10-flannel.conflist` lists `bandwidth`), and Calico can be set up to; Cilium has a bandwidth manager of its own, which has to be turned on and does not act on both annotations in every release, so check its documentation. **It was checked with Geass on a running K3s (v1.36.4+k3s1, one node, a Docker container on an Ubuntu 24.04 host):** the pods made in a limited namespace carried both annotations, the node had a token bucket of 10 Mbit on the pod's interface, and a 300 MB download into a pod limited to 10M took 97 seconds where the same download into a pod without the annotations took 0.6. **The plugin's default burst is large, about 193 MB there**, so a pod can move that much at full speed before the rate bites: a 20 MB download into the limited pod was not slowed at all. Read the limit as a rate for sustained traffic, not a cap on a burst. The policy needs a few seconds to become active when it is first made: a pod created in those seconds has no annotations, and a pod keeps whatever it had when it started. Confirm all of this on your own cluster, for example with a pod running `iperf3` or a large download in a limited namespace.
- **Kubernetes 1.36 or later**, for mutating admission policies. On an older cluster the policy is not installed, nothing gives pods the annotations, and each project of the organization says so on its `TenantLimitsApplied` condition.
- **A policy that fails lets the pod through without the limit.** Its failure policy is `Ignore`, so that a fault in it cannot stop every pod of every namespace Geass manages.
- The limit counts all of a pod's traffic, to other pods of the cluster as well as to the internet, and traffic that comes in through the cluster's ingress counts against the receiving pod.

## What Kubernetes cannot limit

**An organization's pods on one node.** Kubernetes has no cap on the pods of one tenant on one node. The cap a node has is the kubelet's `maxPods` (110 by default, K3s included), which is the node's own configuration and is shared by everyone on it. The quotas above bound how many pods an organization has, in each namespace and in total, and so its share of all the nodes, but not of one of them. The scheduler's spread constraints count only the pods of the incoming pod's own namespace, so they cannot spread an organization over its namespaces; Geass already spreads the copies of each service over different machines. What does keep organizations apart on nodes is to give an organization machines of its own: tag nodes, reserve the tag and require it on the organization ([Scaling and placement](/docs/guides/scaling-and-placement/), [node pools](/docs/guides/node-pools/)).

**Disk reads and writes.** Kubernetes has no field for a pod's block I/O: no limit on reads and writes per second or on throughput, for its own filesystem or for its volumes. Linux can throttle a cgroup (`io.max`, with cgroup v2), and some container runtimes can be set up, on each node, to throttle classes of containers that a pod annotation picks (containerd's block I/O classes, for example). That is configuration of each node's runtime, which Geass does not own, and nothing of it has been tried with Geass. A volume's own I/O is the storage system's (Longhorn's, or the cloud's disk).

**What a pod uses between its request and its limit.** A request is a reservation and a limit a ceiling. CPU above a request is shared on the node by weight. Memory above a request is not set aside, so a node whose pods use more than they asked for can run out, and the kubelet then evicts pods that use more than they asked for before the others.

## What happens at a limit

- **A pod over a limit is not made.** The API server refuses it, and the Deployment, Job or operator that wanted it tries again with a growing delay. A service that cannot start says why on its `Ready` condition: "The environment's resource quota is used up, so this service cannot start" for the quota, with what was asked for, used and allowed; "The organization's limits refuse this service's pods" with what the LimitRange said, such as `maximum cpu usage per Container is 4, but limit is 8`; or "Kubernetes refuses this service's pods" for a request above the limit it was given. It waits, and is not given up on: raising the limits, or changing what the service asks for, lets it start.
- **A pod over its ephemeral storage limit is evicted** by the kubelet, and its controller makes a new one. The event on the pod reads `Pod ephemeral local storage usage exceeds the total limit of containers`, or names the container.
- **Raising or lowering a limit** changes what the next pods get. Lowering the pods cap below what runs stops nothing; the next pod waits for room.

## The TenantLimitsApplied condition

Each project of an organization with limits carries the condition `TenantLimitsApplied`. A project of an organization without them has none.

| Status | Reason | Meaning |
| --- | --- | --- |
| `True` | `Applied` | The limits are in every environment of the project. |
| `False` | `InvalidLimits` | The limits, the platform's and the organization's put together, cannot be held: a default above the most, an amount that is not above zero, an `ephemeralStorage` with no default limit for it, a bandwidth outside 1k to 1P. The message says which. **Each namespace keeps what it had** until they are fixed, rather than losing what held it. The build namespace is left as it was too, and the operator's log says why. |
| `False` | `NotApplied` | An environment's limits could not be written; the message names it, with what the API server said. The operator tries again with a growing delay. The environments are still there and `Ready`. |
| `False` | `BandwidthNotApplied` | Everything is applied but the bandwidth: the cluster has no mutating admission policies. |

```bash
kubectl -n geass-system get geassproject morning-beach \
  -o jsonpath='{.status.conditions[?(@.type=="TenantLimitsApplied")]}{"\n"}'
```

## What happens under the hood

In each environment of a project of the organization, and in its build namespace once its first build has made it:

| Object | Name | What it holds |
| --- | --- | --- |
| ResourceQuota | `geass-tenant-quota` | `pods`, `requests.ephemeral-storage` and `limits.ephemeral-storage`, for each limit that is set. It sits beside the project's own `geass-project-quota` and the build namespace's `geass-build-quota`; Kubernetes holds a pod to every quota of its namespace. |
| LimitRange | `geass-project-defaults` (an environment), `geass-build-defaults` (the build namespace) | One `Container` item with the default request, the default limit and the most. In an environment it is the same object that gives containers a request for what the project's own quota counts, because Kubernetes applies the defaults of several LimitRanges in no set order. |
| Namespace annotations | `geass.dev/pod-ingress-bandwidth`, `geass.dev/pod-egress-bandwidth` | The bandwidth each pod of the namespace is given. |
| MutatingAdmissionPolicy and binding | `geass-pod-bandwidth` | Installed the first time a bandwidth is asked for, bound to the namespaces Geass manages, and never removed: in a namespace without the annotations it changes nothing. |

The project controller writes them into the environments and the organization controller into the build namespace, and both follow a change of the platform's defaults or of the organization's limits. One that is edited or deleted by hand is put back. They are removed when the limits that made them go, and only objects carrying Geass's `geass.dev/managed-by` label are ever removed.

```bash
kubectl describe resourcequota geass-tenant-quota -n morning-beach-production
kubectl describe limitrange geass-project-defaults -n morning-beach-production
kubectl get namespace morning-beach-production -o jsonpath='{.metadata.annotations}{"\n"}'
```

## What has been tested

The objects Geass writes, their content, that they follow a change and go with the setting, that nothing is written for a project of no organization or for a platform that sets nothing, and the condition, are tested against fake API clients. The bandwidth policy is run by the Kubernetes API server's own code for admission policies (its CEL compiler and JSON patcher) on a pod and a namespace. **Nothing of it has run on a real cluster yet**: neither a real API server admitting a pod through the LimitRange, the quota and the policy, nor a network plugin shaping a pod's traffic, nor the kubelet evicting a pod for its disk.

## Troubleshooting

**A service says "The organization's limits refuse this service's pods".** A container of it asks for more than `container.max` allows. Lower what the service asks for, or raise the organization's `max`.

**A service says "Kubernetes refuses this service's pods" with "must be less than or equal to cpu limit".** It sets a request and no limit, and the request is above the default limit. Give the service a limit as well, or raise `defaultLimit`.

**Pods are evicted for ephemeral storage.** The pod wrote more than its containers' limits to its own disk (logs, temporary files, `emptyDir`). Give the service a larger ephemeral storage limit of its own, or raise `defaultLimit.ephemeralStorage`; data a service keeps belongs on a volume.

**A build waits with "exceeded quota: geass-tenant-quota".** The build namespace is at its ephemeral storage or pod total. The build starts when one that runs finishes, or raise the organization's `ephemeralStorage`.

**Pods are not limited in bandwidth.** Check that the pod carries the annotations (`kubectl get pod <pod> -n <namespace> -o jsonpath='{.metadata.annotations}'`). If it does, the network plugin does not act on them. If it does not, check the project's `TenantLimitsApplied` condition and that the policy is installed (`kubectl get mutatingadmissionpolicy geass-pod-bandwidth`). Pods that were running before the limit was set keep the traffic they had until they are made again.

## Related

- [Organizations and workspaces](/docs/guides/organizations-and-workspaces/)
- [Project limits](/docs/guides/project-limits/)
- [Usage and capacity](/docs/guides/usage-and-capacity/)
- [Scaling and placement](/docs/guides/scaling-and-placement/)
- [Outbound limits](/docs/guides/outbound-limits/), [Sandboxed runtime for organizations](/docs/operations/sandboxed-runtime/) and [Registry isolation for organizations](/docs/operations/registry-isolation/), the other settings for a cluster that organizations share
