---
title: "Network isolation"
description: "The services of each project are private by default. Let one project call another, open a project to the whole cluster, and check the rules with kubectl."
---

In a cluster with no rules, any service can call any other service. Thus, the service of one project could reach the database of a different project, or read its metrics. Geass closes that path by default. Each project is private. Only these can reach its services: its own services, Geass itself, and visitors who arrive through your domains. You select which other projects can reach a project, or you open the project to everything. If you created a project in the dashboard or the API, Geass also keeps the project away from the servers that the cluster runs on.

## Before you start

- Only admins can change network settings. Admins are dashboard admins and workspace API tokens with the admin role. Geass refuses project tokens, observer tokens, and viewer tokens.
- Kubernetes network policies enforce isolation. K3s enforces them with its built-in network policy controller, and Geass does not turn that controller off. On a cluster whose network plugin ignores network policies, Geass still writes the rules, but nothing enforces them. Geass checks for this problem and shows the result on the **Usage** page. See [Check that the cluster enforces policies](#check-that-the-cluster-enforces-policies).

## What is isolated by default

The services and databases of a project accept connections only from these sources:

- The project itself, in **every** one of its environments. This includes pull request environments.
- The platform: the ingress controller, the Geass operator and dashboard, Prometheus, the Postgres operator, and the scale-to-zero proxy. See the table in the next section.
- The projects that you name in its allow list.

The project drops all other connections, and that includes all other projects. The rule limits who can **reach** a project. The services of a project can always reach these destinations:

- Other services.
- Other projects that allow them.
- The cluster DNS.
- The internet.

Answers to connections that a project starts come back as usual.

## What is always allowed

These sources can always reach the services of a project, whatever you select, because Geass or your visitors need them:

| Source | Why |
| --- | --- |
| Namespace `kube-system` | The ingress controller (Traefik), which forwards visitors who arrive through your domains. |
| Namespace `geass-system` | The operator and dashboard, and the Cloudflare tunnel when you use one. |
| Namespace `monitoring` | Prometheus, which scrapes service metrics. |
| Namespace `cnpg-system` | The Postgres operator, which manages every Postgres database. |
| Namespace `moco-system` | The MySQL operator, which manages every high availability MySQL database. |
| Namespace `mariadb-system` | The MariaDB operator, which manages every MariaDB database. |
| Namespace `keda` | The proxy of the serverless add-on. It holds a request while a [sleeping service](/docs/guides/scale-to-zero/) starts, and then passes the request on. The namespace exists only when the add-on is installed. The rule is there in both cases. |
| The own namespaces of the project | A service that calls its database, or the production environment that calls its staging copy. |

Geass runs project workloads only in their own `<project>-<environment>` namespaces, never in these namespaces.

:::note
An ingress controller that you install in a different namespace is not on the list. Thus, it cannot reach isolated services. Geass uses the K3s Traefik in `kube-system`.
:::

## Let one project call another

The allow list belongs to the project that **receives** the calls. To let a front end project call an API project, change the API project.

1. Open the API project.
2. Select **Settings**, then **Network**.
3. Under **Projects allowed to reach this one**, select the front end project. The list shows all other projects by name.
4. Click **Save**. The dashboard shows "Saved". The change applies immediately and needs no redeploy.

The front end then calls the API on its private address. The address of a service is on its **Settings** page, in the **Networking** section under **Private networking**:

```text
api.morning-beach-production.svc.cluster.local:8080
```

The form is `<service>.<project>-<environment>.svc.cluster.local:<port>`. The allow list covers all environments of the receiving project. All environments of the allowed project can call it.

You can allow a maximum of 20 projects. The dashboard refuses a name that is not an existing project. Thus, a typo cannot leave a door open for a person who creates that project later.

## Open a project to the whole cluster

1. Under **Settings**, then **Network**, turn off **Keep this project's services private**.
2. Click **Save**. The page then reads "Every project in the cluster can reach this project's services."

While the switch is off, Geass ignores the allow list, and the dashboard clears it.

If you turn the switch on again, Geass restores isolation. The allow list is empty unless you select projects again.

## Keep services away from the cluster's machines

Isolation limits who can reach the services of a project. It does not limit what a service can reach. By default, a service can open a connection to the servers that Geass runs on. Each server answers on its own address: its kubelet, the metrics of the machine, and all other software that you run on the host. Most of these ask for a login, but a service does not need any of them. This switch closes that door.

**The default value of this switch depends on the project:**

- **On** for projects that you create in the dashboard or the API.
- **Off** for all projects that existed before the switch, and for a project that you create with a manifest that does not set the switch.

When you update to a release that has the switch, the update does not change a project.

1. Open the project.
2. Select **Settings**, then **Network**.
3. Turn **Keep this project's services away from the cluster's machines** on or off.
4. Click **Save**. The change applies immediately and needs no redeploy.

**What it blocks.** The switch blocks all connections from a service in the project to a node:

- The own addresses of each node, internal and external.
- The two lowest addresses of the pod range of the node. These are where the node itself is on the pod network, which is the address that pods use as their gateway.

Those connections fail with a timeout.

**What stays open.** A service can still reach all other destinations that it could reach before: other services, cluster DNS, the internet, and machines that are not nodes. An example of such a machine is a database in your own network. The only exception is the Kubernetes API on the control plane. The exception applies only to the port that the `kubernetes` Service of the cluster sends to, which is 6443 on K3s. It also applies only to the addresses of the control plane. Postgres databases in the namespace of a project use the API, and they stop working without it.

**It follows the nodes.** The rule names each node. When a node joins, leaves, or changes an address, Geass rewrites the rule for each project that has the switch on. This rewrite takes a few seconds. During that time, a node that joined is open to the services of the project.

**What it costs.** A service that reaches something through the address of a server stops working. These are the usual cases:

- A service calls its own public address, DNS points directly at a server, and the server reports that address.
- A service calls software that you run on the server itself, for example a database on the host.

To fix these cases, do one of these steps:

- Call the other service by its private address, not through the address of a server. See [Let one project call another](#let-one-project-call-another).
- Put host software on a machine that is not a node.
- Turn the block off for the project.

:::note
This switch needs a network plugin that treats node addresses as address ranges. The network policy controller in K3s does this. A plugin that handles traffic to nodes with its own rules might not obey the switch. Check with the test that follows on your cluster. The rule lists only IPv4 addresses, because the policy lets only IPv4 leave the cluster.
:::

To check, ask the kubelet of a node for its health from a pod in the project. Use a node address from `kubectl get nodes -o wide`:

```bash
kubectl run probe --rm -it --restart=Never -n morning-beach-production \
  --image=curlimages/curl:8.10.1 -- \
  curl -sk -m 5 -o /dev/null -w '%{http_code}\n' https://10.0.0.5:10250/healthz
```

With the switch off, the command prints `401`: the kubelet answered and wants a login. With the switch on, the command prints `000`: the connection timed out.

## Change it with a manifest or the API

The settings are in the `spec` of the project. The fragment that follows shows only the network fields. Do not apply the fragment alone. Add the fields to the full manifest of your project, or use the `kubectl patch` that follows:

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
| `spec.network.isolation` | `Project` limits who can reach the services of the project. `None` leaves them open to every service in the cluster. | `Project`, also when the field is absent |
| `spec.network.allowFromProjects` | Other projects whose services can reach this one. Maximum 20. Geass ignores it when `isolation` is `None`. | None |
| `spec.network.blockNodeAddresses` | `true` keeps the services of the project from opening connections to the machines of the cluster. It is independent of `isolation`. | `false` when absent. Projects that the dashboard or the API creates set it to `true` |

With the command line, sign in as an admin. The command changes only what you name, and leaves the rest as it was:

```bash
geass projects network show morning-beach
geass projects network set morning-beach --block-nodes            # keep its services away from the cluster's machines
geass projects network set morning-beach --block-nodes=false
geass projects network set morning-beach --allow-from quiet-river
geass projects network set morning-beach --isolation none         # open it to every service in the cluster
```

An AI agent connected through [the MCP server](/docs/guides/mcp-server/) can read this setting with `project_network`. It has no tool to change the setting, because a person must decide to open a project.

With `kubectl`:

```bash
kubectl -n geass-system patch geassproject morning-beach --type merge \
  -p '{"spec":{"network":{"isolation":"Project","allowFromProjects":["quiet-river"],"blockNodeAddresses":true}}}'
```

Through the API, use a workspace token that has the admin role:

- Repeat `allowFromProjects` for each project.
- Always send `isolation`.
- To change the machine block, send `blockNodeAddresses=true` or `false`. A JSON body takes the booleans `true` and `false`.

If a request does not include `blockNodeAddresses`, Geass keeps the value as it was. Thus, a client that changes only who can call in cannot turn off the machine block:

```bash
curl -sS -X POST https://geass.example.com/api/projects/morning-beach/network \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d isolation=Project -d allowFromProjects=quiet-river -d blockNodeAddresses=true
```

`kubectl` does not check that the names you list are projects. The dashboard and the API do. They also remove your own project and repeated names from the list. When `isolation` is `None`, the API stores the list as sent. The list then has no effect.

## What holds whatever you choose

**Services cannot read the credentials of the cloud provider.** On most clouds, any process on a machine can ask `169.254.169.254` for the credentials of the machine. With these credentials, the process can do all that the role of the machine can do. Geass blocks that address range (`169.254.0.0/16`) for every pod in every project. This includes projects that you opened to the cluster. The page says so: "Whatever you choose here, services are never allowed to read the cloud provider's credentials for the machine they run on."

**No project can read the metrics or logs of a different project.** Geass closes the metrics store (Prometheus) and the log store to all sources except the platform. Thus, the pods of a project cannot query them. The dashboard also builds each metrics and log query from the own namespaces of the project. See [Logs, metrics and terminal](/docs/guides/observability/).

## Projects that existed before the machine block

A project that existed before the machine block has no `blockNodeAddresses` setting. A project with no setting is not blocked. Thus, when you update Geass, nothing changes for that project. Only projects that you create after the update through the dashboard or the API start with the block on.

To turn on the block for an existing project, do one of these steps:

- Open its **Settings**, then **Network**, and turn on **Keep this project's services away from the cluster's machines**.
- Patch `spec.network.blockNodeAddresses` to `true`.

## Projects that existed before isolation

Projects that were created before Geass added isolation have no network setting. A project with no setting is isolated. Thus, when you update Geass, each such project becomes isolated. A service that called the service of a different project stops working until you allow it.

Before you update, do these steps:

1. List the projects that call the services of a different project.
2. After the update, open **Settings**, then **Network**, of each receiving project, and select the callers.
3. If you want the old behavior for a project, turn off **Keep this project's services private** on that project.

## Check that the cluster enforces policies

A network policy has no effect until the network plugin acts on it. A plugin that does not enforce policies accepts all policies without an error. Without a check, a cluster could look isolated and not be isolated. Thus, Geass tests the cluster. The check starts a few minutes after the operator starts, then runs one time each day, and also when you ask:

1. Geass makes a namespace `geass-netcheck` with a policy that closes one pod to all sources.
2. Geass starts two small servers. One of them is the closed pod. Geass also starts a third pod that tries to connect to both servers. The pods run the image of the operator, so the check needs no registry and no internet. The pods run on the control plane, where that image is.
3. The server that is not closed must answer. This shows that the pod network works. The closed server must refuse.
4. Geass records the result on the cluster as the condition `NetworkPolicyEnforced`, and removes the namespace. The check takes less than one minute.

**Usage** shows the result under **Network isolation**:

| Shown | Condition | Meaning |
| --- | --- | --- |
| **Enforced** | `True` | The closed pod refused the connection. Isolation holds. |
| **Not enforced** | `False` | The closed pod accepted the connection. Geass writes project isolation, the machine block, and the protection of the metrics and log stores, but nothing acts on them. K3s enforces policies unless it was started with `--disable-network-policy`. On a different network plugin, use one that enforces policies, and then ask for a check. |
| **Unknown** | `Unknown` | The check could not get a result. The pods did not start, or the pod that is not closed was also not reachable. In that case, the pod network itself might be the problem. Geass tries again after 30 minutes. |

The page shows nothing until the first check has run. To read the result, or to ask for a check immediately after you fix a problem:

```bash
kubectl -n geass-system get geasscluster default -o jsonpath='{.status.conditions[?(@.type=="NetworkPolicyEnforced")]}{"\n"}'
kubectl -n geass-system annotate geasscluster default geass.dev/recheck-network-policy=now --overwrite
```

The check runs one time for the cluster, in the operator that holds the lock. The check never blocks anything. If the check fails, it sets the condition to Unknown, and the cluster stays as it was.

## Check the rules

Network policies are Kubernetes objects. Thus, `kubectl` shows exactly what is in force. The namespace of a project environment is `<project>-<environment>`.

```bash
kubectl get networkpolicy -n morning-beach-production
kubectl describe networkpolicy geass-project-isolation -n morning-beach-production
kubectl get namespace morning-beach-production --show-labels      # expect geass.dev/project=morning-beach
kubectl get networkpolicy -n monitoring
kubectl get networkpolicy -n geass-logging
kubectl -n geass-system get geassproject morning-beach -o jsonpath='{.spec.network}'
```

To prove that isolation works, call a service from the namespace of a different project. The call must time out:

```bash
kubectl run probe --rm -it --restart=Never -n quiet-river-production \
  --image=curlimages/curl:8.10.1 -- \
  curl -sS -m 5 http://api.morning-beach-production.svc.cluster.local:8080
```

Add `quiet-river` to the allow list of `morning-beach`, and do the test again. The same call now answers. Run the test from the namespace of a different project, so that the allow list is the only thing that changes. Geass also blocks a pod in a namespace that is not a project, for example `default`. But that pod cannot show that an allowed project gets through.

## What happens under the hood

The project controller writes and keeps `geass-project-isolation` and `geass-block-cloud-metadata`. The cluster controller writes `geass-restrict-metrics-store` and `geass-restrict-log-store`. The object store controller keeps `geass-s3`. If you edit or delete a project policy or `geass-s3` by hand, its controller puts it back immediately. The cluster controller puts back the metrics and log policies the next time that it reconciles the cluster.

| Policy | Namespace | What it does |
| --- | --- | --- |
| `geass-project-isolation` | Each project environment | Selects every pod. Allows ingress only from namespaces with the label `geass.dev/project` set to the name of the project or of an allowed project. Also allows ingress from `kube-system`, `geass-system`, `monitoring`, `cnpg-system`, `moco-system`, `mariadb-system`, and `keda`. Present only when isolation is `Project`. |
| `geass-block-cloud-metadata` | Each project environment | Selects every pod. Allows egress to every pod and to `0.0.0.0/0` except `169.254.0.0/16`. With the machine block on, the exception list also names each node address and the two lowest addresses of the pod range of each node, as `/32` ranges. A second rule then allows the addresses of the control plane on the API port. For a project of an organization with [outbound limits](/docs/guides/outbound-limits/), the rule for `0.0.0.0/0` lists the ports that stay open, and the pods are in a separate rule. Always present, even when isolation is `None`. |
| `geass-restrict-metrics-store` | `monitoring` | Selects only the Prometheus pods. Allows ingress only from the same namespace and `geass-system`. Created when monitoring is on. |
| `geass-restrict-log-store` | `geass-logging` | Selects every pod. Allows ingress only from the same namespace, `geass-system`, and `monitoring`. Created when the logging add-on is on. |
| `geass-s3` | `geass-system` | Selects only the pod of the object storage server of the cluster. Allows ingress to the S3 port from every namespace. The S3 port is 8333 for SeaweedFS and 3900 for Garage. Allows ingress to the Garage admin port 3903 only from `geass-system`. Allows no other port. Created with the server and deleted with it. |

The isolation policy finds the namespaces of a project by their `geass.dev/project` label. If the label is missing, the project controller adds it again.

The machine block is in this policy, not in a separate policy, because policies add up. A second policy that excluded the nodes would have no effect, because this policy allows them. For that reason, the policy keeps its name. The name also stays the same so that a Geass update does not replace the policy.

The metadata policy lists the pods of every namespace and also the address range. This is intentional. On some network plugins, a rule that named only addresses would cut pods off from cluster DNS and from other services.

Geass creates the metrics and log policies after their namespace exists. The namespace exists after Geass installs the chart of the add-on. Until then, Geass looks again every 30 seconds.

Geass restricts only the Prometheus pods in `monitoring`, not the whole namespace. The reason is that the Kubernetes API server calls webhooks in that namespace from outside the pod network.

## What isolation does not cover

- **Outbound traffic.** Geass blocks only the cloud metadata range, and the machines of the cluster when you turn that block on. Services can reach the internet, other private networks, and all other services that accept them. On a cluster that organizations share, the platform can close ports to the outside for their projects, the mail ports first. See [Outbound limits](/docs/guides/outbound-limits/).
- **Shared database servers.** They run in `geass-system`, which has no isolation policy. Thus, the pods of every project can open a connection to them. Each project gets its own database and its own login. A login can reach only its own database. See [Shared database servers](/docs/guides/shared-database-servers/).
- **The object storage server.** Its S3 port is open to every namespace, whatever the isolation of a project. Apps reach their buckets through that port. The login of each project can reach only its own bucket. The policy `geass-s3` limits the server to its ports. See [Object storage](/docs/guides/object-storage/).
- **The platform namespaces.** `geass-system`, `kube-system`, `monitoring`, `cnpg-system`, `moco-system`, `mariadb-system`, and `keda` can reach every project. Geass adds no rule in front of the dashboard, the operator, or the add-ons, other than the metrics and log stores and the object storage server.
- **Builds of an organization.** A project that belongs to an [organization](/docs/guides/organizations-and-workspaces/#builds-and-the-registry) builds in the own namespace of the organization, `geass-build-<organization>`. The policy `geass-build-isolation` closes that namespace. Nothing can connect to a build. A build can reach the cluster DNS and the internet, but no private address: `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`, `100.64.0.0/10`, link-local, and loopback. Thus, the builds of an organization cannot reach a registry on a private address.
- **Traffic between pods of one project.** All traffic inside a project, across environments, is open.
- **Ports.** Project rules cover whole services. A project has no rules for each port. There are two exceptions. The policy of the object storage server lists ports. The [outbound limits](/docs/guides/outbound-limits/) of an organization list the ports that its pods can reach outside the cluster.

## Troubleshooting

**A service cannot reach another project's service. Calls time out.**
The receiving project is isolated and does not list the caller. Open **Settings**, then **Network**, of the receiving project, and select the calling project. Calls to a service in the same project, in any environment, need no setting.

**Calls worked before an update and stopped after it.**
Projects without a network setting became isolated. See [Projects that existed before isolation](#projects-that-existed-before-isolation).

**Visitors reach a service through its domain, but another project's pod cannot.**
This is the intended behavior. Visitors arrive through Traefik in `kube-system`, which is always allowed.

**Visitors time out after I moved to a different ingress controller.**
Only `kube-system` is allowed. An ingress controller in a different namespace cannot reach isolated services. Run the ingress controller in `kube-system`, or turn off isolation for the project.

**My test from another project connects.**
Make sure that the target project is isolated: `kubectl get networkpolicy -n <project>-<environment>` must list `geass-project-isolation`. If it does not, the project has `isolation: None`. If it does, but the call still connects, your network plugin might not enforce network policies.

**A service cannot reach a server's address after I switched on the machine block.**
The machine block causes this result. Call the service by its private address (`<service>.<project>-<environment>.svc.cluster.local`), not through the address of a server. If you need the old behavior for this project, turn off **Keep this project's services away from the cluster's machines**.

**A Postgres database stopped working after I switched on the machine block.**
This must not occur, because the Kubernetes API stays open on the control plane. Check the policy: `kubectl get networkpolicy geass-block-cloud-metadata -n <project>-<environment> -o yaml` must list a second egress rule with the address of the control plane and the port 6443. Port 6443 is the `targetPort` of the `kubernetes` Service in `default`. If the rule is missing, make sure that the control plane node has the label `node-role.kubernetes.io/control-plane`.

**A node I just added can still be reached by a project's services.**
Geass updates the policies a few seconds after a node joins. If the node stays open, make sure that the node reports an address (`kubectl get nodes -o wide`), and that the operator is running.

**The Usage page says network policies are not enforced.**
The check found that a pod that a policy closes to all sources still accepted a connection. On a cluster that Geass installed, the built-in network policy controller of K3s enforces policies. The exception is a server started with `--disable-network-policy`. Look in `/etc/rancher/k3s/config.yaml` and at `systemctl cat k3s`. Remove that flag and restart K3s. Then ask for a check with the annotation in [Check that the cluster enforces policies](#check-that-the-cluster-enforces-policies).

**The Usage page says the network policy check is unknown.**
Read the message of the condition. These are the usual causes:

- The image of the operator is not on the control plane node. The check runs the image there.
- The pod network does not work at all.

Look at the pods while a check runs: `kubectl -n geass-netcheck get pods -o wide`.

**A policy I edited keeps changing back.**
Geass owns the five policies in the table and restores them. The project controller and the object store controller restore their policies immediately. The cluster controller restores the metrics and log policies the next time that it reconciles the cluster. Add your own rules as separate policies with different names. Policies add up. Thus, your policies add to the policies of Geass and never remove anything from them.

**The dashboard says "project does not exist" when I save.**
You selected or sent a project name that is not an existing project. Check `kubectl -n geass-system get geassprojects`.

**A project's namespace has no `geass.dev/project` label.**
The project controller restores the label the next time that it reconciles the project. A change to the project or to one of its policies starts a reconcile. An operator restart also starts one. If the label does not come back, make sure that the operator is running: `kubectl -n geass-system get deploy geass-controller-manager`.

## Related

- [Projects and environments](/docs/guides/projects-and-environments/)
- [Logs, metrics and terminal](/docs/guides/observability/)
- [Shared database servers](/docs/guides/shared-database-servers/)
- [Sleep when idle](/docs/guides/scale-to-zero/)
- [Security](/docs/operations/security/)
