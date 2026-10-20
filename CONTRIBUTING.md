# Contributing to Geass

Thank you for wanting to help. Bug reports, documentation fixes, ideas and code are all welcome. This page says how to
propose a change so it can be reviewed and merged quickly. By taking part you agree to the
[code of conduct](CODE_OF_CONDUCT.md).

## Ways to help

- **Report a bug.** Use the [bug report form](https://github.com/degoke/geass/issues/new?template=bug_report.yml). The
  most useful reports say the Geass release, how it was installed, what you expected and what happened, and include the
  operator log (`sudo k3s kubectl -n geass-system logs deploy/geass-controller-manager -c manager`). Remove secrets first.
- **Report a security problem.** Not here: follow [SECURITY.md](SECURITY.md).
- **Suggest a feature.** Use the [feature request form](https://github.com/degoke/geass/issues/new?template=feature_request.yml)
  and describe the problem before the solution. For anything bigger than a small change, please open an issue and agree
  the approach before writing code: Geass keeps an upgrade path for the data clusters already hold, so some designs that
  look simple are not possible, and we would rather say so before you spend the time.
- **Improve the documentation.** The user documentation is the Astro site in [`website/`](website), one Markdown file
  per page under `website/src/content/docs/docs/`. Contributor documentation is in [`docs/`](docs).
- **Fix something.** Issues labelled `good first issue` are a place to start.

## Setting up

[docs/development.md](docs/development.md) covers the tools you need, the repository layout, how to build and run Geass
on your machine and how each kind of test works. In short:

```bash
git clone https://github.com/degoke/geass && cd geass
make build-cli     # the command line, needs only Go
make test          # unit tests against a real API server; downloads its binaries the first time
```

## Making a change

1. Fork the repository and create a branch from `main`.
2. Make the change, with a test that fails without it. Behaviour that only a live cluster shows is tested in
   `test/k3sintegration` and described in [docs/development.md](docs/development.md#tests).
3. Run what CI runs: `make lint-fix`, then `make test`. If you touched anything under `api/` or a `+kubebuilder` marker,
   also `make manifests generate build-installer crd-compat`, and commit the files they change.
4. Update the documentation the change makes wrong, in the same pull request. If it changes what a user sees, add a line
   under **Unreleased** in [CHANGELOG.md](CHANGELOG.md).
5. Open a pull request against `main`. Say what changed and why, and how you tested it. Keep it to one change: a small
   pull request is reviewed sooner than a large one.

Commit messages are a short imperative sentence that says what changed ("Refuse a downgrade before the snapshot", not
"fixes"), with the reason in the body when it is not obvious.

## Rules that are easy to break

These come from how the project is built, and CI checks most of them.

- **API changes only grow.** A cluster that is updated keeps its stored objects, and the API server drops a field a CRD
  no longer has. In `v1alpha1`, do not remove or rename a field, change a type, add a `required` field (give it a
  `+kubebuilder:default`, or make it `+optional`), or tighten a bound or an enum. `make crd-compat` compares the CRDs with
  the last release and CI fails on a break. To reshape stored data, add the new field beside the old one and register a
  migration in `pkg/migrations` (idempotent, appended, never edited).
- **Do not edit generated files**: `config/crd/bases/*`, `config/rbac/role.yaml`, `zz_generated.*.go`, `PROJECT`.
  Change the types or the markers and run `make manifests generate`. Keep `dist/install.yaml` current with
  `make build-installer`: CI fails when it is stale.
- **Keep the `// +kubebuilder:scaffold:*` markers**, and scaffold new APIs and webhooks with `kubebuilder create`, not by
  hand. [AGENTS.md](AGENTS.md) has the details; it is written for AI coding agents and is a fair summary for people too.
- **Reconciliation is idempotent**: safe to run twice, re-reading the object before an update, with owner references for
  what it creates.
- **Log messages** start with a capital letter, do not end with a period, name the object (`"Created Deployment"`) and
  pass balanced key and value pairs. The linter checks this.
- **Secrets are never logged or returned.** Secret values and database passwords are not in any API response, MCP tool
  result, log line or event.
- **Say what is true.** The documentation states what the code does today, with its limits. A claim that has not been run
  on a real cluster says so.

## Documentation changes

```bash
cd website
pnpm install
pnpm dev        # http://localhost:4321
pnpm build && pnpm links && pnpm seo   # what CI runs
```

Write plainly, in the present tense, and give commands that can be pasted. Link to other pages with absolute paths such
as `/docs/guides/functions/`; `pnpm links` fails on a broken link or heading anchor.

## Licensing of contributions

Geass is licensed under the [Apache License 2.0](LICENSE). When you submit a contribution you confirm that you wrote it
or have the right to submit it, and, as section 5 of the license says, you license it under the Apache License 2.0
too. If your employer has rights to what you write, make sure they agree.

## Review

A maintainer reviews pull requests when they can, usually within a few days. We may ask for changes, or decline a change
that does not fit the project, and we will say why. A pull request that CI rejects is not reviewed until CI passes.
