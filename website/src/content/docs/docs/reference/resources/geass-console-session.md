---
title: "GeassConsoleSession"
description: "Reference for the GeassConsoleSession resource, the audit record and time limit of a terminal opened in a service."
sidebar:
  order: 14
---

A `GeassConsoleSession` is the record of one terminal. When an admin opens the **Console** tab of a service and chooses **Connect**, the dashboard first creates a `GeassConsoleSession` that says who is opening a shell, in which pod and container, with which command, and for how long. The terminal itself only works while that session is active. When the time runs out or the terminal closes, the session is closed.

You do not create these objects yourself. They are a short-lived audit trail: they record that a shell was opened, not what was typed. Every session lives in `geass-system`.

## Summary

| Property | Value |
| --- | --- |
| API version | `geass.geass.dev/v1alpha1` |
| Kind | `GeassConsoleSession` |
| Plural | `geassconsolesessions` |
| Scope | Namespaced |
| Namespace | `geass-system` |
| Subresources | `status` |
| Printer columns | None |

## Example

This is a session the dashboard created for a service, after it ended.

```yaml title="storefront-console-1760000000000000000.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassConsoleSession
metadata:
  name: storefront-console-1760000000000000000
  namespace: geass-system
  labels:
    geass.dev/app: storefront
    geass.dev/project: proj-1a2b3c4d
    geass.dev/environment: production
spec:
  app: storefront
  project: proj-1a2b3c4d
  environment: production
  pod: storefront-6d9c7b8f5d-x2k4m
  container: app
  actor: admin
  command:
    - sh
  timeoutSeconds: 300
status:
  phase: Closed
  startedAt: "2026-10-12T09:00:00Z"
  endedAt: "2026-10-12T09:03:41Z"
  reason: Terminal closed
```

