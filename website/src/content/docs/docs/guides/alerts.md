---
title: "Alerts"
description: "Get a Slack message or an email when a build or deploy fails, a service is down, backups fail, a pod crashes, storage fills, or a node stops responding."
---

Geass can tell you when something goes wrong, so you do not have to watch the dashboard to find out. You set up one or more **channels**. Each channel is a Slack channel or a list of email addresses, and you select which alerts each channel hears. After that, you get a message for each of these problems, and another message when the problem is gone:

- A failed build.
- A deploy that Geass gave up on.
- A service with nothing running.
- A database whose backups are failing.
- A pod that keeps crashing.
- A volume that is filling.
- A node that stopped responding.
- A cluster problem.

Alerting is under **Settings** → **Alerts**. Only a signed-in admin can see or change it. Alerting needs the [monitoring add-on](/docs/guides/observability/), which is on by default. Prometheus makes the alerts, and Alertmanager sends them. The monitoring add-on installs both of them. Geass does not send the alerts itself.

## What you are told about

| Alert | Sent when | Waits |
| --- | --- | --- |
| **Build failed** | A build of a repository service's current settings fails. If the service is built again and fails again, that is a new alert | None |
| **Deploy failed** | A service, function, database, or bucket could not be deployed, and Geass gave up. That is, it failed five times in a row, or for three minutes, or for a reason that no retry can fix. The alert stays open while you try again, and ends when the resource is ready | None |
| **Service down** | A service that was deployed has no ready replica. This does not include a service that is scaled to zero, or a service that is already reported as failing to deploy | 5 minutes |
| **Backups failing** | A database's backups are failing, so you might not be able to recover its recent changes. See [Backups and restore](/docs/guides/backups-and-restore/) | None |
| **Node not ready** | A node of the cluster is not ready, or Geass could not set up the node. This does not include a node whose K3s is being upgraded | 5 minutes |
| **Crash loop** | A container of a pod keeps crashing and being restarted. A pod of a project belongs to that project | 15 minutes |
| **Storage filling** | A project's volume is more than 85% full (warning, after 10 minutes) or more than 95% full (critical, after 5 minutes). Or a platform volume is more than 90% full (10 minutes). Or a node's disk is more than 90% full (15 minutes) | As listed |
| **Storage failing** | A node's disk became read-only (critical, after 2 minutes). Or a replicated volume lost a copy of its data and did not get it back (warning, after 30 minutes). Or a replicated volume lost every copy (critical, after 5 minutes). See [Disk failures](/docs/operations/disaster-recovery/#disk-failures) | As listed |
| **Abuse suspected** | The [abuse detection](/docs/guides/abuse-detection/) (`spec.tenancy.abuse`) suspects that a service of an organization abuses the machines, for example by mining. The alert is a warning, or critical when the service was suspended. It is a signal, not proof. Only the platform's channels hear it. The alert carries no project, so no organization's channel hears it. A platform channel that is limited to some projects does not hear it either | None |
| **Cluster problem** | A cluster upgrade stopped. Or certificates cannot be issued. Or Geass gave up getting the cluster ready. Or the network plugin does not enforce network policies. Or copies of the cluster's own state stopped reaching their bucket, when you asked for these copies (see [Disaster recovery](/docs/operations/disaster-recovery/)). Or the operator itself is down or is not keeping up (the alerts under [Metrics and alerts for the operator](/docs/guides/observability/#metrics-and-alerts-for-the-operator)) | 10 minutes for the cluster itself, and for the operator's alerts, what each alert says |

Prometheus checks every minute. So you get an alert about one minute after its wait ends. Alertmanager then holds a group for **30 seconds**, so that alerts that arrive together become one message. When a node goes down, every service on that node goes down with it. You then get one message for the node, and one message for the services of each project, not one message for each service.

A message is **critical** for these problems:

- A deploy that failed, or a service that is down, in a production environment.
- A database's backups.
- A node.
- A project volume that is almost full.
- A disk that became read-only, or a volume that cannot be read.
- A cluster upgrade that stopped, or a cluster that is not ready.

