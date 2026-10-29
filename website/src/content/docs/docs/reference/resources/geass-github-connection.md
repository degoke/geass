---
title: "GeassGitHubConnection"
description: "Reference for the GeassGitHubConnection resource, which links a project to GitHub so that Geass can list repositories and build Git services."
sidebar:
  order: 11
---

A `GeassGitHubConnection` links one project to GitHub. It holds no credentials itself. It points at a Secret that holds a GitHub token, and at the Secret of the platform GitHub App that supplies the webhook secret. Geass uses the connection for these tasks:

- To list your repositories.
- To clone your repositories.
- To read the status of CI checks when Geass builds a Git service.
- To report builds, deploys, and pull request environments back to GitHub.

The dashboard creates the connection when you click **Connect to GitHub** on a project and finish the GitHub installation. You do not usually create or edit it yourself. It is in `geass-system`.

## Summary

| Property | Value |
| --- | --- |
| API version | `geass.geass.dev/v1alpha1` |
| Kind | `GeassGitHubConnection` |
| Plural | `geassgithubconnections` |
| Scope | Namespaced |
| Namespace | `geass-system` |
| Subresources | `status` |
| Printer columns | None. Use `kubectl get geassgithubconnections -o wide` or read `status.ready`. |

The dashboard names the connection `<project>-github`, and its token Secret `<project>-github-token`. The dashboard also requires a Git service to use exactly that connection name. See [Editing by hand](#editing-by-hand).

## Example

This is what the dashboard creates for a project named `storefront`.

```yaml title="storefront-github.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassGitHubConnection
metadata:
  name: storefront-github
  namespace: geass-system
spec:
  secretRef:
    name: storefront-github-token
  webhookSecretRef:
    name: platform-github-app
```

The project points at the connection with `spec.githubConnectionRef.name: storefront-github`. See [GeassProject](/docs/reference/resources/geass-project/#github-connection).

## Spec fields

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.secretRef.name` | string | none | Required object. It is a standard `LocalObjectReference`. | The Secret that holds the GitHub token, in the same namespace as the connection. |
| `spec.webhookSecretRef.name` | string | none | Optional | A Secret that must also exist for the connection to be ready. The dashboard sets it to the platform GitHub App Secret, `platform-github-app`, when the platform has one. |

Geass does not read the webhook secret from this reference. Geass verifies GitHub webhooks with the secret in the platform GitHub App configuration. The reference affects only whether the connection reports ready.

### The token Secret

The dashboard writes the Secret `<project>-github-token` with these keys:

| Key | Meaning |
| --- | --- |
| `token` | A GitHub installation token. |
| `installation_id` | The GitHub App installation ID. |
| `account` | The GitHub account that installed the app. |
| `account_type` | `User` or `Organization`. |

An installation token is short-lived. When Geass uses the connection, it asks GitHub for a new installation token with the credentials of the platform GitHub App. Geass writes the new token back into the `token` key, together with `token_minted_at`. Geass uses a token minted less than 30 minutes ago again, so a burst of reports does not mint a token for each report. This behavior needs a configured platform GitHub App. See [Connect GitHub](/docs/guides/connectors/github/).

## Status

| Field | Type | Meaning |
| --- | --- | --- |
| `status.ready` | boolean | Required in the status object. `true` when the Secret named in `spec.secretRef` exists and, if `spec.webhookSecretRef` is set, that Secret also exists. |
| `status.conditions` | list | The `Ready` condition, and `GitHubReporting` after Geass has tried to report to GitHub with this connection. For the App of a provider, also `TokensReady`. See [The connection of a provider's App](#the-connection-of-a-providers-app). |
| `status.installations` | list | For the App of a provider only: the state of the token of each installation. |

### Conditions

The operator sets the condition type `Ready`, and `GitHubReporting` when it reports to GitHub.

| Status | Reason | Message | Meaning |
| --- | --- | --- | --- |
| `True` | `ConnectionReady` | `GitHub connection Secret is available` | Every referenced Secret exists. |
| `False` | `SecretUnavailable` | `GitHub authentication or webhook Secret is unavailable` | A referenced Secret is missing. |

`GitHubReporting` says if GitHub accepts what Geass reports with this connection:

- A commit status for each build.
- A GitHub Deployment for each deploy.
- A comment on each pull request that has an environment.

The condition is not present until Geass has made a report.

| Status | Reason | Meaning |
| --- | --- | --- |
| `True` | `Reporting` | A report went through, and Geass holds back no kind of report. |
| `False` | `PermissionMissing` | GitHub refused a report because the GitHub App does not have the permission that it needs. The message names the permission (Commit statuses, Deployments, or Pull requests, at read and write) and says how to approve it. Geass holds back reports of that kind for ten minutes. The next report after that finds out if the permission was approved. |

A refusal is one of these answers from GitHub:

- `401` or `403`.
- `404` for a repository that the app cannot see. GitHub answers this way for a permission that the installation was never given.

A `404` for one particular GitHub Deployment means that the deployment was deleted, not that a permission is missing. It does not set
the condition. A failure that might pass also does not set the condition, for example no answer from GitHub, a `5xx`, or a rate limit.
Geass retries such a failure up to three times. Geass does not forget a report that was refused or that failed all its tries. Geass sends
the report again after ten minutes. Geass does not record the state that the report was about as reported until the report goes through.
The condition does not affect `status.ready` or any build or deploy. See
[Reporting back to GitHub](/docs/guides/github-and-builds/#reporting-back-to-github).

The operator checks that the Secrets exist. It does not check that the token is valid, or that GitHub accepts it. It also does not watch the Secrets of a project connection. So the condition changes when the connection object changes or the operator restarts, and not at the moment when a Secret appears or disappears. The operator does watch the Secrets of the App of a provider, which are labeled. Dashboard actions do not depend on `status.ready`. They read the Secret directly.

## The connection of a provider's App

A `GeassGitHubConnection` with `spec.external` does not belong to a project. It is the connection of a GitHub App that a provider outside
the cluster runs, for example rungeass. You make and change it through the
[API](/docs/reference/rest-api/#github-an-app-a-provider-runs), not manually. See
[GitHub through a provider](/docs/guides/connectors/github/#github-through-a-provider). Its name does not end in `-github`, so no
organization sees it. The connections of its projects are ordinary `<project>-github` connections.

```yaml title="rungeass.yaml"
apiVersion: geass.geass.dev/v1alpha1
kind: GeassGitHubConnection
metadata:
  name: rungeass
  namespace: geass-system
spec:
  secretRef:
    name: github-app-rungeass-webhook
  webhookSecretRef:
    name: github-app-rungeass-webhook
  external:
    provider: rungeass
    appSlug: rungeass
    installURL: https://app.rungeass.app/github/install
    installations:
      - id: 42
        account: acme
        accountType: Organization
        org: org-acme
status:
  ready: true
  installations:
    - id: 42
      tokenState: Valid
      tokenExpiresAt: "2026-10-23T13:00:00Z"
```

| Field | Type | Validation | Meaning |
| --- | --- | --- | --- |
| `spec.external.provider` | string | Required, 1 to 63 characters | Who runs the App, as the dashboard names it. |
| `spec.external.appSlug` | string | Optional, a GitHub App slug | The name of the App in its GitHub address. Reports are made as `<appSlug>[bot]`, and Geass finds the comment that it keeps on a pull request by this name. |
| `spec.external.installURL` | string | Optional, `https://` | Where a person installs the App. |
| `spec.external.installations[]` | list | At most 1000, each `id` only one time | The installations of the App that belong to the cluster. |
| `spec.external.installations[].id` | integer | Required, at least 1 | The installation id at GitHub. |
| `spec.external.installations[].account` | string | Required, at most 100 characters | The GitHub user or organization that the App is installed on. |
| `spec.external.installations[].accountType` | string | Optional | `User`, `Organization`, or `Enterprise`. |
| `spec.external.installations[].org` | string | Optional | The [organization](/docs/guides/organizations-and-workspaces/) whose projects can connect to it. Empty means the platform itself: projects of no organization. |

`spec.secretRef` and `spec.webhookSecretRef` both name the Secret `github-app-<name>-webhook`. The key `webhook_secret` of this Secret is
the secret that the provider signs forwarded events with. The token of each installation is in the Secret
`github-app-<name>-installation-<id>`. That Secret has these properties:

- The keys `token`, `expires_at`, and `updated_at` (RFC 3339).
- The labels `geass.dev/github-connection: <name>` and `geass.dev/github-installation: <id>`.
- The connection owns the Secret, so the Secret goes with the connection.

Geass never asks GitHub for a token of these installations, and it holds no App private key for them.

| Status field | Meaning |
| --- | --- |
| `status.ready` | `true` when the webhook secret is stored, so Geass accepts forwarded events. |
| `status.installations[].tokenState` | `Valid`. `ExpiringSoon`: less than ten minutes left, still used. `Expired`: expired, or less than a minute left, not used. `Missing`: none given yet, or one without an expiry. |
| `status.installations[].tokenExpiresAt` | When the token that Geass holds stops working. |

The condition `TokensReady` summarizes them:

| Status | Reason | Meaning |
| --- | --- | --- |
| `True` | `TokensValid` | Every installation has a token with at least ten minutes left. |
| `True` | `NoInstallations` | No installation is recorded yet. |
| `False` | `TokenExpiringSoon` | A token has less than ten minutes left. It is still used. Nothing waits. |
| `False` | `TokenMissing` | An installation has no token. What needs it waits. |
| `False` | `TokenExpired` | A token has expired. What needs it waits. |

The message names at most five installations, by id and account, and never a token. The operator looks at the connection again when
the next token is due for renewal or lapses, and whenever one of its Secrets changes. The API calculates the same state at the moment
of the request, so the state is correct even before the operator has written it.

A project connected through it has the usual `<project>-github` connection. Its Secret `<project>-github-token` holds
`installation_id`, `account`, `account_type`, and `external_connection` (the connection of the provider). Geass copies the token in use
into its `token` key, with `token_expires_at`, because the clone of a build reads the token there.

## How the dashboard maps to this resource

| Dashboard | What it does |
| --- | --- |
| **Project Settings**, **GitHub**, **Connect to GitHub** | Starts the installation of the platform GitHub App for this project. When GitHub returns, the dashboard stores the token Secret, creates or updates the connection, and sets `spec.githubConnectionRef` on the project. |
| **+ Add resource**, **GitHub Repository**, **Connect GitHub** | The same flow, started from the add-resource dialog. It returns you to the repository list. |
| **Project Settings**, **GitHub**, **Disconnect** | Clears `spec.githubConnectionRef` on the project. The connection object and its Secret stay in place. |
| **Project Settings**, **Danger**, **Delete Project** | Deletes the connection object. |

When the platform has no GitHub App, the **GitHub** page shows **Set up GitHub** instead of **Connect to GitHub**. Set up the App first under **Settings**, **Connectors**. Before a project can connect, the platform needs a dashboard URL and a GitHub App. See [GitHub and builds](/docs/guides/github-and-builds/).

After a project is connected, **GitHub Repository** in the add-resource dialog lists the repositories that the installation can see. When you select one, the dashboard creates a draft [GeassApp](/docs/reference/resources/geass-app/) whose `source.git.connectionRef.name` is this connection.

## Editing by hand

You rarely need to edit the connection manually. The dashboard owns the connection, and a reconnect rewrites it.

- Keep the name `<project>-github`. The dashboard refuses to create a Git service whose `connectionRef` is not exactly `<project>-github`, with the message `GitHub connection does not belong to this project`. The dashboard also requires the `spec.githubConnectionRef` of the project to name the same connection.
- If you reconnect a project that already has the connection, the dashboard updates `spec.secretRef`, `spec.webhookSecretRef`, and the keys of the token Secret. The dashboard replaces your edits to those fields.
- Geass updates the `token` key of the Secret each time it refreshes the token.

:::caution
After you delete a project, delete the Secret `<project>-github-token` yourself with `kubectl delete secret <project>-github-token -n geass-system`. When you delete a project, Geass deletes the `GeassGitHubConnection`, but not that Secret. The Secret keeps an installation token.
:::

Geass treats a connection as a legacy token connection when its Secret has no `installation_id` and only a `token` (or `githubToken` or `github-token`) key. Geass lists repositories for the user of the token, and clones with the token. A legacy connection never matches a GitHub App webhook, because there is no installation ID to compare. So pull requests do not create pull request environments for it, and GitHub `push` events are never matched to its services. The dashboard does not create this kind of connection.

## What happens under the hood

| Object | Namespace | Notes |
| --- | --- | --- |
| `GeassGitHubConnection` `<project>-github` | `geass-system` | The object this page describes. |
| Secret `<project>-github-token` | `geass-system` | Created and refreshed by the dashboard and the build controller. |
| Secret `platform-github-app` | `geass-system` | The platform GitHub App credentials. `spec.webhookSecretRef` references it. |

When a Git service builds, the build copies the name of the token Secret of the connection into `spec.gitCredentialRef`. The init container of the build Job reads the `token` key into `GITHUB_TOKEN`. Then it clones the repository over HTTPS with an `Authorization: Bearer` header. The controller uses the same token to resolve a branch to a commit. When `waitForCI` is on, the controller also uses the token to read the check runs of the commit.

To inspect a connection, run these commands:

```bash
kubectl get geassgithubconnection storefront-github -n geass-system -o yaml
kubectl get secret storefront-github-token -n geass-system -o jsonpath='{.data.installation_id}' | base64 -d
```

## Troubleshooting

**The dashboard says `GitHub is not set up for this cluster yet`, or connecting fails with `dashboard URL is not configured` or `GitHub App is not configured`.** The platform has no dashboard URL or GitHub App. Configure them under **Settings**, **Connectors**, and then connect the project.

**The dashboard shows `GitHub installation state is invalid` after GitHub redirects back.** Geass could not verify the signed state that GitHub returned. Start again from **Connect to GitHub**.

**GitHub redirects back and the dashboard answers `authentication required`.** To finish the installation, you need a signed-in dashboard session that can make changes. Sign in as an admin, and then start again.

**The repository list is empty or the dashboard shows `could not load GitHub repositories`.** Geass could not use the token, or GitHub did not answer. In the installation settings at GitHub, make sure that the installation includes the repositories that you expect. Then reconnect the project.

**Creating a Git service fails with `GitHub connection does not belong to this project`, `project is not connected to GitHub`, `GitHub connection was not found` or `GitHub connection is unavailable`.** The service must use the connection of its own project, and the project must reference that connection. Connect the project again, or fix `spec.githubConnectionRef`.

**`GitHub connection Secret is unavailable` or `GitHub connection is not ready`.** The token Secret is missing or empty. To write the Secret again, reconnect the project.

**`status.ready` is `false` with `SecretUnavailable`.** One of the Secrets named in the spec is missing. It is usually `platform-github-app`, which disappears when the platform GitHub App settings are cleared. Set up the GitHub App again. Then reconnect the project, so that the dashboard rewrites `spec.webhookSecretRef`.

**A build fails with `GitHub API returned 404 Not Found` or `401 Unauthorized`.** GitHub did not accept the token for that repository. Make sure that the installation includes the repository. See [GeassBuild](/docs/reference/resources/geass-build/#troubleshooting).

## Related

- [GitHub and builds](/docs/guides/github-and-builds/)
- [Connect GitHub](/docs/guides/connectors/github/)
- [GeassProject](/docs/reference/resources/geass-project/)
- [GeassApp](/docs/reference/resources/geass-app/) and [GeassBuild](/docs/reference/resources/geass-build/)
- [Pull request environments](/docs/guides/pull-request-environments/)
- [Resources overview](/docs/reference/resources/overview/)
