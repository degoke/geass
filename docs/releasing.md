# Releasing Geass

How versions are numbered, what a release contains, and how a maintainer cuts one. Users read the short version in
[Releases and versions](https://rungeass.com/docs/operations/releases/).

## One release, one version

The installer, the operator, the command line and its MCP server, and the install scripts are one release with one
version, tagged `vMAJOR.MINOR.PATCH` on `main`. They are not versioned apart because they depend on each other:

- the installer carries the operator it installs, and the operator reports the release it runs on the `GeassCluster`
  (`status.release`), which is how an update refuses a downgrade;
- the command line talks to the cluster's API, whose resources carry that release's fields, so `geass version` compares
  the two and warns when they differ;
- the CRDs, the migrations and the operator's permissions move together.

The version is stamped in at build time from the tag: `pkg/release.Version` for the installer and the operator, and
`main.version` for the command line. Both come from `GEASS_VERSION` (`CLI_VERSION` defaults to it). A build that is not
from a tag reports `git describe` output (`v0.2.0-3-g1a2b3c4`) or `dev`, and `pkg/release` orders those against real
releases.

### What a number means

[Semantic Versioning](https://semver.org), with the usual caveat that Geass is before 1.0.

| Change | Version |
| --- | --- |
| Bug fixes and documentation only; nothing an existing cluster has to be told about | patch (`0.2.1`) |
| New features, and changes to behaviour, while the version is `0.x` | minor (`0.3.0`) |
| After 1.0: a breaking change to the API, the CLI or the installer's options | major |
| A candidate for a release, for people who want to try it first | `-rc.1` after the version (`v0.3.0-rc.1`) |

Three rules hold across every version, and they are what make an update safe:

1. **The API only grows.** `v1alpha1` gains fields and never loses or tightens one. `make crd-compat` checks the CRDs
   against the baseline release, and a reshaped field gets a migration in `pkg/migrations`. See
   [CONTRIBUTING.md](../CONTRIBUTING.md#rules-that-are-easy-to-break).
2. **A cluster is never downgraded.** The installer refuses to install an older release than the one a cluster runs,
   because the newer one may have stored data the older cannot read. A bad release is therefore fixed by a newer one, not
   by taking the old tag back.
3. **K3s moves one minor version at a time**, and only when asked (`--k3s-version`).

A published tag is never moved, deleted or reused.

## What a release contains

The [release workflow](../.github/workflows/release.yml) builds these into the GitHub release:

| File | What it is |
| --- | --- |
| `geass-linux-amd64`, `geass-linux-arm64` | The installer. It embeds the operator image and manifests, so it fetches nothing else from Geass. `install.sh` downloads it. |
| `geass-cli-linux-amd64`, `geass-cli-linux-arm64`, `geass-cli-darwin-amd64`, `geass-cli-darwin-arm64`, `geass-cli-windows-amd64.exe` | The command line, with the MCP server (`geass mcp`). `install-cli.sh` installs it as `geass`. |
| `install.sh`, `install-cli.sh` | The scripts behind the one-line installs. They check what they download against `SHA256SUMS`. |
| `SHA256SUMS` | The checksum of every file above. |

Every file also has signed build provenance (a GitHub attestation of the workflow run and commit that built it), checked
with `gh attestation verify <file> --repo degoke/geass`. The release notes are the version's section of
[CHANGELOG.md](../CHANGELOG.md).

The same run also pushes the operator image to `ghcr.io/degoke/geass:vMAJOR.MINOR.PATCH` (the version only, no `latest`)
for `linux/amd64` and `linux/arm64`; a pre-release gets one too. The installer carries its own copy, so the image is for
a cluster whose control plane is hosted elsewhere, which pulls it (`config/external-control-plane`). It is not a file of
the GitHub release and is not in `SHA256SUMS`. A manual run builds it and pushes nothing, so the first real tag is the
first time the push, and the package's visibility, are exercised: step 6 checks that it can be pulled without a login,
and if it cannot, make the package public in its settings on GitHub.

`dist/install.yaml` is kept current by CI for people who read or customise the manifests. It names a placeholder image
and is not published: the supported way to install is the installer.

## Cutting a release

You need push access to `main` and tags. Nothing in these scripts pushes for you.

1. **Make sure `main` is green**: CI, and the end-to-end tests, on the commit you mean to release. The `CHANGELOG.md`
   **Unreleased** section should already describe the release, because each pull request adds its own lines; read it and
   tidy it.
2. **Rehearse, if the pipeline changed or it is the first release.** In GitHub: Actions, Release, Run workflow, with a
   version such as `v0.0.0-dryrun`. It builds every file and keeps them as a workflow artifact for 14 days, attests them,
   and runs the publishing step as a draft release that it deletes again, so what a real release does is exercised and
   nothing stays public (the attestations remain, unreferenced). Locally, `make release-assets GEASS_VERSION=v0.0.0-dryrun` builds the same files into `bin/` (it needs Docker).
3. **Write the changelog section** and merge it:

   ```bash
   git switch -c release-v0.2.0
   hack/release.sh prepare v0.2.0     # moves Unreleased under "## [0.2.0] - <today>" and fixes the compare links
   git commit -am "Release v0.2.0" && git push -u origin release-v0.2.0   # open a pull request, merge it
   ```

4. **Tag the tip of `main`**, then publish the tag:

   ```bash
   git switch main && git pull
   hack/release.sh tag v0.2.0         # checks the tree, that you are on origin/main and the changelog; creates an annotated tag
   git push origin v0.2.0
   ```

   For a candidate, tag `v0.2.0-rc.1` the same way. It needs no changelog section, but **Unreleased** must not be empty
   (those are its notes). It is published as a pre-release, and is never what `releases/latest` points to.

5. **Watch the Release workflow.** It checks the tag is a version, is on `main` and has release notes, and that CI has
   passed on the commit (the `Lint`, `Test`, `Build` and end-to-end `Run on Ubuntu` checks), so tag after CI on `main`
   has finished or run the workflow again. Then it builds the files and publishes. Publishing creates the release, so a
   failure before the last step leaves nothing public. Tags pushed close together each get their own run.
6. **Check the release.** From a machine that is not your build machine:

   ```bash
   gh release download v0.2.0 --repo degoke/geass --dir /tmp/geass-release && cd /tmp/geass-release
   sha256sum --check SHA256SUMS
   gh attestation verify geass-linux-amd64 --repo degoke/geass
   chmod +x geass-cli-linux-amd64 && ./geass-cli-linux-amd64 version        # geass v0.2.0
   docker logout ghcr.io; docker pull ghcr.io/degoke/geass:v0.2.0          # the operator image, with no login
   ```

   Then install it on a real server: `curl -sfL .../releases/latest/download/install.sh | sudo sh` on a fresh one, and
   the installer of the new release on a cluster that runs the previous one (the update path is what most people will
   hit).
7. **Move the CRD baseline** so the next release is held to what this one shipped, in a pull request:

   ```bash
   make crd-baseline REF=v0.2.0      # writes the tag into hack/crd-baseline-ref
   ```

8. **Tell people**, and if the release changes what an existing cluster does, say it in the notes, not just the
   changelog.

## When something goes wrong

- **The workflow fails before "Create the GitHub release".** Nothing was published. Delete the tag
  (`git push origin :refs/tags/v0.2.0 && git tag -d v0.2.0`), fix `main`, and tag again.
- **A release is published and is bad.** Do not delete it if anyone could have installed it: a cluster that updated cannot
  go back. Edit the release notes to say what is wrong, publish a fixed patch release (`v0.2.1`) as soon as you can, and
  say in the changelog which release it replaces. Mark the bad one as a pre-release in the GitHub UI so `latest` moves off it.
- **A file fails `gh attestation verify` or its checksum.** Treat it as a compromise: stop, do not publish, and follow
  [SECURITY.md](../SECURITY.md).
- **`tag` says `[Unreleased]` has notes.** A change merged after the release pull request. Move those lines under the
  version's section in `CHANGELOG.md` in a pull request, merge it, and tag again.
- **You tagged the wrong commit but have not pushed the tag.** `git tag -d v0.2.0` and redo step 4.

## One-time repository setup

These are settings on GitHub rather than files, so a pull request cannot make them. Do them once, as an admin of
`degoke/geass`:

- **Private vulnerability reporting**, which [SECURITY.md](../SECURITY.md) tells reporters to use:
  `gh api --method PUT repos/degoke/geass/private-vulnerability-reporting`
- **Dependabot alerts and security updates**:
  `gh api --method PUT repos/degoke/geass/vulnerability-alerts` and
  `gh api --method PUT repos/degoke/geass/automated-security-fixes`
- **A ruleset on `main`** (Settings, Rules, Rulesets): require a pull request and these status checks: `Lint`,
  `CRD compatibility and install bundle`, `Website`, `Test`, `Build`, `Command line for every system` and
  `Install and release scripts`; block force pushes and deletion.
- **A ruleset on tags matching `v*`**: restrict creation, update and deletion to maintainers, so that a release tag cannot
  be moved or removed.
- **Repository details**: the website `https://rungeass.com`, and topics.
- **Actions**: leave the default workflow permission at read only; the workflows ask for what they need per job.
