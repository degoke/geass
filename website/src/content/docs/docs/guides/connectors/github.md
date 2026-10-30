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

The setup has two parts: one GitHub App for the whole cluster, and one link for each project. For daily use, see
[GitHub and builds](/docs/guides/github-and-builds/) and
[Pull request environments](/docs/guides/pull-request-environments/).

## Before you start

Set a public HTTPS dashboard address in **Settings** → **Domain**. GitHub calls back to that address. Until you set the
address, the **GitHub** drawer shows **Configure domain**, not the app form. Geass refuses an app whose address is not
HTTPS, with "GitHub App public URL must use HTTPS".

## Register the GitHub App

1. Open **Settings** → **Connectors**.
2. On the **GitHub** card, click **Configure**.
3. Click **Create GitHub App on GitHub**. GitHub shows an app with the settings filled in.
4. Confirm the app in GitHub. GitHub sends you back to Geass, and the card shows **Connected**.

Geass makes the app name from the dashboard host. For example, the name for `geass.example.com` is
`Geass-example-com`.

The app is private. It receives the `push` and `pull_request` events, and has these permissions:

| Permission | Level | Used for |
| --- | --- | --- |
| **Metadata** | Read | Required of every app. |
| **Contents** | Read | Clone repositories to build them. |
| **Checks** | Read | **Wait for CI** reads the check runs of a commit. |
| **Pull requests** | Read and write | Pull request environments, and the comment that Geass keeps on each pull request. |
| **Commit statuses** | Read and write | The `geass/build/<service name>` status on each commit that Geass builds. |
| **Deployments** | Read and write | A GitHub deployment for each deploy. |

