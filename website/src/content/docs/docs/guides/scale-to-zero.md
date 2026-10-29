---
title: "Sleep when idle"
description: "Stop a service when nobody uses it, and start it on the next request. Turn on the serverless add-on, set it for each service, and see what visitors get."
---

A service that sleeps when idle stops after it has had no requests for a set time. The next request starts it again. While the service sleeps, it reserves no CPU or memory. Thus a small cluster can hold many services that people use only sometimes, such as pull request environments and internal tools. The cost is the first request after a sleep: that request waits while the service starts.

Sleep when idle works for every kind of service: an image, a Dockerfile or GitHub build, or a [function](/docs/guides/functions/). It does not apply to databases or buckets.

## Before you start

- **The serverless add-on must be on for the cluster.** The add-on installs [KEDA](https://keda.sh) and its HTTP add-on. The add-on is off by default, and the installer does not turn it on. The dashboard has no switch for it. See [Turn on the add-on](#turn-on-the-add-on).
- **The service needs a public host.** A request to that host wakes the service. Set a domain on the service ([Domains and TLS](/docs/guides/domains-and-tls/)). Or connect Cloudflare, so that services without a domain get a default domain.
- **The service cannot use CPU autoscaling at the same time.** Both features try to decide how many copies run. See [Scaling and placement](/docs/guides/scaling-and-placement/).
- To change the setting, you need one of these accesses:
  - You are a dashboard admin.
  - You have a workspace token with the admin role.
  - You have the project token of the project of the service.

## Turn on the add-on

On the control plane, set `spec.addons.serverless.enabled` on the cluster definition:

```bash
kubectl -n geass-system patch geasscluster default --type merge \
  -p '{"spec":{"addons":{"serverless":{"enabled":true}}}}'
```

The operator installs KEDA first, and then the HTTP add-on. Then it lets the ingress controller route to the add-on. The installation takes a few minutes. The installation never holds the cluster back. While the add-on installs, `Ready` and `AddonsReady` on the cluster do not change, and every service runs awake. To check the progress, run these commands:

```bash
kubectl -n kube-system get helmcharts geass-keda geass-keda-http
kubectl -n keda get pods
kubectl get crd httpscaledobjects.http.keda.sh
```

If you turn the add-on off again, Geass does not uninstall KEDA. For the effect on services that already sleep, see [Limits](#limits).

## Make a service sleep

### In the dashboard

1. Open the project and select the service.
2. Select **Settings**, and then select the **Scale** section.
3. Under **Sleep when idle**, turn on **Sleep when idle**. This action turns off **Enable autoscaling**.
4. Set **Sleep after (minutes)**, from 1 to 1440. The default is 5.
5. Click **Save changes**.

The setting applies immediately, and you do not have to deploy. If the service cannot sleep yet, the block gives the reason and adds "Until then it keeps running."

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

The fields have these values:

- `scaleToZero` is `on` or `off`.
- `idleSeconds` is 30 to 86400.
- `scaleToZeroMax` is 1 to 100.

A field that you do not send keeps its value. The versioned API has the same fields (`PATCH /api/v1/apps/{name}`). An agent uses the `set_sleep_when_idle` tool. See [Command line](/docs/guides/command-line/), [MCP server](/docs/guides/mcp-server/), [CLI commands](/docs/reference/cli/), and [MCP tools](/docs/reference/mcp-tools/).

If you send `scaleToZero=on` while the service uses autoscaling, Geass refuses the request with "Scale to zero decides how many copies run, so turn CPU autoscaling off to use it".

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
| `spec.scaleToZero.idleSeconds` | The time without a request before the service stops. 30 to 86400. | `300` |
| `spec.scaleToZero.maxReplicas` | The maximum number of copies that run when the service is awake and busy. 1 to 100. | `3` |

While sleep when idle is on, the add-on decides how many copies run, from zero to `maxReplicas`. The `replicas` value of the service does not apply. The `replicas` value applies again when you turn sleeping off.

## What a visitor experiences

The add-on holds the first request after a sleep. It does not refuse that request. The visitor waits while a copy of the service starts, and then gets the usual response. Later requests have no delay. The length of the wait depends on these items of the service:

- Its start-up time.
- Its readiness check.
- Whether its image is already on the node. A first pull takes longer.

The settings of the KEDA HTTP add-on set the maximum wait. Geass keeps these settings at their defaults.

Every request to a service that can sleep goes through the proxy of the add-on, when the service is awake and when it sleeps. Thus each request has one extra hop.

A service wakes only for requests that come through its public host. Calls from another service to its private address (`<service>.<project>-<environment>.svc.cluster.local`) do not wake it. These calls fail to connect while the service sleeps. If other services call a service privately, keep that service always on.

Each request to the public host counts as use. An uptime monitor that checks the host every minute keeps the service awake.

## What the dashboard shows

| Moment | Dashboard | `ScaleToZero` condition | `GET /api/v1/apps/{name}/status` |
| --- | --- | --- | --- |
| Asleep or running | **Ready** | `True`, reason `Active`, "Sleeps after idle, wakes on the next request" | `state` is `ready`. Asleep also has `replicas.desired: 0` and `sleeping: true`. |
| Starting after a request | **Setting up** | `True` | `state` is `pending`, with the message "Starting after a request" |
| Cannot sleep, runs always on | **Ready** | `False`, with a reason from the next section | `state` is `ready` |

A sleeping service has no pods. Thus its **Console** tab shows "No running pods", and its **Metrics** charts have gaps. The command line marks the service `(asleep)`.

The `ScaleToZero` condition exists only on services that asked to sleep. To read the condition, run this command:

```bash
kubectl -n geass-system get geassapp web \
  -o jsonpath='{.status.conditions[?(@.type=="ScaleToZero")]}{"\n"}'
```

## When a service stays always on

If a service asks to sleep but cannot sleep, it runs always on, as it did before. The service gives the reason:

| Reason | Message | Fix |
| --- | --- | --- |
| `AddonNotInstalled` | "Scale to zero needs the serverless add-on: set spec.addons.serverless.enabled on the GeassCluster" | [Turn on the add-on](#turn-on-the-add-on) and wait for it to finish. |
| `NoPublicHost` | "Scale to zero needs a public host: it is the requests to it that wake the service" | Set a domain on the service, or connect Cloudflare. |
| `AutoscalingSet` | "Scale to zero decides how many copies run, so CPU autoscaling has to be turned off" | Turn off **Enable autoscaling**. |

Geass calculates the reason each time it reconciles a service. Nothing reconciles a service only because the add-on finished its installation. Thus a service that asked too early can continue to show `AddonNotInstalled` until something else changes the service. To fix this, click **Redeploy** on the service, or turn **Sleep when idle** off and on again.

## New versions of a sleeping service

A version that cannot start must fail the deploy, not the first visitor. Thus Geass acts after each change to what makes the pods, for example the image, the variables, or the command. Geass keeps one copy running until a copy of the new version has started. Only then can the service sleep again, after its idle time. Thus a deploy wakes a sleeping service.

If the new version cannot start, the deploy fails with the reason, for example "The new version of the service cannot start: app is CrashLoopBackOff". Geass reads the reason from the pod. The reason is one of these:

- `CrashLoopBackOff`
- `ImagePullBackOff`
- `ErrImagePull`
- `InvalidImageName`
- `CreateContainerConfigError`
- `CreateContainerError`
- A Deployment that did not become ready in time.

If a request wakes a service and the service cannot start, Geass reports it in the same way: "The service was woken but its copy cannot start: ...". A service that is only slow gets 10 minutes: "The service was woken but has not started within 10m0s".

## Pull request environments

The copy of a service in a [pull request environment](/docs/guides/pull-request-environments/) sleeps by default. Geass turns on `scaleToZero` for the copy with the default idle time. If the original service has its own `scaleToZero` setting, the copy inherits that setting instead. Most open pull requests are idle most of the time.

The copy runs always on, with the reason on its condition, in these cases:

- The cluster has no add-on.
- The copy has no public host.

The copy also gets one replica, no autoscaling, and no schedules.

## Functions and schedules

A [function](/docs/guides/functions/) is a service. Thus it can sleep. A schedule on a function calls the function through the same proxy. Thus a run wakes a sleeping function first. Each run waits a maximum of 120 seconds for a response, and a cold start is part of that time.

## Cloudflare

If you publish the dashboard through a Cloudflare tunnel, the tunnel reaches services directly, not through Traefik. Geass sends the route of a sleeping service to the proxy of the add-on, not to the service. Thus the tunnel also wakes the service. Geass calculates the route from the settings of the service and from whether the add-on is installed. Geass calculates the route again when a service changes, and every five minutes. See [Domains and TLS](/docs/guides/domains-and-tls/) and [Connect Cloudflare](/docs/guides/connectors/cloudflare/).

## Limits

- **HTTP through a public host only.** Nothing else wakes a service.
- **The add-on is never uninstalled by Geass.** If you set `spec.addons.serverless.enabled` back to `false`, the operator stops its management of the add-on. The operator does not remove KEDA. Geass decides whether a service can sleep from whether the KEDA `HTTPScaledObject` kind exists in the cluster, not from that setting. Thus services continue to sleep after you turn the add-on off. To stop the sleep of a service, turn off the setting of that service (`geass scale web --always-on`). When you turn off sleeping for a service, the service starts immediately with its `replicas` value.
- **Not a place for work that must run continuously.** The add-on stops a background worker that gets no requests.
- **Idle time is measured by the add-on**, from requests to the public host.
- **Your own Traefik settings.** The ingress controller must allow services that are only a name. Geass sets this up on the Traefik that K3s installs, and only when no `traefik` configuration exists. If you have your own configuration, add the setting yourself, as shown in the next section.

## What happens under the hood

The request path of a service that sleeps:

```text
visitor -> Traefik -> web-wake -> add-on proxy (keda namespace) -> web Service -> pods
```

The ingress of the service points to `web-wake`, not to `web`. `web-wake` is a Service of type `ExternalName` that is only a name for the proxy. The proxy finds the service from the host and the path. If the service has pods, the proxy sends the request on immediately. If the service has no pods, the proxy does these steps:

1. It holds the request.
2. It asks KEDA to start a copy.
3. It sends the request on when the copy is ready.

| Object | Where | What it is |
| --- | --- | --- |
| HelmChart `geass-keda` and `geass-keda-http` | `kube-system`, installing into `keda` | KEDA 2.21.0 and the HTTP add-on 0.16.0, from `https://kedacore.github.io/charts`. |
| HelmChartConfig `traefik` | `kube-system` | Sets `providers.kubernetesIngress.allowExternalNameServices: true`. Geass creates it only when none exists. |
| Service `<service>-wake` | `<project>-<environment>` | `ExternalName` to `keda-add-ons-http-interceptor-proxy.keda.svc.cluster.local`, port 8080. |
| HTTPScaledObject `<service>` | `<project>-<environment>` | Tells the add-on the hosts, the paths, the target, the copy range, and the idle time. `replicas.min` is 0, or 1 while Geass checks a new version. `scaledownPeriod` is `idleSeconds`. |
| Ingress `<service>` | `<project>-<environment>` | Its backend is `<service>-wake` on port 8080. |
| Annotations | The Deployment and the HTTPScaledObject | `geass.dev/template-fingerprint` and `geass.dev/verified-template` record which version Geass saw start. |

The `keda` namespace can reach the services of every project, like the other platform namespaces ([Network isolation](/docs/guides/network-isolation/)). While a service sleeps, Geass does not set the replica count of its Deployment. Thus Geass does not wake the service on each reconcile.

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
Read the `ScaleToZero` condition. If the reason is not `Active`, the service runs always on. See [When a service stays always on](#when-a-service-stays-always-on). If the reason is `Active`, something still sends requests to the public host. Look for uptime monitors, link checkers, and schedules.

**The condition says `AddonNotInstalled` and the add-on is installed.**
Run `kubectl get crd httpscaledobjects.http.keda.sh`. If the CRD exists, click **Redeploy** on the service, so that Geass evaluates it again.

**The add-on does not finish installing.**
Read the charts and their jobs: `kubectl -n kube-system get helmcharts` and `kubectl -n kube-system logs job/helm-install-geass-keda`. The cluster must reach `kedacore.github.io` and the registries that the charts pull from. The operator checks again every 20 seconds.

**A visitor gets an error instead of waiting.**
If Traefik gives an error for the host, Traefik cannot route to a service that is only a name. Do these checks:

- Look for `allowExternalNameServices: true` in the output of `kubectl -n kube-system get helmchartconfig traefik -o yaml`.
- Look for "Traefik has its own configuration that does not allow external name services" in the operator log.

**The first request takes very long.**
The service is slow to start. The dashboard shows **Setting up** while the service starts. Run `kubectl -n morning-beach-production get pods`, and read the events of the pod. Look for these causes:

- A large image that is not on the node yet.
- A slow readiness check.
- A node without space.

**The service says "Starting after a request" for a long time.**
After 10 minutes the condition changes to "The service was woken but has not started within 10m0s". Read the pod: `kubectl -n morning-beach-production describe pod <pod>`.

**Another service cannot reach it while it sleeps.**
Calls to its private address do not wake the service. Keep the service always on, or send the call through its public host.

**Deploying a sleeping service started it.**
This result is expected. Geass checks the new version before the service can sleep again. See [New versions of a sleeping service](#new-versions-of-a-sleeping-service).

**I switched the add-on off and services still sleep.**
See [Limits](#limits). Turn off sleeping for each service.

## Related

- [Scaling and placement](/docs/guides/scaling-and-placement/)
- [Functions](/docs/guides/functions/)
- [Pull request environments](/docs/guides/pull-request-environments/)
- [Domains and TLS](/docs/guides/domains-and-tls/)
- [Network isolation](/docs/guides/network-isolation/)
- [GeassCluster](/docs/reference/resources/geass-cluster/)
- [GeassApp](/docs/reference/resources/geass-app/)
- [Troubleshooting](/docs/operations/troubleshooting/)
