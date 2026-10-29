---
title: "Noisy-neighbour limits"
description: "Stop organizations on a shared cluster from crowding each other out: pods, local disk, container defaults, and bandwidth, and what Kubernetes cannot limit."
---

On a cluster that several [organizations](/docs/guides/organizations-and-workspaces/) share, their pods run side by
side on the same nodes. The [quota](/docs/guides/organizations-and-workspaces/#what-the-platform-sets-for-an-organization)
of an organization limits the total CPU, memory, storage, and pods that all its projects ask for. The quota does not
stop these problems:

- One container fills the disk of a node.
- One pod takes all the network of a node.
- A container that asks for nothing is free to the scheduler and to the [usage meter](/docs/guides/usage-and-capacity/).

**Noisy-neighbour limits** cover what Kubernetes itself can enforce in addition to the quota. They apply in every
namespace that the pods of an organization run in.

The limits are off until the platform sets them. A Geass without organizations, or whose platform sets no limits, does
not change. Geass writes no object and changes no pod.

## What is limited, and how firmly

| Limit | Held by | Firm? |
| --- | --- | --- |
| **Pods in each namespace** | A ResourceQuota (`pods`) in the namespace. The API server refuses a pod that goes above it. | Enforced by Kubernetes. |
| **Ephemeral storage of each namespace** | The same ResourceQuota (`requests.ephemeral-storage` and `limits.ephemeral-storage`). The API server refuses a pod if the total of its containers goes above it. | Enforced by Kubernetes, at admission. |
| **A container's ephemeral storage** | Its limit, from the default of the LimitRange or its own. The kubelet evicts a pod that writes more than the limits of its containers to its own filesystem, logs, and `emptyDir` volumes. | Enforced by the kubelet, which measures at intervals. A pod can write past its limit for a short time before the kubelet evicts it. |
| **Defaults for a container that asks for nothing** (CPU, memory, ephemeral storage) | A LimitRange in the namespace. The API server copies the default request and limit into each container that does not set them. | Enforced by Kubernetes, for every pod made after you set the limits, by any creator. |
| **The most one container may ask for** (CPU, memory, ephemeral storage) | The same LimitRange (`max`). The API server refuses a pod with a container above it. | Enforced by Kubernetes. |
| **Bandwidth of each pod** (in and out) | The annotations `kubernetes.io/ingress-bandwidth` and `kubernetes.io/egress-bandwidth`. A mutating admission policy puts them on every pod made in the namespace. | **Depends on the network plugin**, and on Kubernetes 1.36 or later. See [Bandwidth](#bandwidth). |
| **An organization's pods on one node** | Nothing. See [What Kubernetes cannot limit](#what-kubernetes-cannot-limit). | Kubernetes cannot enforce this limit. To get machines of its own, an organization uses reserved nodes. |
| **Disk reads and writes (IOPS, throughput)** | Nothing. See [What Kubernetes cannot limit](#what-kubernetes-cannot-limit). | Kubernetes cannot enforce this limit. |

The limits apply to each namespace, because Kubernetes counts for each namespace. Each of these namespaces gets the
full amount of each limit:

- Every environment of every project of the organization.
- The build namespace of the organization (`geass-build-<organization>`).

The total of the organization over all its environments stays its quota (`spec.quota`, including `pods`). Geass
divides the quota between the environments, as it did before.

## Before you start

- Organizations are set up, and projects belong to them (`spec.org`). These limits never apply to a project that has
  no organization. They also never apply to anything in `geass-system`: shared database servers, and builds of
  projects that have no organization.
- Only a platform administrator can set the limits, with `kubectl` (or any tool that writes the resources). The
  dashboard does not show the limits yet.
- **What they do to running workloads.** Geass stops or changes nothing that runs. These pods get the defaults and are
  held to the maximum:
  - A pod made after you set the limits.
  - Every pod of the next rollout of a service.
  - Every restart of a pod.

  Set the maximum above what the services of the organization ask for. If you do not, the services stop rolling out,
  and they give the reason.
- Bandwidth needs Kubernetes 1.36 or later, and a network plugin that acts on the annotations. All other limits work on
  every version that Geass supports.

## Set the limits

The defaults of the platform are in the
[GeassPlatformConfig](/docs/reference/resources/geass-platform-config/), under `spec.tenantLimits.defaults`. They
apply to every organization:

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

The fragment shows only the limits. Do not apply the fragment alone. Patch the limits into the resource:

```bash
kubectl -n geass-system patch geassplatformconfig platform --type merge -p '{"spec":{"tenantLimits":{"defaults":{
  "pods":40,"ephemeralStorage":"20Gi",
  "container":{"defaultRequest":{"cpu":"100m","memory":"128Mi","ephemeralStorage":"100Mi"},
               "defaultLimit":{"cpu":"1","memory":"512Mi","ephemeralStorage":"1Gi"},
               "max":{"cpu":"4","memory":"8Gi","ephemeralStorage":"10Gi"}},
  "bandwidth":{"ingress":"100M","egress":"50M"}}}}}'
```

To give one organization different limits, set `spec.limits` on its
[GeassOrganization](/docs/reference/resources/geass-organization/). Each of `pods`, `ephemeralStorage`, `container`,
and `bandwidth` that the organization sets **replaces the platform's whole**:

- An organization that sets `container` gets none of the container defaults of the platform. Thus, give it all three of
  `defaultRequest`, `defaultLimit`, and `max` that it must have.
- For each field that the organization does not set, the value of the platform applies.
- An organization cannot remove a default. It can only replace the default with a value of its own.

```bash
kubectl -n geass-system patch geassorganization org-1a2b3c4d --type merge \
  -p '{"spec":{"limits":{"pods":200,"bandwidth":{"ingress":"1G","egress":"1G"}}}}'
```

To remove the limits, run
`kubectl -n geass-system patch geassplatformconfig platform --type merge -p '{"spec":{"tenantLimits":null}}'`. For an
organization, run the same command with `{"spec":{"limits":null}}` on the organization. Geass removes the objects that
it wrote, within a moment.

| Field | Meaning |
| --- | --- |
| `pods` | The maximum number of pods, running or waiting, in each namespace. Finished pods do not count. 1 to 100000. |
| `ephemeralStorage` | The maximum ephemeral storage for all the pods of each namespace together: the total of the limits of their containers, and of their requests. It needs a default limit for containers that set none (`container.defaultLimit.ephemeralStorage` or `container.max.ephemeralStorage`). Without one, the API server refuses every such pod. |
| `container.defaultRequest.cpu`, `.memory`, `.ephemeralStorage` | The request that a container gets for a resource that it asks for none of. |
| `container.defaultLimit.cpu`, `.memory`, `.ephemeralStorage` | The limit that a container gets for a resource that it sets no limit on. If you do not set it, the limit is `max`. |
| `container.max.cpu`, `.memory`, `.ephemeralStorage` | The maximum limit that a container can set. The API server refuses a pod with a container above it. |
| `bandwidth.ingress`, `bandwidth.egress` | The maximum that a pod can receive and send, in bits per second (`100M` is 100 megabits), from `1k` to `1P`. |

Every field is optional. If you do not set a field, Geass does not limit it.

### How a container's defaults are worked out

A request is what the scheduler keeps for a container, and what the quota and the usage meter count. A limit is the
maximum that the container can use. Past its limit, the container gets less CPU, it stops because of memory, or the
kubelet evicts its pod because of disk.

- A resource with a `max` or a `defaultLimit` gives every container a limit for that resource. A container with no
  request for the resource gets the `defaultRequest`. If there is no `defaultRequest`, the container gets a small
  request: 50m CPU, 64Mi memory, and 64Mi ephemeral storage, or the limit if the limit is lower.
- Geass writes this request to prevent a problem. Without it, Kubernetes itself makes the request equal to the limit.
  Then a container that asks for nothing asks for the maximum that it can have. A node keeps all of that amount for
  the container, and the usage meter bills all of it.
- `defaultRequest` alone gives a request and no limit.
- Geass services already set their own CPU and memory (100m and 128Mi, or what the service asks for). Thus, the
  container defaults mostly apply where Geass does not write the resources:
  - Builds.
  - The pods of the database operators.
  - Init containers and sidecars.
  - The ephemeral storage of every container.
- A service can set a request and no limit, with the request above the default limit. When Geass adds the default
  limit, that service is not valid. Kubernetes refuses its pods, and the service tells you. Give the service a limit
  too.

### Builds

The builds of an organization run in its build namespace, which has the same limits. A build asks for nothing itself.
It needs much more than a service, for the few minutes that it runs. Thus, where the limits set a `max`, both containers
of a build get that `max` as their limit, and the default request as their request. A resource with no `max` gets the
defaults, the same as all other containers.

If `ephemeralStorage` is set, each running build counts its `max` ephemeral storage against the total of the
namespace. For example, a total of 20Gi with a maximum of 10Gi runs two builds at a time. A third build waits for
room. The build namespace also keeps its own limit of 20 build pods.

## Bandwidth

The CNI bandwidth plugin reads two annotations of a pod when it sets up the network of the pod on its node:

- `kubernetes.io/ingress-bandwidth`, for what the pod receives.
- `kubernetes.io/egress-bandwidth`, for what the pod sends.

The plugin then shapes the traffic of the pod with a queue on its interface. Geass writes the values on each namespace
(`geass.dev/pod-ingress-bandwidth`, `geass.dev/pod-egress-bandwidth`). A mutating admission policy,
`geass-pod-bandwidth`, puts the values on every pod made in that namespace, by any creator. The policy overwrites the
values that the pod set. The limit of a pod is set when the pod starts. A change applies only to pods made after the
change.

Bandwidth limits depend on these conditions:

- **The network plugin.** Only a plugin that runs the CNI `bandwidth` plugin in its chain acts on the annotations.
  With other plugins, the annotations are only text on a pod, and nothing is limited.
  - In the CNI configuration of your cluster, look for `"type": "bandwidth"`. The configuration is in `/etc/cni/net.d`
    on a node. For K3s, it is under `/var/lib/rancher/k3s/agent/etc/cni/net.d`.
  - The built-in flannel configuration of K3s chains the plugin. This was checked on K3s v1.36.4+k3s1:
    `10-flannel.conflist` lists `bandwidth`.
  - You can set up Calico to chain the plugin.
  - Cilium has its own bandwidth manager. You must turn it on, and it does not act on both annotations in every
    release. Check the Cilium documentation.

  **It was checked with Geass on a running K3s (v1.36.4+k3s1, one node, a Docker container on an Ubuntu 24.04 host):**
  - The pods made in a limited namespace had both annotations.
  - The node had a token bucket of 10 Mbit on the interface of the pod.
  - A 300 MB download into a pod limited to 10M took 97 seconds. The same download into a pod without the annotations
    took 0.6 seconds.

  **The plugin's default burst is large, about 193 MB there**. Thus, a pod can move that much data at full speed
  before the rate has an effect. A 20 MB download into the limited pod was not slower at all. Read the limit as a rate
  for continuous traffic, not as a limit on a burst.

  The policy needs a few seconds to become active when Geass first makes it. A pod that starts in those seconds has no
  annotations, and a pod keeps the annotations that it had when it started. Confirm all of this on your own cluster.
  For example, use a pod that runs `iperf3`, or a large download, in a limited namespace.
- **Kubernetes 1.36 or later**, for mutating admission policies. On an older cluster, Geass does not install the policy,
  and nothing gives pods the annotations. Each project of the organization reports this on its `TenantLimitsApplied`
  condition.
- **A policy that fails lets the pod through without the limit.** The failure policy is `Ignore`, so that a fault in
  the policy cannot stop every pod of every namespace that Geass manages.
- The limit counts all the traffic of a pod: to other pods of the cluster and to the internet. Traffic that comes in
  through the ingress of the cluster counts against the pod that receives it.

## What Kubernetes cannot limit

**An organization's pods on one node.** Kubernetes has no limit on the pods of one tenant on one node. The limit that
a node has is the `maxPods` of the kubelet (110 by default, K3s included). That limit is the configuration of the node
itself, and everyone on the node shares it.

The quotas above limit the number of pods that an organization has, in each namespace and in total. Thus, they limit
the share of the organization on all the nodes, but not on one node. The spread constraints of the scheduler count
only the pods of the namespace of the incoming pod. Thus, they cannot spread an organization over its namespaces.
Geass already spreads the copies of each service over different machines.

To keep organizations apart on nodes, give an organization its own machines. Tag nodes, reserve the tag, and require
the tag on the organization
([Scaling and placement](/docs/guides/scaling-and-placement/), [node pools](/docs/guides/node-pools/)).

**Disk reads and writes.** Kubernetes has no field for the block I/O of a pod. It has no limit on reads and writes per
second, or on throughput, for the filesystem of the pod or for its volumes. Linux can throttle a cgroup (`io.max`,
with cgroup v2). On each node, you can set up some container runtimes to throttle classes of containers that a pod
annotation selects (for example, the block I/O classes of containerd). That is configuration of the runtime of each
node, which Geass does not own. Nobody has tried any of it with Geass. The I/O of a volume belongs to the storage
system (Longhorn, or the disk of the cloud).

**What a pod uses between its request and its limit.** A request is a reservation, and a limit is a maximum. The node
shares CPU above a request by weight. The node does not keep memory above a request. Thus, a node whose pods use more
than they asked for can run out of memory. The kubelet then evicts pods that use more than they asked for before the
other pods.

## What happens at a limit

- **Kubernetes does not make a pod that goes above a limit.** The API server refuses it. The Deployment, Job, or
  operator that wanted the pod tries again, with a delay that increases. A service that cannot start gives the reason
  on its `Ready` condition:
  - "The environment's resource quota is used up, so this service cannot start", for the quota. The message includes
    what was asked for, used, and allowed.
  - "The organization's limits refuse this service's pods", with what the LimitRange said, for example
    `maximum cpu usage per Container is 4, but limit is 8`.
  - "Kubernetes refuses this service's pods", for a request above the limit that the container got.

  The service waits, and Geass does not stop trying. To let the service start, raise the limits, or change what the
  service asks for.
- **The kubelet evicts a pod that goes above its ephemeral storage limit.** The controller of the pod makes a new pod.
  The event on the pod reads `Pod ephemeral local storage usage exceeds the total limit of containers`, or names the
  container.
- **When you raise or lower a limit**, the change applies to the next pods. If you lower the pods limit below what
  runs, Geass stops nothing. The next pod waits for room.

## The TenantLimitsApplied condition

Each project of an organization with limits has the condition `TenantLimitsApplied`. A project of an organization
without limits does not have the condition.

| Status | Reason | Meaning |
| --- | --- | --- |
| `True` | `Applied` | The limits are in every environment of the project. |
| `False` | `InvalidLimits` | Geass cannot enforce the limits of the platform and the organization together. Causes: a default above the maximum, an amount that is not above zero, an `ephemeralStorage` with no default limit for it, or a bandwidth outside 1k to 1P. The message says which. **Each namespace keeps what it had** until you fix the limits, so that no namespace loses its limits. Geass also does not change the build namespace, and the operator log gives the reason. |
| `False` | `NotApplied` | Geass could not write the limits of an environment. The message names the environment, with what the API server said. The operator tries again, with a delay that increases. The environments are still there and `Ready`. |
| `False` | `BandwidthNotApplied` | All limits are applied except the bandwidth: the cluster has no mutating admission policies. |

```bash
kubectl -n geass-system get geassproject morning-beach \
  -o jsonpath='{.status.conditions[?(@.type=="TenantLimitsApplied")]}{"\n"}'
```

## What happens under the hood

Geass writes these objects in each environment of a project of the organization. It also writes them in the build
namespace of the organization, after the first build has made that namespace.

| Object | Name | What it holds |
| --- | --- | --- |
| ResourceQuota | `geass-tenant-quota` | `pods`, `requests.ephemeral-storage`, and `limits.ephemeral-storage`, for each limit that is set. It is in addition to the `geass-project-quota` of the project and the `geass-build-quota` of the build namespace. Kubernetes holds a pod to every quota of its namespace. |
| LimitRange | `geass-project-defaults` (an environment), `geass-build-defaults` (the build namespace) | One `Container` item with the default request, the default limit, and the maximum. In an environment, it is the same object that gives containers a request for what the quota of the project counts. The reason is that Kubernetes applies the defaults of several LimitRanges in no fixed order. |
| Namespace annotations | `geass.dev/pod-ingress-bandwidth`, `geass.dev/pod-egress-bandwidth` | The bandwidth that each pod of the namespace gets. |
| MutatingAdmissionPolicy and binding | `geass-pod-bandwidth` | Geass installs it the first time that a limit asks for bandwidth, and binds it to the namespaces that Geass manages. Geass never removes it. In a namespace without the annotations, it changes nothing. |

The project controller writes the objects into the environments. The organization controller writes them into the
build namespace. Both controllers follow a change of the defaults of the platform or of the limits of the
organization. If you edit or delete an object by hand, the controller puts it back. Geass removes the objects when
the limits that made them are removed. Geass removes only objects that have the Geass `geass.dev/managed-by` label.

```bash
kubectl describe resourcequota geass-tenant-quota -n morning-beach-production
kubectl describe limitrange geass-project-defaults -n morning-beach-production
kubectl get namespace morning-beach-production -o jsonpath='{.metadata.annotations}{"\n"}'
```

## What has been tested

Tests against fake API clients cover these items:

- The objects that Geass writes, and their content.
- That the objects follow a change, and go away with the setting.
- That Geass writes nothing for a project with no organization, or for a platform that sets nothing.
- The condition.

The admission policy code of the Kubernetes API server (its CEL compiler and JSON patcher) runs the bandwidth policy
on a pod and a namespace. **On a real K3s (v1.36.4+k3s1)**, the API server admitted pods through these objects:

- Geass wrote the quota and the LimitRange.
- A pod that declared nothing got the defaults.
- The API server refused a container above the maximum.
- The pod-count quota refused the pods above it.
- All objects went away when the setting was removed.

The bandwidth was shaped on that cluster, as [Bandwidth](#bandwidth) says. Nobody has seen the kubelet evict a pod
because of its disk.

## Troubleshooting

**A service says "The organization's limits refuse this service's pods".** A container of the service asks for more
than `container.max` allows. Lower what the service asks for, or raise the `max` of the organization.

**A service says "Kubernetes refuses this service's pods" with "must be less than or equal to cpu limit".** The service
sets a request and no limit, and the request is above the default limit. Give the service a limit too, or raise
`defaultLimit`.

**Pods are evicted for ephemeral storage.** The pod wrote more than the limits of its containers to its own disk
(logs, temporary files, `emptyDir`). Give the service a larger ephemeral storage limit of its own, or raise
`defaultLimit.ephemeralStorage`. Data that a service keeps belongs on a volume.

**A build waits with "exceeded quota: geass-tenant-quota".** The build namespace is at its ephemeral storage or pod
total. The build starts when a running build finishes. Or raise the `ephemeralStorage` of the organization.

**Pods are not limited in bandwidth.** Check that the pod has the annotations
(`kubectl get pod <pod> -n <namespace> -o jsonpath='{.metadata.annotations}'`).

- If the pod has them, the network plugin does not act on them.
- If the pod does not have them, check the `TenantLimitsApplied` condition of the project. Also check that the policy
  is installed (`kubectl get mutatingadmissionpolicy geass-pod-bandwidth`).

Pods that were running before you set the limit keep the traffic they had until they are created again.

## Related

- [Organizations and workspaces](/docs/guides/organizations-and-workspaces/)
- [Project limits](/docs/guides/project-limits/)
- [Usage and capacity](/docs/guides/usage-and-capacity/)
- [Scaling and placement](/docs/guides/scaling-and-placement/)
- [Outbound limits](/docs/guides/outbound-limits/), [Sandboxed runtime for organizations](/docs/operations/sandboxed-runtime/), and [Registry isolation for organizations](/docs/operations/registry-isolation/): the other settings for a cluster that organizations share
