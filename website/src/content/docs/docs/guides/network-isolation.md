---
title: "Network isolation"
description: "The services of each project are private by default. Let one project call another, open a project to all projects, and keep services away from servers."
---

By default, the services of one project cannot call the services or databases of another project. Each project is private. Only these can reach its services:

- Its own services, in every environment.
- Geass itself.
- Visitors who arrive through your domains.

You can let other projects in, or open a project to all projects. A project that you create in the dashboard also cannot reach the servers of the cluster.

## Before you start

- Only admins can change network settings. Project tokens, observer tokens, and viewer tokens cannot.
- Isolation works only when the cluster enforces it. K3s enforces it by default. Geass checks this, and shows the result on the **Usage** page. See [Check that the cluster enforces policies](#check-that-the-cluster-enforces-policies).

## What is isolated by default

The services and databases of a project accept connections only from these sources:

- The project itself, in every environment, pull request environments included.
- Geass and its add-ons. These include the ingress controller that brings in visitors, the metrics collector, the database managers, and the [sleep when idle](/docs/guides/scale-to-zero/) proxy.
- The projects that you allow.

Isolation limits who can **reach** a project. It does not limit what the services of a project can reach. They can always reach these destinations:

- Other services in the same project.
- Other projects that allow them.
- The cluster DNS.
- The internet.

## Let one project call another

The project that **receives** the calls holds the allow list. For example, to let a front end project call an API project, change the API project.

1. Open the API project.
2. Select **Settings**, and then **Network**.
3. Under **Projects allowed to reach this one**, select the front end project.
4. Click **Save**. The dashboard shows "Saved". The change applies immediately, without a deploy.

The front end then calls the API on its private address. The private address of a service is on its **Settings** page, under **Networking** and **Private networking**. For example:

```text
api.morning-beach-production.svc.cluster.local:8080
```

The allow list covers every environment of both projects. You can allow up to 20 projects. Geass refuses a name that is not an existing project. So a typo cannot open a door for a project that someone creates later.

## Open a project to all projects

1. Open the project, and select **Settings**, and then **Network**.
2. Turn off **Keep this project's services private**.
3. Click **Save**. The page then shows "Every project can reach this project's services."

While the switch is off, Geass ignores the allow list, and the dashboard clears it. If you turn the switch on again, the allow list is empty until you select projects again.

## Keep services away from the cluster's machines

By default, a service can open a connection to the servers that Geass runs on. A service does not need these connections, and they are a security risk. This switch closes them.

The default value of the switch depends on the project:

- **On** for projects that you create in the dashboard or the API.
- **Off** for projects that existed before the switch, and for projects that you create with a manifest.

To change the switch, do these steps:

1. Open the project, and select **Settings**, and then **Network**.
2. Turn **Keep this project's services away from the cluster's servers** on or off.
3. Click **Save**. The change applies immediately, without a deploy.

With the switch on, a connection from a service to the address of a server times out. A service can still reach other services, the cluster DNS, the internet, and machines that are not part of the cluster. When a server joins or leaves, Geass updates the rule within a few seconds.

A service that reaches something through the address of a server stops working. These are the usual cases:

- A service calls its own public domain, and the DNS points directly at a server.
- A service calls software that runs on the server itself, for example a database on the host.

To fix these cases, do one of these steps:

- Call the other service by its private address. See [Let one project call another](#let-one-project-call-another).
- Move the host software to a machine that is not part of the cluster.
- Turn off the switch for the project.

:::note
The switch was tested with the network plugin of K3s. Another network plugin might not obey it. See [Under the hood](#under-the-hood) for a test.
:::

## From the command line

Sign in as an admin. The command changes only what you name:

```bash
geass projects network show morning-beach
geass projects network set morning-beach --block-nodes            # keep its services away from the servers
geass projects network set morning-beach --block-nodes=false
geass projects network set morning-beach --allow-from quiet-river
geass projects network set morning-beach --isolation none         # open it to all projects
```

An AI agent can read this setting with the `project_network` tool. It cannot change it, because a person must decide to open a project. A script calls `POST /api/projects/<project>/network` with `isolation`, `allowFromProjects`, and `blockNodeAddresses`. A request without `blockNodeAddresses` keeps its value. See the [REST API](/docs/reference/rest-api/).

## What holds whatever you choose

**Services cannot read the credentials of the cloud provider.** On most clouds, any program on a machine can ask `169.254.169.254` for the credentials of the machine. Geass blocks that address range for every project, also for open projects.

**No project can read the metrics or logs of another project.** The services of a project cannot reach the metrics store or the log store. See [Logs, metrics and terminal](/docs/guides/observability/#who-can-see-what).

## Projects from before these settings

Projects from before isolation was added have no network setting. A project with no setting is private. So after you update Geass, a service that called another project stops working until you allow it. Before you update, do these steps:

1. List the projects that call the services of another project.
2. After the update, open **Settings** → **Network** of each project that receives calls, and select the callers.
3. To keep the old behavior for a project, turn off **Keep this project's services private**.

Projects from before the server block keep the block off. To turn it on, use the switch in [Keep services away from the cluster's machines](#keep-services-away-from-the-clusters-machines).

## Check that the cluster enforces policies

Isolation is a set of rules that the network of the cluster must enforce. A network that does not enforce them accepts them without an error. So Geass tests the cluster. The test runs a few minutes after Geass starts, and then one time each day. It takes less than one minute.

The **Usage** page shows the result under **Network isolation**:

| Shown | Meaning |
| --- | --- |
| **Enforced** | The test passed. Isolation holds. |
| **Not enforced** | The rules have no effect. This includes isolation, the server block, and the protection of the metrics and log stores. See [Troubleshooting](#troubleshooting). |
| **Unknown** | The test could not get a result. Geass tries again after 30 minutes. |

The page shows nothing until the first test has run. Geass has no dashboard or command-line way to run the test again immediately, for example after a fix. See [Under the hood](#under-the-hood).

When the network does not enforce the rules, Geass also sends a **Cluster problem** [alert](/docs/guides/alerts/).

## What isolation does not cover

- **Outbound traffic.** Services can reach the internet and other private networks. Geass blocks only the cloud credentials address, and the servers when you turn on that switch. On a cluster that organizations share, the platform can close more ports. See [Outbound limits](/docs/guides/outbound-limits/).
- **Shared database servers.** Every project can connect to them. Each project gets its own database and login, and a login can reach only its own database. See [Shared database servers](/docs/guides/shared-database-servers/).
- **The object storage server.** Every project can connect to it. The login of each project can reach only its own bucket. See [Object storage](/docs/guides/object-storage/).
- **Geass and its add-ons.** They can reach every project.
- **Builds of an organization.** Nothing can connect to a build. A build can reach the cluster DNS and the internet, but no private address. So a build cannot reach a registry on a private address. See [Organizations and workspaces](/docs/guides/organizations-and-workspaces/#builds-and-the-registry).
- **Traffic in one project.** All traffic in a project is open, across environments.
- **Ports.** The rules cover whole services, not single ports.

## Troubleshooting

**A service cannot reach a service of another project. Calls time out.**
The receiving project is private and does not allow the caller. Open **Settings** → **Network** of the receiving project, and select the caller.

**Calls worked before an update and stopped after it.**
Projects with no setting became private. See [Projects from before these settings](#projects-from-before-these-settings).

**Visitors reach a service through its domain, but another project cannot.**
This is expected. Visitors arrive through the ingress controller, which is always allowed.

**Visitors time out after you moved to another ingress controller.**
Geass allows only the ingress controller of K3s. Use that ingress controller, or turn off isolation for the project.

**A service cannot reach the address of a server after you turned on the server block.**
This is expected. Call the other service by its private address, or turn off the switch for the project.

**The Usage page says "Not enforced".**
On a cluster that Geass installed, K3s enforces the rules, unless a server started with `--disable-network-policy`. Geass has no dashboard or command-line way to fix this. See [Under the hood](#under-the-hood).

**The Usage page says "Unknown".**
The test could not run. Usually the network of the cluster does not work at all. See [Under the hood](#under-the-hood).

**The dashboard says "project does not exist" when you save.**
You selected a project that does not exist. Reload the page, and select again.

## Under the hood

Isolation is made of Kubernetes NetworkPolicies. Geass keeps these policies, and puts them back if you change them. To add rules of your own, create policies with other names. Policies add up, so your policies can open more, but cannot close what Geass opens.

| Policy | Namespace | What it does |
| --- | --- | --- |
| `geass-project-isolation` | Each environment | Allows connections only from the project, the allowed projects, and the namespaces of Geass and its add-ons. Only when the project is private. |
| `geass-block-cloud-metadata` | Each environment | Blocks `169.254.0.0/16`. With the server block on, it also blocks each server address, but leaves the Kubernetes API (port 6443) open on the control plane. |
| `geass-restrict-metrics-store` | `monitoring` | Allows only Geass to reach Prometheus. |
| `geass-restrict-log-store` | `geass-logging` | Allows only Geass and monitoring to reach the log store. |
| `geass-s3` | `geass-system` | Opens only the S3 port of the object storage server. |

The namespace of an environment is `<project>-<environment>`, with the label `geass.dev/project`. The test of the cluster runs in a namespace `geass-netcheck`, and records the condition `NetworkPolicyEnforced` on the cluster.

To see the rules, and to prove that isolation works, run these commands. The call from another project must time out:

```bash
kubectl get networkpolicy -n morning-beach-production
kubectl -n geass-system get geasscluster default -o jsonpath='{.status.conditions[?(@.type=="NetworkPolicyEnforced")]}{"\n"}'
kubectl -n geass-netcheck get pods -o wide        # while a test runs
kubectl run probe --rm -it --restart=Never -n quiet-river-production \
  --image=curlimages/curl:8.10.1 -- \
  curl -sS -m 5 http://api.morning-beach-production.svc.cluster.local:8080
```

To test the server block, call the kubelet of a server from the project. With the block off, the command prints `401`. With the block on, it prints `000`:

```bash
kubectl run probe --rm -it --restart=Never -n morning-beach-production \
  --image=curlimages/curl:8.10.1 -- \
  curl -sk -m 5 -o /dev/null -w '%{http_code}\n' https://10.0.0.5:10250/healthz
```

To run the test of the cluster again immediately, run this command on the control plane server:

```bash
sudo k3s kubectl -n geass-system annotate geasscluster default geass.dev/recheck-network-policy=now --overwrite
```

When the **Usage** page says **Not enforced**, look for the flag `--disable-network-policy` in `/etc/rancher/k3s/config.yaml` and in `systemctl cat k3s` on each server. Remove the flag, and restart K3s. Then run the test again.

The settings are in `spec.network` of the project: `isolation` (`Project` or `None`), `allowFromProjects`, and `blockNodeAddresses`. See [GeassProject](/docs/reference/resources/geass-project/).

## Related

- [Projects and environments](/docs/guides/projects-and-environments/)
- [Outbound limits](/docs/guides/outbound-limits/)
- [Logs, metrics and terminal](/docs/guides/observability/)
- [Shared database servers](/docs/guides/shared-database-servers/)
- [Sleep when idle](/docs/guides/scale-to-zero/)
- [Security](/docs/operations/security/)
