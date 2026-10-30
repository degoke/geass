---
title: "Releases and versions"
description: "How Geass numbers its releases, what each release contains, how to check a download, and what a version number tells you about an update."
---

Geass has one version for all of its parts. The installer, the operator, the dashboard, the command line, and its MCP server come from the same tag, and they always ship together. This page tells you these things:

- What a release contains.
- What the release number tells you.
- How to check the files that you download.
- Where to read what changed.

For the procedure that a maintainer uses to make a release, see [`docs/releasing.md`](https://github.com/degoke/geass/blob/main/docs/releasing.md) in the repository.

## What a release contains

Every release on the [Releases page](https://github.com/degoke/geass/releases) has these files:

| File | What it is |
| --- | --- |
| `geass-linux-amd64`, `geass-linux-arm64` | The installer for the server. The installer contains the operator image and manifests, so it downloads nothing more from Geass. `install.sh` downloads and runs the installer. See [Install Geass](/docs/getting-started/install/). |
| `geass-cli-linux-amd64`, `geass-cli-linux-arm64`, `geass-cli-darwin-amd64`, `geass-cli-darwin-arm64`, `geass-cli-windows-amd64.exe` | The command line, which includes the MCP server (`geass mcp`). `install-cli.sh` installs the command line as `geass`. See [Command line](/docs/guides/command-line/). |
| `install.sh`, `install-cli.sh` | The scripts for the one-line installs. |
| `SHA256SUMS` | The checksum of each file in this table. |

The installer and the operator report their release, and the cluster records the release. To see the releases, use these commands:

- `sudo k3s kubectl -n geass-system get geasscluster` shows the cluster release in the `RELEASE` column.
- `geass version` shows the release of the command line and the release of the cluster.

## Version numbers

A release number is `vMAJOR.MINOR.PATCH`, as [Semantic Versioning](https://semver.org) describes. Geass is before version 1.0, so read the version like this:

| Release | What to expect |
| --- | --- |
| A patch, such as `v0.2.1` | Fixes only. You can safely install it when you want. |
| A minor, such as `v0.3.0` | New features, and behavior that might have changed. Read its notes before you update. |
| A candidate, such as `v0.3.0-rc.1` | A release to try before it is final. GitHub marks it as a pre-release, and `latest` never points at a candidate. A candidate comes before the release that it leads to in the version order, so you can update from it to `v0.3.0`. |

Three rules apply to every version. These rules make an update safe:

- **The data a cluster holds is kept.** The custom resources only get new fields. If a release changes the shape of stored data, the new operator migrates the data when it starts. Before the installer changes anything, it makes sure that the new definitions can still read the stored data.
- **A cluster is never downgraded.** The installer does not install a release that is older than the release that the cluster runs. The reason is that the newer release might have stored data that the older release cannot read. If a release has a problem, the fix is a newer release. To go back, restore the etcd snapshot that the installer made before the update. See [Upgrade Geass](/docs/operations/upgrading/#go-back).
- **K3s moves only when you ask.** A Geass update does not upgrade K3s unless you give `--k3s-version`. K3s moves one minor version at a time.

## Which release to install

`latest` is the newest release that is not a pre-release. Use `latest` for a first install. For a cluster that you rely on, pin a release. Then a second run of the installer, or a second server, installs the same release. See [Pin a version](/docs/getting-started/install/#pin-a-version). To update, run the installer of the release that you want on the control plane. The cluster keeps its data, and the installer makes a snapshot first.

Only the latest release gets fixes, including security fixes. If you use an older release, update to the latest release.

## Where to read what changed

The [changelog](https://github.com/degoke/geass/blob/main/CHANGELOG.md) lists the changes in each release. The notes on each GitHub release repeat the changelog section for that version. The changelog states each change that affects an existing cluster, for example a setting with a new default, or a feature that moved. Read the notes of every release between your release and the release that you update to.

## Check what you download

The install scripts check the installer and the command line against `SHA256SUMS`. The scripts do not run a file that does not match. You can also check a file yourself, and check where it was built. Every file has signed build provenance. The provenance is a GitHub attestation of the workflow run and the commit that made the file.

```bash
gh release download v0.1.0 --repo degoke/geass --pattern 'geass-cli-linux-amd64' --pattern SHA256SUMS
sha256sum --check --ignore-missing SHA256SUMS
gh attestation verify geass-cli-linux-amd64 --repo degoke/geass
```

If a file does not verify, do not run it. Report the problem as [SECURITY.md](https://github.com/degoke/geass/blob/main/SECURITY.md) describes.

## Keep the command line in step

The command line connects to the API of the cluster. The resources of that API have the fields of the cluster release. Thus, keep the command line and the cluster on the same release. `geass version` shows both releases, and it tells you when they are different. To update the command line, run `install-cli.sh` again. To pin a release, set `GEASS_VERSION`. See [Command line](/docs/guides/command-line/#install).

## Related

- [Upgrade Geass](/docs/operations/upgrading/): what an update checks, keeps, and does, and how to go back.
- [Install Geass](/docs/getting-started/install/): the installer, its variables, and its log.
- [Security](/docs/operations/security/): how Geass protects what it manages.
