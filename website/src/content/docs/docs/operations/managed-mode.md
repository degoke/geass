---
title: "Managed mode"
description: "For a platform that runs Geass for other people: refuse writes to sign-in, the dashboard's domain and the installer's settings, and show them read-only."
---

A platform that runs Geass for the people who use it (a hosting service such as [rungeass](https://rungeass.com)) decides some things
itself: who may sign in, the dashboard's domain, how Geass and K3s are updated. A person who runs their own Geass changes these from
the dashboard or with the installer. On a Geass that someone else runs for you, that would be a way to break what the platform sets
up.

**Managed mode** is one switch, in the platform configuration, that makes the dashboard's API refuse a write to those things and the
dashboard show them as they are, read-only, with the reason. It is **off unless you turn it on**. With it off, nothing changes: every
route answers as it did, and the dashboard shows what it showed.

It is a guard rail for the dashboard and its API. It is not a boundary against whoever can write the cluster's objects: see
[What it does not do](#what-it-does-not-do).

## Turn it on

Set `spec.managed` on the [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/) named `platform`:

```yaml title="platform-managed.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassPlatformConfig
metadata:
  name: platform
  namespace: geass-system
spec:
  managed:
    enabled: true
    provider: rungeass
```

```bash
kubectl apply -f platform-managed.yaml
```

| Field | Meaning |
| --- | --- |
| `spec.managed.enabled` | Turns managed mode on. Unset or `false` is off |
| `spec.managed.provider` | Who manages the cluster, for the messages: "managed by rungeass". Up to 60 letters, digits, spaces, dots, underscores and hyphens. Left empty, the messages say "the platform that runs this cluster" |

It takes effect with no restart, as soon as the operator has seen the change (a moment). The dashboard process that runs apart from the controllers (see
[Dashboard and controllers apart](/docs/operations/dashboard-only/)) reads the same object, so the two cannot disagree, which a
flag on each process could. No route of the dashboard's API writes `spec.managed`: the platform sets it, with whatever it writes the
cluster's objects with, and a person signed in to the dashboard cannot turn it off.

## What it keeps

Five things belong to the platform. Each is refused for every caller, administrators and admin tokens included, on the dashboard's
routes and on the same operations under `/api/v1`.

| Area | `area` in the refusal | Writes it refuses | What the dashboard shows |
| --- | --- | --- | --- |
| Sign-in | `signIn` | `POST /api/settings/auth/save`, `mode`, `clear`, `logout-policy` and `tenancy` (the identity provider, how people sign in, how the token names organizations, automatic organizations), and `POST /api/orgs/{org}/idp/save`, `enable` and `clear` (an organization's own provider) | **Settings > Sign-in** and an organization's own provider, as they are, read-only, with the reason |
| The dashboard's domain and DNS | `dashboardDomain` | `POST /api/settings/domain/save` and `verify`, and `POST /api/settings/cloudflare/save`, `clear`, `discover` and `refresh` (Cloudflare is how Geass writes the dashboard's DNS records and tunnel) | **Settings > Domain** and the Cloudflare connector, read-only; no banner that asks for a domain; the GitHub connector says who set the public host |
| The installer | `installer` | `POST /api/settings/etcd-snapshots/save` and `clear`: the bucket the cluster's own state is copied to, which K3s is told to send to once, by the installer | **Usage > Cluster backup**: what is there, with no form and no installer command |
| Updating Geass | `upgrades` | None: the dashboard has no route that updates Geass, which is the installer's job | **Usage > Version** says who updates it, and no longer tells the person to run the installer |
| The K3s version | `k3sVersion` | None: no route of the dashboard sets `spec.k3s.version` or updates K3s | The same section |

The last two are in the list so that the dashboard can say they are managed. The table is complete: a write that is not in it is not
refused.

### What a refusal looks like

```http
POST /api/v1/settings/domain/save
```

```http
HTTP/1.1 403 Forbidden
Content-Type: application/json

{
  "error": "The dashboard's domain and DNS are managed by rungeass and cannot be changed here.",
  "code": "managed",
  "area": "dashboardDomain",
  "managedBy": "rungeass"
}
```

The status is `403` because the refusal does not depend on who asks or on the state of the resource, so a `409` would be wrong. A
program switches on `code: "managed"`. `managedBy` is left out when the configuration names no provider. A refused write changes
nothing and does not reach anything outside the cluster (Cloudflare, an identity provider). It is logged as `Refused a change that the
platform manages` with the area and the path.

If Geass cannot read the platform configuration while a write to one of these routes is arriving, it answers `500`
`could not check whether the platform manages this setting` and does nothing. It refuses when it cannot tell.

## What it does not change

- **Reads.** Every `GET` answers as before: the sign-in settings (without the client secret), the domain, the backup's state, the
  versions.
- **Everything that is not one of the five.** Projects, services, databases, buckets, API tokens, organizations and their
  workspaces (`POST /api/orgs/create`, `update`, `delete`, `workspaces/...`), pools, servers, node placement, the registry, alerts,
  GitHub, and the connectors for clouds. A platform that makes organizations and pools through the API goes on doing so.
- **The controllers.** Nothing in a reconciler looks at managed mode. They go on acting on what the objects say, including
  `spec.k3s.version` (workers are moved one at a time to a version the control plane already runs) and the dashboard domain's DNS
  records.

## How the dashboard knows

For a platform administrator, the bootstrap that the dashboard reads (`GET /api/bootstrap`) has `platform.managed` while managed mode
is on, and has no such key while it is off:

```json
{ "platform": { "managed": { "enabled": true, "provider": "rungeass", "locked": ["installer", "upgrades", "k3sVersion", "signIn", "dashboardDomain"] } } }
```

`@geass/dashboard-ui` reads it. Each page of an area in `locked` renders as it is, with a **Managed by** notice above it, and every
field and button of it off (a disabled fieldset). Managed mode hides no page. The library's `hide` option does, for a host that
mounts the dashboard in its own app: the page then says it is not available. With both, `hide` wins.

A session that cannot change anything (a viewer) and an organization's administrator are not told: a viewer's bootstrap never reads the
platform configuration (a test pins that), and an organization has none of these pages. Their pages are read-only or not theirs
already, and the server refuses a write whoever sends it.

## What it does not do

- **It is not a boundary.** Whoever can write `GeassPlatformConfig`, `GeassCluster` or the operator's Deployment can turn it off or
  change what it keeps; so can the installer on the control plane. It stops a person using the dashboard, the command line, the MCP
  server or a script with an API token, and it stops a mistake. Protect the objects with Kubernetes RBAC as before.
- **It does not change the installer.** The installer (`geass`, the install script) runs on a machine and does not ask the dashboard
  for anything, so it neither reads managed mode nor is stopped by it. Nobody should run it on a cluster whose control plane the
  platform hosts (see [Hosted control plane](/docs/operations/hosted-control-plane/)). Managed mode does not make it safe to.
- **It does not stop `kubectl`.** Sign-in (`spec.auth`), the domain (`spec.dashboardURL`, `spec.rootDomain`) and the K3s version
  (`spec.k3s.version` of the `GeassCluster`) are fields of objects. Managed mode refuses the dashboard's routes that write them,
  and does not stop anything else that writes them.
- **It ends no sign-in that already works.** People go on signing in through the identity provider and through an organization's
  own provider that was set up before managed mode went on, and automatic organizations (`spec.auth.tenancy.autoProvision`) go on
  working as set. `GET /auth/oidc/login?purpose=test` still starts a test sign-in, which keeps its result in the operator's memory and
  saves nothing.
- **It does not lock API tokens.** `POST /api/tokens/create` is a person's own credential, not a sign-in setting. A platform that does
  not want its customers' tokens refuses `/api/tokens` and `/api/v1/tokens` at its own proxy, as rungeass does.
- **Messages from the controllers are the controllers'.** The status of a K3s upgrade that waits for the control plane, shown in the
  Version table, can still say to run the installer when the control plane is on one of the cluster's nodes. On a hosted control plane
  it says that whoever hosts it moves it.

## What has been tested

- **Go tests of the API** (`internal/dashboard/managed_test.go`, with a fake client and the dashboard's whole handler chain,
  authentication included). With managed mode on, each of the 16 writes in the table (the three areas that have routes, and an
  organization's own provider) is refused with `403`, `code` `managed`, the area and the platform's name, over the dashboard's route as
  a signed-in administrator, and nothing stored changes (the configuration, the cluster, the organizations, the Secrets); the Cloudflare
  routes reach no other host. Nine of them are refused over `/api/v1` with an admin token. The message names the platform, or says "the
  platform that runs this cluster" when it has no name. Switching it off, or removing the field, lets the same write through.
  Reads, and writes that are not one of the five (creating an organization, the registry, alerts, GitHub), are untouched. When the
  configuration cannot be read, a write to one of the five is refused with `500` and a write to another is not held up. The path
  rules are checked against near misses (`/settings/authority`, `/orgs/x/update`, `/orgs/create`). Administrators are told in the
  bootstrap, and a viewer is not.
- **Off is as before.** The same 12 writes that need no network are sent to two clusters, one with no `spec.managed` and one with
  `enabled: false` and a provider: every status and body, the objects stored afterwards and the whole bootstrap are identical, byte for
  byte (the one difference is the field in the echo of the platform configuration, which is removed before comparing), and `platform`
  has no `managed` key. Every existing test of the dashboard package still passes.
- **Dashboard tests** (`dashboard-ui/src/managed.test.jsx` and `lib/managed.test.js`, with the whole dashboard mounted over a stub of
  the API). With nothing managed, Sign-in, Domain, Cluster backup, the Cloudflare connector and an organization's own provider are
  editable, the installer's commands are shown and the banner asks for a domain. With managed mode on, each shows its notice, the
  settings stay readable, every field and button of it is off (a click sends nothing), the installer's commands are gone, the
  banner is gone, and the Version section says who updates. An area that is not in `locked` stays editable. The text of the
  messages is compared with the server's, word for word. Changing the fieldset to no longer disable fails three of them.
- `go run ./hack/crdcompat` reports the CRDs as compatible with the release baseline: `spec.managed` is a new optional field.
- **On a real K3s on EC2 (2026-10-28), K3s v1.36.4, Geass built from this branch.** With `spec.managed` never set, none of 32 writes (16 routes, each over the dashboard's `/api` and over `/api/v1`) answered `managed`, and the bootstrap had no `managed` key. With it on, a platform administrator's bootstrap carried `managed` with `locked: [installer, upgrades, k3sVersion, signIn, dashboardDomain]` and a viewer's did not, and **all 32 writes were refused `403` with `code: managed`**, the right `area` (sign-in 16, dashboard domain 12, installer 4) and `managedBy`. The refusal comes before a route's own refusal: the sign-in routes, which an API token cannot change anyway ("only from the dashboard by a signed-in admin"), also answered `managed`. A snapshot of the platform configuration, the cluster and organization specs, every Secret in `geass-system` and the ConfigMaps was identical after the refused writes; reads and unrelated writes (creating a project or an organization, an import and an export) were untouched; and with it turned off again the writes worked (shown for the dashboard's domain, the Cloudflare connection and the cluster-state backup, which wrote its Secret and removed it again). The change showed in the bootstrap at once, with no restart. A `provider` left out gives the message without a name and no `managedBy`; `enabled: false` is off; the CRD refuses a provider with markup in it or one over 60 characters. The operator logged each refusal with the area and the path.

**Not tested:** the read-only notice in a browser (the dashboard tests run in jsdom, which has no layout and does not draw it, and a real session needs a password login), a real sign-in session (the sign-in routes were tried with an administrator's API token, which they refuse with or without managed mode, so "writes work again when it is turned off" was not shown for sign-in), a real identity provider, a real Cloudflare account beyond what a fixture token gets, the dashboard-only process, rungeass's own operator running with it (only the installer tool's `--managed` flag was used), or the installer on a machine whose Geass is managed. The controllers were not changed.