A message is **warning** for all other problems. The severity sets the color of the message. The severity does not route the message: a channel hears the alerts that you selected for it, at every severity.

### When a problem ends

Alertmanager sends a message that says a problem is **resolved** when the problem has been gone for **five minutes**, not immediately. Prometheus keeps an alert firing for that time after its cause is gone. The reason is that when the operator restarts, or another operator takes over, the operator reports nothing for one minute. Without the delay, every problem that still exists would be announced as resolved, and then announced again.

Alertmanager mentions a problem that is still there again every **twelve hours**.

### What is left out

- **Pull request environments.** It is normal for a copy made for a pull request to fail. The pull request already shows the failure through [GitHub](/docs/guides/github-and-builds/). No alert is sent for anything in a pull request environment.
- **Drafts** that are not deployed, and anything that is being deleted.

### When you add the first channel

Everything that is firing at that moment goes to the new channel within one minute. This is how you find out about a problem that existed before you added the channel. If there are several problems, you can get several messages. **Firing now** on the page lists the alerts that Alertmanager holds.

## Set up a Slack channel

1. In Slack, create an **incoming webhook** for the channel that you want. Use *Incoming Webhooks* in the Slack app directory.
2. Copy the URL of the webhook. The URL starts with `https://hooks.slack.com/services/`.
3. In Geass, open **Settings** → **Alerts** → **Slack channel**.
4. Enter a name for the channel, and paste the URL.
5. Select the alerts, the projects, and the environments that the channel hears.
6. Click **Add channel**.
7. From the channel's menu, select **Send test**. A test message arrives, or the page tells you why it did not arrive.

The URL is a secret. Anyone who has the URL can post to the channel. Geass keeps the URL in the Secret `geass-alert-channels` in `geass-system`, under the channel's id. Geass never sends the URL back to the browser. The page shows only the host that the channel posts to. Any service that accepts Slack's incoming webhook JSON works, for example Mattermost or Rocket.Chat.

If you change a Slack URL, the new URL is used from a minute or two later, because the monitoring add-on must detect the change.

## Set up email

Email goes through a mail server of your own. It can be your cloud's mail server, for example Amazon SES, Postmark, Mailgun, or SendGrid, or your company's mail server. Geass does not send mail itself.

1. Open **Settings** → **Alerts** → **Set up mail server**, and enter these values:

   | Field | What to put |
   | --- | --- |
   | Server and port | Your provider's SMTP host and port, for example `email-smtp.eu-north-1.amazonaws.com` and `587` |
   | Security | **STARTTLS** with port 587, or **TLS from the start** with port **465**. Port 465 is the only port that is encrypted from the first byte. **None** has no encryption, so it cannot have a password. Select it only for a server that asks for none |
   | Username and password | The provider's SMTP credentials. If the server does not ask for a username, leave the username empty. Geass keeps the password in the Secret `geass-alert-smtp`, and does not send it back to the browser |
   | From | The address that the mail comes from, for example `alerts@example.com` or `Geass <alerts@example.com>`. Most providers accept only an address or a domain that you verified with them |

2. Click **Test connection**. Geass connects from the operator, encrypts the connection, and signs in, the same as for a message. It sends nothing. If the password is wrong or the port is closed, the result shows the server's own words. The test does not show that the monitoring namespace can also reach the server. **Send test** shows that.
3. Select **Email**.
4. Enter a name for the channel and up to 20 addresses. Separate the addresses with commas, spaces, or new lines.
5. From the channel's menu, select **Send test**.

The mail is plain text and HTML. Its header has `Auto-Submitted: auto-generated`, so that out-of-office replies are not sent back. Alertmanager checks the mail server's certificate, and you cannot skip this check.

## Choose what a channel hears

Each channel has its own filters:

| Filter | Meaning |
| --- | --- |
| **Alerts** | Which of the alert types above the channel hears. If you select all of them, the channel also hears alert types that are added later |
| **Projects** | Limit the channel to these projects. Leave all unchecked for every project |
| **Environments** | Limit the channel to these environments, for example `production`. Leave all unchecked for every environment |
| **Also tell me when a problem is resolved** | On by default. You can turn it off for a channel that goes to a pager |
| **Paused** | Keeps the channel, but sends nothing |

