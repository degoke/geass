---
title: "Connect GitHub"
description: "Connect GitHub to Geass: register the GitHub App, link a project, test the webhook, and rotate or remove credentials so pushes and pull requests deploy."
sidebar:
  label: GitHub
---

Use GitHub when you deploy from a repository: it lets Geass build on a push and create pull request environments.
The setup has two parts: one GitHub App for the whole cluster, and one install per project. The day-to-day use is in
[GitHub and builds](/docs/guides/github-and-builds/) and
[Pull request environments](/docs/guides/pull-request-environments/).

## Before you start

Set a public HTTPS dashboard address first (**Settings** → **Domain**). GitHub calls back to it, and Geass refuses a
GitHub App whose public URL is not HTTPS ("GitHub App public URL must use HTTPS"). Until then the card offers
**Configure domain** instead of the app form.

## Register the GitHub App

1. Open **Settings** → **Connectors** and choose **Configure** on the **GitHub** card.
2. Choose **Create GitHub App on GitHub**. GitHub shows a prefilled app and you confirm it. Its name comes from the dashboard
   host with the `geass.` prefix dropped and dots turned into hyphens: `Geass-example-com` for `geass.example.com`.
3. GitHub sends you back and Geass stores the credentials. The card shows **Connected**.

The app Geass asks GitHub for is private, with permissions **Contents** read, **Metadata** read and **Pull requests**
read, and events `push` and `pull_request`. The webhook URL is `<dashboard URL>/webhooks/github`, the callback and
setup URLs are `<dashboard URL>/github/callback`.

If you already have a GitHub App, use **Manual credentials** instead: enter **App ID**, **Client ID**, **App slug**,
**Client secret**, **Webhook secret** and **Private key (PEM)**, then choose **Save GitHub App**. The key must be an RSA
private key in PEM form. You can also supply the same values to the operator as environment variables (see
[Configuration](/docs/reference/configuration/)); the dashboard form and the stored Secret take precedence.

## Link a project

In a project, open **Settings** → **Integrations** and choose **Connect to GitHub** (or add a GitHub repository
service, which does the same). GitHub asks which account and repositories the app may use, then returns to Geass.
Geass creates the connection `<project id>-github` and the Secret `<project id>-github-token` with the installation
token, the installation ID and the account. The token is short-lived: Geass asks GitHub for a fresh one with the app's
private key when it needs one. **Disconnect** removes the project's link only; the connection object and Secret stay
until you delete them.

## Test, rotate and remove

- **Test GitHub App** compares the webhook URL stored in the GitHub App with `<dashboard URL>/webhooks/github`.
  It fails with `GitHub webhook URL is X, expected Y` when the dashboard address changed.
- To rotate the **Client secret**, **Webhook secret** or **Private key (PEM)**, generate the new value in GitHub and
  save it with **Manual credentials**. A field you leave empty keeps its stored value. Update the webhook secret in
  GitHub too, or deliveries fail verification.
- **Remove GitHub App** needs the confirmation text `remove-github`. It clears the platform's reference and deletes the
  Secret `platform-github-app`. The app and its installations stay in GitHub, so uninstall them there. Projects keep
  their links but cannot refresh tokens until you register an app again.

The Secret `platform-github-app` holds `app_id`, `client_id`, `client_secret`, `slug`, `private_key` and
`webhook_secret`.

## How webhooks are verified

GitHub posts to `/webhooks/github`, which is reachable without a login because GitHub cannot sign in. Geass checks the
`X-Hub-Signature-256` header (HMAC-SHA256 of the body with the webhook secret) and rejects a mismatch with `401`. It
answers `503` if no webhook secret is configured. It handles `ping`, `push` and `pull_request` and ignores other events.

## What happens under the hood

- The GitHub App is stored on the platform configuration, named `platform`, with its credentials in the Secret
  `platform-github-app`. See [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/).
- Each project you link has a connection, `<project id>-github`, with its installation token in the Secret
  `<project id>-github-token`. See [GeassGitHubConnection](/docs/reference/resources/geass-github-connection/).

```bash title="Inspect the GitHub connection"
kubectl -n geass-system get geassplatformconfig platform -o yaml
kubectl -n geass-system get secret platform-github-app
```

## Troubleshooting

**GitHub says "dashboard URL is not configured" or "GitHub App is not configured".** Finish **Settings** → **Domain** first,
then register the app.

**GitHub webhooks return 401.** The webhook secret in GitHub differs from the one Geass stores. Save the matching value with
**Manual credentials**.

## Related

- [Cloud connectors](/docs/guides/connectors/)
- [GitHub and builds](/docs/guides/github-and-builds/)
- [Pull-request environments](/docs/guides/pull-request-environments/)
- [Domains and TLS](/docs/guides/domains-and-tls/)
- [GeassGitHubConnection](/docs/reference/resources/geass-github-connection/)
- [Security](/docs/operations/security/)