## Spec fields

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.app` | string | none | Required | The `GeassApp` the shell belongs to, or the cluster MinIO server's name for a MinIO terminal. |
| `spec.project` | string | none | Required | The project ID. Empty for a MinIO terminal. |
| `spec.environment` | string | none | Required | The environment of the service. Empty for a MinIO terminal. |
| `spec.pod` | string | none | Required | The pod the shell runs in. |
| `spec.container` | string | none | Required | The container of that pod. |
| `spec.actor` | string | none | none | Who opened it: the dashboard user's name, `token:<name>` for an API token, or `dashboard`. |
| `spec.command` | list of strings | none | none | The command to run. The dashboard sends `sh`, which Geass runs as a login `bash` when the image has it and as `sh` otherwise. At most eight words are accepted. |
| `spec.timeoutSeconds` | integer | `300` | none | The time limit. The operator sets it to 300 when it is empty, zero, negative or above 900. |

The dashboard checks, before it creates the session, that the pod is running, that it belongs to the service (its label `app.kubernetes.io/name` equals the service name) and that the container exists in it.

## Status

| Field | Type | Meaning |
| --- | --- | --- |
| `status.phase` | string | `Pending`, `Active`, `Expired` or `Closed`. |
| `status.startedAt` | time | When the operator activated the session. The time limit counts from here, not from when the terminal connects. |
| `status.endedAt` | time | When the session expired or closed. |
| `status.reason` | string | `Session timeout` when it expired, `Terminal closed` when the terminal ended, `Stream ended` when the older streaming endpoint ended. |

Nothing writes conditions for this kind, and it has no printer columns.

| Phase | Meaning |
| --- | --- |
| `Pending` | Created, not yet activated. |
| `Active` | A terminal may attach, until `startedAt` plus `timeoutSeconds`. |
| `Expired` | The time limit passed. |
| `Closed` | The terminal ended. |

A session serves one terminal at a time. Opening a second one on the same session fails with `this console session already has an open terminal`. Once the session is closed or expired, a new attach fails with `console session has ended`, so each **Connect** creates a new session.

## How the dashboard uses it

| Dashboard | Request | Effect |
| --- | --- | --- |
| **Console** tab of a service, **Connect** | `POST /api/apps/<app>/console/create`, then a WebSocket to `/api/apps/<app>/console/ws?session=<name>` | Creates the session, then attaches a terminal to the pod. |
| None. The dashboard has no screen for it | `POST /api/object-stores/cluster-minio/console/create`, then a stream on `/api/object-stores/cluster-minio/console/stream?session=<name>` | The same, for the MinIO pod in `geass-system`. Only the REST API reaches it. Its sessions carry no `geass.dev/app` label, so the `kubectl delete -l` command under [Troubleshooting](#troubleshooting) does not match them. |

The tab shows **Connected** with a countdown of the time left. A viewer sees "Your viewer role cannot open consoles." API tokens that are limited to a project cannot open terminals. A workspace token with the `admin` role can: its sessions are recorded with the actor `token:<name>`. A token with the `observer` or `viewer` role cannot, because it cannot create the session. See [Users and API tokens](/docs/guides/users-and-tokens/).

**Copy kubectl command** gives you the `kubectl exec` command for the same pod and container. Running it yourself leaves no `GeassConsoleSession`.

## Editing by hand

Do not create or edit sessions. Creating one by hand does not open a terminal, and the dashboard's own checks on the pod and command do not run for it.

Nothing deletes old sessions. They stay in `geass-system` until you delete them, which is safe once they are `Closed` or `Expired`.

## What happens under the hood

The operator's session controller activates a new session (sets `startedAt` and the phase `Active`, and fixes an out-of-range timeout), and when `startedAt` plus the timeout has passed it sets the phase `Expired`, `endedAt` and the reason `Session timeout`. The terminal itself is a Kubernetes exec call made by the dashboard, over a WebSocket, with the time left in the session as its limit. When the terminal ends the dashboard marks the session `Closed`. The operator log notes the start and the end (`Console terminal opened` and `Console terminal closed`, with the app, pod and actor).

Geass does not store terminal input or output. The audit trail is the session object and the operator log.

```bash title="Audit terminals"
kubectl -n geass-system get geassconsolesessions \
  -o custom-columns='NAME:.metadata.name,APP:.spec.app,ACTOR:.spec.actor,POD:.spec.pod,PHASE:.status.phase,STARTED:.status.startedAt,REASON:.status.reason'
kubectl -n geass-system logs deployment/geass-controller-manager -c manager | grep "Console terminal"
```

## Troubleshooting

**`a running pod and container are required`.** The tab sent no target. The service has no running pod: deploy it or wait for it to start. A service that [sleeps when idle](/docs/guides/scale-to-zero/) has no pod while it sleeps, and its **Pod** menu says "No running pods". A request to its public host wakes it.

**`pod is not running` or `pod is not part of the app`.** The pod changed between loading the tab and clicking **Connect**. Reload the tab and choose a pod again.

**`command is required`.** The command was empty or longer than eight words.

**`Could not open the terminal. The session may have ended; connect again.`** The session expired before the WebSocket attached (it counts down from creation), or the browser could not reach `/api/apps/<app>/console/ws`. Connect again. If it keeps failing, check that your proxy passes WebSocket upgrades.

**`request origin could not be verified`.** The WebSocket's `Origin` does not match the dashboard's host. This happens when a proxy rewrites the `Host` header.

**The terminal ends after five minutes.** That is the time limit, `Session time limit reached`. The limit counts from when the session was created and cannot be extended: connect again.

**There are hundreds of old sessions.** Nothing prunes them. Delete the closed ones with `kubectl -n geass-system delete geassconsolesessions -l geass.dev/app=<app>`.

## Related

- [GeassApp](/docs/reference/resources/geass-app/)
- [Security](/docs/operations/security/#auditing)
- [Users and API tokens](/docs/guides/users-and-tokens/)
- [Resources overview](/docs/reference/resources/overview/)
