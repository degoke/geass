# Third-party notices

Geass is licensed under the [Functional Source License, Version 1.1, ALv2 Future License](LICENSE). It is built from, and installs, other software that has its
own licenses. This page says what and where; it is not legal advice.

## Code in the Geass binaries

**Go dependencies.** The installer, the operator and the command line are linked from Go modules (listed in
[`go.mod`](go.mod)). On 2026-10-15 every one of them was under a permissive license: Apache-2.0, BSD-2-Clause,
BSD-3-Clause, MIT or ISC, and none is copyleft. Their license texts are in the module cache after `go mod download`, and
this prints the licenses of what the binaries link:

```bash
go install github.com/google/go-licenses/v2@latest
go-licenses report ./cmd/... --template <(printf '{{range .}}{{.LicenseName}} {{.Name}}\n{{end}}')
```

**Dashboard bundle.** `internal/dashboard/frontend/dist` is built from [`dashboard-ui`](dashboard-ui) with the packages in
its `package.json` and `pnpm-lock.yaml`, almost all MIT, and none copyleft in what is shipped. `pnpm licenses list --prod`
(in `dashboard-ui`) lists them. It also carries three self-hosted variable fonts, all under the
[SIL Open Font License 1.1](https://openfontlicense.org/): **Geist**, **JetBrains Mono** and **Space Grotesk**, packaged by
[Fontsource](https://fontsource.org/). The website serves JetBrains Mono and Space Grotesk the same way.

## Software Geass installs or deploys

These are separate programs, with their own licenses, downloaded at install time or when a feature is turned on. Geass runs
them unmodified as separate processes and does not link to them. Read their licenses before you rely on them, in particular
if you redistribute them:

- K3s (with the components it bundles, such as Traefik), Longhorn, cert-manager, CloudNativePG, KEDA and its HTTP add-on,
  the monitoring stack (kube-prometheus-stack), the log store and shipper (VictoriaLogs and Fluent Bit), the Redis and
  MySQL Helm charts, and the image builder Kaniko;
- the object storage servers, **SeaweedFS** and **Garage**. Garage is licensed under the AGPL-3.0, and Geass runs its
  published image unmodified, only when you choose it for a bucket server;
- the database engines you choose (PostgreSQL, MySQL, Redis, SQLite) and whatever you deploy yourself.

## Trademarks

Geass is the name of this project. Other names are the property of their owners, and their use here only says what Geass
works with.
