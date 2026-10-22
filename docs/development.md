# Development guide

How to build, run and test Geass. User documentation is in [`website/`](../website) and published at
<https://rungeass.com/docs/>; this page is for people changing the code. [CONTRIBUTING.md](../CONTRIBUTING.md) says how to
propose a change.

## What you need

| Tool | For | Notes |
| --- | --- | --- |
| Go | everything | The version in [`go.mod`](../go.mod) (1.26). The Makefile downloads `controller-gen`, `kustomize`, `golangci-lint` and the envtest binaries into `bin/` itself. |
| Docker | the installer, the operator image, the k3s tests | Not needed for the command line, the unit tests or lint. |
| Node.js 24 and pnpm | the dashboard (`dashboard-ui/`) and the website (`website/`) | Only when you change those. `corepack enable` provides pnpm. |
| kind | the e2e tests | Only for `make test-e2e`. Use a cluster that exists for the tests, never one you care about. |
| A cluster | running Geass against something real | `kubectl` pointing at any cluster, or `make k3s-up` for a throwaway k3s in Docker. |

A [devcontainer](../.devcontainer) with Go and Docker is provided.

## The repository

```text
cmd/main.go            the operator: controllers, the dashboard and the JSON API, in one binary
cmd/geass/             the installer: installs or updates K3s and Geass on a server
cmd/geass-cli/         the geass command line, including the MCP server (geass mcp)
api/v1alpha1/          the custom resource types (+kubebuilder markers); zz_generated.* is generated
internal/controller/   reconciliation: one controller per resource, plus capacity, cluster issuer, K3s upgrade
internal/dashboard/    the HTTP server, /api/v1, auth, and the embedded dashboard bundle (frontend/dist)
internal/cli/          the command line's commands
internal/mcpserver/    the MCP server and its tools
pkg/installer/         the installer's steps (checks, K3s, operator, upgrade, join)
pkg/platform/          names, labels, images and defaults shared by the operator and the dashboard
pkg/release/           the release the binary was built as, and how releases and K3s versions are ordered
pkg/migrations/        data migrations the operator runs on update (idempotent, appended, never edited)
pkg/crdcompat/         the rules behind make crd-compat
pkg/...                cloud connectors (AWS, Cloudflare, PlanetScale), GitHub, OIDC, SSH, templates, webhooks, API client
config/                kustomize bases: CRDs, RBAC, manager, samples (CRDs and RBAC are generated)
dist/install.yaml      the whole operator as one manifest (generated: make build-installer)
dashboard-ui/          the React dashboard (Vite, Mantine); builds into internal/dashboard/frontend/dist
website/               the public site and user documentation (Astro, Starlight)
geass-brand/           logos, icons and design tokens
hack/                  helper programs and scripts: crd compatibility, release, load test, k3s test clusters
scripts/               install.sh and install-cli.sh, which are published with every release
test/                  e2e (kind), k3sintegration (real k3s), upgrade (CRD upgrade), limits, scripts
docs/                  this guide, the release process, the design guide
```

The command line and the installer are separate binaries but one release: see [releasing.md](releasing.md).

## Everyday commands

`make help` lists every target. The ones you will use:

```bash
make build-cli           # bin/geass-cli-<os>-<arch>: needs only Go
make build               # the operator (builds the dashboard first)
make manifests generate  # after editing api/ types or +kubebuilder markers: CRDs, RBAC, deepcopy
make build-installer     # regenerate dist/install.yaml (CI fails when it is stale)
make lint-fix            # golangci-lint with fixes
make test                # unit tests (see below)
make crd-compat          # fail if the CRDs would break stored data, against hack/crd-baseline-ref
```

A change to a `*_types.go` file or a marker needs `make manifests generate build-installer` and the generated files
committed. Never edit generated files by hand: the list is in [AGENTS.md](../AGENTS.md).

## Running Geass from your machine

The operator and the dashboard can run on your host against whatever cluster `kubectl` points at:

