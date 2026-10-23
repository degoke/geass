---
title: "GeassDeployment"
description: "Reference for the GeassDeployment resource, the record of what a service ran and deployed, written by the dashboard and the operator."
sidebar:
  order: 12
---

A `GeassDeployment` is a record of one deploy of a [GeassApp](/docs/reference/resources/geass-app/): which image it ran, how many replicas, who asked for it and where it came from. The dashboard writes one every time you deploy. The operator also keeps one current record per service, named `<app>-active`.

You do not create or edit these objects. They feed the **Deployments** tab of a service, the deploy logs and the rollback action. Nothing starts or changes a workload because a `GeassDeployment` exists. The workload comes from the `GeassApp`. Every record lives in `geass-system`.

## Summary

| Property | Value |
| --- | --- |
| API version | `geass.geass.dev/v1alpha1` |
| Kind | `GeassDeployment` |
| Plural | `geassdeployments` |
| Scope | Namespaced |
| Namespace | `geass-system` |
| Subresources | `status` |
| Printer columns | None |

## Example

This is the record the dashboard writes when you deploy an image service. The `geass.dev/app` label is how the dashboard finds the records of a service.

```yaml title="storefront-web-9nr6p.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassDeployment
metadata:
  name: storefront-web-9nr6p
  namespace: geass-system
  labels:
    geass.dev/app: storefront-web
    geass.dev/managed-by: geass
    geass.dev/project: storefront
    geass.dev/environment: production
spec:
  app: storefront-web
  project: storefront
  environment: production
  image: ghcr.io/acme/storefront:1.4.2
  replicas: 2
  changeTitle: Deploy ghcr.io/acme/storefront:1.4.2
  source: Dashboard
  actor: Dashboard
```

## Who writes records

| Writer | Name | When |
| --- | --- | --- |
| The dashboard | `<app>-` plus five random characters | Each time you deploy a service with **Deploy changes** or **Redeploy**, or with the REST API. Also after a rollback and after `POST /api/apps/<name>/networking/delete`, which clears the domain. |
| The operator | `<app>-active` | On each reconcile of a deployed service, once its Deployment exists. It is updated in place, and it has an owner reference to the `GeassApp`. |

The dashboard keeps the newest 12 records of each service and deletes the older ones each time it adds one. It finds them with the label `geass.dev/app`. The `<app>-active` record is not counted and is never pruned. When you delete the `GeassApp`, the operator deletes the dashboard's records, and Kubernetes garbage collection removes `<app>-active`.

## Spec fields

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.app` | string | none | Required | The `GeassApp` name. |
| `spec.project` | string | none | Required, at least 1 character | The project ID. |
| `spec.environment` | string | none | Required. Pattern `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`, 63 characters at most | The environment of the service. |
| `spec.image` | string | none | Required | The image of the deploy. The dashboard writes the image reference, the image digest of the build for a Git service once there is one (before that `<repository>@<branch>`), and `function:<runtime>` for a function. The operator writes the image of the running container, so for a function it is the runtime image, for example `node:22-alpine`. |
| `spec.replicas` | integer | none | none | The replica count at the time of the record. |
| `spec.changeTitle` | string | none | none | A readable reason, such as `Deploy ghcr.io/acme/storefront:1.4.2`, `Deploy acme/storefront@main` or `Deploy function (node)`. |
| `spec.source` | string | none | none | Where the deploy came from. The dashboard writes `Dashboard`, or `GitHub` for a Git service. The operator writes `GeassApp controller`. |
| `spec.actor` | string | none | none | Who asked. The dashboard always writes `Dashboard`, even for a deploy made with an API token. |
| `spec.commit` | string | none | none | The commit of a Git service, when the service pins one. The operator fills it. The dashboard does not. |
| `spec.buildRef` | string | none | none | The [GeassBuild](/docs/reference/resources/geass-build/) that produced the image. Only the operator fills it. |
| `spec.imageDigest` | string | none | none | The value of the service's `status.resolvedImage` at the time of the record. |
| `spec.trigger` | string | none | none | The operator writes `Reconciliation`. The dashboard leaves it empty. |
| `spec.eventLogRef` | string | none | none | Nothing sets it. |
| `spec.rollbackRef` | string | none | none | Nothing sets it. |

## Status

| Field | Type | Meaning |
| --- | --- | --- |
| `status.phase` | string | A free-form string. The dashboard writes `Recorded`. The operator writes `RollingOut` or `Available` on `<app>-active`. |
| `status.startedAt` | time | On a dashboard record, when the deploy was recorded. |
| `status.completedAt` | time | On a dashboard record, the same moment. |
| `status.availableReplicas` | integer | On `<app>-active`, the available replicas of the Deployment. |
| `status.healthCheck` | string | On `<app>-active`, `RollingOut` or `Available`. |
| `status.conditions` | list | Nothing writes conditions for this kind. |

Both writers send the status to the status endpoint after they create or update the object, and the operator writes it only when it changed. `<app>-active` is `Available` when the Deployment has the replicas it should have, which is none for a service that is [asleep](/docs/guides/scale-to-zero/). A record that an older release of Geass wrote has no status, because that release sent the status with the request that created the object, and the API server ignores it there.

The dashboard treats a missing `phase` as `Recorded`. It shows the label `Skipped` for a record whose phase is `skipped`, but nothing writes that value. The deploy outcome shown on the **Deployments** tab comes from the service's provision attempts and its `Ready` condition, not from these records.

## How the dashboard uses records

The **Deployments** tab of a service lists the records together with the provision attempts of the service, newest first. The operator's `<app>-active` record is listed too, with the source `GeassApp controller`. The newest entry is shown as the active deploy unless it failed. The title is `spec.changeTitle`, and the detail line shows `spec.source` and `spec.actor`. Selecting a deploy opens its logs: the provision messages, the build log for a Git service, and the logs of the running pods. Pod logs are only shown for the current deploy.

A record can also be the target of a rollback. The rollback is a REST action, and the dashboard has no button for it:

```bash
curl -X POST "https://geass.example.com/api/apps/storefront-web/rollback" \
  -H "Authorization: Bearer $GEASS_TOKEN" \
  -d "revision=storefront-web-9nr6p"
