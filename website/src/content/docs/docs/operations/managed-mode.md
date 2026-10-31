---
title: "Managed mode"
description: "For a platform that runs Geass for others: refuse writes to sign-in, the dashboard domain, and installer settings, and show them read-only."
---

Some platforms run Geass for the people who use it. An example is a hosting service such as
[rungeass](https://rungeass.com). Such a platform decides some things itself:

- Who can sign in.
- The domain of the dashboard.
- How Geass and K3s are updated.

A person who runs their own Geass changes these things from the dashboard or with the installer. On a Geass that
someone else runs for you, these changes could break what the platform sets up.

**Managed mode** is one switch in the platform configuration. When managed mode is on, the API of the dashboard refuses
a write to those things. The dashboard shows them as they are, read-only, with the reason. Managed mode is
**off unless you turn it on**. When managed mode is off, nothing changes. Every route answers as it did, and the dashboard shows what
it showed.

Managed mode is a guard rail for the dashboard and its API. It is not a boundary against whoever can write the objects
of the cluster. For more information, see [What it does not do](#what-it-does-not-do).

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
| `spec.managed.provider` | Who manages the cluster, for the messages: "managed by rungeass". Up to 60 letters, digits, spaces, dots, underscores, and hyphens. If it is empty, the messages say "the platform that runs this cluster" |

The change takes effect with no restart, when the operator has seen it. This takes a moment. The dashboard process
that runs apart from the controllers reads the same object. For that process, see
[Dashboard and controllers apart](/docs/operations/dashboard-only/). Because both processes read one object, the two
cannot disagree. A flag on each process could disagree. No route of the API of the dashboard writes `spec.managed`.
The platform sets `spec.managed` with the tool that it uses to write the objects of the cluster. A person who is signed
in to the dashboard cannot turn managed mode off.

## What it keeps

Five things belong to the platform. Geass refuses a write to each of them for every caller, administrators and admin
tokens included. Geass refuses these writes on the routes of the dashboard and on the same operations under `/api/v1`.

| Area | `area` in the refusal | Writes it refuses | What the dashboard shows |
| --- | --- | --- | --- |
| Sign-in | `signIn` | `POST /api/settings/auth/save`, `mode`, `clear`, `logout-policy`, and `tenancy` (the identity provider, how people sign in, how the token names organizations, and automatic organizations), `POST /api/orgs/{org}/idp/save`, `enable`, and `clear` (an organization's own provider), the writes under `/api/users`, and `POST /api/setup` (a [setup link](/docs/operations/installer/#the-setup-link)) | **Settings > Sign-in** and an organization's own provider, as they are, read-only, with the reason |
| The dashboard's domain and DNS | `dashboardDomain` | `POST /api/settings/domain/save` and `verify`, and `POST /api/settings/cloudflare/save`, `clear`, `discover`, and `refresh`. Geass uses Cloudflare to write the DNS records and the tunnel of the dashboard | **Settings > Domain** and the Cloudflare connector, read-only. No banner that asks for a domain. The GitHub connector says who set the public host |
| The installer | `installer` | `POST /api/settings/etcd-snapshots/save` and `clear`. These set the bucket that receives a copy of the state of the cluster. The installer tells K3s one time to send the state to that bucket | **Usage > Cluster backup**: what is there, with no form and no installer command |
| Updating Geass | `upgrades` | None. The dashboard has no route that updates Geass. Updates are the job of the installer | **Usage > Version** says who updates Geass, and does not tell the person to run the installer |
| The K3s version | `k3sVersion` | None. No route of the dashboard sets `spec.k3s.version` or updates K3s | The same section |

The last two areas are in the list so that the dashboard can say that they are managed. The table is complete. Geass
does not refuse a write that is not in the table.

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

The status is `403` because the refusal does not depend on who asks or on the state of the resource. Thus, a `409`
would be wrong. A program switches on `code: "managed"`. Geass leaves out `managedBy` when the configuration names no
provider. A refused write changes nothing. It does not reach anything outside the cluster, for example Cloudflare or an
identity provider. Geass logs the refusal as `Refused a change that the platform manages`, with the area and the path.

If Geass cannot read the platform configuration when a write to one of these routes arrives, it answers `500`
`could not check whether the platform manages this setting` and does nothing. Geass refuses when it cannot tell.

## What it does not change

- **Reads.** Every `GET` answers as before. This includes the sign-in settings without the client secret, the domain,
  the state of the backup, and the versions.
- **Everything that is not one of the five.** These things include projects, services, databases, buckets,
  and API tokens. They also include organizations and their workspaces (`POST /api/orgs/create`, `update`, `delete`,
  `workspaces/...`), pools, servers, node placement, the registry, alerts, GitHub, and the connectors for clouds. A platform that makes organizations and pools through the
  API can continue to do so.
- **The controllers.** No reconciler looks at managed mode. The controllers continue to act on what the objects say.
  This includes `spec.k3s.version` and the DNS records of the dashboard domain. For `spec.k3s.version`, the controllers
  move the workers one at a time to a version that the control plane already runs.

## How the dashboard knows

For a platform administrator, the bootstrap that the dashboard reads (`GET /api/bootstrap`) has `platform.managed` when
managed mode is on. When managed mode is off, the bootstrap has no such key:

```json
{ "platform": { "managed": { "enabled": true, "provider": "rungeass", "locked": ["installer", "upgrades", "k3sVersion", "signIn", "dashboardDomain"] } } }
```

`@geass/dashboard-ui` reads this key. Each page of an area in `locked` renders as it is, with a **Managed by** notice
above it. Every field and button of that page is off, because the page is in a disabled fieldset. Managed mode hides
no page. The `hide` option of the library hides pages, for a host that mounts the dashboard in its own app. A hidden
page says that it is not available. If you use both, `hide` wins.

Geass does not tell a viewer or an organization administrator. A viewer is a session that cannot change anything. The
bootstrap of a viewer never reads the platform configuration, and a test makes sure of this. An organization has none
of these pages. The pages of these sessions are already read-only or not theirs, and the server refuses a write from
any sender.

## What it does not do

- **It is not a boundary.** Whoever can write `GeassPlatformConfig`, `GeassCluster`, or the operator's Deployment can
  turn managed mode off or change what it keeps. The installer on the control plane can do this too. Managed mode
  stops a person who uses the dashboard, the command line, the MCP server, or a script with an API token. It also stops
  a mistake. Protect the objects with Kubernetes RBAC as before.
- **It does not change the installer.** The installer (`geass`, the install script) runs on a machine and does not ask
  the dashboard for anything. Thus, the installer does not read managed mode, and managed mode does not stop the
  installer. Nobody should run the installer on a cluster whose control plane the platform hosts. For more
  information, see [Hosted control plane](/docs/operations/hosted-control-plane/). Managed mode does not make it safe
  to run the installer there.
- **It does not stop `kubectl`.** Sign-in (`spec.auth`), the domain (`spec.dashboardURL`, `spec.rootDomain`), and the
  K3s version (`spec.k3s.version` of the `GeassCluster`) are fields of objects. Managed mode refuses the routes of the
  dashboard that write these fields. It does not stop anything else that writes them.
- **It ends no sign-in that already works.** People continue to sign in through the identity provider. They also
  continue to sign in through an organization's own provider, if that provider was set up before managed mode
  went on. Automatic organizations (`spec.auth.tenancy.autoProvision`) continue to work as set.
  `GET /auth/oidc/login?purpose=test` still starts a test sign-in. A test sign-in keeps its result in the memory of
  the operator and saves nothing.
- **It does not lock API tokens.** `POST /api/tokens/create` is a person's own credential, not a sign-in setting. If a
  platform does not want the tokens of its customers, the platform refuses `/api/tokens` and `/api/v1/tokens` at its
  own proxy. rungeass does this.
- **Messages from the controllers are the controllers'.** The Version table shows the status of a K3s upgrade that
  waits for the control plane. When the control plane is on one of the nodes of the cluster, this status can still say
  to run the installer. On a hosted control plane, the status says that whoever hosts the control plane moves it.

## What has been tested

- **Go tests of the API** (`internal/dashboard/managed_test.go`, with a fake client and the dashboard's whole handler
  chain, authentication included). The tests turn managed mode on. Then, as a signed-in administrator, they send each
  of the 16 writes in the table over the route of the dashboard. These 16 writes are for the three areas that have routes,
  and for an organization's own provider. Geass refuses each write with these values:
  - `403`.
  - `code` `managed`.
  - The area.
  - The name of the platform.

  Nothing stored changes: the configuration, the cluster, the organizations, and the Secrets. The Cloudflare routes
  reach no other host. Geass refuses nine of the writes over `/api/v1` with an admin token. The message names the
  platform. If the platform has no name, the message says "the platform that runs this cluster". When the tests turn
  managed mode off, or remove the field, the same write goes through. Reads are untouched. Writes that are not one of
  the five are untouched, for example creating an organization, the registry, alerts, and GitHub. When Geass cannot
  read the configuration, it refuses a write to one of the five with `500`. It does not hold up a write to another
  route. The tests check the path rules against near misses (`/settings/authority`, `/orgs/x/update`, `/orgs/create`).
  The bootstrap tells administrators, and does not tell a viewer.
- **Off is as before.** The tests send the same 12 writes that need no network to two clusters. One cluster has no
  `spec.managed`. The other has `enabled: false` and a provider. These results are identical, byte for byte:
  - Every status and body.
  - The objects stored afterwards.
  - The whole bootstrap.

  The one difference is the field in the echo of the platform configuration, and the tests remove it before they
  compare. `platform` has no `managed` key. Every existing test of the dashboard package still passes.
- **Dashboard tests** (`dashboard-ui/src/managed.test.jsx` and `lib/managed.test.js`, with the whole dashboard mounted
  over a stub of the API). With nothing managed, these parts are editable: Sign-in, Domain, Cluster backup, the
  Cloudflare connector, and an organization's own provider. The dashboard shows the installer's commands, and the
  banner asks for a domain. With managed mode on, these results occur:
  - Each part shows its notice.
  - The settings stay readable.
  - Every field and button of the part is off, and a click sends nothing.
  - The installer's commands are gone.
  - The banner is gone.
  - The Version section says who updates.

  An area that is not in `locked` stays editable. The tests compare the text of the messages with the text of the
  server, word for word. If you change the fieldset so that it no longer disables, three of the tests fail.
- `go run ./hack/crdcompat` reports that the CRDs are compatible with the release baseline. `spec.managed` is a new
  optional field.
- **On a real K3s on EC2 (2026-10-28), K3s v1.36.4, Geass built from this branch.** This test had 32 writes. These
  writes were 16 routes, each over the `/api` of the dashboard and over `/api/v1`.
  - With `spec.managed` never set, none of the 32 writes answered `managed`. The bootstrap had no `managed` key.
  - With managed mode on, the bootstrap of a platform administrator carried `managed` with
    `locked: [installer, upgrades, k3sVersion, signIn, dashboardDomain]`. The bootstrap of a viewer did not.
  - With managed mode on, **all 32 writes were refused `403` with `code: managed`**. Each refusal had the right `area`
    and `managedBy`. By area, the count was sign-in 16, dashboard domain 12, and installer 4.
  - The refusal comes before a route's own refusal. An API token cannot change the sign-in routes anyway ("only
    from the dashboard by a signed-in admin"). These routes also answered `managed`.
  - After the refused writes, a snapshot was identical. The snapshot had the platform configuration, the cluster and
    organization specs, every Secret in `geass-system`, and the ConfigMaps.
  - Reads and unrelated writes were untouched. These writes were creating a project or an organization, an import,
    and an export.
  - With managed mode turned off again, the writes worked. The test showed this for the domain of the dashboard, the
    Cloudflare connection, and the cluster-state backup. The backup wrote its Secret and removed it again.
  - The change showed in the bootstrap immediately, with no restart.
  - If `provider` is left out, the message has no name, and the refusal has no `managedBy`.
  - `enabled: false` is off.
  - The CRD refuses a provider with markup in it, or a provider over 60 characters.
  - The operator logged each refusal with the area and the path.

**Not tested:** the tests did not cover these items:

- The read-only notice in a browser. The dashboard tests run in jsdom, which has no layout and does not draw the
  notice. A real session needs a password login.
- A real sign-in session. The sign-in routes were tried with the API token of an administrator. These routes refuse
  that token with or without managed mode. Thus, the tests did not show "writes work again when it is turned off" for
  sign-in.
- A real identity provider.
- A real Cloudflare account, beyond what a fixture token gets.
- The dashboard-only process.
- The rungeass operator itself, with managed mode on. Only the `--managed` flag of the installer tool was used.
- The installer on a machine whose Geass is managed.

The controllers were not changed.
