---
title: "Sandboxed runtime for organizations"
description: "Run organizations' services, schedules and builds under gVisor or Kata: what the nodes need, how to turn it on, how it fails closed, and its limits."
---

With [organizations](/docs/guides/organizations-and-workspaces/), every organization's pods still run on the same nodes,
and a container shares the node's kernel: code that escapes its container reaches the node, and every other organization's
pods on it. When the organizations of a cluster do not trust each other, run their code under a **sandboxed runtime**
instead: [gVisor](https://gvisor.dev), which gives each pod a kernel of its own in user space, or
[Kata Containers](https://katacontainers.io), which gives each pod a small virtual machine. Kubernetes picks the runtime
by a [RuntimeClass](https://kubernetes.io/docs/concepts/containers/runtime-class/), and Geass puts its name on the pods
of organizations' projects.

It is off until you set it, and while it is off no pod changes.

:::caution[Not run with a real sandbox]
Geass's tests check the pods it writes, and that it waits when the RuntimeClass is missing, against a fake API server.
gVisor and Kata themselves have not been run with Geass: neither the services nor the Kaniko builds have been seen to
work under them. Try it on one cluster, with your own services, before you rely on it.
:::

## What runs in the sandbox

With `spec.tenancy.sandbox` set on the [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/), the pods
Geass makes for a project that belongs to an organization (its `spec.org` is set) get `runtimeClassName`:

| Pods | Sandboxed |
| --- | --- |
| A service's copies (its Deployment), including the services of [pull request environments](/docs/guides/pull-request-environments/) | Yes |
| The pods of a [function's schedules](/docs/guides/functions/) (CronJobs) | Yes |
| [Builds](/docs/guides/github-and-builds/) (the Kaniko Job in the organization's `geass-build-<organization>` namespace), which run the repository's Dockerfile | Yes |
| Everything of a project that belongs to no organization | No: those are the platform's own |
| Database servers, object storage, backups and restores, even of an organization's project | No: see [What it does not cover](#what-it-does-not-cover) |
| Geass's own pods (the operator, the network policy check) and the add-ons | No |

A project that names an organization is sandboxed whether or not organizations are turned on for sign-in
(`spec.auth.tenancy.enabled`), so that switching sign-in back to passwords for a while never takes the sandbox away. A
project that a pod names but that cannot be found is sandboxed too.

## Prepare the nodes

Install the runtime on **every node an organization's pods can run on**, which on a shared cluster is every worker, and
make the RuntimeClass. A node without the runtime cannot start a sandboxed pod: the pod stays `ContainerCreating` with a
`FailedCreatePodSandBox` event (`no runtime for "runsc" is configured`). Nodes reserved for one organization can go
without it only with `skipOnReservedNodes` and only while its rule holds (see [Reserved nodes](#reserved-nodes)): when it
stops holding, that organization's pods are sandboxed and cannot start there.

These steps are for gVisor on K3s, which runs its own containerd. They follow the gVisor and K3s documentation and were
not run here; check them against the [gVisor installation guide](https://gvisor.dev/docs/user_guide/install/) and K3s's
[containerd configuration](https://docs.k3s.io/advanced#configuring-containerd) for your releases.

1. **Install `runsc` and its containerd shim** on each node:

   ```bash title="Install gVisor"
   ARCH=$(uname -m)
   URL=https://storage.googleapis.com/gvisor/releases/release/latest/${ARCH}
   wget ${URL}/runsc ${URL}/runsc.sha512 ${URL}/containerd-shim-runsc-v1 ${URL}/containerd-shim-runsc-v1.sha512
   sha512sum -c runsc.sha512 -c containerd-shim-runsc-v1.sha512
   chmod a+rx runsc containerd-shim-runsc-v1
   sudo mv runsc containerd-shim-runsc-v1 /usr/local/bin
   ```

2. **Tell K3s's containerd about it.** K3s writes its containerd configuration itself on every start, from a template
   you can extend. Recent K3s releases ship containerd 2, whose file is `config-v3.toml.tmpl`; an older one with
   containerd 1.7 reads `config.toml.tmpl`. See which K3s generated: `ls /var/lib/rancher/k3s/agent/etc/containerd/`.

   ```toml title="/var/lib/rancher/k3s/agent/etc/containerd/config-v3.toml.tmpl (containerd 2)"
   {{ template "base" . }}

   [plugins.'io.containerd.cri.v1.runtime'.containerd.runtimes.runsc]
     runtime_type = "io.containerd.runsc.v1"
   ```

   ```toml title="/var/lib/rancher/k3s/agent/etc/containerd/config.toml.tmpl (containerd 1.7)"
   {{ template "base" . }}

   [plugins."io.containerd.grpc.v1.cri".containerd.runtimes.runsc]
     runtime_type = "io.containerd.runsc.v1"
   ```

   Then restart K3s (`sudo systemctl restart k3s-agent` on a worker, `k3s` on a server) and check that the generated
   `config-v3.toml` (or `config.toml`) has the `runsc` runtime.

3. **Make the RuntimeClass** once for the cluster. Its name is what Geass is told; its `handler` is the runtime's name in
   containerd's configuration.

   ```yaml title="runtimeclass-gvisor.yaml"
   apiVersion: node.k8s.io/v1
   kind: RuntimeClass
   metadata:
     name: gvisor
   handler: runsc
   # Only if some nodes have no gVisor: keep sandboxed pods to the nodes that carry this label.
   # scheduling:
   #   nodeSelector:
   #     sandbox.example.com/gvisor: "true"
   # Only to account for the sandbox's own memory and CPU. Kubernetes adds it to each pod, and to what the
   # namespace's quota counts.
   # overhead:
   #   podFixed:
   #     cpu: 50m
   #     memory: 64Mi
   ```

   ```bash
   kubectl apply -f runtimeclass-gvisor.yaml
   ```

4. **Check one pod by hand** before Geass sends every organization's code there:

   ```bash title="A pod under gVisor"
   kubectl run sandbox-check --rm -it --restart=Never --image=busybox \
     --overrides='{"apiVersion":"v1","spec":{"runtimeClassName":"gvisor"}}' -- dmesg
   ```

   gVisor's own boot messages (`Starting gVisor...`) mean the pod ran in the sandbox.

For Kata Containers, install it with its `kata-deploy` and name one of the RuntimeClasses it makes, such as `kata-qemu`.
Kata runs a virtual machine per pod, so the nodes need hardware virtualization: bare metal, or instances that allow
nested virtualization.

## Turn it on

```yaml title="platform.yaml (spec.tenancy)"
spec:
  tenancy:
    sandbox:
      runtimeClassName: gvisor
      skipOnReservedNodes: false
```

```bash
kubectl -n geass-system patch geassplatformconfig platform --type merge \
  -p '{"spec":{"tenancy":{"sandbox":{"runtimeClassName":"gvisor"}}}}'
```

There is no form for it in the dashboard.

What happens to what is already running:

- **Services are rolled at once.** The setting reaches every service, and an organization's service gets a pod template
  with the RuntimeClass, so its Deployment replaces its copies the way it rolls out any change: a few at a time, and
  never more than a quarter of them down at once, so a service with one to three copies starts a new copy before it
  stops an old one. All organizations' services begin at the same time, so a cluster that is nearly full needs room for
  the extra copies while they roll; until there is, the copies not yet replaced keep running. Turning the setting off,
  or changing the class, rolls them again the same way. A service whose rollout is paused, or that is not deployed,
  takes it when it next deploys.
- **Builds take it from their next run.** A build that is running finishes as it started.
- **Schedules** are rewritten with the service, and their next run is sandboxed.

## When the RuntimeClass is missing

Geass never starts an organization's code without the sandbox. While the cluster has no RuntimeClass of that name:

- **A service waits.** Its Deployment is not written: a new service gets none, and one that was already running keeps
  exactly the pods it had. The service is not ready, with the message
  `Waiting for the sandboxed runtime: RuntimeClass "gvisor" is not in the cluster, ...`, its `Sandboxed` condition is
  `False` with the reason `RuntimeClassMissing`, and it gets one `Warning` event, `SandboxRuntimeClassMissing`, when the
  wait begins. The wait does not count against the retry limit, so the service is not given up on.
- **A build waits** in `Pending`, with the same condition and event, before anything of it is made or copied into the
  organization's namespace. The service that waits for the build shows the build's message.
- **When the class appears**, services go on at once (the operator watches RuntimeClasses) and builds within half a minute.

A RuntimeClass that is deleted later does the same to what changes next. Kubernetes itself never runs a pod whose
RuntimeClass does not exist with another runtime (the API server refuses it, or the kubelet fails it), so a copy that a
Deployment replaces in the meantime is not started without the sandbox either.

```bash title="See what waits"
kubectl -n geass-system get geassapps,geassbuilds -o json | jq -r '.items[]
  | select(any(.status.conditions[]?; .type == "Sandboxed" and .reason == "RuntimeClassMissing")) | .metadata.name'
kubectl -n geass-system get events --field-selector reason=SandboxRuntimeClassMissing
```

## Reserved nodes

An organization that has nodes of its own (a node pool whose tag is reserved, and that the organization requires of all
its projects; see [Scaling and placement](/docs/guides/scaling-and-placement/)) shares those nodes with nobody, and can
run there without the sandbox. Set `skipOnReservedNodes: true` for that. A pod then runs without the sandbox only when
all of these hold when Geass writes it:

1. Its node selector asks for a tag, so it can only be scheduled on nodes that carry the tag.
2. Its organization requires that tag of all its projects (`spec.requiredTags` of the
   [GeassOrganization](/docs/reference/resources/geass-organization/), which only a platform administrator sets).
3. No other organization requires the same tag.
4. Every node that carries the tag is reserved for it (tainted `tag.geass.dev/<tag>`, `NoSchedule` or `NoExecute`), so
   nothing that does not ask for the tag is scheduled there, and there is at least one such node.

Anything less certain is sandboxed. The service's `Sandboxed` condition says which: `True` with `RuntimeClass`, or
`False` with `ReservedNodes` and the tag. When one of the four stops being true (another organization comes to require
the tag, a node with the tag is no longer reserved, the organization stops requiring it), the operator hears of it and
rolls the organization's services into the sandbox. If the RuntimeClass is missing it holds them instead: their copies
keep running as they are, and none is started without the sandbox. Builds take it from their next run.

A reserved node without the sandbox is a node that its organization's code can take over, like any node without it:
whatever else runs there, such as the add-ons' DaemonSets, is exposed to that organization.

## What it does not cover

- **Database servers, object storage, backups and restores.** They run images Geass chose, not the organization's code,
  but they run what the organization sends them, and a database login with enough rights can run programs through it
  (Postgres's `COPY ... PROGRAM`, untrusted languages). They are not sandboxed yet.
- **Other organizations asking for a reserved tag themselves.** A service's `runOn` tags and a project's own required
  tags can name any tag, including one reserved for another organization, and that work then runs on the other
  organization's nodes. It is sandboxed itself (its organization does not require the tag), but it sits beside the
  owner's pods, which with `skipOnReservedNodes` are not.
- **Capacity.** The check that a new service fits counts the nodes of its region and tags, not the RuntimeClass's
  `scheduling`, and a RuntimeClass `overhead` is added to every pod and to what the project's quota counts.
- **Compatibility.** gVisor implements most, not all, of Linux: some system calls and `/proc` files behave differently,
  and I/O-heavy work runs slower. A service that does not start under it shows the usual
  `The service cannot start: …` or stays starting; read its pod's events.

## Related

- [Organizations and workspaces](/docs/guides/organizations-and-workspaces/)
- [Security](/docs/operations/security/)
- [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/)
- [Scaling and placement](/docs/guides/scaling-and-placement/)
