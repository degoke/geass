---
title: "Sleep when idle"
description: "Stop a service while nobody uses it and start it on the next request. Turn on the serverless add-on, set it per service, and see what visitors get."
---

A service that sleeps when idle is stopped after it has had no requests for a set time, and started again by the next request. While it sleeps it reserves no CPU or memory, so a small cluster can hold many services that are only used now and then, such as pull request environments and internal tools. The price is the first request after a sleep: it waits while the service starts.

It works for every kind of service: an image, a Dockerfile or GitHub build, or a [function](/docs/guides/functions/). It does not apply to databases or buckets.

## Before you start

- **The serverless add-on must be on for the cluster.** It installs [KEDA](https://keda.sh) and its HTTP add-on. It is off by default, the installer does not turn it on, and there is no switch for it in the dashboard. See [Turn on the add-on](#turn-on-the-add-on).
- **The service needs a public host.** A request to that host is what wakes it. Set a domain on the service ([Domains and TLS](/docs/guides/domains-and-tls/)), or connect Cloudflare so that services without a domain get a default one.
- **The service cannot use CPU autoscaling at the same time.** Both would decide how many copies run. See [Scaling and placement](/docs/guides/scaling-and-placement/).
- To change the setting you need to be a dashboard admin, hold a workspace token with the admin role, or hold the project token of the service's project.

## Turn on the add-on

Set `spec.addons.serverless.enabled` on the cluster definition, on the control plane:

```bash
kubectl -n geass-system patch geasscluster default --type merge \
  -p '{"spec":{"addons":{"serverless":{"enabled":true}}}}'
```

The operator installs KEDA first, then the HTTP add-on, then lets the ingress controller route to it. This takes a few minutes. It never holds the cluster back: while it installs, `Ready` and `AddonsReady` on the cluster are unaffected and every service runs awake. Check progress:

```bash
kubectl -n kube-system get helmcharts geass-keda geass-keda-http
kubectl -n keda get pods
kubectl get crd httpscaledobjects.http.keda.sh
```

Switching it off again does not uninstall KEDA. See [Limits](#limits) for what that means for services that already sleep.

## Make a service sleep

### In the dashboard

1. Open the project, select the service, and choose **Settings**, then the **Scale** section.
2. Under **Sleep when idle**, switch on **Sleep when idle**. This switches **Enable autoscaling** off.
3. Set **Sleep after (minutes)**, from 1 to 1440. The default is 5.
4. Choose **Save changes**.

The setting applies at once and needs no deploy. If the service cannot sleep yet, the block says why and adds "Until then it keeps running."

### From the command line, the API or an agent

```bash
geass scale web --sleep-after 10m   # stop after 10 minutes without requests (30s to 24h)
geass scale web --always-on         # keep it running again
```

```bash
curl -sS -X POST https://geass.example.com/api/apps/web/update \
  -H "Authorization: Bearer $GEASS_TOKEN" -H "Accept: application/json" \
  -d scaleToZero=on -d idleSeconds=600 -d scaleToZeroMax=5
```

`scaleToZero` is `on` or `off`. `idleSeconds` is 30 to 86400. `scaleToZeroMax` is 1 to 100. A field you leave out keeps its value. The same fields exist in the versioned API (`PATCH /api/v1/apps/{name}`), and an agent uses the `set_sleep_when_idle` tool. See [Command line](/docs/guides/command-line/), [MCP server](/docs/guides/mcp-server/), [CLI commands](/docs/reference/cli/) and [MCP tools](/docs/reference/mcp-tools/). Asking for `scaleToZero=on` while the service uses autoscaling is refused with "Scale to zero decides how many copies run, so turn CPU autoscaling off to use it".

### With a manifest

```yaml title="web.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassApp
metadata:
  name: web
  namespace: geass-system
spec:
  project: morning-beach
  environment: production
  source:
    image:
      image: ghcr.io/example/storefront:1.4.2
  port: 8080
  deploy:
    enabled: true
  ingress:
    host: shop.example.com
  scaleToZero:
    enabled: true
    idleSeconds: 600
    maxReplicas: 5
```

| Field | Meaning | Default |
| --- | --- | --- |
| `spec.scaleToZero.enabled` | Turns sleeping on. | Required inside `scaleToZero` |
| `spec.scaleToZero.idleSeconds` | How long without a request before the service is stopped. 30 to 86400. | `300` |
| `spec.scaleToZero.maxReplicas` | The most copies that run when the service is awake and busy. 1 to 100. | `3` |

While a service sleeps, the add-on decides how many copies run, from none up to `maxReplicas`. The service's `replicas` value does not apply. It applies again when you switch sleeping off.

## What a visitor experiences

The first request after a sleep is held, not refused. The visitor waits while a copy of the service starts, then gets the normal answer. Later requests are not delayed. How long the wait is depends on the service: its start-up time, its readiness check, and whether its image is already on the node (a first pull takes longer). The wait is limited by the KEDA HTTP add-on's own settings, which Geass leaves at their defaults.

Every request to a service that can sleep passes through the add-on's proxy, awake or not, so there is one extra hop on each request.

A service wakes only for requests that come through its public host. Calls from another service to its private address (`<service>.<project>-<environment>.svc.cluster.local`) do not wake it, and fail to connect while it sleeps. Keep a service that others call privately always on.

Anything that requests the public host counts as use. An uptime monitor that checks the host every minute keeps the service awake.

## What the dashboard shows

| Moment | Dashboard | `ScaleToZero` condition | `GET /api/v1/apps/{name}/status` |
| --- | --- | --- | --- |
| Asleep or running | **Ready** | `True`, reason `Active`, "Sleeps after idle, wakes on the next request" | `state` is `ready`. Asleep also has `replicas.desired: 0` and `sleeping: true`. |
| Starting after a request | **Setting up** | `True` | `state` is `pending`, with the message "Starting after a request" |
| Cannot sleep, runs always on | **Ready** | `False`, with a reason from the next section | `state` is `ready` |

A sleeping service has no pods, so its **Console** tab says "No running pods" and its **Metrics** charts have gaps. The command line marks it `(asleep)`.

The `ScaleToZero` condition exists only on services that asked to sleep. Read it with:

```bash
kubectl -n geass-system get geassapp web \
  -o jsonpath='{.status.conditions[?(@.type=="ScaleToZero")]}{"\n"}'
```

## When a service stays always on

A service that asks to sleep but cannot runs always on, as it did before, and says why:

| Reason | Message | Fix |
| --- | --- | --- |
| `AddonNotInstalled` | "Scale to zero needs the serverless add-on: set spec.addons.serverless.enabled on the GeassCluster" | [Turn on the add-on](#turn-on-the-add-on) and wait for it to finish. |
| `NoPublicHost` | "Scale to zero needs a public host: it is the requests to it that wake the service" | Set a domain on the service, or connect Cloudflare. |
| `AutoscalingSet` | "Scale to zero decides how many copies run, so CPU autoscaling has to be turned off" | Switch **Enable autoscaling** off. |

Geass works this out each time it reconciles a service. Nothing reconciles a service only because the add-on finished installing, so a service that asked too early can keep showing `AddonNotInstalled` until something else touches it. Choose **Redeploy** on the service, or switch **Sleep when idle** off and on again.

## New versions of a sleeping service

A version that cannot start should fail the deploy, not the first visitor. So after any change to what the pods are made from (the image, variables, the command and so on), Geass keeps one copy running until a copy of the new version has started. Only then may the service sleep again, after its idle time. A deploy therefore wakes a sleeping service.

If the new version cannot start, the deploy fails with the reason, for example "The new version of the service cannot start: app is CrashLoopBackOff". Geass reads the reason from the pod: `CrashLoopBackOff`, `ImagePullBackOff`, `ErrImagePull`, `InvalidImageName`, `CreateContainerConfigError`, `CreateContainerError`, or a Deployment that did not become ready in time.

A service that was woken by a request and cannot start is reported the same way: "The service was woken but its copy cannot start: ...". One that is only slow gets 10 minutes: "The service was woken but has not started within 10m0s".

## Pull request environments

The copy of a service in a [pull request environment](/docs/guides/pull-request-environments/) sleeps by default: Geass turns `scaleToZero` on for it with the default idle time, unless the original service has its own `scaleToZero` setting, which the copy inherits. Most open pull requests are idle most of the time. The copy runs always on, with the reason on its condition, when the cluster has no add-on or the copy has no public host. The copy also gets one replica, no autoscaling and no schedules.

## Functions and schedules

A [function](/docs/guides/functions/) is a service, so it can sleep. A schedule on a function calls it through the same proxy, so a run wakes a sleeping function first. Each run waits up to 120 seconds for an answer, and a cold start counts toward that.

## Cloudflare

With the dashboard published through a Cloudflare tunnel, the tunnel reaches services without going through Traefik. Geass sends the route of a sleeping service to the add-on's proxy instead of to the service, so the tunnel wakes it too. Geass works the route out from the service's own settings and from the add-on being installed, and looks again whenever a service changes and every five minutes. See [Domains and TLS](/docs/guides/domains-and-tls/) and [Cloud connections](/docs/guides/connections/).

## Limits

- **HTTP through a public host only.** Nothing else wakes a service.
- **The add-on is never uninstalled by Geass.** Setting `spec.addons.serverless.enabled` back to `false` stops the operator from managing it, and does not remove KEDA. Geass decides whether a service can sleep by whether KEDA's `HTTPScaledObject` kind exists in the cluster, not by that setting, so services keep sleeping after you switch it off. To stop a service sleeping, switch its own setting off (`geass scale web --always-on`). Switching a service off starts it at once with its `replicas` value.
- **Not a place for work that must run continuously.** A background worker with no requests would be stopped.
- **Idle time is measured by the add-on**, from requests to the public host.
- **Your own Traefik settings.** The ingress controller must allow services that are only a name. Geass sets this up on the Traefik that K3s installs, and only when no `traefik` configuration exists. If you have your own, add the setting yourself (see below).

## What happens under the hood

The request path of a service that sleeps:

```text
visitor -> Traefik -> web-wake -> add-on proxy (keda namespace) -> web Service -> pods
```

The service's ingress points at `web-wake` instead of `web`. `web-wake` is a Service of type `ExternalName` that is only a name for the proxy. The proxy tells services apart by host and path. It passes a request on at once when the service has pods. When it has none, it holds the request, asks KEDA to start a copy, and passes the request on when the copy is ready.

| Object | Where | What it is |
| --- | --- | --- |
| HelmChart `geass-keda` and `geass-keda-http` | `kube-system`, installing into `keda` | KEDA 2.21.0 and the HTTP add-on 0.16.0, from `https://kedacore.github.io/charts`. |
| HelmChartConfig `traefik` | `kube-system` | Sets `providers.kubernetesIngress.allowExternalNameServices: true`. Created only when none exists. |
| Service `<service>-wake` | `<project>-<environment>` | `ExternalName` to `keda-add-ons-http-interceptor-proxy.keda.svc.cluster.local`, port 8080. |
| HTTPScaledObject `<service>` | `<project>-<environment>` | Tells the add-on the hosts, paths, target, copy range and idle time. `replicas.min` is 0, or 1 while a new version is being checked. `scaledownPeriod` is `idleSeconds`. |
| Ingress `<service>` | `<project>-<environment>` | Its backend is `<service>-wake` on port 8080. |
| Annotations | The Deployment and the HTTPScaledObject | `geass.dev/template-fingerprint` and `geass.dev/verified-template` record which version has been seen to start. |

The `keda` namespace can reach every project's services, like the other platform namespaces ([Network isolation](/docs/guides/network-isolation/)). While a service sleeps, Geass does not set its Deployment's replica count, so it does not wake the service on each reconcile.

```bash
kubectl -n morning-beach-production get httpscaledobject,service,ingress,deploy
kubectl -n kube-system get helmchartconfig traefik -o yaml
kubectl -n keda get pods
kubectl -n geass-system logs deploy/geass-controller-manager -c manager | grep -i "external name"
```

If you manage Traefik yourself, add this to its Helm values:

```yaml title="traefik values"
providers:
  kubernetesIngress:
    allowExternalNameServices: true
```

## Troubleshooting

**The setting is on and the service never sleeps.**
Read the `ScaleToZero` condition. A reason other than `Active` means it is running always on: see [When a service stays always on](#when-a-service-stays-always-on). With `Active`, something is still requesting the public host. Check for uptime monitors, link checkers and schedules.

**The condition says `AddonNotInstalled` and the add-on is installed.**
Check `kubectl get crd httpscaledobjects.http.keda.sh`. If it exists, choose **Redeploy** on the service so that it is evaluated again.

**The add-on does not finish installing.**
Read the charts and their jobs: `kubectl -n kube-system get helmcharts` and `kubectl -n kube-system logs job/helm-install-geass-keda`. The cluster needs to reach `kedacore.github.io` and the registries the charts pull from. The operator looks again every 20 seconds.

**A visitor gets an error instead of waiting.**
If Traefik answers with an error for the host, it is not allowed to route to a service that is only a name. Check `kubectl -n kube-system get helmchartconfig traefik -o yaml` for `allowExternalNameServices: true`, and the operator log for "Traefik has its own configuration that does not allow external name services".

**The first request takes very long.**
The service is slow to start. The dashboard shows **Setting up** while it does. Check `kubectl -n morning-beach-production get pods` and the pod's events: a large image that is not on the node yet, a slow readiness check, or a node without room.

**The service says "Starting after a request" for a long time.**
After 10 minutes the condition changes to "The service was woken but has not started within 10m0s". Read the pod: `kubectl -n morning-beach-production describe pod <pod>`.

**Another service cannot reach it while it sleeps.**
Calls to its private address do not wake it. Keep it always on, or send the call through its public host.

**Deploying a sleeping service started it.**
Expected: the new version is checked before the service may sleep again. See [New versions of a sleeping service](#new-versions-of-a-sleeping-service).

**I switched the add-on off and services still sleep.**
See [Limits](#limits). Switch each service off.

## Related

- [Scaling and placement](/docs/guides/scaling-and-placement/)
- [Functions](/docs/guides/functions/)
- [Pull request environments](/docs/guides/pull-request-environments/)
- [Domains and TLS](/docs/guides/domains-and-tls/)
- [Network isolation](/docs/guides/network-isolation/)
- [GeassCluster](/docs/reference/resources/geass-cluster/)
- [GeassApp](/docs/reference/resources/geass-app/)
- [Troubleshooting](/docs/operations/troubleshooting/)
