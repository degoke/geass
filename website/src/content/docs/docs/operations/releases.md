---
title: "Releases and versions"
description: "How Geass releases are numbered, what each release contains, how to check a download, and what a version number tells you about updating."
---

Geass is released as one version: the installer, the operator, the dashboard, the command line and its MCP server are built from the same tag and always ship together. This page says what a release contains, what its number tells you, how to check what you download, and where to read what changed. How a maintainer makes a release is in the repository, in [`docs/releasing.md`](https://github.com/degoke/geass/blob/main/docs/releasing.md).

## What a release contains

Every release on the [Releases page](https://github.com/degoke/geass/releases) has these files:

| File | What it is |
| --- | --- |
| `geass-linux-amd64`, `geass-linux-arm64` | The installer for the server. It carries the operator image and manifests, so it fetches nothing else from Geass. `install.sh` downloads and runs it. See [Install Geass](/docs/getting-started/install/). |
| `geass-cli-linux-amd64`, `geass-cli-linux-arm64`, `geass-cli-darwin-amd64`, `geass-cli-darwin-arm64`, `geass-cli-windows-amd64.exe` | The command line, which includes the MCP server (`geass mcp`). `install-cli.sh` installs it as `geass`. See [Command line](/docs/guides/command-line/). |
| `install.sh`, `install-cli.sh` | The scripts behind the one-line installs. |
| `SHA256SUMS` | The checksum of every file above. |

Each release also has the operator image, `ghcr.io/degoke/geass:vMAJOR.MINOR.PATCH`, for `linux/amd64` and `linux/arm64`. The installer carries its own copy, so you need the image only when the control plane of your cluster is hosted elsewhere: see [Hosted control plane](/docs/operations/hosted-control-plane/).

The installer and the operator report the release they are, and the cluster records it: `sudo k3s kubectl -n geass-system get geasscluster` shows it in the `RELEASE` column, and `geass version` shows the command line's release and the cluster's.

## Version numbers

A release is `vMAJOR.MINOR.PATCH`, as [Semantic Versioning](https://semver.org) describes. Geass is before 1.0, so read the version like this:

| Release | What to expect |
| --- | --- |
| A patch, such as `v0.2.1` | Fixes only. Safe to take as soon as you like. |
| A minor, such as `v0.3.0` | New features, and behaviour that may have changed. Read its notes before you update. |
| A candidate, such as `v0.3.0-rc.1` | A release to try before it is final. It is marked as a pre-release on GitHub, and `latest` never points at one. It is ordered before the release it leads to, so you can update from it to `v0.3.0`. |

Three rules hold for every version, and they are what make an update safe:

- **The data a cluster holds is kept.** The custom resources only gain fields. A release that reshapes stored data migrates it when the new operator starts, and the installer checks the new definitions can still read what is stored before it changes anything.
- **A cluster is never downgraded.** The installer refuses to install an older release than the one a cluster runs, because the newer one may have stored data the older one cannot read. If a release has a problem, the fix is a newer release. To go back, restore the etcd snapshot the installer took before the update. See [Upgrade Geass](/docs/operations/upgrading/#go-back).
- **K3s moves only when you ask.** A Geass update does not upgrade K3s unless you pass `--k3s-version`, and K3s moves one minor version at a time.

## Which release to install

`latest` is the newest release that is not a pre-release. It suits a first install. For a cluster you rely on, pin a release, so a re-run or a second server installs the same thing: see [Pin a version](/docs/getting-started/install/#pin-a-version). To update, run the installer of the release you want on the control plane. The cluster keeps its data and takes a snapshot first.

Only the latest release gets fixes, including security fixes. If you are on an older release, update to the latest one.

## Where to read what changed

The [changelog](https://github.com/degoke/geass/blob/main/CHANGELOG.md) lists each release's changes, and the notes on each GitHub release repeat the section for that version. A change that affects an existing cluster, such as a setting that now defaults differently or a feature that moved, is stated there. Read the notes of every release between yours and the one you update to.

## Check what you download

The install scripts check the installer and the command line against `SHA256SUMS` and refuse to run anything that does not match. You can check a file yourself, and also check where it was built. Every file has signed build provenance: a GitHub attestation of the workflow run and the commit that produced it.

```bash
gh release download v0.0.1 --repo degoke/geass --pattern 'geass-cli-linux-amd64' --pattern SHA256SUMS
sha256sum --check --ignore-missing SHA256SUMS
gh attestation verify geass-cli-linux-amd64 --repo degoke/geass
```

If a file does not verify, do not run it, and report it as [SECURITY.md](https://github.com/degoke/geass/blob/main/SECURITY.md) describes.

## Keep the command line in step

The command line talks to the cluster's API, whose resources carry the fields of the cluster's release, so keep the two on the same release. `geass version` shows both and says when they differ. To update the command line, run `install-cli.sh` again: `GEASS_VERSION` pins a release. See [Command line](/docs/guides/command-line/#install).

## Related

- [Upgrade Geass](/docs/operations/upgrading/): what an update checks, keeps and does, and how to go back.
- [Install Geass](/docs/getting-started/install/): the installer, its variables and its log.
- [Security](/docs/operations/security/): how Geass protects what it manages.
