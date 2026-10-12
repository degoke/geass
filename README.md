# Geass

> Managed cloud experience on your VPS or bare-metal — powered by K3s.

[![License: ELv2](https://img.shields.io/badge/License-Elastic%202.0-blue.svg)](LICENSE)
[![Release](https://img.shields.io/github/v/release/degoke/geass)](https://github.com/degoke/geass/releases/latest)

Geass is a source-available platform that brings the managed public cloud experience to your own VPS or bare-metal hardware.

Powered by [K3s](https://k3s.io/), Geass turns a single affordable server—or a cluster of machines—into a cohesive, horizontally scalable environment. It abstracts away raw infrastructure management so you can deploy applications, provision databases, and manage networking as smoothly as you would on AWS or GCP, on infrastructure you control.

## Why Geass?

Managed clouds and Kubernetes services are powerful, but expensive. Running on VPS providers is cheaper, but manual setup, scaling, and routing across machines is tedious and error-prone. And the full application lifecycle—databases, storage, DNS, security—usually means juggling many disconnected tools.

Geass bridges that gap: a unified control plane for your services, with the developer velocity of a major cloud provider and without vendor lock-in or premium pricing.

## What you get

- **Horizontal scaling** — distribute workloads across multiple machines without hand-configuring load balancers
- **Unified service management** — deploy and monitor applications, databases, and storage from one place
- **Backups and restore** — Postgres, MySQL and SQLite databases are backed up to a bucket in your own AWS account (Postgres continuously, so it can be restored to any recent moment; MySQL and SQLite daily); restore as a new database, and keep deleted databases restorable for 7 days
- **Lightweight orchestration** — Kubernetes-native, built on K3s, without the overhead of a full cluster install

Geass is aimed at teams that need sovereign or on-premise deployments, self-hosted infrastructure, or cost-effective microservices on a fleet of VPS instances.

## Getting started

### Requirements

- A fresh Linux VPS (amd64 or arm64)
- At least 2 GB RAM and 10 GB free disk on `/`
- Root access (`sudo`)

### Install

SSH into your VPS and run:

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo sh
```

That downloads the Geass installer binary for your architecture and runs it. No build step and no extra files to copy—the package includes everything needed to bootstrap the cluster.

Pin a specific version:

```bash
curl -sfL https://github.com/degoke/geass/releases/download/v0.1.0/install.sh | sudo env GEASS_VERSION=v0.1.0 sh
```

The installer will:

- Verify system requirements and open required firewall ports when a host firewall is active
- Install K3s in server mode
- Deploy the Geass operator
- Apply the initial cluster configuration

Installation logs are written to disk; the path is printed when the installer starts.

### HTTPS certificates

Geass gets certificates for the dashboard and for your services from [Let's Encrypt](https://letsencrypt.org/), through
cert-manager. Let's Encrypt registers an account for the cluster and wants a contact address for it, which only you can
give, so pass one to the installer as `GEASS_ACME_EMAIL`:

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo env GEASS_ACME_EMAIL=you@yourdomain.com sh
```

Use a real address: Let's Encrypt refuses placeholder domains such as `example.com`. The installer checks that the
value looks like an email address and stops before changing anything if it does not.

With an address set, Geass creates a cert-manager `ClusterIssuer` named `letsencrypt-prod` as soon as cert-manager is
ready (ACME over HTTP-01, solved through the Traefik ingress that K3s ships). Every ingress Geass creates asks for its
certificate from that issuer. Let's Encrypt must be able to reach your server on **TCP 80** to validate a domain, and
visitors reach your services on **TCP 443**; the installer does not open these, so allow them in your firewall and
your provider's security group.

Without an address the cluster still installs and runs, but no issuer is created, so **no HTTPS certificate is issued**
and the dashboard's domain stays pending. Geass says so rather than failing: the `GeassCluster` has a
`ClusterIssuerReady` condition that names the problem, and the same message is added to the dashboard domain's status
(**Settings → Domain** and the `DashboardDomainReady` condition) while its HTTPS is not reachable.

```bash
kubectl -n geass-system get geasscluster default -o jsonpath='{.status.conditions[?(@.type=="ClusterIssuerReady")].message}'
```

To set or change the address on a running cluster (including one installed before this setting existed), edit the
cluster; Geass creates or updates the issuer within seconds:

```bash
kubectl -n geass-system patch geasscluster default --type merge \
  -p '{"spec":{"addons":{"certManager":{"acmeEmail":"you@yourdomain.com"}}}}'
```

Two things Geass deliberately leaves alone: a `letsencrypt-prod` issuer that you created yourself is never overwritten
(it is reported as `ExternallyManaged`), and removing the address later does not delete the issuer that Geass made,
because certificates issued through it still renew through it. If you run your own cert-manager, turn the add-on off
(`spec.addons.certManager.enabled: false`) and Geass manages no issuer at all.

### Nodes at more than one provider (experimental)

By default the cluster's nodes share a private network. To build a cluster whose nodes sit at different
providers and reach each other over the internet, give the installer this machine's public address:

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install.sh | sudo env GEASS_MULTI_SITE=1 GEASS_NODE_EXTERNAL_IP=203.0.113.10 sh
```

The cluster then uses an encrypted WireGuard overlay (UDP 51820, instead of VXLAN's 8472) and every node
advertises its public IP. Keep the control plane at one site and use a kernel with WireGuard (built in from
Linux 5.6). Between the nodes' public addresses, and in each provider's security group, open:

- **TCP 6443** to the control plane, from every node **including the control plane itself**: it reaches its own
  API through its public address, and without this the operator and most system services cannot start.
- **UDP 51820** between every pair of nodes.
- **TCP 10250** between every pair of nodes, so container metrics are collected from every node.
- **TCP 22** to each new worker, from the control plane, because Geass joins workers over SSH.

Workers are joined over SSH as `root` or as any user with passwordless `sudo` (such as `ubuntu`).

This has been run on real servers: a four-node cluster across three availability zones, joined over their public
addresses, with traffic carried by the encrypted overlay. It has not yet been run across two different
providers. Host-level node charts (node CPU and memory) are collected only for the node that runs monitoring,
because they are published on private addresses; container metrics, which feed the service and project charts,
are collected from every node.

Tag nodes with a region, a site and tags under **Settings → Usage → Nodes**; projects then choose a region.

### Project isolation

Each project is private by default: only its own services (in every environment), Geass itself, and visitors who
arrive through your domains can reach them. Services in other projects cannot, and neither can anything else in
the cluster. A project's services can still reach each other, other services and the internet.

To let one project call another (a front end calling an internal API, say), open the receiving project's
**Settings → Network** and choose which projects may reach it. The same page can open a project to every project
in the cluster. The change applies instantly and needs no redeploy.

Two things hold whatever you choose: services cannot read the cloud provider's credentials for the machine they
run on, and no project can read another's metrics or logs. Projects that existed before isolation was added
become isolated when Geass is updated, so list any project that relied on reaching another before updating.

### Compliance: nodes that only some projects may use

Some data has to stay on machines you can account for. Tag the nodes that qualify, mark the tag as a
**compliance tag** (under **Settings → Usage → Nodes**, or `geass nodes tag`), and set it as a required tag on
the project (**Project settings → Compliance**, or `geass compliance set`). From then on everything the project
runs, including its databases, buckets, builds and the jobs that back them up, stays on nodes that carry the
tag, and those nodes take no other project's work, including the replicas of other projects' volumes. Pods created
outside Geass in the project's namespaces are refused by an admission policy (Kubernetes 1.30 or later).

What this does not do: move data that is already somewhere else (a project with databases has to restore them into
new ones first), protect against a compromised node, or cover databases and storage outside the cluster. The logs of
a project that requires tags are not sent to the shared log store, so their history is not kept.

### Services that sleep when idle

A service can stop when nobody is using it and start again on the next request, like a function on a serverless platform. It works for every kind of service (an image, a Dockerfile or GitHub build, or a function), costs nothing while asleep, and the first request after a sleep waits a few seconds while the service starts.

Turn the add-on on once for the cluster (it installs [KEDA](https://keda.sh) and its HTTP add-on, and is never uninstalled automatically):

```yaml
apiVersion: geass.geass.dev/v1alpha1
kind: GeassCluster
spec:
  addons:
    serverless:
      enabled: true
```

Then choose it per service in its Scale settings, or from the command line:

```bash
geass scale web --sleep-after 10m   # stop after 10 minutes without requests
geass scale web --always-on         # go back to always running
```

A service needs a public domain (a request to it is what wakes it) and cannot also use CPU autoscaling. Each new version of a sleeping service is first kept running until a copy of it has started, so a version that cannot start (a bad image, a crash on boot) fails the deploy with the reason, instead of being found out by the first visitor; only then may it sleep. A service that is woken later and cannot start is reported failed too, rather than staying in "waking". A service that cannot sleep keeps running as before, and its `ScaleToZero` condition says why. Pull request environments sleep by default. Traefik has to allow external-name services: Geass sets this up on a stock k3s, and logs what to add if you have your own Traefik configuration.

### Event functions and schedules

A function is a service made from code you give Geass. It has two modes. In **http** mode (the default) your code is a program that serves HTTP on `$PORT`. In **event** mode your code is only a handler, and Geass serves HTTP for it, the way a function on AWS Lambda works:

```js
export async function handler(event) {
  // event: { method, path, query, headers, body, trigger }
  return { message: "Hello", path: event.path };
}
```

Whatever the handler returns is the response: nothing is a 204, text is text, an object with a numeric `statusCode` (and optional `headers` and `body`) is a response of that status, and anything else is sent as JSON. A handler that throws answers 500 and the error is logged. In Python it is `def handler(event): ...`. `event.body` is parsed JSON when the request said it was JSON, and `event.trigger` is `cron` for a call from one of the function's own schedules and `http` for anything else. A schedule proves itself with a secret token (the `GEASS_TRIGGER_TOKEN` environment variable of the function, sent by the schedule in the `x-geass-trigger-token` header and checked for you in event mode), so a visitor cannot pretend to be one; `event.triggerName` says which. A function in http mode that wants to check can compare that header to the variable itself.

A function can be called on a schedule. Each trigger is a cron expression in UTC, and each run is a `POST` to the function, so it works in either mode and wakes a function that is asleep:

```bash
geass apps create report -p shop -e production --function report.mjs --runtime node --mode event --deploy
geass functions triggers add report nightly "0 3 * * *"
geass functions triggers add report sync "*/15 * * * *" --path /sync --payload '{"full":true}'
geass deploy report
geass functions invoke report --path /sync
```

Schedules start with the next deploy. The runs use the same nodes as the project's other jobs, so a project that has to stay on certain nodes keeps its schedules there too.

## Command line, API and AI agents

Install the command line, then sign in. Nothing secret is typed: it shows a code, and you approve it in the dashboard.

```bash
curl -sfL https://github.com/degoke/geass/releases/latest/download/install-cli.sh | sh
geass login --url https://geass.example.com
```

```bash
geass apps list -p shop -e production
geass deploy web --wait              # exits 5, saying why, if the build or rollout fails
geass logs web -f --level error
geass env set web LOG_LEVEL=debug --deploy --wait
geass status -p shop -e production
```

Every command takes `--json`, uses distinct exit codes (3 not found, 4 not allowed, 5 deploy failed, 6 timed out), and
asks before deleting unless you pass `--yes`. For scripts, set `GEASS_URL` and `GEASS_TOKEN` instead of signing in.

Everything the command line does is the JSON API under `/api/v1` (`GET /api/v1/openapi.json` describes it). Send
`Authorization: Bearer <token>`. Tokens come from the dashboard (**Settings → Tokens**) or from `geass login`:

| Token | Can |
|---|---|
| Full access | everything the dashboard can do |
| Read only, with logs (`observer`) | read everything, including logs; change nothing |
| Read only | read resources, but not logs |
| Project token | deploy, scale, change variables and read logs for one project environment only |

To let an AI agent deploy and monitor services, run the MCP server with the same login:

```bash
claude mcp add geass -- geass mcp          # or: geass mcp --listen 127.0.0.1:8765
```

The agent acts as your token and is only shown the tools it may use: a read-only token cannot even see a tool that
deploys. `--read-only` limits it further, deleting needs `--allow-delete`, and secret values and database passwords
are never returned by any tool.

## License

Copyright 2026 Adegoke Adewoye.

Geass is licensed under the [Elastic License 2.0](LICENSE). You may use, modify, and deploy Geass for yourself or your organization. You may not offer Geass—or a substantially similar product—to third parties as a hosted or managed service.

See [LICENSE](LICENSE) for the full terms.
