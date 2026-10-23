---
title: "Network isolation"
description: "Each project's services are private by default. Let one project call another, open a project to the whole cluster, and check the rules with kubectl."
---

In a cluster with no rules, any service can call any other, so one project's service could reach another project's database or read its metrics. Geass closes that by default. Each project is private: only its own services, Geass itself and visitors who arrive through your domains can reach them. You choose which other projects may reach a project, or open it to everything. A project is also kept away from the servers the cluster runs on, if it was created in the dashboard or the API.

## Before you start

- Only admins can change network settings: dashboard admins, and workspace API tokens with the admin role. Project tokens, observer tokens and viewer tokens are refused.
- Isolation is enforced by Kubernetes network policies. K3s enforces them with its built-in network policy controller, and Geass does not turn that off. On a cluster whose network plugin ignores network policies, Geass still writes the rules, but nothing enforces them. Geass checks for that, and says so on the **Usage** page (see [Check that the cluster enforces policies](#check-that-the-cluster-enforces-policies)).

## What is isolated by default

A project's services and databases accept connections only from:

- the project itself, in **every** one of its environments, pull request environments included;
- the platform: the ingress controller, the Geass operator and dashboard, Prometheus, the Postgres operator and the scale-to-zero proxy (see the table below);
- the projects you name in its allow list.

Everything else is dropped, and that includes every other project. The rule limits who can **reach** a project. A project's services can always reach other services, other projects that allow them, the cluster's DNS and the internet. Answers to connections a project starts come back as normal.

## What is always allowed

These can always reach a project's services, whatever you choose, because Geass or your visitors need them:

| Source | Why |
| --- | --- |
| Namespace `kube-system` | The ingress controller (Traefik), which forwards visitors who arrive through your domains. |
| Namespace `geass-system` | The operator and dashboard, and the Cloudflare tunnel when you use one. |
| Namespace `monitoring` | Prometheus, which scrapes service metrics. |
| Namespace `cnpg-system` | The Postgres operator, which manages every Postgres database. |
| Namespace `moco-system` | The MySQL operator, which manages every high availability MySQL database. |
| Namespace `mariadb-system` | The MariaDB operator, which manages every MariaDB database. |
| Namespace `keda` | The proxy of the serverless add-on, which holds a request while a [sleeping service](/docs/guides/scale-to-zero/) starts and passes it on. The namespace exists only when the add-on is installed, and the rule is there either way. |
| The project's own namespaces | A service calling its database, or the production environment calling its staging copy. |

Geass runs project workloads only in their own `<project>-<environment>` namespaces, never in these.

:::note
An ingress controller that you install in another namespace is not on the list, so it cannot reach isolated services. Geass uses the K3s Traefik in `kube-system`.
:::

## Let one project call another

The allow list belongs to the project that **receives** the calls. To let a front end project call an API project, change the API project.

1. Open the API project and choose **Settings**, then **Network**.
2. Under **Projects allowed to reach this one**, tick the front end project. The list shows every other project, by display name and ID.
3. Choose **Save**. The dashboard shows "Network settings saved". The change applies at once and needs no redeploy.

The front end then calls the API on its private address. A service's address is on its **Settings** page, in the **Networking** section under **Private networking**:

```text
api.morning-beach-production.svc.cluster.local:8080
```

The form is `<service>.<project>-<environment>.svc.cluster.local:<port>`. The allow list covers every environment of the receiving project, and every environment of the allowed project can call it.

You can allow at most 20 projects. The dashboard refuses a name that is not an existing project, so a typo cannot leave a door open for whoever creates that project later.

## Open a project to the whole cluster

Switch off **Keep this project's services private** under **Settings**, then **Network**, and choose **Save**. The page then reads "Every project in the cluster can reach this project's services." The allow list is ignored while the switch is off, and the dashboard clears it.

Switching it back on restores isolation, with an empty allow list unless you tick projects again.

## Keep services away from the cluster's machines

Isolation limits who can reach a project's services. It says nothing about what a service can reach. By default a service can open a connection to the servers Geass runs on, and each server answers on its own address: its kubelet, the metrics of the machine and anything else you run on the host. Most of it asks for a login, but a service has no use for any of it. This switch closes that door. **It is on for projects created in the dashboard or the API, and off for every project that existed before it, and for a project you create with a manifest that does not set it.** A project is not changed by being updated to a release that has the switch.

1. Open the project and choose **Settings**, then **Network**.
2. Switch **Keep this project's services away from the cluster's machines** on, or off.
3. Choose **Save**. The change applies at once and needs no redeploy.

**What it blocks.** Every connection from a service in the project to a node: each node's own addresses (internal and external), and the two lowest addresses of the node's pod range, which is where the node itself sits on the pod network (the address pods use as their gateway). Those connections fail by timing out.

**What stays open.** Everything else a service could reach before: other services, cluster DNS, the internet, and machines that are not nodes, such as a database in your own network. The one exception is the Kubernetes API on the control plane, only on the port that the cluster's `kubernetes` Service sends to (6443 on K3s), and only at the control plane's addresses. Postgres databases in a project's namespace use the API, and stop working without it.

**It follows the nodes.** The rule names each node, so Geass rewrites it for every project that has the switch on when a node joins, leaves or changes an address. This takes a few seconds. A node that has just joined is open to the project's services for that long.

**What it costs.** A service that reaches something through a server's address stops working. The usual cases are a service calling its own public address when DNS points straight at a server and the server reports that address, and a service calling software that you run on the server itself, such as a database on the host. Call another service by its private address instead (see [Let one project call another](#let-one-project-call-another)), and put host software on a machine that is not a node, or switch the block off for the project.

:::note
This relies on the network plugin treating node addresses as address ranges, which the network policy controller built into K3s does. A plugin that handles traffic to nodes with rules of its own may not honour it. Check with the test below on your cluster. Only IPv4 addresses are listed, because the policy only lets IPv4 leave the cluster.
:::

To check, ask a node's kubelet for its health from a pod in the project. Use a node's address from `kubectl get nodes -o wide`:

```bash
kubectl run probe --rm -it --restart=Never -n morning-beach-production \
  --image=curlimages/curl:8.10.1 -- \
  curl -sk -m 5 -o /dev/null -w '%{http_code}\n' https://10.0.0.5:10250/healthz
```

With the switch off this prints `401`: the kubelet answered and wants a login. With it on it prints `000`: the connection timed out.

## Change it with a manifest or the API

The settings live in the project's `spec`. The fragment below shows only the network fields. Add them to your project's full manifest, or use the `kubectl patch` that follows, rather than applying the fragment alone:

```yaml title="project-network.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassProject
metadata:
  name: morning-beach
  namespace: geass-system
spec:
  network:
    isolation: Project        # Project (default) or None
    allowFromProjects:
      - quiet-river
```

| Field | Meaning | Default |
| --- | --- | --- |
| `spec.network.isolation` | `Project` limits who can reach the project's services. `None` leaves them open to every service in the cluster. | `Project`, also when the field is absent |
| `spec.network.allowFromProjects` | Other projects whose services may reach this one. At most 20. Ignored when `isolation` is `None`. | None |
| `spec.network.blockNodeAddresses` | `true` keeps the project's services from opening connections to the cluster's machines. It is independent of `isolation`. | `false` when absent. Projects created by the dashboard or the API set it to `true` |

With the command line, signed in as an admin. It changes only what you name, and leaves the rest as it was:

```bash
geass projects network show morning-beach
geass projects network set morning-beach --block-nodes            # keep its services away from the cluster's machines
geass projects network set morning-beach --block-nodes=false
geass projects network set morning-beach --allow-from quiet-river
geass projects network set morning-beach --isolation none         # open it to every service in the cluster
```

An AI agent connected through [the MCP server](/docs/guides/mcp-server/) can read this setting with `project_network`, and has no tool to change it: opening a project up is for a person to choose.

With `kubectl`:

```bash
kubectl -n geass-system patch geassproject morning-beach --type merge \
  -p '{"spec":{"network":{"isolation":"Project","allowFromProjects":["quiet-river"],"blockNodeAddresses":true}}}'
```

Through the API, with a workspace token that has the admin role. Repeat `allowFromProjects` for each project, and always send `isolation`. Send `blockNodeAddresses=true` or `false` to change the machine block (a JSON body takes the booleans `true` and `false`). A request that leaves it out keeps it as it was, so a client that only changes who may call in cannot switch it off:

```bash
curl -sS -X POST https://geass.example.com/api/projects/morning-beach/network \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d isolation=Project -d allowFromProjects=quiet-river -d blockNodeAddresses=true
```

`kubectl` does not check that the names you list are projects. The dashboard and the API do. They also drop your own project and repeated names from the list. The API stores the list as sent when `isolation` is `None`; it has no effect then.

## What holds whatever you choose

**Services cannot read the cloud provider's credentials.** On most clouds, any process on a machine can ask `169.254.169.254` for the machine's credentials, and with them whatever the machine's role may do. Geass blocks that address range (`169.254.0.0/16`) for every pod in every project, including projects you have opened to the cluster. The page says so: "Whatever you choose here, services are never allowed to read the cloud provider's credentials for the machine they run on."

**No project can read another's metrics or logs.** Geass closes the metrics store (Prometheus) and the log store to everything but the platform, so a project's pods cannot query them. The dashboard also builds every metrics and log query from the project's own namespaces. See [Logs, metrics and terminal](/docs/guides/observability/).

## Projects that existed before the machine block

A project that existed before the machine block has no `blockNodeAddresses` setting, and a project with no setting is not blocked, so updating Geass changes nothing for it. Only projects created afterwards through the dashboard or the API start with it on. To switch it on for an existing project, open its **Settings**, then **Network**, and switch **Keep this project's services away from the cluster's machines** on, or patch `spec.network.blockNodeAddresses` to `true`.

## Projects that existed before isolation

Projects that were created before Geass added isolation have no network setting. A project with no setting is isolated, so when Geass is updated, every such project becomes isolated. A service that called another project's service stops working until you allow it.

Before you update:

1. List the projects that call another project's services.
2. After the update, open each receiving project's **Settings**, then **Network**, and tick the callers.
3. If you want the old behaviour for a project, switch off **Keep this project's services private** on it.

## Check that the cluster enforces policies

A network policy is only words until the network plugin acts on it, and a plugin that does not enforce policies accepts every one without complaint. Without a check, a cluster could look isolated and not be, so Geass finds out by trying. It starts a few minutes after the operator does, then once a day, and whenever you ask:

1. It makes a namespace `geass-netcheck` with a policy that closes one pod to everyone.
2. It starts two small servers, one of them the closed pod, and a third pod that tries to connect to both. The pods run the operator's own image, so the check needs no registry and no internet, and they run on the control plane where that image is.
3. A server that nobody closed must answer, which shows that the pod network works. The closed one must refuse.
4. It records the answer on the cluster as the condition `NetworkPolicyEnforced` and removes the namespace. The check takes under a minute.

**Usage** shows the answer under **Network isolation**:

| Shown | Condition | Meaning |
| --- | --- | --- |
| **Enforced** | `True` | The closed pod refused the connection. Isolation holds. |
| **Not enforced** | `False` | The closed pod accepted it. Project isolation, the machine block and the protection of the metrics and log stores are written and nothing acts on them. K3s enforces policies unless it was started with `--disable-network-policy`. On another network plugin, use one that enforces them, then ask for a check. |
| **Unknown** | `Unknown` | The check could not tell: the pods did not start, or the pod that nobody closed could not be reached either, so the pod network itself may be at fault. Geass tries again after half an hour. |

Nothing is shown until the first check has run. To read it, or to ask for a check at once after you have fixed something:

```bash
kubectl -n geass-system get geasscluster default -o jsonpath='{.status.conditions[?(@.type=="NetworkPolicyEnforced")]}{"\n"}'
kubectl -n geass-system annotate geasscluster default geass.dev/recheck-network-policy=now --overwrite
```

The check runs once for the cluster, in the operator that holds the lock, and it never holds anything back: a check that fails leaves the condition Unknown and the cluster as it was.

## Check the rules

Network policies are Kubernetes objects, so `kubectl` shows exactly what is in force. A project environment's namespace is `<project>-<environment>`.

```bash
kubectl get networkpolicy -n morning-beach-production
kubectl describe networkpolicy geass-project-isolation -n morning-beach-production
kubectl get namespace morning-beach-production --show-labels      # expect geass.dev/project=morning-beach
kubectl get networkpolicy -n monitoring
kubectl get networkpolicy -n geass-logging
kubectl -n geass-system get geassproject morning-beach -o jsonpath='{.spec.network}'
```

To prove that isolation works, call a service from another project's namespace. The call should time out:

```bash
kubectl run probe --rm -it --restart=Never -n quiet-river-production \
  --image=curlimages/curl:8.10.1 -- \
  curl -sS -m 5 http://api.morning-beach-production.svc.cluster.local:8080
```

Add `quiet-river` to the allow list of `morning-beach` and repeat. The same call now answers. Run the test from another project's namespace, so that the allow list is the only thing that changes. A pod in a namespace that is not a project, such as `default`, is blocked too, but it cannot show that an allowed project gets through.

## What happens under the hood

The project controller writes and keeps the first four of these objects, and the object store controller keeps `geass-s3`. If you edit or delete one by hand, its controller puts it back.

| Policy | Namespace | What it does |
| --- | --- | --- |
| `geass-project-isolation` | Each project environment | Selects every pod. Allows ingress only from namespaces labelled `geass.dev/project` with the project's name or an allowed project's name, and from `kube-system`, `geass-system`, `monitoring`, `cnpg-system`, `moco-system`, `mariadb-system` and `keda`. Present only when isolation is `Project`. |
| `geass-block-cloud-metadata` | Each project environment | Selects every pod. Allows egress to every pod and to `0.0.0.0/0` except `169.254.0.0/16`. With the machine block on, the exception list also names every node address and the two lowest addresses of every node's pod range as `/32` ranges, and a second rule allows the control plane's addresses on the API port. For a project of an organization with [outbound limits](/docs/guides/outbound-limits/), the rule for `0.0.0.0/0` lists the ports left open, and the pods are in a rule of their own. Always present, even when isolation is `None`. |
| `geass-restrict-metrics-store` | `monitoring` | Selects only the Prometheus pods. Allows ingress only from the same namespace and `geass-system`. Created when monitoring is on. |
| `geass-restrict-log-store` | `geass-logging` | Selects every pod. Allows ingress only from the same namespace, `geass-system` and `monitoring`. Created when the logging add-on is on. |
| `geass-s3` | `geass-system` | Selects only the pod of the cluster's object storage server. Allows ingress to the S3 port (8333 for SeaweedFS, 3900 for Garage) from every namespace, to Garage's admin port 3903 only from `geass-system`, and to no other port. Created with the server and deleted with it. |

The isolation policy finds a project's namespaces by their `geass.dev/project` label. The project controller adds the label back if it is missing. The machine block sits in this policy, and not in a policy of its own, because policies add up: a second policy that excluded the nodes would be cancelled by this one allowing them. The policy keeps its name for that reason, and so that updating Geass does not replace it. The metadata policy lists every namespace's pods as well as the address range on purpose: a rule that named only addresses would cut pods off from cluster DNS and from other services on some network plugins.

The metrics and log policies are created once their namespace exists, which is after the add-on's chart is installed. Geass looks again every 30 seconds until then.

Geass restricts only the Prometheus pods in `monitoring`, not the whole namespace, because the Kubernetes API server calls webhooks in that namespace from outside the pod network.

## What isolation does not cover

- **Outbound traffic.** Only the cloud metadata range is blocked, and the cluster's machines when you switch that on. Services can reach the internet, other private networks and every other service that accepts them. On a cluster that organizations share, the platform can close ports to the outside for their projects, mail's first: see [Outbound limits](/docs/guides/outbound-limits/).
- **Shared database servers.** They run in `geass-system`, which has no isolation policy, so every project's pods can open a connection to them. Each project gets its own database and its own login, and a login can reach only its own database. See [Shared database servers](/docs/guides/shared-database-servers/).
- **The object storage server.** Its S3 port is open to every namespace, whatever a project's isolation, because that is how apps reach their buckets. Each project's login can reach only its own bucket. The policy `geass-s3` limits the server to its ports. See [Object storage](/docs/guides/object-storage/).
- **The platform namespaces.** `geass-system`, `kube-system`, `monitoring`, `cnpg-system`, `moco-system`, `mariadb-system` and `keda` can reach every project. Geass adds no rule in front of the dashboard, the operator or the add-ons other than the metrics and log stores and the object storage server.
- **Builds of an organization.** A project that belongs to an [organization](/docs/guides/organizations-and-workspaces/#builds-and-the-registry) builds in the organization's own namespace, `geass-build-<organization>`, which the policy `geass-build-isolation` closes: nothing may connect to a build, and a build reaches the cluster's DNS and the internet but no private address (`10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `100.64.0.0/10`, link-local, loopback). A registry on a private address is therefore out of reach of an organization's builds.
- **Traffic between pods of one project.** Everything inside a project, across environments, is open.
- **Ports.** Project rules cover whole services. There are no per-port rules for a project. The exceptions are the object storage server's policy, which lists ports, and an organization's [outbound limits](/docs/guides/outbound-limits/), which list the ports its pods may reach outside the cluster.

## Troubleshooting

**A service cannot reach another project's service. Calls time out.**
The receiving project is isolated and does not list the caller. Open the receiving project's **Settings**, then **Network**, and tick the calling project. Calls to a service in the same project, in any environment, need no setting.

**Calls worked before an update and stopped after it.**
Projects without a network setting became isolated. See [Projects that existed before isolation](#projects-that-existed-before-isolation).

**Visitors reach a service through its domain, but another project's pod cannot.**
That is intended. Visitors arrive through Traefik in `kube-system`, which is always allowed.

**Visitors time out after I moved to a different ingress controller.**
Only `kube-system` is allowed. An ingress controller in another namespace cannot reach isolated services. Run it in `kube-system`, or switch the project's isolation off.

**My test from another project connects.**
Check that the target project is isolated: `kubectl get networkpolicy -n <project>-<environment>` should list `geass-project-isolation`. If it does not, the project has `isolation: None`. If it does, but the call still connects, your network plugin may not enforce network policies.

**A service cannot reach a server's address after I switched on the machine block.**
That is what it does. Call the service by its private address (`<service>.<project>-<environment>.svc.cluster.local`) instead of through a server's address. If you need the old behaviour for this project, switch **Keep this project's services away from the cluster's machines** off.

**A Postgres database stopped working after I switched on the machine block.**
It should not: the Kubernetes API stays open on the control plane. Check the policy: `kubectl get networkpolicy geass-block-cloud-metadata -n <project>-<environment> -o yaml` should list a second egress rule with the control plane's address and the port 6443 (the `targetPort` of the `kubernetes` Service in `default`). If the rule is missing, check that the control plane node carries the label `node-role.kubernetes.io/control-plane`.

**A node I just added can still be reached by a project's services.**
Geass updates the policies within seconds of a node joining. If it stays open, check that the node reports an address (`kubectl get nodes -o wide`) and that the operator is running.

**The Usage page says network policies are not enforced.**
The check found that a pod a policy closes to everyone still accepted a connection. On a cluster Geass installed, K3s's built-in network policy controller enforces policies unless the server was started with `--disable-network-policy` (look in `/etc/rancher/k3s/config.yaml` and at `systemctl cat k3s`). Take that out and restart K3s, then ask for a check with the annotation above.

**The Usage page says the network policy check is unknown.**
Read the condition's message. The usual causes are that the operator's image is not on the control plane node (the check runs it there), or that the pod network is not working at all. Look at the pods while a check runs: `kubectl -n geass-netcheck get pods -o wide`.

**A policy I edited keeps changing back.**
The controller owns the four policies in the table and restores them. Add your own rules as separate policies with other names. Policies are additive, so yours add to Geass's and never remove from them.

**The dashboard says "project does not exist" when I save.**
You ticked or sent a project name that is not an existing project. Check `kubectl -n geass-system get geassprojects`.

**A project's namespace has no `geass.dev/project` label.**
The project controller restores it the next time it reconciles the project, which a change to the project or to one of its policies triggers, and so does an operator restart. If it does not, check that the operator is running: `kubectl -n geass-system get deploy geass-controller-manager`.

## Related

- [Projects and environments](/docs/guides/projects-and-environments/)
- [Logs, metrics and terminal](/docs/guides/observability/)
- [Shared database servers](/docs/guides/shared-database-servers/)
- [Sleep when idle](/docs/guides/scale-to-zero/)
- [Security](/docs/operations/security/)