```

The rule is:

| Service source | What a rollback does |
| --- | --- |
| Image | Sets `spec.source.image` to the record's `spec.image` (this drops a `pullSecret`), sets `spec.replicas` to the record's replicas, and deploys. |
| Git | Sets `spec.replicas` to the record's replicas and deploys. It does not change the commit or branch, so the code that runs does not change. |
| Function | Refused with `functions cannot be rolled back; restore the earlier code and deploy again`. |

A rollback puts back only what the table says. A record does not hold the service's variables or its other settings, so those stay as they are now: a variable added after the record was made is still there after a rollback to it.

A rollback is refused with `deployment revision does not belong to app` when the record does not carry the label `geass.dev/app` for that service. That includes `<app>-active`. See the [REST API](/docs/reference/rest-api/) reference and [Users and tokens](/docs/guides/users-and-tokens/) for tokens.

## Editing by hand

Do not create or edit these objects. They are history. Deleting one removes it from the **Deployments** tab and from the rollback targets. The operator recreates `<app>-active` on its next reconcile. A record you create yourself has no effect on what runs.

## What happens under the hood

A `GeassDeployment` creates no Kubernetes workloads. It is only data in `geass-system`. The labels differ by writer:

| Writer | Labels |
| --- | --- |
| The dashboard | `geass.dev/managed-by=geass`, `geass.dev/app=<app>`, `geass.dev/project`, `geass.dev/environment` |
| The operator | `geass.dev/managed-by=geass`, `geass.dev/kind=GeassDeployment`, `geass.dev/name=<app>`, `geass.dev/system-namespace=geass-system`, `geass.dev/project`, `geass.dev/environment` |

List them with these commands:

```bash
kubectl get geassdeployments -n geass-system -l geass.dev/app=storefront-web \
  -o custom-columns=NAME:.metadata.name,TITLE:.spec.changeTitle,IMAGE:.spec.image,CREATED:.metadata.creationTimestamp
kubectl get geassdeployment storefront-web-active -n geass-system -o yaml
```

## Troubleshooting

**The Deployments tab shows only one entry for a service I created with `kubectl`.** Only the dashboard records deploys. A service deployed by hand has the operator's `<app>-active` record and the service's provision attempts, but no dashboard records.

**I only see the last 12 deploys.** The dashboard keeps the newest 12 records of each service.

**`status` is empty on a record.** The record was written by an older release of Geass. Records written now carry a status. See [Status](#status).

**A rollback does nothing to the code of a Git service.** A Git rollback restores the replica count only. To run older code, pin `spec.source.git.commit` to the commit you want and deploy, or deploy from a branch that holds it.

**A rollback fails with `deployment revision does not belong to app`.** The record belongs to another service, or it is the operator's `<app>-active` record. Pick a record that carries the label `geass.dev/app=<app>`.

## Related

- [GeassApp](/docs/reference/resources/geass-app/#the-draft-and-deploy-workflow)
- [GeassBuild](/docs/reference/resources/geass-build/)
- [REST API](/docs/reference/rest-api/)
- [Resources overview](/docs/reference/resources/overview/)