The alerts that belong to the cluster itself have no project. These are the alerts about nodes, certificates, upgrades, the operator, a platform volume, or a pod outside the projects. These alerts go only to channels that are **not limited to projects**. This way, a channel for one team's project does not hear about the cluster's nodes. The environment filter limits the alerts about a service or a database, but it does not limit the cluster's alerts. So a channel for production still hears that a node went down.

A typical setup has two channels:

- A Slack channel that hears everything.
- An email channel for the person on call. This channel is limited to the **production** environment, and it does not hear **Build failed**.

## What a message looks like

A Slack message has these parts:

- A colored bar: red for critical, amber for warning, and green for resolved.
- A title, for example *Deploy failed*, with a count when there are several alerts.
- A line for each item, for example `web (Morning Beach / production)`.
- What the alert means, and what to do.
- A link to the dashboard, when the dashboard has a public address.

An email has the same content in its body, and a subject such as `[Geass] Deploy failed (2 alerts)`.

The messages say what is wrong and where. The dashboard says why: the reason that a build failed is in its log, and the reason that a deploy failed is on the service. The messages do not contain text from a service's own output.

Text that came from a person, for example a service's name, is made safe first. A service named `<!channel>` does not notify the whole Slack workspace.

## See what is happening

The page shows:

- Whether **Alertmanager** can be reached. If it cannot, nothing can be sent, and the page says so.
- **Firing now**: the alerts that Alertmanager holds and that it was not told are over. Each alert shows its project, how long it has fired, and what it is.
- A warning when Alertmanager failed to send requests to Slack or to the mail server since it started.

**Send test** puts a test alert into Alertmanager, the same way as a real alert. So the test proves the whole path, not only that the address is correct. The test can take up to one minute. Alertmanager must first load the channel, which takes a moment after you add or change a channel. Then Alertmanager reports whether it sent the message. Each test is a separate alert, so you can test the same channel again immediately. A test never goes to a channel other than the one that you selected. The test goes to that channel even if the channel is paused.

The dashboard reads a failure from Alertmanager's log, which names the channel that the failure is for. If the dashboard cannot read the log, it uses Alertmanager's counts. All channels that send through Slack or through the mail server share these counts. So a problem with another channel can show as a problem with this channel.

To silence an alert for a period, use Alertmanager. Alertmanager is not exposed outside the cluster:

```bash
kubectl -n monitoring port-forward svc/geass-kube-prometheus-stac-alertmanager 9093
```

Then open `http://localhost:9093`. Prometheus shows the alerts as it makes them. Prometheus also shows `geass_problem`, the metric behind most of the alerts, with the project, the environment, and the resource.

## Set it up as code

All the settings on the page are in `spec.alerting` of the [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/). The Slack URLs and the mail password stay in Secrets.

```yaml title="alerting.yaml"
apiVersion: v1
kind: Secret
metadata:
  name: geass-alert-channels
  namespace: geass-system
stringData:
  # The key is the channel's id.
  a1b2c3d4: https://hooks.slack.com/services/T000/B000/XXXX
---
apiVersion: v1
kind: Secret
metadata:
  name: ses-smtp
  namespace: geass-system
stringData:
  password: <smtp password>
---
apiVersion: geass.geass.dev/v1alpha1
kind: GeassPlatformConfig
metadata:
  name: platform
  namespace: geass-system
spec:
  alerting:
    smtp:
      host: email-smtp.eu-north-1.amazonaws.com
      port: 587
      security: starttls
      username: AKIAEXAMPLE
      credentialRef:
        name: ses-smtp
      from: Geass <alerts@example.com>
    channels:
      - id: a1b2c3d4
        name: "#ops-alerts"
        type: slack
      - id: e5f6a7b8
        name: On call
        type: email
        recipients: [oncall@example.com]
        environments: [production]
        events: [deploy.failed, service.down, backup.failed, node.unhealthy, cluster.issue]
        notifyResolved: false
```