```bash
make run     # installs the CRDs on the current context, then runs the operator (API on :8085) and the Vite dev server
```

Or run the two halves yourself: `make dashboard-backend` (the operator with the dashboard API on :8085) and
`make dashboard-dev` (the Vite dev server). The dashboard signs in with the users in the `geass-system` Secret; the
[sign-in guide](https://rungeass.com/docs/getting-started/sign-in/) says how they are created. `GEASS_ALLOW_LOOPBACK=true`,
which `make dashboard-backend` sets, lets Settings → Sign-in use an identity provider on your own machine.

## Building the installer

`make build-geass` builds the operator image for your architecture, embeds it and the manifests in the installer, and
writes `bin/geass`. It needs Docker. `GEASS_GOARCH=arm64` builds the arm64 installer (the Dockerfile cross-compiles, so
nothing is emulated). `make release-assets GEASS_VERSION=v0.0.0-test` builds everything a release publishes into `bin/`.

The version comes from `git describe` unless you set `GEASS_VERSION`. A build that is some commits past a tag
(`v0.2.0-3-g1a2b3c4`) is ordered after the tag by [`pkg/release`](../pkg/release/release.go); `dev` is not a release and
cannot be ordered.

## Tests

| Kind | Command | What it needs | What it covers |
| --- | --- | --- | --- |
| Unit and envtest | `make test` | Go. The first run downloads a real `kube-apiserver` and `etcd`. | Controllers against a real API server, the dashboard and API, the CLI, the installer's logic, the CRD upgrade test. |
| Install scripts | `sh test/scripts/install-scripts-test.sh` | a POSIX shell | `install.sh` and `install-cli.sh` against a release laid out in a directory: checksum, tamper and missing-file cases. |
| End to end | `make test-e2e` | Docker and kind | The operator deployed into a kind cluster. |
| k3s integration | `eval "$(make -s k3s-up)"`, then `make test-k3s-serverless`, `test-k3s-objectstorage`, `test-k3s-limits` | Docker | Things only a real k3s has: its Helm controller, Traefik, KEDA, object storage, network policy. See [test/k3sintegration/README.md](../test/k3sintegration/README.md). |
| High availability | `make test-k3s-ha` | Docker | A three-server K3s where the server running the operator is taken down. |
| Load | `make loadtest LOADTEST_ARGS="-apps 250,500,1000"` | Docker | What the operator costs as a cluster grows. See [hack/loadtest/main.go](../hack/loadtest/main.go). |
| Node pools, simulated | `make test-k3s-nodepool` (after `make k3s-up`) | Docker | A pool adding and removing nodes on a real scheduler, with a container standing in for each machine. |
| Amazon EC2, live | `go test -tags live -run TestLive -v ./pkg/compute/aws` | An AWS account and an IAM user's key (below) | The EC2 driver against the real API. It spends a few cents. |

The EC2 live test is behind the `live` build tag, so no ordinary test run reaches AWS. It needs `GEASS_LIVE_AWS_ACCESS_KEY_ID` and
`GEASS_LIVE_AWS_SECRET_ACCESS_KEY` (and `GEASS_LIVE_AWS_REGION`, default `eu-north-1`), the key of an IAM user that has only the
policy in [Cloud connections for machines](../website/src/content/docs/docs/guides/connectors/clouds.md#amazon-ec2): the driver takes an
access key and secret, not a session token, so a role's temporary credentials do not work. It launches one `t3.micro`, checks it
boots and answers on port 22, and terminates it. Its cleanup terminates every instance and tries to delete every security group that
carries its run's `geass-cluster` tag, but the policy does not let the key delete a group (Geass never does), so sweep what is left
with an administrator's key, by that tag, and check the console. Do not run it in an account where an organization policy denies
`ec2:RunInstances`.

The upgrade test reads the baseline release (`hack/crd-baseline-ref`) from git history, so it needs a full clone: in a
shallow one it skips and says why, and `make crd-compat` fails. Set `GEASS_REQUIRE_UPGRADE_TESTS=1` to make a test that
cannot run fail instead of skip, as CI does.

Backups go to AWS, so a throwaway cluster has nowhere to put them. A manager built with `-tags geasslivetest` reads
`GEASS_LIVETEST_S3_ENDPOINT` (a host and port) and keeps backups there instead, in an S3 server such as MinIO that the cluster's
pods can reach (`internal/controller/backup_standin.go`). A release is not built with the tag and has no such setting. The
project still needs a cloud connection, a backup storage choice and a Secret `geass-backup-storage-<bucket>` in `geass-system`
(with its `geass.dev/backup-storage-config` annotation), so that Geass does not call AWS to make the bucket and its user.

Tests use the standard `testing` package with testify, and Ginkgo for the e2e suite. A behaviour that depends on K3s, on a
real node or on the network between pods is not proven by the envtest suite; the k3s integration tests exist for those, and
the documentation says when a claim has only been checked on a particular platform.

## The dashboard

`dashboard-ui/` is a React app. `make dashboard-build` builds it into `internal/dashboard/frontend/dist`, which the
operator embeds; that directory is committed so that `go build` works without Node, and it is rebuilt inside the operator
image by the Dockerfile. If you change the dashboard, rebuild and commit the bundle with the source. Design tokens live in
[`geass-brand/tokens`](../geass-brand) and the rules in [design.md](design.md).

### The dashboard as a library

The same sources also build as a package, `@geass/dashboard-ui`, that another React app mounts as a component. The app
the operator embeds is unchanged: it is `vite.config.js` and `src/main.jsx`, as before. The library is `vite.lib.config.js`
and `src/index.js`.

```bash
cd dashboard-ui
pnpm build:lib   # dist-lib/index.js and dist-lib/style.css (not committed); `make dashboard-lib` does the same
pnpm test        # vitest: the transport, the hide options, and the component mounted under a base path; `make dashboard-test`
pnpm pack        # a tarball with dist-lib and the type definitions
```

Another repository can use it from a pnpm workspace (`"@geass/dashboard-ui": "workspace:*"`, after `pnpm build:lib`), from a
tarball (`pnpm pack`, then `"file:../geass-dashboard-ui-0.1.0.tgz"`), or from git: `pnpm add "github:degoke/geass#<commit>&path:/dashboard-ui"`.
Installing from git makes pnpm install the dashboard's own dependencies and run `prepack`, which builds the library (a minute or so),
and pnpm 10 and later refuse to run that build until the host lists the package in `allowBuilds` in its `pnpm-workspace.yaml`, under the
full name pnpm prints (`@geass/dashboard-ui@git+https://github.com/degoke/geass.git#<commit>&path:/dashboard-ui: true`). A tarball
built in CI is simpler for a build that runs often. The package is private, so nothing publishes it by accident. React, Mantine,
TanStack Router and Query are peer dependencies: the host provides one of each, and the library build leaves them as imports.

```jsx
import "@mantine/core/styles.css";
import "@mantine/notifications/styles.css";
import "@geass/dashboard-ui/style.css";
import { MantineProvider } from "@mantine/core";
import { Notifications } from "@mantine/notifications";
import { GeassDashboard, geassTheme, SELF_HOSTED_ONLY } from "@geass/dashboard-ui";

// A route of the host such as /$org/clusters/$id/$ (a splat), so that the page stays mounted while the dashboard navigates.
function ClusterPage({ org, id }) {
  return (
    <GeassDashboard
      basePath={`/${org}/clusters/${id}`}
      history={router.history}              // the host's TanStack Router history: both routers see every change
      apiBaseUrl={`/api/clusters/${id}/geass`}  // the host's proxy to the cluster's Geass API
      getToken={() => tokenFor(id)}         // sent as `Authorization: Bearer <token>`
      hide={SELF_HOSTED_ONLY}
      style={{ height: "calc(100vh - 56px)" }}
    />
  );
}

<MantineProvider theme={geassTheme}>
  <Notifications />
  <RouterProvider router={router} />
</MantineProvider>
```

How it works, so that a change keeps it working:

- **The router.** `GeassDashboard` makes a router of its own with `basepath` set (`createDashboardRouter` in `src/router.jsx`), so
  every link and navigation in the pages stays below the host's path with no change to them. Pass the host router's
  `history` so that the two routers share one address; without it the dashboard uses the browser's, and the host's router does not hear
  of its navigation. Each router gets a fresh route tree, and a new `basePath` or `history` mounts the dashboard again.
- **Requests.** Everything that talks to Geass goes through `src/lib/transport.js`: `fetch` calls, the stream of changes and the log
  stream (read with `fetch` when there is a token, since `EventSource` cannot send a header), and the terminal's WebSocket
  (a browser cannot set a header on one, so the token is offered as the subprotocol `geass.bearer.<token>`, which the server reads
  for a terminal's upgrade only). A new call to the API must use `api`, `action` or the helpers there, never `fetch`, `EventSource` or
  `WebSocket` directly, or it breaks when the dashboard is hosted. A 401 asks `getToken({ forceRefresh: true })` and tries once more, and
  then the page offers a *Reconnect* button that calls `onUnauthorized`. Without `getToken` the cookie is used, so a host whose proxy adds
  the credential can leave it out, and `fetch` and `credentials` replace how the request is made.
- **Theme.** The host owns the `MantineProvider` (one provider means one colour scheme, and a Mantine portal in the host's body is not
  styled by two themes): give it `geassTheme`, or merge it into the host's theme, since the pages use the `signal` colour and square
  corners. The library brings no provider and no `Notifications`; the host mounts the latter. The stylesheet is the same `styles.css` as
  the app's, with every rule placed under `.geass-dashboard` at build time (`scopeUnder` in `vite.lib.config.js`) so that it does not
  restyle the host's own Buttons, and `src/embedded.css` makes the element fill the box the host gives it. Menus, dialogs and drawers
  open inside that element (a `Portal` target set on a nested theme), where the scoped styles reach them. Dark and light follow the
  `data-mantine-color-scheme` the host's Mantine puts on `<html>`.
- **What can be left out.** `hide` takes names from `HIDEABLE` (`src/lib/options.jsx`): `upgrade`, `servers`, `signin`, `domain`, `tokens`,
  `alerts`, `registry`, `account` and `brand`. `SELF_HOSTED_ONLY` is all but `brand`. A hidden page's links are removed and its address
  says *Not available here*. `tokens`, `alerts` and `registry` are hidden because the server answers those only to a signed-in admin, never
  to a token (`requireSessionAdmin`), so with a token they would only fail. A new page that a host cannot use belongs in that list.
- **Sizes.** The dashboard fills its element, which must have a height. Only one `GeassDashboard` can show at a time: the way requests are
  made is a setting of the page.

To try it against a real Geass, build the package, install the tarball in a scratch Vite app that has a TanStack Router route like the
one above, and proxy `/api/clusters/<id>/geass` to a dashboard with `make dashboard-backend`, with a token made in *Settings, Tokens*.

## The website

```bash
cd website
pnpm install
pnpm dev                              # http://localhost:4321
pnpm build && pnpm links && pnpm seo  # what CI runs
```

See [website/README.md](../website/README.md). Every user-visible change belongs in the documentation in the same pull
request.

## Conventions

- Reconciliation is idempotent, re-reads the object before updating it, and sets owner references.
- Status is `metav1.Condition`s, and `metav1.Time` for times.
- Log messages follow the Kubernetes style (capital first letter, no final period, past tense, the object named): the
  `logcheck` linter enforces it.
- The API grows and never shrinks: see [CONTRIBUTING.md](../CONTRIBUTING.md#rules-that-are-easy-to-break) and
  `hack/crd-compat-exceptions.yaml`.
