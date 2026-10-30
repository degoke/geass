---
title: "GeassNetworkLog"
description: "Reference for the GeassNetworkLog resource, a schema for one HTTP request record. Nothing in Geass writes it. The operator only deletes old ones."
sidebar:
  order: 16
---

A `GeassNetworkLog` is a schema for one HTTP access-log record. A record holds these values:

- The project, the environment, and the service.
- The host, the method, and the path of a request.
- The status code and the latency.

A record holds request metadata only, never request or response bodies.

:::caution
For HTTP request data today, use the request metrics on the **Metrics** tab of a service. See
[Observability](/docs/guides/observability/). Nothing in Geass creates `GeassNetworkLog` objects, and nothing reads
them:

- The operator has a controller that deletes records older than seven days.
- The platform configuration has a `traefikAccessLogs` block.
- But no code turns the Traefik access log into `GeassNetworkLog` records.
- The dashboard has no screen for the records.

The kind is a stub for a feature that is not built.
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

This example shows a record, if something writes one.

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

The operator runs a controller for the kind. The controller does these steps for each record:

1. It compares `spec.timestamp` with the retention, which is 7 days by default.
2. If the record is older than the retention, the controller deletes the record.
3. If the record is not old enough, the controller looks at the record again after half the retention (3.5 days).

So a record can stay up to 3.5 days longer than its retention. The controller never deletes a record with an empty
`timestamp`. The role of the operator can create, delete, get, list, and watch the kind. The role cannot update the
kind.

The retention is a field of the controller. The operator does not set the retention from a flag, an environment
variable, or a resource. So the retention is always 7 days.

The `traefikAccessLogs` block of the [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/)
(`enabled`, `format`, `networkLogEndpoint`) is in the schema, but nothing reads it.

```bash title="Look for records"
kubectl -n geass-system get geassnetworklogs
```

On a cluster that you installed from the current code, this command lists nothing.

## Editing by hand

You can create and delete records, but you get no benefit. The records do not appear anywhere, and the operator
removes them after seven days. Do not store personal data in the records.

## Troubleshooting

**The list is empty.** This result is expected. Nothing writes these records.

**I set `traefikAccessLogs.enabled` on the platform configuration and nothing happens.** Geass does not use this field. See the caution at the top of this page.

## Related

- [Observability](/docs/guides/observability/)
- [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/)
- [Resources overview](/docs/reference/resources/overview/)
