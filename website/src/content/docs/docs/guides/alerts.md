---
title: "Alerts"
description: "Get a Slack message or an email when a build or deploy fails, a service is down, backups fail, a service keeps crashing, storage fills, or a server stops."
---

Geass can tell you when something goes wrong, so that you do not have to watch the dashboard. You set up one or more **channels**. A channel is a Slack channel or a list of email addresses. You select which alerts each channel gets. Geass then sends a message when a problem starts, and another message when the problem ends.

Alerts are under **Settings** → **Alerts**. Only a signed-in admin can see or change them. Alerts need the [monitoring add-on](/docs/guides/observability/), which is on by default.

## What you are told about

| Alert | Sent when | Waits |
| --- | --- | --- |
| **Build failed** | A build of the current settings of a service fails. A new failed build is a new alert. | None |
| **Deploy failed** | Geass gave up on a deploy of a service, function, database, or bucket. That is, it failed five times in a row, failed for three minutes, or failed for a reason that a retry cannot fix. The alert ends when the deploy succeeds. | None |
| **Service down** | A deployed service has no copy that is ready. A sleeping service does not count. A service that already has a **Deploy failed** alert does not count. | 5 minutes |
| **Backups failing** | The backups of a database fail, so you might not be able to recover its recent changes. See [Backups and restore](/docs/guides/backups-and-restore/). | None |
| **Node not ready** | A server does not answer, or Geass could not set it up. A server that Geass is upgrading does not count. | 5 minutes |
| **Crash loop** | A service keeps crashing and restarting. | 15 minutes |
| **Storage filling** | A volume of a project is more than 85% full (warning, after 10 minutes) or 95% full (critical, after 5 minutes). Or a volume of Geass is more than 90% full (10 minutes). Or the disk of a server is more than 90% full (15 minutes). | As listed |
| **Storage failing** | The disk of a server became read-only (critical, after 2 minutes). Or a replicated volume lost a copy of its data (warning, after 30 minutes), or every copy (critical, after 5 minutes). See [Disk failures](/docs/operations/disaster-recovery/#disk-failures). | As listed |
| **Abuse suspected** | [Abuse detection](/docs/guides/abuse-detection/) suspects that a service of an organization abuses the servers, for example by mining. It is a signal, not proof. Only platform channels that are not limited to projects get it. | None |
| **Cluster problem** | A cluster upgrade stopped. Or certificates cannot be issued. Or the cluster is not ready. Or the network does not enforce [network isolation](/docs/guides/network-isolation/). Or copies of the cluster state stopped reaching their bucket. Or Geass itself is down or slow. See [Metrics and alerts for the operator](/docs/guides/observability/#metrics-and-alerts-for-the-operator). | 10 minutes, or what each alert on Geass says |

Geass checks every minute. So an alert arrives about one minute after its wait ends. Alerts that start within 30 seconds of each other come in one message. When a server goes down, you get one message for the server, and one message for the services of each project.

A message is **critical** for these problems:

- A failed deploy or a down service in a production environment.
- Failed backups.
- A server that is not ready.
- A project volume that is almost full.
- A disk that became read-only, or a volume that lost every copy.
- A cluster upgrade that stopped, or a cluster that is not ready.

All other messages are **warning**. The severity sets the color of the message. It does not decide which channel gets the message.

### When a problem ends

A **resolved** message arrives five minutes after the problem ends. This delay stops a false "resolved" message when Geass restarts. A problem that continues is sent again every 12 hours.

### What is left out

- **Pull request environments.** A failure there is normal, and the pull request already shows it. See [GitHub and builds](/docs/guides/github-and-builds/).
- **Drafts** that are not deployed, and anything that is being deleted.

### When you add the first channel

Every problem that exists at that moment goes to the new channel within one minute. So you also learn about problems that started before you added the channel. **Firing now** on the page lists them.

## Set up a Slack channel

1. In Slack, create an incoming webhook for the channel. Use *Incoming Webhooks* in the Slack app directory.
2. Copy the URL of the webhook. It starts with `https://hooks.slack.com/services/`.
3. In Geass, open **Settings** → **Alerts**, and click **Slack channel**.
4. Enter a name for the channel, and paste the URL.
5. Select the alerts, the projects, and the environments for the channel.
6. Click **Add channel**.
7. In the menu of the channel, select **Send test**. The test message arrives, or the page tells you why not.

Keep the URL secret. Anyone who has the URL can post to the channel. Geass never shows the URL again. The page shows only the host. Any service that accepts Slack incoming webhooks works, for example Mattermost or Rocket.Chat.

After you change a Slack URL, Geass uses the new URL after one or two minutes.

## Set up email

Email goes through your own mail server, for example Amazon SES, Postmark, Mailgun, SendGrid, or the mail server of your company.

1. Open **Settings** → **Alerts**, and click **Set up mail server**.
2. Enter these values:

   | Field | Value |
   | --- | --- |
   | Server and port | The SMTP host and port of your provider, for example `email-smtp.eu-north-1.amazonaws.com` and `587`. |
   | Security | **STARTTLS** with port 587, or **TLS from the start** with port 465. **None** has no encryption and cannot send a password. Select **None** only for a server that asks for no password. |
   | Username and password | The SMTP credentials of the provider. If the server asks for no username, leave it empty. Geass never shows the password again. |
   | From | The sender address, for example `alerts@example.com` or `Geass <alerts@example.com>`. Most providers accept only an address or domain that you verified with them. |

3. Click **Test connection**. Geass connects and signs in, but sends nothing. If it fails, the result shows the answer of the server.
4. Click **Email**.
5. Enter a name for the channel and up to 20 addresses. Separate the addresses with commas, spaces, or new lines.
6. In the menu of the channel, select **Send test**.

A message has a plain text part and an HTML part. Its header stops out-of-office replies. The certificate of the mail server must be valid. You cannot skip this check.

## Choose what a channel gets

Each channel has these filters:

| Filter | Meaning |
| --- | --- |
| **Alerts** | The alert types that the channel gets. If you select all types, the channel also gets types that Geass adds later. |
| **Projects** | Only these projects. Leave all clear for every project. |
| **Environments** | Only these environments, for example `production`. Leave all clear for every environment. |
| **Also tell me when a problem is resolved** | On by default. Turn it off for a channel that pages a person. |
| **Paused** | Keeps the channel, but sends nothing. |

Some alerts belong to the cluster, not to a project. Examples are alerts about servers, certificates, upgrades, and Geass itself. Only channels that are **not limited to projects** get these alerts. The environment filter does not stop them. So a channel for production still learns that a server went down.

A typical setup has two channels:

- A Slack channel that gets everything.
- An email channel for the person on call. It gets only the **production** environment, and not **Build failed**.

## What a message looks like

A Slack message has these parts:

- A colored bar: red for critical, amber for warning, and green for resolved.
- A title, for example *Deploy failed*, with a count when there are several alerts.
- One line for each item, for example `web (Morning Beach / production)`.
- What the alert means, and what to do.
- A link to the dashboard, when the dashboard has a public address.

An email has the same content. Its subject is like `[Geass] Deploy failed (2 alerts)`.

A message says what is wrong and where. The dashboard says why. For example, the log of a build tells why it failed. A message never contains the output of a service. Geass makes names safe first, so a service named `<!channel>` does not notify the whole Slack workspace.

## See what is happening

The **Alerts** page shows these items:

- Whether the alert sender can be reached. If it cannot, Geass can send nothing, and the page says so.
- **Firing now**: the alerts that are active. Each alert shows its project, how long it has been active, and what it is.
- A warning when a message to Slack or to the mail server failed.

**Send test** sends a test alert the same way as a real alert. So the test proves the whole path. The test can take up to one minute. You can send another test immediately. A test goes only to the channel that you selected, also when the channel is paused.

The dashboard has no control to silence an alert for a time. See [Under the hood](#under-the-hood).

## Alerts for an organization

When [organizations](/docs/guides/organizations-and-workspaces/) are on, each organization has its own channels under **Settings** → **Alerts**. These rules apply to the channel of an organization:

- It gets alerts only about the projects of the organization. It never gets cluster alerts, alerts about other organizations, or **Abuse suspected**.
- When a project moves to another organization, the channel stops getting its alerts immediately.
- A channel of an organization is a Slack channel. It can post only to `hooks.slack.com`. An organization cannot send email alerts.
- An administrator of the organization manages the channels. A member of a workspace manages the channels that name projects of that workspace.
- An organization can have up to 10 channels.

The platform channels that are not limited to projects still get alerts about every project.

## Limits and safety

- The platform can have up to 20 channels.
- An email channel can have up to 20 addresses.
- A filter can name up to 100 projects or environments.
- Only a signed-in admin can change alerts. An API token cannot, not even an admin token. A token that can change where alerts go can also silence them.
- A Slack address must use `https`.
- Geass refuses a host that is a loopback, link-local, or private address. An admin can allow private addresses with `GEASS_WEBHOOKS_ALLOW_PRIVATE_NETWORKS=true` on Geass.
- Geass refuses an address that contains braces.

## Troubleshooting

| Message | What to do |
| --- | --- |
| `Alertmanager could not be reached ... It is part of the monitoring add-on` | The monitoring add-on is off or not running. Turn it on. See [Change the add-ons](/docs/guides/observability/#change-the-add-ons). |
| `Saved. Nothing can be sent until the monitoring add-on is installed` | Geass keeps your settings. Alerts start after the monitoring add-on is installed. |
| `Alertmanager has not loaded this channel yet` | A new channel takes up to one minute to load. Try the test again. |
| `Alertmanager could not post to Slack` | The webhook URL is wrong or was removed in Slack, or your servers cannot reach Slack. |
| `Alertmanager could not send the email` | The login is wrong, or the server refused the sender. The server refuses a From address or domain that you did not verify with the provider. |
| `Alertmanager has not reported sending it yet` | The test is still on its way. Look in the channel after one minute. |
| `No Slack webhook URL is saved for this channel.` | Edit the channel, and paste the URL again. |
| `Set up the mail server first.` | Click **Set up mail server** before you add an email channel. |
| `port 465 is encrypted from the first byte` or `TLS from the start is only used on port 465` | Use **STARTTLS** on port 587, or **TLS from the start** on port 465. |
| `a password cannot be sent over a connection that is not encrypted` | Select **STARTTLS** or **TLS from the start**, or remove the username. |
| `... is a private network address, which cannot be used` | The host is on a private network. See [Limits and safety](#limits-and-safety). |
| `could not connect to host:port: connect: connection refused` or `i/o timeout` | The host or the port is wrong, or a firewall blocks outbound mail. |
| `does not offer STARTTLS` | Use **TLS from the start** with port 465, or the port that the provider gives. |
| `could not set up TLS ... certificate ...` | The certificate of the server is not trusted, or it is for a different host name. |
| `the mail server refused the username and password` | The SMTP credentials are wrong. Many providers use SMTP credentials that are not the account password. |
| Nothing arrives, and **Firing now** is empty | Nothing is wrong, or the alert rules are not loaded. See [Under the hood](#under-the-hood). |
| Something fires, and nothing arrives | The filters of the channel leave out the alert, or the message failed (see the warning on the page), or the alert is silenced. |

## Under the hood

### How it works

Three parts work together:

1. Every 30 seconds, Geass exports a metric, `geass_problem`, for each problem that it finds.
2. Prometheus changes these metrics into alerts. The rules are in the `PrometheusRule` `geass-alerts` in `monitoring`. Each alert has the labels `geass_kind`, `project`, `environment`, and `severity`.
3. Alertmanager sends the alerts. Geass writes the channels into the `AlertmanagerConfig` `geass-alerts` in `geass-system`.

Geass leaves out a channel that cannot send, for example a Slack channel without its URL. So one broken channel does not silence the other channels. The Slack URLs are in the secret `geass-alert-channels`, and the mail password is in `geass-alert-smtp`, both in `geass-system`.

### Commands

To silence an alert for a time, open Alertmanager. It has no public address. Run this command, and open `http://localhost:9093`:

```bash
kubectl -n monitoring port-forward svc/geass-kube-prometheus-stac-alertmanager 9093
```

To check the rules, and to read why Alertmanager failed to send, run these commands:

```bash
kubectl -n monitoring get prometheusrule geass-alerts
kubectl -n monitoring logs alertmanager-geass-kube-prometheus-stac-alertmanager-0 -c alertmanager
```

The settings of the page are in `spec.alerting` of the [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/#spec-fields). To use your own Alertmanager in the dashboard, set `GEASS_ALERTMANAGER_URL` on Geass. Route the alerts there by their labels.

## Related

- [Logs, metrics and terminal](/docs/guides/observability/#metrics-and-alerts-for-the-operator): the monitoring add-on, and the alerts on Geass itself.
- [GitHub and builds](/docs/guides/github-and-builds/): what Geass reports to GitHub, and outgoing webhooks.
- [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/)
