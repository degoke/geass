---
title: "Abuse detection"
description: "Look for organizations' services that may be mining or otherwise abusing a shared cluster's machines: what Geass can see, what it cannot, how a suspect is reviewed, and how to plug in Falco."
---

On a cluster that several [organizations](/docs/guides/organizations-and-workspaces/) share, the classic abuse is free compute: a stranger's service that mines a cryptocurrency on the platform's machines, or does some other work nobody pays for. **Abuse detection** looks for the services of organizations' projects that may be doing it, says so to the platform's administrators, and, if you ask it to, scales a suspect to zero until somebody has looked.

It is off until the platform sets it. A Geass whose platform sets none is unchanged: nothing is sampled, nothing is written, and no service is touched.

**What it finds is a signal, never proof.** Geass sees a container from the outside: how much CPU it uses, how many requests reach it, and what a detector you run tells it. A miner uses all the CPU it is allowed, for hours, while nobody calls it. So do a batch job, a render, the training of a model, a load test and a queue worker that is behind. That is why every suspect is reviewed by a person, why the default action changes nothing, and why there are exemptions.

## What it can see, and what it cannot

| Input | What it says | From | Firm? |
| --- | --- | --- | --- |
| **Sustained CPU** | A service whose copies use nearly all the CPU they are allowed (90% by default), without a break, for a window (30 minutes by default). | The cluster's metrics server (`metrics.k8s.io`), which K3s runs, and which a service's CPU autoscaler already needs. Nothing to install. | **A signal.** Legitimate heavy work looks the same. See [False positives](#false-positives). |
| **Few requests** (optional) | That the busy service is not being called, which a miner never is and a busy web service always is. | The requests the ingress (Traefik) passes to the service, from the cluster's Prometheus. Only while the [monitoring add-on](/docs/guides/observability/) is on. | **A signal**, and a partial one: only requests through the ingress are seen. See [Quiet services](#quiet-services). |
| **A report from a detector outside Geass** | Whatever that detector saw: a process called `xmrig`, a connection to a pool, a stratum handshake. | Falco (through falcosidekick), Tetragon, or the provider's own tooling, sent to the API. Geass installs none of them. | As good as the detector and its rule. See [Outside detectors](#outside-detectors). |
| **Mining pools' ports** | Nothing: they are closed, not watched. | The [outbound limits](/docs/guides/outbound-limits/#mining) (`miningPools: Blocked`). | **A heuristic** that a miner on port 443 walks past. It detects nothing. |

What it does **not** detect:

- **A miner that keeps below the threshold.** One that is throttled to 80% of its limit, or that runs "low and slow", is never suspected: the threshold is the line, and a careful miner stays under it.
- **A miner that pauses**, for one sample a window, or that restarts itself more often than the window. Every break starts the count again; that is what keeps restarts and rollouts from being counted, and it is also a way around it.
- **A miner that sleeps**, or that only mines at night: each run is judged on its own.
- **Mining on a GPU.** Geass sees the CPU. A service that is given a GPU and uses it all says nothing here.
- **What a program is, or where it connects.** Geass does not look inside containers. A miner that connects to a pool on port 443, or through a proxy, looks like any other client.
- **Builds.** A build is not a service. It runs the organization's Dockerfile, and a build that mines for its whole length is not looked at (builds are limited by their own [limits](/docs/guides/tenant-limits/) and [sandbox](/docs/operations/sandboxed-runtime/)).
- **Databases, buckets and the platform's own services**, and the projects of no organization. Only the services (and functions) of organizations' projects are looked at.
- **What one organization costs in total.** The [usage meter](/docs/guides/usage-and-capacity/) bills what is reserved; this looks for patterns of use.

## Before you start

- Organizations are set up, and projects are given to them (`spec.org`).
- **The metrics server answers.** K3s runs one unless it was started with `--disable=metrics-server`. Check with `kubectl top pods -A`. Without it the signal measures nothing, and the API says so (`cpu.metricsServer.available: false`).
- **Containers have a CPU limit, or at least a request.** The share is what a service uses over what it is allowed. A container with neither has no share to take, and its service is never measured. The [noisy-neighbour limits](/docs/guides/tenant-limits/) give every container of an organization a default limit; set them first.
- Only a platform administrator can turn it on, with `kubectl` (or anything that writes the resources), and only the platform's administrators see the suspects: no page of the dashboard shows them yet. The [API](#review-a-suspect) is how you read and review them.
- **What it does to running services.** With the default action, `None`, nothing: a suspect is said to be one, and runs on. With `Suspend`, a suspect is scaled to zero the moment it is suspected. Start with `None`, watch what is found for a while, and switch to `Suspend` once you trust the settings on your cluster.

## Turn it on

The settings are in the [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/), under `spec.tenancy.abuse`. Setting the section at all turns it on with its defaults; everything inside is optional:

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

Patch it in rather than applying the fragment alone:

```bash
kubectl -n geass-system patch geassplatformconfig platform --type merge -p '{"spec":{"tenancy":{"abuse":{}}}}'
```

That is the defaults: 90% for 30 minutes, action `None`. To turn it off: `kubectl -n geass-system patch geassplatformconfig platform --type merge -p '{"spec":{"tenancy":{"abuse":null}}}'`. See [Turn it off](#turn-it-off) for what that does to suspects.

| Field | Default | Meaning |
| --- | --- | --- |
| `action` | `None` | What is done to a suspect: `None` says so and changes nothing; `Suspend` also scales it to zero until a platform administrator releases or clears it. |
| `reviewExemptionDays` | 30 | 1 to 365. How long the CPU signal leaves a service alone after a platform administrator has cleared it. |
| `cpu.thresholdPercent` | 90 | 50 to 100. The share of what its copies are allowed that a service has to use in every sample. |
| `cpu.windowMinutes` | 30 | 10 to 1440. How long it has to last without a break. |
| `cpu.quietTraffic.maxRequestsPerMinute` | none (requests not considered) | 0 or more. When set, a sample counts only while the ingress passes the service at most this many requests a minute, averaged over five minutes. |
| `cpu.quietTraffic.withoutTrafficData.thresholdPercent` | the larger of 95 and `thresholdPercent` | 50 to 100. Replaces the threshold while the requests cannot be read. |
| `cpu.quietTraffic.withoutTrafficData.windowMinutes` | twice `windowMinutes`, at most 1440 | 10 to 1440. Replaces the window while the requests cannot be read. |

An organization is treated by the platform's settings, with what its own `spec.abuse` on the [GeassOrganization](/docs/reference/resources/geass-organization/) says in their place:

| Field | Meaning |
| --- | --- |
| `spec.abuse.exempt` | `true` leaves every service of the organization out of the CPU signal. A report from an outside detector still applies. |
| `spec.abuse.exemptProjects` | The organization's projects whose services are left out of the CPU signal. A report still applies. |
| `spec.abuse.action` | `None` or `Suspend`, in place of the platform's action, for the organization's suspects. |

```bash
# An organization that renders video all night: its render project is left out.
kubectl -n geass-system patch geassorganization org-1a2b3c4d --type merge -p '{"spec":{"abuse":{"exemptProjects":["proj-render"]}}}'
```

Like the rest of an organization's spec, `spec.abuse` is the platform's to write. No route of the dashboard changes it, and an organization's people cannot exempt themselves.

## How the CPU signal works

Once a minute the operator that holds the leader lease looks at every deployed service of every organization's project, less those that are exempt, already suspected or cleared, and takes a **sample**: what all the service's copies use, from the metrics server (each copy's use is the metrics server's average over its own short window, 15 seconds on K3s), over what they are allowed together (each container's CPU limit, or its request when it has none). A container that runs beside the service for the pod's life, such as a sidecar, counts in both.

A service is only measured when it is **settled**: every copy its Deployment asks for is there, up to date and ready, and each has been running, with each of its containers since it last started, for **three minutes**. A sample of a service that is not settled is not taken, and ends its count. That is what keeps these from ever adding up to a suspicion:

- **A rollout.** New copies start busy (a runtime warming up, a migration, a cache filling), next to the old ones. The count starts again three minutes after the rollout ends, from zero.
- **A restart.** A copy whose container restarted is a new count, even when the restart is quick.
- **A change in the number of copies**, by a person, by the CPU autoscaler, or by scale to zero waking the service. An autoscaled service is suspected only once it has stayed at one number of copies for the whole window.
- **No copies at all**, because the service is stopped, asleep under [scale to zero](/docs/guides/scale-to-zero/), or suspended.

A **run** is an unbroken string of samples at or above the threshold. A sample below it, one that was not taken, one of other copies, and a gap of more than three minutes (the operator restarting for long, the metrics server not answering) end the run. A run that has lasted the window (its first and its latest sample are the window apart: with the defaults, the 31st sample) makes the service a **suspect**, with its evidence: the run's average and least share, the threshold and window, and how many samples it had.

The runs are kept in the ConfigMap `geass-abuse-signal` in `geass-system`, written once a minute. An operator that restarts, or a new leader, reads it and carries on where the last sample stopped, as long as that was no more than three minutes before. A service at rest has no run, so the ConfigMap holds only the services that are busy right now, and never more than 2,000 of them (those that have been busy longest). It was kept out of the services' own status on purpose: a write a minute to every busy service would wake their controller every minute.

## Quiet services

A busy service that is being called is doing its job. With `quietTraffic`, a sample counts only while the ingress passes the service at most `maxRequestsPerMinute` requests a minute (averaged over the last five minutes), so a web service at its limit under load is never suspected, and a miner, which nobody calls, is.

The requests are what Traefik counts for the service (`traefik_service_requests_total`), which only the cluster's Prometheus has, and only while the [monitoring add-on](/docs/guides/observability/) is on and scrapes the ingress. **When they cannot be read** (the add-on is off, Prometheus does not answer, or it has no request metrics at all, which would otherwise make every service look quiet), a busy service cannot be told from a quiet one, so the stricter `withoutTrafficData` applies in its place: by default 95% for twice the window. A run with one sample taken without the requests is held to the longer window as a whole. The API says whether Prometheus answered at the last sample.

What it does not see: requests that reach the service from inside the cluster, not through the ingress. A worker fed by a queue, or a service that only other services call, always looks quiet, and is judged on its CPU alone. A store of metrics outside the cluster (`spec.observability.external`) is not read.

## What a suspect gets

| Where | What |
| --- | --- |
| **The service**: condition `AbuseSuspected` | `True`, reason `SustainedCPU` (the CPU signal), `Reported` (an outside detector) or `Suspended` (either, and scaled to zero). The message gives the evidence and says it is a signal. `False`, reason `Reviewed`, while a cleared service is left alone. A service that has never been suspected has no such condition. |
| **The project**: condition `AbuseSuspected` | `True`, reason `ServicesSuspected`, naming the suspected services (and which are suspended), while any is. None otherwise. |
| **An event on the service** | `Warning` `AbuseSuspected` when it becomes a suspect, once; `Normal` `AbuseReleased` when a suspension is released, and `AbuseReviewed` when it is cleared. |
| **An alert** | **Abuse suspected** (`abuse.suspected`), to the platform's [alert channels](/docs/guides/alerts/): warning, or critical for a suspended service. It carries no project, so no organization's channel hears it, and a channel of the platform's that is limited to some projects does not either. |
| **The API** | [`GET /api/v1/abuse/suspects`](#review-a-suspect), with the evidence, for the platform's administrators alone. |

A suspicion stays until a platform administrator clears it. A miner that stops once it is suspected, or is scaled to zero, is not let off by that. What was decided is kept on the service, in the annotations `geass.dev/abuse-suspect` (the suspicion, its evidence and what was done) and `geass.dev/abuse-review` (a clearing, until when and by whom), which Geass and the platform's administrators write. The dashboard gives an organization's people no way to set a service's annotations.

**The organization sees the conditions and the event on its own service**, which is deliberate: the owner of a legitimate heavy job learns why it was stopped, and asks for an exemption. It also means a miner can learn it was seen. It never sees another organization's suspects, the services that are being counted, how close its own are to the threshold, or the alert.

## Suspend

With `action: Suspend` (or an organization's own), a suspect is scaled to **zero copies** the moment it is suspected, by a report as well as by the CPU signal, and stays there until a platform administrator releases or clears it:

- Its Deployment is kept at zero copies. Saving or deploying the service changes its pods, not that.
- [Scale to zero](/docs/guides/scale-to-zero/) is set aside, so that a request cannot wake it: its condition `ScaleToZero` says `Suspended`. It is put back on release.
- A CPU autoscaler is left in place: Kubernetes does not scale a Deployment that is at zero.
- Its `Ready` condition is `False`, reason `Suspended`, with the reason, and that is not counted as a failed deploy: no **Deploy failed** or **Service down** alert follows, and no success is reported to GitHub or to webhooks.
- **Nothing is deleted**: the service, its settings, its Service and ingress, its volumes and its databases stay as they were. Its copies come back on release, as many as it asks for.

An organization can still delete a suspended service and make it again under the same name. The new one starts without a suspicion, and the CPU signal finds it again after a window. An organization that does that is for you to deal with: [suspend the organization](/docs/guides/organizations-and-workspaces/) (`spec.suspended`), lower its [limits](/docs/guides/tenant-limits/), or remove it.

## Review a suspect

The API is the platform's administrators' alone, signed in or with an install-wide admin token. An organization's administrator, a viewer of the platform, an observer and a project token are refused with `403`, and are told nothing.

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

`?org=` keeps one organization's. `counting` is the services the CPU signal is counting now, which are not suspects yet. A report has `reporter`, `detail`, `pod` and `priority` in place of the CPU evidence; `traffic` says `quiet` (few requests in every sample) or `unknown` (counted without them, at the stricter threshold and window).

Then, for each suspect, look at what it runs (its image, its logs, its metrics in the dashboard), and decide:

| Request | What it does |
| --- | --- |
| `POST /api/v1/abuse/suspects/{service}/clear` | It was legitimate. The suspicion goes (a suspended service runs again), and the CPU signal leaves it alone for `days` (1 to 365; default `reviewExemptionDays`), with your `note`. A report from an outside detector still applies. A service that is not suspected can be cleared too, ahead of a heavy job you know of. |
| `POST /api/v1/abuse/suspects/{service}/release` | Let a suspended service run again while you look. It stays suspected, and is not suspended again. |
| Nothing | Leave it suspected (and suspended, if it was). |
| Deal with the organization | Suspend it, remove the service yourself, or remove the organization. Geass never deletes anything for a suspicion. |

```bash
curl -X POST -H "Authorization: Bearer $GEASS_TOKEN" -H "Content-Type: application/json" \
  https://geass.example.com/api/v1/abuse/suspects/worker/clear -d '{"days": 14, "note": "nightly batch, asked the customer"}'
# {"service":"worker","wasSuspected":true,"reviewedUntil":"2026-11-10T12:00:00Z","message":"Cleared. ..."}
```

All of them answer `409` while abuse detection is off.

## Outside detectors

A tool that watches what programs do on the nodes sees what Geass cannot: the name and arguments of a process, the connections it opens, a stratum handshake. Geass does not bundle or install one. If you run one, it can report a workload to `POST /api/v1/abuse/reports` with a platform administrator's token, and the workload gets the same suspicion, condition, event and action as from the CPU signal (exemptions and reviews are for the CPU signal, and do not hold a report back; a report replaces a review the service had).

The body names the service, or a pod that is one of its copies:

| Field | Meaning |
| --- | --- |
| `service` | The service, by name. Or: |
| `namespace`, `pod` | The pod and its namespace. A pod that is already gone is found by its name (`<service>-<hash>-<suffix>`). |
| `reporter`, `reason`, `priority`, `time` | Who reports it, what was seen (a rule's name: it is shown to the organization on its service), how urgent, and when (RFC 3339). |

**Falco**, through [falcosidekick](https://github.com/falcosecurity/falcosidekick)'s webhook output, can send its events as they are: Geass reads the rule, the priority, the time, and the namespace and pod from `output_fields` (`k8s.ns.name`, `k8s.pod.name`), and keeps nothing else (Falco's `output` and the command line may hold secrets, and the organization reads the detail). Send only the rules you mean as reports, such as Falco's mining rules:

```yaml title="falcosidekick values (Helm)"
config:
  webhook:
    address: https://geass.example.com/api/v1/abuse/reports
    customHeaders: "Authorization: Bearer <a platform administrator's token>"
    minimumpriority: critical
```

A report about a pod that is not a copy of a service (a build, a database, a schedule's job) is refused with `422`, as is one about a service of a project of no organization. A service that is suspected already is left as it is (`alreadySuspected: true`).

**A tool that watches system calls does not see inside a pod that runs under gVisor.** With the [sandboxed runtime](/docs/operations/sandboxed-runtime/), an organization's system calls are handled by gVisor's own kernel and never reach the node's, so Falco or Tetragon on the node sees gVisor, not the miner. CPU accounting still works there: the metrics server measures a sandboxed pod like any other, so the CPU signal does. Kata Containers runs a pod in a virtual machine, which a tool on the node does not see into either.

## Mining pools' ports

The [outbound limits](/docs/guides/outbound-limits/#mining) can close the ports public mining pools listen on by default: `miningPools: Blocked` closes a curated list (TCP 3333 to 3336, 4444, 5555, 7777, 14433, 14444 and 45700). That stops a miner run with a pool's example configuration, and nothing else: a miner that connects to port 443, which many pools offer, or through a proxy, is not stopped, and nothing is detected or reported. Use it beside the CPU signal, not instead of it.

## False positives

Legitimate work that uses all its CPU for a long time, and how to keep it from being suspected:

- **Batch jobs, renders, encoding, scientific computing, training a model.** Exempt the project (`exemptProjects`) or the organization (`exempt`), or raise `windowMinutes` above the length of the jobs.
- **A load test**, or a busy period of a real service. With `quietTraffic`, a service that is being called through the ingress is not counted. Without it, clear it ahead of the test.
- **A queue worker that is behind**, or a service that only other services call: it is sent nothing through the ingress, so `quietTraffic` does not help. Clear it, or exempt its project.
- **A service whose limit is tiny.** 90% of 100 millicores is not much. The share is of what the service is allowed, so a small limit is reached by ordinary work; the [noisy-neighbour limits](/docs/guides/tenant-limits/) set the default.
- **A service that spins** on a bug (a busy loop). It is a problem of its own, if not abuse; the suspicion tells its owner.

And false negatives, which it is built to accept rather than to be wrong more often: a throttled, paused or restarting miner, a GPU miner, a miner on a service that is also really called, and a miner in a build. See [What it can see, and what it cannot](#what-it-can-see-and-what-it-cannot).

## Turn it off

Removing `spec.tenancy.abuse` stops the sampling and removes what it wrote: the ConfigMap of runs, the annotations of suspects and reviews, and the conditions of services and projects. **A suspended service runs again.** If you mean to keep one stopped, scale it to zero yourself, or suspend its organization, before you turn it off.

## What has been tested

The signal is tested with a fake clock: below the threshold, exactly at it, exactly at the window and one sample short, a quiet minute, a restart, a gap and an operator that restarts (within three minutes and after ten), a rollout of busy copies, scale to zero, an autoscaler adding copies, requests known, too many, and unknown. The detector, the conditions and events, the suspension (to zero and back, nothing deleted, scale to zero and an autoscaler), the alert's routing and the API (what it says, who is refused, clearing, releasing, Falco's own body, a pod that is gone) are tested against fake clients; the CRDs against a real API server (envtest). The closed ports of `miningPools` are tested port by port with an evaluator of the network policies written apart from the code.

**It has not been run on a real cluster, or against a real miner.** The parsing of the metrics server's answer is tested on its documented shape, and of falcosidekick's on Falco's documented output; neither was run against the real program. Thresholds that suit your cluster are for you to find: start with `action: None`.

## Related

- [Outbound limits](/docs/guides/outbound-limits/), whose `miningPools` closes pools' ports
- [Noisy-neighbour limits](/docs/guides/tenant-limits/), which give every container a CPU limit to take a share of
- [Sandboxed runtime for organizations](/docs/operations/sandboxed-runtime/) and [Registry isolation for organizations](/docs/operations/registry-isolation/), the other settings for a cluster that organizations share
- [Organizations and workspaces](/docs/guides/organizations-and-workspaces/)
- [Alerts](/docs/guides/alerts/)
- [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/) and [GeassOrganization](/docs/reference/resources/geass-organization/)
- [REST API](/docs/reference/rest-api/#abuse-detection)
- [Security](/docs/operations/security/)
