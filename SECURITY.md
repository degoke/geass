# Security policy

Geass runs on servers you own and holds the credentials of the clouds you connect to it, so we take reports of
vulnerabilities seriously. Thank you for taking the time to report one responsibly.

## Reporting a vulnerability

**Do not put the details of a vulnerability in a public issue, pull request or discussion.**

Report it privately through GitHub: open the repository's **Security** tab, choose **Report a vulnerability**, and fill
in the form. Only the maintainers can see it. If that button is not available, use the
[private contact request form](https://github.com/degoke/geass/issues/new?template=private_contact.yml), which asks only which kind of problem it is,
with no details, and a maintainer will reply with a private way to send them.

Please include:

- what the problem is and what an attacker gains (read a secret, run code, cross a project boundary, skip sign-in);
- the Geass release (`geass version`, or `RELEASE` in `kubectl -n geass-system get geasscluster`) and how it was installed;
- the steps to reproduce it, or a proof of concept, and the logs or requests that show it;
- whether it is already public, or you plan to publish it, and when.

We aim to acknowledge a report within five working days, to tell you whether we consider it a vulnerability and how we
plan to fix it, and to keep you informed until it is fixed. We will credit you in the release notes and the advisory if
you want to be named. Please give us a reasonable time to fix a problem before you disclose it; we will agree a date
with you, and 90 days from your report is our default.

## What is covered

Geass's own code and release files:

- the operator and the dashboard that is built into it, including the JSON API under `/api/v1` and its tokens;
- the installer, the install scripts and the release files;
- the command line and its MCP server;
- the custom resources and the permissions Geass asks for.

Problems that are particularly interesting to us: sign-in or token bypass, a token doing more than its role allows
(including an AI agent seeing a tool its token may not use), reading another project's data or network, secrets
appearing in logs, API responses or the dashboard, and anything that lets a tampered download be installed.

## What is not covered

- Vulnerabilities in what Geass installs and does not own: K3s, Kubernetes, Longhorn, cert-manager, CloudNativePG, KEDA,
  SeaweedFS, Garage, Traefik, and the images you deploy. Report those to their projects. If Geass ships a version with a
  known vulnerability, or configures one unsafely, that is ours: tell us.
- The limits [the documentation](https://rungeass.com/docs/operations/security/) already states, such as the control
  plane being a single server, or a person who already has `kubectl` access to the cluster being able to read its Secrets.
- Findings that need an attacker to already hold an admin account, a full access token, or root on a server.
- Missing hardening that the [hardening checklist](https://rungeass.com/docs/operations/security/#hardening-checklist) asks
  you to do yourself, on a server you operate.
- Reports from automated scanners with no demonstrated impact.

## Supported versions

Geass is before 1.0 and moves quickly. Security fixes are made in the latest release only, and shipped as a new release:
update by running the installer of that release on the control plane
([Upgrade Geass](https://rungeass.com/docs/operations/upgrading/)). The [changelog](CHANGELOG.md) marks which
release fixed a vulnerability, under "Security".

## Checking what you download

Every release has a `SHA256SUMS` file, which the install scripts check, and signed build provenance:

```bash
gh attestation verify geass-linux-amd64 --repo degoke/geass
```

If a release file does not verify, do not run it, and report it as above.

## Safe harbor

We will not take legal action against, and will work with, anyone who reports a vulnerability in good faith under this
policy: you test only against Geass you installed yourself, you do not access or change data that is not yours, you do
not degrade other people's services, and you give us the chance to fix it before you disclose it.
