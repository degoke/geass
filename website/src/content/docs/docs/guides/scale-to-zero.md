---
title: "Sleep when idle"
description: "Stop a service when nobody uses it, and start it on the next request. Turn on the serverless add-on, set it for each service, and see what visitors get."
---

A service that sleeps when idle stops after it gets no requests for a set time. The next request starts it again. While the service sleeps, it reserves no CPU or memory. So a small cluster can hold many services that people use only sometimes, for example pull request environments and internal tools. The cost is the first request after a sleep: that request waits while the service starts.

Sleep when idle works for every kind of service: an image, a Dockerfile or GitHub build, or a [function](/docs/guides/functions/). It does not apply to databases or buckets.

## Before you start

- **The Sleep when idle add-on must be on.** It is off by default. See [Turn on the add-on](#turn-on-the-add-on).
- **The service needs a public domain.** A request to that domain wakes the service. Set a domain on the service, or connect Cloudflare to give services a default domain. See [Domains and TLS](/docs/guides/domains-and-tls/).
- **The service cannot use autoscaling.** Both features decide how many copies run. See [Scaling and placement](/docs/guides/scaling-and-placement/).
- To change the setting, you need the admin role, or the project token of the project.

## Turn on the add-on

To turn on the add-on, you must be an admin.

1. Open **Settings** → **Add-ons**.
2. Under **Sleep when idle**, click **Turn on**.
3. Wait until the badge next to the title changes from **Installing** to **Ready**.

The installation takes a few minutes. Your services run as usual while it installs. The add-on installs [KEDA](https://keda.sh) and its HTTP add-on, and needs internet access to `kedacore.github.io`.

To do the same from the command line, run `geass addons enable serverless --wait`. To see the state later, run `geass addons`.

If the badge says **Failed**, read the reason under the title. If the reason names the install job, click **Retry install**, or run `geass addons retry serverless`.

## Make a service sleep

### In the dashboard

1. Open the project, and select the service.
2. Select **Settings**, and then the **Scale** section.
3. Turn on **Sleep when idle**. This action turns off **Enable autoscaling**.
4. Set **Sleep after (minutes)**, from 1 to 1440. The default is 5.
5. Click **Save changes**.

The setting applies immediately, without a deploy. If the service cannot sleep, the reason shows under the switch, and the service keeps running.

### From the command line

```bash
geass scale web --sleep-after 10m   # stop after 10 minutes without requests (30s to 24h)
geass scale web --always-on         # keep it running again
```

An AI agent uses the `set_sleep_when_idle` tool. A script calls `POST /api/apps/<service>/update` with `scaleToZero=on`, `idleSeconds` (30 to 86400), and `scaleToZeroMax` (1 to 100). `scaleToZeroMax` is the most copies that run when the service is busy. The default is 3. See [Command line](/docs/guides/command-line/), [MCP server](/docs/guides/mcp-server/), and the [REST API](/docs/reference/rest-api/).

While the service can sleep, **Replicas** does not apply. It applies again when you turn off sleep.

## What a visitor sees

The first request after a sleep waits while a copy of the service starts. Then the visitor gets the usual response. Later requests have no delay. The wait depends on these items:

- How long the service takes to start.
- Its readiness check.
- Whether its image is already on the server. A first download takes longer.

Every request to a service that can sleep passes through the add-on, also when the service is awake. So each request has one extra hop.

Only requests to the public domain wake the service. Calls from other services to its private address do not wake it. These calls fail while the service sleeps. If other services call a service privately, keep that service always on.

Every request to the public domain counts as use. For example, an uptime monitor that checks the domain every minute keeps the service awake.

## What the dashboard shows

| Moment | Dashboard |
| --- | --- |
| Asleep or running | **Ready** |
| Starting after a request | **Setting up**. The status message is "Starting after a request". |
| Cannot sleep, so it runs always on | **Ready**. The reason shows under **Sleep when idle**. |

A sleeping service has no running copies. So its **Console** tab shows "No running pods", and its **Metrics** charts have gaps. The command line marks the service `(asleep)`.

## When a service stays always on

If a service cannot sleep, it runs always on. The reason is one of these:

| Message | Fix |
| --- | --- |
| "Scale to zero needs the serverless add-on: set spec.addons.serverless.enabled on the GeassCluster" | [Turn on the add-on](#turn-on-the-add-on), and wait for it to finish. |
| "Scale to zero needs a public host: it is the requests to it that wake the service" | Set a domain on the service, or connect Cloudflare. |
| "Scale to zero decides how many copies run, so CPU autoscaling has to be turned off" | Turn off **Enable autoscaling**. |

A service that asked to sleep before the add-on finished can keep the first message. To fix this, click **Redeploy** on the service. You can also turn **Sleep when idle** off and on again.

## New versions of a sleeping service

A deploy wakes a sleeping service. Geass keeps one copy running until the new version starts. Then the service can sleep again after its idle time. So a version that cannot start fails the deploy, not the first visitor.

If the new version cannot start, the deploy fails with the reason, for example "The new version of the service cannot start: app is CrashLoopBackOff". If a request wakes a service that cannot start, the message is "The service was woken but its copy cannot start: ...". If a service takes more than 10 minutes to start, the message is "The service was woken but has not started within 10m0s".

## Pull request environments

The copy of a service in a [pull request environment](/docs/guides/pull-request-environments/) sleeps by default, with the default idle time. If the original service has its own sleep setting, the copy uses that setting. The copy runs always on when the add-on is off or the copy has no public domain.

## Functions and schedules

A [function](/docs/guides/functions/) is a service, so it can sleep. A schedule wakes a sleeping function first. Each run waits up to 120 seconds for a response, and this time includes the start.

## Cloudflare

A Cloudflare tunnel also wakes a sleeping service. Geass sends the tunnel route of the service to the add-on. Geass updates the route when the service changes, and every five minutes. See [Connect Cloudflare](/docs/guides/connectors/cloudflare/).

## Limits

- **Only HTTP requests to a public domain wake a service.**
- **Geass never removes the add-on.** If you turn off the add-on setting, services continue to sleep. To stop a service from sleeping, turn off **Sleep when idle** on that service, or run `geass scale web --always-on`. The service then starts immediately.
- **Do not use sleep for work that must run all the time.** A background worker that gets no requests stops.
- **If you manage your own Traefik settings,** you must allow one more setting yourself. See [Under the hood](#under-the-hood).

## Troubleshooting

**The setting is on, and the service never sleeps.**
If a reason shows under **Sleep when idle**, see [When a service stays always on](#when-a-service-stays-always-on). If not, something sends requests to the public domain. Look for uptime monitors, link checkers, and schedules.

**The service says that the add-on is missing, but you turned it on.**
Open **Settings** → **Add-ons**, and check that **Sleep when idle** says **Ready**. Then click **Redeploy** on the service, so that Geass checks again.

**A visitor gets an error instead of a wait.**
The ingress controller cannot route to the add-on. This happens when you manage your own Traefik settings. See [Under the hood](#under-the-hood).

**The first request takes very long.**
The service is slow to start. The dashboard shows **Setting up** while it starts. Look for a large image, a slow readiness check, or a full server.

**The service says "Starting after a request" for a long time.**
After 10 minutes, the message changes to "The service was woken but has not started within 10m0s". Read the logs of the service. See [Logs, metrics and terminal](/docs/guides/observability/).

**Another service cannot reach it while it sleeps.**
Calls to the private address do not wake the service. Keep the service always on, or call its public domain.

**A deploy started a sleeping service.**
This is expected. See [New versions of a sleeping service](#new-versions-of-a-sleeping-service).

**You turned off the add-on, and services still sleep.**
See [Limits](#limits). Turn off sleep for each service.

## Under the hood

### The add-on setting

The **Add-ons** page sets `spec.addons.serverless.enabled` on the `GeassCluster` named `default`. The operator writes the state of the add-on to `status.addons`. **Retry install** deletes the stuck install job of the chart, and K3s then makes a new one.

### The path of a request

A request to a service that can sleep goes this way:

```text
visitor -> Traefik -> web-wake -> KEDA HTTP proxy (keda namespace) -> web Service -> pods
```

Geass creates an `HTTPScaledObject` and a `<service>-wake` Service in the namespace of the environment. The status of the service has a `ScaleToZero` condition. To check the add-on and a service, run these commands:

```bash
kubectl -n kube-system get helmcharts geass-keda geass-keda-http
kubectl -n kube-system logs job/helm-install-geass-keda
kubectl -n keda get pods
kubectl get crd httpscaledobjects.http.keda.sh
kubectl -n geass-system get geassapp web -o jsonpath='{.status.conditions[?(@.type=="ScaleToZero")]}{"\n"}'
kubectl -n morning-beach-production get httpscaledobject,service,ingress,deploy
```

Traefik must allow Services of type `ExternalName`. Geass sets this on the Traefik of K3s, but only when no `traefik` HelmChartConfig exists. If you have your own, add this to its values:

```yaml title="traefik values"
providers:
  kubernetesIngress:
    allowExternalNameServices: true
```

If Traefik blocks it, the Geass log says "Traefik has its own configuration that does not allow external name services".

## Related

- [Scaling and placement](/docs/guides/scaling-and-placement/)
- [Functions](/docs/guides/functions/)
- [Pull request environments](/docs/guides/pull-request-environments/)
- [Domains and TLS](/docs/guides/domains-and-tls/)
- [Network isolation](/docs/guides/network-isolation/)
- [Troubleshooting](/docs/operations/troubleshooting/)
