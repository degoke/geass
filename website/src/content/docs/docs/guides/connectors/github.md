---
title: "Connect GitHub"
description: "Connect GitHub to Geass: register the GitHub App and its permissions, link a project, test the webhook, and rotate or remove the credentials."
sidebar:
  label: GitHub
---

Use GitHub when you deploy from a repository. With GitHub, Geass can do these tasks:

- Build on a push.
- Create pull request environments.
- Show each build and deploy on GitHub as a commit status, a deployment, and a pull request comment.

The setup has two parts: one GitHub App for the whole cluster, and one install for each project. For daily use, see
[GitHub and builds](/docs/guides/github-and-builds/) and
[Pull request environments](/docs/guides/pull-request-environments/).

## Before you start

First, set a public HTTPS dashboard address in **Settings** → **Domain**. GitHub calls back to that address. Geass
refuses a GitHub App whose public URL is not HTTPS, with the message "GitHub App public URL must use HTTPS". Until you
set the address, the card shows **Configure domain**, not the app form.

## Register the GitHub App

1. Open **Settings** → **Connectors**.
2. On the **GitHub** card, click **Configure**.
3. Click **Create GitHub App on GitHub**. GitHub shows a prefilled app.
4. Confirm the app in GitHub. GitHub sends you back to Geass, and Geass stores the credentials. The card shows
   **Connected**.

Geass makes the app name from the dashboard host. Geass removes the `geass.` prefix and changes dots to hyphens. For
example, the name for `geass.example.com` is `Geass-example-com`.

The app that Geass asks GitHub for is private. It has the events `push` and `pull_request`, and these permissions:

| Permission | Level | Used for |
| --- | --- | --- |
| **Metadata** | Read | Required of every app. |
| **Contents** | Read | Clone repositories to build them. |
| **Checks** | Read | **Wait for CI** reads the check runs of a commit. |
| **Pull requests** | Read and write | Pull request environments, and the comment that Geass keeps on each pull request. |
| **Commit statuses** | Read and write | The `geass/build/<service name>` status on each commit that Geass builds. |
| **Deployments** | Read and write | A GitHub Deployment for each deploy. |

