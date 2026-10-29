---
title: "GeassGitHubConnection"
description: "Reference for the GeassGitHubConnection resource that links a project to GitHub so Geass can list repositories and build Git services."
sidebar:
  order: 11
---

A `GeassGitHubConnection` links one project to GitHub. It holds no credentials itself. It points at a Secret that holds a GitHub token, and at the Secret of the platform GitHub App that supplies the webhook secret. Geass uses it to list your repositories, to clone them, to read the status of CI checks when it builds a Git service, and to report builds, deploys and pull request environments back to GitHub.

The dashboard creates it when you click **Connect to GitHub** on a project and finish the GitHub installation. You do not normally create or edit it yourself. It lives in `geass-system`.

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

The dashboard names the connection `<project>-github` and its token Secret `<project>-github-token`. The dashboard also requires a Git service to use exactly that connection name. See [Editing by hand](#editing-by-hand).

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

The project points at it with `spec.githubConnectionRef.name: storefront-github`. See [GeassProject](/docs/reference/resources/geass-project/#github-connection).

## Spec fields

| Field | Type | Default | Validation | Meaning |
| --- | --- | --- | --- | --- |
| `spec.secretRef.name` | string | none | Required object. It is a standard `LocalObjectReference`. | The Secret that holds the GitHub token, in the same namespace as the connection. |
| `spec.webhookSecretRef.name` | string | none | Optional | A Secret that must also exist for the connection to be ready. The dashboard sets it to the platform GitHub App Secret, `platform-github-app`, when the platform has one. |

The webhook secret is not read from this reference. GitHub webhooks are verified with the secret in the platform GitHub App configuration. The reference only affects whether the connection reports ready.

### The token Secret

The dashboard writes the Secret `<project>-github-token` with these keys:

| Key | Meaning |
| --- | --- |
| `token` | A GitHub installation token. |
| `installation_id` | The GitHub App installation ID. |
| `account` | The GitHub account that installed the app. |
| `account_type` | `User` or `Organization`. |

An installation token is short lived. When Geass uses the connection it asks GitHub for a new installation token with the platform GitHub App's credentials and writes it back into the `token` key, together with `token_minted_at`. A token minted less than 30 minutes ago is used again, so a burst of reports does not mint one for each. This needs the platform GitHub App to be configured. See [Connect GitHub](/docs/guides/connectors/github/).

## Status

| Field | Type | Meaning |
| --- | --- | --- |
| `status.ready` | boolean | Required in the status object. `true` when the Secret named in `spec.secretRef` exists and, if `spec.webhookSecretRef` is set, that Secret exists too. |
| `status.conditions` | list | The `Ready` condition, and `GitHubReporting` once Geass has tried to report to GitHub with this connection. For a provider's App, also `TokensReady`; see [The connection of a provider's App](#the-connection-of-a-providers-app). |
| `status.installations` | list | For a provider's App only: the state of each installation's token. |

### Conditions

The operator sets the condition type `Ready`, and `GitHubReporting` when it reports to GitHub.

| Status | Reason | Message | Meaning |
| --- | --- | --- | --- |
| `True` | `ConnectionReady` | `GitHub connection Secret is available` | Every referenced Secret exists. |
| `False` | `SecretUnavailable` | `GitHub authentication or webhook Secret is unavailable` | A referenced Secret is missing. |

`GitHubReporting` says whether GitHub takes what Geass reports with this connection: a commit status for each build, a GitHub
Deployment for each deploy and a comment on each pull request that has an environment. It is not there until a report has
been made.

| Status | Reason | Meaning |
| --- | --- | --- |
| `True` | `Reporting` | A report went through, and none of the kinds of report is being held back. |
| `False` | `PermissionMissing` | GitHub refused a report because the GitHub App does not have the permission it needs. The message names the permission (Commit statuses, Deployments or Pull requests, at read and write) and says how to approve it. Reports of that kind are held back for ten minutes, and the next one after that finds out whether it was approved. |

A refusal is `401` or `403` from GitHub, or `404` for a repository the app cannot see, which is how GitHub answers for a
permission the installation was never given. A `404` for one particular GitHub Deployment is that deployment having been
deleted, not a missing permission, and does not set the condition. A failure that may pass (GitHub not answering, a `5xx`, a
rate limit) does not set the condition either: it is retried up to three times. A report that was refused or that failed
all its tries is not forgotten: it is sent again after ten minutes, and the state it was about is not recorded as reported
until it has been. The condition does not affect `status.ready` or any build or deploy. See
[Reporting back to GitHub](/docs/guides/github-and-builds/#reporting-back-to-github).

The operator checks that the Secrets exist. It does not check that the token is valid or that GitHub accepts it. It also does not watch a project connection's Secrets, so the condition changes when the connection object changes or the operator restarts, and not at the moment a Secret appears or disappears. (It does watch the Secrets of a provider's App, which are labelled.) Dashboard actions do not depend on `status.ready`. They read the Secret directly.

## The connection of a provider's App

A `GeassGitHubConnection` with `spec.external` is not a project's: it is the connection of a GitHub App that a provider outside the
cluster runs, such as rungeass, made and changed through the
[API](/docs/reference/rest-api/#github-an-app-a-provider-runs) and not by hand. See
[GitHub through a provider](/docs/guides/connectors/github/#github-through-a-provider). Its name does not end in `-github`, so no
organization sees it; its projects' connections are ordinary `<project>-github` ones.

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
| `spec.external.appSlug` | string | Optional, a GitHub App slug | The App's name in its GitHub address. Reports are made as `<appSlug>[bot]`, and the comment Geass keeps on a pull request is found by it. |
| `spec.external.installURL` | string | Optional, `https://` | Where a person installs the App. |
| `spec.external.installations[]` | list | At most 1000, each `id` once | The installations of the App that belong to the cluster. |
| `spec.external.installations[].id` | integer | Required, at least 1 | GitHub's installation id. |
| `spec.external.installations[].account` | string | Required, at most 100 characters | The GitHub user or organization the App is installed on. |
| `spec.external.installations[].accountType` | string | Optional | `User`, `Organization` or `Enterprise`. |
| `spec.external.installations[].org` | string | Optional | The [organization](/docs/guides/organizations-and-workspaces/) whose projects may connect to it. Empty is the platform's own: projects of no organization. |

`spec.secretRef` and `spec.webhookSecretRef` both name the Secret `github-app-<name>-webhook`, whose key `webhook_secret` is the
secret the provider signs forwarded events with. The token of each installation is in the Secret
`github-app-<name>-installation-<id>`, with the keys `token`, `expires_at` and `updated_at` (RFC 3339), labelled
`geass.dev/github-connection: <name>` and `geass.dev/github-installation: <id>`, and owned by the connection, so they go with it.
Geass never asks GitHub for a token of these installations and holds no App private key for them.

| Status field | Meaning |
| --- | --- |
| `status.ready` | `true` when the webhook secret is stored, so forwarded events are taken. |
| `status.installations[].tokenState` | `Valid`; `ExpiringSoon` (less than ten minutes left, still used); `Expired` (expired, or less than a minute left, not used); `Missing` (none given yet, or one without an expiry). |
| `status.installations[].tokenExpiresAt` | When the token Geass holds stops working. |

The condition `TokensReady` sums them up:

| Status | Reason | Meaning |
| --- | --- | --- |
| `True` | `TokensValid` | Every installation has a token with at least ten minutes left. |
| `True` | `NoInstallations` | No installation is recorded yet. |
| `False` | `TokenExpiringSoon` | A token has less than ten minutes left. It is still used; nothing waits. |
| `False` | `TokenMissing` | An installation has no token. What needs it waits. |
| `False` | `TokenExpired` | A token has expired. What needs it waits. |

The message names at most five installations, by id and account, and never a token. The operator looks at the connection again when
the next token is due for renewal or lapses, and whenever one of its Secrets changes. The API works the same state out at the moment
it is asked, so it is right even before the operator has written it.

A project connected through it has the usual `<project>-github` connection. Its Secret `<project>-github-token` holds
`installation_id`, `account`, `account_type`, and `external_connection` (the provider's connection), and Geass copies the token in use
into its `token` key, with `token_expires_at`, because that is where a build's clone reads it.

## How the dashboard maps to this resource

| Dashboard | What it does |
| --- | --- |
| **Project Settings**, **GitHub**, **Connect to GitHub** | Starts the installation of the platform GitHub App for this project. When GitHub returns, the dashboard stores the token Secret, creates or updates the connection and sets `spec.githubConnectionRef` on the project. |
| **+ Add resource**, **GitHub Repository**, **Connect GitHub** | The same flow, started from the add-resource dialog. It returns you to the repository list. |
| **Project Settings**, **GitHub**, **Disconnect** | Clears `spec.githubConnectionRef` on the project. The connection object and its Secret stay in place. |
| **Project Settings**, **Danger**, **Delete Project** | Deletes the connection object. |

The **GitHub** page shows **Set up GitHub** instead of **Connect to GitHub** when the platform has no GitHub App. Set that up first under **Settings**, **Connectors**. The platform needs a dashboard URL and a GitHub App before a project can connect. See [GitHub and builds](/docs/guides/github-and-builds/).

Once a project is connected, **GitHub Repository** in the add-resource dialog lists the repositories that the installation can see. Choosing one creates a draft [GeassApp](/docs/reference/resources/geass-app/) whose `source.git.connectionRef.name` is this connection.

## Editing by hand

You rarely need to. The dashboard owns the connection, and reconnecting rewrites it.

- Keep the name `<project>-github`. The dashboard refuses to create a Git service whose `connectionRef` is not exactly `<project>-github`, with the message `GitHub connection does not belong to this project`, and it requires the project's `spec.githubConnectionRef` to name the same connection.
- Reconnecting a project that already has the connection updates `spec.secretRef` and `spec.webhookSecretRef` and the keys of the token Secret. Your edits to those fields are replaced.
- Geass updates the `token` key of the Secret each time it refreshes the token.

:::caution
Deleting a project deletes the `GeassGitHubConnection`, but not the Secret `<project>-github-token`. The Secret keeps an installation token. Delete it yourself with `kubectl delete secret <project>-github-token -n geass-system`.
:::

A connection whose Secret has no `installation_id` and only a `token` (or `githubToken` or `github-token`) key is treated as a legacy token connection. Geass lists repositories for the token's user and clones with it. It never matches a GitHub App webhook, because there is no installation ID to compare, so pull requests do not create pull request environments for it and GitHub's `push` events are never matched to its services. The dashboard does not create this kind of connection.

## What happens under the hood

| Object | Namespace | Notes |
| --- | --- | --- |
| `GeassGitHubConnection` `<project>-github` | `geass-system` | The object this page describes. |
| Secret `<project>-github-token` | `geass-system` | Created and refreshed by the dashboard and the build controller. |
| Secret `platform-github-app` | `geass-system` | The platform GitHub App credentials. It is referenced by `spec.webhookSecretRef`. |

When a Git service builds, the build copies the connection's token Secret name into `spec.gitCredentialRef`. The build Job's init container reads the `token` key into `GITHUB_TOKEN`, and clones the repository over HTTPS with an `Authorization: Bearer` header. The controller uses the same token to resolve a branch to a commit and, when `waitForCI` is on, to read the check runs of the commit.

Inspect a connection with these commands:

```bash
kubectl get geassgithubconnection storefront-github -n geass-system -o yaml
kubectl get secret storefront-github-token -n geass-system -o jsonpath='{.data.installation_id}' | base64 -d
```

## Troubleshooting

**The dashboard says `GitHub is not set up for this cluster yet`, or connecting fails with `dashboard URL is not configured` or `GitHub App is not configured`.** The platform has no dashboard URL or GitHub App. Configure them under **Settings**, **Connectors**, then connect the project.

**The dashboard shows `GitHub installation state is invalid` after GitHub redirects back.** The signed state that GitHub returned could not be verified. Start again from **Connect to GitHub**.

**GitHub redirects back and the dashboard answers `authentication required`.** Finishing the installation needs a signed-in dashboard session that can make changes. Sign in as an admin, then start again.

**The repository list is empty or the dashboard shows `could not load GitHub repositories`.** The token could not be used, or GitHub did not answer. Check that the installation includes the repositories you expect, in GitHub's installation settings. Then reconnect the project.

**Creating a Git service fails with `GitHub connection does not belong to this project`, `project is not connected to GitHub`, `GitHub connection was not found` or `GitHub connection is unavailable`.** The service must use the project's own connection, and the project must reference it. Connect the project again, or fix `spec.githubConnectionRef`.

**`GitHub connection Secret is unavailable` or `GitHub connection is not ready`.** The token Secret is missing or empty. Reconnect the project to write it again.

**`status.ready` is `false` with `SecretUnavailable`.** One of the Secrets named in the spec is missing. The most common one is `platform-github-app`, which disappears when the platform GitHub App settings are cleared. Set up the GitHub App again, then reconnect the project so the dashboard rewrites `spec.webhookSecretRef`.

**A build fails with `GitHub API returned 404 Not Found` or `401 Unauthorized`.** GitHub did not accept the token for that repository. Check that the installation includes the repository. See [GeassBuild](/docs/reference/resources/geass-build/#troubleshooting).

## Related

- [GitHub and builds](/docs/guides/github-and-builds/)
- [Connect GitHub](/docs/guides/connectors/github/)
- [GeassProject](/docs/reference/resources/geass-project/)
- [GeassApp](/docs/reference/resources/geass-app/) and [GeassBuild](/docs/reference/resources/geass-build/)
- [Pull request environments](/docs/guides/pull-request-environments/)
- [Resources overview](/docs/reference/resources/overview/)
