---
title: "Abuse detection"
description: "Find organizations' services that might mine or abuse a shared cluster: what Geass can and cannot see, how to review suspects, and how to connect Falco."
---

On a cluster that several [organizations](/docs/guides/organizations-and-workspaces/) share, the classic abuse is free compute. For example, a stranger's service mines a cryptocurrency on the platform's machines, or does other work that nobody pays for. **Abuse detection** looks for the services of organizations' projects that might do this. It tells the platform's administrators about them. If you tell it to, it also scales a suspect to zero until a person has examined it.

Abuse detection is off until the platform sets it. If the platform sets nothing, Geass does not change: it samples nothing, writes nothing, and touches no service.

**What it finds is a signal, never proof.** Geass sees a container from the outside. It sees how much CPU the container uses, how many requests reach it, and what a detector that you run tells Geass. A miner uses all the CPU that it is allowed, for hours, while nobody calls it. A batch job, a render, the training of a model, a load test, and a queue worker that is behind do the same. For these reasons, a person reviews every suspect, the default action changes nothing, and there are exemptions.

## What it can see, and what it cannot

| Input | What it says | From | Firm? |
| --- | --- | --- | --- |
| **Sustained CPU** | A service whose copies use nearly all the CPU that they are allowed (90% by default), without a break, for a window (30 minutes by default). | The cluster's metrics server (`metrics.k8s.io`). K3s runs it, and a service's CPU autoscaler already needs it. You install nothing. | **A signal.** Legitimate heavy work looks the same. See [False positives](#false-positives). |
| **Few requests** (optional) | That nobody calls the busy service. Nobody calls a miner, but somebody always calls a busy web service. | The requests that the ingress (Traefik) passes to the service, from the cluster's Prometheus. Only while the [monitoring add-on](/docs/guides/observability/) is on. | **A signal**, and a partial one: Geass sees only the requests that go through the ingress. See [Quiet services](#quiet-services). |
| **A report from a detector outside Geass** | What that detector saw. For example, a process called `xmrig`, a connection to a pool, or a stratum handshake. | Falco (through falcosidekick), Tetragon, or the provider's own tooling sends it to the API. Geass installs none of these tools. | As good as the detector and its rule. See [Outside detectors](#outside-detectors). |
| **Mining pools' ports** | Nothing. The ports are closed, not watched. | The [outbound limits](/docs/guides/outbound-limits/#mining) (`miningPools: Blocked`). | **A heuristic** that a miner on port 443 goes around. It detects nothing. |

Abuse detection does **not** detect these things:

- **A miner that keeps below the threshold.** Geass never suspects a miner that is throttled to 80% of its limit, or that runs "low and slow". The threshold is the line, and a careful miner stays under it.
- **A miner that pauses** for one sample in each window, or that restarts itself more often than the window. Every break starts the count again. This rule keeps restarts and rollouts out of the count, and it is also a way around the signal.
- **A miner that sleeps**, or that mines only at night. Geass judges each run separately.
- **Mining on a GPU.** Geass sees the CPU. A service that has a GPU and uses all of it shows nothing here.
- **What a program is, or where it connects.** Geass does not look inside containers. A miner that connects to a pool on port 443, or through a proxy, looks like any other client.
- **Builds.** A build is not a service. A build runs the organization's Dockerfile, and Geass does not examine a build, even one that mines for its whole length. Builds have their own [limits](/docs/guides/tenant-limits/) and [sandbox](/docs/operations/sandboxed-runtime/).
- **Databases, buckets, and the platform's own services**, and the projects that belong to no organization. Geass examines only the services and functions of organizations' projects.
- **What one organization costs in total.** The [usage meter](/docs/guides/usage-and-capacity/) bills what is reserved. Abuse detection looks for patterns of use.

## Before you start

- You have set up organizations, and you have given projects to them (`spec.org`).
- **The metrics server answers.** K3s runs one, unless someone started K3s with `--disable=metrics-server`. To check, run `kubectl top pods -A`. Without a metrics server, the signal measures nothing, and the API says so (`cpu.metricsServer.available: false`).
- **Containers have a CPU limit, or at least a request.** The share is what a service uses, divided by what it is allowed. A container with neither has no share to take, and Geass never measures its service. The [noisy-neighbour limits](/docs/guides/tenant-limits/) give every container of an organization a default limit. Set them first.
- Only a platform administrator can turn abuse detection on. To turn it on, use `kubectl` or any other tool that writes the resources. Only the platform's administrators see the suspects. No page of the dashboard shows them yet. Use the [API](#review-a-suspect) to read and review them.
- **What it does to running services.** With the default action, `None`, it does nothing: Geass marks a suspect as a suspect, and the suspect continues to run. With `Suspend`, Geass scales a suspect to zero immediately when it suspects it. Start with `None`, and watch what Geass finds for some time. Switch to `Suspend` after you trust the settings on your cluster. **Switching does not reach a service that is already a suspect**: see [Suspend](#suspend).

## Turn it on

The settings are in the [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/), under `spec.tenancy.abuse`. If you set the section at all, abuse detection turns on with its defaults. Every field in the section is optional:

```yaml title="platform-abuse.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassPlatformConfig
metadata:
  name: platform
  namespace: geass-system
spec:
  tenancy:
    abuse:
      action: None                  # the default; Suspend scales a suspect to zero
      reviewExemptionDays: 30       # how long a cleared service is left alone
      cpu:
        thresholdPercent: 90        # of what the copies are allowed (limit, or request without one)
        windowMinutes: 30           # without a break
        quietTraffic:               # optional: count only while the service is sent few requests
          maxRequestsPerMinute: 6
          withoutTrafficData:       # when the requests cannot be read: stricter
            thresholdPercent: 95
            windowMinutes: 60
```

Patch the section into the existing resource. Do not apply the fragment alone:

```bash
kubectl -n geass-system patch geassplatformconfig platform --type merge -p '{"spec":{"tenancy":{"abuse":{}}}}'
```

This command sets the defaults: 90% for 30 minutes, and the action `None`. To turn abuse detection off, run `kubectl -n geass-system patch geassplatformconfig platform --type merge -p '{"spec":{"tenancy":{"abuse":null}}}'`. For what that does to suspects, see [Turn it off](#turn-it-off).

| Field | Default | Meaning |
| --- | --- | --- |
| `action` | `None` | What Geass does to a suspect. `None` marks the suspect and changes nothing. `Suspend` also scales it to zero until a platform administrator releases or clears it. Geass reads the action when it raises a suspicion. If you switch the action to `Suspend` later, Geass does not suspend a service that is already a suspect. |
| `reviewExemptionDays` | 30 | 1 to 365. How long the CPU signal leaves a service alone after a platform administrator has cleared it. |
| `cpu.thresholdPercent` | 90 | 50 to 100. The share of what its copies are allowed that a service must use in every sample. |
| `cpu.windowMinutes` | 30 | 10 to 1440. How long the use must last without a break. |
| `cpu.quietTraffic.maxRequestsPerMinute` | none (requests not considered) | 0 or more. When you set it, a sample counts only while the ingress passes the service at most this many requests a minute, averaged over five minutes. |
| `cpu.quietTraffic.withoutTrafficData.thresholdPercent` | the larger of 95 and `thresholdPercent` | 50 to 100. Replaces the threshold while Geass cannot read the requests. |
| `cpu.quietTraffic.withoutTrafficData.windowMinutes` | twice `windowMinutes`, at most 1440 | 10 to 1440. Replaces the window while Geass cannot read the requests. |

Geass applies the platform's settings to an organization. Where the organization's own `spec.abuse` on the [GeassOrganization](/docs/reference/resources/geass-organization/) sets a field, that field replaces the platform's setting:

| Field | Meaning |
| --- | --- |
| `spec.abuse.exempt` | `true` leaves every service of the organization out of the CPU signal. A report from an outside detector still applies. |
| `spec.abuse.exemptProjects` | The organization's projects whose services the CPU signal leaves out. A report still applies. |
| `spec.abuse.action` | `None` or `Suspend`, in place of the platform's action, for the organization's suspects. |

```bash
# An organization that renders video all night: its render project is left out.
kubectl -n geass-system patch geassorganization org-1a2b3c4d --type merge -p '{"spec":{"abuse":{"exemptProjects":["proj-render"]}}}'
```

Like the rest of an organization's spec, only the platform writes `spec.abuse`. No route of the dashboard changes it, and an organization's people cannot exempt themselves.

## How the CPU signal works

Every minute, the operator that holds the leader lease examines every deployed service of every organization's project. It skips the services that are exempt, already suspected, or cleared. For each other service, it takes a **sample**. A sample is what all the service's copies use, divided by what they are allowed together:

- The use comes from the metrics server. Each copy's use is the metrics server's average over its own short window, which is 15 seconds on K3s.
- The allowance is each container's CPU limit, or its request when it has no limit.

A container that runs beside the service for the pod's life, such as a sidecar, counts in both values.

Geass measures a service only when the service is **settled**. A settled service meets these conditions:

- Every copy that its Deployment asks for is there, up to date, and ready.
- Each copy has been running for **three minutes**, and each of its containers has been running for three minutes since it last started.

Geass does not take a sample of a service that is not settled, and that missing sample ends the service's count. For this reason, these events never add up to a suspicion:

- **A rollout.** New copies start busy, next to the old ones. For example, a runtime warms up, a migration runs, or a cache fills. The count starts again from zero, three minutes after the rollout ends.
- **A restart.** A copy whose container restarted starts a new count, even when the restart is quick.
- **A change in the number of copies**, by a person, by the CPU autoscaler, or by scale to zero when it wakes the service. Geass suspects an autoscaled service only after the service has stayed at one number of copies for the whole window.
- **No copies at all**, because the service is stopped, asleep under [scale to zero](/docs/guides/scale-to-zero/), or suspended.

A **run** is an unbroken string of samples at or above the threshold. These events end a run:

- A sample below the threshold.
- A sample that Geass did not take.
- A sample of other copies.
- A gap of more than three minutes. For example, the operator restarts for a long time, or the metrics server does not answer.

A run that has lasted the window makes the service a **suspect**. A run has lasted the window when its first and its latest sample are the window apart. With the defaults, this is the 31st sample. Geass records the evidence with the suspect: the run's average and least share, the threshold and window, and the number of samples in the run.

Geass keeps the runs in the ConfigMap `geass-abuse-signal` in `geass-system`, and writes the ConfigMap every minute. An operator that restarts, or a new leader, reads the ConfigMap. It continues from the last sample, if that sample was no more than three minutes earlier. A service at rest has no run. So the ConfigMap holds only the services that are busy now, and never more than 2,000 of them: those that have been busy longest. Geass keeps the runs out of the services' own status on purpose. A write every minute to every busy service would wake their controller every minute.

## Quiet services

A busy service that receives calls is doing its job. With `quietTraffic`, a sample counts only while the ingress passes the service at most `maxRequestsPerMinute` requests a minute, averaged over the last five minutes. So Geass never suspects a web service at its limit under load, but it does suspect a miner, which nobody calls.

The requests are what Traefik counts for the service (`traefik_service_requests_total`). Only the cluster's Prometheus has that count, and only while the [monitoring add-on](/docs/guides/observability/) is on and scrapes the ingress. **When they cannot be read**, Geass cannot tell a busy service from a quiet one. This happens in these cases:

- The add-on is off.
- Prometheus does not answer.
- Prometheus has no request metrics at all. Without this case, missing request metrics would make every service look quiet.

In these cases, the stricter `withoutTrafficData` settings apply instead: by default, 95% for twice the window. If Geass took one sample of a run without the requests, the whole run must last the longer window. The API says whether Prometheus answered at the last sample.

The request count does not include requests that reach the service from inside the cluster, not through the ingress. A worker that a queue feeds, or a service that only other services call, always looks quiet. Geass judges such a service on its CPU alone. Geass does not read a store of metrics outside the cluster (`spec.observability.external`).

## What a suspect gets

| Where | What |
| --- | --- |
| **The service**: condition `AbuseSuspected` | `True`, reason `SustainedCPU` (the CPU signal), `Reported` (an outside detector), or `Suspended` (either one, and scaled to zero). The message gives the evidence and says that it is a signal. `False`, reason `Reviewed`, while Geass leaves a cleared service alone. A service that Geass has never suspected has no such condition. |
| **The project**: condition `AbuseSuspected` | `True`, reason `ServicesSuspected`, while any service is suspected. The condition names the suspected services, and which of them are suspended. No condition otherwise. |
| **An event on the service** | `Warning` `AbuseSuspected` one time, when the service becomes a suspect. `Normal` `AbuseReleased` when a platform administrator releases a suspension, and `AbuseReviewed` when one clears the service. |
| **An alert** | **Abuse suspected** (`abuse.suspected`), to the platform's [alert channels](/docs/guides/alerts/): warning, or critical for a suspended service. The alert carries no project. So no organization's channel receives it, and a platform channel that is limited to some projects does not receive it either. |
| **The API** | [`GET /api/v1/abuse/suspects`](#review-a-suspect), with the evidence, for the platform's administrators only. |

A suspicion stays until a platform administrator clears it. If a miner stops after Geass suspects it, or after Geass scales it to zero, the suspicion stays. Geass keeps the decisions on the service, in two annotations:

- `geass.dev/abuse-suspect`: the suspicion, its evidence, and the action that Geass took.
- `geass.dev/abuse-review`: a clearing, its end time, and who made it.

Geass and the platform's administrators write these annotations. The dashboard gives an organization's people no way to set a service's annotations.

**The organization sees the conditions and the event on its own service**, on purpose. The owner of a legitimate heavy job learns why Geass stopped it, and asks for an exemption. It also means that a miner can learn that Geass saw it. The organization never sees these things:

- Another organization's suspects.
- The services that Geass is counting.
- How close its own services are to the threshold.
- The alert.

## Suspend

With `action: Suspend`, or an organization's own `Suspend` action, Geass scales a suspect to **zero copies** immediately when it suspects it. This applies to a suspicion from a report and to one from the CPU signal. The service stays at zero until a platform administrator releases or clears it:

- Geass keeps its Deployment at zero copies. When someone saves or deploys the service, its pods change, but the Deployment stays at zero copies.
- Geass sets [scale to zero](/docs/guides/scale-to-zero/) aside, so that a request cannot wake the service. Its condition `ScaleToZero` says `Suspended`. Geass puts scale to zero back on release.
- Geass leaves a CPU autoscaler in place, because Kubernetes does not scale a Deployment that is at zero.
- Its `Ready` condition is `False`, reason `Suspended`, with the reason. Geass does not count this as a failed deploy. No **Deploy failed** or **Service down** alert follows, and Geass reports no success to GitHub or to webhooks.
- **Nothing is deleted**: the service, its settings, its Service and ingress, its volumes, and its databases stay as they were. On release, its copies come back, as many as it asks for.

**The action is decided when the suspicion is raised, and written into it**. The field `suspended` in the annotation `geass.dev/abuse-suspect` records it. If you switch the platform's action from `None` to `Suspend` later, Geass does not suspend a service that is already a suspect. A test on K3s v1.36.4+k3s1 showed this:

- The service that Geass found under `None` continued to run for the 2.5 minutes that it was watched after the switch.
- `GET /api/v1/abuse/suspects` showed `suspended: false` for the service.
- `release` of the service answered `409 the service is not suspended`.

A report about a service that is already a suspect leaves the service as it is (`alreadySuspected: true`). The CPU signal does not raise a second suspicion while one stands. To suspend such a service, clear it, and then report it. A clearing does not hold a report back. On that cluster, a report about a cleared service made it a suspect again and suspended it. Geass suspended the services that it found after the switch when it found them.

An organization can still delete a suspended service and create it again with the same name. The new service starts without a suspicion, and the CPU signal finds it again after a window. You must deal with an organization that does this. You can [suspend the organization](/docs/guides/organizations-and-workspaces/) (`spec.suspended`), lower its [limits](/docs/guides/tenant-limits/), or remove it.

## Review a suspect

The API is for the platform's administrators only, signed in or with an install-wide admin token. Geass refuses an organization's administrator, a viewer of the platform, an observer, and a project token with `403`, and tells them nothing.

```bash
curl -H "Authorization: Bearer $GEASS_TOKEN" https://geass.example.com/api/v1/abuse/suspects
```

```json title="GET /api/v1/abuse/suspects (one suspect shown)"
{
  "enabled": true,
  "note": "Every suspect is a signal, not proof: ...",
  "action": "None",
  "cpu": {
    "thresholdPercent": 90, "windowMinutes": 30, "source": "metrics.k8s.io",
    "metricsServer": { "available": true }, "sampledAt": "2026-10-27T12:00:00Z"
  },
  "suspects": [
    {
      "service": "worker", "displayName": "worker", "project": "proj-1a2b3c4d", "environment": "production",
      "namespace": "proj-1a2b3c4d-production", "org": "org-1a2b3c4d",
      "source": "cpu", "since": "2026-10-27T10:58:00Z", "detectedAt": "2026-10-27T11:28:00Z",
      "averagePercent": 98.4, "minPercent": 93.1, "thresholdPercent": 90, "windowMinutes": 30, "samples": 31,
      "suspended": false,
      "message": "Abuse is suspected. Its copies used 98.4% on average (never below 93.1%) of the CPU they are allowed, ..."
    }
  ],
  "counting": [
    { "service": "api", "org": "org-5e6f7a8b", "since": "2026-10-27T11:50:00Z", "samples": 11, "averagePercent": 96.0, "...": "..." }
  ],
  "reviewed": [
    { "service": "render", "org": "org-1a2b3c4d", "until": "2026-11-20T09:00:00Z", "by": "root", "note": "nightly render", "...": "..." }
  ]
}
```

- `?org=` keeps only one organization's entries.
- `counting` is the services that the CPU signal counts now, which are not suspects yet.
- A report has `reporter`, `detail`, `pod`, and `priority` in place of the CPU evidence.
- `traffic` says `quiet` (few requests in every sample) or `unknown` (counted without them, at the stricter threshold and window).

Then, for each suspect, examine what it runs: its image, its logs, and its metrics in the dashboard. Then decide what to do:

| Request | What it does |
| --- | --- |
| `POST /api/v1/abuse/suspects/{service}/clear` | It was legitimate. The suspicion goes, and a suspended service runs again. The CPU signal leaves the service alone for `days` (1 to 365, default `reviewExemptionDays`), with your `note`. A report from an outside detector still applies. You can also clear a service that is not suspected, before a heavy job that you know of. |
| `POST /api/v1/abuse/suspects/{service}/release` | Let a suspended service run again while you examine it. It stays suspected, and Geass does not suspend it again. |
| Nothing | Leave it suspected, and suspended if it was. |
| Deal with the organization | Suspend the organization, remove the service yourself, or remove the organization. Geass never deletes anything because of a suspicion. |

```bash
curl -X POST -H "Authorization: Bearer $GEASS_TOKEN" -H "Content-Type: application/json" \
  https://geass.example.com/api/v1/abuse/suspects/worker/clear -d '{"days": 14, "note": "nightly batch, asked the customer"}'
# {"service":"worker","wasSuspected":true,"reviewedUntil":"2026-11-10T12:00:00Z","message":"Cleared. ..."}
```

All these routes answer `409` while abuse detection is off.

## Outside detectors

A tool that watches what programs do on the nodes sees what Geass cannot see: the name and arguments of a process, the connections that it opens, and a stratum handshake. Geass does not bundle or install such a tool. If you run one, it can report a workload to `POST /api/v1/abuse/reports` with a platform administrator's token. The workload then gets the same suspicion, condition, event, and action as from the CPU signal. Exemptions and reviews are for the CPU signal only, and do not hold a report back. A report replaces a review that the service had.

The body names the service, or a pod that is one of its copies:

| Field | Meaning |
| --- | --- |
| `service` | The service, by name. Or: |
| `namespace`, `pod` | The pod and its namespace. Geass finds a pod that is already gone by its name (`<service>-<hash>-<suffix>`). |
| `reporter`, `reason`, `priority`, `time` | Who reports it, what was seen, how urgent it is, and when (RFC 3339). What was seen is a rule's name, and Geass shows it to the organization on its service. |

**Falco** can send its events unchanged, through the webhook output of [falcosidekick](https://github.com/falcosecurity/falcosidekick). Geass reads the rule, the priority, the time, and the namespace and pod from `output_fields` (`k8s.ns.name`, `k8s.pod.name`). Geass keeps nothing else, because Falco's `output` and the command line might hold secrets, and the organization reads the detail. Send only the rules that you mean as reports, such as Falco's mining rules:

```yaml title="falcosidekick values (Helm)"
config:
  webhook:
    address: https://geass.example.com/api/v1/abuse/reports
    customHeaders: "Authorization: Bearer <a platform administrator's token>"
    minimumpriority: critical
```

The format of `customHeaders` depends on where you configure falcosidekick. In its own configuration file, it is a map. In the Helm chart's values, it is `key:value` pairs. Check the falcosidekick documentation for your version. This configuration was not run against a real falcosidekick. The body that Geass reads is Falco's documented JSON output.

If a report is about a pod that is not a copy of a service, Geass refuses it with `422`. Examples are a build, a database, and a schedule's job. Geass also refuses with `422` a report about a service of a project that belongs to no organization. Geass leaves a service that is already suspected as it is (`alreadySuspected: true`).

**A tool that watches system calls does not see inside a pod that runs under gVisor.** With the [sandboxed runtime](/docs/operations/sandboxed-runtime/), gVisor's own kernel handles an organization's system calls, and they never reach the node's kernel. So Falco or Tetragon on the node sees gVisor, not the miner. CPU accounting still works there. The metrics server measures a sandboxed pod like any other pod, so the CPU signal works too. On K3s v1.36.4+k3s1 with gVisor `release-20261019.0`, Geass measured a busy loop in a gVisor pod at its 100m limit. Geass suspected the busy loop 13 minutes and 42 seconds after it started. Kata Containers runs a pod in a virtual machine, and a tool on the node does not see into that machine either.

## Mining pools' ports

The [outbound limits](/docs/guides/outbound-limits/#mining) can close the ports that public mining pools listen on by default. `miningPools: Blocked` closes a curated list: TCP 3333 to 3336, 4444, 5555, 7777, 14433, 14444, and 45700. This setting stops a miner that runs with a pool's example configuration, and nothing else. It does not stop a miner that connects to port 443, which many pools offer, or through a proxy. It detects and reports nothing. Use it with the CPU signal, not instead of it.

## False positives

Some legitimate work uses all its CPU for a long time. This list gives that work, and how to keep Geass from suspecting it:

- **Batch jobs, renders, encoding, scientific computing, training a model.** Exempt the project (`exemptProjects`) or the organization (`exempt`), or raise `windowMinutes` above the length of the jobs.
- **A load test**, or a busy period of a real service. With `quietTraffic`, Geass does not count a service that receives calls through the ingress. Without `quietTraffic`, clear the service before the test.
- **A queue worker that is behind**, or a service that only other services call. Such a service receives nothing through the ingress, so `quietTraffic` does not help. Clear it, or exempt its project.
- **A service whose limit is tiny.** 90% of 100 millicores is not much. The share is of what the service is allowed, so ordinary work reaches a small limit. The [noisy-neighbour limits](/docs/guides/tenant-limits/) set the default.
- **A service that spins** on a bug (a busy loop). The loop is a problem of its own, even if it is not abuse. The suspicion tells its owner.

Abuse detection is built to accept these false negatives, rather than to be wrong more often:

- A throttled, paused, or restarting miner.
- A GPU miner.
- A miner on a service that also receives real calls.
- A miner in a build.

See [What it can see, and what it cannot](#what-it-can-see-and-what-it-cannot).

## Turn it off

If you remove `spec.tenancy.abuse`, Geass stops the sampling and removes what it wrote: the ConfigMap of runs, the annotations of suspects and reviews, and the conditions of services and projects. **A suspended service runs again.** To keep a service stopped, scale it to zero yourself, or suspend its organization, before you turn abuse detection off.

## What has been tested

The tests of the signal use a fake clock. They cover these cases:

- Below the threshold, exactly at it, exactly at the window, and one sample short.
- A quiet minute, a restart, and a gap.
- An operator that restarts, within three minutes and after ten.
- A rollout of busy copies, scale to zero, and an autoscaler that adds copies.
- Requests known, too many, and unknown.

Tests against fake clients cover these parts:

- The detector, and the conditions and events.
- The suspension: to zero and back, nothing deleted, scale to zero, and an autoscaler.
- The alert's routing.
- The API: what it says, who it refuses, clearing, releasing, Falco's own body, and a pod that is gone.

Tests against a real API server (envtest) cover the CRDs. An evaluator of the network policies, written separately from the code, tests the closed ports of `miningPools` port by port.

**On a real K3s (v1.36.4+k3s1, the metrics server K3s ships, thresholds of 50% for 10 minutes) it ran as follows.**

- Geass read the metrics server's answer (`cpu.metricsServer.available: true`).
- Geass suspected the busy loops of a service with a 100m limit 13 minutes 10 seconds, 13 minutes 54 seconds, and 13 minutes 42 seconds after they started. That time is the three minutes to settle, then 11 samples at 100% or a little over.
- Geass never counted the busy loop of an organization with `spec.abuse.exempt: true`, and never counted a quiet service.
- The alert `GeassAbuseSuspected` fired in the cluster's Prometheus, critical for a suspended service, with no `project` label.
- A service found under `action: Suspend` went to zero copies, and nothing was deleted. `release` brought it back, and Geass did not suspend it again. `clear` removed the suspicion and listed the service under `reviewed`.
- A report in falcosidekick's webhook body (the rule, the priority, the time, the namespace, and the pod) suspended a service. The made-up secret strings in its `output` and command line were found nowhere: not in the service, the events, the API, or the operator log.
- Geass resolved a pod that was already gone by its name. Geass refused the relay's pod with `422`, and a pod named in another project's namespace with `404`.
- When the setting was turned off, the suspended service ran again. Geass removed the annotations, the conditions, and the ConfigMap of runs, and the routes answered `409`.

**Not run:** a real Falco or falcosidekick, a real miner, `quietTraffic`, a per-organization `action`, `exemptProjects`, and the refusals for organization administrators, viewers, and project tokens. `quietTraffic` needs the monitoring add-on and Traefik, which that cluster did not have. The test used only the install-wide token. Without a token, and with a bad one, the answer was `401`. The parsing of the metrics server's answer was run against the real metrics server. The falcosidekick body is still Falco's documented JSON, not the real program's. You must find the thresholds that suit your cluster. Start with `action: None`.

## Related

- [Outbound limits](/docs/guides/outbound-limits/), whose `miningPools` closes pools' ports
- [Noisy-neighbour limits](/docs/guides/tenant-limits/), which give every container a CPU limit to take a share of
- [Sandboxed runtime for organizations](/docs/operations/sandboxed-runtime/) and [Registry isolation for organizations](/docs/operations/registry-isolation/), the other settings for a cluster that organizations share
- [Organizations and workspaces](/docs/guides/organizations-and-workspaces/)
- [Alerts](/docs/guides/alerts/)
- [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/) and [GeassOrganization](/docs/reference/resources/geass-organization/)
- [REST API](/docs/reference/rest-api/#abuse-detection)
- [Security](/docs/operations/security/)