The write permissions are for reports back to GitHub. See
[Reporting back to GitHub](/docs/guides/github-and-builds/#reporting-back-to-github).

- The webhook URL is `<dashboard URL>/webhooks/github`.
- The callback URL is `<dashboard URL>/github/callback`.

The app also asks the person who installs it to authorize it. The GitHub setting for this is
**Request user authorization (OAuth) during installation**. With this setting, GitHub sends back a code that shows who
came back. GitHub then redirects to the callback URL, and the app uses no setup URL.

If you already have a GitHub App, use **Manual credentials**:

1. Enter **App ID**, **Client ID**, **App slug**, **Client secret**, **Webhook secret**, and **Private key (PEM)**. The
   key must be an RSA private key in PEM form.
2. Click **Save GitHub App**.

You can also give the same values to the operator as environment variables. See
[Configuration](/docs/reference/configuration/). The dashboard form and the stored Secret take precedence over the
environment variables.

## Link a project

1. In a project, open **Settings** → **GitHub**.
2. Click **Connect to GitHub**. Or add a GitHub repository service, which does the same thing.
3. In GitHub, select the account and the repositories that the app can use. GitHub then returns you to Geass.

Geass creates the connection `<project id>-github`. Geass also creates the Secret `<project id>-github-token`, with the
installation token, the installation ID, and the account. The token is short-lived. When Geass needs a token, it asks
GitHub for a new one with the private key of the app.

**Disconnect** removes only the link of the project. The connection object and the Secret stay until you delete them.

### Several organizations on one app

This section applies when [organizations](/docs/guides/organizations-and-workspaces/#github) are on:

- When a person of an organization connects a project, that person must show that GitHub lists the installation
  among the installations that their own account can use.
- You cannot connect an installation that the project of one organization uses to the project of a different
  organization.

For this check, the app must ask for the authorization of the person when that person installs the app. An app that
Geass makes does this. For an app that you registered yourself, do these steps in its settings on GitHub:

1. Turn on **Request user authorization (OAuth) during installation**.
2. Keep the **Callback URL** as `<dashboard URL>/github/callback`.

Without this setting, Geass tells a person of an organization that GitHub did not say who is connecting. Geass does
not ask a platform administrator for this check. While organizations are off, nothing changes.

## Test, rotate and remove

- **Test GitHub App** compares the webhook URL stored in the GitHub App with `<dashboard URL>/webhooks/github`.
  - If the dashboard address changed, the test fails with `GitHub webhook URL is X, expected Y`.
  - If the webhook is correct, the test also reads the permissions that GitHub gave the app. If a permission from the
    table is missing or too low, the test shows a warning. The warning lists those permissions, tells what each one is
    for, and tells how to approve them.
  - An app that an earlier Geass registered does not have Commit statuses or Deployments, and has Pull requests at
    read only. That app builds and deploys as before. It reports nothing to GitHub until the owner approves the
    permissions. An app from an even earlier Geass also has no Checks permission, and **Wait for CI** fails for it. See
    [Give an existing GitHub App the new permissions](/docs/guides/github-and-builds/#give-an-existing-github-app-the-new-permissions).
- To rotate the **Client secret**, **Webhook secret**, or **Private key (PEM)**, make the new value in GitHub. Then
  save it with **Manual credentials**. A field that you leave empty keeps its stored value. Also update the webhook
  secret in GitHub. If you do not, deliveries fail verification.
- **Remove GitHub App** needs the confirmation text `remove-github`. It clears the reference of the platform and
  deletes the Secret `platform-github-app`. The app and its installations stay in GitHub, so uninstall them in GitHub.
  Projects keep their links. But the projects cannot refresh tokens until you register an app again.

The Secret `platform-github-app` holds `app_id`, `client_id`, `client_secret`, `slug`, `private_key`, and
`webhook_secret`.

## How webhooks are verified

GitHub posts to `/webhooks/github`. You can reach this path without a login, because GitHub cannot sign in. Geass
does these checks:

- Geass checks the `X-Hub-Signature-256` header. The header is the HMAC-SHA256 of the body with the webhook secret.
  If the header does not match, Geass rejects the request with `401`.
- If no webhook secret is configured, Geass answers `503`.
- Geass handles `ping`, `push`, and `pull_request`. It ignores other events.

Geass puts the delivery ID of GitHub (`X-GitHub-Delivery`) in the answer and in its log lines. See
[Webhooks](/docs/guides/github-and-builds/#from-github-to-geass).

## GitHub through a provider

A provider that runs Geass for many clusters, for example rungeass, can run one GitHub App for all of the clusters. The
clusters then do not each need an App. In this mode, the cluster never holds the private key of the App, and it
registers no App. The provider does these tasks:

- The provider receives the events of GitHub. For each event, it finds the cluster that has the installation, and
  forwards the event to that cluster.
- The provider makes the access token of each installation with the key of the App, and gives it to the cluster. The
  provider renews each token before it expires.

A self-hosted cluster has none of this until a provider sets it up. If a cluster has its own App, that App continues
to work beside the provider's App.

The provider sets up this mode with the [API](/docs/reference/rest-api/#github-an-app-a-provider-runs), as an
administrator of the platform. The people of an organization cannot get access to any of it.

1. `PUT /api/v1/github/connections/<name>` makes the connection of the App of the provider. The request gives the
   provider name, the App slug, the address where a person installs the App, and a webhook secret. Only this cluster
   and the provider know that webhook secret. Geass keeps the secret in a Secret and never shows it again.
2. `PUT /api/v1/github/connections/<name>/installations/<id>` records an installation of the App as an installation of
   this cluster. The request gives the GitHub account of the installation. It also gives the
   [organization](/docs/guides/organizations-and-workspaces/) whose projects can connect to the installation. For a
   cluster without organizations, the organization is none.
3. `PUT /api/v1/github/connections/<name>/installations/<id>/token` gives Geass the token of the installation and the
   time when it expires. Geass uses that token for all that it does on GitHub for that installation. That includes
   repository lists, clones, branch resolution, check runs, and the statuses, deployments, and comments that it
   reports.
4. The provider forwards the events of GitHub to `/webhooks/github/forwarded/<name>`, signed with the webhook secret.
   Geass does with these events exactly what it does with a webhook from GitHub. For the signature, see
   [Forwarded GitHub events](/docs/reference/rest-api/#forwarded-github-events).

While the App of a provider is set up, **Settings** → **Connectors** → **GitHub** shows that App, not the steps
earlier on this page. The page shows these items:

- The provider.
- The GitHub accounts that the App is installed on.
- How many projects use each account.
- Whether Geass holds a token for each account, and until when.

Geass refuses three actions with `GitHub is provided to this cluster by <provider>`: to create an App on GitHub,
**Manual credentials**, and **Test GitHub App**. **Remove GitHub App** still removes an App that the cluster owns.

**Connect to GitHub** on a project connects the project to the installation of its organization.

- If the organization has several installations, Geass tells which installations there are. The API takes the one to
  use as `installation`.
- If the organization has no installation, Geass sends the person to the install address of the provider. That
  address includes the project and the environment.

A project can connect only to an installation of its own organization. While a project of an installation is
connected, you cannot move that installation to a different organization.

:::note
This mode is tested against a fake GitHub and a fake provider. The tests cover the routes, the signature, the tokens
and their expiry, and that forwarded events do what the events of GitHub do. This mode has not yet run with a real
GitHub App, real installation tokens, or real forwarded deliveries.
:::

### Tokens and their expiry

An installation token lasts one hour. The provider renews the token before then. Geass does not renew it.

The connection shows the state of each token in `status.installations`, in `tokenState` and `tokenExpiresAt`. It also
shows the state in the condition `TokensReady`. `TokensReady` is `False` in these cases:

- A token has less than ten minutes left (`TokenExpiringSoon`).
- A token is missing or expired (`TokenMissing`, `TokenExpired`).

Geass continues to use a token that is due for renewal. Thus, a renewal that is a few minutes late costs nothing.
When a token has less than one minute left, Geass does not use it:

- A build that needs the token waits as `Pending`, with the reason in `failureReason`. The build starts when the
  provider gives a new token.
- The repository list answers `503` with the same reason.
- Geass tries the reports to GitHub again later.

Geass does not fail anything because of the token.

## What happens under the hood

- Geass stores the GitHub App on the platform configuration, named `platform`. Its credentials are in the Secret
  `platform-github-app`. See [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/).
- Each project that you link has a connection, `<project id>-github`. Its installation token is in the Secret
  `<project id>-github-token`. See [GeassGitHubConnection](/docs/reference/resources/geass-github-connection/).

- With the App of a provider, these items apply:
  - The connection of the App is a `GeassGitHubConnection` with `spec.external`. It has the name that the provider
    chose, for example `rungeass`.
  - Its webhook secret is in the Secret `github-app-<name>-webhook`.
  - The token of each installation is in `github-app-<name>-installation-<id>`.
  - A project that connects through the App has the same `<project id>-github` connection. The Secret of that
    connection names the installation and the connection of the provider. The Secret also holds a copy of the token
    in use, which the clone step of a build reads.

```bash title="Inspect the GitHub connection"
kubectl -n geass-system get geassplatformconfig platform -o yaml
kubectl -n geass-system get secret platform-github-app
kubectl -n geass-system get geassgithubconnection rungeass -o jsonpath='{.status.installations}'
```

## Troubleshooting

**GitHub says "dashboard URL is not configured" or "GitHub App is not configured".** First finish **Settings** →
**Domain**. Then register the app.

**GitHub webhooks return 401.** The webhook secret in GitHub is different from the secret that Geass stores. Save the
matching value with **Manual credentials**.

**A build stays `Pending` with "Geass has no token for GitHub installation 42 (acme) yet" or "the token for GitHub installation 42
(acme) expired".** The provider of the GitHub App of the cluster has not given or renewed the token of the
installation. The build starts automatically when the provider does. To see the `TokensReady` condition, run
`kubectl -n geass-system get geassgithubconnection <name> -o yaml`.

**A build fails with "GitHub installation 42 is no longer connected to this cluster through rungeass".** The provider
removed the installation. Connect the project to GitHub again.

**The provider's forwarded events are answered `401`.** One of these is the cause:

- The webhook secret that the provider signs with is not the secret that it gave the cluster.
- The signature is not over `<t>.<event>.<body>`.
- The clocks are more than five minutes apart.

`422` means that the installation of the event is not one that the connection lists.

**Test GitHub App warns that permissions are missing.** The app can read and build, but it cannot report back. Follow
[Give an existing GitHub App the new permissions](/docs/guides/github-and-builds/#give-an-existing-github-app-the-new-permissions).
If you made a GitHub App yourself and entered it under **Manual credentials**, set the same permissions on it by hand.

## Related

- [Cloud connectors](/docs/guides/connectors/)
- [GitHub and builds](/docs/guides/github-and-builds/)
- [Pull-request environments](/docs/guides/pull-request-environments/)
- [Domains and TLS](/docs/guides/domains-and-tls/)
- [GeassGitHubConnection](/docs/reference/resources/geass-github-connection/)
- [Security](/docs/operations/security/)
