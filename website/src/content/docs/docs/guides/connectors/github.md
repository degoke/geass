---
title: "Connect GitHub"
description: "Connect GitHub to Geass: register the GitHub App and its permissions, link a project, test the webhook and permissions, and rotate or remove credentials."
sidebar:
  label: GitHub
---

Use GitHub when you deploy from a repository: it lets Geass build on a push, create pull request environments, and
show each build and deploy on GitHub as a commit status, a deployment and a pull request comment.
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

The app Geass asks GitHub for is private, with these permissions and the events `push` and `pull_request`:

| Permission | Level | Used for |
| --- | --- | --- |
| **Metadata** | Read | Required of every app. |
| **Contents** | Read | Cloning repositories to build them. |
| **Checks** | Read | **Wait for CI** reads a commit's check runs. |
| **Pull requests** | Read and write | Pull request environments, and the comment Geass keeps on each pull request. |
| **Commit statuses** | Read and write | The `geass/build/<service name>` status on each commit that is built. |
| **Deployments** | Read and write | A GitHub Deployment for each deploy. |

The write permissions are for reporting back to GitHub: see
[Reporting back to GitHub](/docs/guides/github-and-builds/#reporting-back-to-github). The webhook URL is
`<dashboard URL>/webhooks/github`, the callback URL is `<dashboard URL>/github/callback`. The app asks the person who installs it to authorize it as well (**Request user authorization (OAuth) during installation**), so that GitHub sends back a code that shows who came back: GitHub then redirects to the callback URL and no setup URL is used.

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

### Several organizations on one app

With [organizations](/docs/guides/organizations-and-workspaces/#github) on, a person of an organization who connects a project has to show that GitHub lists the installation among the ones their own account can use, and an installation one organization's project uses cannot be connected to another's. For that the app has to ask for the person's authorization when it is installed. An app made by Geass does. For one you registered yourself, turn on **Request user authorization (OAuth) during installation** in its settings on GitHub, and keep the **Callback URL** as `<dashboard URL>/github/callback`; without it an organization's person is told that GitHub did not say who is connecting. A platform administrator is not asked, and nothing changes while organizations are off.

## Test, rotate and remove

- **Test GitHub App** compares the webhook URL stored in the GitHub App with `<dashboard URL>/webhooks/github`.
  It fails with `GitHub webhook URL is X, expected Y` when the dashboard address changed. When the webhook is right it
  also reads the permissions GitHub has granted the app, and warns when any of the ones in the table above is missing or
  too low: it lists them, says what each is for, and says how to approve them. An app registered by an earlier Geass
  lacks Commit statuses and Deployments, and has Pull requests at read only; it builds and deploys as before and reports
  nothing to GitHub until the owner approves them. See
  [Give an existing GitHub App the new permissions](/docs/guides/github-and-builds/#give-an-existing-github-app-the-new-permissions).
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
It puts GitHub's delivery ID (`X-GitHub-Delivery`) in the answer and in its log lines; see
[Webhooks](/docs/guides/github-and-builds/#from-github-to-geass).

## GitHub through a provider

A provider that runs Geass for many clusters, such as rungeass, can run one GitHub App for all of them in place of an App per
cluster. The cluster then never holds the App's private key and registers no App: the provider receives GitHub's events,
finds the cluster whose installation each one is, and forwards it; and it mints each installation's access token with the
App's key and gives it to the cluster, renewing it before it expires. A self-hosted cluster has none of this until a provider
sets it up, and a cluster's own App, if it has one, keeps working beside it.

The provider sets it up with the [API](/docs/reference/rest-api/#github-an-app-a-provider-runs), as an administrator of the
platform. An organization's people cannot reach any of it.

1. `PUT /api/v1/github/connections/<name>` makes the connection of the provider's App, with the provider's name, the App's
   slug, where a person installs it, and a webhook secret that only this cluster and the provider know. Geass keeps the
   secret in a Secret and never shows it again.
2. `PUT /api/v1/github/connections/<name>/installations/<id>` records an installation of the App as this cluster's: the GitHub
   account it is on, and the [organization](/docs/guides/organizations-and-workspaces/) whose projects may connect to it (none,
   for a cluster without organizations).
3. `PUT /api/v1/github/connections/<name>/installations/<id>/token` gives Geass the installation's token and when it expires.
   Geass uses it for everything it does on GitHub for that installation: listing repositories, cloning, resolving a branch,
   reading check runs, and the statuses, deployments and comments it reports.
4. The provider forwards GitHub's events to `/webhooks/github/forwarded/<name>`, signed with the webhook secret, and Geass does
   with them exactly what it does with a webhook from GitHub. See
   [Forwarded GitHub events](/docs/reference/rest-api/#forwarded-github-events) for the signature.

While a provider's App is set up, **Settings** → **Connectors** → **GitHub** shows it in place of the steps above: the
provider, the GitHub accounts it is installed on, how many projects use each, and whether Geass holds a token for each and until
when. Creating an App on GitHub, **Manual credentials** and **Test GitHub App** are refused with
`GitHub is provided to this cluster by <provider>`. **Remove GitHub App** still removes an App of the cluster's own.

**Connect to GitHub** on a project connects it to the installation its organization has. With several, it says which there
are, and the API takes the one to use as `installation`; with none, it sends the person to the provider's install address,
with the project and environment in the address. A project can only connect to an installation of its own organization, and an
installation cannot be moved to another organization while one of its projects is connected to it.

:::note
This mode is tested against a fake GitHub and a fake provider: the routes, the signature, the tokens and their expiry, and that
forwarded events do what GitHub's own do. It has not yet been run with a real GitHub App, real installation tokens or real
forwarded deliveries.
:::

### Tokens and their expiry

An installation token lasts an hour. The provider renews it before then; Geass does not. The connection says where each stands,
in `status.installations` (`tokenState` and `tokenExpiresAt`) and in the condition `TokensReady`, which is `False` once a token has
less than ten minutes left (`TokenExpiringSoon`), and when one is missing or expired (`TokenMissing`, `TokenExpired`). A token
that is due for renewal is still used, so a renewal a few minutes late costs nothing. Once it has less than a minute left it is
not used: a build that needs it waits, as `Pending` with the reason in `failureReason`, and starts once the provider gives a new one;
the repository list answers `503` with the same reason; reports to GitHub are tried again later. Nothing is failed for it.

## What happens under the hood

- The GitHub App is stored on the platform configuration, named `platform`, with its credentials in the Secret
  `platform-github-app`. See [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/).
- Each project you link has a connection, `<project id>-github`, with its installation token in the Secret
  `<project id>-github-token`. See [GeassGitHubConnection](/docs/reference/resources/geass-github-connection/).

- With a provider's App, its connection is a `GeassGitHubConnection` with `spec.external` (named as the provider chose, such as
  `rungeass`), its webhook secret is in the Secret `github-app-<name>-webhook`, and the token of each installation in
  `github-app-<name>-installation-<id>`. A project connected through it has the same `<project id>-github` connection, whose
  Secret names the installation and the provider's connection, and holds a copy of the token in use, which a build's clone reads.

```bash title="Inspect the GitHub connection"
kubectl -n geass-system get geassplatformconfig platform -o yaml
kubectl -n geass-system get secret platform-github-app
kubectl -n geass-system get geassgithubconnection rungeass -o jsonpath='{.status.installations}'
```

## Troubleshooting

**GitHub says "dashboard URL is not configured" or "GitHub App is not configured".** Finish **Settings** → **Domain** first,
then register the app.

**GitHub webhooks return 401.** The webhook secret in GitHub differs from the one Geass stores. Save the matching value with
**Manual credentials**.

**A build stays `Pending` with "Geass has no token for GitHub installation 42 (acme) yet" or "the token for GitHub installation 42
(acme) expired".** The provider of the cluster's GitHub App has not given (or renewed) the installation's token. It starts on its
own once the provider does. `kubectl -n geass-system get geassgithubconnection <name> -o yaml` shows the `TokensReady` condition.

**A build fails with "GitHub installation 42 is no longer connected to this cluster through rungeass".** The provider removed the
installation. Connect the project to GitHub again.

**The provider's forwarded events are answered `401`.** The webhook secret the provider signs with is not the one it gave the
cluster, the signature is not over `<t>.<event>.<body>`, or the clocks are more than five minutes apart. `422` means the event's
installation is not one the connection lists.

**Test GitHub App warns that permissions are missing.** The app can read and build but cannot report back. Follow
[Give an existing GitHub App the new permissions](/docs/guides/github-and-builds/#give-an-existing-github-app-the-new-permissions).
A GitHub App you created yourself, and enter under **Manual credentials**, needs the same permissions set by hand.

## Related

- [Cloud connectors](/docs/guides/connectors/)
- [GitHub and builds](/docs/guides/github-and-builds/)
- [Pull-request environments](/docs/guides/pull-request-environments/)
- [Domains and TLS](/docs/guides/domains-and-tls/)
- [GeassGitHubConnection](/docs/reference/resources/geass-github-connection/)
- [Security](/docs/operations/security/)
