---
title: "GeassConsoleSession"
description: "Reference for GeassConsoleSession, the audit record and time limit of a terminal that an admin opens in a service."
sidebar:
  order: 14
---

A `GeassConsoleSession` is the record of one terminal. An admin opens the **Console** tab of a service and clicks **Connect**. The dashboard then creates a `GeassConsoleSession` before the terminal opens. The session records these values:

- Who opens the shell.
- The pod and the container.
- The command.
- The time limit.

The terminal works only while that session is active. When the time limit ends or the terminal closes, the session closes.

You do not create these objects yourself. They are a short-lived audit trail. They record that a user opened a shell, but they do not record what the user typed. Every session is in `geass-system`.

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

This example is a session that the dashboard created for a service, after the session ended.

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
| `spec.app` | string | none | Required | The `GeassApp` that the shell belongs to. For an object storage terminal, the name of the cluster object storage server. |
| `spec.project` | string | none | Required. The value can be empty. | The project ID. Empty for an object storage terminal. |
| `spec.environment` | string | none | Required. The value can be empty. | The environment of the service. Empty for an object storage terminal. |
| `spec.pod` | string | none | Required | The pod that the shell runs in. |
| `spec.container` | string | none | Required | The container of that pod. |
| `spec.actor` | string | none | none | The user who opened the shell: the name of the dashboard user, `token:<name>` for an API token, or `dashboard`. |
| `spec.command` | list of strings | none | none | The command to run. The dashboard sends `sh`. Geass runs it as a login `bash` when the image has `bash`, and as `sh` when the image does not. Geass accepts a maximum of eight words. |
| `spec.timeoutSeconds` | integer | `300` | none | The time limit. The operator sets it to 300 when it is empty, zero, negative, or more than 900. |

Before the dashboard creates the session, it does these checks:

- The pod is running.
- The pod belongs to the service. The label `app.kubernetes.io/name` of the pod is equal to the service name.
- The container exists in the pod.

## Status

| Field | Type | Meaning |
| --- | --- | --- |
| `status.phase` | string | `Pending`, `Active`, `Expired`, or `Closed`. |
| `status.startedAt` | time | The time when the operator activated the session. The time limit counts from this time, not from when the terminal connects. |
| `status.endedAt` | time | The time when the session expired or closed. |
| `status.reason` | string | `Session timeout` when the session reached its time limit. The operator and the dashboard both write this reason, with the phase `Expired`. `Terminal closed` when the terminal ended for another reason. `Session ended` when the sign-in that opened the terminal ended. `Stream ended` when the older streaming endpoint ended before the time limit. |

Nothing writes conditions for this kind, and this kind has no printer columns.

| Phase | Meaning |
| --- | --- |
| `Pending` | Created, not activated yet. |
| `Active` | A terminal can attach, until `startedAt` plus `timeoutSeconds`. |
| `Expired` | The time limit passed. |
| `Closed` | The terminal ended. |

A session serves one terminal at a time. If you open a second terminal on the same session, the attach fails with `this console session already has an open terminal`. After the session is closed or expired, a new attach fails with `console session has ended`. Thus each **Connect** creates a new session.

## How the dashboard uses it

| Dashboard | Request | Effect |
| --- | --- | --- |
| **Console** tab of a service, **Connect** | `POST /api/apps/<app>/console/create`, then a WebSocket to `/api/apps/<app>/console/ws?session=<name>` | Creates the session, then attaches a terminal to the pod. |
| None. The dashboard has no screen for it | `POST /api/object-stores/<server name>/console/create`, then a stream on `/api/object-stores/<server name>/console/stream?session=<name>` | The same, for the pod of the object storage server in `geass-system`. `<server name>` is the resource name of the server, `cluster-s3` by default. Only the REST API reaches it. Its sessions have no `geass.dev/app` label. Thus the `kubectl delete -l` command in [Troubleshooting](#troubleshooting) does not match them. |

The tab shows **Connected** with a countdown of the time that is left. A viewer sees "Your viewer role cannot open consoles." These rules apply to API tokens:

- An API token that is limited to a project cannot open terminals.
- A workspace token with the `admin` role can open terminals. Geass records its sessions with the actor `token:<name>`.
- A token with the `observer` or `viewer` role cannot open terminals, because it cannot create the session.

See [Users and API tokens](/docs/guides/users-and-tokens/).

**Copy kubectl command** gives you the `kubectl exec` command for the same pod and container. If you run that command yourself, Geass does not create a `GeassConsoleSession`.

## Editing by hand

Do not create or edit sessions. A session that you create by hand does not open a terminal. Also, the dashboard checks on the pod and the command do not run for that session.

Nothing deletes old sessions. They stay in `geass-system` until you delete them. It is safe to delete a session after it is `Closed` or `Expired`.

## What happens under the hood

The session controller of the operator activates a new session. It sets `startedAt` and the phase `Active`, and it fixes a timeout that is out of range. When `startedAt` plus the timeout has passed, the controller sets these values:

- The phase `Expired`.
- `endedAt`.
- The reason `Session timeout`.

The terminal itself is a Kubernetes exec call that the dashboard makes over a WebSocket. The limit of that call is the time that is left in the session. When the terminal ends, the dashboard marks the session `Closed`. The operator log records the start and the end with the app, the pod, and the actor. The log messages are `Console terminal opened` and `Console terminal closed`.

Geass does not keep terminal input or output. The audit trail is the session object and the operator log.

```bash title="Audit terminals"
kubectl -n geass-system get geassconsolesessions \
  -o custom-columns='NAME:.metadata.name,APP:.spec.app,ACTOR:.spec.actor,POD:.spec.pod,PHASE:.status.phase,STARTED:.status.startedAt,REASON:.status.reason'
kubectl -n geass-system logs deployment/geass-controller-manager -c manager | grep "Console terminal"
```

## Troubleshooting

**`a running pod and container are required`.** The tab sent no target. The service has no running pod. Deploy the service, or wait for it to start. A service that [sleeps when idle](/docs/guides/scale-to-zero/) has no pod while it sleeps, and its **Pod** menu shows "No running pods". A request to its public host wakes the service.

**`pod is not running` or `pod is not part of the app`.** The pod changed after the tab loaded and before you clicked **Connect**. Reload the tab and select a pod again.

**`command is required`.** The command was empty or longer than eight words.

**`Could not open the terminal. The session may have ended; connect again.`** One of these problems occurred:

- The session expired before the WebSocket attached. The session counts down from `status.startedAt`, when the operator activated it.
- The browser could not reach `/api/apps/<app>/console/ws`.

Connect again. If the error continues, make sure that your proxy passes WebSocket upgrades.

**`request origin could not be verified`.** The `Origin` of the WebSocket does not match the host of the dashboard. This error occurs when a proxy changes the `Host` header.

**The terminal ends after five minutes.** Five minutes is the time limit. The terminal shows `Session time limit reached`. The `status.phase` of the session is `Expired`, and its `status.reason` is `Session timeout`. The limit counts from `status.startedAt`, when the operator activated the session, and you cannot extend it. Connect again.

**There are hundreds of old sessions.** Nothing removes them. To delete the sessions of a service, run `kubectl -n geass-system delete geassconsolesessions -l geass.dev/app=<app>`. This command deletes every session of the service, also a session that is `Pending` or `Active`. Run it when nobody has a terminal open on the service.

## Related

- [GeassApp](/docs/reference/resources/geass-app/)
- [Security](/docs/operations/security/#auditing)
- [Users and API tokens](/docs/guides/users-and-tokens/)
- [Resources overview](/docs/reference/resources/overview/)