The write permissions are for reports back to GitHub. See
[Reporting back to GitHub](/docs/guides/github-and-builds/#reporting-back-to-github).

The webhook URL is `<dashboard URL>/webhooks/github`. The callback URL is `<dashboard URL>/github/callback`. The app
also has **Request user authorization (OAuth) during installation** turned on, so that GitHub says who installed it.

### Use a GitHub App that you have

1. In the **GitHub** drawer, open **Manual credentials**.
2. Enter **App ID**, **Client ID**, **App slug**, **Client secret**, **Webhook secret**, and **Private key (PEM)**. The
   key must be an RSA private key in PEM form.
3. Click **Save GitHub App**.

Give the app the permissions and events in the table earlier on this page. Set its webhook URL and callback URL to the
addresses earlier on this page.

## Link a project

1. In the project, open **Settings** → **GitHub**.
2. Click **Connect to GitHub**. To do the same thing, you can also add a GitHub repository service.
3. In GitHub, select the account and the repositories that the app can use. GitHub then returns you to Geass.

**Disconnect** removes only the link of the project.

### Several organizations on one app

This section applies when [organizations](/docs/guides/organizations-and-workspaces/#github) are on:

- When a person of an organization links a project, GitHub must list the installation among the installations that
  the account of that person can use.
- You cannot link an installation that a project of one organization uses to a project of another organization.

For this check, the app must ask the person to authorize it during installation. An app that Geass makes does this.
For an app that you registered yourself, do these steps in its settings on GitHub:

1. Turn on **Request user authorization (OAuth) during installation**.
2. Keep the **Callback URL** as `<dashboard URL>/github/callback`.

Without this setting, Geass tells a person of an organization that GitHub did not say who is connecting. While
organizations are off, nothing changes.

## Test, rotate, and remove

**Test GitHub App** compares the webhook URL of the app with `<dashboard URL>/webhooks/github`:

- If the dashboard address changed, the test fails with `GitHub webhook URL is X, expected Y`.
- If the webhook is correct, the test also reads the permissions of the app. If a permission from the table is
  missing or too low, the test shows a warning. The warning tells what each permission is for, and how to approve it.

An app from an earlier Geass might not have the report permissions. That app builds and deploys as before, but reports
nothing to GitHub until the owner approves the permissions. See
[Give an existing GitHub App the new permissions](/docs/guides/github-and-builds/#give-an-existing-github-app-the-new-permissions).

To rotate the **Client secret**, **Webhook secret**, or **Private key (PEM)**, do these steps:

1. Make the new value in GitHub.
2. Enter it under **Manual credentials**. A field that you leave empty keeps its stored value.
3. Click **Save GitHub App**.
4. If you changed the webhook secret, also set it in GitHub. If you do not, GitHub deliveries fail.

To remove the app from Geass, enter `remove-github` in the confirmation field, and click **Remove GitHub App**. The app
and its installations stay in GitHub, so uninstall them there. Projects keep their links, but cannot get new GitHub
tokens until you register an app again.

## How webhooks are verified

GitHub sends events to `/webhooks/github`. This address needs no sign-in, because GitHub cannot sign in. Geass checks
the signature of each event with the webhook secret, and rejects a bad signature with `401`. If no webhook secret is
set, Geass answers `503`. Geass acts on `ping`, `push`, and `pull_request`, and ignores other events. See
[Webhooks](/docs/guides/github-and-builds/#from-github-to-geass).

## GitHub through a provider

A provider that runs Geass for many clusters, for example rungeass, can run one GitHub App for all of them. Then a
cluster needs no app of its own, and never holds the private key of the app. The provider does these tasks:

- It receives the events of GitHub, and forwards each event to the cluster that has the installation.
- It makes the access token of each installation, gives it to the cluster, and renews it before it expires.

A self-hosted cluster has none of this until a provider sets it up. A cluster that has its own app keeps that app
beside the app of the provider.

The provider sets up this mode with the [API](/docs/reference/rest-api/#github-an-app-a-provider-runs), as a platform
administrator:

1. `PUT /api/v1/github/connections/<name>` makes the connection of the app of the provider. The request gives the
   provider name, the app slug, the address where a person installs the app, and a webhook secret.
2. `PUT /api/v1/github/connections/<name>/installations/<id>` records an installation for this cluster. The request
   gives the GitHub account, and the [organization](/docs/guides/organizations-and-workspaces/) whose projects can
   use it.
3. `PUT /api/v1/github/connections/<name>/installations/<id>/token` gives Geass the token of the installation and
   when it expires.
4. The provider forwards GitHub events to `/webhooks/github/forwarded/<name>`, signed with the webhook secret. See
   [Forwarded GitHub events](/docs/reference/rest-api/#forwarded-github-events).

While a provider app is set up, the **GitHub** drawer shows these items:

- The provider.
- The GitHub accounts that the app is installed on.
- How many projects use each account.
- Whether Geass has a valid token for each account, and until when.

Geass refuses **Create GitHub App on GitHub**, **Manual credentials**, and **Test GitHub App** with
`GitHub is provided to this cluster by <provider>`. **Remove GitHub App** still removes an app that the cluster owns.

**Connect to GitHub** on a project links the project to the installation of its organization. If the organization has
several installations, Geass lists them. If it has none, Geass sends the person to the install address of the
provider. A project can use only an installation of its own organization.

An installation token lasts one hour, and the provider renews it. When a token has less than one minute left, Geass
does not use it. A build that needs the token waits, and starts when the provider gives a new token. Geass tries
reports to GitHub again later.

:::note
This mode was tested against a fake GitHub and a fake provider. It has not yet run with a real GitHub App, real
installation tokens, or real forwarded events.
:::

## Troubleshooting

**"dashboard URL is not configured" or "GitHub App is not configured".** Finish **Settings** → **Domain** first. Then
register the app.

**GitHub webhooks return 401.** The webhook secret in GitHub is different from the secret in Geass. Enter the same
value under **Manual credentials**.

**A build waits with "Geass has no token for GitHub installation 42 (acme) yet" or "the token for GitHub installation
42 (acme) expired".** The provider has not given or renewed the token. The build starts when the provider does.

**A build fails with "GitHub installation 42 is no longer connected to this cluster through rungeass".** The provider
removed the installation. Link the project to GitHub again.

**The forwarded events of the provider get `401`.** The provider signs with a different secret, or the clocks are more
than five minutes apart. `422` means that the connection does not list the installation of the event.

**Test GitHub App warns that permissions are missing.** The app can build, but cannot report back. Follow
[Give an existing GitHub App the new permissions](/docs/guides/github-and-builds/#give-an-existing-github-app-the-new-permissions).
For an app that you entered under **Manual credentials**, set the permissions on it in GitHub.

## Under the hood

- The GitHub App is on the platform configuration, named `platform`. Its credentials are in the Secret
  `platform-github-app` in `geass-system`. See
  [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/).
- Each linked project has a connection `<project id>-github`, with its installation token in the Secret
  `<project id>-github-token`. See [GeassGitHubConnection](/docs/reference/resources/geass-github-connection/).
- You can also give the app credentials to Geass as environment variables. See
  [Configuration](/docs/reference/configuration/). The dashboard form has priority over the variables.
- The app of a provider is a `GeassGitHubConnection` with `spec.external`. Its `TokensReady` condition and
  `status.installations` show the state of each token.

```bash title="Inspect the GitHub connection"
kubectl -n geass-system get geassplatformconfig platform -o yaml
kubectl -n geass-system get geassgithubconnection rungeass -o jsonpath='{.status.installations}'
```

## Related

- [Cloud connectors](/docs/guides/connectors/)
- [GitHub and builds](/docs/guides/github-and-builds/)
- [Pull request environments](/docs/guides/pull-request-environments/)
- [Domains and TLS](/docs/guides/domains-and-tls/)
- [Security](/docs/operations/security/)
