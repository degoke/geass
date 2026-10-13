---
title: "GeassNetworkLog"
description: "Reference for the GeassNetworkLog resource, a schema for one HTTP request record that nothing in Geass writes today; the operator only deletes old ones."
sidebar:
  order: 16
---

A `GeassNetworkLog` is a schema for one HTTP access-log record: the project, environment and service, the host, method and path of a request, the status code and the latency. It holds request metadata only, never request or response bodies.

:::caution
Nothing in Geass creates these objects, and nothing reads them. The operator has a controller that deletes records older than seven days, and the platform configuration has a `traefikAccessLogs` block, but no code turns Traefik's access log into `GeassNetworkLog` records, and the dashboard has no screen for them. The kind is a stub for a feature that is not built. For HTTP request data today, use the request metrics on a service's **Metrics** tab (see [Observability](/docs/guides/observability/)).
:::

## Summary

| Property | Value |
| --- | --- |
| API version | `geass.geass.dev/v1alpha1` |
| Kind | `GeassNetworkLog` |
| Plural | `geassnetworklogs` |
| Scope | Namespaced |
| Namespace | `geass-system`, by convention |
| Subresources | None. The kind has no `status`. |
| Printer columns | None |

## Example

This is what a record looks like if something writes one.

```yaml title="storefront-request.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassNetworkLog
metadata:
  name: storefront-1760000000-a1b2c3
  namespace: geass-system
spec:
  project: proj-1a2b3c4d
  environment: production
  app: storefront
  timestamp: "2026-10-12T09:00:00Z"
  host: shop.example.com
  method: GET
  path: /checkout
  status: 200
  latencyMillis: 42
  upstreamOutcome: ok
```

## Spec fields

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.project` | string | none | Required | The project ID. |
| `spec.environment` | string | none | Required | The environment. |
| `spec.app` | string | none | Required | The service that answered. |
| `spec.timestamp` | time | none | Required | When the request happened. The retention check uses it. |
| `spec.host` | string | none | none | The `Host` of the request. |
| `spec.method` | string | none | none | The HTTP method. |
| `spec.path` | string | none | none | The request path. |
| `spec.status` | integer | none | 32-bit integer | The response status code. |
| `spec.latencyMillis` | integer | none | 64-bit integer | How long the request took, in milliseconds. |
| `spec.upstreamOutcome` | string | none | none | A free-form note on how the upstream answered. |

## What happens under the hood

The operator runs a controller for the kind. For each record it compares `spec.timestamp` with the retention, 7 days by default, and deletes the record when it is older. If it is not old enough, it looks at the record again after half the retention (3.5 days), so a record can outlive its retention by up to that long. A record with an empty `timestamp` is never deleted. The operator's role can create, delete, get, list and watch the kind, and it cannot update it.

The retention is a field of the controller, and the operator does not set it from any flag, environment variable or resource, so 7 days is what you get.

The `traefikAccessLogs` block of the [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/) (`enabled`, `format`, `networkLogEndpoint`) is in the schema and nothing reads it.

```bash title="Look for records"
kubectl -n geass-system get geassnetworklogs
```

On a cluster installed from the current code this lists nothing.

## Editing by hand

You can create and delete records, but there is nothing to gain: they do not appear anywhere, and the operator removes them after seven days. Do not store personal data in them.

## Troubleshooting

**The list is empty.** That is expected. Nothing writes these records.

**I set `traefikAccessLogs.enabled` on the platform configuration and nothing happens.** The field is not used. See the caution at the top of this page.

## Related

- [Observability](/docs/guides/observability/)
- [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/)
- [Resources overview](/docs/reference/resources/overview/)