Do not apply this manifest as a whole over a platform configuration that has other settings. `kubectl apply` of a partial manifest can drop fields. Use `kubectl patch`, or edit the object. The field reference is in [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/#spec-fields).

## Alerts for an organization

When [organizations](/docs/guides/organizations-and-workspaces/) are on, an organization has its own channels under **Settings** → **Alerts**. The platform's channels are under the platform's settings. These rules apply to an organization's channel:

- The channel hears only about the organization's projects: the projects that it names, or all of them, including projects made later. It never hears about the cluster, about another organization's projects, or about suspected abuse.
- Routing uses the projects that the organization has now. If a project is given to another organization, the channel stops hearing about that project immediately.
- The channel posts to Slack, to `hooks.slack.com` only. Mail goes out through the platform's mail server, from the platform's address. An organization cannot send through that mail server.
- An administrator of the organization creates, sees, tests, and deletes the channel. A member of one workspace manages the channels that name projects of that workspace. A channel for all of the organization's projects is for the organization's administrators.
- The channel never shows its address. Like every Slack address, the address is kept in a Secret, and the page shows only the host.
- The channel counts against the organization's own limit of 10 channels, not the platform's limit of 20.

The platform's channels that are not limited to projects still hear about every project. So the platform's administrators know what happens to every organization. The API is `GET /api/alerts`, and `POST /api/alerts/channels/save`, `/delete`, and `/test`. The platform's `/api/settings/alerts` is closed to an organization.

## How it works

Three components in the cluster do the work, in this order:

1. **The operator says what is wrong.** Every 30 seconds, the operator that holds the lease reads the status that its controllers already write. The operator exports one series of the metric `geass_problem` for each problem. A problem is one of these:

   - A build that failed.
   - A resource whose `Ready` condition says that Geass gave up.
   - A workload with nothing available.
   - A database whose backups are at risk.
   - A node that is not ready.
   - A cluster condition.

   The operator also exports which project owns each namespace, so that a crashing pod can be connected to a project. Prometheus scrapes these metrics with the operator's other metrics. An operator that has not checked for five minutes reports nothing, so its last problems do not stay open.
2. **Prometheus decides when it counts.** The `PrometheusRule` `geass-alerts` in `monitoring` changes those series into alerts. It also changes the Kubernetes metrics for crashing pods, and for filling volumes and disks, into alerts. Each alert waits as long as the table above says. Every alert has the labels that the channel filters match: `geass_kind`, `project`, `environment`, and `severity`.
3. **Alertmanager sends it.** Geass keeps one `AlertmanagerConfig`, `geass-alerts` in `geass-system`. Geass makes it from the channels that you set up, with a receiver and a route for each channel. The dashboard writes the `AlertmanagerConfig` immediately when you save. The operator corrects it if someone edits or deletes it, and when you change the settings with `kubectl`. If no channel can send, there is no `AlertmanagerConfig`. The Prometheus operator reads the `AlertmanagerConfig` and the Secrets that it refers to. Then the Prometheus operator gives Alertmanager its configuration.

Geass leaves a channel out of the configuration if the channel does not have what it needs to send. Examples are a Slack channel without its URL, and an email channel before the mail server is set up. This is deliberate. The Prometheus operator discards a whole `AlertmanagerConfig` that refers to a Secret key that it cannot find. One channel without its secret must not silence the other channels.

The monitoring chart has its own rules that make a warning give way to a critical alert of the same name. These rules do not apply to Geass's alerts. The rules compare the namespace, and Geass's alerts have no namespace. Without this exception, a critical failure in production would silence the warning about the same failure in staging.

If you run your own Alertmanager, set `GEASS_ALERTMANAGER_URL` on the operator to point the dashboard's view at it. The alerts carry their labels, so you can route them there yourself, by `geass_kind` and `project`.

## Limits and safety

- The platform can have up to 20 channels, and each organization can have up to 10 channels.
- An email channel can have up to 20 addresses.
- The filters of a channel can have up to 100 projects or environments.
- Only a signed-in admin can see or change alert settings. An API token cannot, not even an admin token. A token that could change where alerts go could also silence them.
- Alertmanager connects to Slack and to the mail server, not Geass. So the guard that [outbound webhooks](/docs/guides/github-and-builds/#outgoing-webhooks) have when they connect does not apply. Geass checks the host when you save. Geass refuses a host that is, or resolves to, a loopback address or a link-local address. Geass also refuses a private address, unless the operator runs with `GEASS_WEBHOOKS_ALLOW_PRIVATE_NETWORKS=true`. Geass cannot see a name that resolves to a different address later.
- A Slack address must use `https`.
- Geass never writes text that a person typed, for example a channel's name or an address, into a message template. Alertmanager runs message templates. For the same reason, Geass refuses an address that contains braces.

## Troubleshooting

| What you see | What it means |
| --- | --- |
| `Alertmanager could not be reached ... It is part of the monitoring add-on` | The monitoring add-on is off, or its Alertmanager is not running. Turn the add-on on (`spec.addons.monitoring.enabled`), and look at the output of `kubectl -n monitoring get pods` |
| `Saved. Nothing can be sent until the monitoring add-on is installed` | The cluster does not have the `AlertmanagerConfig` kind yet. The add-on brings that kind. Geass keeps what you saved, and it works after the kind is there. The page shows this message as a notice, not as a failure |
| `Alertmanager has not loaded this channel yet` | A test was sent before Alertmanager had the new channel. Alertmanager takes up to one minute to load the channel. Try again |
| `Alertmanager could not post to Slack` | The webhook URL was removed or is wrong, or the `monitoring` namespace cannot reach Slack. Alertmanager's log has Slack's answer: `kubectl -n monitoring logs alertmanager-geass-kube-prometheus-stac-alertmanager-0 -c alertmanager` |
| `Alertmanager could not send the email` | The mail server's login is wrong, or the server refused the sender, or the `monitoring` namespace cannot reach the server. The server refuses the sender when the From address or its domain is not verified with your provider. Alertmanager's log has the server's answer |
| `Alertmanager has not reported sending it yet` | The test is still on its way, or it is stuck. Look in the channel after one minute |
| `No Slack webhook URL is saved for this channel.` | The channel exists, but its secret does not. Edit the channel and paste the URL again |
| `Set up the mail server first.` | An email channel needs the mail server under **Mail server** |
| `port 465 is encrypted from the first byte` / `TLS from the start is only used on port 465` | Alertmanager connects encrypted from the first byte only on port 465. Use STARTTLS on port 587, or TLS on port 465 |
| `a password cannot be sent over a connection that is not encrypted` | Alertmanager does not send a password in clear text. Select STARTTLS or TLS, or remove the username |
| `... is a private network address, which cannot be used` | The host is on a private network. See the limits above |
| `could not connect to host:port: connect: connection refused` (or `i/o timeout`) | **Test connection** runs from the operator, and it could not reach the mail server. The host or the port is wrong, or a firewall blocks outbound mail |
| `does not offer STARTTLS` | The port does not support STARTTLS. Try TLS from the start with port 465, or the port that the provider documents |
| `could not set up TLS ... certificate ...` | The mail server's certificate is not trusted, or the certificate is for a name other than the host that you entered |
| `the mail server refused the username and password` | The SMTP credentials are wrong. Many providers use SMTP credentials that are different from the account's password |
| Nothing arrives and **Firing now** is empty | Nothing is wrong, or Prometheus is not loading the rules. Run `kubectl -n monitoring get prometheusrule geass-alerts`, and look at the **Rules** page of Prometheus |
| Something is firing and nothing arrives | The channel's filters leave the alert out, or Alertmanager failed to send it (see the warning on the page), or the alert is silenced in Alertmanager |

## Related

- [Logs, metrics and terminal](/docs/guides/observability/#metrics-and-alerts-for-the-operator): the monitoring add-on, and the alerts on the operator's own health. These channels treat those alerts as cluster problems.
- [GitHub and builds](/docs/guides/github-and-builds/): what Geass reports to GitHub, and outgoing webhooks for deployments.
- [GeassPlatformConfig](/docs/reference/resources/geass-platform-config/)
