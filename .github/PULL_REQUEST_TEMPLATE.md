## What changed and why

<!-- The problem, and what this changes. Link the issue it closes: "Closes #123". -->

## How it was tested

<!-- Tests added or run. Behaviour that only a live cluster shows: what you ran it on. -->

## Checklist

- [ ] `make lint-fix` and `make test` pass
- [ ] Changed `api/` types or `+kubebuilder` markers: ran `make manifests generate build-installer crd-compat`, and the change only adds (nothing removed, renamed, retyped or tightened)
- [ ] Documentation in `website/` updated for what a user will see
- [ ] User-visible change: a line under **Unreleased** in `CHANGELOG.md`
- [ ] No secrets, tokens or private addresses in the change, the tests or the logs
